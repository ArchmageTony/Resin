package state

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Resinat/Resin/internal/model"
)

// Fresh databases must carry the platforms.latency_test_url column.
func TestMigrateStateDB_AddsLatencyTestURLToPlatforms(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := MigrateStateDB(db); err != nil {
		t.Fatalf("MigrateStateDB: %v", err)
	}

	ok, err := hasTableColumn(db, "platforms", "latency_test_url")
	if err != nil || !ok {
		t.Fatalf("expected migrated column platforms.latency_test_url, ok=%v err=%v", ok, err)
	}
}

// The column survives the platform persistence roundtrip.
func TestPlatformLatencyTestURL_Roundtrip(t *testing.T) {
	repo := newTestStateRepo(t)

	mp := model.Platform{
		ID:                               "11111111-1111-1111-1111-111111111111",
		Name:                             "probe-platform",
		StickyTTLNs:                      int64(30 * time.Minute),
		RegexFilters:                     []string{},
		RegionFilters:                    []string{},
		ReverseProxyMissAction:           "TREAT_AS_EMPTY",
		ReverseProxyEmptyAccountBehavior: "RANDOM",
		AllocationPolicy:                 "BALANCED",
		LatencyTestURL:                   "  https://api.example-target.com/v1/models  ",
		UpdatedAtNs:                      time.Now().UnixNano(),
	}
	if err := repo.UpsertPlatform(mp); err != nil {
		t.Fatalf("UpsertPlatform: %v", err)
	}

	got, err := repo.GetPlatform(mp.ID)
	if err != nil {
		t.Fatalf("GetPlatform: %v", err)
	}
	if got.LatencyTestURL != "https://api.example-target.com/v1/models" {
		t.Fatalf("expected trimmed URL, got %q", got.LatencyTestURL)
	}

	// Clearing the URL persists as empty.
	mp.LatencyTestURL = ""
	mp.UpdatedAtNs = time.Now().UnixNano()
	if err := repo.UpsertPlatform(mp); err != nil {
		t.Fatalf("UpsertPlatform (clear): %v", err)
	}
	got, err = repo.GetPlatform(mp.ID)
	if err != nil {
		t.Fatalf("GetPlatform (clear): %v", err)
	}
	if got.LatencyTestURL != "" {
		t.Fatalf("expected cleared URL, got %q", got.LatencyTestURL)
	}
}
