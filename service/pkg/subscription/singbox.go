package subscription

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/ProxyStation/proxystation/db/configure"
)

// SingboxParser 解析 sing-box JSON 格式订阅
type SingboxParser struct{}

func (p *SingboxParser) Format() configure.SubscriptionFormat {
	return configure.FormatSingbox
}

func (p *SingboxParser) Detect(content []byte) bool {
	// sing-box JSON 包含 "outbounds" 字段
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	return bytes.Contains(content, []byte(`"outbounds"`))
}

type singboxConfig struct {
	Outbounds []map[string]interface{} `json:"outbounds"`
}

// sing-box 中不作为代理节点的出站类型
var singboxNonProxyTypes = map[string]bool{
	"direct": true, "block": true, "dns": true,
	"selector": true, "urltest": true,
}

func (p *SingboxParser) Parse(content []byte) ([]configure.ServerRaw, error) {
	var cfg singboxConfig
	if err := json.Unmarshal(content, &cfg); err != nil {
		return nil, fmt.Errorf("singbox parse error: %w", err)
	}
	var servers []configure.ServerRaw
	for _, outbound := range cfg.Outbounds {
		outType, _ := outbound["type"].(string)
		if singboxNonProxyTypes[outType] {
			continue
		}
		s := singboxOutboundToServerRaw(outbound)
		if s != nil {
			servers = append(servers, *s)
		}
	}
	return servers, nil
}

func singboxOutboundToServerRaw(outbound map[string]interface{}) *configure.ServerRaw {
	tag, _ := outbound["tag"].(string)
	outType, _ := outbound["type"].(string)
	server, _ := outbound["server"].(string)

	var port int
	switch v := outbound["server_port"].(type) {
	case float64:
		port = int(v)
	case string:
		port, _ = strconv.Atoi(v)
	}

	if server == "" || outType == "" {
		return nil
	}

	// 处理 HTTP/HTTPS 的特殊情况：如果协议是 http 但 tls 为 true，则应该是 https
	normalizedType := normalizeSingboxType(outType)
	if normalizedType == "http" {
		if tlsSettings, ok := outbound["tls"].(map[string]interface{}); ok && tlsSettings != nil {
			normalizedType = "https"
		}
	}

	data, _ := json.Marshal(outbound)
	encoded := base64.StdEncoding.EncodeToString(data)
	return &configure.ServerRaw{
		Link: "singbox://" + encoded,
		Name: tag,
		Host: server,
		Port: port,
		Type: normalizedType,
	}
}

func normalizeSingboxType(t string) string {
	switch t {
	case "shadowsocks":
		return "ss"
	case "vmess":
		return "vmess"
	case "vless":
		return "vless"
	case "trojan":
		return "trojan"
	case "hysteria":
		return "hysteria"
	case "hysteria2":
		return "hysteria2"
	case "tuic":
		return "tuic"
	case "wireguard":
		return "wireguard"
	case "http":
		return "http"
	case "https":
		return "https"
	case "socks", "socks5":
		return "socks5"
	case "socks4":
		return "socks4"
	case "anytls":
		return "anytls"
	case "naive":
		return "naive"
	case "juicity":
		return "juicity"
	default:
		return t
	}
}
