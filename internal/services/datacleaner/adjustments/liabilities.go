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

// LiabilityAdjustmentResult is the slim native carrier returned by
// ProcessLiabilityAdjustments.
//
// DC-1 Phase 5 P5-C4 deleted the legacy translator stack and the
// translator-fed fields (Applied / TotalLiabilityAdjustment /
// AdjustedTotalDebt / Adjustments / AuditTrail). The cleaner orchestrator
// consumes ONLY the native emissions: it drains NativeLedgerEntries onto
// data.AdjustmentLedger and NativeOverlays onto data.Overlays (preserving
// liability-category ordering), derives the firing signal via nativeFired(...),
// and projects the public entities.Adjustment audit trail from the ledger via
// adjustmentsFromLedger. Flags carries the category's collected risk flags.
//
// Mirrors the slimmed AssetAdjustmentResult / EarningsAdjustmentResult.
type LiabilityAdjustmentResult struct {
	Flags                  []entities.Flag        `json:"flags"`
	NativeLedgerEntries    []entities.LedgerEntry `json:"-"`
	NativeOverlays         []entities.OverlaySpec `json:"-"`
	NativelyEmittedRuleIDs map[string]bool        `json:"-"`
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
// DC-1 Phase 2 PR-4 Task 4.4 (Option α absorption — SHIPPED): the dual-write
// that used to live in a post-switch `if result != nil && result.Applied`
// block (mutating data.TotalDebt += result.Amount and data.InterestBearingDebt
// += result.Amount uniformly) has been MOVED INTO each per-rule switch arm
// via a small helper closure `dualWrite` that mutates the working
// FinancialData when the result fires. Mutation source remains
// `result.Amount` for byte-for-byte parity with the pre-Task-4.4 behavior;
// for the native OverlayEmitter path of B1/B2/B3 this is equal to
// `out.Overlays[0].Amount` (see b{1,2,3}AdjusterOutputToLegacyResult, which
// sets result.Amount = overlay.Amount when fired). The dual-write firing
// order B1→B2→B3 is preserved because the rule iteration order is unchanged
// and the helper is invoked at the same logical point in each arm. The
// mutation REMAINS load-bearing for the DDM legacy path (JPM bit-for-bit
// invariant — DDM reads data.TotalDebt directly); Phase 4 deletes it when
// downstream consumers read views/overlays instead. The post-switch
// `data.TotalDebt += result.Amount` line is GONE; what remains is aggregate
// accumulation (allAdjustments, allFlags, totalAdjustment) which is NOT a
// mutation of `data` and therefore stays at the rule-loop level.
//
// Parameter `cleaningCtx` was historically named `context` here; PR-4 Task
// 4.1 renames it so the `context` package identifier is unshadowed inside
// the function body — required for the new Adjuster.Apply call site.
func (la *LiabilityAdjuster) ProcessLiabilityAdjustments(ctx context.Context, data *entities.FinancialData, rules []*entities.CleaningRule, cleaningCtx *entities.CleaningContext) *LiabilityAdjustmentResult {
	var allFlags []entities.Flag

	// Phase 2 PR-4 native emissions — collected here in rule-iteration order
	// so the orchestrator can append them to data.AdjustmentLedger in
	// position. The set NativelyEmittedRuleIDs records which rules emitted
	// natively (consumed by per-rule contract tests).
	var nativeLedger []entities.LedgerEntry
	var nativeOverlays []entities.OverlaySpec
	nativelyEmittedRuleIDs := make(map[string]bool, len(rules))

	// DC-1 Phase 3 (Task 3.9 + followup F.2): ctx is threaded through the
	// public signature from service.go::applyActiveAdjustments. B3's AI
	// path (analyzeContingentLiabilityWithAI) uses this ctx to respect
	// request-scoped cancellation against the upstream AI service —
	// previously the amount-path call hard-coded context.Background()
	// and ignored cancellation (HIGH-3 fix).
	applyCtx := ctx

	// DC-1 Phase 4 (C-4 / B3 routing flip, §8.2.1 Option A): the B-rule debt
	// dual-write is DELETED. B1 (lease) / B2 (pension) / B3 (contingent) are
	// OverlayEmitters — their monetary effect lives on the OverlaySpec (drained
	// into data.Overlays via NativeOverlays) and is realized at the view level
	// by cleaneddata.InvestedCapital().DebtLikeClaims, which the EV→Equity
	// bridge subtracts. They NO LONGER inflate data.TotalDebt /
	// data.InterestBearingDebt (the WACC capital-structure denominator now reads
	// Restated().InterestBearingDebt, B-rule-free). This is the substantive
	// accuracy correction: contingent/lease/pension claims compete with
	// shareholders for cash flows but are not interest-bearing capital.
	//
	// DC-1 Phase 5 P5-C4: the no-op dualWrite closure + the legacy
	// *AdjustmentResult translator chain are DELETED. The B1→B2→B3 firing
	// order is preserved by the rule-iteration order of the switch below.

	// Process each Category B rule
	for _, rule := range rules {
		if rule.Category != entities.LiabilityCompleteness || !rule.Enabled {
			continue
		}

		switch rule.ID {
		case "operating_leases":
			// DC-1 Phase 2 PR-4 Task 4.1: route B1 through the new
			// Adjuster-shaped ApplyB1OperatingLeases. Apply is mutation-
			// free; the per-arm dualWrite below performs the legacy
			// data.TotalDebt mutation so the legacy *AdjustmentResult
			// callers stay byte-identical AND the AdjusterOutput's
			// LedgerEntries / Overlays / Flags reach the cleaner
			// orchestrator.
			out, err := la.ApplyB1OperatingLeases(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				// Adjuster.Apply errors are not a defined surface today;
				// ApplyB1OperatingLeases never returns one. Skip on a
				// hypothetical future error (the deleted legacy fallback
				// would have bypassed the native path the orchestrator now
				// depends on exclusively).
				continue
			}

			// Record native emissions for the orchestrator. Even when the
			// rule does not "fire" in the legacy sense (Fired:false), the
			// AdjusterOutput carries a Fired:false LedgerEntry that is still
			// load-bearing for "why didn't B1 fire?" observability. The
			// OverlaySpec carries the lease PV for InvestedCapital().DebtLikeClaims.
			allFlags = append(allFlags, out.Flags...)
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
		case "pension_obligations":
			// DC-1 Phase 2 PR-4 Task 4.2: route B2 through the new
			// Adjuster-shaped ApplyB2PensionUnderfunding. Mirrors the B1
			// wiring above — Apply is mutation-free; the per-arm
			// dualWrite below performs the legacy data.TotalDebt mutation
			// so the legacy *AdjustmentResult callers stay byte-identical
			// AND the AdjusterOutput's LedgerEntries / Overlays / Flags
			// reach the cleaner orchestrator.
			out, err := la.ApplyB2PensionUnderfunding(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				// Adjuster.Apply errors are not a defined surface today;
				// ApplyB2PensionUnderfunding never returns one. Skip on a
				// hypothetical future error (see B1 arm rationale).
				continue
			}

			// Record native emissions for the orchestrator. Even when the
			// rule does not "fire" in the legacy sense (Fired:false), the
			// AdjusterOutput carries a Fired:false LedgerEntry that is still
			// load-bearing for "why didn't B2 fire?" observability.
			allFlags = append(allFlags, out.Flags...)
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
		case "contingent_liabilities":
			// DC-1 Phase 2 PR-4 Task 4.3: route B3 through the new
			// Adjuster-shaped ApplyB3Contingent. Mirrors B1/B2 wiring —
			// Apply is mutation-free; the per-arm dualWrite below
			// performs the legacy data.TotalDebt mutation so the legacy
			// *AdjustmentResult callers stay byte-identical AND the
			// AdjusterOutput's LedgerEntries / Overlays / Flags reach
			// the cleaner orchestrator. Unlike B1/B2, the emitted
			// OverlaySpec's Field is "DebtLikeClaims" (Phase 4 routing
			// intent), but the per-arm dual-write still mutates
			// data.TotalDebt per spec §"B3 routing correction" lines
			// 181-189.
			out, err := la.ApplyB3Contingent(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				// Adjuster.Apply errors are not a defined surface today;
				// ApplyB3Contingent surfaces no error even when the AI
				// service fails (the AI failure is absorbed inside Apply).
				// Skip on a hypothetical future error (see B1 arm rationale).
				continue
			}

			// Record native emissions for the orchestrator. The OverlaySpec
			// (Field:"DebtLikeClaims") carries the probability-weighted
			// contingent amount for InvestedCapital().DebtLikeClaims. Even on
			// the non-fired path the Fired:false LedgerEntry is load-bearing
			// for "why didn't B3 fire?" observability.
			allFlags = append(allFlags, out.Flags...)
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
		default:
			continue // Skip unknown rules
		}
	}

	// DC-1 Phase 5 P5-C4: the legacy *AdjustmentResult accumulation +
	// audit-trail string were deleted. The orchestrator projects the public
	// audit trail from data.AdjustmentLedger via adjustmentsFromLedger; the
	// B-rule OverlaySpecs flow to InvestedCapital().DebtLikeClaims. B-rules
	// never mutate an umbrella, so there is no post-loop recompute.
	return &LiabilityAdjustmentResult{
		Flags:                  allFlags,
		NativeLedgerEntries:    nativeLedger,
		NativeOverlays:         nativeOverlays,
		NativelyEmittedRuleIDs: nativelyEmittedRuleIDs,
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
	now := time.Now()

	// Phase 3 followup (MEDIUM-1 fix): forward ctx to the legacy method so
	// the leaseCalculator.CalculatePresentValue call honors request-scoped
	// cancellation. Previously ProcessOperatingLeaseAdjustment hard-coded
	// context.Background() and ignored upstream cancellation.
	//
	// Delegate to the legacy method for the actual PV calculation
	// (including fallbackToSimpleCapitalization on calculator failure).
	// This preserves the existing flag taxonomy + reasoning strings
	// bit-for-bit, which is load-bearing for downstream consumers that
	// grep on the "operating_lease_adj:" / "lease_calculation_quality"
	// / etc. prefixes.
	legacy := la.ProcessOperatingLeaseAdjustment(ctx, working, rule, cleaningCtx)

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

// ProcessOperatingLeaseAdjustment implements B1 rule: Operating lease present value calculation
//
// Phase 3 followup (MEDIUM-1 fix): takes ctx as the first parameter and
// forwards it to leaseCalculator.CalculatePresentValue. Previously the
// helper hard-coded context.Background() with a TODO marker, so
// request-scoped cancellation never propagated to the lease PV engine.
func (la *LiabilityAdjuster) ProcessOperatingLeaseAdjustment(ctx context.Context, data *entities.FinancialData, rule *entities.CleaningRule, cleaningContext *entities.CleaningContext) *AdjustmentResult {
	if ctx == nil {
		// Defensive: legacy direct-call test paths may pass a nil ctx.
		// leaseCalculator.CalculatePresentValue may call ctx.Err() so we
		// promote nil to a Background context rather than crash.
		ctx = context.Background()
	}

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
// AI invariant (single call): when the AI gate is open (aiEnabled &&
// aiService != nil && (FootnoteText != "" || totalContingent > 0)) Apply
// invokes analyzeContingentLiabilityWithAI EXACTLY ONCE, capturing both
// the probability-weighted amount AND the AIProvenance from the same
// response. The pre-computed result is injected into the legacy
// processContingentLiabilityAdjustment via preComputedAIResult so the
// legacy path does NOT re-invoke AnalyzeFootnote. This closes the
// pre-followup divergence where two separate AI calls could record
// amount and provenance from different (non-deterministic) responses.
//
// ctx propagation: the unified helper takes ctx as first parameter,
// threading it to ai.AnalyzeFootnote so upstream cancellation propagates
// to the AMOUNT-producing call (not only the provenance side as before
// the followup).
//
// AIProvenance contract: recorded ONLY on AI success. The recorded
// amount and recorded provenance describe the SAME response. Rule-based,
// AI-disabled, and AI-failed paths leave OverlaySpec.AIProvenance = nil
// (only AI-derived overlays carry provenance).
//
// PromptHash + SourceDocHash are SHA-256 hex digests computed PRE-API-CALL
// in the helper via internal/services/datacleaner/adjustments/hash.go, so
// a network failure leaves no partial hash state. Both hashes are
// deterministic functions of the request inputs, not of the LLM response
// — a future model upgrade leaves hashes unchanged.
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md §"B3 routing correction"
// Phase 3 §5.2: docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-3-spec.md (PromptHash semantics: canonical-request fingerprint)
// Followup spec: docs/refactoring/archive/dc1-phase-3-followup-spec.md §"HIGH-2 + HIGH-3"
func (la *LiabilityAdjuster) ApplyB3Contingent(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	now := time.Now()

	// Phase 3 followup (HIGH-2 + HIGH-3 fix): single AI call invariant.
	// When the AI gate is open, invoke analyzeContingentLiabilityWithAI
	// EXACTLY ONCE here and inject the pre-computed result into the
	// legacy probability-weighting math via processContingentLiabilityAdjustment.
	// The previous implementation invoked AnalyzeFootnote twice (once
	// inside the legacy method for the amount, once here for provenance)
	// which could record divergent probabilities under non-deterministic
	// LLM responses.
	//
	// The single-call invariant also threads ctx through the AI path,
	// closing HIGH-3 — the previous amount-side call used
	// context.Background() and silently ignored upstream cancellation.
	var aiProvenance *entities.AIProvenance
	var aiResult *preComputedAIResult
	totalContingent := working.ContingentLiabilities + working.EnvironmentalLiabilities + working.LitigationLiabilities
	if la.aiEnabled && la.aiService != nil && (cleaningCtx.FootnoteText != "" || totalContingent > 0) {
		prob, provenance, metadata, aiErr := la.analyzeContingentLiabilityWithAI(ctx, working, cleaningCtx, now)
		aiResult = &preComputedAIResult{
			probability: prob,
			metadata:    metadata,
			err:         aiErr,
		}
		// AIProvenance is recorded ONLY on a successful AI response — the
		// recorded amount and the recorded provenance must describe the
		// same response. On AI failure the legacy fallback computes an
		// amount with no AI input; provenance stays nil.
		if aiErr == nil {
			aiProvenance = provenance
		}
	}

	// Delegate to the shared probability-weighting math with the pre-
	// computed AI result injected. This preserves the existing flag
	// taxonomy + reasoning strings bit-for-bit, which is load-bearing
	// for downstream consumers that grep on the "contingent_liabilities:"
	// / "contingent_liability_exposure" prefixes.
	legacy := la.processContingentLiabilityAdjustment(ctx, working, rule, cleaningCtx, aiResult)

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

// preComputedAIResult carries the output of a single
// analyzeContingentLiabilityWithAI call so callers that already invoked
// the AI service can inject the result into ProcessContingentLiabilityAdjustment
// without triggering a second AI call. Phase 3 followup (HIGH-2 fix):
// ApplyB3Contingent uses this to guarantee exactly-one AnalyzeFootnote
// invocation per B3 fire — the recorded amount and the recorded
// AIProvenance now describe the SAME response.
//
// A nil *preComputedAIResult means "no pre-computed result; the legacy
// path may invoke the AI itself". A non-nil pointer means the caller
// already invoked the AI; the err field indicates whether the call
// succeeded (when err != nil the rule-based fallback is engaged with
// reasoning that names the failure mode).
type preComputedAIResult struct {
	probability float64
	metadata    map[string]string
	err         error
}

// ProcessContingentLiabilityAdjustment implements B3 rule: Contingent liability estimation
//
// DC-1 Phase 2 PR-4 Task 4.3: the parameter previously named `context` was
// renamed to `cleaningCtx` to unshadow the `context` package identifier.
//
// Phase 3 followup (HIGH-3 fix): takes ctx as the first parameter and
// forwards it to analyzeContingentLiabilityWithAI so request-scoped
// cancellation propagates to the AI service. The legacy direct-call test
// path may still pass context.Background(); the dispatcher passes the
// real request ctx.
//
// Unlike the deleted legacy singular A/C Process*Adjustment helpers (removed
// in P5-C4 closeout), this remains a SUPPORTED direct-call entry point: it is
// exercised by the integration smoke test and the B3 AI-provenance direct-call
// coverage, and was deliberately ctx-threaded in the Phase 3 followup.
func (la *LiabilityAdjuster) ProcessContingentLiabilityAdjustment(ctx context.Context, data *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) *AdjustmentResult {
	return la.processContingentLiabilityAdjustment(ctx, data, rule, cleaningCtx, nil)
}

// processContingentLiabilityAdjustment is the shared implementation for
// the legacy public method and the Adjuster path. When `aiResult` is
// non-nil, the caller has already invoked the AI service and the pre-
// computed result is consumed without a second invocation; when nil and
// the legacy AI gate is open, the helper runs the AI call itself.
func (la *LiabilityAdjuster) processContingentLiabilityAdjustment(
	ctx context.Context,
	data *entities.FinancialData,
	rule *entities.CleaningRule,
	cleaningCtx *entities.CleaningContext,
	aiResult *preComputedAIResult,
) *AdjustmentResult {
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

	var probabilityWeight float64
	var reasoningPrefix string

	aiGateOpen := la.aiEnabled && la.aiService != nil && (cleaningCtx.FootnoteText != "" || totalContingentLiability > 0)

	switch {
	case aiResult != nil && aiResult.err != nil:
		// Caller already attempted the AI call and it failed. Fall back to the
		// deterministic industry heuristic (TDB-3) — the SAME fallback the
		// AI-disabled `default` arm uses — so the two fallback modes are
		// consistent and sector-calibrated rather than a flat 40%. The
		// heuristic is network-free, so the single-AI-call invariant holds and
		// AIProvenance stays nil (no AI input produced this amount).
		probabilityWeight = la.getContingentLiabilityProbability(cleaningCtx.IndustryCode, totalContingentLiability)
		reasoningPrefix = fmt.Sprintf("AI analysis failed (%v), using industry heuristic fallback", aiResult.err)
	case aiResult != nil:
		probabilityWeight = aiResult.probability
		reasoningPrefix = "AI analysis of footnotes"
		if cleaningCtx.AIMetadata == nil {
			cleaningCtx.AIMetadata = make(map[string]string)
		}
		for k, v := range aiResult.metadata {
			cleaningCtx.AIMetadata[k] = v
		}
	case aiGateOpen:
		// Legacy direct-call path: invoke the AI once and use its
		// probability + metadata. AIProvenance is intentionally discarded
		// here because *AdjustmentResult has no provenance field; Phase 4
		// flips consumers to read the overlay provenance directly through
		// the Adjuster (ApplyB3Contingent) path which captures the
		// provenance via the pre-computed-aiResult branch above.
		aiProbability, _, aiMetadata, err := la.analyzeContingentLiabilityWithAI(ctx, data, cleaningCtx, time.Now())
		if err != nil {
			// Legacy direct-call AI failure → same deterministic industry-heuristic
			// fallback as arm 1 and the AI-disabled default arm (TDB-3).
			probabilityWeight = la.getContingentLiabilityProbability(cleaningCtx.IndustryCode, totalContingentLiability)
			reasoningPrefix = fmt.Sprintf("AI analysis failed (%v), using industry heuristic fallback", err)
		} else {
			probabilityWeight = aiProbability
			reasoningPrefix = "AI analysis of footnotes"
			if cleaningCtx.AIMetadata == nil {
				cleaningCtx.AIMetadata = make(map[string]string)
			}
			for k, v := range aiMetadata {
				cleaningCtx.AIMetadata[k] = v
			}
		}
	default:
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
	// Deterministic industry-heuristic probability. This is the documented
	// FALLBACK used when the AI footnote analyzer is disabled OR enabled-but-
	// failed (TDB-3); the primary estimator is analyzeContingentLiabilityWithAI
	// when AI is enabled and succeeds. Sourced from the industry classifier's
	// per-sector ContingentLiabilityRate when available, else the GICS switch.

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

// analyzeContingentLiabilityWithAI runs the B3 contingent-liability footnote
// analysis through la.aiService.AnalyzeFootnote ONCE and returns both the
// probability (for amount computation) and the AIProvenance record (for
// OverlaySpec.AIProvenance stamping) derived from the SAME response.
//
// Phase 3 followup (HIGH-2 + HIGH-3 fix): unifies the previous two helpers
// (analyzeContingentLiabilityWithAI for amount + captureB3AIProvenance for
// provenance) which called AnalyzeFootnote independently and could record
// divergent probabilities under non-deterministic LLM responses. The fix
// preserves audit integrity — the recorded overlay.Amount is derived from
// the SAME probability that AIProvenance.Probability records.
//
// HIGH-3 fix: ctx is the first parameter and is forwarded to
// aiService.AnalyzeFootnote, so request-scoped cancellation propagates
// through the AI call. The previous amount-path helper used
// context.Background() and ignored upstream cancellation entirely.
//
// Q4 contract (preserved): PromptHash + SourceDocHash are SHA-256 hex
// digests computed PRE-API-CALL so a network failure leaves no partial
// or inconsistent hash. The hashes are deterministic functions of the
// request inputs (timestamp-stripped canonical serialization, sorted
// Context map keys); they are independent of the model response.
//
// Returns (0, nil, nil, err) on AI service errors so the caller can
// silently fall through to the conservative rule-based fallback path.
func (la *LiabilityAdjuster) analyzeContingentLiabilityWithAI(
	ctx context.Context,
	data *entities.FinancialData,
	cleaningCtx *entities.CleaningContext,
	timestamp time.Time,
) (probability float64, provenance *entities.AIProvenance, metadata map[string]string, err error) {
	if ctx == nil {
		// Defensive: test callers may invoke the unexported helper directly
		// without a ctx. MockAIService.AnalyzeFootnote calls ctx.Err() so we
		// promote nil to a usable Background ctx rather than crashing.
		ctx = context.Background()
	}

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

	// Pre-API-call hashes — deterministic functions of the inputs;
	// independent of the model response. If the API call fails below,
	// the caller discards both hashes anyway (returns nil, nil, nil, err).
	promptHash := sha256HexPromptCanonical(request)
	sourceDocHash := sha256Hex(footnoteText)

	response, callErr := la.aiService.AnalyzeFootnote(ctx, request)
	if callErr != nil {
		return 0.0, nil, nil, fmt.Errorf("AI service call failed: %w", callErr)
	}
	if response == nil {
		return 0.0, nil, nil, fmt.Errorf("AI service returned nil response")
	}
	if response.Error != "" {
		return 0.0, nil, nil, fmt.Errorf("AI service returned error: %s", response.Error)
	}

	extractedData, ok := response.ExtractedData["contingent_liability_estimate"]
	if !ok {
		return 0.0, nil, nil, fmt.Errorf("AI response missing contingent liability estimate")
	}

	prob, extractedSpan, parseErr := parseContingentLiabilityEstimate(extractedData)
	if parseErr != nil {
		return 0.0, nil, nil, parseErr
	}
	if prob < 0.0 || prob > 1.0 {
		return 0.0, nil, nil, fmt.Errorf("AI returned invalid probability: %.2f%%", prob*100)
	}

	provenance = &entities.AIProvenance{
		ModelName:     b3AIModelName,
		PromptHash:    promptHash,
		SourceDocHash: sourceDocHash,
		ExtractedSpan: extractedSpan,
		Probability:   prob,
		Confidence:    response.Confidence,
		Timestamp:     timestamp,
	}

	metadata = map[string]string{
		"ai_confidence":      fmt.Sprintf("%.2f", response.Confidence),
		"ai_model_used":      b3AIModelName,
		"ai_processing_time": response.ProcessingTime.String(),
		"ai_probability":     fmt.Sprintf("%.2f%%", prob*100),
		"analysis_type":      string(response.AnalysisType),
		"request_id":         response.RequestID,
	}

	return prob, provenance, metadata, nil
}

// parseContingentLiabilityEstimate extracts the probability and supporting-
// evidence span from a FootnoteAnalysisResponse's extracted-data slot.
// Handles both the direct-struct form (MockAIService) and the map form
// (HTTP-backed AI service).
func parseContingentLiabilityEstimate(extractedData interface{}) (prob float64, extractedSpan string, err error) {
	switch est := extractedData.(type) {
	case ai.ContingentLiabilityEstimate:
		prob = est.ProbabilityPercent / 100.0
		if len(est.SupportingEvidence) > 0 {
			extractedSpan = est.SupportingEvidence[0]
		}
		return prob, extractedSpan, nil
	case map[string]interface{}:
		raw, ok := est["probability_percent"].(float64)
		if !ok {
			return 0.0, "", fmt.Errorf("AI response missing probability percentage")
		}
		prob = raw / 100.0
		if spans, ok := est["supporting_evidence"].([]interface{}); ok && len(spans) > 0 {
			if s, ok := spans[0].(string); ok {
				extractedSpan = s
			}
		}
		return prob, extractedSpan, nil
	default:
		return 0.0, "", fmt.Errorf("AI response has invalid format: expected ContingentLiabilityEstimate or map[string]interface{}, got %T", extractedData)
	}
}
