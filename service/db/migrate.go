package db

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	bolt "go.etcd.io/bbolt"
)

// bbolt 文件头里第 16 字节开始的 4 字节是 meta page magic（小端 0xED0CDAED）。
const boltMagic = 0xED0CDAED

var sqliteHeader = []byte("SQLite format 3\x00")

// prepareStorage 在打开数据库前检查目标文件：如果它还是旧版 bbolt 文件，
// 先把它迁移成 SQLite；已经是 SQLite 或不存在则直接返回。
func prepareStorage(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("database path is a directory: %s", path)
	}
	if info.Size() == 0 {
		return nil
	}

	kind, err := detectFileKind(path)
	if err != nil {
		return err
	}
	switch kind {
	case fileKindSQLite:
		return nil
	case fileKindBolt:
		return migrateFromBolt(path)
	default:
		return fmt.Errorf("unrecognized database file format: %s", path)
	}
}

type fileKind int

const (
	fileKindUnknown fileKind = iota
	fileKindSQLite
	fileKindBolt
)

func detectFileKind(path string) (fileKind, error) {
	f, err := os.Open(path)
	if err != nil {
		return fileKindUnknown, err
	}
	defer f.Close()

	header := make([]byte, 4096)
	n, err := f.Read(header)
	if err != nil && n == 0 {
		return fileKindUnknown, err
	}
	header = header[:n]
	if len(header) >= len(sqliteHeader) && bytes.Equal(header[:len(sqliteHeader)], sqliteHeader) {
		return fileKindSQLite, nil
	}
	if len(header) >= 20 && binary.LittleEndian.Uint32(header[16:20]) == boltMagic {
		return fileKindBolt, nil
	}
	return fileKindUnknown, nil
}

// migrateFromBolt 把旧版 bbolt 里的所有 bucket/key 迁移进 SQLite。
// 顶层是 JSON 数组的值会被拆成 list_items 的多行（每个元素一行）；其它值按标量写入。
// 迁移先写临时文件，成功后备份原文件再原子替换，失败时原文件不受影响。
func migrateFromBolt(path string) error {
	tmpPath := path + ".sqlite-migrating"
	for _, candidate := range []string{tmpPath, tmpPath + "-wal", tmpPath + "-shm"} {
		if err := os.Remove(candidate); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	old, err := bolt.Open(path, 0600, &bolt.Options{ReadOnly: true, Timeout: 5 * time.Second})
	if err != nil {
		return fmt.Errorf("open legacy bbolt database: %w", err)
	}
	defer old.Close()

	handle, err := sql.Open("sqlite", "file:"+tmpPath+"?_pragma=busy_timeout(10000)")
	if err != nil {
		return err
	}
	handle.SetMaxOpenConns(1)

	cleanupTmp := func() {
		_ = handle.Close()
		for _, candidate := range []string{tmpPath, tmpPath + "-wal", tmpPath + "-shm"} {
			_ = os.Remove(candidate)
		}
	}

	for _, stmt := range schemaStatements {
		if _, err := handle.Exec(stmt); err != nil {
			cleanupTmp()
			return fmt.Errorf("prepare migration schema: %w", err)
		}
	}

	// 所有插入放在一个事务里：否则每条元素一次自动提交 + fsync，上万条数据会拖到几分钟。
	sqlTx, err := handle.Begin()
	if err != nil {
		cleanupTmp()
		return err
	}

	var scalarCount, itemCount, listCount int
	err = old.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			bucket := string(name)
			return b.ForEach(func(k, v []byte) error {
				if b.Bucket(k) != nil {
					return nil // 嵌套 bucket 未在本项目中使用
				}
				key := string(k)
				value := cloneBytes(v)
				if isJSONArray(value) {
					var items []json.RawMessage
					if err := json.Unmarshal(value, &items); err == nil {
						stmt, err := sqlTx.Prepare(`INSERT INTO list_items(bucket, key, idx, value) VALUES(?, ?, ?, ?)`)
						if err != nil {
							return err
						}
						for i, item := range items {
							if _, err := stmt.Exec(bucket, key, i, []byte(item)); err != nil {
								stmt.Close()
								return err
							}
							itemCount++
						}
						if err := stmt.Close(); err != nil {
							return err
						}
						listCount++
						return nil
					}
				}
				if _, err := sqlTx.Exec(`INSERT INTO kv(bucket, key, value) VALUES(?, ?, ?)`,
					bucket, key, value); err != nil {
					return err
				}
				scalarCount++
				return nil
			})
		})
	})
	if err != nil {
		_ = sqlTx.Rollback()
		cleanupTmp()
		return fmt.Errorf("read legacy bbolt data: %w", err)
	}
	if err := sqlTx.Commit(); err != nil {
		cleanupTmp()
		return fmt.Errorf("commit migrated data: %w", err)
	}

	if err := handle.Close(); err != nil {
		cleanupTmp()
		return err
	}

	backupPath := path + ".bolt.bak"
	_ = os.Remove(backupPath)
	if err := os.Rename(path, backupPath); err != nil {
		cleanupTmp()
		return fmt.Errorf("backup legacy database: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		// 尽量把原文件放回去，避免数据丢失。
		_ = os.Rename(backupPath, path)
		cleanupTmp()
		return fmt.Errorf("activate migrated database: %w", err)
	}
	for _, candidate := range []string{tmpPath + "-wal", tmpPath + "-shm"} {
		_ = os.Remove(candidate)
	}

	log.Printf("已迁移旧版 bbolt 数据到 SQLite：标量 %d 个，列表 %d 个（共 %d 条元素）；原文件备份为 %s",
		scalarCount, listCount, itemCount, backupPath)
	return nil
}
