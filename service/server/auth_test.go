package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ProxyStation/proxystation/db"
	"github.com/ProxyStation/proxystation/db/configure"
	"github.com/gin-gonic/gin"
)

func setupAuthTestDB(t *testing.T) {
	t.Helper()
	if err := db.Init(filepath.Join(t.TempDir(), "test.db")); err != nil {
		t.Fatalf("db init: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := configure.SetSetting(configure.NewSetting()); err != nil {
		t.Fatalf("set setting: %v", err)
	}
}

func authTestContext(sessionID string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	request.AddCookie(&http.Cookie{Name: authCookieName, Value: sessionID})
	context.Request = request
	return context, recorder
}

func TestLoginReturnsAndPersistsSession(t *testing.T) {
	setupAuthTestDB(t)
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	requestBody, err := json.Marshal(map[string]string{
		"username": configure.DefaultWebUsername,
		"password": configure.DefaultWebPassword,
	})
	if err != nil {
		t.Fatalf("marshal login request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	context, _ := gin.CreateTestContext(recorder)
	context.Request = request

	done := make(chan struct{})
	go func() {
		login(context)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("login did not return within 5 seconds")
	}

	if recorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	cookie := recorder.Result().Cookies()
	if len(cookie) != 1 || cookie[0].Name != authCookieName || cookie[0].Value == "" {
		t.Fatalf("login did not set a session cookie: %v", cookie)
	}
	if _, err := configure.GetSession(cookie[0].Value); err != nil {
		t.Fatalf("login did not persist session: %v", err)
	}
}

func TestValidateSessionSkipsFreshSessionWrite(t *testing.T) {
	setupAuthTestDB(t)
	gin.SetMode(gin.TestMode)
	lastAccess := time.Now().Add(-time.Minute).Round(time.Millisecond)
	session := &configure.Session{
		SessionID:  "fresh",
		Username:   configure.DefaultWebUsername,
		LastAccess: lastAccess,
		ExpiresAt:  lastAccess.Add(authSessionTTL),
	}
	if err := configure.SetSession(session); err != nil {
		t.Fatalf("set session: %v", err)
	}

	context, recorder := authTestContext(session.SessionID)
	if username, ok := validateSession(context); !ok || username != configure.DefaultWebUsername {
		t.Fatalf("unexpected validation result: username=%q ok=%v", username, ok)
	}
	stored, err := configure.GetSession(session.SessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !stored.LastAccess.Equal(lastAccess) {
		t.Fatalf("fresh session was rewritten: got %v want %v", stored.LastAccess, lastAccess)
	}
	if len(recorder.Header().Values("Set-Cookie")) != 0 {
		t.Fatalf("fresh session unexpectedly refreshed cookie: %v", recorder.Header().Values("Set-Cookie"))
	}
}

func TestValidateSessionCoalescesConcurrentRefresh(t *testing.T) {
	setupAuthTestDB(t)
	gin.SetMode(gin.TestMode)
	lastAccess := time.Now().Add(-authSessionRefreshInterval - time.Minute)
	session := &configure.Session{
		SessionID:  "stale",
		Username:   configure.DefaultWebUsername,
		LastAccess: lastAccess,
		ExpiresAt:  lastAccess.Add(authSessionTTL),
	}
	if err := configure.SetSession(session); err != nil {
		t.Fatalf("set session: %v", err)
	}

	const requestCount = 12
	start := make(chan struct{})
	var waitGroup sync.WaitGroup
	var failures atomic.Int32
	var cookieRefreshes atomic.Int32
	for index := 0; index < requestCount; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			context, recorder := authTestContext(session.SessionID)
			if username, ok := validateSession(context); !ok || username != configure.DefaultWebUsername {
				failures.Add(1)
			}
			if len(recorder.Header().Values("Set-Cookie")) > 0 {
				cookieRefreshes.Add(1)
			}
		}()
	}
	close(start)
	waitGroup.Wait()

	if failures.Load() != 0 {
		t.Fatalf("%d concurrent validations failed", failures.Load())
	}
	if cookieRefreshes.Load() != 1 {
		t.Fatalf("session refreshed %d times, want 1", cookieRefreshes.Load())
	}
	stored, err := configure.GetSession(session.SessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !stored.LastAccess.After(lastAccess) {
		t.Fatalf("session was not refreshed: got %v", stored.LastAccess)
	}
}
