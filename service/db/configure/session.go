package configure

import (
	"time"
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
	return getSessionStore(sessionID)
}

// SetSession 保存或更新 session
func SetSession(session *Session) error {
	return setSessionStore(session)
}

// DeleteSession 删除指定的 session
func DeleteSession(sessionID string) error {
	return deleteSessionStore(sessionID)
}

// GetAllSessions 获取所有 session
func GetAllSessions() ([]Session, error) {
	return allSessionsStore()
}

// ClearAllSessions 清空所有 session
func ClearAllSessions() error {
	return clearSessionsStore()
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
