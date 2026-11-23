package configure

import (
	"database/sql"
	"encoding/json"
	"log"

	"github.com/ProxyStation/proxystation/db"
)

// subscriptions 表按 idx 保存订阅自身的元数据（不含节点）。field 与列一一对应，
// 不再把订阅文档序列化成 JSON。节点仍按行存在 nodes 表、用 sub_servers.<id> 关联。
var subscriptionDAO = idxRecordDAO[subscriptionDoc]{
	table:   "subscriptions",
	columns: []string{"id", "name", "url", "format", "updated_at", "group_id", "proxy_outbound"},
	args: func(d subscriptionDoc) []any {
		return []any{d.ID, d.Name, d.URL, string(d.Format), timeToInt(d.UpdatedAt), d.GroupID, d.ProxyOutbound}
	},
	scan: func(scan func(...any) error) (subscriptionDoc, error) {
		var d subscriptionDoc
		var format string
		var updatedAt int64
		if err := scan(&d.ID, &d.Name, &d.URL, &format, &updatedAt, &d.GroupID, &d.ProxyOutbound); err != nil {
			return d, err
		}
		d.Format = SubscriptionFormat(format)
		d.UpdatedAt = intToTime(updatedAt)
		return d, nil
	},
}

// MigrateSubscriptionStorage 把订阅从旧布局迁移到列式 subscriptions 表，并顺便把
// 旧布局里内嵌的节点拆到 nodes 表。幂等：list_items 里没有 subscriptions 行时为
// 空操作，所以第二次启动不会重复迁移，也不会覆盖迁移后用户/探测写入的数据。
func MigrateSubscriptionStorage() error {
	legacy, err := db.ListGetAll("touch", "subscriptions")
	if err != nil {
		return err
	}
	if len(legacy) == 0 {
		return nil
	}

	docs := make([]subscriptionDoc, 0, len(legacy))
	splitNodes := 0
	for _, raw := range legacy {
		var sub SubscriptionRaw
		if err := json.Unmarshal(raw, &sub); err != nil {
			continue
		}
		ensureSubscriptionID(&sub)
		if len(sub.Servers) > 0 {
			// nodes 表里已有该订阅节点时不再重写，避免迁移覆盖运行时结果。
			if nodeLen(nodeBucket, subscriptionServerKey(sub.ID)) == 0 {
				if err := setSubscriptionServersByID(sub.ID, sub.Servers); err != nil {
					return err
				}
				splitNodes += len(sub.Servers)
			}
		}
		docs = append(docs, subscriptionDocFrom(&sub))
	}

	if err := db.WriteTx(func(tx *sql.Tx) error {
		if err := subscriptionDAO.replaceAllTx(tx, docs); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM list_items WHERE bucket = ? AND key = ?`, "touch", "subscriptions")
		return err
	}); err != nil {
		return err
	}
	if splitNodes > 0 {
		log.Printf("已把 %d 个订阅节点从内嵌 JSON 拆到列式 nodes 表", splitNodes)
	}
	if len(docs) > 0 {
		log.Printf("已把 %d 个订阅从 JSON 文档迁移到列式 subscriptions 表", len(docs))
	}
	return nil
}
