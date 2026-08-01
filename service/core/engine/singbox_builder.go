package engine

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/ProxyStation/proxystation/db/configure"
)

// SingboxConfig sing-box 完整配置结构
type SingboxConfig struct {
	Log          *SingboxLog          `json:"log,omitempty"`
	DNS          *SingboxDNS          `json:"dns,omitempty"`
	Inbounds     []SingboxInbound     `json:"inbounds"`
	Outbounds    []SingboxOutbound    `json:"outbounds"`
	Route        *SingboxRoute        `json:"route,omitempty"`
	Experimental *SingboxExperimental `json:"experimental,omitempty"`
}

type SingboxExperimental struct {
	CacheFile *SingboxCacheFile `json:"cache_file,omitempty"`
}

type SingboxCacheFile struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path,omitempty"`
}

type SingboxLog struct {
	Level    string `json:"level,omitempty"`
	Output   string `json:"output,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
}

type SingboxDNS struct {
	Servers  []SingboxDNSServer `json:"servers,omitempty"`
	Rules    []SingboxDNSRule   `json:"rules,omitempty"`
	Final    string             `json:"final,omitempty"`
	Strategy string             `json:"strategy,omitempty"`
}

// SingboxDNSServer sing-box 1.12+ 新格式，用 type+server 替代 address
type SingboxDNSServer struct {
	Type           string `json:"type"`
	Tag            string `json:"tag,omitempty"`
	Server         string `json:"server,omitempty"`
	DomainResolver string `json:"domain_resolver,omitempty"`
	Detour         string `json:"detour,omitempty"`
}

type SingboxDNSRule struct {
	GeoSite []string `json:"geosite,omitempty"`
	RuleSet []string `json:"rule_set,omitempty"`
	Server  string   `json:"server,omitempty"`
}

type SingboxInboundUser struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type SingboxInbound struct {
	Type       string `json:"type"`
	Tag        string `json:"tag"`
	Listen     string `json:"listen"`
	ListenPort int    `json:"listen_port"`
	// 认证（socks 和 http 用 users 数组）
	Users []SingboxInboundUser `json:"users,omitempty"`
}

type SingboxOutbound struct {
	Type string `json:"type"`
	Tag  string `json:"tag"`
	// 通用字段
	Server     string `json:"server,omitempty"`
	ServerPort int    `json:"server_port,omitempty"`
	// vmess
	UUID     string `json:"uuid,omitempty"`
	Security string `json:"security,omitempty"`
	AlterID  int    `json:"alter_id,omitempty"`
	// vless
	Flow string `json:"flow,omitempty"`
	// ss
	Method   string `json:"method,omitempty"`
	Password string `json:"password,omitempty"`
	// hysteria2
	UpMbps   int `json:"up_mbps,omitempty"`
	DownMbps int `json:"down_mbps,omitempty"`
	// tuic
	CongestionControl string `json:"congestion_control,omitempty"`
	// wireguard
	PrivateKey    string          `json:"private_key,omitempty"`
	PeerPublicKey string          `json:"peer_public_key,omitempty"`
	Peers         []SingboxWGPeer `json:"peers,omitempty"`
	// TLS
	TLS *SingboxTLS `json:"tls,omitempty"`
	// Transport
	Transport *SingboxTransport `json:"transport,omitempty"`
	// multiplex
	Multiplex *SingboxMux `json:"multiplex,omitempty"`
	// socks/http
	Username string `json:"username,omitempty"`
	// detour (for chaining)
	Detour string `json:"detour,omitempty"`
}

type SingboxTLS struct {
	Enabled    bool            `json:"enabled"`
	ServerName string          `json:"server_name,omitempty"`
	Insecure   bool            `json:"insecure,omitempty"`
	ALPN       []string        `json:"alpn,omitempty"`
	UTLS       *SingboxUTLS    `json:"utls,omitempty"`
	Reality    *SingboxReality `json:"reality,omitempty"`
}

type SingboxUTLS struct {
	Enabled     bool   `json:"enabled"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

type SingboxReality struct {
	Enabled   bool   `json:"enabled"`
	PublicKey string `json:"public_key,omitempty"`
	ShortID   string `json:"short_id,omitempty"`
}

type SingboxTransport struct {
	Type        string            `json:"type"`
	Path        string            `json:"path,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	ServiceName string            `json:"service_name,omitempty"`
	Host        []string          `json:"host,omitempty"`
}

type SingboxMux struct {
	Enabled  bool   `json:"enabled"`
	Protocol string `json:"protocol,omitempty"`
}

type SingboxWGPeer struct {
	PublicKey  string   `json:"public_key"`
	AllowedIPs []string `json:"allowed_ips,omitempty"`
	Endpoint   string   `json:"endpoint,omitempty"`
}

type SingboxRoute struct {
	Rules                 []SingboxRouteRule `json:"rules,omitempty"`
	RuleSet               []SingboxRuleSet   `json:"rule_set,omitempty"`
	Final                 string             `json:"final,omitempty"`
	AutoDetectInterface   bool               `json:"auto_detect_interface,omitempty"`
	DefaultDomainResolver string             `json:"default_domain_resolver,omitempty"`
}

type SingboxRuleSet struct {
	Tag            string `json:"tag"`
	Type           string `json:"type"`
	Format         string `json:"format"`
	URL            string `json:"url,omitempty"`
	Path           string `json:"path,omitempty"`
	DownloadDetour string `json:"download_detour,omitempty"`
}

type SingboxRouteRule struct {
	IPIsPrivate   bool     `json:"ip_is_private,omitempty"`
	RuleSet       []string `json:"rule_set,omitempty"`
	Domain        []string `json:"domain,omitempty"`
	DomainSuffix  []string `json:"domain_suffix,omitempty"`
	DomainKeyword []string `json:"domain_keyword,omitempty"`
	DomainRegex   []string `json:"domain_regex,omitempty"`
	IPCIDR        []string `json:"ip_cidr,omitempty"`
	Port          []uint16 `json:"port,omitempty"`
	PortRange     []string `json:"port_range,omitempty"`
	Network       string   `json:"network,omitempty"`
	InboundTag    []string `json:"inbound,omitempty"`
	Protocol      string   `json:"protocol,omitempty"`
	// action 模式（sniff/hijack-dns/route）
	Action   string `json:"action,omitempty"`
	Outbound string `json:"outbound,omitempty"`
}

// BuildSingboxConfig 生成 sing-box config.json
func BuildSingboxConfig(setting *configure.Setting) (*SingboxConfig, error) {
	if setting == nil {
		setting = configure.GetSettingNotNil()
	}
	listenAddr := configure.BuiltinProxyListenAddress(setting)
	dnsServers, dnsRules, routeRules, autoDetectInterface, defaultDomainResolver := buildSingboxDefaults(setting)

	cfg := &SingboxConfig{
		Log: &SingboxLog{Level: singboxLogLevel(setting.LogLevel)},
		DNS: &SingboxDNS{
			Servers: dnsServers,
			Rules:   dnsRules,
			Final:   "remote",
		},
		Inbounds: []SingboxInbound{
			buildSingboxInbound("socks", "socks-in", listenAddr, setting.Socks5Port, setting.Socks5AuthAccounts()),
			buildSingboxInbound("http", "http-in", listenAddr, setting.HttpPort, setting.HTTPAuthAccounts()),
			buildSingboxInbound("socks", ProbeInboundTag, ProbeInboundListen, ProbeInboundSocksPort, nil),
		},
		Outbounds: []SingboxOutbound{
			{Type: "direct", Tag: ProbeOutboundName},
			{Type: "direct", Tag: "direct"},
			{Type: "block", Tag: "block"},
		},
		Route: &SingboxRoute{
			Rules: append([]SingboxRouteRule{
				{InboundTag: []string{ProbeInboundTag}, Outbound: ProbeOutboundName},
			}, routeRules...),
			RuleSet: []SingboxRuleSet{
				makeRuleSet("geosite-cn", "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/cn.srs"),
				makeRuleSet("geoip-cn", "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geoip/cn.srs"),
			},
			Final:                 "proxy",
			AutoDetectInterface:   autoDetectInterface,
			DefaultDomainResolver: defaultDomainResolver,
		},
	}

	// 添加自定义入站
	for _, ci := range configure.GetCustomInbounds() {
		p := strings.ToLower(strings.TrimSpace(ci.Protocol))
		if p == "cfdo" || p == "cfgoodnet" {
			continue
		}
		ib := buildSingboxInbound("", ci.Tag, ci.Listen, ci.Port, ci.AuthAccounts())
		if ib.Listen == "" {
			ib.Listen = "127.0.0.1"
		}
		switch ci.Protocol {
		case "socks":
			ib.Type = "socks"
		case "http":
			ib.Type = "http"
		case "dokodemo-door":
			ib.Type = "tproxy"
		default:
			ib.Type = ci.Protocol
		}
		cfg.Inbounds = append(cfg.Inbounds, ib)
		// 如果需要 sniff，通过路由规则添加
		if ci.SniffEnabled {
			cfg.Route.Rules = append([]SingboxRouteRule{
				{InboundTag: []string{ci.Tag}, Action: "sniff"},
			}, cfg.Route.Rules...)
		}
	}

	// 先收集所有有效出站 tag（已绑定节点的），供前置代理引用校验
	validOutboundTags := collectValidOutboundTags()

	var firstProxyOutbound string
	for _, name := range configure.GetOutboundNames() {
		o := configure.GetOutbound(name)
		if o == nil {
			continue
		}
		s := resolveOutboundServerRaw(name, o)
		if s == nil {
			continue
		}
		ob, err := singboxServerToOutbound(s, name)
		if err != nil {
			return nil, fmt.Errorf("outbound %v: %w", name, err)
		}
		// 前置代理：sing-box 通过 detour 引用前置出站 tag
		if front := resolveFrontProxyTag(s, name, validOutboundTags); front != "" {
			ob.Detour = front
			if LogCallback != nil {
				LogCallback(fmt.Sprintf("🔗 singbox outbound %s 前置代理 => %s", name, front))
			}
		}
		if LogCallback != nil {
			LogCallback(fmt.Sprintf("📘 singbox outbound %s => type=%s server=%s port=%d", name, ob.Type, ob.Server, ob.ServerPort))
		}
		if name == ProbeOutboundName {
			replaced := false
			for i := range cfg.Outbounds {
				if cfg.Outbounds[i].Tag == ProbeOutboundName {
					cfg.Outbounds[i] = ob
					replaced = true
					break
				}
			}
			if !replaced {
				cfg.Outbounds = append([]SingboxOutbound{ob}, cfg.Outbounds...)
			}
		} else {
			cfg.Outbounds = append([]SingboxOutbound{ob}, cfg.Outbounds...)
		}
		validOutboundTags[name] = true
		if firstProxyOutbound == "" {
			firstProxyOutbound = name
		}
	}

	// 更新路由的 Final 出站和 DNS remote 的 Detour：优先使用第一个代理出站，否则使用 direct
	if firstProxyOutbound != "" {
		cfg.Route.Final = firstProxyOutbound
		// 更新 DNS remote 服务器的 Detour
		for i := range cfg.DNS.Servers {
			if cfg.DNS.Servers[i].Tag == "remote" {
				cfg.DNS.Servers[i].Detour = firstProxyOutbound
				break
			}
		}
	} else {
		cfg.Route.Final = "direct"
		// 没有代理出站时，DNS remote 也走 direct
		for i := range cfg.DNS.Servers {
			if cfg.DNS.Servers[i].Tag == "remote" {
				cfg.DNS.Servers[i].Detour = "direct"
				break
			}
		}
	}

	// 注入用户自定义路由规则（跳过引用了不存在出站的规则）
	for _, r := range configure.GetRoutingRules() {
		if !r.Enabled {
			continue
		}
		outTag := string(r.Action)
		if r.Action == configure.RuleActionOutbound {
			outTag = r.OutboundName
			// 出站不存在或未绑定节点，跳过该规则
			if !validOutboundTags[outTag] {
				if LogCallback != nil {
					LogCallback(fmt.Sprintf("⚠️ 路由规则「%s」引用的出站「%s」不存在或未绑定节点，已跳过", r.Name, outTag))
				}
				continue
			}
		}
		rule := SingboxRouteRule{Outbound: outTag}
		if len(r.InboundTags) > 0 {
			rule.InboundTag = r.InboundTags
		}
		if r.Protocol != "" {
			rule.Network = r.Protocol
		}

		// 收集各类条件，每类单独生成一条规则（OR 语义）
		var ruleSets []string
		var domainSuffixes, domainKeywords, domainRegexes []string
		var ipCIDRs []string
		var ipIsPrivate bool

		// 解析域名规则
		for _, d := range r.Domains {
			if strings.HasPrefix(d, "geosite:") {
				name, ok := resolveGeoName(strings.TrimPrefix(d, "geosite:"))
				if !ok {
					continue
				}
				tag := "geosite-" + name
				ruleSets = append(ruleSets, tag)
				cfg.Route.RuleSet = appendRuleSetIfAbsent(cfg.Route.RuleSet, tag, geositeRuleSetURL(name))
			} else if strings.HasPrefix(d, "domain:") {
				// domain: 前缀用 domain_suffix，匹配该域名及其所有子域名
				domainSuffixes = append(domainSuffixes, strings.TrimPrefix(d, "domain:"))
			} else if strings.HasPrefix(d, "regexp:") {
				domainRegexes = append(domainRegexes, strings.TrimPrefix(d, "regexp:"))
			} else if strings.HasPrefix(d, "keyword:") {
				domainKeywords = append(domainKeywords, strings.TrimPrefix(d, "keyword:"))
			} else {
				// 裸域名：用 domain_suffix 匹配该域名及其所有子域名
				domainSuffixes = append(domainSuffixes, d)
			}
		}
		// 解析 IP 规则
		for _, ip := range r.IPs {
			if strings.HasPrefix(ip, "geoip:") {
				rawName := strings.TrimPrefix(ip, "geoip:")
				if rawName == "private" {
					ipIsPrivate = true
					continue
				}
				name, ok := resolveGeoName(rawName)
				if !ok {
					continue
				}
				tag := "geoip-" + name
				ruleSets = append(ruleSets, tag)
				cfg.Route.RuleSet = appendRuleSetIfAbsent(cfg.Route.RuleSet, tag, geoipRuleSetURL(name))
			} else {
				ipCIDRs = append(ipCIDRs, ip)
			}
		}
		// 解析端口
		var ports []uint16
		var portRanges []string
		if r.Ports != "" {
			for _, p := range strings.Split(r.Ports, ",") {
				p = strings.TrimSpace(p)
				if strings.Contains(p, "-") {
					portRanges = append(portRanges, p)
				} else if port := strToInt(p); port > 0 {
					ports = append(ports, uint16(port))
				}
			}
		}

		// 生成规则：每类条件单独一条（OR 语义）
		// 如果只有一类条件，直接合并到 rule 里
		// 如果有多类条件，每类单独生成一条规则
		addRule := func(r SingboxRouteRule) {
			cfg.Route.Rules = append(cfg.Route.Rules, r)
		}
		base := SingboxRouteRule{
			Outbound:   outTag,
			InboundTag: rule.InboundTag,
			Network:    rule.Network,
			Port:       ports,
			PortRange:  portRanges,
		}
		if len(ruleSets) > 0 {
			r2 := base
			r2.RuleSet = ruleSets
			if ipIsPrivate {
				r2.IPIsPrivate = true
			}
			addRule(r2)
		}
		if len(domainSuffixes) > 0 {
			r2 := base
			r2.DomainSuffix = domainSuffixes
			addRule(r2)
		}
		if len(domainKeywords) > 0 {
			r2 := base
			r2.DomainKeyword = domainKeywords
			addRule(r2)
		}
		if len(domainRegexes) > 0 {
			r2 := base
			r2.DomainRegex = domainRegexes
			addRule(r2)
		}
		if len(ipCIDRs) > 0 {
			r2 := base
			r2.IPCIDR = ipCIDRs
			addRule(r2)
		}
		if ipIsPrivate && len(ruleSets) == 0 {
			r2 := base
			r2.IPIsPrivate = true
			addRule(r2)
		}
		// 如果所有条件都为空（只有 inbound/port/protocol），生成一条基础规则
		if len(ruleSets) == 0 && len(domainSuffixes) == 0 &&
			len(domainKeywords) == 0 && len(domainRegexes) == 0 && len(ipCIDRs) == 0 && !ipIsPrivate {
			addRule(base)
		}
	}

	// download_detour 必须在所有 rule-set 收集完之后统一赋值：
	// 用户自定义规则的 rule-set 是在上面的循环里才追加的，早于此处赋值会漏掉它们，
	// 导致这些 rule-set 以直连方式下载而在被墙环境下静默失败。
	ruleSetDetour := cfg.Route.Final
	for i := range cfg.Route.RuleSet {
		if cfg.Route.RuleSet[i].Type == "remote" {
			cfg.Route.RuleSet[i].DownloadDetour = ruleSetDetour
		}
	}

	// 启用 rule-set 缓存，避免每次启动都重新下载
	cfg.Experimental = &SingboxExperimental{
		CacheFile: &SingboxCacheFile{Enabled: true, Path: "cache.db"},
	}
	cfg.Route.Rules = optimizeSingboxRules(cfg.Route.Rules)

	return cfg, nil
}

func singboxServerToOutbound(s *configure.ServerRaw, tag string) (SingboxOutbound, error) {
	switch strings.ToLower(s.Type) {
	case "vmess":
		return sbVmess(s, tag)
	case "vless":
		return sbVless(s, tag)
	case "ss", "shadowsocks":
		return sbSS(s, tag)
	case "ssr":
		return sbSSR(s, tag)
	case "trojan":
		return sbTrojan(s, tag)
	case "hysteria2", "hy2":
		return sbHysteria2(s, tag)
	case "hysteria":
		return sbHysteria(s, tag)
	case "tuic":
		return sbTuic(s, tag)
	case "wireguard":
		return sbWireguard(s, tag)
	case "socks5", "socks", "socks4":
		return sbSocks(s, tag)
	case "http", "https", "cfgoodnet":
		return sbHTTP(s, tag)
	case "anytls":
		return sbAnyTLS(s, tag)
	case "naive":
		return sbNaive(s, tag)
	case "juicity":
		return sbJuicity(s, tag)
	default:
		return SingboxOutbound{}, fmt.Errorf("singbox does not support: %v", s.Type)
	}
}

func sbVmess(s *configure.ServerRaw, tag string) (SingboxOutbound, error) {
	b64 := s.Link[len("vmess://"):]
	decoded, err := b64Decode(b64)
	if err != nil {
		return SingboxOutbound{}, err
	}
	var v struct {
		Add  string      `json:"add"`
		Port interface{} `json:"port"`
		ID   string      `json:"id"`
		Aid  interface{} `json:"aid"`
		Net  string      `json:"net"`
		Host string      `json:"host"`
		Path string      `json:"path"`
		TLS  string      `json:"tls"`
		SNI  string      `json:"sni"`
		Fp   string      `json:"fp"`
		Alpn string      `json:"alpn"`
	}
	if err := json.Unmarshal([]byte(decoded), &v); err != nil {
		return SingboxOutbound{}, err
	}
	ob := SingboxOutbound{
		Type: "vmess", Tag: tag,
		Server: v.Add, ServerPort: anyToInt(v.Port),
		UUID: v.ID, Security: "auto", AlterID: anyToInt(v.Aid),
	}
	ob.TLS = sbTLS(v.TLS == "tls", v.SNI, v.Fp, v.Alpn, "", "", false)
	ob.Transport = sbTransport(v.Net, v.Path, v.Host, "")
	return ob, nil
}

func sbVless(s *configure.ServerRaw, tag string) (SingboxOutbound, error) {
	u, err := url.Parse(s.Link)
	if err != nil {
		return SingboxOutbound{}, err
	}
	q := u.Query()
	security := q.Get("security")
	ob := SingboxOutbound{
		Type: "vless", Tag: tag,
		Server: u.Hostname(), ServerPort: strToInt(u.Port()),
		UUID: u.User.Username(), Flow: q.Get("flow"),
	}
	ob.TLS = sbTLS(security == "tls" || security == "reality",
		firstNonEmpty(q.Get("sni"), q.Get("host")), q.Get("fp"), q.Get("alpn"),
		q.Get("pbk"), q.Get("sid"), security == "reality")
	ob.Transport = sbTransport(q.Get("type"), q.Get("path"), q.Get("host"), q.Get("serviceName"))
	return ob, nil
}

func sbSS(s *configure.ServerRaw, tag string) (SingboxOutbound, error) {
	ss, err := parseShadowsocksEndpoint(s)
	if err != nil {
		return SingboxOutbound{}, err
	}
	return SingboxOutbound{
		Type: "shadowsocks", Tag: tag,
		Server: ss.Host, ServerPort: ss.Port,
		Method: ss.Method, Password: ss.Password,
	}, nil
}

func sbTrojan(s *configure.ServerRaw, tag string) (SingboxOutbound, error) {
	u, err := url.Parse(s.Link)
	if err != nil {
		return SingboxOutbound{}, err
	}
	q := u.Query()
	sni := firstNonEmpty(q.Get("sni"), q.Get("peer"))
	ob := SingboxOutbound{
		Type: "trojan", Tag: tag,
		Server: u.Hostname(), ServerPort: strToInt(u.Port()),
		Password: u.User.Username(),
	}
	ob.TLS = sbTLS(true, sni, q.Get("fp"), q.Get("alpn"), "", "", false)
	ob.Transport = sbTransport(q.Get("type"), q.Get("path"), q.Get("host"), "")
	return ob, nil
}

func sbHysteria2(s *configure.ServerRaw, tag string) (SingboxOutbound, error) {
	u, err := url.Parse(s.Link)
	if err != nil {
		return SingboxOutbound{}, err
	}
	q := u.Query()
	pwd := u.User.Username()
	if p, ok := u.User.Password(); ok && p != "" {
		pwd = p
	}
	sni := firstNonEmpty(q.Get("sni"), u.Hostname())
	ob := SingboxOutbound{
		Type: "hysteria2", Tag: tag,
		Server: u.Hostname(), ServerPort: strToInt(u.Port()),
		Password: pwd,
	}
	ob.TLS = sbTLS(true, sni, q.Get("pinSHA256"), "", "", "", false)
	if ob.TLS != nil {
		ob.TLS.Insecure = q.Get("insecure") == "1"
	}
	return ob, nil
}

func sbHysteria(s *configure.ServerRaw, tag string) (SingboxOutbound, error) {
	u, err := url.Parse(s.Link)
	if err != nil {
		return SingboxOutbound{}, err
	}
	q := u.Query()
	ob := SingboxOutbound{
		Type: "hysteria", Tag: tag,
		Server: u.Hostname(), ServerPort: strToInt(u.Port()),
		Password: q.Get("auth"),
		UpMbps:   strToInt(q.Get("upmbps")), DownMbps: strToInt(q.Get("downmbps")),
	}
	ob.TLS = sbTLS(true, firstNonEmpty(q.Get("peer"), u.Hostname()), "", "", "", "", false)
	if ob.TLS != nil {
		ob.TLS.Insecure = q.Get("insecure") == "1"
	}
	return ob, nil
}

func sbTuic(s *configure.ServerRaw, tag string) (SingboxOutbound, error) {
	u, err := url.Parse(s.Link)
	if err != nil {
		return SingboxOutbound{}, err
	}
	q := u.Query()
	ob := SingboxOutbound{
		Type: "tuic", Tag: tag,
		Server: u.Hostname(), ServerPort: strToInt(u.Port()),
		UUID: u.User.Username(), Password: func() string { p, _ := u.User.Password(); return p }(),
		CongestionControl: firstNonEmpty(q.Get("congestion_control"), "bbr"),
	}
	ob.TLS = sbTLS(true, firstNonEmpty(q.Get("sni"), u.Hostname()), "", q.Get("alpn"), "", "", false)
	return ob, nil
}

func sbWireguard(s *configure.ServerRaw, tag string) (SingboxOutbound, error) {
	// wireguard:// 格式：wireguard://privatekey@host:port?publickey=xxx&ip=xxx
	u, err := url.Parse(s.Link)
	if err != nil {
		return SingboxOutbound{}, err
	}
	q := u.Query()
	ob := SingboxOutbound{
		Type: "wireguard", Tag: tag,
		Server: u.Hostname(), ServerPort: strToInt(u.Port()),
		PrivateKey:    u.User.Username(),
		PeerPublicKey: q.Get("publickey"),
	}
	return ob, nil
}

func sbSocks(s *configure.ServerRaw, tag string) (SingboxOutbound, error) {
	var host string
	var port int
	var username, password string

	// 对于 clash:// 或 singbox:// 格式，需要从 Link 中提取凭证
	if strings.HasPrefix(s.Link, "clash://") {
		host = s.Host
		port = s.Port
		username, password = extractClashCredentialsForSingbox(s.Link)
	} else if strings.HasPrefix(s.Link, "singbox://") {
		host = s.Host
		port = s.Port
		username, password = extractSingboxCredentialsForSingbox(s.Link)
	} else {
		// 标准 URI 格式 - 直接使用 s.Link 而不是重新构建
		u, err := url.Parse(s.Link)
		if err != nil {
			return SingboxOutbound{}, fmt.Errorf("invalid socks link: %w", err)
		}
		host = u.Hostname()
		port = strToInt(u.Port())
		if u.User != nil {
			username = u.User.Username()
			password, _ = u.User.Password()
		}
	}

	if host == "" || port == 0 {
		return SingboxOutbound{}, fmt.Errorf("invalid socks link: missing host or port")
	}

	ob := SingboxOutbound{
		Type: "socks", Tag: tag,
		Server: host, ServerPort: port,
		Username: username, Password: password,
	}
	return ob, nil
}

// sbAnyTLS 处理 anytls 协议（仅 sing-box 支持）
// 链接格式: anytls://password@host:port?sni=xxx&insecure=1&...
func sbAnyTLS(s *configure.ServerRaw, tag string) (SingboxOutbound, error) {
	// clash:// 或 singbox:// 格式：从已解析的 Host/Port 和 Link 中提取字段
	if strings.HasPrefix(s.Link, "clash://") || strings.HasPrefix(s.Link, "singbox://") {
		if s.Host == "" || s.Port == 0 {
			return SingboxOutbound{}, fmt.Errorf("invalid anytls link: missing host or port")
		}
		var password, sni string
		var insecure bool
		var fp string
		if strings.HasPrefix(s.Link, "clash://") {
			encoded := strings.TrimPrefix(s.Link, "clash://")
			if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil {
				var m map[string]interface{}
				if json.Unmarshal(decoded, &m) == nil {
					password, _ = m["password"].(string)
					sni, _ = m["sni"].(string)
					insecure, _ = m["skip-cert-verify"].(bool)
					fp, _ = m["client-fingerprint"].(string)
				}
			}
		} else {
			encoded := strings.TrimPrefix(s.Link, "singbox://")
			if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil {
				var m map[string]interface{}
				if json.Unmarshal(decoded, &m) == nil {
					password, _ = m["password"].(string)
					if tls, ok := m["tls"].(map[string]interface{}); ok {
						sni, _ = tls["server_name"].(string)
						insecure, _ = tls["insecure"].(bool)
					}
				}
			}
		}
		if sni == "" {
			sni = s.Host
		}
		ob := SingboxOutbound{
			Type: "anytls", Tag: tag,
			Server: s.Host, ServerPort: s.Port,
			Password: password,
			TLS:      &SingboxTLS{Enabled: true, ServerName: sni, Insecure: insecure},
		}
		if fp != "" {
			ob.TLS.UTLS = &SingboxUTLS{Enabled: true, Fingerprint: fp}
		}
		return ob, nil
	}

	// 标准 anytls:// URI 格式
	u, err := url.Parse(s.Link)
	if err != nil || u.Hostname() == "" {
		if s.Host == "" || s.Port == 0 {
			return SingboxOutbound{}, fmt.Errorf("invalid anytls link")
		}
		return SingboxOutbound{Type: "anytls", Tag: tag, Server: s.Host, ServerPort: s.Port}, nil
	}
	q := u.Query()
	password := u.User.Username()
	sni := firstNonEmpty(q.Get("sni"), u.Hostname())
	insecure := q.Get("insecure") == "1" || q.Get("allowInsecure") == "1"
	fp := q.Get("fp")

	ob := SingboxOutbound{
		Type: "anytls", Tag: tag,
		Server: u.Hostname(), ServerPort: strToInt(u.Port()),
		Password: password,
		TLS:      &SingboxTLS{Enabled: true, ServerName: sni, Insecure: insecure},
	}
	if fp != "" {
		ob.TLS.UTLS = &SingboxUTLS{Enabled: true, Fingerprint: fp}
	}
	return ob, nil
}

// sbSSR sing-box 不原生支持 SSR，降级为 shadowsocks（仅 method+password）
// 注意：SSR 的混淆/协议插件无法在 sing-box 中使用，仅作兼容处理
func sbSSR(s *configure.ServerRaw, tag string) (SingboxOutbound, error) {
	// SSR 链接格式: ssr://base64(host:port:protocol:method:obfs:base64pass/?params)
	link := strings.TrimPrefix(s.Link, "ssr://")
	decoded, err := b64Decode(link)
	if err != nil {
		// 解析失败，用已有的 Host/Port
		return SingboxOutbound{
			Type: "shadowsocks", Tag: tag,
			Server: s.Host, ServerPort: s.Port,
			Method: "aes-256-cfb", Password: "",
		}, nil
	}
	parts := strings.SplitN(decoded, ":", 6)
	host := s.Host
	port := s.Port
	method := "aes-256-cfb"
	password := ""
	if len(parts) >= 6 {
		host = parts[0]
		port = strToInt(parts[1])
		method = parts[3]
		pwdAndParams := parts[5]
		pwdB64 := pwdAndParams
		if idx := strings.Index(pwdAndParams, "/?"); idx != -1 {
			pwdB64 = pwdAndParams[:idx]
		}
		if p, err := b64Decode(pwdB64); err == nil {
			password = p
		}
	}
	return SingboxOutbound{
		Type: "shadowsocks", Tag: tag,
		Server: host, ServerPort: port,
		Method: method, Password: password,
	}, nil
}

// sbNaive naive 协议（sing-box 支持）
// 链接格式: naive+https://user:pass@host:port
func sbNaive(s *configure.ServerRaw, tag string) (SingboxOutbound, error) {
	link := s.Link
	// 去掉 naive+ 前缀，变成标准 https:// URI
	link = strings.TrimPrefix(link, "naive+")
	u, err := url.Parse(link)
	if err != nil || u.Hostname() == "" {
		return SingboxOutbound{}, fmt.Errorf("invalid naive link")
	}
	username := u.User.Username()
	password, _ := u.User.Password()
	sni := firstNonEmpty(u.Query().Get("sni"), u.Hostname())
	return SingboxOutbound{
		Type:       "http",
		Tag:        tag,
		Server:     u.Hostname(),
		ServerPort: strToInt(u.Port()),
		Username:   username,
		Password:   password,
		TLS: &SingboxTLS{
			Enabled:    true,
			ServerName: sni,
		},
	}, nil
}

// sbJuicity juicity 协议（sing-box 支持）
// 链接格式: juicity://uuid:password@host:port?congestion_control=bbr&sni=xxx
func sbJuicity(s *configure.ServerRaw, tag string) (SingboxOutbound, error) {
	u, err := url.Parse(s.Link)
	if err != nil || u.Hostname() == "" {
		return SingboxOutbound{}, fmt.Errorf("invalid juicity link")
	}
	q := u.Query()
	uuid := u.User.Username()
	password, _ := u.User.Password()
	sni := firstNonEmpty(q.Get("sni"), u.Hostname())
	cc := firstNonEmpty(q.Get("congestion_control"), "bbr")
	insecure := q.Get("insecure") == "1" || q.Get("allowInsecure") == "1"
	return SingboxOutbound{
		Type:              "juicity",
		Tag:               tag,
		Server:            u.Hostname(),
		ServerPort:        strToInt(u.Port()),
		UUID:              uuid,
		Password:          password,
		CongestionControl: cc,
		TLS: &SingboxTLS{
			Enabled:    true,
			ServerName: sni,
			Insecure:   insecure,
		},
	}, nil
}

// geoNameAliases 将用户可能输入的别名映射到 MetaCubeX 实际文件名
var geoNameAliases = map[string]string{
	"china-list": "cn",
	"china":      "cn",
	"private":    "", // 用 ip_is_private 替代，不生成 rule-set
}

// resolveGeoName 应用别名映射；返回 ok=false 表示该名称不对应 rule-set 文件
func resolveGeoName(name string) (string, bool) {
	if alias, ok := geoNameAliases[name]; ok {
		if alias == "" {
			return "", false
		}
		return alias, true
	}
	if !safeGeoName(name) {
		return "", false
	}
	return name, true
}

// safeGeoName 拒绝会逃出 rule-set 目录的名称。
// 该名称同时用于拼接下载 URL 和本地文件路径，且完全来自用户输入。
func safeGeoName(name string) bool {
	if name == "" || strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == '!':
		default:
			return false
		}
	}
	return true
}

// geositeRuleSetURL / geoipRuleSetURL 是 rule-set 下载地址的唯一来源，
// 配置生成与预下载共用，避免两处拼接逻辑漂移。
func geositeRuleSetURL(name string) string {
	return "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/" + name + ".srs"
}

func geoipRuleSetURL(name string) string {
	return "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geoip/" + name + ".srs"
}

// collectUserRuleSetRefs 扫描已启用的自定义路由规则，得出其引用的 rule-set。
// 只看已启用的规则，与 BuildSingboxConfig 的取值范围保持一致。
func collectUserRuleSetRefs() []RuleSetRef {
	seen := map[string]struct{}{}
	var refs []RuleSetRef
	add := func(tag, url string) {
		if _, ok := seen[tag]; ok {
			return
		}
		seen[tag] = struct{}{}
		refs = append(refs, RuleSetRef{Name: tag, URL: url})
	}

	for _, r := range configure.GetRoutingRules() {
		if !r.Enabled {
			continue
		}
		for _, d := range r.Domains {
			if !strings.HasPrefix(d, "geosite:") {
				continue
			}
			if name, ok := resolveGeoName(strings.TrimPrefix(d, "geosite:")); ok {
				add("geosite-"+name, geositeRuleSetURL(name))
			}
		}
		for _, ip := range r.IPs {
			if !strings.HasPrefix(ip, "geoip:") {
				continue
			}
			raw := strings.TrimPrefix(ip, "geoip:")
			if raw == "private" {
				continue
			}
			if name, ok := resolveGeoName(raw); ok {
				add("geoip-"+name, geoipRuleSetURL(name))
			}
		}
	}
	return refs
}

// singboxLogLevel 将通用日志级别直接传给 sing-box，保持一致
// buildSingboxInbound 构建 SingboxInbound，若有账号则填入 users 数组
func buildSingboxInbound(typ, tag, listen string, port int, accounts []configure.InboundAccount) SingboxInbound {
	ib := SingboxInbound{Type: typ, Tag: tag, Listen: listen, ListenPort: port}
	if len(accounts) > 0 {
		ib.Users = make([]SingboxInboundUser, 0, len(accounts))
		for _, a := range accounts {
			ib.Users = append(ib.Users, SingboxInboundUser{Username: a.Username, Password: a.Password})
		}
	}
	return ib
}

func singboxLogLevel(level string) string {
	switch strings.ToLower(level) {
	case "debug", "info", "warn", "warning", "error":
		return strings.ToLower(level)
	default:
		return "info"
	}
}

func buildSingboxDefaults(setting *configure.Setting) ([]SingboxDNSServer, []SingboxDNSRule, []SingboxRouteRule, bool, string) {
	dnsMode := strings.ToLower(setting.DNSMode)
	if dnsMode == "" {
		dnsMode = "lightweight"
	}

	var dnsServers []SingboxDNSServer
	var defaultDomainResolver string
	autoDetectInterface := false

	switch dnsMode {
	case "compatible":
		dnsServers = []SingboxDNSServer{
			{Type: "udp", Tag: "bootstrap", Server: "223.5.5.5"},
			{Type: "https", Tag: "local", Server: "223.5.5.5", DomainResolver: "bootstrap"},
			{Type: "https", Tag: "remote", Server: "8.8.8.8", DomainResolver: "bootstrap"},
		}
		defaultDomainResolver = "bootstrap"
		autoDetectInterface = true
	default:
		dnsServers = []SingboxDNSServer{
			{Type: "udp", Tag: "local", Server: "223.5.5.5"},
			{Type: "udp", Tag: "remote", Server: "1.1.1.1"},
		}
		defaultDomainResolver = "local"
	}

	dnsRules := []SingboxDNSRule{{RuleSet: []string{"geosite-cn"}, Server: "local"}}
	routeRules := []SingboxRouteRule{}
	if setting.EnableSniff {
		routeRules = append(routeRules, SingboxRouteRule{Action: "sniff"})
	}
	if setting.EnableHijackDNS {
		routeRules = append(routeRules, SingboxRouteRule{Protocol: "dns", Action: "hijack-dns"})
	}
	routeRules = append(routeRules,
		SingboxRouteRule{IPIsPrivate: true, Outbound: "direct"},
		SingboxRouteRule{RuleSet: []string{"geosite-cn"}, Outbound: "direct"},
		SingboxRouteRule{RuleSet: []string{"geoip-cn"}, Outbound: "direct"},
	)

	return dnsServers, dnsRules, routeRules, autoDetectInterface, defaultDomainResolver
}

// appendRuleSetIfAbsent 如果 rule-set 不存在则添加
func appendRuleSetIfAbsent(sets []SingboxRuleSet, tag, url string) []SingboxRuleSet {
	for _, s := range sets {
		if s.Tag == tag {
			return sets
		}
	}
	rs := makeRuleSet(tag, url)
	return append(sets, rs)
}

// makeRuleSet 创建 rule-set 配置，优先使用本地文件
func makeRuleSet(tag, remoteURL string) SingboxRuleSet {
	if Manager.dataDir != "" {
		localPath := filepath.Join(Manager.dataDir, "rule-set", tag+".srs")
		if fi, err := os.Stat(localPath); err == nil && fi.Size() > 0 {
			return SingboxRuleSet{Tag: tag, Type: "local", Format: "binary", Path: localPath}
		}
	}
	// DownloadDetour 会在后面统一设置
	return SingboxRuleSet{Tag: tag, Type: "remote", Format: "binary", URL: remoteURL}
}

// buildStandardLinkSingbox 根据协议类型和节点信息构建标准的协议 URL（用于 singbox）
func buildStandardLinkSingbox(s *configure.ServerRaw) string {
	switch strings.ToLower(s.Type) {
	case "http":
		return fmt.Sprintf("http://%s:%d", s.Host, s.Port)
	case "https":
		return fmt.Sprintf("https://%s:%d", s.Host, s.Port)
	case "socks5", "socks":
		return fmt.Sprintf("socks5://%s:%d", s.Host, s.Port)
	case "socks4":
		return fmt.Sprintf("socks4://%s:%d", s.Host, s.Port)
	default:
		// 对于其他协议，返回原始 Link
		return s.Link
	}
}

func sbHTTP(s *configure.ServerRaw, tag string) (SingboxOutbound, error) {
	var host string
	var port int
	var username, password string

	if strings.ToLower(s.Type) == "cfgoodnet" {
		host = s.Host
		port = s.Port
	}

	// 对于 clash:// 或 singbox:// 格式，需要从 Link 中提取凭证
	if host == "" && port == 0 && strings.HasPrefix(s.Link, "clash://") {
		host = s.Host
		port = s.Port
		username, password = extractClashCredentialsForSingbox(s.Link)
	} else if host == "" && port == 0 && strings.HasPrefix(s.Link, "singbox://") {
		host = s.Host
		port = s.Port
		username, password = extractSingboxCredentialsForSingbox(s.Link)
	} else if host == "" || port == 0 {
		// 标准 URI 格式 - 直接使用 s.Link 而不是重新构建
		u, err := url.Parse(s.Link)
		if err == nil {
			host = u.Hostname()
			port = strToInt(u.Port())
			if u.User != nil {
				username = u.User.Username()
				password, _ = u.User.Password()
			}
		}
		if host == "" || port == 0 {
			host = s.Host
			port = s.Port
		}
	}

	if host == "" || port == 0 {
		return SingboxOutbound{}, fmt.Errorf("invalid http link: missing host or port")
	}

	ob := SingboxOutbound{
		Type: "http", Tag: tag,
		Server: host, ServerPort: port,
		Username: username, Password: password,
	}
	if strings.ToLower(s.Type) == "https" {
		ob.TLS = &SingboxTLS{Enabled: true, ServerName: host}
	}
	return ob, nil
}

// extractClashCredentialsForSingbox 从 Clash YAML 格式中提取用户名和密码
func extractClashCredentialsForSingbox(link string) (string, string) {
	// link 格式: clash://base64encodedJSON
	if !strings.HasPrefix(link, "clash://") {
		return "", ""
	}
	encoded := strings.TrimPrefix(link, "clash://")

	// 尝试解码为 base64（新格式）
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err == nil {
		// 新格式：base64 编码的 JSON
		var data map[string]interface{}
		if err := json.Unmarshal(decoded, &data); err == nil {
			username, _ := data["username"].(string)
			password, _ := data["password"].(string)
			return username, password
		}
	}

	// 旧格式：直接的 YAML 文本，尝试解析
	lines := strings.Split(encoded, "\n")
	var username, password string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "username:") {
			username = strings.TrimSpace(strings.TrimPrefix(line, "username:"))
		} else if strings.HasPrefix(line, "password:") {
			password = strings.TrimSpace(strings.TrimPrefix(line, "password:"))
		}
	}
	return username, password
}

// extractSingboxCredentialsForSingbox 从 Singbox JSON 格式中提取用户名和密码
func extractSingboxCredentialsForSingbox(link string) (string, string) {
	// link 格式: singbox://base64encodedJSON
	if !strings.HasPrefix(link, "singbox://") {
		return "", ""
	}
	encoded := strings.TrimPrefix(link, "singbox://")

	// 尝试解码为 base64（新格式）
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err == nil {
		// 新格式：base64 编码的 JSON
		var data map[string]interface{}
		if err := json.Unmarshal(decoded, &data); err == nil {
			username, _ := data["username"].(string)
			password, _ := data["password"].(string)
			return username, password
		}
	}

	// 旧格式：直接的 JSON 文本，尝试解析
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(encoded), &data); err == nil {
		username, _ := data["username"].(string)
		password, _ := data["password"].(string)
		return username, password
	}

	return "", ""
}

// sbTLS 构建 sing-box TLS 配置
func sbTLS(enabled bool, sni, fp, alpn, pbk, sid string, isReality bool) *SingboxTLS {
	if !enabled {
		return nil
	}
	tls := &SingboxTLS{Enabled: true, ServerName: sni}
	if alpn != "" {
		tls.ALPN = strings.Split(alpn, ",")
	}
	if fp != "" {
		tls.UTLS = &SingboxUTLS{Enabled: true, Fingerprint: fp}
	}
	if isReality {
		tls.Reality = &SingboxReality{Enabled: true, PublicKey: pbk, ShortID: sid}
	}
	return tls
}

// sbTransport 构建 sing-box Transport 配置
func sbTransport(network, path, host, serviceName string) *SingboxTransport {
	switch strings.ToLower(network) {
	case "ws":
		t := &SingboxTransport{Type: "ws", Path: path}
		if host != "" {
			t.Headers = map[string]string{"Host": host}
		}
		return t
	case "grpc":
		return &SingboxTransport{Type: "grpc", ServiceName: serviceName}
	case "http", "h2":
		t := &SingboxTransport{Type: "http", Path: path}
		if host != "" {
			t.Host = []string{host}
		}
		return t
	case "httpupgrade":
		return &SingboxTransport{Type: "httpupgrade", Path: path}
	}
	return nil
}

// 确保 SingboxOutbound 能正确序列化（去掉零值字段）
func (o SingboxOutbound) MarshalJSON() ([]byte, error) {
	type Alias SingboxOutbound
	return json.Marshal((Alias)(o))
}
