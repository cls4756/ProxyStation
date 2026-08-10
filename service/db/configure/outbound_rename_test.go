package configure

import (
	"path/filepath"
	"testing"

	"github.com/ProxyStation/proxystation/db"
)

func setupRenameDB(t *testing.T) {
	t.Helper()
	if err := db.Init(filepath.Join(t.TempDir(), "test.db")); err != nil {
		t.Fatalf("db init: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

// 改名必须把配置连同 DB key 一起搬走，否则旧 key 残留会让出站列表出现幽灵条目。
func TestRenameOutboundMovesConfig(t *testing.T) {
	setupRenameDB(t)

	if err := AddOutbound("hk"); err != nil {
		t.Fatalf("AddOutbound: %v", err)
	}
	mode := ModeStable
	if err := SetOutbound("hk", &Outbound{Name: "hk", ProbeMode: "fast", Target: OutboundTarget{TargetType: "group", GroupID: "g1", Mode: mode}}); err != nil {
		t.Fatalf("SetOutbound: %v", err)
	}

	if err := RenameOutbound("hk", "hk2"); err != nil {
		t.Fatalf("RenameOutbound: %v", err)
	}

	names := GetOutboundNames()
	for _, n := range names {
		if n == "hk" {
			t.Fatalf("旧名字仍在出站列表里：%v", names)
		}
	}
	o := GetOutbound("hk2")
	if o == nil {
		t.Fatal("新名字读不到出站配置")
	}
	if o.Name != "hk2" {
		t.Errorf("Outbound.Name 应同步更新，实际 %q", o.Name)
	}
	if o.ProbeMode != "fast" || o.Target.GroupID != "g1" || o.Target.Mode != mode {
		t.Errorf("改名不应丢失原有配置，实际 %+v", o)
	}
}

// 改名后所有引用旧名字的地方都要跟着走，漏掉任何一处对应功能会静默失效。
func TestRenameOutboundRetargetsReferences(t *testing.T) {
	setupRenameDB(t)

	if err := AddOutbound("hk"); err != nil {
		t.Fatalf("AddOutbound: %v", err)
	}
	if err := SetServers([]ServerRaw{
		{Name: "a", Host: "a.example.com", Port: 443, Type: "vmess", FrontProxy: "hk"},
		{Name: "b", Host: "b.example.com", Port: 443, Type: "vmess", FrontProxy: "proxy"},
	}); err != nil {
		t.Fatalf("SetServers: %v", err)
	}
	if err := AppendSubscriptions([]*SubscriptionRaw{{
		ID:            "sub1",
		ProxyOutbound: "hk",
		Servers:       []ServerRaw{{Name: "c", Host: "c.example.com", Port: 443, FrontProxy: "hk"}},
	}}); err != nil {
		t.Fatalf("AppendSubscriptions: %v", err)
	}
	if err := SetRoutingRules([]RoutingRule{
		{ID: "r1", Action: RuleActionOutbound, OutboundName: "hk"},
		{ID: "r2", Action: RuleActionOutbound, OutboundName: "proxy"},
	}); err != nil {
		t.Fatalf("SetRoutingRules: %v", err)
	}

	if err := RenameOutbound("hk", "hk-new"); err != nil {
		t.Fatalf("RenameOutbound: %v", err)
	}

	servers := GetServers()
	if servers[0].FrontProxy != "hk-new" {
		t.Errorf("节点前置代理应更新为 hk-new，实际 %q", servers[0].FrontProxy)
	}
	if servers[1].FrontProxy != "proxy" {
		t.Errorf("引用其他出站的节点不应被改动，实际 %q", servers[1].FrontProxy)
	}
	sub := GetSubscription(0)
	if sub.ProxyOutbound != "hk-new" {
		t.Errorf("订阅拉取代理应更新为 hk-new，实际 %q", sub.ProxyOutbound)
	}
	if sub.Servers[0].FrontProxy != "hk-new" {
		t.Errorf("订阅节点前置代理应更新为 hk-new，实际 %q", sub.Servers[0].FrontProxy)
	}
	rules := GetRoutingRules()
	if rules[0].OutboundName != "hk-new" {
		t.Errorf("路由规则出站应更新为 hk-new，实际 %q", rules[0].OutboundName)
	}
	if rules[1].OutboundName != "proxy" {
		t.Errorf("引用其他出站的规则不应被改动，实际 %q", rules[1].OutboundName)
	}
}

// proxy 是内核配置里硬编码的兜底出站 tag，改掉会让生成的配置失去出口。
func TestRenameOutboundRejectsProxy(t *testing.T) {
	setupRenameDB(t)

	if err := RenameOutbound("proxy", "main"); err == nil {
		t.Fatal("默认出站 proxy 不应允许改名")
	}
}

func TestRenameOutboundRejectsConflictAndInvalidName(t *testing.T) {
	setupRenameDB(t)

	for _, n := range []string{"hk", "jp"} {
		if err := AddOutbound(n); err != nil {
			t.Fatalf("AddOutbound %v: %v", n, err)
		}
		if err := SetOutbound(n, &Outbound{Name: n}); err != nil {
			t.Fatalf("SetOutbound %v: %v", n, err)
		}
	}

	if err := RenameOutbound("hk", "jp"); err == nil {
		t.Error("重名不应通过")
	}
	// 名字会拼进 outbound.<name> 的 bucket 名和 REST 路径，含 / 会破坏两者
	if err := RenameOutbound("hk", "a/b"); err == nil {
		t.Error("含 / 的名字不应通过")
	}
	if err := RenameOutbound("hk", "direct"); err == nil {
		t.Error("保留名不应通过")
	}
	if err := RenameOutbound("hk", "  "); err == nil {
		t.Error("空名字不应通过")
	}
	// 校验失败后不应留下半迁移状态
	if GetOutbound("hk") == nil {
		t.Error("改名失败后原出站应保持可用")
	}
}
