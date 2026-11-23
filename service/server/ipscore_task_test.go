package server

import (
	"testing"

	"github.com/ProxyStation/proxystation/db/configure"
)

// 本地/保留地址发给第三方评分毫无意义，必须在解析阶段就挡掉。
func TestPublicScoreIPRejectsLocalAndInvalid(t *testing.T) {
	for _, raw := range []string{"", "not-an-ip", "127.0.0.1", "10.0.0.1", "192.168.1.5", "::1", "fe80::1", "0.0.0.0"} {
		if got := publicScoreIP(raw); got != "" {
			t.Errorf("publicScoreIP(%q) 应为空，实际 %q", raw, got)
		}
	}
	if got := publicScoreIP(" 8.8.8.8 "); got != "8.8.8.8" {
		t.Errorf("公网 IP 应保留，实际 %q", got)
	}
}

// IP 类型检测已解析出落地 IP 时必须直接复用，避免再次触发 DNS 解析。
func TestScoreTargetIPPrefersDetectedAddr(t *testing.T) {
	s := &configure.ServerRaw{Host: "example.com", IPTypeAddr: "14.137.237.91"}
	got, err := scoreTargetIP(s)
	if err != nil || got != "14.137.237.91" {
		t.Fatalf("应复用 IPTypeAddr，实际 %q / %v", got, err)
	}
}

// 节点地址本身就是公网 IP 字面量（含方括号 IPv6）时无需 DNS。
func TestScoreTargetIPUsesIPLiteralHost(t *testing.T) {
	s := &configure.ServerRaw{Host: "[2001:4860:4860::8888]"}
	got, err := scoreTargetIP(s)
	if err != nil || got != "2001:4860:4860::8888" {
		t.Fatalf("应解析出方括号 IPv6 字面量，实际 %q / %v", got, err)
	}
}

// 内网节点无法评分，应明确失败而不是把地址发出去。
func TestScoreTargetIPRejectsPrivateHost(t *testing.T) {
	s := &configure.ServerRaw{Host: "192.168.1.10"}
	if _, err := scoreTargetIP(s); err == nil {
		t.Fatal("内网地址应报错")
	}
}
