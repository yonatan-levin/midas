package artifact_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
)

// TestReaper_DisabledNoOps verifies the master switch.
func TestReaper_DisabledNoOps(t *testing.T) {
	r := artifact.NewReaper(artifact.Config{Enabled: false})
	r.Start(context.Background())
	r.Stop()
	assert.NoError(t, r.Sweep(), "Sweep on disabled reaper must be no-op")
}

// TestReaper_SweepByAge — old date dirs evicted; recent ones kept.
func TestReaper_SweepByAge(t *testing.T) {
	root := t.TempDir()

	// Lay out two date directories: one expired, one fresh.
	expired := time.Now().UTC().AddDate(0, 0, -10).Format("2006-01-02")
	fresh := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")

	for _, d := range []string{expired, fresh} {
		path := filepath.Join(root, d, "AAPL", "req_x")
		require.NoError(t, os.MkdirAll(path, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(path, "00-manifest.json"), []byte("{}"), 0o644))
	}

	r := artifact.NewReaper(artifact.Config{
		Enabled:       true,
		RootPath:      root,
		RetentionDays: 7,
	})
	require.NoError(t, r.Sweep())

	// Expired directory must be gone.
	_, err := os.Stat(filepath.Join(root, expired))
	assert.True(t, os.IsNotExist(err), "expired date dir must be removed")

	// Fresh directory must still exist.
	_, err = os.Stat(filepath.Join(root, fresh))
	assert.NoError(t, err, "fresh date dir must survive")
}

// TestReaper_SweepBySize — when total bytes exceed cap, oldest req-dirs evicted.
func TestReaper_SweepBySize(t *testing.T) {
	root := t.TempDir()
	today := time.Now().UTC().Format("2006-01-02")

	// Create three req dirs with 500-byte payloads each (total ~1500 bytes).
	for i, name := range []string{"req_old", "req_mid", "req_new"} {
		path := filepath.Join(root, today, "AAPL", name)
		require.NoError(t, os.MkdirAll(path, 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(path, "01.json"),
			[]byte(strings.Repeat("x", 500)),
			0o644,
		))
		// Force the mtime so the oldest can be identified.
		mtime := time.Now().Add(time.Duration(i) * time.Hour)
		require.NoError(t, os.Chtimes(filepath.Join(path, "01.json"), mtime, mtime))
	}

	// Cap to 700 bytes — must evict the two oldest, keep the newest.
	r := artifact.NewReaper(artifact.Config{
		Enabled:       true,
		RootPath:      root,
		MaxTotalBytes: 700,
	})
	require.NoError(t, r.Sweep())

	// Expect only req_new survives.
	for _, name := range []string{"req_old", "req_mid"} {
		_, err := os.Stat(filepath.Join(root, today, "AAPL", name))
		assert.True(t, os.IsNotExist(err), "%s must be evicted", name)
	}
	_, err := os.Stat(filepath.Join(root, today, "AAPL", "req_new"))
	assert.NoError(t, err, "req_new must survive")
}

// TestReaper_NoOpWhenUnderCap — happy path.
func TestReaper_NoOpWhenUnderCap(t *testing.T) {
	root := t.TempDir()
	today := time.Now().UTC().Format("2006-01-02")
	path := filepath.Join(root, today, "AAPL", "req_a")
	require.NoError(t, os.MkdirAll(path, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(path, "01.json"), []byte("ok"), 0o644))

	r := artifact.NewReaper(artifact.Config{
		Enabled:       true,
		RootPath:      root,
		MaxTotalBytes: 1 << 20, // 1 MiB cap; 2 bytes used
		RetentionDays: 7,
	})
	require.NoError(t, r.Sweep())

	_, err := os.Stat(path)
	assert.NoError(t, err, "bundle within retention + size cap must survive")
}

// TestReaper_ZeroValuesDisableSweeps — RetentionDays=0 and MaxTotalBytes=0
// disable each rule independently.
func TestReaper_ZeroValuesDisableSweeps(t *testing.T) {
	root := t.TempDir()
	old := time.Now().UTC().AddDate(0, -1, 0).Format("2006-01-02")
	path := filepath.Join(root, old, "AAPL", "req_old")
	require.NoError(t, os.MkdirAll(path, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(path, "01.json"), []byte("ok"), 0o644))

	r := artifact.NewReaper(artifact.Config{
		Enabled:       true,
		RootPath:      root,
		RetentionDays: 0,
		MaxTotalBytes: 0,
	})
	require.NoError(t, r.Sweep())

	_, err := os.Stat(path)
	assert.NoError(t, err, "with both rules zero, nothing must be swept")
}

// TestReaper_StartStopGoroutine smoke. Doesn't validate timing — Start kicks
// an immediate sweep, then Stop should return cleanly.
func TestReaper_StartStopGoroutine(t *testing.T) {
	root := t.TempDir()
	r := artifact.NewReaper(artifact.Config{
		Enabled:       true,
		RootPath:      root,
		RetentionDays: 7,
	})
	r.Start(context.Background())
	// Stop should never block.
	done := make(chan struct{})
	go func() { r.Stop(); close(done) }()
	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("Stop blocked >2s")
	}
}

// TestReaper_MissingRootIsNotError — defensive: the directory may not exist
// yet at startup if no traced request has happened.
func TestReaper_MissingRootIsNotError(t *testing.T) {
	r := artifact.NewReaper(artifact.Config{
		Enabled:       true,
		RootPath:      filepath.Join(t.TempDir(), "does", "not", "exist"),
		RetentionDays: 7,
		MaxTotalBytes: 1 << 30,
	})
	require.NoError(t, r.Sweep())
}

// TestReaper_EmptyRootPathErrors guards against silent mis-configuration.
func TestReaper_EmptyRootPathErrors(t *testing.T) {
	r := artifact.NewReaper(artifact.Config{Enabled: true, RootPath: ""})
	err := r.Sweep()
	assert.Error(t, err)
}

// TestReaper_IgnoresUnrelatedFilesAtRoot — only YYYY-MM-DD dirs are touched.
func TestReaper_IgnoresUnrelatedFilesAtRoot(t *testing.T) {
	root := t.TempDir()
	// stray file
	require.NoError(t, os.WriteFile(filepath.Join(root, "stray.txt"), []byte("x"), 0o644))
	// stray non-date dir
	require.NoError(t, os.MkdirAll(filepath.Join(root, "notadate"), 0o755))
	// expired date
	expired := time.Now().UTC().AddDate(0, 0, -10).Format("2006-01-02")
	require.NoError(t, os.MkdirAll(filepath.Join(root, expired), 0o755))

	r := artifact.NewReaper(artifact.Config{
		Enabled:       true,
		RootPath:      root,
		RetentionDays: 7,
	})
	require.NoError(t, r.Sweep())

	// stray file/dir untouched
	_, err := os.Stat(filepath.Join(root, "stray.txt"))
	assert.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, "notadate"))
	assert.NoError(t, err)
	// expired date removed
	_, err = os.Stat(filepath.Join(root, expired))
	assert.True(t, os.IsNotExist(err))
}

// nolint:unused — keep helper for potential future tests
func touchFile(t *testing.T, p string) { //nolint:unused
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(fmt.Sprintf("ok-%d", time.Now().UnixNano())), 0o644))
}
