package configure

import (
	"testing"

	"github.com/ProxyStation/proxystation/db"
)

func TestMigrateNodeStorageFromLegacyJSON(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	score := 55
	legacyServers := []ServerRaw{
		{Name: "m1", Host: "m1.example", Port: 1, Latency: 11, IPScore: &score},
		{Name: "m2", Host: "m2.example", Port: 2, Source: "manual"},
	}
	if err := db.Set("touch", "servers", legacyServers); err != nil {
		t.Fatalf("seed legacy servers: %v", err)
	}
	subServers := []ServerRaw{{Name: "s1n", Host: "s1.example", Port: 9, Latency: 9}}
	if err := db.Set("touch", subscriptionServerKey("s1"), subServers); err != nil {
		t.Fatalf("seed legacy sub servers: %v", err)
	}
	if n := nodeLen(nodeBucket, manualServersKey); n != 0 {
		t.Fatalf("nodes should be empty before migration, got %d", n)
	}

	if err := MigrateNodeStorage(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if n := nodeLen(nodeBucket, manualServersKey); n != 2 {
		t.Fatalf("manual nodes = %d, want 2", n)
	}
	if n := nodeLen(nodeBucket, subscriptionServerKey("s1")); n != 1 {
		t.Fatalf("subscription nodes = %d, want 1", n)
	}
	// 旧的 JSON 行应被清掉。
	if n, _ := db.ListLen("touch", "servers"); n != 0 {
		t.Fatalf("legacy manual rows remain: %d", n)
	}
	if n, _ := db.ListLen("touch", subscriptionServerKey("s1")); n != 0 {
		t.Fatalf("legacy subscription rows remain: %d", n)
	}

	got := GetServer(0)
	if got == nil || got.Name != "m1" || got.Latency != 11 || got.IPScore == nil || *got.IPScore != 55 {
		t.Fatalf("migrated server mismatch: %+v", got)
	}
	if sub := GetSubscriptionServer(0, 0); sub != nil {
		// s1 不是订阅文档，GetSubscriptionServer 走的是订阅下标，这里不该命中。
		t.Fatalf("unexpected subscription server: %+v", sub)
	}
	if !existsNode(nodeBucket, subscriptionServerKey("s1"), 0) {
		t.Fatalf("subscription node not migrated")
	}

	// 幂等。
	if err := MigrateNodeStorage(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if n := nodeLen(nodeBucket, manualServersKey); n != 2 {
		t.Fatalf("idempotent migration changed rows: %d", n)
	}
}

func existsNode(bucket, key string, index int) bool {
	return nodeGet(bucket, key, index) != nil
}

// 节点表的数组语义需与旧的列表存储一致：下标连续、删除后前移。
func TestNodeStoreArraySemantics(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	if err := AppendServers([]*ServerRaw{{Name: "n0"}, {Name: "n1"}, {Name: "n2"}, {Name: "n3"}, {Name: "n4"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if got := GetLenServers(); got != 5 {
		t.Fatalf("len = %d, want 5", got)
	}

	if err := SetServer(1, &ServerRaw{Name: "n1-set"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := GetServer(1); got == nil || got.Name != "n1-set" {
		t.Fatalf("set result: %+v", got)
	}
	if err := SetServer(99, &ServerRaw{Name: "x"}); err == nil {
		t.Fatalf("set out of range should fail")
	}

	if err := UpdateServer(2, func(s *ServerRaw) { s.Latency = 42 }); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := GetServer(2); got == nil || got.Latency != 42 {
		t.Fatalf("update result: %+v", got)
	}

	// 删除 1、3 后下标应压缩为 0..2，且顺序保留。
	if err := RemoveServers([]int{1, 3}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := GetLenServers(); got != 3 {
		t.Fatalf("len after remove = %d, want 3", got)
	}
	want := []string{"n0", "n2", "n4"}
	for i, name := range want {
		s := GetServer(i)
		if s == nil || s.Name != name {
			t.Fatalf("index %d = %+v, want %s", i, s, name)
		}
	}
	if s := GetServer(3); s != nil {
		t.Fatalf("index 3 should be gone, got %+v", s)
	}

	// 批量精确读取。
	many := GetServersByIndexes([]int{0, 2})
	if len(many) != 2 || many[0].Name != "n0" || many[2].Name != "n4" {
		t.Fatalf("GetServersByIndexes = %+v", many)
	}
}

// 部分迁移状态：某 key 已有新数据、另一个 key 还是旧 JSON 行时都要正确处理，
// 且不能用旧行覆盖已有的新数据。
func TestMigrateNodeStorageHandlesPartialState(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	if err := nodeAppend(nodeBucket, manualServersKey, []ServerRaw{{Name: "new", Host: "new.example", Port: 1}}); err != nil {
		t.Fatalf("seed new nodes: %v", err)
	}
	// 同 key 的遗留旧行，迁移时应被忽略并清理，不能覆盖新数据。
	if err := db.Set("touch", manualServersKey, []ServerRaw{{Name: "old", Host: "old.example", Port: 2}}); err != nil {
		t.Fatalf("seed legacy same key: %v", err)
	}
	// 另一个 key 仍是旧布局。
	if err := db.Set("touch", subscriptionServerKey("s2"), []ServerRaw{{Name: "legacy-sub", Host: "l.example", Port: 3}}); err != nil {
		t.Fatalf("seed legacy sub: %v", err)
	}

	if err := MigrateNodeStorage(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	servers := GetServers()
	if len(servers) != 1 || servers[0].Name != "new" {
		t.Fatalf("manual nodes = %+v, want only new", servers)
	}
	if n := nodeLen(nodeBucket, subscriptionServerKey("s2")); n != 1 {
		t.Fatalf("legacy sub nodes = %d, want 1", n)
	}
	if n, _ := db.ListLen("touch", manualServersKey); n != 0 {
		t.Fatalf("legacy manual rows remain: %d", n)
	}
	if n, _ := db.ListLen("touch", subscriptionServerKey("s2")); n != 0 {
		t.Fatalf("legacy sub rows remain: %d", n)
	}
}
