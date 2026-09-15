package server

import (
	"path/filepath"
	"testing"

	"github.com/ProxyStation/proxystation/db"
	"github.com/ProxyStation/proxystation/db/configure"
)

func setupLogFilterTestDB(t *testing.T) {
	t.Helper()
	if err := db.Init(filepath.Join(t.TempDir(), "test.db")); err != nil {
		t.Fatalf("db init: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

// 回归测试：Tag 为空的自定义入站（cfdo/cfgoodnet 就是这种情况）
// 不能把"全部入站"（空过滤条件）劫持成"按该入站过滤"，
// 否则所有带 [from: ...] 的请求日志都会被 shouldIncludeLog 过滤掉。
func TestResolveInboundFilterEmptyMeansAll(t *testing.T) {
	setupLogFilterTestDB(t)
	if err := configure.SetCustomInbounds([]configure.CustomInbound{
		{ID: "cfgoodnet-id", Tag: "", Name: "CFgoodnet", Protocol: "cfgoodnet"},
		{ID: "socks-id", Tag: "inbound-socks", Name: "socksin", Protocol: "socks"},
	}); err != nil {
		t.Fatalf("set custom inbounds: %v", err)
	}

	if id, tag := resolveInboundFilter(""); id != "" || tag != "" {
		t.Fatalf(`空过滤条件应表示全部入站，实际解析为 id=%q tag=%q`, id, tag)
	}
}

func TestResolveInboundFilterByCustomInbound(t *testing.T) {
	setupLogFilterTestDB(t)
	if err := configure.SetCustomInbounds([]configure.CustomInbound{
		{ID: "cfgoodnet-id", Tag: "", Name: "CFgoodnet", Protocol: "cfgoodnet"},
		{ID: "socks-id", Tag: "inbound-socks", Name: "socksin", Protocol: "socks"},
	}); err != nil {
		t.Fatalf("set custom inbounds: %v", err)
	}

	if id, tag := resolveInboundFilter("socks-id"); id != "socks-id" || tag != "inbound-socks" {
		t.Fatalf("按 ID 解析失败: id=%q tag=%q", id, tag)
	}
	if id, tag := resolveInboundFilter("inbound-socks"); id != "socks-id" || tag != "inbound-socks" {
		t.Fatalf("按 Tag 解析失败: id=%q tag=%q", id, tag)
	}
}

// 全部入站 + 系统日志：带 [from: ...] 的请求日志和系统日志都必须通过。
func TestShouldIncludeLogAllInboundWithRequestLogs(t *testing.T) {
	requestLog := "INFO[0016] outbound/http[US]: outbound connection to www.example.com:443 [from: http-in]"
	if !shouldIncludeLog(requestLog, "", "", true) {
		t.Fatalf("全部入站不应过滤请求日志: %s", requestLog)
	}
	if !shouldIncludeLog("✅ 代理启动成功", "", "", true) {
		t.Fatalf("勾选系统日志时系统日志应通过")
	}
}
