package adjustments

// AssetThresholds carries the resolved (already-defaulted) asset-adjuster
// materiality / review gate thresholds (TDB-5). Every field is a concrete
// float64 — the missing-vs-zero discrimination happens upstream in the config
// loader/resolver; by the time an AssetAdjuster holds an AssetThresholds, every
// gate has a value.
//
// The package owns its own default table (DefaultAssetThresholds) so the
// adjuster stays self-contained and unit tests need no config plumbing.
type AssetThresholds struct {
	// A1 goodwill (assets.go ApplyA1Goodwill).
	GoodwillMateriality      float64 // gate: goodwill/assets must exceed to fire
	GoodwillSignificanceFlag float64 // gate: emit the significance flag

	// A2 intangible writedown (assets.go ApplyA2Intangible).
	IntangibleMateriality float64 // gate

	// A4 deferred-tax-asset valuation allowance (assets.go ApplyA4DTAValuationAllowance).
	DTAMateriality      float64 // gate
	DTASignificanceFlag float64 // gate: emit the significance flag

	// A6 right-of-use exclusion (assets.go ApplyA6RightOfUseAssets).
	ROUMateriality      float64 // gate
	ROUSignificanceFlag float64 // gate: emit the significance flag

	// Review-only gates (no balance-sheet mutation — they raise informational flags).
	RDCapitalizationReview    float64 // A-RD R&D-capitalization review (assets.go ApplyARDCapitalizationReview)
	CapitalizedSoftwareReview float64 // A-SW capitalized-software review (assets.go ApplyACapitalizedSoftwareReview)
}

// DefaultAssetThresholds returns the pre-TDB-5 hardcoded constants. This is the
// single source of truth for "behaviour when config is absent": a pinned unit
// test (TestDefaultAssetThresholds_EqualLegacyConstants) asserts each field
// equals its documented legacy literal, so any drift fails CI and the
// byte-identical-until-override guarantee is preserved.
func DefaultAssetThresholds() AssetThresholds {
	return AssetThresholds{
		GoodwillMateriality:       0.05,
		GoodwillSignificanceFlag:  0.10,
		IntangibleMateriality:     0.02,
		DTAMateriality:            0.05,
		DTASignificanceFlag:       0.10,
		ROUMateriality:            0.05,
		ROUSignificanceFlag:       0.10,
		RDCapitalizationReview:    0.10,
		CapitalizedSoftwareReview: 0.015,
	}
}
