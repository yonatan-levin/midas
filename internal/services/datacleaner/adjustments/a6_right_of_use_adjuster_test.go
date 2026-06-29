package adjustments

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// productionRightOfUseRule returns a CleaningRule whose ID matches the
// production rules.json entry ("right_of_use_assets") so the rule reaches the
// right_of_use_assets branch in ProcessAssetAdjustments.
func productionRightOfUseRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:          "right_of_use_assets",
		Name:        "Right-of-Use Assets Adjustment",
		Category:    entities.AssetQuality,
		Adjustment:  entities.Exclude,
		Description: "Exclude ROU assets from invested capital",
		Severity:    entities.Info,
		Enabled:     true,
	}
}

// TestA6RightOfUseAdjuster_Adjuster_Interface_Contract pins the TDB-2 A6
// acceptance gate: a6RightOfUseAdjuster satisfies the Adjuster interface AND
// its AdjusterOutput matches the spec §3 OverlayEmitter contract for the
// fired + both skipped paths. Mirrors TestA1GoodwillAdjuster_*.
func TestA6RightOfUseAdjuster_Adjuster_Interface_Contract(t *testing.T) {
	// SR-1 A3: the adapter struct was deleted; call ApplyA6RightOfUseAssets
	// directly on the AssetAdjuster (the production dispatch path).
	adj := NewAssetAdjuster()
	require.NotNil(t, adj)

	rule := productionRightOfUseRule()
	cleaningCtx := &entities.CleaningContext{}

	t.Run("fired path (>=10%) emits overlay, Fired:true ledger entry, and an info flag", func(t *testing.T) {
		// ROU ratio = 200_000 / 1_000_000 = 20% — above 5% threshold AND the
		// 10% significance threshold, so the flag also fires.
		data := &entities.FinancialData{
			OperatingLeaseRightOfUseAsset: 200_000.0,
			OperatingLeaseLiability:       180_000.0,
			TotalAssets:                   1_000_000.0,
		}

		out, err := adj.ApplyA6RightOfUseAssets(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1, "fired path emits exactly one LedgerEntry")
		require.Len(t, out.Overlays, 1, "fired path emits exactly one OverlaySpec")
		require.Len(t, out.Flags, 1, "fired path with rouRatio>=10%% emits one info Flag")

		// LedgerEntry contract (OverlayEmitter role): Fired=true, no Component/
		// DeltaAmount/EquityOffset; B1-overlap guard metrics on the fired entry.
		entry := out.LedgerEntries[0]
		assert.True(t, entry.Fired)
		assert.Equal(t, adjusterIDA6RightOfUseExclusion, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.Empty(t, entry.Component, "A6 is an OverlayEmitter — Component must NOT be set")
		assert.Zero(t, entry.DeltaAmount, "A6 is an OverlayEmitter — DeltaAmount must be zero")
		assert.Zero(t, entry.EquityOffset, "A6 is an OverlayEmitter — EquityOffset must be zero")
		require.NotNil(t, entry.SkipMetrics, "fired A6 entry carries the B1-overlap guard metrics")
		assert.InDelta(t, 200_000.0, entry.SkipMetrics["rou_value"], 1e-9)
		assert.InDelta(t, 180_000.0, entry.SkipMetrics["operating_lease_liability"], 1e-9)

		// OverlaySpec contract: subtract semantics on the dedicated
		// InvestedCapitalExclusion field (NOT TotalAssets — avoids A1's
		// goodwill-zero side effect).
		overlay := out.Overlays[0]
		assert.Equal(t, adjusterIDA6RightOfUseExclusion, overlay.OverlayID)
		assert.Equal(t, rule.ID, overlay.RuleID)
		assert.Equal(t, "InvestedCapitalExclusion", overlay.Field)
		assert.Equal(t, "subtract", overlay.Operation)
		assert.InDelta(t, 200_000.0, overlay.Amount, 1e-9, "overlay amount must equal ROU value")
		assert.Equal(t, entities.AmountIncremental, overlay.AmountSemantics)
		assert.Contains(t, overlay.Reasoning, "right_of_use_assets",
			"overlay reasoning must carry the right_of_use_assets prefix (greppable)")
		assert.Nil(t, overlay.AIProvenance)

		// Flag contract: info severity per config.
		flag := out.Flags[0]
		assert.Equal(t, "right_of_use_exclusion", flag.Type)
		assert.Equal(t, entities.Info, flag.Severity, "config severity:info maps to entities.Info")
		assert.InDelta(t, 200_000.0, flag.Amount, 1e-9)

		// CRITICAL: Apply must NOT mutate `working`.
		assert.Equal(t, 200_000.0, data.OperatingLeaseRightOfUseAsset)
		assert.Equal(t, 1_000_000.0, data.TotalAssets)
	})

	t.Run("fired path (5%-10%) fires overlay but no flag", func(t *testing.T) {
		// ROU ratio = 70_000 / 1_000_000 = 7% — fires but below the 10% flag gate.
		data := &entities.FinancialData{
			OperatingLeaseRightOfUseAsset: 70_000.0,
			TotalAssets:                   1_000_000.0,
		}

		out, err := adj.ApplyA6RightOfUseAssets(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		require.Len(t, out.Overlays, 1)
		assert.Empty(t, out.Flags, "5%%-10%% ROU ratio fires overlay but stays below the flag threshold")
	})

	t.Run("skip path (no ROU) emits Fired:false ledger entry, no overlay", func(t *testing.T) {
		data := &entities.FinancialData{
			OperatingLeaseRightOfUseAsset: 0.0,
			TotalAssets:                   1_000_000.0,
		}

		out, err := adj.ApplyA6RightOfUseAssets(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		assert.Empty(t, out.Overlays)
		assert.Empty(t, out.Flags)

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.NotEmpty(t, entry.SkipReason)
		assert.Empty(t, entry.SkipMetrics, "no-ROU skip path does not carry threshold SkipMetrics")
	})

	t.Run("skip path (TotalAssets<=0) guards the ROU ratio divide (TDB-2 NIT a)", func(t *testing.T) {
		// ROU present but total assets non-positive: rou/0 would be +Inf and
		// spuriously clear the materiality gate. The guard must skip cleanly.
		for _, ta := range []float64{0.0, -1_000_000.0} {
			data := &entities.FinancialData{
				OperatingLeaseRightOfUseAsset: 200_000.0,
				TotalAssets:                   ta,
			}

			out, err := adj.ApplyA6RightOfUseAssets(context.Background(), data, rule, cleaningCtx)
			require.NoError(t, err)

			require.Len(t, out.LedgerEntries, 1)
			assert.Empty(t, out.Overlays, "guard must NOT emit an overlay (no +Inf-driven fire)")
			assert.Empty(t, out.Flags)

			entry := out.LedgerEntries[0]
			assert.False(t, entry.Fired, "TotalAssets<=0 must take the skip path, not fire")
			assert.Contains(t, entry.SkipReason, "Total assets")
		}
	})

	t.Run("skip path (below 5%) emits Fired:false ledger entry with SkipMetrics", func(t *testing.T) {
		// ROU ratio = 30_000 / 1_000_000 = 3% — below the 5% threshold.
		data := &entities.FinancialData{
			OperatingLeaseRightOfUseAsset: 30_000.0,
			TotalAssets:                   1_000_000.0,
		}

		out, err := adj.ApplyA6RightOfUseAssets(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		assert.Empty(t, out.Overlays)
		assert.Empty(t, out.Flags)

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Contains(t, entry.SkipReason, "threshold")
		require.NotNil(t, entry.SkipMetrics)
		assert.InDelta(t, 0.03, entry.SkipMetrics["rou_ratio"], 1e-9)
		assert.InDelta(t, 0.05, entry.SkipMetrics["threshold"], 1e-9)
	})

	t.Run("Apply is mutation-free on the fired path", func(t *testing.T) {
		data := &entities.FinancialData{
			OperatingLeaseRightOfUseAsset: 200_000.0,
			OperatingLeaseLiability:       180_000.0,
			TotalAssets:                   1_000_000.0,
		}
		before := *data
		_, err := adj.ApplyA6RightOfUseAssets(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)
		assert.True(t, reflect.DeepEqual(before, *data),
			"ApplyA6RightOfUseAssets must not mutate working")
	})
}
