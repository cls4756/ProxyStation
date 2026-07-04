package engine

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/ProxyStation/proxystation/db/configure"
)

type shadowsocksEndpoint struct {
	Host     string
	Port     int
	Method   string
	Password string
}

func parseShadowsocksEndpoint(s *configure.ServerRaw) (shadowsocksEndpoint, error) {
	if s == nil {
		return shadowsocksEndpoint{}, fmt.Errorf("missing shadowsocks server")
	}

	link := strings.TrimSpace(s.Link)
	lower := strings.ToLower(link)
	switch {
	case strings.HasPrefix(lower, "clash://"):
		return parseClashShadowsocksEndpoint(s)
	case strings.HasPrefix(lower, "singbox://"):
		return parseSingboxShadowsocksEndpoint(s)
	case strings.HasPrefix(lower, "ss://"):
		return parseShadowsocksURI(s)
	default:
		return shadowsocksEndpoint{}, fmt.Errorf("invalid shadowsocks link")
	}
}

func parseClashShadowsocksEndpoint(s *configure.ServerRaw) (shadowsocksEndpoint, error) {
	m, err := decodeInternalNodeMap(s.Link, "clash://")
	if err != nil {
		return shadowsocksEndpoint{}, err
	}
	return validateShadowsocksEndpoint(shadowsocksEndpoint{
		Host:     firstNonEmpty(mapString(m, "server"), s.Host),
		Port:     firstNonZero(mapInt(m, "port"), s.Port),
		Method:   firstNonEmpty(mapString(m, "cipher"), mapString(m, "method")),
		Password: mapString(m, "password"),
	})
}

func parseSingboxShadowsocksEndpoint(s *configure.ServerRaw) (shadowsocksEndpoint, error) {
	m, err := decodeInternalNodeMap(s.Link, "singbox://")
	if err != nil {
		return shadowsocksEndpoint{}, err
	}
	return validateShadowsocksEndpoint(shadowsocksEndpoint{
		Host:     firstNonEmpty(mapString(m, "server"), s.Host),
		Port:     firstNonZero(mapInt(m, "server_port"), mapInt(m, "port"), s.Port),
		Method:   mapString(m, "method"),
		Password: mapString(m, "password"),
	})
}

func parseShadowsocksURI(s *configure.ServerRaw) (shadowsocksEndpoint, error) {
	link := strings.TrimSpace(s.Link)
	if !strings.HasPrefix(strings.ToLower(link), "ss://") {
		return shadowsocksEndpoint{}, fmt.Errorf("invalid shadowsocks uri")
	}

	body := link[len("ss://"):]
	if idx := strings.Index(body, "#"); idx != -1 {
		body = body[:idx]
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return shadowsocksEndpoint{}, fmt.Errorf("empty shadowsocks uri")
	}

	var ep shadowsocksEndpoint
	if atIdx := strings.LastIndex(body, "@"); atIdx != -1 {
		userinfo := body[:atIdx]
		hostport := body[atIdx+1:]
		method, password := parseShadowsocksUserinfo(userinfo)
		host, port := parseShadowsocksHostPort(hostport)
		ep = shadowsocksEndpoint{
			Host:     firstNonEmpty(host, s.Host),
			Port:     firstNonZero(port, s.Port),
			Method:   method,
			Password: password,
		}
		return validateShadowsocksEndpoint(ep)
	}

	decoded, err := b64Decode(body)
	if err != nil {
		if idx := strings.IndexAny(body, "/?"); idx != -1 {
			decoded, err = b64Decode(body[:idx])
		}
		if err != nil {
			return shadowsocksEndpoint{}, fmt.Errorf("shadowsocks decode: %w", err)
		}
	}
	if atIdx := strings.LastIndex(decoded, "@"); atIdx != -1 {
		userinfo := decoded[:atIdx]
		hostport := decoded[atIdx+1:]
		method, password := parseShadowsocksUserinfo(userinfo)
		host, port := parseShadowsocksHostPort(hostport)
		ep = shadowsocksEndpoint{
			Host:     firstNonEmpty(host, s.Host),
			Port:     firstNonZero(port, s.Port),
			Method:   method,
			Password: password,
		}
		return validateShadowsocksEndpoint(ep)
	}

	return shadowsocksEndpoint{}, fmt.Errorf("invalid shadowsocks uri payload")
}

func parseShadowsocksUserinfo(userinfo string) (string, string) {
	candidates := []string{userinfo}
	if unescaped, err := url.PathUnescape(userinfo); err == nil && unescaped != userinfo {
		candidates = append([]string{unescaped}, candidates...)
	}
	for _, candidate := range candidates {
		if decoded, err := b64Decode(candidate); err == nil && strings.Contains(decoded, ":") {
			return splitShadowsocksUserinfo(decoded)
		}
	}
	for _, candidate := range candidates {
		if strings.Contains(candidate, ":") {
			return splitShadowsocksUserinfo(candidate)
		}
	}
	return "", ""
}

func splitShadowsocksUserinfo(userinfo string) (string, string) {
	parts := strings.SplitN(userinfo, ":", 2)
	if len(parts) != 2 {
		return "", ""
	}
	password, err := url.PathUnescape(parts[1])
	if err != nil {
		password = parts[1]
	}
	method, err := url.PathUnescape(parts[0])
	if err != nil {
		method = parts[0]
	}
	return method, password
}

func parseShadowsocksHostPort(hostport string) (string, int) {
	hostport = strings.TrimSpace(hostport)
	if idx := strings.IndexAny(hostport, "/?"); idx != -1 {
		hostport = hostport[:idx]
	}
	if hostport == "" {
		return "", 0
	}
	if host, portStr, err := net.SplitHostPort(hostport); err == nil {
		port, _ := strconv.Atoi(portStr)
		return host, port
	}
	return splitHostPort(hostport)
}

func validateShadowsocksEndpoint(ep shadowsocksEndpoint) (shadowsocksEndpoint, error) {
	switch {
	case ep.Host == "":
		return shadowsocksEndpoint{}, fmt.Errorf("invalid shadowsocks link: missing server")
	case ep.Port == 0:
		return shadowsocksEndpoint{}, fmt.Errorf("invalid shadowsocks link: missing server port")
	case ep.Method == "":
		return shadowsocksEndpoint{}, fmt.Errorf("invalid shadowsocks link: missing method")
	case ep.Password == "":
		return shadowsocksEndpoint{}, fmt.Errorf("invalid shadowsocks link: missing password")
	default:
		return ep, nil
	}
}

func decodeInternalNodeMap(link, prefix string) (map[string]interface{}, error) {
	if !strings.HasPrefix(strings.ToLower(link), prefix) {
		return nil, fmt.Errorf("invalid internal node link")
	}
	encoded := strings.TrimPrefix(link, prefix)
	decoded, err := b64Decode(encoded)
	if err != nil {
		decoded = encoded
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(decoded), &m); err != nil {
		return nil, fmt.Errorf("decode internal node link: %w", err)
	}
	return m, nil
}

func mapString(m map[string]interface{}, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}

func mapInt(m map[string]interface{}, key string) int {
	return anyToInt(m[key])
}

func firstNonZero(vals ...int) int {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}
