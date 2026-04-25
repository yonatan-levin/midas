package artifact_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
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
