package models_test

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/cleaneddata"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
)

// TestDDM_ConsumerPath_RestatedViewParity is the DC-1 Phase 5 (P5-C2)
// permanent successor to the (now-renamed) Phase 4 guard
// TestDDM_ConsumerPath_UnaffectedByPhase4. Where the Phase 4 guard pinned
// that DDM consumed the entity (latest.X via GetLatestPeriod) unchanged,
// this guard pins that the migrated DDM read path consumes the Restated
// VIEW (input.LatestRestatedView.X) with parity to the entity for the
// pinned bit-for-bit fixtures.
//
// It is a SUPERSET of TestDDM_LegacyPath_BitForBit:
//
//	(a) Output bit-for-bit: IntrinsicValuePerShare / EquityValue /
//	    EnterpriseValue match the captured pre-Tier-2 golden bits, run
//	    through the Phase-5 view-consuming code path.
//	(b) View-field parity: for the JPM/BAC/WFC fixtures the view fields
//	    DDM now consumes (StockholdersEquity / NetIncome /
//	    DividendsPerShare) equal the entity fields bit-for-bit. This
//	    pins the property the migration's bit-for-bit safety argument
//	    depends on (spec §3.3 KEY SAFETY ANALYSIS).
//
// Failure modes:
//   - Output bits drift → DDM math regressed (revert + investigate, never
//     update goldens; CLAUDE.md DDM gotcha).
//   - View fields drift from entity → a future Restater touched a DDM-
//     consumed field for the fixture tickers (regenerate fixtures with
//     Restater fires, re-prove bit-for-bit, OR exclude the offending
//     fixture from the bit-for-bit set with explicit ARCH approval).
func TestDDM_ConsumerPath_RestatedViewParity(t *testing.T) {
	tickers := []string{"jpm", "bac", "wfc"}
	for _, ticker := range tickers {
		t.Run(ticker, func(t *testing.T) {
			input := loadGoldenInput(t, ticker)
			expected := loadGoldenOutput(t, ticker)

			require.NotNil(t, input.HistoricalData, "%s golden input missing HistoricalData", ticker)
			latest, _ := input.HistoricalData.GetLatestPeriod()
			require.NotNil(t, latest, "%s golden input has no latest period", ticker)

			// Build the Restated view from the golden entity. JPM/BAC/WFC
			// fixtures fire no Restater, so view.X must identity-equal
			// latest.X — the bit-for-bit safety property the P5-C2
			// migration relies on.
			cfd := cleaneddata.New(latest, latest)
			view := cfd.Restated()
			require.NotNil(t, view, "%s view construction failed", ticker)

			// (b) View-field parity: pin the entity-equals-view property
			// at the field bits BEFORE running the model. Failures here
			// diagnose a Restater intrusion into DDM-consumed fields.
			assert.Equal(t,
				math.Float64bits(latest.StockholdersEquity),
				math.Float64bits(view.StockholdersEquity),
				"%s StockholdersEquity: Restated() drifted from entity (entity=%g view=%g)",
				ticker, latest.StockholdersEquity, view.StockholdersEquity)
			assert.Equal(t,
				math.Float64bits(latest.NetIncome),
				math.Float64bits(view.NetIncome),
				"%s NetIncome: Restated() drifted from entity (entity=%g view=%g)",
				ticker, latest.NetIncome, view.NetIncome)
			assert.Equal(t,
				math.Float64bits(latest.DividendsPerShare),
				math.Float64bits(view.DividendsPerShare),
				"%s DividendsPerShare: Restated() drifted from entity (entity=%g view=%g)",
				ticker, latest.DividendsPerShare, view.DividendsPerShare)

			// Populate the view onto ModelInput (mirroring production —
			// service.go::performAlternativeValuation now populates
			// LatestRestatedView for DDM too post-P5-C2).
			input.LatestRestatedView = view

			ddm := models.NewDDMModel(zap.NewNop())
			actual, err := ddm.Calculate(context.Background(), input)
			require.NoError(t, err)

			// (a) Output bit-for-bit through the view-consuming path.
			assert.Equal(t,
				math.Float64bits(expected.IntrinsicValuePerShare),
				math.Float64bits(actual.IntrinsicValuePerShare),
				"%s IntrinsicValuePerShare drifted (expected=%g actual=%g)",
				ticker, expected.IntrinsicValuePerShare, actual.IntrinsicValuePerShare)
			assert.Equal(t,
				math.Float64bits(expected.EquityValue),
				math.Float64bits(actual.EquityValue),
				"%s EquityValue drifted (expected=%g actual=%g)",
				ticker, expected.EquityValue, actual.EquityValue)
			assert.Equal(t,
				math.Float64bits(expected.EnterpriseValue),
				math.Float64bits(actual.EnterpriseValue),
				"%s EnterpriseValue drifted (expected=%g actual=%g)",
				ticker, expected.EnterpriseValue, actual.EnterpriseValue)
			assert.Equal(t, expected.Warnings, actual.Warnings,
				"%s Warnings drifted", ticker)
			assert.Equal(t, expected.Confidence, actual.Confidence,
				"%s Confidence drifted", ticker)
		})
	}
}
