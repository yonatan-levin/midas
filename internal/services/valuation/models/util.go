// Package-scoped helpers shared across valuation models. Kept in a separate
// file (rather than tucked inside any single model's source) so future phases
// can reuse them without cross-file coupling. See spec §6.1 (RM-3) and §6.4
// (VAL-3 P3 forward-FFO) for the consumers.
package models

// avg returns the arithmetic mean of the given slice. Returns 0 if empty.
// Used by RM-3 (P1) for the projected-growth warning summary and reserved
// for VAL-3 P3 (P4) forward-projection warnings.
func avg(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}
