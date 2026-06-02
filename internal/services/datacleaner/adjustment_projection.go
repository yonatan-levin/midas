package datacleaner

import (
	"fmt"
	"math"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// percentageMode names how a rule populates the legacy Adjustment.Percentage
// field. Exactly one mode fires per rule.
type percentageMode string

const (
	// percentageAbsent — the rule's legacy translator does not set
	// Percentage; the field stays at Go's zero value 0.0 and is omitted
	// from JSON via the struct tag `omitempty`. Used by A1, A-RD,
	// A-CapSW, B1, B2, B3, C1 (deliberate-zero per its inline comment),
	// C7 (no Adjustment emitted at all).
	percentageAbsent percentageMode = "absent"
	// percentageConstant — the rule's legacy translator hard-codes a
	// constant Percentage value regardless of pre-state. Used by A4
	// (50.0) and A5 (40.0).
	percentageConstant percentageMode = "constant"
	// percentageFromPreState — the rule's legacy translator computes
	// Percentage from a pre-state denominator captured on the
	// LedgerEntry's SkipMetrics map under the "original_<Field>"
	// convention (DC-1 P5-followup §4.2). Used by A2 (denominator
	// original_OtherIntangibles) and C2/C3/C4/C5/C6 (denominator
	// original_Revenue).
	percentageFromPreState percentageMode = "from_pre_state"
)

// amountSource names how a rule's legacy Adjustment.Amount field is sourced
// from the native LedgerEntry / OverlaySpec emissions.
type amountSource string

const (
	// amountLedgerDeltaAbs — Amount = abs(LedgerEntry.DeltaAmount). Used
	// by Restaters where DeltaAmount is signed (negative on writedowns
	// like A2/A5/C2/C5-gain, positive on add-backs like C3/C5-loss/C6,
	// positive-or-zero on A4 valuation allowance).
	amountLedgerDeltaAbs amountSource = "ledger_delta_abs"
	// amountOverlayAmount — Amount = OverlaySpec.Amount. Used by
	// OverlayEmitters (A1/B1/B2/B3) where the LedgerEntry's
	// DeltaAmount/Component is empty by role contract.
	amountOverlayAmount amountSource = "overlay_amount"
	// amountSkipMetricsSBCAmount — Amount = LedgerEntry.SkipMetrics["sbc_amount"].
	// Used by C4 (FlagEmitter with non-empty Flags as firing signal;
	// SBC magnitude lives on SkipMetrics because C4's LedgerEntry has
	// Fired:false and no DeltaAmount by role contract).
	amountSkipMetricsSBCAmount amountSource = "skipmetrics_sbc_amount"
)

// reasoningFormatter receives the resolved amount + denominator-or-zero +
// computed Percentage (already in 0-100 scale) and returns the per-rule
// Adjustment.Reasoning string. Per-rule because the legacy translators
// each formatted their own variant — some include a (% of revenue) suffix,
// some don't. See §4.7 of the P5-followup spec for the option-a-vs-b
// tradeoff; we picked option-b (per-rule formatter) so the LedgerEntry's
// own Reasoning string can stay semantically richer than the projected
// Adjustment.Reasoning without breaking the basket-parity golden.
type reasoningFormatter func(amount, denominator, percentage float64) string

// ruleMeta carries the static-per-rule context that each legacy translator
// hard-coded. The projection helper looks up the AdjusterID → ruleMeta
// once per LedgerEntry firing and assembles the resulting entities.Adjustment.
type ruleMeta struct {
	Category    entities.RuleCategory
	Type        entities.AdjustmentType
	FromAccount string
	ToAccount   string // empty → omitted from JSON via `omitempty`

	PercentageMode percentageMode
	ConstantPct    float64 // populated when PercentageMode == percentageConstant
	PreStateKey    string  // populated when PercentageMode == percentageFromPreState

	AmountSource amountSource

	// Reasoning emits the per-rule audit-trail string. Receives the
	// resolved Amount + the resolved denominator (0 when not in
	// from_pre_state mode) + the computed Percentage (already in
	// 0-100 scale).
	Reasoning reasoningFormatter
}

// reasoningFromOverlay returns a formatter that ignores the amount/denom
// arithmetic and surfaces the matching OverlaySpec's Reasoning string
// directly. Used by A1/B1/B2/B3 — the OverlayEmitter family where the
// per-rule Reasoning lives on the Overlay payload.
func reasoningFromOverlay(overlays []entities.OverlaySpec, overlayID string) string {
	for _, ov := range overlays {
		if ov.OverlayID == overlayID {
			return ov.Reasoning
		}
	}
	return ""
}

// perRuleAdjustmentMeta is the projection's metadata table — one row per
// AdjusterID. Transcribed from §4.6 of the P5-followup spec and verified
// against the legacy translator implementations one-by-one.
//
// Maintainability note: adding a new adjuster requires adding a row here.
// The projection helper emits zero Adjustments and silently skips any
// LedgerEntry whose AdjusterID is not in this map (no logger dependency at
// this layer; covered by the defensive pin
// TestAdjustmentsProjection_HandlesUnknownAdjusterID).
//
//nolint:gochecknoglobals // immutable canonical-set sentinel; populated once via package init.
var perRuleAdjustmentMeta = map[string]ruleMeta{
	// A1 goodwill exclusion — OverlayEmitter, no Percentage.
	"A1_goodwill_exclusion": {
		Category:       entities.AssetQuality,
		Type:           entities.Exclude,
		FromAccount:    "Goodwill",
		ToAccount:      "",
		PercentageMode: percentageAbsent,
		AmountSource:   amountOverlayAmount,
		// Reasoning sourced from the OverlaySpec at projection time; the
		// formatter parameter is unused here because the closure is
		// rebuilt per-call below in adjustmentsFromLedger.
	},
	// A2 intangible writedown — Restater with from_pre_state Percentage.
	"A2_intangible_writedown": {
		Category:       entities.AssetQuality,
		Type:           entities.Writedown,
		FromAccount:    "IntangibleAssets",
		ToAccount:      "IntangibleWritedown",
		PercentageMode: percentageFromPreState,
		PreStateKey:    "original_OtherIntangibles",
		AmountSource:   amountLedgerDeltaAbs,
		Reasoning: func(amount, _ /*denom*/, _ /*pct*/ float64) string {
			// A2's legacy translator copied LedgerEntry.Reasoning verbatim,
			// which is the canonical "intangible_writedown: Applied X%
			// writedown to indefinite-lived intangibles (Y.Y% of assets)
			// per A2 rule" form. The closure below rebuilds the string
			// from amount-only inputs; we fall back to LedgerEntry.Reasoning
			// via the closure-pull in adjustmentsFromLedger.
			return "" // placeholder — overridden by the LedgerEntry.Reasoning pull-through.
		},
	},
	// A4 DTA valuation allowance — Restater with constant 50% Percentage.
	"A4_dta_valuation_allowance": {
		Category:       entities.AssetQuality,
		Type:           entities.AdjustmentTypeValuationAllowance,
		FromAccount:    "DeferredTaxAssets",
		ToAccount:      "ValuationAllowance",
		PercentageMode: percentageConstant,
		ConstantPct:    50.0,
		AmountSource:   amountLedgerDeltaAbs,
		Reasoning: func(_ /*amount*/, _ /*denom*/, _ /*pct*/ float64) string {
			return "" // placeholder — overridden by the LedgerEntry.Reasoning pull-through.
		},
	},
	// A5 inventory writedown — Restater with constant 40% Percentage.
	"A5_inventory_writedown": {
		Category:       entities.AssetQuality,
		Type:           entities.Writedown,
		FromAccount:    "Inventory",
		ToAccount:      "InventoryWritedown",
		PercentageMode: percentageConstant,
		ConstantPct:    40.0,
		AmountSource:   amountLedgerDeltaAbs,
		Reasoning: func(_ /*amount*/, _ /*denom*/, _ /*pct*/ float64) string {
			return "" // placeholder — overridden by the LedgerEntry.Reasoning pull-through.
		},
	},
	// B1 operating lease capitalization — OverlayEmitter, no Percentage.
	"B1_operating_lease_capitalization": {
		Category:       entities.LiabilityCompleteness,
		Type:           entities.TreatAsDebt,
		FromAccount:    "OperatingLeaseCommitments",
		ToAccount:      "InterestBearingDebt",
		PercentageMode: percentageAbsent,
		AmountSource:   amountOverlayAmount,
	},
	// B2 pension underfunding — OverlayEmitter, no Percentage.
	"B2_pension_underfunding": {
		Category:       entities.LiabilityCompleteness,
		Type:           entities.TreatAsDebt,
		FromAccount:    "PensionUnderfunding",
		ToAccount:      "InterestBearingDebt",
		PercentageMode: percentageAbsent,
		AmountSource:   amountOverlayAmount,
	},
	// B3 contingent liability — OverlayEmitter, no Percentage.
	"B3_contingent_liability": {
		Category:       entities.LiabilityCompleteness,
		Type:           entities.ProbabilityWeighted,
		FromAccount:    "ContingentLiabilities",
		ToAccount:      "EstimatedLiabilities",
		PercentageMode: percentageAbsent,
		AmountSource:   amountOverlayAmount,
	},
	// C1 restructuring charges — Restater with deliberate-zero Percentage
	// (per the legacy translator's inline "Percentage is not strictly
	// needed for downstream consumers" comment).
	"C1_restructuring_charges": {
		Category:       entities.EarningsNormalization,
		Type:           entities.Exclude,
		FromAccount:    "RestructuringCharges",
		ToAccount:      "NormalizedOperatingIncome",
		PercentageMode: percentageAbsent, // legacy translator leaves field unset
		AmountSource:   amountLedgerDeltaAbs,
		Reasoning: func(_ /*amount*/, _ /*denom*/, _ /*pct*/ float64) string {
			return "" // overridden by the LedgerEntry.Reasoning pull-through.
		},
	},
	// C2 asset sale gains — Restater with from_pre_state Percentage.
	"C2_asset_sale_gains": {
		Category:       entities.EarningsNormalization,
		Type:           entities.Exclude,
		FromAccount:    "AssetSaleGains",
		ToAccount:      "NormalizedOperatingIncome",
		PercentageMode: percentageFromPreState,
		PreStateKey:    "original_Revenue",
		AmountSource:   amountLedgerDeltaAbs,
		Reasoning: func(amount, _ /*denom*/, _ /*pct*/ float64) string {
			return fmt.Sprintf("Excluded asset sale gains of $%.1fM from operating income", amount/1000000)
		},
	},
	// C3 litigation settlements — Restater with from_pre_state Percentage.
	"C3_litigation_settlements": {
		Category:       entities.EarningsNormalization,
		Type:           entities.Exclude,
		FromAccount:    "LitigationSettlements",
		ToAccount:      "NormalizedOperatingIncome",
		PercentageMode: percentageFromPreState,
		PreStateKey:    "original_Revenue",
		AmountSource:   amountLedgerDeltaAbs,
		Reasoning: func(amount, _ /*denom*/, pct float64) string {
			return fmt.Sprintf("Excluded litigation settlements of $%.1fM (%.1f%% of revenue)", amount/1000000, pct)
		},
	},
	// C4 stock-based compensation — FlagEmitter with from_pre_state
	// Percentage. AmountSource reads from SkipMetrics["sbc_amount"]
	// because the LedgerEntry's DeltaAmount is empty by FlagEmitter
	// role contract.
	"C4_stock_compensation": {
		Category:       entities.EarningsNormalization,
		Type:           entities.Reclassify,
		FromAccount:    "StockBasedCompensation",
		ToAccount:      "OperatingExpenses",
		PercentageMode: percentageFromPreState,
		PreStateKey:    "original_Revenue",
		AmountSource:   amountSkipMetricsSBCAmount,
		Reasoning: func(amount, _ /*denom*/, _ /*pct*/ float64) string {
			return fmt.Sprintf("Reclassified stock-based compensation of $%.1fM for dilution analysis", amount/1000000)
		},
	},
	// C5 derivative gains/losses — Restater with from_pre_state Percentage.
	// Branch-divergent sign handling collapses to abs(DeltaAmount).
	"C5_derivative_gains_losses": {
		Category:       entities.EarningsNormalization,
		Type:           entities.Exclude,
		FromAccount:    "DerivativeGainsLosses",
		ToAccount:      "NormalizedOperatingIncome",
		PercentageMode: percentageFromPreState,
		PreStateKey:    "original_Revenue",
		AmountSource:   amountLedgerDeltaAbs,
		Reasoning: func(amount, _ /*denom*/, _ /*pct*/ float64) string {
			return fmt.Sprintf("Excluded derivative gains/losses of $%.1fM from operating income", amount/1000000)
		},
	},
	// C6 capitalized interest — Restater (Reclassify type) with from_pre_state.
	"C6_capitalized_interest": {
		Category:       entities.EarningsNormalization,
		Type:           entities.Reclassify,
		FromAccount:    "CapitalizedInterest",
		ToAccount:      "InterestExpense",
		PercentageMode: percentageFromPreState,
		PreStateKey:    "original_Revenue",
		AmountSource:   amountLedgerDeltaAbs,
		Reasoning: func(amount, _ /*denom*/, _ /*pct*/ float64) string {
			return fmt.Sprintf("Reclassified capitalized interest of $%.1fM to interest expense", amount/1000000)
		},
	},
	// C7 working-capital window-dressing and the flag-only review pair
	// (A-RD, A-CapSW) intentionally have no rows — their legacy
	// translators emit no entities.Adjustment record (only Flags). The
	// projection helper short-circuits on the missing key WITHOUT a
	// WARN log because these AdjusterIDs are KNOWN-no-emission. Tracked
	// in the `knownNoEmission` set below.
}

// knownNoEmission lists AdjusterIDs that the legacy translators
// deliberately produce no entities.Adjustment for. The projection
// helper short-circuits on these without logging.
//
//nolint:gochecknoglobals // immutable canonical-set sentinel.
var knownNoEmission = map[string]struct{}{
	"C7_working_capital":            {},
	"A-RD_capitalization_review":    {},
	"A-capitalized_software_review": {},
}

// adjustmentsFromLedger derives the public entities.Adjustment audit-trail
// from the native LedgerEntry + OverlaySpec slices on cleaned
// FinancialData. Replaces the per-rule *AdjusterOutputToLegacyResult
// translator chain.
//
// Iteration order: ledger ordering IS the contract (asset → liability →
// earnings; within each category, rule-iteration order). The projection
// preserves that ordering by walking `ledger` in its native sequence.
//
// Emission predicate per entry:
//   - Fired:false → SKIP (the FlagEmitter family is special-cased below).
//   - AdjusterID unknown → SKIP silently (no logger dependency at this
//     layer; defensive pin
//     TestAdjustmentsProjection_HandlesUnknownAdjusterID).
//   - AdjusterID in knownNoEmission → SKIP silently (legacy parity).
//   - C4 special case: a Fired:false LedgerEntry whose SkipMetrics
//     carries "sbc_amount" AND whose AdjusterID is the C4 constant
//     IS the FlagEmitter firing signal. We emit when sbc_amount > 0.
//
// Spec: docs/refactoring/spec/dc1-phase-5-followup-percentage-decision.md §4.4 / §4.5.
func adjustmentsFromLedger(
	ledger entities.AdjustmentLedger,
	overlays []entities.OverlaySpec,
	perRuleMeta map[string]ruleMeta,
) []entities.Adjustment {
	out := make([]entities.Adjustment, 0, len(ledger))

	for _, entry := range ledger {
		// Known-no-emission rules: legacy parity — never produce an
		// entities.Adjustment record.
		if _, skip := knownNoEmission[entry.AdjusterID]; skip {
			continue
		}

		// Resolve meta. Unknown AdjusterID → silently skip; the basket-
		// parity test surfaces drifts; production callers see no
		// regression for absent-meta rules (defensive — should not
		// happen in practice).
		meta, ok := perRuleMeta[entry.AdjusterID]
		if !ok {
			continue
		}

		// C4 (FlagEmitter) — Fired:false on the fire path, with the
		// populated Flags slice as the firing signal. The projection
		// reads sbc_amount from SkipMetrics to detect the fire (a
		// Fired:false entry with sbc_amount > 0 = fired).
		if meta.AmountSource == amountSkipMetricsSBCAmount {
			sbcAmount, hasSBC := entry.SkipMetrics["sbc_amount"]
			if !hasSBC || sbcAmount <= 0 {
				continue
			}
		} else if !entry.Fired {
			// Standard (Restater + OverlayEmitter) — Fired:true is the
			// firing signal.
			continue
		}

		// Resolve Amount.
		var amount float64
		switch meta.AmountSource {
		case amountLedgerDeltaAbs:
			amount = math.Abs(entry.DeltaAmount)
		case amountOverlayAmount:
			amount = overlayAmountByID(overlays, entry.AdjusterID)
		case amountSkipMetricsSBCAmount:
			amount = entry.SkipMetrics["sbc_amount"]
		}

		// Resolve Percentage.
		var (
			percentage  float64
			denominator float64
		)
		switch meta.PercentageMode {
		case percentageAbsent:
			percentage = 0 // Go zero value — omitted from JSON via `omitempty`.
		case percentageConstant:
			percentage = meta.ConstantPct
		case percentageFromPreState:
			// Explicit presence check (NOT just `> 0`): a MISSING pre-state
			// key and a legitimate zero denominator must be distinguishable.
			// Without the `ok` guard a future dropped capture would silently
			// emit Percentage=0 in the public API — the exact Path-(b)
			// degradation Path (a) exists to prevent. Capture coverage for
			// every percentageFromPreState rule is pinned by
			// TestPreStateCapture_OnFiredLedgerEntries.
			if d, ok := entry.SkipMetrics[meta.PreStateKey]; ok && d > 0 {
				denominator = d
				percentage = (amount / denominator) * 100
			} // else zero — matches the legacy `if originalRevenue > 0` guard.
		}

		// Resolve Reasoning. For A1/B1/B2/B3 (OverlayEmitter family),
		// the per-rule Reasoning lives on the OverlaySpec. For
		// A2/A4/A5/C1 the legacy translator copied LedgerEntry.Reasoning
		// verbatim. For C2/C3/C4/C5/C6 the legacy translator built its
		// own string via Sprintf — re-implemented in the per-rule
		// Reasoning formatter above.
		var reasoning string
		switch meta.AmountSource {
		case amountOverlayAmount:
			reasoning = reasoningFromOverlay(overlays, entry.AdjusterID)
		default:
			if meta.Reasoning != nil {
				reasoning = meta.Reasoning(amount, denominator, percentage)
			}
			if reasoning == "" {
				// Pull-through: A2/A4/A5/C1 — the legacy translator copied
				// the LedgerEntry's own Reasoning. Honors §4.7 option-a
				// (single canonical source) for these rules without
				// touching the Apply*-side emission.
				reasoning = entry.Reasoning
			}
		}

		// Adjustment.ID and .Timestamp: legacy translators used
		// `time.Now()` at projection time. Mirror that contract so the
		// non-deterministic fields are excluded from the basket-parity
		// golden but ship the same shape (numeric ID, non-zero
		// Timestamp). The per-rule prefix is preserved for grep parity.
		ts := time.Now()

		out = append(out, entities.Adjustment{
			ID:          fmt.Sprintf("%s_%d", entry.AdjusterID, ts.UnixNano()),
			RuleID:      entry.RuleID,
			Category:    meta.Category,
			Type:        meta.Type,
			Amount:      amount,
			FromAccount: meta.FromAccount,
			ToAccount:   meta.ToAccount,
			Percentage:  percentage,
			Reasoning:   reasoning,
			Applied:     true,
			Timestamp:   ts,
		})
	}
	return out
}

// overlayAmountByID finds the OverlaySpec whose OverlayID matches the
// given AdjusterID and returns its Amount. Returns 0 when not found
// (defensive — should not happen for a correctly-paired
// LedgerEntry+OverlaySpec emission).
func overlayAmountByID(overlays []entities.OverlaySpec, id string) float64 {
	for _, ov := range overlays {
		if ov.OverlayID == id {
			return ov.Amount
		}
	}
	return 0
}
