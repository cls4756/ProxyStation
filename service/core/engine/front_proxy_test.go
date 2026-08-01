package engine

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProxyStation/proxystation/db"
	"github.com/ProxyStation/proxystation/db/configure"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	if err := db.Init(filepath.Join(t.TempDir(), "test.db")); err != nil {
		t.Fatalf("db init: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

// bindOutboundToServer 注册一个出站并绑定到手动节点列表中的第 index 个节点
func bindOutboundToServer(t *testing.T, name string, index int) {
	t.Helper()
	if name != "proxy" {
		if err := configure.AddOutbound(name); err != nil {
			t.Fatalf("add outbound %s: %v", name, err)
		}
	}
	o := &configure.Outbound{
		Name: name,
		Target: configure.OutboundTarget{
			TargetType: "node",
			NodeRef:    &configure.NodeRef{Type: "server", Index: index},
		},
	}
	if err := configure.SetOutbound(name, o); err != nil {
		t.Fatalf("set outbound %s: %v", name, err)
	}
}

func TestResolveFrontProxyTag(t *testing.T) {
	setupTestDB(t)

	// servers[0] <- proxy, servers[1] <- hk, servers[2] <- jp（未绑定出站）
	if err := configure.SetServers([]configure.ServerRaw{
		{Name: "a", Host: "a.example.com", Port: 443, Type: "vmess"},
		{Name: "b", Host: "b.example.com", Port: 443, Type: "vmess"},
		{Name: "c", Host: "c.example.com", Port: 443, Type: "vmess"},
	}); err != nil {
		t.Fatalf("set servers: %v", err)
	}
	bindOutboundToServer(t, "proxy", 0)
	bindOutboundToServer(t, "hk", 1)

	validTags := collectValidOutboundTags()
	for _, want := range []string{"proxy", "hk", "direct", "block"} {
		if !validTags[want] {
			t.Fatalf("collectValidOutboundTags 缺少 %s: %v", want, validTags)
		}
	}

	tests := []struct {
		name  string
		front string
		on    string
		want  string
	}{
		{"正常引用已绑定出站", "hk", "proxy", "hk"},
		{"未设置前置代理", "", "proxy", ""},
		{"自引用应被拒绝", "proxy", "proxy", ""},
		{"引用不存在的出站", "nope", "proxy", ""},
		{"引用 direct 合法", "direct", "proxy", "direct"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &configure.ServerRaw{Name: "x", FrontProxy: tc.front}
			if got := resolveFrontProxyTag(s, tc.on, validTags); got != tc.want {
				t.Errorf("resolveFrontProxyTag(front=%q, on=%q) = %q, 期望 %q",
					tc.front, tc.on, got, tc.want)
			}
		})
	}
}

// 环形引用必须被拦下，否则内核建链时会死循环
func TestResolveFrontProxyTagDetectsCycle(t *testing.T) {
	setupTestDB(t)

	// proxy -> servers[0].FrontProxy=hk，hk -> servers[1].FrontProxy=proxy
	if err := configure.SetServers([]configure.ServerRaw{
		{Name: "a", Host: "a.example.com", Port: 443, Type: "vmess", FrontProxy: "hk"},
		{Name: "b", Host: "b.example.com", Port: 443, Type: "vmess", FrontProxy: "proxy"},
	}); err != nil {
		t.Fatalf("set servers: %v", err)
	}
	bindOutboundToServer(t, "proxy", 0)
	bindOutboundToServer(t, "hk", 1)

	validTags := collectValidOutboundTags()

	s := configure.GetServer(0)
	if got := resolveFrontProxyTag(s, "proxy", validTags); got != "" {
		t.Errorf("环形引用应返回空，实际返回 %q", got)
	}

	// 三段环 a -> b -> c -> a 同样要拦住
	setupTestDB(t)
	if err := configure.SetServers([]configure.ServerRaw{
		{Name: "a", Host: "a.example.com", Port: 443, Type: "vmess", FrontProxy: "hk"},
		{Name: "b", Host: "b.example.com", Port: 443, Type: "vmess", FrontProxy: "jp"},
		{Name: "c", Host: "c.example.com", Port: 443, Type: "vmess", FrontProxy: "proxy"},
	}); err != nil {
		t.Fatalf("set servers: %v", err)
	}
	bindOutboundToServer(t, "proxy", 0)
	bindOutboundToServer(t, "hk", 1)
	bindOutboundToServer(t, "jp", 2)

	validTags = collectValidOutboundTags()
	if got := resolveFrontProxyTag(configure.GetServer(0), "proxy", validTags); got != "" {
		t.Errorf("三段环应返回空，实际返回 %q", got)
	}
}

// 非环形的多级链路不能被误判成环
func TestResolveFrontProxyTagAllowsChain(t *testing.T) {
	setupTestDB(t)

	// proxy -> hk -> jp（jp 无前置），是合法链路
	if err := configure.SetServers([]configure.ServerRaw{
		{Name: "a", Host: "a.example.com", Port: 443, Type: "vmess", FrontProxy: "hk"},
		{Name: "b", Host: "b.example.com", Port: 443, Type: "vmess", FrontProxy: "jp"},
		{Name: "c", Host: "c.example.com", Port: 443, Type: "vmess"},
	}); err != nil {
		t.Fatalf("set servers: %v", err)
	}
	bindOutboundToServer(t, "proxy", 0)
	bindOutboundToServer(t, "hk", 1)
	bindOutboundToServer(t, "jp", 2)

	validTags := collectValidOutboundTags()
	if got := resolveFrontProxyTag(configure.GetServer(0), "proxy", validTags); got != "hk" {
		t.Errorf("合法多级链路应返回 hk，实际返回 %q", got)
	}
}

// 订阅走代理拉取时，其节点应继承订阅的代理出站作为前置代理
func TestSubscriptionNodeInheritsProxyOutbound(t *testing.T) {
	setupTestDB(t)

	if err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{
		{
			ID:            "sub1",
			Name:          "带代理的订阅",
			ProxyOutbound: "hk",
			Servers: []configure.ServerRaw{
				{Name: "n1", Host: "n1.example.com", Port: 443, Type: "vmess"},
				{Name: "n2", Host: "n2.example.com", Port: 443, Type: "vmess", FrontProxy: "jp"},
			},
		},
		{
			ID:   "sub2",
			Name: "直连订阅",
			Servers: []configure.ServerRaw{
				{Name: "m1", Host: "m1.example.com", Port: 443, Type: "vmess"},
			},
		},
	}); err != nil {
		t.Fatalf("append subscriptions: %v", err)
	}

	// 未显式设置的节点继承订阅的 ProxyOutbound
	got := getServerRaw(&configure.NodeRef{Type: "sub_server", Index: 0, Sub: 0})
	if got == nil {
		t.Fatal("getServerRaw 返回 nil")
	}
	if got.FrontProxy != "hk" {
		t.Errorf("应继承订阅代理出站 hk，实际 %q", got.FrontProxy)
	}

	// 节点自身已设置时不被订阅覆盖
	got = getServerRaw(&configure.NodeRef{Type: "sub_server", Index: 1, Sub: 0})
	if got.FrontProxy != "jp" {
		t.Errorf("节点自身设置的 jp 不应被覆盖，实际 %q", got.FrontProxy)
	}

	// 订阅未配代理时节点保持无前置
	got = getServerRaw(&configure.NodeRef{Type: "sub_server", Index: 0, Sub: 1})
	if got.FrontProxy != "" {
		t.Errorf("直连订阅的节点不应有前置代理，实际 %q", got.FrontProxy)
	}
}

// 验证前置代理最终真的落到了两个内核的配置字段里
func TestFrontProxyLandsInGeneratedConfig(t *testing.T) {
	setupTestDB(t)

	if err := configure.SetServers([]configure.ServerRaw{
		{Name: "a", Host: "a.example.com", Port: 443, Type: "vmess",
			Link:       "vmess://" + vmessB64(`{"add":"a.example.com","port":"443","id":"11111111-1111-1111-1111-111111111111","net":"tcp"}`),
			FrontProxy: "hk"},
		{Name: "b", Host: "b.example.com", Port: 443, Type: "vmess",
			Link: "vmess://" + vmessB64(`{"add":"b.example.com","port":"443","id":"22222222-2222-2222-2222-222222222222","net":"tcp"}`)},
	}); err != nil {
		t.Fatalf("set servers: %v", err)
	}
	bindOutboundToServer(t, "proxy", 0)
	bindOutboundToServer(t, "hk", 1)

	setting := configure.GetSettingNotNil()

	sb, err := BuildSingboxConfig(setting)
	if err != nil {
		t.Fatalf("BuildSingboxConfig: %v", err)
	}
	var found bool
	for _, ob := range sb.Outbounds {
		if ob.Tag == "proxy" {
			found = true
			if ob.Detour != "hk" {
				t.Errorf("singbox proxy.detour = %q, 期望 hk", ob.Detour)
			}
		}
		if ob.Tag == "hk" && ob.Detour != "" {
			t.Errorf("singbox hk.detour 应为空, 实际 %q", ob.Detour)
		}
	}
	if !found {
		t.Error("singbox 配置中未找到 proxy 出站")
	}
	if b, _ := json.Marshal(sb); !strings.Contains(string(b), `"detour":"hk"`) {
		t.Error("singbox JSON 中缺少 detour 字段")
	}

	xr, err := BuildXrayConfig(setting)
	if err != nil {
		t.Fatalf("BuildXrayConfig: %v", err)
	}
	found = false
	for _, ob := range xr.Outbounds {
		if ob.Tag == "proxy" {
			found = true
			if ob.StreamSettings == nil || ob.StreamSettings.Sockopt == nil {
				t.Fatal("xray proxy 缺少 streamSettings.sockopt")
			}
			if got := ob.StreamSettings.Sockopt.DialerProxy; got != "hk" {
				t.Errorf("xray proxy dialerProxy = %q, 期望 hk", got)
			}
		}
	}
	if !found {
		t.Error("xray 配置中未找到 proxy 出站")
	}
	if b, _ := json.Marshal(xr); !strings.Contains(string(b), `"dialerProxy":"hk"`) {
		t.Error("xray JSON 中缺少 dialerProxy 字段")
	}
}

func vmessB64(jsonStr string) string {
	return base64.StdEncoding.EncodeToString([]byte(jsonStr))
}
