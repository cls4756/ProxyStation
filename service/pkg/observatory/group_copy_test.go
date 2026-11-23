package observatory

import (
	"testing"

	"github.com/ProxyStation/proxystation/db/configure"
)

func TestAutomaticGroupCopyUsesCompleteContentAndIndependentClones(t *testing.T) {
	setupTestDB(t)
	if err := configure.SetServers([]configure.ServerRaw{
		{Name: "first", Host: "proxy.example", Port: 443, Type: "vless", Link: "vless://credential-a@proxy.example:443#first"},
		{Name: "alias", Host: "proxy.example", Port: 443, Type: "vless", Link: "vless://credential-a@proxy.example:443#alias"},
		{Name: "different credential", Host: "proxy.example", Port: 443, Type: "vless", Link: "vless://credential-b@proxy.example:443#other"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := configure.AppendGroup(&configure.Group{ID: "target-a", Name: "target-a"}); err != nil {
		t.Fatal(err)
	}
	if err := configure.AppendGroup(&configure.Group{ID: "target-b", Name: "target-b"}); err != nil {
		t.Fatal(err)
	}
	previous := nodeMetadataEnricher
	enriched := make([][]configure.NodeRef, 0)
	nodeMetadataEnricher = func(refs []configure.NodeRef) { enriched = append(enriched, refs) }
	t.Cleanup(func() { nodeMetadataEnricher = previous })
	picked := []probeResult{
		{ref: configure.NodeRef{Type: "server", Index: 0}},
		{ref: configure.NodeRef{Type: "server", Index: 1}},
		{ref: configure.NodeRef{Type: "server", Index: 2}},
	}
	copyNodesToGroup("source", "target-a", picked)
	copyNodesToGroup("source", "target-a", picked)
	if len(configure.GetGroup(0).Servers) != 2 || configure.GetLenServers() != 5 || len(enriched) != 1 || len(enriched[0]) != 2 {
		t.Fatal("automatic copy must skip aliases and only enrich newly created nodes")
	}
	copyNodesToGroup("source", "target-b", picked)
	if len(configure.GetGroup(1).Servers) != 2 || configure.GetLenServers() != 7 {
		t.Fatal("each target group must receive independent local copies")
	}
	if configure.GetGroup(0).Servers[0] == configure.GetGroup(1).Servers[0] {
		t.Fatal("automatic copying must not reuse global manual-node references across groups")
	}
}
