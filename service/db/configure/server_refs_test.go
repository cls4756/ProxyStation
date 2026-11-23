package configure

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/ProxyStation/proxystation/db"
)

func TestManualRemovalPreservesIndependentClonesAndRetargetsIndexes(testContext *testing.T) {
	setupSubscriptionAtomicTestDB(testContext)
	if err := SetServers(identityGroupTestServers()); err != nil {
		testContext.Fatal(err)
	}
	if err := AppendGroup(&Group{ID: "source", Servers: []ServerRef{{Type: "server", Index: 0}}}); err != nil {
		testContext.Fatal(err)
	}
	if err := AppendGroup(&Group{ID: "target", Name: "target"}); err != nil {
		testContext.Fatal(err)
	}
	created, _, err := CloneServersToGroup("target", []ServerRef{{Type: "server", Index: 0}})
	if err != nil || len(created) != 1 {
		testContext.Fatalf("clone failed: %v", err)
	}
	cloned := created[0]
	before := GetServer(cloned.Index).Link
	for name, target := range map[string]OutboundTarget{
		"source": {TargetType: "node", NodeRef: &NodeRef{Type: "server", Index: 0}},
		"copy":   {TargetType: "node", NodeRef: &cloned},
		"group":  {TargetType: "group", GroupID: "target", ActiveNodeRef: &cloned},
	} {
		if err := SetOutbound(name, &Outbound{Target: target}); err != nil {
			testContext.Fatal(err)
		}
	}
	GetGroupNamesForRefs([]ServerRef{{Type: "server", Index: cloned.Index}})
	if err := RemoveServers([]int{0, 0, 99}); err != nil {
		testContext.Fatal(err)
	}
	current := GetGroup(1).Servers[0]
	if len(GetGroup(0).Servers) != 0 || current.Index != cloned.Index-1 || GetServer(current.Index).Link != before {
		testContext.Fatal("deleting the original must not delete or misidentify another group's clone")
	}
	if GetOutbound("source").Target.TargetType != "none" ||
		GetOutbound("copy").Target.NodeRef.Index != current.Index ||
		GetOutbound("group").Target.ActiveNodeRef.Index != current.Index {
		testContext.Fatal("persistent outbound references must follow the same atomic index remap")
	}
	if names := GetGroupNamesForRefs([]ServerRef{current})[current]; len(names) != 1 || names[0] != "target" {
		testContext.Fatal("deletion must invalidate cached group membership")
	}
}

func TestManualRemovalReferenceFailureRollsBack(testContext *testing.T) {
	setupSubscriptionAtomicTestDB(testContext)
	if err := SetServers(identityGroupTestServers()); err != nil {
		testContext.Fatal(err)
	}
	if err := AppendGroup(&Group{ID: "source", Servers: []ServerRef{{Type: "server", Index: 1}}}); err != nil {
		testContext.Fatal(err)
	}
	if err := db.WriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO outbounds(name, target_type, node_ref) VALUES('invalid', 'node', 'invalid-json')`)
		return err
	}); err != nil {
		testContext.Fatal(err)
	}
	if err := RemoveServers([]int{0}); err == nil {
		testContext.Fatal("invalid persistent references must reject deletion without partial changes")
	}
	if GetLenServers() != 4 || GetGroup(0).Servers[0].Index != 1 {
		testContext.Fatal("node and group-reference changes must roll back together")
	}
}

func nativeProxyTestServer(port int) ServerRaw {
	return ServerRaw{
		Name: "native", Host: "127.0.0.1", Port: port, Type: "socks5",
		Link: fmt.Sprintf("socks5://127.0.0.1:%d#native", port), Source: "inbound:test", Latency: -1,
	}
}

func TestInboundSyncPreservesCopiesAndOnlyChangesNativeNodes(testContext *testing.T) {
	setupSubscriptionAtomicTestDB(testContext)
	originals := identityGroupTestServers()
	if err := SetServers(originals); err != nil {
		testContext.Fatal(err)
	}
	if err := AppendGroup(&Group{ID: "target"}); err != nil {
		testContext.Fatal(err)
	}
	generated := []ServerRaw{nativeProxyTestServer(12000)}
	if err := SyncInboundProxyServers(generated); err != nil {
		testContext.Fatal(err)
	}
	var nativeIndex int
	if err := db.ReadDB().QueryRow(`SELECT idx FROM nodes WHERE bucket = ? AND key = ? AND source = ?`,
		nodeBucket, manualServersKey, generated[0].Source).Scan(&nativeIndex); err != nil {
		testContext.Fatal(err)
	}
	created, _, err := CloneServersToGroup("target", []ServerRef{{Type: "server", Index: nativeIndex}})
	if err != nil || len(created) != 1 {
		testContext.Fatalf("native clone failed: %v", err)
	}
	cloned := created[0]
	if err := SetOutbound("copy", &Outbound{Target: OutboundTarget{TargetType: "node", NodeRef: &cloned}}); err != nil {
		testContext.Fatal(err)
	}
	if err := SyncInboundProxyServers([]ServerRaw{nativeProxyTestServer(12001)}); err != nil {
		testContext.Fatal(err)
	}
	if GetLenServers() != 6 || GetServer(nativeIndex).Port != 12001 ||
		GetServer(cloned.Index).Port != 12000 || GetServer(cloned.Index).Source != "manual" {
		testContext.Fatal("native updates must keep indexes stable and leave independent copies unchanged")
	}
	if err := SyncInboundProxyServers(nil); err != nil {
		testContext.Fatal(err)
	}
	current := GetGroup(0).Servers[0]
	if GetLenServers() != 5 || GetServer(current.Index).Port != 12000 ||
		GetOutbound("copy").Target.NodeRef.Index != current.Index {
		testContext.Fatal("retiring a native node must preserve copies and atomically reindex their references")
	}
	for index, original := range originals {
		if GetServer(index).Link != original.Link {
			testContext.Fatal("inbound synchronization must not rewrite unrelated manual nodes")
		}
	}
}

func TestInboundSyncDoesNotModifyHistoricalCopiesOwnedElsewhere(testContext *testing.T) {
	setupSubscriptionAtomicTestDB(testContext)
	original := nativeProxyTestServer(12000)
	if err := SetServers([]ServerRaw{original}); err != nil {
		testContext.Fatal(err)
	}
	if err := AppendGroup(&Group{ID: "copied-group", Name: "copied-group",
		Servers: []ServerRef{{Type: "server", Index: 0}}}); err != nil {
		testContext.Fatal(err)
	}
	if err := SyncInboundProxyServers([]ServerRaw{nativeProxyTestServer(12001)}); err != nil {
		testContext.Fatal(err)
	}
	if GetServer(0).Link != original.Link || GetServer(0).Source != "manual" ||
		GetGroup(0).Servers[0].Index != 0 || GetLenServers() != 2 {
		testContext.Fatal("historical copies in another group must become manual and remain independent")
	}
}

func TestInboundSyncQueriesStayLocalInLargeNodeCollections(testContext *testing.T) {
	setupSubscriptionAtomicTestDB(testContext)
	servers := make([]ServerRaw, 10000)
	for index := range servers {
		servers[index] = ServerRaw{Name: fmt.Sprintf("node-%d", index), Host: "proxy.example", Port: 443,
			Type: "vless", Link: fmt.Sprintf("vless://credential-%d@proxy.example:443", index), Latency: index}
	}
	if err := SetServers(servers); err != nil {
		testContext.Fatal(err)
	}
	generated := []ServerRaw{nativeProxyTestServer(12000)}
	for attempt := 0; attempt < 2; attempt++ {
		if err := SyncInboundProxyServers(generated); err != nil {
			testContext.Fatal(err)
		}
	}
	if GetLenServers() != 10001 || GetServer(9999).Latency != 9999 {
		testContext.Fatal("repeated native synchronization must not recreate or rewrite the manual collection")
	}
	rows, err := db.ReadDB().Query(`EXPLAIN QUERY PLAN SELECT idx, `+nodeSelectColumns()+`
		FROM nodes WHERE bucket = ? AND key = ? AND source GLOB 'inbound:*' ORDER BY idx`,
		nodeBucket, manualServersKey)
	if err != nil {
		testContext.Fatal(err)
	}
	defer rows.Close()
	indexed := false
	for rows.Next() {
		var queryID, parent, auxiliary int
		var detail string
		if err := rows.Scan(&queryID, &parent, &auxiliary, &detail); err != nil {
			testContext.Fatal(err)
		}
		indexed = indexed || strings.Contains(detail, "idx_nodes_source")
	}
	if err := rows.Err(); err != nil {
		testContext.Fatal(err)
	}
	if !indexed {
		testContext.Fatal("native source lookup must use its bounded source index, not scan all node rows")
	}
}
