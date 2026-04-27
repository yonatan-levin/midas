package integration

import (
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

// TestE2E_TSM_ProducesValuation is the headline acceptance test for Phase B
// of the IFRS-FPI plan (docs/refactoring/ifrs-foreign-private-issuer-support-spec.md).
//
// It executes the FULL pipeline against live SEC + Yahoo Finance + FRED:
//
//  1. SEC parser reads TSM's IFRS-full XBRL (Phase B5/B6) and stamps each
//     period with ReportingCurrency="TWD".
//  2. valuation/currency.go: convertFinancialsToUSD looks up TWD/USD via
//     FRED (DEXTAIUS series) and FX-multiplies every monetary field
//     (Phase B9). Each period's ReportingCurrency becomes "USD".
//  3. valuation/currency.go: applyADRRatio divides ordinary-share counts
//     by 5 (Phase B10) so per-ADR fair value matches the listed ADR price.
//  4. The handler surfaces currency="USD" and adr_ratio_applied=5 on the
//     response (Phase B12).
//
// Gated by E2E_LIVE=1 because it consumes live API quotas (SEC EDGAR
// rate-limited at 10 req/sec, FRED requires a key, Yahoo cookie+crumb
// auth). Skipped on Windows for the same reason as the other E2E tests:
// the SEC User-Agent and Yahoo cookie flow are flakier on the Windows CI
// runner than on macOS/Linux.
//
// Manual smoke equivalent (for the human reviewer):
//
//	go run ./cmd/migrate -db ./data/midas.db
//	go run cmd/server/main.go &
//	curl -s -H "X-API-Key: <demo>" http://localhost:8080/api/v1/fair-value/TSM \
//	  | jq '.dcf_value_per_share, .currency, .adr_ratio_applied'
func TestE2E_TSM_ProducesValuation(t *testing.T) {
	if os.Getenv("E2E_LIVE") != "1" {
		t.Skip("Skipping live IFRS E2E: set E2E_LIVE=1 to run against real SEC + Yahoo + FRED")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Skipping live E2E test on Windows environment")
	}

	testEnv := SetupTestEnvironment(t)
	if testEnv == nil {
		return
	}
	defer testEnv.Cleanup()

	apiKey, err := testEnv.NewTestAPIKey(
		context.Background(),
		"ifrs-fpi-e2e",
		[]coreEntities.Permission{coreEntities.PermissionReadFairValue},
	)
	require.NoError(t, err)

	ts := httptest.NewServer(testEnv.Router)
	defer ts.Close()

	client := &http.Client{Timeout: 90 * time.Second}

	req, err := http.NewRequest("GET", ts.URL+"/api/v1/fair-value/TSM", nil)
	require.NoError(t, err)
	req.Header.Set("X-API-Key", apiKey.Key)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode,
		"expected 200 for TSM; if 422 FOREIGN_PRIVATE_ISSUER_UNSUPPORTED, the IFRS-FPI pipeline is broken")

	var body handlers.FairValueResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	// Phase B12 transparency fields.
	assert.Equal(t, "USD", body.Currency,
		"Currency must be USD post-Phase-B9 FX conversion")
	assert.Equal(t, 5, body.ADRRatioApplied,
		"TSM ADR ratio is 5 ordinary:1 ADR per config/adr_ratios.json")

	// Headline numbers — the proof Phase B is wired correctly.
	assert.Greater(t, body.DCFValuePerShare, 0.0,
		"DCFValuePerShare must be positive for TSM")
	assert.Greater(t, body.WACC, 0.0, "WACC must be positive")
	assert.NotZero(t, body.GrowthRate, "GrowthRate must be set")

	// Plausibility fence — TSM's per-ADR Damodaran-style intrinsic should
	// land in the $50-$2000 range. Way outside that means the ADR ratio or
	// FX conversion misfired (e.g., applied twice, applied to the wrong
	// fields, or ratio off by a factor of 100).
	assert.GreaterOrEqual(t, body.DCFValuePerShare, 50.0,
		"TSM DCFValuePerShare too low — ADR ratio likely double-applied")
	assert.LessOrEqual(t, body.DCFValuePerShare, 2000.0,
		"TSM DCFValuePerShare too high — FX conversion likely missing or inverted")

	// Industry sanity: TSM is a semiconductor manufacturer, SIC=3674.
	require.NotNil(t, body.Industry, "Industry field must be populated")
	assert.NotEmpty(t, body.Industry.SICCode)
	assert.Equal(t, "MFG", body.Industry.SIC,
		"TSM SIC label should be MFG (semiconductor manufacturer)")
}
