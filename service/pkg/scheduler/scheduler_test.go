package scheduler

import (
	"testing"

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
