package main

import (
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ProxyStation/proxystation/core/engine"
	"github.com/ProxyStation/proxystation/db"
	"github.com/ProxyStation/proxystation/db/configure"
	"github.com/ProxyStation/proxystation/pkg/observatory"
	"github.com/ProxyStation/proxystation/pkg/scheduler"
	"github.com/ProxyStation/proxystation/server"
	gonanoid "github.com/matoous/go-nanoid"
)

// logWriter 实现 io.Writer 接口，将日志写入到 addLog
type logWriter struct{}

func (w *logWriter) Write(p []byte) (n int, err error) {
	msg := string(p)
	// 移除末尾的换行符，因为 addLog 会添加时间戳
	if len(msg) > 0 && msg[len(msg)-1] == '\n' {
		msg = msg[:len(msg)-1]
	}
	server.AddLog(msg)
	return len(p), nil
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("fatal error: %v", r)
			cleanup()
			os.Exit(1)
		}
	}()

	var (
		addr    = flag.String("addr", "0.0.0.0:2026", "listen address")
		dataDir = flag.String("data", "./data", "data directory")
		guiDir  = flag.String("gui", "../gui/dist", "frontend dist directory")
	)
	flag.Parse()

	*dataDir = resolveDataDir(*dataDir)
	*guiDir = resolveGUIDir(*guiDir)

	// 重定向标准日志到 addLog
	log.SetOutput(io.MultiWriter(os.Stdout, &logWriter{}))
	log.SetFlags(log.Ltime)

	// 创建数据目录
	if err := os.MkdirAll(*dataDir, 0755); err != nil {
		log.Fatalf("failed to create data dir: %v", err)
	}

	// 初始化数据库
	dbPath := filepath.Join(*dataDir, "proxystation.db")
	if err := db.Init(dbPath); err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	// 初始化引擎管理器
	engine.Init(*dataDir)
	// 提前绑定引擎日志回调，确保启动阶段日志可见
	engine.LogCallback = server.AddLog
	observatory.SetLogCallback(server.AddLog)

	// 初始化日志文件路径
	server.InitLogFile(*dataDir)

	// 从设置中加载日志配置
	setting := configure.GetSettingNotNil()
	applyWebAuthEnvOverride(setting)
	if setting.MaxLogLines > 0 {
		server.SetMaxLogLines(setting.MaxLogLines)
	}
	if setting.MaxLogFileSize > 0 {
		server.SetMaxLogFileSize(setting.MaxLogFileSize)
	}

	// 迁移旧端口到新端口
	migratePortSettings(setting)

	// 确保 SERVER 内置分组存在
	ensureServerGroup()
	// 启动独立于内核的 cfdo/cfgoodnet 代理
	server.EnsureIndependentProxyInbounds()

	// 启动 observatory（分组测速）
	observatory.Start()
	defer observatory.Stop()

	// 启动订阅自动更新调度器
	scheduler.Start()
	defer scheduler.Stop()

	// 启动时自动启用代理（只要有出站配置就自动启动）
	log.Println("📌 检测出站配置，尝试自动启动代理...")
	if err := engine.Manager.Start(); err != nil {
		log.Printf("⚠️ 自动启动代理失败（可能未配置出站）: %v", err)
	} else {
		log.Println("✅ 代理已自动启动")
	}

	// 优雅关闭：监听系统信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		log.Println("shutting down...")
		cleanup()
		os.Exit(0)
	}()

	// 启动 HTTP 服务
	r := server.NewRouter(*guiDir)
	log.Printf("ProxyStation listening on %s", *addr)
	if err := r.Run(*addr); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func resolveDataDir(configured string) string {
	if pathExists(configured) {
		return toAbsPath(configured)
	}

	exeDir := executableDir()
	candidates := []string{
		filepath.Join(exeDir, "data"),
		filepath.Join(exeDir, "..", "data"),
	}
	for _, candidate := range candidates {
		if pathExists(candidate) {
			return toAbsPath(candidate)
		}
	}
	return toAbsPath(configured)
}

func resolveGUIDir(configured string) string {
	if dirHasIndex(configured) {
		return configured
	}

	exeDir := executableDir()
	candidates := []string{
		filepath.Join(exeDir, "gui", "dist"),
		filepath.Join(exeDir, "..", "gui", "dist"),
		filepath.Join(exeDir, "..", "..", "gui", "dist"),
		filepath.Join(exeDir, "..", "dist"),
		filepath.Join(exeDir, "dist"),
		filepath.Join("gui", "dist"),
	}
	for _, candidate := range candidates {
		if dirHasIndex(candidate) {
			return candidate
		}
	}
	return configured
}

func executableDir() string {
	exePath, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exePath)
}

func toAbsPath(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func dirHasIndex(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(path, "index.html"))
	return err == nil
}

func cleanup() {
	engine.Manager.Stop()
	observatory.Stop()
	scheduler.Stop()
	db.Close()
}

func applyWebAuthEnvOverride(setting *configure.Setting) {
	username := os.Getenv("PROXYSTATION_WEB_USERNAME")
	password := os.Getenv("PROXYSTATION_WEB_PASSWORD")
	if username == "" && password == "" {
		return
	}

	changed := false
	if username != "" && setting.WebUsername != username {
		setting.WebUsername = username
		changed = true
	}
	if password != "" && !configure.VerifyWebPassword(setting.WebPassword, password) {
		hashed, err := configure.HashWebPassword(password)
		if err != nil {
			log.Printf("⚠️ 加密 Web 管理端环境变量密码失败: %v", err)
			return
		}
		setting.WebPassword = hashed
		changed = true
	}
	if !changed {
		return
	}

	if err := configure.SetSetting(setting); err != nil {
		log.Printf("⚠️ 保存 Web 管理端环境变量配置失败: %v", err)
		return
	}
	log.Println("✅ 已应用环境变量中的 Web 管理端认证配置")
}

// ensureServerGroup 确保 SERVER 内置分组存在
func ensureServerGroup() {
	groups := configure.GetGroups()
	for _, g := range groups {
		if g.Name == "SERVER" && !g.FromSub {
			return // SERVER 分组已存在
		}
	}
	// SERVER 分组不存在，创建它
	id, _ := gonanoid.Nanoid()
	serverGroup := &configure.Group{
		ID:        id,
		Name:      "SERVER",
		FromSub:   false,
		CreatedAt: time.Now(),
	}
	if err := configure.AppendGroup(serverGroup); err != nil {
		log.Printf("⚠️ 创建 SERVER 分组失败: %v", err)
	} else {
		log.Println("✅ SERVER 内置分组已创建")
	}
}

// migratePortSettings 迁移旧端口到新端口
func migratePortSettings(setting *configure.Setting) {
	changed := false
	if setting.Socks5Port == 20170 {
		setting.Socks5Port = 20260
		changed = true
		log.Println("🔄 迁移 SOCKS5 端口: 20170 → 20260")
	}
	if setting.HttpPort == 20171 {
		setting.HttpPort = 20261
		changed = true
		log.Println("🔄 迁移 HTTP 端口: 20171 → 20261")
	}
	if changed {
		if err := configure.SetSetting(setting); err != nil {
			log.Printf("⚠️ 保存端口迁移失败: %v", err)
		}
	}
}
