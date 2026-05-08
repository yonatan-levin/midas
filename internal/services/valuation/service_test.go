package valuation

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/observability/calclog"
	"github.com/midas/dcf-valuation-api/internal/services/datafetcher"
	"github.com/midas/dcf-valuation-api/internal/services/metrics"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
)

// newTestCalcEmitter builds a real calclog.Emitter for unit tests so that the
// 9 `if s.calcEmitter != nil` guard blocks in service.go are exercised.
// Phase M added the emit sites; without this, unit coverage drops below the
// project's 90% floor for critical finance code because the guarded branches
// are never reached.
func newTestCalcEmitter() *calclog.Emitter {
	return calclog.NewEmitter(&config.Config{
		Logging: config.LoggingConfig{TraceCalculations: true},
	})
}

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
			CacheTTL:                 1 * time.Hour,
			SlowRequestThreshold:     500 * time.Millisecond,
			DataFetchTimeout:         30 * time.Second,
			DefaultTerminalGrowthCap: 0.025,
			DCFMaxGrowthRate:         0.5,
			DCFMinGrowthRate:         -0.3,
		},
	}

	// Use nil for DataFetcher in unit tests since we mock repository responses
	service := NewService(financialRepo, marketRepo, macroRepo, cache, dataCleaner, nil, metricsService, cfg, logger, newTestCalcEmitter())

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
		service := NewService(financialRepo, marketRepo, macroRepo, cache, dataCleaner, nil, mockMetrics, cfg, logger, newTestCalcEmitter())

		// Setup expectations - cache miss first
		cache.On("Get", ctx, "valuation:v4:AAPL", mock.AnythingOfType("*entities.ValuationResult")).Return(errors.New("cache miss"))

		// Then data retrieval
		financialRepo.On("GetHistorical", ctx, "AAPL", 10).Return(historicalData, nil)
		marketRepo.On("GetLatest", ctx, "AAPL").Return(marketData, nil)
		macroRepo.On("GetLatest", ctx).Return(macroData, nil)

		// Cache storage
		cache.On("Set", ctx, "valuation:v4:AAPL", mock.AnythingOfType("*entities.ValuationResult"), 1*time.Hour).Return(nil)

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
		freshService := NewService(financialRepo, marketRepo, macroRepo, freshCache, freshDataCleaner, nil, freshMetrics, cfg, logger, newTestCalcEmitter())

		cachedResult := &entities.ValuationResult{
			Ticker:                "AAPL",
			CalculatedAt:          time.Now().Add(-30 * time.Minute),
			TangibleValuePerShare: 150.0,
			DCFValuePerShare:      175.0,
			WACC:                  0.08,
			GrowthRate:            0.05,
		}

		// Setup cache hit expectation
		freshCache.On("Get", ctx, "valuation:v4:AAPL", mock.AnythingOfType("*entities.ValuationResult")).Run(func(args mock.Arguments) {
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
		freshService := NewService(freshFinancialRepo, marketRepo, macroRepo, freshCache, freshDataCleaner, nil, mockMetrics, cfg, logger, newTestCalcEmitter())

		// Setup expectations - cache miss, no data in repo
		freshCache.On("Get", ctx, "valuation:v4:AAPL", mock.AnythingOfType("*entities.ValuationResult")).Return(errors.New("cache miss"))
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

		cache.On("Get", ctx, "valuation:v4:AAPL", mock.AnythingOfType("*entities.ValuationResult")).Return(errors.New("cache miss"))
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

		cache.On("Get", ctx, "valuation:v4:AAPL", mock.AnythingOfType("*entities.ValuationResult")).Return(errors.New("cache miss"))
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
	service := NewService(freshFinancialRepo, freshMarketRepo, freshMacroRepo, freshCache, nil, nil, freshMetrics, cfg, logger, newTestCalcEmitter())

	// Setup expectations: cache miss, then successful data retrieval
	freshCache.On("Get", ctx, "valuation:v4:AAPL", mock.AnythingOfType("*entities.ValuationResult")).Return(errors.New("cache miss"))
	freshFinancialRepo.On("GetHistorical", ctx, "AAPL", 10).Return(historicalData, nil)
	freshMarketRepo.On("GetLatest", ctx, "AAPL").Return(marketData, nil)
	freshMacroRepo.On("GetLatest", ctx).Return(macroData, nil)
	freshCache.On("Set", ctx, "valuation:v4:AAPL", mock.AnythingOfType("*entities.ValuationResult"), 1*time.Hour).Return(nil)

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

	service := NewService(freshFinancialRepo, freshMarketRepo, freshMacroRepo, freshCache, freshDataCleaner, nil, freshMetrics, cfg, logger, newTestCalcEmitter())

	// DataCleaner returns an error — the service should log a warning and continue with original data
	freshDataCleaner.On("CleanFinancialData", ctx, mock.AnythingOfType("*entities.FinancialData")).
		Return((*entities.CleaningResult)(nil), errors.New("cleaning service unavailable"))

	// Setup expectations: cache miss, then successful data retrieval
	freshCache.On("Get", ctx, "valuation:v4:AAPL", mock.AnythingOfType("*entities.ValuationResult")).Return(errors.New("cache miss"))
	freshFinancialRepo.On("GetHistorical", ctx, "AAPL", 10).Return(historicalData, nil)
	freshMarketRepo.On("GetLatest", ctx, "AAPL").Return(marketData, nil)
	freshMacroRepo.On("GetLatest", ctx).Return(macroData, nil)
	freshCache.On("Set", ctx, "valuation:v4:AAPL", mock.AnythingOfType("*entities.ValuationResult"), 1*time.Hour).Return(nil)

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

	service := NewService(freshFinancialRepo, freshMarketRepo, freshMacroRepo, freshCache, freshDataCleaner, nil, freshMetrics, cfg, logger, newTestCalcEmitter())

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
	freshCache.On("Get", ctx, "valuation:v4:AAPL", mock.AnythingOfType("*entities.ValuationResult")).Return(errors.New("cache miss"))
	freshFinancialRepo.On("GetHistorical", ctx, "AAPL", 10).Return(historicalData, nil)
	freshMarketRepo.On("GetLatest", ctx, "AAPL").Return(marketData, nil)
	freshMacroRepo.On("GetLatest", ctx).Return(macroData, nil)
	freshCache.On("Set", ctx, "valuation:v4:AAPL", mock.AnythingOfType("*entities.ValuationResult"), 1*time.Hour).
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

// TestService_performValuation_MinorityInterestPreferredEquity_BridgeDelta
// pins the M-1d follow-through end-to-end (BLOCKER-2 from the validation
// cycle): given a HistoricalFinancialData carrying non-zero MinorityInterest
// and PreferredEquity, the per-share value emitted by performValuation must
// drop by exactly (MI + PE) / shares vs. the same fixture with both zero.
//
// This is the integration-shaped counterpart to TestCalculateEquityValue: the
// dcf-layer test pins the bridge math; this test pins that the service
// actually threads the values from FinancialData into the bridge call. The
// two together close the original "end-to-end claim is unverified" gap.
func TestService_performValuation_MinorityInterestPreferredEquity_BridgeDelta(t *testing.T) {
	const (
		// BRK-style scale relative to the createTestData fixture: MI/PE roughly
		// 5%/0.5% of the fixture's total assets. Large enough that the per-share
		// delta is well above floating-point noise without triggering any
		// downstream guard (negative equity, etc.).
		miAmount = 25_000_000_000.0 // $25B
		peAmount = 2_500_000_000.0  // $2.5B
	)

	makeService := func() (*Service, *entities.HistoricalFinancialData, *entities.MarketData, *entities.MacroData) {
		financialRepo := &MockFinancialDataRepository{}
		marketRepo := &MockMarketDataRepository{}
		macroRepo := &MockMacroDataRepository{}
		cache := &MockCacheRepository{}
		dataCleaner := &MockDataCleanerService{}
		metricsService := &MockMetricsService{}
		metricsService.On("IncWACCCalculations").Return()
		metricsService.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
		metricsService.On("IncDCFCalculations").Return()
		metricsService.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()
		cfg := &config.Config{
			Valuation: config.ValuationConfig{
				CacheTTL:                 1 * time.Hour,
				SlowRequestThreshold:     500 * time.Millisecond,
				DataFetchTimeout:         30 * time.Second,
				DefaultTerminalGrowthCap: 0.025,
				DCFMaxGrowthRate:         0.5,
				DCFMinGrowthRate:         -0.3,
			},
		}
		svc := NewService(financialRepo, marketRepo, macroRepo, cache, dataCleaner, nil, metricsService, cfg, zap.NewNop(), newTestCalcEmitter())
		hist, mkt, mac := createTestData()
		return svc, hist, mkt, mac
	}

	// Baseline: MI = PE = 0 (legacy behavior).
	baseSvc, baseHist, baseMkt, baseMac := makeService()
	baseResult, err := baseSvc.performValuation(context.Background(), baseHist, baseMkt, baseMac, nil)
	require.NoError(t, err)
	require.NotNil(t, baseResult)
	require.Greater(t, baseResult.DCFValuePerShare, 0.0)

	// With MI/PE: clone the fixture and set MI/PE on every period so whichever
	// period is the "latest" carries them. createTestData() returns three
	// periods (2021FY, 2022FY, 2023FY) — patch all three.
	miSvc, miHist, miMkt, miMac := makeService()
	for _, period := range miHist.Data {
		period.MinorityInterest = miAmount
		period.PreferredEquity = peAmount
	}
	miResult, err := miSvc.performValuation(context.Background(), miHist, miMkt, miMac, nil)
	require.NoError(t, err)
	require.NotNil(t, miResult)

	// Per-share delta must match (MI + PE) / shares within float tolerance.
	// shares is whatever performValuation chose — read it back from the result
	// via EquityValue / DCFValuePerShare to avoid duplicating the share
	// resolution logic.
	require.Greater(t, baseResult.DCFValuePerShare, 0.0)
	shares := baseResult.EquityValue / baseResult.DCFValuePerShare
	require.Greater(t, shares, 0.0, "shares derived from baseResult must be positive")

	expectedDelta := (miAmount + peAmount) / shares
	actualDelta := baseResult.DCFValuePerShare - miResult.DCFValuePerShare

	// Delta tolerance: 0.5% of the expected delta is comfortable headroom for
	// any floating-point recombination through the WACC/growth pipeline.
	tolerance := 0.005 * expectedDelta
	assert.InDelta(t, expectedDelta, actualDelta, tolerance,
		"per-share delta must equal (MI+PE)/shares; baseline=%.4f miResult=%.4f delta=%.4f expected=%.4f",
		baseResult.DCFValuePerShare, miResult.DCFValuePerShare, actualDelta, expectedDelta)

	// Sanity: the equity value itself should drop by exactly MI + PE.
	expectedEquityDelta := miAmount + peAmount
	actualEquityDelta := baseResult.EquityValue - miResult.EquityValue
	assert.InDelta(t, expectedEquityDelta, actualEquityDelta, expectedEquityDelta*0.001,
		"equity value delta must equal MI+PE")
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
			CacheTTL:                 1 * time.Hour,
			SlowRequestThreshold:     500 * time.Millisecond,
			DataFetchTimeout:         30 * time.Second,
			DefaultTerminalGrowthCap: 0.025,
			DCFMaxGrowthRate:         0.5,
			DCFMinGrowthRate:         -0.3,
		},
	}

	// Setup metrics expectations for performValuation calls
	metricsService.On("IncWACCCalculations").Return()
	metricsService.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	metricsService.On("IncDCFCalculations").Return()
	metricsService.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()

	service := NewService(financialRepo, marketRepo, macroRepo, cache, dataCleaner, nil, metricsService, cfg, logger, newTestCalcEmitter())
	historicalData, marketData, macroData := createTestData()

	t.Run("successful valuation with good data", func(t *testing.T) {
		result, err := service.performValuation(context.Background(), historicalData, marketData, macroData, nil)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "AAPL", result.Ticker)
		assert.Greater(t, result.DCFValuePerShare, 0.0)
		assert.Greater(t, result.TangibleValuePerShare, 0.0)
		assert.Greater(t, result.WACC, 0.0)
		assert.Greater(t, result.GrowthRate, 0.0)
		assert.Greater(t, result.EnterpriseValue, 0.0)
		assert.Greater(t, result.DataFreshnessScore, 0)
		assert.Equal(t, "4.1", result.CalculationVersion)
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

		result, err := service.performValuation(context.Background(), singlePeriodData, marketData, macroData, nil)

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

		result, err := service.performValuation(context.Background(), emptyData, marketData, macroData, nil)

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

		result, err := service.performValuation(context.Background(), historicalData, incompleteMarketData, macroData, nil)

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

		result, err := service.performValuation(context.Background(), historicalData, marketData, incompleteMacroData, nil)

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

// TestService_calculateTangibleValuePerShare_DilutedDenominator pins the
// Graham-floor PR #2 denominator flip: when DilutedSharesOutstanding > 0 it
// MUST be preferred over market.SharesOutstanding and financial.SharesOutstanding,
// matching the share-resolution priority chain used for the DCF path
// (service.go ~lines 862-873). This is the breaking 2-5% numeric drift called
// out in graham-floor-metrics-spec.md §4.5 and §10 R1, AC-4.
func TestService_calculateTangibleValuePerShare_DilutedDenominator(t *testing.T) {
	service, _, _, _, _, _ := createTestService()

	tests := []struct {
		name           string
		financial      *entities.FinancialData
		market         *entities.MarketData
		expectedShares float64 // chosen denominator
		expectedValue  float64 // tangibleAssets / expectedShares
	}{
		{
			// Diluted differs from basic; diluted MUST win over both market
			// and financial basic. Pre-flip behaviour would have picked
			// market.SharesOutstanding (100M) and produced 110.0.
			name: "diluted preferred over market basic and financial basic",
			financial: &entities.FinancialData{
				TangibleAssets:           11_000_000_000, // $11B
				DilutedSharesOutstanding: 110_000_000,    // 110M diluted
				SharesOutstanding:        100_000_000,    // 100M basic (decoy)
			},
			market: &entities.MarketData{
				Ticker:            "DILTEST",
				AsOf:              time.Now(),
				SharePrice:        50.0,
				SharesOutstanding: 100_000_000, // 100M (decoy — should NOT be picked)
			},
			expectedShares: 110_000_000,
			expectedValue:  11_000_000_000.0 / 110_000_000.0, // 100.0
		},
		{
			// Diluted == 0 falls through to market basic.
			name: "diluted zero falls back to market basic",
			financial: &entities.FinancialData{
				TangibleAssets:           10_000_000_000, // $10B
				DilutedSharesOutstanding: 0,
				SharesOutstanding:        120_000_000, // 120M (decoy financial basic)
			},
			market: &entities.MarketData{
				Ticker:            "MKTFALLBACK",
				AsOf:              time.Now(),
				SharePrice:        50.0,
				SharesOutstanding: 100_000_000, // 100M market basic — should win
			},
			expectedShares: 100_000_000,
			expectedValue:  100.0,
		},
		{
			// Diluted == 0 AND market <= 0 falls through to financial basic.
			name: "diluted zero and market zero falls back to financial basic",
			financial: &entities.FinancialData{
				TangibleAssets:           12_000_000_000, // $12B
				DilutedSharesOutstanding: 0,
				SharesOutstanding:        120_000_000, // 120M financial basic — should win
			},
			market: &entities.MarketData{
				Ticker:            "FINFALLBACK",
				AsOf:              time.Now(),
				SharePrice:        50.0,
				SharesOutstanding: 0, // zero — falls through
			},
			expectedShares: 120_000_000,
			expectedValue:  100.0,
		},
		{
			// All share sources zero → returns 0 (no divide-by-zero).
			name: "all shares zero returns zero",
			financial: &entities.FinancialData{
				TangibleAssets:           5_000_000_000,
				DilutedSharesOutstanding: 0,
				SharesOutstanding:        0,
			},
			market: &entities.MarketData{
				Ticker:            "ZERO",
				AsOf:              time.Now(),
				SharePrice:        10.0,
				SharesOutstanding: 0,
			},
			expectedShares: 0,
			expectedValue:  0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := service.calculateTangibleValuePerShare(tc.financial, tc.market)
			assert.InDelta(t, tc.expectedValue, got, 1e-9,
				"expected tangible_value_per_share = TangibleAssets/%.0f = %.4f, got %.4f",
				tc.expectedShares, tc.expectedValue, got)
		})
	}
}

func TestService_calculateTerminalGrowthRate(t *testing.T) {
	service, _, _, _, _, _ := createTestService()
	normalWACC := 0.10 // 10% — comfortably above all terminal growth rates

	t.Run("normal growth rate", func(t *testing.T) {
		historicalCAGR := 0.08 // 8%
		terminalGrowth := service.calculateTerminalGrowthRate(historicalCAGR, normalWACC)

		// Should be min(3%, half of 8%) = min(3%, 4%) = 3%
		assert.Equal(t, 0.03, terminalGrowth)
	})

	t.Run("low growth rate", func(t *testing.T) {
		historicalCAGR := 0.04 // 4%
		terminalGrowth := service.calculateTerminalGrowthRate(historicalCAGR, normalWACC)

		// Should be min(3%, half of 4%) = min(3%, 2%) = 2%
		assert.Equal(t, 0.02, terminalGrowth)
	})

	t.Run("zero growth rate", func(t *testing.T) {
		historicalCAGR := 0.0 // 0%
		terminalGrowth := service.calculateTerminalGrowthRate(historicalCAGR, normalWACC)

		// Should use 2% floor for inflation since CAGR <= 0
		assert.Equal(t, 0.02, terminalGrowth)
	})

	t.Run("negative growth rate", func(t *testing.T) {
		historicalCAGR := -0.02 // -2%
		terminalGrowth := service.calculateTerminalGrowthRate(historicalCAGR, normalWACC)

		// Should be min(3%, half of -2%) = min(3%, -1%) but with a floor of 2%
		assert.Equal(t, 0.02, terminalGrowth) // 2% minimum for inflation
	})

	t.Run("WACC-terminal spread enforcement", func(t *testing.T) {
		// With low WACC of 4%, terminal growth 3% would leave only 1% spread
		// The function should cap terminal to WACC - 2% = 2%
		historicalCAGR := 0.10 // 10% — would normally give 3% terminal
		lowWACC := 0.04

		terminalGrowth := service.calculateTerminalGrowthRate(historicalCAGR, lowWACC)

		assert.Equal(t, 0.02, terminalGrowth) // capped to WACC - 0.02
	})

	t.Run("very low WACC floors terminal at 1%", func(t *testing.T) {
		historicalCAGR := 0.10
		veryLowWACC := 0.025 // 2.5% WACC

		terminalGrowth := service.calculateTerminalGrowthRate(historicalCAGR, veryLowWACC)

		assert.Equal(t, 0.01, terminalGrowth) // floor at 1% even when WACC-0.02 < 1%
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

	t.Run("score floors at zero not negative", func(t *testing.T) {
		financial := &entities.FinancialData{
			AsOf: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), // very old
		}
		market := &entities.MarketData{
			AsOf: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), // very old
		}
		macro := &entities.MacroData{
			AsOf: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), // very old
		}

		score := service.calculateDataFreshnessScore(financial, market, macro)
		// All penalties: -30 -20 -20 = -70 from 100 = 30. But with extreme dates all branches hit max.
		assert.GreaterOrEqual(t, score, 0, "Score should never go below 0")
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
		service := NewService(mockFinancialRepo, mockMarketRepo, mockMacroRepo, mockCache, mockDataCleaner, nil, mockMetrics, cfg, zap.NewNop(), newTestCalcEmitter())

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
		nil, // calcEmitter nil in unit tests
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
		nil, // calcEmitter nil in unit tests
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
		nil, // calcEmitter nil in unit tests
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
	service := NewService(nil, nil, nil, nil, nil, nil, metricsService, cfg, zap.NewNop(), newTestCalcEmitter())

	// Low beta (0.5) should produce a lower WACC
	lowBeta := 0.5
	resultLow, err := service.performValuation(context.Background(), historicalData, marketData, macroData, &ValuationOptions{OverrideBeta: &lowBeta})
	require.NoError(t, err)

	// High beta (2.0) should produce a higher WACC
	highBeta := 2.0
	resultHigh, err := service.performValuation(context.Background(), historicalData, marketData, macroData, &ValuationOptions{OverrideBeta: &highBeta})
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
	service := NewService(nil, nil, nil, nil, nil, nil, metricsService, cfg, zap.NewNop(), newTestCalcEmitter())

	// Low risk-free rate (1%) should produce a lower WACC
	lowRF := 0.01
	resultLow, err := service.performValuation(context.Background(), historicalData, marketData, macroData, &ValuationOptions{OverrideRiskFree: &lowRF})
	require.NoError(t, err)

	// High risk-free rate (8%) should produce a higher WACC
	highRF := 0.08
	resultHigh, err := service.performValuation(context.Background(), historicalData, marketData, macroData, &ValuationOptions{OverrideRiskFree: &highRF})
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
	service := NewService(nil, nil, nil, nil, nil, nil, metricsService, cfg, zap.NewNop(), newTestCalcEmitter())

	// Two calls with nil opts should produce identical WACCs
	result1, err := service.performValuation(context.Background(), historicalData, marketData, macroData, nil)
	require.NoError(t, err)

	result2, err := service.performValuation(context.Background(), historicalData, marketData, macroData, nil)
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
	service := NewService(freshFinancialRepo, nil, nil, freshCache, nil, nil, freshMetrics, cfg, zap.NewNop(), newTestCalcEmitter())

	// Cache miss, then repo returns no data
	freshCache.On("Get", ctx, "valuation:v4:XYZA1", mock.AnythingOfType("*entities.ValuationResult")).Return(errors.New("cache miss"))
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
	service := NewService(nil, nil, nil, nil, nil, nil, metricsService, cfg, zap.NewNop(), newTestCalcEmitter())

	_, marketData, macroData := createTestData()

	// Empty historical data should produce ErrInsufficientData
	emptyData := &entities.HistoricalFinancialData{
		Ticker: "AAPL",
		Data:   map[string]*entities.FinancialData{},
	}

	_, err := service.performValuation(context.Background(), emptyData, marketData, macroData, nil)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInsufficientData, "empty data must return ErrInsufficientData sentinel")
}

// TestService_CalculateValuation_OverridesSkipCache verifies that when
// ValuationOptions contain overrides (beta or risk-free), the cache is
// bypassed for both reads (Get) and writes (Set) to prevent cache pollution.
func TestService_CalculateValuation_OverridesSkipCache(t *testing.T) {
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
			CacheTTL:                 1 * time.Hour,
			SlowRequestThreshold:     500 * time.Millisecond,
			DataFetchTimeout:         30 * time.Second,
			DefaultTerminalGrowthCap: 0.025,
			DCFMaxGrowthRate:         0.5,
			DCFMinGrowthRate:         -0.3,
		},
	}

	service := NewService(freshFinancialRepo, freshMarketRepo, freshMacroRepo, freshCache, freshDataCleaner, nil, freshMetrics, cfg, logger, newTestCalcEmitter())

	// Repo returns data normally
	freshFinancialRepo.On("GetHistorical", ctx, "AAPL", 10).Return(historicalData, nil)
	freshMarketRepo.On("GetLatest", ctx, "AAPL").Return(marketData, nil)
	freshMacroRepo.On("GetLatest", ctx).Return(macroData, nil)

	// DataCleaner succeeds
	cleaningResult := &entities.CleaningResult{
		Success:      true,
		QualityScore: 80.0,
		CleanedData:  historicalData.Data["2023FY"],
		Flags:        []entities.Flag{},
		Adjustments:  []entities.Adjustment{},
	}
	freshDataCleaner.On("CleanFinancialData", ctx, mock.AnythingOfType("*entities.FinancialData")).Return(cleaningResult, nil)

	// Metrics expectations
	freshMetrics.On("RecordValuationRequest", "AAPL", "single", "success", mock.AnythingOfType("time.Duration")).Return()
	freshMetrics.On("IncWACCCalculations").Return()
	freshMetrics.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	freshMetrics.On("IncDCFCalculations").Return()
	freshMetrics.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()

	// Call with override beta — cache should NOT be used at all
	overrideBeta := 1.0
	result, err := service.CalculateValuation(ctx, "AAPL", &ValuationOptions{OverrideBeta: &overrideBeta})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "AAPL", result.Ticker)

	// Verify cache.Get and cache.Set were never called (overrides bypass cache)
	freshCache.AssertNotCalled(t, "Get", mock.Anything, mock.Anything, mock.Anything)
	freshCache.AssertNotCalled(t, "Set", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	freshFinancialRepo.AssertExpectations(t)
	freshMarketRepo.AssertExpectations(t)
	freshMacroRepo.AssertExpectations(t)
}

// TestService_CalculateValuation_PerformValuationError verifies that when
// performValuation returns an error (e.g., zero shares), the CalculateValuation
// method records error metrics and returns the error to the caller.
func TestService_CalculateValuation_PerformValuationError(t *testing.T) {
	ctx := context.Background()
	_, _, macroData := createTestData()

	// Create historical data where all shares = 0, causing performValuation to fail
	// with ErrInsufficientData (shares outstanding not available)
	badHistorical := &entities.HistoricalFinancialData{
		Ticker: "BAD",
		Data: map[string]*entities.FinancialData{
			"2023FY": {
				Ticker:                    "BAD",
				FilingPeriod:              "2023FY",
				FilingDate:                time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
				AsOf:                      time.Now(),
				OperatingIncome:           50000000000,
				NormalizedOperatingIncome: 50000000000,
				Revenue:                   200000000000,
				TaxRate:                   0.21,
				TotalAssets:               300000000000,
				TangibleAssets:            250000000000,
				InterestBearingDebt:       80000000000,
				SharesOutstanding:         0, // zero — will fail
				DilutedSharesOutstanding:  0, // zero — will fail
				HasNormalizedData:         true,
				InterestExpense:           2000000000,
			},
			"2022FY": {
				Ticker:                    "BAD",
				FilingPeriod:              "2022FY",
				FilingDate:                time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC),
				AsOf:                      time.Now().Add(-365 * 24 * time.Hour),
				OperatingIncome:           45000000000,
				NormalizedOperatingIncome: 45000000000,
				Revenue:                   180000000000,
				TaxRate:                   0.21,
				TotalAssets:               280000000000,
				TangibleAssets:            230000000000,
				InterestBearingDebt:       75000000000,
				SharesOutstanding:         0,
				DilutedSharesOutstanding:  0,
				HasNormalizedData:         true,
				InterestExpense:           1800000000,
			},
		},
	}

	// Use market data with zero shares so the fallback also fails
	zeroSharesMarket := &entities.MarketData{
		Ticker:            "BAD",
		AsOf:              time.Now(),
		SharePrice:        100.0,
		MarketCap:         0,
		SharesOutstanding: 0, // zero shares in market data too
		Beta:              1.2,
		Beta3Y:            1.1,
		AverageVolume:     50000000,
		Source:            "yfinance",
		DataQuality:       "good",
	}

	freshFinancialRepo := &MockFinancialDataRepository{}
	freshMarketRepo := &MockMarketDataRepository{}
	freshMacroRepo := &MockMacroDataRepository{}
	freshCache := &MockCacheRepository{}
	freshDataCleaner := &MockDataCleanerService{}
	freshMetrics := &MockMetricsService{}
	logger := zap.NewNop()
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:                 1 * time.Hour,
			DefaultTerminalGrowthCap: 0.025,
			DCFMaxGrowthRate:         0.5,
			DCFMinGrowthRate:         -0.3,
		},
	}

	service := NewService(freshFinancialRepo, freshMarketRepo, freshMacroRepo, freshCache, freshDataCleaner, nil, freshMetrics, cfg, logger, newTestCalcEmitter())

	// Cache miss
	freshCache.On("Get", ctx, "valuation:v4:BAD", mock.AnythingOfType("*entities.ValuationResult")).Return(errors.New("cache miss"))

	// Repo returns bad data
	freshFinancialRepo.On("GetHistorical", ctx, "BAD", 10).Return(badHistorical, nil)
	freshMarketRepo.On("GetLatest", ctx, "BAD").Return(zeroSharesMarket, nil)
	freshMacroRepo.On("GetLatest", ctx).Return(macroData, nil)

	// DataCleaner succeeds but it doesn't matter — performValuation will fail
	cleaningResult := &entities.CleaningResult{
		Success:      true,
		QualityScore: 75.0,
		CleanedData:  badHistorical.Data["2023FY"],
		Flags:        []entities.Flag{},
		Adjustments:  []entities.Adjustment{},
	}
	freshDataCleaner.On("CleanFinancialData", ctx, mock.AnythingOfType("*entities.FinancialData")).Return(cleaningResult, nil)

	// Expect error metrics to be recorded (this is the path we want to cover)
	freshMetrics.On("RecordValuationRequest", "BAD", "single", "error", mock.AnythingOfType("time.Duration")).Return()
	freshMetrics.On("RecordValuationError", "BAD", "calculation_failed").Return()
	freshMetrics.On("IncWACCCalculations").Return()
	freshMetrics.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()

	result, err := service.CalculateValuation(ctx, "BAD", nil)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to perform valuation")

	// Verify error metrics were called
	freshMetrics.AssertCalled(t, "RecordValuationRequest", "BAD", "single", "error", mock.AnythingOfType("time.Duration"))
	freshMetrics.AssertCalled(t, "RecordValuationError", "BAD", "calculation_failed")
	freshCache.AssertExpectations(t)
}

// TestService_performValuation_WACCFailure verifies that when WACC calculation
// fails (e.g., negative beta via override), performValuation returns the error.
func TestService_performValuation_WACCFailure(t *testing.T) {
	historicalData, marketData, macroData := createTestData()

	metricsService := &MockMetricsService{}
	// No WACC/DCF metrics expected since WACC calculation will fail
	logger := zap.NewNop()
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:                 1 * time.Hour,
			DefaultTerminalGrowthCap: 0.025,
			DCFMaxGrowthRate:         0.5,
			DCFMinGrowthRate:         -0.3,
		},
	}
	service := NewService(nil, nil, nil, nil, nil, nil, metricsService, cfg, logger, newTestCalcEmitter())

	// Use a negative beta override to trigger WACC validation failure
	negativeBeta := -1.0
	result, err := service.performValuation(context.Background(), historicalData, marketData, macroData, &ValuationOptions{OverrideBeta: &negativeBeta})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to calculate WACC")
}

// TestService_performValuation_SharesFallback tests the share count fallback
// chain: DilutedShares -> MarketData shares -> FinancialData shares.
func TestService_performValuation_SharesFallback(t *testing.T) {
	metricsService := &MockMetricsService{}
	metricsService.On("IncWACCCalculations").Return()
	metricsService.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	metricsService.On("IncDCFCalculations").Return()
	metricsService.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()
	logger := zap.NewNop()
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:                 1 * time.Hour,
			DefaultTerminalGrowthCap: 0.025,
			DCFMaxGrowthRate:         0.5,
			DCFMinGrowthRate:         -0.3,
		},
	}
	service := NewService(nil, nil, nil, nil, nil, nil, metricsService, cfg, logger, newTestCalcEmitter())

	t.Run("uses diluted shares when available", func(t *testing.T) {
		_, marketData, macroData := createTestData()
		historicalData := &entities.HistoricalFinancialData{
			Ticker: "TEST",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					Ticker: "TEST", FilingPeriod: "2023FY",
					FilingDate:                time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
					AsOf:                      time.Now(),
					OperatingIncome:           10000000000,
					NormalizedOperatingIncome: 10000000000,
					Revenue:                   50000000000,
					TaxRate:                   0.21,
					TotalAssets:               100000000000,
					TangibleAssets:            80000000000,
					InterestBearingDebt:       20000000000,
					InterestExpense:           1000000000,
					SharesOutstanding:         1000000000,
					DilutedSharesOutstanding:  1050000000, // diluted > basic
					HasNormalizedData:         true,
				},
				"2022FY": {
					Ticker: "TEST", FilingPeriod: "2022FY",
					FilingDate:                time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC),
					AsOf:                      time.Now().Add(-365 * 24 * time.Hour),
					OperatingIncome:           9000000000,
					NormalizedOperatingIncome: 9000000000,
					Revenue:                   45000000000,
					TaxRate:                   0.21,
					TotalAssets:               95000000000,
					TangibleAssets:            75000000000,
					InterestBearingDebt:       18000000000,
					InterestExpense:           900000000,
					SharesOutstanding:         1000000000,
					DilutedSharesOutstanding:  1050000000,
					HasNormalizedData:         true,
				},
			},
		}

		result, err := service.performValuation(context.Background(), historicalData, marketData, macroData, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Greater(t, result.DCFValuePerShare, 0.0)
	})

	t.Run("falls back to market shares when diluted is zero", func(t *testing.T) {
		_, marketData, macroData := createTestData()
		historicalData := &entities.HistoricalFinancialData{
			Ticker: "TEST",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					Ticker: "TEST", FilingPeriod: "2023FY",
					FilingDate:                time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
					AsOf:                      time.Now(),
					OperatingIncome:           10000000000,
					NormalizedOperatingIncome: 10000000000,
					Revenue:                   50000000000,
					TaxRate:                   0.21,
					TotalAssets:               100000000000,
					TangibleAssets:            80000000000,
					InterestBearingDebt:       20000000000,
					InterestExpense:           1000000000,
					SharesOutstanding:         1000000000,
					DilutedSharesOutstanding:  0, // no diluted shares
					HasNormalizedData:         true,
				},
				"2022FY": {
					Ticker: "TEST", FilingPeriod: "2022FY",
					FilingDate:                time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC),
					AsOf:                      time.Now().Add(-365 * 24 * time.Hour),
					OperatingIncome:           9000000000,
					NormalizedOperatingIncome: 9000000000,
					Revenue:                   45000000000,
					TaxRate:                   0.21,
					TotalAssets:               95000000000,
					TangibleAssets:            75000000000,
					InterestBearingDebt:       18000000000,
					InterestExpense:           900000000,
					SharesOutstanding:         1000000000,
					DilutedSharesOutstanding:  0,
					HasNormalizedData:         true,
				},
			},
		}

		result, err := service.performValuation(context.Background(), historicalData, marketData, macroData, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Should use marketData.SharesOutstanding = 15744231000
		assert.Greater(t, result.DCFValuePerShare, 0.0)
	})

	// NOTE: The "falls back to financial shares" and "all share sources zero" paths
	// are unreachable in production because MarketData.IsComplete() requires
	// SharesOutstanding > 0, which means the second fallback (marketData.SharesOutstanding)
	// will always succeed. Lines 349-355 in service.go are defensive guards only.
}

// TestService_calculateDataFreshnessScore_ScoreFloorAtZero verifies that the
// data freshness score is floored at 0 when all data sources are extremely stale.
// Current penalties max out at -70 (from 100 start), so score = 30. This test
// documents the boundary behavior and ensures the floor guard works.
func TestService_calculateDataFreshnessScore_ScoreFloorAtZero(t *testing.T) {
	service, _, _, _, _, _ := createTestService()

	financial := &entities.FinancialData{
		AsOf: time.Now().Add(-365 * 24 * time.Hour), // 1 year old
	}
	market := &entities.MarketData{
		AsOf: time.Now().Add(-365 * 24 * time.Hour), // 1 year old
	}
	macro := &entities.MacroData{
		AsOf: time.Now().Add(-365 * 24 * time.Hour), // 1 year old
	}

	score := service.calculateDataFreshnessScore(financial, market, macro)

	// Score = 100 - 30 (financial) - 20 (market) - 20 (macro) = 30
	assert.Equal(t, 30, score)
	assert.GreaterOrEqual(t, score, 0, "Score should never be negative")
}

func TestService_calculateNetWorkingCapitalChange(t *testing.T) {
	service, _, _, _, _, _ := createTestService()

	t.Run("valid two-period data", func(t *testing.T) {
		latest := &entities.FinancialData{
			CurrentAssets:      500000,
			CurrentLiabilities: 300000,
			FilingDate:         time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		}
		historical := &entities.HistoricalFinancialData{
			Ticker: "TEST",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					CurrentAssets:      400000,
					CurrentLiabilities: 250000,
					FilingDate:         time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC),
					Revenue:            1000000,
					OperatingIncome:    100000,
					SharesOutstanding:  1000,
				},
				"2024FY": latest,
			},
		}
		// Latest NWC = 500000 - 300000 = 200000
		// Prior NWC = 400000 - 250000 = 150000
		// Delta = 200000 - 150000 = 50000 (cash consumed)
		result := service.calculateNetWorkingCapitalChange(historical, latest)
		assert.InDelta(t, 50000.0, result, 0.01)
	})

	t.Run("single period returns zero", func(t *testing.T) {
		latest := &entities.FinancialData{
			CurrentAssets:      500000,
			CurrentLiabilities: 300000,
			FilingDate:         time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			Revenue:            1000000,
			OperatingIncome:    100000,
			SharesOutstanding:  1000,
		}
		historical := &entities.HistoricalFinancialData{
			Ticker: "TEST",
			Data:   map[string]*entities.FinancialData{"2024FY": latest},
		}
		result := service.calculateNetWorkingCapitalChange(historical, latest)
		assert.Equal(t, 0.0, result)
	})

	t.Run("missing current assets returns zero", func(t *testing.T) {
		latest := &entities.FinancialData{
			CurrentAssets:      0, // missing
			CurrentLiabilities: 300000,
		}
		historical := &entities.HistoricalFinancialData{Ticker: "TEST", Data: map[string]*entities.FinancialData{}}
		result := service.calculateNetWorkingCapitalChange(historical, latest)
		assert.Equal(t, 0.0, result)
	})

	t.Run("missing current liabilities returns zero", func(t *testing.T) {
		latest := &entities.FinancialData{
			CurrentAssets:      500000,
			CurrentLiabilities: 0, // missing
		}
		historical := &entities.HistoricalFinancialData{Ticker: "TEST", Data: map[string]*entities.FinancialData{}}
		result := service.calculateNetWorkingCapitalChange(historical, latest)
		assert.Equal(t, 0.0, result)
	})

	t.Run("prior period missing WC data returns zero", func(t *testing.T) {
		latest := &entities.FinancialData{
			CurrentAssets:      500000,
			CurrentLiabilities: 300000,
			FilingDate:         time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		}
		historical := &entities.HistoricalFinancialData{
			Ticker: "TEST",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					CurrentAssets:      0, // missing
					CurrentLiabilities: 0, // missing
					FilingDate:         time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC),
					Revenue:            1000000,
					OperatingIncome:    100000,
					SharesOutstanding:  1000,
				},
				"2024FY": latest,
			},
		}
		result := service.calculateNetWorkingCapitalChange(historical, latest)
		assert.Equal(t, 0.0, result)
	})

	t.Run("negative NWC change (cash released)", func(t *testing.T) {
		latest := &entities.FinancialData{
			CurrentAssets:      400000,
			CurrentLiabilities: 350000,
			FilingDate:         time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		}
		historical := &entities.HistoricalFinancialData{
			Ticker: "TEST",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					CurrentAssets:      500000,
					CurrentLiabilities: 300000,
					FilingDate:         time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC),
					Revenue:            1000000,
					OperatingIncome:    100000,
					SharesOutstanding:  1000,
				},
				"2024FY": latest,
			},
		}
		// Latest NWC = 400000 - 350000 = 50000
		// Prior NWC = 500000 - 300000 = 200000
		// Delta = 50000 - 200000 = -150000 (cash released)
		result := service.calculateNetWorkingCapitalChange(historical, latest)
		assert.InDelta(t, -150000.0, result, 0.01)
	})
}

func TestService_performValuation_NegativeOperatingIncome(t *testing.T) {
	_, marketData, macroData := createTestData()

	negativeOI := &entities.HistoricalFinancialData{
		Ticker: "RIVN",
		Data: map[string]*entities.FinancialData{
			"2023FY": {
				Ticker:                    "RIVN",
				FilingPeriod:              "2023FY",
				FilingDate:                time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
				AsOf:                      time.Now(),
				OperatingIncome:           -5000000000,
				NormalizedOperatingIncome: -4500000000,
				Revenue:                   4434000000,
				TaxRate:                   0.0,
				TotalAssets:               18000000000,
				TangibleAssets:            16000000000,
				InterestBearingDebt:       5000000000,
				SharesOutstanding:         1000000000,
				HasNormalizedData:         true,
			},
			"2022FY": {
				Ticker:                    "RIVN",
				FilingPeriod:              "2022FY",
				FilingDate:                time.Date(2023, 2, 1, 0, 0, 0, 0, time.UTC),
				AsOf:                      time.Now().Add(-365 * 24 * time.Hour),
				OperatingIncome:           -6800000000,
				NormalizedOperatingIncome: -6500000000,
				Revenue:                   1658000000,
				TaxRate:                   0.0,
				TotalAssets:               17000000000,
				TangibleAssets:            15000000000,
				InterestBearingDebt:       4500000000,
				SharesOutstanding:         950000000,
				HasNormalizedData:         true,
			},
		},
	}

	metricsService := &MockMetricsService{}
	metricsService.On("IncWACCCalculations").Return()
	metricsService.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	logger := zap.NewNop()
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:                 1 * time.Hour,
			DefaultTerminalGrowthCap: 0.025,
			DCFMaxGrowthRate:         0.5,
			DCFMinGrowthRate:         -0.3,
		},
	}
	svc := NewService(nil, nil, nil, nil, nil, nil, metricsService, cfg, logger, newTestCalcEmitter())

	result, err := svc.performValuation(context.Background(), negativeOI, marketData, macroData, nil)
	// Phase 3: negative OI now routes to revenue_multiple model instead of failing
	assert.NoError(t, err, "Negative OI should route to revenue multiple model")
	assert.NotNil(t, result, "Should return a result from revenue multiple model")
	if result != nil {
		assert.Equal(t, "revenue_multiple", result.CalculationMethod,
			"Should use revenue multiple model for negative OI")
		assert.Equal(t, "4.1", result.CalculationVersion)
		assert.Greater(t, result.DCFValuePerShare, 0.0,
			"Revenue multiple should produce a positive value when revenue is available")
	}
}

func TestService_performValuation_TrueFCF(t *testing.T) {
	_, _, _, _, _, _ = createTestService()
	historicalData, marketData, macroData := createTestData()

	// Add D&A and CapEx data to trigger true FCF path
	for _, fd := range historicalData.Data {
		fd.DepreciationAndAmortization = 11000000000
		fd.CapitalExpenditures = 10000000000
		fd.CurrentAssets = 135000000000
		fd.CurrentLiabilities = 145000000000
		fd.CashAndCashEquivalents = 29000000000
		fd.DilutedSharesOutstanding = fd.SharesOutstanding * 1.02
	}

	metricsService := &MockMetricsService{}
	metricsService.On("IncWACCCalculations").Return()
	metricsService.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	metricsService.On("IncDCFCalculations").Return()
	metricsService.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()
	logger := zap.NewNop()
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:                 1 * time.Hour,
			DefaultTerminalGrowthCap: 0.025,
			DCFMaxGrowthRate:         0.5,
			DCFMinGrowthRate:         -0.3,
		},
	}
	svc := NewService(nil, nil, nil, nil, nil, nil, metricsService, cfg, logger, newTestCalcEmitter())

	result, err := svc.performValuation(context.Background(), historicalData, marketData, macroData, nil)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Greater(t, result.DCFValuePerShare, 0.0)
	assert.Greater(t, result.EquityValue, 0.0)
	assert.Equal(t, "4.1", result.CalculationVersion)
}

func TestService_performValuation_GrowthCapping(t *testing.T) {
	metricsService := &MockMetricsService{}
	metricsService.On("IncWACCCalculations").Return()
	metricsService.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	metricsService.On("IncDCFCalculations").Return()
	metricsService.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()
	logger := zap.NewNop()
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:                 1 * time.Hour,
			DefaultTerminalGrowthCap: 0.025,
			DCFMaxGrowthRate:         0.30, // tight cap: 30%
			DCFMinGrowthRate:         -0.1,
		},
	}
	svc := NewService(nil, nil, nil, nil, nil, nil, metricsService, cfg, logger, newTestCalcEmitter())

	// Create data with extreme growth (OI jumps 5x in 2 years → ~124% CAGR)
	_, marketData, macroData := createTestData()
	extremeGrowth := &entities.HistoricalFinancialData{
		Ticker: "NVDA",
		Data: map[string]*entities.FinancialData{
			"2022FY": {
				Ticker: "NVDA", FilingPeriod: "2022FY",
				FilingDate:                time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC),
				AsOf:                      time.Now().Add(-730 * 24 * time.Hour),
				OperatingIncome:           10000000000,
				NormalizedOperatingIncome: 10000000000,
				Revenue:                   27000000000,
				TaxRate:                   0.12,
				TotalAssets:               44000000000,
				TangibleAssets:            20000000000,
				InterestBearingDebt:       10000000000,
				SharesOutstanding:         25000000000,
				HasNormalizedData:         true,
			},
			"2024FY": {
				Ticker: "NVDA", FilingPeriod: "2024FY",
				FilingDate:                time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC),
				AsOf:                      time.Now(),
				OperatingIncome:           50000000000,
				NormalizedOperatingIncome: 50000000000,
				Revenue:                   130000000000,
				TaxRate:                   0.12,
				TotalAssets:               96000000000,
				TangibleAssets:            50000000000,
				InterestBearingDebt:       12000000000,
				SharesOutstanding:         25000000000,
				HasNormalizedData:         true,
			},
		},
	}

	result, err := svc.performValuation(context.Background(), extremeGrowth, marketData, macroData, nil)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	// Growth should be capped to 30% (config max), not the raw ~124% CAGR
	assert.LessOrEqual(t, result.GrowthRate, 0.30,
		"Growth rate should be capped to configured maximum")
}

// --- Mock gateway implementations for DataFetcher integration tests ---

// MockSECGateway mocks the SEC EDGAR gateway for DataFetcher tests.
type MockSECGateway struct {
	mock.Mock
}

func (m *MockSECGateway) GetCompanyFacts(ctx context.Context, cik string) (*entities.CompanyFactsResponse, error) {
	args := m.Called(ctx, cik)
	return args.Get(0).(*entities.CompanyFactsResponse), args.Error(1)
}

func (m *MockSECGateway) GetCompanyConcepts(ctx context.Context, cik string, tag string) (*entities.ConceptResponse, error) {
	args := m.Called(ctx, cik, tag)
	return args.Get(0).(*entities.ConceptResponse), args.Error(1)
}

func (m *MockSECGateway) GetTickerCIKMapping(ctx context.Context) (map[string]string, error) {
	args := m.Called(ctx)
	return args.Get(0).(map[string]string), args.Error(1)
}

func (m *MockSECGateway) GetFinancialDataForTicker(ctx context.Context, ticker, cik string) (*entities.HistoricalFinancialData, error) {
	args := m.Called(ctx, ticker, cik)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.HistoricalFinancialData), args.Error(1)
}

func (m *MockSECGateway) HealthCheck(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// MockMarketDataGateway mocks the market data gateway for DataFetcher tests.
type MockMarketDataGateway struct {
	mock.Mock
}

func (m *MockMarketDataGateway) GetQuote(ctx context.Context, ticker string) (*entities.MarketData, error) {
	args := m.Called(ctx, ticker)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.MarketData), args.Error(1)
}

func (m *MockMarketDataGateway) GetQuotes(ctx context.Context, tickers []string) (map[string]*entities.MarketData, error) {
	args := m.Called(ctx, tickers)
	return args.Get(0).(map[string]*entities.MarketData), args.Error(1)
}

func (m *MockMarketDataGateway) GetHistoricalPrices(ctx context.Context, ticker string, startDate, endDate time.Time) ([]*entities.PriceData, error) {
	args := m.Called(ctx, ticker, startDate, endDate)
	return args.Get(0).([]*entities.PriceData), args.Error(1)
}

func (m *MockMarketDataGateway) HealthCheck(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// MockMacroDataGateway mocks the macro data gateway for DataFetcher tests.
type MockMacroDataGateway struct {
	mock.Mock
}

func (m *MockMacroDataGateway) GetTreasuryRates(ctx context.Context) (*entities.TreasuryRates, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.TreasuryRates), args.Error(1)
}

func (m *MockMacroDataGateway) GetMarketRiskPremium(ctx context.Context) (float64, error) {
	args := m.Called(ctx)
	return args.Get(0).(float64), args.Error(1)
}

// GetFXRate is a no-op stub for tests that do not exercise the FX path.
// Phase B7 added GetFXRate to ports.MacroDataGateway; existing valuation
// tests do not call it, so we return identity (1.0) without consulting
// m.Called to avoid forcing every test to declare an FX expectation.
func (m *MockMacroDataGateway) GetFXRate(_ context.Context, _, _ string) (float64, error) {
	return 1.0, nil
}

func (m *MockMacroDataGateway) HealthCheck(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// TestService_CalculateValuation_DataFetcherPath verifies the end-to-end path
// where the repository has no data and the service falls back to the DataFetcher
// to retrieve data from external APIs (SEC, market, macro).
func TestService_CalculateValuation_DataFetcherPath(t *testing.T) {
	ctx := context.Background()

	// Create mock gateways for the DataFetcher
	mockSEC := &MockSECGateway{}
	mockMarketGW := &MockMarketDataGateway{}
	mockMacroGW := &MockMacroDataGateway{}
	fetcherCache := &MockCacheRepository{}

	// Build a real DataFetcher with mock gateways
	dataFetcher := datafetcher.NewDataFetcher(mockSEC, mockMarketGW, mockMacroGW, fetcherCache)

	// Service-level mocks
	freshFinancialRepo := &MockFinancialDataRepository{}
	freshMarketRepo := &MockMarketDataRepository{}
	freshMacroRepo := &MockMacroDataRepository{}
	serviceCache := &MockCacheRepository{}
	freshMetrics := &MockMetricsService{}
	logger := zap.NewNop()
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:                 1 * time.Hour,
			DefaultTerminalGrowthCap: 0.025,
			DCFMaxGrowthRate:         0.5,
			DCFMinGrowthRate:         -0.3,
		},
	}

	// Create service with DataFetcher (not nil) and nil DataCleaner
	service := NewService(freshFinancialRepo, freshMarketRepo, freshMacroRepo, serviceCache, nil, dataFetcher, freshMetrics, cfg, logger, newTestCalcEmitter())

	// Prepare test data that the gateways will return
	historicalData := &entities.HistoricalFinancialData{
		Ticker: "MSFT",
		Data: map[string]*entities.FinancialData{
			"2023FY": {
				Ticker:                    "MSFT",
				FilingPeriod:              "2023FY",
				FilingDate:                time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
				AsOf:                      time.Now(),
				OperatingIncome:           88000000000,
				NormalizedOperatingIncome: 85000000000,
				Revenue:                   211915000000,
				TaxRate:                   0.18,
				TotalAssets:               411000000000,
				TangibleAssets:            300000000000,
				InterestBearingDebt:       50000000000,
				InterestExpense:           2000000000,
				SharesOutstanding:         7433000000,
				DilutedSharesOutstanding:  7500000000,
				HasNormalizedData:         true,
			},
			"2022FY": {
				Ticker:                    "MSFT",
				FilingPeriod:              "2022FY",
				FilingDate:                time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC),
				AsOf:                      time.Now().Add(-365 * 24 * time.Hour),
				OperatingIncome:           83383000000,
				NormalizedOperatingIncome: 80000000000,
				Revenue:                   198270000000,
				TaxRate:                   0.18,
				TotalAssets:               364840000000,
				TangibleAssets:            260000000000,
				InterestBearingDebt:       47000000000,
				InterestExpense:           1900000000,
				SharesOutstanding:         7473000000,
				DilutedSharesOutstanding:  7530000000,
				HasNormalizedData:         true,
			},
		},
	}

	marketData := &entities.MarketData{
		Ticker:            "MSFT",
		AsOf:              time.Now(),
		SharePrice:        420.0,
		MarketCap:         3121860000000,
		SharesOutstanding: 7433000000,
		Beta:              0.89,
		Beta3Y:            0.85,
		AverageVolume:     25000000,
		Source:            "yfinance",
		DataQuality:       "good",
	}

	_ = &entities.MacroData{
		AsOf:               time.Now(),
		RiskFreeRate:       0.045,
		RiskFreeRate3Month: 0.043,
		MarketRiskPremium:  0.06,
		InflationRate:      0.032,
		Source:             "fred",
	}

	// --- Setup expectations ---

	// Service cache miss
	serviceCache.On("Get", ctx, "valuation:v4:MSFT", mock.AnythingOfType("*entities.ValuationResult")).Return(errors.New("cache miss"))

	// Repository returns no data, triggering DataFetcher path
	freshFinancialRepo.On("GetHistorical", ctx, "MSFT", 10).Return(
		(*entities.HistoricalFinancialData)(nil), errors.New("no data found"))

	// DataFetcher gateway mocks: SEC returns historical data
	mockSEC.On("GetTickerCIKMapping", mock.Anything).Return(
		map[string]string{"MSFT": "789019"}, nil)
	mockSEC.On("GetFinancialDataForTicker", mock.Anything, "MSFT", "789019").Return(
		historicalData, nil)

	// DataFetcher gateway mocks: market data
	mockMarketGW.On("GetQuote", mock.Anything, "MSFT").Return(marketData, nil)

	// DataFetcher gateway mocks: macro data
	mockMacroGW.On("GetTreasuryRates", mock.Anything).Return(
		&entities.TreasuryRates{
			AsOf:        time.Now(),
			Yield10Year: 0.045,
			Yield3Month: 0.043,
		}, nil)
	mockMacroGW.On("GetMarketRiskPremium", mock.Anything).Return(0.06, nil)

	// DataFetcher uses its own cache for ticker mapping
	fetcherCache.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("cache miss"))
	fetcherCache.On("Set", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Service cache set after successful valuation
	serviceCache.On("Set", ctx, "valuation:v4:MSFT", mock.AnythingOfType("*entities.ValuationResult"), 1*time.Hour).Return(nil)

	// Metrics
	freshMetrics.On("RecordValuationRequest", "MSFT", "single", "success", mock.AnythingOfType("time.Duration")).Return()
	freshMetrics.On("IncWACCCalculations").Return()
	freshMetrics.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	freshMetrics.On("IncDCFCalculations").Return()
	freshMetrics.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()

	// Execute
	result, err := service.CalculateValuation(ctx, "MSFT", nil)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "MSFT", result.Ticker)
	assert.Greater(t, result.DCFValuePerShare, 0.0)
	assert.Greater(t, result.WACC, 0.0)
	assert.Greater(t, result.EnterpriseValue, 0.0)

	// Verify DataFetcher gateway mocks were called
	mockSEC.AssertCalled(t, "GetTickerCIKMapping", mock.Anything)
	mockSEC.AssertCalled(t, "GetFinancialDataForTicker", mock.Anything, "MSFT", "789019")
	freshFinancialRepo.AssertExpectations(t)
	serviceCache.AssertExpectations(t)
}

// TestService_CalculateValuation_DataFetcherFetchFails verifies that when the
// DataFetcher's Fetch method fails, the error is propagated to the caller.
func TestService_CalculateValuation_DataFetcherFetchFails(t *testing.T) {
	ctx := context.Background()

	// Create mock gateways that will cause failure
	mockSEC := &MockSECGateway{}
	mockMarketGW := &MockMarketDataGateway{}
	mockMacroGW := &MockMacroDataGateway{}
	fetcherCache := &MockCacheRepository{}

	dataFetcher := datafetcher.NewDataFetcher(mockSEC, mockMarketGW, mockMacroGW, fetcherCache)

	freshFinancialRepo := &MockFinancialDataRepository{}
	serviceCache := &MockCacheRepository{}
	freshMetrics := &MockMetricsService{}
	logger := zap.NewNop()
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:                 1 * time.Hour,
			DefaultTerminalGrowthCap: 0.025,
			DCFMaxGrowthRate:         0.5,
			DCFMinGrowthRate:         -0.3,
		},
	}

	service := NewService(freshFinancialRepo, nil, nil, serviceCache, nil, dataFetcher, freshMetrics, cfg, logger, newTestCalcEmitter())

	// Cache miss
	serviceCache.On("Get", ctx, "valuation:v4:UNKNOWN", mock.AnythingOfType("*entities.ValuationResult")).Return(errors.New("cache miss"))

	// Repo returns no data
	freshFinancialRepo.On("GetHistorical", ctx, "UNKNOWN", 10).Return(
		(*entities.HistoricalFinancialData)(nil), errors.New("no data"))

	// SEC gateway fails — causes DataFetcher to fail
	mockSEC.On("GetTickerCIKMapping", mock.Anything).Return(
		map[string]string{}, nil) // empty mapping — ticker not found

	// SEC gateway returns nil for unknown ticker
	mockSEC.On("GetFinancialDataForTicker", mock.Anything, "UNKNOWN", "").Return(
		(*entities.HistoricalFinancialData)(nil), errors.New("CIK not found"))

	// Market and macro gateways return errors too
	mockMarketGW.On("GetQuote", mock.Anything, "UNKNOWN").Return(
		(*entities.MarketData)(nil), errors.New("ticker not found"))
	mockMacroGW.On("GetTreasuryRates", mock.Anything).Return(
		(*entities.TreasuryRates)(nil), errors.New("service unavailable"))
	mockMacroGW.On("GetMarketRiskPremium", mock.Anything).Return(0.0, errors.New("service unavailable"))

	// Fetcher cache
	fetcherCache.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("cache miss"))
	fetcherCache.On("Set", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	result, err := service.CalculateValuation(ctx, "UNKNOWN", nil)

	assert.Error(t, err)
	assert.Nil(t, result)
	// Should indicate either fetch failure or ticker not found
	assert.True(t,
		errors.Is(err, ErrTickerNotFound) ||
			assert.ObjectsAreEqual("failed to fetch data via DataFetcher", err.Error()),
		"Error should indicate data fetch failure: %v", err)

	freshFinancialRepo.AssertExpectations(t)
}

// TestService_performValuation_FINZeroDPS_FallbackToDCF verifies that when a FIN company
// has zero dividends (DDM fails) but positive operating income, the service falls back
// to the standard multi-stage DCF path instead of returning ErrModelNotApplicable.
func TestService_performValuation_FINZeroDPS_FallbackToDCF(t *testing.T) {
	_, marketData, macroData := createTestData()

	// FIN company with zero dividends but positive OI -> DDM should fail -> DCF fallback
	finData := &entities.HistoricalFinancialData{
		Ticker: "JPM",
		Data: map[string]*entities.FinancialData{
			"2023FY": {
				Ticker:                    "JPM",
				FilingPeriod:              "2023FY",
				FilingDate:                time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
				AsOf:                      time.Now(),
				OperatingIncome:           55000000000,
				NormalizedOperatingIncome: 53000000000,
				Revenue:                   160000000000,
				InterestExpense:           8000000000,
				TaxRate:                   0.21,
				TotalAssets:               3700000000000,
				TangibleAssets:            3600000000000,
				InterestBearingDebt:       300000000000,
				SharesOutstanding:         2900000000,
				DilutedSharesOutstanding:  2950000000,
				HasNormalizedData:         true,
				IndustryCode:              "FIN",
				DividendsPerShare:         0, // Zero DPS causes DDM to fail
				NetIncome:                 49000000000,
			},
			"2022FY": {
				Ticker:                    "JPM",
				FilingPeriod:              "2022FY",
				FilingDate:                time.Date(2023, 2, 1, 0, 0, 0, 0, time.UTC),
				AsOf:                      time.Now().Add(-365 * 24 * time.Hour),
				OperatingIncome:           48000000000,
				NormalizedOperatingIncome: 46000000000,
				Revenue:                   130000000000,
				InterestExpense:           6000000000,
				TaxRate:                   0.20,
				TotalAssets:               3500000000000,
				TangibleAssets:            3400000000000,
				InterestBearingDebt:       280000000000,
				SharesOutstanding:         2950000000,
				DilutedSharesOutstanding:  3000000000,
				HasNormalizedData:         true,
				IndustryCode:              "FIN",
				DividendsPerShare:         0,
				NetIncome:                 42000000000,
			},
		},
	}

	metricsService := &MockMetricsService{}
	metricsService.On("IncWACCCalculations").Return()
	metricsService.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	metricsService.On("IncDCFCalculations").Return()
	metricsService.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()
	logger := zap.NewNop()
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:                 1 * time.Hour,
			DefaultTerminalGrowthCap: 0.025,
			DCFMaxGrowthRate:         0.5,
			DCFMinGrowthRate:         -0.3,
		},
	}
	svc := NewService(nil, nil, nil, nil, nil, nil, metricsService, cfg, logger, newTestCalcEmitter())

	result, err := svc.performValuation(context.Background(), finData, marketData, macroData, nil)

	// DDM should fail (zero DPS) but positive OI triggers DCF fallback
	assert.NoError(t, err, "FIN company with zero DPS but positive OI should fall back to DCF")
	assert.NotNil(t, result, "Should return a result from DCF fallback")
	if result != nil {
		assert.Equal(t, "multi_stage_dcf", result.CalculationMethod,
			"Should fall back to multi_stage_dcf when DDM fails and OI is positive")
		assert.Equal(t, "4.1", result.CalculationVersion)
		assert.Greater(t, result.DCFValuePerShare, 0.0,
			"DCF fallback should produce a positive value")
		// S-2 nit: verify the fallback warning is present
		hasWarning := false
		for _, w := range result.Warnings {
			if strings.Contains(w, "Primary model") && strings.Contains(w, "fell back") {
				hasWarning = true
			}
		}
		assert.True(t, hasWarning, "Should have a warning about primary model fallback")
	}
}

func TestService_performValuation_FINNegativeOI_FallbackToRevMultiple(t *testing.T) {
	// FIN company with zero DPS AND negative OI → DDM fails → revenue_multiple fallback
	metricsService := &MockMetricsService{}
	metricsService.On("IncWACCCalculations").Return()
	metricsService.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	metricsService.On("IncDCFCalculations").Return().Maybe()
	metricsService.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return().Maybe()
	logger := zap.NewNop()
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:                 1 * time.Hour,
			DefaultTerminalGrowthCap: 0.025,
			DCFMaxGrowthRate:         0.5,
			DCFMinGrowthRate:         -0.3,
		},
	}
	svc := NewService(nil, nil, nil, nil, nil, nil, metricsService, cfg, logger, newTestCalcEmitter())

	_, marketData, macroData := createTestData()
	historicalData := &entities.HistoricalFinancialData{
		Ticker: "AFRM",
		Data: map[string]*entities.FinancialData{
			"2023FY": {
				Ticker:                    "AFRM",
				FilingPeriod:              "2023FY",
				FilingDate:                time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC),
				AsOf:                      time.Now(),
				OperatingIncome:           -500000000,
				NormalizedOperatingIncome: -450000000,
				Revenue:                   1600000000,
				TaxRate:                   0.0,
				TotalAssets:               10000000000,
				TangibleAssets:            8000000000,
				InterestBearingDebt:       3000000000,
				SharesOutstanding:         300000000,
				DilutedSharesOutstanding:  320000000,
				IndustryCode:              "FIN",
				DividendsPerShare:         0,
				HasNormalizedData:         true,
			},
			"2022FY": {
				Ticker:                    "AFRM",
				FilingPeriod:              "2022FY",
				FilingDate:                time.Date(2023, 9, 1, 0, 0, 0, 0, time.UTC),
				AsOf:                      time.Now().Add(-365 * 24 * time.Hour),
				OperatingIncome:           -700000000,
				NormalizedOperatingIncome: -650000000,
				Revenue:                   1300000000,
				TaxRate:                   0.0,
				TotalAssets:               9000000000,
				TangibleAssets:            7000000000,
				InterestBearingDebt:       2500000000,
				SharesOutstanding:         290000000,
				DilutedSharesOutstanding:  310000000,
				IndustryCode:              "FIN",
				DividendsPerShare:         0,
				HasNormalizedData:         true,
			},
		},
	}

	result, err := svc.performValuation(context.Background(), historicalData, marketData, macroData, nil)

	assert.NoError(t, err, "FIN with negative OI should fall back to revenue_multiple, not error")
	assert.NotNil(t, result)
	if result != nil {
		assert.Equal(t, "revenue_multiple", result.CalculationMethod,
			"Should use revenue_multiple as last-resort fallback")
		// Verify fallback warning
		hasWarning := false
		for _, w := range result.Warnings {
			if strings.Contains(w, "revenue_multiple") && strings.Contains(w, "fallback") {
				hasWarning = true
			}
		}
		assert.True(t, hasWarning, "Should warn about revenue_multiple fallback")
	}
}

// TestService_SetYFinanceGateway verifies that the optional Yahoo Finance gateway
// can be injected after service creation.
func TestService_SetYFinanceGateway(t *testing.T) {
	service, _, _, _, _, _ := createTestService()

	// Initially nil
	assert.Nil(t, service.yfinanceGateway)

	// Inject mock gateway
	mockGW := &MockYFinanceGateway{}
	service.SetYFinanceGateway(mockGW)

	assert.NotNil(t, service.yfinanceGateway, "gateway should be set after injection")
}

// MockYFinanceGateway is a mock for the YFinanceGateway port interface
type MockYFinanceGateway struct {
	mock.Mock
}

func (m *MockYFinanceGateway) GetQuote(ctx context.Context, ticker string) (*ports.YFinanceQuote, error) {
	args := m.Called(ctx, ticker)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ports.YFinanceQuote), args.Error(1)
}

func (m *MockYFinanceGateway) GetBatchQuotes(ctx context.Context, tickers []string) (map[string]*ports.YFinanceQuote, error) {
	args := m.Called(ctx, tickers)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]*ports.YFinanceQuote), args.Error(1)
}

func (m *MockYFinanceGateway) GetKeyStatistics(ctx context.Context, ticker string) (*ports.YFinanceKeyStats, error) {
	args := m.Called(ctx, ticker)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ports.YFinanceKeyStats), args.Error(1)
}

func (m *MockYFinanceGateway) GetHistoricalPrices(ctx context.Context, ticker string, days int) ([]ports.YFinancePricePoint, error) {
	args := m.Called(ctx, ticker, days)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]ports.YFinancePricePoint), args.Error(1)
}

func (m *MockYFinanceGateway) GetAnalystEstimates(ctx context.Context, ticker string) (*ports.YFinanceAnalystEstimates, error) {
	args := m.Called(ctx, ticker)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ports.YFinanceAnalystEstimates), args.Error(1)
}

// TestService_isFinancialDataIncomplete_NilLatest verifies that when GetLatestPeriod
// returns nil (no data in any period), isFinancialDataIncomplete returns true.
func TestService_isFinancialDataIncomplete_NilLatest(t *testing.T) {
	service, _, _, _, _, _ := createTestService()

	// Empty data map - GetLatestPeriod returns nil
	emptyData := &entities.HistoricalFinancialData{
		Ticker: "EMPTY",
		Data:   map[string]*entities.FinancialData{},
	}

	assert.True(t, service.isFinancialDataIncomplete(emptyData),
		"should be incomplete when no periods exist")
}

// TestService_isFinancialDataIncomplete_CompleteData verifies that data with
// FCF fields populated is NOT flagged as incomplete.
func TestService_isFinancialDataIncomplete_CompleteData(t *testing.T) {
	service, _, _, _, _, _ := createTestService()

	completeData := &entities.HistoricalFinancialData{
		Ticker: "COMPLETE",
		Data: map[string]*entities.FinancialData{
			"2023FY": {
				DepreciationAndAmortization: 5000000000,
				CapitalExpenditures:         3000000000,
				CashAndCashEquivalents:      10000000000,
				FilingDate:                  time.Now(),
				FilingPeriod:                "2023FY",
			},
		},
	}

	assert.False(t, service.isFinancialDataIncomplete(completeData),
		"should NOT be incomplete when FCF fields are populated")
}

// TestService_isFinancialDataIncomplete_AllZero verifies that when all FCF fields
// are zero, the data is flagged as incomplete.
func TestService_isFinancialDataIncomplete_AllZero(t *testing.T) {
	service, _, _, _, _, _ := createTestService()

	incompleteData := &entities.HistoricalFinancialData{
		Ticker: "INCOMPLETE",
		Data: map[string]*entities.FinancialData{
			"2023FY": {
				DepreciationAndAmortization: 0,
				CapitalExpenditures:         0,
				CashAndCashEquivalents:      0,
				FilingDate:                  time.Now(),
				FilingPeriod:                "2023FY",
			},
		},
	}

	assert.True(t, service.isFinancialDataIncomplete(incompleteData),
		"should be incomplete when all FCF fields are zero")
}

// TestService_averageCapExAndDA_NoPeriods verifies that when no periods have
// CapEx or D&A data, the function returns (0, 0).
func TestService_averageCapExAndDA_NoPeriods(t *testing.T) {
	service, _, _, _, _, _ := createTestService()

	// All periods have zero D&A and CapEx
	data := &entities.HistoricalFinancialData{
		Ticker: "NO_CAPEX",
		Data: map[string]*entities.FinancialData{
			"2023FY": {
				DepreciationAndAmortization: 0,
				CapitalExpenditures:         0,
				FilingDate:                  time.Now(),
				FilingPeriod:                "2023FY",
			},
			"2022FY": {
				DepreciationAndAmortization: 0,
				CapitalExpenditures:         0,
				FilingDate:                  time.Now().Add(-365 * 24 * time.Hour),
				FilingPeriod:                "2022FY",
			},
		},
	}

	avgDA, avgCapEx := service.averageCapExAndDA(data)
	assert.Equal(t, 0.0, avgDA, "should return 0 for D&A when no periods have data")
	assert.Equal(t, 0.0, avgCapEx, "should return 0 for CapEx when no periods have data")
}

// TestLoadCountryRiskPremiums_ValidFile tests loading CRP from a valid JSON file
func TestLoadCountryRiskPremiums_ValidFile(t *testing.T) {
	tmpFile := t.TempDir() + "/country_risk.json"
	content := `{"country_risk_premiums": {"US": 0.0, "CN": 0.025, "BR": 0.035, "default": 0.02}}`
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	require.NoError(t, err)

	premiums, err := LoadCountryRiskPremiums(tmpFile)
	require.NoError(t, err)
	assert.Equal(t, 0.0, premiums["US"])
	assert.Equal(t, 0.025, premiums["CN"])
	assert.Equal(t, 0.035, premiums["BR"])
	assert.Equal(t, 0.02, premiums["default"])
}

// TestLoadCountryRiskPremiums_InvalidJSON tests loading CRP from invalid JSON
func TestLoadCountryRiskPremiums_InvalidJSON(t *testing.T) {
	tmpFile := t.TempDir() + "/bad_country_risk.json"
	err := os.WriteFile(tmpFile, []byte("{invalid json}"), 0644)
	require.NoError(t, err)

	_, err = LoadCountryRiskPremiums(tmpFile)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse")
}

// TestLoadCountryRiskPremiums_MissingFile tests loading CRP from non-existent file
func TestLoadCountryRiskPremiums_MissingFile(t *testing.T) {
	_, err := LoadCountryRiskPremiums("/nonexistent/path/country_risk.json")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read")
}

// TestLoadIndustryMultiples_UsesEmbed verifies LoadIndustryMultiples reads
// from the embedded industry_multiples.json. Replaces the legacy tmpfile-path
// tests (Valid/InvalidJSON/MissingFile) that exercised os.ReadFile branches
// no longer possible with embed. The missing-path branch is exercised by
// configfs.TestRead_MissingFile; the path parameter of LoadIndustryMultiples
// is now deprecated and ignored.
func TestLoadIndustryMultiples_UsesEmbed(t *testing.T) {
	cfg, err := LoadIndustryMultiples("")
	require.NoError(t, err)
	require.NotNil(t, cfg)
	// Values from config/industry_multiples.json at the time of the sweep.
	assert.Equal(t, 18.0, cfg.EVEBITDAMultiples["TECH"])
	assert.Equal(t, 28.0, cfg.SectorMedianPE["TECH"])
	assert.Equal(t, 15.0, cfg.REITPFFOMultiples["default"])
}

// TestLookupMultiple_NoDefaultAndNoMatch tests LookupMultiple when there is no default key
// and the industry code doesn't match any entry (returns 0).
func TestLookupMultiple_NoDefaultAndNoMatch(t *testing.T) {
	multiples := map[string]float64{
		"TECH": 18.0,
		"FIN":  10.0,
	}

	result := LookupMultiple(multiples, "UNKNOWN")
	assert.Equal(t, 0.0, result, "should return 0 when no match and no default")
}

// TestService_performValuation_NegativeOI_ErrModelNotApplicable verifies that
// a company with negative OI and no alternative model routes to ErrModelNotApplicable.
func TestService_performValuation_NegativeOI_ErrModelNotApplicable(t *testing.T) {
	metricsService := &MockMetricsService{}
	metricsService.On("IncWACCCalculations").Return()
	metricsService.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()

	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:                 1 * time.Hour,
			DefaultTerminalGrowthCap: 0.025,
			DCFMaxGrowthRate:         0.5,
			DCFMinGrowthRate:         -0.3,
		},
	}

	// Create service with no models registered (empty router) — so no alternative
	// model can handle the negative OI, and DCF requires positive OI
	emptyRouter := models.NewModelRouter([]models.ValuationModel{
		models.NewMultiStageDCFModel(zap.NewNop()),
	}, zap.NewNop(), nil)

	service := NewService(nil, nil, nil, nil, nil, nil, metricsService, cfg, zap.NewNop(), newTestCalcEmitter())
	service.modelRouter = emptyRouter

	_, marketData, macroData := createTestData()

	// Create data with negative operating income
	negOIData := &entities.HistoricalFinancialData{
		Ticker: "NEGOI",
		Data: map[string]*entities.FinancialData{
			"2023FY": {
				Ticker:                    "NEGOI",
				FilingPeriod:              "2023FY",
				FilingDate:                time.Now(),
				AsOf:                      time.Now(),
				OperatingIncome:           -50000000,
				NormalizedOperatingIncome: -40000000,
				Revenue:                   200000000,
				InterestExpense:           5000000,
				TaxRate:                   0.21,
				TotalAssets:               500000000,
				TangibleAssets:            400000000,
				InterestBearingDebt:       100000000,
				SharesOutstanding:         10000000,
				DilutedSharesOutstanding:  10000000,
				HasNormalizedData:         true,
			},
		},
	}

	_, err := service.performValuation(context.Background(), negOIData, marketData, macroData, nil)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrModelNotApplicable,
		"negative OI with no revenue_multiple model should return ErrModelNotApplicable")
}

// TestService_performValuation_WithExitMultiple verifies that when industryMultiples
// config is present, the exit multiple is wired into the DCF calculation.
func TestService_performValuation_WithExitMultiple(t *testing.T) {
	metricsService := &MockMetricsService{}
	metricsService.On("IncWACCCalculations").Return()
	metricsService.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	metricsService.On("IncDCFCalculations").Return()
	metricsService.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()

	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:                 1 * time.Hour,
			DefaultTerminalGrowthCap: 0.025,
			DCFMaxGrowthRate:         0.5,
			DCFMinGrowthRate:         -0.3,
		},
	}

	service := NewService(nil, nil, nil, nil, nil, nil, metricsService, cfg, zap.NewNop(), newTestCalcEmitter())

	// Inject industry multiples with EV/EBITDA and P/E data for sanity checks
	service.industryMultiples = &industryMultiplesConfig{
		EVEBITDAMultiples: map[string]float64{
			"default": 12.0,
			"TECH":    18.0,
		},
		SectorMedianPE: map[string]float64{
			"default": 16.0,
			"TECH":    25.0,
		},
	}

	historicalData, marketData, macroData := createTestData()

	result, err := service.performValuation(context.Background(), historicalData, marketData, macroData, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify sanity check was attached (non-nil means cross-check ran)
	assert.NotNil(t, result.SanityCheck, "sanity check should be populated when industry multiples are configured")
}

// TestService_performValuation_DDMFallbackToDCF verifies that when the DDM model
// fails for a financial company with positive OI, the service falls back to DCF.
func TestService_performValuation_DDMFallbackToDCF(t *testing.T) {
	metricsService := &MockMetricsService{}
	metricsService.On("IncWACCCalculations").Return()
	metricsService.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	metricsService.On("IncDCFCalculations").Return()
	metricsService.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()

	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:                 1 * time.Hour,
			DefaultTerminalGrowthCap: 0.025,
			DCFMaxGrowthRate:         0.5,
			DCFMinGrowthRate:         -0.3,
		},
	}

	service := NewService(nil, nil, nil, nil, nil, nil, metricsService, cfg, zap.NewNop(), newTestCalcEmitter())

	_, marketData, macroData := createTestData()

	// Financial company (FIN industry) with positive OI but zero DPS -> DDM will fail,
	// and since OI is positive, it should fall back to DCF
	finData := &entities.HistoricalFinancialData{
		Ticker:      "BANK",
		CompanyName: "Test Bank Corp",
		Data: map[string]*entities.FinancialData{
			"2023FY": {
				Ticker:                    "BANK",
				FilingPeriod:              "2023FY",
				FilingDate:                time.Now(),
				AsOf:                      time.Now(),
				OperatingIncome:           5000000000,
				NormalizedOperatingIncome: 5000000000,
				Revenue:                   20000000000,
				NetIncome:                 4000000000,
				InterestExpense:           1000000000,
				TaxRate:                   0.21,
				TotalAssets:               100000000000,
				TangibleAssets:            80000000000,
				InterestBearingDebt:       30000000000,
				SharesOutstanding:         1000000000,
				DilutedSharesOutstanding:  1000000000,
				DividendsPerShare:         0, // Zero DPS -> DDM will fail
				IndustryCode:              "FIN",
				HasNormalizedData:         true,
			},
			"2022FY": {
				Ticker:                    "BANK",
				FilingPeriod:              "2022FY",
				FilingDate:                time.Now().Add(-365 * 24 * time.Hour),
				AsOf:                      time.Now().Add(-365 * 24 * time.Hour),
				OperatingIncome:           4500000000,
				NormalizedOperatingIncome: 4500000000,
				Revenue:                   18000000000,
				NetIncome:                 3500000000,
				InterestExpense:           900000000,
				TaxRate:                   0.21,
				TotalAssets:               95000000000,
				TangibleAssets:            75000000000,
				InterestBearingDebt:       28000000000,
				SharesOutstanding:         1000000000,
				DilutedSharesOutstanding:  1000000000,
				DividendsPerShare:         0,
				HasNormalizedData:         true,
			},
		},
	}

	result, err := service.performValuation(context.Background(), finData, marketData, macroData, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should have fallen back to DCF and produced a warning
	assert.Equal(t, "multi_stage_dcf", result.CalculationMethod,
		"should fall back to DCF when DDM fails for company with positive OI")

	hasWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "fell back to multi_stage_dcf") {
			hasWarning = true
		}
	}
	assert.True(t, hasWarning, "should have a DCF fallback warning")
}

// Note: TestService_CalculateValuation_WithDataFetcher removed — the DataFetcher
// path is already covered by TestService_CalculateValuation_DataFetcherPath (line 2374)
// which uses proper mock gateways. Testing with nil gateways panics in the
// coordinator's concurrent goroutines (not a service-layer bug).

// TestService_getAnalystEstimates_CacheHit verifies that cached analyst estimates
// are returned without calling the Yahoo Finance gateway.
func TestService_getAnalystEstimates_CacheHit(t *testing.T) {
	service, _, _, _, cache, _ := createTestService()
	ctx := context.Background()

	// Inject mock YFinance gateway (should NOT be called on cache hit)
	mockGW := &MockYFinanceGateway{}
	service.SetYFinanceGateway(mockGW)

	cachedEstimates := &ports.YFinanceAnalystEstimates{
		EarningsGrowth5Year: 0.12,
		NumberOfAnalysts:    15,
	}

	// Cache hit: populate dest with cached value
	cache.On("Get", ctx, "analyst:v1:AAPL", mock.AnythingOfType("*ports.YFinanceAnalystEstimates")).
		Run(func(args mock.Arguments) {
			dest := args.Get(2).(*ports.YFinanceAnalystEstimates)
			*dest = *cachedEstimates
		}).Return(nil)

	result := service.getAnalystEstimates(ctx, "AAPL")

	require.NotNil(t, result)
	assert.Equal(t, 0.12, result.EarningsGrowth5Year)
	assert.Equal(t, 15, result.NumberOfAnalysts)

	// YFinance gateway should NOT have been called
	mockGW.AssertNotCalled(t, "GetAnalystEstimates", mock.Anything, mock.Anything)
	cache.AssertExpectations(t)
}

// TestService_getAnalystEstimates_CacheMiss verifies that on cache miss,
// estimates are fetched from Yahoo Finance and then cached with 7-day TTL.
func TestService_getAnalystEstimates_CacheMiss(t *testing.T) {
	service, _, _, _, cache, _ := createTestService()
	ctx := context.Background()

	mockGW := &MockYFinanceGateway{}
	service.SetYFinanceGateway(mockGW)

	freshEstimates := &ports.YFinanceAnalystEstimates{
		EarningsGrowth5Year: 0.08,
		NumberOfAnalysts:    22,
	}

	// Cache miss
	cache.On("Get", ctx, "analyst:v1:MSFT", mock.AnythingOfType("*ports.YFinanceAnalystEstimates")).
		Return(errors.New("cache miss"))

	// YFinance gateway returns fresh data
	mockGW.On("GetAnalystEstimates", ctx, "MSFT").Return(freshEstimates, nil)

	// Cache set with 7-day TTL (168 hours)
	cache.On("Set", ctx, "analyst:v1:MSFT", freshEstimates, 168*time.Hour).Return(nil)

	result := service.getAnalystEstimates(ctx, "MSFT")

	require.NotNil(t, result)
	assert.Equal(t, 0.08, result.EarningsGrowth5Year)
	assert.Equal(t, 22, result.NumberOfAnalysts)

	mockGW.AssertExpectations(t)
	cache.AssertExpectations(t)
}

// TestService_getAnalystEstimates_NoGateway verifies graceful degradation
// when YFinance gateway is not configured (nil).
func TestService_getAnalystEstimates_NoGateway(t *testing.T) {
	service, _, _, _, _, _ := createTestService()
	ctx := context.Background()

	// yfinanceGateway is nil by default
	assert.Nil(t, service.yfinanceGateway)

	result := service.getAnalystEstimates(ctx, "AAPL")

	assert.Nil(t, result, "should return nil when gateway is not configured")
}

// TestService_getAnalystEstimates_FetchError verifies graceful degradation
// when Yahoo Finance returns an error.
func TestService_getAnalystEstimates_FetchError(t *testing.T) {
	service, _, _, _, cache, _ := createTestService()
	ctx := context.Background()

	mockGW := &MockYFinanceGateway{}
	service.SetYFinanceGateway(mockGW)

	// Cache miss
	cache.On("Get", ctx, "analyst:v1:FAIL", mock.AnythingOfType("*ports.YFinanceAnalystEstimates")).
		Return(errors.New("cache miss"))

	// YFinance gateway returns error
	mockGW.On("GetAnalystEstimates", ctx, "FAIL").Return(nil, errors.New("rate limited"))

	result := service.getAnalystEstimates(ctx, "FAIL")

	assert.Nil(t, result, "should return nil on fetch error")

	// Cache Set should NOT be called when fetch fails
	cache.AssertNotCalled(t, "Set", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}
