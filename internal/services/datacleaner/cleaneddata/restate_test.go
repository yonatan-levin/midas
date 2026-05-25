package cleaneddata

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestCleanedFinancialData_Restated_BitForBitOnNoFiredAdjusters pins the
// identity property: when no adjuster fired (no LedgerEntry with
// Fired:true, no Overlays), Restated() reproduces AsReported() for every
// consumed balance-sheet umbrella that the recompute touches.
//
// The recompute below is exact when components+plug==umbrella by Phase 0
// construction; this test seeds the raw FinancialData with values that
// satisfy the invariant, then asserts Restated == AsReported for the
// recomputed fields.
func TestCleanedFinancialData_Restated_BitForBitOnNoFiredAdjusters(t *testing.T) {
	// Components-sum-to-umbrella seed:
	//   CurrentAssets       = Cash + Inventory + OtherCurrentAssets
	//                       = 100 + 150 + 50 = 300
	//   TotalAssets         = CurrentAssets + Goodwill + OtherIntangibles +
	//                         DeferredTaxAssets + OtherNonCurrentAssets
	//                       = 300 + 80 + 40 + 20 + 60 = 500
	//   CurrentLiabilities  = OpLeaseLiabCurrent + OtherCurrentLiab
	//                       = 0 + 90 = 90
	//   TotalLiabilities    = CurrentLiabilities + TotalDebt +
	//                         OpLeaseLiabNoncurrent + OtherNonCurrentLiab
	//                       = 90 + 120 + 0 + 70 = 280
	//   TangibleAssets      = TotalAssets - Goodwill - OtherIntangibles
	//                       = 500 - 80 - 40 = 380
	raw := &entities.FinancialData{
		Ticker:                            "CLEAN",
		CashAndCashEquivalents:            100,
		Inventory:                         150,
		OtherCurrentAssets:                50,
		CurrentAssets:                     300,
		Goodwill:                          80,
		OtherIntangibles:                  40,
		DeferredTaxAssets:                 20,
		OtherNonCurrentAssets:             60,
		TotalAssets:                       500,
		TangibleAssets:                    380,
		OperatingLeaseLiabilityCurrent:    0,
		OtherCurrentLiabilities:           90,
		CurrentLiabilities:                90,
		TotalDebt:                         120,
		OperatingLeaseLiabilityNoncurrent: 0,
		OtherNonCurrentLiabilities:        70,
		TotalLiabilities:                  280,
		StockholdersEquity:                220,
	}

	c := New(raw)
	asReported := c.AsReported()
	restated := c.Restated()
	require.NotNil(t, asReported)
	require.NotNil(t, restated)

	assert.Equal(t, RestatedView, restated.ViewKind)

	assert.Equal(t, asReported.CurrentAssets, restated.CurrentAssets)
	assert.Equal(t, asReported.TotalAssets, restated.TotalAssets)
	assert.Equal(t, asReported.CurrentLiabilities, restated.CurrentLiabilities)
	assert.Equal(t, asReported.TotalLiabilities, restated.TotalLiabilities)
	assert.Equal(t, asReported.TangibleAssets, restated.TangibleAssets)
	assert.Equal(t, asReported.StockholdersEquity, restated.StockholdersEquity,
		"Restated equity must equal AsReported when no adjuster fired")
	assert.Equal(t, asReported.DeferredTaxAssets, restated.DeferredTaxAssets)
	assert.Equal(t, asReported.OperatingIncome, restated.OperatingIncome)
	assert.Equal(t, asReported.NormalizedOperatingIncome, restated.NormalizedOperatingIncome)
	assert.Equal(t, asReported.InterestExpense, restated.InterestExpense)
	assert.Equal(t, asReported.Inventory, restated.Inventory)
	assert.Equal(t, asReported.OtherIntangibles, restated.OtherIntangibles)
}

// TestCleanedFinancialData_Restated_C6EquityOffsetZero is the LOAD-BEARING
// invariant test for Phase 3: a C6 (capitalized_interest) LedgerEntry
// carries DeltaAmount != 0 on InterestExpense AND EquityOffset == 0
// because the reclassification is between income-statement lines, NOT a
// real economic loss flowing to retained earnings.
//
// Restated() MUST read e.EquityOffset directly. If the implementation
// derived EquityOffset from DeltaAmount (e.g. equityOffset := -e.DeltaAmount),
// retained equity would silently absorb capitalized-interest moves —
// which would be wrong. The dispatcher-level test pin in
// c6_capitalized_interest_adjuster_test.go says explicitly: "Phase 3
// Restated() must NOT add C6 DeltaAmount to retained earnings".
func TestCleanedFinancialData_Restated_C6EquityOffsetZero(t *testing.T) {
	raw := &entities.FinancialData{
		Ticker:             "CAPX",
		InterestExpense:    50_000,
		StockholdersEquity: 1_000_000,
		AdjustmentLedger: entities.AdjustmentLedger{
			{
				Fired:        true,
				AdjusterID:   "C6_capitalized_interest",
				RuleID:       "capitalized_interest",
				Component:    "InterestExpense",
				DeltaAmount:  20_000,
				EquityOffset: 0, // LOAD-BEARING: C6 reclassification, no equity flow
				Reasoning:    "C6 capitalized interest reclassification",
			},
		},
	}

	c := New(raw)
	restated := c.Restated()
	require.NotNil(t, restated)

	assert.Equal(t, 70_000.0, restated.InterestExpense,
		"C6 DeltaAmount should adjust the InterestExpense component")
	assert.Equal(t, 1_000_000.0, restated.StockholdersEquity,
		"C6 reclassification MUST NOT flow into retained equity (EquityOffset=0 LOAD-BEARING)")
}

// TestCleanedFinancialData_Restated_AppliesEquityOffsetAndTaxShield exercises
// a Restater that DOES move equity (DeltaAmount + EquityOffset matched) and
// populates TaxShieldDTA (mirrors A5/A2 Phase 3 pattern). This is the
// counterpoint to the C6 test: when EquityOffset is non-zero, equity moves.
func TestCleanedFinancialData_Restated_AppliesEquityOffsetAndTaxShield(t *testing.T) {
	// A5-style entry: Inventory write-down of $100 with 25% effective tax rate
	// producing a $25 deferred-tax-asset shield.
	raw := &entities.FinancialData{
		Ticker:             "WDWN",
		Inventory:          300,
		DeferredTaxAssets:  10,
		StockholdersEquity: 1000,
		// Components-to-umbrella seed (so the recompute equality holds where
		// applicable; balance-sheet umbrellas not asserted here).
		CashAndCashEquivalents: 0,
		OtherCurrentAssets:     0,
		CurrentAssets:          300,
		Goodwill:               0,
		OtherIntangibles:       0,
		OtherNonCurrentAssets:  0,
		AdjustmentLedger: entities.AdjustmentLedger{
			{
				Fired:        true,
				AdjusterID:   "A5_inventory_writedown",
				RuleID:       "obsolete_inventory",
				Component:    "Inventory",
				DeltaAmount:  -100,
				EquityOffset: -100,
				TaxShieldDTA: 25,
			},
		},
	}

	restated := New(raw).Restated()
	require.NotNil(t, restated)

	assert.Equal(t, 200.0, restated.Inventory, "Inventory should be reduced by 100")
	assert.Equal(t, 900.0, restated.StockholdersEquity, "equity flows EquityOffset -100")
	assert.Equal(t, 35.0, restated.DeferredTaxAssets, "DTA gets +25 shield on top of seed 10")
}

// TestCleanedFinancialData_Restated_SkipsUnfiredEntries pins that Fired:false
// LedgerEntries (skip-path observability records) do NOT affect the view.
// Phase 2's "why didn't this rule fire?" entries must remain inert.
func TestCleanedFinancialData_Restated_SkipsUnfiredEntries(t *testing.T) {
	raw := &entities.FinancialData{
		Ticker:             "SKIP",
		Inventory:          500,
		StockholdersEquity: 1_000,
		AdjustmentLedger: entities.AdjustmentLedger{
			{
				Fired:        false,
				AdjusterID:   "A5_inventory_writedown",
				Component:    "Inventory",
				DeltaAmount:  -200, // would change Inventory if Fired
				EquityOffset: -200, // would change equity if Fired
				SkipReason:   "below threshold",
			},
		},
	}

	restated := New(raw).Restated()
	require.NotNil(t, restated)
	assert.Equal(t, 500.0, restated.Inventory, "skip-path entries are inert")
	assert.Equal(t, 1_000.0, restated.StockholdersEquity, "skip-path entries leave equity untouched")
}

// TestCleanedFinancialData_Restated_MemoizationIdempotent pins pointer
// identity across repeated Restated() calls. Memoization is the contract;
// recomputing on every call would be wasteful and would also expose
// callers to a race against in-progress view construction.
func TestCleanedFinancialData_Restated_MemoizationIdempotent(t *testing.T) {
	c := New(&entities.FinancialData{Ticker: "MEM"})
	v1 := c.Restated()
	v2 := c.Restated()
	assert.Same(t, v1, v2, "Restated must memoize its returned pointer")
}
