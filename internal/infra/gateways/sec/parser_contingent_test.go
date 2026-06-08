package sec

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// TestParser_ParseFinancialData_ContingentLiabilities pins TDB-12: the SEC
// parser must populate the three B3 contingent-liability balance-sheet fields
// (ContingentLiabilities / EnvironmentalLiabilities / LitigationLiabilities)
// from recognized ASC 450 / ASC 410 XBRL accrual concepts, in reporting
// currency, using the established findValue first-hit (aggregate-first) idiom.
//
// Load-bearing sub-cases:
//   - aggregate populates its field (each of the three fields independently);
//   - split fallback fires when ONLY the current/noncurrent split is reported;
//   - THE DOUBLE-COUNT GUARD: a filer reporting BOTH the aggregate AND the
//     current split yields the aggregate, NOT their sum (first-hit, not
//     sumValues — MSFT/MXL shape, TDB-12 spec §3.3 vector 1);
//   - cross-rule exclusion: an income-statement LitigationSettlementExpense
//     (C3's field, mapped by TDB-1) must NOT populate LitigationLiabilities;
//   - disclosure exclusion: a possible-loss DISCLOSURE estimate
//     (LossContingencyEstimateOfPossibleLoss) must NOT populate
//     ContingentLiabilities (it is not a recognized accrual);
//   - sign policy: a NEGATIVE recognized accrual clamps to 0 (val > 0), NOT
//     math.Abs — a negative recognized liability is a data anomaly, not a
//     credit-presentation flip (TDB-12 spec §3.1, Q1 — the opposite of TDB-1's
//     Abs choice for income-statement charges);
//   - no false population: absent concepts leave all three fields at 0.
//
// Spec: docs/refactoring/spec/tdb-12-contingent-liability-parser-extraction-spec.md
func TestParser_ParseFinancialData_ContingentLiabilities(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// usGAAPFact builds a single-fact FY-2023 SECFactGroup (USD, 10-K) so the
	// period key is "2023FY", matching the existing success-test idiom.
	usGAAPFact := func(val float64) ports.SECFactGroup {
		return ports.SECFactGroup{
			Units: map[string][]ports.SECFact{
				"USD": {
					{End: "2023-09-30", Val: val, Accn: "0000320193-23-000106", Fy: 2023, Fp: "FY", Form: "10-K", Filed: "2023-11-03"},
				},
			},
		}
	}

	tests := []struct {
		name              string
		usGAAP            map[string]ports.SECFactGroup
		wantContingent    float64
		wantEnvironmental float64
		wantLitigation    float64
	}{
		{
			name: "contingent_aggregate",
			usGAAP: map[string]ports.SECFactGroup{
				"Revenues":                              usGAAPFact(383285000000),
				"OperatingIncomeLoss":                   usGAAPFact(114301000000),
				"LossContingencyAccrualAtCarryingValue": usGAAPFact(541000000),
			},
			wantContingent: 541000000,
		},
		{
			// Only the current split, no aggregate — first-hit fallback fires.
			name: "contingent_split_fallback",
			usGAAP: map[string]ports.SECFactGroup{
				"Revenues":            usGAAPFact(383285000000),
				"OperatingIncomeLoss": usGAAPFact(114301000000),
				"LossContingencyAccrualCarryingValueCurrent": usGAAPFact(364000000),
			},
			wantContingent: 364000000,
		},
		{
			// THE DOUBLE-COUNT GUARD (MSFT-shaped): aggregate + current both
			// present. The aggregate is the TOTAL of current+noncurrent, so
			// summing would double-count. first-hit aggregate-first wins.
			name: "contingent_aggregate_wins_over_split",
			usGAAP: map[string]ports.SECFactGroup{
				"Revenues":                                   usGAAPFact(383285000000),
				"OperatingIncomeLoss":                        usGAAPFact(114301000000),
				"LossContingencyAccrualAtCarryingValue":      usGAAPFact(541000000),
				"LossContingencyAccrualCarryingValueCurrent": usGAAPFact(364000000),
			},
			wantContingent: 541000000, // aggregate wins; NOT 541M+364M summed.
		},
		{
			name: "environmental_aggregate",
			usGAAP: map[string]ports.SECFactGroup{
				"Revenues":            usGAAPFact(383285000000),
				"OperatingIncomeLoss": usGAAPFact(114301000000),
				"AccrualForEnvironmentalLossContingencies": usGAAPFact(4800000),
			},
			wantEnvironmental: 4800000,
		},
		{
			name: "litigation_reserve",
			usGAAP: map[string]ports.SECFactGroup{
				"Revenues":                     usGAAPFact(383285000000),
				"OperatingIncomeLoss":          usGAAPFact(114301000000),
				"EstimatedLitigationLiability": usGAAPFact(250000000),
			},
			wantLitigation: 250000000,
		},
		{
			// Cross-rule exclusion: income-statement litigation EXPENSE is C3's
			// (TDB-1), NOT B3's balance-sheet litigation liability. Reusing it
			// would double-count the same dollars across two rules.
			name: "excludes_income_statement_litigation",
			usGAAP: map[string]ports.SECFactGroup{
				"Revenues":                    usGAAPFact(383285000000),
				"OperatingIncomeLoss":         usGAAPFact(114301000000),
				"LitigationSettlementExpense": usGAAPFact(379000000),
			},
			wantLitigation: 0,
		},
		{
			// Disclosure exclusion: possible-loss estimate is NOT a recognized
			// accrual (ASC 450 "reasonably possible" range). B3 already
			// probability-weights, so feeding an unaccrued upper bound would
			// over-weight a number the filer chose not to recognize.
			name: "excludes_possible_loss_disclosure",
			usGAAP: map[string]ports.SECFactGroup{
				"Revenues":                              usGAAPFact(383285000000),
				"OperatingIncomeLoss":                   usGAAPFact(114301000000),
				"LossContingencyEstimateOfPossibleLoss": usGAAPFact(900000000),
			},
			wantContingent: 0,
		},
		{
			// Sign policy: negative recognized accrual clamps to 0 (val > 0),
			// NOT math.Abs. A negative recognized liability is a data anomaly,
			// not a credit-presentation flip (TDB-12 Q1).
			name: "negative_clamps_to_zero",
			usGAAP: map[string]ports.SECFactGroup{
				"Revenues":                              usGAAPFact(383285000000),
				"OperatingIncomeLoss":                   usGAAPFact(114301000000),
				"LossContingencyAccrualAtCarryingValue": usGAAPFact(-100000000),
			},
			wantContingent: 0,
		},
		{
			name: "all_absent",
			usGAAP: map[string]ports.SECFactGroup{
				"Revenues":            usGAAPFact(383285000000),
				"OperatingIncomeLoss": usGAAPFact(114301000000),
			},
			// All three want-fields default to 0 — pins no false population.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factsByTaxonomy := map[string]map[string]ports.SECFactGroup{}
			if len(tt.usGAAP) > 0 {
				factsByTaxonomy["us-gaap"] = tt.usGAAP
			}

			facts := &ports.SECCompanyFacts{
				CIK:        "0000320193",
				EntityName: "Test Filer",
				Facts:      factsByTaxonomy,
			}

			ctx := context.Background()
			historical, err := parser.ParseFinancialData(ctx, facts)
			require.NoError(t, err)
			require.NotNil(t, historical)

			data, exists := historical.Data["2023FY"]
			require.True(t, exists, "expected 2023FY period in historical.Data")
			require.NotNil(t, data)

			assert.Equal(t, tt.wantContingent, data.ContingentLiabilities,
				"ContingentLiabilities (B3 source) must match expected accrual (aggregate-first, clamp>=0)")
			assert.Equal(t, tt.wantEnvironmental, data.EnvironmentalLiabilities,
				"EnvironmentalLiabilities (B3 source) must match expected accrual (aggregate-first, clamp>=0)")
			assert.Equal(t, tt.wantLitigation, data.LitigationLiabilities,
				"LitigationLiabilities (B3 source) must match expected accrual (clamp>=0, NOT income-statement expense)")
		})
	}
}
