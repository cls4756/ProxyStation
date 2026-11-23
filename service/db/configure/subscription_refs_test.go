package configure

import (
	"database/sql"
	"testing"

	"github.com/ProxyStation/proxystation/db"
)

func TestRemoveSubscriptionsRemapsPersistentReferences(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	for index, id := range []string{"first", "drop", "last"} {
		if err := AppendSubscriptions([]*SubscriptionRaw{{ID: id, GroupID: id + "-group", Servers: []ServerRaw{{
			Name: id, Type: "vless", Host: "proxy.example", Port: 443,
			Link: "vless://" + id + "@proxy.example:443",
		}}}}); err != nil {
			t.Fatal(err)
		}
		if err := AppendGroup(&Group{ID: id + "-group", Name: id, FromSub: true, SubID: id,
			Servers: []ServerRef{{Type: "sub_server", Sub: index, Index: 0}}}); err != nil {
			t.Fatal(err)
		}
	}
	appendLegacyTestGroup(t, &Group{ID: "mixed", Name: "mixed", Servers: []ServerRef{
		{Type: "sub_server", Sub: 0}, {Type: "sub_server", Sub: 1}, {Type: "sub_server", Sub: 2},
	}})
	if err := SetOutbound("keep", &Outbound{Name: "keep", Target: OutboundTarget{
		TargetType: "node", NodeRef: &NodeRef{Type: "sub_server", Sub: 2},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := SetOutbound("drop", &Outbound{Name: "drop", Target: OutboundTarget{
		TargetType: "node", NodeRef: &NodeRef{Type: "sub_server", Sub: 1},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := SetOutbound("active", &Outbound{Name: "active", Target: OutboundTarget{
		TargetType: "group", GroupID: "mixed", ActiveNodeRef: &NodeRef{Type: "sub_server", Sub: 2},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := SetOutbound("drop-group", &Outbound{Name: "drop-group", Target: OutboundTarget{
		TargetType: "group", GroupID: "drop-group", ActiveNodeRef: &NodeRef{Type: "sub_server", Sub: 1},
	}}); err != nil {
		t.Fatal(err)
	}
	GetGroupNamesForRefs([]ServerRef{{Type: "sub_server", Sub: 2}})
	if err := RemoveSubscriptions([]int{1, 1, 99}); err != nil {
		t.Fatal(err)
	}
	if GetLenSubscriptions() != 2 || GetSubscription(1).ID != "last" || nodeLen(nodeBucket, subscriptionServerKey("drop")) != 0 {
		t.Fatal("only the removed subscription and its node rows should be deleted")
	}
	if refs := GetGroup(3).Servers; len(refs) != 2 || refs[1].Sub != 1 {
		t.Fatalf("mixed group references did not follow subscription reindexing: %v", refs)
	}
	if refs := GetGroup(2).Servers; len(refs) != 1 || refs[0].Sub != 1 {
		t.Fatal("remaining subscription groups must follow the new index")
	}
	if GetOutbound("keep").Target.NodeRef.Sub != 1 || GetOutbound("active").Target.ActiveNodeRef.Sub != 1 {
		t.Fatal("node and group active outbound references must follow the new index")
	}
	if GetOutbound("drop").Target.TargetType != "none" || GetOutbound("drop-group").Target.TargetType != "none" {
		t.Fatal("outbounds pointing to deleted subscription targets must be disconnected")
	}
	if names := GetGroupNamesForRefs([]ServerRef{{Type: "sub_server", Sub: 1}}); len(names[ServerRef{Type: "sub_server", Sub: 1}]) != 2 {
		t.Fatal("reference reindexing must invalidate cached group membership")
	}
}

func TestRemoveSubscriptionsReferenceChangesRollBackOnFailure(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	if err := AppendSubscriptions([]*SubscriptionRaw{
		{ID: "first", Servers: []ServerRaw{{Name: "first"}}},
		{ID: "last", Servers: []ServerRaw{{Name: "last"}}},
	}); err != nil {
		t.Fatal(err)
	}
	appendLegacyTestGroup(t, &Group{ID: "group", Servers: []ServerRef{{Type: "sub_server", Sub: 1}}})
	if err := db.WriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO outbounds(name, target_type, node_ref) VALUES('invalid', 'node', 'invalid-json')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSubscriptions([]int{0}); err == nil {
		t.Fatal("invalid persistent references must fail without partial deletion")
	}
	if GetLenSubscriptions() != 2 || GetLenSubscriptionServers(0) != 1 || GetGroup(0).Servers[0].Sub != 1 {
		t.Fatal("failed deletion must roll back node, subscription and group-reference changes")
	}
}

func TestStaleSubscriptionUpdateCannotOverwriteShiftedSubscription(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	if err := AppendSubscriptions([]*SubscriptionRaw{
		{ID: "old", Name: "old", Servers: []ServerRaw{{Name: "old-node"}}},
		{ID: "keep", Name: "keep", Servers: []ServerRaw{{Name: "keep-node"}}},
	}); err != nil {
		t.Fatal(err)
	}
	stale := GetSubscription(0)
	if err := RemoveSubscriptions([]int{0}); err != nil {
		t.Fatal(err)
	}
	for _, update := range []func(int, *SubscriptionRaw) error{SetSubscription, RefreshSubscriptionData} {
		if err := update(0, stale); err == nil {
			t.Fatal("stale subscription responses must be rejected by stable ID")
		}
	}
	if current := GetSubscription(0); current.ID != "keep" || current.Servers[0].Name != "keep-node" {
		t.Fatal("a late response must not overwrite the subscription now occupying the old index")
	}
}

func TestSubscriptionRefreshCommitsNodesGroupsAndActiveRefsTogether(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	servers := []ServerRaw{
		{Name: "first", Type: "vless", Host: "proxy.example", Port: 443, Link: "vless://credential-a@proxy.example:443"},
		{Name: "last", Type: "vless", Host: "proxy.example", Port: 443, Link: "vless://credential-b@proxy.example:443"},
	}
	if err := AppendSubscriptions([]*SubscriptionRaw{{ID: "stable", Name: "old-name", URL: "https://subscription.example",
		Format: FormatAuto, GroupID: "group", Servers: servers}}); err != nil {
		t.Fatal(err)
	}
	if err := AppendGroup(&Group{ID: "group", FromSub: true, SubID: "stable", Servers: []ServerRef{
		{Type: "sub_server", Index: 0}, {Type: "sub_server", Index: 1},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := SetOutbound("active", &Outbound{Name: "active", Target: OutboundTarget{
		TargetType: "group", GroupID: "group", ActiveNodeRef: &NodeRef{Type: "sub_server", Index: 0},
	}}); err != nil {
		t.Fatal(err)
	}
	snapshot := GetSubscription(0)
	metadata := GetSubscriptionMeta(0)
	metadata.Name = "new-name"
	if err := UpdateSubscriptionMeta(0, metadata); err != nil {
		t.Fatal(err)
	}
	snapshot.Servers = []ServerRaw{servers[1]}
	snapshot.Format = FormatClash
	if err := RefreshSubscriptionData(0, snapshot); err != nil {
		t.Fatal(err)
	}
	current := GetSubscription(0)
	if current.Name != "new-name" || len(current.Servers) != 2 || current.Servers[1].Link != servers[0].Link {
		t.Fatal("refresh must preserve current metadata and retain active nodes missing from the new feed")
	}
	if GetOutbound("active").Target.ActiveNodeRef.Index != 1 || len(GetGroup(0).Servers) != 2 {
		t.Fatal("active node references and subscription group membership must be committed together")
	}
}
