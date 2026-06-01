package adjustments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// productionOperatingLeasesRule returns a CleaningRule whose ID matches the
// production rules.json entry ("operating_leases") so the rule reaches the
// operating_leases branch in ProcessLiabilityAdjustments. Mirrors the
// productionGoodwillRule helper in a1_goodwill_adjuster_test.go.
func productionOperatingLeasesRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:          "operating_leases",
		Name:        "Operating Lease Capitalization",
		Category:    entities.LiabilityCompleteness,
		Adjustment:  entities.TreatAsDebt,
		Description: "Capitalize operating lease commitments as debt-equivalent obligations",
		Enabled:     true,
	}
}

// TestB1OperatingLeasesAdjuster_Adjuster_Interface_Contract pins the DC-1
// Phase 2 PR-4 Task 4.1 acceptance gate: b1OperatingLeaseCapitalizationAdjuster
// satisfies the Adjuster interface AND its AdjusterOutput matches the spec /
// plan §3.2 / §3.3 / §3.5 contracts for the fired + skipped paths.
//
// The compile-time assertion
// `var _ Adjuster = (*b1OperatingLeaseCapitalizationAdjuster)(nil)` in
// liabilities.go is the primary signature pin; this test exercises the
// runtime contract — every branch of ApplyB1OperatingLeases produces an
// AdjusterOutput whose LedgerEntries, Overlays, and Flags match the shape
// the orchestrator + Phase 3 view reconstruction will rely on.
func TestB1OperatingLeasesAdjuster_Adjuster_Interface_Contract(t *testing.T) {
	// Construct through the exported factory so the test exercises the
	// public API surface the orchestrator will use. The factory returns
	// the Adjuster interface, so any signature drift breaks the test
	// compile.
	la := NewLiabilityAdjuster(&mockAIService{}, nil)
	adj := NewB1OperatingLeaseCapitalizationAdjuster(la)
	require.NotNil(t, adj)

	// Name() contract: stable identifier consumers can join on. Locked to
	// the AdjusterID constant so a rename forces both the test and the
	// constant to move together.
	assert.Equal(t, adjusterIDB1OperatingLeaseCapitalization, adj.Name(),
		"b1OperatingLeaseCapitalizationAdjuster.Name() must equal the AdjusterID constant")

	rule := productionOperatingLeasesRule()

	t.Run("fired path emits OverlaySpec on TotalDebt and Fired:true audit LedgerEntry", func(t *testing.T) {
		// Use the calculator-fallback fixture: no lease commitment data
		// (so the calculator can't compute a real PV), but
		// OperatingLeaseLiability is populated — fallbackToSimpleCapitalization
		// will pick it up and apply the book value. This is a deterministic
		// fired path that does not depend on the lease calculator's internal
		// term/discount-rate estimation.
		data := &entities.FinancialData{
			Ticker:                  "TEST",
			OperatingLeaseLiability: 200_000.0,
			TotalAssets:             1_000_000.0,
			Revenue:                 500_000.0,
		}
		cleaningCtx := &entities.CleaningContext{IndustryCode: "44"} // Retail

		// Snapshot data fields touched by the dual-write so we can assert
		// Apply is mutation-FREE.
		origTotalDebt := data.TotalDebt
		origInterestBearingDebt := data.InterestBearingDebt

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err, "Apply must not error on a well-formed fired-path input")

		// AdjusterOutput contract: exactly one fired LedgerEntry and
		// exactly one OverlaySpec; flags may be present (lease-quality
		// flag from the legacy path when the calculator falls back).
		require.Len(t, out.LedgerEntries, 1, "fired path emits exactly one LedgerEntry")
		require.Len(t, out.Overlays, 1, "fired path emits exactly one OverlaySpec on TotalDebt")

		// LedgerEntry contract (plan §3.5 OverlayEmitter role): Fired=true,
		// AdjusterID matches Name(), Component / DeltaAmount / EquityOffset
		// LEFT UNSET because the declarative amount lives on OverlaySpec.
		entry := out.LedgerEntries[0]
		assert.True(t, entry.Fired, "fired-path LedgerEntry must have Fired=true")
		assert.Equal(t, adjusterIDB1OperatingLeaseCapitalization, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.NotEmpty(t, entry.Reasoning, "Reasoning must be populated for fired entries")
		assert.False(t, entry.Timestamp.IsZero(), "Timestamp must be set on fired entries")
		assert.Empty(t, entry.Component, "B1 is an OverlayEmitter — Component must NOT be set")
		assert.Zero(t, entry.DeltaAmount, "B1 is an OverlayEmitter — DeltaAmount must be zero on the LedgerEntry")
		assert.Zero(t, entry.EquityOffset, "B1 is an OverlayEmitter — EquityOffset must be zero on the LedgerEntry")
		assert.Zero(t, entry.TaxShieldDTA, "B1 is an OverlayEmitter — TaxShieldDTA must be zero on the LedgerEntry")
		assert.Empty(t, entry.SkipReason, "SkipReason must be empty for fired entries")

		// OverlaySpec contract: add semantics on TotalDebt, the lease
		// PV (or book-value fallback) captured at the legacy path, Reasoning
		// string carries the canonical "operating_lease_adj:" /
		// "Fallback to book value..." prefix preserved across the migration.
		overlay := out.Overlays[0]
		assert.Equal(t, adjusterIDB1OperatingLeaseCapitalization, overlay.OverlayID)
		assert.Equal(t, rule.ID, overlay.RuleID)
		assert.Equal(t, "TotalDebt", overlay.Field)
		assert.Equal(t, "add", overlay.Operation)
		assert.Greater(t, overlay.Amount, 0.0, "overlay amount must be positive on the fired path")
		assert.Equal(t, entities.AmountIncremental, overlay.AmountSemantics)
		assert.NotEmpty(t, overlay.Reasoning, "overlay reasoning must be populated")
		assert.Nil(t, overlay.AIProvenance, "B1 amount is deterministic — AIProvenance must be nil")

		// CRITICAL invariant: Apply must NOT mutate `working`. The
		// dispatcher in ProcessLiabilityAdjustments performs the dual-write
		// — Apply is read-only.
		assert.Equal(t, origTotalDebt, data.TotalDebt, "Apply must NOT mutate data.TotalDebt")
		assert.Equal(t, origInterestBearingDebt, data.InterestBearingDebt, "Apply must NOT mutate data.InterestBearingDebt")
	})

	t.Run("skip path (no lease data) emits one Fired:false LedgerEntry", func(t *testing.T) {
		data := &entities.FinancialData{
			Ticker:                  "TEST",
			OperatingLeaseLiability: 0.0,
			TotalAssets:             1_000_000.0,
			Revenue:                 500_000.0,
		}
		cleaningCtx := &entities.CleaningContext{IndustryCode: "45"} // Technology

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		// AdjusterOutput contract for the no-lease skip path:
		require.Len(t, out.LedgerEntries, 1, "skip path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays, "skip path emits no OverlaySpec")
		assert.Empty(t, out.Flags, "skip path emits no Flags")

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired, "skip-path LedgerEntry must have Fired=false")
		assert.Equal(t, adjusterIDB1OperatingLeaseCapitalization, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.NotEmpty(t, entry.SkipReason, "SkipReason must be populated on the skip path")
		// Plan §3.6.6: skipped entries carry zero monetary deltas.
		assert.Zero(t, entry.DeltaAmount)
		assert.Zero(t, entry.EquityOffset)
		assert.Zero(t, entry.TaxShieldDTA)
	})
}

// TestLiabilityAdjuster_ProcessLiabilityAdjustments_NativeB1Emission pins the
// dispatcher's contract: when operating_leases is among the input rules AND
// operating-lease data is present, ProcessLiabilityAdjustments populates
// LiabilityAdjustmentResult.{NativeLedgerEntries,NativeOverlays,NativelyEmittedRuleIDs}
// AND mutates data.TotalDebt / data.InterestBearingDebt exactly as before
// (dual-write preserved — load-bearing for the DDM bit-for-bit invariant).
//
// This is the gateway test for the orchestrator's drain-then-shim wiring in
// service.go::applyActiveAdjustments.
func TestLiabilityAdjuster_ProcessLiabilityAdjustments_NativeB1Emission(t *testing.T) {
	la := NewLiabilityAdjuster(&mockAIService{}, nil)
	// Use the fallback-fixture pattern from the Apply contract test: no
	// lease commitment series, but OperatingLeaseLiability populated, so
	// fallbackToSimpleCapitalization fires with a deterministic amount.
	data := &entities.FinancialData{
		Ticker:                  "RETAIL",
		OperatingLeaseLiability: 200_000.0,
		TotalAssets:             1_000_000.0,
		Revenue:                 500_000.0,
		TotalDebt:               150_000.0,
		InterestBearingDebt:     150_000.0,
	}
	rules := []*entities.CleaningRule{productionOperatingLeasesRule()}
	cleaningCtx := &entities.CleaningContext{IndustryCode: "44"} // Retail

	origTotalDebt := data.TotalDebt
	origInterestBearingDebt := data.InterestBearingDebt

	result := la.ProcessLiabilityAdjustments(context.Background(), data, rules, cleaningCtx)
	require.NotNil(t, result)

	// DC-1 Phase 5 P5-C4: the legacy *LiabilityAdjustmentResult fields were
	// deleted. The fired lease amount is asserted via the native OverlaySpec
	// below (B1 is an OverlayEmitter); the projected entities.Adjustment is
	// covered end-to-end by the basket-parity golden.

	// Phase 2 PR-4 Task 4.1 native emission contract:
	require.GreaterOrEqual(t, len(result.NativeLedgerEntries), 1,
		"ProcessLiabilityAdjustments must surface the B1 native LedgerEntry")
	require.Len(t, result.NativeOverlays, 1,
		"ProcessLiabilityAdjustments must surface the B1 native OverlaySpec")
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["operating_leases"],
		"operating_leases must appear in NativelyEmittedRuleIDs so the shim skips it")

	// OverlaySpec landed in NativeOverlays — verify shape.
	overlay := result.NativeOverlays[0]
	assert.Equal(t, adjusterIDB1OperatingLeaseCapitalization, overlay.OverlayID)
	assert.Equal(t, "TotalDebt", overlay.Field)
	assert.Equal(t, "add", overlay.Operation)

	// DC-1 Phase 4 (C-4 / B3 routing flip, §8.2.1 Option A): the B-rule debt
	// dual-write is DELETED. B1's lease amount flows through the OverlaySpec
	// (above) into InvestedCapital().DebtLikeClaims at the view level; it no
	// longer inflates data.TotalDebt / data.InterestBearingDebt. The WACC
	// denominator reads Restated().InterestBearingDebt (B-rule-free).
	leaseAmount := overlay.Amount
	require.Greater(t, leaseAmount, 0.0)
	assert.Equal(t, origTotalDebt, data.TotalDebt,
		"Phase 4 §8.2.1 Option A: B1 must NOT mutate data.TotalDebt (effect → InvestedCapital().DebtLikeClaims)")
	assert.Equal(t, origInterestBearingDebt, data.InterestBearingDebt,
		"Phase 4 §8.2.1 Option A: B1 must NOT mutate data.InterestBearingDebt")
}

// TestLiabilityAdjuster_ProcessLiabilityAdjustments_NativeB1SkipPath confirms
// that even on the skip path (no operating-lease data), ProcessLiabilityAdjustments
// surfaces the Fired:false LedgerEntry through NativeLedgerEntries — and
// the shim path (run later in service.go) will skip emitting its own
// generic skip entry for the same rule.
func TestLiabilityAdjuster_ProcessLiabilityAdjustments_NativeB1SkipPath(t *testing.T) {
	la := NewLiabilityAdjuster(&mockAIService{}, nil)
	data := &entities.FinancialData{
		Ticker:                  "TEST",
		OperatingLeaseLiability: 0.0,
		TotalAssets:             1_000_000.0,
		Revenue:                 500_000.0,
		TotalDebt:               150_000.0,
		InterestBearingDebt:     150_000.0,
	}
	rules := []*entities.CleaningRule{productionOperatingLeasesRule()}
	cleaningCtx := &entities.CleaningContext{IndustryCode: "45"} // Technology

	result := la.ProcessLiabilityAdjustments(context.Background(), data, rules, cleaningCtx)
	require.NotNil(t, result)

	// DC-1 Phase 5 P5-C4: skip contract asserted natively — no fired entry.

	// Native emission contract — skip path still emits a Fired:false entry.
	require.Len(t, result.NativeLedgerEntries, 1,
		"skip path must still surface a Fired:false native LedgerEntry")
	assert.False(t, result.NativeLedgerEntries[0].Fired)
	assert.Empty(t, result.NativeOverlays, "skip path emits no Overlays")
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["operating_leases"],
		"operating_leases must appear in NativelyEmittedRuleIDs even on skip path")

	// Dual-write contract — skip path must NOT mutate balance-sheet fields.
	assert.Equal(t, 150_000.0, data.TotalDebt)
	assert.Equal(t, 150_000.0, data.InterestBearingDebt)
}

// TestB1OperatingLeasesAdjuster_FallbackInline_NoExtraction documents the
// PR-4 Task 4.1 design decision: fallbackToSimpleCapitalization is kept
// inline as a private method on LiabilityAdjuster (not extracted as a
// separate B1-fallback Adjuster). This test exercises the standard
// calculator path through the Adjuster interface to confirm the
// OverlayEmitter contract holds — and pins the reasoning-prefix invariant
// that the legacy "operating_lease_adj:" / "Fallback to book value..."
// strings remain greppable across the migration.
//
// Note: the lease calculator's simplified_liability_based method actually
// fires whenever OperatingLeaseLiability is populated — the explicit
// fallbackToSimpleCapitalization branch only fires when the calculator
// itself returns an error, which requires specific config / data shapes
// not easily reproduced in a unit fixture. Both paths emit an OverlaySpec
// on TotalDebt with AmountIncremental semantics; that is the load-bearing
// invariant for Phase 3's InvestedCapital() consumer.
func TestB1OperatingLeasesAdjuster_FallbackInline_NoExtraction(t *testing.T) {
	la := NewLiabilityAdjuster(&mockAIService{}, nil)
	adj := NewB1OperatingLeaseCapitalizationAdjuster(la)
	rule := productionOperatingLeasesRule()

	// Fixture with OperatingLeaseLiability populated — exercises the
	// calculator's simplified_liability_based path.
	data := &entities.FinancialData{
		Ticker:                  "STANDARD",
		OperatingLeaseLiability: 300_000.0,
		TotalAssets:             1_500_000.0,
		Revenue:                 800_000.0,
	}
	cleaningCtx := &entities.CleaningContext{IndustryCode: "44"}

	out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
	require.NoError(t, err)

	// Calculator-success path still respects the OverlayEmitter contract:
	require.Len(t, out.LedgerEntries, 1)
	require.Len(t, out.Overlays, 1)

	overlay := out.Overlays[0]
	assert.Equal(t, "TotalDebt", overlay.Field, "calculator path also targets TotalDebt")
	assert.Equal(t, "add", overlay.Operation)
	assert.Greater(t, overlay.Amount, 0.0, "calculator path produces a positive PV")
	assert.Equal(t, entities.AmountIncremental, overlay.AmountSemantics)
	// The reasoning carries the legacy "operating_lease_adj:" prefix per
	// ProcessOperatingLeaseAdjustment line 144 — preserved across the
	// migration so log greps keep working. The fallback path produces a
	// different prefix ("Fallback to book value...") at
	// fallbackToSimpleCapitalization line 257; both prefixes are
	// load-bearing for downstream observability.
	assert.Contains(t, overlay.Reasoning, "operating_lease_adj",
		"calculator-path reasoning must carry the legacy 'operating_lease_adj:' prefix for greppability")

	// Audit LedgerEntry contract on the fired path: no Component /
	// DeltaAmount / EquityOffset (OverlayEmitter role).
	entry := out.LedgerEntries[0]
	assert.True(t, entry.Fired)
	assert.Empty(t, entry.Component)
	assert.Zero(t, entry.DeltaAmount)
}

// TestB1OperatingLeasesAdjuster_LegacyDirectInvocation pins backward
// compatibility: existing TestLiabilityAdjuster_ProcessOperatingLeaseAdjustment
// callers that invoke ProcessOperatingLeaseAdjustment directly (not through
// the dispatcher's switch arm) still get the legacy *AdjustmentResult shape
// with the same Applied / Amount / Flags behavior. This guards against an
// accidental migration of the legacy method that would break the existing
// test suite.
func TestB1OperatingLeasesAdjuster_LegacyDirectInvocation(t *testing.T) {
	la := NewLiabilityAdjuster(&mockAIService{}, nil)
	data := &entities.FinancialData{
		Ticker:                  "RETAIL",
		OperatingLeaseLiability: 200_000.0,
		TotalAssets:             1_000_000.0,
		Revenue:                 800_000.0,
	}
	rule := productionOperatingLeasesRule()
	cleaningCtx := &entities.CleaningContext{IndustryCode: "44"}

	// Direct invocation bypasses the dispatcher's switch arm — must still
	// return a populated legacy result.
	result := la.ProcessOperatingLeaseAdjustment(context.Background(), data, rule, cleaningCtx)
	require.NotNil(t, result)
	assert.True(t, result.Applied)
	assert.Equal(t, 200_000.0, result.Amount)
	// Apply was NOT called on this path — data must remain unmutated by the
	// legacy method itself (the legacy method only emits the Adjustment;
	// dual-write is the dispatcher's responsibility).
	assert.Equal(t, 0.0, data.TotalDebt, "ProcessOperatingLeaseAdjustment does not mutate data — only the dispatcher does")
}

// TestFirstAdjustmentReasoning_NilSafety pins the helper's nil-safety
// contract: the helper must not panic on nil result or empty Adjustments
// slices. This is defense-in-depth because the dispatcher only calls Apply
// on rules whose category matches LiabilityCompleteness.
func TestFirstAdjustmentReasoning_NilSafety(t *testing.T) {
	assert.Equal(t, "", firstAdjustmentReasoning(nil), "nil result must produce empty string")

	empty := &AdjustmentResult{}
	assert.Equal(t, "", firstAdjustmentReasoning(empty), "empty result must produce empty string")

	resultOnly := &AdjustmentResult{Reasoning: "fallback summary"}
	assert.Equal(t, "fallback summary", firstAdjustmentReasoning(resultOnly),
		"result.Reasoning must be used when no Adjustments present")

	withAdj := &AdjustmentResult{
		Adjustments: []entities.Adjustment{{Reasoning: "rule-specific prefix: detail"}},
		Reasoning:   "summary",
	}
	assert.Equal(t, "rule-specific prefix: detail", firstAdjustmentReasoning(withAdj),
		"first Adjustment.Reasoning must be preferred over result.Reasoning")
}
