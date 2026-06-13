package adjustments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// productionGoodwillRule returns a CleaningRule whose ID matches the
// production rules.json entry ("goodwill_exclusion") so the rule reaches the
// goodwill_exclusion branch in ProcessAssetAdjustments. The existing
// createGoodwillRule() helper uses ID="A1" which short-cuts via the rule.ID
// switch's `default` arm — fine for direct ProcessGoodwillAdjustment tests
// but useless for dispatcher tests.
func productionGoodwillRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:          "goodwill_exclusion",
		Name:        "Goodwill Exclusion",
		Category:    entities.AssetQuality,
		Adjustment:  entities.Exclude,
		Description: "Exclude goodwill from invested capital calculation",
		Threshold: &entities.ThresholdConfig{
			PercentageOfAssets: &[]float64{0.20}[0],
		},
		Enabled: true,
	}
}

// TestA1GoodwillAdjuster_Adjuster_Interface_Contract pins the DC-1 Phase 2
// PR-2 Task 2.1 acceptance gate: a1GoodwillAdjuster satisfies the Adjuster
// interface AND its AdjusterOutput matches the spec / plan §3.2 / §3.3 /
// §3.5 contracts for the fired + both skipped paths.
//
// The compile-time assertion `var _ Adjuster = (*a1GoodwillAdjuster)(nil)`
// in assets.go is the primary signature pin; this test exercises the
// runtime contract — every branch of ApplyA1Goodwill produces an
// AdjusterOutput whose LedgerEntries, Overlays, and Flags match the shape
// the orchestrator + Phase 3 view reconstruction will rely on.
func TestA1GoodwillAdjuster_Adjuster_Interface_Contract(t *testing.T) {
	// SR-1 A3: the adapter struct was deleted; call ApplyA1Goodwill directly
	// on the AssetAdjuster (the production dispatch path).
	adj := NewAssetAdjuster()
	require.NotNil(t, adj)

	rule := createGoodwillRule()
	cleaningCtx := &entities.CleaningContext{}

	t.Run("fired path emits one OverlaySpec and one Fired:true LedgerEntry", func(t *testing.T) {
		// Goodwill ratio = 500_000 / 1_000_000 = 50% — well above the 5%
		// threshold AND the 10% significance threshold, so the flag also
		// fires.
		data := &entities.FinancialData{
			Goodwill:    500_000.0,
			TotalAssets: 1_000_000.0,
		}

		out, err := adj.ApplyA1Goodwill(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err, "Apply must not error on a well-formed fired-path input")

		// AdjusterOutput contract: exactly one fired LedgerEntry, exactly
		// one OverlaySpec, exactly one Flag (because goodwillRatio >= 10%).
		require.Len(t, out.LedgerEntries, 1, "fired path emits exactly one LedgerEntry")
		require.Len(t, out.Overlays, 1, "fired path emits exactly one OverlaySpec")
		require.Len(t, out.Flags, 1, "fired path with goodwillRatio>=10%% emits one significance Flag")

		// LedgerEntry contract (plan §3.5 OverlayEmitter role): Fired=true,
		// AdjusterID matches Name(), Component / DeltaAmount / EquityOffset
		// LEFT UNSET because the declarative amount lives on OverlaySpec.
		entry := out.LedgerEntries[0]
		assert.True(t, entry.Fired, "fired-path LedgerEntry must have Fired=true")
		assert.Equal(t, adjusterIDA1GoodwillExclusion, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.NotEmpty(t, entry.Reasoning, "Reasoning must be populated for fired entries")
		assert.False(t, entry.Timestamp.IsZero(), "Timestamp must be set on fired entries")
		assert.Empty(t, entry.Component, "A1 is an OverlayEmitter — Component must NOT be set")
		assert.Zero(t, entry.DeltaAmount, "A1 is an OverlayEmitter — DeltaAmount must be zero on the LedgerEntry")
		assert.Zero(t, entry.EquityOffset, "A1 is an OverlayEmitter — EquityOffset must be zero on the LedgerEntry")
		assert.Empty(t, entry.SkipReason, "SkipReason must be empty for fired entries")

		// OverlaySpec contract: subtract semantics on TotalAssets, the
		// goodwill amount captured before mutation, Reasoning string
		// preserved across the migration.
		overlay := out.Overlays[0]
		assert.Equal(t, adjusterIDA1GoodwillExclusion, overlay.OverlayID)
		assert.Equal(t, rule.ID, overlay.RuleID)
		assert.Equal(t, "TotalAssets", overlay.Field)
		assert.Equal(t, "subtract", overlay.Operation)
		assert.Equal(t, 500_000.0, overlay.Amount, "overlay amount must equal original goodwill")
		assert.Equal(t, entities.AmountIncremental, overlay.AmountSemantics)
		assert.Contains(t, overlay.Reasoning, "goodwill_exclusion",
			"overlay reasoning must carry the goodwill_exclusion prefix (greppable across logs)")
		assert.Nil(t, overlay.AIProvenance, "A1 amount is deterministic — AIProvenance must be nil")

		// Flag contract: significance gate fires at >= 10% goodwill ratio.
		flag := out.Flags[0]
		assert.Equal(t, "goodwill_exclusion", flag.Type)
		assert.Equal(t, 500_000.0, flag.Amount)

		// CRITICAL invariant: Apply must NOT mutate `working`. The
		// dispatcher in ProcessAssetAdjustments performs the dual-write —
		// Apply is read-only.
		assert.Equal(t, 500_000.0, data.Goodwill, "Apply must NOT mutate data.Goodwill")
		assert.Equal(t, 1_000_000.0, data.TotalAssets, "Apply must NOT mutate data.TotalAssets")
	})

	t.Run("skip path 1 (no goodwill) emits one Fired:false LedgerEntry without metrics", func(t *testing.T) {
		data := &entities.FinancialData{
			Goodwill:    0.0,
			TotalAssets: 1_000_000.0,
		}

		out, err := adj.ApplyA1Goodwill(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		// AdjusterOutput contract for the no-goodwill skip path:
		require.Len(t, out.LedgerEntries, 1, "skip path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays, "skip path emits no OverlaySpec")
		assert.Empty(t, out.Flags, "skip path emits no Flags")

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, adjusterIDA1GoodwillExclusion, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.Equal(t, "No goodwill present to adjust", entry.SkipReason,
			"no-goodwill skip path must use the canonical SkipReason string")
		assert.NotEmpty(t, entry.Reasoning, "Reasoning must be populated even on skip")
		assert.Empty(t, entry.SkipMetrics,
			"no-goodwill skip path does not carry SkipMetrics — only the threshold-failed path does")
		// Plan §3.6.6: skipped entries carry zero monetary deltas.
		assert.Zero(t, entry.DeltaAmount)
		assert.Zero(t, entry.EquityOffset)
		assert.Zero(t, entry.TaxShieldDTA)
	})

	t.Run("skip path 2 (below threshold) emits one Fired:false LedgerEntry with SkipMetrics", func(t *testing.T) {
		// Goodwill ratio = 30_000 / 1_000_000 = 3% — below the 5% threshold.
		data := &entities.FinancialData{
			Goodwill:    30_000.0,
			TotalAssets: 1_000_000.0,
		}

		out, err := adj.ApplyA1Goodwill(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		assert.Empty(t, out.Overlays)
		assert.Empty(t, out.Flags)

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, adjusterIDA1GoodwillExclusion, entry.AdjusterID)
		assert.Contains(t, entry.SkipReason, "below threshold",
			"threshold-failed SkipReason must explain why")
		// SkipMetrics carries the ratio + threshold so dashboards can
		// chart "how close was A1 to firing?" without re-parsing
		// SkipReason strings.
		require.NotNil(t, entry.SkipMetrics, "threshold-failed skip path must carry SkipMetrics")
		assert.InDelta(t, 0.03, entry.SkipMetrics["goodwill_ratio"], 1e-9)
		assert.InDelta(t, 0.05, entry.SkipMetrics["threshold"], 1e-9)
	})

	t.Run("fired path below 10% ratio emits no significance Flag", func(t *testing.T) {
		// Goodwill ratio = 70_000 / 1_000_000 = 7% — fires (> 5% threshold)
		// but stays below the 10% flag threshold.
		data := &entities.FinancialData{
			Goodwill:    70_000.0,
			TotalAssets: 1_000_000.0,
		}

		out, err := adj.ApplyA1Goodwill(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		require.Len(t, out.Overlays, 1)
		assert.Empty(t, out.Flags,
			"goodwill ratio between 5%% and 10%% fires the overlay but stays below the significance flag threshold")
	})
}

// TestAssetAdjuster_ProcessAssetAdjustments_NativeA1Emission pins the
// dispatcher's contract: when goodwill_exclusion is among the input rules
// AND goodwill is present, ProcessAssetAdjustments populates
// AssetAdjustmentResult.{NativeLedgerEntries,NativeOverlays,NativelyEmittedRuleIDs}
// AND mutates data.Goodwill / data.TotalAssets exactly as before (dual-
// write preserved).
//
// This is the gateway test for the orchestrator's drain-then-shim wiring
// in service.go::applyActiveAdjustments.
func TestAssetAdjuster_ProcessAssetAdjustments_NativeA1Emission(t *testing.T) {
	aa := NewAssetAdjuster()
	data := &entities.FinancialData{
		Goodwill:    500_000.0, // 50% of assets — fired path
		TotalAssets: 1_000_000.0,
	}
	// productionGoodwillRule() (not createGoodwillRule()) — the dispatcher
	// switches on rule.ID == "goodwill_exclusion", which is the value the
	// production rules.json uses. createGoodwillRule() returns ID="A1"
	// which short-circuits via the switch's `default` arm.
	rules := []*entities.CleaningRule{productionGoodwillRule()}
	cleaningCtx := &entities.CleaningContext{}

	result := aa.ProcessAssetAdjustments(context.Background(), data, rules, cleaningCtx)
	require.NotNil(t, result)

	// DC-1 Phase 5 P5-C4: the legacy *AdjustmentResult fields were deleted.
	// The fired contract is now asserted natively: A1 emits one fired
	// LedgerEntry + one OverlaySpec carrying the goodwill amount (the
	// projected entities.Adjustment audit content is covered end-to-end by
	// TestApplyActiveAdjustments_AdjustmentsProjection_BasketParity).
	require.Len(t, result.NativeLedgerEntries, 1)
	assert.True(t, result.NativeLedgerEntries[0].Fired)

	// Phase 2 PR-2 Task 2.1 native emission contract:
	require.Len(t, result.NativeLedgerEntries, 1,
		"ProcessAssetAdjustments must surface the A1 native LedgerEntry")
	require.Len(t, result.NativeOverlays, 1,
		"ProcessAssetAdjustments must surface the A1 native OverlaySpec")
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["goodwill_exclusion"],
		"goodwill_exclusion must appear in NativelyEmittedRuleIDs so the shim skips it")

	// DC-1 Phase 4 (C-4, §8.2.1 Option A): A1 is an OverlayEmitter — its
	// dispatcher dual-write (data.Goodwill=0; data.TotalAssets-=goodwill) is
	// DELETED. The goodwill-exclusion effect is realized at the view level by
	// InvestedCapital(); the entity's Goodwill/TotalAssets stay as-reported.
	// The OverlaySpec carries the goodwill amount for the view to consume.
	assert.Equal(t, 500_000.0, data.Goodwill,
		"Phase 4 §8.2.1 Option A: dispatcher must NOT zero data.Goodwill (effect moves to InvestedCapital())")
	assert.Equal(t, 1_000_000.0, data.TotalAssets,
		"Phase 4 §8.2.1 Option A: dispatcher must NOT mutate data.TotalAssets")
	assert.InDelta(t, 500_000.0, result.NativeOverlays[0].Amount, 1e-6,
		"A1 OverlaySpec carries the goodwill amount for InvestedCapital() to subtract")
	assert.Equal(t, "TotalAssets", result.NativeOverlays[0].Field,
		"A1 overlay targets TotalAssets (Damodaran goodwill exclusion)")
}

// TestAssetAdjuster_ProcessAssetAdjustments_NativeA1SkipPath confirms that
// even on the skip path (no goodwill present), ProcessAssetAdjustments
// surfaces the Fired:false LedgerEntry through NativeLedgerEntries — the
// shim then skips emitting its own generic skip entry for the same rule.
func TestAssetAdjuster_ProcessAssetAdjustments_NativeA1SkipPath(t *testing.T) {
	aa := NewAssetAdjuster()
	data := &entities.FinancialData{
		Goodwill:    0.0,
		TotalAssets: 1_000_000.0,
	}
	// productionGoodwillRule() so the dispatcher actually routes through
	// the goodwill_exclusion case rather than `default: continue`.
	rules := []*entities.CleaningRule{productionGoodwillRule()}

	result := aa.ProcessAssetAdjustments(context.Background(), data, rules, &entities.CleaningContext{})
	require.NotNil(t, result)

	// DC-1 Phase 5 P5-C4: skip contract asserted natively — no fired entry.

	// Native emission contract — skip path still emits a Fired:false entry.
	require.Len(t, result.NativeLedgerEntries, 1,
		"skip path must still surface a Fired:false native LedgerEntry")
	assert.False(t, result.NativeLedgerEntries[0].Fired)
	assert.Empty(t, result.NativeOverlays, "skip path emits no Overlays")
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["goodwill_exclusion"],
		"goodwill_exclusion must appear in NativelyEmittedRuleIDs even on skip path")

	// Dual-write contract — skip path must NOT mutate balance-sheet fields.
	assert.Equal(t, 0.0, data.Goodwill)
	assert.Equal(t, 1_000_000.0, data.TotalAssets)
}
