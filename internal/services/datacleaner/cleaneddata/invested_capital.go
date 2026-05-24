package cleaneddata

import "github.com/midas/dcf-valuation-api/internal/core/entities"

// InvestedCapital returns the analytical view that applies OverlaySpec
// entries on top of Restated:
//
//   - B1 lease capitalization      (Field:"TotalDebt")       → DebtLikeClaims += Amount
//   - B2 pension underfunding      (Field:"TotalDebt")       → DebtLikeClaims += Amount
//   - B3 contingent liabilities    (Field:"DebtLikeClaims")  → DebtLikeClaims += Amount  (Phase 4 routing intent realized here)
//   - A1 goodwill exclusion        (Field:"TotalAssets")     → TotalAssets -= Amount; Goodwill = 0 (Damodaran convention)
//
// AmountSemantics governs the operator: Incremental adds on top of the
// current value (default for all current overlays); Replacement overwrites;
// Delta is treated as incremental for additive fields. Phase 3 only sees
// Incremental in practice.
//
// CRITICAL: result.TotalDebt stays UNCHANGED from Restated(). In Phase 4
// the WACC consumer will read InvestedCapital.TotalDebt for the capital-
// structure denominator and InvestedCapital.DebtLikeClaims separately for
// the EV→Equity bridge. The two numbers MUST NEVER collapse.
//
// First-call cost: O(adjusters + fields). Subsequent calls: O(1) cached.
func (c *CleanedFinancialData) InvestedCapital() *FinancialDataView {
	if c == nil {
		return zeroView(InvestedCapitalView)
	}
	if c.investedCap != nil {
		return c.investedCap
	}

	// Start from Restated. Restated() handles its own memoization; we copy
	// the value so InvestedCapital's mutations stay confined to its own
	// memoized view.
	base := c.Restated()
	v := *base
	v.ViewKind = InvestedCapitalView

	if c.raw != nil {
		for _, o := range c.raw.Overlays {
			applyOverlayToView(&v, o)
		}
	}

	c.investedCap = &v
	return c.investedCap
}

// applyOverlayToView routes one OverlaySpec to the field it adjusts.
// Unknown Field values are silently skipped (fail-soft); the switch
// covers the Phase 2 emitter set (A1 + B1/B2/B3) and Phase 3 does not
// add new overlays.
func applyOverlayToView(v *FinancialDataView, o entities.OverlaySpec) {
	// Replacement semantics overwrite the target. Phase 3 sees this only
	// in tests, but the branching is encoded so future analytical
	// overlays can declare "set field to amount" without bypassing the
	// view accessor.
	if o.AmountSemantics == entities.AmountReplacement {
		applyReplacement(v, o)
		return
	}

	// Incremental and Delta are treated identically (delta == incremental
	// for additive monetary fields). Operation drives sign: "subtract"
	// negates the amount; everything else adds.
	signed := o.Amount
	if o.Operation == "subtract" {
		signed = -signed
	}

	switch o.Field {
	case "TotalDebt":
		// B1 + B2: semantically DebtLikeClaims contributors today. The
		// OverlaySpec.Field name reflects the legacy dual-write target
		// (data.TotalDebt); Phase 4 may rename, but Phase 3 reads the
		// existing field name to avoid churn.
		v.DebtLikeClaims += signed
	case "DebtLikeClaims":
		// B3 (Phase 2 routing intent realized here in Phase 3).
		v.DebtLikeClaims += signed
	case "TotalAssets":
		// A1 goodwill exclusion (Damodaran convention).
		v.TotalAssets += signed
		v.Goodwill = 0
		v.TangibleAssets = v.TotalAssets - v.OtherIntangibles
		// default: silently skip; future overlays added before the view
		// is updated fall through here.
	}
}

func applyReplacement(v *FinancialDataView, o entities.OverlaySpec) {
	switch o.Field {
	case "TotalDebt":
		v.DebtLikeClaims = o.Amount
	case "DebtLikeClaims":
		v.DebtLikeClaims = o.Amount
	case "TotalAssets":
		v.TotalAssets = o.Amount
		v.Goodwill = 0
		v.TangibleAssets = v.TotalAssets - v.OtherIntangibles
	}
}
