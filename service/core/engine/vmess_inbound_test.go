package engine

import (
	"testing"

	"github.com/ProxyStation/proxystation/db/configure"
)

func TestVMessInboundBuiltForBothKernels(t *testing.T) {
	setupTestDB(t)

	if err := configure.SetCustomInbounds([]configure.CustomInbound{
		{
			ID:           "vmess-id",
			Tag:          "vmess-in",
			Name:         "VMess WS",
			Protocol:     "vmess",
			Listen:       "0.0.0.0",
			Port:         10086,
			SniffEnabled: true,
			SniffDest:    []string{"http", "tls"},
			VMess: &configure.VMessInbound{
				Transport: "ws",
				Path:      "/gateway",
				Host:      "edge.example.com",
				Users: []configure.VMessInboundUser{
					{UUID: "11111111-1111-1111-1111-111111111111", AlterID: 0, Name: "primary"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("set custom inbounds: %v", err)
	}

	xrayConfig, err := BuildXrayConfig(configure.GetSettingNotNil())
	if err != nil {
		t.Fatalf("BuildXrayConfig: %v", err)
	}
	var foundXray bool
	for _, inbound := range xrayConfig.Inbounds {
		if inbound.Tag != "vmess-in" {
			continue
		}
		foundXray = true
		if inbound.Protocol != "vmess" || inbound.Settings == nil || len(inbound.Settings.Clients) != 1 {
			t.Fatalf("xray VMess 入站配置错误: %+v", inbound)
		}
		if inbound.StreamSettings == nil || inbound.StreamSettings.Network != "ws" || inbound.StreamSettings.WSSettings == nil {
			t.Fatalf("xray VMess WS 配置缺失: %+v", inbound.StreamSettings)
		}
		if !inbound.Sniffing.Enabled {
			t.Fatal("xray VMess 入站未启用嗅探")
		}
	}
	if !foundXray {
		t.Fatal("xray 配置缺少 VMess 入站")
	}

	singboxConfig, err := BuildSingboxConfig(configure.GetSettingNotNil())
	if err != nil {
		t.Fatalf("BuildSingboxConfig: %v", err)
	}
	var foundSingbox, foundSniff bool
	for _, inbound := range singboxConfig.Inbounds {
		if inbound.Tag != "vmess-in" {
			continue
		}
		foundSingbox = true
		if inbound.Type != "vmess" || len(inbound.Users) != 1 || inbound.Users[0].UUID == "" {
			t.Fatalf("sing-box VMess 入站配置错误: %+v", inbound)
		}
		if inbound.Transport == nil || inbound.Transport.Type != "ws" || inbound.Transport.Path != "/gateway" {
			t.Fatalf("sing-box VMess WS 配置缺失: %+v", inbound.Transport)
		}
	}
	for _, rule := range singboxConfig.Route.Rules {
		if rule.Action == "sniff" && hasStr(rule.InboundTag, "vmess-in") {
			foundSniff = true
			break
		}
	}
	if !foundSingbox {
		t.Fatal("sing-box 配置缺少 VMess 入站")
	}
	if !foundSniff {
		t.Fatal("sing-box 配置缺少 VMess 入站嗅探规则")
	}
}
