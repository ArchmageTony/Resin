package service

import (
	"encoding/json"
	"net/netip"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Resinat/Resin/internal/config"
	"github.com/Resinat/Resin/internal/state"
	"github.com/Resinat/Resin/internal/topology"
)

func newPlatformLatencyURLTestService(t *testing.T) (*ControlPlaneService, *topology.GlobalNodePool, *state.StateEngine) {
	t.Helper()
	dir := t.TempDir()
	engine, closer, err := state.PersistenceBootstrap(
		filepath.Join(dir, "state"),
		filepath.Join(dir, "cache"),
	)
	if err != nil {
		t.Fatalf("PersistenceBootstrap: %v", err)
	}
	t.Cleanup(func() {
		_ = closer.Close()
	})

	subMgr := topology.NewSubscriptionManager()
	pool := topology.NewGlobalNodePool(topology.PoolConfig{
		SubLookup:              subMgr.Lookup,
		GeoLookup:              func(netip.Addr) string { return "us" },
		MaxLatencyTableEntries: 16,
		MaxConsecutiveFailures: func() int { return 3 },
		LatencyDecayWindow:     func() time.Duration { return 10 * time.Minute },
	})

	runtimeCfg := &atomic.Pointer[config.RuntimeConfig]{}
	runtimeCfg.Store(config.NewDefaultRuntimeConfig())

	cp := &ControlPlaneService{
		Engine:     engine,
		Pool:       pool,
		SubMgr:     subMgr,
		RuntimeCfg: runtimeCfg,
		EnvCfg: &config.EnvConfig{
			DefaultPlatformStickyTTL:              30 * time.Minute,
			DefaultPlatformRegexFilters:           []string{},
			DefaultPlatformRegionFilters:          []string{},
			DefaultPlatformReverseProxyMissAction: "TREAT_AS_EMPTY",
			DefaultPlatformAllocationPolicy:       "BALANCED",
		},
	}
	return cp, pool, engine
}

func TestPlatformLatencyTestURL_CRUDRoundtrip(t *testing.T) {
	cp, pool, engine := newPlatformLatencyURLTestService(t)

	name := "probe-platform"
	urlStr := "https://api.example-target.com/v1/models"
	created, err := cp.CreatePlatform(CreatePlatformRequest{
		Name:           &name,
		LatencyTestURL: &urlStr,
	})
	if err != nil {
		t.Fatalf("CreatePlatform: %v", err)
	}
	if created.LatencyTestURL != urlStr {
		t.Fatalf("response should echo latency_test_url, got %q", created.LatencyTestURL)
	}

	plat, ok := pool.GetPlatform(created.ID)
	if !ok {
		t.Fatal("platform not registered in pool")
	}
	if !plat.LatencyProbeEnabled() || plat.ProbeDomain() != "example-target.com" {
		t.Fatalf("runtime platform should carry the probe URL, got %q", plat.ProbeDomain())
	}

	mp, err := engine.GetPlatform(created.ID)
	if err != nil {
		t.Fatalf("engine.GetPlatform: %v", err)
	}
	if mp.LatencyTestURL != urlStr {
		t.Fatalf("persistence should carry latency_test_url, got %q", mp.LatencyTestURL)
	}

	// Patch: replace the URL.
	url2 := "https://www.gstatic.com/generate_204"
	patch, _ := json.Marshal(map[string]string{"latency_test_url": url2})
	updated, err := cp.UpdatePlatform(created.ID, patch)
	if err != nil {
		t.Fatalf("UpdatePlatform (replace): %v", err)
	}
	if updated.LatencyTestURL != url2 {
		t.Fatalf("expected patched URL %q, got %q", url2, updated.LatencyTestURL)
	}
	if plat2, _ := pool.GetPlatform(created.ID); plat2.ProbeDomain() != "gstatic.com" {
		t.Fatalf("runtime platform should follow the patched URL, got %q", plat2.ProbeDomain())
	}

	// Patch: clear the URL (empty string disables platform probing).
	patchClear, _ := json.Marshal(map[string]string{"latency_test_url": ""})
	cleared, err := cp.UpdatePlatform(created.ID, patchClear)
	if err != nil {
		t.Fatalf("UpdatePlatform (clear): %v", err)
	}
	if cleared.LatencyTestURL != "" {
		t.Fatalf("expected cleared URL, got %q", cleared.LatencyTestURL)
	}
	if plat3, _ := pool.GetPlatform(created.ID); plat3.LatencyProbeEnabled() {
		t.Fatal("runtime platform should be probe-disabled after clearing the URL")
	}
}

func TestPlatformLatencyTestURL_RejectsInvalidURL(t *testing.T) {
	cp, _, _ := newPlatformLatencyURLTestService(t)

	name := "probe-invalid"
	created, err := cp.CreatePlatform(CreatePlatformRequest{Name: &name})
	if err != nil {
		t.Fatalf("CreatePlatform: %v", err)
	}

	for _, bad := range []string{"ftp://example.com/x", "not a url", "example.com/only"} {
		patch, _ := json.Marshal(map[string]string{"latency_test_url": bad})
		if _, err := cp.UpdatePlatform(created.ID, patch); err == nil {
			t.Fatalf("expected rejection for %q", bad)
		}
	}

	// Create-side validation too.
	bad := "ftp://example.com/x"
	if _, err := cp.CreatePlatform(CreatePlatformRequest{
		Name:           &name,
		LatencyTestURL: &bad,
	}); err == nil {
		t.Fatal("expected create-side rejection for invalid URL")
	}
}
