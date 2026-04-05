package valuation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/metrics"
)

// MockMetricsService for testing
type MockMetricsService struct {
	mock.Mock
}

func (m *MockMetricsService) RecordHTTPRequest(method, endpoint string, statusCode int, duration time.Duration, responseSize int) {
	m.Called(method, endpoint, statusCode, duration, responseSize)
}

func (m *MockMetricsService) IncHTTPRequestsInFlight() {
	m.Called()
}

func (m *MockMetricsService) DecHTTPRequestsInFlight() {
	m.Called()
}

func (m *MockMetricsService) RecordValuationRequest(ticker, requestType, status string, duration time.Duration) {
	m.Called(ticker, requestType, status, duration)
}

func (m *MockMetricsService) RecordValuationError(ticker, errorType string) {
	m.Called(ticker, errorType)
}

func (m *MockMetricsService) IncDCFCalculations() {
	m.Called()
}

func (m *MockMetricsService) IncWACCCalculations() {
	m.Called()
}

func (m *MockMetricsService) RecordSECAPIRequest(endpoint, status string) {
	m.Called(endpoint, status)
}

func (m *MockMetricsService) RecordMarketAPIRequest(provider, status string) {
	m.Called(provider, status)
}

func (m *MockMetricsService) RecordMacroAPIRequest(provider, status string) {
	m.Called(provider, status)
}

func (m *MockMetricsService) RecordDataFetch(source, ticker string, duration time.Duration) {
	m.Called(source, ticker, duration)
}

func (m *MockMetricsService) RecordCacheRequest(cacheType, operation, result string) {
	m.Called(cacheType, operation, result)
}

func (m *MockMetricsService) SetCacheHitRatio(cacheType string, ratio float64) {
	m.Called(cacheType, ratio)
}

func (m *MockMetricsService) SetAverageWACC(wacc float64) {
	m.Called(wacc)
}

func (m *MockMetricsService) SetAverageGrowthRate(rate float64) {
	m.Called(rate)
}

func (m *MockMetricsService) GetTotalRequests() int64 {
	args := m.Called()
	return args.Get(0).(int64)
}

func (m *MockMetricsService) GetActiveConnections() int {
	args := m.Called()
	return args.Get(0).(int)
}

func (m *MockMetricsService) GetAverageResponseTime() float64 {
	args := m.Called()
	return args.Get(0).(float64)
}

func (m *MockMetricsService) GetErrorRate() float64 {
	args := m.Called()
	return args.Get(0).(float64)
}

func (m *MockMetricsService) GetCacheHitRate() float64 {
	args := m.Called()
	return args.Get(0).(float64)
}

func (m *MockMetricsService) GetTotalValuations() int64 {
	args := m.Called()
	return args.Get(0).(int64)
}

func (m *MockMetricsService) GetSuccessfulValuations() int64 {
	args := m.Called()
	return args.Get(0).(int64)
}

func (m *MockMetricsService) GetFailedValuations() int64 {
	args := m.Called()
	return args.Get(0).(int64)
}

func (m *MockMetricsService) GetAverageWACC() float64 {
	args := m.Called()
	return args.Get(0).(float64)
}

func (m *MockMetricsService) GetAverageGrowthRate() float64 {
	args := m.Called()
	return args.Get(0).(float64)
}

func (m *MockMetricsService) GetUniqueTickersServed() int64 {
	args := m.Called()
	return args.Get(0).(int64)
}

func (m *MockMetricsService) HealthCheck() error {
	args := m.Called()
	return args.Error(0)
}

// Mock repositories for testing
type MockFinancialDataRepository struct {
	mock.Mock
}

func (m *MockFinancialDataRepository) Store(ctx context.Context, data *entities.FinancialData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

func (m *MockFinancialDataRepository) GetLatest(ctx context.Context, ticker string) (*entities.FinancialData, error) {
	args := m.Called(ctx, ticker)
	return args.Get(0).(*entities.FinancialData), args.Error(1)
}

func (m *MockFinancialDataRepository) GetHistorical(ctx context.Context, ticker string, limit int) (*entities.HistoricalFinancialData, error) {
	args := m.Called(ctx, ticker, limit)
	return args.Get(0).(*entities.HistoricalFinancialData), args.Error(1)
}

func (m *MockFinancialDataRepository) GetByPeriod(ctx context.Context, ticker, period string) (*entities.FinancialData, error) {
	args := m.Called(ctx, ticker, period)
	return args.Get(0).(*entities.FinancialData), args.Error(1)
}

func (m *MockFinancialDataRepository) GetLastUpdated(ctx context.Context, ticker string) (time.Time, error) {
	args := m.Called(ctx, ticker)
	return args.Get(0).(time.Time), args.Error(1)
}

func (m *MockFinancialDataRepository) StoreHistorical(ctx context.Context, data *entities.HistoricalFinancialData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

type MockMarketDataRepository struct {
	mock.Mock
}

func (m *MockMarketDataRepository) Store(ctx context.Context, data *entities.MarketData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

func (m *MockMarketDataRepository) GetLatest(ctx context.Context, ticker string) (*entities.MarketData, error) {
	args := m.Called(ctx, ticker)
	return args.Get(0).(*entities.MarketData), args.Error(1)
}

func (m *MockMarketDataRepository) GetBatch(ctx context.Context, tickers []string) (map[string]*entities.MarketData, error) {
	args := m.Called(ctx, tickers)
	return args.Get(0).(map[string]*entities.MarketData), args.Error(1)
}

func (m *MockMarketDataRepository) IsStale(ctx context.Context, ticker string, maxAge time.Duration) (bool, error) {
	args := m.Called(ctx, ticker, maxAge)
	return args.Bool(0), args.Error(1)
}

func (m *MockMarketDataRepository) GetLastUpdated(ctx context.Context, ticker string) (time.Time, error) {
	args := m.Called(ctx, ticker)
	return args.Get(0).(time.Time), args.Error(1)
}

type MockMacroDataRepository struct {
	mock.Mock
}

func (m *MockMacroDataRepository) Store(ctx context.Context, data *entities.MacroData) error {
	args := m.Called(ctx, data)
	return args.Error(0)
}

func (m *MockMacroDataRepository) GetLatest(ctx context.Context) (*entities.MacroData, error) {
	args := m.Called(ctx)
	return args.Get(0).(*entities.MacroData), args.Error(1)
}

func (m *MockMacroDataRepository) GetByDate(ctx context.Context, date time.Time) (*entities.MacroData, error) {
	args := m.Called(ctx, date)
	return args.Get(0).(*entities.MacroData), args.Error(1)
}

func (m *MockMacroDataRepository) GetLastUpdated(ctx context.Context) (time.Time, error) {
	args := m.Called(ctx)
	return args.Get(0).(time.Time), args.Error(1)
}

func (m *MockMacroDataRepository) IsStale(ctx context.Context, maxAge time.Duration) (bool, error) {
	args := m.Called(ctx, maxAge)
	return args.Bool(0), args.Error(1)
}

type MockCacheRepository struct {
	mock.Mock
}

func (m *MockCacheRepository) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	args := m.Called(ctx, key, value, ttl)
	return args.Error(0)
}

func (m *MockCacheRepository) Get(ctx context.Context, key string, dest interface{}) error {
	args := m.Called(ctx, key, dest)
	return args.Error(0)
}

func (m *MockCacheRepository) Delete(ctx context.Context, key string) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

func (m *MockCacheRepository) Exists(ctx context.Context, key string) (bool, error) {
	args := m.Called(ctx, key)
	return args.Bool(0), args.Error(1)
}

func (m *MockCacheRepository) SetNX(ctx context.Context, key string, value interface{}, ttl time.Duration) (bool, error) {
	args := m.Called(ctx, key, value, ttl)
	return args.Bool(0), args.Error(1)
}

func (m *MockCacheRepository) GetKeys(ctx context.Context, pattern string) ([]string, error) {
	args := m.Called(ctx, pattern)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockCacheRepository) DeletePattern(ctx context.Context, pattern string) error {
	args := m.Called(ctx, pattern)
	return args.Error(0)
}

// MockDataCleanerService for testing
type MockDataCleanerService struct {
	mock.Mock
}

func (m *MockDataCleanerService) CleanFinancialData(ctx context.Context, data *entities.FinancialData) (*entities.CleaningResult, error) {
	args := m.Called(ctx, data)
	return args.Get(0).(*entities.CleaningResult), args.Error(1)
}

func (m *MockDataCleanerService) GetIndustryRules(industryCode string) ([]entities.CleaningRule, error) {
	args := m.Called(industryCode)
	return args.Get(0).([]entities.CleaningRule), args.Error(1)
}

func (m *MockDataCleanerService) GetQualityScore(ctx context.Context, data *entities.FinancialData) (float64, error) {
	args := m.Called(ctx, data)
	return args.Get(0).(float64), args.Error(1)
}

func (m *MockDataCleanerService) ValidateData(data *entities.FinancialData) error {
	args := m.Called(data)
	return args.Error(0)
}

func createTestService() (*Service, *MockFinancialDataRepository, *MockMarketDataRepository, *MockMacroDataRepository, *MockCacheRepository, *MockDataCleanerService) {
	financialRepo := &MockFinancialDataRepository{}
	marketRepo := &MockMarketDataRepository{}
	macroRepo := &MockMacroDataRepository{}
	cache := &MockCacheRepository{}
	dataCleaner := &MockDataCleanerService{}
	logger := zap.NewNop()
	metricsService := &MockMetricsService{}

	// Create test config
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:             1 * time.Hour,
			SlowRequestThreshold: 500 * time.Millisecond,
			DataFetchTimeout:     30 * time.Second,
		},
	}

	// Use nil for DataFetcher in unit tests since we mock repository responses
	service := NewService(financialRepo, marketRepo, macroRepo, cache, dataCleaner, nil, metricsService, cfg, logger)

	return service, financialRepo, marketRepo, macroRepo, cache, dataCleaner
}

func createTestData() (*entities.HistoricalFinancialData, *entities.MarketData, *entities.MacroData) {
	// Create test financial data
	historicalData := &entities.HistoricalFinancialData{
		Ticker: "AAPL",
		Data: map[string]*entities.FinancialData{
			"2023FY": {
				Ticker:                    "AAPL",
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

	// Create test market data
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

	// Create test macro data
	macroData := &entities.MacroData{
		AsOf:               time.Now(),
		RiskFreeRate:       0.045, // 4.5%
		RiskFreeRate3Month: 0.043, // 4.3%
		MarketRiskPremium:  0.06,  // 6%
		InflationRate:      0.032, // 3.2%
		Source:             "fred",
	}

	return historicalData, marketData, macroData
}

func TestService_CalculateValuation(t *testing.T) {
	service, financialRepo, marketRepo, macroRepo, cache, _ := createTestService()
	ctx := context.Background()

	historicalData, marketData, macroData := createTestData()

	t.Run("successful valuation calculation", func(t *testing.T) {
		// Setup DataCleaner mock
		dataCleaner := &MockDataCleanerService{}
		cleaningResult := &entities.CleaningResult{
			Success:      true,
			QualityScore: 85.0,
			CleanedData:  historicalData.Data["2023FY"], // Use the same data
			Flags:        []entities.Flag{},
			Adjustments:  []entities.Adjustment{},
		}
		dataCleaner.On("CleanFinancialData", ctx, mock.AnythingOfType("*entities.FinancialData")).Return(cleaningResult, nil)

		// Create service with mocked DataCleaner
		logger := zap.NewNop()
		mockMetrics := &MockMetricsService{}
		cfg := &config.Config{
			Valuation: config.ValuationConfig{
				CacheTTL:             1 * time.Hour,
				SlowRequestThreshold: 500 * time.Millisecond,
				DataFetchTimeout:     30 * time.Second,
			},
		}
		service := NewService(financialRepo, marketRepo, macroRepo, cache, dataCleaner, nil, mockMetrics, cfg, logger)

		// Setup expectations - cache miss first
		cache.On("Get", ctx, "valuation:AAPL", mock.AnythingOfType("*entities.ValuationResult")).Return(errors.New("cache miss"))

		// Then data retrieval
		financialRepo.On("GetHistorical", ctx, "AAPL", 10).Return(historicalData, nil)
		marketRepo.On("GetLatest", ctx, "AAPL").Return(marketData, nil)
		macroRepo.On("GetLatest", ctx).Return(macroData, nil)

		// Cache storage
		cache.On("Set", ctx, "valuation:AAPL", mock.AnythingOfType("*entities.ValuationResult"), 1*time.Hour).Return(nil)

		// Setup metrics service expectations
		mockMetrics.On("RecordValuationRequest", "AAPL", "single", "success", mock.AnythingOfType("time.Duration")).Return()
		mockMetrics.On("IncWACCCalculations").Return()
		mockMetrics.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
		mockMetrics.On("IncDCFCalculations").Return()
		mockMetrics.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()

		result, err := service.CalculateValuation(ctx, "AAPL", nil)

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

		// Verify all mock expectations
		financialRepo.AssertExpectations(t)
		marketRepo.AssertExpectations(t)
		macroRepo.AssertExpectations(t)
		cache.AssertExpectations(t)
		dataCleaner.AssertExpectations(t)
	})

	t.Run("returns cached result if available", func(t *testing.T) {
		// Create fresh mocks for this test case
		freshCache := &MockCacheRepository{}
		freshMetrics := &MockMetricsService{}
		freshDataCleaner := &MockDataCleanerService{}
		logger := zap.NewNop()
		cfg := &config.Config{
			Valuation: config.ValuationConfig{
				CacheTTL:             1 * time.Hour,
				SlowRequestThreshold: 500 * time.Millisecond,
				DataFetchTimeout:     30 * time.Second,
			},
		}

		// Create fresh service with new mocks
		freshService := NewService(financialRepo, marketRepo, macroRepo, freshCache, freshDataCleaner, nil, freshMetrics, cfg, logger)

		cachedResult := &entities.ValuationResult{
			Ticker:                "AAPL",
			CalculatedAt:          time.Now().Add(-30 * time.Minute),
			TangibleValuePerShare: 150.0,
			DCFValuePerShare:      175.0,
			WACC:                  0.08,
			GrowthRate:            0.05,
		}

		// Setup cache hit expectation
		freshCache.On("Get", ctx, "valuation:AAPL", mock.AnythingOfType("*entities.ValuationResult")).Run(func(args mock.Arguments) {
			dest := args.Get(2).(*entities.ValuationResult)
			*dest = *cachedResult
		}).Return(nil)

		// Setup metrics expectation for cache hit
		freshMetrics.On("RecordValuationRequest", "AAPL", "single", "cache_hit", mock.AnythingOfType("time.Duration")).Return()

		result, err := freshService.CalculateValuation(ctx, "AAPL", nil)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, cachedResult.Ticker, result.Ticker)
		assert.Equal(t, cachedResult.DCFValuePerShare, result.DCFValuePerShare)

		freshCache.AssertExpectations(t)
		freshMetrics.AssertExpectations(t)
		// Should not call other repos when cache hit
		financialRepo.AssertNotCalled(t, "GetHistorical")
		marketRepo.AssertNotCalled(t, "GetLatest")
		macroRepo.AssertNotCalled(t, "GetLatest")
	})

	t.Run("handles missing financial data", func(t *testing.T) {
		// Create fresh mocks for this test
		freshFinancialRepo := &MockFinancialDataRepository{}
		freshCache := &MockCacheRepository{}
		freshDataCleaner := &MockDataCleanerService{}

		// Create service with nil DataFetcher - this tests the error path when DataFetcher is not available
		logger := zap.NewNop()
		mockMetrics := &MockMetricsService{}
		cfg := &config.Config{
			Valuation: config.ValuationConfig{
				CacheTTL:             1 * time.Hour,
				SlowRequestThreshold: 500 * time.Millisecond,
				DataFetchTimeout:     30 * time.Second,
			},
		}
		freshService := NewService(freshFinancialRepo, marketRepo, macroRepo, freshCache, freshDataCleaner, nil, mockMetrics, cfg, logger)

		// Setup expectations - cache miss, no data in repo
		freshCache.On("Get", ctx, "valuation:AAPL", mock.AnythingOfType("*entities.ValuationResult")).Return(errors.New("cache miss"))
		freshFinancialRepo.On("GetHistorical", ctx, "AAPL", 10).Return((*entities.HistoricalFinancialData)(nil), errors.New("no data found"))

		result, err := freshService.CalculateValuation(ctx, "AAPL", nil)

		assert.Error(t, err)
		assert.Nil(t, result)
		// The test should fail when DataFetcher is nil, which is expected behavior
		// This tests that the service properly handles the case when DataFetcher is not configured
		assert.Contains(t, err.Error(), "data fetcher not configured")

		freshFinancialRepo.AssertExpectations(t)
		freshCache.AssertExpectations(t)
	})

	t.Run("handles missing market data", func(t *testing.T) {
		// Reset mocks
		financialRepo.ExpectedCalls = nil
		marketRepo.ExpectedCalls = nil
		macroRepo.ExpectedCalls = nil
		cache.ExpectedCalls = nil

		cache.On("Get", ctx, "valuation:AAPL", mock.AnythingOfType("*entities.ValuationResult")).Return(errors.New("cache miss"))
		financialRepo.On("GetHistorical", ctx, "AAPL", 10).Return(historicalData, nil)
		marketRepo.On("GetLatest", ctx, "AAPL").Return((*entities.MarketData)(nil), errors.New("no market data"))

		result, err := service.CalculateValuation(ctx, "AAPL", nil)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to fetch market data")

		financialRepo.AssertExpectations(t)
		marketRepo.AssertExpectations(t)
		cache.AssertExpectations(t)
	})

	t.Run("handles missing macro data", func(t *testing.T) {
		// Reset mocks
		financialRepo.ExpectedCalls = nil
		marketRepo.ExpectedCalls = nil
		macroRepo.ExpectedCalls = nil
		cache.ExpectedCalls = nil

		cache.On("Get", ctx, "valuation:AAPL", mock.AnythingOfType("*entities.ValuationResult")).Return(errors.New("cache miss"))
		financialRepo.On("GetHistorical", ctx, "AAPL", 10).Return(historicalData, nil)
		marketRepo.On("GetLatest", ctx, "AAPL").Return(marketData, nil)
		macroRepo.On("GetLatest", ctx).Return((*entities.MacroData)(nil), errors.New("no macro data"))

		result, err := service.CalculateValuation(ctx, "AAPL", nil)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to fetch macro data")

		financialRepo.AssertExpectations(t)
		marketRepo.AssertExpectations(t)
		macroRepo.AssertExpectations(t)
		cache.AssertExpectations(t)
	})
}

// TestService_CalculateValuation_NilDataCleaner verifies that the valuation pipeline
// succeeds when the DataCleaner service is not configured (nil). The code should
// skip cleaning and use original data.
func TestService_CalculateValuation_NilDataCleaner(t *testing.T) {
	ctx := context.Background()
	historicalData, marketData, macroData := createTestData()

	// Create fresh mocks for isolation
	freshFinancialRepo := &MockFinancialDataRepository{}
	freshMarketRepo := &MockMarketDataRepository{}
	freshMacroRepo := &MockMacroDataRepository{}
	freshCache := &MockCacheRepository{}
	freshMetrics := &MockMetricsService{}
	logger := zap.NewNop()
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:             1 * time.Hour,
			SlowRequestThreshold: 500 * time.Millisecond,
			DataFetchTimeout:     30 * time.Second,
		},
	}

	// Create service with nil dataCleaner — this is the path we want to cover
	service := NewService(freshFinancialRepo, freshMarketRepo, freshMacroRepo, freshCache, nil, nil, freshMetrics, cfg, logger)

	// Setup expectations: cache miss, then successful data retrieval
	freshCache.On("Get", ctx, "valuation:AAPL", mock.AnythingOfType("*entities.ValuationResult")).Return(errors.New("cache miss"))
	freshFinancialRepo.On("GetHistorical", ctx, "AAPL", 10).Return(historicalData, nil)
	freshMarketRepo.On("GetLatest", ctx, "AAPL").Return(marketData, nil)
	freshMacroRepo.On("GetLatest", ctx).Return(macroData, nil)
	freshCache.On("Set", ctx, "valuation:AAPL", mock.AnythingOfType("*entities.ValuationResult"), 1*time.Hour).Return(nil)

	// Setup metrics expectations
	freshMetrics.On("RecordValuationRequest", "AAPL", "single", "success", mock.AnythingOfType("time.Duration")).Return()
	freshMetrics.On("IncWACCCalculations").Return()
	freshMetrics.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	freshMetrics.On("IncDCFCalculations").Return()
	freshMetrics.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()

	result, err := service.CalculateValuation(ctx, "AAPL", nil)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "AAPL", result.Ticker)
	assert.Greater(t, result.DCFValuePerShare, 0.0)
	// DataQualityScore should be zero default since no cleaning was applied
	assert.Equal(t, 0.0, result.DataQualityScore)

	freshFinancialRepo.AssertExpectations(t)
	freshMarketRepo.AssertExpectations(t)
	freshMacroRepo.AssertExpectations(t)
	freshCache.AssertExpectations(t)
}

// TestService_CalculateValuation_DataCleanerError verifies that when the DataCleaner
// returns an error, the valuation pipeline falls back to using the original (uncleaned)
// data and still produces a valid result.
func TestService_CalculateValuation_DataCleanerError(t *testing.T) {
	ctx := context.Background()
	historicalData, marketData, macroData := createTestData()

	// Create fresh mocks for isolation
	freshFinancialRepo := &MockFinancialDataRepository{}
	freshMarketRepo := &MockMarketDataRepository{}
	freshMacroRepo := &MockMacroDataRepository{}
	freshCache := &MockCacheRepository{}
	freshDataCleaner := &MockDataCleanerService{}
	freshMetrics := &MockMetricsService{}
	logger := zap.NewNop()
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:             1 * time.Hour,
			SlowRequestThreshold: 500 * time.Millisecond,
			DataFetchTimeout:     30 * time.Second,
		},
	}

	service := NewService(freshFinancialRepo, freshMarketRepo, freshMacroRepo, freshCache, freshDataCleaner, nil, freshMetrics, cfg, logger)

	// DataCleaner returns an error — the service should log a warning and continue with original data
	freshDataCleaner.On("CleanFinancialData", ctx, mock.AnythingOfType("*entities.FinancialData")).
		Return((*entities.CleaningResult)(nil), errors.New("cleaning service unavailable"))

	// Setup expectations: cache miss, then successful data retrieval
	freshCache.On("Get", ctx, "valuation:AAPL", mock.AnythingOfType("*entities.ValuationResult")).Return(errors.New("cache miss"))
	freshFinancialRepo.On("GetHistorical", ctx, "AAPL", 10).Return(historicalData, nil)
	freshMarketRepo.On("GetLatest", ctx, "AAPL").Return(marketData, nil)
	freshMacroRepo.On("GetLatest", ctx).Return(macroData, nil)
	freshCache.On("Set", ctx, "valuation:AAPL", mock.AnythingOfType("*entities.ValuationResult"), 1*time.Hour).Return(nil)

	// Metrics expectations for a successful valuation
	freshMetrics.On("RecordValuationRequest", "AAPL", "single", "success", mock.AnythingOfType("time.Duration")).Return()
	freshMetrics.On("IncWACCCalculations").Return()
	freshMetrics.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	freshMetrics.On("IncDCFCalculations").Return()
	freshMetrics.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()

	result, err := service.CalculateValuation(ctx, "AAPL", nil)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "AAPL", result.Ticker)
	assert.Greater(t, result.DCFValuePerShare, 0.0)
	// DataQualityScore should be zero since cleaning failed and cleaningResult is nil
	assert.Equal(t, 0.0, result.DataQualityScore)

	freshDataCleaner.AssertExpectations(t)
	freshCache.AssertExpectations(t)
}

// TestService_CalculateValuation_CacheSetFailure verifies that when caching the
// result fails, the valuation still returns the successfully computed result.
// The cache failure is a non-critical error that is only logged as a warning.
func TestService_CalculateValuation_CacheSetFailure(t *testing.T) {
	ctx := context.Background()
	historicalData, marketData, macroData := createTestData()

	// Create fresh mocks for isolation
	freshFinancialRepo := &MockFinancialDataRepository{}
	freshMarketRepo := &MockMarketDataRepository{}
	freshMacroRepo := &MockMacroDataRepository{}
	freshCache := &MockCacheRepository{}
	freshDataCleaner := &MockDataCleanerService{}
	freshMetrics := &MockMetricsService{}
	logger := zap.NewNop()
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:             1 * time.Hour,
			SlowRequestThreshold: 500 * time.Millisecond,
			DataFetchTimeout:     30 * time.Second,
		},
	}

	service := NewService(freshFinancialRepo, freshMarketRepo, freshMacroRepo, freshCache, freshDataCleaner, nil, freshMetrics, cfg, logger)

	// DataCleaner succeeds normally
	cleaningResult := &entities.CleaningResult{
		Success:      true,
		QualityScore: 90.0,
		CleanedData:  historicalData.Data["2023FY"],
		Flags:        []entities.Flag{},
		Adjustments:  []entities.Adjustment{},
	}
	freshDataCleaner.On("CleanFinancialData", ctx, mock.AnythingOfType("*entities.FinancialData")).Return(cleaningResult, nil)

	// Cache miss on read, then cache Set returns an error (e.g., Redis unavailable)
	freshCache.On("Get", ctx, "valuation:AAPL", mock.AnythingOfType("*entities.ValuationResult")).Return(errors.New("cache miss"))
	freshFinancialRepo.On("GetHistorical", ctx, "AAPL", 10).Return(historicalData, nil)
	freshMarketRepo.On("GetLatest", ctx, "AAPL").Return(marketData, nil)
	freshMacroRepo.On("GetLatest", ctx).Return(macroData, nil)
	freshCache.On("Set", ctx, "valuation:AAPL", mock.AnythingOfType("*entities.ValuationResult"), 1*time.Hour).
		Return(errors.New("redis connection refused"))

	// Metrics expectations: valuation still succeeds despite cache failure
	freshMetrics.On("RecordValuationRequest", "AAPL", "single", "success", mock.AnythingOfType("time.Duration")).Return()
	freshMetrics.On("IncWACCCalculations").Return()
	freshMetrics.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	freshMetrics.On("IncDCFCalculations").Return()
	freshMetrics.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()

	result, err := service.CalculateValuation(ctx, "AAPL", nil)

	// The valuation should still succeed even though cache storage failed
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "AAPL", result.Ticker)
	assert.Greater(t, result.DCFValuePerShare, 0.0)
	// Cleaning was applied successfully
	assert.Equal(t, 90.0, result.DataQualityScore)

	freshCache.AssertExpectations(t)
	freshDataCleaner.AssertExpectations(t)
}

func TestService_performValuation(t *testing.T) {
	// Create service with properly configured metrics mock
	financialRepo := &MockFinancialDataRepository{}
	marketRepo := &MockMarketDataRepository{}
	macroRepo := &MockMacroDataRepository{}
	cache := &MockCacheRepository{}
	dataCleaner := &MockDataCleanerService{}
	metricsService := &MockMetricsService{}
	logger := zap.NewNop()
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:             1 * time.Hour,
			SlowRequestThreshold: 500 * time.Millisecond,
			DataFetchTimeout:     30 * time.Second,
		},
	}

	// Setup metrics expectations for performValuation calls
	metricsService.On("IncWACCCalculations").Return()
	metricsService.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	metricsService.On("IncDCFCalculations").Return()
	metricsService.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()

	service := NewService(financialRepo, marketRepo, macroRepo, cache, dataCleaner, nil, metricsService, cfg, logger)
	historicalData, marketData, macroData := createTestData()

	t.Run("successful valuation with good data", func(t *testing.T) {
		result, err := service.performValuation(historicalData, marketData, macroData, nil)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "AAPL", result.Ticker)
		assert.Greater(t, result.DCFValuePerShare, 0.0)
		assert.Greater(t, result.TangibleValuePerShare, 0.0)
		assert.Greater(t, result.WACC, 0.0)
		assert.Greater(t, result.GrowthRate, 0.0)
		assert.Greater(t, result.EnterpriseValue, 0.0)
		assert.Greater(t, result.DataFreshnessScore, 0)
		assert.Equal(t, "1.0", result.CalculationVersion)
	})

	t.Run("single period uses default growth rate", func(t *testing.T) {
		// With only 1 period, growth rate can't be calculated from history
		// so performValuation should use the conservative default (DefaultTerminalGrowthCap)
		singlePeriodData := &entities.HistoricalFinancialData{
			Ticker: "AAPL",
			Data: map[string]*entities.FinancialData{
				"2023FY": historicalData.Data["2023FY"], // Only one period
			},
		}

		result, err := service.performValuation(singlePeriodData, marketData, macroData, nil)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Greater(t, result.DCFValuePerShare, 0.0)
		// Growth rate comes from DefaultTerminalGrowthCap (0 in test config = most conservative)
		assert.GreaterOrEqual(t, result.GrowthRate, 0.0)
	})

	t.Run("insufficient historical data with empty periods", func(t *testing.T) {
		// Zero periods should still fail
		emptyData := &entities.HistoricalFinancialData{
			Ticker: "AAPL",
			Data:   map[string]*entities.FinancialData{},
		}

		result, err := service.performValuation(emptyData, marketData, macroData, nil)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrInsufficientData)
	})

	t.Run("incomplete market data", func(t *testing.T) {
		// Create incomplete market data
		incompleteMarketData := &entities.MarketData{
			Ticker:     "AAPL",
			AsOf:       time.Now(),
			SharePrice: 0, // Missing price
		}

		result, err := service.performValuation(historicalData, incompleteMarketData, macroData, nil)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "incomplete market data")
	})

	t.Run("incomplete macro data", func(t *testing.T) {
		// Create incomplete macro data
		incompleteMacroData := &entities.MacroData{
			AsOf:         time.Now(),
			RiskFreeRate: 0, // Missing risk-free rate
		}

		result, err := service.performValuation(historicalData, marketData, incompleteMacroData, nil)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "incomplete macro data")
	})
}

func TestService_calculateTangibleValuePerShare(t *testing.T) {
	service, _, _, _, _, _ := createTestService()
	_, marketData, _ := createTestData()

	t.Run("calculate with valid data", func(t *testing.T) {
		financial := &entities.FinancialData{
			TangibleAssets:      350000000000, // $350B
			InterestBearingDebt: 110000000000, // $110B
		}

		tangibleValue := service.calculateTangibleValuePerShare(financial, marketData)

		// Expected: 350B / 15.744B shares = ~22.23 (debt is not subtracted in this calculation)
		expected := 350000000000 / 15744231000
		assert.InDelta(t, expected, tangibleValue, 1.0) // Use larger delta for floating point precision
	})

	t.Run("calculate with zero debt", func(t *testing.T) {
		financial := &entities.FinancialData{
			TangibleAssets:      350000000000, // $350B
			InterestBearingDebt: 0,            // No debt
		}

		tangibleValue := service.calculateTangibleValuePerShare(financial, marketData)

		// Expected: 350B / 15.744B shares = ~22.23
		expected := 350000000000 / 15744231000
		assert.InDelta(t, expected, tangibleValue, 1.0) // Use larger delta for floating point precision
	})

	t.Run("zero market shares falls back to financial shares", func(t *testing.T) {
		// Market data has zero shares, so the function should fall back to financial shares
		financial := &entities.FinancialData{
			TangibleAssets:    350000000000, // $350B
			SharesOutstanding: 1000000000,   // 1B shares from financial data
		}
		zeroSharesMarket := &entities.MarketData{
			Ticker:            "AAPL",
			AsOf:              time.Now(),
			SharePrice:        180.50,
			SharesOutstanding: 0, // Zero shares in market data triggers fallback
		}

		tangibleValue := service.calculateTangibleValuePerShare(financial, zeroSharesMarket)

		// Expected: 350B / 1B shares = 350
		expected := 350000000000.0 / 1000000000.0
		assert.InDelta(t, expected, tangibleValue, 0.001)
	})

	t.Run("zero shares everywhere returns zero", func(t *testing.T) {
		// Both market and financial data have zero shares, should return 0
		financial := &entities.FinancialData{
			TangibleAssets:    350000000000,
			SharesOutstanding: 0,
		}
		zeroSharesMarket := &entities.MarketData{
			Ticker:            "AAPL",
			AsOf:              time.Now(),
			SharePrice:        180.50,
			SharesOutstanding: 0,
		}

		tangibleValue := service.calculateTangibleValuePerShare(financial, zeroSharesMarket)

		assert.Equal(t, 0.0, tangibleValue)
	})

	t.Run("negative market shares falls back to financial shares", func(t *testing.T) {
		// Negative shares in market data should trigger fallback to financial shares
		financial := &entities.FinancialData{
			TangibleAssets:    100000000000, // $100B
			SharesOutstanding: 500000000,    // 500M shares
		}
		negativeSharesMarket := &entities.MarketData{
			Ticker:            "AAPL",
			AsOf:              time.Now(),
			SharePrice:        180.50,
			SharesOutstanding: -1, // Negative triggers fallback
		}

		tangibleValue := service.calculateTangibleValuePerShare(financial, negativeSharesMarket)

		// Expected: 100B / 500M shares = 200
		expected := 100000000000.0 / 500000000.0
		assert.InDelta(t, expected, tangibleValue, 0.001)
	})
}

func TestService_calculateTerminalGrowthRate(t *testing.T) {
	service, _, _, _, _, _ := createTestService()

	t.Run("normal growth rate", func(t *testing.T) {
		historicalCAGR := 0.08 // 8%
		terminalGrowth := service.calculateTerminalGrowthRate(historicalCAGR)

		// Should be min(3%, half of 8%) = min(3%, 4%) = 3%
		assert.Equal(t, 0.03, terminalGrowth)
	})

	t.Run("low growth rate", func(t *testing.T) {
		historicalCAGR := 0.04 // 4%
		terminalGrowth := service.calculateTerminalGrowthRate(historicalCAGR)

		// Should be min(3%, half of 4%) = min(3%, 2%) = 2%
		assert.Equal(t, 0.02, terminalGrowth)
	})

	t.Run("zero growth rate", func(t *testing.T) {
		historicalCAGR := 0.0 // 0%
		terminalGrowth := service.calculateTerminalGrowthRate(historicalCAGR)

		// Should be min(3%, half of 0%) = min(3%, 0%) = 0%
		assert.Equal(t, 0.0, terminalGrowth)
	})

	t.Run("negative growth rate", func(t *testing.T) {
		historicalCAGR := -0.02 // -2%
		terminalGrowth := service.calculateTerminalGrowthRate(historicalCAGR)

		// Should be min(3%, half of -2%) = min(3%, -1%) but with a floor of 2%
		assert.Equal(t, 0.02, terminalGrowth) // 2% minimum for inflation
	})
}

func TestService_calculateDataFreshnessScore(t *testing.T) {
	service, _, _, _, _, _ := createTestService()

	t.Run("fresh data gets high score", func(t *testing.T) {
		financial := &entities.FinancialData{
			AsOf: time.Now().Add(-24 * time.Hour), // 1 day old
		}
		market := &entities.MarketData{
			AsOf: time.Now().Add(-1 * time.Hour), // 1 hour old
		}
		macro := &entities.MacroData{
			AsOf: time.Now().Add(-2 * time.Hour), // 2 hours old
		}

		score := service.calculateDataFreshnessScore(financial, market, macro)

		assert.Greater(t, score, 80) // Should be high score for fresh data
	})

	t.Run("stale data gets lower score", func(t *testing.T) {
		financial := &entities.FinancialData{
			AsOf: time.Now().Add(-120 * 24 * time.Hour), // 120 days old
		}
		market := &entities.MarketData{
			AsOf: time.Now().Add(-48 * time.Hour), // 2 days old
		}
		macro := &entities.MacroData{
			AsOf: time.Now().Add(-24 * time.Hour), // 1 day old
		}

		score := service.calculateDataFreshnessScore(financial, market, macro)

		assert.LessOrEqual(t, score, 60) // Should be lower score for stale data
	})

	t.Run("medium age financial data deducts 15 points", func(t *testing.T) {
		// Financial data between 30 and 90 days old hits the else-if branch (-15 penalty)
		financial := &entities.FinancialData{
			AsOf: time.Now().Add(-45 * 24 * time.Hour), // 45 days old (between 30 and 90 days)
		}
		market := &entities.MarketData{
			AsOf: time.Now(), // Fresh market data (no penalty)
		}
		macro := &entities.MacroData{
			AsOf: time.Now(), // Fresh macro data (no penalty)
		}

		score := service.calculateDataFreshnessScore(financial, market, macro)

		// Expected: 100 - 15 (financial 30-90 days) = 85
		assert.Equal(t, 85, score)
	})

	t.Run("very stale market data deducts 20 points", func(t *testing.T) {
		// Market data older than 7 days hits the first if branch (-20 penalty)
		financial := &entities.FinancialData{
			AsOf: time.Now(), // Fresh financial data (no penalty)
		}
		market := &entities.MarketData{
			AsOf: time.Now().Add(-10 * 24 * time.Hour), // 10 days old (> 7 days)
		}
		macro := &entities.MacroData{
			AsOf: time.Now(), // Fresh macro data (no penalty)
		}

		score := service.calculateDataFreshnessScore(financial, market, macro)

		// Expected: 100 - 20 (market > 7 days) = 80
		assert.Equal(t, 80, score)
	})

	t.Run("medium age macro data deducts 10 points", func(t *testing.T) {
		// Macro data between 7 and 30 days old hits the else-if branch (-10 penalty)
		financial := &entities.FinancialData{
			AsOf: time.Now(), // Fresh financial data (no penalty)
		}
		market := &entities.MarketData{
			AsOf: time.Now(), // Fresh market data (no penalty)
		}
		macro := &entities.MacroData{
			AsOf: time.Now().Add(-14 * 24 * time.Hour), // 14 days old (between 7 and 30 days)
		}

		score := service.calculateDataFreshnessScore(financial, market, macro)

		// Expected: 100 - 10 (macro 7-30 days) = 90
		assert.Equal(t, 90, score)
	})

	t.Run("very stale macro data deducts 20 points", func(t *testing.T) {
		// Macro data older than 30 days hits the first if branch (-20 penalty)
		financial := &entities.FinancialData{
			AsOf: time.Now(), // Fresh financial data (no penalty)
		}
		market := &entities.MarketData{
			AsOf: time.Now(), // Fresh market data (no penalty)
		}
		macro := &entities.MacroData{
			AsOf: time.Now().Add(-60 * 24 * time.Hour), // 60 days old (> 30 days)
		}

		score := service.calculateDataFreshnessScore(financial, market, macro)

		// Expected: 100 - 20 (macro > 30 days) = 80
		assert.Equal(t, 80, score)
	})

	t.Run("all data sources maximally stale", func(t *testing.T) {
		// All data sources hit their maximum penalty branches
		financial := &entities.FinancialData{
			AsOf: time.Now().Add(-365 * 24 * time.Hour), // 1 year old (> 90 days, -30)
		}
		market := &entities.MarketData{
			AsOf: time.Now().Add(-30 * 24 * time.Hour), // 30 days old (> 7 days, -20)
		}
		macro := &entities.MacroData{
			AsOf: time.Now().Add(-90 * 24 * time.Hour), // 90 days old (> 30 days, -20)
		}

		score := service.calculateDataFreshnessScore(financial, market, macro)

		// Expected: 100 - 30 (financial) - 20 (market) - 20 (macro) = 30
		assert.Equal(t, 30, score)
	})
}

// TestValuationWithCleaningIntegration tests integration between ValuationService and DataCleaner
func TestValuationWithCleaningIntegration(t *testing.T) {
	// Simple test to verify DataCleaner is properly injected
	t.Run("data_cleaner_injection_verification", func(t *testing.T) {
		// Create mocks
		mockFinancialRepo := &MockFinancialDataRepository{}
		mockMarketRepo := &MockMarketDataRepository{}
		mockMacroRepo := &MockMacroDataRepository{}
		mockCache := &MockCacheRepository{}
		mockDataCleaner := &MockDataCleanerService{}

		// Create service with DataCleaner injected
		mockMetrics := &MockMetricsService{}
		cfg := &config.Config{
			Valuation: config.ValuationConfig{
				CacheTTL:             1 * time.Hour,
				SlowRequestThreshold: 500 * time.Millisecond,
				DataFetchTimeout:     30 * time.Second,
			},
		}
		service := NewService(mockFinancialRepo, mockMarketRepo, mockMacroRepo, mockCache, mockDataCleaner, nil, mockMetrics, cfg, zap.NewNop())

		// Verify DataCleaner is injected
		assert.NotNil(t, service)
		// TODO: Add more detailed verification once DataCleaner integration is complete
	})
}

// TestValuationService_MetricsInstrumentation tests that valuation calculations are properly instrumented with metrics
func TestValuationService_MetricsInstrumentation(t *testing.T) {
	logger := zap.NewNop()
	metricsService := metrics.NewService(logger)

	// Create mocks
	mockFinancialRepo := &MockFinancialDataRepository{}
	mockMarketRepo := &MockMarketDataRepository{}
	mockMacroRepo := &MockMacroDataRepository{}
	mockCache := &MockCacheRepository{}
	mockDataCleaner := &MockDataCleanerService{}

	// Create service with metrics
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:             1 * time.Hour,
			SlowRequestThreshold: 500 * time.Millisecond,
			DataFetchTimeout:     30 * time.Second,
		},
	}
	service := NewService(
		mockFinancialRepo,
		mockMarketRepo,
		mockMacroRepo,
		mockCache,
		mockDataCleaner,
		nil, // DataFetcher not needed for this test
		metricsService,
		cfg,
		logger,
	)

	// Setup test data
	ctx := context.Background()
	ticker := "AAPL"

	// Mock successful data retrieval
	financialData := &entities.FinancialData{
		Ticker:                    ticker,
		Revenue:                   100000000000,
		OperatingIncome:           25000000000,
		NormalizedOperatingIncome: 26000000000, // Positive normalized operating income
		TotalAssets:               350000000000,
		TangibleAssets:            320000000000,
		InterestBearingDebt:       100000000000,
		SharesOutstanding:         1020000000,
		DilutedSharesOutstanding:  1030000000,
		InterestExpense:           3000000000,
		TaxRate:                   0.21,
		FilingPeriod:              "2023FY",
		FilingDate:                time.Now(),
		AsOf:                      time.Now(),
		Period:                    "2023FY",
		HasNormalizedData:         true,
	}

	marketData := &entities.MarketData{
		Ticker:            ticker,
		AsOf:              time.Now(),
		SharePrice:        150.0,
		MarketCap:         153000000000, // 150 * 1.02B shares
		SharesOutstanding: 1020000000,
		Beta:              1.2,
		Beta3Y:            1.15,
		AverageVolume:     75000000,
		Source:            "yfinance",
		DataQuality:       "good",
	}

	macroData := &entities.MacroData{
		RiskFreeRate:      0.05,
		MarketRiskPremium: 0.06,
		AsOf:              time.Now(),
	}

	// Setup mocks
	mockCache.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("cache miss"))
	historicalData := &entities.HistoricalFinancialData{
		Ticker: ticker,
		Data: map[string]*entities.FinancialData{
			"2023FY": financialData,
			"2022FY": {
				Ticker:                    ticker,
				Revenue:                   90000000000,
				OperatingIncome:           18000000000,
				NormalizedOperatingIncome: 18500000000,
				TotalAssets:               180000000000,
				TangibleAssets:            140000000000,
				TotalDebt:                 45000000000,
				InterestBearingDebt:       42000000000,
				SharesOutstanding:         1020000000,
				InterestExpense:           1800000000,
				TaxRate:                   0.21,
				AsOf:                      time.Now().Add(-365 * 24 * time.Hour),
				Period:                    "2022FY",
				HasNormalizedData:         true,
			},
			"2021FY": {
				Ticker:                    ticker,
				Revenue:                   80000000000,
				OperatingIncome:           16000000000,
				NormalizedOperatingIncome: 16800000000,
				TotalAssets:               160000000000,
				TangibleAssets:            125000000000,
				TotalDebt:                 40000000000,
				InterestBearingDebt:       38000000000,
				SharesOutstanding:         1030000000,
				InterestExpense:           1600000000,
				TaxRate:                   0.21,
				AsOf:                      time.Now().Add(-2 * 365 * 24 * time.Hour),
				Period:                    "2021FY",
				HasNormalizedData:         true,
			},
		},
	}
	mockFinancialRepo.On("GetHistorical", ctx, ticker, 10).Return(historicalData, nil)
	mockMarketRepo.On("GetLatest", ctx, ticker).Return(marketData, nil)
	mockMacroRepo.On("GetLatest", ctx).Return(macroData, nil)
	// Setup DataCleaner to return proper CleaningResult
	cleaningResult := &entities.CleaningResult{
		Success:      true,
		QualityScore: 85.0,
		CleanedData:  financialData,
		Flags:        []entities.Flag{},
		Adjustments:  []entities.Adjustment{},
	}
	mockDataCleaner.On("CleanFinancialData", mock.Anything, mock.AnythingOfType("*entities.FinancialData")).Return(cleaningResult, nil)
	mockCache.On("Set", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Record initial metrics
	initialDCFCalculations := metricsService.GetTotalValuations()
	initialSuccessfulValuations := metricsService.GetSuccessfulValuations()

	// Execute valuation
	result, err := service.CalculateValuation(ctx, ticker, nil)

	// Verify calculation succeeded
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, ticker, result.Ticker)
	assert.Greater(t, result.DCFValuePerShare, 0.0)
	assert.Greater(t, result.TangibleValuePerShare, 0.0)

	// Verify metrics were recorded
	assert.Greater(t, metricsService.GetTotalValuations(), initialDCFCalculations, "Should increment DCF calculations")
	assert.Greater(t, metricsService.GetSuccessfulValuations(), initialSuccessfulValuations, "Should increment successful valuations")

	// Verify all mocks were called
	mockFinancialRepo.AssertExpectations(t)
	mockMarketRepo.AssertExpectations(t)
	mockMacroRepo.AssertExpectations(t)
	mockDataCleaner.AssertExpectations(t)
}

// TestNewValuationService tests the service creation with mocked dependencies
func TestNewValuationService(t *testing.T) {
	// Create mocked dependencies
	mockFinancialRepo := &MockFinancialDataRepository{}
	mockMarketRepo := &MockMarketDataRepository{}
	mockMacroRepo := &MockMacroDataRepository{}
	mockCache := &MockCacheRepository{}
	mockDataCleaner := &MockDataCleanerService{}
	mockMetrics := &MockMetricsService{}
	logger := zap.NewNop()

	// Create test configuration
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:             1 * time.Hour,
			SlowRequestThreshold: 500 * time.Millisecond,
			DataFetchTimeout:     30 * time.Second,
		},
	}

	// Test service creation
	svc := NewService(
		mockFinancialRepo,
		mockMarketRepo,
		mockMacroRepo,
		mockCache,
		mockDataCleaner,
		nil, // DataFetcher not needed for this test
		mockMetrics,
		cfg,
		logger,
	)

	// Verify service was created successfully
	require.NotNil(t, svc, "Service should not be nil")

	// Verify service has all required dependencies
	assert.NotNil(t, svc.financialRepo, "Financial repository should be injected")
	assert.NotNil(t, svc.marketRepo, "Market repository should be injected")
	assert.NotNil(t, svc.macroRepo, "Macro repository should be injected")
	assert.NotNil(t, svc.cache, "Cache repository should be injected")
	assert.NotNil(t, svc.dataCleaner, "Data cleaner should be injected")
	assert.NotNil(t, svc.metricsService, "Metrics service should be injected")
	assert.NotNil(t, svc.config, "Configuration should be injected")
	assert.NotNil(t, svc.logger, "Logger should be injected")
}

// FakeMetricsService is a simple fake implementation for testing
type FakeMetricsService struct{}

func (f *FakeMetricsService) RecordHTTPRequest(method, endpoint string, statusCode int, duration time.Duration, responseSize int) {
}
func (f *FakeMetricsService) IncHTTPRequestsInFlight() {}
func (f *FakeMetricsService) DecHTTPRequestsInFlight() {}
func (f *FakeMetricsService) RecordValuationRequest(ticker, requestType, status string, duration time.Duration) {
}
func (f *FakeMetricsService) RecordValuationError(ticker, errorType string)                 {}
func (f *FakeMetricsService) IncDCFCalculations()                                           {}
func (f *FakeMetricsService) IncWACCCalculations()                                          {}
func (f *FakeMetricsService) RecordSECAPIRequest(endpoint, status string)                   {}
func (f *FakeMetricsService) RecordMarketAPIRequest(provider, status string)                {}
func (f *FakeMetricsService) RecordMacroAPIRequest(provider, status string)                 {}
func (f *FakeMetricsService) RecordDataFetch(source, ticker string, duration time.Duration) {}
func (f *FakeMetricsService) RecordCacheRequest(cacheType, operation, result string)        {}
func (f *FakeMetricsService) SetCacheHitRatio(cacheType string, ratio float64)              {}
func (f *FakeMetricsService) SetAverageWACC(wacc float64)                                   {}
func (f *FakeMetricsService) SetAverageGrowthRate(rate float64)                             {}
func (f *FakeMetricsService) GetTotalRequests() int64                                       { return 0 }
func (f *FakeMetricsService) GetActiveConnections() int                                     { return 0 }
func (f *FakeMetricsService) GetAverageResponseTime() float64                               { return 0 }
func (f *FakeMetricsService) GetErrorRate() float64                                         { return 0 }
func (f *FakeMetricsService) GetCacheHitRate() float64                                      { return 0 }
func (f *FakeMetricsService) GetTotalValuations() int64                                     { return 0 }
func (f *FakeMetricsService) GetSuccessfulValuations() int64                                { return 0 }
func (f *FakeMetricsService) GetFailedValuations() int64                                    { return 0 }
func (f *FakeMetricsService) GetAverageWACC() float64                                       { return 0 }
func (f *FakeMetricsService) GetAverageGrowthRate() float64                                 { return 0 }
func (f *FakeMetricsService) GetUniqueTickersServed() int64                                 { return 0 }
func (f *FakeMetricsService) HealthCheck() error                                            { return nil }

// TestNewValuationService_WithFakeMetrics tests service creation with a simple fake metrics implementation
func TestNewValuationService_WithFakeMetrics(t *testing.T) {
	// Create mocked dependencies
	mockFinancialRepo := &MockFinancialDataRepository{}
	mockMarketRepo := &MockMarketDataRepository{}
	mockMacroRepo := &MockMacroDataRepository{}
	mockCache := &MockCacheRepository{}
	mockDataCleaner := &MockDataCleanerService{}
	fakeMetrics := &FakeMetricsService{}
	logger := zap.NewNop()

	// Create test configuration
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:             1 * time.Hour,
			SlowRequestThreshold: 500 * time.Millisecond,
			DataFetchTimeout:     30 * time.Second,
		},
	}

	// Test service creation with fake metrics
	svc := NewService(
		mockFinancialRepo,
		mockMarketRepo,
		mockMacroRepo,
		mockCache,
		mockDataCleaner,
		nil, // DataFetcher not needed for this test
		fakeMetrics,
		cfg,
		logger,
	)

	// Verify service was created successfully
	require.NotNil(t, svc, "Service should not be nil")
	assert.NotNil(t, svc.metricsService, "Fake metrics service should be injected")
}

// TestService_CalculateValuation_OverrideBeta verifies that providing an override beta
// through ValuationOptions produces a different WACC than the default data-source beta.
// BUG-005: override_beta and override_rf were dead code — this test proves they work.
func TestService_CalculateValuation_OverrideBeta(t *testing.T) {
	historicalData, marketData, macroData := createTestData()

	// Create service with metrics expectations for two performValuation calls
	metricsService := &MockMetricsService{}
	metricsService.On("IncWACCCalculations").Return()
	metricsService.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	metricsService.On("IncDCFCalculations").Return()
	metricsService.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()

	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:             1 * time.Hour,
			SlowRequestThreshold: 500 * time.Millisecond,
			DataFetchTimeout:     30 * time.Second,
		},
	}
	service := NewService(nil, nil, nil, nil, nil, nil, metricsService, cfg, zap.NewNop())

	// Low beta (0.5) should produce a lower WACC
	lowBeta := 0.5
	resultLow, err := service.performValuation(historicalData, marketData, macroData, &ValuationOptions{OverrideBeta: &lowBeta})
	require.NoError(t, err)

	// High beta (2.0) should produce a higher WACC
	highBeta := 2.0
	resultHigh, err := service.performValuation(historicalData, marketData, macroData, &ValuationOptions{OverrideBeta: &highBeta})
	require.NoError(t, err)

	// Assert that different betas produce different WACCs
	assert.NotEqual(t, resultLow.WACC, resultHigh.WACC,
		"override_beta=0.5 and override_beta=2.0 must produce different WACC values")
	assert.Less(t, resultLow.WACC, resultHigh.WACC,
		"lower beta should produce lower WACC")
}

// TestService_CalculateValuation_OverrideRiskFree verifies that providing an override
// risk-free rate through ValuationOptions changes the resulting WACC.
func TestService_CalculateValuation_OverrideRiskFree(t *testing.T) {
	historicalData, marketData, macroData := createTestData()

	metricsService := &MockMetricsService{}
	metricsService.On("IncWACCCalculations").Return()
	metricsService.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	metricsService.On("IncDCFCalculations").Return()
	metricsService.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()

	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:             1 * time.Hour,
			SlowRequestThreshold: 500 * time.Millisecond,
			DataFetchTimeout:     30 * time.Second,
		},
	}
	service := NewService(nil, nil, nil, nil, nil, nil, metricsService, cfg, zap.NewNop())

	// Low risk-free rate (1%) should produce a lower WACC
	lowRF := 0.01
	resultLow, err := service.performValuation(historicalData, marketData, macroData, &ValuationOptions{OverrideRiskFree: &lowRF})
	require.NoError(t, err)

	// High risk-free rate (8%) should produce a higher WACC
	highRF := 0.08
	resultHigh, err := service.performValuation(historicalData, marketData, macroData, &ValuationOptions{OverrideRiskFree: &highRF})
	require.NoError(t, err)

	assert.NotEqual(t, resultLow.WACC, resultHigh.WACC,
		"override_rf=0.01 and override_rf=0.08 must produce different WACC values")
	assert.Less(t, resultLow.WACC, resultHigh.WACC,
		"lower risk-free rate should produce lower WACC")
}

// TestService_CalculateValuation_NilOptsDefaultBehavior verifies that passing nil opts
// produces the same result as before the BUG-005 fix (no regression).
func TestService_CalculateValuation_NilOptsDefaultBehavior(t *testing.T) {
	historicalData, marketData, macroData := createTestData()

	metricsService := &MockMetricsService{}
	metricsService.On("IncWACCCalculations").Return()
	metricsService.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	metricsService.On("IncDCFCalculations").Return()
	metricsService.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()

	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:             1 * time.Hour,
			SlowRequestThreshold: 500 * time.Millisecond,
			DataFetchTimeout:     30 * time.Second,
		},
	}
	service := NewService(nil, nil, nil, nil, nil, nil, metricsService, cfg, zap.NewNop())

	// Two calls with nil opts should produce identical WACCs
	result1, err := service.performValuation(historicalData, marketData, macroData, nil)
	require.NoError(t, err)

	result2, err := service.performValuation(historicalData, marketData, macroData, nil)
	require.NoError(t, err)

	assert.Equal(t, result1.WACC, result2.WACC, "nil opts should produce deterministic WACC")
}

// TestService_CalculateValuation_TickerNotFoundSentinel verifies that a non-existent
// ticker returns an error wrapping ErrTickerNotFound (BUG-006).
func TestService_CalculateValuation_TickerNotFoundSentinel(t *testing.T) {
	ctx := context.Background()

	freshFinancialRepo := &MockFinancialDataRepository{}
	freshCache := &MockCacheRepository{}
	freshMetrics := &MockMetricsService{}
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:             1 * time.Hour,
			SlowRequestThreshold: 500 * time.Millisecond,
			DataFetchTimeout:     30 * time.Second,
		},
	}

	// Service with nil DataFetcher — simulates missing data path
	service := NewService(freshFinancialRepo, nil, nil, freshCache, nil, nil, freshMetrics, cfg, zap.NewNop())

	// Cache miss, then repo returns no data
	freshCache.On("Get", ctx, "valuation:XYZA1", mock.AnythingOfType("*entities.ValuationResult")).Return(errors.New("cache miss"))
	freshFinancialRepo.On("GetHistorical", ctx, "XYZA1", 10).Return((*entities.HistoricalFinancialData)(nil), errors.New("no data"))

	result, err := service.CalculateValuation(ctx, "XYZA1", nil)

	assert.Nil(t, result)
	assert.Error(t, err)
	// The key assertion: the error wraps ErrTickerNotFound so handlers can use errors.Is()
	assert.ErrorIs(t, err, ErrTickerNotFound, "non-existent ticker must return ErrTickerNotFound sentinel")
}

// TestService_performValuation_InsufficientDataSentinel verifies that insufficient
// data errors wrap ErrInsufficientData (BUG-006).
func TestService_performValuation_InsufficientDataSentinel(t *testing.T) {
	metricsService := &MockMetricsService{}
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:             1 * time.Hour,
			SlowRequestThreshold: 500 * time.Millisecond,
			DataFetchTimeout:     30 * time.Second,
		},
	}
	service := NewService(nil, nil, nil, nil, nil, nil, metricsService, cfg, zap.NewNop())

	_, marketData, macroData := createTestData()

	// Empty historical data should produce ErrInsufficientData
	emptyData := &entities.HistoricalFinancialData{
		Ticker: "AAPL",
		Data:   map[string]*entities.FinancialData{},
	}

	_, err := service.performValuation(emptyData, marketData, macroData, nil)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInsufficientData, "empty data must return ErrInsufficientData sentinel")
}
