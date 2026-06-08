package datacleaner

import (
	"path/filepath"
	"testing"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/adjustments"
)

func float64Ptr(v float64) *float64 { return &v }

// TestResolveAssetThresholds_NilConfig_ReturnsDefaults proves a nil config (the
// absent-file path) yields the unmodified defaults.
func TestResolveAssetThresholds_NilConfig_ReturnsDefaults(t *testing.T) {
	def := adjustments.DefaultAssetThresholds()
	got := ResolveAssetThresholds(def, nil)
	if got != def {
		t.Errorf("ResolveAssetThresholds(def, nil) = %+v, want defaults %+v", got, def)
	}
}

// TestResolveAssetThresholds_PartialOverride proves only the provided key is
// overridden; every other field keeps its default. This is the regression-safe
// merge-over-defaults contract.
func TestResolveAssetThresholds_PartialOverride(t *testing.T) {
	def := adjustments.DefaultAssetThresholds()
	cfg := &config.AdjustmentThresholdsConfig{
		Version: "1.0.0",
		Asset: config.AssetThresholdConfig{
			A1Goodwill: config.GoodwillThresholdConfig{
				MaterialityRatio: float64Ptr(0.2),
			},
		},
	}

	got := ResolveAssetThresholds(def, cfg)

	if got.GoodwillMateriality != 0.2 {
		t.Errorf("GoodwillMateriality = %v, want overridden 0.2", got.GoodwillMateriality)
	}
	// Every other field must equal the default.
	want := def
	want.GoodwillMateriality = 0.2
	if got != want {
		t.Errorf("partial override leaked: got %+v, want %+v (only GoodwillMateriality changed)", got, want)
	}
}

// TestResolveAssetThresholds_AllKeys proves every config key maps to the correct
// resolved field (guards against a copy-paste wiring bug).
func TestResolveAssetThresholds_AllKeys(t *testing.T) {
	def := adjustments.DefaultAssetThresholds()
	cfg := &config.AdjustmentThresholdsConfig{
		Version: "1.0.0",
		Asset: config.AssetThresholdConfig{
			A1Goodwill:   config.GoodwillThresholdConfig{MaterialityRatio: float64Ptr(0.11), SignificanceFlagRatio: float64Ptr(0.12)},
			A2Intangible: config.RatioThresholdConfig{MaterialityRatio: float64Ptr(0.13)},
			A4DTA:        config.GoodwillThresholdConfig{MaterialityRatio: float64Ptr(0.14), SignificanceFlagRatio: float64Ptr(0.15)},
			A6RightOfUse: config.GoodwillThresholdConfig{MaterialityRatio: float64Ptr(0.16), SignificanceFlagRatio: float64Ptr(0.17)},
			Reviews:      config.ReviewThresholdConfig{RDCapitalizationRatio: float64Ptr(0.18), CapitalizedSoftwareRatio: float64Ptr(0.19)},
		},
	}

	got := ResolveAssetThresholds(def, cfg)
	want := adjustments.AssetThresholds{
		GoodwillMateriality:       0.11,
		GoodwillSignificanceFlag:  0.12,
		IntangibleMateriality:     0.13,
		DTAMateriality:            0.14,
		DTASignificanceFlag:       0.15,
		ROUMateriality:            0.16,
		ROUSignificanceFlag:       0.17,
		RDCapitalizationReview:    0.18,
		CapitalizedSoftwareReview: 0.19,
	}
	if got != want {
		t.Errorf("ResolveAssetThresholds full override = %+v, want %+v", got, want)
	}
}

// TestShippedConfig_ResolvesToDefaults pins the load-bearing invariant from
// spec §6: the SHIPPED config/datacleaner/adjustment_thresholds.json must parse,
// validate, and resolve to EXACTLY DefaultAssetThresholds — so the present-file
// runtime path is byte-identical to the absent-file path. If the shipped file
// ever drifts from the defaults without a deliberate decision, this fails CI.
func TestShippedConfig_ResolvesToDefaults(t *testing.T) {
	// Test cwd is internal/services/datacleaner; the repo root is three up.
	path := filepath.Join("..", "..", "..", "config", "datacleaner", "adjustment_thresholds.json")
	cfg, err := config.LoadAdjustmentThresholdsConfig(path)
	if err != nil {
		t.Fatalf("shipped adjustment_thresholds.json failed to load/validate: %v", err)
	}
	def := adjustments.DefaultAssetThresholds()
	if got := ResolveAssetThresholds(def, cfg); got != def {
		t.Errorf("shipped config resolves to %+v, want defaults %+v — the shipped file MUST stay byte-equal to defaults", got, def)
	}
}
