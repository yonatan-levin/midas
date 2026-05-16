package entities

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFinancialData_PlugFields_JSONRoundtrip pins the four plug fields' JSON
// tags so the replay bundle / cache layer can round-trip them deterministically.
// DC-1 Phase 0 — see docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md
func TestFinancialData_PlugFields_JSONRoundtrip(t *testing.T) {
	fd := FinancialData{
		OtherCurrentAssets:         12_345.0,
		OtherNonCurrentAssets:      67_890.0,
		OtherCurrentLiabilities:    1_111.0,
		OtherNonCurrentLiabilities: 2_222.0,
	}

	raw, err := json.Marshal(fd)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"other_current_assets":12345`)
	assert.Contains(t, string(raw), `"other_non_current_assets":67890`)
	assert.Contains(t, string(raw), `"other_current_liabilities":1111`)
	assert.Contains(t, string(raw), `"other_non_current_liabilities":2222`)

	var decoded FinancialData
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, fd.OtherCurrentAssets, decoded.OtherCurrentAssets)
	assert.Equal(t, fd.OtherNonCurrentAssets, decoded.OtherNonCurrentAssets)
	assert.Equal(t, fd.OtherCurrentLiabilities, decoded.OtherCurrentLiabilities)
	assert.Equal(t, fd.OtherNonCurrentLiabilities, decoded.OtherNonCurrentLiabilities)
}
