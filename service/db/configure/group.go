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
	result, err := loadGroups()
	if err != nil {
		return []Group{}
	}
	return result
}

func GetGroup(index int) *Group {
	row, err := groupDAO.get(index)
	if err != nil {
		return nil
	}
	g := row.toGroup(groupServersGet(row.id))
	return &g
}

func GetGroupByID(id string) (int, *Group) {
	var index int
	if err := db.ReadDB().QueryRow(`SELECT idx FROM groups WHERE id = ?`, id).Scan(&index); err != nil {
		return -1, nil
	}
	row, err := groupDAO.get(index)
	if err != nil {
		return -1, nil
	}
	g := row.toGroup(groupServersGet(row.id))
	return index, &g
}

func GetGroupMetadataByID(id string) *Group {
	row, err := groupDAO.scan(db.ReadDB().QueryRow(
		`SELECT id, name, from_sub, sub_id, created_at FROM groups WHERE id = ?`, id).Scan)
	if err != nil {
		return nil
	}
	group := row.toGroup(nil)
	return &group
}

func AppendGroup(group *Group) error {
	return appendGroup(group)
}

func SetGroup(index int, group *Group) error {
	return setGroup(index, group)
}

func RemoveGroups(indexes []int) error {
	return removeGroups(indexes)
}

func GetLenGroups() int {
	return groupDAO.count()
}

// AddServerToGroup 将节点引用加入分组
func AddServerToGroup(groupIndex int, ref ServerRef) error {
	return AddServersToGroup(groupIndex, []ServerRef{ref})
}

// AddServersToGroup 将多个节点引用一次性加入分组。
func AddServersToGroup(groupIndex int, refs []ServerRef) error {
	return addServersToGroup(groupIndex, refs)
}

// RemoveServerFromGroup 从分组中移除节点引用
func RemoveServerFromGroup(groupIndex int, ref ServerRef) error {
	return RemoveServersFromGroup(groupIndex, []ServerRef{ref})
}

// RemoveServersFromGroup 将多个节点引用一次性移出分组。
func RemoveServersFromGroup(groupIndex int, refs []ServerRef) error {
	return removeServersFromGroup(groupIndex, refs)
}
