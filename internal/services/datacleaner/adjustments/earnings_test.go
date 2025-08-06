package adjustments

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

func TestNewEarningsAdjuster(t *testing.T) {
	adjuster := NewEarningsAdjuster()
	assert.NotNil(t, adjuster)
}

func TestProcessRestructuringChargesAdjustment(t *testing.T) {
	tests := []struct {
		name            string
		data            *entities.FinancialData
		rule            *entities.CleaningRule
		expectedAmount  float64
		expectedApplied bool
		expectedReason  string
	}{
		{
			name: "significant_restructuring_charges",
			data: &entities.FinancialData{
				Ticker:                    "TEST",
				Revenue:                   1000000000, // $1B revenue
				OperatingIncome:           150000000,  // $150M operating income
				NormalizedOperatingIncome: 150000000,  // Will be adjusted
				// Assume 3% of revenue in restructuring charges
			},
			rule: &entities.CleaningRule{
				ID:          "restructuring_charges",
				Name:        "Restructuring and Integration Charges",
				Description: "Strip recurring restructuring charges from EBITDA",
				Category:    entities.EarningsNormalization,
				Adjustment:  entities.Exclude,
				Threshold: &entities.ThresholdConfig{
					PercentageOfRevenue: floatPtr(0.02), // 2% threshold
				},
				Severity: entities.Info,
			},
			expectedAmount:  30000000, // 3% of $1B = $30M (above 2% threshold)
			expectedApplied: true,
			expectedReason:  "Restructuring charges adjustment: Excluded $30.0M (3.0% of revenue) from normalized operating income",
		},
		{
			name: "minimal_restructuring_charges",
			data: &entities.FinancialData{
				Ticker:                    "TEST",
				Revenue:                   1000000000, // $1B revenue
				OperatingIncome:           150000000,  // $150M operating income
				NormalizedOperatingIncome: 150000000,
				// Assume 1% of revenue in restructuring charges (below threshold)
			},
			rule: &entities.CleaningRule{
				ID:         "restructuring_charges",
				Category:   entities.EarningsNormalization,
				Adjustment: entities.Exclude,
				Threshold: &entities.ThresholdConfig{
					PercentageOfRevenue: floatPtr(0.02), // 2% threshold
				},
				Severity: entities.Info,
			},
			expectedAmount:  0, // Below threshold, no adjustment
			expectedApplied: false,
			expectedReason:  "Restructuring charges below materiality threshold (1.0% < 2.0%)",
		},
		{
			name: "no_revenue_data",
			data: &entities.FinancialData{
				Ticker:                    "TEST",
				Revenue:                   0, // No revenue data
				OperatingIncome:           150000000,
				NormalizedOperatingIncome: 150000000,
			},
			rule: &entities.CleaningRule{
				ID:         "restructuring_charges",
				Category:   entities.EarningsNormalization,
				Adjustment: entities.Exclude,
				Severity:   entities.Info,
			},
			expectedAmount:  0,
			expectedApplied: false,
			expectedReason:  "Insufficient revenue data to calculate restructuring charges",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adjuster := NewEarningsAdjuster()

			// Set up test data with estimated restructuring charges
			if tt.data.Revenue > 0 {
				// Simulate restructuring charges based on test scenario
				// nolint:staticcheck // explicit case easier to read here
				if tt.name == "significant_restructuring_charges" {
					// 3% of revenue
					tt.data.RestructuringCharges = tt.data.Revenue * 0.03
				} else if tt.name == "minimal_restructuring_charges" {
					// 1% of revenue
					tt.data.RestructuringCharges = tt.data.Revenue * 0.01
				}
			}

			result := adjuster.ProcessRestructuringChargesAdjustment(tt.data, tt.rule)

			assert.Equal(t, tt.expectedApplied, result.Applied, "Applied status should match")
			if tt.expectedApplied {
				assert.InDelta(t, tt.expectedAmount, result.Amount, 1000, "Amount should be within tolerance")
				assert.Len(t, result.Adjustments, 1, "Should have one adjustment")
				assert.Contains(t, result.Reasoning, "Restructuring charges adjustment", "Reasoning should mention restructuring")

				// Verify the adjustment details
				adjustment := result.Adjustments[0]
				assert.Equal(t, "RestructuringCharges", adjustment.FromAccount)
				assert.Equal(t, "NormalizedOperatingIncome", adjustment.ToAccount)
				assert.Equal(t, entities.Exclude, adjustment.Type)
				assert.InDelta(t, tt.expectedAmount, adjustment.Amount, 1000)
			} else {
				assert.Equal(t, 0.0, result.Amount, "Amount should be zero when not applied")
				assert.Contains(t, result.Reasoning, tt.expectedReason, "Reasoning should match expected")
			}
		})
	}
}

func TestProcessAssetSaleGainsAdjustment(t *testing.T) {
	tests := []struct {
		name            string
		data            *entities.FinancialData
		rule            *entities.CleaningRule
		expectedAmount  float64
		expectedApplied bool
	}{
		{
			name: "significant_asset_sale_gains",
			data: &entities.FinancialData{
				Ticker:                    "TEST",
				Revenue:                   2000000000, // $2B revenue
				OperatingIncome:           300000000,  // $300M operating income
				NormalizedOperatingIncome: 300000000,
				AssetSaleGains:            15000000, // $15M in gains (0.75% of revenue)
			},
			rule: &entities.CleaningRule{
				ID:         "asset_sale_gains",
				Category:   entities.EarningsNormalization,
				Adjustment: entities.Exclude,
				Severity:   entities.Info,
			},
			expectedAmount:  15000000, // $15M gains to be excluded
			expectedApplied: true,
		},
		{
			name: "no_asset_sale_gains",
			data: &entities.FinancialData{
				Ticker:                    "TEST",
				Revenue:                   2000000000,
				OperatingIncome:           300000000,
				NormalizedOperatingIncome: 300000000,
				AssetSaleGains:            0, // No gains
			},
			rule: &entities.CleaningRule{
				ID:         "asset_sale_gains",
				Category:   entities.EarningsNormalization,
				Adjustment: entities.Exclude,
				Severity:   entities.Info,
			},
			expectedAmount:  0,
			expectedApplied: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adjuster := NewEarningsAdjuster()
			result := adjuster.ProcessAssetSaleGainsAdjustment(tt.data, tt.rule)

			assert.Equal(t, tt.expectedApplied, result.Applied)
			assert.InDelta(t, tt.expectedAmount, result.Amount, 1000)

			if tt.expectedApplied {
				assert.Len(t, result.Adjustments, 1)
				adjustment := result.Adjustments[0]
				assert.Equal(t, "AssetSaleGains", adjustment.FromAccount)
				assert.Equal(t, "NormalizedOperatingIncome", adjustment.ToAccount)
			}
		})
	}
}

func TestProcessStockCompensationAdjustment(t *testing.T) {
	tests := []struct {
		name            string
		data            *entities.FinancialData
		rule            *entities.CleaningRule
		expectedAmount  float64
		expectedApplied bool
	}{
		{
			name: "significant_stock_compensation",
			data: &entities.FinancialData{
				Ticker:                    "TECH",
				Revenue:                   5000000000, // $5B revenue
				OperatingIncome:           1000000000, // $1B operating income
				NormalizedOperatingIncome: 1000000000,
				StockBasedCompensation:    200000000,  // $200M in stock comp (4% of revenue)
				SharesOutstanding:         1000000000, // 1B shares
			},
			rule: &entities.CleaningRule{
				ID:         "stock_compensation",
				Category:   entities.EarningsNormalization,
				Adjustment: entities.Reclassify,
				Severity:   entities.Info,
			},
			expectedAmount:  200000000, // $200M to be reclassified
			expectedApplied: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adjuster := NewEarningsAdjuster()
			result := adjuster.ProcessStockCompensationAdjustment(tt.data, tt.rule)

			assert.Equal(t, tt.expectedApplied, result.Applied)
			assert.InDelta(t, tt.expectedAmount, result.Amount, 1000)

			if tt.expectedApplied {
				assert.Len(t, result.Adjustments, 1)
				adjustment := result.Adjustments[0]
				assert.Equal(t, "StockBasedCompensation", adjustment.FromAccount)
				assert.Equal(t, entities.Reclassify, adjustment.Type)
				assert.Contains(t, result.Reasoning, "Stock-based compensation")
			}
		})
	}
}

func TestProcessEarningsAdjustments(t *testing.T) {
	// Integration test for multiple earnings adjustments
	data := &entities.FinancialData{
		Ticker:                    "MULTI",
		Revenue:                   3000000000, // $3B revenue
		OperatingIncome:           450000000,  // $450M operating income
		NormalizedOperatingIncome: 450000000,  // Will be adjusted
		RestructuringCharges:      90000000,   // $90M (3% of revenue)
		AssetSaleGains:            30000000,   // $30M gains
		StockBasedCompensation:    150000000,  // $150M stock comp
		SharesOutstanding:         500000000,  // 500M shares
	}

	rules := []*entities.CleaningRule{
		{
			ID:         "restructuring_charges",
			Category:   entities.EarningsNormalization,
			Adjustment: entities.Exclude,
			Enabled:    true,
			Threshold: &entities.ThresholdConfig{
				PercentageOfRevenue: floatPtr(0.02), // 2% threshold
			},
			Severity: entities.Info,
		},
		{
			ID:         "asset_sale_gains",
			Category:   entities.EarningsNormalization,
			Adjustment: entities.Exclude,
			Enabled:    true,
			Severity:   entities.Info,
		},
		{
			ID:         "stock_compensation",
			Category:   entities.EarningsNormalization,
			Adjustment: entities.Reclassify,
			Enabled:    true,
			Severity:   entities.Info,
		},
	}

	adjuster := NewEarningsAdjuster()
	context := &entities.CleaningContext{
		IndustryCode:     "45", // Technology
		CompanySize:      entities.LargeCap,
		EnableIndustry:   true,
		QualityThreshold: 70.0,
	}

	result := adjuster.ProcessEarningsAdjustments(data, rules, context)

	assert.True(t, result.Applied, "Should apply earnings adjustments")
	assert.Len(t, result.Adjustments, 3, "Should have three adjustments")
	assert.Len(t, result.Flags, 1, "Should have one flag for stock compensation dilution analysis")

	// Verify the stock compensation flag
	stockFlag := result.Flags[0]
	assert.Equal(t, "earnings_quality", stockFlag.Type, "Should be earnings quality flag")
	assert.Equal(t, "stock_compensation", stockFlag.RuleID, "Should be from stock compensation rule")
	assert.InDelta(t, 150000000.0, stockFlag.Amount, 1000, "Flag amount should match stock compensation")

	// Verify normalized operating income was adjusted
	// Original: $450M
	// Add back restructuring: +$90M = $540M
	// Remove asset sale gains: -$30M = $510M
	// Stock comp is reclassified, not excluded from operating income
	expectedNormalizedIncome := 450000000.0 + 90000000.0 - 30000000.0
	assert.InDelta(t, expectedNormalizedIncome, data.NormalizedOperatingIncome, 1000000,
		"Normalized operating income should be adjusted correctly")
}

// Helper function to create float pointer
func floatPtr(f float64) *float64 {
	return &f
}
