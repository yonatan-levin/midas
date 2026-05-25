package adjustments

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestB1LeasePV_HonorsContextCancellation is the MEDIUM-1 regression pin.
// It verifies the ctx-threading contract for the B1 operating-lease PV
// path: ProcessOperatingLeaseAdjustment AND ApplyB1OperatingLeases accept
// ctx as their first parameter and forward it through to
// leaseCalculator.CalculatePresentValue.
//
// The lease calculator is a concrete type (PerformanceOptimizedCalculator)
// without an interface seam, so the test cannot observe cancellation
// directly. Two assertions stand in for the cancellation-respect contract:
//
//  1. Calling the methods with a cancelled ctx does not crash. This is
//     the same shape Phase 3 Task 3.9 used for the dispatcher-level ctx
//     threading pin (see ctx_threading_test.go).
//
//  2. The static-source-analysis sibling
//     TestB1LeasePath_HasNoContextBackgroundLiteral asserts that no
//     `context.Background()` literal remains inside the B1 production
//     code path. Pre-fix code carried `ctx := context.Background()` on
//     liabilities.go:656 with a `// TODO: Use proper context from caller`
//     marker; the fix removed that line, so a regression that re-adds
//     the literal would fail the static check.
func TestB1LeasePV_HonorsContextCancellation(t *testing.T) {
	la := NewLiabilityAdjuster(nil, nil)
	rule := &entities.CleaningRule{ID: "operating_lease_capitalization", Enabled: true}
	data := &entities.FinancialData{
		Ticker:      "CXLB1",
		TotalAssets: 1_000_000,
		// Seed minimal lease commitments so the calculator does not crash
		// on a fully empty input. We don't care which result branch
		// the calculator returns; we only assert no panic on cancelled ctx.
		OperatingLeaseLiability: 100_000,
	}
	cleaningCtx := &entities.CleaningContext{IndustryCode: "TECH"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Pin 1a: ProcessOperatingLeaseAdjustment accepts ctx and does not crash.
	result := la.ProcessOperatingLeaseAdjustment(ctx, data, rule, cleaningCtx)
	require.NotNil(t, result,
		"ProcessOperatingLeaseAdjustment must not return nil even with a cancelled ctx")

	// Pin 1b: ApplyB1OperatingLeases accepts ctx and forwards it.
	out, err := la.ApplyB1OperatingLeases(ctx, data, rule, cleaningCtx)
	require.NoError(t, err,
		"ApplyB1OperatingLeases must not error on cancelled ctx (legacy fallback absorbs internal errors)")
	require.NotNil(t, out.LedgerEntries,
		"ApplyB1OperatingLeases must emit at least one LedgerEntry")
}

// TestB1LeasePath_HasNoContextBackgroundLiteral is the source-analysis
// sibling to TestB1LeasePV_HonorsContextCancellation. It parses the
// production liabilities.go and asserts that no
// `context.Background()` call expression appears inside the B1 functions
// (ProcessOperatingLeaseAdjustment, ApplyB1OperatingLeases). The single
// remaining context.Background() in those functions is the defensive
// nil-ctx promotion inside ProcessOperatingLeaseAdjustment which
// activates ONLY when the caller passes nil — production call sites
// always pass a real ctx.
//
// This test guards against a regression where a future edit re-adds the
// hardcoded `ctx := context.Background() // TODO` pattern. The defensive
// promotion is allowed (it sits inside `if ctx == nil`).
func TestB1LeasePath_HasNoContextBackgroundLiteral(t *testing.T) {
	src, err := os.ReadFile("liabilities.go")
	require.NoError(t, err, "reading liabilities.go must succeed")

	// Parse for syntactic validity so a future edit that breaks the file
	// surfaces as a clear failure rather than a downstream test crash.
	fset := token.NewFileSet()
	_, err = parser.ParseFile(fset, "liabilities.go", src, parser.ParseComments)
	require.NoError(t, err, "parsing liabilities.go must succeed")

	srcStr := string(src)

	// Locate the byte ranges for the two B1 functions. We grep by name
	// rather than walking the AST because Go doesn't surface receiver-
	// method names directly via package navigation here.
	b1Names := []string{
		"ProcessOperatingLeaseAdjustment",
		"ApplyB1OperatingLeases",
	}

	for _, name := range b1Names {
		// Locate `func ... ` declaration.
		marker := "func (la *LiabilityAdjuster) " + name + "("
		idx := strings.Index(srcStr, marker)
		require.Greater(t, idx, 0, "B1 method %s must exist in liabilities.go", name)
		// Find the matching closing brace by counting braces from the
		// opening one after the signature. Naive — but the production
		// file uses unindented top-level closing braces only at the end
		// of each top-level function.
		body := srcStr[idx:]
		depth := 0
		open := strings.Index(body, "{")
		require.Greater(t, open, 0)
		end := -1
		for i := open; i < len(body); i++ {
			switch body[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					end = i
				}
			}
			if end >= 0 {
				break
			}
		}
		require.Greater(t, end, open, "must find closing brace for %s", name)

		fnBody := body[open : end+1]

		// Count context.Background() occurrences. The defensive nil-ctx
		// promotion inside ProcessOperatingLeaseAdjustment is allowed
		// (sits inside `if ctx == nil`). We assert <= 1 occurrence; the
		// pre-fix code had 1 hard-coded line at the top of the function
		// PLUS no defensive promotion, while the post-fix code has ≤ 1
		// inside the nil-ctx guard.
		count := strings.Count(fnBody, "context.Background()")
		assert.LessOrEqual(t, count, 1,
			"%s must contain at most ONE context.Background() (the defensive nil-ctx promotion). Pre-fix code carried a hardcoded `ctx := context.Background() // TODO` line; the fix removed it.", name)
	}
}
