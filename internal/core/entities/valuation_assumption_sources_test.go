package entities

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValuationResult_AssumptionSources_OmittedOnDefaultPath is the B4
// byte-identity guard for NF1: a ValuationResult with no AssumptionSources (the
// default / absent-guidance path) MUST NOT emit the assumption_sources key, so
// default-path response JSON is byte-identical to the pre-Phase-2 shape.
func TestValuationResult_AssumptionSources_OmittedOnDefaultPath(t *testing.T) {
	r := ValuationResult{Ticker: "AMD"}

	raw, err := json.Marshal(r)
	require.NoError(t, err)
	assert.False(t, strings.Contains(string(raw), "assumption_sources"),
		"assumption_sources must be omitted (omitempty) when unset — NF1 byte-identity")
}

// TestValuationResult_AssumptionSources_EmittedWhenSet confirms the field
// round-trips losslessly when a guidance/non-default source fired.
func TestValuationResult_AssumptionSources_EmittedWhenSet(t *testing.T) {
	r := ValuationResult{
		Ticker: "AMD",
		AssumptionSources: map[string]AssumptionSourceValue{
			"capex_year1": {
				Source: "guidance",
				Detail: "accession=0000002488-26-000012 period=FY2026 conf=0.82 midpoint=$1.50B",
			},
		},
	}

	raw, err := json.Marshal(r)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"assumption_sources"`)

	var got ValuationResult
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, r.AssumptionSources, got.AssumptionSources)
}

// TestAssumptionSourceValue_DetailOmittedWhenEmpty pins the inner omitempty.
func TestAssumptionSourceValue_DetailOmittedWhenEmpty(t *testing.T) {
	raw, err := json.Marshal(AssumptionSourceValue{Source: "profile"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"source":"profile"}`, string(raw))
}
