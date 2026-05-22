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
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/midas/dcf-valuation-api/internal/config"
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

	replayCfg := replayConfig()

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

	replayCfg := replayConfig()

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
