package configure

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/ProxyStation/proxystation/db"
)

// 节点在 nodes 表里按字段列存储（见 db 包的建表语句），每个字段一列，可直接
// 建索引与过滤，不再把整行序列化成 JSON。数组语义（下标连续、删除后前移）与
// 旧的 list_items 完全一致，调用方无感知。

const (
	nodeBucket       = "touch"
	manualServersKey = "servers"
)

// nodeColumns 是节点表除 (bucket, key, idx) 外的字段列，顺序即读写的参数顺序。
var nodeColumns = []string{
	"link", "name", "host", "port", "type", "latency", "last_probe_time",
	"fast_latency", "last_fast_probe_time", "source", "front_proxy",
	"ip_type", "ip_type_addr", "ip_type_isp", "ip_type_checked_at", "ip_score",
	"ip_score_grade", "ip_score_risk", "ip_score_provider", "ip_score_at",
	"ip_geo_country", "ip_geo_country_code", "ip_geo_region", "ip_geo_city",
}

func nodeSelectColumns() string {
	return strings.Join(nodeColumns, ", ")
}

// nodeUpdateSet 形如 "link = ?, name = ?, ..."。
func nodeUpdateSet() string {
	parts := make([]string, len(nodeColumns))
	for i, c := range nodeColumns {
		parts[i] = c + " = ?"
	}
	return strings.Join(parts, ", ")
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func serverRowArgs(s *ServerRaw) []any {
	var score any
	if s.IPScore != nil {
		score = *s.IPScore
	}
	return []any{
		s.Link, s.Name, s.Host, s.Port, s.Type, s.Latency, s.LastProbeTime,
		s.FastLatency, s.LastFastProbeTime, s.Source, s.FrontProxy, s.IPType,
		s.IPTypeAddr, s.IPTypeISP, s.IPTypeCheckedAt, score, s.IPScoreGrade,
		s.IPScoreRisk, s.IPScoreProvider, s.IPScoreAt, s.IPGeoCountry,
		s.IPGeoCountryCode, s.IPGeoRegion, s.IPGeoCity,
	}
}

func scanServerRow(scan func(...any) error) (ServerRaw, error) {
	var s ServerRaw
	var score sql.NullInt64
	err := scan(
		&s.Link, &s.Name, &s.Host, &s.Port, &s.Type, &s.Latency, &s.LastProbeTime,
		&s.FastLatency, &s.LastFastProbeTime, &s.Source, &s.FrontProxy, &s.IPType,
		&s.IPTypeAddr, &s.IPTypeISP, &s.IPTypeCheckedAt, &score, &s.IPScoreGrade,
		&s.IPScoreRisk, &s.IPScoreProvider, &s.IPScoreAt, &s.IPGeoCountry,
		&s.IPGeoCountryCode, &s.IPGeoRegion, &s.IPGeoCity,
	)
	if err != nil {
		return s, err
	}
	if score.Valid {
		v := int(score.Int64)
		s.IPScore = &v
	}
	return s, nil
}

func dedupeInts(indexes []int) []int {
	seen := make(map[int]struct{}, len(indexes))
	out := make([]int, 0, len(indexes))
	for _, i := range indexes {
		if _, ok := seen[i]; ok {
			continue
		}
		seen[i] = struct{}{}
		out = append(out, i)
	}
	return out
}

// ---- 读 ----

func nodeGetAll(bucket, key string) []ServerRaw {
	rows, err := db.ReadDB().Query(`SELECT `+nodeSelectColumns()+` FROM nodes WHERE bucket = ? AND key = ? ORDER BY idx`, bucket, key)
	if err != nil {
		return []ServerRaw{}
	}
	defer rows.Close()
	out := make([]ServerRaw, 0)
	for rows.Next() {
		s, err := scanServerRow(rows.Scan)
		if err != nil {
			continue
		}
		out = append(out, s)
	}
	return out
}

func nodeGet(bucket, key string, index int) *ServerRaw {
	if index < 0 {
		return nil
	}
	row := db.ReadDB().QueryRow(`SELECT `+nodeSelectColumns()+` FROM nodes WHERE bucket = ? AND key = ? AND idx = ?`, bucket, key, index)
	s, err := scanServerRow(row.Scan)
	if err != nil {
		return nil
	}
	return &s
}

func nodeGetMany(bucket, key string, indexes []int) map[int]ServerRaw {
	result := make(map[int]ServerRaw, len(indexes))
	unique := dedupeInts(indexes)
	if len(unique) == 0 {
		return result
	}
	args := make([]any, 0, len(unique)+2)
	args = append(args, bucket, key)
	for _, i := range unique {
		args = append(args, i)
	}
	query := `SELECT idx, ` + nodeSelectColumns() + ` FROM nodes WHERE bucket = ? AND key = ? AND idx IN (` + placeholders(len(unique)) + `)`
	rows, err := db.ReadDB().Query(query, args...)
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var index int
		s, err := scanServerRow(func(dest ...any) error {
			return rows.Scan(append([]any{&index}, dest...)...)
		})
		if err != nil {
			continue
		}
		result[index] = s
	}
	return result
}

func nodeLen(bucket, key string) int {
	var n int
	if err := db.ReadDB().QueryRow(`SELECT COUNT(*) FROM nodes WHERE bucket = ? AND key = ?`, bucket, key).Scan(&n); err != nil {
		return 0
	}
	return n
}

func nodeGetRange(bucket, key string, offset, limit int) ([]ServerRaw, int, error) {
	total := nodeLen(bucket, key)
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	if limit <= 0 || offset+limit > total {
		limit = total - offset
	}
	if limit < 0 {
		limit = 0
	}
	rows, err := db.ReadDB().Query(`SELECT `+nodeSelectColumns()+` FROM nodes WHERE bucket = ? AND key = ? ORDER BY idx LIMIT ? OFFSET ?`,
		bucket, key, limit, offset)
	if err != nil {
		return nil, total, err
	}
	defer rows.Close()
	out := make([]ServerRaw, 0, limit)
	for rows.Next() {
		s, err := scanServerRow(rows.Scan)
		if err != nil {
			return nil, total, err
		}
		out = append(out, s)
	}
	return out, total, rows.Err()
}

// nodeHit 是搜索命中的一行（含下标）。
type nodeHit struct {
	Index  int
	Server ServerRaw
}

func nodeSearch(bucket, key string, f ServerFilter, limit, offset int) ([]nodeHit, int, error) {
	where, filterArgs := nodeSearchWhere(f)
	clause := ""
	args := []any{bucket, key}
	if where != "" {
		clause = " AND (" + where + ")"
		args = append(args, filterArgs...)
	}

	var total int
	if err := db.ReadDB().QueryRow(`SELECT COUNT(*) FROM nodes WHERE bucket = ? AND key = ?`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 100
	}
	selectArgs := append([]any{}, args...)
	selectArgs = append(selectArgs, limit, offset)
	rows, err := db.ReadDB().Query(`SELECT idx, `+nodeSelectColumns()+` FROM nodes WHERE bucket = ? AND key = ?`+clause+` ORDER BY idx LIMIT ? OFFSET ?`, selectArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	hits := make([]nodeHit, 0, limit)
	for rows.Next() {
		var index int
		s, err := scanServerRow(func(dest ...any) error {
			return rows.Scan(append([]any{&index}, dest...)...)
		})
		if err != nil {
			return nil, 0, err
		}
		hits = append(hits, nodeHit{Index: index, Server: s})
	}
	return hits, total, rows.Err()
}

// nodeSearchWhere 把筛选条件翻译成对 nodes 列的 SQL 表达式，语义与 MatchServerFilter 一致。
func nodeSearchWhere(f ServerFilter) (string, []any) {
	var clauses []string
	var args []any
	add := func(clause string, values ...any) {
		clauses = append(clauses, clause)
		args = append(args, values...)
	}
	if f.Query != "" {
		like := "%" + f.Query + "%"
		add(`(lower(name) LIKE ? OR lower(host) LIKE ? OR lower(type) LIKE ?)`, like, like, like)
	}
	if f.Type != "" {
		add(`lower(type) = ?`, f.Type)
	}
	if f.Geo != "" {
		add(`lower(ip_geo_country || ' ' || ip_geo_country_code || ' ' || ip_geo_region || ' ' || ip_geo_city) LIKE ?`, "%"+f.Geo+"%")
	}
	switch f.IPType {
	case "":
	case "unchecked":
		add(`ip_type = ''`)
	default:
		add(`lower(ip_type) = ?`, f.IPType)
	}
	switch f.Probe {
	case "unchecked":
		add(`last_probe_time <= 0`)
	case "reachable":
		add(`last_probe_time > 0 AND latency > 0`)
	case "failed":
		add(`last_probe_time > 0 AND latency <= 0`)
	}
	switch f.FastProbe {
	case "unchecked":
		add(`last_fast_probe_time <= 0`)
	case "reachable":
		add(`last_fast_probe_time > 0 AND fast_latency > 0`)
	case "failed":
		add(`last_fast_probe_time > 0 AND fast_latency <= 0`)
	}
	if f.MaxLatency != nil {
		add(`last_probe_time > 0 AND latency > 0 AND latency <= ?`, *f.MaxLatency)
	}
	if f.MinScore != nil {
		add(`ip_score IS NOT NULL AND ip_score >= ?`, *f.MinScore)
	}
	return strings.Join(clauses, " AND "), args
}

// ---- 写 ----

func nodeAppend(bucket, key string, nodes []ServerRaw) error {
	if len(nodes) == 0 {
		return nil
	}
	return db.WriteTx(func(tx *sql.Tx) error {
		var base int
		if err := tx.QueryRow(`SELECT COALESCE(MAX(idx), -1) FROM nodes WHERE bucket = ? AND key = ?`, bucket, key).Scan(&base); err != nil {
			return err
		}
		stmt, err := tx.Prepare(`INSERT INTO nodes(bucket, key, idx, ` + nodeSelectColumns() + `) VALUES(` + placeholders(3+len(nodeColumns)) + `)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for i := range nodes {
			args := append([]any{bucket, key, base + 1 + i}, serverRowArgs(&nodes[i])...)
			if _, err := stmt.Exec(args...); err != nil {
				return err
			}
		}
		return nil
	})
}

func nodeReplaceAll(bucket, key string, nodes []ServerRaw) error {
	return db.WriteTx(func(tx *sql.Tx) error {
		return nodeReplaceAllTx(tx, bucket, key, nodes)
	})
}

func nodeReplaceAllTx(tx *sql.Tx, bucket, key string, nodes []ServerRaw) error {
	if _, err := tx.Exec(`DELETE FROM nodes WHERE bucket = ? AND key = ?`, bucket, key); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO nodes(bucket, key, idx, ` + nodeSelectColumns() + `) VALUES(` + placeholders(3+len(nodeColumns)) + `)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for index := range nodes {
		args := append([]any{bucket, key, index}, serverRowArgs(&nodes[index])...)
		if _, err := stmt.Exec(args...); err != nil {
			return err
		}
	}
	return nil
}

func nodeDeleteAll(bucket, key string) error {
	return db.WriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM nodes WHERE bucket = ? AND key = ?`, bucket, key)
		return err
	})
}

func nodeSet(bucket, key string, index int, server *ServerRaw) error {
	return db.WriteTx(func(tx *sql.Tx) error {
		args := append(serverRowArgs(server), bucket, key, index)
		res, err := tx.Exec(`UPDATE nodes SET `+nodeUpdateSet()+` WHERE bucket = ? AND key = ? AND idx = ?`, args...)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return fmt.Errorf("index %d out of range", index)
		}
		return nil
	})
}

func nodeUpdate(bucket, key string, index int, update func(*ServerRaw)) error {
	return db.WriteTx(func(tx *sql.Tx) error {
		row := tx.QueryRow(`SELECT `+nodeSelectColumns()+` FROM nodes WHERE bucket = ? AND key = ? AND idx = ?`, bucket, key, index)
		server, err := scanServerRow(row.Scan)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("index %d out of range", index)
			}
			return err
		}
		update(&server)
		args := append(serverRowArgs(&server), bucket, key, index)
		_, err = tx.Exec(`UPDATE nodes SET `+nodeUpdateSet()+` WHERE bucket = ? AND key = ? AND idx = ?`, args...)
		return err
	})
}

func nodeUpdateMany(bucket, key string, updates map[int]func(*ServerRaw)) error {
	if len(updates) == 0 {
		return nil
	}
	return db.WriteTx(func(tx *sql.Tx) error {
		for index, update := range updates {
			row := tx.QueryRow(`SELECT `+nodeSelectColumns()+` FROM nodes WHERE bucket = ? AND key = ? AND idx = ?`, bucket, key, index)
			server, err := scanServerRow(row.Scan)
			if err != nil {
				if err == sql.ErrNoRows {
					return fmt.Errorf("index %d out of range", index)
				}
				return err
			}
			update(&server)
			args := append(serverRowArgs(&server), bucket, key, index)
			if _, err := tx.Exec(`UPDATE nodes SET `+nodeUpdateSet()+` WHERE bucket = ? AND key = ? AND idx = ?`, args...); err != nil {
				return err
			}
		}
		return nil
	})
}

func nodeRemove(bucket, key string, indexes []int) error {
	remove := dedupeInts(indexes)
	if len(remove) == 0 {
		return nil
	}
	sort.Sort(sort.Reverse(sort.IntSlice(remove)))
	return db.WriteTx(func(tx *sql.Tx) error {
		for _, index := range remove {
			res, err := tx.Exec(`DELETE FROM nodes WHERE bucket = ? AND key = ? AND idx = ?`, bucket, key, index)
			if err != nil {
				return err
			}
			affected, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if affected == 0 {
				continue
			}
			if _, err := tx.Exec(`UPDATE nodes SET idx = idx - 1 WHERE bucket = ? AND key = ? AND idx > ?`, bucket, key, index); err != nil {
				return err
			}
		}
		return nil
	})
}

// MigrateNodeStorage 把节点从旧的整行 JSON 布局（list_items）迁移到列式 nodes 表。
// 按 key 幂等：只迁移 nodes 表里还没有该 key 数据的旧行；已有数据的 key 视为已迁移，
// 直接清掉遗留的 JSON 行。整个过程在单个写事务内完成，失败会整体回滚。
func MigrateNodeStorage() error {
	return db.WriteTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT DISTINCT key FROM list_items WHERE bucket = ? AND (key = ? OR key LIKE ?)`,
			nodeBucket, manualServersKey, subscriptionServerKeyPrefix+"%")
		if err != nil {
			return err
		}
		keys := make([]string, 0)
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return err
			}
			keys = append(keys, key)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(keys) == 0 {
			return nil
		}

		insertStmt, err := tx.Prepare(`INSERT INTO nodes(bucket, key, idx, ` + nodeSelectColumns() + `) VALUES(` + placeholders(3+len(nodeColumns)) + `)`)
		if err != nil {
			return err
		}
		defer insertStmt.Close()

		migrated := 0
		for _, key := range keys {
			var existing int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM nodes WHERE bucket = ? AND key = ?`, nodeBucket, key).Scan(&existing); err != nil {
				return err
			}
			if existing == 0 {
				legacy, err := tx.Query(`SELECT idx, value FROM list_items WHERE bucket = ? AND key = ? ORDER BY idx`, nodeBucket, key)
				if err != nil {
					return err
				}
				for legacy.Next() {
					var index int
					var raw []byte
					if err := legacy.Scan(&index, &raw); err != nil {
						legacy.Close()
						return err
					}
					var server ServerRaw
					if err := json.Unmarshal(raw, &server); err != nil {
						continue
					}
					args := append([]any{nodeBucket, key, index}, serverRowArgs(&server)...)
					if _, err := insertStmt.Exec(args...); err != nil {
						legacy.Close()
						return err
					}
					migrated++
				}
				legacy.Close()
				if err := legacy.Err(); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(`DELETE FROM list_items WHERE bucket = ? AND key = ?`, nodeBucket, key); err != nil {
				return err
			}
		}
		if migrated > 0 {
			log.Printf("已把 %d 个节点从 JSON 行迁移到列式存储", migrated)
		}
		return nil
	})
}
