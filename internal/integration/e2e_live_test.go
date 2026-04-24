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

			// Industry field smoke-check: present and well-formed per the
			// "Industry in fair-value response" design
			// (docs/superpowers/specs/2026-04-23-industry-in-response-design.md).
			// Detailed per-ticker expectations live in
			// TestE2E_Live_FairValue_IndustryField below.
			require.NotNil(t, body.Industry, "Industry field must be populated")
			assert.NotEmpty(t, body.Industry.SICCode, "raw SIC code must be present")
			assert.NotEmpty(t, body.Industry.SIC, "SIC classifier label must be present")
		})
	}
}

// TestE2E_Live_FairValue_IndustryField verifies the Industry field is populated
// and well-formed for real tickers via live SEC/Yahoo/FRED calls, and documents
// the current heuristic-classifier gaps as expected Match outcomes.
//
// Heuristic-side classification has known gaps captured in
// docs/refactoring/industry-classification-unification-spec.md (the SIC-only
// unification will retire the heuristic). Those gaps mean Match legitimately
// returns false for financial and owned-store-retail tickers today. This test
// pins that behavior so the gap is visible in CI and detects silent regressions.
// The SIC-side label is deterministic from the SEC SIC code and IS pinned.
//
// Update the expected Match value per-ticker when the heuristic classifier or
// data pipeline improves (e.g., populating R&D on FinancialData for AMD,
// adding GICS-40 sector config for Financials).
func TestE2E_Live_FairValue_IndustryField(t *testing.T) {
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

	apiKey, err := testEnv.NewTestAPIKey(context.Background(), "live-user", []coreEntities.Permission{coreEntities.PermissionReadFairValue})
	require.NoError(t, err)

	ts := httptest.NewServer(testEnv.Router)
	defer ts.Close()

	client := &http.Client{Timeout: 90 * time.Second}

	// Expected values captured on 2026-04-24 live data. Matching SIC is
	// deterministic from SEC data. Match reflects the current heuristic
	// gaps (see the class doc above).
	cases := []struct {
		ticker      string
		expectedSIC string
		expectMatch bool
		rationale   string
	}{
		{
			ticker:      "NVDA",
			expectedSIC: "MFG",
			expectMatch: true,
			rationale:   "Semiconductor with visible R&D; heuristic → GICS 45, MFG multi-maps {20,45} so match",
		},
		{
			ticker:      "JPM",
			expectedSIC: "FIN",
			expectMatch: false,
			rationale:   "Bank; ClassifyIndustry has no GICS-40 config, defaults to 20 — pre-existing gap. Also regression sentinel for the FINL→FIN label fix",
		},
		{
			ticker:      "TGT",
			expectedSIC: "RETAIL",
			expectMatch: false,
			rationale:   "Owned-store retailer; tangibles>70% fails the tangibles branch and intangibles<10% fails the intangibles branch, so isRetailCompany rejects and isManufacturingCompany picks up — new gap surfaced by live QA 2026-04-24",
		},
	}

	for _, tc := range cases {
		t.Run("industry_"+tc.ticker, func(t *testing.T) {
			req, err := http.NewRequest("GET", ts.URL+"/api/v1/fair-value/"+tc.ticker, nil)
			require.NoError(t, err)
			req.Header.Set("X-API-Key", apiKey.Key)

			resp, err := client.Do(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()

			require.Equal(t, http.StatusOK, resp.StatusCode, "expected 200 for "+tc.ticker)

			var body handlers.FairValueResponse
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

			require.NotNil(t, body.Industry, "Industry field must be populated for "+tc.ticker)
			assert.NotEmpty(t, body.Industry.SICCode, "raw SIC code must be present")
			assert.Equal(t, tc.expectedSIC, body.Industry.SIC, "SIC classifier label mismatch — %s", tc.rationale)
			assert.Equal(t, tc.expectMatch, body.Industry.Match, "Match flag mismatch vs documented live behavior — %s", tc.rationale)
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
