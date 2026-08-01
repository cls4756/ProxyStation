package engine

import (
	"testing"

	"github.com/ProxyStation/proxystation/db/configure"
)

// 用户自定义规则引入的 rule-set 同样要拿到 download_detour。
// 早期实现里 detour 赋值早于自定义规则追加 rule-set，导致这些条目以直连下载，
// 在被墙环境下静默失败、规则不生效。
func TestUserRuleSetGetsDownloadDetour(t *testing.T) {
	setupTestDB(t)

	if err := configure.SetServers([]configure.ServerRaw{
		{Name: "a", Host: "a.example.com", Port: 443, Type: "vmess",
			Link: "vmess://" + vmessB64(`{"add":"a.example.com","port":"443","id":"11111111-1111-1111-1111-111111111111","net":"tcp"}`)},
	}); err != nil {
		t.Fatalf("set servers: %v", err)
	}
	bindOutboundToServer(t, "proxy", 0)

	if err := configure.SetRoutingRules([]configure.RoutingRule{
		{
			Enabled:      true,
			Action:       configure.RuleActionOutbound,
			OutboundName: "proxy",
			Domains:      []string{"geosite:category-ai-!cn"},
		},
	}); err != nil {
		t.Fatalf("set routing rules: %v", err)
	}

	cfg, err := BuildSingboxConfig(configure.GetSettingNotNil())
	if err != nil {
		t.Fatalf("BuildSingboxConfig: %v", err)
	}

	var found bool
	for _, rs := range cfg.Route.RuleSet {
		if rs.Type != "remote" {
			continue
		}
		if rs.DownloadDetour == "" {
			t.Errorf("rule-set %s 缺少 download_detour，将以直连下载", rs.Tag)
		}
		if rs.Tag == "geosite-category-ai-!cn" {
			found = true
			if rs.DownloadDetour != "proxy" {
				t.Errorf("自定义 rule-set 的 download_detour = %q，期望 proxy", rs.DownloadDetour)
			}
		}
	}
	if !found {
		t.Error("未找到用户自定义规则生成的 geosite-category-ai-!cn rule-set")
	}
}

// 没有代理出站时应回落到 direct，而不是留空
func TestRuleSetDetourFallsBackToDirect(t *testing.T) {
	setupTestDB(t)

	if err := configure.SetRoutingRules([]configure.RoutingRule{
		{
			Enabled: true,
			Action:  configure.RuleActionDirect,
			Domains: []string{"geosite:openai"},
		},
	}); err != nil {
		t.Fatalf("set routing rules: %v", err)
	}

	cfg, err := BuildSingboxConfig(configure.GetSettingNotNil())
	if err != nil {
		t.Fatalf("BuildSingboxConfig: %v", err)
	}

	for _, rs := range cfg.Route.RuleSet {
		if rs.Type == "remote" && rs.DownloadDetour != "direct" {
			t.Errorf("无代理出站时 rule-set %s 的 detour = %q，期望 direct", rs.Tag, rs.DownloadDetour)
		}
	}
}
