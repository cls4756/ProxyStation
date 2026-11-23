package configure

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/url"
	"strconv"
	"strings"
)

func NodeConfigFingerprint(server ServerRaw) (string, bool) {
	link := strings.TrimSpace(server.Link)
	host := canonicalNodeHost(server.Host)
	nodeType := canonicalNodeType(server.Type)
	if link == "" {
		if host == "" || server.Port <= 0 {
			return "", false
		}
		switch nodeType {
		case "socks", "http", "https":
		default:
			return "", false
		}
	}
	canonical := canonicalNodeLink(link)
	content, err := json.Marshal(struct {
		Type       string
		Host       string
		Port       int
		FrontProxy string
		Link       string
	}{
		Type: nodeType, Host: host,
		Port: server.Port, FrontProxy: strings.TrimSpace(server.FrontProxy), Link: canonical,
	})
	if err != nil {
		return "", false
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), true
}

func canonicalNodeHost(host string) string {
	address, zone, scoped := strings.Cut(strings.Trim(strings.TrimSpace(host), "[]"), "%")
	address = strings.ToLower(address)
	if ip := net.ParseIP(address); ip != nil {
		address = ip.String()
	}
	if scoped {
		return address + "%" + zone
	}
	return address
}

func canonicalNodeType(nodeType string) string {
	nodeType = strings.ToLower(strings.TrimSpace(nodeType))
	switch nodeType {
	case "socks5":
		return "socks"
	case "hy2":
		return "hysteria2"
	}
	return nodeType
}

func canonicalNodeLink(link string) string {
	scheme, payload, found := strings.Cut(link, "://")
	if !found {
		return link
	}
	scheme = strings.ToLower(scheme)
	payload, _, _ = strings.Cut(payload, "#")
	switch scheme {
	case "clash", "singbox", "vmess":
		decoded, ok := decodeNodeBase64(payload)
		if !ok {
			return link
		}
		var config map[string]interface{}
		decoder := json.NewDecoder(strings.NewReader(string(decoded)))
		decoder.UseNumber()
		if decoder.Decode(&config) != nil || config == nil {
			return link
		}
		switch scheme {
		case "clash":
			delete(config, "name")
		case "singbox":
			delete(config, "tag")
		case "vmess":
			delete(config, "ps")
			for _, field := range []string{"aid", "v"} {
				if value, ok := config[field].(string); ok {
					if number, err := strconv.ParseInt(value, 10, 64); err == nil {
						config[field] = json.Number(strconv.FormatInt(number, 10))
					}
				}
			}
		}
		hostField, portField := "server", "port"
		if scheme == "vmess" {
			hostField = "add"
		} else if scheme == "singbox" {
			portField = "server_port"
		}
		if value, ok := config[hostField].(string); ok {
			config[hostField] = canonicalNodeHost(value)
		}
		if value, ok := config[portField].(string); ok {
			if number, err := strconv.ParseInt(value, 10, 64); err == nil {
				config[portField] = json.Number(strconv.FormatInt(number, 10))
			}
		}
		content, err := json.Marshal(config)
		if err != nil {
			return link
		}
		return scheme + "://" + string(content)
	case "ssr":
		decoded, ok := decodeNodeBase64(payload)
		if !ok {
			return link
		}
		address, query, _ := strings.Cut(string(decoded), "/?")
		params, err := url.ParseQuery(query)
		if err != nil {
			return link
		}
		params.Del("remarks")
		params.Del("group")
		return scheme + "://" + address + "/?" + params.Encode()
	case "ss":
		if !strings.Contains(payload, "@") {
			if decoded, ok := decodeNodeBase64(payload); ok {
				link = "ss://" + string(decoded)
			}
		}
	}
	parsed, err := url.Parse(link)
	if err != nil || parsed.Host == "" {
		return link
	}
	parsed.Scheme = scheme
	host := canonicalNodeHost(parsed.Hostname())
	if port := parsed.Port(); port != "" {
		parsed.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		parsed.Host = "[" + host + "]"
	} else {
		parsed.Host = host
	}
	parsed.Fragment = ""
	parsed.RawFragment = ""
	params, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return link
	}
	parsed.RawQuery = params.Encode()
	if scheme == "ss" && parsed.User != nil {
		if _, hasPassword := parsed.User.Password(); !hasPassword {
			if decoded, ok := decodeNodeBase64(parsed.User.Username()); ok {
				method, password, found := strings.Cut(string(decoded), ":")
				if found {
					parsed.User = url.UserPassword(method, password)
				}
			}
		}
	}
	return parsed.String()
}

func decodeNodeBase64(payload string) ([]byte, bool) {
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if decoded, err := encoding.DecodeString(payload); err == nil {
			return decoded, true
		}
	}
	return nil, false
}
