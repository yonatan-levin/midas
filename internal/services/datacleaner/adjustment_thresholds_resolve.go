package datacleaner

import (
	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/adjustments"
)

// ResolveAssetThresholds merges the parsed (pointer-field) threshold config over
// the adjuster's defaults, overwriting a field ONLY when its config pointer is
// non-nil. A nil cfg (the absent-file path) returns the defaults unchanged.
//
// This lives in the datacleaner package — which already imports both config and
// adjustments — to avoid a config→adjustments import edge (adjustments
// transitively imports config, so that edge would close a cycle). The adjuster
// package owns the default table; this resolver only layers operator overrides
// on top.
func ResolveAssetThresholds(def adjustments.AssetThresholds, cfg *config.AdjustmentThresholdsConfig) adjustments.AssetThresholds {
	out := def
	if cfg == nil {
		return out
	}

	a := cfg.Asset
	if v := a.A1Goodwill.MaterialityRatio; v != nil {
		out.GoodwillMateriality = *v
	}
	if v := a.A1Goodwill.SignificanceFlagRatio; v != nil {
		out.GoodwillSignificanceFlag = *v
	}
	if v := a.A2Intangible.MaterialityRatio; v != nil {
		out.IntangibleMateriality = *v
	}
	if v := a.A4DTA.MaterialityRatio; v != nil {
		out.DTAMateriality = *v
	}
	if v := a.A4DTA.SignificanceFlagRatio; v != nil {
		out.DTASignificanceFlag = *v
	}
	if v := a.A6RightOfUse.MaterialityRatio; v != nil {
		out.ROUMateriality = *v
	}
	if v := a.A6RightOfUse.SignificanceFlagRatio; v != nil {
		out.ROUSignificanceFlag = *v
	}
	if v := a.Reviews.RDCapitalizationRatio; v != nil {
		out.RDCapitalizationReview = *v
	}
	if v := a.Reviews.CapitalizedSoftwareRatio; v != nil {
		out.CapitalizedSoftwareReview = *v
	}

	return out
}
