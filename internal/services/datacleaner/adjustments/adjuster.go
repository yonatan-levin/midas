package adjustments

import (
	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// SR-1 A3: the per-rule Adjuster adapter structs were deleted — production
// dispatches via the Apply* methods directly; the Adjuster interface had no
// production consumer.

// AdjusterOutput is the return shape every Apply* method produces. Zero-value
// is a valid no-op. The orchestrator at service.go::applyActiveAdjustments
// appends LedgerEntries and Overlays onto FinancialData in execution order;
// adjusters MUST NOT read or write working.AdjustmentLedger / working.Overlays
// directly (spec §3.6 invariant 2).
type AdjusterOutput struct {
	LedgerEntries []entities.LedgerEntry
	Overlays      []entities.OverlaySpec
	Flags         []entities.Flag
}
