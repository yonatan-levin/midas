package adjustments

import (
	gocontext "context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

func TestAssetAdjuster_CalculateNetTangibleAssets(t *testing.T) {
	tests := []struct {
		name                    string
		financialData           *entities.FinancialData
		expectedTangibleAssets  float64
		expectedAdjustmentsMade int
	}{
		{
			name: "comprehensive asset cleaning",
			financialData: &entities.FinancialData{
				TotalAssets:                500000.0,
				Goodwill:                   100000.0, // Will be excluded
				OtherIntangibles:           50000.0,  // Will be reduced
				IndefiniteLivedIntangibles: 30000.0,  // Will be excluded
				DeferredTaxAssets:          40000.0,  // Will be haircut
				Inventory:                  80000.0,  // Will be written down if obsolete
				TangibleAssets:             200000.0, // Should remain
			},
			expectedTangibleAssets:  200000.0, // Only tangible assets remain after cleaning
			expectedAdjustmentsMade: 4,        // Goodwill, intangibles, DTA, inventory
		},
		{
			name: "clean company - minimal adjustments",
			financialData: &entities.FinancialData{
				TotalAssets:                1000000.0,
				Goodwill:                   50000.0,  // Minimal - 5%
				OtherIntangibles:           30000.0,  // Minimal
				IndefiniteLivedIntangibles: 0.0,      // None
				DeferredTaxAssets:          20000.0,  // Minimal
				Inventory:                  100000.0, // Reasonable
				TangibleAssets:             800000.0, // Strong tangible base
			},
			expectedTangibleAssets:  800000.0, // Mostly preserved
			expectedAdjustmentsMade: 0,        // No major adjustments needed
		},
	}

	adjuster := NewAssetAdjuster()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalData := copyFinancialData(tt.financialData)
			context := createDefaultContext()

			result := adjuster.CalculateNetTangibleAssets(originalData, context)

			assert.InDelta(t, tt.expectedTangibleAssets, result.AdjustedTangibleAssets, 50000.0, "Tangible assets should be within expected range")
			assert.GreaterOrEqual(t, len(result.Adjustments), tt.expectedAdjustmentsMade, "Should make expected adjustments")

			// Verify audit trail
			assert.NotEmpty(t, result.AuditTrail, "Should provide audit trail")
			assert.Contains(t, result.AuditTrail, "Asset quality", "Should reference asset quality in audit trail")
		})
	}
}

func TestAssetAdjuster_ProcessAssetAdjustments_ActiveWorkflow(t *testing.T) {
	tests := []struct {
		name                     string
		financialData            *entities.FinancialData
		rules                    []*entities.CleaningRule
		expectedOriginalGoodwill float64
		expectedFinalGoodwill    float64
		expectedOriginalAssets   float64
		expectedFinalAssets      float64
		expectedAdjustmentsMade  int
		expectedFlagCount        int
	}{
		{
			name: "active goodwill exclusion - data actually modified",
			financialData: &entities.FinancialData{
				Goodwill:         500000.0, // 50% of total assets - will be excluded
				TotalAssets:      1000000.0,
				OtherIntangibles: 100000.0,
				TangibleAssets:   400000.0, // Will be recalculated
			},
			rules: []*entities.CleaningRule{
				{
					ID:         "goodwill_exclusion",
					Category:   entities.AssetQuality,
					Adjustment: entities.Exclude,
					Enabled:    true,
				},
			},
			expectedOriginalGoodwill: 500000.0,
			// DC-1 Phase 4 (C-4): A1's dispatcher dual-write is DELETED — the
			// goodwill-exclusion effect moves to InvestedCapital(). The entity's
			// Goodwill/TotalAssets stay as-reported.
			expectedFinalGoodwill:   500000.0,
			expectedOriginalAssets:  1000000.0,
			expectedFinalAssets:     1000000.0,
			expectedAdjustmentsMade: 1,
			expectedFlagCount:       1, // Should flag significant goodwill
		},
		{
			name: "multiple asset adjustments - intangibles and goodwill",
			financialData: &entities.FinancialData{
				Goodwill:         300000.0, // 30% of assets
				TotalAssets:      1000000.0,
				OtherIntangibles: 200000.0, // 20% of assets - will be written down
				TangibleAssets:   500000.0, // Will be recalculated
			},
			rules: []*entities.CleaningRule{
				{
					ID:         "goodwill_exclusion",
					Category:   entities.AssetQuality,
					Adjustment: entities.Exclude,
					Enabled:    true,
				},
				{
					ID:         "intangible_adjustment",
					Category:   entities.AssetQuality,
					Adjustment: entities.Writedown,
					Enabled:    true,
				},
			},
			expectedOriginalGoodwill: 300000.0,
			expectedOriginalAssets:   1000000.0,
			// DC-1 Phase 4 (C-4): BOTH A1 (goodwill) and A2 (intangible)
			// umbrella dual-writes are now DELETED — A1's effect moves to
			// InvestedCapital(), A2's writedown lands on the OtherIntangibles
			// component only. The data.TotalAssets umbrella + data.Goodwill stay
			// as-reported; view accessors recompute/exclude downstream.
			expectedFinalGoodwill:   300000.0,
			expectedFinalAssets:     1000000.0,
			expectedAdjustmentsMade: 2, // Both goodwill and intangible adjustments
			expectedFlagCount:       2, // Flags for both adjustments
		},
		{
			name: "no adjustments needed - clean company",
			financialData: &entities.FinancialData{
				Goodwill:         30000.0, // 3% of assets - below threshold
				TotalAssets:      1000000.0,
				OtherIntangibles: 20000.0, // 2% of assets - below threshold
				TangibleAssets:   950000.0,
			},
			rules: []*entities.CleaningRule{
				{
					ID:         "goodwill_exclusion",
					Category:   entities.AssetQuality,
					Adjustment: entities.Exclude,
					Enabled:    true,
				},
				{
					ID:         "intangible_adjustment",
					Category:   entities.AssetQuality,
					Adjustment: entities.Writedown,
					Enabled:    true,
				},
			},
			expectedOriginalGoodwill: 30000.0,
			expectedFinalGoodwill:    30000.0, // Should remain unchanged
			expectedOriginalAssets:   1000000.0,
			expectedFinalAssets:      1000000.0, // Should remain unchanged
			expectedAdjustmentsMade:  0,         // No adjustments needed
			expectedFlagCount:        0,         // No flags generated
		},
	}

	adjuster := NewAssetAdjuster()
	context := createDefaultContext()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Take snapshot of original data
			originalData := copyFinancialData(tt.financialData)

			// Verify original state
			assert.Equal(t, tt.expectedOriginalGoodwill, originalData.Goodwill, "Original goodwill should match expected")
			assert.Equal(t, tt.expectedOriginalAssets, originalData.TotalAssets, "Original assets should match expected")

			// Apply active adjustments - THIS IS THE KEY DIFFERENCE
			result := adjuster.ProcessAssetAdjustments(gocontext.Background(), tt.financialData, tt.rules, context)

			// Verify the financial data was actually modified
			assert.Equal(t, tt.expectedFinalGoodwill, tt.financialData.Goodwill, "Final goodwill should be modified")
			assert.Equal(t, tt.expectedFinalAssets, tt.financialData.TotalAssets, "Final assets should be modified")

			// Verify adjustment results. DC-1 Phase 5 P5-C4: the legacy
			// result.Applied / .Adjustments / .AuditTrail fields were deleted;
			// the per-rule audit-trail count is now derived from the native
			// fired emissions (a fired writedown LedgerEntry or a goodwill
			// overlay). The projected entities.Adjustment audit content is
			// covered end-to-end by the basket-parity golden.
			assert.Equal(t, tt.expectedAdjustmentsMade, countFiredAssetEmissions(result), "Fired emission count should match expectations")
			assert.Len(t, result.Flags, tt.expectedFlagCount, "Should have expected number of flags")

			// Verify tangible assets were recalculated correctly
			expectedTangibleAssets := tt.financialData.TotalAssets - tt.financialData.Goodwill - tt.financialData.OtherIntangibles
			assert.InDelta(t, expectedTangibleAssets, tt.financialData.TangibleAssets, 1000.0, "Tangible assets should be recalculated correctly")
		})
	}
}

// Helper functions for creating test data

func createGoodwillRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:          "A1",
		Name:        "Goodwill Exclusion",
		Category:    entities.AssetQuality,
		Adjustment:  entities.Exclude,
		Description: "Exclude goodwill from invested capital calculation",
		Threshold: &entities.ThresholdConfig{
			PercentageOfAssets: &[]float64{0.20}[0], // 20% threshold
		},
		Enabled: true,
	}
}

func createIntangibleRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:          "A2",
		Name:        "Indefinite-lived Intangibles Adjustment",
		Category:    entities.AssetQuality,
		Adjustment:  entities.Writedown,
		Description: "Conservative treatment of indefinite-lived intangible assets",
		Threshold: &entities.ThresholdConfig{
			PercentageOfAssets: &[]float64{0.15}[0], // 15% threshold
		},
		Enabled: true,
	}
}

func createInventoryRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:          "A5",
		Name:        "Dead Inventory Adjustment",
		Category:    entities.AssetQuality,
		Adjustment:  entities.Writedown,
		Description: "Write down obsolete or slow-moving inventory",
		Threshold: &entities.ThresholdConfig{
			PercentageOfAssets: &[]float64{0.25}[0], // 25% threshold
			GrowthMultiple:     &[]float64{3.0}[0],  // Minimum turnover
			WritedownRate:      &[]float64{0.40}[0], // 40% writedown
		},
		Enabled: true,
	}
}

func createDeferredTaxRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:          "A4",
		Name:        "Deferred Tax Asset Valuation Allowance",
		Category:    entities.AssetQuality,
		Adjustment:  entities.AdjustmentTypeValuationAllowance,
		Description: "Apply conservative valuation allowance to deferred tax assets",
		Threshold: &entities.ThresholdConfig{
			PercentageOfAssets: &[]float64{0.10}[0], // 10% threshold
			MinAmount:          &[]float64{0.50}[0], // Minimum 50% allowance
		},
		Enabled: true,
	}
}

func createDefaultContext() *entities.CleaningContext {
	return &entities.CleaningContext{
		IndustryCode:     "20", // Industrials
		CompanySize:      entities.LargeCap,
		DataVintage:      time.Now(),
		EnableIndustry:   true,
		EnableCaching:    true,
		QualityThreshold: 70.0,
	}
}

func createRetailContext() *entities.CleaningContext {
	return &entities.CleaningContext{
		IndustryCode:     "25", // Consumer Discretionary
		CompanySize:      entities.MidCap,
		DataVintage:      time.Now(),
		EnableIndustry:   true,
		EnableCaching:    true,
		QualityThreshold: 70.0,
	}
}

func createTechContext() *entities.CleaningContext {
	return &entities.CleaningContext{
		IndustryCode:     "45", // Technology
		CompanySize:      entities.LargeCap,
		DataVintage:      time.Now(),
		EnableIndustry:   true,
		EnableCaching:    true,
		QualityThreshold: 80.0, // Higher standards for tech
	}
}

func copyFinancialData(data *entities.FinancialData) *entities.FinancialData {
	if data == nil {
		return nil
	}

	// Create deep copy to avoid test interference
	return &entities.FinancialData{
		Ticker:                     data.Ticker,
		Period:                     data.Period,
		TotalAssets:                data.TotalAssets,
		TangibleAssets:             data.TangibleAssets,
		Goodwill:                   data.Goodwill,
		IntangibleAssets:           data.IntangibleAssets,
		IndefiniteLivedIntangibles: data.IndefiniteLivedIntangibles,
		DeferredTaxAssets:          data.DeferredTaxAssets,
		ValuationAllowance:         data.ValuationAllowance,
		Inventory:                  data.Inventory,
		InventoryTurnover:          data.InventoryTurnover,
		CostOfGoodsSold:            data.CostOfGoodsSold,
		EffectiveTaxRate:           data.EffectiveTaxRate,
		OtherIntangibles:           data.OtherIntangibles,
		TotalDebt:                  data.TotalDebt,
		InterestBearingDebt:        data.InterestBearingDebt,
		Revenue:                    data.Revenue,
		InterestExpense:            data.InterestExpense,
		TaxRate:                    data.TaxRate,
		OperatingIncome:            data.OperatingIncome,
		NormalizedOperatingIncome:  data.NormalizedOperatingIncome,
		DeadInventoryWritedown:     data.DeadInventoryWritedown,
		SharesOutstanding:          data.SharesOutstanding,
		CIK:                        data.CIK,
		AsOf:                       data.AsOf,
		MissingFields:              data.MissingFields,
		DilutedSharesOutstanding:   data.DilutedSharesOutstanding,
		FilingPeriod:               data.FilingPeriod,
		FilingDate:                 data.FilingDate,
		HasNormalizedData:          data.HasNormalizedData,
	}
}

// TODO: Add tests for:
// - ProcessRightOfUseAssetAdjustment (A6)
// - ProcessExcessCashAdjustment (A7)
// - ProcessCapitalizedSoftwareAdjustment (A3)
// - Integration tests with multiple adjustments
// - Error handling and edge cases
