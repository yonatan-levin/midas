package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/api/v1/handlers"
	coreEntities "github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestE2E_Live_FairValue_RealAPIs executes real external requests (SEC/market/macro)
// Only runs when E2E_LIVE=1 and is skipped on Windows.
func TestE2E_Live_FairValue_RealAPIs(t *testing.T) {
	if os.Getenv("E2E_LIVE") != "1" {
		t.Skip("Skipping live E2E test: set E2E_LIVE=1 to run against real external APIs")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Skipping live E2E test on Windows environment")
	}

	testEnv := SetupTestEnvironment(t)
	if testEnv == nil {
		return
	}
	defer testEnv.Cleanup()

	// Issue a real API key with fair-value permission
	apiKey, err := testEnv.NewTestAPIKey(context.Background(), "live-user", []coreEntities.Permission{coreEntities.PermissionReadFairValue})
	require.NoError(t, err)

	// Start a real HTTP server backed by the fully-wired Gin engine
	ts := httptest.NewServer(testEnv.Router)
	defer ts.Close()

	client := &http.Client{Timeout: 45 * time.Second}
	for _, ticker := range []string{"AAPL", "MSFT", "TSLA"} {
		t.Run("live_"+ticker, func(t *testing.T) {
			req, err := http.NewRequest("GET", ts.URL+"/api/v1/fair-value/"+ticker, nil)
			require.NoError(t, err)
			req.Header.Set("X-API-Key", apiKey.Key)

			resp, err := client.Do(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()

			require.Equal(t, http.StatusOK, resp.StatusCode, "expected 200 for "+ticker)

			var body handlers.FairValueResponse
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

			assert.Equal(t, ticker, body.Ticker)
			assert.NotEmpty(t, body.AsOf)
			assert.Greater(t, body.WACC, 0.0)
			assert.NotZero(t, body.GrowthRate)
			assert.NotZero(t, body.TangibleValuePerShare)
			assert.Greater(t, body.DCFValuePerShare, 0.0)
		})
	}
}

// TestE2E_Live_BulkFairValue verifies bulk valuation with real external dependencies
// Only runs when E2E_LIVE=1 and is skipped on Windows.
func TestE2E_Live_BulkFairValue(t *testing.T) {
	if os.Getenv("E2E_LIVE") != "1" {
		t.Skip("Skipping live bulk E2E test: set E2E_LIVE=1 to run against real external APIs")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Skipping live E2E test on Windows environment")
	}

	testEnv := SetupTestEnvironment(t)
	if testEnv == nil {
		return
	}
	defer testEnv.Cleanup()

	apiKey, err := testEnv.NewTestAPIKey(context.Background(), "live-user", []coreEntities.Permission{coreEntities.PermissionReadFairValue})
	require.NoError(t, err)

	ts := httptest.NewServer(testEnv.Router)
	defer ts.Close()

	client := &http.Client{Timeout: 60 * time.Second}

	reqBody := handlers.BulkFairValueRequest{Tickers: []string{"AAPL", "MSFT", "TSLA"}}
	payload, err := json.Marshal(reqBody)
	require.NoError(t, err)

	req, err := http.NewRequest("POST", ts.URL+"/api/v1/fair-value/bulk", bytes.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey.Key)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body handlers.BulkFairValueResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	assert.Equal(t, 3, body.Summary.TotalRequested)
	assert.Equal(t, 3, body.Summary.Successful)
	assert.Len(t, body.Results, 3)
	// Sanity: each result should have positive DCF
	for _, r := range body.Results {
		assert.Greater(t, r.DCFValuePerShare, 0.0)
	}
}
