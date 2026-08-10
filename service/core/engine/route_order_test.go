package engine

import (
	"testing"

	"github.com/ProxyStation/proxystation/db/configure"
)

// 路由规则必须严格按用户在界面上排列的顺序写入内核配置。
// 早期实现会按启发式评分重排序（带 geosite/geoip 条件的规则被加分排到前面），
// 使得界面上的上移/下移按钮和"优先级从上到下"的说明形同虚设：
// 一条仅按入站分流的规则会被带域名条件的规则抢先命中。
func TestSingboxUserRulesKeepOrder(t *testing.T) {
	setupTestDB(t)

	if err := configure.SetServers([]configure.ServerRaw{
		{Name: "a", Host: "a.example.com", Port: 443, Type: "vmess",
			Link: "vmess://" + vmessB64(`{"add":"a.example.com","port":"443","id":"11111111-1111-1111-1111-111111111111","net":"tcp"}`)},
	}); err != nil {
		t.Fatalf("set servers: %v", err)
	}
	bindOutboundToServer(t, "proxy", 0)

	// 第一条：纯入站规则（旧评分为 0，会被压到最后）
	// 第二条：带 geosite:cn（旧评分 +160，会被抬到最前）
	if err := configure.SetRoutingRules([]configure.RoutingRule{
		{
			Name:         "global-proxy",
			Enabled:      true,
			Action:       configure.RuleActionOutbound,
			OutboundName: "proxy",
			InboundTags:  []string{"my-in"},
		},
		{
			Name:    "cn-direct",
			Enabled: true,
			Action:  configure.RuleActionDirect,
			Domains: []string{"geosite:cn"},
		},
	}); err != nil {
		t.Fatalf("set routing rules: %v", err)
	}

	cfg, err := BuildSingboxConfig(configure.GetSettingNotNil())
	if err != nil {
		t.Fatalf("BuildSingboxConfig: %v", err)
	}

	inboundIdx, geositeIdx := -1, -1
	for i, r := range cfg.Route.Rules {
		if len(r.InboundTag) == 1 && r.InboundTag[0] == "my-in" {
			inboundIdx = i
		}
		if hasStr(r.RuleSet, "geosite-cn") && r.Outbound == "direct" && geositeIdx == -1 {
			geositeIdx = i
		}
	}
	if inboundIdx == -1 {
		t.Fatal("未找到入站规则 my-in")
	}
	if geositeIdx == -1 {
		t.Fatal("未找到 geosite-cn 规则")
	}
	if inboundIdx > geositeIdx {
		t.Errorf("规则被重排序：入站规则在索引 %d，geosite:cn 在 %d，期望入站规则在前", inboundIdx, geositeIdx)
	}
}

// 内置的 geosite-cn/geoip-cn 默认直连只是缺省策略，
// 用户显式规则必须能覆盖它，否则用户无法让国内域名走代理。
func TestSingboxUserRulesPrecedeDefaultGeoRules(t *testing.T) {
	setupTestDB(t)

	if err := configure.SetRoutingRules([]configure.RoutingRule{
		{
			Name:    "cn-via-proxy",
			Enabled: true,
			Action:  configure.RuleActionBlock,
			Domains: []string{"geosite:cn"},
		},
	}); err != nil {
		t.Fatalf("set routing rules: %v", err)
	}

	cfg, err := BuildSingboxConfig(configure.GetSettingNotNil())
	if err != nil {
		t.Fatalf("BuildSingboxConfig: %v", err)
	}

	userIdx, defaultIdx := -1, -1
	for i, r := range cfg.Route.Rules {
		if !hasStr(r.RuleSet, "geosite-cn") {
			continue
		}
		if r.Outbound == "block" && userIdx == -1 {
			userIdx = i
		}
		if r.Outbound == "direct" && defaultIdx == -1 {
			defaultIdx = i
		}
	}
	if userIdx == -1 {
		t.Fatal("未找到用户的 geosite:cn 规则")
	}
	if defaultIdx == -1 {
		t.Fatal("未找到默认的 geosite-cn 直连规则")
	}
	if userIdx > defaultIdx {
		t.Errorf("用户规则在索引 %d，默认直连在 %d，用户规则被默认策略遮蔽", userIdx, defaultIdx)
	}
}

// sniff/hijack-dns 是流量处理动作，私有地址直连是安全兜底，
// 三者都不该被用户的宽泛规则遮蔽，必须留在最前。
func TestSingboxHeadRulesStayFirst(t *testing.T) {
	setupTestDB(t)

	setting := configure.GetSettingNotNil()
	on := true
	setting.Socks5SniffEnabled = &on
	setting.HttpSniffEnabled = &on
	setting.EnableHijackDNS = true

	if err := configure.SetRoutingRules([]configure.RoutingRule{
		{
			Name:        "catch-all",
			Enabled:     true,
			Action:      configure.RuleActionBlock,
			InboundTags: []string{"socks-in"},
		},
	}); err != nil {
		t.Fatalf("set routing rules: %v", err)
	}

	cfg, err := BuildSingboxConfig(setting)
	if err != nil {
		t.Fatalf("BuildSingboxConfig: %v", err)
	}

	userIdx := -1
	for i, r := range cfg.Route.Rules {
		if r.Outbound == "block" {
			userIdx = i
			break
		}
	}
	if userIdx == -1 {
		t.Fatal("未找到用户规则")
	}

	for _, want := range []string{"sniff", "hijack-dns"} {
		idx := -1
		for i, r := range cfg.Route.Rules {
			if r.Action == want {
				idx = i
				break
			}
		}
		if idx == -1 {
			t.Errorf("未找到 %s 规则", want)
			continue
		}
		if idx > userIdx {
			t.Errorf("%s 规则在索引 %d，晚于用户规则 %d", want, idx, userIdx)
		}
	}

	privIdx := -1
	for i, r := range cfg.Route.Rules {
		if r.IPIsPrivate && r.Outbound == "direct" {
			privIdx = i
			break
		}
	}
	if privIdx == -1 {
		t.Fatal("未找到私有地址直连规则")
	}
	if privIdx > userIdx {
		t.Errorf("私有地址直连在索引 %d，晚于用户规则 %d，内网访问会被切断", privIdx, userIdx)
	}
}

// xray 的 proxy catch-all（port 0-65535）会匹配任意流量，
// 必须排在用户规则之后，否则所有用户规则都失效。
func TestXrayCatchAllIsLast(t *testing.T) {
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
			Name:        "global-proxy",
			Enabled:     true,
			Action:      configure.RuleActionDirect,
			InboundTags: []string{"my-in"},
		},
	}); err != nil {
		t.Fatalf("set routing rules: %v", err)
	}

	cfg, err := BuildXrayConfig(configure.GetSettingNotNil())
	if err != nil {
		t.Fatalf("BuildXrayConfig: %v", err)
	}

	catchAllIdx, userIdx := -1, -1
	for i, r := range cfg.Routing.Rules {
		if r.Port == "0-65535" && r.OutboundTag == "proxy" {
			catchAllIdx = i
		}
		if len(r.InboundTag) == 1 && r.InboundTag[0] == "my-in" {
			userIdx = i
		}
	}
	if catchAllIdx == -1 {
		t.Fatal("未找到 proxy catch-all 规则")
	}
	if userIdx == -1 {
		t.Fatal("未找到用户规则")
	}
	if catchAllIdx < userIdx {
		t.Errorf("catch-all 在索引 %d，早于用户规则 %d，用户规则将永远不被命中", catchAllIdx, userIdx)
	}
}

func hasStr(vals []string, target string) bool {
	for _, v := range vals {
		if v == target {
			return true
		}
	}
	return false
}
