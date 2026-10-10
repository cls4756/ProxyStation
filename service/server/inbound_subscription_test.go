package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProxyStation/proxystation/db"
	"github.com/ProxyStation/proxystation/db/configure"
	"github.com/gin-gonic/gin"
)

func setupInboundSubscriptionTestDB(t *testing.T) {
	t.Helper()
	if err := db.Init(filepath.Join(t.TempDir(), "test.db")); err != nil {
		t.Fatalf("db init: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := configure.SetSetting(configure.NewSetting()); err != nil {
		t.Fatalf("set setting: %v", err)
	}
	if err := configure.SetCustomInbounds([]configure.CustomInbound{
		{ID: "custom-http", Name: "公网 HTTP", Protocol: "http", Port: 18080, Accounts: []configure.InboundAccount{{Username: "u", Password: "p"}}},
		{ID: "custom-vmess", Name: "公网 VMess", Protocol: "vmess", Port: 10086, VMess: &configure.VMessInbound{
			Transport: "ws", Path: "/gateway", Host: "edge.example.com",
			Users: []configure.VMessInboundUser{{UUID: "11111111-1111-1111-1111-111111111111", Name: "primary"}},
		}},
	}); err != nil {
		t.Fatalf("set inbounds: %v", err)
	}
}

func TestRenderInboundSubscriptionIncludesVMess(t *testing.T) {
	setupInboundSubscriptionTestDB(t)

	content, err := renderInboundSubscription(configure.InboundSubscription{Host: "proxy.example.com", Format: "singbox", InboundIDs: []string{"custom-vmess"}})
	if err != nil {
		t.Fatalf("render singbox: %v", err)
	}
	var config struct {
		Outbounds []map[string]interface{} `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		t.Fatalf("decode singbox: %v", err)
	}
	if len(config.Outbounds) != 1 || config.Outbounds[0]["type"] != "vmess" || config.Outbounds[0]["uuid"] != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("unexpected VMess outbound: %#v", config.Outbounds)
	}
	transport, ok := config.Outbounds[0]["transport"].(map[string]interface{})
	if !ok || transport["type"] != "ws" || transport["path"] != "/gateway" {
		t.Fatalf("unexpected VMess transport: %#v", config.Outbounds[0]["transport"])
	}

	content, err = renderInboundSubscription(configure.InboundSubscription{Host: "proxy.example.com", Format: "v2ray", InboundIDs: []string{"custom-vmess"}})
	if err != nil {
		t.Fatalf("render v2ray: %v", err)
	}
	if !strings.HasPrefix(content, "vmess://") {
		t.Fatalf("unexpected VMess link: %q", content)
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(content, "vmess://"))
	if err != nil {
		t.Fatalf("decode VMess link: %v", err)
	}
	var node map[string]string
	if err := json.Unmarshal(payload, &node); err != nil {
		t.Fatalf("decode VMess payload: %v", err)
	}
	if node["add"] != "proxy.example.com" || node["id"] != "11111111-1111-1111-1111-111111111111" || node["net"] != "ws" || node["path"] != "/gateway" {
		t.Fatalf("unexpected VMess payload: %#v", node)
	}
}

func TestRenderInboundSubscriptionReplacesListenHost(t *testing.T) {
	setupInboundSubscriptionTestDB(t)
	content, err := renderInboundSubscription(configure.InboundSubscription{Host: "proxy.example.com", Format: "singbox", InboundIDs: []string{"builtin:socks", "custom-http"}})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var config struct {
		Outbounds []map[string]interface{} `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(config.Outbounds) != 2 || config.Outbounds[0]["server"] != "proxy.example.com" || config.Outbounds[1]["server"] != "proxy.example.com" {
		t.Fatalf("unexpected outbounds: %#v", config.Outbounds)
	}
	if config.Outbounds[1]["username"] != "u" || config.Outbounds[1]["password"] != "p" {
		t.Fatalf("authentication missing: %#v", config.Outbounds[1])
	}
}

func TestRenderInboundSubscriptionIncludesSingboxRouteRules(t *testing.T) {
	setupInboundSubscriptionTestDB(t)
	content, err := renderInboundSubscription(configure.InboundSubscription{
		Host:       "proxy.example.com",
		Format:     "singbox",
		InboundIDs: []string{"builtin:socks", "custom-http"},
		Rules: []configure.InboundSubscriptionRule{
			{Name: "国内直连", Enabled: true, Domains: []string{"geosite:cn"}, Action: "direct"},
			{Name: "广告拦截", Enabled: true, Domains: []string{"keyword:ads"}, Action: "block"},
		},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var config struct {
		Route struct {
			Rules []map[string]interface{} `json:"rules"`
		} `json:"route"`
	}
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(config.Route.Rules) != 2 || config.Route.Rules[0]["outbound"] != "direct" || config.Route.Rules[1]["action"] != "reject" {
		t.Fatalf("unexpected route rules: %#v", config.Route.Rules)
	}
}

func TestPublicInboundSubscriptionDoesNotRequireLogin(t *testing.T) {
	setupInboundSubscriptionTestDB(t)
	item := &configure.InboundSubscription{ID: "sub", Token: "public-token", Name: "test", Host: "1.2.3.4", Format: "v2ray", InboundIDs: []string{"custom-http"}}
	if err := configure.AppendInboundSubscription(item); err != nil {
		t.Fatalf("append subscription: %v", err)
	}
	gin.SetMode(gin.TestMode)
	router := NewRouter("")
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/subscribe/inbounds/public-token", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") == "" {
		t.Fatal("public subscription should disable caching")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(recorder.Body.String()))
	decodedText := string(decoded)
	if err != nil || !strings.Contains(decodedText, "http://u:p@1.2.3.4:18080") || !strings.Contains(decodedText, "name=%E5%85%AC%E7%BD%91+HTTP") {
		t.Fatalf("unexpected subscription body: %q, err=%v", recorder.Body.String(), err)
	}
}

func TestNormalizeInboundSubscriptionAcceptsVMessAndRejectsInvalidHostAndProtocol(t *testing.T) {
	setupInboundSubscriptionTestDB(t)
	base := inboundSubscriptionRequest{
		Name:       "test",
		Host:       "proxy.example.com",
		Format:     "singbox",
		InboundIDs: []string{"custom-http"},
	}

	for _, host := range []string{"https://proxy.example.com", "proxy.example.com:443", "proxy example.com", ""} {
		req := base
		req.Host = host
		if _, err := normalizeInboundSubscription(req, nil); err == nil {
			t.Fatalf("expected invalid host to fail: %q", host)
		}
	}
	base.InboundIDs = []string{"custom-vmess"}
	if _, err := normalizeInboundSubscription(base, nil); err != nil {
		t.Fatalf("expected VMess inbound to be accepted: %v", err)
	}

	if err := configure.SetCustomInbounds([]configure.CustomInbound{{ID: "custom-cfdo", Name: "CFDO", Protocol: "cfdo", Port: 18080}}); err != nil {
		t.Fatalf("set inbounds: %v", err)
	}
	base.InboundIDs = []string{"custom-cfdo"}
	if _, err := normalizeInboundSubscription(base, nil); err == nil {
		t.Fatal("expected unsupported inbound to be rejected")
	}
}
