package routing

import (
	"testing"

	"github.com/Resinat/Resin/internal/platform"
)

// Platforms with a probe URL use replacement semantics (决策③): the P2C L2
// authority set is exactly the platform probe domain. All others fall through
// to the global list.
func TestAuthoritiesFor_ReplacementSemantics(t *testing.T) {
	global := []string{"gstatic.com", "cloudflare.com"}
	r := NewRouter(RouterConfig{
		Authorities: func() []string { return append([]string(nil), global...) },
	})

	withoutURL := platform.NewPlatform("p1", "A", nil, nil)
	got := r.authoritiesFor(withoutURL)
	if len(got) != 2 || got[0] != "gstatic.com" || got[1] != "cloudflare.com" {
		t.Fatalf("platform without URL should fall through to global list, got %v", got)
	}

	withURL := platform.NewPlatform("p2", "B", nil, nil)
	withURL.LatencyTestURL = "https://api.example-target.com/v1/models"
	got = r.authoritiesFor(withURL)
	if len(got) != 1 || got[0] != "example-target.com" {
		t.Fatalf("platform with URL should use replacement semantics, got %v", got)
	}

	got = r.authoritiesFor(nil)
	if len(got) != 2 {
		t.Fatalf("nil platform should fall through to global list, got %v", got)
	}
}
