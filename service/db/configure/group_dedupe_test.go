package configure

import (
	"database/sql"
	"testing"

	"github.com/ProxyStation/proxystation/db"
)

func TestDeduplicateGroupsPreservesActiveNodesAndStorage(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	servers := identityGroupTestServers()
	servers[2] = servers[1]
	servers[2].Name = "third alias"
	if err := SetServers(servers); err != nil {
		t.Fatal(err)
	}
	if err := AppendGroup(&Group{ID: "group", Name: "G"}); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteTx(func(tx *sql.Tx) error {
		return groupServersInsertTx(tx, "group", []ServerRef{
			{Type: "server", Index: 0}, {Type: "server", Index: 1}, {Type: "server", Index: 2},
		}, 0)
	}); err != nil {
		t.Fatal(err)
	}
	for index, name := range []string{"outbound-a", "outbound-b"} {
		if err := SetOutbound(name, &Outbound{Name: name, Target: OutboundTarget{
			TargetType: "group", GroupID: "group", ActiveNodeRef: &NodeRef{Type: "server", Index: index + 1},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, apply := range []bool{false, true} {
		if err := db.WriteTx(func(tx *sql.Tx) error {
			results, err := DeduplicateGroups(tx, apply)
			if err != nil {
				return err
			}
			if len(results) != 1 || results[0].Before != 3 || results[0].After != 1 || results[0].Retargeted != 1 {
				t.Fatalf("unexpected cleanup report: %+v", results)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if !apply && (len(GetGroup(0).Servers) != 3 || GetOutbound("outbound-b").Target.ActiveNodeRef.Index != 2) {
			t.Fatal("dry run must leave group references and active targets unchanged")
		}
	}
	group := GetGroup(0)
	if len(group.Servers) != 1 || group.Servers[0].Index != 1 {
		t.Fatal("active reference must be preferred over an earlier inactive alias")
	}
	if GetOutbound("outbound-a").Target.ActiveNodeRef.Index != 1 || GetOutbound("outbound-b").Target.ActiveNodeRef.Index != 1 {
		t.Fatal("equivalent active targets must stay inside the deduplicated group")
	}
	if GetLenServers() != 4 {
		t.Fatal("maintenance must not delete or reindex node records")
	}
	if err := db.WriteTx(func(tx *sql.Tx) error {
		results, err := DeduplicateGroups(tx, false)
		if err == nil && results[0].Before != results[0].After {
			t.Fatal("cleanup must be idempotent")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReplacingGroupPreservesEquivalentActiveTargets(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	if err := SetServers(identityGroupTestServers()); err != nil {
		t.Fatal(err)
	}
	if err := AppendGroup(&Group{ID: "group"}); err != nil {
		t.Fatal(err)
	}
	if err := SetOutbound("outbound", &Outbound{Name: "outbound", Target: OutboundTarget{
		TargetType: "group", GroupID: "group", ActiveNodeRef: &NodeRef{Type: "server", Index: 1},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := SetGroup(0, &Group{ID: "group", Servers: []ServerRef{
		{Type: "server", Index: 0}, {Type: "server", Index: 1}, {Type: "server", Index: 2},
	}}); err != nil {
		t.Fatal(err)
	}
	group := GetGroup(0)
	if len(group.Servers) != 2 || group.Servers[0].Index != 1 || GetOutbound("outbound").Target.ActiveNodeRef.Index != 1 {
		t.Fatal("group rebuild must retain the active alias and distinct credentials")
	}
}

func TestRepairAndDeduplicateGroupsUsesStableSubscriptionBinding(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	if err := AppendSubscriptions([]*SubscriptionRaw{{ID: "stable", Servers: []ServerRaw{
		{Name: "first", Type: "vless", Host: "proxy.example", Port: 443, Link: "vless://credential-a@proxy.example:443#first"},
		{Name: "alias", Type: "vless", Host: "proxy.example", Port: 443, Link: "vless://credential-a@proxy.example:443#alias"},
	}}}); err != nil {
		t.Fatal(err)
	}
	appendLegacyTestGroup(t, &Group{ID: "group", Name: "G", FromSub: true, SubID: "stable", Servers: []ServerRef{
		{Type: "sub_server", Sub: 1, Index: 0}, {Type: "sub_server", Sub: 1, Index: 1},
	}})
	if err := SetOutbound("outbound", &Outbound{Name: "outbound", Target: OutboundTarget{
		TargetType: "group", GroupID: "group", ActiveNodeRef: &NodeRef{Type: "sub_server", Sub: 1, Index: 1},
	}}); err != nil {
		t.Fatal(err)
	}
	for _, apply := range []bool{false, true} {
		if err := db.WriteTx(func(tx *sql.Tx) error {
			if _, err := DeduplicateGroups(tx, false); err == nil {
				t.Fatal("stale subscription references must not be compared against unrelated nodes")
			}
			results, err := RepairAndDeduplicateGroups(tx, apply)
			if err == nil && (results[0].Repaired != 2 || results[0].Before != 2 ||
				results[0].After != 1 || results[0].Retargeted != 1) {
				t.Fatalf("unexpected repair report: %+v", results)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if !apply && (GetGroup(0).Servers[0].Sub != 1 || GetOutbound("outbound").Target.ActiveNodeRef.Sub != 1) {
			t.Fatal("repair preview must leave database references unchanged")
		}
	}
	ref := GetGroup(0).Servers[0]
	if ref.Sub != 0 || ref.Index != 1 || GetOutbound("outbound").Target.ActiveNodeRef.Sub != 0 {
		t.Fatal("repair must preserve the active alias while restoring the stable subscription")
	}
	if GetLenSubscriptionServers(0) != 2 {
		t.Fatal("reference repair must not remove subscription nodes")
	}
}
