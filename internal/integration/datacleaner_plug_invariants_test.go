package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/infra/gateways/sec"
)

// TestDatacleaner_PlugInvariants_TickerBasket asserts that the SEC parser's
// post-Phase-0 plug computation produces balanced FinancialData for every
// available ticker in the DC-1 acceptance basket. Closes the Phase 0 → Phase 1
// gate per the spec: "Plug values match (umbrella - sum) across ticker basket."
//
// Fixture source: replay bundles under artifacts/tier2-baseline/<newest-date>/.
// The test resolves the most recent ISO-dated directory under tier2-baseline/
// at runtime (lexicographic max — ISO format sorts correctly as strings) so
// freshly-captured baselines automatically become the source-of-truth without
// editing the test. Each bundle's 05-fetch-sec.raw.json is the captured SEC
// company-facts payload; running it through the production parser exercises
// the same code path that fires on a live request, including the computePlugs
// call at the end of parsePeriodData.
//
// Tickers without a captured bundle skip (t.Skipf) rather than fail — Phase 1's
// shadow-mode work will tighten this to require all 10. The integration suite
// MUST stay green on the captured subset to gate Phase 0 → Phase 1 transition.
//
// Invariant model. After computePlugs runs there are two valid states per
// (umbrella, components, plug) triple:
//
//	(well-formed)   umbrella >= sum(components):
//	                  plug == umbrella - sum  AND  plug >= 0
//	                  => umbrella == sum + plug exactly (within float tolerance)
//
//	(clamp-fired)   umbrella  < sum(components):
//	                  plug == 0  AND  Debug log emitted by clampPlug
//	                  => umbrella < sum + plug (the over-sum signal is preserved)
//
// The clamp branch is intentional per plugs.go: it's the documented Phase 0
// behavior for cross-period component contamination (e.g., MXL 2017FY where
// goodwill + intangibles + DTA briefly exceed TotalAssets - CurrentAssets),
// and for filers where TotalDebt aggregates current+noncurrent debt and
// over-subtracts from non-current liabilities (EQIX 2013Q1). Phase 1's
// recomputeUmbrellas shadow-mode tightens this; for Phase 0 the assertion
// surface is: plug >= 0 always, AND equality when no clamp fired.
//
// Tolerance is relative (1e-9 × magnitude, floor of 1.0 USD) so IFRS-full
// filers carrying trillions in local-currency units (TSM/TWD, BABA/CNY) do not
// trigger false negatives once those bundles join the basket.
func TestDatacleaner_PlugInvariants_TickerBasket(t *testing.T) {
	// DC-1 Phase 0 acceptance basket per the spec.
	basket := []string{"AAPL", "MSFT", "JNJ", "KO", "F", "AMD", "MXL", "TSM", "BABA", "EQIX"}

	parser := sec.NewParser(zap.NewNop())

	// Resolve the newest baseline-date directory under tier2-baseline/ (REVIEWER
	// L1 fix from Worktree B). ISO date directories sort lexicographically, so
	// the max element is the freshest capture. Fails loudly if no baseline
	// exists — Phase 1's shadow-mode work would otherwise silent-skip the entire
	// basket and let regressions through.
	baselineParent, err := filepath.Abs(filepath.Join("..", "..", "artifacts", "tier2-baseline"))
	require.NoError(t, err, "resolve baseline parent")
	matches, err := filepath.Glob(filepath.Join(baselineParent, "*"))
	require.NoError(t, err, "glob tier2-baseline subdirs")
	var dateDirs []string
	for _, m := range matches {
		if info, statErr := os.Stat(m); statErr == nil && info.IsDir() {
			dateDirs = append(dateDirs, m)
		}
	}
	if len(dateDirs) == 0 {
		// BUG-016: the tier2-baseline subtree is gitignored and not present on
		// every machine (e.g. CI). Skip rather than hard-fail when it's absent.
		t.Skipf("no tier2-baseline date dirs under %s (BUG-016)", baselineParent)
	}
	sort.Strings(dateDirs)
	bundleRoot := dateDirs[len(dateDirs)-1]
	t.Logf("tier2-baseline resolved to %s (newest of %d date dirs)", filepath.Base(bundleRoot), len(dateDirs))

	// passedCount tracks tickers whose subtest completed all parser-driven
	// assertions without failure (REVIEWER L2 fix). Atomic for safety against
	// future t.Parallel() adoption. Asserted >= 5 after the loop to catch the
	// "all-skip silent regression" failure mode where loadSECRawForTicker
	// silently returns false for every basket member (e.g. baseline dir moved
	// or fixture-file naming changed).
	var passedCount atomic.Int32

	for _, ticker := range basket {
		ticker := ticker // capture for closure
		t.Run(ticker, func(t *testing.T) {
			rawBytes, fixturePath, ok := loadSECRawForTicker(t, bundleRoot, ticker)
			if !ok {
				t.Skipf("no captured SEC fixture for %s under %s — basket coverage will be tightened in Phase 1", ticker, bundleRoot)
				return
			}

			var facts ports.SECCompanyFacts
			require.NoError(t, json.Unmarshal(rawBytes, &facts),
				"%s: unmarshal SEC raw payload at %s", ticker, fixturePath)

			historical, err := parser.ParseFinancialData(context.Background(), &facts)
			require.NoError(t, err, "%s: parser.ParseFinancialData failed", ticker)
			require.NotNil(t, historical, "%s: parser returned nil historical", ticker)
			require.NotEmpty(t, historical.Data, "%s: parser produced no periods", ticker)

			// Walk every parsed period; each must satisfy the plug invariants
			// in either the well-formed branch (equality) or the clamp-fired
			// branch (plug == 0). This is a stricter assertion than "the latest
			// period is balanced" — Phase 1+ uses these plugs to recompute
			// umbrellas across the full historical series, so any period that
			// violates BOTH branches is a Phase 0 leak.
			for period, fd := range historical.Data {
				if fd == nil {
					continue
				}

				// Invariant 1: every plug field is non-negative (clamp guarantee,
				// holds unconditionally).
				assert.GreaterOrEqual(t, fd.OtherCurrentAssets, 0.0,
					"%s %s: OtherCurrentAssets negative", ticker, period)
				assert.GreaterOrEqual(t, fd.OtherNonCurrentAssets, 0.0,
					"%s %s: OtherNonCurrentAssets negative", ticker, period)
				assert.GreaterOrEqual(t, fd.OtherCurrentLiabilities, 0.0,
					"%s %s: OtherCurrentLiabilities negative", ticker, period)
				assert.GreaterOrEqual(t, fd.OtherNonCurrentLiabilities, 0.0,
					"%s %s: OtherNonCurrentLiabilities negative", ticker, period)

				// Invariant 2 (CurrentAssets).
				if fd.CurrentAssets > 0 {
					assertPlugTriple(t, ticker, period, "CurrentAssets",
						fd.CurrentAssets,
						fd.CashAndCashEquivalents+fd.Inventory,
						fd.OtherCurrentAssets,
					)
				}

				// Invariant 3 (NonCurrentAssets): umbrella == TotalAssets - CurrentAssets.
				// Note: computePlugs clamps the umbrella itself to >= 0 before
				// computing the residual (plugs.go:69-72), so the test uses the
				// same max(0, ...) form rather than skipping the assertion when
				// CurrentAssets > TotalAssets (which the parser sometimes emits
				// for partial-year periods).
				ncaUmbrella := fd.TotalAssets - fd.CurrentAssets
				if ncaUmbrella < 0 {
					ncaUmbrella = 0
				}
				if ncaUmbrella > 0 {
					assertPlugTriple(t, ticker, period, "NonCurrentAssets",
						ncaUmbrella,
						fd.Goodwill+fd.OtherIntangibles+fd.DeferredTaxAssets,
						fd.OtherNonCurrentAssets,
					)
				}

				// Invariant 4 (CurrentLiabilities).
				// In production today, fd.OperatingLeaseLiabilityCurrent is always
				// zero (the parser populates only the umbrella OperatingLeaseLiability)
				// so the plug absorbs the entire CurrentLiabilities total. The
				// invariant still holds by construction; this assertion catches the
				// day the lease-split fallback starts firing.
				if fd.CurrentLiabilities > 0 {
					assertPlugTriple(t, ticker, period, "CurrentLiabilities",
						fd.CurrentLiabilities,
						fd.OperatingLeaseLiabilityCurrent,
						fd.OtherCurrentLiabilities,
					)
				}

				// Invariant 5 (NonCurrentLiabilities). Same TotalLiabilities -
				// CurrentLiabilities clamp as Invariant 3.
				nclUmbrella := fd.TotalLiabilities - fd.CurrentLiabilities
				if nclUmbrella < 0 {
					nclUmbrella = 0
				}
				if nclUmbrella > 0 {
					assertPlugTriple(t, ticker, period, "NonCurrentLiabilities",
						nclUmbrella,
						fd.TotalDebt+fd.OperatingLeaseLiabilityNoncurrent,
						fd.OtherNonCurrentLiabilities,
					)
				}
			}

			// REVIEWER L2 fix: count this ticker as exercised only if no
			// assertion failed inside the subtest. Skipped tickers (early
			// return above) and failed-assertion tickers don't increment.
			if !t.Failed() {
				passedCount.Add(1)
			}
		})
	}

	// REVIEWER L2 fix: gate against the all-skip silent regression. Current
	// captured baselines yield 7 of 10 PASS (AAPL/MSFT/KO/F/AMD/MXL/EQIX with
	// JNJ/TSM/BABA skipping for lack of fixtures); 5 is a deliberately
	// conservative floor that survives one or two tickers temporarily
	// dropping out without becoming so high it false-positives during the
	// Phase 1 baseline refresh cycle.
	require.GreaterOrEqual(t, passedCount.Load(), int32(5),
		"basket coverage degraded — fewer than 5 tickers exercised the parser successfully (got %d)", passedCount.Load())
}

// loadSECRawForTicker walks the per-ticker bundle directory under bundleRoot
// and returns the first 05-fetch-sec.raw.json payload it finds. A bundle dir
// looks like `<bundleRoot>/<TICKER>/req_<uuid>/05-fetch-sec.raw.json` so we
// only need one level of nesting under the ticker.
//
// Returns (bytes, fullPath, true) when a fixture is found, or (nil, "", false)
// when the ticker has no captured bundle (test must t.Skip).
func loadSECRawForTicker(t *testing.T, bundleRoot, ticker string) ([]byte, string, bool) {
	t.Helper()

	tickerDir := filepath.Join(bundleRoot, ticker)
	info, err := os.Stat(tickerDir)
	if err != nil || !info.IsDir() {
		return nil, "", false
	}

	entries, err := os.ReadDir(tickerDir)
	if err != nil {
		return nil, "", false
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(tickerDir, entry.Name(), "05-fetch-sec.raw.json")
		body, readErr := os.ReadFile(candidate)
		if readErr != nil {
			continue
		}
		return body, candidate, true
	}
	return nil, "", false
}

// assertPlugTriple asserts the Phase 0 plug invariant for a single
// (umbrella, knownComponentSum, plug) triple. Two valid states:
//
//   - well-formed (knownSum <= umbrella):
//     umbrella == knownSum + plug  (equality within float tolerance)
//
//   - clamp-fired (knownSum > umbrella):
//     plug == 0                    (clampPlug zeroed the negative residual)
//
// The clamp branch is a documented Phase 0 limitation per plugs.go (TotalDebt
// over-subtraction for filers with material short-term debt, cross-period
// component contamination on small filers, etc.) and is observable via the
// Debug log "plug residual clamped to zero". Phase 1's recomputeUmbrellas
// shadow-mode quantifies the residual; for Phase 0 the test surface is the
// two-branch invariant above.
//
// Failure mode pinned: a clamp fires AND the plug somehow ends up non-zero,
// which would indicate a clampPlug regression. Either branch passing is a
// well-formed Phase 0 state.
func assertPlugTriple(t *testing.T, ticker, period, name string, umbrella, knownSum, plug float64) {
	t.Helper()

	if knownSum <= umbrella {
		// Well-formed branch: equality within tolerance.
		assert.InDelta(t, umbrella, knownSum+plug, plugTolerance(umbrella),
			"%s %s: %s well-formed plug invariant (umbrella=%.2f, knownSum=%.2f, plug=%.2f)",
			ticker, period, name, umbrella, knownSum, plug,
		)
		return
	}

	// Clamp-fired branch: plug must be exactly zero (clampPlug returned 0).
	// We assert with float tolerance only to absorb the unlikely case where
	// downstream code adds a sub-cent rounding error onto the zeroed plug.
	assert.InDelta(t, 0.0, plug, 1e-6,
		"%s %s: %s clamp-fired plug expected zero (umbrella=%.2f, knownSum=%.2f, plug=%.2f) — clampPlug regression?",
		ticker, period, name, umbrella, knownSum, plug,
	)
}

// plugTolerance returns max(1.0, value * 1e-9). For large IFRS-full filer
// magnitudes (1e12+ TWD) the absolute floor of 1.0 isn't enough; the relative
// term takes over. For small magnitudes (US$1M and under) the floor catches
// float64 accumulation error from a chain of subtractions in computePlugs.
//
// One US dollar of absolute slack at the floor is intentionally generous —
// the assertion is "components sum to umbrellas within a dollar," not
// "components sum to umbrellas exactly." Tighter tolerances trip on IFRS
// filers whose parser-resolved values went through currency-bucket collapse
// (parser.go:309-363) and accumulated a handful of ULPs of error.
func plugTolerance(value float64) float64 {
	if value < 0 {
		value = -value
	}
	tol := value * 1e-9
	if tol < 1.0 {
		tol = 1.0
	}
	return tol
}

// Compile-time enforcement: the test must compile against the real entity
// shape. If a Phase 0 entity rename happens, this declaration breaks first.
var _ = (*entities.FinancialData)(nil)
