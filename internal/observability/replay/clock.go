package replay

import (
	"time"

	"github.com/midas/dcf-valuation-api/internal/services/valuation"
)

// manifestClock binds *valuation.Service's Clock seam to the captured
// manifest's started_at value. Phase R0's D10 design pinned the engine's
// four wall-clock reads (request-start, FY-period fallback, two
// CalculatedAt stamps in service.go) behind valuation.Clock so a 2026
// bundle replayed in 2027 produces byte-identical output. This binding
// is the replay-side resolution of that pin.
//
// The clock is constant: every Now() returns the same instant. Clock
// callers that need a "before/after this fixed instant" relation can
// still call time.Now elsewhere (this Clock is scoped to *Service only).
type manifestClock struct {
	at time.Time
}

// newManifestClock parses the manifest's started_at (RFC3339Nano) and
// returns a Clock pinned to that instant. If the input fails to parse,
// fall back to time.Now() — replay still succeeds (the cross-year test
// would catch silent drift) and we emit no error because Clock has no
// error channel. Production stamps started_at via time.Now().UTC() in
// NewManifestBuilder, so a parse failure indicates a corrupted bundle
// rather than a replay-side bug.
//
// Empty input is also tolerated for the same reason — a future bundle
// producer that forgets to stamp started_at would otherwise crash replay.
func newManifestClock(startedAt string) valuation.Clock {
	if startedAt == "" {
		return valuation.NewWallClock()
	}
	t, err := time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		return valuation.NewWallClock()
	}
	return &manifestClock{at: t}
}

// Now returns the pinned instant. Time-zone is preserved as-parsed; the
// engine's downstream uses (Year, Format) are TZ-aware so a UTC manifest
// produces UTC-relative output.
func (c *manifestClock) Now() time.Time {
	return c.at
}
