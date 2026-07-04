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

func RemoveServers(indexes []int) error {
	return db.ListRemove("touch", "servers", indexes)
}

func GetLenServers() int {
	l, _ := db.ListLen("touch", "servers")
	return l
}
