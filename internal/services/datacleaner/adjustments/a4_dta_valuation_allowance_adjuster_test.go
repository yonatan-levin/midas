package adjustments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// productionDeferredTaxRule returns a CleaningRule whose ID matches the
// production rules.json entry ("deferred_tax_assets") so the rule reaches
// the deferred_tax_assets branch in ProcessAssetAdjustments. The existing
// createDeferredTaxRule() helper uses ID="A4" which short-cuts via the
// rule.ID switch's `default` arm — fine for direct ProcessDeferredTaxAdjustment
// tests but useless for dispatcher tests.
func productionDeferredTaxRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:          "deferred_tax_assets",
		Name:        "Deferred Tax Asset Valuation Allowance",
		Category:    entities.AssetQuality,
		Adjustment:  entities.AdjustmentTypeValuationAllowance,
		Description: "Apply conservative valuation allowance to deferred tax assets",
		Threshold: &entities.ThresholdConfig{
			PercentageOfAssets: &[]float64{0.10}[0], // 10% threshold
			MinAmount:          &[]float64{0.50}[0], // Minimum 50% allowance
		},
		Enabled: true,
	}
}

// TestA4DTAValuationAllowanceAdjuster_Adjuster_Interface_Contract pins the
// DC-1 Phase 2 PR-2 Task 2.3 acceptance gate: a4DTAValuationAllowanceAdjuster
// satisfies the Adjuster interface AND its AdjusterOutput matches the spec /
// plan §3.5 / §4 row A4 Restater contract for the fired + both skipped paths.
//
// The compile-time assertion
// `var _ Adjuster = (*a4DTAValuationAllowanceAdjuster)(nil)` in assets.go is
// the primary signature pin; this test exercises the runtime contract —
// every branch of ApplyA4DTAValuationAllowance produces an AdjusterOutput
// whose LedgerEntries (Restater-shaped) and Flags match the shape the
// orchestrator + Phase 3 view reconstruction will rely on.
func TestA4DTAValuationAllowanceAdjuster_Adjuster_Interface_Contract(t *testing.T) {
	// Construct through the exported factory so the test exercises the
	// public API surface the orchestrator will use.
	aa := NewAssetAdjuster()
	adj := NewA4DTAValuationAllowanceAdjuster(aa)
	require.NotNil(t, adj)

	// Name() contract: stable identifier consumers can join on. Locked to
	// the AdjusterID constant so a rename forces both the test and the
	// constant to move together.
	assert.Equal(t, adjusterIDA4DTAValuationAllowance, adj.Name(),
		"a4DTAValuationAllowanceAdjuster.Name() must equal the AdjusterID constant")

	rule := createDeferredTaxRule()
	cleaningCtx := &entities.CleaningContext{}

	t.Run("fired path with high DTA ratio (>=10%) emits Restater LedgerEntry + Flag", func(t *testing.T) {
		// DTA ratio = 200_000 / 1_000_000 = 20% — above both the 5% fire
		// threshold AND the 10% flag-emission threshold. valuationAllowance
		// = 200_000 * 0.50 = 100_000.
		data := &entities.FinancialData{
			DeferredTaxAssets: 200_000.0,
			TotalAssets:       1_000_000.0,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err, "Apply must not error on a well-formed fired-path input")

		// AdjusterOutput contract: exactly one fired LedgerEntry, NO Overlays
		// (Restater — direct component mutation, no analytical overlay), one
		// Flag (legacy A4 only emits a flag when ratio >=10% — this case
		// qualifies).
		require.Len(t, out.LedgerEntries, 1, "fired path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays, "A4 is a Restater — must NOT emit OverlaySpecs")
		require.Len(t, out.Flags, 1, "DTA ratio >=10% fires the legacy significance flag")

		// LedgerEntry contract (plan §3.5 Restater role + §4 row A4):
		// Fired=true, Component:"DeferredTaxAssets", DeltaAmount:-allowance,
		// EquityOffset:-allowance, TaxShieldDTA:0 (A4 IS the DTA valuation
		// allowance — no separate tax shield to compute).
		entry := out.LedgerEntries[0]
		assert.True(t, entry.Fired, "fired-path LedgerEntry must have Fired=true")
		assert.Equal(t, adjusterIDA4DTAValuationAllowance, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.NotEmpty(t, entry.Reasoning, "Reasoning must be populated for fired entries")
		assert.False(t, entry.Timestamp.IsZero(), "Timestamp must be set on fired entries")
		assert.Equal(t, "DeferredTaxAssets", entry.Component,
			"A4 is a Restater — Component must point at the mutated balance-sheet line")
		assert.InDelta(t, -100_000.0, entry.DeltaAmount, 1e-6,
			"DeltaAmount must equal -valuationAllowance (signed reduction of DeferredTaxAssets)")
		assert.InDelta(t, -100_000.0, entry.EquityOffset, 1e-6,
			"EquityOffset must mirror DeltaAmount — writedowns reduce equity 1:1")
		assert.Zero(t, entry.TaxShieldDTA,
			"A4 TaxShieldDTA stays 0 — the valuation allowance IS the DTA reduction, no separate shield")
		assert.Empty(t, entry.SkipReason, "SkipReason must be empty for fired entries")

		// Flag contract: severity comes from the legacy ratio bucket helper.
		flag := out.Flags[0]
		assert.Equal(t, "dta_valuation_allowance", flag.Type)
		assert.InDelta(t, 100_000.0, flag.Amount, 1e-6, "flag amount must equal valuation-allowance magnitude")

		// CRITICAL invariant: Apply must NOT mutate `working`. The dispatcher
		// in ProcessAssetAdjustments performs the dual-write — Apply is
		// read-only.
		assert.Equal(t, 200_000.0, data.DeferredTaxAssets, "Apply must NOT mutate data.DeferredTaxAssets")
		assert.Equal(t, 1_000_000.0, data.TotalAssets, "Apply must NOT mutate data.TotalAssets")
		assert.Equal(t, 0.0, data.ValuationAllowance, "Apply must NOT mutate data.ValuationAllowance")
	})

	t.Run("fired path with moderate DTA ratio (5-10%) emits LedgerEntry but no Flag", func(t *testing.T) {
		// DTA ratio = 70_000 / 1_000_000 = 7% — above the 5% fire threshold
		// but below the 10% flag-emission threshold. valuationAllowance =
		// 70_000 * 0.50 = 35_000.
		data := &entities.FinancialData{
			DeferredTaxAssets: 70_000.0,
			TotalAssets:       1_000_000.0,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1, "fired path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays, "A4 is a Restater — no OverlaySpec")
		// Legacy contract: only emit significance flag when DTA ratio >=10%.
		// 7% fires the rule but stays silent on the flag side.
		assert.Empty(t, out.Flags,
			"DTA ratio 5-10% fires the rule but must NOT emit the legacy >=10% significance flag")

		entry := out.LedgerEntries[0]
		assert.True(t, entry.Fired)
		assert.Equal(t, "DeferredTaxAssets", entry.Component)
		assert.InDelta(t, -35_000.0, entry.DeltaAmount, 1e-6)
		assert.InDelta(t, -35_000.0, entry.EquityOffset, 1e-6)
		assert.Zero(t, entry.TaxShieldDTA)

		// Apply mutation-freeness preserved on this branch too.
		assert.Equal(t, 70_000.0, data.DeferredTaxAssets)
		assert.Equal(t, 1_000_000.0, data.TotalAssets)
	})

	t.Run("skip path 1 (no DTA) emits one Fired:false LedgerEntry without metrics", func(t *testing.T) {
		data := &entities.FinancialData{
			DeferredTaxAssets: 0.0,
			TotalAssets:       1_000_000.0,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1, "skip path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays, "skip path emits no OverlaySpec")
		assert.Empty(t, out.Flags, "skip path emits no Flags")

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, adjusterIDA4DTAValuationAllowance, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.Equal(t, "No deferred tax assets present to adjust", entry.SkipReason,
			"no-DTA skip path must use the canonical SkipReason string")
		assert.NotEmpty(t, entry.Reasoning, "Reasoning must be populated even on skip")
		assert.Empty(t, entry.SkipMetrics,
			"no-DTA skip path does not carry SkipMetrics — only the threshold-failed path does")
		// Plan §3.6.6: skipped entries carry zero monetary deltas.
		assert.Zero(t, entry.DeltaAmount)
		assert.Zero(t, entry.EquityOffset)
		assert.Zero(t, entry.TaxShieldDTA)
	})

	t.Run("skip path 2 (below threshold) emits Fired:false LedgerEntry with SkipMetrics", func(t *testing.T) {
		// DTA ratio = 30_000 / 1_000_000 = 3% — below the 5% threshold.
		data := &entities.FinancialData{
			DeferredTaxAssets: 30_000.0,
			TotalAssets:       1_000_000.0,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		assert.Empty(t, out.Overlays)
		assert.Empty(t, out.Flags)

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, adjusterIDA4DTAValuationAllowance, entry.AdjusterID)
		assert.Contains(t, entry.SkipReason, "below threshold",
			"threshold-failed SkipReason must explain why")
		require.NotNil(t, entry.SkipMetrics, "threshold-failed skip path must carry SkipMetrics")
		assert.InDelta(t, 0.03, entry.SkipMetrics["dta_ratio"], 1e-9)
		assert.InDelta(t, 0.05, entry.SkipMetrics["threshold"], 1e-9)
	})
}

// TestAssetAdjuster_ProcessAssetAdjustments_NativeA4Emission pins the
// dispatcher's contract for the deferred_tax_assets rule: when present in
// the input rules AND DTA is above threshold, ProcessAssetAdjustments
// populates NativeLedgerEntries with the A4 fired entry AND mutates
// data.DeferredTaxAssets / data.TotalAssets / data.ValuationAllowance
// exactly as the legacy ProcessDeferredTaxAdjustment did (dual-write
// preserved).
func TestAssetAdjuster_ProcessAssetAdjustments_NativeA4Emission(t *testing.T) {
	aa := NewAssetAdjuster()
	// DTA ratio = 200_000 / 1_000_000 = 20% — fires with the legacy
	// significance flag. valuationAllowance = 200_000 * 0.50 = 100_000.
	data := &entities.FinancialData{
		DeferredTaxAssets: 200_000.0,
		TotalAssets:       1_000_000.0,
	}
	originalDTA := data.DeferredTaxAssets
	rules := []*entities.CleaningRule{productionDeferredTaxRule()}
	cleaningCtx := &entities.CleaningContext{}

	result := aa.ProcessAssetAdjustments(data, rules, cleaningCtx)
	require.NotNil(t, result)

	// Legacy contract: Applied=true, one Adjustment, Adjustments[0].Amount
	// equals the valuation-allowance magnitude (100_000). The legacy
	// *AdjustmentResult shape stays unchanged so callers that don't know
	// about the Adjuster interface keep working.
	assert.True(t, result.Applied)
	require.Len(t, result.Adjustments, 1)
	assert.InDelta(t, 100_000.0, result.Adjustments[0].Amount, 1e-6)
	assert.Equal(t, "DeferredTaxAssets", result.Adjustments[0].FromAccount)
	assert.Equal(t, "ValuationAllowance", result.Adjustments[0].ToAccount)
	assert.Equal(t, entities.AdjustmentTypeValuationAllowance, result.Adjustments[0].Type)
	assert.InDelta(t, 50.0, result.Adjustments[0].Percentage, 1e-9,
		"legacy A4 percentage is a fixed 50% allowance rate")

	// Phase 2 PR-2 Task 2.3 native emission contract:
	require.Len(t, result.NativeLedgerEntries, 1,
		"ProcessAssetAdjustments must surface the A4 native LedgerEntry")
	assert.Empty(t, result.NativeOverlays, "A4 is a Restater — no OverlaySpec native emission")
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["deferred_tax_assets"],
		"deferred_tax_assets must appear in NativelyEmittedRuleIDs so the shim skips it")

	// Restater shape on the native entry:
	nativeEntry := result.NativeLedgerEntries[0]
	assert.True(t, nativeEntry.Fired)
	assert.Equal(t, adjusterIDA4DTAValuationAllowance, nativeEntry.AdjusterID)
	assert.Equal(t, "DeferredTaxAssets", nativeEntry.Component)
	assert.InDelta(t, -100_000.0, nativeEntry.DeltaAmount, 1e-6)
	assert.InDelta(t, -100_000.0, nativeEntry.EquityOffset, 1e-6)
	assert.Zero(t, nativeEntry.TaxShieldDTA, "A4 TaxShieldDTA stays 0 on dispatcher path too")

	// Dual-write preserved — data was mutated as the legacy code did.
	// adjustedDTA = originalDTA - valuationAllowance = 100_000.
	assert.InDelta(t, 100_000.0, data.DeferredTaxAssets, 1e-6,
		"dispatcher must reduce data.DeferredTaxAssets by the valuation allowance (dual-write)")
	assert.InDelta(t, 900_000.0, data.TotalAssets, 1e-6,
		"dispatcher must subtract valuation allowance from data.TotalAssets (dual-write)")
	assert.InDelta(t, 100_000.0, data.ValuationAllowance, 1e-6,
		"dispatcher must add valuation allowance into data.ValuationAllowance (dual-write)")
	// Sanity: combined balance-sheet invariant — adjustedDTA + valuationAllowance == originalDTA.
	assert.InDelta(t, originalDTA, data.DeferredTaxAssets+data.ValuationAllowance, 1e-6,
		"DTA reduction + valuation allowance must equal original DTA (no value created/destroyed)")
}

// TestAssetAdjuster_ProcessAssetAdjustments_NativeA4SkipPath confirms that
// even on the skip path (no DTA present), ProcessAssetAdjustments surfaces
// the Fired:false LedgerEntry through NativeLedgerEntries and performs NO
// mutation — the shim then skips emitting its own generic skip entry for
// the same rule.
func TestAssetAdjuster_ProcessAssetAdjustments_NativeA4SkipPath(t *testing.T) {
	aa := NewAssetAdjuster()
	data := &entities.FinancialData{
		DeferredTaxAssets:  0.0,
		TotalAssets:        1_000_000.0,
		ValuationAllowance: 0.0,
	}
	rules := []*entities.CleaningRule{productionDeferredTaxRule()}

	result := aa.ProcessAssetAdjustments(data, rules, &entities.CleaningContext{})
	require.NotNil(t, result)

	// Legacy contract: Applied=false, no Adjustments.
	assert.False(t, result.Applied)
	assert.Empty(t, result.Adjustments)

	// Native emission contract — skip path still emits a Fired:false entry.
	require.Len(t, result.NativeLedgerEntries, 1,
		"skip path must still surface a Fired:false native LedgerEntry")
	assert.False(t, result.NativeLedgerEntries[0].Fired)
	assert.Empty(t, result.NativeOverlays, "skip path emits no Overlays")
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["deferred_tax_assets"],
		"deferred_tax_assets must appear in NativelyEmittedRuleIDs even on skip path")

	// Dual-write contract — skip path must NOT mutate balance-sheet fields.
	assert.Equal(t, 0.0, data.DeferredTaxAssets)
	assert.Equal(t, 1_000_000.0, data.TotalAssets)
	assert.Equal(t, 0.0, data.ValuationAllowance)
}
