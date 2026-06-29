package sec

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// SR-1 B4 regression test — the TotalAssets candidate list mixed the umbrella
// tag with COMPONENT tags under first-hit semantics:
//
//	findValue(data, {"Assets", "AssetsCurrent", "AssetsNoncurrent"})
//
// For a filer missing the us-gaap:Assets umbrella, TotalAssets silently became
// CURRENT assets only — understating the balance sheet and corrupting every
// downstream ratio gate (A1/A2/A4 materiality, the DC-1 plugs, Graham floor).
// The corrected shape mirrors the TSM debt-components pattern: umbrella
// first-hit, then sumValues over the disjoint current+noncurrent components.
//
// See docs/reviewer/archive/SR-1-simplify-and-code-review-candidates.md §B4.
func TestParser_ParseFinancialData_TotalAssetsComponentFallback(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	usGAAPFact := func(val float64) ports.SECFactGroup {
		return ports.SECFactGroup{
			Units: map[string][]ports.SECFact{
				"USD": {
					{End: "2023-09-30", Val: val, Accn: "0000320193-23-000106", Fy: 2023, Fp: "FY", Form: "10-K", Filed: "2023-11-03"},
				},
			},
		}
	}

	parse := func(t *testing.T, extra map[string]ports.SECFactGroup) float64 {
		t.Helper()
		usGAAP := map[string]ports.SECFactGroup{
			"Revenues":            usGAAPFact(100_000_000_000),
			"OperatingIncomeLoss": usGAAPFact(20_000_000_000),
		}
		for k, v := range extra {
			usGAAP[k] = v
		}
		hist, err := parser.ParseFinancialData(context.Background(), &ports.SECCompanyFacts{
			CIK:        ports.FlexibleCIK("320193"),
			EntityName: "Test Filer",
			Facts:      map[string]map[string]ports.SECFactGroup{"us-gaap": usGAAP},
		})
		require.NoError(t, err)
		latest := hist.Data["2023FY"]
		require.NotNil(t, latest)
		return latest.TotalAssets
	}

	t.Run("umbrella_present_wins", func(t *testing.T) {
		got := parse(t, map[string]ports.SECFactGroup{
			"Assets":           usGAAPFact(352_583_000_000),
			"AssetsCurrent":    usGAAPFact(143_566_000_000),
			"AssetsNoncurrent": usGAAPFact(209_017_000_000),
		})
		assert.Equal(t, 352_583_000_000.0, got,
			"the umbrella tag is authoritative when present (never summed with components)")
	})

	t.Run("missing_umbrella_sums_components", func(t *testing.T) {
		// THE B4 BUG: pre-fix this returned 143.566B (current assets only).
		got := parse(t, map[string]ports.SECFactGroup{
			"AssetsCurrent":    usGAAPFact(143_566_000_000),
			"AssetsNoncurrent": usGAAPFact(209_017_000_000),
		})
		assert.Equal(t, 352_583_000_000.0, got,
			"missing umbrella must fall back to AssetsCurrent + AssetsNoncurrent, not current-only")
	})

	t.Run("single_component_only_still_usable", func(t *testing.T) {
		// Only one component reported: same value as the pre-fix behavior
		// (still understated, but strictly no worse — sumValues over one
		// present tag returns that tag).
		got := parse(t, map[string]ports.SECFactGroup{
			"AssetsCurrent": usGAAPFact(143_566_000_000),
		})
		assert.Equal(t, 143_566_000_000.0, got)
	})

	t.Run("no_asset_tags_leaves_zero", func(t *testing.T) {
		got := parse(t, nil)
		assert.Zero(t, got, "no asset tags at all must leave TotalAssets at 0 (missing_fields records it)")
	})
}
