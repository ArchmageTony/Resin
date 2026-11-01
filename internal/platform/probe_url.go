package platform

import (
	"sync"
	"time"

	"github.com/Resinat/Resin/internal/netutil"
	"github.com/Resinat/Resin/internal/node"
)

// defaultPlatformAdmissionWindow is the fallback admission window for
// platform-probe-gated platforms: 2 × the default MaxLatencyTestInterval (1h).
const defaultPlatformAdmissionWindow = 2 * time.Hour

// platformProbeState groups the platform-specific latency-probe support state
// so the official Platform definition carries a single extra field.
type platformProbeState struct {
	mu sync.Mutex
	// attemptNs stores the last platform-probe attempt timestamp (unix ns)
	// per node. Attempt-based, mirroring NodeEntry.LastLatencyProbeAttempt
	// semantics, but scoped per (platform, node) and kept in memory only.
	attemptNs map[node.Hash]int64
	// admissionWindow returns the admission window duration
	// (2 × max_latency_test_interval). Nil falls back to the default.
	admissionWindow func() time.Duration
}

func newPlatformProbeState() platformProbeState {
	return platformProbeState{
		attemptNs: make(map[node.Hash]int64),
	}
}

func (s *platformProbeState) storeAttempt(h node.Hash, ns int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attemptNs[h] = ns
}

func (s *platformProbeState) lastAttempt(h node.Hash) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attemptNs[h]
}

func (s *platformProbeState) setWindow(fn func() time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.admissionWindow = fn
}

func (s *platformProbeState) window() time.Duration {
	s.mu.Lock()
	fn := s.admissionWindow
	s.mu.Unlock()
	if fn != nil {
		if w := fn(); w > 0 {
			return w
		}
	}
	return defaultPlatformAdmissionWindow
}

// SetLatencyAdmissionWindow injects the admission-window closure
// (2 × RuntimeConfig.MaxLatencyTestInterval). Call once at wiring time,
// before the platform is registered and probes start.
func (p *Platform) SetLatencyAdmissionWindow(fn func() time.Duration) {
	p.probe.setWindow(fn)
}

// LatencyProbeEnabled reports whether this platform has a platform-specific
// latency probe URL configured.
func (p *Platform) LatencyProbeEnabled() bool {
	return p.LatencyTestURL != ""
}

// ProbeDomain returns the eTLD+1 domain of the platform probe URL, or "" when
// the platform has no probe URL. Normalization matches
// GlobalNodePool.RecordLatency, so platform probe samples and passive traffic
// samples land in the same domain bucket.
func (p *Platform) ProbeDomain() string {
	if p.LatencyTestURL == "" {
		return ""
	}
	return netutil.ExtractDomain(p.LatencyTestURL)
}

// StorePlatformProbeAttempt records the last platform-probe attempt time.
func (p *Platform) StorePlatformProbeAttempt(h node.Hash, ns int64) {
	p.probe.storeAttempt(h, ns)
}

// LastPlatformProbeAttempt returns the last platform-probe attempt timestamp
// (0 when this node was never probed by this platform).
func (p *Platform) LastPlatformProbeAttempt(h node.Hash) int64 {
	return p.probe.lastAttempt(h)
}

// PlatformProbeDue reports whether the node is due for a platform latency
// probe. Formula mirrors the official latency due check: last attempt +
// interval − lookahead <= now; never-probed nodes are immediately due.
func (p *Platform) PlatformProbeDue(h node.Hash, now time.Time, interval, lookahead time.Duration) bool {
	last := p.probe.lastAttempt(h)
	if last == 0 {
		return true
	}
	return !now.Before(time.Unix(0, last).Add(interval).Add(-lookahead))
}

// MatchesProbeStaticFilter applies the platform's static filters
// (subscription-enabled, outbound-ready, tag rules, region rules) without any
// latency condition. It backs the platform probe scanloop: nodes that were
// never probed by this platform carry no platform-domain sample yet, so
// requiring one would deadlock admission. The global circuit breaker is
// intentionally NOT checked here — circuit-open nodes must keep receiving
// platform probes so they can recover samples and re-qualify.
func (p *Platform) MatchesProbeStaticFilter(
	entry *node.NodeEntry,
	subLookup node.SubLookupFunc,
	geoLookup GeoLookupFunc,
) bool {
	if entry == nil {
		return false
	}
	if entry.IsDisabledBySubscriptions(subLookup) {
		return false
	}
	if entry.Outbound.Load() == nil {
		return false
	}
	if !entry.MatchTagFilter(p.RegexFilters, subLookup) {
		return false
	}
	if len(p.RegionFilters) > 0 {
		if !MatchRegionFilter(entry.GetRegion(geoLookup), p.RegionFilters) {
			return false
		}
	}
	return true
}

// meetsLatencyAdmission implements evaluateNode step 5.
//
// Platforms with a probe URL require a recent successful sample on the
// platform probe domain (window = 2 × max_latency_test_interval, refreshed by
// platform probes and passive traffic to the same eTLD+1 domain). Platforms
// without a probe URL keep the official any-record behavior unchanged.
func (p *Platform) meetsLatencyAdmission(entry *node.NodeEntry) bool {
	if p.LatencyTestURL == "" {
		return entry.HasLatency()
	}
	domain := p.ProbeDomain()
	if domain == "" {
		return entry.HasLatency()
	}
	if entry.LatencyTable == nil {
		return false
	}
	stats, ok := entry.LatencyTable.GetDomainStats(domain)
	if !ok {
		return false
	}
	return time.Since(stats.LastUpdated) <= p.probe.window()
}
