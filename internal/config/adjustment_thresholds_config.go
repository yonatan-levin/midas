package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// AdjustmentThresholdsConfig is the externalized datacleaner adjuster
// materiality / review gate-threshold config (TDB-5). It mirrors the direct
// JSON-loader pattern of FlagConditionsConfig (os.ReadFile + json.Unmarshal +
// Validate), NOT the viper config.yaml path.
//
// Every ratio is a pointer (*float64) so the loader can distinguish "key
// absent" (nil → caller keeps its in-code default) from "present value 0"
// (caught by Validate as out of range). A resolver merges the present keys over
// the adjuster's DefaultAssetThresholds; an absent file or absent key yields
// byte-identical pre-TDB-5 behaviour.
type AdjustmentThresholdsConfig struct {
	Version     string               `json:"version"`
	Description string               `json:"description,omitempty"`
	UpdatedAt   string               `json:"updated_at,omitempty"`
	Asset       AssetThresholdConfig `json:"asset"`
}

// AssetThresholdConfig groups the Category A gate thresholds by adjuster,
// mirroring the nested grouping of lease_estimation.json. Only the asset group
// exists in the first cut; a future liability/earnings group slots in
// additively without breaking the loader.
type AssetThresholdConfig struct {
	A1Goodwill   GoodwillThresholdConfig `json:"a1_goodwill"`
	A2Intangible RatioThresholdConfig    `json:"a2_intangible"`
	A4DTA        GoodwillThresholdConfig `json:"a4_dta"`
	A6RightOfUse GoodwillThresholdConfig `json:"a6_right_of_use"`
	Reviews      ReviewThresholdConfig   `json:"reviews"`
}

// GoodwillThresholdConfig carries a {materiality, significance-flag} gate pair.
// Reused by A1 / A4 / A6, all of which have the same two-gate shape.
type GoodwillThresholdConfig struct {
	MaterialityRatio      *float64 `json:"materiality_ratio,omitempty"`
	SignificanceFlagRatio *float64 `json:"significance_flag_ratio,omitempty"`
}

// RatioThresholdConfig carries a single materiality gate (A2).
type RatioThresholdConfig struct {
	MaterialityRatio *float64 `json:"materiality_ratio,omitempty"`
}

// ReviewThresholdConfig carries the review-only gates (A-RD, A-SW).
type ReviewThresholdConfig struct {
	RDCapitalizationRatio    *float64 `json:"rd_capitalization_ratio,omitempty"`
	CapitalizedSoftwareRatio *float64 `json:"capitalized_software_ratio,omitempty"`
}

// LoadAdjustmentThresholdsConfig loads the adjuster threshold config from a
// file. Resolution order mirrors LoadFlagConditionsConfig: explicit path → the
// ADJUSTMENT_THRESHOLDS_CONFIG_PATH env var → the default repo path. A read,
// parse, or validation failure is returned as an error so the caller can fall
// back to in-code defaults (warn-and-fallback in NewDataCleanerService).
func LoadAdjustmentThresholdsConfig(configPath string) (*AdjustmentThresholdsConfig, error) {
	if configPath == "" {
		configPath = os.Getenv("ADJUSTMENT_THRESHOLDS_CONFIG_PATH")
		if configPath == "" {
			configPath = "config/datacleaner/adjustment_thresholds.json"
		}
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read adjustment thresholds config file: %w", err)
	}

	var cfg AdjustmentThresholdsConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse adjustment thresholds config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid adjustment thresholds config: %w", err)
	}

	return &cfg, nil
}

// Validate enforces the schema invariants: version is required, and every
// PRESENT ratio must fall in the half-open range (0, 1]. Absent keys (nil
// pointers) are not an error — they fall back to in-code defaults.
func (c *AdjustmentThresholdsConfig) Validate() error {
	if c.Version == "" {
		return fmt.Errorf("configuration version is required")
	}

	ratios := []struct {
		name string
		val  *float64
	}{
		{"asset.a1_goodwill.materiality_ratio", c.Asset.A1Goodwill.MaterialityRatio},
		{"asset.a1_goodwill.significance_flag_ratio", c.Asset.A1Goodwill.SignificanceFlagRatio},
		{"asset.a2_intangible.materiality_ratio", c.Asset.A2Intangible.MaterialityRatio},
		{"asset.a4_dta.materiality_ratio", c.Asset.A4DTA.MaterialityRatio},
		{"asset.a4_dta.significance_flag_ratio", c.Asset.A4DTA.SignificanceFlagRatio},
		{"asset.a6_right_of_use.materiality_ratio", c.Asset.A6RightOfUse.MaterialityRatio},
		{"asset.a6_right_of_use.significance_flag_ratio", c.Asset.A6RightOfUse.SignificanceFlagRatio},
		{"asset.reviews.rd_capitalization_ratio", c.Asset.Reviews.RDCapitalizationRatio},
		{"asset.reviews.capitalized_software_ratio", c.Asset.Reviews.CapitalizedSoftwareRatio},
	}
	for _, r := range ratios {
		if r.val == nil {
			continue // absent key → use default, not an error
		}
		if *r.val <= 0 || *r.val > 1 {
			return fmt.Errorf("%s must be in (0, 1], got %v", r.name, *r.val)
		}
	}

	return nil
}
