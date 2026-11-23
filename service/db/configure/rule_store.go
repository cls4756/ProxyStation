package configure

import (
	"database/sql"
	"encoding/json"
	"log"

	"github.com/ProxyStation/proxystation/db"
)

// routingRuleRow 是 routing_rules 表的标量字段；入站标签/域名/IP 这类可能很长的
// 匹配列表拆到子表（见 child_list.go），改一条规则不再重写整份规则数组。
type routingRuleRow struct {
	id           string
	enabled      bool
	name         string
	ports        string
	protocol     string
	action       RuleAction
	outboundName string
}

var routingRuleDAO = idxRecordDAO[routingRuleRow]{
	table:   "routing_rules",
	columns: []string{"id", "enabled", "name", "ports", "protocol", "action", "outbound_name"},
	args: func(r routingRuleRow) []any {
		return []any{r.id, r.enabled, r.name, r.ports, r.protocol, string(r.action), r.outboundName}
	},
	scan: func(scan func(...any) error) (routingRuleRow, error) {
		var r routingRuleRow
		var action string
		if err := scan(&r.id, &r.enabled, &r.name, &r.ports, &r.protocol, &action, &r.outboundName); err != nil {
			return r, err
		}
		r.action = RuleAction(action)
		return r, nil
	},
}

func routingRuleRowFrom(r RoutingRule) routingRuleRow {
	return routingRuleRow{id: r.ID, enabled: r.Enabled, name: r.Name, ports: r.Ports, protocol: r.Protocol, action: r.Action, outboundName: r.OutboundName}
}

const (
	routingTagsTable    = "routing_rule_inbound_tags"
	routingDomainsTable = "routing_rule_domains"
	routingIPsTable     = "routing_rule_ips"
)

func loadRoutingRules() []RoutingRule {
	rows, err := routingRuleDAO.getAll()
	if err != nil {
		return []RoutingRule{}
	}
	tags := stringListAll(routingTagsTable, "rule_idx")
	domains := stringListAll(routingDomainsTable, "rule_idx")
	ips := stringListAll(routingIPsTable, "rule_idx")
	result := make([]RoutingRule, 0, len(rows))
	for i, r := range rows {
		result = append(result, RoutingRule{
			ID: r.id, Enabled: r.enabled, Name: r.name,
			InboundTags: tags[i], Domains: domains[i], IPs: ips[i],
			Ports: r.ports, Protocol: r.protocol, Action: r.action, OutboundName: r.outboundName,
		})
	}
	return result
}

func replaceRoutingRules(rules []RoutingRule) error {
	rows := make([]routingRuleRow, len(rules))
	for i := range rules {
		rows[i] = routingRuleRowFrom(rules[i])
	}
	return db.WriteTx(func(tx *sql.Tx) error {
		if err := routingRuleDAO.replaceAllTx(tx, rows); err != nil {
			return err
		}
		for _, table := range []string{routingTagsTable, routingDomainsTable, routingIPsTable} {
			if _, err := tx.Exec(`DELETE FROM ` + table); err != nil {
				return err
			}
		}
		for i := range rules {
			if err := replaceStringListTx(tx, routingTagsTable, "rule_idx", i, rules[i].InboundTags); err != nil {
				return err
			}
			if err := replaceStringListTx(tx, routingDomainsTable, "rule_idx", i, rules[i].Domains); err != nil {
				return err
			}
			if err := replaceStringListTx(tx, routingIPsTable, "rule_idx", i, rules[i].IPs); err != nil {
				return err
			}
		}
		return nil
	})
}

// MigrateRoutingRuleStorage 把 routing_rules 从 kv 里的单个 JSON 数组迁移到列式表。
// 幂等：kv 里没有该键时直接跳过。
func MigrateRoutingRuleStorage() error {
	raw, err := db.GetRaw("system", "routing_rules")
	if err != nil {
		return nil
	}
	var rules []RoutingRule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil
	}
	if err := replaceRoutingRules(rules); err != nil {
		return err
	}
	if err := db.WriteTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM kv WHERE bucket = ? AND key = ?`, "system", "routing_rules"); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM list_items WHERE bucket = ? AND key = ?`, "system", "routing_rules")
		return err
	}); err != nil {
		return err
	}
	if len(rules) > 0 {
		log.Printf("已把 %d 条路由规则迁移到列式存储", len(rules))
	}
	return nil
}
