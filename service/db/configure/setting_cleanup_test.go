package configure

import (
	"encoding/json"
	"testing"
)

func TestCleanupTransitThresholdDefaultsAndBoundaries(testContext *testing.T) {
	for _, test := range []struct {
		payload string
		want    int
	}{
		{`{"mode":"transit"}`, 85},
		{`{"mode":"transit","transitScoreThreshold":0}`, 0},
		{`{"mode":"transit","transitScoreThreshold":85}`, 85},
		{`{"mode":"transit","transitScoreThreshold":100}`, 100},
	} {
		var rule NodeCleanupRule
		if err := json.Unmarshal([]byte(test.payload), &rule); err != nil {
			testContext.Fatal(err)
		}
		if got := rule.TransitThreshold(); got != test.want {
			testContext.Fatalf("payload %s: threshold=%d, want %d", test.payload, got, test.want)
		}
	}
}

func TestCleanupTransitSnapshotIsolationAndInvalidation(testContext *testing.T) {
	setupSubscriptionAtomicTestDB(testContext)
	threshold := 90
	setting := NewSetting()
	setting.NodeCleanupRules = []NodeCleanupRule{{
		GroupIDs: []string{"source"}, Mode: "transit", TransitScoreThreshold: &threshold,
	}}
	if err := SetSetting(setting); err != nil {
		testContext.Fatal(err)
	}
	snapshot := GetSchedulerConfig()
	snapshot.NodeCleanupRules[0].GroupIDs[0] = "mutated"
	*snapshot.NodeCleanupRules[0].TransitScoreThreshold = 1
	fresh := GetSchedulerConfig()
	if fresh.NodeCleanupRules[0].GroupIDs[0] != "source" || fresh.NodeCleanupRules[0].TransitThreshold() != 90 {
		testContext.Fatalf("snapshot mutation changed cached configuration: %+v", fresh)
	}
	threshold = 95
	if err := SetSetting(setting); err != nil {
		testContext.Fatal(err)
	}
	if got := GetSchedulerConfig().NodeCleanupRules[0].TransitThreshold(); got != 95 {
		testContext.Fatalf("configuration write did not invalidate snapshot: %d", got)
	}
}
