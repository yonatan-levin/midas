package adjustments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestDispatcherDualWriteDeleted_Assets pins the DC-1 Phase 4 §8.2.1 Option A
// contract change for the asset-side Restaters (A2 intangible, A4 DTA, A5
// inventory): after Phase 4 the dispatcher applies each fired LedgerEntry's
// COMPONENT delta to working but does NOT mutate the TotalAssets umbrella (nor
// the A4 ValuationAllowance auxiliary aggregate). Umbrella coherence is
// restored downstream by cleaneddata.Restated().
//
// This is the companion contract pin to the per-rule emission tests; it asserts
// the invariant generically across all three asset Restaters in one place so a
// future reintroduction of an umbrella dual-write fails loudly here.
func TestDispatcherDualWriteDeleted_Assets(t *testing.T) {
	t.Run("A2 intangible writedown leaves TotalAssets umbrella untouched", func(t *testing.T) {
		aa := NewAssetAdjuster()
		const originalTotalAssets = 1_000_000.0
		data := &entities.FinancialData{
			OtherIntangibles: 300_000.0, // 30% of assets — fires A2
			TotalAssets:      originalTotalAssets,
		}
		rules := []*entities.CleaningRule{productionIntangibleRule()}

		result := aa.ProcessAssetAdjustments(context.Background(), data, rules, &entities.CleaningContext{})
		require.NotNil(t, result)
		require.True(t, result.Applied)

		// Component delta still applied (helper, not the deleted dual-write).
		assert.InDelta(t, 100_000.0, data.OtherIntangibles, 1e-6,
			"A2 component delta must still land on data.OtherIntangibles")
		// Umbrella NOT mutated.
		assert.InDelta(t, originalTotalAssets, data.TotalAssets, 1e-6,
			"A2 dispatcher must NOT mutate data.TotalAssets (Phase 4 §8.2.1 Option A)")
	})

	t.Run("A4 DTA valuation allowance leaves TotalAssets + ValuationAllowance untouched", func(t *testing.T) {
		aa := NewAssetAdjuster()
		const originalTotalAssets = 1_000_000.0
		data := &entities.FinancialData{
			DeferredTaxAssets: 200_000.0, // 20% of assets — fires A4
			TotalAssets:       originalTotalAssets,
		}
		rules := []*entities.CleaningRule{productionDeferredTaxRule()}

		result := aa.ProcessAssetAdjustments(context.Background(), data, rules, &entities.CleaningContext{})
		require.NotNil(t, result)
		require.True(t, result.Applied)

		assert.InDelta(t, 100_000.0, data.DeferredTaxAssets, 1e-6,
			"A4 component delta must still land on data.DeferredTaxAssets")
		assert.InDelta(t, originalTotalAssets, data.TotalAssets, 1e-6,
			"A4 dispatcher must NOT mutate data.TotalAssets (Phase 4 §8.2.1 Option A)")
		assert.InDelta(t, 0.0, data.ValuationAllowance, 1e-6,
			"A4 dispatcher must NOT dual-write data.ValuationAllowance (Phase 4 §8.2.1 Option A)")
	})

	t.Run("A5 inventory writedown leaves TotalAssets umbrella untouched", func(t *testing.T) {
		aa := NewAssetAdjuster()
		const originalTotalAssets = 1_000_000.0
		data := &entities.FinancialData{
			Inventory:         400_000.0,
			TotalAssets:       originalTotalAssets,
			InventoryTurnover: 2.0,
			EffectiveTaxRate:  0.21,
		}
		rules := []*entities.CleaningRule{productionObsoleteInventoryRule()}

		result := aa.ProcessAssetAdjustments(context.Background(), data, rules, createRetailContext())
		require.NotNil(t, result)
		require.True(t, result.Applied)

		assert.InDelta(t, 240_000.0, data.Inventory, 1e-6,
			"A5 component delta must still land on data.Inventory")
		assert.InDelta(t, originalTotalAssets, data.TotalAssets, 1e-6,
			"A5 dispatcher must NOT mutate data.TotalAssets (Phase 4 §8.2.1 Option A)")
	})
}
