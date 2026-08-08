package configure

import (
	"time"

	"github.com/ProxyStation/proxystation/db"
)

const sessionBucket = "auth"

type Session struct {
	SessionID  string    `json:"sessionId"`
	Username   string    `json:"username"`
	ExpiresAt  time.Time `json:"expiresAt"`
	LastAccess time.Time `json:"lastAccess"`
}

// GetSession 获取指定的 session
func GetSession(sessionID string) (*Session, error) {
	var session Session
	err := db.Get(sessionBucket, sessionID, &session)
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// SetSession 保存或更新 session
func SetSession(session *Session) error {
	return db.Set(sessionBucket, session.SessionID, session)
}

// DeleteSession 删除指定的 session
func DeleteSession(sessionID string) error {
	return db.Set(sessionBucket, sessionID, nil)
}

// GetAllSessions 获取所有 session
func GetAllSessions() ([]Session, error) {
	keys, err := db.GetBucketKeys(sessionBucket)
	if err != nil {
		return nil, err
	}

	var sessions []Session
	for _, key := range keys {
		var session Session
		if err := db.Get(sessionBucket, key, &session); err == nil {
			sessions = append(sessions, session)
		}
	}
	return sessions, nil
}

// ClearAllSessions 清空所有 session
func ClearAllSessions() error {
	return db.BucketClear(sessionBucket)
}

// CleanExpiredSessions 清理过期的 session（从最后访问时间算起）
func CleanExpiredSessions(ttl time.Duration) error {
	sessions, err := GetAllSessions()
	if err != nil {
		return err
	}

	now := time.Now()
	for _, session := range sessions {
		if now.Sub(session.LastAccess) > ttl {
			_ = DeleteSession(session.SessionID)
		}
	}
	return nil
}
