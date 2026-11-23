package configure

import (
	"testing"

	"github.com/ProxyStation/proxystation/db"
)

func serverNames(servers []ServerRaw) []string {
	names := make([]string, len(servers))
	for i := range servers {
		names[i] = servers[i].Name
	}
	return names
}

func TestSubscriptionServersStoredPerRow(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	sub := &SubscriptionRaw{ID: "sub-row", Name: "s", Servers: []ServerRaw{
		{Name: "a", Host: "a.example", Port: 1, Latency: 11},
		{Name: "b", Host: "b.example", Port: 2, Latency: 22},
		{Name: "c", Host: "c.example", Port: 3, Latency: 33},
	}}
	if err := AppendSubscriptions([]*SubscriptionRaw{sub}); err != nil {
		t.Fatalf("append: %v", err)
	}

	// 节点应按行存在列式 nodes 表的独立 key 下。
	if n := nodeLen(nodeBucket, subscriptionServerKey("sub-row")); n != 3 {
		t.Fatalf("server rows = %d, want 3", n)
	}
	if n, _ := db.ListLen("touch", subscriptionServerKey("sub-row")); n != 0 {
		t.Fatalf("legacy JSON rows should be gone, got %d", n)
	}
	// 订阅元数据应落到列式 subscriptions 表，旧 list_items 布局清空。
	if n, _ := db.ListLen("touch", "subscriptions"); n != 0 {
		t.Fatalf("legacy JSON subscription rows should be gone, got %d", n)
	}
	if doc, err := getSubscriptionDoc(0); err != nil || doc.ID != "sub-row" {
		t.Fatalf("subscription doc missing from columnar table: %+v err=%v", doc, err)
	}

	// 读回时节点被重新挂上。
	got := GetSubscription(0)
	if got == nil || len(got.Servers) != 3 || got.Servers[2].Name != "c" {
		t.Fatalf("unexpected subscription: %+v", got)
	}
	if n := GetLenSubscriptionServers(0); n != 3 {
		t.Fatalf("GetLenSubscriptionServers = %d, want 3", n)
	}
	summaries := GetSubscriptionSummaries()
	if len(summaries) != 1 || summaries[0].ServerCount != 3 {
		t.Fatalf("unexpected summaries: %+v", summaries)
	}
}

func TestUpdateSingleSubscriptionServerKeepsSiblings(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	sub := &SubscriptionRaw{ID: "sub-upd", Servers: []ServerRaw{
		{Name: "a", Host: "a.example", Port: 1, Latency: 1},
		{Name: "b", Host: "b.example", Port: 2, Latency: 2},
		{Name: "c", Host: "c.example", Port: 3, Latency: 3},
	}}
	if err := AppendSubscriptions([]*SubscriptionRaw{sub}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := UpdateSubscriptionServer(0, 1, func(s *ServerRaw) { s.Latency = 99 }); err != nil {
		t.Fatalf("update: %v", err)
	}
	got := GetSubscription(0)
	if got.Servers[0].Latency != 1 || got.Servers[1].Latency != 99 || got.Servers[2].Latency != 3 {
		t.Fatalf("unexpected latencies: %+v", got.Servers)
	}
}

func TestGetSubscriptionServerIsExact(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	sub := &SubscriptionRaw{ID: "sub-exact", Servers: []ServerRaw{
		{Name: "a", Host: "a.example", Port: 1},
		{Name: "b", Host: "b.example", Port: 2},
	}}
	if err := AppendSubscriptions([]*SubscriptionRaw{sub}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if s := GetSubscriptionServer(0, 1); s == nil || s.Name != "b" {
		t.Fatalf("exact read failed: %+v", s)
	}
	if s := GetSubscriptionServer(0, 5); s != nil {
		t.Fatalf("expected nil for out-of-range, got %+v", s)
	}
	byIndex := GetSubscriptionServersByIndexes(0, []int{0, 2})
	if len(byIndex) != 1 || byIndex[0].Name != "a" {
		t.Fatalf("batch exact read: %+v", byIndex)
	}
}

func TestMigrateSubscriptionStorage(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)

	// 直接写入旧布局：节点内嵌在订阅文档里。
	legacy := []SubscriptionRaw{{
		ID:   "legacy-sub",
		Name: "legacy",
		Servers: []ServerRaw{
			{Name: "a", Host: "a.example", Port: 1, Latency: 7},
			{Name: "b", Host: "b.example", Port: 2, Latency: 8},
		},
	}}
	if err := db.Set("touch", "subscriptions", legacy); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	if n := nodeLen(nodeBucket, subscriptionServerKey("legacy-sub")); n != 0 {
		t.Fatalf("expected no migrated rows before migration")
	}

	if err := MigrateSubscriptionStorage(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if n := nodeLen(nodeBucket, subscriptionServerKey("legacy-sub")); n != 2 {
		t.Fatalf("server rows after migration = %d, want 2", n)
	}
	if n, _ := db.ListLen("touch", "subscriptions"); n != 0 {
		t.Fatalf("legacy JSON subscription rows should be gone after migration, got %d", n)
	}
	if doc, err := getSubscriptionDoc(0); err != nil || doc.ID != "legacy-sub" {
		t.Fatalf("migrated subscription doc missing from columnar table: %+v err=%v", doc, err)
	}
	got := GetSubscription(0)
	if got == nil || len(got.Servers) != 2 || got.Servers[1].Latency != 8 {
		t.Fatalf("unexpected subscription after migration: %+v", got)
	}

	// 再次迁移应为幂等空操作。
	if err := MigrateSubscriptionStorage(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if n := nodeLen(nodeBucket, subscriptionServerKey("legacy-sub")); n != 2 {
		t.Fatalf("idempotent migration changed rows: %d", n)
	}

	// 迁移后单节点更新仍然正常。
	if err := UpdateSubscriptionServer(0, 0, func(s *ServerRaw) { s.Latency = 123 }); err != nil {
		t.Fatalf("update after migration: %v", err)
	}
	updated := GetSubscriptionServer(0, 0)
	if updated == nil || updated.Latency != 123 {
		t.Fatalf("latency = %+v, want 123", updated)
	}
}

func TestRemoveSubscriptionsDeletesServerRows(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	if err := AppendSubscriptions([]*SubscriptionRaw{
		{ID: "keep", Servers: []ServerRaw{{Name: "k", Host: "k.example", Port: 1}}},
		{ID: "drop", Servers: []ServerRaw{{Name: "d", Host: "d.example", Port: 2}}},
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := RemoveSubscriptions([]int{1}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if n := nodeLen(nodeBucket, subscriptionServerKey("drop")); n != 0 {
		t.Fatalf("removed subscription still has %d server rows", n)
	}
	if n := nodeLen(nodeBucket, subscriptionServerKey("keep")); n != 1 {
		t.Fatalf("kept subscription server rows = %d, want 1", n)
	}
	if got := GetSubscription(0); got == nil || got.ID != "keep" {
		t.Fatalf("unexpected remaining subscription: %+v", got)
	}
}

func TestGetSubscriptionServersPage(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	servers := make([]ServerRaw, 5)
	for i := range servers {
		servers[i] = ServerRaw{Name: namesIndex(i), Host: "h", Port: i + 1}
	}
	if err := AppendSubscriptions([]*SubscriptionRaw{{ID: "paged", Servers: servers}}); err != nil {
		t.Fatalf("append: %v", err)
	}

	page, total, err := GetSubscriptionServersPage(0, 1, 2)
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if total != 5 || len(page) != 2 || page[0].Name != namesIndex(1) || page[1].Name != namesIndex(2) {
		t.Fatalf("unexpected page: total=%d page=%v", total, serverNames(page))
	}

	// limit<=0 返回 offset 之后的全部
	all, total, err := GetSubscriptionServersPage(0, 0, 0)
	if err != nil || total != 5 || len(all) != 5 {
		t.Fatalf("all page: total=%d len=%d err=%v", total, len(all), err)
	}

	// 越界 offset 返回空页但仍带总数
	empty, total, err := GetSubscriptionServersPage(0, 99, 10)
	if err != nil || total != 5 || len(empty) != 0 {
		t.Fatalf("out of range: total=%d len=%d err=%v", total, len(empty), err)
	}
}

func namesIndex(i int) string {
	return "node-" + string(rune('a'+i))
}
