package datacleaner

import "github.com/midas/dcf-valuation-api/internal/core/entities"

// nativeFired is the orchestrator's per-category firing-signal predicate
// for applyActiveAdjustments (DC-1 Phase 5 P5-C3 followup, post-gpt-5.5
// HIGH-1 review finding).
//
// Returns true when the category arm actually fired a rule. Used to gate
// `totalRulesApplied += len(rules)` and the append of per-rule
// `result.Adjustments` / `result.Flags` onto the orchestrator's
// `allAdjustments` / `allFlags` accumulators.
//
// FIRING SEMANTICS PER ROLE (spec §3.4 + Phase 2 implementation closeout):
//   - Restater adjusters (A2/A4/A5/C1/C2/C3/C5/C6) emit one Fired:true
//     LedgerEntry per fire. SKIP paths emit a Fired:false LedgerEntry
//     with SkipReason/SkipMetrics for diagnostic observability — these
//     are NOT a category fire and must NOT inflate totalRulesApplied.
//   - OverlayEmitter adjusters (A1/B1/B2/B3) emit one OverlaySpec per
//     fire. Skip paths emit a Fired:false LedgerEntry only — they do
//     NOT emit an OverlaySpec. So the presence of any OverlaySpec is
//     unconditionally a fire signal.
//   - FlagEmitter adjusters (C4/C7 + the two A-flag reviews) emit at
//     least one Flag per fire. Skip paths emit no Flag. So any non-
//     empty Flags slice is a fire signal.
//
// Why a helper, not an inline predicate at three call sites: the inline
// form `len(NativeLedgerEntries) > 0 || ...` shipped in P5-C3 was
// behaviorally NOT equivalent to the legacy `XResult.Applied` bool —
// when an adjuster's outer applicability check passed but its inner
// Apply skipped (e.g. A1 with goodwill < 5% of TotalAssets), the
// Fired:false diagnostic LedgerEntry made the inline predicate fire,
// inflating totalRulesApplied → result.RulesApplied → pipeline
// summary.TotalRulesApplied. The helper filters LedgerEntries on
// e.Fired==true; overlays + flags remain valid as-is.
//
// Pinned by TestApplyActiveAdjustments_FiringSignalParity_* (incl.
// the A1-applicable-but-skipped regression fixture).
func nativeFired(entries []entities.LedgerEntry, overlays []entities.OverlaySpec, flags []entities.Flag) bool {
	if len(overlays) > 0 || len(flags) > 0 {
		return true
	}
	for _, e := range entries {
		if e.Fired {
			return true
		}
	}
	return false
}
