package configure

import (
	"encoding/json"
	"fmt"

	"github.com/ProxyStation/proxystation/db"
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
	// 延迟，单位 ms，-1 表示未测速，0 表示超时
	Latency int `json:"latency"`
	// 上次测速时间（Unix 时间戳）
	LastProbeTime int64 `json:"lastProbeTime"`
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
	result := make([]ServerRaw, 0)
	raw, err := db.ListGetAll("touch", "servers")
	if err != nil {
		return result
	}
	for _, b := range raw {
		s, err := Bytes2ServerRaw(b)
		if err != nil {
			continue
		}
		result = append(result, *s)
	}
	return result
}

func GetServer(index int) *ServerRaw {
	b, err := db.ListGet("touch", "servers", index)
	if err != nil {
		return nil
	}
	s, _ := Bytes2ServerRaw(b)
	return s
}

func AppendServers(servers []*ServerRaw) error {
	return db.ListAppend("touch", "servers", servers)
}

func SetServers(servers []ServerRaw) error {
	return db.Set("touch", "servers", servers)
}

func SetServer(index int, server *ServerRaw) error {
	return db.ListSet("touch", "servers", index, server)
}

func UpdateServer(index int, update func(*ServerRaw)) error {
	return db.ListUpdate("touch", "servers", index, func(raw []byte) ([]byte, error) {
		server, err := Bytes2ServerRaw(raw)
		if err != nil {
			return nil, err
		}
		update(server)
		return json.Marshal(server)
	})
}

func RemoveServers(indexes []int) error {
	return db.ListRemove("touch", "servers", indexes)
}

func GetLenServers() int {
	l, _ := db.ListLen("touch", "servers")
	return l
}
