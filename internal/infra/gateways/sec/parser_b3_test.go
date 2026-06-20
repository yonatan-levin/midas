package sec

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// SR-1 B3 regression tests — the SEC parser never extracted six fields that
// downstream logic depends on, so:
//
//   - C2 (asset-sale gains), C4 (SBC dilution flag), C5 (derivative marks)
//     could NEVER fire in production (the same gap class TDB-1 closed for
//     C1/C3/C6 and TDB-12 closed for B3 inputs);
//   - A2/A5's TaxShieldDTA was always 0 (EffectiveTaxRate never populated),
//     making the Q2-shipped tax-shield plumbing production-dead;
//   - the balance-sheet classifier's R&D/SBC tech heuristics ran on
//     permanently-zero inputs.
//
// Extraction policy mirrors the TDB-1/TDB-12 precedents:
//
//   - R&D / SBC: income-statement expense magnitudes → absAddBack (TDB-1's
//     idiom — a credit-presented charge is the same dollars with a flipped
//     presentation sign);
//   - AssetSaleGains / DerivativeGainsLosses: SIGNED as-is — the sign is
//     semantic (C2 skips non-positive values = net losses must not be
//     reverse-added; C5 handles both signs and skips only exact zero);
//   - CostOfGoodsSold: as-is (neutral data field, consumers guard);
//   - EffectiveTaxRate: from the `pure`-unit ratio concept, accepted only in
//     (0, 1] — a ratio outside that band (tiny pre-tax income years) would
//     corrupt the A2/A5 TaxShieldDTA computation, so it is left at 0.
//
// See docs/reviewer/SR-1-simplify-and-code-review-candidates.md §B3.
func TestParser_ParseFinancialData_EarningsNormalizationSources(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	usGAAPFact := func(val float64) ports.SECFactGroup {
		return ports.SECFactGroup{
			Units: map[string][]ports.SECFact{
				"USD": {
					{End: "2023-09-30", Val: val, Accn: "0000320193-23-000106", Fy: 2023, Fp: "FY", Form: "10-K", Filed: "2023-11-03"},
				},
			},
		}
	}
	// pureFact builds a `pure`-unit ratio fact (EffectiveIncomeTaxRate* style).
	pureFact := func(val float64) ports.SECFactGroup {
		return ports.SECFactGroup{
			Units: map[string][]ports.SECFact{
				"pure": {
					{End: "2023-09-30", Val: val, Accn: "0000320193-23-000106", Fy: 2023, Fp: "FY", Form: "10-K", Filed: "2023-11-03"},
				},
			},
		}
	}

	baseFacts := func(extra map[string]ports.SECFactGroup) *ports.SECCompanyFacts {
		usGAAP := map[string]ports.SECFactGroup{
			"Revenues":            usGAAPFact(383_285_000_000),
			"OperatingIncomeLoss": usGAAPFact(114_301_000_000),
		}
		for k, v := range extra {
			usGAAP[k] = v
		}
		return &ports.SECCompanyFacts{
			CIK:        ports.FlexibleCIK("320193"),
			EntityName: "Test Filer",
			Facts:      map[string]map[string]ports.SECFactGroup{"us-gaap": usGAAP},
		}
	}

	parseLatest := func(t *testing.T, facts *ports.SECCompanyFacts) map[string]float64 {
		t.Helper()
		hist, err := parser.ParseFinancialData(context.Background(), facts)
		require.NoError(t, err)
		latest := hist.Data["2023FY"]
		require.NotNil(t, latest, "2023FY period must parse")
		return map[string]float64{
			"rd":   latest.ResearchAndDevelopment,
			"sbc":  latest.StockBasedCompensation,
			"asg":  latest.AssetSaleGains,
			"der":  latest.DerivativeGainsLosses,
			"cogs": latest.CostOfGoodsSold,
			"etr":  latest.EffectiveTaxRate,
		}
	}

	t.Run("happy_path_all_six_fields", func(t *testing.T) {
		got := parseLatest(t, baseFacts(map[string]ports.SECFactGroup{
			"ResearchAndDevelopmentExpense":              usGAAPFact(29_915_000_000),
			"ShareBasedCompensation":                     usGAAPFact(10_833_000_000),
			"GainLossOnDispositionOfAssets":              usGAAPFact(450_000_000),
			"GainLossOnDerivativeInstrumentsNetPretax":   usGAAPFact(-120_000_000),
			"CostOfGoodsAndServicesSold":                 usGAAPFact(214_137_000_000),
			"EffectiveIncomeTaxRateContinuingOperations": pureFact(0.147),
		}))

		assert.Equal(t, 29_915_000_000.0, got["rd"], "R&D must be extracted")
		assert.Equal(t, 10_833_000_000.0, got["sbc"], "SBC must be extracted")
		assert.Equal(t, 450_000_000.0, got["asg"], "asset-sale gains must be extracted")
		assert.Equal(t, -120_000_000.0, got["der"],
			"derivative LOSSES must stay signed — C5's gain/loss branches depend on the sign")
		assert.Equal(t, 214_137_000_000.0, got["cogs"], "COGS must be extracted")
		assert.InDelta(t, 0.147, got["etr"], 1e-12, "effective tax rate must ingest from the pure unit")
	})

	t.Run("rd_and_sbc_credit_presentation_abs", func(t *testing.T) {
		got := parseLatest(t, baseFacts(map[string]ports.SECFactGroup{
			"ResearchAndDevelopmentExpense": usGAAPFact(-2_000_000_000),
			"ShareBasedCompensation":        usGAAPFact(-500_000_000),
		}))
		assert.Equal(t, 2_000_000_000.0, got["rd"],
			"credit-presented R&D is the same charge with a flipped sign — absAddBack (TDB-1 idiom)")
		assert.Equal(t, 500_000_000.0, got["sbc"],
			"credit-presented SBC is the same charge with a flipped sign — absAddBack")
	})

	t.Run("asset_sale_net_loss_stays_signed", func(t *testing.T) {
		got := parseLatest(t, baseFacts(map[string]ports.SECFactGroup{
			"GainLossOnDispositionOfAssets": usGAAPFact(-300_000_000),
		}))
		assert.Equal(t, -300_000_000.0, got["asg"],
			"a net disposal LOSS must stay negative — C2's <=0 guard correctly skips it; Abs would fabricate a gain subtraction")
	})

	t.Run("sbc_fallback_to_allocated_expense", func(t *testing.T) {
		got := parseLatest(t, baseFacts(map[string]ports.SECFactGroup{
			"AllocatedShareBasedCompensationExpense": usGAAPFact(7_000_000_000),
		}))
		assert.Equal(t, 7_000_000_000.0, got["sbc"])
	})

	t.Run("cogs_fallback_chain", func(t *testing.T) {
		got := parseLatest(t, baseFacts(map[string]ports.SECFactGroup{
			"CostOfRevenue": usGAAPFact(60_000_000_000),
		}))
		assert.Equal(t, 60_000_000_000.0, got["cogs"], "CostOfRevenue is the second-priority COGS source")
	})

	t.Run("etr_out_of_band_rejected", func(t *testing.T) {
		// A ratio > 1 (tiny pre-tax income year) or <= 0 would corrupt the
		// A2/A5 TaxShieldDTA computation; the parser leaves the field at 0.
		gotHigh := parseLatest(t, baseFacts(map[string]ports.SECFactGroup{
			"EffectiveIncomeTaxRateContinuingOperations": pureFact(1.85),
		}))
		assert.Zero(t, gotHigh["etr"], "ETR > 1 must be rejected (sanity band (0,1])")

		gotNeg := parseLatest(t, baseFacts(map[string]ports.SECFactGroup{
			"EffectiveIncomeTaxRateContinuingOperations": pureFact(-0.10),
		}))
		assert.Zero(t, gotNeg["etr"], "negative ETR must be rejected (sanity band (0,1])")
	})

	t.Run("absent_concepts_leave_fields_zero", func(t *testing.T) {
		got := parseLatest(t, baseFacts(nil))
		assert.Zero(t, got["rd"])
		assert.Zero(t, got["sbc"])
		assert.Zero(t, got["asg"])
		assert.Zero(t, got["der"])
		assert.Zero(t, got["cogs"])
		assert.Zero(t, got["etr"])
	})
}

// TestParser_ParseFinancialData_IFRSEarningsNormalizationSources pins the
// IFRS-full equivalents for 20-F filers (TSM/ASML/SAP class): R&D, SBC and
// COGS publish under ifrs-full concept names that the candidate lists must
// cover (Phase B6 convention — us-gaap names first, IFRS appended).
func TestParser_ParseFinancialData_IFRSEarningsNormalizationSources(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	twdFact := func(val float64) ports.SECFactGroup {
		return ports.SECFactGroup{
			Units: map[string][]ports.SECFact{
				"TWD": {
					{End: "2023-12-31", Val: val, Accn: "0001046179-24-000023", Fy: 2023, Fp: "FY", Form: "20-F", Filed: "2024-04-18"},
				},
			},
		}
	}

	facts := &ports.SECCompanyFacts{
		CIK:        ports.FlexibleCIK("1046179"),
		EntityName: "IFRS Test Filer",
		Facts: map[string]map[string]ports.SECFactGroup{
			"ifrs-full": {
				"Revenue":                                  twdFact(2_161_736_000_000),
				"ProfitLossFromOperatingActivities":        twdFact(921_465_000_000),
				"ResearchAndDevelopmentExpense":            twdFact(182_370_000_000),
				"ExpenseFromSharebasedPaymentTransactions": twdFact(2_000_000_000),
				"CostOfSales":                              twdFact(986_625_000_000),
			},
		},
	}

	hist, err := parser.ParseFinancialData(context.Background(), facts)
	require.NoError(t, err)
	latest := hist.Data["2023FY"]
	require.NotNil(t, latest)

	assert.Equal(t, 182_370_000_000.0, latest.ResearchAndDevelopment, "IFRS R&D concept must extract")
	assert.Equal(t, 2_000_000_000.0, latest.StockBasedCompensation, "IFRS share-based payment expense must extract")
	assert.Equal(t, 986_625_000_000.0, latest.CostOfGoodsSold, "IFRS CostOfSales must extract")
	assert.Equal(t, "TWD", latest.ReportingCurrency, "currency stamp must be unaffected")
}
