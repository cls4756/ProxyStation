package observatory

import (
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ProxyStation/proxystation/db/configure"
)

func TestTransitCleanupNodeChecks(testContext *testing.T) {
	highScore, lowScore, equalScore, zeroScore, maxScore := 86, 84, 85, 0, 100
	tests := []struct {
		name        string
		score       *int
		threshold   int
		frontProxy  string
		firstFast   int
		retryFast   int
		realLatency int
		canUse      bool
		saveFails   bool
		wantInvalid bool
		wantCalls   []string
	}{
		{name: "direct success still checks real", firstFast: 10, realLatency: 50, wantCalls: []string{"fast:", "real:"}},
		{name: "direct real failure is invalid", score: &highScore, threshold: 85, firstFast: 10, realLatency: -1, canUse: true, wantInvalid: true, wantCalls: []string{"fast:", "real:"}},
		{name: "unscored failure skips transit", threshold: 85, firstFast: -1, canUse: true, wantInvalid: true, wantCalls: []string{"fast:"}},
		{name: "low score failure skips transit", score: &lowScore, threshold: 85, firstFast: -1, canUse: true, wantInvalid: true, wantCalls: []string{"fast:"}},
		{name: "equal threshold skips transit", score: &equalScore, threshold: 85, firstFast: -1, canUse: true, wantInvalid: true, wantCalls: []string{"fast:"}},
		{name: "zero score is not unscored or high", score: &zeroScore, threshold: 0, firstFast: -1, canUse: true, wantInvalid: true, wantCalls: []string{"fast:"}},
		{name: "zero threshold permits positive scores", score: &lowScore, threshold: 0, firstFast: -1, retryFast: 20, realLatency: 50, canUse: true, wantCalls: []string{"fast:", "save", "fast:trans-proxy", "real:trans-proxy"}},
		{name: "maximum threshold skips transit", score: &maxScore, threshold: 100, firstFast: -1, canUse: true, wantInvalid: true, wantCalls: []string{"fast:"}},
		{name: "high score transit success is retained", score: &highScore, threshold: 85, firstFast: -1, retryFast: 20, realLatency: 50, canUse: true, wantCalls: []string{"fast:", "save", "fast:trans-proxy", "real:trans-proxy"}},
		{name: "transit fast failure skips real", score: &highScore, threshold: 85, firstFast: -1, retryFast: -1, canUse: true, wantInvalid: true, wantCalls: []string{"fast:", "save", "fast:trans-proxy"}},
		{name: "transit real failure is invalid", score: &highScore, threshold: 85, firstFast: -1, retryFast: 20, realLatency: -1, canUse: true, wantInvalid: true, wantCalls: []string{"fast:", "save", "fast:trans-proxy", "real:trans-proxy"}},
		{name: "unavailable or circular transit is not assigned", score: &highScore, threshold: 85, firstFast: -1, wantInvalid: true, wantCalls: []string{"fast:"}},
		{name: "failed save skips retry", score: &highScore, threshold: 85, firstFast: -1, canUse: true, saveFails: true, wantInvalid: true, wantCalls: []string{"fast:", "save"}},
		{name: "failed custom front proxy is replaced", score: &highScore, threshold: 85, frontProxy: "old-proxy", firstFast: -1, retryFast: 20, realLatency: 50, canUse: true, wantCalls: []string{"fast:old-proxy", "save", "fast:trans-proxy", "real:trans-proxy"}},
		{name: "existing transit gets one retry", score: &highScore, threshold: 85, frontProxy: "trans-proxy", firstFast: -1, retryFast: 20, realLatency: 50, canUse: true, wantCalls: []string{"fast:trans-proxy", "fast:trans-proxy", "real:trans-proxy"}},
	}
	for _, test := range tests {
		testContext.Run(test.name, func(testContext *testing.T) {
			var calls []string
			fastCalls := 0
			probes := transitCleanupProbes{
				fast: func(server *configure.ServerRaw) (int, error) {
					calls = append(calls, "fast:"+server.FrontProxy)
					fastCalls++
					if fastCalls == 1 {
						return test.firstFast, nil
					}
					return test.retryFast, nil
				},
				real: func(_ configure.NodeRef, server *configure.ServerRaw) int {
					calls = append(calls, "real:"+server.FrontProxy)
					return test.realLatency
				},
				canUseTransit: func(configure.NodeRef) bool { return test.canUse },
				saveFrontProxy: func(configure.NodeRef) bool {
					calls = append(calls, "save")
					return !test.saveFails
				},
			}
			server := configure.ServerRaw{IPScore: test.score, FrontProxy: test.frontProxy}
			fastLatency, realLatency := probeTransitCleanupNode(nodeRef(0), server, test.threshold, probes)
			if invalid := fastLatency <= 0 || realLatency <= 0; invalid != test.wantInvalid {
				testContext.Fatalf("invalid = %v, want %v (fast=%d, real=%d)", invalid, test.wantInvalid, fastLatency, realLatency)
			}
			if !reflect.DeepEqual(calls, test.wantCalls) {
				testContext.Fatalf("calls = %v, want %v", calls, test.wantCalls)
			}
			if server.FrontProxy != test.frontProxy {
				testContext.Fatal("probing must not mutate the input snapshot")
			}
		})
	}
}

func TestTransitCleanupPersistsFrontProxyBeforeRetry(testContext *testing.T) {
	setupTestDB(testContext)
	score := 90
	server := configure.ServerRaw{Name: "high-score", IPScore: &score, Latency: 500, FastLatency: 500}
	if err := configure.SetServers([]configure.ServerRaw{server}); err != nil {
		testContext.Fatal(err)
	}
	server.Name = "high-score-sub"
	if err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{{
		ID: "transit-sub", Servers: []configure.ServerRaw{server},
	}}); err != nil {
		testContext.Fatal(err)
	}
	refs := []configure.NodeRef{nodeRef(0), {Type: "sub_server", Sub: 0, Index: 0}}
	probes := transitCleanupProbes{
		loadNodes: configure.GetNodesByRefs,
		fast: func(server *configure.ServerRaw) (int, error) {
			if server.FrontProxy == "" {
				return -1, nil
			}
			ref := refs[0]
			if server.Name == "high-score-sub" {
				ref = refs[1]
			}
			saved := configure.GetNodesByRefs([]configure.NodeRef{ref})[ref]
			if saved.FrontProxy == transProxyFallbackName {
				return 20, nil
			}
			testContext.Error("front proxy must be saved before retry")
			return -1, nil
		},
		real: func(ref configure.NodeRef, server *configure.ServerRaw) int {
			if saved := configure.GetNodesByRefs([]configure.NodeRef{ref})[ref]; saved.FrontProxy != transProxyFallbackName || server.FrontProxy != saved.FrontProxy {
				testContext.Error("real probe must use the saved front proxy")
			}
			if ref.Type == "sub_server" {
				return -1
			}
			return 50
		},
		canUseTransit:  func(configure.NodeRef) bool { return true },
		saveFrontProxy: saveTransProxyFrontProxy,
	}
	invalid := probeInvalidTransitNodesWithProbes(refs, 85, 1, probes)
	if !reflect.DeepEqual(invalid, []bool{false, true}) {
		testContext.Fatalf("invalid = %v, want [false true]", invalid)
	}
	for _, ref := range refs {
		saved := configure.GetNodesByRefs([]configure.NodeRef{ref})[ref]
		if saved.FrontProxy != transProxyFallbackName || saved.FastLatency != 20 || saved.LastFastProbeTime == 0 || saved.LastProbeTime == 0 {
			testContext.Fatalf("front proxy and probe results not persisted: %+v", saved)
		}
		if ref.Type == "server" && saved.Latency != 50 || ref.Type == "sub_server" && saved.Latency != -1 {
			testContext.Fatalf("wrong real latency: %+v", saved)
		}
	}
}

func TestTransitCleanupLoadsOnlyUniqueReferencesFromLargeCollection(testContext *testing.T) {
	setupTestDB(testContext)
	seedServers(testContext, 10000)
	refs := []configure.NodeRef{nodeRef(9999), nodeRef(7), nodeRef(9999), nodeRef(20000)}
	loadCalls := 0
	var loaded []configure.NodeRef
	probes := transitCleanupProbes{
		loadNodes: func(requested []configure.NodeRef) map[configure.NodeRef]configure.ServerRaw {
			loadCalls++
			loaded = append(loaded, requested...)
			return configure.GetNodesByRefs(requested)
		},
		fast: func(server *configure.ServerRaw) (int, error) {
			if server.Port == 450 {
				return -1, nil
			}
			return 20, nil
		},
		real: func(configure.NodeRef, *configure.ServerRaw) int { return 50 },
	}
	invalid := probeInvalidTransitNodesWithProbes(refs, 85, 2, probes)
	if !reflect.DeepEqual(invalid, []bool{false, true, false, true}) {
		testContext.Fatalf("invalid = %v", invalid)
	}
	if loadCalls != 1 || !reflect.DeepEqual(loaded, []configure.NodeRef{nodeRef(9999), nodeRef(7), nodeRef(20000)}) {
		testContext.Fatalf("must make one exact, deduplicated batch read: calls=%d, refs=%v", loadCalls, loaded)
	}
	if untouched := configure.GetServer(0); untouched.FastLatency != 500 || untouched.Latency != 500 {
		testContext.Fatalf("unselected node was changed: %+v", untouched)
	}
}

func TestTransitCleanupBatchAndConcurrencyBounds(testContext *testing.T) {
	setupTestDB(testContext)
	seedServers(testContext, transitCleanupBatchSize+1)
	refs := make([]configure.NodeRef, transitCleanupBatchSize+1)
	for index := range refs {
		refs[index] = nodeRef(index)
	}
	loadCalls := 0
	var active, maximum, fastCalls int32
	probes := transitCleanupProbes{
		loadNodes: func(requested []configure.NodeRef) map[configure.NodeRef]configure.ServerRaw {
			loadCalls++
			if len(requested) > transitCleanupBatchSize {
				testContext.Errorf("unbounded node batch: %d", len(requested))
			}
			return configure.GetNodesByRefs(requested)
		},
		fast: func(*configure.ServerRaw) (int, error) {
			atomic.AddInt32(&fastCalls, 1)
			current := atomic.AddInt32(&active, 1)
			for {
				previous := atomic.LoadInt32(&maximum)
				if current <= previous || atomic.CompareAndSwapInt32(&maximum, previous, current) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			atomic.AddInt32(&active, -1)
			return -1, nil
		},
		real: func(configure.NodeRef, *configure.ServerRaw) int {
			testContext.Error("real probe must not run after failed unscored fast probe")
			return 50
		},
	}
	invalid := probeInvalidTransitNodesWithProbes(refs, 85, 3, probes)
	for index, result := range invalid {
		if !result {
			testContext.Errorf("node %d should be invalid", index)
		}
	}
	if loadCalls != 2 || int(fastCalls) != len(refs) || maximum > 3 {
		testContext.Fatalf("wrong query or worker budget: loads=%d fast=%d max=%d", loadCalls, fastCalls, maximum)
	}
}

func TestTransitCleanupEmptyReferences(testContext *testing.T) {
	rule := configure.NodeCleanupRule{Mode: "transit"}
	if result := ProbeInvalidNodes(nil, rule); len(result) != 0 {
		testContext.Fatalf("empty cleanup should return no results: %v", result)
	}
}

func TestTransitOutboundTargetRejectsSelfReference(testContext *testing.T) {
	setupTestDB(testContext)
	ref := nodeRef(0)
	if err := configure.AppendGroup(&configure.Group{
		ID: "transit-group", Servers: []configure.ServerRef{{Type: ref.Type, Index: ref.Index}},
	}); err != nil {
		testContext.Fatal(err)
	}
	for _, target := range []configure.OutboundTarget{
		{TargetType: "node", NodeRef: &ref},
		{TargetType: "group", GroupID: "transit-group", ActiveNodeRef: &ref},
		{TargetType: "group", GroupID: "transit-group"},
	} {
		testContext.Run(fmt.Sprintf("%s-%v", target.TargetType, target.ActiveNodeRef), func(testContext *testing.T) {
			outbound := &configure.Outbound{Target: target}
			if !nodeRefEqual(outboundTargetNode(outbound), &ref) {
				testContext.Fatal("transit must detect direct and group self references")
			}
		})
	}
	if outboundTargetNode(nil) != nil {
		testContext.Fatal("missing transit outbound must have no node target")
	}
}
