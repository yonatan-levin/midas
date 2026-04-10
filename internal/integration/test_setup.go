package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/midas/dcf-valuation-api/internal/api"
	"github.com/midas/dcf-valuation-api/internal/api/v1/handlers"
	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/di"
	"github.com/midas/dcf-valuation-api/internal/services/auth"
	"github.com/midas/dcf-valuation-api/internal/services/metrics"
	"github.com/midas/dcf-valuation-api/internal/services/ratelimit"
	"github.com/midas/dcf-valuation-api/internal/services/valuation"
	"go.uber.org/zap"
)

// TestContainer represents the test environment with containers and dependencies
type TestContainer struct {
	RedisContainer   testcontainers.Container
	RedisURL         string
	Config           *config.Config
	App              *fxtest.App
	Router           *gin.Engine
	FairValueHandler *handlers.FairValueHandler
	AuthService      *auth.Service
	WatchlistRepo    ports.WatchlistRepository
	Logger           *zap.Logger
	Database         *sqlx.DB
	cleanup          func()
}

// SetupTestEnvironment creates a complete test environment with real infrastructure
func SetupTestEnvironment(t *testing.T) *TestContainer {
	ctx := context.Background()

	var (
		redisContainer testcontainers.Container
		redisURL       string
	)

	// Fallback to in-memory cache on Windows or when NO_DOCKER=1
	if runtime.GOOS == "windows" || os.Getenv("NO_DOCKER") == "1" {
		// Use an invalid Redis URL to force memory cache fallback in DI
		redisURL = "redis://127.0.0.1:0"
	} else {
		// Start Redis container for non-Windows environments
		redisContainer, redisURL = setupRedisContainer(t, ctx)
	}

	// Step 2: Create test configuration with Redis URL (or invalid URL to trigger memory cache)
	cfg := createTestConfig(redisURL)

	// Step 3: Declare variables first
	var fairValueHandler *handlers.FairValueHandler
	var authService *auth.Service
	var database *sqlx.DB
	var valuationService *valuation.Service
	var rateLimiter *ratelimit.RateLimiter
	var healthHandler *handlers.HealthHandler
	var metricsService *metrics.Service
	var logger *zap.Logger
	var watchlistRepo ports.WatchlistRepository

	// Step 4: Create DI container with real services
	app := fxtest.New(t,
		// Provide test configuration
		fx.Provide(func() *config.Config { return cfg }),

		// Include all real services via DI module
		di.CoreModule,
		di.ServiceModule,
		di.HandlerModule,

		// Provide additional handlers if not already included by DI modules
		fx.Provide(handlers.NewFairValueHandler),

		// Extract handlers and database for testing
		fx.Populate(&fairValueHandler, &authService, &database, &valuationService, &rateLimiter, &healthHandler, &metricsService, &logger, &watchlistRepo),
	)

	// Step 5: Start the DI container
	app.RequireStart()

	// Step 6: Setup database schema and test data
	SetupDatabase(t, database)
	SeedTestData(t, database)

	// Step 7: Create the real API server to use genuine middleware & routes
	server := api.NewServer(cfg, logger, valuationService, authService, rateLimiter, healthHandler, metricsService)
	router := server.Engine()

	// Step 8: Setup cleanup function
	cleanup := func() {
		if app != nil {
			app.RequireStop()
		}
		if redisContainer != nil {
			_ = redisContainer.Terminate(ctx)
		}
	}

	return &TestContainer{
		RedisContainer:   redisContainer,
		RedisURL:         redisURL,
		Config:           cfg,
		App:              app,
		Router:           router,
		FairValueHandler: fairValueHandler,
		AuthService:      authService,
		WatchlistRepo:    watchlistRepo,
		Logger:           logger,
		Database:         database,
		cleanup:          cleanup,
	}
}

// Cleanup cleans up the test environment
func (tc *TestContainer) Cleanup() {
	if tc.cleanup != nil {
		tc.cleanup()
	}
}

// NewTestAPIKey creates a test API key with specified permissions
func (tc *TestContainer) NewTestAPIKey(ctx context.Context, userID string, permissions []entities.Permission) (*entities.APIKey, error) {
	return tc.AuthService.CreateKey(ctx, userID, permissions)
}

// setupRedisContainer starts a Redis container for testing
// setupRedisContainer is implemented in platform-specific files.

// createTestConfig creates a configuration optimized for integration testing
func createTestConfig(redisURL string) *config.Config {
	// Check environment variable for swagger setting
	enableSwagger := false
	if swaggerEnv := os.Getenv("ENABLE_SWAGGER"); swaggerEnv != "" {
		if parsed, err := strconv.ParseBool(swaggerEnv); err == nil {
			enableSwagger = parsed
		}
	}

	return &config.Config{
		Port:          "0", // Let system assign port for testing
		LogLevel:      "debug",
		EnableSwagger: enableSwagger, // Respect environment variable

		Database: config.DatabaseConfig{
			Driver:      "sqlite3",
			SQLitePath:  ":memory:", // In-memory database for fast tests
			MaxOpenConn: 1,          // Single connection for SQLite :memory: to avoid separate DB per conn
			MaxIdleConn: 1,
		},

		Cache: config.CacheConfig{
			RedisURL:   redisURL,
			DefaultTTL: time.Hour,
		},

		Valuation: config.ValuationConfig{
			DCFProjectionYears:   5,
			DefaultTaxRate:       0.21,
			CacheTTL:             time.Hour,
			SlowRequestThreshold: 500 * time.Millisecond,
			DataFetchTimeout:     30 * time.Second,
		},

		DataCleaner: config.DataCleanerConfig{
			Enabled:             true, // Enable data cleaning for integration tests
			RulesPath:           "../../config/datacleaner/rules.json",
			IndustryRulesPath:   "../../config/datacleaner/industry",
			SchemaPath:          "../../config/datacleaner/schema.json",
			EnableAIIntegration: false, // Disable AI for faster tests
			MinQualityScore:     50.0,
			HighQualityScore:    80.0,
		},

		SEC: config.SECConfig{
			BaseURL:          "https://data.sec.gov/api/xbrl",
			TickerMappingURL: "https://www.sec.gov/files/company_tickers.json",
			UserAgent:        "DCF-Valuation-API-Test/1.0 (email@example.com)",
			RateLimit:        1, // Respect SEC rate limits
			RequestTimeout:   30 * time.Second,
			MaxRetries:       3,
			RetryBackoffBase: 1 * time.Second,
		},
	}
}

// SetupDatabase initializes the test database with schema
func SetupDatabase(t *testing.T, db *sqlx.DB) {
	// Load and execute schema.sql
	schemaPath := filepath.Join("../../", "internal", "infra", "database", "schema.sql")

	schemaBytes, err := os.ReadFile(schemaPath)
	require.NoError(t, err, "Failed to read schema.sql")

	schema := string(schemaBytes)

	// Execute the entire schema as one statement
	// SQLite can handle multiple statements in a single Exec
	_, err = db.Exec(schema)
	require.NoError(t, err, "Failed to execute schema")

	t.Log("Database schema setup completed")

	// --- NEW: create minimal auth tables required by middleware ---
	authSQL := `
        CREATE TABLE IF NOT EXISTS api_keys (
            id TEXT PRIMARY KEY,
            key_hash TEXT NOT NULL,
            user_id TEXT NOT NULL,
            permissions TEXT NOT NULL,
            rate_limit INTEGER DEFAULT 1000,
            expires_at TIMESTAMP,
            is_active BOOLEAN DEFAULT 1,
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        );

        CREATE TABLE IF NOT EXISTS api_key_usage (
            id TEXT PRIMARY KEY,
            api_key_id TEXT NOT NULL,
            endpoint TEXT,
            timestamp TIMESTAMP,
            response_status INTEGER,
            response_time_ms INTEGER,
            user_agent TEXT,
            ip_address TEXT
        );`

	_, err = db.Exec(authSQL)
	require.NoError(t, err, "failed to create auth tables")

	// quick sanity
	var count int
	err = db.Get(&count, "SELECT count(name) FROM sqlite_master WHERE type='table' AND name='api_keys'")
	require.NoError(t, err)
	require.Equal(t, 1, count, "api_keys table should exist")
}

// Remove ALL data processing functions and implement proper mock server
func SeedTestData(t *testing.T, db *sqlx.DB) {
	// Only insert basic company data - financial data comes from service pipeline
	companies := []struct {
		ticker, cik, name, exchange, sector, industry string
	}{
		{"AAPL", "0000320193", "Apple Inc.", "NASDAQ", "Technology", "Consumer Electronics"},
		{"MSFT", "0000789019", "Microsoft Corporation", "NASDAQ", "Technology", "Software"},
		{"GOOGL", "0001652044", "Alphabet Inc.", "NASDAQ", "Technology", "Internet Services"},
	}

	for _, company := range companies {
		_, err := db.Exec(`
			INSERT OR REPLACE INTO companies (ticker, cik, company_name, exchange, sector, industry)
			VALUES (?, ?, ?, ?, ?, ?)
		`, company.ticker, company.cik, company.name, company.exchange, company.sector, company.industry)
		require.NoError(t, err, "Failed to insert company %s", company.ticker)
	}

	// Seed minimal macro data to avoid external dependency
	now := time.Now().UTC()
	_, err := db.Exec(`
		INSERT INTO macro_data (as_of, risk_free_rate, risk_free_rate_3m, market_risk_premium, inflation_rate, source, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, now, 0.04, 0.04, 0.05, 0.02, "seed")
	require.NoError(t, err, "Failed to seed macro data")

	// Seed minimal market and financial data for each company
	for _, ticker := range []string{"AAPL", "MSFT", "GOOGL"} {
		// Market data
		_, err = db.Exec(`
			INSERT INTO market_data (ticker, as_of_date, share_price, market_cap, shares_outstanding, beta, beta_3_year, average_volume, source, data_quality, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, ticker, now, 150.0, 2.0e12, 1.5e10, 1.2, 1.1, 1.0e7, "seed", "high")
		require.NoError(t, err, "Failed to seed market data for %s", ticker)

		// Financial data (three recent periods to satisfy minimum data requirement)
		periods := []struct {
			period     string
			filingDate time.Time
			rev        float64
			oi         float64
		}{
			{"2021FY", now.AddDate(-3, 0, 0), 2.5e11, 7.5e9},
			{"2022FY", now.AddDate(-2, 0, 0), 2.8e11, 8.5e9},
			{"2023FY", now.AddDate(-1, 0, 0), 3.0e11, 1.0e10},
		}
		for _, p := range periods {
			_, err = db.Exec(`
				INSERT INTO financial_data (
					ticker, cik, filing_period, filing_date, as_of_date,
					operating_income, normalized_operating_income, revenue,
					interest_expense, tax_rate,
					total_assets, tangible_assets, goodwill, other_intangibles,
					total_debt, interest_bearing_debt,
					inventory, inventory_turnover, dead_inventory_writedown,
					dividends_per_share, net_income, gain_on_property_sales,
					depreciation_and_amortization, capital_expenditures, operating_cash_flow,
					current_assets, current_liabilities,
					cash_and_cash_equivalents, stockholders_equity,
					shares_outstanding, diluted_shares_outstanding,
					has_normalized_data, missing_fields, created_at, updated_at
				) VALUES (
					?, ?, ?, ?, ?,
					?, ?, ?,
					?, ?,
					?, ?, ?, ?,
					?, ?,
					?, ?, ?,
					?, ?, ?,
					?, ?, ?,
					?, ?,
					?, ?,
					?, ?,
					?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
				)
			`, ticker, "", p.period, p.filingDate, now,
				p.oi, p.oi, p.rev,
				1.0e9, 0.21,
				5.0e11, 4.5e11, 0.0, 0.0,
				2.0e10, 2.0e10,
				5.0e10, 5.0, 0.0,
				0.0, p.oi*0.8, 0.0, // dividends_per_share, net_income, gain_on_property_sales
				1.0e10, 1.2e10, p.oi*1.2, // D&A, CapEx, operating_cash_flow
				1.0e11, 8.0e10, // current_assets, current_liabilities
				5.0e10, 2.0e11, // cash_and_cash_equivalents, stockholders_equity
				1.5e10, 1.5e10,
				1, "[]")
			require.NoError(t, err, "Failed to seed financial data for %s %s", ticker, p.period)
		}
	}

	t.Log("✅ Basic test data seeded - financial/market/macro data present for AAPL/MSFT/GOOGL")
}

// MockSECServer creates an HTTP mock server that returns real Apple SEC data
func SetupMockSECServer(t *testing.T) *httptest.Server {
	// Read the real Apple SEC JSON
	jsonPath := filepath.Join("../../", "testdata", "CIK-example-2016onwards.min.json")
	appleJSON, err := os.ReadFile(jsonPath)
	require.NoError(t, err, "Failed to read Apple SEC test data")

	// Create mock server with debug logging
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("🔍 Mock SEC Server: %s %s", r.Method, r.URL.Path)

		// Handle ticker-to-CIK mapping requests
		if strings.Contains(r.URL.Path, "company_tickers.json") {
			t.Logf("✅ Serving ticker-CIK mapping")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			// Provide basic ticker mapping for AAPL
			_, _ = w.Write([]byte(`{"0": {"cik_str": "320193", "ticker": "AAPL", "title": "Apple Inc."}}`))
			return
		}

		// Check if this is a request for Apple's CIK (formatted with leading zeros)
		if strings.Contains(r.URL.Path, "CIK0000320193.json") || strings.Contains(r.URL.Path, "companyfacts/CIK0000320193.json") {
			t.Logf("✅ Serving Apple SEC data for: %s", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(appleJSON)
			return
		}

		// For other CIKs, return 404 (will trigger real SEC API calls)
		t.Logf("❌ Mock SEC Server: CIK not found for: %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error": "CIK not found"}`))
	}))

	return server
}

// SetupTestEnvironmentWithMockSEC creates test environment with mock SEC server
func SetupTestEnvironmentWithMockSEC(t *testing.T, mockSECURL string) *TestContainer {
	ctx := context.Background()

	var (
		redisContainer testcontainers.Container
		redisURL       string
	)

	// Fallback to in-memory cache on Windows or when NO_DOCKER=1
	if runtime.GOOS == "windows" || os.Getenv("NO_DOCKER") == "1" {
		redisURL = "redis://127.0.0.1:0"
	} else {
		redisContainer, redisURL = setupRedisContainer(t, ctx)
	}

	// Create test configuration with mock SEC URL
	cfg := createTestConfigWithMockSEC(redisURL, mockSECURL)

	// Declare variables
	var fairValueHandler *handlers.FairValueHandler
	var authService *auth.Service
	var database *sqlx.DB
	var valuationService *valuation.Service
	var rateLimiter *ratelimit.RateLimiter
	var healthHandler *handlers.HealthHandler
	var metricsService *metrics.Service
	var logger *zap.Logger

	// DI container
	app := fxtest.New(t,
		fx.Provide(func() *config.Config { return cfg }),
		di.CoreModule,
		di.ServiceModule,
		di.HandlerModule,
		fx.Provide(handlers.NewFairValueHandler),
		fx.Populate(&fairValueHandler, &authService, &database, &valuationService, &rateLimiter, &healthHandler, &metricsService, &logger),
	)

	app.RequireStart()

	SetupDatabase(t, database)
	SeedTestData(t, database)

	// Use the real API server with full middleware
	server := api.NewServer(cfg, logger, valuationService, authService, rateLimiter, healthHandler, metricsService)
	router := server.Engine()

	cleanup := func() {
		if app != nil {
			app.RequireStop()
		}
		if redisContainer != nil {
			_ = redisContainer.Terminate(ctx)
		}
	}

	return &TestContainer{
		RedisContainer:   redisContainer,
		RedisURL:         redisURL,
		Config:           cfg,
		App:              app,
		Router:           router,
		FairValueHandler: fairValueHandler,
		AuthService:      authService,
		cleanup:          cleanup,
	}
}

// createTestConfigWithMockSEC creates test config with custom SEC URL
func createTestConfigWithMockSEC(redisURL, mockSECURL string) *config.Config {
	cfg := createTestConfig(redisURL)

	// Override SEC config to use mock server
	cfg.SEC.BaseURL = mockSECURL
	cfg.SEC.TickerMappingURL = mockSECURL + "/company_tickers.json"
	cfg.SEC.UserAgent = "DCF-Valuation-API-Test/1.0"
	cfg.SEC.RateLimit = 100 // High limit for tests
	cfg.SEC.RequestTimeout = 30 * time.Second
	cfg.SEC.MaxRetries = 1 // Fast failure in tests

	// Disable external market calls for deterministic tests
	cfg.Market.YFinance.Enabled = false
	// Provide manual macro values for determinism
	cfg.Macro.FREDEnabled = false
	cfg.Macro.ManualRiskFreeRate = 0.04
	cfg.Macro.ManualMarketRiskPremium = 0.05

	return cfg
}
