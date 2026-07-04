package probe

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ProxyStation/proxystation/db/configure"
)

const (
	defaultTimeout = 5 * time.Second
	probePort      = 443
)

var probeHosts = []string{
	"www.gstatic.com",
	"www.google.com",
	"www.youtube.com",
	"cp.cloudflare.com",
	"example.com",
}

func resolveProbeHosts() []string {
	setting := configure.GetSettingNotNil()
	raw := strings.TrimSpace(setting.ProbeTargets)
	if raw == "" {
		return probeHosts
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, p := range parts {
		h := strings.ToLower(strings.TrimSpace(p))
		h = strings.TrimPrefix(h, "https://")
		h = strings.TrimPrefix(h, "http://")
		if i := strings.Index(h, "/"); i >= 0 {
			h = h[:i]
		}
		if h == "" {
			continue
		}
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	if len(out) == 0 {
		return probeHosts
	}
	return out
}

// ProbeServer probes availability of a server and returns latency in ms, -1 on failure.
func ProbeServer(s *configure.ServerRaw) int {
	return ProbeServerWithTimeout(s, defaultTimeout)
}

// ProbeServerWithTimeout probes availability of a server and returns latency in ms, -1 on failure.
func ProbeServerWithTimeout(s *configure.ServerRaw, timeout time.Duration) int {
	if s == nil || strings.TrimSpace(s.Host) == "" || s.Port <= 0 {
		return -1
	}
	username, password := extractProxyCredentials(s)
	switch normalizeType(s.Type) {
	case "socks5", "socks":
		return probeSOCKS5(s.Host, s.Port, timeout, username, password)
	case "http":
		return probeHTTPConnect(s.Host, s.Port, timeout, false, username, password)
	case "https":
		return probeHTTPConnect(s.Host, s.Port, timeout, true, username, password)
	default:
		return probeTCP(s.Host, s.Port, timeout)
	}
}

// FastReachable performs a very quick TCP reachability check.
// It is used as a pre-check to fail fast before expensive strict verification.
func FastReachable(s *configure.ServerRaw, timeout time.Duration) bool {
	if s == nil || strings.TrimSpace(s.Host) == "" || s.Port <= 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(s.Host, strconv.Itoa(s.Port)), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func normalizeType(t string) string {
	return strings.ToLower(strings.TrimSpace(t))
}

// SupportsRealProbe reports whether this proxy type is currently verified by a real tunnel request.
func SupportsRealProbe(serverType string) bool {
	switch normalizeType(serverType) {
	case "socks5", "socks", "http", "https":
		return true
	default:
		return false
	}
}

func probeTCP(host string, port int, timeout time.Duration) int {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		return -1
	}
	_ = conn.Close()
	ms := int(time.Since(start).Milliseconds())
	if ms <= 0 {
		return 1
	}
	return ms
}

func probeSOCKS5(host string, port int, timeout time.Duration, username, password string) int {
	for _, targetHost := range resolveProbeHosts() {
		if ms := probeSOCKS5Target(host, port, timeout, username, password, targetHost); ms > 0 {
			return ms
		}
	}
	return -1
}

func probeSOCKS5Target(host string, port int, timeout time.Duration, username, password, targetHost string) int {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		return -1
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	// greeting: version 5, advertise no-auth and username/password
	if _, err := conn.Write([]byte{0x05, 0x02, 0x00, 0x02}); err != nil {
		return -1
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return -1
	}
	if reply[0] != 0x05 {
		return -1
	}
	if reply[1] == 0x02 {
		if username == "" || len(username) > 255 || len(password) > 255 {
			return -1
		}
		authReq := make([]byte, 0, 3+len(username)+len(password))
		authReq = append(authReq, 0x01, byte(len(username)))
		authReq = append(authReq, []byte(username)...)
		authReq = append(authReq, byte(len(password)))
		authReq = append(authReq, []byte(password)...)
		if _, err := conn.Write(authReq); err != nil {
			return -1
		}
		authResp := make([]byte, 2)
		if _, err := io.ReadFull(conn, authResp); err != nil {
			return -1
		}
		if authResp[0] != 0x01 || authResp[1] != 0x00 {
			return -1
		}
	} else if reply[1] != 0x00 {
		return -1
	}

	// CONNECT targetHost:443
	hostBytes := []byte(targetHost)
	req := make([]byte, 0, 6+len(hostBytes))
	req = append(req, 0x05, 0x01, 0x00, 0x03, byte(len(hostBytes)))
	req = append(req, hostBytes...)
	req = append(req, byte(probePort>>8), byte(probePort&0xff))
	if _, err := conn.Write(req); err != nil {
		return -1
	}

	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return -1
	}
	if head[0] != 0x05 || head[1] != 0x00 {
		return -1
	}

	// consume bound addr
	var addrLen int
	switch head[3] {
	case 0x01:
		addrLen = 4
	case 0x03:
		lb := make([]byte, 1)
		if _, err := io.ReadFull(conn, lb); err != nil {
			return -1
		}
		addrLen = int(lb[0])
	case 0x04:
		addrLen = 16
	default:
		return -1
	}
	if addrLen > 0 {
		dummy := make([]byte, addrLen)
		if _, err := io.ReadFull(conn, dummy); err != nil {
			return -1
		}
	}
	dummyPort := make([]byte, 2)
	if _, err := io.ReadFull(conn, dummyPort); err != nil {
		return -1
	}

	// 真实探测：在隧道内完成 TLS 握手并请求 204 页面
	if !verifyHTTPS204(conn, timeout, targetHost) {
		return -1
	}

	ms := int(time.Since(start).Milliseconds())
	if ms <= 0 {
		return 1
	}
	return ms
}

func probeHTTPConnect(host string, port int, timeout time.Duration, secure bool, username, password string) int {
	for _, targetHost := range resolveProbeHosts() {
		if ms := probeHTTPConnectTarget(host, port, timeout, secure, username, password, targetHost); ms > 0 {
			return ms
		}
	}
	return -1
}

func probeHTTPConnectTarget(host string, port int, timeout time.Duration, secure bool, username, password, targetHost string) int {
	start := time.Now()
	rawConn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		return -1
	}
	defer rawConn.Close()
	_ = rawConn.SetDeadline(time.Now().Add(timeout))

	conn := net.Conn(rawConn)
	if secure {
		tlsConn := tls.Client(rawConn, &tls.Config{
			ServerName:         host,
			InsecureSkipVerify: true,
		})
		if err := tlsConn.Handshake(); err != nil {
			return -1
		}
		conn = tlsConn
	}

	req := fmt.Sprintf("CONNECT %s:%d HTTP/1.1\r\nHost: %s:%d\r\nProxy-Connection: Keep-Alive\r\n", targetHost, probePort, targetHost, probePort)
	if username != "" {
		token := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		req += "Proxy-Authorization: Basic " + token + "\r\n"
	}
	req += "\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		return -1
	}

	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		return -1
	}
	if !strings.Contains(line, " 200 ") {
		return -1
	}

	// 吃掉 CONNECT 响应头剩余部分，避免干扰后续 TLS 握手
	for {
		l, e := br.ReadString('\n')
		if e != nil {
			return -1
		}
		if l == "\r\n" {
			break
		}
	}

	// 真实探测：在隧道内完成 TLS 握手并请求 204 页面
	if !verifyHTTPS204(conn, timeout, targetHost) {
		return -1
	}

	ms := int(time.Since(start).Milliseconds())
	if ms <= 0 {
		return 1
	}
	return ms
}

func verifyHTTPS204(conn net.Conn, timeout time.Duration, targetHost string) bool {
	_ = conn.SetDeadline(time.Now().Add(timeout))
	tlsConn := tls.Client(conn, &tls.Config{
		ServerName:         targetHost,
		InsecureSkipVerify: true,
	})
	if err := tlsConn.Handshake(); err != nil {
		return false
	}
	defer tlsConn.Close()

	path := "/generate_204?_=" + strconv.FormatInt(time.Now().UnixNano()+int64(rand.Intn(1000)), 10)
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + targetHost + "\r\n" +
		"Connection: close\r\n" +
		"User-Agent: ProxyStation-Probe/1.0\r\n\r\n"
	if _, err := tlsConn.Write([]byte(req)); err != nil {
		return false
	}

	r := bufio.NewReader(tlsConn)
	statusLine, err := r.ReadString('\n')
	if err != nil {
		return false
	}
	return strings.Contains(statusLine, " 204 ") || strings.Contains(statusLine, " 200 ")
}

func extractProxyCredentials(s *configure.ServerRaw) (string, string) {
	if s == nil {
		return "", ""
	}
	link := strings.TrimSpace(s.Link)
	if link == "" {
		return "", ""
	}
	u, err := url.Parse(link)
	if err != nil || u == nil || u.User == nil {
		return "", ""
	}
	username := u.User.Username()
	password, _ := u.User.Password()
	return username, password
}
