package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ProxyStation/proxystation/db"
	"github.com/ProxyStation/proxystation/db/configure"
	"github.com/ProxyStation/proxystation/pkg/ipinfo"
	"github.com/ProxyStation/proxystation/pkg/ipscore"
	"github.com/gin-gonic/gin"
)

func setupListQueryTestDB(t *testing.T) {
	t.Helper()
	if err := db.Init(filepath.Join(t.TempDir(), "test.db")); err != nil {
		t.Fatalf("db init: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

func TestListViewsOmitRawLinksAndSubscriptionNodes(t *testing.T) {
	setupListQueryTestDB(t)
	manual := &configure.ServerRaw{Link: "vless://secret", Name: "manual", Host: "1.1.1.1", Port: 443, Type: "vless"}
	if err := configure.AppendServers([]*configure.ServerRaw{manual}); err != nil {
		t.Fatalf("append server: %v", err)
	}
	sub := &configure.SubscriptionRaw{ID: "sub", Name: "subscription", Servers: []configure.ServerRaw{
		{Link: "vmess://secret", Name: "sub-node", Host: "2.2.2.2", Port: 443, Type: "vmess"},
	}}
	if err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{sub}); err != nil {
		t.Fatalf("append subscription: %v", err)
	}

	gin.SetMode(gin.TestMode)
	serverRecorder := httptest.NewRecorder()
	serverContext, _ := gin.CreateTestContext(serverRecorder)
	serverContext.Request = httptest.NewRequest(http.MethodGet, "/api/servers?view=list", nil)
	getServers(serverContext)
	if bytes.Contains(serverRecorder.Body.Bytes(), []byte("vless://secret")) || bytes.Contains(serverRecorder.Body.Bytes(), []byte(`"link"`)) {
		t.Fatalf("list response leaked raw link: %s", serverRecorder.Body.String())
	}

	subRecorder := httptest.NewRecorder()
	subContext, _ := gin.CreateTestContext(subRecorder)
	subContext.Request = httptest.NewRequest(http.MethodGet, "/api/subscriptions?view=summary", nil)
	getSubscriptions(subContext)
	var payload struct {
		Subscriptions []struct {
			ServerCount int               `json:"serverCount"`
			Servers     []json.RawMessage `json:"servers"`
		} `json:"subscriptions"`
	}
	if err := json.Unmarshal(subRecorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode subscriptions: %v", err)
	}
	if len(payload.Subscriptions) != 1 || payload.Subscriptions[0].ServerCount != 1 || payload.Subscriptions[0].Servers != nil {
		t.Fatalf("unexpected summary response: %s", subRecorder.Body.String())
	}
}

func TestSubscriptionServersSupportsPagination(t *testing.T) {
	setupListQueryTestDB(t)
	sub := &configure.SubscriptionRaw{ID: "sub", Servers: []configure.ServerRaw{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	}}
	if err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{sub}); err != nil {
		t.Fatalf("append subscription: %v", err)
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "index", Value: "0"}}
	context.Request = httptest.NewRequest(http.MethodGet, "/api/subscriptions/0/servers?page=2&pageSize=2", nil)
	getSubscriptionServers(context)

	var payload struct {
		Servers     []serverListItem `json:"servers"`
		ServerCount int              `json:"serverCount"`
		Offset      int              `json:"offset"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.ServerCount != 3 || payload.Offset != 2 || len(payload.Servers) != 1 || payload.Servers[0].Name != "c" {
		t.Fatalf("unexpected paged response: %s", recorder.Body.String())
	}
}

func TestSubscriptionServersSupportsThousandAndAllPageSizes(t *testing.T) {
	setupListQueryTestDB(t)
	servers := make([]configure.ServerRaw, 1001)
	for i := range servers {
		servers[i].Name = fmt.Sprintf("node-%d", i)
	}
	if err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{{ID: "sub", Servers: servers}}); err != nil {
		t.Fatalf("append subscription: %v", err)
	}

	for _, testCase := range []struct {
		name     string
		query    string
		expected int
	}{
		{name: "thousand", query: "?page=1&pageSize=1000", expected: 1000},
		{name: "all", query: "?page=1&pageSize=0", expected: 1001},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Params = gin.Params{{Key: "index", Value: "0"}}
			context.Request = httptest.NewRequest(http.MethodGet, "/api/subscriptions/0/servers"+testCase.query, nil)
			getSubscriptionServers(context)

			var payload struct {
				Servers []serverListItem `json:"servers"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if len(payload.Servers) != testCase.expected {
				t.Fatalf("expected %d servers, got %d", testCase.expected, len(payload.Servers))
			}
		})
	}
}

func TestPageBoundsSupportsThousandAndAllPageSizes(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		pageSize      int
		expectedStart int
		expectedEnd   int
	}{
		{name: "thousand", pageSize: 1000, expectedStart: 0, expectedEnd: 1000},
		{name: "maximum", pageSize: 2000, expectedStart: 0, expectedEnd: 1000},
		{name: "all", pageSize: 0, expectedStart: 0, expectedEnd: 1001},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			start, end := pageBounds(1001, 1, testCase.pageSize)
			if start != testCase.expectedStart || end != testCase.expectedEnd {
				t.Fatalf("expected bounds %d:%d, got %d:%d", testCase.expectedStart, testCase.expectedEnd, start, end)
			}
		})
	}
}

func TestQueryServersReturnsOnlyRequestedRefs(t *testing.T) {
	setupListQueryTestDB(t)
	if err := configure.AppendServers([]*configure.ServerRaw{
		{Name: "a", Host: "1.1.1.1", Port: 1, Type: "socks"},
		{Name: "b", Host: "2.2.2.2", Port: 2, Type: "socks", Latency: 44, LastProbeTime: 45, FastLatency: 22, LastFastProbeTime: 23},
	}); err != nil {
		t.Fatalf("append servers: %v", err)
	}

	body := bytes.NewBufferString(`{"refs":[{"type":"server","index":1,"sub":0}]}`)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/servers/query", body)
	context.Request.Header.Set("Content-Type", "application/json")
	queryServers(context)

	var payload struct {
		Results []struct {
			Ref    configure.NodeRef `json:"ref"`
			Server serverListItem    `json:"server"`
		} `json:"results"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode query response: %v", err)
	}
	if len(payload.Results) != 1 || payload.Results[0].Ref.Index != 1 || payload.Results[0].Server.Name != "b" ||
		payload.Results[0].Server.Latency != 44 || payload.Results[0].Server.LastProbeTime != 45 ||
		payload.Results[0].Server.FastLatency != 22 || payload.Results[0].Server.LastFastProbeTime != 23 {
		t.Fatalf("unexpected query response: %s", recorder.Body.String())
	}
}

func TestSearchServersReturnsGroupNames(t *testing.T) {
	setupListQueryTestDB(t)
	if err := configure.AppendServers([]*configure.ServerRaw{{Name: "target-node"}}); err != nil {
		t.Fatalf("append server: %v", err)
	}
	ref := configure.ServerRef{Type: "server", Index: 0}
	if err := configure.AppendGroup(&configure.Group{ID: "one", Name: "分组一", Servers: []configure.ServerRef{ref}}); err != nil {
		t.Fatalf("append first group: %v", err)
	}
	if err := configure.AppendGroup(&configure.Group{ID: "two", Name: "分组二", Servers: []configure.ServerRef{ref}}); err != nil {
		t.Fatalf("append second group: %v", err)
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/servers/search?q=target", nil)
	searchServers(context)

	var payload struct {
		Results []struct {
			GroupNames []string `json:"groupNames"`
		} `json:"results"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Results) != 1 || !reflect.DeepEqual(payload.Results[0].GroupNames, []string{"分组一", "分组二"}) {
		t.Fatalf("unexpected search groups: %s", recorder.Body.String())
	}
}

func TestSearchServersSupportsMultipleFiltersAndIndependentTotal(t *testing.T) {
	setupListQueryTestDB(t)
	scoreGood := 85
	scoreLow := 55
	if err := configure.AppendServers([]*configure.ServerRaw{
		{Name: "tokyo-a", Host: "1.1.1.1", Type: "vless", IPType: "residential", Latency: 120, LastProbeTime: 1, IPScore: &scoreGood},
		{Name: "tokyo-b", Host: "2.2.2.2", Type: "vless", IPType: "residential", Latency: 180, LastProbeTime: 1, IPScore: &scoreGood},
		{Name: "tokyo-c", Host: "3.3.3.3", Type: "vless", IPType: "idc", Latency: 100, LastProbeTime: 1, IPScore: &scoreGood},
		{Name: "tokyo-d", Host: "4.4.4.4", Type: "vmess", IPType: "residential", Latency: 100, LastProbeTime: 1, IPScore: &scoreGood},
		{Name: "tokyo-e", Host: "5.5.5.5", Type: "vless", IPType: "residential", Latency: 100, LastProbeTime: 1, IPScore: &scoreLow},
		{Name: "tokyo-f", Host: "6.6.6.6", Type: "vless", IPType: "residential"},
	}); err != nil {
		t.Fatalf("append servers: %v", err)
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet,
		"/api/servers/search?type=vless&ipType=residential&probe=reachable&maxLatency=200&minScore=70&source=manual&limit=1", nil)
	searchServers(context)

	var payload struct {
		Results []struct {
			Server serverListItem `json:"server"`
		} `json:"results"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	if recorder.Code != http.StatusOK || payload.Total != 2 || len(payload.Results) != 1 || payload.Results[0].Server.Name != "tokyo-a" {
		t.Fatalf("unexpected filtered response: %s", recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	context, _ = gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet,
		"/api/servers/search?type=vless&ipType=residential&probe=reachable&maxLatency=200&minScore=70&source=manual&limit=1&offset=1", nil)
	searchServers(context)
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode paged search response: %v", err)
	}
	if recorder.Code != http.StatusOK || payload.Total != 2 || len(payload.Results) != 1 || payload.Results[0].Server.Name != "tokyo-b" {
		t.Fatalf("unexpected paged filtered response: %s", recorder.Body.String())
	}
}

func TestGetServerBySourceReturnsSingleMatch(t *testing.T) {
	setupListQueryTestDB(t)
	if err := configure.AppendServers([]*configure.ServerRaw{
		{Name: "normal", Source: "manual"},
		{Name: "proxy", Source: "inbound:abc:cfdo:8080"},
	}); err != nil {
		t.Fatalf("append servers: %v", err)
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/servers/by-source?prefix=inbound%3Aabc", nil)
	getServerBySource(context)
	var payload struct {
		Result serverQueryResult `json:"result"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if recorder.Code != http.StatusOK || payload.Result.Ref.Index != 1 || payload.Result.Server.Name != "proxy" {
		t.Fatalf("unexpected source lookup: %s", recorder.Body.String())
	}
}

func TestBootstrapOnlyReturnsRuntimeStatus(t *testing.T) {
	setupListQueryTestDB(t)
	if err := configure.AppendServers([]*configure.ServerRaw{
		{Link: "socks://secret", Name: "bootstrap-node", Host: "1.1.1.1", Port: 1080, Type: "socks"},
	}); err != nil {
		t.Fatalf("append servers: %v", err)
	}
	if err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{
		{ID: "sub", Name: "subscription", Servers: []configure.ServerRaw{{Link: "vmess://secret", Name: "hidden"}}},
	}); err != nil {
		t.Fatalf("append subscription: %v", err)
	}
	if err := configure.AppendGroup(&configure.Group{ID: "group", Name: "group"}); err != nil {
		t.Fatalf("append group: %v", err)
	}
	if err := configure.SetOutbound("proxy", &configure.Outbound{
		Name: "proxy",
		Target: configure.OutboundTarget{
			TargetType: "node",
			NodeRef:    &configure.NodeRef{Type: "server", Index: 0},
		},
	}); err != nil {
		t.Fatalf("set outbound: %v", err)
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	getBootstrap(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", recorder.Code, recorder.Body.String())
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("socks://secret")) || bytes.Contains(recorder.Body.Bytes(), []byte("vmess://secret")) {
		t.Fatalf("bootstrap leaked raw links: %s", recorder.Body.String())
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	if len(payload) != 1 || payload["running"] == nil {
		t.Fatalf("bootstrap should only contain running status: %s", recorder.Body.String())
	}
}

func TestGroupSummaryAndPagedServers(t *testing.T) {
	setupListQueryTestDB(t)
	if err := configure.AppendServers([]*configure.ServerRaw{
		{Name: "a", Host: "1.1.1.1", Port: 1, Type: "socks"},
		{Name: "b", Host: "2.2.2.2", Port: 2, Type: "socks"},
		{Name: "c", Host: "3.3.3.3", Port: 3, Type: "socks"},
	}); err != nil {
		t.Fatalf("append servers: %v", err)
	}
	if err := configure.AppendGroup(&configure.Group{
		ID: "group", Name: "group", Servers: []configure.ServerRef{
			{Type: "server", Index: 0}, {Type: "server", Index: 1}, {Type: "server", Index: 2},
		},
	}); err != nil {
		t.Fatalf("append group: %v", err)
	}

	summaryRecorder := httptest.NewRecorder()
	summaryContext, _ := gin.CreateTestContext(summaryRecorder)
	summaryContext.Request = httptest.NewRequest(http.MethodGet, "/api/groups?view=summary", nil)
	getGroups(summaryContext)
	var summary struct {
		Groups []struct {
			ServerCount int               `json:"serverCount"`
			Servers     []json.RawMessage `json:"servers"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(summaryRecorder.Body.Bytes(), &summary); err != nil {
		t.Fatalf("decode group summary: %v", err)
	}
	if len(summary.Groups) != 1 || summary.Groups[0].ServerCount != 3 || summary.Groups[0].Servers != nil {
		t.Fatalf("unexpected group summary: %s", summaryRecorder.Body.String())
	}

	pageRecorder := httptest.NewRecorder()
	pageContext, _ := gin.CreateTestContext(pageRecorder)
	pageContext.Params = gin.Params{{Key: "index", Value: "0"}}
	pageContext.Request = httptest.NewRequest(http.MethodGet, "/api/groups/0/servers?page=2&pageSize=2", nil)
	getGroupServers(pageContext)
	var page struct {
		Refs        []configure.ServerRef `json:"refs"`
		Results     []serverQueryResult   `json:"results"`
		ServerCount int                   `json:"serverCount"`
		Offset      int                   `json:"offset"`
	}
	if err := json.Unmarshal(pageRecorder.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode group page: %v", err)
	}
	if page.ServerCount != 3 || page.Offset != 2 || len(page.Refs) != 1 || len(page.Results) != 1 || page.Results[0].Server.Name != "c" {
		t.Fatalf("unexpected group page: %s", pageRecorder.Body.String())
	}
}

func TestSetOutboundActiveNodePreservesGroupBinding(t *testing.T) {
	setupListQueryTestDB(t)
	if err := configure.AppendServers([]*configure.ServerRaw{
		{Name: "a"}, {Name: "b"}, {Name: "outside"},
	}); err != nil {
		t.Fatalf("append servers: %v", err)
	}
	if err := configure.AppendGroup(&configure.Group{
		ID: "group", Name: "group", Servers: []configure.ServerRef{
			{Type: "server", Index: 0}, {Type: "server", Index: 1},
		},
	}); err != nil {
		t.Fatalf("append group: %v", err)
	}
	threshold, cooldown := 120, 900
	if err := configure.SetOutbound("proxy", &configure.Outbound{
		Name: "proxy",
		Target: configure.OutboundTarget{
			TargetType: "group", GroupID: "group", Mode: configure.ModeStable,
			ActiveNodeRef: &configure.NodeRef{Type: "server", Index: 0},
		},
		ProbeInterval: "5m", ProbeMode: "real",
		SwitchThresholdMs: &threshold, SwitchCooldownSec: &cooldown,
	}); err != nil {
		t.Fatalf("set outbound: %v", err)
	}

	body := bytes.NewBufferString(`{"ref":{"type":"server","index":1,"sub":0}}`)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "name", Value: "proxy"}}
	context.Request = httptest.NewRequest(http.MethodPut, "/api/outbounds/proxy/active-node", body)
	context.Request.Header.Set("Content-Type", "application/json")
	setOutboundActiveNode(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
	}
	outbound := configure.GetOutbound("proxy")
	if outbound == nil || outbound.Target.TargetType != "group" || outbound.Target.GroupID != "group" || outbound.Target.Mode != configure.ModeStable {
		t.Fatalf("group binding changed: %+v", outbound)
	}
	if outbound.Target.ActiveNodeRef == nil || outbound.Target.ActiveNodeRef.Index != 1 {
		t.Fatalf("active node not updated: %+v", outbound.Target.ActiveNodeRef)
	}
	if outbound.ProbeInterval != "5m" || outbound.ProbeMode != "real" || outbound.SwitchThresholdMs == nil || *outbound.SwitchThresholdMs != threshold || outbound.SwitchCooldownSec == nil || *outbound.SwitchCooldownSec != cooldown {
		t.Fatalf("outbound settings changed: %+v", outbound)
	}
}

func TestSetOutboundActiveNodeRejectsNodeOutsideGroup(t *testing.T) {
	setupListQueryTestDB(t)
	if err := configure.AppendServers([]*configure.ServerRaw{{Name: "inside"}, {Name: "outside"}}); err != nil {
		t.Fatalf("append servers: %v", err)
	}
	if err := configure.AppendGroup(&configure.Group{
		ID: "group", Servers: []configure.ServerRef{{Type: "server", Index: 0}},
	}); err != nil {
		t.Fatalf("append group: %v", err)
	}
	if err := configure.SetOutbound("proxy", &configure.Outbound{
		Name: "proxy", Target: configure.OutboundTarget{
			TargetType: "group", GroupID: "group", Mode: configure.ModeLeastPing,
			ActiveNodeRef: &configure.NodeRef{Type: "server", Index: 0},
		},
	}); err != nil {
		t.Fatalf("set outbound: %v", err)
	}

	body := bytes.NewBufferString(`{"ref":{"type":"server","index":1,"sub":0}}`)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "name", Value: "proxy"}}
	context.Request = httptest.NewRequest(http.MethodPut, "/api/outbounds/proxy/active-node", body)
	context.Request.Header.Set("Content-Type", "application/json")
	setOutboundActiveNode(context)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
	}
	outbound := configure.GetOutbound("proxy")
	if outbound.Target.ActiveNodeRef == nil || outbound.Target.ActiveNodeRef.Index != 0 {
		t.Fatalf("active node changed after rejected request: %+v", outbound.Target.ActiveNodeRef)
	}
}

func TestGroupServersByIDSupportsFilterAndCatalogView(t *testing.T) {
	setupListQueryTestDB(t)
	if err := configure.AppendServers([]*configure.ServerRaw{
		{Name: "hk-01", Host: "1.1.1.1", Port: 1, Type: "socks", Latency: 33},
		{Name: "jp-01", Host: "2.2.2.2", Port: 2, Type: "vmess", Latency: 44},
		{Name: "hk-02", Host: "3.3.3.3", Port: 3, Type: "socks", Latency: 55},
	}); err != nil {
		t.Fatalf("append servers: %v", err)
	}
	// 第一个分组只用来把目标分组挤到下标 1，验证按 ID 定位不受下标影响
	if err := configure.AppendGroup(&configure.Group{ID: "other", Name: "other"}); err != nil {
		t.Fatalf("append other group: %v", err)
	}
	if err := configure.AppendGroup(&configure.Group{
		ID: "target", Name: "target", Servers: []configure.ServerRef{
			{Type: "server", Index: 0}, {Type: "server", Index: 1}, {Type: "server", Index: 2},
		},
	}); err != nil {
		t.Fatalf("append target group: %v", err)
	}

	filterRecorder := httptest.NewRecorder()
	filterContext, _ := gin.CreateTestContext(filterRecorder)
	filterContext.Params = gin.Params{{Key: "index", Value: "target"}}
	filterContext.Request = httptest.NewRequest(http.MethodGet, "/api/groups/target/servers?page=1&pageSize=100&q=hk", nil)
	getGroupServers(filterContext)
	var filtered struct {
		Refs        []configure.ServerRef `json:"refs"`
		Results     []serverQueryResult   `json:"results"`
		ServerCount int                   `json:"serverCount"`
	}
	if err := json.Unmarshal(filterRecorder.Body.Bytes(), &filtered); err != nil {
		t.Fatalf("decode filtered group page: %v", err)
	}
	if filtered.ServerCount != 2 || len(filtered.Results) != 2 || len(filtered.Refs) != 2 {
		t.Fatalf("filter should only keep matching nodes: %s", filterRecorder.Body.String())
	}
	if filtered.Results[0].Server.Name != "hk-01" || filtered.Results[1].Server.Name != "hk-02" ||
		filtered.Refs[1].Index != 2 {
		t.Fatalf("unexpected filtered payload: %s", filterRecorder.Body.String())
	}

	catalogRecorder := httptest.NewRecorder()
	catalogContext, _ := gin.CreateTestContext(catalogRecorder)
	catalogContext.Params = gin.Params{{Key: "index", Value: "target"}}
	catalogContext.Request = httptest.NewRequest(http.MethodGet, "/api/groups/target/servers?view=catalog&page=1&pageSize=100", nil)
	getGroupServers(catalogContext)
	var catalog struct {
		Refs        []configure.ServerRef `json:"refs"`
		Results     []serverCatalogResult `json:"results"`
		ServerCount int                   `json:"serverCount"`
	}
	if err := json.Unmarshal(catalogRecorder.Body.Bytes(), &catalog); err != nil {
		t.Fatalf("decode catalog group page: %v", err)
	}
	if catalog.ServerCount != 3 || len(catalog.Results) != 3 || catalog.Refs != nil {
		t.Fatalf("unexpected catalog payload: %s", catalogRecorder.Body.String())
	}
	if catalog.Results[0].Server.Name != "hk-01" || catalog.Results[0].Server.Port != 1 {
		t.Fatalf("catalog should keep selection fields: %s", catalogRecorder.Body.String())
	}
	if bytes.Contains(catalogRecorder.Body.Bytes(), []byte(`"latency"`)) ||
		bytes.Contains(catalogRecorder.Body.Bytes(), []byte(`"lastProbeTime"`)) ||
		bytes.Contains(catalogRecorder.Body.Bytes(), []byte(`"fastLatency"`)) ||
		bytes.Contains(catalogRecorder.Body.Bytes(), []byte(`"lastFastProbeTime"`)) {
		t.Fatalf("catalog view should drop probe fields: %s", catalogRecorder.Body.String())
	}
}

func TestBatchMoveServersToGroupUpdatesBothGroups(t *testing.T) {
	setupListQueryTestDB(t)
	if err := configure.AppendServers([]*configure.ServerRaw{
		{Name: "a", Host: "1.1.1.1", Port: 1, Type: "socks"},
		{Name: "b", Host: "2.2.2.2", Port: 2, Type: "socks"},
	}); err != nil {
		t.Fatalf("append servers: %v", err)
	}
	if err := configure.AppendGroup(&configure.Group{
		ID: "source", Name: "source", Servers: []configure.ServerRef{
			{Type: "server", Index: 0},
			{Type: "server", Index: 1},
		},
	}); err != nil {
		t.Fatalf("append source group: %v", err)
	}
	if err := configure.AppendGroup(&configure.Group{ID: "target", Name: "target"}); err != nil {
		t.Fatalf("append target group: %v", err)
	}

	body := bytes.NewBufferString(`{"refs":[{"type":"server","index":1,"sub":0}],"sourceGroupId":"source","targetGroupId":"target"}`)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/servers/batch-move-to-group", body)
	context.Request.Header.Set("Content-Type", "application/json")
	batchMoveServersToGroup(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
	}
	source := configure.GetGroup(0)
	target := configure.GetGroup(1)
	if source == nil || len(source.Servers) != 1 || source.Servers[0].Index != 0 {
		t.Fatalf("unexpected source group: %+v", source)
	}
	if target == nil || len(target.Servers) != 1 || target.Servers[0].Index != 1 {
		t.Fatalf("unexpected target group: %+v", target)
	}
}

func TestBatchRemoveServersFromGroupUsesSingleRequest(t *testing.T) {
	setupListQueryTestDB(t)
	if err := configure.AppendGroup(&configure.Group{
		ID: "group", Name: "group", Servers: []configure.ServerRef{
			{Type: "server", Index: 0},
			{Type: "server", Index: 1},
			{Type: "server", Index: 2},
		},
	}); err != nil {
		t.Fatalf("append group: %v", err)
	}

	body := bytes.NewBufferString(`{"refs":[{"type":"server","index":0,"sub":0},{"type":"server","index":2,"sub":0}]}`)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "index", Value: "0"}}
	context.Request = httptest.NewRequest(http.MethodPost, "/api/groups/0/servers/batch-remove", body)
	context.Request.Header.Set("Content-Type", "application/json")
	removeServersFromGroup(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
	}
	group := configure.GetGroup(0)
	if group == nil || len(group.Servers) != 1 || group.Servers[0].Index != 1 {
		t.Fatalf("unexpected group: %+v", group)
	}
}

func TestBatchCopyServersToGroupClonesInOneBatchAndSkipsDuplicates(t *testing.T) {
	setupListQueryTestDB(t)
	score := 82
	if err := configure.AppendServers([]*configure.ServerRaw{
		{Name: "a", Host: "1.1.1.1", Port: 1, Type: "socks", Source: "manual", Latency: 20, LastProbeTime: 101, FastLatency: 10, LastFastProbeTime: 201, IPType: "idc", IPGeoCountry: "美国", IPGeoRegion: "加州", IPScore: &score},
		{Name: "duplicate", Host: "1.1.1.1", Port: 1, Type: "socks", Source: "manual", Latency: 30, IPType: "idc"},
		{Name: "b", Host: "2.2.2.2", Port: 2, Type: "socks", Source: "manual", Latency: 40, LastProbeTime: 102, FastLatency: 30, LastFastProbeTime: 202, IPType: "residential", IPGeoCountry: "日本", IPScore: &score},
	}); err != nil {
		t.Fatalf("append servers: %v", err)
	}
	if err := configure.AppendGroup(&configure.Group{ID: "target", Name: "target"}); err != nil {
		t.Fatalf("append target group: %v", err)
	}

	body := bytes.NewBufferString(`{"refs":[{"type":"server","index":0},{"type":"server","index":1},{"type":"server","index":2}],"groupId":"target","clone":true}`)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/servers/batch-copy-to-group", body)
	context.Request.Header.Set("Content-Type", "application/json")
	batchCopyServersToGroup(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
	}
	group := configure.GetGroup(0)
	if group == nil || len(group.Servers) != 2 {
		t.Fatalf("unexpected target group: %+v", group)
	}
	servers := configure.GetServers()
	if len(servers) != 5 {
		t.Fatalf("expected two clones, got %d servers", len(servers))
	}
	for _, ref := range group.Servers {
		if ref.Type != "server" || ref.Index < 3 || servers[ref.Index].Source != "manual" {
			t.Fatalf("unexpected clone ref=%+v server=%+v", ref, servers[ref.Index])
		}
	}
	if servers[group.Servers[0].Index].Latency != 20 || servers[group.Servers[0].Index].LastProbeTime != 101 ||
		servers[group.Servers[0].Index].FastLatency != 10 || servers[group.Servers[0].Index].LastFastProbeTime != 201 ||
		servers[group.Servers[1].Index].Latency != 40 || servers[group.Servers[1].Index].LastProbeTime != 102 ||
		servers[group.Servers[1].Index].FastLatency != 30 || servers[group.Servers[1].Index].LastFastProbeTime != 202 {
		t.Fatalf("clone should preserve probe results: %+v, %+v", servers[group.Servers[0].Index], servers[group.Servers[1].Index])
	}
}

func TestIPScoreStateOmitsRefsWhileRunning(t *testing.T) {
	ipScoreTask.mu.Lock()
	previous := ipScoreTask.state
	ipScoreTask.state = ipScoreTaskState{
		Running: true,
		Refs:    []configure.NodeRef{{Type: "server", Index: 1}},
	}
	ipScoreTask.mu.Unlock()
	t.Cleanup(func() {
		ipScoreTask.mu.Lock()
		ipScoreTask.state = previous
		ipScoreTask.mu.Unlock()
	})

	if state := ipScoreState(); len(state.Refs) != 0 {
		t.Fatalf("running status should not repeat refs: %+v", state.Refs)
	}
	ipScoreTask.mu.Lock()
	ipScoreTask.state.Running = false
	ipScoreTask.mu.Unlock()
	if state := ipScoreState(); len(state.Refs) != 1 {
		t.Fatalf("completed status should include refs once: %+v", state.Refs)
	}
}

func TestSaveIPScoreStoresGeoAndKeepsItWhenProviderOmitsGeo(t *testing.T) {
	setupListQueryTestDB(t)
	if err := configure.AppendServers([]*configure.ServerRaw{{Name: "node", Host: "1.1.1.1", Port: 443}}); err != nil {
		t.Fatalf("append server: %v", err)
	}
	ref := configure.NodeRef{Type: "server", Index: 0}
	if err := saveIPScore(ref, ipscore.Result{
		Score: 82, Provider: ipscore.ProviderCleanIP,
		Country: "澳大利亚", CountryCode: "AU", Region: "昆士兰州", City: "布里斯班",
	}); err != nil {
		t.Fatalf("save score with geo: %v", err)
	}
	if err := saveIPScore(ref, ipscore.Result{Score: 70, Provider: ipscore.ProviderCustom}); err != nil {
		t.Fatalf("save score without geo: %v", err)
	}

	server := configure.GetServer(0)
	if server == nil || server.IPScore == nil || *server.IPScore != 70 {
		t.Fatalf("score not saved: %+v", server)
	}
	if server.IPGeoCountry != "澳大利亚" || server.IPGeoCountryCode != "AU" || server.IPGeoRegion != "昆士兰州" || server.IPGeoCity != "布里斯班" {
		t.Fatalf("geo should be preserved when later provider omits it: %+v", server)
	}
}

func TestSaveIPTypeAlsoStoresGeo(t *testing.T) {
	setupListQueryTestDB(t)
	if err := configure.AppendServers([]*configure.ServerRaw{{Name: "node", Host: "1.1.1.1", Port: 443}}); err != nil {
		t.Fatalf("append server: %v", err)
	}
	ref := configure.NodeRef{Type: "server", Index: 0}
	if err := saveIPType(ref, ipinfo.Result{
		Kind: ipinfo.KindIDC, IP: "1.1.1.1", ISP: "Cloudflare",
		Country: "Australia", CountryCode: "AU", Region: "Queensland", City: "Brisbane",
	}); err != nil {
		t.Fatalf("save ip type: %v", err)
	}
	server := configure.GetServer(0)
	if server == nil || server.IPType != "idc" || server.IPGeoCountry != "Australia" ||
		server.IPGeoCountryCode != "AU" || server.IPGeoRegion != "Queensland" || server.IPGeoCity != "Brisbane" {
		t.Fatalf("ip info was not fully saved: %+v", server)
	}
}
