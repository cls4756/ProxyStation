package configure

import (
	"database/sql"

	"github.com/ProxyStation/proxystation/db"
)

// replaceStringListTx 用给定的顺序重写某个 owner 下的字符串列表子表。
// 子表结构固定为 (ownerCol, item_idx, value)。
func replaceStringListTx(tx *sql.Tx, table, ownerCol string, ownerIdx int, values []string) error {
	if _, err := tx.Exec(`DELETE FROM `+table+` WHERE `+ownerCol+` = ?`, ownerIdx); err != nil {
		return err
	}
	if len(values) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`INSERT INTO ` + table + `(` + ownerCol + `, item_idx, value) VALUES(?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i, value := range values {
		if _, err := stmt.Exec(ownerIdx, i, value); err != nil {
			return err
		}
	}
	return nil
}

// stringListAll 读取整张字符串列表子表，按 owner 聚合并保持 item_idx 顺序。
func stringListAll(table, ownerCol string) map[int][]string {
	result := make(map[int][]string)
	rows, err := db.ReadDB().Query(`SELECT ` + ownerCol + `, value FROM ` + table + ` ORDER BY ` + ownerCol + `, item_idx`)
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var owner int
		var value string
		if err := rows.Scan(&owner, &value); err != nil {
			return result
		}
		result[owner] = append(result[owner], value)
	}
	return result
}

func stringListGet(table, ownerCol string, ownerIdx int) []string {
	rows, err := db.ReadDB().Query(`SELECT value FROM `+table+` WHERE `+ownerCol+` = ? ORDER BY item_idx`, ownerIdx)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return out
		}
		out = append(out, value)
	}
	return out
}
