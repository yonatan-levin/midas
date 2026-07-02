package artifact_test

// BUG-012 — runtime Warn on artifact-bundle buffer drops.
//
// The Bundle increments dropped / writeErrors / oversizeLines atomically but,
// pre-fix, SILENTLY: operators only learned of an incomplete capture by reading
// 00-manifest.json postmortem. These tests pin the at-most-once runtime Warn
// emitted at the FIRST drop of each kind via the opt-in WithLogger seam, while
// preserving ALL existing counter/manifest behaviour.
//
// We use zaptest/observer so the Warn lines are captured in-memory and can be
// asserted on by message + field.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
)

// newWarnObserver returns a logger whose entries are captured in-memory plus
// the ObservedLogs accessor. Debug-level so nothing the bundle emits is
// filtered before we can assert on it.
func newWarnObserver() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return zap.New(core), logs
}

// TestBundle_NilLoggerNoOp — a bundle constructed WITHOUT WithLogger must
// behave exactly as before: drops/oversize still increment their counters and
// nothing panics. Proves back-compat (existing call sites compile + behave
// unchanged) and nil-logger safety in the warn helpers.
func TestBundle_NilLoggerNoOp(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{
		Enabled:         true,
		RootPath:        root,
		QueueSize:       100,
		PendingBytesCap: 50, // tiny cap forces deterministic drops
	}

	// No WithLogger option — b.logger stays nil.
	b, err := artifact.OpenDeferredBundle(cfg, "rid-nil-logger", "AAPL", artifact.TriggerOnError)
	require.NoError(t, err)
	require.NotNil(t, b)

	// Force a snapshot drop: payload bigger than the entire cap.
	type Big struct{ Padding string }
	pad := make([]byte, 500)
	for i := range pad {
		pad[i] = 'y'
	}
	b.Snapshot(context.Background(), "phase", "snap.json", Big{Padding: string(pad)})

	// Force an oversize-line drop: line over MaxStreamLineBytes.
	oversize := make([]byte, artifact.MaxStreamLineBytes+1)
	for i := range oversize {
		oversize[i] = 'x'
	}
	require.NoError(t, b.AppendStream("99-narrate.jsonl", oversize))

	require.NoError(t, b.Promote(artifact.TriggerOnError))
	require.NoError(t, b.Close())

	// Counters still increment with a nil logger — behaviour unchanged.
	assert.GreaterOrEqual(t, b.Dropped(), int64(1), "nil-logger drop must still be counted")
	assert.Equal(t, int64(1), b.OversizeLines(), "nil-logger oversize must still be counted")
}

// TestBundle_FirstDropEmitsWarn — a deferred bundle with WithLogger and a tiny
// PendingBytesCap drops a too-large snapshot deterministically (deferred mode
// has no concurrent worker draining). Exactly ONE Warn with the
// snapshot_dropped message and a request_id field must fire.
func TestBundle_FirstDropEmitsWarn(t *testing.T) {
	logger, logs := newWarnObserver()
	root := t.TempDir()
	cfg := artifact.Config{
		Enabled:         true,
		RootPath:        root,
		QueueSize:       100,
		PendingBytesCap: 64, // tiny: any payload over 64 bytes drops outright
	}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-first-drop", "AAPL",
		artifact.TriggerOnError, artifact.WithLogger(logger))
	require.NoError(t, err)
	require.NotNil(t, b)

	// Snapshot a payload larger than the cap so bufferSnapshot hits the
	// `size > b.pendingCap` branch and drops it.
	type Big struct{ Padding string }
	pad := make([]byte, 256)
	for i := range pad {
		pad[i] = 'z'
	}
	b.Snapshot(context.Background(), "phase", "snap.json", Big{Padding: string(pad)})

	require.Equal(t, int64(1), b.Dropped(), "exactly one snapshot must have dropped")

	dropWarns := logs.FilterMessage("artifact.bundle.snapshot_dropped").All()
	require.Len(t, dropWarns, 1, "first drop must emit exactly one Warn")
	assert.Equal(t, zapcore.WarnLevel, dropWarns[0].Level)
	assert.Equal(t, "rid-first-drop", dropWarns[0].ContextMap()["request_id"],
		"warn must carry the bundle request_id for log correlation")
	// QA MINOR-1: a single payload larger than the whole byte-cap is a
	// byte-pressure drop, not a queue-count drop — the reason field must say so.
	assert.Equal(t, "bytes_overflow", dropWarns[0].ContextMap()["reason"],
		"a too-big single payload is a bytes_overflow drop")

	require.NoError(t, b.Promote(artifact.TriggerOnError))
	require.NoError(t, b.Close())
}

// TestBundle_QueueFullDropEmitsQueueFullReason — a deferred bundle whose
// COUNT cap (QueueSize) is exceeded by many small snapshots (each well under
// the byte-cap) drops via the count-bound eviction path, which must tag the
// Warn reason "queue_full" — distinct from the byte-pressure "bytes_overflow"
// path. This pins the QA MINOR-1 reason split end to end (both reasons are
// exercised: bytes_overflow above, queue_full here).
func TestBundle_QueueFullDropEmitsQueueFullReason(t *testing.T) {
	logger, logs := newWarnObserver()
	root := t.TempDir()
	cfg := artifact.Config{
		Enabled:         true,
		RootPath:        root,
		QueueSize:       2,       // count cap of 2 → the 3rd buffered snapshot evicts
		PendingBytesCap: 1 << 20, // 1 MiB: generous, so byte-cap never fires here
	}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-queue-full", "AAPL",
		artifact.TriggerOnError, artifact.WithLogger(logger))
	require.NoError(t, err)
	require.NotNil(t, b)

	// Three small snapshots: each fits the byte-cap, but the count cap is 2, so
	// the third forces a count-bound eviction of the oldest (queue_full).
	type Small struct{ N int }
	for i := 0; i < 3; i++ {
		b.Snapshot(context.Background(), "phase", "snap.json", Small{N: i})
	}

	require.GreaterOrEqual(t, b.Dropped(), int64(1), "count-cap overflow must drop at least one")

	dropWarns := logs.FilterMessage("artifact.bundle.snapshot_dropped").All()
	require.Len(t, dropWarns, 1, "first count-cap drop must emit exactly one Warn")
	assert.Equal(t, "queue_full", dropWarns[0].ContextMap()["reason"],
		"a count-cap eviction is a queue_full drop, not bytes_overflow")

	require.NoError(t, b.Promote(artifact.TriggerOnError))
	require.NoError(t, b.Close())
}

// TestBundle_SecondDropDoesNotEmitDuplicateWarn — two drops, but the at-most-
// once gate means the drop Warn fires exactly once while Dropped()==2.
func TestBundle_SecondDropDoesNotEmitDuplicateWarn(t *testing.T) {
	logger, logs := newWarnObserver()
	root := t.TempDir()
	cfg := artifact.Config{
		Enabled:         true,
		RootPath:        root,
		QueueSize:       100,
		PendingBytesCap: 64,
	}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-second-drop", "AAPL",
		artifact.TriggerOnError, artifact.WithLogger(logger))
	require.NoError(t, err)
	require.NotNil(t, b)

	type Big struct{ Padding string }
	pad := make([]byte, 256)
	for i := range pad {
		pad[i] = 'z'
	}
	// Two oversize snapshots → two drops.
	b.Snapshot(context.Background(), "phase", "snap.json", Big{Padding: string(pad)})
	b.Snapshot(context.Background(), "phase", "snap.json", Big{Padding: string(pad)})

	require.Equal(t, int64(2), b.Dropped(), "both snapshots must have dropped")

	dropWarns := logs.FilterMessage("artifact.bundle.snapshot_dropped").All()
	assert.Len(t, dropWarns, 1, "at-most-once: only the FIRST drop emits a Warn even though two dropped")

	require.NoError(t, b.Promote(artifact.TriggerOnError))
	require.NoError(t, b.Close())
}

// TestBundle_OversizeLineEmitsWarn — an eager bundle with WithLogger that gets
// a stream line over MaxStreamLineBytes must emit exactly one oversize_line
// Warn and OversizeLines()==1.
func TestBundle_OversizeLineEmitsWarn(t *testing.T) {
	logger, logs := newWarnObserver()
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenBundle(cfg, "rid-oversize", "AAPL",
		artifact.TriggerHeader, artifact.WithLogger(logger))
	require.NoError(t, err)
	require.NotNil(t, b)

	// 300 KiB line — over the 256 KiB MaxStreamLineBytes.
	oversize := make([]byte, 300*1024)
	for i := range oversize {
		oversize[i] = 'x'
	}
	require.NoError(t, b.AppendStream("99-narrate.jsonl", oversize))

	assert.Equal(t, int64(1), b.OversizeLines(), "oversize line must be counted")

	overWarns := logs.FilterMessage("artifact.bundle.oversize_line").All()
	require.Len(t, overWarns, 1, "oversize line must emit exactly one Warn")
	assert.Equal(t, zapcore.WarnLevel, overWarns[0].Level)
	assert.Equal(t, "rid-oversize", overWarns[0].ContextMap()["request_id"])

	require.NoError(t, b.Close())
}

// TestBundle_WriteErrorEmitsWarn — the worker's os.WriteFile fails because the
// bundle root is replaced with a regular file after construction, so every
// per-job filepath.Join(root, filename) write ENOTDIRs. Exactly one
// write_error Warn must fire and WriteErrors() > 0.
//
// This mirrors the deterministic, cross-platform sabotage pattern already used
// by TestSnapshot_WriteFailureDegradesAndAnnotates in bundle_test.go (proven on
// Windows + POSIX), so it is safe to keep rather than skip.
func TestBundle_WriteErrorEmitsWarn(t *testing.T) {
	logger, logs := newWarnObserver()
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root, QueueSize: 64}

	b, err := artifact.OpenBundle(cfg, "rid-write-err", "AAPL",
		artifact.TriggerHeader, artifact.WithLogger(logger))
	require.NoError(t, err)
	require.NotNil(t, b)

	// Sabotage: replace the bundle directory with a regular file so every
	// subsequent os.WriteFile under it fails with "not a directory".
	bundleRoot := b.Root()
	require.NoError(t, os.RemoveAll(bundleRoot))
	require.NoError(t, os.WriteFile(bundleRoot, []byte("blocked"), 0o644))

	type Payload struct{ N int }
	const writeAttempts = 5
	for i := 0; i < writeAttempts; i++ {
		b.Snapshot(context.Background(), "fetch.sec", "x.json", Payload{N: i})
	}

	// Block until the worker has observed all blocked writes BEFORE healing the
	// directory. WriteErrors() reads the atomic counter the worker bumps on each
	// failed os.WriteFile; once it reaches writeAttempts the first failure has
	// already fired the at-most-once write_error Warn. Removes the fast-CI race
	// (CI-1 / #20) where the worker drained everything only after the heal.
	require.Eventually(t, func() bool {
		return b.WriteErrors() >= int64(writeAttempts)
	}, 5*time.Second, time.Millisecond,
		"worker must observe all write failures before the directory is healed")

	// Restore the directory before Close so Finalize's manifest write can succeed.
	require.NoError(t, os.Remove(bundleRoot))
	require.NoError(t, os.MkdirAll(bundleRoot, 0o755))
	require.NoError(t, b.Close())

	assert.GreaterOrEqual(t, b.WriteErrors(), int64(1),
		"at least one worker os.WriteFile must have failed")

	writeWarns := logs.FilterMessage("artifact.bundle.write_error").All()
	require.Len(t, writeWarns, 1, "write error must emit exactly one (at-most-once) Warn")
	assert.Equal(t, zapcore.WarnLevel, writeWarns[0].Level)
	assert.Equal(t, "rid-write-err", writeWarns[0].ContextMap()["request_id"])
	// REVIEWER NIT: the write_error Warn must name the failing call site so an
	// operator knows WHICH disk op failed from the single at-most-once line.
	// The sabotaged-root path fails in the worker's os.WriteFile → "worker_write".
	assert.Equal(t, "worker_write", writeWarns[0].ContextMap()["site"],
		"a worker os.WriteFile failure must be tagged site=worker_write")
}
