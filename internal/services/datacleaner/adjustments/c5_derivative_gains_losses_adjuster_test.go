package adjustments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// productionDerivativeGainsLossesRule returns a CleaningRule whose ID matches
// the production rules.json entry ("derivative_gains_losses"). C5 has no
// production threshold gate — legacy code fires whenever DerivativeGainsLosses
// is non-zero.
func productionDerivativeGainsLossesRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:         "derivative_gains_losses",
		Name:       "Volatile Derivative Marks",
		Category:   entities.EarningsNormalization,
		Adjustment: entities.Exclude,
		Enabled:    true,
		Severity:   entities.Info,
	}
}

// TestC5DerivativeGainsLossesAdjuster_Adjuster_Interface_Contract pins the
// DC-1 Phase 2 PR-3 Task 3.5 acceptance gate.
//
// LOAD-BEARING subtests:
//   - GAIN-PATH fire: positive original gain → NEGATIVE DeltaAmount (subtract).
//   - LOSS-PATH fire: negative original loss → POSITIVE DeltaAmount (add-back).
//   - ONE LedgerEntry per fire (not two — the legacy two-line mutation sites
//     are branches, not duplicates).
//   - Mutation-free Apply on all paths.
func TestC5DerivativeGainsLossesAdjuster_Adjuster_Interface_Contract(t *testing.T) {
	ea := NewEarningsAdjuster()
	adj := NewC5DerivativeGainsLossesAdjuster(ea)
	require.NotNil(t, adj)
	assert.Equal(t, adjusterIDC5DerivativeGainsLosses, adj.Name())

	rule := productionDerivativeGainsLossesRule()
	cleaningCtx := &entities.CleaningContext{}

	t.Run("GAIN-PATH fire — positive original gain → NEGATIVE DeltaAmount", func(t *testing.T) {
		// Positive derivative gain of $20M; legacy code subtracts from operating income.
		data := &entities.FinancialData{
			Ticker:                    "TEST",
			Revenue:                   1_000_000_000,
			DerivativeGainsLosses:     20_000_000, // POSITIVE gain
			NormalizedOperatingIncome: 150_000_000,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		// LOAD-BEARING: ONE LedgerEntry per fire — legacy has two mutation
		// SITES (gain path / loss path) but only ONE FIRES per call.
		require.Len(t, out.LedgerEntries, 1, "C5 must emit EXACTLY ONE LedgerEntry per fire; legacy's two mutation lines live in two branches")
		assert.Empty(t, out.Overlays, "C5 is a Restater — must NOT emit OverlaySpecs")
		assert.Empty(t, out.Flags, "C5 emits no significance Flag")

		entry := out.LedgerEntries[0]
		assert.True(t, entry.Fired)
		assert.Equal(t, adjusterIDC5DerivativeGainsLosses, entry.AdjusterID)
		assert.Equal(t, "NormalizedOperatingIncome", entry.Component)
		// NEGATIVE DeltaAmount — gain path subtracts.
		assert.InDelta(t, -20_000_000.0, entry.DeltaAmount, 1e-6,
			"GAIN-PATH DeltaAmount must equal -DerivativeGainsLosses (NEGATIVE — subtract)")
		assert.InDelta(t, -20_000_000.0, entry.EquityOffset, 1e-6,
			"EquityOffset must mirror DeltaAmount 1:1")
		assert.Zero(t, entry.TaxShieldDTA, "Q2 deferral: C5 TaxShieldDTA stays 0")

		// Mutation-free.
		assert.Equal(t, 150_000_000.0, data.NormalizedOperatingIncome,
			"Apply must NOT mutate data.NormalizedOperatingIncome")
		assert.Equal(t, 20_000_000.0, data.DerivativeGainsLosses)
	})

	t.Run("LOSS-PATH fire — negative original loss → POSITIVE DeltaAmount", func(t *testing.T) {
		// Negative derivative loss of -$15M; legacy code "subtracts the
		// negative" (effectively adding back) AND flips reporting sign.
		data := &entities.FinancialData{
			Ticker:                    "TEST",
			Revenue:                   1_000_000_000,
			DerivativeGainsLosses:     -15_000_000, // NEGATIVE loss
			NormalizedOperatingIncome: 150_000_000,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		// LOAD-BEARING: ONE LedgerEntry per fire.
		require.Len(t, out.LedgerEntries, 1, "LOSS-PATH must emit EXACTLY ONE LedgerEntry, not two")

		entry := out.LedgerEntries[0]
		assert.True(t, entry.Fired)
		assert.Equal(t, "NormalizedOperatingIncome", entry.Component)
		// POSITIVE DeltaAmount — loss path: legacy subtracts a negative = adds back.
		assert.InDelta(t, 15_000_000.0, entry.DeltaAmount, 1e-6,
			"LOSS-PATH DeltaAmount must equal -DerivativeGainsLosses = +15M (POSITIVE — add-back equivalent of subtracting a negative)")
		assert.InDelta(t, 15_000_000.0, entry.EquityOffset, 1e-6)
		assert.Zero(t, entry.TaxShieldDTA)

		// Mutation-free.
		assert.Equal(t, 150_000_000.0, data.NormalizedOperatingIncome)
		assert.Equal(t, -15_000_000.0, data.DerivativeGainsLosses)
	})

	t.Run("skip path — no derivative activity emits one Fired:false LedgerEntry", func(t *testing.T) {
		data := &entities.FinancialData{
			Ticker:                    "TEST",
			Revenue:                   1_000_000_000,
			DerivativeGainsLosses:     0,
			NormalizedOperatingIncome: 150_000_000,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, "No derivative gains/losses to adjust", entry.SkipReason)
		assert.Empty(t, entry.SkipMetrics, "C5 skip path has no metric to surface — numerator is zero")
		// Mutation-free.
		assert.Equal(t, 150_000_000.0, data.NormalizedOperatingIncome)
	})

	t.Run("fired path TaxShieldDTA stays zero per Q2 deferral", func(t *testing.T) {
		data := &entities.FinancialData{
			Ticker:                    "TEST",
			Revenue:                   1_000_000_000,
			DerivativeGainsLosses:     20_000_000,
			NormalizedOperatingIncome: 150_000_000,
			EffectiveTaxRate:          0.21,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)
		require.Len(t, out.LedgerEntries, 1)
		entry := out.LedgerEntries[0]
		require.True(t, entry.Fired)
		assert.Zero(t, entry.TaxShieldDTA)
	})
}

// TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC5Emission pins the
// dispatcher's contract for the derivative_gains_losses rule — verifying the
// dual-write happens AFTER Apply and the native LedgerEntries / RuleID
// surface for the cleaner orchestrator drain.
func TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC5Emission(t *testing.T) {
	t.Run("GAIN-PATH dispatcher subtracts via signed DeltaAmount", func(t *testing.T) {
		ea := NewEarningsAdjuster()
		data := &entities.FinancialData{
			Ticker:                    "TEST",
			Revenue:                   1_000_000_000,
			DerivativeGainsLosses:     20_000_000, // gain
			NormalizedOperatingIncome: 150_000_000,
		}
		rules := []*entities.CleaningRule{productionDerivativeGainsLossesRule()}

		result := ea.ProcessEarningsAdjustments(context.Background(), data, rules, &entities.CleaningContext{})
		require.NotNil(t, result)

		assert.True(t, result.Applied)
		require.Len(t, result.Adjustments, 1)
		// Legacy *AdjustmentResult.Amount uses POSITIVE magnitude (reporting).
		assert.InDelta(t, 20_000_000.0, result.Adjustments[0].Amount, 1e-6)
		assert.Equal(t, "DerivativeGainsLosses", result.Adjustments[0].FromAccount)
		assert.Equal(t, "NormalizedOperatingIncome", result.Adjustments[0].ToAccount)

		require.Len(t, result.NativeLedgerEntries, 1)
		assert.Empty(t, result.NativeOverlays)
		require.NotNil(t, result.NativelyEmittedRuleIDs)
		assert.True(t, result.NativelyEmittedRuleIDs["derivative_gains_losses"])

		nativeEntry := result.NativeLedgerEntries[0]
		assert.True(t, nativeEntry.Fired)
		assert.Equal(t, adjusterIDC5DerivativeGainsLosses, nativeEntry.AdjusterID)
		assert.Equal(t, "NormalizedOperatingIncome", nativeEntry.Component)
		// NEGATIVE — gain path.
		assert.InDelta(t, -20_000_000.0, nativeEntry.DeltaAmount, 1e-6,
			"GAIN-PATH native DeltaAmount must be NEGATIVE")

		// Dual-write: legacy "data.NormalizedOperatingIncome -= 20M" → 130M.
		assert.InDelta(t, 130_000_000.0, data.NormalizedOperatingIncome, 1e-6)
		assert.Equal(t, 20_000_000.0, data.DerivativeGainsLosses,
			"dispatcher must NOT mutate data.DerivativeGainsLosses")
	})

	t.Run("LOSS-PATH dispatcher adds back via signed DeltaAmount", func(t *testing.T) {
		ea := NewEarningsAdjuster()
		data := &entities.FinancialData{
			Ticker:                    "TEST",
			Revenue:                   1_000_000_000,
			DerivativeGainsLosses:     -15_000_000, // loss
			NormalizedOperatingIncome: 150_000_000,
		}
		rules := []*entities.CleaningRule{productionDerivativeGainsLossesRule()}

		result := ea.ProcessEarningsAdjustments(context.Background(), data, rules, &entities.CleaningContext{})
		require.NotNil(t, result)
		assert.True(t, result.Applied)

		require.Len(t, result.NativeLedgerEntries, 1)
		nativeEntry := result.NativeLedgerEntries[0]
		assert.True(t, nativeEntry.Fired)
		// POSITIVE — loss path.
		assert.InDelta(t, 15_000_000.0, nativeEntry.DeltaAmount, 1e-6,
			"LOSS-PATH native DeltaAmount must be POSITIVE")

		// Dual-write: legacy subtracts the negative → adds back → 165M.
		assert.InDelta(t, 165_000_000.0, data.NormalizedOperatingIncome, 1e-6)
	})
}

// TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC5SkipPath pins
// no-mutation on the no-derivative-activity skip path.
func TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC5SkipPath(t *testing.T) {
	ea := NewEarningsAdjuster()
	data := &entities.FinancialData{
		Ticker:                    "TEST",
		Revenue:                   1_000_000_000,
		DerivativeGainsLosses:     0,
		NormalizedOperatingIncome: 150_000_000,
	}
	rules := []*entities.CleaningRule{productionDerivativeGainsLossesRule()}

	result := ea.ProcessEarningsAdjustments(context.Background(), data, rules, &entities.CleaningContext{})
	require.NotNil(t, result)

	assert.False(t, result.Applied)
	assert.Empty(t, result.Adjustments)

	require.Len(t, result.NativeLedgerEntries, 1)
	assert.False(t, result.NativeLedgerEntries[0].Fired)
	assert.True(t, result.NativelyEmittedRuleIDs["derivative_gains_losses"])

	// No mutation on skip path.
	assert.Equal(t, 150_000_000.0, data.NormalizedOperatingIncome)
}
