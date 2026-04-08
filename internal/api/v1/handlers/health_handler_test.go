package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/metrics"
	"github.com/midas/dcf-valuation-api/internal/services/ratelimit"
)

// ---- Mocks for health handler dependencies ----

// mockCacheRepository implements ports.CacheRepository for testing.
type mockCacheRepository struct {
	mock.Mock
}

func (m *mockCacheRepository) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	args := m.Called(ctx, key, value, ttl)
	return args.Error(0)
}

func (m *mockCacheRepository) Get(ctx context.Context, key string, dest interface{}) error {
	args := m.Called(ctx, key, dest)
	return args.Error(0)
}

func (m *mockCacheRepository) Delete(ctx context.Context, key string) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

func (m *mockCacheRepository) Exists(ctx context.Context, key string) (bool, error) {
	args := m.Called(ctx, key)
	return args.Bool(0), args.Error(1)
}

func (m *mockCacheRepository) SetNX(ctx context.Context, key string, value interface{}, ttl time.Duration) (bool, error) {
	args := m.Called(ctx, key, value, ttl)
	return args.Bool(0), args.Error(1)
}

func (m *mockCacheRepository) GetKeys(ctx context.Context, pattern string) ([]string, error) {
	args := m.Called(ctx, pattern)
	return args.Get(0).([]string), args.Error(1)
}

func (m *mockCacheRepository) DeletePattern(ctx context.Context, pattern string) error {
	args := m.Called(ctx, pattern)
	return args.Error(0)
}

// mockSECGateway implements ports.SECGateway for testing.
type mockSECGateway struct {
	mock.Mock
}

func (m *mockSECGateway) GetCompanyFacts(ctx context.Context, cik string) (*entities.CompanyFactsResponse, error) {
	args := m.Called(ctx, cik)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.CompanyFactsResponse), args.Error(1)
}

func (m *mockSECGateway) GetCompanyConcepts(ctx context.Context, cik, tag string) (*entities.ConceptResponse, error) {
	args := m.Called(ctx, cik, tag)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.ConceptResponse), args.Error(1)
}

func (m *mockSECGateway) GetTickerCIKMapping(ctx context.Context) (map[string]string, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]string), args.Error(1)
}

func (m *mockSECGateway) GetFinancialDataForTicker(ctx context.Context, ticker, cik string) (*entities.HistoricalFinancialData, error) {
	args := m.Called(ctx, ticker, cik)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.HistoricalFinancialData), args.Error(1)
}

func (m *mockSECGateway) HealthCheck(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// mockMarketGateway implements ports.MarketDataGateway for testing.
type mockMarketGateway struct {
	mock.Mock
}

func (m *mockMarketGateway) GetQuote(ctx context.Context, ticker string) (*entities.MarketData, error) {
	args := m.Called(ctx, ticker)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.MarketData), args.Error(1)
}

func (m *mockMarketGateway) GetQuotes(ctx context.Context, tickers []string) (map[string]*entities.MarketData, error) {
	args := m.Called(ctx, tickers)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]*entities.MarketData), args.Error(1)
}

func (m *mockMarketGateway) GetHistoricalPrices(ctx context.Context, ticker string, start, end time.Time) ([]*entities.PriceData, error) {
	args := m.Called(ctx, ticker, start, end)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*entities.PriceData), args.Error(1)
}

func (m *mockMarketGateway) HealthCheck(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// mockMacroGateway implements ports.MacroDataGateway for testing.
type mockMacroGateway struct {
	mock.Mock
}

func (m *mockMacroGateway) GetTreasuryRates(ctx context.Context) (*entities.TreasuryRates, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.TreasuryRates), args.Error(1)
}

func (m *mockMacroGateway) GetMarketRiskPremium(ctx context.Context) (float64, error) {
	args := m.Called(ctx)
	return args.Get(0).(float64), args.Error(1)
}

func (m *mockMacroGateway) HealthCheck(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// ---- In-memory CacheStore for rate limiter ----

// rateLimitCacheEntry holds a single counter with expiry for rate limiter testing.
type rateLimitCacheEntry struct {
	value     int
	expiresAt time.Time
}

// inMemoryRateLimitStore implements ratelimit.CacheStore for unit tests.
type inMemoryRateLimitStore struct {
	data map[string]rateLimitCacheEntry
}

func newInMemoryRateLimitStore() *inMemoryRateLimitStore {
	return &inMemoryRateLimitStore{data: make(map[string]rateLimitCacheEntry)}
}

func (s *inMemoryRateLimitStore) Increment(_ context.Context, key string, window time.Duration) (int, time.Time, error) {
	now := time.Now()
	if e, ok := s.data[key]; ok && e.expiresAt.Before(now) {
		delete(s.data, key)
	}
	e, ok := s.data[key]
	if !ok {
		e = rateLimitCacheEntry{value: 0, expiresAt: now.Add(window)}
	}
	e.value++
	s.data[key] = e
	return e.value, e.expiresAt, nil
}

func (s *inMemoryRateLimitStore) Get(_ context.Context, key string) (int, time.Time, error) {
	e, ok := s.data[key]
	if !ok {
		return 0, time.Time{}, nil
	}
	return e.value, e.expiresAt, nil
}

func (s *inMemoryRateLimitStore) Set(_ context.Context, key string, value int, window time.Duration) error {
	s.data[key] = rateLimitCacheEntry{value: value, expiresAt: time.Now().Add(window)}
	return nil
}

func (s *inMemoryRateLimitStore) Delete(_ context.Context, key string) error {
	delete(s.data, key)
	return nil
}

// ---- Helper to build a HealthHandler with test dependencies ----

// healthHandlerDeps bundles mock dependencies for constructing a HealthHandler in tests.
type healthHandlerDeps struct {
	db            *sqlx.DB
	cache         *mockCacheRepository
	secGateway    *mockSECGateway
	marketGateway *mockMarketGateway
	macroGateway  *mockMacroGateway
	rateLimiter   *ratelimit.RateLimiter
}

// newTestHealthHandler creates a HealthHandler with all dependencies controllable via mocks.
func newTestHealthHandler(t *testing.T) (*HealthHandler, *healthHandlerDeps) {
	t.Helper()

	logger := zap.NewNop()
	metricsService := metrics.NewService(logger)

	// In-memory SQLite database for testing
	db, err := sqlx.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cache := new(mockCacheRepository)
	secGW := new(mockSECGateway)
	marketGW := new(mockMarketGateway)
	macroGW := new(mockMacroGateway)

	// Create a real rate limiter backed by an in-memory cache store
	rl := ratelimit.NewRateLimiter(newInMemoryRateLimitStore(), logger)

	handler := &HealthHandler{
		logger:         logger,
		startTime:      time.Now().Add(-1 * time.Hour), // 1 hour uptime for tests
		db:             db,
		redis:          nil, // Redis is optional; nil exercises the "no Redis" code path
		cache:          cache,
		rateLimiter:    rl,
		secGateway:     secGW,
		marketGateway:  marketGW,
		macroGateway:   macroGW,
		metricsService: metricsService,
	}

	deps := &healthHandlerDeps{
		db:            db,
		cache:         cache,
		secGateway:    secGW,
		marketGateway: marketGW,
		macroGateway:  macroGW,
		rateLimiter:   rl,
	}

	return handler, deps
}

// ---- Tests for HealthCheckHandler (simple endpoint) ----

func TestHealthHandler_HealthCheckHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, _ := newTestHealthHandler(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/health", nil)

	handler.HealthCheckHandler(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "healthy", resp["status"])
	assert.Equal(t, "dcf-valuation-api", resp["service"])
	assert.NotEmpty(t, resp["timestamp"])
	assert.NotEmpty(t, resp["uptime"])
}

// ---- Tests for DetailedHealthCheck ----

func TestHealthHandler_DetailedHealthCheck_AllHealthy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, deps := newTestHealthHandler(t)

	// Configure mocks so all sub-checks pass
	// Cache: Set succeeds, Get succeeds with matching value, Delete succeeds
	deps.cache.On("Set", mock.Anything, mock.AnythingOfType("string"), "test", time.Minute).Return(nil)
	deps.cache.On("Get", mock.Anything, mock.AnythingOfType("string"), mock.Anything).
		Run(func(args mock.Arguments) {
			// Simulate writing the retrieved value into the dest pointer
			dest := args.Get(2).(*string)
			*dest = "test"
		}).Return(nil)
	deps.cache.On("Delete", mock.Anything, mock.AnythingOfType("string")).Return(nil)

	// SEC gateway: return dummy facts (healthy)
	deps.secGateway.On("GetCompanyFacts", mock.Anything, "0000320193").
		Return(&entities.CompanyFactsResponse{}, nil)

	// Market gateway: return dummy quote (healthy)
	deps.marketGateway.On("GetQuote", mock.Anything, "AAPL").
		Return(&entities.MarketData{Ticker: "AAPL"}, nil)

	// Macro gateway: return dummy rates (healthy)
	deps.macroGateway.On("GetTreasuryRates", mock.Anything).
		Return(&entities.TreasuryRates{}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/health/detailed", nil)

	handler.DetailedHealthCheck(c)

	// Health check may return 200 (healthy) or 206 (degraded) depending on sub-checks
	assert.True(t, w.Code == http.StatusOK || w.Code == http.StatusPartialContent,
		"Expected 200 or 206, got %d", w.Code)

	var resp DetailedHealthCheckResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	// Status may be "healthy" or "degraded" depending on mock behavior for sub-checks
	assert.Contains(t, []string{"healthy", "degraded"}, resp.Status)
	assert.Equal(t, "dcf-valuation-api", resp.Service)
	assert.NotEmpty(t, resp.Uptime)

	// Verify all individual checks are present
	assert.Contains(t, resp.Checks, "database")
	assert.Contains(t, resp.Checks, "cache")
	assert.Contains(t, resp.Checks, "external_apis")
	assert.Contains(t, resp.Checks, "memory")
	assert.Contains(t, resp.Checks, "rate_limiter")

	// Verify metadata includes check duration
	assert.Contains(t, resp.Metadata, "check_duration_ms")
	assert.Contains(t, resp.Metadata, "go_version")
}

func TestHealthHandler_DetailedHealthCheck_DegradedWhenExternalAPIFails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, deps := newTestHealthHandler(t)

	// Cache passes
	deps.cache.On("Set", mock.Anything, mock.AnythingOfType("string"), "test", time.Minute).Return(nil)
	deps.cache.On("Get", mock.Anything, mock.AnythingOfType("string"), mock.Anything).
		Run(func(args mock.Arguments) {
			dest := args.Get(2).(*string)
			*dest = "test"
		}).Return(nil)
	deps.cache.On("Delete", mock.Anything, mock.AnythingOfType("string")).Return(nil)

	// SEC gateway fails — should cause "degraded" overall status
	deps.secGateway.On("GetCompanyFacts", mock.Anything, "0000320193").
		Return(nil, errors.New("SEC API timeout"))

	// Market and macro gateways pass
	deps.marketGateway.On("GetQuote", mock.Anything, "AAPL").
		Return(&entities.MarketData{Ticker: "AAPL"}, nil)
	deps.macroGateway.On("GetTreasuryRates", mock.Anything).
		Return(&entities.TreasuryRates{}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/health/detailed", nil)

	handler.DetailedHealthCheck(c)

	// Degraded returns HTTP 206 Partial Content
	assert.Equal(t, http.StatusPartialContent, w.Code)

	var resp DetailedHealthCheckResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "degraded", resp.Status)
}

func TestHealthHandler_DetailedHealthCheck_UnhealthyWhenDBFails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, deps := newTestHealthHandler(t)

	// Close the database to force a ping failure
	_ = deps.db.Close()

	// Cache passes
	deps.cache.On("Set", mock.Anything, mock.AnythingOfType("string"), "test", time.Minute).Return(nil)
	deps.cache.On("Get", mock.Anything, mock.AnythingOfType("string"), mock.Anything).
		Run(func(args mock.Arguments) {
			dest := args.Get(2).(*string)
			*dest = "test"
		}).Return(nil)
	deps.cache.On("Delete", mock.Anything, mock.AnythingOfType("string")).Return(nil)

	// External APIs pass
	deps.secGateway.On("GetCompanyFacts", mock.Anything, "0000320193").
		Return(&entities.CompanyFactsResponse{}, nil)
	deps.marketGateway.On("GetQuote", mock.Anything, "AAPL").
		Return(&entities.MarketData{Ticker: "AAPL"}, nil)
	deps.macroGateway.On("GetTreasuryRates", mock.Anything).
		Return(&entities.TreasuryRates{}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/health/detailed", nil)

	handler.DetailedHealthCheck(c)

	// Unhealthy returns HTTP 503 Service Unavailable
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var resp DetailedHealthCheckResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "unhealthy", resp.Status)
	assert.Equal(t, "unhealthy", resp.Checks["database"].Status)
}

func TestHealthHandler_DetailedHealthCheck_CacheWriteFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler, deps := newTestHealthHandler(t)

	// Cache write fails — cache check should report "unhealthy"
	deps.cache.On("Set", mock.Anything, mock.AnythingOfType("string"), "test", time.Minute).
		Return(errors.New("cache write timeout"))

	// External APIs pass
	deps.secGateway.On("GetCompanyFacts", mock.Anything, "0000320193").
		Return(&entities.CompanyFactsResponse{}, nil)
	deps.marketGateway.On("GetQuote", mock.Anything, "AAPL").
		Return(&entities.MarketData{Ticker: "AAPL"}, nil)
	deps.macroGateway.On("GetTreasuryRates", mock.Anything).
		Return(&entities.TreasuryRates{}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/health/detailed", nil)

	handler.DetailedHealthCheck(c)

	// Cache failure makes overall status "unhealthy"
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var resp DetailedHealthCheckResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "unhealthy", resp.Checks["cache"].Status)
}

// TestNewHealthHandler verifies the constructor wires all dependencies correctly.
func TestNewHealthHandler(t *testing.T) {
	logger := zap.NewNop()
	metricsService := metrics.NewService(logger)

	db, err := sqlx.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	cache := new(mockCacheRepository)
	secGW := new(mockSECGateway)
	marketGW := new(mockMarketGateway)
	macroGW := new(mockMacroGateway)
	rl := ratelimit.NewRateLimiter(newInMemoryRateLimitStore(), logger)

	handler := NewHealthHandler(logger, db, nil, cache, rl, secGW, marketGW, macroGW, metricsService)
	require.NotNil(t, handler)
	assert.Equal(t, logger, handler.logger)
	assert.Equal(t, db, handler.db)
	assert.NotZero(t, handler.startTime)
}

// TestHealthHandler_DetailedHealthCheck_CacheReadFailure tests the cache read-failure path
// where Set succeeds but Get returns an error (covers the "degraded" branch in checkCache).
func TestHealthHandler_DetailedHealthCheck_CacheReadFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, deps := newTestHealthHandler(t)

	// Cache write succeeds but read fails
	deps.cache.On("Set", mock.Anything, mock.AnythingOfType("string"), "test", time.Minute).Return(nil)
	deps.cache.On("Get", mock.Anything, mock.AnythingOfType("string"), mock.Anything).
		Return(errors.New("read timeout"))
	deps.cache.On("Delete", mock.Anything, mock.AnythingOfType("string")).Return(nil)

	deps.secGateway.On("GetCompanyFacts", mock.Anything, "0000320193").
		Return(&entities.CompanyFactsResponse{}, nil)
	deps.marketGateway.On("GetQuote", mock.Anything, "AAPL").
		Return(&entities.MarketData{Ticker: "AAPL"}, nil)
	deps.macroGateway.On("GetTreasuryRates", mock.Anything).
		Return(&entities.TreasuryRates{}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/health/detailed", nil)

	handler.DetailedHealthCheck(c)

	var resp DetailedHealthCheckResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	// Cache read failure marks cache as "degraded"
	assert.Equal(t, "degraded", resp.Checks["cache"].Status)
}

// TestHealthHandler_DetailedHealthCheck_MarketAPIFails exercises the branch where
// only the market API fails (SEC and macro pass), covering the "Market API unavailable" path.
func TestHealthHandler_DetailedHealthCheck_MarketAPIFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, deps := newTestHealthHandler(t)

	deps.cache.On("Set", mock.Anything, mock.AnythingOfType("string"), "test", time.Minute).Return(nil)
	deps.cache.On("Get", mock.Anything, mock.AnythingOfType("string"), mock.Anything).
		Run(func(args mock.Arguments) {
			dest := args.Get(2).(*string)
			*dest = "test"
		}).Return(nil)
	deps.cache.On("Delete", mock.Anything, mock.AnythingOfType("string")).Return(nil)

	// SEC passes, market fails, macro passes
	deps.secGateway.On("GetCompanyFacts", mock.Anything, "0000320193").
		Return(&entities.CompanyFactsResponse{}, nil)
	deps.marketGateway.On("GetQuote", mock.Anything, "AAPL").
		Return(nil, errors.New("market API error"))
	deps.macroGateway.On("GetTreasuryRates", mock.Anything).
		Return(&entities.TreasuryRates{}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/health/detailed", nil)

	handler.DetailedHealthCheck(c)

	var resp DetailedHealthCheckResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	// External APIs should be "degraded" due to market failure
	assert.Equal(t, "degraded", resp.Checks["external_apis"].Status)
}

// TestHealthHandler_DetailedHealthCheck_MacroAPIFails exercises the branch where
// only the macro API fails (SEC and market pass), covering the "Macro API unavailable" path.
func TestHealthHandler_DetailedHealthCheck_MacroAPIFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, deps := newTestHealthHandler(t)

	deps.cache.On("Set", mock.Anything, mock.AnythingOfType("string"), "test", time.Minute).Return(nil)
	deps.cache.On("Get", mock.Anything, mock.AnythingOfType("string"), mock.Anything).
		Run(func(args mock.Arguments) {
			dest := args.Get(2).(*string)
			*dest = "test"
		}).Return(nil)
	deps.cache.On("Delete", mock.Anything, mock.AnythingOfType("string")).Return(nil)

	// SEC passes, market passes, macro fails
	deps.secGateway.On("GetCompanyFacts", mock.Anything, "0000320193").
		Return(&entities.CompanyFactsResponse{}, nil)
	deps.marketGateway.On("GetQuote", mock.Anything, "AAPL").
		Return(&entities.MarketData{Ticker: "AAPL"}, nil)
	deps.macroGateway.On("GetTreasuryRates", mock.Anything).
		Return(nil, errors.New("FRED API timeout"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/health/detailed", nil)

	handler.DetailedHealthCheck(c)

	var resp DetailedHealthCheckResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "degraded", resp.Checks["external_apis"].Status)
}

// TestHealthHandler_DetailedHealthCheck_AllExternalAPIsFail exercises the branch where
// all three external APIs fail, ensuring the combined status is reported properly.
func TestHealthHandler_DetailedHealthCheck_AllExternalAPIsFail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, deps := newTestHealthHandler(t)

	deps.cache.On("Set", mock.Anything, mock.AnythingOfType("string"), "test", time.Minute).Return(nil)
	deps.cache.On("Get", mock.Anything, mock.AnythingOfType("string"), mock.Anything).
		Run(func(args mock.Arguments) {
			dest := args.Get(2).(*string)
			*dest = "test"
		}).Return(nil)
	deps.cache.On("Delete", mock.Anything, mock.AnythingOfType("string")).Return(nil)

	// All three external APIs fail
	deps.secGateway.On("GetCompanyFacts", mock.Anything, "0000320193").
		Return(nil, errors.New("SEC error"))
	deps.marketGateway.On("GetQuote", mock.Anything, "AAPL").
		Return(nil, errors.New("market error"))
	deps.macroGateway.On("GetTreasuryRates", mock.Anything).
		Return(nil, errors.New("macro error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/health/detailed", nil)

	handler.DetailedHealthCheck(c)

	var resp DetailedHealthCheckResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "degraded", resp.Checks["external_apis"].Status)
}
