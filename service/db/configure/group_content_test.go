package configure

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/ProxyStation/proxystation/db"
)

func identityGroupTestServers() []ServerRaw {
	score := 83
	return []ServerRaw{
		{Name: "original", Type: "vless", Host: "proxy.example", Port: 443, Link: "vless://credential-a@proxy.example:443#original"},
		{Name: "renamed", Type: "vless", Host: "proxy.example", Port: 443, Link: "vless://credential-a@proxy.example:443#renamed"},
		{Name: "other credential", Type: "vless", Host: "proxy.example", Port: 443, Link: "vless://credential-b@proxy.example:443#other",
			Source: "subscription", Latency: 25, LastProbeTime: 100, IPScore: &score, IPGeoCountry: "日本"},
		{Name: "front proxy", Type: "vless", Host: "proxy.example", Port: 443, Link: "vless://credential-a@proxy.example:443#front",
			FrontProxy: "trans-proxy"},
	}
}

func TestCloneServersToGroupUsesContentAndPreservesMetadata(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	if err := SetServers(identityGroupTestServers()); err != nil {
		t.Fatal(err)
	}
	if err := AppendGroup(&Group{ID: "target", Servers: []ServerRef{{Type: "server", Index: 0}}}); err != nil {
		t.Fatal(err)
	}
	refs := []ServerRef{{Type: "server", Index: 1}, {Type: "server", Index: 2}, {Type: "server", Index: 3}, {Type: "server", Index: 2}}
	created, total, err := CloneServersToGroup("target", refs)
	if err != nil || len(created) != 2 || total != 3 || GetLenServers() != 6 {
		t.Fatalf("created=%v total=%d nodes=%d err=%v", created, total, GetLenServers(), err)
	}
	first := GetServer(created[0].Index)
	if first.Source != "manual" || first.Latency != 25 || first.LastProbeTime != 100 ||
		first.IPScore == nil || *first.IPScore != 83 || first.IPGeoCountry != "日本" {
		t.Fatal("clones must preserve completed node metadata")
	}
	created, total, err = CloneServersToGroup("target", refs)
	if err != nil || len(created) != 0 || total != 3 || GetLenServers() != 6 {
		t.Fatalf("repeat must be idempotent: created=%v total=%d nodes=%d err=%v", created, total, GetLenServers(), err)
	}
}

func TestCloneServersToGroupIsAtomicAndConcurrentSafe(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	if err := SetServers(identityGroupTestServers()); err != nil {
		t.Fatal(err)
	}
	if err := AppendGroup(&Group{ID: "target"}); err != nil {
		t.Fatal(err)
	}
	refs := []ServerRef{{Type: "server", Index: 0}}
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if _, _, err := CloneServersToGroup("target", refs); err != nil {
				t.Errorf("clone: %v", err)
			}
		}()
	}
	workers.Wait()
	if count := len(GetGroup(0).Servers); count != 1 || GetLenServers() != 5 {
		t.Fatalf("concurrent clones created duplicates: group=%d nodes=%d", count, GetLenServers())
	}
	if _, _, err := CloneServersToGroup("target", []ServerRef{
		{Type: "server", Index: 2}, {Type: "server", Index: 9999},
	}); err == nil {
		t.Fatal("missing source references must fail")
	}
	if len(GetGroup(0).Servers) != 1 || GetLenServers() != 5 {
		t.Fatal("failed clone must not leave orphan clones or partial group writes")
	}
}

func TestAddServersToGroupUsesGroupLocalContentIdentity(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	if err := SetServers(identityGroupTestServers()); err != nil {
		t.Fatal(err)
	}
	if err := AppendGroup(&Group{ID: "target", Servers: []ServerRef{{Type: "server", Index: 0}}}); err != nil {
		t.Fatal(err)
	}
	if err := AddServersToGroup(0, []ServerRef{{Type: "server", Index: 1}, {Type: "server", Index: 2}}); err != nil {
		t.Fatal(err)
	}
	if len(GetGroup(0).Servers) != 2 {
		t.Fatal("renamed aliases must be skipped within the target group")
	}
	if err := AddServersToGroup(0, []ServerRef{{Type: "server", Index: 1}, {Type: "server", Index: 3}}); err != nil {
		t.Fatal(err)
	}
	if len(GetGroup(0).Servers) != 3 || GetLenServers() != 4 {
		t.Fatal("reference insertion must deduplicate content without creating nodes")
	}
}

func TestMoveServersToGroupSkipsDuplicatesWithoutRemovingSource(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	if err := SetServers(identityGroupTestServers()); err != nil {
		t.Fatal(err)
	}
	if err := AppendGroup(&Group{ID: "source", Servers: []ServerRef{{Type: "server", Index: 1}, {Type: "server", Index: 2}}}); err != nil {
		t.Fatal(err)
	}
	if err := AppendGroup(&Group{ID: "target", Servers: []ServerRef{{Type: "server", Index: 0}}}); err != nil {
		t.Fatal(err)
	}
	source, target, moved, err := MoveServersToGroup("source", "target",
		[]ServerRef{{Type: "server", Index: 1}, {Type: "server", Index: 2}})
	if err != nil || source != 1 || target != 2 || moved != 1 {
		t.Fatalf("source=%d target=%d moved=%d err=%v", source, target, moved, err)
	}
	if !reflect.DeepEqual(GetGroup(0).Servers, []ServerRef{{Type: "server", Index: 1}}) {
		t.Fatal("skipped duplicate must remain in the source group")
	}
	if GetLenServers() != 4 {
		t.Fatal("moving references must not modify node storage")
	}
	if _, _, _, err := MoveServersToGroup("source", "target", []ServerRef{{Type: "server", Index: 3}}); err == nil {
		t.Fatal("cannot move a reference outside the source group")
	}
	if len(GetGroup(0).Servers) != 1 || len(GetGroup(1).Servers) != 2 {
		t.Fatal("failed move must not partially change either group")
	}
}

func TestSubscriptionGroupRebuildDeduplicatesContent(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	if err := AppendSubscriptions([]*SubscriptionRaw{{ID: "sub", Servers: identityGroupTestServers()}}); err != nil {
		t.Fatal(err)
	}
	group := &Group{ID: "subscription-group", FromSub: true, SubID: "sub", Servers: []ServerRef{
		{Type: "sub_server", Index: 0}, {Type: "sub_server", Index: 1}, {Type: "sub_server", Index: 2},
	}}
	if err := AppendGroup(group); err != nil {
		t.Fatal(err)
	}
	if len(GetGroup(0).Servers) != 2 || GetLenSubscriptionServers(0) != 4 {
		t.Fatal("deduplication must affect group references, not subscription node records")
	}
	if err := SetGroup(0, group); err != nil {
		t.Fatal(err)
	}
	if len(GetGroup(0).Servers) != 2 {
		t.Fatal("subscription rebuild must not reintroduce differently named duplicates")
	}
}

func TestNodeRefContentQueriesStayBoundedInLargeCollections(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	servers := make([]ServerRaw, 10000)
	for index := range servers {
		servers[index] = ServerRaw{Name: fmt.Sprintf("node-%d", index), Host: "proxy.example", Port: 443,
			Type: "vless", Link: fmt.Sprintf("vless://credential-%d@proxy.example:443", index)}
	}
	if err := SetServers(servers); err != nil {
		t.Fatal(err)
	}
	refs := []ServerRef{{Type: "server", Index: 9}, {Type: "server", Index: 9999}}
	if err := db.WriteTx(func(tx *sql.Tx) error {
		configs, err := nodeConfigsByServerRefsTx(tx, append(refs, refs[0]))
		if err != nil {
			return err
		}
		if len(configs) != 2 || configs[refs[0]].Name != "" {
			t.Fatal("content queries must return only requested connection fields")
		}
		query, args := nodeRefsQuery(refs, false)
		if strings.Contains(query, "ip_score") || strings.Contains(query, "latency") {
			t.Fatal("content queries must not read unrelated node metadata")
		}
		rows, err := tx.Query("EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		searchedNodes := false
		for rows.Next() {
			var planID, parentID, unused int
			var detail string
			if err := rows.Scan(&planID, &parentID, &unused, &detail); err != nil {
				return err
			}
			if strings.Contains(detail, "SCAN nodes") || strings.Contains(detail, "SCAN subscriptions") {
				t.Fatalf("reference lookup must not scan full persistent collections: %s", detail)
			}
			searchedNodes = searchedNodes || strings.Contains(detail, "SEARCH nodes")
		}
		if !searchedNodes {
			t.Fatal("node references must use indexed node lookups")
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
}
