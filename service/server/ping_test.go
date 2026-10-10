package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPingNodesDisablesProxyBuffering(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/ping", strings.NewReader(`{"refs":[],"mode":"fast"}`))
	context.Request.Header.Set("Content-Type", "application/json")

	pingNodes(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("ping status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if got := recorder.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q, want no", got)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "event: end") {
		t.Fatalf("response missing end event: %q", body)
	}
}
