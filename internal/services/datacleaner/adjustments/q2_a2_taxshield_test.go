package adjustments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestQ2_A2TaxShieldDTA_Populated is the named per-spec-§8.1 pin for the
// Q2 resolution: A2 (indefinite-lived intangible writedown) populates
// LedgerEntry.TaxShieldDTA = writedownAmount * working.EffectiveTaxRate
// when ETR > 0. Replaces the Phase 2 deferral pin.
//
// Why this lives as a standalone test alongside the per-adjuster suite:
//   - explicit grep target for Phase 3 acceptance review (the spec
//     names the test verbatim);
//   - documents the policy/math relationship for Phase 4 maintainers
//     who would otherwise need to read the Phase 3 spec to know
//     A2 ≅ A5 on this dimension.
func TestQ2_A2TaxShieldDTA_Populated(t *testing.T) {
	tests := []struct {
		name                   string
		intangibles            float64
		totalAssets            float64
		effectiveTaxRate       float64
		expectedTaxShieldDelta float64 // 0 means assert exactly zero
	}{
		{
			name:                   "ETR=25%, $100M writedown produces $25M shield",
			intangibles:            500_000.0, // tier "≥300k" → retention 1/3, writedown 333_333.33
			totalAssets:            1_000_000.0,
			effectiveTaxRate:       0.25,
			expectedTaxShieldDelta: 500_000.0 * (1 - 1.0/3.0) * 0.25, // ≈ 83,333.33
		},
		{
			name:                   "ETR=21% on $250k intangibles fires the 0.3 tier",
			intangibles:            250_000.0,
			totalAssets:            1_000_000.0,
			effectiveTaxRate:       0.21,
			expectedTaxShieldDelta: 250_000.0 * (1 - 0.3) * 0.21, // = 36,750
		},
		{
			name:                   "ETR=0 leaves TaxShieldDTA at zero (A5 convention)",
			intangibles:            500_000.0,
			totalAssets:            1_000_000.0,
			effectiveTaxRate:       0.0,
			expectedTaxShieldDelta: 0.0,
		},
		{
			name:                   "negative ETR (data error) leaves TaxShieldDTA at zero",
			intangibles:            500_000.0,
			totalAssets:            1_000_000.0,
			effectiveTaxRate:       -0.05,
			expectedTaxShieldDelta: 0.0,
		},
	}

	aa := NewAssetAdjuster()
	adj := NewA2IntangibleAdjuster(aa)
	rule := createIntangibleRule()
	cleaningCtx := &entities.CleaningContext{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := &entities.FinancialData{
				OtherIntangibles: tt.intangibles,
				TotalAssets:      tt.totalAssets,
				EffectiveTaxRate: tt.effectiveTaxRate,
			}

			out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
			require.NoError(t, err)
			require.Len(t, out.LedgerEntries, 1)
			entry := out.LedgerEntries[0]
			require.True(t, entry.Fired, "fixture chosen to fire A2")

			assert.InDelta(t, tt.expectedTaxShieldDelta, entry.TaxShieldDTA, 1e-6)
		})
	}
}
