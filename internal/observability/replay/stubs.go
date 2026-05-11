package replay

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// This file holds the side-effect stubs replay swaps in for repositories
// and metrics so the engine path can run hermetically (no DB, no Redis,
// no Prometheus) per spec D8 and F11.
//
// Three classes of stub:
//
//   1. NotFound* repos — Get/GetLatest/GetHistorical/etc. return a
//      not-found-like error so the engine falls through to the bundle
//      gateways (which is the only data source replay actually has).
//      Set/Store/etc. are no-ops; replay must never write to anything.
//
//   2. NoOpMetricsService — every emission method is a no-op.
//      A counter (CallsCount) is exposed so module_test.go can prove
//      the engine path actually invoked metrics methods (regression
//      guard against the engine silently bypassing metrics in replay).
//
//   3. PanicAuth + PanicWatchlist — these repos sit OUTSIDE the engine
//      path under D8. They're never invoked by *valuation.Service.
//      Per spec, the stub panics on call so a future engine refactor
//      that accidentally consults them surfaces immediately. Panic is
//      safe here because these methods are not invoked from the
//      datafetcher coordinator goroutines (F11 only mandates no-panic
//      for goroutine-reachable code).
//
// All NotFound returns mirror the production sentinel string used by the
// in-memory cache repository (`cache key not found: <key>` /
// "no <entity>" patterns). Cache callers in production check `err == nil`
// for a hit; any non-nil error is treated as a miss. We preserve that
// idiom — replay's Get returns a non-nil error and the engine paths
// silently miss into the bundle-gateway path.

// notFoundCacheRepo is the replay's Cache implementation. Every Get
// reports "cache key not found"; Set is a no-op. Behaves like an
// always-cold cache, forcing the engine to consult the bundle gateways
// for every read. This is exactly what we want — replay must never
// short-circuit into a stale cached value.
type notFoundCacheRepo struct{}

// NewNotFoundCacheRepo constructs the replay cache stub.
func NewNotFoundCacheRepo() ports.CacheRepository {
	return &notFoundCacheRepo{}
}

func (r *notFoundCacheRepo) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	return nil
}

func (r *notFoundCacheRepo) Get(ctx context.Context, key string, dest interface{}) error {
	return fmt.Errorf("cache key not found: %s", key)
}

func (r *notFoundCacheRepo) Delete(ctx context.Context, key string) error {
	return nil
}

func (r *notFoundCacheRepo) Exists(ctx context.Context, key string) (bool, error) {
	return false, nil
}

func (r *notFoundCacheRepo) SetNX(ctx context.Context, key string, value interface{}, ttl time.Duration) (bool, error) {
	return true, nil
}

func (r *notFoundCacheRepo) GetKeys(ctx context.Context, pattern string) ([]string, error) {
	return nil, nil
}

func (r *notFoundCacheRepo) DeletePattern(ctx context.Context, pattern string) error {
	return nil
}

// notFoundFinancialDataRepo always reports "no historical data" so the
// engine falls through to the data-fetcher / bundle-gateway path.
type notFoundFinancialDataRepo struct{}

// NewNotFoundFinancialDataRepo constructs the replay financial-data repo
// stub.
func NewNotFoundFinancialDataRepo() ports.FinancialDataRepository {
	return &notFoundFinancialDataRepo{}
}

func (r *notFoundFinancialDataRepo) Store(ctx context.Context, data *entities.FinancialData) error {
	return nil
}

func (r *notFoundFinancialDataRepo) GetLatest(ctx context.Context, ticker string) (*entities.FinancialData, error) {
	return nil, fmt.Errorf("no financial data for ticker %s", ticker)
}

func (r *notFoundFinancialDataRepo) GetHistorical(ctx context.Context, ticker string, periods int) (*entities.HistoricalFinancialData, error) {
	// Return an empty struct so service.go's len(historicalData.Data)==0
	// branch fires correctly — the engine consults dataFetcher in that
	// branch, which routes to the bundle gateways.
	return &entities.HistoricalFinancialData{
		Ticker: ticker,
		Data:   map[string]*entities.FinancialData{},
	}, nil
}

func (r *notFoundFinancialDataRepo) GetByPeriod(ctx context.Context, ticker, period string) (*entities.FinancialData, error) {
	return nil, fmt.Errorf("no financial data for ticker %s period %s", ticker, period)
}

func (r *notFoundFinancialDataRepo) StoreHistorical(ctx context.Context, data *entities.HistoricalFinancialData) error {
	return nil
}

func (r *notFoundFinancialDataRepo) GetLastUpdated(ctx context.Context, ticker string) (time.Time, error) {
	return time.Time{}, fmt.Errorf("no financial data for ticker %s", ticker)
}

// notFoundMarketDataRepo: as above, but for market data.
type notFoundMarketDataRepo struct{}

// NewNotFoundMarketDataRepo constructs the replay market-data repo stub.
func NewNotFoundMarketDataRepo() ports.MarketDataRepository {
	return &notFoundMarketDataRepo{}
}

func (r *notFoundMarketDataRepo) Store(ctx context.Context, data *entities.MarketData) error {
	return nil
}

func (r *notFoundMarketDataRepo) GetLatest(ctx context.Context, ticker string) (*entities.MarketData, error) {
	return nil, fmt.Errorf("no market data for ticker %s", ticker)
}

func (r *notFoundMarketDataRepo) GetBatch(ctx context.Context, tickers []string) (map[string]*entities.MarketData, error) {
	return map[string]*entities.MarketData{}, nil
}

func (r *notFoundMarketDataRepo) IsStale(ctx context.Context, ticker string, maxAge time.Duration) (bool, error) {
	return true, nil
}

func (r *notFoundMarketDataRepo) GetLastUpdated(ctx context.Context, ticker string) (time.Time, error) {
	return time.Time{}, fmt.Errorf("no market data for ticker %s", ticker)
}

// notFoundMacroDataRepo: as above, but for macro data.
type notFoundMacroDataRepo struct{}

// NewNotFoundMacroDataRepo constructs the replay macro-data repo stub.
func NewNotFoundMacroDataRepo() ports.MacroDataRepository {
	return &notFoundMacroDataRepo{}
}

func (r *notFoundMacroDataRepo) Store(ctx context.Context, data *entities.MacroData) error {
	return nil
}

func (r *notFoundMacroDataRepo) GetLatest(ctx context.Context) (*entities.MacroData, error) {
	// Wrap ErrBundleMissingPayload so the cmd/replay CLI's
	// annotateMissingPayloadHint catches this error class and appends
	// the "(hint: try --from=parsed)" suggestion when the operator is
	// on the default --from=raw mode. Without the wrap, valuation's
	// `failed to fetch macro data: %w` chain surfaces a bare
	// "no macro data" string and the operator has no actionable next
	// step from the error message alone (observed in live use 2026-05-11).
	//
	// Repository semantics are preserved: the engine still treats this
	// as a fatal repo miss; the only difference is that errors.Is on the
	// wrapped chain now matches the bundle-missing sentinel.
	return nil, fmt.Errorf("no macro data: %w", ErrBundleMissingPayload)
}

func (r *notFoundMacroDataRepo) IsStale(ctx context.Context, maxAge time.Duration) (bool, error) {
	return true, nil
}

// notFoundTickerMappingRepo: ticker→CIK mapping is consulted by some
// engine paths; replay returns "not found" so the bundle gateways have
// authoritative answers.
type notFoundTickerMappingRepo struct{}

// NewNotFoundTickerMappingRepo constructs the replay ticker-mapping stub.
func NewNotFoundTickerMappingRepo() ports.TickerMappingRepository {
	return &notFoundTickerMappingRepo{}
}

func (r *notFoundTickerMappingRepo) GetCIK(ctx context.Context, ticker string) (string, error) {
	return "", fmt.Errorf("no CIK mapping for ticker %s", ticker)
}

func (r *notFoundTickerMappingRepo) GetTicker(ctx context.Context, cik string) (string, error) {
	return "", fmt.Errorf("no ticker mapping for CIK %s", cik)
}

func (r *notFoundTickerMappingRepo) Store(ctx context.Context, ticker, cik string) error {
	return nil
}

func (r *notFoundTickerMappingRepo) BulkStore(ctx context.Context, mappings map[string]string) error {
	return nil
}

func (r *notFoundTickerMappingRepo) GetAllMappings(ctx context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}

func (r *notFoundTickerMappingRepo) LoadFromSEC(ctx context.Context) error {
	return nil
}

// panicWatchlistRepo: scheduler-only surface. Replay must never reach it;
// if it does, a panic surfaces immediately so we know engine wiring drifted.
// Panic is safe because none of these methods sit inside a coordinator
// goroutine (F11's no-panic rule applies only to goroutine-reachable code).
type panicWatchlistRepo struct{}

// NewPanicWatchlistRepo constructs the replay watchlist panic-stub.
func NewPanicWatchlistRepo() ports.WatchlistRepository {
	return &panicWatchlistRepo{}
}

func (r *panicWatchlistRepo) GetActiveWatchlist(ctx context.Context, filter *entities.WatchlistFilter) ([]*entities.WatchlistEntry, error) {
	panic("replay: watchlist repository must not be reached during replay")
}

func (r *panicWatchlistRepo) GetAll(ctx context.Context, filter *entities.WatchlistFilter) ([]*entities.WatchlistEntry, error) {
	panic("replay: watchlist repository must not be reached during replay")
}

func (r *panicWatchlistRepo) GetByTicker(ctx context.Context, ticker string) (*entities.WatchlistEntry, error) {
	panic("replay: watchlist repository must not be reached during replay")
}

func (r *panicWatchlistRepo) Add(ctx context.Context, entry *entities.WatchlistEntry) error {
	panic("replay: watchlist repository must not be reached during replay")
}

func (r *panicWatchlistRepo) Update(ctx context.Context, ticker string, updates *entities.UpdateWatchlistEntryRequest) error {
	panic("replay: watchlist repository must not be reached during replay")
}

func (r *panicWatchlistRepo) Remove(ctx context.Context, ticker string) error {
	panic("replay: watchlist repository must not be reached during replay")
}

func (r *panicWatchlistRepo) RecordSuccess(ctx context.Context, ticker string, fetchedAt time.Time) error {
	panic("replay: watchlist repository must not be reached during replay")
}

func (r *panicWatchlistRepo) RecordFailure(ctx context.Context, ticker string) error {
	panic("replay: watchlist repository must not be reached during replay")
}

func (r *panicWatchlistRepo) GetStats(ctx context.Context) (*entities.WatchlistStats, error) {
	panic("replay: watchlist repository must not be reached during replay")
}

func (r *panicWatchlistRepo) BulkUpdateFailures(ctx context.Context, failures map[string]bool) error {
	panic("replay: watchlist repository must not be reached during replay")
}

// noOpMetricsService is the replay metrics implementation. Every method
// is a no-op except CallsCount, which increments atomically and is
// exposed for module_test.go to assert "the engine actually called
// metrics" (a regression guard against the engine bypassing metrics in
// replay, which would mask a real production drift).
type noOpMetricsService struct {
	calls uint64
}

// NewNoOpMetricsService constructs the replay metrics stub.
func NewNoOpMetricsService() ports.MetricsService {
	return &noOpMetricsService{}
}

// CallsCount returns the cumulative number of metrics-method invocations.
// Test-only.
func (s *noOpMetricsService) CallsCount() uint64 {
	return atomic.LoadUint64(&s.calls)
}

func (s *noOpMetricsService) RecordHTTPRequest(method, endpoint string, statusCode int, duration time.Duration, responseSize int) {
	atomic.AddUint64(&s.calls, 1)
}
func (s *noOpMetricsService) IncHTTPRequestsInFlight() { atomic.AddUint64(&s.calls, 1) }
func (s *noOpMetricsService) DecHTTPRequestsInFlight() { atomic.AddUint64(&s.calls, 1) }
func (s *noOpMetricsService) IncDCFCalculations()      { atomic.AddUint64(&s.calls, 1) }
func (s *noOpMetricsService) IncWACCCalculations()     { atomic.AddUint64(&s.calls, 1) }
func (s *noOpMetricsService) RecordValuationRequest(ticker, requestType, status string, duration time.Duration) {
	atomic.AddUint64(&s.calls, 1)
}
func (s *noOpMetricsService) RecordValuationError(ticker, errorType string) {
	atomic.AddUint64(&s.calls, 1)
}
func (s *noOpMetricsService) RecordSECAPIRequest(endpoint, status string) {
	atomic.AddUint64(&s.calls, 1)
}
func (s *noOpMetricsService) RecordMarketAPIRequest(provider, status string) {
	atomic.AddUint64(&s.calls, 1)
}
func (s *noOpMetricsService) RecordMacroAPIRequest(provider, status string) {
	atomic.AddUint64(&s.calls, 1)
}
func (s *noOpMetricsService) RecordDataFetch(source, ticker string, duration time.Duration) {
	atomic.AddUint64(&s.calls, 1)
}
func (s *noOpMetricsService) RecordCacheRequest(cacheType, operation, result string) {
	atomic.AddUint64(&s.calls, 1)
}
func (s *noOpMetricsService) SetCacheHitRatio(cacheType string, ratio float64) {
	atomic.AddUint64(&s.calls, 1)
}
func (s *noOpMetricsService) SetAverageWACC(wacc float64) { atomic.AddUint64(&s.calls, 1) }
func (s *noOpMetricsService) SetAverageGrowthRate(rate float64) {
	atomic.AddUint64(&s.calls, 1)
}
func (s *noOpMetricsService) GetTotalRequests() int64         { return 0 }
func (s *noOpMetricsService) GetActiveConnections() int       { return 0 }
func (s *noOpMetricsService) GetAverageResponseTime() float64 { return 0 }
func (s *noOpMetricsService) GetErrorRate() float64           { return 0 }
func (s *noOpMetricsService) GetCacheHitRate() float64        { return 0 }
func (s *noOpMetricsService) GetTotalValuations() int64       { return 0 }
func (s *noOpMetricsService) GetSuccessfulValuations() int64  { return 0 }
func (s *noOpMetricsService) GetFailedValuations() int64      { return 0 }
func (s *noOpMetricsService) GetAverageWACC() float64         { return 0 }
func (s *noOpMetricsService) GetAverageGrowthRate() float64   { return 0 }
func (s *noOpMetricsService) GetUniqueTickersServed() int64   { return 0 }
func (s *noOpMetricsService) HealthCheck() error              { return nil }

// AsNoOpMetricsService is a typed accessor used by tests + module_test
// to retrieve the underlying *noOpMetricsService for CallsCount inspection.
// Returns nil when m is not the replay no-op type — callers should treat
// nil as "wrong service installed" in assertions.
func AsNoOpMetricsService(m ports.MetricsService) *noOpMetricsService {
	if op, ok := m.(*noOpMetricsService); ok {
		return op
	}
	return nil
}
