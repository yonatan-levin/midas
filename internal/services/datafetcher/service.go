package datafetcher

import (
	"context"
	"fmt"
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

// FetchRequest represents a request for financial data
type FetchRequest struct {
	Ticker          string                 `json:"ticker"`
	CIK             string                 `json:"cik,omitempty"`
	DataSources     []DataSource           `json:"data_sources"`
	ValidationLevel ValidationLevel        `json:"validation_level"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
}

// FetchResult represents the complete result of data fetching
type FetchResult struct {
	Ticker         string                    `json:"ticker"`
	Success        bool                      `json:"success"`
	FinancialData  *entities.FinancialData   `json:"financial_data"`
	MarketData     *entities.MarketData      `json:"market_data"`
	MacroData      *entities.MacroData       `json:"macro_data"`
	QualityReport  *DataQualityReport        `json:"quality_report"`
	SourceMetadata map[DataSource]SourceInfo `json:"source_metadata"`
	FetchDuration  time.Duration             `json:"fetch_duration"`
	CacheStatus    CacheStatus               `json:"cache_status"`
	Errors         []FetchError              `json:"errors,omitempty"`
	Warnings       []string                  `json:"warnings,omitempty"`
}

// DataSource represents different types of data sources
type DataSource string

const (
	SECSource    DataSource = "sec"
	MarketSource DataSource = "market"
	MacroSource  DataSource = "macro"
)

// ValidationLevel defines the strictness of data validation
type ValidationLevel string

const (
	ValidationNone     ValidationLevel = "none"
	ValidationBasic    ValidationLevel = "basic"
	ValidationStrict   ValidationLevel = "strict"
	ValidationCritical ValidationLevel = "critical"
)

// CacheStatus indicates whether data was served from cache
type CacheStatus string

const (
	CacheHit    CacheStatus = "hit"
	CacheMiss   CacheStatus = "miss"
	CacheError  CacheStatus = "error"
	CacheBypass CacheStatus = "bypass"
)

// SourceInfo contains metadata about data source fetch
type SourceInfo struct {
	FetchTime  time.Time     `json:"fetch_time"`
	Duration   time.Duration `json:"duration"`
	FromCache  bool          `json:"from_cache"`
	DataAge    time.Duration `json:"data_age"`
	Retries    int           `json:"retries"`
	StatusCode int           `json:"status_code,omitempty"`
}

// FetchError represents an error from a specific data source
type FetchError struct {
	Source  DataSource `json:"source"`
	Type    string     `json:"type"`
	Message string     `json:"message"`
	Code    string     `json:"code,omitempty"`
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
		ValidateCompleteness: true,
		RequiredFields: []string{
			"TotalAssets", "Revenue", "OperatingIncome",
			"TotalDebt", "SharesOutstanding",
		},
	}

	validator := NewDataValidator(config)
	coordinator := NewDataCoordinator(config)

	return &DataFetcher{
		secGateway:    secGateway,
		marketGateway: marketGateway,
		macroGateway:  macroGateway,
		cacheRepo:     cacheRepo,
		validator:     validator,
		coordinator:   coordinator,
		config:        config,
	}
}

// NewDataFetcherWithConfig creates a DataFetcher with custom configuration
func NewDataFetcherWithConfig(
	secGateway ports.SECGateway,
	marketGateway ports.MarketDataGateway,
	macroGateway ports.MacroDataGateway,
	cacheRepo ports.CacheRepository,
	config *DataFetcherConfig,
) *DataFetcher {
	validator := NewDataValidator(config)
	coordinator := NewDataCoordinator(config)

	return &DataFetcher{
		secGateway:    secGateway,
		marketGateway: marketGateway,
		macroGateway:  macroGateway,
		cacheRepo:     cacheRepo,
		validator:     validator,
		coordinator:   coordinator,
		config:        config,
	}
}

// FetchComprehensiveData fetches and coordinates data from all sources
func (df *DataFetcher) FetchComprehensiveData(ctx context.Context, request *FetchRequest) (*FetchResult, error) {
	if request == nil {
		return nil, fmt.Errorf("fetch request cannot be nil")
	}

	if request.Ticker == "" {
		return nil, fmt.Errorf("ticker is required")
	}

	startTime := time.Now()

	// Initialize result
	result := &FetchResult{
		Ticker:         request.Ticker,
		Success:        false,
		SourceMetadata: make(map[DataSource]SourceInfo),
		Errors:         make([]FetchError, 0),
		Warnings:       make([]string, 0),
		CacheStatus:    CacheMiss,
	}

	// Check cache first if enabled
	if df.config.EnableCaching {
		cached, cacheErr := df.checkCache(ctx, request.Ticker)
		if cacheErr == nil && cached != nil {
			result.FinancialData = cached.FinancialData
			result.MarketData = cached.MarketData
			result.MacroData = cached.MacroData
			result.CacheStatus = CacheHit
			result.Success = true
			result.FetchDuration = time.Since(startTime)
			return result, nil
		}
		if cacheErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Cache check failed: %v", cacheErr))
			result.CacheStatus = CacheError
		}
	}

	// Coordinate data fetching from multiple sources
	coordinatedResult, err := df.coordinator.CoordinateFetch(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("data coordination failed: %w", err)
	}

	// Merge coordinated results
	result.FinancialData = coordinatedResult.FinancialData
	result.MarketData = coordinatedResult.MarketData
	result.MacroData = coordinatedResult.MacroData
	result.SourceMetadata = coordinatedResult.SourceMetadata
	result.Errors = coordinatedResult.Errors
	result.Warnings = append(result.Warnings, coordinatedResult.Warnings...)

	// Validate data quality
	if request.ValidationLevel != ValidationNone {
		qualityReport, validationErr := df.validator.ValidateDataQuality(result, request.ValidationLevel)
		if validationErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Validation error: %v", validationErr))
		}
		result.QualityReport = qualityReport
	}

	// Check if we have sufficient data for success
	result.Success = df.assessDataSufficiency(result)

	// Cache the result if successful and caching is enabled
	if result.Success && df.config.EnableCaching {
		if cacheErr := df.cacheResult(ctx, result); cacheErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Cache store failed: %v", cacheErr))
		}
	}

	result.FetchDuration = time.Since(startTime)
	return result, nil
}

// FetchFinancialDataOnly fetches only financial data (SEC source)
func (df *DataFetcher) FetchFinancialDataOnly(ctx context.Context, ticker string, cik string) (*entities.FinancialData, error) {
	request := &FetchRequest{
		Ticker:          ticker,
		CIK:             cik,
		DataSources:     []DataSource{SECSource},
		ValidationLevel: ValidationBasic,
	}

	result, err := df.FetchComprehensiveData(ctx, request)
	if err != nil {
		return nil, err
	}

	return result.FinancialData, nil
}

// FetchMarketDataOnly fetches only market data
func (df *DataFetcher) FetchMarketDataOnly(ctx context.Context, ticker string) (*entities.MarketData, error) {
	request := &FetchRequest{
		Ticker:          ticker,
		DataSources:     []DataSource{MarketSource},
		ValidationLevel: ValidationBasic,
	}

	result, err := df.FetchComprehensiveData(ctx, request)
	if err != nil {
		return nil, err
	}

	return result.MarketData, nil
}

// FetchMacroDataOnly fetches only macro economic data
func (df *DataFetcher) FetchMacroDataOnly(ctx context.Context) (*entities.MacroData, error) {
	request := &FetchRequest{
		Ticker:          "MACRO", // Special ticker for macro data
		DataSources:     []DataSource{MacroSource},
		ValidationLevel: ValidationBasic,
	}

	result, err := df.FetchComprehensiveData(ctx, request)
	if err != nil {
		return nil, err
	}

	return result.MacroData, nil
}

// BulkFetch fetches data for multiple tickers concurrently
func (df *DataFetcher) BulkFetch(ctx context.Context, requests []*FetchRequest) ([]*FetchResult, error) {
	if len(requests) == 0 {
		return []*FetchResult{}, nil
	}

	// TODO: Implement bulk coordination with rate limiting and connection pooling
	results := make([]*FetchResult, len(requests))
	errors := make([]error, len(requests))

	// Use a wait group for concurrent processing
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 5) // Limit concurrent requests to avoid overwhelming APIs

	for i, request := range requests {
		wg.Add(1)
		go func(index int, req *FetchRequest) {
			defer wg.Done()
			semaphore <- struct{}{}        // Acquire semaphore
			defer func() { <-semaphore }() // Release semaphore

			result, err := df.FetchComprehensiveData(ctx, req)
			results[index] = result
			errors[index] = err
		}(i, request)
	}

	wg.Wait()

	// Check for any critical errors
	var criticalErrors []error
	for _, err := range errors {
		if err != nil {
			criticalErrors = append(criticalErrors, err)
		}
	}

	if len(criticalErrors) > 0 {
		return results, fmt.Errorf("bulk fetch encountered %d errors", len(criticalErrors))
	}

	return results, nil
}

// checkCache attempts to retrieve cached data
func (df *DataFetcher) checkCache(ctx context.Context, ticker string) (*FetchResult, error) {
	// TODO: Implement proper cache key generation and data deserialization
	// For now, return cache miss
	_ = fmt.Sprintf("comprehensive_data:%s", ticker) // Cache key placeholder
	return nil, fmt.Errorf("cache miss")
}

// cacheResult stores the result in cache
func (df *DataFetcher) cacheResult(ctx context.Context, result *FetchResult) error {
	if result == nil || !result.Success {
		return fmt.Errorf("cannot cache unsuccessful result")
	}

	// TODO: Implement proper cache serialization and storage
	// For now, just log the cache operation
	_ = fmt.Sprintf("comprehensive_data:%s", result.Ticker) // Cache key placeholder
	return nil
}

// assessDataSufficiency determines if we have enough data for a successful result
func (df *DataFetcher) assessDataSufficiency(result *FetchResult) bool {
	if result.FinancialData == nil {
		return false
	}

	// Check required fields
	for _, field := range df.config.RequiredFields {
		if !df.hasRequiredField(result.FinancialData, field) {
			return false
		}
	}

	// If we have critical validation errors, mark as unsuccessful
	if result.QualityReport != nil && result.QualityReport.CriticalIssues > 0 {
		return false
	}

	return true
}

// hasRequiredField checks if a required field has a non-zero value
func (df *DataFetcher) hasRequiredField(data *entities.FinancialData, field string) bool {
	// TODO: Use reflection or implement proper field checking
	// For now, do basic checks on key fields
	switch field {
	case "TotalAssets":
		return data.TotalAssets > 0
	case "Revenue":
		return data.Revenue > 0
	case "OperatingIncome":
		return data.OperatingIncome != 0 // Can be negative
	case "TotalDebt":
		return true // Debt can be zero
	case "SharesOutstanding":
		return data.SharesOutstanding > 0
	default:
		return true // Unknown fields are considered present
	}
}

// GetHealth returns the health status of all data sources
func (df *DataFetcher) GetHealth(ctx context.Context) (map[DataSource]bool, error) {
	health := make(map[DataSource]bool)

	// TODO: Implement proper health checks for each gateway
	// For now, assume all sources are healthy
	health[SECSource] = true
	health[MarketSource] = true
	health[MacroSource] = true

	return health, nil
}

// GetMetrics returns operational metrics for the data fetcher
func (df *DataFetcher) GetMetrics() *DataFetcherMetrics {
	// TODO: Implement proper metrics collection
	return &DataFetcherMetrics{
		TotalRequests:   0,
		CacheHitRate:    0.0,
		AverageLatency:  0,
		ErrorRate:       0.0,
		SourceLatencies: make(map[DataSource]time.Duration),
	}
}

// DataFetcherMetrics holds operational metrics
type DataFetcherMetrics struct {
	TotalRequests   int64                        `json:"total_requests"`
	CacheHitRate    float64                      `json:"cache_hit_rate"`
	AverageLatency  time.Duration                `json:"average_latency"`
	ErrorRate       float64                      `json:"error_rate"`
	SourceLatencies map[DataSource]time.Duration `json:"source_latencies"`
}
