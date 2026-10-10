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

func TestSaveLatencyKeepsFastAndRealResultsSeparate(t *testing.T) {
	setupTestDB(t)
	if err := configure.SetServers([]configure.ServerRaw{{
		Name: "node", Latency: 120, LastProbeTime: 10, FastLatency: 30, LastFastProbeTime: 20,
	}}); err != nil {
		t.Fatalf("SetServers: %v", err)
	}

	saveLatency(nodeRef(0), "fast", 45)
	server := configure.GetServer(0)
	if server == nil || server.FastLatency != 45 || server.LastFastProbeTime <= 20 {
		t.Fatalf("快速检测结果未保存：%+v", server)
	}
	if server.Latency != 120 || server.LastProbeTime != 10 {
		t.Fatalf("快速检测不应覆盖真连结果：%+v", server)
	}

	fastProbeTime := server.LastFastProbeTime
	saveLatency(nodeRef(0), "real", -1)
	server = configure.GetServer(0)
	if server == nil || server.Latency != -1 || server.LastProbeTime <= 10 {
		t.Fatalf("真连接结果未保存：%+v", server)
	}
	if server.FastLatency != 45 || server.LastFastProbeTime != fastProbeTime {
		t.Fatalf("真连接检测不应覆盖快速结果：%+v", server)
	}
	if got := readLatency(nodeRef(0), "fast"); got != 45 {
		t.Fatalf("快速模式读取错误：%d", got)
	}
	if got := readLatency(nodeRef(0), "real"); got != -1 {
		t.Fatalf("真连接模式读取错误：%d", got)
	}
}

func TestSaveLatencySeparatesSubscriptionProbeModes(t *testing.T) {
	setupTestDB(t)
	if err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{{
		ID: "sub", Servers: []configure.ServerRaw{{Name: "node", Latency: 90, LastProbeTime: 10}},
	}}); err != nil {
		t.Fatalf("AppendSubscriptions: %v", err)
	}

	ref := configure.NodeRef{Type: "sub_server", Sub: 0, Index: 0}
	saveLatency(ref, "fast", 25)
	sub := configure.GetSubscription(0)
	if sub == nil || sub.Servers[0].FastLatency != 25 || sub.Servers[0].LastFastProbeTime == 0 {
		t.Fatalf("订阅节点快速结果未保存：%+v", sub)
	}
	if sub.Servers[0].Latency != 90 || sub.Servers[0].LastProbeTime != 10 {
		t.Fatalf("订阅节点快速检测覆盖了真连结果：%+v", sub.Servers[0])
	}
}

func TestServerRawByRefDoesNotInheritSubscriptionProxyOutbound(t *testing.T) {
	setupTestDB(t)
	if err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{{
		ID: "sub", ProxyOutbound: "hk", Servers: []configure.ServerRaw{{
			Name: "node", Host: "node.example.com", Port: 443,
		}},
	}}); err != nil {
		t.Fatalf("AppendSubscriptions: %v", err)
	}

	server := serverRawByRef(configure.NodeRef{Type: "sub_server", Sub: 0, Index: 0})
	if server == nil || server.FrontProxy != "" {
		t.Fatalf("订阅拉取代理不应成为节点前置代理: %+v", server)
	}
}

func TestTransProxyFallbackHelpersAvoidSelfReferenceAndPersist(t *testing.T) {
	setupTestDB(t)
	if err := configure.SetServers([]configure.ServerRaw{
		{Name: "trans", Host: "trans.example.com", Port: 443},
		{Name: "target", Host: "target.example.com", Port: 443},
	}); err != nil {
		t.Fatalf("SetServers: %v", err)
	}
	if err := configure.SetOutbound("trans-proxy", &configure.Outbound{
		Name: "trans-proxy", Target: configure.OutboundTarget{
			TargetType: "node", NodeRef: &configure.NodeRef{Type: "server", Index: 0},
		},
	}); err != nil {
		t.Fatalf("SetOutbound: %v", err)
	}

	if !outboundUsesNode("trans-proxy", configure.NodeRef{Type: "server", Index: 0}) {
		t.Fatal("trans-proxy 应识别为目标节点自身")
	}
	if canUseTransProxy(configure.NodeRef{Type: "server", Index: 0}) {
		t.Fatal("不能把 trans-proxy 自身作为自己的前置代理")
	}
	if !canUseTransProxy(configure.NodeRef{Type: "server", Index: 1}) {
		t.Fatal("其他节点应允许使用 trans-proxy")
	}

	if !saveTransProxyFrontProxy(configure.NodeRef{Type: "server", Index: 1}) {
		t.Fatal("保存 trans-proxy 前置代理失败")
	}
	target := configure.GetServer(1)
	if target == nil || target.FrontProxy != "trans-proxy" {
		t.Fatalf("节点前置代理未保存: %+v", target)
	}
}

func TestFailoverFrontProxyMarksFailedNodeAndUsesGroupRule(t *testing.T) {
	setupTestDB(t)
	servers := []configure.ServerRaw{
		{Name: "front-down", Host: "front-down.example.com", Port: 443, Latency: 200},
		{Name: "front-good", Host: "front-good.example.com", Port: 443, Latency: 20},
		{Name: "target", Host: "target.example.com", Port: 443, FrontProxy: "front"},
	}
	if err := configure.SetServers(servers); err != nil {
		t.Fatalf("SetServers: %v", err)
	}
	if err := configure.AppendGroup(&configure.Group{ID: "front-group", Name: "Front", Servers: []configure.ServerRef{
		{Type: "server", Index: 0}, {Type: "server", Index: 1},
	}}); err != nil {
		t.Fatalf("AppendGroup: %v", err)
	}
	threshold, cooldown := 0, 0
	if err := configure.SetOutbound("front", &configure.Outbound{
		Name: "front", Target: configure.OutboundTarget{
			TargetType: "group", GroupID: "front-group", Mode: configure.ModeLeastPing,
			ActiveNodeRef: &configure.NodeRef{Type: "server", Index: 0},
		},
		ProbeMode: "real", SwitchThresholdMs: &threshold, SwitchCooldownSec: &cooldown,
	}); err != nil {
		t.Fatalf("SetOutbound front: %v", err)
	}
	failed := configure.NodeRef{Type: "server", Index: 0}
	if !markFrontProxyFailure("front", failed) {
		t.Fatal("前置出站故障应切换到分组内其他可用节点")
	}
	front := configure.GetOutbound("front")
	if front == nil || front.Target.ActiveNodeRef == nil || front.Target.ActiveNodeRef.Index != 1 {
		t.Fatalf("前置出站未切换：%+v", front)
	}
	server := configure.GetServer(0)
	if server == nil || server.Latency != -1 || server.LastProbeTime == 0 {
		t.Fatalf("前置故障节点真连接结果未写回：%+v", server)
	}
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

func TestCleanupProbeInvalidRules(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		fastLatency int
		realLatency int
		want        bool
	}{
		{name: "快速失败规则会清理", mode: "fast", fastLatency: -1, want: true},
		{name: "快速成功规则会保留", mode: "fast", fastLatency: 20, want: false},
		{name: "真连规则仅清理快速成功真连失败", mode: "real", fastLatency: 20, realLatency: -1, want: true},
		{name: "真连规则保留快速失败", mode: "real", fastLatency: -1, realLatency: -1, want: false},
		{name: "真连规则保留完整成功", mode: "real", fastLatency: 20, realLatency: 80, want: false},
		{name: "严格真连规则清理快速成功真连失败", mode: "realstrict", fastLatency: 20, realLatency: -1, want: true},
		{name: "严格真连规则清理快速失败真连失败", mode: "realstrict", fastLatency: -1, realLatency: -1, want: true},
		{name: "严格真连规则保留完整成功", mode: "realstrict", fastLatency: 20, realLatency: 80, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := cleanupProbeInvalid(test.mode, test.fastLatency, test.realLatency); got != test.want {
				t.Fatalf("cleanupProbeInvalid(%q, %d, %d) = %v, want %v", test.mode, test.fastLatency, test.realLatency, got, test.want)
			}
		})
	}
}

func TestResolveActiveNodeProbeIntervalClampsTooSmall(t *testing.T) {
	if got := resolveActiveNodeProbeInterval(1); got != minActiveNodeProbeInterval {
		t.Fatalf("过小的活动节点巡检间隔应被钳到 %v，got %v", minActiveNodeProbeInterval, got)
	}
	if got := resolveActiveNodeProbeInterval(180); got != 180*time.Second {
		t.Fatalf("活动节点巡检间隔应使用设置值，got %v", got)
	}
}

func TestProbeActiveOutboundOnlyExpandsCandidatesAfterFailure(t *testing.T) {
	setupTestDB(t)
	if err := configure.SetServers([]configure.ServerRaw{
		{Name: "direct-slow", Host: "a.example.com", Port: 443, Latency: 80},
		{Name: "current", Host: "b.example.com", Port: 443, Latency: 10},
		{Name: "fronted-fast", Host: "c.example.com", Port: 443, Latency: 5, FrontProxy: "front"},
		{Name: "direct-fast", Host: "d.example.com", Port: 443, Latency: 20},
	}); err != nil {
		t.Fatalf("SetServers: %v", err)
	}
	if err := configure.AppendGroup(&configure.Group{ID: "g1", Name: "G1", Servers: []configure.ServerRef{
		{Type: "server", Index: 0}, {Type: "server", Index: 1},
		{Type: "server", Index: 2}, {Type: "server", Index: 3},
	}}); err != nil {
		t.Fatalf("AppendGroup: %v", err)
	}
	if err := configure.SetOutbound("proxy", &configure.Outbound{Name: "proxy", Target: configure.OutboundTarget{
		TargetType: "group", GroupID: "g1", Mode: configure.ModeLeastPing,
		ActiveNodeRef: &configure.NodeRef{Type: "server", Index: 1},
	}}); err != nil {
		t.Fatalf("SetOutbound: %v", err)
	}

	var probed []int
	probeResults := map[int]int{1: -1, 3: -1, 0: 45, 2: 5}
	probe := func(ref configure.NodeRef) int {
		probed = append(probed, ref.Index)
		return probeResults[ref.Index]
	}
	activate := func(name, groupID string, failedRef, candidateRef configure.NodeRef) (bool, error) {
		if name != "proxy" || groupID != "g1" || failedRef.Index != 1 {
			t.Fatalf("故障切换上下文错误：%s %s %+v", name, groupID, failedRef)
		}
		if err := configure.SetOutboundActiveNode(name, &candidateRef); err != nil {
			return false, err
		}
		return true, nil
	}

	probeActiveOutbound("proxy", probe, activate)
	want := []int{1, 3, 0}
	if len(probed) != len(want) {
		t.Fatalf("探测顺序错误：got %v want %v", probed, want)
	}
	for i := range want {
		if probed[i] != want[i] {
			t.Fatalf("探测顺序错误：got %v want %v", probed, want)
		}
	}
	outbound := configure.GetOutbound("proxy")
	if outbound == nil || outbound.Target.ActiveNodeRef == nil || outbound.Target.ActiveNodeRef.Index != 0 {
		t.Fatalf("应切换到首个真连成功候选：%+v", outbound)
	}
}

func TestProbeActiveOutboundHealthyOnlyTestsCurrentNode(t *testing.T) {
	setupTestDB(t)
	if err := configure.SetServers([]configure.ServerRaw{
		{Name: "candidate", Host: "a.example.com", Port: 443},
		{Name: "current", Host: "b.example.com", Port: 443},
	}); err != nil {
		t.Fatalf("SetServers: %v", err)
	}
	if err := configure.AppendGroup(&configure.Group{ID: "g1", Servers: []configure.ServerRef{
		{Type: "server", Index: 0}, {Type: "server", Index: 1},
	}}); err != nil {
		t.Fatalf("AppendGroup: %v", err)
	}
	if err := configure.SetOutbound("proxy", &configure.Outbound{Name: "proxy", Target: configure.OutboundTarget{
		TargetType: "group", GroupID: "g1", Mode: configure.ModeStable,
		ActiveNodeRef: &configure.NodeRef{Type: "server", Index: 1},
	}}); err != nil {
		t.Fatalf("SetOutbound: %v", err)
	}

	var probed []int
	probeActiveOutbound("proxy", func(ref configure.NodeRef) int {
		probed = append(probed, ref.Index)
		return 30
	}, func(string, string, configure.NodeRef, configure.NodeRef) (bool, error) {
		t.Fatal("活动节点正常时不应进入故障切换")
		return false, nil
	})
	if len(probed) != 1 || probed[0] != 1 {
		t.Fatalf("只应探测当前实际活动节点，got %v", probed)
	}
}

func TestCollectActiveNodeProbeTargetsDirectFirst(t *testing.T) {
	setupTestDB(t)
	if err := configure.SetServers([]configure.ServerRaw{
		{Name: "fronted", Host: "a.example.com", Port: 443, FrontProxy: "front"},
		{Name: "direct", Host: "b.example.com", Port: 443},
	}); err != nil {
		t.Fatalf("SetServers: %v", err)
	}
	if err := configure.AppendGroup(&configure.Group{ID: "front-group", Servers: []configure.ServerRef{{Type: "server", Index: 0}}}); err != nil {
		t.Fatalf("AppendGroup front: %v", err)
	}
	if err := configure.AppendGroup(&configure.Group{ID: "direct-group", Servers: []configure.ServerRef{{Type: "server", Index: 1}}}); err != nil {
		t.Fatalf("AppendGroup direct: %v", err)
	}
	if err := configure.SetOutbound("proxy", &configure.Outbound{Name: "proxy", Target: configure.OutboundTarget{
		TargetType: "group", GroupID: "front-group", ActiveNodeRef: &configure.NodeRef{Type: "server", Index: 0},
	}}); err != nil {
		t.Fatalf("SetOutbound proxy: %v", err)
	}
	if err := configure.AddOutbound("direct-out"); err != nil {
		t.Fatalf("AddOutbound: %v", err)
	}
	if err := configure.SetOutbound("direct-out", &configure.Outbound{Name: "direct-out", Target: configure.OutboundTarget{
		TargetType: "group", GroupID: "direct-group", ActiveNodeRef: &configure.NodeRef{Type: "server", Index: 1},
	}}); err != nil {
		t.Fatalf("SetOutbound direct-out: %v", err)
	}

	targets := collectActiveNodeProbeTargets()
	if len(targets) != 2 || targets[0].outboundName != "direct-out" || targets[1].outboundName != "proxy" {
		t.Fatalf("活动节点应按直连优先排序：%+v", targets)
	}
}

func TestActivateFailoverNodeRejectsStaleCurrent(t *testing.T) {
	setupTestDB(t)
	if err := configure.SetServers([]configure.ServerRaw{{Name: "old"}, {Name: "new"}, {Name: "other"}}); err != nil {
		t.Fatalf("SetServers: %v", err)
	}
	if err := configure.AppendGroup(&configure.Group{ID: "g1", Servers: []configure.ServerRef{
		{Type: "server", Index: 0}, {Type: "server", Index: 1}, {Type: "server", Index: 2},
	}}); err != nil {
		t.Fatalf("AppendGroup: %v", err)
	}
	if err := configure.SetOutbound("proxy", &configure.Outbound{Name: "proxy", Target: configure.OutboundTarget{
		TargetType: "group", GroupID: "g1", ActiveNodeRef: &configure.NodeRef{Type: "server", Index: 2},
	}}); err != nil {
		t.Fatalf("SetOutbound: %v", err)
	}

	changed, err := activateFailoverNode("proxy", "g1", nodeRef(0), nodeRef(1))
	if err != nil {
		t.Fatalf("activateFailoverNode: %v", err)
	}
	if changed {
		t.Fatal("过期任务不应覆盖已变化的活动节点")
	}
	outbound := configure.GetOutbound("proxy")
	if outbound == nil || outbound.Target.ActiveNodeRef == nil || outbound.Target.ActiveNodeRef.Index != 2 {
		t.Fatalf("活动节点被过期任务覆盖：%+v", outbound)
	}
}
