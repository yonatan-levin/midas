package config

import (
	"os"
	"path/filepath"
	"testing"
)

func float64Ptr(v float64) *float64 { return &v }

// TestLoadAdjustmentThresholdsConfig_Absent_UsesDefaults verifies the loader
// returns an error for a non-existent path (so callers fall back to defaults).
func TestLoadAdjustmentThresholdsConfig_Absent_UsesDefaults(t *testing.T) {
	_, err := LoadAdjustmentThresholdsConfig(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err == nil {
		t.Fatalf("expected error loading a non-existent config path, got nil")
	}
}

// TestLoadAdjustmentThresholdsConfig_RoundTrip loads a well-formed config from
// disk and confirms the pointer fields are populated.
func TestLoadAdjustmentThresholdsConfig_RoundTrip(t *testing.T) {
	path := writeTempThresholdConfig(t, `{
		"version": "1.0.0",
		"asset": {
			"a1_goodwill": {"materiality_ratio": 0.07, "significance_flag_ratio": 0.12}
		}
	}`)
	cfg, err := LoadAdjustmentThresholdsConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Asset.A1Goodwill.MaterialityRatio == nil || *cfg.Asset.A1Goodwill.MaterialityRatio != 0.07 {
		t.Errorf("A1 materiality_ratio = %v, want 0.07", cfg.Asset.A1Goodwill.MaterialityRatio)
	}
	if cfg.Asset.A1Goodwill.SignificanceFlagRatio == nil || *cfg.Asset.A1Goodwill.SignificanceFlagRatio != 0.12 {
		t.Errorf("A1 significance_flag_ratio = %v, want 0.12", cfg.Asset.A1Goodwill.SignificanceFlagRatio)
	}
	// Absent keys stay nil.
	if cfg.Asset.A2Intangible.MaterialityRatio != nil {
		t.Errorf("A2 materiality_ratio should be nil (absent), got %v", *cfg.Asset.A2Intangible.MaterialityRatio)
	}
}

// TestLoadAdjustmentThresholdsConfig_Validate covers the Validate() rules:
// version required; present ratios must be in (0,1]; absent keys are fine.
func TestLoadAdjustmentThresholdsConfig_Validate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     AdjustmentThresholdsConfig
		wantErr bool
	}{
		{
			name:    "empty version rejected",
			cfg:     AdjustmentThresholdsConfig{Version: ""},
			wantErr: true,
		},
		{
			name:    "version present, all keys absent accepted",
			cfg:     AdjustmentThresholdsConfig{Version: "1.0.0"},
			wantErr: false,
		},
		{
			name: "ratio of 0 rejected (out of (0,1])",
			cfg: AdjustmentThresholdsConfig{
				Version: "1.0.0",
				Asset:   AssetThresholdConfig{A1Goodwill: GoodwillThresholdConfig{MaterialityRatio: float64Ptr(0)}},
			},
			wantErr: true,
		},
		{
			name: "ratio above 1 rejected",
			cfg: AdjustmentThresholdsConfig{
				Version: "1.0.0",
				Asset:   AssetThresholdConfig{A1Goodwill: GoodwillThresholdConfig{MaterialityRatio: float64Ptr(1.5)}},
			},
			wantErr: true,
		},
		{
			name: "ratio of exactly 1 accepted (upper-inclusive)",
			cfg: AdjustmentThresholdsConfig{
				Version: "1.0.0",
				Asset:   AssetThresholdConfig{A1Goodwill: GoodwillThresholdConfig{MaterialityRatio: float64Ptr(1.0)}},
			},
			wantErr: false,
		},
		{
			name: "negative review ratio rejected",
			cfg: AdjustmentThresholdsConfig{
				Version: "1.0.0",
				Asset:   AssetThresholdConfig{Reviews: ReviewThresholdConfig{RDCapitalizationRatio: float64Ptr(-0.1)}},
			},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// TestLoadAdjustmentThresholdsConfig_RejectsInvalid confirms the loader runs
// Validate() and surfaces the error (a present ratio of 0).
func TestLoadAdjustmentThresholdsConfig_RejectsInvalid(t *testing.T) {
	path := writeTempThresholdConfig(t, `{"version": "1.0.0", "asset": {"a1_goodwill": {"materiality_ratio": 0}}}`)
	if _, err := LoadAdjustmentThresholdsConfig(path); err == nil {
		t.Fatalf("expected loader to reject a config with materiality_ratio=0")
	}
}

// TestLoadAdjustmentThresholdsConfig_EnvPath verifies the env-var fallback path
// is honoured when configPath is empty.
func TestLoadAdjustmentThresholdsConfig_EnvPath(t *testing.T) {
	path := writeTempThresholdConfig(t, `{"version": "1.0.0"}`)
	t.Setenv("ADJUSTMENT_THRESHOLDS_CONFIG_PATH", path)
	cfg, err := LoadAdjustmentThresholdsConfig("")
	if err != nil {
		t.Fatalf("unexpected error loading via env path: %v", err)
	}
	if cfg.Version != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", cfg.Version)
	}
}

func writeTempThresholdConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "adjustment_thresholds.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}
	return path
}
