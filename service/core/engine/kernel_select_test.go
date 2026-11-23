package engine

import (
	"testing"

	"github.com/ProxyStation/proxystation/db/configure"
)

// 主内核与探测内核必须解析出同一个内核，否则"直连真连检测"与"绑定出站后复用主内核
// 检测"会因为内核不同给出不一致的结果（本次回归：auto 模式下主内核选 sing-box、
// 探测内核却选了 xray）。
func TestSelectKernelMatchesProbeInstanceKernel(t *testing.T) {
	setupTestDB(t)
	if err := configure.SetServers([]configure.ServerRaw{
		{Name: "http", Host: "http.example.com", Port: 443, Type: "http"},
		{Name: "hy2", Host: "hy2.example.com", Port: 443, Type: "hysteria2"},
	}); err != nil {
		t.Fatalf("set servers: %v", err)
	}
	bindOutboundToServer(t, "proxy", 0)

	httpNode := configure.GetServer(0)
	hy2Node := configure.GetServer(1)
	if httpNode == nil || hy2Node == nil {
		t.Fatal("server setup failed")
	}

	cases := []struct {
		mode string
		want KernelType
	}{
		{"singbox", KernelSingbox},
		{"xray", KernelXray},
		{"v2ray", KernelV2ray},
		{"SINGBOX", KernelSingbox}, // 大小写与空白不应影响解析
		{" xray ", KernelXray},
	}
	for _, tc := range cases {
		setting := configure.GetSettingNotNil()
		setting.KernelMode = tc.mode
		if got := Manager.selectKernel(setting); got != tc.want {
			t.Fatalf("显式内核 %q：selectKernel = %v，期望 %v", tc.mode, got, tc.want)
		}
		if got := probeInstanceKernel(httpNode, tc.mode); got != tc.want {
			t.Fatalf("显式内核 %q：probeInstanceKernel = %v，期望 %v", tc.mode, got, tc.want)
		}
	}

	// auto：主内核与探测内核必须一致（默认偏好相同）。
	setting := configure.GetSettingNotNil()
	setting.KernelMode = "auto"
	mainKernel := Manager.selectKernel(setting)
	if probeKernel := probeInstanceKernel(httpNode, "auto"); probeKernel != mainKernel {
		t.Fatalf("auto 模式两端内核不一致：主内核=%v，探测内核=%v", mainKernel, probeKernel)
	}

	// auto + 需要 sing-box 的协议：探测内核必须强制 sing-box。
	if got := probeInstanceKernel(hy2Node, "auto"); got != KernelSingbox {
		t.Fatalf("hysteria2 在 auto 下应强制 sing-box，实际 %v", got)
	}
}

func TestExplicitKernelModeParsing(t *testing.T) {
	cases := map[string]struct {
		want KernelType
		ok   bool
	}{
		"singbox": {KernelSingbox, true},
		"xray":    {KernelXray, true},
		"v2ray":   {KernelV2ray, true},
		"auto":    {"", false},
		"":        {"", false},
		"other":   {"", false},
	}
	for mode, want := range cases {
		got, ok := explicitKernelMode(mode)
		if got != want.want || ok != want.ok {
			t.Fatalf("explicitKernelMode(%q) = (%v,%v)，期望 (%v,%v)", mode, got, ok, want.want, want.ok)
		}
	}
}
