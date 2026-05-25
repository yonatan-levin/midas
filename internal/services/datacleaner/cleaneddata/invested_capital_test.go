package cleaneddata

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestCleanedFinancialData_InvestedCapital_AppliesOverlays exercises the
// four canonical overlay shapes that Phase 2 emits today:
//   - B1 / B2 (Field:"TotalDebt", Operation:"add")       → DebtLikeClaims
//   - B3      (Field:"DebtLikeClaims", Operation:"add")  → DebtLikeClaims  (Phase 4 routing intent)
//   - A1      (Field:"TotalAssets", Operation:"subtract")→ TotalAssets -=, Goodwill=0  (Damodaran)
//
// Asserts the three contract guarantees:
//  1. DebtLikeClaims accumulates B1 + B2 + B3.
//  2. TotalAssets decreases by A1 amount; Goodwill flushes to 0.
//  3. TotalDebt is UNCHANGED from Restated (capital-structure denominator
//     must not collapse with DebtLikeClaims — Phase 4 WACC reads them
//     separately).
func TestCleanedFinancialData_InvestedCapital_AppliesOverlays(t *testing.T) {
	// Components-sum-to-umbrella seed so Restated reproduces TotalAssets=1000:
	//   CurrentAssets       = Cash(50) + Inventory(100) + OtherCA(50)    = 200
	//   TotalAssets         = CurrentAssets(200) + Goodwill(200) +
	//                         OtherIntangibles(50) + DTA(0) + OtherNCA(550) = 1000
	//   TangibleAssets      = TotalAssets(1000) - Goodwill(200) - OtherIntangibles(50) = 750
	raw := &entities.FinancialData{
		Ticker:                 "OVRL",
		CashAndCashEquivalents: 50,
		Inventory:              100,
		OtherCurrentAssets:     50,
		CurrentAssets:          200,
		Goodwill:               200,
		OtherIntangibles:       50,
		DeferredTaxAssets:      0,
		OtherNonCurrentAssets:  550,
		TotalAssets:            1_000,
		TangibleAssets:         750,
		TotalDebt:              300,
		Overlays: []entities.OverlaySpec{
			{
				OverlayID:       "B1_operating_leases",
				Field:           "TotalDebt",
				Operation:       "add",
				Amount:          120,
				AmountSemantics: entities.AmountIncremental,
			},
			{
				OverlayID:       "B2_pension_underfunding",
				Field:           "TotalDebt",
				Operation:       "add",
				Amount:          80,
				AmountSemantics: entities.AmountIncremental,
			},
			{
				OverlayID:       "B3_contingent_liability",
				Field:           "DebtLikeClaims", // Phase 4 routing intent
				Operation:       "add",
				Amount:          40,
				AmountSemantics: entities.AmountIncremental,
			},
			{
				OverlayID:       "A1_goodwill_exclusion",
				Field:           "TotalAssets",
				Operation:       "subtract",
				Amount:          200,
				AmountSemantics: entities.AmountIncremental,
			},
		},
	}

	ic := New(raw).InvestedCapital()
	require.NotNil(t, ic)

	assert.Equal(t, InvestedCapitalView, ic.ViewKind)
	assert.Equal(t, 240.0, ic.DebtLikeClaims, "DebtLikeClaims = B1(120) + B2(80) + B3(40)")
	assert.Equal(t, 800.0, ic.TotalAssets, "TotalAssets reduced by A1 goodwill exclusion (1000 - 200)")
	assert.Equal(t, 0.0, ic.Goodwill, "Goodwill must flush to 0 per Damodaran convention")
	assert.Equal(t, 750.0, ic.TangibleAssets, "TangibleAssets = TotalAssets - OtherIntangibles (800 - 50)")
	assert.Equal(t, 300.0, ic.TotalDebt, "TotalDebt MUST stay unchanged (WACC denominator)")
}

// TestCleanedFinancialData_InvestedCapital_EmptyOverlaysEqualsRestated pins
// the property: with no overlays, InvestedCapital() equals Restated() for
// every field except ViewKind.
func TestCleanedFinancialData_InvestedCapital_EmptyOverlaysEqualsRestated(t *testing.T) {
	raw := &entities.FinancialData{
		Ticker:                 "NOOP",
		CashAndCashEquivalents: 10,
		Inventory:              20,
		OtherCurrentAssets:     5,
		CurrentAssets:          35,
		Goodwill:               5,
		OtherIntangibles:       3,
		DeferredTaxAssets:      2,
		OtherNonCurrentAssets:  5,
		TotalAssets:            50,
		TotalDebt:              15,
	}

	c := New(raw)
	restated := c.Restated()
	ic := c.InvestedCapital()
	require.NotNil(t, ic)

	// Compare per-field. Direct struct-equality would fail on ViewKind alone.
	assert.Equal(t, restated.TotalAssets, ic.TotalAssets)
	assert.Equal(t, restated.TotalDebt, ic.TotalDebt)
	assert.Equal(t, restated.Goodwill, ic.Goodwill)
	assert.Equal(t, restated.CurrentAssets, ic.CurrentAssets)
	assert.Equal(t, restated.StockholdersEquity, ic.StockholdersEquity)
	assert.Equal(t, 0.0, ic.DebtLikeClaims, "empty overlays produce no DebtLikeClaims")
	assert.Equal(t, InvestedCapitalView, ic.ViewKind)
}

// TestCleanedFinancialData_InvestedCapital_UnknownFieldSilentlySkipped
// pins fail-soft behavior on overlays with unrecognized Field values.
// Future overlays added before the view is updated must not crash older
// callers — they're skipped silently, which is the documented contract.
func TestCleanedFinancialData_InvestedCapital_UnknownFieldSilentlySkipped(t *testing.T) {
	raw := &entities.FinancialData{
		Ticker:    "UNKNWN",
		TotalDebt: 100,
		Overlays: []entities.OverlaySpec{
			{
				OverlayID:       "future_unknown",
				Field:           "FutureField",
				Operation:       "add",
				Amount:          999,
				AmountSemantics: entities.AmountIncremental,
			},
		},
	}

	ic := New(raw).InvestedCapital()
	require.NotNil(t, ic)
	assert.Equal(t, 0.0, ic.DebtLikeClaims, "unknown Field overlay produces no mutation")
	assert.Equal(t, 100.0, ic.TotalDebt, "unknown Field overlay leaves TotalDebt untouched")
}

// TestCleanedFinancialData_InvestedCapital_MemoizationIdempotent pins
// pointer identity across repeated InvestedCapital() calls.
func TestCleanedFinancialData_InvestedCapital_MemoizationIdempotent(t *testing.T) {
	c := New(&entities.FinancialData{Ticker: "MEM"})
	v1 := c.InvestedCapital()
	v2 := c.InvestedCapital()
	assert.Same(t, v1, v2)
}
