package replay

import (
	"os"
	"path/filepath"
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
// hosts without symlink-creation privileges (Windows non-admin etc.).
func TestWalkBundles_SymlinkDoesNotCycle(t *testing.T) {
	root := t.TempDir()
	makeBundle(t, filepath.Join(root, "AAPL", "req_01"))
	// Symlink that points back to root — would be a cycle if followed.
	trySymlink(t, root, filepath.Join(root, "loop"))

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

// trySymlink wraps os.Symlink with t.Skip on EPERM/ENOTSUP. Windows
// non-admin users cannot create symlinks (the OS returns
// ERROR_PRIVILEGE_NOT_HELD); macOS sandboxes and some containerized CI
// environments may also reject the call. Skipping at the call site means
// the symlink-coverage tests run on Linux dev boxes and CI hosts that
// have the capability, while Windows non-admin users see a Skip rather
// than a hard fail.
func trySymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation not permitted on this host (%v); covered on Linux/macOS with sufficient privileges", err)
	}
}

// TestWalkBundles_FollowsSymlinkOnce pins R1 follow-up #6 + spec §5 D9
// "Symlinks are followed once (no cycles)." If a user symlinks
// ~/bundles → /storage/bundles and runs `replay ~/bundles/<sub>`, the
// walker MUST descend through the symlink and find bundles inside it.
// The previous behavior (skip all symlinks) silently missed them.
//
// Skipped on hosts where symlink creation is rejected (Windows non-admin,
// some sandboxed environments).
func TestWalkBundles_FollowsSymlinkOnce(t *testing.T) {
	root := t.TempDir()
	// Set up dir-a/ with a symlink "link-to-b" pointing at dir-b/, which
	// contains a real bundle. The walker must follow the link and find
	// the bundle.
	dirA := filepath.Join(root, "dir-a")
	dirB := filepath.Join(root, "dir-b")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	makeBundle(t, dirB)
	trySymlink(t, dirB, filepath.Join(dirA, "link-to-b"))

	got, err := WalkBundles(dirA)
	if err != nil {
		t.Fatalf("WalkBundles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 bundle (via symlink); got %d (%v)", len(got), got)
	}
}

// TestWalkBundles_SelfSymlinkDoesNotCycle protects against the
// pathological case where a directory contains a symlink to itself.
// Spec §5 D9's "follow once, no cycles" requires termination.
func TestWalkBundles_SelfSymlinkDoesNotCycle(t *testing.T) {
	root := t.TempDir()
	dirA := filepath.Join(root, "dir-a")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// A self-loop symlink — following it would re-walk dir-a infinitely.
	trySymlink(t, dirA, filepath.Join(dirA, "loop"))
	makeBundle(t, filepath.Join(dirA, "real-bundle"))

	got, err := WalkBundles(dirA)
	if err != nil {
		t.Fatalf("WalkBundles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 bundle (loop must not revisit); got %d (%v)", len(got), got)
	}
}

// TestWalkBundles_BrokenSymlinkSkipped covers the broken-symlink branch
// of walkOnce: a symlink whose target doesn't exist is silently skipped
// rather than aborting the batch. Surfacing it would noise the summary
// for a class of "user removed a target" issues that aren't actionable
// at the bundle layer.
func TestWalkBundles_BrokenSymlinkSkipped(t *testing.T) {
	root := t.TempDir()
	makeBundle(t, filepath.Join(root, "real-bundle"))
	// Symlink to a non-existent target.
	trySymlink(t, filepath.Join(root, "does-not-exist"), filepath.Join(root, "broken"))

	got, err := WalkBundles(root)
	if err != nil {
		t.Fatalf("WalkBundles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 bundle (broken link skipped); got %d (%v)", len(got), got)
	}
}

// TestWalkBundles_SymlinkToFileIgnored covers the non-directory-target
// branch of walkOnce: a symlink to a regular file is not a candidate
// bundle root and must not be followed.
func TestWalkBundles_SymlinkToFileIgnored(t *testing.T) {
	root := t.TempDir()
	makeBundle(t, filepath.Join(root, "real-bundle"))
	// Stray file at root + a symlink pointing to it.
	if err := os.WriteFile(filepath.Join(root, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	trySymlink(t, filepath.Join(root, "stray.txt"), filepath.Join(root, "link-to-file"))

	got, err := WalkBundles(root)
	if err != nil {
		t.Fatalf("WalkBundles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 bundle (file-target symlink ignored); got %d (%v)", len(got), got)
	}
}

// TestWalkBundles_SymlinkDirectlyToBundle covers the
// "symlink IS a bundle" terse-syntax branch. A user can symlink
// ~/AAPL-bundle → /storage/...../AAPL/req_X and run
// `replay ~/AAPL-bundle`; walkOnce recognises the symlink target as a
// bundle and reports the LINK path (not the resolved target) as the
// bundle root.
func TestWalkBundles_SymlinkDirectlyToBundle(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "real-bundle")
	makeBundle(t, bundle)
	linkPath := filepath.Join(root, "link-to-bundle")
	trySymlink(t, bundle, linkPath)

	got, err := WalkBundles(root)
	if err != nil {
		t.Fatalf("WalkBundles: %v", err)
	}
	// Both the real bundle AND the linked bundle should be discovered.
	// Cycle-protection prevents the latter from re-descending into the
	// former. The order is sorted ascending.
	if len(got) != 2 {
		t.Fatalf("expected 2 bundles (real + symlink); got %d (%v)", len(got), got)
	}
}
