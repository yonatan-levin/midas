package adjustments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// productionRestructuringRule returns a CleaningRule whose ID matches the
// production rules.json entry ("restructuring_charges") so the rule reaches
// the restructuring_charges branch in ProcessEarningsAdjustments. Mirrors
// productionIntangibleRule()'s convention from a2_intangible_adjuster_test.go.
func productionRestructuringRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:          "restructuring_charges",
		Name:        "Restructuring and Integration Charges",
		Category:    entities.EarningsNormalization,
		Adjustment:  entities.Exclude,
		Description: "Strip recurring restructuring charges from EBITDA",
		Threshold: &entities.ThresholdConfig{
			PercentageOfRevenue: floatPtr(0.02), // 2% threshold (legacy default)
		},
		Enabled:  true,
		Severity: entities.Info,
	}
}

// TestC1RestructuringAdjuster_Adjuster_Interface_Contract pins the DC-1 Phase 2
// PR-3 Task 3.1 acceptance gate: c1RestructuringAdjuster satisfies the Adjuster
// interface AND its AdjusterOutput matches the spec / plan §3.5 / §4 row C1
// Restater contract for the fired + both skip paths.
//
// The compile-time assertion `var _ Adjuster = (*c1RestructuringAdjuster)(nil)`
// in earnings.go is the primary signature pin; this test exercises the
// runtime contract — every branch of ApplyC1Restructuring produces an
// AdjusterOutput whose LedgerEntries (Restater-shaped, POSITIVE DeltaAmount —
// C1 is an add-back) match the shape the orchestrator + Phase 3 view
// reconstruction will rely on.
func TestC1RestructuringAdjuster_Adjuster_Interface_Contract(t *testing.T) {
	// Construct through the exported factory so the test exercises the public
	// API surface the orchestrator will use.
	ea := NewEarningsAdjuster()
	adj := NewC1RestructuringAdjuster(ea)
	require.NotNil(t, adj)

	// Name() contract: stable identifier consumers can join on. Locked to the
	// AdjusterID constant so a rename forces both the test and the constant to
	// move together.
	assert.Equal(t, adjusterIDC1RestructuringCharges, adj.Name(),
		"c1RestructuringAdjuster.Name() must equal the AdjusterID constant")

	rule := productionRestructuringRule()
	cleaningCtx := &entities.CleaningContext{}

	t.Run("fired path emits one Restater-shaped Fired:true LedgerEntry, no Overlays", func(t *testing.T) {
		// Restructuring = $30M, revenue = $1B → 3% ratio (above 2% threshold).
		data := &entities.FinancialData{
			Ticker:                    "TEST",
			Revenue:                   1_000_000_000,
			RestructuringCharges:      30_000_000,
			NormalizedOperatingIncome: 150_000_000,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err, "Apply must not error on a well-formed fired-path input")

		// AdjusterOutput contract: exactly one fired LedgerEntry, NO Overlays
		// (Restater — direct component restate, no analytical overlay), NO
		// Flags (legacy C1 emits no Flag — only an Adjustment).
		require.Len(t, out.LedgerEntries, 1, "fired path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays, "C1 is a Restater — must NOT emit OverlaySpecs")
		assert.Empty(t, out.Flags, "C1 emits no significance Flag on the fired path (legacy parity)")

		// LedgerEntry contract (plan §3.5 Restater role + §4 row C1):
		// Fired=true, Component:"NormalizedOperatingIncome",
		// DeltaAmount:+restructuringAmount (POSITIVE — add-back),
		// EquityOffset:+restructuringAmount, TaxShieldDTA:0 (Q2 deferral).
		entry := out.LedgerEntries[0]
		assert.True(t, entry.Fired, "fired-path LedgerEntry must have Fired=true")
		assert.Equal(t, adjusterIDC1RestructuringCharges, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.NotEmpty(t, entry.Reasoning, "Reasoning must be populated for fired entries")
		assert.False(t, entry.Timestamp.IsZero(), "Timestamp must be set on fired entries")
		assert.Equal(t, "NormalizedOperatingIncome", entry.Component,
			"C1 is a Restater — Component must point at the mutated income-statement line")
		// DeltaAmount is POSITIVE for C1 because it's an add-back (legacy code:
		// data.NormalizedOperatingIncome += restructuringAmount). EquityOffset
		// mirrors DeltaAmount 1:1.
		assert.InDelta(t, 30_000_000.0, entry.DeltaAmount, 1e-6,
			"DeltaAmount must equal +restructuringAmount (POSITIVE — add-back, not writedown)")
		assert.InDelta(t, 30_000_000.0, entry.EquityOffset, 1e-6,
			"EquityOffset must mirror DeltaAmount — add-backs increase retained earnings 1:1")
		assert.Zero(t, entry.TaxShieldDTA,
			"Q2 deferral: C1 TaxShieldDTA stays 0 in Phase 2 to preserve dual-write bit-for-bit")
		assert.Empty(t, entry.SkipReason, "SkipReason must be empty for fired entries")

		// CRITICAL invariant: Apply must NOT mutate `working`. The dispatcher
		// in ProcessEarningsAdjustments performs the dual-write — Apply is
		// read-only.
		assert.Equal(t, 150_000_000.0, data.NormalizedOperatingIncome,
			"Apply must NOT mutate data.NormalizedOperatingIncome")
		assert.Equal(t, 30_000_000.0, data.RestructuringCharges,
			"Apply must NOT mutate data.RestructuringCharges")
	})

	t.Run("skip path 1 (no revenue) emits one Fired:false LedgerEntry without SkipMetrics", func(t *testing.T) {
		data := &entities.FinancialData{
			Ticker:                    "TEST",
			Revenue:                   0, // No revenue — cannot compute ratio
			RestructuringCharges:      30_000_000,
			NormalizedOperatingIncome: 150_000_000,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1, "skip path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays, "skip path emits no OverlaySpec")
		assert.Empty(t, out.Flags, "skip path emits no Flags")

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, adjusterIDC1RestructuringCharges, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.Equal(t, "Insufficient revenue data to calculate restructuring charges", entry.SkipReason,
			"no-revenue skip path must use the canonical SkipReason string")
		assert.Empty(t, entry.SkipMetrics,
			"no-revenue skip path does not carry SkipMetrics — only the threshold-failed path does")
		// Skipped entries carry zero monetary deltas.
		assert.Zero(t, entry.DeltaAmount)
		assert.Zero(t, entry.EquityOffset)
		assert.Zero(t, entry.TaxShieldDTA)
		// Apply must NOT mutate working on skip paths either.
		assert.Equal(t, 150_000_000.0, data.NormalizedOperatingIncome,
			"Apply must NOT mutate working on no-revenue skip path")
	})

	t.Run("skip path 2 (below threshold) emits one Fired:false LedgerEntry with SkipMetrics", func(t *testing.T) {
		// Restructuring ratio = 10M / 1B = 1% — below 2% threshold.
		data := &entities.FinancialData{
			Ticker:                    "TEST",
			Revenue:                   1_000_000_000,
			RestructuringCharges:      10_000_000,
			NormalizedOperatingIncome: 150_000_000,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		assert.Empty(t, out.Overlays)
		assert.Empty(t, out.Flags)

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, adjusterIDC1RestructuringCharges, entry.AdjusterID)
		assert.Contains(t, entry.SkipReason, "below materiality threshold",
			"threshold-failed SkipReason must explain why")
		require.NotNil(t, entry.SkipMetrics, "threshold-failed skip path must carry SkipMetrics")
		assert.InDelta(t, 0.01, entry.SkipMetrics["restructuring_ratio"], 1e-9)
		assert.InDelta(t, 0.02, entry.SkipMetrics["threshold"], 1e-9)
		// Skipped entries carry zero monetary deltas — even though Reasoning
		// repeats the legacy formatted string.
		assert.Zero(t, entry.DeltaAmount)
		assert.Zero(t, entry.EquityOffset)
		// Apply must NOT mutate on below-threshold skip path.
		assert.Equal(t, 150_000_000.0, data.NormalizedOperatingIncome,
			"Apply must NOT mutate working on below-threshold skip path")
	})

	t.Run("fired path TaxShieldDTA stays zero per Q2 deferral", func(t *testing.T) {
		// Independent assertion of the Q2 (plan §10) deferral contract: a
		// non-zero EffectiveTaxRate must NOT cause C1 to populate
		// TaxShieldDTA. Future Phase 3 work may revisit; until then, any
		// change to Apply that starts populating TaxShieldDTA must fail this
		// test FIRST so the implementer notices the deferral contract.
		data := &entities.FinancialData{
			Ticker:                    "TEST",
			Revenue:                   1_000_000_000,
			RestructuringCharges:      30_000_000,
			NormalizedOperatingIncome: 150_000_000,
			EffectiveTaxRate:          0.21,
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)
		require.Len(t, out.LedgerEntries, 1)
		entry := out.LedgerEntries[0]
		require.True(t, entry.Fired, "preconditions chosen to fire — sanity check")
		assert.Zero(t, entry.TaxShieldDTA,
			"Q2 deferral (plan §10): C1 must NOT compute TaxShieldDTA in Phase 2 even when EffectiveTaxRate is non-zero")
	})
}

// TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC1Emission pins the
// dispatcher's contract for the restructuring_charges rule: when present in
// the input rules AND restructuring charges are above threshold,
// ProcessEarningsAdjustments populates NativeLedgerEntries with the C1 fired
// entry AND mutates data.NormalizedOperatingIncome exactly as before
// (dual-write preserved).
func TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC1Emission(t *testing.T) {
	ea := NewEarningsAdjuster()
	data := &entities.FinancialData{
		Ticker:                    "TEST",
		Revenue:                   1_000_000_000,
		RestructuringCharges:      30_000_000, // 3% of revenue (above 2% threshold)
		NormalizedOperatingIncome: 150_000_000,
	}
	rules := []*entities.CleaningRule{productionRestructuringRule()}
	cleaningCtx := &entities.CleaningContext{}

	result := ea.ProcessEarningsAdjustments(context.Background(), data, rules, cleaningCtx)
	require.NotNil(t, result)

	// Legacy contract: Applied=true, one Adjustment, Adjustments[0].Amount
	// equals the add-back magnitude (30M).
	assert.True(t, result.Applied)
	require.Len(t, result.Adjustments, 1)
	assert.InDelta(t, 30_000_000.0, result.Adjustments[0].Amount, 1e-6)
	assert.Equal(t, "RestructuringCharges", result.Adjustments[0].FromAccount)
	assert.Equal(t, "NormalizedOperatingIncome", result.Adjustments[0].ToAccount)
	assert.Equal(t, entities.Exclude, result.Adjustments[0].Type)

	// Phase 2 PR-3 Task 3.1 native emission contract:
	require.Len(t, result.NativeLedgerEntries, 1,
		"ProcessEarningsAdjustments must surface the C1 native LedgerEntry")
	assert.Empty(t, result.NativeOverlays, "C1 is a Restater — no OverlaySpec native emission")
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["restructuring_charges"],
		"restructuring_charges must appear in NativelyEmittedRuleIDs so the shim skips it")

	// Restater shape on the native entry:
	nativeEntry := result.NativeLedgerEntries[0]
	assert.True(t, nativeEntry.Fired)
	assert.Equal(t, adjusterIDC1RestructuringCharges, nativeEntry.AdjusterID)
	assert.Equal(t, "NormalizedOperatingIncome", nativeEntry.Component)
	assert.InDelta(t, 30_000_000.0, nativeEntry.DeltaAmount, 1e-6,
		"C1 native DeltaAmount must be POSITIVE — add-back, not writedown")
	assert.InDelta(t, 30_000_000.0, nativeEntry.EquityOffset, 1e-6,
		"C1 native EquityOffset must mirror DeltaAmount 1:1 (add-back flows to retained earnings)")
	assert.Zero(t, nativeEntry.TaxShieldDTA, "Q2 deferral: TaxShieldDTA stays 0 on dispatcher path too")

	// Dual-write preserved — data was mutated as the legacy code did.
	// Starting NormalizedOperatingIncome = 150M + add-back 30M = 180M.
	assert.InDelta(t, 180_000_000.0, data.NormalizedOperatingIncome, 1e-6,
		"dispatcher must add restructuringAmount to data.NormalizedOperatingIncome (dual-write)")
	// RestructuringCharges itself is not mutated by C1 (the field is the source,
	// not the target of the add-back).
	assert.Equal(t, 30_000_000.0, data.RestructuringCharges,
		"dispatcher must NOT mutate data.RestructuringCharges (C1 only touches NormalizedOperatingIncome)")
}

// TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC1SkipPath confirms
// that even on the skip path (below-threshold restructuring charges),
// ProcessEarningsAdjustments surfaces the Fired:false LedgerEntry through
// NativeLedgerEntries and performs NO mutation — the shim then skips emitting
// its own generic skip entry for the same rule.
func TestEarningsAdjuster_ProcessEarningsAdjustments_NativeC1SkipPath(t *testing.T) {
	ea := NewEarningsAdjuster()
	data := &entities.FinancialData{
		Ticker:                    "TEST",
		Revenue:                   1_000_000_000,
		RestructuringCharges:      10_000_000, // 1% of revenue — below 2% threshold
		NormalizedOperatingIncome: 150_000_000,
	}
	rules := []*entities.CleaningRule{productionRestructuringRule()}

	result := ea.ProcessEarningsAdjustments(context.Background(), data, rules, &entities.CleaningContext{})
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
	assert.True(t, result.NativelyEmittedRuleIDs["restructuring_charges"],
		"restructuring_charges must appear in NativelyEmittedRuleIDs even on skip path")

	// Skip path threshold metrics surface for dashboards.
	skipEntry := result.NativeLedgerEntries[0]
	assert.Contains(t, skipEntry.SkipReason, "below materiality threshold")
	require.NotNil(t, skipEntry.SkipMetrics)
	assert.InDelta(t, 0.01, skipEntry.SkipMetrics["restructuring_ratio"], 1e-9)
	assert.InDelta(t, 0.02, skipEntry.SkipMetrics["threshold"], 1e-9)

	// Dual-write contract — skip path must NOT mutate income-statement fields.
	assert.Equal(t, 150_000_000.0, data.NormalizedOperatingIncome,
		"dispatcher must NOT mutate NormalizedOperatingIncome on C1 skip path")
}
