package config

import "testing"

// TestGuidanceRoot_DefaultsToEmpty pins the Layer-B Phase-2 production default:
// valuation.guidance_root is empty unless explicitly set, so the guidance loader
// is DISABLED by default ⇒ absent path ⇒ byte-identical to the 4.7 engine (NF1).
func TestGuidanceRoot_DefaultsToEmpty(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.Valuation.GuidanceRoot != "" {
		t.Fatalf("expected valuation.guidance_root to default to empty (guidance disabled), got %q",
			cfg.Valuation.GuidanceRoot)
	}
}

// TestGuidanceRoot_EnvOverride confirms the field is wired to the
// VALUATION_GUIDANCE_ROOT env key (the standard nested-key replacer), so Phase 3
// can flip the root without a code change.
func TestGuidanceRoot_EnvOverride(t *testing.T) {
	t.Setenv("VALUATION_GUIDANCE_ROOT", "testdata/guidance")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.Valuation.GuidanceRoot != "testdata/guidance" {
		t.Fatalf("expected valuation.guidance_root override, got %q", cfg.Valuation.GuidanceRoot)
	}
}
