package artifact_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
)

// TestBundleSink_TeesNarrateLines verifies that an Info-level entry tagged
// event=narrate is written both to the wrapped core (host log stream) AND
// to <bundle>/99-narrate.jsonl.
func TestBundleSink_TeesNarrateLines(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root, QueueSize: 64}
	b, err := artifact.OpenBundle(cfg, "rid-narrate-tee", "AAPL", artifact.TriggerHeader)
	require.NoError(t, err)
	require.NotNil(t, b)

	wrapped, recorded := observer.New(zapcore.InfoLevel)
	sink := artifact.NewBundleSink(wrapped, b)
	logger := zap.New(sink)

	const N = 3
	for i := 0; i < N; i++ {
		logger.Info("trace.phase",
			zap.String("event", "narrate"),
			zap.String("phase", "test"),
			zap.Int("seq", i),
		)
	}

	// Forwarding still works: observer saw all N entries.
	assert.Equal(t, N, recorded.Len(), "wrapped core must receive every entry")

	require.NoError(t, b.Close())

	// Tee landed in 99-narrate.jsonl.
	body, err := os.ReadFile(filepath.Join(b.Root(), "99-narrate.jsonl"))
	require.NoError(t, err, "99-narrate.jsonl must exist after Bundle.Close")

	lines := splitJSONLines(string(body))
	require.Len(t, lines, N)
	for i, l := range lines {
		var v map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(l), &v), "line %d not valid JSON: %s", i, l)
		assert.Equal(t, "narrate", v["event"])
		assert.Equal(t, "test", v["phase"])
	}

	// Debug stream should NOT exist — no Debug entries were emitted.
	_, err = os.Stat(filepath.Join(b.Root(), "99-debug-trace.jsonl"))
	assert.True(t, os.IsNotExist(err), "no debug stream when only Info entries emitted")
}

// TestBundleSink_TeesDebugLines verifies that Debug-level entries (without
// event=narrate) land in 99-debug-trace.jsonl and NOT in 99-narrate.jsonl.
func TestBundleSink_TeesDebugLines(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root, QueueSize: 64}
	b, err := artifact.OpenBundle(cfg, "rid-debug-tee", "AAPL", artifact.TriggerHeader)
	require.NoError(t, err)

	// Observer at DebugLevel so the wrapped core accepts Debug entries.
	wrapped, recorded := observer.New(zapcore.DebugLevel)
	sink := artifact.NewBundleSink(wrapped, b)
	logger := zap.New(sink)

	const N = 5
	for i := 0; i < N; i++ {
		logger.Debug("trace.debug",
			zap.String("phase", "compute"),
			zap.Int("seq", i),
		)
	}

	assert.Equal(t, N, recorded.Len())

	require.NoError(t, b.Close())

	debugBody, err := os.ReadFile(filepath.Join(b.Root(), "99-debug-trace.jsonl"))
	require.NoError(t, err)

	lines := splitJSONLines(string(debugBody))
	assert.Len(t, lines, N, "every debug entry must land in 99-debug-trace.jsonl")

	// 99-narrate.jsonl must NOT exist (no event=narrate entries emitted).
	_, err = os.Stat(filepath.Join(b.Root(), "99-narrate.jsonl"))
	assert.True(t, os.IsNotExist(err), "no narrate stream when no narrate entries emitted")
}

// TestBundleSink_NilBundleIsTransparent — a nil bundle means the wrapper
// is purely pass-through. No filesystem side effects, observer sees every
// entry, no panics.
func TestBundleSink_NilBundleIsTransparent(t *testing.T) {
	wrapped, recorded := observer.New(zapcore.DebugLevel)
	sink := artifact.NewBundleSink(wrapped, nil)
	logger := zap.New(sink)

	logger.Info("info-line", zap.String("event", "narrate"))
	logger.Debug("debug-line")
	logger.Warn("warn-line")
	logger.Error("err-line")

	assert.Equal(t, 4, recorded.Len(), "wrapped core must receive every entry")
}

// TestBundleSink_PreservesWithFields ensures fields added via .With() (the
// request-scoped logger pattern: request_id, key_id) propagate to the JSONL
// tee, not just to the wrapped core's output.
func TestBundleSink_PreservesWithFields(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root, QueueSize: 64}
	b, err := artifact.OpenBundle(cfg, "rid-with-fields", "AAPL", artifact.TriggerHeader)
	require.NoError(t, err)

	wrapped, _ := observer.New(zapcore.InfoLevel)
	sink := artifact.NewBundleSink(wrapped, b)
	// Apply With at the logger level so the sink's With chain is exercised.
	logger := zap.New(sink).With(zap.String("request_id", "req_X"))

	logger.Info("phase",
		zap.String("event", "narrate"),
		zap.String("phase", "growth.estimated"),
	)

	require.NoError(t, b.Close())

	body, err := os.ReadFile(filepath.Join(b.Root(), "99-narrate.jsonl"))
	require.NoError(t, err)
	require.NotEmpty(t, body)

	var entry map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(splitJSONLines(string(body))[0]), &entry))
	assert.Equal(t, "req_X", entry["request_id"], "with-fields must propagate to JSONL tee")
	assert.Equal(t, "growth.estimated", entry["phase"])
	assert.Equal(t, "narrate", entry["event"])
}

// TestBundleSink_DebugNarrateLandsInBoth — a Debug-level entry that ALSO
// carries event=narrate must land in BOTH bundle streams. This pins the
// "both rules can fire on the same entry" branch in Write.
func TestBundleSink_DebugNarrateLandsInBoth(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root, QueueSize: 64}
	b, err := artifact.OpenBundle(cfg, "rid-both", "AAPL", artifact.TriggerHeader)
	require.NoError(t, err)

	wrapped, _ := observer.New(zapcore.DebugLevel)
	sink := artifact.NewBundleSink(wrapped, b)
	logger := zap.New(sink)

	logger.Debug("trace.both",
		zap.String("event", "narrate"),
		zap.String("phase", "compute"),
	)
	require.NoError(t, b.Close())

	narrateBody, err := os.ReadFile(filepath.Join(b.Root(), "99-narrate.jsonl"))
	require.NoError(t, err)
	debugBody, err := os.ReadFile(filepath.Join(b.Root(), "99-debug-trace.jsonl"))
	require.NoError(t, err)

	assert.Len(t, splitJSONLines(string(narrateBody)), 1, "narrate stream gets the entry")
	assert.Len(t, splitJSONLines(string(debugBody)), 1, "debug stream gets the entry")
}

// TestBundleSink_NonNarrateInfoNotTeed — Info-level entries WITHOUT
// event=narrate must NOT land in any bundle stream (the only Info entries
// the spec wants in 99-narrate.jsonl are explicitly tagged narrate).
func TestBundleSink_NonNarrateInfoNotTeed(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root, QueueSize: 64}
	b, err := artifact.OpenBundle(cfg, "rid-not-teed", "AAPL", artifact.TriggerHeader)
	require.NoError(t, err)

	wrapped, _ := observer.New(zapcore.InfoLevel)
	sink := artifact.NewBundleSink(wrapped, b)
	logger := zap.New(sink)

	logger.Info("regular access log line",
		zap.String("phase", "irrelevant"),
		zap.Int("status", 200),
	)
	logger.Warn("not narrate either")
	logger.Error("not narrate either")
	require.NoError(t, b.Close())

	for _, name := range []string{"99-narrate.jsonl", "99-debug-trace.jsonl"} {
		_, err := os.Stat(filepath.Join(b.Root(), name))
		assert.True(t, os.IsNotExist(err), "%s must not exist when no narrate/debug entries emitted", name)
	}
}

// TestBundleSink_Sync_ForwardsToWrapped — the wrapper's Sync() must call
// the wrapped core's Sync(). We can't directly observe sync from observer.New,
// but we can confirm Sync returns nil and doesn't panic.
func TestBundleSink_Sync_ForwardsToWrapped(t *testing.T) {
	wrapped, _ := observer.New(zapcore.DebugLevel)
	sink := artifact.NewBundleSink(wrapped, nil)
	assert.NoError(t, sink.Sync())
}

// TestBundleSink_AppendStream_OpenError_AccountedAsWriteError — when the
// bundle root has been removed underneath us (rare but possible),
// AppendStream's underlying os.OpenFile fails. The sink swallows the error
// (best-effort), and Bundle.writeErrors must increment so Close() degrades
// the manifest outcome to "partial".
func TestBundleSink_AppendStream_OpenError_AccountedAsWriteError(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root, QueueSize: 64}
	b, err := artifact.OpenBundle(cfg, "rid-streamfail", "AAPL", artifact.TriggerHeader)
	require.NoError(t, err)

	// Sabotage: replace bundle root with a regular file so OpenFile under it
	// errors with ENOTDIR.
	bundleRoot := b.Root()
	require.NoError(t, os.RemoveAll(bundleRoot))
	require.NoError(t, os.WriteFile(bundleRoot, []byte("blocked"), 0o644))

	// Direct AppendStream call (we don't need the sink for this assertion;
	// the sink's failure path is the same code).
	err = b.AppendStream("99-narrate.jsonl", []byte(`{"x":1}`))
	require.Error(t, err, "AppendStream must surface the OpenFile error to direct callers")

	// Restore the directory before Close so Finalize can write the manifest.
	require.NoError(t, os.Remove(bundleRoot))
	require.NoError(t, os.MkdirAll(bundleRoot, 0o755))
	require.NoError(t, b.Close())

	assert.GreaterOrEqual(t, b.WriteErrors(), int64(1),
		"writeErrors must capture the AppendStream open failure")
}

// TestBundleSink_Check_RespectsLevelGate — when the wrapped core is at
// ErrorLevel and we ask Check about a Debug entry, the wrapper must NOT
// add itself to the CheckedEntry (so zap skips the Write entirely). This
// pins the `return ce` branch in Check.
func TestBundleSink_Check_RespectsLevelGate(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root, QueueSize: 64}
	b, err := artifact.OpenBundle(cfg, "rid-check-gate", "AAPL", artifact.TriggerHeader)
	require.NoError(t, err)

	wrapped, recorded := observer.New(zapcore.ErrorLevel)
	sink := artifact.NewBundleSink(wrapped, b)
	logger := zap.New(sink)

	// Debug + Info + Warn must all be filtered out (ErrorLevel gate).
	logger.Debug("d")
	logger.Info("i", zap.String("event", "narrate"))
	logger.Warn("w")
	logger.Error("e")

	assert.Equal(t, 1, recorded.Len(), "only the Error entry should pass the gate")
	require.NoError(t, b.Close())

	// Bundle streams should also be empty for Debug/narrate (the entry
	// never reached Write because Check filtered it out).
	for _, name := range []string{"99-narrate.jsonl", "99-debug-trace.jsonl"} {
		_, err := os.Stat(filepath.Join(b.Root(), name))
		assert.True(t, os.IsNotExist(err), "%s must not exist when entry filtered by level gate", name)
	}
}

// failingCore is a minimal zapcore.Core that always errors on Write. Used
// to exercise the wrapper's "wrapped write failed, propagate error" branch.
type failingCore struct{ zapcore.LevelEnabler }

func (failingCore) With(_ []zapcore.Field) zapcore.Core { return failingCore{zapcore.DebugLevel} }
func (f failingCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if f.Enabled(ent.Level) {
		return ce.AddCore(ent, f)
	}
	return ce
}
func (failingCore) Write(_ zapcore.Entry, _ []zapcore.Field) error { return assertErr }
func (failingCore) Sync() error                                    { return nil }

var assertErr = errSentinel("write failed")

type errSentinel string

func (e errSentinel) Error() string { return string(e) }

// TestBundleSink_Write_PropagatesWrappedError — when the wrapped core's
// Write fails, the wrapper must return that error so zap's pipeline notices.
// The bundle tee must NOT have run (no point persisting an entry the host
// log stream rejected).
func TestBundleSink_Write_PropagatesWrappedError(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root, QueueSize: 64}
	b, err := artifact.OpenBundle(cfg, "rid-wrap-fail", "AAPL", artifact.TriggerHeader)
	require.NoError(t, err)

	sink := artifact.NewBundleSink(failingCore{zapcore.DebugLevel}, b)
	// Call Write directly (zap.New + Logger.Info would swallow the error
	// silently and we want to assert on it).
	ent := zapcore.Entry{Level: zapcore.InfoLevel, Message: "x"}
	err = sink.Write(ent, []zapcore.Field{zap.String("event", "narrate")})
	require.Error(t, err, "wrapper must propagate wrapped core's Write error")

	require.NoError(t, b.Close())
	// Tee must not have written 99-narrate.jsonl because the wrapped Write
	// failed first and we returned early.
	_, err = os.Stat(filepath.Join(b.Root(), "99-narrate.jsonl"))
	assert.True(t, os.IsNotExist(err), "no tee when wrapped Write fails")
}

// TestBundleSink_BaseFieldsInJSONL pins the new constructor contract:
// fields supplied via NewBundleSink(..., baseFields...) must appear in the
// JSONL tee output WITHOUT having been routed through the wrapped core's
// .With() chain. This is the mechanism that lets the trace middleware
// inject request_id into 99-narrate.jsonl without producing a duplicate
// "request_id" key in the host log stream (REVIEWER finding 2026-04-25).
func TestBundleSink_BaseFieldsInJSONL(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root, QueueSize: 64}
	b, err := artifact.OpenBundle(cfg, "rid-base-fields", "AAPL", artifact.TriggerHeader)
	require.NoError(t, err)

	wrapped, recorded := observer.New(zapcore.InfoLevel)
	// Inject request_id as a baseline field — must NOT trigger any .With()
	// call on the wrapped core.
	sink := artifact.NewBundleSink(wrapped, b, zap.String("request_id", "req_TEST"))
	logger := zap.New(sink)

	logger.Info("phase",
		zap.String("event", "narrate"),
		zap.String("phase", "test"),
	)

	require.NoError(t, b.Close())

	// JSONL tee must carry request_id from the baseFields.
	body, err := os.ReadFile(filepath.Join(b.Root(), "99-narrate.jsonl"))
	require.NoError(t, err)
	lines := splitJSONLines(string(body))
	require.Len(t, lines, 1)

	var entry map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &entry))
	assert.Equal(t, "req_TEST", entry["request_id"],
		"baseFields must populate the JSONL encoder context")
	assert.Equal(t, "test", entry["phase"])
	assert.Equal(t, "narrate", entry["event"])

	// And the wrapped core must NOT have seen request_id added to its
	// internal state — only the call-site fields should be visible there.
	require.Equal(t, 1, recorded.Len())
	wrappedFields := recorded.All()[0].Context
	for _, f := range wrappedFields {
		if f.Key == "request_id" {
			t.Fatalf("baseFields must NOT propagate to wrapped core's With-state; "+
				"got duplicate request_id field in host log stream: %v", wrappedFields)
		}
	}
}

// TestBundleSink_BaseFieldsAliasingProtection ensures the constructor copies
// the supplied baseFields slice — mutating the caller's slice or appending
// via subsequent .With() calls must not bleed into the original baseFields.
func TestBundleSink_BaseFieldsAliasingProtection(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root, QueueSize: 64}
	b, err := artifact.OpenBundle(cfg, "rid-alias", "AAPL", artifact.TriggerHeader)
	require.NoError(t, err)

	wrapped, _ := observer.New(zapcore.InfoLevel)
	caller := []zapcore.Field{zap.String("request_id", "req_orig")}
	sink := artifact.NewBundleSink(wrapped, b, caller...)

	// Mutate the caller's slice — sink must be unaffected.
	caller[0] = zap.String("request_id", "req_HIJACKED")

	logger := zap.New(sink)
	logger.Info("phase", zap.String("event", "narrate"), zap.String("phase", "test"))
	require.NoError(t, b.Close())

	body, err := os.ReadFile(filepath.Join(b.Root(), "99-narrate.jsonl"))
	require.NoError(t, err)
	var entry map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(splitJSONLines(string(body))[0]), &entry))
	assert.Equal(t, "req_orig", entry["request_id"],
		"sink must defensively copy baseFields so caller mutations do not bleed in")
}

// splitJSONLines splits on \n and drops empty lines.
func splitJSONLines(s string) []string {
	raw := strings.Split(strings.TrimSpace(s), "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
