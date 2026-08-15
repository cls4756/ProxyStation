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

func TestSnapshotLoadedNodeOutboundsUsesResolvedActiveNodes(t *testing.T) {
	setupTestDB(t)
	if err := configure.SetServers([]configure.ServerRaw{
		{Name: "default", Host: "default.example.com", Port: 443},
		{Name: "group-first", Host: "first.example.com", Port: 443},
	}); err != nil {
		t.Fatalf("set servers: %v", err)
	}
	bindOutboundToServer(t, "proxy", 0)
	if err := configure.AppendGroup(&configure.Group{
		ID: "g1", Name: "g1", Servers: []configure.ServerRef{{Type: "server", Index: 1}},
	}); err != nil {
		t.Fatalf("append group: %v", err)
	}
	if err := configure.AddOutbound("hk"); err != nil {
		t.Fatalf("add outbound hk: %v", err)
	}
	if err := configure.SetOutbound("hk", &configure.Outbound{
		Name: "hk", Target: configure.OutboundTarget{TargetType: "group", GroupID: "g1"},
	}); err != nil {
		t.Fatalf("set outbound hk: %v", err)
	}

	loaded := snapshotLoadedNodeOutbounds()
	if got := loaded[nodeRefKey(configure.NodeRef{Type: "server", Index: 0})]; got != "proxy" {
		t.Fatalf("默认节点加载出站 = %q，期望 proxy", got)
	}
	if got := loaded[nodeRefKey(configure.NodeRef{Type: "server", Index: 1})]; got != "hk" {
		t.Fatalf("分组首节点加载出站 = %q，期望 hk", got)
	}
}

func TestProbeConfigsReuseMainCoreFrontOutbound(t *testing.T) {
	target := &configure.ServerRaw{
		Name: "target", Host: "target.example.com", Port: 443, Type: "vmess", FrontProxy: "hk",
		Link: "vmess://" + vmessB64(`{"add":"target.example.com","port":"443","id":"11111111-1111-1111-1111-111111111111","net":"tcp"}`),
	}
	credential := mainProbeCredentialForOutbound("hk")
	options := probeInstanceOptions{mainFrontOutbound: "hk"}

	singboxConfig, err := buildProbeSingboxConfigWithOptions(target, 21001, options)
	if err != nil {
		t.Fatalf("buildProbeSingboxConfigWithOptions: %v", err)
	}
	var singTarget, singFront *SingboxOutbound
	for i := range singboxConfig.Outbounds {
		switch singboxConfig.Outbounds[i].Tag {
		case "proxy":
			singTarget = &singboxConfig.Outbounds[i]
		case "main-front":
			singFront = &singboxConfig.Outbounds[i]
		}
	}
	if singTarget == nil || singTarget.Detour != "main-front" {
		t.Fatalf("sing-box 目标出站未转交主内核前置代理: %+v", singTarget)
	}
	if singFront == nil || singFront.Type != "socks" || singFront.Server != ProbeInboundListen ||
		singFront.ServerPort != ProbeInboundSocksPort || singFront.Username != credential.Username || singFront.Password != credential.Password {
		t.Fatalf("sing-box 主内核前置出站错误: %+v", singFront)
	}

	xrayConfig, err := buildProbeXrayConfigWithOptions(target, 21002, options)
	if err != nil {
		t.Fatalf("buildProbeXrayConfigWithOptions: %v", err)
	}
	var xrayTarget, xrayFront *coreObj.OutboundObject
	for i := range xrayConfig.Outbounds {
		switch xrayConfig.Outbounds[i].Tag {
		case "proxy":
			xrayTarget = &xrayConfig.Outbounds[i]
		case "main-front":
			xrayFront = &xrayConfig.Outbounds[i]
		}
	}
	if xrayTarget == nil || xrayTarget.StreamSettings == nil || xrayTarget.StreamSettings.Sockopt == nil ||
		xrayTarget.StreamSettings.Sockopt.DialerProxy != "main-front" {
		t.Fatalf("xray 目标出站未转交主内核前置代理: %+v", xrayTarget)
	}
	if xrayFront == nil || xrayFront.Protocol != "socks" || len(xrayFront.Settings.Servers) != 1 ||
		xrayFront.Settings.Servers[0].Address != ProbeInboundListen || xrayFront.Settings.Servers[0].Port != ProbeInboundSocksPort ||
		len(xrayFront.Settings.Servers[0].Users) != 1 || xrayFront.Settings.Servers[0].Users[0].User != credential.Username ||
		xrayFront.Settings.Servers[0].Users[0].Pass != credential.Password {
		t.Fatalf("xray 主内核前置出站错误: %+v", xrayFront)
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
