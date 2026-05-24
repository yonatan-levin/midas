package cleaneddata

import "github.com/midas/dcf-valuation-api/internal/core/entities"

// CleanedFinancialData wraps a post-clean *entities.FinancialData together
// with its AdjustmentLedger and Overlays and exposes three semantically-
// distinct views over the same underlying entity: AsReported, Restated,
// and InvestedCapital. Views are computed on first access and memoized
// on the struct.
//
// Phase 3 invariant: NO production consumer reads from CleanedFinancialData
// yet. The accessor surface exists so Phase 4 can migrate one consumer at
// a time without further entity-shape changes.
//
// raw is NEVER mutated by accessor calls. View construction copies values
// into new FinancialDataView records.
type CleanedFinancialData struct {
	raw *entities.FinancialData

	// Memoized views. nil = not yet computed.
	asReported  *FinancialDataView
	restated    *FinancialDataView
	investedCap *FinancialDataView
}

// New constructs a CleanedFinancialData around the cleaner's working copy.
//
// Caller MUST NOT mutate the input *FinancialData after the call returns;
// accessor caching assumes the underlying entity is stable.
//
// nil raw is acceptable — accessors handle it by returning a zero
// FinancialDataView with the appropriate ViewKind. The zero view is a
// useful sentinel for callers that need to distinguish "no data available"
// from "all-zero data" via the ViewKind alone.
func New(raw *entities.FinancialData) *CleanedFinancialData {
	return &CleanedFinancialData{raw: raw}
}

// Raw returns the underlying *entities.FinancialData. Intended for the
// migration window only — Phase 4 consumers will read views directly.
// Returning the entity rather than a copy keeps the migration cheap;
// callers MUST treat it as read-only.
func (c *CleanedFinancialData) Raw() *entities.FinancialData {
	if c == nil {
		return nil
	}
	return c.raw
}
