package cfdo

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Config struct {
	ListenHost            string   `json:"listenHost,omitempty"`
	ListenPort            int      `json:"listenPort,omitempty"`
	Listeners             []ListenerConfig `json:"listeners,omitempty"`
	WorkerDomain          string   `json:"workerDomain,omitempty"`
	Secret                string   `json:"secret,omitempty"`
	Path                  string   `json:"path,omitempty"`
	WorkerIP              string   `json:"workerIp,omitempty"`
	UseBareWS             bool     `json:"useBareWs,omitempty"`
	AlwaysUseDO           bool     `json:"alwaysUseDo,omitempty"`
	DOPoolSize            int      `json:"doPoolSize,omitempty"`
	RejectDomains         []string `json:"rejectDomains,omitempty"`
	DOFallbackDomains     []string `json:"doFallbackDomains,omitempty"`
	DOFallbackExtensions  []string `json:"doFallbackExtensions,omitempty"`
	rejectDomainRules     []string
	doFallbackDomainRules []string
	doFallbackExtPatterns []string
}

type ListenerConfig struct {
	ListenPort int    `json:"listenPort,omitempty"`
	WorkerIP   string `json:"workerIp,omitempty"`
}

type processEntry struct {
	cancel context.CancelFunc
	addr   string
	addrs  []string
	done   chan struct{}
}

var (
	mu      sync.Mutex
	entries = map[string]processEntry{}
)

func NormalizeConfig(cfg *Config) *Config {
	if cfg == nil {
		cfg = &Config{}
	}
	out := *cfg
	if strings.TrimSpace(out.ListenHost) == "" {
		out.ListenHost = "127.0.0.1"
	}
	if out.ListenPort < 0 {
		out.ListenPort = 0
	}
	if len(out.Listeners) > 0 {
		normalized := make([]ListenerConfig, 0, len(out.Listeners))
		for _, l := range out.Listeners {
			if l.ListenPort < 0 {
				continue
			}
			normalized = append(normalized, ListenerConfig{
				ListenPort: l.ListenPort,
				WorkerIP:   strings.TrimSpace(l.WorkerIP),
			})
		}
		out.Listeners = normalized
	}
	if out.DOPoolSize < 0 {
		out.DOPoolSize = 0
	}
	out.Path = normalizePath(out.Path)
	out.rejectDomainRules = normalizeDomainRules(out.RejectDomains)
	out.doFallbackDomainRules = normalizeDomainRules(out.DOFallbackDomains)
	out.doFallbackExtPatterns = normalizeExtensions(out.DOFallbackExtensions)
	return &out
}

func normalizeDomainRules(values []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, raw := range values {
		for _, part := range strings.FieldsFunc(raw, splitListRune) {
			rule := strings.ToLower(strings.TrimSpace(part))
			rule = strings.TrimPrefix(rule, "http://")
			rule = strings.TrimPrefix(rule, "https://")
			if i := strings.IndexAny(rule, "/:"); i >= 0 {
				rule = rule[:i]
			}
			rule = strings.TrimRight(rule, ".")
			if rule == "" {
				continue
			}
			if strings.HasPrefix(rule, "*.") {
				rule = "." + strings.TrimPrefix(rule, "*.")
			}
			if _, ok := seen[rule]; ok {
				continue
			}
			seen[rule] = struct{}{}
			out = append(out, rule)
		}
	}
	return out
}

func normalizeExtensions(values []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, raw := range values {
		for _, part := range strings.FieldsFunc(raw, splitListRune) {
			ext := strings.ToLower(strings.TrimSpace(part))
			if ext == "" {
				continue
			}
			if !strings.HasPrefix(ext, ".") {
				ext = "." + ext
			}
			if _, ok := seen[ext]; ok {
				continue
			}
			seen[ext] = struct{}{}
			out = append(out, ext)
		}
	}
	return out
}

func splitListRune(r rune) bool {
	return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
}

func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/api/tcp"
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

func Addr(key string) string {
	mu.Lock()
	defer mu.Unlock()
	if e, ok := entries[key]; ok {
		return e.addr
	}
	return ""
}

func StopAll() {
	mu.Lock()
	waiters := make([]chan struct{}, 0, len(entries))
	for k, e := range entries {
		e.cancel()
		waiters = append(waiters, e.done)
		delete(entries, k)
	}
	mu.Unlock()
	waitForStops(waiters)
}

func Stop(key string) {
	mu.Lock()
	var done chan struct{}
	if e, ok := entries[key]; ok {
		e.cancel()
		done = e.done
		delete(entries, key)
	}
	mu.Unlock()
	waitForStops([]chan struct{}{done})
}

func waitForStops(waiters []chan struct{}) {
	for _, done := range waiters {
		if done == nil {
			continue
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
}

func EnsureRunning(key string, cfg *Config, logf func(string)) (string, error) {
	n := NormalizeConfig(cfg)
	if strings.TrimSpace(n.WorkerDomain) == "" || strings.TrimSpace(n.Secret) == "" {
		return "", fmt.Errorf("workerDomain and secret are required")
	}
	listenerCfgs := buildListenerConfigs(n)
	if len(listenerCfgs) == 0 {
		return "", fmt.Errorf("at least one listener is required")
	}

	mu.Lock()
	if e, ok := entries[key]; ok {
		mu.Unlock()
		return e.addr, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	entries[key] = processEntry{cancel: cancel, addr: "", addrs: nil, done: done}
	mu.Unlock()

	type startedListener struct {
		ln  net.Listener
		cfg *Config
	}
	started := make([]startedListener, 0, len(listenerCfgs))
	actualAddrs := make([]string, 0, len(listenerCfgs))
	for _, lc := range listenerCfgs {
		ln, err := net.Listen("tcp", net.JoinHostPort(lc.ListenHost, strconv.Itoa(lc.ListenPort)))
		if err != nil {
			for _, s := range started {
				_ = s.ln.Close()
			}
			mu.Lock()
			delete(entries, key)
			mu.Unlock()
			return "", err
		}
		lcfg := *n
		lcfg.ListenHost = lc.ListenHost
		lcfg.ListenPort = lc.ListenPort
		// 多监听模式下，WorkerIP 由监听项决定；为空时表示不使用优选地址。
		lcfg.WorkerIP = lc.WorkerIP
		started = append(started, startedListener{ln: ln, cfg: &lcfg})
		actualAddrs = append(actualAddrs, ln.Addr().String())
	}

	primaryAddr := actualAddrs[0]
	mu.Lock()
	if e, ok := entries[key]; ok {
		e.addr = primaryAddr
		e.addrs = append([]string(nil), actualAddrs...)
		entries[key] = e
	}
	mu.Unlock()

	go func() {
		<-ctx.Done()
		for _, s := range started {
			_ = s.ln.Close()
		}
	}()
	go func() {
		var wg sync.WaitGroup
		for _, s := range started {
			ln := s.ln
			h := &handler{cfg: s.cfg, logf: logf}
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					conn, e := ln.Accept()
					if e != nil {
						select {
						case <-ctx.Done():
							return
						default:
							h.log("accept failed: %v", e)
							continue
						}
					}
					go h.handleConn(conn)
				}
			}()
		}
		wg.Wait()
		close(done)
	}()
	if logf != nil {
		for _, addr := range actualAddrs {
			logf(fmt.Sprintf("✅ cfdo 本地代理已启动: %s -> %s%s", addr, n.WorkerDomain, n.Path))
		}
	}
	return primaryAddr, nil
}

type effectiveListenerConfig struct {
	ListenHost string
	ListenPort int
	WorkerIP   string
}

func buildListenerConfigs(cfg *Config) []effectiveListenerConfig {
	if cfg == nil {
		return nil
	}
	host := cfg.ListenHost
	if strings.TrimSpace(host) == "" {
		host = "127.0.0.1"
	}
	if len(cfg.Listeners) == 0 {
		return []effectiveListenerConfig{{
			ListenHost: host,
			ListenPort: cfg.ListenPort,
			WorkerIP:   strings.TrimSpace(cfg.WorkerIP),
		}}
	}
	usedPorts := map[int]struct{}{}
	out := make([]effectiveListenerConfig, 0, len(cfg.Listeners))
	for _, l := range cfg.Listeners {
		if l.ListenPort < 0 {
			continue
		}
		if _, exists := usedPorts[l.ListenPort]; exists {
			continue
		}
		usedPorts[l.ListenPort] = struct{}{}
		out = append(out, effectiveListenerConfig{
			ListenHost: host,
			ListenPort: l.ListenPort,
			WorkerIP:   strings.TrimSpace(l.WorkerIP),
		})
	}
	return out
}

type handler struct {
	cfg  *Config
	logf func(string)
}

func (h *handler) log(format string, args ...interface{}) {
	msg := fmt.Sprintf("cfdo: "+format, args...)
	if h.logf != nil {
		h.logf("📘 " + msg)
		return
	}
	log.Print(msg)
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }

func (h *handler) handleConn(clientConn net.Conn) {
	br := bufio.NewReader(clientConn)
	b, err := br.Peek(1)
	if err != nil {
		_ = clientConn.Close()
		return
	}
	bc := &bufferedConn{Conn: clientConn, r: br}
	if b[0] == 0x05 {
		h.handleSocks5Connection(bc)
		return
	}
	h.handleHTTPConnection(bc, br)
}

func (h *handler) handleHTTPConnection(clientConn net.Conn, br *bufio.Reader) {
	defer clientConn.Close()
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	host := req.URL.Host
	if host == "" {
		host = req.Host
	}
	targetHost, portStr, err := net.SplitHostPort(host)
	if err != nil {
		targetHost = host
		if req.Method == http.MethodConnect || req.URL.Scheme == "https" {
			portStr = "443"
		} else {
			portStr = "80"
		}
	}
	pu, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return
	}
	if h.shouldRejectHost(targetHost) {
		if req.Method == http.MethodConnect {
			_, _ = clientConn.Write([]byte("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n"))
		} else {
			_, _ = clientConn.Write([]byte("HTTP/1.1 403 Forbidden\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: 21\r\n\r\nrejected by cfdo rule"))
		}
		h.log("rejected http host=%s method=%s", targetHost, req.Method)
		return
	}
	var initial []byte
	if req.Method == http.MethodConnect {
		_, err = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		if err != nil {
			return
		}
	} else {
		req.RequestURI = ""
		req.Header.Del("Proxy-Connection")
		req.Header.Del("Proxy-Authenticate")
		req.Header.Del("Proxy-Authorization")
		var buf bytes.Buffer
		if err := req.Write(&buf); err != nil {
			return
		}
		initial = buf.Bytes()
	}
	h.proxyToWorker(clientConn, targetHost, uint16(pu), initial, h.shouldUseDOFallback(targetHost, req))
}

func (h *handler) handleSocks5Connection(clientConn net.Conn) {
	defer clientConn.Close()
	buf := make([]byte, 256)
	if _, err := io.ReadFull(clientConn, buf[:2]); err != nil || buf[0] != 0x05 {
		return
	}
	n := int(buf[1])
	if _, err := io.ReadFull(clientConn, buf[:n]); err != nil {
		return
	}
	_, _ = clientConn.Write([]byte{0x05, 0x00})
	if _, err := io.ReadFull(clientConn, buf[:4]); err != nil || buf[1] != 0x01 {
		return
	}
	var targetHost string
	switch buf[3] {
	case 0x01:
		addr := make([]byte, 4)
		_, _ = io.ReadFull(clientConn, addr)
		targetHost = net.IP(addr).String()
	case 0x03:
		_, _ = io.ReadFull(clientConn, buf[:1])
		dl := int(buf[0])
		domain := make([]byte, dl)
		_, _ = io.ReadFull(clientConn, domain)
		targetHost = string(domain)
	case 0x04:
		addr := make([]byte, 16)
		_, _ = io.ReadFull(clientConn, addr)
		targetHost = fmt.Sprintf("[%s]", net.IP(addr).String())
	default:
		return
	}
	portBuf := make([]byte, 2)
	_, _ = io.ReadFull(clientConn, portBuf)
	targetPort := binary.BigEndian.Uint16(portBuf)
	if h.shouldRejectHost(targetHost) {
		_, _ = clientConn.Write([]byte{0x05, 0x02, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		h.log("rejected socks5 host=%s", targetHost)
		return
	}
	_, _ = clientConn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	h.proxyToWorker(clientConn, targetHost, targetPort, nil, h.shouldUseDOFallbackForHost(targetHost))
}

func (h *handler) shouldRejectHost(targetHost string) bool {
	host := normalizeMatchHost(targetHost)
	if host == "" || len(h.cfg.rejectDomainRules) == 0 {
		return false
	}
	return matchDomainRules(host, h.cfg.rejectDomainRules)
}

func (h *handler) shouldUseDOFallback(targetHost string, req *http.Request) bool {
	if h.cfg.AlwaysUseDO {
		return true
	}
	if h.shouldUseDOFallbackForHost(targetHost) {
		return true
	}
	return h.shouldUseDOFallbackForRequestPath(req)
}

func (h *handler) shouldUseDOFallbackForHost(targetHost string) bool {
	if h.cfg.AlwaysUseDO {
		return true
	}
	host := normalizeMatchHost(targetHost)
	if host == "" || len(h.cfg.doFallbackDomainRules) == 0 {
		return false
	}
	return matchDomainRules(host, h.cfg.doFallbackDomainRules)
}

func matchDomainRules(host string, rules []string) bool {
	for _, rule := range rules {
		if strings.HasPrefix(rule, ".") {
			suffix := strings.TrimPrefix(rule, ".")
			if host == suffix || strings.HasSuffix(host, rule) {
				return true
			}
			continue
		}
		if host == rule {
			return true
		}
	}
	return false
}

func (h *handler) shouldUseDOFallbackForRequestPath(req *http.Request) bool {
	if req == nil || req.Method == http.MethodConnect || len(h.cfg.doFallbackExtPatterns) == 0 {
		return false
	}
	path := strings.ToLower(req.URL.EscapedPath())
	if path == "" {
		path = strings.ToLower(req.URL.Path)
	}
	for _, ext := range h.cfg.doFallbackExtPatterns {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

func normalizeMatchHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.Trim(host, "[]")
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.Trim(host, ".")
}

func (h *handler) proxyToWorker(clientConn net.Conn, targetHost string, targetPort uint16, initialPayload []byte, useDOFallback bool) {
	scheme := "wss"
	if h.cfg.UseBareWS {
		scheme = "ws"
	}
	key := generateTimeBasedKey(h.cfg.Secret)
	mode := "direct"
	if useDOFallback {
		mode = "do"
	}
	wsURL := fmt.Sprintf("%s://%s%s?k=%s&h=%s&p=%d&m=%s", scheme, h.cfg.WorkerDomain, normalizePath(h.cfg.Path), key, url.QueryEscape(targetHost), targetPort, mode)
	if useDOFallback {
		if doID := h.doShardID(targetHost); doID != "" {
			wsURL += "&doid=" + url.QueryEscape(doID)
		}
	}

	dialer := &websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if h.cfg.WorkerIP != "" {
				_, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				addr = net.JoinHostPort(h.cfg.WorkerIP, port)
			}
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{ServerName: h.cfg.WorkerDomain, InsecureSkipVerify: false},
	}
	wsConn, resp, err := dialer.Dial(wsURL, http.Header{})
	if err != nil {
		if resp != nil {
			bodySnippet := readResponseSnippet(resp.Body, 4096)
			h.log("worker dial failed host=%s:%d err=%v status=%d statusText=%s body=%q", targetHost, targetPort, err, resp.StatusCode, resp.Status, bodySnippet)
		} else {
			h.log("worker dial failed host=%s:%d err=%v", targetHost, targetPort, err)
		}
		return
	}
	defer wsConn.Close()
	if len(initialPayload) > 0 {
		if err := wsConn.WriteMessage(websocket.BinaryMessage, initialPayload); err != nil {
			h.log("write initial payload failed: %v", err)
			return
		}
	}
	errc := make(chan error, 2)
	go func() {
		for {
			_, msg, err := wsConn.ReadMessage()
			if err != nil {
				errc <- err
				return
			}
			if _, err := clientConn.Write(msg); err != nil {
				errc <- err
				return
			}
		}
	}()
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := clientConn.Read(buf)
			if n > 0 {
				if e := wsConn.WriteMessage(websocket.BinaryMessage, buf[:n]); e != nil {
					errc <- e
					return
				}
			}
			if err != nil {
				errc <- err
				return
			}
		}
	}()
	<-errc
}

func (h *handler) doShardID(targetHost string) string {
	if h == nil || h.cfg == nil || h.cfg.DOPoolSize <= 0 {
		return ""
	}
	host := normalizeMatchHost(targetHost)
	if host == "" {
		host = strings.ToLower(strings.TrimSpace(targetHost))
	}
	if host == "" {
		return ""
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(host))
	idx := int(hasher.Sum32() % uint32(h.cfg.DOPoolSize))
	if idx < 0 {
		idx = 0
	}
	return fmt.Sprintf("pool-%d", idx)
}

func readResponseSnippet(body io.ReadCloser, limit int64) string {
	if body == nil {
		return ""
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, limit))
	if err != nil {
		return "read-body-failed: " + err.Error()
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	return text
}

func generateTimeBasedKey(secret string) string {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write([]byte(ts))
	sig := hex.EncodeToString(h.Sum(nil))
	return ts + "-" + sig
}
