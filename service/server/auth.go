package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/ProxyStation/proxystation/db/configure"
	"github.com/gin-gonic/gin"
)

const (
	authCookieName = "proxystation_session"
	authSessionTTL = 3 * 24 * time.Hour // 3 天内有访问就自动续期
)

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

	now := time.Now()
	expiresAt := now.Add(authSessionTTL)
	session := &configure.Session{
		SessionID:  sessionID,
		Username:   setting.WebUsername,
		ExpiresAt:  expiresAt,
		LastAccess: now,
	}
	if err := configure.SetSession(session); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save session"})
		return
	}

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
		_ = configure.DeleteSession(sessionID)
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(authCookieName, "", -1, "/", "", false, true)
}

func clearAllSessions() {
	_ = configure.ClearAllSessions()
}

func validateSession(c *gin.Context) (string, bool) {
	sessionID, err := c.Cookie(authCookieName)
	if err != nil || sessionID == "" {
		return "", false
	}

	now := time.Now()

	session, err := configure.GetSession(sessionID)
	if err != nil {
		return "", false
	}

	// 检查是否超过过期时间（从最后访问时间算起）
	if now.Sub(session.LastAccess) > authSessionTTL {
		_ = configure.DeleteSession(sessionID)
		return "", false
	}

	setting := configure.GetSettingNotNil()
	if session.Username != setting.WebUsername {
		_ = configure.DeleteSession(sessionID)
		return "", false
	}

	// 滑动过期：更新最后访问时间和过期时间
	session.LastAccess = now
	session.ExpiresAt = now.Add(authSessionTTL)
	_ = configure.SetSession(session)

	// 同时更新 cookie 的过期时间
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(authCookieName, sessionID, int(authSessionTTL.Seconds()), "/", "", false, true)

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
