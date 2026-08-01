package configure

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ProxyStation/proxystation/db"
)

// SubscriptionFormat 订阅格式类型
type SubscriptionFormat string

const (
	FormatV2ray   SubscriptionFormat = "v2ray"   // base64 编码的节点链接列表
	FormatClash   SubscriptionFormat = "clash"   // Clash YAML 格式
	FormatSingbox SubscriptionFormat = "singbox" // sing-box JSON 格式
	FormatAuto    SubscriptionFormat = "auto"    // 自动检测
)

// SubscriptionRaw 代表一个订阅源
type SubscriptionRaw struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	URL       string             `json:"url"`
	Format    SubscriptionFormat `json:"format"`
	Servers   []ServerRaw        `json:"servers"`
	UpdatedAt time.Time          `json:"updatedAt"`
	// 对应自动创建的 Group ID
	GroupID string `json:"groupId"`
	// 拉取订阅时使用的代理出站名，为空表示直连拉取
	ProxyOutbound string `json:"proxyOutbound,omitempty"`
}

func Bytes2SubscriptionRaw(b []byte) (*SubscriptionRaw, error) {
	var s SubscriptionRaw
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("Bytes2SubscriptionRaw: %w", err)
	}
	return &s, nil
}

func GetSubscriptions() []SubscriptionRaw {
	result := make([]SubscriptionRaw, 0)
	raw, err := db.ListGetAll("touch", "subscriptions")
	if err != nil {
		return result
	}
	for _, b := range raw {
		s, err := Bytes2SubscriptionRaw(b)
		if err != nil {
			continue
		}
		result = append(result, *s)
	}
	return result
}

func GetSubscription(index int) *SubscriptionRaw {
	b, err := db.ListGet("touch", "subscriptions", index)
	if err != nil {
		return nil
	}
	s, _ := Bytes2SubscriptionRaw(b)
	return s
}

func AppendSubscriptions(subs []*SubscriptionRaw) error {
	return db.ListAppend("touch", "subscriptions", subs)
}

func SetSubscription(index int, sub *SubscriptionRaw) error {
	return db.ListSet("touch", "subscriptions", index, sub)
}

func RemoveSubscriptions(indexes []int) error {
	return db.ListRemove("touch", "subscriptions", indexes)
}

func GetLenSubscriptions() int {
	l, _ := db.ListLen("touch", "subscriptions")
	return l
}

func GetLenSubscriptionServers(index int) int {
	sub := GetSubscription(index)
	if sub == nil {
		return 0
	}
	return len(sub.Servers)
}

// PreserveServerProbeResults copies latency metadata from old subscription nodes
// to newly parsed nodes when they represent the same server.
func PreserveServerProbeResults(oldServers, newServers []ServerRaw) {
	if len(oldServers) == 0 || len(newServers) == 0 {
		return
	}
	byKey := make(map[string]ServerRaw, len(oldServers)*2)
	for _, old := range oldServers {
		for _, key := range serverProbeKeys(old) {
			if _, ok := byKey[key]; !ok {
				byKey[key] = old
			}
		}
	}
	for i := range newServers {
		for _, key := range serverProbeKeys(newServers[i]) {
			if old, ok := byKey[key]; ok {
				newServers[i].Latency = old.Latency
				newServers[i].LastProbeTime = old.LastProbeTime
				// 订阅刷新会整体覆盖节点，用户手动设的前置代理需要跟着保留
				newServers[i].FrontProxy = old.FrontProxy
				break
			}
		}
	}
}

// PreserveActiveSubscriptionServers keeps subscription nodes that are currently
// selected by an outbound when a refreshed subscription no longer contains them.
// It also remaps active outbound references when the same node moves index.
func PreserveActiveSubscriptionServers(subIndex int, oldServers, newServers []ServerRaw) []ServerRaw {
	if subIndex < 0 || len(oldServers) == 0 {
		return newServers
	}
	oldToNew := matchOldServerIndexes(oldServers, newServers)
	appended := map[int]int{}

	for _, name := range GetOutboundNames() {
		o := GetOutbound(name)
		if o == nil {
			continue
		}
		changed := false
		if remapActiveSubscriptionRef(o.Target.NodeRef, subIndex, oldServers, &newServers, oldToNew, appended) {
			changed = true
		}
		if remapActiveSubscriptionRef(o.Target.ActiveNodeRef, subIndex, oldServers, &newServers, oldToNew, appended) {
			changed = true
		}
		if changed {
			_ = SetOutbound(name, o)
		}
	}

	return newServers
}

func remapActiveSubscriptionRef(ref *NodeRef, subIndex int, oldServers []ServerRaw, newServers *[]ServerRaw, oldToNew map[int]int, appended map[int]int) bool {
	if ref == nil || ref.Type != "sub_server" || ref.Sub != subIndex {
		return false
	}
	if ref.Index < 0 || ref.Index >= len(oldServers) {
		return false
	}
	if newIndex, ok := oldToNew[ref.Index]; ok {
		if ref.Index == newIndex {
			return false
		}
		ref.Index = newIndex
		return true
	}
	if newIndex, ok := appended[ref.Index]; ok {
		if ref.Index == newIndex {
			return false
		}
		ref.Index = newIndex
		return true
	}
	newIndex := len(*newServers)
	*newServers = append(*newServers, oldServers[ref.Index])
	appended[ref.Index] = newIndex
	ref.Index = newIndex
	return true
}

func matchOldServerIndexes(oldServers, newServers []ServerRaw) map[int]int {
	newIndexByKey := make(map[string]int, len(newServers)*2)
	for i, server := range newServers {
		for _, key := range serverProbeKeys(server) {
			if _, ok := newIndexByKey[key]; !ok {
				newIndexByKey[key] = i
			}
		}
	}
	result := map[int]int{}
	for i, server := range oldServers {
		for _, key := range serverProbeKeys(server) {
			if newIndex, ok := newIndexByKey[key]; ok {
				result[i] = newIndex
				break
			}
		}
	}
	return result
}

func serverProbeKeys(server ServerRaw) []string {
	keys := make([]string, 0, 2)
	if link := strings.TrimSpace(server.Link); link != "" {
		keys = append(keys, "link:"+link)
	}
	serverKey := strings.ToLower(strings.TrimSpace(server.Type)) + "|" +
		strings.ToLower(strings.TrimSpace(server.Host)) + "|" +
		fmt.Sprintf("%d", server.Port) + "|" +
		strings.TrimSpace(server.Name)
	if serverKey != "||0|" {
		keys = append(keys, "server:"+serverKey)
	}
	return keys
}
