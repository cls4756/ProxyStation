package observatory

import (
	"testing"

	"github.com/ProxyStation/proxystation/db/configure"
)

func setupGroupSwitchDB(t *testing.T, mode configure.SelectionMode, probeMode string, latencies []int) string {
	t.Helper()
	setupTestDB(t)

	servers := make([]configure.ServerRaw, 0, len(latencies))
	refs := make([]configure.ServerRef, 0, len(latencies))
	for i, lat := range latencies {
		servers = append(servers, configure.ServerRaw{
			Name: string(rune('a' + i)), Host: "h.example.com", Port: 443, Type: "vmess", Latency: lat,
		})
		refs = append(refs, configure.ServerRef{Type: "server", Index: i})
	}
	if err := configure.SetServers(servers); err != nil {
		t.Fatalf("SetServers: %v", err)
	}
	if err := configure.AppendGroup(&configure.Group{ID: "g1", Name: "G1", Servers: refs}); err != nil {
		t.Fatalf("AppendGroup: %v", err)
	}
	if err := configure.AddOutbound("hk"); err != nil {
		t.Fatalf("AddOutbound: %v", err)
	}
	threshold, cooldown := 100, 0
	if err := configure.SetOutbound("hk", &configure.Outbound{
		Name:      "hk",
		ProbeMode: probeMode,
		Target: configure.OutboundTarget{
			TargetType:    "group",
			GroupID:       "g1",
			Mode:          mode,
			ActiveNodeRef: &configure.NodeRef{Type: "server", Index: 0},
		},
		SwitchThresholdMs: &threshold,
		SwitchCooldownSec: &cooldown,
	}); err != nil {
		t.Fatalf("SetOutbound: %v", err)
	}
	return "g1"
}

func activeIndex(t *testing.T) int {
	t.Helper()
	o := configure.GetOutbound("hk")
	if o == nil || o.Target.ActiveNodeRef == nil {
		t.Fatal("出站或活跃节点丢失")
	}
	return o.Target.ActiveNodeRef.Index
}

// 界面上手动测速跑完后，最低延迟策略应按刚写回的延迟切到更快的节点。
func TestApplyGroupProbeResultsSwitchesOnLeastPing(t *testing.T) {
	groupID := setupGroupSwitchDB(t, configure.ModeLeastPing, "real", []int{900, 20})

	outcomes := ApplyGroupProbeResults(groupID, "real")
	if len(outcomes) != 1 || !outcomes[0].Switched {
		t.Fatalf("延迟差远超阈值应当切换，实际 %+v", outcomes)
	}
	if got := activeIndex(t); got != 1 {
		t.Errorf("应切到 index=1 的节点，实际 %d", got)
	}
}

// 本次测速方式与出站绑定的方式不一致时不能参与判定：fast 只做 TCP 握手，
// real 走完整代理链路，两者的数字不可比，混用会造成误切换。
func TestApplyGroupProbeResultsSkipsMismatchedMode(t *testing.T) {
	groupID := setupGroupSwitchDB(t, configure.ModeLeastPing, "real", []int{900, 20})

	outcomes := ApplyGroupProbeResults(groupID, "fast")
	if len(outcomes) != 1 {
		t.Fatalf("应返回该出站的判定结果，实际 %+v", outcomes)
	}
	if outcomes[0].Switched {
		t.Error("测速方式不一致时不应切换")
	}
	if got := activeIndex(t); got != 0 {
		t.Errorf("活跃节点不应变化，实际 %d", got)
	}
}

// 出站没配 ProbeMode 时回退到全局默认（默认 real），此时用 real 测速应当能参与判定，
// 否则"未配置"的出站会永远被跳过。
func TestApplyGroupProbeResultsTreatsEmptyModeAsDefault(t *testing.T) {
	groupID := setupGroupSwitchDB(t, configure.ModeLeastPing, "", []int{900, 20})

	outcomes := ApplyGroupProbeResults(groupID, "real")
	if len(outcomes) != 1 || !outcomes[0].Switched {
		t.Fatalf("空 ProbeMode 应回退到默认 real 并参与判定，实际 %+v", outcomes)
	}
}

// 稳定优先的语义：当前节点还连得上就不动，界面手动测速也不能绕过它。
func TestApplyGroupProbeResultsRespectsStableMode(t *testing.T) {
	groupID := setupGroupSwitchDB(t, configure.ModeStable, "real", []int{900, 20})

	outcomes := ApplyGroupProbeResults(groupID, "real")
	if len(outcomes) != 1 || outcomes[0].Switched {
		t.Fatalf("稳定优先不应切换，实际 %+v", outcomes)
	}
	if got := activeIndex(t); got != 0 {
		t.Errorf("活跃节点不应变化，实际 %d", got)
	}
}

// 手动指定策略下用户自己选的节点必须钉住，测速再快也不切。
func TestApplyGroupProbeResultsRespectsManualMode(t *testing.T) {
	groupID := setupGroupSwitchDB(t, configure.ModeManual, "real", []int{900, 20})

	outcomes := ApplyGroupProbeResults(groupID, "real")
	if len(outcomes) != 1 || outcomes[0].Switched {
		t.Fatalf("手动指定不应切换，实际 %+v", outcomes)
	}
}

// 延迟差没到出站配置的阈值就不该切，避免为了几毫秒频繁重启内核。
func TestApplyGroupProbeResultsRespectsThreshold(t *testing.T) {
	groupID := setupGroupSwitchDB(t, configure.ModeLeastPing, "real", []int{150, 100})

	outcomes := ApplyGroupProbeResults(groupID, "real")
	if len(outcomes) != 1 || outcomes[0].Switched {
		t.Fatalf("差值 50ms 小于阈值 100ms，不应切换，实际 %+v", outcomes)
	}
}

// 全部节点都不通时保持现状，不能把活跃节点清成空。
func TestApplyGroupProbeResultsKeepsCurrentWhenAllDown(t *testing.T) {
	groupID := setupGroupSwitchDB(t, configure.ModeLeastPing, "real", []int{-1, 0})

	outcomes := ApplyGroupProbeResults(groupID, "real")
	if len(outcomes) != 1 || outcomes[0].Switched {
		t.Fatalf("无可用节点不应切换，实际 %+v", outcomes)
	}
	if got := activeIndex(t); got != 0 {
		t.Errorf("活跃节点应保持不变，实际 %d", got)
	}
}

// 没有出站绑定这个分组时应当安静地什么都不做。
func TestApplyGroupProbeResultsIgnoresUnboundGroup(t *testing.T) {
	setupGroupSwitchDB(t, configure.ModeLeastPing, "real", []int{900, 20})

	if outcomes := ApplyGroupProbeResults("no-such-group", "real"); len(outcomes) != 0 {
		t.Errorf("未绑定的分组不应产生判定结果，实际 %+v", outcomes)
	}
	if got := activeIndex(t); got != 0 {
		t.Errorf("活跃节点不应变化，实际 %d", got)
	}
}

func TestNormalizeProbeModeFoldsToDefault(t *testing.T) {
	setupTestDB(t)

	if got := normalizeProbeMode("FAST"); got != "fast" {
		t.Errorf("大小写应归一，实际 %q", got)
	}
	// 空值与非法值都回退到设置默认（NewSetting 里是 real）
	for _, raw := range []string{"", "  ", "bogus"} {
		if got := normalizeProbeMode(raw); got != "real" {
			t.Errorf("raw=%q 应回退到 real，实际 %q", raw, got)
		}
	}
}

// 存量出站记录可能没有 Name 字段。补默认切换参数时若拿 o.Name 当存储 key，
// SetOutbound 会把空名字归一成 "proxy"，导致该出站的整份配置（含绑定分组、
// 探测间隔）覆盖掉默认出站——表现为"改一个出站，其他出站的参数跟着变"。
func TestEnsureOutboundSwitchSettingsDoesNotClobberProxy(t *testing.T) {
	setupTestDB(t)

	proxyThreshold, proxyCooldown := 100, 600
	if err := configure.SetOutbound("proxy", &configure.Outbound{
		Name:              "proxy",
		ProbeInterval:     "300s",
		SwitchThresholdMs: &proxyThreshold,
		SwitchCooldownSec: &proxyCooldown,
	}); err != nil {
		t.Fatalf("SetOutbound proxy: %v", err)
	}
	if err := configure.AddOutbound("hk"); err != nil {
		t.Fatalf("AddOutbound: %v", err)
	}
	// 关键：Name 为空，模拟老版本落库的数据
	if err := configure.SetOutbound("hk", &configure.Outbound{
		ProbeInterval: "45s",
		Target:        configure.OutboundTarget{TargetType: "group", GroupID: "g1"},
	}); err != nil {
		t.Fatalf("SetOutbound hk: %v", err)
	}

	ensureOutboundSwitchSettings("hk", configure.GetOutbound("hk"))

	p := configure.GetOutbound("proxy")
	if p.ProbeInterval != "300s" {
		t.Errorf("proxy 的探测间隔被污染成 %q", p.ProbeInterval)
	}
	if p.Target.GroupID != "" {
		t.Errorf("proxy 的绑定分组被污染成 %q", p.Target.GroupID)
	}
	if p.SwitchThresholdMs == nil || *p.SwitchThresholdMs != 100 {
		t.Errorf("proxy 的切换阈值被污染：%v", p.SwitchThresholdMs)
	}

	// 补的默认值必须落在 hk 自己身上
	hk := configure.GetOutbound("hk")
	if hk.SwitchThresholdMs == nil || hk.SwitchCooldownSec == nil {
		t.Fatal("hk 的切换参数没有补上默认值")
	}
	if hk.ProbeInterval != "45s" {
		t.Errorf("hk 自己的探测间隔应保持 45s，实际 %q", hk.ProbeInterval)
	}
	if hk.Name != "hk" {
		t.Errorf("落库时应顺带补上 Name，实际 %q", hk.Name)
	}
}

// 每个出站的探测间隔/阈值/冷却互相独立，改一个不能影响另一个。
func TestOutboundSwitchSettingsAreIndependent(t *testing.T) {
	setupTestDB(t)

	mk := func(name, interval string, threshold, cooldown int) {
		if err := configure.AddOutbound(name); err != nil {
			t.Fatalf("AddOutbound %s: %v", name, err)
		}
		th, cd := threshold, cooldown
		if err := configure.SetOutbound(name, &configure.Outbound{
			Name: name, ProbeInterval: interval, ProbeMode: "fast",
			Target:            configure.OutboundTarget{TargetType: "group", GroupID: "g1"},
			SwitchThresholdMs: &th, SwitchCooldownSec: &cd,
		}); err != nil {
			t.Fatalf("SetOutbound %s: %v", name, err)
		}
	}
	mk("ob1", "45s", 250, 900)
	mk("ob2", "120s", 50, 60)

	for _, name := range []string{"ob1", "ob2"} {
		ensureOutboundSwitchSettings(name, configure.GetOutbound(name))
	}

	o1, o2 := configure.GetOutbound("ob1"), configure.GetOutbound("ob2")
	if o1.ProbeInterval != "45s" || *o1.SwitchThresholdMs != 250 || *o1.SwitchCooldownSec != 900 {
		t.Errorf("ob1 参数被改动：%+v", o1)
	}
	if o2.ProbeInterval != "120s" || *o2.SwitchThresholdMs != 50 || *o2.SwitchCooldownSec != 60 {
		t.Errorf("ob2 参数被改动：%+v", o2)
	}
}
