package configure

import (
	"database/sql"
	"log"

	"github.com/ProxyStation/proxystation/db"
)

// sessions 表按会话 ID 存一行，字段分列，取代原来 kv 里一个 key 一份 JSON 对象。
// 会话会随时间增长，按列存也便于将来直接在 SQL 侧按最后访问时间清理。

const sessionsTable = "sessions"

func getSessionStore(sessionID string) (*Session, error) {
	row := db.ReadDB().QueryRow(`SELECT session_id, username, expires_at, last_access FROM `+sessionsTable+` WHERE session_id = ?`, sessionID)
	var s Session
	var expires, last int64
	if err := row.Scan(&s.SessionID, &s.Username, &expires, &last); err != nil {
		return nil, err
	}
	s.ExpiresAt = intToTime(expires)
	s.LastAccess = intToTime(last)
	return &s, nil
}

func setSessionStore(s *Session) error {
	return db.WriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO `+sessionsTable+`(session_id, username, expires_at, last_access) VALUES(?, ?, ?, ?)
			ON CONFLICT(session_id) DO UPDATE SET username=excluded.username, expires_at=excluded.expires_at, last_access=excluded.last_access`,
			s.SessionID, s.Username, timeToInt(s.ExpiresAt), timeToInt(s.LastAccess))
		return err
	})
}

func deleteSessionStore(sessionID string) error {
	return db.WriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM `+sessionsTable+` WHERE session_id = ?`, sessionID)
		return err
	})
}

func allSessionsStore() ([]Session, error) {
	rows, err := db.ReadDB().Query(`SELECT session_id, username, expires_at, last_access FROM ` + sessionsTable)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []Session
	for rows.Next() {
		var s Session
		var expires, last int64
		if err := rows.Scan(&s.SessionID, &s.Username, &expires, &last); err != nil {
			return nil, err
		}
		s.ExpiresAt = intToTime(expires)
		s.LastAccess = intToTime(last)
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

func clearSessionsStore() error {
	return db.WriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM ` + sessionsTable)
		return err
	})
}

// MigrateSessionStorage 把会话从 kv（bucket="auth"）迁移到列式 sessions 表。
func MigrateSessionStorage() error {
	keys, err := db.GetBucketKeys(sessionBucket)
	if err != nil || len(keys) == 0 {
		return nil
	}
	migrated := 0
	for _, key := range keys {
		var s Session
		if err := db.Get(sessionBucket, key, &s); err != nil {
			continue
		}
		if s.SessionID == "" {
			s.SessionID = key
		}
		if err := setSessionStore(&s); err != nil {
			return err
		}
		migrated++
	}
	if err := db.BucketClear(sessionBucket); err != nil {
		return err
	}
	if migrated > 0 {
		log.Printf("已把 %d 个会话迁移到列式存储", migrated)
	}
	return nil
}
