package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

type Config struct {
	ListenAddr   string
	WorkerDomain string
	WorkerIP     string
	SecretKey    string
	UseBareWS    bool
	WorkerPath   string
}

// bufferedConn 包装 net.Conn 和 bufio.Reader
// 目的是让偷看 (Peek) 过的连接依然可以通过标准 io.Reader 接口无缝读取，防止丢包
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) {
	return b.r.Read(p)
}

func main() {
	var config Config
	flag.StringVar(&config.ListenAddr, "listen", "127.0.0.1:1080", "Local SOCKS5/HTTP server listen address")
	flag.StringVar(&config.WorkerDomain, "domain", "", "Cloudflare Worker domain (e.g., my-worker.username.workers.dev)")
	flag.StringVar(&config.WorkerIP, "ip", "", "Specific IP to resolve Worker domain (Anti DNS-Pollution)")
	flag.StringVar(&config.SecretKey, "secret", "YOUR_SUPER_SECRET_KEY", "Secret key matching the worker config")
	flag.BoolVar(&config.UseBareWS, "ws", false, "Use bare ws:// instead of wss://")
	flag.StringVar(&config.WorkerPath, "path", "", "Cloudflare Worker path (e.g., /api/tcp)")
	flag.Parse()

	if config.WorkerDomain == "" {
		log.Fatal("Worker domain is required. Use -domain flag.")
	}

	// 设置优雅退出和信号拦截
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("Received termination signal, shutting down...")
		triggerCleanup(&config)
		os.Exit(0)
	}()

	listener, err := net.Listen("tcp", config.ListenAddr)
	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
	log.Printf("SOCKS5/HTTP Proxy server listening on %s", config.ListenAddr)
	log.Printf("Target Worker: %s (IP Override: %v, Bare WS: %v)", config.WorkerDomain, config.WorkerIP != "", config.UseBareWS)

	for {
		client, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v", err)
			continue
		}
		// 协议多路复用处理
		go handleConnection(client, &config)
	}
}

// 生成基于时间戳的 HMAC-SHA256 Token
func generateTimeBasedKey(secret string) string {
	timestamp := time.Now().UnixMilli()
	tsStr := strconv.FormatInt(timestamp, 10)

	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(tsStr))
	sig := hex.EncodeToString(h.Sum(nil))

	return fmt.Sprintf("%s-%s", tsStr, sig)
}

// 端口复用协议分发
func handleConnection(clientConn net.Conn, config *Config) {
	br := bufio.NewReader(clientConn)
	// Peek 1个字节来判断协议
	b, err := br.Peek(1)
	if err != nil {
		clientConn.Close()
		return
	}

	// 构造带缓冲区的连接，确保底层流读取不会丢失已被 bufio 读取的字节
	bConn := &bufferedConn{Conn: clientConn, r: br}

	if b[0] == 0x05 {
		// 0x05 是 SOCKS5 协议的魔数标志
		handleSocks5Connection(bConn, config)
	} else {
		// 其他默认进入 HTTP 代理处理流程
		handleHttpConnection(bConn, br, config)
	}
}

func handleHttpConnection(clientConn net.Conn, br *bufio.Reader, config *Config) {
	defer clientConn.Close()

	// 读取并解析 HTTP 请求
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}

	// 解析目标地址
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

	targetPortUint, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return
	}
	targetPort := uint16(targetPortUint)

	var initialPayload []byte

	if req.Method == http.MethodConnect {
		// HTTPS 的 CONNECT 隧道模式，响应 200 后直接盲传后续 TCP（如 TLS 握手）
		_, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		if err != nil {
			return
		}
	} else {
		// 纯 HTTP 代理模式：需要将修改后的 Request 转发给 Worker
		req.RequestURI = "" // Write 操作要求必须为空
		req.Header.Del("Proxy-Connection")
		req.Header.Del("Proxy-Authenticate")
		req.Header.Del("Proxy-Authorization")

		var buf bytes.Buffer
		if err := req.Write(&buf); err != nil {
			return
		}
		// 生成需要打头阵的 HTTP 报文
		initialPayload = buf.Bytes()
	}

	// 桥接 Cloudflare Worker WebSocket 隧道
	proxyToWorker(clientConn, targetHost, targetPort, config, initialPayload)
}

func handleSocks5Connection(clientConn net.Conn, config *Config) {
	defer clientConn.Close()

	// 1. SOCKS5 握手阶段 (No Auth)
	buf := make([]byte, 256)
	if _, err := io.ReadFull(clientConn, buf[:2]); err != nil {
		return
	}
	if buf[0] != 0x05 {
		return // 仅支持 SOCKS5
	}
	numMethods := int(buf[1])
	if _, err := io.ReadFull(clientConn, buf[:numMethods]); err != nil {
		return
	}
	// 响应无鉴权验证
	clientConn.Write([]byte{0x05, 0x00})

	// 2. 解析请求命令 (CONNECT)
	if _, err := io.ReadFull(clientConn, buf[:4]); err != nil {
		return
	}
	if buf[1] != 0x01 {
		return // 仅支持 CONNECT 命令
	}

	var targetHost string
	addrType := buf[3]

	switch addrType {
	case 0x01: // IPv4
		addr := make([]byte, 4)
		io.ReadFull(clientConn, addr)
		targetHost = net.IP(addr).String()
	case 0x03: // Domain name
		if _, err := io.ReadFull(clientConn, buf[:1]); err != nil {
			return
		}
		domainLen := int(buf[0])
		domain := make([]byte, domainLen)
		io.ReadFull(clientConn, domain)
		targetHost = string(domain)
	case 0x04: // IPv6
		addr := make([]byte, 16)
		io.ReadFull(clientConn, addr)
		// Worker 端有去除 [] 的逻辑，所以这里加上 brackets 符合 URL 习惯
		targetHost = fmt.Sprintf("[%s]", net.IP(addr).String())
	default:
		return
	}

	portBuf := make([]byte, 2)
	io.ReadFull(clientConn, portBuf)
	targetPort := binary.BigEndian.Uint16(portBuf)

	// 3. 响应客户端连接成功
	clientConn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})

	// 4. 连接 Cloudflare Worker WebSocket
	proxyToWorker(clientConn, targetHost, targetPort, config, nil)
}

func proxyToWorker(clientConn net.Conn, targetHost string, targetPort uint16, config *Config, initialPayload []byte) {
	scheme := "wss"
	if config.UseBareWS {
		scheme = "ws"
	}

	key := generateTimeBasedKey(config.SecretKey)

	path := config.WorkerPath
	if len(path) == 0 || path[0] != '/' {
		path = "/" + path
	}

	wsURL := fmt.Sprintf("%s://%s%s?k=%s&h=%s&p=%d", scheme, config.WorkerDomain, path, key, url.QueryEscape(targetHost), targetPort)

	dialer := &websocket.Dialer{
		// 动态拦截底层 TCP 拨号，实现 IP 直连对抗 DNS 污染
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if config.WorkerIP != "" {
				_, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				addr = net.JoinHostPort(config.WorkerIP, port)
			}
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, addr)
		},
		// 确保 SNI 指向 Worker 域名，即使底层拨号的是特定 IP
		TLSClientConfig: &tls.Config{
			ServerName:         config.WorkerDomain,
			InsecureSkipVerify: false,
		},
	}

	headers := http.Header{}
	// 发起连接
	wsConn, _, err := dialer.Dial(wsURL, headers)
	if err != nil {
		log.Printf("Failed to connect to worker (%s:%d): %v", targetHost, targetPort, err)
		return
	}
	defer wsConn.Close()

	// 如果有初始化负载（如重组后的HTTP GET报文），先发送过去
	if len(initialPayload) > 0 {
		if err := wsConn.WriteMessage(websocket.BinaryMessage, initialPayload); err != nil {
			log.Printf("Failed to write initial payload: %v", err)
			return
		}
	}

	// 5. 双向流量转发
	errc := make(chan error, 2)

	// WS -> Client TCP
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

	// Client TCP -> WS
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := clientConn.Read(buf)
			if n > 0 {
				if err := wsConn.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
					errc <- err
					return
				}
			}
			if err != nil {
				errc <- err
				return
			}
		}
	}()

	<-errc // 阻塞直至某条流关闭或出错
}

// 发起清理请求
func triggerCleanup(config *Config) {
	log.Println("Initiating DO instance cleanup...")
	scheme := "https"
	if config.UseBareWS {
		scheme = "http"
	}

	cleanupURL := fmt.Sprintf("%s://%s/api/cleanup", scheme, config.WorkerDomain)
	req, err := http.NewRequest("POST", cleanupURL, nil)
	if err != nil {
		log.Printf("Failed to create cleanup request: %v", err)
		return
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	// 动态拦截底层 TCP 拨号，实现 IP 直连对抗 DNS 污染
	if config.WorkerIP != "" {
		client.Transport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				_, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				addr = net.JoinHostPort(config.WorkerIP, port)
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, addr)
			},
			TLSClientConfig: &tls.Config{
				ServerName:         config.WorkerDomain,
				InsecureSkipVerify: false,
			},
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Cleanup request failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		log.Println("Cleanup completed successfully")
	} else {
		log.Printf("Cleanup request returned status: %v", resp.Status)
	}
}
