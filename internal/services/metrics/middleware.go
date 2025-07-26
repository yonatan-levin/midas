package metrics

import (
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// HTTPMetricsMiddleware creates Gin middleware for HTTP metrics collection
func HTTPMetricsMiddleware(metricsService *Service, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		// Increment in-flight requests
		metricsService.IncHTTPRequestsInFlight()
		defer metricsService.DecHTTPRequestsInFlight()

		// Process request
		c.Next()

		// Calculate duration and response size
		duration := time.Since(start)
		responseSize := c.Writer.Size()
		if responseSize < 0 {
			responseSize = 0 // Handle case where size is unknown
		}

		// Get normalized endpoint for cleaner metrics
		endpoint := normalizeEndpoint(c.FullPath())

		// Record metrics
		metricsService.RecordHTTPRequest(
			c.Request.Method,
			endpoint,
			c.Writer.Status(),
			duration,
			responseSize,
		)

		// Log slow requests for monitoring
		if duration > 1*time.Second {
			logger.Warn("Slow HTTP request detected",
				zap.String("method", c.Request.Method),
				zap.String("endpoint", endpoint),
				zap.Int("status", c.Writer.Status()),
				zap.Duration("duration", duration),
				zap.String("client_ip", c.ClientIP()),
			)
		}
	}
}

// normalizeEndpoint converts path parameters to normalized form for metrics
// This prevents high cardinality issues in Prometheus metrics
func normalizeEndpoint(fullPath string) string {
	if fullPath == "" {
		return "unknown"
	}

	// Common endpoint normalizations for our API
	switch {
	case fullPath == "/":
		return "root"
	case fullPath == "/health":
		return "health"
	case fullPath == "/ready":
		return "ready"
	case fullPath == "/version":
		return "version"
	case fullPath == "/api/v1/fair-value/:ticker":
		return "/api/v1/fair-value/:ticker"
	case fullPath == "/api/v1/fair-value/bulk":
		return "/api/v1/fair-value/bulk"
	case fullPath == "/api/v1/health/detailed":
		return "/api/v1/health/detailed"
	case fullPath == "/api/v1/metrics":
		return "/api/v1/metrics"
	case fullPath == "/metrics":
		return "/metrics"
	default:
		// For any other paths, return as-is but with a fallback
		if len(fullPath) > 100 {
			return "long_path" // Prevent very long paths from creating metrics issues
		}
		return fullPath
	}
}

// ValuationMetricsWrapper wraps valuation operations with metrics
type ValuationMetricsWrapper struct {
	metricsService *Service
	logger         *zap.Logger
}

// NewValuationMetricsWrapper creates a new valuation metrics wrapper
func NewValuationMetricsWrapper(metricsService *Service, logger *zap.Logger) *ValuationMetricsWrapper {
	return &ValuationMetricsWrapper{
		metricsService: metricsService,
		logger:         logger,
	}
}

// WrapValuationOperation wraps a valuation operation with metrics and error tracking
func (v *ValuationMetricsWrapper) WrapValuationOperation(
	ticker string,
	requestType string,
	operation func() error,
) error {
	start := time.Now()

	// Execute operation
	err := operation()
	duration := time.Since(start)

	// Determine status and record metrics
	status := "success"
	if err != nil {
		status = "error"
		// Classify error type for better monitoring
		errorType := classifyValuationError(err)
		v.metricsService.RecordValuationError(ticker, errorType)
	}

	// Record the valuation request
	v.metricsService.RecordValuationRequest(ticker, requestType, status, duration)

	// Log long-running operations
	if duration > 5*time.Second {
		v.logger.Warn("Long-running valuation operation",
			zap.String("ticker", ticker),
			zap.String("request_type", requestType),
			zap.String("status", status),
			zap.Duration("duration", duration),
		)
	}

	return err
}

// classifyValuationError categorizes errors for better metrics granularity
func classifyValuationError(err error) string {
	if err == nil {
		return "none"
	}

	errStr := err.Error()

	// Classify based on error message patterns
	switch {
	case contains(errStr, "data fetch", "fetch", "api"):
		return "data_fetch_error"
	case contains(errStr, "parse", "json", "unmarshal"):
		return "parsing_error"
	case contains(errStr, "cache"):
		return "cache_error"
	case contains(errStr, "timeout", "context deadline"):
		return "timeout_error"
	case contains(errStr, "validation", "invalid"):
		return "validation_error"
	case contains(errStr, "calculation", "math", "divide by zero"):
		return "calculation_error"
	case contains(errStr, "database", "sql"):
		return "database_error"
	case contains(errStr, "rate limit", "throttle"):
		return "rate_limit_error"
	case contains(errStr, "auth", "permission", "unauthorized"):
		return "auth_error"
	default:
		return "unknown_error"
	}
}

// contains checks if any of the substrings exist in the target string
func contains(target string, substrings ...string) bool {
	for _, substr := range substrings {
		if len(target) >= len(substr) {
			for i := 0; i <= len(target)-len(substr); i++ {
				if target[i:i+len(substr)] == substr {
					return true
				}
			}
		}
	}
	return false
}

// DataFetchMetricsWrapper wraps data fetching operations with metrics
type DataFetchMetricsWrapper struct {
	metricsService *Service
	logger         *zap.Logger
}

// NewDataFetchMetricsWrapper creates a new data fetch metrics wrapper
func NewDataFetchMetricsWrapper(metricsService *Service, logger *zap.Logger) *DataFetchMetricsWrapper {
	return &DataFetchMetricsWrapper{
		metricsService: metricsService,
		logger:         logger,
	}
}

// WrapSECFetch wraps SEC API calls with metrics
func (d *DataFetchMetricsWrapper) WrapSECFetch(endpoint string, operation func() error) error {
	err := operation()
	status := "success"
	if err != nil {
		status = "error"
	}

	d.metricsService.RecordSECAPIRequest(endpoint, status)
	return err
}

// WrapMarketFetch wraps market data API calls with metrics
func (d *DataFetchMetricsWrapper) WrapMarketFetch(provider string, operation func() error) error {
	err := operation()
	status := "success"
	if err != nil {
		status = "error"
	}

	d.metricsService.RecordMarketAPIRequest(provider, status)
	return err
}

// WrapMacroFetch wraps macro data API calls with metrics
func (d *DataFetchMetricsWrapper) WrapMacroFetch(provider string, operation func() error) error {
	err := operation()
	status := "success"
	if err != nil {
		status = "error"
	}

	d.metricsService.RecordMacroAPIRequest(provider, status)
	return err
}

// RecordDataFetchTiming records data fetch timing metrics
func (d *DataFetchMetricsWrapper) RecordDataFetchTiming(source, ticker string, duration time.Duration) {
	d.metricsService.RecordDataFetch(source, ticker, duration)

	// Log slow data fetches
	if duration > 10*time.Second {
		d.logger.Warn("Slow data fetch detected",
			zap.String("source", source),
			zap.String("ticker", ticker),
			zap.Duration("duration", duration),
		)
	}
}
