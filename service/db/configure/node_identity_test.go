package configure

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func encodedIdentityTestLink(t *testing.T, scheme string, config map[string]any) string {
	t.Helper()
	content, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	return scheme + "://" + base64.StdEncoding.EncodeToString(content)
}

func TestNodeConfigFingerprintIgnoresDisplayNamesAndMetadata(t *testing.T) {
	clash := func(name, password string) string {
		return encodedIdentityTestLink(t, "clash", map[string]any{
			"name": name, "type": "trojan", "server": "proxy.example", "port": 443,
			"password": password, "sni": "tls.example", "network": "ws",
			"ws-opts": map[string]any{"path": "/proxy", "headers": map[string]any{"Host": "ws.example"}},
		})
	}
	singbox := func(tag string) string {
		return encodedIdentityTestLink(t, "singbox", map[string]any{
			"tag": tag, "type": "vless", "server": "proxy.example", "server_port": 443,
			"uuid": "credential-a", "tls": map[string]any{"enabled": true, "server_name": "tls.example"},
		})
	}
	vmess := func(name string, port any) string {
		return encodedIdentityTestLink(t, "vmess", map[string]any{
			"ps": name, "add": "proxy.example", "port": port, "id": "credential-a",
			"aid": "0", "v": "2", "net": "ws", "path": "/proxy", "tls": "tls",
		})
	}
	ssr := func(name string) string {
		payload := "proxy.example:443:origin:aes-128-gcm:plain:cGFzcw/?remarks=" + name + "&group=ignored&obfsparam=param"
		return "ssr://" + base64.RawURLEncoding.EncodeToString([]byte(payload))
	}
	cases := []struct {
		name  string
		first string
		last  string
	}{
		{"URI names and query order", "vless://credential-a@proxy.example:443?security=tls&type=ws#first", "vless://credential-a@PROXY.EXAMPLE:443?type=ws&security=tls#second"},
		{"IPv6 URI representations", "vless://credential-a@[2001:db8::1]:443#first", "vless://credential-a@[2001:db8:0:0:0:0:0:1]:443#second"},
		{"Clash names", clash("first", "credential-a"), clash("second", "credential-a")},
		{"Sing-box tags", singbox("first"), singbox("second")},
		{"VMess names and numeric encoding", vmess("first", "443"), vmess("second", 443)},
		{"SSR names", ssr("Zmlyc3Q"), ssr("c2Vjb25k")},
		{"Shadowsocks encodings", "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:pass@proxy.example:443")) + "#first",
			"ss://" + base64.RawStdEncoding.EncodeToString([]byte("aes-128-gcm:pass")) + "@proxy.example:443#second"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			score := 90
			first := ServerRaw{Link: testCase.first, Name: "first", Host: "proxy.example", Port: 443, Type: "vless"}
			last := ServerRaw{
				Link: testCase.last, Name: "second", Host: "PROXY.EXAMPLE", Port: 443, Type: "VLESS",
				Source: "subscription", Latency: 90, LastProbeTime: 100, FastLatency: 30,
				IPType: "residential", IPScore: &score, IPGeoCountry: "美国",
			}
			firstKey, firstValid := NodeConfigFingerprint(first)
			lastKey, lastValid := NodeConfigFingerprint(last)
			if !firstValid || !lastValid || firstKey != lastKey {
				t.Fatal("display-only differences must not change the connection identity")
			}
		})
	}
}

func TestNodeConfigFingerprintPreservesConnectionDifferences(t *testing.T) {
	first := ServerRaw{
		Link: "vless://credential-a@proxy.example:443?security=tls&type=ws&path=%2Ffirst",
		Host: "proxy.example", Port: 443, Type: "vless",
	}
	cases := []struct {
		name   string
		change func(*ServerRaw)
	}{
		{"credentials", func(server *ServerRaw) {
			server.Link = "vless://credential-b@proxy.example:443?security=tls&type=ws&path=%2Ffirst"
		}},
		{"transport", func(server *ServerRaw) {
			server.Link = "vless://credential-a@proxy.example:443?security=tls&type=ws&path=%2Fsecond"
		}},
		{"TLS", func(server *ServerRaw) {
			server.Link = "vless://credential-a@proxy.example:443?security=none&type=ws&path=%2Ffirst"
		}},
		{"front proxy", func(server *ServerRaw) { server.FrontProxy = "trans-proxy" }},
		{"port", func(server *ServerRaw) { server.Port = 8443 }},
	}
	firstKey, _ := NodeConfigFingerprint(first)
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			last := first
			testCase.change(&last)
			lastKey, valid := NodeConfigFingerprint(last)
			if !valid || firstKey == lastKey {
				t.Fatal("different connection configurations must not be merged")
			}
		})
	}
}

func TestNodeConfigFingerprintPreservesEmbeddedOptions(t *testing.T) {
	for _, scheme := range []string{"clash", "singbox", "vmess"} {
		t.Run(scheme, func(t *testing.T) {
			config := map[string]any{
				"name": "node", "tag": "node", "ps": "node", "server": "proxy.example",
				"port": 443, "password": "credential-a",
				"transport": map[string]any{"type": "ws", "path": "/first"},
			}
			first := ServerRaw{Link: encodedIdentityTestLink(t, scheme, config)}
			firstKey, _ := NodeConfigFingerprint(first)
			config["password"] = "credential-b"
			lastKey, _ := NodeConfigFingerprint(ServerRaw{Link: encodedIdentityTestLink(t, scheme, config)})
			if firstKey == lastKey {
				t.Fatal("embedded credentials must remain part of the fingerprint")
			}
			config["password"] = "credential-a"
			config["transport"] = map[string]any{"type": "ws", "path": "/second"}
			lastKey, _ = NodeConfigFingerprint(ServerRaw{Link: encodedIdentityTestLink(t, scheme, config)})
			if firstKey == lastKey {
				t.Fatal("embedded transport options must remain part of the fingerprint")
			}
		})
	}
}

func TestNodeConfigFingerprintRejectsIncompleteCredentialProtocols(t *testing.T) {
	for _, nodeType := range []string{"", "vmess", "vless", "trojan", "ss", "hysteria2"} {
		if _, valid := NodeConfigFingerprint(ServerRaw{Type: nodeType, Host: "proxy.example", Port: 443}); valid {
			t.Fatalf("missing connection credentials for %q must not fall back to address-only identity", nodeType)
		}
	}
	first, valid := NodeConfigFingerprint(ServerRaw{Type: "socks", Host: "[2001:db8::1]", Port: 1080})
	last, _ := NodeConfigFingerprint(ServerRaw{Type: "socks5", Host: "2001:db8:0:0:0:0:0:1", Port: 1080})
	if !valid || first != last {
		t.Fatal("plain SOCKS configuration and IP/type aliases should be normalized")
	}
	first, _ = NodeConfigFingerprint(ServerRaw{Link: "socks://[fe80::1%25eth0]:1080"})
	last, _ = NodeConfigFingerprint(ServerRaw{Link: "socks://[fe80::1%25ETH0]:1080"})
	if first == last {
		t.Fatal("case-sensitive IPv6 interface identifiers must remain distinct")
	}
}

func TestNodeConfigFingerprintPreservesEncodedURLPaths(t *testing.T) {
	first, _ := NodeConfigFingerprint(ServerRaw{Link: "https://proxy.example/first%2Fsecond"})
	last, _ := NodeConfigFingerprint(ServerRaw{Link: "https://proxy.example/first/second"})
	if first == last {
		t.Fatal("encoded reserved path characters must not be decoded for connection identity")
	}
}
