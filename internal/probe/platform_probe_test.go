package probe

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/Resinat/Resin/internal/node"
	"github.com/Resinat/Resin/internal/platform"
	"github.com/Resinat/Resin/internal/testutil"
	"github.com/Resinat/Resin/internal/topology"
)

func newPlatformProbeTestPool() *topology.GlobalNodePool {
	return topology.NewGlobalNodePool(topology.PoolConfig{
		MaxLatencyTableEntries: 16,
		MaxConsecutiveFailures: func() int { return 3 },
	})
}

func addPlatformProbeTestNode(pool *topology.GlobalNodePool, raw string) node.Hash {
	hash := node.HashFromRawOptions([]byte(raw))
	pool.AddNodeFromSub(hash, []byte(raw), "sub1")
	if entry, ok := pool.GetEntry(hash); ok {
		ob := testutil.NewNoopOutbound()
		entry.Outbound.Store(&ob)
		entry.SetEgressIP(netip.MustParseAddr("203.0.113.10"))
		// New nodes start circuit-open; prove health so tests start from a
		// known non-open baseline.
		pool.RecordResult(hash, true)
	}
	return hash
}

// scanPlatformLatency enqueues platform-scoped tasks for due nodes,
// keyed with the platform ID.
func TestScanPlatformLatency_EnqueuesPlatformTasks(t *testing.T) {
	pool := newPlatformProbeTestPool()

	plat := platform.NewPlatform("plat-a", "A", nil, nil)
	plat.LatencyTestURL = "https://api.example-target.com/v1/models"
	pool.RegisterPlatform(plat)

	hash := addPlatformProbeTestNode(pool, `{"type":"ss","n":"1"}`)

	mgr := NewProbeManager(ProbeConfig{Pool: pool})
	defer mgr.Stop()

	mgr.scanPlatformLatency()

	key := probeTaskKey{hash: hash, kind: probeTaskKindPlatformLatency, platformID: "plat-a"}
	if _, ok := mgr.taskStates.Load(key); !ok {
		t.Fatal("platform probe task was not enqueued for a due node")
	}

	task, ok := mgr.taskQueue.Dequeue()
	if !ok {
		t.Fatal("expected one queued platform task")
	}
	if task.key != key {
		t.Fatalf("unexpected task key: %+v", task.key)
	}
}

// Nodes excluded by the platform static filter are not probed; platforms
// without a probe URL are ignored entirely.
func TestScanPlatformLatency_SkipsUnmatchedNodesAndDisabledPlatforms(t *testing.T) {
	pool := newPlatformProbeTestPool()

	plat := platform.NewPlatform("plat-a", "A", nil, nil)
	plat.LatencyTestURL = "https://api.example-target.com/v1/models"
	pool.RegisterPlatform(plat)
	plain := platform.NewPlatform("plat-b", "B", nil, nil)
	pool.RegisterPlatform(plain)

	addPlatformProbeTestNode(pool, `{"type":"ss","n":"1"}`)

	mgr := NewProbeManager(ProbeConfig{Pool: pool})
	defer mgr.Stop()

	mgr.scanPlatformLatency()

	// Workers are not started, so taskStates retains everything the scan
	// enqueued. Only plat-a's task may exist; plat-b has no URL.
	count := 0
	mgr.taskStates.Range(func(k probeTaskKey, _ *probeTaskState) bool {
		if k.kind != probeTaskKindPlatformLatency {
			return true
		}
		count++
		if k.platformID != "plat-a" {
			t.Fatalf("unexpected platformID in task: %q", k.platformID)
		}
		return true
	})
	if count != 1 {
		t.Fatalf("expected exactly 1 platform task, got %d", count)
	}

	task, ok := mgr.taskQueue.Dequeue()
	if !ok {
		t.Fatal("expected one queued platform task")
	}
	if task.key.platformID != "plat-a" {
		t.Fatalf("unexpected task key: %+v", task.key)
	}
}

// The platform probe execution path uses the platform URL and writes back
// through RecordPlatformLatency only: sample yes, RecordResult and shared
// timestamps no.
func TestProbePlatformLatency_Success(t *testing.T) {
	pool := newPlatformProbeTestPool()
	plat := platform.NewPlatform("plat-a", "A", nil, nil)
	plat.LatencyTestURL = "https://api.example-target.com/v1/models"
	pool.RegisterPlatform(plat)
	hash := addPlatformProbeTestNode(pool, `{"type":"ss","n":"1"}`)
	entry, _ := pool.GetEntry(hash)

	var fetchedURL string
	mgr := NewProbeManager(ProbeConfig{
		Pool: pool,
		Fetcher: func(_ node.Hash, url string) ([]byte, time.Duration, error) {
			fetchedURL = url
			return []byte("ok"), 25 * time.Millisecond, nil
		},
	})
	defer mgr.Stop()

	mgr.probePlatformLatency(hash, "plat-a")

	if fetchedURL != "https://api.example-target.com/v1/models" {
		t.Fatalf("expected platform URL to be probed, got %q", fetchedURL)
	}
	if _, ok := entry.LatencyTable.GetDomainStats("example-target.com"); !ok {
		t.Fatal("platform sample was not recorded")
	}
	if entry.FailureCount.Load() != 0 || entry.IsCircuitOpen() {
		t.Fatal("RecordResult must not be called by the platform probe path")
	}
	if entry.LastLatencyProbeAttempt.Load() != 0 || entry.LastAuthorityLatencyProbeAttempt.Load() != 0 {
		t.Fatal("shared probe timestamps must not be refreshed")
	}
	if plat.LastPlatformProbeAttempt(hash) == 0 {
		t.Fatal("platform attempt timestamp was not recorded")
	}
	if !plat.View().Contains(hash) {
		t.Fatal("platform view should admit the node after a successful probe")
	}
}

// Platform probe failures never feed the global circuit breaker (决策①).
func TestProbePlatformLatency_FailureDoesNotFeedBreaker(t *testing.T) {
	pool := newPlatformProbeTestPool()
	plat := platform.NewPlatform("plat-a", "A", nil, nil)
	plat.LatencyTestURL = "https://api.example-target.com/v1/models"
	pool.RegisterPlatform(plat)
	hash := addPlatformProbeTestNode(pool, `{"type":"ss","n":"1"}`)
	entry, _ := pool.GetEntry(hash)

	mgr := NewProbeManager(ProbeConfig{
		Pool: pool,
		Fetcher: func(_ node.Hash, _ string) ([]byte, time.Duration, error) {
			return nil, 0, errors.New("connection refused")
		},
	})
	defer mgr.Stop()

	mgr.probePlatformLatency(hash, "plat-a")

	if entry.FailureCount.Load() != 0 {
		t.Fatalf("FailureCount must stay 0, got %d", entry.FailureCount.Load())
	}
	if entry.IsCircuitOpen() {
		t.Fatal("global circuit must not open from a platform probe failure")
	}
	if _, ok := entry.LatencyTable.GetDomainStats("example-target.com"); ok {
		t.Fatal("failed platform probe must not write a sample")
	}
	if plat.LastPlatformProbeAttempt(hash) == 0 {
		t.Fatal("failed attempt must still record the attempt timestamp")
	}
	if plat.View().Contains(hash) {
		t.Fatal("node without a platform sample must not be in the platform view")
	}
}
