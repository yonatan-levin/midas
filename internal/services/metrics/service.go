package metrics

import (
	"runtime"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"
)

// Metrics tracking state
type MetricsState struct {
	totalRequests        int64
	totalErrors          int64
	totalResponseTime    float64
	activeConnections    int
	totalValuations      int64
	successfulValuations int64
	failedValuations     int64
	averageWACC          float64
	averageGrowthRate    float64
	uniqueTickers        map[string]bool
	cacheHitRate         float64
}

// Service provides Prometheus metrics collection for the DCF API
type Service struct {
	logger    *zap.Logger
	startTime time.Time
	state     *MetricsState

	// HTTP Metrics
	httpRequestsTotal     *prometheus.CounterVec
	httpRequestDuration   *prometheus.HistogramVec
	httpRequestsInFlight  prometheus.Gauge
	httpResponseSizeBytes *prometheus.HistogramVec

	// API-specific Metrics
	valuationRequestsTotal *prometheus.CounterVec
	valuationDuration      *prometheus.HistogramVec
	valuationErrorsTotal   *prometheus.CounterVec
	dcfCalculationsTotal   prometheus.Counter
	waccCalculationsTotal  prometheus.Counter

	// Data Source Metrics
	secAPIRequestsTotal    *prometheus.CounterVec
	marketAPIRequestsTotal *prometheus.CounterVec
	macroAPIRequestsTotal  *prometheus.CounterVec
	dataFetchDuration      *prometheus.HistogramVec
	dataCleaningDuration   *prometheus.HistogramVec

	// Datacleaner adjustment counter (TDB-4). Bounded labels only — NEVER
	// ticker (high-cardinality; ticker lives in the audit log instead).
	datacleanerAdjustmentsTotal *prometheus.CounterVec

	// Cache Metrics
	cacheRequestsTotal *prometheus.CounterVec
	cacheHitRatio      *prometheus.GaugeVec
	cacheSize          *prometheus.GaugeVec

	// Rate Limiting Metrics
	rateLimitRequestsTotal *prometheus.CounterVec
	rateLimitRejectsTotal  *prometheus.CounterVec

	// Business Metrics
	uniqueTickersServed prometheus.Gauge
	averageWACC         prometheus.Gauge
	averageGrowthRate   prometheus.Gauge
	dataQualityScore    *prometheus.HistogramVec

	// System Metrics
	systemInfo    *prometheus.GaugeVec
	processUptime prometheus.Gauge

	// Database Metrics
	dbConnectionsInUse prometheus.Gauge
	dbConnectionsIdle  prometheus.Gauge
	dbConnectionsOpen  prometheus.Gauge
	dbQueryDuration    *prometheus.HistogramVec

	// Service-owned Prometheus registry. Not nil after construction.
	// Exposed via GetRegistry() so server.go serves /metrics from this
	// registry (not prometheus.DefaultRegisterer), avoiding collisions
	// with Go runtime collectors (e.g. Midas defines its own "go_info"
	// gauge, PREX-1).
	registry *prometheus.Registry
}

// NewService creates a new metrics service with Prometheus metrics registered
// on a fresh per-service registry. Prior behavior (pre-PREX-1 fix) relied on
// promauto.Factory{} (zero value) which silently dropped every registration
// — /metrics served only Go runtime data. Every Midas-specific series was
// missing.
func NewService(logger *zap.Logger) *Service {
	return NewServiceWithRegistry(logger, nil)
}

// NewServiceWithRegistry creates a new metrics service. If registry is nil,
// a fresh *prometheus.Registry is allocated and owned by the service, and
// Go runtime + process collectors are registered on it so /metrics surfaces
// go_goroutines, go_memstats_*, process_cpu_seconds_total, etc. — matching
// what an operator expects from a typical Prometheus-instrumented Go service.
//
// Caller-supplied registries are NOT augmented (the caller already owns the
// registration policy — common in tests that want strict isolation).
func NewServiceWithRegistry(logger *zap.Logger, registry *prometheus.Registry) *Service {
	if registry == nil {
		registry = prometheus.NewRegistry()
		registry.MustRegister(collectors.NewGoCollector())
		registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	}
	s := &Service{
		logger:    logger,
		startTime: time.Now(),
		state: &MetricsState{
			uniqueTickers: make(map[string]bool),
		},
		registry: registry,
	}

	s.initMetrics(registry)

	return s
}

// initMetrics initializes all Prometheus metrics on the service-owned registry.
// All registrations are via promauto.With(registry) so construction-time
// registration failures surface immediately instead of being silently dropped
// (PREX-1). The registry is guaranteed non-nil by NewServiceWithRegistry.
func (s *Service) initMetrics(registry *prometheus.Registry) {
	factory := promauto.With(registry)

	// HTTP Metrics
	s.httpRequestsTotal = factory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests processed",
		},
		[]string{"method", "endpoint", "status_code"},
	)

	s.httpRequestDuration = factory.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "endpoint"},
	)

	s.httpRequestsInFlight = factory.NewGauge(prometheus.GaugeOpts{
		Name: "http_requests_in_flight",
		Help: "Number of HTTP requests currently being processed",
	})

	s.httpResponseSizeBytes = factory.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_response_size_bytes",
			Help:    "HTTP response size in bytes",
			Buckets: []float64{100, 1000, 10000, 100000, 1000000},
		},
		[]string{"method", "endpoint"},
	)

	// API-specific Metrics
	s.valuationRequestsTotal = factory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dcf_valuation_requests_total",
			Help: "Total number of DCF valuation requests",
		},
		[]string{"ticker", "request_type", "status"},
	)

	s.valuationDuration = factory.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "dcf_valuation_duration_seconds",
			Help:    "Time spent calculating DCF valuations",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0},
		},
		[]string{"ticker", "request_type"},
	)

	s.valuationErrorsTotal = factory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dcf_valuation_errors_total",
			Help: "Total number of DCF valuation errors",
		},
		[]string{"ticker", "error_type"},
	)

	s.dcfCalculationsTotal = factory.NewCounter(prometheus.CounterOpts{
		Name: "dcf_calculations_total",
		Help: "Total number of DCF calculations performed",
	})

	s.waccCalculationsTotal = factory.NewCounter(prometheus.CounterOpts{
		Name: "wacc_calculations_total",
		Help: "Total number of WACC calculations performed",
	})

	// Data Source Metrics
	s.secAPIRequestsTotal = factory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "sec_api_requests_total",
			Help: "Total number of SEC API requests",
		},
		[]string{"endpoint", "status"},
	)

	s.marketAPIRequestsTotal = factory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "market_api_requests_total",
			Help: "Total number of market data API requests",
		},
		[]string{"provider", "status"},
	)

	s.macroAPIRequestsTotal = factory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "macro_api_requests_total",
			Help: "Total number of macro data API requests",
		},
		[]string{"provider", "status"},
	)

	s.dataFetchDuration = factory.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "data_fetch_duration_seconds",
			Help:    "Time spent fetching data from external sources",
			Buckets: []float64{0.1, 0.5, 1.0, 2.0, 5.0, 10.0, 30.0},
		},
		[]string{"source", "ticker"},
	)

	s.dataCleaningDuration = factory.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "data_cleaning_duration_seconds",
			Help:    "Time spent cleaning and normalizing financial data",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5},
		},
		[]string{"ticker", "industry"},
	)

	// Datacleaner adjustment counter (TDB-4). Labels are bounded by code: the
	// fixed adjuster set (rule_id ~16-20), the three rule categories, and the
	// AdjustmentType enum (~7). No ticker label — cardinality lives in the
	// audit log, not the metric (see spec §5).
	s.datacleanerAdjustmentsTotal = factory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "datacleaner_adjustments_total",
			Help: "Total datacleaner adjustments applied, by rule/category/type",
		},
		[]string{"rule_id", "category", "type"},
	)

	// Cache Metrics
	s.cacheRequestsTotal = factory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cache_requests_total",
			Help: "Total number of cache requests",
		},
		[]string{"cache_type", "operation", "result"},
	)

	s.cacheHitRatio = factory.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cache_hit_ratio",
			Help: "Cache hit ratio (0-1)",
		},
		[]string{"cache_type"},
	)

	s.cacheSize = factory.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cache_size_items",
			Help: "Number of items in cache",
		},
		[]string{"cache_type"},
	)

	// Rate Limiting Metrics
	s.rateLimitRequestsTotal = factory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rate_limit_requests_total",
			Help: "Total number of rate limit checks",
		},
		[]string{"limit_type", "api_key"},
	)

	s.rateLimitRejectsTotal = factory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rate_limit_rejects_total",
			Help: "Total number of requests rejected by rate limiter",
		},
		[]string{"limit_type", "reason"},
	)

	// Business Metrics
	s.uniqueTickersServed = factory.NewGauge(prometheus.GaugeOpts{
		Name: "unique_tickers_served_total",
		Help: "Total number of unique tickers served",
	})

	s.averageWACC = factory.NewGauge(prometheus.GaugeOpts{
		Name: "average_wacc",
		Help: "Average WACC across all calculated valuations",
	})

	s.averageGrowthRate = factory.NewGauge(prometheus.GaugeOpts{
		Name: "average_growth_rate",
		Help: "Average growth rate across all calculated valuations",
	})

	s.dataQualityScore = factory.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "data_quality_score",
			Help:    "Data quality score distribution (0-1)",
			Buckets: []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
		},
		[]string{"ticker", "industry"},
	)

	// System Metrics
	s.systemInfo = factory.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "system_info",
			Help: "System information",
		},
		[]string{"version", "go_version", "build_date"},
	)

	// go_info is provided by collectors.NewGoCollector(), registered in
	// NewServiceWithRegistry on owned registries. The previous in-house
	// gauge here duplicated the standard collector's series (same name,
	// same "version" label) and was silently dropped pre-PREX-1 by
	// promauto.Factory{}'s nil registerer; with PREX-1 it would have
	// collided with the runtime collector. Removed in favor of the
	// authoritative standard collector.

	s.processUptime = factory.NewGauge(prometheus.GaugeOpts{
		Name: "process_uptime_seconds",
		Help: "Process uptime in seconds",
	})

	// Database Metrics
	s.dbConnectionsInUse = factory.NewGauge(prometheus.GaugeOpts{
		Name: "db_connections_in_use",
		Help: "Number of database connections currently in use",
	})

	s.dbConnectionsIdle = factory.NewGauge(prometheus.GaugeOpts{
		Name: "db_connections_idle",
		Help: "Number of idle database connections",
	})

	s.dbConnectionsOpen = factory.NewGauge(prometheus.GaugeOpts{
		Name: "db_connections_open",
		Help: "Total number of open database connections",
	})

	s.dbQueryDuration = factory.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "db_query_duration_seconds",
			Help:    "Database query duration in seconds",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0},
		},
		[]string{"operation", "table"},
	)

	// Initialize system info — go_info is now provided by the standard Go
	// collector registered in NewServiceWithRegistry, so no manual Set call.
	s.systemInfo.WithLabelValues("v1.0.0", runtime.Version(), time.Now().Format("2006-01-02")).Set(1)

	s.logger.Info("Prometheus metrics initialized",
		zap.Int("total_metrics", s.getMetricsCount()))
}

// HTTP Metrics Methods
func (s *Service) RecordHTTPRequest(method, endpoint string, statusCode int, duration time.Duration, responseSize int) {
	// Update Prometheus metrics
	s.httpRequestsTotal.WithLabelValues(method, endpoint, strconv.Itoa(statusCode)).Inc()
	s.httpRequestDuration.WithLabelValues(method, endpoint).Observe(duration.Seconds())
	s.httpResponseSizeBytes.WithLabelValues(method, endpoint).Observe(float64(responseSize))

	// Update internal state for health checks
	s.state.totalRequests++
	s.state.totalResponseTime += duration.Seconds() * 1000 // Convert to milliseconds
	if statusCode >= 400 {
		s.state.totalErrors++
	}
}

func (s *Service) IncHTTPRequestsInFlight() {
	s.httpRequestsInFlight.Inc()
}

func (s *Service) DecHTTPRequestsInFlight() {
	s.httpRequestsInFlight.Dec()
}

// Valuation Metrics Methods
func (s *Service) RecordValuationRequest(ticker, requestType, status string, duration time.Duration) {
	s.valuationRequestsTotal.WithLabelValues(ticker, requestType, status).Inc()
	s.valuationDuration.WithLabelValues(ticker, requestType).Observe(duration.Seconds())

	// Update internal state
	s.state.totalValuations++
	if status == "success" {
		s.state.successfulValuations++
	} else {
		s.state.failedValuations++
	}

	// Track unique tickers
	if s.state.uniqueTickers == nil {
		s.state.uniqueTickers = make(map[string]bool)
	}
	s.state.uniqueTickers[ticker] = true
}

func (s *Service) RecordValuationError(ticker, errorType string) {
	s.valuationErrorsTotal.WithLabelValues(ticker, errorType).Inc()
	s.state.failedValuations++
}

func (s *Service) IncDCFCalculations() {
	s.dcfCalculationsTotal.Inc()
}

func (s *Service) IncWACCCalculations() {
	s.waccCalculationsTotal.Inc()
}

// Data Source Metrics Methods
func (s *Service) RecordSECAPIRequest(endpoint, status string) {
	s.secAPIRequestsTotal.WithLabelValues(endpoint, status).Inc()
}

func (s *Service) RecordMarketAPIRequest(provider, status string) {
	s.marketAPIRequestsTotal.WithLabelValues(provider, status).Inc()
}

func (s *Service) RecordMacroAPIRequest(provider, status string) {
	s.macroAPIRequestsTotal.WithLabelValues(provider, status).Inc()
}

func (s *Service) RecordDataFetch(source, ticker string, duration time.Duration) {
	s.dataFetchDuration.WithLabelValues(source, ticker).Observe(duration.Seconds())
}

func (s *Service) RecordDataCleaning(ticker, industry string, duration time.Duration) {
	s.dataCleaningDuration.WithLabelValues(ticker, industry).Observe(duration.Seconds())
}

// RecordAdjustment counts one fired datacleaner adjustment (TDB-4). Labels are
// bounded (rule_id/category/type) — never ticker. Satisfies the datacleaner's
// AdjustmentMetrics port so *metrics.Service can be injected without the
// datacleaner taking a hard import on this package.
func (s *Service) RecordAdjustment(ruleID, category, adjType string) {
	s.datacleanerAdjustmentsTotal.WithLabelValues(ruleID, category, adjType).Inc()
}

// Cache Metrics Methods
func (s *Service) RecordCacheRequest(cacheType, operation, result string) {
	s.cacheRequestsTotal.WithLabelValues(cacheType, operation, result).Inc()
}

func (s *Service) SetCacheHitRatio(cacheType string, ratio float64) {
	s.cacheHitRatio.WithLabelValues(cacheType).Set(ratio)
	// Update internal state for health checks
	s.state.cacheHitRate = ratio
}

func (s *Service) SetCacheSize(cacheType string, size float64) {
	s.cacheSize.WithLabelValues(cacheType).Set(size)
}

// Rate Limiting Metrics Methods
func (s *Service) RecordRateLimitRequest(limitType, apiKey string) {
	s.rateLimitRequestsTotal.WithLabelValues(limitType, apiKey).Inc()
}

func (s *Service) RecordRateLimitReject(limitType, reason string) {
	s.rateLimitRejectsTotal.WithLabelValues(limitType, reason).Inc()
}

// Business Metrics Methods
func (s *Service) SetUniqueTickersServed(count float64) {
	s.uniqueTickersServed.Set(count)
}

func (s *Service) SetAverageWACC(wacc float64) {
	s.averageWACC.Set(wacc)
}

func (s *Service) SetAverageGrowthRate(rate float64) {
	s.averageGrowthRate.Set(rate)
}

func (s *Service) RecordDataQuality(ticker, industry string, score float64) {
	s.dataQualityScore.WithLabelValues(ticker, industry).Observe(score)
}

// Database Metrics Methods
func (s *Service) UpdateDBStats(inUse, idle, open int) {
	s.dbConnectionsInUse.Set(float64(inUse))
	s.dbConnectionsIdle.Set(float64(idle))
	s.dbConnectionsOpen.Set(float64(open))
}

func (s *Service) RecordDBQuery(operation, table string, duration time.Duration) {
	s.dbQueryDuration.WithLabelValues(operation, table).Observe(duration.Seconds())
}

// System Metrics Methods
func (s *Service) UpdateSystemMetrics() {
	// Update uptime
	s.processUptime.Set(time.Since(s.startTime).Seconds())
}

// GetRegistry returns the default Prometheus registry
// GetRegistry returns the service-owned Prometheus registry holding all
// Midas-specific metrics. The /metrics HTTP handler serves from this
// registry (see internal/api/server.go), isolated from prometheus.Default
// Registerer so no collision with Go runtime collectors.
func (s *Service) GetRegistry() *prometheus.Registry {
	return s.registry
}

// getMetricsCount returns the total number of metrics registered
func (s *Service) getMetricsCount() int {
	// Count all metric families (approximation)
	return 29 // Approximate count of metric families defined above
}

// Health check for metrics service
func (s *Service) HealthCheck() error {
	// Simple health check - verify metrics are responsive
	s.UpdateSystemMetrics()
	return nil
}

// Real Metrics Collection Methods for Health Handler

// GetTotalRequests returns the total number of HTTP requests processed
func (s *Service) GetTotalRequests() int64 {
	return s.state.totalRequests
}

// GetActiveConnections returns the current number of active connections
func (s *Service) GetActiveConnections() int {
	return s.state.activeConnections
}

// GetAverageResponseTime returns the average response time in milliseconds
func (s *Service) GetAverageResponseTime() float64 {
	if s.state.totalRequests == 0 {
		return 0
	}
	return s.state.totalResponseTime / float64(s.state.totalRequests)
}

// GetErrorRate returns the error rate as a percentage (0.0 to 1.0)
func (s *Service) GetErrorRate() float64 {
	if s.state.totalRequests == 0 {
		return 0
	}
	return float64(s.state.totalErrors) / float64(s.state.totalRequests)
}

// GetCacheHitRate returns the cache hit rate
func (s *Service) GetCacheHitRate() float64 {
	return s.state.cacheHitRate
}

// GetTotalValuations returns the total number of valuations performed
func (s *Service) GetTotalValuations() int64 {
	return s.state.totalValuations
}

// GetSuccessfulValuations returns the number of successful valuations
func (s *Service) GetSuccessfulValuations() int64 {
	return s.state.successfulValuations
}

// GetFailedValuations returns the number of failed valuations
func (s *Service) GetFailedValuations() int64 {
	return s.state.failedValuations
}

// GetAverageWACC returns the average WACC across all calculations
func (s *Service) GetAverageWACC() float64 {
	return s.state.averageWACC
}

// GetAverageGrowthRate returns the average growth rate across all calculations
func (s *Service) GetAverageGrowthRate() float64 {
	return s.state.averageGrowthRate
}

// GetUniqueTickersServed returns the number of unique tickers that have been processed
func (s *Service) GetUniqueTickersServed() int64 {
	return int64(len(s.state.uniqueTickers))
}
