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

// TestDispatcherDualWriteDeleted_Earnings pins the DC-1 Phase 4 §8.2.1 Option A
// contract for the earnings-side Restaters (C1 restructuring add-back, C6
// capitalized-interest reclassification). The generic apply-component-delta
// helper still mutates the COMPONENT field (NormalizedOperatingIncome /
// InterestExpense) exactly as the deleted per-rule dual-writes did — C-rules
// never touched an umbrella, so there is nothing to leave untouched; the test
// pins that the component mutation magnitude is unchanged under the new
// mechanism.
func TestDispatcherDualWriteDeleted_Earnings(t *testing.T) {
	t.Run("C1 restructuring add-back still lands on NormalizedOperatingIncome", func(t *testing.T) {
		ea := NewEarningsAdjuster()
		data := &entities.FinancialData{
			Ticker:                    "TEST",
			Revenue:                   1_000_000_000,
			RestructuringCharges:      30_000_000, // 3% of revenue — fires C1
			NormalizedOperatingIncome: 150_000_000,
		}
		rules := []*entities.CleaningRule{productionRestructuringRule()}

		result := ea.ProcessEarningsAdjustments(context.Background(), data, rules, &entities.CleaningContext{})
		require.NotNil(t, result)
		require.True(t, result.Applied)

		// 150M + 30M add-back = 180M, applied by the generic helper.
		assert.InDelta(t, 180_000_000.0, data.NormalizedOperatingIncome, 1e-6,
			"C1 component add-back must still land on data.NormalizedOperatingIncome via the helper")
		assert.Equal(t, 30_000_000.0, data.RestructuringCharges,
			"C1 must not mutate its source field")
	})

	t.Run("C6 capitalized interest still lands on InterestExpense with EquityOffset 0", func(t *testing.T) {
		ea := NewEarningsAdjuster()
		data := &entities.FinancialData{
			Ticker:              "TEST",
			Revenue:             1_000_000_000,
			CapitalizedInterest: 20_000_000,
			InterestExpense:     50_000_000,
		}
		rules := []*entities.CleaningRule{productionCapitalizedInterestRule()}

		result := ea.ProcessEarningsAdjustments(context.Background(), data, rules, &entities.CleaningContext{})
		require.NotNil(t, result)
		require.True(t, result.Applied)

		// 50M + 20M = 70M, applied by the helper to InterestExpense.
		assert.InDelta(t, 70_000_000.0, data.InterestExpense, 1e-6,
			"C6 component delta must still land on data.InterestExpense via the helper")

		// LOAD-BEARING: C6 native LedgerEntry carries EquityOffset 0 — the
		// helper touches the component only and the Restated() reducer never
		// flows C6 into equity.
		require.Len(t, result.NativeLedgerEntries, 1)
		assert.Zero(t, result.NativeLedgerEntries[0].EquityOffset,
			"C6 EquityOffset must stay 0 (income-statement reclassification, not an equity event)")
	})
}

// TestDispatcherDualWriteDeleted_Liabilities pins the DC-1 Phase 4 §8.2.1
// Option A + B3 routing-flip contract for the liability-side OverlayEmitters
// (B1 lease, B2 pension): the dispatcher no longer mutates data.TotalDebt /
// data.InterestBearingDebt. Each B-rule's amount flows ONLY through its
// OverlaySpec (drained into NativeOverlays) for InvestedCapital().DebtLikeClaims
// to consume. (B3's dedicated routing-flip pin lives in
// b3_contingent_liabilities_adjuster_test.go.)
func TestDispatcherDualWriteDeleted_Liabilities(t *testing.T) {
	t.Run("B1 operating leases leave TotalDebt + InterestBearingDebt untouched", func(t *testing.T) {
		la := NewLiabilityAdjuster(&mockAIService{}, nil)
		const origTotalDebt = 150_000.0
		const origIBD = 150_000.0
		data := &entities.FinancialData{
			Ticker:                  "RETAIL",
			OperatingLeaseLiability: 200_000.0,
			TotalAssets:             1_000_000.0,
			Revenue:                 500_000.0,
			TotalDebt:               origTotalDebt,
			InterestBearingDebt:     origIBD,
		}
		rules := []*entities.CleaningRule{productionOperatingLeasesRule()}

		result := la.ProcessLiabilityAdjustments(context.Background(), data, rules, &entities.CleaningContext{IndustryCode: "44"})
		require.NotNil(t, result)
		require.True(t, result.Applied)
		require.Len(t, result.NativeOverlays, 1)
		require.Greater(t, result.NativeOverlays[0].Amount, 0.0)

		assert.Equal(t, origTotalDebt, data.TotalDebt,
			"Phase 4: B1 must NOT mutate data.TotalDebt (effect → InvestedCapital().DebtLikeClaims)")
		assert.Equal(t, origIBD, data.InterestBearingDebt,
			"Phase 4: B1 must NOT mutate data.InterestBearingDebt")
	})
}
