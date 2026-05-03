package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ProxyStation/proxystation/core/coreObj"
)

// optimizeXrayRules performs lightweight optimization:
// 1) remove exact duplicates
// 2) sort by estimated hit-frequency/cost (cheap/high-hit first)
func optimizeXrayRules(rules []coreObj.RoutingRule) []coreObj.RoutingRule {
	type wrapped struct {
		r     coreObj.RoutingRule
		score int
		idx   int
	}

	seen := make(map[string]struct{}, len(rules))
	uniq := make([]wrapped, 0, len(rules))
	for i, r := range rules {
		key := mustJSON(r)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		uniq = append(uniq, wrapped{
			r:     r,
			score: scoreXrayRule(r),
			idx:   i,
		})
	}

	sort.SliceStable(uniq, func(i, j int) bool {
		if uniq[i].score != uniq[j].score {
			return uniq[i].score > uniq[j].score
		}
		return uniq[i].idx < uniq[j].idx
	})

	out := make([]coreObj.RoutingRule, 0, len(uniq))
	for _, w := range uniq {
		out = append(out, w.r)
	}
	logRuleOptimization("xray", len(rules), len(out))
	return out
}

func scoreXrayRule(r coreObj.RoutingRule) int {
	score := 0
	if r.IP != nil && containsPrivateCIDR(r.IP) {
		score += 200
	}
	if hasGeoTag(r.Domain, "geosite:cn") || hasGeoTag(r.IP, "geoip:cn") {
		score += 120
	}
	if len(r.Domain) > 0 {
		score += 50
	}
	if len(r.IP) > 0 {
		score += 40
	}
	if r.Port != "" {
		score += 20
	}
	if strings.Contains(strings.ToLower(strings.Join(r.Domain, ",")), "regexp:") {
		score -= 40
	}
	if len(r.Domain)+len(r.IP) > 8 {
		score -= 20
	}
	return score
}

func optimizeSingboxRules(rules []SingboxRouteRule) []SingboxRouteRule {
	type wrapped struct {
		r     SingboxRouteRule
		score int
		idx   int
	}

	seen := make(map[string]struct{}, len(rules))
	uniq := make([]wrapped, 0, len(rules))
	for i, r := range rules {
		key := mustJSON(r)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		uniq = append(uniq, wrapped{
			r:     r,
			score: scoreSingboxRule(r),
			idx:   i,
		})
	}

	sort.SliceStable(uniq, func(i, j int) bool {
		if uniq[i].score != uniq[j].score {
			return uniq[i].score > uniq[j].score
		}
		return uniq[i].idx < uniq[j].idx
	})

	out := make([]SingboxRouteRule, 0, len(uniq))
	for _, w := range uniq {
		out = append(out, w.r)
	}
	logRuleOptimization("singbox", len(rules), len(out))
	return out
}

func scoreSingboxRule(r SingboxRouteRule) int {
	score := 0
	if r.Action == "sniff" || r.Action == "hijack-dns" {
		score += 300
	}
	if r.IPIsPrivate {
		score += 200
	}
	if hasTag(r.RuleSet, "geosite-cn") || hasTag(r.RuleSet, "geoip-cn") {
		score += 120
	}
	if len(r.DomainSuffix) > 0 {
		score += 70
	}
	if len(r.DomainKeyword) > 0 {
		score += 50
	}
	if len(r.DomainRegex) > 0 {
		score -= 40
	}
	if len(r.RuleSet) > 0 {
		score += 40
	}
	if len(r.IPCIDR) > 0 {
		score += 30
	}
	if len(r.DomainSuffix)+len(r.DomainKeyword)+len(r.DomainRegex)+len(r.IPCIDR)+len(r.RuleSet) > 8 {
		score -= 20
	}
	return score
}

func containsPrivateCIDR(ips []string) bool {
	privateCIDRs := map[string]struct{}{
		"10.0.0.0/8":      {},
		"172.16.0.0/12":   {},
		"192.168.0.0/16":  {},
		"127.0.0.0/8":     {},
		"169.254.0.0/16":  {},
		"224.0.0.0/4":     {},
		"240.0.0.0/4":     {},
		"::1/128":         {},
		"fc00::/7":        {},
		"fe80::/10":       {},
	}
	for _, ip := range ips {
		if _, ok := privateCIDRs[ip]; ok {
			return true
		}
	}
	return false
}

func hasGeoTag(vals []string, target string) bool {
	t := strings.ToLower(target)
	for _, v := range vals {
		if strings.ToLower(v) == t {
			return true
		}
	}
	return false
}

func hasTag(vals []string, target string) bool {
	for _, v := range vals {
		if v == target {
			return true
		}
	}
	return false
}

func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%#v", v)
	}
	return string(b)
}

func logRuleOptimization(kernel string, before, after int) {
	if LogCallback == nil {
		return
	}
	removed := before - after
	if removed < 0 {
		removed = 0
	}
	LogCallback(fmt.Sprintf("⚙️ %s route optimize: rules %d -> %d (dedup %d)", kernel, before, after, removed))
}

