package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ProxyStation/proxystation/db/configure"
	"github.com/gin-gonic/gin"
)

func setupGroupTransferTest(t *testing.T) {
	t.Helper()
	setupListQueryTestDB(t)
	score := 80
	servers := []configure.ServerRaw{
		{Name: "first", Host: "proxy.example", Port: 443, Type: "vless", Link: "vless://credential-a@proxy.example:443#first"},
		{Name: "alias", Host: "proxy.example", Port: 443, Type: "vless", Link: "vless://credential-a@proxy.example:443#alias"},
		{Name: "other", Host: "proxy.example", Port: 443, Type: "vless", Link: "vless://credential-b@proxy.example:443#other"},
	}
	for index := range servers {
		servers[index].IPType = "idc"
		servers[index].IPGeoCountry = "美国"
		servers[index].IPScore = &score
	}
	if err := configure.SetServers(servers); err != nil {
		t.Fatal(err)
	}
}

func invokeGroupTransfer(t *testing.T, handler gin.HandlerFunc, payload any) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "index", Value: "0"}}
	context.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	context.Request.Header.Set("Content-Type", "application/json")
	handler(context)
	var data map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	return recorder.Code, data
}

func TestCopyGroupAPISkipsAliasesButNotDifferentCredentials(t *testing.T) {
	setupGroupTransferTest(t)
	if err := configure.AppendGroup(&configure.Group{ID: "target", Servers: []configure.ServerRef{{Type: "server", Index: 0}}}); err != nil {
		t.Fatal(err)
	}
	status, data := invokeGroupTransfer(t, copyServerToGroup, gin.H{
		"groupId": "target", "ref": configure.ServerRef{Type: "server", Index: 1},
	})
	if status != http.StatusOK || data["copied"] != float64(0) || data["skippedCount"] != float64(1) || configure.GetLenServers() != 3 {
		t.Fatalf("renamed duplicate was not skipped: status=%d data=%v", status, data)
	}
	status, data = invokeGroupTransfer(t, batchCopyServersToGroup, gin.H{
		"groupId": "target", "refs": []configure.ServerRef{{Type: "server", Index: 1}, {Type: "server", Index: 2}},
	})
	if status != http.StatusOK || data["copied"] != float64(1) || data["skippedCount"] != float64(1) ||
		data["groupServerCount"] != float64(2) || configure.GetLenServers() != 4 {
		t.Fatalf("credential distinction was lost: status=%d data=%v", status, data)
	}
	status, data = invokeGroupTransfer(t, batchCopyServersToGroup, gin.H{
		"groupId": "target", "clone": false, "refs": []configure.ServerRef{{Type: "server", Index: 2}},
	})
	if status != http.StatusOK || data["copied"] != float64(0) || data["groupServerCount"] != float64(2) {
		t.Fatalf("legacy clone flag must not bypass group-local deduplication: status=%d data=%v", status, data)
	}
}

func TestCopyGroupAPICannotCreateSharedNodesWithLegacyFlag(testContext *testing.T) {
	setupGroupTransferTest(testContext)
	if err := configure.AppendGroup(&configure.Group{ID: "source", Servers: []configure.ServerRef{{Type: "server", Index: 0}}}); err != nil {
		testContext.Fatal(err)
	}
	for _, target := range []string{"first-target", "second-target"} {
		if err := configure.AppendGroup(&configure.Group{ID: target}); err != nil {
			testContext.Fatal(err)
		}
		status, data := invokeGroupTransfer(testContext, batchCopyServersToGroup, gin.H{
			"groupId": target, "clone": false, "refs": []configure.ServerRef{{Type: "server", Index: 0}},
		})
		if status != http.StatusOK || data["copied"] != float64(1) || data["groupServerCount"] != float64(1) {
			testContext.Fatalf("legacy flag prevented independent copying: status=%d data=%v", status, data)
		}
	}
	first := configure.GetGroup(1).Servers[0]
	second := configure.GetGroup(2).Servers[0]
	if first.Index == 0 || second.Index == 0 || first == second || configure.GetLenServers() != 5 {
		testContext.Fatal("same content is allowed across groups, but node records must be independent")
	}
}

func TestGroupTransferAPIRejectsInvalidAndOversizedReferences(t *testing.T) {
	setupGroupTransferTest(t)
	if err := configure.AppendGroup(&configure.Group{ID: "target"}); err != nil {
		t.Fatal(err)
	}
	oversized := make([]configure.ServerRef, maxGroupTransferRefs+1)
	for index := range oversized {
		oversized[index] = configure.ServerRef{Type: "server", Index: 0}
	}
	for _, refs := range [][]configure.ServerRef{
		{{Type: "unknown", Index: 0}}, {{Type: "server", Index: -1}}, {{Type: "sub_server", Index: 0, Sub: -1}}, oversized,
	} {
		status, _ := invokeGroupTransfer(t, batchCopyServersToGroup, gin.H{"groupId": "target", "refs": refs})
		if status != http.StatusBadRequest {
			t.Fatalf("invalid or unbounded batch must fail before writes: status=%d", status)
		}
	}
	if configure.GetLenServers() != 3 || len(configure.GetGroup(0).Servers) != 0 {
		t.Fatal("rejected transfer must not create nodes or references")
	}
}

func TestMoveGroupAPIReturnsActualCountsAndPreservesSkippedSources(t *testing.T) {
	setupGroupTransferTest(t)
	if err := configure.AppendGroup(&configure.Group{ID: "source", Servers: []configure.ServerRef{
		{Type: "server", Index: 1}, {Type: "server", Index: 2},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := configure.AppendGroup(&configure.Group{ID: "target", Servers: []configure.ServerRef{{Type: "server", Index: 0}}}); err != nil {
		t.Fatal(err)
	}
	status, data := invokeGroupTransfer(t, batchMoveServersToGroup, gin.H{
		"sourceGroupId": "source", "targetGroupId": "target",
		"refs": []configure.ServerRef{{Type: "server", Index: 1}, {Type: "server", Index: 2}},
	})
	if status != http.StatusOK || data["sourceServerCount"] != float64(1) || data["targetServerCount"] != float64(2) ||
		data["moved"] != float64(1) || data["skippedCount"] != float64(1) {
		t.Fatalf("unexpected move response: status=%d data=%v", status, data)
	}
	if configure.GetGroup(0).Servers[0].Index != 1 {
		t.Fatal("skipped source reference must remain available")
	}
}

func TestGroupRemovalReturnsCountWithoutFullReferenceCollection(t *testing.T) {
	setupGroupTransferTest(t)
	if err := configure.AppendGroup(&configure.Group{ID: "group", Servers: []configure.ServerRef{
		{Type: "server", Index: 0}, {Type: "server", Index: 2},
	}}); err != nil {
		t.Fatal(err)
	}
	status, data := invokeGroupTransfer(t, removeServersFromGroup, gin.H{
		"refs": []configure.ServerRef{{Type: "server", Index: 0}},
	})
	if status != http.StatusOK || data["groupId"] != "group" || data["groupServerCount"] != float64(1) {
		t.Fatalf("unexpected removal summary: status=%d data=%v", status, data)
	}
	if _, exists := data["group"]; exists {
		t.Fatal("local removal must not return the complete group reference collection")
	}
	if _, exists := data["servers"]; exists {
		t.Fatal("local removal must not return unrelated node details")
	}
}
