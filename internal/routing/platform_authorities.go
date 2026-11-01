package routing

import (
	"github.com/Resinat/Resin/internal/platform"
)

// authoritiesFor returns the P2C level-2 authority domain set for a platform.
//
// Platforms configured with a latency probe URL use replacement semantics:
// the set is exactly {platform probe domain}, so the platform's target weights
// are not diluted by global authority domains (e.g. gstatic) the platform
// does not care about. All other platforms fall through to the global
// authority list, preserving official behavior.
func (r *Router) authoritiesFor(plat *platform.Platform) []string {
	if plat != nil {
		if domain := plat.ProbeDomain(); domain != "" {
			return []string{domain}
		}
	}
	return r.authorities()
}
