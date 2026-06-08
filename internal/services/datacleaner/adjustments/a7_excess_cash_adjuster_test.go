package adjustments

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// productionExcessCashRule returns a CleaningRule matching the production
// rules.json entry ("excess_cash") with the 10%-of-revenue operating-cash
// floor, so the rule reaches the excess_cash dispatcher branch.
func productionExcessCashRule() *entities.CleaningRule {
	pct := 0.10
	return &entities.CleaningRule{
		ID:          "excess_cash",
		Name:        "Excess Cash Identification",
		Category:    entities.AssetQuality,
		Adjustment:  entities.Exclude,
		Description: "Identify non-operating excess cash",
		Threshold:   &entities.ThresholdConfig{PercentageOfRevenue: &pct},
		Severity:    entities.Info,
		Enabled:     true,
	}
}

// TestA7ExcessCashAdjuster_Adjuster_Interface_Contract pins the TDB-2 A7
// acceptance gate: a7ExcessCashAdjuster satisfies the Adjuster interface AND
// its AdjusterOutput matches the spec §4 OverlayEmitter contract.
func TestA7ExcessCashAdjuster_Adjuster_Interface_Contract(t *testing.T) {
	aa := NewAssetAdjuster()
	adj := NewA7ExcessCashAdjuster(aa)
	require.NotNil(t, adj)

	assert.Equal(t, adjusterIDA7ExcessCash, adj.Name(),
		"a7ExcessCashAdjuster.Name() must equal the AdjusterID constant")

	rule := productionExcessCashRule()
	cleaningCtx := &entities.CleaningContext{}

	t.Run("fired path with 10%% revenue floor identifies excess cash", func(t *testing.T) {
		// Cash=50B, Revenue=100B → operatingNeed = 0.10*100B = 10B → excess=40B.
		data := &entities.FinancialData{
			CashAndCashEquivalents: 50_000_000_000.0,
			Revenue:                100_000_000_000.0,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		require.Len(t, out.Overlays, 1)
		require.Len(t, out.Flags, 1, "A7 always flags on the fired path")

		entry := out.LedgerEntries[0]
		assert.True(t, entry.Fired)
		assert.Equal(t, adjusterIDA7ExcessCash, entry.AdjusterID)
		assert.Empty(t, entry.Component, "A7 is an OverlayEmitter — Component must NOT be set")
		assert.Zero(t, entry.DeltaAmount)
		require.NotNil(t, entry.SkipMetrics)
		assert.InDelta(t, 50_000_000_000.0, entry.SkipMetrics["cash"], 1.0)
		assert.InDelta(t, 10_000_000_000.0, entry.SkipMetrics["operating_cash_need"], 1.0)
		assert.InDelta(t, 40_000_000_000.0, entry.SkipMetrics["excess_cash"], 1.0)

		overlay := out.Overlays[0]
		assert.Equal(t, adjusterIDA7ExcessCash, overlay.OverlayID)
		assert.Equal(t, "ExcessCash", overlay.Field)
		assert.Equal(t, "identify", overlay.Operation)
		assert.InDelta(t, 40_000_000_000.0, overlay.Amount, 1.0, "excess = cash - 10%% of revenue")
		assert.Equal(t, entities.AmountReplacement, overlay.AmountSemantics)
		assert.Contains(t, overlay.Reasoning, "excess_cash")

		flag := out.Flags[0]
		assert.Equal(t, "excess_cash", flag.Type)
		assert.Equal(t, entities.Info, flag.Severity, "config severity:info maps to entities.Info")
		assert.InDelta(t, 40_000_000_000.0, flag.Amount, 1.0)

		// Apply must NOT mutate working.
		assert.Equal(t, 50_000_000_000.0, data.CashAndCashEquivalents)
	})

	t.Run("Revenue<=0 treats all cash as excess", func(t *testing.T) {
		data := &entities.FinancialData{
			CashAndCashEquivalents: 5_000_000.0,
			Revenue:                0.0,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.Overlays, 1)
		assert.InDelta(t, 5_000_000.0, out.Overlays[0].Amount, 1e-6,
			"Revenue<=0 → operatingNeed=0 → all cash is excess")
		assert.True(t, out.LedgerEntries[0].Fired)
	})

	t.Run("threshold nil treats all cash as excess", func(t *testing.T) {
		ruleNoThreshold := productionExcessCashRule()
		ruleNoThreshold.Threshold = nil
		data := &entities.FinancialData{
			CashAndCashEquivalents: 8_000_000.0,
			Revenue:                100_000_000.0, // revenue positive, but no pct
		}

		out, err := adj.Apply(context.Background(), data, ruleNoThreshold, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.Overlays, 1)
		assert.InDelta(t, 8_000_000.0, out.Overlays[0].Amount, 1e-6,
			"absent threshold → all cash is excess (safe default)")
	})

	t.Run("skip path (no cash) emits Fired:false ledger entry, no overlay", func(t *testing.T) {
		data := &entities.FinancialData{
			CashAndCashEquivalents: 0.0,
			Revenue:                100_000_000.0,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		assert.Empty(t, out.Overlays)
		assert.Empty(t, out.Flags)
		assert.False(t, out.LedgerEntries[0].Fired)
		assert.NotEmpty(t, out.LedgerEntries[0].SkipReason)
	})

	t.Run("skip path (cash <= operating need) emits Fired:false with SkipMetrics", func(t *testing.T) {
		// Cash=5B, Revenue=100B → operatingNeed=10B → excess=0 → skip.
		data := &entities.FinancialData{
			CashAndCashEquivalents: 5_000_000_000.0,
			Revenue:                100_000_000_000.0,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		assert.Empty(t, out.Overlays)
		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		require.NotNil(t, entry.SkipMetrics)
		assert.InDelta(t, 5_000_000_000.0, entry.SkipMetrics["cash"], 1.0)
		assert.InDelta(t, 10_000_000_000.0, entry.SkipMetrics["operating_cash_need"], 1.0)
	})

	t.Run("Apply is mutation-free on the fired path", func(t *testing.T) {
		data := &entities.FinancialData{
			CashAndCashEquivalents: 50_000_000_000.0,
			Revenue:                100_000_000_000.0,
		}
		before := *data
		_, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)
		assert.True(t, reflect.DeepEqual(before, *data),
			"ApplyA7ExcessCash must not mutate working")
	})
}
