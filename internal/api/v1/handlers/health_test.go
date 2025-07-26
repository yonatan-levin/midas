package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/midas/dcf-valuation-api/internal/services/metrics"
)

// TestHealthHandler_GetMetrics_RealMetricsCollection tests that real metrics are collected instead of placeholders
func TestHealthHandler_GetMetrics_RealMetricsCollection(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Create mock services with metrics tracking
	mockMetricsService := metrics.NewService(logger)

	// Simulate some metrics being recorded
	mockMetricsService.RecordHTTPRequest("GET", "/api/v1/fair-value/AAPL", 200, 150*time.Millisecond, 1024)
	mockMetricsService.RecordValuationRequest("AAPL", "single", "success", 100*time.Millisecond)
	mockMetricsService.IncDCFCalculations()
	mockMetricsService.IncWACCCalculations()

	// Create in-memory database for testing
	db, err := sqlx.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	// Create health handler with metrics tracking
	healthHandler := &HealthHandler{
		logger:         logger,
		startTime:      time.Now().Add(-1 * time.Hour), // 1 hour uptime
		metricsService: mockMetricsService,
		db:             db,
	}

	// Setup Gin context
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/metrics", nil)

	// Call GetMetrics
	healthHandler.GetMetrics(c)

	// Verify response
	assert.Equal(t, http.StatusOK, w.Code)

	var response MetricsResponse
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify system metrics are populated
	assert.NotEmpty(t, response.System.GoVersion)
	assert.Greater(t, response.System.UptimeSeconds, int64(3500)) // At least ~1 hour

	// Verify application metrics are real (not zeros)
	assert.Greater(t, response.Application.TotalRequests, int64(0), "Should track real request count")
	assert.Greater(t, response.Application.AverageResponseTime, float64(0), "Should track real response time")

	// Verify business metrics are real
	assert.Greater(t, response.Business.TotalValuations, int64(0), "Should track real valuations")
	assert.Greater(t, response.Business.SuccessfulValuations, int64(0), "Should track successful valuations")
}

// TestHealthHandler_GetMetrics_WithDatabaseStats tests database metrics collection
func TestHealthHandler_GetMetrics_WithDatabaseStats(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Create in-memory database for testing
	db, err := sqlx.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	// Create metrics service for testing
	mockMetricsService := metrics.NewService(logger)

	healthHandler := &HealthHandler{
		logger:         logger,
		startTime:      time.Now(),
		db:             db,
		metricsService: mockMetricsService,
	}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/metrics", nil)

	healthHandler.GetMetrics(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var response MetricsResponse
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify database connections are tracked
	assert.GreaterOrEqual(t, response.Application.DatabaseConnections, 0)
}

// TestHealthHandler_GetMetrics_ErrorRateCalculation tests error rate calculation
func TestHealthHandler_GetMetrics_ErrorRateCalculation(t *testing.T) {
	logger := zaptest.NewLogger(t)
	mockMetricsService := metrics.NewService(logger)

	// Record some successful and failed requests
	mockMetricsService.RecordHTTPRequest("GET", "/api/v1/fair-value/AAPL", 200, 100*time.Millisecond, 1024)
	mockMetricsService.RecordHTTPRequest("GET", "/api/v1/fair-value/INVALID", 400, 50*time.Millisecond, 256)
	mockMetricsService.RecordHTTPRequest("GET", "/api/v1/fair-value/TSLA", 500, 200*time.Millisecond, 512)

	// Create in-memory database for testing
	db, err := sqlx.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	healthHandler := &HealthHandler{
		logger:         logger,
		startTime:      time.Now(),
		metricsService: mockMetricsService,
		db:             db,
	}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/metrics", nil)

	healthHandler.GetMetrics(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var response MetricsResponse
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Should calculate error rate based on failed requests
	expectedErrorRate := float64(2) / float64(3) // 2 errors (400, 500) out of 3 requests
	assert.InDelta(t, expectedErrorRate, response.Application.ErrorRate, 0.01)
}

// TestHealthHandler_GetMetrics_CacheHitRateTracking tests cache hit rate calculation
func TestHealthHandler_GetMetrics_CacheHitRateTracking(t *testing.T) {
	logger := zaptest.NewLogger(t)
	mockMetricsService := metrics.NewService(logger)

	// Simulate cache operations
	mockMetricsService.RecordCacheRequest("memory", "get", "hit")
	mockMetricsService.RecordCacheRequest("memory", "get", "hit")
	mockMetricsService.RecordCacheRequest("memory", "get", "miss")
	mockMetricsService.SetCacheHitRatio("memory", 0.67) // 2 hits out of 3 requests

	// Create in-memory database for testing
	db, err := sqlx.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	healthHandler := &HealthHandler{
		logger:         logger,
		startTime:      time.Now(),
		metricsService: mockMetricsService,
		db:             db,
	}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/metrics", nil)

	healthHandler.GetMetrics(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var response MetricsResponse
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Should report actual cache hit rate
	assert.InDelta(t, 0.67, response.Application.CacheHitRate, 0.01)
}
