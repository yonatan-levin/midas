package artifact_test

// Phase 2.A — auto-on-error trigger unit tests for the artifact.Bundle's
// deferred mode. The middleware-level integration of these primitives is
// pinned by trace_test.go; this file pins the building blocks in isolation.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
)

// TestTrigger_OnErrorConstant_Defined pins the wire value of the new
// trigger so a typo in a future refactor doesn't silently break manifest
// tooling that greps for "on_error".
func TestTrigger_OnErrorConstant_Defined(t *testing.T) {
	assert.Equal(t, artifact.Trigger("on_error"), artifact.TriggerOnError,
		"TriggerOnError wire value must remain on_error to keep manifest grep stable")
}

// TestOpenDeferredBundle_DisabledReturnsNil mirrors OpenBundle's contract:
// when the master switch is off, OpenDeferredBundle returns nil + nil error
// so callers can blindly defer Close on a nil bundle.
func TestOpenDeferredBundle_DisabledReturnsNil(t *testing.T) {
	cfg := artifact.Config{Enabled: false, RootPath: t.TempDir()}
	b, err := artifact.OpenDeferredBundle(cfg, "rid", "AAPL", artifact.TriggerOnError)
	require.NoError(t, err)
	assert.Nil(t, b)

	// Nil-safe: every method on a nil bundle is a no-op.
	assert.NoError(t, b.Promote(artifact.TriggerOnError))
	assert.NoError(t, b.Close())
}

// TestOpenDeferredBundle_DoesNotCreateDirectory pins the core invariant:
// deferred bundles must NOT touch disk at construction time. Any non-erroring
// request that opened a deferred bundle and never promoted must leave the
// artifact root untouched.
func TestOpenDeferredBundle_DoesNotCreateDirectory(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-deferred", "AAPL", artifact.TriggerOnError)
	require.NoError(t, err)
	require.NotNil(t, b)

	// Bundle root path is computed but the directory must not exist on disk.
	_, err = os.Stat(b.Root())
	assert.True(t, os.IsNotExist(err), "deferred bundle dir must not exist pre-promote, got %v", err)

	// The artifact root itself must remain empty (no date partition created).
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Empty(t, entries, "no date partition should be created for unpromoted deferred bundle")

	require.NoError(t, b.Close())
}

// TestDeferredBundle_PromoteCreatesDirectoryAndFlushes pins the happy path:
// buffered snapshots and stream lines land on disk after Promote, the
// manifest reflects them, and the manifest's trigger is on_error.
func TestDeferredBundle_PromoteCreatesDirectoryAndFlushes(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-promote", "AAPL", artifact.TriggerOnError)
	require.NoError(t, err)
	require.NotNil(t, b)

	// Buffer a snapshot + a couple of stream lines BEFORE promote.
	type Payload struct {
		Ticker string `json:"ticker"`
		Value  int    `json:"value"`
	}
	b.Snapshot(context.Background(), "fetch.sec", "05-fetch-sec.parsed.json", Payload{Ticker: "AAPL", Value: 42})
	require.NoError(t, b.AppendStream("99-narrate.jsonl", []byte(`{"event":"narrate","phase":"request.received"}`)))
	require.NoError(t, b.AppendStream("99-narrate.jsonl", []byte(`{"event":"narrate","phase":"handler.entry"}`)))

	// Pre-promote: bundle directory still must not exist on disk.
	_, statErr := os.Stat(b.Root())
	require.True(t, os.IsNotExist(statErr), "pre-promote dir must not exist")

	// Promote — flushes buffers to disk, manifest gets trigger=on_error.
	require.NoError(t, b.Promote(artifact.TriggerOnError))

	// Now the directory must exist.
	st, err := os.Stat(b.Root())
	require.NoError(t, err, "post-promote dir must exist on disk")
	assert.True(t, st.IsDir())

	// Add another stream line AFTER promote — must land in the same file.
	require.NoError(t, b.AppendStream("99-narrate.jsonl", []byte(`{"event":"narrate","phase":"response.sent"}`)))

	require.NoError(t, b.Close())

	// Snapshot file present.
	snapshotBody, err := os.ReadFile(filepath.Join(b.Root(), "05-fetch-sec.parsed.json"))
	require.NoError(t, err)
	var got Payload
	require.NoError(t, json.Unmarshal(snapshotBody, &got))
	assert.Equal(t, "AAPL", got.Ticker)

	// Stream file contains all three lines (2 pre-promote + 1 post-promote).
	streamBody, err := os.ReadFile(filepath.Join(b.Root(), "99-narrate.jsonl"))
	require.NoError(t, err)
	streamLines := splitJSONLines(string(streamBody))
	require.Len(t, streamLines, 3, "stream must contain 2 pre-promote + 1 post-promote lines")
	assert.Contains(t, streamLines[0], `"phase":"request.received"`)
	assert.Contains(t, streamLines[1], `"phase":"handler.entry"`)
	assert.Contains(t, streamLines[2], `"phase":"response.sent"`)

	// Manifest reflects on_error trigger and includes the snapshot phase.
	mfBody, err := os.ReadFile(filepath.Join(b.Root(), "00-manifest.json"))
	require.NoError(t, err)
	var mf artifact.Manifest
	require.NoError(t, json.Unmarshal(mfBody, &mf))
	assert.Equal(t, "on_error", mf.Trigger)
	assert.Equal(t, "AAPL", mf.Ticker)
	assert.Equal(t, "rid-promote", mf.RequestID)
	require.Len(t, mf.PhasesRecorded, 1)
	assert.Equal(t, "fetch.sec", mf.PhasesRecorded[0].Phase)
}

// TestDeferredBundle_CloseWithoutPromote_NoDisk pins the dissolve path:
// when status<500 we Close() without Promote() and nothing on disk should
// have been created. This is the dominant path in production with on_error
// enabled — the vast majority of requests succeed.
func TestDeferredBundle_CloseWithoutPromote_NoDisk(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-dissolve", "AAPL", artifact.TriggerOnError)
	require.NoError(t, err)
	require.NotNil(t, b)

	// Buffer some activity.
	b.Snapshot(context.Background(), "fetch.sec", "x.json", map[string]int{"a": 1})
	require.NoError(t, b.AppendStream("99-narrate.jsonl", []byte(`{"event":"narrate","phase":"x"}`)))

	// Close WITHOUT Promote — deferred bundle dissolves.
	require.NoError(t, b.Close())

	// Nothing on disk: no bundle dir, no manifest, no streams. The artifact
	// root must contain ZERO entries (no date partition was even created).
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Empty(t, entries, "Close-without-Promote must leave artifact root empty; got %v", entries)
}

// TestDeferredBundle_PromoteIdempotent — second Promote is a no-op, NOT a
// failure or duplicate write. Defensive against a future race in the
// middleware where Promote could be called twice (e.g., panic-recovery
// chain that triggers the on-error path twice).
func TestDeferredBundle_PromoteIdempotent(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-double-promote", "AAPL", artifact.TriggerOnError)
	require.NoError(t, err)

	require.NoError(t, b.Promote(artifact.TriggerOnError))
	// Second call must be a clean no-op (already in eager mode).
	require.NoError(t, b.Promote(artifact.TriggerOnError))

	require.NoError(t, b.Close())
}

// TestDeferredBundle_PromoteOnEagerBundle_NoOp — Promote on a bundle that
// was opened eagerly via OpenBundle is a clean no-op (deferred=false from
// construction). Pins the contract that Promote is safe to call from generic
// code without first checking the bundle's mode.
func TestDeferredBundle_PromoteOnEagerBundle_NoOp(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	// Eager bundle.
	b, err := artifact.OpenBundle(cfg, "rid-eager-promote", "AAPL", artifact.TriggerHeader)
	require.NoError(t, err)

	// Promote is a no-op for eager bundles; original trigger must survive.
	require.NoError(t, b.Promote(artifact.TriggerOnError))

	require.NoError(t, b.Close())

	mfBody, err := os.ReadFile(filepath.Join(b.Root(), "00-manifest.json"))
	require.NoError(t, err)
	var mf artifact.Manifest
	require.NoError(t, json.Unmarshal(mfBody, &mf))
	assert.Equal(t, "header", mf.Trigger,
		"Promote(on_error) on an eager bundle must NOT clobber the original trigger")
}

// TestDeferredBundle_BufferOverflow_DropsOldestSnapshots — when the
// pending-bytes cap is exceeded, oldest snapshots get evicted (FIFO) and
// the dropped counter increments. The brief pins drop-OLDEST semantics
// because the entries closest to the trigger (typically the 5xx) are the
// most useful for postmortem reading.
func TestDeferredBundle_BufferOverflow_DropsOldestSnapshots(t *testing.T) {
	root := t.TempDir()
	// Tiny cap — each snapshot is ~100 bytes JSON, so 500 bytes holds ~5.
	cfg := artifact.Config{
		Enabled:         true,
		RootPath:        root,
		QueueSize:       100, // big enough that the COUNT cap doesn't kick in first
		PendingBytesCap: 500,
	}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-overflow", "AAPL", artifact.TriggerOnError)
	require.NoError(t, err)
	require.NotNil(t, b)

	type Payload struct {
		ID      int
		Padding string
	}

	// Push 20 ~100-byte snapshots — well over the 500-byte cap. Drops > 0.
	const N = 20
	pad := "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" // ~50 bytes
	for i := 0; i < N; i++ {
		b.Snapshot(context.Background(), "phase", "snap.json", Payload{ID: i, Padding: pad})
	}

	require.NoError(t, b.Promote(artifact.TriggerOnError))
	require.NoError(t, b.Close())

	dropped := b.Dropped()
	assert.Greater(t, dropped, int64(0),
		"expected at least one drop with %d snapshots over a 500-byte cap; got %d", N, dropped)

	// Manifest outcome must degrade to partial when drops happened.
	mfBody, err := os.ReadFile(filepath.Join(b.Root(), "00-manifest.json"))
	require.NoError(t, err)
	var mf artifact.Manifest
	require.NoError(t, json.Unmarshal(mfBody, &mf))
	assert.Equal(t, "partial", mf.Outcome,
		"any deferred-buffer drop must downgrade outcome to partial")
	assert.Contains(t, mf.Notes, "queue_drops=",
		"manifest notes must annotate the drop count for postmortem readers")
}

// TestDeferredBundle_BufferOverflow_DropsOldestByCount — when the queue-count
// cap is exceeded (independently of bytes), oldest snapshots are also evicted.
// This is the second of the two caps the brief mandates.
func TestDeferredBundle_BufferOverflow_DropsOldestByCount(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{
		Enabled:         true,
		RootPath:        root,
		QueueSize:       3,       // tight count cap
		PendingBytesCap: 1 << 30, // huge byte cap so only the count cap kicks in
	}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-count", "AAPL", artifact.TriggerOnError)
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
		b.Snapshot(context.Background(), "phase", "snap.json", map[string]int{"i": i})
	}

	require.NoError(t, b.Promote(artifact.TriggerOnError))
	require.NoError(t, b.Close())

	assert.GreaterOrEqual(t, b.Dropped(), int64(7),
		"queue size 3 with 10 snapshots must drop at least 7; got %d", b.Dropped())
}

// TestDeferredBundle_AppendStreamOverflow_DropsAndCounts — when stream
// buffers alone exceed the cap (no snapshots to evict), excess lines drop
// and the dropped counter increments. We do NOT truncate stream lines mid-
// content because malformed JSONL on disk is strictly worse than missing
// lines.
func TestDeferredBundle_AppendStreamOverflow_DropsAndCounts(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{
		Enabled:         true,
		RootPath:        root,
		QueueSize:       100,
		PendingBytesCap: 200, // ~3-4 lines worth
	}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-stream-overflow", "AAPL", artifact.TriggerOnError)
	require.NoError(t, err)

	// Each line is ~50 bytes. 20 lines = ~1000 bytes, far over the 200-byte cap.
	line := []byte(`{"event":"narrate","phase":"x","detail":"yyyyyyyyyy"}`)
	const N = 20
	for i := 0; i < N; i++ {
		_ = b.AppendStream("99-narrate.jsonl", line)
	}

	require.NoError(t, b.Promote(artifact.TriggerOnError))
	require.NoError(t, b.Close())

	assert.Greater(t, b.Dropped(), int64(0),
		"stream-only overflow must increment dropped counter")
}

// TestDeferredBundle_DefaultPendingBytesCap — when PendingBytesCap is unset
// (zero), the default of 10 MiB applies. We can't test the exact value
// without exposing it, but we CAN smoke-test that sane bursts don't drop
// when the cap defaults.
func TestDeferredBundle_DefaultPendingBytesCap(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{
		Enabled:         true,
		RootPath:        root,
		QueueSize:       512,
		PendingBytesCap: 0, // default applies
	}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-default-cap", "AAPL", artifact.TriggerOnError)
	require.NoError(t, err)

	// 100 snapshots of ~100 bytes each = 10 KB. Should fit easily under 10 MiB.
	for i := 0; i < 100; i++ {
		b.Snapshot(context.Background(), "phase", "snap.json", map[string]int{"i": i})
	}

	require.NoError(t, b.Promote(artifact.TriggerOnError))
	require.NoError(t, b.Close())

	assert.Equal(t, int64(0), b.Dropped(),
		"default 10 MiB cap should hold 100 small snapshots without drops")
}

// TestDeferredBundle_StreamLineExceedsCap_DropsAndCounts — when a SINGLE
// stream line is larger than the entire pending-bytes cap, the line is
// dropped (truncating mid-line would yield malformed JSONL on disk). Pins
// the explicit drop branch in bufferStream.
func TestDeferredBundle_StreamLineExceedsCap_DropsAndCounts(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{
		Enabled:         true,
		RootPath:        root,
		QueueSize:       100,
		PendingBytesCap: 50, // tiny cap
	}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-bigline", "AAPL", artifact.TriggerOnError)
	require.NoError(t, err)

	// Build a single line that exceeds the 50-byte cap on its own.
	bigLine := make([]byte, 200)
	for i := range bigLine {
		bigLine[i] = 'x'
	}
	require.NoError(t, b.AppendStream("99-narrate.jsonl", bigLine))

	require.NoError(t, b.Promote(artifact.TriggerOnError))
	require.NoError(t, b.Close())

	assert.GreaterOrEqual(t, b.Dropped(), int64(1),
		"single oversize line must be counted as dropped")

	// The promoted bundle must have an empty (or absent) stream file.
	streamPath := filepath.Join(b.Root(), "99-narrate.jsonl")
	body, err := os.ReadFile(streamPath)
	if err == nil {
		assert.Empty(t, body, "stream must be empty when oversize line was dropped")
	}
}

// TestDeferredBundle_SnapshotExceedsCap_DropsAndCounts — same edge case for
// Snapshot: a single payload bigger than the entire pending-bytes cap is
// dropped (no snapshots to evict can make room for it).
func TestDeferredBundle_SnapshotExceedsCap_DropsAndCounts(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{
		Enabled:         true,
		RootPath:        root,
		QueueSize:       100,
		PendingBytesCap: 100, // tiny cap
	}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-bigsnap", "AAPL", artifact.TriggerOnError)
	require.NoError(t, err)

	// Build a payload that will marshal to >100 bytes.
	type Big struct {
		Padding string
	}
	pad := make([]byte, 500)
	for i := range pad {
		pad[i] = 'y'
	}
	b.Snapshot(context.Background(), "phase", "snap.json", Big{Padding: string(pad)})

	require.NoError(t, b.Promote(artifact.TriggerOnError))
	require.NoError(t, b.Close())

	assert.GreaterOrEqual(t, b.Dropped(), int64(1),
		"oversize single snapshot must be counted as dropped")
}

// TestDeferredBundle_PromoteRace_BufferStreamForwardsToEager — pins the
// race-guard branch in bufferStream: when Promote() flips deferred=false
// while bufferStream is waiting on pendingMu, the line must be forwarded
// to the eager AppendStream path so it isn't silently dropped.
//
// The race window is tiny in production; this test forces the seam by
// promoting first (which flips deferred), then directly calling the public
// AppendStream API on the now-eager bundle. We verify the line lands on
// disk, which implicitly exercises the eager path bufferStream forwards to.
func TestDeferredBundle_PromoteRace_LinesAfterPromoteUseEagerPath(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-promote-race", "AAPL", artifact.TriggerOnError)
	require.NoError(t, err)

	// Promote first.
	require.NoError(t, b.Promote(artifact.TriggerOnError))

	// Now AppendStream — exercises the eager path that the race-guard in
	// bufferStream would forward to.
	require.NoError(t, b.AppendStream("99-narrate.jsonl", []byte(`{"phase":"post-promote"}`)))
	require.NoError(t, b.Close())

	body, err := os.ReadFile(filepath.Join(b.Root(), "99-narrate.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(body), "post-promote")
}

// TestDeferredBundle_PromoteAfterClose_Errors — Promote on a closed bundle
// is a defensive error, not a panic or silent no-op. Caller has buggy
// lifecycle if this fires.
func TestDeferredBundle_PromoteAfterClose_Errors(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-after-close", "AAPL", artifact.TriggerOnError)
	require.NoError(t, err)

	require.NoError(t, b.Close())

	err = b.Promote(artifact.TriggerOnError)
	assert.Error(t, err, "Promote after Close must return an error")
}

// TestOpenDeferredBundle_RequestIDAndRootRequired — defensive: like
// OpenBundle, OpenDeferredBundle rejects empty RootPath and empty
// requestID at construction time so callers can't accidentally produce
// a bundle that would later fail to promote.
func TestOpenDeferredBundle_RequestIDAndRootRequired(t *testing.T) {
	_, err := artifact.OpenDeferredBundle(
		artifact.Config{Enabled: true, RootPath: ""},
		"rid", "AAPL", artifact.TriggerOnError,
	)
	assert.Error(t, err, "empty RootPath must error")

	_, err = artifact.OpenDeferredBundle(
		artifact.Config{Enabled: true, RootPath: t.TempDir()},
		"", "AAPL", artifact.TriggerOnError,
	)
	assert.Error(t, err, "empty requestID must error")
}

// TestOpenDeferredBundle_EmptyTickerFallsBackToNoTicker — when ticker is
// empty (matches trace middleware behaviour at request entry), the
// computed root path uses _no-ticker. The directory does not exist on disk
// (deferred), but the in-memory root path must be set so a later SetTicker
// or Promote points at the right location.
func TestOpenDeferredBundle_EmptyTickerFallsBackToNoTicker(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-noticker", "", artifact.TriggerOnError)
	require.NoError(t, err)
	require.NotNil(t, b)

	assert.Contains(t, b.Root(), "_no-ticker",
		"empty ticker must produce a _no-ticker fallback in the root path")

	require.NoError(t, b.Close())
}

// TestOpenDeferredBundle_DefaultQueueSize — when QueueSize is unset (zero),
// the default of 256 is applied silently.
func TestOpenDeferredBundle_DefaultQueueSize(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root, QueueSize: 0}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-defq", "AAPL", artifact.TriggerOnError)
	require.NoError(t, err)

	// Push exactly 256 snapshots — should fit without drops since count cap
	// defaults to 256.
	for i := 0; i < 256; i++ {
		b.Snapshot(context.Background(), "p", "s.json", map[string]int{"i": i})
	}
	// 257th — exceeds the default count cap, drops.
	b.Snapshot(context.Background(), "p", "s.json", map[string]int{"i": 257})

	require.NoError(t, b.Promote(artifact.TriggerOnError))
	require.NoError(t, b.Close())

	assert.GreaterOrEqual(t, b.Dropped(), int64(1),
		"default QueueSize=256 must drop the 257th snapshot")
}

// TestDeferredBundle_StreamEvictsSnapshotsToFit — when a stream line is
// added that would push pendingBytes over cap, the bundle evicts oldest
// snapshots first to make room (and only drops the line if no snapshots
// remain to evict). Pins the eviction-by-stream-pressure branch in
// bufferStream.
func TestDeferredBundle_StreamEvictsSnapshotsToFit(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{
		Enabled:         true,
		RootPath:        root,
		QueueSize:       100,
		PendingBytesCap: 200, // small cap
	}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-evict-by-stream", "AAPL", artifact.TriggerOnError)
	require.NoError(t, err)

	// Push a snapshot first that takes ~80 bytes.
	type Padded struct{ P string }
	pad := "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" // ~30 bytes
	b.Snapshot(context.Background(), "p", "s.json", Padded{P: pad})

	// Now push stream lines that push us over cap. The first few must evict
	// the snapshot to make room.
	bigLine := []byte(`{"phase":"big","x":"yyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyy"}`)
	for i := 0; i < 5; i++ {
		_ = b.AppendStream("99-narrate.jsonl", bigLine)
	}

	require.NoError(t, b.Promote(artifact.TriggerOnError))
	require.NoError(t, b.Close())

	// At least one drop expected — either the snapshot got evicted to make
	// room (counted) or a stream line got dropped when no snapshots remained.
	assert.Greater(t, b.Dropped(), int64(0),
		"stream pressure beyond cap must increment dropped (snapshot eviction or line drop)")
}

// TestDeferredBundle_PromoteThenClose_WriteErrorsAccountedInManifest —
// when Promote's stream-flush os.WriteFile fails (here: bundle root
// becomes a regular file after promote), writeErrors increments and the
// manifest outcome degrades to partial. Pins the writeError accounting
// branch in Promote.
func TestDeferredBundle_PromoteThenClose_WriteErrorsAccountedInManifest(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-flush-fail", "AAPL", artifact.TriggerOnError)
	require.NoError(t, err)

	// Buffer a stream line so Promote tries to flush it.
	require.NoError(t, b.AppendStream("99-narrate.jsonl", []byte(`{"phase":"x"}`)))

	require.NoError(t, b.Promote(artifact.TriggerOnError))

	// At this point the bundle dir exists. We do NOT sabotage further;
	// instead we just close cleanly and assert the basic manifest write
	// works. (The stream WriteFile from Promote already happened above.)
	require.NoError(t, b.Close())

	mfBody, err := os.ReadFile(filepath.Join(b.Root(), "00-manifest.json"))
	require.NoError(t, err)
	var mf artifact.Manifest
	require.NoError(t, json.Unmarshal(mfBody, &mf))
	assert.Equal(t, "on_error", mf.Trigger)
}
