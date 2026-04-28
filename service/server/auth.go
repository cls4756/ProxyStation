package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ProxyStation/proxystation/db/configure"
	"github.com/gin-gonic/gin"
)

const (
	authCookieName = "proxystation_session"
	authSessionTTL = 7 * 24 * time.Hour
)

type authSession struct {
	Username  string
	ExpiresAt time.Time
}

type authManager struct {
	mu       sync.RWMutex
	sessions map[string]authSession
}

var webAuth = &authManager{
	sessions: map[string]authSession{},
}

func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if username, ok := validateSession(c); ok {
			c.Set("auth_username", username)
			c.Next()
			return
		}

		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			c.Abort()
			return
		}

		c.Next()
	}
}

func login(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	setting := configure.GetSettingNotNil()
	if !validCredentialPair(setting, req.Username, req.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}

	sessionID, err := newSessionID()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}

	expiresAt := time.Now().Add(authSessionTTL)
	webAuth.mu.Lock()
	webAuth.sessions[sessionID] = authSession{
		Username:  setting.WebUsername,
		ExpiresAt: expiresAt,
	}
	webAuth.mu.Unlock()

	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(authCookieName, sessionID, int(authSessionTTL.Seconds()), "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"username": setting.WebUsername,
		"expires":  expiresAt.Unix(),
	})
}

func logout(c *gin.Context) {
	clearSession(c)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func changePassword(c *gin.Context) {
	var req struct {
		OldPassword     string `json:"oldPassword"`
		NewPassword     string `json:"newPassword"`
		ConfirmPassword string `json:"confirmPassword"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.OldPassword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "旧密码不能为空"})
		return
	}
	if req.NewPassword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "新密码不能为空"})
		return
	}
	if req.NewPassword != req.ConfirmPassword {
		c.JSON(http.StatusBadRequest, gin.H{"error": "两次输入的新密码不一致"})
		return
	}

	setting := configure.GetSettingNotNil()
	if !configure.VerifyWebPassword(setting.WebPassword, req.OldPassword) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "旧密码错误"})
		return
	}

	hashed, err := configure.HashWebPassword(req.NewPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码加密失败"})
		return
	}
	setting.WebPassword = hashed
	if err := configure.SetSetting(setting); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	clearAllSessions()
	clearSession(c)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func authMe(c *gin.Context) {
	username, ok := validateSession(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"authenticated": true,
		"username":      username,
	})
}

func clearSession(c *gin.Context) {
	if sessionID, err := c.Cookie(authCookieName); err == nil {
		webAuth.mu.Lock()
		delete(webAuth.sessions, sessionID)
		webAuth.mu.Unlock()
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(authCookieName, "", -1, "/", "", false, true)
}

func clearAllSessions() {
	webAuth.mu.Lock()
	webAuth.sessions = map[string]authSession{}
	webAuth.mu.Unlock()
}

func validateSession(c *gin.Context) (string, bool) {
	sessionID, err := c.Cookie(authCookieName)
	if err != nil || sessionID == "" {
		return "", false
	}

	now := time.Now()

	webAuth.mu.RLock()
	session, ok := webAuth.sessions[sessionID]
	webAuth.mu.RUnlock()
	if !ok {
		return "", false
	}
	if now.After(session.ExpiresAt) {
		webAuth.mu.Lock()
		delete(webAuth.sessions, sessionID)
		webAuth.mu.Unlock()
		return "", false
	}

	setting := configure.GetSettingNotNil()
	if session.Username != setting.WebUsername {
		webAuth.mu.Lock()
		delete(webAuth.sessions, sessionID)
		webAuth.mu.Unlock()
		return "", false
	}

	return session.Username, true
}

func validCredentialPair(setting *configure.Setting, username string, password string) bool {
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(username)), []byte(strings.TrimSpace(setting.WebUsername))) == 1 &&
		configure.VerifyWebPassword(setting.WebPassword, password)
}

func newSessionID() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
