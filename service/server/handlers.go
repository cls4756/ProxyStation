package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ProxyStation/proxystation/core/engine"
	"github.com/ProxyStation/proxystation/db/configure"
	"github.com/ProxyStation/proxystation/pkg/observatory"
	"github.com/ProxyStation/proxystation/pkg/subscription"
	"github.com/gin-gonic/gin"
	gonanoid "github.com/matoous/go-nanoid"
)

// 日志缓冲区和订阅者管理
var (
	logMutex       sync.RWMutex
	logBuffer      []string
	logSubscribers []chan LogEntry
	maxLogLines    = 500
	logFilePath    string
	maxLogFileSize int64 = 2 * 1024 * 1024 // 2MB
)

// InitLogFile 初始化日志文件路径
func InitLogFile(dataDir string) {
	logFilePath = filepath.Join(dataDir, "proxystation.log")
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
	logMutex.Lock()
	defer logMutex.Unlock()

	now := time.Now().Format("15:04:05")
	formattedMsg := fmt.Sprintf("[%s] %s", now, msg)
	logBuffer = append(logBuffer, formattedMsg)

	// 如果超过最大行数，写入文件并清空缓冲区
	if len(logBuffer) > maxLogLines {
		writeLogsToFile(logBuffer[:len(logBuffer)-maxLogLines])
		logBuffer = logBuffer[len(logBuffer)-maxLogLines:]
	}

	// 创建日志条目
	entry := LogEntry{
		Time:    now,
		Message: msg,
		Level:   level,
	}

	// 广播给所有订阅者
	for _, ch := range logSubscribers {
		select {
		case ch <- entry:
		default:
			// 如果通道满了，跳过
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
		if isHTTPURL(line) {
			subURLs = append(subURLs, line)
		} else if isNodeLink(line) {
			nodeLinks = append(nodeLinks, line)
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

func isHTTPURL(s string) bool {
	lower := strings.ToLower(s)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return false
	}
	// 区分订阅 URL 和 HTTP 代理节点链接
	// HTTP 代理节点格式：http://[user:pass@]host:port  —— 有端口，路径极短或无路径
	// 订阅 URL 格式：https://example.com/subscribe/xxx  —— 有路径
	//
	// 判断规则：
	//   1. 包含 @ 且 @ 在第一个 / 之前 → 代理节点（http://user:pass@host:port）
	//   2. 去掉 scheme 后，host:port 部分后面没有路径（或路径为 /）→ 可能是代理节点，但不确定
	//   3. 有明显路径（/xxx/yyy）→ 订阅 URL
	//
	// 最可靠的判断：有 @ 符号在 host 部分 → 代理节点
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
		return false // 是节点链接，不是订阅
	}
	// 没有路径或路径只有 / → 不像订阅 URL，当节点处理
	if slashIdx < 0 || (slashIdx == len(afterScheme)-1) {
		return false
	}
	// 有实质路径 → 当订阅 URL
	return true
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

func getServers(c *gin.Context) {
	servers := configure.GetServers()
	c.JSON(http.StatusOK, gin.H{"servers": servers})
}

// searchServers 搜索节点（跨手动节点和所有订阅）
func searchServers(c *gin.Context) {
	q := strings.ToLower(c.Query("q"))
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "q is required"})
		return
	}
	type result struct {
		Server configure.ServerRaw `json:"server"`
		Ref    configure.NodeRef   `json:"ref"`
	}
	var results []result
	// 搜索手动节点
	for i, s := range configure.GetServers() {
		if matchServer(s, q) {
			results = append(results, result{Server: s, Ref: configure.NodeRef{Type: "server", Index: i}})
		}
	}
	// 搜索订阅节点
	for si, sub := range configure.GetSubscriptions() {
		for i, s := range sub.Servers {
			if matchServer(s, q) {
				results = append(results, result{Server: s, Ref: configure.NodeRef{Type: "sub_server", Index: i, Sub: si}})
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"results": results, "total": len(results)})
}

func matchServer(s configure.ServerRaw, q string) bool {
	return strings.Contains(strings.ToLower(s.Name), q) ||
		strings.Contains(strings.ToLower(s.Host), q) ||
		strings.Contains(strings.ToLower(s.Type), q)
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
		sni := getString("sni")
		insecure := getBool("skip-cert-verify")
		q := url.Values{}
		if sni != "" {
			q.Set("sni", sni)
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

	default:
		// 其他协议直接返回原始 link 或简单格式
		return fmt.Sprintf("%s://%s:%d#%s", s.Type, host, port, name)
	}
}

// copyServerToGroup 将节点复制到指定分组
func copyServerToGroup(c *gin.Context) {
	var req struct {
		Ref     configure.ServerRef `json:"ref"`
		GroupID string              `json:"groupId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	groupIndex, _ := configure.GetGroupByID(req.GroupID)
	if groupIndex < 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}
	if err := configure.AddServerToGroup(groupIndex, req.Ref); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---- Subscriptions ----

func getSubscriptions(c *gin.Context) {
	subs := configure.GetSubscriptions()
	c.JSON(http.StatusOK, gin.H{"subscriptions": subs})
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
	subClient := &http.Client{
		Transport: &http.Transport{
			DialContext:         (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout: 30 * time.Second,
		},
	}
	resp, err := subClient.Get(sub.URL)
	if err != nil {
		addLog(fmt.Sprintf("❌ 拉取订阅失败: %v", err))
		return
	}
	defer resp.Body.Close()
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
	for i := range servers {
		servers[i].Source = sub.ID
	}
	sub.Servers = servers
	sub.UpdatedAt = time.Now()

	_ = configure.SetSubscription(index, sub)
	addLog(fmt.Sprintf("✅ 订阅更新成功: %s (%d 个节点)", sub.Name, len(servers)))

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
	}
}

// ---- Groups ----

func getGroups(c *gin.Context) {
	groups := configure.GetGroups()
	c.JSON(http.StatusOK, gin.H{"groups": groups})
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

// ---- Outbounds ----

func getOutbounds(c *gin.Context) {
	names := configure.GetOutboundNames()
	var outbounds []*configure.Outbound
	for _, name := range names {
		o := configure.GetOutbound(name)
		if o == nil {
			o = &configure.Outbound{
				Name:   name,
				Target: configure.OutboundTarget{},
			}
		}
		outbounds = append(outbounds, o)
	}
	c.JSON(http.StatusOK, gin.H{"outbounds": outbounds})
}

func createOutbound(c *gin.Context) {
	var req configure.Outbound
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
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

func connectOutbound(c *gin.Context) {
	name := c.Param("name")
	var req configure.OutboundTarget
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	o := configure.GetOutbound(name)
	if o == nil {
		o = &configure.Outbound{Name: name}
	}
	o.Target = req
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
		Refs []configure.NodeRef `json:"refs"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	addLog(fmt.Sprintf("⚡ 开始测速 %d 个节点...", len(req.Refs)))

	type result struct {
		Ref     configure.NodeRef `json:"ref"`
		Latency int               `json:"latency"` // ms, -1=timeout
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []result
	)

	for _, ref := range req.Refs {
		ref := ref
		wg.Add(1)
		go func() {
			defer wg.Done()
			lat := probeNodeLatency(ref)
			mu.Lock()
			results = append(results, result{Ref: ref, Latency: lat})
			mu.Unlock()
			// 顺便持久化延迟
			saveLatency(ref, lat)
		}()
	}
	wg.Wait()
	addLog(fmt.Sprintf("✅ 测速完成"))
	c.JSON(http.StatusOK, gin.H{"results": results})
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

	type result struct {
		Ref     configure.ServerRef `json:"ref"`
		Latency int                 `json:"latency"`
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []result
	)

	for _, ref := range group.Servers {
		ref := ref
		wg.Add(1)
		go func() {
			defer wg.Done()
			nodeRef := configure.NodeRef{Type: ref.Type, Index: ref.Index, Sub: ref.Sub}
			lat := probeNodeLatency(nodeRef)
			mu.Lock()
			results = append(results, result{Ref: ref, Latency: lat})
			mu.Unlock()
			saveLatency(nodeRef, lat)
		}()
	}
	wg.Wait()
	c.JSON(http.StatusOK, gin.H{"results": results})
}

// probeNodeLatency 测速，先尝试 HTTP，失败则用 TCP，返回 ms，-1 表示超时
func probeNodeLatency(ref configure.NodeRef) int {
	var host string
	var port int
	switch ref.Type {
	case "server":
		s := configure.GetServer(ref.Index)
		if s == nil {
			return -1
		}
		host, port = s.Host, s.Port
	case "sub_server":
		sub := configure.GetSubscription(ref.Sub)
		if sub == nil || ref.Index >= len(sub.Servers) {
			return -1
		}
		s := sub.Servers[ref.Index]
		host, port = s.Host, s.Port
	default:
		return -1
	}
	if host == "" || port == 0 {
		return -1
	}

	// 先尝试 HTTP 测试（更准确）
	latency := probeHTTP(host, port)
	if latency > 0 {
		return latency
	}

	// HTTP 失败则用 TCP 测试
	return probeTCP(host, port)
}

// probeHTTP 通过 HTTP 请求测试延迟
func probeHTTP(host string, port int) int {
	url := fmt.Sprintf("http://%s:%d/", host, port)
	start := time.Now()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return -1
	}
	defer resp.Body.Close()
	return int(time.Since(start).Milliseconds())
}

// probeTCP 通过 TCP 连接测试延迟
func probeTCP(host string, port int) int {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 5*time.Second)
	if err != nil {
		return -1
	}
	_ = conn.Close()
	return int(time.Since(start).Milliseconds())
}

// saveLatency 将测速结果写回存储
func saveLatency(ref configure.NodeRef, latency int) {
	now := time.Now().Unix()
	switch ref.Type {
	case "server":
		s := configure.GetServer(ref.Index)
		if s == nil {
			return
		}
		s.Latency = latency
		s.LastProbeTime = now
		_ = configure.SetServer(ref.Index, s)
	case "sub_server":
		sub := configure.GetSubscription(ref.Sub)
		if sub == nil || ref.Index >= len(sub.Servers) {
			return
		}
		sub.Servers[ref.Index].Latency = latency
		sub.Servers[ref.Index].LastProbeTime = now
		_ = configure.SetSubscription(ref.Sub, sub)
	}
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
	if s.KernelMode == "" {
		s.KernelMode = "auto"
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
		kernelMeta[name] = engine.GetKernelMeta(name)
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
	c.JSON(http.StatusOK, gin.H{"logs": logBuffer})
}

// getLogsStream 通过 SSE 流式推送日志
func getLogsStream(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Access-Control-Allow-Origin", "*")

	// 创建新的日志订阅通道
	logCh := make(chan LogEntry, 50)

	logMutex.Lock()
	logSubscribers = append(logSubscribers, logCh)
	logMutex.Unlock()

	defer func() {
		logMutex.Lock()
		// 移除订阅者
		for i, ch := range logSubscribers {
			if ch == logCh {
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

	// 比较版本
	hasUpdate := engine.CompareVersions(latestVersion, localVersion) > 0

	c.JSON(http.StatusOK, gin.H{
		"kernel":        kernel,
		"localVersion":  localVersion,
		"latestVersion": latestVersion,
		"hasUpdate":     hasUpdate,
	})
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

func setCustomInbounds(c *gin.Context) {
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
		if inbounds[i].Tag == "" {
			inbounds[i].Tag = "inbound-" + inbounds[i].ID[:8]
		}
	}
	if err := configure.SetCustomInbounds(inbounds); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 重启代理使入站生效
	if engine.Manager.IsRunning() {
		_ = engine.Manager.Restart()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func getRuleSetInfos(c *gin.Context) {
	infos := engine.GetRuleSetInfos()
	c.JSON(http.StatusOK, gin.H{"infos": infos})
}
