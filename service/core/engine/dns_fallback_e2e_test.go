package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// 端到端验证：真实 sing-box 通过本地回退转发器解析，主上游故意失败时走兜底。
//
// 依赖本机 sing-box 二进制与外网 DNS，默认跳过；需要时设置
// PROXYSTATION_E2E_SINGBOX=1 运行。
func TestE2EDNSFallbackWithRealSingbox(t *testing.T) {
	if os.Getenv("PROXYSTATION_E2E_SINGBOX") == "" {
		t.Skip("未设置 PROXYSTATION_E2E_SINGBOX，跳过端到端验证")
	}
	bin := os.Getenv("PROXYSTATION_SINGBOX_BIN")
	if bin == "" {
		bin = filepath.Join(DataDir(), "bin", "sing-box")
	}
	if _, err := os.Stat(bin); err != nil {
		if looked, lookErr := exec.LookPath("sing-box"); lookErr == nil {
			bin = looked
		} else {
			t.Skipf("找不到 sing-box 二进制: %v", err)
		}
	}

	primaryFail := dnsQueryFunc(func(ctx context.Context, query []byte) ([]byte, error) {
		return nil, errors.New("模拟节点侧远端解析失败")
	})
	server := newDNSFallbackServer(primaryFail, directUDPQueryFunc(dnsFallbackBootstrapAddr), func(msg string) { t.Log(msg) })
	if err := server.start(); err != nil {
		t.Fatalf("start forwarder: %v", err)
	}
	defer server.Close()
	port := server.port()
	t.Logf("forwarder listening on 127.0.0.1:%d", port)

	apiPort := freeTCPPort(t)
	config := map[string]any{
		"log": map[string]any{"level": "debug", "timestamp": true},
		"dns": map[string]any{
			"servers": []any{map[string]any{"type": "udp", "tag": "remote", "server": "127.0.0.1", "server_port": port}},
			"final":   "remote",
		},
		"inbounds":     []any{map[string]any{"type": "socks", "tag": "in", "listen": "127.0.0.1", "listen_port": freeTCPPort(t)}},
		"outbounds":    []any{map[string]any{"type": "direct", "tag": "direct"}},
		"route":        map[string]any{"auto_detect_interface": true, "final": "direct"},
		"experimental": map[string]any{"clash_api": map[string]any{"external_controller": fmt.Sprintf("127.0.0.1:%d", apiPort)}},
	}
	raw, _ := json.MarshalIndent(config, "", "  ")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := exec.Command(bin, "run", "-D", dir, "-c", configPath)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sing-box: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		response, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/dns/query?name=example.com&type=A", apiPort))
		if err != nil {
			lastErr = err
			time.Sleep(200 * time.Millisecond)
			continue
		}
		defer response.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if status, _ := payload["Status"].(float64); status != 0 {
			t.Fatalf("期望 NOERROR，实际 %v", payload)
		}
		answers, _ := payload["Answer"].([]any)
		if len(answers) == 0 {
			t.Fatalf("没有解析结果：%v", payload)
		}
		t.Logf("经兜底解析成功：%v", answers)
		return
	}
	t.Fatalf("sing-box 未在超时内提供解析服务: %v", lastErr)
}
