package valuation

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// Simplified benchmark tests for valuation service performance

func BenchmarkService_CalculateValuation_SingleTicker(b *testing.B) {
	// Skip this benchmark for now as it requires full mock setup
	b.Skip("Requires complete mock repository setup")
}

func BenchmarkService_CalculateValuation_Parallel(b *testing.B) {
	// Skip this benchmark for now as it requires full mock setup
	b.Skip("Requires complete mock repository setup")
}

func BenchmarkFinancialDataCreation(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = createTestFinancialData("BENCHMARK")
	}
}

func BenchmarkFinancialDataProcessing(b *testing.B) {
	testData := createLargeFinancialDataset()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Simulate processing the financial data
		_ = processFinancialData(testData)
	}
}

func BenchmarkMemoryAllocation_MultipleDatasets(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		datasets := make([]*entities.FinancialData, 100)
		for j := 0; j < 100; j++ {
			datasets[j] = createTestFinancialData(fmt.Sprintf("TICKER%03d", j))
		}

		// Force garbage collection periodically
		if i%10 == 0 {
			runtime.GC()
		}
	}
}

func BenchmarkTicker_Generation(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = generateTestTicker(i)
	}
}

// Helper functions for benchmark tests

func processFinancialData(data *entities.FinancialData) float64 {
	// Simulate some financial calculations
	if data.SharesOutstanding == 0 {
		return 0
	}

	tangibleBookValue := data.TangibleAssets / data.SharesOutstanding
	return tangibleBookValue
}

func generateTestTicker(index int) string {
	// Generate realistic-looking ticker symbols
	tickers := []string{
		"AAPL", "GOOGL", "MSFT", "AMZN", "TSLA", "META", "NVDA", "AMD", "INTC", "ORCL",
		"CRM", "ADBE", "NOW", "SNOW", "PLTR", "UBER", "LYFT", "SPOT", "NFLX", "DIS",
		"WMT", "TGT", "COST", "HD", "LOW", "MCD", "SBUX", "KO", "PEP", "NKE",
	}

	if index < len(tickers) {
		return tickers[index]
	}

	// Generate synthetic tickers for larger datasets
	return fmt.Sprintf("TEST%03d", index)
}

func createLargeFinancialDataset() *entities.FinancialData {
	return &entities.FinancialData{
		Ticker:                    "BENCHMARK",
		Revenue:                   500000000000,  // $500B
		OperatingIncome:           50000000000,   // $50B
		NormalizedOperatingIncome: 52000000000,   // $52B (after adjustments)
		TotalAssets:               1000000000000, // $1T
		TangibleAssets:            800000000000,  // $800B
		TotalDebt:                 200000000000,  // $200B
		InterestBearingDebt:       180000000000,  // $180B
		SharesOutstanding:         1000000000,    // 1B shares
		DilutedSharesOutstanding:  1050000000,    // 1.05B shares
		InterestExpense:           5000000000,    // $5B
		TaxRate:                   0.21,          // 21%
		AsOf:                      time.Now(),
		FilingDate:                time.Now().AddDate(0, -1, 0), // 1 month ago
		Period:                    "2024Q3",
		HasNormalizedData:         true,

		// Add various adjustment data
		RestructuringCharges:   1000000000, // $1B
		AssetSaleGains:         500000000,  // $500M
		StockBasedCompensation: 8000000000, // $8B

		// Asset quality adjustments
		Goodwill:         150000000000, // $150B
		OtherIntangibles: 50000000000,  // $50B
		Inventory:        25000000000,  // $25B

		// Complete enough data for all calculations
		CostOfGoodsSold:        300000000000, // $300B
		ResearchAndDevelopment: 25000000000,  // $25B
		InventoryTurnover:      12.0,         // 12x per year
		EffectiveTaxRate:       0.19,         // 19%
	}
}

func createTestFinancialData(ticker string) *entities.FinancialData {
	return &entities.FinancialData{
		Ticker:                    ticker,
		Revenue:                   100000000000, // $100B
		OperatingIncome:           20000000000,  // $20B
		NormalizedOperatingIncome: 21000000000,  // $21B
		TotalAssets:               200000000000, // $200B
		TangibleAssets:            150000000000, // $150B
		TotalDebt:                 50000000000,  // $50B
		InterestBearingDebt:       45000000000,  // $45B
		SharesOutstanding:         1000000000,   // 1B shares
		DilutedSharesOutstanding:  1020000000,   // 1.02B shares
		InterestExpense:           2000000000,   // $2B
		TaxRate:                   0.21,         // 21%
		AsOf:                      time.Now(),
		Period:                    "2024Q3",
		HasNormalizedData:         true,
	}
}
 