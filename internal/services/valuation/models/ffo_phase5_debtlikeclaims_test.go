package models_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
)

// TestFFO_IgnoresDebtLikeClaims is the DC-1 Phase 5 (P5-C1 follow-up,
// gpt-5.5 MEDIUM-3 review finding) permanent guard for the load-bearing
// "FFO does NOT consume ModelInput.DebtLikeClaims" contract.
//
// Pre-P5-C1, the valuation service's performAlternativeValuation set
// modelDebtLikeClaims=0 for both FFO and DDM (only revenue_multiple
// consumed it). P5-C1 simplified the plumbing so modelDebtLikeClaims is
// now populated unconditionally from InvestedCapital().DebtLikeClaims
// for ALL alt-models — including FFO. The simplification is safe TODAY
// because ffo.go has no read site for input.DebtLikeClaims (grep-
// confirmed), so the non-zero value passed by the plumbing is silently
// ignored. But the implicit contract is now load-bearing in production
// code without a permanent regression test.
//
// This test asserts FFO output (IntrinsicValuePerShare + EquityValue +
// EnterpriseValue) is byte-identical under arbitrary DebtLikeClaims
// inputs. A future edit that accidentally adds a DebtLikeClaims term to
// FFO's EV bridge — or to any other FFO read site — would fail this
// guard, surfacing the contract violation before it ships.
//
// FFO contract rationale (router.go ModelInput.DebtLikeClaims godoc):
// FFO's equity is derived directly from the P/FFO multiple
// (valuePerShare = FFO/share × P/FFO; equity = valuePerShare × shares),
// not from an EV→Equity bridge. InterestBearingDebt only back-derives
// the reported EnterpriseValue (EV = equity + IBD − cash). Adding
// DebtLikeClaims to that back-derivation would double-count the REIT's
// lease-bearing cash flows (which already flow through FFO via D&A
// addback + the P/FFO multiple's lease-aware sector calibration).
func TestFFO_IgnoresDebtLikeClaims(t *testing.T) {
	makeInput := func(claims float64) *models.ModelInput {
		return &models.ModelInput{
			HistoricalData: &entities.HistoricalFinancialData{
				Ticker: "REIT_DLC_GUARD",
				Data: map[string]*entities.FinancialData{
					"2024FY": {
						NetIncome:                   100_000_000,
						DepreciationAndAmortization: 40_000_000,
						GainOnPropertySales:         5_000_000,
						OperatingIncome:             120_000_000,
						FilingDate:                  time.Now(),
						FilingPeriod:                "2024FY",
						SharesOutstanding:           10_000_000,
					},
				},
			},
			SharesOutstanding:      10_000_000,
			InterestBearingDebt:    500_000_000,
			CashAndCashEquivalents: 50_000_000,
			DebtLikeClaims:         claims,
			Industry:               "REIT_RESIDENTIAL", // ensure sector multiple path runs
		}
	}

	ffo := models.NewFFOModelWithMultiple(15.0, zap.NewNop())
	ctx := context.Background()

	zero, err := ffo.Calculate(ctx, makeInput(0))
	require.NoError(t, err)
	require.NotNil(t, zero)
	require.Equal(t, "ffo", zero.ModelType)

	// Exercise a meaningful non-zero DebtLikeClaims (half the IBD scale)
	// — large enough that any accidental FFO read would surface as a
	// non-trivial bit drift.
	const claims = 250_000_000.0
	withClaims, err := ffo.Calculate(ctx, makeInput(claims))
	require.NoError(t, err)
	require.NotNil(t, withClaims)
	require.Equal(t, "ffo", withClaims.ModelType)

	// All three output fields must be Float64-bit-identical under
	// arbitrary DebtLikeClaims input.
	assert.Equal(t,
		math.Float64bits(zero.IntrinsicValuePerShare),
		math.Float64bits(withClaims.IntrinsicValuePerShare),
		"FFO IntrinsicValuePerShare drifted under non-zero DebtLikeClaims "+
			"(zero=%g withClaims=%g) — FFO must NOT consume input.DebtLikeClaims; "+
			"if a deliberate FFO bridge change was intended, update the router.go "+
			"ModelInput.DebtLikeClaims godoc + this test atomically",
		zero.IntrinsicValuePerShare, withClaims.IntrinsicValuePerShare)

	assert.Equal(t,
		math.Float64bits(zero.EquityValue),
		math.Float64bits(withClaims.EquityValue),
		"FFO EquityValue drifted under non-zero DebtLikeClaims")

	assert.Equal(t,
		math.Float64bits(zero.EnterpriseValue),
		math.Float64bits(withClaims.EnterpriseValue),
		"FFO EnterpriseValue drifted under non-zero DebtLikeClaims — "+
			"the EV-bridge MUST stay EV = equity + IBD − cash (no claims term); "+
			"otherwise the REIT lease-bearing cash flows would be double-counted "+
			"against the P/FFO multiple")
}
