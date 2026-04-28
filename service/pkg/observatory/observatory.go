// observatory 负责对绑定了 Group 的出站进行定期测速，并自动切换到最优节点
package observatory

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/ProxyStation/proxystation/db/configure"
)

const (
	defaultProbeURL      = "https://www.gstatic.com/generate_204"
	defaultProbeInterval = 30 * time.Second
	probeTimeout         = 5 * time.Second
)

// Observatory 管理所有出站的测速任务
type Observatory struct {
	mu      sync.Mutex
	tasks   map[string]*probeTask // outbound name -> task
	stopCh  chan struct{}
}

type probeTask struct {
	outboundName string
	ticker       *time.Ticker
	stopCh       chan struct{}
}

var global = &Observatory{
	tasks:  make(map[string]*probeTask),
	stopCh: make(chan struct{}),
}

// Start 启动 observatory，对所有绑定了 Group 的出站开始测速
func Start() {
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

func (obs *Observatory) startTask(name string, o *configure.Outbound) {
	interval := defaultProbeInterval
	if o.ProbeInterval != "" {
		if d, err := time.ParseDuration(o.ProbeInterval); err == nil {
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

	type result struct {
		ref     configure.NodeRef
		latency int
	}

	var available []result

	// 收集所有有测速数据的节点
	for _, ref := range group.Servers {
		nodeRef := configure.NodeRef{
			Type:  ref.Type,
			Index: ref.Index,
			Sub:   ref.Sub,
		}

		var latency int
		switch ref.Type {
		case "server":
			s := configure.GetServer(ref.Index)
			if s == nil {
				continue
			}
			latency = s.Latency
		case "sub_server":
			sub := configure.GetSubscription(ref.Sub)
			if sub == nil || ref.Index >= len(sub.Servers) {
				continue
			}
			latency = sub.Servers[ref.Index].Latency
		default:
			continue
		}

		// 只考虑有有效测速数据的节点（latency > 0）
		if latency > 0 {
			available = append(available, result{ref: nodeRef, latency: latency})
		}
	}

	// 如果有可用的节点，选择最快的
	if len(available) > 0 {
		sort.Slice(available, func(i, j int) bool {
			return available[i].latency < available[j].latency
		})
		best := available[0]
		_ = configure.SetOutboundActiveNode(outboundName, &best.ref)
	}
}

// probeAndUpdate 对出站绑定的 Group 内所有节点测速，选出最优节点并更新
func probeAndUpdate(outboundName string) {
	o := configure.GetOutbound(outboundName)
	if o == nil || o.Target.TargetType != "group" {
		return
	}

	_, group := configure.GetGroupByID(o.Target.GroupID)
	if group == nil {
		return
	}

	type result struct {
		ref     configure.NodeRef
		latency int // ms, -1 = timeout
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
			nodeRef := configure.NodeRef{
				Type:  ref.Type,
				Index: ref.Index,
				Sub:   ref.Sub,
			}
			latency := probeNode(ref)
			mu.Lock()
			results = append(results, result{ref: nodeRef, latency: latency})
			mu.Unlock()
			
			// 保存测速结果到数据库
			saveLatency(nodeRef, latency)
		}()
	}
	wg.Wait()

	// 过滤掉超时节点，按延迟排序
	var available []result
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

	best := available[0]
	_ = configure.SetOutboundActiveNode(outboundName, &best.ref)
}

// saveLatency 将测速结果写回存储
func saveLatency(ref configure.NodeRef, latency int) {
	switch ref.Type {
	case "server":
		s := configure.GetServer(ref.Index)
		if s == nil {
			return
		}
		s.Latency = latency
		_ = configure.SetServer(ref.Index, s)
	case "sub_server":
		sub := configure.GetSubscription(ref.Sub)
		if sub == nil || ref.Index >= len(sub.Servers) {
			return
		}
		sub.Servers[ref.Index].Latency = latency
		_ = configure.SetSubscription(ref.Sub, sub)
	}
}

// probeNode TCP 连接测速，返回延迟 ms，-1 表示超时/失败
func probeNode(ref configure.ServerRef) int {
	var host string
	var port int

	switch ref.Type {
	case "server":
		s := configure.GetServer(ref.Index)
		if s == nil {
			return -1
		}
		host = s.Host
		port = s.Port
	case "sub_server":
		sub := configure.GetSubscription(ref.Sub)
		if sub == nil || ref.Index >= len(sub.Servers) {
			return -1
		}
		s := sub.Servers[ref.Index]
		host = s.Host
		port = s.Port
	default:
		return -1
	}

	if host == "" || port == 0 {
		return -1
	}

	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), probeTimeout)
	if err != nil {
		// 如果是 connection refused，说明端口可达，也算成功
		if isRefused(err) {
			return int(time.Since(start).Milliseconds())
		}
		return -1
	}
	_ = conn.Close()
	return int(time.Since(start).Milliseconds())
}

func isRefused(err error) bool {
	if err == nil {
		return false
	}
	return fmt.Sprintf("%v", err) != "" &&
		(contains(err.Error(), "refused") || contains(err.Error(), "connection reset"))
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
