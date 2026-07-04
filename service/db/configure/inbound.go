package configure

import (
	"github.com/ProxyStation/proxystation/db"
)

// CustomInbound 用户自定义入站
type CustomInbound struct {
	ID       string `json:"id"`
	Tag      string `json:"tag"`      // 入站标签，用于路由规则匹配
	Name     string `json:"name"`     // 显示名称
	Protocol string `json:"protocol"` // "socks" | "http" | "dokodemo-door" | "cfdo"
	Listen   string `json:"listen"`   // 监听地址，默认 127.0.0.1
	Port     int    `json:"port"`
	// socks 入站选项
	UDPEnabled bool `json:"udpEnabled,omitempty"`
	// dokodemo-door 透明代理选项
	Network        string `json:"network,omitempty"` // "tcp" | "udp" | "tcp,udp"
	FollowRedirect bool   `json:"followRedirect,omitempty"`
	// 嗅探
	SniffEnabled bool     `json:"sniffEnabled,omitempty"`
	SniffDest    []string `json:"sniffDest,omitempty"` // ["http","tls","quic"]
	// 认证（socks 和 http 支持）
	Username  string            `json:"username,omitempty"` // 兼容旧配置：单账号
	Password  string            `json:"password,omitempty"` // 兼容旧配置：单账号
	Accounts  []InboundAccount  `json:"accounts,omitempty"`
	CfDO      *CfDOInbound      `json:"cfdo,omitempty"`
	CfGoodNet *CfGoodNetInbound `json:"cfgoodnet,omitempty"`
}

type InboundAccount struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type CfDOInbound struct {
	WorkerDomain         string   `json:"workerDomain,omitempty"`
	Secret               string   `json:"secret,omitempty"`
	Path                 string   `json:"path,omitempty"`
	Port                 int      `json:"port,omitempty"` // 0 表示自动分配
	WorkerIP             string   `json:"workerIp,omitempty"`
	Listeners            []CfDOListener `json:"listeners,omitempty"` // 多监听端口与独立优选地址
	UseBareWS            bool     `json:"useBareWs,omitempty"`
	AlwaysUseDO          bool     `json:"alwaysUseDo,omitempty"`
	DOPoolSize           int      `json:"doPoolSize,omitempty"` // 0 表示不启用分片复用
	RejectDomains        []string `json:"rejectDomains,omitempty"`
	DOFallbackDomains    []string `json:"doFallbackDomains,omitempty"`
	DOFallbackExtensions []string `json:"doFallbackExtensions,omitempty"`
}

type CfDOListener struct {
	ListenPort int    `json:"listenPort,omitempty"` // 0 表示自动分配
	WorkerIP   string `json:"workerIp,omitempty"`   // 可填 IP 或域名；为空则该监听不使用优选地址
}

type CfGoodNetInbound struct {
	ListenHost string          `json:"listenHost,omitempty"`
	ListenPort int             `json:"listenPort,omitempty"` // 0 表示自动分配
	CfProxy    string          `json:"cfProxy,omitempty"`
	CfGoodIP   string          `json:"cfGoodIp,omitempty"`
	EnableXFF  bool            `json:"enableXff,omitempty"`
	Rules      []CfGoodNetRule `json:"rules,omitempty"`
}

// AuthAccounts 返回可用账号列表，优先使用 accounts；若为空则回退到旧字段 username/password。
func (c CustomInbound) AuthAccounts() []InboundAccount {
	accounts := make([]InboundAccount, 0, len(c.Accounts))
	for _, a := range c.Accounts {
		if a.Username == "" && a.Password == "" {
			continue
		}
		accounts = append(accounts, a)
	}
	if len(accounts) > 0 {
		return accounts
	}
	if c.Username != "" || c.Password != "" {
		return []InboundAccount{{Username: c.Username, Password: c.Password}}
	}
	return nil
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
