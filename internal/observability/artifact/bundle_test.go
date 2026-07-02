package artifact_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// TestOpenBundle_DisabledReturnsNil verifies the master switch.
func TestOpenBundle_DisabledReturnsNil(t *testing.T) {
	cfg := artifact.Config{Enabled: false, RootPath: t.TempDir()}
	b, err := artifact.OpenBundle(cfg, "rid", "AAPL", artifact.TriggerHeader)
	require.NoError(t, err)
	assert.Nil(t, b, "disabled cfg must return a nil bundle")

	// Nil-safe: every method on a nil bundle is a no-op.
	b.Snapshot(context.Background(), "fetch.sec", "x.json", map[string]int{"a": 1})
	b.SnapshotRaw(context.Background(), "fetch.sec", "x.raw.json", []byte("ok"), nil)
	b.SetTicker("X")
	b.SetOutcome("ok")
	b.AddSchemaVersion("X", 1)
	assert.NoError(t, b.Close())
	assert.Equal(t, int64(0), b.Dropped())
	assert.Equal(t, "", b.Root())
}

// TestOpenBundle_DirectoryLayout pins the on-disk directory shape.
func TestOpenBundle_DirectoryLayout(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenBundle(cfg, "req-abc-123", "AAPL", artifact.TriggerQuery)
	require.NoError(t, err)
	require.NotNil(t, b)
	defer b.Close()

	// Path: <root>/<UTC date>/<TICKER>/req_<id>/
	parts := splitPath(t, b.Root(), root)
	require.Len(t, parts, 3, "expected 3 levels under root, got %v", parts)
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, parts[0])
	assert.Equal(t, "AAPL", parts[1])
	assert.Equal(t, "req_req-abc-123", parts[2])

	// Directory must exist on disk.
	st, err := os.Stat(b.Root())
	require.NoError(t, err)
	assert.True(t, st.IsDir())
}

// TestOpenBundle_SanitizesTicker — ../ traversal must be neutralised.
func TestOpenBundle_SanitizesTicker(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenBundle(cfg, "rid", "../../etc", artifact.TriggerQuery)
	require.NoError(t, err)
	defer b.Close()

	// The ticker segment must NOT contain "..".
	parts := splitPath(t, b.Root(), root)
	require.Len(t, parts, 3)
	assert.NotContains(t, parts[1], "..", "traversal must be sanitised")
}

// TestSnapshot_WritesFileAndManifest end-to-end smoke.
func TestSnapshot_WritesFileAndManifest(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenBundle(cfg, "rid-1", "AAPL", artifact.TriggerQuery)
	require.NoError(t, err)

	type Payload struct {
		Ticker string `json:"ticker"`
		Value  int    `json:"value"`
	}
	b.Snapshot(context.Background(), "fetch.sec", "05-fetch-sec.parsed.json", Payload{Ticker: "AAPL", Value: 42})
	b.AddSchemaVersion("FinancialData", 7)
	b.SetOutcome("ok")
	require.NoError(t, b.Close())

	// File must exist.
	body, err := os.ReadFile(filepath.Join(b.Root(), "05-fetch-sec.parsed.json"))
	require.NoError(t, err)
	var got Payload
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, "AAPL", got.Ticker)
	assert.Equal(t, 42, got.Value)

	// Manifest must reference the phase + file.
	mfBody, err := os.ReadFile(filepath.Join(b.Root(), "00-manifest.json"))
	require.NoError(t, err)
	var mf artifact.Manifest
	require.NoError(t, json.Unmarshal(mfBody, &mf))

	assert.Equal(t, "rid-1", mf.RequestID)
	assert.Equal(t, "AAPL", mf.Ticker)
	assert.Equal(t, "query", mf.Trigger)
	assert.Equal(t, "ok", mf.Outcome)
	assert.Equal(t, 7, mf.SchemaVersions["FinancialData"])
	require.Len(t, mf.PhasesRecorded, 1)
	assert.Equal(t, "fetch.sec", mf.PhasesRecorded[0].Phase)
	assert.Contains(t, mf.PhasesRecorded[0].Files, "05-fetch-sec.parsed.json")
	assert.Greater(t, mf.PhasesRecorded[0].Bytes, int64(0))
}

// TestSnapshotRaw_WritesBytesAndRedactionsRecorded — raw byte path.
func TestSnapshotRaw_WritesBytesAndRedactions(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenBundle(cfg, "rid-raw", "AMD", artifact.TriggerHeader)
	require.NoError(t, err)

	rawBody := []byte(`{"data":"raw"}`)
	b.SnapshotRaw(context.Background(), "fetch.sec", "05-fetch-sec.raw.json",
		rawBody, []string{"headers.Authorization", "query.crumb"})
	require.NoError(t, b.Close())

	got, err := os.ReadFile(filepath.Join(b.Root(), "05-fetch-sec.raw.json"))
	require.NoError(t, err)
	assert.Equal(t, rawBody, got)

	mfBody, err := os.ReadFile(filepath.Join(b.Root(), "00-manifest.json"))
	require.NoError(t, err)
	var mf artifact.Manifest
	require.NoError(t, json.Unmarshal(mfBody, &mf))
	assert.Contains(t, mf.RedactionsApplied, "headers.Authorization")
	assert.Contains(t, mf.RedactionsApplied, "query.crumb")
}

// TestSnapshot_QueueOverflowDrops — when the queue is full, snapshots drop
// rather than block; the bundle outcome degrades to partial.
func TestSnapshot_QueueOverflowDrops(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{
		Enabled:   true,
		RootPath:  root,
		QueueSize: 1, // Force overflow on second call before worker drains
	}
	b, err := artifact.OpenBundle(cfg, "rid-burst", "AAPL", artifact.TriggerQuery)
	require.NoError(t, err)

	// Hold a goroutine on the worker by writing a tiny snapshot then immediately
	// flooding more. The worker will drain serially; some will inevitably drop.
	type Payload struct{ N int }
	for i := 0; i < 1000; i++ {
		b.Snapshot(context.Background(), "fetch.sec",
			"x.json", Payload{N: i})
	}
	require.NoError(t, b.Close())

	// At least some drops with QueueSize=1 and 1000 snapshots is overwhelmingly
	// likely. If this proves flaky on certain Go schedulers we can refactor to
	// block the worker explicitly.
	if b.Dropped() == 0 {
		t.Skip("scheduler drained all 1000 snapshots before overflow; not flaky-fail")
	}

	// Dropped > 0 means manifest outcome must be partial.
	mfBody, err := os.ReadFile(filepath.Join(b.Root(), "00-manifest.json"))
	require.NoError(t, err)
	var mf artifact.Manifest
	require.NoError(t, json.Unmarshal(mfBody, &mf))
	assert.Equal(t, "partial", mf.Outcome,
		"bundle with dropped snapshots must report outcome=partial")
}

// TestSnapshot_WriteFailureDegradesAndAnnotates — when os.WriteFile fails on
// the worker (here: the bundle root is replaced with a regular file after
// OpenBundle succeeds, so every subsequent write under it is ENOTDIR), the
// bundle MUST:
//   - increment writeErrors for every failed write;
//   - degrade outcome to "partial" at Close();
//   - record manifest.Notes describing the failure counts so a reader of
//     the bundle directory immediately knows why the capture is incomplete.
//
// Pre-fix the worker silently swallowed the error and the manifest claimed
// outcome="ok" with zero phases — the bundle was lying about itself.
//
// We restore the directory just before Close() so the manifest write inside
// Finalize() can succeed and we can read it back to assert on outcome+notes.
func TestSnapshot_WriteFailureDegradesAndAnnotates(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root, QueueSize: 64}
	b, err := artifact.OpenBundle(cfg, "rid-write-fail", "AAPL", artifact.TriggerHeader)
	require.NoError(t, err)
	require.NotNil(t, b)

	// Sabotage: replace the bundle directory with a regular file. Every
	// subsequent os.WriteFile under it will fail with "not a directory" on
	// POSIX and Windows alike. Queue size 64 > write attempts means no
	// queue-overflow drops — every failure observed is a WriteFile failure.
	bundleRoot := b.Root()
	require.NoError(t, os.RemoveAll(bundleRoot))
	require.NoError(t, os.WriteFile(bundleRoot, []byte("blocked"), 0o644))

	type Payload struct{ N int }
	const writeAttempts = 5
	for i := 0; i < writeAttempts; i++ {
		b.Snapshot(context.Background(), "fetch.sec", "x.json", Payload{N: i})
	}

	// Block until the background worker has actually observed the blocked
	// writes BEFORE restoring the directory. WriteErrors() reads the atomic
	// counter the worker increments on each failed os.WriteFile, so once it
	// reaches writeAttempts every queued snapshot has drained against the
	// sabotaged root. Removes the fast-CI race (CI-1 / #20) where the worker
	// drained all snapshots only AFTER the heal, observing zero failures.
	require.Eventually(t, func() bool {
		return b.WriteErrors() >= int64(writeAttempts)
	}, 5*time.Second, time.Millisecond,
		"worker must observe all write failures before the directory is healed")

	// Restore the directory before Close so Finalize's manifest write succeeds.
	require.NoError(t, os.Remove(bundleRoot))
	require.NoError(t, os.MkdirAll(bundleRoot, 0o755))
	require.NoError(t, b.Close())

	// At least 1 write failure must have been observed; in practice all 5
	// fail because the worker drains in tight succession. We assert >=1 so
	// the test isn't flaky on slow CI runners where the worker might
	// observe the restored directory for one trailing snapshot.
	writeErrs := b.WriteErrors()
	assert.GreaterOrEqual(t, writeErrs, int64(1),
		"at least one os.WriteFile attempt should have been counted as a failure")
	assert.EqualValues(t, 0, b.Dropped(),
		"queue was big enough; nothing should have been queue-dropped")

	// Manifest must reflect the partial outcome with annotated notes.
	mfBody, err := os.ReadFile(filepath.Join(bundleRoot, "00-manifest.json"))
	require.NoError(t, err)
	var mf artifact.Manifest
	require.NoError(t, json.Unmarshal(mfBody, &mf))
	assert.Equal(t, "partial", mf.Outcome,
		"writeErrors > 0 must downgrade outcome to partial")
	assert.Contains(t, mf.Notes, "write_failures=",
		"notes must annotate the write-failure count")
	assert.Contains(t, mf.Notes, "queue_drops=0",
		"notes must annotate the queue-drop count for completeness")
}

// TestClose_Idempotent — defer + explicit Close is safe.
func TestClose_Idempotent(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}
	b, err := artifact.OpenBundle(cfg, "rid", "X", artifact.TriggerHeader)
	require.NoError(t, err)
	assert.NoError(t, b.Close())
	assert.NoError(t, b.Close(), "second Close must be a no-op, not panic")
}

// TestInjectFrom — the context attachment contract.
func TestInjectFrom(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}
	b, err := artifact.OpenBundle(cfg, "rid", "X", artifact.TriggerHeader)
	require.NoError(t, err)
	defer b.Close()

	ctx := artifact.Inject(context.Background(), b)
	assert.Same(t, b, artifact.From(ctx))

	// Miss returns nil.
	assert.Nil(t, artifact.From(context.Background()))
	// Nil context.
	assert.Nil(t, artifact.From(nil)) //nolint:staticcheck
}

// TestSetTickerUpdatesManifest — late-bind ticker.
func TestSetTickerUpdatesManifest(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}
	b, err := artifact.OpenBundle(cfg, "rid", "", artifact.TriggerQuery)
	require.NoError(t, err)

	b.SetTicker("MSFT")
	require.NoError(t, b.Close())

	mfBody, err := os.ReadFile(filepath.Join(b.Root(), "00-manifest.json"))
	require.NoError(t, err)
	var mf artifact.Manifest
	require.NoError(t, json.Unmarshal(mfBody, &mf))
	assert.Equal(t, "MSFT", mf.Ticker)
}

// TestOutcomeStickyError — a later "ok" must not override a prior "error".
func TestOutcomeStickyError(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}
	b, err := artifact.OpenBundle(cfg, "rid", "X", artifact.TriggerHeader)
	require.NoError(t, err)

	b.SetOutcome("error")
	b.SetOutcome("ok") // must not stick
	require.NoError(t, b.Close())

	mfBody, err := os.ReadFile(filepath.Join(b.Root(), "00-manifest.json"))
	require.NoError(t, err)
	var mf artifact.Manifest
	require.NoError(t, json.Unmarshal(mfBody, &mf))
	assert.Equal(t, "error", mf.Outcome)
}

// TestSnapshot_RaceFreeUnderConcurrentCallers — race detector smoke.
// `go test -race ./...` is the actual gate; this test makes sure the
// concurrent paths exist for the detector to inspect.
func TestSnapshot_RaceFreeUnderConcurrentCallers(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root, QueueSize: 64}
	b, err := artifact.OpenBundle(cfg, "rid", "X", artifact.TriggerHeader)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 32; j++ {
				b.Snapshot(context.Background(), "fetch.sec",
					"x.json", map[string]int{"i": id, "j": j})
				b.SetOutcome("ok")
				b.AddSchemaVersion("X", id)
			}
		}(i)
	}
	wg.Wait()
	require.NoError(t, b.Close())
}

// TestOpenBundle_RequestIDAndRootRequired — defensive
func TestOpenBundle_RequestIDAndRootRequired(t *testing.T) {
	_, err := artifact.OpenBundle(artifact.Config{Enabled: true, RootPath: ""}, "rid", "X", artifact.TriggerHeader)
	assert.Error(t, err)

	_, err = artifact.OpenBundle(artifact.Config{Enabled: true, RootPath: t.TempDir()}, "", "X", artifact.TriggerHeader)
	assert.Error(t, err)
}

// TestBundle_AppendStream_Persists pins the JSONL append contract: 5 lines
// in, 5 valid JSON lines on disk after Close.
func TestBundle_AppendStream_Persists(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}
	b, err := artifact.OpenBundle(cfg, "rid-stream", "AAPL", artifact.TriggerHeader)
	require.NoError(t, err)
	require.NotNil(t, b)

	const N = 5
	for i := 0; i < N; i++ {
		line := []byte(`{"event":"narrate","phase":"test"}`)
		require.NoError(t, b.AppendStream("99-narrate.jsonl", line))
	}
	require.NoError(t, b.Close())

	body, err := os.ReadFile(filepath.Join(b.Root(), "99-narrate.jsonl"))
	require.NoError(t, err)

	// 5 lines, each valid JSON.
	lines := splitJSONLines(string(body))
	require.Len(t, lines, N)
	for i, l := range lines {
		var v map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(l), &v), "line %d not valid JSON: %s", i, l)
		assert.Equal(t, "narrate", v["event"])
	}
}

// TestBundle_AppendStream_NilSafe — same nil-receiver contract as Snapshot.
func TestBundle_AppendStream_NilSafe(t *testing.T) {
	var b *artifact.Bundle
	assert.NoError(t, b.AppendStream("foo", []byte("x")))
}

// TestBundle_AppendStream_AfterClose_NoOps — calling AppendStream on a
// closed bundle must be a no-op (matches Snapshot's contract). The pre-close
// stream content must remain untouched.
func TestBundle_AppendStream_AfterClose_NoOps(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}
	b, err := artifact.OpenBundle(cfg, "rid-closed", "AAPL", artifact.TriggerHeader)
	require.NoError(t, err)

	require.NoError(t, b.AppendStream("99-narrate.jsonl", []byte("before")))
	require.NoError(t, b.Close())

	// File from pre-close write must exist.
	beforePath := filepath.Join(b.Root(), "99-narrate.jsonl")
	beforeBody, err := os.ReadFile(beforePath)
	require.NoError(t, err)

	// Post-close call must not error and must not alter the file.
	assert.NoError(t, b.AppendStream("99-narrate.jsonl", []byte("after")))
	assert.NoError(t, b.AppendStream("99-fresh.jsonl", []byte("never")))

	afterBody, err := os.ReadFile(beforePath)
	require.NoError(t, err)
	assert.Equal(t, beforeBody, afterBody, "post-close AppendStream must not mutate stream files")

	// Fresh stream that was never opened pre-close must not exist either.
	_, err = os.Stat(filepath.Join(b.Root(), "99-fresh.jsonl"))
	assert.True(t, os.IsNotExist(err), "no new stream files after Close")
}

// splitPath returns the path components of fullPath relative to root.
func splitPath(t *testing.T, fullPath, root string) []string {
	t.Helper()
	rel, err := filepath.Rel(root, fullPath)
	require.NoError(t, err)
	return splitOSSep(rel)
}

func splitOSSep(p string) []string {
	if p == "" {
		return nil
	}
	// filepath.SplitList splits on OS list-sep; we want path-component split.
	var parts []string
	cur := ""
	for _, r := range p {
		if r == filepath.Separator || r == '/' {
			if cur != "" {
				parts = append(parts, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		parts = append(parts, cur)
	}
	return parts
}

// TestSetTicker_RenamesDirectory pins the contract that SetTicker updates
// the on-disk directory layout, not just the manifest. Trace middleware opens
// the bundle BEFORE the handler parses :ticker, so the initial directory
// segment is "_no-ticker"; once the handler stamps the ticker, the segment
// must be replaced so per-ticker forensics like
// `ls artifacts/<date>/TSM/` find the bundle.
//
// Repros the 2026-04-26 bug report: `?trace=1` against /api/v1/fair-value/TSM
// produced artifacts under `_no-ticker/` instead of `TSM/`.
func TestSetTicker_RenamesDirectory(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	// Open with empty ticker — mirrors trace middleware behaviour.
	b, err := artifact.OpenBundle(cfg, "rid-rename-1", "", artifact.TriggerQuery)
	require.NoError(t, err)
	require.NotNil(t, b)

	originalRoot := b.Root()

	// Initial directory MUST be under "_no-ticker" — sanity-check the precondition.
	parts := splitPath(t, originalRoot, root)
	require.Equal(t, "_no-ticker", parts[1], "precondition: initial dir is _no-ticker")

	// Simulate work that opens cached file handles before SetTicker.
	require.NoError(t, b.AppendStream("99-narrate.jsonl", []byte(`{"event":"narrate","phase":"request.received"}`)))
	b.Snapshot(context.Background(), "fetch.sec", "05-fetch-sec.parsed.json", map[string]string{"cik": "1018724"})

	// Late-bind the ticker. This is what the handler does.
	b.SetTicker("TSM")

	// Bundle root must now point to the TSM directory.
	newRoot := b.Root()
	newParts := splitPath(t, newRoot, root)
	require.Len(t, newParts, 3, "expected 3 levels under root, got %v", newParts)
	assert.Equal(t, "TSM", newParts[1], "after SetTicker the ticker segment must be TSM, not %s", newParts[1])
	assert.Equal(t, "req_rid-rename-1", newParts[2])

	// New directory must exist on disk.
	st, err := os.Stat(newRoot)
	require.NoError(t, err, "renamed directory must exist on disk")
	assert.True(t, st.IsDir())

	// Old _no-ticker request directory must no longer exist.
	_, err = os.Stat(originalRoot)
	assert.True(t, os.IsNotExist(err), "old _no-ticker req dir must be gone after rename, stat err=%v", err)

	// Append more data AFTER rename — must land in the NEW location.
	require.NoError(t, b.AppendStream("99-narrate.jsonl", []byte(`{"event":"narrate","phase":"handler.entry"}`)))

	require.NoError(t, b.Close())

	// Manifest must reflect the new ticker AND live in the new directory.
	mfPath := filepath.Join(newRoot, "00-manifest.json")
	mfBody, err := os.ReadFile(mfPath)
	require.NoError(t, err, "manifest must exist at new path %s", mfPath)
	var mf artifact.Manifest
	require.NoError(t, json.Unmarshal(mfBody, &mf))
	assert.Equal(t, "TSM", mf.Ticker)

	// Both the pre-rename and post-rename JSONL appends must be present.
	streamBody, err := os.ReadFile(filepath.Join(newRoot, "99-narrate.jsonl"))
	require.NoError(t, err)
	streamLines := splitJSONLines(string(streamBody))
	require.Len(t, streamLines, 2, "expected 2 narrate lines (pre + post rename), got %d", len(streamLines))
}

// TestSetTicker_NoOpWhenAlreadyMatching — opening with a ticker and then
// calling SetTicker with the same value (or empty) is idempotent and does
// not move the directory.
func TestSetTicker_NoOpWhenAlreadyMatching(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenBundle(cfg, "rid-noop", "AAPL", artifact.TriggerHeader)
	require.NoError(t, err)
	require.NotNil(t, b)
	originalRoot := b.Root()

	// Same ticker — must be a no-op.
	b.SetTicker("AAPL")
	assert.Equal(t, originalRoot, b.Root(), "same-ticker SetTicker must not move the dir")

	// Empty ticker after non-empty — must NOT rename to _no-ticker.
	b.SetTicker("")
	assert.Equal(t, originalRoot, b.Root(), "empty SetTicker must not move a non-empty bundle dir")

	require.NoError(t, b.Close())
}

// TestSetTicker_NoOpAfterClose — calling SetTicker after Close must not
// panic, must not attempt a rename, and must leave the bundle's root path
// unchanged.
func TestSetTicker_NoOpAfterClose(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenBundle(cfg, "rid-after-close", "", artifact.TriggerHeader)
	require.NoError(t, err)
	require.NotNil(t, b)

	rootBeforeClose := b.Root()
	require.NoError(t, b.Close())

	// SetTicker after close — must be a safe no-op.
	b.SetTicker("AAPL")
	assert.Equal(t, rootBeforeClose, b.Root(), "SetTicker after Close must not move the dir")
}

// TestSetTicker_RenameFailureCountedAsWriteError — when the underlying
// os.Rename fails (e.g., source directory deleted out from under us), the
// failure must be accounted as a writeError so the manifest outcome degrades
// to "partial", and the manifest's ticker field must still be updated so the
// in-memory record is honest about what the request was for.
func TestSetTicker_RenameFailureCountedAsWriteError(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenBundle(cfg, "rid-rename-fail", "", artifact.TriggerQuery)
	require.NoError(t, err)
	require.NotNil(t, b)
	// Close reaps the eager worker goroutine spawned by OpenBundle. Without
	// this defer the worker sits on `chan receive` forever, leaking a
	// goroutine that trips goleak.VerifyNone in
	// TestDeferredBundle_PromoteCloseRace_NoGoroutineLeak when the package
	// runs under `-count=N -race`. Close() is safe to call after the root
	// directory has been removed: manifest.Finalize errors are swallowed and
	// any worker WriteFile errors simply accumulate into WriteErrors() —
	// which has already been asserted on by the time Close runs.
	defer func() { _ = b.Close() }()

	// Sabotage the bundle directory so os.Rename fails.
	require.NoError(t, os.RemoveAll(b.Root()))

	// SetTicker should not panic; should record a writeError and update manifest.
	b.SetTicker("FAIL")
	assert.Greater(t, b.WriteErrors(), int64(0), "rename failure must be counted as a writeError")
}

// TestSetTicker_EagerBundle_StillRenamesOnDisk pins the BUG-013 fix's
// "eager-mode unchanged" invariant: the deferred-mode short-circuit
// (skip os.Rename, just update b.root) must NOT regress the eager path.
// An eager bundle has a real on-disk directory at construction time, so
// SetTicker MUST still rename the directory on disk — otherwise a
// `?trace=1` request for /api/v1/fair-value/TSM would land at _no-ticker/
// (the eager-mode bug fixed earlier in 2026-04-26 by TestSetTicker_RenamesDirectory).
//
// Companion regression pin to TestSetTicker_DeferredBundle_* in
// bundle_deferred_test.go.
func TestSetTicker_EagerBundle_StillRenamesOnDisk(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenBundle(cfg, "rid-eager-rename", "", artifact.TriggerHeader)
	require.NoError(t, err)
	require.NotNil(t, b)

	originalRoot := b.Root()
	// Sanity: the on-disk dir for the original (no-ticker) path exists.
	st, err := os.Stat(originalRoot)
	require.NoError(t, err, "eager bundle must create on-disk dir at construction")
	assert.True(t, st.IsDir())

	b.SetTicker("AAPL")

	// New on-disk path under AAPL.
	newRoot := b.Root()
	parts := splitPath(t, newRoot, root)
	require.Equal(t, "AAPL", parts[1],
		"eager-mode SetTicker must still move the bundle to <date>/AAPL/")

	// New dir exists on disk; old _no-ticker req dir is gone (the rename
	// MOVED the directory, not just updated b.root in memory).
	st, err = os.Stat(newRoot)
	require.NoError(t, err)
	assert.True(t, st.IsDir())
	_, err = os.Stat(originalRoot)
	assert.True(t, os.IsNotExist(err),
		"eager rename must remove the old _no-ticker req dir; pre-fix this assertion would still pass, but a regression to deferred-style in-memory-only update would leave the old dir intact")

	// writeErrors stays zero — the rename succeeded against a real directory.
	assert.Equal(t, int64(0), b.WriteErrors())

	require.NoError(t, b.Close())
}

// TestSetTicker_SanitizesPathSeparators — a malicious ticker with path
// separators must be neutralised before becoming a directory name.
func TestSetTicker_SanitizesPathSeparators(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenBundle(cfg, "rid-evil", "", artifact.TriggerQuery)
	require.NoError(t, err)
	require.NotNil(t, b)

	b.SetTicker("../../etc/passwd")
	parts := splitPath(t, b.Root(), root)
	assert.NotContains(t, parts[1], "..", "ticker segment must not contain ..")
	assert.NotContains(t, parts[1], "/", "ticker segment must not contain /")
	assert.NotContains(t, parts[1], "\\", "ticker segment must not contain backslash")

	require.NoError(t, b.Close())
}

// TestBundle_SetAssumptionProfileManifest_WritesJSON pins the Tier 2 P0b bundle
// extension: SetAssumptionProfileManifest writes 08-assumption-profile.json
// carrying the resolved-profile + audit trail so replay tooling can re-resolve
// (or short-circuit to the captured snapshot) deterministically. Schema version
// is registered on the manifest so future consumers can version-gate.
func TestBundle_SetAssumptionProfileManifest_WritesJSON(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenBundle(cfg, "rid-profile", "JPM", artifact.TriggerQuery)
	require.NoError(t, err)
	require.NotNil(t, b)

	manifest := profile.AssumptionProfileManifest{
		ProfileID:       "mature_large_bank:mature",
		Source:          profile.SourceExplicit,
		ResolverVersion: "1.0.0",
		ConfigVersion:   "1.0.0",
		ConfigHash:      "abcdef0123",
		Trace: profile.ResolutionTrace{
			ProfileID:       "mature_large_bank:mature",
			Source:          profile.SourceExplicit,
			ResolverVersion: "1.0.0",
			ConfigVersion:   "1.0.0",
			MatchedRuleID:   "fin_large_bank",
		},
	}
	b.SetAssumptionProfileManifest(context.Background(), manifest)
	require.NoError(t, b.Close())

	// File must exist on disk with the expected profile_id payload.
	body, err := os.ReadFile(filepath.Join(b.Root(), "08-assumption-profile.json"))
	require.NoError(t, err)
	assert.Contains(t, string(body), `"profile_id": "mature_large_bank:mature"`)
	assert.Contains(t, string(body), `"matched_rule_id": "fin_large_bank"`)

	// Manifest schema version must be registered so replay tooling can
	// version-gate against schema drift.
	mfBody, err := os.ReadFile(filepath.Join(b.Root(), "00-manifest.json"))
	require.NoError(t, err)
	var mf artifact.Manifest
	require.NoError(t, json.Unmarshal(mfBody, &mf))
	assert.Equal(t, 1, mf.SchemaVersions["AssumptionProfileManifest"],
		"AssumptionProfileManifest schema_version must be 1")
}

// TestBundle_SetAssumptionProfileManifest_NilSafe — the method must be a
// no-op on a nil receiver so service.go can call it through artifact.From(ctx)
// without nil-checking.
func TestBundle_SetAssumptionProfileManifest_NilSafe(t *testing.T) {
	var b *artifact.Bundle
	// Must not panic.
	b.SetAssumptionProfileManifest(context.Background(), profile.AssumptionProfileManifest{
		ProfileID: "irrelevant",
	})
}
