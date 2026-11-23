package configure

import (
	"database/sql"
	"encoding/json"
	"log"
	"strings"

	"github.com/ProxyStation/proxystation/db"
)

// outbounds 表以出站名为主键（"proxy" 为默认出站）。目标里的协议专属选项
// （nodeRef/activeNodeRef/cfgoodnet/cfdo）整体读写，保持 JSON 列，其余字段按列存。
// 这样出站的名字本身就是主键，不再需要 "outbounds" 存在标记桶 + "outbound.<name>"
// 配置桶两处存储。

type outboundRow struct {
	name              string
	targetType        string
	nodeRef           sql.NullString
	groupId           string
	mode              string
	activeNodeRef     sql.NullString
	specialType       string
	specialCfGoodNet  sql.NullString
	specialCfDO       sql.NullString
	probeURL          string
	probeInterval     string
	probeMode         string
	switchThresholdMs sql.NullInt64
	switchCooldownSec sql.NullInt64
}

func jsonOrNull(v any) sql.NullString {
	// 注意：typed nil（如 (*NodeRef)(nil)）转成 any 后 != nil，必须靠序列化结果判断，
	// 否则会把 NULL 存成字符串 "null"，读回时变成非空零值指针。
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		return sql.NullString{}
	}
	return sql.NullString{String: string(b), Valid: true}
}

func intOrNull(v *int) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*v), Valid: true}
}

func outboundRowFrom(o *Outbound) outboundRow {
	return outboundRow{
		name:              o.Name,
		targetType:        o.Target.TargetType,
		nodeRef:           jsonOrNull(o.Target.NodeRef),
		groupId:           o.Target.GroupID,
		mode:              string(o.Target.Mode),
		activeNodeRef:     jsonOrNull(o.Target.ActiveNodeRef),
		specialType:       o.Target.SpecialType,
		specialCfGoodNet:  jsonOrNull(o.Target.CfGoodNet),
		specialCfDO:       jsonOrNull(o.Target.CfDO),
		probeURL:          o.ProbeURL,
		probeInterval:     o.ProbeInterval,
		probeMode:         o.ProbeMode,
		switchThresholdMs: intOrNull(o.SwitchThresholdMs),
		switchCooldownSec: intOrNull(o.SwitchCooldownSec),
	}
}

func (r outboundRow) toOutbound() *Outbound {
	o := &Outbound{
		Name:          r.name,
		ProbeURL:      r.probeURL,
		ProbeInterval: r.probeInterval,
		ProbeMode:     r.probeMode,
		Target: OutboundTarget{
			TargetType:  r.targetType,
			GroupID:     r.groupId,
			Mode:        SelectionMode(r.mode),
			SpecialType: r.specialType,
		},
	}
	if r.nodeRef.Valid {
		o.Target.NodeRef = &NodeRef{}
		_ = json.Unmarshal([]byte(r.nodeRef.String), o.Target.NodeRef)
	}
	if r.activeNodeRef.Valid {
		o.Target.ActiveNodeRef = &NodeRef{}
		_ = json.Unmarshal([]byte(r.activeNodeRef.String), o.Target.ActiveNodeRef)
	}
	if r.specialCfGoodNet.Valid {
		o.Target.CfGoodNet = &CfGoodNetOutbound{}
		_ = json.Unmarshal([]byte(r.specialCfGoodNet.String), o.Target.CfGoodNet)
	}
	if r.specialCfDO.Valid {
		o.Target.CfDO = &CfDOOutbound{}
		_ = json.Unmarshal([]byte(r.specialCfDO.String), o.Target.CfDO)
	}
	if r.switchThresholdMs.Valid {
		v := int(r.switchThresholdMs.Int64)
		o.SwitchThresholdMs = &v
	}
	if r.switchCooldownSec.Valid {
		v := int(r.switchCooldownSec.Int64)
		o.SwitchCooldownSec = &v
	}
	return o
}

const outboundColumns = "name, target_type, node_ref, group_id, mode, active_node_ref, special_type, special_cfgoodnet, special_cfdo, probe_url, probe_interval, probe_mode, switch_threshold_ms, switch_cooldown_sec"

func scanOutbound(scan func(...any) error) (*Outbound, error) {
	var r outboundRow
	if err := scan(&r.name, &r.targetType, &r.nodeRef, &r.groupId, &r.mode, &r.activeNodeRef,
		&r.specialType, &r.specialCfGoodNet, &r.specialCfDO, &r.probeURL, &r.probeInterval,
		&r.probeMode, &r.switchThresholdMs, &r.switchCooldownSec); err != nil {
		return nil, err
	}
	return r.toOutbound(), nil
}

func outboundRowArgs(r outboundRow) []any {
	return []any{r.name, r.targetType, r.nodeRef, r.groupId, r.mode, r.activeNodeRef,
		r.specialType, r.specialCfGoodNet, r.specialCfDO, r.probeURL, r.probeInterval,
		r.probeMode, r.switchThresholdMs, r.switchCooldownSec}
}

func getOutboundStore(name string) *Outbound {
	row := db.ReadDB().QueryRow(`SELECT `+outboundColumns+` FROM outbounds WHERE name = ?`, name)
	o, err := scanOutbound(row.Scan)
	if err != nil {
		return nil
	}
	return o
}

func setOutboundStore(o *Outbound) error {
	r := outboundRowFrom(o)
	return db.WriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO outbounds(`+outboundColumns+`) VALUES(`+placeholders(14)+`)
			ON CONFLICT(name) DO UPDATE SET
				target_type=excluded.target_type, node_ref=excluded.node_ref, group_id=excluded.group_id,
				mode=excluded.mode, active_node_ref=excluded.active_node_ref, special_type=excluded.special_type,
				special_cfgoodnet=excluded.special_cfgoodnet, special_cfdo=excluded.special_cfdo,
				probe_url=excluded.probe_url, probe_interval=excluded.probe_interval, probe_mode=excluded.probe_mode,
				switch_threshold_ms=excluded.switch_threshold_ms, switch_cooldown_sec=excluded.switch_cooldown_sec`,
			outboundRowArgs(r)...)
		return err
	})
}

func outboundExists(name string) bool {
	var one int
	err := db.ReadDB().QueryRow(`SELECT 1 FROM outbounds WHERE name = ?`, name).Scan(&one)
	return err == nil
}

func deleteOutboundStore(name string) error {
	return db.WriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM outbounds WHERE name = ?`, name)
		return err
	})
}

func outboundNamesStore() []string {
	rows, err := db.ReadDB().Query(`SELECT name FROM outbounds ORDER BY name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return names
		}
		names = append(names, name)
	}
	return names
}

// MigrateOutboundStorage 把出站从「outbounds 存在标记桶 + outbound.<name> 配置桶」
// 迁移到单个 outbounds 表。幂等：已无旧桶数据时跳过。
func MigrateOutboundStorage() error {
	type legacyRow struct {
		bucket string
		name   string
		config []byte
	}
	legacy := make([]legacyRow, 0)
	// 存在标记（"outbounds" 桶里的每个 key 就是一个出站名）。
	if names, err := db.GetBucketKeys("outbounds"); err == nil {
		for _, name := range names {
			legacy = append(legacy, legacyRow{name: name})
		}
	}
	// 配置（"outbound.<name>" 桶的 config 值）。
	rows, err := db.ReadDB().Query(`SELECT bucket, value FROM kv WHERE bucket LIKE 'outbound.%' AND key = 'config'`)
	if err == nil {
		for rows.Next() {
			var bucket string
			var value []byte
			if err := rows.Scan(&bucket, &value); err != nil {
				break
			}
			name := strings.TrimPrefix(bucket, "outbound.")
			legacy = append(legacy, legacyRow{bucket: bucket, name: name, config: value})
		}
		rows.Close()
	}
	if len(legacy) == 0 {
		return nil
	}
	// 以配置为准合并：存在标记只贡献名字，配置贡献完整内容。
	byName := map[string]*Outbound{}
	for _, item := range legacy {
		if byName[item.name] == nil {
			byName[item.name] = &Outbound{Name: item.name}
		}
		if len(item.config) == 0 {
			continue
		}
		var o Outbound
		if err := json.Unmarshal(item.config, &o); err != nil {
			continue
		}
		o.Name = item.name
		byName[item.name] = &o
	}
	count := 0
	for _, o := range byName {
		if o.Name == "" {
			continue
		}
		if err := setOutboundStore(o); err != nil {
			return err
		}
		count++
	}
	if err := db.WriteTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM kv WHERE bucket = ?`, "outbounds"); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM kv WHERE bucket LIKE 'outbound.%'`)
		return err
	}); err != nil {
		return err
	}
	if count > 0 {
		log.Printf("已把 %d 个出站迁移到列式存储", count)
	}
	return nil
}
