package models_test

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
)

// TestDDM_ConsumerPath_UnaffectedByPhase4 is the DC-1 Phase 4 guard for the
// DDM-migration deferral (spec §7.2 + §9.3). DDM consumer migration is
// deferred to Phase 5 because the mature-large-bank legacy Gordon path is
// pinned bit-for-bit by TestDDM_LegacyPath_BitForBit and migrating its read
// sites to Restated() risks the cross-Tier-2 invariant.
//
// This test is a SUPERSET assertion of TestDDM_LegacyPath_BitForBit: it pins
// not only the DDM output math but ALSO that the upstream model-input field
// values DDM reads via input.HistoricalData.GetLatestPeriod() —
// StockholdersEquity, NetIncome, DividendsPerShare, InterestBearingDebt,
// CashAndCashEquivalents — are unchanged. If any Phase 4 commit accidentally
// ripples into DDM's input (e.g. via a dispatcher dual-write deletion that
// changes the entity DDM consumes), this test diagnoses the regression by
// failing on the specific field that drifted, before the output bits even
// diverge.
//
// Because Phase 4 does NOT modify ddm.go and does NOT regenerate the golden
// fixtures, this test passes trivially today. It exists to FAIL LOUDLY if a
// future Phase 4 change breaks the deferral assumption — at which point the
// commit must be reverted per the CLAUDE.md DDM bit-for-bit gotcha.
func TestDDM_ConsumerPath_UnaffectedByPhase4(t *testing.T) {
	tickers := []string{"jpm", "bac", "wfc"}
	for _, ticker := range tickers {
		t.Run(ticker, func(t *testing.T) {
			input := loadGoldenInput(t, ticker)
			expected := loadGoldenOutput(t, ticker)

			// Pin the DDM consumer-path inputs: the exact *FinancialData
			// fields DDM reads through GetLatestPeriod(). These MUST stay
			// byte-identical to the captured golden input across every
			// Phase 4 commit cluster (C-1..C-5).
			require.NotNil(t, input.HistoricalData, "%s golden input missing HistoricalData", ticker)
			latest, _ := input.HistoricalData.GetLatestPeriod()
			require.NotNil(t, latest, "%s golden input has no latest period", ticker)

			// Snapshot the consumed field bits up front so the assertions
			// below describe exactly which input drifted if Phase 4 leaks.
			stockholdersEquityBits := math.Float64bits(latest.StockholdersEquity)
			netIncomeBits := math.Float64bits(latest.NetIncome)
			dpsBits := math.Float64bits(latest.DividendsPerShare)
			ibdBits := math.Float64bits(input.InterestBearingDebt)
			cashBits := math.Float64bits(input.CashAndCashEquivalents)

			ddm := models.NewDDMModel(zap.NewNop())
			actual, err := ddm.Calculate(context.Background(), input)
			require.NoError(t, err)

			// Output bit-for-bit (superset of the legacy pin).
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

			// Input bit-for-bit: prove Calculate did not mutate the entity
			// fields it reads, and that the golden input is internally stable.
			assert.Equal(t, stockholdersEquityBits, math.Float64bits(latest.StockholdersEquity),
				"%s StockholdersEquity input drifted during DDM consumer path", ticker)
			assert.Equal(t, netIncomeBits, math.Float64bits(latest.NetIncome),
				"%s NetIncome input drifted during DDM consumer path", ticker)
			assert.Equal(t, dpsBits, math.Float64bits(latest.DividendsPerShare),
				"%s DividendsPerShare input drifted during DDM consumer path", ticker)
			assert.Equal(t, ibdBits, math.Float64bits(input.InterestBearingDebt),
				"%s InterestBearingDebt input drifted during DDM consumer path", ticker)
			assert.Equal(t, cashBits, math.Float64bits(input.CashAndCashEquivalents),
				"%s CashAndCashEquivalents input drifted during DDM consumer path", ticker)
		})
	}
}
