package params_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestImportBoundary_ParamsPackage_DoesNotImportModelsOrEntities is the
// load-bearing import-boundary guard. The params package MUST NOT import
// internal/services/valuation/models or internal/core/entities — either
// import would create a forbidden dependency:
//
//	models → params → models    (FORBIDDEN: cycle)
//	params → entities           (FORBIDDEN: entities is consumed by models;
//	    a params→entities edge means any future entities→models edge breaks params
//	    and pollutes the resolver's pure-scalar design)
//
// Plan §3.1 (NF5) and plan §3.8. Mirrors the mechanism in
// internal/services/valuation/profile/import_boundary_test.go exactly.
// Production (non-test) .go files only — test files may legitimately import
// richer types for test helpers.
func TestImportBoundary_ParamsPackage_DoesNotImportModelsOrEntities(t *testing.T) {
	forbidden := []string{
		"github.com/midas/dcf-valuation-api/internal/services/valuation/models",
		"github.com/midas/dcf-valuation-api/internal/core/entities",
	}

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
		// Skip test files: forbidding entities/models in tests would be
		// over-tight (a resolver test could legitimately use synthetic types
		// from testhelpers). The boundary applies to PRODUCTION package files only.
		if strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(pkgDir, e.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbidden {
				assert.NotEqual(t, bad, path,
					"FORBIDDEN IMPORT in %s: params package must not import %s",
					e.Name(), bad)
			}
		}
	}
}
