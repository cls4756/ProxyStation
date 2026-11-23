package scheduler

import (
	"path/filepath"
	"testing"

	"github.com/ProxyStation/proxystation/db"
	"github.com/ProxyStation/proxystation/db/configure"
)

func TestNodeCleanupRuleKeyIgnoresGroupOrder(t *testing.T) {
	first := configure.NodeCleanupRule{
		GroupIDs:       []string{"group-b", "group-a"},
		Mode:           "real",
		IntervalHour:   12,
		RecycleGroupID: "recycle",
	}
	second := first
	second.GroupIDs = []string{"group-a", "group-b"}
	if nodeCleanupRuleKey(first) != nodeCleanupRuleKey(second) {
		t.Fatal("同一规则仅调整来源分组顺序时，调度标识不应改变")
	}
	second.IntervalHour = 24
	if nodeCleanupRuleKey(first) == nodeCleanupRuleKey(second) {
		t.Fatal("规则周期改变后，调度标识应改变")
	}
}

func TestNodeCleanupRuleKeyIncludesTransitThreshold(testContext *testing.T) {
	rule := configure.NodeCleanupRule{GroupIDs: []string{"source"}, Mode: "transit", IntervalHour: 24}
	defaultKey := nodeCleanupRuleKey(rule)
	threshold := 85
	rule.TransitScoreThreshold = &threshold
	if nodeCleanupRuleKey(rule) != defaultKey {
		testContext.Fatal("implicit and explicit default thresholds must have the same schedule key")
	}
	threshold = 90
	if nodeCleanupRuleKey(rule) == defaultKey {
		testContext.Fatal("changing the transit threshold must invalidate the schedule key")
	}
}

func TestNodeCleanupSkipsSubscriptionGroupsFromOldRules(testContext *testing.T) {
	if err := db.Init(filepath.Join(testContext.TempDir(), "cleanup.db")); err != nil {
		testContext.Fatal(err)
	}
	testContext.Cleanup(func() { _ = db.Close() })
	if err := configure.SetServers([]configure.ServerRaw{{Name: "invalid", Host: "127.0.0.1", Port: 0}}); err != nil {
		testContext.Fatal(err)
	}
	ref := configure.ServerRef{Type: "server", Index: 0}
	if err := configure.AppendGroup(&configure.Group{
		ID: "subscription-group", FromSub: true, Servers: []configure.ServerRef{ref},
	}); err != nil {
		testContext.Fatal(err)
	}
	for _, mode := range []string{"fast", "real", "realstrict", "transit"} {
		cleanupInvalidNodes(configure.NodeCleanupRule{GroupIDs: []string{"subscription-group"}, Mode: mode})
		group := configure.GetGroup(0)
		if len(group.Servers) != 1 || group.Servers[0] != ref {
			testContext.Fatalf("mode %s cleaned a subscription group: %+v", mode, group)
		}
	}
	if server := configure.GetServer(0); server.LastFastProbeTime != 0 || server.LastProbeTime != 0 {
		testContext.Fatalf("subscription group should not even be probed: %+v", server)
	}
}
