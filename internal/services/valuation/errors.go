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

	// ErrModelNotApplicable indicates the standard DCF model cannot value this
	// company (e.g., negative operating income). Industry-specific models
	// (DDM, FFO, revenue multiples) may handle it in a future phase.
	ErrModelNotApplicable = errors.New("model not applicable")
)
