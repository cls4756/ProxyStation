package configure

import (
	"encoding/json"
	"fmt"
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
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	URL         string             `json:"url"`
	Format      SubscriptionFormat `json:"format"`
	Servers     []ServerRaw        `json:"servers"`
	UpdatedAt   time.Time          `json:"updatedAt"`
	// 对应自动创建的 Group ID
	GroupID     string             `json:"groupId"`
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
