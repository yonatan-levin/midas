package datacleaner

// Phase 2.B — quality-flag severity ranking helpers.
//
// The cleaner's flag taxonomy (see internal/core/entities/data_cleaning.go)
// defines TWO parallel value sets for FlagSeverity that share the type
// alias `string`:
//
//   modern:  low / medium / high / critical
//   legacy:  info / warning / critical
//
// Both vocabularies coexist in production: the rules engine emits modern
// values, while createHardcodedRiskFlags and createRiskWarningFlags emit
// legacy values. Phase 2.B's threshold compare must rank both consistently
// so an operator setting threshold="warning" sees the same trigger
// behaviour regardless of which vocabulary the cleaner used for any given
// flag.
//
// The mapping below collapses both vocabularies onto a single integer
// scale:
//
//   info / low      -> 1
//   warning / medium -> 2
//   high            -> 3
//   critical        -> 4
//   anything else   -> 0  (never qualifies under any threshold)
//
// Empty string and unknown thresholds short-circuit countQualifyingFlags
// to 0, so a typo in config (e.g. "warnng") fails closed (no trigger
// firing) rather than open (firing on every request).

import (
	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// severityRank returns the numeric rank for a FlagSeverity value.
// Returns 0 for empty strings and unknown values; callers treat 0 as
// "never qualifies".
func severityRank(s entities.FlagSeverity) int {
	switch s {
	case entities.Info, entities.FlagSeverityLow:
		return 1
	case entities.Warning, entities.FlagSeverityMedium:
		return 2
	case entities.FlagSeverityHigh:
		return 3
	case entities.FlagSeverityCritical:
		// `entities.Critical` aliases this same value ("critical") — covered
		// by this single case. Listing both would be a compile-time
		// duplicate-case error, so we rely on the alias collapsing into one
		// branch. See entities.FlagSeverity declaration in
		// internal/core/entities/data_cleaning.go for the alias chain.
		return 4
	default:
		return 0
	}
}

// countQualifyingFlags returns the count of flags whose severity ranks
// at or above the named threshold. Empty threshold short-circuits to 0
// (trigger disabled). Unknown threshold strings also short-circuit to 0
// so misconfiguration fails closed rather than firing on every request.
//
// Pure function — no allocations, no I/O. Safe to call on the request
// hot path.
func countQualifyingFlags(flags []entities.Flag, threshold string) int {
	if threshold == "" {
		return 0
	}
	min := severityRank(entities.FlagSeverity(threshold))
	if min == 0 {
		// Unknown threshold value (typo, etc.). Fail closed.
		return 0
	}
	n := 0
	for _, f := range flags {
		if severityRank(f.Severity) >= min {
			n++
		}
	}
	return n
}
