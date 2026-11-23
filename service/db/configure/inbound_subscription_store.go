package configure

import (
	"database/sql"
	"encoding/json"
	"log"

	"github.com/ProxyStation/proxystation/db"
)

// inbound_subscriptions 表按列存发布入站订阅的元数据；inbound_ids 拆子表，
// 规则列表（含嵌套 domains/ips）与父行整体读写，保留 JSON 列。
type inboundSubscriptionRow struct {
	id        string
	name      string
	token     string
	host      string
	format    string
	rules     string
	createdAt int64
	updatedAt int64
}

var inboundSubscriptionDAO = idxRecordDAO[inboundSubscriptionRow]{
	table:   "inbound_subscriptions",
	columns: []string{"id", "name", "token", "host", "format", "rules", "created_at", "updated_at"},
	args: func(r inboundSubscriptionRow) []any {
		return []any{r.id, r.name, r.token, r.host, r.format, r.rules, r.createdAt, r.updatedAt}
	},
	scan: func(scan func(...any) error) (inboundSubscriptionRow, error) {
		var r inboundSubscriptionRow
		if err := scan(&r.id, &r.name, &r.token, &r.host, &r.format, &r.rules, &r.createdAt, &r.updatedAt); err != nil {
			return r, err
		}
		return r, nil
	},
}

func inboundSubscriptionRowFrom(v InboundSubscription) inboundSubscriptionRow {
	rules := "[]"
	if len(v.Rules) > 0 {
		if b, err := json.Marshal(v.Rules); err == nil {
			rules = string(b)
		}
	}
	return inboundSubscriptionRow{
		id: v.ID, name: v.Name, token: v.Token, host: v.Host, format: v.Format,
		rules: rules, createdAt: timeToInt(v.CreatedAt), updatedAt: timeToInt(v.UpdatedAt),
	}
}

func (r inboundSubscriptionRow) toValue(inboundIDs []string) InboundSubscription {
	if inboundIDs == nil {
		inboundIDs = []string{}
	}
	v := InboundSubscription{
		ID: r.id, Name: r.name, Token: r.token, Host: r.host, Format: r.format,
		InboundIDs: inboundIDs, CreatedAt: intToTime(r.createdAt), UpdatedAt: intToTime(r.updatedAt),
	}
	if r.rules != "" {
		_ = json.Unmarshal([]byte(r.rules), &v.Rules)
	}
	return v
}

func loadInboundSubscriptions() []InboundSubscription {
	rows, err := inboundSubscriptionDAO.getAll()
	if err != nil {
		return []InboundSubscription{}
	}
	ids := stringListAll("inbound_subscription_inbounds", "sub_idx")
	result := make([]InboundSubscription, 0, len(rows))
	for i, r := range rows {
		result = append(result, r.toValue(ids[i]))
	}
	return result
}

func appendInboundSubscription(v *InboundSubscription) error {
	return db.WriteTx(func(tx *sql.Tx) error {
		if err := inboundSubscriptionDAO.appendTx(tx, []inboundSubscriptionRow{inboundSubscriptionRowFrom(*v)}); err != nil {
			return err
		}
		var index int
		if err := tx.QueryRow(`SELECT COALESCE(MAX(idx), -1) FROM inbound_subscriptions`).Scan(&index); err != nil {
			return err
		}
		return replaceStringListTx(tx, "inbound_subscription_inbounds", "sub_idx", index, v.InboundIDs)
	})
}

func setInboundSubscription(index int, v *InboundSubscription) error {
	return db.WriteTx(func(tx *sql.Tx) error {
		if err := inboundSubscriptionDAO.setTx(tx, index, inboundSubscriptionRowFrom(*v)); err != nil {
			return err
		}
		return replaceStringListTx(tx, "inbound_subscription_inbounds", "sub_idx", index, v.InboundIDs)
	})
}

func deleteInboundSubscription(index int) error {
	return db.WriteTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM inbound_subscription_inbounds WHERE sub_idx = ?`, index); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM inbound_subscriptions WHERE idx = ?`, index); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE inbound_subscriptions SET idx = idx - 1 WHERE idx > ?`, index)
		return err
	})
}

// MigrateInboundSubscriptionStorage 把 inbound_subscriptions 从 list_items 的整行
// JSON 迁移到列式表。幂等：list_items 里没有该键时跳过。
func MigrateInboundSubscriptionStorage() error {
	legacy, err := db.ListGetAll("touch", "inbound_subscriptions")
	if err != nil || len(legacy) == 0 {
		return nil
	}
	values := make([]InboundSubscription, 0, len(legacy))
	for _, raw := range legacy {
		var v InboundSubscription
		if err := json.Unmarshal(raw, &v); err != nil {
			continue
		}
		values = append(values, v)
	}
	if err := replaceInboundSubscriptions(values); err != nil {
		return err
	}
	if err := db.WriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM list_items WHERE bucket = ? AND key = ?`, "touch", "inbound_subscriptions")
		return err
	}); err != nil {
		return err
	}
	if len(values) > 0 {
		log.Printf("已把 %d 个入站订阅迁移到列式存储", len(values))
	}
	return nil
}

func replaceInboundSubscriptions(values []InboundSubscription) error {
	rows := make([]inboundSubscriptionRow, len(values))
	for i := range values {
		rows[i] = inboundSubscriptionRowFrom(values[i])
	}
	return db.WriteTx(func(tx *sql.Tx) error {
		if err := inboundSubscriptionDAO.replaceAllTx(tx, rows); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM inbound_subscription_inbounds`); err != nil {
			return err
		}
		for i := range values {
			if err := replaceStringListTx(tx, "inbound_subscription_inbounds", "sub_idx", i, values[i].InboundIDs); err != nil {
				return err
			}
		}
		return nil
	})
}
