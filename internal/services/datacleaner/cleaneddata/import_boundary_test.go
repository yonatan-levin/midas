package cleaneddata_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCleanedFinancialData_ImportBoundary is the load-bearing import-boundary
// guard. The cleaneddata package depends on entities for LedgerEntry /
// OverlaySpec / FinancialData field reads — and on standard library
// (time) — and on NOTHING ELSE from inside internal/. Phase 4 consumers
// will import this package; widening its dependency cone would risk
// circular imports as those migrations land.
//
// Allowlist:
//   - stdlib (anything not starting with "github.com/midas/")
//   - github.com/midas/dcf-valuation-api/internal/core/entities
//
// Spec §4.1 (Phase 3): "cleaneddata imports `internal/core/entities` and
// nothing else from inside `internal/services/`".
func TestCleanedFinancialData_ImportBoundary(t *testing.T) {
	const (
		modulePrefix    = "github.com/midas/dcf-valuation-api/"
		allowedInternal = "github.com/midas/dcf-valuation-api/internal/core/entities"
	)

	pkgDir := "."
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		// Skip test files; the boundary applies to PRODUCTION package files.
		// Tests may legitimately import testify or other test-only deps.
		if strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(pkgDir, e.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			// stdlib / external dependencies are unrestricted.
			if !strings.HasPrefix(path, modulePrefix) {
				continue
			}
			assert.Equal(t, allowedInternal, path,
				"FORBIDDEN IMPORT in %s: cleaneddata package may only import %s from internal/, got %s",
				e.Name(), allowedInternal, path)
		}
	}
}
