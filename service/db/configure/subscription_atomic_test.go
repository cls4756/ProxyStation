package configure

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/ProxyStation/proxystation/db"
)

func setupSubscriptionAtomicTestDB(t *testing.T) {
	t.Helper()
	if err := db.Init(filepath.Join(t.TempDir(), "test.db")); err != nil {
		t.Fatalf("db init: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

func TestUpdateSubscriptionServerKeepsConcurrentNodeResults(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	sub := &SubscriptionRaw{ID: "sub", Servers: []ServerRaw{
		{Name: "a", Host: "a.example", Port: 443, Latency: 10},
		{Name: "b", Host: "b.example", Port: 443, Latency: 20},
	}}
	if err := AppendSubscriptions([]*SubscriptionRaw{sub}); err != nil {
		t.Fatalf("append subscription: %v", err)
	}

	var wg sync.WaitGroup
	for index, latency := range []int{-1, 88} {
		index, latency := index, latency
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := UpdateSubscriptionServer(0, index, func(server *ServerRaw) {
				server.Latency = latency
			}); err != nil {
				t.Errorf("update server %d: %v", index, err)
			}
		}()
	}
	wg.Wait()

	got := GetSubscription(0)
	if got == nil {
		t.Fatal("subscription missing")
	}
	if got.Servers[0].Latency != -1 || got.Servers[1].Latency != 88 {
		t.Fatalf("concurrent results lost: got [%d, %d]", got.Servers[0].Latency, got.Servers[1].Latency)
	}
}

func TestSetSubscriptionPreservesResultsWrittenAfterSnapshot(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	score := 91
	original := &SubscriptionRaw{ID: "sub", Name: "old", Servers: []ServerRaw{
		{Name: "a", Host: "a.example", Port: 443, Latency: 12},
	}}
	if err := AppendSubscriptions([]*SubscriptionRaw{original}); err != nil {
		t.Fatalf("append subscription: %v", err)
	}

	stale := GetSubscription(0)
	if err := UpdateSubscriptionServer(0, 0, func(server *ServerRaw) {
		server.Latency = -1
		server.LastProbeTime = 101
		server.FastLatency = 88
		server.LastFastProbeTime = 102
		server.IPType = "idc"
		server.IPScore = &score
		server.IPGeoCountry = "日本"
		server.IPGeoCity = "东京"
	}); err != nil {
		t.Fatalf("write runtime results: %v", err)
	}
	stale.Name = "updated"
	if err := SetSubscription(0, stale); err != nil {
		t.Fatalf("set stale subscription: %v", err)
	}

	got := GetSubscription(0)
	if got.Servers[0].Latency != -1 || got.Servers[0].LastProbeTime != 101 ||
		got.Servers[0].FastLatency != 88 || got.Servers[0].LastFastProbeTime != 102 ||
		got.Servers[0].IPType != "idc" || got.Servers[0].IPScore == nil || *got.Servers[0].IPScore != score ||
		got.Servers[0].IPGeoCountry != "日本" || got.Servers[0].IPGeoCity != "东京" {
		t.Fatalf("runtime results were overwritten: %+v", got.Servers[0])
	}
}
