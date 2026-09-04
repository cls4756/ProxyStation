// scheduler 负责订阅自动更新等定时任务
package scheduler

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
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

const subscriptionRetryInterval = 5 * time.Minute

type Scheduler struct {
	mu                      sync.Mutex
	stopCh                  chan struct{}
	lastNodeCleanup         map[string]time.Time
	lastSubscriptionAttempt map[int]time.Time
	subscriptionUpdating    map[int]bool
}

func Start() {
	global.mu.Lock()
	defer global.mu.Unlock()
	if global.stopCh != nil {
		return
	}
	global.stopCh = make(chan struct{})
	if global.lastSubscriptionAttempt == nil {
		global.lastSubscriptionAttempt = make(map[int]time.Time)
	}
	if global.subscriptionUpdating == nil {
		global.subscriptionUpdating = make(map[int]bool)
	}
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
	ticker := time.NewTicker(1 * time.Minute) // 每分钟检查一次配置到期任务
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.checkAndUpdate()
			s.checkAndCleanupNodes()
		}
	}
}

func (s *Scheduler) checkAndCleanupNodes() {
	schedulerConfig := configure.GetSchedulerConfig()
	if len(schedulerConfig.NodeCleanupRules) == 0 {
		return
	}
	if s.lastNodeCleanup == nil {
		s.lastNodeCleanup = make(map[string]time.Time)
	}
	for _, rule := range schedulerConfig.NodeCleanupRules {
		if len(rule.GroupIDs) == 0 {
			continue
		}
		ruleKey := nodeCleanupRuleKey(rule)
		interval := time.Duration(rule.IntervalHour) * time.Hour
		if interval <= 0 {
			interval = 24 * time.Hour
		}
		if last := s.lastNodeCleanup[ruleKey]; !last.IsZero() && time.Since(last) < interval {
			continue
		}
		s.lastNodeCleanup[ruleKey] = time.Now()
		rule := rule
		go cleanupInvalidNodes(rule)
	}
}

func nodeCleanupRuleKey(rule configure.NodeCleanupRule) string {
	groupIDs := append([]string(nil), rule.GroupIDs...)
	sort.Strings(groupIDs)
	return fmt.Sprintf("%s|%d|%s|%s", rule.Mode, rule.IntervalHour, rule.RecycleGroupID, strings.Join(groupIDs, ","))
}

func cleanupInvalidNodes(rule configure.NodeCleanupRule) {
	type cleanupGroup struct {
		id   string
		refs []configure.ServerRef
	}
	groups := make([]cleanupGroup, 0, len(rule.GroupIDs))
	uniqueRefs := make([]configure.NodeRef, 0)
	refIndexes := make(map[configure.NodeRef]int)
	for _, groupID := range rule.GroupIDs {
		_, group := configure.GetGroupByID(groupID)
		if group == nil || len(group.Servers) == 0 || group.ID == rule.RecycleGroupID {
			continue
		}
		groups = append(groups, cleanupGroup{id: group.ID, refs: append([]configure.ServerRef(nil), group.Servers...)})
		for _, ref := range group.Servers {
			nodeRef := configure.NodeRef{Type: ref.Type, Index: ref.Index, Sub: ref.Sub}
			if _, exists := refIndexes[nodeRef]; exists {
				continue
			}
			refIndexes[nodeRef] = len(uniqueRefs)
			uniqueRefs = append(uniqueRefs, nodeRef)
		}
	}
	invalidResults := observatory.ProbeInvalidNodes(uniqueRefs, rule.Mode)
	for _, selectedGroup := range groups {
		invalid := make([]configure.ServerRef, 0)
		for _, ref := range selectedGroup.refs {
			nodeRef := configure.NodeRef{Type: ref.Type, Index: ref.Index, Sub: ref.Sub}
			if invalidResults[refIndexes[nodeRef]] {
				invalid = append(invalid, ref)
			}
		}
		if len(invalid) == 0 {
			continue
		}
		sourceIndex, sourceGroup := configure.GetGroupByID(selectedGroup.id)
		if sourceIndex < 0 || sourceGroup == nil {
			continue
		}
		recycleIndex := -1
		if rule.RecycleGroupID != "" {
			recycleIndex, _ = configure.GetGroupByID(rule.RecycleGroupID)
		}
		if recycleIndex >= 0 {
			if err := configure.AddServersToGroup(recycleIndex, invalid); err != nil {
				log.Warn("node cleanup recycle group %s failed: %v", rule.RecycleGroupID, err)
				continue
			}
		}
		if err := configure.RemoveServersFromGroup(sourceIndex, invalid); err != nil {
			log.Warn("node cleanup source group %s failed: %v", selectedGroup.id, err)
			continue
		}
		log.Info("node cleanup removed %d invalid nodes from group %s%s", len(invalid), sourceGroup.Name, cleanupRecycleSuffix(recycleIndex >= 0, rule.RecycleGroupID))
	}
}

func cleanupRecycleSuffix(recycled bool, groupID string) string {
	if !recycled {
		return ""
	}
	return fmt.Sprintf(" and recycled them to %s", groupID)
}

func (s *Scheduler) checkAndUpdate() {
	schedulerConfig := configure.GetSchedulerConfig()
	if schedulerConfig.SubscriptionAutoUpdateMode != "on" {
		return
	}
	intervalHour := schedulerConfig.SubscriptionAutoUpdateIntervalHour
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
		if !s.beginSubscriptionUpdate(i, interval) {
			continue
		}
		log.Info("auto updating subscription: %s", sub.Name)
		go func(index int, subscription *configure.SubscriptionRaw) {
			defer s.endSubscriptionUpdate(index)
			updateSub(index, subscription)
		}(i, &subs[i])
	}
}

func (s *Scheduler) beginSubscriptionUpdate(index int, interval time.Duration) bool {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subscriptionUpdating[index] {
		return false
	}
	if lastAttempt := s.lastSubscriptionAttempt[index]; !lastAttempt.IsZero() && now.Sub(lastAttempt) < subscriptionRetryInterval {
		return false
	}
	s.lastSubscriptionAttempt[index] = now
	s.subscriptionUpdating[index] = true
	return true
}

func (s *Scheduler) endSubscriptionUpdate(index int) {
	s.mu.Lock()
	delete(s.subscriptionUpdating, index)
	s.mu.Unlock()
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
