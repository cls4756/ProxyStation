package server

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/ProxyStation/proxystation/db/configure"
	"github.com/ProxyStation/proxystation/pkg/ipscore"
)

// IP 评分是串行、低频的外部请求：几十个节点会跑上一分钟，HTTP 请求不能一直挂着等。
// 因此提交任务后立刻返回，前端轮询进度，任务本身在后台按限速逐个查。

type ipScoreTaskState struct {
	Running   bool   `json:"running"`
	Total     int    `json:"total"`
	Done      int    `json:"done"`
	Failed    int    `json:"failed"`
	Provider  string `json:"provider"`
	CurrentIP string `json:"currentIp,omitempty"`
	StartedAt int64  `json:"startedAt,omitempty"`
	EndedAt   int64  `json:"endedAt,omitempty"`
	// Canceled 标记本轮是否被用户中止，前端据此不提示“完成”
	Canceled bool `json:"canceled,omitempty"`
	// LastError 最近一次失败原因，便于界面提示是限流还是网络不通
	LastError string              `json:"lastError,omitempty"`
	Refs      []configure.NodeRef `json:"refs,omitempty"`
}

var ipScoreTask = struct {
	mu      sync.Mutex
	state   ipScoreTaskState
	cancel  chan struct{}
	client  *ipscore.Client
	pending []configure.NodeRef
}{}

var ipScoreStartMu sync.Mutex

func ipScoreState() ipScoreTaskState {
	ipScoreTask.mu.Lock()
	defer ipScoreTask.mu.Unlock()
	state := ipScoreTask.state
	if state.Running {
		state.Refs = nil
	}
	return state
}

func cancelIPScoreTask() bool {
	ipScoreTask.mu.Lock()
	defer ipScoreTask.mu.Unlock()
	if !ipScoreTask.state.Running || ipScoreTask.cancel == nil {
		return false
	}
	close(ipScoreTask.cancel)
	ipScoreTask.cancel = nil
	return true
}

// startIPScoreTask 启动后台评分任务。同一时间只允许一个任务，避免多个请求叠加
// 把外部服务的频率吃穿。
func startIPScoreTask(refs []configure.NodeRef) error {
	ipScoreStartMu.Lock()
	defer ipScoreStartMu.Unlock()
	return startIPScoreTaskSerialized(refs)
}

func startIPScoreTaskSerialized(refs []configure.NodeRef) error {
	ipScoreConfig := configure.GetIPScoreConfig()
	if !ipScoreConfig.Enabled {
		return fmt.Errorf("IP 评分未启用，请先在设置中开启并选择服务商")
	}
	provider, endpoint, err := ipscore.ValidateSettings(ipScoreConfig.Provider, ipScoreConfig.Endpoint)
	if err != nil {
		return err
	}

	ipScoreTask.mu.Lock()
	if ipScoreTask.state.Running {
		ipScoreTask.mu.Unlock()
		return fmt.Errorf("已有评分任务在进行中，请等待完成或先中止")
	}
	cancel := make(chan struct{})
	ipScoreTask.cancel = cancel
	// Client 常驻，让多轮任务之间也共享限速节奏，避免“停一下再点”绕过间隔
	if ipScoreTask.client == nil {
		ipScoreTask.client = ipscore.NewClient(ipTypeHTTPClient())
		// 走代理的请求遇到链路抖动时，换直连再试一次，明显减少“部分节点失败”
		ipScoreTask.client.FallbackHTTPClient = directHTTPClient(20 * time.Second)
	}
	client := ipScoreTask.client
	ipScoreTask.state = ipScoreTaskState{
		Running:   true,
		Total:     len(refs),
		Provider:  string(provider),
		StartedAt: time.Now().Unix(),
		Refs:      append([]configure.NodeRef(nil), refs...),
	}
	ipScoreTask.mu.Unlock()

	opt := ipscore.Options{
		Provider:    provider,
		Endpoint:    endpoint,
		MinInterval: time.Duration(ipScoreConfig.MinIntervalMs) * time.Millisecond,
		Timeout:     20 * time.Second,
	}

	go runIPScoreTask(client, refs, opt, cancel)
	return nil
}

// queueIPScoreTask 把自动补全请求加入评分队列，避免与手动评分任务并发请求外部服务。
func queueIPScoreTask(refs []configure.NodeRef) error {
	refs = uniqueNodeRefs(refs)
	if len(refs) == 0 {
		return nil
	}
	if !configure.GetIPScoreConfig().Enabled {
		return nil
	}

	ipScoreStartMu.Lock()
	defer ipScoreStartMu.Unlock()
	ipScoreTask.mu.Lock()
	if ipScoreTask.state.Running {
		ipScoreTask.pending = appendUniqueNodeRefs(ipScoreTask.pending, refs...)
		ipScoreTask.mu.Unlock()
		return nil
	}
	ipScoreTask.mu.Unlock()
	return startIPScoreTaskSerialized(refs)
}

func runIPScoreTask(client *ipscore.Client, refs []configure.NodeRef, opt ipscore.Options, cancel chan struct{}) {
	addLog(fmt.Sprintf("🧮 开始查询 %d 个节点的 IP 评分（%s，每 %v 一个）",
		len(refs), opt.Provider, opt.MinInterval))

	// 同一 IP 只查一次：同一个落地 IP 的多个节点（即便域名不同）没必要重复消耗外部额度
	cache := map[string]ipscore.Result{}
	pendingScores := make([]ipScoreUpdate, 0, len(refs))
	failed := 0
	canceled := false

	for _, ref := range refs {
		select {
		case <-cancel:
			canceled = true
		default:
		}
		if canceled {
			break
		}

		s := serverRawByNodeRef(ref)
		if s == nil {
			continue
		}
		// 评分服务普遍只接受 IP：传节点域名会被判非法（如 Net.Coffee 直接 400）。
		// 因此先解析出真实落地 IP，再拿它做查询和去重键。
		target, err := scoreTargetIP(s)
		if err != nil {
			failed++
			bumpIPScoreProgress("", failed, err.Error())
			continue
		}

		res, cached := cache[target]
		if !cached {
			setIPScoreCurrent(target)
			res = client.Lookup(target, opt)
			cache[target] = res
		}
		errMsg := ""
		if res.Err != nil {
			failed++
			errMsg = res.Err.Error()
		} else {
			pendingScores = append(pendingScores, ipScoreUpdate{ref: ref, result: res})
		}
		bumpIPScoreProgress(target, failed, errMsg)
	}
	if err := saveIPScores(pendingScores); err != nil {
		addLog(fmt.Sprintf("⚠️ 批量保存 IP 评分失败：%v", err))
	}

	ipScoreTask.mu.Lock()
	ipScoreTask.state.Running = false
	ipScoreTask.state.Canceled = canceled
	ipScoreTask.state.CurrentIP = ""
	ipScoreTask.state.EndedAt = time.Now().Unix()
	done := ipScoreTask.state.Done
	pending := append([]configure.NodeRef(nil), ipScoreTask.pending...)
	ipScoreTask.pending = nil
	ipScoreTask.cancel = nil
	ipScoreTask.mu.Unlock()

	if canceled {
		addLog(fmt.Sprintf("⏹ IP 评分已中止：已处理 %d 个，失败 %d 个", done, failed))
		return
	}
	addLog(fmt.Sprintf("✅ IP 评分完成：成功 %d 个，失败 %d 个", done-failed, failed))
	if len(pending) > 0 {
		go func() {
			if err := startIPScoreTask(pending); err != nil {
				addLog(fmt.Sprintf("⚠️ 启动排队的 IP 评分失败：%v", err))
			}
		}()
	}
}

type ipScoreUpdate struct {
	ref    configure.NodeRef
	result ipscore.Result
}

func uniqueNodeRefs(refs []configure.NodeRef) []configure.NodeRef {
	result := make([]configure.NodeRef, 0, len(refs))
	seen := make(map[configure.NodeRef]struct{}, len(refs))
	for _, ref := range refs {
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		result = append(result, ref)
	}
	return result
}

func appendUniqueNodeRefs(dst []configure.NodeRef, refs ...configure.NodeRef) []configure.NodeRef {
	seen := make(map[configure.NodeRef]struct{}, len(dst)+len(refs))
	for _, ref := range dst {
		seen[ref] = struct{}{}
	}
	for _, ref := range refs {
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		dst = append(dst, ref)
	}
	return dst
}

func setIPScoreCurrent(host string) {
	ipScoreTask.mu.Lock()
	ipScoreTask.state.CurrentIP = host
	ipScoreTask.mu.Unlock()
}

func bumpIPScoreProgress(host string, failed int, errMsg string) {
	ipScoreTask.mu.Lock()
	ipScoreTask.state.Done++
	ipScoreTask.state.Failed = failed
	ipScoreTask.state.CurrentIP = host
	if errMsg != "" {
		ipScoreTask.state.LastError = errMsg
	}
	ipScoreTask.mu.Unlock()
}

// scoreTargetIP 返回节点用于评分的目标 IP。评分服务普遍只接受 IP，传节点域名会
// 被判非法；而且同一个落地 IP 可能对应多个域名，按 IP 查询还能顺带去重。
// 优先复用 IP 类型检测已解析出的落地 IP，没有时再自己解析域名。
func scoreTargetIP(s *configure.ServerRaw) (string, error) {
	if ip := publicScoreIP(strings.TrimSpace(s.IPTypeAddr)); ip != "" {
		return ip, nil
	}
	host := strings.Trim(strings.TrimSpace(s.Host), "[]")
	if host == "" {
		return "", fmt.Errorf("节点地址为空")
	}
	if ip := publicScoreIP(host); ip != "" {
		return ip, nil
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		return "", fmt.Errorf("域名解析失败：%w", err)
	}
	var fallback string
	for _, addr := range addrs {
		ip := addr.String()
		if isPrivateScoreIP(ip) {
			continue
		}
		if addr.To4() != nil {
			return ip, nil
		}
		if fallback == "" {
			fallback = ip
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("未解析到可用的公网 IP")
}

// publicScoreIP 归一化并返回可用于评分的公网 IP，非 IP 或本地/保留地址返回空串。
func publicScoreIP(raw string) string {
	raw = strings.TrimSpace(raw)
	ip := net.ParseIP(raw)
	if ip == nil || isPrivateScoreIP(raw) {
		return ""
	}
	return ip.String()
}

// isPrivateScoreIP 判断是否为不该发给第三方评分的本地/保留地址。
func isPrivateScoreIP(raw string) bool {
	ip := net.ParseIP(raw)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

// saveIPScore 把评分写回节点。失败结果不落库，保留上一次的有效评分。
func saveIPScore(ref configure.NodeRef, res ipscore.Result) error {
	return saveIPScores([]ipScoreUpdate{{ref: ref, result: res}})
}

func saveIPScores(updates []ipScoreUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	apply := func(s *configure.ServerRaw, res ipscore.Result) {
		score := res.Score
		s.IPScore = &score
		s.IPScoreGrade = res.Grade
		s.IPScoreRisk = res.RiskLevel
		s.IPScoreProvider = string(res.Provider)
		s.IPScoreAt = time.Now().Unix()
		if strings.TrimSpace(res.Country) != "" || strings.TrimSpace(res.CountryCode) != "" ||
			strings.TrimSpace(res.Region) != "" || strings.TrimSpace(res.City) != "" {
			s.IPGeoCountry = res.Country
			s.IPGeoCountryCode = res.CountryCode
			s.IPGeoRegion = res.Region
			s.IPGeoCity = res.City
		}
	}
	serverUpdates := make(map[int]func(*configure.ServerRaw))
	subscriptionUpdates := make(map[int]map[int]func(*configure.ServerRaw))
	for _, item := range updates {
		item := item
		update := func(server *configure.ServerRaw) {
			apply(server, item.result)
		}
		switch item.ref.Type {
		case "server":
			serverUpdates[item.ref.Index] = update
		case "sub_server":
			if subscriptionUpdates[item.ref.Sub] == nil {
				subscriptionUpdates[item.ref.Sub] = make(map[int]func(*configure.ServerRaw))
			}
			subscriptionUpdates[item.ref.Sub][item.ref.Index] = update
		default:
			return fmt.Errorf("不支持的节点类型 %q", item.ref.Type)
		}
	}
	if err := configure.UpdateServers(serverUpdates); err != nil {
		return err
	}
	for subIndex, subUpdates := range subscriptionUpdates {
		if err := configure.UpdateSubscriptionServers(subIndex, subUpdates); err != nil {
			return err
		}
	}
	return nil
}
