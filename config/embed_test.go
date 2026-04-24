package configfs

import (
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
