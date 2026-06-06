package integration

// fair_value_override_matrix_test.go — "Phase 7" validation-contract regression
// matrix for the request-valuation-overrides feature.
//
// THE CONTRACT: an override value that BOTH validation layers (Layer-1 static
// ranges + Layer-2 resolver invariants) accept must end in one of exactly two
// terminal states — never an engine 500:
//
//	200 OK            — the value COMPUTES (the engine produced a result), or
//	422 INVALID_OVERRIDE — genuine math-invalidity, caught as a typed *ParamError
//	                       in the resolver BEFORE the engine runs.
//
// Every row below asserts the status is 200-or-422 AND explicitly asserts it is
// NOT 500. This is the regression guard for the headline defect: override values
// that both layers accepted (e.g. a NEGATIVE terminal_growth_rate) used to reach
// dcf/wacc.validateInputs, be rejected with an UNTYPED error, and surface as HTTP
// 500. The Chunk-1 validator widening + Chunk-2 resolver 422s close that gap.
//
// Ticker: AAPL (seeded synthetic data in SeedTestData) — routes through multi-stage
// DCF (DividendsPerShare=0 and NormalizedOperatingIncome>0, so DDM/FFO are skipped),
// so every row exercises the dcf/wacc path the contract is about. Default seeded
// beta=1.2; WACC computes to a small positive rate (~8-12%), well below any of the
// "terminal ≥ WACC" 422 rows.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/api/v1/handlers"
)

// We POST raw JSON (rather than building the typed DTO) so each row exercises the
// real decode path + BOTH validation layers + the engine end-to-end.

// postOverrideStatus performs POST /api/v1/fair-value/AAPL with the given options
// map and returns the HTTP status code plus the raw body (for diagnostics).
func postOverrideStatus(t *testing.T, tc *TestContainer, apiKey string, options map[string]any) (int, []byte) {
	t.Helper()
	body := map[string]any{"options": options}
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/fair-value/AAPL", bytes.NewReader(bodyBytes))
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	tc.Router.ServeHTTP(w, req)
	return w.Code, w.Body.Bytes()
}

// TestPostFairValue_OverrideContract_NoFiveHundred is the 200-or-422 regression
// matrix. Each row asserts the response is NEVER 500 and is either 200 (computes)
// or 422 INVALID_OVERRIDE (typed math-invalidity), with row-specific expectations
// where the outcome is deterministic.
func TestPostFairValue_OverrideContract_NoFiveHundred(t *testing.T) {
	tc := SetupTestEnvironment(t)
	defer tc.Cleanup()

	apiKey := helperAPIKey(t, tc)

	// expect captures the intended terminal state for a row.
	type expect int
	const (
		expectCompute expect = iota // must be 200
		expect422                   // must be 422 INVALID_OVERRIDE
		expectEither                // 200 or 422, but documented as a real edge
	)

	tests := []struct {
		name    string
		options map[string]any
		want    expect
		// wantKnob, when non-empty, asserts context.knob on a 422 row.
		wantKnob string
	}{
		{
			// THE HEADLINE REQUIREMENT: a negative terminal growth rate is a
			// supported, first-class scenario (real-terms contraction). It must
			// COMPUTE — it used to 500.
			name:    "negative terminal_growth_rate computes",
			options: map[string]any{"terminal_growth_rate": -0.01},
			want:    expectCompute,
		},
		{
			// Negative beta is real (inverse-correlated assets). Must compute.
			name:    "negative beta computes",
			options: map[string]any{"beta": -1.0},
			want:    expectCompute,
		},
		{
			// Negative effective tax rate is real (NOLs / credits). Must compute.
			name:    "negative tax_rate computes",
			options: map[string]any{"tax_rate": -0.2},
			want:    expectCompute,
		},
		{
			// Negative nominal risk-free rates have occurred (EUR/JPY/CHF). Must compute.
			name:    "negative risk_free_rate computes",
			options: map[string]any{"risk_free_rate": -0.02},
			want:    expectCompute,
		},
		{
			// A high-but-plausible MRP (25%) is within the contract band [0, 0.30].
			name:    "high market_risk_premium computes",
			options: map[string]any{"market_risk_premium": 0.25},
			want:    expectCompute,
		},
		{
			// A 20-year horizon with stage years summing to >= 20 is supported
			// (ProjectionYears band widened to [1, 50]).
			name: "20-year horizon with sufficient stage years computes",
			options: map[string]any{
				"horizon_years": 20,
				"growth_stages": map[string]any{
					"stage1_years": 7,
					"stage2_years": 7,
					"stage3_years": 7,
				},
			},
			want: expectCompute,
		},
		{
			// terminal_growth_rate at 0.45 is within Layer-1 [-0.20, 0.50] but far
			// above AAPL's WACC (~0.08-0.12). The resolver upgrades this to a typed
			// 422 on knob terminal_growth_rate BEFORE the engine — never a 500.
			name:     "terminal_growth_rate above WACC is a typed 422",
			options:  map[string]any{"terminal_growth_rate": 0.45},
			want:     expect422,
			wantKnob: "terminal_growth_rate",
		},
		{
			// An extreme CAPM combo (very negative beta + max MRP + zero risk-free)
			// drives the resolved cost of capital non-positive. The resolver catches
			// it as a typed 422 on knob "wacc" instead of the engine's WACC>0 500.
			name: "extreme combo driving WACC <= 0 is a typed 422",
			options: map[string]any{
				"beta":                -5.0,
				"market_risk_premium": 0.30,
				"risk_free_rate":      0.0,
			},
			want:     expect422,
			wantKnob: "wacc",
		},
		{
			// HIGH-1: a low-but-positive WACC regime (0 < WACC < 0.02). With
			// terminal_growth_rate OMITTED, auto-derive clamps to the 0.01 degenerate
			// floor, leaving (WACC − g) below the 1% spread. The resolver upgrades this
			// to a typed 422 (knob terminal_growth_rate, or "wacc" if the combo lands
			// WACC ≤ 0) instead of letting dcf.validateInputs 500. wantKnob is left
			// empty because the exact knob depends on the seeded data's cost-of-debt
			// contribution — the contract is "typed 422, never 500".
			name: "low-WACC auto-derive is a typed 422 (HIGH-1)",
			options: map[string]any{
				"risk_free_rate":      0.005,
				"market_risk_premium": 0.0,
				"beta":                0.0,
			},
			want: expect422,
		},
		{
			// HIGH-2: growth_stages summing > 50 with horizon_years OMITTED. The core
			// contract here is NOT-500. Whether this lands 422 or 200 depends on the
			// effective horizon's SOURCE:
			//   - If the horizon is DEFAULT-sourced (== growth-rate length == stage-sum
			//     == 51), it exceeds the engine's ProjectionYears ≤ 50 rail and the
			//     resolver 422s on growth_stages BEFORE dcf.validateInputs would 500.
			//   - In THIS seeded environment AAPL resolves to the fallback profile
			//     `software_like_scaling`, whose HorizonYears=5 takes precedence over the
			//     default growth-rate length, pinning the DCF horizon to 5 → 200. The
			//     stage overrides still reshape the growth curve but never push the
			//     horizon past the rail.
			// Either terminal state is contract-valid; the deterministic 422-when-default-
			// sourced path is pinned by params.TestResolveInputs_HorizonExceedsDCFMax_*.
			name: "stage-derived horizon > 50 never 500s (HIGH-2)",
			options: map[string]any{
				"growth_stages": map[string]any{
					"stage1_years": 50,
					"stage2_years": 1,
				},
			},
			want: expectEither,
		},
		{
			// terminal_growth_cap is within Layer-1 [-0.20, 0.50]; 0.10 only raises the
			// auto-derive ceiling and must COMPUTE.
			name:    "terminal_growth_cap computes",
			options: map[string]any{"terminal_growth_cap": 0.10},
			want:    expectCompute,
		},
		{
			// max_growth_rate at the Layer-1 ceiling (10.0) only widens the estimator
			// clamp and must COMPUTE.
			name:    "max_growth_rate at ceiling computes",
			options: map[string]any{"max_growth_rate": 10.0},
			want:    expectCompute,
		},
		{
			// MEDIUM: terminal_method=gordon_growth SUPPRESSES exit-multiple blending
			// (pure Gordon Growth TV). It must COMPUTE for a ticker that has an industry
			// multiple available (AAPL → TECH). Before the selector fix this still
			// averaged; now it produces a pure Gordon TV without error.
			name:    "terminal_method gordon_growth computes (suppresses exit multiple)",
			options: map[string]any{"terminal_method": "gordon_growth"},
			want:    expectCompute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := postOverrideStatus(t, tc, apiKey, tt.options)

			// THE CORE INVARIANT, asserted on every single row: never a 500.
			require.NotEqual(t, http.StatusInternalServerError, status,
				"override must never produce HTTP 500 — got body: %s", string(body))

			// And the status must be exactly one of the two contract terminals.
			require.Contains(t, []int{http.StatusOK, http.StatusUnprocessableEntity}, status,
				"status must be 200 or 422, got %d: %s", status, string(body))

			switch tt.want {
			case expectCompute:
				require.Equal(t, http.StatusOK, status,
					"this override must COMPUTE (200), got %d: %s", status, string(body))
				var resp handlers.FairValueResponse
				require.NoError(t, json.Unmarshal(body, &resp),
					"200 body must decode to a FairValueResponse")
				assert.Equal(t, "AAPL", resp.Ticker, "a real valuation result must be returned")
				assert.NotEmpty(t, resp.CalculationMethod,
					"a computed result must carry a calculation method")

			case expect422:
				require.Equal(t, http.StatusUnprocessableEntity, status,
					"this override must be a typed 422, got %d: %s", status, string(body))
				var errResp handlers.ErrorResponse
				require.NoError(t, json.Unmarshal(body, &errResp),
					"422 body must decode to an ErrorResponse")
				assert.Equal(t, "INVALID_OVERRIDE", errResp.Code,
					"422 must be coded INVALID_OVERRIDE")
				if tt.wantKnob != "" {
					require.NotNil(t, errResp.Context)
					assert.Equal(t, tt.wantKnob, errResp.Context["knob"],
						"422 context.knob must name the offending knob")
				}

			case expectEither:
				// Already covered by the NotEqual(500) + Contains assertions above.
			}
		})
	}
}
