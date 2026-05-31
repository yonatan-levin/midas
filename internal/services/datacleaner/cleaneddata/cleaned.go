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
// GOROUTINE-SAFETY (hard contract — formalized in DC-1 Phase 5 P5-C5):
//
// A *CleanedFinancialData is REQUEST-LOCAL and READ-ONLY-via-views. Do NOT
// share a single instance across goroutines. The three accessor methods
// (AsReported, Restated, InvestedCapital) lazily populate cached
// *FinancialDataView pointers without locking; concurrent calls from
// multiple goroutines on the same *CleanedFinancialData would race on
// these caches.
//
// Why no sync.Once retrofit (Phase 5 spec §3.6): every current consumer
// runs on a single request goroutine. Adding sync.Once to the accessors
// would only make INITIALIZATION race-free — the accessors return a
// shared mutable *FinancialDataView pointer, so a sync.Once does not
// prevent a future caller from reading inconsistent state if they
// mutated the returned view. The real contract that must hold is
// "callers treat the returned view as read-only and do not share a
// *CleanedFinancialData across goroutines." This contract is documented
// here and at every accessor.
//
// If/when a parallel-read batch consumer lands (e.g., a batch valuation
// endpoint with fan-out across multiple tickers), that consumer is
// responsible for either: (a) constructing a SEPARATE
// *CleanedFinancialData per goroutine, OR (b) retrofitting sync.Once on
// the accessors AND adding immutability enforcement on the returned
// views. Choose (a) unless a benchmark demonstrates (b) is necessary.
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
