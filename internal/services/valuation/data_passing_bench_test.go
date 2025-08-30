package valuation

import (
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// BenchmarkStructPassing_Current measures current memory allocation patterns
// This serves as a baseline before optimizations
func BenchmarkStructPassing_Current(b *testing.B) {
	// Create a sample FinancialData struct (large struct)
	financialData := createLargeFinancialData()

	b.Run("FinancialData_ByValue", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			// Simulate passing large struct by value (current pattern in some places)
			result := processFinancialDataByValue(financialData)
			_ = result // Prevent optimization
		}
	})

	b.Run("FinancialData_ByPointer", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			// Compare with pointer passing
			result := processFinancialDataByPointer(&financialData)
			_ = result // Prevent optimization
		}
	})
}

// BenchmarkValuationResultCreation measures result creation patterns
func BenchmarkValuationResultCreation(b *testing.B) {
	b.Run("ValuationResult_ByValue", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			// Current pattern: create and return by value
			result := createValuationResultByValue()
			_ = result // Prevent optimization
		}
	})

	b.Run("ValuationResult_ByPointer", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			// Optimized pattern: create and return pointer
			result := createValuationResultByPointer()
			_ = result // Prevent optimization
		}
	})
}

// BenchmarkSliceOfStructs measures collection patterns
func BenchmarkSliceOfStructs(b *testing.B) {
	const numItems = 100

	b.Run("Slice_OfValues", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			// Current pattern: slice of values
			slice := make([]entities.FinancialData, 0, numItems)
			for j := 0; j < numItems; j++ {
				data := createLargeFinancialData()
				slice = append(slice, data) // Copies the entire struct
			}
			_ = slice // Prevent optimization
		}
	})

	b.Run("Slice_OfPointers", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			// Optimized pattern: slice of pointers
			slice := make([]*entities.FinancialData, 0, numItems)
			for j := 0; j < numItems; j++ {
				data := createLargeFinancialData()
				slice = append(slice, &data) // Only copies pointer
			}
			_ = slice // Prevent optimization
		}
	})
}

// Helper functions to simulate processing patterns

func processFinancialDataByValue(data entities.FinancialData) float64 {
	// Simulate some processing that doesn't modify the data
	return data.OperatingIncome + data.Revenue + data.TotalAssets
}

func processFinancialDataByPointer(data *entities.FinancialData) float64 {
	// Simulate same processing with pointer
	return data.OperatingIncome + data.Revenue + data.TotalAssets
}

func createValuationResultByValue() entities.ValuationResult {
	return entities.ValuationResult{
		Ticker:                "AAPL",
		AsOf:                  time.Now(),
		TangibleValuePerShare: 150.0,
		DCFValuePerShare:      175.0,
		WACC:                  0.08,
		CostOfEquity:          0.09,
		CostOfDebt:            0.04,
		WeightOfEquity:        0.75,
		WeightOfDebt:          0.25,
		GrowthRate:            0.05,
		TerminalGrowthRate:    0.03,
		ProjectionYears:       5,
		TerminalValue:         2000.0,
		FinancialDataAsOf:     time.Now().Add(-30 * 24 * time.Hour),
		MarketDataAsOf:        time.Now(),
		FilingPeriod:          "2023FY",
		CalculationMethod:     "standard_dcf",
		DataQualityScore:      0.85,
		Warnings:              []string{"Beta estimation based on limited data"},
		CalculatedAt:          time.Now(),
		DataQualityGrade:      "A",
		MarketRiskPremium:     0.06,
		EnterpriseValue:       2500000000000,
		EquityValue:           2400000000000,
		FinancialDataPeriod:   "2023FY",
	}
}

func createValuationResultByPointer() *entities.ValuationResult {
	result := &entities.ValuationResult{
		Ticker:                "AAPL",
		AsOf:                  time.Now(),
		TangibleValuePerShare: 150.0,
		DCFValuePerShare:      175.0,
		WACC:                  0.08,
		CostOfEquity:          0.09,
		CostOfDebt:            0.04,
		WeightOfEquity:        0.75,
		WeightOfDebt:          0.25,
		GrowthRate:            0.05,
		TerminalGrowthRate:    0.03,
		ProjectionYears:       5,
		TerminalValue:         2000.0,
		FinancialDataAsOf:     time.Now().Add(-30 * 24 * time.Hour),
		MarketDataAsOf:        time.Now(),
		FilingPeriod:          "2023FY",
		CalculationMethod:     "standard_dcf",
		DataQualityScore:      0.85,
		Warnings:              []string{"Beta estimation based on limited data"},
		CalculatedAt:          time.Now(),
		DataQualityGrade:      "A",
		MarketRiskPremium:     0.06,
		EnterpriseValue:       2500000000000,
		EquityValue:           2400000000000,
		FinancialDataPeriod:   "2023FY",
	}
	return result
}

func createLargeFinancialData() entities.FinancialData {
	return entities.FinancialData{
		Ticker:                     "AAPL",
		IndustryCode:               "334413",
		CIK:                        "0000320193",
		AsOf:                       time.Now(),
		OperatingIncome:            123500000000,
		NormalizedOperatingIncome:  120000000000,
		Revenue:                    383932000000,
		ResearchAndDevelopment:     29915000000,
		InterestExpense:            3920000000,
		TaxRate:                    0.20,
		RestructuringCharges:       500000000,
		AssetSaleGains:             200000000,
		LitigationSettlements:      100000000,
		StockBasedCompensation:     15000000000,
		DerivativeGainsLosses:      50000000,
		CapitalizedInterest:        25000000,
		WorkingCapitalAdjustment:   1000000000,
		TotalAssets:                381190000000,
		TangibleAssets:             350000000000,
		Goodwill:                   25000000000,
		OtherIntangibles:           6190000000,
		TotalDebt:                  110000000000,
		InterestBearingDebt:        110000000000,
		IntangibleAssets:           31190000000,
		IndefiniteLivedIntangibles: 5000000000,
		DeferredTaxAssets:          15000000000,
		ValuationAllowance:         500000000,
		EffectiveTaxRate:           0.20,
		CostOfGoodsSold:            210000000000,
		Inventory:                  6331000000,
		InventoryTurnover:          33.2,
		SharesOutstanding:          15744231000,
		DilutedSharesOutstanding:   15812547000,
		FilingPeriod:               "2023FY",
		FilingDate:                 time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		HasNormalizedData:          true,
	}
}
