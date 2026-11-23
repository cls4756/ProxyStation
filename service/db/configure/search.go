package configure

import (
	"fmt"
	"strings"
)

// maxServerSearchScan 限制服务端搜索一次最多预取多少条匹配记录，避免极深的
// offset 把整张表都拉进内存。正常翻页远达不到这个量级。
const maxServerSearchScan = 20000

// ServerFilter 是节点筛选条件。字符串字段在构造时应规范化为小写（沿用搜索接口的约定）。
type ServerFilter struct {
	Query      string
	Type       string
	Geo        string
	IPType     string
	Probe      string
	FastProbe  string
	MaxLatency *int
	MinScore   *int
}

// ServerNode 是命中的节点及其引用。
type ServerNode struct {
	Ref    NodeRef
	Server ServerRaw
}

func (f ServerFilter) isEmpty() bool {
	return f.Query == "" && f.Type == "" && f.Geo == "" && f.IPType == "" &&
		f.Probe == "" && f.FastProbe == "" && f.MaxLatency == nil && f.MinScore == nil
}

// MatchServerFilter 与 SearchServers 的 SQL 筛选保持同一套语义，供分组等小范围筛选复用。
func MatchServerFilter(server ServerRaw, f ServerFilter) bool {
	if f.Query != "" && !strings.Contains(strings.ToLower(server.Name), f.Query) &&
		!strings.Contains(strings.ToLower(server.Host), f.Query) &&
		!strings.Contains(strings.ToLower(server.Type), f.Query) {
		return false
	}
	if f.Type != "" && strings.ToLower(strings.TrimSpace(server.Type)) != f.Type {
		return false
	}
	if f.Geo != "" {
		geo := strings.ToLower(strings.Join([]string{
			server.IPGeoCountry, server.IPGeoCountryCode, server.IPGeoRegion, server.IPGeoCity,
		}, " "))
		if !strings.Contains(geo, f.Geo) {
			return false
		}
	}
	switch f.IPType {
	case "":
	case "unchecked":
		if strings.TrimSpace(server.IPType) != "" {
			return false
		}
	default:
		if strings.ToLower(strings.TrimSpace(server.IPType)) != f.IPType {
			return false
		}
	}
	switch f.Probe {
	case "unchecked":
		if server.LastProbeTime > 0 {
			return false
		}
	case "reachable":
		if server.LastProbeTime <= 0 || server.Latency <= 0 {
			return false
		}
	case "failed":
		if server.LastProbeTime <= 0 || server.Latency > 0 {
			return false
		}
	}
	switch f.FastProbe {
	case "unchecked":
		if server.LastFastProbeTime > 0 {
			return false
		}
	case "reachable":
		if server.LastFastProbeTime <= 0 || server.FastLatency <= 0 {
			return false
		}
	case "failed":
		if server.LastFastProbeTime <= 0 || server.FastLatency > 0 {
			return false
		}
	}
	if f.MaxLatency != nil {
		if server.LastProbeTime <= 0 || server.Latency <= 0 || server.Latency > *f.MaxLatency {
			return false
		}
	}
	if f.MinScore != nil && (server.IPScore == nil || *server.IPScore < *f.MinScore) {
		return false
	}
	return true
}

// SearchServers 按列在数据库端筛选节点并按全局 offset/limit 返回一页，同时给出匹配总数。
// source 为 ""（全部）| "manual"（仅手动）| "subscription"（仅订阅）。
func SearchServers(f ServerFilter, source string, limit, offset int) ([]ServerNode, int, error) {
	limit, offset = normalizeSearchPage(limit, offset)
	prefetch := offset + limit
	if prefetch > maxServerSearchScan {
		return nil, 0, fmt.Errorf("搜索范围过大，请增加筛选条件")
	}

	nodes := make([]ServerNode, 0, prefetch)
	total := 0

	if source != "subscription" {
		hits, count, err := nodeSearch(nodeBucket, manualServersKey, f, prefetch, 0)
		if err != nil {
			return nil, 0, err
		}
		total += count
		for _, hit := range hits {
			nodes = append(nodes, ServerNode{Ref: NodeRef{Type: "server", Index: hit.Index}, Server: hit.Server})
		}
	}

	if source != "manual" {
		for subIndex := 0; subIndex < GetLenSubscriptions(); subIndex++ {
			doc, err := getSubscriptionDoc(subIndex)
			if err != nil {
				continue
			}
			hits, count, err := nodeSearch(nodeBucket, subscriptionServerKey(doc.ID), f, prefetch, 0)
			if err != nil {
				continue
			}
			total += count
			for _, hit := range hits {
				nodes = append(nodes, ServerNode{Ref: NodeRef{Type: "sub_server", Sub: subIndex, Index: hit.Index}, Server: hit.Server})
			}
		}
	}

	if offset >= len(nodes) {
		return []ServerNode{}, total, nil
	}
	end := offset + limit
	if end > len(nodes) {
		end = len(nodes)
	}
	return nodes[offset:end], total, nil
}

// SearchSubscriptionServers 在单个订阅内筛选并分页。
func SearchSubscriptionServers(subIndex int, f ServerFilter, limit, offset int) ([]ServerNode, int, error) {
	limit, offset = normalizeSearchPage(limit, offset)
	return searchSubscriptionNodes(subIndex, f, limit, offset)
}

func searchSubscriptionNodes(subIndex int, f ServerFilter, limit, offset int) ([]ServerNode, int, error) {
	doc, err := getSubscriptionDoc(subIndex)
	if err != nil {
		return nil, 0, err
	}
	hits, total, err := nodeSearch(nodeBucket, subscriptionServerKey(doc.ID), f, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	nodes := make([]ServerNode, 0, len(hits))
	for _, hit := range hits {
		nodes = append(nodes, ServerNode{Ref: NodeRef{Type: "sub_server", Sub: subIndex, Index: hit.Index}, Server: hit.Server})
	}
	return nodes, total, nil
}

func normalizeSearchPage(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 100
	} else if limit > 1000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
