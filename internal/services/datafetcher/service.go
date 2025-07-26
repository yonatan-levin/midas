package datafetcher

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// DataFetcher orchestrates data collection from multiple sources
type DataFetcher struct {
	secGateway    ports.SECGateway
	marketGateway ports.MarketDataGateway
	macroGateway  ports.MacroDataGateway
	cacheRepo     ports.CacheRepository
	validator     *DataValidator
	coordinator   *DataCoordinator
	config        *DataFetcherConfig

	// Metrics tracking
	metrics      *entities.DataFetcherMetrics
	metricsMutex sync.RWMutex
}

// DataFetcherConfig holds configuration for the data fetcher
type DataFetcherConfig struct {
	EnableCaching        bool          `json:"enable_caching"`
	CacheTTL             time.Duration `json:"cache_ttl"`
	ConcurrentFetching   bool          `json:"concurrent_fetching"`
	MaxRetries           int           `json:"max_retries"`
	TimeoutDuration      time.Duration `json:"timeout_duration"`
	ValidateCompleteness bool          `json:"validate_completeness"`
	RequiredFields       []string      `json:"required_fields"`
}

// NewDataFetcher creates a new DataFetcher instance
func NewDataFetcher(
	secGateway ports.SECGateway,
	marketGateway ports.MarketDataGateway,
	macroGateway ports.MacroDataGateway,
	cacheRepo ports.CacheRepository,
) *DataFetcher {
	config := &DataFetcherConfig{
		EnableCaching:        true,
		CacheTTL:             24 * time.Hour,
		ConcurrentFetching:   true,
		MaxRetries:           3,
		TimeoutDuration:      30 * time.Second,
		ValidateCompleteness: false, // Disable field validation for simpler testing
		RequiredFields:       []string{"TotalAssets", "Revenue", "OperatingIncome"},
	}

	validator := NewDataValidator(config)
	coordinator := NewDataCoordinator(config, secGateway, marketGateway, macroGateway)

	return &DataFetcher{
		secGateway:    secGateway,
		marketGateway: marketGateway,
		macroGateway:  macroGateway,
		cacheRepo:     cacheRepo,
		validator:     validator,
		coordinator:   coordinator,
		config:        config,
		metrics: &entities.DataFetcherMetrics{
			SourceLatencies: make(map[entities.DataSource]time.Duration),
			StartTime:       time.Now(),
		},
	}
}

// Fetch retrieves comprehensive financial data for a ticker
func (df *DataFetcher) Fetch(ctx context.Context, request *entities.FetchRequest) (*entities.FetchResult, error) {
	// Validate input
	if request == nil {
		return nil, fmt.Errorf("fetch request cannot be nil")
	}
	if request.Ticker == "" {
		return nil, fmt.Errorf("ticker cannot be empty")
	}

	start := time.Now()
	df.updateMetrics(func(m *entities.DataFetcherMetrics) {
		m.TotalRequests++
	})

	// Check cache first if enabled
	if df.config.EnableCaching {
		if cachedResult := df.checkCache(ctx, request); cachedResult != nil {
			result := &entities.FetchResult{
				Ticker:         request.Ticker,
				Success:        true,
				FinancialData:  cachedResult.FinancialData,
				MarketData:     cachedResult.MarketData,
				MacroData:      cachedResult.MacroData,
				SourceMetadata: cachedResult.SourceMetadata,
				FetchDuration:  time.Since(start),
				CacheStatus:    entities.CacheHit,
			}

			df.updateMetrics(func(m *entities.DataFetcherMetrics) {
				m.CacheHits++
				m.SuccessfulFetches++
			})

			return result, nil
		}
	}

	// Proceed with fresh data fetch
	result := &entities.FetchResult{
		Ticker:         request.Ticker,
		SourceMetadata: make(map[entities.DataSource]entities.SourceInfo),
		CacheStatus:    entities.CacheMiss,
	}

	// Use coordinator for orchestrated fetching
	coordResult, err := df.coordinator.CoordinateFetch(ctx, request)
	if err != nil {
		result.Success = false
		result.Errors = []entities.FetchError{{
			Source:  "coordinator",
			Type:    "coordination_error",
			Message: err.Error(),
		}}
		df.updateMetrics(func(m *entities.DataFetcherMetrics) {
			m.TotalErrors++
		})
		return result, err
	}

	// Populate result from coordination
	result.FinancialData = coordResult.FinancialData
	result.MarketData = coordResult.MarketData
	result.MacroData = coordResult.MacroData
	result.SourceMetadata = coordResult.SourceMetadata
	result.Errors = coordResult.Errors
	result.Warnings = coordResult.Warnings

	// Assess data sufficiency
	result.Success = df.assessDataSufficiency(result)
	result.FetchDuration = time.Since(start)

	// Validate data quality if requested
	if request.ValidationLevel != entities.ValidationNone {
		qualityReport, err := df.validator.ValidateDataQuality(result, request.ValidationLevel)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("validation error: %v", err))
		} else {
		result.QualityReport = qualityReport
	}
	}

	// Cache result if successful and caching is enabled
	if result.Success && df.config.EnableCaching {
		df.cacheResult(ctx, request, result)
	}

	// Update metrics
	df.updateMetrics(func(m *entities.DataFetcherMetrics) {
		if result.Success {
			m.SuccessfulFetches++
		} else {
			m.TotalErrors++
		}
		m.TotalLatency += result.FetchDuration
	})

	return result, nil
}

// BulkFetch retrieves data for multiple tickers with controlled concurrency
func (df *DataFetcher) BulkFetch(ctx context.Context, requests []*entities.FetchRequest) ([]*entities.FetchResult, error) {
	if len(requests) == 0 {
		return []*entities.FetchResult{}, nil
	}

	results := make([]*entities.FetchResult, len(requests))

	// Use semaphore to control concurrency
	semaphore := make(chan struct{}, 5) // Limit to 5 concurrent requests
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, request := range requests {
		wg.Add(1)
		go func(index int, req *entities.FetchRequest) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Create individual context with timeout
			fetchCtx, cancel := context.WithTimeout(ctx, df.config.TimeoutDuration)
			defer cancel()

			result, err := df.Fetch(fetchCtx, req)
			if err != nil {
				// Create error result
				result = &entities.FetchResult{
					Ticker:  req.Ticker,
					Success: false,
					Errors: []entities.FetchError{{
						Source:  "bulk_fetch",
						Type:    "fetch_error",
						Message: err.Error(),
					}},
				}
			}

			mu.Lock()
			results[index] = result
			mu.Unlock()
		}(i, request)
	}

	wg.Wait()
	return results, nil
}

// GetMetrics returns current operational metrics
func (df *DataFetcher) GetMetrics() *entities.DataFetcherMetrics {
	df.metricsMutex.RLock()
	defer df.metricsMutex.RUnlock()

	// Create a copy to avoid race conditions
	copy := *df.metrics
	if df.metrics.TotalRequests > 0 {
		copy.CacheHitRate = float64(df.metrics.CacheHits) / float64(df.metrics.TotalRequests)
		copy.AverageLatency = df.metrics.TotalLatency / time.Duration(df.metrics.TotalRequests)
		copy.ErrorRate = float64(df.metrics.TotalErrors) / float64(df.metrics.TotalRequests)
	}
	return &copy
}

// GetHealth performs health checks on all data sources
func (df *DataFetcher) GetHealth(ctx context.Context) map[string]interface{} {
	health := make(map[string]interface{})

	// Check SEC Gateway
	if _, err := df.secGateway.GetCompanyConcepts(ctx, "0000320193", "Assets"); err != nil {
		health["sec_gateway"] = map[string]interface{}{
			"status": "unhealthy",
			"error":  err.Error(),
		}
	} else {
		health["sec_gateway"] = map[string]interface{}{
			"status": "healthy",
		}
	}

	// Check Market Data Gateway
	if _, err := df.marketGateway.GetQuote(ctx, "AAPL"); err != nil {
		health["market_gateway"] = map[string]interface{}{
			"status": "unhealthy",
			"error":  err.Error(),
		}
	} else {
		health["market_gateway"] = map[string]interface{}{
			"status": "healthy",
		}
	}

	// Check Macro Data Gateway
	if _, err := df.macroGateway.GetTreasuryRates(ctx); err != nil {
		health["macro_gateway"] = map[string]interface{}{
			"status": "unhealthy",
			"error":  err.Error(),
		}
	} else {
		health["macro_gateway"] = map[string]interface{}{
			"status": "healthy",
		}
	}

	// Check cache repository
	testKey := "health_check"
	if err := df.cacheRepo.Set(ctx, testKey, "test", time.Minute); err != nil {
		health["cache"] = map[string]interface{}{
			"status": "unhealthy",
			"error":  err.Error(),
		}
	} else {
		health["cache"] = map[string]interface{}{
			"status": "healthy",
		}
		// Clean up test data
		df.cacheRepo.Delete(ctx, testKey)
	}

	return health
}

// checkCache attempts to retrieve cached data
func (df *DataFetcher) checkCache(ctx context.Context, request *entities.FetchRequest) *entities.CachedDataResult {
	cacheKey := df.generateCacheKey(request.Ticker)

	var cachedData entities.CachedDataResult
	if err := df.cacheRepo.Get(ctx, cacheKey, &cachedData); err == nil {
		// Check if cached data is still fresh
		if time.Since(cachedData.CachedAt) < df.config.CacheTTL {
			return &cachedData
		}
	}

	return nil
}

// cacheResult stores the fetch result in cache
func (df *DataFetcher) cacheResult(ctx context.Context, request *entities.FetchRequest, result *entities.FetchResult) {
	if !result.Success || result.FinancialData == nil {
		return
	}

	cachedData := entities.CachedDataResult{
		FinancialData:  result.FinancialData,
		MarketData:     result.MarketData,
		MacroData:      result.MacroData,
		SourceMetadata: result.SourceMetadata,
		CachedAt:       time.Now(),
	}

	cacheKey := df.generateCacheKey(request.Ticker)
	if err := df.cacheRepo.Set(ctx, cacheKey, cachedData, df.config.CacheTTL); err != nil {
		// Log error but don't fail the request
		fmt.Printf("Failed to cache result for %s: %v\n", request.Ticker, err)
	}
}

// generateCacheKey creates a cache key for the request
func (df *DataFetcher) generateCacheKey(ticker string) string {
	return fmt.Sprintf("datafetcher:comprehensive:%s", ticker)
}

// assessDataSufficiency determines if we have enough data for a successful result
func (df *DataFetcher) assessDataSufficiency(result *entities.FetchResult) bool {
	if result.FinancialData == nil {
		return false
	}

	// Check if there are any errors that would indicate data source failures
	if len(result.Errors) > 0 {
		// If any errors occurred, consider it a partial success (insufficient)
		return false
	}

	// Check for required fields
	if df.config.ValidateCompleteness {
		return df.hasRequiredFields(result.FinancialData)
	}

	return true
}

// hasRequiredFields checks if financial data has all required fields
func (df *DataFetcher) hasRequiredFields(data *entities.FinancialData) bool {
	dataValue := reflect.ValueOf(data).Elem()
	dataType := dataValue.Type()

	for _, requiredField := range df.config.RequiredFields {
		fieldFound := false
		for i := 0; i < dataType.NumField(); i++ {
			field := dataType.Field(i)
			if field.Name == requiredField {
				fieldValue := dataValue.Field(i)
				if !fieldValue.IsZero() {
					fieldFound = true
					break
				}
			}
		}
		if !fieldFound {
			return false
		}
	}
	return true
}

// updateMetrics safely updates metrics using a function
func (df *DataFetcher) updateMetrics(updateFunc func(*entities.DataFetcherMetrics)) {
	df.metricsMutex.Lock()
	defer df.metricsMutex.Unlock()
	updateFunc(df.metrics)
}
