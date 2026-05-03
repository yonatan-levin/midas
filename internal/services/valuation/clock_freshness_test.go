package valuation

import (
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// fixedClock is a Clock implementation that returns the same time every call.
// Used to pin DataFreshnessScore tests so they are independent of wall-clock
// drift between writing the test and running it. See item #5 of the
// observability-replay R1 follow-up dispatch.
type fixedClock struct {
	t time.Time
}

func (f fixedClock) Now() time.Time { return f.t }

// TestService_calculateDataFreshnessScore_UsesInjectedClock pins the spec
// invariant from §5 D7: every wall-clock read inside the valuation service
// must route through s.clock so a replay (which binds Clock to
// manifest.started_at) produces byte-identical freshness scores to the
// original capture.
//
// This test would have caught the latent leak at service.go:1414, 1430
// (calculateDataFreshnessScore reading time.Since directly) because the
// fixedClock would be ignored — the function would still read the wall
// clock and the score would float depending on when the test ran.
//
// Setup: pick T0 = 2026-01-01T00:00:00Z. With financial.AsOf = T0 - 30d
// (exactly 30 days, which is NOT > 30 days), market and macro fresh, the
// score should be 100 (no penalty branches fire).
func TestService_calculateDataFreshnessScore_UsesInjectedClock(t *testing.T) {
	service, _, _, _, _, _ := createTestService()

	// Pin the clock to a fixed instant. If the implementation routes through
	// s.clock as required, this score is deterministic; if it reads
	// time.Now() (the latent leak), the score depends on real wall-clock.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	service.SetClock(fixedClock{t: t0})

	// 30 days exactly is the boundary — the implementation uses strict `>`,
	// so 30d is NOT a penalty. Use exactly 30 * 24h to land at the boundary.
	thirtyDaysAgo := t0.Add(-30 * 24 * time.Hour)
	financial := &entities.FinancialData{AsOf: thirtyDaysAgo}
	market := &entities.MarketData{AsOf: t0}
	macro := &entities.MacroData{AsOf: t0}

	got := service.calculateDataFreshnessScore(financial, market, macro)
	if got != 100 {
		t.Fatalf("calculateDataFreshnessScore = %d; want 100 with t0-30d financial AsOf and t0 market/macro AsOf (the function must route time-of-day reads through the injected Clock)", got)
	}
}

// TestService_calculateDataFreshnessScore_DeterministicAcrossWallClock pins
// the cross-year-replay regression intent: with the Clock bound to a fixed
// past instant, the function must produce a stable score regardless of
// when the test runs.
//
// Strategy: if the implementation still uses time.Since (which reads the
// real wall clock), the absolute age of the bundle's financial.AsOf would
// drift by years between captures, scoring would land in the > 90d branch
// (-30) instead of the 30-90d branch (-15), and the score would change.
func TestService_calculateDataFreshnessScore_DeterministicAcrossWallClock(t *testing.T) {
	service, _, _, _, _, _ := createTestService()

	// Pick a clearly historical instant: if Clock is honored, the
	// financial.AsOf 45 days BEFORE this t0 is in the 30-90d band (-15),
	// scoring 85. If the implementation reads time.Now(), the AsOf would be
	// many years old and score (100 - 30 - 20 - 20) = 30.
	t0 := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	service.SetClock(fixedClock{t: t0})

	financial := &entities.FinancialData{AsOf: t0.Add(-45 * 24 * time.Hour)} // 45d → -15
	market := &entities.MarketData{AsOf: t0}
	macro := &entities.MacroData{AsOf: t0}

	got := service.calculateDataFreshnessScore(financial, market, macro)
	if got != 85 {
		t.Fatalf("calculateDataFreshnessScore = %d; want 85 (100 - 15 for 45-day-old financial data) — Clock injection must apply to financial-age and macro-age reads", got)
	}
}

// TestService_calculateDataFreshnessScore_MacroAgeUsesInjectedClock pins
// the second of the two leaks (line 1430). Verifies macro-age computation
// also flows through the Clock seam.
func TestService_calculateDataFreshnessScore_MacroAgeUsesInjectedClock(t *testing.T) {
	service, _, _, _, _, _ := createTestService()

	t0 := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	service.SetClock(fixedClock{t: t0})

	// Only macro is stale (45 days → > 30, so -20).
	financial := &entities.FinancialData{AsOf: t0}
	market := &entities.MarketData{AsOf: t0}
	macro := &entities.MacroData{AsOf: t0.Add(-45 * 24 * time.Hour)}

	got := service.calculateDataFreshnessScore(financial, market, macro)
	if got != 80 {
		t.Fatalf("calculateDataFreshnessScore = %d; want 80 (macro 45d > 30d → -20) — macro-age read must route through the Clock seam", got)
	}
}
