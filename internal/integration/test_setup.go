package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/midas/dcf-valuation-api/internal/api/v1/handlers"
	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/di"
)

// TestContainer represents the test environment with containers and dependencies
type TestContainer struct {
	RedisContainer   testcontainers.Container
	RedisURL         string
	Config           *config.Config
	App              *fxtest.App
	Router           *gin.Engine
	FairValueHandler *handlers.FairValueHandler
	cleanup          func()
}

// SetupTestEnvironment creates a complete test environment with real infrastructure
func SetupTestEnvironment(t *testing.T) *TestContainer {
	ctx := context.Background()

	// Step 1: Start Redis container for real integration testing
	redisContainer, redisURL := setupRedisContainer(t, ctx)

	// Step 2: Create test configuration with real Redis and in-memory SQLite
	cfg := createTestConfig(redisURL)

	// Step 3: Declare variables first
	var fairValueHandler *handlers.FairValueHandler
	var database *sqlx.DB

	// Step 4: Create DI container with real services
	app := fxtest.New(t,
		// Provide test configuration
		fx.Provide(func() *config.Config { return cfg }),

		// Include all real services via DI module
		di.CoreModule,
		di.ServiceModule,

		// Provide handlers
		fx.Provide(handlers.NewFairValueHandler),

		// Extract handlers and database for testing
		fx.Populate(&fairValueHandler, &database),
	)

	// Step 5: Start the DI container
	app.RequireStart()

	// Step 6: Setup database schema and test data
	SetupDatabase(t, database)
	SeedTestData(t, database)

	// Step 6: Create Gin router with real middleware
	gin.SetMode(gin.TestMode)
	router := gin.New()

	// TODO: Add real middleware (auth, metrics, rate limiting)
	// For now, create basic routes for testing
	v1 := router.Group("/api/v1")

	// Handle empty ticker case for proper validation error (matching server.go)
	v1.GET("/fair-value/", func(c *gin.Context) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "ticker parameter is required",
			"code":  "INVALID_TICKER",
		})
	})
	v1.GET("/fair-value/:ticker", fairValueHandler.GetFairValue)
	v1.POST("/fair-value/bulk", fairValueHandler.GetBulkFairValue)

	// Step 6: Setup cleanup function
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
		cleanup:          cleanup,
	}
}

// Cleanup cleans up the test environment
func (tc *TestContainer) Cleanup() {
	if tc.cleanup != nil {
		tc.cleanup()
	}
}

// setupRedisContainer starts a Redis container for testing
func setupRedisContainer(t *testing.T, ctx context.Context) (testcontainers.Container, string) {
	// Create Redis container request
	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections"),
		Cmd:          []string{"redis-server", "--appendonly", "yes"},
	}

	// Start the container
	redisContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "Failed to start Redis container")

	// Get the mapped port
	mappedPort, err := redisContainer.MappedPort(ctx, "6379")
	require.NoError(t, err, "Failed to get Redis mapped port")

	// Get the host
	host, err := redisContainer.Host(ctx)
	require.NoError(t, err, "Failed to get Redis host")

	redisURL := fmt.Sprintf("redis://%s:%s", host, mappedPort.Port())

	// TODO: Add health check to ensure Redis is ready
	// For now, add a small delay
	time.Sleep(2 * time.Second)

	return redisContainer, redisURL
}

// createTestConfig creates a configuration optimized for integration testing
func createTestConfig(redisURL string) *config.Config {
	return &config.Config{
		Port:     "0", // Let system assign port for testing
		LogLevel: "debug",

		Database: config.DatabaseConfig{
			Driver:      "sqlite",
			SQLitePath:  ":memory:", // In-memory database for fast tests
			MaxOpenConn: 5,
			MaxIdleConn: 2,
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

	t.Log("✅ Basic test data seeded - financial data will be processed by real service pipeline")
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
			w.Write([]byte(`{"0": {"cik_str": "320193", "ticker": "AAPL", "title": "Apple Inc."}}`))
			return
		}

		// Check if this is a request for Apple's CIK (formatted with leading zeros)
		if strings.Contains(r.URL.Path, "CIK0000320193.json") || strings.Contains(r.URL.Path, "companyfacts/CIK0000320193.json") {
			t.Logf("✅ Serving Apple SEC data for: %s", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(appleJSON)
			return
		}

		// For other CIKs, return 404 (will trigger real SEC API calls)
		t.Logf("❌ Mock SEC Server: CIK not found for: %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error": "CIK not found"}`))
	}))

	return server
}

// SetupTestEnvironmentWithMockSEC creates test environment with mock SEC server
func SetupTestEnvironmentWithMockSEC(t *testing.T, mockSECURL string) *TestContainer {
	ctx := context.Background()

	// Step 1: Start Redis container for real integration testing
	redisContainer, redisURL := setupRedisContainer(t, ctx)

	// Step 2: Create test configuration with real Redis and mock SEC URL
	cfg := createTestConfigWithMockSEC(redisURL, mockSECURL)

	// Step 3: Declare variables first
	var fairValueHandler *handlers.FairValueHandler
	var database *sqlx.DB

	// Step 4: Create DI container with real services
	app := fxtest.New(t,
		// Provide test configuration with mock SEC URL
		fx.Provide(func() *config.Config { return cfg }),

		// Include all real services via DI module
		di.CoreModule,
		di.ServiceModule,

		// Provide handlers
		fx.Provide(handlers.NewFairValueHandler),

		// Extract handlers and database for testing
		fx.Populate(&fairValueHandler, &database),
	)

	// Step 5: Start the DI container
	app.RequireStart()

	// Step 6: Setup database schema and test data
	SetupDatabase(t, database)
	SeedTestData(t, database)

	// Step 7: Create Gin router with real middleware
	gin.SetMode(gin.TestMode)
	router := gin.New()

	// TODO: Add real middleware (auth, metrics, rate limiting)
	// For now, create basic routes for testing
	v1 := router.Group("/api/v1")

	// Handle empty ticker case for proper validation error (matching server.go)
	v1.GET("/fair-value/", func(c *gin.Context) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "ticker parameter is required",
			"code":  "INVALID_TICKER",
		})
	})
	v1.GET("/fair-value/:ticker", fairValueHandler.GetFairValue)
	v1.POST("/fair-value/bulk", fairValueHandler.GetBulkFairValue)

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

	return cfg
}
