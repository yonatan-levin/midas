package adjustments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// productionPensionRule returns a CleaningRule whose ID matches the
// production rules.json entry ("pension_obligations") so the rule reaches
// the pension_obligations branch in ProcessLiabilityAdjustments. Mirrors
// productionOperatingLeasesRule / productionGoodwillRule.
func productionPensionRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:          "pension_obligations",
		Name:        "Pension / OPEB Underfunding",
		Category:    entities.LiabilityCompleteness,
		Adjustment:  entities.TreatAsDebt,
		Description: "Add under-funded pension and OPEB obligations to debt base per B2 rule",
		Enabled:     true,
	}
}

// TestB2PensionUnderfundingAdjuster_Adjuster_Interface_Contract pins the
// DC-1 Phase 2 PR-4 Task 4.2 acceptance gate: b2PensionUnderfundingAdjuster
// satisfies the Adjuster interface AND its AdjusterOutput matches the
// spec / plan §3.5 contracts for the fired + skipped paths.
//
// The compile-time assertion
// `var _ Adjuster = (*b2PensionUnderfundingAdjuster)(nil)` in liabilities.go
// is the primary signature pin; this test exercises the runtime contract.
func TestB2PensionUnderfundingAdjuster_Adjuster_Interface_Contract(t *testing.T) {
	// Construct through the exported factory so the test exercises the
	// public API surface the orchestrator will use.
	la := NewLiabilityAdjuster(&mockAIService{}, nil)
	adj := NewB2PensionUnderfundingAdjuster(la)
	require.NotNil(t, adj)

	// Name() contract: stable identifier consumers can join on. Locked to
	// the AdjusterID constant so a rename forces both the test and the
	// constant to move together.
	assert.Equal(t, adjusterIDB2PensionUnderfunding, adj.Name(),
		"b2PensionUnderfundingAdjuster.Name() must equal the AdjusterID constant")

	rule := productionPensionRule()

	t.Run("fired path emits OverlaySpec on TotalDebt and Fired:true audit LedgerEntry", func(t *testing.T) {
		// PBO > PlanAssets so underfunding = 200_000; plus OPEB = 50_000;
		// total pension obligation = 250_000. With Revenue = 1_000_000 →
		// ratio = 25% which is above the "critical" threshold (15%) so a
		// significance flag also fires.
		data := &entities.FinancialData{
			Ticker:                     "UTILITY",
			ProjectedBenefitObligation: 500_000.0,
			PensionPlanAssets:          300_000.0,
			OPEBLiability:              50_000.0,
			TotalAssets:                2_000_000.0,
			Revenue:                    1_000_000.0,
		}
		cleaningCtx := &entities.CleaningContext{IndustryCode: "22"} // Utilities

		// Snapshot data fields touched by the dual-write so we can assert
		// Apply is mutation-FREE.
		origTotalDebt := data.TotalDebt
		origInterestBearingDebt := data.InterestBearingDebt

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err, "Apply must not error on a well-formed fired-path input")

		// AdjusterOutput contract: exactly one fired LedgerEntry, exactly
		// one OverlaySpec, exactly one Flag (because pensionRatio>=15%).
		require.Len(t, out.LedgerEntries, 1, "fired path emits exactly one LedgerEntry")
		require.Len(t, out.Overlays, 1, "fired path emits exactly one OverlaySpec on TotalDebt")
		require.Len(t, out.Flags, 1, "fired path with pensionRatio>=15%% emits one significance Flag")

		// LedgerEntry contract (plan §3.5 OverlayEmitter role): Fired=true,
		// AdjusterID matches Name(), Component / DeltaAmount / EquityOffset
		// LEFT UNSET because the declarative amount lives on OverlaySpec.
		entry := out.LedgerEntries[0]
		assert.True(t, entry.Fired, "fired-path LedgerEntry must have Fired=true")
		assert.Equal(t, adjusterIDB2PensionUnderfunding, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.NotEmpty(t, entry.Reasoning, "Reasoning must be populated for fired entries")
		assert.False(t, entry.Timestamp.IsZero(), "Timestamp must be set on fired entries")
		assert.Empty(t, entry.Component, "B2 is an OverlayEmitter — Component must NOT be set")
		assert.Zero(t, entry.DeltaAmount, "B2 is an OverlayEmitter — DeltaAmount must be zero on the LedgerEntry")
		assert.Zero(t, entry.EquityOffset, "B2 is an OverlayEmitter — EquityOffset must be zero on the LedgerEntry")
		assert.Zero(t, entry.TaxShieldDTA, "B2 is an OverlayEmitter — TaxShieldDTA must be zero on the LedgerEntry")
		assert.Empty(t, entry.SkipReason, "SkipReason must be empty for fired entries")

		// OverlaySpec contract: add semantics on TotalDebt, the total
		// pension obligation (underfunding + OPEB), Reasoning carries the
		// canonical "pension_adjustment:" prefix preserved across the migration.
		overlay := out.Overlays[0]
		assert.Equal(t, adjusterIDB2PensionUnderfunding, overlay.OverlayID)
		assert.Equal(t, rule.ID, overlay.RuleID)
		assert.Equal(t, "TotalDebt", overlay.Field)
		assert.Equal(t, "add", overlay.Operation)
		assert.Equal(t, 250_000.0, overlay.Amount, "overlay amount = underfunding (200k) + OPEB (50k)")
		assert.Equal(t, entities.AmountIncremental, overlay.AmountSemantics)
		assert.Contains(t, overlay.Reasoning, "pension_adjustment",
			"overlay reasoning must carry the 'pension_adjustment:' prefix (greppable across logs)")
		assert.Nil(t, overlay.AIProvenance, "B2 amount is deterministic — AIProvenance must be nil")

		// Flag contract: significance gate fires at >=15% pension ratio
		// (Utilities industry threshold). Severity=Critical for 25%.
		flag := out.Flags[0]
		assert.Equal(t, "pension_underfunding", flag.Type)
		assert.Equal(t, 250_000.0, flag.Amount)
		assert.Equal(t, entities.FlagSeverityCritical, flag.Severity,
			"25%% pension ratio must trigger Critical severity per getSeverityForPensionRatio")

		// CRITICAL invariant: Apply must NOT mutate `working`. The
		// dispatcher in ProcessLiabilityAdjustments performs the dual-write
		// — Apply is read-only.
		assert.Equal(t, origTotalDebt, data.TotalDebt, "Apply must NOT mutate data.TotalDebt")
		assert.Equal(t, origInterestBearingDebt, data.InterestBearingDebt, "Apply must NOT mutate data.InterestBearingDebt")
	})

	t.Run("fired path PensionLiabilities fallback (no PBO data)", func(t *testing.T) {
		// When PBO/PlanAssets aren't populated, B2 falls back to
		// PensionLiabilities. This is a different code path inside
		// ProcessPensionAdjustment that ApplyB2PensionUnderfunding must
		// pass through cleanly.
		data := &entities.FinancialData{
			Ticker:             "MFG",
			PensionLiabilities: 80_000.0,
			OPEBLiability:      20_000.0,
			TotalAssets:        2_000_000.0,
			Revenue:            1_500_000.0,
		}
		cleaningCtx := &entities.CleaningContext{IndustryCode: "31"} // Manufacturing

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.Overlays, 1)
		overlay := out.Overlays[0]
		assert.Equal(t, 100_000.0, overlay.Amount, "fallback amount = PensionLiabilities (80k) + OPEB (20k)")
		assert.Equal(t, "TotalDebt", overlay.Field)
		assert.Equal(t, "add", overlay.Operation)
	})

	t.Run("skip path (no pension/OPEB data) emits one Fired:false LedgerEntry", func(t *testing.T) {
		data := &entities.FinancialData{
			Ticker:                     "TEST",
			ProjectedBenefitObligation: 0.0,
			PensionPlanAssets:          0.0,
			PensionLiabilities:         0.0,
			OPEBLiability:              0.0,
			TotalAssets:                1_000_000.0,
			Revenue:                    500_000.0,
		}
		cleaningCtx := &entities.CleaningContext{IndustryCode: "45"} // Technology

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		// AdjusterOutput contract for the no-pension skip path:
		require.Len(t, out.LedgerEntries, 1, "skip path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays, "skip path emits no OverlaySpec")
		assert.Empty(t, out.Flags, "skip path emits no Flags")

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired, "skip-path LedgerEntry must have Fired=false")
		assert.Equal(t, adjusterIDB2PensionUnderfunding, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.Contains(t, entry.SkipReason, "No under-funded pension or OPEB",
			"SkipReason must use the canonical legacy phrasing")
		// Plan §3.6.6: skipped entries carry zero monetary deltas.
		assert.Zero(t, entry.DeltaAmount)
		assert.Zero(t, entry.EquityOffset)
		assert.Zero(t, entry.TaxShieldDTA)
	})

	t.Run("skip path (over-funded pension)", func(t *testing.T) {
		// PlanAssets > PBO — no underfunding. OPEB=0 too, so total
		// obligation = 0 and B2 skips.
		data := &entities.FinancialData{
			Ticker:                     "TECH",
			ProjectedBenefitObligation: 300_000.0,
			PensionPlanAssets:          350_000.0,
			OPEBLiability:              0.0,
			TotalAssets:                1_000_000.0,
			Revenue:                    600_000.0,
		}
		cleaningCtx := &entities.CleaningContext{IndustryCode: "45"}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		assert.False(t, out.LedgerEntries[0].Fired,
			"over-funded pension must produce Fired:false LedgerEntry")
		assert.Empty(t, out.Overlays)
	})
}

// TestLiabilityAdjuster_ProcessLiabilityAdjustments_NativeB2Emission pins
// the dispatcher's contract: when pension_obligations is among the input
// rules AND pension data is present, ProcessLiabilityAdjustments populates
// LiabilityAdjustmentResult.{NativeLedgerEntries,NativeOverlays,NativelyEmittedRuleIDs}
// AND mutates data.TotalDebt / data.InterestBearingDebt exactly as before
// (dual-write preserved — load-bearing for DDM bit-for-bit invariant).
func TestLiabilityAdjuster_ProcessLiabilityAdjustments_NativeB2Emission(t *testing.T) {
	la := NewLiabilityAdjuster(&mockAIService{}, nil)
	data := &entities.FinancialData{
		Ticker:                     "UTILITY",
		ProjectedBenefitObligation: 500_000.0,
		PensionPlanAssets:          300_000.0,
		OPEBLiability:              50_000.0,
		TotalAssets:                2_000_000.0,
		Revenue:                    1_000_000.0,
		TotalDebt:                  400_000.0,
		InterestBearingDebt:        400_000.0,
	}
	rules := []*entities.CleaningRule{productionPensionRule()}
	cleaningCtx := &entities.CleaningContext{IndustryCode: "22"} // Utilities

	origTotalDebt := data.TotalDebt
	origInterestBearingDebt := data.InterestBearingDebt

	result := la.ProcessLiabilityAdjustments(context.Background(), data, rules, cleaningCtx)
	require.NotNil(t, result)

	// Legacy contract: Applied=true, one Adjustment, Adjustments[0].Amount =
	// underfunding + OPEB.
	assert.True(t, result.Applied)
	require.Len(t, result.Adjustments, 1)
	assert.Equal(t, 250_000.0, result.Adjustments[0].Amount,
		"legacy Adjustment.Amount must equal underfunding (200k) + OPEB (50k)")

	// Phase 2 PR-4 Task 4.2 native emission contract:
	require.GreaterOrEqual(t, len(result.NativeLedgerEntries), 1,
		"ProcessLiabilityAdjustments must surface the B2 native LedgerEntry")
	require.Len(t, result.NativeOverlays, 1,
		"ProcessLiabilityAdjustments must surface the B2 native OverlaySpec")
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["pension_obligations"],
		"pension_obligations must appear in NativelyEmittedRuleIDs so the shim skips it")

	// OverlaySpec landed in NativeOverlays — verify shape.
	overlay := result.NativeOverlays[0]
	assert.Equal(t, adjusterIDB2PensionUnderfunding, overlay.OverlayID)
	assert.Equal(t, "TotalDebt", overlay.Field)
	assert.Equal(t, "add", overlay.Operation)
	assert.Equal(t, 250_000.0, overlay.Amount)

	// DC-1 Phase 4 (C-4, §8.2.1 Option A): the B-rule debt dual-write is
	// DELETED. B2's pension underfunding flows through the OverlaySpec (above)
	// into InvestedCapital().DebtLikeClaims; it no longer inflates
	// data.TotalDebt / data.InterestBearingDebt.
	assert.Equal(t, origTotalDebt, data.TotalDebt,
		"Phase 4 §8.2.1 Option A: B2 must NOT mutate data.TotalDebt (effect → InvestedCapital().DebtLikeClaims)")
	assert.Equal(t, origInterestBearingDebt, data.InterestBearingDebt,
		"Phase 4 §8.2.1 Option A: B2 must NOT mutate data.InterestBearingDebt")
}

// TestLiabilityAdjuster_ProcessLiabilityAdjustments_NativeB2SkipPath
// confirms that on the skip path (no pension/OPEB data),
// ProcessLiabilityAdjustments surfaces the Fired:false LedgerEntry through
// NativeLedgerEntries — and the shim path (run later in service.go) skips
// emitting its own generic skip entry for the same rule.
func TestLiabilityAdjuster_ProcessLiabilityAdjustments_NativeB2SkipPath(t *testing.T) {
	la := NewLiabilityAdjuster(&mockAIService{}, nil)
	data := &entities.FinancialData{
		Ticker:                     "TEST",
		ProjectedBenefitObligation: 0.0,
		PensionPlanAssets:          0.0,
		PensionLiabilities:         0.0,
		OPEBLiability:              0.0,
		TotalAssets:                1_000_000.0,
		Revenue:                    500_000.0,
		TotalDebt:                  100_000.0,
		InterestBearingDebt:        100_000.0,
	}
	rules := []*entities.CleaningRule{productionPensionRule()}
	cleaningCtx := &entities.CleaningContext{IndustryCode: "45"}

	result := la.ProcessLiabilityAdjustments(context.Background(), data, rules, cleaningCtx)
	require.NotNil(t, result)

	// Legacy contract: Applied=false, no Adjustments.
	assert.False(t, result.Applied)
	assert.Empty(t, result.Adjustments)

	// Native emission contract — skip path still emits a Fired:false entry.
	require.Len(t, result.NativeLedgerEntries, 1,
		"skip path must still surface a Fired:false native LedgerEntry")
	assert.False(t, result.NativeLedgerEntries[0].Fired)
	assert.Empty(t, result.NativeOverlays, "skip path emits no Overlays")
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["pension_obligations"],
		"pension_obligations must appear in NativelyEmittedRuleIDs even on skip path")

	// Dual-write contract — skip path must NOT mutate balance-sheet fields.
	assert.Equal(t, 100_000.0, data.TotalDebt)
	assert.Equal(t, 100_000.0, data.InterestBearingDebt)
}

// TestB2PensionUnderfundingAdjuster_LegacyDirectInvocation pins backward
// compatibility: existing TestLiabilityAdjuster_ProcessPensionAdjustment
// callers that invoke ProcessPensionAdjustment directly (not through the
// dispatcher's switch arm) still get the legacy *AdjustmentResult shape
// with the same Applied / Amount / Flags behavior.
func TestB2PensionUnderfundingAdjuster_LegacyDirectInvocation(t *testing.T) {
	la := NewLiabilityAdjuster(&mockAIService{}, nil)
	data := &entities.FinancialData{
		Ticker:                     "UTILITY",
		ProjectedBenefitObligation: 500_000.0,
		PensionPlanAssets:          300_000.0,
		OPEBLiability:              50_000.0,
		TotalAssets:                2_000_000.0,
		Revenue:                    1_000_000.0,
	}
	rule := productionPensionRule()
	cleaningCtx := &entities.CleaningContext{IndustryCode: "22"}

	// Direct invocation bypasses the dispatcher's switch arm — must still
	// return a populated legacy result.
	result := la.ProcessPensionAdjustment(data, rule, cleaningCtx)
	require.NotNil(t, result)
	assert.True(t, result.Applied)
	assert.Equal(t, 250_000.0, result.Amount)
	// Apply was NOT called on this path — data must remain unmutated by the
	// legacy method itself (the legacy method only emits the Adjustment;
	// dual-write is the dispatcher's responsibility).
	assert.Equal(t, 0.0, data.TotalDebt, "ProcessPensionAdjustment does not mutate data — only the dispatcher does")
}
