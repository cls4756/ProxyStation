package configure

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/ProxyStation/proxystation/db"
	gonanoid "github.com/matoous/go-nanoid"
)

// groupRow 是 groups 表的一行（不含节点引用）。节点引用可能上万条，单独存在
// group_servers 子表里，按稳定的分组 ID 关联，避免分组下标变化或单个引用增删
// 时重写整份分组。
type groupRow struct {
	id        string
	name      string
	fromSub   bool
	subID     string
	createdAt time.Time
}

var groupDAO = idxRecordDAO[groupRow]{
	table:   "groups",
	columns: []string{"id", "name", "from_sub", "sub_id", "created_at"},
	args: func(r groupRow) []any {
		return []any{r.id, r.name, r.fromSub, r.subID, timeToInt(r.createdAt)}
	},
	scan: func(scan func(...any) error) (groupRow, error) {
		var r groupRow
		var created int64
		if err := scan(&r.id, &r.name, &r.fromSub, &r.subID, &created); err != nil {
			return r, err
		}
		r.createdAt = intToTime(created)
		return r, nil
	},
}

func groupRowFrom(g *Group) groupRow {
	return groupRow{id: g.ID, name: g.Name, fromSub: g.FromSub, subID: g.SubID, createdAt: g.CreatedAt}
}

func (r groupRow) toGroup(refs []ServerRef) Group {
	if refs == nil {
		refs = []ServerRef{}
	}
	return Group{ID: r.id, Name: r.name, FromSub: r.fromSub, SubID: r.subID, Servers: refs, CreatedAt: r.createdAt}
}

func ensureGroupID(group *Group) {
	if group == nil || strings.TrimSpace(group.ID) != "" {
		return
	}
	if id, err := gonanoid.Nanoid(); err == nil {
		group.ID = id
	}
}

// ---- group_servers 子表 ----

func groupServersAllTx(q interface {
	Query(query string, args ...any) (*sql.Rows, error)
}) (map[string][]ServerRef, error) {
	rows, err := q.Query(`SELECT group_id, type, ref_index, sub FROM group_servers ORDER BY group_id, ref_idx`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string][]ServerRef)
	for rows.Next() {
		var groupID string
		var ref ServerRef
		if err := rows.Scan(&groupID, &ref.Type, &ref.Index, &ref.Sub); err != nil {
			return nil, err
		}
		result[groupID] = append(result[groupID], ref)
	}
	return result, rows.Err()
}

func groupServersAll() map[string][]ServerRef {
	result, err := groupServersAllTx(db.ReadDB())
	if err != nil {
		return map[string][]ServerRef{}
	}
	return result
}

func groupServersGet(groupID string) []ServerRef {
	if strings.TrimSpace(groupID) == "" {
		return []ServerRef{}
	}
	rows, err := db.ReadDB().Query(`SELECT type, ref_index, sub FROM group_servers WHERE group_id = ? ORDER BY ref_idx`, groupID)
	if err != nil {
		return []ServerRef{}
	}
	defer rows.Close()
	refs := make([]ServerRef, 0)
	for rows.Next() {
		var ref ServerRef
		if err := rows.Scan(&ref.Type, &ref.Index, &ref.Sub); err != nil {
			return refs
		}
		refs = append(refs, ref)
	}
	return refs
}

func groupServersReplaceTx(tx *sql.Tx, groupID string, refs []ServerRef) error {
	unique, retargets, err := deduplicateGroupRefsTx(tx, groupID, refs)
	if err != nil {
		return err
	}
	independent, remapped, err := independentGroupRefsTx(tx, groupID, unique)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM group_servers WHERE group_id = ?`, groupID); err != nil {
		return err
	}
	if err := groupServersInsertTx(tx, groupID, independent, 0); err != nil {
		return err
	}
	return retargetIndependentGroupOutboundsTx(tx, groupID, remapped, retargets)
}

func groupServersInsertTx(tx *sql.Tx, groupID string, refs []ServerRef, startIdx int) error {
	if len(refs) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`INSERT INTO group_servers(group_id, ref_idx, type, ref_index, sub) VALUES(?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i, ref := range refs {
		if _, err := stmt.Exec(groupID, startIdx+i, ref.Type, ref.Index, ref.Sub); err != nil {
			return err
		}
	}
	return nil
}

func groupServersAddTx(tx *sql.Tx, groupID string, refs []ServerRef) error {
	if len(refs) == 0 {
		return nil
	}
	existing, err := groupRefsTx(tx, groupID)
	if err != nil {
		return err
	}
	unique, err := uniqueGroupContentRefsTx(tx, existing, refs)
	if err != nil {
		return err
	}
	independent, remapped, err := independentGroupRefsTx(tx, groupID, unique)
	if err != nil {
		return err
	}

	var next int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(ref_idx), -1) + 1 FROM group_servers WHERE group_id = ?`, groupID).Scan(&next); err != nil {
		return err
	}
	if err := groupServersInsertTx(tx, groupID, independent, next); err != nil {
		return err
	}
	return retargetIndependentGroupOutboundsTx(tx, groupID, remapped, nil)
}

func groupServersRemoveTx(tx *sql.Tx, groupID string, refs []ServerRef) error {
	if len(refs) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`DELETE FROM group_servers WHERE group_id = ? AND type = ? AND ref_index = ? AND sub = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, ref := range refs {
		if _, err := stmt.Exec(groupID, ref.Type, ref.Index, ref.Sub); err != nil {
			return err
		}
	}
	return nil
}

// ---- 读 ----

func loadGroups() ([]Group, error) {
	rows, err := groupDAO.getAll()
	if err != nil {
		return nil, err
	}
	refsByGroup := groupServersAll()
	result := make([]Group, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.toGroup(refsByGroup[row.id]))
	}
	return result, nil
}

// ---- 写 ----

func appendGroup(group *Group) error {
	ensureGroupID(group)
	err := db.WriteTx(func(tx *sql.Tx) error {
		if err := groupDAO.appendTx(tx, []groupRow{groupRowFrom(group)}); err != nil {
			return err
		}
		return groupServersReplaceTx(tx, group.ID, group.Servers)
	})
	invalidateGroupRefIndex()
	return err
}

func setGroup(index int, group *Group) error {
	ensureGroupID(group)
	err := db.WriteTx(func(tx *sql.Tx) error {
		var oldID string
		err := tx.QueryRow(`SELECT id FROM groups WHERE idx = ?`, index).Scan(&oldID)
		if err == sql.ErrNoRows {
			return fmt.Errorf("index %d out of range", index)
		}
		if err != nil {
			return err
		}
		if err := groupDAO.setTx(tx, index, groupRowFrom(group)); err != nil {
			return err
		}
		if oldID != group.ID {
			if _, err := tx.Exec(`DELETE FROM group_servers WHERE group_id = ?`, oldID); err != nil {
				return err
			}
		}
		return groupServersReplaceTx(tx, group.ID, group.Servers)
	})
	invalidateGroupRefIndex()
	return err
}

func removeGroups(indexes []int) error {
	remove := dedupeInts(indexes)
	if len(remove) == 0 {
		return nil
	}
	sort.Sort(sort.Reverse(sort.IntSlice(remove)))
	err := db.WriteTx(func(tx *sql.Tx) error {
		for _, index := range remove {
			var id string
			err := tx.QueryRow(`SELECT id FROM groups WHERE idx = ?`, index).Scan(&id)
			if err == sql.ErrNoRows {
				continue
			}
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM group_servers WHERE group_id = ?`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM groups WHERE idx = ?`, index); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE groups SET idx = idx - 1 WHERE idx > ?`, index); err != nil {
				return err
			}
		}
		return nil
	})
	invalidateGroupRefIndex()
	return err
}

func addServersToGroup(groupIndex int, refs []ServerRef) error {
	row, err := groupDAO.get(groupIndex)
	if err != nil {
		return err
	}
	err = db.WriteTx(func(tx *sql.Tx) error {
		return groupServersAddTx(tx, row.id, refs)
	})
	invalidateGroupRefIndex()
	return err
}

func removeServersFromGroup(groupIndex int, refs []ServerRef) error {
	row, err := groupDAO.get(groupIndex)
	if err != nil {
		return err
	}
	err = db.WriteTx(func(tx *sql.Tx) error {
		return groupServersRemoveTx(tx, row.id, refs)
	})
	invalidateGroupRefIndex()
	return err
}

// MigrateGroupStorage 把分组从 list_items 里的整份 JSON 迁移到 groups + group_servers
// 列式表。幂等：已迁移则 list_items 里没有 groups 行，直接跳过。
func MigrateGroupStorage() error {
	legacy, err := db.ListGetAll("touch", "groups")
	if err != nil {
		return err
	}
	if len(legacy) == 0 {
		return nil
	}
	groups := make([]Group, 0, len(legacy))
	for _, raw := range legacy {
		var g Group
		if err := json.Unmarshal(raw, &g); err != nil {
			continue
		}
		ensureGroupID(&g)
		groups = append(groups, g)
	}
	refCount := 0
	err = db.WriteTx(func(tx *sql.Tx) error {
		for i := range groups {
			if err := groupDAO.appendTx(tx, []groupRow{groupRowFrom(&groups[i])}); err != nil {
				return err
			}
			if err := groupServersReplaceTx(tx, groups[i].ID, groups[i].Servers); err != nil {
				return err
			}
			refCount += len(groups[i].Servers)
		}
		_, err := tx.Exec(`DELETE FROM list_items WHERE bucket = ? AND key = ?`, "touch", "groups")
		return err
	})
	if err != nil {
		return err
	}
	if len(groups) > 0 {
		log.Printf("已把 %d 个分组（%d 条节点引用）迁移到列式存储", len(groups), refCount)
	}
	return nil
}
