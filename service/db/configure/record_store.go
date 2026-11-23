package configure

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ProxyStation/proxystation/db"
)

// timeToInt/intToTime 在 SQLite INTEGER 列与 time.Time 之间做无损转换。
// 零值单独还原，避免 time.Unix 把 time.Time{} 变成 1970 或本地时区。
func timeToInt(t time.Time) int64 { return t.UnixNano() }

func intToTime(n int64) time.Time {
	if n == (time.Time{}).UnixNano() {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

// idxRecordDAO 是「以 idx 为主键的有序记录表」的通用访问层，语义与旧 list_items
// 完全一致：追加写到末尾，删除指定下标后把后面的下标整体前移，读取按下标升序。
//
// 各实体的专属 store 文件只需声明表名、列名，以及「结构体 <-> 列参数」和
// 「扫描一行 -> 结构体」两个小函数，即可复用这里的增删改查与下标压缩逻辑，
// 避免把整份集合序列化成单个 JSON 再整体重写。
type idxRecordDAO[T any] struct {
	table   string
	columns []string
	args    func(T) []any
	scan    func(scan func(...any) error) (T, error)
}

func (d idxRecordDAO[T]) columnList() string {
	return strings.Join(d.columns, ", ")
}

func (d idxRecordDAO[T]) updateSet() string {
	parts := make([]string, len(d.columns))
	for i, c := range d.columns {
		parts[i] = c + " = ?"
	}
	return strings.Join(parts, ", ")
}

// getAll 按 idx 升序返回全部记录。返回切片的第 i 项即下标 i 对应的记录。
func (d idxRecordDAO[T]) getAll() ([]T, error) {
	rows, err := db.ReadDB().Query(`SELECT ` + d.columnList() + ` FROM ` + d.table + ` ORDER BY idx`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]T, 0)
	for rows.Next() {
		value, err := d.scan(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (d idxRecordDAO[T]) get(index int) (*T, error) {
	if index < 0 {
		return nil, fmt.Errorf("index %d out of range", index)
	}
	row := db.ReadDB().QueryRow(`SELECT `+d.columnList()+` FROM `+d.table+` WHERE idx = ?`, index)
	value, err := d.scan(row.Scan)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("index %d out of range", index)
	}
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func (d idxRecordDAO[T]) getMany(indexes []int) (map[int]T, error) {
	result := make(map[int]T, len(indexes))
	wanted := dedupeInts(indexes)
	if len(wanted) == 0 {
		return result, nil
	}
	args := make([]any, 0, len(wanted))
	for _, index := range wanted {
		args = append(args, index)
	}
	rows, err := db.ReadDB().Query(`SELECT idx, `+d.columnList()+` FROM `+d.table+` WHERE idx IN (`+placeholders(len(wanted))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var index int
		value, err := d.scan(func(dest ...any) error {
			return rows.Scan(append([]any{&index}, dest...)...)
		})
		if err != nil {
			return nil, err
		}
		result[index] = value
	}
	return result, rows.Err()
}

func (d idxRecordDAO[T]) count() int {
	var n int
	if err := db.ReadDB().QueryRow(`SELECT COUNT(*) FROM ` + d.table).Scan(&n); err != nil {
		return 0
	}
	return n
}

func (d idxRecordDAO[T]) append(records []T) error {
	if len(records) == 0 {
		return nil
	}
	return db.WriteTx(func(tx *sql.Tx) error {
		return d.appendTx(tx, records)
	})
}

func (d idxRecordDAO[T]) appendTx(tx *sql.Tx, records []T) error {
	var base int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(idx), -1) FROM ` + d.table).Scan(&base); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO ` + d.table + `(idx, ` + d.columnList() + `) VALUES(` + placeholders(1+len(d.columns)) + `)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i := range records {
		args := append([]any{base + 1 + i}, d.args(records[i])...)
		if _, err := stmt.Exec(args...); err != nil {
			return err
		}
	}
	return nil
}

func (d idxRecordDAO[T]) set(index int, record T) error {
	return db.WriteTx(func(tx *sql.Tx) error {
		return d.setTx(tx, index, record)
	})
}

func (d idxRecordDAO[T]) setTx(tx *sql.Tx, index int, record T) error {
	args := append(d.args(record), index)
	res, err := tx.Exec(`UPDATE `+d.table+` SET `+d.updateSet()+` WHERE idx = ?`, args...)
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
}

// replaceAll 清空整表后按给定顺序重写。仅用于「整份集合一次替换」的语义。
func (d idxRecordDAO[T]) replaceAll(records []T) error {
	return db.WriteTx(func(tx *sql.Tx) error {
		return d.replaceAllTx(tx, records)
	})
}

func (d idxRecordDAO[T]) replaceAllTx(tx *sql.Tx, records []T) error {
	if _, err := tx.Exec(`DELETE FROM ` + d.table); err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`INSERT INTO ` + d.table + `(idx, ` + d.columnList() + `) VALUES(` + placeholders(1+len(d.columns)) + `)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i := range records {
		args := append([]any{i}, d.args(records[i])...)
		if _, err := stmt.Exec(args...); err != nil {
			return err
		}
	}
	return nil
}

// remove 删除若干下标并把后面的下标整体前移，保持下标连续。
// 越界下标按原 list_items 语义直接忽略。
func (d idxRecordDAO[T]) remove(indexes []int) error {
	remove := dedupeInts(indexes)
	if len(remove) == 0 {
		return nil
	}
	sort.Sort(sort.Reverse(sort.IntSlice(remove)))
	return db.WriteTx(func(tx *sql.Tx) error {
		for _, index := range remove {
			res, err := tx.Exec(`DELETE FROM `+d.table+` WHERE idx = ?`, index)
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
			if _, err := tx.Exec(`UPDATE `+d.table+` SET idx = idx - 1 WHERE idx > ?`, index); err != nil {
				return err
			}
		}
		return nil
	})
}

// update 在同一个写事务里读取并更新一条记录，避免读旧快照后整体覆盖。
func (d idxRecordDAO[T]) update(index int, fn func(*T)) error {
	return db.WriteTx(func(tx *sql.Tx) error {
		row := tx.QueryRow(`SELECT `+d.columnList()+` FROM `+d.table+` WHERE idx = ?`, index)
		value, err := d.scan(row.Scan)
		if err == sql.ErrNoRows {
			return fmt.Errorf("index %d out of range", index)
		}
		if err != nil {
			return err
		}
		fn(&value)
		return d.setTx(tx, index, value)
	})
}
