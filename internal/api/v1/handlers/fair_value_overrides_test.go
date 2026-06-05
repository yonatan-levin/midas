package handlers

// T6 unit tests for the transport DTO projection and conflict-detection helpers
// introduced in fair_value.go (T6: ValuationOverrides DTO + projectOverrides +
// detectOverrideConflicts + bulk-path wiring).
//
// Test coverage:
//   - projectOverrides: nil-safe, maps all fields 1:1, flattens GrowthStages.
//   - anyBulkOverride: true/false for every override source.
//   - detectOverrideConflicts: beta/rf conflict, non-conflict, nil options.
//   - GetBulkFairValue with options: reaches service with populated Overrides.
//   - GetBulkFairValue conflict returns 422 INVALID_OVERRIDE.

import (
	"bytes"
	"encoding/json"
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

// ptr helpers — used to keep table-driven tests concise.
func ptrFloat(v float64) *float64 { return &v }
func ptrInt(v int) *int           { return &v }
func ptrStr(v string) *string     { return &v }

// ---------------------------------------------------------------------------
// projectOverrides
// ---------------------------------------------------------------------------

func TestProjectOverrides_NilInput(t *testing.T) {
	// A nil DTO must return a zero Overrides (all pointers nil) so the caller
	// does not accidentally override anything.
	got := projectOverrides(nil)
	assert.Equal(t, params.Overrides{}, got,
		"nil DTO must project to zero Overrides")
}

func TestProjectOverrides_EmptyInput(t *testing.T) {
	// A non-nil but all-zero DTO also maps to a zero Overrides.
	got := projectOverrides(&ValuationOverrides{})
	assert.Equal(t, params.Overrides{}, got,
		"empty DTO must project to zero Overrides")
}

func TestProjectOverrides_AllScalarFields(t *testing.T) {
	// Every non-nested pointer field must be projected 1:1.
	dto := &ValuationOverrides{
		TerminalGrowthRate: ptrFloat(-0.01),
		TerminalGrowthCap:  ptrFloat(0.03),
		HorizonYears:       ptrInt(5),
		MaxGrowthRate:      ptrFloat(0.5),
		MinGrowthRate:      ptrFloat(-0.3),
		TerminalMethod:     ptrStr("exit_multiple"),
		TerminalMultiple:   ptrFloat(14.0),
		TaxRate:            ptrFloat(0.21),
		Beta:               ptrFloat(1.2),
		RiskFreeRate:       ptrFloat(0.045),
		MarketRiskPremium:  ptrFloat(0.05),
	}

	got := projectOverrides(dto)

	assert.Equal(t, dto.TerminalGrowthRate, got.TerminalGrowthRate, "TerminalGrowthRate")
	assert.Equal(t, dto.TerminalGrowthCap, got.TerminalGrowthCap, "TerminalGrowthCap")
	assert.Equal(t, dto.HorizonYears, got.HorizonYears, "HorizonYears")
	assert.Equal(t, dto.MaxGrowthRate, got.MaxGrowthRate, "MaxGrowthRate")
	assert.Equal(t, dto.MinGrowthRate, got.MinGrowthRate, "MinGrowthRate")
	assert.Equal(t, dto.TerminalMethod, got.TerminalMethod, "TerminalMethod")
	assert.Equal(t, dto.TerminalMultiple, got.TerminalMultiple, "TerminalMultiple")
	assert.Equal(t, dto.TaxRate, got.TaxRate, "TaxRate")
	assert.Equal(t, dto.Beta, got.Beta, "Beta")
	assert.Equal(t, dto.RiskFreeRate, got.RiskFreeRate, "RiskFreeRate")
	assert.Equal(t, dto.MarketRiskPremium, got.MarketRiskPremium, "MarketRiskPremium")

	// GrowthStages not set → stage fields must remain nil
	assert.Nil(t, got.Stage1Years, "Stage1Years should be nil when GrowthStages absent")
	assert.Nil(t, got.Stage2Years, "Stage2Years should be nil when GrowthStages absent")
	assert.Nil(t, got.Stage3Years, "Stage3Years should be nil when GrowthStages absent")
}

func TestProjectOverrides_GrowthStages_Flattened(t *testing.T) {
	// GrowthStages sub-struct must be flattened into flat Overrides fields.
	dto := &ValuationOverrides{
		GrowthStages: &GrowthStages{
			Stage1Years: ptrInt(3),
			Stage2Years: ptrInt(4),
			Stage3Years: ptrInt(0),
		},
	}

	got := projectOverrides(dto)

	require.NotNil(t, got.Stage1Years, "Stage1Years must not be nil")
	require.NotNil(t, got.Stage2Years, "Stage2Years must not be nil")
	require.NotNil(t, got.Stage3Years, "Stage3Years must not be nil")
	assert.Equal(t, 3, *got.Stage1Years, "Stage1Years value")
	assert.Equal(t, 4, *got.Stage2Years, "Stage2Years value")
	assert.Equal(t, 0, *got.Stage3Years, "Stage3Years value")

	// Scalar fields unset → all nil in Overrides
	assert.Nil(t, got.Beta, "Beta must be nil when not set")
}

func TestProjectOverrides_GrowthStages_PartiallySet(t *testing.T) {
	// Only Stage1Years set; Stage2Years and Stage3Years should remain nil.
	dto := &ValuationOverrides{
		GrowthStages: &GrowthStages{
			Stage1Years: ptrInt(2),
		},
	}

	got := projectOverrides(dto)

	require.NotNil(t, got.Stage1Years)
	assert.Equal(t, 2, *got.Stage1Years)
	assert.Nil(t, got.Stage2Years, "Stage2Years must remain nil")
	assert.Nil(t, got.Stage3Years, "Stage3Years must remain nil")
}

func TestProjectOverrides_GrowthStages_NilSubStruct(t *testing.T) {
	// GrowthStages present but all nil pointers inside → all stage fields nil.
	dto := &ValuationOverrides{
		GrowthStages: &GrowthStages{}, // non-nil struct, but all sub-fields nil
	}

	got := projectOverrides(dto)

	assert.Nil(t, got.Stage1Years, "Stage1Years must be nil when GrowthStages is empty")
	assert.Nil(t, got.Stage2Years)
	assert.Nil(t, got.Stage3Years)
}

// ---------------------------------------------------------------------------
// anyBulkOverride
// ---------------------------------------------------------------------------

func TestAnyBulkOverride_NilEverything(t *testing.T) {
	assert.False(t, anyBulkOverride(nil, nil, nil),
		"no legacy fields and nil options → false")
}

func TestAnyBulkOverride_LegacyBetaOnly(t *testing.T) {
	assert.True(t, anyBulkOverride(ptrFloat(1.0), nil, nil),
		"legacy beta → true")
}

func TestAnyBulkOverride_LegacyRFOnly(t *testing.T) {
	assert.True(t, anyBulkOverride(nil, ptrFloat(0.04), nil),
		"legacy rf → true")
}

func TestAnyBulkOverride_OptionsEmpty(t *testing.T) {
	assert.False(t, anyBulkOverride(nil, nil, &ValuationOverrides{}),
		"non-nil but empty options → false")
}

func TestAnyBulkOverride_OptionsBeta(t *testing.T) {
	assert.True(t, anyBulkOverride(nil, nil, &ValuationOverrides{Beta: ptrFloat(1.1)}),
		"options.beta → true")
}

func TestAnyBulkOverride_OptionsGrowthStages(t *testing.T) {
	assert.True(t, anyBulkOverride(nil, nil, &ValuationOverrides{
		GrowthStages: &GrowthStages{Stage1Years: ptrInt(2)},
	}), "options.growth_stages.stage1_years → true")
}

func TestAnyBulkOverride_OptionsGrowthStagesAllNilSubfields(t *testing.T) {
	// GrowthStages non-nil but all sub-fields nil → still false.
	assert.False(t, anyBulkOverride(nil, nil, &ValuationOverrides{
		GrowthStages: &GrowthStages{},
	}), "GrowthStages present but all sub-fields nil → false")
}

// ---------------------------------------------------------------------------
// detectOverrideConflicts
// ---------------------------------------------------------------------------

func TestDetectOverrideConflicts_NilOptions(t *testing.T) {
	// Nil options can never conflict.
	conflicts := detectOverrideConflicts(ptrFloat(1.2), ptrFloat(0.04), nil)
	assert.Empty(t, conflicts, "nil options → no conflicts")
}

func TestDetectOverrideConflicts_NilLegacyFields(t *testing.T) {
	// Legacy fields absent → no conflicts even when options are set.
	conflicts := detectOverrideConflicts(nil, nil, &ValuationOverrides{
		Beta:         ptrFloat(1.5),
		RiskFreeRate: ptrFloat(0.05),
	})
	assert.Empty(t, conflicts, "nil legacy fields → no conflicts")
}

func TestDetectOverrideConflicts_BetaConflict(t *testing.T) {
	// Both legacy override_beta and options.beta set → beta conflict reported.
	conflicts := detectOverrideConflicts(ptrFloat(1.2), nil, &ValuationOverrides{
		Beta: ptrFloat(1.5),
	})
	require.Len(t, conflicts, 1)
	assert.Contains(t, conflicts[0], "beta",
		"conflict message must mention 'beta'")
	assert.Contains(t, conflicts[0], "override_beta",
		"conflict message must name the legacy field 'override_beta'")
	assert.Contains(t, conflicts[0], "options.beta",
		"conflict message must name the options field 'options.beta'")
}

func TestDetectOverrideConflicts_RFConflict(t *testing.T) {
	// Both legacy override_rf and options.risk_free_rate set → rf conflict reported.
	conflicts := detectOverrideConflicts(nil, ptrFloat(0.04), &ValuationOverrides{
		RiskFreeRate: ptrFloat(0.05),
	})
	require.Len(t, conflicts, 1)
	assert.Contains(t, conflicts[0], "risk_free_rate",
		"conflict message must mention 'risk_free_rate'")
	assert.Contains(t, conflicts[0], "override_rf",
		"conflict message must name the legacy field 'override_rf'")
	assert.Contains(t, conflicts[0], "options.risk_free_rate",
		"conflict message must name the options field 'options.risk_free_rate'")
}

func TestDetectOverrideConflicts_BothConflict(t *testing.T) {
	// Beta AND rf both conflict → two entries returned.
	conflicts := detectOverrideConflicts(ptrFloat(1.2), ptrFloat(0.04), &ValuationOverrides{
		Beta:         ptrFloat(1.5),
		RiskFreeRate: ptrFloat(0.05),
	})
	assert.Len(t, conflicts, 2, "both conflicts must be reported")
}

func TestDetectOverrideConflicts_NoConflict_OnlyOptions(t *testing.T) {
	// Options.MarketRiskPremium set but no legacy twin → no conflict (there is no
	// legacy override_mrp field).
	conflicts := detectOverrideConflicts(nil, nil, &ValuationOverrides{
		MarketRiskPremium: ptrFloat(0.055),
	})
	assert.Empty(t, conflicts)
}

// ---------------------------------------------------------------------------
// GetBulkFairValue — integration via httptest
// ---------------------------------------------------------------------------

// newBulkTestRouter creates a minimal Gin router wired to the given handler.
// No middleware — tests call directly to isolate handler behaviour.
func newBulkTestRouter(h *FairValueHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/fair-value/bulk", h.GetBulkFairValue)
	return r
}

// TestGetBulkFairValue_WithOptions_ReachesServiceWithPopulatedOverrides verifies
// that when a bulk request supplies options, the projected Overrides arrive at the
// service with all fields set correctly — specifically that the service is called
// with a non-nil *ValuationOptions whose Overrides.Beta matches the dto value.
func TestGetBulkFairValue_WithOptions_ReachesServiceWithPopulatedOverrides(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())
	router := newBulkTestRouter(h)

	// The mock should be called once for the single ticker "AAPL" with options
	// that carry the projected Beta override.
	svc.On("CalculateValuation", mock.Anything, "AAPL",
		mock.MatchedBy(func(opts *valuation.ValuationOptions) bool {
			return opts != nil &&
				opts.Overrides.Beta != nil &&
				*opts.Overrides.Beta == 1.35
		}),
	).Return(sampleValuationResult("AAPL"), nil)

	body := map[string]interface{}{
		"tickers": []string{"AAPL"},
		"options": map[string]interface{}{
			"beta": 1.35,
		},
	}
	bodyJSON, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/fair-value/bulk", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	svc.AssertExpectations(t)
}

// TestGetBulkFairValue_WithOptions_GrowthStages verifies that the GrowthStages
// sub-struct is flattened and projected correctly onto Overrides.Stage1Years.
func TestGetBulkFairValue_WithOptions_GrowthStages(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())
	router := newBulkTestRouter(h)

	svc.On("CalculateValuation", mock.Anything, "MSFT",
		mock.MatchedBy(func(opts *valuation.ValuationOptions) bool {
			return opts != nil &&
				opts.Overrides.Stage1Years != nil &&
				*opts.Overrides.Stage1Years == 2 &&
				opts.Overrides.Stage2Years != nil &&
				*opts.Overrides.Stage2Years == 5
		}),
	).Return(sampleValuationResult("MSFT"), nil)

	body := map[string]interface{}{
		"tickers": []string{"MSFT"},
		"options": map[string]interface{}{
			"growth_stages": map[string]interface{}{
				"stage1_years": 2,
				"stage2_years": 5,
			},
		},
	}
	bodyJSON, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/fair-value/bulk", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	svc.AssertExpectations(t)
}

// TestGetBulkFairValue_NoOptions_ServiceCalledWithNilOpts verifies that a
// request without options still calls the service with nil opts (preserving the
// existing cache-friendly default-path behaviour).
func TestGetBulkFairValue_NoOptions_ServiceCalledWithNilOpts(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())
	router := newBulkTestRouter(h)

	// Service must be called with nil opts (no override → cache eligible)
	svc.On("CalculateValuation", mock.Anything, "AAPL",
		(*valuation.ValuationOptions)(nil),
	).Return(sampleValuationResult("AAPL"), nil)

	body := map[string]interface{}{
		"tickers": []string{"AAPL"},
	}
	bodyJSON, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/fair-value/bulk", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	svc.AssertExpectations(t)
}

// TestGetBulkFairValue_ConflictingBetaOverride_Returns422 verifies that when both
// the legacy override_beta field and options.beta are set the bulk handler returns
// 422 INVALID_OVERRIDE without calling the service.
func TestGetBulkFairValue_ConflictingBetaOverride_Returns422(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())
	router := newBulkTestRouter(h)

	// Service must NOT be called on a conflict
	svc.AssertNotCalled(t, "CalculateValuation")

	body := map[string]interface{}{
		"tickers":       []string{"AAPL"},
		"override_beta": 1.2,
		"options": map[string]interface{}{
			"beta": 1.5,
		},
	}
	bodyJSON, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/fair-value/bulk", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"conflicting beta should return 422 Unprocessable Entity")

	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "INVALID_OVERRIDE", errResp.Code,
		"error code must be INVALID_OVERRIDE")
	assert.Equal(t, http.StatusUnprocessableEntity, errResp.Status)
	assert.Contains(t, errResp.Detail, "beta",
		"detail must name the conflicting knob")
	// Context must carry the knob name for programmatic consumers
	require.NotNil(t, errResp.Context)
	assert.Equal(t, "beta", errResp.Context["knob"])
}

// TestGetBulkFairValue_ConflictingRFOverride_Returns422 verifies the same 422
// behaviour for the risk_free_rate conflict (legacy override_rf vs options.risk_free_rate).
func TestGetBulkFairValue_ConflictingRFOverride_Returns422(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())
	router := newBulkTestRouter(h)

	svc.AssertNotCalled(t, "CalculateValuation")

	body := map[string]interface{}{
		"tickers":     []string{"AAPL"},
		"override_rf": 0.04,
		"options": map[string]interface{}{
			"risk_free_rate": 0.05,
		},
	}
	bodyJSON, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/fair-value/bulk", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"conflicting risk_free_rate should return 422")

	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "INVALID_OVERRIDE", errResp.Code)
	assert.Contains(t, errResp.Detail, "risk_free_rate",
		"detail must mention 'risk_free_rate'")
	require.NotNil(t, errResp.Context)
	assert.Equal(t, "risk_free_rate", errResp.Context["knob"])
}

// TestGetBulkFairValue_LegacyBeta_NoConflict_StillWorks verifies that the legacy
// override_beta field alone (without options) continues to work — backward-compat.
func TestGetBulkFairValue_LegacyBeta_NoConflict_StillWorks(t *testing.T) {
	svc := &mockValuationService{}
	h := NewFairValueHandler(svc, zap.NewNop())
	router := newBulkTestRouter(h)

	svc.On("CalculateValuation", mock.Anything, "AAPL",
		mock.MatchedBy(func(opts *valuation.ValuationOptions) bool {
			return opts != nil &&
				opts.OverrideBeta != nil &&
				*opts.OverrideBeta == 1.2 &&
				opts.Overrides.Beta == nil // NOT in Overrides — legacy path only
		}),
	).Return(sampleValuationResult("AAPL"), nil)

	body := map[string]interface{}{
		"tickers":       []string{"AAPL"},
		"override_beta": 1.2,
	}
	bodyJSON, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/fair-value/bulk", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	svc.AssertExpectations(t)
}
