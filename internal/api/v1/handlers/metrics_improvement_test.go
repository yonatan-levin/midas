package handlers

import (
	"encoding/json"
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

// TestRealMetricsCollection tests that actual metrics are collected instead of placeholder zeros
func TestRealMetricsCollection(t *testing.T) {
	logger := zaptest.NewLogger(t)
	metricsService := metrics.NewService(logger)

	// Record some sample metrics to simulate real usage
	metricsService.RecordHTTPRequest("GET", "/api/v1/fair-value/AAPL", 200, 150*time.Millisecond, 1024)
	metricsService.RecordValuationRequest("AAPL", "single", "success", 100*time.Millisecond)
	metricsService.IncDCFCalculations()

	// Create in-memory database for testing
	db, err := sqlx.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Create health handler
	healthHandler := &HealthHandler{
		logger:         logger,
		startTime:      time.Now().Add(-30 * time.Minute),
		metricsService: metricsService,
		db:             db,
	}

	// Test the GetMetrics endpoint
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/metrics", nil)

	// This test should fail initially due to placeholder implementation
	healthHandler.GetMetrics(c)

	// Parse response
	var response MetricsResponse
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// These assertions will fail until we implement real metrics collection
	assert.Greater(t, response.Application.TotalRequests, int64(0), "Should track real request count")
	assert.Greater(t, response.Application.AverageResponseTime, float64(0), "Should track real response time")
	assert.Greater(t, response.Business.TotalValuations, int64(0), "Should track real valuations")
	assert.Greater(t, response.Business.SuccessfulValuations, int64(0), "Should track successful valuations")
}

// TestBusinessMetricsFromValuationService tests integration with valuation service for business metrics
func TestBusinessMetricsFromValuationService(t *testing.T) {
	logger := zaptest.NewLogger(t)
	metricsService := metrics.NewService(logger)

	// Simulate business operations
	metricsService.RecordValuationRequest("AAPL", "single", "success", 120*time.Millisecond)
	metricsService.RecordValuationRequest("TSLA", "single", "error", 80*time.Millisecond)
	metricsService.SetAverageWACC(0.085)      // 8.5%
	metricsService.SetAverageGrowthRate(0.06) // 6%

	// Create in-memory database for testing
	db, dbErr := sqlx.Open("sqlite3", ":memory:")
	require.NoError(t, dbErr)
	defer func() { _ = db.Close() }()

	healthHandler := &HealthHandler{
		logger:         logger,
		startTime:      time.Now(),
		metricsService: metricsService,
		db:             db,
	}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/metrics", nil)

	healthHandler.GetMetrics(c)

	var response MetricsResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Should use actual calculated values, not hardcoded defaults
	assert.NotEqual(t, 0.08, response.Business.AverageWACC, "Should use actual WACC calculation")
	assert.NotEqual(t, 0.05, response.Business.AverageGrowthRate, "Should use actual growth rate calculation")
}
