package adjustments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// productionAssetSaleGainsRule returns a CleaningRule whose ID matches the
// production rules.json entry ("asset_sale_gains") so the rule reaches the
// asset_sale_gains branch in ProcessEarningsAdjustments.
func productionAssetSaleGainsRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:         "asset_sale_gains",
		Name:       "Non-Core Asset Sale Gains",
		Category:   entities.EarningsNormalization,
		Adjustment: entities.Exclude,
		Enabled:    true,
		Severity:   entities.Info,
	}
}

// TestC2AssetSaleGainsAdjuster_Adjuster_Interface_Contract pins the DC-1
// Phase 2 PR-3 Task 3.2 acceptance gate: c2AssetSaleGainsAdjuster satisfies
// the Adjuster interface AND its AdjusterOutput matches the spec / plan §3.5 /
// §4 row C2 Restater contract (NEGATIVE DeltaAmount — subtraction).
func TestC2AssetSaleGainsAdjuster_Adjuster_Interface_Contract(t *testing.T) {
	ea := NewEarningsAdjuster()
	adj := NewC2AssetSaleGainsAdjuster(ea)
	require.NotNil(t, adj)

	assert.Equal(t, adjusterIDC2AssetSaleGains, adj.Name(),
		"c2AssetSaleGainsAdjuster.Name() must equal the AdjusterID constant")

	rule := productionAssetSaleGainsRule()
	cleaningCtx := &entities.CleaningContext{}

	t.Run("fired path emits one Restater-shaped Fired:true LedgerEntry with NEGATIVE DeltaAmount", func(t *testing.T) {
		// Asset sale gains = $15M, revenue = $2B (legacy test case).
		data := &entities.FinancialData{
			Ticker:                    "TEST",
			Revenue:                   2_000_000_000,
			AssetSaleGains:            15_000_000,
			NormalizedOperatingIncome: 300_000_000,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1, "fired path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays, "C2 is a Restater — must NOT emit OverlaySpecs")
		assert.Empty(t, out.Flags, "C2 emits no significance Flag on the fired path (legacy parity)")

		entry := out.LedgerEntries[0]
		assert.True(t, entry.Fired)
		assert.Equal(t, adjusterIDC2AssetSaleGains, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.NotEmpty(t, entry.Reasoning)
		assert.False(t, entry.Timestamp.IsZero())
		assert.Equal(t, "NormalizedOperatingIncome", entry.Component,
			"C2 is a Restater — Component must point at the mutated income-statement line")
		// DeltaAmount is NEGATIVE for C2 because it's a subtraction (legacy
		// code: data.NormalizedOperatingIncome -= data.AssetSaleGains). This
		// is the OPPOSITE sign from C1's add-back.
		assert.InDelta(t, -15_000_000.0, entry.DeltaAmount, 1e-6,
			"DeltaAmount must equal -AssetSaleGains (NEGATIVE — subtraction, opposite of C1's add-back)")
		assert.InDelta(t, -15_000_000.0, entry.EquityOffset, 1e-6,
			"EquityOffset must mirror DeltaAmount — non-core gains subtracted from earnings flow out of retained earnings 1:1")
		assert.Zero(t, entry.TaxShieldDTA,
			"Q2 deferral: C2 TaxShieldDTA stays 0 in Phase 2")
		assert.Empty(t, entry.SkipReason)

		// CRITICAL invariant: Apply must NOT mutate `working`.
		assert.Equal(t, 300_000_000.0, data.NormalizedOperatingIncome,
			"Apply must NOT mutate data.NormalizedOperatingIncome")
		assert.Equal(t, 15_000_000.0, data.AssetSaleGains,
			"Apply must NOT mutate data.AssetSaleGains")
	})

	t.Run("skip path (no asset sale gains) emits one Fired:false LedgerEntry without SkipMetrics", func(t *testing.T) {
		data := &entities.FinancialData{
			Ticker:                    "TEST",
			Revenue:                   2_000_000_000,
			AssetSaleGains:            0,
			NormalizedOperatingIncome: 300_000_000,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		assert.Empty(t, out.Overlays)
		assert.Empty(t, out.Flags)

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, adjusterIDC2AssetSaleGains, entry.AdjusterID)
		assert.Equal(t, "No asset sale gains to adjust", entry.SkipReason,
			"no-gains skip path must use the canonical SkipReason string")
		assert.Empty(t, entry.SkipMetrics,
			"no-gains skip path does not carry SkipMetrics — C2 has no ratio threshold")
		assert.Zero(t, entry.DeltaAmount)
		assert.Zero(t, entry.EquityOffset)
		assert.Equal(t, 300_000_000.0, data.NormalizedOperatingIncome,
			"Apply must NOT mutate working on no-gains skip path")
	})

	t.Run("fired path TaxShieldDTA stays zero per Q2 deferral", func(t *testing.T) {
		data := &entities.FinancialData{
			Ticker:                    "TEST",
			Revenue:                   2_000_000_000,
			AssetSaleGains:            15_000_000,
			NormalizedOperatingIncome: 300_000_000,
			EffectiveTaxRate:          0.21,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)
		require.Len(t, out.LedgerEntries, 1)
		entry := out.LedgerEntries[0]
		require.True(t, entry.Fired)
		assert.Zero(t, entry.TaxShieldDTA,
			"Q2 deferral (plan §10): C2 must NOT compute TaxShieldDTA in Phase 2")
	})
}

// TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC2Emission pins the
// dispatcher's contract for the asset_sale_gains rule.
func TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC2Emission(t *testing.T) {
	ea := NewEarningsAdjuster()
	data := &entities.FinancialData{
		Ticker:                    "TEST",
		Revenue:                   2_000_000_000,
		AssetSaleGains:            15_000_000,
		NormalizedOperatingIncome: 300_000_000,
	}
	rules := []*entities.CleaningRule{productionAssetSaleGainsRule()}

	result := ea.ProcessEarningsAdjustments(context.Background(), data, rules, &entities.CleaningContext{})
	require.NotNil(t, result)

	// DC-1 Phase 5 P5-C4: legacy *AdjustmentResult fields deleted; the fired
	// magnitude / accounts / type are asserted via the native Restater entry
	// below + the projection metadata table (basket-parity golden).

	// Native emission contract.
	require.Len(t, result.NativeLedgerEntries, 1)
	assert.Empty(t, result.NativeOverlays, "C2 is a Restater — no OverlaySpec")
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["asset_sale_gains"])

	nativeEntry := result.NativeLedgerEntries[0]
	assert.True(t, nativeEntry.Fired)
	assert.Equal(t, adjusterIDC2AssetSaleGains, nativeEntry.AdjusterID)
	assert.Equal(t, "NormalizedOperatingIncome", nativeEntry.Component)
	assert.InDelta(t, -15_000_000.0, nativeEntry.DeltaAmount, 1e-6,
		"C2 native DeltaAmount must be NEGATIVE — subtraction, NOT add-back")
	assert.InDelta(t, -15_000_000.0, nativeEntry.EquityOffset, 1e-6)
	assert.Zero(t, nativeEntry.TaxShieldDTA)

	// Dual-write preserved — data was mutated as the legacy code did:
	// 300M - 15M subtraction = 285M.
	assert.InDelta(t, 285_000_000.0, data.NormalizedOperatingIncome, 1e-6,
		"dispatcher must subtract AssetSaleGains from data.NormalizedOperatingIncome (dual-write)")
	assert.Equal(t, 15_000_000.0, data.AssetSaleGains,
		"dispatcher must NOT mutate data.AssetSaleGains (C2 only touches NormalizedOperatingIncome)")
}

// TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC2SkipPath pins
// no-mutation on the skip path.
func TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC2SkipPath(t *testing.T) {
	ea := NewEarningsAdjuster()
	data := &entities.FinancialData{
		Ticker:                    "TEST",
		Revenue:                   2_000_000_000,
		AssetSaleGains:            0,
		NormalizedOperatingIncome: 300_000_000,
	}
	rules := []*entities.CleaningRule{productionAssetSaleGainsRule()}

	result := ea.ProcessEarningsAdjustments(context.Background(), data, rules, &entities.CleaningContext{})
	require.NotNil(t, result)

	// DC-1 Phase 5 P5-C4: skip contract asserted natively — no fired entry.
	require.Len(t, result.NativeLedgerEntries, 1)
	assert.False(t, result.NativeLedgerEntries[0].Fired)
	assert.True(t, result.NativelyEmittedRuleIDs["asset_sale_gains"])

	// No mutation on skip path.
	assert.Equal(t, 300_000_000.0, data.NormalizedOperatingIncome)
}
