package adjustments

import (
	"context"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// applyLedgerComponentDeltas applies each fired Restater-role LedgerEntry's
// signed DeltaAmount to the named COMPONENT field on working, per DC-1
// Phase 4 §8.2.1 Option A.
//
// This replaces the per-rule dispatcher dual-writes that Phase 4 deletes.
// Before Phase 4 each ProcessXAdjustments switch arm performed two mutations
// after Apply returned: a component mutation (e.g. data.OtherIntangibles -=
// writedown) AND an umbrella mutation (e.g. data.TotalAssets -= writedown).
// Phase 4 keeps the COMPONENT mutation (so the post-clean entity's component
// fields still carry the restated value, which cleaneddata.Restated() seeds
// from per the Phase 3 followup HIGH-1 reducer) but drops the UMBRELLA
// mutation entirely — umbrellas are recomputed from components inside
// Restated() at the view level.
//
// Consequence (documented transitional state): after Phase 4 the post-clean
// *FinancialData's umbrella fields (TotalAssets, TotalDebt, ...) may be
// INCOHERENT relative to their components. No Phase 4 consumer reads the
// umbrellas off the entity directly — they read them via the view accessors,
// which recompute. See spec §8.2.1.
//
// Entries with an empty Component are skipped: OverlayEmitters (A1 goodwill,
// B1 lease, B2 pension, B3 contingent) carry their monetary effect on an
// OverlaySpec, not a component DeltaAmount, and FlagEmitters (C4, C7) carry
// no monetary delta at all. Their effects are surfaced through Overlays /
// Flags and applied at the view level by InvestedCapital().
//
// ctx is accepted as the first parameter per Go convention and the DC-1
// Phase 3 ctx-threading contract; it is currently opaque (no deadline /
// span read), matching the Apply* methods.
func applyLedgerComponentDeltas(ctx context.Context, working *entities.FinancialData, out AdjusterOutput) {
	_ = ctx
	if working == nil {
		return
	}
	for _, e := range out.LedgerEntries {
		if !e.Fired || e.Component == "" {
			continue
		}
		switch e.Component {
		case "OtherIntangibles":
			working.OtherIntangibles += e.DeltaAmount
		case "Inventory":
			working.Inventory += e.DeltaAmount
		case "DeferredTaxAssets":
			working.DeferredTaxAssets += e.DeltaAmount
		case "NormalizedOperatingIncome":
			working.NormalizedOperatingIncome += e.DeltaAmount
		case "InterestExpense":
			working.InterestExpense += e.DeltaAmount
		}
	}
}
