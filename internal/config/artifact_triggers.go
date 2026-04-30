package config

// Phase 2.B post-launch (REVIEWER MEDIUM-1): operator-input normalization
// + boot-time validation for the artifact-store auto-trigger thresholds.
//
// The trigger threshold is set by env var
// (LOGGING_ARTIFACT_STORE_TRIGGERS_QUALITY_FLAG_THRESHOLD=warning) or YAML
// (logging.artifact_store.triggers.quality_flag_threshold: warning). Without
// normalization, operator typos like "Warning", " warning", "WARNING" fail
// silently — severityRank sees an unknown FlagSeverity and short-circuits
// every request to count=0, disabling the trigger. The operator only learns
// the trigger never fired when the bundle they expected during an incident
// isn't on disk.
//
// We solve this in two layers:
//
//  1. Normalize: at config-load time, lower-case + trim every operator-set
//     threshold. The downstream comparator now sees the canonical lowercase
//     vocabulary regardless of how the operator typed it. Done in
//     normalizeArtifactTriggers (called by Load).
//
//  2. Validate at boot: surface a Warn line on the singleton logger when a
//     non-empty threshold doesn't match any known severity. The boot log
//     is the FIRST place an operator looks when a new env var doesn't
//     "stick" — surfacing the typo here turns a multi-day incident-time
//     mystery into a five-second boot-log search. Done in
//     ValidateArtifactTriggers (called from server boot, where the
//     singleton *zap.Logger is in scope).

import (
	"strings"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// normalizeArtifactTriggers canonicalises operator-set trigger string
// values. Mutates the receiver in place. Idempotent — safe to call
// multiple times. Currently only QualityFlagThreshold needs normalization;
// future severity-style fields should be normalised here too.
func normalizeArtifactTriggers(t *ArtifactTriggers) {
	if t == nil {
		return
	}
	t.QualityFlagThreshold = strings.ToLower(strings.TrimSpace(t.QualityFlagThreshold))
}

// knownSeverityValues is the set of valid FlagSeverity strings the
// QualityFlagThreshold env var/YAML key may be set to. Built from the
// canonical entities.KnownFlagSeverities slice so a future severity
// addition automatically propagates here without a manual edit.
//
// Returns lowercase strings to match the post-normalize comparison
// vocabulary.
func knownSeverityValues() map[string]struct{} {
	out := make(map[string]struct{}, len(entities.KnownFlagSeverities))
	for _, s := range entities.KnownFlagSeverities {
		out[strings.ToLower(string(s))] = struct{}{}
	}
	return out
}

// ValidateArtifactTriggers emits a Warn on the supplied logger when a
// non-empty QualityFlagThreshold doesn't match any known severity value.
// Empty (= disabled) is the default and is NOT warned about.
//
// MUST be called AFTER normalizeArtifactTriggers (which Load does
// automatically) so that case/whitespace typos resolve to canonical
// values BEFORE we decide whether to warn. Otherwise "Warning" would
// look like an unknown value when the user clearly meant a real one.
//
// Uses the singleton boot-time *zap.Logger by design — this runs during
// fx wire-up, before any HTTP request exists, so logctx-scoped logging
// is not appropriate (and not available).
//
// Nil-safe: nil logger silently no-ops (defensive — should never happen
// in production but keeps unit tests painless).
func ValidateArtifactTriggers(t ArtifactTriggers, logger *zap.Logger) {
	if logger == nil {
		return
	}
	if t.QualityFlagThreshold == "" {
		// Default = disabled. No warn.
		return
	}
	known := knownSeverityValues()
	if _, ok := known[t.QualityFlagThreshold]; ok {
		return
	}
	// Build a sorted list of valid values for the operator-facing message.
	// Avoids leaking map-iteration nondeterminism into log output (which
	// would show up as flaky test diffs and confused operators).
	valid := make([]string, 0, len(known))
	for k := range known {
		valid = append(valid, k)
	}
	// Stable order — sort.Strings would pull in `sort` for one call site.
	// A two-line bubble sort or strings.Join after a sort.Strings call
	// would be cleaner; use sort here for clarity.
	sortStrings(valid)
	logger.Warn("config.artifact_store.quality_flag_threshold.unknown",
		zap.String("configured_value", t.QualityFlagThreshold),
		zap.Strings("valid_values", valid),
		zap.String("effect", "trigger silently disabled — typo or unsupported severity; fix the env var / YAML key to enable"),
	)
}

// sortStrings is a tiny in-place sort to avoid pulling sort just for
// one tidy log message. Insertion sort is fine — N is at most 6.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
