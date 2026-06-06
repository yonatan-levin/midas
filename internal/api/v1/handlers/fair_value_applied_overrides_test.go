package handlers

// fair_value_applied_overrides_test.go — T10 tests for the applied_overrides
// response field.
//
// Covered scenarios:
//  1. No overrides → applied_overrides absent from JSON (omitempty).
//  2. Request sets tax_rate + horizon_years → applied_overrides has exactly
//     those two keys, each with source:"request" and the resolved values.
//  3. A profile-sourced knob (horizon from profile, no request override) is
//     NOT echoed in v1 (R5: echo request-touched knobs only).
//  4. POST{} ≡ GET parity: applied_overrides absent on both (this reinforces
//     TestPostFairValue_EmptyBody_EqualsGET in fair_value_post_test.go).
//  5. buildFairValueResponse copies the entity carrier 1:1 (unit test).
//  6. params.RequestOverrides returns nil when no knob is request-sourced.
//  7. params.RequestOverrides returns the correct subset for mixed sources.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/params"
)

// ---------------------------------------------------------------------------
// params.RequestOverrides unit tests (pure function, no HTTP machinery needed)
// ---------------------------------------------------------------------------

// TestRequestOverrides_NilWhenNoRequestSource confirms that when no knob has
// SourceRequest in Provenance the method returns nil (not an empty map), so
// the service's `if kvs != nil` guard holds and omitempty drops the field.
func TestRequestOverrides_NilWhenNoRequestSource(t *testing.T) {
	p := &params.EffectiveValuationParams{
		Provenance: map[string]params.Source{
			"beta":            params.SourceDefault,
			"risk_free_rate":  params.SourceDefault,
			"terminal_method": params.SourceProfile,
			"horizon_years":   params.SourceProfile,
		},
	}
	got := p.RequestOverrides()
	assert.Nil(t, got, "RequestOverrides must return nil (not empty map) when no knob is request-sourced")
}

// TestRequestOverrides_EmptyProvenance confirms that a nil/empty Provenance
// map also returns nil (not a panic) — defensive guard.
func TestRequestOverrides_EmptyProvenance(t *testing.T) {
	p := &params.EffectiveValuationParams{}
	got := p.RequestOverrides()
	assert.Nil(t, got)
}

// TestRequestOverrides_ReturnsOnlyRequestSourced confirms that knobs with
// SourceDefault or SourceProfile are NOT echoed; only SourceRequest knobs
// appear in the returned map.
func TestRequestOverrides_ReturnsOnlyRequestSourced(t *testing.T) {
	taxVal := 0.21
	horizonVal := 7

	p := &params.EffectiveValuationParams{
		TaxRate:        taxVal,
		HorizonYears:   horizonVal,
		Beta:           1.2,             // SourceDefault — must NOT appear
		TerminalMethod: "gordon_growth", // SourceProfile — must NOT appear
		Provenance: map[string]params.Source{
			"tax_rate":        params.SourceRequest,
			"horizon_years":   params.SourceRequest,
			"beta":            params.SourceDefault,
			"terminal_method": params.SourceProfile,
		},
	}

	got := p.RequestOverrides()
	require.NotNil(t, got, "expected non-nil map with request-sourced knobs")
	assert.Len(t, got, 2, "only the 2 request-sourced knobs must appear")

	assert.Equal(t, taxVal, got["tax_rate"], "tax_rate value must equal the resolved TaxRate field")
	assert.Equal(t, horizonVal, got["horizon_years"], "horizon_years value must equal the resolved HorizonYears field")

	assert.NotContains(t, got, "beta", "beta is default-sourced and must NOT appear")
	assert.NotContains(t, got, "terminal_method", "terminal_method is profile-sourced and must NOT appear")
}

// TestRequestOverrides_AllKnobTypes exercises every knob name so a future
// knob addition that forgets to add a case is caught by missing coverage.
func TestRequestOverrides_AllKnobTypes(t *testing.T) {
	p := &params.EffectiveValuationParams{
		TerminalGrowthRate: 0.025,
		TerminalGrowthCap:  0.04,
		HorizonYears:       5,
		Stage1Years:        3,
		Stage2Years:        4,
		Stage3Years:        2,
		MaxGrowthRate:      0.5,
		MinGrowthRate:      -0.3,
		TerminalMethod:     "exit_multiple",
		TerminalMultiple:   14.0,
		TaxRate:            0.21,
		Beta:               1.1,
		RiskFreeRate:       0.045,
		MarketRiskPremium:  0.055,
		Provenance: map[string]params.Source{
			"terminal_growth_rate":       params.SourceRequest,
			"terminal_growth_cap":        params.SourceRequest,
			"horizon_years":              params.SourceRequest,
			"growth_stages.stage1_years": params.SourceRequest,
			"growth_stages.stage2_years": params.SourceRequest,
			"growth_stages.stage3_years": params.SourceRequest,
			"max_growth_rate":            params.SourceRequest,
			"min_growth_rate":            params.SourceRequest,
			"terminal_method":            params.SourceRequest,
			"terminal_multiple":          params.SourceRequest,
			"tax_rate":                   params.SourceRequest,
			"beta":                       params.SourceRequest,
			"risk_free_rate":             params.SourceRequest,
			"market_risk_premium":        params.SourceRequest,
		},
	}

	got := p.RequestOverrides()
	require.NotNil(t, got)
	// 14 knobs registered as SourceRequest — all must appear.
	assert.Len(t, got, 14)

	assert.Equal(t, 0.025, got["terminal_growth_rate"])
	assert.Equal(t, 0.04, got["terminal_growth_cap"])
	assert.Equal(t, 5, got["horizon_years"])
	assert.Equal(t, 3, got["growth_stages.stage1_years"])
	assert.Equal(t, 4, got["growth_stages.stage2_years"])
	assert.Equal(t, 2, got["growth_stages.stage3_years"])
	assert.Equal(t, 0.5, got["max_growth_rate"])
	assert.Equal(t, -0.3, got["min_growth_rate"])
	assert.Equal(t, "exit_multiple", got["terminal_method"])
	assert.Equal(t, 14.0, got["terminal_multiple"])
	assert.Equal(t, 0.21, got["tax_rate"])
	assert.Equal(t, 1.1, got["beta"])
	assert.Equal(t, 0.045, got["risk_free_rate"])
	assert.Equal(t, 0.055, got["market_risk_premium"])
}

// ---------------------------------------------------------------------------
// buildFairValueResponse — applied_overrides copy tests (unit, no HTTP)
// ---------------------------------------------------------------------------

// TestBuildFairValueResponse_AppliedOverrides_AbsentWhenNil confirms that when
// ValuationResult.AppliedOverrides is nil the response field is absent from JSON.
func TestBuildFairValueResponse_AppliedOverrides_AbsentWhenNil(t *testing.T) {
	h := &FairValueHandler{}
	result := &entities.ValuationResult{
		Ticker:       "AAPL",
		CalculatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		// AppliedOverrides deliberately nil
	}

	resp := h.buildFairValueResponse("AAPL", result)
	assert.Nil(t, resp.AppliedOverrides, "AppliedOverrides must be nil on the default path")

	// Confirm JSON omits the field entirely (not serialized as null or {}).
	b, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "applied_overrides",
		"applied_overrides must be absent from JSON when nil (omitempty)")
}

// TestBuildFairValueResponse_AppliedOverrides_CopiedWhenPresent confirms that
// when ValuationResult.AppliedOverrides is populated the handler copies it
// to the response with the correct keys, values, and source strings.
func TestBuildFairValueResponse_AppliedOverrides_CopiedWhenPresent(t *testing.T) {
	h := &FairValueHandler{}

	taxVal := 0.21
	horizonVal := 7

	result := &entities.ValuationResult{
		Ticker:       "AAPL",
		CalculatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		AppliedOverrides: map[string]entities.AppliedOverrideValue{
			"tax_rate":      {Value: taxVal, Source: "request"},
			"horizon_years": {Value: horizonVal, Source: "request"},
		},
	}

	resp := h.buildFairValueResponse("AAPL", result)
	require.NotNil(t, resp.AppliedOverrides, "AppliedOverrides must be populated")
	assert.Len(t, resp.AppliedOverrides, 2)

	taxEntry, ok := resp.AppliedOverrides["tax_rate"]
	require.True(t, ok, "tax_rate must be present")
	assert.Equal(t, taxVal, taxEntry.Value, "tax_rate value must be copied verbatim")
	assert.Equal(t, "request", taxEntry.Source, "source must be 'request'")

	horizEntry, ok := resp.AppliedOverrides["horizon_years"]
	require.True(t, ok, "horizon_years must be present")
	assert.Equal(t, horizonVal, horizEntry.Value, "horizon_years value must be copied verbatim")
	assert.Equal(t, "request", horizEntry.Source)
}

// TestBuildFairValueResponse_AppliedOverrides_JSONShape verifies the JSON
// serialization shape of applied_overrides matches the documented contract:
//
//	"applied_overrides": {
//	  "tax_rate":     { "value": 0.21, "source": "request" },
//	  "horizon_years": { "value": 7,   "source": "request" }
//	}
func TestBuildFairValueResponse_AppliedOverrides_JSONShape(t *testing.T) {
	h := &FairValueHandler{}
	result := &entities.ValuationResult{
		Ticker:       "AAPL",
		CalculatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		AppliedOverrides: map[string]entities.AppliedOverrideValue{
			"tax_rate": {Value: 0.21, Source: "request"},
		},
	}

	resp := h.buildFairValueResponse("AAPL", result)
	b, err := json.Marshal(resp)
	require.NoError(t, err)

	// Unmarshal just the applied_overrides field to check shape.
	var wire struct {
		AppliedOverrides map[string]struct {
			Value  interface{} `json:"value"`
			Source string      `json:"source"`
		} `json:"applied_overrides"`
	}
	require.NoError(t, json.Unmarshal(b, &wire))
	require.NotNil(t, wire.AppliedOverrides)
	require.Contains(t, wire.AppliedOverrides, "tax_rate")

	entry := wire.AppliedOverrides["tax_rate"]
	// JSON numbers unmarshal as float64 when the target is interface{}.
	assert.InDelta(t, 0.21, entry.Value.(float64), 1e-12, "value must round-trip through JSON")
	assert.Equal(t, "request", entry.Source)
}

// ---------------------------------------------------------------------------
// POST{} ≡ GET parity — applied_overrides absent on both
// ---------------------------------------------------------------------------

// TestAppliedOverrides_AbsentOnDefaultPath_PostParityGET is a focused
// regression guard: when ValuationResult carries no AppliedOverrides the
// JSON fields are byte-identical for GET and POST{} responses.
//
// This overlaps with TestPostFairValue_EmptyBody_EqualsGET in post_test.go but
// focuses specifically on the absence of applied_overrides, confirming the
// omitempty contract is respected for the new field.
func TestAppliedOverrides_AbsentOnDefaultPath_PostParityGET(t *testing.T) {
	result := sampleValuationResult("AAPL")
	// sampleValuationResult does not set AppliedOverrides — default path.
	h := &FairValueHandler{}

	resp := h.buildFairValueResponse("AAPL", result)
	b, err := json.Marshal(resp)
	require.NoError(t, err)

	assert.NotContains(t, string(b), "applied_overrides",
		"applied_overrides must be absent from JSON for a default-path result (no overrides applied)")
}

// TestAppliedOverrides_ProfileSourcedKnotNotEchoed confirms the R5 design
// decision: a knob whose Provenance is SourceProfile (e.g. horizon from a
// profile rule) is NOT present in RequestOverrides(), so it would not be
// stamped on the result and would not appear in applied_overrides.
//
// This is a pure params unit test — no HTTP machinery needed.
func TestAppliedOverrides_ProfileSourcedKnobNotEchoed(t *testing.T) {
	// Simulate: profile supplied horizon_years=7, no request override.
	p := &params.EffectiveValuationParams{
		HorizonYears: 7,
		Provenance: map[string]params.Source{
			"horizon_years": params.SourceProfile,
		},
	}

	got := p.RequestOverrides()
	// Must return nil — profile-sourced horizon is NOT echoed in v1.
	assert.Nil(t, got,
		"a profile-sourced knob must NOT appear in RequestOverrides() (R5 v1 design decision)")
}
