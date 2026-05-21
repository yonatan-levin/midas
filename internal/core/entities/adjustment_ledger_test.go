package entities

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLedgerEntry_FiredFalse_NoMonetaryDeltas pins invariant §3.6.6 of the
// Phase 2 plan: skipped entries carry zero monetary deltas and a non-empty
// SkipReason. Test acts as the contract every adjuster's Fired=false branch
// must honor.
func TestLedgerEntry_FiredFalse_NoMonetaryDeltas(t *testing.T) {
	tests := []struct {
		name  string
		entry LedgerEntry
	}{
		{
			name: "skip-with-metrics",
			entry: LedgerEntry{
				Timestamp:   time.Now(),
				AdjusterID:  "A1_goodwill_exclusion",
				RuleID:      "goodwill_exclusion",
				Fired:       false,
				Reasoning:   "goodwill_ratio=4.2% below 5% threshold",
				SkipReason:  "goodwill_ratio_below_threshold",
				SkipMetrics: map[string]float64{"goodwill_ratio": 0.042, "threshold": 0.05},
			},
		},
		{
			name: "skip-without-metrics",
			entry: LedgerEntry{
				Timestamp:  time.Now(),
				AdjusterID: "A2_intangible_writedown",
				RuleID:     "intangible_adjustment",
				Fired:      false,
				Reasoning:  "no intangibles present",
				SkipReason: "no_intangibles",
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			assert.False(t, tc.entry.Fired, "Fired must be false for skip entries")
			assert.NotEmpty(t, tc.entry.SkipReason, "SkipReason must be populated on skip")
			assert.Zero(t, tc.entry.DeltaAmount, "DeltaAmount must be zero on skip")
			assert.Zero(t, tc.entry.EquityOffset, "EquityOffset must be zero on skip")
			assert.Zero(t, tc.entry.TaxShieldDTA, "TaxShieldDTA must be zero on skip")
		})
	}
}

// TestLedgerEntry_FiredTrue_HasReasoning pins the contract that every fired
// adjuster supplies a non-empty Reasoning string. Mirrors today's
// Adjustment.Reasoning invariant on the legacy entities.Adjustment struct.
func TestLedgerEntry_FiredTrue_HasReasoning(t *testing.T) {
	entry := LedgerEntry{
		Timestamp:    time.Now(),
		AdjusterID:   "A5_inventory_writedown",
		RuleID:       "obsolete_inventory",
		Fired:        true,
		Reasoning:    "inventory writedown of $34M per A5 rule",
		Component:    "Inventory",
		DeltaAmount:  -34_336_000,
		EquityOffset: -34_336_000,
	}

	assert.True(t, entry.Fired)
	assert.NotEmpty(t, entry.Reasoning, "Reasoning must be populated for fired entries")
	assert.NotEmpty(t, entry.Component, "Component must be populated for fired Restater entries")
	assert.NotZero(t, entry.DeltaAmount, "DeltaAmount must be non-zero for fired Restater entries")
}

// TestLedgerEntry_ZeroValue_IsSafe asserts that the struct's zero value is a
// sensible no-op record. Code that conditionally appends entries must be able
// to rely on zero-value defaults (empty fields, false Fired) without
// initializing every field.
func TestLedgerEntry_ZeroValue_IsSafe(t *testing.T) {
	var entry LedgerEntry

	assert.False(t, entry.Fired)
	assert.Empty(t, entry.AdjusterID)
	assert.Empty(t, entry.Reasoning)
	assert.Empty(t, entry.SkipReason)
	assert.Nil(t, entry.SkipMetrics)
	assert.Zero(t, entry.DeltaAmount)
	assert.Zero(t, entry.EquityOffset)
	assert.Zero(t, entry.TaxShieldDTA)
	assert.Empty(t, entry.SourceReliability)
}

// TestAdjustmentLedger_AppendsPreserveOrder pins the §3.6.1 invariant:
// AdjustmentLedger is a slice newtype, so the order in which the orchestrator
// appends entries IS the audit order. A regression that re-sorts the slice
// would break Phase 3's chronological replay.
func TestAdjustmentLedger_AppendsPreserveOrder(t *testing.T) {
	var ledger AdjustmentLedger

	ledger = append(ledger, LedgerEntry{AdjusterID: "A1_goodwill_exclusion", Fired: true, Reasoning: "first"})
	ledger = append(ledger, LedgerEntry{AdjusterID: "B1_operating_lease", Fired: true, Reasoning: "second"})
	ledger = append(ledger, LedgerEntry{AdjusterID: "C1_restructuring", Fired: false, Reasoning: "third", SkipReason: "no_charges"})

	require.Len(t, ledger, 3)
	assert.Equal(t, "A1_goodwill_exclusion", ledger[0].AdjusterID)
	assert.Equal(t, "B1_operating_lease", ledger[1].AdjusterID)
	assert.Equal(t, "C1_restructuring", ledger[2].AdjusterID)
}

// TestAmountSemantics_KnownSetExhaustive pins KnownAmountSemantics to its
// canonical set. New constants added to the AmountSemantics type MUST also be
// appended to KnownAmountSemantics — this test breaks first if a contributor
// forgets.
func TestAmountSemantics_KnownSetExhaustive(t *testing.T) {
	want := map[AmountSemantics]bool{
		AmountIncremental: true,
		AmountReplacement: true,
		AmountDelta:       true,
	}

	require.Len(t, KnownAmountSemantics, len(want),
		"KnownAmountSemantics length must match the set of declared constants")

	for _, semantic := range KnownAmountSemantics {
		assert.True(t, want[semantic], "unexpected semantic in KnownAmountSemantics: %s", semantic)
	}
}

// TestAmountSemantics_StringValues fixes the wire-format values so a rename
// can't silently break JSON consumers (replay bundles, Phase 3+ persistence).
func TestAmountSemantics_StringValues(t *testing.T) {
	assert.Equal(t, AmountSemantics("incremental"), AmountIncremental)
	assert.Equal(t, AmountSemantics("replacement"), AmountReplacement)
	assert.Equal(t, AmountSemantics("delta"), AmountDelta)
}

// TestOverlaySpec_AIProvenance_PointerSemantics confirms that AIProvenance
// uses pointer-vs-nil to distinguish "AI did not produce this amount" from
// "AI produced this amount with all-zero metadata". This is the spec's
// declared serialization contract — omitempty on the pointer field hides
// non-AI overlays from JSON consumers entirely.
func TestOverlaySpec_AIProvenance_PointerSemantics(t *testing.T) {
	nonAI := OverlaySpec{
		OverlayID:       "B1_operating_lease_capitalization",
		RuleID:          "operating_leases",
		Field:           "TotalDebt",
		Operation:       "add",
		Amount:          254_000_000,
		AmountSemantics: AmountIncremental,
		Reasoning:       "Capitalized operating leases via PV method",
	}
	assert.Nil(t, nonAI.AIProvenance, "non-AI overlays must leave AIProvenance nil")

	ai := OverlaySpec{
		OverlayID:       "B3_contingent_liabilities",
		RuleID:          "contingent_liabilities",
		Field:           "DebtLikeClaims",
		Operation:       "add",
		Amount:          50_000_000,
		AmountSemantics: AmountIncremental,
		Reasoning:       "AI-estimated contingent liability via footnote analysis",
		AIProvenance: &AIProvenance{
			ModelName:   "claude-haiku-4-5-20251001",
			Confidence:  0.8,
			Probability: 0.3,
			Timestamp:   time.Now(),
		},
	}
	require.NotNil(t, ai.AIProvenance, "AI-derived overlays must carry AIProvenance")
	assert.Equal(t, "claude-haiku-4-5-20251001", ai.AIProvenance.ModelName)
	assert.Empty(t, ai.AIProvenance.PromptHash,
		"Phase 2 ships empty PromptHash per Q4 resolution; Phase 3 fills it")
}

// TestAIProvenance_ZeroValue_IsSafe confirms a struct literal with no fields
// set is safe to embed inside an OverlaySpec pointer chain.
func TestAIProvenance_ZeroValue_IsSafe(t *testing.T) {
	var prov AIProvenance

	assert.Empty(t, prov.ModelName)
	assert.Empty(t, prov.PromptHash)
	assert.Empty(t, prov.SourceDocHash)
	assert.Empty(t, prov.ExtractedSpan)
	assert.Zero(t, prov.Probability)
	assert.Zero(t, prov.Confidence)
	assert.True(t, prov.Timestamp.IsZero())
}

// TestLedgerEntry_SourceReliability_DefaultsEmpty captures the T2-BS-3
// carve-out hook semantics: empty string == "high" trust; "parser_known_dropout"
// is the explicit signal for Phase 3's Restated() fallback.
func TestLedgerEntry_SourceReliability_DefaultsEmpty(t *testing.T) {
	var entry LedgerEntry
	assert.Empty(t, entry.SourceReliability, "zero-value SourceReliability must be empty string (interpreted as 'high')")

	entry.SourceReliability = "parser_known_dropout"
	assert.Equal(t, "parser_known_dropout", entry.SourceReliability)
}
