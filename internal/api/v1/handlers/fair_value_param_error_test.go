package handlers

// fair_value_param_error_test.go — T8 tests for Layer-2 params.ParamError
// classification at the fair-value handler call sites.
//
// Coverage:
//   - paramErrorResponse: returns (nil, false) for non-ParamError, builds
//     correct 422 body (including knob in context) for *params.ParamError
//     wrapped with fmt.Errorf("%w", ...) as it arrives from CalculateValuation.
//   - classifyBulkError with ParamError: returns BulkFailure{ErrorCode:"INVALID_OVERRIDE"}
//     carrying the ParamError message.
//   - GetBulkFairValue integration: min_growth_rate > max_growth_rate passes Layer-1
//     static ranges but fails the Layer-2 resolver; the violating ticker gets
//     BulkFailure{ErrorCode:"INVALID_OVERRIDE"} and the other ticker still succeeds
//     (failure isolation).
//   - GetFairValue single-ticker: a *params.ParamError from the service returns 422
//     INVALID_OVERRIDE with the knob in the context.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
// paramErrorResponse unit tests
// ---------------------------------------------------------------------------

// TestParamErrorResponse_NonParamError returns (nil, false) for an error that is
// not (and does not wrap) a *params.ParamError.
func TestParamErrorResponse_NonParamError(t *testing.T) {
	resp, ok := paramErrorResponse(fmt.Errorf("some other error"))
	assert.False(t, ok, "non-ParamError should return ok=false")
	assert.Nil(t, resp, "non-ParamError should return nil response")
}

// TestParamErrorResponse_DirectParamError handles a bare *params.ParamError.
func TestParamErrorResponse_DirectParamError(t *testing.T) {
	pe := &params.ParamError{
		Knob:   "terminal_growth_rate",
		Reason: "must be strictly less than WACC (0.094)",
		Value:  0.095,
		Limit:  0.094,
	}

	resp, ok := paramErrorResponse(pe)
	require.True(t, ok, "ParamError should return ok=true")
	require.NotNil(t, resp)

	assert.Equal(t, http.StatusUnprocessableEntity, resp.Status)
	assert.Equal(t, "INVALID_OVERRIDE", resp.Code)
	// MEDIUM-3: detail must carry the FULL ParamError message (value + limit), not
	// just the bare Reason — matching the bulk path's pe.Error() and keeping single
	// and bulk consistent.
	assert.Equal(t, pe.Error(), resp.Detail, "detail must be the full ParamError.Error()")
	// context must carry the knob name plus the offending value and limit.
	require.NotNil(t, resp.Context)
	assert.Equal(t, "terminal_growth_rate", resp.Context["knob"])
	assert.Equal(t, 0.095, resp.Context["value"])
	assert.Equal(t, 0.094, resp.Context["limit"])
}

// TestParamErrorResponse_WrappedParamError verifies that errors.As traverses the
// %w chain — matching how CalculateValuation wraps the performValuation error
// (fmt.Errorf("failed to perform valuation: %w", err)).
func TestParamErrorResponse_WrappedParamError(t *testing.T) {
	pe := &params.ParamError{
		Knob:   "min_growth_rate",
		Reason: "must be ≤ max_growth_rate (0.01)",
		Value:  0.05,
		Limit:  0.01,
	}
	// Simulate the double-wrap that CalculateValuation applies:
	// performValuation returns the ParamError directly, then CalculateValuation wraps it.
	wrapped := fmt.Errorf("failed to perform valuation: %w", pe)

	resp, ok := paramErrorResponse(wrapped)
	require.True(t, ok, "wrapped ParamError should be detected via errors.As chain")
	require.NotNil(t, resp)

	assert.Equal(t, "INVALID_OVERRIDE", resp.Code)
	assert.Equal(t, "min_growth_rate", resp.Context["knob"])
}

// TestParamErrorResponse_MinGrowthGreaterThanMax covers the specific Layer-2
// invariant triggered by min_growth_rate: 5.0 + max_growth_rate: 1.0 (both
// within Layer-1 static [-1,10] but min>max violates Layer-2).
func TestParamErrorResponse_MinGrowthGreaterThanMax(t *testing.T) {
	pe := &params.ParamError{
		Knob:   "min_growth_rate",
		Reason: "must be ≤ max_growth_rate (0.01)",
		Value:  0.05,
		Limit:  0.01,
	}

	resp, ok := paramErrorResponse(pe)
	require.True(t, ok)
	assert.Equal(t, "INVALID_OVERRIDE", resp.Code)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.Status)
	assert.Equal(t, "min_growth_rate", resp.Context["knob"])
}

// ---------------------------------------------------------------------------
// classifyBulkError with ParamError
// ---------------------------------------------------------------------------

// TestClassifyBulkError_ParamError maps a *params.ParamError to INVALID_OVERRIDE.
func TestClassifyBulkError_ParamError(t *testing.T) {
	pe := &params.ParamError{
		Knob:   "horizon_years",
		Reason: "must be ≤ stage1_years + stage2_years + stage3_years",
		Value:  20,
		Limit:  7,
	}

	f := classifyBulkError("AAPL", pe)

	assert.Equal(t, "AAPL", f.Ticker)
	assert.Equal(t, "INVALID_OVERRIDE", f.ErrorCode)
	// The message must carry the error text so the consumer can diagnose.
	assert.NotEmpty(t, f.Message)
	assert.Contains(t, f.Message, "horizon_years",
		"message should contain the offending knob name")
}

// TestClassifyBulkError_WrappedParamError propagates through an error chain.
func TestClassifyBulkError_WrappedParamError(t *testing.T) {
	pe := &params.ParamError{
		Knob:   "terminal_growth_rate",
		Reason: "must be strictly less than WACC (0.094)",
		Value:  0.095,
		Limit:  0.094,
	}
	wrapped := fmt.Errorf("failed to perform valuation: %w", pe)

	f := classifyBulkError("MSFT", wrapped)

	assert.Equal(t, "MSFT", f.Ticker)
	assert.Equal(t, "INVALID_OVERRIDE", f.ErrorCode)
	assert.Contains(t, f.Message, "terminal_growth_rate")
}

// TestClassifyBulkError_OtherErrors ensures non-ParamError errors still map
// to their existing codes (regression guard — T8 must not change non-ParamError
// error handling).
func TestClassifyBulkError_OtherErrors_Unchanged(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode string
	}{
		{"ticker_not_found", valuation.ErrTickerNotFound, "TICKER_NOT_FOUND"},
		{"insufficient_data", valuation.ErrInsufficientData, "INSUFFICIENT_DATA"},
		{"model_not_applicable", valuation.ErrModelNotApplicable, "MODEL_NOT_APPLICABLE"},
		{"foreign_private_issuer", valuation.ErrForeignPrivateIssuer, "FOREIGN_PRIVATE_ISSUER_UNSUPPORTED"},
		{"generic_error", fmt.Errorf("some internal error"), "CALCULATION_ERROR"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := classifyBulkError("AAPL", tc.err)
			assert.Equal(t, tc.wantCode, f.ErrorCode,
				"error code for %s must be %s", tc.name, tc.wantCode)
		})
	}
}

// ---------------------------------------------------------------------------
// GetBulkFairValue integration: failure isolation
// ---------------------------------------------------------------------------

// newGetFairValueTestRouter creates a minimal Gin router for GET /fair-value/:ticker.
func newGetFairValueTestRouter(h *FairValueHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/fair-value/:ticker", h.GetFairValue)
	return r
}

// TestGetBulkFairValue_ParamError_FailureIsolation verifies that when a per-ticker
// valuation returns a *params.ParamError (Layer-2 invariant violation, passes
// Layer-1 static ranges), only that ticker gets BulkFailure{ErrorCode:
// "INVALID_OVERRIDE"} while other tickers in the same batch still succeed.
//
// The scenario simulates min_growth_rate: 5.0 + max_growth_rate: 1.0 — both
// within the static Layer-1 range [-1, 10] but min>max violates Layer-2.
// In production the resolver fires inside CalculateValuation and wraps the error
// with fmt.Errorf("failed to perform valuation: %w", pe). We replicate that here
// so the test exercises the full classification path.
func TestGetBulkFairValue_ParamError_FailureIsolation(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())
	router := newBulkTestRouter(h)

	// AAPL succeeds normally.
	svc.On("CalculateValuation", mock.Anything, "AAPL", mock.Anything).
		Return(sampleValuationResult("AAPL"), nil)

	// MSFT fails with a Layer-2 ParamError (min > max), wrapped as CalculateValuation
	// would wrap it after performValuation returns it directly.
	pe := &params.ParamError{
		Knob:   "min_growth_rate",
		Reason: "must be ≤ max_growth_rate (0.01)",
		Value:  0.05,
		Limit:  0.01,
	}
	wrappedParamErr := fmt.Errorf("failed to perform valuation: %w", pe)
	svc.On("CalculateValuation", mock.Anything, "MSFT", mock.Anything).
		Return(nil, wrappedParamErr)

	body := map[string]interface{}{
		"tickers": []string{"AAPL", "MSFT"},
		// Both min/max are within Layer-1 static ranges [-1, 10], so validateOverrides
		// passes; the violation is caught inside the resolver (Layer-2) during the MSFT call.
		"options": map[string]interface{}{
			"min_growth_rate": 0.05,
			"max_growth_rate": 0.01,
		},
	}
	bodyJSON, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/fair-value/bulk", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// One ticker succeeded, one failed → 207 Multi-Status.
	require.Equal(t, http.StatusMultiStatus, w.Code,
		"partial success must return 207 Multi-Status")

	var resp BulkFairValueResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// Summary counts.
	assert.Equal(t, 2, resp.Summary.TotalRequested)
	assert.Equal(t, 1, resp.Summary.Successful,
		"AAPL should have succeeded")
	assert.Equal(t, 1, resp.Summary.Failed,
		"MSFT should have failed with INVALID_OVERRIDE")

	// The successful result must be AAPL.
	require.Len(t, resp.Results, 1)
	assert.Equal(t, "AAPL", resp.Results[0].Ticker)

	// The failure must identify MSFT with INVALID_OVERRIDE and knob context.
	require.Len(t, resp.Failures, 1)
	f := resp.Failures[0]
	assert.Equal(t, "MSFT", f.Ticker)
	assert.Equal(t, "INVALID_OVERRIDE", f.ErrorCode,
		"Layer-2 ParamError must map to INVALID_OVERRIDE, not CALCULATION_ERROR")
	assert.NotEmpty(t, f.Message,
		"failure message must describe the violation")

	svc.AssertExpectations(t)
}

// TestGetBulkFairValue_AllParamErrors_Returns422 verifies that when ALL tickers
// fail with a ParamError the response is 422 (all-fail path) — matching the
// existing all-fail behaviour for other error types.
func TestGetBulkFairValue_AllParamErrors_Returns422(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())
	router := newBulkTestRouter(h)

	pe := &params.ParamError{
		Knob:   "terminal_growth_rate",
		Reason: "must be strictly less than WACC (0.094)",
		Value:  0.095,
		Limit:  0.094,
	}
	wrapped := fmt.Errorf("failed to perform valuation: %w", pe)

	svc.On("CalculateValuation", mock.Anything, "AAPL", mock.Anything).
		Return(nil, wrapped)
	svc.On("CalculateValuation", mock.Anything, "MSFT", mock.Anything).
		Return(nil, wrapped)

	body := map[string]interface{}{
		"tickers": []string{"AAPL", "MSFT"},
		"options": map[string]interface{}{
			// terminal_growth_rate=0.095 is within Layer-1 static range,
			// but will fail Layer-2 WACC invariant inside the service.
			"terminal_growth_rate": 0.095,
		},
	}
	bodyJSON, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/fair-value/bulk", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// All failed → 422.
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"all-fail bulk response must return 422")

	var resp BulkFairValueResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	assert.Equal(t, 0, resp.Summary.Successful)
	assert.Equal(t, 2, resp.Summary.Failed)

	for _, f := range resp.Failures {
		assert.Equal(t, "INVALID_OVERRIDE", f.ErrorCode,
			"all failures should be INVALID_OVERRIDE")
	}
}

// ---------------------------------------------------------------------------
// GetFairValue single-ticker: ParamError → 422
// ---------------------------------------------------------------------------

// TestGetFairValue_ParamError_Returns422 verifies that when CalculateValuation
// returns a *params.ParamError (wrapped as it would be from performValuation),
// the GET handler returns 422 INVALID_OVERRIDE with the correct knob in context.
func TestGetFairValue_ParamError_Returns422(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())
	router := newGetFairValueTestRouter(h)

	pe := &params.ParamError{
		Knob:   "terminal_growth_rate",
		Reason: "must be strictly less than WACC (0.094)",
		Value:  0.095,
		Limit:  0.094,
	}
	// Mirror the wrap CalculateValuation applies.
	wrapped := fmt.Errorf("failed to perform valuation: %w", pe)

	svc.On("CalculateValuation", mock.Anything, "AAPL", mock.Anything).
		Return(nil, wrapped)

	req := httptest.NewRequest(http.MethodGet, "/fair-value/AAPL", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"ParamError from service must return 422")

	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))

	assert.Equal(t, "INVALID_OVERRIDE", errResp.Code,
		"error code must be INVALID_OVERRIDE for a ParamError")
	assert.Equal(t, http.StatusUnprocessableEntity, errResp.Status)
	// MEDIUM-3: detail must carry the FULL ParamError message (value + limit),
	// consistent with the bulk path.
	assert.Equal(t, pe.Error(), errResp.Detail,
		"detail must be the full ParamError.Error()")
	// Context must name the offending knob and carry the value/limit.
	require.NotNil(t, errResp.Context)
	assert.Equal(t, "terminal_growth_rate", errResp.Context["knob"],
		"context.knob must be the offending knob name")
	assert.Equal(t, 0.095, errResp.Context["value"])
	assert.Equal(t, 0.094, errResp.Context["limit"])

	svc.AssertExpectations(t)
}

// TestGetFairValue_NonParamError_StillMapsCorrectly ensures the existing
// error-classification logic is NOT disturbed by T8 (regression guard).
func TestGetFairValue_NonParamError_StillMapsCorrectly(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"ticker_not_found", valuation.ErrTickerNotFound, http.StatusNotFound, "TICKER_NOT_FOUND"},
		{"insufficient_data", fmt.Errorf("wrap: %w", valuation.ErrInsufficientData), http.StatusUnprocessableEntity, "INSUFFICIENT_DATA"},
		{"foreign_private_issuer", valuation.ErrForeignPrivateIssuer, http.StatusUnprocessableEntity, "FOREIGN_PRIVATE_ISSUER_UNSUPPORTED"},
		{"model_not_applicable", valuation.ErrModelNotApplicable, http.StatusUnprocessableEntity, "MODEL_NOT_APPLICABLE"},
		{"generic_error", fmt.Errorf("boom"), http.StatusInternalServerError, "CALCULATION_ERROR"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &mockValuationService{}
			h := NewFairValueHandler(svc, zap.NewNop())
			router := newGetFairValueTestRouter(h)

			svc.On("CalculateValuation", mock.Anything, "AAPL", mock.Anything).
				Return(nil, tc.err)

			req := httptest.NewRequest(http.MethodGet, "/fair-value/AAPL", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, tc.wantStatus, w.Code, "status for %s", tc.name)

			var errResp ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
			assert.Equal(t, tc.wantCode, errResp.Code, "code for %s", tc.name)
		})
	}
}
