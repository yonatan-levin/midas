// Package artifact — config_snapshot.go
//
// Defines the on-disk shape (`00-config.json`) that captures the effective
// production config under which a bundled request ran. Closes RPL-9
// (capture-side): the replay binary previously had to mirror production
// viper defaults by hand-copy, and any divergence silently corrupted
// re-runs (cycles 1+2+3 of the replay-fidelity debug each fixed one
// instance — DCFMaxGrowthRate, DCFMinGrowthRate, DefaultTerminalGrowthCap).
// Stamping the resolved subset into the bundle at capture time makes the
// hand-copy class of bug impossible by construction once the replay-side
// consumer (RPL-9 follow-up) is wired up.
//
// Scope: the minimal Valuation + Macro subset that algorithmically affects
// downstream computation. Out-of-scope: runtime-only fields (cache TTLs,
// server timeouts, log levels) — those don't change valuation math.
//
// Backward compatibility: bundles captured pre-1.2 simply lack the
// `00-config.json` file. Replay-side consumers fall back to the hardcoded
// production-defaults mirror in `replay/module.go::replayConfig()` (RPL-10
// stopgap) when the file is absent.

package artifact

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ConfigSnapshotFileName is the canonical basename for the bundle-level
// config snapshot. Lives at the bundle root next to `00-manifest.json` —
// the `00-` prefix groups it with manifest metadata in directory listings,
// signalling its role as bundle-level (not per-request handler) data.
const ConfigSnapshotFileName = "00-config.json"

// ConfigSnapshot is the subset of the resolved production config that
// algorithmically affects downstream valuation. Captured at bundle
// construction time and written to `00-config.json` so the replay binary
// can re-run against the EXACT configuration the original request used,
// not whatever the replay binary's hardcoded defaults happened to be at
// replay-time.
//
// Field naming uses snake_case to mirror the on-disk YAML / viper keys
// (default_market_risk_premium, dcf_max_growth_rate, …) so postmortem
// readers can grep across config.yaml ↔ bundle without translation.
//
// Schema is stable across 1.x bundle versions; new fields MUST be additive
// (zero-value = "not captured" via omitempty) so old replay binaries
// reading new bundles don't fail decode.
type ConfigSnapshot struct {
	// Valuation holds the *config.Valuation subset consumed by valuation.Service
	// and growth.Estimator. Cycles 1+2+3 of the replay-fidelity debug found
	// drift on DCFMaxGrowthRate, DCFMinGrowthRate, and DefaultTerminalGrowthCap
	// — capturing ALL of them defense-in-depth closes the class.
	Valuation ValuationConfigSnapshot `json:"valuation"`

	// Macro holds the *config.Macro subset used as fallback when FRED is
	// unavailable. ManualMarketRiskPremium is consumed by
	// BundleMacroGateway. ManualRiskFreeRate is not consumed today by
	// replay-reachable code paths (replay uses the bundle's macro snapshot)
	// but is captured defense-in-depth so future engine changes that start
	// reading it don't introduce a new drift channel.
	Macro MacroConfigSnapshot `json:"macro"`
}

// ValuationConfigSnapshot mirrors the algorithmically-load-bearing subset
// of config.ValuationConfig (internal/config/config.go:263). Field order
// matches the struct declaration upstream so a future addition there
// surfaces here as a one-line append.
type ValuationConfigSnapshot struct {
	DefaultMarketRiskPremium float64 `json:"default_market_risk_premium"`
	DefaultTerminalGrowthCap float64 `json:"default_terminal_growth_cap"`
	DefaultTaxRate           float64 `json:"default_tax_rate"`
	MinDataPointsForGrowth   int     `json:"min_data_points_for_growth"`

	// DCF calculation specific settings.
	DCFProjectionYears    int     `json:"dcf_projection_years"`
	DCFMaxGrowthRate      float64 `json:"dcf_max_growth_rate"`
	DCFMinGrowthRate      float64 `json:"dcf_min_growth_rate"`
	DCFIterationTolerance float64 `json:"dcf_iteration_tolerance"`
	DCFMaxIterations      int     `json:"dcf_max_iterations"`
}

// MacroConfigSnapshot mirrors the relevant subset of config.MacroConfig
// (internal/config/config.go:255). Only the manual fallback rates are
// captured — FRED API key / URL / enabled flag are deployment concerns
// and have no effect on saved bundles (the gateway uses the bundle's macro
// snapshot, not live FRED).
type MacroConfigSnapshot struct {
	ManualRiskFreeRate      float64 `json:"manual_risk_free_rate"`
	ManualMarketRiskPremium float64 `json:"manual_market_risk_premium"`
}

// IsZero reports whether the snapshot is the zero-value (i.e. config
// capture was disabled or the operator never set ConfigSnapshot on
// artifact.Config). Used by the writer to skip stamping `00-config.json`
// when the caller did not supply a snapshot — preserves backward
// compatibility for tests / callers that construct artifact.Config inline
// without populating the new field.
func (s ConfigSnapshot) IsZero() bool {
	return s == ConfigSnapshot{}
}

// writeConfigSnapshot serialises a ConfigSnapshot to `00-config.json`
// under bundleRoot. Returns nil if snap is zero-value (no-op for back-
// compat callers). Returns a wrapped error on marshal / write failure;
// the caller is responsible for surfacing it (typically by incrementing
// writeErrors so the manifest's outcome degrades to "partial").
func writeConfigSnapshot(bundleRoot string, snap ConfigSnapshot) error {
	if snap.IsZero() {
		return nil
	}
	body, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("artifact: marshal config snapshot: %w", err)
	}
	path := filepath.Join(bundleRoot, ConfigSnapshotFileName)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("artifact: write config snapshot %s: %w", path, err)
	}
	return nil
}

// ReadConfigSnapshot reads 00-config.json from bundleRoot. Returns found=false
// (no error) when the file is absent — pre-1.2 bundles legitimately lack it, and
// callers fall back to the hand-mirrored defaults. Returns a wrapped error only on
// a present-but-unreadable / malformed file. This is the read-side mirror of
// writeConfigSnapshot, consumed by the replay binary (RPL-9) to overlay the
// bundle's captured production config onto its hand-mirrored fallback defaults.
func ReadConfigSnapshot(bundleRoot string) (snap ConfigSnapshot, found bool, err error) {
	path := filepath.Join(bundleRoot, ConfigSnapshotFileName)
	body, err := os.ReadFile(path)
	if err != nil {
		// Absent file is the back-compat signal, not an error: pre-1.2
		// bundles never wrote 00-config.json.
		if errors.Is(err, fs.ErrNotExist) {
			return ConfigSnapshot{}, false, nil
		}
		return ConfigSnapshot{}, false, fmt.Errorf("artifact: read config snapshot %s: %w", path, err)
	}
	if err := json.Unmarshal(body, &snap); err != nil {
		return ConfigSnapshot{}, false, fmt.Errorf("artifact: unmarshal config snapshot %s: %w", path, err)
	}
	return snap, true, nil
}
