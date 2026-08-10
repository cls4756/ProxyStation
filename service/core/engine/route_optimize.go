package engine

import (
	"encoding/json"
	"fmt"

	"github.com/ProxyStation/proxystation/core/coreObj"
)

// 路由规则是顺序敏感的：内核按数组顺序逐条匹配，命中即止。
// 因此这里只做去重，绝不重排序——调整顺序会让界面上的规则优先级失去意义。
// 去重保留首次出现的位置，这同样是顺序安全的：重复项在后，永远匹配不到。

func dedupXrayRules(rules []coreObj.RoutingRule) []coreObj.RoutingRule {
	seen := make(map[string]struct{}, len(rules))
	out := make([]coreObj.RoutingRule, 0, len(rules))
	for _, r := range rules {
		key := mustJSON(r)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, r)
	}
	logRuleDedup("xray", len(rules), len(out))
	return out
}

func dedupSingboxRules(rules []SingboxRouteRule) []SingboxRouteRule {
	seen := make(map[string]struct{}, len(rules))
	out := make([]SingboxRouteRule, 0, len(rules))
	for _, r := range rules {
		key := mustJSON(r)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, r)
	}
	logRuleDedup("singbox", len(rules), len(out))
	return out
}

func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%#v", v)
	}
	return string(b)
}

func logRuleDedup(kernel string, before, after int) {
	if LogCallback == nil {
		return
	}
	removed := before - after
	if removed <= 0 {
		return
	}
	LogCallback(fmt.Sprintf("⚙️ %s route: 去除 %d 条重复规则 (%d -> %d)，顺序保持不变", kernel, removed, before, after))
}
