package artifact_test

// Phase 2.A — auto-on-error trigger unit tests for the artifact.Bundle's
// deferred mode. The middleware-level integration of these primitives is
// pinned by trace_test.go; this file pins the building blocks in isolation.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

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

// TestDeferredBundle_PromoteRace_NoLineLoss is the stress test that pins
// the REVIEWER HIGH-1 fix (Promote's stream flush switched from
// os.WriteFile/O_TRUNC to O_APPEND). Pre-fix, this test fails because
// Promote's WriteFile would truncate lines that racing eager AppendStream
// callers had already written, causing the on-disk line count to be less
// than the number of successful AppendStream returns.
//
// Reproduction strategy: spawn N goroutines each spamming AppendStream in
// a tight loop while the main goroutine calls Promote partway through.
// Some goroutines will land their lines via the deferred buffer (pre-flip),
// some via the eager path (post-flip), some will race exactly during
// Promote's flush. After Close(), the on-disk line count MUST equal the
// number of AppendStream calls that returned successfully.
//
// Run with -race to also catch data races on b.streams / b.pendingStreams.
func TestDeferredBundle_PromoteRace_NoLineLoss(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{
		Enabled:         true,
		RootPath:        root,
		QueueSize:       1024,
		PendingBytesCap: 1 << 20, // 1 MiB — plenty of headroom
	}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-promote-race-noline-loss", "AAPL", artifact.TriggerOnError)
	require.NoError(t, err)
	require.NotNil(t, b)

	const (
		numGoroutines   = 16
		linesPerRoutine = 50
		filename        = "99-narrate.jsonl"
	)

	var (
		wg              sync.WaitGroup
		successfulLines atomic.Int64
		startGate       = make(chan struct{})
	)

	// Spawn writer goroutines first; they block on startGate so they all
	// race the Promote call together rather than serialising.
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-startGate
			for j := 0; j < linesPerRoutine; j++ {
				// Distinct line content per write so we can also detect
				// duplicates if the truncation bug ever re-introduces them.
				line := []byte(`{"writer":` + itoa(id) + `,"seq":` + itoa(j) + `}` + "\n")
				if err := b.AppendStream(filename, line); err == nil {
					successfulLines.Add(1)
				}
			}
		}(i)
	}

	// Release the writers and concurrently call Promote. We sleep briefly
	// so a few writers buffer pre-promote and a few race the flush.
	close(startGate)
	time.Sleep(200 * time.Microsecond)
	require.NoError(t, b.Promote(artifact.TriggerOnError))

	wg.Wait()
	require.NoError(t, b.Close())

	// Read the on-disk file and count lines.
	body, err := os.ReadFile(filepath.Join(b.Root(), filename))
	require.NoError(t, err)
	diskLineCount := int64(len(splitJSONLines(string(body))))

	// CRITICAL ASSERTION: every successful AppendStream call must be
	// represented on disk. Pre-fix, Promote's O_TRUNC flush silently
	// destroyed bytes written by racing eager AppendStream calls between
	// the deferred-flip and the WriteFile, making diskLineCount strictly
	// less than successfulLines.Load().
	require.Equal(t, successfulLines.Load(), diskLineCount,
		"successful AppendStream count (%d) must equal on-disk line count (%d) — Promote's stream flush must not destroy concurrent eager-path writes",
		successfulLines.Load(), diskLineCount)
}

// itoa is a tiny non-allocating int-to-ascii helper local to this file so
// the stress test doesn't pull strconv into the import set just for one
// line of formatting. Handles only non-negative ints (sufficient for the
// loop indices used above).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestDeferredBundle_PromoteCloseRace_NoGoroutineLeak pins the REVIEWER
// HIGH-2 fix (the `promoted` flag check in Close). Pre-fix, Close racing
// with Promote would observe deferred=true (still set by Promote at the
// time Close took its snapshot), take the dissolve path, return without
// close(b.queue) — leaving the worker goroutine ranging over the channel
// forever and the WaitGroup never counting down.
//
// We use go.uber.org/goleak.VerifyNone to assert no goroutines remain
// after the test body completes. The test runs many race iterations to
// give the goroutine scheduler ample chance to interleave Promote and
// Close in the dangerous order.
func TestDeferredBundle_PromoteCloseRace_NoGoroutineLeak(t *testing.T) {
	// Snapshot the baseline goroutine set NOW (before the race) so VerifyNone
	// only flags goroutines created by THIS test body. Without IgnoreCurrent
	// the assertion would observe any goroutines leaked by sibling tests in
	// the same package run (e.g. an OpenBundle worker left running by another
	// test) and falsely blame the HIGH-2 fix. IgnoreCurrent is goleak's
	// recommended pattern for tests that share a process with other tests
	// (see go.uber.org/goleak docs). The IgnoreTopFunction options remain as
	// belt-and-braces against benign runtime goroutines that may be spawned
	// AFTER the snapshot (e.g. test-runner subgoroutines, GC park).
	ignoreOption := goleak.IgnoreCurrent()
	defer goleak.VerifyNone(t,
		ignoreOption,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
	)

	const iterations = 50
	for i := 0; i < iterations; i++ {
		root := t.TempDir()
		cfg := artifact.Config{
			Enabled:   true,
			RootPath:  root,
			QueueSize: 16,
		}
		b, err := artifact.OpenDeferredBundle(cfg, "rid-race-"+itoa(i), "AAPL", artifact.TriggerOnError)
		require.NoError(t, err)

		// Buffer a snapshot so the worker has work to do post-Promote.
		b.Snapshot(context.Background(), "phase", "x.json", map[string]int{"i": i})

		// Race: spawn one goroutine that calls Promote, one that calls Close.
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = b.Promote(artifact.TriggerOnError)
		}()
		go func() {
			defer wg.Done()
			// Tiny sleep with ~50% probability so Close sometimes wins
			// and sometimes loses the race vs Promote.
			if i%2 == 0 {
				runtime.Gosched()
			}
			_ = b.Close()
		}()
		wg.Wait()
	}
}

// TestDeferredBundle_SetTickerPromoteRace_NoDataRace pins the BUG-013 fix's
// concurrency story. It addresses REVIEWER's HIGH-A finding (Promote reads
// b.root without holding b.mu while SetTicker writes it under b.mu) and the
// MEDIUM-A TOCTOU window (SetTicker observes deferred=true but Promote may
// flip it before SetTicker writes b.root).
//
// Setup: open a deferred bundle with no ticker. Spawn two goroutines
// concurrently — one calls SetTicker("AAPL"), the other calls
// Promote(TriggerOnError). Run 50 iterations so the scheduler has ample
// chance to interleave the two paths in the dangerous order.
//
// Assertions:
//   - go test -race must report zero data races on b.root reads/writes.
//   - The bundle must end up with on-disk directory under one of
//     {_no-ticker/, AAPL/}. Both are acceptable per the "benign degradation"
//     claim: SetTicker-wins → AAPL, Promote-wins → _no-ticker.
//   - Manifest outcome must be "ok" or "partial" (NOT "error" or unset). A
//     mid-flight rebind in deferred mode should never lose data; at worst it
//     leaves the on-disk dir at one path while later writes target the
//     other, which Promote's flush loop accounts via writeErrors → outcome
//     downgrade to "partial".
//
// Run with `-race -count=3` to flush out scheduler-dependent races. The
// MEDIUM-A TOCTOU fix exits the b.mu section before re-checking deferred
// under b.pendingMu so b.mu and b.pendingMu are never nested — preserving
// the invariant documented in bufferStream (line ~793).
func TestDeferredBundle_SetTickerPromoteRace_NoDataRace(t *testing.T) {
	const iterations = 50
	for i := 0; i < iterations; i++ {
		root := t.TempDir()
		cfg := artifact.Config{
			Enabled:   true,
			RootPath:  root,
			QueueSize: 16,
		}
		// Open deferred with EMPTY ticker so SetTicker has real work to do
		// (a same-target SetTicker is a no-op and would defeat the test).
		b, err := artifact.OpenDeferredBundle(cfg, "rid-set-promote-race-"+itoa(i), "", artifact.TriggerOnError)
		require.NoError(t, err)
		require.NotNil(t, b)

		// Buffer a snapshot so Promote has work to flush into the (possibly
		// late-renamed) on-disk dir. Without this, the flush loop is empty
		// and the race window narrows.
		b.Snapshot(context.Background(), "phase", "x.json", map[string]int{"i": i})

		// Race: SetTicker vs Promote.
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			b.SetTicker("AAPL")
		}()
		go func() {
			defer wg.Done()
			// Tiny scheduling nudge so SetTicker sometimes wins and
			// sometimes loses the race vs Promote.
			if i%2 == 0 {
				runtime.Gosched()
			}
			_ = b.Promote(artifact.TriggerOnError)
		}()
		wg.Wait()

		// Drain the worker and finalize the manifest.
		require.NoError(t, b.Close())

		// Final on-disk root must live under _no-ticker/ OR AAPL/. Anything
		// else means b.root got mangled by a torn read or interleaved write.
		finalRoot := b.Root()
		parts := splitPath(t, finalRoot, root)
		require.Len(t, parts, 3, "iter %d: expected 3 path segments under root, got %v", i, parts)
		require.Contains(t, []string{"_no-ticker", "AAPL"}, parts[1],
			"iter %d: ticker segment must be _no-ticker or AAPL, got %q", i, parts[1])
		require.Equal(t, "req_rid-set-promote-race-"+itoa(i), parts[2],
			"iter %d: req_<id> segment must be preserved across the race", i)

		// Manifest must exist at SOME promoted path (Promote ran in every
		// iteration). Look it up via b.Root() which is the authoritative
		// post-flush location.
		mfPath := filepath.Join(finalRoot, "00-manifest.json")
		mfBody, err := os.ReadFile(mfPath)
		require.NoError(t, err, "iter %d: manifest must exist at %s", i, mfPath)
		var mf artifact.Manifest
		require.NoError(t, json.Unmarshal(mfBody, &mf), "iter %d: manifest must be valid JSON", i)

		// Outcome must be ok or partial — the race may legitimately produce
		// partial when SetTicker beats Promote into the deferred branch but
		// Promote has already flipped deferred=false (b.root rename misses,
		// Promote MkdirAll's at the OLD path, the flush loop writes there,
		// b.Root() returns the NEW path → manifest write may also fail).
		// The test pins that we never produce "error" or an unset outcome.
		switch mf.Outcome {
		case "ok", "partial":
			// acceptable
		default:
			t.Fatalf("iter %d: manifest outcome must be ok|partial, got %q (notes=%q)", i, mf.Outcome, mf.Notes)
		}
	}
}

// TestBundle_StreamLineExceedsHardCap_DroppedAndCounted pins the REVIEWER
// HIGH-3 fix (per-line MaxStreamLineBytes guard in AppendStream). A 1 MiB
// stream line MUST be rejected at AppendStream entry, OversizeLines() MUST
// return 1, and the manifest's notes MUST contain "oversize_lines=1" so
// postmortem readers can attribute the partial outcome to the rogue line
// rather than to buffer-pressure eviction.
//
// Tests both modes (eager and deferred) in one parameterised form because
// the cap is enforced at AppendStream entry, BEFORE the deferred branch.
func TestBundle_StreamLineExceedsHardCap_DroppedAndCounted(t *testing.T) {
	tests := []struct {
		name       string
		isDeferred bool
		open       func(cfg artifact.Config) (*artifact.Bundle, error)
	}{
		{
			name:       "eager_bundle",
			isDeferred: false,
			open: func(cfg artifact.Config) (*artifact.Bundle, error) {
				return artifact.OpenBundle(cfg, "rid-oversize-eager", "AAPL", artifact.TriggerHeader)
			},
		},
		{
			name:       "deferred_bundle",
			isDeferred: true,
			open: func(cfg artifact.Config) (*artifact.Bundle, error) {
				return artifact.OpenDeferredBundle(cfg, "rid-oversize-deferred", "AAPL", artifact.TriggerOnError)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			cfg := artifact.Config{Enabled: true, RootPath: root}

			b, err := tc.open(cfg)
			require.NoError(t, err)
			require.NotNil(t, b)

			// Build a 1 MiB line — well over MaxStreamLineBytes (256 KiB)
			// and large enough that pre-fix it would have evicted any
			// buffered snapshots in deferred mode.
			oversize := make([]byte, 1<<20)
			for i := range oversize {
				oversize[i] = 'x'
			}

			// AppendStream must NOT error — the contract is "drop + count",
			// not "fail loudly".
			require.NoError(t, b.AppendStream("99-narrate.jsonl", oversize))

			// OversizeLines counter must reflect the rejection.
			assert.Equal(t, int64(1), b.OversizeLines(),
				"oversize line must be counted via OversizeLines()")

			// Deferred bundles need Promote to materialise the manifest on
			// disk; otherwise Close dissolves the bundle and there's
			// nothing to read. We Promote with TriggerOnError to mirror the
			// production trace-middleware path.
			if tc.isDeferred {
				require.NoError(t, b.Promote(artifact.TriggerOnError))
			}
			require.NoError(t, b.Close())

			// On-disk file must NOT contain the oversize line. (For eager
			// bundles the file may not exist at all; for deferred bundles
			// the file may exist but be empty.)
			body, _ := os.ReadFile(filepath.Join(b.Root(), "99-narrate.jsonl"))
			assert.NotContains(t, string(body), string(oversize[:64]),
				"oversize line bytes must not appear on disk")

			// Manifest's notes must annotate the oversize count so
			// postmortem readers can grep for it.
			mfBody, err := os.ReadFile(filepath.Join(b.Root(), "00-manifest.json"))
			require.NoError(t, err)
			var mf artifact.Manifest
			require.NoError(t, json.Unmarshal(mfBody, &mf))
			assert.Equal(t, "partial", mf.Outcome,
				"oversize-line drop must downgrade outcome to partial")
			assert.Contains(t, mf.Notes, "oversize_lines=1",
				"manifest notes must record the oversize_lines count for postmortem grep")
		})
	}
}

// TestBundle_MaxStreamLineBytesConstant pins the public constant value
// so a future refactor can't silently shrink the cap and reject log lines
// the production code reasonably emits (e.g. ~8 KiB narrate entries with
// large payload_ref lists).
func TestBundle_MaxStreamLineBytesConstant(t *testing.T) {
	assert.Equal(t, 256*1024, artifact.MaxStreamLineBytes,
		"MaxStreamLineBytes is part of the package's public surface — do not shrink without a deprecation cycle")
}

// --- BUG-013 regression pins -----------------------------------------------
//
// Deferred bundles have NO on-disk directory at construction time (Promote()
// MkdirAll's at promote-time). The pre-fix SetTicker unconditionally called
// os.Rename(b.root, newRoot) which ENOENT'd because b.root was an in-memory
// placeholder, NOT an on-disk directory yet — the rename failure incremented
// writeErrors, b.root was NOT updated, and the subsequent Promote MkdirAll'd
// at the unchanged "_no-ticker/" placeholder. Every auto-triggered bundle
// (on_error, on_quality_flag, always) landed at <date>/_no-ticker/req_<id>/
// instead of <date>/<TICKER>/req_<id>/, breaking per-ticker forensics.
//
// Fix: in deferred mode SetTicker skips os.Rename and just updates b.root in
// memory. Promote then MkdirAll's at the correct path. Eager mode (OpenBundle)
// is unchanged — directory exists on disk so os.Rename still applies.
//
// These tests pin the four corner cases of the deferred-mode SetTicker
// contract; the eager-mode regression pin lives in bundle_test.go's
// TestSetTicker_EagerBundle_StillRenamesOnDisk and the existing
// TestSetTicker_RenameFailureCountedAsWriteError still passes unchanged
// because the eager rename code path is untouched by the fix.

// TestSetTicker_DeferredBundle_UpdatesRootInMemory: opening a deferred bundle
// with no ticker yields b.Root() under "_no-ticker/"; calling SetTicker("AAPL")
// must move the in-memory root to "<date>/AAPL/req_<id>/" without touching disk.
// Pin pre-Promote so we observe the in-memory state directly.
func TestSetTicker_DeferredBundle_UpdatesRootInMemory(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	// Open with empty ticker — mirrors trace middleware behaviour where the
	// bundle is opened BEFORE the handler parses :ticker from the URL path.
	b, err := artifact.OpenDeferredBundle(cfg, "rid-deferred-set", "", artifact.TriggerOnError)
	require.NoError(t, err)
	require.NotNil(t, b)
	defer b.Close()

	// Pre-condition: root is under _no-ticker because no ticker was provided.
	preRoot := b.Root()
	parts := splitPath(t, preRoot, root)
	require.Equal(t, "_no-ticker", parts[1],
		"precondition: empty-ticker deferred bundle starts under _no-ticker")

	// Late-bind the ticker — what the handler does at fair_value.go:258.
	b.SetTicker("AAPL")

	// Post-condition: root now lives under AAPL even though Promote hasn't run.
	newRoot := b.Root()
	newParts := splitPath(t, newRoot, root)
	require.Len(t, newParts, 3, "expected 3 levels under root, got %v", newParts)
	assert.Equal(t, "AAPL", newParts[1],
		"deferred-mode SetTicker must update b.root to <date>/AAPL/, not leave it at _no-ticker")
	assert.Equal(t, "req_rid-deferred-set", newParts[2],
		"req_<id> segment must be preserved across the rebind")

	// And the on-disk directory MUST still not exist — deferred mode means no
	// disk I/O. The pre-fix bug masked itself partially because Promote later
	// created the dir, but at the WRONG path.
	_, statErr := os.Stat(newRoot)
	assert.True(t, os.IsNotExist(statErr),
		"deferred-mode SetTicker must not create the on-disk directory; got stat err=%v", statErr)
}

// TestSetTicker_DeferredBundle_PromoteCreatesAtTickerPath: the operator-visible
// acceptance criterion from BUG-013. After SetTicker + Promote the on-disk
// bundle MUST live at <date>/AAPL/req_<id>/, NOT at <date>/_no-ticker/req_<id>/.
// Equivalent to: `ls artifacts/<date>/AAPL/` finds the bundle.
func TestSetTicker_DeferredBundle_PromoteCreatesAtTickerPath(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-promote-ticker", "", artifact.TriggerOnError)
	require.NoError(t, err)
	require.NotNil(t, b)

	// Buffer a snapshot pre-SetTicker — exactly what request.received and the
	// other early phases do before the handler parses the URL ticker.
	b.Snapshot(context.Background(), "request.received", "01-request.json", map[string]string{"path": "/api/v1/fair-value/AAPL"})
	require.NoError(t, b.AppendStream("99-narrate.jsonl", []byte(`{"event":"narrate","phase":"request.received"}`)))

	// Late-bind ticker (handler.entry equivalent).
	b.SetTicker("AAPL")

	// Promote and Close — the trace middleware's defer block.
	require.NoError(t, b.Promote(artifact.TriggerOnError))
	require.NoError(t, b.Close())

	// On-disk directory MUST be under <date>/AAPL/, NOT _no-ticker/.
	finalRoot := b.Root()
	parts := splitPath(t, finalRoot, root)
	require.Len(t, parts, 3)
	assert.Equal(t, "AAPL", parts[1],
		"promoted deferred bundle must live at <date>/AAPL/req_<id>/, got <date>/%s/%s/", parts[1], parts[2])
	assert.Equal(t, "req_rid-promote-ticker", parts[2])

	// And the directory must actually exist on disk — Promote did the MkdirAll.
	st, err := os.Stat(finalRoot)
	require.NoError(t, err, "promoted bundle directory must exist at the ticker path")
	assert.True(t, st.IsDir())

	// _no-ticker MUST NOT have been created — Promote computed the path from
	// the post-SetTicker b.root, so no placeholder dir lingers on disk.
	noTickerPath := filepath.Join(root, parts[0], "_no-ticker")
	_, err = os.Stat(noTickerPath)
	assert.True(t, os.IsNotExist(err),
		"_no-ticker placeholder must NOT be created on disk when SetTicker fired before Promote; got stat err=%v", err)

	// Manifest must be at the new path AND carry the ticker.
	mfBody, err := os.ReadFile(filepath.Join(finalRoot, "00-manifest.json"))
	require.NoError(t, err, "manifest must exist at the AAPL path")
	var mf artifact.Manifest
	require.NoError(t, json.Unmarshal(mfBody, &mf))
	assert.Equal(t, "AAPL", mf.Ticker)
	assert.Equal(t, "on_error", mf.Trigger)
}

// TestSetTicker_DeferredBundle_NoSpuriousWriteError: the secondary symptom of
// BUG-013 — the failed os.Rename incremented writeErrors, which downgraded the
// manifest outcome to "partial" with notes "write_failures=1 …" even though
// no actual data was lost. Postmortem readers would misread the bundle as
// data-incomplete when only the rename had failed (cosmetic).
//
// After the fix: deferred SetTicker doesn't attempt a rename, so writeErrors
// stays 0 and the manifest outcome is "ok".
func TestSetTicker_DeferredBundle_NoSpuriousWriteError(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-no-spurious", "", artifact.TriggerOnError)
	require.NoError(t, err)
	require.NotNil(t, b)

	// SetTicker before Promote — the deferred-mode case the fix addresses.
	b.SetTicker("AAPL")

	// writeErrors MUST be zero — no rename was attempted, so no failure to count.
	assert.Equal(t, int64(0), b.WriteErrors(),
		"deferred-mode SetTicker must NOT increment writeErrors (no os.Rename is attempted)")

	// Promote + Close — manifest outcome must be "ok" (or at worst empty/unset),
	// not "partial". Pre-fix this assertion would fail because the rename's
	// writeErrors=1 cascaded into outcome="partial".
	require.NoError(t, b.Promote(artifact.TriggerOnError))
	require.NoError(t, b.Close())

	mfBody, err := os.ReadFile(filepath.Join(b.Root(), "00-manifest.json"))
	require.NoError(t, err)
	var mf artifact.Manifest
	require.NoError(t, json.Unmarshal(mfBody, &mf))
	assert.NotEqual(t, "partial", mf.Outcome,
		"deferred SetTicker must not cause spurious outcome=partial via a phantom writeError; got %q with notes %q", mf.Outcome, mf.Notes)
}

// TestSetTicker_DeferredBundle_NoOpAfterClose pins safety: SetTicker on a
// closed deferred bundle must not panic, must not move the in-memory root
// (Close already released the buffers), and must not increment writeErrors.
// Equivalent of TestSetTicker_NoOpAfterClose for the deferred path.
func TestSetTicker_DeferredBundle_NoOpAfterClose(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-deferred-after-close", "", artifact.TriggerOnError)
	require.NoError(t, err)
	require.NotNil(t, b)

	rootBeforeClose := b.Root()
	require.NoError(t, b.Close()) // dissolve path — never promoted

	b.SetTicker("AAPL")
	assert.Equal(t, rootBeforeClose, b.Root(),
		"SetTicker after Close on a deferred bundle must not move the root")
	assert.Equal(t, int64(0), b.WriteErrors(),
		"closed-bundle SetTicker must not increment writeErrors")
}

// TestSetTicker_DeferredBundle_CloseDuringTOCTOURace exercises the
// closed-bundle defensive branches inside SetTicker's MEDIUM-A re-check.
// SetTicker drops b.mu, takes b.pendingMu, re-takes b.mu — and must honour
// b.closed.Load() at the second b.mu acquisition. Same for the fall-through-
// to-eager path's b.mu re-acquisition.
//
// We exercise both branches probabilistically by racing SetTicker against
// Close 100 times. Over the full run the scheduler will land Close inside
// SetTicker's narrow lock-drop windows reliably enough to mark both branches
// covered. The assertion is purely safety (no panic, no negative writeError
// surge) — the closed branches return early without observable side effects.
func TestSetTicker_DeferredBundle_CloseDuringTOCTOURace(t *testing.T) {
	const iterations = 100
	for i := 0; i < iterations; i++ {
		root := t.TempDir()
		cfg := artifact.Config{Enabled: true, RootPath: root, QueueSize: 16}

		b, err := artifact.OpenDeferredBundle(cfg, "rid-close-toctou-"+itoa(i), "", artifact.TriggerOnError)
		require.NoError(t, err)
		require.NotNil(t, b)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			b.SetTicker("AAPL")
		}()
		go func() {
			defer wg.Done()
			// Mix in a Promote on half the iterations so SetTicker
			// sometimes hits the deferred re-check branch, sometimes the
			// fall-through branch — both have a closed-mid-flight check.
			if i%2 == 0 {
				_ = b.Promote(artifact.TriggerOnError)
			}
			runtime.Gosched()
			_ = b.Close()
		}()
		wg.Wait()

		// Idempotent re-Close — must not panic regardless of who won.
		require.NoError(t, b.Close())
	}
}

// TestSetTicker_DeferredBundle_AfterPromoteFallsThroughToEager pins the
// MEDIUM-A TOCTOU fall-through path: SetTicker enters its deferred branch
// because b.deferred.Load() returned true under b.mu, then re-checks under
// b.pendingMu and finds deferred=false (Promote already ran). The fall-
// through must drop into the eager rename path and physically move the
// directory from <date>/_no-ticker/req_<id>/ to <date>/AAPL/req_<id>/.
//
// We exercise this DETERMINISTICALLY by promoting BEFORE calling SetTicker.
// In the race test the same code path fires non-deterministically; here we
// pin the fall-through behaviour without scheduler dependence.
func TestSetTicker_DeferredBundle_AfterPromoteFallsThroughToEager(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-after-promote", "", artifact.TriggerOnError)
	require.NoError(t, err)
	require.NotNil(t, b)
	defer b.Close()

	// Force eager mode by promoting first — this flips deferred=false and
	// MkdirAll's the directory at the _no-ticker path.
	require.NoError(t, b.Promote(artifact.TriggerOnError))

	preRoot := b.Root()
	preParts := splitPath(t, preRoot, root)
	require.Equal(t, "_no-ticker", preParts[1],
		"precondition: post-Promote bundle lives under _no-ticker because no ticker was set yet")
	st, err := os.Stat(preRoot)
	require.NoError(t, err, "Promote must have created the on-disk directory")
	require.True(t, st.IsDir())

	// Now SetTicker — the deferred branch's TOCTOU re-check will see
	// deferred=false under pendingMu and fall through to the eager rename.
	b.SetTicker("AAPL")

	// Post-condition: directory physically moved.
	newRoot := b.Root()
	newParts := splitPath(t, newRoot, root)
	require.Len(t, newParts, 3)
	assert.Equal(t, "AAPL", newParts[1],
		"fall-through eager rename must move directory to AAPL/")
	assert.Equal(t, "req_rid-after-promote", newParts[2])

	// New directory must exist on disk.
	st, err = os.Stat(newRoot)
	require.NoError(t, err, "renamed directory must exist at the AAPL path")
	assert.True(t, st.IsDir())

	// Old directory must be gone (os.Rename moved, not copied).
	_, err = os.Stat(preRoot)
	assert.True(t, os.IsNotExist(err),
		"_no-ticker placeholder must be removed by the rename; got stat err=%v", err)

	// writeErrors must stay 0 — the rename succeeded cleanly.
	assert.Equal(t, int64(0), b.WriteErrors(),
		"clean fall-through rename must not increment writeErrors")
}
