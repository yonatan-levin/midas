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
// Role classification (plan §3.5 / §4 row A5): Restater + TaxShieldDTA. A5 was
// the FIRST PR-2 adjuster to populate LedgerEntry.TaxShieldDTA — the 40%
// inventory writedown generates a derived deferred-tax-asset shield equal to
// writedownAmount * working.EffectiveTaxRate (when the rate is > 0). DC-1
// Phase 3 Task 3.7 extends the same pattern to A2 (intangible writedowns also
// generate a tax shield). A1 (OverlayEmitter) and A4 (which IS the DTA
// reduction itself) intentionally still leave TaxShieldDTA at zero. See
// ApplyA5InventoryWritedown godoc for the full TaxShieldDTA rationale.
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
// EquityOffset:-writedownAmount, TaxShieldDTA:writedownAmount*EffectiveTaxRate
// (when ETR > 0; zero otherwise). No OverlaySpec — the
// writedown is a direct component reduction, not an analytical overlay.
//
// Q2 resolution (DC-1 Phase 3 Task 3.7, spec §5.1): TaxShieldDTA is now
// populated as writedownAmount × working.EffectiveTaxRate when ETR > 0,
// mirroring A5's pattern. Phase 3's Restated() accessor adds TaxShieldDTA
// to DeferredTaxAssets so the restated view reflects the real economic
// position post-impairment. The dispatcher dual-write still mutates only
// data.OtherIntangibles (not data.DeferredTaxAssets), so legacy consumers
// reading data.DeferredTaxAssets directly see no change — only Restated()
// consumers see the tax shield. Phase 4 migrates the consumers.
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

	// Q2 resolution (DC-1 Phase 3 Task 3.7, spec §5.1): an intangible
	// writedown generates a deferred-tax-asset shield equal to
	// writedown × EffectiveTaxRate (IRC §197 / equivalent IFRS treatment).
	// Only populated when EffectiveTaxRate > 0 — foreign filers without
	// tax-rate data or zero-rate jurisdictions stay at the zero default,
	// matching A5's convention.
	var taxShieldDTA float64
	if working.EffectiveTaxRate > 0 {
		taxShieldDTA = writedownAmount * working.EffectiveTaxRate
	}

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
			TaxShieldDTA: taxShieldDTA,
			// DC-1 P5-followup §4.2: capture pre-state OtherIntangibles
			// so the LedgerEntry → Adjustment projection (P5-C3-full /
			// A4 below) can recompute Percentage = writedown / original
			// × 100 without dispatcher-side capture. Replaces the legacy
			// `originalIntangibles` thread into c2AdjusterOutputToLegacyResult.
			SkipMetrics: map[string]float64{"original_OtherIntangibles": originalIntangibles},
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
// tax purposes once realized. A5 was the FIRST PR-2 adjuster to populate
// TaxShieldDTA; DC-1 Phase 3 Task 3.7 extended the same pattern to A2.
// A1 (OverlayEmitter — leaves all monetary fields on OverlaySpec) and A4
// (which IS the DTA reduction itself, so a separate shield would
// double-count) intentionally still leave TaxShieldDTA at zero.
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
func (aa *AssetAdjuster) ProcessAssetAdjustments(ctx context.Context, data *entities.FinancialData, rules []*entities.CleaningRule, cleaningCtx *entities.CleaningContext) *AssetAdjustmentResult {
	var allFlags []entities.Flag

	// Phase 2 PR-2 native emissions — collected here in rule-iteration
	// order so the orchestrator can append them to data.AdjustmentLedger in
	// position. The set NativelyEmittedRuleIDs records which rules emitted
	// natively (consumed by per-rule contract tests; the orchestrator drains
	// NativeLedgerEntries / NativeOverlays directly).
	var nativeLedger []entities.LedgerEntry
	var nativeOverlays []entities.OverlaySpec
	nativelyEmittedRuleIDs := make(map[string]bool, len(rules))

	// DC-1 Phase 3 (Task 3.9): ctx is now threaded through the public
	// signature from service.go::applyActiveAdjustments. The Apply
	// methods accept it as their first parameter per Adjuster interface
	// convention. Phase 4+ may attach OTel spans or read deadlines from
	// it; today the consumers still treat it as opaque.
	applyCtx := ctx

	// Process each Category A rule
	for _, rule := range rules {
		if rule.Category != entities.AssetQuality || !rule.Enabled {
			continue
		}

		// armOut captures the AdjusterOutput the rule's arm produced so the
		// post-switch tangible-asset recompute can read it. DC-1 Phase 5
		// P5-C4: the legacy *AdjustmentResult translator chain was deleted;
		// the recompute trigger is now derived from the native emission
		// (a fired writedown LedgerEntry, or a positive goodwill overlay)
		// rather than from the translated result.Applied/.Amount.
		var armOut AdjusterOutput

		switch rule.ID {
		case "goodwill_exclusion":
			// DC-1 Phase 2 PR-2 Task 2.1: route A1 through the new
			// Adjuster-shaped ApplyA1Goodwill. ApplyA1Goodwill does NOT
			// mutate data — it emits a Fired LedgerEntry (empty Component) plus
			// an OverlaySpec (Field:"TotalAssets") carrying the goodwill amount.
			out, err := aa.ApplyA1Goodwill(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				// Adjuster.Apply errors are not a defined surface today;
				// ApplyA1Goodwill never returns one. Skip the rule on a
				// hypothetical future error (the deleted legacy fallback
				// would have bypassed the native path the orchestrator now
				// depends on exclusively).
				continue
			}
			armOut = out

			// DC-1 Phase 4 (C-4, §8.2.1 Option A): A1 is an OverlayEmitter — its
			// goodwill-exclusion effect is realized at the view level by
			// InvestedCapital() (subtracts the OverlaySpec amount from
			// TotalAssets, zeroes Goodwill per Damodaran). The legacy dispatcher
			// dual-write (data.Goodwill = 0; data.TotalAssets -= goodwill) is
			// DELETED. The generic helper skips A1's empty-Component LedgerEntry.
			// Net effect: Restated().TotalAssets stays goodwill-INCLUDED;
			// InvestedCapital().TotalAssets excludes it (consumed by the WACC /
			// bridge path). The cross-check reads Restated() (goodwill-included)
			// — Class IV drift for A1-firing tickers is expected per spec §5.4.
			applyLedgerComponentDeltas(applyCtx, data, out)

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
			out, err := aa.ApplyA2Intangible(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				// Adjuster.Apply errors are not a defined surface today;
				// ApplyA2Intangible never returns one. Skip on a
				// hypothetical future error (see A1 arm rationale).
				continue
			}
			armOut = out

			// DC-1 Phase 4 (C-2, §8.2.1 Option A): the dispatcher applies the
			// fired LedgerEntry's COMPONENT delta to data.OtherIntangibles
			// only. The legacy umbrella dual-write (data.TotalAssets -=
			// writedown) is DELETED — umbrellas recompute in
			// cleaneddata.Restated(). Consumers read Restated().OtherIntangibles.
			applyLedgerComponentDeltas(applyCtx, data, out)

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
			out, err := aa.ApplyA5InventoryWritedown(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				// Adjuster.Apply errors are not a defined surface today;
				// ApplyA5InventoryWritedown never returns one. Skip on a
				// hypothetical future error (see A1 arm rationale).
				continue
			}
			armOut = out

			// DC-1 Phase 4 (C-2, §8.2.1 Option A): apply the fired LedgerEntry's
			// COMPONENT delta to data.Inventory (and TaxShieldDTA →
			// data.DeferredTaxAssets is handled at the view level by
			// Restated()). The legacy umbrella dual-write (data.TotalAssets -=
			// writedown) is DELETED. Consumers read Restated().Inventory.
			applyLedgerComponentDeltas(applyCtx, data, out)

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
			out, err := aa.ApplyA4DTAValuationAllowance(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				// Adjuster.Apply errors are not a defined surface today;
				// ApplyA4DTAValuationAllowance never returns one. Skip on a
				// hypothetical future error (see A1 arm rationale).
				continue
			}
			armOut = out

			// DC-1 Phase 4 (C-2, §8.2.1 Option A): apply the fired LedgerEntry's
			// COMPONENT delta to data.DeferredTaxAssets only. The legacy
			// dual-writes to the umbrella (data.TotalAssets -= valuationAllowance)
			// AND to the auxiliary aggregate (data.ValuationAllowance +=
			// valuationAllowance) are DELETED. Neither is read by a Phase 4
			// consumer: TotalAssets recomputes in Restated(); ValuationAllowance
			// is not a view field (the audit trail is preserved via the
			// translated *AdjustmentResult + the native LedgerEntry). Consumers
			// read Restated().DeferredTaxAssets.
			applyLedgerComponentDeltas(applyCtx, data, out)

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
			// the native* slices so the cleaner orchestrator picks them up.
			// NO dual-write here: this review never mutates the balance sheet
			// on any path (see ApplyARDCapitalizationReview godoc for the
			// FlagEmitter convention) — armOut stays zero so no recompute fires.
			out, err := aa.ApplyARDCapitalizationReview(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				// Adjuster.Apply errors are not a defined surface today;
				// ApplyARDCapitalizationReview never returns one. Skip on a
				// hypothetical future error (see A1 arm rationale).
				continue
			}

			// Record native emissions for the orchestrator. Both the fired
			// path (Flags non-empty) and skip paths (Flags empty) emit a
			// Fired:false LedgerEntry that is load-bearing for "did the
			// cleaner consider R&D capitalization for this ticker?". Flags are
			// drained into allFlags below.
			allFlags = append(allFlags, out.Flags...)
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
			continue
		case "capitalized_software":
			// DC-1 Phase 2 PR-2 Task 2.5: route the capitalized-software
			// review through ApplyACapitalizedSoftwareReview. Same dispatcher
			// shape as the R&D review above — FlagEmitter convention, NO
			// dual-write since this review never mutates the balance sheet.
			out, err := aa.ApplyACapitalizedSoftwareReview(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				continue
			}

			allFlags = append(allFlags, out.Flags...)
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
			continue
		default:
			continue // Skip unknown rules
		}

		// Drain the arm's flags. DC-1 Phase 5 P5-C4: the legacy
		// *AdjustmentResult accumulation was deleted; the orchestrator
		// projects the public audit trail from data.AdjustmentLedger. The
		// per-arm blocks above already drained armOut's LedgerEntries /
		// Overlays into the native slices in rule-iteration order.
		allFlags = append(allFlags, armOut.Flags...)

		// Recalculate tangible assets after each asset adjustment that
		// modified an asset component. Native trigger (replaces the legacy
		// `result.Applied && result.Amount > 0`): a fired Restater LedgerEntry
		// with a writedown (DeltaAmount < 0 → A2/A4/A5) OR a positive
		// goodwill-exclusion overlay (A1). FlagEmitters (RD/CapSW) return
		// early above and never reach here.
		if assetArmTriggersTangibleRecompute(armOut) {
			aa.recalculateTangibleAssets(data)
		}
	}

	return &AssetAdjustmentResult{
		Flags:                  allFlags,
		NativeLedgerEntries:    nativeLedger,
		NativeOverlays:         nativeOverlays,
		NativelyEmittedRuleIDs: nativelyEmittedRuleIDs,
	}
}

// assetArmTriggersTangibleRecompute reports whether an asset rule's
// AdjusterOutput warrants a tangible-asset recompute. DC-1 Phase 5 P5-C4: this
// replaces the legacy `result.Applied && result.Amount > 0` predicate that the
// deleted translator chain produced.
//
// Behaviorally identical to the legacy predicate across all six asset rules:
//   - A2/A4/A5 (Restaters): legacy Amount = abs(DeltaAmount) > 0 on a fired
//     component restate. A fired LedgerEntry with a non-empty Component and a
//     non-zero DeltaAmount triggers. (All asset Restaters write NEGATIVE deltas
//     today; the `!= 0` guard — rather than `< 0` — future-proofs against a
//     positive-delta asset Restater that legacy `Amount > 0` would also have
//     triggered, removing a one-directional sign fragility.)
//   - A1 (OverlayEmitter): legacy Amount = overlay.Amount (>0 when fired). A1
//     goodwill exclusion is the ONLY asset-side overlay, so the trigger is
//     scoped to its OverlayID rather than any positive overlay amount.
//   - A-RD / A-CapSoftware (FlagEmitters): legacy Applied=false always; they
//     emit no component delta and no overlay, so this returns false. (They also
//     return early in the dispatcher and never reach this call.)
func assetArmTriggersTangibleRecompute(out AdjusterOutput) bool {
	for _, entry := range out.LedgerEntries {
		if entry.Fired && entry.Component != "" && entry.DeltaAmount != 0 {
			return true
		}
	}
	for _, overlay := range out.Overlays {
		if overlay.OverlayID == adjusterIDA1GoodwillExclusion && overlay.Amount > 0 {
			return true
		}
	}
	return false
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

// AssetAdjustmentResult is the slim native carrier returned by
// ProcessAssetAdjustments.
//
// DC-1 Phase 5 P5-C4 deleted the legacy translator stack and the
// translator-fed fields (Applied / TotalAssetAdjustment /
// AdjustedTangibleAssets / Adjustments / AuditTrail). The cleaner orchestrator
// (datacleaner/service.go::applyActiveAdjustments) consumes ONLY the native
// emissions: it drains NativeLedgerEntries onto data.AdjustmentLedger and
// NativeOverlays onto data.Overlays (preserving asset-category ordering),
// derives the firing signal via nativeFired(...), and projects the public
// entities.Adjustment audit trail from the ledger via adjustmentsFromLedger.
// Flags carries the category's collected risk flags.
//
// The native slices are not serialized (json:"-") because they live on entity
// types whose JSON tags already pin a public contract.
type AssetAdjustmentResult struct {
	Flags                  []entities.Flag        `json:"flags"`
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
