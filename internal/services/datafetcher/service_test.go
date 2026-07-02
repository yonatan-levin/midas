package datafetcher

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock implementations for testing
type mockSECGateway struct {
	companyFacts *entities.CompanyFactsResponse
	err          error
	mu           sync.Mutex // guards callCount — the coordinator fans out fetches concurrently (CI-1 / #20)
	callCount    int
}

func (m *mockSECGateway) GetCompanyFacts(ctx context.Context, cik string) (*entities.CompanyFactsResponse, error) {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()
	// Add small delay to simulate real API call for duration tracking
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(10 * time.Millisecond):
		// Continue with normal execution
	}
	return m.companyFacts, m.err
}

func (m *mockSECGateway) GetCompanyConcepts(ctx context.Context, cik string, tag string) (*entities.ConceptResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockSECGateway) GetFinancialDataForTicker(ctx context.Context, ticker, cik string) (*entities.HistoricalFinancialData, error) {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()
	// Simulate API latency for duration tracking
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Millisecond):
	}
	if m.err != nil {
		return nil, m.err
	}
	// If companyFacts is configured, build a single-period HistoricalFinancialData.
	// This keeps existing tests working without needing to restructure their data.
	if m.companyFacts != nil {
		fd := &entities.FinancialData{
			Ticker:      ticker,
			CIK:         m.companyFacts.CIK,
			TotalAssets: 1_000_000, // Default non-zero values for test sufficiency
			Revenue:     500_000,
			FilingDate:  time.Now(),
		}
		return &entities.HistoricalFinancialData{
			Ticker: ticker,
			Data:   map[string]*entities.FinancialData{"FY2023": fd},
		}, nil
	}
	return nil, nil
}

func (m *mockSECGateway) HealthCheck(ctx context.Context) error {
	return nil // Mock always healthy for tests
}

func (m *mockSECGateway) GetTickerCIKMapping(ctx context.Context) (map[string]string, error) {
	// If a predefined error is configured, propagate it (used by error-aggregation tests)
	if m.err != nil {
		return nil, m.err
	}

	// Provide a mapping that covers the most common unit-tests tickers so that
	// coordinator logic can resolve a CIK without failing. The values do not
	// need to be the real SEC CIKs – only stable placeholders.
	mapping := map[string]string{
		"AAPL":             "320193",
		"MSFT":             "789019",
		"TSLA":             "1318605",
		"GOOGL":            "1652044",
		"COORD_TEST":       "1234567890",
		"PARTIAL_FAIL":     "PARTIAL_FAIL",
		"CANCELLED":        "CANCELLED",
		"MODE_TEST":        "MODE_TEST",
		"DEFAULT_TEST":     "DEFAULT_TEST",
		"ERROR_TEST":       "ERROR_TEST",
		"TEST":             "TEST",
		"TEST_INTEGRATION": "TEST_INTEGRATION",
		"TEST_MULTI":       "TEST_MULTI",
		"TEST_PARTIAL":     "TEST_PARTIAL",
		"TEST_FAIL":        "TEST_FAIL",
		"VALIDATION_TEST":  "VALIDATION_TEST",
		"PERF_TEST":        "PERF_TEST",
		"METRICS_TEST":     "METRICS_TEST",
		"NOCACHE_TEST":     "NOCACHE_TEST",
		"COMPLETE_TEST":    "COMPLETE_TEST",
	}

	// If the mock has companyFacts with a distinct CIK we attempt to add a
	// reverse mapping assuming the caller will supply ticker of that facts.
	if m.companyFacts != nil && m.companyFacts.CIK != "" {
		// Attempt a heuristic: use the first 10 chars of CIK as ticker key if
		// no match exists. This keeps mapping deterministic for the tests that
		// set an explicit CIK like "1234567890" but expect their custom ticker
		// (e.g. COORD_TEST) to resolve via explicit entry above.
		// We skip adding if key already exists to avoid overwriting.
		if _, exists := mapping[m.companyFacts.CIK]; !exists {
			mapping[m.companyFacts.CIK] = m.companyFacts.CIK
		}
	}

	return mapping, nil
}

type mockMarketDataGateway struct {
	marketData *entities.MarketData
	err        error
	mu         sync.Mutex // guards callCount — concurrent coordinator fan-out (CI-1 / #20)
	callCount  int
}

func (m *mockMarketDataGateway) GetQuote(ctx context.Context, ticker string) (*entities.MarketData, error) {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()
	// Add small delay to simulate real API call for duration tracking
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(8 * time.Millisecond):
		// Continue with normal execution
	}
	return m.marketData, m.err
}

func (m *mockMarketDataGateway) GetQuotes(ctx context.Context, tickers []string) (map[string]*entities.MarketData, error) {
	result := make(map[string]*entities.MarketData)
	for _, ticker := range tickers {
		result[ticker] = m.marketData
	}
	return result, m.err
}

func (m *mockMarketDataGateway) GetHistoricalPrices(ctx context.Context, ticker string, startDate, endDate time.Time) ([]*entities.PriceData, error) {
	// Return mock historical prices
	prices := []*entities.PriceData{
		{
			Ticker:   ticker,
			Date:     time.Now().Add(-24 * time.Hour),
			Open:     150.0,
			High:     155.0,
			Low:      149.0,
			Close:    152.0,
			Volume:   1000000,
			AdjClose: 152.0,
		},
	}
	return prices, m.err
}

func (m *mockMarketDataGateway) HealthCheck(ctx context.Context) error {
	return nil // Mock always healthy for tests
}

type mockMacroDataGateway struct {
	macroData *entities.MacroData
	err       error
	mu        sync.Mutex // guards callCount — concurrent coordinator fan-out (CI-1 / #20)
	callCount int
}

func (m *mockMacroDataGateway) GetTreasuryRates(ctx context.Context) (*entities.TreasuryRates, error) {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()
	// Add small delay to simulate real API call for duration tracking
	time.Sleep(5 * time.Millisecond)
	treasuryRates := &entities.TreasuryRates{
		AsOf:        time.Now(),
		Yield10Year: 0.045, // 4.5%
		Yield5Year:  0.040, // 4.0%
		Yield2Year:  0.035, // 3.5%
	}
	return treasuryRates, m.err
}

func (m *mockMacroDataGateway) GetMarketRiskPremium(ctx context.Context) (float64, error) {
	// Add small delay to simulate real API call for duration tracking
	time.Sleep(3 * time.Millisecond)
	if m.macroData != nil {
		return m.macroData.MarketRiskPremium, m.err
	}
	return 0.05, m.err // Default 5% market risk premium
}

// GetFXRate is a no-op stub. DataFetcher tests don't exercise the FX path
// (Phase B9 will be the first consumer); identity 1.0 keeps the contract
// satisfied without spreading FX setup boilerplate across unrelated tests.
func (m *mockMacroDataGateway) GetFXRate(_ context.Context, _, _ string) (float64, error) {
	return 1.0, nil
}

func (m *mockMacroDataGateway) HealthCheck(ctx context.Context) error {
	return nil // Mock always healthy for tests
}

type mockCacheRepository struct {
	data      map[string]interface{}
	err       error
	callCount int
	mu        sync.Mutex
}

func (m *mockCacheRepository) Get(ctx context.Context, key string, dest interface{}) error {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	_, exists := m.data[key]
	m.mu.Unlock()
	if exists {
		// Simple mock - just return success for cache hit
		return nil
	}
	return errors.New("cache miss")
}

func (m *mockCacheRepository) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	m.mu.Lock()
	if m.data == nil {
		m.data = make(map[string]interface{})
	}
	m.data[key] = value
	m.mu.Unlock()
	return m.err
}

func (m *mockCacheRepository) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	delete(m.data, key)
	m.mu.Unlock()
	return m.err
}

func (m *mockCacheRepository) Exists(ctx context.Context, key string) (bool, error) {
	m.mu.Lock()
	_, exists := m.data[key]
	m.mu.Unlock()
	return exists, m.err
}

func (m *mockCacheRepository) SetNX(ctx context.Context, key string, value interface{}, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.data[key]; exists {
		return false, m.err
	}
	m.data[key] = value
	return true, m.err
}

func (m *mockCacheRepository) GetKeys(ctx context.Context, pattern string) ([]string, error) {
	m.mu.Lock()
	keys := make([]string, 0, len(m.data))
	for key := range m.data {
		keys = append(keys, key)
	}
	m.mu.Unlock()
	return keys, m.err
}

func (m *mockCacheRepository) DeletePattern(ctx context.Context, pattern string) error {
	// Simple mock - delete all keys for testing
	m.mu.Lock()
	for key := range m.data {
		delete(m.data, key)
	}
	m.mu.Unlock()
	return m.err
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
	assert.True(t, fetcher.config.ConcurrentFetching)
}

// TestDataFetcher_Fetch tests comprehensive data fetching
func TestDataFetcher_Fetch(t *testing.T) {
	tests := []struct {
		name           string
		request        *entities.FetchRequest
		setupMocks     func(*mockSECGateway, *mockMarketDataGateway, *mockMacroDataGateway, *mockCacheRepository)
		expectError    bool
		expectSuccess  bool
		expectSources  int
		expectDuration time.Duration
	}{
		{
			name: "successful_comprehensive_fetch",
			request: &entities.FetchRequest{
				Ticker:          "AAPL",
				CIK:             "0000320193",
				DataSources:     []entities.DataSource{entities.SECSource, entities.MarketSource, entities.MacroSource},
				ValidationLevel: entities.ValidationBasic,
			},
			setupMocks: func(sec *mockSECGateway, market *mockMarketDataGateway, macro *mockMacroDataGateway, cache *mockCacheRepository) {
				sec.companyFacts = &entities.CompanyFactsResponse{
					CIK:        "0000320193",
					EntityName: "Apple Inc",
					Facts: map[string]interface{}{
						"Assets": map[string]interface{}{
							"units": map[string]interface{}{
								"USD": []interface{}{
									map[string]interface{}{
										"val":   1000000000,
										"fy":    2023,
										"form":  "10-K",
										"end":   "2023-09-30",
										"frame": "CY2023Q3",
									},
								},
							},
						},
						"Revenues": map[string]interface{}{
							"units": map[string]interface{}{
								"USD": []interface{}{
									map[string]interface{}{
										"val":   500000000,
										"fy":    2023,
										"form":  "10-K",
										"end":   "2023-09-30",
										"frame": "CY2023Q3",
									},
								},
							},
						},
					},
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
			request: &entities.FetchRequest{
				Ticker: "",
			},
			expectError: true,
		},
		{
			name: "partial_success_with_errors",
			request: &entities.FetchRequest{
				Ticker:          "MSFT",
				DataSources:     []entities.DataSource{entities.SECSource, entities.MarketSource},
				ValidationLevel: entities.ValidationBasic,
			},
			setupMocks: func(sec *mockSECGateway, market *mockMarketDataGateway, macro *mockMacroDataGateway, cache *mockCacheRepository) {
				sec.companyFacts = &entities.CompanyFactsResponse{
					CIK:        "0000789019",
					EntityName: "Microsoft Corporation",
					Facts: map[string]interface{}{
						"Assets": map[string]interface{}{
							"units": map[string]interface{}{
								"USD": []interface{}{
									map[string]interface{}{
										"val":   800000000,
										"fy":    2023,
										"form":  "10-K",
										"end":   "2023-09-30",
										"frame": "CY2023Q3",
									},
								},
							},
						},
					},
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
			result, err := fetcher.Fetch(ctx, tt.request)

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
		companyFacts: &entities.CompanyFactsResponse{
			CIK:        "0000000000",
			EntityName: "Test Company",
			Facts: map[string]interface{}{
				"Assets": map[string]interface{}{
					"units": map[string]interface{}{
						"USD": []interface{}{
							map[string]interface{}{
								"val":   1000000,
								"fy":    2023,
								"form":  "10-K",
								"end":   "2023-09-30",
								"frame": "CY2023Q3",
							},
						},
					},
				},
				"Revenues": map[string]interface{}{
					"units": map[string]interface{}{
						"USD": []interface{}{
							map[string]interface{}{
								"val":   500000,
								"fy":    2023,
								"form":  "10-K",
								"end":   "2023-09-30",
								"frame": "CY2023Q3",
							},
						},
					},
				},
			},
		},
	}
	marketGateway := &mockMarketDataGateway{}
	macroGateway := &mockMacroDataGateway{}
	cacheRepo := &mockCacheRepository{}

	fetcher := NewDataFetcher(secGateway, marketGateway, macroGateway, cacheRepo)
	ctx := context.Background()

	request := &entities.FetchRequest{
		Ticker:          "TEST",
		CIK:             "0000000000",
		DataSources:     []entities.DataSource{entities.SECSource},
		ValidationLevel: entities.ValidationBasic,
	}
	result, err := fetcher.Fetch(ctx, request)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "TEST", result.Ticker)
	// Note: FinancialData structure doesn't have TotalAssets directly
	assert.NotNil(t, result.FinancialData)
}

// TestDataFetcher_BulkFetch tests bulk data fetching functionality
func TestDataFetcher_BulkFetch(t *testing.T) {
	tests := []struct {
		name        string
		requests    []*entities.FetchRequest
		expectError bool
		expectCount int
		expectTime  time.Duration
	}{
		{
			name:        "empty_requests",
			requests:    []*entities.FetchRequest{},
			expectError: false,
			expectCount: 0,
			expectTime:  10 * time.Millisecond,
		},
		{
			name: "multiple_valid_requests",
			requests: []*entities.FetchRequest{
				{Ticker: "AAPL", ValidationLevel: entities.ValidationBasic},
				{Ticker: "MSFT", ValidationLevel: entities.ValidationBasic},
				{Ticker: "GOOGL", ValidationLevel: entities.ValidationBasic},
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
				companyFacts: &entities.CompanyFactsResponse{
					CIK:        "0000000000",
					EntityName: "Test Company",
					Facts: map[string]interface{}{
						"Assets": map[string]interface{}{
							"units": map[string]interface{}{
								"USD": []interface{}{
									map[string]interface{}{
										"val":   1000000,
										"fy":    2023,
										"form":  "10-K",
										"end":   "2023-09-30",
										"frame": "CY2023Q3",
									},
								},
							},
						},
						"Revenues": map[string]interface{}{
							"units": map[string]interface{}{
								"USD": []interface{}{
									map[string]interface{}{
										"val":   500000,
										"fy":    2023,
										"form":  "10-K",
										"end":   "2023-09-30",
										"frame": "CY2023Q3",
									},
								},
							},
						},
					},
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

	request := &entities.FetchRequest{
		Ticker:          "TEST",
		ValidationLevel: entities.ValidationBasic,
	}

	start := time.Now()
	result, err := fetcher.Fetch(ctx, request)
	duration := time.Since(start)

	// Should fail quickly due to context cancellation but return a result with errors
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.False(t, result.Success, "Should not be successful due to context cancellation")
	assert.True(t, len(result.Errors) > 0, "Should have errors due to context cancellation")
	assert.True(t, duration < 50*time.Millisecond, "Should fail quickly on context cancellation")
}

// TestDataFetcher_ValidationLevels tests different validation levels
func TestDataFetcher_ValidationLevels(t *testing.T) {
	tests := []struct {
		name            string
		validationLevel entities.ValidationLevel
		expectValidated bool
	}{
		{
			name:            "no_validation",
			validationLevel: entities.ValidationNone,
			expectValidated: false,
		},
		{
			name:            "basic_validation",
			validationLevel: entities.ValidationBasic,
			expectValidated: true,
		},
		{
			name:            "strict_validation",
			validationLevel: entities.ValidationStrict,
			expectValidated: true,
		},
		{
			name:            "critical_validation",
			validationLevel: entities.ValidationCritical,
			expectValidated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			secGateway := &mockSECGateway{
				companyFacts: &entities.CompanyFactsResponse{
					CIK:        "VALIDATION_TEST",
					EntityName: "Validation Test",
					Facts: map[string]interface{}{
						"Assets": map[string]interface{}{
							"units": map[string]interface{}{
								"USD": []interface{}{
									map[string]interface{}{
										"val":   1000000,
										"fy":    2023,
										"form":  "10-K",
										"end":   "2023-09-30",
										"frame": "CY2023Q3",
									},
								},
							},
						},
						"Revenues": map[string]interface{}{
							"units": map[string]interface{}{
								"USD": []interface{}{
									map[string]interface{}{
										"val":   500000,
										"fy":    2023,
										"form":  "10-K",
										"end":   "2023-09-30",
										"frame": "CY2023Q3",
									},
								},
							},
						},
					},
				},
			}
			marketGateway := &mockMarketDataGateway{}
			macroGateway := &mockMacroDataGateway{}
			cacheRepo := &mockCacheRepository{}

			fetcher := NewDataFetcher(secGateway, marketGateway, macroGateway, cacheRepo)
			ctx := context.Background()

			request := &entities.FetchRequest{
				Ticker:          "VALIDATION_TEST",
				ValidationLevel: tt.validationLevel,
			}

			// Act
			result, err := fetcher.Fetch(ctx, request)

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

	health := fetcher.GetHealth(ctx)

	assert.NotNil(t, health)
	assert.Equal(t, 4, len(health)) // Should check three gateways plus cache
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

	fetcher := NewDataFetcher(secGateway, marketGateway, macroGateway, cacheRepo)

	// Update config post-creation since NewDataFetcherWithConfig doesn't exist
	fetcher.config = config

	assert.NotNil(t, fetcher)
	assert.Equal(t, config, fetcher.config)
	assert.False(t, fetcher.config.ConcurrentFetching)
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
		companyFacts: &entities.CompanyFactsResponse{
			CIK:        "PERF_TEST",
			EntityName: "Performance Test",
			Facts: map[string]interface{}{
				"Assets": map[string]interface{}{
					"units": map[string]interface{}{
						"USD": []interface{}{
							map[string]interface{}{
								"val":   1000000,
								"fy":    2023,
								"form":  "10-K",
								"end":   "2023-09-30",
								"frame": "CY2023Q3",
							},
						},
					},
				},
				"Revenues": map[string]interface{}{
					"units": map[string]interface{}{
						"USD": []interface{}{
							map[string]interface{}{
								"val":   500000,
								"fy":    2023,
								"form":  "10-K",
								"end":   "2023-09-30",
								"frame": "CY2023Q3",
							},
						},
					},
				},
			},
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

	request := &entities.FetchRequest{
		Ticker:          "PERF_TEST",
		ValidationLevel: entities.ValidationBasic,
	}

	// Run multiple iterations
	iterations := 100
	var totalDuration time.Duration

	for i := 0; i < iterations; i++ {
		start := time.Now()
		result, err := fetcher.Fetch(ctx, request)
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

// TestMultiSourceFetchIntegration tests multi-source data coordination specifically
func TestMultiSourceFetchIntegration(t *testing.T) {
	tests := []struct {
		name          string
		dataSources   []entities.DataSource
		setupMocks    func(*mockSECGateway, *mockMarketDataGateway, *mockMacroDataGateway)
		expectSuccess bool
		expectSources int
		expectData    map[entities.DataSource]bool
	}{
		{
			name:        "all_sources_successful",
			dataSources: []entities.DataSource{entities.SECSource, entities.MarketSource, entities.MacroSource},
			setupMocks: func(sec *mockSECGateway, market *mockMarketDataGateway, macro *mockMacroDataGateway) {
				sec.companyFacts = &entities.CompanyFactsResponse{
					CIK:        "TEST_MULTI",
					EntityName: "Multi Source Test",
					Facts: map[string]interface{}{
						"Assets": map[string]interface{}{
							"units": map[string]interface{}{
								"USD": []interface{}{
									map[string]interface{}{
										"val":   5000000,
										"fy":    2023,
										"form":  "10-K",
										"end":   "2023-09-30",
										"frame": "CY2023Q3",
									},
								},
							},
						},
					},
				}
				market.marketData = &entities.MarketData{
					Ticker:     "TEST_MULTI",
					SharePrice: 125.0,
					Beta:       1.1,
				}
				macro.macroData = &entities.MacroData{
					RiskFreeRate:      0.04,
					MarketRiskPremium: 0.055,
				}
			},
			expectSuccess: true,
			expectSources: 3,
			expectData: map[entities.DataSource]bool{
				entities.SECSource:    true,
				entities.MarketSource: true,
				entities.MacroSource:  true,
			},
		},
		{
			name:        "sec_and_market_only",
			dataSources: []entities.DataSource{entities.SECSource, entities.MarketSource},
			setupMocks: func(sec *mockSECGateway, market *mockMarketDataGateway, macro *mockMacroDataGateway) {
				sec.companyFacts = &entities.CompanyFactsResponse{
					CIK:        "TEST_PARTIAL",
					EntityName: "Partial Test",
					Facts:      map[string]interface{}{},
				}
				market.marketData = &entities.MarketData{
					Ticker:     "TEST_PARTIAL",
					SharePrice: 95.0,
					Beta:       0.8,
				}
			},
			expectSuccess: true,
			expectSources: 2,
			expectData: map[entities.DataSource]bool{
				entities.SECSource:    true,
				entities.MarketSource: true,
				entities.MacroSource:  false,
			},
		},
		{
			name:        "sec_failure_partial_success",
			dataSources: []entities.DataSource{entities.SECSource, entities.MarketSource, entities.MacroSource},
			setupMocks: func(sec *mockSECGateway, market *mockMarketDataGateway, macro *mockMacroDataGateway) {
				sec.err = errors.New("SEC API unavailable")
				market.marketData = &entities.MarketData{
					Ticker:     "TEST_FAIL",
					SharePrice: 110.0,
					Beta:       1.3,
				}
				macro.macroData = &entities.MacroData{
					RiskFreeRate:      0.035,
					MarketRiskPremium: 0.06,
				}
			},
			expectSuccess: false, // Should fail due to missing financial data
			expectSources: 3,
			expectData: map[entities.DataSource]bool{
				entities.SECSource:    false,
				entities.MarketSource: true,
				entities.MacroSource:  true,
			},
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
				tt.setupMocks(secGateway, marketGateway, macroGateway)
			}

			fetcher := NewDataFetcher(secGateway, marketGateway, macroGateway, cacheRepo)
			ctx := context.Background()

			request := &entities.FetchRequest{
				Ticker:          "TEST_INTEGRATION",
				DataSources:     tt.dataSources,
				ValidationLevel: entities.ValidationBasic,
			}

			start := time.Now()

			// Act
			result, err := fetcher.Fetch(ctx, request)

			// Assert
			fetchDuration := time.Since(start)

			assert.NoError(t, err)
			assert.NotNil(t, result)
			assert.Equal(t, tt.expectSuccess, result.Success)
			assert.Equal(t, tt.expectSources, len(result.SourceMetadata))

			// Verify source metadata exists for all requested sources
			for _, source := range tt.dataSources {
				metadata, exists := result.SourceMetadata[source]
				assert.True(t, exists, "Source metadata missing for %s", source)
				assert.Greater(t, metadata.Duration, time.Duration(0), "Duration should be tracked for %s", source)
			}

			// Verify data presence matches expectations
			if tt.expectData[entities.SECSource] {
				assert.NotNil(t, result.FinancialData, "Should have financial data when SEC source succeeds")
			}
			if tt.expectData[entities.MarketSource] {
				assert.NotNil(t, result.MarketData, "Should have market data when market source succeeds")
			}
			if tt.expectData[entities.MacroSource] {
				assert.NotNil(t, result.MacroData, "Should have macro data when macro source succeeds")
			}

			// Verify coordination timing (should be faster than sequential)
			if len(tt.dataSources) > 1 {
				maxSequentialTime := time.Duration(len(tt.dataSources)) * 100 * time.Millisecond
				assert.True(t, fetchDuration < maxSequentialTime,
					"Multi-source fetch should benefit from concurrency: %v vs max %v", fetchDuration, maxSequentialTime)
			}

			// Reset mock state for next test
			secGateway.err = nil
			marketGateway.err = nil
			macroGateway.err = nil
			secGateway.callCount = 0
			marketGateway.callCount = 0
			macroGateway.callCount = 0
		})
	}
}

// ---------------------------------------------------------------------------
// hasRequiredFields tests
// ---------------------------------------------------------------------------

// TestDataFetcher_HasRequiredFields tests the reflection-based required field
// check that is only invoked when ValidateCompleteness is true.
func TestDataFetcher_HasRequiredFields(t *testing.T) {
	tests := []struct {
		name           string
		requiredFields []string
		data           *entities.FinancialData
		expected       bool
	}{
		{
			name:           "all_required_fields_present_and_nonzero",
			requiredFields: []string{"TotalAssets", "Revenue"},
			data: &entities.FinancialData{
				TotalAssets: 1_000_000_000,
				Revenue:     500_000_000,
			},
			expected: true,
		},
		{
			name:           "missing_field_revenue_is_zero",
			requiredFields: []string{"TotalAssets", "Revenue"},
			data: &entities.FinancialData{
				TotalAssets: 1_000_000_000,
				Revenue:     0, // zero is treated as missing
			},
			expected: false,
		},
		{
			name:           "field_name_does_not_exist_on_struct",
			requiredFields: []string{"TotalAssets", "NonExistentField"},
			data: &entities.FinancialData{
				TotalAssets: 500_000,
			},
			expected: false,
		},
		{
			name:           "no_required_fields_configured",
			requiredFields: []string{},
			data:           &entities.FinancialData{},
			expected:       true,
		},
		{
			name:           "operating_income_present",
			requiredFields: []string{"OperatingIncome"},
			data: &entities.FinancialData{
				OperatingIncome: 120_000_000,
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetcher := NewDataFetcher(
				&mockSECGateway{},
				&mockMarketDataGateway{},
				&mockMacroDataGateway{},
				&mockCacheRepository{},
			)
			// Override config to enable completeness validation with custom fields
			fetcher.config.ValidateCompleteness = true
			fetcher.config.RequiredFields = tt.requiredFields

			got := fetcher.hasRequiredFields(tt.data)
			assert.Equal(t, tt.expected, got)
		})
	}
}

// TestDataFetcher_AssessDataSufficiency_WithCompleteness exercises the
// assessDataSufficiency path when ValidateCompleteness is enabled, which calls
// hasRequiredFields internally.
func TestDataFetcher_AssessDataSufficiency_WithCompleteness(t *testing.T) {
	fetcher := NewDataFetcher(
		&mockSECGateway{},
		&mockMarketDataGateway{},
		&mockMacroDataGateway{},
		&mockCacheRepository{},
	)
	fetcher.config.ValidateCompleteness = true
	fetcher.config.RequiredFields = []string{"TotalAssets", "Revenue"}

	// Financial data with all required fields populated
	resultComplete := &entities.FetchResult{
		FinancialData: &entities.FinancialData{
			TotalAssets: 1_000_000_000,
			Revenue:     500_000_000,
		},
	}
	assert.True(t, fetcher.assessDataSufficiency(resultComplete))

	// Financial data with missing required field
	resultIncomplete := &entities.FetchResult{
		FinancialData: &entities.FinancialData{
			TotalAssets: 1_000_000_000,
			Revenue:     0,
		},
	}
	assert.False(t, fetcher.assessDataSufficiency(resultIncomplete))
}

// ---------------------------------------------------------------------------
// GetMetrics computed values test
// ---------------------------------------------------------------------------

// TestDataFetcher_GetMetrics_ComputedValues verifies that CacheHitRate,
// AverageLatency, and ErrorRate are computed correctly after requests.
func TestDataFetcher_GetMetrics_ComputedValues(t *testing.T) {
	secGateway := &mockSECGateway{
		companyFacts: &entities.CompanyFactsResponse{
			CIK:        "METRICS_TEST",
			EntityName: "Metrics Test",
			Facts:      map[string]interface{}{},
		},
	}
	marketGateway := &mockMarketDataGateway{
		marketData: &entities.MarketData{SharePrice: 100.0, Beta: 1.0},
	}
	macroGateway := &mockMacroDataGateway{
		macroData: &entities.MacroData{RiskFreeRate: 0.04, MarketRiskPremium: 0.05},
	}
	cacheRepo := &mockCacheRepository{}

	fetcher := NewDataFetcher(secGateway, marketGateway, macroGateway, cacheRepo)
	ctx := context.Background()

	// Execute a few requests to populate metrics
	for i := 0; i < 3; i++ {
		_, _ = fetcher.Fetch(ctx, &entities.FetchRequest{
			Ticker:          "METRICS_TEST",
			ValidationLevel: entities.ValidationNone,
		})
	}

	metrics := fetcher.GetMetrics()

	assert.Equal(t, int64(3), metrics.TotalRequests)
	assert.Greater(t, metrics.AverageLatency, time.Duration(0), "AverageLatency should be computed")
	// CacheHitRate and ErrorRate should be valid ratios
	assert.GreaterOrEqual(t, metrics.CacheHitRate, 0.0)
	assert.LessOrEqual(t, metrics.CacheHitRate, 1.0)
	assert.GreaterOrEqual(t, metrics.ErrorRate, 0.0)
	assert.LessOrEqual(t, metrics.ErrorRate, 1.0)
}

// ---------------------------------------------------------------------------
// cacheResult with nil financial data test
// ---------------------------------------------------------------------------

// NOTE: TestDataFetcher_CacheResult_* tests were removed because caching
// was removed from DataFetcher. The fetcher always fetches fresh data now.

// ---------------------------------------------------------------------------
// GetHealth unhealthy paths tests
// ---------------------------------------------------------------------------

// mockUnhealthySECGateway simulates an SEC gateway that reports errors.
type mockUnhealthySECGateway struct {
	mockSECGateway
}

func (m *mockUnhealthySECGateway) GetCompanyConcepts(ctx context.Context, cik string, tag string) (*entities.ConceptResponse, error) {
	return nil, errors.New("SEC API down")
}

// mockUnhealthyMarketGateway simulates a market gateway that reports errors.
type mockUnhealthyMarketGateway struct {
	mockMarketDataGateway
}

func (m *mockUnhealthyMarketGateway) GetQuote(ctx context.Context, ticker string) (*entities.MarketData, error) {
	return nil, errors.New("market API down")
}

// mockUnhealthyMacroGateway simulates a macro gateway that reports errors.
type mockUnhealthyMacroGateway struct {
	mockMacroDataGateway
}

func (m *mockUnhealthyMacroGateway) GetTreasuryRates(ctx context.Context) (*entities.TreasuryRates, error) {
	return nil, errors.New("FRED API down")
}

// mockUnhealthyCacheRepo simulates a cache repository that reports errors.
type mockUnhealthyCacheRepo struct {
	mockCacheRepository
}

func (m *mockUnhealthyCacheRepo) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	return errors.New("cache write failed")
}

// TestDataFetcher_GetHealth_AllUnhealthy verifies the health check captures
// errors from each gateway and the cache.
func TestDataFetcher_GetHealth_AllUnhealthy(t *testing.T) {
	fetcher := NewDataFetcher(
		&mockUnhealthySECGateway{},
		&mockUnhealthyMarketGateway{},
		&mockUnhealthyMacroGateway{},
		&mockUnhealthyCacheRepo{},
	)

	health := fetcher.GetHealth(context.Background())

	assert.NotNil(t, health)
	assert.Equal(t, 4, len(health), "should check all 4 components")

	// SEC should be unhealthy
	secHealth, ok := health["sec_gateway"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "unhealthy", secHealth["status"])

	// Market should be unhealthy
	marketHealth, ok := health["market_gateway"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "unhealthy", marketHealth["status"])

	// Macro should be unhealthy
	macroHealth, ok := health["macro_gateway"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "unhealthy", macroHealth["status"])

	// Cache should be unhealthy
	cacheHealth, ok := health["cache"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "unhealthy", cacheHealth["status"])
}

// ---------------------------------------------------------------------------
// Fetch with caching disabled path
// ---------------------------------------------------------------------------

// TestDataFetcher_Fetch_CachingDisabled exercises the Fetch path when caching
// is turned off, ensuring checkCache is skipped entirely.
func TestDataFetcher_Fetch_CachingDisabled(t *testing.T) {
	secGateway := &mockSECGateway{
		companyFacts: &entities.CompanyFactsResponse{
			CIK:        "NOCACHE_TEST",
			EntityName: "No Cache Test",
			Facts:      map[string]interface{}{},
		},
	}
	marketGateway := &mockMarketDataGateway{
		marketData: &entities.MarketData{SharePrice: 50.0, Beta: 1.0},
	}
	macroGateway := &mockMacroDataGateway{
		macroData: &entities.MacroData{RiskFreeRate: 0.04, MarketRiskPremium: 0.05},
	}
	cacheRepo := &mockCacheRepository{}

	fetcher := NewDataFetcher(secGateway, marketGateway, macroGateway, cacheRepo)

	result, err := fetcher.Fetch(context.Background(), &entities.FetchRequest{
		Ticker:          "NOCACHE_TEST",
		ValidationLevel: entities.ValidationNone,
	})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	// CacheStatus should be miss since caching is no longer supported
	assert.Equal(t, entities.CacheMiss, result.CacheStatus)
}

// ---------------------------------------------------------------------------
// Fetch with ValidateCompleteness enabled end-to-end
// ---------------------------------------------------------------------------

// TestDataFetcher_Fetch_WithValidateCompleteness tests the Fetch path that
// invokes assessDataSufficiency -> hasRequiredFields when enabled.
func TestDataFetcher_Fetch_WithValidateCompleteness(t *testing.T) {
	secGateway := &mockSECGateway{
		companyFacts: &entities.CompanyFactsResponse{
			CIK:        "COMPLETE_TEST",
			EntityName: "Completeness Test",
			Facts: map[string]interface{}{
				"us-gaap": map[string]interface{}{
					"Assets": map[string]interface{}{
						"units": map[string]interface{}{
							"USD": []interface{}{
								map[string]interface{}{"val": 1000000.0},
							},
						},
					},
					"Revenues": map[string]interface{}{
						"units": map[string]interface{}{
							"USD": []interface{}{
								map[string]interface{}{"val": 500000.0},
							},
						},
					},
				},
			},
		},
	}
	marketGateway := &mockMarketDataGateway{
		marketData: &entities.MarketData{SharePrice: 100.0, Beta: 1.0},
	}
	macroGateway := &mockMacroDataGateway{
		macroData: &entities.MacroData{RiskFreeRate: 0.04, MarketRiskPremium: 0.05},
	}
	cacheRepo := &mockCacheRepository{}

	fetcher := NewDataFetcher(secGateway, marketGateway, macroGateway, cacheRepo)
	fetcher.config.ValidateCompleteness = true
	// Only require TotalAssets which will be set from extractLatestUSDValue
	fetcher.config.RequiredFields = []string{"TotalAssets"}

	result, err := fetcher.Fetch(context.Background(), &entities.FetchRequest{
		Ticker:          "COMPLETE_TEST",
		CIK:             "COMPLETE_TEST",
		ValidationLevel: entities.ValidationNone,
	})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	// Should be successful since TotalAssets is populated by extractLatestUSDValue
	assert.True(t, result.Success, "should be successful when required fields are present")
}
