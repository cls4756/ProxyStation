package server

import (
	"fmt"
	"strings"
	"sync"

	"github.com/ProxyStation/proxystation/db/configure"
	"github.com/ProxyStation/proxystation/pkg/ipinfo"
)

var nodeMetadataIPTypeMu sync.Mutex

func enrichMissingNodeMetadataAsync(refs []configure.NodeRef) {
	refs = uniqueNodeRefs(refs)
	refs = filterNodeRefs(refs, needsNodeMetadata)
	if len(refs) == 0 {
		return
	}
	go enrichMissingNodeMetadata(refs)
}

func enrichMissingNodeMetadata(refs []configure.NodeRef) {
	nodeMetadataIPTypeMu.Lock()
	typeRefs := filterNodeRefs(refs, needsIPInfo)
	if len(typeRefs) > 0 {
		detectNodeIPInfo(typeRefs)
	}
	nodeMetadataIPTypeMu.Unlock()

	scoreRefs := filterNodeRefs(refs, needsIPScore)
	if len(scoreRefs) == 0 {
		return
	}
	if err := queueIPScoreTask(scoreRefs); err != nil {
		addLog(fmt.Sprintf("⚠️ 自动补全 IP 评分失败：%v", err))
	}
}

func filterNodeRefs(refs []configure.NodeRef, missing func(*configure.ServerRaw) bool) []configure.NodeRef {
	result := make([]configure.NodeRef, 0, len(refs))
	for _, ref := range refs {
		server := serverRawByNodeRef(ref)
		if server != nil && missing(server) {
			result = append(result, ref)
		}
	}
	return result
}

func needsIPInfo(server *configure.ServerRaw) bool {
	return strings.TrimSpace(server.IPType) == "" || nodeGeoMissing(server)
}

func needsIPScore(server *configure.ServerRaw) bool {
	return server.IPScore == nil
}

func needsNodeMetadata(server *configure.ServerRaw) bool {
	return needsIPInfo(server) || needsIPScore(server)
}

func nodeGeoMissing(server *configure.ServerRaw) bool {
	return strings.TrimSpace(server.IPGeoCountry) == "" &&
		strings.TrimSpace(server.IPGeoCountryCode) == "" &&
		strings.TrimSpace(server.IPGeoRegion) == "" &&
		strings.TrimSpace(server.IPGeoCity) == ""
}

func detectNodeIPInfo(refs []configure.NodeRef) {
	refs = filterNodeRefs(refs, needsIPInfo)
	if len(refs) == 0 {
		return
	}
	hosts := make([]string, 0, len(refs))
	for _, ref := range refs {
		if server := serverRawByNodeRef(ref); server != nil {
			hosts = append(hosts, server.Host)
		}
	}
	if len(hosts) == 0 {
		return
	}

	addLog(fmt.Sprintf("🌐 自动补全 %d 个节点的地区和 IP 类型...", len(refs)))
	lookups := ipinfo.Lookup(ipTypeHTTPClient(), hosts)
	failed := 0
	for _, ref := range refs {
		server := serverRawByNodeRef(ref)
		if server == nil {
			continue
		}
		result, ok := lookups[strings.TrimSpace(server.Host)]
		if !ok {
			result = ipinfo.Result{Kind: ipinfo.KindUnknown, Err: fmt.Errorf("节点地址为空")}
		}
		if result.Err != nil {
			failed++
		}
		if err := saveIPType(ref, result); err != nil && result.Err == nil {
			failed++
		}
	}
	addLog(fmt.Sprintf("✅ 地区和 IP 类型补全完成：成功 %d 个，失败 %d 个", len(refs)-failed, failed))
}
