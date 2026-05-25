package adjustments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// productionStockCompensationRule returns a CleaningRule whose ID matches the
// production rules.json entry ("stock_compensation") so the rule reaches the
// stock_compensation branch in ProcessEarningsAdjustments. C4 has NO threshold
// gate in legacy code — every positive SBC fires the dilution flag.
func productionStockCompensationRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:         "stock_compensation",
		Name:       "Stock-Based Compensation",
		Category:   entities.EarningsNormalization,
		Adjustment: entities.Reclassify,
		Enabled:    true,
		Severity:   entities.Info,
	}
}

// TestC4StockCompensationAdjuster_Adjuster_Interface_Contract pins the
// DC-1 Phase 2 PR-3 Task 3.4 acceptance gate: c4StockCompensationAdjuster
// satisfies the Adjuster interface AND its AdjusterOutput matches the
// FlagEmitter contract documented in ApplyC4StockCompensation's godoc.
//
// PLAN-VS-CODE DISAGREEMENT (pinned here as a regression): the original PR-3
// plan §7 Task 3.4 row called C4 "same pattern as C1" (Restater), but legacy
// ProcessStockCompensationAdjustment does NOT mutate the balance sheet. The
// fired-path subtests below assert the FlagEmitter convention (Fired:false +
// non-empty Flags as the firing signal) directly so any future refactor that
// adds a `data.NormalizedOperatingIncome += sbcAmount` mutation will fail
// loudly here.
func TestC4StockCompensationAdjuster_Adjuster_Interface_Contract(t *testing.T) {
	ea := NewEarningsAdjuster()
	adj := NewC4StockCompensationAdjuster(ea)
	require.NotNil(t, adj)

	assert.Equal(t, adjusterIDC4StockCompensation, adj.Name(),
		"c4StockCompensationAdjuster.Name() must equal the AdjusterID constant")

	rule := productionStockCompensationRule()
	cleaningCtx := &entities.CleaningContext{}

	t.Run("review fires flag (positive SBC — no threshold gate)", func(t *testing.T) {
		// sbcRatio = 200M / 5B = 4% — fires regardless because legacy has no
		// materiality threshold for C4.
		data := &entities.FinancialData{
			Ticker:                    "TECH",
			Revenue:                   5_000_000_000,
			NormalizedOperatingIncome: 1_000_000_000,
			StockBasedCompensation:    200_000_000,
		}
		// Snapshot pre-Apply state to assert mutation-freeness post-call.
		preNOI := data.NormalizedOperatingIncome
		preSBC := data.StockBasedCompensation
		preRevenue := data.Revenue

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1, "fired path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays, "FlagEmitter must NOT emit OverlaySpecs")
		require.Len(t, out.Flags, 1, "fired C4 emits exactly one dilution Flag")

		entry := out.LedgerEntries[0]
		// CANONICAL PIN of the FlagEmitter convention for C4: Fired:false even
		// when the flag fires because no balance-sheet adjustment occurred.
		// The populated Flags slice IS the firing signal.
		assert.False(t, entry.Fired,
			"FlagEmitter convention: LedgerEntry stays Fired:false because no balance-sheet adjustment occurred")
		assert.Equal(t, adjusterIDC4StockCompensation, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.Equal(t, "flag-only review; no balance-sheet adjustment", entry.SkipReason,
			"fired-path SkipReason must use the canonical FlagEmitter string")

		// SkipMetrics carry the SBC ratio + amount for dashboards.
		require.NotNil(t, entry.SkipMetrics, "fired path must populate SkipMetrics")
		assert.InDelta(t, 200_000_000.0, entry.SkipMetrics["sbc_amount"], 1e-6)
		assert.InDelta(t, 0.04, entry.SkipMetrics["sbc_ratio"], 1e-9)

		// Reasoning surfaces the legacy formatting for log-grep parity.
		assert.Contains(t, entry.Reasoning, "Stock-based compensation adjustment")

		// FlagEmitter shape: no monetary deltas.
		assert.Zero(t, entry.Component)
		assert.Zero(t, entry.DeltaAmount)
		assert.Zero(t, entry.EquityOffset)
		assert.Zero(t, entry.TaxShieldDTA)

		// Flag content checks — preserve legacy ProcessStockCompensationAdjustment
		// behavior bit-for-bit.
		flag := out.Flags[0]
		assert.Equal(t, "earnings_quality", flag.Type)
		assert.Equal(t, rule.Severity, flag.Severity)
		assert.Equal(t, rule.ID, flag.RuleID)
		assert.InDelta(t, 200_000_000.0, flag.Amount, 1e-6)
		assert.InDelta(t, 4.0, flag.Percentage, 1e-9)
		assert.Contains(t, flag.Description, "dilution")
		assert.Contains(t, flag.Recommendation, "dilution")

		// Apply MUST NOT mutate working — C4 never touches the balance sheet
		// on any path (FlagEmitter convention).
		assert.Equal(t, preNOI, data.NormalizedOperatingIncome,
			"Apply must NOT mutate data.NormalizedOperatingIncome (FlagEmitter)")
		assert.Equal(t, preSBC, data.StockBasedCompensation)
		assert.Equal(t, preRevenue, data.Revenue)
	})

	t.Run("review skips (no SBC)", func(t *testing.T) {
		data := &entities.FinancialData{
			Ticker:                 "NOSCC",
			Revenue:                5_000_000_000,
			StockBasedCompensation: 0,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1, "skip path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays)
		assert.Empty(t, out.Flags, "no-SBC skip path emits no Flags")

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, adjusterIDC4StockCompensation, entry.AdjusterID)
		assert.Equal(t, "No stock-based compensation to adjust", entry.SkipReason)
		assert.Empty(t, entry.SkipMetrics,
			"no-SBC skip path does not carry SkipMetrics (only the fired path does for C4)")
	})

	t.Run("review skips (negative SBC — legacy <= 0 guard)", func(t *testing.T) {
		// Legacy guard is `<= 0` (not `== 0`); a negative SBC (data-quality
		// bug) follows the skip branch identically.
		data := &entities.FinancialData{
			Ticker:                 "BADDATA",
			Revenue:                5_000_000_000,
			StockBasedCompensation: -5_000_000,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Empty(t, out.Flags)
	})

	t.Run("review fires with zero Revenue (sbc_ratio surfaces as 0; no divide-by-zero)", func(t *testing.T) {
		// Defensive check: legacy code at lines 1343/1357 divides by Revenue
		// without a guard, which would produce +Inf for Revenue=0 tickers.
		// Apply uses an `if Revenue > 0` guard so SkipMetrics["sbc_ratio"]
		// surfaces as 0 instead of +Inf, sidestepping the legacy data-quality
		// concern while still firing the flag (since SBC>0).
		data := &entities.FinancialData{
			Ticker:                 "PRE_REV",
			Revenue:                0,
			StockBasedCompensation: 1_000_000,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		require.Len(t, out.Flags, 1, "C4 still fires when SBC>0, regardless of Revenue")
		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired, "FlagEmitter convention persists across all fired branches")
		assert.InDelta(t, 1_000_000.0, entry.SkipMetrics["sbc_amount"], 1e-6)
		assert.Zero(t, entry.SkipMetrics["sbc_ratio"], "Revenue<=0 guard surfaces ratio as 0, not +Inf")
	})
}

// TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC4FiresFlag pins the
// dispatcher's contract for the stock_compensation rule: when SBC>0, the
// dispatcher routes through ApplyC4StockCompensation, the AdjusterOutput's
// dilution Flag reaches result.Flags, the native Fired:false LedgerEntry
// reaches NativeLedgerEntries, the rule is registered as natively-emitted
// (so the shim skips it), AND there is NO balance-sheet mutation.
func TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC4FiresFlag(t *testing.T) {
	ea := NewEarningsAdjuster()
	data := &entities.FinancialData{
		Ticker:                    "TECH",
		Revenue:                   5_000_000_000,
		NormalizedOperatingIncome: 1_000_000_000,
		StockBasedCompensation:    200_000_000,
	}
	preNOI := data.NormalizedOperatingIncome
	preSBC := data.StockBasedCompensation

	rules := []*entities.CleaningRule{productionStockCompensationRule()}
	result := ea.ProcessEarningsAdjustments(context.Background(), data, rules, &entities.CleaningContext{})
	require.NotNil(t, result)

	// Legacy contract: Applied=true (matches the legacy
	// ProcessStockCompensationAdjustment return shape), Adjustments carries
	// the Reclassify record, Flags carries the dilution flag.
	assert.True(t, result.Applied,
		"C4 legacy parity: Applied:true on fire (NOT Applied:false like asset-side FlagEmitters)")
	require.Len(t, result.Adjustments, 1)
	assert.Equal(t, "StockBasedCompensation", result.Adjustments[0].FromAccount)
	assert.Equal(t, "OperatingExpenses", result.Adjustments[0].ToAccount)
	assert.Equal(t, entities.Reclassify, result.Adjustments[0].Type,
		"C4 Adjustment.Type must be Reclassify (between-line move, no mutation)")
	assert.InDelta(t, 200_000_000.0, result.Adjustments[0].Amount, 1e-6)
	assert.InDelta(t, 4.0, result.Adjustments[0].Percentage, 1e-9)
	require.Len(t, result.Flags, 1)
	assert.Equal(t, "earnings_quality", result.Flags[0].Type)

	// Native emission contract: the Fired:false LedgerEntry + native rule-ID
	// registration must reach the orchestrator so the shim skips this rule.
	require.Len(t, result.NativeLedgerEntries, 1,
		"ProcessEarningsAdjustments must surface C4's native LedgerEntry")
	assert.Empty(t, result.NativeOverlays, "FlagEmitter emits no Overlays")
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["stock_compensation"],
		"stock_compensation must appear in NativelyEmittedRuleIDs so the shim skips it")

	// FlagEmitter shape on the native entry: Fired:false even when the flag
	// fires, no monetary deltas.
	nativeEntry := result.NativeLedgerEntries[0]
	assert.False(t, nativeEntry.Fired,
		"FlagEmitter convention: native LedgerEntry stays Fired:false through the dispatcher")
	assert.Equal(t, adjusterIDC4StockCompensation, nativeEntry.AdjusterID)
	assert.Equal(t, "flag-only review; no balance-sheet adjustment", nativeEntry.SkipReason)
	assert.Zero(t, nativeEntry.Component)
	assert.Zero(t, nativeEntry.DeltaAmount)
	assert.Zero(t, nativeEntry.EquityOffset)

	// LOAD-BEARING regression: NO dual-write — balance-sheet field untouched.
	// Any future refactor that re-routes C4 as a Restater (adding a `+=` or
	// `-=` mutation to NormalizedOperatingIncome) will fail HERE.
	assert.Equal(t, preNOI, data.NormalizedOperatingIncome,
		"dispatcher must NOT mutate NormalizedOperatingIncome (FlagEmitter convention)")
	assert.Equal(t, preSBC, data.StockBasedCompensation)
}

// TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC4SkipPath pins
// the no-SBC skip-path behavior: Fired:false native entry, no Flag, no
// Adjustment, no mutation.
func TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC4SkipPath(t *testing.T) {
	ea := NewEarningsAdjuster()
	data := &entities.FinancialData{
		Ticker:                    "NOSCC",
		Revenue:                   5_000_000_000,
		NormalizedOperatingIncome: 1_000_000_000,
		StockBasedCompensation:    0,
	}
	preNOI := data.NormalizedOperatingIncome

	rules := []*entities.CleaningRule{productionStockCompensationRule()}
	result := ea.ProcessEarningsAdjustments(context.Background(), data, rules, &entities.CleaningContext{})
	require.NotNil(t, result)

	assert.False(t, result.Applied)
	assert.Empty(t, result.Adjustments)
	assert.Empty(t, result.Flags)

	require.Len(t, result.NativeLedgerEntries, 1)
	assert.False(t, result.NativeLedgerEntries[0].Fired)
	assert.True(t, result.NativelyEmittedRuleIDs["stock_compensation"],
		"stock_compensation appears in NativelyEmittedRuleIDs on the skip path too")

	// No mutation on skip path.
	assert.Equal(t, preNOI, data.NormalizedOperatingIncome)
}
