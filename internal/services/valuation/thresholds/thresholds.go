// Package thresholds exposes the single source of truth for divergence
// thresholds used across cross-check sites (P/E, EV/EBITDA, P/FCF, NAV, P/BV).
// Lives in a leaf package so both `valuation` and `valuation/models` can
// import it without creating an import cycle.
package thresholds

// DeviationHigh is the upper-bound multiplier. Ratios above this are flagged.
const DeviationHigh = 2.0

// DeviationLow is the lower-bound multiplier. Ratios below this are flagged.
const DeviationLow = 0.5
