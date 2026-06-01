package adjustments

import (
	gocontext "context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

func TestAssetAdjuster_ProcessGoodwillAdjustment(t *testing.T) {
	tests := []struct {
		name             string
		financialData    *entities.FinancialData
		rule             *entities.CleaningRule
		expectedAdjusted *entities.FinancialData
		expectedFlags    int
		expectedAmount   float64
	}{
		{
			name: "significant goodwill exclusion - A1",
			financialData: &entities.FinancialData{
				Goodwill:         500000.0, // 50% of total assets
				TotalAssets:      1000000.0,
				OtherIntangibles: 100000.0,
			},
			rule: createGoodwillRule(),
			expectedAdjusted: &entities.FinancialData{
				Goodwill:         0.0,      // Excluded from invested capital
				TotalAssets:      500000.0, // Reduced by goodwill amount
				OtherIntangibles: 100000.0, // Unchanged
			},
			expectedFlags:  1,
			expectedAmount: 500000.0,
		},
		{
			name: "minimal goodwill - no adjustment needed",
			financialData: &entities.FinancialData{
				Goodwill:         50000.0, // 5% of total assets
				TotalAssets:      1000000.0,
				OtherIntangibles: 100000.0,
				TangibleAssets:   850000.0,
			},
			rule: createGoodwillRule(),
			expectedAdjusted: &entities.FinancialData{
				Goodwill:         50000.0,   // No change - below threshold
				TotalAssets:      1000000.0, // No change
				OtherIntangibles: 100000.0,  // No change
				TangibleAssets:   850000.0,  // No change
			},
			expectedFlags:  0,
			expectedAmount: 0.0,
		},
		{
			name: "no goodwill present",
			financialData: &entities.FinancialData{
				Goodwill:         0.0,
				TotalAssets:      1000000.0,
				OtherIntangibles: 100000.0,
				TangibleAssets:   900000.0,
			},
			rule: createGoodwillRule(),
			expectedAdjusted: &entities.FinancialData{
				Goodwill:         0.0,       // No change
				TotalAssets:      1000000.0, // No change
				OtherIntangibles: 100000.0,  // No change
				TangibleAssets:   900000.0,  // No change
			},
			expectedFlags:  0,
			expectedAmount: 0.0,
		},
	}

	adjuster := NewAssetAdjuster()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Deep copy original data to avoid mutation
			originalData := copyFinancialData(tt.financialData)

			result := adjuster.ProcessGoodwillAdjustment(originalData, tt.rule)

			// Verify adjustment was applied correctly
			assert.Equal(t, tt.expectedAdjusted.Goodwill, originalData.Goodwill, "Goodwill should be adjusted correctly")
			assert.Equal(t, tt.expectedAdjusted.TotalAssets, originalData.TotalAssets, "Total assets should be adjusted correctly")

			// Verify adjustment result
			require.NotNil(t, result, "Should return adjustment result")
			assert.Equal(t, tt.expectedAmount, result.Amount, "Adjustment amount should match")

			// Verify flags were generated appropriately
			if tt.expectedFlags > 0 {
				assert.NotEmpty(t, result.Flags, "Should generate flags for significant adjustments")
				assert.Len(t, result.Flags, tt.expectedFlags, "Should generate expected number of flags")
				assert.Equal(t, "goodwill_exclusion", result.Flags[0].Type, "Should flag goodwill exclusion")
			} else {
				assert.Empty(t, result.Flags, "Should not generate flags for minimal adjustments")
			}
		})
	}
}

func TestAssetAdjuster_ProcessIntangibleAdjustment(t *testing.T) {
	tests := []struct {
		name             string
		financialData    *entities.FinancialData
		rule             *entities.CleaningRule
		expectedAdjusted *entities.FinancialData
		expectedFlags    int
		expectedAmount   float64
	}{
		{
			name: "indefinite-lived intangibles adjustment - A2",
			financialData: &entities.FinancialData{
				OtherIntangibles: 300000.0, // 30% of total assets
				TotalAssets:      1000000.0,
				TangibleAssets:   600000.0, // 60% of total assets
			},
			rule: createIntangibleRule(),
			expectedAdjusted: &entities.FinancialData{
				OtherIntangibles: 100000.0, // Reduced by indefinite-lived amount
				TotalAssets:      800000.0, // Reduced by excluded intangibles
				TangibleAssets:   600000.0, // 60% of total assets
			},
			expectedFlags:  1,
			expectedAmount: 200000.0,
		},
		{
			name: "minimal intangibles - conservative write-down",
			financialData: &entities.FinancialData{
				OtherIntangibles: 100000.0, // 10% of total assets
				TotalAssets:      1000000.0,
				TangibleAssets:   800000.0,
			},
			rule: createIntangibleRule(),
			expectedAdjusted: &entities.FinancialData{
				OtherIntangibles: 20000.0,  // Reduced
				TotalAssets:      920000.0, // Reduced
				TangibleAssets:   800000.0, // Unchanged
			},
			expectedFlags:  1,
			expectedAmount: 80000.0,
		},
	}

	adjuster := NewAssetAdjuster()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalData := copyFinancialData(tt.financialData)

			result := adjuster.ProcessIntangibleAdjustment(originalData, tt.rule)

			assert.Equal(t, tt.expectedAdjusted.OtherIntangibles, originalData.OtherIntangibles, "Intangible assets should be adjusted")
			assert.Equal(t, tt.expectedAdjusted.TotalAssets, originalData.TotalAssets, "Total assets should be adjusted")

			require.NotNil(t, result, "Should return adjustment result")
			assert.Equal(t, tt.expectedAmount, result.Amount, "Adjustment amount should match")
			assert.Len(t, result.Flags, tt.expectedFlags, "Should generate expected flags")
		})
	}
}

func TestAssetAdjuster_ProcessInventoryAdjustment(t *testing.T) {
	tests := []struct {
		name             string
		financialData    *entities.FinancialData
		rule             *entities.CleaningRule
		context          *entities.CleaningContext
		expectedAdjusted *entities.FinancialData
		expectedFlags    int
		expectedAmount   float64
	}{
		{
			name: "dead inventory detection and writedown - A5",
			financialData: &entities.FinancialData{
				Inventory:         400000.0, // 40% of total assets - excessive
				TotalAssets:       1000000.0,
				InventoryTurnover: 2.0, // Low turnover indicates obsolescence
			},
			rule:    createInventoryRule(),
			context: createRetailContext(), // Retail context with higher inventory tolerance
			expectedAdjusted: &entities.FinancialData{
				Inventory:         240000.0, // 40% haircut applied (400k * 0.6)
				TotalAssets:       840000.0, // Reduced by inventory writedown
				InventoryTurnover: 2.0,      // Unchanged
			},
			expectedFlags:  1,
			expectedAmount: 160000.0, // 40% of 400k written down
		},
		{
			name: "healthy inventory levels - no adjustment",
			financialData: &entities.FinancialData{
				Inventory:         150000.0, // 15% of total assets - normal
				TotalAssets:       1000000.0,
				InventoryTurnover: 6.0, // Healthy turnover
			},
			rule:    createInventoryRule(),
			context: createRetailContext(),
			expectedAdjusted: &entities.FinancialData{
				Inventory:         150000.0,  // No change
				TotalAssets:       1000000.0, // No change
				InventoryTurnover: 6.0,       // No change
			},
			expectedFlags:  0,
			expectedAmount: 0.0,
		},
		{
			name: "tech company with any inventory - concerning",
			financialData: &entities.FinancialData{
				Inventory:         80000.0, // 8% of total assets - high for tech
				TotalAssets:       1000000.0,
				InventoryTurnover: 4.0,
			},
			rule:    createInventoryRule(),
			context: createTechContext(), // Tech context with very low inventory tolerance
			expectedAdjusted: &entities.FinancialData{
				Inventory:         48000.0,  // 40% haircut
				TotalAssets:       968000.0, // Reduced
				InventoryTurnover: 4.0,
			},
			expectedFlags:  1,
			expectedAmount: 32000.0, // 40% of 80k
		},
	}

	adjuster := NewAssetAdjuster()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalData := copyFinancialData(tt.financialData)

			result := adjuster.ProcessInventoryAdjustment(originalData, tt.rule, tt.context)

			assert.Equal(t, tt.expectedAdjusted.Inventory, originalData.Inventory, "Inventory should be adjusted correctly")
			assert.Equal(t, tt.expectedAdjusted.TotalAssets, originalData.TotalAssets, "Total assets should be adjusted correctly")

			require.NotNil(t, result, "Should return adjustment result")
			assert.Equal(t, tt.expectedAmount, result.Amount, "Adjustment amount should match")
			assert.Len(t, result.Flags, tt.expectedFlags, "Should generate expected flags")

			if tt.expectedFlags > 0 {
				assert.Equal(t, "dead_inventory", result.Flags[0].Type, "Should flag dead inventory")
			}
		})
	}
}

func TestAssetAdjuster_ProcessDeferredTaxAdjustment(t *testing.T) {
	tests := []struct {
		name             string
		financialData    *entities.FinancialData
		rule             *entities.CleaningRule
		expectedAdjusted *entities.FinancialData
		expectedFlags    int
		expectedAmount   float64
	}{
		{
			name: "significant DTA with valuation allowance - A4",
			financialData: &entities.FinancialData{
				DeferredTaxAssets: 200000.0, // 20% of total assets
				TotalAssets:       1000000.0,
				EffectiveTaxRate:  0.21,
			},
			rule: createDeferredTaxRule(),
			expectedAdjusted: &entities.FinancialData{
				DeferredTaxAssets: 100000.0, // Haircut applied based on realizability
				TotalAssets:       900000.0, // Reduced by DTA writedown
				EffectiveTaxRate:  0.21,     // Unchanged
			},
			expectedFlags:  1,
			expectedAmount: 100000.0, // Amount written down
		},
		{
			name: "minimal DTA - no adjustment needed",
			financialData: &entities.FinancialData{
				DeferredTaxAssets: 30000.0, // 3% of total assets - minimal
				TotalAssets:       1000000.0,
				EffectiveTaxRate:  0.21,
			},
			rule: createDeferredTaxRule(),
			expectedAdjusted: &entities.FinancialData{
				DeferredTaxAssets:  30000.0,   // No change - below threshold
				TotalAssets:        1000000.0, // No change
				ValuationAllowance: 5000.0,    // No change
				EffectiveTaxRate:   0.21,      // No change
			},
			expectedFlags:  0,
			expectedAmount: 0.0,
		},
	}

	adjuster := NewAssetAdjuster()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalData := copyFinancialData(tt.financialData)

			result := adjuster.ProcessDeferredTaxAdjustment(originalData, tt.rule)

			assert.Equal(t, tt.expectedAdjusted.DeferredTaxAssets, originalData.DeferredTaxAssets, "DTA should be adjusted correctly")
			assert.Equal(t, tt.expectedAdjusted.TotalAssets, originalData.TotalAssets, "Total assets should be adjusted correctly")

			require.NotNil(t, result, "Should return adjustment result")
			assert.Equal(t, tt.expectedAmount, result.Amount, "Adjustment amount should match")
			assert.Len(t, result.Flags, tt.expectedFlags, "Should generate expected flags")
		})
	}
}

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
