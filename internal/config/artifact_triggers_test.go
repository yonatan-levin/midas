package config

// Phase 2.B post-launch (REVIEWER MEDIUM-1): tests for operator-input
// normalization and boot-time validation of the artifact-store auto-trigger
// thresholds. Together they pin the contract that case/whitespace typos
// resolve to canonical values, and that a typo not present in the known
// severity set is loudly surfaced on the boot log.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// TestConfig_QualityFlagThreshold_NormalizesCaseAndWhitespace pins that
// every common operator typo resolves to the canonical lowercase form.
// The downstream comparator (severityRank) only matches on lowercase
// constants, so a non-normalised "Warning" / "WARNING" / " warning " /
// "warning " all silently disable the trigger. Normalising at config-load
// time means the runtime never sees the un-canonical value.
func TestConfig_QualityFlagThreshold_NormalizesCaseAndWhitespace(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"sentence_case", "Warning", "warning"},
		{"all_caps", "WARNING", "warning"},
		{"leading_space", " warning", "warning"},
		{"trailing_space", "warning ", "warning"},
		{"both_sides_padding", "  Warning  ", "warning"},
		{"already_canonical", "warning", "warning"},
		{"empty_unchanged", "", ""},
		{"tab_whitespace", "\twarning\t", "warning"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			triggers := ArtifactTriggers{QualityFlagThreshold: tc.in}
			normalizeArtifactTriggers(&triggers)
			assert.Equal(t, tc.want, triggers.QualityFlagThreshold,
				"input %q must normalise to %q (got %q)", tc.in, tc.want, triggers.QualityFlagThreshold)
		})
	}
}

// TestConfig_QualityFlagThreshold_NormalizationIsIdempotent — running
// the normaliser twice in a row must not change the result. Guards
// against regressions that introduce a non-idempotent transformation
// (e.g. accidentally adding a prefix on every call).
func TestConfig_QualityFlagThreshold_NormalizationIsIdempotent(t *testing.T) {
	triggers := ArtifactTriggers{QualityFlagThreshold: "  Warning  "}
	normalizeArtifactTriggers(&triggers)
	first := triggers.QualityFlagThreshold
	normalizeArtifactTriggers(&triggers)
	assert.Equal(t, first, triggers.QualityFlagThreshold,
		"second normalisation must not change the value (idempotency)")
}

// TestConfig_QualityFlagThreshold_NilSafe pins the nil-receiver contract
// — defensive against a future refactor that constructs an
// ArtifactTriggers via reflection or fx and may pass nil through.
func TestConfig_QualityFlagThreshold_NilSafe(t *testing.T) {
	assert.NotPanics(t, func() {
		normalizeArtifactTriggers(nil)
	}, "normalizeArtifactTriggers(nil) must not panic")
}

// TestConfig_QualityFlagThreshold_WarnsOnUnknownValue captures the
// singleton-logger Warn line emitted at boot when an unknown severity
// value is configured. Operators discover misconfigurations from boot
// logs faster than from missing bundles during incidents.
//
// The value passed in is post-normalisation (the boot-time validator
// runs AFTER Load), so we pass the canonical lowercase typo directly.
func TestConfig_QualityFlagThreshold_WarnsOnUnknownValue(t *testing.T) {
	core, recorded := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)

	triggers := ArtifactTriggers{QualityFlagThreshold: "warnng"} // typo
	ValidateArtifactTriggers(triggers, logger)

	entries := recorded.All()
	require.Len(t, entries, 1, "exactly one Warn line expected for unknown threshold")
	entry := entries[0]
	assert.Equal(t, zapcore.WarnLevel, entry.Level, "must be Warn level so operators see it on boot")
	assert.Equal(t, "config.artifact_store.quality_flag_threshold.unknown", entry.Message,
		"log message must be a stable, greppable identifier")

	// Find the configured_value field — the typo MUST be in the log line so
	// an operator can immediately see what they typed wrong.
	var sawConfiguredValue, sawValidValues bool
	for _, f := range entry.Context {
		if f.Key == "configured_value" {
			assert.Equal(t, "warnng", f.String,
				"configured_value field must echo the typo verbatim")
			sawConfiguredValue = true
		}
		if f.Key == "valid_values" {
			sawValidValues = true
			// valid_values is stored by zap as zap.stringArray (which has
			// underlying type []string). Render the field via zap's
			// MapObjectEncoder so we exercise the same path the production
			// log line takes — avoids depending on internal type names.
			enc := zapcore.NewMapObjectEncoder()
			f.AddTo(enc)
			vals, ok := enc.Fields["valid_values"].([]any)
			require.True(t, ok, "valid_values must marshal to []any via the encoder; got %T", enc.Fields["valid_values"])
			// Spot-check a few canonical values are present so a typo in
			// the value list itself is caught.
			parts := make([]string, 0, len(vals))
			for _, v := range vals {
				parts = append(parts, fmt.Sprintf("%v", v))
			}
			joined := strings.Join(parts, ",")
			assert.Contains(t, joined, "warning",
				"valid_values must include 'warning' so operators see what they probably meant")
			assert.Contains(t, joined, "critical",
				"valid_values must include 'critical'")
		}
	}
	assert.True(t, sawConfiguredValue, "Warn line must carry configured_value field")
	assert.True(t, sawValidValues, "Warn line must carry valid_values field")
}

// TestConfig_QualityFlagThreshold_NoWarnOnEmpty — empty threshold is the
// default (= trigger disabled). It MUST NOT generate a boot Warn, otherwise
// every default deployment would emit a noise-line at startup.
func TestConfig_QualityFlagThreshold_NoWarnOnEmpty(t *testing.T) {
	core, recorded := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)

	triggers := ArtifactTriggers{QualityFlagThreshold: ""}
	ValidateArtifactTriggers(triggers, logger)

	assert.Empty(t, recorded.All(),
		"empty threshold (default = disabled) must NOT emit a boot Warn")
}

// TestConfig_QualityFlagThreshold_NoWarnOnKnownValue — every known
// severity is a valid threshold and MUST NOT trigger a Warn. Iterates
// the entities.KnownFlagSeverities slice so a future severity addition
// extends coverage automatically.
func TestConfig_QualityFlagThreshold_NoWarnOnKnownValue(t *testing.T) {
	known := knownSeverityValues()
	for v := range known {
		t.Run(v, func(t *testing.T) {
			core, recorded := observer.New(zapcore.WarnLevel)
			logger := zap.New(core)

			triggers := ArtifactTriggers{QualityFlagThreshold: v}
			ValidateArtifactTriggers(triggers, logger)

			assert.Empty(t, recorded.All(),
				"known severity %q must NOT emit a boot Warn", v)
		})
	}
}

// TestConfig_ValidateArtifactTriggers_NilLoggerIsSafe pins the
// defensive nil-logger contract so unit tests and partially-constructed
// servers don't panic.
func TestConfig_ValidateArtifactTriggers_NilLoggerIsSafe(t *testing.T) {
	assert.NotPanics(t, func() {
		ValidateArtifactTriggers(ArtifactTriggers{QualityFlagThreshold: "warnng"}, nil)
	}, "nil logger must be a no-op, not a panic")
}
