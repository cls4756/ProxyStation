package engine

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---- 测试用 DNS 报文构造 ----

func testDNSQuery(id uint16, name string) []byte {
	message := make([]byte, 12)
	binary.BigEndian.PutUint16(message[0:2], id)
	binary.BigEndian.PutUint16(message[2:4], 0x0100)
	binary.BigEndian.PutUint16(message[4:6], 1)
	message = append(message, encodeTestName(name)...)
	message = append(message, 0x00, 0x01, 0x00, 0x01)
	return message
}

func encodeTestName(name string) []byte {
	var out []byte
	for _, label := range strings.Split(name, ".") {
		if label == "" {
			continue
		}
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	return append(out, 0)
}

func testDNSResponseWithAddress(id uint16, name string, addr [4]byte) []byte {
	response := testDNSQuery(id, name)
	binary.BigEndian.PutUint16(response[2:4], 0x8180)
	binary.BigEndian.PutUint16(response[6:8], 1)
	response = append(response, 0xC0, 0x0C)
	response = append(response, 0x00, 0x01, 0x00, 0x01)
	response = append(response, 0, 0, 0, 60)
	response = append(response, 0, 4)
	response = append(response, addr[:]...)
	return response
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func startTestFallbackServer(t *testing.T, primary, fallback dnsQueryFunc) (*dnsFallbackServer, string, *[]string) {
	t.Helper()
	logs := &[]string{}
	var mu sync.Mutex
	logf := func(msg string) {
		mu.Lock()
		*logs = append(*logs, msg)
		mu.Unlock()
	}
	// UDP 与 TCP 必须共用同一端口（与生产一致），先挑一个空闲端口再启动
	var server *dnsFallbackServer
	for attempt := 0; attempt < 8; attempt++ {
		server = newDNSFallbackServer(primary, fallback, logf)
		server.listenAddr = fmt.Sprintf("127.0.0.1:%d", freeTCPPort(t))
		if err := server.start(); err != nil {
			continue
		}
		break
	}
	if server == nil || server.udpConn == nil {
		t.Fatal("启动测试用回退转发器失败")
	}
	t.Cleanup(server.Close)
	return server, server.udpConn.LocalAddr().String(), logs
}

func udpExchange(t *testing.T, addr string, query []byte) []byte {
	t.Helper()
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatalf("dial udp: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(query); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, dnsFallbackMaxMessage)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return buf[:n]
}

func tcpExchange(t *testing.T, addr string, query []byte) []byte {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial tcp: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := writeDNSMessage(conn, query); err != nil {
		t.Fatalf("write: %v", err)
	}
	response, err := readDNSMessage(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return response
}

// ---- 行为测试 ----

// 主上游可用时应直接使用主上游，不碰兜底
func TestDNSFallbackPrefersPrimary(t *testing.T) {
	var fallbackCalls atomic.Int32
	primary := dnsQueryFunc(func(ctx context.Context, query []byte) ([]byte, error) {
		return testDNSResponseWithAddress(binary.BigEndian.Uint16(query[0:2]), "a.example.com", [4]byte{1, 1, 1, 1}), nil
	})
	fallback := dnsQueryFunc(func(ctx context.Context, query []byte) ([]byte, error) {
		fallbackCalls.Add(1)
		return testDNSResponseWithAddress(binary.BigEndian.Uint16(query[0:2]), "a.example.com", [4]byte{2, 2, 2, 2}), nil
	})
	_, addr, _ := startTestFallbackServer(t, primary, fallback)

	response := udpExchange(t, addr, testDNSQuery(0x1234, "a.example.com"))
	if got := binary.BigEndian.Uint16(response[0:2]); got != 0x1234 {
		t.Fatalf("响应 ID = %#x，期望 0x1234", got)
	}
	if fallbackCalls.Load() != 0 {
		t.Fatalf("主上游可用时不应调用兜底，实际调用 %d 次", fallbackCalls.Load())
	}
}

// 主上游失败时应回退到兜底
func TestDNSFallbackUsesFallbackOnPrimaryFailure(t *testing.T) {
	primary := dnsQueryFunc(func(ctx context.Context, query []byte) ([]byte, error) {
		return nil, errors.New("远端不可达")
	})
	fallback := dnsQueryFunc(func(ctx context.Context, query []byte) ([]byte, error) {
		return testDNSResponseWithAddress(binary.BigEndian.Uint16(query[0:2]), "b.example.com", [4]byte{3, 3, 3, 3}), nil
	})
	_, addr, _ := startTestFallbackServer(t, primary, fallback)

	response := udpExchange(t, addr, testDNSQuery(0x0101, "b.example.com"))
	if response[3]&0x0F != 0 {
		t.Fatalf("期望成功应答，rcode = %d", response[3]&0x0F)
	}
	if !strings.Contains(string(response), string([]byte{3, 3, 3, 3})) {
		t.Fatal("响应里没有兜底解析返回的地址")
	}
}

// 两个上游都失败时回 SERVFAIL，并保留查询 ID 与问题段
func TestDNSFallbackServfailWhenAllFail(t *testing.T) {
	fail := dnsQueryFunc(func(ctx context.Context, query []byte) ([]byte, error) {
		return nil, errors.New("down")
	})
	_, addr, _ := startTestFallbackServer(t, fail, fail)

	query := testDNSQuery(0xABCD, "c.example.com")
	response := udpExchange(t, addr, query)
	if got := binary.BigEndian.Uint16(response[0:2]); got != 0xABCD {
		t.Fatalf("响应 ID = %#x，期望 0xABCD", got)
	}
	if rcode := response[3] & 0x0F; rcode != dnsRcodeServerFailure {
		t.Fatalf("rcode = %d，期望 SERVFAIL(%d)", rcode, dnsRcodeServerFailure)
	}
	if len(response) < 12 || binary.BigEndian.Uint16(response[4:6]) != 1 {
		t.Fatal("SERVFAIL 应答未回显问题段")
	}
}

// 主上游连续失败达到阈值后进入冷却：冷却期内不再尝试主上游
func TestDNSFallbackBreakerSkipsPrimary(t *testing.T) {
	var primaryCalls atomic.Int32
	primary := dnsQueryFunc(func(ctx context.Context, query []byte) ([]byte, error) {
		primaryCalls.Add(1)
		return nil, errors.New("远端不可达")
	})
	fallback := dnsQueryFunc(func(ctx context.Context, query []byte) ([]byte, error) {
		return testDNSResponseWithAddress(binary.BigEndian.Uint16(query[0:2]), "d.example.com", [4]byte{4, 4, 4, 4}), nil
	})
	server, addr, logs := startTestFallbackServer(t, primary, fallback)
	server.failThreshold = 2
	server.cooldown = time.Minute

	for i := 0; i < 2; i++ {
		udpExchange(t, addr, testDNSQuery(uint16(0x1000+i), "d.example.com"))
	}
	if primaryCalls.Load() != 2 {
		t.Fatalf("冷却前应尝试主上游 2 次，实际 %d 次", primaryCalls.Load())
	}
	udpExchange(t, addr, testDNSQuery(0x1002, "d.example.com"))
	if primaryCalls.Load() != 2 {
		t.Fatalf("冷却期内不应再尝试主上游，实际 %d 次", primaryCalls.Load())
	}
	found := false
	for _, line := range *logs {
		if strings.Contains(line, "已临时回退直连解析") {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺少降级日志：%v", *logs)
	}
}

// 冷却到期且主上游恢复后，应恢复优先远端解析
func TestDNSFallbackRecoversAfterCooldown(t *testing.T) {
	var healthy atomic.Bool
	primary := dnsQueryFunc(func(ctx context.Context, query []byte) ([]byte, error) {
		if !healthy.Load() {
			return nil, errors.New("远端不可达")
		}
		return testDNSResponseWithAddress(binary.BigEndian.Uint16(query[0:2]), "e.example.com", [4]byte{5, 5, 5, 5}), nil
	})
	fallback := dnsQueryFunc(func(ctx context.Context, query []byte) ([]byte, error) {
		return testDNSResponseWithAddress(binary.BigEndian.Uint16(query[0:2]), "e.example.com", [4]byte{6, 6, 6, 6}), nil
	})
	server, addr, logs := startTestFallbackServer(t, primary, fallback)
	server.failThreshold = 1
	server.cooldown = 50 * time.Millisecond

	udpExchange(t, addr, testDNSQuery(0x2000, "e.example.com"))
	healthy.Store(true)
	time.Sleep(80 * time.Millisecond)
	udpExchange(t, addr, testDNSQuery(0x2001, "e.example.com"))
	if server.primaryAllowed() != true {
		t.Fatal("冷却到期后应重新允许主上游")
	}
	found := false
	for _, line := range *logs {
		if strings.Contains(line, "已恢复") {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺少恢复日志：%v", *logs)
	}
}

// 超出查询方 UDP 上限的应答回 TC，调用方改用 TCP 后拿到完整应答
func TestDNSFallbackTruncatesLargeUDPResponse(t *testing.T) {
	primary := dnsQueryFunc(func(ctx context.Context, query []byte) ([]byte, error) {
		// 构造一个远大于 512 字节的合法应答：单条 996 字节 TXT 记录
		large := testDNSQuery(binary.BigEndian.Uint16(query[0:2]), "big.example.com")
		binary.BigEndian.PutUint16(large[2:4], 0x8180)
		binary.BigEndian.PutUint16(large[6:8], 1)
		large = append(large, 0xC0, 0x0C, 0x00, 0x10, 0x00, 0x01, 0, 0, 0, 60, 0x03, 0xE4)
		large = append(large, make([]byte, 996)...)
		return large, nil
	})
	fallback := dnsQueryFunc(func(ctx context.Context, query []byte) ([]byte, error) {
		return nil, errors.New("unused")
	})
	_, addr, _ := startTestFallbackServer(t, primary, fallback)

	response := udpExchange(t, addr, testDNSQuery(0x3000, "big.example.com"))
	if response[2]&0x02 == 0 {
		t.Fatal("超过 512 字节的应答应置 TC 位")
	}
	full := tcpExchange(t, addr, testDNSQuery(0x3001, "big.example.com"))
	if len(full) <= 512 {
		t.Fatalf("TCP 应答长度 = %d，期望完整应答", len(full))
	}
}

// 非查询报文直接丢弃，不应触达上游
func TestDNSFallbackIgnoresMalformedQuery(t *testing.T) {
	var calls atomic.Int32
	upstream := dnsQueryFunc(func(ctx context.Context, query []byte) ([]byte, error) {
		calls.Add(1)
		return testDNSResponseWithAddress(1, "f.example.com", [4]byte{7, 7, 7, 7}), nil
	})
	server, addr, _ := startTestFallbackServer(t, upstream, upstream)

	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatalf("dial udp: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(300 * time.Millisecond))
	if _, err := conn.Write([]byte{0x01, 0x02, 0x03}); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 64)
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("畸形查询不应收到应答")
	}
	if calls.Load() != 0 {
		t.Fatalf("畸形查询不应触达上游，实际 %d 次", calls.Load())
	}
	if server.udpConn == nil {
		t.Fatal("server 未启动")
	}
}

// UDP 与 TCP 必须监听同一端口，sing-box 截断后才好回退 TCP
func TestDNSFallbackListensTCPSamePort(t *testing.T) {
	upstream := dnsQueryFunc(func(ctx context.Context, query []byte) ([]byte, error) {
		return testDNSResponseWithAddress(binary.BigEndian.Uint16(query[0:2]), "g.example.com", [4]byte{8, 8, 8, 8}), nil
	})
	server, addr, _ := startTestFallbackServer(t, upstream, upstream)

	tcpPort := server.tcpLn.Addr().(*net.TCPAddr).Port
	udpPort := server.udpConn.LocalAddr().(*net.UDPAddr).Port
	if tcpPort != udpPort {
		t.Fatalf("TCP 端口 %d 与 UDP 端口 %d 不一致", tcpPort, udpPort)
	}
	response := tcpExchange(t, addr, testDNSQuery(0x4000, "g.example.com"))
	if binary.BigEndian.Uint16(response[0:2]) != 0x4000 {
		t.Fatal("TCP 应答 ID 不匹配")
	}
}
