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
	"sync/atomic"
	"time"

	"github.com/ProxyStation/proxystation/core/engine"
	"github.com/ProxyStation/proxystation/db/configure"
	"github.com/ProxyStation/proxystation/pkg/probe"
	xproxy "golang.org/x/net/proxy"
)

const (
	defaultProbeIntervalSec = 300
	defaultSwitchThreshold  = 100
	// minProbeInterval 测速任务的最小间隔，与界面上的提示保持一致
	minProbeInterval = 10 * time.Second
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

var nodeMetadataEnricher func([]configure.NodeRef)

// SetNodeMetadataEnricher registers the server-side metadata completion hook.
func SetNodeMetadataEnricher(enricher func([]configure.NodeRef)) {
	nodeMetadataEnricher = enricher
}

// 独立探测实例的端口在 [基址+1, 基址+probePortSpan] 内轮转，避免并发碰撞。
const probePortSpan = 64

var probePortCounter uint32
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
			ensureOutboundSwitchSettings(name, o)
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
		ensureOutboundSwitchSettings(name, o)
		global.startTask(name, o)
	}
}

// MarkManualSwitch records a user-selected active node so automatic switching
// respects the outbound cooldown instead of immediately overriding the choice.
func MarkManualSwitch(name string) {
	markSwitched(name)
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

// resolveTaskInterval 优先用出站自己配置的探测间隔，非法或缺失时回退到设置里的默认值。
// 过小的间隔会让测速任务一直占着内核，所以钳到 minProbeInterval。
func resolveTaskInterval(raw string) time.Duration {
	interval := resolveProbeInterval()
	if raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			interval = d
		}
	}
	if interval < minProbeInterval {
		interval = minProbeInterval
	}
	return interval
}

func (obs *Observatory) startTask(name string, o *configure.Outbound) {
	interval := resolveTaskInterval(o.ProbeInterval)

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

	// 已有活跃节点时交给统一切换判定处理，避免重启任务时绕过阈值直接切换。
	if o.Target.ActiveNodeRef != nil && groupContainsRef(group.Servers, *o.Target.ActiveNodeRef) {
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

// resolveProbeConcurrencyForMode 返回测速的并发实例数。fast 模式是纯 TCP 拨测、
// 开销极低，用更高并发；real 模式每个节点起一个独立内核，用设置里的值（默认 4）。
// mode 为空时回退到全局设置 GroupProbeMode。
func resolveProbeConcurrencyForMode(mode string) int {
	setting := configure.GetSettingNotNil()
	if mode == "" {
		mode = setting.GroupProbeMode
	}
	if mode == "fast" {
		return 16
	}
	n := setting.GroupProbeConcurrency
	if n < 1 || n > 8 {
		n = 4
	}
	return n
}

// probeNodesConcurrent 并发测速一组节点，保持返回结果与输入顺序一致。
// 每个 worker 独立处理一个节点：fast 模式走 TCP 拨测，real 模式起独立探测内核。
func probeNodesConcurrent(refs []configure.NodeRef) []probeResult {
	return probeNodesWithProgress(refs, "", nil, nil)
}

// ProbeProgressStart 在某个节点开始测速时回调，i 为其在 refs 中的索引。
type ProbeProgressStart func(i int, ref configure.NodeRef)

// ProbeProgressDone 在某个节点测速完成时回调，latency 为结果（-1 表示失败）。
type ProbeProgressDone func(i int, ref configure.NodeRef, latency int)

// ProbeNodesWithProgress 与 probeNodesConcurrent 相同的并发调度，额外在每个节点
// 开始/完成时触发回调，供 HTTP 层推送 SSE 进度。mode 显式指定 fast/real（为空回退
// 全局设置），并发上限由 resolveProbeConcurrencyForMode(mode) 控制，与定时任务、
// 出站探测共用同一节流设置。onStart/onDone 可能被多个 worker 并发调用，调用方需
// 自行保证回调内的并发安全。
func ProbeNodesWithProgress(refs []configure.NodeRef, mode string, onStart ProbeProgressStart, onDone ProbeProgressDone) []probeResult {
	return probeNodesWithProgress(refs, mode, onStart, onDone)
}

func probeNodesWithProgress(refs []configure.NodeRef, mode string, onStart ProbeProgressStart, onDone ProbeProgressDone) []probeResult {
	results := make([]probeResult, len(refs))
	if len(refs) == 0 {
		return results
	}
	workers := resolveProbeConcurrencyForMode(mode)
	if workers > len(refs) {
		workers = len(refs)
	}
	var idx int64 = -1
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(atomic.AddInt64(&idx, 1))
				if i >= len(refs) {
					return
				}
				if onStart != nil {
					onStart(i, refs[i])
				}
				latency := probeNodeByMode(refs[i], mode)
				results[i] = probeResult{ref: refs[i], latency: latency}
				saveLatency(refs[i], latency)
				if onDone != nil {
					onDone(i, refs[i], latency)
				}
			}
		}()
	}
	wg.Wait()
	return results
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

	refs := make([]configure.NodeRef, 0, len(group.Servers))
	for _, ref := range group.Servers {
		refs = append(refs, configure.NodeRef{Type: ref.Type, Index: ref.Index, Sub: ref.Sub})
	}
	// 出站绑定分组时可单独指定测速方式，为空才回退到设置里的默认测速方式
	results := probeNodesWithProgress(refs, o.ProbeMode, nil, nil)

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
	threshold, cooldown := resolveSwitchSettings(o)
	ok, reason := shouldSwitchWithSettings(outboundName, o.Target.Mode, *current, available, best, threshold, cooldown)
	if ok {
		_ = configure.SetOutboundActiveNode(outboundName, &best.ref)
		restartEngineIfRunning(outboundName)
		markSwitched(outboundName)
		addLog(fmt.Sprintf("🔁 [%s] 切换节点：%s:%d:%d -> %s:%d:%d", outboundName, current.Type, current.Sub, current.Index, best.ref.Type, best.ref.Sub, best.ref.Index))
	} else {
		addLog(fmt.Sprintf("⏸ [%s] 不切换：%s", outboundName, reason))
	}
}

// GroupSwitchOutcome 一次手动测速后，某个绑定该分组的出站的切换判定结果。
type GroupSwitchOutcome struct {
	Outbound  string `json:"outbound"`
	Switched  bool   `json:"switched"`
	Reason    string `json:"reason"`
	ProbeMode string `json:"probeMode"`
}

// ApplyGroupProbeResults 在界面上手动测完一个分组后调用，按绑定该分组的出站各自的
// 策略（手动/最低延迟/稳定优先）与阈值、冷却决定是否切换活跃节点。
//
// mode 是本次界面测速用的方式。出站的周期测速方式与之不同时直接跳过：两种方式的
// 延迟口径不同（fast 只做 TCP 握手，real 走完整代理链路），拿 fast 的数字去和按
// real 阈值调好的配置比较会导致误切换。
func ApplyGroupProbeResults(groupID string, mode string) []GroupSwitchOutcome {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return nil
	}
	_, group := configure.GetGroupByID(groupID)
	if group == nil {
		return nil
	}

	mode = normalizeProbeMode(mode)
	outcomes := make([]GroupSwitchOutcome, 0, 2)
	switched := make([]string, 0, 2)
	for _, name := range configure.GetOutboundNames() {
		o := configure.GetOutbound(name)
		if o == nil || o.Target.TargetType != "group" || o.Target.GroupID != groupID {
			continue
		}
		outboundMode := normalizeProbeMode(o.ProbeMode)
		if outboundMode != mode {
			reason := fmt.Sprintf("本次为%s，出站绑定的测速方式是%s，不参与切换判定",
				probeModeLabel(mode), probeModeLabel(outboundMode))
			addLog(fmt.Sprintf("⏸ [%s] 不切换：%s", name, reason))
			outcomes = append(outcomes, GroupSwitchOutcome{Outbound: name, Reason: reason, ProbeMode: outboundMode})
			continue
		}

		ok, reason := applyOutboundSwitch(name, o, group)
		if ok {
			switched = append(switched, name)
		}
		outcomes = append(outcomes, GroupSwitchOutcome{
			Outbound:  name,
			Switched:  ok,
			Reason:    reason,
			ProbeMode: outboundMode,
		})
	}
	// 多个出站可能绑定同一分组，重启内核只做一次
	if len(switched) > 0 {
		restartEngineIfRunning(strings.Join(switched, ", "))
	}
	return outcomes
}

// applyOutboundSwitch 复用周期测速的切换判定，但延迟取自存储里刚写回的测速结果，
// 因此不会重新发起探测。
func applyOutboundSwitch(name string, o *configure.Outbound, group *configure.Group) (bool, string) {
	available := make([]probeResult, 0, len(group.Servers))
	for _, ref := range group.Servers {
		nodeRef := configure.NodeRef{Type: ref.Type, Index: ref.Index, Sub: ref.Sub}
		if latency := readLatency(nodeRef); latency > 0 {
			available = append(available, probeResult{ref: nodeRef, latency: latency})
		}
	}
	if len(available) == 0 {
		reason := "分组内无可用节点，保持当前节点"
		addLog(fmt.Sprintf("🔎 [%s] %s", name, reason))
		return false, reason
	}
	sort.Slice(available, func(i, j int) bool {
		return available[i].latency < available[j].latency
	})
	best := available[0]

	current := o.Target.ActiveNodeRef
	if current == nil {
		_ = configure.SetOutboundActiveNode(name, &best.ref)
		markSwitched(name)
		reason := fmt.Sprintf("初次选择为 %dms 的节点", best.latency)
		addLog(fmt.Sprintf("🔁 [%s] 切换节点：%s", name, reason))
		return true, reason
	}
	if nodeRefEqual(current, &best.ref) {
		reason := fmt.Sprintf("当前已是最优节点（%dms）", best.latency)
		addLog(fmt.Sprintf("✅ [%s] %s，不切换", name, reason))
		return false, reason
	}

	threshold, cooldown := resolveSwitchSettings(o)
	ok, reason := shouldSwitchWithSettings(name, o.Target.Mode, *current, available, best, threshold, cooldown)
	if !ok {
		addLog(fmt.Sprintf("⏸ [%s] 不切换：%s", name, reason))
		return false, reason
	}
	_ = configure.SetOutboundActiveNode(name, &best.ref)
	markSwitched(name)
	addLog(fmt.Sprintf("🔁 [%s] 切换节点：%s:%d:%d -> %s:%d:%d（%dms）",
		name, current.Type, current.Sub, current.Index, best.ref.Type, best.ref.Sub, best.ref.Index, best.latency))
	return true, reason
}

// normalizeProbeMode 把空值折叠成设置里的默认测速方式，让「未配置」和「配置成默认值」
// 在比较时等价。
func normalizeProbeMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "fast" && mode != "real" {
		mode = configure.GetSettingNotNil().GroupProbeMode
	}
	if mode != "fast" {
		return "real"
	}
	return "fast"
}

func probeModeLabel(mode string) string {
	if mode == "fast" {
		return "快速测速"
	}
	return "真连接测速"
}

func probeGroupLatency(groupID string) {
	_, group := configure.GetGroupByID(groupID)
	if group == nil {
		return
	}
	refs := make([]configure.NodeRef, 0, len(group.Servers))
	for _, ref := range group.Servers {
		refs = append(refs, configure.NodeRef{Type: ref.Type, Index: ref.Index, Sub: ref.Sub})
	}
	results := probeNodesConcurrent(refs)
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
	copiedRefs := make([]configure.NodeRef, 0, len(picked))
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
		copiedRefs = append(copiedRefs, configure.NodeRef{Type: localRef.Type, Index: localRef.Index, Sub: localRef.Sub})
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
	if nodeMetadataEnricher != nil {
		nodeMetadataEnricher(copiedRefs)
	}
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

// resolveProbeInterval 是出站未设置 ProbeInterval 时的兜底间隔。绑定分组时界面会把
// 设置里的值预填成出站自己的间隔，所以正常情况下走的是出站级配置，这里只兜历史数据。
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

func resolveSwitchSettings(o *configure.Outbound) (int, time.Duration) {
	if o == nil || o.SwitchThresholdMs == nil || o.SwitchCooldownSec == nil {
		return resolveSwitchThreshold(), resolveSwitchCooldown()
	}
	threshold := *o.SwitchThresholdMs
	if threshold < 0 {
		threshold = 0
	}
	cooldown := time.Duration(*o.SwitchCooldownSec) * time.Second
	if cooldown < 0 {
		cooldown = 0
	}
	return threshold, cooldown
}

// ensureOutboundSwitchSettings 给缺失的切换参数补上默认值并落库。
// name 必须由调用方传入：存量记录的 Name 字段可能为空，而 SetOutbound 会把空名字
// 归一成 "proxy"，用 o.Name 落库会把本出站的配置整个写进默认出站。
func ensureOutboundSwitchSettings(name string, o *configure.Outbound) {
	if o == nil || o.Target.TargetType != "group" {
		return
	}
	if o.SwitchThresholdMs != nil && o.SwitchCooldownSec != nil {
		return
	}
	threshold, cooldown := resolveSwitchThreshold(), int(resolveSwitchCooldown()/time.Second)
	if o.SwitchThresholdMs == nil {
		o.SwitchThresholdMs = &threshold
	}
	if o.SwitchCooldownSec == nil {
		o.SwitchCooldownSec = &cooldown
	}
	if o.Name == "" {
		o.Name = name
	}
	_ = configure.SetOutbound(name, o)
}

func shouldSwitch(outboundName string, mode configure.SelectionMode, current configure.NodeRef, available []probeResult, best probeResult) (bool, string) {
	threshold := resolveSwitchThreshold()
	cooldown := resolveSwitchCooldown()
	return shouldSwitchWithSettings(outboundName, mode, current, available, best, threshold, cooldown)
}

func shouldSwitchWithSettings(outboundName string, mode configure.SelectionMode, current configure.NodeRef, available []probeResult, best probeResult, threshold int, cooldown time.Duration) (bool, string) {
	currentLatency := -1
	for _, item := range available {
		if nodeRefEqual(&current, &item.ref) {
			currentLatency = item.latency
			break
		}
	}
	if currentLatency <= 0 {
		if mode == configure.ModeManual {
			return false, "手动指定：当前节点不可用，但不会自动切换"
		}
		return true, "当前节点无有效延迟，允许切换"
	}
	if mode == configure.ModeManual {
		return false, fmt.Sprintf("手动指定：当前节点仍连通（%dms），不自动切换", currentLatency)
	}
	// 稳定优先：当前节点连通就不切换，延迟高也不动，只有掉线才会走到上面的分支
	if mode == configure.ModeStable {
		return false, fmt.Sprintf("稳定优先：当前节点仍连通（%dms），不主动切换", currentLatency)
	}
	if (currentLatency - best.latency) < threshold {
		return false, fmt.Sprintf("延迟差值不足（当前 %dms，最优 %dms，阈值 %dms）", currentLatency, best.latency, threshold)
	}
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

func groupContainsRef(refs []configure.ServerRef, target configure.NodeRef) bool {
	for _, ref := range refs {
		if ref.Type == target.Type && ref.Index == target.Index && ref.Sub == target.Sub {
			return true
		}
	}
	return false
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

func allocProbePort() int {
	n := atomic.AddUint32(&probePortCounter, 1)
	return engine.ProbeInboundSocksPort + int(n%probePortSpan) + 1
}

func probeNodeViaCore(ref configure.NodeRef) int {
	if !fastReachableRef(ref) {
		return -1
	}
	s := serverRawByRef(ref)
	if s == nil {
		return -1
	}
	socksPort := allocProbePort()
	inst, err := engine.StartProbeInstance(s, socksPort)
	if err != nil {
		return -1
	}
	defer inst.Close()
	return probeViaCoreTargets(socksPort)
}

func probeViaCoreTargets(socksPort int) int {
	targets := parseProbeTargets(configure.GetSettingNotNil().ProbeTargets)
	if len(targets) == 0 {
		targets = []string{"www.gstatic.com", "www.google.com", "www.youtube.com", "cp.cloudflare.com", "example.com"}
	}
	for _, host := range targets {
		if ms := probeViaCoreSocks(host, socksPort, 4500*time.Millisecond); ms > 0 {
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

// probeNodeByMode 按显式 mode 探测；mode 为空时回退全局设置 GroupProbeMode。
func probeNodeByMode(ref configure.NodeRef, mode string) int {
	if mode == "" {
		mode = configure.GetSettingNotNil().GroupProbeMode
	}
	if mode == "fast" {
		return probeNodeFast(ref)
	}
	// real 模式：经内核走完整代理链路做真连测试，仅 TCP 可达但无法真正代理的节点会被判定失败
	return probeNodeViaCore(ref)
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

func probeViaCoreSocks(host string, socksPort int, timeout time.Duration) int {
	start := time.Now()
	dialSocksProxy, err := xproxy.SOCKS5("tcp", net.JoinHostPort(engine.ProbeInboundListen, strconv.Itoa(socksPort)), nil, xproxy.Direct)
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
	apply := func(server *configure.ServerRaw) {
		server.Latency = latency
		server.LastProbeTime = now
	}
	var err error
	switch ref.Type {
	case "server":
		err = configure.UpdateServer(ref.Index, apply)
	case "sub_server":
		err = configure.UpdateSubscriptionServer(ref.Sub, ref.Index, apply)
	}
	if err != nil {
		addLog(fmt.Sprintf("⚠️ 保存节点测速结果失败（%s:%d:%d）：%v", ref.Type, ref.Sub, ref.Index, err))
	}
}
