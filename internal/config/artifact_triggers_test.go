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
	// HIGH-G follow-up: nil-safety must also hold when the always knob
	// is the trigger that would otherwise produce a Warn line.
	assert.NotPanics(t, func() {
		ValidateArtifactTriggers(ArtifactTriggers{Always: true}, nil)
	}, "nil logger must be a no-op for the always-on Warn path too")
}

// TestConfig_AlwaysOn_WarnsAtBoot pins REVIEWER HIGH-G (Phase 2.C
// follow-up). When the always knob is flipped on, the boot log MUST
// surface a Warn line so the operator has an in-process reminder it's
// still active — combined with HIGH-F's suppression of the per-request
// promoted Info line for trigger=always, the operator otherwise has
// ZERO in-process signal between flipping the knob and the disk filling.
//
// The Warn must:
//   - Be at WARN level (not Info) so it stands out in a default Info
//     deployment and isn't lost in startup noise.
//   - Carry a stable greppable identifier so runbooks can teach grep.
//   - Include the operator-facing effect description so the human reading
//     the boot log understands the cost without consulting docs.
//   - Echo the on_error_also_active and quality_flag_threshold fields so
//     the operator sees the FULL trigger picture in one line.
func TestConfig_AlwaysOn_WarnsAtBoot(t *testing.T) {
	core, recorded := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)

	triggers := ArtifactTriggers{Always: true}
	ValidateArtifactTriggers(triggers, logger)

	entries := recorded.FilterMessage("config.artifact_store.always_on_active").All()
	require.Len(t, entries, 1, "exactly one Warn line expected when Always=true")
	entry := entries[0]
	assert.Equal(t, zapcore.WarnLevel, entry.Level,
		"always-on signal MUST be Warn so operators see it on a default Info-level boot")

	// Walk the structured fields and assert all three diagnostic fields
	// are present. Avoids depending on internal zap field-list ordering.
	var sawEffect, sawOnError, sawQualityFlag bool
	for _, f := range entry.Context {
		switch f.Key {
		case "effect":
			assert.Contains(t, f.String, "every request will be bundled",
				"effect field must spell out the per-request capture cost")
			assert.Contains(t, f.String, "disk will fill",
				"effect field must surface the disk-fill caveat")
			sawEffect = true
		case "on_error_also_active":
			assert.False(t, f.Integer == 1,
				"on_error_also_active must echo the configured value (false in this test)")
			sawOnError = true
		case "quality_flag_threshold":
			assert.Equal(t, "", f.String,
				"quality_flag_threshold must echo the configured value (empty in this test)")
			sawQualityFlag = true
		}
	}
	assert.True(t, sawEffect, "Warn line must carry effect field")
	assert.True(t, sawOnError, "Warn line must carry on_error_also_active field")
	assert.True(t, sawQualityFlag, "Warn line must carry quality_flag_threshold field")
}

// TestConfig_AlwaysOff_NoWarn pins the negative case for HIGH-G: when
// Always=false (the default), the always-on Warn MUST NOT fire. Otherwise
// every default deployment would emit a noise line at startup, training
// operators to ignore the one signal that matters when the knob is
// actually on.
func TestConfig_AlwaysOff_NoWarn(t *testing.T) {
	core, recorded := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)

	ValidateArtifactTriggers(ArtifactTriggers{Always: false}, logger)

	assert.Empty(t, recorded.FilterMessage("config.artifact_store.always_on_active").All(),
		"Always=false (default) MUST NOT emit the always-on Warn — would train operators to ignore the one signal that matters")
}

// TestConfig_AlwaysOn_AndUnknownThreshold_BothWarn pins that the two
// validation branches are independent — a misconfigured deployment that
// flipped Always=true AND set an unknown quality_flag_threshold MUST
// see BOTH Warns on a single boot. Otherwise the operator fixing one
// problem would have to restart to discover the other, lengthening the
// "incident → fixed" loop.
func TestConfig_AlwaysOn_AndUnknownThreshold_BothWarn(t *testing.T) {
	core, recorded := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)

	triggers := ArtifactTriggers{
		Always:               true,
		QualityFlagThreshold: "warnng", // typo → threshold Warn fires
	}
	ValidateArtifactTriggers(triggers, logger)

	assert.NotEmpty(t, recorded.FilterMessage("config.artifact_store.quality_flag_threshold.unknown").All(),
		"threshold-typo Warn must still fire when Always is also active")
	assert.NotEmpty(t, recorded.FilterMessage("config.artifact_store.always_on_active").All(),
		"always-on Warn must fire even when threshold validation also has something to say")
}

// TestConfig_AlwaysOn_EchoesOtherTriggerFields pins the field-echoing
// contract: when other triggers ARE configured alongside Always, the Warn
// must echo their configured values. The operator reading the boot log
// then sees the FULL trigger picture in one line — they don't need to
// cross-reference YAML to understand precedence.
func TestConfig_AlwaysOn_EchoesOtherTriggerFields(t *testing.T) {
	core, recorded := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)

	triggers := ArtifactTriggers{
		Always:               true,
		OnError:              true,
		QualityFlagThreshold: "warning",
	}
	ValidateArtifactTriggers(triggers, logger)

	entries := recorded.FilterMessage("config.artifact_store.always_on_active").All()
	require.Len(t, entries, 1)
	entry := entries[0]

	var sawOnErrorTrue, sawThresholdWarning bool
	for _, f := range entry.Context {
		if f.Key == "on_error_also_active" && f.Integer == 1 {
			sawOnErrorTrue = true
		}
		if f.Key == "quality_flag_threshold" && f.String == "warning" {
			sawThresholdWarning = true
		}
	}
	assert.True(t, sawOnErrorTrue,
		"on_error_also_active MUST echo true when OnError is configured alongside Always")
	assert.True(t, sawThresholdWarning,
		"quality_flag_threshold MUST echo the configured value alongside the always-on signal")
}
