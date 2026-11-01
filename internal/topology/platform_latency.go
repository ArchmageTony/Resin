package topology

import (
	"time"

	"github.com/Resinat/Resin/internal/node"
	"github.com/Resinat/Resin/internal/platform"
)

// RecordPlatformLatency records a platform-specific latency probe attempt for
// the given node. It is the platform-scoped sibling of RecordLatency with two
// deliberate differences (platform probe URL design, 决策①):
//
//   - it never calls RecordResult, so platform probe failures do not feed the
//     global circuit breaker;
//   - it never touches the shared probe-attempt timestamps
//     (LastLatencyProbeAttempt / LastAuthorityLatencyProbeAttempt), so platform
//     probes do not postpone the global latency probe schedule.
//
// Success samples are written into the node's latency-table authority
// partition (resident, never evicted) under the platform probe domain's
// eTLD+1. The platform domain is NOT added to the global latency_authorities
// list, so other platforms' P2C authority comparisons are unaffected.
//
// Every attempt (success or failure) records a per-(platform, node) attempt
// timestamp on the platform and re-evaluates that single node on that single
// platform (NotifyDirty). This drives the admission window in both
// directions: a fresh success re-admits the node, and the first evaluation
// after the last success sample goes stale removes it from the platform view.
func (p *GlobalNodePool) RecordPlatformLatency(hash node.Hash, plat *platform.Platform, latency *time.Duration) {
	if plat == nil {
		return
	}
	domain := plat.ProbeDomain()
	if domain == "" {
		return
	}

	plat.StorePlatformProbeAttempt(hash, time.Now().UnixNano())

	entry, ok := p.nodes.Load(hash)
	if !ok || entry.LatencyTable == nil {
		return
	}

	if latency != nil && *latency > 0 {
		var decayWindow time.Duration
		if p.latencyDecayWindow != nil {
			decayWindow = p.latencyDecayWindow()
		}
		if decayWindow <= 0 {
			decayWindow = 30 * time.Second // default, matches RecordLatency
		}

		wasEmpty, evictedDomain, evicted := entry.LatencyTable.UpdateClassified(domain, *latency, decayWindow, true)

		// Mirror RecordLatency: a table that transitioned from empty to
		// non-empty may satisfy the official any-record admission filter,
		// which applies to all platforms.
		if wasEmpty {
			p.notifyAllPlatformsDirty(hash)
		}
		if p.onNodeLatencyChanged != nil {
			p.onNodeLatencyChanged(hash, domain)
			if evicted {
				p.onNodeLatencyChanged(hash, evictedDomain)
			}
		}
	}

	p.notifyPlatformDirty(plat, hash)
}

// notifyPlatformDirty re-evaluates a single node on a single platform.
// Synchronous, mirroring the per-platform work inside notifyAllPlatformsDirty.
func (p *GlobalNodePool) notifyPlatformDirty(plat *platform.Platform, hash node.Hash) {
	subLookup := p.MakeSubLookup()
	getEntry := func(h node.Hash) (*node.NodeEntry, bool) {
		return p.nodes.Load(h)
	}
	plat.NotifyDirty(hash, getEntry, subLookup, p.geoLookup)
}
