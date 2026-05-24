package adjustments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// productionLitigationRule returns a CleaningRule whose ID matches the
// production rules.json entry ("litigation_settlements").
func productionLitigationRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:         "litigation_settlements",
		Name:       "Episodic Litigation Settlements",
		Category:   entities.EarningsNormalization,
		Adjustment: entities.Exclude,
		Enabled:    true,
		Severity:   entities.Info,
		Threshold: &entities.ThresholdConfig{
			PercentageOfRevenue: floatPtr(0.01), // 1% threshold (legacy default)
		},
	}
}

// TestC3LitigationSettlementsAdjuster_Adjuster_Interface_Contract pins the
// DC-1 Phase 2 PR-3 Task 3.3 acceptance gate.
func TestC3LitigationSettlementsAdjuster_Adjuster_Interface_Contract(t *testing.T) {
	ea := NewEarningsAdjuster()
	adj := NewC3LitigationSettlementsAdjuster(ea)
	require.NotNil(t, adj)
	assert.Equal(t, adjusterIDC3LitigationSettlements, adj.Name())

	rule := productionLitigationRule()
	cleaningCtx := &entities.CleaningContext{}

	t.Run("fired path emits one Restater-shaped Fired:true LedgerEntry with POSITIVE DeltaAmount", func(t *testing.T) {
		// Litigation = $30M, revenue = $1B → 3% ratio (above 1% threshold).
		data := &entities.FinancialData{
			Ticker:                    "TEST",
			Revenue:                   1_000_000_000,
			LitigationSettlements:     30_000_000,
			NormalizedOperatingIncome: 150_000_000,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		assert.Empty(t, out.Overlays, "C3 is a Restater — must NOT emit OverlaySpecs")
		assert.Empty(t, out.Flags, "C3 emits no significance Flag on the fired path")

		entry := out.LedgerEntries[0]
		assert.True(t, entry.Fired)
		assert.Equal(t, adjusterIDC3LitigationSettlements, entry.AdjusterID)
		assert.Equal(t, "NormalizedOperatingIncome", entry.Component)
		// POSITIVE DeltaAmount — C3 is an add-back, same sign as C1.
		assert.InDelta(t, 30_000_000.0, entry.DeltaAmount, 1e-6,
			"DeltaAmount must equal +LitigationSettlements (POSITIVE — add-back, same sign as C1)")
		assert.InDelta(t, 30_000_000.0, entry.EquityOffset, 1e-6,
			"EquityOffset must mirror DeltaAmount 1:1")
		assert.Zero(t, entry.TaxShieldDTA, "Q2 deferral: C3 TaxShieldDTA stays 0")

		// Mutation-free.
		assert.Equal(t, 150_000_000.0, data.NormalizedOperatingIncome,
			"Apply must NOT mutate data.NormalizedOperatingIncome")
		assert.Equal(t, 30_000_000.0, data.LitigationSettlements)
	})

	t.Run("skip path 1 (no litigation) emits one Fired:false LedgerEntry without SkipMetrics", func(t *testing.T) {
		data := &entities.FinancialData{
			Ticker:                    "TEST",
			Revenue:                   1_000_000_000,
			LitigationSettlements:     0,
			NormalizedOperatingIncome: 150_000_000,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, "No litigation settlements to adjust", entry.SkipReason)
		assert.Empty(t, entry.SkipMetrics)
		assert.Equal(t, 150_000_000.0, data.NormalizedOperatingIncome)
	})

	t.Run("skip path 2 (below threshold) emits one Fired:false LedgerEntry with SkipMetrics", func(t *testing.T) {
		// Litigation = $5M, revenue = $1B → 0.5% (below 1% threshold).
		data := &entities.FinancialData{
			Ticker:                    "TEST",
			Revenue:                   1_000_000_000,
			LitigationSettlements:     5_000_000,
			NormalizedOperatingIncome: 150_000_000,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Contains(t, entry.SkipReason, "below materiality threshold")
		require.NotNil(t, entry.SkipMetrics)
		assert.InDelta(t, 0.005, entry.SkipMetrics["litigation_ratio"], 1e-9)
		assert.InDelta(t, 0.01, entry.SkipMetrics["threshold"], 1e-9)
		assert.Equal(t, 150_000_000.0, data.NormalizedOperatingIncome)
	})

	t.Run("fired path TaxShieldDTA stays zero per Q2 deferral", func(t *testing.T) {
		data := &entities.FinancialData{
			Ticker:                    "TEST",
			Revenue:                   1_000_000_000,
			LitigationSettlements:     30_000_000,
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

// TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC3Emission pins the
// dispatcher's contract for the litigation_settlements rule.
func TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC3Emission(t *testing.T) {
	ea := NewEarningsAdjuster()
	data := &entities.FinancialData{
		Ticker:                    "TEST",
		Revenue:                   1_000_000_000,
		LitigationSettlements:     30_000_000, // 3% of revenue (above 1% threshold)
		NormalizedOperatingIncome: 150_000_000,
	}
	rules := []*entities.CleaningRule{productionLitigationRule()}

	result := ea.ProcessEarningsAdjustments(data, rules, &entities.CleaningContext{})
	require.NotNil(t, result)

	assert.True(t, result.Applied)
	require.Len(t, result.Adjustments, 1)
	assert.InDelta(t, 30_000_000.0, result.Adjustments[0].Amount, 1e-6)
	assert.Equal(t, "LitigationSettlements", result.Adjustments[0].FromAccount)
	assert.Equal(t, "NormalizedOperatingIncome", result.Adjustments[0].ToAccount)

	require.Len(t, result.NativeLedgerEntries, 1)
	assert.Empty(t, result.NativeOverlays)
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["litigation_settlements"])

	nativeEntry := result.NativeLedgerEntries[0]
	assert.True(t, nativeEntry.Fired)
	assert.Equal(t, adjusterIDC3LitigationSettlements, nativeEntry.AdjusterID)
	assert.Equal(t, "NormalizedOperatingIncome", nativeEntry.Component)
	assert.InDelta(t, 30_000_000.0, nativeEntry.DeltaAmount, 1e-6,
		"C3 native DeltaAmount must be POSITIVE — add-back")
	assert.InDelta(t, 30_000_000.0, nativeEntry.EquityOffset, 1e-6)

	// Dual-write: 150M + 30M add-back = 180M.
	assert.InDelta(t, 180_000_000.0, data.NormalizedOperatingIncome, 1e-6)
	assert.Equal(t, 30_000_000.0, data.LitigationSettlements,
		"dispatcher must NOT mutate data.LitigationSettlements")
}

// TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC3SkipPath pins
// no-mutation on the below-threshold skip path.
func TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC3SkipPath(t *testing.T) {
	ea := NewEarningsAdjuster()
	data := &entities.FinancialData{
		Ticker:                    "TEST",
		Revenue:                   1_000_000_000,
		LitigationSettlements:     5_000_000, // 0.5% — below 1% threshold
		NormalizedOperatingIncome: 150_000_000,
	}
	rules := []*entities.CleaningRule{productionLitigationRule()}

	result := ea.ProcessEarningsAdjustments(data, rules, &entities.CleaningContext{})
	require.NotNil(t, result)

	assert.False(t, result.Applied)
	assert.Empty(t, result.Adjustments)

	require.Len(t, result.NativeLedgerEntries, 1)
	assert.False(t, result.NativeLedgerEntries[0].Fired)
	assert.True(t, result.NativelyEmittedRuleIDs["litigation_settlements"])

	skipEntry := result.NativeLedgerEntries[0]
	assert.Contains(t, skipEntry.SkipReason, "below materiality threshold")
	require.NotNil(t, skipEntry.SkipMetrics)
	assert.InDelta(t, 0.005, skipEntry.SkipMetrics["litigation_ratio"], 1e-9)

	// No mutation on skip path.
	assert.Equal(t, 150_000_000.0, data.NormalizedOperatingIncome)
}
