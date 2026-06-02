package datacleaner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestApplyActiveAdjustments_AdjustmentsProjection_BasketParity is the
// load-bearing pre-vs-post-rewrite gate for P5-C3-full (Adjustments-
// projection). It runs a fixed basket of synthetic ticker fixtures
// through the real DataCleanerService and asserts byte-equality between
// the live `result.Adjustments` slice and a committed golden JSON.
//
// Fixture design: each named seed tunes input fields to push the cleaner
// down a different cluster of adjusters so the basket as a whole covers
//
//   - the OverlayEmitter family (A1 goodwill, B1 leases, B2 pension, B3 CL),
//   - the Restater family with non-trivial pre-state-derived Percentage
//     (A2 intangibles, C2 asset-sale gains, C3 litigation, C5 derivatives,
//     C6 capitalized interest),
//   - the constant-Percentage Restaters (A4 DTA, A5 inventory),
//   - the FlagEmitter family (C4 SBC with non-trivial Percentage; C7 WC),
//   - the deliberate-zero-Percentage Restater (C1 restructuring),
//   - and a handful of skip paths so the projection's
//     `(unknown AdjusterID → skip)` and `(Fired:false → skip)` filters are
//     covered.
//
// Excluded fields (non-deterministic / re-derivable):
//   - `ID`               — built from `fmt.Sprintf("...%d", time.Now().UnixNano())`.
//     The projection helper (post-A4) preserves the
//     `<prefix>_<UnixNano>` shape, but the nanosecond
//     tail drifts every run.
//   - `Timestamp`        — `time.Now()` at emit. Post-A4 the projection
//     reads `LedgerEntry.Timestamp`, but the input to the
//     pre-rewrite golden uses the legacy translator's
//     `time.Now()` so a same-run-vs-golden compare would
//     drift even on a no-op rewrite. Drop it.
//
// Acceptance bar: 0 ticker drifts. Any drift in a non-excluded field
// REVERTS the projection change — do NOT update the golden to make this
// pass.
//
// Spec: docs/refactoring/spec/dc1-phase-5-followup-percentage-decision.md §7.1
func TestApplyActiveAdjustments_AdjustmentsProjection_BasketParity(t *testing.T) {
	// Synthetic fixtures, sorted by name for stable iteration. The seed
	// values are tuned by inspection of the per-rule applicability +
	// fire thresholds at:
	//   - checkRuleApplicability (service.go ~line 648),
	//   - per-Apply method skip-path guards (assets.go/earnings.go/liabilities.go).
	//
	// Each seed sets minimum required input fields (Ticker, Revenue,
	// TotalAssets, FilingDate, SharesOutstanding) so ValidateData passes.
	type fixture struct {
		name string
		seed *entities.FinancialData
	}

	baseFiling := time.Date(2025, 6, 30, 0, 0, 0, 0, time.UTC)

	fixtures := []fixture{
		// HEAVY_INTANGIBLES — tuned to fire A1 (goodwill > 5% of TA),
		// A2 (intangibles > 2% of TA), A4 (DTA threshold), A5 (high
		// inventory + low turnover), B1 (any revenue), B2 (revenue
		// large enough), B3 (contingent ratio > 2%), C2/C3/C5/C6
		// (positive raw values above 1-2% revenue thresholds), C4
		// (SBC > 0), C7 (revenue > 50M).
		{
			name: "HEAVY_INTANGIBLES",
			seed: &entities.FinancialData{
				Ticker:                    "HEAVY_INTANGIBLES",
				CIK:                       "1000001",
				Revenue:                   1_000_000_000.0,
				TotalAssets:               2_000_000_000.0,
				Goodwill:                  500_000_000.0,
				OtherIntangibles:          400_000_000.0,
				Inventory:                 700_000_000.0,
				InventoryTurnover:         3.0,
				DeferredTaxAssets:         100_000_000.0,
				StockholdersEquity:        800_000_000.0,
				EffectiveTaxRate:          0.21,
				OperatingIncome:           200_000_000.0,
				NormalizedOperatingIncome: 200_000_000.0,
				InterestExpense:           20_000_000.0,
				InterestBearingDebt:       300_000_000.0,
				TotalDebt:                 350_000_000.0,
				OperatingLeaseLiability:   50_000_000.0,
				RestructuringCharges:      30_000_000.0,
				AssetSaleGains:            15_000_000.0,
				LitigationSettlements:     20_000_000.0,
				DerivativeGainsLosses:     12_000_000.0,
				CapitalizedInterest:       8_000_000.0,
				StockBasedCompensation:    40_000_000.0,
				ContingentLiabilities:     30_000_000.0,
				WorkingCapitalAdjustment:  200_000_000.0,
				SharesOutstanding:         100_000_000,
				DilutedSharesOutstanding:  105_000_000,
				FilingPeriod:              "2025Q2",
				FilingDate:                baseFiling,
			},
		},
		// MINIMAL_SKIPS — tuned to make every C-rule + A2/A4/A5/B-rules
		// SKIP via per-Apply guards (zero raw values or below-threshold
		// ratios). Goodwill stays > 5% so A1 fires; B1 still fires on
		// non-zero revenue. Exercises projection's Fired:false-skip
		// filter for the non-firing rules.
		{
			name: "MINIMAL_SKIPS",
			seed: &entities.FinancialData{
				Ticker:                    "MINIMAL_SKIPS",
				CIK:                       "1000002",
				Revenue:                   200_000_000.0,
				TotalAssets:               1_000_000_000.0,
				Goodwill:                  200_000_000.0,
				OtherIntangibles:          5_000_000.0, // below 2% of TA → A2 skips
				Inventory:                 10_000_000.0,
				InventoryTurnover:         9.0, // above 6 → A5 skips
				DeferredTaxAssets:         0,   // A4 skips
				StockholdersEquity:        500_000_000.0,
				EffectiveTaxRate:          0.21,
				OperatingIncome:           50_000_000.0,
				NormalizedOperatingIncome: 50_000_000.0,
				InterestExpense:           5_000_000.0,
				InterestBearingDebt:       100_000_000.0,
				TotalDebt:                 120_000_000.0,
				OperatingLeaseLiability:   10_000_000.0,
				RestructuringCharges:      0, // C1 skips
				AssetSaleGains:            0, // C2 skips
				LitigationSettlements:     0, // C3 skips
				DerivativeGainsLosses:     0, // C5 skips
				CapitalizedInterest:       0, // C6 skips
				StockBasedCompensation:    0, // C4 skips
				ContingentLiabilities:     0, // B3 skips
				WorkingCapitalAdjustment:  0, // C7 skips
				SharesOutstanding:         50_000_000,
				DilutedSharesOutstanding:  52_000_000,
				FilingPeriod:              "2025Q2",
				FilingDate:                baseFiling,
			},
		},
		// FIRES_ALL_C_RULES — separate fixture that fires the full C
		// family at coverage thresholds, with no asset-side firings
		// (so basket coverage cleanly separates Restater Percentages
		// from constants).
		{
			name: "FIRES_ALL_C_RULES",
			seed: &entities.FinancialData{
				Ticker:                    "FIRES_ALL_C_RULES",
				CIK:                       "1000003",
				Revenue:                   2_000_000_000.0,
				TotalAssets:               3_000_000_000.0,
				Goodwill:                  0, // A1 skips
				OtherIntangibles:          0, // A2 skips
				Inventory:                 0,
				InventoryTurnover:         0,
				DeferredTaxAssets:         0,
				StockholdersEquity:        1_500_000_000.0,
				EffectiveTaxRate:          0.21,
				OperatingIncome:           400_000_000.0,
				NormalizedOperatingIncome: 400_000_000.0,
				InterestExpense:           40_000_000.0,
				InterestBearingDebt:       500_000_000.0,
				TotalDebt:                 600_000_000.0,
				OperatingLeaseLiability:   0,
				RestructuringCharges:      100_000_000.0,
				AssetSaleGains:            30_000_000.0,
				LitigationSettlements:     40_000_000.0,
				DerivativeGainsLosses:     -25_000_000.0, // loss branch
				CapitalizedInterest:       16_000_000.0,
				StockBasedCompensation:    80_000_000.0,
				ContingentLiabilities:     0,
				WorkingCapitalAdjustment:  0,
				SharesOutstanding:         200_000_000,
				DilutedSharesOutstanding:  210_000_000,
				FilingPeriod:              "2025Q2",
				FilingDate:                baseFiling,
			},
		},
	}

	// Stable iteration order for golden capture and assertion.
	sort.Slice(fixtures, func(i, j int) bool { return fixtures[i].name < fixtures[j].name })

	cfg := createTestConfig()
	svc, err := NewDataCleanerService(cfg, &mockAIServiceDataCleaner{}, nil)
	require.NoError(t, err, "construct DataCleanerService")

	ctx := context.Background()

	// Collect per-fixture normalized Adjustments slices into the basket map.
	basket := map[string][]normalizedAdjustment{}
	for _, fx := range fixtures {
		// Deep-copy seed before Clean so the test-local fixture object is
		// not mutated by the dispatcher (Clean copies internally too, but
		// pointer fields would still alias).
		seedCopy := *fx.seed
		res, cleanErr := svc.CleanFinancialData(ctx, &seedCopy)
		require.NoError(t, cleanErr, "%s: CleanFinancialData failed", fx.name)
		require.NotNil(t, res, "%s: CleanFinancialData returned nil result", fx.name)

		basket[fx.name] = normalizeAdjustmentsForBasket(res.Adjustments)
	}

	goldenPath := filepath.Join("testdata", "adjustments_projection_basket_golden.json")

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		raw, err := json.MarshalIndent(basket, "", "  ")
		require.NoError(t, err, "marshal golden")
		require.NoError(t, os.WriteFile(goldenPath, append(raw, '\n'), 0o644), "write golden")
		t.Logf("golden written: %s (%d fixtures)", goldenPath, len(basket))
		return
	}

	goldenBytes, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "read golden %s (run with UPDATE_GOLDEN=1 to capture)", goldenPath)

	var expected map[string][]normalizedAdjustment
	require.NoError(t, json.Unmarshal(goldenBytes, &expected), "unmarshal golden")

	// Per-fixture byte-compare on the marshaled JSON to surface drifts
	// at the field-by-field granularity in the failure output.
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			wantBytes, err := json.MarshalIndent(expected[fx.name], "", "  ")
			require.NoError(t, err)
			gotBytes, err := json.MarshalIndent(basket[fx.name], "", "  ")
			require.NoError(t, err)
			if string(wantBytes) != string(gotBytes) {
				t.Errorf("%s: Adjustments drift vs golden\n--- WANT ---\n%s\n--- GOT ---\n%s",
					fx.name, string(wantBytes), string(gotBytes))
			}
		})
	}
}

// normalizedAdjustment is the basket-parity comparison shape — strip the
// non-deterministic fields (ID, Timestamp) so the golden compares only
// the behaviorally-meaningful payload.
type normalizedAdjustment struct {
	RuleID      string                  `json:"rule_id"`
	Category    entities.RuleCategory   `json:"category"`
	Type        entities.AdjustmentType `json:"type"`
	Amount      float64                 `json:"amount"`
	FromAccount string                  `json:"from_account"`
	ToAccount   string                  `json:"to_account,omitempty"`
	Percentage  float64                 `json:"percentage,omitempty"`
	Reasoning   string                  `json:"reasoning"`
	Applied     bool                    `json:"applied"`
}

func normalizeAdjustmentsForBasket(in []entities.Adjustment) []normalizedAdjustment {
	out := make([]normalizedAdjustment, 0, len(in))
	for _, a := range in {
		out = append(out, normalizedAdjustment{
			RuleID:      a.RuleID,
			Category:    a.Category,
			Type:        a.Type,
			Amount:      a.Amount,
			FromAccount: a.FromAccount,
			ToAccount:   a.ToAccount,
			Percentage:  a.Percentage,
			Reasoning:   a.Reasoning,
			Applied:     a.Applied,
		})
	}
	// Stable order within a fixture: cleaner already emits in
	// (asset → liability → earnings) → rule-iteration order, but to be
	// safe against future map-iteration introduction, sort by RuleID
	// + Amount + FromAccount. This is OK because the projection's
	// downstream consumer (API JSON serializer) does not depend on
	// slice order.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].RuleID != out[j].RuleID {
			return out[i].RuleID < out[j].RuleID
		}
		if out[i].Amount != out[j].Amount {
			return out[i].Amount < out[j].Amount
		}
		return out[i].FromAccount < out[j].FromAccount
	})
	return out
}
