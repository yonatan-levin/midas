package datafetcher

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock implementations for testing
type mockSECGateway struct {
	financialData *entities.FinancialData
	err           error
	callCount     int
}

func (m *mockSECGateway) GetCompanyFacts(ctx context.Context, cik string) (*entities.FinancialData, error) {
	m.callCount++
	return m.financialData, m.err
}

func (m *mockSECGateway) GetCompanyConcepts(ctx context.Context, cik string) (map[string]interface{}, error) {
	return nil, errors.New("not implemented")
}

type mockMarketDataGateway struct {
	marketData *entities.MarketData
	err        error
	callCount  int
}

func (m *mockMarketDataGateway) GetMarketData(ctx context.Context, ticker string) (*entities.MarketData, error) {
	m.callCount++
	return m.marketData, m.err
}

type mockMacroDataGateway struct {
	macroData *entities.MacroData
	err       error
	callCount int
}

func (m *mockMacroDataGateway) GetRiskFreeRate(ctx context.Context) (float64, error) {
	m.callCount++
	if m.err != nil {
		return 0, m.err
	}
	return m.macroData.RiskFreeRate, nil
}

func (m *mockMacroDataGateway) GetMarketRiskPremium(ctx context.Context) (float64, error) {
	return m.macroData.MarketRiskPremium, m.err
}

type mockCacheRepository struct {
	data      map[string]interface{}
	err       error
	callCount int
}

func (m *mockCacheRepository) Get(ctx context.Context, key string) (interface{}, error) {
	m.callCount++
	if m.err != nil {
		return nil, m.err
	}
	return m.data[key], nil
}

func (m *mockCacheRepository) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	if m.data == nil {
		m.data = make(map[string]interface{})
	}
	m.data[key] = value
	return m.err
}

func (m *mockCacheRepository) Delete(ctx context.Context, key string) error {
	delete(m.data, key)
	return m.err
}

func (m *mockCacheRepository) Exists(ctx context.Context, key string) (bool, error) {
	_, exists := m.data[key]
	return exists, m.err
}

func (m *mockCacheRepository) GetMultiple(ctx context.Context, keys []string) (map[string]interface{}, error) {
	result := make(map[string]interface{})
	for _, key := range keys {
		if val, exists := m.data[key]; exists {
			result[key] = val
		}
	}
	return result, m.err
}

// TestNewDataFetcher tests DataFetcher creation
func TestNewDataFetcher(t *testing.T) {
	secGateway := &mockSECGateway{}
	marketGateway := &mockMarketDataGateway{}
	macroGateway := &mockMacroDataGateway{}
	cacheRepo := &mockCacheRepository{}

	fetcher := NewDataFetcher(secGateway, marketGateway, macroGateway, cacheRepo)

	assert.NotNil(t, fetcher)
	assert.NotNil(t, fetcher.config)
	assert.NotNil(t, fetcher.validator)
	assert.NotNil(t, fetcher.coordinator)
	assert.True(t, fetcher.config.EnableCaching)
	assert.Equal(t, 24*time.Hour, fetcher.config.CacheTTL)
}

// TestDataFetcher_FetchComprehensiveData tests comprehensive data fetching
func TestDataFetcher_FetchComprehensiveData(t *testing.T) {
	tests := []struct {
		name           string
		request        *FetchRequest
		setupMocks     func(*mockSECGateway, *mockMarketDataGateway, *mockMacroDataGateway, *mockCacheRepository)
		expectError    bool
		expectSuccess  bool
		expectSources  int
		expectDuration time.Duration
	}{
		{
			name: "successful_comprehensive_fetch",
			request: &FetchRequest{
				Ticker:          "AAPL",
				CIK:             "0000320193",
				DataSources:     []DataSource{SECSource, MarketSource, MacroSource},
				ValidationLevel: ValidationBasic,
			},
			setupMocks: func(sec *mockSECGateway, market *mockMarketDataGateway, macro *mockMacroDataGateway, cache *mockCacheRepository) {
				sec.financialData = &entities.FinancialData{
					Ticker:            "AAPL",
					TotalAssets:       1000000,
					Revenue:           500000,
					SharesOutstanding: 1000,
				}
				market.marketData = &entities.MarketData{
					Ticker:     "AAPL",
					SharePrice: 150.0,
					Beta:       1.2,
				}
				macro.macroData = &entities.MacroData{
					RiskFreeRate:      0.045,
					MarketRiskPremium: 0.05,
				}
			},
			expectError:    false,
			expectSuccess:  true,
			expectSources:  3,
			expectDuration: 100 * time.Millisecond,
		},
		{
			name:        "nil_request_error",
			request:     nil,
			expectError: true,
		},
		{
			name: "empty_ticker_error",
			request: &FetchRequest{
				Ticker: "",
			},
			expectError: true,
		},
		{
			name: "partial_success_with_errors",
			request: &FetchRequest{
				Ticker:          "MSFT",
				DataSources:     []DataSource{SECSource, MarketSource},
				ValidationLevel: ValidationBasic,
			},
			setupMocks: func(sec *mockSECGateway, market *mockMarketDataGateway, macro *mockMacroDataGateway, cache *mockCacheRepository) {
				sec.financialData = &entities.FinancialData{
					Ticker:            "MSFT",
					TotalAssets:       800000,
					Revenue:           400000,
					SharesOutstanding: 800,
				}
				market.err = errors.New("market data unavailable")
			},
			expectError:    false, // Should handle partial failures gracefully
			expectSuccess:  false, // But mark as unsuccessful due to missing data
			expectSources:  2,
			expectDuration: 100 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			secGateway := &mockSECGateway{}
			marketGateway := &mockMarketDataGateway{}
			macroGateway := &mockMacroDataGateway{}
			cacheRepo := &mockCacheRepository{}

			if tt.setupMocks != nil {
				tt.setupMocks(secGateway, marketGateway, macroGateway, cacheRepo)
			}

			fetcher := NewDataFetcher(secGateway, marketGateway, macroGateway, cacheRepo)
			ctx := context.Background()

			start := time.Now()

			// Act
			result, err := fetcher.FetchComprehensiveData(ctx, tt.request)

			// Assert
			duration := time.Since(start)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, tt.expectSuccess, result.Success)
				assert.True(t, duration < tt.expectDuration, "Fetch took %v, expected < %v", duration, tt.expectDuration)

				if tt.expectSources > 0 {
					assert.Equal(t, tt.expectSources, len(result.SourceMetadata))
				}
			}
		})
	}
}

// TestDataFetcher_FetchFinancialDataOnly tests SEC-only data fetching
func TestDataFetcher_FetchFinancialDataOnly(t *testing.T) {
	secGateway := &mockSECGateway{
		financialData: &entities.FinancialData{
			Ticker:            "TSLA",
			TotalAssets:       600000,
			Revenue:           300000,
			SharesOutstanding: 600,
		},
	}
	marketGateway := &mockMarketDataGateway{}
	macroGateway := &mockMacroDataGateway{}
	cacheRepo := &mockCacheRepository{}

	fetcher := NewDataFetcher(secGateway, marketGateway, macroGateway, cacheRepo)
	ctx := context.Background()

	result, err := fetcher.FetchFinancialDataOnly(ctx, "TSLA", "0000000000")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "TSLA", result.Ticker)
	assert.Equal(t, float64(600000), result.TotalAssets)
}

// TestDataFetcher_BulkFetch tests bulk data fetching functionality
func TestDataFetcher_BulkFetch(t *testing.T) {
	tests := []struct {
		name        string
		requests    []*FetchRequest
		expectError bool
		expectCount int
		expectTime  time.Duration
	}{
		{
			name:        "empty_requests",
			requests:    []*FetchRequest{},
			expectError: false,
			expectCount: 0,
			expectTime:  10 * time.Millisecond,
		},
		{
			name: "multiple_valid_requests",
			requests: []*FetchRequest{
				{Ticker: "AAPL", ValidationLevel: ValidationBasic},
				{Ticker: "MSFT", ValidationLevel: ValidationBasic},
				{Ticker: "GOOGL", ValidationLevel: ValidationBasic},
			},
			expectError: false,
			expectCount: 3,
			expectTime:  200 * time.Millisecond, // Should be faster than sequential due to concurrency
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			secGateway := &mockSECGateway{
				financialData: &entities.FinancialData{
					TotalAssets:       1000000,
					Revenue:           500000,
					SharesOutstanding: 1000,
				},
			}
			marketGateway := &mockMarketDataGateway{}
			macroGateway := &mockMacroDataGateway{}
			cacheRepo := &mockCacheRepository{}

			fetcher := NewDataFetcher(secGateway, marketGateway, macroGateway, cacheRepo)
			ctx := context.Background()

			start := time.Now()

			// Act
			results, err := fetcher.BulkFetch(ctx, tt.requests)

			// Assert
			duration := time.Since(start)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Len(t, results, tt.expectCount)
				assert.True(t, duration < tt.expectTime, "Bulk fetch took %v, expected < %v", duration, tt.expectTime)
			}
		})
	}
}

// TestDataFetcher_ContextCancellation tests context cancellation handling
func TestDataFetcher_ContextCancellation(t *testing.T) {
	secGateway := &mockSECGateway{}
	marketGateway := &mockMarketDataGateway{}
	macroGateway := &mockMacroDataGateway{}
	cacheRepo := &mockCacheRepository{}

	fetcher := NewDataFetcher(secGateway, marketGateway, macroGateway, cacheRepo)

	// Create context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	request := &FetchRequest{
		Ticker:          "TEST",
		ValidationLevel: ValidationBasic,
	}

	start := time.Now()
	result, err := fetcher.FetchComprehensiveData(ctx, request)
	duration := time.Since(start)

	// Should fail quickly due to context cancellation
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, duration < 50*time.Millisecond, "Should fail quickly on context cancellation")
}

// TestDataFetcher_ValidationLevels tests different validation levels
func TestDataFetcher_ValidationLevels(t *testing.T) {
	tests := []struct {
		name            string
		validationLevel ValidationLevel
		expectValidated bool
	}{
		{
			name:            "no_validation",
			validationLevel: ValidationNone,
			expectValidated: false,
		},
		{
			name:            "basic_validation",
			validationLevel: ValidationBasic,
			expectValidated: true,
		},
		{
			name:            "strict_validation",
			validationLevel: ValidationStrict,
			expectValidated: true,
		},
		{
			name:            "critical_validation",
			validationLevel: ValidationCritical,
			expectValidated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			secGateway := &mockSECGateway{
				financialData: &entities.FinancialData{
					Ticker:            "VALIDATION_TEST",
					TotalAssets:       1000000,
					Revenue:           500000,
					SharesOutstanding: 1000,
				},
			}
			marketGateway := &mockMarketDataGateway{}
			macroGateway := &mockMacroDataGateway{}
			cacheRepo := &mockCacheRepository{}

			fetcher := NewDataFetcher(secGateway, marketGateway, macroGateway, cacheRepo)
			ctx := context.Background()

			request := &FetchRequest{
				Ticker:          "VALIDATION_TEST",
				ValidationLevel: tt.validationLevel,
			}

			// Act
			result, err := fetcher.FetchComprehensiveData(ctx, request)

			// Assert
			assert.NoError(t, err)
			assert.NotNil(t, result)

			if tt.expectValidated {
				assert.NotNil(t, result.QualityReport, "Should have quality report when validation is enabled")
			} else {
				assert.Nil(t, result.QualityReport, "Should not have quality report when validation is disabled")
			}
		})
	}
}

// TestDataFetcher_Health tests health check functionality
func TestDataFetcher_Health(t *testing.T) {
	secGateway := &mockSECGateway{}
	marketGateway := &mockMarketDataGateway{}
	macroGateway := &mockMacroDataGateway{}
	cacheRepo := &mockCacheRepository{}

	fetcher := NewDataFetcher(secGateway, marketGateway, macroGateway, cacheRepo)
	ctx := context.Background()

	health, err := fetcher.GetHealth(ctx)

	assert.NoError(t, err)
	assert.NotNil(t, health)
	assert.Equal(t, 3, len(health)) // Should check all three sources
	assert.True(t, health[SECSource])
	assert.True(t, health[MarketSource])
	assert.True(t, health[MacroSource])
}

// TestDataFetcher_Metrics tests metrics collection
func TestDataFetcher_Metrics(t *testing.T) {
	secGateway := &mockSECGateway{}
	marketGateway := &mockMarketDataGateway{}
	macroGateway := &mockMacroDataGateway{}
	cacheRepo := &mockCacheRepository{}

	fetcher := NewDataFetcher(secGateway, marketGateway, macroGateway, cacheRepo)

	metrics := fetcher.GetMetrics()

	assert.NotNil(t, metrics)
	assert.NotNil(t, metrics.SourceLatencies)
	assert.GreaterOrEqual(t, metrics.TotalRequests, int64(0))
	assert.GreaterOrEqual(t, metrics.CacheHitRate, 0.0)
	assert.LessOrEqual(t, metrics.CacheHitRate, 1.0)
}

// TestDataFetcher_Configuration tests custom configuration
func TestDataFetcher_Configuration(t *testing.T) {
	config := &DataFetcherConfig{
		EnableCaching:        false,
		CacheTTL:             1 * time.Hour,
		ConcurrentFetching:   false,
		MaxRetries:           5,
		TimeoutDuration:      10 * time.Second,
		ValidateCompleteness: false,
		RequiredFields:       []string{"TotalAssets"},
	}

	secGateway := &mockSECGateway{}
	marketGateway := &mockMarketDataGateway{}
	macroGateway := &mockMacroDataGateway{}
	cacheRepo := &mockCacheRepository{}

	fetcher := NewDataFetcherWithConfig(secGateway, marketGateway, macroGateway, cacheRepo, config)

	assert.NotNil(t, fetcher)
	assert.Equal(t, config, fetcher.config)
	assert.False(t, fetcher.config.EnableCaching)
	assert.Equal(t, 5, fetcher.config.MaxRetries)
	assert.Equal(t, 10*time.Second, fetcher.config.TimeoutDuration)
}

// TestDataFetcher_Performance tests performance requirements
func TestDataFetcher_Performance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	// Setup with fast mock responses
	secGateway := &mockSECGateway{
		financialData: &entities.FinancialData{
			Ticker:            "PERF_TEST",
			TotalAssets:       1000000,
			Revenue:           500000,
			SharesOutstanding: 1000,
		},
	}
	marketGateway := &mockMarketDataGateway{
		marketData: &entities.MarketData{
			Ticker:     "PERF_TEST",
			SharePrice: 100.0,
			Beta:       1.0,
		},
	}
	macroGateway := &mockMacroDataGateway{
		macroData: &entities.MacroData{
			RiskFreeRate:      0.045,
			MarketRiskPremium: 0.05,
		},
	}
	cacheRepo := &mockCacheRepository{}

	fetcher := NewDataFetcher(secGateway, marketGateway, macroGateway, cacheRepo)
	ctx := context.Background()

	request := &FetchRequest{
		Ticker:          "PERF_TEST",
		ValidationLevel: ValidationBasic,
	}

	// Run multiple iterations
	iterations := 100
	var totalDuration time.Duration

	for i := 0; i < iterations; i++ {
		start := time.Now()
		result, err := fetcher.FetchComprehensiveData(ctx, request)
		duration := time.Since(start)

		require.NoError(t, err)
		require.NotNil(t, result)
		totalDuration += duration
	}

	avgDuration := totalDuration / time.Duration(iterations)
	t.Logf("Average fetch duration: %v", avgDuration)

	// KPI: Average fetch should be < 50ms for mocked data
	assert.True(t, avgDuration < 50*time.Millisecond,
		"Average fetch duration %v exceeds 50ms threshold", avgDuration)
}
