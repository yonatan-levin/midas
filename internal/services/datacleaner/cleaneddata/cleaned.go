package cleaneddata

import "github.com/midas/dcf-valuation-api/internal/core/entities"

// CleanedFinancialData wraps a post-clean *entities.FinancialData together
// with its AdjustmentLedger and Overlays and exposes three semantically-
// distinct views over the underlying entity: AsReported, Restated, and
// InvestedCapital. Views are computed on first access and memoized on the
// struct.
//
// The Phase 3 followup (HIGH-1 fix) separates the as-reported and restated
// inputs:
//
//   - asReportedSnapshot is the PRE-CLEAN input — captured before
//     CleanFinancialData runs any dispatcher dual-writes. AsReported()
//     reads from this snapshot so callers see the parser-stamped values
//     verbatim (preserving T2-BS-3 carve-outs and any other parser
//     idiosyncrasies).
//
//   - restated is the POST-CLEAN entity (the output of CleanFinancialData).
//     The dispatcher dual-writes have already applied every Restater-role
//     adjuster's component delta to its fields. Restated() seeds from this
//     entity and additionally applies LedgerEntry.EquityOffset and
//     LedgerEntry.TaxShieldDTA from the ledger — NOT LedgerEntry.DeltaAmount,
//     because that delta is already in the component fields via the dual-
//     write. Applying it again would double-count every Restater fire.
//
// Phase 3 invariant: NO production consumer reads from CleanedFinancialData
// yet. The accessor surface exists so Phase 4 can migrate one consumer at
// a time without further entity-shape changes.
//
// GOROUTINE-SAFETY: NOT goroutine-safe. The accessor methods (AsReported,
// Restated, InvestedCapital) lazily populate cached *FinancialDataView
// pointers without locking. Do NOT share a single *CleanedFinancialData
// across goroutines without external synchronization. Phase 3 / Phase 4
// consumers all run on a single request goroutine, which is sufficient
// for current use cases; a future parallel-read consumer (e.g., a batch
// valuation endpoint) would need a sync.Once retrofit on the three
// accessor methods. Tracked as a Phase 5 watch item — see
// docs/refactoring/spec/dc1-phase-3-followup-spec.md §4.6.
//
// Neither input *FinancialData is mutated by accessor calls. View
// construction copies values into new FinancialDataView records.
type CleanedFinancialData struct {
	asReportedSnapshot *entities.FinancialData
	restated           *entities.FinancialData

	// Memoized views. nil = not yet computed.
	asReportedView *FinancialDataView
	restatedView   *FinancialDataView
	investedCap    *FinancialDataView
}

// New constructs a CleanedFinancialData with explicit pre-clean (asReported)
// and post-clean (restated) *FinancialData inputs.
//
// Phase 3 followup (HIGH-1 fix): the signature replaces the single-entity
// New(raw) constructor. The production caller in
// service.CleanFinancialDataWithViews captures a pre-clean snapshot of the
// input and passes it alongside result.CleanedData. Test callers that
// synthesize a *FinancialData directly (no dispatcher run) pass the same
// pointer twice — the pre/post entities are identical in that scenario,
// so AsReported and Restated seed from the same values.
//
// Caller MUST NOT mutate either input after the call returns; accessor
// caching assumes both entities are stable.
//
// nil arguments are acceptable — accessors handle nil by returning a zero
// FinancialDataView with the appropriate ViewKind. The zero view is a
// useful sentinel for callers that need to distinguish "no data available"
// from "all-zero data" via the ViewKind alone.
func New(asReported, restated *entities.FinancialData) *CleanedFinancialData {
	return &CleanedFinancialData{
		asReportedSnapshot: asReported,
		restated:           restated,
	}
}

// Raw returns the underlying post-clean *entities.FinancialData. Intended
// for the migration window only — Phase 4 consumers will read views
// directly. Returning the entity rather than a copy keeps the migration
// cheap; callers MUST treat it as read-only.
func (c *CleanedFinancialData) Raw() *entities.FinancialData {
	if c == nil {
		return nil
	}
	return c.restated
}
