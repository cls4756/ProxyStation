// observatory 负责对绑定了 Group 的出站进行定期真连接测速，并按阈值自动切换到更优节点
package observatory

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ProxyStation/proxystation/core/engine"
	"github.com/ProxyStation/proxystation/db/configure"
	"github.com/ProxyStation/proxystation/pkg/probe"
	xproxy "golang.org/x/net/proxy"
)

const (
	defaultProbeIntervalSec = 300
	defaultSwitchThreshold  = 100
)

// Observatory 管理所有出站的测速任务
type Observatory struct {
	mu     sync.Mutex
	tasks  map[string]*probeTask // outbound name -> task
	stopCh chan struct{}
}

type probeTask struct {
	outboundName string
	ticker       *time.Ticker
	stopCh       chan struct{}
}

type probeResult struct {
	ref     configure.NodeRef
	latency int
}

var global = &Observatory{
	tasks:  make(map[string]*probeTask),
	stopCh: make(chan struct{}),
}

// 串行化真连接探测，避免隐藏探测出站互相覆盖。
var probeCoreMu sync.Mutex
var switchStateMu sync.Mutex
var lastSwitchAt = map[string]time.Time{}
var pendingBestCopyMu sync.Mutex
var pendingBestCopyGroups = map[string]bool{}
var logCallback func(string)

func SetLogCallback(cb func(string)) {
	logCallback = cb
}

func addLog(msg string) {
	if logCallback != nil {
		logCallback(msg)
	}
}

// Start 启动 observatory，对所有绑定了 Group 的出站开始测速
func Start() {
	Stop()
	outbounds := configure.GetOutboundNames()
	for _, name := range outbounds {
		o := configure.GetOutbound(name)
		if o == nil {
			continue
		}
		if o.Target.TargetType == "group" {
			global.startTask(name, o)
		}
	}
}

// Stop 停止所有测速任务
func Stop() {
	global.mu.Lock()
	defer global.mu.Unlock()
	for _, task := range global.tasks {
		close(task.stopCh)
	}
	global.tasks = make(map[string]*probeTask)
	switchStateMu.Lock()
	lastSwitchAt = map[string]time.Time{}
	switchStateMu.Unlock()
}

// Reload 重新加载所有分组出站任务（设置变更后调用）
func Reload() {
	Start()
}

// OnOutboundUpdated 当出站配置变更时调用，重启对应的测速任务
func OnOutboundUpdated(name string) {
	global.mu.Lock()
	if task, ok := global.tasks[name]; ok {
		close(task.stopCh)
		delete(global.tasks, name)
	}
	global.mu.Unlock()

	o := configure.GetOutbound(name)
	if o != nil && o.Target.TargetType == "group" {
		global.startTask(name, o)
	}
}

// OnGroupUpdated runs an immediate probe after a group membership/source update.
// If the group is bound to an outbound, reuse that outbound's switch logic;
// otherwise just refresh node latency values for display.
func OnGroupUpdated(groupID string) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return
	}
	triggered := false
	for _, name := range configure.GetOutboundNames() {
		o := configure.GetOutbound(name)
		if o == nil || o.Target.TargetType != "group" || o.Target.GroupID != groupID {
			continue
		}
		triggered = true
		markPendingBestCopy(groupID)
		OnOutboundUpdated(name)
	}
	if !triggered {
		go probeGroupLatency(groupID)
	}
}

func (obs *Observatory) startTask(name string, o *configure.Outbound) {
	interval := resolveProbeInterval()
	if o.ProbeInterval != "" {
		if d, err := time.ParseDuration(o.ProbeInterval); err == nil && d > 0 {
			interval = d
		}
	}

	task := &probeTask{
		outboundName: name,
		ticker:       time.NewTicker(interval),
		stopCh:       make(chan struct{}),
	}

	obs.mu.Lock()
	obs.tasks[name] = task
	obs.mu.Unlock()

	// 先尝试使用已有的测速数据选择最快的节点
	selectBestNodeFromExisting(name, o)

	// 立即测一次（后台异步）
	go probeAndUpdate(name)

	go func() {
		for {
			select {
			case <-task.ticker.C:
				probeAndUpdate(name)
			case <-task.stopCh:
				task.ticker.Stop()
				return
			}
		}
	}()
}

// selectBestNodeFromExisting 从已有的测速数据中选择最快的节点
func selectBestNodeFromExisting(outboundName string, o *configure.Outbound) {
	if o.Target.TargetType != "group" {
		return
	}

	_, group := configure.GetGroupByID(o.Target.GroupID)
	if group == nil {
		return
	}

	var available []probeResult

	for _, ref := range group.Servers {
		nodeRef := configure.NodeRef{Type: ref.Type, Index: ref.Index, Sub: ref.Sub}
		latency := readLatency(nodeRef)
		if latency > 0 {
			available = append(available, probeResult{ref: nodeRef, latency: latency})
		}
	}

	if len(available) > 0 {
		sort.Slice(available, func(i, j int) bool {
			return available[i].latency < available[j].latency
		})
		best := available[0]
		_ = configure.SetOutboundActiveNode(outboundName, &best.ref)
	}
}

// probeAndUpdate 对出站绑定 Group 的所有节点测速，按阈值更新最优节点
func probeAndUpdate(outboundName string) {
	o := configure.GetOutbound(outboundName)
	if o == nil || o.Target.TargetType != "group" {
		return
	}

	_, group := configure.GetGroupByID(o.Target.GroupID)
	if group == nil {
		return
	}

	results := make([]probeResult, 0, len(group.Servers))
	for _, ref := range group.Servers {
		nodeRef := configure.NodeRef{Type: ref.Type, Index: ref.Index, Sub: ref.Sub}
		latency := probeNodeByDefaultMode(nodeRef)
		results = append(results, probeResult{ref: nodeRef, latency: latency})
		saveLatency(nodeRef, latency)
	}

	available := make([]probeResult, 0, len(results))
	for _, r := range results {
		if r.latency > 0 {
			available = append(available, r)
		}
	}
	if shouldCopyBestNodeForGroup(o.Target.GroupID) {
		copyNodesByRules(o.Target.GroupID, results)
	}
	if len(available) == 0 {
		addLog(fmt.Sprintf("🔎 [%s] 分组测速完成：无可用节点，保持当前节点", outboundName))
		return
	}

	sort.Slice(available, func(i, j int) bool {
		return available[i].latency < available[j].latency
	})
	best := available[0]

	current := o.Target.ActiveNodeRef
	if current == nil {
		_ = configure.SetOutboundActiveNode(outboundName, &best.ref)
		restartEngineIfRunning(outboundName)
		markSwitched(outboundName)
		addLog(fmt.Sprintf("🔁 [%s] 切换节点：初次选择为 %s:%d:%d (%dms)", outboundName, best.ref.Type, best.ref.Sub, best.ref.Index, best.latency))
		return
	}
	if nodeRefEqual(current, &best.ref) {
		addLog(fmt.Sprintf("✅ [%s] 当前已是最优节点 (%dms)，不切换", outboundName, best.latency))
		return
	}
	ok, reason := shouldSwitch(outboundName, *current, available, best)
	if ok {
		_ = configure.SetOutboundActiveNode(outboundName, &best.ref)
		restartEngineIfRunning(outboundName)
		markSwitched(outboundName)
		addLog(fmt.Sprintf("🔁 [%s] 切换节点：%s:%d:%d -> %s:%d:%d", outboundName, current.Type, current.Sub, current.Index, best.ref.Type, best.ref.Sub, best.ref.Index))
	} else {
		addLog(fmt.Sprintf("⏸ [%s] 不切换：%s", outboundName, reason))
	}
}

func probeGroupLatency(groupID string) {
	_, group := configure.GetGroupByID(groupID)
	if group == nil {
		return
	}
	results := make([]probeResult, 0, len(group.Servers))
	for _, ref := range group.Servers {
		nodeRef := configure.NodeRef{Type: ref.Type, Index: ref.Index, Sub: ref.Sub}
		latency := probeNodeByDefaultMode(nodeRef)
		saveLatency(nodeRef, latency)
		results = append(results, probeResult{ref: nodeRef, latency: latency})
	}
	copyNodesByRules(groupID, results)
}

func markPendingBestCopy(groupID string) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return
	}
	pendingBestCopyMu.Lock()
	pendingBestCopyGroups[groupID] = true
	pendingBestCopyMu.Unlock()
}

func shouldCopyBestNodeForGroup(groupID string) bool {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return false
	}
	pendingBestCopyMu.Lock()
	defer pendingBestCopyMu.Unlock()
	if !pendingBestCopyGroups[groupID] {
		return false
	}
	delete(pendingBestCopyGroups, groupID)
	return true
}

func copyNodesByRules(sourceGroupID string, results []probeResult) {
	sourceGroupID = strings.TrimSpace(sourceGroupID)
	if sourceGroupID == "" || len(results) == 0 {
		return
	}

	available := make([]probeResult, 0, len(results))
	for _, r := range results {
		if r.latency > 0 {
			available = append(available, r)
		}
	}
	if len(available) == 0 {
		return
	}
	sort.Slice(available, func(i, j int) bool {
		return available[i].latency < available[j].latency
	})

	setting := configure.GetSettingNotNil()
	for _, rule := range setting.SubscriptionBestNodeCopyRules {
		if !containsGroupID(rule.SourceGroupIDs, sourceGroupID) {
			continue
		}
		// 每条规则的复制数量可以不同，故在规则内取候选
		picked := available
		if n := rule.CopyCount(); n > 0 && n < len(picked) {
			picked = picked[:n]
		}
		for _, targetGroupID := range uniqueGroupIDs(rule.TargetGroupIDs) {
			if targetGroupID == sourceGroupID {
				continue
			}
			copyNodesToGroup(sourceGroupID, targetGroupID, picked)
		}
	}
}

// copyNodesToGroup 把候选节点复制进目标分组，已存在的跳过。
// 每个节点单独判重，因此重复调用是幂等的。
func copyNodesToGroup(sourceGroupID, targetGroupID string, picked []probeResult) {
	groupIndex, targetGroup := configure.GetGroupByID(targetGroupID)
	if targetGroup == nil || targetGroup.FromSub {
		return
	}

	copied := 0
	for _, r := range picked {
		server := serverRawByRef(r.ref)
		if server == nil {
			continue
		}
		if groupHasServer(targetGroup.Servers, *server) {
			continue
		}
		localRef, ok := ensureLocalCopy(*server)
		if !ok {
			continue
		}
		targetGroup.Servers = append(targetGroup.Servers, localRef)
		copied++
	}
	if copied == 0 {
		return
	}
	if err := configure.SetGroup(groupIndex, targetGroup); err != nil {
		addLog(fmt.Sprintf("⚠️ 复制节点到分组失败：%s -> %s: %v", sourceGroupID, targetGroupID, err))
		return
	}
	addLog(fmt.Sprintf("📋 已复制 %d 个节点到分组：%s -> %s", copied, sourceGroupID, targetGroup.Name))
}

func containsGroupID(ids []string, target string) bool {
	for _, id := range ids {
		if strings.TrimSpace(id) == target {
			return true
		}
	}
	return false
}

func ensureLocalCopy(server configure.ServerRaw) (configure.ServerRef, bool) {
	servers := configure.GetServers()
	for i, existing := range servers {
		if sameServer(existing, server) {
			return configure.ServerRef{Type: "server", Index: i, Sub: 0}, true
		}
	}

	server.Source = "manual"
	server.Latency = -1
	server.LastProbeTime = 0
	index := len(servers)
	if err := configure.AppendServers([]*configure.ServerRaw{&server}); err != nil {
		addLog(fmt.Sprintf("⚠️ 创建本地节点副本失败：%v", err))
		return configure.ServerRef{}, false
	}
	return configure.ServerRef{Type: "server", Index: index, Sub: 0}, true
}

func groupHasServer(refs []configure.ServerRef, server configure.ServerRaw) bool {
	for _, ref := range refs {
		existing := serverRawByRef(configure.NodeRef{Type: ref.Type, Index: ref.Index, Sub: ref.Sub})
		if existing != nil && sameServer(*existing, server) {
			return true
		}
	}
	return false
}

func uniqueGroupIDs(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, id)
	}
	return result
}

func sameServer(a, b configure.ServerRaw) bool {
	if strings.TrimSpace(a.Link) != "" && strings.TrimSpace(a.Link) == strings.TrimSpace(b.Link) {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(a.Type), strings.TrimSpace(b.Type)) &&
		strings.EqualFold(strings.TrimSpace(a.Host), strings.TrimSpace(b.Host)) &&
		a.Port == b.Port &&
		strings.TrimSpace(a.Name) == strings.TrimSpace(b.Name)
}

func resolveProbeInterval() time.Duration {
	setting := configure.GetSettingNotNil()
	sec := setting.GroupRealProbeIntervalSec
	if sec <= 0 {
		sec = defaultProbeIntervalSec
	}
	return time.Duration(sec) * time.Second
}

func resolveSwitchThreshold() int {
	setting := configure.GetSettingNotNil()
	if setting.GroupSwitchThresholdMs < 0 {
		return defaultSwitchThreshold
	}
	return setting.GroupSwitchThresholdMs
}

func resolveSwitchCooldown() time.Duration {
	setting := configure.GetSettingNotNil()
	if setting.GroupSwitchCooldownSec < 0 {
		return 600 * time.Second
	}
	return time.Duration(setting.GroupSwitchCooldownSec) * time.Second
}

func shouldSwitch(outboundName string, current configure.NodeRef, available []probeResult, best probeResult) (bool, string) {
	currentLatency := -1
	for _, item := range available {
		if nodeRefEqual(&current, &item.ref) {
			currentLatency = item.latency
			break
		}
	}
	if currentLatency <= 0 {
		return true, "当前节点无有效延迟，允许切换"
	}
	threshold := resolveSwitchThreshold()
	if (currentLatency - best.latency) < threshold {
		return false, fmt.Sprintf("延迟差值不足（当前 %dms，最优 %dms，阈值 %dms）", currentLatency, best.latency, threshold)
	}
	cooldown := resolveSwitchCooldown()
	if cooldown <= 0 {
		return true, "满足阈值且未设置冷却"
	}
	switchStateMu.Lock()
	defer switchStateMu.Unlock()
	last, ok := lastSwitchAt[outboundName]
	if !ok {
		return true, "满足阈值且无冷却历史"
	}
	elapsed := time.Since(last)
	if elapsed >= cooldown {
		return true, "满足阈值且已过冷却期"
	}
	remain := int((cooldown - elapsed).Seconds())
	if remain < 1 {
		remain = 1
	}
	return false, fmt.Sprintf("冷却中，剩余约 %d 秒", remain)
}

func markSwitched(outboundName string) {
	switchStateMu.Lock()
	lastSwitchAt[outboundName] = time.Now()
	switchStateMu.Unlock()
}

func nodeRefEqual(a, b *configure.NodeRef) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Type == b.Type && a.Index == b.Index && a.Sub == b.Sub
}

func readLatency(ref configure.NodeRef) int {
	switch ref.Type {
	case "server":
		s := configure.GetServer(ref.Index)
		if s == nil {
			return -1
		}
		return s.Latency
	case "sub_server":
		sub := configure.GetSubscription(ref.Sub)
		if sub == nil || ref.Index >= len(sub.Servers) {
			return -1
		}
		return sub.Servers[ref.Index].Latency
	default:
		return -1
	}
}

func probeNodeViaCore(ref configure.NodeRef) int {
	probeCoreMu.Lock()
	defer probeCoreMu.Unlock()

	if !engine.Manager.IsRunning() {
		return -1
	}
	if !fastReachableRef(ref) {
		return -1
	}
	if err := ensureProbeOutboundForNode(ref); err != nil {
		return -1
	}
	return probeViaCoreTargets()
}

func probeViaCoreTargets() int {
	targets := parseProbeTargets(configure.GetSettingNotNil().ProbeTargets)
	if len(targets) == 0 {
		targets = []string{"www.gstatic.com", "www.google.com", "www.youtube.com", "cp.cloudflare.com", "example.com"}
	}
	for _, host := range targets {
		if ms := probeViaCoreSocks(host, 4500*time.Millisecond); ms > 0 {
			return ms
		}
	}
	return -1
}

func probeNodeDirect(ref configure.NodeRef) int {
	const timeout = 5 * time.Second
	s := serverRawByRef(ref)
	if s == nil {
		return -1
	}
	return probe.ProbeServerWithTimeout(s, timeout)
}

func probeNodeFast(ref configure.NodeRef) int {
	const timeout = 1200 * time.Millisecond
	s := serverRawByRef(ref)
	if s == nil || strings.TrimSpace(s.Host) == "" || s.Port <= 0 {
		return -1
	}
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(s.Host, strconv.Itoa(s.Port)), timeout)
	if err != nil {
		return -1
	}
	_ = conn.Close()
	ms := int(time.Since(start).Milliseconds())
	if ms <= 0 {
		return 1
	}
	return ms
}

func probeNodeByDefaultMode(ref configure.NodeRef) int {
	if configure.GetSettingNotNil().GroupProbeMode == "fast" {
		return probeNodeFast(ref)
	}
	return probeNodeDirect(ref)
}

func serverRawByRef(ref configure.NodeRef) *configure.ServerRaw {
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

func fastReachableRef(ref configure.NodeRef) bool {
	const fastProbeTimeout = 1200 * time.Millisecond
	switch ref.Type {
	case "server":
		s := configure.GetServer(ref.Index)
		if s == nil {
			return false
		}
		// 需经前置代理才能连通的节点，直连预检必然失败，交给内核走链路测
		if s.FrontProxy != "" {
			return true
		}
		return probe.FastReachable(s, fastProbeTimeout)
	case "sub_server":
		sub := configure.GetSubscription(ref.Sub)
		if sub == nil || ref.Index < 0 || ref.Index >= len(sub.Servers) {
			return false
		}
		s := sub.Servers[ref.Index]
		if s.FrontProxy != "" || sub.ProxyOutbound != "" {
			return true
		}
		return probe.FastReachable(&s, fastProbeTimeout)
	default:
		return false
	}
}

func ensureProbeOutboundForNode(ref configure.NodeRef) error {
	_ = configure.AddOutbound(engine.ProbeOutboundName)
	if cur := configure.GetOutbound(engine.ProbeOutboundName); cur != nil &&
		cur.Target.TargetType == "node" && nodeRefEqual(cur.Target.NodeRef, &ref) {
		return nil
	}
	o := &configure.Outbound{
		Name: engine.ProbeOutboundName,
		Target: configure.OutboundTarget{
			TargetType: "node",
			NodeRef:    &ref,
		},
	}
	if err := configure.SetOutbound(engine.ProbeOutboundName, o); err != nil {
		return err
	}
	OnOutboundUpdated(engine.ProbeOutboundName)
	return engine.Manager.Restart()
}

func restartEngineIfRunning(outboundName string) {
	if !engine.Manager.IsRunning() {
		return
	}
	if err := engine.Manager.Restart(); err != nil {
		addLog(fmt.Sprintf("❌ [%s] 切换节点后重启代理失败: %v", outboundName, err))
	}
}

func parseProbeTargets(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, p := range parts {
		h := strings.ToLower(strings.TrimSpace(p))
		h = strings.TrimPrefix(h, "https://")
		h = strings.TrimPrefix(h, "http://")
		if i := strings.Index(h, "/"); i >= 0 {
			h = h[:i]
		}
		if h == "" {
			continue
		}
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	return out
}

func probeViaCoreSocks(host string, timeout time.Duration) int {
	start := time.Now()
	dialSocksProxy, err := xproxy.SOCKS5("tcp", net.JoinHostPort(engine.ProbeInboundListen, strconv.Itoa(engine.ProbeInboundSocksPort)), nil, xproxy.Direct)
	if err != nil {
		return -1
	}
	dialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dialSocksProxy.Dial(network, addr)
	}
	transport := &http.Transport{DialContext: dialContext, TLSHandshakeTimeout: timeout}
	client := &http.Client{Transport: transport, Timeout: timeout}

	u := "https://" + host + "/generate_204?_=" + strconv.FormatInt(time.Now().UnixNano(), 10)
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "ProxyStation-CoreProbe/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return -1
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 && resp.StatusCode != 200 {
		return -1
	}
	ms := int(time.Since(start).Milliseconds())
	if ms <= 0 {
		return 1
	}
	return ms
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
