package calclog_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/observability/calclog"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
)

// buildObserverCtx creates an observer-backed logger, injects it into a context,
// and returns both the context and the log sink for assertions.
func buildObserverCtx(level zapcore.Level) (context.Context, *observer.ObservedLogs) {
	core, logs := observer.New(level)
	l := zap.New(core)
	return logctx.Inject(context.Background(), l), logs
}

// buildEmitter is a helper to construct a calclog.Emitter from a TraceCalculations flag.
func buildEmitter(traceInfo bool) *calclog.Emitter {
	cfg := &config.Config{
		Logging: config.LoggingConfig{
			TraceCalculations: traceInfo,
		},
	}
	return calclog.NewEmitter(cfg)
}

// TestEmitter_TraceEnabled verifies that Emit logs at Info level when
// TraceCalculations is true, and that the "stage" and "event" fields are
// present on every emitted entry.
func TestEmitter_TraceEnabled(t *testing.T) {
	// Observer at Debug level so it captures both Debug and Info.
	ctx, logs := buildObserverCtx(zapcore.DebugLevel)
	emitter := buildEmitter(true)

	emitter.Emit(ctx, "dcf_wacc", zap.Float64("wacc", 0.08))

	require.Equal(t, 1, logs.Len(), "expected exactly one log entry")
	entry := logs.All()[0]

	assert.Equal(t, zapcore.InfoLevel, entry.Level, "traceInfo=true must emit at Info level")
	assert.Equal(t, "dcf_wacc", fieldValue(t, entry, "stage"))
	assert.Equal(t, "calc", fieldValue(t, entry, "event"))
	assert.Equal(t, 0.08, fieldFloat(t, entry, "wacc"))
}

// TestEmitter_TraceDisabled verifies that Emit logs at Debug level when
// TraceCalculations is false.
func TestEmitter_TraceDisabled(t *testing.T) {
	ctx, logs := buildObserverCtx(zapcore.DebugLevel)
	emitter := buildEmitter(false)

	emitter.Emit(ctx, "growth_blend", zap.Float64("growth", 0.05))

	require.Equal(t, 1, logs.Len(), "expected exactly one log entry")
	entry := logs.All()[0]

	assert.Equal(t, zapcore.DebugLevel, entry.Level, "traceInfo=false must emit at Debug level")
	assert.Equal(t, "growth_blend", fieldValue(t, entry, "stage"))
	assert.Equal(t, "calc", fieldValue(t, entry, "event"))
}

// TestEmitter_StageFieldPresent verifies the "stage" field is present on every
// emitted log entry regardless of the traceInfo flag.
func TestEmitter_StageFieldPresent(t *testing.T) {
	tests := []struct {
		name      string
		traceInfo bool
		stage     string
	}{
		{"trace_enabled", true, "terminal_value"},
		{"trace_disabled", false, "discount_rate"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, logs := buildObserverCtx(zapcore.DebugLevel)
			emitter := buildEmitter(tc.traceInfo)

			emitter.Emit(ctx, tc.stage)

			require.Equal(t, 1, logs.Len())
			assert.Equal(t, tc.stage, fieldValue(t, logs.All()[0], "stage"))
		})
	}
}

// TestEmitter_EmptyStageBecomesUnknown verifies that an empty stage string is
// replaced with "unknown" and no panic occurs.
func TestEmitter_EmptyStageBecomesUnknown(t *testing.T) {
	ctx, logs := buildObserverCtx(zapcore.DebugLevel)
	emitter := buildEmitter(true)

	// Must not panic; stage should fall back to "unknown"
	emitter.Emit(ctx, "")

	require.Equal(t, 1, logs.Len(), "should still emit one log entry")
	assert.Equal(t, "unknown", fieldValue(t, logs.All()[0], "stage"),
		"empty stage must fall back to 'unknown'")
}

// TestEmitter_DebugLevelNotCapturedAtInfoObserver verifies that when
// traceInfo=false the entry is Debug and an Info-only observer does NOT
// capture it (confirming actual level gating).
func TestEmitter_DebugLevelNotCapturedAtInfoObserver(t *testing.T) {
	// Observer set at Info level — should filter out Debug entries.
	ctx, logs := buildObserverCtx(zapcore.InfoLevel)
	emitter := buildEmitter(false) // emits at Debug

	emitter.Emit(ctx, "hidden_stage")

	assert.Equal(t, 0, logs.Len(), "Debug entry must be filtered by Info-level observer")
}

// TestEmitter_MultipleFieldsPreserved verifies that additional fields passed
// to Emit appear in the log entry alongside the built-in stage/event fields.
func TestEmitter_MultipleFieldsPreserved(t *testing.T) {
	ctx, logs := buildObserverCtx(zapcore.DebugLevel)
	emitter := buildEmitter(true)

	emitter.Emit(ctx, "fcf_projection",
		zap.Int("year", 3),
		zap.Float64("fcf", 500_000.0),
		zap.String("ticker", "AAPL"),
	)

	require.Equal(t, 1, logs.Len())
	entry := logs.All()[0]
	assert.Equal(t, "fcf_projection", fieldValue(t, entry, "stage"))
	assert.Equal(t, "calc", fieldValue(t, entry, "event"))
	assert.NotEmpty(t, entry.ContextMap()["year"])
	assert.NotEmpty(t, entry.ContextMap()["fcf"])
	assert.Equal(t, "AAPL", entry.ContextMap()["ticker"])
}

// TestEmitter_NoopWhenNoLoggerInContext verifies that Emit does not panic when
// the context carries no logger (falls back to nop).
func TestEmitter_NoopWhenNoLoggerInContext(t *testing.T) {
	// Plain background context has no logger — should not panic.
	emitter := buildEmitter(true)
	emitter.Emit(context.Background(), "nop_stage")
}

// ---------------------------------------------------------------------------
// Field extraction helpers
// ---------------------------------------------------------------------------

// fieldValue extracts a string field value from an observed log entry.
func fieldValue(t *testing.T, entry observer.LoggedEntry, key string) string {
	t.Helper()
	v, ok := entry.ContextMap()[key]
	require.True(t, ok, "expected field %q to be present in log entry", key)
	s, ok := v.(string)
	require.True(t, ok, "expected field %q to be a string, got %T", key, v)
	return s
}

// fieldFloat extracts a float64 field value from an observed log entry.
func fieldFloat(t *testing.T, entry observer.LoggedEntry, key string) float64 {
	t.Helper()
	v, ok := entry.ContextMap()[key]
	require.True(t, ok, "expected field %q to be present in log entry", key)
	f, ok := v.(float64)
	require.True(t, ok, "expected field %q to be a float64, got %T", key, v)
	return f
}
