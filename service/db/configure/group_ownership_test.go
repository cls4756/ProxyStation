package configure

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/ProxyStation/proxystation/db"
)

func appendLegacyTestGroup(testContext *testing.T, group *Group) {
	testContext.Helper()
	metadata := *group
	metadata.Servers = nil
	if err := AppendGroup(&metadata); err != nil {
		testContext.Fatal(err)
	}
	if err := db.WriteTx(func(tx *sql.Tx) error {
		return groupServersInsertTx(tx, group.ID, group.Servers, 0)
	}); err != nil {
		testContext.Fatal(err)
	}
	invalidateGroupRefIndex()
}

func TestCopyAcrossGroupsCreatesIndependentNodeRows(testContext *testing.T) {
	setupSubscriptionAtomicTestDB(testContext)
	if err := SetServers(identityGroupTestServers()); err != nil {
		testContext.Fatal(err)
	}
	original := ServerRef{Type: "server", Index: 0}
	if err := AppendGroup(&Group{ID: "source", Servers: []ServerRef{original}}); err != nil {
		testContext.Fatal(err)
	}
	copies := make([]NodeRef, 0, 2)
	for _, groupID := range []string{"first-target", "second-target"} {
		if err := AppendGroup(&Group{ID: groupID}); err != nil {
			testContext.Fatal(err)
		}
		created, total, err := CloneServersToGroup(groupID, []ServerRef{original})
		if err != nil || len(created) != 1 || total != 1 {
			testContext.Fatalf("cross-group copy failed: created=%v total=%d err=%v", created, total, err)
		}
		copies = append(copies, created[0])
	}
	if copies[0].Index == original.Index || copies[1].Index == original.Index || copies[0] == copies[1] {
		testContext.Fatal("copying identical content to different groups must create distinct rows")
	}
	before := GetServer(original.Index).Link
	if err := UpdateServers(map[int]func(*ServerRaw){
		copies[0].Index: func(server *ServerRaw) {
			server.Name = "changed-copy"
			server.Link = "vless://changed-credential@proxy.example:443"
		},
	}); err != nil {
		testContext.Fatal(err)
	}
	if GetServer(original.Index).Link != before || GetServer(copies[1].Index).Link != before {
		testContext.Fatal("editing one clone must not affect the source or another group's clone")
	}
}

func TestGroupWritesIsolateSharedAndSubscriptionNodes(testContext *testing.T) {
	setupSubscriptionAtomicTestDB(testContext)
	servers := identityGroupTestServers()
	if err := SetServers(servers); err != nil {
		testContext.Fatal(err)
	}
	if err := AppendSubscriptions([]*SubscriptionRaw{{ID: "subscription", Servers: []ServerRaw{servers[2]}}}); err != nil {
		testContext.Fatal(err)
	}
	if err := AppendGroup(&Group{ID: "source", Servers: []ServerRef{{Type: "server", Index: 0}}}); err != nil {
		testContext.Fatal(err)
	}
	if err := AppendGroup(&Group{ID: "target", Servers: []ServerRef{
		{Type: "server", Index: 0}, {Type: "sub_server", Index: 0},
	}}); err != nil {
		testContext.Fatal(err)
	}
	target := GetGroup(1).Servers
	if len(target) != 2 || target[0].Index == 0 || target[0].Type != "server" || target[1].Type != "server" {
		testContext.Fatal("manual groups must own independent rows rather than source or subscription references")
	}
	count := GetLenServers()
	if err := AddServersToGroup(1, []ServerRef{{Type: "server", Index: 1}, {Type: "sub_server", Index: 0}}); err != nil {
		testContext.Fatal(err)
	}
	if len(GetGroup(1).Servers) != 2 || GetLenServers() != count {
		testContext.Fatal("existing content must be skipped before creating any clone")
	}
	if err := AppendGroup(&Group{ID: "native", FromSub: true, SubID: "subscription",
		Servers: []ServerRef{{Type: "sub_server", Index: 0}}}); err != nil {
		testContext.Fatal(err)
	}
	if GetGroup(2).Servers[0].Type != "sub_server" {
		testContext.Fatal("the subscription's own group must retain its native subscription node")
	}
	if err := RemoveSubscriptions([]int{0}); err != nil {
		testContext.Fatal(err)
	}
	if GetServer(target[1].Index).Link != servers[2].Link || len(GetGroup(1).Servers) != 2 {
		testContext.Fatal("removing the source subscription must not delete independent manual copies")
	}
}

func TestMovingOwnedNodeTransfersWithoutCloning(testContext *testing.T) {
	setupSubscriptionAtomicTestDB(testContext)
	if err := SetServers(identityGroupTestServers()); err != nil {
		testContext.Fatal(err)
	}
	if err := AppendGroup(&Group{ID: "source", Servers: []ServerRef{{Type: "server", Index: 2}}}); err != nil {
		testContext.Fatal(err)
	}
	if err := AppendGroup(&Group{ID: "target"}); err != nil {
		testContext.Fatal(err)
	}
	source, target, moved, err := MoveServersToGroup("source", "target", []ServerRef{{Type: "server", Index: 2}})
	if err != nil || source != 0 || target != 1 || moved != 1 {
		testContext.Fatalf("move failed: source=%d target=%d moved=%d err=%v", source, target, moved, err)
	}
	if GetGroup(1).Servers[0].Index != 2 || GetLenServers() != 4 {
		testContext.Fatal("a true move transfers exclusive ownership without creating an unnecessary clone")
	}
}

func setupSharedGroupNodes(testContext *testing.T) {
	testContext.Helper()
	setupSubscriptionAtomicTestDB(testContext)
	servers := identityGroupTestServers()
	if err := SetServers(servers); err != nil {
		testContext.Fatal(err)
	}
	if err := AppendSubscriptions([]*SubscriptionRaw{{ID: "subscription", Servers: []ServerRaw{servers[3]}}}); err != nil {
		testContext.Fatal(err)
	}
	groups := []Group{
		{ID: "first", Name: "first"},
		{ID: "second", Name: "second"},
		{ID: "third", Name: "third"},
		{ID: "native", Name: "native", FromSub: true, SubID: "subscription"},
	}
	for _, group := range groups {
		if err := AppendGroup(&group); err != nil {
			testContext.Fatal(err)
		}
	}
	if err := db.WriteTx(func(tx *sql.Tx) error {
		members := map[string][]ServerRef{
			"first":  {{Type: "server", Index: 0}},
			"second": {{Type: "server", Index: 0}, {Type: "server", Index: 1}},
			"third":  {{Type: "server", Index: 0}, {Type: "server", Index: 2}, {Type: "sub_server", Index: 0}},
			"native": {{Type: "sub_server", Index: 0}},
		}
		for groupID, refs := range members {
			if err := groupServersInsertTx(tx, groupID, refs, 0); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		testContext.Fatal(err)
	}
	for name, target := range map[string]OutboundTarget{
		"active-owner": {TargetType: "group", GroupID: "second", ActiveNodeRef: &NodeRef{Type: "server", Index: 0}},
		"active-copy":  {TargetType: "group", GroupID: "third", ActiveNodeRef: &NodeRef{Type: "sub_server", Index: 0}},
		"direct-node":  {TargetType: "node", NodeRef: &NodeRef{Type: "server", Index: 0}},
	} {
		if err := SetOutbound(name, &Outbound{Target: target}); err != nil {
			testContext.Fatal(err)
		}
	}
}

func TestIsolateGroupNodesDeduplicatesBeforeCloning(testContext *testing.T) {
	setupSharedGroupNodes(testContext)
	for _, apply := range []bool{false, true} {
		if err := db.WriteTx(func(tx *sql.Tx) error {
			results, err := IsolateGroupNodes(tx, apply)
			if err != nil {
				return err
			}
			cloned, removed, retargeted := 0, 0, 0
			for _, result := range results {
				cloned += result.Cloned
				removed += result.Before - result.After
				retargeted += result.Retargeted
			}
			if cloned != 3 || removed != 1 || retargeted != 1 {
				testContext.Fatalf("unexpected isolation report: clones=%d duplicates=%d retargets=%d", cloned, removed, retargeted)
			}
			return nil
		}); err != nil {
			testContext.Fatal(err)
		}
		if !apply && (GetLenServers() != 4 || len(GetGroup(1).Servers) != 2) {
			testContext.Fatal("preview must not create clones or remove group members")
		}
	}
	if GetLenServers() != 7 || GetLenSubscriptionServers(0) != 1 {
		testContext.Fatal("migration must preserve original nodes and create only the required clones")
	}
	if GetGroup(1).Servers[0].Index != 0 || GetOutbound("direct-node").Target.NodeRef.Index != 0 {
		testContext.Fatal("the preferred active owner and direct source target must remain unchanged")
	}
	active := GetOutbound("active-copy").Target.ActiveNodeRef
	if active.Type != "server" || GetServer(active.Index).FrontProxy != identityGroupTestServers()[3].FrontProxy {
		testContext.Fatal("active references must follow their identical independent clones")
	}
	var shared, foreign int
	if err := db.ReadDB().QueryRow(`SELECT COUNT(*) FROM (
		SELECT type, ref_index, sub FROM group_servers GROUP BY type, ref_index, sub
		HAVING COUNT(DISTINCT group_id) > 1)`).Scan(&shared); err != nil {
		testContext.Fatal(err)
	}
	if err := db.ReadDB().QueryRow(`SELECT COUNT(*) FROM group_servers AS refs
		JOIN groups ON groups.id = refs.group_id WHERE groups.from_sub = 0 AND refs.type = 'sub_server'`).Scan(&foreign); err != nil {
		testContext.Fatal(err)
	}
	if shared != 0 || foreign != 0 || len(GetGroup(0).Servers) != 1 || len(GetGroup(1).Servers) != 1 || len(GetGroup(2).Servers) != 3 {
		testContext.Fatal("group contents must remain intact across groups while all sharing is removed")
	}
	if err := db.WriteTx(func(tx *sql.Tx) error {
		results, err := IsolateGroupNodes(tx, false)
		for _, result := range results {
			if result.Cloned != 0 || result.Before != result.After {
				testContext.Fatal("isolation must be idempotent")
			}
		}
		return err
	}); err != nil {
		testContext.Fatal(err)
	}
}

func TestIsolateGroupNodesRejectsMissingSharedSource(testContext *testing.T) {
	setupSharedGroupNodes(testContext)
	if err := db.WriteTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM nodes WHERE bucket = ? AND key = ? AND idx = 0`,
			nodeBucket, manualServersKey); err != nil {
			return err
		}
		return nil
	}); err != nil {
		testContext.Fatal(err)
	}
	count := GetLenServers()
	if err := db.WriteTx(func(tx *sql.Tx) error {
		_, err := IsolateGroupNodes(tx, true)
		return err
	}); err == nil {
		testContext.Fatal("missing source content must not be guessed or silently discarded")
	}
	if GetLenServers() != count || len(GetGroup(1).Servers) != 2 {
		testContext.Fatal("failed migration must not leave partial clones or group updates")
	}
}

func TestSharedNodeCloningUsesBoundedReferenceQueries(testContext *testing.T) {
	setupSubscriptionAtomicTestDB(testContext)
	servers := make([]ServerRaw, 10000)
	for index := range servers {
		servers[index] = ServerRaw{Name: fmt.Sprintf("node-%d", index), Host: "proxy.example", Port: 443,
			Type: "vless", Link: fmt.Sprintf("vless://credential-%d@proxy.example:443", index)}
	}
	if err := SetServers(servers); err != nil {
		testContext.Fatal(err)
	}
	refs := []ServerRef{{Type: "server", Index: 5}, {Type: "server", Index: 9999}}
	if err := AppendGroup(&Group{ID: "source", Servers: refs}); err != nil {
		testContext.Fatal(err)
	}
	if err := AppendGroup(&Group{ID: "target"}); err != nil {
		testContext.Fatal(err)
	}
	if err := AddServersToGroup(1, refs); err != nil {
		testContext.Fatal(err)
	}
	if GetLenServers() != 10002 || len(GetGroup(1).Servers) != 2 {
		testContext.Fatal("small reference batches must clone only the requested nodes in a large collection")
	}
}
