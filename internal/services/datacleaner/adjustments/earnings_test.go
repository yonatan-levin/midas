package adjustments

import (
	gocontext "context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

func TestNewEarningsAdjuster(t *testing.T) {
	adjuster := NewEarningsAdjuster()
	assert.NotNil(t, adjuster)
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

	result := adjuster.ProcessEarningsAdjustments(gocontext.Background(), data, rules, context)

	// DC-1 Phase 5 P5-C4: the legacy result.Applied / .Adjustments / .Reasoning
	// fields were deleted. The three earnings adjustments (restructuring
	// add-back + asset-sale subtraction fired Restaters, plus the
	// stock-compensation reclassify) now surface natively: two fired Restater
	// LedgerEntries + the C4 dilution Flag. The projected entities.Adjustment
	// audit trail (count == 3) is covered end-to-end by the basket-parity
	// golden in package datacleaner.
	firedRestaters := 0
	for _, e := range result.NativeLedgerEntries {
		if e.Fired {
			firedRestaters++
		}
	}
	assert.Equal(t, 2, firedRestaters, "restructuring + asset-sale fire as Restaters")
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
