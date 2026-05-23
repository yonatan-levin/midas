package adjustments

import (
	"context"
	"fmt"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/ai"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/industry"
	"github.com/midas/dcf-valuation-api/pkg/finance/leases"
)

// LiabilityAdjuster handles Category B adjustments from SEC cleaning guide
// Implements under-stated liabilities and off-balance-sheet exposures
type LiabilityAdjuster struct {
	// TODO: Add configuration for adjustment thresholds
	leaseCalculator    *leases.PerformanceOptimizedCalculator
	industryClassifier *industry.IndustryClassifier
	// AI service for footnote analysis (config-gated)
	aiService ai.AIService
	aiEnabled bool
}

// NewLiabilityAdjuster creates a new liability adjuster instance
func NewLiabilityAdjuster(aiSvc ai.AIService, industryClassifier *industry.IndustryClassifier) *LiabilityAdjuster {
	// TODO: Load configuration from proper source
	config := leases.GetDefaultConfig()
	leaseCalculator := leases.NewPerformanceOptimizedCalculator(config)

	return &LiabilityAdjuster{
		leaseCalculator:    leaseCalculator,
		industryClassifier: industryClassifier,
		aiService:          aiSvc,
		aiEnabled:          false, // Disabled by default, enabled via WithAI()
	}
}

// WithAI enables AI-driven analysis pathways when available.
func (la *LiabilityAdjuster) WithAI(enabled bool) *LiabilityAdjuster {
	la.aiEnabled = enabled
	return la
}

// AdjusterID constants identify each Category B adjuster on LedgerEntry /
// OverlaySpec records. They MUST be stable across builds — Phase 3's view
// reconstruction joins on these IDs. Keep the trailing "_<descriptor>"
// suffixes in sync with the legacy rule.ID values where possible so log greps
// continue to work across the migration.
const (
	adjusterIDB1OperatingLeaseCapitalization = "B1_operating_lease_capitalization"
	adjusterIDB2PensionUnderfunding          = "B2_pension_underfunding"
	adjusterIDB3ContingentLiability          = "B3_contingent_liability"
)

// b3AIModelName is the canonical ModelName string stamped on B3's
// AIProvenance when the AI footnote-analysis path fires. It mirrors the
// "ai_model_used" metadata legacy code emits at
// analyzeContingentLiabilityWithAI (line ~1237: "footnote_analysis") so
// downstream log greps continue to work across the migration. The legacy
// TODO at that site (read actual model from config) carries through here —
// today's LiabilityAdjuster does not receive AIServiceConfig, so the
// hardcoded literal is the only stable identifier available in Phase 2.
const b3AIModelName = "footnote_analysis"

// LiabilityAdjustmentResult represents the result of applying liability adjustments.
//
// DC-1 Phase 2 PR-4 Task 4.1 added the three Native* fields below to carry
// AdjusterOutput state from Category B rules that have migrated to the
// Adjuster interface (PR-4 Task 4.1 onwards). The cleaner orchestrator reads
// NativeLedgerEntries / NativeOverlays / NativelyEmittedRuleIDs to:
//   - append the native LedgerEntries to data.AdjustmentLedger BEFORE the
//     PR-1 shim runs, preserving liability-category ordering;
//   - append the native Overlays to data.Overlays;
//   - instruct the shim to SKIP any rule whose ID appears in
//     NativelyEmittedRuleIDs so the same rule is not double-counted.
//
// Mirrors AssetAdjustmentResult (PR-2 Task 2.1) and EarningsAdjustmentResult
// (PR-3 Task 3.1). PR-4 Task 4.4 absorbs the dispatcher's dual-write into the
// Adjuster path and Task 4.5 deletes the shim's liability branch.
type LiabilityAdjustmentResult struct {
	Applied                  bool                   `json:"applied"`
	TotalLiabilityAdjustment float64                `json:"total_liability_adjustment"`
	AdjustedTotalDebt        float64                `json:"adjusted_total_debt"`
	Adjustments              []entities.Adjustment  `json:"adjustments"`
	Flags                    []entities.Flag        `json:"flags"`
	AuditTrail               string                 `json:"audit_trail"`
	NativeLedgerEntries      []entities.LedgerEntry `json:"-"`
	NativeOverlays           []entities.OverlaySpec `json:"-"`
	NativelyEmittedRuleIDs   map[string]bool        `json:"-"`
}

// b1OperatingLeaseCapitalizationAdjuster is the per-rule adapter that lets
// LiabilityAdjuster — which hosts multiple Category B rules — satisfy the
// single-Apply Adjuster interface. Mirrors a1GoodwillAdjuster's shape:
// the adapter holds a pointer to the existing LiabilityAdjuster and Apply
// delegates to the new mutation-free ApplyB1OperatingLeases method.
//
// Role classification (plan §3.5 / §4 row B1): OverlayEmitter. The fired
// LedgerEntry carries NO Component / DeltaAmount / EquityOffset — the
// declarative amount lives on the OverlaySpec (Field:"TotalDebt",
// Operation:"add"). The fired LedgerEntry exists for ordering / audit
// purposes so consumers can answer "did B1 fire?" without reading overlays.
type b1OperatingLeaseCapitalizationAdjuster struct {
	la *LiabilityAdjuster
}

// NewB1OperatingLeaseCapitalizationAdjuster returns an Adjuster-shaped wrapper
// around LiabilityAdjuster's B1 rule. Exported so the cleaner orchestrator can
// hold the instance alongside the legacy LiabilityAdjuster.
func NewB1OperatingLeaseCapitalizationAdjuster(la *LiabilityAdjuster) Adjuster {
	return &b1OperatingLeaseCapitalizationAdjuster{la: la}
}

// Compile-time assertion: b1OperatingLeaseCapitalizationAdjuster MUST
// implement Adjuster. If either signature drifts, the package fails to build.
var _ Adjuster = (*b1OperatingLeaseCapitalizationAdjuster)(nil)

// Name implements Adjuster.
func (b *b1OperatingLeaseCapitalizationAdjuster) Name() string {
	return adjusterIDB1OperatingLeaseCapitalization
}

// Apply implements Adjuster by delegating to
// LiabilityAdjuster.ApplyB1OperatingLeases. The dual-write contract (in-place
// mutation of data.TotalDebt / data.InterestBearingDebt) is preserved by the
// dispatcher in ProcessLiabilityAdjustments — NOT by Apply itself. See
// ApplyB1OperatingLeases godoc for the role split.
func (b *b1OperatingLeaseCapitalizationAdjuster) Apply(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	return b.la.ApplyB1OperatingLeases(ctx, working, rule, cleaningCtx)
}

// b2PensionUnderfundingAdjuster is the per-rule adapter that lets
// LiabilityAdjuster — which hosts multiple Category B rules — satisfy the
// single-Apply Adjuster interface for the B2 rule. Mirrors
// b1OperatingLeaseCapitalizationAdjuster's shape: the adapter holds a
// pointer to the existing LiabilityAdjuster and Apply delegates to the new
// mutation-free ApplyB2PensionUnderfunding method.
//
// Role classification (plan §3.5 / §4 row B2): OverlayEmitter. The fired
// LedgerEntry carries NO Component / DeltaAmount / EquityOffset — the
// declarative amount lives on the OverlaySpec (Field:"TotalDebt",
// Operation:"add"). The fired LedgerEntry exists for ordering / audit
// purposes so consumers can answer "did B2 fire?" without reading overlays.
type b2PensionUnderfundingAdjuster struct {
	la *LiabilityAdjuster
}

// NewB2PensionUnderfundingAdjuster returns an Adjuster-shaped wrapper around
// LiabilityAdjuster's B2 rule. Exported for parity with
// NewB1OperatingLeaseCapitalizationAdjuster so the cleaner orchestrator can
// hold the instance alongside the legacy LiabilityAdjuster.
func NewB2PensionUnderfundingAdjuster(la *LiabilityAdjuster) Adjuster {
	return &b2PensionUnderfundingAdjuster{la: la}
}

// Compile-time assertion: b2PensionUnderfundingAdjuster MUST implement
// Adjuster. If either signature drifts, the package fails to build.
var _ Adjuster = (*b2PensionUnderfundingAdjuster)(nil)

// Name implements Adjuster.
func (b *b2PensionUnderfundingAdjuster) Name() string {
	return adjusterIDB2PensionUnderfunding
}

// Apply implements Adjuster by delegating to
// LiabilityAdjuster.ApplyB2PensionUnderfunding. The dual-write contract
// (in-place mutation of data.TotalDebt / data.InterestBearingDebt) is
// preserved by the dispatcher in ProcessLiabilityAdjustments — NOT by Apply
// itself. See ApplyB2PensionUnderfunding godoc for the role split.
func (b *b2PensionUnderfundingAdjuster) Apply(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	return b.la.ApplyB2PensionUnderfunding(ctx, working, rule, cleaningCtx)
}

// b3ContingentLiabilityAdjuster is the per-rule adapter that lets
// LiabilityAdjuster — which hosts multiple Category B rules — satisfy the
// single-Apply Adjuster interface for the B3 rule. Mirrors B1/B2 adapters:
// the adapter holds a pointer to the existing LiabilityAdjuster and Apply
// delegates to the new mutation-free ApplyB3Contingent method.
//
// Role classification (plan §3.5 / §4 row B3): OverlayEmitter. The fired
// LedgerEntry carries NO Component / DeltaAmount / EquityOffset — the
// declarative amount lives on the OverlaySpec.
//
// Critical accuracy correction (spec §"B3 routing correction" lines 181-189):
// the OverlaySpec.Field is "DebtLikeClaims" (NOT "TotalDebt") — this records
// the Phase 4 routing intent. In Phase 2 the dispatcher's dual-write at
// liabilities.go:87-88 STILL mutates data.TotalDebt; Phase 4 flips
// consumers to read Overlays[Field:"DebtLikeClaims"] and the dual-write
// mutation gets deleted. The mismatch is intentional and documented.
//
// AIProvenance is populated best-effort when the AI footnote-analysis path
// fires (la.aiEnabled && la.aiService != nil): ModelName / Confidence /
// Probability / ExtractedSpan / Timestamp are stamped from the AI
// response. PromptHash + SourceDocHash stay empty string with a Phase 3
// TODO per Q4 resolution (plan §10). Rule-based / AI-disabled / AI-failed
// paths emit AIProvenance:nil — only AI-derived amounts carry provenance.
type b3ContingentLiabilityAdjuster struct {
	la *LiabilityAdjuster
}

// NewB3ContingentLiabilityAdjuster returns an Adjuster-shaped wrapper around
// LiabilityAdjuster's B3 rule. Exported for parity with
// NewB1OperatingLeaseCapitalizationAdjuster / NewB2PensionUnderfundingAdjuster
// so the cleaner orchestrator can hold the instance alongside the legacy
// LiabilityAdjuster.
func NewB3ContingentLiabilityAdjuster(la *LiabilityAdjuster) Adjuster {
	return &b3ContingentLiabilityAdjuster{la: la}
}

// Compile-time assertion: b3ContingentLiabilityAdjuster MUST implement
// Adjuster. If either signature drifts, the package fails to build.
var _ Adjuster = (*b3ContingentLiabilityAdjuster)(nil)

// Name implements Adjuster.
func (b *b3ContingentLiabilityAdjuster) Name() string {
	return adjusterIDB3ContingentLiability
}

// Apply implements Adjuster by delegating to
// LiabilityAdjuster.ApplyB3Contingent. The dual-write contract (in-place
// mutation of data.TotalDebt / data.InterestBearingDebt) is preserved by
// the dispatcher in ProcessLiabilityAdjustments — NOT by Apply itself.
// See ApplyB3Contingent godoc for the role split AND the
// Field:"DebtLikeClaims" / dual-write-against-TotalDebt mismatch.
func (b *b3ContingentLiabilityAdjuster) Apply(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	return b.la.ApplyB3Contingent(ctx, working, rule, cleaningCtx)
}

// ProcessLiabilityAdjustments orchestrates all Category B liability adjustments
//
// DC-1 Phase 2 PR-4 Task 4.1 (incremental Adjuster-interface migration):
// rules whose AdjusterID appears in result.NativelyEmittedRuleIDs have
// produced LedgerEntries / Overlays / Flags via their Adjuster.Apply path.
// The cleaner orchestrator (service.go::applyActiveAdjustments) reads those
// fields and appends them to data.AdjustmentLedger / data.Overlays directly,
// then instructs the shim to SKIP those rules so the same rule is not
// double-counted. Tasks 4.2/4.3 add more rules to the NativelyEmittedRuleIDs
// set; Task 4.4 absorbs the dispatcher dual-write into the Adjuster path and
// Task 4.5 deletes the shim's liability branch entirely.
//
// IMPORTANT (PR-4 Task 4.1/4.2 scope): the dual-write at the bottom of this
// loop (data.TotalDebt += result.Amount, data.InterestBearingDebt +=
// result.Amount) STAYS UNCHANGED. This is load-bearing for the DDM legacy
// path (JPM bit-for-bit invariant — DDM reads data.TotalDebt directly).
// Task 4.4 audits the absorption strategy.
//
// Parameter `cleaningCtx` was historically named `context` here; PR-4 Task
// 4.1 renames it so the `context` package identifier is unshadowed inside
// the function body — required for the new Adjuster.Apply call site.
func (la *LiabilityAdjuster) ProcessLiabilityAdjustments(data *entities.FinancialData, rules []*entities.CleaningRule, cleaningCtx *entities.CleaningContext) *LiabilityAdjustmentResult {
	var allAdjustments []entities.Adjustment
	var allFlags []entities.Flag
	var totalAdjustment float64
	originalDebt := data.TotalDebt

	// Phase 2 PR-4 native emissions — collected here in rule-iteration order
	// so the orchestrator can append them to data.AdjustmentLedger in
	// position. The set NativelyEmittedRuleIDs tells the shim which legacy
	// emissions to skip to avoid double counting.
	var nativeLedger []entities.LedgerEntry
	var nativeOverlays []entities.OverlaySpec
	nativelyEmittedRuleIDs := make(map[string]bool, len(rules))

	// Apply.ctx is nil here because ProcessLiabilityAdjustments does not yet
	// thread ctx through its public signature. ApplyB1OperatingLeases treats
	// nil ctx as safe (it only uses ctx for future industry-aware logic).
	// TODO(PR-4 follow-up): thread context.Context through
	// ProcessLiabilityAdjustments to align with the Adjuster.Apply signature.
	var applyCtx context.Context

	// Process each Category B rule
	for _, rule := range rules {
		if rule.Category != entities.LiabilityCompleteness || !rule.Enabled {
			continue
		}

		var result *AdjustmentResult

		switch rule.ID {
		case "operating_leases":
			// DC-1 Phase 2 PR-4 Task 4.1: route B1 through the new
			// Adjuster-shaped ApplyB1OperatingLeases. Apply is mutation-
			// free; the dispatcher performs the dual-write AFTER Apply
			// so the legacy *AdjustmentResult callers stay byte-identical
			// AND the AdjusterOutput's LedgerEntries / Overlays / Flags
			// reach the cleaner orchestrator.
			out, err := la.ApplyB1OperatingLeases(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				// Adjuster.Apply errors are not yet a defined surface in
				// Phase 2; today's ApplyB1OperatingLeases never returns one.
				// Falling back to the legacy path on hypothetical future
				// errors preserves the dual-write contract.
				result = la.ProcessOperatingLeaseAdjustment(data, rule, cleaningCtx)
				break
			}

			// Translate the AdjusterOutput into the legacy *AdjustmentResult
			// shape so the dispatcher's existing dual-write + audit-trail
			// accounting keeps working. The dual-write at the bottom of the
			// loop performs the actual data.TotalDebt mutation.
			result = b1AdjusterOutputToLegacyResult(out, rule)

			// Record native emissions for the orchestrator. Even when the
			// rule does not "fire" in the legacy sense (Applied=false), the
			// AdjusterOutput carries a Fired:false LedgerEntry that is still
			// load-bearing for "why didn't B1 fire?" observability.
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
		case "pension_obligations":
			// DC-1 Phase 2 PR-4 Task 4.2: route B2 through the new
			// Adjuster-shaped ApplyB2PensionUnderfunding. Mirrors the B1
			// wiring above — Apply is mutation-free; the dispatcher
			// performs the dual-write AFTER Apply so the legacy
			// *AdjustmentResult callers stay byte-identical AND the
			// AdjusterOutput's LedgerEntries / Overlays / Flags reach the
			// cleaner orchestrator.
			out, err := la.ApplyB2PensionUnderfunding(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				// Adjuster.Apply errors are not yet a defined surface in
				// Phase 2; today's ApplyB2PensionUnderfunding never returns
				// one. Falling back to the legacy path on hypothetical
				// future errors preserves the dual-write contract.
				result = la.ProcessPensionAdjustment(data, rule, cleaningCtx)
				break
			}

			// Translate the AdjusterOutput into the legacy *AdjustmentResult
			// shape so the dispatcher's existing dual-write + audit-trail
			// accounting keeps working. The dual-write at the bottom of the
			// loop performs the actual data.TotalDebt mutation.
			result = b2AdjusterOutputToLegacyResult(out, rule)

			// Record native emissions for the orchestrator. Even when the
			// rule does not "fire" in the legacy sense (Applied=false), the
			// AdjusterOutput carries a Fired:false LedgerEntry that is still
			// load-bearing for "why didn't B2 fire?" observability.
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
		case "contingent_liabilities":
			// DC-1 Phase 2 PR-4 Task 4.3: route B3 through the new
			// Adjuster-shaped ApplyB3Contingent. Mirrors B1/B2 wiring —
			// Apply is mutation-free; the dispatcher performs the dual-
			// write AFTER Apply so the legacy *AdjustmentResult callers
			// stay byte-identical AND the AdjusterOutput's
			// LedgerEntries / Overlays / Flags reach the cleaner
			// orchestrator. Unlike B1/B2, the emitted OverlaySpec's
			// Field is "DebtLikeClaims" (Phase 4 routing intent), but
			// the dispatcher's dual-write still mutates data.TotalDebt
			// per spec §"B3 routing correction" lines 181-189.
			out, err := la.ApplyB3Contingent(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				// Adjuster.Apply errors are not yet a defined surface in
				// Phase 2; today's ApplyB3Contingent surfaces no errors
				// even when the AI service fails (the AI failure is
				// absorbed by the legacy fallback path inside Process*).
				// Falling back to the legacy path on hypothetical future
				// errors preserves the dual-write contract.
				result = la.ProcessContingentLiabilityAdjustment(data, rule, cleaningCtx)
				break
			}

			// Translate the AdjusterOutput into the legacy
			// *AdjustmentResult shape so the dispatcher's existing
			// dual-write + audit-trail accounting keeps working. The
			// dual-write at the bottom of the loop performs the actual
			// data.TotalDebt mutation.
			result = b3AdjusterOutputToLegacyResult(out, rule)

			// Record native emissions for the orchestrator. Even when
			// the rule does not "fire" in the legacy sense
			// (Applied=false), the AdjusterOutput carries a Fired:false
			// LedgerEntry that is still load-bearing for "why didn't B3
			// fire?" observability.
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
		default:
			continue // Skip unknown rules
		}

		if result != nil && result.Applied {
			allAdjustments = append(allAdjustments, result.Adjustments...)
			allFlags = append(allFlags, result.Flags...)
			totalAdjustment += result.Amount

			// Add to debt base for WACC calculations.
			// PR-4 Task 4.1/4.2: this dual-write STAYS UNCHANGED in this
			// task. Task 4.4 absorbs the absorption into the Adjuster path.
			data.TotalDebt += result.Amount
			data.InterestBearingDebt += result.Amount
		}
	}

	applied := len(allAdjustments) > 0
	auditTrail := fmt.Sprintf("Processed %d Category B liability rules, total adjustment: %.0f, debt increased from %.0f to %.0f",
		len(rules), totalAdjustment, originalDebt, data.TotalDebt)

	return &LiabilityAdjustmentResult{
		Applied:                  applied,
		TotalLiabilityAdjustment: totalAdjustment,
		AdjustedTotalDebt:        data.TotalDebt,
		Adjustments:              allAdjustments,
		Flags:                    allFlags,
		AuditTrail:               auditTrail,
		NativeLedgerEntries:      nativeLedger,
		NativeOverlays:           nativeOverlays,
		NativelyEmittedRuleIDs:   nativelyEmittedRuleIDs,
	}
}

// ApplyB1OperatingLeases is the Adjuster-shaped (DC-1 Phase 2 PR-4 Task 4.1)
// implementation of the B1 operating-lease capitalization rule. It produces
// an AdjusterOutput describing what the rule would do — LedgerEntries (audit
// trail), Overlays (declarative "add operating-lease PV to TotalDebt"
// record), and Flags (quality / validation / materiality triggers) — but
// does NOT mutate `working`. The dual-write mutation
// (data.TotalDebt += presentValue, data.InterestBearingDebt += presentValue)
// is performed by ProcessLiabilityAdjustments' dispatcher so the legacy
// *AdjustmentResult callers stay byte-identical.
//
// Role classification (plan §3.5 / §4 row B1): OverlayEmitter. The fired
// LedgerEntry intentionally carries NO Component / DeltaAmount /
// EquityOffset — the declarative amount lives on OverlaySpec
// (Field:"TotalDebt", Operation:"add"). The audit-only LedgerEntry exists
// for ordering / "did B1 fire?" diagnostics; the Restater fields are left
// zero per the OverlayEmitter convention (mirrors A1 goodwill).
//
// Implementation strategy: delegates to ProcessOperatingLeaseAdjustment for
// the actual PV math (including the calculator-failure fallback to simple
// capitalization via fallbackToSimpleCapitalization) and translates the
// returned *AdjustmentResult into the AdjusterOutput shape. Keeping the
// fallback inline (private method on LiabilityAdjuster) rather than
// extracting it as a separate B1-fallback Adjuster minimizes the PR-4
// surface area — the fallback never alters the OverlayEmitter contract
// (still emits a single overlay on TotalDebt + audit LedgerEntry).
//
// Skipped paths emit Fired:false LedgerEntries with SkipReason so
// observability can answer "why didn't B1 fire on this ticker?" without
// code reading. Today's ProcessOperatingLeaseAdjustment encodes two skip
// paths: (a) PV calculator failed AND no fallback lease liability — emits
// the original calculator error in SkipReason; (b) PV calculator returned
// zero — emits the canonical "no meaningful operating lease present value
// calculated" string. Both surface as Fired:false LedgerEntries here.
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md §"Adjuster output"
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md §3.5 / §4 row B1 / §7 Task 4.1
func (la *LiabilityAdjuster) ApplyB1OperatingLeases(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	// ctx accepted for interface symmetry with future industry-aware
	// adjusters; ProcessOperatingLeaseAdjustment already binds its own
	// context.Background() internally for the calculator call (today's
	// production behavior — PR-4 preserves it bit-for-bit).
	_ = ctx

	now := time.Now()

	// Delegate to the legacy method for the actual PV calculation (including
	// fallbackToSimpleCapitalization on calculator failure). This preserves
	// the existing flag taxonomy + reasoning strings bit-for-bit, which is
	// load-bearing for downstream consumers that grep on the
	// "operating_lease_adj:" / "lease_calculation_quality" / etc. prefixes.
	legacy := la.ProcessOperatingLeaseAdjustment(working, rule, cleaningCtx)

	// Skip path: PV calculation returned no meaningful value OR the fallback
	// itself returned Applied=false (no operating-lease data at all). Emit a
	// single Fired:false LedgerEntry so observability can answer "why didn't
	// B1 fire on this ticker?".
	if legacy == nil || !legacy.Applied {
		skipReason := "No operating lease data available"
		reasoning := skipReason
		if legacy != nil && legacy.Reasoning != "" {
			skipReason = legacy.Reasoning
			reasoning = legacy.Reasoning
		}
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:  now,
				AdjusterID: adjusterIDB1OperatingLeaseCapitalization,
				RuleID:     rule.ID,
				Fired:      false,
				Reasoning:  reasoning,
				SkipReason: skipReason,
			}},
		}, nil
	}

	// Fired path: emit a declarative OverlaySpec on TotalDebt + a Fired:true
	// audit LedgerEntry (no Component / DeltaAmount per OverlayEmitter role)
	// + any flags the legacy path generated. legacy.Amount carries the PV
	// (or the book-value fallback when the calculator failed).
	overlay := entities.OverlaySpec{
		OverlayID:       adjusterIDB1OperatingLeaseCapitalization,
		RuleID:          rule.ID,
		Field:           "TotalDebt",
		Operation:       "add",
		Amount:          legacy.Amount,
		AmountSemantics: entities.AmountIncremental,
		// Preserve the legacy "operating_lease_adj:" / fallback reasoning
		// prefix on the overlay so existing log greps keep working. The
		// legacy.Adjustments[0].Reasoning carries the canonical phrasing
		// (full PV path or fallback path); fall back to legacy.Reasoning
		// when Adjustments is unexpectedly empty.
		Reasoning: firstAdjustmentReasoning(legacy),
	}

	out := AdjusterOutput{
		LedgerEntries: []entities.LedgerEntry{{
			Timestamp:  now,
			AdjusterID: adjusterIDB1OperatingLeaseCapitalization,
			RuleID:     rule.ID,
			Fired:      true,
			// Greppable summary; the load-bearing detail lives on the
			// OverlaySpec.Reasoning. Component / DeltaAmount intentionally
			// unset (OverlayEmitter role per plan §3.5).
			Reasoning: "B1 operating-lease capitalization overlay emitted",
		}},
		Overlays: []entities.OverlaySpec{overlay},
		Flags:    legacy.Flags,
	}

	return out, nil
}

// firstAdjustmentReasoning extracts the canonical legacy reasoning string
// from an *AdjustmentResult — preferring the first Adjustment's Reasoning
// (which carries the rule-specific prefix like "operating_lease_adj:")
// over the result-level Reasoning (which is a summary). Returns empty
// string when the result has no adjustments.
func firstAdjustmentReasoning(legacy *AdjustmentResult) string {
	if legacy == nil {
		return ""
	}
	if len(legacy.Adjustments) > 0 && legacy.Adjustments[0].Reasoning != "" {
		return legacy.Adjustments[0].Reasoning
	}
	return legacy.Reasoning
}

// b1AdjusterOutputToLegacyResult translates the new AdjusterOutput shape
// into the legacy *AdjustmentResult expected by ProcessLiabilityAdjustments'
// existing audit-trail accounting. Mirrors a1AdjusterOutputToLegacyResult —
// B1 is an OverlayEmitter, so the translation reads the lease amount from
// the OverlaySpec.Amount (not from a LedgerEntry DeltaAmount; B1 emits
// none on the LedgerEntry per the OverlayEmitter convention).
func b1AdjusterOutputToLegacyResult(out AdjusterOutput, rule *entities.CleaningRule) *AdjustmentResult {
	// Locate the firing OverlaySpec — B1 emits exactly one when fired and
	// zero when skipped (skip paths produce a Fired:false LedgerEntry only).
	for _, overlay := range out.Overlays {
		if overlay.OverlayID != adjusterIDB1OperatingLeaseCapitalization {
			continue
		}
		adjustment := entities.Adjustment{
			ID:          fmt.Sprintf("lease-pv-adj-%d", time.Now().UnixNano()),
			RuleID:      rule.ID,
			Category:    entities.LiabilityCompleteness,
			Type:        entities.TreatAsDebt,
			Amount:      overlay.Amount,
			FromAccount: "OperatingLeaseCommitments",
			ToAccount:   "InterestBearingDebt",
			Reasoning:   overlay.Reasoning,
			Applied:     true,
			Timestamp:   time.Now(),
		}
		return &AdjustmentResult{
			Amount:      overlay.Amount,
			Applied:     true,
			Adjustments: []entities.Adjustment{adjustment},
			Flags:       out.Flags,
			Reasoning:   fmt.Sprintf("operating_lease_adj: Capitalized %.0f operating lease commitments to debt", overlay.Amount),
		}
	}

	// Skipped path — surface the SkipReason from the Fired:false LedgerEntry
	// for parity with the legacy "no adjustment" branches.
	reasoning := "No operating lease data available"
	for _, entry := range out.LedgerEntries {
		if entry.AdjusterID == adjusterIDB1OperatingLeaseCapitalization {
			reasoning = entry.SkipReason
			if reasoning == "" {
				reasoning = entry.Reasoning
			}
			break
		}
	}
	return &AdjustmentResult{
		Amount:      0.0,
		Applied:     false,
		Adjustments: []entities.Adjustment{},
		Flags:       []entities.Flag{},
		Reasoning:   reasoning,
	}
}

// ProcessOperatingLeaseAdjustment implements B1 rule: Operating lease present value calculation
func (la *LiabilityAdjuster) ProcessOperatingLeaseAdjustment(data *entities.FinancialData, rule *entities.CleaningRule, cleaningContext *entities.CleaningContext) *AdjustmentResult {
	// Step 1: Calculate present value of operating lease commitments using sophisticated engine
	ctx := context.Background() // TODO: Use proper context from caller

	presentValueResult, err := la.leaseCalculator.CalculatePresentValue(ctx, data, cleaningContext)
	if err != nil {
		// Fallback to simple capitalization if PV calculation fails
		return la.fallbackToSimpleCapitalization(data, rule, cleaningContext, err)
	}

	// Step 2: Validate present value result
	if presentValueResult.PresentValue <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No meaningful operating lease present value calculated",
		}
	}

	// Step 3: Calculate lease-to-asset ratio for materiality assessment
	leaseRatio := presentValueResult.PresentValue / data.TotalAssets

	// Step 4: Industry-specific threshold application
	threshold := la.getLeaseThresholdForIndustry(cleaningContext.IndustryCode)

	// Step 5: Create comprehensive adjustment record
	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("lease-pv-adj-%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.LiabilityCompleteness,
		Type:        entities.TreatAsDebt,
		Amount:      presentValueResult.PresentValue,
		FromAccount: "OperatingLeaseCommitments",
		ToAccount:   "InterestBearingDebt",
		Percentage:  leaseRatio * 100,
		Reasoning: fmt.Sprintf("operating_lease_adj: Present value of operating lease commitments (%.1f%% of assets) calculated using %s method with %.1f%% discount rate over %d years",
			leaseRatio*100, presentValueResult.CalculationMethod, presentValueResult.DiscountRate*100, presentValueResult.LeaseTermYears),
		Applied:   true,
		Timestamp: time.Now(),
	}

	// Step 6: Generate comprehensive flags based on calculation quality and materiality
	var flags []entities.Flag

	// Add calculation quality flag
	if presentValueResult.EstimationQuality == "low" || presentValueResult.EstimationQuality == "very_low" {
		flag := entities.Flag{
			ID:         fmt.Sprintf("lease-quality-flag-%d", time.Now().UnixNano()),
			RuleID:     rule.ID,
			Type:       "lease_calculation_quality",
			Severity:   la.getSeverityForQuality(presentValueResult.EstimationQuality),
			Amount:     presentValueResult.PresentValue,
			Percentage: presentValueResult.ConfidenceScore * 100,
			Description: fmt.Sprintf("Lease present value calculated with %s quality (%.1f%% confidence)",
				presentValueResult.EstimationQuality, presentValueResult.ConfidenceScore*100),
			Recommendation: la.getQualityRecommendation(presentValueResult.EstimationQuality),
			Timestamp:      time.Now(),
		}
		flags = append(flags, flag)
	}

	// Add validation warnings if present
	for _, validationFlag := range presentValueResult.ValidationFlags {
		flag := entities.Flag{
			ID:             fmt.Sprintf("lease-validation-flag-%d", time.Now().UnixNano()),
			RuleID:         rule.ID,
			Type:           "lease_validation_warning",
			Severity:       entities.FlagSeverityMedium,
			Amount:         presentValueResult.PresentValue,
			Percentage:     leaseRatio * 100,
			Description:    fmt.Sprintf("Lease calculation validation warning: %s", validationFlag),
			Recommendation: "Review lease commitment data and calculation assumptions",
			Timestamp:      time.Now(),
		}
		flags = append(flags, flag)
	}

	// Add materiality flag if needed
	if leaseRatio >= threshold {
		severity := la.getSeverityForLeaseRatio(leaseRatio, cleaningContext.IndustryCode)

		flag := entities.Flag{
			ID:             fmt.Sprintf("lease-materiality-flag-%d", time.Now().UnixNano()),
			RuleID:         rule.ID,
			Type:           "operating_lease_obligation", // Updated to match test expectations
			Severity:       severity,
			Amount:         presentValueResult.PresentValue,
			Percentage:     leaseRatio * 100,
			Description:    fmt.Sprintf("Material operating lease present value (%.1f%% of assets) added to debt", leaseRatio*100),
			Recommendation: la.getLeaseRecommendation(cleaningContext.IndustryCode, leaseRatio),
			Timestamp:      time.Now(),
		}
		flags = append(flags, flag)
	}

	// Step 7: Build comprehensive reasoning
	reasoning := fmt.Sprintf("Calculated present value of %.0f for operating lease commitments using %s method. "+
		"Discount rate: %.2f%%, Lease term: %d years, Confidence: %.1f%%, Quality: %s",
		presentValueResult.PresentValue,
		presentValueResult.CalculationMethod,
		presentValueResult.DiscountRate*100,
		presentValueResult.LeaseTermYears,
		presentValueResult.ConfidenceScore*100,
		presentValueResult.EstimationQuality)

	// TODO: Add monitoring metrics for calculation performance
	// TODO: Log calculation details for audit trail

	return &AdjustmentResult{
		Amount:      presentValueResult.PresentValue,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       flags,
		Reasoning:   reasoning,
	}
}

// fallbackToSimpleCapitalization provides fallback when PV calculation fails
func (la *LiabilityAdjuster) fallbackToSimpleCapitalization(data *entities.FinancialData, rule *entities.CleaningRule, cleaningContext *entities.CleaningContext, originalError error) *AdjustmentResult {
	// Calculate total operating lease liability from balance sheet
	totalLeaseObligation := data.OperatingLeaseLiability
	if totalLeaseObligation == 0 {
		totalLeaseObligation = data.OperatingLeaseLiabilityCurrent + data.OperatingLeaseLiabilityNoncurrent
	}

	if totalLeaseObligation <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   fmt.Sprintf("Present value calculation failed (%v) and no fallback lease liability available", originalError),
		}
	}

	// Calculate lease-to-asset ratio for materiality assessment
	leaseRatio := totalLeaseObligation / data.TotalAssets

	// Create fallback adjustment record
	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("lease-fallback-adj-%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.LiabilityCompleteness,
		Type:        entities.TreatAsDebt,
		Amount:      totalLeaseObligation,
		FromAccount: "OperatingLeaseObligations",
		ToAccount:   "InterestBearingDebt",
		Percentage:  leaseRatio * 100,
		Reasoning:   fmt.Sprintf("Fallback to book value lease obligations (%.1f%% of assets) due to PV calculation failure", leaseRatio*100),
		Applied:     true,
		Timestamp:   time.Now(),
	}

	// Generate fallback error flag
	var flags []entities.Flag
	flag := entities.Flag{
		ID:             fmt.Sprintf("lease-fallback-flag-%d", time.Now().UnixNano()),
		RuleID:         rule.ID,
		Type:           "lease_calculation_fallback",
		Severity:       entities.FlagSeverityHigh,
		Amount:         totalLeaseObligation,
		Percentage:     leaseRatio * 100,
		Description:    fmt.Sprintf("Present value calculation failed, using book value lease obligations: %v", originalError),
		Recommendation: "Review lease commitment data quality and calculation configuration",
		Timestamp:      time.Now(),
	}
	flags = append(flags, flag)

	return &AdjustmentResult{
		Amount:      totalLeaseObligation,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       flags,
		Reasoning:   fmt.Sprintf("Fallback capitalization of %.0f in operating lease obligations due to PV calculation failure", totalLeaseObligation),
	}
}

// ApplyB2PensionUnderfunding is the Adjuster-shaped (DC-1 Phase 2 PR-4 Task
// 4.2) implementation of the B2 pension / OPEB underfunding rule. Mirrors
// ApplyB1OperatingLeases's structure — produces an AdjusterOutput describing
// what the rule would do (LedgerEntry audit trail + OverlaySpec on
// TotalDebt + significance Flag) but does NOT mutate `working`. The dual-
// write mutation (data.TotalDebt += totalPensionObligation,
// data.InterestBearingDebt += totalPensionObligation) is performed by
// ProcessLiabilityAdjustments' dispatcher so the legacy *AdjustmentResult
// callers stay byte-identical.
//
// Role classification (plan §3.5 / §4 row B2): OverlayEmitter. The fired
// LedgerEntry intentionally carries NO Component / DeltaAmount /
// EquityOffset — the declarative amount lives on OverlaySpec
// (Field:"TotalDebt", Operation:"add"). The audit-only LedgerEntry exists
// for ordering / "did B2 fire?" diagnostics; the Restater fields are left
// zero per the OverlayEmitter convention (mirrors A1 goodwill / B1 leases).
//
// Implementation strategy: delegates to ProcessPensionAdjustment for the
// actual underfunding + OPEB math (PBO − PlanAssets fallback to
// PensionLiabilities, plus OPEBLiability addition) and translates the
// returned *AdjustmentResult into the AdjusterOutput shape. Preserves the
// legacy "pension_adjustment:" reasoning prefix on the OverlaySpec for
// downstream log greppability.
//
// Skipped path emits a Fired:false LedgerEntry with SkipReason
// "No under-funded pension or OPEB obligations present" so observability
// can answer "why didn't B2 fire on this ticker?" without code reading.
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md §"Adjuster output"
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md §3.5 / §4 row B2 / §7 Task 4.2
func (la *LiabilityAdjuster) ApplyB2PensionUnderfunding(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	// ctx accepted for interface symmetry with future industry-aware
	// adjusters; B2 itself uses neither today.
	_ = ctx

	now := time.Now()

	// Delegate to the legacy method for the actual underfunding calculation.
	// This preserves the existing flag taxonomy + reasoning strings bit-for-
	// bit, which is load-bearing for downstream consumers that grep on the
	// "pension_adjustment:" / "pension_underfunding" prefixes.
	legacy := la.ProcessPensionAdjustment(working, rule, cleaningCtx)

	// Skip path: no underfunding / OPEB present. Emit a single Fired:false
	// LedgerEntry so observability can answer "why didn't B2 fire on this
	// ticker?". The legacy method returns a deterministic reasoning string;
	// surface it verbatim as SkipReason.
	if legacy == nil || !legacy.Applied {
		skipReason := "No under-funded pension or OPEB obligations present"
		reasoning := skipReason
		if legacy != nil && legacy.Reasoning != "" {
			skipReason = legacy.Reasoning
			reasoning = legacy.Reasoning
		}
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:  now,
				AdjusterID: adjusterIDB2PensionUnderfunding,
				RuleID:     rule.ID,
				Fired:      false,
				Reasoning:  reasoning,
				SkipReason: skipReason,
			}},
		}, nil
	}

	// Fired path: emit a declarative OverlaySpec on TotalDebt + a Fired:true
	// audit LedgerEntry (no Component / DeltaAmount per OverlayEmitter role)
	// + any flags the legacy path generated. legacy.Amount carries the
	// total pension obligation (underfunding + OPEB).
	overlay := entities.OverlaySpec{
		OverlayID:       adjusterIDB2PensionUnderfunding,
		RuleID:          rule.ID,
		Field:           "TotalDebt",
		Operation:       "add",
		Amount:          legacy.Amount,
		AmountSemantics: entities.AmountIncremental,
		// Preserve the legacy "pension_adjustment:" prefix on the overlay
		// so existing log greps keep working. firstAdjustmentReasoning
		// pulls it from legacy.Adjustments[0].Reasoning.
		Reasoning: firstAdjustmentReasoning(legacy),
	}

	out := AdjusterOutput{
		LedgerEntries: []entities.LedgerEntry{{
			Timestamp:  now,
			AdjusterID: adjusterIDB2PensionUnderfunding,
			RuleID:     rule.ID,
			Fired:      true,
			// Greppable summary; the load-bearing detail lives on the
			// OverlaySpec.Reasoning. Component / DeltaAmount intentionally
			// unset (OverlayEmitter role per plan §3.5).
			Reasoning: "B2 pension/OPEB underfunding overlay emitted",
		}},
		Overlays: []entities.OverlaySpec{overlay},
		Flags:    legacy.Flags,
	}

	return out, nil
}

// b2AdjusterOutputToLegacyResult translates the new AdjusterOutput shape
// into the legacy *AdjustmentResult expected by ProcessLiabilityAdjustments'
// existing audit-trail accounting. Mirrors b1AdjusterOutputToLegacyResult —
// B2 is an OverlayEmitter, so the translation reads the pension amount
// from the OverlaySpec.Amount (not from a LedgerEntry DeltaAmount; B2
// emits none on the LedgerEntry per the OverlayEmitter convention).
func b2AdjusterOutputToLegacyResult(out AdjusterOutput, rule *entities.CleaningRule) *AdjustmentResult {
	// Locate the firing OverlaySpec — B2 emits exactly one when fired and
	// zero when skipped (skip paths produce a Fired:false LedgerEntry only).
	for _, overlay := range out.Overlays {
		if overlay.OverlayID != adjusterIDB2PensionUnderfunding {
			continue
		}
		adjustment := entities.Adjustment{
			ID:          fmt.Sprintf("pension-adj-%d", time.Now().UnixNano()),
			RuleID:      rule.ID,
			Category:    entities.LiabilityCompleteness,
			Type:        entities.TreatAsDebt,
			Amount:      overlay.Amount,
			FromAccount: "PensionUnderfunding",
			ToAccount:   "InterestBearingDebt",
			Reasoning:   overlay.Reasoning,
			Applied:     true,
			Timestamp:   time.Now(),
		}
		return &AdjustmentResult{
			Amount:      overlay.Amount,
			Applied:     true,
			Adjustments: []entities.Adjustment{adjustment},
			Flags:       out.Flags,
			Reasoning:   fmt.Sprintf("Added %.0f in under-funded pension/OPEB obligations to debt", overlay.Amount),
		}
	}

	// Skipped path — surface the SkipReason from the Fired:false LedgerEntry
	// for parity with the legacy "no adjustment" branches.
	reasoning := "No under-funded pension or OPEB obligations present"
	for _, entry := range out.LedgerEntries {
		if entry.AdjusterID == adjusterIDB2PensionUnderfunding {
			reasoning = entry.SkipReason
			if reasoning == "" {
				reasoning = entry.Reasoning
			}
			break
		}
	}
	return &AdjustmentResult{
		Amount:      0.0,
		Applied:     false,
		Adjustments: []entities.Adjustment{},
		Flags:       []entities.Flag{},
		Reasoning:   reasoning,
	}
}

// ApplyB3Contingent is the Adjuster-shaped (DC-1 Phase 2 PR-4 Task 4.3)
// implementation of the B3 contingent-liability rule. Mirrors
// ApplyB1OperatingLeases / ApplyB2PensionUnderfunding's structure — produces
// an AdjusterOutput describing what the rule would do (LedgerEntry audit
// trail + OverlaySpec on DebtLikeClaims + significance Flag) but does NOT
// mutate `working`. The dual-write mutation (data.TotalDebt +=
// weightedAmount, data.InterestBearingDebt += weightedAmount) is performed
// by ProcessLiabilityAdjustments' dispatcher so the legacy
// *AdjustmentResult callers stay byte-identical.
//
// Role classification (plan §3.5 / §4 row B3): OverlayEmitter. The fired
// LedgerEntry intentionally carries NO Component / DeltaAmount /
// EquityOffset — the declarative amount lives on OverlaySpec.
//
// CRITICAL FIELD-vs-MUTATION MISMATCH (spec §"B3 routing correction"
// lines 181-189): the OverlaySpec.Field is "DebtLikeClaims" (NOT
// "TotalDebt") because Phase 4 will flip downstream consumers to read
// Overlays[Field:"DebtLikeClaims"] for the WACC accuracy correction. In
// Phase 2 the dispatcher's dual-write at liabilities.go:87-88 STILL
// mutates data.TotalDebt — Phase 4 deletes that mutation. The mismatch
// is intentional and documented; do NOT "fix" it by aligning Field to
// "TotalDebt".
//
// AIProvenance best-effort capture (Q4 resolution per plan §10): when
// la.aiEnabled && la.aiService != nil AND the AI call succeeds, ModelName /
// Confidence / Probability / ExtractedSpan / Timestamp are populated on
// the OverlaySpec. PromptHash + SourceDocHash stay empty string with a
// TODO Phase 3 marker — today's ai.AnalyzeFootnote does not return prompt
// or source-doc hashes; Phase 3 adds the hashing alongside view-
// reconstruction work where the hashes are actually consumed for replay
// determinism. Rule-based / AI-disabled / AI-failed paths set
// OverlaySpec.AIProvenance = nil (only AI-derived overlays carry
// provenance).
//
// Context propagation: this is the FIRST Apply method in PR-2/PR-3/PR-4
// that genuinely uses `ctx` — the AI path threads it down to
// ai.AnalyzeFootnote(ctx, ...) via the analyzeContingentLiabilityWithAI
// helper. The helper still calls context.Background() today; PR-4 Task
// 4.3 widens its signature to accept ctx and uses it (Phase 3 may further
// thread the cleaner's root ctx if useful).
//
// Implementation strategy: delegates to ProcessContingentLiabilityAdjustment
// for the aggregation + probability-weighting math (including the AI vs.
// rule-based decision branch) and translates the returned
// *AdjustmentResult into the AdjusterOutput shape. To capture the AI
// response fields needed for AIProvenance — which the legacy method
// discards into a metadata map — Apply also performs a parallel AI call
// when conditions are met, then merges the response into the OverlaySpec.
// This duplicates ONE AI call per fired B3 invocation in the migration
// window; Phase 3 collapses the duplication when the legacy method is
// deleted.
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md §"B3 routing correction"
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md §3.5 / §4 row B3 / §7 Task 4.3 / §10 Q4
func (la *LiabilityAdjuster) ApplyB3Contingent(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	now := time.Now()

	// Delegate to the legacy method for the actual probability-weighting
	// math (including AI-vs-conservative decision logic). This preserves
	// the existing flag taxonomy + reasoning strings bit-for-bit, which is
	// load-bearing for downstream consumers that grep on the
	// "contingent_liabilities:" / "contingent_liability_exposure" prefixes.
	legacy := la.ProcessContingentLiabilityAdjustment(working, rule, cleaningCtx)

	// Skip path: no contingent-liability data disclosed (or all sources
	// zero). Emit a single Fired:false LedgerEntry so observability can
	// answer "why didn't B3 fire on this ticker?".
	if legacy == nil || !legacy.Applied {
		skipReason := "No contingent liabilities disclosed to assess"
		reasoning := skipReason
		if legacy != nil && legacy.Reasoning != "" {
			skipReason = legacy.Reasoning
			reasoning = legacy.Reasoning
		}
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:  now,
				AdjusterID: adjusterIDB3ContingentLiability,
				RuleID:     rule.ID,
				Fired:      false,
				Reasoning:  reasoning,
				SkipReason: skipReason,
			}},
		}, nil
	}

	// AI provenance capture (best-effort) — when AI fired during legacy
	// processing, run a parallel AnalyzeFootnote call here so we can
	// stamp ModelName / Confidence / Probability / ExtractedSpan /
	// Timestamp on the OverlaySpec. We deliberately re-invoke (rather
	// than restructuring ProcessContingentLiabilityAdjustment to return
	// the response) because Phase 3 will delete the legacy method
	// entirely; restructuring it now adds migration churn for code that
	// is going to be replaced. The duplication only fires when AI is
	// enabled — disabled-AI tenants pay zero extra cost.
	//
	// On AI failure here, we silently fall through to AIProvenance:nil —
	// the legacy method already absorbed AI failures into a conservative
	// rule-based amount (which has no AI provenance), so dropping
	// provenance on this branch matches the semantic invariant "only AI-
	// derived amounts carry provenance".
	var aiProvenance *entities.AIProvenance
	if la.aiEnabled && la.aiService != nil && (cleaningCtx.FootnoteText != "" || (working.ContingentLiabilities+working.EnvironmentalLiabilities+working.LitigationLiabilities) > 0) {
		aiProv, aiErr := la.captureB3AIProvenance(ctx, working, cleaningCtx, now)
		if aiErr == nil && aiProv != nil {
			aiProvenance = aiProv
		}
		// Silent fallthrough on AI error — the legacy path already used
		// the conservative 40% fallback for its amount; provenance is
		// nil because the recorded amount is not AI-derived.
	}

	// Fired path: emit a declarative OverlaySpec on DebtLikeClaims + a
	// Fired:true audit LedgerEntry (no Component / DeltaAmount per
	// OverlayEmitter role) + any flags the legacy path generated.
	// legacy.Amount carries the probability-weighted contingent amount.
	overlay := entities.OverlaySpec{
		OverlayID: adjusterIDB3ContingentLiability,
		RuleID:    rule.ID,
		// Phase 4 routing intent — spec §"B3 routing correction" lines
		// 181-189. Phase 2 dispatcher's dual-write still mutates
		// data.TotalDebt; Phase 4 flips consumers to read this overlay
		// via InvestedCapital.DebtLikeClaims and deletes the dual-write.
		Field:           "DebtLikeClaims",
		Operation:       "add",
		Amount:          legacy.Amount,
		AmountSemantics: entities.AmountIncremental,
		// Preserve the legacy "contingent_liabilities:" prefix on the
		// overlay so existing log greps keep working.
		// firstAdjustmentReasoning pulls it from
		// legacy.Adjustments[0].Reasoning.
		Reasoning:    firstAdjustmentReasoning(legacy),
		AIProvenance: aiProvenance, // nil when AI did not produce the amount
	}

	out := AdjusterOutput{
		LedgerEntries: []entities.LedgerEntry{{
			Timestamp:  now,
			AdjusterID: adjusterIDB3ContingentLiability,
			RuleID:     rule.ID,
			Fired:      true,
			// Greppable summary; the load-bearing detail lives on the
			// OverlaySpec.Reasoning. Component / DeltaAmount intentionally
			// unset (OverlayEmitter role per plan §3.5).
			Reasoning: "B3 contingent-liability overlay emitted",
		}},
		Overlays: []entities.OverlaySpec{overlay},
		Flags:    legacy.Flags,
	}

	return out, nil
}

// captureB3AIProvenance runs a focused AnalyzeFootnote call to extract the
// AIProvenance fields that the legacy ProcessContingentLiabilityAdjustment
// path discards. Returns nil + nil when AI succeeds but produces no usable
// provenance signal; returns nil + err on AI service errors so the caller
// can choose to silently fall through.
//
// PromptHash + SourceDocHash are deliberately left empty per Q4 (plan §10):
// today's ai.AnalyzeFootnote does not expose prompt or source-document
// hashes, and Phase 2 accepted empty hashes with a Phase 3 TODO.
//
// Nil-ctx tolerance: the legacy `analyzeContingentLiabilityWithAI` uses
// `context.Background()` because the dispatcher's `applyCtx` is `nil`
// today (its public signature does not accept a ctx — see PR-4 TODO at
// liabilities.go:204). To preserve the legacy AI-call invariant and
// avoid nil-deref panics inside MockAIService.AnalyzeFootnote (which
// calls ctx.Err()), promote nil ctx to context.Background() here too.
// Phase 3's planned ctx threading collapses this branch.
func (la *LiabilityAdjuster) captureB3AIProvenance(ctx context.Context, data *entities.FinancialData, cleaningCtx *entities.CleaningContext, timestamp time.Time) (*entities.AIProvenance, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Build the request shape used by the legacy AI helper — same fields
	// so the AI service receives identical context across both call sites
	// during the migration window. Phase 3 deletes the legacy call site
	// and this helper becomes the only invocation.
	footnoteText := cleaningCtx.FootnoteText
	if footnoteText == "" {
		footnoteText = fmt.Sprintf("Company disclosed contingent liabilities of $%.0f related to litigation and other potential exposures.",
			data.ContingentLiabilities+data.EnvironmentalLiabilities+data.LitigationLiabilities)
	}

	request := &ai.FootnoteAnalysisRequest{
		Ticker:           data.Ticker,
		FilingType:       data.FilingPeriod,
		FootnoteText:     footnoteText,
		AnalysisType:     ai.ContingentLiabilityAnalysis,
		PriorityLevel:    ai.PriorityNormal,
		RequestTimestamp: timestamp,
		Context: map[string]interface{}{
			"industry_code":           cleaningCtx.IndustryCode,
			"total_contingent_amount": data.ContingentLiabilities + data.EnvironmentalLiabilities + data.LitigationLiabilities,
			"revenue":                 data.Revenue,
		},
	}

	response, err := la.aiService.AnalyzeFootnote(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("AI provenance capture failed: %w", err)
	}
	if response == nil {
		return nil, fmt.Errorf("AI service returned nil response")
	}
	if response.Error != "" {
		return nil, fmt.Errorf("AI service returned error: %s", response.Error)
	}

	// Pull the ContingentLiabilityEstimate so we can stamp Probability +
	// ExtractedSpan. Handle both the direct-struct form (mock service) and
	// the map form (HTTP service) — mirrors the legacy helper's branching.
	var probability float64
	var extractedSpan string
	if extractedData, ok := response.ExtractedData["contingent_liability_estimate"]; ok {
		switch est := extractedData.(type) {
		case ai.ContingentLiabilityEstimate:
			probability = est.ProbabilityPercent / 100.0
			if len(est.SupportingEvidence) > 0 {
				extractedSpan = est.SupportingEvidence[0]
			}
		case map[string]interface{}:
			if prob, ok := est["probability_percent"].(float64); ok {
				probability = prob / 100.0
			}
			if spans, ok := est["supporting_evidence"].([]interface{}); ok && len(spans) > 0 {
				if s, ok := spans[0].(string); ok {
					extractedSpan = s
				}
			}
		}
	}

	return &entities.AIProvenance{
		ModelName:     b3AIModelName,
		PromptHash:    "", // TODO Phase 3: compute SHA-256 of prompt template (Q4 per plan §10)
		SourceDocHash: "", // TODO Phase 3: compute SHA-256 of footnote text (Q4 per plan §10)
		ExtractedSpan: extractedSpan,
		Probability:   probability,
		Confidence:    response.Confidence,
		Timestamp:     timestamp,
	}, nil
}

// b3AdjusterOutputToLegacyResult translates the new AdjusterOutput shape
// into the legacy *AdjustmentResult expected by ProcessLiabilityAdjustments'
// existing audit-trail accounting. Mirrors b1AdjusterOutputToLegacyResult /
// b2AdjusterOutputToLegacyResult — B3 is an OverlayEmitter, so the
// translation reads the contingent amount from the OverlaySpec.Amount (not
// from a LedgerEntry DeltaAmount; B3 emits none on the LedgerEntry per
// the OverlayEmitter convention).
//
// Note: the emitted legacy Adjustment uses entities.ProbabilityWeighted (the
// canonical B3 type per ProcessContingentLiabilityAdjustment), NOT
// entities.TreatAsDebt — preserves the legacy taxonomy that downstream
// callers (`Adjustment.Type` switches in service.go, etc.) depend on.
func b3AdjusterOutputToLegacyResult(out AdjusterOutput, rule *entities.CleaningRule) *AdjustmentResult {
	// Locate the firing OverlaySpec — B3 emits exactly one when fired and
	// zero when skipped (skip paths produce a Fired:false LedgerEntry only).
	for _, overlay := range out.Overlays {
		if overlay.OverlayID != adjusterIDB3ContingentLiability {
			continue
		}
		adjustment := entities.Adjustment{
			ID:          fmt.Sprintf("contingent-adj-%d", time.Now().UnixNano()),
			RuleID:      rule.ID,
			Category:    entities.LiabilityCompleteness,
			Type:        entities.ProbabilityWeighted,
			Amount:      overlay.Amount,
			FromAccount: "ContingentLiabilities",
			ToAccount:   "EstimatedLiabilities",
			Reasoning:   overlay.Reasoning,
			Applied:     true,
			Timestamp:   time.Now(),
		}
		return &AdjustmentResult{
			Amount:      overlay.Amount,
			Applied:     true,
			Adjustments: []entities.Adjustment{adjustment},
			Flags:       out.Flags,
			Reasoning:   fmt.Sprintf("Applied probability-weighted adjustment of %.0f for contingent liabilities", overlay.Amount),
		}
	}

	// Skipped path — surface the SkipReason from the Fired:false LedgerEntry
	// for parity with the legacy "no adjustment" branches.
	reasoning := "No contingent liabilities disclosed to assess"
	for _, entry := range out.LedgerEntries {
		if entry.AdjusterID == adjusterIDB3ContingentLiability {
			reasoning = entry.SkipReason
			if reasoning == "" {
				reasoning = entry.Reasoning
			}
			break
		}
	}
	return &AdjustmentResult{
		Amount:      0.0,
		Applied:     false,
		Adjustments: []entities.Adjustment{},
		Flags:       []entities.Flag{},
		Reasoning:   reasoning,
	}
}

// ProcessPensionAdjustment implements B2 rule: Under-funded pension obligations as debt
func (la *LiabilityAdjuster) ProcessPensionAdjustment(data *entities.FinancialData, rule *entities.CleaningRule, context *entities.CleaningContext) *AdjustmentResult {
	// Calculate pension underfunding
	var pensionUnderfunding float64
	if data.ProjectedBenefitObligation > 0 && data.PensionPlanAssets > 0 {
		underfunding := data.ProjectedBenefitObligation - data.PensionPlanAssets
		if underfunding > 0 {
			pensionUnderfunding = underfunding
		}
	} else if data.PensionLiabilities > 0 {
		// Use net pension liability if PBO/plan assets not available
		pensionUnderfunding = data.PensionLiabilities
	}

	// Add OPEB liability
	totalPensionObligation := pensionUnderfunding + data.OPEBLiability

	if totalPensionObligation <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No under-funded pension or OPEB obligations present",
		}
	}

	// Calculate pension-to-revenue ratio for assessment
	pensionRatio := totalPensionObligation / data.Revenue

	// Create adjustment record
	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("pension-adj-%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.LiabilityCompleteness,
		Type:        entities.TreatAsDebt,
		Amount:      totalPensionObligation,
		FromAccount: "PensionUnderfunding",
		ToAccount:   "InterestBearingDebt",
		Percentage:  pensionRatio * 100,
		Reasoning:   fmt.Sprintf("pension_adjustment: Added under-funded pension/OPEB obligations (%.1f%% of revenue) to debt per B2 rule", pensionRatio*100),
		Applied:     true,
		Timestamp:   time.Now(),
	}

	// Generate flags for material pension obligations
	var flags []entities.Flag
	threshold := la.getPensionThresholdForIndustry(context.IndustryCode)

	if pensionRatio >= threshold {
		severity := la.getSeverityForPensionRatio(pensionRatio, context.IndustryCode)

		flag := entities.Flag{
			ID:             fmt.Sprintf("pension-flag-%d", time.Now().UnixNano()),
			RuleID:         rule.ID,
			Type:           "pension_underfunding",
			Severity:       severity,
			Amount:         totalPensionObligation,
			Percentage:     pensionRatio * 100,
			Description:    fmt.Sprintf("Material pension underfunding (%.1f%% of revenue) added to debt", pensionRatio*100),
			Recommendation: "Monitor pension funding status and potential cash flow impact from required contributions",
			Timestamp:      time.Now(),
		}
		flags = append(flags, flag)
	}

	return &AdjustmentResult{
		Amount:      totalPensionObligation,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       flags,
		Reasoning:   fmt.Sprintf("Added %.0f in under-funded pension/OPEB obligations to debt", totalPensionObligation),
	}
}

// ProcessContingentLiabilityAdjustment implements B3 rule: Contingent liability estimation
//
// DC-1 Phase 2 PR-4 Task 4.3: the parameter previously named `context` was
// renamed to `cleaningCtx` to unshadow the `context` package identifier.
// The function body uses the standard Go `context.Context` type via the
// `analyzeContingentLiabilityWithAI` helper; without the rename, the
// package identifier would be inaccessible inside this function (and
// inside any future ctx-aware refactor of the helper).
func (la *LiabilityAdjuster) ProcessContingentLiabilityAdjustment(data *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) *AdjustmentResult {
	// Aggregate all contingent liability sources
	totalContingentLiability := data.ContingentLiabilities +
		data.EnvironmentalLiabilities +
		data.LitigationLiabilities

	if totalContingentLiability <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No contingent liabilities disclosed to assess",
		}
	}

	// Determine probability weighting: AI-enhanced or conservative fallback
	var probabilityWeight float64
	var reasoningPrefix string

	if la.aiEnabled && la.aiService != nil && (cleaningCtx.FootnoteText != "" || totalContingentLiability > 0) {
		// Attempt AI-powered analysis of footnotes
		aiProbability, aiMetadata, err := la.analyzeContingentLiabilityWithAI(data, cleaningCtx)
		if err != nil {
			// AI failed - use baseline conservative probability (40%) independent of industry
			probabilityWeight = 0.40
			reasoningPrefix = fmt.Sprintf("AI analysis failed (%v), using conservative", err)
		} else {
			// AI succeeded - use AI probability and capture metadata
			probabilityWeight = aiProbability
			reasoningPrefix = "AI analysis of footnotes"
			// Store AI metadata in the cleaning context for propagation to result
			if cleaningCtx.AIMetadata == nil {
				cleaningCtx.AIMetadata = make(map[string]string)
			}
			for k, v := range aiMetadata {
				cleaningCtx.AIMetadata[k] = v
			}
		}
	} else {
		// AI disabled or no footnotes - use conservative approach
		probabilityWeight = la.getContingentLiabilityProbability(cleaningCtx.IndustryCode, totalContingentLiability)
		reasoningPrefix = "Conservative"
	}

	weightedAmount := totalContingentLiability * probabilityWeight

	// Calculate contingent liability ratios for materiality assessment
	originalRatio := totalContingentLiability / data.Revenue // Use original amount for materiality
	weightedRatio := weightedAmount / data.Revenue           // Use weighted amount for reporting

	// Create adjustment record
	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("contingent-adj-%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.LiabilityCompleteness,
		Type:        entities.ProbabilityWeighted,
		Amount:      weightedAmount,
		FromAccount: "ContingentLiabilities",
		ToAccount:   "EstimatedLiabilities",
		Percentage:  weightedRatio * 100,
		Reasoning:   fmt.Sprintf("contingent_liabilities: %s applied %.0f%% probability weighting to contingent liabilities (%.1f%% of revenue) per B3 rule", reasoningPrefix, probabilityWeight*100, originalRatio*100),
		Applied:     true,
		Timestamp:   time.Now(),
	}

	// Generate flags for material contingent exposures based on original ratio
	var flags []entities.Flag
	threshold := la.getContingentLiabilityThreshold(cleaningCtx.IndustryCode)

	if originalRatio >= threshold {
		severity := la.getSeverityForContingentRatio(originalRatio, cleaningCtx.IndustryCode)

		flag := entities.Flag{
			ID:             fmt.Sprintf("contingent-flag-%d", time.Now().UnixNano()),
			RuleID:         rule.ID,
			Type:           "contingent_liability_exposure",
			Severity:       severity,
			Amount:         weightedAmount,
			Percentage:     originalRatio * 100,
			Description:    fmt.Sprintf("Material contingent liability exposure (%.1f%% of revenue) with %.0f%% probability weighting", originalRatio*100, probabilityWeight*100),
			Recommendation: la.getContingentLiabilityRecommendation(cleaningCtx.IndustryCode),
			Timestamp:      time.Now(),
		}
		flags = append(flags, flag)
	}

	return &AdjustmentResult{
		Amount:      weightedAmount,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       flags,
		Reasoning:   fmt.Sprintf("Applied probability-weighted adjustment of %.0f for contingent liabilities", weightedAmount),
	}
}

// Industry-specific threshold and severity methods

func (la *LiabilityAdjuster) getLeaseThresholdForIndustry(industryCode string) float64 {
	switch industryCode {
	case "44": // Retail Trade - high lease tolerance
		return 0.15 // 15% threshold
	case "45": // Technology - low lease tolerance
		return 0.08 // 8% threshold
	case "31", "32", "33": // Manufacturing - moderate tolerance
		return 0.12 // 12% threshold
	case "52": // Financial Services - minimal leases expected
		return 0.05 // 5% threshold
	default:
		return 0.10 // 10% default threshold
	}
}

func (la *LiabilityAdjuster) getPensionThresholdForIndustry(industryCode string) float64 {
	switch industryCode {
	case "22": // Utilities - typically high pension exposure
		return 0.08 // 8% of revenue threshold
	case "31", "32", "33": // Manufacturing - moderate pension exposure
		return 0.05 // 5% of revenue threshold
	case "45": // Technology - typically minimal pension exposure
		return 0.02 // 2% of revenue threshold
	default:
		return 0.03 // 3% default threshold
	}
}

func (la *LiabilityAdjuster) getContingentLiabilityThreshold(industryCode string) float64 {
	switch industryCode {
	case "21": // Energy - high environmental/regulatory exposure
		return 0.03 // 3% of revenue threshold
	case "62": // Healthcare - high litigation exposure
		return 0.03 // 3% of revenue threshold
	case "45": // Technology - patent litigation exposure
		return 0.02 // 2% of revenue threshold
	default:
		return 0.01 // 1% default threshold
	}
}

func (la *LiabilityAdjuster) getSeverityForLeaseRatio(ratio float64, industryCode string) entities.FlagSeverity {
	threshold := la.getLeaseThresholdForIndustry(industryCode)

	if ratio >= threshold*2.0 {
		return entities.FlagSeverityHigh
	} else if ratio >= threshold*1.5 {
		return entities.FlagSeverityMedium
	}
	return entities.FlagSeverityLow
}

func (la *LiabilityAdjuster) getSeverityForPensionRatio(ratio float64, industryCode string) entities.FlagSeverity {
	if ratio >= 0.15 { // 15% of revenue is critical
		return entities.FlagSeverityCritical
	} else if ratio >= 0.08 { // 8% is high
		return entities.FlagSeverityHigh
	} else if ratio >= 0.05 { // 5% is medium
		return entities.FlagSeverityMedium
	}
	return entities.FlagSeverityLow
}

func (la *LiabilityAdjuster) getSeverityForContingentRatio(ratio float64, industryCode string) entities.FlagSeverity {
	threshold := la.getContingentLiabilityThreshold(industryCode)

	if ratio >= threshold*3.0 {
		return entities.FlagSeverityCritical
	} else if ratio >= threshold*2.0 {
		return entities.FlagSeverityHigh
	} else if ratio >= threshold*1.5 {
		return entities.FlagSeverityMedium
	}
	return entities.FlagSeverityLow
}

func (la *LiabilityAdjuster) getContingentLiabilityProbability(industryCode string, amount float64) float64 {
	// Use industry-specific probability from classifier if available
	// TODO: Replace with AI-powered footnote analysis for more precise estimates

	// Try to get probability from industry classifier first
	if la.industryClassifier != nil {
		if sectorConfig, exists := la.industryClassifier.GetSectorConfig(industryCode); exists {
			return sectorConfig.Thresholds.ContingentLiabilityRate
		}
	}

	// Fallback to GICS sector code mapping for known sectors
	switch industryCode {
	case "45": // Information Technology - patent disputes often settled
		return 0.40 // 40% probability (conservative for tech)
	case "20": // Industrials/Manufacturing - higher probability due to operations
		return 0.70 // 70% probability (matches industry classifier config)
	case "25": // Consumer Discretionary/Retail - moderate probability
		return 0.65 // 65% probability (matches industry classifier config)
	case "21": // Energy - environmental liabilities often materialize
		return 0.60 // 60% probability
	case "62": // Healthcare - litigation often settled
		return 0.50 // 50% probability
	default:
		return 0.30 // 30% conservative default
	}
}

func (la *LiabilityAdjuster) getLeaseRecommendation(industryCode string, ratio float64) string {
	switch industryCode {
	case "44": // Retail
		return "Monitor lease obligation trends and renewal terms, especially for store locations"
	case "45": // Technology
		return "Evaluate lease commitments against asset utilization and growth projections"
	case "31", "32", "33": // Manufacturing
		return "Assess equipment lease obligations and potential purchase options"
	default:
		return "Review lease portfolio for optimization opportunities and renewal risks"
	}
}

func (la *LiabilityAdjuster) getContingentLiabilityRecommendation(industryCode string) string {
	switch industryCode {
	case "21": // Energy
		return "Monitor environmental remediation progress and regulatory developments"
	case "62": // Healthcare
		return "Track litigation settlement patterns and establish appropriate reserves"
	case "45": // Technology
		return "Assess patent portfolio risks and consider defensive strategies"
	default:
		return "Regularly evaluate contingent liability exposure and disclosure adequacy"
	}
}

// getSeverityForQuality returns flag severity based on estimation quality
func (la *LiabilityAdjuster) getSeverityForQuality(quality string) entities.FlagSeverity {
	switch quality {
	case "high":
		return entities.FlagSeverityLow
	case "medium":
		return entities.FlagSeverityMedium
	case "low":
		return entities.FlagSeverityHigh
	case "very_low":
		return entities.FlagSeverityCritical
	default:
		return entities.FlagSeverityMedium
	}
}

// getQualityRecommendation returns recommendation based on estimation quality
func (la *LiabilityAdjuster) getQualityRecommendation(quality string) string {
	switch quality {
	case "high":
		return "Lease present value calculation is highly reliable"
	case "medium":
		return "Consider obtaining additional lease commitment details for improved accuracy"
	case "low":
		return "Review lease commitment disclosures and consider manual verification"
	case "very_low":
		return "Lease calculation has significant uncertainty - recommend detailed analysis"
	default:
		return "Review lease calculation inputs and methodology"
	}
}

// analyzeContingentLiabilityWithAI performs AI-powered analysis of footnotes to determine
// more accurate contingent liability probability estimates
func (la *LiabilityAdjuster) analyzeContingentLiabilityWithAI(data *entities.FinancialData, cleaningCtx *entities.CleaningContext) (float64, map[string]string, error) {
	ctx := context.Background() // TODO: Extract from cleaning context if available

	// Prepare AI analysis request
	footnoteText := cleaningCtx.FootnoteText
	if footnoteText == "" {
		// For testing: generate synthetic footnote text when none provided
		footnoteText = fmt.Sprintf("Company disclosed contingent liabilities of $%.0f related to litigation and other potential exposures.",
			data.ContingentLiabilities+data.EnvironmentalLiabilities+data.LitigationLiabilities)
	}

	request := &ai.FootnoteAnalysisRequest{
		Ticker:           data.Ticker,
		FilingType:       data.FilingPeriod, // Use filing period as proxy for filing type
		FootnoteText:     footnoteText,
		AnalysisType:     ai.ContingentLiabilityAnalysis,
		PriorityLevel:    ai.PriorityNormal,
		RequestTimestamp: time.Now(),
		Context: map[string]interface{}{
			"industry_code":           cleaningCtx.IndustryCode,
			"total_contingent_amount": data.ContingentLiabilities + data.EnvironmentalLiabilities + data.LitigationLiabilities,
			"revenue":                 data.Revenue,
		},
	}

	// Call AI service
	response, err := la.aiService.AnalyzeFootnote(ctx, request)
	if err != nil {
		return 0.0, nil, fmt.Errorf("AI service call failed: %w", err)
	}

	if response.Error != "" {
		return 0.0, nil, fmt.Errorf("AI service returned error: %s", response.Error)
	}

	// Extract contingent liability estimate from AI response
	extractedData, ok := response.ExtractedData["contingent_liability_estimate"]
	if !ok {
		return 0.0, nil, fmt.Errorf("AI response missing contingent liability estimate")
	}

	// Convert extracted data to ContingentLiabilityEstimate
	var estimate ai.ContingentLiabilityEstimate

	// Handle both struct and map formats (for different AI service implementations)
	if estimateStruct, ok := extractedData.(ai.ContingentLiabilityEstimate); ok {
		// Direct struct from mock AI service
		estimate = estimateStruct
	} else if estimateData, ok := extractedData.(map[string]interface{}); ok {
		// Map format from HTTP AI service
		if prob, ok := estimateData["probability_percent"].(float64); ok {
			estimate.ProbabilityPercent = prob
			estimate.ConfidenceLevel = response.Confidence
		} else {
			return 0.0, nil, fmt.Errorf("AI response missing probability percentage")
		}
	} else {
		return 0.0, nil, fmt.Errorf("AI response has invalid format: expected ContingentLiabilityEstimate or map[string]interface{}, got %T", extractedData)
	}

	// Validate AI probability estimate
	probability := estimate.ProbabilityPercent / 100.0 // Convert percentage to decimal
	if probability < 0.0 || probability > 1.0 {
		return 0.0, nil, fmt.Errorf("AI returned invalid probability: %.2f%%", estimate.ProbabilityPercent)
	}

	// Create metadata for tracking
	metadata := map[string]string{
		"ai_confidence":      fmt.Sprintf("%.2f", response.Confidence),
		"ai_model_used":      "footnote_analysis", // TODO: Get actual model from config
		"ai_processing_time": response.ProcessingTime.String(),
		"ai_probability":     fmt.Sprintf("%.2f%%", estimate.ProbabilityPercent),
		"analysis_type":      string(response.AnalysisType),
		"request_id":         response.RequestID,
	}

	return probability, metadata, nil
}
