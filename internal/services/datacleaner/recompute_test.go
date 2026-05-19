package datacleaner

import (
	"context"
	"reflect"
	"testing"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
)

// freshObserver builds an isolated (recorded, ctx) pair where ctx carries an
// observer-backed *zap.Logger. Each test that wants to assert on WARN
// structure calls this helper, so each test gets its own log buffer.
func freshObserver(t *testing.T) (*observer.ObservedLogs, context.Context) {
	t.Helper()
	core, recorded := observer.New(zap.WarnLevel)
	ctx := logctx.Inject(context.Background(), zap.New(core))
	return recorded, ctx
}

// ---------------------------------------------------------------------------
// TestRecomputeUmbrellas_NoMutation
//
// Critical invariant for DC-1 Phase 1: recomputeUmbrellas MUST NOT mutate
// *entities.FinancialData. This test takes a pre-call deep copy of a cleaner-
// like (mutated) fd, runs recomputeUmbrellas, and asserts the struct is
// bit-for-bit identical via reflect.DeepEqual.
// ---------------------------------------------------------------------------
func TestRecomputeUmbrellas_NoMutation(t *testing.T) {
	tests := []struct {
		name string
		fd   *entities.FinancialData
	}{
		{
			name: "well-formed-aapl-shape",
			fd: &entities.FinancialData{
				Ticker:                            "AAPL",
				CIK:                               "0000320193",
				FilingPeriod:                      "2023FY",
				TotalAssets:                       352_755.0,
				CurrentAssets:                     143_566.0,
				CurrentLiabilities:                145_308.0,
				TotalLiabilities:                  290_437.0,
				CashAndCashEquivalents:            29_965.0,
				Inventory:                         6_331.0,
				TotalDebt:                         111_088.0,
				OperatingLeaseLiabilityCurrent:    1_410.0,
				OperatingLeaseLiabilityNoncurrent: 10_550.0,
				// Plugs already filled by Phase 0's computePlugs:
				OtherCurrentAssets:         107_270.0,
				OtherNonCurrentAssets:      209_189.0,
				OtherCurrentLiabilities:    143_898.0,
				OtherNonCurrentLiabilities: 23_491.0,
			},
		},
		{
			name: "cleaner-mutated-mxl-shape", // umbrella mutated, components & plug stale → divergence path
			fd: &entities.FinancialData{
				Ticker:                 "MXL",
				CIK:                    "0001288469",
				FilingPeriod:           "2026Q1",
				TotalAssets:            387_402_067.0,
				CurrentAssets:          249_450_000.0,
				CashAndCashEquivalents: 150_000_000.0,
				Inventory:              51_503_400.0,
				Goodwill:               0.0,
				OtherIntangibles:       30_000_000.0,
				OtherCurrentAssets:     47_946_600.0,
				OtherNonCurrentAssets:  200_000_000.0,
			},
		},
		{
			name: "all-zero-fd",
			fd: &entities.FinancialData{
				Ticker:       "ZERO",
				CIK:          "0000000000",
				FilingPeriod: "2024Q1",
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Deep-copy via reflect: copy the dereferenced struct value.
			pre := *tc.fd

			// Run the shadow-mode shim. The third call uses an observer-backed
			// context to confirm even with logger activity, no field write
			// sneaks through.
			_, ctx := freshObserver(t)
			recomputeUmbrellas(ctx, tc.fd)

			// Bit-for-bit equality on the struct value.
			require.True(t, reflect.DeepEqual(pre, *tc.fd),
				"recomputeUmbrellas mutated fd — pre vs post differ.\npre=%#v\npost=%#v", pre, *tc.fd)
		})
	}
}

// ---------------------------------------------------------------------------
// TestRecomputeUmbrellas_NilFD_Safe
//
// Defensive — a nil *FinancialData must not panic; the function early-returns.
// ---------------------------------------------------------------------------
func TestRecomputeUmbrellas_NilFD_Safe(t *testing.T) {
	_, ctx := freshObserver(t)
	assert.NotPanics(t, func() {
		recomputeUmbrellas(ctx, nil)
	}, "recomputeUmbrellas(ctx, nil) must not panic")
}

// ---------------------------------------------------------------------------
// TestRecomputeUmbrellas_NilCtx_Safe
//
// Defensive — a nil context must not panic; logctx.From(nil) returns the
// nop logger so the WARN path is silent but safe.
// ---------------------------------------------------------------------------
func TestRecomputeUmbrellas_NilCtx_Safe(t *testing.T) {
	fd := &entities.FinancialData{
		Ticker:                 "TEST",
		FilingPeriod:           "2024Q1",
		TotalAssets:            100.0,
		CurrentAssets:          50.0,
		CashAndCashEquivalents: 25.0,
		// Components don't sum to umbrella → divergence path would fire if logger present.
	}

	assert.NotPanics(t, func() {
		// nolint:staticcheck // explicitly passing nil to exercise the nil-ctx safe path
		recomputeUmbrellas(nil, fd)
	}, "recomputeUmbrellas(nil, fd) must not panic")
}

// ---------------------------------------------------------------------------
// TestRecomputeUmbrellas_EmitsWarnOnDivergence
//
// Synthetic case: cleaner mutated TotalAssets - 100 (umbrella drifted) but
// components stay intact. Asserts EXACTLY one WARN per diverged umbrella with
// the correct structured fields.
// ---------------------------------------------------------------------------
func TestRecomputeUmbrellas_EmitsWarnOnDivergence(t *testing.T) {
	// Start with a well-formed shape (no divergence).
	fd := &entities.FinancialData{
		Ticker:                            "AAPL",
		CIK:                               "0000320193",
		FilingPeriod:                      "2023FY",
		TotalAssets:                       352_755.0,
		CurrentAssets:                     143_566.0,
		CurrentLiabilities:                145_308.0,
		TotalLiabilities:                  290_437.0,
		CashAndCashEquivalents:            29_965.0,
		Inventory:                         6_331.0,
		TotalDebt:                         111_088.0,
		OperatingLeaseLiabilityCurrent:    1_410.0,
		OperatingLeaseLiabilityNoncurrent: 10_550.0,
		OtherCurrentAssets:                107_270.0,
		OtherNonCurrentAssets:             209_189.0,
		OtherCurrentLiabilities:           143_898.0,
		OtherNonCurrentLiabilities:        23_491.0,
	}

	// Simulate the cleaner's umbrella-mutation pattern: subtract 100 from
	// TotalAssets without touching the components or plug. This is exactly
	// what assets.go:69,157,232,308 does today.
	fd.TotalAssets -= 100.0

	recorded, ctx := freshObserver(t)
	recomputeUmbrellas(ctx, fd)

	entries := recorded.FilterMessage("recomputeUmbrellas: umbrella divergence").All()
	require.Len(t, entries, 1, "exactly one WARN expected for the TotalAssets-only divergence")

	ctxMap := entries[0].ContextMap()
	assert.Equal(t, "AAPL", ctxMap["ticker"])
	assert.Equal(t, "2023FY", ctxMap["period"])
	assert.Equal(t, "0000320193", ctxMap["cik"])
	assert.Equal(t, "TotalAssets", ctxMap["umbrella"])
	assert.Equal(t, "DC-1-P1-shadow", ctxMap["phase"])
	// reported = umbrella after cleaner mutation = 352755 - 100 = 352655
	assert.InDelta(t, 352_655.0, ctxMap["reported"].(float64), 0.01)
	// recomputed = sum(components) + plug = 352755 (the original, undisturbed umbrella)
	assert.InDelta(t, 352_755.0, ctxMap["recomputed"].(float64), 0.01)
	// delta = recomputed - reported = +100
	assert.InDelta(t, 100.0, ctxMap["delta"].(float64), 0.01)
	// plug = fd.OtherNonCurrentAssets (the non-current-assets plug used for TotalAssets)
	assert.InDelta(t, 209_189.0, ctxMap["plug"].(float64), 0.01)
	// clamp_suspected: recomputed > reported AND plug == 0 → false here because plug != 0.
	assert.Equal(t, false, ctxMap["clamp_suspected"])
}

// ---------------------------------------------------------------------------
// TestRecomputeUmbrellas_ClampSuspectedFlag
//
// Plug exactly zero AND sum(components) > umbrella reproduces the Phase 0
// clamp fingerprint (AMD 2023FY / KO 2023FY in the live baseline date
// range; MXL 2017FY / EQIX 2013Q1 are historical examples cited in the
// Phase 0 closeout but fall outside the artifacts/tier2-baseline/2026-05-15/
// window). The WARN must set clamp_suspected: true so Phase 2's analysis
// can filter these out.
// ---------------------------------------------------------------------------
func TestRecomputeUmbrellas_ClampSuspectedFlag(t *testing.T) {
	// Synthetic clamp-fired shape: CurrentAssets umbrella = 100, but
	// CashAndCashEquivalents + Inventory = 130 > 100. computePlugs would have
	// clamped OtherCurrentAssets to 0 here.
	fd := &entities.FinancialData{
		Ticker:                 "CLAMP",
		CIK:                    "0001234567",
		FilingPeriod:           "2025Q2",
		CurrentAssets:          100.0,
		CashAndCashEquivalents: 80.0,
		Inventory:              50.0,
		OtherCurrentAssets:     0.0, // clamped
	}

	recorded, ctx := freshObserver(t)
	recomputeUmbrellas(ctx, fd)

	entries := recorded.FilterMessage("recomputeUmbrellas: umbrella divergence").All()
	require.GreaterOrEqual(t, len(entries), 1, "at least one WARN expected for the clamped CurrentAssets")

	// Find the CurrentAssets entry.
	var caEntry *observer.LoggedEntry
	for i := range entries {
		ctxMap := entries[i].ContextMap()
		if ctxMap["umbrella"] == "CurrentAssets" {
			caEntry = &entries[i]
			break
		}
	}
	require.NotNil(t, caEntry, "expected a WARN for CurrentAssets umbrella")

	ctxMap := caEntry.ContextMap()
	assert.Equal(t, "CurrentAssets", ctxMap["umbrella"])
	// reported = 100; recomputed = 80 + 50 + 0 = 130; delta = +30; plug = 0 → clamp_suspected=true.
	assert.InDelta(t, 100.0, ctxMap["reported"].(float64), 0.01)
	assert.InDelta(t, 130.0, ctxMap["recomputed"].(float64), 0.01)
	assert.InDelta(t, 30.0, ctxMap["delta"].(float64), 0.01)
	assert.InDelta(t, 0.0, ctxMap["plug"].(float64), 0.01)
	assert.Equal(t, true, ctxMap["clamp_suspected"],
		"clamp_suspected MUST be true when recomputed > reported AND plug == 0")
}

// ---------------------------------------------------------------------------
// TestRecomputeUmbrellas_Property_WellFormedNoDivergence
//
// Load-bearing property for DC-1 Phase 1: for any well-formed FinancialData
// where the parser's plug invariant `umbrella == sum(components) + plug`
// holds with `plug >= 0`, recomputeUmbrellas emits ZERO WARN lines.
//
// 4 properties × 200 iterations, pinned seed 20260517 (matches Phase 0's
// 20260516 cadence; deterministic reproduction across CI). Generator shape
// mirrors plugs_test.go::TestComputePlugs_Property_ComponentsSumToUmbrellas
// except instead of running computePlugs to fill the plugs, the test
// pre-stamps the well-formed plug values onto fd.
// ---------------------------------------------------------------------------
func TestRecomputeUmbrellas_Property_WellFormedNoDivergence(t *testing.T) {
	params := gopter.DefaultTestParameters()
	params.Rng.Seed(20260517)
	params.MinSuccessfulTests = 200

	properties := gopter.NewProperties(params)

	// Property 1: well-formed CurrentAssets emits no WARN.
	properties.Property("well-formed CurrentAssets → no divergence WARN", prop.ForAll(
		func(cash, inventory, otherCA float64) bool {
			fd := &entities.FinancialData{
				Ticker:                 "FUZZ",
				CIK:                    "0",
				FilingPeriod:           "2024FY",
				CashAndCashEquivalents: cash,
				Inventory:              inventory,
				OtherCurrentAssets:     otherCA,
				CurrentAssets:          cash + inventory + otherCA, // invariant holds
			}
			core, recorded := observer.New(zap.WarnLevel)
			ctx := logctx.Inject(context.Background(), zap.New(core))
			recomputeUmbrellas(ctx, fd)
			// Filter to CurrentAssets divergence only — the other umbrellas
			// are all zero on both sides here so they also shouldn't fire,
			// but we only assert the property's umbrella to keep the
			// counterexample readable on failure.
			for _, e := range recorded.FilterMessage("recomputeUmbrellas: umbrella divergence").All() {
				if e.ContextMap()["umbrella"] == "CurrentAssets" {
					return false
				}
			}
			return true
		},
		gen.Float64Range(0, 1e12),
		gen.Float64Range(0, 1e12),
		gen.Float64Range(0, 1e12),
	))

	// Property 2: well-formed TotalAssets emits no WARN.
	properties.Property("well-formed TotalAssets → no divergence WARN", prop.ForAll(
		func(currentAssets, goodwill, intangibles, dta, otherNCA float64) bool {
			fd := &entities.FinancialData{
				Ticker:                "FUZZ",
				CIK:                   "0",
				FilingPeriod:          "2024FY",
				CurrentAssets:         currentAssets,
				Goodwill:              goodwill,
				OtherIntangibles:      intangibles,
				DeferredTaxAssets:     dta,
				OtherNonCurrentAssets: otherNCA,
				TotalAssets:           currentAssets + goodwill + intangibles + dta + otherNCA, // invariant holds
			}
			core, recorded := observer.New(zap.WarnLevel)
			ctx := logctx.Inject(context.Background(), zap.New(core))
			recomputeUmbrellas(ctx, fd)
			for _, e := range recorded.FilterMessage("recomputeUmbrellas: umbrella divergence").All() {
				if e.ContextMap()["umbrella"] == "TotalAssets" {
					return false
				}
			}
			return true
		},
		gen.Float64Range(0, 1e12),
		gen.Float64Range(0, 1e12),
		gen.Float64Range(0, 1e12),
		gen.Float64Range(0, 1e12),
		gen.Float64Range(0, 1e12),
	))

	// Property 3: well-formed CurrentLiabilities emits no WARN.
	properties.Property("well-formed CurrentLiabilities → no divergence WARN", prop.ForAll(
		func(opLeaseCurrent, otherCL float64) bool {
			fd := &entities.FinancialData{
				Ticker:                         "FUZZ",
				CIK:                            "0",
				FilingPeriod:                   "2024FY",
				OperatingLeaseLiabilityCurrent: opLeaseCurrent,
				OtherCurrentLiabilities:        otherCL,
				CurrentLiabilities:             opLeaseCurrent + otherCL, // invariant holds
			}
			core, recorded := observer.New(zap.WarnLevel)
			ctx := logctx.Inject(context.Background(), zap.New(core))
			recomputeUmbrellas(ctx, fd)
			for _, e := range recorded.FilterMessage("recomputeUmbrellas: umbrella divergence").All() {
				if e.ContextMap()["umbrella"] == "CurrentLiabilities" {
					return false
				}
			}
			return true
		},
		gen.Float64Range(0, 1e12),
		gen.Float64Range(0, 1e12),
	))

	// Property 4: well-formed TotalLiabilities emits no WARN.
	properties.Property("well-formed TotalLiabilities → no divergence WARN", prop.ForAll(
		func(currentLiab, totalDebt, opLeaseNoncurrent, otherNCL float64) bool {
			fd := &entities.FinancialData{
				Ticker:                            "FUZZ",
				CIK:                               "0",
				FilingPeriod:                      "2024FY",
				CurrentLiabilities:                currentLiab,
				TotalDebt:                         totalDebt,
				OperatingLeaseLiabilityNoncurrent: opLeaseNoncurrent,
				OtherNonCurrentLiabilities:        otherNCL,
				TotalLiabilities:                  currentLiab + totalDebt + opLeaseNoncurrent + otherNCL, // invariant holds
			}
			core, recorded := observer.New(zap.WarnLevel)
			ctx := logctx.Inject(context.Background(), zap.New(core))
			recomputeUmbrellas(ctx, fd)
			for _, e := range recorded.FilterMessage("recomputeUmbrellas: umbrella divergence").All() {
				if e.ContextMap()["umbrella"] == "TotalLiabilities" {
					return false
				}
			}
			return true
		},
		gen.Float64Range(0, 1e12),
		gen.Float64Range(0, 1e12),
		gen.Float64Range(0, 1e12),
		gen.Float64Range(0, 1e12),
	))

	properties.TestingRun(t)
}

// Compile-time enforcement: this test file must compile against the real
// entity shape. If a future entity rename happens, this declaration breaks
// first (matches the pattern in datacleaner_plug_invariants_test.go).
var _ = (*entities.FinancialData)(nil)
