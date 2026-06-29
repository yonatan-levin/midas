package valuation

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// Note: Mock implementations are reused from service_test.go to avoid duplication

// Simplified benchmark tests for valuation service performance

func BenchmarkService_CalculateValuation_SingleTicker(b *testing.B) {
	// Create mock dependencies
	financialRepo := &MockFinancialDataRepository{}
	marketRepo := &MockMarketDataRepository{}
	macroRepo := &MockMacroDataRepository{}
	cache := &MockCacheRepository{}
	mockMetrics := &MockMetricsService{}
	mockDataCleaner := &MockDataCleanerService{}

	// Create logger
	logger := zap.NewNop()

	// Create configuration
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:                  1 * time.Hour,
			SlowRequestThreshold:      500 * time.Millisecond,
			DataFetchTimeout:          30 * time.Second,
			EnableConcurrentDataFetch: false, // Sequential for consistency
		},
	}

	// Create service
	service := NewService(financialRepo, marketRepo, macroRepo, cache, mockDataCleaner, nil, mockMetrics, cfg, logger, nil, nil)

	// Setup test data
	ctx := context.Background()
	ticker := "AAPL"

	// Create realistic test data (using the same proven working data from service_test.go)
	historicalData := &entities.HistoricalFinancialData{
		Ticker: ticker,
		Data: map[string]*entities.FinancialData{
			"2023FY": {
				Ticker:                    ticker,
				FilingPeriod:              "2023FY",
				FilingDate:                time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
				AsOf:                      time.Now(),
				OperatingIncome:           123450000000,
				NormalizedOperatingIncome: 120000000000,
				Revenue:                   383930000000,
				InterestExpense:           3490000000,
				TaxRate:                   0.21,
				TotalAssets:               381190000000,
				TangibleAssets:            350000000000,
				InterestBearingDebt:       110000000000,
				SharesOutstanding:         15744231000,
				HasNormalizedData:         true,
			},
			"2022FY": {
				Ticker:                    ticker,
				FilingPeriod:              "2022FY",
				FilingDate:                time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC),
				AsOf:                      time.Now().Add(-365 * 24 * time.Hour),
				OperatingIncome:           119440000000,
				NormalizedOperatingIncome: 116000000000,
				Revenue:                   365817000000,
				InterestExpense:           2930000000,
				TaxRate:                   0.19,
				TotalAssets:               352755000000,
				TangibleAssets:            320000000000,
				InterestBearingDebt:       108000000000,
				SharesOutstanding:         15943425000,
				HasNormalizedData:         true,
			},
			"2021FY": {
				Ticker:                    ticker,
				FilingPeriod:              "2021FY",
				FilingDate:                time.Date(2022, 1, 15, 0, 0, 0, 0, time.UTC),
				AsOf:                      time.Now().Add(-2 * 365 * 24 * time.Hour),
				OperatingIncome:           108949000000,
				NormalizedOperatingIncome: 105000000000,
				Revenue:                   365817000000,
				InterestExpense:           2650000000,
				TaxRate:                   0.18,
				TotalAssets:               323888000000,
				TangibleAssets:            290000000000,
				InterestBearingDebt:       98000000000,
				SharesOutstanding:         16426786000,
				HasNormalizedData:         true,
			},
		},
	}

	marketData := &entities.MarketData{
		Ticker:            ticker,
		AsOf:              time.Now(),
		SharePrice:        180.50,
		MarketCap:         2840000000000,
		SharesOutstanding: 15744231000,
		Beta:              1.25,
		Beta3Y:            1.20,
		AverageVolume:     75000000,
		Source:            "yfinance",
		DataQuality:       "good",
	}

	macroData := &entities.MacroData{
		AsOf:               time.Now(),
		RiskFreeRate:       0.045, // 4.5%
		RiskFreeRate3Month: 0.043, // 4.3%
		MarketRiskPremium:  0.06,  // 6%
		InflationRate:      0.032, // 3.2%
		Source:             "fred",
	}

	cleaningResult := &entities.CleaningResult{
		Success:      true,
		QualityScore: 85.0,
		CleanedData:  historicalData.Data["2023FY"],
		Flags:        []entities.Flag{},
		Adjustments:  []entities.Adjustment{},
	}

	// Setup mock expectations (using assert.AnError for cache miss)
	cache.On("Get", ctx, "valuation:"+ticker, mock.AnythingOfType("*entities.ValuationResult")).Return(assert.AnError).Maybe()
	financialRepo.On("GetHistorical", ctx, ticker, 10).Return(historicalData, nil).Maybe()
	marketRepo.On("GetLatest", ctx, ticker).Return(marketData, nil).Maybe()
	macroRepo.On("GetLatest", ctx).Return(macroData, nil).Maybe()
	mockDataCleaner.On("CleanFinancialData", ctx, mock.AnythingOfType("*entities.FinancialData")).Return(cleaningResult, nil).Maybe()
	cache.On("Set", ctx, "valuation:"+ticker, mock.AnythingOfType("*entities.ValuationResult"), 1*time.Hour).Return(nil).Maybe()
	// Handle both success and error cases for metrics
	mockMetrics.On("RecordValuationRequest", ticker, "single", "success", mock.AnythingOfType("time.Duration")).Return().Maybe()
	mockMetrics.On("RecordValuationRequest", ticker, "single", "error", mock.AnythingOfType("time.Duration")).Return().Maybe()
	mockMetrics.On("RecordValuationError", mock.AnythingOfType("string")).Return().Maybe()
	mockMetrics.On("IncDCFCalculations").Return().Maybe()
	mockMetrics.On("IncWACCCalculations").Return().Maybe()
	mockMetrics.On("SetAverageWACC", mock.AnythingOfType("float64")).Return().Maybe()
	mockMetrics.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return().Maybe()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		result, err := service.CalculateValuation(ctx, ticker, nil)
		if err != nil {
			b.Fatalf("Unexpected error in iteration %d: %v", i, err)
		}
		if result == nil {
			b.Fatalf("Expected result in iteration %d, got nil", i)
		}
	}
}

func BenchmarkService_CalculateValuation_Parallel(b *testing.B) {
	// This benchmark compares sequential vs concurrent data fetching
	b.Run("Sequential", func(b *testing.B) {
		benchmarkValuationService(b, false)
	})

	b.Run("Concurrent", func(b *testing.B) {
		benchmarkValuationService(b, true)
	})
}

func benchmarkValuationService(b *testing.B, enableConcurrent bool) {
	// Create mock dependencies with realistic delays
	financialRepo := &MockFinancialDataRepository{}
	marketRepo := &MockMarketDataRepository{}
	macroRepo := &MockMacroDataRepository{}
	cache := &MockCacheRepository{}
	mockMetrics := &MockMetricsService{}
	mockDataCleaner := &MockDataCleanerService{}

	logger := zap.NewNop()

	// Create configuration
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:                  1 * time.Hour,
			SlowRequestThreshold:      500 * time.Millisecond,
			DataFetchTimeout:          30 * time.Second,
			EnableConcurrentDataFetch: enableConcurrent,
		},
	}

	service := NewService(financialRepo, marketRepo, macroRepo, cache, mockDataCleaner, nil, mockMetrics, cfg, logger, nil, nil)
	ctx := context.Background()
	ticker := "AAPL"

	// Setup test data (using the same proven working data)
	historicalData := &entities.HistoricalFinancialData{
		Ticker: ticker,
		Data: map[string]*entities.FinancialData{
			"2023FY": {
				Ticker:                    ticker,
				FilingPeriod:              "2023FY",
				FilingDate:                time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
				AsOf:                      time.Now(),
				OperatingIncome:           123450000000,
				NormalizedOperatingIncome: 120000000000,
				Revenue:                   383930000000,
				InterestExpense:           3490000000,
				TaxRate:                   0.21,
				TotalAssets:               381190000000,
				TangibleAssets:            350000000000,
				InterestBearingDebt:       110000000000,
				SharesOutstanding:         15744231000,
				HasNormalizedData:         true,
			},
			"2022FY": {
				Ticker:                    ticker,
				FilingPeriod:              "2022FY",
				FilingDate:                time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC),
				AsOf:                      time.Now().Add(-365 * 24 * time.Hour),
				OperatingIncome:           119440000000,
				NormalizedOperatingIncome: 116000000000,
				Revenue:                   365817000000,
				InterestExpense:           2930000000,
				TaxRate:                   0.19,
				TotalAssets:               352755000000,
				TangibleAssets:            320000000000,
				InterestBearingDebt:       108000000000,
				SharesOutstanding:         15943425000,
				HasNormalizedData:         true,
			},
			"2021FY": {
				Ticker:                    ticker,
				FilingPeriod:              "2021FY",
				FilingDate:                time.Date(2022, 1, 15, 0, 0, 0, 0, time.UTC),
				AsOf:                      time.Now().Add(-2 * 365 * 24 * time.Hour),
				OperatingIncome:           108949000000,
				NormalizedOperatingIncome: 105000000000,
				Revenue:                   365817000000,
				InterestExpense:           2650000000,
				TaxRate:                   0.18,
				TotalAssets:               323888000000,
				TangibleAssets:            290000000000,
				InterestBearingDebt:       98000000000,
				SharesOutstanding:         16426786000,
				HasNormalizedData:         true,
			},
		},
	}

	marketData := &entities.MarketData{
		Ticker:            ticker,
		AsOf:              time.Now(),
		SharePrice:        180.50,
		MarketCap:         2840000000000,
		SharesOutstanding: 15744231000,
		Beta:              1.25,
		Beta3Y:            1.20,
		AverageVolume:     75000000,
		Source:            "yfinance",
		DataQuality:       "good",
	}

	macroData := &entities.MacroData{
		AsOf:               time.Now(),
		RiskFreeRate:       0.045, // 4.5%
		RiskFreeRate3Month: 0.043, // 4.3%
		MarketRiskPremium:  0.06,  // 6%
		InflationRate:      0.032, // 3.2%
		Source:             "fred",
	}

	cleaningResult := &entities.CleaningResult{
		Success:      true,
		QualityScore: 85.0,
		CleanedData:  historicalData.Data["2023FY"],
		Flags:        []entities.Flag{},
		Adjustments:  []entities.Adjustment{},
	}

	// Setup mocks with appropriate call counts for benchmark iterations
	cache.On("Get", ctx, "valuation:"+ticker, mock.AnythingOfType("*entities.ValuationResult")).Return(assert.AnError).Maybe()
	financialRepo.On("GetHistorical", ctx, ticker, 10).Return(historicalData, nil).Maybe()
	marketRepo.On("GetLatest", ctx, ticker).Return(marketData, nil).Maybe()
	macroRepo.On("GetLatest", ctx).Return(macroData, nil).Maybe()
	mockDataCleaner.On("CleanFinancialData", ctx, mock.AnythingOfType("*entities.FinancialData")).Return(cleaningResult, nil).Maybe()
	cache.On("Set", ctx, "valuation:"+ticker, mock.AnythingOfType("*entities.ValuationResult"), 1*time.Hour).Return(nil).Maybe()
	mockMetrics.On("RecordValuationRequest", ticker, "single", "success", mock.AnythingOfType("time.Duration")).Return().Maybe()
	mockMetrics.On("IncDCFCalculations").Return().Maybe()
	mockMetrics.On("IncWACCCalculations").Return().Maybe()
	mockMetrics.On("SetAverageWACC", mock.AnythingOfType("float64")).Return().Maybe()
	mockMetrics.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return().Maybe()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		result, err := service.CalculateValuation(ctx, ticker, nil)
		if err != nil {
			b.Fatalf("Unexpected error: %v", err)
		}
		if result == nil {
			b.Fatal("Expected result, got nil")
		}
	}
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
