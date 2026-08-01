// scheduler 负责订阅自动更新等定时任务
package scheduler

import (
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/ProxyStation/proxystation/db/configure"
	"github.com/ProxyStation/proxystation/pkg/observatory"
	"github.com/ProxyStation/proxystation/pkg/subscription"
	"github.com/ProxyStation/proxystation/pkg/util/log"
)

// subHTTPClient 订阅拉取专用客户端：只设连接超时，不限制读取时间
var subHTTPClient = &http.Client{
	Transport: &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 30 * time.Second,
	},
}

var global = &Scheduler{}

type Scheduler struct {
	mu     sync.Mutex
	stopCh chan struct{}
}

func Start() {
	global.mu.Lock()
	defer global.mu.Unlock()
	if global.stopCh != nil {
		return
	}
	global.stopCh = make(chan struct{})
	go global.run()
}

func Stop() {
	global.mu.Lock()
	defer global.mu.Unlock()
	if global.stopCh != nil {
		close(global.stopCh)
		global.stopCh = nil
	}
}

func (s *Scheduler) run() {
	ticker := time.NewTicker(10 * time.Minute) // 每10分钟检查一次
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.checkAndUpdate()
		}
	}
}

func (s *Scheduler) checkAndUpdate() {
	setting := configure.GetSettingNotNil()
	if setting.SubscriptionAutoUpdateMode != "on" {
		return
	}
	intervalHour := setting.SubscriptionAutoUpdateIntervalHour
	if intervalHour <= 0 {
		intervalHour = 12
	}
	interval := time.Duration(intervalHour) * time.Hour

	subs := configure.GetSubscriptions()
	for i, sub := range subs {
		if sub.URL == "" {
			continue
		}
		if time.Since(sub.UpdatedAt) < interval {
			continue
		}
		log.Info("auto updating subscription: %s", sub.Name)
		go updateSub(i, &subs[i])
	}
}

func updateSub(index int, sub *configure.SubscriptionRaw) {
	resp, err := subHTTPClient.Get(sub.URL)
	if err != nil {
		log.Warn("auto update sub %s: %v", sub.Name, err)
		return
	}
	defer resp.Body.Close()
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	servers, format, err := subscription.Parse(content, sub.Format)
	if err != nil {
		return
	}
	if sub.Format == configure.FormatAuto {
		sub.Format = format
	}
	configure.PreserveServerProbeResults(sub.Servers, servers)
	for i := range servers {
		servers[i].Source = sub.ID
	}
	servers = configure.PreserveActiveSubscriptionServers(index, sub.Servers, servers)
	sub.Servers = servers
	sub.UpdatedAt = time.Now()
	_ = configure.SetSubscription(index, sub)

	// 同步更新对应 Group
	if sub.GroupID != "" {
		groupIndex, group := configure.GetGroupByID(sub.GroupID)
		if group != nil {
			refs := make([]configure.ServerRef, len(servers))
			for i := range servers {
				refs[i] = configure.ServerRef{Type: "sub_server", Index: i, Sub: index}
			}
			group.Servers = refs
			_ = configure.SetGroup(groupIndex, group)
		}
		observatory.OnGroupUpdated(sub.GroupID)
	}
}
