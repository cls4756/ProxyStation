package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

func TestQueryServersReturnsOnlyRequestedRefs(t *testing.T) {
	setupListQueryTestDB(t)
	if err := configure.AppendServers([]*configure.ServerRaw{
		{Name: "a", Host: "1.1.1.1", Port: 1, Type: "socks"},
		{Name: "b", Host: "2.2.2.2", Port: 2, Type: "socks"},
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
	if len(payload.Results) != 1 || payload.Results[0].Ref.Index != 1 || payload.Results[0].Server.Name != "b" {
		t.Fatalf("unexpected query response: %s", recorder.Body.String())
	}
}

func TestBootstrapIncludesInitialDataAndReferencedNodes(t *testing.T) {
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
	var payload struct {
		Subscriptions []subscriptionListItem `json:"subscriptions"`
		Groups        []configure.Group      `json:"groups"`
		Outbounds     []configure.Outbound   `json:"outbounds"`
		Nodes         []serverQueryResult    `json:"nodes"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	if len(payload.Subscriptions) != 1 || payload.Subscriptions[0].ServerCount != 1 {
		t.Fatalf("unexpected subscriptions: %+v", payload.Subscriptions)
	}
	if len(payload.Groups) != 1 || len(payload.Outbounds) != 1 {
		t.Fatalf("unexpected bootstrap lists: groups=%d outbounds=%d", len(payload.Groups), len(payload.Outbounds))
	}
	if len(payload.Nodes) != 1 || payload.Nodes[0].Server.Name != "bootstrap-node" {
		t.Fatalf("unexpected referenced nodes: %+v", payload.Nodes)
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
		{Name: "a", Host: "1.1.1.1", Port: 1, Type: "socks", Source: "manual", Latency: 20, LastProbeTime: 101, IPType: "idc", IPGeoCountry: "美国", IPGeoRegion: "加州", IPScore: &score},
		{Name: "duplicate", Host: "1.1.1.1", Port: 1, Type: "socks", Source: "manual", Latency: 30, IPType: "idc"},
		{Name: "b", Host: "2.2.2.2", Port: 2, Type: "socks", Source: "manual", Latency: 40, LastProbeTime: 102, IPType: "residential", IPGeoCountry: "日本", IPScore: &score},
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
		servers[group.Servers[1].Index].Latency != 40 || servers[group.Servers[1].Index].LastProbeTime != 102 {
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
