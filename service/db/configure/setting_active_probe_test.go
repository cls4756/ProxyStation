package configure

import "testing"

func TestNewSettingEnablesActiveNodeProbeByDefault(t *testing.T) {
	setting := NewSetting()
	if !setting.GroupActiveNodeProbeOn() {
		t.Fatal("新配置应默认启用活动节点真连巡检")
	}
	if setting.GroupActiveNodeProbeIntervalSec != 180 {
		t.Fatalf("默认活动节点巡检间隔应为 180 秒，got %d", setting.GroupActiveNodeProbeIntervalSec)
	}
}

func TestGroupActiveNodeProbeOnRespectsExplicitDisable(t *testing.T) {
	disabled := false
	setting := &Setting{GroupActiveNodeProbeEnabled: &disabled}
	if setting.GroupActiveNodeProbeOn() {
		t.Fatal("显式关闭后不应启动活动节点巡检")
	}
}
