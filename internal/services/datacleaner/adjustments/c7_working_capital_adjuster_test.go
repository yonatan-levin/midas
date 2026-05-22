package adjustments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// productionWorkingCapitalRule returns a CleaningRule whose ID matches the
// production rules.json entry ("working_capital_window_dressing") so the rule
// reaches the working_capital_window_dressing branch in
// ProcessEarningsAdjustments. C7's default threshold is 15% of revenue;
// rule-configurable via Threshold.PercentageOfRevenue.
func productionWorkingCapitalRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:         "working_capital_window_dressing",
		Name:       "Working Capital Window Dressing",
		Category:   entities.EarningsNormalization,
		Adjustment: entities.Reclassify,
		Enabled:    true,
		Severity:   entities.Warning,
	}
}

// TestC7WorkingCapitalAdjuster_Adjuster_Interface_Contract pins the
// DC-1 Phase 2 PR-3 Task 3.7 acceptance gate: c7WorkingCapitalAdjuster
// satisfies the Adjuster interface AND its AdjusterOutput matches the
// FlagEmitter contract documented in ApplyC7WorkingCapital's godoc.
//
// C7 is unambiguously a FlagEmitter — the legacy
// ProcessWorkingCapitalAdjustment emits only a Flag (no entities.Adjustment,
// no balance-sheet mutation). The fired-path subtests below assert the
// FlagEmitter convention directly so any future refactor that adds a
// balance-sheet mutation will fail loudly here.
func TestC7WorkingCapitalAdjuster_Adjuster_Interface_Contract(t *testing.T) {
	ea := NewEarningsAdjuster()
	adj := NewC7WorkingCapitalAdjuster(ea)
	require.NotNil(t, adj)

	assert.Equal(t, adjusterIDC7WorkingCapital, adj.Name(),
		"c7WorkingCapitalAdjuster.Name() must equal the AdjusterID constant")

	rule := productionWorkingCapitalRule()
	cleaningCtx := &entities.CleaningContext{}

	t.Run("review fires flag (wc_ratio above 15% threshold)", func(t *testing.T) {
		// wcRatio = 200M / 1B = 20% > 15% threshold → review fires.
		data := &entities.FinancialData{
			Ticker:                   "WCDRESS",
			Revenue:                  1_000_000_000,
			WorkingCapitalAdjustment: 200_000_000,
		}
		preWCAdj := data.WorkingCapitalAdjustment
		preRevenue := data.Revenue

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1, "fired path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays, "FlagEmitter must NOT emit OverlaySpecs")
		require.Len(t, out.Flags, 1, "fired C7 emits exactly one window-dressing Flag")

		entry := out.LedgerEntries[0]
		// CANONICAL PIN of the FlagEmitter convention for C7.
		assert.False(t, entry.Fired,
			"FlagEmitter convention: LedgerEntry stays Fired:false because no balance-sheet adjustment occurred")
		assert.Equal(t, adjusterIDC7WorkingCapital, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.Equal(t, "flag-only review; no balance-sheet adjustment", entry.SkipReason,
			"fired-path SkipReason must use the canonical FlagEmitter string")

		require.NotNil(t, entry.SkipMetrics, "fired path must populate SkipMetrics")
		assert.InDelta(t, 0.20, entry.SkipMetrics["wc_ratio"], 1e-9)
		assert.InDelta(t, 0.15, entry.SkipMetrics["threshold"], 1e-9)
		assert.InDelta(t, 200_000_000.0, entry.SkipMetrics["wc_amount"], 1e-6)

		// Reasoning surfaces the legacy "Working capital window dressing" string.
		assert.Contains(t, entry.Reasoning, "Working capital window dressing")

		assert.Zero(t, entry.Component)
		assert.Zero(t, entry.DeltaAmount)
		assert.Zero(t, entry.EquityOffset)
		assert.Zero(t, entry.TaxShieldDTA)

		// Flag content checks — preserve legacy ProcessWorkingCapitalAdjustment
		// behavior bit-for-bit.
		flag := out.Flags[0]
		assert.Equal(t, "earnings_quality", flag.Type)
		assert.Equal(t, rule.Severity, flag.Severity)
		assert.Equal(t, rule.ID, flag.RuleID)
		assert.InDelta(t, 200_000_000.0, flag.Amount, 1e-6)
		assert.InDelta(t, 20.0, flag.Percentage, 1e-9)
		assert.Contains(t, flag.Description, "window dressing")
		assert.Contains(t, flag.Recommendation, "receivables")

		// Apply MUST NOT mutate working (FlagEmitter convention).
		assert.Equal(t, preWCAdj, data.WorkingCapitalAdjustment)
		assert.Equal(t, preRevenue, data.Revenue)
	})

	t.Run("review skips (no WC adjustment)", func(t *testing.T) {
		// Legacy guard: WorkingCapitalAdjustment == 0 (NOT <= 0). Zero is the
		// only "no data" sentinel; non-zero signed values both go through
		// the threshold check.
		data := &entities.FinancialData{
			Ticker:                   "NOWC",
			Revenue:                  1_000_000_000,
			WorkingCapitalAdjustment: 0,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1, "skip path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays)
		assert.Empty(t, out.Flags, "no-WC skip path emits no Flags")

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, adjusterIDC7WorkingCapital, entry.AdjusterID)
		assert.Equal(t, "No working capital adjustments detected", entry.SkipReason)
		assert.Empty(t, entry.SkipMetrics,
			"no-WC skip path does not carry SkipMetrics (only threshold-failed + fired do)")
	})

	t.Run("review skips (below threshold)", func(t *testing.T) {
		// wcRatio = 50M / 1B = 5% < 15% threshold → skip with metrics.
		data := &entities.FinancialData{
			Ticker:                   "BELOW",
			Revenue:                  1_000_000_000,
			WorkingCapitalAdjustment: 50_000_000,
		}
		preWCAdj := data.WorkingCapitalAdjustment
		preRevenue := data.Revenue

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		assert.Empty(t, out.Overlays)
		assert.Empty(t, out.Flags, "below-threshold skip path emits no Flags")

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, adjusterIDC7WorkingCapital, entry.AdjusterID)
		assert.Contains(t, entry.SkipReason, "below materiality threshold")

		require.NotNil(t, entry.SkipMetrics,
			"below-threshold skip path must carry SkipMetrics")
		assert.InDelta(t, 0.05, entry.SkipMetrics["wc_ratio"], 1e-9)
		assert.InDelta(t, 0.15, entry.SkipMetrics["threshold"], 1e-9)
		assert.InDelta(t, 50_000_000.0, entry.SkipMetrics["wc_amount"], 1e-6)

		// Mutation-freeness.
		assert.Equal(t, preWCAdj, data.WorkingCapitalAdjustment)
		assert.Equal(t, preRevenue, data.Revenue)
	})

	t.Run("rule-configurable threshold honored", func(t *testing.T) {
		// 5% wcRatio with a 3% configured threshold → review fires.
		threshold := 0.03
		ruleWithThreshold := productionWorkingCapitalRule()
		ruleWithThreshold.Threshold = &entities.ThresholdConfig{
			PercentageOfRevenue: &threshold,
		}
		data := &entities.FinancialData{
			Ticker:                   "TIGHT",
			Revenue:                  1_000_000_000,
			WorkingCapitalAdjustment: 50_000_000, // 5% > 3%
		}

		out, err := adj.Apply(context.Background(), data, ruleWithThreshold, cleaningCtx)
		require.NoError(t, err)
		require.Len(t, out.Flags, 1, "rule-configured 3% threshold should fire at 5% ratio")
		assert.InDelta(t, 0.03, out.LedgerEntries[0].SkipMetrics["threshold"], 1e-9,
			"SkipMetrics must surface the rule-configured threshold, not the 15% default")
	})

	t.Run("negative WC adjustment below threshold treated as skip", func(t *testing.T) {
		// Legacy comparison `wcRatio < threshold` is unsigned: a negative
		// ratio (-5%) is also below the 15% threshold → skip with metrics.
		// Mirrors the legacy behavior at line 1486 exactly.
		data := &entities.FinancialData{
			Ticker:                   "NEG_WC",
			Revenue:                  1_000_000_000,
			WorkingCapitalAdjustment: -50_000_000,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		assert.Empty(t, out.Flags, "negative ratio below threshold emits no Flag")
		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.InDelta(t, -0.05, entry.SkipMetrics["wc_ratio"], 1e-9)
	})
}

// TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC7FiresFlag pins the
// dispatcher's contract for the working_capital_window_dressing rule. When
// the wcRatio crosses the threshold, the dispatcher routes through
// ApplyC7WorkingCapital, the AdjusterOutput's Flag reaches result.Flags, the
// native Fired:false LedgerEntry reaches NativeLedgerEntries, the rule is
// registered as natively-emitted (so the shim skips it), AND there is NO
// balance-sheet mutation.
func TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC7FiresFlag(t *testing.T) {
	ea := NewEarningsAdjuster()
	data := &entities.FinancialData{
		Ticker:                    "WCDRESS",
		Revenue:                   1_000_000_000,
		NormalizedOperatingIncome: 500_000_000,
		WorkingCapitalAdjustment:  200_000_000, // 20% > 15%
	}
	preNOI := data.NormalizedOperatingIncome
	preWC := data.WorkingCapitalAdjustment

	rules := []*entities.CleaningRule{productionWorkingCapitalRule()}
	result := ea.ProcessEarningsAdjustments(data, rules, &entities.CleaningContext{})
	require.NotNil(t, result)

	// Legacy contract: Applied:true on fire (matches the legacy
	// ProcessWorkingCapitalAdjustment return shape), but the Adjustments
	// slice is EMPTY (NO entities.Adjustment record — C7 emits only a Flag).
	assert.True(t, result.Applied, "C7 legacy parity: Applied:true on fire")
	assert.Empty(t, result.Adjustments,
		"C7 legacy parity: Adjustments slice is EMPTY (only a Flag is emitted)")
	require.Len(t, result.Flags, 1,
		"C7's fired Flag must surface in the dispatcher result.Flags")
	assert.Equal(t, "earnings_quality", result.Flags[0].Type)

	// Native emission contract.
	require.Len(t, result.NativeLedgerEntries, 1,
		"ProcessEarningsAdjustments must surface C7's native LedgerEntry")
	assert.Empty(t, result.NativeOverlays, "FlagEmitter emits no Overlays")
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["working_capital_window_dressing"],
		"working_capital_window_dressing must appear in NativelyEmittedRuleIDs")

	// FlagEmitter shape on the native entry.
	nativeEntry := result.NativeLedgerEntries[0]
	assert.False(t, nativeEntry.Fired,
		"FlagEmitter convention: native LedgerEntry stays Fired:false through the dispatcher")
	assert.Equal(t, adjusterIDC7WorkingCapital, nativeEntry.AdjusterID)
	assert.Equal(t, "flag-only review; no balance-sheet adjustment", nativeEntry.SkipReason)
	assert.Zero(t, nativeEntry.Component)
	assert.Zero(t, nativeEntry.DeltaAmount)
	assert.Zero(t, nativeEntry.EquityOffset)

	// LOAD-BEARING regression: NO dual-write — balance-sheet field untouched.
	assert.Equal(t, preNOI, data.NormalizedOperatingIncome,
		"dispatcher must NOT mutate NormalizedOperatingIncome (FlagEmitter convention)")
	assert.Equal(t, preWC, data.WorkingCapitalAdjustment)
}

// TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC7SkipPath pins the
// no-WC-data skip-path behavior.
func TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC7SkipPath(t *testing.T) {
	ea := NewEarningsAdjuster()
	data := &entities.FinancialData{
		Ticker:                    "NOWC",
		Revenue:                   1_000_000_000,
		NormalizedOperatingIncome: 500_000_000,
		WorkingCapitalAdjustment:  0,
	}
	preNOI := data.NormalizedOperatingIncome

	rules := []*entities.CleaningRule{productionWorkingCapitalRule()}
	result := ea.ProcessEarningsAdjustments(data, rules, &entities.CleaningContext{})
	require.NotNil(t, result)

	assert.False(t, result.Applied)
	assert.Empty(t, result.Adjustments)
	assert.Empty(t, result.Flags)

	require.Len(t, result.NativeLedgerEntries, 1)
	assert.False(t, result.NativeLedgerEntries[0].Fired)
	assert.True(t, result.NativelyEmittedRuleIDs["working_capital_window_dressing"])

	// No mutation.
	assert.Equal(t, preNOI, data.NormalizedOperatingIncome)
}
