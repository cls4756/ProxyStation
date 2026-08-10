package engine

import (
	"strings"
	"testing"
)

// 用真实内核输出的日志行做样本，验证 outbound 行能补上来源入站。
// 这些行取自实际运行的 sing-box / xray（已剥离 ANSI 颜色码）。
func TestLogWriterAnnotatesInbound(t *testing.T) {
	var got []string
	old := LogCallback
	LogCallback = func(s string) { got = append(got, s) }
	defer func() { LogCallback = old }()

	w := &logWriter{}
	input := strings.Join([]string{
		"INFO[0000] sing-box started (0.02s)",
		"INFO[0016] [3867926420 0ms] inbound/socks[socks-in]: inbound connection from 127.0.0.1:46856",
		"INFO[0016] [3867926420 0ms] inbound/socks[socks-in]: inbound connection to www.example.com:443",
		"INFO[0017] [3867926420 231ms] outbound/direct[direct]: outbound connection to www.example.com:443",
		"INFO[0018] [2133155180 0ms] inbound/http[http-in]: inbound connection from 127.0.0.1:50098",
		"INFO[0018] [2133155180 211ms] outbound/vless[global-proxy]: outbound connection to www.googleapis.com:443",
	}, "\n") + "\n"

	if _, err := w.Write([]byte(input)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	want := []string{
		"INFO[0000] sing-box started (0.02s)",
		"INFO[0017] [3867926420 231ms] outbound/direct[direct]: outbound connection to www.example.com:443 [from: socks-in]",
		"INFO[0018] [2133155180 211ms] outbound/vless[global-proxy]: outbound connection to www.googleapis.com:443 [from: http-in]",
	}
	if len(got) != len(want) {
		t.Fatalf("输出 %d 行，期望 %d 行:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 行:\n  实际 %s\n  期望 %s", i, got[i], want[i])
		}
	}
}

// xray 日志自带 "[入站 -> 出站]" 且没有连接 ID，不应被改动
func TestLogWriterLeavesXrayLinesAlone(t *testing.T) {
	var got []string
	old := LogCallback
	LogCallback = func(s string) { got = append(got, s) }
	defer func() { LogCallback = old }()

	line := "2026/08/06 15:14:37.143268 from tcp:127.0.0.1:46036 accepted tcp:www.example.com:443 [socks-in -> direct]"
	w := &logWriter{}
	if _, err := w.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(got) != 1 || got[0] != line {
		t.Errorf("xray 日志被改动:\n  实际 %v\n  期望 %s", got, line)
	}
}

// 连接中途夭折（只有 inbound 行、没有 outbound 行）不应让缓存无限增长
func TestInboundTagCacheBounded(t *testing.T) {
	var c inboundTagCache
	for i := 0; i < maxInboundTagEntries*3; i++ {
		c.put(string(rune('a'+i%26))+string(rune(i)), "socks-in")
	}
	if n := len(c.cur) + len(c.prev); n > 2*maxInboundTagEntries {
		t.Errorf("缓存 %d 条，超出上限 %d", n, 2*maxInboundTagEntries)
	}
}

// 跨 Write 调用的半行数据要能正确拼接，标签不能丢
func TestLogWriterHandlesSplitLines(t *testing.T) {
	var got []string
	old := LogCallback
	LogCallback = func(s string) { got = append(got, s) }
	defer func() { LogCallback = old }()

	w := &logWriter{}
	w.Write([]byte("INFO[0016] [999 0ms] inbound/socks[socks-in]: inbound connection to a.com:443\nINFO[0017] [999 5ms] outbound/dir"))
	w.Write([]byte("ect[direct]: outbound connection to a.com:443\n"))

	if len(got) != 1 {
		t.Fatalf("输出 %d 行，期望 1 行: %v", len(got), got)
	}
	if !strings.HasSuffix(got[0], "[from: socks-in]") {
		t.Errorf("跨 Write 分割后未补上入站标签: %s", got[0])
	}
}
