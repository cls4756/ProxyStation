package engine

import (
	"testing"

	"github.com/ProxyStation/proxystation/db/configure"
)

// 内置入站的嗅探开关从全局拆成按入站后，两个入站必须互不影响。
func TestBuiltinInboundSniffIsPerInbound(t *testing.T) {
	setupTestDB(t)

	on, off := true, false
	setting := configure.GetSettingNotNil()
	setting.Socks5SniffEnabled = &on
	setting.HttpSniffEnabled = &off

	cfg, err := BuildSingboxConfig(setting)
	if err != nil {
		t.Fatalf("BuildSingboxConfig: %v", err)
	}

	socksSniff, httpSniff := false, false
	for _, r := range cfg.Route.Rules {
		if r.Action != "sniff" {
			continue
		}
		if hasStr(r.InboundTag, "socks-in") {
			socksSniff = true
		}
		if hasStr(r.InboundTag, "http-in") {
			httpSniff = true
		}
	}
	if !socksSniff {
		t.Error("socks-in 开启了嗅探，但未生成对应的 sniff 规则")
	}
	if httpSniff {
		t.Error("http-in 关闭了嗅探，却生成了 sniff 规则")
	}

	xcfg, err := BuildXrayConfig(setting)
	if err != nil {
		t.Fatalf("BuildXrayConfig: %v", err)
	}
	for _, ib := range xcfg.Inbounds {
		switch ib.Tag {
		case "socks-in":
			if !ib.Sniffing.Enabled {
				t.Error("xray socks-in 应启用嗅探")
			}
		case "http-in":
			if ib.Sniffing.Enabled {
				t.Error("xray http-in 应关闭嗅探")
			}
		}
	}
}

// 老配置只有全局 EnableSniff。升级后必须迁移成两个按入站的开关且保持原值，
// 否则用户开着的嗅探会被静默关掉。
func TestLegacyGlobalSniffMigrates(t *testing.T) {
	setupTestDB(t)

	s := configure.GetSettingNotNil()
	s.EnableSniff = true
	s.Socks5SniffEnabled = nil
	s.HttpSniffEnabled = nil
	if err := configure.SetSetting(s); err != nil {
		t.Fatalf("set setting: %v", err)
	}

	got := configure.GetSettingNotNil()
	if got.Socks5SniffEnabled == nil || !*got.Socks5SniffEnabled {
		t.Error("socks 嗅探未从旧的全局开关迁移过来")
	}
	if got.HttpSniffEnabled == nil || !*got.HttpSniffEnabled {
		t.Error("http 嗅探未从旧的全局开关迁移过来")
	}
	if !got.Socks5SniffOn() || !got.HttpSniffOn() {
		t.Error("访问器未反映迁移后的值")
	}
}

// 显式关闭必须能盖住旧的全局开关，不能被回落逻辑重新打开
func TestExplicitOffBeatsLegacyGlobal(t *testing.T) {
	setupTestDB(t)

	off := false
	s := configure.GetSettingNotNil()
	s.EnableSniff = true
	s.Socks5SniffEnabled = &off
	if s.Socks5SniffOn() {
		t.Error("显式关闭被旧的全局开关覆盖了")
	}
}

// 内置入站 tag 在三个 builder 之间必须一致，
// 否则界面上的 socks-in/http-in 快捷标签在某个内核下会匹配不到任何流量。
func TestBuiltinInboundTagsConsistent(t *testing.T) {
	setupTestDB(t)

	setting := configure.GetSettingNotNil()

	xcfg, err := BuildXrayConfig(setting)
	if err != nil {
		t.Fatalf("BuildXrayConfig: %v", err)
	}
	xrayTags := map[string]bool{}
	for _, ib := range xcfg.Inbounds {
		xrayTags[ib.Tag] = true
	}

	scfg, err := BuildSingboxConfig(setting)
	if err != nil {
		t.Fatalf("BuildSingboxConfig: %v", err)
	}
	sbTags := map[string]bool{}
	for _, ib := range scfg.Inbounds {
		sbTags[ib.Tag] = true
	}

	for _, tag := range []string{"socks-in", "http-in"} {
		if !xrayTags[tag] {
			t.Errorf("xray 缺少内置入站 tag %q", tag)
		}
		if !sbTags[tag] {
			t.Errorf("sing-box 缺少内置入站 tag %q", tag)
		}
	}
}
