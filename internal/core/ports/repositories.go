package ports

import (
	"context"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// FinancialDataRepository defines the interface for financial data storage
type FinancialDataRepository interface {
	// Store stores financial data for a company
	Store(ctx context.Context, data *entities.FinancialData) error

	// GetLatest retrieves the most recent financial data for a ticker
	GetLatest(ctx context.Context, ticker string) (*entities.FinancialData, error)

	// GetHistorical retrieves historical financial data for a ticker
	GetHistorical(ctx context.Context, ticker string, periods int) (*entities.HistoricalFinancialData, error)

	// GetByPeriod retrieves financial data for a specific period
	GetByPeriod(ctx context.Context, ticker, period string) (*entities.FinancialData, error)

	// StoreHistorical stores multiple periods of financial data
	StoreHistorical(ctx context.Context, data *entities.HistoricalFinancialData) error

	// GetLastUpdated returns when the data was last updated for a ticker
	GetLastUpdated(ctx context.Context, ticker string) (time.Time, error)
}

// MarketDataRepository defines the interface for market data storage
type MarketDataRepository interface {
	// Store stores market data for a company
	Store(ctx context.Context, data *entities.MarketData) error

	// GetLatest retrieves the most recent market data for a ticker
	GetLatest(ctx context.Context, ticker string) (*entities.MarketData, error)

	// GetBatch retrieves market data for multiple tickers
	GetBatch(ctx context.Context, tickers []string) (map[string]*entities.MarketData, error)

	// IsStale checks if market data is stale for a ticker
	IsStale(ctx context.Context, ticker string, maxAge time.Duration) (bool, error)

	// GetLastUpdated returns when the data was last updated for a ticker
	GetLastUpdated(ctx context.Context, ticker string) (time.Time, error)
}

// MacroDataRepository defines the interface for macro-economic data storage
type MacroDataRepository interface {
	// Store stores macro-economic data
	Store(ctx context.Context, data *entities.MacroData) error

	// GetLatest retrieves the most recent macro data
	GetLatest(ctx context.Context) (*entities.MacroData, error)

	// IsStale checks if macro data is stale
	IsStale(ctx context.Context, maxAge time.Duration) (bool, error)
}

// CacheRepository defines the interface for caching operations
type CacheRepository interface {
	// Set stores a value in cache with TTL
	Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error

	// Get retrieves a value from cache
	Get(ctx context.Context, key string, dest interface{}) error

	// Delete removes a value from cache
	Delete(ctx context.Context, key string) error

	// Exists checks if a key exists in cache
	Exists(ctx context.Context, key string) (bool, error)

	// SetNX sets a value only if key doesn't exist (for locking)
	SetNX(ctx context.Context, key string, value interface{}, ttl time.Duration) (bool, error)

	// GetKeys returns all keys matching a pattern
	GetKeys(ctx context.Context, pattern string) ([]string, error)

	// DeletePattern deletes all keys matching a pattern
	DeletePattern(ctx context.Context, pattern string) error
}

// TickerMappingRepository defines the interface for ticker-to-CIK mapping
type TickerMappingRepository interface {
	// GetCIK retrieves the CIK for a ticker symbol
	GetCIK(ctx context.Context, ticker string) (string, error)

	// GetTicker retrieves the ticker for a CIK
	GetTicker(ctx context.Context, cik string) (string, error)

	// Store stores a ticker-to-CIK mapping
	Store(ctx context.Context, ticker, cik string) error

	// BulkStore stores multiple ticker-to-CIK mappings
	BulkStore(ctx context.Context, mappings map[string]string) error

	// GetAllMappings retrieves all ticker-to-CIK mappings
	GetAllMappings(ctx context.Context) (map[string]string, error)

	// LoadFromSEC loads ticker mappings from SEC data
	LoadFromSEC(ctx context.Context) error
}

// WatchlistRepository defines the interface for scheduler watchlist management
type WatchlistRepository interface {
	// GetActiveWatchlist retrieves all active tickers from the watchlist, optionally filtered by priority
	GetActiveWatchlist(ctx context.Context, filter *entities.WatchlistFilter) ([]*entities.WatchlistEntry, error)

	// GetAll retrieves all watchlist entries with optional filtering
	GetAll(ctx context.Context, filter *entities.WatchlistFilter) ([]*entities.WatchlistEntry, error)

	// GetByTicker retrieves a watchlist entry by ticker symbol
	GetByTicker(ctx context.Context, ticker string) (*entities.WatchlistEntry, error)

	// Add adds a new ticker to the watchlist
	Add(ctx context.Context, entry *entities.WatchlistEntry) error

	// Update updates an existing watchlist entry
	Update(ctx context.Context, ticker string, updates *entities.UpdateWatchlistEntryRequest) error

	// Remove removes a ticker from the watchlist
	Remove(ctx context.Context, ticker string) error

	// RecordSuccess updates the last fetched time and resets failure count
	RecordSuccess(ctx context.Context, ticker string, fetchedAt time.Time) error

	// RecordFailure increments the failure count and optionally disables the entry
	RecordFailure(ctx context.Context, ticker string) error

	// GetStats retrieves statistics about the watchlist
	GetStats(ctx context.Context) (*entities.WatchlistStats, error)

	// BulkUpdateFailures updates failure counts for multiple tickers (for batch operations)
	BulkUpdateFailures(ctx context.Context, failures map[string]bool) error
}

// MetricsService defines the interface for metrics collection and reporting
type MetricsService interface {
	// HTTP Metrics
	RecordHTTPRequest(method, endpoint string, statusCode int, duration time.Duration, responseSize int)
	IncHTTPRequestsInFlight()
	DecHTTPRequestsInFlight()

	// Valuation Metrics
	RecordValuationRequest(ticker, requestType, status string, duration time.Duration)
	RecordValuationError(errorType string)
	IncDCFCalculations()
	IncWACCCalculations()

	// Data Source Metrics
	RecordSECAPIRequest(endpoint, status string)
	RecordMarketAPIRequest(provider, status string)
	RecordMacroAPIRequest(provider, status string)
	RecordDataFetch(source, ticker string, duration time.Duration)

	// Cache Metrics
	RecordCacheRequest(cacheType, operation, result string)
	SetCacheHitRatio(cacheType string, ratio float64)

	// Business Metrics Setters
	SetAverageWACC(wacc float64)
	SetAverageGrowthRate(rate float64)

	// Getters for Health Checks and Reporting
	GetTotalRequests() int64
	GetActiveConnections() int
	GetAverageResponseTime() float64
	GetErrorRate() float64
	GetCacheHitRate() float64
	GetTotalValuations() int64
	GetSuccessfulValuations() int64
	GetFailedValuations() int64
	GetAverageWACC() float64
	GetAverageGrowthRate() float64
	GetUniqueTickersServed() int64

	// Health Check
	HealthCheck() error
}
