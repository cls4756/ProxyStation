// Package db 提供应用配置的持久化访问。
//
// 早期版本直接使用 bbolt，把列表（节点、订阅、分组……）序列化成单个 JSON 数组
// 存在一个 key 下，因此任何单条修改都要解析并整体重写整个数组。这在节点数量增长
// 后成为瓶颈。
//
// 现在底层换成 SQLite：标量仍然按 (bucket, key) 存一行；列表被拆成 list_items
// 表，每个元素一行并带 idx。这样单条增删改只影响对应行，不再全量重写。
// 对外函数签名与语义保持与 bbolt 版本一致，调用方（db/configure）无需改动。
package db

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// database 承担所有写操作（以及可能的读改写），连接数限制为 1，保持与 bbolt 等价的
// 单写者语义。readDatabase 是独立的只读连接池，借助 WAL 与写并发，避免读取被后台
// 探测/同步等写事务阻塞。
var (
	database     *sql.DB
	readDatabase *sql.DB
)

// reader 返回用于查询的句柄：优先使用只读连接池。所有纯读函数都通过它访问数据库，
// 写事务（withTx）仍走 database，二者互不阻塞。
func reader() *sql.DB {
	if readDatabase != nil {
		return readDatabase
	}
	return database
}

// ReadDB 返回只读连接池，供按专用表结构组织的查询（如节点表）使用。
func ReadDB() *sql.DB { return reader() }

// WriteTx 在唯一的写连接上执行事务。写事务之间串行，读走独立连接池互不阻塞。
func WriteTx(fn func(*sql.Tx) error) error { return withTx(fn) }

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS kv (
		bucket TEXT NOT NULL,
		key    TEXT NOT NULL,
		value  BLOB NOT NULL,
		PRIMARY KEY (bucket, key)
	)`,
	`CREATE TABLE IF NOT EXISTS list_items (
		bucket TEXT NOT NULL,
		key    TEXT NOT NULL,
		idx    INTEGER NOT NULL,
		value  BLOB NOT NULL,
		PRIMARY KEY (bucket, key, idx)
	)`,
	// nodes：节点（手动节点与订阅节点）按字段列存储，字段可直接建索引与过滤，
	// 不再把整行序列化成 JSON。数组语义（下标连续、删除后前移）与 list_items 一致。
	`CREATE TABLE IF NOT EXISTS nodes (
		bucket TEXT NOT NULL,
		key    TEXT NOT NULL,
		idx    INTEGER NOT NULL,
		link TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL DEFAULT '',
		host TEXT NOT NULL DEFAULT '',
		port INTEGER NOT NULL DEFAULT 0,
		type TEXT NOT NULL DEFAULT '',
		latency INTEGER NOT NULL DEFAULT 0,
		last_probe_time INTEGER NOT NULL DEFAULT 0,
		fast_latency INTEGER NOT NULL DEFAULT 0,
		last_fast_probe_time INTEGER NOT NULL DEFAULT 0,
		source TEXT NOT NULL DEFAULT '',
		front_proxy TEXT NOT NULL DEFAULT '',
		ip_type TEXT NOT NULL DEFAULT '',
		ip_type_addr TEXT NOT NULL DEFAULT '',
		ip_type_isp TEXT NOT NULL DEFAULT '',
		ip_type_checked_at INTEGER NOT NULL DEFAULT 0,
		ip_score INTEGER,
		ip_score_grade TEXT NOT NULL DEFAULT '',
		ip_score_risk TEXT NOT NULL DEFAULT '',
		ip_score_provider TEXT NOT NULL DEFAULT '',
		ip_score_at INTEGER NOT NULL DEFAULT 0,
		ip_geo_country TEXT NOT NULL DEFAULT '',
		ip_geo_country_code TEXT NOT NULL DEFAULT '',
		ip_geo_region TEXT NOT NULL DEFAULT '',
		ip_geo_city TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (bucket, key, idx)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_nodes_type ON nodes(bucket, key, type)`,
	`CREATE INDEX IF NOT EXISTS idx_nodes_ip_type ON nodes(bucket, key, ip_type)`,
	`CREATE INDEX IF NOT EXISTS idx_nodes_source ON nodes(bucket, key, source)`,
	// subscriptions：订阅自身的元数据按字段列存储。订阅节点按行存在 nodes 表里，
	// 由独立 key 关联。数组语义（idx 连续）与 list_items 一致。
	`CREATE TABLE IF NOT EXISTS subscriptions (
		idx INTEGER PRIMARY KEY,
		id TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL DEFAULT '',
		url TEXT NOT NULL DEFAULT '',
		format TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL DEFAULT 0,
		group_id TEXT NOT NULL DEFAULT '',
		proxy_outbound TEXT NOT NULL DEFAULT ''
	)`,
	// groups：分组元数据按列存储；分组引用的节点列表可能上万条，拆到 group_servers
	// 子表按 (group_id, ref_idx) 存，增删单个引用不再重写整份分组。
	`CREATE TABLE IF NOT EXISTS groups (
		idx INTEGER PRIMARY KEY,
		id TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL DEFAULT '',
		from_sub INTEGER NOT NULL DEFAULT 0,
		sub_id TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS group_servers (
		group_id TEXT NOT NULL,
		ref_idx INTEGER NOT NULL,
		type TEXT NOT NULL DEFAULT '',
		ref_index INTEGER NOT NULL DEFAULT 0,
		sub INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (group_id, ref_idx)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_group_servers_ref ON group_servers(type, ref_index, sub)`,
	// routing_rules：路由规则标量字段按列存储；域名/IP/入站标签是可能很长的匹配列表，
	// 拆到子表，rule 改一条不再重写整个 JSON 数组。
	`CREATE TABLE IF NOT EXISTS routing_rules (
		idx INTEGER PRIMARY KEY,
		id TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 0,
		name TEXT NOT NULL DEFAULT '',
		ports TEXT NOT NULL DEFAULT '',
		protocol TEXT NOT NULL DEFAULT '',
		action TEXT NOT NULL DEFAULT '',
		outbound_name TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE IF NOT EXISTS routing_rule_inbound_tags (
		rule_idx INTEGER NOT NULL,
		item_idx INTEGER NOT NULL,
		value TEXT NOT NULL,
		PRIMARY KEY (rule_idx, item_idx)
	)`,
	`CREATE TABLE IF NOT EXISTS routing_rule_domains (
		rule_idx INTEGER NOT NULL,
		item_idx INTEGER NOT NULL,
		value TEXT NOT NULL,
		PRIMARY KEY (rule_idx, item_idx)
	)`,
	`CREATE TABLE IF NOT EXISTS routing_rule_ips (
		rule_idx INTEGER NOT NULL,
		item_idx INTEGER NOT NULL,
		value TEXT NOT NULL,
		PRIMARY KEY (rule_idx, item_idx)
	)`,
	// custom_inbounds：入站顶层字段按列存储；协议专属的嵌套选项（vmess/cfdo/
	// cfgoodnet/accounts/sniff_dest）与父行整体读写、从不单独过滤，保持 JSON 列。
	`CREATE TABLE IF NOT EXISTS custom_inbounds (
		idx INTEGER PRIMARY KEY,
		id TEXT NOT NULL DEFAULT '',
		tag TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL DEFAULT '',
		protocol TEXT NOT NULL DEFAULT '',
		listen TEXT NOT NULL DEFAULT '',
		port INTEGER NOT NULL DEFAULT 0,
		udp_enabled INTEGER NOT NULL DEFAULT 0,
		network TEXT NOT NULL DEFAULT '',
		follow_redirect INTEGER NOT NULL DEFAULT 0,
		sniff_enabled INTEGER NOT NULL DEFAULT 0,
		sniff_dest TEXT NOT NULL DEFAULT '[]',
		username TEXT NOT NULL DEFAULT '',
		password TEXT NOT NULL DEFAULT '',
		accounts TEXT NOT NULL DEFAULT '[]',
		vmess TEXT,
		cfdo TEXT,
		cfgoodnet TEXT
	)`,
	// inbound_subscriptions：发布入站订阅的元数据按列存储；inbound_ids 拆子表，
	// 规则列表（含嵌套 domains/ips）整体读写，保持 JSON 列。
	`CREATE TABLE IF NOT EXISTS inbound_subscriptions (
		idx INTEGER PRIMARY KEY,
		id TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL DEFAULT '',
		token TEXT NOT NULL DEFAULT '',
		host TEXT NOT NULL DEFAULT '',
		format TEXT NOT NULL DEFAULT '',
		rules TEXT NOT NULL DEFAULT '[]',
		created_at INTEGER NOT NULL DEFAULT 0,
		updated_at INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS inbound_subscription_inbounds (
		sub_idx INTEGER NOT NULL,
		item_idx INTEGER NOT NULL,
		value TEXT NOT NULL,
		PRIMARY KEY (sub_idx, item_idx)
	)`,
	// outbounds：出站按名字为主键（不再按 outbound.<name> 桶 + 存在标记两处存）。
	// 出站目标里的协议专属选项整体读写，保持 JSON 列。
	`CREATE TABLE IF NOT EXISTS outbounds (
		name TEXT PRIMARY KEY,
		target_type TEXT NOT NULL DEFAULT '',
		node_ref TEXT,
		group_id TEXT NOT NULL DEFAULT '',
		mode TEXT NOT NULL DEFAULT '',
		active_node_ref TEXT,
		special_type TEXT NOT NULL DEFAULT '',
		special_cfgoodnet TEXT,
		special_cfdo TEXT,
		probe_url TEXT NOT NULL DEFAULT '',
		probe_interval TEXT NOT NULL DEFAULT '',
		probe_mode TEXT NOT NULL DEFAULT '',
		switch_threshold_ms INTEGER,
		switch_cooldown_sec INTEGER
	)`,
	// setting：单行配置表，标量字段按列存储；节点清理/复制规则与认证账号列表
	// 与整行配置一起读写，保持 JSON 列。
	`CREATE TABLE IF NOT EXISTS setting (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		log_level TEXT NOT NULL DEFAULT '',
		kernel_mode TEXT NOT NULL DEFAULT '',
		enable_sniff INTEGER NOT NULL DEFAULT 0,
		socks5_sniff_enabled INTEGER,
		http_sniff_enabled INTEGER,
		enable_hijack_dns INTEGER NOT NULL DEFAULT 0,
		dns_mode TEXT NOT NULL DEFAULT '',
		dns_remote_server TEXT NOT NULL DEFAULT '',
		subscription_auto_update_mode TEXT NOT NULL DEFAULT '',
		subscription_auto_update_interval_hour INTEGER NOT NULL DEFAULT 0,
		transparent_mode TEXT NOT NULL DEFAULT '',
		lan_sharing_enabled INTEGER NOT NULL DEFAULT 0,
		socks5_port INTEGER NOT NULL DEFAULT 0,
		http_port INTEGER NOT NULL DEFAULT 0,
		max_log_lines INTEGER NOT NULL DEFAULT 0,
		max_log_file_size INTEGER NOT NULL DEFAULT 0,
		download_proxy TEXT NOT NULL DEFAULT '',
		github_mirror TEXT NOT NULL DEFAULT '',
		probe_targets TEXT NOT NULL DEFAULT '',
		group_real_probe_interval_sec INTEGER NOT NULL DEFAULT 0,
		group_probe_mode TEXT NOT NULL DEFAULT '',
		group_probe_concurrency INTEGER NOT NULL DEFAULT 0,
		group_switch_threshold_ms INTEGER NOT NULL DEFAULT 0,
		group_switch_cooldown_sec INTEGER NOT NULL DEFAULT 0,
		group_active_node_probe_enabled INTEGER,
		group_active_node_probe_interval_sec INTEGER NOT NULL DEFAULT 0,
		node_cleanup_rules TEXT NOT NULL DEFAULT '[]',
		node_cleanup_mode TEXT NOT NULL DEFAULT '',
		node_cleanup_interval_hour INTEGER NOT NULL DEFAULT 0,
		node_cleanup_group_ids TEXT NOT NULL DEFAULT '[]',
		node_cleanup_recycle_group_id TEXT NOT NULL DEFAULT '',
		subscription_best_node_copy_rules TEXT NOT NULL DEFAULT '[]',
		ip_score_enabled INTEGER NOT NULL DEFAULT 0,
		ip_score_provider TEXT NOT NULL DEFAULT '',
		ip_score_endpoint TEXT NOT NULL DEFAULT '',
		ip_score_min_interval_ms INTEGER NOT NULL DEFAULT 0,
		socks5_username TEXT NOT NULL DEFAULT '',
		socks5_password TEXT NOT NULL DEFAULT '',
		http_username TEXT NOT NULL DEFAULT '',
		http_password TEXT NOT NULL DEFAULT '',
		socks5_accounts TEXT NOT NULL DEFAULT '[]',
		http_accounts TEXT NOT NULL DEFAULT '[]',
		web_username TEXT NOT NULL DEFAULT '',
		web_password TEXT NOT NULL DEFAULT '',
		shared_nodes_fixed INTEGER NOT NULL DEFAULT 0
	)`,
	// sessions：把会话从 kv 的 JSON 值改成按列存储，便于直接按最后访问时间清理。
	`CREATE TABLE IF NOT EXISTS sessions (
		session_id TEXT PRIMARY KEY,
		username TEXT NOT NULL DEFAULT '',
		expires_at INTEGER NOT NULL DEFAULT 0,
		last_access INTEGER NOT NULL DEFAULT 0
	)`,
}

// Init 打开（必要时先从旧版 bbolt 文件迁移）数据库。path 是数据库文件路径。
func Init(path string) error {
	if err := prepareStorage(path); err != nil {
		return err
	}

	// WAL 允许读与写并发；busy_timeout 避免瞬时锁冲突直接报错；immediate 让写事务
	// 一开始就取写锁，配合下面限制的单连接，完全避免 sql.Tx 的锁升级死锁。
	dsn := "file:" + path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	handle, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	// 所有访问串行化：语义上等价于 bbolt 的单写者模型，且回调式更新
	// （ListUpdate/ListUpdateMany）绝不会因为回调里再访问 db 而自锁。
	handle.SetMaxOpenConns(1)
	handle.SetMaxIdleConns(1)
	if err := handle.Ping(); err != nil {
		_ = handle.Close()
		return err
	}
	for _, stmt := range schemaStatements {
		if _, err := handle.Exec(stmt); err != nil {
			_ = handle.Close()
			return fmt.Errorf("init schema: %w", err)
		}
	}
	database = handle

	// 只读连接池：WAL 下与写并发，读请求不会排在写事务后面。
	readHandle, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = handle.Close()
		database = nil
		return err
	}
	readHandle.SetMaxOpenConns(6)
	readHandle.SetMaxIdleConns(6)
	if err := readHandle.Ping(); err != nil {
		_ = readHandle.Close()
		_ = handle.Close()
		database = nil
		return err
	}
	readDatabase = readHandle
	return nil
}

func Close() error {
	if readDatabase != nil {
		_ = readDatabase.Close()
		readDatabase = nil
	}
	if database == nil {
		return nil
	}
	err := database.Close()
	database = nil
	return err
}

func withTx(fn func(*sql.Tx) error) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func isJSONArray(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	return len(trimmed) > 0 && trimmed[0] == '['
}

// Set 写入一个标量值。value 为 nil 表示删除。
// 与 bbolt 版本一致：如果写入的是一个 JSON 数组（调用方用 Set 保存列表，例如
// SetServers），则自动按列表元素分行存储，保证之后 ListGetAll 能读到。
func Set(bucket, key string, value interface{}) error {
	if value == nil {
		return writeScalar(bucket, key, nil)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeValue(bucket, key, data)
}

// writeValue 统一处理标量与数组两种写入。
func writeValue(bucket, key string, data []byte) error {
	if isJSONArray(data) {
		var items []json.RawMessage
		if err := json.Unmarshal(data, &items); err == nil {
			return replaceList(bucket, key, items)
		}
	}
	return writeScalar(bucket, key, data)
}

// writeScalar 写入/删除标量，并清掉同名列表，避免两种表示同时存在。
func writeScalar(bucket, key string, data []byte) error {
	return withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM list_items WHERE bucket = ? AND key = ?`, bucket, key); err != nil {
			return err
		}
		if data == nil {
			_, err := tx.Exec(`DELETE FROM kv WHERE bucket = ? AND key = ?`, bucket, key)
			return err
		}
		_, err := tx.Exec(`INSERT INTO kv(bucket, key, value) VALUES(?, ?, ?)
			ON CONFLICT(bucket, key) DO UPDATE SET value = excluded.value`, bucket, key, data)
		return err
	})
}

// replaceList 用给定元素整体替换一个列表，并清掉同名标量。
func replaceList(bucket, key string, items []json.RawMessage) error {
	return withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM kv WHERE bucket = ? AND key = ?`, bucket, key); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM list_items WHERE bucket = ? AND key = ?`, bucket, key); err != nil {
			return err
		}
		for i, item := range items {
			if _, err := tx.Exec(`INSERT INTO list_items(bucket, key, idx, value) VALUES(?, ?, ?, ?)`,
				bucket, key, i, []byte(item)); err != nil {
				return err
			}
		}
		return nil
	})
}

// readValue 读取一个键的原始 JSON。标量优先；没有标量时把列表重新拼成数组。
// 空列表会返回 not found，与“键不存在”在语义上等价（调用方的空值处理一致）。
func readValue(bucket, key string) ([]byte, error) {
	var data []byte
	err := reader().QueryRow(`SELECT value FROM kv WHERE bucket = ? AND key = ?`, bucket, key).Scan(&data)
	if err == nil {
		return cloneBytes(data), nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	rows, err := reader().Query(`SELECT value FROM list_items WHERE bucket = ? AND key = ? ORDER BY idx`, bucket, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var buf bytes.Buffer
	buf.WriteByte('[')
	count := 0
	for rows.Next() {
		var item []byte
		if err := rows.Scan(&item); err != nil {
			return nil, err
		}
		if count > 0 {
			buf.WriteByte(',')
		}
		buf.Write(item)
		count++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, fmt.Errorf("key %s not found", key)
	}
	buf.WriteByte(']')
	return buf.Bytes(), nil
}

func Get(bucket, key string, value interface{}) error {
	data, err := readValue(bucket, key)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func GetRaw(bucket, key string) ([]byte, error) {
	return readValue(bucket, key)
}

func Exists(bucket, key string) bool {
	var one int
	err := reader().QueryRow(`SELECT 1 FROM kv WHERE bucket = ? AND key = ?
		UNION ALL SELECT 1 FROM list_items WHERE bucket = ? AND key = ? LIMIT 1`,
		bucket, key, bucket, key).Scan(&one)
	return err == nil
}

func BucketClear(bucket string) error {
	return withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM kv WHERE bucket = ?`, bucket); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM list_items WHERE bucket = ?`, bucket)
		return err
	})
}

// ListAppend 在列表末尾追加一个或多个元素。items 可以是切片，也可以是单个值。
func ListAppend(bucket, key string, items interface{}) error {
	data, err := json.Marshal(items)
	if err != nil {
		return err
	}
	rawItems, err := toRawMessages(data)
	if err != nil {
		return err
	}
	return withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM kv WHERE bucket = ? AND key = ?`, bucket, key); err != nil {
			return err
		}
		var base int
		if err := tx.QueryRow(`SELECT COALESCE(MAX(idx), -1) FROM list_items WHERE bucket = ? AND key = ?`,
			bucket, key).Scan(&base); err != nil {
			return err
		}
		for i, item := range rawItems {
			if _, err := tx.Exec(`INSERT INTO list_items(bucket, key, idx, value) VALUES(?, ?, ?, ?)`,
				bucket, key, base+1+i, []byte(item)); err != nil {
				return err
			}
		}
		return nil
	})
}

func ListGetAll(bucket, key string) ([][]byte, error) {
	rows, err := reader().Query(`SELECT value FROM list_items WHERE bucket = ? AND key = ? ORDER BY idx`, bucket, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([][]byte, 0)
	for rows.Next() {
		var item []byte
		if err := rows.Scan(&item); err != nil {
			return nil, err
		}
		result = append(result, cloneBytes(item))
	}
	return result, rows.Err()
}

func ListGet(bucket, key string, index int) ([]byte, error) {
	if index < 0 {
		return nil, fmt.Errorf("index %d out of range", index)
	}
	var item []byte
	err := reader().QueryRow(`SELECT value FROM list_items WHERE bucket = ? AND key = ? AND idx = ?`,
		bucket, key, index).Scan(&item)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("index %d out of range", index)
	}
	if err != nil {
		return nil, err
	}
	return cloneBytes(item), nil
}

func ListGetMany(bucket, key string, indexes []int) (map[int][]byte, error) {
	result := make(map[int][]byte, len(indexes))
	if len(indexes) == 0 {
		return result, nil
	}
	wanted := dedupeIndexes(indexes)
	if len(wanted) == 0 {
		return result, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(wanted)), ",")
	args := make([]interface{}, 0, len(wanted)+2)
	args = append(args, bucket, key)
	for _, index := range wanted {
		args = append(args, index)
	}
	rows, err := reader().Query(`SELECT idx, value FROM list_items WHERE bucket = ? AND key = ? AND idx IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var index int
		var item []byte
		if err := rows.Scan(&index, &item); err != nil {
			return nil, err
		}
		result[index] = cloneBytes(item)
	}
	return result, rows.Err()
}

func ListSet(bucket, key string, index int, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return withTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE list_items SET value = ? WHERE bucket = ? AND key = ? AND idx = ?`,
			data, bucket, key, index)
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

// ListUpdate 在同一个写事务里读取并更新一条元素，避免读旧快照后整体覆盖。
func ListUpdate(bucket, key string, index int, update func([]byte) ([]byte, error)) error {
	return withTx(func(tx *sql.Tx) error {
		current, err := listItemTx(tx, bucket, key, index)
		if err != nil {
			return err
		}
		updated, err := update(current)
		if err != nil {
			return err
		}
		if !json.Valid(updated) {
			return fmt.Errorf("updated item is not valid JSON")
		}
		_, err = tx.Exec(`UPDATE list_items SET value = ? WHERE bucket = ? AND key = ? AND idx = ?`,
			updated, bucket, key, index)
		return err
	})
}

// ListUpdateMany 在一个事务里批量更新多条元素，避免重复解析同一份大列表。
func ListUpdateMany(bucket, key string, updates map[int]func([]byte) ([]byte, error)) error {
	if len(updates) == 0 {
		return nil
	}
	return withTx(func(tx *sql.Tx) error {
		for index, update := range updates {
			current, err := listItemTx(tx, bucket, key, index)
			if err != nil {
				return err
			}
			updated, err := update(current)
			if err != nil {
				return err
			}
			if !json.Valid(updated) {
				return fmt.Errorf("updated item at index %d is not valid JSON", index)
			}
			if _, err := tx.Exec(`UPDATE list_items SET value = ? WHERE bucket = ? AND key = ? AND idx = ?`,
				updated, bucket, key, index); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListRemove 删除若干下标，并把后面的元素整体前移，保持下标连续。
// 越界的下标按原实现语义直接忽略。
func ListRemove(bucket, key string, indexes []int) error {
	remove := dedupeIndexes(indexes)
	if len(remove) == 0 {
		return nil
	}
	// 从大到小处理，前移只影响更大的下标，互不干扰。
	for i := 0; i < len(remove)-1; i++ {
		for j := i + 1; j < len(remove); j++ {
			if remove[j] > remove[i] {
				remove[i], remove[j] = remove[j], remove[i]
			}
		}
	}
	return withTx(func(tx *sql.Tx) error {
		for _, index := range remove {
			res, err := tx.Exec(`DELETE FROM list_items WHERE bucket = ? AND key = ? AND idx = ?`, bucket, key, index)
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
			if _, err := tx.Exec(`UPDATE list_items SET idx = idx - 1 WHERE bucket = ? AND key = ? AND idx > ?`,
				bucket, key, index); err != nil {
				return err
			}
		}
		return nil
	})
}

func ListLen(bucket, key string) (int, error) {
	var count int
	err := reader().QueryRow(`SELECT COUNT(*) FROM list_items WHERE bucket = ? AND key = ?`, bucket, key).Scan(&count)
	return count, err
}

// ListGetRange 只读取列表中 [offset, offset+limit) 区间的元素，按 idx 升序。
// limit <= 0 表示不限制数量。区间总量请用 ListLen 单独获取。
func ListGetRange(bucket, key string, offset, limit int) ([][]byte, error) {
	if offset < 0 {
		offset = 0
	}
	query := `SELECT value FROM list_items WHERE bucket = ? AND key = ? ORDER BY idx`
	args := []interface{}{bucket, key}
	switch {
	case limit > 0:
		query += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	case offset > 0:
		query += ` LIMIT -1 OFFSET ?`
		args = append(args, offset)
	}
	rows, err := reader().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([][]byte, 0)
	for rows.Next() {
		var item []byte
		if err := rows.Scan(&item); err != nil {
			return nil, err
		}
		result = append(result, cloneBytes(item))
	}
	return result, rows.Err()
}

// ListEntry 是列表里的一条记录及其下标。
type ListEntry struct {
	Index int
	Value []byte
}

// ListQuery 描述在数据库端对列表元素的筛选与分页。
type ListQuery struct {
	// Where 是基于 value 列的布尔表达式（通常是 json_extract 调用），空表示不过滤。
	Where string
	// Args 是 Where 里的占位符参数。
	Args []interface{}
	// Offset/Limit 用于分页；Limit <= 0 表示不限制。
	Offset int
	Limit  int
}

// QueryList 在数据库端筛选列表元素，返回按 idx 升序的匹配项与满足条件的独立总数。
// 总数不受 Limit/Offset 影响，便于调用方区分“匹配总数”和“本页条数”。
func QueryList(bucket, key string, q ListQuery) ([]ListEntry, int, error) {
	clause := ""
	args := []interface{}{bucket, key}
	if strings.TrimSpace(q.Where) != "" {
		clause = " AND (" + q.Where + ")"
		args = append(args, q.Args...)
	}

	var total int
	if err := reader().QueryRow(`SELECT COUNT(*) FROM list_items WHERE bucket = ? AND key = ?`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := q.Offset
	if offset < 0 {
		offset = 0
	}
	query := `SELECT idx, value FROM list_items WHERE bucket = ? AND key = ?` + clause + ` ORDER BY idx`
	selectArgs := append([]interface{}{}, args...)
	switch {
	case q.Limit > 0:
		query += ` LIMIT ? OFFSET ?`
		selectArgs = append(selectArgs, q.Limit, offset)
	case offset > 0:
		query += ` LIMIT -1 OFFSET ?`
		selectArgs = append(selectArgs, offset)
	}

	rows, err := reader().Query(query, selectArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := make([]ListEntry, 0)
	for rows.Next() {
		var entry ListEntry
		if err := rows.Scan(&entry.Index, &entry.Value); err != nil {
			return nil, 0, err
		}
		entry.Value = cloneBytes(entry.Value)
		result = append(result, entry)
	}
	return result, total, rows.Err()
}

func GetBucketLen(bucket string) (int, error) {
	var count int
	err := reader().QueryRow(`SELECT COUNT(*) FROM (
		SELECT key FROM kv WHERE bucket = ?
		UNION SELECT key FROM list_items WHERE bucket = ?
	)`, bucket, bucket).Scan(&count)
	return count, err
}

func GetBucketKeys(bucket string) ([]string, error) {
	rows, err := reader().Query(`SELECT key FROM kv WHERE bucket = ?
		UNION SELECT key FROM list_items WHERE bucket = ?
		ORDER BY key`, bucket, bucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func listItemTx(tx *sql.Tx, bucket, key string, index int) ([]byte, error) {
	var item []byte
	err := tx.QueryRow(`SELECT value FROM list_items WHERE bucket = ? AND key = ? AND idx = ?`,
		bucket, key, index).Scan(&item)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("index %d out of range", index)
	}
	if err != nil {
		return nil, err
	}
	return cloneBytes(item), nil
}

func toRawMessages(data []byte) ([]json.RawMessage, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err == nil && items != nil {
		return items, nil
	}
	if isJSONArray(data) {
		return nil, fmt.Errorf("invalid JSON array: %s", string(data))
	}
	return []json.RawMessage{data}, nil
}

func dedupeIndexes(indexes []int) []int {
	seen := make(map[int]struct{}, len(indexes))
	result := make([]int, 0, len(indexes))
	for _, index := range indexes {
		if index < 0 {
			continue
		}
		if _, ok := seen[index]; ok {
			continue
		}
		seen[index] = struct{}{}
		result = append(result, index)
	}
	return result
}

func cloneBytes(data []byte) []byte {
	if data == nil {
		return nil
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	return cp
}
