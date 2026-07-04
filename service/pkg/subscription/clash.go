package subscription

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ProxyStation/proxystation/db/configure"
	"gopkg.in/yaml.v3"
)

// ClashParser 解析 Clash YAML 格式订阅
// 支持 Clash/Clash.Meta/Mihomo 的 proxies 字段
type ClashParser struct{}

func (p *ClashParser) Format() configure.SubscriptionFormat {
	return configure.FormatClash
}

func (p *ClashParser) Detect(content []byte) bool {
	// Clash YAML 必须包含 proxies: 字段
	return bytes.Contains(content, []byte("proxies:"))
}

type clashConfig struct {
	Proxies []map[string]interface{} `yaml:"proxies"`
}

func (p *ClashParser) Parse(content []byte) ([]configure.ServerRaw, error) {
	var cfg clashConfig
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		if repaired, ok := tryRepairClashIndentation(content); ok {
			if repairErr := yaml.Unmarshal(repaired, &cfg); repairErr == nil {
				return p.parseConfig(cfg), nil
			}
		}
		if hint := detectClashIndentationIssue(content); hint != "" {
			return nil, fmt.Errorf("clash parse error: %w (%s)", err, hint)
		}
		return nil, fmt.Errorf("clash parse error: %w", err)
	}
	return p.parseConfig(cfg), nil
}

func (p *ClashParser) parseConfig(cfg clashConfig) []configure.ServerRaw {
	var servers []configure.ServerRaw
	for _, proxy := range cfg.Proxies {
		s := clashProxyToServerRaw(proxy)
		if s != nil {
			servers = append(servers, *s)
		}
	}
	return servers
}

func detectClashIndentationIssue(content []byte) string {
	lines := strings.Split(string(content), "\n")
	inProxies := false
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "proxies:" {
			inProxies = true
			continue
		}
		if !inProxies {
			continue
		}
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			inProxies = false
			continue
		}
		if strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "  ") && strings.Contains(trimmed, ":") {
			return fmt.Sprintf("likely invalid indentation under proxies near line %d; fields under each '- name' entry must be indented by two spaces", i+1)
		}
	}
	return ""
}

func tryRepairClashIndentation(content []byte) ([]byte, bool) {
	lines := strings.Split(string(content), "\n")
	fixed := make([]string, 0, len(lines))
	changed := false

	inTopLevelProxies := false
	inProxyGroups := false
	inGroupItem := false
	inNestedProxies := false

	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)

		if trimmed == "proxies:" && !strings.HasPrefix(line, " ") {
			inTopLevelProxies = true
			inProxyGroups = false
			inGroupItem = false
			inNestedProxies = false
			fixed = append(fixed, line)
			continue
		}
		if trimmed == "proxy-groups:" && !strings.HasPrefix(line, " ") {
			inTopLevelProxies = false
			inProxyGroups = true
			inGroupItem = false
			inNestedProxies = false
			fixed = append(fixed, line)
			continue
		}

		if inTopLevelProxies {
			switch {
			case strings.HasPrefix(line, "- "):
				fixed = append(fixed, line)
				continue
			case strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "  ") && strings.Contains(trimmed, ":"):
				fixed = append(fixed, " "+line)
				changed = true
				continue
			case trimmed == "":
				fixed = append(fixed, line)
				continue
			case !strings.HasPrefix(line, " "):
				inTopLevelProxies = false
			}
		}

		if inProxyGroups {
			switch {
			case strings.HasPrefix(line, "- "):
				inGroupItem = true
				inNestedProxies = false
				fixed = append(fixed, line)
				continue
			case inGroupItem && strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "  ") && strings.Contains(trimmed, ":"):
				line = " " + line
				changed = true
				if strings.TrimSpace(line) == "proxies:" {
					inNestedProxies = true
				}
				fixed = append(fixed, line)
				continue
			case inGroupItem && strings.TrimSpace(line) == "proxies:" && strings.HasPrefix(line, "  "):
				inNestedProxies = true
				fixed = append(fixed, line)
				continue
			case inGroupItem && inNestedProxies && strings.HasPrefix(line, " - "):
				fixed = append(fixed, "   "+line)
				changed = true
				continue
			case inGroupItem && inNestedProxies && strings.HasPrefix(line, "  - "):
				fixed = append(fixed, "  "+line)
				changed = true
				continue
			case trimmed == "":
				fixed = append(fixed, line)
				continue
			case !strings.HasPrefix(line, " "):
				inProxyGroups = false
				inGroupItem = false
				inNestedProxies = false
			case inNestedProxies && strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   "):
				inNestedProxies = false
			}
		}

		fixed = append(fixed, line)
	}

	if !changed {
		return nil, false
	}
	return []byte(strings.Join(fixed, "\n")), true
}

func clashProxyToServerRaw(proxy map[string]interface{}) *configure.ServerRaw {
	name, _ := proxy["name"].(string)
	proxyType, _ := proxy["type"].(string)
	server, _ := proxy["server"].(string)

	var port int
	switch v := proxy["port"].(type) {
	case int:
		port = v
	case float64:
		port = int(v)
	case string:
		port, _ = strconv.Atoi(v)
	}

	if server == "" || proxyType == "" {
		return nil
	}

	// 处理 HTTP/HTTPS 的特殊情况：如果协议是 http 但 tls 为 true，则应该是 https
	normalizedType := normalizeClashType(proxyType)
	if normalizedType == "http" {
		if tls, ok := proxy["tls"].(bool); ok && tls {
			normalizedType = "https"
		}
	}

	// 将 clash proxy map 序列化为 link 字符串（简化存储，后续可扩展为完整解析）
	// 实际使用时可以根据 type 构造对应的 URI
	return &configure.ServerRaw{
		Link: buildClashLink(proxy),
		Name: name,
		Host: server,
		Port: port,
		Type: normalizedType,
	}
}

// normalizeClashType 将 clash 协议名统一为内部协议名
func normalizeClashType(t string) string {
	switch t {
	case "ss":
		return "ss"
	case "ssr":
		return "ssr"
	case "vmess":
		return "vmess"
	case "vless":
		return "vless"
	case "trojan":
		return "trojan"
	case "hysteria":
		return "hysteria"
	case "hysteria2", "hy2":
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

// buildClashLink 将 clash proxy 配置序列化为 base64 编码的 JSON 存储
// 后续连接时再完整解析
func buildClashLink(proxy map[string]interface{}) string {
	data, err := json.Marshal(proxy)
	if err != nil {
		return ""
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	return "clash://" + encoded
}
