package replay

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// WalkBundles walks rootDir recursively and returns the absolute paths of
// every bundle directory (any directory containing 00-manifest.json) found
// underneath. Implements §5 D9: a single bundle is a degenerate one-element
// batch.
//
// Behavior:
//   - rootDir does not exist or is not a directory → returns an error.
//   - rootDir IS a bundle (contains 00-manifest.json) → returns
//     []string{rootDir}.
//   - rootDir contains bundles → returns each bundle's absolute path,
//     sorted ascending for deterministic stdout. Sorting is essential for
//     --workers=1 reproducibility (NF3 / spec §7).
//   - rootDir contains no bundles → returns an empty slice (NOT an error).
//     The CLI treats "0/0 passed" as exit 0 (spec §9 R1 acceptance).
//   - Symlinks are followed once (spec §5 D9): a directory symlink is
//     descended into the first time it is seen, but cycle protection
//     prevents infinite loops on self-loops or back-references. Cycle
//     detection uses os.SameFile against a set of already-visited
//     FileInfos so it is portable across Linux/macOS/Windows without
//     reaching into syscall-specific Inode fields.
//
// The signature is intentionally narrow — no filter or limit args. Filters
// (--filter-ticker / --filter-since) live at the orchestration layer in
// R3, applied after the walk. Keeping this primitive policy-free makes
// testing simpler.
func WalkBundles(rootDir string) ([]string, error) {
	info, err := os.Stat(rootDir)
	if err != nil {
		return nil, fmt.Errorf("replay: stat %s: %w", rootDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("replay: %s is not a directory", rootDir)
	}

	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("replay: absolute path %s: %w", rootDir, err)
	}

	// Top-level shortcut: rootDir itself is a bundle.
	if isBundle(abs) {
		return []string{abs}, nil
	}

	bundles, err := walkOnce(abs, info, []os.FileInfo{info})
	if err != nil {
		return nil, fmt.Errorf("replay: walk %s: %w", rootDir, err)
	}

	// Deterministic ordering for reproducibility. Empty slice when no
	// bundles found is a legitimate result, NOT an error.
	sort.Strings(bundles)
	return bundles, nil
}

// walkOnce recursively walks the directory tree rooted at dir, following
// symlinked subdirectories at most once each. visited tracks every
// directory FileInfo that the walker has already entered; when a symlink
// resolves to a directory whose FileInfo matches any visited entry (per
// os.SameFile), it is skipped to break cycles.
//
// We hand-roll the recursion instead of using filepath.WalkDir because
// WalkDir does not follow symlinks (and its DirEntry interface deliberately
// hides Lstat-vs-Stat resolution). os.SameFile gives us portable
// inode-equivalence semantics (sys-Inode on POSIX, NT-handle ID on Windows)
// without forcing per-OS code paths into this package.
func walkOnce(dir string, dirInfo os.FileInfo, visited []os.FileInfo) ([]string, error) {
	var bundles []string

	entries, err := os.ReadDir(dir)
	if err != nil {
		// Skip unreadable subtrees rather than aborting the whole batch.
		// The orchestration layer surfaces per-bundle errors; a transient
		// permission issue under the root should not erase the whole run.
		return nil, nil
	}

	for _, e := range entries {
		path := filepath.Join(dir, e.Name())

		// e.Type() reflects the LSTAT result — we need that to detect
		// symlinks. For symlinks we then Stat the target to learn whether
		// it resolves to a directory and to get a FileInfo for cycle
		// detection.
		isSymlink := e.Type()&fs.ModeSymlink != 0

		if isSymlink {
			target, statErr := os.Stat(path)
			if statErr != nil {
				// Broken symlink — skip silently. Surfacing it would noise
				// the batch summary; consumers care about bundles, not
				// dangling links.
				continue
			}
			if !target.IsDir() {
				continue
			}
			// Cycle protection: if we've already entered a directory with
			// this identity (by os.SameFile), skip it. Spec §5 D9
			// "follow once, no cycles".
			cycle := false
			for _, v := range visited {
				if os.SameFile(v, target) {
					cycle = true
					break
				}
			}
			if cycle {
				continue
			}
			// Follow the symlink: descend into the target with an
			// expanded visited set. We do NOT register the target as a
			// candidate bundle root (that's the responsibility of the
			// recursive call below if target/00-manifest.json exists).
			if isBundle(path) {
				bundles = append(bundles, path)
				continue
			}
			sub, subErr := walkOnce(path, target, append(visited, target))
			if subErr != nil {
				return nil, subErr
			}
			bundles = append(bundles, sub...)
			continue
		}

		if !e.IsDir() {
			continue
		}

		// Plain directory: stat it for cycle tracking, then recurse.
		// (Hard-link cycles between directories are rare on POSIX and
		// impossible on most filesystems, but tracking them costs only an
		// already-paid Stat.)
		info, statErr := os.Stat(path)
		if statErr != nil {
			continue
		}
		if isBundle(path) {
			bundles = append(bundles, path)
			// Don't descend into a bundle — bundles are leaf directories
			// in the artifact tree by construction. This avoids treating
			// a bundle's testdata-style nested directories as bundles.
			continue
		}
		// Cycle check on hard-linked dirs (defensive; rare in practice).
		cycle := false
		for _, v := range visited {
			if os.SameFile(v, info) {
				cycle = true
				break
			}
		}
		if cycle {
			continue
		}
		sub, subErr := walkOnce(path, info, append(visited, info))
		if subErr != nil {
			return nil, subErr
		}
		bundles = append(bundles, sub...)
	}

	_ = dirInfo // dirInfo is the FileInfo for `dir`; reserved for future
	// extensions (e.g. emitting structured walk diagnostics). Kept in
	// the signature so callers can pass the already-stat'd root without
	// re-statting.
	return bundles, nil
}

// isBundle returns true when dir contains 00-manifest.json. Cheap check used
// by WalkBundles; doesn't validate the manifest content (that's
// ReadManifest's job and would be wasteful to do for every directory).
func isBundle(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ManifestFileName))
	return err == nil
}
