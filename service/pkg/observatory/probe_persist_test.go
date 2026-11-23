package observatory

import (
	"fmt"
	"testing"

	"github.com/ProxyStation/proxystation/db/configure"
)

func seedServers(t *testing.T, n int) {
	t.Helper()
	servers := make([]configure.ServerRaw, n)
	for i := range servers {
		servers[i] = configure.ServerRaw{
			Name:        fmt.Sprintf("n%d", i),
			Host:        "example.com",
			Port:        443 + i,
			Latency:     500,
			FastLatency: 500,
		}
	}
	if err := configure.SetServers(servers); err != nil {
		t.Fatalf("SetServers: %v", err)
	}
}

func TestProbeLatencyBufferFastMode(t *testing.T) {
	setupTestDB(t)
	seedServers(t, 3)
	buf := newProbeLatencyBuffer("fast")

	// 未达到批量阈值时不落库。
	buf.add(nodeRef(0), 0, 111)
	if got := configure.GetServer(0); got.FastLatency == 111 {
		t.Fatalf("buffer flushed before reaching threshold")
	}
	buf.flushAll()
	got := configure.GetServer(0)
	if got == nil || got.FastLatency != 111 {
		t.Fatalf("fast result not persisted on flush: %+v", got)
	}
	if got.Latency != 500 {
		t.Fatalf("fast mode must not touch real latency: %+v", got)
	}
}

func TestProbeLatencyBufferRealMode(t *testing.T) {
	setupTestDB(t)
	seedServers(t, 2)
	buf := newProbeLatencyBuffer("real")

	buf.add(nodeRef(0), 42, 222)
	buf.flushAll()
	got := configure.GetServer(0)
	if got == nil || got.FastLatency != 42 || got.Latency != 222 {
		t.Fatalf("real mode should persist both fast and real results: %+v", got)
	}
}

func TestProbeLatencyBufferAutoFlushAtBatchSize(t *testing.T) {
	setupTestDB(t)
	seedServers(t, probePersistBatchSize+1)
	buf := newProbeLatencyBuffer("fast")

	for i := 0; i < probePersistBatchSize; i++ {
		buf.add(nodeRef(i), 0, 100+i)
	}
	// 达到批量阈值后应自动落库，无需等待收尾。
	if got := configure.GetServer(0); got == nil || got.FastLatency != 100 {
		t.Fatalf("batch not flushed at threshold: %+v", got)
	}
	// 剩余不足一批的节点由收尾刷出。
	buf.add(nodeRef(probePersistBatchSize), 0, 999)
	buf.flushAll()
	if got := configure.GetServer(probePersistBatchSize); got == nil || got.FastLatency != 999 {
		t.Fatalf("tail result not flushed: %+v", got)
	}
}
