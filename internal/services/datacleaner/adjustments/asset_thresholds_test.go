package adjustments

import (
	"context"
	"testing"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestDefaultAssetThresholds_EqualLegacyConstants is the LOAD-BEARING regression
// pin for TDB-5: DefaultAssetThresholds() MUST return the exact pre-TDB-5
// hardcoded constants. If a future edit drifts a default, this fails CI and the
// "absent config => byte-identical behaviour" guarantee is broken.
func TestDefaultAssetThresholds_EqualLegacyConstants(t *testing.T) {
	got := DefaultAssetThresholds()

	cases := []struct {
		name string
		got  float64
		want float64
	}{
		{"GoodwillMateriality (A1, assets.go:354)", got.GoodwillMateriality, 0.05},
		{"GoodwillSignificanceFlag (A1, assets.go:400)", got.GoodwillSignificanceFlag, 0.10},
		{"IntangibleMateriality (A2, assets.go:753)", got.IntangibleMateriality, 0.02},
		{"DTAMateriality (A4, assets.go:892)", got.DTAMateriality, 0.05},
		{"DTASignificanceFlag (A4, assets.go:932)", got.DTASignificanceFlag, 0.10},
		{"ROUMateriality (A6, assets.go:487)", got.ROUMateriality, 0.05},
		{"ROUSignificanceFlag (A6, assets.go:534)", got.ROUSignificanceFlag, 0.10},
		{"RDCapitalizationReview (A-RD, assets.go:1140)", got.RDCapitalizationReview, 0.10},
		{"CapitalizedSoftwareReview (A-SW, assets.go:1250)", got.CapitalizedSoftwareReview, 0.015},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

// TestNewAssetAdjuster_UsesDefaultThresholds proves the zero-arg constructor
// (used by ~40 existing call sites + pipeline.go) yields the defaults.
func TestNewAssetAdjuster_UsesDefaultThresholds(t *testing.T) {
	aa := NewAssetAdjuster()
	if aa.thresholds != DefaultAssetThresholds() {
		t.Errorf("NewAssetAdjuster() thresholds = %+v, want defaults %+v", aa.thresholds, DefaultAssetThresholds())
	}
}

// buildA1Fixture constructs a minimal FinancialData + rule that exercises the A1
// goodwill materiality gate at a chosen goodwill/assets ratio.
func buildA1GoodwillFixture(goodwillRatio float64) (*entities.FinancialData, *entities.CleaningRule) {
	const totalAssets = 1_000_000.0
	data := &entities.FinancialData{
		TotalAssets: totalAssets,
		Goodwill:    goodwillRatio * totalAssets,
	}
	rule := &entities.CleaningRule{ID: "A1_goodwill_exclusion"}
	return data, rule
}

// TestAssetAdjuster_DefaultGate_Unchanged pins the A1 materiality boundary under
// the default constructor: ratio exactly at 0.05 skips (<=), 0.0501 fires. This
// proves no behaviour drift from the literal->field swap.
func TestAssetAdjuster_DefaultGate_Unchanged(t *testing.T) {
	aa := NewAssetAdjuster()
	ctx := context.Background()

	// Exactly at threshold (0.05) -> skip (gate is `<= threshold`).
	dataAt, ruleAt := buildA1GoodwillFixture(0.05)
	outAt, err := aa.ApplyA1Goodwill(ctx, dataAt, ruleAt, &entities.CleaningContext{})
	if err != nil {
		t.Fatalf("ApplyA1Goodwill at-threshold returned error: %v", err)
	}
	if firedA1(outAt) {
		t.Errorf("A1 at goodwill ratio 0.05 fired; expected skip (<= threshold)")
	}

	// Just above threshold (0.0501) -> fire.
	dataAbove, ruleAbove := buildA1GoodwillFixture(0.0501)
	outAbove, err := aa.ApplyA1Goodwill(ctx, dataAbove, ruleAbove, &entities.CleaningContext{})
	if err != nil {
		t.Fatalf("ApplyA1Goodwill above-threshold returned error: %v", err)
	}
	if !firedA1(outAbove) {
		t.Errorf("A1 at goodwill ratio 0.0501 skipped; expected fire (> threshold)")
	}
}

// TestAssetAdjuster_OverrideGate proves the injected thresholds flow to the gate:
// raising A1 materiality to 0.20 makes a 0.10-ratio goodwill (which fires under
// the default) now SKIP. This proves the config path is live end-to-end.
func TestAssetAdjuster_OverrideGate(t *testing.T) {
	ctx := context.Background()

	// Sanity: under defaults, a 0.10 ratio fires.
	defaultAA := NewAssetAdjuster()
	dataDefault, ruleDefault := buildA1GoodwillFixture(0.10)
	outDefault, err := defaultAA.ApplyA1Goodwill(ctx, dataDefault, ruleDefault, &entities.CleaningContext{})
	if err != nil {
		t.Fatalf("default ApplyA1Goodwill returned error: %v", err)
	}
	if !firedA1(outDefault) {
		t.Fatalf("precondition failed: A1 at ratio 0.10 should fire under default thresholds")
	}

	// Override: raise materiality to 0.20 (other fields stay default).
	thr := DefaultAssetThresholds()
	thr.GoodwillMateriality = 0.20
	overrideAA := NewAssetAdjusterWithThresholds(thr)
	dataOverride, ruleOverride := buildA1GoodwillFixture(0.10)
	outOverride, err := overrideAA.ApplyA1Goodwill(ctx, dataOverride, ruleOverride, &entities.CleaningContext{})
	if err != nil {
		t.Fatalf("override ApplyA1Goodwill returned error: %v", err)
	}
	if firedA1(outOverride) {
		t.Errorf("A1 at ratio 0.10 fired under raised materiality 0.20; expected skip — config path not live")
	}
}

// firedA1 reports whether the A1 AdjusterOutput represents a fired adjustment
// (an OverlaySpec was emitted), as opposed to a Fired:false skip LedgerEntry.
func firedA1(out AdjusterOutput) bool {
	return len(out.Overlays) > 0
}
