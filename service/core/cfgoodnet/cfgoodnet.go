package cfgoodnet

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Rule struct {
	Pattern string `json:"pattern"`
	Action  string `json:"action"` // "direct" | "reject" | "cf_proxy"
}

type Config struct {
	ListenHost string `json:"listenHost,omitempty"`
	ListenPort int    `json:"listenPort,omitempty"`
	CfProxy    string `json:"cfProxy,omitempty"`
	CfGoodIP   string `json:"cfGoodIP,omitempty"`
	EnableXFF  bool   `json:"enableXFF,omitempty"`
	EnableMITM bool   `json:"enableMITM,omitempty"`
	Rules      []Rule `json:"rules,omitempty"`
	DataDir    string `json:"-"`
}

type processEntry struct {
	cancel context.CancelFunc
	addr   string
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
	if !out.EnableMITM {
		out.EnableMITM = true
	}
	return &out
}

func EncodeLink(cfg *Config) string {
	b, _ := json.Marshal(NormalizeConfig(cfg))
	return "cfgoodnet://" + base64.StdEncoding.EncodeToString(b)
}

func DecodeLink(link string) (*Config, string, int, error) {
	if !strings.HasPrefix(strings.ToLower(link), "cfgoodnet://") {
		return nil, "", 0, fmt.Errorf("invalid cfgoodnet link")
	}
	raw := strings.TrimPrefix(link, "cfgoodnet://")
	if i := strings.Index(raw, "#"); i >= 0 {
		raw = raw[:i]
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, "", 0, fmt.Errorf("decode cfgoodnet link failed: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, "", 0, fmt.Errorf("parse cfgoodnet link failed: %w", err)
	}
	n := NormalizeConfig(&cfg)
	return n, n.ListenHost, n.ListenPort, nil
}

func EnsureRunning(key string, cfg *Config, logf func(string)) (string, error) {
	n := NormalizeConfig(cfg)
	addr := net.JoinHostPort(n.ListenHost, strconv.Itoa(n.ListenPort))

	mu.Lock()
	if e, ok := entries[key]; ok {
		mu.Unlock()
		return e.addr, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	entries[key] = processEntry{cancel: cancel, addr: addr, done: done}
	mu.Unlock()

	server := &http.Server{
		Addr:              addr,
		Handler:           newProxyHandler(n, logf),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		mu.Lock()
		delete(entries, key)
		mu.Unlock()
		return "", fmt.Errorf("listen %s failed: %w", addr, err)
	}
	addr = ln.Addr().String()
	mu.Lock()
	if e, ok := entries[key]; ok {
		e.addr = addr
		entries[key] = e
	}
	mu.Unlock()

	go func() {
		<-ctx.Done()
		shutCtx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_ = server.Shutdown(shutCtx)
	}()
	go func() {
		defer close(done)
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			if logf != nil {
				logf(fmt.Sprintf("❌ cfgoodnet server stopped: %v", err))
			}
		}
		mu.Lock()
		delete(entries, key)
		mu.Unlock()
	}()
	if logf != nil {
		logf(fmt.Sprintf("✅ cfgoodnet 内置出站已启动: %s", addr))
	}
	return addr, nil
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

func StopAll() {
	mu.Lock()
	waiters := make([]chan struct{}, 0, len(entries))
	for key, e := range entries {
		e.cancel()
		waiters = append(waiters, e.done)
		delete(entries, key)
	}
	mu.Unlock()
	waitForStops(waiters)
}

func Addr(key string) string {
	mu.Lock()
	defer mu.Unlock()
	if e, ok := entries[key]; ok {
		return e.addr
	}
	return ""
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

type proxyHandler struct {
	cfg      *Config
	logf     func(string)
	ca       tls.Certificate
	caParsed *x509.Certificate
	certMu   sync.Mutex
	certByCN map[string]*tls.Certificate
}

func newForwardTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
}

func newProxyHandler(cfg *Config, logf func(string)) http.Handler {
	h := &proxyHandler{
		cfg:      cfg,
		logf:     logf,
		certByCN: map[string]*tls.Certificate{},
	}
	if cfg.EnableMITM {
		ca, cert, err := ensureOrCreateCA(cfg.DataDir)
		if err != nil {
			h.log("mitm CA init failed: %v", err)
		} else {
			h.ca = ca
			h.caParsed = cert
			h.log("mitm CA ready cert=%s", caCertPath(cfg.DataDir))
		}
	}
	return h
}

func (h *proxyHandler) log(msg string, args ...interface{}) {
	if h.logf != nil {
		h.logf(fmt.Sprintf("📘 cfgoodnet: "+msg, args...))
		return
	}
	log.Printf("cfgoodnet: "+msg, args...)
}

func (h *proxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		applyCORSHeaders(w.Header(), r.Header.Get("Origin"))
		w.WriteHeader(http.StatusNoContent)
		h.log("cors preflight host=%s origin=%s", r.Host, r.Header.Get("Origin"))
		return
	}
	action := h.matchAction(r.Host)
	h.log("request method=%s host=%s url=%s action=%s", r.Method, r.Host, r.URL.String(), action)
	if action == "reject" {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("rejected by cfgoodnet rule"))
		h.log("rejected host=%s", r.Host)
		return
	}
	if r.Method == http.MethodConnect {
		h.handleConnect(w, r, action)
		return
	}
	h.handleHTTP(w, r, action)
}

func (h *proxyHandler) matchAction(hostport string) string {
	host := hostport
	if hst, _, err := net.SplitHostPort(hostport); err == nil {
		host = hst
	}
	host = strings.ToLower(host)
	for _, rule := range h.cfg.Rules {
		p := strings.ToLower(strings.TrimSpace(rule.Pattern))
		if p == "" {
			continue
		}
		if p == "*" || strings.HasSuffix(host, p) || strings.Contains(host, p) {
			if rule.Action != "" {
				return rule.Action
			}
		}
	}
	if strings.TrimSpace(h.cfg.CfProxy) != "" {
		return "cf_proxy"
	}
	return "direct"
}

func (h *proxyHandler) handleConnect(w http.ResponseWriter, r *http.Request, action string) {
	if h.cfg.EnableMITM && action == "cf_proxy" {
		h.handleConnectMITM(w, r, action)
		return
	}

	targetAddr := r.Host
	originalTarget := r.Host
	if action == "cf_proxy" && h.cfg.CfGoodIP != "" {
		if _, port, err := net.SplitHostPort(r.Host); err == nil {
			targetAddr = net.JoinHostPort(h.cfg.CfGoodIP, port)
		}
	}
	h.log("connect action=%s original=%s dial=%s", action, originalTarget, targetAddr)
	dst, err := net.DialTimeout("tcp", targetAddr, 10*time.Second)
	if err != nil {
		h.log("connect dial failed target=%s err=%v", targetAddr, err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		dst.Close()
		h.log("connect hijack unsupported")
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}
	src, _, err := hj.Hijack()
	if err != nil {
		dst.Close()
		h.log("connect hijack failed err=%v", err)
		return
	}
	_, _ = src.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	h.log("connect established original=%s dial=%s", originalTarget, targetAddr)
	go io.Copy(dst, src)
	go io.Copy(src, dst)
}

func (h *proxyHandler) handleHTTP(w http.ResponseWriter, r *http.Request, action string) {
	h.forwardHTTP(w, r, action, false)
}

func (h *proxyHandler) forwardHTTP(w http.ResponseWriter, r *http.Request, action string, fromMITM bool) {
	if action == "cf_proxy" && strings.TrimSpace(h.cfg.CfProxy) != "" {
		base, err := url.Parse(h.cfg.CfProxy)
		if err == nil {
			target := r.URL.String()
			if fromMITM {
				target = "https://" + r.Host + r.URL.RequestURI()
			}
			upstreamURL := strings.TrimRight(base.String(), "/") + "/" + strings.TrimLeft(target, "/")
			u, parseErr := url.Parse(upstreamURL)
			if parseErr != nil {
				h.log("cf_proxy parse upstream url failed target=%s err=%v", upstreamURL, parseErr)
				http.Error(w, parseErr.Error(), http.StatusBadGateway)
				return
			}
			h.log("http cf_proxy action=cf_proxy host=%s upstream=%s", r.Host, u.String())

			req := r.Clone(r.Context())
			req.RequestURI = ""
			req.URL = u
			req.Host = u.Host
			if h.cfg.EnableXFF {
				req.Header.Set("X-Forwarded-For", "1.1.1.1")
			}

			resp, rtErr := newForwardTransport().RoundTrip(req)
			if rtErr != nil {
				h.log("cf_proxy upstream failed method=%s host=%s err=%v", r.Method, r.Host, rtErr)
				http.Error(w, "cf_proxy upstream unavailable", http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()
			for k, vv := range resp.Header {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			rewriteCfProxyLocationHeader(w.Header(), base, h)
			applyCORSHeaders(w.Header(), r.Header.Get("Origin"))
			rewriteCfHTMLContentTypeForDocument(w.Header(), r, h, true)
			w.WriteHeader(resp.StatusCode)
			_, _ = io.Copy(w, resp.Body)
			return
		}
	}

	transport := newForwardTransport()
	if action == "cf_proxy" && h.cfg.CfGoodIP != "" {
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				h.log("http dial split hostport failed addr=%s err=%v", addr, err)
				return nil, err
			}
			target := net.JoinHostPort(h.cfg.CfGoodIP, port)
			h.log("http dial override original=%s dial=%s", addr, target)
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, target)
		}
	}
	req := r.Clone(r.Context())
	req.RequestURI = ""
	resp, err := transport.RoundTrip(req)
	if err != nil {
		h.log("http upstream failed method=%s host=%s err=%v", r.Method, r.Host, err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	h.log("http upstream ok method=%s host=%s status=%d", r.Method, r.Host, resp.StatusCode)
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	applyCORSHeaders(w.Header(), r.Header.Get("Origin"))
	rewriteCfHTMLContentTypeForDocument(w.Header(), r, h, false)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func rewriteCfProxyLocationHeader(header http.Header, base *url.URL, h *proxyHandler) {
	if header == nil || base == nil {
		return
	}
	loc := strings.TrimSpace(header.Get("Location"))
	if loc == "" {
		return
	}
	u, err := url.Parse(loc)
	if err != nil {
		return
	}
	if !strings.EqualFold(u.Scheme, base.Scheme) || !strings.EqualFold(u.Host, base.Host) {
		return
	}
	p := strings.TrimLeft(u.EscapedPath(), "/")
	if p == "" {
		return
	}
	rawTarget, err := url.PathUnescape(p)
	if err != nil || rawTarget == "" {
		rawTarget = p
	}
	target, err := url.Parse(rawTarget)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return
	}
	// Preserve query/fragment that worker attached on redirect URL itself.
	if u.RawQuery != "" {
		target.RawQuery = u.RawQuery
	}
	if u.Fragment != "" {
		target.Fragment = u.Fragment
	}
	header.Set("Location", target.String())
	if h != nil {
		h.log("rewrite location from worker url to target url: %s -> %s", loc, target.String())
	}
}

func rewriteCfHTMLContentTypeForDocument(header http.Header, r *http.Request, h *proxyHandler, viaCfProxy bool) {
	if header == nil || r == nil {
		return
	}
	ct := strings.TrimSpace(header.Get("Content-Type"))
	if !strings.HasPrefix(strings.ToLower(ct), "text/cf-html") {
		return
	}
	// Only rewrite for likely top-level page/document requests.
	accept := strings.ToLower(r.Header.Get("Accept"))
	secFetchDest := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Dest")))
	if secFetchDest != "" && secFetchDest != "document" && secFetchDest != "iframe" && secFetchDest != "empty" {
		return
	}
	if accept != "" && !strings.Contains(accept, "text/html") {
		return
	}
	header.Set("Content-Type", "text/html; charset=UTF-8")
	if h != nil {
		if viaCfProxy {
			h.log("rewrite content-type from %q to %q (cf_proxy, document request)", ct, "text/html; charset=UTF-8")
		} else {
			h.log("rewrite content-type from %q to %q (document request)", ct, "text/html; charset=UTF-8")
		}
	}
}

func applyCORSHeaders(h http.Header, origin string) {
	if h == nil {
		return
	}
	if h.Get("Access-Control-Allow-Origin") == "" {
		if origin != "" {
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", appendVary(h.Get("Vary"), "Origin"))
			h.Set("Access-Control-Allow-Credentials", "true")
		} else {
			h.Set("Access-Control-Allow-Origin", "*")
		}
	}
	if h.Get("Access-Control-Allow-Methods") == "" {
		h.Set("Access-Control-Allow-Methods", "GET,HEAD,OPTIONS")
	}
	if h.Get("Access-Control-Allow-Headers") == "" {
		h.Set("Access-Control-Allow-Headers", "*")
	}
	expose := "Content-Length,Content-Range,Accept-Ranges,Content-Type,ETag,Last-Modified"
	if h.Get("Access-Control-Expose-Headers") == "" {
		h.Set("Access-Control-Expose-Headers", expose)
	}
}

func appendVary(existing, token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return existing
	}
	if existing == "" {
		return token
	}
	for _, part := range strings.Split(existing, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return existing
		}
	}
	return existing + ", " + token
}

func (h *proxyHandler) handleConnectMITM(w http.ResponseWriter, r *http.Request, action string) {
	if h.caParsed == nil || len(h.ca.Certificate) == 0 || h.ca.PrivateKey == nil {
		h.log("mitm disabled: ca unavailable, fallback tunnel host=%s", r.Host)
		h.handleConnect(w, r, "direct")
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hj.Hijack()
	if err != nil {
		h.log("mitm hijack failed host=%s err=%v", r.Host, err)
		return
	}
	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	host := r.Host
	if hst, _, err := net.SplitHostPort(r.Host); err == nil {
		host = hst
	}
	cert, err := h.issueLeafCert(host)
	if err != nil {
		h.log("mitm issue cert failed host=%s err=%v", host, err)
		_ = clientConn.Close()
		return
	}
	tlsConn := tls.Server(clientConn, &tls.Config{
		Certificates: []tls.Certificate{*cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err := tlsConn.Handshake(); err != nil {
		h.log("mitm tls handshake failed host=%s err=%v", host, err)
		_ = tlsConn.Close()
		return
	}
	h.log("mitm handshake ok host=%s", host)

	br := bufio.NewReader(tlsConn)
	bw := bufio.NewWriter(tlsConn)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			if err != io.EOF {
				h.log("mitm read request failed host=%s err=%v", host, err)
			}
			_ = tlsConn.Close()
			return
		}
		req.URL.Scheme = "https"
		req.URL.Host = req.Host
		req.RequestURI = ""
		req.Proto = "HTTP/1.1"
		req.ProtoMajor = 1
		req.ProtoMinor = 1
		h.log("mitm request method=%s host=%s url=%s", req.Method, req.Host, req.URL.String())

		rw := &mitmResponseWriter{header: make(http.Header), bw: bw}
		h.forwardHTTP(rw, req, action, true)
		if err := rw.flush(); err != nil {
			h.log("mitm write response failed host=%s err=%v", host, err)
			_ = tlsConn.Close()
			return
		}
		if req.Close {
			_ = tlsConn.Close()
			return
		}
	}
}

func (h *proxyHandler) issueLeafCert(host string) (*tls.Certificate, error) {
	h.certMu.Lock()
	defer h.certMu.Unlock()
	if cert, ok := h.certByCN[host]; ok {
		return cert, nil
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: host,
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{host},
	}
	if ip := net.ParseIP(host); ip != nil {
		tpl.IPAddresses = []net.IP{ip}
		tpl.DNSNames = nil
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	parent := h.caParsed
	parentKey, ok := h.ca.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("ca private key type mismatch")
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, parent, &key.PublicKey, parentKey)
	if err != nil {
		return nil, err
	}
	cert := &tls.Certificate{
		Certificate: [][]byte{der, h.ca.Certificate[0]},
		PrivateKey:  key,
	}
	h.certByCN[host] = cert
	return cert, nil
}

type mitmResponseWriter struct {
	header      http.Header
	statusCode  int
	wroteHeader bool
	body        bytes.Buffer
	bw          *bufio.Writer
}

func (w *mitmResponseWriter) Header() http.Header { return w.header }
func (w *mitmResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.statusCode = code
}
func (w *mitmResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.body.Write(b)
}
func (w *mitmResponseWriter) flush() error {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if _, err := fmt.Fprintf(w.bw, "HTTP/1.1 %d %s\r\n", w.statusCode, http.StatusText(w.statusCode)); err != nil {
		return err
	}
	if _, ok := w.header["Date"]; !ok {
		w.header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	}
	if _, ok := w.header["Connection"]; !ok {
		w.header.Set("Connection", "keep-alive")
	}
	if _, ok := w.header["Content-Length"]; !ok {
		w.header.Set("Content-Length", strconv.Itoa(w.body.Len()))
	}
	for k, vv := range w.header {
		for _, v := range vv {
			if _, err := fmt.Fprintf(w.bw, "%s: %s\r\n", k, v); err != nil {
				return err
			}
		}
	}
	if _, err := w.bw.WriteString("\r\n"); err != nil {
		return err
	}
	if _, err := io.Copy(w.bw, &w.body); err != nil {
		return err
	}
	return w.bw.Flush()
}

func caCertPath(dataDir string) string {
	base := dataDir
	if strings.TrimSpace(base) == "" {
		base = "."
	}
	return filepath.Join(base, "cfgoodnet-ca.crt")
}

func caKeyPath(dataDir string) string {
	base := dataDir
	if strings.TrimSpace(base) == "" {
		base = "."
	}
	return filepath.Join(base, "cfgoodnet-ca.key")
}

func CACertPath(dataDir string) string {
	return caCertPath(dataDir)
}

func EnsureCA(dataDir string) (string, error) {
	if _, _, err := ensureOrCreateCA(dataDir); err != nil {
		return "", err
	}
	return caCertPath(dataDir), nil
}

func ensureOrCreateCA(dataDir string) (tls.Certificate, *x509.Certificate, error) {
	certPath := caCertPath(dataDir)
	keyPath := caKeyPath(dataDir)
	if _, err := os.Stat(certPath); err == nil {
		if _, err2 := os.Stat(keyPath); err2 == nil {
			ca, err := tls.LoadX509KeyPair(certPath, keyPath)
			if err != nil {
				return tls.Certificate{}, nil, err
			}
			x, err := x509.ParseCertificate(ca.Certificate[0])
			if err != nil {
				return tls.Certificate{}, nil, err
			}
			return ca, x, nil
		}
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "ProxyStation cfgoodnet Root CA",
			Organization: []string{"ProxyStation"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            1,
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0755); err != nil {
		return tls.Certificate{}, nil, err
	}
	cf, err := os.OpenFile(certPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	if err := pem.Encode(cf, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		cf.Close()
		return tls.Certificate{}, nil, err
	}
	cf.Close()

	kf, err := os.OpenFile(keyPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	if err := pem.Encode(kf, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}); err != nil {
		kf.Close()
		return tls.Certificate{}, nil, err
	}
	kf.Close()

	ca, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	x, err := x509.ParseCertificate(ca.Certificate[0])
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	return ca, x, nil
}
