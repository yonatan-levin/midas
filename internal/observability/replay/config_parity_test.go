package replay

// RPL-10 — Golden parity test: replayConfig() must mirror every
// production viper default declared in internal/config/config.go's
// setDefaults() body. Cycles 1+2+3 of the replay-fidelity debug each
// fixed one instance of "replay-side config field hand-copied wrong
// from production default". This test catches the next instance at
// compile/test time rather than at the next replay-fidelity debug
// cycle.
//
// The tracker (docs/reviewer/RPL10-replay-config-mirror-defense-in-
// depth.md) is the design source. The fix has two layers: (1)
// replayConfig() at module.go now mirrors all non-zero Valuation +
// non-zero Macro viper defaults; (2) this test pins the parity.
//
// Once RPL-9 (manifest-config snapshot) lands, replayConfig() becomes
// a fallback for old bundles only and this test gets repurposed to
// assert "old-bundle-fallback ↔ production defaults" parity, which
// is a smaller surface but the same shape.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
)

// TestReplayConfig_MirrorsAllValuationViperDefaults asserts that every
// field in config.ValuationConfig matches between replayConfig().Valuation
// and a freshly-defaulted Config from config.LoadDefaults(). LoadDefaults
// loads ONLY viper defaults — no config file, no env vars — so it is the
// canonical "production defaults" snapshot.
//
// Test fails the moment a new viper.SetDefault is added to setDefaults()
// without a corresponding mirror in replayConfig(). The cmp.Diff output
// names the drifting field directly so the fix is obvious.
func TestReplayConfig_MirrorsAllValuationViperDefaults(t *testing.T) {
	productionDefaults, err := config.LoadDefaults()
	if err != nil {
		t.Fatalf("config.LoadDefaults: %v", err)
	}

	// Empty bundle dir → no 00-config.json → the RPL-9 overlay is a no-op,
	// so this exercises the pre-1.2 hand-mirror fallback path (the surface
	// this parity test guards).
	replayCfg, err := replayConfig(t.TempDir())
	if err != nil {
		t.Fatalf("replayConfig: %v", err)
	}

	if diff := cmp.Diff(productionDefaults.Valuation, replayCfg.Valuation); diff != "" {
		t.Fatalf("replayConfig.Valuation drifts from production viper defaults (-production +replay):\n%s\n\n"+
			"Fix: mirror the differing field in internal/observability/replay/module.go::replayConfig(). "+
			"If the field is intentionally unconsumed by replay, mirror it anyway — defense in depth. "+
			"See docs/reviewer/RPL10-replay-config-mirror-defense-in-depth.md.", diff)
	}
}

// TestReplayConfig_MirrorsAllMacroViperDefaults extends the parity check
// to config.MacroConfig. Replay consumes Macro.ManualMarketRiskPremium
// (debug cycle 2 finding HIGH-1) and may consume more Macro fields in
// the future, so the same defense-in-depth applies.
//
// NOTE: ManualRiskFreeRate is not currently consumed by replay-reachable
// paths (replay uses the bundle's macro snapshot for the risk-free rate),
// but RPL-10 mirrors it. FRED toggles (FREDEnabled, FREDAPIKey,
// FREDBaseURL) are NOT mirrored because replay deliberately disables
// FRED — the bundle is the source of truth — and mirroring them would
// pin a fragile contract that "production FRED behaviour matches
// replay's gateway substitution". A future Macro field that DOES affect
// replay behaviour should be added to replayConfig() AND to the cmp.Diff
// comparator below.
//
// To keep the surface tight without losing the parity-test benefit, we
// compare only the two manual-rate fields that genuinely affect replay
// fidelity. If a new manual-rate-style field is added to MacroConfig,
// add it here too.
func TestReplayConfig_MirrorsAllMacroViperDefaults(t *testing.T) {
	productionDefaults, err := config.LoadDefaults()
	if err != nil {
		t.Fatalf("config.LoadDefaults: %v", err)
	}

	replayCfg, err := replayConfig(t.TempDir())
	if err != nil {
		t.Fatalf("replayConfig: %v", err)
	}

	// Compare only the two manual-rate fields. Using a sub-struct
	// avoids pinning the FRED toggle surface which replay deliberately
	// diverges on.
	type manualRates struct {
		ManualRiskFreeRate      float64
		ManualMarketRiskPremium float64
	}
	prod := manualRates{
		ManualRiskFreeRate:      productionDefaults.Macro.ManualRiskFreeRate,
		ManualMarketRiskPremium: productionDefaults.Macro.ManualMarketRiskPremium,
	}
	rep := manualRates{
		ManualRiskFreeRate:      replayCfg.Macro.ManualRiskFreeRate,
		ManualMarketRiskPremium: replayCfg.Macro.ManualMarketRiskPremium,
	}

	if diff := cmp.Diff(prod, rep); diff != "" {
		t.Fatalf("replayConfig.Macro manual-rate fields drift from production viper defaults (-production +replay):\n%s\n\n"+
			"Fix: mirror the differing field in internal/observability/replay/module.go::replayConfig(). "+
			"See docs/reviewer/RPL10-replay-config-mirror-defense-in-depth.md.", diff)
	}
}

// writeBundleConfigSnapshot serialises a ConfigSnapshot to 00-config.json
// under bundleDir, mirroring the capture-side writer. Used by the RPL-9
// overlay tests below to stage a bundle the replay consumer reads back.
func writeBundleConfigSnapshot(t *testing.T, bundleDir string, snap artifact.ConfigSnapshot) {
	t.Helper()
	body, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatalf("marshal config snapshot: %v", err)
	}
	path := filepath.Join(bundleDir, artifact.ConfigSnapshotFileName)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write config snapshot: %v", err)
	}
}

// TestReplayConfig_BundleSnapshotOverridesHandMirror is the RPL-9 golden
// divergence test (acceptance criterion #3): a 1.2+ bundle that ships a
// 00-config.json captured under a NON-default production config must drive
// replay from THOSE values, not the hand-mirrored defaults. We stage a
// snapshot with values deliberately divergent from the hand-mirror and assert
// replayConfig surfaces the bundle's values — proving the bundle overrides.
func TestReplayConfig_BundleSnapshotOverridesHandMirror(t *testing.T) {
	bundleDir := t.TempDir()
	writeBundleConfigSnapshot(t, bundleDir, artifact.ConfigSnapshot{
		Valuation: artifact.ValuationConfigSnapshot{
			DefaultMarketRiskPremium: 0.058,
			DefaultTerminalGrowthCap: 0.025,
			DefaultTaxRate:           0.22, // hand-mirror is 0.21
			MinDataPointsForGrowth:   3,
			DCFProjectionYears:       7,    // hand-mirror is 5
			DCFMaxGrowthRate:         0.42, // hand-mirror is 0.50
			DCFMinGrowthRate:         -0.25,
			DCFIterationTolerance:    0.00005,
			DCFMaxIterations:         200,
		},
		Macro: artifact.MacroConfigSnapshot{
			ManualRiskFreeRate:      0.041,
			ManualMarketRiskPremium: 0.066, // hand-mirror is 0.05
		},
	})

	cfg, err := replayConfig(bundleDir)
	if err != nil {
		t.Fatalf("replayConfig: %v", err)
	}

	// Every overlaid field must carry the bundle's value, not the hand-mirror.
	// We assert ALL 11 captured fields (not a subset) so that a future field
	// added to artifact.ConfigSnapshot without a matching overlay line in
	// module.go::replayConfig fails HERE — a silent partial overlay would
	// reintroduce exactly the hand-copy drift class RPL-9 exists to kill
	// (REVIEWER MEDIUM, 2026-06-29). All staged values above are deliberately
	// divergent from the hand-mirror defaults (see inline mirror values).
	assertEqualFloat(t, "DefaultMarketRiskPremium", 0.058, cfg.Valuation.DefaultMarketRiskPremium)
	assertEqualFloat(t, "DefaultTerminalGrowthCap", 0.025, cfg.Valuation.DefaultTerminalGrowthCap)
	assertEqualFloat(t, "DefaultTaxRate", 0.22, cfg.Valuation.DefaultTaxRate)
	assertEqualFloat(t, "DCFMaxGrowthRate", 0.42, cfg.Valuation.DCFMaxGrowthRate)
	assertEqualFloat(t, "DCFMinGrowthRate", -0.25, cfg.Valuation.DCFMinGrowthRate)
	assertEqualFloat(t, "DCFIterationTolerance", 0.00005, cfg.Valuation.DCFIterationTolerance)
	assertEqualFloat(t, "ManualRiskFreeRate", 0.041, cfg.Macro.ManualRiskFreeRate)
	assertEqualFloat(t, "ManualMarketRiskPremium", 0.066, cfg.Macro.ManualMarketRiskPremium)
	if cfg.Valuation.MinDataPointsForGrowth != 3 {
		t.Fatalf("MinDataPointsForGrowth = %d, want 3 (bundle override)", cfg.Valuation.MinDataPointsForGrowth)
	}
	if cfg.Valuation.DCFProjectionYears != 7 {
		t.Fatalf("DCFProjectionYears = %d, want 7 (bundle override)", cfg.Valuation.DCFProjectionYears)
	}
	if cfg.Valuation.DCFMaxIterations != 200 {
		t.Fatalf("DCFMaxIterations = %d, want 200 (bundle override)", cfg.Valuation.DCFMaxIterations)
	}
}

// TestReplayConfig_AbsentBundleUsesHandMirror pins the back-compat path
// (acceptance criterion #6): a bundle with no 00-config.json (pre-1.2) leaves
// replayConfig on the hand-mirrored production defaults.
func TestReplayConfig_AbsentBundleUsesHandMirror(t *testing.T) {
	cfg, err := replayConfig(t.TempDir()) // empty dir → no snapshot file
	if err != nil {
		t.Fatalf("replayConfig: %v", err)
	}
	assertEqualFloat(t, "DCFMaxGrowthRate", 0.50, cfg.Valuation.DCFMaxGrowthRate)
	assertEqualFloat(t, "DefaultTaxRate", 0.21, cfg.Valuation.DefaultTaxRate)
	if cfg.Valuation.DCFProjectionYears != 5 {
		t.Fatalf("DCFProjectionYears = %d, want 5 (hand-mirror fallback)", cfg.Valuation.DCFProjectionYears)
	}
}

// TestReplayConfig_MalformedSnapshotErrors verifies the fail-loud contract: a
// present-but-corrupt 00-config.json makes replayConfig return a non-nil error
// rather than silently falling back. Consistent with replay's strict
// schema/hash drift philosophy.
func TestReplayConfig_MalformedSnapshotErrors(t *testing.T) {
	bundleDir := t.TempDir()
	path := filepath.Join(bundleDir, artifact.ConfigSnapshotFileName)
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("seed malformed snapshot: %v", err)
	}

	cfg, err := replayConfig(bundleDir)
	if err == nil {
		t.Fatalf("replayConfig returned nil error on malformed 00-config.json; want non-nil")
	}
	if cfg != nil {
		t.Fatalf("replayConfig returned non-nil config alongside error; want nil config")
	}
}

// assertEqualFloat is a small helper to keep the override assertions readable.
func assertEqualFloat(t *testing.T, name string, want, got float64) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}
