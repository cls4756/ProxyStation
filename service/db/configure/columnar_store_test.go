package configure

import (
	"reflect"
	"testing"
	"time"

	"github.com/ProxyStation/proxystation/db"
)

// ---- groups ----

func TestGroupRefsColumnarMutations(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	if err := SetServers(identityGroupTestServers()); err != nil {
		t.Fatal(err)
	}
	if err := AppendSubscriptions([]*SubscriptionRaw{{ID: "source", Servers: []ServerRaw{identityGroupTestServers()[2]}}}); err != nil {
		t.Fatal(err)
	}
	refA := ServerRef{Type: "server", Index: 1}
	refB := ServerRef{Type: "sub_server", Index: 0}
	if err := AppendGroup(&Group{ID: "g1", Name: "G", Servers: []ServerRef{refA}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := AddServersToGroup(0, []ServerRef{refA, refB}); err != nil {
		t.Fatalf("add: %v", err)
	}
	// 重复引用不能重复插入
	if got := GetGroup(0).Servers; len(got) != 2 {
		t.Fatalf("servers=%v want 2 (dedup)", got)
	}
	cloned := GetGroup(0).Servers[1]
	if cloned.Type != "server" || GetServer(cloned.Index).Link != identityGroupTestServers()[2].Link {
		t.Fatal("adding a subscription node to a manual group must create a full independent clone")
	}
	if err := RemoveServersFromGroup(0, []ServerRef{refA}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := GetGroup(0).Servers; len(got) != 1 || got[0] != cloned {
		t.Fatalf("after remove servers=%v want [clone]", got)
	}
	// 通过 ID 精确定位且不依赖下标
	idx, g := GetGroupByID("g1")
	if idx != 0 || g == nil || len(g.Servers) != 1 {
		t.Fatalf("GetGroupByID = %d %+v", idx, g)
	}
	// 删除分组后下标压缩且引用被清掉
	if err := AppendGroup(&Group{ID: "g2", Name: "G2"}); err != nil {
		t.Fatalf("append g2: %v", err)
	}
	if err := RemoveGroups([]int{0}); err != nil {
		t.Fatalf("remove group: %v", err)
	}
	if GetLenGroups() != 1 || GetGroup(0).ID != "g2" {
		t.Fatalf("groups after remove = %+v", GetGroups())
	}
	var n int
	if err := db.ReadDB().QueryRow(`SELECT COUNT(*) FROM group_servers WHERE group_id = ?`, "g1").Scan(&n); err != nil {
		t.Fatalf("count refs: %v", err)
	}
	if n != 0 {
		t.Fatalf("removed group still has %d refs", n)
	}
}

func TestMigrateGroupStorageFromLegacyJSON(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	legacy := []Group{{ID: "g1", Name: "旧分组", Servers: []ServerRef{{Type: "server", Index: 5}}}}
	if err := db.Set("touch", "groups", legacy); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := MigrateGroupStorage(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if n, _ := db.ListLen("touch", "groups"); n != 0 {
		t.Fatalf("legacy rows remain: %d", n)
	}
	got := GetGroups()
	if len(got) != 1 || got[0].ID != "g1" || len(got[0].Servers) != 1 || got[0].Servers[0].Index != 5 {
		t.Fatalf("unexpected groups after migration: %+v", got)
	}
	if err := MigrateGroupStorage(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if GetLenGroups() != 1 {
		t.Fatalf("idempotent migration changed groups: %d", GetLenGroups())
	}
}

// ---- routing rules ----

func TestRoutingRulesColumnarRoundTrip(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	rules := []RoutingRule{
		{ID: "r1", Enabled: true, Name: "a", InboundTags: []string{"in1", "in2"}, Domains: []string{"x.com"}, IPs: []string{"1.1.1.1"}, Ports: "80", Protocol: "tcp", Action: RuleActionOutbound, OutboundName: "hk"},
		{ID: "r2", Action: RuleActionDirect},
	}
	if err := SetRoutingRules(rules); err != nil {
		t.Fatalf("set: %v", err)
	}
	got := GetRoutingRules()
	if !reflect.DeepEqual(got, rules) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, rules)
	}
}

func TestMigrateRoutingRulesFromJSON(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	rules := []RoutingRule{{ID: "r1", Domains: []string{"a.com", "b.com"}, Action: RuleActionBlock}}
	if err := db.Set("system", "routing_rules", rules); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := MigrateRoutingRuleStorage(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if db.Exists("system", "routing_rules") {
		t.Fatal("legacy routing_rules key should be gone")
	}
	got := GetRoutingRules()
	if len(got) != 1 || !reflect.DeepEqual(got[0].Domains, []string{"a.com", "b.com"}) {
		t.Fatalf("unexpected rules after migration: %+v", got)
	}
}

// ---- custom inbounds ----

func TestCustomInboundColumnarRoundTrip(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	inbounds := []CustomInbound{{
		ID: "i1", Tag: "socks", Protocol: "socks", Listen: "127.0.0.1", Port: 1080,
		UDPEnabled: true, Accounts: []InboundAccount{{Username: "u", Password: "p"}},
		SniffDest: []string{"http", "tls"},
		VMess:     &VMessInbound{Transport: "ws", Path: "/x"},
	}}
	if err := SetCustomInbounds(inbounds); err != nil {
		t.Fatalf("set: %v", err)
	}
	got := GetCustomInbounds()
	if len(got) != 1 {
		t.Fatalf("got %d inbounds", len(got))
	}
	g := got[0]
	if g.ID != "i1" || !g.UDPEnabled || g.Port != 1080 || len(g.Accounts) != 1 || !reflect.DeepEqual(g.SniffDest, []string{"http", "tls"}) || g.VMess == nil || g.VMess.Path != "/x" {
		t.Fatalf("unexpected inbound: %+v", g)
	}
}

func TestMigrateCustomInboundsFromJSON(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	inbounds := []CustomInbound{{ID: "i1", Protocol: "http", Port: 8080}}
	if err := db.Set("system", "custom_inbounds", inbounds); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := MigrateCustomInboundStorage(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if db.Exists("system", "custom_inbounds") {
		t.Fatal("legacy custom_inbounds key should be gone")
	}
	got := GetCustomInbounds()
	if len(got) != 1 || got[0].Port != 8080 {
		t.Fatalf("unexpected inbounds after migration: %+v", got)
	}
}

// ---- inbound subscriptions ----

func TestInboundSubscriptionColumnar(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	if err := AppendInboundSubscription(&InboundSubscription{ID: "s1", Name: "one", InboundIDs: []string{"i1", "i2"}, Rules: []InboundSubscriptionRule{{ID: "r1", Action: "proxy"}}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	got := GetInboundSubscriptions()
	if len(got) != 1 || !reflect.DeepEqual(got[0].InboundIDs, []string{"i1", "i2"}) || len(got[0].Rules) != 1 {
		t.Fatalf("unexpected subscription: %+v", got)
	}
	if err := SetInboundSubscription(0, &InboundSubscription{ID: "s1", Name: "one", InboundIDs: []string{"i3"}}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := GetInboundSubscriptions(); !reflect.DeepEqual(got[0].InboundIDs, []string{"i3"}) {
		t.Fatalf("set left stale ids: %+v", got[0].InboundIDs)
	}
	if err := DeleteInboundSubscription(0); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(GetInboundSubscriptions()) != 0 {
		t.Fatalf("delete failed")
	}
}

func TestMigrateInboundSubscriptionsFromJSON(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	legacy := []InboundSubscription{{ID: "s1", Name: "legacy", InboundIDs: []string{"i9"}}}
	if err := db.Set("touch", "inbound_subscriptions", legacy); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := MigrateInboundSubscriptionStorage(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if n, _ := db.ListLen("touch", "inbound_subscriptions"); n != 0 {
		t.Fatalf("legacy rows remain: %d", n)
	}
	got := GetInboundSubscriptions()
	if len(got) != 1 || !reflect.DeepEqual(got[0].InboundIDs, []string{"i9"}) {
		t.Fatalf("unexpected after migration: %+v", got)
	}
}

// ---- outbounds ----

func TestMigrateOutboundStorage(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	if err := db.Set("outbounds", "hk", true); err != nil {
		t.Fatalf("seed marker: %v", err)
	}
	cooldown := 30
	if err := db.Set("outbound.hk", "config", &Outbound{
		Name: "hk", ProbeMode: "fast",
		Target:            OutboundTarget{TargetType: "group", GroupID: "g1", Mode: ModeStable, ActiveNodeRef: &NodeRef{Type: "server", Index: 7}},
		SwitchCooldownSec: &cooldown,
	}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if !db.Exists("outbounds", "hk") {
		t.Fatal("precondition: marker missing")
	}
	if err := MigrateOutboundStorage(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if db.Exists("outbounds", "hk") || db.Exists("outbound.hk", "config") {
		t.Fatal("legacy outbound keys should be gone")
	}
	o := GetOutbound("hk")
	if o == nil || o.ProbeMode != "fast" || o.Target.GroupID != "g1" || o.Target.ActiveNodeRef == nil || o.Target.ActiveNodeRef.Index != 7 || o.SwitchCooldownSec == nil || *o.SwitchCooldownSec != 30 {
		t.Fatalf("unexpected outbound after migration: %+v", o)
	}
	// 无目标的出站不能因为 typed nil 被读成有值指针
	if o.Target.CfDO != nil || o.Target.CfGoodNet != nil || o.Target.NodeRef != nil {
		t.Fatalf("nil nested options became non-nil: %+v", o.Target)
	}
}

// ---- setting ----

func TestSettingColumnarRoundTrip(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	s := NewSetting()
	s.LogLevel = "debug"
	s.Socks5Port = 1234
	disabled := false
	s.Socks5SniffEnabled = &disabled
	s.NodeCleanupRules = []NodeCleanupRule{{GroupIDs: []string{"g1"}, Mode: "fast", IntervalHour: 6}}
	s.Socks5Accounts = []InboundAccount{{Username: "u", Password: "p"}}
	if err := settingSet(s); err != nil {
		t.Fatalf("set: %v", err)
	}
	row, err := settingGetRow()
	if err != nil || row == nil {
		t.Fatalf("get row: %v", err)
	}
	got := NewSetting()
	row.apply(got)
	if got.LogLevel != "debug" || got.Socks5Port != 1234 {
		t.Fatalf("scalar mismatch: %+v", got)
	}
	if got.Socks5SniffEnabled == nil || *got.Socks5SniffEnabled {
		t.Fatalf("pointer tri-state lost: %+v", got.Socks5SniffEnabled)
	}
	if len(got.NodeCleanupRules) != 1 || len(got.Socks5Accounts) != 1 {
		t.Fatalf("nested lists lost: %+v", got)
	}
}

func TestMigrateSettingStorageFromJSON(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	// 模拟旧配置缺字段：只写 logLevel 一个键
	if err := db.Set("system", "setting", map[string]any{"logLevel": "warn"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := MigrateSettingStorage(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if db.Exists("system", "setting") {
		t.Fatal("legacy setting key should be gone")
	}
	row, err := settingGetRow()
	if err != nil || row == nil {
		t.Fatalf("setting row missing: %v", err)
	}
	got := NewSetting()
	row.apply(got)
	if got.LogLevel != "warn" {
		t.Fatalf("logLevel=%q want warn", got.LogLevel)
	}
	// 旧 JSON 缺失的字段必须补成默认值，而不是 0/空
	if got.Socks5Port == 0 || got.WebUsername == "" || got.GroupRealProbeIntervalSec == 0 {
		t.Fatalf("defaults not backfilled: %+v", got)
	}
}

// ---- sessions ----

func TestSessionColumnar(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	if err := SetSession(&Session{SessionID: "s1", Username: "u", LastAccess: time.Now()}); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := GetSession("s1")
	if err != nil || got.Username != "u" {
		t.Fatalf("get: %+v err=%v", got, err)
	}
	all, err := GetAllSessions()
	if err != nil || len(all) != 1 {
		t.Fatalf("all: %+v err=%v", all, err)
	}
	if err := DeleteSession("s1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := GetSession("s1"); err == nil {
		t.Fatal("deleted session still readable")
	}
}

func TestMigrateSessionStorageFromKV(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	if err := db.Set(sessionBucket, "s1", &Session{SessionID: "s1", Username: "u"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := MigrateSessionStorage(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if db.Exists(sessionBucket, "s1") {
		t.Fatal("legacy session key should be gone")
	}
	got, err := GetSession("s1")
	if err != nil || got.Username != "u" {
		t.Fatalf("after migration: %+v err=%v", got, err)
	}
}
