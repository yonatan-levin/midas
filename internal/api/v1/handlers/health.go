package handlers

import (
	"context"
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
	"github.com/midas/dcf-valuation-api/internal/services/ratelimit"
)

// HealthHandler handles health check and metrics endpoints
type HealthHandler struct {
	// logger is retained for non-request contexts; request-path log sites use logctx.From(ctx)
	logger         *zap.Logger
	startTime      time.Time
	db             *sqlx.DB
	redis          *redis.Client
	cache          ports.CacheRepository
	rateLimiter    *ratelimit.RateLimiter
	secGateway     ports.SECGateway
	marketGateway  ports.MarketDataGateway
	macroGateway   ports.MacroDataGateway
	metricsService ports.MetricsService // Added metricsService field
}

// NewHealthHandler creates a new HealthHandler instance with dependencies
func NewHealthHandler(
	logger *zap.Logger,
	db *sqlx.DB,
	redis *redis.Client,
	cache ports.CacheRepository,
	rateLimiter *ratelimit.RateLimiter,
	secGateway ports.SECGateway,
	marketGateway ports.MarketDataGateway,
	macroGateway ports.MacroDataGateway,
	metricsService ports.MetricsService, // Added metricsService to NewHealthHandler
) *HealthHandler {
	return &HealthHandler{
		logger:         logger,
		startTime:      time.Now(),
		db:             db,
		redis:          redis,
		cache:          cache,
		rateLimiter:    rateLimiter,
		secGateway:     secGateway,
		marketGateway:  marketGateway,
		macroGateway:   macroGateway,
		metricsService: metricsService, // Initialize metricsService
	}
}

// DetailedHealthCheckResponse represents detailed health check response
type DetailedHealthCheckResponse struct {
	Status    string                 `json:"status"`
	Timestamp time.Time              `json:"timestamp"`
	Service   string                 `json:"service"`
	Version   string                 `json:"version"`
	Uptime    string                 `json:"uptime"`
	Checks    map[string]HealthCheck `json:"checks"`
	Metadata  map[string]interface{} `json:"metadata"`
}

// HealthCheck represents an individual health check
type HealthCheck struct {
	Status      string                 `json:"status"` // "healthy", "unhealthy", "degraded"
	LastChecked time.Time              `json:"last_checked"`
	Duration    time.Duration          `json:"duration"`
	Message     string                 `json:"message,omitempty"`
	Details     map[string]interface{} `json:"details,omitempty"`
}

// MetricsResponse represents system metrics
type MetricsResponse struct {
	System      SystemMetrics      `json:"system"`
	Application ApplicationMetrics `json:"application"`
	Business    BusinessMetrics    `json:"business"`
	Timestamp   time.Time          `json:"timestamp"`
}

// SystemMetrics represents system-level metrics
type SystemMetrics struct {
	GoVersion     string `json:"go_version"`
	NumGoroutines int    `json:"num_goroutines"`
	NumCPU        int    `json:"num_cpu"`
	MemoryAlloc   uint64 `json:"memory_alloc"`
	MemoryTotal   uint64 `json:"memory_total"`
	MemorySys     uint64 `json:"memory_sys"`
	GCCount       uint32 `json:"gc_count"`
	LastGC        string `json:"last_gc"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}

// ApplicationMetrics represents application-level metrics
type ApplicationMetrics struct {
	TotalRequests       int64   `json:"total_requests"`
	ActiveConnections   int     `json:"active_connections"`
	AverageResponseTime float64 `json:"average_response_time"`
	ErrorRate           float64 `json:"error_rate"`
	CacheHitRate        float64 `json:"cache_hit_rate"`
	DatabaseConnections int     `json:"database_connections"`
}

// BusinessMetrics represents business-specific metrics
type BusinessMetrics struct {
	TotalValuations      int64   `json:"total_valuations"`
	SuccessfulValuations int64   `json:"successful_valuations"`
	FailedValuations     int64   `json:"failed_valuations"`
	AverageWACC          float64 `json:"average_wacc"`
	AverageGrowthRate    float64 `json:"average_growth_rate"`
	UniqueTickersServed  int64   `json:"unique_tickers_served"`
}

// HealthCheckHandler handles GET /health (simple check)
func (h *HealthHandler) HealthCheckHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"timestamp": time.Now().UTC(),
		"service":   "dcf-valuation-api",
		"uptime":    time.Since(h.startTime).String(),
	})
}

// DetailedHealthCheck handles GET /api/v1/health/detailed
// @Summary      Detailed health check
// @Description  Reports component-level health for the database, cache, external APIs (SEC/market/macro), memory, and rate limiter, plus an aggregate status. Returns 200 when healthy, 206 (Partial Content) when degraded, and 503 when any component is unhealthy.
// @Tags         health
// @Produce      json
// @Security     ApiKeyAuth
// @Success      200  {object}  DetailedHealthCheckResponse  "All components healthy"
// @Success      206  {object}  DetailedHealthCheckResponse  "One or more components degraded"
// @Failure      401  {object}  ErrorResponse                "Missing or invalid API key"
// @Failure      403  {object}  ErrorResponse                "Insufficient permissions"
// @Failure      429  {object}  ErrorResponse                "Rate limit exceeded"
// @Failure      503  {object}  DetailedHealthCheckResponse  "One or more components unhealthy"
// @Router       /health/detailed [get]
func (h *HealthHandler) DetailedHealthCheck(c *gin.Context) {
	startTime := time.Now()
	ctx := c.Request.Context()

	checks := make(map[string]HealthCheck)

	// Database health check
	checks["database"] = h.checkDatabase(ctx)

	// Cache health check
	checks["cache"] = h.checkCache(ctx)

	// External APIs health check
	checks["external_apis"] = h.checkExternalAPIs(ctx)

	// Memory health check
	checks["memory"] = h.checkMemory()

	// Rate limiter health check
	checks["rate_limiter"] = h.checkRateLimiter(ctx)

	// Determine overall status
	overallStatus := "healthy"
	for _, check := range checks {
		if check.Status == "unhealthy" {
			overallStatus = "unhealthy"
			break
		} else if check.Status == "degraded" && overallStatus == "healthy" {
			overallStatus = "degraded"
		}
	}

	response := DetailedHealthCheckResponse{
		Status:    overallStatus,
		Timestamp: time.Now().UTC(),
		Service:   "dcf-valuation-api",
		Version:   "v1.0.0", // TODO: Get from config
		Uptime:    time.Since(h.startTime).String(),
		Checks:    checks,
		Metadata: map[string]interface{}{
			"check_duration_ms": time.Since(startTime).Milliseconds(),
			"go_version":        runtime.Version(),
			"num_goroutines":    runtime.NumGoroutine(),
		},
	}

	// Return appropriate HTTP status based on health
	statusCode := http.StatusOK
	// nolint:staticcheck // simple if–else chain sufficient here
	if overallStatus == "unhealthy" {
		statusCode = http.StatusServiceUnavailable
	} else if overallStatus == "degraded" {
		statusCode = http.StatusPartialContent
	}

	logctx.From(c.Request.Context()).Info("Detailed health check completed",
		zap.String("status", overallStatus),
		zap.Duration("duration", time.Since(startTime)))

	c.JSON(statusCode, response)
}

// GetMetrics handles GET /api/v1/metrics
// @Summary      Application & system metrics (JSON)
// @Description  Returns JSON-formatted system metrics (Go runtime, memory, GC), application metrics (total requests, latency, error and cache-hit rates, DB connections), and business metrics (valuation counts, average WACC and growth, unique tickers served). Distinct from the Prometheus exposition endpoint served at the root GET /metrics.
// @Tags         metrics
// @Produce      json
// @Security     ApiKeyAuth
// @Success      200  {object}  MetricsResponse
// @Failure      401  {object}  ErrorResponse  "Missing or invalid API key"
// @Failure      403  {object}  ErrorResponse  "Insufficient permissions"
// @Failure      429  {object}  ErrorResponse  "Rate limit exceeded"
// @Router       /metrics [get]
func (h *HealthHandler) GetMetrics(c *gin.Context) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	systemMetrics := SystemMetrics{
		GoVersion:     runtime.Version(),
		NumGoroutines: runtime.NumGoroutine(),
		NumCPU:        runtime.NumCPU(),
		MemoryAlloc:   memStats.Alloc,
		MemoryTotal:   memStats.TotalAlloc,
		MemorySys:     memStats.Sys,
		GCCount:       memStats.NumGC,
		LastGC:        time.Unix(0, int64(memStats.LastGC)).Format(time.RFC3339),
		UptimeSeconds: int64(time.Since(h.startTime).Seconds()),
	}

	// Get database connection stats
	dbStats := h.db.Stats()

	// Collect actual application metrics from services
	applicationMetrics := ApplicationMetrics{
		TotalRequests:       h.metricsService.GetTotalRequests(),
		ActiveConnections:   h.metricsService.GetActiveConnections(),
		AverageResponseTime: h.metricsService.GetAverageResponseTime(),
		ErrorRate:           h.metricsService.GetErrorRate(),
		CacheHitRate:        h.metricsService.GetCacheHitRate(),
		DatabaseConnections: dbStats.OpenConnections,
	}

	// Collect actual business metrics from valuation service
	businessMetrics := BusinessMetrics{
		TotalValuations:      h.metricsService.GetTotalValuations(),
		SuccessfulValuations: h.metricsService.GetSuccessfulValuations(),
		FailedValuations:     h.metricsService.GetFailedValuations(),
		AverageWACC:          h.metricsService.GetAverageWACC(),
		AverageGrowthRate:    h.metricsService.GetAverageGrowthRate(),
		UniqueTickersServed:  h.metricsService.GetUniqueTickersServed(),
	}

	response := MetricsResponse{
		System:      systemMetrics,
		Application: applicationMetrics,
		Business:    businessMetrics,
		Timestamp:   time.Now().UTC(),
	}

	c.JSON(http.StatusOK, response)
}

// Health check implementations

// checkDatabase verifies database connectivity
func (h *HealthHandler) checkDatabase(ctx context.Context) HealthCheck {
	start := time.Now()

	status := "healthy"
	message := "Database connection active"
	details := make(map[string]interface{})

	// Test database connectivity
	if err := h.db.PingContext(ctx); err != nil {
		status = "unhealthy"
		message = "Database connection failed: " + err.Error()
		logctx.From(ctx).Error("Database health check failed", zap.Error(err))
	} else {
		// Get database stats
		dbStats := h.db.Stats()
		details["connection_pool_size"] = dbStats.MaxOpenConnections
		details["active_connections"] = dbStats.OpenConnections
		details["in_use_connections"] = dbStats.InUse
		details["idle_connections"] = dbStats.Idle

		// Check if we're running out of connections
		if dbStats.OpenConnections >= dbStats.MaxOpenConnections-2 {
			status = "degraded"
			message = "Database connection pool nearly exhausted"
		}
	}

	return HealthCheck{
		Status:      status,
		LastChecked: time.Now().UTC(),
		Duration:    time.Since(start),
		Message:     message,
		Details:     details,
	}
}

// checkCache verifies cache connectivity and performance
func (h *HealthHandler) checkCache(ctx context.Context) HealthCheck {
	start := time.Now()

	status := "healthy"
	message := "Cache operational"
	details := make(map[string]interface{})

	// Test Redis connectivity if available
	if h.redis != nil {
		if err := h.redis.Ping(ctx).Err(); err != nil {
			status = "degraded"
			message = "Redis cache unavailable, using memory cache: " + err.Error()
			details["redis_available"] = false
		} else {
			details["redis_available"] = true

			// Get Redis info
			info, err := h.redis.Info(ctx, "memory").Result()
			if err == nil {
				details["redis_info"] = info
			}
		}
	} else {
		details["redis_available"] = false
		message = "Using memory cache (Redis not configured)"
	}

	// Test cache operations
	testKey := "health_check_" + time.Now().Format("20060102150405")
	testValue := "test"

	if err := h.cache.Set(ctx, testKey, testValue, time.Minute); err != nil {
		status = "unhealthy"
		message = "Cache write operation failed: " + err.Error()
		logctx.From(ctx).Error("Cache health check write failed", zap.Error(err))
	} else {
		var retrievedValue string
		if err := h.cache.Get(ctx, testKey, &retrievedValue); err != nil {
			status = "degraded"
			message = "Cache read operation failed: " + err.Error()
		} else if retrievedValue != testValue {
			status = "degraded"
			message = "Cache data integrity issue"
		}

		// Clean up test key (best-effort)
		if err := h.cache.Delete(ctx, testKey); err != nil {
			logctx.From(ctx).Warn("Health check cache cleanup failed", zap.Error(err))
		}
	}

	return HealthCheck{
		Status:      status,
		LastChecked: time.Now().UTC(),
		Duration:    time.Since(start),
		Message:     message,
		Details:     details,
	}
}

// checkExternalAPIs verifies external API connectivity
func (h *HealthHandler) checkExternalAPIs(ctx context.Context) HealthCheck {
	start := time.Now()

	status := "healthy"
	message := "All external APIs operational"
	details := make(map[string]interface{})

	// Check SEC API
	if secCheck := h.checkSECAPI(ctx); secCheck.Status != "healthy" {
		details["sec_api"] = secCheck
		if secCheck.Status == "unhealthy" {
			status = "degraded"
			message = "SEC API unavailable"
		}
	} else {
		details["sec_api"] = map[string]interface{}{"status": "healthy"}
	}

	// Check Market Data API
	if marketCheck := h.checkMarketAPI(ctx); marketCheck.Status != "healthy" {
		details["market_api"] = marketCheck
		if marketCheck.Status == "unhealthy" && status == "healthy" {
			status = "degraded"
			if message == "All external APIs operational" {
				message = "Market API unavailable"
			}
		}
	} else {
		details["market_api"] = map[string]interface{}{"status": "healthy"}
	}

	// Check Macro Data API
	if macroCheck := h.checkMacroAPI(ctx); macroCheck.Status != "healthy" {
		details["macro_api"] = macroCheck
		if macroCheck.Status == "unhealthy" && status == "healthy" {
			status = "degraded"
			if message == "All external APIs operational" {
				message = "Macro API unavailable"
			}
		}
	} else {
		details["macro_api"] = map[string]interface{}{"status": "healthy"}
	}

	return HealthCheck{
		Status:      status,
		LastChecked: time.Now().UTC(),
		Duration:    time.Since(start),
		Message:     message,
		Details:     details,
	}
}

// checkSECAPI verifies SEC API connectivity
func (h *HealthHandler) checkSECAPI(ctx context.Context) HealthCheck {
	start := time.Now()

	// Simple health check by trying to get company facts
	// Use a well-known ticker like Apple (AAPL) for testing
	_, err := h.secGateway.GetCompanyFacts(ctx, "0000320193") // Apple's CIK

	status := "healthy"
	message := "SEC API operational"

	if err != nil {
		status = "unhealthy"
		message = "SEC API check failed: " + err.Error()
	}

	return HealthCheck{
		Status:      status,
		LastChecked: time.Now().UTC(),
		Duration:    time.Since(start),
		Message:     message,
	}
}

// checkMarketAPI verifies market data API connectivity
func (h *HealthHandler) checkMarketAPI(ctx context.Context) HealthCheck {
	start := time.Now()

	// Simple health check by trying to get market data
	_, err := h.marketGateway.GetQuote(ctx, "AAPL")

	status := "healthy"
	message := "Market API operational"

	if err != nil {
		status = "unhealthy"
		message = "Market API check failed: " + err.Error()
	}

	return HealthCheck{
		Status:      status,
		LastChecked: time.Now().UTC(),
		Duration:    time.Since(start),
		Message:     message,
	}
}

// checkMacroAPI verifies macro data API connectivity
func (h *HealthHandler) checkMacroAPI(ctx context.Context) HealthCheck {
	start := time.Now()

	// Simple health check by trying to get treasury rates
	_, err := h.macroGateway.GetTreasuryRates(ctx)

	status := "healthy"
	message := "Macro API operational"

	if err != nil {
		status = "unhealthy"
		message = "Macro API check failed: " + err.Error()
	}

	return HealthCheck{
		Status:      status,
		LastChecked: time.Now().UTC(),
		Duration:    time.Since(start),
		Message:     message,
	}
}

// checkRateLimiter verifies rate limiter functionality
func (h *HealthHandler) checkRateLimiter(ctx context.Context) HealthCheck {
	start := time.Now()

	status := "healthy"
	message := "Rate limiter operational"
	details := make(map[string]interface{})

	// Test rate limiter functionality
	testRequest := ratelimit.RateLimitRequest{
		Identifier: "health_check",
		Type:       ratelimit.LimitTypeAPIKey,
	}

	result, err := h.rateLimiter.AllowRequest(ctx, testRequest)
	if err != nil {
		status = "unhealthy"
		message = "Rate limiter check failed: " + err.Error()
	} else {
		details["test_allowed"] = result.Allowed
		details["remaining"] = result.Remaining
		details["limits_configured"] = len(h.rateLimiter.GetLimits())
	}

	return HealthCheck{
		Status:      status,
		LastChecked: time.Now().UTC(),
		Duration:    time.Since(start),
		Message:     message,
		Details:     details,
	}
}

// checkMemory verifies memory usage is within acceptable limits
func (h *HealthHandler) checkMemory() HealthCheck {
	start := time.Now()

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// Consider memory unhealthy if using more than 1GB
	const maxMemoryBytes = 1024 * 1024 * 1024

	status := "healthy"
	message := "Memory usage normal"

	if memStats.Alloc > maxMemoryBytes {
		status = "degraded"
		message = "High memory usage detected"
	}

	return HealthCheck{
		Status:      status,
		LastChecked: time.Now().UTC(),
		Duration:    time.Since(start),
		Message:     message,
		Details: map[string]interface{}{
			"allocated_bytes":   memStats.Alloc,
			"total_alloc_bytes": memStats.TotalAlloc,
			"system_bytes":      memStats.Sys,
			"gc_count":          memStats.NumGC,
		},
	}
}
