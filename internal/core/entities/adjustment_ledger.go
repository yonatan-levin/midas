package entities

import "time"

// LedgerEntry records a single adjuster decision (fired or skipped) made during
// data cleaning. The slice of entries that accumulates on FinancialData
// (AdjustmentLedger) is the authoritative audit trail of cleaner activity.
//
// Fired=true entries describe a Restater-style component mutation (Component +
// DeltaAmount populated). Fired=false entries answer "why didn't this rule
// fire on this ticker?" without requiring code reading; SkipReason and
// SkipMetrics carry that diagnostic.
//
// Phase 2 invariant: this struct is POPULATED but no production consumer reads
// it yet (matches the Phase 0 plug-field discipline). Phase 3 introduces the
// CleanedFinancialData.Restated() accessor that consumes EquityOffset and
// TaxShieldDTA.
//
// SourceReliability is the T2-BS-3 carve-out hook. It defaults to "high" and
// flips to "parser_known_dropout" when an adjuster touches a field known to be
// affected by the AMD/KO TotalLiabilities=0 parser dropout. Phase 3's Restated
// accessor uses this hook to fall back to recomputed components.
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md
type LedgerEntry struct {
	// Always populated.
	Timestamp  time.Time `json:"timestamp"`
	AdjusterID string    `json:"adjuster_id"`
	RuleID     string    `json:"rule_id"`
	Fired      bool      `json:"fired"`
	Reasoning  string    `json:"reasoning"`

	// Populated when Fired=true.
	Component    string  `json:"component,omitempty"`
	DeltaAmount  float64 `json:"delta_amount,omitempty"`
	EquityOffset float64 `json:"equity_offset,omitempty"`
	TaxShieldDTA float64 `json:"tax_shield_dta,omitempty"`

	// Populated when Fired=false.
	SkipReason  string             `json:"skip_reason,omitempty"`
	SkipMetrics map[string]float64 `json:"skip_metrics,omitempty"`

	// T2-BS-3 carve-out hook. "" or "high" → trustworthy; "medium" → review;
	// "parser_known_dropout" → reconstruct from components in Phase 3.
	SourceReliability string `json:"source_reliability,omitempty"`
}

// AdjustmentLedger is the chronological sequence of LedgerEntry records
// produced by a single CleanFinancialData run. Ordering IS the contract:
// asset-adjuster entries appear first, then liability, then earnings; within
// each category, rule-engine order is preserved. Phase 3's view reconstruction
// depends on chronological replay.
//
// Slice newtype instead of a struct so Phase 2's surface stays minimal. Phase
// 3 may wrap it in a struct when adding methods like Restated().
type AdjustmentLedger []LedgerEntry

// AmountSemantics distinguishes how an OverlaySpec.Amount interacts with the
// target Field's current value. The distinction matters for Phase 3's
// InvestedCapital() accessor so it can avoid double-counting incremental
// overlays against an already-mutated umbrella.
type AmountSemantics string

const (
	AmountIncremental AmountSemantics = "incremental"
	AmountReplacement AmountSemantics = "replacement"
	AmountDelta       AmountSemantics = "delta"
)

// OverlaySpec is a declarative analytical-overlay record emitted by
// OverlayEmitter-role adjusters (A1 goodwill, B1 leases, B2 pension, B3
// contingent liabilities).
//
// Phase 2 collects these but does NOT apply them to any view — no Restated or
// InvestedCapital exists yet; the existing in-place mutation (e.g.
// data.TotalDebt += amount) carries the dual-write contract. Phase 3's view
// reconstruction consumes Overlays via InvestedCapital() and Phase 4 deletes
// the in-place mutation.
//
// AIProvenance is populated only when an adjuster's amount was AI-derived
// (today: B3 contingent-liability AI path). nil for every other path.
type OverlaySpec struct {
	OverlayID       string          `json:"overlay_id"`
	RuleID          string          `json:"rule_id"`
	Field           string          `json:"field"`
	Operation       string          `json:"operation"`
	Amount          float64         `json:"amount"`
	AmountSemantics AmountSemantics `json:"amount_semantics"`
	Reasoning       string          `json:"reasoning"`
	AIProvenance    *AIProvenance   `json:"ai_provenance,omitempty"`
}

// AIProvenance captures the deterministic trail for AI-derived overlay
// amounts so replay golden bundles stay reproducible across model upgrades.
//
// Phase 2 stamps best-effort fields from the existing AI integration:
// ModelName, Confidence, Probability, Timestamp can be populated; PromptHash,
// SourceDocHash, ExtractedSpan default to empty string in Phase 2 because
// today's ai.AnalyzeFootnote call site does not compute them. Q4 resolution
// (plan §10) accepted empty hashes with a Phase 3 TODO.
type AIProvenance struct {
	ModelName     string    `json:"model_name"`
	PromptHash    string    `json:"prompt_hash"`
	SourceDocHash string    `json:"source_doc_hash"`
	ExtractedSpan string    `json:"extracted_span"`
	Probability   float64   `json:"probability"`
	Confidence    float64   `json:"confidence"`
	Timestamp     time.Time `json:"timestamp"`
}

// KnownAmountSemantics is the canonical set of AmountSemantics values. It
// exists so exhaustiveness tests and config validation can iterate the set
// without reflecting over the package or duplicating the list.
//
// Any new AmountSemantics constant added above MUST also be appended here, or
// it will silently fall outside validation coverage.
//
//nolint:gochecknoglobals // immutable canonical-set sentinel; not mutable state
var KnownAmountSemantics = []AmountSemantics{
	AmountIncremental,
	AmountReplacement,
	AmountDelta,
}
