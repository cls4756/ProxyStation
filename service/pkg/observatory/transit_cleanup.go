package observatory

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ProxyStation/proxystation/core/engine"
	"github.com/ProxyStation/proxystation/db/configure"
)

const transitCleanupBatchSize = 256

type transitCleanupProbes struct {
	loadNodes      func([]configure.NodeRef) map[configure.NodeRef]configure.ServerRaw
	fast           func(*configure.ServerRaw) (int, error)
	real           func(configure.NodeRef, *configure.ServerRaw) int
	canUseTransit  func(configure.NodeRef) bool
	saveFrontProxy func(configure.NodeRef) bool
}

func probeInvalidTransitNodes(refs []configure.NodeRef, threshold int) []bool {
	if len(refs) == 0 {
		return []bool{}
	}
	probeConfig := configure.GetProbeConfig()
	targets := parseProbeTargets(probeConfig.Targets)
	if targets == nil {
		targets = []string{}
	}
	outbound := configure.GetOutbound(transProxyFallbackName)
	transitNode := outboundTargetNode(outbound)
	probes := transitCleanupProbes{
		loadNodes: configure.GetNodesByRefs,
		fast:      probeServerFast,
		real: func(_ configure.NodeRef, server *configure.ServerRaw) int {
			if server.FrontProxy != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				release, err := engine.AcquireMainProbe(ctx)
				if err != nil {
					return -1
				}
				defer release()
			}
			socksPort := allocProbePort()
			instance, err := engine.StartProbeInstanceWithKernelMode(server, socksPort, probeConfig.KernelMode)
			if err != nil {
				return -1
			}
			defer instance.Close()
			return probeViaCoreTargetsWithTargets(socksPort, targets)
		},
		canUseTransit: func(ref configure.NodeRef) bool {
			return outbound != nil && !nodeRefEqual(transitNode, &ref)
		},
		saveFrontProxy: saveTransProxyFrontProxy,
	}
	return probeInvalidTransitNodesWithProbes(refs, threshold, resolveProbeConcurrency("real", probeConfig), probes)
}

func probeInvalidTransitNodesWithProbes(refs []configure.NodeRef, threshold, concurrency int, probes transitCleanupProbes) []bool {
	uniqueRefs := make([]configure.NodeRef, 0, len(refs))
	refIndexes := make(map[configure.NodeRef]int, len(refs))
	for _, ref := range refs {
		if _, exists := refIndexes[ref]; !exists {
			refIndexes[ref] = len(uniqueRefs)
			uniqueRefs = append(uniqueRefs, ref)
		}
	}
	if concurrency < 1 || concurrency > 8 {
		concurrency = 4
	}
	uniqueInvalid := make([]bool, len(uniqueRefs))
	buffer := newProbeLatencyBuffer("real")
	for start := 0; start < len(uniqueRefs); start += transitCleanupBatchSize {
		end := start + transitCleanupBatchSize
		if end > len(uniqueRefs) {
			end = len(uniqueRefs)
		}
		batch := uniqueRefs[start:end]
		servers := probes.loadNodes(batch)
		workers := concurrency
		if workers > len(batch) {
			workers = len(batch)
		}
		var nextIndex int64 = -1
		var waitGroup sync.WaitGroup
		for worker := 0; worker < workers; worker++ {
			waitGroup.Add(1)
			go func() {
				defer waitGroup.Done()
				for {
					index := int(atomic.AddInt64(&nextIndex, 1))
					if index >= len(batch) {
						return
					}
					ref := batch[index]
					server, exists := servers[ref]
					if !exists {
						uniqueInvalid[start+index] = true
						continue
					}
					fastLatency, realLatency := probeTransitCleanupNode(ref, server, threshold, probes)
					uniqueInvalid[start+index] = fastLatency <= 0 || realLatency <= 0
					buffer.add(ref, fastLatency, realLatency)
				}
			}()
		}
		waitGroup.Wait()
	}
	buffer.flushAll()
	invalid := make([]bool, len(refs))
	for index, ref := range refs {
		invalid[index] = uniqueInvalid[refIndexes[ref]]
	}
	return invalid
}

func probeTransitCleanupNode(ref configure.NodeRef, server configure.ServerRaw, threshold int, probes transitCleanupProbes) (int, int) {
	fastLatency, _ := probes.fast(&server)
	if fastLatency <= 0 && server.IPScore != nil && *server.IPScore > threshold && probes.canUseTransit(ref) {
		if server.FrontProxy == transProxyFallbackName || probes.saveFrontProxy(ref) {
			server.FrontProxy = transProxyFallbackName
			addLog(fmt.Sprintf("🛠️ 高评分节点 %s:%d:%d 使用前置代理 %s 重新检测", ref.Type, ref.Sub, ref.Index, transProxyFallbackName))
			fastLatency, _ = probes.fast(&server)
		}
	}
	if fastLatency <= 0 {
		return fastLatency, -1
	}
	return fastLatency, probes.real(ref, &server)
}
