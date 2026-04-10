package valuation

import "errors"

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

	// errFallbackToDCF is an internal signal (unexported) indicating the primary
	// alternative model failed but the company has positive OI, so the caller
	// should continue with the standard DCF path.
	errFallbackToDCF = errors.New("primary model failed; falling back to DCF")
)
