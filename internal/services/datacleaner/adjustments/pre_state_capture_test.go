package adjustments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestPreStateCapture_OnFiredLedgerEntries pins the DC-1 P5-followup §4.2
// contract: every Restater whose legacy translator computed a Percentage
// from a pre-mutation snapshot now writes that snapshot onto the fired
// LedgerEntry's SkipMetrics map under the canonical "original_<Field>"
// key. The LedgerEntry → Adjustment projection (P5-C3-full A4) reads
// these keys instead of dispatcher-side threaded `original*` arguments.
//
// Convention surface covered:
//   - A2 intangibles → SkipMetrics["original_OtherIntangibles"]
//   - C2/C3/C5/C6   → SkipMetrics["original_Revenue"] (single shared key)
//   - C4 SBC       → SkipMetrics["original_Revenue"] (extends existing
//     sbc_amount/sbc_ratio map; C4 keeps the SBC scalars
//     for the projection's amount-source resolution)
//
// Skip paths are NOT exercised here (covered by the existing per-rule
// _SkipPath tests) — the load-bearing pin is the fired-path capture,
// where a regression silently degrades Adjustment.Percentage to 0.0
// for the affected rule and the basket-parity golden flags it.
//
// Spec: docs/refactoring/archive/dc1-phase-5-followup-percentage-decision.md §6.2 / §7.3 item 4
func TestPreStateCapture_OnFiredLedgerEntries(t *testing.T) {
	ctx := context.Background()
	cleaningCtx := &entities.CleaningContext{}

	t.Run("A2_intangible_writedown_captures_original_OtherIntangibles", func(t *testing.T) {
		aa := NewAssetAdjuster()
		// Tune intangibles to land above the 2% materiality threshold so A2 fires.
		data := &entities.FinancialData{
			OtherIntangibles: 400_000.0,
			TotalAssets:      1_000_000.0,
		}
		rule := productionIntangibleRule()

		out, err := aa.ApplyA2Intangible(ctx, data, rule, cleaningCtx)
		require.NoError(t, err)
		require.Len(t, out.LedgerEntries, 1)
		entry := out.LedgerEntries[0]
		require.True(t, entry.Fired, "fixture must fire A2")
		require.NotNil(t, entry.SkipMetrics, "SkipMetrics must be populated on fired Restater")
		require.Contains(t, entry.SkipMetrics, "original_OtherIntangibles",
			"A2 must carry pre-state OtherIntangibles for the projection")
		assert.InDelta(t, 400_000.0, entry.SkipMetrics["original_OtherIntangibles"], 1e-6,
			"original_OtherIntangibles must equal working.OtherIntangibles at Apply entry")
	})

	t.Run("C2_asset_sale_gains_captures_original_Revenue", func(t *testing.T) {
		ea := NewEarningsAdjuster()
		data := &entities.FinancialData{
			Revenue:        2_000_000_000.0,
			AssetSaleGains: 30_000_000.0,
		}
		rule := &entities.CleaningRule{ID: "asset_sale_gains", Category: entities.EarningsNormalization, Enabled: true}

		out, err := ea.ApplyC2AssetSaleGains(ctx, data, rule, cleaningCtx)
		require.NoError(t, err)
		require.Len(t, out.LedgerEntries, 1)
		entry := out.LedgerEntries[0]
		require.True(t, entry.Fired)
		require.NotNil(t, entry.SkipMetrics)
		require.Contains(t, entry.SkipMetrics, "original_Revenue")
		assert.InDelta(t, 2_000_000_000.0, entry.SkipMetrics["original_Revenue"], 1e-6)
	})

	t.Run("C3_litigation_settlements_captures_original_Revenue", func(t *testing.T) {
		ea := NewEarningsAdjuster()
		// Settlements / revenue = 2% → at the default threshold. Use 3% so
		// the materiality guard passes deterministically.
		data := &entities.FinancialData{
			Revenue:               2_000_000_000.0,
			LitigationSettlements: 60_000_000.0,
		}
		rule := &entities.CleaningRule{ID: "litigation_settlements", Category: entities.EarningsNormalization, Enabled: true}

		out, err := ea.ApplyC3Litigation(ctx, data, rule, cleaningCtx)
		require.NoError(t, err)
		require.Len(t, out.LedgerEntries, 1)
		entry := out.LedgerEntries[0]
		require.True(t, entry.Fired)
		require.NotNil(t, entry.SkipMetrics)
		require.Contains(t, entry.SkipMetrics, "original_Revenue")
		assert.InDelta(t, 2_000_000_000.0, entry.SkipMetrics["original_Revenue"], 1e-6)
	})

	t.Run("C4_stock_compensation_carries_original_Revenue_alongside_sbc_keys", func(t *testing.T) {
		ea := NewEarningsAdjuster()
		data := &entities.FinancialData{
			Revenue:                2_000_000_000.0,
			StockBasedCompensation: 80_000_000.0,
		}
		rule := &entities.CleaningRule{ID: "stock_compensation", Category: entities.EarningsNormalization, Enabled: true, Severity: "warning"}

		out, err := ea.ApplyC4StockCompensation(ctx, data, rule, cleaningCtx)
		require.NoError(t, err)
		require.Len(t, out.LedgerEntries, 1)
		entry := out.LedgerEntries[0]
		// C4 is a FlagEmitter: Fired stays false; the populated Flags slice
		// is the firing signal. The SkipMetrics map carries BOTH the legacy
		// sbc_amount/sbc_ratio diagnostics AND the new pre-state key.
		require.False(t, entry.Fired, "C4 FlagEmitter contract: Fired stays false on the fire path")
		require.NotEmpty(t, out.Flags, "C4 firing signal is a populated Flags slice")
		require.NotNil(t, entry.SkipMetrics)
		require.Contains(t, entry.SkipMetrics, "sbc_amount", "legacy sbc_amount diagnostic must survive")
		require.Contains(t, entry.SkipMetrics, "sbc_ratio", "legacy sbc_ratio diagnostic must survive")
		require.Contains(t, entry.SkipMetrics, "original_Revenue", "C4 must extend SkipMetrics with original_Revenue")
		assert.InDelta(t, 80_000_000.0, entry.SkipMetrics["sbc_amount"], 1e-6)
		assert.InDelta(t, 2_000_000_000.0, entry.SkipMetrics["original_Revenue"], 1e-6)
	})

	t.Run("C5_derivative_gains_losses_captures_original_Revenue", func(t *testing.T) {
		ea := NewEarningsAdjuster()
		data := &entities.FinancialData{
			Revenue:               2_000_000_000.0,
			DerivativeGainsLosses: 25_000_000.0, // gain branch
		}
		rule := &entities.CleaningRule{ID: "derivative_gains_losses", Category: entities.EarningsNormalization, Enabled: true}

		out, err := ea.ApplyC5DerivativeGainsLosses(ctx, data, rule, cleaningCtx)
		require.NoError(t, err)
		require.Len(t, out.LedgerEntries, 1)
		entry := out.LedgerEntries[0]
		require.True(t, entry.Fired)
		require.NotNil(t, entry.SkipMetrics)
		require.Contains(t, entry.SkipMetrics, "original_Revenue")
		assert.InDelta(t, 2_000_000_000.0, entry.SkipMetrics["original_Revenue"], 1e-6)
	})

	t.Run("C6_capitalized_interest_captures_original_Revenue", func(t *testing.T) {
		ea := NewEarningsAdjuster()
		data := &entities.FinancialData{
			Revenue:             2_000_000_000.0,
			CapitalizedInterest: 16_000_000.0,
		}
		rule := &entities.CleaningRule{ID: "capitalized_interest", Category: entities.EarningsNormalization, Enabled: true}

		out, err := ea.ApplyC6CapitalizedInterest(ctx, data, rule, cleaningCtx)
		require.NoError(t, err)
		require.Len(t, out.LedgerEntries, 1)
		entry := out.LedgerEntries[0]
		require.True(t, entry.Fired)
		require.NotNil(t, entry.SkipMetrics)
		require.Contains(t, entry.SkipMetrics, "original_Revenue")
		assert.InDelta(t, 2_000_000_000.0, entry.SkipMetrics["original_Revenue"], 1e-6)
	})
}
