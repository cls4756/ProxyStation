// config_builder.go 负责根据 ProxyStation 的配置生成 v2ray-core 的 config.json
package v2ray

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/ProxyStation/proxystation/core/coreObj"
	"github.com/ProxyStation/proxystation/db/configure"
)

// BuildConfig 根据当前出站配置生成 v2ray config.json
func BuildConfig(setting *configure.Setting) (*coreObj.Config, error) {
	if setting == nil {
		setting = configure.GetSettingNotNil()
	}

	cfg := &coreObj.Config{
		Log: &coreObj.Log{
			Loglevel: setting.LogLevel,
			Error:    "none",
		},
		Routing: coreObj.Routing{
			DomainStrategy: "IPIfNonMatch",
		},
	}

	// 基础入站：socks5 + http
	cfg.Inbounds = []coreObj.Inbound{
		{
			Tag:      "socks",
			Port:     setting.Socks5Port,
			Listen:   "127.0.0.1",
			Protocol: "socks",
			Settings: &coreObj.InboundSettings{
				UDP:  true,
				Auth: "noauth",
			},
			Sniffing: coreObj.Sniffing{
				Enabled:      true,
				DestOverride: []string{"http", "tls"},
			},
		},
		{
			Tag:      "http",
			Port:     setting.HttpPort,
			Listen:   "127.0.0.1",
			Protocol: "http",
			Sniffing: coreObj.Sniffing{
				Enabled:      true,
				DestOverride: []string{"http", "tls"},
			},
		},
	}

	// 为默认入站添加认证
	socksAccounts := setting.Socks5AuthAccounts()
	if len(socksAccounts) > 0 {
		cfg.Inbounds[0].Settings.Auth = "password"
		cfg.Inbounds[0].Settings.Accounts = make([]coreObj.InboundAccount, 0, len(socksAccounts))
		for _, a := range socksAccounts {
			cfg.Inbounds[0].Settings.Accounts = append(cfg.Inbounds[0].Settings.Accounts, coreObj.InboundAccount{
				User: a.Username, Pass: a.Password,
			})
		}
	}
	httpAccounts := setting.HTTPAuthAccounts()
	if len(httpAccounts) > 0 {
		cfg.Inbounds[1].Settings = &coreObj.InboundSettings{
			Auth: "basic",
			Accounts: make([]coreObj.InboundAccount, 0, len(httpAccounts)),
		}
		for _, a := range httpAccounts {
			cfg.Inbounds[1].Settings.Accounts = append(cfg.Inbounds[1].Settings.Accounts, coreObj.InboundAccount{
				User: a.Username, Pass: a.Password,
			})
		}
	}

	// 添加自定义入站
	for _, ci := range configure.GetCustomInbounds() {
		accounts := ci.AuthAccounts()
		ib := coreObj.Inbound{
			Tag:      ci.Tag,
			Port:     ci.Port,
			Listen:   ci.Listen,
			Protocol: ci.Protocol,
			Sniffing: coreObj.Sniffing{
				Enabled:      ci.SniffEnabled,
				DestOverride: ci.SniffDest,
			},
		}
		if ib.Listen == "" {
			ib.Listen = "127.0.0.1"
		}
		// 根据协议类型设置 Settings
		switch ci.Protocol {
		case "socks":
			ib.Settings = &coreObj.InboundSettings{
				UDP:  ci.UDPEnabled,
				Auth: "noauth",
			}
			if len(accounts) > 0 {
				ib.Settings.Auth = "password"
				ib.Settings.Accounts = make([]coreObj.InboundAccount, 0, len(accounts))
				for _, a := range accounts {
					ib.Settings.Accounts = append(ib.Settings.Accounts, coreObj.InboundAccount{User: a.Username, Pass: a.Password})
				}
			}
		case "http":
			if len(accounts) > 0 {
				ib.Settings = &coreObj.InboundSettings{
					Auth: "basic",
					Accounts: make([]coreObj.InboundAccount, 0, len(accounts)),
				}
				for _, a := range accounts {
					ib.Settings.Accounts = append(ib.Settings.Accounts, coreObj.InboundAccount{User: a.Username, Pass: a.Password})
				}
			}
		case "dokodemo-door":
			ib.Settings = &coreObj.InboundSettings{
				Network:        ci.Network,
				FollowRedirect: ci.FollowRedirect,
			}
		}
		cfg.Inbounds = append(cfg.Inbounds, ib)
	}

	// 固定出站：direct + block
	cfg.Outbounds = []coreObj.OutboundObject{
		{Tag: "direct", Protocol: "freedom"},
		{Tag: "block", Protocol: "blackhole"},
	}

	// 路由规则：局域网直连（不依赖 geoip.dat）
	cfg.Routing.Rules = []coreObj.RoutingRule{
		// 私有 IP 直连
		{
			Type:        "field",
			OutboundTag: "direct",
			IP: []string{
				"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
				"127.0.0.0/8", "169.254.0.0/16", "224.0.0.0/4", "240.0.0.0/4",
			},
		},
		// IPv6 本地地址
		{
			Type:        "field",
			OutboundTag: "direct",
			IP: []string{
				"::1/128", "fc00::/7", "fe80::/10",
			},
		},
	}

	// 遍历所有出站，生成对应的 v2ray outbound
	outboundNames := configure.GetOutboundNames()
	for _, name := range outboundNames {
		o := configure.GetOutbound(name)
		if o == nil {
			continue
		}

		// 确定实际使用的节点
		nodeRef := resolveActiveNode(o)
		if nodeRef == nil {
			continue
		}

		serverRaw := getServerRaw(nodeRef)
		if serverRaw == nil {
			continue
		}

		outbound, err := serverRawToOutbound(serverRaw, name)
		if err != nil {
			return nil, fmt.Errorf("outbound %v: %w", name, err)
		}
		cfg.Outbounds = append([]coreObj.OutboundObject{outbound}, cfg.Outbounds...)

		// 路由：入站流量 -> 该出站
		inboundTags := []string{"socks", "http"}
		if name == "proxy" {
			// 默认出站：未匹配的流量走这里
			cfg.Routing.Rules = append(cfg.Routing.Rules, coreObj.RoutingRule{
				Type:        "field",
				OutboundTag: name,
				Port:        "0-65535",
			})
		} else {
			// 自定义出站：需要用户在路由规则里指定，这里只注册 outbound
			_ = inboundTags
		}
	}

	return cfg, nil
}

// resolveActiveNode 确定出站当前实际使用的节点
func resolveActiveNode(o *configure.Outbound) *configure.NodeRef {
	switch o.Target.TargetType {
	case "node":
		return o.Target.NodeRef
	case "group":
		// 优先用 observatory 维护的活跃节点
		if o.Target.ActiveNodeRef != nil {
			return o.Target.ActiveNodeRef
		}
		// 否则取分组第一个节点
		_, group := configure.GetGroupByID(o.Target.GroupID)
		if group == nil || len(group.Servers) == 0 {
			return nil
		}
		ref := group.Servers[0]
		return &configure.NodeRef{
			Type:  ref.Type,
			Index: ref.Index,
			Sub:   ref.Sub,
		}
	}
	return nil
}

// getServerRaw 根据 NodeRef 取出 ServerRaw
func getServerRaw(ref *configure.NodeRef) *configure.ServerRaw {
	switch ref.Type {
	case "server":
		return configure.GetServer(ref.Index)
	case "sub_server":
		sub := configure.GetSubscription(ref.Sub)
		if sub == nil || ref.Index >= len(sub.Servers) {
			return nil
		}
		s := sub.Servers[ref.Index]
		return &s
	}
	return nil
}

// serverRawToOutbound 将 ServerRaw 转换为 v2ray OutboundObject
// 支持 vmess / vless / ss / trojan / socks5 / http / hysteria2 / tuic / wireguard / clash / singbox
func serverRawToOutbound(s *configure.ServerRaw, tag string) (coreObj.OutboundObject, error) {
	switch strings.ToLower(s.Type) {
	case "vmess":
		return buildVmessOutbound(s, tag)
	case "vless":
		return buildVlessOutbound(s, tag)
	case "ss", "shadowsocks":
		return buildSSOutbound(s, tag)
	case "trojan":
		return buildTrojanOutbound(s, tag)
	case "socks5", "socks", "socks4":
		return buildSocksOutbound(s, tag)
	case "http", "https", "naive":
		return buildHTTPOutbound(s, tag)
	case "hysteria2", "hy2", "hysteria":
		// hysteria2 需要 sing-box 或 xray 支持，v2ray-core 不原生支持
		// 这里生成一个占位出站，实际使用时需要 sing-box 内核
		return buildUnsupportedOutbound(s, tag, "hysteria2/tuic 需要 sing-box 内核支持")
	case "tuic":
		return buildUnsupportedOutbound(s, tag, "tuic 需要 sing-box 内核支持")
	case "wireguard":
		return buildUnsupportedOutbound(s, tag, "wireguard 需要 sing-box/xray 内核支持")
	default:
		return coreObj.OutboundObject{}, fmt.Errorf("unsupported protocol: %v", s.Type)
	}
}

// buildVmessOutbound 解析 vmess:// 链接
func buildVmessOutbound(s *configure.ServerRaw, tag string) (coreObj.OutboundObject, error) {
	// vmess:// 是 base64(json)
	link := s.Link
	if !strings.HasPrefix(strings.ToLower(link), "vmess://") {
		return coreObj.OutboundObject{}, fmt.Errorf("invalid vmess link")
	}
	b64 := link[8:]
	decoded, err := base64Decode(b64)
	if err != nil {
		return coreObj.OutboundObject{}, fmt.Errorf("vmess decode: %w", err)
	}
	var v struct {
		Add  string      `json:"add"`
		Port interface{} `json:"port"`
		ID   string      `json:"id"`
		Aid  interface{} `json:"aid"`
		Net  string      `json:"net"`
		Type string      `json:"type"`
		Host string      `json:"host"`
		Path string      `json:"path"`
		TLS  string      `json:"tls"`
		SNI  string      `json:"sni"`
		PS   string      `json:"ps"`
		Fp   string      `json:"fp"`
	}
	if err := json.Unmarshal(decoded, &v); err != nil {
		return coreObj.OutboundObject{}, fmt.Errorf("vmess json: %w", err)
	}
	port := toInt(v.Port)
	aid := toInt(v.Aid)

	user := coreObj.VnextUser{
		ID:       v.ID,
		AlterID:  aid,
		Security: "auto",
	}

	stream := buildStreamSettings(v.Net, v.TLS, v.Host, v.Path, v.SNI, v.Fp, "")

	return coreObj.OutboundObject{
		Tag:      tag,
		Protocol: "vmess",
		Settings: coreObj.OutboundSettings{
			Vnext: []coreObj.VnextObject{{
				Address: v.Add,
				Port:    port,
				Users:   []coreObj.VnextUser{user},
			}},
		},
		StreamSettings: stream,
	}, nil
}

// buildVlessOutbound 解析 vless:// 链接
func buildVlessOutbound(s *configure.ServerRaw, tag string) (coreObj.OutboundObject, error) {
	u, err := url.Parse(s.Link)
	if err != nil {
		return coreObj.OutboundObject{}, fmt.Errorf("vless parse: %w", err)
	}
	q := u.Query()
	port := toInt(u.Port())
	flow := q.Get("flow")
	fp := q.Get("fp")
	sni := q.Get("sni")
	if sni == "" {
		sni = q.Get("host")
	}
	net := q.Get("type")
	if net == "" {
		net = "tcp"
	}
	security := q.Get("security")
	path := q.Get("path")
	host := q.Get("host")
	pbk := q.Get("pbk")
	sid := q.Get("sid")
	spx := q.Get("spx")

	user := coreObj.VnextUser{
		ID:         u.User.Username(),
		Encryption: "none",
		Flow:       flow,
	}

	stream := buildStreamSettings(net, security, host, path, sni, fp, "")
	if security == "reality" && stream != nil {
		stream.RealitySettings = &coreObj.RealityObject{
			ServerName:  sni,
			Fingerprint: fp,
			PublicKey:   pbk,
			ShortID:     sid,
			SpiderX:     spx,
		}
		stream.TLSSettings = nil
	}

	return coreObj.OutboundObject{
		Tag:      tag,
		Protocol: "vless",
		Settings: coreObj.OutboundSettings{
			Vnext: []coreObj.VnextObject{{
				Address: u.Hostname(),
				Port:    port,
				Users:   []coreObj.VnextUser{user},
			}},
		},
		StreamSettings: stream,
	}, nil
}

// buildSSOutbound 解析 ss:// 链接
func buildSSOutbound(s *configure.ServerRaw, tag string) (coreObj.OutboundObject, error) {
	link := s.Link
	// ss://BASE64(method:password)@host:port#name
	// or ss://BASE64(method:password@host:port)#name
	if idx := strings.Index(link, "#"); idx != -1 {
		link = link[:idx]
	}
	link = strings.TrimPrefix(link, "ss://")

	var method, password, host string
	var port int

	if atIdx := strings.LastIndex(link, "@"); atIdx != -1 {
		// userinfo@host:port
		userinfo := link[:atIdx]
		hostport := link[atIdx+1:]
		decoded, err := base64Decode(userinfo)
		if err != nil {
			// not base64, plain text
			decoded = []byte(userinfo)
		}
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) == 2 {
			method = parts[0]
			password = parts[1]
		}
		if h, p, err := splitHostPort(hostport); err == nil {
			host = h
			port = p
		}
	} else {
		// all base64
		decoded, err := base64Decode(link)
		if err != nil {
			return coreObj.OutboundObject{}, fmt.Errorf("ss decode: %w", err)
		}
		// method:password@host:port
		if atIdx := strings.LastIndex(string(decoded), "@"); atIdx != -1 {
			userinfo := string(decoded)[:atIdx]
			hostport := string(decoded)[atIdx+1:]
			parts := strings.SplitN(userinfo, ":", 2)
			if len(parts) == 2 {
				method = parts[0]
				password = parts[1]
			}
			if h, p, err := splitHostPort(hostport); err == nil {
				host = h
				port = p
			}
		}
	}

	return coreObj.OutboundObject{
		Tag:      tag,
		Protocol: "shadowsocks",
		Settings: coreObj.OutboundSettings{
			Servers: []coreObj.ServerObject{{
				Address:  host,
				Port:     port,
				Password: password,
				Method:   method,
			}},
		},
	}, nil
}

// buildTrojanOutbound 解析 trojan:// 链接
func buildTrojanOutbound(s *configure.ServerRaw, tag string) (coreObj.OutboundObject, error) {
	u, err := url.Parse(s.Link)
	if err != nil {
		return coreObj.OutboundObject{}, fmt.Errorf("trojan parse: %w", err)
	}
	q := u.Query()
	sni := q.Get("sni")
	if sni == "" {
		sni = q.Get("peer")
	}
	fp := q.Get("fp")
	net := q.Get("type")
	path := q.Get("path")
	host := q.Get("host")

	stream := buildStreamSettings(net, "tls", host, path, sni, fp, "")

	return coreObj.OutboundObject{
		Tag:      tag,
		Protocol: "trojan",
		Settings: coreObj.OutboundSettings{
			Servers: []coreObj.ServerObject{{
				Address:  u.Hostname(),
				Port:     toInt(u.Port()),
				Password: u.User.Username(),
			}},
		},
		StreamSettings: stream,
	}, nil
}

// buildSocksOutbound 解析 socks5:// / socks:// 链接
func buildSocksOutbound(s *configure.ServerRaw, tag string) (coreObj.OutboundObject, error) {
	u, err := url.Parse(s.Link)
	if err != nil {
		return coreObj.OutboundObject{}, fmt.Errorf("socks parse: %w", err)
	}
	server := coreObj.ServerObject{
		Address: u.Hostname(),
		Port:    toInt(u.Port()),
	}
	if u.User != nil {
		server.Users = []coreObj.SocksUser{{
			User: u.User.Username(),
			Pass: func() string { p, _ := u.User.Password(); return p }(),
		}}
	}
	return coreObj.OutboundObject{
		Tag:      tag,
		Protocol: "socks",
		Settings: coreObj.OutboundSettings{
			Servers: []coreObj.ServerObject{server},
		},
	}, nil
}

// buildHTTPOutbound 解析 http:// / https:// 代理节点链接
func buildHTTPOutbound(s *configure.ServerRaw, tag string) (coreObj.OutboundObject, error) {
	u, err := url.Parse(s.Link)
	if err != nil {
		return coreObj.OutboundObject{}, fmt.Errorf("http proxy parse: %w", err)
	}
	server := coreObj.ServerObject{
		Address: u.Hostname(),
		Port:    toInt(u.Port()),
	}
	if u.User != nil {
		server.Users = []coreObj.SocksUser{{
			User: u.User.Username(),
			Pass: func() string { p, _ := u.User.Password(); return p }(),
		}}
	}
	return coreObj.OutboundObject{
		Tag:      tag,
		Protocol: "http",
		Settings: coreObj.OutboundSettings{
			Servers: []coreObj.ServerObject{server},
		},
	}, nil
}

// buildUnsupportedOutbound 对不支持的协议生成 blackhole 占位，并记录原因
func buildUnsupportedOutbound(s *configure.ServerRaw, tag, reason string) (coreObj.OutboundObject, error) {
	// 返回 blackhole，避免流量泄漏，同时不让整个配置生成失败
	_ = reason // 可以后续写入日志
	return coreObj.OutboundObject{
		Tag:      tag,
		Protocol: "blackhole",
	}, nil
}

func buildStreamSettings(network, security, host, path, sni, fp, alpn string) *coreObj.StreamSettings {
	if network == "" {
		network = "tcp"
	}
	ss := &coreObj.StreamSettings{
		Network: network,
	}
	if security == "tls" || security == "reality" {
		ss.Security = security
		ss.TLSSettings = &coreObj.TLSObject{
			ServerName:  sni,
			Fingerprint: fp,
		}
		if alpn != "" {
			ss.TLSSettings.ALPN = strings.Split(alpn, ",")
		}
	}
	switch network {
	case "ws":
		ss.WSSettings = &coreObj.WSObject{
			Path:    path,
			Headers: map[string]string{"Host": host},
		}
	case "grpc":
		ss.GRPCSettings = &coreObj.GRPCObject{
			ServiceName: path,
		}
	case "http", "h2":
		ss.Network = "http"
	}
	return ss
}
