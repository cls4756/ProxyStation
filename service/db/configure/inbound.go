package configure

import (
	"github.com/ProxyStation/proxystation/db"
)

// CustomInbound 用户自定义入站
type CustomInbound struct {
	ID       string `json:"id"`
	Tag      string `json:"tag"`      // 入站标签，用于路由规则匹配
	Name     string `json:"name"`     // 显示名称
	Protocol string `json:"protocol"` // "socks" | "http" | "dokodemo-door"
	Listen   string `json:"listen"`   // 监听地址，默认 127.0.0.1
	Port     int    `json:"port"`
	// socks 入站选项
	UDPEnabled bool   `json:"udpEnabled,omitempty"`
	// dokodemo-door 透明代理选项
	Network    string `json:"network,omitempty"` // "tcp" | "udp" | "tcp,udp"
	FollowRedirect bool `json:"followRedirect,omitempty"`
	// 嗅探
	SniffEnabled bool     `json:"sniffEnabled,omitempty"`
	SniffDest    []string `json:"sniffDest,omitempty"` // ["http","tls","quic"]
	// 认证（socks 和 http 支持）
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

func GetCustomInbounds() []CustomInbound {
	var inbounds []CustomInbound
	_ = db.Get("system", "custom_inbounds", &inbounds)
	if inbounds == nil {
		inbounds = []CustomInbound{}
	}
	return inbounds
}

func SetCustomInbounds(inbounds []CustomInbound) error {
	return db.Set("system", "custom_inbounds", inbounds)
}
