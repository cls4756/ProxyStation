package engine

import (
	"testing"

	"github.com/ProxyStation/proxystation/db/configure"
)

func setupCompatibleDNS(t *testing.T, remote string) *configure.Setting {
	t.Helper()
	setupTestDB(t)
	if err := configure.SetServers([]configure.ServerRaw{
		{Name: "a", Host: "a.example.com", Port: 443, Type: "vmess",
			Link: "vmess://" + vmessB64(`{"add":"a.example.com","port":"443","id":"11111111-1111-1111-1111-111111111111","net":"tcp"}`)},
	}); err != nil {
		t.Fatalf("set servers: %v", err)
	}
	bindOutboundToServer(t, "proxy", 0)

	setting := configure.GetSettingNotNil()
	setting.DNSMode = "compatible"
	setting.DNSRemoteServer = remote
	return setting
}

func findRemoteDNSServer(t *testing.T, setting *configure.Setting) SingboxDNSServer {
	t.Helper()
	cfg, err := BuildSingboxConfig(setting)
	if err != nil {
		t.Fatalf("BuildSingboxConfig: %v", err)
	}
	for _, server := range cfg.DNS.Servers {
		if server.Tag == "remote" {
			return server
		}
	}
	t.Fatal("配置里没有 remote 解析器")
	return SingboxDNSServer{}
}

// 默认远端解析器保持历史行为：DoH + bootstrap + 经第一个出站
func TestCompatibleRemoteDNSDefault(t *testing.T) {
	server := findRemoteDNSServer(t, setupCompatibleDNS(t, ""))
	if server.Type != "https" || server.Server != "8.8.8.8" || server.Path != "/dns-query" {
		t.Fatalf("remote 解析器 = %+v，期望 https://8.8.8.8/dns-query", server)
	}
	if server.Detour != "proxy" {
		t.Fatalf("detour = %q，期望 proxy", server.Detour)
	}
}

// 可配置：DoT 上游要生成 tls 类型且不带 DoH 路径
func TestCompatibleRemoteDNSConfigurableDoT(t *testing.T) {
	server := findRemoteDNSServer(t, setupCompatibleDNS(t, "tls://1.1.1.1:8853"))
	if server.Type != "tls" || server.Server != "1.1.1.1" || server.ServerPort != 8853 {
		t.Fatalf("remote 解析器 = %+v，期望 tls://1.1.1.1:8853", server)
	}
	if server.Path != "" {
		t.Fatalf("DoT 不应带 path，实际 %q", server.Path)
	}
	if server.Detour != "proxy" {
		t.Fatalf("detour = %q，期望 proxy", server.Detour)
	}
}

// 域名上游需要 bootstrap 解析
func TestCompatibleRemoteDNSDomainHost(t *testing.T) {
	server := findRemoteDNSServer(t, setupCompatibleDNS(t, "https://dns.google/dns-query"))
	if server.Server != "dns.google" {
		t.Fatalf("server = %q，期望 dns.google", server.Server)
	}
	if server.DomainResolver != "bootstrap" {
		t.Fatalf("domain_resolver = %q，期望 bootstrap", server.DomainResolver)
	}
}

// 明文解析器直连，不套 detour
func TestCompatibleRemoteDNSUDPHasNoDetour(t *testing.T) {
	server := findRemoteDNSServer(t, setupCompatibleDNS(t, "udp://223.5.5.5"))
	if server.Type != "udp" || server.Server != "223.5.5.5" {
		t.Fatalf("remote 解析器 = %+v，期望 udp://223.5.5.5", server)
	}
	if server.Detour != "" {
		t.Fatalf("明文直连解析器不应带 detour，实际 %q", server.Detour)
	}
}

// 回退转发器可用时 remote 指向本机，且不能套 detour（否则本机回环查询会被送进节点）
func TestCompatibleRemoteDNSPointsToFallbackForwarder(t *testing.T) {
	setting := setupCompatibleDNS(t, "https://dns.google/dns-query")
	dnsFallbackPort.Store(20263)
	t.Cleanup(func() { dnsFallbackPort.Store(0) })

	server := findRemoteDNSServer(t, setting)
	if server.Type != "udp" || server.Server != dnsFallbackListenHost || server.ServerPort != 20263 {
		t.Fatalf("remote 解析器 = %+v，期望指向本地回退转发器 %s:20263", server, dnsFallbackListenHost)
	}
	if server.Detour != "" {
		t.Fatalf("本地回退转发器不应带 detour，实际 %q", server.Detour)
	}
	if server.DomainResolver != "" {
		t.Fatalf("本地回退转发器不需要 domain_resolver，实际 %q", server.DomainResolver)
	}
}

// 轻量模式不受远端解析器配置影响
func TestLightweightDNSIgnoresRemoteServer(t *testing.T) {
	setting := setupCompatibleDNS(t, "tls://1.1.1.1")
	setting.DNSMode = "lightweight"

	server := findRemoteDNSServer(t, setting)
	if server.Type != "udp" || server.Server != "1.1.1.1" {
		t.Fatalf("轻量模式 remote = %+v，期望 udp://1.1.1.1", server)
	}
}

// 非法配置值退回默认解析器，而不是让内核起不来
func TestCompatibleRemoteDNSInvalidFallsBackToDefault(t *testing.T) {
	server := findRemoteDNSServer(t, setupCompatibleDNS(t, "ftp://8.8.8.8"))
	if server.Server != "8.8.8.8" || server.Type != "https" {
		t.Fatalf("非法配置应退回默认解析器，实际 %+v", server)
	}
}

func TestSettingDNSRemoteServerRaw(t *testing.T) {
	setting := &configure.Setting{}
	if setting.DNSRemoteServerRaw() != configure.DNSRemoteServerDefault {
		t.Fatalf("空配置应返回默认值，实际 %q", setting.DNSRemoteServerRaw())
	}
	setting.DNSRemoteServer = "  tls://1.1.1.1  "
	if setting.DNSRemoteServerRaw() != "tls://1.1.1.1" {
		t.Fatalf("应去掉首尾空白，实际 %q", setting.DNSRemoteServerRaw())
	}
}
