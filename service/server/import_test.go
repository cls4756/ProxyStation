package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClassifyHTTPURL(t *testing.T) {
	cases := []struct {
		in   string
		want urlKind
	}{
		{"vmess://abc", urlKindNotHTTP},
		{"https://example.com/subscribe/xxx", urlKindSubscription},
		{"http://user:pass@1.2.3.4:8080", urlKindProxyNode},
		{"http://user:pass@1.2.3.4:8080/", urlKindProxyNode},
		// 裸 host:port —— 订阅和 HTTP 代理节点在 URL 层面完全同形
		{"http://43.166.0.237:8080/", urlKindAmbiguous},
		{"http://43.166.0.237:8080", urlKindAmbiguous},
	}
	for _, c := range cases {
		if got := classifyHTTPURL(c.in); got != c.want {
			t.Errorf("classifyHTTPURL(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

const clashProfile = `mixed-port: 7890
proxies:
  - name: "Hysteria2"
    type: hysteria2
    server: 43.166.0.237
    port: 443
    password: "secret"
`

func TestLooksLikeSubscription(t *testing.T) {
	t.Run("clash yaml on bare host:port is a subscription", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/x-yaml")
			_, _ = w.Write([]byte(clashProfile))
		}))
		defer srv.Close()
		if !looksLikeSubscription(srv.URL + "/") {
			t.Error("expected clash yaml endpoint to be detected as subscription")
		}
	})

	t.Run("non-subscription response falls back to node", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("hello"))
		}))
		defer srv.Close()
		if looksLikeSubscription(srv.URL + "/") {
			t.Error("expected plain text endpoint not to be detected as subscription")
		}
	})

	t.Run("error status falls back to node", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusProxyAuthRequired)
		}))
		defer srv.Close()
		if looksLikeSubscription(srv.URL + "/") {
			t.Error("expected 407 to be treated as proxy node, not subscription")
		}
	})

	t.Run("unreachable host falls back to node", func(t *testing.T) {
		// 127.0.0.1:1 上没有监听，连接会立刻失败
		if looksLikeSubscription("http://127.0.0.1:1/") {
			t.Error("expected unreachable host to fall back to node handling")
		}
	})
}
