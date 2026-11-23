package configure

import (
	"encoding/json"
	"fmt"
)

// ServerRaw 代表一个代理节点，可以是手动导入的，也可以来自订阅
type ServerRaw struct {
	// 节点链接原始内容，如 vmess://... vless://... ss://...
	Link string `json:"link"`
	// 解析后的节点信息（用于展示和连接）
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
	// 协议类型: vmess, vless, ss, trojan, hysteria2, tuic ...
	Type string `json:"type"`
	// 真连接延迟，单位 ms；是否检测过由 LastProbeTime 判断，非正数表示不通
	Latency int `json:"latency"`
	// 上次真连接检测时间（Unix 时间戳）
	LastProbeTime int64 `json:"lastProbeTime"`
	// 快速 TCP 检测延迟，单位 ms；是否检测过由 LastFastProbeTime 判断，非正数表示不通
	FastLatency int `json:"fastLatency"`
	// 上次快速检测时间（Unix 时间戳）
	LastFastProbeTime int64 `json:"lastFastProbeTime"`
	// 来源：manual（手动导入）或订阅ID
	Source string `json:"source"`
	// 前置代理出站名（用于链式代理，如 "proxy"），为空表示不使用前置
	FrontProxy string `json:"frontProxy,omitempty"`
	// 地址类型：idc（机房）| residential（家宽）| mobile（移动网络）| unknown，空表示未检测
	IPType string `json:"ipType,omitempty"`
	// 检测时解析到的 IP 与运营商，用于展示判定依据
	IPTypeAddr string `json:"ipTypeAddr,omitempty"`
	IPTypeISP  string `json:"ipTypeIsp,omitempty"`
	// 上次检测时间（Unix 时间戳）
	IPTypeCheckedAt int64 `json:"ipTypeCheckedAt,omitempty"`
	// IP 纯净度评分 0-100，越高越可信。0 值无意义，用指针区分"未检测"与"真的 0 分"
	IPScore *int `json:"ipScore,omitempty"`
	// 评分等级与风险描述，以及产出该评分的服务商，便于界面说明依据
	IPScoreGrade    string `json:"ipScoreGrade,omitempty"`
	IPScoreRisk     string `json:"ipScoreRisk,omitempty"`
	IPScoreProvider string `json:"ipScoreProvider,omitempty"`
	IPScoreAt       int64  `json:"ipScoreAt,omitempty"`
	// IP 评分接口同一次响应附带的地理位置
	IPGeoCountry     string `json:"ipGeoCountry,omitempty"`
	IPGeoCountryCode string `json:"ipGeoCountryCode,omitempty"`
	IPGeoRegion      string `json:"ipGeoRegion,omitempty"`
	IPGeoCity        string `json:"ipGeoCity,omitempty"`
}

func Bytes2ServerRaw(b []byte) (*ServerRaw, error) {
	var s ServerRaw
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("Bytes2ServerRaw: %w", err)
	}
	return &s, nil
}

func GetServers() []ServerRaw {
	return nodeGetAll(nodeBucket, manualServersKey)
}

func GetServer(index int) *ServerRaw {
	return nodeGet(nodeBucket, manualServersKey, index)
}

func GetServersByIndexes(indexes []int) map[int]ServerRaw {
	return nodeGetMany(nodeBucket, manualServersKey, indexes)
}

// GetNodesByRefs 按引用精确批量读取节点：手动节点读 servers 行，订阅节点只读取该
// 订阅下被引用的行，不加载整份订阅。
func GetNodesByRefs(refs []NodeRef) map[NodeRef]ServerRaw {
	result := make(map[NodeRef]ServerRaw, len(refs))
	if len(refs) == 0 {
		return result
	}
	manualIndexes := make([]int, 0, len(refs))
	seenManual := make(map[int]struct{}, len(refs))
	subIndexes := make(map[int][]int)
	seenSubIndex := make(map[int]map[int]struct{})
	for _, ref := range refs {
		switch ref.Type {
		case "server":
			if _, ok := seenManual[ref.Index]; !ok {
				seenManual[ref.Index] = struct{}{}
				manualIndexes = append(manualIndexes, ref.Index)
			}
		case "sub_server":
			if seenSubIndex[ref.Sub] == nil {
				seenSubIndex[ref.Sub] = make(map[int]struct{})
			}
			if _, ok := seenSubIndex[ref.Sub][ref.Index]; !ok {
				seenSubIndex[ref.Sub][ref.Index] = struct{}{}
				subIndexes[ref.Sub] = append(subIndexes[ref.Sub], ref.Index)
			}
		}
	}
	manual := GetServersByIndexes(manualIndexes)
	subServers := make(map[int]map[int]ServerRaw, len(subIndexes))
	for sub, indexes := range subIndexes {
		subServers[sub] = GetSubscriptionServersByIndexes(sub, indexes)
	}
	for _, ref := range refs {
		switch ref.Type {
		case "server":
			if server, ok := manual[ref.Index]; ok {
				result[ref] = server
			}
		case "sub_server":
			if server, ok := subServers[ref.Sub][ref.Index]; ok {
				result[ref] = server
			}
		}
	}
	return result
}

func AppendServers(servers []*ServerRaw) error {
	values := make([]ServerRaw, len(servers))
	for i, s := range servers {
		if s != nil {
			values[i] = *s
		}
	}
	return nodeAppend(nodeBucket, manualServersKey, values)
}

func SetServers(servers []ServerRaw) error {
	return nodeReplaceAll(nodeBucket, manualServersKey, servers)
}

func SetServer(index int, server *ServerRaw) error {
	return nodeSet(nodeBucket, manualServersKey, index, server)
}

func UpdateServer(index int, update func(*ServerRaw)) error {
	return nodeUpdate(nodeBucket, manualServersKey, index, update)
}

func UpdateServers(updates map[int]func(*ServerRaw)) error {
	return nodeUpdateMany(nodeBucket, manualServersKey, updates)
}

func RemoveServers(indexes []int) error {
	return removeManualServersWithRefs(indexes)
}

func GetLenServers() int {
	return nodeLen(nodeBucket, manualServersKey)
}
