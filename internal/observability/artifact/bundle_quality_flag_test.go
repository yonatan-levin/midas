package artifact_test

// Phase 2.B — auto-on-quality-flag trigger unit tests for the artifact.Bundle.
//
// The bundle gets two new responsibilities for Phase 2.B:
//   - It exposes the configured quality-flag severity threshold so the data
//     cleaner can read it back from ctx and decide which flags qualify.
//   - It accepts a count of qualifying flags via RecordQualityFlagCount and
//     surfaces it via QualityFlagCount so the trace middleware can decide
//     whether to Promote with TriggerOnQualityFlag at request-end.
//
// These tests pin the bundle-level building blocks in isolation. The
// middleware integration is pinned by trace_test.go's Phase 2.B section.

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
)

// TestTrigger_OnQualityFlagConstant_Defined pins the wire value of the new
// trigger. Manifest tooling and ops dashboards grep for "on_quality_flag";
// a typo in a future refactor would silently break that contract.
func TestTrigger_OnQualityFlagConstant_Defined(t *testing.T) {
	assert.Equal(t, artifact.Trigger("on_quality_flag"), artifact.TriggerOnQualityFlag,
		"TriggerOnQualityFlag wire value must remain on_quality_flag")
}

// TestBundle_QualityFlagCount_DefaultsToZero pins the zero-value contract:
// a freshly-opened bundle reports zero qualifying flags. Tests downstream
// of this rely on it as the baseline.
func TestBundle_QualityFlagCount_DefaultsToZero(t *testing.T) {
	cfg := artifact.Config{Enabled: true, RootPath: t.TempDir()}
	b, err := artifact.OpenDeferredBundle(cfg, "rid-qcount-default", "AAPL", artifact.TriggerOnQualityFlag)
	require.NoError(t, err)
	require.NotNil(t, b)
	t.Cleanup(func() { _ = b.Close() })

	assert.Equal(t, int64(0), b.QualityFlagCount(),
		"freshly-opened bundle must report zero qualifying flags")
}

// TestBundle_QualityFlagCount_NilSafe — RecordQualityFlagCount and
// QualityFlagCount on a nil receiver must be no-ops. Mirrors the
// nil-receiver contract of every other Bundle method (Snapshot, Close,
// Promote, etc.) so callers don't have to guard with `if b != nil`.
func TestBundle_QualityFlagCount_NilSafe(t *testing.T) {
	var b *artifact.Bundle
	// Both must not panic.
	b.RecordQualityFlagCount(5)
	assert.Equal(t, int64(0), b.QualityFlagCount(),
		"nil bundle's QualityFlagCount must return 0")
}

// TestBundle_RecordQualityFlagCount_Accumulates pins the accumulator
// semantics. The data cleaner may run more than once per request (e.g. cache
// miss followed by re-clean, or future multi-pass pipelines); each call
// must add to the running total so the middleware sees the request-wide
// total at promote-time, not just the most recent call.
func TestBundle_RecordQualityFlagCount_Accumulates(t *testing.T) {
	cfg := artifact.Config{Enabled: true, RootPath: t.TempDir()}
	b, err := artifact.OpenDeferredBundle(cfg, "rid-qcount-accum", "AAPL", artifact.TriggerOnQualityFlag)
	require.NoError(t, err)
	require.NotNil(t, b)
	t.Cleanup(func() { _ = b.Close() })

	b.RecordQualityFlagCount(2)
	assert.Equal(t, int64(2), b.QualityFlagCount())

	b.RecordQualityFlagCount(3)
	assert.Equal(t, int64(5), b.QualityFlagCount(),
		"RecordQualityFlagCount must accumulate, not overwrite")
}

// TestBundle_RecordQualityFlagCount_NegativeIsNoOp guards against an
// over-eager caller passing a negative count (e.g. a future bug where the
// cleaner subtracts a removed flag). Recording must clamp at zero rather
// than letting the count go negative — a negative count would silently
// disable the trigger in the middleware's `count > 0` check.
func TestBundle_RecordQualityFlagCount_NegativeIsNoOp(t *testing.T) {
	cfg := artifact.Config{Enabled: true, RootPath: t.TempDir()}
	b, err := artifact.OpenDeferredBundle(cfg, "rid-qcount-neg", "AAPL", artifact.TriggerOnQualityFlag)
	require.NoError(t, err)
	require.NotNil(t, b)
	t.Cleanup(func() { _ = b.Close() })

	b.RecordQualityFlagCount(-3)
	assert.Equal(t, int64(0), b.QualityFlagCount(),
		"negative count must be ignored so the running total never goes below zero")

	b.RecordQualityFlagCount(2)
	b.RecordQualityFlagCount(-10)
	assert.Equal(t, int64(2), b.QualityFlagCount(),
		"negative subsequent record must not erase prior positive recordings")
}

// TestBundle_RecordQualityFlagCount_RaceFree pins the concurrent-safety
// contract. The cleaner runs from a single goroutine per request today, but
// future fan-out (parallel adjusters) could call RecordQualityFlagCount from
// multiple goroutines. Pinning this now prevents a future regression from
// being subtle and load-only-reproducible.
func TestBundle_RecordQualityFlagCount_RaceFree(t *testing.T) {
	cfg := artifact.Config{Enabled: true, RootPath: t.TempDir()}
	b, err := artifact.OpenDeferredBundle(cfg, "rid-qcount-race", "AAPL", artifact.TriggerOnQualityFlag)
	require.NoError(t, err)
	require.NotNil(t, b)
	t.Cleanup(func() { _ = b.Close() })

	const goroutines = 32
	const perGoroutine = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				b.RecordQualityFlagCount(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(goroutines*perGoroutine), b.QualityFlagCount(),
		"concurrent recordings must accumulate without lost updates")
}

// TestBundle_QualityFlagThreshold_RoundTrips pins the config plumb-through.
// The cleaner reads the threshold from the bundle on the request-path so it
// doesn't need its own copy of the artifact config. Storing the threshold on
// the bundle keeps the cleaner free of artifact-package config types.
func TestBundle_QualityFlagThreshold_RoundTrips(t *testing.T) {
	cfg := artifact.Config{
		Enabled:  true,
		RootPath: t.TempDir(),
		Triggers: artifact.TriggerConfig{
			QualityFlagThreshold: "warning",
		},
	}
	b, err := artifact.OpenDeferredBundle(cfg, "rid-qthr", "AAPL", artifact.TriggerOnQualityFlag)
	require.NoError(t, err)
	require.NotNil(t, b)
	t.Cleanup(func() { _ = b.Close() })

	assert.Equal(t, "warning", b.QualityFlagThreshold(),
		"bundle must surface the configured threshold so the cleaner can read it from ctx")
}

// TestBundle_QualityFlagThreshold_DefaultEmpty pins the disabled-by-default
// invariant. When the operator hasn't configured a threshold, the bundle
// reports empty string so the cleaner skips its hook and the middleware
// never fires the on_quality_flag trigger.
func TestBundle_QualityFlagThreshold_DefaultEmpty(t *testing.T) {
	cfg := artifact.Config{Enabled: true, RootPath: t.TempDir()}
	b, err := artifact.OpenDeferredBundle(cfg, "rid-qthr-empty", "AAPL", artifact.TriggerOnQualityFlag)
	require.NoError(t, err)
	require.NotNil(t, b)
	t.Cleanup(func() { _ = b.Close() })

	assert.Equal(t, "", b.QualityFlagThreshold(),
		"absent QualityFlagThreshold config must surface as empty string")
}

// TestBundle_QualityFlagThreshold_NilSafe — nil-receiver contract for the
// threshold getter, mirroring the rest of the Bundle API.
func TestBundle_QualityFlagThreshold_NilSafe(t *testing.T) {
	var b *artifact.Bundle
	assert.Equal(t, "", b.QualityFlagThreshold(),
		"nil bundle's QualityFlagThreshold must return empty string")
}
