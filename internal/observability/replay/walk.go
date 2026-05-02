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
//   - Symlinks are NOT followed when descending: hermetic walks must not
//     loop on cyclic symlinks (§5 D9). A symlink directly pointing at a
//     bundle directory IS recognized at the top level (so users can
//     `ln -s ./real artifacts-symlink && replay artifacts-symlink`), but
//     symlinks inside the tree are skipped.
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

	var bundles []string
	walkErr := filepath.WalkDir(abs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// A read error mid-walk should not abort the whole batch — log
			// the path and continue. The user-facing diagnostic comes from
			// the orchestration layer (which surfaces "errored" Result).
			// For R1 we surface fs errors but only at the orchestration
			// edge; here we just skip the bad subtree.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip symlinks inside the tree to prevent cycles. Use Type()
		// instead of os.Lstat() to keep allocations bounded.
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if isBundle(path) {
			bundles = append(bundles, path)
			// Don't descend into a bundle — bundles are leaf directories
			// in the artifact tree by construction. This avoids treating
			// a bundle's testdata-style nested directories as bundles.
			return filepath.SkipDir
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("replay: walk %s: %w", rootDir, walkErr)
	}

	// Deterministic ordering for reproducibility. Empty slice when no
	// bundles found is a legitimate result, NOT an error.
	sort.Strings(bundles)
	return bundles, nil
}

// isBundle returns true when dir contains 00-manifest.json. Cheap check used
// by WalkBundles; doesn't validate the manifest content (that's
// ReadManifest's job and would be wasteful to do for every directory).
func isBundle(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ManifestFileName))
	return err == nil
}
