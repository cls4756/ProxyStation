package observatory

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ProxyStation/proxystation/db"
	"github.com/ProxyStation/proxystation/db/configure"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	if err := db.Init(filepath.Join(t.TempDir(), "test.db")); err != nil {
		t.Fatalf("db init: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

func nodeRef(index int) configure.NodeRef {
	return configure.NodeRef{Type: "server", Index: index}
}

// 稳定优先的唯一卖点：当前节点还连得上就不动，哪怕它比最优节点慢一个数量级。
// 这条语义必须在阈值判断之前���效，否则和最低延迟策略没有区别。
func TestShouldSwitchStableKeepsCurrentWhenAlive(t *testing.T) {
	setupTestDB(t)

	current := nodeRef(1)
	available := []probeResult{
		{ref: nodeRef(1), latency: 900},
		{ref: nodeRef(2), latency: 20},
	}
	best := probeResult{ref: nodeRef(2), latency: 20}

	switched, reason := shouldSwitch("out-stable", configure.ModeStable, current, available, best)
	if switched {
		t.Fatalf("稳定优先不应切换，实际切换了：%s", reason)
	}
}

// 稳定优先只在当前节点掉线时才放行，否则一个失效节点会被永久钉住。
func TestShouldSwitchStableSwitchesWhenCurrentDown(t *testing.T) {
	setupTestDB(t)

	current := nodeRef(1)
	// 当前节点不在可用列表里 = 已掉线
	available := []probeResult{
		{ref: nodeRef(2), latency: 20},
	}
	best := probeResult{ref: nodeRef(2), latency: 20}

	switched, _ := shouldSwitch("out-stable-down", configure.ModeStable, current, available, best)
	if !switched {
		t.Fatal("当前节点已掉线，稳定优先也必须切换")
	}
}

// 同样的输入在最低延迟策略下必须切换，用来证明上面的不切换来自策略本身而非阈值。
func TestShouldSwitchLeastPingSwitchesOnSameInput(t *testing.T) {
	setupTestDB(t)

	current := nodeRef(1)
	available := []probeResult{
		{ref: nodeRef(1), latency: 900},
		{ref: nodeRef(2), latency: 20},
	}
	best := probeResult{ref: nodeRef(2), latency: 20}

	switched, reason := shouldSwitch("out-leastping", configure.ModeLeastPing, current, available, best)
	if !switched {
		t.Fatalf("延迟差 880ms 远超阈值，应当切换：%s", reason)
	}
}

func TestShouldSwitchUsesOutboundThreshold(t *testing.T) {
	setupTestDB(t)

	current := nodeRef(1)
	available := []probeResult{
		{ref: nodeRef(1), latency: 150},
		{ref: nodeRef(2), latency: 100},
	}
	best := probeResult{ref: nodeRef(2), latency: 100}

	switched, _ := shouldSwitchWithSettings("out-threshold", configure.ModeLeastPing, current, available, best, 100, 0)
	if switched {
		t.Fatal("延迟差小于出站阈值时不应切换")
	}
}

func TestShouldSwitchManualNeverChangesNode(t *testing.T) {
	setupTestDB(t)

	current := nodeRef(1)
	available := []probeResult{
		{ref: nodeRef(1), latency: 900},
		{ref: nodeRef(2), latency: 20},
	}
	best := probeResult{ref: nodeRef(2), latency: 20}

	switched, _ := shouldSwitchWithSettings("out-manual", configure.ModeManual, current, available, best, 0, 0)
	if switched {
		t.Fatal("手动策略不应自动切换节点")
	}
}

func TestResolveTaskIntervalPrefersOutboundValue(t *testing.T) {
	setupTestDB(t)

	if got := resolveTaskInterval("45s"); got != 45*time.Second {
		t.Fatalf("出站自己的间隔应当生效，want 45s got %v", got)
	}
}

// 非法或空值回退到设置默认值，而不是变成 0 间隔把 ticker 打爆。
func TestResolveTaskIntervalFallsBackOnInvalid(t *testing.T) {
	setupTestDB(t)

	for _, raw := range []string{"", "abc", "0s", "-5s"} {
		if got := resolveTaskInterval(raw); got < minProbeInterval {
			t.Fatalf("raw=%q 回退值不应低于下限，got %v", raw, got)
		}
	}
}

func TestResolveTaskIntervalClampsTooSmall(t *testing.T) {
	setupTestDB(t)

	if got := resolveTaskInterval("1s"); got != minProbeInterval {
		t.Fatalf("过小的间隔应被钳到 %v，got %v", minProbeInterval, got)
	}
}
