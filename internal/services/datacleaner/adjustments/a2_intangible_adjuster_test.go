package adjustments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// productionIntangibleRule returns a CleaningRule whose ID matches the
// production rules.json entry ("intangible_adjustment") so the rule reaches
// the intangible_adjustment branch in ProcessAssetAdjustments. The existing
// createIntangibleRule() helper uses ID="A2" which short-cuts via the
// rule.ID switch's `default` arm — fine for direct ProcessIntangibleAdjustment
// tests but useless for dispatcher tests.
func productionIntangibleRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:          "intangible_adjustment",
		Name:        "Indefinite-lived Intangibles Adjustment",
		Category:    entities.AssetQuality,
		Adjustment:  entities.Writedown,
		Description: "Conservative treatment of indefinite-lived intangible assets",
		Threshold: &entities.ThresholdConfig{
			PercentageOfAssets: &[]float64{0.15}[0],
		},
		Enabled: true,
	}
}

// TestA2IntangibleAdjuster_Adjuster_Interface_Contract pins the DC-1 Phase 2
// PR-2 Task 2.2 acceptance gate: a2IntangibleAdjuster satisfies the Adjuster
// interface AND its AdjusterOutput matches the spec / plan §3.5 / §4 row A2
// Restater contract for the fired + both skipped paths.
//
// The compile-time assertion `var _ Adjuster = (*a2IntangibleAdjuster)(nil)`
// in assets.go is the primary signature pin; this test exercises the
// runtime contract — every branch of ApplyA2Intangible produces an
// AdjusterOutput whose LedgerEntries (Restater-shaped) and Flags match the
// shape the orchestrator + Phase 3 view reconstruction will rely on.
func TestA2IntangibleAdjuster_Adjuster_Interface_Contract(t *testing.T) {
	// Construct through the exported factory so the test exercises the
	// public API surface the orchestrator will use.
	aa := NewAssetAdjuster()
	adj := NewA2IntangibleAdjuster(aa)
	require.NotNil(t, adj)

	// Name() contract: stable identifier consumers can join on. Locked to
	// the AdjusterID constant so a rename forces both the test and the
	// constant to move together.
	assert.Equal(t, adjusterIDA2IntangibleWritedown, adj.Name(),
		"a2IntangibleAdjuster.Name() must equal the AdjusterID constant")

	rule := createIntangibleRule()
	cleaningCtx := &entities.CleaningContext{}

	t.Run("fired path emits one Restater-shaped Fired:true LedgerEntry, no Overlays", func(t *testing.T) {
		// Intangible ratio = 300_000 / 1_000_000 = 30% — well above the 2%
		// threshold. originalIntangibles >= 300k → retentionRate = 1/3,
		// writedown = 200_000.
		data := &entities.FinancialData{
			OtherIntangibles: 300_000.0,
			TotalAssets:      1_000_000.0,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err, "Apply must not error on a well-formed fired-path input")

		// AdjusterOutput contract: exactly one fired LedgerEntry, NO Overlays
		// (Restater — direct component mutation, no analytical overlay), one
		// Flag (legacy A2 always emits one significance flag when fired).
		require.Len(t, out.LedgerEntries, 1, "fired path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays, "A2 is a Restater — must NOT emit OverlaySpecs")
		require.Len(t, out.Flags, 1, "fired-path A2 emits exactly one significance Flag")

		// LedgerEntry contract (plan §3.5 Restater role + §4 row A2):
		// Fired=true, Component:"OtherIntangibles", DeltaAmount:-writedown,
		// EquityOffset:-writedown, TaxShieldDTA:0 (Q2 deferral).
		entry := out.LedgerEntries[0]
		assert.True(t, entry.Fired, "fired-path LedgerEntry must have Fired=true")
		assert.Equal(t, adjusterIDA2IntangibleWritedown, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.NotEmpty(t, entry.Reasoning, "Reasoning must be populated for fired entries")
		assert.False(t, entry.Timestamp.IsZero(), "Timestamp must be set on fired entries")
		assert.Equal(t, "OtherIntangibles", entry.Component,
			"A2 is a Restater — Component must point at the mutated balance-sheet line")
		// writedownAmount = 300_000 * (1 - 1/3) = 200_000; DeltaAmount/EquityOffset are signed-negative.
		assert.InDelta(t, -200_000.0, entry.DeltaAmount, 1e-6,
			"DeltaAmount must equal -writedownAmount (signed reduction of OtherIntangibles)")
		assert.InDelta(t, -200_000.0, entry.EquityOffset, 1e-6,
			"EquityOffset must mirror DeltaAmount — writedowns reduce equity 1:1")
		assert.Zero(t, entry.TaxShieldDTA,
			"Q2 deferral: A2 TaxShieldDTA stays 0 in Phase 2 to preserve dual-write bit-for-bit")
		assert.Empty(t, entry.SkipReason, "SkipReason must be empty for fired entries")

		// Flag contract: severity comes from the legacy ratio bucket helper.
		flag := out.Flags[0]
		assert.Equal(t, "intangible_writedown", flag.Type)
		assert.InDelta(t, 200_000.0, flag.Amount, 1e-6, "flag amount must equal writedown magnitude")

		// CRITICAL invariant: Apply must NOT mutate `working`. The dispatcher
		// in ProcessAssetAdjustments performs the dual-write — Apply is
		// read-only.
		assert.Equal(t, 300_000.0, data.OtherIntangibles, "Apply must NOT mutate data.OtherIntangibles")
		assert.Equal(t, 1_000_000.0, data.TotalAssets, "Apply must NOT mutate data.TotalAssets")
	})

	t.Run("skip path 1 (no intangibles) emits one Fired:false LedgerEntry without metrics", func(t *testing.T) {
		data := &entities.FinancialData{
			OtherIntangibles: 0.0,
			TotalAssets:      1_000_000.0,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1, "skip path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays, "skip path emits no OverlaySpec")
		assert.Empty(t, out.Flags, "skip path emits no Flags")

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, adjusterIDA2IntangibleWritedown, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.Equal(t, "No intangible assets present to adjust", entry.SkipReason,
			"no-intangibles skip path must use the canonical SkipReason string")
		assert.NotEmpty(t, entry.Reasoning, "Reasoning must be populated even on skip")
		assert.Empty(t, entry.SkipMetrics,
			"no-intangibles skip path does not carry SkipMetrics — only the threshold-failed path does")
		// Plan §3.6.6: skipped entries carry zero monetary deltas.
		assert.Zero(t, entry.DeltaAmount)
		assert.Zero(t, entry.EquityOffset)
		assert.Zero(t, entry.TaxShieldDTA)
	})

	t.Run("skip path 2 (below threshold) emits one Fired:false LedgerEntry with SkipMetrics", func(t *testing.T) {
		// Intangible ratio = 10_000 / 1_000_000 = 1% — below the 2% threshold.
		data := &entities.FinancialData{
			OtherIntangibles: 10_000.0,
			TotalAssets:      1_000_000.0,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		assert.Empty(t, out.Overlays)
		assert.Empty(t, out.Flags)

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, adjusterIDA2IntangibleWritedown, entry.AdjusterID)
		assert.Contains(t, entry.SkipReason, "below adjustment threshold",
			"threshold-failed SkipReason must explain why")
		require.NotNil(t, entry.SkipMetrics, "threshold-failed skip path must carry SkipMetrics")
		assert.InDelta(t, 0.01, entry.SkipMetrics["intangible_ratio"], 1e-9)
		assert.InDelta(t, 0.02, entry.SkipMetrics["threshold"], 1e-9)
	})

	t.Run("fired path TaxShieldDTA stays zero per Q2 deferral", func(t *testing.T) {
		// Independent assertion of the Q2 (plan §10) deferral contract:
		// non-zero EffectiveTaxRate must NOT cause A2 to populate
		// TaxShieldDTA. Future Phase 3 work may revisit this; until then,
		// any change to Apply that starts populating TaxShieldDTA must
		// fail this test FIRST so the implementer notices the deferral
		// contract.
		data := &entities.FinancialData{
			OtherIntangibles: 250_000.0,
			TotalAssets:      1_000_000.0,
			EffectiveTaxRate: 0.21,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)
		require.Len(t, out.LedgerEntries, 1)
		entry := out.LedgerEntries[0]
		require.True(t, entry.Fired, "preconditions chosen to fire — sanity check")
		assert.Zero(t, entry.TaxShieldDTA,
			"Q2 deferral (plan §10): A2 must NOT compute TaxShieldDTA in Phase 2 even when EffectiveTaxRate is non-zero")
	})
}

// TestAssetAdjuster_ProcessAssetAdjustments_NativeA2Emission pins the
// dispatcher's contract for the intangible_adjustment rule: when present in
// the input rules AND intangibles are above threshold, ProcessAssetAdjustments
// populates NativeLedgerEntries with the A2 fired entry AND mutates
// data.OtherIntangibles / data.TotalAssets exactly as before (dual-write
// preserved).
func TestAssetAdjuster_ProcessAssetAdjustments_NativeA2Emission(t *testing.T) {
	aa := NewAssetAdjuster()
	data := &entities.FinancialData{
		OtherIntangibles: 300_000.0, // 30% of assets — fired, retention 1/3
		TotalAssets:      1_000_000.0,
	}
	rules := []*entities.CleaningRule{productionIntangibleRule()}
	cleaningCtx := &entities.CleaningContext{}

	result := aa.ProcessAssetAdjustments(context.Background(), data, rules, cleaningCtx)
	require.NotNil(t, result)

	// Legacy contract: Applied=true, one Adjustment, Adjustments[0].Amount
	// equals the writedown magnitude (200_000). The legacy *AdjustmentResult
	// shape stays unchanged so callers that don't know about the Adjuster
	// interface keep working.
	assert.True(t, result.Applied)
	require.Len(t, result.Adjustments, 1)
	assert.InDelta(t, 200_000.0, result.Adjustments[0].Amount, 1e-6)
	assert.Equal(t, "IntangibleAssets", result.Adjustments[0].FromAccount)
	assert.Equal(t, "IntangibleWritedown", result.Adjustments[0].ToAccount)
	assert.Equal(t, entities.Writedown, result.Adjustments[0].Type)

	// Phase 2 PR-2 Task 2.2 native emission contract:
	require.Len(t, result.NativeLedgerEntries, 1,
		"ProcessAssetAdjustments must surface the A2 native LedgerEntry")
	assert.Empty(t, result.NativeOverlays, "A2 is a Restater — no OverlaySpec native emission")
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["intangible_adjustment"],
		"intangible_adjustment must appear in NativelyEmittedRuleIDs so the shim skips it")

	// Restater shape on the native entry:
	nativeEntry := result.NativeLedgerEntries[0]
	assert.True(t, nativeEntry.Fired)
	assert.Equal(t, adjusterIDA2IntangibleWritedown, nativeEntry.AdjusterID)
	assert.Equal(t, "OtherIntangibles", nativeEntry.Component)
	assert.InDelta(t, -200_000.0, nativeEntry.DeltaAmount, 1e-6)
	assert.InDelta(t, -200_000.0, nativeEntry.EquityOffset, 1e-6)
	assert.Zero(t, nativeEntry.TaxShieldDTA, "Q2 deferral: TaxShieldDTA stays 0 on dispatcher path too")

	// Dual-write preserved — data was mutated as the legacy code did.
	// retainedAmount = 300_000 / 3 = 100_000; writedown = 200_000.
	assert.InDelta(t, 100_000.0, data.OtherIntangibles, 1e-6,
		"dispatcher must set data.OtherIntangibles to retained amount (dual-write)")
	assert.InDelta(t, 800_000.0, data.TotalAssets, 1e-6,
		"dispatcher must subtract writedown from data.TotalAssets (dual-write)")
}

// TestAssetAdjuster_ProcessAssetAdjustments_NativeA2SkipPath confirms that
// even on the skip path (no intangibles present), ProcessAssetAdjustments
// surfaces the Fired:false LedgerEntry through NativeLedgerEntries and
// performs NO mutation — the shim then skips emitting its own generic skip
// entry for the same rule.
func TestAssetAdjuster_ProcessAssetAdjustments_NativeA2SkipPath(t *testing.T) {
	aa := NewAssetAdjuster()
	data := &entities.FinancialData{
		OtherIntangibles: 0.0,
		TotalAssets:      1_000_000.0,
	}
	rules := []*entities.CleaningRule{productionIntangibleRule()}

	result := aa.ProcessAssetAdjustments(context.Background(), data, rules, &entities.CleaningContext{})
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
	assert.True(t, result.NativelyEmittedRuleIDs["intangible_adjustment"],
		"intangible_adjustment must appear in NativelyEmittedRuleIDs even on skip path")

	// Dual-write contract — skip path must NOT mutate balance-sheet fields.
	assert.Equal(t, 0.0, data.OtherIntangibles)
	assert.Equal(t, 1_000_000.0, data.TotalAssets)
}
