package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ProxyStation/proxystation/core/cfdo"
	"github.com/ProxyStation/proxystation/core/cfgoodnet"
	"github.com/ProxyStation/proxystation/core/engine"
	"github.com/ProxyStation/proxystation/db/configure"
	"github.com/ProxyStation/proxystation/pkg/ipinfo"
	"github.com/ProxyStation/proxystation/pkg/ipscore"
	"github.com/ProxyStation/proxystation/pkg/observatory"
	"github.com/ProxyStation/proxystation/pkg/subscription"
	"github.com/gin-gonic/gin"
	gonanoid "github.com/matoous/go-nanoid"
)

// 日志缓冲区和订阅者管理
var (
	logMutex        sync.RWMutex
	logBuffer       []string
	logSubscribers  []*logSubscriber
	maxLogLines     = 500
	logFilePath     string
	maxLogFileSize  int64 = 2 * 1024 * 1024 // 2MB
	logQueue              = make(chan queuedLogEntry, 1024)
	logDroppedCount int
	logWorkerOnce   sync.Once
)

func init() {
	observatory.SetNodeMetadataEnricher(enrichMissingNodeMetadataAsync)
}

type queuedLogEntry struct {
	message string
	level   string
}

// logSubscriber 携带单个 SSE 订阅者的过滤条件。
// inbound 非空时只推送消息前缀匹配 "inbound:<id>" 的日志；
// alwaysSystem 为真时，即使设置了 inbound 过滤，无 "inbound:" 前缀的系统日志仍会推送。
type logSubscriber struct {
	ch           chan LogEntry
	inbound      string
	inboundTag   string
	alwaysSystem bool
}

// InitLogFile 初始化日志文件路径
func InitLogFile(dataDir string) {
	logFilePath = filepath.Join(dataDir, "proxystation.log")
	ensureLogWorker()
}

func ensureLogWorker() {
	logWorkerOnce.Do(func() {
		go processLogQueue()
	})
}

// SetMaxLogLines 设置最大日志行数
func SetMaxLogLines(lines int) {
	if lines > 0 {
		maxLogLines = lines
	}
}

// SetMaxLogFileSize 设置最大日志文件大小
func SetMaxLogFileSize(size int64) {
	if size > 0 {
		maxLogFileSize = size
	}
}

// LogEntry 代表一条日志记录
type LogEntry struct {
	Time    string `json:"time"`
	Message string `json:"message"`
	Level   string `json:"level"` // "info", "warn", "error", "debug", "success"
}

// AddLog 添加日志到缓冲区并广播给所有订阅者
func AddLog(msg string) {
	AddLogWithLevel(msg, "info")
}

// AddLogWithLevel 添加带级别的日志
func AddLogWithLevel(msg string, level string) {
	ensureLogWorker()
	entry := queuedLogEntry{message: msg, level: level}
	select {
	case logQueue <- entry:
	default:
		logMutex.Lock()
		logDroppedCount++
		dropped := logDroppedCount
		logMutex.Unlock()
		if dropped%100 == 1 {
			fmt.Fprintf(os.Stderr, "log queue full, dropped %d messages\n", dropped)
		}
	}
}

func processLogQueue() {
	for item := range logQueue {
		now := time.Now().Format("15:04:05")
		formattedMsg := fmt.Sprintf("[%s] %s", now, item.message)
		entry := LogEntry{
			Time:    now,
			Message: item.message,
			Level:   item.level,
		}

		var (
			logsToFlush []string
			subscribers []*logSubscriber
		)

		logMutex.Lock()
		logBuffer = append(logBuffer, formattedMsg)
		if len(logBuffer) > maxLogLines {
			logsToFlush = append(logsToFlush, logBuffer[:len(logBuffer)-maxLogLines]...)
			logBuffer = logBuffer[len(logBuffer)-maxLogLines:]
		}
		if len(logSubscribers) > 0 {
			subscribers = append(subscribers, logSubscribers...)
		}
		logMutex.Unlock()

		if len(logsToFlush) > 0 {
			writeLogsToFile(logsToFlush)
		}

		for _, sub := range subscribers {
			if !shouldIncludeLog(item.message, sub.inbound, sub.inboundTag, sub.alwaysSystem) {
				continue
			}
			select {
			case sub.ch <- entry:
			default:
			}
		}
	}
}

// writeLogsToFile 将日志写入文件，并检查文件大小
func writeLogsToFile(logs []string) {
	if logFilePath == "" || len(logs) == 0 {
		return
	}

	// 检查文件大小，如果超过限制则轮转
	if info, err := os.Stat(logFilePath); err == nil && info.Size() > maxLogFileSize {
		backupPath := logFilePath + "." + time.Now().Format("20060102150405")
		os.Rename(logFilePath, backupPath)
	}

	// 追加写入日志
	f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	for _, log := range logs {
		fmt.Fprintln(f, log)
	}
}

// addLog 是 AddLog 的别名，用于内部调用
func addLog(msg string) {
	// 自动检测日志级别
	level := "info"
	if strings.Contains(msg, "[ERROR]") || strings.Contains(msg, "❌") || strings.Contains(msg, "error") {
		level = "error"
	} else if strings.Contains(msg, "[WARN]") || strings.Contains(msg, "⚠️") || strings.Contains(msg, "warn") {
		level = "warn"
	} else if strings.Contains(msg, "[DEBUG]") || strings.Contains(msg, "debug") {
		level = "debug"
	} else if strings.Contains(msg, "✅") || strings.Contains(msg, "success") {
		level = "success"
	}
	AddLogWithLevel(msg, level)
}

// ---- Import（统一入口：自动判断节点链接 vs 订阅 URL）----

// importAuto 统一导入接口，自动判断：
//   - http/https URL → 当订阅处理，拉取内容解析，自动创建分组
//   - 节点链接（vmess/vless/ss/trojan/...）→ 直接导入为手动节点，可选加入分组
func importAuto(c *gin.Context) {
	var req struct {
		// 每行一个，可以混合节点链接和订阅 URL
		Content string `json:"content"`
		// 手动节点可选加入的分组 ID
		GroupID string `json:"groupId"`
		// 订阅名称（仅当 content 是单个订阅 URL 时有效，否则用 URL 域名）
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	lines := splitLines(req.Content)
	var (
		nodeLinks []string
		subURLs   []string
	)
	for _, line := range lines {
		switch classifyHTTPURL(line) {
		case urlKindSubscription:
			subURLs = append(subURLs, line)
		case urlKindAmbiguous:
			// http://host:port 既可能是订阅也可能是 HTTP 代理节点，拉一次内容再定
			if looksLikeSubscription(line) {
				addLog(fmt.Sprintf("🔍 %s 返回可解析的订阅内容，按订阅导入", line))
				subURLs = append(subURLs, line)
			} else {
				nodeLinks = append(nodeLinks, line)
			}
		default:
			if isNodeLink(line) {
				nodeLinks = append(nodeLinks, line)
			}
		}
	}

	if len(subURLs) == 0 && len(nodeLinks) == 0 {
		if servers, _, err := subscription.Parse([]byte(strings.TrimSpace(req.Content)), configure.FormatAuto); err == nil && len(servers) > 0 {
			for _, s := range servers {
				if strings.TrimSpace(s.Link) == "" {
					continue
				}
				nodeLinks = append(nodeLinks, s.Link)
			}
		}
	}

	imported := 0
	var createdGroups []string

	// 处理订阅 URL
	for _, u := range subURLs {
		name := req.Name
		if name == "" || len(subURLs) > 1 {
			name = urlDomain(u)
		}
		id, _ := gonanoid.Nanoid()
		groupID, _ := gonanoid.Nanoid()
		group := &configure.Group{
			ID:      groupID,
			Name:    name,
			FromSub: true,
			SubID:   id,
		}
		if err := configure.AppendGroup(group); err != nil {
			addLog(fmt.Sprintf("❌ 创建订阅分组失败: %v", err))
			continue
		}
		sub := &configure.SubscriptionRaw{
			ID:      id,
			Name:    name,
			URL:     u,
			Format:  configure.FormatAuto,
			GroupID: groupID,
		}
		if err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{sub}); err != nil {
			addLog(fmt.Sprintf("❌ 添加订阅失败: %v", err))
			continue
		}
		subIndex := configure.GetLenSubscriptions() - 1
		addLog(fmt.Sprintf("📥 添加订阅: %s", name))
		go fetchSubscription(subIndex, sub)
		createdGroups = append(createdGroups, groupID)
		imported++
	}

	// 处理节点链接
	if len(nodeLinks) > 0 {
		var newServers []*configure.ServerRaw
		for _, link := range nodeLinks {
			servers, _, err := subscription.Parse([]byte(link), configure.FormatV2ray)
			if err != nil || len(servers) == 0 {
				addLog(fmt.Sprintf("⚠️ 解析节点链接失败: %v", err))
				continue
			}
			for i := range servers {
				servers[i].Source = "manual"
				newServers = append(newServers, &servers[i])
			}
		}
		startIndex := configure.GetLenServers()
		if err := configure.AppendServers(newServers); err != nil {
			addLog(fmt.Sprintf("❌ 导入节点失败: %v", err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// 确定目标分组：优先使用指定的 GroupID，否则使用默认的 SERVER 分组
		targetGroupID := req.GroupID
		if targetGroupID == "" {
			// 查找或创建 SERVER 分组
			groups := configure.GetGroups()
			for _, g := range groups {
				if g.Name == "SERVER" && !g.FromSub {
					targetGroupID = g.ID
					break
				}
			}
			// 如果 SERVER 分组不存在，创建它
			if targetGroupID == "" {
				id, _ := gonanoid.Nanoid()
				serverGroup := &configure.Group{
					ID:      id,
					Name:    "SERVER",
					FromSub: false,
				}
				if err := configure.AppendGroup(serverGroup); err == nil {
					targetGroupID = id
				}
			}
		}

		// 将节点添加到目标分组
		if targetGroupID != "" {
			groupIndex, _ := configure.GetGroupByID(targetGroupID)
			if groupIndex >= 0 {
				for i := range newServers {
					_ = configure.AddServerToGroup(groupIndex, configure.ServerRef{
						Type:  "server",
						Index: startIndex + i,
					})
				}
			}
		}
		addLog(fmt.Sprintf("✅ 导入 %d 个节点", len(newServers)))
		imported += len(newServers)
	}

	c.JSON(http.StatusOK, gin.H{
		"imported":      imported,
		"createdGroups": createdGroups,
		"nodeCount":     len(nodeLinks),
		"subCount":      len(subURLs),
	})
}

func splitLines(s string) []string {
	var lines []string
	for _, line := range splitByNewline(s) {
		line = trimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func splitByNewline(s string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	result = append(result, s[start:])
	return result
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// urlKind 描述一行 http(s) 输入的归类结果
type urlKind int

const (
	// urlKindNotHTTP 不是 http(s) 开头，交给 isNodeLink 判断
	urlKindNotHTTP urlKind = iota
	// urlKindSubscription 明确是订阅 URL
	urlKindSubscription
	// urlKindProxyNode 明确是 HTTP 代理节点链接
	urlKindProxyNode
	// urlKindAmbiguous 形如 http://host:port，订阅和代理节点无法从 URL 本身区分
	urlKindAmbiguous
)

// classifyHTTPURL 区分订阅 URL 和 HTTP 代理节点链接
// HTTP 代理节点格式：http://[user:pass@]host:port  —— 有端口，路径极短或无路径
// 订阅 URL 格式：https://example.com/subscribe/xxx  —— 有路径
//
// 判断规则：
//  1. 包含 @ 且 @ 在第一个 / 之前 → 代理节点（http://user:pass@host:port）
//  2. 有实质路径（/xxx/yyy）→ 订阅 URL
//  3. 去掉 scheme 后没有路径（或路径只有 /）→ 两者结构完全一致，无法靠字符串区分，
//     交给调用方拉取内容后再定（urlKindAmbiguous）
func classifyHTTPURL(s string) urlKind {
	lower := strings.ToLower(s)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return urlKindNotHTTP
	}
	afterScheme := s
	if i := strings.Index(s, "://"); i >= 0 {
		afterScheme = s[i+3:]
	}
	// 找第一个 /（路径开始）
	slashIdx := strings.Index(afterScheme, "/")
	hostPart := afterScheme
	if slashIdx >= 0 {
		hostPart = afterScheme[:slashIdx]
	}
	// hostPart 里有 @ → 是代理节点（user:pass@host:port）
	if strings.Contains(hostPart, "@") {
		return urlKindProxyNode
	}
	// 没有路径或路径只有 / → 无法判断，需要探测内容
	if slashIdx < 0 || slashIdx == len(afterScheme)-1 {
		return urlKindAmbiguous
	}
	// 有实质路径 → 当订阅 URL
	return urlKindSubscription
}

// probeBodyLimit 探测时最多读取的响应字节数，避免误连到大文件端点时读爆内存
const probeBodyLimit = 4 << 20

// subscriptionUserAgent 伪装成常见客户端。多数机场按 UA 白名单放行，
// Go 默认的 Go-http-client/1.1 会被直接 403，拿到错误页后解析必然失败。
const subscriptionUserAgent = "clash-verge/v1.6.0"

// getSubscription 按订阅站期望的 UA 发起 GET 请求。
func getSubscription(client *http.Client, rawURL string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", subscriptionUserAgent)
	return client.Do(req)
}

// looksLikeSubscription 拉取一次内容来判断 http://host:port 形式的地址是不是订阅。
// 能解析出节点即认定为订阅；HTTP 代理节点收到普通 GET 时不会返回可解析的订阅内容，
// 拉取失败（离线、超时）时同样落回节点处理，保持原有行为。
func looksLikeSubscription(rawURL string) bool {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext:         (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			TLSHandshakeTimeout: 5 * time.Second,
		},
		Timeout: 10 * time.Second,
	}
	resp, err := getSubscription(client, rawURL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	content, err := io.ReadAll(io.LimitReader(resp.Body, probeBodyLimit))
	if err != nil {
		return false
	}
	servers, _, err := subscription.Parse(content, configure.FormatAuto)
	return err == nil && len(servers) > 0
}

func isNodeLink(s string) bool {
	// 所有已知代理节点协议前缀
	prefixes := []string{
		"vmess://", "vless://",
		"ss://", "ssr://",
		"trojan://", "trojan-go://",
		"hysteria://", "hysteria2://", "hy2://",
		"tuic://",
		"juicity://",
		"wireguard://",
		"socks://", "socks5://", "socks4://",
		"http://", "https://", // HTTP 代理节点（user:pass@host:port 格式）
		"naive+https://", "naive+http://",
	}
	lower := strings.ToLower(s)
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

func urlDomain(u string) string {
	// 简单提取域名作为名称
	s := u
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.Index(s, "/"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, ":"); i >= 0 {
		s = s[:i]
	}
	return s
}

// ---- Servers ----

type serverListItem struct {
	Identity          string `json:"identity"`
	Name              string `json:"name"`
	Host              string `json:"host"`
	Port              int    `json:"port"`
	Type              string `json:"type"`
	Latency           int    `json:"latency"`
	LastProbeTime     int64  `json:"lastProbeTime"`
	FastLatency       int    `json:"fastLatency"`
	LastFastProbeTime int64  `json:"lastFastProbeTime"`
	Source            string `json:"source"`
	FrontProxy        string `json:"frontProxy,omitempty"`
	IPType            string `json:"ipType,omitempty"`
	IPTypeAddr        string `json:"ipTypeAddr,omitempty"`
	IPTypeISP         string `json:"ipTypeIsp,omitempty"`
	IPTypeCheckedAt   int64  `json:"ipTypeCheckedAt,omitempty"`
	IPScore           *int   `json:"ipScore,omitempty"`
	IPScoreGrade      string `json:"ipScoreGrade,omitempty"`
	IPScoreRisk       string `json:"ipScoreRisk,omitempty"`
	IPScoreProvider   string `json:"ipScoreProvider,omitempty"`
	IPScoreAt         int64  `json:"ipScoreAt,omitempty"`
	IPGeoCountry      string `json:"ipGeoCountry,omitempty"`
	IPGeoCountryCode  string `json:"ipGeoCountryCode,omitempty"`
	IPGeoRegion       string `json:"ipGeoRegion,omitempty"`
	IPGeoCity         string `json:"ipGeoCity,omitempty"`
}

type serverCatalogItem struct {
	Identity string `json:"identity"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Type     string `json:"type"`
	Source   string `json:"source"`
}

func newServerListItem(server configure.ServerRaw) serverListItem {
	identity := strings.TrimSpace(server.Link)
	if identity != "" {
		sum := sha256.Sum256([]byte(identity))
		identity = fmt.Sprintf("link:%x", sum[:12])
	} else {
		identity = fmt.Sprintf("srv:%s|%s|%d|%s",
			strings.ToLower(strings.TrimSpace(server.Type)),
			strings.ToLower(strings.TrimSpace(server.Host)),
			server.Port,
			strings.TrimSpace(server.Name))
	}
	return serverListItem{
		Identity: identity, Name: server.Name, Host: server.Host, Port: server.Port,
		Type: server.Type, Latency: server.Latency, LastProbeTime: server.LastProbeTime,
		FastLatency: server.FastLatency, LastFastProbeTime: server.LastFastProbeTime,
		Source: server.Source, FrontProxy: server.FrontProxy, IPType: server.IPType,
		IPTypeAddr: server.IPTypeAddr, IPTypeISP: server.IPTypeISP,
		IPTypeCheckedAt: server.IPTypeCheckedAt, IPScore: server.IPScore,
		IPScoreGrade: server.IPScoreGrade, IPScoreRisk: server.IPScoreRisk,
		IPScoreProvider: server.IPScoreProvider, IPScoreAt: server.IPScoreAt,
		IPGeoCountry: server.IPGeoCountry, IPGeoCountryCode: server.IPGeoCountryCode,
		IPGeoRegion: server.IPGeoRegion, IPGeoCity: server.IPGeoCity,
	}
}

func serverListItems(servers []configure.ServerRaw) []serverListItem {
	items := make([]serverListItem, len(servers))
	for i := range servers {
		items[i] = newServerListItem(servers[i])
	}
	return items
}

func serverCatalogItems(servers []configure.ServerRaw) []serverCatalogItem {
	items := make([]serverCatalogItem, len(servers))
	for i := range servers {
		item := newServerListItem(servers[i])
		items[i] = serverCatalogItem{
			Identity: item.Identity, Name: item.Name, Host: item.Host,
			Port: item.Port, Type: item.Type, Source: item.Source,
		}
	}
	return items
}

func getServers(c *gin.Context) {
	servers := configure.GetServers()
	if c.Query("view") == "catalog" {
		c.JSON(http.StatusOK, gin.H{"servers": serverCatalogItems(servers)})
		return
	}
	if c.Query("view") == "list" {
		c.JSON(http.StatusOK, gin.H{"servers": serverListItems(servers)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"servers": servers})
}

type serverQueryResult struct {
	Ref    configure.NodeRef `json:"ref"`
	Server serverListItem    `json:"server"`
}

type serverCatalogResult struct {
	Ref    configure.NodeRef `json:"ref"`
	Server serverCatalogItem `json:"server"`
}

// serverCatalogResults 只保留选择节点所需的字段，避免下拉列表拉取延迟、IP 画像等无用数据
func serverCatalogResults(results []serverQueryResult) []serverCatalogResult {
	items := make([]serverCatalogResult, len(results))
	for i, item := range results {
		items[i] = serverCatalogResult{Ref: item.Ref, Server: serverCatalogItem{
			Identity: item.Server.Identity, Name: item.Server.Name, Host: item.Server.Host,
			Port: item.Server.Port, Type: item.Server.Type, Source: item.Server.Source,
		}}
	}
	return items
}

func queryServerRefs(refs []configure.NodeRef) []serverQueryResult {
	results := make([]serverQueryResult, 0, len(refs))
	var manualServers []configure.ServerRaw
	manualLoaded := false
	subscriptions := make(map[int]*configure.SubscriptionRaw)
	for _, ref := range refs {
		var server *configure.ServerRaw
		switch ref.Type {
		case "server":
			if !manualLoaded {
				manualServers = configure.GetServers()
				manualLoaded = true
			}
			if ref.Index >= 0 && ref.Index < len(manualServers) {
				server = &manualServers[ref.Index]
			}
		case "sub_server":
			sub, ok := subscriptions[ref.Sub]
			if !ok {
				sub = configure.GetSubscription(ref.Sub)
				subscriptions[ref.Sub] = sub
			}
			if sub != nil && ref.Index >= 0 && ref.Index < len(sub.Servers) {
				server = &sub.Servers[ref.Index]
			}
		}
		if server == nil {
			continue
		}
		results = append(results, serverQueryResult{Ref: ref, Server: newServerListItem(*server)})
	}
	return results
}

func queryServers(c *gin.Context) {
	var req struct {
		Refs []configure.NodeRef `json:"refs"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	results := queryServerRefs(req.Refs)
	c.JSON(http.StatusOK, gin.H{"results": results})
}

type serverSearchFilters struct {
	Query      string
	Type       string
	Geo        string
	Source     string
	IPType     string
	Probe      string
	FastProbe  string
	MaxLatency *int
	MinScore   *int
}

func optionalIntQuery(c *gin.Context, key string) (*int, error) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be an integer", key)
	}
	return &value, nil
}

func serverMatchesFilters(server configure.ServerRaw, filters serverSearchFilters) bool {
	if filters.Query != "" && !matchServer(server, filters.Query) {
		return false
	}
	if filters.Type != "" && strings.ToLower(strings.TrimSpace(server.Type)) != filters.Type {
		return false
	}
	if filters.Geo != "" {
		geo := strings.ToLower(strings.Join([]string{
			server.IPGeoCountry, server.IPGeoCountryCode, server.IPGeoRegion, server.IPGeoCity,
		}, " "))
		if !strings.Contains(geo, filters.Geo) {
			return false
		}
	}
	if filters.IPType == "unchecked" {
		if strings.TrimSpace(server.IPType) != "" {
			return false
		}
	} else if filters.IPType != "" && strings.ToLower(strings.TrimSpace(server.IPType)) != filters.IPType {
		return false
	}
	switch filters.Probe {
	case "unchecked":
		if server.LastProbeTime > 0 {
			return false
		}
	case "reachable":
		if server.LastProbeTime <= 0 || server.Latency <= 0 {
			return false
		}
	case "failed":
		if server.LastProbeTime <= 0 || server.Latency > 0 {
			return false
		}
	}
	switch filters.FastProbe {
	case "unchecked":
		if server.LastFastProbeTime > 0 {
			return false
		}
	case "reachable":
		if server.LastFastProbeTime <= 0 || server.FastLatency <= 0 {
			return false
		}
	case "failed":
		if server.LastFastProbeTime <= 0 || server.FastLatency > 0 {
			return false
		}
	}
	if filters.MaxLatency != nil {
		if server.LastProbeTime <= 0 || server.Latency <= 0 || server.Latency > *filters.MaxLatency {
			return false
		}
	}
	if filters.MinScore != nil && (server.IPScore == nil || *server.IPScore < *filters.MinScore) {
		return false
	}
	return true
}

// searchServers 搜索节点（跨手动节点和所有订阅）
func searchServers(c *gin.Context) {
	maxLatency, err := optionalIntQuery(c, "maxLatency")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	minScore, err := optionalIntQuery(c, "minScore")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	filters := serverSearchFilters{
		Query:      strings.ToLower(strings.TrimSpace(c.Query("q"))),
		Type:       strings.ToLower(strings.TrimSpace(c.Query("type"))),
		Geo:        strings.ToLower(strings.TrimSpace(c.Query("geo"))),
		Source:     strings.ToLower(strings.TrimSpace(c.Query("source"))),
		IPType:     strings.ToLower(strings.TrimSpace(c.Query("ipType"))),
		Probe:      strings.ToLower(strings.TrimSpace(c.Query("probe"))),
		FastProbe:  strings.ToLower(strings.TrimSpace(c.Query("fastProbe"))),
		MaxLatency: maxLatency,
		MinScore:   minScore,
	}
	if filters.Query == "" && filters.Type == "" && filters.Geo == "" && filters.Source == "" &&
		filters.IPType == "" && filters.Probe == "" && filters.FastProbe == "" && filters.MaxLatency == nil && filters.MinScore == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one search filter is required"})
		return
	}
	type result struct {
		Server     serverListItem    `json:"server"`
		Ref        configure.NodeRef `json:"ref"`
		GroupNames []string          `json:"groupNames"`
	}
	var results []result
	total := 0
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit < 1 || limit > 500 {
		limit = 100
	}
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if offset < 0 {
		offset = 0
	}
	// 搜索手动节点
	if filters.Source == "" || filters.Source == "manual" {
		for i, s := range configure.GetServers() {
			if !serverMatchesFilters(s, filters) {
				continue
			}
			total++
			if total > offset && len(results) < limit {
				results = append(results, result{Server: newServerListItem(s), Ref: configure.NodeRef{Type: "server", Index: i}})
			}
		}
	}
	// 搜索订阅节点
	if filters.Source == "" || filters.Source == "subscription" {
		for si, sub := range configure.GetSubscriptions() {
			for i, s := range sub.Servers {
				if !serverMatchesFilters(s, filters) {
					continue
				}
				total++
				if total > offset && len(results) < limit {
					results = append(results, result{Server: newServerListItem(s), Ref: configure.NodeRef{Type: "sub_server", Index: i, Sub: si}})
				}
			}
		}
	}
	resultIndexes := make(map[configure.ServerRef]int, len(results))
	for i, item := range results {
		resultIndexes[configure.ServerRef{Type: item.Ref.Type, Index: item.Ref.Index, Sub: item.Ref.Sub}] = i
	}
	for _, group := range configure.GetGroups() {
		for _, ref := range group.Servers {
			if index, ok := resultIndexes[ref]; ok {
				results[index].GroupNames = append(results[index].GroupNames, group.Name)
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"results": results, "total": total})
}

func getServerBySource(c *gin.Context) {
	prefix := strings.TrimSpace(c.Query("prefix"))
	if prefix == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "prefix is required"})
		return
	}
	for index, server := range configure.GetServers() {
		if server.Source == prefix || strings.HasPrefix(server.Source, prefix+":") {
			c.JSON(http.StatusOK, gin.H{"result": serverQueryResult{
				Ref: configure.NodeRef{Type: "server", Index: index}, Server: newServerListItem(server),
			}})
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "server not found"})
}

func matchServer(s configure.ServerRaw, q string) bool {
	return strings.Contains(strings.ToLower(s.Name), q) ||
		strings.Contains(strings.ToLower(s.Host), q) ||
		strings.Contains(strings.ToLower(s.Type), q)
}

func matchServerItem(item serverListItem, q string) bool {
	return strings.Contains(strings.ToLower(item.Name), q) ||
		strings.Contains(strings.ToLower(item.Host), q) ||
		strings.Contains(strings.ToLower(item.Type), q)
}

func importServers(c *gin.Context) {
	importAuto(c)
}

func deleteServers(c *gin.Context) {
	var req struct {
		Indexes []int `json:"indexes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := configure.RemoveServers(req.Indexes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func downloadCfGoodNetCA(c *gin.Context) {
	dataDir := engine.DataDir()
	if strings.TrimSpace(dataDir) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "data directory not initialized"})
		return
	}
	certPath, err := cfgoodnet.EnsureCA(dataDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if _, err := os.Stat(certPath); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "ca certificate not found"})
		return
	}
	c.Header("Content-Type", "application/x-x509-ca-cert")
	c.Header("Content-Disposition", "attachment; filename=cfgoodnet-ca.crt")
	c.File(certPath)
}

func importCfGoodNetCAToSystem(c *gin.Context) {
	if runtime.GOOS != "windows" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only supported on windows"})
		return
	}
	dataDir := engine.DataDir()
	if strings.TrimSpace(dataDir) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "data directory not initialized"})
		return
	}
	certPath, err := cfgoodnet.EnsureCA(dataDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	cmd := exec.Command("certutil", "-user", "-addstore", "Root", certPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "import failed",
			"detail": string(out),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "CA imported to CurrentUser Root store",
		"detail":  string(out),
	})
}

// editServer 修改手动节点的名称或链接
func editServer(c *gin.Context) {
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid index"})
		return
	}
	var req struct {
		Name string `json:"name"`
		Link string `json:"link"`
		// 指针类型：区分「未传」与「显式清空」
		FrontProxy *string `json:"frontProxy"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s := configure.GetServer(index)
	if s == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "server not found"})
		return
	}
	if req.Name != "" {
		s.Name = req.Name
	}
	if req.FrontProxy != nil {
		fp := strings.TrimSpace(*req.FrontProxy)
		if fp != "" && configure.GetOutbound(fp) == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("前置代理出站 %s 不存在", fp)})
			return
		}
		s.FrontProxy = fp
	}
	if req.Link != "" {
		s.Link = req.Link
		// 重新解析 host/port/type
		servers, _, err := subscription.Parse([]byte(req.Link), configure.FormatV2ray)
		if err == nil && len(servers) > 0 {
			s.Host = servers[0].Host
			s.Port = servers[0].Port
			s.Type = servers[0].Type
		}
	}
	if err := configure.SetServer(index, s); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"server": s})
}

// getServerLink 获取节点的标准分享链接（用于生成二维码）
// 对于 clash:// / singbox:// 内部格式，尝试还原为标准 URI
func getServerLink(c *gin.Context) {
	sType := c.Query("type")
	index, _ := strconv.Atoi(c.Query("index"))
	sub, _ := strconv.Atoi(c.Query("sub"))

	var s *configure.ServerRaw
	if sType == "sub_server" {
		subRaw := configure.GetSubscription(sub)
		if subRaw == nil || index >= len(subRaw.Servers) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		srv := subRaw.Servers[index]
		s = &srv
	} else {
		s = configure.GetServer(index)
	}
	if s == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}

	link := s.Link
	// clash:// 是内部存储格式，需要还原为标准分享链接
	if strings.HasPrefix(link, "clash://") {
		link = clashLinkToShareURI(s)
	} else if strings.HasPrefix(link, "singbox://") {
		// singbox:// 格式同样需要还原
		link = singboxLinkToShareURI(s)
	}

	c.JSON(http.StatusOK, gin.H{"link": link, "name": s.Name, "type": s.Type})
}

// clashLinkToShareURI 将 clash://base64 格式还原为标准分享 URI
func clashLinkToShareURI(s *configure.ServerRaw) string {
	encoded := strings.TrimPrefix(s.Link, "clash://")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Sprintf("%s://%s:%d#%s", s.Type, s.Host, s.Port, url.QueryEscape(s.Name))
	}
	var m map[string]interface{}
	if err := json.Unmarshal(decoded, &m); err != nil {
		return fmt.Sprintf("%s://%s:%d#%s", s.Type, s.Host, s.Port, url.QueryEscape(s.Name))
	}
	return buildShareURIFromClashMap(s, m)
}

// singboxLinkToShareURI 将 singbox://base64 格式还原为标准分享 URI
func singboxLinkToShareURI(s *configure.ServerRaw) string {
	encoded := strings.TrimPrefix(s.Link, "singbox://")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Sprintf("%s://%s:%d#%s", s.Type, s.Host, s.Port, url.QueryEscape(s.Name))
	}
	var m map[string]interface{}
	if err := json.Unmarshal(decoded, &m); err != nil {
		return fmt.Sprintf("%s://%s:%d#%s", s.Type, s.Host, s.Port, url.QueryEscape(s.Name))
	}
	return buildShareURIFromClashMap(s, m)
}

// buildShareURIFromClashMap 从 clash/singbox JSON map 构建标准分享 URI
func buildShareURIFromClashMap(s *configure.ServerRaw, m map[string]interface{}) string {
	name := url.QueryEscape(s.Name)
	host := s.Host
	port := s.Port

	getString := func(key string) string {
		v, _ := m[key].(string)
		return v
	}
	getBool := func(key string) bool {
		v, _ := m[key].(bool)
		return v
	}

	switch s.Type {
	case "anytls":
		password := getString("password")
		sni := getString("sni")
		fp := getString("client-fingerprint")
		insecure := getBool("skip-cert-verify")
		udp := getBool("udp")
		q := url.Values{}
		if sni != "" {
			q.Set("sni", sni)
		}
		if fp != "" {
			q.Set("fp", fp)
		}
		if insecure {
			q.Set("insecure", "1")
		}
		if udp {
			q.Set("udp", "1")
		}
		u := url.URL{
			Scheme:   "anytls",
			User:     url.User(password),
			Host:     fmt.Sprintf("%s:%d", host, port),
			RawQuery: q.Encode(),
			Fragment: s.Name,
		}
		return u.String()

	case "hysteria2", "hy2":
		password := getString("password")
		sni := getString("sni")
		insecure := getBool("skip-cert-verify")
		q := url.Values{}
		if sni != "" {
			q.Set("sni", sni)
		}
		if insecure {
			q.Set("insecure", "1")
		}
		u := url.URL{
			Scheme:   "hysteria2",
			User:     url.User(password),
			Host:     fmt.Sprintf("%s:%d", host, port),
			RawQuery: q.Encode(),
			Fragment: s.Name,
		}
		return u.String()

	case "tuic":
		uuid := getString("uuid")
		password := getString("password")
		sni := getString("sni")
		insecure := getBool("skip-cert-verify")
		q := url.Values{}
		if sni != "" {
			q.Set("sni", sni)
		}
		if insecure {
			q.Set("insecure", "1")
		}
		u := url.URL{
			Scheme:   "tuic",
			User:     url.UserPassword(uuid, password),
			Host:     fmt.Sprintf("%s:%d", host, port),
			RawQuery: q.Encode(),
			Fragment: s.Name,
		}
		return u.String()

	case "trojan":
		password := getString("password")
		sni := firstNonEmptyStr(getString("sni"), getString("servername"))
		insecure := getBool("skip-cert-verify")
		q := clashTransportQuery(m)
		if sni != "" {
			q.Set("sni", sni)
		}
		if fp := getString("client-fingerprint"); fp != "" {
			q.Set("fp", fp)
		}
		if insecure {
			q.Set("allowInsecure", "1")
		}
		u := url.URL{
			Scheme:   "trojan",
			User:     url.User(password),
			Host:     fmt.Sprintf("%s:%d", host, port),
			RawQuery: q.Encode(),
			Fragment: s.Name,
		}
		return u.String()

	case "ss", "shadowsocks":
		method := firstNonEmptyStr(getString("cipher"), getString("method"))
		password := getString("password")
		if method == "" || password == "" {
			break
		}
		// SIP002: ss://base64(method:password)@host:port#name
		userinfo := base64.RawURLEncoding.EncodeToString([]byte(method + ":" + password))
		q := url.Values{}
		if plugin := getString("plugin"); plugin != "" {
			q.Set("plugin", plugin+clashPluginOpts(m))
		}
		share := fmt.Sprintf("ss://%s@%s:%d", userinfo, host, port)
		if encoded := q.Encode(); encoded != "" {
			share += "?" + encoded
		}
		return share + "#" + name

	case "vless":
		uuid := firstNonEmptyStr(getString("uuid"), getString("id"))
		if uuid == "" {
			break
		}
		q := clashTransportQuery(m)
		security := "none"
		if reality, ok := m["reality-opts"].(map[string]interface{}); ok {
			security = "reality"
			if pbk, _ := reality["public-key"].(string); pbk != "" {
				q.Set("pbk", pbk)
			}
			if sid, _ := reality["short-id"].(string); sid != "" {
				q.Set("sid", sid)
			}
		} else if getBool("tls") {
			security = "tls"
		}
		q.Set("security", security)
		if sni := firstNonEmptyStr(getString("sni"), getString("servername")); sni != "" {
			q.Set("sni", sni)
		}
		if fp := getString("client-fingerprint"); fp != "" {
			q.Set("fp", fp)
		}
		if flow := getString("flow"); flow != "" {
			q.Set("flow", flow)
		}
		if getBool("skip-cert-verify") {
			q.Set("allowInsecure", "1")
		}
		u := url.URL{
			Scheme:   "vless",
			User:     url.User(uuid),
			Host:     fmt.Sprintf("%s:%d", host, port),
			RawQuery: q.Encode(),
			Fragment: s.Name,
		}
		return u.String()

	case "vmess":
		uuid := firstNonEmptyStr(getString("uuid"), getString("id"))
		if uuid == "" {
			break
		}
		// vmess 通用格式为 base64(JSON)，字段名沿用 v2rayN 约定
		network := strings.ToLower(getString("network"))
		if network == "" {
			network = "tcp"
		}
		path, wsHost, serviceName := clashTransportParts(m)
		if serviceName != "" {
			path = serviceName
		}
		tls := ""
		if getBool("tls") {
			tls = "tls"
		}
		conf := map[string]interface{}{
			"v":    "2",
			"ps":   s.Name,
			"add":  host,
			"port": strconv.Itoa(port),
			"id":   uuid,
			"aid":  strconv.Itoa(anyToIntLocal(m["alterId"])),
			"scy":  firstNonEmptyStr(getString("cipher"), "auto"),
			"net":  network,
			"type": "none",
			"host": wsHost,
			"path": path,
			"tls":  tls,
			"sni":  firstNonEmptyStr(getString("sni"), getString("servername")),
		}
		data, err := json.Marshal(conf)
		if err != nil {
			break
		}
		return "vmess://" + base64.StdEncoding.EncodeToString(data)

	case "socks5", "socks":
		u := url.URL{
			Scheme:   "socks5",
			Host:     fmt.Sprintf("%s:%d", host, port),
			Fragment: s.Name,
		}
		if username := getString("username"); username != "" {
			u.User = url.UserPassword(username, getString("password"))
		}
		return u.String()

	case "http", "https":
		scheme := "http"
		if s.Type == "https" || getBool("tls") {
			scheme = "https"
		}
		u := url.URL{
			Scheme:   scheme,
			Host:     fmt.Sprintf("%s:%d", host, port),
			Fragment: s.Name,
		}
		if username := getString("username"); username != "" {
			u.User = url.UserPassword(username, getString("password"))
		}
		return u.String()
	}

	// 无法还原完整参数时退回最简形式，至少保留地址信息
	return fmt.Sprintf("%s://%s:%d#%s", s.Type, host, port, name)
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func anyToIntLocal(v interface{}) int {
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case string:
		n, _ := strconv.Atoi(val)
		return n
	}
	return 0
}

// clashTransportParts 取出 clash 各传输层子字典里的 path / host / serviceName
func clashTransportParts(m map[string]interface{}) (path, host, serviceName string) {
	sub := func(key string) map[string]interface{} {
		v, _ := m[key].(map[string]interface{})
		return v
	}
	if ws := sub("ws-opts"); ws != nil {
		path, _ = ws["path"].(string)
		if headers, ok := ws["headers"].(map[string]interface{}); ok {
			if h, _ := headers["Host"].(string); h != "" {
				host = h
			} else if h, _ := headers["host"].(string); h != "" {
				host = h
			}
		}
	}
	if hu := sub("http-upgrade-opts"); hu != nil && path == "" {
		path, _ = hu["path"].(string)
	}
	if h2 := sub("h2-opts"); h2 != nil && path == "" {
		path, _ = h2["path"].(string)
	}
	if grpc := sub("grpc-opts"); grpc != nil {
		serviceName, _ = grpc["grpc-service-name"].(string)
	}
	if host == "" {
		host, _ = m["servername"].(string)
	}
	return path, host, serviceName
}

// clashTransportQuery 把传输层参数拼成标准分享 URI 的查询串
func clashTransportQuery(m map[string]interface{}) url.Values {
	q := url.Values{}
	network, _ := m["network"].(string)
	network = strings.ToLower(network)
	path, host, serviceName := clashTransportParts(m)
	// clash 用 ws-opts.v2ray-http-upgrade 表示 httpupgrade
	if ws, ok := m["ws-opts"].(map[string]interface{}); ok {
		if upgrade, _ := ws["v2ray-http-upgrade"].(bool); upgrade {
			network = "httpupgrade"
		}
	}
	if network != "" {
		q.Set("type", network)
	}
	if path != "" {
		q.Set("path", path)
	}
	if host != "" {
		q.Set("host", host)
	}
	if serviceName != "" {
		q.Set("serviceName", serviceName)
	}
	return q
}

// clashPluginOpts 还原 ss 的 plugin-opts 为 ";k=v" 串
func clashPluginOpts(m map[string]interface{}) string {
	opts, ok := m["plugin-opts"].(map[string]interface{})
	if !ok || len(opts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(opts))
	for k := range opts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(";")
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(fmt.Sprintf("%v", opts[k]))
	}
	return b.String()
}

// copyServerToGroup 将节点复制到指定分组
func copyServerToGroup(c *gin.Context) {
	var req struct {
		Ref     configure.ServerRef `json:"ref"`
		GroupID string              `json:"groupId"`
		Clone   *bool               `json:"clone,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	groupIndex, group := configure.GetGroupByID(req.GroupID)
	if groupIndex < 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}
	ref := req.Ref
	clone := req.Clone == nil || *req.Clone
	if clone {
		server := serverRawByServerRef(req.Ref)
		if server == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "server not found"})
			return
		}
		// 检查目标分组内是否已有配置相同的节点（不比较名称）
		if groupHasServerRaw(group.Servers, *server) {
			c.JSON(http.StatusOK, gin.H{"ok": true, "skipped": true})
			return
		}
		// 每次都创建新的独立副本，不复用已有节点
		localRef, ok := createLocalServerCopy(*server)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "clone server failed"})
			return
		}
		ref = localRef
	}
	if err := configure.AddServerToGroup(groupIndex, ref); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	enrichMissingNodeMetadataAsync([]configure.NodeRef{{Type: ref.Type, Index: ref.Index, Sub: ref.Sub}})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func batchCopyServersToGroup(c *gin.Context) {
	var req struct {
		Refs    []configure.ServerRef `json:"refs"`
		GroupID string                `json:"groupId"`
		Clone   *bool                 `json:"clone,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	groupIndex, group := configure.GetGroupByID(req.GroupID)
	if groupIndex < 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}
	clone := req.Clone == nil || *req.Clone
	refs := make([]configure.ServerRef, 0, len(req.Refs))
	manualCount := configure.GetLenServers()
	clones := make([]*configure.ServerRaw, 0, len(req.Refs))
	pending := make([]configure.ServerRaw, 0, len(req.Refs))
	for _, sourceRef := range req.Refs {
		ref := sourceRef
		if clone {
			server := serverRawByServerRef(sourceRef)
			if server == nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "server not found"})
				return
			}
			if groupHasServerRaw(group.Servers, *server) || serverListHasConfig(pending, *server) {
				continue
			}
			copy := localServerCopy(*server)
			ref = configure.ServerRef{Type: "server", Index: manualCount + len(clones)}
			clones = append(clones, &copy)
			pending = append(pending, copy)
		}
		refs = append(refs, ref)
	}
	if len(clones) > 0 {
		if err := configure.AppendServers(clones); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "clone server failed"})
			return
		}
	}
	if err := configure.AddServersToGroup(groupIndex, refs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	nodeRefs := make([]configure.NodeRef, 0, len(refs))
	for _, ref := range refs {
		nodeRefs = append(nodeRefs, configure.NodeRef{Type: ref.Type, Index: ref.Index, Sub: ref.Sub})
	}
	enrichMissingNodeMetadataAsync(nodeRefs)
	c.JSON(http.StatusOK, gin.H{"ok": true, "group": configure.GetGroup(groupIndex)})
}

func batchMoveServersToGroup(c *gin.Context) {
	var req struct {
		Refs          []configure.ServerRef `json:"refs"`
		SourceGroupID string                `json:"sourceGroupId"`
		TargetGroupID string                `json:"targetGroupId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	sourceIndex, sourceGroup := configure.GetGroupByID(req.SourceGroupID)
	targetIndex, targetGroup := configure.GetGroupByID(req.TargetGroupID)
	if sourceIndex < 0 || targetIndex < 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}
	if sourceGroup.ID == targetGroup.ID {
		c.JSON(http.StatusOK, gin.H{"ok": true, "sourceGroup": sourceGroup, "targetGroup": targetGroup})
		return
	}
	if err := configure.AddServersToGroup(targetIndex, req.Refs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := configure.RemoveServersFromGroup(sourceIndex, req.Refs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"sourceGroup": configure.GetGroup(sourceIndex),
		"targetGroup": configure.GetGroup(targetIndex),
	})
}

func serverRawByServerRef(ref configure.ServerRef) *configure.ServerRaw {
	switch ref.Type {
	case "server":
		return configure.GetServer(ref.Index)
	case "sub_server":
		sub := configure.GetSubscription(ref.Sub)
		if sub == nil || ref.Index < 0 || ref.Index >= len(sub.Servers) {
			return nil
		}
		return &sub.Servers[ref.Index]
	default:
		return nil
	}
}

// createLocalServerCopy 创建节点的新副本（不复用已有节点）
func createLocalServerCopy(server configure.ServerRaw) (configure.ServerRef, bool) {
	server = localServerCopy(server)
	servers := configure.GetServers()
	index := len(servers)
	if err := configure.AppendServers([]*configure.ServerRaw{&server}); err != nil {
		return configure.ServerRef{}, false
	}
	return configure.ServerRef{Type: "server", Index: index, Sub: 0}, true
}

func localServerCopy(server configure.ServerRaw) configure.ServerRaw {
	server.Source = "manual"
	return server
}

// groupHasServerRaw 检查分组内是否已有配置相同的节点（不比较名称）
func groupHasServerRaw(refs []configure.ServerRef, server configure.ServerRaw) bool {
	for _, ref := range refs {
		existing := serverRawByServerRef(ref)
		if existing != nil && sameServerConfig(*existing, server) {
			return true
		}
	}
	return false
}

func serverListHasConfig(servers []configure.ServerRaw, server configure.ServerRaw) bool {
	for _, existing := range servers {
		if sameServerConfig(existing, server) {
			return true
		}
	}
	return false
}

// sameServerConfig 比较两个节点的配置是否相同（不比较名称）
func sameServerConfig(a, b configure.ServerRaw) bool {
	// 优先比较链接，如果链接相同则认为是同一节点
	if strings.TrimSpace(a.Link) != "" && strings.TrimSpace(a.Link) == strings.TrimSpace(b.Link) {
		return true
	}
	// 链接不同或为空时，比较类型、主机、端口（不包括名称）
	return strings.EqualFold(strings.TrimSpace(a.Type), strings.TrimSpace(b.Type)) &&
		strings.EqualFold(strings.TrimSpace(a.Host), strings.TrimSpace(b.Host)) &&
		a.Port == b.Port
}

// ---- Subscriptions ----

type subscriptionListItem struct {
	ID            string                       `json:"id"`
	Name          string                       `json:"name"`
	URL           string                       `json:"url"`
	Format        configure.SubscriptionFormat `json:"format"`
	UpdatedAt     time.Time                    `json:"updatedAt"`
	GroupID       string                       `json:"groupId"`
	ProxyOutbound string                       `json:"proxyOutbound,omitempty"`
	ServerCount   int                          `json:"serverCount"`
	Servers       []serverListItem             `json:"servers,omitempty"`
}

type subscriptionCatalogItem struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	ServerCount int                 `json:"serverCount"`
	Servers     []serverCatalogItem `json:"servers"`
}

func newSubscriptionListItem(sub configure.SubscriptionRaw, includeServers bool) subscriptionListItem {
	item := subscriptionListItem{
		ID: sub.ID, Name: sub.Name, URL: sub.URL, Format: sub.Format,
		UpdatedAt: sub.UpdatedAt, GroupID: sub.GroupID, ProxyOutbound: sub.ProxyOutbound,
		ServerCount: len(sub.Servers),
	}
	if includeServers {
		item.Servers = serverListItems(sub.Servers)
	}
	return item
}

func getSubscriptions(c *gin.Context) {
	view := c.Query("view")
	if view == "summary" {
		summaries := configure.GetSubscriptionSummaries()
		items := make([]subscriptionListItem, len(summaries))
		for i, sub := range summaries {
			items[i] = subscriptionListItem{
				ID: sub.ID, Name: sub.Name, URL: sub.URL, Format: sub.Format,
				UpdatedAt: sub.UpdatedAt, GroupID: sub.GroupID, ProxyOutbound: sub.ProxyOutbound,
				ServerCount: sub.ServerCount,
			}
		}
		c.JSON(http.StatusOK, gin.H{"subscriptions": items})
		return
	}
	subs := configure.GetSubscriptions()
	if view == "catalog" {
		items := make([]subscriptionCatalogItem, len(subs))
		for i, sub := range subs {
			items[i] = subscriptionCatalogItem{
				ID: sub.ID, Name: sub.Name, ServerCount: len(sub.Servers),
				Servers: serverCatalogItems(sub.Servers),
			}
		}
		c.JSON(http.StatusOK, gin.H{"subscriptions": items})
		return
	}
	if view == "list" {
		items := make([]subscriptionListItem, len(subs))
		for i := range subs {
			items[i] = newSubscriptionListItem(subs[i], true)
		}
		c.JSON(http.StatusOK, gin.H{"subscriptions": items})
		return
	}
	c.JSON(http.StatusOK, gin.H{"subscriptions": subs})
}

func getSubscriptionServers(c *gin.Context) {
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid index"})
		return
	}
	sub := configure.GetSubscription(index)
	if sub == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "subscription not found"})
		return
	}
	page, _ := strconv.Atoi(c.Query("page"))
	pageSize, _ := strconv.Atoi(c.Query("pageSize"))
	start, end := 0, len(sub.Servers)
	if page > 0 && pageSize > 0 {
		if pageSize > 1000 {
			pageSize = 1000
		}
		start = (page - 1) * pageSize
		if start > len(sub.Servers) {
			start = len(sub.Servers)
		}
		end = start + pageSize
		if end > len(sub.Servers) {
			end = len(sub.Servers)
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"servers":     serverListItems(sub.Servers[start:end]),
		"serverCount": len(sub.Servers),
		"updatedAt":   sub.UpdatedAt,
		"offset":      start,
	})
}

func addSubscription(c *gin.Context) {
	var req struct {
		Name   string                       `json:"name"`
		URL    string                       `json:"url"`
		Format configure.SubscriptionFormat `json:"format"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	id, _ := gonanoid.Nanoid()
	sub := &configure.SubscriptionRaw{
		ID:     id,
		Name:   req.Name,
		URL:    req.URL,
		Format: req.Format,
	}
	if sub.Format == "" {
		sub.Format = configure.FormatAuto
	}

	// 自动创建对应的 Group
	groupID, _ := gonanoid.Nanoid()
	group := &configure.Group{
		ID:      groupID,
		Name:    req.Name,
		FromSub: true,
		SubID:   id,
	}
	if err := configure.AppendGroup(group); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	sub.GroupID = groupID

	if err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{sub}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 立即拉取节点
	go fetchSubscription(configure.GetLenSubscriptions()-1, sub)

	c.JSON(http.StatusOK, gin.H{"subscription": sub})
}

func updateSubscription(c *gin.Context) {
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid index"})
		return
	}
	var req struct {
		Name   string                       `json:"name"`
		URL    string                       `json:"url"`
		Format configure.SubscriptionFormat `json:"format"`
		// 指针类型：区分「未传」与「显式清空」
		ProxyOutbound *string `json:"proxyOutbound"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	sub := configure.GetSubscription(index)
	if sub == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "subscription not found"})
		return
	}
	if req.Name != "" {
		sub.Name = req.Name
	}
	if req.URL != "" {
		sub.URL = req.URL
	}
	if req.Format != "" {
		sub.Format = req.Format
	}
	if req.ProxyOutbound != nil {
		sub.ProxyOutbound = strings.TrimSpace(*req.ProxyOutbound)
	}
	if err := configure.SetSubscription(index, sub); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"subscription": sub})
}

func deleteSubscription(c *gin.Context) {
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid index"})
		return
	}
	// 删除订阅前，先找到对应的 Group 并删除
	sub := configure.GetSubscription(index)
	if sub != nil && sub.GroupID != "" {
		groupIndex, _ := configure.GetGroupByID(sub.GroupID)
		if groupIndex >= 0 {
			_ = configure.RemoveGroups([]int{groupIndex})
		}
	}
	if err := configure.RemoveSubscriptions([]int{index}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// cleanupOrphanGroups 清理孤立分组（fromSub=true 但对应订阅不存在）
func cleanupOrphanGroups(c *gin.Context) {
	groups := configure.GetGroups()
	subs := configure.GetSubscriptions()
	subIDMap := make(map[string]bool)
	for _, sub := range subs {
		subIDMap[sub.ID] = true
	}

	var orphanIndexes []int
	for i, g := range groups {
		if g.FromSub && !subIDMap[g.SubID] {
			orphanIndexes = append(orphanIndexes, i)
		}
	}

	if len(orphanIndexes) == 0 {
		c.JSON(http.StatusOK, gin.H{"cleaned": 0, "message": "no orphan groups"})
		return
	}

	if err := configure.RemoveGroups(orphanIndexes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"cleaned": len(orphanIndexes), "message": "orphan groups removed"})
}

func updateSubscriptionNodes(c *gin.Context) {
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid index"})
		return
	}
	sub := configure.GetSubscription(index)
	if sub == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "subscription not found"})
		return
	}
	go fetchSubscription(index, sub)
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "updating in background"})
}

// fetchSubscription 拉取订阅内容并更新节点
func fetchSubscription(index int, sub *configure.SubscriptionRaw) {
	addLog(fmt.Sprintf("📥 开始拉取订阅: %s (%s)", sub.Name, sub.URL))

	// 构建 HTTP 客户端，支持通过指定出站代理拉取
	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 30 * time.Second,
	}

	// 若订阅配置了走代理拉取，则经由内核的本地 SOCKS5 入站
	if sub.ProxyOutbound != "" {
		if proxyURL, err := builtinSocksProxyURL(); err != nil {
			addLog(fmt.Sprintf("⚠️  %v，将尝试直连拉取", err))
		} else {
			transport.Proxy = http.ProxyURL(proxyURL)
			addLog(fmt.Sprintf("🔗 通过本地代理 %s 拉取订阅", proxyURL.Host))
		}
	}

	subClient := &http.Client{Transport: transport}
	resp, err := getSubscription(subClient, sub.URL)
	if err != nil {
		addLog(fmt.Sprintf("❌ 拉取订阅失败: %v", err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		addLog(fmt.Sprintf("❌ 拉取订阅失败: 服务器返回 HTTP %d，订阅链接可能已失效或拒绝了本次请求", resp.StatusCode))
		return
	}
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		addLog(fmt.Sprintf("❌ 读取订阅内容失败: %v", err))
		return
	}

	servers, format, err := subscription.Parse(content, sub.Format)
	if err != nil {
		addLog(fmt.Sprintf("❌ 解析订阅失败: %v", err))
		return
	}

	// 更新 format（如果是 auto 检测到了具体格式）
	if sub.Format == configure.FormatAuto {
		sub.Format = format
	}

	// 为每个节点标记来源
	configure.PreserveServerProbeResults(sub.Servers, servers)
	for i := range servers {
		servers[i].Source = sub.ID
	}
	servers = configure.PreserveActiveSubscriptionServers(index, sub.Servers, servers)
	sub.Servers = servers
	sub.UpdatedAt = time.Now()

	_ = configure.SetSubscription(index, sub)
	addLog(fmt.Sprintf("✅ 订阅更新成功: %s (%d 个节点)", sub.Name, len(servers)))

	refs := make([]configure.NodeRef, len(servers))
	for i := range servers {
		refs[i] = configure.NodeRef{Type: "sub_server", Index: i, Sub: index}
	}
	enrichMissingNodeMetadataAsync(refs)

	// 同步更新对应 Group 的节点引用
	if sub.GroupID != "" {
		groupIndex, group := configure.GetGroupByID(sub.GroupID)
		if group != nil {
			// 重建 Group 的节点引用列表
			refs := make([]configure.ServerRef, len(servers))
			for i := range servers {
				refs[i] = configure.ServerRef{
					Type:  "sub_server",
					Index: i,
					Sub:   index,
				}
			}
			group.Servers = refs
			_ = configure.SetGroup(groupIndex, group)
		}
		observatory.OnGroupUpdated(sub.GroupID)
	}
}

// ---- Groups ----

type groupListItem struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	FromSub     bool      `json:"fromSub"`
	SubID       string    `json:"subId,omitempty"`
	ServerCount int       `json:"serverCount"`
	CreatedAt   time.Time `json:"createdAt"`
}

func getGroups(c *gin.Context) {
	groups := configure.GetGroups()
	if c.Query("view") != "list" {
		items := make([]groupListItem, len(groups))
		for i, group := range groups {
			items[i] = groupListItem{
				ID: group.ID, Name: group.Name, FromSub: group.FromSub, SubID: group.SubID,
				ServerCount: len(group.Servers), CreatedAt: group.CreatedAt,
			}
		}
		c.JSON(http.StatusOK, gin.H{"groups": items})
		return
	}
	c.JSON(http.StatusOK, gin.H{"groups": groups})
}

// resolveGroupParam 允许用分组 ID 或列表下标定位分组，ID 不受分组增删导致的下标漂移影响
func resolveGroupParam(param string) *configure.Group {
	if index, err := strconv.Atoi(param); err == nil {
		return configure.GetGroup(index)
	}
	_, group := configure.GetGroupByID(param)
	return group
}

func pageBounds(total, page, pageSize int) (int, int) {
	if page < 1 || pageSize < 1 {
		return 0, total
	}
	if pageSize > 1000 {
		pageSize = 1000
	}
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return start, end
}

func groupNodeRefs(refs []configure.ServerRef) []configure.NodeRef {
	nodeRefs := make([]configure.NodeRef, len(refs))
	for i, ref := range refs {
		nodeRefs[i] = configure.NodeRef{Type: ref.Type, Index: ref.Index, Sub: ref.Sub}
	}
	return nodeRefs
}

func getGroupServers(c *gin.Context) {
	group := resolveGroupParam(c.Param("index"))
	if group == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}
	page, _ := strconv.Atoi(c.Query("page"))
	pageSize, _ := strconv.Atoi(c.Query("pageSize"))

	// 按名称过滤必须先展开全部引用；无过滤时只展开当前页，避免整组查询
	if query := strings.ToLower(strings.TrimSpace(c.Query("q"))); query != "" {
		matched := make([]serverQueryResult, 0, len(group.Servers))
		for _, item := range queryServerRefs(groupNodeRefs(group.Servers)) {
			if matchServerItem(item.Server, query) {
				matched = append(matched, item)
			}
		}
		start, end := pageBounds(len(matched), page, pageSize)
		paged := matched[start:end]
		refs := make([]configure.ServerRef, len(paged))
		for i, item := range paged {
			refs[i] = configure.ServerRef{Type: item.Ref.Type, Index: item.Ref.Index, Sub: item.Ref.Sub}
		}
		writeGroupServers(c, refs, paged, len(matched), start)
		return
	}

	start, end := pageBounds(len(group.Servers), page, pageSize)
	refs := group.Servers[start:end]
	writeGroupServers(c, refs, queryServerRefs(groupNodeRefs(refs)), len(group.Servers), start)
}

func writeGroupServers(c *gin.Context, refs []configure.ServerRef, results []serverQueryResult, total, offset int) {
	if c.Query("view") == "catalog" {
		c.JSON(http.StatusOK, gin.H{
			"results": serverCatalogResults(results), "serverCount": total, "offset": offset,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"refs": refs, "results": results, "serverCount": total, "offset": offset,
	})
}

func createGroup(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id, _ := gonanoid.Nanoid()
	group := &configure.Group{
		ID:        id,
		Name:      req.Name,
		FromSub:   false,
		Servers:   []configure.ServerRef{},
		CreatedAt: time.Now(),
	}
	if err := configure.AppendGroup(group); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"group": group})
}

func updateGroup(c *gin.Context) {
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid index"})
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	group := configure.GetGroup(index)
	if group == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}
	// 防止修改 SERVER 内置分组
	if group.Name == "SERVER" && !group.FromSub {
		c.JSON(http.StatusForbidden, gin.H{"error": "cannot modify built-in SERVER group"})
		return
	}
	group.Name = req.Name
	if err := configure.SetGroup(index, group); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"group": group})
}

func deleteGroup(c *gin.Context) {
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid index"})
		return
	}
	// 防止删除 SERVER 内置分组
	group := configure.GetGroup(index)
	if group != nil && group.Name == "SERVER" && !group.FromSub {
		c.JSON(http.StatusForbidden, gin.H{"error": "cannot delete built-in SERVER group"})
		return
	}

	// 如果是订阅分组，删除对应的订阅
	if group != nil && group.FromSub && group.SubID != "" {
		// 找到对应的订阅并删除
		subs := configure.GetSubscriptions()
		for i, sub := range subs {
			if sub.ID == group.SubID {
				_ = configure.RemoveSubscriptions([]int{i})
				break
			}
		}
	} else if group != nil {
		// 删除手动分组下的所有手动节点（来自 servers 列表的节点）
		var serverIndexesToDelete []int
		for _, ref := range group.Servers {
			if ref.Type == "server" {
				serverIndexesToDelete = append(serverIndexesToDelete, ref.Index)
			}
		}
		// 从大到小排序，避免删除时索引变化
		for i := len(serverIndexesToDelete) - 1; i >= 0; i-- {
			for j := i - 1; j >= 0; j-- {
				if serverIndexesToDelete[j] > serverIndexesToDelete[i] {
					serverIndexesToDelete[i], serverIndexesToDelete[j] = serverIndexesToDelete[j], serverIndexesToDelete[i]
				}
			}
		}
		if len(serverIndexesToDelete) > 0 {
			configure.RemoveServers(serverIndexesToDelete)
		}
	}

	if err := configure.RemoveGroups([]int{index}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func addServerToGroup(c *gin.Context) {
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid index"})
		return
	}
	var ref configure.ServerRef
	if err := c.ShouldBindJSON(&ref); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := configure.AddServerToGroup(index, ref); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func removeServerFromGroup(c *gin.Context) {
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid index"})
		return
	}
	var ref configure.ServerRef
	if err := c.ShouldBindJSON(&ref); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := configure.RemoveServerFromGroup(index, ref); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func removeServersFromGroup(c *gin.Context) {
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid index"})
		return
	}
	var req struct {
		Refs []configure.ServerRef `json:"refs"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := configure.RemoveServersFromGroup(index, req.Refs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "group": configure.GetGroup(index)})
}

// ---- Outbounds ----

func listOutbounds() []*configure.Outbound {
	names := configure.GetOutboundNames()
	var outbounds []*configure.Outbound
	for _, name := range names {
		if name == engine.ProbeOutboundName {
			continue
		}
		o := configure.GetOutbound(name)
		if o == nil {
			o = &configure.Outbound{
				Name:   name,
				Target: configure.OutboundTarget{},
			}
		}
		if o.Target.TargetType == "special" {
			o.Target = configure.OutboundTarget{}
			_ = configure.SetOutbound(name, o)
			observatory.OnOutboundUpdated(name)
		}
		outbounds = append(outbounds, o)
	}
	return outbounds
}

func getOutbounds(c *gin.Context) {
	outbounds := listOutbounds()
	c.JSON(http.StatusOK, gin.H{"outbounds": outbounds})
}

// normalizeProbeInterval 校验出站的探测间隔（Go duration 字符串，如 "30s"、"5m"）。
// 空串表示未设置，由 observatory 回退到默认间隔。
func normalizeProbeInterval(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return "", fmt.Errorf("探测间隔格式非法，应形如 30s / 5m：%v", raw)
	}
	if d < 10*time.Second {
		return "", fmt.Errorf("探测间隔不能小于 10s")
	}
	return raw, nil
}

// normalizeProbeMode 校验出站的测速方式，空串表示回退到设置里的默认测速方式。
func normalizeProbeMode(raw string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "", "real", "fast":
		return mode, nil
	default:
		return "", fmt.Errorf("测速方式非法：%v", raw)
	}
}

func outboundSwitchDefaults() (int, int) {
	s := configure.GetSettingNotNil()
	threshold := s.GroupSwitchThresholdMs
	if threshold < 0 {
		threshold = 100
	}
	cooldown := s.GroupSwitchCooldownSec
	if cooldown < 0 {
		cooldown = 600
	}
	return threshold, cooldown
}

func normalizeSwitchThreshold(raw int) (int, error) {
	if raw < 0 {
		return 0, fmt.Errorf("切换阈值不能小于 0")
	}
	return raw, nil
}

func normalizeSwitchCooldown(raw int) (int, error) {
	if raw < 0 {
		return 0, fmt.Errorf("切换冷却时间不能小于 0")
	}
	return raw, nil
}

func createOutbound(c *gin.Context) {
	var req configure.Outbound
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	interval, err := normalizeProbeInterval(req.ProbeInterval)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.ProbeInterval = interval
	mode, err := normalizeProbeMode(req.ProbeMode)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.ProbeMode = mode
	req.Name = strings.TrimSpace(req.Name)
	if err := configure.AddOutbound(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := configure.SetOutbound(req.Name, &req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	observatory.OnOutboundUpdated(req.Name)
	c.JSON(http.StatusOK, gin.H{"outbound": req})
}

func updateOutbound(c *gin.Context) {
	name := c.Param("name")
	var req configure.Outbound
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Name = name
	interval, err := normalizeProbeInterval(req.ProbeInterval)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.ProbeInterval = interval
	mode, err := normalizeProbeMode(req.ProbeMode)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.ProbeMode = mode
	if err := configure.SetOutbound(name, &req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	observatory.OnOutboundUpdated(name)
	c.JSON(http.StatusOK, gin.H{"outbound": req})
}

func deleteOutbound(c *gin.Context) {
	name := c.Param("name")
	if err := configure.RemoveOutbound(name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	observatory.OnOutboundUpdated(name)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func renameOutbound(c *gin.Context) {
	oldName := c.Param("name")
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	newName := strings.TrimSpace(req.Name)
	if err := configure.RenameOutbound(oldName, newName); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	observatory.OnOutboundUpdated(oldName)
	observatory.OnOutboundUpdated(newName)
	// 出站 tag 变了，运行中的配置引用的是旧 tag，必须重建
	if engine.Manager.IsRunning() {
		_ = engine.Manager.Restart()
	}
	addLog(fmt.Sprintf("✏️ 出站已重命名：%s -> %s", oldName, newName))
	c.JSON(http.StatusOK, gin.H{"outbound": configure.GetOutbound(newName)})
}

func groupContainsNodeRef(refs []configure.ServerRef, target configure.NodeRef) bool {
	for _, ref := range refs {
		if ref.Type == target.Type && ref.Index == target.Index && ref.Sub == target.Sub {
			return true
		}
	}
	return false
}

func connectOutbound(c *gin.Context) {
	name := c.Param("name")
	// 请求体同时承载 target 字段与出站级测速配置。指针用于区分"未提交"和"提交了空值"，
	// 未提交时沿用出站已存的配置。
	var req struct {
		configure.OutboundTarget
		ProbeInterval     *string `json:"probeInterval"`
		ProbeMode         *string `json:"probeMode"`
		SwitchThresholdMs *int    `json:"switchThresholdMs"`
		SwitchCooldownSec *int    `json:"switchCooldownSec"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	o := configure.GetOutbound(name)
	if o == nil {
		o = &configure.Outbound{Name: name}
	}
	o.Target = req.OutboundTarget
	if o.Target.TargetType == "group" {
		_, group := configure.GetGroupByID(o.Target.GroupID)
		if group == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "分组不存在"})
			return
		}
		if o.Target.Mode == configure.ModeManual && o.Target.ActiveNodeRef == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "手动指定策略必须选择分组内节点"})
			return
		}
		if o.Target.ActiveNodeRef != nil && !groupContainsNodeRef(group.Servers, *o.Target.ActiveNodeRef) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "指定节点不属于所选分组"})
			return
		}
		if o.SwitchThresholdMs == nil || o.SwitchCooldownSec == nil {
			threshold, cooldown := outboundSwitchDefaults()
			if o.SwitchThresholdMs == nil {
				o.SwitchThresholdMs = &threshold
			}
			if o.SwitchCooldownSec == nil {
				o.SwitchCooldownSec = &cooldown
			}
		}
	}
	if req.ProbeInterval != nil {
		interval, err := normalizeProbeInterval(*req.ProbeInterval)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		o.ProbeInterval = interval
	}
	if req.ProbeMode != nil {
		mode, err := normalizeProbeMode(*req.ProbeMode)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		o.ProbeMode = mode
	}
	if req.SwitchThresholdMs != nil {
		threshold, err := normalizeSwitchThreshold(*req.SwitchThresholdMs)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		o.SwitchThresholdMs = &threshold
	}
	if req.SwitchCooldownSec != nil {
		cooldown, err := normalizeSwitchCooldown(*req.SwitchCooldownSec)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		o.SwitchCooldownSec = &cooldown
	}
	if err := configure.SetOutbound(name, o); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	observatory.OnOutboundUpdated(name)
	// 如果代理正在运行，重新生成配置
	if engine.Manager.IsRunning() {
		_ = engine.Manager.Restart()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func setOutboundActiveNode(c *gin.Context) {
	name := c.Param("name")
	var req struct {
		Ref configure.NodeRef `json:"ref"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	o := configure.GetOutbound(name)
	if o == nil || o.Target.TargetType != "group" || strings.TrimSpace(o.Target.GroupID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "出站未绑定分组"})
		return
	}
	_, group := configure.GetGroupByID(o.Target.GroupID)
	if group == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "出站绑定的分组不存在"})
		return
	}
	if !groupContainsNodeRef(group.Servers, req.Ref) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "指定节点不属于出站绑定分组"})
		return
	}
	current := o.Target.ActiveNodeRef
	if current != nil && current.Type == req.Ref.Type && current.Index == req.Ref.Index && current.Sub == req.Ref.Sub {
		c.JSON(http.StatusOK, gin.H{"ok": true, "outbound": o})
		return
	}
	if err := configure.SetOutboundActiveNode(name, &req.Ref); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	observatory.MarkManualSwitch(name)
	if engine.Manager.IsRunning() {
		_ = engine.Manager.Restart()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "outbound": configure.GetOutbound(name)})
}

func disconnectOutbound(c *gin.Context) {
	name := c.Param("name")
	o := configure.GetOutbound(name)
	if o == nil {
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}
	o.Target = configure.OutboundTarget{}
	if err := configure.SetOutbound(name, o); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	observatory.OnOutboundUpdated(name)
	// 断开连接后重启代理，使配置生效
	if engine.Manager.IsRunning() {
		_ = engine.Manager.Restart()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---- Ping ----

func pingNodes(c *gin.Context) {
	var req struct {
		Refs                     []configure.NodeRef `json:"refs"`
		Mode                     string              `json:"mode"` // "fast" | "real"
		EnableTransProxyFallback bool                `json:"enableTransProxyFallback"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	strictProbeMode := mode == "real"
	if strictProbeMode {
		addLog(fmt.Sprintf("⚡ 开始真连接探测 %d 个节点...", len(req.Refs)))
	} else {
		addLog(fmt.Sprintf("⚡ 开始快速探测 %d 个节点...", len(req.Refs)))
	}

	type result struct {
		Ref     configure.NodeRef `json:"ref"`
		Latency int               `json:"latency"` // ms, -1=timeout
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	flusher, _ := c.Writer.(http.Flusher)

	ctx := c.Request.Context()
	var sendMu sync.Mutex
	send := func(event string, payload interface{}) {
		b, _ := json.Marshal(payload)
		sendMu.Lock()
		defer sendMu.Unlock()
		fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, b)
		if flusher != nil {
			flusher.Flush()
		}
	}

	observatory.ProbeNodesWithProgressOptions(req.Refs, mode, observatory.ProbeOptions{
		EnableTransProxyFallback: req.EnableTransProxyFallback && strictProbeMode,
	},
		func(i int, ref configure.NodeRef) {
			if ctx.Err() != nil {
				return
			}
			send("running", result{Ref: ref, Latency: 0})
		},
		func(i int, ref configure.NodeRef, latency int) {
			if ctx.Err() != nil {
				return
			}
			send("done", result{Ref: ref, Latency: latency})
		},
	)

	if ctx.Err() != nil {
		if strictProbeMode {
			addLog("⏹ 真连接探测已中断")
		} else {
			addLog("⏹ 快速探测已中断")
		}
		return
	}
	if strictProbeMode {
		addLog("✅ 真连接探测完成")
	} else {
		addLog("✅ 快速探测完成")
	}
	send("end", gin.H{"done": true})
}

func pingGroup(c *gin.Context) {
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid index"})
		return
	}
	group := configure.GetGroup(index)
	if group == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	mode := strings.ToLower(strings.TrimSpace(c.Query("mode")))
	strictProbeMode := mode == "real"
	enableTransProxyFallback := strictProbeMode && strings.EqualFold(strings.TrimSpace(c.Query("enableTransProxyFallback")), "true")

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
		return
	}

	type result struct {
		Ref     configure.ServerRef `json:"ref"`
		Latency int                 `json:"latency"`
	}
	ctx := c.Request.Context()
	var sendMu sync.Mutex
	send := func(event string, payload any) {
		sendMu.Lock()
		defer sendMu.Unlock()
		b, _ := json.Marshal(payload)
		fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
	}

	serverRefs := group.Servers
	refs := make([]configure.NodeRef, len(serverRefs))
	for i, ref := range serverRefs {
		refs[i] = configure.NodeRef{Type: ref.Type, Index: ref.Index, Sub: ref.Sub}
	}

	observatory.ProbeNodesWithProgressOptions(refs, mode, observatory.ProbeOptions{
		EnableTransProxyFallback: enableTransProxyFallback,
	},
		func(i int, _ configure.NodeRef) {
			if ctx.Err() != nil {
				return
			}
			send("running", result{Ref: serverRefs[i], Latency: 0})
		},
		func(i int, _ configure.NodeRef, latency int) {
			if ctx.Err() != nil {
				return
			}
			send("done", result{Ref: serverRefs[i], Latency: latency})
		},
	)

	if ctx.Err() != nil {
		if strictProbeMode {
			addLog("⏹ 真连接探测已中断")
		} else {
			addLog("⏹ 快速探测已中断")
		}
		return
	}
	send("end", gin.H{"done": true})
}

// applyGroupSwitch 在界面上手动测完一个分组后调用，按绑定该分组的出站策略决定是否
// 切换活跃节点。延迟直接读测速刚写回的值，不会重新探测；中途被中断的测速前端不会
// 调这个接口，避免用半份数据做判定。
func applyGroupSwitch(c *gin.Context) {
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid index"})
		return
	}
	group := configure.GetGroup(index)
	if group == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}
	mode := strings.ToLower(strings.TrimSpace(c.Query("mode")))
	outcomes := observatory.ApplyGroupProbeResults(group.ID, mode)
	c.JSON(http.StatusOK, gin.H{"outcomes": outcomes})
}

// ---- IP 类型检测 ----

// detectServerIPType 查询节点地址属于机房、家宽还是移动网络，并把结果写回节点。
// 只把节点的主机名发给 ip-api.com，不涉及节点凭据；若配置了下载代理则经代理请求。
func detectServerIPType(c *gin.Context) {
	var req struct {
		Refs []configure.NodeRef `json:"refs"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.Refs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "refs is required"})
		return
	}

	hosts := make([]string, 0, len(req.Refs))
	for _, ref := range req.Refs {
		if s := serverRawByNodeRef(ref); s != nil {
			hosts = append(hosts, s.Host)
		}
	}
	if len(hosts) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "没有可检测的节点"})
		return
	}
	addLog(fmt.Sprintf("🌐 开始检测 %d 个节点的 IP 类型...", len(hosts)))

	lookups := ipinfo.Lookup(ipTypeHTTPClient(), hosts)

	type result struct {
		Ref    configure.NodeRef `json:"ref"`
		IPType string            `json:"ipType"`
		IP     string            `json:"ip,omitempty"`
		ISP    string            `json:"isp,omitempty"`
		Error  string            `json:"error,omitempty"`
	}
	results := make([]result, 0, len(req.Refs))
	failed := 0
	for _, ref := range req.Refs {
		s := serverRawByNodeRef(ref)
		if s == nil {
			continue
		}
		res, ok := lookups[strings.TrimSpace(s.Host)]
		if !ok {
			res = ipinfo.Result{Kind: ipinfo.KindUnknown, Err: fmt.Errorf("节点地址为空")}
		}
		item := result{Ref: ref, IPType: string(res.Kind), IP: res.IP, ISP: res.ISP}
		if res.Err != nil {
			item.Error = res.Err.Error()
			failed++
		}
		if err := saveIPType(ref, res); err != nil {
			item.Error = fmt.Sprintf("保存检测结果失败：%v", err)
			if res.Err == nil {
				failed++
			}
		}
		results = append(results, item)
	}
	addLog(fmt.Sprintf("✅ IP 类型检测完成：成功 %d 个，失败 %d 个", len(results)-failed, failed))
	c.JSON(http.StatusOK, gin.H{"results": results})
}

// ---- IP 评分 ----

// scoreServerIP 提交后台评分任务。评分是串行低频的外部请求，不能在 HTTP 请求里等，
// 因此立刻返回，由前端轮询 /servers/ip-score/status 获取进度。
func scoreServerIP(c *gin.Context) {
	var req struct {
		Refs []configure.NodeRef `json:"refs"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.Refs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "refs is required"})
		return
	}
	if err := startIPScoreTask(req.Refs); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "state": ipScoreState()})
}

func getIPScoreStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"state": ipScoreState()})
}

func cancelIPScore(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"canceled": cancelIPScoreTask()})
}

// ipTypeHTTPClient ip-api.com 免费接口在国内常被墙，配置了下载代理时优先走它，
// 否则回退到运行中的内建 SOCKS5 入站，都没有就直连。
func ipTypeHTTPClient() *http.Client {
	const timeout = 20 * time.Second
	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	if raw := strings.TrimSpace(configure.GetSettingNotNil().DownloadProxy); raw != "" {
		if proxyURL, err := url.Parse(raw); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
			return &http.Client{Timeout: timeout, Transport: transport}
		}
	}
	if proxyURL, err := builtinSocksProxyURL(); err == nil {
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

func serverRawByNodeRef(ref configure.NodeRef) *configure.ServerRaw {
	switch ref.Type {
	case "server":
		return configure.GetServer(ref.Index)
	case "sub_server":
		sub := configure.GetSubscription(ref.Sub)
		if sub == nil || ref.Index < 0 || ref.Index >= len(sub.Servers) {
			return nil
		}
		return &sub.Servers[ref.Index]
	default:
		return nil
	}
}

// saveIPType 把检测结果写回节点。查询失败时只记 unknown，不覆盖上次成功的 IP/ISP，
// 免得一次网络抖动把已有信息抹掉。
func saveIPType(ref configure.NodeRef, res ipinfo.Result) error {
	apply := func(s *configure.ServerRaw) {
		s.IPType = string(res.Kind)
		s.IPTypeCheckedAt = time.Now().Unix()
		if res.Err != nil {
			return
		}
		s.IPTypeAddr = res.IP
		s.IPTypeISP = res.ISP
		if strings.TrimSpace(res.Country) != "" || strings.TrimSpace(res.CountryCode) != "" ||
			strings.TrimSpace(res.Region) != "" || strings.TrimSpace(res.City) != "" {
			s.IPGeoCountry = res.Country
			s.IPGeoCountryCode = res.CountryCode
			s.IPGeoRegion = res.Region
			s.IPGeoCity = res.City
		}
	}
	switch ref.Type {
	case "server":
		return configure.UpdateServer(ref.Index, apply)
	case "sub_server":
		return configure.UpdateSubscriptionServer(ref.Sub, ref.Index, apply)
	default:
		return fmt.Errorf("不支持的节点类型 %q", ref.Type)
	}
}

// builtinSocksProxyURL 返回本地内建 SOCKS5 入站的代理 URL，若内核未运行则报错
func builtinSocksProxyURL() (*url.URL, error) {
	if !engine.Manager.IsRunning() {
		return nil, fmt.Errorf("内核未运行")
	}
	setting := configure.GetSettingNotNil()
	// 构造带认证的 SOCKS5 URL（若配置了认证账号）
	accounts := setting.Socks5AuthAccounts()
	if len(accounts) > 0 {
		// 使用第一个账号
		acc := accounts[0]
		return url.Parse(fmt.Sprintf("socks5://%s:%s@127.0.0.1:%d",
			url.QueryEscape(acc.Username), url.QueryEscape(acc.Password), setting.Socks5Port))
	}
	return url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", setting.Socks5Port))
}

// ---- Setting ----

func getSetting(c *gin.Context) {
	s := configure.GetSettingNotNil()
	resp := *s
	resp.WebPassword = ""
	c.JSON(http.StatusOK, gin.H{"setting": &resp})
}

func setSetting(c *gin.Context) {
	var s configure.Setting
	if err := c.ShouldBindJSON(&s); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(s.WebUsername) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "webUsername 不能为空"})
		return
	}
	switch s.KernelMode {
	case "", "auto", "singbox", "xray", "v2ray":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "kernelMode 非法"})
		return
	}
	switch s.DNSMode {
	case "", "lightweight", "compatible":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "dnsMode 非法"})
		return
	}
	if s.KernelMode == "" {
		s.KernelMode = "auto"
	}
	if s.DNSMode == "" {
		s.DNSMode = "lightweight"
	}
	if s.GroupRealProbeIntervalSec <= 0 {
		s.GroupRealProbeIntervalSec = 300
	}
	switch s.GroupProbeMode {
	case "", "real":
		s.GroupProbeMode = "real"
	case "fast":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "groupProbeMode 非法"})
		return
	}
	if s.GroupSwitchThresholdMs < 0 {
		s.GroupSwitchThresholdMs = 100
	}
	if s.GroupSwitchCooldownSec < 0 {
		s.GroupSwitchCooldownSec = 600
	}
	for i := range s.SubscriptionBestNodeCopyRules {
		r := &s.SubscriptionBestNodeCopyRules[i]
		switch r.Mode {
		case "":
			r.Mode = configure.CopyModeBest
		case configure.CopyModeBest, configure.CopyModeAll:
		case configure.CopyModeTopN:
			if r.Count <= 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "复制数量必须大于 0"})
				return
			}
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "节点复制方式非法"})
			return
		}
		if r.Mode != configure.CopyModeTopN {
			r.Count = 0
		}
	}
	current := configure.GetSettingNotNil()
	if s.WebPassword == "" {
		s.WebPassword = current.WebPassword
	} else if !configure.IsWebPasswordHashed(s.WebPassword) {
		hashed, err := configure.HashWebPassword(s.WebPassword)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "密码加密失败"})
			return
		}
		s.WebPassword = hashed
	}

	// 未启用时只校验格式：用户可能先切到自定义、之后再回来填地址
	provider, endpoint, err := ipscore.ValidateProviderEndpoint(s.IPScoreProvider, s.IPScoreEndpoint, s.IPScoreEnabled)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.IPScoreProvider = string(provider)
	s.IPScoreEndpoint = endpoint
	// 间隔太小会把第三方免费服务打成洪水，这里兜底钳住，界面上也有同样的下限
	if s.IPScoreMinIntervalMs < 500 {
		s.IPScoreMinIntervalMs = 1500
	}

	// 更新日志配置
	if s.MaxLogLines > 0 {
		maxLogLines = s.MaxLogLines
	}
	if s.MaxLogFileSize > 0 {
		maxLogFileSize = s.MaxLogFileSize
	}

	if err := configure.SetSetting(&s); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 设置更新后重载分组出站自动探测任务（间隔/阈值等立即生效）
	observatory.Reload()
	// 设置变更后，若内核正在运行则自动重启使配置生效
	if engine.Manager.IsRunning() {
		if err := engine.Manager.Restart(); err != nil {
			c.JSON(http.StatusOK, gin.H{"ok": true, "warning": "设置已保存，但重启内核失败: " + err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---- Proxy Control ----

func getBootstrap(c *gin.Context) {
	running := engine.Manager.IsRunning()
	if running != configure.GetRunning() {
		_ = configure.SetRunning(running)
	}
	c.JSON(http.StatusOK, gin.H{"running": running})
}

func getStatus(c *gin.Context) {
	// 使用实际的进程状态而不是数据库状态
	running := engine.Manager.IsRunning()
	// 同步数据库状态
	if running != configure.GetRunning() {
		_ = configure.SetRunning(running)
	}
	c.JSON(http.StatusOK, gin.H{
		"running": running,
		"kernel":  string(engine.Manager.CurrentKernel()),
	})
}

func getKernelStatus(c *gin.Context) {
	kernels := engine.KernelStatus()
	kernelMeta := gin.H{}
	for _, name := range []string{"singbox", "xray", "v2ray"} {
		// 快速返回本地状态，避免每次请求都阻塞在 GitHub release 检查上
		kernelMeta[name] = engine.KernelMeta{
			Path:         kernels[name],
			LocalVersion: engine.GetKernelVersion(name),
			HasUpdate:    false,
		}
	}
	// 检查数据文件是否存在
	kernels["geoip"] = engine.DataFileStatus("geoip")
	kernels["geosite"] = engine.DataFileStatus("geosite")
	// 检查 rule-set 文件
	for name, path := range engine.RuleSetStatus() {
		kernels["ruleset:"+name] = path
	}
	c.JSON(http.StatusOK, gin.H{"kernels": kernels, "kernelMeta": kernelMeta})
}

func downloadRuleSets(c *gin.Context) {
	mirror := c.Query("mirror") // 可选的 GitHub 加速镜像前缀

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Access-Control-Allow-Origin", "*")

	progressCh := make(chan engine.DownloadProgress, 20)

	go func() {
		engine.DownloadRuleSets(mirror, progressCh)
		close(progressCh)
	}()

	flusher, canFlush := c.Writer.(http.Flusher)
	clientGone := c.Request.Context().Done()

	for {
		select {
		case <-clientGone:
			return
		case p, ok := <-progressCh:
			if !ok {
				return
			}
			data, _ := json.Marshal(p)
			fmt.Fprintf(c.Writer, "data: %s\n\n", data)
			if canFlush {
				flusher.Flush()
			}
		}
	}
}

// downloadKernel 下载指定内核，通过 SSE 推送进度（GET 接口，EventSource 只支持 GET）
func downloadKernel(c *gin.Context) {
	kernel := c.Param("kernel")
	if kernel != "xray" && kernel != "singbox" && kernel != "v2ray" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown kernel"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Access-Control-Allow-Origin", "*")

	progressCh := make(chan engine.DownloadProgress, 20)

	go func() {
		engine.DownloadKernel(kernel, progressCh)
		close(progressCh)
	}()

	flusher, canFlush := c.Writer.(http.Flusher)
	clientGone := c.Request.Context().Done()

	for {
		select {
		case <-clientGone:
			return
		case p, ok := <-progressCh:
			if !ok {
				return
			}
			data, _ := json.Marshal(p)
			fmt.Fprintf(c.Writer, "data: %s\n\n", data)
			if canFlush {
				flusher.Flush()
			}
		}
	}
}

// downloadData 下载数据文件（geoip.dat 或 geosite.dat），通过 SSE 推送进度
func downloadData(c *gin.Context) {
	dataType := c.Param("type")
	if dataType != "geoip" && dataType != "geosite" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown data type"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Access-Control-Allow-Origin", "*")

	progressCh := make(chan engine.DownloadProgress, 20)

	go func() {
		engine.DownloadData(dataType, progressCh)
		close(progressCh)
	}()

	flusher, canFlush := c.Writer.(http.Flusher)
	clientGone := c.Request.Context().Done()

	for {
		select {
		case <-clientGone:
			return
		case p, ok := <-progressCh:
			if !ok {
				return
			}
			data, _ := json.Marshal(p)
			fmt.Fprintf(c.Writer, "data: %s\n\n", data)
			if canFlush {
				flusher.Flush()
			}
		}
	}
}

func startProxy(c *gin.Context) {
	addLog("📌 开始启动代理...")
	if err := engine.Manager.Start(); err != nil {
		addLog(fmt.Sprintf("❌ 启动失败: %v", err))
		// 确保启动失败时状态被设置为未运行
		_ = configure.SetRunning(false)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	addLog("✅ 代理启动成功")
	c.JSON(http.StatusOK, gin.H{"ok": true, "kernel": string(engine.Manager.CurrentKernel())})
}

func stopProxy(c *gin.Context) {
	addLog("📌 停止代理...")
	engine.Manager.Stop()
	addLog("✅ 代理已停止")
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// 避免 unused import 错误
var _ = engine.Manager

// ---- Logs ----

// getLogs 获取最近的日志缓冲区内容
func getLogs(c *gin.Context) {
	logMutex.RLock()
	defer logMutex.RUnlock()
	filter := c.Query("inbound")
	includeSystem := c.Query("system") == "1" || c.Query("system") == "true"
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit < 1 || limit > 500 {
		limit = 50
	}
	inboundID, inboundTag := resolveInboundFilter(filter)
	capacity := limit
	if capacity > len(logBuffer) {
		capacity = len(logBuffer)
	}
	logs := make([]string, 0, capacity)
	for i := len(logBuffer) - 1; i >= 0 && len(logs) < limit; i-- {
		message := logBuffer[i]
		if shouldIncludeLog(message, inboundID, inboundTag, includeSystem) {
			logs = append(logs, message)
		}
	}
	for left, right := 0, len(logs)-1; left < right; left, right = left+1, right-1 {
		logs[left], logs[right] = logs[right], logs[left]
	}
	c.JSON(http.StatusOK, gin.H{"logs": logs})
}

func resolveInboundFilter(filter string) (string, string) {
	if filter == "socks-in" || filter == "http-in" {
		return "", filter
	}
	for _, ib := range configure.GetCustomInbounds() {
		if ib.ID == filter {
			return ib.ID, ib.Tag
		}
		if ib.Tag == filter {
			return ib.ID, ib.Tag
		}
	}
	return filter, filter
}

func hasInboundMarker(message string) bool {
	return strings.Contains(message, "inbound:") ||
		strings.Contains(message, "[from: ") ||
		strings.Contains(message, " ->")
}

func matchesInboundLog(message, inboundID, inboundTag string) bool {
	if inboundID != "" {
		prefix := "inbound:" + inboundID
		if strings.Contains(message, prefix+" ") || strings.Contains(message, prefix+":") || strings.HasSuffix(message, prefix) {
			return true
		}
	}
	if inboundTag == "" {
		return false
	}
	return strings.Contains(message, "[from: "+inboundTag+"]") ||
		strings.Contains(message, "["+inboundTag+" ->")
}

func shouldIncludeLog(message, inboundID, inboundTag string, includeSystem bool) bool {
	isInboundLog := hasInboundMarker(message)
	if inboundID == "" && inboundTag == "" {
		return isInboundLog || (includeSystem && !isInboundLog)
	}
	return matchesInboundLog(message, inboundID, inboundTag) || (includeSystem && !isInboundLog)
}

// getLogsStream 通过 SSE 流式推送日志
func getLogsStream(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Access-Control-Allow-Origin", "*")

	// 创建新的日志订阅通道，携带过滤条件
	logCh := make(chan LogEntry, 50)
	sub := &logSubscriber{
		ch:           logCh,
		inbound:      c.Query("inbound"),
		alwaysSystem: c.Query("system") == "1" || c.Query("system") == "true",
	}
	sub.inbound, sub.inboundTag = resolveInboundFilter(sub.inbound)

	logMutex.Lock()
	logSubscribers = append(logSubscribers, sub)
	logMutex.Unlock()

	defer func() {
		logMutex.Lock()
		// 移除订阅者
		for i, s := range logSubscribers {
			if s == sub {
				logSubscribers = append(logSubscribers[:i], logSubscribers[i+1:]...)
				break
			}
		}
		logMutex.Unlock()
		close(logCh)
	}()

	flusher, canFlush := c.Writer.(http.Flusher)
	clientGone := c.Request.Context().Done()

	for {
		select {
		case <-clientGone:
			return
		case entry, ok := <-logCh:
			if !ok {
				return
			}
			data, _ := json.Marshal(entry)
			fmt.Fprintf(c.Writer, "data: %s\n\n", data)
			if canFlush {
				flusher.Flush()
			}
		}
	}
}

// checkKernelUpdate 检测内核是否有新版本
func checkKernelUpdate(c *gin.Context) {
	kernel := c.Param("kernel")
	if kernel != "xray" && kernel != "singbox" && kernel != "v2ray" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown kernel"})
		return
	}

	// 获取最新版本
	latestVersion, _, err := engine.GetLatestRelease(kernel)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 获取本地版本
	localVersion := engine.GetKernelVersion(kernel)

	// 仅当本地/远端都能解析出有效版本号时才比较，避免 "version" 之类文本误判
	hasUpdate := false
	if len(strings.TrimSpace(localVersion)) > 0 && len(strings.TrimSpace(latestVersion)) > 0 {
		if len(engineVersionSegments(localVersion)) > 0 && len(engineVersionSegments(latestVersion)) > 0 {
			hasUpdate = engine.CompareVersions(latestVersion, localVersion) > 0
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"kernel":        kernel,
		"localVersion":  localVersion,
		"latestVersion": latestVersion,
		"hasUpdate":     hasUpdate,
	})
}

func engineVersionSegments(v string) []int {
	v = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(v), "v"))
	var segments []int
	var current int
	inNumber := false
	for i := 0; i < len(v); i++ {
		ch := v[i]
		if ch >= '0' && ch <= '9' {
			current = current*10 + int(ch-'0')
			inNumber = true
			continue
		}
		if inNumber {
			segments = append(segments, current)
			current = 0
			inNumber = false
		}
	}
	if inNumber {
		segments = append(segments, current)
	}
	return segments
}

// ===== 路由规则 =====

func getRoutingRules(c *gin.Context) {
	rules := configure.GetRoutingRules()
	c.JSON(http.StatusOK, gin.H{"rules": rules})
}

func setRoutingRules(c *gin.Context) {
	var rules []configure.RoutingRule
	if err := c.ShouldBindJSON(&rules); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// 确保每条规则有 ID
	for i := range rules {
		if rules[i].ID == "" {
			id, _ := gonanoid.Nanoid()
			rules[i].ID = id
		}
	}
	if err := configure.SetRoutingRules(rules); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 重启代理使规则生效
	if engine.Manager.IsRunning() {
		_ = engine.Manager.Restart()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// exportRoutingRules 导出路由规则为 JSON 文件
func exportRoutingRules(c *gin.Context) {
	rules := configure.GetRoutingRules()
	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", "attachment; filename=routing-rules.json")
	c.JSON(http.StatusOK, rules)
}

// importRoutingRules 导入路由规则从 JSON 文件
func importRoutingRules(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no file provided"})
		return
	}

	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	defer src.Close()

	var rules []configure.RoutingRule
	decoder := json.NewDecoder(src)
	if err := decoder.Decode(&rules); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON format"})
		return
	}

	// 确保每条规则有 ID
	for i := range rules {
		if rules[i].ID == "" {
			id, _ := gonanoid.Nanoid()
			rules[i].ID = id
		}
	}

	if err := configure.SetRoutingRules(rules); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 重启代理使规则生效
	if engine.Manager.IsRunning() {
		_ = engine.Manager.Restart()
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "count": len(rules)})
}

// ===== 自定义入站 =====

func getCustomInbounds(c *gin.Context) {
	inbounds := configure.GetCustomInbounds()
	c.JSON(http.StatusOK, gin.H{"inbounds": inbounds})
}

type builtinProxyStatus struct {
	ID        string `json:"id"`
	Protocol  string `json:"protocol"`
	Running   bool   `json:"running"`
	Reachable bool   `json:"reachable"`
	Healthy   bool   `json:"healthy"`
	Addr      string `json:"addr,omitempty"`
	Error     string `json:"error,omitempty"`
}

func getBuiltinProxyStatuses(c *gin.Context) {
	inbounds := configure.GetCustomInbounds()
	statuses := make([]builtinProxyStatus, 0, len(inbounds))
	for _, ib := range inbounds {
		if status, ok := getBuiltinProxyStatus(ib); ok {
			statuses = append(statuses, status)
		}
	}
	c.JSON(http.StatusOK, gin.H{"statuses": statuses})
}

func restartBuiltinProxy(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing id"})
		return
	}
	inbounds := configure.GetCustomInbounds()
	found := false
	for i := range inbounds {
		if inbounds[i].ID != id {
			continue
		}
		proto := strings.ToLower(strings.TrimSpace(inbounds[i].Protocol))
		if proto != "cfdo" && proto != "cfgoodnet" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "not a builtin proxy"})
			return
		}
		stopBuiltinProxy("inbound:"+inbounds[i].ID, proto)
		restarted, err := ensureBuiltinProxyRunning(inbounds[i])
		if err != nil {
			addLog(fmt.Sprintf("❌ 重启内置出站失败 id=%s proto=%s: %v", inbounds[i].ID, proto, err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		inbounds[i] = restarted
		found = true
		break
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "builtin proxy not found"})
		return
	}
	if err := configure.SetCustomInbounds(inbounds); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	rebuildInboundProxyServers(inbounds)
	status, _ := getBuiltinProxyStatus(findBuiltinProxyByID(inbounds, id))
	c.JSON(http.StatusOK, gin.H{"ok": true, "status": status})
}

func setCustomInbounds(c *gin.Context) {
	oldInbounds := configure.GetCustomInbounds()
	var inbounds []configure.CustomInbound
	if err := c.ShouldBindJSON(&inbounds); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// 确保每个入站有 ID 和 Tag
	for i := range inbounds {
		if inbounds[i].ID == "" {
			id, _ := gonanoid.Nanoid()
			inbounds[i].ID = id
		}
		proto := strings.ToLower(strings.TrimSpace(inbounds[i].Protocol))
		if inbounds[i].Tag == "" && proto != "cfdo" && proto != "cfgoodnet" {
			inbounds[i].Tag = "inbound-" + inbounds[i].ID[:8]
		}
		if proto == "cfdo" {
			inbounds[i].Tag = ""
			inbounds[i].Listen = "127.0.0.1"
			if inbounds[i].CfDO != nil {
				inbounds[i].Port = inbounds[i].CfDO.Port
			}
		}
		if proto == "cfgoodnet" {
			inbounds[i].Tag = ""
			inbounds[i].Listen = "127.0.0.1"
			if inbounds[i].CfGoodNet != nil {
				inbounds[i].Port = inbounds[i].CfGoodNet.ListenPort
				inbounds[i].CfGoodNet.ListenHost = "127.0.0.1"
			}
		}
	}
	if err := configure.SetCustomInbounds(inbounds); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	restartIndependentProxySidecars(oldInbounds, inbounds)
	inbounds = ensureInboundProxySidecars(inbounds)
	_ = configure.SetCustomInbounds(inbounds)
	rebuildInboundProxyServers(inbounds)
	// 重启代理使入站生效
	if engine.Manager.IsRunning() {
		_ = engine.Manager.Restart()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func restartIndependentProxySidecars(oldInbounds, newInbounds []configure.CustomInbound) {
	oldHasCfdo := false
	oldHasCfgoodnet := false
	for _, ib := range oldInbounds {
		switch strings.ToLower(strings.TrimSpace(ib.Protocol)) {
		case "cfdo":
			oldHasCfdo = true
		case "cfgoodnet":
			oldHasCfgoodnet = true
		}
	}
	newHasCfdo := false
	newHasCfgoodnet := false
	for _, ib := range newInbounds {
		switch strings.ToLower(strings.TrimSpace(ib.Protocol)) {
		case "cfdo":
			newHasCfdo = true
		case "cfgoodnet":
			newHasCfgoodnet = true
		}
	}
	if oldHasCfdo || newHasCfdo {
		cfdo.StopAll()
	}
	if oldHasCfgoodnet || newHasCfgoodnet {
		cfgoodnet.StopAll()
	}
}

func ensureInboundProxySidecars(inbounds []configure.CustomInbound) []configure.CustomInbound {
	for i := range inbounds {
		restarted, err := ensureBuiltinProxyRunning(inbounds[i])
		if err != nil {
			proto := strings.ToLower(strings.TrimSpace(inbounds[i].Protocol))
			if proto == "cfdo" {
				addLog("⚠ 启动 CFDO 入站失败: " + err.Error())
			} else if proto == "cfgoodnet" {
				addLog("⚠ 启动 cfgoodnet 入站失败: " + err.Error())
			}
			continue
		}
		inbounds[i] = restarted
	}
	return inbounds
}

func ensureBuiltinProxyRunning(ib configure.CustomInbound) (configure.CustomInbound, error) {
	proto := strings.ToLower(strings.TrimSpace(ib.Protocol))
	switch proto {
	case "cfdo":
		if ib.CfDO == nil {
			return ib, nil
		}
		cfg := &cfdo.Config{
			ListenHost:           "127.0.0.1",
			ListenPort:           ib.CfDO.Port,
			Listeners:            toCFDOListenerConfigs(ib.CfDO.Listeners),
			WorkerDomain:         ib.CfDO.WorkerDomain,
			Secret:               ib.CfDO.Secret,
			Path:                 ib.CfDO.Path,
			WorkerIP:             ib.CfDO.WorkerIP,
			UseBareWS:            ib.CfDO.UseBareWS,
			AlwaysUseDO:          ib.CfDO.AlwaysUseDO,
			DOPoolSize:           ib.CfDO.DOPoolSize,
			RejectDomains:        ib.CfDO.RejectDomains,
			DOFallbackDomains:    ib.CfDO.DOFallbackDomains,
			DOFallbackExtensions: ib.CfDO.DOFallbackExtensions,
		}
		addr, err := cfdo.EnsureRunning("inbound:"+ib.ID, cfg, func(message string) {
			addLog("inbound:" + ib.ID + " " + message)
		})
		if err != nil {
			return ib, err
		}
		if _, p, err := net.SplitHostPort(addr); err == nil {
			if port, e := strconv.Atoi(p); e == nil && port > 0 {
				ib.Port = port
				ib.CfDO.Port = port
			}
		}
		return ib, nil
	case "cfgoodnet":
		if ib.CfGoodNet == nil {
			return ib, nil
		}
		cfg := &cfgoodnet.Config{
			ListenHost: "127.0.0.1",
			ListenPort: ib.CfGoodNet.ListenPort,
			CfProxy:    ib.CfGoodNet.CfProxy,
			CfGoodIP:   ib.CfGoodNet.CfGoodIP,
			EnableXFF:  ib.CfGoodNet.EnableXFF,
			DataDir:    engine.DataDir(),
		}
		for _, r := range ib.CfGoodNet.Rules {
			cfg.Rules = append(cfg.Rules, cfgoodnet.Rule{Pattern: r.Pattern, Action: r.Action})
		}
		addr, err := cfgoodnet.EnsureRunning("inbound:"+ib.ID, cfg, func(message string) {
			addLog("inbound:" + ib.ID + " " + message)
		})
		if err != nil {
			return ib, err
		}
		if _, p, err := net.SplitHostPort(addr); err == nil {
			if port, e := strconv.Atoi(p); e == nil && port > 0 {
				ib.Port = port
				ib.CfGoodNet.ListenPort = port
				ib.CfGoodNet.ListenHost = "127.0.0.1"
			}
		}
		return ib, nil
	default:
		return ib, nil
	}
}

func toCFDOListenerConfigs(listeners []configure.CfDOListener) []cfdo.ListenerConfig {
	if len(listeners) == 0 {
		return nil
	}
	out := make([]cfdo.ListenerConfig, 0, len(listeners))
	for _, l := range listeners {
		out = append(out, cfdo.ListenerConfig{
			ListenPort: l.ListenPort,
			WorkerIP:   l.WorkerIP,
		})
	}
	return out
}

func stopBuiltinProxy(key, protocol string) {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "cfdo":
		cfdo.Stop(key)
	case "cfgoodnet":
		cfgoodnet.Stop(key)
	}
}

func getBuiltinProxyStatus(ib configure.CustomInbound) (builtinProxyStatus, bool) {
	proto := strings.ToLower(strings.TrimSpace(ib.Protocol))
	if proto != "cfdo" && proto != "cfgoodnet" {
		return builtinProxyStatus{}, false
	}
	key := "inbound:" + ib.ID
	addr := ""
	switch proto {
	case "cfdo":
		addr = cfdo.Addr(key)
	case "cfgoodnet":
		addr = cfgoodnet.Addr(key)
	}
	status := builtinProxyStatus{
		ID:       ib.ID,
		Protocol: proto,
		Addr:     addr,
		Running:  addr != "",
	}
	if addr == "" {
		status.Error = "sidecar not running"
		return status, true
	}
	conn, err := net.DialTimeout("tcp", addr, 1500*time.Millisecond)
	if err != nil {
		status.Error = err.Error()
		return status, true
	}
	_ = conn.Close()
	status.Reachable = true
	status.Healthy = true
	return status, true
}

func findBuiltinProxyByID(inbounds []configure.CustomInbound, id string) configure.CustomInbound {
	for _, ib := range inbounds {
		if ib.ID == id {
			return ib
		}
	}
	return configure.CustomInbound{}
}

// EnsureIndependentProxyInbounds 在服务启动时确保 cfdo/cfgoodnet 独立代理就绪。
func EnsureIndependentProxyInbounds() {
	inbounds := configure.GetCustomInbounds()
	restartIndependentProxySidecars(inbounds, inbounds)
	inbounds = ensureInboundProxySidecars(inbounds)
	_ = configure.SetCustomInbounds(inbounds)
	rebuildInboundProxyServers(inbounds)
}

func rebuildInboundProxyServers(inbounds []configure.CustomInbound) {
	cleanupLegacyInboundRefsInGroups()
	groups := configure.GetGroups()
	serverGroupIndex := -1
	for i := range groups {
		if !groups[i].FromSub && strings.EqualFold(strings.TrimSpace(groups[i].Name), "SERVER") {
			serverGroupIndex = i
			break
		}
	}
	if serverGroupIndex < 0 {
		id, _ := gonanoid.Nanoid()
		g := &configure.Group{
			ID:        id,
			Name:      "SERVER",
			FromSub:   false,
			Servers:   []configure.ServerRef{},
			CreatedAt: time.Now(),
		}
		if err := configure.AppendGroup(g); err != nil {
			return
		}
		groups = configure.GetGroups()
		serverGroupIndex = len(groups) - 1
	}

	oldServers := configure.GetServers()
	oldToNewIndex := map[int]int{}
	newServers := make([]configure.ServerRaw, 0, len(oldServers))
	serverIndexBySource := map[string]int{}
	for i, s := range oldServers {
		if strings.HasPrefix(s.Source, "inbound:") {
			continue
		}
		oldToNewIndex[i] = len(newServers)
		newServers = append(newServers, s)
	}
	for _, ib := range inbounds {
		p := strings.ToLower(strings.TrimSpace(ib.Protocol))
		if p != "cfdo" && p != "cfgoodnet" {
			continue
		}
		host := "127.0.0.1"
		name := ib.Name
		if strings.TrimSpace(name) == "" {
			name = ib.Tag
		}
		if p == "cfdo" {
			ports := cfdoListenPortsForInbound(ib)
			for _, port := range ports {
				if port <= 0 {
					continue
				}
				source := fmt.Sprintf("inbound:%s:cfdo:%d", ib.ID, port)
				serverName := name
				if len(ports) > 1 {
					serverName = fmt.Sprintf("%s [%d]", name, port)
				}
				serverIndexBySource[source] = len(newServers)
				newServers = append(newServers, configure.ServerRaw{
					Link:    fmt.Sprintf("socks5://%s:%d#%s", host, port, url.QueryEscape(serverName)),
					Name:    serverName,
					Host:    host,
					Port:    port,
					Type:    "socks5",
					Latency: -1,
					Source:  source,
				})
			}
		} else {
			port := ib.Port
			if ib.CfGoodNet != nil && ib.CfGoodNet.ListenPort > 0 {
				port = ib.CfGoodNet.ListenPort
			}
			if port <= 0 {
				continue
			}
			source := "inbound:" + ib.ID
			serverIndexBySource[source] = len(newServers)
			newServers = append(newServers, configure.ServerRaw{
				Link:    fmt.Sprintf("http://%s:%d#%s", host, port, url.QueryEscape(name)),
				Name:    name,
				Host:    host,
				Port:    port,
				Type:    "http",
				Latency: -1,
				Source:  source,
			})
		}
	}

	if err := configure.SetServers(newServers); err != nil {
		return
	}

	for i := range groups {
		g := groups[i]
		if len(g.Servers) == 0 {
			continue
		}
		filtered := make([]configure.ServerRef, 0, len(g.Servers))
		seen := map[string]struct{}{}
		changed := false
		for _, ref := range g.Servers {
			if ref.Type != "server" {
				key := fmt.Sprintf("%s:%d:%d", ref.Type, ref.Index, ref.Sub)
				if _, ok := seen[key]; ok {
					changed = true
					continue
				}
				seen[key] = struct{}{}
				filtered = append(filtered, ref)
				continue
			}
			if ref.Index < 0 || ref.Index >= len(oldServers) {
				changed = true
				continue
			}
			oldServer := oldServers[ref.Index]
			newIndex := -1
			if strings.HasPrefix(oldServer.Source, "inbound:") {
				var ok bool
				newIndex, ok = serverIndexBySource[oldServer.Source]
				if !ok {
					changed = true
					continue
				}
			} else {
				var ok bool
				newIndex, ok = oldToNewIndex[ref.Index]
				if !ok {
					changed = true
					continue
				}
			}
			if newIndex != ref.Index {
				changed = true
			}
			ref.Index = newIndex
			key := fmt.Sprintf("%s:%d:%d", ref.Type, ref.Index, ref.Sub)
			if _, ok := seen[key]; ok {
				changed = true
				continue
			}
			seen[key] = struct{}{}
			filtered = append(filtered, ref)
		}
		if i == serverGroupIndex {
			for _, ib := range inbounds {
				p := strings.ToLower(strings.TrimSpace(ib.Protocol))
				if p != "cfdo" && p != "cfgoodnet" {
					continue
				}
				if p == "cfdo" {
					for _, port := range cfdoListenPortsForInbound(ib) {
						source := fmt.Sprintf("inbound:%s:cfdo:%d", ib.ID, port)
						serverIndex, ok := serverIndexBySource[source]
						if !ok {
							continue
						}
						ref := configure.ServerRef{Type: "server", Index: serverIndex, Sub: 0}
						key := fmt.Sprintf("%s:%d:%d", ref.Type, ref.Index, ref.Sub)
						if _, exists := seen[key]; exists {
							continue
						}
						seen[key] = struct{}{}
						filtered = append(filtered, ref)
						changed = true
					}
					continue
				}
				source := "inbound:" + ib.ID
				serverIndex, ok := serverIndexBySource[source]
				if !ok {
					continue
				}
				ref := configure.ServerRef{Type: "server", Index: serverIndex, Sub: 0}
				key := fmt.Sprintf("%s:%d:%d", ref.Type, ref.Index, ref.Sub)
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				filtered = append(filtered, ref)
				changed = true
			}
		}
		if changed {
			g.Servers = filtered
			_ = configure.SetGroup(i, &g)
		}
	}
}

func cfdoListenPortsForInbound(ib configure.CustomInbound) []int {
	if ib.CfDO == nil {
		if ib.Port > 0 {
			return []int{ib.Port}
		}
		return nil
	}
	seen := map[int]struct{}{}
	var out []int
	for _, l := range ib.CfDO.Listeners {
		if l.ListenPort <= 0 {
			continue
		}
		if _, ok := seen[l.ListenPort]; ok {
			continue
		}
		seen[l.ListenPort] = struct{}{}
		out = append(out, l.ListenPort)
	}
	if len(out) > 0 {
		return out
	}
	port := ib.CfDO.Port
	if port <= 0 {
		port = ib.Port
	}
	if port > 0 {
		return []int{port}
	}
	return nil
}

func cleanupLegacyInboundRefsInGroups() {
	groups := configure.GetGroups()
	for i := range groups {
		g := groups[i]
		if len(g.Servers) == 0 {
			continue
		}
		filtered := make([]configure.ServerRef, 0, len(g.Servers))
		changed := false
		for _, ref := range g.Servers {
			if strings.EqualFold(strings.TrimSpace(ref.Type), "inbound") {
				changed = true
				continue
			}
			filtered = append(filtered, ref)
		}
		if changed {
			g.Servers = filtered
			_ = configure.SetGroup(i, &g)
		}
	}
}

func getRuleSetInfos(c *gin.Context) {
	infos := engine.GetRuleSetInfos()
	c.JSON(http.StatusOK, gin.H{"infos": infos})
}
