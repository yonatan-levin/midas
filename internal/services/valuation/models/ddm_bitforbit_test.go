package models_test

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
)

// TestDDM_LegacyPath_BitForBit asserts the mature-large-bank DDM output is
// byte-identical pre- and post-Tier-2. The legacy single-stage Gordon path
// (which is the only path DDMModel.Calculate exposes at master HEAD
// e547e39) must produce the same math.Float64bits for IntrinsicValuePerShare,
// EquityValue, and EnterpriseValue as captured at master HEAD via Task B.2.
//
// This test FAILS immediately if any Tier 2 commit causes drift in the
// legacy DDM path. It is the load-bearing assertion for VAL-2
// backward-compat (spec §7.1) and the canonical exit criterion for the
// entire Tier 2 sprint.
//
// Phase Bootstrap input note: the captured ModelInput JSON for each
// ticker derives from the live engine bundle (financial/market/macro/growth
// fields are exactly what the engine produced for the live request) with
// one targeted patch — the dividends_per_share field is overridden with the
// public-record FY2024 annual DPS for each bank. This is necessary because
// the production datacleaner currently emits dps=0 for these tickers (an
// upstream extraction gap tracked separately); without the patch, DDM
// cannot execute and there is no math to pin. The bit-for-bit invariant
// still binds the DDM math for these exact inputs, which is the purpose
// of this test.
func TestDDM_LegacyPath_BitForBit(t *testing.T) {
	tickers := []string{"jpm", "bac", "wfc"}
	for _, ticker := range tickers {
		t.Run(ticker, func(t *testing.T) {
			input := loadGoldenInput(t, ticker)
			expected := loadGoldenOutput(t, ticker)

			ddm := models.NewDDMModel(zap.NewNop())
			actual, err := ddm.Calculate(context.Background(), input)
			require.NoError(t, err)

			assert.Equal(t,
				math.Float64bits(expected.IntrinsicValuePerShare),
				math.Float64bits(actual.IntrinsicValuePerShare),
				"%s IntrinsicValuePerShare drifted from pre-Tier-2 bits (expected=%g, actual=%g)",
				ticker, expected.IntrinsicValuePerShare, actual.IntrinsicValuePerShare)
			assert.Equal(t,
				math.Float64bits(expected.EquityValue),
				math.Float64bits(actual.EquityValue),
				"%s EquityValue drifted from pre-Tier-2 bits (expected=%g, actual=%g)",
				ticker, expected.EquityValue, actual.EquityValue)
			assert.Equal(t,
				math.Float64bits(expected.EnterpriseValue),
				math.Float64bits(actual.EnterpriseValue),
				"%s EnterpriseValue drifted from pre-Tier-2 bits (expected=%g, actual=%g)",
				ticker, expected.EnterpriseValue, actual.EnterpriseValue)

			assert.Equal(t, expected.ModelType, actual.ModelType,
				"%s ModelType drifted", ticker)
			assert.Equal(t, expected.Confidence, actual.Confidence,
				"%s Confidence drifted", ticker)
			assert.Equal(t, expected.Warnings, actual.Warnings,
				"%s Warnings drifted", ticker)
		})
	}
}

func loadGoldenInput(t *testing.T, ticker string) *models.ModelInput {
	t.Helper()
	path := filepath.Join("testdata", "golden", ticker+"_ddm_pre_tier2_input.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "golden input fixture missing — run Task B.2 first")
	var input models.ModelInput
	require.NoError(t, json.Unmarshal(data, &input))
	return &input
}

func loadGoldenOutput(t *testing.T, ticker string) *models.ModelResult {
	t.Helper()
	path := filepath.Join("testdata", "golden", ticker+"_ddm_pre_tier2_output.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "golden output fixture missing — run Task B.2 first")
	var result models.ModelResult
	require.NoError(t, json.Unmarshal(data, &result))
	return &result
}
