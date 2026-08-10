package configure

import (
	"fmt"
	"strings"

	"github.com/ProxyStation/proxystation/db"
)

// SelectionMode 出站节点选择模式
type SelectionMode string

const (
	// ModeManual 手动指定某个节点
	ModeManual SelectionMode = "manual"
	// ModeLeastPing 自动选择分组内延迟最低的节点
	ModeLeastPing SelectionMode = "leastping"
	// ModeStable 稳定优先：当前节点仍连通就不切换，延迟再高也保持
	ModeStable SelectionMode = "stable"
)

// OutboundTarget 出站绑定的目标，可以是单个节点或一个分组
type OutboundTarget struct {
	// "node" | "group" | "special"
	TargetType string `json:"targetType"`

	// TargetType == "node" 时有效
	NodeRef *NodeRef `json:"nodeRef,omitempty"`

	// TargetType == "group" 时有效
	GroupID string        `json:"groupId,omitempty"`
	Mode    SelectionMode `json:"mode,omitempty"`

	// 当前实际使用的节点（group 模式下由 observatory 维护）
	ActiveNodeRef *NodeRef `json:"activeNodeRef,omitempty"`

	// TargetType == "special" 时有效
	SpecialType string             `json:"specialType,omitempty"` // "cfgoodnet" | "cfdo"
	CfGoodNet   *CfGoodNetOutbound `json:"cfgoodnet,omitempty"`
	CfDO        *CfDOOutbound      `json:"cfdo,omitempty"`
}

type CfGoodNetRule struct {
	Pattern string `json:"pattern"`
	Action  string `json:"action"` // "direct" | "reject" | "cf_proxy"
}

type CfGoodNetOutbound struct {
	InboundID  string          `json:"inboundId,omitempty"` // 可选：引用 cfgoodnet 自定义入站
	ListenHost string          `json:"listenHost,omitempty"`
	ListenPort int             `json:"listenPort,omitempty"`
	CfProxy    string          `json:"cfProxy,omitempty"`
	CfGoodIP   string          `json:"cfGoodIp,omitempty"`
	EnableXFF  bool            `json:"enableXff,omitempty"`
	Rules      []CfGoodNetRule `json:"rules,omitempty"`
}

type CfDOOutbound struct {
	// 可选：引用已有的 cfdo 类型自定义入站 ID
	InboundID string `json:"inboundId,omitempty"`

	ListenHost   string         `json:"listenHost,omitempty"` // 强制归一到 127.0.0.1
	ListenPort   int            `json:"listenPort,omitempty"` // 0 表示自动选端口
	Listeners    []CfDOListener `json:"listeners,omitempty"`  // 多监听端口与独立优选地址
	WorkerDomain string         `json:"workerDomain,omitempty"`
	Secret       string         `json:"secret,omitempty"`
	Path         string         `json:"path,omitempty"` // 默认 /api/tcp
	WorkerIP     string         `json:"workerIp,omitempty"`
	UseBareWS    bool           `json:"useBareWs,omitempty"`
}

// NodeRef 指向一个具体节点
type NodeRef struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Sub   int    `json:"sub"` // 不用 omitempty，sub=0 也要序列化
}

// Outbound 代表一个出站
type Outbound struct {
	// 出站名称，"proxy" 是默认出站，用户可以自定义添加
	Name   string         `json:"name"`
	Target OutboundTarget `json:"target"`
	// 测速配置
	ProbeURL      string `json:"probeURL"`
	ProbeInterval string `json:"probeInterval"`
	// 分组周期测速方式：real | fast。空值回退到设置里的默认测速方式
	ProbeMode string `json:"probeMode,omitempty"`
	// 分组自动切换参数。旧配置为空时由绑定接口补齐默认值。
	SwitchThresholdMs *int `json:"switchThresholdMs,omitempty"`
	SwitchCooldownSec *int `json:"switchCooldownSec,omitempty"`
}

func GetOutbound(name string) *Outbound {
	if name == "" {
		name = "proxy"
	}
	var o Outbound
	if err := db.Get(fmt.Sprintf("outbound.%v", name), "config", &o); err != nil {
		return nil
	}
	return &o
}

func SetOutbound(name string, o *Outbound) error {
	if name == "" {
		name = "proxy"
	}
	return db.Set(fmt.Sprintf("outbound.%v", name), "config", o)
}

func GetOutboundNames() []string {
	names, _ := db.GetBucketKeys("outbounds")
	result := []string{"proxy"}
	for _, n := range names {
		if n != "proxy" {
			result = append(result, n)
		}
	}
	return result
}

// reservedOutboundNames 内核配置里已占用的 tag。"__probe__" 对应
// engine.ProbeOutboundName，这里写字面量以避免 configure -> engine 的循环依赖。
var reservedOutboundNames = map[string]bool{
	"proxy":     true,
	"direct":    true,
	"block":     true,
	"__probe__": true,
}

// ValidateOutboundName 出站名既是内核配置里的 outbound tag，也是 REST 路径的一段，
// 所以要挡掉会破坏路径或配置引用的字符。非 ASCII 名字（如中文）是允许的。
func ValidateOutboundName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("出站名称不能为空")
	}
	if len([]rune(name)) > 64 {
		return fmt.Errorf("出站名称不能超过 64 个字符")
	}
	if reservedOutboundNames[strings.ToLower(name)] {
		return fmt.Errorf("cannot use reserved name: %v", name)
	}
	if strings.ContainsAny(name, "/\\?#%\"'`") || strings.ContainsAny(name, " \t\r\n") {
		return fmt.Errorf("出站名称不能包含空白字符或 / \\ ? # %% \" ' `")
	}
	return nil
}

func AddOutbound(name string) error {
	if err := ValidateOutboundName(name); err != nil {
		return err
	}
	return db.Set("outbounds", strings.TrimSpace(name), true)
}

// RenameOutbound 给出站改名，并把所有引用旧名字的配置一起迁移过去。
// "proxy" 是内核配置里硬编码的默认出站 tag（route.final / 兜底规则都指向它），改名会
// 让生成的配置失去出口，因此不允许。
func RenameOutbound(oldName, newName string) error {
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" {
		return fmt.Errorf("出站名称不能为空")
	}
	if oldName == "proxy" {
		return fmt.Errorf("默认出站 proxy 不能改名")
	}
	if err := ValidateOutboundName(newName); err != nil {
		return err
	}
	if oldName == newName {
		return nil
	}
	if !db.Exists("outbounds", oldName) {
		return fmt.Errorf("出站 %v 不存在", oldName)
	}
	if db.Exists("outbounds", newName) {
		return fmt.Errorf("出站 %v 已存在", newName)
	}

	o := GetOutbound(oldName)
	if o == nil {
		o = &Outbound{Name: newName}
	}
	o.Name = newName
	if err := db.Set("outbounds", newName, true); err != nil {
		return err
	}
	if err := SetOutbound(newName, o); err != nil {
		return err
	}
	if err := db.BucketClear(fmt.Sprintf("outbound.%v", oldName)); err != nil {
		return err
	}
	if err := db.Set("outbounds", oldName, nil); err != nil {
		return err
	}
	RetargetOutboundReferences(oldName, newName)
	return nil
}

// RetargetOutboundReferences 把节点前置代理、订阅拉取代理、路由规则里的出站名从
// oldName 改成 newName。漏掉任何一处都会让对应功能静默失效（前置代理被当成不存在
// 而忽略，路由规则匹配不到出站）。
func RetargetOutboundReferences(oldName, newName string) {
	servers := GetServers()
	serversChanged := false
	for i := range servers {
		if servers[i].FrontProxy == oldName {
			servers[i].FrontProxy = newName
			serversChanged = true
		}
	}
	if serversChanged {
		_ = SetServers(servers)
	}

	for i := 0; i < GetLenSubscriptions(); i++ {
		sub := GetSubscription(i)
		if sub == nil {
			continue
		}
		changed := false
		if sub.ProxyOutbound == oldName {
			sub.ProxyOutbound = newName
			changed = true
		}
		for j := range sub.Servers {
			if sub.Servers[j].FrontProxy == oldName {
				sub.Servers[j].FrontProxy = newName
				changed = true
			}
		}
		if changed {
			_ = SetSubscription(i, sub)
		}
	}

	rules := GetRoutingRules()
	rulesChanged := false
	for i := range rules {
		if rules[i].OutboundName == oldName {
			rules[i].OutboundName = newName
			rulesChanged = true
		}
	}
	if rulesChanged {
		_ = SetRoutingRules(rules)
	}
}

func RemoveOutbound(name string) error {
	if err := db.BucketClear(fmt.Sprintf("outbound.%v", name)); err != nil {
		return err
	}
	return db.Set("outbounds", name, nil)
}

// SetOutboundActiveNode 由 observatory 调用，更新当前活跃节点
func SetOutboundActiveNode(name string, ref *NodeRef) error {
	o := GetOutbound(name)
	if o == nil {
		return fmt.Errorf("outbound %v not found", name)
	}
	o.Target.ActiveNodeRef = ref
	return SetOutbound(name, o)
}
