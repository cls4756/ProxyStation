package engine

import (
	"testing"

	"github.com/ProxyStation/proxystation/core/coreObj"
	"github.com/ProxyStation/proxystation/db/configure"
)

func TestMainProbeInboundRoutesAuthenticatedUsersToOutbounds(t *testing.T) {
	setupTestDB(t)
	if err := configure.SetServers([]configure.ServerRaw{
		{Name: "default", Host: "default.example.com", Port: 443, Type: "vmess",
			Link: "vmess://" + vmessB64(`{"add":"default.example.com","port":"443","id":"11111111-1111-1111-1111-111111111111","net":"tcp"}`)},
		{Name: "front", Host: "front.example.com", Port: 443, Type: "vmess",
			Link: "vmess://" + vmessB64(`{"add":"front.example.com","port":"443","id":"22222222-2222-2222-2222-222222222222","net":"tcp"}`)},
	}); err != nil {
		t.Fatalf("set servers: %v", err)
	}
	bindOutboundToServer(t, "proxy", 0)
	bindOutboundToServer(t, "hk", 1)

	credential := mainProbeCredentialForOutbound("hk")
	setting := configure.GetSettingNotNil()

	xrayConfig, err := BuildXrayConfig(setting)
	if err != nil {
		t.Fatalf("BuildXrayConfig: %v", err)
	}
	if len(xrayConfig.Inbounds) < 3 {
		t.Fatalf("xray 缺少主内核探测入站: %+v", xrayConfig.Inbounds)
	}
	xrayInbound := xrayConfig.Inbounds[2]
	if xrayInbound.Tag != MainProbeInboundTag || xrayInbound.Listen != ProbeInboundListen || xrayInbound.Port != ProbeInboundSocksPort {
		t.Fatalf("xray 主内核探测入站错误: %+v", xrayInbound)
	}
	if !hasXrayProbeAccount(xrayInbound.Settings.Accounts, credential) || !hasXrayProbeRoute(xrayConfig.Routing.Rules, "hk", credential.Username) {
		t.Fatal("xray 缺少 hk 的探测账号或路由")
	}

	singboxConfig, err := BuildSingboxConfig(setting)
	if err != nil {
		t.Fatalf("BuildSingboxConfig: %v", err)
	}
	if len(singboxConfig.Inbounds) < 3 {
		t.Fatalf("sing-box 缺少主内核探测入站: %+v", singboxConfig.Inbounds)
	}
	singboxInbound := singboxConfig.Inbounds[2]
	if singboxInbound.Tag != MainProbeInboundTag || singboxInbound.Listen != ProbeInboundListen || singboxInbound.ListenPort != ProbeInboundSocksPort {
		t.Fatalf("sing-box 主内核探测入站错误: %+v", singboxInbound)
	}
	if !hasSingboxProbeUser(singboxInbound.Users, credential) || !hasSingboxProbeRoute(singboxConfig.Route.Rules, "hk", credential.Username) {
		t.Fatal("sing-box 缺少 hk 的探测账号或路由")
	}
}

func hasXrayProbeAccount(accounts []coreObj.InboundAccount, credential mainProbeCredential) bool {
	for _, account := range accounts {
		if account.User == credential.Username && account.Pass == credential.Password {
			return true
		}
	}
	return false
}

func hasXrayProbeRoute(rules []coreObj.RoutingRule, outboundName, username string) bool {
	for _, rule := range rules {
		if rule.OutboundTag == outboundName && hasStr(rule.InboundTag, MainProbeInboundTag) && hasStr(rule.User, username) {
			return true
		}
	}
	return false
}

func hasSingboxProbeUser(users []SingboxInboundUser, credential mainProbeCredential) bool {
	for _, user := range users {
		if user.Username == credential.Username && user.Password == credential.Password {
			return true
		}
	}
	return false
}

func hasSingboxProbeRoute(rules []SingboxRouteRule, outboundName, username string) bool {
	for _, rule := range rules {
		if rule.Outbound == outboundName && hasStr(rule.InboundTag, MainProbeInboundTag) && hasStr(rule.AuthUser, username) {
			return true
		}
	}
	return false
}
