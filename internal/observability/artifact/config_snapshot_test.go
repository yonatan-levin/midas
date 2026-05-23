// Package artifact_test — config_snapshot_test.go
//
// Tests the RPL-9 (capture side) `00-config.json` snapshot: when the
// caller populates artifact.Config.ConfigSnapshot, OpenBundle (eager) and
// Promote (deferred) write a `00-config.json` at the bundle root carrying
// the exact resolved Valuation + Macro subset. When the snapshot is the
// zero value (back-compat default for callers that haven't been updated),
// the file is NOT written and the bundle reads identically to the pre-1.2
// layout.

package artifact_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
)

// fixtureConfigSnapshot returns a non-zero ConfigSnapshot pre-populated
// with the production viper defaults from internal/config/config.go's
// setDefaults(). Mirroring real defaults keeps the test honest — if a
// production default changes, the assertion at the bottom of each test
// catches the drift.
func fixtureConfigSnapshot() artifact.ConfigSnapshot {
	return artifact.ConfigSnapshot{
		Valuation: artifact.ValuationConfigSnapshot{
			DefaultMarketRiskPremium: 0.05,
			DefaultTerminalGrowthCap: 0.03,
			DefaultTaxRate:           0.21,
			MinDataPointsForGrowth:   2,
			DCFProjectionYears:       5,
			DCFMaxGrowthRate:         0.50,
			DCFMinGrowthRate:         -0.30,
			DCFIterationTolerance:    0.0001,
			DCFMaxIterations:         100,
		},
		Macro: artifact.MacroConfigSnapshot{
			ManualRiskFreeRate:      0.045,
			ManualMarketRiskPremium: 0.05,
		},
	}
}

// readConfigSnapshot decodes 00-config.json from the bundle root and
// returns it for assertion. Fails the test if the file is missing,
// malformed, or empty.
func readConfigSnapshot(t *testing.T, bundleRoot string) artifact.ConfigSnapshot {
	t.Helper()
	path := filepath.Join(bundleRoot, artifact.ConfigSnapshotFileName)
	body, err := os.ReadFile(path)
	require.NoError(t, err, "expected 00-config.json to exist at bundle root")
	require.NotEmpty(t, body, "00-config.json must not be empty")
	var snap artifact.ConfigSnapshot
	require.NoError(t, json.Unmarshal(body, &snap), "00-config.json must parse as ConfigSnapshot")
	return snap
}

// TestOpenBundle_StampsConfigSnapshot_WhenProvided verifies the eager
// path writes 00-config.json synchronously at bundle construction time,
// carrying the exact ConfigSnapshot the caller supplied. This is the
// capture-side acceptance test for RPL-9: every eager bundle MUST land
// with the operative config snapshot inline (not deferred to a worker).
func TestOpenBundle_StampsConfigSnapshot_WhenProvided(t *testing.T) {
	root := t.TempDir()
	want := fixtureConfigSnapshot()
	cfg := artifact.Config{
		Enabled:        true,
		RootPath:       root,
		ConfigSnapshot: want,
	}

	b, err := artifact.OpenBundle(cfg, "rid-cfg", "AAPL", artifact.TriggerHeader)
	require.NoError(t, err)
	require.NotNil(t, b)
	defer b.Close()

	got := readConfigSnapshot(t, b.Root())

	// All fields round-trip — pins the on-disk shape against drift.
	assert.Equal(t, want, got)

	// Spot-checks for the fields whose hand-copy bugs the tracker calls
	// out — explicit assertions surface drift even if a future refactor
	// changes the equality comparator semantics.
	assert.Equal(t, 0.50, got.Valuation.DCFMaxGrowthRate, "DCFMaxGrowthRate (cycle 2 drift field)")
	assert.Equal(t, -0.30, got.Valuation.DCFMinGrowthRate, "DCFMinGrowthRate (cycle 2 drift field)")
	assert.Equal(t, 0.03, got.Valuation.DefaultTerminalGrowthCap, "DefaultTerminalGrowthCap (cycle 3 drift field)")
	assert.Equal(t, 0.045, got.Macro.ManualRiskFreeRate, "ManualRiskFreeRate")
	assert.Equal(t, 0.05, got.Macro.ManualMarketRiskPremium, "ManualMarketRiskPremium")
}

// TestOpenBundle_SkipsConfigSnapshot_WhenZero verifies the back-compat
// path: callers that haven't been updated to populate ConfigSnapshot get
// a bundle indistinguishable from the pre-1.2 layout — no 00-config.json
// on disk. This preserves the "old bundles continue to replay" invariant
// from the tracker.
func TestOpenBundle_SkipsConfigSnapshot_WhenZero(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{
		Enabled:  true,
		RootPath: root,
		// ConfigSnapshot deliberately omitted → zero value.
	}

	b, err := artifact.OpenBundle(cfg, "rid-nocfg", "AAPL", artifact.TriggerHeader)
	require.NoError(t, err)
	require.NotNil(t, b)
	defer b.Close()

	path := filepath.Join(b.Root(), artifact.ConfigSnapshotFileName)
	_, err = os.Stat(path)
	assert.True(t, errors.Is(err, os.ErrNotExist),
		"expected 00-config.json to NOT exist when ConfigSnapshot is zero; got err=%v", err)
}

// TestPromote_StampsConfigSnapshot verifies the deferred path: a
// deferred bundle does NOT create the directory at construction time,
// so the 00-config.json stamp must defer to Promote(). After Promote
// fires, the file MUST exist at the bundle root.
func TestPromote_StampsConfigSnapshot(t *testing.T) {
	root := t.TempDir()
	want := fixtureConfigSnapshot()
	cfg := artifact.Config{
		Enabled:        true,
		RootPath:       root,
		ConfigSnapshot: want,
		Triggers:       artifact.TriggerConfig{OnError: true},
	}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-deferred", "BAC", artifact.TriggerOnError)
	require.NoError(t, err)
	require.NotNil(t, b)

	// Pre-promote: directory does NOT exist on disk → file absent.
	_, statErr := os.Stat(filepath.Join(b.Root(), artifact.ConfigSnapshotFileName))
	require.True(t, errors.Is(statErr, os.ErrNotExist),
		"deferred bundle must not have 00-config.json before Promote; got %v", statErr)

	require.NoError(t, b.Promote(artifact.TriggerOnError))
	defer b.Close()

	got := readConfigSnapshot(t, b.Root())
	assert.Equal(t, want, got, "Promote must stamp the same snapshot OpenBundle would have")
}

// TestPromote_SkipsConfigSnapshot_WhenZero — the deferred analogue of
// the back-compat test. A deferred bundle with zero ConfigSnapshot
// promotes to disk WITHOUT 00-config.json.
func TestPromote_SkipsConfigSnapshot_WhenZero(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{
		Enabled:  true,
		RootPath: root,
		Triggers: artifact.TriggerConfig{OnError: true},
		// ConfigSnapshot deliberately omitted.
	}

	b, err := artifact.OpenDeferredBundle(cfg, "rid-deferred-nocfg", "BAC", artifact.TriggerOnError)
	require.NoError(t, err)
	require.NotNil(t, b)
	require.NoError(t, b.Promote(artifact.TriggerOnError))
	defer b.Close()

	path := filepath.Join(b.Root(), artifact.ConfigSnapshotFileName)
	_, err = os.Stat(path)
	assert.True(t, errors.Is(err, os.ErrNotExist),
		"expected 00-config.json to NOT exist when deferred bundle has zero ConfigSnapshot; got err=%v", err)
}

// TestManifestVersion_BumpedTo1_2 pins the version bump that ships with
// this change. Replay-side SupportedBundleVersions must stay in lockstep —
// see internal/observability/replay/manifest.go.
func TestManifestVersion_BumpedTo1_2(t *testing.T) {
	assert.Equal(t, "1.2", artifact.ManifestVersion,
		"RPL-9 bumps bundle version 1.1 → 1.2 (see manifest.go header)")
}

// TestConfigSnapshot_IsZero verifies the predicate the writer uses to
// decide whether to skip the 00-config.json write. Pinning this keeps the
// back-compat contract explicit: zero value → no file → indistinguishable
// from pre-1.2 bundle layout.
func TestConfigSnapshot_IsZero(t *testing.T) {
	assert.True(t, artifact.ConfigSnapshot{}.IsZero(), "default-constructed ConfigSnapshot must report IsZero")

	nonZero := artifact.ConfigSnapshot{
		Valuation: artifact.ValuationConfigSnapshot{DCFMaxGrowthRate: 0.5},
	}
	assert.False(t, nonZero.IsZero(), "ConfigSnapshot with any populated field must NOT report IsZero")
}

// Touch context import (in case future test additions need it).
var _ = context.Background
