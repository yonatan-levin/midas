package authority_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestImportBoundary_AuthorityPackage_DoesNotImportModelsOrEntities pins the
// resolver's leaf boundary (spec §"Critical abstractions"): authority decides
// §9 precedence + §9.3 guardrails over guidance + profile inputs ONLY. It must
// not import the valuation models or core entities — that would couple the
// precedence engine to DCF math / domain models and risk an import cycle
// (models → authority → models). It legitimately imports guidance + profile.
func TestImportBoundary_AuthorityPackage_DoesNotImportModelsOrEntities(t *testing.T) {
	forbidden := []string{
		"github.com/midas/dcf-valuation-api/internal/services/valuation/models",
		"github.com/midas/dcf-valuation-api/internal/core/entities",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
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
					"FORBIDDEN IMPORT in %s: authority package must not import %s", e.Name(), bad)
			}
		}
	}
}
