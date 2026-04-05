package entities

import (
	"time"
)

// DataSource represents different types of data sources
type DataSource string

const (
	SECSource    DataSource = "sec"
	MarketSource DataSource = "market"
	MacroSource  DataSource = "macro"
)

// CacheStatus indicates whether data was served from cache
type CacheStatus string

const (
	CacheHit    CacheStatus = "hit"
	CacheMiss   CacheStatus = "miss"
	CacheError  CacheStatus = "error"
	CacheBypass CacheStatus = "bypass"
)

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
	FinancialData  *FinancialData            `json:"financial_data"`
	HistoricalData *HistoricalFinancialData  `json:"historical_data,omitempty"` // Multi-period SEC data from full parser
	MarketData     *MarketData               `json:"market_data"`
	MacroData      *MacroData                `json:"macro_data"`
	QualityReport  *DataQualityReport        `json:"quality_report"`
	SourceMetadata map[DataSource]SourceInfo `json:"source_metadata"`
	FetchDuration  time.Duration             `json:"fetch_duration"`
	CacheStatus    CacheStatus               `json:"cache_status"`
	Errors         []FetchError              `json:"errors,omitempty"`
	Warnings       []string                  `json:"warnings,omitempty"`
}

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

// DataFetcherMetrics holds operational metrics
type DataFetcherMetrics struct {
	TotalRequests   int64                        `json:"total_requests"`
	CacheHitRate    float64                      `json:"cache_hit_rate"`
	AverageLatency  time.Duration                `json:"average_latency"`
	ErrorRate       float64                      `json:"error_rate"`
	SourceLatencies map[DataSource]time.Duration `json:"source_latencies"`

	// Additional metrics for calculations
	CacheHits         int64         `json:"cache_hits"`
	CacheMisses       int64         `json:"cache_misses"`
	TotalErrors       int64         `json:"total_errors"`
	SuccessfulFetches int64         `json:"successful_fetches"`
	StartTime         time.Time     `json:"start_time"`
	TotalLatency      time.Duration `json:"total_latency"`
}

// CoordinationResult represents the result of coordinated data fetching
type CoordinationResult struct {
	FinancialData  *FinancialData            `json:"financial_data"`
	HistoricalData *HistoricalFinancialData  `json:"historical_data,omitempty"`
	MarketData     *MarketData               `json:"market_data"`
	MacroData      *MacroData                `json:"macro_data"`
	SourceMetadata map[DataSource]SourceInfo `json:"source_metadata"`
	Errors         []FetchError              `json:"errors"`
	Warnings       []string                  `json:"warnings"`
}

// CoordinationMetrics represents metrics for data coordination
type CoordinationMetrics struct {
	TotalCoordinations int64                  `json:"total_coordinations"`
	ConcurrentRequests int                    `json:"concurrent_requests"`
	AverageLatency     time.Duration          `json:"average_latency"`
	RetryRate          float64                `json:"retry_rate"`
	SourceErrorRates   map[DataSource]float64 `json:"source_error_rates"`
}
