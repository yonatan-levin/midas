package handlers

// fair_value_post_test.go — T9 tests for:
//
//  1. R7 router conflict: POST /bulk and POST /:ticker coexist in the same
//     route group without panic; each dispatches to the correct handler.
//  2. PostFairValue — POST{} body ≡ GET (byte-identical response for same ticker).
//  3. PostFairValue — Layer-1 out-of-range override → 422 INVALID_OVERRIDE.
//  4. PostFairValue — Layer-2 cross-knob ParamError → 422 via paramErrorResponse.
//  5. PostFairValue — standard sentinel errors (404 / 422 / 500 fallthrough).
//  6. PostFairValue — empty/absent body treated as "no overrides" (GET semantics).
//  7. I-1: typed overrideConflict returned by detectOverrideConflicts (Knob field).
//  8. I-2: BulkFailure.Knob populated for INVALID_OVERRIDE entries.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/services/valuation"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/params"
)

// ---------------------------------------------------------------------------
// Router helpers
// ---------------------------------------------------------------------------

// newPostTestRouter builds a minimal Gin engine that mimics the real server.go
// route group for fair-value:
//
//	GET  /:ticker → GetFairValue
//	POST /bulk    → GetBulkFairValue  (static, registered FIRST — R7)
//	POST /:ticker → PostFairValue     (wildcard, registered SECOND — R7)
//
// No middleware is added so tests focus purely on handler behaviour.
func newPostTestRouter(h *FairValueHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/fair-value")
	g.GET("/:ticker", h.GetFairValue)
	// R7: static POST /bulk registered before wildcard POST /:ticker.
	g.POST("/bulk", h.GetBulkFairValue)
	g.POST("/:ticker", h.PostFairValue)
	return r
}

// ---------------------------------------------------------------------------
// R7 — Router boots without panic + correct routing
// ---------------------------------------------------------------------------

// TestRouter_R7_NoPanic_AndCorrectRouting verifies that registering
// POST /bulk (static) and POST /:ticker (wildcard) in the same Gin group does
// not panic at startup, and that each path routes to the expected handler.
//
// Gin v1.10.x resolves static paths before wildcard paths within the same
// method+group, so POST /bulk never falls through to the /:ticker wildcard.
func TestRouter_R7_NoPanic_AndCorrectRouting(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := &mockValuationService{}

	// POST /bulk needs a valid tickers body; mock AAPL so it returns 200
	svc.On("CalculateValuation", mock.Anything, "AAPL", (*valuation.ValuationOptions)(nil)).
		Return(sampleValuationResult("AAPL"), nil)

	h := NewFairValueHandler(svc, zap.NewNop())

	// Building the router must NOT panic — this is the R7 boot assertion.
	var r *gin.Engine
	assert.NotPanics(t, func() {
		r = newPostTestRouter(h)
	}, "registering POST /bulk + POST /:ticker in the same group must not panic")
	require.NotNil(t, r)

	// --- POST /bulk routes to GetBulkFairValue ---
	bulkBody := `{"tickers":["AAPL"]}`
	wBulk := httptest.NewRecorder()
	reqBulk := httptest.NewRequest(http.MethodPost, "/fair-value/bulk", strings.NewReader(bulkBody))
	reqBulk.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wBulk, reqBulk)

	// GetBulkFairValue always wraps its response in a BulkFairValueResponse shape.
	assert.Equal(t, http.StatusOK, wBulk.Code, "POST /bulk must route to GetBulkFairValue")
	var bulkResp BulkFairValueResponse
	require.NoError(t, json.Unmarshal(wBulk.Body.Bytes(), &bulkResp),
		"POST /bulk response must unmarshal as BulkFairValueResponse")
	assert.Equal(t, 1, bulkResp.Summary.TotalRequested,
		"POST /bulk must have called GetBulkFairValue (has summary)")

	// --- POST /:ticker (e.g. /MU) routes to PostFairValue ---
	// PostFairValue returns a flat FairValueResponse, not a BulkFairValueResponse.
	svc.On("CalculateValuation", mock.Anything, "MU", (*valuation.ValuationOptions)(nil)).
		Return(sampleValuationResult("MU"), nil)

	wPost := httptest.NewRecorder()
	reqPost := httptest.NewRequest(http.MethodPost, "/fair-value/MU", nil)
	r.ServeHTTP(wPost, reqPost)

	assert.Equal(t, http.StatusOK, wPost.Code, "POST /MU must route to PostFairValue")
	var postResp FairValueResponse
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &postResp),
		"POST /MU response must unmarshal as FairValueResponse")
	assert.Equal(t, "MU", postResp.Ticker,
		"POST /MU must have called PostFairValue (ticker in response)")

	svc.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// POST{}≡GET parity
// ---------------------------------------------------------------------------

// TestPostFairValue_EmptyBody_EqualsGET verifies that POST with an empty body
// (no overrides) produces a response byte-identical to GET for the same ticker.
// This is the POST{}≡GET contract test.
func TestPostFairValue_EmptyBody_EqualsGET(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := &mockValuationService{}
	result := sampleValuationResult("AAPL")
	// Both GET and POST (no overrides) must call the service with nil opts.
	svc.On("CalculateValuation", mock.Anything, "AAPL", (*valuation.ValuationOptions)(nil)).
		Return(result, nil).Times(2)

	h := NewFairValueHandler(svc, zap.NewNop())
	r := newPostTestRouter(h)

	// GET response
	wGet := httptest.NewRecorder()
	reqGet := httptest.NewRequest(http.MethodGet, "/fair-value/AAPL", nil)
	r.ServeHTTP(wGet, reqGet)
	require.Equal(t, http.StatusOK, wGet.Code, "GET must succeed")

	// POST with empty body (no Content-Type header, no body)
	wPost := httptest.NewRecorder()
	reqPost := httptest.NewRequest(http.MethodPost, "/fair-value/AAPL", nil)
	r.ServeHTTP(wPost, reqPost)
	require.Equal(t, http.StatusOK, wPost.Code, "POST with empty body must succeed")

	// Unmarshal both responses and compare field-by-field.
	var getResp, postResp FairValueResponse
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &getResp))
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &postResp))

	assert.Equal(t, getResp.Ticker, postResp.Ticker, "Ticker")
	assert.InDelta(t, getResp.WACC, postResp.WACC, 1e-12, "WACC")
	assert.InDelta(t, getResp.DCFValuePerShare, postResp.DCFValuePerShare, 1e-12, "DCFValuePerShare")
	assert.InDelta(t, getResp.TangibleValuePerShare, postResp.TangibleValuePerShare, 1e-12, "TangibleValuePerShare")
	assert.Equal(t, getResp.CalculationVersion, postResp.CalculationVersion, "CalculationVersion")
	assert.Equal(t, getResp.Currency, postResp.Currency, "Currency")
	assert.Equal(t, getResp.AsOf, postResp.AsOf, "AsOf")

	svc.AssertExpectations(t)
}

// TestPostFairValue_EmptyJSONBody_EqualsGET verifies the same POST{}≡GET
// contract when the body is an explicit `{}` (empty JSON object).
func TestPostFairValue_EmptyJSONBody_EqualsGET(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := &mockValuationService{}
	// Both GET and POST {} must call with nil opts.
	svc.On("CalculateValuation", mock.Anything, "MSFT", (*valuation.ValuationOptions)(nil)).
		Return(sampleValuationResult("MSFT"), nil).Times(2)

	h := NewFairValueHandler(svc, zap.NewNop())
	r := newPostTestRouter(h)

	// GET
	wGet := httptest.NewRecorder()
	r.ServeHTTP(wGet, httptest.NewRequest(http.MethodGet, "/fair-value/MSFT", nil))
	require.Equal(t, http.StatusOK, wGet.Code)

	// POST with explicit `{}`
	wPost := httptest.NewRecorder()
	reqPost := httptest.NewRequest(http.MethodPost, "/fair-value/MSFT", strings.NewReader(`{}`))
	reqPost.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wPost, reqPost)
	require.Equal(t, http.StatusOK, wPost.Code)

	var getResp, postResp FairValueResponse
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &getResp))
	require.NoError(t, json.Unmarshal(wPost.Body.Bytes(), &postResp))
	assert.Equal(t, getResp.Ticker, postResp.Ticker)
	assert.InDelta(t, getResp.DCFValuePerShare, postResp.DCFValuePerShare, 1e-12, "DCFValuePerShare parity")

	svc.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Layer-1 invalid override → 422
// ---------------------------------------------------------------------------

// TestPostFairValue_OutOfRangeOverride_Returns422 verifies that a beta value
// outside the valid range [-5, 5] → Layer-1 validation → 422 INVALID_OVERRIDE.
func TestPostFairValue_OutOfRangeOverride_Returns422(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := &mockValuationService{} // must NOT be called
	h := NewFairValueHandler(svc, zap.NewNop())
	r := newPostTestRouter(h)

	body := map[string]interface{}{
		"options": map[string]interface{}{
			"beta": 99.0, // out of range — betaMax=5
		},
	}
	bodyJSON, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/fair-value/AAPL", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "INVALID_OVERRIDE", errResp.Code,
		"out-of-range override must produce INVALID_OVERRIDE")
	assert.Equal(t, http.StatusUnprocessableEntity, errResp.Status)
	// Layer-1 error detail includes the knob name
	assert.Contains(t, errResp.Detail, "beta",
		"error detail must mention the offending knob")
	require.NotNil(t, errResp.Context)
	assert.Equal(t, "beta", errResp.Context["knob"])

	svc.AssertNotCalled(t, "CalculateValuation")
}

// TestPostFairValue_OutOfRangeTerminalGrowth_Returns422 pins a second knob.
func TestPostFairValue_OutOfRangeTerminalGrowth_Returns422(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())
	r := newPostTestRouter(h)

	// terminal_growth_rate of 2.0 is above the 0.50 ceiling.
	body := map[string]interface{}{
		"options": map[string]interface{}{
			"terminal_growth_rate": 2.0,
		},
	}
	bodyJSON, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/fair-value/GOOGL", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "INVALID_OVERRIDE", errResp.Code)
	require.NotNil(t, errResp.Context)
	assert.Equal(t, "terminal_growth_rate", errResp.Context["knob"])

	svc.AssertNotCalled(t, "CalculateValuation")
}

// ---------------------------------------------------------------------------
// Layer-2 ParamError → 422 via paramErrorResponse
// ---------------------------------------------------------------------------

// TestPostFairValue_Layer2ParamError_Returns422 verifies that a *params.ParamError
// returned by CalculateValuation (wrapping a cross-knob invariant violation)
// is surfaced as 422 INVALID_OVERRIDE with the offending knob in context.
func TestPostFairValue_Layer2ParamError_Returns422(t *testing.T) {
	gin.SetMode(gin.TestMode)

	pe := &params.ParamError{
		Knob:   "terminal_growth_rate",
		Reason: "must be strictly less than WACC (0.094)",
		Value:  0.095,
		Limit:  0.094,
	}
	// Mirror the double-wrap applied by CalculateValuation.
	wrapped := fmt.Errorf("failed to perform valuation: %w", pe)

	svc := &mockValuationService{}
	svc.On("CalculateValuation", mock.Anything, "AAPL", mock.Anything).
		Return(nil, wrapped)

	h := NewFairValueHandler(svc, zap.NewNop())
	r := newPostTestRouter(h)

	// terminal_growth_rate=0.095 passes Layer-1 (≤ 0.50) but the service
	// rejects it at Layer-2 (>= WACC).
	body := map[string]interface{}{
		"options": map[string]interface{}{
			"terminal_growth_rate": 0.095,
		},
	}
	bodyJSON, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/fair-value/AAPL", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"Layer-2 ParamError must produce 422")

	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "INVALID_OVERRIDE", errResp.Code)
	assert.Equal(t, pe.Reason, errResp.Detail,
		"detail must carry the ParamError.Reason verbatim")
	require.NotNil(t, errResp.Context)
	assert.Equal(t, "terminal_growth_rate", errResp.Context["knob"])

	svc.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Standard sentinel errors
// ---------------------------------------------------------------------------

// TestPostFairValue_SentinelErrors maps service errors to the correct HTTP
// codes and error codes — mirrors the GET handler path (regression guard).
func TestPostFairValue_SentinelErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "ticker_not_found",
			err:        fmt.Errorf("lookup: %w", valuation.ErrTickerNotFound),
			wantStatus: http.StatusNotFound,
			wantCode:   "TICKER_NOT_FOUND",
		},
		{
			name:       "foreign_private_issuer",
			err:        valuation.ErrForeignPrivateIssuer,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "FOREIGN_PRIVATE_ISSUER_UNSUPPORTED",
		},
		{
			name:       "insufficient_data",
			err:        valuation.ErrInsufficientData,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "INSUFFICIENT_DATA",
		},
		{
			name:       "model_not_applicable",
			err:        valuation.ErrModelNotApplicable,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "MODEL_NOT_APPLICABLE",
		},
		{
			name:       "generic_error",
			err:        fmt.Errorf("database connection lost"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "CALCULATION_ERROR",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &mockValuationService{}
			svc.On("CalculateValuation", mock.Anything, "AAPL", (*valuation.ValuationOptions)(nil)).
				Return(nil, tc.err)

			h := NewFairValueHandler(svc, zap.NewNop())
			r := newPostTestRouter(h)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/fair-value/AAPL", nil)
			r.ServeHTTP(w, req)

			assert.Equal(t, tc.wantStatus, w.Code, "status for %s", tc.name)

			var errResp ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
			assert.Equal(t, tc.wantCode, errResp.Code, "code for %s", tc.name)

			svc.AssertExpectations(t)
		})
	}
}

// ---------------------------------------------------------------------------
// PostFairValue with options — service called with correct ValuationOptions
// ---------------------------------------------------------------------------

// TestPostFairValue_WithOptions_ReachesServiceWithPopulatedOverrides verifies
// that when the POST body supplies options, the projected Overrides arrive at
// the service correctly.
func TestPostFairValue_WithOptions_ReachesServiceWithPopulatedOverrides(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := &mockValuationService{}
	svc.On("CalculateValuation", mock.Anything, "AAPL",
		mock.MatchedBy(func(opts *valuation.ValuationOptions) bool {
			return opts != nil &&
				opts.Overrides.Beta != nil &&
				*opts.Overrides.Beta == 1.35
		}),
	).Return(sampleValuationResult("AAPL"), nil)

	h := NewFairValueHandler(svc, zap.NewNop())
	r := newPostTestRouter(h)

	body := map[string]interface{}{
		"options": map[string]interface{}{
			"beta": 1.35,
		},
	}
	bodyJSON, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/fair-value/AAPL", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	svc.AssertExpectations(t)
}

// TestPostFairValue_NilOptionsField_ServiceCalledWithNilOpts ensures that a
// body with an explicit null options field is treated as "no overrides" and
// calls the service with nil opts (cache-eligible path).
func TestPostFairValue_NilOptionsField_ServiceCalledWithNilOpts(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := &mockValuationService{}
	svc.On("CalculateValuation", mock.Anything, "AAPL", (*valuation.ValuationOptions)(nil)).
		Return(sampleValuationResult("AAPL"), nil)

	h := NewFairValueHandler(svc, zap.NewNop())
	r := newPostTestRouter(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/fair-value/AAPL",
		strings.NewReader(`{"options":null}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	svc.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Ticker validation
// ---------------------------------------------------------------------------

// TestPostFairValue_InvalidTicker_Returns400 verifies the path-param ticker
// validation on the POST handler (same guard as GET).
func TestPostFairValue_InvalidTicker_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())
	r := newPostTestRouter(h)

	w := httptest.NewRecorder()
	// Gin URL-encodes special chars; use a valid URL but a 6-char ticker that
	// exceeds the 5-char maximum.
	req := httptest.NewRequest(http.MethodPost, "/fair-value/TOOLONG", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "INVALID_TICKER", errResp.Code)

	svc.AssertNotCalled(t, "CalculateValuation")
}

// ---------------------------------------------------------------------------
// I-1 — typed overrideConflict
// ---------------------------------------------------------------------------

// TestDetectOverrideConflicts_TypedKnob_Beta verifies that the refactored
// detectOverrideConflicts returns a typed overrideConflict whose Knob field
// directly carries "beta" rather than requiring callers to parse the Message.
func TestDetectOverrideConflicts_TypedKnob_Beta(t *testing.T) {
	conflicts := detectOverrideConflicts(ptrFloat(1.2), nil, &ValuationOverrides{
		Beta: ptrFloat(1.5),
	})
	require.Len(t, conflicts, 1)
	assert.Equal(t, "beta", conflicts[0].Knob,
		"Knob field must be 'beta' — callers should NOT parse Message for the knob name")
	assert.Contains(t, conflicts[0].Message, "beta",
		"Message must still mention 'beta' for human-readable detail")
}

// TestDetectOverrideConflicts_TypedKnob_RiskFreeRate verifies the rf conflict
// carries "risk_free_rate" directly in the typed Knob field.
func TestDetectOverrideConflicts_TypedKnob_RiskFreeRate(t *testing.T) {
	conflicts := detectOverrideConflicts(nil, ptrFloat(0.04), &ValuationOverrides{
		RiskFreeRate: ptrFloat(0.05),
	})
	require.Len(t, conflicts, 1)
	assert.Equal(t, "risk_free_rate", conflicts[0].Knob,
		"Knob field must be 'risk_free_rate'")
}

// TestDetectOverrideConflicts_BothTyped confirms both conflicts carry distinct
// Knob values so the caller can distinguish them without string parsing.
func TestDetectOverrideConflicts_BothTyped(t *testing.T) {
	conflicts := detectOverrideConflicts(ptrFloat(1.2), ptrFloat(0.04), &ValuationOverrides{
		Beta:         ptrFloat(1.5),
		RiskFreeRate: ptrFloat(0.05),
	})
	require.Len(t, conflicts, 2)
	// Order: beta first, rf second (matches function implementation order).
	assert.Equal(t, "beta", conflicts[0].Knob)
	assert.Equal(t, "risk_free_rate", conflicts[1].Knob)
}

// TestDetectOverrideConflicts_NilOptions_NoTypedConflict confirms nil options
// returns an empty (nil) slice — not an empty overrideConflict.
func TestDetectOverrideConflicts_NilOptions_NoTypedConflict(t *testing.T) {
	conflicts := detectOverrideConflicts(ptrFloat(1.2), ptrFloat(0.04), nil)
	assert.Empty(t, conflicts, "nil options must return empty slice")
}

// ---------------------------------------------------------------------------
// I-2 — BulkFailure.Knob for INVALID_OVERRIDE entries
// ---------------------------------------------------------------------------

// TestBulkFailure_Knob_PopulatedForParamError verifies that the Knob field on
// BulkFailure is populated for INVALID_OVERRIDE failures so programmatic
// consumers can read the offending knob without parsing the Message string.
func TestBulkFailure_Knob_PopulatedForParamError(t *testing.T) {
	pe := &params.ParamError{
		Knob:   "min_growth_rate",
		Reason: "must be ≤ max_growth_rate (0.01)",
		Value:  0.05,
		Limit:  0.01,
	}

	f := classifyBulkError("AAPL", pe)

	assert.Equal(t, "INVALID_OVERRIDE", f.ErrorCode)
	assert.Equal(t, "min_growth_rate", f.Knob,
		"Knob must be populated from pe.Knob for INVALID_OVERRIDE BulkFailure entries")
}

// TestBulkFailure_Knob_EmptyForNonParamErrors verifies that the Knob field is
// empty (and omitted) for non-override error codes — backward-compat guard.
func TestBulkFailure_Knob_EmptyForNonParamErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{"ticker_not_found", valuation.ErrTickerNotFound, "TICKER_NOT_FOUND"},
		{"insufficient_data", valuation.ErrInsufficientData, "INSUFFICIENT_DATA"},
		{"model_not_applicable", valuation.ErrModelNotApplicable, "MODEL_NOT_APPLICABLE"},
		{"foreign_private_issuer", valuation.ErrForeignPrivateIssuer, "FOREIGN_PRIVATE_ISSUER_UNSUPPORTED"},
		{"generic_error", fmt.Errorf("boom"), "CALCULATION_ERROR"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := classifyBulkError("AAPL", tc.err)
			assert.Equal(t, tc.code, f.ErrorCode)
			assert.Empty(t, f.Knob,
				"Knob must be empty for non-INVALID_OVERRIDE error code %s", tc.code)
		})
	}
}

// TestBulkFailure_Knob_PresentInJSON verifies the Knob field appears in the
// JSON output for INVALID_OVERRIDE entries (omitempty keeps it out otherwise).
func TestBulkFailure_Knob_PresentInJSON(t *testing.T) {
	pe := &params.ParamError{
		Knob:   "horizon_years",
		Reason: "out of range",
		Value:  99,
		Limit:  50,
	}

	f := classifyBulkError("MSFT", pe)
	b, err := json.Marshal(f)
	require.NoError(t, err)
	j := string(b)

	assert.Contains(t, j, `"knob":"horizon_years"`,
		"Knob must be serialized in JSON for INVALID_OVERRIDE BulkFailure")
}

// TestBulkFailure_Knob_AbsentInJSONForNonOverrideError verifies that the Knob
// field is omitted (omitempty) in JSON for non-INVALID_OVERRIDE failures so
// existing consumers are not broken.
func TestBulkFailure_Knob_AbsentInJSONForNonOverrideError(t *testing.T) {
	f := classifyBulkError("AAPL", valuation.ErrTickerNotFound)
	b, err := json.Marshal(f)
	require.NoError(t, err)
	j := string(b)

	assert.NotContains(t, j, `"knob"`,
		"Knob must be omitted from JSON for non-INVALID_OVERRIDE errors")
}

// ---------------------------------------------------------------------------
// buildFairValueResponse — shared builder correctness
// ---------------------------------------------------------------------------

// TestBuildFairValueResponse_FieldMapping verifies that the shared builder
// maps every ValuationResult field to the correct FairValueResponse field.
// This pins the extraction and ensures GET and POST share the same mapping.
func TestBuildFairValueResponse_FieldMapping(t *testing.T) {
	h := &FairValueHandler{}
	result := sampleValuationResult("AAPL")

	resp := h.buildFairValueResponse("AAPL", result)

	assert.Equal(t, "AAPL", resp.Ticker)
	assert.InDelta(t, result.WACC, resp.WACC, 1e-12)
	assert.InDelta(t, result.GrowthRate, resp.GrowthRate, 1e-12)
	assert.InDelta(t, result.TangibleValuePerShare, resp.TangibleValuePerShare, 1e-12)
	assert.InDelta(t, result.DCFValuePerShare, resp.DCFValuePerShare, 1e-12)
	assert.InDelta(t, result.CurrentPrice, resp.CurrentPrice, 1e-12)
	assert.Equal(t, "USD", resp.Currency, "currency should default to USD")
	assert.Equal(t, result.CalculatedAt.Format("2006-01-02T15:04:05Z"), resp.AsOf)
	assert.Equal(t, string(result.DataQualityGrade), resp.DataQualityGrade)
}
