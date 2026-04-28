package v2ray

import (
	"encoding/base64"
	"fmt"
	"net"
	"strconv"
	"strings"
)

func base64Decode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	// try all variants
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
	return nil, fmt.Errorf("base64 decode failed")
}

func toInt(v interface{}) int {
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case string:
		n, _ := strconv.Atoi(val)
		return n
	}
	return 0
}

func splitHostPort(hostport string) (string, int, error) {
	h, p, err := net.SplitHostPort(hostport)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(p)
	return h, port, err
}
