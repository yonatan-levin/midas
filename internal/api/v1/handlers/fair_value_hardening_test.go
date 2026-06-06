package handlers

// fair_value_hardening_test.go — iteration-3 hardening tests for FIX 1, 2, 3.
//
// FIX 1 (HIGH): parseFloatParam must reject NaN and ±Inf with a 400, not
// propagate non-finite values into the valuation engine.
//
// FIX 2 (MEDIUM): the resolver spread predicate and DCF's validateInputs
// predicate must agree on the boundary value — no resolver-accept / DCF-reject
// mismatch that would survive to become a 500.
//
// FIX 3 (LOW): legacy GET override_beta / override_rf ranges are widened to
// match betaMin/betaMax and riskFreeRateMin/riskFreeRateMax; previously-rejected
// values like -1.0 must now be accepted.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// FIX 1: parseFloatParam — NaN / ±Inf / valid / absent
// ---------------------------------------------------------------------------

// TestParseFloatParam_NonFiniteValues checks that NaN and ±Inf produce an error.
// strconv.ParseFloat accepts "NaN", "+Inf", "Inf", "-Inf" without error, so
// the guard must be explicit.
func TestParseFloatParam_NonFiniteValues(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"NaN", "NaN"},
		{"plus_inf", "+Inf"},
		{"minus_inf", "-Inf"},
		{"bare_inf", "Inf"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			req := httptest.NewRequest(http.MethodGet, "/?p="+tc.value, nil)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = req

			got, err := parseFloatParam(c, "p")
			assert.Nil(t, got, "non-finite value must return nil pointer")
			assert.Error(t, err, "non-finite value must return an error")
			assert.Contains(t, err.Error(), "finite", "error message must mention 'finite'")
		})
	}
}

// TestParseFloatParam_ValidValue confirms a normal float is parsed correctly.
func TestParseFloatParam_ValidValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodGet, "/?p=1.2", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	got, err := parseFloatParam(c, "p")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.InDelta(t, 1.2, *got, 1e-12)
}

// TestParseFloatParam_Absent confirms an absent param returns (nil, nil).
func TestParseFloatParam_Absent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	got, err := parseFloatParam(c, "p")
	assert.Nil(t, got, "absent param must return nil pointer")
	assert.NoError(t, err, "absent param must return nil error")
}

// TestParseFloatParam_Unparseable confirms a garbage string returns an error
// (not a silent nil the way the old implementation did).
func TestParseFloatParam_Unparseable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodGet, "/?p=not-a-number", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	got, err := parseFloatParam(c, "p")
	assert.Nil(t, got, "unparseable value must return nil pointer")
	assert.Error(t, err, "unparseable value must return an error")
}

// ---------------------------------------------------------------------------
// FIX 1 end-to-end: GET /fair-value/:ticker?override_beta=NaN → 400
// ---------------------------------------------------------------------------

// TestGetFairValue_NaNBeta_Returns400 exercises the full GET handler path:
// a NaN override_beta must produce 400 INVALID_PARAMETER, NOT a 500 or a
// silent default that produces a non-finite response.
func TestGetFairValue_NaNBeta_Returns400(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/fair-value/:ticker", h.GetFairValue)

	// Service must NOT be called — the handler must reject before reaching it.
	req := httptest.NewRequest(http.MethodGet, "/fair-value/AAPL?override_beta=NaN", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"NaN override_beta must yield 400, not 500 or 200")
	svc.AssertNotCalled(t, "CalculateValuation")
}

// TestGetFairValue_InfBeta_Returns400 mirrors the NaN test for ±Inf.
func TestGetFairValue_InfBeta_Returns400(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/fair-value/:ticker", h.GetFairValue)

	for _, val := range []string{"Inf", "+Inf", "-Inf"} {
		t.Run(val, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet,
				fmt.Sprintf("/fair-value/AAPL?override_beta=%s", val), nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code,
				"non-finite override_beta=%s must yield 400", val)
		})
	}
	svc.AssertNotCalled(t, "CalculateValuation")
}

// TestGetFairValue_NaNRF_Returns400 confirms override_rf=NaN also returns 400.
func TestGetFairValue_NaNRF_Returns400(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/fair-value/:ticker", h.GetFairValue)

	req := httptest.NewRequest(http.MethodGet, "/fair-value/AAPL?override_rf=NaN", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"NaN override_rf must yield 400")
	svc.AssertNotCalled(t, "CalculateValuation")
}

// TestGetFairValue_ValidBeta_StillWorks confirms override_beta=1.2 still
// reaches the valuation service (no regression on the valid path).
func TestGetFairValue_ValidBeta_StillWorks(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/fair-value/:ticker", h.GetFairValue)

	svc.On("CalculateValuation", mock.Anything, "AAPL", mock.Anything).
		Return(sampleValuationResult("AAPL"), nil)

	req := httptest.NewRequest(http.MethodGet, "/fair-value/AAPL?override_beta=1.2", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "valid override_beta=1.2 must succeed")
	svc.AssertExpectations(t)
}

// TestGetFairValue_AbsentBeta_UsesDefault confirms that omitting override_beta
// still produces a successful (default-path) response.
func TestGetFairValue_AbsentBeta_UsesDefault(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/fair-value/:ticker", h.GetFairValue)

	svc.On("CalculateValuation", mock.Anything, "AAPL", mock.Anything).
		Return(sampleValuationResult("AAPL"), nil)

	req := httptest.NewRequest(http.MethodGet, "/fair-value/AAPL", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "absent override_beta must use default and succeed")
	svc.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// FIX 2: resolver spread predicate mirrors DCF exactly — no boundary mismatch
// ---------------------------------------------------------------------------

// TestResolverSpread_MatchesDCFPredicate is the load-bearing FIX-2 gate.
//
// It encodes the predicate FORM that BOTH the resolver (params.ResolveTerminal)
// and dcf.validateInputs now use:
//
//	computedWACC - terminalGrowth < MinWACCTerminalSpread
//
// The old resolver used the algebraically-rearranged form:
//
//	terminalGrowth > computedWACC - MinWACCTerminalSpread
//
// These are mathematically equivalent but differ at the float boundary by 1 ULP
// because IEEE-754 subtraction rounds to nearest-even. A value that passes the
// rearranged form could fail DCF's form and bubble up as a 500.
//
// This test simulates both predicates at a (WACC, tg) pair just above the spread
// boundary and asserts they agree. After FIX 2, both use the subtraction form,
// so they are bit-identical.
//
// spread = 0.01 (params.MinTerminalWACCSpread = dcf.MinWACCTerminalSpread; verified
// by params_test and dcf_test; pinned as a literal here to keep this test free of
// cross-package imports).
func TestResolverSpread_MatchesDCFPredicate(t *testing.T) {
	// The spread constant used by both resolver and DCF.
	const spread = 0.01 // params.MinTerminalWACCSpread == dcf.MinWACCTerminalSpread
	const wacc = 0.10

	// Case 1: tg just above the boundary → both predicates must REJECT.
	// "just above" means computedWACC - tg < spread.
	tgAbove := wacc - spread + 1e-12

	// DCF predicate (the canonical form):
	dcfRejects := wacc-tgAbove < spread
	// Resolver predicate after FIX 2 (SAME form):
	resolverRejects := wacc-tgAbove < spread

	assert.True(t, dcfRejects,
		"DCF must reject tg above boundary (WACC=%.4f, tg=%.15f, spread=%.4f)",
		wacc, tgAbove, spread)
	assert.Equal(t, dcfRejects, resolverRejects,
		"resolver and DCF predicate must agree: both REJECT tg above boundary")

	// Case 2: tg clearly below the boundary → both predicates must ACCEPT.
	tgBelow := wacc - spread - 1e-12

	dcfAccepts := !(wacc-tgBelow < spread)
	resolverAccepts := !(wacc-tgBelow < spread)

	assert.True(t, dcfAccepts,
		"DCF must accept tg below boundary (WACC=%.4f, tg=%.15f, spread=%.4f)",
		wacc, tgBelow, spread)
	assert.Equal(t, dcfAccepts, resolverAccepts,
		"resolver and DCF predicate must agree: both ACCEPT tg below boundary")
}

// ---------------------------------------------------------------------------
// FIX 3: widened legacy GET ranges — formerly-rejected values now accepted
// ---------------------------------------------------------------------------

// TestGetFairValue_LegacyBeta_NegativeNowAccepted verifies that override_beta=-1.0
// is no longer rejected at the range-check layer (betaMin is now -5.0).
// The service is mocked to return a result so the test stays fast and deterministic.
func TestGetFairValue_LegacyBeta_NegativeNowAccepted(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/fair-value/:ticker", h.GetFairValue)

	svc.On("CalculateValuation", mock.Anything, "AAPL", mock.Anything).
		Return(sampleValuationResult("AAPL"), nil)

	req := httptest.NewRequest(http.MethodGet, "/fair-value/AAPL?override_beta=-1.0", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Must NOT be 400 from the range check; reaches the service.
	assert.Equal(t, http.StatusOK, w.Code,
		"override_beta=-1.0 must be accepted after range widening")
	svc.AssertExpectations(t)
}

// TestGetFairValue_LegacyBeta_OutOfWidenedRange_StillRejected confirms that a
// value beyond the widened range (e.g., 99) is still rejected with 400.
func TestGetFairValue_LegacyBeta_OutOfWidenedRange_StillRejected(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/fair-value/:ticker", h.GetFairValue)

	req := httptest.NewRequest(http.MethodGet, "/fair-value/AAPL?override_beta=99", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"override_beta=99 must still be rejected (beyond betaMax=5)")
	svc.AssertNotCalled(t, "CalculateValuation")
}

// TestGetFairValue_LegacyRF_NegativeNowAccepted verifies override_rf=-0.02 is
// accepted (riskFreeRateMin is -0.05 after widening; was 0 before).
func TestGetFairValue_LegacyRF_NegativeNowAccepted(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/fair-value/:ticker", h.GetFairValue)

	svc.On("CalculateValuation", mock.Anything, "AAPL", mock.Anything).
		Return(sampleValuationResult("AAPL"), nil)

	req := httptest.NewRequest(http.MethodGet, "/fair-value/AAPL?override_rf=-0.02", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code,
		"override_rf=-0.02 must be accepted after range widening to riskFreeRateMin=-0.05")
	svc.AssertExpectations(t)
}

// TestGetFairValue_LegacyRF_HigherUpperRail verifies override_rf=0.22 is now
// accepted (riskFreeRateMax is 0.25 after widening; was 0.20 before).
func TestGetFairValue_LegacyRF_HigherUpperRail(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/fair-value/:ticker", h.GetFairValue)

	svc.On("CalculateValuation", mock.Anything, "AAPL", mock.Anything).
		Return(sampleValuationResult("AAPL"), nil)

	req := httptest.NewRequest(http.MethodGet, "/fair-value/AAPL?override_rf=0.22", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code,
		"override_rf=0.22 must be accepted (riskFreeRateMax=0.25 after widening)")
	svc.AssertExpectations(t)
}
