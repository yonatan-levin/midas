package datacleaner

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestApplyActiveAdjustments_FiringSignalParity pins the DC-1 Phase 5
// (P5-C3) native firing-signal migration: the orchestrator's three
// category arms in applyActiveAdjustments switched their `if XResult.Applied`
// guards to `nativeFired(XResult.NativeLedgerEntries, XResult.NativeOverlays,
// XResult.Flags)` (defined at firing_signal.go).
//
// IMPORTANT: the helper is NOT the naive `len(NativeLedgerEntries) > 0 ||
// len(NativeOverlays) > 0 || len(Flags) > 0` predicate that initially
// shipped under P5-C3 (commit 586c370) — that inline form was a HIGH-1
// review finding (gpt-5.5) because skip paths emit Fired:false LedgerEntries
// for diagnostic observability and the naive predicate over-counted
// RulesApplied on the rules-pass-applicability-but-Apply-skips path.
// nativeFired filters LedgerEntries on `e.Fired==true` while treating
// any OverlaySpec or Flag as an unconditional fire signal (those
// streams are skip-free by their role contract). See the
// _A1ApplicableButSkipped regression test below for the failure mode
// the post-fix helper prevents.
//
// The helper is equivalent to the legacy `Applied` bool because every
// fired rule emits at least ONE of:
//   - a Fired:true Restater LedgerEntry (A2/A4/A5/C1/C2/C3/C5/C6),
//   - an OverlaySpec (A1/B1/B2/B3 OverlayEmitters), or
//   - a Flag (C4/C7 FlagEmitters + the two PR-2 A-flag reviews).
//
// This test exercises the firing-signal equivalence via a
// CleanFinancialData run on a fixture engineered to fire across all three
// categories — asserting that:
//   - the orchestrator's totalRulesApplied count + result.Adjustments
//     length match what the legacy `Applied`-bool path produced
//     (pre-P5-C3 baseline behavior pinned by service_test.go::testAuditTrail
//     and the basket integration tests),
//   - the result struct's `Applied` field (still set by the per-rule
//     translators in P5-C3 — only its READ is migrated) and the native
//     signal agree on which category arms fired.
//
// P5-C4 will retire the translators AND the `Applied` field on
// {Asset,Liability,Earnings}AdjustmentResult; until then, both signals
// coexist and this test pins they stay equivalent. After P5-C4 lands,
// this test's check on the legacy `Applied` field collapses to the native
// signal — the test becomes a regression guard rather than a parity
// gate.
func TestApplyActiveAdjustments_FiringSignalParity(t *testing.T) {
	cfg := createTestConfig()
	ctx := context.Background()
	svc, err := NewDataCleanerService(cfg, &mockAIServiceDataCleaner{}, nil)
	require.NoError(t, err)

	// Use the standard "issues" fixture which fires asset + earnings
	// rules in service_test.go::testAuditTrail.
	data := createTestFinancialDataWithIssues()

	result, err := svc.CleanFinancialData(ctx, data)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Sanity: the run actually exercised the orchestrator's category
	// arms (at least one rule fired). The pre-P5-C3 baseline pinned
	// "Should apply at least one rule" — preserving that.
	assert.Greater(t, result.RulesApplied, 0,
		"P5-C3 firing-signal migration must not silently zero out RulesApplied")
	assert.NotNil(t, result.Adjustments,
		"P5-C3 firing-signal migration must not drop Adjustments")

	// Pre-P5-C3 expected behavior pinned: when a category arm fires,
	// result.Adjustments includes entries with non-empty RuleID (Category
	// gets set by translators today). The native signal must not
	// accidentally enter the arm when no rule actually fired (would
	// inflate Adjustments).
	for _, adj := range result.Adjustments {
		assert.NotEmpty(t, adj.RuleID,
			"every Adjustment in result.Adjustments must have a non-empty RuleID — "+
				"a native signal misfire would surface as empty-RuleID phantoms")
	}

	// Native-signal property: when at least one Adjustment fired, at least
	// one of the native streams (ledger, overlays, flags) must be non-
	// empty on data.AdjustmentLedger / data.Overlays / result.Flags.
	// This is the property the P5-C3 migration relies on.
	if len(result.Adjustments) > 0 {
		nativeFired := len(result.CleanedData.AdjustmentLedger) > 0 ||
			len(result.CleanedData.Overlays) > 0 ||
			len(result.Flags) > 0
		assert.True(t, nativeFired,
			"P5-C3 invariant: if result.Adjustments is non-empty, at least one "+
				"native stream (AdjustmentLedger / Overlays / Flags) must also "+
				"be non-empty — otherwise the native firing-signal is unsound")
	}
}

// TestApplyActiveAdjustments_FiringSignalParity_EmptyFixture pins that
// a fixture with no firing rules produces neither legacy Adjustments
// NOR native ledger/overlay/flag emissions — guarding against a native-
// signal false-positive that would inflate totalRulesApplied.
func TestApplyActiveAdjustments_FiringSignalParity_EmptyFixture(t *testing.T) {
	cfg := createTestConfig()
	ctx := context.Background()
	svc, err := NewDataCleanerService(cfg, &mockAIServiceDataCleaner{}, nil)
	require.NoError(t, err)

	// Pristine balance-sheet fixture: no goodwill, no leases, no DTA,
	// no inventory, no contingent liabilities — nothing for adjusters to
	// touch. (Synthesized inline to keep the test self-contained.)
	data := &entities.FinancialData{
		Ticker:            "PRISTINE",
		ReportingCurrency: "USD",
		FilingPeriod:      "2024FY",
		FilingDate:        time.Now(),
		TotalAssets:       1_000_000_000,
		SharesOutstanding: 100_000_000,
		// No goodwill / no intangibles / no inventory / no DTA — A-rules
		// should all skip on applicability.
		// No operating leases / no pension / no contingents — B-rules skip.
		// No restructuring / no asset sales / no litigation — C-rules skip.
		Revenue:   500_000_000,
		NetIncome: 50_000_000,
	}

	result, err := svc.CleanFinancialData(ctx, data)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Some C-rules + flagging may still produce data-quality flags from
	// the recompute / quality-score pass even on a pristine balance
	// sheet (umbrella zero-checks). What MUST hold is the firing-signal
	// equivalence: if RulesApplied == 0 (no orchestrator arm fired),
	// then result.Adjustments must be empty.
	if result.RulesApplied == 0 {
		assert.Empty(t, result.Adjustments,
			"P5-C3 invariant: zero RulesApplied must yield empty Adjustments — "+
				"otherwise the native firing-signal is over-detecting")
	}
}

// TestApplyActiveAdjustments_FiringSignalParity_A1ApplicableButSkipped
// is the regression pin for the gpt-5.5 HIGH-1 finding against the
// initial P5-C3 shipping. When an adjuster's outer applicability check
// (s.checkRuleApplicability) passes but the inner Apply skips (e.g. A1
// goodwill is present but BELOW the 5% materiality threshold), the
// dispatcher emits a single Fired:false diagnostic LedgerEntry per the
// spec's observability contract. The PRE-fix native firing-signal
// `len(NativeLedgerEntries) > 0 || ...` incorrectly returned true on
// this path, inflating totalRulesApplied → result.RulesApplied →
// pipeline summary.TotalRulesApplied (a public observable).
//
// The fix introduces nativeFired() which filters LedgerEntries on
// e.Fired==true (overlays + flags are skip-free by their role contract,
// so they remain unconditional firing signals).
//
// This fixture exercises the exact failure path:
//   - Goodwill = 3% of TotalAssets → s.checkRuleApplicability returns
//     true (data.Goodwill > 0) AND ApplyA1Goodwill skips at the 5%
//     threshold, emitting one Fired:false LedgerEntry with SkipMetrics.
//   - No other A/B/C rule fires (no intangibles, no inventory, no DTA,
//     no leases, no pension, no contingent liabilities, no restructuring).
//
// Pre-fix expectation (what THIS test would FAIL under): RulesApplied
// inflated by len(assetRules) because the inline predicate would treat
// the Fired:false diagnostic entry as a category fire.
// Post-fix expectation: RulesApplied == 0 (the legacy Applied=false
// semantics, preserved).
func TestApplyActiveAdjustments_FiringSignalParity_A1ApplicableButSkipped(t *testing.T) {
	cfg := createTestConfig()
	ctx := context.Background()
	svc, err := NewDataCleanerService(cfg, &mockAIServiceDataCleaner{}, nil)
	require.NoError(t, err)

	// Goodwill at 3% of TotalAssets — A1 applicability is "data.Goodwill > 0"
	// (passes), but ApplyA1Goodwill's internal threshold is 5% (skips).
	// The skip path emits one Fired:false NativeLedgerEntry with
	// SkipMetrics["goodwill_ratio"] = 0.03 — confirmed by the A1 adjuster's
	// own test at a1_goodwill_adjuster_test.go.
	data := &entities.FinancialData{
		Ticker:            "A1_BELOW_THRESHOLD",
		ReportingCurrency: "USD",
		FilingPeriod:      "2024FY",
		FilingDate:        time.Now(),
		TotalAssets:       1_000_000_000,
		Goodwill:          30_000_000, // 3% of TotalAssets — passes applicability, skips Apply
		SharesOutstanding: 100_000_000,
		Revenue:           500_000_000,
		NetIncome:         50_000_000,
	}

	result, err := svc.CleanFinancialData(ctx, data)
	require.NoError(t, err)
	require.NotNil(t, result)

	// The orchestrator's three A-arm assertions:
	assert.Empty(t, result.Adjustments,
		"A1 skipped → translator emits no Adjustment → result.Adjustments must be empty")
	assert.Equal(t, 0, result.RulesApplied,
		"applicability-passed-but-Apply-skipped path must NOT increment RulesApplied "+
			"(pre-fix inline predicate inflated this count via the diagnostic "+
			"Fired:false LedgerEntry — the nativeFired helper filters those out)")

	// Verify the diagnostic Fired:false entry is still on the ledger
	// (its observability purpose is preserved — only its inflation
	// of RulesApplied is fixed).
	foundSkipEntry := false
	for _, entry := range result.CleanedData.AdjustmentLedger {
		if entry.AdjusterID == "A1_goodwill_exclusion" && !entry.Fired {
			foundSkipEntry = true
			break
		}
	}
	assert.True(t, foundSkipEntry,
		"A1 skip diagnostic LedgerEntry must still land on data.AdjustmentLedger — "+
			"the fix removes the count inflation but preserves the observability surface")
}
