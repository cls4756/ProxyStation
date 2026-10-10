// Package dnsupstream 解析"远端解析器"配置。
//
// 兼容模式下需要把域名交给一个抗污染的远端解析器：这里是用户可填的一行地址，
// 包内把它规整成明确的上游类型（DoH / DoT / 明文 UDP）与主机、端口、路径，
// 供配置生成（写进 sing-box）和本地回退转发器（实际发起查询）共用，
// 避免两处各自解析出现分歧。
package dnsupstream

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Default 兼容模式远端解析器的默认值，与历史实现里写死的
// {type: https, server: 8.8.8.8} 等价。
const Default = "https://8.8.8.8/dns-query"

// DefaultDoHPath DoH 未显式给出路径时使用的标准路径。
const DefaultDoHPath = "/dns-query"

// Kind 上游使用的协议类型。
type Kind string

const (
	// KindHTTPS DoH（HTTP/2 + TLS）。
	KindHTTPS Kind = "https"
	// KindTLS DoT（TLS 上的 DNS）。
	KindTLS Kind = "tls"
	// KindUDP 明文 DNS，走直连，不经过节点。
	KindUDP Kind = "udp"
)

// Spec 规范化后的远端解析器定义。
type Spec struct {
	Kind Kind
	Host string // 域名或 IP 字面量，不含端口与方括号
	Port uint16
	Path string // 仅 DoH 使用
}

// IsDomain 判断上游地址是否是域名（域名需要额外解析）。
func (s Spec) IsDomain() bool {
	return s.Host != "" && net.ParseIP(s.Host) == nil
}

// Addr 返回 host:port 形式，IPv6 自动加方括号。
func (s Spec) Addr() string {
	return net.JoinHostPort(s.Host, strconv.Itoa(int(s.Port)))
}

// IsDefaultPort 判断端口是否为该协议的默认端口（默认端口在配置里可省略）。
func (s Spec) IsDefaultPort() bool {
	switch s.Kind {
	case KindTLS:
		return s.Port == 853
	case KindUDP:
		return s.Port == 53
	default:
		return s.Port == 443
	}
}

// URL 返回规范化后的字符串形式，用于回填设置项与展示。
func (s Spec) URL() string {
	switch s.Kind {
	case KindTLS:
		if s.Port == 853 {
			return "tls://" + s.Host
		}
		return "tls://" + s.Addr()
	case KindUDP:
		if s.Port == 53 {
			return "udp://" + s.Host
		}
		return "udp://" + s.Addr()
	default:
		host := s.Host
		if s.Port != 443 {
			host = s.Addr()
		}
		path := s.Path
		if path == "" {
			path = DefaultDoHPath
		}
		return "https://" + host + path
	}
}

// Normalize 校验用户输入并返回规范化字符串。空输入返回空串，表示"使用默认值"。
func Normalize(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	spec, err := Parse(trimmed)
	if err != nil {
		return "", err
	}
	return spec.URL(), nil
}

// Parse 解析用户输入。空输入使用 Default。
func Parse(raw string) (Spec, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = Default
	}

	scheme := ""
	rest := trimmed
	if idx := strings.Index(trimmed, "://"); idx >= 0 {
		scheme = strings.ToLower(trimmed[:idx])
		rest = trimmed[idx+3:]
	} else {
		// 只写主机时按历史语义当作 DoH（https + 443 + /dns-query）
		scheme = string(KindHTTPS)
	}

	spec := Spec{Kind: Kind(scheme)}
	switch spec.Kind {
	case KindHTTPS:
		spec.Port = 443
		spec.Path = DefaultDoHPath
	case KindTLS, "dot":
		spec.Kind = KindTLS
		spec.Port = 853
	case KindUDP:
		spec.Port = 53
	default:
		return Spec{}, fmt.Errorf("不支持的解析器协议 %q（可用 https:// tls:// udp://）", scheme)
	}

	parsed, err := url.Parse(scheme + "://" + rest)
	if err != nil {
		return Spec{}, fmt.Errorf("解析器地址无法解析：%v", err)
	}
	if parsed.User != nil {
		return Spec{}, fmt.Errorf("解析器地址不支持用户名密码")
	}
	if parsed.Fragment != "" {
		return Spec{}, fmt.Errorf("解析器地址不支持 # 片段")
	}
	if spec.Kind != KindHTTPS && parsed.Path != "" && parsed.Path != "/" {
		return Spec{}, fmt.Errorf("解析器地址 %s 不支持路径", spec.Kind)
	}
	if parsed.RawQuery != "" {
		return Spec{}, fmt.Errorf("解析器地址不支持查询参数")
	}

	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return Spec{}, fmt.Errorf("解析器地址缺少主机名")
	}
	if strings.ContainsAny(host, " /") {
		return Spec{}, fmt.Errorf("解析器地址主机名非法：%q", host)
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return Spec{}, fmt.Errorf("解析器端口非法：%q", port)
		}
		spec.Port = uint16(value)
	}
	spec.Host = strings.ToLower(host)

	if spec.Kind == KindHTTPS {
		path := parsed.Path
		if path == "" || path == "/" {
			path = DefaultDoHPath
		}
		if !strings.HasPrefix(path, "/") {
			return Spec{}, fmt.Errorf("解析器路径非法：%q", path)
		}
		spec.Path = path
	}
	return spec, nil
}
