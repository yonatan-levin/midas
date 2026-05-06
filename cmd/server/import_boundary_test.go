package main_test

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestImportBoundary_CmdServer_DoesNotDependOnReplayPackage enforces the
// architectural invariant that cmd/server (production HTTP binary) MUST
// NOT transitively import the replay package.
//
// Background: the replay package contains init()-time reflection guards
// that panic on field-walker drift (RPL-2h, planned in R3 Stage O.6).
// Those panics are scoped to the replay binary by convention today;
// nothing enforces the boundary. A future refactor (e.g., extracting a
// helper "shared library" between server and replay) could silently
// reintroduce the dependency, at which point the init() panic scope
// collapses and replay-package field-walker drift could brick
// production startup.
//
// Mechanism: shell out to `go list -deps ./cmd/server` and assert no
// entry contains "/internal/observability/replay" or "/cmd/replay".
//
// Decision O.13.a (v2 plan §3): use `go list` via os/exec rather than
// golang.org/x/tools/go/packages — adding the latter would violate
// spec NF1 (no new external Go modules).
//
// Manual injection check: temporarily add
//
//	import _ "github.com/midas/dcf-valuation-api/internal/observability/replay"
//
// to cmd/server/main.go; this test FAILS with the descriptive error
// message; revert to confirm it passes again.
func TestImportBoundary_CmdServer_DoesNotDependOnReplayPackage(t *testing.T) {
	// `go list -deps` prints one import path per line, ending with the
	// requested target. We don't care about ordering or completeness;
	// we only need to assert that no replay-package path appears.
	// Test runs with cwd at cmd/server/. `go list -deps ./cmd/server`
	// must run from the repo root for the module-relative target to
	// resolve, so anchor explicitly via `cmd.Dir`.
	cmd := exec.Command("go", "list", "-deps", "./cmd/server")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = filepath.Join("..", "..")
	if err := cmd.Run(); err != nil {
		// `go list` failure is a test infrastructure error, not a
		// boundary violation. Skip rather than fail so a missing/
		// misconfigured Go toolchain doesn't fail CI on a wholly
		// unrelated check.
		t.Skipf("go list -deps failed (infrastructure): %v\nstderr=%s", err, stderr.String())
	}

	forbidden := []string{
		"github.com/midas/dcf-valuation-api/internal/observability/replay",
		"github.com/midas/dcf-valuation-api/cmd/replay",
	}
	deps := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	for _, dep := range deps {
		for _, bad := range forbidden {
			if dep == bad {
				t.Errorf("cmd/server transitively imports %q. The replay package contains init()-time reflection guards that will panic-on-startup in cmd/server if a field-walker drift is introduced. Either revert the new import, or extract the shared symbol into a third package neither replay nor cmd/server depends on.", dep)
			}
		}
	}
}
