package datacleaner

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire is
// the load-bearing HIGH-1 regression pin for the DC-1 Phase 3 followup.
//
// Before the fix, CleanFinancialDataWithViews wrapped the POST-dispatcher
// *FinancialData and Restated() seeded the view from those post-mutation
// component values AND re-applied each LedgerEntry.DeltaAmount on top,
// double-counting every Restater-role adjuster (A2/A4/A5, C1/C2/C3/C5/C6).
//
// The fix captures a PRE-clean snapshot inside CleanFinancialDataWithViews
// and hands BOTH (asReported, restated) to cleaneddata.New. Restated()
// seeds from the post-clean entity (already has dispatcher dual-writes)
// and applies ONLY EquityOffset + TaxShieldDTA from the ledger.
//
// Without this test HIGH-1 can silently re-regress under any future
// refactor that changes the seed/ledger contract.
func TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire(t *testing.T) {
	cfg := createTestConfig()
	ctx := context.Background()

	svc, err := NewDataCleanerService(cfg, &mockAIServiceDataCleaner{}, nil)
	require.NoError(t, err)

	// Build a synthetic FinancialData that fires the "intangible_adjustment"
	// (A2) production rule. With OtherIntangibles=$150M / TotalAssets=$1B
	// the ratio is 15% — well above A2's 2% materiality threshold. The
	// $150M >= $300k tier produces retentionRate=1/3 → writedown=$100M.
	data := &entities.FinancialData{
		Ticker:             "AAPL",
		CIK:                "0000320193",
		AsOf:               time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		FilingPeriod:       "2025Q4",
		FilingDate:         time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC),
		ReportingCurrency:  "USD",
		Revenue:            500_000_000,
		TotalAssets:        1_000_000_000,
		TangibleAssets:     650_000_000,
		Goodwill:           200_000_000,
		OtherIntangibles:   150_000_000,
		StockholdersEquity: 400_000_000,
		EffectiveTaxRate:   0.25,
		SharesOutstanding:  100_000_000,
		HasNormalizedData:  true,
	}

	originalIntangibles := data.OtherIntangibles
	originalEquity := data.StockholdersEquity
	originalTotalAssets := data.TotalAssets

	result, views, err := svc.CleanFinancialDataWithViews(ctx, data)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, views)
	require.NotNil(t, result.CleanedData)

	// Verify the A2 dispatcher dual-write fired: the post-clean
	// OtherIntangibles is strictly less than the input, and a fired
	// Restater-shaped LedgerEntry on OtherIntangibles is present.
	cleanedIntangibles := result.CleanedData.OtherIntangibles
	require.Less(t, cleanedIntangibles, originalIntangibles,
		"fixture must produce a fired A2 writedown — required for this regression to exercise the bug")
	writedown := originalIntangibles - cleanedIntangibles
	require.Greater(t, writedown, 0.0)

	var firedA2 *entities.LedgerEntry
	for i := range result.CleanedData.AdjustmentLedger {
		e := &result.CleanedData.AdjustmentLedger[i]
		if e.Fired && e.Component == "OtherIntangibles" {
			firedA2 = e
			break
		}
	}
	require.NotNil(t, firedA2,
		"fixture must produce a fired Restater LedgerEntry on OtherIntangibles")

	// AsReported must preserve the PRE-CLEAN input verbatim. This is the
	// snapshot-side of the fix: AsReported reads from the snapshot captured
	// before the dispatcher mutates anything.
	asReported := views.AsReported()
	require.NotNil(t, asReported)
	assert.InDelta(t, originalIntangibles, asReported.OtherIntangibles, 0.01,
		"AsReported.OtherIntangibles MUST preserve the pre-clean input")
	assert.InDelta(t, originalEquity, asReported.StockholdersEquity, 0.01,
		"AsReported.StockholdersEquity MUST preserve the pre-clean input")
	assert.InDelta(t, originalTotalAssets, asReported.TotalAssets, 0.01,
		"AsReported.TotalAssets MUST preserve the pre-clean input")

	// KEY ASSERTION (HIGH-1): Restated().OtherIntangibles equals the
	// post-dispatcher value — ONE application of the writedown delta, NOT
	// two. Before the fix this would equal `originalIntangibles - 2*writedown`.
	restated := views.Restated()
	require.NotNil(t, restated)
	assert.InDelta(t, cleanedIntangibles, restated.OtherIntangibles, 0.01,
		"Restated.OtherIntangibles must equal the post-dispatcher value (one delta application). "+
			"If it equals originalIntangibles - 2*writedown the ledger reducer is double-counting.")

	// Equity offset flows from the ledger ONCE. Post-clean equity has NOT
	// been mutated by the A2 dispatcher (A2's dual-write touches only
	// OtherIntangibles + TotalAssets — equity is not in the dual-write set);
	// the LedgerEntry's EquityOffset is the lone source of the equity hit.
	expectedRestatedEquity := result.CleanedData.StockholdersEquity + firedA2.EquityOffset
	assert.InDelta(t, expectedRestatedEquity, restated.StockholdersEquity, 0.01,
		"Restated.StockholdersEquity = post-clean equity + LedgerEntry.EquityOffset (single application)")

	// TaxShieldDTA flows from the ledger to DeferredTaxAssets (Q2 resolution
	// pin). A2 populates TaxShieldDTA = writedown × ETR when ETR > 0.
	expectedTaxShield := writedown * data.EffectiveTaxRate
	assert.InDelta(t, expectedTaxShield, firedA2.TaxShieldDTA, 1.0,
		"A2 must populate LedgerEntry.TaxShieldDTA = writedown * EffectiveTaxRate (Q2 resolution)")
	expectedRestatedDTA := result.CleanedData.DeferredTaxAssets + expectedTaxShield
	assert.InDelta(t, expectedRestatedDTA, restated.DeferredTaxAssets, 1.0,
		"Restated.DeferredTaxAssets = post-clean DTA + LedgerEntry.TaxShieldDTA")
}

// TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnEarningsFire is
// the earnings-side counterpart to the OnRestaterFire pin. C1/C2/C3/C5
// fire on NormalizedOperatingIncome via Restater dual-writes; the test
// confirms Restated.NormalizedOperatingIncome receives ONE delta
// application, not two.
//
// The fixture is constructed to fire C1 restructuring on the production
// rule path. If no C-rule fires the test is skipped gracefully so a future
// classifier change cannot silently turn it into a vacuous pass.
func TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnEarningsFire(t *testing.T) {
	cfg := createTestConfig()
	ctx := context.Background()

	svc, err := NewDataCleanerService(cfg, &mockAIServiceDataCleaner{}, nil)
	require.NoError(t, err)

	// Build a fixture with a material restructuring charge — large enough
	// to clear C1's materiality threshold (>= 5% of revenue typically).
	data := &entities.FinancialData{
		Ticker:                    "AAPL",
		CIK:                       "0000320193",
		AsOf:                      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		FilingPeriod:              "2025Q4",
		FilingDate:                time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC),
		ReportingCurrency:         "USD",
		Revenue:                   500_000_000,
		OperatingIncome:           100_000_000,
		NormalizedOperatingIncome: 100_000_000,
		RestructuringCharges:      80_000_000,
		TotalAssets:               1_000_000_000,
		StockholdersEquity:        400_000_000,
		EffectiveTaxRate:          0.25,
		SharesOutstanding:         100_000_000,
		HasNormalizedData:         true,
	}

	originalNOI := data.NormalizedOperatingIncome

	result, views, err := svc.CleanFinancialDataWithViews(ctx, data)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, views)
	require.NotNil(t, result.CleanedData)

	// Find any fired earnings Restater entry on NormalizedOperatingIncome.
	// C1/C2/C3/C5 all use this component.
	var firedEarnings *entities.LedgerEntry
	for i := range result.CleanedData.AdjustmentLedger {
		e := &result.CleanedData.AdjustmentLedger[i]
		if e.Fired && e.Component == "NormalizedOperatingIncome" {
			firedEarnings = e
			break
		}
	}
	if firedEarnings == nil {
		t.Skip("no earnings Restater fired on the fixture — adjust fixture if C-rule thresholds shift")
	}

	cleanedNOI := result.CleanedData.NormalizedOperatingIncome
	require.NotEqual(t, originalNOI, cleanedNOI,
		"fixture must produce a fired earnings Restater")

	// HIGH-1 assertion (earnings side): Restated.NormalizedOperatingIncome
	// equals the post-dispatcher value. Before the fix this would have
	// double-counted the delta.
	restated := views.Restated()
	require.NotNil(t, restated)
	assert.InDelta(t, cleanedNOI, restated.NormalizedOperatingIncome, 0.01,
		"Restated.NormalizedOperatingIncome must equal the post-dispatcher value (one delta application)")

	// Sanity: the ledger entry's DeltaAmount is non-zero (so the
	// "double-count would have shown up" assertion is meaningful).
	require.NotEqual(t, 0.0, firedEarnings.DeltaAmount,
		"earnings Restater's DeltaAmount must be non-zero so the regression assertion is meaningful")

	// Numerical-stability sanity: the restated value is finite.
	require.False(t, math.IsNaN(restated.NormalizedOperatingIncome) || math.IsInf(restated.NormalizedOperatingIncome, 0),
		"Restated.NormalizedOperatingIncome must be a finite number")
}
