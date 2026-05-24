package adjustments

import (
	"context"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// Adjuster is the unified contract for every cleaner-side adjustment rule.
// Roles (Restater / OverlayEmitter / Hybrid / no-op) emerge from the shape of
// the returned AdjusterOutput, not from interface multiplication. Phase 2
// dual-write: implementations may mutate `working` in place exactly as today
// (preserving bit-for-bit downstream behavior); Phase 3 deletes the mutations
// once consumers read CleanedFinancialData views.
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md §"Adjuster output"
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md §3.1
type Adjuster interface {
	Name() string
	Apply(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error)
}

// AdjusterOutput is the return shape every Adjuster.Apply produces. Zero-value
// is a valid no-op. The orchestrator at service.go::applyActiveAdjustments
// appends LedgerEntries and Overlays onto FinancialData in execution order;
// adjusters MUST NOT read or write working.AdjustmentLedger / working.Overlays
// directly (spec §3.6 invariant 2).
type AdjusterOutput struct {
	LedgerEntries []entities.LedgerEntry
	Overlays      []entities.OverlaySpec
	Flags         []entities.Flag
}
