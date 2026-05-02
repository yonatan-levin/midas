package replay

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

// makeBundle creates a synthetic bundle at dir by writing a minimal
// 00-manifest.json there. Returns nothing — the caller verifies via
// WalkBundles. Mirrors the helpers in manifest_test.go but omits the JSON
// validity (walk.go doesn't read content).
func makeBundle(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	body := []byte(`{"bundle_version":"1.0","request_id":"x","ticker":"X","started_at":"2026-04-25T00:00:00Z"}`)
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), body, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func TestWalkBundles_EmptyDirectory(t *testing.T) {
	root := t.TempDir()
	got, err := WalkBundles(root)
	if err != nil {
		t.Fatalf("WalkBundles: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 bundles in empty dir; got %v", got)
	}
}

func TestWalkBundles_SingleBundleAsRoot(t *testing.T) {
	root := t.TempDir()
	makeBundle(t, root)

	got, err := WalkBundles(root)
	if err != nil {
		t.Fatalf("WalkBundles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 bundle; got %d (%v)", len(got), got)
	}
	abs, _ := filepath.Abs(root)
	if got[0] != abs {
		t.Errorf("bundle path = %q, want %q", got[0], abs)
	}
}

func TestWalkBundles_NestedBundles(t *testing.T) {
	root := t.TempDir()
	// Tree mirrors production: <root>/<date>/<TICKER>/req_<id>/
	makeBundle(t, filepath.Join(root, "2026-04-25", "AAPL", "req_01"))
	makeBundle(t, filepath.Join(root, "2026-04-25", "MSFT", "req_02"))
	makeBundle(t, filepath.Join(root, "2026-04-26", "AMD", "req_03"))

	got, err := WalkBundles(root)
	if err != nil {
		t.Fatalf("WalkBundles: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 bundles; got %d (%v)", len(got), got)
	}
	// Verify deterministic sort.
	if !sort.StringsAreSorted(got) {
		t.Errorf("bundles not sorted: %v", got)
	}
}

func TestWalkBundles_IgnoresNonBundleDirs(t *testing.T) {
	root := t.TempDir()
	makeBundle(t, filepath.Join(root, "real-bundle"))
	// Empty subdirectory — should be ignored.
	if err := os.MkdirAll(filepath.Join(root, "noise"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// File at root (not a bundle).
	if err := os.WriteFile(filepath.Join(root, "stray.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	got, err := WalkBundles(root)
	if err != nil {
		t.Fatalf("WalkBundles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 bundle; got %d (%v)", len(got), got)
	}
}

func TestWalkBundles_DoesNotDescendIntoBundle(t *testing.T) {
	// Verify that a nested directory inside a bundle is NOT itself walked
	// for sub-bundles. Bundles are leaf directories by construction; if
	// someone accidentally placed a manifest under <bundle>/inner/ the
	// walker should NOT pick it up because we SkipDir on bundle entry.
	root := t.TempDir()
	bundle := filepath.Join(root, "outer")
	makeBundle(t, bundle)
	// Pathological inner: should be ignored.
	inner := filepath.Join(bundle, "inner")
	makeBundle(t, inner)

	got, err := WalkBundles(root)
	if err != nil {
		t.Fatalf("WalkBundles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 bundle (not the inner one); got %d (%v)", len(got), got)
	}
	abs, _ := filepath.Abs(bundle)
	if got[0] != abs {
		t.Errorf("got %q, want %q", got[0], abs)
	}
}

func TestWalkBundles_NonexistentRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := WalkBundles(root)
	if err == nil {
		t.Fatal("WalkBundles should fail when root does not exist")
	}
}

func TestWalkBundles_NotADirectory(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := WalkBundles(file)
	if err == nil {
		t.Fatal("WalkBundles should fail when root is a file")
	}
}

// TestWalkBundles_SymlinkDoesNotCycle creates a symlink loop and verifies
// the walker terminates without revisiting the same bundle. Skipped on
// Windows where symlink creation requires elevated privileges and the test
// would be flaky in CI.
func TestWalkBundles_SymlinkDoesNotCycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires admin on Windows; covered on Linux/macOS")
	}
	root := t.TempDir()
	makeBundle(t, filepath.Join(root, "AAPL", "req_01"))
	// Symlink that points back to root — would be a cycle if followed.
	loop := filepath.Join(root, "loop")
	if err := os.Symlink(root, loop); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got, err := WalkBundles(root)
	if err != nil {
		t.Fatalf("WalkBundles: %v", err)
	}
	// We should see exactly the one real bundle, not duplicates from
	// chasing the symlink.
	if len(got) != 1 {
		t.Fatalf("expected 1 bundle; got %d (%v)", len(got), got)
	}
}
