package testhelpers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/valuation"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
)

// Phase Bootstrap status of the Service-level builders below:
//
// The midas valuation Service has 10+ DI dependencies (financial / market /
// macro repos, cache, datacleaner, datafetcher, growth estimator, model
// router, industry classifier, country-risk + industry-multiples configs,
// metrics, calc emitter, clock). Building a fully-wired test Service
// requires either:
//
//   (a) mocking every repository + gateway and re-implementing the fx
//       composition from internal/di/container.go, OR
//   (b) re-running the live engine against fixture data (heavier),
//
// neither of which belongs in Phase Bootstrap (whose remit is fixture
// capture + helper symbol pinning). The skeletons below are deliberately
// implemented as t.Skip()-with-clear-message so consumers (P1-P4) can
// reference the named symbols and import this package today, then fill in
// the implementation in the worktree that actually needs full-service
// integration testing.
//
// P1-P4 implementers: replace the t.Skip with the real wiring at the
// point of consumption; do not silently turn these into no-ops.

// BuildTestService constructs a fully-wired Service backed by the full
// fixture profile.Registry. Used by integration tests that need to
// exercise the full valuation lifecycle (service.CalculateValuation).
//
// Phase Bootstrap stub: skips. P1-P4 must replace with real wiring before
// any cross-model regression test can rely on it.
func BuildTestService(t *testing.T) *valuation.Service {
	t.Helper()
	t.Skip("BuildTestService: not implemented in Phase Bootstrap — wire fx-mocked Service in the consuming phase (P1-P4)")
	return nil
}

// BuildTestServiceWithFixedProfile constructs a Service that resolves
// EVERY ticker to the given profileID. Used by P2 to test DCF
// archetype-aware horizon without relying on the full resolver chain.
//
// Phase Bootstrap stub: skips. P2 owns the real implementation.
func BuildTestServiceWithFixedProfile(t *testing.T, profileID string) *valuation.Service {
	t.Helper()
	_ = profileID
	t.Skip("BuildTestServiceWithFixedProfile: not implemented in Phase Bootstrap — P2 wires this when DCF archetype-aware horizon lands")
	return nil
}

// RunValuation runs a full CalculateValuation against the test Service
// and returns the result for assertion. ticker MUST be one of the
// 10-ticker basket; the corresponding artifact bundle pre-populates
// the data repositories via test fixtures.
//
// Phase Bootstrap stub: skips. P1-P4 implement when full integration tests
// are added in their respective worktrees.
func RunValuation(t *testing.T, ticker string) *entities.ValuationResult {
	t.Helper()
	_ = ticker
	t.Skip("RunValuation: not implemented in Phase Bootstrap — P1-P4 wire this in their consuming worktree")
	return nil
}

// LoadGoldenJPMPrimaryValue returns the pre-Tier-2 captured
// IntrinsicValuePerShare for JPM, for bit-for-bit comparison in P3
// regression tests.
//
// This helper IS implemented in Phase Bootstrap because the underlying
// fixture (jpm_ddm_pre_tier2_output.json) is captured as part of Task B.2.
func LoadGoldenJPMPrimaryValue(t *testing.T) float64 {
	t.Helper()
	return loadGoldenPrimary(t, "jpm")
}

// loadGoldenPrimary reads the captured DDM golden output JSON for the
// given ticker (lowercase) and returns the IntrinsicValuePerShare field.
// Path is resolved relative to the consuming test file's module root via
// testGoldenPath so consumers from different packages can call it.
func loadGoldenPrimary(t *testing.T, ticker string) float64 {
	t.Helper()
	path := testGoldenPath(t, ticker+"_ddm_pre_tier2_output.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "golden output fixture missing at %s — run Task B.2 capture", path)
	var result models.ModelResult
	require.NoError(t, json.Unmarshal(data, &result))
	return result.IntrinsicValuePerShare
}

// testGoldenPath returns the absolute path to the DDM golden fixture
// directory. Walks upward from the test working directory until it finds
// the canonical testdata/golden path under internal/services/valuation/models;
// this lets cross-package consumers (e.g. tests in profile/) reference the
// shared fixtures without hard-coded relative paths.
func testGoldenPath(t *testing.T, filename string) string {
	t.Helper()
	const target = "internal/services/valuation/models/testdata/golden"
	wd, err := os.Getwd()
	require.NoError(t, err)
	for cur := wd; ; {
		candidate := filepath.Join(cur, target, filename)
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			// Hit filesystem root without finding the golden directory.
			t.Fatalf("golden fixture %s not found anywhere under %s upward to root", filename, wd)
			return ""
		}
		cur = parent
	}
}
