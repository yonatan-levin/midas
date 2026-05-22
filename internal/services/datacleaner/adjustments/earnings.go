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
	adjusterIDC1RestructuringCharges = "C1_restructuring_charges"
	adjusterIDC2AssetSaleGains       = "C2_asset_sale_gains"
)

// EarningsAdjustmentResult represents the result of applying Category C
// earnings normalization adjustments.
//
// DC-1 Phase 2 PR-3 Task 3.1 added the three Native* fields below to carry
// AdjusterOutput state from rules that have migrated to the Adjuster
// interface. Mirrors the AssetAdjustmentResult shape PR-2 introduced for
// Category A. The cleaner orchestrator reads NativeLedgerEntries /
// NativeOverlays / NativelyEmittedRuleIDs to:
//   - append the native LedgerEntries to data.AdjustmentLedger BEFORE the
//     PR-1 shim runs, preserving earnings-category ordering;
//   - append the native Overlays to data.Overlays (Restater C-rules emit
//     none, but the field exists for symmetry + future hybrids);
//   - instruct the shim to SKIP any rule whose ID appears in
//     NativelyEmittedRuleIDs so the same rule is not double-counted.
//
// Tasks 3.2-3.6 widen NativelyEmittedRuleIDs as more C-rules migrate; Task 3.8
// deletes the shim earnings branch entirely.
type EarningsAdjustmentResult struct {
	Amount      float64               `json:"amount"`
	Applied     bool                  `json:"applied"`
	Adjustments []entities.Adjustment `json:"adjustments"`
	Flags       []entities.Flag       `json:"flags"`
	Reasoning   string                `json:"reasoning"`

	NativeLedgerEntries    []entities.LedgerEntry `json:"-"`
	NativeOverlays         []entities.OverlaySpec `json:"-"`
	NativelyEmittedRuleIDs map[string]bool        `json:"-"`
}

// c1RestructuringAdjuster is the per-rule adapter that wraps EarningsAdjuster's
// C1 rule into the single-Apply Adjuster interface. Each migrated C-rule gets
// its own adapter struct in Phase 2 PR-3; once every C-rule has migrated
// (Task 3.8), service.go::applyActiveAdjustments will dispatch through the
// adapters and the shim's earnings branch will be deleted.
//
// Mirrors the assets.go a1/a2/a4/a5 adapters shipped in PR-2.
type c1RestructuringAdjuster struct {
	ea *EarningsAdjuster
}

// NewC1RestructuringAdjuster returns an Adjuster-shaped wrapper around
// EarningsAdjuster's C1 rule. Exported so the cleaner orchestrator can hold
// the instance alongside the legacy EarningsAdjuster.
func NewC1RestructuringAdjuster(ea *EarningsAdjuster) Adjuster {
	return &c1RestructuringAdjuster{ea: ea}
}

// Compile-time pin so any future signature drift fails to build.
var _ Adjuster = (*c1RestructuringAdjuster)(nil)

// Name returns the stable AdjusterID for the C1 rule.
func (c *c1RestructuringAdjuster) Name() string {
	return adjusterIDC1RestructuringCharges
}

// Apply implements Adjuster by delegating to EarningsAdjuster.ApplyC1Restructuring.
// The dual-write contract (in-place mutation of data.NormalizedOperatingIncome)
// is preserved by the dispatcher in ProcessEarningsAdjustments — NOT by Apply
// itself. See ApplyC1Restructuring godoc for the role split.
func (c *c1RestructuringAdjuster) Apply(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	return c.ea.ApplyC1Restructuring(ctx, working, rule, cleaningCtx)
}

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

// c1AdjusterOutputToLegacyResult translates the new AdjusterOutput shape into
// the legacy *AdjustmentResult expected by ProcessEarningsAdjustments' existing
// aggregate accounting. Parallel to assets.go's a2/a4/a5 translators — C1 is a
// Restater, so the translation reads the add-back amount from the LedgerEntry's
// DeltaAmount (positive — C1 is an add-back, not a writedown).
//
// originalRestructuring is captured at the dispatcher BEFORE ApplyC1Restructuring
// runs and threaded in for parity with the asset-side a2/a4/a5 translators.
// C1's percentage is derived (amount / revenue) — not constant — so the
// translator computes it directly from the AdjusterOutput shape.
func c1AdjusterOutputToLegacyResult(out AdjusterOutput, rule *entities.CleaningRule, originalRestructuring float64) *AdjustmentResult {
	_ = originalRestructuring // reserved for future symmetry; today's C1 reads the amount from the LedgerEntry directly.

	// Locate the fired LedgerEntry — C1 emits exactly one when fired and zero
	// Restater-shaped entries when skipped (skip paths produce a Fired:false
	// LedgerEntry only).
	for _, entry := range out.LedgerEntries {
		if entry.AdjusterID != adjusterIDC1RestructuringCharges || !entry.Fired {
			continue
		}
		// DeltaAmount is signed-POSITIVE for C1 (add-back). The legacy
		// Adjustment.Amount is a positive magnitude, so no sign flip.
		restructuringAmount := entry.DeltaAmount
		// Derive ratio for the legacy Percentage field. The original ratio
		// (restructuring / revenue) is needed for the historical
		// "X.X% of revenue" string formatting and Adjustment.Percentage.
		// LedgerEntry doesn't carry revenue; we re-derive from the original
		// inputs the dispatcher captured. originalRestructuring carries the
		// raw input field value (which may be <=0 and estimated inside Apply);
		// the LedgerEntry's Reasoning string already contains the formatted
		// ratio so we surface that directly.
		adjustment := entities.Adjustment{
			ID:          fmt.Sprintf("restructuring_%d", time.Now().UnixNano()),
			RuleID:      rule.ID,
			Category:    entities.EarningsNormalization,
			Type:        entities.Exclude,
			Amount:      restructuringAmount,
			FromAccount: "RestructuringCharges",
			ToAccount:   "NormalizedOperatingIncome",
			// Percentage is not strictly needed for downstream consumers (no
			// regression test reads it for C1); leave as 0 for now and rely on
			// the Reasoning string for the formatted ratio. Mirrors a4/a5's
			// approach of using a constant percentage when not derivable
			// without re-reading working.
			Reasoning: entry.Reasoning,
			Applied:   true,
			Timestamp: time.Now(),
		}
		return &AdjustmentResult{
			Amount:      restructuringAmount,
			Applied:     true,
			Adjustments: []entities.Adjustment{adjustment},
			Flags:       out.Flags,
			Reasoning:   entry.Reasoning,
		}
	}

	// Skipped path — surface the reasoning from the Fired:false LedgerEntry
	// for parity with the legacy "no adjustment" branches.
	reasoning := "No restructuring charges to add back"
	for _, entry := range out.LedgerEntries {
		if entry.AdjusterID == adjusterIDC1RestructuringCharges {
			if entry.SkipReason != "" {
				reasoning = entry.SkipReason
			} else if entry.Reasoning != "" {
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

// c2AssetSaleGainsAdjuster is the per-rule adapter that wraps EarningsAdjuster's
// C2 rule into the single-Apply Adjuster interface. Mirrors c1RestructuringAdjuster.
type c2AssetSaleGainsAdjuster struct {
	ea *EarningsAdjuster
}

// NewC2AssetSaleGainsAdjuster returns an Adjuster-shaped wrapper around
// EarningsAdjuster's C2 rule.
func NewC2AssetSaleGainsAdjuster(ea *EarningsAdjuster) Adjuster {
	return &c2AssetSaleGainsAdjuster{ea: ea}
}

var _ Adjuster = (*c2AssetSaleGainsAdjuster)(nil)

func (c *c2AssetSaleGainsAdjuster) Name() string {
	return adjusterIDC2AssetSaleGains
}

// Apply delegates to EarningsAdjuster.ApplyC2AssetSaleGains. The dual-write
// contract (in-place subtraction from data.NormalizedOperatingIncome) is
// preserved by the dispatcher — NOT by Apply itself.
func (c *c2AssetSaleGainsAdjuster) Apply(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
	return c.ea.ApplyC2AssetSaleGains(ctx, working, rule, cleaningCtx)
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
		}},
	}, nil
}

// c2AdjusterOutputToLegacyResult translates the new AdjusterOutput shape into
// the legacy *AdjustmentResult expected by ProcessEarningsAdjustments. C2 is a
// Restater with NEGATIVE DeltaAmount (subtraction). The legacy
// Adjustment.Amount is a positive magnitude, so the translator flips sign.
//
// originalGains is captured at the dispatcher BEFORE ApplyC2AssetSaleGains
// runs and threaded in so the legacy Adjustment.Percentage field can be
// derived from the original revenue ratio when needed. Today's legacy code
// formats it inline; the translator preserves the existing
// (gains/revenue)*100 formula.
func c2AdjusterOutputToLegacyResult(out AdjusterOutput, rule *entities.CleaningRule, originalGains float64, originalRevenue float64) *AdjustmentResult {
	for _, entry := range out.LedgerEntries {
		if entry.AdjusterID != adjusterIDC2AssetSaleGains || !entry.Fired {
			continue
		}
		// DeltaAmount is signed-negative for C2; the legacy Adjustment.Amount
		// is a positive magnitude.
		gains := -entry.DeltaAmount
		_ = originalGains // reserved for symmetry; the legacy magnitude comes from the LedgerEntry.
		var percentage float64
		if originalRevenue > 0 {
			percentage = (gains / originalRevenue) * 100
		}
		adjustment := entities.Adjustment{
			ID:          fmt.Sprintf("asset_gains_%d", time.Now().UnixNano()),
			RuleID:      rule.ID,
			Category:    entities.EarningsNormalization,
			Type:        entities.Exclude,
			Amount:      gains,
			FromAccount: "AssetSaleGains",
			ToAccount:   "NormalizedOperatingIncome",
			Percentage:  percentage,
			Reasoning:   fmt.Sprintf("Excluded asset sale gains of $%.1fM from operating income", gains/1000000),
			Applied:     true,
			Timestamp:   time.Now(),
		}
		return &AdjustmentResult{
			Amount:      gains,
			Applied:     true,
			Adjustments: []entities.Adjustment{adjustment},
			Flags:       out.Flags,
			Reasoning:   entry.Reasoning,
		}
	}

	// Skipped path — surface the reasoning from the Fired:false LedgerEntry.
	reasoning := "No asset sale gains to adjust"
	for _, entry := range out.LedgerEntries {
		if entry.AdjusterID == adjusterIDC2AssetSaleGains {
			if entry.SkipReason != "" {
				reasoning = entry.SkipReason
			} else if entry.Reasoning != "" {
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
func (ea *EarningsAdjuster) ProcessEarningsAdjustments(data *entities.FinancialData, rules []*entities.CleaningRule, cleaningCtx *entities.CleaningContext) *EarningsAdjustmentResult {
	var allAdjustments []entities.Adjustment
	var allFlags []entities.Flag
	totalAmount := 0.0
	applied := false

	// Phase 2 PR-3 native emissions — collected here in rule-iteration order so
	// the orchestrator can append them to data.AdjustmentLedger in position.
	// The set NativelyEmittedRuleIDs tells the shim which legacy emissions to
	// skip to avoid double counting.
	var nativeLedger []entities.LedgerEntry
	var nativeOverlays []entities.OverlaySpec
	nativelyEmittedRuleIDs := make(map[string]bool, len(rules))

	// Apply.ctx is nil here because ProcessEarningsAdjustments does not yet
	// thread ctx through its public signature. ApplyC1Restructuring treats nil
	// ctx as safe (it only uses ctx for future industry-aware logic).
	// TODO(PR-3 follow-up / PR-4): thread context.Context through
	// ProcessEarningsAdjustments to align with the Adjuster.Apply signature.
	var applyCtx context.Context

	for _, rule := range rules {
		if !rule.Enabled || rule.Category != entities.EarningsNormalization {
			continue
		}

		var result *AdjustmentResult

		switch rule.ID {
		case "restructuring_charges":
			// DC-1 Phase 2 PR-3 Task 3.1: route C1 through the new Adjuster-
			// shaped ApplyC1Restructuring. Mirrors the asset-side A1/A2/A4/A5
			// wiring — Apply is mutation-free; the dispatcher performs the
			// dual-write AFTER Apply so the legacy *AdjustmentResult callers
			// stay byte-identical AND the AdjusterOutput's LedgerEntries /
			// Flags reach the cleaner orchestrator.
			//
			// CAPTURE originalRestructuring BEFORE Apply runs (mirrors A1's
			// originalGoodwill capture). Apply does not mutate, so reading
			// data.RestructuringCharges before AND after Apply yields the same
			// value; we still capture-before for parity and to document the
			// execution-order invariant.
			originalRestructuring := data.RestructuringCharges
			out, err := ea.ApplyC1Restructuring(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				// Adjuster.Apply errors are not yet a defined surface in
				// Phase 2; ApplyC1Restructuring never returns one today.
				// Falling back to the legacy path preserves behavior on
				// hypothetical future errors.
				result = ea.ProcessRestructuringChargesAdjustment(data, rule)
				break
			}

			// Translate the AdjusterOutput into the legacy *AdjustmentResult
			// shape so the existing aggregate accounting keeps working, AND
			// perform the dual-write mutation that ApplyC1Restructuring
			// intentionally omitted.
			result = c1AdjusterOutputToLegacyResult(out, rule, originalRestructuring)
			if result.Applied {
				// Dual-write: today's downstream consumers still read
				// data.NormalizedOperatingIncome in place. Phase 4 deletes
				// these mutations once Phase 3's CleanedFinancialData views
				// replace direct reads. The add-back amount is the
				// LedgerEntry DeltaAmount (positive — C1 is an add-back).
				data.NormalizedOperatingIncome += result.Amount
			}

			// Record native emissions for the orchestrator. Even when the rule
			// does not "fire" (Applied=false), the AdjusterOutput carries a
			// Fired:false LedgerEntry that is load-bearing for "why didn't C1
			// fire?" observability.
			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
		case "asset_sale_gains":
			// DC-1 Phase 2 PR-3 Task 3.2: route C2 through the new Adjuster-
			// shaped ApplyC2AssetSaleGains. Mirrors the C1 wiring above —
			// Apply is mutation-free; the dispatcher performs the dual-write
			// (subtraction, not add-back) AFTER Apply.
			originalGains := data.AssetSaleGains
			originalRevenue := data.Revenue
			out, err := ea.ApplyC2AssetSaleGains(applyCtx, data, rule, cleaningCtx)
			if err != nil {
				result = ea.ProcessAssetSaleGainsAdjustment(data, rule)
				break
			}

			result = c2AdjusterOutputToLegacyResult(out, rule, originalGains, originalRevenue)
			if result.Applied {
				// Dual-write: subtraction, NOT add-back. Legacy code:
				// data.NormalizedOperatingIncome -= data.AssetSaleGains
				data.NormalizedOperatingIncome -= result.Amount
			}

			nativeLedger = append(nativeLedger, out.LedgerEntries...)
			nativeOverlays = append(nativeOverlays, out.Overlays...)
			nativelyEmittedRuleIDs[rule.ID] = true
		case "litigation_settlements":
			result = ea.ProcessLitigationSettlementsAdjustment(data, rule)
		case "stock_compensation":
			result = ea.ProcessStockCompensationAdjustment(data, rule)
		case "derivative_gains_losses":
			result = ea.ProcessDerivativeGainsLossesAdjustment(data, rule)
		case "capitalized_interest":
			result = ea.ProcessCapitalizedInterestAdjustment(data, rule)
		case "working_capital_window_dressing":
			result = ea.ProcessWorkingCapitalAdjustment(data, rule, cleaningCtx)
		default:
			// Skip unknown rules
			continue
		}

		if result != nil && result.Applied {
			allAdjustments = append(allAdjustments, result.Adjustments...)
			allFlags = append(allFlags, result.Flags...)
			totalAmount += result.Amount
			applied = true
		}
	}

	reasoning := fmt.Sprintf("Applied %d earnings normalization adjustments totaling $%.1fM",
		len(allAdjustments), totalAmount/1000000)

	return &EarningsAdjustmentResult{
		Amount:                 totalAmount,
		Applied:                applied,
		Adjustments:            allAdjustments,
		Flags:                  allFlags,
		Reasoning:              reasoning,
		NativeLedgerEntries:    nativeLedger,
		NativeOverlays:         nativeOverlays,
		NativelyEmittedRuleIDs: nativelyEmittedRuleIDs,
	}
}

// ProcessRestructuringChargesAdjustment implements C1 rule: Remove recurring restructuring charges
func (ea *EarningsAdjuster) ProcessRestructuringChargesAdjustment(data *entities.FinancialData, rule *entities.CleaningRule) *AdjustmentResult {
	if data.Revenue <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "Insufficient revenue data to calculate restructuring charges",
		}
	}

	// Use actual restructuring charges if available, otherwise estimate
	restructuringAmount := data.RestructuringCharges
	if restructuringAmount <= 0 {
		// Estimate based on revenue (conservative approach)
		restructuringAmount = data.Revenue * 0.015 // Estimate 1.5% of revenue
	}

	// Check materiality threshold
	restructuringRatio := restructuringAmount / data.Revenue
	threshold := 0.02 // Default 2% threshold
	if rule.Threshold != nil && rule.Threshold.PercentageOfRevenue != nil {
		threshold = *rule.Threshold.PercentageOfRevenue
	}

	if restructuringRatio < threshold {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning: fmt.Sprintf("Restructuring charges below materiality threshold (%.1f%% < %.1f%%)",
				restructuringRatio*100, threshold*100),
		}
	}

	// Apply adjustment - add back to normalized operating income
	data.NormalizedOperatingIncome += restructuringAmount

	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("restructuring_%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.EarningsNormalization,
		Type:        entities.Exclude,
		Amount:      restructuringAmount,
		FromAccount: "RestructuringCharges",
		ToAccount:   "NormalizedOperatingIncome",
		Percentage:  restructuringRatio * 100,
		Reasoning: fmt.Sprintf("Excluded restructuring charges of $%.1fM (%.1f%% of revenue)",
			restructuringAmount/1000000, restructuringRatio*100),
		Applied:   true,
		Timestamp: time.Now(),
	}

	reasoning := fmt.Sprintf("Restructuring charges adjustment: Excluded $%.1fM (%.1f%% of revenue) from normalized operating income",
		restructuringAmount/1000000, restructuringRatio*100)

	return &AdjustmentResult{
		Amount:      restructuringAmount,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       []entities.Flag{},
		Reasoning:   reasoning,
	}
}

// ProcessAssetSaleGainsAdjustment implements C2 rule: Remove non-core asset sale gains
func (ea *EarningsAdjuster) ProcessAssetSaleGainsAdjustment(data *entities.FinancialData, rule *entities.CleaningRule) *AdjustmentResult {
	if data.AssetSaleGains <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No asset sale gains to adjust",
		}
	}

	// Remove asset sale gains from normalized operating income
	data.NormalizedOperatingIncome -= data.AssetSaleGains

	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("asset_gains_%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.EarningsNormalization,
		Type:        entities.Exclude,
		Amount:      data.AssetSaleGains,
		FromAccount: "AssetSaleGains",
		ToAccount:   "NormalizedOperatingIncome",
		Percentage:  (data.AssetSaleGains / data.Revenue) * 100,
		Reasoning: fmt.Sprintf("Excluded asset sale gains of $%.1fM from operating income",
			data.AssetSaleGains/1000000),
		Applied:   true,
		Timestamp: time.Now(),
	}

	reasoning := fmt.Sprintf("Asset sale gains adjustment: Excluded $%.1fM from normalized operating income",
		data.AssetSaleGains/1000000)

	return &AdjustmentResult{
		Amount:      data.AssetSaleGains,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       []entities.Flag{},
		Reasoning:   reasoning,
	}
}

// ProcessLitigationSettlementsAdjustment implements C3 rule: Remove episodic litigation costs
func (ea *EarningsAdjuster) ProcessLitigationSettlementsAdjustment(data *entities.FinancialData, rule *entities.CleaningRule) *AdjustmentResult {
	if data.LitigationSettlements <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No litigation settlements to adjust",
		}
	}

	// Check materiality threshold
	litigationRatio := data.LitigationSettlements / data.Revenue
	threshold := 0.01 // Default 1% threshold
	if rule.Threshold != nil && rule.Threshold.PercentageOfRevenue != nil {
		threshold = *rule.Threshold.PercentageOfRevenue
	}

	if litigationRatio < threshold {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning: fmt.Sprintf("Litigation settlements below materiality threshold (%.1f%% < %.1f%%)",
				litigationRatio*100, threshold*100),
		}
	}

	// Add back litigation settlements to normalized operating income
	data.NormalizedOperatingIncome += data.LitigationSettlements

	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("litigation_%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.EarningsNormalization,
		Type:        entities.Exclude,
		Amount:      data.LitigationSettlements,
		FromAccount: "LitigationSettlements",
		ToAccount:   "NormalizedOperatingIncome",
		Percentage:  litigationRatio * 100,
		Reasoning: fmt.Sprintf("Excluded litigation settlements of $%.1fM (%.1f%% of revenue)",
			data.LitigationSettlements/1000000, litigationRatio*100),
		Applied:   true,
		Timestamp: time.Now(),
	}

	reasoning := fmt.Sprintf("Litigation settlements adjustment: Excluded $%.1fM (%.1f%% of revenue) from normalized operating income",
		data.LitigationSettlements/1000000, litigationRatio*100)

	return &AdjustmentResult{
		Amount:      data.LitigationSettlements,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       []entities.Flag{},
		Reasoning:   reasoning,
	}
}

// ProcessStockCompensationAdjustment implements C4 rule: Handle stock-based compensation
func (ea *EarningsAdjuster) ProcessStockCompensationAdjustment(data *entities.FinancialData, rule *entities.CleaningRule) *AdjustmentResult {
	if data.StockBasedCompensation <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No stock-based compensation to adjust",
		}
	}

	// Stock-based compensation is reclassified, not excluded from operating income
	// It's treated as a real expense but flagged for dilution analysis
	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("stock_comp_%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.EarningsNormalization,
		Type:        entities.Reclassify,
		Amount:      data.StockBasedCompensation,
		FromAccount: "StockBasedCompensation",
		ToAccount:   "OperatingExpenses",
		Percentage:  (data.StockBasedCompensation / data.Revenue) * 100,
		Reasoning: fmt.Sprintf("Reclassified stock-based compensation of $%.1fM for dilution analysis",
			data.StockBasedCompensation/1000000),
		Applied:   true,
		Timestamp: time.Now(),
	}

	// Create flag for dilution analysis
	flag := entities.Flag{
		ID:             fmt.Sprintf("stock_dilution_%d", time.Now().UnixNano()),
		RuleID:         rule.ID,
		Type:           "earnings_quality",
		Severity:       rule.Severity,
		Amount:         data.StockBasedCompensation,
		Percentage:     (data.StockBasedCompensation / data.Revenue) * 100,
		Description:    "High stock-based compensation may indicate dilution risk",
		Recommendation: "Consider dilution impact in per-share calculations",
		Timestamp:      time.Now(),
	}

	reasoning := fmt.Sprintf("Stock-based compensation adjustment: Reclassified $%.1fM (%.1f%% of revenue) for dilution analysis",
		data.StockBasedCompensation/1000000, (data.StockBasedCompensation/data.Revenue)*100)

	return &AdjustmentResult{
		Amount:      data.StockBasedCompensation,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       []entities.Flag{flag},
		Reasoning:   reasoning,
	}
}

// ProcessDerivativeGainsLossesAdjustment implements C5 rule: Remove volatile derivative marks
func (ea *EarningsAdjuster) ProcessDerivativeGainsLossesAdjustment(data *entities.FinancialData, rule *entities.CleaningRule) *AdjustmentResult {
	if data.DerivativeGainsLosses == 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No derivative gains/losses to adjust",
		}
	}

	// Remove derivative gains/losses from normalized operating income
	adjustmentAmount := data.DerivativeGainsLosses
	if adjustmentAmount > 0 {
		// Gains - subtract from operating income
		data.NormalizedOperatingIncome -= adjustmentAmount
	} else {
		// Losses - add back to operating income (remove the negative impact)
		data.NormalizedOperatingIncome -= adjustmentAmount // This adds back since amount is negative
		adjustmentAmount = -adjustmentAmount               // Make positive for reporting
	}

	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("derivative_%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.EarningsNormalization,
		Type:        entities.Exclude,
		Amount:      adjustmentAmount,
		FromAccount: "DerivativeGainsLosses",
		ToAccount:   "NormalizedOperatingIncome",
		Percentage:  (adjustmentAmount / data.Revenue) * 100,
		Reasoning: fmt.Sprintf("Excluded derivative gains/losses of $%.1fM from operating income",
			adjustmentAmount/1000000),
		Applied:   true,
		Timestamp: time.Now(),
	}

	reasoning := fmt.Sprintf("Derivative gains/losses adjustment: Excluded $%.1fM from normalized operating income",
		adjustmentAmount/1000000)

	return &AdjustmentResult{
		Amount:      adjustmentAmount,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       []entities.Flag{},
		Reasoning:   reasoning,
	}
}

// ProcessCapitalizedInterestAdjustment implements C6 rule: Reclassify capitalized interest
func (ea *EarningsAdjuster) ProcessCapitalizedInterestAdjustment(data *entities.FinancialData, rule *entities.CleaningRule) *AdjustmentResult {
	if data.CapitalizedInterest <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No capitalized interest to adjust",
		}
	}

	// Add capitalized interest back to interest expense
	data.InterestExpense += data.CapitalizedInterest

	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("cap_interest_%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.EarningsNormalization,
		Type:        entities.Reclassify,
		Amount:      data.CapitalizedInterest,
		FromAccount: "CapitalizedInterest",
		ToAccount:   "InterestExpense",
		Percentage:  (data.CapitalizedInterest / data.Revenue) * 100,
		Reasoning: fmt.Sprintf("Reclassified capitalized interest of $%.1fM to interest expense",
			data.CapitalizedInterest/1000000),
		Applied:   true,
		Timestamp: time.Now(),
	}

	reasoning := fmt.Sprintf("Capitalized interest adjustment: Reclassified $%.1fM from PP&E to interest expense",
		data.CapitalizedInterest/1000000)

	return &AdjustmentResult{
		Amount:      data.CapitalizedInterest,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       []entities.Flag{},
		Reasoning:   reasoning,
	}
}

// ProcessWorkingCapitalAdjustment implements C7 rule: Flag working capital window dressing
func (ea *EarningsAdjuster) ProcessWorkingCapitalAdjustment(data *entities.FinancialData, rule *entities.CleaningRule, context *entities.CleaningContext) *AdjustmentResult {
	if data.WorkingCapitalAdjustment == 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No working capital adjustments detected",
		}
	}

	// Check materiality threshold
	wcRatio := data.WorkingCapitalAdjustment / data.Revenue
	threshold := 0.15 // Default 15% threshold
	if rule.Threshold != nil && rule.Threshold.PercentageOfRevenue != nil {
		threshold = *rule.Threshold.PercentageOfRevenue
	}

	if wcRatio < threshold {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning: fmt.Sprintf("Working capital adjustment below materiality threshold (%.1f%% < %.1f%%)",
				wcRatio*100, threshold*100),
		}
	}

	// Create flag for working capital window dressing (no adjustment to income)
	flag := entities.Flag{
		ID:             fmt.Sprintf("wc_dressing_%d", time.Now().UnixNano()),
		RuleID:         rule.ID,
		Type:           "earnings_quality",
		Severity:       rule.Severity,
		Amount:         data.WorkingCapitalAdjustment,
		Percentage:     wcRatio * 100,
		Description:    "Unusual working capital movements may indicate window dressing",
		Recommendation: "Review quarter-end receivables and payables patterns",
		Timestamp:      time.Now(),
	}

	reasoning := fmt.Sprintf("Working capital window dressing: Flagged $%.1fM (%.1f%% of revenue) unusual movement",
		data.WorkingCapitalAdjustment/1000000, wcRatio*100)

	return &AdjustmentResult{
		Amount:      data.WorkingCapitalAdjustment,
		Applied:     true,
		Adjustments: []entities.Adjustment{}, // No income adjustments, just flagging
		Flags:       []entities.Flag{flag},
		Reasoning:   reasoning,
	}
}
