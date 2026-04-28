package configure

import (
	"fmt"

	"github.com/ProxyStation/proxystation/db"
)

// SelectionMode 出站节点选择模式
type SelectionMode string

const (
	// ModeManual 手动指定某个节点
	ModeManual    SelectionMode = "manual"
	// ModeLeastPing 自动选择分组内延迟最低的节点
	ModeLeastPing SelectionMode = "leastping"
	// ModeRoundRobin 轮询分组内节点
	ModeRoundRobin SelectionMode = "roundrobin"
)

// OutboundTarget 出站绑定的目标，可以是单个节点或一个分组
type OutboundTarget struct {
	// "node" 或 "group"
	TargetType string `json:"targetType"`

	// TargetType == "node" 时有效
	NodeRef *NodeRef `json:"nodeRef,omitempty"`

	// TargetType == "group" 时有效
	GroupID string        `json:"groupId,omitempty"`
	Mode    SelectionMode `json:"mode,omitempty"`

	// 当前实际使用的节点（group 模式下由 observatory 维护）
	ActiveNodeRef *NodeRef `json:"activeNodeRef,omitempty"`
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

func AddOutbound(name string) error {
	if name == "proxy" || name == "direct" || name == "block" {
		return fmt.Errorf("cannot use reserved name: %v", name)
	}
	return db.Set("outbounds", name, true)
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
