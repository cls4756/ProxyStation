// process.go 负责启动/停止 v2ray-core 子进程，管理 config.json 写入
// 参考 v2rayA/service/core/v2ray/process.go，大幅简化
package v2ray

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/ProxyStation/proxystation/db/configure"
)

var Manager = &ProcessManager{}

type ProcessManager struct {
	mu      sync.Mutex
	proc    *os.Process
	cancel  context.CancelFunc
	dataDir string
}

func Init(dataDir string) {
	Manager.dataDir = dataDir
}

// Start 生成 config.json 并启动 v2ray-core
func (pm *ProcessManager) Start() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// 先停掉旧进程
	pm.stopLocked()

	setting := configure.GetSettingNotNil()
	cfg, err := BuildConfig(setting)
	if err != nil {
		return fmt.Errorf("build config: %w", err)
	}

	configPath := filepath.Join(pm.dataDir, "config.json")
	if err := writeConfig(cfg, configPath); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	v2rayBin, err := findV2rayBin()
	if err != nil {
		return fmt.Errorf("v2ray binary not found: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, v2rayBin, "run", "--config="+configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start v2ray: %w", err)
	}

	pm.proc = cmd.Process
	pm.cancel = cancel

	// 等待 socks5 端口就绪（最多 10s）
	socks5Port := setting.Socks5Port
	if err := waitPort(fmt.Sprintf("127.0.0.1:%d", socks5Port), 10*time.Second); err != nil {
		pm.stopLocked()
		return fmt.Errorf("v2ray did not start in time: %w", err)
	}

	_ = configure.SetRunning(true)
	go func() {
		_ = cmd.Wait()
		pm.mu.Lock()
		pm.proc = nil
		pm.cancel = nil
		pm.mu.Unlock()
		_ = configure.SetRunning(false)
	}()

	return nil
}

// Stop 停止 v2ray-core
func (pm *ProcessManager) Stop() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.stopLocked()
	_ = configure.SetRunning(false)
}

func (pm *ProcessManager) stopLocked() {
	if pm.cancel != nil {
		pm.cancel()
		pm.cancel = nil
	}
	if pm.proc != nil {
		_ = pm.proc.Kill()
		pm.proc = nil
	}
}

// Restart 重新生成配置并重启（出站变更时调用）
func (pm *ProcessManager) Restart() error {
	if !configure.GetRunning() {
		return nil
	}
	return pm.Start()
}

func writeConfig(cfg interface{}, path string) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}

// findV2rayBin 在 PATH 和常见位置查找 v2ray/xray 可执行文件
func findV2rayBin() (string, error) {
	candidates := []string{"v2ray", "xray", "v2ray.exe", "xray.exe"}
	for _, name := range candidates {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	// 常见安装路径
	commonPaths := []string{
		"/usr/local/bin/v2ray",
		"/usr/bin/v2ray",
		"/usr/local/bin/xray",
		"/usr/bin/xray",
	}
	for _, p := range commonPaths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("v2ray/xray not found in PATH or common locations")
}

func waitPort(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", addr)
}
