// engine 包负责根据节点协议自动选择合适的内核（v2ray/xray/sing-box）
// 并管理内核进程的生命周期
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ProxyStation/proxystation/db/configure"
)

// KernelType 内核类型
type KernelType string

const (
	KernelV2ray   KernelType = "v2ray"
	KernelXray    KernelType = "xray"
	KernelSingbox KernelType = "singbox"
)

// ProtocolKernelMap 协议 -> 首选内核
// v2ray/xray 都支持的协议优先用 xray（功能更全）
// hysteria2/tuic/wireguard/anytls/naive/juicity 只有 sing-box 支持
var ProtocolKernelMap = map[string]KernelType{
	"vmess":       KernelXray,
	"vless":       KernelXray,
	"ss":          KernelXray,
	"shadowsocks": KernelXray,
	"ssr":         KernelXray,
	"trojan":      KernelXray,
	"socks5":      KernelXray,
	"socks4":      KernelXray,
	"socks":       KernelXray,
	"http":        KernelXray,
	"https":       KernelXray,
	"hysteria2":   KernelSingbox,
	"hy2":         KernelSingbox,
	"hysteria":    KernelSingbox,
	"tuic":        KernelSingbox,
	"wireguard":   KernelSingbox,
	"naive":       KernelSingbox,
	"anytls":      KernelSingbox,
	"juicity":     KernelSingbox,
}

// Manager 全局引擎管理器
var Manager = &EngineManager{}

type EngineManager struct {
	mu      sync.Mutex
	proc    *os.Process
	cancel  context.CancelFunc
	kernel  KernelType
	dataDir string
	running bool  // 添加运行状态标志
}

func Init(dataDir string) {
	Manager.dataDir = dataDir
}

// Start 根据当前出站节点协议选择内核并启动
func (em *EngineManager) Start() error {
	em.mu.Lock()
	defer em.mu.Unlock()
	
	// 停止旧进程
	if em.cancel != nil {
		em.cancel()
		// 等待旧进程的 goroutine 完成，避免竞态条件
		time.Sleep(100 * time.Millisecond)
	}
	if em.proc != nil {
		_ = em.proc.Kill()
		em.proc = nil
	}
	em.cancel = nil

	// 检查是否有可用的出站配置
	if !em.hasAnyOutbound() {
		em.running = false
		_ = configure.SetRunning(false)
		return fmt.Errorf("未配置任何出站节点，请先在节点页面选择节点或在出站下拉中配置分组")
	}

	setting := configure.GetSettingNotNil()

	// 确定需要哪个内核
	kernel := em.selectKernel(setting)
	em.kernel = kernel

	var configPath string
	var binPath string
	var args []string
	var err error

	switch kernel {
	case KernelSingbox:
		binPath, err = findBin([]string{"sing-box", "sing-box.exe"}, []string{
			"/usr/local/bin/sing-box", "/usr/bin/sing-box",
		})
		if err != nil {
			em.running = false
			_ = configure.SetRunning(false)
			return fmt.Errorf("sing-box not found: %w", err)
		}
		configPath = filepath.Join(em.dataDir, "singbox.json")
		cfg, e := BuildSingboxConfig(setting)
		if e != nil {
			em.running = false
			_ = configure.SetRunning(false)
			return fmt.Errorf("build singbox config: %w", e)
		}
		// 打印配置到日志
		if cfgJSON, err := json.MarshalIndent(cfg, "", "  "); err == nil {
			if LogCallback != nil {
				LogCallback(fmt.Sprintf("📋 Singbox 配置:\n%s", string(cfgJSON)))
			}
		}
		if e := writeJSON(cfg, configPath); e != nil {
			em.running = false
			_ = configure.SetRunning(false)
			return fmt.Errorf("write singbox config: %w", e)
		}
		args = []string{binPath, "run", "-c", configPath}

	case KernelXray:
		binPath, err = findBin([]string{"xray", "xray.exe"}, []string{
			"/usr/local/bin/xray", "/usr/bin/xray",
		})
		if err != nil {
			if setting.KernelMode == "xray" {
				em.running = false
				_ = configure.SetRunning(false)
				return fmt.Errorf("xray not found: %w", err)
			}
			// auto mode fallback to v2ray
			binPath, err = findBin([]string{"v2ray", "v2ray.exe"}, []string{
				"/usr/local/bin/v2ray", "/usr/bin/v2ray",
			})
			if err != nil {
				em.running = false
				_ = configure.SetRunning(false)
				return fmt.Errorf("xray/v2ray not found")
			}
			em.kernel = KernelV2ray
		}
		configPath = filepath.Join(em.dataDir, "config.json")
		cfg, e := BuildXrayConfig(setting)
		if e != nil {
			em.running = false
			_ = configure.SetRunning(false)
			return fmt.Errorf("build xray config: %w", e)
		}
		// 打印配置到日志
		if cfgJSON, err := json.MarshalIndent(cfg, "", "  "); err == nil {
			if LogCallback != nil {
				LogCallback(fmt.Sprintf("📋 Xray 配置:\n%s", string(cfgJSON)))
			}
		}
		if e := writeJSON(cfg, configPath); e != nil {
			em.running = false
			_ = configure.SetRunning(false)
			return fmt.Errorf("write xray config: %w", e)
		}
		args = []string{binPath, "run", "--config=" + configPath}

	case KernelV2ray:
		binPath, err = findBin([]string{"v2ray", "v2ray.exe"}, []string{
			"/usr/local/bin/v2ray", "/usr/bin/v2ray",
		})
		if err != nil {
			em.running = false
			_ = configure.SetRunning(false)
			return fmt.Errorf("v2ray not found: %w", err)
		}
		configPath = filepath.Join(em.dataDir, "config.json")
		cfg, e := BuildXrayConfig(setting)
		if e != nil {
			em.running = false
			_ = configure.SetRunning(false)
			return fmt.Errorf("build v2ray config: %w", e)
		}
		if cfgJSON, err := json.MarshalIndent(cfg, "", "  "); err == nil {
			if LogCallback != nil {
				LogCallback(fmt.Sprintf("📋 V2Ray 配置:\n%s", string(cfgJSON)))
			}
		}
		if e := writeJSON(cfg, configPath); e != nil {
			em.running = false
			_ = configure.SetRunning(false)
			return fmt.Errorf("write v2ray config: %w", e)
		}
		args = []string{binPath, "run", "--config=" + configPath}

	default:
		em.running = false
		_ = configure.SetRunning(false)
		return fmt.Errorf("unknown kernel: %v", kernel)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	
	// 创建多路输出：既输出到控制台，也转发到日志系统
	cmd.Stdout = &logWriter{}
	cmd.Stderr = &logWriter{}

	if err := cmd.Start(); err != nil {
		cancel()
		em.running = false
		_ = configure.SetRunning(false)
		return fmt.Errorf("start %v: %w", kernel, explainKernelStartError(binPath, err))
	}

	em.proc = cmd.Process
	em.cancel = cancel

	// 等待 socks5 端口就绪（首次启动需要下载 rule-set，给更长时间）
	addr := fmt.Sprintf("127.0.0.1:%d", setting.Socks5Port)
	if err := waitPort(addr, 60*time.Second); err != nil {
		// 启动失败，清理进程
		if em.cancel != nil {
			em.cancel()
			em.cancel = nil
		}
		if em.proc != nil {
			_ = em.proc.Kill()
			em.proc = nil
		}
		em.running = false
		_ = configure.SetRunning(false)
		return fmt.Errorf("%v did not start in time: %w", kernel, err)
	}

	// 只有在端口就绪后才设置运行状态
	em.running = true
	_ = configure.SetRunning(true)
	
	// 启动 goroutine 监听进程退出
	// 使用局部变量保存当前进程，避免竞态条件
	currentProc := em.proc
	go func() {
		_ = cmd.Wait()
		em.mu.Lock()
		defer em.mu.Unlock()
		// 只有当进程还是当前进程时才清理状态
		// 避免旧进程的 goroutine 覆盖新进程的状态
		if em.proc == currentProc {
			em.proc = nil
			em.cancel = nil
			em.running = false
			_ = configure.SetRunning(false)
			if LogCallback != nil {
				LogCallback("⚠️ 进程意外退出")
			}
		}
	}()

	return nil
}

func (em *EngineManager) Stop() {
	em.mu.Lock()
	defer em.mu.Unlock()
	em.stopLocked()
	em.running = false
	_ = configure.SetRunning(false)
}

func (em *EngineManager) stopLocked() {
	if em.cancel != nil {
		em.cancel()
		em.cancel = nil
	}
	if em.proc != nil {
		_ = em.proc.Kill()
		em.proc = nil
	}
}

func (em *EngineManager) Restart() error {
	// 检查实际的进程状态而不是数据库状态
	if !em.IsRunning() {
		if LogCallback != nil {
			LogCallback("⚠️ 代理未运行，跳过重启")
		}
		return nil
	}
	if LogCallback != nil {
		LogCallback("🔄 开始重启代理...")
	}
	if err := em.Start(); err != nil {
		// 重启失败时，确保状态被设置为未运行
		_ = configure.SetRunning(false)
		if LogCallback != nil {
			LogCallback(fmt.Sprintf("❌ 重启失败: %v", err))
		}
		return err
	}
	if LogCallback != nil {
		LogCallback("✅ 代理重启成功")
	}
	return nil
}

// IsRunning 检查实际的进程状态（不依赖数据库）
func (em *EngineManager) IsRunning() bool {
	em.mu.Lock()
	defer em.mu.Unlock()
	return em.running
}

func (em *EngineManager) CurrentKernel() KernelType {
	em.mu.Lock()
	defer em.mu.Unlock()
	return em.kernel
}

// hasAnyOutbound 检查是否有至少一个出站配置了有效节点
func (em *EngineManager) hasAnyOutbound() bool {
	for _, name := range configure.GetOutboundNames() {
		o := configure.GetOutbound(name)
		if o == nil {
			continue
		}
		ref := resolveActiveNode(o)
		if ref != nil {
			return true
		}
	}
	return false
}
// 如果有任何节点需要 sing-box，就用 sing-box（它也支持 vmess/vless/ss/trojan）
// 如果 sing-box 已安装，优先使用它（协议支持更广）；否则用 xray
func (em *EngineManager) selectKernel(setting *configure.Setting) KernelType {
	switch setting.KernelMode {
	case "singbox":
		return KernelSingbox
	case "xray":
		return KernelXray
	case "v2ray":
		return KernelV2ray
	}

	outboundNames := configure.GetOutboundNames()
	needSingbox := false
	for _, name := range outboundNames {
		o := configure.GetOutbound(name)
		if o == nil {
			continue
		}
		ref := resolveActiveNode(o)
		if ref == nil {
			continue
		}
		s := getServerRaw(ref)
		if s == nil {
			continue
		}
		k, ok := ProtocolKernelMap[s.Type]
		if ok && k == KernelSingbox {
			needSingbox = true
			break
		}
	}
	if needSingbox {
		return KernelSingbox
	}
	// 优先用 sing-box（如果已安装），它支持更多协议
	_, errSingbox := findBin([]string{"sing-box", "sing-box.exe"}, []string{
		"/usr/local/bin/sing-box", "/usr/bin/sing-box",
	})
	if errSingbox == nil {
		return KernelSingbox
	}
	return KernelXray
}

func resolveActiveNode(o *configure.Outbound) *configure.NodeRef {
	switch o.Target.TargetType {
	case "node":
		return o.Target.NodeRef
	case "group":
		if o.Target.ActiveNodeRef != nil {
			return o.Target.ActiveNodeRef
		}
		_, group := configure.GetGroupByID(o.Target.GroupID)
		if group == nil || len(group.Servers) == 0 {
			return nil
		}
		ref := group.Servers[0]
		return &configure.NodeRef{Type: ref.Type, Index: ref.Index, Sub: ref.Sub}
	}
	return nil
}

func getServerRaw(ref *configure.NodeRef) *configure.ServerRaw {
	switch ref.Type {
	case "server":
		return configure.GetServer(ref.Index)
	case "sub_server":
		sub := configure.GetSubscription(ref.Sub)
		if sub == nil || ref.Index >= len(sub.Servers) {
			return nil
		}
		s := sub.Servers[ref.Index]
		return &s
	}
	return nil
}

func findBin(names, paths []string) (string, error) {
	names = filterBinNamesForCurrentOS(names)
	paths = filterBinPathsForCurrentOS(paths)

	// 1. PATH 里找
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	// 2. 固定路径找
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	// 3. dataDir/bin/ 里找
	if Manager.dataDir != "" {
		for _, name := range names {
			p := filepath.Join(Manager.dataDir, "bin", name)
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	// 4. 可执行文件同目录下找
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, name := range names {
			p := filepath.Join(dir, name)
			if isUsableBinaryPath(p) {
				return p, nil
			}
			p = filepath.Join(dir, "bin", name)
			if isUsableBinaryPath(p) {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("binary not found for %s: %v", runtime.GOOS, names)
}

func filterBinNamesForCurrentOS(names []string) []string {
	var filtered []string
	for _, name := range names {
		if runtime.GOOS == "windows" {
			filtered = append(filtered, name)
			continue
		}
		if strings.HasSuffix(strings.ToLower(name), ".exe") {
			continue
		}
		filtered = append(filtered, name)
	}
	return filtered
}

func filterBinPathsForCurrentOS(paths []string) []string {
	var filtered []string
	for _, p := range paths {
		if runtime.GOOS != "windows" && strings.HasSuffix(strings.ToLower(p), ".exe") {
			continue
		}
		filtered = append(filtered, p)
	}
	return filtered
}

func isUsableBinaryPath(path string) bool {
	if runtime.GOOS != "windows" && strings.HasSuffix(strings.ToLower(path), ".exe") {
		return false
	}
	if _, err := os.Stat(path); err != nil {
		return false
	}
	return true
}

// KernelStatus 返回各内核的可用状态
func KernelStatus() map[string]string {
	status := map[string]string{}
	checks := map[string][]string{
		"xray":    {"xray", "xray.exe"},
		"v2ray":   {"v2ray", "v2ray.exe"},
		"singbox": {"sing-box", "sing-box.exe"},
	}
	commonPaths := map[string][]string{
		"xray":    {"/usr/local/bin/xray", "/usr/bin/xray"},
		"v2ray":   {"/usr/local/bin/v2ray", "/usr/bin/v2ray"},
		"singbox": {"/usr/local/bin/sing-box", "/usr/bin/sing-box"},
	}
	for k, names := range checks {
		if p, err := findBin(names, commonPaths[k]); err == nil {
			status[k] = p
		} else {
			status[k] = ""
		}
	}
	return status
}

// DataFileStatus 检查数据文件是否存在，返回路径或空字符串
func DataFileStatus(dataType string) string {
	if Manager.dataDir == "" {
		return ""
	}
	path := filepath.Join(Manager.dataDir, dataType+".dat")
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

// RuleSetStatus 检查 rule-set 文件是否存在，返回 map[name]path
func RuleSetStatus() map[string]string {
	result := map[string]string{}
	if Manager.dataDir == "" {
		return result
	}
	ruleSetDir := filepath.Join(Manager.dataDir, "rule-set")
	for _, rs := range ruleSetFiles {
		path := filepath.Join(ruleSetDir, rs.Name+".srs")
		if _, err := os.Stat(path); err == nil {
			result[rs.Name] = path
		} else {
			result[rs.Name] = ""
		}
	}
	return result
}

// DownloadRuleSets 下载所有内置 rule-set 文件到 dataDir/rule-set/
func DownloadRuleSets(mirror string, progressCh chan<- DownloadProgress) error {
	// 若前端未传 mirror，从设置里读
	if mirror == "" {
		mirror = configure.GetSettingNotNil().GithubMirror
	}
	ruleSetDir := filepath.Join(Manager.dataDir, "rule-set")
	if err := os.MkdirAll(ruleSetDir, 0755); err != nil {
		progressCh <- DownloadProgress{Kernel: "rule-set", Status: "error", Message: "创建目录失败: " + err.Error()}
		return err
	}

	total := len(ruleSetFiles)
	for i, rs := range ruleSetFiles {
		pct := float64(i) / float64(total) * 100
		progressCh <- DownloadProgress{
			Kernel:  "rule-set",
			Status:  "downloading",
			Message: fmt.Sprintf("[%d/%d] 下载 %s…", i+1, total, rs.Name),
			Percent: pct,
		}
		if LogCallback != nil {
			LogCallback(fmt.Sprintf("📥 下载 rule-set: %s", rs.Name))
		}

		// 应用加速镜像前缀
		fileURL := applyMirrorURL(rs.URL, mirror)
		destPath := filepath.Join(ruleSetDir, rs.Name+".srs")
		if err := downloadFile(fileURL, destPath); err != nil {
			msg := fmt.Sprintf("下载 %s 失败: %v", rs.Name, err)
			progressCh <- DownloadProgress{Kernel: "rule-set", Status: "error", Message: msg}
			if LogCallback != nil {
				LogCallback("❌ " + msg)
			}
			return err
		}
	}

	progressCh <- DownloadProgress{Kernel: "rule-set", Status: "done", Message: "所有 rule-set 下载完成", Percent: 100}
	if LogCallback != nil {
		LogCallback("✅ rule-set 下载完成")
	}
	return nil
}

// applyMirror 将 GitHub URL 替换为加速镜像
// mirror 格式示例: "https://ghfast.top" 或 "https://mirror.ghproxy.com"
// 结果: mirror + "/" + originalURL
func applyMirror(originalURL, mirror string) string {
	return applyMirrorURL(originalURL, mirror)
}

// downloadFile 下载单个文件到目标路径
func downloadFile(url, destPath string) error {
	client := newDownloadClient()
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("网络错误: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// 先写到临时文件，成功后再移动，避免写入失败留下损坏文件
	tmpPath := destPath + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil || n == 0 {
		os.Remove(tmpPath)
		if err != nil {
			return fmt.Errorf("写入失败: %w", err)
		}
		return fmt.Errorf("文件为空")
	}
	// 原子替换
	os.Remove(destPath)
	return os.Rename(tmpPath, destPath)
}

func writeJSON(v interface{}, path string) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
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

func explainKernelStartError(binPath string, err error) error {
	if err == nil {
		return nil
	}
	if runtime.GOOS != "linux" {
		return err
	}
	if !isUsableBinaryPath(binPath) {
		return err
	}

	msg := err.Error()
	if strings.Contains(msg, "no such file or directory") {
		return fmt.Errorf("%w; binary exists but cannot be loaded. This usually means the kernel binary is incompatible with the container runtime (for example Alpine/musl vs glibc). Current binary: %s", err, binPath)
	}
	if strings.Contains(msg, "exec format error") {
		return fmt.Errorf("%w; binary architecture does not match the current container platform. Current binary: %s", err, binPath)
	}
	if strings.Contains(msg, "permission denied") {
		return fmt.Errorf("%w; check execute permission on kernel binary: %s", err, binPath)
	}
	return err
}


// logWriter 实现 io.Writer 接口，用于捕获进程输出并转发到日志系统
type logWriter struct {
	buffer bytes.Buffer
	mu     sync.Mutex
}

func (w *logWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	
	// 同时输出到控制台（保留原始 ANSI 颜色）
	_, _ = os.Stdout.Write(p)
	
	// 缓冲数据，按行处理
	w.buffer.Write(p)
	
	for {
		line, err := w.buffer.ReadBytes('\n')
		if err != nil {
			if err == io.EOF && len(line) > 0 {
				w.buffer.Write(line)
			}
			break
		}
		lineStr := string(bytes.TrimSuffix(line, []byte("\n")))
		lineStr = stripANSI(lineStr)
		lineStr = strings.TrimSpace(lineStr)
		if lineStr != "" && LogCallback != nil {
			// 过滤掉 inbound 连接日志（冗余，outbound 行已包含完整信息）
			if !strings.Contains(lineStr, "inbound connection from ") &&
				!strings.Contains(lineStr, "inbound connection to ") {
				LogCallback(lineStr)
			}
		}
	}
	
	return len(p), nil
}

// stripANSI 移除 ANSI 转义码（颜色、样式等）
func stripANSI(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// 跳过 ESC[ ... m 序列
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++ // 跳过 'm'
		} else {
			result.WriteByte(s[i])
			i++
		}
	}
	return result.String()
}
