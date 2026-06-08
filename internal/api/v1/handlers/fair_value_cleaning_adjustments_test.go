package handlers

// fair_value_cleaning_adjustments_test.go — TDB-11 (#11) tests for surfacing the
// cleaner audit trail (entities.ValuationResult.CleaningAdjustments) on the
// fair-value HTTP response as the `cleaning_adjustments` field.
//
// Coverage:
//  1. GET /:ticker — fired adjusters appear in cleaning_adjustments.
//  2. POST /:ticker — same projection (parity with GET).
//  3. POST /bulk — each result carries its own cleaning_adjustments.
//  4. omitempty — no adjusters ⇒ field absent from the JSON entirely.
//  5. POST{} ≡ GET — adding the field must not break the byte-identity contract.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/valuation"
)

// resultWithCleaningAdjustments returns a ValuationResult carrying two fired
// cleaner adjustments (one asset-quality OverlayEmitter, one liability-completeness
// OverlayEmitter) so the projection into the response can be asserted
// field-by-field. The RuleID / Category / Type triples are REAL wire values: the
// projection in adjustmentsFromLedger stamps Adjustment.RuleID from the config
// rule ID (goodwill_exclusion, contingent_liabilities, …), Category from the
// three entities.RuleCategory constants, and Type from the entities.AdjustmentType
// constants — NOT the AdjusterID ("A1") or fictional "asset"/"restate" strings.
func resultWithCleaningAdjustments(ticker string) *entities.ValuationResult {
	r := sampleValuationResult(ticker)
	r.CleaningAdjustments = []entities.Adjustment{
		{
			ID:          "adj-1",
			RuleID:      "goodwill_exclusion",
			Category:    entities.AssetQuality,
			Type:        entities.Exclude,
			Amount:      1234.5,
			FromAccount: "Goodwill",
			Reasoning:   "Excluded goodwill of $1234.5M from invested capital",
			Applied:     true,
			Timestamp:   time.Date(2025, 8, 13, 22, 15, 34, 0, time.UTC),
		},
		{
			ID:          "adj-2",
			RuleID:      "contingent_liabilities",
			Category:    entities.LiabilityCompleteness,
			Type:        entities.ProbabilityWeighted,
			Amount:      9876.0,
			FromAccount: "ContingentLiabilities",
			ToAccount:   "EstimatedLiabilities",
			Reasoning:   "Probability-weighted contingent liability estimate",
			Applied:     true,
			Timestamp:   time.Date(2025, 8, 13, 22, 15, 34, 0, time.UTC),
		},
	}
	return r
}

// TestFairValueResponse_CleaningAdjustments_GET asserts the GET single-ticker
// endpoint surfaces the cleaner audit trail with the meaningful projected fields.
func TestFairValueResponse_CleaningAdjustments_GET(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockSvc := new(mockValuationService)
	mockSvc.On("CalculateValuation", mock.Anything, "AAPL", (*valuation.ValuationOptions)(nil)).
		Return(resultWithCleaningAdjustments("AAPL"), nil)

	handler := newTestFairValueHandler(mockSvc)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/fair-value/AAPL", nil)
	c.Params = gin.Params{{Key: "ticker", Value: "AAPL"}}
	handler.GetFairValue(c)

	require.Equal(t, http.StatusOK, w.Code)

	var resp FairValueResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.CleaningAdjustments, 2, "both fired adjusters must surface")

	first := resp.CleaningAdjustments[0]
	assert.Equal(t, "goodwill_exclusion", first.Rule)
	assert.Equal(t, "asset_quality", first.Category)
	assert.Equal(t, "exclude", first.Type)
	assert.Equal(t, "Goodwill", first.FromAccount)
	assert.Empty(t, first.ToAccount, "to_account omitted for an exclude overlay")
	assert.InDelta(t, 1234.5, first.Amount, 1e-9)
	assert.Equal(t, "Excluded goodwill of $1234.5M from invested capital", first.Reasoning)

	second := resp.CleaningAdjustments[1]
	assert.Equal(t, "contingent_liabilities", second.Rule)
	assert.Equal(t, "liability_completeness", second.Category)
	assert.Equal(t, "probability_weighted", second.Type)
	assert.Equal(t, "ContingentLiabilities", second.FromAccount)
	assert.Equal(t, "EstimatedLiabilities", second.ToAccount)

	// The raw JSON must use the snake_case wire name and the real config rule ID.
	assert.Contains(t, w.Body.String(), `"cleaning_adjustments"`)
	assert.Contains(t, w.Body.String(), `"rule":"goodwill_exclusion"`)

	mockSvc.AssertExpectations(t)
}

// TestFairValueResponse_CleaningAdjustments_POST asserts the POST single-ticker
// endpoint projects the same audit trail as GET.
func TestFairValueResponse_CleaningAdjustments_POST(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockSvc := new(mockValuationService)
	mockSvc.On("CalculateValuation", mock.Anything, "AAPL", (*valuation.ValuationOptions)(nil)).
		Return(resultWithCleaningAdjustments("AAPL"), nil)

	handler := newTestFairValueHandler(mockSvc)
	r := newPostTestRouter(handler)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/fair-value/AAPL", nil))
	require.Equal(t, http.StatusOK, w.Code)

	var resp FairValueResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.CleaningAdjustments, 2)
	assert.Equal(t, "goodwill_exclusion", resp.CleaningAdjustments[0].Rule)
	assert.Equal(t, "contingent_liabilities", resp.CleaningAdjustments[1].Rule)

	// POST projects the same audit trail as GET (same fixture, same builder) —
	// the field-by-field GET-vs-POST equality contract claimed in the header.
	getResp := buildGetCleaningAdjustments(t, mockSvc)
	assert.Equal(t, getResp, resp.CleaningAdjustments,
		"POST cleaning_adjustments must be byte-equal to the GET projection")

	mockSvc.AssertExpectations(t)
}

// buildGetCleaningAdjustments drives the GET single-ticker endpoint against the
// same mocked service and returns its projected cleaning_adjustments slice, so a
// caller can assert GET ≡ POST equality without duplicating the request setup.
func buildGetCleaningAdjustments(t *testing.T, mockSvc *mockValuationService) []CleaningAdjustment {
	t.Helper()
	handler := newTestFairValueHandler(mockSvc)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/fair-value/AAPL", nil)
	c.Params = gin.Params{{Key: "ticker", Value: "AAPL"}}
	handler.GetFairValue(c)
	require.Equal(t, http.StatusOK, w.Code)

	var resp FairValueResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.CleaningAdjustments
}

// TestFairValueResponse_CleaningAdjustments_Bulk asserts the bulk endpoint
// carries cleaning_adjustments on each per-ticker result via the shared builder.
func TestFairValueResponse_CleaningAdjustments_Bulk(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockSvc := new(mockValuationService)
	mockSvc.On("CalculateValuation", mock.Anything, "AAPL", (*valuation.ValuationOptions)(nil)).
		Return(resultWithCleaningAdjustments("AAPL"), nil)

	handler := newTestFairValueHandler(mockSvc)
	r := newPostTestRouter(handler)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/fair-value/bulk", strings.NewReader(`{"tickers":["AAPL"]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp BulkFairValueResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Results, 1)
	require.Len(t, resp.Results[0].CleaningAdjustments, 2)
	assert.Equal(t, "goodwill_exclusion", resp.Results[0].CleaningAdjustments[0].Rule)

	mockSvc.AssertExpectations(t)
}

// TestFairValueResponse_CleaningAdjustments_OmittedWhenEmpty asserts that a
// result with no fired adjusters drops the field from the JSON entirely
// (omitempty), preserving byte-identity with pre-TDB-11 captures.
func TestFairValueResponse_CleaningAdjustments_OmittedWhenEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockSvc := new(mockValuationService)
	mockSvc.On("CalculateValuation", mock.Anything, "AAPL", (*valuation.ValuationOptions)(nil)).
		Return(sampleValuationResult("AAPL"), nil) // no CleaningAdjustments

	handler := newTestFairValueHandler(mockSvc)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/fair-value/AAPL", nil)
	c.Params = gin.Params{{Key: "ticker", Value: "AAPL"}}
	handler.GetFairValue(c)

	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "cleaning_adjustments",
		"empty audit trail must be omitted from the JSON")

	var resp FairValueResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Nil(t, resp.CleaningAdjustments)

	mockSvc.AssertExpectations(t)
}
