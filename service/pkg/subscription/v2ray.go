package subscription

import (
	"encoding/base64"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/ProxyStation/proxystation/db/configure"
)

// V2rayParser 解析 base64 编码的节点链接列表
// 每行一个链接，如 vmess://... vless://... ss://... trojan://...
type V2rayParser struct{}

func (p *V2rayParser) Format() configure.SubscriptionFormat {
	return configure.FormatV2ray
}

func (p *V2rayParser) Detect(content []byte) bool {
	decoded, err := tryBase64Decode(content)
	if err != nil {
		decoded = content
	}
	lines := strings.Split(strings.TrimSpace(string(decoded)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if isKnownScheme(line) {
			return true
		}
	}
	return false
}

func (p *V2rayParser) Parse(content []byte) ([]configure.ServerRaw, error) {
	decoded, err := tryBase64Decode(content)
	if err != nil {
		decoded = content
	}
	lines := strings.Split(strings.TrimSpace(string(decoded)), "\n")
	var servers []configure.ServerRaw
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !isKnownScheme(line) {
			continue
		}
		s := parseLink(line)
		if s != nil {
			servers = append(servers, *s)
		}
	}
	return servers, nil
}

func tryBase64Decode(content []byte) ([]byte, error) {
	s := strings.TrimSpace(string(content))
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, base64.CorruptInputError(0)
}

// 所有已知代理节点协议前缀（小写）
var knownSchemes = []string{
	"vmess://", "vless://",
	"ss://", "ssr://",
	"trojan://", "trojan-go://",
	"hysteria://", "hysteria2://", "hy2://",
	"tuic://",
	"juicity://",
	"wireguard://",
	"anytls://",
	"socks://", "socks5://", "socks4://",
	"http://", "https://",
	"naive+https://", "naive+http://",
}

func isKnownScheme(s string) bool {
	lower := strings.ToLower(s)
	for _, scheme := range knownSchemes {
		if strings.HasPrefix(lower, scheme) {
			return true
		}
	}
	return false
}

// parseLink 将单条链接解析为 ServerRaw，提取 host/port/name/type
func parseLink(link string) *configure.ServerRaw {
	lower := strings.ToLower(link)
	var protocol string
	for _, scheme := range knownSchemes {
		if strings.HasPrefix(lower, scheme) {
			// 规范化协议名
			protocol = normalizeProtocol(strings.TrimSuffix(scheme, "://"))
			break
		}
	}
	if protocol == "" {
		return nil
	}

	host, port := extractHostPort(link, protocol)
	name := extractName(link)

	// 处理 HTTP/HTTPS 的特殊情况：检查 URL 中是否有 tls=true 参数
	if protocol == "http" {
		u, err := url.Parse(link)
		if err == nil {
			q := u.Query()
			if tlsVal := q.Get("tls"); tlsVal == "true" || tlsVal == "1" {
				protocol = "https"
			}
		}
	}

	return &configure.ServerRaw{
		Link: link,
		Type: protocol,
		Name: name,
		Host: host,
		Port: port,
	}
}

func normalizeProtocol(scheme string) string {
	switch scheme {
	case "hy2":
		return "hysteria2"
	case "trojan-go":
		return "trojan"
	case "socks", "socks5":
		return "socks5"
	case "socks4":
		return "socks4"
	case "naive+https", "naive+http":
		return "naive"
	default:
		return scheme
	}
}

// extractHostPort 从链接中提取 host 和 port
func extractHostPort(link, protocol string) (string, int) {
	// vmess:// 是 base64(json)，特殊处理
	if protocol == "vmess" {
		return extractVmessHostPort(link)
	}

	// 其他协议都是标准 URI 格式，用 net/url 解析
	u, err := url.Parse(link)
	if err != nil {
		return "", 0
	}
	host := u.Hostname()
	portStr := u.Port()
	port, _ := strconv.Atoi(portStr)
	return host, port
}

func extractVmessHostPort(link string) (string, int) {
	// vmess://BASE64
	b64 := strings.TrimPrefix(strings.ToLower(link), "vmess://")
	// 还原大小写（base64 区分大小写）
	b64 = link[len("vmess://"):]

	decoded, err := tryBase64Decode([]byte(b64))
	if err != nil {
		return "", 0
	}
	// 简单 JSON 字段提取，避免引入 encoding/json 依赖
	host := jsonField(string(decoded), "add")
	portStr := jsonField(string(decoded), "port")
	port, _ := strconv.Atoi(portStr)
	return host, port
}

// jsonField 从 JSON 字符串中提取指定字段的字符串值（简单实现，不处理嵌套）
func jsonField(json, key string) string {
	needle := `"` + key + `"`
	idx := strings.Index(json, needle)
	if idx < 0 {
		return ""
	}
	rest := json[idx+len(needle):]
	// 跳过 : 和空白
	rest = strings.TrimLeft(rest, " \t\r\n:")
	if len(rest) == 0 {
		return ""
	}
	if rest[0] == '"' {
		// 字符串值
		end := strings.Index(rest[1:], `"`)
		if end < 0 {
			return ""
		}
		return rest[1 : end+1]
	}
	// 数字值
	end := strings.IndexAny(rest, ",}")
	if end < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:end])
}

// extractName 从链接中提取节点名称（# fragment 部分）
func extractName(link string) string {
	if idx := strings.LastIndex(link, "#"); idx != -1 {
		name := link[idx+1:]
		name = urlDecode(name)
		return strings.TrimSpace(name)
	}
	// 没有 # 则用 host 作为名称
	u, err := url.Parse(link)
	if err == nil && u.Host != "" {
		return u.Hostname()
	}
	return ""
}

// urlDecode 简单 URL 解码
func urlDecode(s string) string {
	decoded, err := url.QueryUnescape(strings.ReplaceAll(s, "+", "%2B"))
	if err != nil {
		return s
	}
	return decoded
}

// extractHostPortFromAddr 从 "host:port" 字符串提取
func extractHostPortFromAddr(addr string) (string, int) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, 0
	}
	port, _ := strconv.Atoi(p)
	return h, port
}
