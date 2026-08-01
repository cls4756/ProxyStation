package engine

import (
	"testing"

	"github.com/ProxyStation/proxystation/db/configure"
)

func refNames(refs []RuleSetRef) map[string]RuleSetRef {
	m := make(map[string]RuleSetRef, len(refs))
	for _, r := range refs {
		m[r.Name] = r
	}
	return m
}

// 自定义规则引用的 rule-set 要能被收集出来，供"更新规则文件"预下载
func TestCollectUserRuleSetRefs(t *testing.T) {
	setupTestDB(t)

	if err := configure.SetRoutingRules([]configure.RoutingRule{
		{
			Enabled:      true,
			Action:       configure.RuleActionOutbound,
			OutboundName: "proxy",
			Domains:      []string{"geosite:category-ai-!cn", "domain:example.com"},
			IPs:          []string{"geoip:jp", "geoip:private", "1.2.3.4/32"},
		},
		{
			Enabled: false,
			Action:  configure.RuleActionDirect,
			Domains: []string{"geosite:should-be-ignored"},
		},
	}); err != nil {
		t.Fatalf("set routing rules: %v", err)
	}

	got := refNames(collectUserRuleSetRefs())

	if ref, ok := got["geosite-category-ai-!cn"]; !ok {
		t.Error("未收集到 geosite-category-ai-!cn")
	} else if ref.URL != geositeRuleSetURL("category-ai-!cn") {
		t.Errorf("URL 不匹配: %s", ref.URL)
	}
	if _, ok := got["geoip-jp"]; !ok {
		t.Error("未收集到 geoip-jp")
	}
	if _, ok := got["geosite-should-be-ignored"]; ok {
		t.Error("未启用的规则不应被收集")
	}
	// private 用 ip_is_private 表达，没有对应文件
	if _, ok := got["geoip-private"]; ok {
		t.Error("geoip:private 不应产生 rule-set")
	}
	// 非 geo 前缀的条件不产生 rule-set
	if len(got) != 2 {
		t.Errorf("应只收集到 2 个 rule-set，实际 %d: %v", len(got), got)
	}
}

// 内置列表与用户引用合并后不应重复，且内置项要标记 Builtin
func TestAllRuleSetRefsDedup(t *testing.T) {
	setupTestDB(t)

	if err := configure.SetRoutingRules([]configure.RoutingRule{
		{
			Enabled: true,
			Action:  configure.RuleActionDirect,
			// geosite-cn 已在内置列表里，不应重复出现
			Domains: []string{"geosite:cn", "geosite:category-ai-!cn"},
		},
	}); err != nil {
		t.Fatalf("set routing rules: %v", err)
	}

	refs := allRuleSetRefs()
	seen := map[string]int{}
	for _, r := range refs {
		seen[r.Name]++
	}
	for name, n := range seen {
		if n > 1 {
			t.Errorf("rule-set %s 重复出现 %d 次", name, n)
		}
	}

	byName := refNames(refs)
	if ref, ok := byName["geosite-cn"]; !ok {
		t.Error("缺少内置 geosite-cn")
	} else if !ref.Builtin {
		t.Error("geosite-cn 应标记为内置")
	}
	if ref, ok := byName["geosite-category-ai-!cn"]; !ok {
		t.Error("缺少用户引用的 geosite-category-ai-!cn")
	} else if ref.Builtin {
		t.Error("用户引用的 rule-set 不应标记为内置")
	}
}

// 名称会拼进本地文件路径，必须挡住目录穿越
func TestSafeGeoNameRejectsTraversal(t *testing.T) {
	bad := []string{
		"../../etc/passwd",
		"..",
		"a/b",
		`a\b`,
		"",
		"foo bar",
	}
	for _, name := range bad {
		if safeGeoName(name) {
			t.Errorf("safeGeoName(%q) 应为 false", name)
		}
		if _, ok := resolveGeoName(name); ok {
			t.Errorf("resolveGeoName(%q) 应被拒绝", name)
		}
	}

	good := []string{"cn", "category-ai-!cn", "geolocation-!cn", "private_x", "v2ray.dat"}
	for _, name := range good {
		if !safeGeoName(name) {
			t.Errorf("safeGeoName(%q) 应为 true", name)
		}
	}
}
