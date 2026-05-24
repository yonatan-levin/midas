package adjustments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// productionRDCapitalizationReviewRule returns a CleaningRule whose ID matches
// the production rule emitted by the rules engine
// ("rd_capitalization_review") so the rule reaches the
// rd_capitalization_review branch in ProcessAssetAdjustments. Mirrors the
// productionObsoleteInventoryRule helper next to A5's test file.
func productionRDCapitalizationReviewRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:          "rd_capitalization_review",
		Name:        "R&D Capitalization Review",
		Category:    entities.AssetQuality,
		Adjustment:  entities.Reclassify,
		Description: "Flag potentially inappropriate capitalization of R&D",
		Threshold: &entities.ThresholdConfig{
			PercentageOfRevenue: &[]float64{0.10}[0],
		},
		Enabled: true,
	}
}

// productionCapitalizedSoftwareReviewRule returns a CleaningRule whose ID
// matches the production rules.json entry ("capitalized_software") so the rule
// reaches the capitalized_software branch in ProcessAssetAdjustments.
func productionCapitalizedSoftwareReviewRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:          "capitalized_software",
		Name:        "Capitalized Software Development",
		Category:    entities.AssetQuality,
		Adjustment:  entities.Reclassify,
		Description: "Flag potentially inappropriate capitalization of software",
		Threshold: &entities.ThresholdConfig{
			PercentageOfRevenue: &[]float64{0.015}[0],
		},
		Enabled: true,
	}
}

// TestARDCapitalizationReviewAdjuster_Adjuster_Interface_Contract pins the
// DC-1 Phase 2 PR-2 Task 2.5 acceptance gate: aRDCapitalizationReviewAdjuster
// satisfies the Adjuster interface AND its AdjusterOutput matches the spec /
// plan §3.5 / §7 Task 2.5 FlagEmitter contract for the fired path (review
// triggers, AdjusterOutput.Flags carries the firing signal) and both skipped
// paths.
//
// The compile-time assertion
// `var _ Adjuster = (*aRDCapitalizationReviewAdjuster)(nil)` in assets.go is
// the primary signature pin; this test exercises the runtime contract — every
// branch of ApplyARDCapitalizationReview produces an AdjusterOutput whose
// LedgerEntries stay Fired:false (per the FlagEmitter convention: no balance-
// sheet adjustment happens) while the Flags slice carries the actual firing
// signal when the review's 10% R&D/Revenue threshold trips.
func TestARDCapitalizationReviewAdjuster_Adjuster_Interface_Contract(t *testing.T) {
	aa := NewAssetAdjuster()
	adj := NewARDCapitalizationReviewAdjuster(aa)
	require.NotNil(t, adj)

	// Name() contract: stable identifier consumers can join on.
	assert.Equal(t, adjusterIDARDCapitalizationReview, adj.Name(),
		"aRDCapitalizationReviewAdjuster.Name() must equal the AdjusterID constant")

	rule := productionRDCapitalizationReviewRule()
	cleaningCtx := createTechContext()

	t.Run("review fires flag (ratio above 10% threshold)", func(t *testing.T) {
		// rdRatio = 200_000 / 1_000_000 = 20% > 10% threshold → review fires.
		data := &entities.FinancialData{
			ResearchAndDevelopment: 200_000.0,
			Revenue:                1_000_000.0,
		}
		// Snapshot pre-Apply state to assert mutation-freeness post-call.
		preRD := data.ResearchAndDevelopment
		preRevenue := data.Revenue

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err, "Apply must not error on a well-formed fired-path input")

		require.Len(t, out.LedgerEntries, 1, "fired path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays, "flag-only review must NOT emit OverlaySpecs")
		require.Len(t, out.Flags, 1, "fired review emits exactly one Critical-severity flag")

		entry := out.LedgerEntries[0]
		// CANONICAL PIN of the FlagEmitter convention: even when the review
		// fires its flag, the LedgerEntry stays Fired:false because no
		// balance-sheet adjustment happened. The populated Flags slice IS
		// the firing signal.
		assert.False(t, entry.Fired,
			"FlagEmitter convention: LedgerEntry stays Fired:false because no balance-sheet adjustment occurred")
		assert.Equal(t, adjusterIDARDCapitalizationReview, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.Equal(t, "flag-only review; no balance-sheet adjustment", entry.SkipReason,
			"fired-path SkipReason must use the canonical FlagEmitter string")

		// SkipMetrics carry the threshold-evaluation diagnostics for dashboards.
		require.NotNil(t, entry.SkipMetrics, "fired path must populate SkipMetrics")
		assert.InDelta(t, 0.20, entry.SkipMetrics["rd_ratio"], 1e-9)
		assert.InDelta(t, 0.10, entry.SkipMetrics["threshold"], 1e-9)
		assert.InDelta(t, 200_000.0, entry.SkipMetrics["rd_amount"], 1e-6)

		// Reasoning surfaces the legacy "flagged for review" string so log
		// greps continue to work across the migration.
		assert.Contains(t, entry.Reasoning, "rd_capitalization_review",
			"Reasoning must carry the legacy review identifier for log-grep parity")
		assert.Contains(t, entry.Reasoning, "flagged for review")

		// Plan §3.6.6: even though the review fired its flag, the LedgerEntry's
		// monetary fields stay at zero (no balance-sheet adjustment occurred).
		assert.Zero(t, entry.Component)
		assert.Zero(t, entry.DeltaAmount)
		assert.Zero(t, entry.EquityOffset)
		assert.Zero(t, entry.TaxShieldDTA)

		// Flag content checks — preserve legacy ProcessRDCapitalizationReview
		// behavior bit-for-bit.
		flag := out.Flags[0]
		assert.Equal(t, "rd_capitalization_review", flag.Type)
		assert.Equal(t, entities.FlagSeverityCritical, flag.Severity)
		assert.Equal(t, rule.ID, flag.RuleID)
		assert.InDelta(t, 200_000.0, flag.Amount, 1e-6)
		assert.InDelta(t, 20.0, flag.Percentage, 1e-9)

		// Apply MUST NOT mutate working — this review never touches the
		// balance sheet on any path.
		assert.Equal(t, preRD, data.ResearchAndDevelopment,
			"Apply must NOT mutate data.ResearchAndDevelopment")
		assert.Equal(t, preRevenue, data.Revenue,
			"Apply must NOT mutate data.Revenue")
	})

	t.Run("review skips (no R&D)", func(t *testing.T) {
		data := &entities.FinancialData{
			ResearchAndDevelopment: 0.0,
			Revenue:                1_000_000.0,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1, "skip path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays, "skip path emits no OverlaySpec")
		assert.Empty(t, out.Flags, "no-R&D skip path emits no Flags")

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, adjusterIDARDCapitalizationReview, entry.AdjusterID)
		assert.Equal(t, "No R&D expenses present to review", entry.SkipReason,
			"no-R&D skip path must use the canonical SkipReason string")
		assert.Empty(t, entry.SkipMetrics,
			"no-R&D skip path does not carry SkipMetrics — only the threshold-failed + fired paths do")

		// Mutation-freeness on the skip path too.
		assert.Zero(t, data.ResearchAndDevelopment)
		assert.Equal(t, 1_000_000.0, data.Revenue)
	})

	t.Run("review skips (below threshold)", func(t *testing.T) {
		// rdRatio = 50_000 / 1_000_000 = 5% < 10% threshold → skip with metrics.
		data := &entities.FinancialData{
			ResearchAndDevelopment: 50_000.0,
			Revenue:                1_000_000.0,
		}
		preRD := data.ResearchAndDevelopment
		preRevenue := data.Revenue

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		assert.Empty(t, out.Overlays)
		assert.Empty(t, out.Flags, "below-threshold skip path emits no Flags")

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, adjusterIDARDCapitalizationReview, entry.AdjusterID)
		assert.Contains(t, entry.SkipReason, "below review threshold",
			"below-threshold SkipReason must explain why")

		require.NotNil(t, entry.SkipMetrics,
			"below-threshold skip path must carry SkipMetrics")
		assert.InDelta(t, 0.05, entry.SkipMetrics["rd_ratio"], 1e-9)
		assert.InDelta(t, 0.10, entry.SkipMetrics["threshold"], 1e-9)

		// Mutation-freeness.
		assert.Equal(t, preRD, data.ResearchAndDevelopment)
		assert.Equal(t, preRevenue, data.Revenue)
	})
}

// TestACapitalizedSoftwareReviewAdjuster_Adjuster_Interface_Contract mirrors
// the R&D review test above for the capitalized-software review. Same
// FlagEmitter convention (Fired:false on every branch; Flags carries the
// firing signal); different threshold (1.5% intangibles/revenue) and different
// flag severity (Warning vs. Critical).
func TestACapitalizedSoftwareReviewAdjuster_Adjuster_Interface_Contract(t *testing.T) {
	aa := NewAssetAdjuster()
	adj := NewACapitalizedSoftwareReviewAdjuster(aa)
	require.NotNil(t, adj)

	assert.Equal(t, adjusterIDACapitalizedSoftwareReview, adj.Name(),
		"aCapitalizedSoftwareReviewAdjuster.Name() must equal the AdjusterID constant")

	rule := productionCapitalizedSoftwareReviewRule()
	cleaningCtx := createTechContext()

	t.Run("review fires flag (ratio above 1.5% threshold)", func(t *testing.T) {
		// intangibleRatio = 30_000 / 1_000_000 = 3% > 1.5% threshold → fires.
		data := &entities.FinancialData{
			OtherIntangibles: 30_000.0,
			Revenue:          1_000_000.0,
		}
		preIntangibles := data.OtherIntangibles
		preRevenue := data.Revenue

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1, "fired path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays)
		require.Len(t, out.Flags, 1, "fired review emits exactly one Warning-severity flag")

		entry := out.LedgerEntries[0]
		// CANONICAL PIN of the FlagEmitter convention for capitalized-software too.
		assert.False(t, entry.Fired,
			"FlagEmitter convention: Fired:false even when the flag fires")
		assert.Equal(t, adjusterIDACapitalizedSoftwareReview, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.Equal(t, "flag-only review; no balance-sheet adjustment", entry.SkipReason)

		require.NotNil(t, entry.SkipMetrics)
		assert.InDelta(t, 0.03, entry.SkipMetrics["intangible_ratio"], 1e-9)
		assert.InDelta(t, 0.015, entry.SkipMetrics["threshold"], 1e-9)
		assert.InDelta(t, 30_000.0, entry.SkipMetrics["intangible_amount"], 1e-6)

		assert.Contains(t, entry.Reasoning, "capitalized_software")
		assert.Contains(t, entry.Reasoning, "flagged for software review")

		assert.Zero(t, entry.Component)
		assert.Zero(t, entry.DeltaAmount)
		assert.Zero(t, entry.EquityOffset)
		assert.Zero(t, entry.TaxShieldDTA)

		flag := out.Flags[0]
		assert.Equal(t, "capitalized_software", flag.Type)
		assert.Equal(t, entities.Warning, flag.Severity)
		assert.InDelta(t, 30_000.0, flag.Amount, 1e-6)
		assert.InDelta(t, 3.0, flag.Percentage, 1e-9)

		// Mutation-freeness.
		assert.Equal(t, preIntangibles, data.OtherIntangibles)
		assert.Equal(t, preRevenue, data.Revenue)
	})

	t.Run("review skips (no intangibles)", func(t *testing.T) {
		data := &entities.FinancialData{
			OtherIntangibles: 0.0,
			Revenue:          1_000_000.0,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		assert.Empty(t, out.Overlays)
		assert.Empty(t, out.Flags)

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, adjusterIDACapitalizedSoftwareReview, entry.AdjusterID)
		assert.Equal(t, "No intangible assets present that might include capitalized software", entry.SkipReason)
		assert.Empty(t, entry.SkipMetrics)
	})

	t.Run("review skips (below threshold)", func(t *testing.T) {
		// intangibleRatio = 10_000 / 1_000_000 = 1% < 1.5% threshold → skip.
		data := &entities.FinancialData{
			OtherIntangibles: 10_000.0,
			Revenue:          1_000_000.0,
		}
		preIntangibles := data.OtherIntangibles
		preRevenue := data.Revenue

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		assert.Empty(t, out.Overlays)
		assert.Empty(t, out.Flags)

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, adjusterIDACapitalizedSoftwareReview, entry.AdjusterID)
		assert.Contains(t, entry.SkipReason, "below software review threshold")

		require.NotNil(t, entry.SkipMetrics)
		assert.InDelta(t, 0.01, entry.SkipMetrics["intangible_ratio"], 1e-9)
		assert.InDelta(t, 0.015, entry.SkipMetrics["threshold"], 1e-9)

		// Mutation-freeness.
		assert.Equal(t, preIntangibles, data.OtherIntangibles)
		assert.Equal(t, preRevenue, data.Revenue)
	})
}

// TestAssetAdjuster_ProcessAssetAdjustments_NativeFlagOnlyReviews_RDFiresFlag
// pins the dispatcher's contract for the rd_capitalization_review rule: when
// the review's 10% threshold trips, ProcessAssetAdjustments surfaces the
// Fired:false LedgerEntry + the flag through NativeLedgerEntries / legacy
// result.Flags, marks the rule as natively-emitted (so the shim skips it),
// AND performs NO balance-sheet mutation (the FlagEmitter convention).
func TestAssetAdjuster_ProcessAssetAdjustments_NativeFlagOnlyReviews_RDFiresFlag(t *testing.T) {
	aa := NewAssetAdjuster()
	// rdRatio = 200_000 / 1_000_000 = 20% > 10% → review fires.
	data := &entities.FinancialData{
		ResearchAndDevelopment: 200_000.0,
		Revenue:                1_000_000.0,
		TotalAssets:            2_000_000.0,
	}
	preRD := data.ResearchAndDevelopment
	preRevenue := data.Revenue
	preTotalAssets := data.TotalAssets

	rules := []*entities.CleaningRule{productionRDCapitalizationReviewRule()}
	result := aa.ProcessAssetAdjustments(data, rules, createTechContext())
	require.NotNil(t, result)

	// Legacy contract: Applied=false (flag-only — no balance-sheet adjustment
	// happened), no Adjustments, but Flags non-empty.
	assert.False(t, result.Applied,
		"flag-only review never sets Applied=true on the legacy result")
	assert.Empty(t, result.Adjustments,
		"flag-only review emits no entities.Adjustment records")
	require.Len(t, result.Flags, 1, "flag-only review's fired flag must surface in result.Flags")
	assert.Equal(t, "rd_capitalization_review", result.Flags[0].Type)

	// Native emission contract: even though no balance-sheet mutation, the
	// Fired:false LedgerEntry + native rule-ID registration must reach the
	// orchestrator so the shim skips this rule.
	require.Len(t, result.NativeLedgerEntries, 1,
		"ProcessAssetAdjustments must surface the R&D review's native LedgerEntry")
	assert.Empty(t, result.NativeOverlays, "flag-only review emits no Overlays")
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["rd_capitalization_review"],
		"rd_capitalization_review must appear in NativelyEmittedRuleIDs so the shim skips it")

	// FlagEmitter shape on the native entry: Fired:false even when the flag
	// fires, populated Flags is the firing signal, no monetary deltas.
	nativeEntry := result.NativeLedgerEntries[0]
	assert.False(t, nativeEntry.Fired,
		"FlagEmitter convention: native LedgerEntry stays Fired:false through the dispatcher")
	assert.Equal(t, adjusterIDARDCapitalizationReview, nativeEntry.AdjusterID)
	assert.Equal(t, "flag-only review; no balance-sheet adjustment", nativeEntry.SkipReason)
	assert.Zero(t, nativeEntry.Component)
	assert.Zero(t, nativeEntry.DeltaAmount)
	assert.Zero(t, nativeEntry.EquityOffset)

	// No dual-write — balance-sheet fields untouched.
	assert.Equal(t, preRD, data.ResearchAndDevelopment,
		"dispatcher must NOT mutate ResearchAndDevelopment (flag-only review)")
	assert.Equal(t, preRevenue, data.Revenue,
		"dispatcher must NOT mutate Revenue (flag-only review)")
	assert.Equal(t, preTotalAssets, data.TotalAssets,
		"dispatcher must NOT mutate TotalAssets (flag-only review)")
}

// TestAssetAdjuster_ProcessAssetAdjustments_NativeFlagOnlyReviews_SoftwareFiresFlag
// pins the dispatcher's contract for the capitalized_software rule. Mirrors
// the R&D test above — Applied:false, Flags populated, NativeLedgerEntries
// carries a Fired:false entry, no balance-sheet mutation.
func TestAssetAdjuster_ProcessAssetAdjustments_NativeFlagOnlyReviews_SoftwareFiresFlag(t *testing.T) {
	aa := NewAssetAdjuster()
	// intangibleRatio = 30_000 / 1_000_000 = 3% > 1.5% → fires.
	data := &entities.FinancialData{
		OtherIntangibles: 30_000.0,
		Revenue:          1_000_000.0,
		TotalAssets:      2_000_000.0,
	}
	preIntangibles := data.OtherIntangibles
	preRevenue := data.Revenue
	preTotalAssets := data.TotalAssets

	rules := []*entities.CleaningRule{productionCapitalizedSoftwareReviewRule()}
	result := aa.ProcessAssetAdjustments(data, rules, createTechContext())
	require.NotNil(t, result)

	assert.False(t, result.Applied)
	assert.Empty(t, result.Adjustments)
	require.Len(t, result.Flags, 1)
	assert.Equal(t, "capitalized_software", result.Flags[0].Type)
	assert.Equal(t, entities.Warning, result.Flags[0].Severity)

	require.Len(t, result.NativeLedgerEntries, 1)
	assert.Empty(t, result.NativeOverlays)
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["capitalized_software"])

	nativeEntry := result.NativeLedgerEntries[0]
	assert.False(t, nativeEntry.Fired)
	assert.Equal(t, adjusterIDACapitalizedSoftwareReview, nativeEntry.AdjusterID)
	assert.Equal(t, "flag-only review; no balance-sheet adjustment", nativeEntry.SkipReason)
	assert.Zero(t, nativeEntry.Component)
	assert.Zero(t, nativeEntry.DeltaAmount)
	assert.Zero(t, nativeEntry.EquityOffset)

	// No dual-write — balance-sheet fields untouched.
	assert.Equal(t, preIntangibles, data.OtherIntangibles)
	assert.Equal(t, preRevenue, data.Revenue)
	assert.Equal(t, preTotalAssets, data.TotalAssets)
}

// TestAssetAdjuster_ProcessAssetAdjustments_NativeFlagOnlyReviews_SkipPathNativeEmission
// confirms that even on the skip paths (no R&D / below threshold), the
// dispatcher still surfaces the Fired:false LedgerEntry through
// NativeLedgerEntries and registers the rule as natively-emitted. This is
// load-bearing for "did the cleaner consider this review for this ticker?"
// observability — without the skip-path native emission, the shim would emit
// a duplicate generic skip entry.
func TestAssetAdjuster_ProcessAssetAdjustments_NativeFlagOnlyReviews_SkipPathNativeEmission(t *testing.T) {
	aa := NewAssetAdjuster()
	data := &entities.FinancialData{
		ResearchAndDevelopment: 0.0, // No R&D → skip path 1.
		OtherIntangibles:       0.0, // No intangibles → skip path 1 for software too.
		Revenue:                1_000_000.0,
		TotalAssets:            2_000_000.0,
	}
	rules := []*entities.CleaningRule{
		productionRDCapitalizationReviewRule(),
		productionCapitalizedSoftwareReviewRule(),
	}

	result := aa.ProcessAssetAdjustments(data, rules, createTechContext())
	require.NotNil(t, result)

	// Both reviews skipped — no flags surface, no adjustments.
	assert.False(t, result.Applied)
	assert.Empty(t, result.Adjustments)
	assert.Empty(t, result.Flags,
		"both reviews skipped → no flags surface to the legacy result")

	// Both reviews still surface a Fired:false native LedgerEntry — load-
	// bearing for "the cleaner considered these reviews on this ticker"
	// observability.
	require.Len(t, result.NativeLedgerEntries, 2,
		"both flag-only reviews must surface a Fired:false native entry on the skip path")
	assert.Empty(t, result.NativeOverlays)
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["rd_capitalization_review"],
		"rd_capitalization_review must appear in NativelyEmittedRuleIDs on skip path too")
	assert.True(t, result.NativelyEmittedRuleIDs["capitalized_software"],
		"capitalized_software must appear in NativelyEmittedRuleIDs on skip path too")

	for _, entry := range result.NativeLedgerEntries {
		assert.False(t, entry.Fired, "skip-path entries stay Fired:false")
		assert.Empty(t, entry.SkipMetrics,
			"no-numerator skip path emits no SkipMetrics (only the threshold-failed + fired paths do)")
	}
}
