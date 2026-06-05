package handlers

// fair_value_validation_test.go — Unit tests for T7 Layer-1 static validation.
//
// Coverage:
//   - validateOverrides: per-knob range table (valid edge, invalid low, invalid high)
//     including ALLOWED-NEGATIVE cases that MUST pass validation.
//   - terminal_method enum: both valid values, an invalid value.
//   - GrowthStages nested range checks.
//   - Nil input (must return nil — never reject the default path).
//   - Bulk HTTP integration: bulk request with out-of-range knob returns 422
//     INVALID_OVERRIDE before the service is called.
//   - Error shape: code, status, context.knob, detail format.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func assertOverrideValid(t *testing.T, o *ValuationOverrides, msg string) {
	t.Helper()
	result := validateOverrides(o)
	assert.Nil(t, result, "expected valid: %s", msg)
}

func assertOverrideInvalid(t *testing.T, o *ValuationOverrides, wantKnob string, msg string) {
	t.Helper()
	result := validateOverrides(o)
	require.NotNil(t, result, "expected invalid: %s", msg)
	assert.Equal(t, "INVALID_OVERRIDE", result.Code, "code must be INVALID_OVERRIDE")
	assert.Equal(t, 422, result.Status, "status must be 422")
	require.NotNil(t, result.Context, "context must be set")
	assert.Equal(t, wantKnob, result.Context["knob"],
		"context.knob must be %q", wantKnob)
	assert.Contains(t, result.Detail, wantKnob,
		"detail must name the knob")
}

// ---------------------------------------------------------------------------
// Nil / empty input
// ---------------------------------------------------------------------------

func TestValidateOverrides_Nil_IsValid(t *testing.T) {
	// A nil options struct must never be rejected — this is the default path.
	assertOverrideValid(t, nil, "nil ValuationOverrides")
}

func TestValidateOverrides_Empty_IsValid(t *testing.T) {
	// A non-nil but all-nil-field struct is the "empty options" case and must pass.
	assertOverrideValid(t, &ValuationOverrides{}, "empty ValuationOverrides")
}

// ---------------------------------------------------------------------------
// terminal_growth_rate — range [-0.20, 0.50]
// ---------------------------------------------------------------------------

func TestValidateOverrides_TerminalGrowthRate(t *testing.T) {
	tests := []struct {
		name    string
		value   float64
		wantErr bool
	}{
		// ── valid boundary and interior ────────────────────────────────────
		{"lower bound -0.20", -0.20, false},
		{"upper bound 0.50", 0.50, false},
		{"zero", 0.0, false},
		// ALLOWED NEGATIVE — real-terms secular decline is economically valid
		{"allowed negative -0.05", -0.05, false},
		{"allowed negative -0.10", -0.10, false},
		// ── out-of-range ──────────────────────────────────────────────────
		{"below lower -0.21", -0.21, true},
		{"above upper 0.51", 0.51, true},
		{"way below -1.0", -1.0, true},
		{"way above 1.0", 1.0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.value
			o := &ValuationOverrides{TerminalGrowthRate: &v}
			if tc.wantErr {
				assertOverrideInvalid(t, o, "terminal_growth_rate", tc.name)
			} else {
				assertOverrideValid(t, o, tc.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// terminal_growth_cap — same range as terminal_growth_rate [-0.20, 0.50]
// ---------------------------------------------------------------------------

func TestValidateOverrides_TerminalGrowthCap(t *testing.T) {
	tests := []struct {
		name    string
		value   float64
		wantErr bool
	}{
		{"lower bound -0.20", -0.20, false},
		{"upper bound 0.50", 0.50, false},
		{"typical 0.03", 0.03, false},
		// ALLOWED NEGATIVE
		{"allowed negative -0.01", -0.01, false},
		// invalid
		{"below lower -0.21", -0.21, true},
		{"above upper 0.51", 0.51, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.value
			o := &ValuationOverrides{TerminalGrowthCap: &v}
			if tc.wantErr {
				assertOverrideInvalid(t, o, "terminal_growth_cap", tc.name)
			} else {
				assertOverrideValid(t, o, tc.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// horizon_years — range [1, 50]
// ---------------------------------------------------------------------------

func TestValidateOverrides_HorizonYears(t *testing.T) {
	tests := []struct {
		name    string
		value   int
		wantErr bool
	}{
		{"lower bound 1", 1, false},
		{"upper bound 50", 50, false},
		{"typical 5", 5, false},
		{"typical 10", 10, false},
		// invalid
		{"zero 0", 0, true},
		{"negative -1", -1, true},
		{"above upper 51", 51, true},
		{"way above 100", 100, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.value
			o := &ValuationOverrides{HorizonYears: &v}
			if tc.wantErr {
				assertOverrideInvalid(t, o, "horizon_years", tc.name)
			} else {
				assertOverrideValid(t, o, tc.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// growth_stages stage years — each [0, 50]
// ---------------------------------------------------------------------------

func TestValidateOverrides_GrowthStages_Stage1Years(t *testing.T) {
	tests := []struct {
		name    string
		value   int
		wantErr bool
	}{
		{"lower bound 0 (stage can be zeroed)", 0, false},
		{"upper bound 50", 50, false},
		{"typical 3", 3, false},
		// invalid
		{"negative -1", -1, true},
		{"above upper 51", 51, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.value
			o := &ValuationOverrides{GrowthStages: &GrowthStages{Stage1Years: &v}}
			if tc.wantErr {
				assertOverrideInvalid(t, o, "growth_stages.stage1_years", tc.name)
			} else {
				assertOverrideValid(t, o, tc.name)
			}
		})
	}
}

func TestValidateOverrides_GrowthStages_Stage2Years(t *testing.T) {
	below := -1
	o := &ValuationOverrides{GrowthStages: &GrowthStages{Stage2Years: &below}}
	assertOverrideInvalid(t, o, "growth_stages.stage2_years", "stage2 below lower bound")
}

func TestValidateOverrides_GrowthStages_Stage3Years(t *testing.T) {
	above := 51
	o := &ValuationOverrides{GrowthStages: &GrowthStages{Stage3Years: &above}}
	assertOverrideInvalid(t, o, "growth_stages.stage3_years", "stage3 above upper bound")

	// Stage3 = 0 is valid (legacy "long-tail extension = disabled")
	zero := 0
	oZero := &ValuationOverrides{GrowthStages: &GrowthStages{Stage3Years: &zero}}
	assertOverrideValid(t, oZero, "stage3_years = 0 is valid")
}

// ---------------------------------------------------------------------------
// max_growth_rate — range [-1.0, 10.0]
// ---------------------------------------------------------------------------

func TestValidateOverrides_MaxGrowthRate(t *testing.T) {
	tests := []struct {
		name    string
		value   float64
		wantErr bool
	}{
		{"lower bound -1.0", -1.0, false},
		{"upper bound 10.0", 10.0, false},
		{"zero", 0.0, false},
		{"typical 0.5", 0.5, false},
		// ALLOWED NEGATIVE — "max" can be negative in a pure-contraction scenario
		{"allowed negative -0.5", -0.5, false},
		// invalid
		{"below lower -1.01", -1.01, true},
		{"above upper 10.01", 10.01, true},
		{"way above 50", 50.0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.value
			o := &ValuationOverrides{MaxGrowthRate: &v}
			if tc.wantErr {
				assertOverrideInvalid(t, o, "max_growth_rate", tc.name)
			} else {
				assertOverrideValid(t, o, tc.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// min_growth_rate — range [-1.0, 10.0]
// ---------------------------------------------------------------------------

func TestValidateOverrides_MinGrowthRate(t *testing.T) {
	tests := []struct {
		name    string
		value   float64
		wantErr bool
	}{
		{"lower bound -1.0", -1.0, false},
		{"upper bound 10.0", 10.0, false},
		// ALLOWED NEGATIVE — contraction scenarios
		{"allowed negative -0.30", -0.30, false},
		{"allowed negative -0.99", -0.99, false},
		// invalid
		{"below lower -1.01", -1.01, true},
		{"above upper 10.01", 10.01, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.value
			o := &ValuationOverrides{MinGrowthRate: &v}
			if tc.wantErr {
				assertOverrideInvalid(t, o, "min_growth_rate", tc.name)
			} else {
				assertOverrideValid(t, o, tc.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// terminal_multiple — range [0, 100]
// ---------------------------------------------------------------------------

func TestValidateOverrides_TerminalMultiple(t *testing.T) {
	tests := []struct {
		name    string
		value   float64
		wantErr bool
	}{
		{"lower bound 0", 0.0, false},
		{"upper bound 100", 100.0, false},
		{"typical 14", 14.0, false},
		// invalid — multiple cannot be negative
		{"negative -0.01", -0.01, true},
		{"above upper 100.01", 100.01, true},
		{"way above 200", 200.0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.value
			o := &ValuationOverrides{TerminalMultiple: &v}
			if tc.wantErr {
				assertOverrideInvalid(t, o, "terminal_multiple", tc.name)
			} else {
				assertOverrideValid(t, o, tc.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// tax_rate — range [-0.5, 1.0]
// ---------------------------------------------------------------------------

func TestValidateOverrides_TaxRate(t *testing.T) {
	tests := []struct {
		name    string
		value   float64
		wantErr bool
	}{
		{"lower bound -0.5", -0.5, false},
		{"upper bound 1.0", 1.0, false},
		{"zero", 0.0, false},
		{"typical 0.21", 0.21, false},
		// ALLOWED NEGATIVE — negative effective tax rate from NOLs / credits
		{"allowed negative -0.2", -0.2, false},
		{"allowed negative -0.01", -0.01, false},
		// invalid
		{"below lower -0.51", -0.51, true},
		{"above upper 1.01", 1.01, true},
		{"way above 2.0", 2.0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.value
			o := &ValuationOverrides{TaxRate: &v}
			if tc.wantErr {
				assertOverrideInvalid(t, o, "tax_rate", tc.name)
			} else {
				assertOverrideValid(t, o, tc.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// beta — range [-5, 5]
// ---------------------------------------------------------------------------

func TestValidateOverrides_Beta(t *testing.T) {
	tests := []struct {
		name    string
		value   float64
		wantErr bool
	}{
		{"lower bound -5.0", -5.0, false},
		{"upper bound 5.0", 5.0, false},
		{"zero", 0.0, false},
		{"typical 1.2", 1.2, false},
		// ALLOWED NEGATIVE — inverse-correlated assets (gold miners, inverse ETFs)
		{"allowed negative -1.0", -1.0, false},
		{"allowed negative -0.5", -0.5, false},
		// invalid
		{"below lower -5.01", -5.01, true},
		{"above upper 5.01", 5.01, true},
		{"way below -10", -10.0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.value
			o := &ValuationOverrides{Beta: &v}
			if tc.wantErr {
				assertOverrideInvalid(t, o, "beta", tc.name)
			} else {
				assertOverrideValid(t, o, tc.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// risk_free_rate — range [-0.05, 0.25]
// ---------------------------------------------------------------------------

func TestValidateOverrides_RiskFreeRate(t *testing.T) {
	tests := []struct {
		name    string
		value   float64
		wantErr bool
	}{
		{"lower bound -0.05", -0.05, false},
		{"upper bound 0.25", 0.25, false},
		{"zero", 0.0, false},
		{"typical 0.045", 0.045, false},
		// ALLOWED NEGATIVE — EUR/JPY/CHF negative-rate regimes were real
		{"allowed negative -0.01", -0.01, false},
		{"allowed negative -0.04", -0.04, false},
		// invalid
		{"below lower -0.06", -0.06, true},
		{"above upper 0.26", 0.26, true},
		{"way above 1.0 (unit error: 5% supplied as 5)", 1.0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.value
			o := &ValuationOverrides{RiskFreeRate: &v}
			if tc.wantErr {
				assertOverrideInvalid(t, o, "risk_free_rate", tc.name)
			} else {
				assertOverrideValid(t, o, tc.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// market_risk_premium — range [0, 0.30]
// NOTE: MRP is FLOORED at 0 — a negative ERP is nonsensical (§3/D6 of design)
// ---------------------------------------------------------------------------

func TestValidateOverrides_MarketRiskPremium(t *testing.T) {
	tests := []struct {
		name    string
		value   float64
		wantErr bool
	}{
		{"lower bound 0.0", 0.0, false},
		{"upper bound 0.30", 0.30, false},
		{"typical 0.055", 0.055, false},
		// invalid — negative MRP is NOT allowed (unlike other knobs)
		{"negative -0.01 INVALID (MRP floored at 0)", -0.01, true},
		{"negative -0.1 INVALID", -0.1, true},
		{"above upper 0.31", 0.31, true},
		{"way above 1.0 (unit error)", 1.0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.value
			o := &ValuationOverrides{MarketRiskPremium: &v}
			if tc.wantErr {
				assertOverrideInvalid(t, o, "market_risk_premium", tc.name)
			} else {
				assertOverrideValid(t, o, tc.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// terminal_method — enum {gordon_growth, exit_multiple}
// ---------------------------------------------------------------------------

func TestValidateOverrides_TerminalMethod_ValidValues(t *testing.T) {
	for _, method := range []string{"gordon_growth", "exit_multiple"} {
		m := method
		o := &ValuationOverrides{TerminalMethod: &m}
		assertOverrideValid(t, o, "terminal_method="+method)
	}
}

func TestValidateOverrides_TerminalMethod_InvalidValues(t *testing.T) {
	for _, bad := range []string{"dcf", "gordon", "exit", "EXIT_MULTIPLE", "", "Gordon_Growth"} {
		b := bad
		o := &ValuationOverrides{TerminalMethod: &b}
		assertOverrideInvalid(t, o, "terminal_method",
			"terminal_method="+b+" should be invalid")
	}
}

// ---------------------------------------------------------------------------
// Error shape: verify §4.3 fields on the returned *ErrorResponse
// ---------------------------------------------------------------------------

func TestValidateOverrides_ErrorShape(t *testing.T) {
	// Use beta out-of-range as a representative case.
	v := 99.0 // way above the [−5, 5] range
	o := &ValuationOverrides{Beta: &v}
	errResp := validateOverrides(o)

	require.NotNil(t, errResp)
	assert.Equal(t, "INVALID_OVERRIDE", errResp.Code)
	assert.Equal(t, 422, errResp.Status)
	assert.Equal(t, "https://problems.midas.dev/INVALID_OVERRIDE", errResp.Type)
	assert.Equal(t, "Invalid valuation override", errResp.Title)
	// detail must contain the knob name and the supplied value
	assert.Contains(t, errResp.Detail, "beta")
	assert.Contains(t, errResp.Detail, "99")
	// context.knob must be set
	require.NotNil(t, errResp.Context)
	assert.Equal(t, "beta", errResp.Context["knob"])
}

// ---------------------------------------------------------------------------
// Bulk HTTP integration — out-of-range knob returns 422 before valuation
// ---------------------------------------------------------------------------

// TestGetBulkFairValue_OutOfRangeKnob_Returns422BeforeValuation verifies that
// when a bulk request carries an out-of-range options.beta the handler returns
// 422 INVALID_OVERRIDE without calling the valuation service.
func TestGetBulkFairValue_OutOfRangeKnob_Returns422BeforeValuation(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())
	router := newBulkTestRouter(h)

	// Service must NOT be called — validation fires first.
	svc.AssertNotCalled(t, "CalculateValuation")

	body := map[string]interface{}{
		"tickers": []string{"AAPL"},
		"options": map[string]interface{}{
			"beta": 99.0, // out of range [-5, 5]
		},
	}
	bodyJSON, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/fair-value/bulk", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"out-of-range beta must return 422")

	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "INVALID_OVERRIDE", errResp.Code)
	assert.Equal(t, http.StatusUnprocessableEntity, errResp.Status)
	assert.Contains(t, errResp.Detail, "beta",
		"detail must name the violating knob")
	require.NotNil(t, errResp.Context)
	assert.Equal(t, "beta", errResp.Context["knob"])

	svc.AssertExpectations(t)
}

// TestGetBulkFairValue_OutOfRangeMRP_Returns422 verifies MRP-specific floor
// (MRP ≥ 0 is mandatory; −0.01 must be rejected even though other knobs
// accept negatives).
func TestGetBulkFairValue_OutOfRangeMRP_Returns422(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())
	router := newBulkTestRouter(h)
	svc.AssertNotCalled(t, "CalculateValuation")

	body := map[string]interface{}{
		"tickers": []string{"MSFT"},
		"options": map[string]interface{}{
			"market_risk_premium": -0.01, // negative MRP is not allowed
		},
	}
	bodyJSON, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/fair-value/bulk", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code)

	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "INVALID_OVERRIDE", errResp.Code)
	assert.Contains(t, errResp.Detail, "market_risk_premium")
	require.NotNil(t, errResp.Context)
	assert.Equal(t, "market_risk_premium", errResp.Context["knob"])

	svc.AssertExpectations(t)
}

// TestGetBulkFairValue_InvalidTerminalMethod_Returns422 verifies enum check
// via the HTTP path.
func TestGetBulkFairValue_InvalidTerminalMethod_Returns422(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())
	router := newBulkTestRouter(h)
	svc.AssertNotCalled(t, "CalculateValuation")

	body := map[string]interface{}{
		"tickers": []string{"AAPL"},
		"options": map[string]interface{}{
			"terminal_method": "dcf", // not in {gordon_growth, exit_multiple}
		},
	}
	bodyJSON, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/fair-value/bulk", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code)

	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "INVALID_OVERRIDE", errResp.Code)
	assert.Contains(t, errResp.Detail, "terminal_method")
	require.NotNil(t, errResp.Context)
	assert.Equal(t, "terminal_method", errResp.Context["knob"])

	svc.AssertExpectations(t)
}

// TestGetBulkFairValue_ValidAllowedNegative_Passes verifies that a legitimately
// negative knob (tax_rate = −0.20, allowed for NOLs) does NOT trigger 422.
func TestGetBulkFairValue_ValidAllowedNegative_Passes(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())
	router := newBulkTestRouter(h)

	svc.On("CalculateValuation",
		mock.Anything, "AAPL", mock.Anything,
	).Return(sampleValuationResult("AAPL"), nil)

	body := map[string]interface{}{
		"tickers": []string{"AAPL"},
		"options": map[string]interface{}{
			// All three are valid negatives per design §3/D6
			"tax_rate":             -0.20,
			"risk_free_rate":       -0.03,
			"terminal_growth_rate": -0.05,
		},
	}
	bodyJSON, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/fair-value/bulk", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code,
		"valid allowed-negative knobs must not be rejected")
	svc.AssertExpectations(t)
}

// TestGetBulkFairValue_OutOfRangeHorizonYears_Returns422 checks an int-range
// knob on the HTTP path.
func TestGetBulkFairValue_OutOfRangeHorizonYears_Returns422(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())
	router := newBulkTestRouter(h)
	svc.AssertNotCalled(t, "CalculateValuation")

	body := map[string]interface{}{
		"tickers": []string{"AAPL"},
		"options": map[string]interface{}{
			"horizon_years": 0, // below minimum of 1
		},
	}
	bodyJSON, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/fair-value/bulk", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code)

	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "INVALID_OVERRIDE", errResp.Code)
	assert.Contains(t, errResp.Detail, "horizon_years")
	require.NotNil(t, errResp.Context)
	assert.Equal(t, "horizon_years", errResp.Context["knob"])

	svc.AssertExpectations(t)
}

// TestGetBulkFairValue_ValidationBeforeConflict_Ordering confirms that
// validateOverrides fires AFTER detectOverrideConflicts in the handler
// (conflict wins) — a conflicting beta + out-of-range beta returns
// INVALID_OVERRIDE for the conflict, not the range violation.
func TestGetBulkFairValue_ConflictTakesPriorityOverRange(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())
	router := newBulkTestRouter(h)
	svc.AssertNotCalled(t, "CalculateValuation")

	body := map[string]interface{}{
		"tickers":       []string{"AAPL"},
		"override_beta": 1.2, // legacy field
		"options": map[string]interface{}{
			"beta": 99.0, // both conflict AND out-of-range
		},
	}
	bodyJSON, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/fair-value/bulk", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code)

	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	// The conflict check runs first, so we get INVALID_OVERRIDE for "beta"
	// with the conflict detail (not the range detail).
	assert.Equal(t, "INVALID_OVERRIDE", errResp.Code)
	assert.Contains(t, errResp.Detail, "beta")
	// Must mention BOTH source fields (conflict message), not a range bound.
	assert.NotContains(t, errResp.Detail, "out of range",
		"conflict takes priority over range validation")

	svc.AssertExpectations(t)
}
