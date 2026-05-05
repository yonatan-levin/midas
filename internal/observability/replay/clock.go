package replay

import (
	"time"

	"go.uber.org/zap"

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

// clockLogger is the package-level zap logger used to emit a WARN line
// when newManifestClock falls back to wall-clock semantics on a malformed
// or empty manifest started_at. Defaults to zap.NewNop so tests that
// don't care about the warning can ignore it. Tests can swap in a real
// logger (or zaptest.NewLogger(t)) to assert the warning fires.
//
// RPL-2n (R3 Stage O.12): the prior silent fallback masked corrupted
// manifests; the operator only knew something was off if a downstream
// diff fired. With the WARN line, the corruption is surfaced at the
// source.
var clockLogger = zap.NewNop()

// newManifestClock parses the manifest's started_at (RFC3339Nano) and
// returns a Clock pinned to that instant. If the input fails to parse
// or is empty, fall back to time.Now() — replay still succeeds (the
// cross-year test would catch silent drift) and we emit a WARN line
// via clockLogger so the corruption is no longer silent. Production
// stamps started_at via time.Now().UTC() in NewManifestBuilder, so a
// parse failure indicates a corrupted bundle rather than a replay-side
// bug; the operator sees the WARN and can re-capture the bundle.
func newManifestClock(startedAt string) valuation.Clock {
	if startedAt == "" {
		clockLogger.Warn("replay: manifest started_at is empty; clock falling back to wall-clock (cross-year regression invariant cannot hold)")
		return valuation.NewWallClock()
	}
	t, err := time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		clockLogger.Warn("replay: manifest started_at malformed; clock falling back to wall-clock",
			zap.String("started_at", startedAt),
			zap.Error(err),
		)
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
