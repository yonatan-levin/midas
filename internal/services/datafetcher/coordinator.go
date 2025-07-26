package datafetcher

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// DataCoordinator handles multi-source data fetching coordination
type DataCoordinator struct {
	config *DataFetcherConfig
}

// CoordinationResult represents the result of coordinated data fetching
type CoordinationResult struct {
	FinancialData  *entities.FinancialData   `json:"financial_data"`
	MarketData     *entities.MarketData      `json:"market_data"`
	MacroData      *entities.MacroData       `json:"macro_data"`
	SourceMetadata map[DataSource]SourceInfo `json:"source_metadata"`
	Errors         []FetchError              `json:"errors"`
	Warnings       []string                  `json:"warnings"`
}

// sourceTask represents a task to fetch data from a specific source
type sourceTask struct {
	source   DataSource
	ticker   string
	cik      string
	metadata SourceInfo
}

// NewDataCoordinator creates a new DataCoordinator instance
func NewDataCoordinator(config *DataFetcherConfig) *DataCoordinator {
	return &DataCoordinator{
		config: config,
	}
}

// CoordinateFetch orchestrates data fetching from multiple sources
func (dc *DataCoordinator) CoordinateFetch(ctx context.Context, request *FetchRequest) (*CoordinationResult, error) {
	if request == nil {
		return nil, fmt.Errorf("fetch request cannot be nil")
	}

	// Determine which sources to fetch from
	sources := request.DataSources
	if len(sources) == 0 {
		// Default to all sources if none specified
		sources = []DataSource{SECSource, MarketSource, MacroSource}
	}

	if dc.config.ConcurrentFetching {
		return dc.coordinateConcurrent(ctx, request, sources)
	}

	return dc.coordinateSequential(ctx, request, sources)
}

// coordinateConcurrent fetches data from multiple sources concurrently
func (dc *DataCoordinator) coordinateConcurrent(ctx context.Context, request *FetchRequest, sources []DataSource) (*CoordinationResult, error) {
	result := &CoordinationResult{
		SourceMetadata: make(map[DataSource]SourceInfo),
		Errors:         make([]FetchError, 0),
		Warnings:       make([]string, 0),
	}

	// Create channels for results

	resultChan := make(chan sourceResult, len(sources))
	var wg sync.WaitGroup

	// Launch goroutines for each source
	for _, source := range sources {
		wg.Add(1)
		go func(src DataSource) {
			defer wg.Done()
			srcResult := dc.fetchFromSource(ctx, request, src)
			resultChan <- srcResult
		}(source)
	}

	// Wait for all goroutines to complete
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results
	for srcResult := range resultChan {
		result.SourceMetadata[srcResult.source] = srcResult.metadata

		if srcResult.err != nil {
			result.Errors = append(result.Errors, FetchError{
				Source:  srcResult.source,
				Type:    "fetch_error",
				Message: srcResult.err.Error(),
			})
			continue
		}

		// Merge data based on source type
		switch srcResult.source {
		case SECSource:
			result.FinancialData = srcResult.financialData
		case MarketSource:
			result.MarketData = srcResult.marketData
		case MacroSource:
			result.MacroData = srcResult.macroData
		}
	}

	return result, nil
}

// coordinateSequential fetches data from sources sequentially
func (dc *DataCoordinator) coordinateSequential(ctx context.Context, request *FetchRequest, sources []DataSource) (*CoordinationResult, error) {
	result := &CoordinationResult{
		SourceMetadata: make(map[DataSource]SourceInfo),
		Errors:         make([]FetchError, 0),
		Warnings:       make([]string, 0),
	}

	for _, source := range sources {
		srcResult := dc.fetchFromSource(ctx, request, source)
		result.SourceMetadata[srcResult.source] = srcResult.metadata

		if srcResult.err != nil {
			result.Errors = append(result.Errors, FetchError{
				Source:  srcResult.source,
				Type:    "fetch_error",
				Message: srcResult.err.Error(),
			})
			continue
		}

		// Merge data based on source type
		switch srcResult.source {
		case SECSource:
			result.FinancialData = srcResult.financialData
		case MarketSource:
			result.MarketData = srcResult.marketData
		case MacroSource:
			result.MacroData = srcResult.macroData
		}
	}

	return result, nil
}

// sourceResult represents the result of fetching from a single source
type sourceResult struct {
	source        DataSource
	financialData *entities.FinancialData
	marketData    *entities.MarketData
	macroData     *entities.MacroData
	metadata      SourceInfo
	err           error
}

// fetchFromSource fetches data from a specific source with retry logic
func (dc *DataCoordinator) fetchFromSource(ctx context.Context, request *FetchRequest, source DataSource) sourceResult {
	startTime := time.Now()

	var err error
	var financialData *entities.FinancialData
	var marketData *entities.MarketData
	var macroData *entities.MacroData

	// Implement retry logic
	for attempt := 0; attempt <= dc.config.MaxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff with jitter
			backoffDuration := time.Duration(attempt*attempt) * 100 * time.Millisecond
			select {
			case <-time.After(backoffDuration):
			case <-ctx.Done():
				err = ctx.Err()
				break
			}
		}

		// Check context before each attempt
		if ctx.Err() != nil {
			err = ctx.Err()
			break
		}

		// Create timeout context for this attempt
		attemptCtx, cancel := context.WithTimeout(ctx, dc.config.TimeoutDuration)

		switch source {
		case SECSource:
			financialData, err = dc.fetchSECData(attemptCtx, request.Ticker, request.CIK)
		case MarketSource:
			marketData, err = dc.fetchMarketData(attemptCtx, request.Ticker)
		case MacroSource:
			macroData, err = dc.fetchMacroData(attemptCtx)
		default:
			err = fmt.Errorf("unknown data source: %s", source)
		}

		cancel()

		// If successful, break out of retry loop
		if err == nil {
			break
		}

		// If it's the last attempt or context is cancelled, don't retry
		if attempt == dc.config.MaxRetries || ctx.Err() != nil {
			break
		}
	}

	metadata := SourceInfo{
		FetchTime: time.Now(),
		Duration:  time.Since(startTime),
		FromCache: false, // TODO: Implement cache detection
		DataAge:   0,     // TODO: Calculate data age
		Retries:   0,     // TODO: Track actual retry count
	}

	return sourceResult{
		source:        source,
		financialData: financialData,
		marketData:    marketData,
		macroData:     macroData,
		metadata:      metadata,
		err:           err,
	}
}

// fetchSECData fetches financial data from SEC source
func (dc *DataCoordinator) fetchSECData(ctx context.Context, ticker, cik string) (*entities.FinancialData, error) {
	// TODO: Implement actual SEC gateway interaction
	// For now, return a mock error indicating this needs implementation
	return nil, fmt.Errorf("SEC data fetching not yet implemented")
}

// fetchMarketData fetches market data from market source
func (dc *DataCoordinator) fetchMarketData(ctx context.Context, ticker string) (*entities.MarketData, error) {
	// TODO: Implement actual market gateway interaction
	// For now, return a mock error indicating this needs implementation
	return nil, fmt.Errorf("market data fetching not yet implemented")
}

// fetchMacroData fetches macro economic data
func (dc *DataCoordinator) fetchMacroData(ctx context.Context) (*entities.MacroData, error) {
	// TODO: Implement actual macro gateway interaction
	// For now, return a mock error indicating this needs implementation
	return nil, fmt.Errorf("macro data fetching not yet implemented")
}

// GetCoordinationMetrics returns metrics about coordination performance
func (dc *DataCoordinator) GetCoordinationMetrics() *CoordinationMetrics {
	// TODO: Implement proper metrics collection
	return &CoordinationMetrics{
		TotalCoordinations: 0,
		ConcurrentRequests: 0,
		AverageLatency:     0,
		RetryRate:          0.0,
		SourceErrorRates:   make(map[DataSource]float64),
	}
}

// CoordinationMetrics holds metrics about coordination operations
type CoordinationMetrics struct {
	TotalCoordinations int64                  `json:"total_coordinations"`
	ConcurrentRequests int                    `json:"concurrent_requests"`
	AverageLatency     time.Duration          `json:"average_latency"`
	RetryRate          float64                `json:"retry_rate"`
	SourceErrorRates   map[DataSource]float64 `json:"source_error_rates"`
}
