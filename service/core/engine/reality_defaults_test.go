package engine

import (
	"testing"
)

// TestSbTLSRealityForcesUTLS 验证 REALITY 出站在缺省 fingerprint 时自动补全 uTLS。
// sing-box 硬性要求 REALITY 客户端必须启用 uTLS，否则初始化报 uTLS is required by reality client。
func TestSbTLSRealityForcesUTLS(t *testing.T) {
	tests := []struct {
		name       string
		fp         string
		isReality  bool
		wantUTLS   bool
		wantFP     string
		wantReality bool
	}{
		{
			name:       "reality without fingerprint falls back to chrome",
			fp:         "",
			isReality:  true,
			wantUTLS:   true,
			wantFP:     "chrome",
			wantReality: true,
		},
		{
			name:       "reality keeps explicit fingerprint",
			fp:         "firefox",
			isReality:  true,
			wantUTLS:   true,
			wantFP:     "firefox",
			wantReality: true,
		},
		{
			name:       "non-reality without fingerprint has no utls",
			fp:         "",
			isReality:  false,
			wantUTLS:   false,
			wantReality: false,
		},
		{
			name:       "non-reality keeps explicit fingerprint",
			fp:         "chrome",
			isReality:  false,
			wantUTLS:   true,
			wantFP:     "chrome",
			wantReality: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tls := sbTLS(true, "example.com", tt.fp, "", "pbk", "sid", tt.isReality)
			if tls == nil {
				t.Fatal("sbTLS() = nil, want non-nil")
			}
			if tt.wantUTLS {
				if tls.UTLS == nil || !tls.UTLS.Enabled {
					t.Fatalf("sbTLS() UTLS = %#v, want enabled", tls.UTLS)
				}
				if tls.UTLS.Fingerprint != tt.wantFP {
					t.Fatalf("sbTLS() UTLS.Fingerprint = %q, want %q", tls.UTLS.Fingerprint, tt.wantFP)
				}
			} else if tls.UTLS != nil {
				t.Fatalf("sbTLS() UTLS = %#v, want nil", tls.UTLS)
			}
			if tt.wantReality != (tls.Reality != nil) {
				t.Fatalf("sbTLS() Reality = %#v, wantReality = %v", tls.Reality, tt.wantReality)
			}
		})
	}
}

// TestNodeTLSRealityForcesUTLS 验证 clash reality-opts 路径在缺 client-fingerprint 时同样补全 uTLS。
func TestNodeTLSRealityForcesUTLS(t *testing.T) {
	m := map[string]interface{}{
		"tls": true,
		"sni": "example.com",
		"reality-opts": map[string]interface{}{
			"public-key": "pbk",
			"short-id":   "sid",
		},
	}
	tls := nodeTLS(m, "example.com", false)
	if tls == nil {
		t.Fatal("nodeTLS() = nil, want non-nil")
	}
	if tls.Reality == nil || !tls.Reality.Enabled {
		t.Fatalf("nodeTLS() Reality = %#v, want enabled", tls.Reality)
	}
	if tls.UTLS == nil || !tls.UTLS.Enabled || tls.UTLS.Fingerprint != "chrome" {
		t.Fatalf("nodeTLS() UTLS = %#v, want enabled chrome", tls.UTLS)
	}
}

// TestNodeTLSRealityKeepsFingerprint 验证已有 client-fingerprint 时不被 chrome 覆盖。
func TestNodeTLSRealityKeepsFingerprint(t *testing.T) {
	m := map[string]interface{}{
		"tls":                true,
		"sni":                "example.com",
		"client-fingerprint": "safari",
		"reality-opts": map[string]interface{}{
			"public-key": "pbk",
			"short-id":   "sid",
		},
	}
	tls := nodeTLS(m, "example.com", false)
	if tls == nil || tls.UTLS == nil {
		t.Fatalf("nodeTLS() = %#v, want non-nil with UTLS", tls)
	}
	if tls.UTLS.Fingerprint != "safari" {
		t.Fatalf("nodeTLS() UTLS.Fingerprint = %q, want safari", tls.UTLS.Fingerprint)
	}
}

// TestSbTransportPathDefaults 验证 ws / http / httpupgrade 缺省 path 补全为 "/"。
func TestSbTransportPathDefaults(t *testing.T) {
	tests := []struct {
		name     string
		network  string
		path     string
		wantPath string
	}{
		{"ws empty path", "ws", "", "/"},
		{"ws explicit path", "ws", "/ray", "/ray"},
		{"http empty path", "http", "", "/"},
		{"h2 empty path", "h2", "", "/"},
		{"httpupgrade empty path", "httpupgrade", "", "/"},
		{"httpupgrade explicit path", "httpupgrade", "/up", "/up"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := sbTransport(tt.network, tt.path, "", "")
			if tr == nil {
				t.Fatalf("sbTransport(%q) = nil, want non-nil", tt.network)
			}
			if tr.Path != tt.wantPath {
				t.Fatalf("sbTransport(%q).Path = %q, want %q", tt.network, tr.Path, tt.wantPath)
			}
		})
	}
}

// TestSbTransportGrpcNoPath 验证 grpc 不受 path 补全影响。
func TestSbTransportGrpcNoPath(t *testing.T) {
	tr := sbTransport("grpc", "", "", "mysvc")
	if tr == nil {
		t.Fatal("sbTransport(grpc) = nil, want non-nil")
	}
	if tr.Path != "" {
		t.Fatalf("sbTransport(grpc).Path = %q, want empty", tr.Path)
	}
	if tr.ServiceName != "mysvc" {
		t.Fatalf("sbTransport(grpc).ServiceName = %q, want mysvc", tr.ServiceName)
	}
}
