package engine

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ProxyStation/proxystation/db/configure"
)

func TestParseShadowsocksEndpoint(t *testing.T) {
	tests := []struct {
		name   string
		server configure.ServerRaw
		want   shadowsocksEndpoint
	}{
		{
			name: "clash internal link",
			server: configure.ServerRaw{
				Type: "ss",
				Name: "US",
				Host: "fallback.example",
				Port: 1111,
				Link: internalNodeLink("clash", map[string]interface{}{
					"type":     "ss",
					"name":     "US",
					"server":   "clash.example",
					"port":     8388,
					"cipher":   "chacha20-ietf-poly1305",
					"password": "secret",
				}),
			},
			want: shadowsocksEndpoint{
				Host: "clash.example", Port: 8388,
				Method: "chacha20-ietf-poly1305", Password: "secret",
			},
		},
		{
			name: "singbox internal link",
			server: configure.ServerRaw{
				Type: "ss",
				Name: "US",
				Link: internalNodeLink("singbox", map[string]interface{}{
					"type":        "shadowsocks",
					"tag":         "US",
					"server":      "singbox.example",
					"server_port": 443,
					"method":      "aes-128-gcm",
					"password":    "secret",
				}),
			},
			want: shadowsocksEndpoint{
				Host: "singbox.example", Port: 443,
				Method: "aes-128-gcm", Password: "secret",
			},
		},
		{
			name: "userinfo base64 uri",
			server: configure.ServerRaw{
				Type: "ss",
				Link: "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:pass")) + "@ss.example:8389#SS",
			},
			want: shadowsocksEndpoint{
				Host: "ss.example", Port: 8389,
				Method: "aes-256-gcm", Password: "pass",
			},
		},
		{
			name: "whole payload base64 uri",
			server: configure.ServerRaw{
				Type: "ss",
				Link: "ss://" + base64.StdEncoding.EncodeToString([]byte("2022-blake3-aes-128-gcm:pass@full.example:8443")) + "#SS",
			},
			want: shadowsocksEndpoint{
				Host: "full.example", Port: 8443,
				Method: "2022-blake3-aes-128-gcm", Password: "pass",
			},
		},
		{
			name: "plain uri with escaped password",
			server: configure.ServerRaw{
				Type: "ss",
				Link: "ss://aes-128-gcm:pa%3Ass@plain.example:8388/?plugin=obfs-local#SS",
			},
			want: shadowsocksEndpoint{
				Host: "plain.example", Port: 8388,
				Method: "aes-128-gcm", Password: "pa:ss",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseShadowsocksEndpoint(&tt.server)
			if err != nil {
				t.Fatalf("parseShadowsocksEndpoint() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseShadowsocksEndpoint() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSingboxShadowsocksOutboundUsesParsedEndpoint(t *testing.T) {
	server := configure.ServerRaw{
		Type: "ss",
		Name: "US",
		Link: internalNodeLink("clash", map[string]interface{}{
			"type":     "ss",
			"name":     "US",
			"server":   "clash.example",
			"port":     8388,
			"cipher":   "chacha20-ietf-poly1305",
			"password": "secret",
		}),
	}

	got, err := sbSS(&server, "US")
	if err != nil {
		t.Fatalf("sbSS() error = %v", err)
	}
	if got.Type != "shadowsocks" || got.Tag != "US" ||
		got.Server != "clash.example" || got.ServerPort != 8388 ||
		got.Method != "chacha20-ietf-poly1305" || got.Password != "secret" {
		t.Fatalf("sbSS() = %#v", got)
	}
}

func TestParseShadowsocksEndpointRejectsIncompleteNode(t *testing.T) {
	server := configure.ServerRaw{
		Type: "ss",
		Name: "broken",
		Link: internalNodeLink("clash", map[string]interface{}{
			"type":   "ss",
			"server": "clash.example",
			"port":   8388,
		}),
	}

	if _, err := parseShadowsocksEndpoint(&server); err == nil {
		t.Fatal("parseShadowsocksEndpoint() error = nil, want error")
	}
}

func internalNodeLink(kind string, node map[string]interface{}) string {
	data, err := json.Marshal(node)
	if err != nil {
		panic(err)
	}
	return kind + "://" + base64.StdEncoding.EncodeToString(data)
}
