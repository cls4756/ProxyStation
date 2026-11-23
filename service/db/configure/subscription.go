package configure

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	gonanoid "github.com/matoous/go-nanoid"
)

// SubscriptionFormat 订阅格式类型
type SubscriptionFormat string

const (
	FormatV2ray   SubscriptionFormat = "v2ray"   // base64 编码的节点链接列表
	FormatClash   SubscriptionFormat = "clash"   // Clash YAML 格式
	FormatSingbox SubscriptionFormat = "singbox" // sing-box JSON 格式
	FormatAuto    SubscriptionFormat = "auto"    // 自动检测
)

// SubscriptionRaw 代表一个订阅源
type SubscriptionRaw struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	URL       string             `json:"url"`
	Format    SubscriptionFormat `json:"format"`
	Servers   []ServerRaw        `json:"servers"`
	UpdatedAt time.Time          `json:"updatedAt"`
	// 对应自动创建的 Group ID
	GroupID string `json:"groupId"`
	// 拉取订阅时使用的代理出站名，为空表示直连拉取
	ProxyOutbound string `json:"proxyOutbound,omitempty"`
}

type SubscriptionSummary struct {
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	URL           string             `json:"url"`
	Format        SubscriptionFormat `json:"format"`
	UpdatedAt     time.Time          `json:"updatedAt"`
	GroupID       string             `json:"groupId"`
	ProxyOutbound string             `json:"proxyOutbound,omitempty"`
	ServerCount   int                `json:"serverCount"`
}

func Bytes2SubscriptionRaw(b []byte) (*SubscriptionRaw, error) {
	var s SubscriptionRaw
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("Bytes2SubscriptionRaw: %w", err)
	}
	return &s, nil
}

// subscriptionServerKeyPrefix 是每个订阅单独存放节点列表的 key 前缀。用订阅的
// 稳定 ID（不是会因删除而移动的下标）做后缀，这样订阅下标变化不会影响节点数据。
const subscriptionServerKeyPrefix = "sub_servers."

func subscriptionServerKey(id string) string {
	return subscriptionServerKeyPrefix + id
}

// subscriptionDoc 是订阅在存储中的形态：只保存订阅自身的字段，节点单独按行存。
// 这样更新单个节点时不会重写整份订阅（大订阅可能有数千个节点）。
type subscriptionDoc struct {
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	URL           string             `json:"url"`
	Format        SubscriptionFormat `json:"format"`
	UpdatedAt     time.Time          `json:"updatedAt"`
	GroupID       string             `json:"groupId"`
	ProxyOutbound string             `json:"proxyOutbound,omitempty"`
}

func subscriptionDocFrom(sub *SubscriptionRaw) subscriptionDoc {
	return subscriptionDoc{
		ID:            sub.ID,
		Name:          sub.Name,
		URL:           sub.URL,
		Format:        sub.Format,
		UpdatedAt:     sub.UpdatedAt,
		GroupID:       sub.GroupID,
		ProxyOutbound: sub.ProxyOutbound,
	}
}

func (d subscriptionDoc) toRaw(servers []ServerRaw) SubscriptionRaw {
	if servers == nil {
		servers = []ServerRaw{}
	}
	return SubscriptionRaw{
		ID:            d.ID,
		Name:          d.Name,
		URL:           d.URL,
		Format:        d.Format,
		Servers:       servers,
		UpdatedAt:     d.UpdatedAt,
		GroupID:       d.GroupID,
		ProxyOutbound: d.ProxyOutbound,
	}
}

func ensureSubscriptionID(sub *SubscriptionRaw) {
	if strings.TrimSpace(sub.ID) != "" {
		return
	}
	if id, err := gonanoid.Nanoid(); err == nil {
		sub.ID = id
	}
}

// getSubscriptionDoc 只读取订阅自身的字段，不加载节点。
func getSubscriptionDoc(index int) (*subscriptionDoc, error) {
	return subscriptionDAO.get(index)
}

func getSubscriptionServersByID(id string) []ServerRaw {
	if strings.TrimSpace(id) == "" {
		return []ServerRaw{}
	}
	return nodeGetAll(nodeBucket, subscriptionServerKey(id))
}

func setSubscriptionServersByID(id string, servers []ServerRaw) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("subscription id is empty")
	}
	return nodeReplaceAll(nodeBucket, subscriptionServerKey(id), servers)
}

func deleteSubscriptionServersByID(id string) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	return nodeDeleteAll(nodeBucket, subscriptionServerKey(id))
}

func subscriptionIDByIndex(index int) (string, error) {
	doc, err := getSubscriptionDoc(index)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(doc.ID) == "" {
		return "", fmt.Errorf("subscription %d has no id", index)
	}
	return doc.ID, nil
}

func GetSubscriptions() []SubscriptionRaw {
	docs, err := subscriptionDAO.getAll()
	result := make([]SubscriptionRaw, 0)
	if err != nil {
		return result
	}
	for _, doc := range docs {
		result = append(result, doc.toRaw(getSubscriptionServersByID(doc.ID)))
	}
	return result
}

func GetSubscriptionSummaries() []SubscriptionSummary {
	docs, err := subscriptionDAO.getAll()
	result := make([]SubscriptionSummary, 0)
	if err != nil {
		return result
	}
	for _, doc := range docs {
		count := nodeLen(nodeBucket, subscriptionServerKey(doc.ID))
		result = append(result, SubscriptionSummary{
			ID: doc.ID, Name: doc.Name, URL: doc.URL, Format: doc.Format,
			UpdatedAt: doc.UpdatedAt, GroupID: doc.GroupID, ProxyOutbound: doc.ProxyOutbound,
			ServerCount: count,
		})
	}
	return result
}

func GetSubscription(index int) *SubscriptionRaw {
	doc, err := getSubscriptionDoc(index)
	if err != nil {
		return nil
	}
	sub := doc.toRaw(getSubscriptionServersByID(doc.ID))
	return &sub
}

func GetSubscriptionsByIndexes(indexes []int) map[int]SubscriptionRaw {
	result := make(map[int]SubscriptionRaw, len(indexes))
	byIndex, err := subscriptionDAO.getMany(indexes)
	if err != nil {
		return result
	}
	for index, doc := range byIndex {
		result[index] = doc.toRaw(getSubscriptionServersByID(doc.ID))
	}
	return result
}

// GetSubscriptionServer 精确读取某个订阅下的单个节点，不加载该订阅的其它节点。
// 探测、评分、元数据写回等按引用访问单个节点的场景应使用它，避免每次都解析整个订阅。
func GetSubscriptionServer(subIndex, serverIndex int) *ServerRaw {
	doc, err := getSubscriptionDoc(subIndex)
	if err != nil {
		return nil
	}
	return nodeGet(nodeBucket, subscriptionServerKey(doc.ID), serverIndex)
}

// GetSubscriptionServersByIndexes 批量精确读取某个订阅下的多个节点。
func GetSubscriptionServersByIndexes(subIndex int, indexes []int) map[int]ServerRaw {
	doc, err := getSubscriptionDoc(subIndex)
	if err != nil {
		return map[int]ServerRaw{}
	}
	return nodeGetMany(nodeBucket, subscriptionServerKey(doc.ID), indexes)
}

func AppendSubscriptions(subs []*SubscriptionRaw) error {
	docs := make([]subscriptionDoc, len(subs))
	for i, sub := range subs {
		ensureSubscriptionID(sub)
		// 先写节点列表再写订阅文档：即使文档写入失败，最多留下无引用的孤儿数据，
		// 不会出现“订阅存在但节点丢失”的情况。
		if err := setSubscriptionServersByID(sub.ID, sub.Servers); err != nil {
			return err
		}
		docs[i] = subscriptionDocFrom(sub)
	}
	return subscriptionDAO.append(docs)
}

func SetSubscription(index int, sub *SubscriptionRaw) error {
	ensureSubscriptionID(sub)
	return setSubscriptionWithID(index, sub, false)
}

func RefreshSubscriptionData(index int, sub *SubscriptionRaw) error {
	ensureSubscriptionID(sub)
	return setSubscriptionWithID(index, sub, true)
}

// UpdateSubscriptionMeta 只更新订阅自身的字段，不触碰节点列表。
func UpdateSubscriptionMeta(index int, sub *SubscriptionRaw) error {
	ensureSubscriptionID(sub)
	return subscriptionDAO.set(index, subscriptionDocFrom(sub))
}

func UpdateSubscriptionServer(subIndex, serverIndex int, update func(*ServerRaw)) error {
	id, err := subscriptionIDByIndex(subIndex)
	if err != nil {
		return err
	}
	return nodeUpdate(nodeBucket, subscriptionServerKey(id), serverIndex, update)
}

func UpdateSubscriptionServers(subIndex int, updates map[int]func(*ServerRaw)) error {
	id, err := subscriptionIDByIndex(subIndex)
	if err != nil {
		return err
	}
	return nodeUpdateMany(nodeBucket, subscriptionServerKey(id), updates)
}

func RemoveSubscriptions(indexes []int) error {
	return removeSubscriptionsWithRefs(indexes)
}

func GetLenSubscriptions() int {
	return subscriptionDAO.count()
}

func GetLenSubscriptionServers(index int) int {
	doc, err := getSubscriptionDoc(index)
	if err != nil {
		return 0
	}
	return nodeLen(nodeBucket, subscriptionServerKey(doc.ID))
}

// GetSubscriptionMeta 只返回订阅自身的字段（不含节点）。
func GetSubscriptionMeta(index int) *SubscriptionRaw {
	doc, err := getSubscriptionDoc(index)
	if err != nil {
		return nil
	}
	sub := doc.toRaw(nil)
	return &sub
}

// GetSubscriptionServersPage 分页读取订阅节点，返回该页节点与独立总数。
// limit <= 0 表示返回从 offset 起的全部节点。
func GetSubscriptionServersPage(subIndex, offset, limit int) ([]ServerRaw, int, error) {
	doc, err := getSubscriptionDoc(subIndex)
	if err != nil {
		return nil, 0, err
	}
	key := subscriptionServerKey(doc.ID)
	return nodeGetRange(nodeBucket, key, offset, limit)
}

// PreserveServerProbeResults copies fast/real probe metadata from old subscription nodes
// to newly parsed nodes when they represent the same server.
func PreserveServerProbeResults(oldServers, newServers []ServerRaw) {
	preserveServerResults(oldServers, newServers, true)
}

// PreserveServerRuntimeResults merges fields produced by background probes.
// It intentionally leaves FrontProxy untouched so explicit node edits and
// outbound renames are not reverted by the merge.
func PreserveServerRuntimeResults(oldServers, newServers []ServerRaw) {
	preserveServerResults(oldServers, newServers, false)
}

func preserveServerResults(oldServers, newServers []ServerRaw, preserveFrontProxy bool) {
	if len(oldServers) == 0 || len(newServers) == 0 {
		return
	}
	byKey := make(map[string]ServerRaw, len(oldServers)*2)
	for _, old := range oldServers {
		for _, key := range serverProbeKeys(old) {
			if _, ok := byKey[key]; !ok {
				byKey[key] = old
			}
		}
	}
	for i := range newServers {
		for _, key := range serverProbeKeys(newServers[i]) {
			if old, ok := byKey[key]; ok {
				newServers[i].Latency = old.Latency
				newServers[i].LastProbeTime = old.LastProbeTime
				newServers[i].FastLatency = old.FastLatency
				newServers[i].LastFastProbeTime = old.LastFastProbeTime
				if preserveFrontProxy {
					// 订阅刷新会整体覆盖节点，用户手动设的前置代理需要跟着保留
					newServers[i].FrontProxy = old.FrontProxy
				}
				// IP 类型检测有外部接口的频率限制，不能因为订阅刷新就丢掉重新查
				newServers[i].IPType = old.IPType
				newServers[i].IPTypeAddr = old.IPTypeAddr
				newServers[i].IPTypeISP = old.IPTypeISP
				newServers[i].IPTypeCheckedAt = old.IPTypeCheckedAt
				// 评分同理：外部服务限速严格，重查代价高
				newServers[i].IPScore = old.IPScore
				newServers[i].IPScoreGrade = old.IPScoreGrade
				newServers[i].IPScoreRisk = old.IPScoreRisk
				newServers[i].IPScoreProvider = old.IPScoreProvider
				newServers[i].IPScoreAt = old.IPScoreAt
				newServers[i].IPGeoCountry = old.IPGeoCountry
				newServers[i].IPGeoCountryCode = old.IPGeoCountryCode
				newServers[i].IPGeoRegion = old.IPGeoRegion
				newServers[i].IPGeoCity = old.IPGeoCity
				break
			}
		}
	}
}

// PreserveActiveSubscriptionServers keeps subscription nodes that are currently
// selected by an outbound when a refreshed subscription no longer contains them.
// It also remaps active outbound references when the same node moves index.
func PreserveActiveSubscriptionServers(subIndex int, oldServers, newServers []ServerRaw) []ServerRaw {
	if subIndex < 0 || len(oldServers) == 0 {
		return newServers
	}
	oldToNew := matchOldServerIndexes(oldServers, newServers)
	appended := map[int]int{}

	for _, name := range GetOutboundNames() {
		o := GetOutbound(name)
		if o == nil {
			continue
		}
		changed := false
		if remapActiveSubscriptionRef(o.Target.NodeRef, subIndex, oldServers, &newServers, oldToNew, appended) {
			changed = true
		}
		if remapActiveSubscriptionRef(o.Target.ActiveNodeRef, subIndex, oldServers, &newServers, oldToNew, appended) {
			changed = true
		}
		if changed {
			_ = SetOutbound(name, o)
		}
	}

	return newServers
}

func remapActiveSubscriptionRef(ref *NodeRef, subIndex int, oldServers []ServerRaw, newServers *[]ServerRaw, oldToNew map[int]int, appended map[int]int) bool {
	if ref == nil || ref.Type != "sub_server" || ref.Sub != subIndex {
		return false
	}
	if ref.Index < 0 || ref.Index >= len(oldServers) {
		return false
	}
	if newIndex, ok := oldToNew[ref.Index]; ok {
		if ref.Index == newIndex {
			return false
		}
		ref.Index = newIndex
		return true
	}
	if newIndex, ok := appended[ref.Index]; ok {
		if ref.Index == newIndex {
			return false
		}
		ref.Index = newIndex
		return true
	}
	newIndex := len(*newServers)
	*newServers = append(*newServers, oldServers[ref.Index])
	appended[ref.Index] = newIndex
	ref.Index = newIndex
	return true
}

func matchOldServerIndexes(oldServers, newServers []ServerRaw) map[int]int {
	newIndexByKey := make(map[string]int, len(newServers)*2)
	for i, server := range newServers {
		for _, key := range serverProbeKeys(server) {
			if _, ok := newIndexByKey[key]; !ok {
				newIndexByKey[key] = i
			}
		}
	}
	result := map[int]int{}
	for i, server := range oldServers {
		for _, key := range serverProbeKeys(server) {
			if newIndex, ok := newIndexByKey[key]; ok {
				result[i] = newIndex
				break
			}
		}
	}
	return result
}

func serverProbeKeys(server ServerRaw) []string {
	keys := make([]string, 0, 2)
	if link := strings.TrimSpace(server.Link); link != "" {
		keys = append(keys, "link:"+link)
	}
	serverKey := strings.ToLower(strings.TrimSpace(server.Type)) + "|" +
		strings.ToLower(strings.TrimSpace(server.Host)) + "|" +
		fmt.Sprintf("%d", server.Port) + "|" +
		strings.TrimSpace(server.Name)
	if serverKey != "||0|" {
		keys = append(keys, "server:"+serverKey)
	}
	return keys
}
