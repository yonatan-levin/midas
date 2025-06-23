package sec

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

func TestNewParser(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	assert.NotNil(t, parser)
	assert.NotNil(t, parser.logger)
}

func TestParser_ParseFinancialData_Success(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Create mock SEC company facts
	facts := &ports.SECCompanyFacts{
		CIK:        "0000320193",
		EntityName: "Apple Inc.",
		Facts: map[string]ports.SECFactGroup{
			"us-gaap:Revenues": {
				Label:       "Revenues",
				Description: "Revenue from operations",
				Units: map[string][]ports.SECFact{
					"USD": {
						{
							End:   "2023-09-30",
							Val:   383285000000,
							Accn:  "0000320193-23-000106",
							Fy:    2023,
							Fp:    "FY",
							Form:  "10-K",
							Filed: "2023-11-03",
							Frame: "CY2023Q3I",
						},
					},
				},
			},
			"us-gaap:OperatingIncomeLoss": {
				Label:       "Operating Income Loss",
				Description: "Operating income or loss",
				Units: map[string][]ports.SECFact{
					"USD": {
						{
							End:   "2023-09-30",
							Val:   114301000000,
							Accn:  "0000320193-23-000106",
							Fy:    2023,
							Fp:    "FY",
							Form:  "10-K",
							Filed: "2023-11-03",
							Frame: "CY2023Q3I",
						},
					},
				},
			},
			"us-gaap:Assets": {
				Label:       "Assets",
				Description: "Total assets",
				Units: map[string][]ports.SECFact{
					"USD": {
						{
							End:   "2023-09-30",
							Val:   352755000000,
							Accn:  "0000320193-23-000106",
							Fy:    2023,
							Fp:    "FY",
							Form:  "10-K",
							Filed: "2023-11-03",
							Frame: "CY2023Q3I",
						},
					},
				},
			},
		},
	}

	ctx := context.Background()
	historical, err := parser.ParseFinancialData(ctx, facts)

	require.NoError(t, err)
	assert.NotNil(t, historical)
	assert.Equal(t, "", historical.Ticker) // Ticker is set by caller
	assert.True(t, len(historical.Data) > 0)

	// Check if we parsed the 2023FY period
	data, exists := historical.Data["2023FY"]
	assert.True(t, exists)
	assert.NotNil(t, data)
	assert.Equal(t, "0000320193", data.CIK)
	assert.Equal(t, "2023FY", data.FilingPeriod)
	assert.Equal(t, 383285000000.0, data.Revenue)
	assert.Equal(t, 114301000000.0, data.OperatingIncome)
	assert.Equal(t, 352755000000.0, data.TotalAssets)
}

func TestParser_ParseFinancialData_NilFacts(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	ctx := context.Background()
	historical, err := parser.ParseFinancialData(ctx, nil)

	assert.Error(t, err)
	assert.Nil(t, historical)
	assert.Contains(t, err.Error(), "facts cannot be nil")
}

func TestParser_ParseFinancialData_NoValidData(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Create facts with no valid financial data
	facts := &ports.SECCompanyFacts{
		CIK:        "0000320193",
		EntityName: "Apple Inc.",
		Facts: map[string]ports.SECFactGroup{
			"invalid-concept": {
				Label:       "Invalid",
				Description: "Invalid concept",
				Units: map[string][]ports.SECFact{
					"USD": {
						{
							End:   "2023-09-30",
							Val:   100,
							Accn:  "0000320193-23-000106",
							Fy:    2023,
							Fp:    "FY",
							Form:  "10-K",
							Filed: "2023-11-03",
							Frame: "CY2023Q3I",
						},
					},
				},
			},
		},
	}

	ctx := context.Background()
	historical, err := parser.ParseFinancialData(ctx, facts)

	assert.Error(t, err)
	assert.Nil(t, historical)
	assert.Contains(t, err.Error(), "no valid financial data found")
}

func TestParser_NormalizeFinancialData_Success(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Create sample financial data
	data := &entities.FinancialData{
		Ticker:                   "AAPL",
		CIK:                      "0000320193",
		OperatingIncome:          100000000,
		Revenue:                  400000000,
		TotalAssets:              300000000,
		Goodwill:                 5000000,
		OtherIntangibles:         10000000,
		Inventory:                5000000,
		InterestExpense:          2000000,
		TotalDebt:                50000000,
		SharesOutstanding:        15000000,
		DilutedSharesOutstanding: 15500000,
		TaxRate:                  0.21,
		FilingPeriod:             "2023FY",
		FilingDate:               time.Now(),
		AsOf:                     time.Now(),
	}

	ctx := context.Background()
	normalized, err := parser.NormalizeFinancialData(ctx, data)

	require.NoError(t, err)
	assert.NotNil(t, normalized)
	assert.True(t, normalized.HasNormalizedData)
	assert.Equal(t, "AAPL", normalized.Ticker)

	// Check tangible assets calculation (total assets - goodwill - intangibles)
	expectedTangibleAssets := 300000000.0 - 5000000.0 - 10000000.0
	assert.Equal(t, expectedTangibleAssets, normalized.TangibleAssets)

	// Check normalized operating income
	assert.Equal(t, 100000000.0, normalized.NormalizedOperatingIncome)

	// Check that tax rate is preserved
	assert.Equal(t, 0.21, normalized.TaxRate)
}

func TestParser_NormalizeFinancialData_NilData(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	ctx := context.Background()
	normalized, err := parser.NormalizeFinancialData(ctx, nil)

	assert.Error(t, err)
	assert.Nil(t, normalized)
	assert.Contains(t, err.Error(), "data cannot be nil")
}

func TestParser_NormalizeFinancialData_InvalidTaxRate(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Create data with invalid tax rate
	data := &entities.FinancialData{
		Ticker:          "AAPL",
		CIK:             "0000320193",
		OperatingIncome: 100000000,
		TaxRate:         -0.1, // Invalid tax rate
		FilingPeriod:    "2023FY",
		FilingDate:      time.Now(),
		AsOf:            time.Now(),
	}

	ctx := context.Background()
	normalized, err := parser.NormalizeFinancialData(ctx, data)

	require.NoError(t, err)
	assert.NotNil(t, normalized)
	assert.Equal(t, 0.21, normalized.TaxRate) // Should use default tax rate
	assert.Contains(t, normalized.MissingFields, "tax_rate")
}

func TestParser_NormalizeFinancialData_NegativeTangibleAssets(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Create data where goodwill + intangibles > total assets
	data := &entities.FinancialData{
		Ticker:           "AAPL",
		CIK:              "0000320193",
		OperatingIncome:  100000000,
		TotalAssets:      100000000,
		Goodwill:         60000000,
		OtherIntangibles: 60000000, // Combined > total assets
		TaxRate:          0.21,
		FilingPeriod:     "2023FY",
		FilingDate:       time.Now(),
		AsOf:             time.Now(),
	}

	ctx := context.Background()
	normalized, err := parser.NormalizeFinancialData(ctx, data)

	require.NoError(t, err)
	assert.NotNil(t, normalized)
	assert.Equal(t, 0.0, normalized.TangibleAssets) // Should be clamped to 0
}

func TestParser_GetSupportedConcepts(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	concepts := parser.GetSupportedConcepts()

	assert.NotEmpty(t, concepts)
	// Check for some expected concepts
	conceptMap := make(map[string]bool)
	for _, concept := range concepts {
		conceptMap[concept] = true
	}

	assert.True(t, conceptMap["us-gaap:Revenues"])
	assert.True(t, conceptMap["us-gaap:OperatingIncomeLoss"])
	assert.True(t, conceptMap["us-gaap:Assets"])
}

func TestParser_ExtractFiscalPeriods(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Create facts with multiple periods
	facts := &ports.SECCompanyFacts{
		CIK:        "0000320193",
		EntityName: "Apple Inc.",
		Facts: map[string]ports.SECFactGroup{
			"us-gaap:Revenues": {
				Label:       "Revenues",
				Description: "Revenue from operations",
				Units: map[string][]ports.SECFact{
					"USD": {
						{
							End:   "2023-09-30",
							Val:   383285000000,
							Fy:    2023,
							Fp:    "FY",
							Filed: "2023-11-03",
						},
						{
							End:   "2023-06-30",
							Val:   81797000000,
							Fy:    2023,
							Fp:    "Q3",
							Filed: "2023-08-03",
						},
					},
				},
			},
		},
	}

	periods, err := parser.extractFiscalPeriods(facts)

	require.NoError(t, err)
	assert.NotEmpty(t, periods)
	assert.Contains(t, periods, "2023FY")
	assert.Contains(t, periods, "2023Q3")

	// Check values (using local concept names)
	assert.Equal(t, 383285000000.0, periods["2023FY"]["Revenues"])
	assert.Equal(t, 81797000000.0, periods["2023Q3"]["Revenues"])
}

func TestParser_FindValue(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	data := map[string]float64{
		"Revenues":                        400000000,
		"RevenueFromContractWithCustomer": 350000000,
		"SalesRevenueNet":                 300000000,
	}

	// Test finding the first available value
	val, found := parser.findValue(data, []string{
		"Revenues",
		"RevenueFromContractWithCustomer",
		"SalesRevenueNet",
	})

	assert.True(t, found)
	assert.Equal(t, 400000000.0, val)

	// Test finding fallback value
	val, found = parser.findValue(data, []string{
		"NonExistent",
		"RevenueFromContractWithCustomer",
		"SalesRevenueNet",
	})

	assert.True(t, found)
	assert.Equal(t, 350000000.0, val)

	// Test not finding any value
	val, found = parser.findValue(data, []string{
		"NonExistent1",
		"NonExistent2",
	})

	assert.False(t, found)
	assert.Equal(t, 0.0, val)
}

func TestParser_NormalizeOperatingIncome(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Test with positive income
	normalized := parser.normalizeOperatingIncome(100000000)
	assert.Equal(t, 100000000.0, normalized)

	// Test with negative income (should be preserved)
	normalized = parser.normalizeOperatingIncome(-50000000)
	assert.Equal(t, -50000000.0, normalized)

	// Test with zero
	normalized = parser.normalizeOperatingIncome(0)
	assert.Equal(t, 0.0, normalized)
}

func TestParser_CalculateDeadInventoryWritedown(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Test with normal inventory
	data := &entities.FinancialData{
		Inventory:         10000000,
		InventoryTurnover: 5.0, // Normal turnover
	}

	writedown := parser.calculateDeadInventoryWritedown(data)
	assert.Equal(t, 0.0, writedown) // No writedown for normal inventory

	// Test with zero inventory
	data = &entities.FinancialData{
		Inventory:         0,
		InventoryTurnover: 5.0,
	}

	writedown = parser.calculateDeadInventoryWritedown(data)
	assert.Equal(t, 0.0, writedown) // No writedown for zero inventory

	// Test with zero turnover
	data = &entities.FinancialData{
		Inventory:         10000000,
		InventoryTurnover: 0,
	}

	writedown = parser.calculateDeadInventoryWritedown(data)
	assert.Equal(t, 0.0, writedown) // No writedown calculation without turnover data
}
