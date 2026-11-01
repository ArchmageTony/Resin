package probe

import (
	"log"
	"time"

	"github.com/Resinat/Resin/internal/node"
	"github.com/Resinat/Resin/internal/platform"
)

// scanPlatformLatency iterates every platform configured with a probe URL and
// enqueues platform latency probes for due nodes. It mirrors the official
// scanLatency cadence and skip rules: subscription-disabled and outbound-less
// nodes are skipped, but globally circuit-open nodes are NOT skipped — they
// must keep receiving platform probes so their platform samples can recover
// and they can re-qualify for the platform view.
//
// The candidate set is the global pool filtered by the platform's static rules
// (enabled/outbound/tags/regions) with no latency condition — nodes without a
// platform-domain sample must still be probed (chicken-and-egg).
func (m *ProbeManager) scanPlatformLatency() {
	now := time.Now()
	interval := 1 * time.Hour // default MaxLatencyTestInterval
	if m.maxLatencyTestInterval != nil {
		interval = m.maxLatencyTestInterval()
	}
	lookahead := 15 * time.Second
	subLookup := m.pool.MakeSubLookup()
	geoLookup := m.pool.GeoLookup()

	m.pool.RangePlatforms(func(plat *platform.Platform) bool {
		if !plat.LatencyProbeEnabled() {
			return true
		}

		m.pool.Range(func(h node.Hash, entry *node.NodeEntry) bool {
			select {
			case <-m.stopCh:
				return false
			default:
			}

			if !plat.MatchesProbeStaticFilter(entry, subLookup, geoLookup) {
				return true
			}

			if !plat.PlatformProbeDue(h, now, interval, lookahead) {
				return true
			}

			m.enqueueProbe(h, probeTaskKindPlatformLatency, probePriorityNormal, plat.ID)

			return true
		})

		return true
	})
}

// probePlatformLatency executes one platform latency probe. Write-back goes
// through RecordPlatformLatency only — never RecordResult, never the shared
// latency-probe timestamps (platform probe URL design, 决策①).
func (m *ProbeManager) probePlatformLatency(hash node.Hash, platformID string) {
	if m.fetcher == nil {
		return
	}

	plat, ok := m.pool.GetPlatform(platformID)
	if !ok || plat == nil || !plat.LatencyProbeEnabled() {
		return
	}

	entry, ok := m.pool.GetEntry(hash)
	if !ok || entry.Outbound.Load() == nil {
		return
	}
	if !plat.MatchesProbeStaticFilter(entry, m.pool.MakeSubLookup(), m.pool.GeoLookup()) {
		return
	}

	// Always record the probe attempt (success or failure).
	if m.onProbeEvent != nil {
		m.onProbeEvent("latency")
	}

	if err := m.performPlatformLatencyProbe(hash, plat); err != nil {
		log.Printf("[probe] platform latency probe failed for %s (platform %s): %v", hash.Hex(), platformID, err)
		return
	}
}

// performPlatformLatencyProbe fetches the platform probe URL through the node
// and writes the result back through the platform-scoped record path.
func (m *ProbeManager) performPlatformLatencyProbe(hash node.Hash, plat *platform.Platform) error {
	_, latency, err := m.fetcher(hash, plat.LatencyTestURL)
	if err != nil {
		m.pool.RecordPlatformLatency(hash, plat, nil)
		return err
	}

	m.pool.RecordPlatformLatency(hash, plat, &latency)
	return nil
}
