package adjustments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// productionCapitalizedInterestRule returns a CleaningRule whose ID matches
// the production rules.json entry ("capitalized_interest"). C6 has no
// production threshold gate — legacy code fires whenever CapitalizedInterest
// is positive.
func productionCapitalizedInterestRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:         "capitalized_interest",
		Name:       "Capitalized Interest Reclassification",
		Category:   entities.EarningsNormalization,
		Adjustment: entities.Reclassify,
		Enabled:    true,
		Severity:   entities.Info,
	}
}

// TestC6CapitalizedInterestAdjuster_Adjuster_Interface_Contract pins the
// DC-1 Phase 2 PR-3 Task 3.6 acceptance gate.
//
// LOAD-BEARING subtests:
//   - Fired path: Component is "InterestExpense" (NOT NormalizedOperatingIncome
//     — DIFFERENT FIELD), POSITIVE DeltaAmount, AND **EquityOffset = 0** —
//     the load-bearing special case. Phase 3's Restated() accessor must NOT
//     add C6's DeltaAmount to retained earnings.
//   - Skip path: no capitalized interest emits one Fired:false LedgerEntry.
//   - Mutation-free Apply on all paths.
func TestC6CapitalizedInterestAdjuster_Adjuster_Interface_Contract(t *testing.T) {
	// SR-1 A3: the adapter struct was deleted; call ApplyC6CapitalizedInterest
	// directly on the EarningsAdjuster (the production dispatch path).
	adj := NewEarningsAdjuster()
	require.NotNil(t, adj)

	rule := productionCapitalizedInterestRule()
	cleaningCtx := &entities.CleaningContext{}

	t.Run("fired path — Component=InterestExpense, POSITIVE DeltaAmount, EquityOffset=0 (SPECIAL CASE)", func(t *testing.T) {
		data := &entities.FinancialData{
			Ticker:              "TEST",
			Revenue:             1_000_000_000,
			CapitalizedInterest: 10_000_000,
			InterestExpense:     50_000_000,
		}

		out, err := adj.ApplyC6CapitalizedInterest(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		assert.Empty(t, out.Overlays, "C6 is a Restater — must NOT emit OverlaySpecs")
		assert.Empty(t, out.Flags, "C6 emits no significance Flag")

		entry := out.LedgerEntries[0]
		assert.True(t, entry.Fired)
		assert.Equal(t, adjusterIDC6CapitalizedInterest, entry.AdjusterID)
		// DIFFERENT FIELD — C6 targets InterestExpense, not NormalizedOperatingIncome.
		assert.Equal(t, "InterestExpense", entry.Component,
			"C6 Component must be 'InterestExpense' (NOT 'NormalizedOperatingIncome')")
		// POSITIVE DeltaAmount — capitalized interest added BACK to interest expense.
		assert.InDelta(t, 10_000_000.0, entry.DeltaAmount, 1e-6,
			"DeltaAmount must equal +CapitalizedInterest (POSITIVE — add-back)")

		// LOAD-BEARING SPECIAL CASE:
		assert.Zero(t, entry.EquityOffset,
			"C6 EquityOffset MUST be 0 — capitalized interest is a reclassification "+
				"BETWEEN income-statement lines (operating expense → interest expense), "+
				"NOT an equity-flowing event. Phase 3 Restated() must NOT add C6 "+
				"DeltaAmount to retained earnings.")

		assert.Zero(t, entry.TaxShieldDTA, "Q2 deferral: C6 TaxShieldDTA stays 0")

		// Mutation-free.
		assert.Equal(t, 50_000_000.0, data.InterestExpense,
			"Apply must NOT mutate data.InterestExpense")
		assert.Equal(t, 10_000_000.0, data.CapitalizedInterest)
	})

	t.Run("skip path — no capitalized interest emits one Fired:false LedgerEntry", func(t *testing.T) {
		data := &entities.FinancialData{
			Ticker:              "TEST",
			Revenue:             1_000_000_000,
			CapitalizedInterest: 0,
			InterestExpense:     50_000_000,
		}

		out, err := adj.ApplyC6CapitalizedInterest(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, "No capitalized interest to adjust", entry.SkipReason)
		assert.Empty(t, entry.SkipMetrics, "C6 skip path has no metric to surface")
		// Mutation-free.
		assert.Equal(t, 50_000_000.0, data.InterestExpense)
	})

	t.Run("skip path — negative capitalized interest treated as skip (legacy <= 0 guard)", func(t *testing.T) {
		// Legacy guard is `<= 0` not `== 0`; verify a negative value (data
		// quality bug) follows the skip branch identically.
		data := &entities.FinancialData{
			Ticker:              "TEST",
			Revenue:             1_000_000_000,
			CapitalizedInterest: -5_000_000,
			InterestExpense:     50_000_000,
		}

		out, err := adj.ApplyC6CapitalizedInterest(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, 50_000_000.0, data.InterestExpense)
	})
}

// TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC6Emission pins the
// dispatcher's contract for the capitalized_interest rule — verifying the
// dual-write `data.InterestExpense += amount` happens AFTER Apply and the
// native LedgerEntries / RuleID surface for the cleaner orchestrator drain.
func TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC6Emission(t *testing.T) {
	ea := NewEarningsAdjuster()
	data := &entities.FinancialData{
		Ticker:              "TEST",
		Revenue:             1_000_000_000,
		CapitalizedInterest: 10_000_000,
		InterestExpense:     50_000_000,
	}
	rules := []*entities.CleaningRule{productionCapitalizedInterestRule()}

	result := ea.ProcessEarningsAdjustments(context.Background(), data, rules, &entities.CleaningContext{})
	require.NotNil(t, result)

	// DC-1 Phase 5 P5-C4: legacy *AdjustmentResult fields deleted; the fired
	// magnitude / accounts / Reclassify type are asserted via the native
	// Restater entry below + the projection metadata table (C6 → Reclassify,
	// covered by the basket-parity golden).

	require.Len(t, result.NativeLedgerEntries, 1)
	assert.Empty(t, result.NativeOverlays)
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["capitalized_interest"])

	nativeEntry := result.NativeLedgerEntries[0]
	assert.True(t, nativeEntry.Fired)
	assert.Equal(t, adjusterIDC6CapitalizedInterest, nativeEntry.AdjusterID)
	assert.Equal(t, "InterestExpense", nativeEntry.Component)
	assert.InDelta(t, 10_000_000.0, nativeEntry.DeltaAmount, 1e-6)
	// LOAD-BEARING regression pin on the dispatcher path too.
	assert.Zero(t, nativeEntry.EquityOffset,
		"C6 native LedgerEntry EquityOffset MUST be 0 (special case — see Apply test)")

	// Dual-write: data.InterestExpense += 10M → 60M.
	assert.InDelta(t, 60_000_000.0, data.InterestExpense, 1e-6,
		"dispatcher must perform `data.InterestExpense += CapitalizedInterest`")
	assert.Equal(t, 10_000_000.0, data.CapitalizedInterest,
		"dispatcher must NOT mutate data.CapitalizedInterest")
}

// TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC6SkipPath pins
// no-mutation on the no-capitalized-interest skip path.
func TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC6SkipPath(t *testing.T) {
	ea := NewEarningsAdjuster()
	data := &entities.FinancialData{
		Ticker:              "TEST",
		Revenue:             1_000_000_000,
		CapitalizedInterest: 0,
		InterestExpense:     50_000_000,
	}
	rules := []*entities.CleaningRule{productionCapitalizedInterestRule()}

	result := ea.ProcessEarningsAdjustments(context.Background(), data, rules, &entities.CleaningContext{})
	require.NotNil(t, result)

	// DC-1 Phase 5 P5-C4: skip contract asserted natively — no fired entry.
	require.Len(t, result.NativeLedgerEntries, 1)
	assert.False(t, result.NativeLedgerEntries[0].Fired)
	assert.True(t, result.NativelyEmittedRuleIDs["capitalized_interest"])

	// No mutation on skip path.
	assert.Equal(t, 50_000_000.0, data.InterestExpense)
}
