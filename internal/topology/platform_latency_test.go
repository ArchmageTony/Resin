package topology

import (
	"net/netip"
	"testing"
	"time"

	"github.com/Resinat/Resin/internal/node"
	"github.com/Resinat/Resin/internal/platform"
	"github.com/Resinat/Resin/internal/testutil"
)

// makePlatformProbeRoutable gives the node a known routable baseline:
// outbound ready, circuit closed, egress IP known.
func makePlatformProbeRoutable(t *testing.T, pool *GlobalNodePool, h node.Hash) *node.NodeEntry {
	t.Helper()
	entry, ok := pool.GetEntry(h)
	if !ok {
		t.Fatal("entry not found")
	}
	ob := testutil.NewNoopOutbound()
	entry.Outbound.Store(&ob)
	entry.SetEgressIP(netip.MustParseAddr("203.0.113.10"))
	pool.RecordResult(h, true)
	return entry
}

// RecordPlatformLatency success path: writes the sample into the latency
// table under the platform probe domain, records the per-(platform, node)
// attempt timestamp, and never touches the shared probe-attempt timestamps
// nor the global circuit breaker (决策①).
func TestRecordPlatformLatency_Success_NoGlobalSideEffects(t *testing.T) {
	pool, subMgr := newHealthTestPool(3)
	sub := subMgr.Lookup("s1")
	h := addTestNode(pool, sub, `{"type":"ss","n":"1"}`)
	entry := makePlatformProbeRoutable(t, pool, h)

	plat := platform.NewPlatform("plat-a", "A", nil, nil)
	plat.LatencyTestURL = "https://api.example-target.com/v1/models"
	pool.RegisterPlatform(plat)

	lat := 120 * time.Millisecond
	pool.RecordPlatformLatency(h, plat, &lat)

	stats, ok := entry.LatencyTable.GetDomainStats("example-target.com")
	if !ok {
		t.Fatal("platform sample was not written to the latency table")
	}
	if stats.Ewma != 120*time.Millisecond {
		t.Fatalf("unexpected EWMA: %v", stats.Ewma)
	}

	if entry.LastLatencyProbeAttempt.Load() != 0 {
		t.Fatal("shared LastLatencyProbeAttempt must not be touched")
	}
	if entry.LastAuthorityLatencyProbeAttempt.Load() != 0 {
		t.Fatal("shared LastAuthorityLatencyProbeAttempt must not be touched")
	}
	if entry.FailureCount.Load() != 0 || entry.IsCircuitOpen() {
		t.Fatal("RecordResult must not be called by the platform probe path")
	}
	if plat.LastPlatformProbeAttempt(h) == 0 {
		t.Fatal("per-(platform, node) attempt timestamp was not recorded")
	}

	// The platform was notified: its view now admits the node.
	if !plat.View().Contains(h) {
		t.Fatal("platform view should admit the node after a fresh sample")
	}
}

// Failure path: attempt timestamp recorded, no sample written, no breaker
// side effects. A subsequent attempt re-evaluates the node, so a stale
// sample drops the node from the platform view (bounded exposure).
func TestRecordPlatformLatency_FailureAndStaleExpiry(t *testing.T) {
	pool, subMgr := newHealthTestPool(3)
	sub := subMgr.Lookup("s1")
	h := addTestNode(pool, sub, `{"type":"ss","n":"1"}`)
	entry := makePlatformProbeRoutable(t, pool, h)

	plat := platform.NewPlatform("plat-a", "A", nil, nil)
	plat.LatencyTestURL = "https://api.example-target.com/v1/models"
	pool.RegisterPlatform(plat)

	lat := 120 * time.Millisecond
	pool.RecordPlatformLatency(h, plat, &lat)
	if !plat.View().Contains(h) {
		t.Fatal("node should be admitted after a successful platform probe")
	}

	// Age the sample beyond the admission window.
	stale := node.DomainLatencyStats{
		Ewma:        120 * time.Millisecond,
		LastUpdated: time.Now().Add(-3 * time.Hour),
	}
	entry.LatencyTable.LoadEntryClassified("example-target.com", stale, true)

	// A failed platform probe writes no sample but re-evaluates the node.
	pool.RecordPlatformLatency(h, plat, nil)

	// The pre-existing (stale) sample stays but was not refreshed.
	stats, ok := entry.LatencyTable.GetDomainStats("example-target.com")
	if !ok {
		t.Fatal("pre-existing stale sample should remain after a failed probe")
	}
	if !stats.LastUpdated.Equal(stale.LastUpdated) {
		t.Fatal("failed probe must not refresh the sample")
	}
	if entry.FailureCount.Load() != 0 || entry.IsCircuitOpen() {
		t.Fatal("failed platform probe must not feed the global circuit breaker")
	}
	if plat.View().Contains(h) {
		t.Fatal("node with a stale sample must leave the platform view after re-evaluation")
	}
}

// The platform probe domain must not leak into other platforms' admission:
// a second platform without a probe URL is only re-evaluated through the
// official wasEmpty path, not by the platform-specific notification.
func TestRecordPlatformLatency_DoesNotNotifyOtherPlatformsUnnecessarily(t *testing.T) {
	pool, subMgr := newHealthTestPool(3)
	sub := subMgr.Lookup("s1")
	h := addTestNode(pool, sub, `{"type":"ss","n":"1"}`)
	entry := makePlatformProbeRoutable(t, pool, h)

	// Pre-seed a non-platform sample so the table is not empty (wasEmpty=false).
	entry.LatencyTable.LoadEntry("cloudflare.com", node.DomainLatencyStats{
		Ewma:        50 * time.Millisecond,
		LastUpdated: time.Now(),
	})

	platA := platform.NewPlatform("plat-a", "A", nil, nil)
	platA.LatencyTestURL = "https://api.example-target.com/v1/models"
	platB := platform.NewPlatform("plat-b", "B", nil, nil)
	pool.RegisterPlatform(platA)
	pool.RegisterPlatform(platB)

	// Both views rebuilt up-front: B admits (any record), A does not yet
	// (no example-target.com sample).
	pool.RebuildAllPlatforms()
	if platA.View().Contains(h) {
		t.Fatal("A must not admit before its platform-domain sample exists")
	}
	if !platB.View().Contains(h) {
		t.Fatal("B should admit on the pre-seeded record")
	}

	lat := 120 * time.Millisecond
	pool.RecordPlatformLatency(h, platA, &lat)

	if !platA.View().Contains(h) {
		t.Fatal("A should admit after its own platform probe")
	}
	// B was already correct and must be untouched by A's notification path.
	if !platB.View().Contains(h) {
		t.Fatal("B's view must not be disturbed by A's platform probe")
	}
}
