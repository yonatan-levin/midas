package configfs

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRead_IndustryMultiples asserts the embed.FS ships the full industry
// multiples config.
func TestRead_IndustryMultiples(t *testing.T) {
	b, err := Read("industry_multiples.json")
	if err != nil {
		t.Fatalf("Read(industry_multiples.json) error: %v", err)
	}
	if !strings.Contains(string(b), "reit_pffo_multiples") {
		t.Fatalf("industry_multiples.json missing expected key 'reit_pffo_multiples'")
	}
}

// TestRead_IndustryCodes asserts the datacleaner subtree is embedded too.
func TestRead_IndustryCodes(t *testing.T) {
	b, err := Read("datacleaner/industry_codes.json")
	if err != nil {
		t.Fatalf("Read(industry_codes.json) error: %v", err)
	}
	if !strings.Contains(string(b), "mappings") {
		t.Fatalf("industry_codes.json missing expected key 'mappings'")
	}
}

// TestRead_MissingFile confirms we return a non-nil error for bogus paths
// rather than empty bytes.
func TestRead_MissingFile(t *testing.T) {
	if _, err := Read("does/not/exist.json"); err == nil {
		t.Fatalf("expected error for missing file, got nil")
	}
}

// TestRead_DamodaranConfigs_EmbeddedAndParse verifies the two RM-2 Phase 2
// configs are embedded (readable via the embed.FS regardless of cwd) and parse
// into the expected shapes. A file missing from the //go:embed directive would
// fail Read here rather than silently at runtime in production/replay.
func TestRead_DamodaranConfigs_EmbeddedAndParse(t *testing.T) {
	t.Run("damodaran_sector_multiples non-empty and parses", func(t *testing.T) {
		data, err := Read("damodaran_sector_multiples.json")
		if err != nil {
			t.Fatalf("Read(damodaran_sector_multiples.json) error: %v", err)
		}
		if len(data) == 0 {
			t.Fatal("damodaran_sector_multiples.json is empty")
		}
		var cfg struct {
			DatasetDate string             `json:"dataset_date"`
			SourceURL   string             `json:"source_url"`
			Industries  map[string]float64 `json:"industries"`
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			t.Fatalf("parse damodaran_sector_multiples.json: %v", err)
		}
		if cfg.DatasetDate == "" {
			t.Error("dataset_date is empty")
		}
		if len(cfg.Industries) == 0 {
			t.Error("industries map is empty")
		}
	})

	t.Run("sic_to_damodaran non-empty and parses", func(t *testing.T) {
		data, err := Read("sic_to_damodaran.json")
		if err != nil {
			t.Fatalf("Read(sic_to_damodaran.json) error: %v", err)
		}
		if len(data) == 0 {
			t.Fatal("sic_to_damodaran.json is empty")
		}
		var cfg struct {
			Version string            `json:"version"`
			Map     map[string]string `json:"map"`
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			t.Fatalf("parse sic_to_damodaran.json: %v", err)
		}
		if len(cfg.Map) == 0 {
			t.Error("crosswalk map is empty")
		}
	})
}
