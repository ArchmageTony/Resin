package platform

import (
	"regexp"
	"testing"
	"time"

	"github.com/Resinat/Resin/internal/node"
)

func TestProbeDomain_ExtractsETLDPlusOne(t *testing.T) {
	p := NewPlatform("p1", "Test", nil, nil)
	p.LatencyTestURL = "https://api.example-target.com/v1/models"
	if got := p.ProbeDomain(); got != "example-target.com" {
		t.Fatalf("expected example-target.com, got %q", got)
	}
	if !p.LatencyProbeEnabled() {
		t.Fatal("platform with URL should be probe-enabled")
	}
}

func TestProbeDomain_EmptyURL(t *testing.T) {
	p := NewPlatform("p1", "Test", nil, nil)
	if got := p.ProbeDomain(); got != "" {
		t.Fatalf("expected empty domain, got %q", got)
	}
	if p.LatencyProbeEnabled() {
		t.Fatal("platform without URL should not be probe-enabled")
	}
}

// Platforms without a probe URL keep the official any-record admission.
func TestEvaluateNode_NoURL_KeepsAnyRecordAdmission(t *testing.T) {
	p := NewPlatform("p1", "Test", nil, nil)
	h := makeHash(`{"type":"ss"}`)
	entry := makeFullyRoutableEntry(h, "sub1") // has example.com sample only

	p.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(h, entry)
	}, alwaysLookup, usGeoLookup)

	if p.View().Size() != 1 {
		t.Fatal("platform without URL should keep official any-record admission")
	}
}

// Platforms with a probe URL admit only on a recent platform-domain sample.
func TestEvaluateNode_WithURL_RequiresPlatformDomainSample(t *testing.T) {
	p := NewPlatform("p1", "Test", nil, nil)
	p.LatencyTestURL = "https://api.example-target.com/v1/models"
	h := makeHash(`{"type":"ss"}`)
	entry := makeFullyRoutableEntry(h, "sub1") // example.com only
	rebuild := func() {
		p.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
			fn(h, entry)
		}, alwaysLookup, usGeoLookup)
	}

	// Only a gstatic-side sample: not admitted.
	rebuild()
	if p.View().Size() != 0 {
		t.Fatal("node without platform-domain sample must not be admitted")
	}

	// Fresh platform-domain sample: admitted.
	entry.LatencyTable.UpdateClassified("example-target.com", 100*time.Millisecond, 30*time.Second, true)
	rebuild()
	if p.View().Size() != 1 {
		t.Fatal("node with fresh platform-domain sample should be admitted")
	}

	// Stale platform-domain sample (beyond the admission window): dropped.
	stale := node.DomainLatencyStats{
		Ewma:        100 * time.Millisecond,
		LastUpdated: time.Now().Add(-3 * time.Hour),
	}
	entry.LatencyTable.LoadEntryClassified("example-target.com", stale, true)
	rebuild()
	if p.View().Size() != 0 {
		t.Fatal("node with stale platform-domain sample must not be admitted")
	}
}

// The admission window closure overrides the default window.
func TestEvaluateNode_AdmissionWindowOverride(t *testing.T) {
	p := NewPlatform("p1", "Test", nil, nil)
	p.LatencyTestURL = "https://api.example-target.com/v1/models"
	// 90-minute window: a sample 2h old is stale, a 30-minute-old sample is fresh.
	p.SetLatencyAdmissionWindow(func() time.Duration { return 90 * time.Minute })

	h := makeHash(`{"type":"ss"}`)
	entry := makeFullyRoutableEntry(h, "sub1")
	entry.LatencyTable.LoadEntryClassified("example-target.com", node.DomainLatencyStats{
		Ewma:        100 * time.Millisecond,
		LastUpdated: time.Now().Add(-2 * time.Hour),
	}, true)

	p.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(h, entry)
	}, alwaysLookup, usGeoLookup)
	if p.View().Size() != 0 {
		t.Fatal("sample older than the injected window must not be admitted")
	}

	entry.LatencyTable.LoadEntryClassified("example-target.com", node.DomainLatencyStats{
		Ewma:        100 * time.Millisecond,
		LastUpdated: time.Now().Add(-30 * time.Minute),
	}, true)
	p.FullRebuild(func(fn func(node.Hash, *node.NodeEntry) bool) {
		fn(h, entry)
	}, alwaysLookup, usGeoLookup)
	if p.View().Size() != 1 {
		t.Fatal("sample within the injected window should be admitted")
	}
}

// The static probe filter must not exclude globally circuit-open nodes:
// they need platform probes to recover their samples (决策⑤).
func TestMatchesProbeStaticFilter_CircuitOpenStillMatches(t *testing.T) {
	p := NewPlatform("p1", "Test", nil, nil)
	h := makeHash(`{"type":"ss"}`)
	entry := makeFullyRoutableEntry(h, "sub1")
	entry.CircuitOpenSince.Store(time.Now().UnixNano())

	if !p.MatchesProbeStaticFilter(entry, alwaysLookup, usGeoLookup) {
		t.Fatal("circuit-open node must still match the static probe filter")
	}
}

func TestMatchesProbeStaticFilter_TagsAndRegions(t *testing.T) {
	p := NewPlatform("p1", "Test", nil, nil)
	h := makeHash(`{"type":"ss"}`)
	entry := makeFullyRoutableEntry(h, "sub1")

	if !p.MatchesProbeStaticFilter(entry, alwaysLookup, usGeoLookup) {
		t.Fatal("unfiltered platform should match")
	}

	filtered := NewPlatformWithTagFilter("p2", "Test2", node.TagFilter{
		// Tag candidates are "<subName>/<tag>" (e.g. "TestSub/fast").
		Any: []*regexp.Regexp{regexp.MustCompile(`fast$`)},
	}, []string{"us"})
	if !filtered.MatchesProbeStaticFilter(entry, alwaysLookup, usGeoLookup) {
		t.Fatal("entry with matching tag and region should match")
	}

	byRegion := NewPlatformWithTagFilter("p3", "Test3", node.TagFilter{}, []string{"!us"})
	if byRegion.MatchesProbeStaticFilter(entry, alwaysLookup, usGeoLookup) {
		t.Fatal("entry excluded by region filter should not match")
	}

	noOutbound := NewPlatform("p4", "Test4", nil, nil)
	bare := node.NewNodeEntry(h, nil, time.Now(), 16)
	bare.AddSubscriptionID("sub1")
	if noOutbound.MatchesProbeStaticFilter(bare, alwaysLookup, usGeoLookup) {
		t.Fatal("node without outbound should not match")
	}
}

func TestPlatformProbeDue(t *testing.T) {
	p := NewPlatform("p1", "Test", nil, nil)
	h := makeHash(`{"type":"ss"}`)
	now := time.Now()
	const interval = time.Hour
	const lookahead = 15 * time.Second

	if !p.PlatformProbeDue(h, now, interval, lookahead) {
		t.Fatal("never-probed node should be due")
	}

	p.StorePlatformProbeAttempt(h, now.Add(-30*time.Minute).UnixNano())
	if p.PlatformProbeDue(h, now, interval, lookahead) {
		t.Fatal("recently probed node should not be due")
	}

	p.StorePlatformProbeAttempt(h, now.Add(-2*time.Hour).UnixNano())
	if !p.PlatformProbeDue(h, now, interval, lookahead) {
		t.Fatal("node past interval should be due")
	}

	// Per-(platform, node) independence.
	other := makeHash(`{"type":"ss2"}`)
	if !p.PlatformProbeDue(other, now, interval, lookahead) {
		t.Fatal("other nodes must have independent due state")
	}
}
