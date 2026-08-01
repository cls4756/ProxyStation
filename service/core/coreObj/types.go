// coreObj 定义 v2ray-core config.json 的数据结构
// 参考 v2rayA/service/core/coreObj，精简为 ProxyStation 所需的部分
package coreObj

// Log v2ray 日志配置
type Log struct {
	Loglevel string `json:"loglevel,omitempty"`
	Access   string `json:"access,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Inbound 入站配置
type Inbound struct {
	Tag      string           `json:"tag,omitempty"`
	Port     int              `json:"port"`
	Listen   string           `json:"listen,omitempty"`
	Protocol string           `json:"protocol"`
	Settings *InboundSettings `json:"settings,omitempty"`
	Sniffing Sniffing         `json:"sniffing,omitempty"`
}

type InboundSettings struct {
	Auth    string `json:"auth,omitempty"`
	UDP     bool   `json:"udp,omitempty"`
	Network string `json:"network,omitempty"`
	Address string `json:"address,omitempty"`
	Port    int    `json:"port,omitempty"`
	FollowRedirect bool `json:"followRedirect,omitempty"`
	// 认证用户列表（socks 和 http）
	Accounts []InboundAccount `json:"accounts,omitempty"`
}

type InboundAccount struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

type Sniffing struct {
	Enabled      bool     `json:"enabled"`
	DestOverride []string `json:"destOverride,omitempty"`
}

// OutboundObject 出站配置
type OutboundObject struct {
	Tag            string          `json:"tag,omitempty"`
	Protocol       string          `json:"protocol"`
	Settings       OutboundSettings `json:"settings,omitempty"`
	StreamSettings *StreamSettings `json:"streamSettings,omitempty"`
	Mux            *Mux            `json:"mux,omitempty"`
}

type OutboundSettings struct {
	// vmess/vless
	Vnext []VnextObject `json:"vnext,omitempty"`
	// ss/trojan
	Servers []ServerObject `json:"servers,omitempty"`
	// freedom
	DomainStrategy string `json:"domainStrategy,omitempty"`
}

type VnextObject struct {
	Address string       `json:"address"`
	Port    int          `json:"port"`
	Users   []VnextUser  `json:"users"`
}

type VnextUser struct {
	ID         string `json:"id"`
	AlterID    int    `json:"alterId,omitempty"`
	Security   string `json:"security,omitempty"`
	Encryption string `json:"encryption,omitempty"`
	Flow       string `json:"flow,omitempty"`
	Level      int    `json:"level,omitempty"`
}

type ServerObject struct {
	Address  string       `json:"address"`
	Port     int          `json:"port"`
	Password string       `json:"password,omitempty"`
	Method   string       `json:"method,omitempty"`
	Level    int          `json:"level,omitempty"`
	Users    []SocksUser  `json:"users,omitempty"`
}

type SocksUser struct {
	User  string `json:"user,omitempty"`
	Pass  string `json:"pass,omitempty"`
	Level int    `json:"level,omitempty"`
}

type StreamSettings struct {
	Network    string      `json:"network,omitempty"`
	Security   string      `json:"security,omitempty"`
	TLSSettings *TLSObject `json:"tlsSettings,omitempty"`
	RealitySettings *RealityObject `json:"realitySettings,omitempty"`
	WSSettings *WSObject   `json:"wsSettings,omitempty"`
	GRPCSettings *GRPCObject `json:"grpcSettings,omitempty"`
	TCPSettings *TCPObject  `json:"tcpSettings,omitempty"`
	XHTTPSettings *XHTTPObject `json:"xhttpSettings,omitempty"`
	Sockopt    *Sockopt     `json:"sockopt,omitempty"`
}

type TLSObject struct {
	ServerName    string   `json:"serverName,omitempty"`
	AllowInsecure bool     `json:"allowInsecure,omitempty"`
	Fingerprint   string   `json:"fingerprint,omitempty"`
	ALPN          []string `json:"alpn,omitempty"`
}

type RealityObject struct {
	ServerName  string `json:"serverName,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	PublicKey   string `json:"publicKey,omitempty"`
	ShortID     string `json:"shortId,omitempty"`
	SpiderX     string `json:"spiderX,omitempty"`
}

type WSObject struct {
	Path    string            `json:"path,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type GRPCObject struct {
	ServiceName string `json:"serviceName,omitempty"`
	MultiMode   bool   `json:"multiMode,omitempty"`
}

type XHTTPObject struct {
	Host string `json:"host,omitempty"`
	Path string `json:"path,omitempty"`
}

type TCPObject struct {
	Header *TCPHeader `json:"header,omitempty"`
}

type TCPHeader struct {
	Type    string      `json:"type,omitempty"`
	Request interface{} `json:"request,omitempty"`
}

type Sockopt struct {
	Mark        *int   `json:"mark,omitempty"`
	TCPFastOpen *bool  `json:"tcpFastOpen,omitempty"`
	Tproxy      string `json:"tproxy,omitempty"`
	// DialerProxy 前置代理出站 tag，本出站的连接先经由该出站建立
	DialerProxy string `json:"dialerProxy,omitempty"`
}

type Mux struct {
	Enabled     bool `json:"enabled"`
	Concurrency int  `json:"concurrency,omitempty"`
}

// Routing 路由配置
type Routing struct {
	DomainStrategy string        `json:"domainStrategy"`
	Rules          []RoutingRule `json:"rules"`
}

type RoutingRule struct {
	Type        string   `json:"type"`
	OutboundTag string   `json:"outboundTag,omitempty"`
	InboundTag  []string `json:"inboundTag,omitempty"`
	Domain      []string `json:"domain,omitempty"`
	IP          []string `json:"ip,omitempty"`
	Port        string   `json:"port,omitempty"`
	Network     string   `json:"network,omitempty"`
}

// DNS 配置
type DNS struct {
	Servers []interface{} `json:"servers,omitempty"`
	Hosts   map[string]string `json:"hosts,omitempty"`
}

// Config v2ray-core 完整配置
type Config struct {
	Log       *Log             `json:"log,omitempty"`
	Inbounds  []Inbound        `json:"inbounds"`
	Outbounds []OutboundObject `json:"outbounds"`
	Routing   Routing          `json:"routing"`
	DNS       *DNS             `json:"dns,omitempty"`
}
