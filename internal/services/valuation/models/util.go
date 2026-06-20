// Package-scoped helpers shared across valuation models. Kept in a separate
// file (rather than tucked inside any single model's source) so future phases
// can reuse them without cross-file coupling. See spec §6.1 (RM-3) and §6.4
// (VAL-3 P3 forward-FFO) for the consumers.
package models

import "strings"

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

// LookupByLongestPrefix is the SINGLE industry-code lookup core shared by the
// three multiple/cap-rate tables (SR-1 A10 — previously triplicated across
// crosscheck.LookupMultiple, FFOModel's subsector lookup, and
// RevenueMultipleModel.getMultiple, which had already drifted once before the
// W-4 determinism fix re-aligned them).
//
// Semantics (unchanged from all three originals):
//
//  1. Exact match on the upper-cased industry code wins.
//  2. Otherwise the LONGEST key matching at an underscore boundary wins —
//     `upper == code || strings.HasPrefix(upper, code+"_")`. Requiring the
//     `code+"_"` boundary prevents "TECHNOLOGY" from silently matching key
//     "TECH"; taking the longest match keeps the result deterministic
//     regardless of Go's randomized map iteration order (the W-4 invariant).
//  3. The reserved "default" key never participates in prefix matching —
//     callers apply their own default fallback on (0, false).
//
// Returns (value, true) on a hit and (0, false) on miss / nil-or-empty table
// / empty industry.
func LookupByLongestPrefix(table map[string]float64, industry string) (float64, bool) {
	if len(table) == 0 || industry == "" {
		return 0, false
	}
	upper := strings.ToUpper(industry)

	if v, ok := table[upper]; ok {
		return v, true
	}

	bestKey := ""
	bestVal := 0.0
	for code, v := range table {
		if code == "default" {
			continue
		}
		if upper == code || strings.HasPrefix(upper, code+"_") {
			if len(code) > len(bestKey) {
				bestKey = code
				bestVal = v
			}
		}
	}
	if bestKey != "" {
		return bestVal, true
	}
	return 0, false
}
