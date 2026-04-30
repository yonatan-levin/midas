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
	"context"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
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

// recordQualityFlagCount looks up the artifact bundle on ctx (if any),
// counts the qualifying flags at or above the bundle's configured severity
// threshold, and reports the count to the bundle via RecordQualityFlagCount.
//
// Single hook used by BOTH the cache-miss post-clean path AND the cache-hit
// short-circuit (REVIEWER HIGH-1 fix). Routing both call sites through the
// same helper guarantees that a cached-result return path can never silently
// drop the count and let the auto-on-quality-flag trigger dissolve.
//
// No-op when:
//   - no bundle is on ctx (the dominant production path when the trigger is
//     off — middleware only opens a deferred bundle when at least one auto-
//     trigger is configured),
//   - the bundle is nil (defensive),
//   - the bundle's threshold is empty (trigger disabled at config time).
//
// Cheap and concurrency-safe: artifact.From is a context lookup,
// QualityFlagThreshold is a struct-field read, and RecordQualityFlagCount
// uses atomic.Int64 internally.
func recordQualityFlagCount(ctx context.Context, flags []entities.Flag) {
	b := artifact.From(ctx)
	if b == nil {
		return
	}
	threshold := b.QualityFlagThreshold()
	if threshold == "" {
		// Skip the slice walk when the trigger is off. countQualifyingFlags
		// itself short-circuits on empty threshold, but the explicit gate
		// keeps intent obvious to readers and avoids the no-op call.
		return
	}
	b.RecordQualityFlagCount(countQualifyingFlags(flags, threshold))
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
