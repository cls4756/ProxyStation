package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ProxyStation/proxystation/db/configure"
	"github.com/ProxyStation/proxystation/pkg/observatory"
	"github.com/gin-gonic/gin"
)

func TestSettingTransitCleanupThresholdValidation(testContext *testing.T) {
	for _, test := range []struct {
		name      string
		threshold any
		wantCode  int
		want      int
	}{
		{"default", nil, http.StatusOK, 85},
		{"zero", 0, http.StatusOK, 0},
		{"custom", 90, http.StatusOK, 90},
		{"maximum", 100, http.StatusOK, 100},
		{"negative", -1, http.StatusBadRequest, 0},
		{"above maximum", 101, http.StatusBadRequest, 0},
		{"fraction", 85.5, http.StatusBadRequest, 0},
		{"string", "85", http.StatusBadRequest, 0},
	} {
		testContext.Run(test.name, func(testContext *testing.T) {
			setupAuthTestDB(testContext)
			testContext.Cleanup(observatory.Stop)
			gin.SetMode(gin.TestMode)
			if err := configure.AppendGroup(&configure.Group{ID: "cleanup-source", Name: "source"}); err != nil {
				testContext.Fatal(err)
			}
			if err := configure.AppendGroup(&configure.Group{ID: "subscription-group", Name: "subscription", FromSub: true}); err != nil {
				testContext.Fatal(err)
			}
			rule := map[string]any{"mode": "transit", "groupIds": []string{"cleanup-source", "subscription-group"}, "intervalHour": 24}
			if test.threshold != nil {
				rule["transitScoreThreshold"] = test.threshold
			}
			body, err := json.Marshal(map[string]any{
				"webUsername": configure.DefaultWebUsername, "groupActiveNodeProbeEnabled": false,
				"nodeCleanupRules": []any{rule},
			})
			if err != nil {
				testContext.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, "/api/setting", bytes.NewReader(body))
			context.Request.Header.Set("Content-Type", "application/json")
			setSetting(context)
			if recorder.Code != test.wantCode {
				testContext.Fatalf("status=%d, want %d: %s", recorder.Code, test.wantCode, recorder.Body.String())
			}
			rules := configure.GetSchedulerConfig().NodeCleanupRules
			if test.wantCode == http.StatusOK {
				if len(rules) != 1 || rules[0].Mode != "transit" || rules[0].TransitThreshold() != test.want || len(rules[0].GroupIDs) != 1 || rules[0].GroupIDs[0] != "cleanup-source" {
					testContext.Fatalf("transit rule not persisted: %+v", rules)
				}
			} else if len(rules) != 0 {
				testContext.Fatalf("invalid request changed saved rules: %+v", rules)
			}
		})
	}
}
