package datacleaner

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestAdjustmentsProjection_HandlesUnknownAdjusterID pins the §7.3 item 1
// defensive contract: a LedgerEntry carrying an AdjusterID not in
// perRuleAdjustmentMeta must NOT panic — it is silently skipped (the
// basket-parity golden surfaces real drifts; an unknown AdjusterID in
// production indicates a forgotten metadata-table row, not a runtime
// fault).
func TestAdjustmentsProjection_HandlesUnknownAdjusterID(t *testing.T) {
	ledger := entities.AdjustmentLedger{
		{
			Timestamp:    time.Now(),
			AdjusterID:   "X9_definitely_not_a_real_rule",
			RuleID:       "synthetic_rule",
			Fired:        true,
			Component:    "TotalAssets",
			DeltaAmount:  -1_000_000.0,
			EquityOffset: -1_000_000.0,
		},
	}

	got := adjustmentsFromLedger(ledger, nil, perRuleAdjustmentMeta)
	assert.Empty(t, got, "unknown AdjusterID must produce zero Adjustments without panic")
}

// TestAdjustmentsProjection_FromPreStateMode_ZeroDenominatorYieldsZeroPct
// pins §7.3 item 2: when the denominator captured on
// LedgerEntry.SkipMetrics is 0 (legacy code's `if originalRevenue > 0`
// guard), the projection emits Percentage = 0 — no Inf/NaN leak into the
// public Adjustment.Percentage field.
func TestAdjustmentsProjection_FromPreStateMode_ZeroDenominatorYieldsZeroPct(t *testing.T) {
	ledger := entities.AdjustmentLedger{
		{
			Timestamp:    time.Now(),
			AdjusterID:   "C2_asset_sale_gains",
			RuleID:       "asset_sale_gains",
			Fired:        true,
			Component:    "NormalizedOperatingIncome",
			DeltaAmount:  -10_000_000.0,
			EquityOffset: -10_000_000.0,
			SkipMetrics: map[string]float64{
				"original_Revenue": 0.0, // pathological — but must NOT produce Inf.
			},
		},
	}

	got := adjustmentsFromLedger(ledger, nil, perRuleAdjustmentMeta)
	require.Len(t, got, 1, "C2 fires regardless of denominator")
	assert.Zero(t, got[0].Percentage, "zero denominator MUST yield zero Percentage, not Inf/NaN")
	assert.InDelta(t, 10_000_000.0, got[0].Amount, 1e-6)
}

// TestAdjustmentsProjection_ConstantPctMode_A4_A5 pins §7.3 item 3:
// constant-mode rules (A4 = 50.0, A5 = 40.0) emit their hard-coded
// Percentage regardless of what LedgerEntry.SkipMetrics carries. This
// guards against an accidental migration of A4/A5 to from_pre_state
// mode that would silently drop the legacy constant.
func TestAdjustmentsProjection_ConstantPctMode_A4_A5(t *testing.T) {
	ledger := entities.AdjustmentLedger{
		{
			Timestamp:    time.Now(),
			AdjusterID:   "A4_dta_valuation_allowance",
			RuleID:       "deferred_tax_assets",
			Fired:        true,
			Component:    "DeferredTaxAssets",
			DeltaAmount:  -50_000_000.0,
			EquityOffset: -50_000_000.0,
			SkipMetrics: map[string]float64{
				// Whatever lives here MUST be ignored by the constant-mode
				// projection — confirms the meta table's PreStateKey field
				// is not consulted in this mode.
				"original_DeferredTaxAssets": 999_999_999.0,
			},
		},
		{
			Timestamp:    time.Now(),
			AdjusterID:   "A5_inventory_writedown",
			RuleID:       "obsolete_inventory",
			Fired:        true,
			Component:    "Inventory",
			DeltaAmount:  -120_000_000.0,
			EquityOffset: -120_000_000.0,
			SkipMetrics: map[string]float64{
				"original_Inventory": 999_999_999.0,
			},
		},
	}

	got := adjustmentsFromLedger(ledger, nil, perRuleAdjustmentMeta)
	require.Len(t, got, 2)

	byRuleID := map[string]entities.Adjustment{}
	for _, a := range got {
		byRuleID[a.RuleID] = a
	}

	a4, ok := byRuleID["deferred_tax_assets"]
	require.True(t, ok)
	assert.InDelta(t, 50.0, a4.Percentage, 1e-9, "A4 must emit constant Percentage=50.0")
	assert.InDelta(t, 50_000_000.0, a4.Amount, 1e-6)
	assert.Equal(t, "DeferredTaxAssets", a4.FromAccount)
	assert.Equal(t, "ValuationAllowance", a4.ToAccount)

	a5, ok := byRuleID["obsolete_inventory"]
	require.True(t, ok)
	assert.InDelta(t, 40.0, a5.Percentage, 1e-9, "A5 must emit constant Percentage=40.0")
	assert.InDelta(t, 120_000_000.0, a5.Amount, 1e-6)
	assert.Equal(t, "Inventory", a5.FromAccount)
	assert.Equal(t, "InventoryWritedown", a5.ToAccount)
}

// TestAdjustmentsProjection_A6A7_OverlayEmitters pins the TDB-2 projection
// meta rows: a fired A6 (ROU exclusion) and a fired A7 (excess cash) each
// project to one entities.Adjustment — Category AssetQuality, Type Exclude,
// the correct FromAccount, Amount sourced from the matching OverlaySpec, and
// Reasoning pulled from the overlay (OverlayEmitter family).
func TestAdjustmentsProjection_A6A7_OverlayEmitters(t *testing.T) {
	ledger := entities.AdjustmentLedger{
		{
			Timestamp:  time.Now(),
			AdjusterID: "A6_right_of_use_exclusion",
			RuleID:     "right_of_use_assets",
			Fired:      true,
			Reasoning:  "A6 right-of-use exclusion overlay emitted",
		},
		{
			Timestamp:  time.Now(),
			AdjusterID: "A7_excess_cash",
			RuleID:     "excess_cash",
			Fired:      true,
			Reasoning:  "A7 excess-cash identification overlay emitted",
		},
	}
	overlays := []entities.OverlaySpec{
		{
			OverlayID:       "A6_right_of_use_exclusion",
			RuleID:          "right_of_use_assets",
			Field:           "InvestedCapitalExclusion",
			Operation:       "subtract",
			Amount:          200_000_000.0,
			AmountSemantics: entities.AmountIncremental,
			Reasoning:       "right_of_use_assets: Excluded 200000000 ROU assets (20.0% of assets) from invested capital per A6 rule",
		},
		{
			OverlayID:       "A7_excess_cash",
			RuleID:          "excess_cash",
			Field:           "ExcessCash",
			Operation:       "identify",
			Amount:          40_000_000_000.0,
			AmountSemantics: entities.AmountReplacement,
			Reasoning:       "excess_cash: Identified 40000000000 excess cash as non-operating per A7 rule",
		},
	}

	got := adjustmentsFromLedger(ledger, overlays, perRuleAdjustmentMeta)
	require.Len(t, got, 2, "fired A6 + fired A7 each project to one Adjustment")

	byRuleID := map[string]entities.Adjustment{}
	for _, a := range got {
		byRuleID[a.RuleID] = a
	}

	a6, ok := byRuleID["right_of_use_assets"]
	require.True(t, ok)
	assert.Equal(t, entities.AssetQuality, a6.Category)
	assert.Equal(t, entities.Exclude, a6.Type)
	assert.Equal(t, "OperatingLeaseRightOfUseAsset", a6.FromAccount)
	assert.InDelta(t, 200_000_000.0, a6.Amount, 1e-6, "A6 amount sourced from the overlay")
	assert.Zero(t, a6.Percentage, "A6 is percentageAbsent")
	assert.Contains(t, a6.Reasoning, "right_of_use_assets", "A6 reasoning pulled from the overlay")
	assert.True(t, a6.Applied)

	a7, ok := byRuleID["excess_cash"]
	require.True(t, ok)
	assert.Equal(t, entities.AssetQuality, a7.Category)
	assert.Equal(t, entities.Exclude, a7.Type)
	assert.Equal(t, "CashAndCashEquivalents", a7.FromAccount)
	assert.InDelta(t, 40_000_000_000.0, a7.Amount, 1e-6, "A7 amount sourced from the overlay")
	assert.Zero(t, a7.Percentage, "A7 is percentageAbsent")
	assert.Contains(t, a7.Reasoning, "excess_cash", "A7 reasoning pulled from the overlay")
	assert.True(t, a7.Applied)
}
