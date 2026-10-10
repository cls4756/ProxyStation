package engine

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ProxyStation/proxystation/db/configure"
	"github.com/ProxyStation/proxystation/pkg/dnsupstream"
)

// 兼容模式下的远端解析器通过本地回退转发器落地。
//
// sing-box 1.13 的 DNS 路由只按首条命中规则查询一次，选中的服务器失败就直接返回失败
// （dns/router.go 的 Exchange 仅在"应答地址数量超限"时才尝试下一条规则），
// 配置层面无法表达"远端 DoH 失败就换 bootstrap/直连"。
// 因此这里在本地起一个 UDP/TCP 转发器接管 remote 解析器：
//
//	主上游 = 经节点出口的远端加密解析（DoH/DoT），与兼容模式原有语义一致
//	兜底   = 直连 bootstrap（223.5.5.5:53），远端不可达时保证解析仍可用
//
// 主上游连续失败若干次后进入冷却，冷却期内直接走兜底，避免每个查询都等满超时。
const (
	dnsFallbackListenHost = "127.0.0.1"
	// dnsFallbackPortAttempts 动态挑端口时的重试次数：UDP/TCP 必须同端口，
	// 中间存在被其它进程抢占的窗口，失败就换一个端口重试。
	dnsFallbackPortAttempts = 5

	// dnsFallbackBootstrapAddr 兜底解析器：直连的明文 DNS，与配置里的 bootstrap 同一地址
	dnsFallbackBootstrapAddr = singboxBootstrapDNSAddr + ":53"

	dnsFallbackFailThreshold = 3
	dnsFallbackCooldown      = 60 * time.Second

	// 单个查询在各上游的耗时上限，两者之和要明显小于 sing-box 的 DNS 超时（10s）
	dnsFallbackPrimaryTimeout  = 3 * time.Second
	dnsFallbackFallbackTimeout = 2500 * time.Millisecond

	// 并发处理上限；超额直接返回 SERVFAIL，避免本地被查询洪水拖垮
	dnsFallbackMaxInflight = 128

	// 单条 DNS 报文上限（TCP 与上游应答）
	dnsFallbackMaxMessage = 4096

	// 已建立的 TCP 查询连接空闲超时
	dnsFallbackTCPIdleTimeout = 15 * time.Second

	// 失败日志限流间隔
	dnsFallbackLogInterval = 10 * time.Second

	// 关闭时等待在途查询的上限
	dnsFallbackCloseWait = 1500 * time.Millisecond
)

// errInvalidDNSResponse 上游返回的报文与本次查询不匹配（ID / 报文头异常）。
var errInvalidDNSResponse = errors.New("上游应答与查询不匹配")

// dnsQueryFunc 执行一次 DNS 查询：入参为原始查询报文，返回原始应答报文。
type dnsQueryFunc func(ctx context.Context, query []byte) ([]byte, error)

// dnsFallbackServer 本地回退转发器。primary / fallback 通过依赖注入传入，便于测试。
type dnsFallbackServer struct {
	primary  dnsQueryFunc
	fallback dnsQueryFunc
	logf     func(string)

	// readyCheck 判断主上游当前是否具备执行条件（例如主内核尚未就绪）。
	// 不就绪时直接走兜底且不计入熔断，避免内核重启把远端解析器误判为不可用。
	readyCheck func() bool

	// listenAddr 为空时自动挑一个空闲端口（UDP/TCP 共用）；测试可显式指定
	listenAddr    string
	failThreshold int
	cooldown      time.Duration

	udpConn *net.UDPConn
	tcpLn   net.Listener

	inflight chan struct{}
	wg       sync.WaitGroup
	closed   chan struct{}
	closeOne sync.Once

	mu        sync.Mutex
	fails     int
	skipUntil time.Time
	degraded  bool

	lastFailLog atomic.Int64
}

func newDNSFallbackServer(primary, fallback dnsQueryFunc, logf func(string)) *dnsFallbackServer {
	return &dnsFallbackServer{
		primary:       primary,
		fallback:      fallback,
		logf:          logf,
		listenAddr:    "",
		failThreshold: dnsFallbackFailThreshold,
		cooldown:      dnsFallbackCooldown,
		inflight:      make(chan struct{}, dnsFallbackMaxInflight),
		closed:        make(chan struct{}),
	}
}

func (s *dnsFallbackServer) start() error {
	attempts := 1
	if s.listenAddr == "" {
		attempts = dnsFallbackPortAttempts
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		addr := s.listenAddr
		if addr == "" {
			port, err := freeLoopbackPort()
			if err != nil {
				return err
			}
			addr = net.JoinHostPort(dnsFallbackListenHost, strconv.Itoa(port))
		}
		udpAddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			return err
		}
		udpConn, err := net.ListenUDP("udp", udpAddr)
		if err != nil {
			lastErr = fmt.Errorf("listen udp %s: %w", addr, err)
			continue
		}
		tcpLn, err := net.Listen("tcp", addr)
		if err != nil {
			_ = udpConn.Close()
			lastErr = fmt.Errorf("listen tcp %s: %w", addr, err)
			continue
		}
		s.udpConn = udpConn
		s.tcpLn = tcpLn
		break
	}
	if s.udpConn == nil || s.tcpLn == nil {
		if lastErr == nil {
			lastErr = errors.New("no free port")
		}
		return lastErr
	}
	s.wg.Add(2)
	go s.serveUDP()
	go s.serveTCP()
	return nil
}

// port 返回实际监听端口（转发器未启动时为 0）。
func (s *dnsFallbackServer) port() int {
	if s.udpConn == nil {
		return 0
	}
	addr, ok := s.udpConn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return 0
	}
	return addr.Port
}

// freeLoopbackPort 让内核绑定 127.0.0.1 上的一个空闲端口。
func freeLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", dnsFallbackListenHost+":0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port, nil
}

func (s *dnsFallbackServer) Close() {
	s.closeOne.Do(func() {
		close(s.closed)
		if s.udpConn != nil {
			_ = s.udpConn.Close()
		}
		if s.tcpLn != nil {
			_ = s.tcpLn.Close()
		}
		// 等正在处理的查询收尾，但必须有上限：停止流程不能因为某个上游慢查询而卡住
		done := make(chan struct{})
		go func() {
			s.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(dnsFallbackCloseWait):
		}
	})
}

func (s *dnsFallbackServer) log(msg string) {
	if s.logf != nil {
		s.logf(msg)
	}
}

func (s *dnsFallbackServer) serveUDP() {
	defer s.wg.Done()
	for {
		buf := make([]byte, dnsFallbackMaxMessage)
		_ = s.udpConn.SetReadDeadline(time.Now().Add(time.Second))
		n, addr, err := s.udpConn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-s.closed:
				return
			default:
			}
			if isTimeout(err) {
				continue
			}
			return
		}
		if n < 12 {
			continue
		}
		query := append([]byte(nil), buf[:n]...)
		if !s.acquire() {
			s.writeUDP(addr, query, buildDNSFailureResponse(query, dnsRcodeServerFailure))
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.release()
			response, ok := s.resolve(query)
			if !ok {
				return
			}
			s.writeUDP(addr, query, response)
		}()
	}
}

// writeUDP 按查询方声明的 UDP 上限回包，超出时回 TC 让调用方改用 TCP 重试。
func (s *dnsFallbackServer) writeUDP(addr *net.UDPAddr, query, response []byte) {
	if limit := dnsUDPPayloadLimit(query); len(response) > limit {
		response = buildDNSTruncatedResponse(query)
	}
	_, _ = s.udpConn.WriteToUDP(response, addr)
}

func (s *dnsFallbackServer) serveTCP() {
	defer s.wg.Done()
	for {
		conn, err := s.tcpLn.Accept()
		if err != nil {
			return
		}
		if !s.acquire() {
			_ = conn.Close()
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.release()
			s.handleTCPConn(conn)
		}()
	}
}

func (s *dnsFallbackServer) handleTCPConn(conn net.Conn) {
	defer conn.Close()
	for {
		_ = conn.SetDeadline(time.Now().Add(dnsFallbackTCPIdleTimeout))
		query, err := readDNSMessage(conn)
		if err != nil {
			return
		}
		if !isValidDNSQuery(query) {
			return
		}
		response, ok := s.resolve(query)
		if !ok {
			return
		}
		if err := writeDNSMessage(conn, response); err != nil {
			return
		}
	}
}

// resolve 依次尝试主上游与兜底上游；两者都失败时回 SERVFAIL。
// 返回 false 表示查询报文无法解析，调用方直接丢弃。
func (s *dnsFallbackServer) resolve(query []byte) ([]byte, bool) {
	if !isValidDNSQuery(query) {
		return nil, false
	}
	if s.primaryAllowed() && s.primaryReady() {
		ctx, cancel := context.WithTimeout(context.Background(), dnsFallbackPrimaryTimeout)
		response, err := s.primary(ctx, query)
		cancel()
		if err == nil && isDNSResponseFor(query, response) {
			s.notePrimarySuccess()
			return response, true
		}
		if err == nil {
			err = errInvalidDNSResponse
		}
		s.notePrimaryFailure(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), dnsFallbackFallbackTimeout)
	response, err := s.fallback(ctx, query)
	cancel()
	if err == nil && isDNSResponseFor(query, response) {
		return response, true
	}
	return buildDNSFailureResponse(query, dnsRcodeServerFailure), true
}

func (s *dnsFallbackServer) acquire() bool {
	select {
	case s.inflight <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *dnsFallbackServer) release() {
	select {
	case <-s.inflight:
	default:
	}
}

func (s *dnsFallbackServer) primaryReady() bool {
	if s.readyCheck == nil {
		return true
	}
	return s.readyCheck()
}

func (s *dnsFallbackServer) primaryAllowed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !time.Now().Before(s.skipUntil)
}

func (s *dnsFallbackServer) notePrimarySuccess() {
	s.mu.Lock()
	wasDegraded := s.degraded
	s.fails = 0
	s.skipUntil = time.Time{}
	s.degraded = false
	s.mu.Unlock()
	if wasDegraded {
		s.log("✅ DNS 远端解析器已恢复，重新优先使用远端解析")
	}
}

func (s *dnsFallbackServer) notePrimaryFailure(err error) {
	s.mu.Lock()
	s.fails++
	opened := false
	threshold := s.failThreshold
	cooldown := s.cooldown
	switch {
	case s.fails >= threshold:
		s.skipUntil = time.Now().Add(cooldown)
		if !s.degraded {
			s.degraded = true
			opened = true
		}
	}
	s.mu.Unlock()

	if opened {
		s.log(fmt.Sprintf("⚠️ DNS 远端解析器连续失败 %d 次（%v），已临时回退直连解析，%s 后重试",
			s.fails, err, dnsFallbackCooldown))
		return
	}
	if err == nil {
		return
	}
	now := time.Now().UnixNano()
	last := s.lastFailLog.Load()
	if now-last < int64(dnsFallbackLogInterval) {
		return
	}
	if s.lastFailLog.CompareAndSwap(last, now) {
		s.log(fmt.Sprintf("⚠️ DNS 远端解析器查询失败：%v，本次改用直连解析", err))
	}
}

func isTimeout(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}

// ---- 上游实现 ----

// buildPrimaryQueryFunc 按上游类型构造主上游查询函数，全部经节点出口（udp:// 除外）。
func buildPrimaryQueryFunc(spec dnsupstream.Spec, outbound string) dnsQueryFunc {
	switch spec.Kind {
	case dnsupstream.KindTLS:
		return tlsQueryFunc(spec, outbound)
	case dnsupstream.KindUDP:
		// 明文 UDP 无法经 SOCKS 隧道，按配置语义走直连
		return directUDPQueryFunc(spec.Addr())
	default:
		upstream := &dohUpstream{spec: spec, outbound: outbound}
		return upstream.query
	}
}

// directUDPQueryFunc 直连的明文 DNS（bootstrap 与 udp:// 上游共用），应答被截断时自动改用 TCP。
func directUDPQueryFunc(addr string) dnsQueryFunc {
	return func(ctx context.Context, query []byte) ([]byte, error) {
		dialer := net.Dialer{}
		conn, err := dialer.DialContext(ctx, "udp", addr)
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		if deadline, ok := ctx.Deadline(); ok {
			_ = conn.SetDeadline(deadline)
		}
		if _, err := conn.Write(query); err != nil {
			return nil, err
		}
		buf := make([]byte, dnsFallbackMaxMessage)
		n, err := conn.Read(buf)
		if err != nil {
			return nil, err
		}
		response := buf[:n]
		// 应答被置 TC 或刚好读满缓冲区（可能被截断）时都改用 TCP 重取
		if len(response) >= 3 && response[2]&0x02 != 0 {
			return plainQueryFunc("tcp", addr)(ctx, query)
		}
		if n >= len(buf) {
			return plainQueryFunc("tcp", addr)(ctx, query)
		}
		return append([]byte(nil), response...), nil
	}
}

// plainQueryFunc 走 TCP 的明文 DNS 查询。
func plainQueryFunc(network, addr string) dnsQueryFunc {
	return func(ctx context.Context, query []byte) ([]byte, error) {
		dialer := net.Dialer{}
		conn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		if deadline, ok := ctx.Deadline(); ok {
			_ = conn.SetDeadline(deadline)
		}
		if err := writeDNSMessage(conn, query); err != nil {
			return nil, err
		}
		return readDNSMessage(conn)
	}
}

// tlsQueryFunc 经节点出口建立 TLS 后走 DNS over TLS（RFC 7858）。
func tlsQueryFunc(spec dnsupstream.Spec, outbound string) dnsQueryFunc {
	return func(ctx context.Context, query []byte) ([]byte, error) {
		raw, err := DialContextViaOutbound(ctx, outbound, "tcp", spec.Addr())
		if err != nil {
			return nil, err
		}
		defer raw.Close()
		if deadline, ok := ctx.Deadline(); ok {
			_ = raw.SetDeadline(deadline)
		}
		tlsConn := tls.Client(raw, &tls.Config{ServerName: spec.Host, NextProtos: []string{"dot"}})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		defer tlsConn.Close()
		if err := writeDNSMessage(tlsConn, query); err != nil {
			return nil, err
		}
		return readDNSMessage(tlsConn)
	}
}

// dohUpstream 经节点出口发起 DNS over HTTPS 查询（RFC 8484，POST application/dns-message）。
type dohUpstream struct {
	spec     dnsupstream.Spec
	outbound string
	once     sync.Once
	client   *http.Client
}

func (u *dohUpstream) query(ctx context.Context, query []byte) ([]byte, error) {
	u.once.Do(func() {
		outbound := u.outbound
		transport := &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return DialContextViaOutbound(ctx, outbound, "tcp", addr)
			},
			TLSClientConfig:    &tls.Config{ServerName: u.spec.Host},
			ForceAttemptHTTP2:  true,
			DisableCompression: true,
		}
		u.client = &http.Client{Transport: transport}
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.spec.URL(), bytes.NewReader(query))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/dns-message")
	request.Header.Set("Accept", "application/dns-message")
	response, err := u.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 512))
		return nil, fmt.Errorf("doh http status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, dnsFallbackMaxMessage+1))
	if err != nil {
		return nil, err
	}
	if len(body) > dnsFallbackMaxMessage {
		return nil, fmt.Errorf("doh response too large: %d", len(body))
	}
	return body, nil
}

// ---- DNS 报文工具 ----

const (
	dnsRcodeServerFailure = 2
	dnsTypeOPT            = 41
)

func isValidDNSQuery(msg []byte) bool {
	if len(msg) < 12 {
		return false
	}
	flags := binary.BigEndian.Uint16(msg[2:4])
	if flags&0x8000 != 0 { // QR：查询报文才处理
		return false
	}
	_, ok := dnsQuestionEnd(msg)
	return ok
}

// isDNSResponseFor 校验应答是否对应本次查询。
func isDNSResponseFor(query, response []byte) bool {
	if len(response) < 12 || len(query) < 12 {
		return false
	}
	if !bytes.Equal(query[:2], response[:2]) {
		return false
	}
	flags := binary.BigEndian.Uint16(response[2:4])
	return flags&0x8000 != 0
}

// dnsQuestionEnd 返回问题段结束偏移。
func dnsQuestionEnd(msg []byte) (int, bool) {
	if len(msg) < 12 {
		return 0, false
	}
	qd := int(binary.BigEndian.Uint16(msg[4:6]))
	if qd < 1 {
		return 0, false
	}
	offset := 12
	for i := 0; i < qd; i++ {
		next, ok := dnsSkipQuestion(msg, offset)
		if !ok {
			return 0, false
		}
		offset = next
	}
	return offset, true
}

func dnsSkipQuestion(msg []byte, offset int) (int, bool) {
	offset, ok := dnsSkipName(msg, offset)
	if !ok {
		return 0, false
	}
	if offset+4 > len(msg) {
		return 0, false
	}
	return offset + 4, true
}

func dnsSkipName(msg []byte, offset int) (int, bool) {
	for {
		if offset >= len(msg) {
			return 0, false
		}
		length := msg[offset]
		switch {
		case length == 0:
			return offset + 1, true
		case length&0xC0 == 0xC0:
			if offset+2 > len(msg) {
				return 0, false
			}
			return offset + 2, true
		case length&0xC0 != 0:
			return 0, false
		default:
			if offset+1+int(length) > len(msg) {
				return 0, false
			}
			offset += 1 + int(length)
		}
	}
}

// dnsSkipRecord 跳过一条资源记录。
func dnsSkipRecord(msg []byte, offset int) (int, bool) {
	offset, ok := dnsSkipName(msg, offset)
	if !ok {
		return 0, false
	}
	if offset+10 > len(msg) {
		return 0, false
	}
	rdLength := int(binary.BigEndian.Uint16(msg[offset+8 : offset+10]))
	if offset+10+rdLength > len(msg) {
		return 0, false
	}
	return offset + 10 + rdLength, true
}

// dnsUDPPayloadLimit 返回查询方允许的 UDP 应答上限：无 EDNS0 按 RFC 1035 取 512，
// 有 EDNS0 时取 OPT 里声明的尺寸（上限 dnsFallbackMaxMessage）。
func dnsUDPPayloadLimit(query []byte) int {
	const base = 512
	offset, ok := dnsQuestionEnd(query)
	if !ok {
		return base
	}
	if len(query) <= offset {
		return base
	}
	an := int(binary.BigEndian.Uint16(query[6:8]))
	ns := int(binary.BigEndian.Uint16(query[8:10]))
	ar := int(binary.BigEndian.Uint16(query[10:12]))
	for i := 0; i < an+ns; i++ {
		offset, ok = dnsSkipRecord(query, offset)
		if !ok {
			return base
		}
	}
	for i := 0; i < ar; i++ {
		recordStart := offset
		if recordStart+4 > len(query) {
			return base
		}
		recordType := binary.BigEndian.Uint16(query[recordStart+2 : recordStart+4])
		if recordType == dnsTypeOPT {
			size := int(binary.BigEndian.Uint16(query[recordStart+4 : recordStart+6]))
			if size < base {
				return base
			}
			if size > dnsFallbackMaxMessage {
				return dnsFallbackMaxMessage
			}
			return size
		}
		offset, ok = dnsSkipRecord(query, offset)
		if !ok {
			return base
		}
	}
	return base
}

// buildDNSFailureResponse 复制查询的问题段，返回带指定 rcode 的失败应答。
func buildDNSFailureResponse(query []byte, rcode uint16) []byte {
	return buildDNSResponse(query, rcode, false)
}

// buildDNSTruncatedResponse 返回 TC 置位的应答，促使查询方改用 TCP 重试。
func buildDNSTruncatedResponse(query []byte) []byte {
	return buildDNSResponse(query, 0, true)
}

func buildDNSResponse(query []byte, rcode uint16, truncated bool) []byte {
	offset, ok := dnsQuestionEnd(query)
	if !ok {
		return nil
	}
	response := make([]byte, 12, 12+(offset-12))
	response[0] = query[0]
	response[1] = query[1]
	flags := uint16(0x8000) | (binary.BigEndian.Uint16(query[2:4]) & 0x0100) | 0x0080 | (rcode & 0x000F)
	if truncated {
		flags |= 0x0200
	}
	binary.BigEndian.PutUint16(response[2:4], flags)
	binary.BigEndian.PutUint16(response[4:6], 1)
	response = append(response, query[12:offset]...)
	return response
}

// readDNSMessage 读取 2 字节长度前缀的 DNS 报文（RFC 1035 4.2.2）。
func readDNSMessage(conn net.Conn) ([]byte, error) {
	var lengthBuf [2]byte
	if _, err := io.ReadFull(conn, lengthBuf[:]); err != nil {
		return nil, err
	}
	length := int(binary.BigEndian.Uint16(lengthBuf[:]))
	if length == 0 || length > dnsFallbackMaxMessage {
		return nil, fmt.Errorf("invalid dns message length: %d", length)
	}
	message := make([]byte, length)
	if _, err := io.ReadFull(conn, message); err != nil {
		return nil, err
	}
	return message, nil
}

func writeDNSMessage(conn net.Conn, message []byte) error {
	if len(message) == 0 || len(message) > 0xFFFF {
		return fmt.Errorf("invalid dns message length: %d", len(message))
	}
	buf := make([]byte, 2+len(message))
	binary.BigEndian.PutUint16(buf[:2], uint16(len(message)))
	copy(buf[2:], message)
	_, err := conn.Write(buf)
	return err
}

// ---- 生命周期接入 ----

var (
	dnsFallbackPort atomic.Int32
	dnsFallbackMu   sync.Mutex
	dnsFallbackSrv  *dnsFallbackServer
	dnsFallbackKey  string
)

// dnsFallbackActivePort 返回本地回退转发器的监听端口，0 表示当前未启用。
func dnsFallbackActivePort() int {
	return int(dnsFallbackPort.Load())
}

func logDNSFallback(msg string) {
	if LogCallback != nil {
		LogCallback(msg)
	}
}

// resolveDNSRemoteSpec 读取兼容模式远端解析器配置，非法值退回默认解析器。
func resolveDNSRemoteSpec(setting *configure.Setting) dnsupstream.Spec {
	spec, err := dnsupstream.Parse(setting.DNSRemoteServerRaw())
	if err != nil {
		logDNSFallback(fmt.Sprintf("⚠️ DNS 远端解析器配置无效（%v），本次使用默认 %s", err, dnsupstream.Default))
		spec, _ = dnsupstream.Parse("")
	}
	return spec
}

// syncDNSFallback 让回退转发器的运行状态与内核/设置保持一致：
// 仅 sing-box 内核 + 兼容模式启用；启动失败时退回"直接下发远端解析器"的配置写法。
func syncDNSFallback(setting *configure.Setting, kernel KernelType) {
	spec := dnsupstream.Spec{}
	outbound := ""
	key := ""
	// DNS 回退解析只与 sing-box 的 dns 配置有关，其它内核不需要转发器
	if kernel == KernelSingbox && singboxDNSMode(setting) == "compatible" {
		spec = resolveDNSRemoteSpec(setting)
		if tag := firstBoundOutboundTag(); tag != "" {
			outbound = tag
			key = spec.URL() + "|" + tag
		}
	}

	dnsFallbackMu.Lock()
	defer dnsFallbackMu.Unlock()
	if key == "" {
		stopDNSFallbackLocked()
		return
	}
	if dnsFallbackSrv != nil && dnsFallbackKey == key {
		return
	}
	stopDNSFallbackLocked()

	server := newDNSFallbackServer(
		buildPrimaryQueryFunc(spec, outbound),
		directUDPQueryFunc(dnsFallbackBootstrapAddr),
		logDNSFallback,
	)
	server.readyCheck = func() bool { return Manager.IsRunning() }
	if err := server.start(); err != nil {
		logDNSFallback(fmt.Sprintf("⚠️ DNS 回退转发器启动失败（%v），远端解析器仍按直连方式下发", err))
		return
	}
	dnsFallbackSrv = server
	dnsFallbackKey = key
	dnsFallbackPort.Store(int32(server.port()))
	logDNSFallback(fmt.Sprintf("🔎 兼容模式远端解析器 %s 经节点 %s 解析，失败时自动回退直连 %s（本地转发 %s:%d）",
		spec.URL(), outbound, dnsFallbackBootstrapAddr, dnsFallbackListenHost, server.port()))
}

// stopDNSFallback 停止回退转发器（内核停止时调用）。
func stopDNSFallback() {
	dnsFallbackMu.Lock()
	defer dnsFallbackMu.Unlock()
	stopDNSFallbackLocked()
}

func stopDNSFallbackLocked() {
	if dnsFallbackSrv == nil {
		return
	}
	dnsFallbackSrv.Close()
	dnsFallbackSrv = nil
	dnsFallbackKey = ""
	dnsFallbackPort.Store(0)
}
