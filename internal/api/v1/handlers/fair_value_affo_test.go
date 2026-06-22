package handlers

// fair_value_affo_test.go — VAL-3 Phase 2 (#15) tests for surfacing the REIT
// FFO/AFFO per-share numbers on the fair-value HTTP response as
// pffo_value_per_share / paffo_value_per_share.
//
// Coverage:
//  1. Mapping — PFFO/PAFFO on ValuationResult flow through buildFairValueResponse.
//  2. omitempty — zero PAFFO is dropped from the marshalled JSON; present values
//     render in snake_case.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildFairValueResponse_AFFOFields asserts the two VAL-3 Phase 2 fields are
// mapped onto FairValueResponse and that omitempty drops a zero PAFFO while a
// present value renders as snake_case JSON.
func TestBuildFairValueResponse_AFFOFields(t *testing.T) {
	h := newTestFairValueHandler(&mockValuationService{})

	t.Run("both_present_map_and_render", func(t *testing.T) {
		result := sampleValuationResult("DLR")
		result.PFFOValuePerShare = 102.0
		result.PAFFOValuePerShare = 84.0
		result.DCFValuePerShare = 84.0 // headline == AFFO when available

		resp := h.buildFairValueResponse("DLR", result)
		assert.Equal(t, 102.0, resp.PFFOValuePerShare)
		assert.Equal(t, 84.0, resp.PAFFOValuePerShare)

		raw, err := json.Marshal(resp)
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		assert.Equal(t, 102.0, m["pffo_value_per_share"])
		assert.Equal(t, 84.0, m["paffo_value_per_share"])
	})

	t.Run("zero_paffo_omitted", func(t *testing.T) {
		// REIT with AFFO unavailable: PFFO present, PAFFO == 0 → omitted.
		result := sampleValuationResult("AMT")
		result.PFFOValuePerShare = 135.0
		result.PAFFOValuePerShare = 0.0

		resp := h.buildFairValueResponse("AMT", result)
		raw, err := json.Marshal(resp)
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))

		assert.Equal(t, 135.0, m["pffo_value_per_share"], "PFFO surfaced")
		_, hasPAFFO := m["paffo_value_per_share"]
		assert.False(t, hasPAFFO, "zero PAFFO must be omitted (omitempty)")
	})

	t.Run("non_reit_both_omitted", func(t *testing.T) {
		// DCF result: neither field set → both omitted, legacy shape preserved.
		result := sampleValuationResult("AAPL")
		resp := h.buildFairValueResponse("AAPL", result)
		raw, err := json.Marshal(resp)
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))

		_, hasPFFO := m["pffo_value_per_share"]
		_, hasPAFFO := m["paffo_value_per_share"]
		assert.False(t, hasPFFO, "non-REIT: PFFO omitted")
		assert.False(t, hasPAFFO, "non-REIT: PAFFO omitted")
	})
}
