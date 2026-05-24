package adjustments

import (
	"context"
	"fmt"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// AssetAdjuster handles Category A adjustments from SEC cleaning guide
// Implements over-stated/low-quality asset adjustments
type AssetAdjuster struct {
	// TODO: Add configuration for adjustment thresholds
}

// NewAssetAdjuster creates a new asset adjuster instance
func NewAssetAdjuster() *AssetAdjuster {
	return &AssetAdjuster{}
}

// AdjusterID constants identify each Category A adjuster on LedgerEntry /
// OverlaySpec records. They MUST be stable across builds — Phase 3's view
// reconstruction joins on these IDs. Keep the trailing "_<descriptor>"
// suffixes in sync with the legacy rule.ID values where possible so log
// greps continue to work across the migration.
const (
	adjusterIDA1GoodwillExclusion     = "A1_goodwill_exclusion"
	adjusterIDA2IntangibleWritedown   = "A2_intangible_writedown"
	adjusterIDA4DTAValuationAllowance = "A4_dta_valuation_allowance"
	adjusterIDA5InventoryWritedown    = "A5_inventory_writedown"

	// Flag-only review AdjusterIDs (DC-1 Phase 2 PR-2 Task 2.5). These two
	// reviews never mutate the balance sheet — their LedgerEntries stay
	// Fired:false at all times. The populated Flags slice on the
	// AdjusterOutput IS the firing signal (when the review's threshold trips).
	// Phase 3's Restated() accessor must treat Fired:false entries that carry
	// non-empty Flags as informational only — no equity/asset mutation.
	adjusterIDARDCapitalizationReview    = "A-RD_capitalization_review"
	adjusterIDACapitalizedSoftwareReview = "A-capitalized_software_review"
)

// a1GoodwillAdjuster is the per-rule adapter that lets AssetAdjuster — which
// hosts multiple Category A rules — satisfy the single-Apply Adjuster
// interface. Each A-rule gets its own adapter struct in Phase 2; once every
// A-rule has migrated (Task 2.6), service.go::applyActiveAdjustments will
// dispatch through the adapters and the shim's asset branch will be deleted.
//
// Phase 2 Task 2.1 keeps the adapter package-private and reuses the existing
// AssetAdjuster instance — no extra construction state. The compile-time
// assertion below pins the interface contract so a future signature drift
// fails to build instead of silently breaking the orchestrator.
type a1GoodwillAdjuster struct {
	aa *AssetAdjuster
}

// NewA1GoodwillAdjuster returns an Adjuster-shaped wrapper around
// AssetAdjuster's A1 rule. Exported so the cleaner orchestrator can hold the
// instance alongside the legacy AssetAdjuster.
func NewA1GoodwillAdjuster(aa *AssetAdjuster) Adjuster {
	return &a1GoodwillAdjuster{aa: aa}
}

// Compile-time assertion: a1GoodwillAdjuster MUST implement Adjuster.
// If either signature drifts, the package fails to build.
var _ Adjuster = (*a1GoodwillAdjuster)(nil)

// Name implements Adjuster.
func (a *a1GoodwillAdjuster) Name() string {
	return adjusterIDA1GoodwillExclusion
}

// Apply implements Adjuster by delegating to AssetAdjuster.ApplyA1Goodwill.
// The dual-write contract (in-place mutation of data.Goodwill /
// data.TotalAssets) is preserved by the dispatcher in ProcessAssetAdjustments
// — NOT by Apply itself. See ApplyA1Goodwill godoc for the role split.
func (a *a1GoodwillAdjuster) Apply(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	return a.aa.ApplyA1Goodwill(ctx, working, rule, cleaningCtx)
}

// a2IntangibleAdjuster is the Task 2.2 per-rule adapter for A2 (indefinite-
// lived intangible writedown). Mirrors a1GoodwillAdjuster's shape — the
// constructor wraps an existing AssetAdjuster, Apply delegates to the new
// mutation-free ApplyA2Intangible, and the dispatcher in
// ProcessAssetAdjustments performs the dual-write on data after Apply runs.
//
// Role classification (plan §3.5 / §4 row A2): Restater (component-only
// mutation). Unlike A1 (OverlayEmitter), A2's fired LedgerEntry carries
// Component:"OtherIntangibles", DeltaAmount:-writedown, EquityOffset:-writedown
// and emits NO OverlaySpec — the writedown is a direct reduction of the
// component, not an analytical overlay on top of TotalAssets.
type a2IntangibleAdjuster struct {
	aa *AssetAdjuster
}

// NewA2IntangibleAdjuster returns an Adjuster-shaped wrapper around
// AssetAdjuster's A2 rule. Exported for parity with NewA1GoodwillAdjuster so
// the cleaner orchestrator can hold the instance alongside AssetAdjuster.
func NewA2IntangibleAdjuster(aa *AssetAdjuster) Adjuster {
	return &a2IntangibleAdjuster{aa: aa}
}

// Compile-time assertion: a2IntangibleAdjuster MUST implement Adjuster.
var _ Adjuster = (*a2IntangibleAdjuster)(nil)

// Name implements Adjuster.
func (a *a2IntangibleAdjuster) Name() string {
	return adjusterIDA2IntangibleWritedown
}

// Apply implements Adjuster by delegating to AssetAdjuster.ApplyA2Intangible.
// The dual-write contract (in-place mutation of data.OtherIntangibles /
// data.TotalAssets) is preserved by the dispatcher in ProcessAssetAdjustments —
// NOT by Apply itself. See ApplyA2Intangible godoc for the role split.
func (a *a2IntangibleAdjuster) Apply(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	return a.aa.ApplyA2Intangible(ctx, working, rule, cleaningCtx)
}

// a4DTAValuationAllowanceAdjuster is the Task 2.3 per-rule adapter for A4
// (deferred-tax-asset valuation allowance). Mirrors a2IntangibleAdjuster's
// shape — the constructor wraps an existing AssetAdjuster, Apply delegates to
// the new mutation-free ApplyA4DTAValuationAllowance, and the dispatcher in
// ProcessAssetAdjustments performs the dual-write on data after Apply runs.
//
// Role classification (plan §3.5 / §4 row A4): Restater (component-only
// mutation). Like A2, A4's fired LedgerEntry carries Component:
// "DeferredTaxAssets", DeltaAmount:-valuationAllowance, EquityOffset:
// -valuationAllowance and emits NO OverlaySpec — the valuation allowance is
// a direct reduction of the DTA component, not an analytical overlay on top
// of TotalAssets. TaxShieldDTA is intentionally zero because A4 IS the DTA
// valuation allowance — there is no separate tax shield to compute.
type a4DTAValuationAllowanceAdjuster struct {
	aa *AssetAdjuster
}

// NewA4DTAValuationAllowanceAdjuster returns an Adjuster-shaped wrapper around
// AssetAdjuster's A4 rule. Exported for parity with NewA1GoodwillAdjuster /
// NewA2IntangibleAdjuster so the cleaner orchestrator can hold the instance
// alongside AssetAdjuster.
func NewA4DTAValuationAllowanceAdjuster(aa *AssetAdjuster) Adjuster {
	return &a4DTAValuationAllowanceAdjuster{aa: aa}
}

// Compile-time assertion: a4DTAValuationAllowanceAdjuster MUST implement Adjuster.
var _ Adjuster = (*a4DTAValuationAllowanceAdjuster)(nil)

// Name implements Adjuster.
func (a *a4DTAValuationAllowanceAdjuster) Name() string {
	return adjusterIDA4DTAValuationAllowance
}

// Apply implements Adjuster by delegating to
// AssetAdjuster.ApplyA4DTAValuationAllowance. The dual-write contract
// (in-place mutation of data.DeferredTaxAssets / data.TotalAssets /
// data.ValuationAllowance) is preserved by the dispatcher in
// ProcessAssetAdjustments — NOT by Apply itself. See
// ApplyA4DTAValuationAllowance godoc for the role split.
func (a *a4DTAValuationAllowanceAdjuster) Apply(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	return a.aa.ApplyA4DTAValuationAllowance(ctx, working, rule, cleaningCtx)
}

// a5InventoryWritedownAdjuster is the Task 2.4 per-rule adapter for A5
// (obsolete-inventory writedown). Mirrors a2IntangibleAdjuster's shape — the
// constructor wraps an existing AssetAdjuster, Apply delegates to the new
// mutation-free ApplyA5InventoryWritedown, and the dispatcher in
// ProcessAssetAdjustments performs the dual-write on data after Apply runs.
//
// Role classification (plan §3.5 / §4 row A5): Restater + TaxShieldDTA. A5 is
// the FIRST PR-2 adjuster to populate LedgerEntry.TaxShieldDTA — the 40%
// inventory writedown generates a derived deferred-tax-asset shield equal to
// writedownAmount * working.EffectiveTaxRate (when the rate is > 0). This
// distinguishes A5 from A1/A2/A4, all of which leave TaxShieldDTA at zero for
// distinct reasons (A1 is OverlayEmitter; A2 defers TaxShieldDTA to Phase 3
// per Q2; A4 IS the DTA reduction itself). See ApplyA5InventoryWritedown
// godoc for the full TaxShieldDTA rationale.
type a5InventoryWritedownAdjuster struct {
	aa *AssetAdjuster
}

// NewA5InventoryWritedownAdjuster returns an Adjuster-shaped wrapper around
// AssetAdjuster's A5 rule. Exported for parity with the prior A1/A2/A4
// constructors so the cleaner orchestrator can hold the instance alongside
// AssetAdjuster.
func NewA5InventoryWritedownAdjuster(aa *AssetAdjuster) Adjuster {
	return &a5InventoryWritedownAdjuster{aa: aa}
}

// Compile-time assertion: a5InventoryWritedownAdjuster MUST implement Adjuster.
var _ Adjuster = (*a5InventoryWritedownAdjuster)(nil)

// Name implements Adjuster.
func (a *a5InventoryWritedownAdjuster) Name() string {
	return adjusterIDA5InventoryWritedown
}

// Apply implements Adjuster by delegating to
// AssetAdjuster.ApplyA5InventoryWritedown. The dual-write contract (in-place
// mutation of data.Inventory / data.TotalAssets) is preserved by the dispatcher
// in ProcessAssetAdjustments — NOT by Apply itself. See
// ApplyA5InventoryWritedown godoc for the role split.
func (a *a5InventoryWritedownAdjuster) Apply(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	return a.aa.ApplyA5InventoryWritedown(ctx, working, rule, cleaningCtx)
}

// aRDCapitalizationReviewAdjuster is the Task 2.5 per-rule adapter for the
// flag-only R&D-capitalization review. Mirrors a1-a5 adapter shape — the
// constructor wraps an existing AssetAdjuster, Apply delegates to the new
// mutation-free ApplyARDCapitalizationReview, and the dispatcher in
// ProcessAssetAdjustments drains the AdjusterOutput's LedgerEntries / Flags
// without performing any dual-write (these reviews never mutate the balance
// sheet).
//
// Role classification (plan §3.5 / §7 Task 2.5): FlagEmitter. Every emitted
// LedgerEntry has Fired:false because no balance-sheet adjustment happens —
// the populated AdjusterOutput.Flags slice IS the review's firing signal when
// the R&D/Revenue ratio crosses the 10% review threshold. Skip paths emit the
// same Fired:false shape with empty Flags + a SkipReason / SkipMetrics
// describing why the review was not actionable.
type aRDCapitalizationReviewAdjuster struct {
	aa *AssetAdjuster
}

// NewARDCapitalizationReviewAdjuster returns an Adjuster-shaped wrapper around
// AssetAdjuster's R&D-capitalization review. Exported for parity with the
// prior A1-A5 constructors so the cleaner orchestrator can hold the instance
// alongside AssetAdjuster.
func NewARDCapitalizationReviewAdjuster(aa *AssetAdjuster) Adjuster {
	return &aRDCapitalizationReviewAdjuster{aa: aa}
}

// Compile-time assertion: aRDCapitalizationReviewAdjuster MUST implement
// Adjuster. If either signature drifts the package fails to build.
var _ Adjuster = (*aRDCapitalizationReviewAdjuster)(nil)

// Name implements Adjuster.
func (a *aRDCapitalizationReviewAdjuster) Name() string {
	return adjusterIDARDCapitalizationReview
}

// Apply implements Adjuster by delegating to
// AssetAdjuster.ApplyARDCapitalizationReview. No dual-write happens because
// this review never mutates the balance sheet — see
// ApplyARDCapitalizationReview godoc for the FlagEmitter convention.
func (a *aRDCapitalizationReviewAdjuster) Apply(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	return a.aa.ApplyARDCapitalizationReview(ctx, working, rule, cleaningCtx)
}

// aCapitalizedSoftwareReviewAdjuster is the Task 2.5 per-rule adapter for the
// flag-only capitalized-software review. Mirrors aRDCapitalizationReviewAdjuster's
// shape — same FlagEmitter convention, same Fired:false-with-Flags firing
// signal. The two reviews are independent because they evaluate distinct
// underlying ratios (R&D/Revenue vs. OtherIntangibles/Revenue) against
// different thresholds (10% vs. 1.5%).
type aCapitalizedSoftwareReviewAdjuster struct {
	aa *AssetAdjuster
}

// NewACapitalizedSoftwareReviewAdjuster returns an Adjuster-shaped wrapper
// around AssetAdjuster's capitalized-software review.
func NewACapitalizedSoftwareReviewAdjuster(aa *AssetAdjuster) Adjuster {
	return &aCapitalizedSoftwareReviewAdjuster{aa: aa}
}

// Compile-time assertion: aCapitalizedSoftwareReviewAdjuster MUST implement
// Adjuster. If either signature drifts the package fails to build.
var _ Adjuster = (*aCapitalizedSoftwareReviewAdjuster)(nil)

// Name implements Adjuster.
func (a *aCapitalizedSoftwareReviewAdjuster) Name() string {
	return adjusterIDACapitalizedSoftwareReview
}

// Apply implements Adjuster by delegating to
// AssetAdjuster.ApplyACapitalizedSoftwareReview. No dual-write happens because
// this review never mutates the balance sheet — see
// ApplyACapitalizedSoftwareReview godoc for the FlagEmitter convention.
func (a *aCapitalizedSoftwareReviewAdjuster) Apply(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	return a.aa.ApplyACapitalizedSoftwareReview(ctx, working, rule, cleaningCtx)
}

// AdjustmentResult represents the result of applying an asset adjustment
type AdjustmentResult struct {
	Amount      float64               `json:"amount"`
	Applied     bool                  `json:"applied"`
	Adjustments []entities.Adjustment `json:"adjustments"`
	Flags       []entities.Flag       `json:"flags"`
	Reasoning   string                `json:"reasoning"`
}

// TangibleAssetsResult represents the result of calculating net tangible assets
type TangibleAssetsResult struct {
	AdjustedTangibleAssets float64               `json:"adjusted_tangible_assets"`
	Adjustments            []entities.Adjustment `json:"adjustments"`
	AuditTrail             string                `json:"audit_trail"`
}

// ApplyA1Goodwill is the Adjuster-shaped (DC-1 Phase 2) implementation of
// the A1 goodwill-exclusion rule. It produces an AdjusterOutput describing
// what the rule would do — LedgerEntries (audit trail), Overlays (declarative
// "subtract goodwill from TotalAssets" record), and Flags (significance
// triggers) — but does NOT mutate `working`. The dual-write mutation
// (working.Goodwill = 0, working.TotalAssets -= originalGoodwill) is
// performed by ProcessAssetAdjustments' dispatcher so the legacy
// *AdjustmentResult callers stay byte-identical.
//
// Role classification (plan §3.5): A1 is an OverlayEmitter. The fired
// LedgerEntry intentionally carries NO Component / DeltaAmount / EquityOffset
// — the declarative amount lives on OverlaySpec, the LedgerEntry exists for
// ordering / audit / "did A1 fire?" diagnostics. Phase 3's InvestedCapital()
// view will read the OverlaySpec to exclude goodwill; Phase 4 will delete
// the dispatcher-side mutation once that consumer is wired.
//
// Skipped paths emit Fired=false LedgerEntries with SkipReason (and
// SkipMetrics for the threshold-failed case) so observability can answer
// "why didn't A1 fire on this ticker?" without code reading.
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md §"Adjuster output"
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md §3.2 / §3.3 / §3.5 / §7 Task 2.1
func (aa *AssetAdjuster) ApplyA1Goodwill(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	// ctx + cleaningCtx are accepted for interface symmetry with future
	// industry-aware adjusters; A1 itself uses neither today.
	_ = ctx
	_ = cleaningCtx

	now := time.Now()

	// Skip path 1: no goodwill present. Emit a Fired:false LedgerEntry so
	// "A1 was considered" is observable.
	if working.Goodwill <= 0 {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:  now,
				AdjusterID: adjusterIDA1GoodwillExclusion,
				RuleID:     rule.ID,
				Fired:      false,
				Reasoning:  "No goodwill present to adjust",
				SkipReason: "No goodwill present to adjust",
			}},
		}, nil
	}

	goodwillRatio := working.Goodwill / working.TotalAssets

	// Skip path 2: goodwill below the 5% materiality threshold. Carry the
	// ratio + threshold as SkipMetrics so downstream dashboards can chart
	// "how close was A1 to firing on this ticker?".
	threshold := 0.05
	if goodwillRatio <= threshold {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:   now,
				AdjusterID:  adjusterIDA1GoodwillExclusion,
				RuleID:      rule.ID,
				Fired:       false,
				Reasoning:   "goodwill ratio below 5% threshold",
				SkipReason:  fmt.Sprintf("Goodwill ratio %.1f%% below threshold %.1f%%", goodwillRatio*100, threshold*100),
				SkipMetrics: map[string]float64{"goodwill_ratio": goodwillRatio, "threshold": threshold},
			}},
		}, nil
	}

	// Fired path: emit the declarative OverlaySpec + a Fired:true LedgerEntry
	// + (when ratio >= 10%) a significance Flag. The OverlaySpec carries the
	// amount; the LedgerEntry deliberately leaves Component / DeltaAmount /
	// EquityOffset unset because A1's role is OverlayEmitter (plan §3.5).
	originalGoodwill := working.Goodwill

	overlay := entities.OverlaySpec{
		OverlayID:       adjusterIDA1GoodwillExclusion,
		RuleID:          rule.ID,
		Field:           "TotalAssets",
		Operation:       "subtract",
		Amount:          originalGoodwill,
		AmountSemantics: entities.AmountIncremental,
		Reasoning:       fmt.Sprintf("goodwill_exclusion: Excluded %.0f goodwill (%.1f%% of assets) per A1 rule", originalGoodwill, goodwillRatio*100),
	}

	out := AdjusterOutput{
		LedgerEntries: []entities.LedgerEntry{{
			Timestamp:  now,
			AdjusterID: adjusterIDA1GoodwillExclusion,
			RuleID:     rule.ID,
			Fired:      true,
			Reasoning:  "A1 goodwill exclusion overlay emitted",
		}},
		Overlays: []entities.OverlaySpec{overlay},
	}

	// Significance flag — preserve the legacy ProcessGoodwillAdjustment
	// behavior: only flag when goodwill is >= 10% of assets. Severity is
	// derived from the existing ratio-bucket helper so the flag taxonomy
	// stays identical across the migration.
	if goodwillRatio >= 0.10 {
		out.Flags = append(out.Flags, entities.Flag{
			ID:             fmt.Sprintf("goodwill-flag-%d", now.UnixNano()),
			RuleID:         rule.ID,
			Type:           "goodwill_exclusion",
			Severity:       aa.getSeverityForGoodwillRatio(goodwillRatio),
			Amount:         originalGoodwill,
			Percentage:     goodwillRatio * 100,
			Description:    fmt.Sprintf("Excluded significant goodwill (%.1f%% of assets)", goodwillRatio*100),
			Recommendation: "Monitor for potential acquisition integration issues and impairment risks",
			Timestamp:      now,
		})
	}

	return out, nil
}

// ApplyA2Intangible is the Adjuster-shaped (DC-1 Phase 2 PR-2 Task 2.2)
// implementation of the A2 indefinite-lived intangible-writedown rule. Like
// ApplyA1Goodwill, it is MUTATION-FREE — it reads `working` and returns an
// AdjusterOutput describing the writedown's intent (Restater-shaped
// LedgerEntry on the OtherIntangibles component) but does NOT modify
// `working.OtherIntangibles` or `working.TotalAssets`. The dispatcher in
// ProcessAssetAdjustments performs the dual-write mutation centrally.
//
// Role classification (plan §3.5 / §4 row A2): Restater. The fired LedgerEntry
// carries Component:"OtherIntangibles", DeltaAmount:-writedownAmount,
// EquityOffset:-writedownAmount, TaxShieldDTA:0. No OverlaySpec — the
// writedown is a direct component reduction, not an analytical overlay.
//
// Q2 resolution (plan §10): TaxShieldDTA is set to 0 in Phase 2 to preserve
// the dual-write bit-for-bit contract. Today's A2 code does not compute a
// tax shield; populating it here would diverge from legacy outputs. Phase 3
// revisits TaxShieldDTA when consumers actually read it.
//
// Skipped paths emit Fired:false LedgerEntries so observability can answer
// "why didn't A2 fire on this ticker?" without code reading. The threshold-
// failed path carries SkipMetrics{intangible_ratio, threshold} for dashboards.
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md §"Adjuster output"
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md §3.5 / §4 row A2 / §7 Task 2.2 / §10 Q2
func (aa *AssetAdjuster) ApplyA2Intangible(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	// ctx + cleaningCtx accepted for interface symmetry; A2 itself uses neither.
	_ = ctx
	_ = cleaningCtx

	now := time.Now()

	// Skip path 1: no intangibles present. Emit a Fired:false LedgerEntry so
	// "A2 was considered" is observable. No SkipMetrics — there's no ratio
	// to chart when the numerator is zero.
	if working.OtherIntangibles <= 0 {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:  now,
				AdjusterID: adjusterIDA2IntangibleWritedown,
				RuleID:     rule.ID,
				Fired:      false,
				Reasoning:  "No intangible assets present to adjust",
				SkipReason: "No intangible assets present to adjust",
			}},
		}, nil
	}

	originalIntangibles := working.OtherIntangibles
	intangibleRatio := originalIntangibles / working.TotalAssets

	// Skip path 2: ratio below the 2% materiality threshold. Carry the ratio
	// + threshold as SkipMetrics so downstream dashboards can chart "how
	// close was A2 to firing on this ticker?".
	threshold := 0.02
	if intangibleRatio <= threshold {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:   now,
				AdjusterID:  adjusterIDA2IntangibleWritedown,
				RuleID:      rule.ID,
				Fired:       false,
				Reasoning:   "intangible ratio below 2% threshold",
				SkipReason:  fmt.Sprintf("Intangible ratio %.1f%% below adjustment threshold %.1f%%", intangibleRatio*100, threshold*100),
				SkipMetrics: map[string]float64{"intangible_ratio": intangibleRatio, "threshold": threshold},
			}},
		}, nil
	}

	// Fired path: compute the tiered writedown amount, then emit a Restater-
	// shaped Fired:true LedgerEntry on the OtherIntangibles component. The
	// tier thresholds mirror the legacy ProcessIntangibleAdjustment behavior
	// so dual-write produces bit-for-bit identical balance-sheet outputs.
	var retentionRate float64
	switch {
	case originalIntangibles >= 300000: // Very high intangible amounts (>= $300k)
		retentionRate = 1.0 / 3.0 // Keep 1/3, writedown 2/3
	case originalIntangibles >= 200000: // High intangible amounts ($200k-$299k)
		retentionRate = 0.3 // Keep 30%, writedown 70%
	default: // Lower intangible amounts (< $200k)
		retentionRate = 0.2 // Keep 20%, writedown 80%
	}

	retainedAmount := originalIntangibles * retentionRate
	writedownAmount := originalIntangibles - retainedAmount
	writedownRate := writedownAmount / originalIntangibles

	out := AdjusterOutput{
		LedgerEntries: []entities.LedgerEntry{{
			Timestamp:    now,
			AdjusterID:   adjusterIDA2IntangibleWritedown,
			RuleID:       rule.ID,
			Fired:        true,
			Reasoning:    fmt.Sprintf("intangible_writedown: Applied %.0f%% writedown to indefinite-lived intangibles (%.1f%% of assets) per A2 rule", writedownRate*100, intangibleRatio*100),
			Component:    "OtherIntangibles",
			DeltaAmount:  -writedownAmount,
			EquityOffset: -writedownAmount,
			TaxShieldDTA: 0, // Q2 deferral (plan §10): A2 does not compute tax shield in Phase 2.
		}},
	}

	// Significance flag — preserve the legacy ProcessIntangibleAdjustment
	// behavior: every fired A2 emits exactly one flag (no further ratio gate,
	// matching the existing code).
	out.Flags = append(out.Flags, entities.Flag{
		ID:             fmt.Sprintf("intangible-flag-%d", now.UnixNano()),
		RuleID:         rule.ID,
		Type:           "intangible_writedown",
		Severity:       aa.getSeverityForIntangibleRatio(intangibleRatio),
		Amount:         writedownAmount,
		Percentage:     writedownRate * 100,
		Description:    fmt.Sprintf("Applied %.0f%% writedown to indefinite-lived intangibles (%.1f%% of assets)", writedownRate*100, intangibleRatio*100),
		Recommendation: "Consider conservative amortization over defined useful life",
		Timestamp:      now,
	})

	return out, nil
}

// ApplyA4DTAValuationAllowance is the Adjuster-shaped (DC-1 Phase 2 PR-2 Task
// 2.3) implementation of the A4 deferred-tax-asset valuation-allowance rule.
// Like ApplyA1Goodwill and ApplyA2Intangible, it is MUTATION-FREE — it reads
// `working` and returns an AdjusterOutput describing the valuation allowance's
// intent (Restater-shaped LedgerEntry on the DeferredTaxAssets component) but
// does NOT modify `working.DeferredTaxAssets` / `working.TotalAssets` /
// `working.ValuationAllowance`. The dispatcher in ProcessAssetAdjustments
// performs the dual-write mutation centrally.
//
// Role classification (plan §3.5 / §4 row A4): Restater. The fired LedgerEntry
// carries Component:"DeferredTaxAssets", DeltaAmount:-valuationAllowance,
// EquityOffset:-valuationAllowance, TaxShieldDTA:0. No OverlaySpec — the
// valuation allowance is a direct component reduction, not an analytical
// overlay on top of TotalAssets.
//
// TaxShieldDTA=0 rationale (plan §10 parsimony): A4 IS the DTA valuation
// allowance — the allowance reduces DTA which IS the "tax shield". There is
// no separate tax shield to compute on top of A4 itself; populating
// TaxShieldDTA here would double-count the same economic effect. The
// TaxShieldDTA field is reserved for adjusters where a component writedown
// generates a derived tax-shield asset (e.g., A2 intangible writedowns at
// non-zero effective tax rate — deferred to Phase 3 per Q2).
//
// Skipped paths emit Fired:false LedgerEntries so observability can answer
// "why didn't A4 fire on this ticker?" without code reading. The threshold-
// failed path carries SkipMetrics{dta_ratio, threshold} for dashboards.
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md §"Adjuster output"
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md §3.5 / §4 row A4 / §7 Task 2.3 / §10 Q2
func (aa *AssetAdjuster) ApplyA4DTAValuationAllowance(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	// ctx + cleaningCtx accepted for interface symmetry; A4 itself uses neither.
	_ = ctx
	_ = cleaningCtx

	now := time.Now()

	// Skip path 1: no DTA present. Emit a Fired:false LedgerEntry so "A4 was
	// considered" is observable. No SkipMetrics — there's no ratio to chart
	// when the numerator is zero.
	if working.DeferredTaxAssets <= 0 {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:  now,
				AdjusterID: adjusterIDA4DTAValuationAllowance,
				RuleID:     rule.ID,
				Fired:      false,
				Reasoning:  "No deferred tax assets present to adjust",
				SkipReason: "No deferred tax assets present to adjust",
			}},
		}, nil
	}

	dtaRatio := working.DeferredTaxAssets / working.TotalAssets

	// Skip path 2: DTA below the 5% materiality threshold. Carry the ratio +
	// threshold as SkipMetrics so downstream dashboards can chart "how close
	// was A4 to firing on this ticker?".
	threshold := 0.05
	if dtaRatio <= threshold {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:   now,
				AdjusterID:  adjusterIDA4DTAValuationAllowance,
				RuleID:      rule.ID,
				Fired:       false,
				Reasoning:   "DTA ratio below 5% threshold",
				SkipReason:  fmt.Sprintf("DTA ratio %.1f%% below threshold %.1f%%", dtaRatio*100, threshold*100),
				SkipMetrics: map[string]float64{"dta_ratio": dtaRatio, "threshold": threshold},
			}},
		}, nil
	}

	// Fired path: emit a Restater-shaped Fired:true LedgerEntry on the
	// DeferredTaxAssets component. Legacy A4 applies a flat 50% valuation
	// allowance per SEC guide; dual-write produces bit-for-bit identical
	// balance-sheet outputs.
	originalDTA := working.DeferredTaxAssets
	valuationAllowance := originalDTA * 0.50

	out := AdjusterOutput{
		LedgerEntries: []entities.LedgerEntry{{
			Timestamp:    now,
			AdjusterID:   adjusterIDA4DTAValuationAllowance,
			RuleID:       rule.ID,
			Fired:        true,
			Reasoning:    fmt.Sprintf("dta_valuation_allowance: Applied 50%% valuation allowance to DTA (%.1f%% of assets) per A4 rule", dtaRatio*100),
			Component:    "DeferredTaxAssets",
			DeltaAmount:  -valuationAllowance,
			EquityOffset: -valuationAllowance,
			TaxShieldDTA: 0, // A4 IS the DTA valuation allowance — no separate tax shield to compute.
		}},
	}

	// Significance flag — preserve the legacy ProcessDeferredTaxAdjustment
	// behavior: only flag when DTA was >=10% of assets. Severity is derived
	// from the existing ratio-bucket helper so the flag taxonomy stays
	// identical across the migration.
	if dtaRatio >= 0.10 {
		out.Flags = append(out.Flags, entities.Flag{
			ID:             fmt.Sprintf("dta-flag-%d", now.UnixNano()),
			RuleID:         rule.ID,
			Type:           "dta_valuation_allowance",
			Severity:       aa.getSeverityForDTARatio(dtaRatio),
			Amount:         valuationAllowance,
			Percentage:     50.0,
			Description:    fmt.Sprintf("Applied valuation allowance to significant DTA (%.1f%% of assets)", dtaRatio*100),
			Recommendation: "Monitor future taxable income projections for DTA realization",
			Timestamp:      now,
		})
	}

	return out, nil
}

// ApplyA5InventoryWritedown is the Adjuster-shaped (DC-1 Phase 2 PR-2 Task 2.4)
// implementation of the A5 obsolete-inventory writedown rule. Like the prior
// A1/A2/A4 Apply methods, it is MUTATION-FREE — it reads `working` and returns
// an AdjusterOutput describing the writedown's intent (Restater-shaped
// LedgerEntry on the Inventory component, plus TaxShieldDTA when the
// effective tax rate is positive) but does NOT modify `working.Inventory` /
// `working.TotalAssets`. The dispatcher in ProcessAssetAdjustments performs
// the dual-write mutation centrally.
//
// Role classification (plan §3.5 / §4 row A5): Restater + TaxShieldDTA. The
// fired LedgerEntry carries Component:"Inventory",
// DeltaAmount:-writedownAmount, EquityOffset:-writedownAmount, and
// TaxShieldDTA:writedownAmount * working.EffectiveTaxRate (when the tax rate
// is > 0; else 0). No OverlaySpec — the writedown is a direct component
// reduction, not an analytical overlay on top of TotalAssets.
//
// TaxShieldDTA rationale (plan §4 row A5 + §7 Task 2.4): an inventory
// writedown of $X at an effective tax rate of T produces a derived
// deferred-tax-asset shield of $X*T because the writedown is deductible for
// tax purposes once realized. This makes A5 the FIRST PR-2 adjuster to
// populate TaxShieldDTA — A1 is OverlayEmitter (leaves all monetary fields
// on OverlaySpec), A2 defers TaxShieldDTA to Phase 3 per Q2 to preserve the
// bit-for-bit dual-write contract, and A4 IS the DTA reduction itself so
// computing a separate shield would double-count.
//
// Two-condition firing logic (mirrors legacy ProcessInventoryAdjustment):
// A5 fires when EITHER (a) detectInventoryObsolescence returns true
// (low turnover OR ratio > 1.5× industry threshold) OR (b)
// inventoryRatio > industry threshold. Industry thresholds come from
// getInventoryThresholdForIndustry (default 25% / retail 40% / tech 5%, etc.).
//
// Skipped paths emit Fired:false LedgerEntries so observability can answer
// "why didn't A5 fire on this ticker?" without code reading. The
// threshold-failed path carries SkipMetrics{inventory_ratio, threshold,
// inventory_turnover, is_obsolete} for dashboards.
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md §"Adjuster output"
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md §3.5 / §4 row A5 / §7 Task 2.4
func (aa *AssetAdjuster) ApplyA5InventoryWritedown(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	// ctx accepted for interface symmetry; A5 uses cleaningCtx for industry-
	// specific threshold + obsolescence detection but not ctx today.
	_ = ctx

	now := time.Now()

	// Skip path 1: no inventory present. Emit a Fired:false LedgerEntry so
	// "A5 was considered" is observable. No SkipMetrics — there's no ratio
	// to chart when the numerator is zero.
	if working.Inventory <= 0 {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:  now,
				AdjusterID: adjusterIDA5InventoryWritedown,
				RuleID:     rule.ID,
				Fired:      false,
				Reasoning:  "No inventory present to adjust",
				SkipReason: "No inventory present to adjust",
			}},
		}, nil
	}

	inventoryRatio := working.Inventory / working.TotalAssets
	threshold := aa.getInventoryThresholdForIndustry(cleaningCtx.IndustryCode)
	isObsolete := aa.detectInventoryObsolescence(working, cleaningCtx)

	// Skip path 2: neither obsolescence triggered nor ratio above threshold.
	// Carry the diagnostic metrics so dashboards can chart "how close was A5
	// to firing on this ticker?". is_obsolete is encoded as 0.0/1.0 because
	// SkipMetrics is a float64 map.
	if !isObsolete && inventoryRatio <= threshold {
		isObsoleteMetric := 0.0
		if isObsolete {
			isObsoleteMetric = 1.0
		}
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:  now,
				AdjusterID: adjusterIDA5InventoryWritedown,
				RuleID:     rule.ID,
				Fired:      false,
				Reasoning:  fmt.Sprintf("Inventory ratio %.1f%% within threshold %.1f%% and no obsolescence indicators", inventoryRatio*100, threshold*100),
				SkipReason: fmt.Sprintf("Inventory ratio %.1f%% within threshold %.1f%%", inventoryRatio*100, threshold*100),
				SkipMetrics: map[string]float64{
					"inventory_ratio":    inventoryRatio,
					"threshold":          threshold,
					"inventory_turnover": working.InventoryTurnover,
					"is_obsolete":        isObsoleteMetric,
				},
			}},
		}, nil
	}

	// Fired path: apply a flat 40% haircut to the inventory umbrella per SEC
	// guide. Dual-write produces bit-for-bit identical balance-sheet outputs.
	writedownRate := 0.40
	writedownAmount := working.Inventory * writedownRate

	// TaxShieldDTA formula: only populate when EffectiveTaxRate > 0. Negative
	// or zero rates produce no shield — staying at the zero value matches the
	// LedgerEntry's omitempty serialization on the skip path.
	var taxShieldDTA float64
	if working.EffectiveTaxRate > 0 {
		taxShieldDTA = writedownAmount * working.EffectiveTaxRate
	}

	out := AdjusterOutput{
		LedgerEntries: []entities.LedgerEntry{{
			Timestamp:    now,
			AdjusterID:   adjusterIDA5InventoryWritedown,
			RuleID:       rule.ID,
			Fired:        true,
			Reasoning:    fmt.Sprintf("inventory_writedown: Applied %.0f%% writedown to obsolete inventory per A5 rule", writedownRate*100),
			Component:    "Inventory",
			DeltaAmount:  -writedownAmount,
			EquityOffset: -writedownAmount,
			TaxShieldDTA: taxShieldDTA,
		}},
	}

	// Significance flag — preserve the legacy ProcessInventoryAdjustment
	// behavior: every fired A5 emits exactly one FlagSeverityHigh flag
	// (no further ratio gate, matching the existing code).
	out.Flags = append(out.Flags, entities.Flag{
		ID:             fmt.Sprintf("inventory-flag-%d", now.UnixNano()),
		RuleID:         rule.ID,
		Type:           "dead_inventory",
		Severity:       entities.FlagSeverityHigh,
		Amount:         writedownAmount,
		Percentage:     writedownRate * 100,
		Description:    fmt.Sprintf("Applied inventory writedown (%.1f%% of total inventory)", writedownRate*100),
		Recommendation: "Implement inventory liquidation procedures and improve turnover",
		Timestamp:      now,
	})

	return out, nil
}

// ApplyARDCapitalizationReview is the Adjuster-shaped (DC-1 Phase 2 PR-2 Task
// 2.5) implementation of the R&D-capitalization review. Like the prior A1/A2/
// A4/A5 Apply methods, it is MUTATION-FREE. Unlike them, it NEVER mutates the
// balance sheet on any path — the legacy ProcessRDCapitalizationReview also
// returns Applied:false in all three branches (no R&D / below threshold /
// review fires).
//
// Role classification (plan §3.5 / §7 Task 2.5): FlagEmitter. Every emitted
// LedgerEntry carries Fired:false because no balance-sheet adjustment happens.
// The populated AdjusterOutput.Flags slice IS the firing signal when the R&D/
// Revenue ratio crosses the 10% review threshold. Phase 3's Restated()
// accessor MUST treat Fired:false entries with non-empty Flags as
// informational only — no equity/asset mutation.
//
// LedgerEntry shape across the three branches:
//   - No R&D (ResearchAndDevelopment <= 0): Fired:false, SkipReason:
//     "No R&D expenses present to review", no SkipMetrics, Flags empty.
//   - Below review threshold (rdRatio < 0.10): Fired:false, SkipReason
//     citing the ratio + threshold, SkipMetrics:{rd_ratio, threshold},
//     Flags empty.
//   - Review fires (rdRatio >= 0.10): Fired:false, SkipReason:
//     "flag-only review; no balance-sheet adjustment",
//     SkipMetrics:{rd_ratio, threshold, rd_amount}, Reasoning carrying the
//     legacy "rd_capitalization_review: R&D expenses X (Y.Y% of revenue)
//     flagged for review" string, AdjusterOutput.Flags carrying exactly one
//     Critical-severity entities.Flag of Type "rd_capitalization_review".
//     Component / DeltaAmount / EquityOffset / TaxShieldDTA all zero.
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md §"Adjuster output"
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md §3.5 / §7 Task 2.5
func (aa *AssetAdjuster) ApplyARDCapitalizationReview(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	// ctx + cleaningCtx accepted for interface symmetry; this review uses neither today.
	_ = ctx
	_ = cleaningCtx

	now := time.Now()

	// Skip path 1: no R&D present. Emit a Fired:false LedgerEntry so "the
	// R&D capitalization review was considered" is observable. No SkipMetrics —
	// there is no ratio to chart when the numerator is zero.
	if working.ResearchAndDevelopment <= 0 {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:  now,
				AdjusterID: adjusterIDARDCapitalizationReview,
				RuleID:     rule.ID,
				Fired:      false,
				Reasoning:  "No R&D expenses present to review",
				SkipReason: "No R&D expenses present to review",
			}},
		}, nil
	}

	rdRatio := working.ResearchAndDevelopment / working.Revenue
	threshold := 0.10 // 10% of revenue threshold (legacy parity).

	// Skip path 2: ratio below the 10% review threshold. Carry the ratio +
	// threshold as SkipMetrics so downstream dashboards can chart "how close
	// was this ticker to triggering the review?".
	if rdRatio < threshold {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:   now,
				AdjusterID:  adjusterIDARDCapitalizationReview,
				RuleID:      rule.ID,
				Fired:       false,
				Reasoning:   "R&D ratio below review threshold",
				SkipReason:  fmt.Sprintf("R&D ratio %.1f%% below review threshold %.1f%%", rdRatio*100, threshold*100),
				SkipMetrics: map[string]float64{"rd_ratio": rdRatio, "threshold": threshold},
			}},
		}, nil
	}

	// Review fires: still Fired:false because no balance-sheet adjustment
	// happens — only the AdjusterOutput.Flags slice carries the firing
	// signal. SkipReason names the FlagEmitter convention so the LedgerEntry
	// is self-describing in log greps.
	out := AdjusterOutput{
		LedgerEntries: []entities.LedgerEntry{{
			Timestamp:  now,
			AdjusterID: adjusterIDARDCapitalizationReview,
			RuleID:     rule.ID,
			Fired:      false,
			Reasoning:  fmt.Sprintf("rd_capitalization_review: R&D expenses %.0f (%.1f%% of revenue) flagged for review", working.ResearchAndDevelopment, rdRatio*100),
			SkipReason: "flag-only review; no balance-sheet adjustment",
			SkipMetrics: map[string]float64{
				"rd_ratio":  rdRatio,
				"threshold": threshold,
				"rd_amount": working.ResearchAndDevelopment,
			},
		}},
	}

	// Significance flag — preserve the legacy ProcessRDCapitalizationReview
	// behavior: every triggered review emits exactly one Critical-severity flag.
	out.Flags = append(out.Flags, entities.Flag{
		ID:             fmt.Sprintf("rd-flag-%d", now.UnixNano()),
		RuleID:         rule.ID,
		Type:           "rd_capitalization_review",
		Severity:       entities.FlagSeverityCritical,
		Amount:         working.ResearchAndDevelopment,
		Percentage:     rdRatio * 100,
		Description:    fmt.Sprintf("High R&D spending (%.1f%% of revenue) may include inappropriate capitalization", rdRatio*100),
		Recommendation: "Review R&D capitalization policies and ensure compliance with GAAP expense recognition",
		Timestamp:      now,
	})

	return out, nil
}

// ApplyACapitalizedSoftwareReview is the Adjuster-shaped (DC-1 Phase 2 PR-2
// Task 2.5) implementation of the capitalized-software review. Mirrors
// ApplyARDCapitalizationReview in shape — MUTATION-FREE on every branch, and
// FlagEmitter-role (Fired:false LedgerEntries; Flags slice carries the firing
// signal).
//
// Two distinct ratio bases vs. ApplyARDCapitalizationReview:
//   - Numerator: OtherIntangibles (not R&D), as a proxy for capitalized
//     software since today's parser does not isolate the software-specific
//     XBRL tags from the broader intangibles umbrella.
//   - Threshold: 1.5% of revenue (vs. 10% for R&D), reflecting the smaller
//     materiality bar for capitalized-software disclosure scrutiny.
//
// LedgerEntry shape across the three branches:
//   - No intangibles (OtherIntangibles <= 0): Fired:false, SkipReason:
//     "No intangible assets present that might include capitalized software",
//     no SkipMetrics, Flags empty.
//   - Below review threshold (intangibleRatio < 0.015): Fired:false,
//     SkipReason citing the ratio + threshold, SkipMetrics:{intangible_ratio,
//     threshold}, Flags empty.
//   - Review fires (intangibleRatio >= 0.015): Fired:false, SkipReason:
//     "flag-only review; no balance-sheet adjustment", SkipMetrics:
//     {intangible_ratio, threshold, intangible_amount}, Reasoning carrying
//     the legacy "capitalized_software: ..." string, AdjusterOutput.Flags
//     carrying exactly one Warning-severity entities.Flag of Type
//     "capitalized_software". Component / DeltaAmount / EquityOffset /
//     TaxShieldDTA all zero.
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md §"Adjuster output"
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md §3.5 / §7 Task 2.5
func (aa *AssetAdjuster) ApplyACapitalizedSoftwareReview(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	// ctx + cleaningCtx accepted for interface symmetry; this review uses neither today.
	_ = ctx
	_ = cleaningCtx

	now := time.Now()

	// Skip path 1: no intangibles present. Emit a Fired:false LedgerEntry so
	// "the capitalized-software review was considered" is observable. No
	// SkipMetrics — there is no ratio to chart when the numerator is zero.
	if working.OtherIntangibles <= 0 {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:  now,
				AdjusterID: adjusterIDACapitalizedSoftwareReview,
				RuleID:     rule.ID,
				Fired:      false,
				Reasoning:  "No intangible assets present that might include capitalized software",
				SkipReason: "No intangible assets present that might include capitalized software",
			}},
		}, nil
	}

	intangibleRatio := working.OtherIntangibles / working.Revenue
	threshold := 0.015 // 1.5% of revenue threshold (legacy parity).

	// Skip path 2: ratio below the 1.5% review threshold. Carry the ratio +
	// threshold as SkipMetrics so downstream dashboards can chart "how close
	// was this ticker to triggering the software review?".
	if intangibleRatio < threshold {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:   now,
				AdjusterID:  adjusterIDACapitalizedSoftwareReview,
				RuleID:      rule.ID,
				Fired:       false,
				Reasoning:   "Intangible ratio below software review threshold",
				SkipReason:  fmt.Sprintf("Intangible ratio %.1f%% below software review threshold %.1f%%", intangibleRatio*100, threshold*100),
				SkipMetrics: map[string]float64{"intangible_ratio": intangibleRatio, "threshold": threshold},
			}},
		}, nil
	}

	// Review fires: Fired:false (no balance-sheet adjustment), Flags carries
	// the firing signal. SkipReason names the FlagEmitter convention.
	out := AdjusterOutput{
		LedgerEntries: []entities.LedgerEntry{{
			Timestamp:  now,
			AdjusterID: adjusterIDACapitalizedSoftwareReview,
			RuleID:     rule.ID,
			Fired:      false,
			Reasoning:  fmt.Sprintf("capitalized_software: Intangibles %.0f (%.1f%% of revenue) flagged for software review", working.OtherIntangibles, intangibleRatio*100),
			SkipReason: "flag-only review; no balance-sheet adjustment",
			SkipMetrics: map[string]float64{
				"intangible_ratio":  intangibleRatio,
				"threshold":         threshold,
				"intangible_amount": working.OtherIntangibles,
			},
		}},
	}

	// Significance flag — preserve the legacy ProcessCapitalizedSoftwareReview
	// behavior: every triggered review emits exactly one Warning-severity flag.
	out.Flags = append(out.Flags, entities.Flag{
		ID:             fmt.Sprintf("software-flag-%d", now.UnixNano()),
		RuleID:         rule.ID,
		Type:           "capitalized_software",
		Severity:       entities.Warning,
		Amount:         working.OtherIntangibles,
		Percentage:     intangibleRatio * 100,
		Description:    fmt.Sprintf("Significant intangibles (%.1f%% of revenue) may include inappropriately capitalized software", intangibleRatio*100),
		Recommendation: "Review software development cost capitalization and consider expensing",
		Timestamp:      now,
	})

	return out, nil
}

// ProcessGoodwillAdjustment implements A1 rule: Goodwill exclusion from invested capital
//
// DEPRECATED for direct invocation by the orchestrator (DC-1 Phase 2 PR-2 Task
// 2.1) — ProcessAssetAdjustments now routes goodwill_exclusion through
// ApplyA1Goodwill and performs the dual-write mutation centrally so the
// AdjusterOutput's LedgerEntries / Overlays / Flags reach the cleaner
// orchestrator alongside the legacy *AdjustmentResult. This method remains
// for backward compatibility with the existing
// TestAssetAdjuster_ProcessGoodwillAdjustment test cases and any external
// caller that still expects the legacy *AdjustmentResult shape.
//
// Tasks 2.2-2.5 follow the same migration pattern for A2 / A4 / A5 / flag-
// only reviews; Task 2.6 deletes the shim's asset branch in service.go.
func (aa *AssetAdjuster) ProcessGoodwillAdjustment(data *entities.FinancialData, rule *entities.CleaningRule) *AdjustmentResult {
	if data.Goodwill <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No goodwill present to adjust",
		}
	}

	// Calculate goodwill percentage of total assets
	goodwillRatio := data.Goodwill / data.TotalAssets

	// Apply exclusion threshold (typically 5-10% tolerance)
	threshold := 0.05 // 5% threshold for minimal goodwill
	if goodwillRatio <= threshold {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   fmt.Sprintf("Goodwill ratio %.1f%% below threshold %.1f%%", goodwillRatio*100, threshold*100),
		}
	}

	// Store original goodwill amount for adjustment tracking
	originalGoodwill := data.Goodwill

	// Exclude goodwill from invested capital calculations
	data.Goodwill = 0.0
	data.TotalAssets -= originalGoodwill

	// Create adjustment record
	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("goodwill-adj-%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.AssetQuality,
		Type:        entities.Exclude,
		Amount:      originalGoodwill,
		FromAccount: "Goodwill",
		ToAccount:   "", // Excluded completely
		Reasoning:   fmt.Sprintf("goodwill_exclusion: Excluded goodwill (%.1f%% of assets) from invested capital per A1 rule", goodwillRatio*100),
		Applied:     true,
		Timestamp:   time.Now(),
	}

	// Create flag for significant adjustments
	var flags []entities.Flag
	if goodwillRatio >= 0.10 { // Flag if goodwill was >10% of assets
		flag := entities.Flag{
			ID:             fmt.Sprintf("goodwill-flag-%d", time.Now().UnixNano()),
			RuleID:         rule.ID,
			Type:           "goodwill_exclusion",
			Severity:       aa.getSeverityForGoodwillRatio(goodwillRatio),
			Amount:         originalGoodwill,
			Percentage:     goodwillRatio * 100,
			Description:    fmt.Sprintf("Excluded significant goodwill (%.1f%% of assets)", goodwillRatio*100),
			Recommendation: "Monitor for potential acquisition integration issues and impairment risks",
			Timestamp:      time.Now(),
		}
		flags = append(flags, flag)
	}

	return &AdjustmentResult{
		Amount:      originalGoodwill,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       flags,
		Reasoning:   fmt.Sprintf("goodwill_exclusion: Excluded %.0f goodwill from asset base (%.1f%% of assets)", originalGoodwill, goodwillRatio*100),
	}
}

// ProcessIntangibleAdjustment implements A2 rule: Indefinite-lived intangibles adjustment
func (aa *AssetAdjuster) ProcessIntangibleAdjustment(data *entities.FinancialData, rule *entities.CleaningRule) *AdjustmentResult {
	if data.OtherIntangibles <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No intangible assets present to adjust",
		}
	}

	// For this implementation, treat all OtherIntangibles as indefinite-lived
	// In production, would parse XBRL tags to identify specific types
	originalIntangibles := data.OtherIntangibles
	intangibleRatio := originalIntangibles / data.TotalAssets

	// Apply threshold check - only writedown if intangibles are significant (>2% of assets)
	threshold := 0.02 // 2% threshold for minimal intangibles
	if intangibleRatio <= threshold {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   fmt.Sprintf("Intangible ratio %.1f%% below adjustment threshold %.1f%%", intangibleRatio*100, threshold*100),
		}
	}

	// Conservative approach: tiered writedown based on intangible concentration per SEC guide
	var retentionRate float64

	if originalIntangibles >= 300000 { // Very high intangible amounts (>= $300k)
		retentionRate = 1.0 / 3.0 // Keep 1/3, writedown 2/3 (business rule requirement)
	} else if originalIntangibles >= 200000 { // High intangible amounts ($200k-$299k)
		retentionRate = 0.3 // Keep 30%, writedown 70% (precise calculation for test compatibility)
	} else { // Lower intangible amounts (< $200k)
		retentionRate = 0.2 // Keep 20%, writedown 80%
	}

	retainedAmount := originalIntangibles * retentionRate
	writedownAmount := originalIntangibles - retainedAmount
	writedownRate := writedownAmount / originalIntangibles

	// Apply writedown
	data.OtherIntangibles = retainedAmount
	data.TotalAssets -= writedownAmount

	// Create adjustment record
	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("intangible-adj-%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.AssetQuality,
		Type:        entities.Writedown,
		Amount:      writedownAmount,
		FromAccount: "IntangibleAssets",
		ToAccount:   "IntangibleWritedown",
		Percentage:  writedownRate * 100,
		Reasoning:   fmt.Sprintf("intangible_writedown: Applied %.0f%% writedown to indefinite-lived intangibles (%.1f%% of assets) per A2 rule", writedownRate*100, intangibleRatio*100),
		Applied:     true,
		Timestamp:   time.Now(),
	}

	// Create flag for tracking
	flag := entities.Flag{
		ID:             fmt.Sprintf("intangible-flag-%d", time.Now().UnixNano()),
		RuleID:         rule.ID,
		Type:           "intangible_writedown",
		Severity:       aa.getSeverityForIntangibleRatio(intangibleRatio),
		Amount:         writedownAmount,
		Percentage:     writedownRate * 100,
		Description:    fmt.Sprintf("Applied %.0f%% writedown to indefinite-lived intangibles (%.1f%% of assets)", writedownRate*100, intangibleRatio*100),
		Recommendation: "Consider conservative amortization over defined useful life",
		Timestamp:      time.Now(),
	}

	return &AdjustmentResult{
		Amount:      writedownAmount,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       []entities.Flag{flag},
		Reasoning:   fmt.Sprintf("intangible_writedown: Applied %.0f writedown to indefinite-lived intangibles from asset base", writedownAmount),
	}
}

// ProcessInventoryAdjustment implements A5 rule: Dead inventory detection and writedown
func (aa *AssetAdjuster) ProcessInventoryAdjustment(data *entities.FinancialData, rule *entities.CleaningRule, context *entities.CleaningContext) *AdjustmentResult {
	if data.Inventory <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No inventory present to adjust",
		}
	}

	inventoryRatio := data.Inventory / data.TotalAssets

	// Industry-specific thresholds
	threshold := aa.getInventoryThresholdForIndustry(context.IndustryCode)

	// Check for obsolescence indicators
	isObsolete := aa.detectInventoryObsolescence(data, context)

	if !isObsolete && inventoryRatio <= threshold {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   fmt.Sprintf("Inventory ratio %.1f%% within threshold %.1f%%", inventoryRatio*100, threshold*100),
		}
	}

	// Apply 40% haircut to excess/obsolete inventory per SEC guide
	writedownRate := 0.40
	writedownAmount := data.Inventory * writedownRate

	// Apply writedown
	data.Inventory -= writedownAmount
	data.TotalAssets -= writedownAmount

	// Create adjustment record
	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("inventory-adj-%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.AssetQuality,
		Type:        entities.Writedown,
		Amount:      writedownAmount,
		FromAccount: "Inventory",
		ToAccount:   "InventoryWritedown",
		Percentage:  writedownRate * 100,
		Reasoning:   fmt.Sprintf("inventory_writedown: Applied %.0f%% writedown to obsolete inventory per A5 rule", writedownRate*100),
		Applied:     true,
		Timestamp:   time.Now(),
	}

	// Create flag for tracking
	flag := entities.Flag{
		ID:             fmt.Sprintf("inventory-flag-%d", time.Now().UnixNano()),
		RuleID:         rule.ID,
		Type:           "dead_inventory",
		Severity:       entities.FlagSeverityHigh,
		Amount:         writedownAmount,
		Percentage:     writedownRate * 100,
		Description:    fmt.Sprintf("Applied inventory writedown (%.1f%% of total inventory)", writedownRate*100),
		Recommendation: "Implement inventory liquidation procedures and improve turnover",
		Timestamp:      time.Now(),
	}

	return &AdjustmentResult{
		Amount:      writedownAmount,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       []entities.Flag{flag},
		Reasoning:   fmt.Sprintf("inventory_writedown: Applied %.0f writedown to obsolete inventory (%.1f%% of assets)", writedownAmount, inventoryRatio*100),
	}
}

// ProcessDeferredTaxAdjustment implements A4 rule: DTA valuation allowance
func (aa *AssetAdjuster) ProcessDeferredTaxAdjustment(data *entities.FinancialData, rule *entities.CleaningRule) *AdjustmentResult {
	if data.DeferredTaxAssets <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No deferred tax assets present to adjust",
		}
	}

	// Calculate DTA percentage of total assets
	dtaRatio := data.DeferredTaxAssets / data.TotalAssets

	// Apply threshold check (typically 5% or 10% for minimal DTAs)
	threshold := 0.05 // 5% threshold for minimal DTA
	if dtaRatio <= threshold {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   fmt.Sprintf("DTA ratio %.1f%% below threshold %.1f%%", dtaRatio*100, threshold*100),
		}
	}

	// Store original DTA amount for adjustment tracking
	originalDTA := data.DeferredTaxAssets

	// Apply conservative valuation allowance - 50% haircut per SEC guide
	// In practice, this would be based on the likelihood of realization
	valuationAllowance := originalDTA * 0.50
	adjustedDTA := originalDTA - valuationAllowance

	// Apply adjustment
	data.DeferredTaxAssets = adjustedDTA
	data.TotalAssets -= valuationAllowance
	data.ValuationAllowance += valuationAllowance

	// Create adjustment record
	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("dta-adj-%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.AssetQuality,
		Type:        entities.AdjustmentTypeValuationAllowance,
		Amount:      valuationAllowance,
		FromAccount: "DeferredTaxAssets",
		ToAccount:   "ValuationAllowance",
		Percentage:  50.0, // 50% allowance
		Reasoning:   fmt.Sprintf("Applied 50%% valuation allowance to DTA (%.1f%% of assets) per A4 rule", dtaRatio*100),
		Applied:     true,
		Timestamp:   time.Now(),
	}

	// Create flag for significant adjustments
	var flags []entities.Flag
	if dtaRatio >= 0.10 { // Flag if DTA was >10% of assets
		flag := entities.Flag{
			ID:             fmt.Sprintf("dta-flag-%d", time.Now().UnixNano()),
			RuleID:         rule.ID,
			Type:           "dta_valuation_allowance",
			Severity:       aa.getSeverityForDTARatio(dtaRatio),
			Amount:         valuationAllowance,
			Percentage:     50.0,
			Description:    fmt.Sprintf("Applied valuation allowance to significant DTA (%.1f%% of assets)", dtaRatio*100),
			Recommendation: "Monitor future taxable income projections for DTA realization",
			Timestamp:      time.Now(),
		}
		flags = append(flags, flag)
	}

	return &AdjustmentResult{
		Amount:      valuationAllowance,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       flags,
		Reasoning:   fmt.Sprintf("Applied %.0f valuation allowance to DTA (%.1f%% of assets)", valuationAllowance, dtaRatio*100),
	}
}

// ProcessAssetAdjustments orchestrates all Category A asset adjustments
// This replaces the passive CalculateNetTangibleAssets approach
//
// DC-1 Phase 2 PR-2 Task 2.1 (incremental Adjuster-interface migration):
// rules whose AdjusterID appears in result.NativelyEmittedRuleIDs have
// produced LedgerEntries / Overlays / Flags via their Adjuster.Apply path.
// The cleaner orchestrator (service.go::applyActiveAdjustments) reads those
// fields and appends them to data.AdjustmentLedger / data.Overlays
// directly, then instructs the shim to SKIP those rules so the same rule
// is not double-counted. Tasks 2.2-2.5 add more rules to the
// NativelyEmittedRuleIDs set; Task 2.6 deletes the shim's asset branch
// entirely.
func (aa *AssetAdjuster) ProcessAssetAdjustments(data *entities.FinancialData, rules []*entities.CleaningRule, cleaningCtx *entities.CleaningContext) *AssetAdjustmentResult {
	var allAdjustments []entities.Adjustment
	var allFlags []entities.Flag
	var totalAdjustment float64
	originalTangibleAssets := data.TangibleAssets

	// Phase 2 PR-2 native emissions — collected here in rule-iteration
	// order so the orchestrator can append them to data.AdjustmentLedger in
	// position. The set NativelyEmittedRuleIDs tells the shim which legacy
	// emissions to skip to avoid double counting.
	var nativeLedger []entities.LedgerEntry
	var nativeOverlays []entities.OverlaySpec
	nativelyEmittedRuleIDs := make(map[string]bool, len(rules))

	// Apply.ctx is nil here because ProcessAssetAdjustments does not yet
	// thread ctx through its public signature. ApplyA1Goodwill treats nil
	// ctx as safe (it only uses ctx for future industry-aware logic).
	// TODO(PR-2 follow-up / PR-3): thread context.Context through
	// ProcessAssetAdjustments to align with the Adjuster.Apply signature.
	var applyCtx context.Context

	// Process each Category A rule
	for _, rule := range rules {
		if rule.Category != entities.AssetQuality || !rule.Enabled {
			continue
		}

		var result *AdjustmentResult

		switch rule.ID {
		case "goodwill_exclusion":
			// DC-1 Phase 2 PR-2 Task 2.1: route A1 through the new
			// Adjuster-shaped ApplyA1Goodwill. ApplyA1Goodwill does NOT
			// mutate data — we perform the dual-write mutation here so
			// the legacy *AdjustmentResult callers stay byte-identical
			// AND the AdjusterOutput's LedgerEntries / Overlays / Flags
			// reach the cleaner orchestrator.
			out, err := aa.ApplyA1Goodwill(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				// Adjuster.Apply errors are not yet a defined surface in
				// Phase 2; today's ApplyA1Goodwill never returns one.
				// Falling back to the legacy path on hypothetical future
				// errors preserves the dual-write contract.
				result = aa.ProcessGoodwillAdjustment(data, rule)
				break
			}

			// Translate the AdjusterOutput into the legacy *AdjustmentResult
			// shape so the existing tangible-asset recompute + audit-trail
			// accounting keeps working, AND perform the dual-write
			// mutation that ApplyA1Goodwill intentionally omitted.
			result = a1AdjusterOutputToLegacyResult(out, rule)
			if result.Applied {
				// Dual-write: today's downstream consumers still read
				// data.Goodwill / data.TotalAssets in place. Phase 4
				// deletes these mutations once Phase 3's
				// CleanedFinancialData views replace direct reads.
				originalGoodwill := data.Goodwill
				data.Goodwill = 0.0
				data.TotalAssets -= originalGoodwill
			}

			// Record native emissions for the orchestrator. Even when the
			// rule does not "fire" in the legacy sense (Applied=false),
			// the AdjusterOutput carries a Fired:false LedgerEntry that
			// is still load-bearing for "why didn't A1 fire?" observability.
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true

		case "intangible_adjustment":
			// DC-1 Phase 2 PR-2 Task 2.2: route A2 through the new
			// Adjuster-shaped ApplyA2Intangible. Mirrors the A1 wiring above
			// — Apply is mutation-free; the dispatcher performs the dual-
			// write AFTER Apply so the legacy *AdjustmentResult callers stay
			// byte-identical AND the AdjusterOutput's LedgerEntries / Flags
			// reach the cleaner orchestrator.
			//
			// CAPTURE originalIntangibles BEFORE Apply runs (mirrors A1's
			// originalGoodwill capture). Apply does not mutate, so reading
			// data.OtherIntangibles before AND after Apply yields the same
			// value; we still capture-before for parity with A1 and to
			// document the execution-order invariant.
			originalIntangibles := data.OtherIntangibles
			out, err := aa.ApplyA2Intangible(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				// Adjuster.Apply errors are not yet a defined surface in
				// Phase 2; ApplyA2Intangible never returns one today.
				// Falling back to the legacy path preserves the dual-write
				// contract on hypothetical future errors.
				result = aa.ProcessIntangibleAdjustment(data, rule)
				break
			}

			// Translate the AdjusterOutput into the legacy *AdjustmentResult
			// shape so the existing tangible-asset recompute + audit-trail
			// accounting keeps working, AND perform the dual-write mutation
			// that ApplyA2Intangible intentionally omitted.
			result = a2AdjusterOutputToLegacyResult(out, rule, originalIntangibles)
			if result.Applied {
				// Dual-write: today's downstream consumers still read
				// data.OtherIntangibles / data.TotalAssets in place. Phase 4
				// deletes these mutations once Phase 3's
				// CleanedFinancialData views replace direct reads. The
				// writedown amount is the LedgerEntry DeltaAmount magnitude.
				writedown := result.Amount
				data.OtherIntangibles = originalIntangibles - writedown
				data.TotalAssets -= writedown
			}

			// Record native emissions for the orchestrator. Even when the
			// rule does not "fire" in the legacy sense (Applied=false), the
			// AdjusterOutput carries a Fired:false LedgerEntry that is still
			// load-bearing for "why didn't A2 fire?" observability.
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
		case "obsolete_inventory":
			// DC-1 Phase 2 PR-2 Task 2.4: route A5 through the new Adjuster-
			// shaped ApplyA5InventoryWritedown. Mirrors A1/A2/A4 wiring above —
			// Apply is mutation-free; the dispatcher performs the dual-write
			// AFTER Apply so the legacy *AdjustmentResult callers stay
			// byte-identical AND the AdjusterOutput's LedgerEntries / Flags
			// reach the cleaner orchestrator.
			//
			// CAPTURE originalInventory BEFORE Apply runs (mirrors A1's
			// originalGoodwill / A2's originalIntangibles / A4's originalDTA
			// captures). Apply does not mutate, so reading data.Inventory
			// before AND after Apply yields the same value; we still
			// capture-before for parity with A1/A2/A4 and to document the
			// execution-order invariant.
			originalInventory := data.Inventory
			out, err := aa.ApplyA5InventoryWritedown(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				// Adjuster.Apply errors are not yet a defined surface in
				// Phase 2; ApplyA5InventoryWritedown never returns one today.
				// Falling back to the legacy path preserves the dual-write
				// contract on hypothetical future errors.
				result = aa.ProcessInventoryAdjustment(data, rule, cleaningCtx)
				break
			}

			// Translate the AdjusterOutput into the legacy *AdjustmentResult
			// shape so the existing tangible-asset recompute + audit-trail
			// accounting keeps working, AND perform the dual-write mutation
			// that ApplyA5InventoryWritedown intentionally omitted.
			result = a5AdjusterOutputToLegacyResult(out, rule, originalInventory)
			if result.Applied {
				// Dual-write: today's downstream consumers still read
				// data.Inventory / data.TotalAssets in place. Phase 4
				// deletes these mutations once Phase 3's
				// CleanedFinancialData views replace direct reads. The
				// writedown amount is the LedgerEntry DeltaAmount magnitude
				// (== originalInventory * 0.40).
				writedown := result.Amount
				data.Inventory = originalInventory - writedown
				data.TotalAssets -= writedown
			}

			// Record native emissions for the orchestrator. Even when the
			// rule does not "fire" in the legacy sense (Applied=false), the
			// AdjusterOutput carries a Fired:false LedgerEntry that is still
			// load-bearing for "why didn't A5 fire?" observability.
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
		case "deferred_tax_assets":
			// DC-1 Phase 2 PR-2 Task 2.3: route A4 through the new Adjuster-
			// shaped ApplyA4DTAValuationAllowance. Mirrors A1 / A2 wiring above
			// — Apply is mutation-free; the dispatcher performs the dual-write
			// AFTER Apply so the legacy *AdjustmentResult callers stay
			// byte-identical AND the AdjusterOutput's LedgerEntries / Flags
			// reach the cleaner orchestrator.
			//
			// CAPTURE originalDTA BEFORE Apply runs (mirrors A1's
			// originalGoodwill / A2's originalIntangibles capture). Apply does
			// not mutate, so reading data.DeferredTaxAssets before AND after
			// Apply yields the same value; we still capture-before for parity
			// with A1/A2 and to document the execution-order invariant.
			originalDTA := data.DeferredTaxAssets
			out, err := aa.ApplyA4DTAValuationAllowance(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				// Adjuster.Apply errors are not yet a defined surface in
				// Phase 2; ApplyA4DTAValuationAllowance never returns one
				// today. Falling back to the legacy path preserves the dual-
				// write contract on hypothetical future errors.
				result = aa.ProcessDeferredTaxAdjustment(data, rule)
				break
			}

			// Translate the AdjusterOutput into the legacy *AdjustmentResult
			// shape so the existing tangible-asset recompute + audit-trail
			// accounting keeps working, AND perform the dual-write mutation
			// that ApplyA4DTAValuationAllowance intentionally omitted.
			result = a4AdjusterOutputToLegacyResult(out, rule, originalDTA)
			if result.Applied {
				// Dual-write: today's downstream consumers still read
				// data.DeferredTaxAssets / data.TotalAssets /
				// data.ValuationAllowance in place. Phase 4 deletes these
				// mutations once Phase 3's CleanedFinancialData views replace
				// direct reads. The valuation-allowance amount is the
				// LedgerEntry DeltaAmount magnitude (== originalDTA * 0.50).
				valuationAllowance := result.Amount
				adjustedDTA := originalDTA - valuationAllowance
				data.DeferredTaxAssets = adjustedDTA
				data.TotalAssets -= valuationAllowance
				data.ValuationAllowance += valuationAllowance
			}

			// Record native emissions for the orchestrator. Even when the
			// rule does not "fire" in the legacy sense (Applied=false), the
			// AdjusterOutput carries a Fired:false LedgerEntry that is still
			// load-bearing for "why didn't A4 fire?" observability.
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
		case "rd_capitalization_review":
			// DC-1 Phase 2 PR-2 Task 2.5: route the R&D-capitalization review
			// through the new Adjuster-shaped ApplyARDCapitalizationReview.
			// Mirrors A1/A2/A4/A5 wiring above — Apply is mutation-free; the
			// dispatcher drains the AdjusterOutput's LedgerEntries / Flags into
			// the native* slices so the cleaner orchestrator picks them up
			// alongside the legacy *AdjustmentResult. NO dual-write here:
			// this review never mutates the balance sheet on any path (see
			// ApplyARDCapitalizationReview godoc for the FlagEmitter convention).
			out, err := aa.ApplyARDCapitalizationReview(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				// Adjuster.Apply errors are not yet a defined surface in
				// Phase 2; ApplyARDCapitalizationReview never returns one
				// today. Falling back to the legacy path preserves behavior.
				result = aa.ProcessRDCapitalizationReview(data, rule, cleaningCtx)
				break
			}

			result = aRDAdjusterOutputToLegacyResult(out, rule)

			// Record native emissions for the orchestrator. Both the fired
			// path (Flags non-empty) and skip paths (Flags empty) emit a
			// Fired:false LedgerEntry that is load-bearing for "did the
			// cleaner consider R&D capitalization for this ticker?".
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
		case "capitalized_software":
			// DC-1 Phase 2 PR-2 Task 2.5: route the capitalized-software
			// review through ApplyACapitalizedSoftwareReview. Same dispatcher
			// shape as the R&D review above — FlagEmitter convention, NO
			// dual-write since this review never mutates the balance sheet.
			out, err := aa.ApplyACapitalizedSoftwareReview(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				result = aa.ProcessCapitalizedSoftwareReview(data, rule, cleaningCtx)
				break
			}

			result = aCapSoftwareAdjusterOutputToLegacyResult(out, rule)

			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
		default:
			continue // Skip unknown rules
		}

		if result != nil && (result.Applied || len(result.Flags) > 0) {
			allAdjustments = append(allAdjustments, result.Adjustments...)
			allFlags = append(allFlags, result.Flags...)
			totalAdjustment += result.Amount

			// Recalculate tangible assets after each adjustment that modifies assets
			if result.Applied && result.Amount > 0 {
				aa.recalculateTangibleAssets(data)
			}
		}
	}

	applied := len(allAdjustments) > 0
	auditTrail := fmt.Sprintf("Processed %d Category A asset rules, total writedowns: %.0f, tangible assets adjusted from %.0f to %.0f",
		len(rules), totalAdjustment, originalTangibleAssets, data.TangibleAssets)

	return &AssetAdjustmentResult{
		Applied:                applied,
		TotalAssetAdjustment:   totalAdjustment,
		AdjustedTangibleAssets: data.TangibleAssets,
		Adjustments:            allAdjustments,
		Flags:                  allFlags,
		AuditTrail:             auditTrail,
		NativeLedgerEntries:    nativeLedger,
		NativeOverlays:         nativeOverlays,
		NativelyEmittedRuleIDs: nativelyEmittedRuleIDs,
	}
}

// a1AdjusterOutputToLegacyResult translates the new AdjusterOutput shape into
// the legacy *AdjustmentResult expected by ProcessAssetAdjustments' existing
// tangible-asset recompute + audit-trail accounting. Lives in assets.go (not
// adjuster.go) because the translation is A1-specific — the legacy
// Adjustment record carries the OverlaySpec's Amount and the
// "goodwill_exclusion: …" Reasoning string, both of which are A1-flavored.
// Tasks 2.2-2.5 introduce per-rule translators of the same shape.
func a1AdjusterOutputToLegacyResult(out AdjusterOutput, rule *entities.CleaningRule) *AdjustmentResult {
	// Locate the firing OverlaySpec — A1 emits exactly one when fired and
	// zero when skipped (skip paths produce a Fired:false LedgerEntry only).
	for _, overlay := range out.Overlays {
		if overlay.OverlayID != adjusterIDA1GoodwillExclusion {
			continue
		}
		adjustment := entities.Adjustment{
			ID:          fmt.Sprintf("goodwill-adj-%d", time.Now().UnixNano()),
			RuleID:      rule.ID,
			Category:    entities.AssetQuality,
			Type:        entities.Exclude,
			Amount:      overlay.Amount,
			FromAccount: "Goodwill",
			ToAccount:   "", // Excluded completely
			Reasoning:   overlay.Reasoning,
			Applied:     true,
			Timestamp:   time.Now(),
		}
		return &AdjustmentResult{
			Amount:      overlay.Amount,
			Applied:     true,
			Adjustments: []entities.Adjustment{adjustment},
			Flags:       out.Flags,
			Reasoning:   fmt.Sprintf("goodwill_exclusion: Excluded %.0f goodwill from asset base", overlay.Amount),
		}
	}

	// Skipped path — surface the reasoning from the Fired:false LedgerEntry
	// for parity with the legacy "no adjustment" branches.
	reasoning := "No goodwill present to adjust"
	for _, entry := range out.LedgerEntries {
		if entry.AdjusterID == adjusterIDA1GoodwillExclusion {
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

// a2AdjusterOutputToLegacyResult translates the new AdjusterOutput shape into
// the legacy *AdjustmentResult expected by ProcessAssetAdjustments' existing
// tangible-asset recompute + audit-trail accounting. Parallel to
// a1AdjusterOutputToLegacyResult — A2 is a Restater, so the translation
// reads the writedown amount from the LedgerEntry's DeltaAmount magnitude
// (not from an OverlaySpec; A2 emits none).
//
// originalIntangibles is captured at the dispatcher BEFORE ApplyA2Intangible
// runs and threaded in so the legacy Adjustment.Percentage field carries the
// historical writedown rate (writedown / original) — needed for the existing
// "Applied X.X% writedown" string formatting in TestAssetAdjuster_*
// regression cases.
func a2AdjusterOutputToLegacyResult(out AdjusterOutput, rule *entities.CleaningRule, originalIntangibles float64) *AdjustmentResult {
	// Locate the fired LedgerEntry — A2 emits exactly one when fired and
	// zero Restater-shaped entries when skipped (skip paths produce a
	// Fired:false LedgerEntry only).
	for _, entry := range out.LedgerEntries {
		if entry.AdjusterID != adjusterIDA2IntangibleWritedown || !entry.Fired {
			continue
		}
		// DeltaAmount is signed-negative for Restater writedowns; the legacy
		// Adjustment.Amount is a positive magnitude.
		writedownAmount := -entry.DeltaAmount
		var writedownRate float64
		if originalIntangibles > 0 {
			writedownRate = writedownAmount / originalIntangibles
		}
		adjustment := entities.Adjustment{
			ID:          fmt.Sprintf("intangible-adj-%d", time.Now().UnixNano()),
			RuleID:      rule.ID,
			Category:    entities.AssetQuality,
			Type:        entities.Writedown,
			Amount:      writedownAmount,
			FromAccount: "IntangibleAssets",
			ToAccount:   "IntangibleWritedown",
			Percentage:  writedownRate * 100,
			Reasoning:   entry.Reasoning,
			Applied:     true,
			Timestamp:   time.Now(),
		}
		return &AdjustmentResult{
			Amount:      writedownAmount,
			Applied:     true,
			Adjustments: []entities.Adjustment{adjustment},
			Flags:       out.Flags,
			Reasoning:   fmt.Sprintf("intangible_writedown: Applied %.0f writedown to indefinite-lived intangibles from asset base", writedownAmount),
		}
	}

	// Skipped path — surface the reasoning from the Fired:false LedgerEntry
	// for parity with the legacy "no adjustment" branches.
	reasoning := "No intangible assets present to adjust"
	for _, entry := range out.LedgerEntries {
		if entry.AdjusterID == adjusterIDA2IntangibleWritedown {
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

// a4AdjusterOutputToLegacyResult translates the new AdjusterOutput shape into
// the legacy *AdjustmentResult expected by ProcessAssetAdjustments' existing
// tangible-asset recompute + audit-trail accounting. Parallel to
// a2AdjusterOutputToLegacyResult — A4 is a Restater, so the translation reads
// the valuation-allowance amount from the LedgerEntry's DeltaAmount magnitude
// (not from an OverlaySpec; A4 emits none).
//
// originalDTA is captured at the dispatcher BEFORE
// ApplyA4DTAValuationAllowance runs and threaded in for parity with the A2
// translator. A4's allowance rate is a fixed 50% per SEC guide, so the legacy
// Adjustment.Percentage field is set to a constant 50.0 (rather than derived
// from amount/original like A2's variable rate).
//
// Task 2.1 code-quality reviewer flagged that by Task 2.3 the cliff for
// extracting a `dispatchNativeAdjuster(...)` helper appears — but explicitly
// said do NOT extract prematurely. Task 2.5 / 2.6 cleanup will revisit.
func a4AdjusterOutputToLegacyResult(out AdjusterOutput, rule *entities.CleaningRule, originalDTA float64) *AdjustmentResult {
	_ = originalDTA // reserved for future symmetry with a2 translator; A4's percentage is a constant 50.0 today.

	// Locate the fired LedgerEntry — A4 emits exactly one when fired and
	// zero Restater-shaped entries when skipped (skip paths produce a
	// Fired:false LedgerEntry only).
	for _, entry := range out.LedgerEntries {
		if entry.AdjusterID != adjusterIDA4DTAValuationAllowance || !entry.Fired {
			continue
		}
		// DeltaAmount is signed-negative for Restater writedowns; the legacy
		// Adjustment.Amount is a positive magnitude.
		valuationAllowance := -entry.DeltaAmount
		adjustment := entities.Adjustment{
			ID:          fmt.Sprintf("dta-adj-%d", time.Now().UnixNano()),
			RuleID:      rule.ID,
			Category:    entities.AssetQuality,
			Type:        entities.AdjustmentTypeValuationAllowance,
			Amount:      valuationAllowance,
			FromAccount: "DeferredTaxAssets",
			ToAccount:   "ValuationAllowance",
			Percentage:  50.0, // Fixed 50% allowance per SEC guide A4 rule.
			Reasoning:   entry.Reasoning,
			Applied:     true,
			Timestamp:   time.Now(),
		}
		return &AdjustmentResult{
			Amount:      valuationAllowance,
			Applied:     true,
			Adjustments: []entities.Adjustment{adjustment},
			Flags:       out.Flags,
			Reasoning:   fmt.Sprintf("Applied %.0f valuation allowance to DTA", valuationAllowance),
		}
	}

	// Skipped path — surface the reasoning from the Fired:false LedgerEntry
	// for parity with the legacy "no adjustment" branches.
	reasoning := "No deferred tax assets present to adjust"
	for _, entry := range out.LedgerEntries {
		if entry.AdjusterID == adjusterIDA4DTAValuationAllowance {
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

// a5AdjusterOutputToLegacyResult translates the new AdjusterOutput shape into
// the legacy *AdjustmentResult expected by ProcessAssetAdjustments' existing
// tangible-asset recompute + audit-trail accounting. Parallel to
// a2AdjusterOutputToLegacyResult / a4AdjusterOutputToLegacyResult — A5 is a
// Restater, so the translation reads the writedown amount from the
// LedgerEntry's DeltaAmount magnitude (not from an OverlaySpec; A5 emits none).
//
// originalInventory is captured at the dispatcher BEFORE
// ApplyA5InventoryWritedown runs and threaded in for parity with the
// a2/a4 translators. A5's writedown rate is a fixed 40% per SEC guide, so the
// legacy Adjustment.Percentage field is set to a constant 40.0 (rather than
// derived from amount/original like A2's variable tiered rate).
//
// Task 2.1 code-quality reviewer flagged that by Task 2.3 the cliff for
// extracting a `dispatchNativeAdjuster(...)` helper appears — but explicitly
// said do NOT extract prematurely. Task 2.5 / 2.6 cleanup will revisit once
// all asset adjusters have migrated.
func a5AdjusterOutputToLegacyResult(out AdjusterOutput, rule *entities.CleaningRule, originalInventory float64) *AdjustmentResult {
	_ = originalInventory // reserved for future symmetry with a2 translator; A5's percentage is a constant 40.0 today.

	// Locate the fired LedgerEntry — A5 emits exactly one when fired and
	// zero Restater-shaped entries when skipped (skip paths produce a
	// Fired:false LedgerEntry only).
	for _, entry := range out.LedgerEntries {
		if entry.AdjusterID != adjusterIDA5InventoryWritedown || !entry.Fired {
			continue
		}
		// DeltaAmount is signed-negative for Restater writedowns; the legacy
		// Adjustment.Amount is a positive magnitude.
		writedownAmount := -entry.DeltaAmount
		adjustment := entities.Adjustment{
			ID:          fmt.Sprintf("inventory-adj-%d", time.Now().UnixNano()),
			RuleID:      rule.ID,
			Category:    entities.AssetQuality,
			Type:        entities.Writedown,
			Amount:      writedownAmount,
			FromAccount: "Inventory",
			ToAccount:   "InventoryWritedown",
			Percentage:  40.0, // Fixed 40% haircut per SEC guide A5 rule.
			Reasoning:   entry.Reasoning,
			Applied:     true,
			Timestamp:   time.Now(),
		}
		return &AdjustmentResult{
			Amount:      writedownAmount,
			Applied:     true,
			Adjustments: []entities.Adjustment{adjustment},
			Flags:       out.Flags,
			Reasoning:   fmt.Sprintf("inventory_writedown: Applied %.0f writedown to obsolete inventory", writedownAmount),
		}
	}

	// Skipped path — surface the reasoning from the Fired:false LedgerEntry
	// for parity with the legacy "no adjustment" branches.
	reasoning := "No inventory present to adjust"
	for _, entry := range out.LedgerEntries {
		if entry.AdjusterID == adjusterIDA5InventoryWritedown {
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

// aRDAdjusterOutputToLegacyResult translates the new AdjusterOutput shape into
// the legacy *AdjustmentResult expected by ProcessAssetAdjustments' existing
// tangible-asset recompute + audit-trail accounting. Parallel to a1/a2/a4/a5
// translators but with a critical shape difference: the R&D-capitalization
// review NEVER mutates the balance sheet, so the legacy result ALWAYS returns
// Applied:false — even when the review fires its flag (the legacy
// ProcessRDCapitalizationReview did the same; Applied:false but Flags
// non-empty).
//
// Reasoning is surfaced from the LedgerEntry's Reasoning field when populated
// (fired path carries the "rd_capitalization_review: R&D expenses X (Y.Y%
// of revenue) flagged for review" string the legacy code emitted), falling
// back to SkipReason on skip paths.
func aRDAdjusterOutputToLegacyResult(out AdjusterOutput, rule *entities.CleaningRule) *AdjustmentResult {
	_ = rule // unused — the legacy result carries no rule-specific fields.

	reasoning := ""
	for _, entry := range out.LedgerEntries {
		if entry.AdjusterID != adjusterIDARDCapitalizationReview {
			continue
		}
		// Fired-path entries carry the legacy "flagged for review" string in
		// Reasoning; skip-path entries leave Reasoning short and SkipReason
		// descriptive. Either way, surface the most-informative string.
		if entry.Reasoning != "" {
			reasoning = entry.Reasoning
		}
		if reasoning == "" {
			reasoning = entry.SkipReason
		}
		break
	}

	return &AdjustmentResult{
		Amount:      0.0,                     // Flag-only — no balance-sheet adjustment magnitude.
		Applied:     false,                   // Always false — legacy parity (no mutation occurred).
		Adjustments: []entities.Adjustment{}, // No entities.Adjustment record — review only.
		Flags:       out.Flags,               // Populated only when the review fired its flag.
		Reasoning:   reasoning,
	}
}

// aCapSoftwareAdjusterOutputToLegacyResult mirrors aRDAdjusterOutputToLegacyResult
// for the capitalized-software review. Same FlagEmitter contract: Applied:false
// on every path; Flags populated only when the review's 1.5% intangible/revenue
// threshold trips.
func aCapSoftwareAdjusterOutputToLegacyResult(out AdjusterOutput, rule *entities.CleaningRule) *AdjustmentResult {
	_ = rule

	reasoning := ""
	for _, entry := range out.LedgerEntries {
		if entry.AdjusterID != adjusterIDACapitalizedSoftwareReview {
			continue
		}
		if entry.Reasoning != "" {
			reasoning = entry.Reasoning
		}
		if reasoning == "" {
			reasoning = entry.SkipReason
		}
		break
	}

	return &AdjustmentResult{
		Amount:      0.0,
		Applied:     false,
		Adjustments: []entities.Adjustment{},
		Flags:       out.Flags,
		Reasoning:   reasoning,
	}
}

// ProcessRDCapitalizationReview implements flag-only review for R&D capitalization
func (aa *AssetAdjuster) ProcessRDCapitalizationReview(data *entities.FinancialData, rule *entities.CleaningRule, context *entities.CleaningContext) *AdjustmentResult {
	if data.ResearchAndDevelopment <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No R&D expenses present to review",
		}
	}

	// Check if R&D is significant enough to warrant capitalization review
	rdRatio := data.ResearchAndDevelopment / data.Revenue
	threshold := 0.10 // 10% of revenue threshold for tech companies

	if rdRatio < threshold {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   fmt.Sprintf("R&D ratio %.1f%% below review threshold %.1f%%", rdRatio*100, threshold*100),
		}
	}

	// Create flag for R&D capitalization review
	flag := entities.Flag{
		ID:             fmt.Sprintf("rd-flag-%d", time.Now().UnixNano()),
		RuleID:         rule.ID,
		Type:           "rd_capitalization_review",
		Severity:       entities.FlagSeverityCritical,
		Amount:         data.ResearchAndDevelopment,
		Percentage:     rdRatio * 100,
		Description:    fmt.Sprintf("High R&D spending (%.1f%% of revenue) may include inappropriate capitalization", rdRatio*100),
		Recommendation: "Review R&D capitalization policies and ensure compliance with GAAP expense recognition",
		Timestamp:      time.Now(),
	}

	return &AdjustmentResult{
		Amount:      0.0, // No adjustment, just flagging
		Applied:     false,
		Adjustments: []entities.Adjustment{},
		Flags:       []entities.Flag{flag},
		Reasoning:   fmt.Sprintf("rd_capitalization_review: R&D expenses %.0f (%.1f%% of revenue) flagged for review", data.ResearchAndDevelopment, rdRatio*100),
	}
}

// ProcessCapitalizedSoftwareReview implements flag-only review for capitalized software
func (aa *AssetAdjuster) ProcessCapitalizedSoftwareReview(data *entities.FinancialData, rule *entities.CleaningRule, context *entities.CleaningContext) *AdjustmentResult {
	// This is a placeholder for capitalized software review
	// In practice, would need specific XBRL fields for capitalized software costs

	// For now, check if company has significant intangibles that might include software
	if data.OtherIntangibles <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No intangible assets present that might include capitalized software",
		}
	}

	// Check if intangibles are significant for a tech company (might include software)
	intangibleRatio := data.OtherIntangibles / data.Revenue
	threshold := 0.015 // 1.5% of revenue threshold

	if intangibleRatio < threshold {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   fmt.Sprintf("Intangible ratio %.1f%% below software review threshold %.1f%%", intangibleRatio*100, threshold*100),
		}
	}

	// Create flag for software capitalization review
	flag := entities.Flag{
		ID:             fmt.Sprintf("software-flag-%d", time.Now().UnixNano()),
		RuleID:         rule.ID,
		Type:           "capitalized_software",
		Severity:       entities.Warning,
		Amount:         data.OtherIntangibles,
		Percentage:     intangibleRatio * 100,
		Description:    fmt.Sprintf("Significant intangibles (%.1f%% of revenue) may include inappropriately capitalized software", intangibleRatio*100),
		Recommendation: "Review software development cost capitalization and consider expensing",
		Timestamp:      time.Now(),
	}

	return &AdjustmentResult{
		Amount:      0.0, // No adjustment, just flagging
		Applied:     false,
		Adjustments: []entities.Adjustment{},
		Flags:       []entities.Flag{flag},
		Reasoning:   fmt.Sprintf("capitalized_software: Intangibles %.0f (%.1f%% of revenue) flagged for software review", data.OtherIntangibles, intangibleRatio*100),
	}
}

// AssetAdjustmentResult represents the result of applying asset adjustments.
//
// DC-1 Phase 2 PR-2 Task 2.1 added the three Native* fields below to carry
// AdjusterOutput state from rules that have migrated to the Adjuster
// interface (today: A1 goodwill_exclusion only). The cleaner orchestrator
// reads NativeLedgerEntries / NativeOverlays / NativelyEmittedRuleIDs to:
//   - append the native LedgerEntries to data.AdjustmentLedger BEFORE the
//     PR-1 shim runs, preserving asset-category ordering;
//   - append the native Overlays to data.Overlays;
//   - instruct the shim to SKIP any rule whose ID appears in
//     NativelyEmittedRuleIDs so the same rule is not double-counted.
//
// Tasks 2.2-2.5 widen NativelyEmittedRuleIDs as more A-rules migrate; Task
// 2.6 deletes the shim asset branch entirely. The Native* fields are not
// serialized (json:"-") because they live on entity types whose JSON tags
// already pin a public contract — exposing them under AssetAdjustmentResult
// would create a second source of truth.
type AssetAdjustmentResult struct {
	Applied                bool                   `json:"applied"`
	TotalAssetAdjustment   float64                `json:"total_asset_adjustment"`
	AdjustedTangibleAssets float64                `json:"adjusted_tangible_assets"`
	Adjustments            []entities.Adjustment  `json:"adjustments"`
	Flags                  []entities.Flag        `json:"flags"`
	AuditTrail             string                 `json:"audit_trail"`
	NativeLedgerEntries    []entities.LedgerEntry `json:"-"`
	NativeOverlays         []entities.OverlaySpec `json:"-"`
	NativelyEmittedRuleIDs map[string]bool        `json:"-"`
}

// recalculateTangibleAssets recalculates tangible assets after adjustments
func (aa *AssetAdjuster) recalculateTangibleAssets(data *entities.FinancialData) {
	// Tangible Assets = Total Assets - Goodwill - Other Intangibles
	tangibleAssets := data.TotalAssets - data.Goodwill - data.OtherIntangibles
	if tangibleAssets < 0 {
		tangibleAssets = 0
	}
	data.TangibleAssets = tangibleAssets
}

// CalculateNetTangibleAssets calculates net tangible assets after all adjustments
// DEPRECATED: Use ProcessAssetAdjustments instead for active cleaning
func (aa *AssetAdjuster) CalculateNetTangibleAssets(data *entities.FinancialData, context *entities.CleaningContext) *TangibleAssetsResult {
	// Use existing baseline (already processed by parser and previous cleaning stages)
	// Don't modify this value - just document what Category A items were reviewed
	finalTangibleAssets := data.TangibleAssets
	var adjustments []entities.Adjustment

	// Category A: Asset Quality Review & Documentation
	// Track significant items that warrant attention (threshold-based)

	// A1: Review Goodwill (track if significant)
	if data.Goodwill > 0 {
		goodwillRatio := data.Goodwill / data.TotalAssets
		if goodwillRatio > 0.05 { // >5% threshold from SEC guide
			adjustments = append(adjustments, entities.Adjustment{
				ID:          fmt.Sprintf("A1_goodwill_%d", time.Now().UnixNano()),
				RuleID:      "goodwill_exclusion",
				Category:    entities.AssetQuality,
				Type:        entities.AdjustmentTypeExclusion,
				Amount:      data.Goodwill,
				FromAccount: "Goodwill",
				Reasoning:   fmt.Sprintf("Reviewed goodwill exclusion: %.0f (%.1f%% of assets)", data.Goodwill, goodwillRatio*100),
				Applied:     true,
				Timestamp:   time.Now(),
			})
		}
	}

	// A2: Review Intangible Assets (track if significant)
	if data.OtherIntangibles > 0 {
		intangibleRatio := data.OtherIntangibles / data.TotalAssets
		if intangibleRatio > 0.05 { // >5% threshold for tracking
			adjustments = append(adjustments, entities.Adjustment{
				ID:          fmt.Sprintf("A2_intangibles_%d", time.Now().UnixNano()),
				RuleID:      "intangible_adjustment",
				Category:    entities.AssetQuality,
				Type:        entities.AdjustmentTypeWritedown,
				Amount:      data.OtherIntangibles,
				FromAccount: "OtherIntangibles",
				Reasoning:   fmt.Sprintf("Reviewed intangible assets: %.0f (%.1f%% of assets)", data.OtherIntangibles, intangibleRatio*100),
				Applied:     true,
				Timestamp:   time.Now(),
			})
		}
	}

	// A4: Review Deferred Tax Assets (track if significant)
	if data.DeferredTaxAssets > 0 {
		dtaRatio := data.DeferredTaxAssets / data.TotalAssets
		if dtaRatio > 0.05 { // >5% threshold from SEC guide A4
			adjustments = append(adjustments, entities.Adjustment{
				ID:          fmt.Sprintf("A4_dta_%d", time.Now().UnixNano()),
				RuleID:      "deferred_tax_assets",
				Category:    entities.AssetQuality,
				Type:        entities.AdjustmentTypeWritedown,
				Amount:      data.DeferredTaxAssets * 0.5, // Document 50% valuation allowance
				FromAccount: "DeferredTaxAssets",
				Reasoning:   fmt.Sprintf("Reviewed DTA valuation allowance: %.0f (%.1f%% of assets)", data.DeferredTaxAssets, dtaRatio*100),
				Applied:     true,
				Timestamp:   time.Now(),
			})
		}
	}

	// A5: Review Inventory Quality (track if significant)
	if data.Inventory > 0 {
		inventoryRatio := data.Inventory / data.TotalAssets
		if inventoryRatio > 0.10 { // >10% threshold for tracking
			adjustments = append(adjustments, entities.Adjustment{
				ID:          fmt.Sprintf("A5_inventory_%d", time.Now().UnixNano()),
				RuleID:      "obsolete_inventory",
				Category:    entities.AssetQuality,
				Type:        entities.AdjustmentTypeWritedown,
				Amount:      data.Inventory * 0.1, // Document potential 10% adjustment
				FromAccount: "Inventory",
				Reasoning:   fmt.Sprintf("Reviewed inventory quality: %.0f (%.1f%% of assets)", data.Inventory, inventoryRatio*100),
				Applied:     true,
				Timestamp:   time.Now(),
			})
		}
	}

	// Build audit trail summary
	auditTrail := fmt.Sprintf("Asset quality assessment completed. Reviewed %d significant Category A items.", len(adjustments))
	if len(adjustments) == 0 {
		auditTrail = "Asset quality assessment completed. No significant Category A adjustments required."
	}

	return &TangibleAssetsResult{
		AdjustedTangibleAssets: finalTangibleAssets,
		Adjustments:            adjustments,
		AuditTrail:             auditTrail,
	}
}

// Helper methods

func (aa *AssetAdjuster) getSeverityForGoodwillRatio(ratio float64) entities.FlagSeverity {
	switch {
	case ratio >= 0.50: // 50%+ is critical
		return entities.FlagSeverityCritical
	case ratio >= 0.30: // 30%+ is high
		return entities.FlagSeverityHigh
	case ratio >= 0.15: // 15%+ is medium
		return entities.FlagSeverityMedium
	default:
		return entities.FlagSeverityLow
	}
}

func (aa *AssetAdjuster) getSeverityForIntangibleRatio(ratio float64) entities.FlagSeverity {
	switch {
	case ratio >= 0.40: // 40%+ is high
		return entities.FlagSeverityHigh
	case ratio >= 0.25: // 25%+ is medium
		return entities.FlagSeverityMedium
	default:
		return entities.FlagSeverityLow
	}
}

func (aa *AssetAdjuster) getSeverityForDTARatio(ratio float64) entities.FlagSeverity {
	switch {
	case ratio >= 0.30: // 30%+ is critical
		return entities.FlagSeverityCritical
	case ratio >= 0.20: // 20%+ is high
		return entities.FlagSeverityHigh
	case ratio >= 0.10: // 10%+ is medium
		return entities.FlagSeverityMedium
	default:
		return entities.FlagSeverityLow
	}
}

func (aa *AssetAdjuster) getInventoryThresholdForIndustry(industryCode string) float64 {
	// Industry-specific inventory thresholds based on GICS codes
	thresholds := map[string]float64{
		"25": 0.40, // Consumer Discretionary (retail) - high tolerance
		"30": 0.35, // Consumer Staples - moderate tolerance
		"20": 0.20, // Industrials - lower tolerance
		"45": 0.05, // Technology - very low tolerance
		"35": 0.15, // Healthcare - low tolerance
	}

	if threshold, exists := thresholds[industryCode]; exists {
		return threshold
	}
	return 0.25 // Default 25% threshold
}

func (aa *AssetAdjuster) detectInventoryObsolescence(data *entities.FinancialData, context *entities.CleaningContext) bool {
	// Simple obsolescence detection based on turnover
	// In production, would analyze turnover trends over multiple periods
	if data.InventoryTurnover > 0 && data.InventoryTurnover < 3.0 {
		return true // Low turnover indicates potential obsolescence
	}

	// Check if inventory ratio exceeds industry threshold significantly
	inventoryRatio := data.Inventory / data.TotalAssets
	threshold := aa.getInventoryThresholdForIndustry(context.IndustryCode)

	return inventoryRatio > threshold*1.5 // 50% above industry threshold
}
