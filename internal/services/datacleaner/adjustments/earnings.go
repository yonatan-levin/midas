package adjustments

import (
	"context"
	"fmt"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// EarningsAdjuster handles Category C adjustments from SEC cleaning guide
// Implements earnings distortion removal and normalization
type EarningsAdjuster struct {
}

// NewEarningsAdjuster creates a new earnings adjuster instance
func NewEarningsAdjuster() *EarningsAdjuster {
	return &EarningsAdjuster{}
}

// AdjusterID constants identify each Category C adjuster on LedgerEntry
// records. They MUST be stable across builds — Phase 3's view reconstruction
// joins on these IDs. Keep the trailing "_<descriptor>" suffixes in sync with
// the legacy rule.ID values where possible so log greps continue to work
// across the migration. Mirrors the assets.go convention shipped in PR-2.
const (
	adjusterIDC1RestructuringCharges  = "C1_restructuring_charges"
	adjusterIDC2AssetSaleGains        = "C2_asset_sale_gains"
	adjusterIDC3LitigationSettlements = "C3_litigation_settlements"
	adjusterIDC4StockCompensation     = "C4_stock_compensation"
	adjusterIDC5DerivativeGainsLosses = "C5_derivative_gains_losses"
	adjusterIDC6CapitalizedInterest   = "C6_capitalized_interest"
	adjusterIDC7WorkingCapital        = "C7_working_capital"
)

// EarningsAdjustmentResult is the slim native carrier returned by
// ProcessEarningsAdjustments.
//
// DC-1 Phase 5 P5-C4 deleted the legacy translator stack and the
// translator-fed fields (Amount / Applied / Adjustments / Reasoning). The
// cleaner orchestrator consumes ONLY the native emissions: it drains
// NativeLedgerEntries onto data.AdjustmentLedger and NativeOverlays onto
// data.Overlays (preserving earnings-category ordering), derives the firing
// signal via nativeFired(...), and projects the public entities.Adjustment
// audit trail from the ledger via adjustmentsFromLedger. Flags carries the
// category's collected risk flags.
//
// Mirrors the slimmed AssetAdjustmentResult / LiabilityAdjustmentResult.
type EarningsAdjustmentResult struct {
	Flags                  []entities.Flag        `json:"flags"`
	NativeLedgerEntries    []entities.LedgerEntry `json:"-"`
	NativeOverlays         []entities.OverlaySpec `json:"-"`
	NativelyEmittedRuleIDs map[string]bool        `json:"-"`
}

// SR-1 A3: the per-rule Adjuster adapter structs were deleted — production
// dispatches via the Apply* methods directly; the Adjuster interface had no
// production consumer.

// ApplyC1Restructuring is the Adjuster-shaped (DC-1 Phase 2 PR-3 Task 3.1)
// implementation of the C1 restructuring-charges add-back rule. Like the
// asset-side Restaters (ApplyA2Intangible / ApplyA4DTAValuationAllowance), it
// is MUTATION-FREE — it reads `working` and returns an AdjusterOutput
// describing the add-back's intent (Restater-shaped LedgerEntry on the
// NormalizedOperatingIncome component) but does NOT modify
// `working.NormalizedOperatingIncome`. The dispatcher in
// ProcessEarningsAdjustments performs the dual-write mutation centrally.
//
// Role classification (plan §3.5 / §4 row C1): Restater. The fired LedgerEntry
// carries Component:"NormalizedOperatingIncome", DeltaAmount:+restructuringAmount
// (POSITIVE — this is an add-back, not a writedown), EquityOffset:+restructuringAmount,
// TaxShieldDTA:0. No OverlaySpec — restructuring add-back is a direct
// component restate, not an analytical overlay.
//
// Q2 resolution (plan §10): TaxShieldDTA is set to 0 in Phase 2 to preserve
// the dual-write bit-for-bit contract. Today's C1 legacy code does not compute
// a tax shield; populating it here would diverge from legacy outputs. Phase 3
// revisits TaxShieldDTA when consumers actually read it.
//
// Skipped paths emit Fired:false LedgerEntries so observability can answer
// "why didn't C1 fire on this ticker?" without code reading. The threshold-
// failed path carries SkipMetrics{restructuring_ratio, threshold} for
// dashboards. The no-revenue and no-restructuring-charges paths emit
// SkipReason without SkipMetrics (no ratio to chart).
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md §"Adjuster output"
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md §3.5 / §4 row C1 / §7 Task 3.1 / §10 Q2
func (ea *EarningsAdjuster) ApplyC1Restructuring(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	// ctx + cleaningCtx accepted for interface symmetry; C1 itself uses neither.
	_ = ctx
	_ = cleaningCtx

	now := time.Now()

	// Skip path 1: no revenue data — cannot compute materiality ratio. Mirrors
	// the legacy ProcessRestructuringChargesAdjustment guard. No SkipMetrics
	// because the ratio's denominator is zero.
	if working.Revenue <= 0 {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:  now,
				AdjusterID: adjusterIDC1RestructuringCharges,
				RuleID:     rule.ID,
				Fired:      false,
				Reasoning:  "Insufficient revenue data to calculate restructuring charges",
				SkipReason: "Insufficient revenue data to calculate restructuring charges",
			}},
		}, nil
	}

	// Match legacy behavior: when actual RestructuringCharges is missing or
	// non-positive, estimate at 1.5% of revenue (conservative). Below-threshold
	// skip still uses the estimated amount when nothing was reported.
	restructuringAmount := working.RestructuringCharges
	if restructuringAmount <= 0 {
		restructuringAmount = working.Revenue * 0.015
	}

	restructuringRatio := restructuringAmount / working.Revenue
	threshold := 0.02 // Default 2% materiality threshold (legacy parity).
	if rule.Threshold != nil && rule.Threshold.PercentageOfRevenue != nil {
		threshold = *rule.Threshold.PercentageOfRevenue
	}

	// Skip path 2: ratio below materiality threshold. Carry ratio + threshold
	// as SkipMetrics so downstream dashboards can chart "how close was C1 to
	// firing on this ticker?".
	if restructuringRatio < threshold {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:   now,
				AdjusterID:  adjusterIDC1RestructuringCharges,
				RuleID:      rule.ID,
				Fired:       false,
				Reasoning:   fmt.Sprintf("Restructuring charges below materiality threshold (%.1f%% < %.1f%%)", restructuringRatio*100, threshold*100),
				SkipReason:  fmt.Sprintf("Restructuring charges below materiality threshold (%.1f%% < %.1f%%)", restructuringRatio*100, threshold*100),
				SkipMetrics: map[string]float64{"restructuring_ratio": restructuringRatio, "threshold": threshold},
			}},
		}, nil
	}

	// Fired path: emit a Restater-shaped Fired:true LedgerEntry on the
	// NormalizedOperatingIncome component. DeltaAmount is POSITIVE because C1
	// is an add-back (the legacy code does `data.NormalizedOperatingIncome +=
	// restructuringAmount`). EquityOffset mirrors DeltaAmount — add-backs
	// increase normalized earnings, which flow to retained earnings 1:1.
	return AdjusterOutput{
		LedgerEntries: []entities.LedgerEntry{{
			Timestamp:    now,
			AdjusterID:   adjusterIDC1RestructuringCharges,
			RuleID:       rule.ID,
			Fired:        true,
			Reasoning:    fmt.Sprintf("Restructuring charges adjustment: Excluded $%.1fM (%.1f%% of revenue) from normalized operating income", restructuringAmount/1000000, restructuringRatio*100),
			Component:    "NormalizedOperatingIncome",
			DeltaAmount:  restructuringAmount,
			EquityOffset: restructuringAmount,
			TaxShieldDTA: 0, // Q2 deferral (plan §10): C1 does not compute tax shield in Phase 2.
		}},
	}, nil
}

// ApplyC2AssetSaleGains is the Adjuster-shaped (DC-1 Phase 2 PR-3 Task 3.2)
// implementation of the C2 asset-sale-gains subtraction rule. Like
// ApplyC1Restructuring, it is MUTATION-FREE — it reads `working` and returns
// an AdjusterOutput describing the subtraction's intent (Restater-shaped
// LedgerEntry on the NormalizedOperatingIncome component) but does NOT modify
// `working.NormalizedOperatingIncome`. The dispatcher in
// ProcessEarningsAdjustments performs the dual-write mutation centrally.
//
// Role classification (plan §3.5 / §4 row C2): Restater. The fired LedgerEntry
// carries Component:"NormalizedOperatingIncome", DeltaAmount:-AssetSaleGains
// (NEGATIVE — C2 subtracts non-core gains from operating income, the opposite
// sign of C1's add-back), EquityOffset:-AssetSaleGains, TaxShieldDTA:0. No
// OverlaySpec — gain subtraction is a direct component restate, not an
// analytical overlay.
//
// Q2 resolution (plan §10): TaxShieldDTA stays 0 in Phase 2 — legacy C2 does
// not compute tax shield.
//
// Skip paths: only one skip path on the legacy code — no asset-sale gains
// present. Emits SkipReason without SkipMetrics (no ratio to chart when the
// numerator is zero).
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md §"Adjuster output"
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md §3.5 / §4 row C2 / §7 Task 3.2 / §10 Q2
func (ea *EarningsAdjuster) ApplyC2AssetSaleGains(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	// ctx + cleaningCtx accepted for interface symmetry; C2 itself uses neither.
	_ = ctx
	_ = cleaningCtx

	now := time.Now()

	// Skip path: no asset-sale gains to subtract. Mirrors the legacy
	// ProcessAssetSaleGainsAdjustment guard.
	if working.AssetSaleGains <= 0 {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:  now,
				AdjusterID: adjusterIDC2AssetSaleGains,
				RuleID:     rule.ID,
				Fired:      false,
				Reasoning:  "No asset sale gains to adjust",
				SkipReason: "No asset sale gains to adjust",
			}},
		}, nil
	}

	gains := working.AssetSaleGains
	// Legacy code re-derives Revenue ratio inline for the Adjustment.Percentage
	// field on the fired branch. We replay the same formatting on the
	// LedgerEntry Reasoning for byte-identical legacy parity.
	var revenueRatio float64
	if working.Revenue > 0 {
		revenueRatio = gains / working.Revenue
	}
	_ = revenueRatio // captured for symmetry; the legacy reasoning string does not include the ratio.

	return AdjusterOutput{
		LedgerEntries: []entities.LedgerEntry{{
			Timestamp:    now,
			AdjusterID:   adjusterIDC2AssetSaleGains,
			RuleID:       rule.ID,
			Fired:        true,
			Reasoning:    fmt.Sprintf("Asset sale gains adjustment: Excluded $%.1fM from normalized operating income", gains/1000000),
			Component:    "NormalizedOperatingIncome",
			DeltaAmount:  -gains,
			EquityOffset: -gains,
			TaxShieldDTA: 0, // Q2 deferral (plan §10): C2 does not compute tax shield in Phase 2.
			// DC-1 P5-followup §4.2: capture pre-state Revenue so the
			// LedgerEntry → Adjustment projection (P5-C3-full / A4
			// below) can recompute Percentage = gains / originalRevenue
			// × 100 without dispatcher-side capture. Same convention
			// as A2/C3/C4/C5/C6.
			SkipMetrics: map[string]float64{"original_Revenue": working.Revenue},
		}},
	}, nil
}

// ApplyC3Litigation is the Adjuster-shaped (DC-1 Phase 2 PR-3 Task 3.3)
// implementation of the C3 litigation-settlements add-back rule. Mirrors
// ApplyC1Restructuring: it is MUTATION-FREE, the dispatcher performs the
// dual-write `data.NormalizedOperatingIncome += LitigationSettlements`
// AFTER Apply.
//
// Role classification (plan §3.5 / §4 row C3): Restater. The fired LedgerEntry
// carries Component:"NormalizedOperatingIncome",
// DeltaAmount:+LitigationSettlements (POSITIVE — add-back, same sign as C1),
// EquityOffset:+LitigationSettlements, TaxShieldDTA:0. No OverlaySpec.
//
// Skip paths: no-litigation (no SkipMetrics) + below-threshold (1% of revenue
// default; carries SkipMetrics{litigation_ratio, threshold}).
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md §"Adjuster output"
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md §3.5 / §4 row C3 / §7 Task 3.3 / §10 Q2
func (ea *EarningsAdjuster) ApplyC3Litigation(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	_ = ctx
	_ = cleaningCtx

	now := time.Now()

	// Skip path 1: no litigation settlements present.
	if working.LitigationSettlements <= 0 {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:  now,
				AdjusterID: adjusterIDC3LitigationSettlements,
				RuleID:     rule.ID,
				Fired:      false,
				Reasoning:  "No litigation settlements to adjust",
				SkipReason: "No litigation settlements to adjust",
			}},
		}, nil
	}

	// C3 legacy code divides by working.Revenue without a Revenue<=0 guard.
	// To preserve byte-identical legacy behavior, replay the same arithmetic
	// here. (Reviewer note: a Revenue=0 ticker with positive
	// LitigationSettlements would have produced +Inf ratio in the legacy
	// code too — this is a pre-existing data-quality concern, not a
	// regression introduced by the migration.)
	settlements := working.LitigationSettlements
	litigationRatio := settlements / working.Revenue
	threshold := 0.01 // Default 1% materiality threshold (legacy parity).
	if rule.Threshold != nil && rule.Threshold.PercentageOfRevenue != nil {
		threshold = *rule.Threshold.PercentageOfRevenue
	}

	// Skip path 2: ratio below materiality threshold.
	if litigationRatio < threshold {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:   now,
				AdjusterID:  adjusterIDC3LitigationSettlements,
				RuleID:      rule.ID,
				Fired:       false,
				Reasoning:   fmt.Sprintf("Litigation settlements below materiality threshold (%.1f%% < %.1f%%)", litigationRatio*100, threshold*100),
				SkipReason:  fmt.Sprintf("Litigation settlements below materiality threshold (%.1f%% < %.1f%%)", litigationRatio*100, threshold*100),
				SkipMetrics: map[string]float64{"litigation_ratio": litigationRatio, "threshold": threshold},
			}},
		}, nil
	}

	// Fired path: add-back. Legacy code:
	//   data.NormalizedOperatingIncome += data.LitigationSettlements
	return AdjusterOutput{
		LedgerEntries: []entities.LedgerEntry{{
			Timestamp:    now,
			AdjusterID:   adjusterIDC3LitigationSettlements,
			RuleID:       rule.ID,
			Fired:        true,
			Reasoning:    fmt.Sprintf("Litigation settlements adjustment: Excluded $%.1fM (%.1f%% of revenue) from normalized operating income", settlements/1000000, litigationRatio*100),
			Component:    "NormalizedOperatingIncome",
			DeltaAmount:  settlements,
			EquityOffset: settlements,
			TaxShieldDTA: 0, // Q2 deferral (plan §10).
			// DC-1 P5-followup §4.2: capture pre-state Revenue for the
			// LedgerEntry → Adjustment projection (P5-C3-full / A4).
			SkipMetrics: map[string]float64{"original_Revenue": working.Revenue},
		}},
	}, nil
}

// ApplyC5DerivativeGainsLosses is the Adjuster-shaped (DC-1 Phase 2 PR-3
// Task 3.5) implementation of the C5 derivative-mark normalization rule. Like
// the other C-rules, it is MUTATION-FREE; the dispatcher in
// ProcessEarningsAdjustments performs the dual-write
// `data.NormalizedOperatingIncome -= rawAmount` AFTER Apply.
//
// Role classification (plan §3.5 / §4 row C5): Restater. Branch-divergent
// sign convention (load-bearing — read carefully):
//
//   - GAIN branch (working.DerivativeGainsLosses > 0): legacy subtracts the
//     positive gain from operating income. LedgerEntry DeltaAmount = -rawAmount
//     = NEGATIVE.
//   - LOSS branch (working.DerivativeGainsLosses < 0): legacy ALSO subtracts
//     (subtracting a negative = add-back). LedgerEntry DeltaAmount = -rawAmount
//     = POSITIVE.
//
// Net effect per fire: ONE mutation, ONE LedgerEntry. The legacy code at
// earnings.go:313 and :316 has two lines that LOOK duplicated but live in two
// branches; `adjustmentAmount` already carries the correct sign through both
// branches. We mirror that with `rawAmount := working.DerivativeGainsLosses`
// (signed) and DeltaAmount = -rawAmount.
//
// `reportingAmount` is the absolute magnitude (legacy line :317 flips sign on
// the loss branch); the legacy *AdjustmentResult Amount field uses this
// positive magnitude. The LedgerEntry preserves the SIGNED DeltaAmount because
// Phase 3's Restated() accessor will treat sign as load-bearing.
//
// Skip path: DerivativeGainsLosses == 0 — no mark to normalize.
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md §"Adjuster output"
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md §3.5 / §4 row C5 / §7 Task 3.5 / §10 Q2
func (ea *EarningsAdjuster) ApplyC5DerivativeGainsLosses(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	_ = ctx
	_ = cleaningCtx

	now := time.Now()

	// Skip path: no derivative gains/losses to normalize. Legacy code uses
	// `== 0` (not `<= 0`) here because C5 handles both signs of mark.
	if working.DerivativeGainsLosses == 0 {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:  now,
				AdjusterID: adjusterIDC5DerivativeGainsLosses,
				RuleID:     rule.ID,
				Fired:      false,
				Reasoning:  "No derivative gains/losses to adjust",
				SkipReason: "No derivative gains/losses to adjust",
			}},
		}, nil
	}

	rawAmount := working.DerivativeGainsLosses // signed: + for gain, - for loss
	// reportingAmount mirrors legacy line :317 — positive magnitude for the
	// *AdjustmentResult.Amount field. The signed delta lives on the LedgerEntry.
	reportingAmount := rawAmount
	if reportingAmount < 0 {
		reportingAmount = -reportingAmount
	}

	return AdjusterOutput{
		LedgerEntries: []entities.LedgerEntry{{
			Timestamp:  now,
			AdjusterID: adjusterIDC5DerivativeGainsLosses,
			RuleID:     rule.ID,
			Fired:      true,
			Reasoning:  fmt.Sprintf("Derivative gains/losses adjustment: Excluded $%.1fM from normalized operating income", reportingAmount/1000000),
			Component:  "NormalizedOperatingIncome",
			// DeltaAmount = -rawAmount: signed delta the dispatcher applies as
			// data.NormalizedOperatingIncome += DeltaAmount equivalently to the
			// legacy `data.NormalizedOperatingIncome -= rawAmount`.
			DeltaAmount:  -rawAmount,
			EquityOffset: -rawAmount,
			TaxShieldDTA: 0, // Q2 deferral (plan §10).
			// DC-1 P5-followup §4.2: capture pre-state Revenue for the
			// LedgerEntry → Adjustment projection (P5-C3-full / A4).
			SkipMetrics: map[string]float64{"original_Revenue": working.Revenue},
		}},
	}, nil
}

// ApplyC6CapitalizedInterest is the Adjuster-shaped (DC-1 Phase 2 PR-3
// Task 3.6) implementation of the C6 capitalized-interest reclassification
// rule. Like the other C-rules, it is MUTATION-FREE; the dispatcher in
// ProcessEarningsAdjustments performs the dual-write
// `data.InterestExpense += data.CapitalizedInterest` AFTER Apply.
//
// Role classification (plan §3.5 / §4 row C6): Restater, but with a
// LOAD-BEARING SPECIAL CASE — EquityOffset = 0. Reason: C6 is a
// reclassification BETWEEN income-statement line items (operating expense →
// interest expense), NOT a real economic event. The dollars do not flow to
// retained earnings; they shift between lines on the same statement.
// Phase 3's Restated() accessor MUST NOT add C6's DeltaAmount to retained
// earnings; the EquityOffset field is the load-bearing carrier of "does
// this flow through equity?" — and for C6 the answer is NO.
//
// The fired LedgerEntry carries:
//   - Component:"InterestExpense" (NOT NormalizedOperatingIncome — DIFFERENT
//     field! C6 targets the interest-expense line specifically).
//   - DeltaAmount: +CapitalizedInterest (POSITIVE — capitalized interest is
//     added BACK to interest expense to undo the PP&E capitalization).
//   - EquityOffset: 0 (LOAD-BEARING special case; see godoc above).
//   - TaxShieldDTA: 0 (Q2 deferral).
//
// Skip path: CapitalizedInterest <= 0 — no capitalization to reclassify.
// The legacy guard uses `<= 0` (not `== 0`) because capitalized interest
// is non-negative by accounting convention; a negative value would
// indicate a data-quality bug, and the legacy code treats it as "skip".
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md §"Adjuster output"
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md §3.5 / §4 row C6 / §7 Task 3.6 / §10 Q2
func (ea *EarningsAdjuster) ApplyC6CapitalizedInterest(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	_ = ctx
	_ = cleaningCtx

	now := time.Now()

	// Skip path: no capitalized interest to reclassify. Mirrors the legacy
	// ProcessCapitalizedInterestAdjustment guard (`<= 0`).
	if working.CapitalizedInterest <= 0 {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:  now,
				AdjusterID: adjusterIDC6CapitalizedInterest,
				RuleID:     rule.ID,
				Fired:      false,
				Reasoning:  "No capitalized interest to adjust",
				SkipReason: "No capitalized interest to adjust",
			}},
		}, nil
	}

	capInterest := working.CapitalizedInterest

	// Fired path. Legacy code:
	//   data.InterestExpense += data.CapitalizedInterest
	// The Restated() accessor MUST NOT add this DeltaAmount to retained
	// earnings — EquityOffset = 0 is the load-bearing carrier of that fact.
	return AdjusterOutput{
		LedgerEntries: []entities.LedgerEntry{{
			Timestamp:    now,
			AdjusterID:   adjusterIDC6CapitalizedInterest,
			RuleID:       rule.ID,
			Fired:        true,
			Reasoning:    fmt.Sprintf("Capitalized interest adjustment: Reclassified $%.1fM from PP&E to interest expense", capInterest/1000000),
			Component:    "InterestExpense", // DIFFERENT from NormalizedOperatingIncome — C6 targets interest expense.
			DeltaAmount:  capInterest,       // POSITIVE — add back to interest expense.
			EquityOffset: 0,                 // LOAD-BEARING: reclassification between IS lines, NOT an equity-flowing event.
			TaxShieldDTA: 0,                 // Q2 deferral (plan §10).
			// DC-1 P5-followup §4.2: capture pre-state Revenue for the
			// LedgerEntry → Adjustment projection (P5-C3-full / A4).
			SkipMetrics: map[string]float64{"original_Revenue": working.Revenue},
		}},
	}, nil
}

// ApplyC4StockCompensation is the Adjuster-shaped (DC-1 Phase 2 PR-3 Task 3.4)
// implementation of the C4 stock-based-compensation rule.
//
// PLAN-VS-CODE DISAGREEMENT (load-bearing — read carefully):
// The PR-3 implementation plan §7 Task 3.1-3.7 row described C4 as "same pattern
// as C1" (i.e. Restater). The actual legacy code at
// ProcessStockCompensationAdjustment (~line 1322) does NOT mutate the balance
// sheet — no `data.NormalizedOperatingIncome += X` or `-= X` happens. The
// legacy code emits an entities.Adjustment{Type:Reclassify} and a dilution
// entities.Flag, but the dual-write step is absent. Therefore C4 is a
// FlagEmitter (per PR-2 Task 2.5's convention), NOT a Restater. This deviation
// from the plan was pre-flagged in the PR-3 handoff doc's TL;DR.
//
// FlagEmitter convention reminder: every LedgerEntry stays Fired:false because
// no balance-sheet adjustment occurred. The populated AdjusterOutput.Flags
// slice IS the firing signal when the rule "fires" (in the legacy
// Applied:true sense). Component / DeltaAmount / EquityOffset / TaxShieldDTA
// are all zero.
//
// LedgerEntry shape across the three branches:
//   - No SBC (StockBasedCompensation <= 0): Fired:false, SkipReason
//     "No stock-based compensation to adjust", no SkipMetrics, Flags empty.
//   - Fired (any positive SBC — legacy code has NO threshold gate): Fired:false,
//     SkipReason "flag-only review; no balance-sheet adjustment",
//     SkipMetrics:{sbc_amount, sbc_ratio} (ratio undefined when Revenue<=0;
//     surfaced as 0 in that case), Reasoning carrying the legacy
//     "Stock-based compensation adjustment: ..." string, AdjusterOutput.Flags
//     carrying exactly one dilution flag of Type "earnings_quality".
//
// Skip-vs-fire decision matches legacy exactly: the only legacy guard is
// `StockBasedCompensation <= 0`. There is no materiality threshold — every
// positive SBC fires the dilution flag.
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md §"Adjuster output"
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md §3.5 / §4 row C4 / §7 Task 3.4 / §10 Q2 / TL;DR "plan-vs-code disagreement"
func (ea *EarningsAdjuster) ApplyC4StockCompensation(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	_ = ctx
	_ = cleaningCtx

	now := time.Now()

	// Skip path: no stock-based compensation to review. Mirrors the legacy
	// ProcessStockCompensationAdjustment guard.
	if working.StockBasedCompensation <= 0 {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:  now,
				AdjusterID: adjusterIDC4StockCompensation,
				RuleID:     rule.ID,
				Fired:      false,
				Reasoning:  "No stock-based compensation to adjust",
				SkipReason: "No stock-based compensation to adjust",
			}},
		}, nil
	}

	sbcAmount := working.StockBasedCompensation
	// sbcRatio is captured for SkipMetrics + the legacy Percentage / reasoning
	// formatting. Legacy code at lines 1343/1357/1364 divides by working.Revenue
	// without a Revenue<=0 guard (a Revenue<=0 ticker with positive SBC would
	// have produced +Inf in the legacy code too — pre-existing data-quality
	// concern, not a regression introduced by this migration).
	var sbcRatio float64
	if working.Revenue > 0 {
		sbcRatio = sbcAmount / working.Revenue
	}

	// Fired path: no balance-sheet mutation — FlagEmitter convention. The
	// AdjusterOutput.Flags slice carries the dilution flag (matching the
	// legacy entities.Flag emission).
	out := AdjusterOutput{
		LedgerEntries: []entities.LedgerEntry{{
			Timestamp:  now,
			AdjusterID: adjusterIDC4StockCompensation,
			RuleID:     rule.ID,
			Fired:      false,
			Reasoning:  fmt.Sprintf("Stock-based compensation adjustment: Reclassified $%.1fM (%.1f%% of revenue) for dilution analysis", sbcAmount/1000000, sbcRatio*100),
			SkipReason: "flag-only review; no balance-sheet adjustment",
			SkipMetrics: map[string]float64{
				"sbc_amount":       sbcAmount,
				"sbc_ratio":        sbcRatio,
				"original_Revenue": working.Revenue, // P5-followup §4.2 — pre-state for projection.
			},
		}},
	}

	// Dilution flag — mirror the legacy ProcessStockCompensationAdjustment
	// emission bit-for-bit (Type, Severity, Amount, Percentage, Description,
	// Recommendation).
	out.Flags = append(out.Flags, entities.Flag{
		ID:             fmt.Sprintf("stock_dilution_%d", now.UnixNano()),
		RuleID:         rule.ID,
		Type:           "earnings_quality",
		Severity:       rule.Severity,
		Amount:         sbcAmount,
		Percentage:     sbcRatio * 100,
		Description:    "High stock-based compensation may indicate dilution risk",
		Recommendation: "Consider dilution impact in per-share calculations",
		Timestamp:      now,
	})

	return out, nil
}

// ApplyC7WorkingCapital is the Adjuster-shaped (DC-1 Phase 2 PR-3 Task 3.7)
// implementation of the C7 working-capital window-dressing review.
//
// Role classification (plan §3.5 / §4 row C7): FlagEmitter — unambiguous. The
// legacy ProcessWorkingCapitalAdjustment (~earnings.go:1467) emits ONLY an
// entities.Flag (no entities.Adjustment, no balance-sheet mutation). The
// legacy result returns Applied:true with an EMPTY Adjustments slice and a
// populated Flags slice.
//
// FlagEmitter convention reminder: every LedgerEntry stays Fired:false; the
// populated AdjusterOutput.Flags slice IS the firing signal. Component /
// DeltaAmount / EquityOffset / TaxShieldDTA are all zero.
//
// LedgerEntry shape across the three branches:
//   - No WC adjustment (WorkingCapitalAdjustment == 0): Fired:false,
//     SkipReason "No working capital adjustments detected", no SkipMetrics,
//     Flags empty.
//   - Below review threshold (wcRatio < 15% default; rule-configurable):
//     Fired:false, SkipReason citing ratio + threshold,
//     SkipMetrics:{wc_ratio, threshold, wc_amount}, Flags empty.
//   - Fired (wcRatio >= threshold): Fired:false, SkipReason
//     "flag-only review; no balance-sheet adjustment",
//     SkipMetrics:{wc_ratio, threshold, wc_amount}, Reasoning carrying the
//     legacy "Working capital window dressing: ..." string,
//     AdjusterOutput.Flags carrying exactly one Flag of Type
//     "earnings_quality".
//
// Legacy guard nuance: C7 uses `== 0` (NOT `<= 0`) because WorkingCapital
// Adjustment can be legitimately negative (signed delta vs. prior period).
// The ratio (wcAdj / Revenue) can therefore be negative; the legacy
// `wcRatio < threshold` comparison treats negative ratios as "below
// threshold" which is the same Skip behavior. We mirror that exactly.
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md §"Adjuster output"
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md §3.5 / §4 row C7 / §7 Task 3.7 / §10 Q2
func (ea *EarningsAdjuster) ApplyC7WorkingCapital(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	_ = ctx
	_ = cleaningCtx

	now := time.Now()

	// Skip path 1: no working-capital adjustment to review.
	if working.WorkingCapitalAdjustment == 0 {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:  now,
				AdjusterID: adjusterIDC7WorkingCapital,
				RuleID:     rule.ID,
				Fired:      false,
				Reasoning:  "No working capital adjustments detected",
				SkipReason: "No working capital adjustments detected",
			}},
		}, nil
	}

	wcAmount := working.WorkingCapitalAdjustment
	// Legacy code at line 1480 divides by Revenue without a guard. To
	// preserve byte-identical legacy parity we mirror that arithmetic; a
	// Revenue<=0 ticker with non-zero WC adjustment would have produced
	// +Inf or -Inf in the legacy code too (pre-existing data-quality
	// concern, NOT a regression introduced by the migration).
	wcRatio := wcAmount / working.Revenue
	threshold := 0.15 // Default 15% materiality threshold (legacy parity).
	if rule.Threshold != nil && rule.Threshold.PercentageOfRevenue != nil {
		threshold = *rule.Threshold.PercentageOfRevenue
	}

	// Skip path 2: ratio below materiality threshold. The legacy comparison
	// is unsigned (`wcRatio < threshold`), which treats negative ratios as
	// "below threshold" — mirrored here.
	if wcRatio < threshold {
		return AdjusterOutput{
			LedgerEntries: []entities.LedgerEntry{{
				Timestamp:  now,
				AdjusterID: adjusterIDC7WorkingCapital,
				RuleID:     rule.ID,
				Fired:      false,
				Reasoning:  fmt.Sprintf("Working capital adjustment below materiality threshold (%.1f%% < %.1f%%)", wcRatio*100, threshold*100),
				SkipReason: fmt.Sprintf("Working capital adjustment below materiality threshold (%.1f%% < %.1f%%)", wcRatio*100, threshold*100),
				SkipMetrics: map[string]float64{
					"wc_ratio":  wcRatio,
					"threshold": threshold,
					"wc_amount": wcAmount,
				},
			}},
		}, nil
	}

	// Fired path: window-dressing flag. No balance-sheet mutation — FlagEmitter
	// convention.
	out := AdjusterOutput{
		LedgerEntries: []entities.LedgerEntry{{
			Timestamp:  now,
			AdjusterID: adjusterIDC7WorkingCapital,
			RuleID:     rule.ID,
			Fired:      false,
			Reasoning:  fmt.Sprintf("Working capital window dressing: Flagged $%.1fM (%.1f%% of revenue) unusual movement", wcAmount/1000000, wcRatio*100),
			SkipReason: "flag-only review; no balance-sheet adjustment",
			SkipMetrics: map[string]float64{
				"wc_ratio":  wcRatio,
				"threshold": threshold,
				"wc_amount": wcAmount,
			},
		}},
	}

	// Window-dressing flag — mirror the legacy ProcessWorkingCapitalAdjustment
	// emission bit-for-bit (Type, Severity, Amount, Percentage, Description,
	// Recommendation).
	out.Flags = append(out.Flags, entities.Flag{
		ID:             fmt.Sprintf("wc_dressing_%d", now.UnixNano()),
		RuleID:         rule.ID,
		Type:           "earnings_quality",
		Severity:       rule.Severity,
		Amount:         wcAmount,
		Percentage:     wcRatio * 100,
		Description:    "Unusual working capital movements may indicate window dressing",
		Recommendation: "Review quarter-end receivables and payables patterns",
		Timestamp:      now,
	})

	return out, nil
}

// ProcessEarningsAdjustments applies all Category C earnings normalization rules.
//
// DC-1 Phase 2 PR-3 Task 3.1 (incremental Adjuster-interface migration):
// rules whose AdjusterID appears in result.NativelyEmittedRuleIDs have
// produced LedgerEntries / Overlays / Flags via their Adjuster.Apply path.
// The cleaner orchestrator (service.go::applyActiveAdjustments) reads those
// fields and appends them to data.AdjustmentLedger / data.Overlays directly,
// then instructs the shim to SKIP those rules so the same rule is not
// double-counted. Tasks 3.2-3.6 add more rules to the NativelyEmittedRuleIDs
// set; Task 3.8 deletes the shim's earnings branch entirely.
func (ea *EarningsAdjuster) ProcessEarningsAdjustments(ctx context.Context, data *entities.FinancialData, rules []*entities.CleaningRule, cleaningCtx *entities.CleaningContext) *EarningsAdjustmentResult {
	var allFlags []entities.Flag

	// Phase 2 PR-3 native emissions — collected here in rule-iteration order so
	// the orchestrator can append them to data.AdjustmentLedger in position.
	// The set NativelyEmittedRuleIDs records which rules emitted natively
	// (consumed by per-rule contract tests).
	var nativeLedger []entities.LedgerEntry
	var nativeOverlays []entities.OverlaySpec
	nativelyEmittedRuleIDs := make(map[string]bool, len(rules))

	// DC-1 Phase 3 (Task 3.9): ctx is now threaded through the public
	// signature from service.go::applyActiveAdjustments. The Apply
	// methods accept it as their first parameter per Adjuster interface
	// convention.
	applyCtx := ctx

	for _, rule := range rules {
		if !rule.Enabled || rule.Category != entities.EarningsNormalization {
			continue
		}

		switch rule.ID {
		case "restructuring_charges":
			// DC-1 Phase 2 PR-3 Task 3.1: route C1 through the new Adjuster-
			// shaped ApplyC1Restructuring. Mirrors the asset-side A1/A2/A4/A5
			// wiring — Apply is mutation-free; the dispatcher performs the
			// dual-write AFTER Apply so the legacy *AdjustmentResult callers
			// stay byte-identical AND the AdjusterOutput's LedgerEntries /
			// Flags reach the cleaner orchestrator.
			//
			out, err := ea.ApplyC1Restructuring(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				// Adjuster.Apply errors are not a defined surface today;
				// ApplyC1Restructuring never returns one. Skip on a
				// hypothetical future error (the deleted legacy fallback
				// would have bypassed the native path the orchestrator now
				// depends on exclusively).
				continue
			}

			// DC-1 Phase 4 (C-3, §8.2.1 Option A): the dispatcher applies the
			// fired LedgerEntry's COMPONENT delta (NormalizedOperatingIncome,
			// positive add-back for C1) to data via the generic helper. No
			// umbrella mutation. Consumers read Restated().NormalizedOperatingIncome.
			applyLedgerComponentDeltas(applyCtx, data, out)

			// Record native emissions for the orchestrator. Even when the rule
			// does not "fire" (Applied=false), the AdjusterOutput carries a
			// Fired:false LedgerEntry that is load-bearing for "why didn't C1
			// fire?" observability.
			allFlags = append(allFlags, out.Flags...)
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
		case "asset_sale_gains":
			// DC-1 Phase 2 PR-3 Task 3.2: route C2 through the new Adjuster-
			// shaped ApplyC2AssetSaleGains. Mirrors the C1 wiring above —
			// Apply is mutation-free; the dispatcher performs the dual-write
			// (subtraction, not add-back) AFTER Apply.
			out, err := ea.ApplyC2AssetSaleGains(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				continue
			}

			// DC-1 Phase 4 (C-3, §8.2.1 Option A): the helper applies the C2
			// LedgerEntry's signed COMPONENT delta (DeltaAmount = -gains, i.e.
			// a subtraction) to data.NormalizedOperatingIncome. No umbrella
			// mutation.
			applyLedgerComponentDeltas(applyCtx, data, out)

			allFlags = append(allFlags, out.Flags...)
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
		case "litigation_settlements":
			// DC-1 Phase 2 PR-3 Task 3.3: route C3 through ApplyC3Litigation.
			// Mirrors C1 wiring (POSITIVE add-back).
			out, err := ea.ApplyC3Litigation(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				continue
			}

			// DC-1 Phase 4 (C-3, §8.2.1 Option A): the helper applies the C3
			// LedgerEntry's COMPONENT delta (positive add-back of
			// LitigationSettlements) to data.NormalizedOperatingIncome. No
			// umbrella mutation.
			applyLedgerComponentDeltas(applyCtx, data, out)

			allFlags = append(allFlags, out.Flags...)
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
		case "stock_compensation":
			// DC-1 Phase 2 PR-3 Task 3.4: route C4 through the new Adjuster-
			// shaped ApplyC4StockCompensation. UNLIKE C1/C2/C3/C5/C6 (Restater
			// roles), C4 is a FlagEmitter — Apply does NOT mutate the balance
			// sheet on any path, and there is no dual-write step here. The
			// dilution Flag surfaces through allFlags. See
			// ApplyC4StockCompensation godoc for the plan-vs-code disagreement.
			out, err := ea.ApplyC4StockCompensation(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				continue
			}
			// NO dual-write: FlagEmitter convention — no balance-sheet mutation.

			allFlags = append(allFlags, out.Flags...)
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
		case "derivative_gains_losses":
			// DC-1 Phase 2 PR-3 Task 3.5: route C5 through ApplyC5DerivativeGainsLosses.
			// Mirrors the C1/C2/C3 wiring above — Apply is mutation-free; the
			// dispatcher performs the dual-write AFTER Apply.
			//
			// Branch-divergent sign convention (load-bearing): the LedgerEntry
			// DeltaAmount is signed (negative on the gain branch, positive on
			// the loss branch), so the dual-write uses
			// `NormalizedOperatingIncome += DeltaAmount` (mirroring legacy
			// `NormalizedOperatingIncome -= rawAmount`).
			//
			// The COMPONENT mutation reads the SIGNED DeltaAmount off the
			// native LedgerEntry directly (negative on the gain branch,
			// positive on the loss branch).
			out, err := ea.ApplyC5DerivativeGainsLosses(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				continue
			}

			// DC-1 Phase 4 (C-3, §8.2.1 Option A): the generic helper applies
			// the SIGNED COMPONENT DeltaAmount (negative on the gain branch,
			// positive on the loss branch) to data.NormalizedOperatingIncome —
			// exactly the per-entry `+= DeltaAmount` the old hand-rolled loop
			// did. No umbrella mutation.
			applyLedgerComponentDeltas(applyCtx, data, out)

			allFlags = append(allFlags, out.Flags...)
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
		case "capitalized_interest":
			// DC-1 Phase 2 PR-3 Task 3.6: route C6 through ApplyC6CapitalizedInterest.
			// Mirrors the C1/C2/C3/C5 wiring — Apply is mutation-free; the
			// dispatcher performs the dual-write AFTER Apply.
			//
			// LOAD-BEARING SPECIAL CASE: C6's LedgerEntry has EquityOffset = 0
			// because capitalized interest is a reclassification BETWEEN
			// income-statement lines (operating expense → interest expense),
			// NOT an equity-flowing event. Phase 3's Restated() accessor must
			// NOT add C6's DeltaAmount to retained earnings.
			//
			// COMPONENT mutation targets `data.InterestExpense` (NOT
			// NormalizedOperatingIncome — different field!).
			out, err := ea.ApplyC6CapitalizedInterest(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				continue
			}

			// DC-1 Phase 4 (C-3, §8.2.1 Option A): the helper applies the C6
			// LedgerEntry's COMPONENT delta to data.InterestExpense (NOT
			// NormalizedOperatingIncome — different field). C6's EquityOffset
			// stays 0 (LOAD-BEARING: capitalized-interest reclassification is
			// between income-statement lines, not an equity event), which the
			// helper respects by touching only the component field. No umbrella
			// mutation.
			applyLedgerComponentDeltas(applyCtx, data, out)

			allFlags = append(allFlags, out.Flags...)
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
		case "working_capital_window_dressing":
			// DC-1 Phase 2 PR-3 Task 3.7: route C7 through the new Adjuster-
			// shaped ApplyC7WorkingCapital. Like C4, C7 is a FlagEmitter
			// (window-dressing flag only — no balance-sheet mutation). The
			// fired window-dressing Flag surfaces through allFlags.
			out, err := ea.ApplyC7WorkingCapital(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				continue
			}
			// NO dual-write: FlagEmitter convention — no balance-sheet mutation.

			allFlags = append(allFlags, out.Flags...)
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
		default:
			// Skip unknown rules
			continue
		}
	}

	// DC-1 Phase 5 P5-C4: the legacy *AdjustmentResult accumulation +
	// audit-trail Reasoning string were deleted. The orchestrator projects
	// the public audit trail from data.AdjustmentLedger via
	// adjustmentsFromLedger; flags + native emissions are drained per-arm
	// above. Earnings rules never mutate an umbrella, so there is no
	// post-loop recompute (unlike the asset dispatcher).
	return &EarningsAdjustmentResult{
		Flags:                  allFlags,
		NativeLedgerEntries:    nativeLedger,
		NativeOverlays:         nativeOverlays,
		NativelyEmittedRuleIDs: nativelyEmittedRuleIDs,
	}
}
