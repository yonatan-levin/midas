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

// TestDDM_EVBridge_AddsDebtLikeClaims pins the DC-1 Phase 5 (P5-C1) EV-bridge
// correction: DDM's EnterpriseValue must ADD DebtLikeClaims (the B1 lease +
// B2 pension + B3 contingent overlay total). The sign is opposite to the DCF
// and revenue_multiple paths (which SUBTRACT it) because DDM derives equity
// FROM dividends first and then derives EV FROM equity:
//
//	EV = equity + debt + DebtLikeClaims − cash
//
// DDM's IntrinsicValuePerShare and EquityValue MUST be unchanged versus the
// zero-claims case — they are dividend-derived, independent of debt terms.
// Only EnterpriseValue is corrected. Spec §3.2.
//
// Test name uses "Adds" (vs the revenue_multiple equivalent "Subtracts") to
// preserve sign clarity — the parallel between the two tests is by PURPOSE
// (both prove the bridge respects DebtLikeClaims), not by sign.
func TestDDM_EVBridge_AddsDebtLikeClaims(t *testing.T) {
	const (
		// Synthetic bank: $4 DPS, 10% CoE, 3B shares, $100B IBD, $50B cash.
		// $20B of debt-like claims to make the EV correction visible at a
		// scale comparable to InterestBearingDebt (forces the test to
		// distinguish "added" from "subtracted" beyond float noise).
		dps               = 4.00
		costOfEquity      = 0.10
		netIncome         = 50_000_000_000.0
		shareholdersEqu   = 300_000_000_000.0
		sharesOutstanding = 3_000_000_000.0
		ibd               = 100_000_000_000.0
		cash              = 50_000_000_000.0
		debtLikeClaims    = 20_000_000_000.0
	)

	makeInput := func(debtLikeClaims float64) *models.ModelInput {
		return &models.ModelInput{
			HistoricalData: &entities.HistoricalFinancialData{
				Ticker: "SYNTH_BANK",
				Data: map[string]*entities.FinancialData{
					"2023FY": {
						DividendsPerShare:  dps,
						NetIncome:          netIncome,
						StockholdersEquity: shareholdersEqu,
						FilingDate:         time.Now(),
						FilingPeriod:       "2023FY",
						SharesOutstanding:  sharesOutstanding,
					},
				},
			},
			CostOfEquity:           costOfEquity,
			SharesOutstanding:      sharesOutstanding,
			InterestBearingDebt:    ibd,
			CashAndCashEquivalents: cash,
			DebtLikeClaims:         debtLikeClaims,
		}
	}

	ddm := models.NewDDMModel(zap.NewNop())
	ctx := context.Background()

	t.Run("legacy_gordon", func(t *testing.T) {
		zero, err := ddm.Calculate(ctx, makeInput(0))
		require.NoError(t, err)
		withClaims, err := ddm.Calculate(ctx, makeInput(debtLikeClaims))
		require.NoError(t, err)

		// IntrinsicValuePerShare + EquityValue must be UNCHANGED — DDM
		// equity is dividend-derived, independent of debt terms.
		assert.Equal(t,
			math.Float64bits(zero.IntrinsicValuePerShare),
			math.Float64bits(withClaims.IntrinsicValuePerShare),
			"IntrinsicValuePerShare must be invariant to DebtLikeClaims")
		assert.Equal(t,
			math.Float64bits(zero.EquityValue),
			math.Float64bits(withClaims.EquityValue),
			"EquityValue must be invariant to DebtLikeClaims")

		// EnterpriseValue must increase by EXACTLY DebtLikeClaims (the +X term).
		gotDelta := withClaims.EnterpriseValue - zero.EnterpriseValue
		assert.InDelta(t, debtLikeClaims, gotDelta, 0.5,
			"EnterpriseValue must increase by DebtLikeClaims (got delta=%g, want=%g)",
			gotDelta, debtLikeClaims)

		// Explicit identity check: EV = equity + IBD + claims − cash.
		wantEV := withClaims.EquityValue + ibd + debtLikeClaims - cash
		assert.InDelta(t, wantEV, withClaims.EnterpriseValue, 0.5,
			"EV identity violated: got=%g want=%g (equity=%g IBD=%g claims=%g cash=%g)",
			withClaims.EnterpriseValue, wantEV,
			withClaims.EquityValue, ibd, debtLikeClaims, cash)
	})

	t.Run("multistage_via_payout_path", func(t *testing.T) {
		// The multi-stage branch fires only when Profile.PayoutPath is
		// populated with a positive horizon. Without a Profile (Profile=nil),
		// the dispatcher falls through to calculateLegacyGordon and the EV
		// math is the same code path covered by legacy_gordon above. We pin
		// the legacy path explicitly here (the subtest documents that the
		// EV correction lands in calculateLegacyGordon's bridge for the
		// default non-Profile execution); a dedicated multi-stage fixture
		// would require building a ResolvedProfile and is exercised by the
		// multi-stage suite (ddm_multistage_test.go) for non-bridge math.
		input := makeInput(debtLikeClaims)
		result, err := ddm.Calculate(ctx, input)
		require.NoError(t, err)
		require.Equal(t, "ddm", result.ModelType)
		assert.Greater(t, result.EnterpriseValue, result.EquityValue,
			"EV should exceed equity when debt+claims > cash")
	})
}

// TestDDM_EVBridge_ZeroClaims_Unchanged is the bit-for-bit safety guard for
// the P5-C1 correction: with DebtLikeClaims=0 the EV bridge must produce
// byte-identical EnterpriseValue to the pre-Phase-5 formula
// (EV = equity + debt − cash). This is the explicit invariant-safety proof
// the spec §3.2 argument relies on for the JPM/BAC/WFC fixtures
// (DebtLikeClaims=0 ⇒ +0 term ⇒ unchanged bits).
func TestDDM_EVBridge_ZeroClaims_Unchanged(t *testing.T) {
	input := &models.ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "ZERO_CLAIMS",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					DividendsPerShare:  3.50,
					NetIncome:          40_000_000_000,
					StockholdersEquity: 250_000_000_000,
					FilingDate:         time.Now(),
					FilingPeriod:       "2023FY",
					SharesOutstanding:  2_500_000_000,
				},
			},
		},
		CostOfEquity:           0.09,
		SharesOutstanding:      2_500_000_000,
		InterestBearingDebt:    80_000_000_000,
		CashAndCashEquivalents: 30_000_000_000,
		DebtLikeClaims:         0.0, // explicit zero
	}

	ddm := models.NewDDMModel(zap.NewNop())
	result, err := ddm.Calculate(context.Background(), input)
	require.NoError(t, err)

	// EV identity under zero claims: EV = equity + IBD − cash (the +0 term
	// makes the formula bit-equivalent to pre-Phase-5).
	wantEV := result.EquityValue + 80_000_000_000.0 - 30_000_000_000.0
	assert.Equal(t,
		math.Float64bits(wantEV),
		math.Float64bits(result.EnterpriseValue),
		"EnterpriseValue must be byte-identical to pre-Phase-5 formula when DebtLikeClaims=0 (got=%g want=%g)",
		result.EnterpriseValue, wantEV)
}

// TestDDM_GoldenFixtures_ZeroDebtLikeClaims is a guard against silent
// regeneration of the JPM/BAC/WFC goldens with a non-zero DebtLikeClaims
// field. The bit-for-bit safety of the P5-C1 correction depends on the
// captured ModelInput JSONs deserializing DebtLikeClaims as 0 (the field
// is absent from the JSON; Go's json package leaves it at its zero value).
// If a future fixture refresh adds the field with a non-zero value,
// TestDDM_LegacyPath_BitForBit would silently fail to detect drift on the
// EV-bridge correction. This test pins the zero-claims property
// independently of the output math.
func TestDDM_GoldenFixtures_ZeroDebtLikeClaims(t *testing.T) {
	tickers := []string{"jpm", "bac", "wfc"}
	for _, ticker := range tickers {
		t.Run(ticker, func(t *testing.T) {
			input := loadGoldenInput(t, ticker)
			require.NotNil(t, input)
			assert.Equal(t, 0.0, input.DebtLikeClaims,
				"%s golden fixture has non-zero DebtLikeClaims (%g) — the bit-for-bit safety argument of P5-C1 requires this to be 0; if intentional, the bit-for-bit test must be re-proved",
				ticker, input.DebtLikeClaims)
		})
	}
}
