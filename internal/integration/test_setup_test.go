package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	coreEntities "github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/stretchr/testify/require"
)

// TestCreateTestConfigDefaults verifies base test config values
func TestCreateTestConfigDefaults(t *testing.T) {
	cfg := createTestConfig("redis://127.0.0.1:0")
	require.Equal(t, "sqlite3", cfg.Database.Driver)
	require.Equal(t, ":memory:", cfg.Database.SQLitePath)
	require.Equal(t, 1, cfg.Database.MaxOpenConn)
	require.Equal(t, 1, cfg.Database.MaxIdleConn)
	require.NotZero(t, cfg.Valuation.DCFProjectionYears)
	require.True(t, cfg.DataCleaner.Enabled)
	require.NotEmpty(t, cfg.SEC.BaseURL)
}

// TestCreateTestConfigWithMockSECOverride ensures SEC overrides & macro/manuals are applied
func TestCreateTestConfigWithMockSECOverride(t *testing.T) {
	// Spin a tiny mock SEC to get a URL
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "1"})
	}))
	defer mock.Close()

	cfg := createTestConfigWithMockSEC("redis://127.0.0.1:0", mock.URL)
	require.Equal(t, mock.URL, cfg.SEC.BaseURL)
	require.Equal(t, mock.URL+"/company_tickers.json", cfg.SEC.TickerMappingURL)
	require.False(t, cfg.Market.YFinance.Enabled)
	require.False(t, cfg.Macro.FREDEnabled)
	require.NotZero(t, cfg.Macro.ManualRiskFreeRate)
	require.NotZero(t, cfg.Macro.ManualMarketRiskPremium)
}

// TestSetupDatabaseAndSeedTestData covers schema creation and seed inserts
func TestSetupDatabaseAndSeedTestData(t *testing.T) {
	db, err := sqlx.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	SetupDatabase(t, db)
	SeedTestData(t, db)

	// verify macro row exists
	var macroCount int
	require.NoError(t, db.Get(&macroCount, "SELECT COUNT(1) FROM macro_data"))
	require.GreaterOrEqual(t, macroCount, 1)

	// verify market rows exist for three tickers
	var marketCount int
	require.NoError(t, db.Get(&marketCount, "SELECT COUNT(1) FROM market_data"))
	require.GreaterOrEqual(t, marketCount, 3)

	// verify at least 3 financial periods inserted per ticker (>=9 total)
	var finCount int
	require.NoError(t, db.Get(&finCount, "SELECT COUNT(1) FROM financial_data"))
	require.GreaterOrEqual(t, finCount, 9)
}

// TestSetupMockSECServer_Endpoints exercises mapping and CIK paths
func TestSetupMockSECServer_Endpoints(t *testing.T) {
	srv := SetupMockSECServer(t)
	t.Cleanup(srv.Close)

	// ticker mapping
	resp, err := http.Get(srv.URL + "/company_tickers.json")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	// AAPL CIK path
	resp, err = http.Get(srv.URL + "/companyfacts/CIK0000320193.json")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	// Unknown CIK -> 404
	resp, err = http.Get(srv.URL + "/companyfacts/CIK0000000000.json")
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	_ = resp.Body.Close()
}

// Smoke: ensure NewTestAPIKey helper works through AuthService
func TestTestContainer_NewTestAPIKey(t *testing.T) {
	env := SetupTestEnvironment(t)
	if env == nil {
		return
	}
	defer env.Cleanup()

	key, err := env.NewTestAPIKey(context.Background(), "smoke-user", []coreEntities.Permission{coreEntities.PermissionReadFairValue})
	require.NoError(t, err)
	require.NotEmpty(t, key.Key)
}
