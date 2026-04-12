package valuation

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestService_ConcurrentDataFetch tests the concurrent data fetching functionality
func TestService_ConcurrentDataFetch(t *testing.T) {
	// Create mock repositories
	financialRepo := &MockFinancialDataRepository{}
	marketRepo := &MockMarketDataRepository{}
	macroRepo := &MockMacroDataRepository{}
	cache := &MockCacheRepository{}
	mockMetrics := &MockMetricsService{}
	mockDataCleaner := &MockDataCleanerService{}

	// Create logger
	logger := zap.NewNop()

	// Create configuration with concurrent fetching ENABLED
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:                  1 * time.Hour,
			SlowRequestThreshold:      500 * time.Millisecond,
			DataFetchTimeout:          30 * time.Second,
			EnableConcurrentDataFetch: true, // Enable concurrent fetching
		},
	}

	// Create service
	service := NewService(financialRepo, marketRepo, macroRepo, cache, mockDataCleaner, nil, mockMetrics, cfg, logger)

	ctx := context.Background()

	// Create test data - based on existing test setup with 3 years of data
	historicalData := &entities.HistoricalFinancialData{
		Ticker: "AAPL",
		Data: map[string]*entities.FinancialData{
			"2023FY": {
				Ticker:                    "AAPL",
				FilingPeriod:              "2023FY",
				FilingDate:                time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
				AsOf:                      time.Now(),
				OperatingIncome:           123500000000,
				NormalizedOperatingIncome: 120000000000,
				Revenue:                   383932000000,
				InterestExpense:           3920000000,
				TaxRate:                   0.20,
				TotalAssets:               381190000000,
				TangibleAssets:            350000000000,
				InterestBearingDebt:       110000000000,
				SharesOutstanding:         15744231000,
				HasNormalizedData:         true,
			},
			"2022FY": {
				Ticker:                    "AAPL",
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
				Ticker:                    "AAPL",
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
		Ticker:            "AAPL",
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

	// Mock cache miss - using errors.New instead of entities.ErrCacheNotFound
	cache.On("Get", ctx, "valuation:v4:AAPL", mock.AnythingOfType("*entities.ValuationResult")).Return(assert.AnError)

	// Setup DataCleaner mock - using correct field names
	cleaningResult := &entities.CleaningResult{
		Success:      true,
		QualityScore: 85.0,
		CleanedData:  historicalData.Data["2023FY"], // Use the same data
		Flags:        []entities.Flag{},
		Adjustments:  []entities.Adjustment{},
	}
	mockDataCleaner.On("CleanFinancialData", ctx, mock.AnythingOfType("*entities.FinancialData")).Return(cleaningResult, nil)

	// Then data retrieval - these will be called concurrently
	financialRepo.On("GetHistorical", ctx, "AAPL", 10).Return(historicalData, nil)
	marketRepo.On("GetLatest", ctx, "AAPL").Return(marketData, nil)
	macroRepo.On("GetLatest", ctx).Return(macroData, nil)

	// Cache storage
	cache.On("Set", ctx, "valuation:v4:AAPL", mock.AnythingOfType("*entities.ValuationResult"), 1*time.Hour).Return(nil)

	// Setup metrics service expectations - only success case for this test
	mockMetrics.On("RecordValuationRequest", "AAPL", "single", "success", mock.AnythingOfType("time.Duration")).Return()
	mockMetrics.On("IncWACCCalculations").Return()
	mockMetrics.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	mockMetrics.On("IncDCFCalculations").Return()
	mockMetrics.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()

	// Measure execution time
	start := time.Now()
	result, err := service.CalculateValuation(ctx, "AAPL", nil)
	executionTime := time.Since(start)

	// Verify results
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "AAPL", result.Ticker)
	assert.Greater(t, result.DCFValuePerShare, 0.0)
	assert.Greater(t, result.TangibleValuePerShare, 0.0)
	assert.Greater(t, result.WACC, 0.0)
	assert.NotEmpty(t, result.FinancialDataPeriod)

	// Verify DataCleaner integration
	assert.Equal(t, 85.0, result.DataQualityScore)
	assert.NotNil(t, result.CleaningFlags)
	assert.NotNil(t, result.CleaningAdjustments)

	// Log execution time for manual verification of performance
	t.Logf("Concurrent execution time: %v", executionTime)

	// Verify all mock expectations
	financialRepo.AssertExpectations(t)
	marketRepo.AssertExpectations(t)
	macroRepo.AssertExpectations(t)
	cache.AssertExpectations(t)
	mockDataCleaner.AssertExpectations(t)
	mockMetrics.AssertExpectations(t)
}

// BenchmarkService_ConcurrentVsSequential compares performance of concurrent vs sequential approaches
func BenchmarkService_ConcurrentVsSequential(b *testing.B) {
	b.Run("Sequential", func(b *testing.B) {
		benchmarkServiceWithConfig(b, false)
	})

	b.Run("Concurrent", func(b *testing.B) {
		benchmarkServiceWithConfig(b, true)
	})
}

func benchmarkServiceWithConfig(b *testing.B, enableConcurrent bool) {
	// Create mock repositories with realistic delays
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

	service := NewService(financialRepo, marketRepo, macroRepo, cache, mockDataCleaner, nil, mockMetrics, cfg, logger)
	ctx := context.Background()

	// Setup test data - same as test above with 3 years
	historicalData := &entities.HistoricalFinancialData{
		Ticker: "AAPL",
		Data: map[string]*entities.FinancialData{
			"2023FY": {
				Ticker:                    "AAPL",
				FilingPeriod:              "2023FY",
				FilingDate:                time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
				AsOf:                      time.Now(),
				OperatingIncome:           123500000000,
				NormalizedOperatingIncome: 120000000000,
				Revenue:                   383932000000,
				InterestExpense:           3920000000,
				TaxRate:                   0.20,
				TotalAssets:               381190000000,
				TangibleAssets:            350000000000,
				InterestBearingDebt:       110000000000,
				SharesOutstanding:         15744231000,
				HasNormalizedData:         true,
			},
			"2022FY": {
				Ticker:                    "AAPL",
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
				Ticker:                    "AAPL",
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
		Ticker:            "AAPL",
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

	// Setup mocks for each iteration
	cache.On("Get", ctx, "valuation:v4:AAPL", mock.AnythingOfType("*entities.ValuationResult")).Return(assert.AnError)
	financialRepo.On("GetHistorical", ctx, "AAPL", 10).Return(historicalData, nil)
	marketRepo.On("GetLatest", ctx, "AAPL").Return(marketData, nil)
	macroRepo.On("GetLatest", ctx).Return(macroData, nil)
	cache.On("Set", ctx, "valuation:v4:AAPL", mock.AnythingOfType("*entities.ValuationResult"), 1*time.Hour).Return(nil)

	cleaningResult := &entities.CleaningResult{
		Success:      true,
		QualityScore: 85.0,
		CleanedData:  historicalData.Data["2023FY"],
		Flags:        []entities.Flag{},
		Adjustments:  []entities.Adjustment{},
	}
	mockDataCleaner.On("CleanFinancialData", ctx, mock.AnythingOfType("*entities.FinancialData")).Return(cleaningResult, nil)

	mockMetrics.On("RecordValuationRequest", "AAPL", "single", "success", mock.AnythingOfType("time.Duration")).Return()
	mockMetrics.On("IncWACCCalculations").Return()
	mockMetrics.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	mockMetrics.On("IncDCFCalculations").Return()
	mockMetrics.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := service.CalculateValuation(ctx, "AAPL", nil)
		if err != nil {
			b.Fatalf("Unexpected error: %v", err)
		}
	}
}
