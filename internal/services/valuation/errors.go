package valuation

import (
	"errors"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// Sentinel errors for the valuation service.
// These allow callers (e.g., HTTP handlers) to classify failures
// with errors.Is() instead of fragile string matching.
var (
	// ErrTickerNotFound indicates the ticker does not exist in any data source.
	ErrTickerNotFound = errors.New("ticker not found")

	// ErrInsufficientData indicates there is not enough financial data
	// to perform a reliable valuation.
	ErrInsufficientData = errors.New("insufficient data")

	// ErrModelNotApplicable indicates neither the standard DCF model nor any
	// alternative model (DDM, FFO, revenue multiple) could produce a result.
	ErrModelNotApplicable = errors.New("model not applicable")

	// ErrForeignPrivateIssuer indicates SEC EDGAR returned company facts using
	// a non-US-GAAP taxonomy (typically `ifrs-full` from a Form 20-F filing
	// by tickers like TSM, ASML, NVO, AZN, SAP, BABA). The HTTP layer maps
	// this to 422 FOREIGN_PRIVATE_ISSUER_UNSUPPORTED — distinguished from
	// generic ErrInsufficientData so users can tell "no data available" apart
	// from "data exists in a format we don't yet parse".
	//
	// Once Phase B of docs/refactoring/ifrs-foreign-private-issuer-support-spec.md
	// ships, the parser will read IFRS data successfully and this sentinel
	// only fires for taxonomies still outside our coverage (JGAAP, K-IFRS).
	ErrForeignPrivateIssuer = errors.New("foreign private issuer: ifrs-full taxonomy not yet supported")

	// errFallbackToDCF is an internal signal (unexported) indicating the primary
	// alternative model failed but the company has positive OI, so the caller
	// should continue with the standard DCF path.
	errFallbackToDCF = errors.New("primary model failed; falling back to DCF")
)

// hasCompanyFactsNotFoundError returns true when any per-source FetchError in
// the list wraps ports.ErrCompanyFactsNotFound. Used by the valuation service
// to distinguish "ticker unknown" from "CIK resolved but SEC has no XBRL
// facts" (clinical-stage biotechs, pre-revenue companies) when deciding
// between ErrTickerNotFound (→ HTTP 404) and ErrInsufficientData (→ HTTP 422).
func hasCompanyFactsNotFoundError(errs []entities.FetchError) bool {
	for i := range errs {
		if errors.Is(errs[i].RawErr, ports.ErrCompanyFactsNotFound) {
			return true
		}
	}
	return false
}

// hasForeignPrivateIssuerError returns true when any per-source FetchError in
// the list wraps ports.ErrForeignPrivateIssuer. Used by the valuation service
// to distinguish "20-F filer with ifrs-full taxonomy" (→ HTTP 422
// FOREIGN_PRIVATE_ISSUER_UNSUPPORTED) from generic missing-companyfacts
// cases (→ HTTP 422 INSUFFICIENT_DATA). Must be checked BEFORE
// hasCompanyFactsNotFoundError because the FPI sentinel is more specific.
func hasForeignPrivateIssuerError(errs []entities.FetchError) bool {
	for i := range errs {
		if errors.Is(errs[i].RawErr, ports.ErrForeignPrivateIssuer) {
			return true
		}
	}
	return false
}
