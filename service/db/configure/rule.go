package configure

import (
	"github.com/ProxyStation/proxystation/db"
)

// RuleAction 规则动作
type RuleAction string

const (
	RuleActionDirect  RuleAction = "direct"
	RuleActionBlock   RuleAction = "block"
	RuleActionOutbound RuleAction = "outbound" // 指定出站
)

// RoutingRule 用户自定义路由规则
type RoutingRule struct {
	ID          string     `json:"id"`
	Enabled     bool       `json:"enabled"`
	Name        string     `json:"name"`
	// 匹配条件（多个条件 AND 关系）
	InboundTags []string   `json:"inboundTags,omitempty"` // 入站标签
	Domains     []string   `json:"domains,omitempty"`     // 域名，支持 geosite:xxx, domain:xxx, regexp:xxx, keyword:xxx
	IPs         []string   `json:"ips,omitempty"`         // IP/CIDR，支持 geoip:xxx
	Ports       string     `json:"ports,omitempty"`       // 端口范围，如 "80,443,8000-9000"
	Protocol    string     `json:"protocol,omitempty"`    // "tcp" | "udp" | "tcp,udp"
	// 动作
	Action      RuleAction `json:"action"`
	OutboundName string    `json:"outboundName,omitempty"` // action=outbound 时指定出站名
}

func GetRoutingRules() []RoutingRule {
	var rules []RoutingRule
	_ = db.Get("system", "routing_rules", &rules)
	if rules == nil {
		rules = []RoutingRule{}
	}
	return rules
}

func SetRoutingRules(rules []RoutingRule) error {
	return db.Set("system", "routing_rules", rules)
}
