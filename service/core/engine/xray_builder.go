package engine

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/ProxyStation/proxystation/core/coreObj"
	"github.com/ProxyStation/proxystation/db/configure"
)

// BuildXrayConfig 生成 xray/v2ray 兼容的 config.json
func BuildXrayConfig(setting *configure.Setting) (*coreObj.Config, error) {
	if setting == nil {
		setting = configure.GetSettingNotNil()
	}
	listenAddr := configure.BuiltinProxyListenAddress(setting)
	builtinSocksSniffing := coreObj.Sniffing{}
	builtinHTTPSniffing := coreObj.Sniffing{}
	if setting.EnableSniff {
		builtinSocksSniffing = coreObj.Sniffing{Enabled: true, DestOverride: []string{"http", "tls", "quic"}}
		builtinHTTPSniffing = coreObj.Sniffing{Enabled: true, DestOverride: []string{"http", "tls"}}
	}
	cfg := &coreObj.Config{
		Log:     &coreObj.Log{Loglevel: setting.LogLevel, Error: "none"},
		Routing: coreObj.Routing{DomainStrategy: "IPIfNonMatch"},
		Inbounds: []coreObj.Inbound{
			{
				Tag: "socks", Port: setting.Socks5Port, Listen: listenAddr, Protocol: "socks",
				Settings: &coreObj.InboundSettings{UDP: true},
				Sniffing: builtinSocksSniffing,
			},
			{
				Tag: "http", Port: setting.HttpPort, Listen: listenAddr, Protocol: "http",
				Sniffing: builtinHTTPSniffing,
			},
		},
		Outbounds: []coreObj.OutboundObject{
			{Tag: "direct", Protocol: "freedom"},
			{Tag: "block", Protocol: "blackhole"},
		},
	}

	// 添加自定义入站
	for _, ci := range configure.GetCustomInbounds() {
		ib := coreObj.Inbound{
			Tag:      ci.Tag,
			Port:     ci.Port,
			Listen:   ci.Listen,
			Protocol: ci.Protocol,
		}
		if ib.Listen == "" {
			ib.Listen = "127.0.0.1"
		}
		switch ci.Protocol {
		case "socks":
			ib.Settings = &coreObj.InboundSettings{UDP: ci.UDPEnabled}
		case "dokodemo-door":
			ib.Settings = &coreObj.InboundSettings{
				Network:        ci.Network,
				FollowRedirect: ci.FollowRedirect,
			}
		}
		if ci.SniffEnabled {
			dest := ci.SniffDest
			if len(dest) == 0 {
				dest = []string{"http", "tls", "quic"}
			}
			ib.Sniffing = coreObj.Sniffing{Enabled: true, DestOverride: dest}
		}
		cfg.Inbounds = append(cfg.Inbounds, ib)
	}

	// 基础路由规则：私有 IP 直连
	cfg.Routing.Rules = []coreObj.RoutingRule{
		{Type: "field", OutboundTag: "direct", IP: []string{
			"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
			"127.0.0.0/8", "169.254.0.0/16", "224.0.0.0/4", "240.0.0.0/4",
		}},
		{Type: "field", OutboundTag: "direct", IP: []string{
			"::1/128", "fc00::/7", "fe80::/10",
		}},
	}

	// 先收集有效出站 tag，再注入路由规则
	validOutboundTags := map[string]bool{"direct": true, "block": true}
	for _, name := range configure.GetOutboundNames() {
		o := configure.GetOutbound(name)
		if o == nil {
			continue
		}
		ref := resolveActiveNode(o)
		if ref == nil {
			continue
		}
		s := getServerRaw(ref)
		if s == nil {
			continue
		}
		ob, err := xrayServerToOutbound(s, name)
		if err != nil {
			return nil, fmt.Errorf("outbound %v: %w", name, err)
		}
		cfg.Outbounds = append([]coreObj.OutboundObject{ob}, cfg.Outbounds...)
		validOutboundTags[name] = true
		if name == "proxy" {
			cfg.Routing.Rules = append(cfg.Routing.Rules,
				coreObj.RoutingRule{Type: "field", OutboundTag: name, Port: "0-65535"})
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
			if !validOutboundTags[outTag] {
				continue // 出站未绑定节点，跳过
			}
		}
		rule := coreObj.RoutingRule{
			Type:        "field",
			OutboundTag: outTag,
		}
		if len(r.InboundTags) > 0 {
			rule.InboundTag = r.InboundTags
		}
		if len(r.Domains) > 0 {
			rule.Domain = r.Domains
		}
		if len(r.IPs) > 0 {
			rule.IP = r.IPs
		}
		if r.Ports != "" {
			rule.Port = r.Ports
		}
		if r.Protocol != "" {
			rule.Network = r.Protocol
		}
		cfg.Routing.Rules = append(cfg.Routing.Rules, rule)
	}

	cfg.Routing.Rules = optimizeXrayRules(cfg.Routing.Rules)

	return cfg, nil
}

func xrayServerToOutbound(s *configure.ServerRaw, tag string) (coreObj.OutboundObject, error) {
	switch strings.ToLower(s.Type) {
	case "vmess":
		return xrayVmess(s, tag)
	case "vless":
		return xrayVless(s, tag)
	case "ss", "shadowsocks":
		return xraySS(s, tag)
	case "ssr":
		return xraySSR(s, tag)
	case "trojan":
		return xrayTrojan(s, tag)
	case "socks5", "socks", "socks4":
		return xraySocks(s, tag)
	case "http", "https", "naive":
		return xrayHTTP(s, tag)
	default:
		return coreObj.OutboundObject{}, fmt.Errorf("xray does not support: %v", s.Type)
	}
}

// buildStandardLink 根据协议类型和节点信息构建标准的协议 URL
func buildStandardLink(s *configure.ServerRaw) string {
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

func xrayVmess(s *configure.ServerRaw, tag string) (coreObj.OutboundObject, error) {
	b64 := s.Link[len("vmess://"):]
	decoded, err := b64Decode(b64)
	if err != nil {
		return coreObj.OutboundObject{}, fmt.Errorf("vmess decode: %w", err)
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
		return coreObj.OutboundObject{}, fmt.Errorf("vmess json: %w", err)
	}
	stream := buildXrayStream(v.Net, v.TLS, v.Host, v.Path, v.SNI, v.Fp, v.Alpn, "", "", "", "")
	return coreObj.OutboundObject{
		Tag: tag, Protocol: "vmess",
		Settings: coreObj.OutboundSettings{Vnext: []coreObj.VnextObject{{
			Address: v.Add, Port: anyToInt(v.Port),
			Users: []coreObj.VnextUser{{ID: v.ID, AlterID: anyToInt(v.Aid), Security: "auto"}},
		}}},
		StreamSettings: stream,
	}, nil
}

func xrayVless(s *configure.ServerRaw, tag string) (coreObj.OutboundObject, error) {
	u, err := url.Parse(s.Link)
	if err != nil || u.Hostname() == "" {
		return coreObj.OutboundObject{}, fmt.Errorf("invalid vless link: %w", err)
	}
	q := u.Query()
	network := q.Get("type")
	if network == "" {
		network = "tcp"
	}
	stream := buildXrayStream(network, q.Get("security"), q.Get("host"), q.Get("path"),
		firstNonEmpty(q.Get("sni"), q.Get("host")), q.Get("fp"), q.Get("alpn"),
		q.Get("pbk"), q.Get("sid"), q.Get("spx"), q.Get("serviceName"))
	return coreObj.OutboundObject{
		Tag: tag, Protocol: "vless",
		Settings: coreObj.OutboundSettings{Vnext: []coreObj.VnextObject{{
			Address: u.Hostname(), Port: strToInt(u.Port()),
			Users: []coreObj.VnextUser{{ID: u.User.Username(), Encryption: "none", Flow: q.Get("flow")}},
		}}},
		StreamSettings: stream,
	}, nil
}

func xraySS(s *configure.ServerRaw, tag string) (coreObj.OutboundObject, error) {
	link := s.Link
	if idx := strings.Index(link, "#"); idx != -1 {
		link = link[:idx]
	}
	link = strings.TrimPrefix(link, "ss://")
	var method, password, host string
	var port int
	if atIdx := strings.LastIndex(link, "@"); atIdx != -1 {
		userinfo := link[:atIdx]
		hostport := link[atIdx+1:]
		decoded, err := b64Decode(userinfo)
		if err != nil {
			decoded = userinfo
		}
		parts := strings.SplitN(decoded, ":", 2)
		if len(parts) == 2 {
			method, password = parts[0], parts[1]
		}
		host, port = splitHostPort(hostport)
	}
	return coreObj.OutboundObject{
		Tag: tag, Protocol: "shadowsocks",
		Settings: coreObj.OutboundSettings{
			Servers: []coreObj.ServerObject{{Address: host, Port: port, Password: password, Method: method}},
		},
	}, nil
}

func xraySSR(s *configure.ServerRaw, tag string) (coreObj.OutboundObject, error) {
	link := strings.TrimPrefix(s.Link, "ssr://")
	decoded, err := b64Decode(link)
	if err != nil {
		return coreObj.OutboundObject{}, fmt.Errorf("ssr decode: %w", err)
	}
	parts := strings.SplitN(decoded, ":", 6)
	if len(parts) < 6 {
		return coreObj.OutboundObject{}, fmt.Errorf("invalid ssr link")
	}
	host := parts[0]
	port := strToInt(parts[1])
	pwdAndParams := parts[5]
	pwdB64 := pwdAndParams
	if idx := strings.Index(pwdAndParams, "/?"); idx != -1 {
		pwdB64 = pwdAndParams[:idx]
	}
	pwd, _ := b64Decode(pwdB64)
	return coreObj.OutboundObject{
		Tag: tag, Protocol: "shadowsocks",
		Settings: coreObj.OutboundSettings{
			Servers: []coreObj.ServerObject{{Address: host, Port: port, Password: pwd, Method: "aes-256-cfb"}},
		},
	}, nil
}

func xrayTrojan(s *configure.ServerRaw, tag string) (coreObj.OutboundObject, error) {
	u, err := url.Parse(s.Link)
	if err != nil || u.Hostname() == "" {
		return coreObj.OutboundObject{}, fmt.Errorf("invalid trojan link: %w", err)
	}
	q := u.Query()
	sni := firstNonEmpty(q.Get("sni"), q.Get("peer"))
	stream := buildXrayStream(q.Get("type"), "tls", q.Get("host"), q.Get("path"), sni, q.Get("fp"), q.Get("alpn"), "", "", "", "")
	return coreObj.OutboundObject{
		Tag: tag, Protocol: "trojan",
		Settings: coreObj.OutboundSettings{
			Servers: []coreObj.ServerObject{{Address: u.Hostname(), Port: strToInt(u.Port()), Password: u.User.Username()}},
		},
		StreamSettings: stream,
	}, nil
}

func xraySocks(s *configure.ServerRaw, tag string) (coreObj.OutboundObject, error) {
	var host string
	var port int
	var username, password string

	// 对于 clash:// 或 singbox:// 格式，需要从 Link 中提取凭证
	if strings.HasPrefix(s.Link, "clash://") {
		host = s.Host
		port = s.Port
		username, password = extractClashCredentials(s.Link)
		if LogCallback != nil {
			LogCallback(fmt.Sprintf("🔑 SOCKS 从 Clash 格式提取凭证 - Username: '%s', Password: '%s' (长度: %d)", username, password, len(password)))
		}
	} else if strings.HasPrefix(s.Link, "singbox://") {
		host = s.Host
		port = s.Port
		username, password = extractSingboxCredentials(s.Link)
		if LogCallback != nil {
			LogCallback(fmt.Sprintf("🔑 SOCKS 从 Singbox 格式提取凭证 - Username: '%s', Password: '%s' (长度: %d)", username, password, len(password)))
		}
	} else {
		// 标准 URI 格式 - 直接使用 s.Link 而不是重新构建
		u, err := url.Parse(s.Link)
		if err != nil || u.Hostname() == "" {
			if LogCallback != nil {
				LogCallback(fmt.Sprintf("❌ SOCKS URL 解析失败: %v, 尝试使用 Host/Port", err))
			}
			// 如果 URL 解析失败，使用 s.Host 和 s.Port
			host = s.Host
			port = s.Port
		} else {
			host = u.Hostname()
			portStr := u.Port()
			if portStr != "" {
				port = strToInt(portStr)
			} else {
				port = s.Port
			}
			if u.User != nil {
				username = u.User.Username()
				password, _ = u.User.Password()
			}
		}
	}

	if host == "" || port == 0 {
		return coreObj.OutboundObject{}, fmt.Errorf("invalid socks link: missing host or port")
	}

	srv := coreObj.ServerObject{Address: host, Port: port}
	if username != "" || password != "" {
		srv.Users = []coreObj.SocksUser{{User: username, Pass: password}}
		if LogCallback != nil {
			LogCallback(fmt.Sprintf("✅ SOCKS 服务器配置 - Address: %s:%d, User: %s", host, port, username))
		}
	} else {
		if LogCallback != nil {
			LogCallback(fmt.Sprintf("⚠️ SOCKS 服务器配置（无凭证）- Address: %s:%d", host, port))
		}
	}

	return coreObj.OutboundObject{Tag: tag, Protocol: "socks",
		Settings: coreObj.OutboundSettings{Servers: []coreObj.ServerObject{srv}}}, nil
}

func xrayHTTP(s *configure.ServerRaw, tag string) (coreObj.OutboundObject, error) {
	var host string
	var port int
	var username, password string

	// 调试：打印节点信息
	if LogCallback != nil {
		LogCallback(fmt.Sprintf("🔍 HTTP 节点信息 - Type: %s, Host: %s, Port: %d, Link前100字: %s",
			s.Type, s.Host, s.Port, s.Link[:min(len(s.Link), 100)]))
	}

	// 对于 clash:// 或 singbox:// 格式，需要从 Link 中提取凭证
	if strings.HasPrefix(s.Link, "clash://") {
		host = s.Host
		port = s.Port
		username, password = extractClashCredentials(s.Link)
		if LogCallback != nil {
			LogCallback(fmt.Sprintf("🔑 从 Clash 格式提取凭证 - Username: '%s', Password: '%s' (长度: %d)", username, password, len(password)))
		}
	} else if strings.HasPrefix(s.Link, "singbox://") {
		host = s.Host
		port = s.Port
		// Singbox 格式的凭证也需要提取
		username, password = extractSingboxCredentials(s.Link)
		if LogCallback != nil {
			LogCallback(fmt.Sprintf("🔑 从 Singbox 格式提取凭证 - Username: '%s', Password: '%s' (长度: %d)", username, password, len(password)))
		}
	} else {
		// 标准 URI 格式 - 直接使用 s.Link 而不是重新构建
		u, err := url.Parse(s.Link)
		if err != nil || u.Hostname() == "" {
			if LogCallback != nil {
				LogCallback(fmt.Sprintf("❌ URL 解析失败: %v, 尝试使用 Host/Port", err))
			}
			// 如果 URL 解析失败，使用 s.Host 和 s.Port
			host = s.Host
			port = s.Port
		} else {
			host = u.Hostname()
			portStr := u.Port()
			if portStr != "" {
				port = strToInt(portStr)
			} else {
				port = s.Port
			}
			if u.User != nil {
				username = u.User.Username()
				password, _ = u.User.Password()
			}
		}
		if LogCallback != nil {
			LogCallback(fmt.Sprintf("🔑 从标准 URI 提取凭证 - Username: '%s', Password: '%s' (长度: %d)", username, password, len(password)))
		}
	}

	if host == "" || port == 0 {
		return coreObj.OutboundObject{}, fmt.Errorf("invalid http link: missing host or port")
	}

	srv := coreObj.ServerObject{Address: host, Port: port}
	if username != "" || password != "" {
		srv.Users = []coreObj.SocksUser{{User: username, Pass: password}}
		if LogCallback != nil {
			LogCallback(fmt.Sprintf("✅ HTTP 服务器配置 - Address: %s:%d, User: %s", host, port, username))
		}
	} else {
		if LogCallback != nil {
			LogCallback(fmt.Sprintf("⚠️ HTTP 服务器配置（无凭证）- Address: %s:%d", host, port))
		}
	}

	// 根据协议类型设置 TLS
	protocol := "http"
	var streamSettings *coreObj.StreamSettings
	if strings.ToLower(s.Type) == "https" {
		protocol = "http"
		streamSettings = &coreObj.StreamSettings{
			Security: "tls",
			TLSSettings: &coreObj.TLSObject{
				ServerName: host,
			},
		}
		if LogCallback != nil {
			LogCallback(fmt.Sprintf("🔒 HTTPS 配置 - TLS 已启用, ServerName: %s", host))
		}
	}

	return coreObj.OutboundObject{
		Tag: tag, Protocol: protocol,
		Settings:       coreObj.OutboundSettings{Servers: []coreObj.ServerObject{srv}},
		StreamSettings: streamSettings,
	}, nil
}

// min 返回两个整数中的较小值
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// extractClashCredentials 从 Clash YAML 格式中提取用户名和密码
func extractClashCredentials(link string) (string, string) {
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

// extractSingboxCredentials 从 Singbox JSON 格式中提取用户名和密码
func extractSingboxCredentials(link string) (string, string) {
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

func buildXrayStream(network, security, host, path, sni, fp, alpn, pbk, sid, spx, serviceName string) *coreObj.StreamSettings {
	if network == "" {
		network = "tcp"
	}
	ss := &coreObj.StreamSettings{Network: network}
	switch security {
	case "tls":
		ss.Security = "tls"
		ss.TLSSettings = &coreObj.TLSObject{ServerName: sni, Fingerprint: fp}
		if alpn != "" {
			ss.TLSSettings.ALPN = strings.Split(alpn, ",")
		}
	case "reality":
		ss.Security = "reality"
		ss.RealitySettings = &coreObj.RealityObject{ServerName: sni, Fingerprint: fp, PublicKey: pbk, ShortID: sid, SpiderX: spx}
	}
	switch network {
	case "ws":
		ss.WSSettings = &coreObj.WSObject{Path: path, Headers: map[string]string{"Host": host}}
	case "grpc":
		ss.GRPCSettings = &coreObj.GRPCObject{ServiceName: serviceName}
	case "xhttp", "splithttp":
		ss.Network = "xhttp"
		ss.XHTTPSettings = &coreObj.XHTTPObject{Host: host, Path: path}
	case "http", "h2":
		ss.Network = "http"
	}
	return ss
}

// helpers
func b64Decode(s string) (string, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.URLEncoding,
		base64.RawStdEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return string(b), nil
		}
	}
	return "", fmt.Errorf("base64 decode failed")
}

func anyToInt(v interface{}) int {
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case string:
		n := 0
		fmt.Sscanf(val, "%d", &n)
		return n
	}
	return 0
}

func strToInt(s string) int {
	n := 0
	fmt.Sscanf(s, "%d", &n)
	return n
}

func splitHostPort(hostport string) (string, int) {
	if idx := strings.LastIndex(hostport, ":"); idx >= 0 {
		return hostport[:idx], strToInt(hostport[idx+1:])
	}
	return hostport, 0
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
