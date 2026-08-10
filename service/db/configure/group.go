package configure

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ProxyStation/proxystation/db"
)

// ServerRef 指向一个具体节点的引用
type ServerRef struct {
	// "server"（手动导入）或 "sub_server"（来自订阅）
	Type string `json:"type"`
	// 当 Type == "server" 时，是 servers 列表的下标（从0开始）
	Index int `json:"index"`
	// 当 Type == "sub_server" 时，是 subscriptions 列表的下标
	Sub int `json:"sub"`
}

// Group 是用户自定义的节点分组，与订阅解耦
// 订阅导入时会自动创建一个对应的 Group，手动节点也可以加入任意 Group
type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// 是否由订阅自动创建（自动创建的 Group 名称跟随订阅）
	FromSub   bool        `json:"fromSub"`
	SubID     string      `json:"subId,omitempty"`
	Servers   []ServerRef `json:"servers"`
	CreatedAt time.Time   `json:"createdAt"`
}

func Bytes2Group(b []byte) (*Group, error) {
	var g Group
	if err := json.Unmarshal(b, &g); err != nil {
		return nil, fmt.Errorf("Bytes2Group: %w", err)
	}
	return &g, nil
}

func GetGroups() []Group {
	result := make([]Group, 0)
	raw, err := db.ListGetAll("touch", "groups")
	if err != nil {
		return result
	}
	for _, b := range raw {
		g, err := Bytes2Group(b)
		if err != nil {
			continue
		}
		result = append(result, *g)
	}
	return result
}

func GetGroup(index int) *Group {
	b, err := db.ListGet("touch", "groups", index)
	if err != nil {
		return nil
	}
	g, _ := Bytes2Group(b)
	return g
}

func GetGroupByID(id string) (int, *Group) {
	groups := GetGroups()
	for i, g := range groups {
		if g.ID == id {
			return i, &groups[i]
		}
	}
	return -1, nil
}

func AppendGroup(group *Group) error {
	return db.ListAppend("touch", "groups", []*Group{group})
}

func SetGroup(index int, group *Group) error {
	return db.ListSet("touch", "groups", index, group)
}

func RemoveGroups(indexes []int) error {
	return db.ListRemove("touch", "groups", indexes)
}

func GetLenGroups() int {
	l, _ := db.ListLen("touch", "groups")
	return l
}

// AddServerToGroup 将节点引用加入分组
func AddServerToGroup(groupIndex int, ref ServerRef) error {
	return AddServersToGroup(groupIndex, []ServerRef{ref})
}

// AddServersToGroup 将多个节点引用一次性加入分组。
func AddServersToGroup(groupIndex int, refs []ServerRef) error {
	return db.ListUpdate("touch", "groups", groupIndex, func(raw []byte) ([]byte, error) {
		group, err := Bytes2Group(raw)
		if err != nil {
			return nil, err
		}
		existing := make(map[ServerRef]struct{}, len(group.Servers)+len(refs))
		for _, ref := range group.Servers {
			existing[ref] = struct{}{}
		}
		for _, ref := range refs {
			if _, ok := existing[ref]; ok {
				continue
			}
			group.Servers = append(group.Servers, ref)
			existing[ref] = struct{}{}
		}
		return json.Marshal(group)
	})
}

// RemoveServerFromGroup 从分组中移除节点引用
func RemoveServerFromGroup(groupIndex int, ref ServerRef) error {
	return RemoveServersFromGroup(groupIndex, []ServerRef{ref})
}

// RemoveServersFromGroup 将多个节点引用一次性移出分组。
func RemoveServersFromGroup(groupIndex int, refs []ServerRef) error {
	remove := make(map[ServerRef]struct{}, len(refs))
	for _, ref := range refs {
		remove[ref] = struct{}{}
	}
	return db.ListUpdate("touch", "groups", groupIndex, func(raw []byte) ([]byte, error) {
		group, err := Bytes2Group(raw)
		if err != nil {
			return nil, err
		}
		servers := make([]ServerRef, 0, len(group.Servers))
		for _, ref := range group.Servers {
			if _, ok := remove[ref]; !ok {
				servers = append(servers, ref)
			}
		}
		group.Servers = servers
		return json.Marshal(group)
	})
}
