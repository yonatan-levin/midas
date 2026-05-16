package profile_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestImportBoundary_ProfilePackage_DoesNotImportModelsOrEntities is the
// load-bearing import-boundary guard. The profile package MUST NOT import
// internal/services/valuation/models or internal/core/entities — either
// import would create the Go import cycle:
//
//	models → profile → models    (FORBIDDEN)
//	models → profile → entities → models   (also forbidden — entities is
//	    consumed by models; profile importing entities means any future
//	    entities → models edge breaks profile too)
//
// Spec §2.2, spec §11 item 7. Translation from entities.FinancialData to
// the neutral Facts DTO lives at the consumer site (service.go in P0b).
func TestImportBoundary_ProfilePackage_DoesNotImportModelsOrEntities(t *testing.T) {
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
		// over-tight (a resolver test could legitimately use a synthetic
		// ModelInput from testhelpers). The boundary applies to PRODUCTION
		// package files only.
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
					"FORBIDDEN IMPORT in %s: profile package must not import %s",
					e.Name(), bad)
			}
		}
	}
}
