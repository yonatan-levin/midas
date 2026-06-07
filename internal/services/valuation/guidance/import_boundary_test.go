package guidance_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestImportBoundary_GuidancePackage_DoesNotImportModelsOrEntities pins the
// leaf-package boundary (spec §"Critical abstractions"): guidance is a pure
// artifact-domain package and MUST NOT import the valuation models or the core
// entities. Either import would couple the contract package to DCF math /
// domain models and break the NF3 hermetic-replay separation (the loader must
// touch only fixture bytes, never the engine).
//
// Mirrors profile/import_boundary_test.go. Production package files only —
// test files may legitimately reach for testify or stdlib.
func TestImportBoundary_GuidancePackage_DoesNotImportModelsOrEntities(t *testing.T) {
	forbidden := []string{
		"github.com/midas/dcf-valuation-api/internal/services/valuation/models",
		"github.com/midas/dcf-valuation-api/internal/core/entities",
		"github.com/midas/dcf-valuation-api/internal/services/valuation/authority",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		if strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", e.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbidden {
				assert.NotEqual(t, bad, path,
					"FORBIDDEN IMPORT in %s: guidance package must not import %s", e.Name(), bad)
			}
		}
	}
}
