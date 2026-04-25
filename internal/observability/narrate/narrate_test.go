package narrate_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
	"github.com/midas/dcf-valuation-api/internal/observability/narrate"
)

// expectedPhases pins the closed 17-phase set against accidental string drift.
// If you add or remove a phase here, you MUST also update the spec
// (docs/refactoring/observability-narrative-and-artifacts-spec.md §5).
var expectedPhases = map[string]struct{}{
	"request.received":     {},
	"auth.resolved":        {},
	"ratelimit.checked":    {},
	"handler.entry":        {},
	"cache.lookup":         {},
	"fetch.fanout":         {},
	"fetch.sec":            {},
	"fetch.market":         {},
	"fetch.macro":          {},
	"clean.normalized":     {},
	"classify.industry":    {},
	"growth.estimated":     {},
	"wacc.computed":        {},
	"model.selected":       {},
	"valuation.computed":   {},
	"crosscheck.evaluated": {},
	"response.sent":        {},
}

// expectedOutcomes pins the closed outcome enum.
var expectedOutcomes = map[string]struct{}{
	"ok":       {},
	"fallback": {},
	"partial":  {},
	"skipped":  {},
	"error":    {},
}

// TestPhases_ClosedSet ensures every Phase* constant value is in
// the expected set, and nothing else exists. Runs as a table that walks
// the constants by their exported names (typed alias guards against typos).
func TestPhases_ClosedSet(t *testing.T) {
	cases := []narrate.Phase{
		narrate.PhaseRequestReceived,
		narrate.PhaseAuthResolved,
		narrate.PhaseRateLimitChecked,
		narrate.PhaseHandlerEntry,
		narrate.PhaseCacheLookup,
		narrate.PhaseFetchFanout,
		narrate.PhaseFetchSEC,
		narrate.PhaseFetchMarket,
		narrate.PhaseFetchMacro,
		narrate.PhaseCleanNormalized,
		narrate.PhaseClassifyIndustry,
		narrate.PhaseGrowthEstimated,
		narrate.PhaseWACCComputed,
		narrate.PhaseModelSelected,
		narrate.PhaseValuationComputed,
		narrate.PhaseCrosscheckEvaluated,
		narrate.PhaseResponseSent,
	}
	require.Len(t, cases, 17, "spec freezes the phase count at 17")

	seen := make(map[string]struct{}, len(cases))
	for _, p := range cases {
		_, ok := expectedPhases[string(p)]
		assert.True(t, ok, "unexpected phase value: %q", string(p))
		seen[string(p)] = struct{}{}
	}
	for want := range expectedPhases {
		_, ok := seen[want]
		assert.True(t, ok, "expected phase %q missing from constants", want)
	}
}

// TestOutcomes_ClosedSet ensures every Outcome* constant value is in
// the expected set, and nothing else exists.
func TestOutcomes_ClosedSet(t *testing.T) {
	cases := []narrate.Outcome{
		narrate.OutcomeOK,
		narrate.OutcomeFallback,
		narrate.OutcomePartial,
		narrate.OutcomeSkipped,
		narrate.OutcomeError,
	}
	require.Len(t, cases, 5, "spec freezes the outcome count at 5")

	for _, o := range cases {
		_, ok := expectedOutcomes[string(o)]
		assert.True(t, ok, "unexpected outcome value: %q", string(o))
	}
}

// TestEmitter_StandardFields verifies every emitted line carries event=narrate,
// phase, outcome, and (when set) ticker.
func TestEmitter_StandardFields(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)
	ctx := logctx.Inject(context.Background(), logger)

	e := narrate.NewEmitter(narrate.Config{Enabled: true, SampleRate: 1.0}, "req-1")
	e.WithTicker("AAPL")

	e.Emit(ctx, narrate.PhaseHandlerEntry, narrate.OutcomeOK, "options applied",
		zap.Int("override_count", 2),
	)

	entries := observed.All()
	require.Len(t, entries, 1, "exactly one narrate line emitted")
	got := entries[0]

	// Field-by-field assertions: closed-set test pinning the JSON contract.
	fields := got.ContextMap()
	assert.Equal(t, "narrate", fields["event"], "every line carries event=narrate")
	assert.Equal(t, "handler.entry", fields["phase"])
	assert.Equal(t, "ok", fields["outcome"])
	assert.Equal(t, "AAPL", fields["ticker"])
	assert.Equal(t, "options applied", fields["notes"])
	assert.EqualValues(t, 2, fields["override_count"])
}

// TestEmitter_NoTicker verifies ticker is omitted when not set (request.received
// fires before ticker is parsed).
func TestEmitter_NoTicker(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)
	ctx := logctx.Inject(context.Background(), logger)

	e := narrate.NewEmitter(narrate.Config{Enabled: true, SampleRate: 1.0}, "req-2")
	e.Emit(ctx, narrate.PhaseRequestReceived, narrate.OutcomeOK, "")

	require.Len(t, observed.All(), 1)
	fields := observed.All()[0].ContextMap()
	_, has := fields["ticker"]
	assert.False(t, has, "ticker key absent when emitter.WithTicker not called")
	_, hasNotes := fields["notes"]
	assert.False(t, hasNotes, "notes key absent when notes string is empty")
}

// TestEmitter_DisabledNoEmit verifies Enabled=false suppresses every line.
func TestEmitter_DisabledNoEmit(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)
	ctx := logctx.Inject(context.Background(), logger)

	e := narrate.NewEmitter(narrate.Config{Enabled: false, SampleRate: 1.0}, "req-3")
	for i := 0; i < 5; i++ {
		e.Emit(ctx, narrate.PhaseFetchSEC, narrate.OutcomeOK, "")
	}

	assert.Empty(t, observed.All(), "disabled emitter emits nothing")
	assert.False(t, e.Sampled(), "Sampled() reports false when disabled")
}

// TestEmitter_SampleRateZero verifies SampleRate=0 sampling never emits.
func TestEmitter_SampleRateZero(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)
	ctx := logctx.Inject(context.Background(), logger)

	e := narrate.NewEmitter(narrate.Config{Enabled: true, SampleRate: 0.0}, "req-4")
	e.Emit(ctx, narrate.PhaseValuationComputed, narrate.OutcomeOK, "")

	assert.Empty(t, observed.All())
	assert.False(t, e.Sampled())
}

// TestEmitter_SampleRateOneAlwaysEmits verifies SampleRate=1 always emits.
func TestEmitter_SampleRateOneAlwaysEmits(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)
	ctx := logctx.Inject(context.Background(), logger)

	e := narrate.NewEmitter(narrate.Config{Enabled: true, SampleRate: 1.0}, "req-5")
	for i := 0; i < 3; i++ {
		e.Emit(ctx, narrate.PhaseFetchSEC, narrate.OutcomeOK, "")
	}

	assert.Len(t, observed.All(), 3)
	assert.True(t, e.Sampled())
}

// TestEmitter_SamplingDeterministic verifies the same request_id always yields
// the same sampling decision (so a request is never half-told).
func TestEmitter_SamplingDeterministic(t *testing.T) {
	cfg := narrate.Config{Enabled: true, SampleRate: 0.5}

	// 100 emitters from the same id; all must agree on sampled state.
	var first bool
	for i := 0; i < 100; i++ {
		e := narrate.NewEmitter(cfg, "req-stable")
		if i == 0 {
			first = e.Sampled()
			continue
		}
		assert.Equal(t, first, e.Sampled(),
			"sampling decision must be deterministic for a given request_id")
	}
}

// TestEmitter_SamplingDistributes verifies SampleRate=0.5 actually gives a
// roughly even split across many request IDs (sanity, not a strict test).
func TestEmitter_SamplingDistributes(t *testing.T) {
	cfg := narrate.Config{Enabled: true, SampleRate: 0.5}
	in, out := 0, 0
	for i := 0; i < 1000; i++ {
		// Build a different request id per iteration. fmt.Sprintf is fine —
		// this is a one-shot test, not a hot path.
		rid := fmt.Sprintf("req-%d", i)
		e := narrate.NewEmitter(cfg, rid)
		if e.Sampled() {
			in++
		} else {
			out++
		}
	}
	// Tolerance: with 1000 trials at p=0.5 the std dev is ~16. ±15% is safe.
	assert.InDelta(t, 500, in, 150, "sampling should cluster around 50%%")
	assert.InDelta(t, 500, out, 150)
}

// TestEmitter_RedactDropsField verifies RedactFields drops matching keys.
func TestEmitter_RedactDropsField(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)
	ctx := logctx.Inject(context.Background(), logger)

	e := narrate.NewEmitter(narrate.Config{
		Enabled:      true,
		SampleRate:   1.0,
		RedactFields: []string{"client_ip_hash"},
	}, "req-redact")

	e.Emit(ctx, narrate.PhaseRequestReceived, narrate.OutcomeOK, "",
		zap.String("client_ip_hash", "abc123"),
		zap.String("method", "GET"),
	)

	require.Len(t, observed.All(), 1)
	fields := observed.All()[0].ContextMap()
	_, has := fields["client_ip_hash"]
	assert.False(t, has, "redacted field must be absent")
	assert.Equal(t, "GET", fields["method"], "non-redacted field must remain")
}

// TestEmitter_NilSafe verifies a nil emitter Emit is a no-op (defensive).
func TestEmitter_NilSafe(t *testing.T) {
	var e *narrate.Emitter
	// Should not panic.
	e.Emit(context.Background(), narrate.PhaseFetchSEC, narrate.OutcomeOK, "")
}

// TestFrom_NoEmitterContext verifies From returns a nop-emitter on miss
// (so callers never panic).
func TestFrom_NoEmitterContext(t *testing.T) {
	got := narrate.From(context.Background())
	assert.NotNil(t, got, "From must return a non-nil emitter even on miss")
	assert.False(t, got.Sampled(), "miss emitter is sampled out")

	// Should not panic.
	got.Emit(context.Background(), narrate.PhaseFetchSEC, narrate.OutcomeOK, "")
}

// TestFrom_NilContext is the nil-context defence.
func TestFrom_NilContext(t *testing.T) {
	got := narrate.From(nil) //nolint:staticcheck // intentionally nil
	require.NotNil(t, got)
	assert.False(t, got.Sampled())
}

// TestInjectFromRoundTrip verifies the Inject/From contract.
func TestInjectFromRoundTrip(t *testing.T) {
	e := narrate.NewEmitter(narrate.Config{Enabled: true, SampleRate: 1.0}, "req")
	ctx := narrate.Inject(context.Background(), e)
	assert.Same(t, e, narrate.From(ctx))
}

// TestEmitter_PayloadRoot verifies WithPayloadRoot/PayloadRoot round-trip.
func TestEmitter_PayloadRoot(t *testing.T) {
	e := narrate.NewEmitter(narrate.Config{Enabled: true, SampleRate: 1.0}, "req")
	assert.Empty(t, e.PayloadRoot())
	e.WithPayloadRoot("/tmp/bundle")
	assert.Equal(t, "/tmp/bundle", e.PayloadRoot())
}
