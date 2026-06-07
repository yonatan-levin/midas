package valuation

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/authority"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
	"github.com/midas/dcf-valuation-api/pkg/finance/dcf"
)

// applyReinvestmentModel wires the Tier-2 Layer-A reinvestment / operating-leverage
// parameters from the resolved AssumptionProfile into the DCF inputs so projected
// FCF can cross from negative to positive WITHIN the explicit horizon for
// reinvestment-heavy, scaling firms (spec
// docs/refactoring/spec/dcf-reinvestment-and-filing-intelligence-spec.md §5-§7).
//
// It is a deliberate NO-OP — leaving the DCF on the bit-for-bit legacy
// proportional path — when:
//   - no profile resolved (test paths / registry not wired), OR
//   - the profile opts out (ReinvestmentMethod empty or legacy_proportional), OR
//   - the revenue base is unavailable (the reinvestment term projects off
//     revenue; without it the engine would have no series to grow).
//
// It returns audit lines (mirroring the RM-1 "revenue_base: …" / BUG-015
// "operating_income_base: …" source-tag convention) for the caller to append to
// result.Warnings, and emits a "reinvestment_model" calc-trace stage.
//
// MARGIN GUARD: the converged operating-margin target is clamped UP to the base
// margin (targetMargin = max(base, profileTarget)). The model may only EXPAND
// margins, never contract them — this protects already-high-margin firms (e.g.
// NVDA, whose realized margin exceeds a coarse archetype target) from a spurious
// margin contraction, while still letting low-margin scalers (AMD) expand.
func (s *Service) applyReinvestmentModel(
	ctx context.Context,
	in *dcf.Inputs,
	rp *profile.ResolvedProfile,
	baseOI float64,
	hist *entities.HistoricalFinancialData,
	growthRates []float64,
	anchors authority.NearTermAnchors,
) []string {
	if rp == nil ||
		rp.ReinvestmentMethod == "" ||
		rp.ReinvestmentMethod == profile.ReinvestmentLegacyProportional {
		return nil // legacy proportional path — bit-for-bit unchanged
	}

	ttmRevenue, revSource, _ := hist.TrailingTwelveMonthsRevenue()
	if ttmRevenue <= 0 || baseOI <= 0 {
		s.log(ctx).Warn("Layer-A reinvestment model requested but revenue/OI base unavailable; using legacy proportional FCF",
			zap.String("ticker", hist.Ticker),
			zap.String("profile_id", rp.ProfileID),
			zap.Float64("ttm_revenue", ttmRevenue),
			zap.Float64("base_oi", baseOI))
		return []string{fmt.Sprintf(
			"reinvestment_model: method=%s requested but revenue base unavailable; fell back to legacy proportional FCF",
			rp.ReinvestmentMethod)}
	}

	baseMargin := baseOI / ttmRevenue
	// Margin guard — only expand, never contract (see godoc above).
	targetMargin := rp.TargetOperatingMargin
	if targetMargin < baseMargin {
		targetMargin = baseMargin
	}

	in.ReinvestmentMethod = string(rp.ReinvestmentMethod)
	in.BaseRevenue = ttmRevenue
	in.RevenueGrowthRates = growthRates
	in.SalesToCapitalStart = rp.SalesToCapitalStart
	in.SalesToCapitalTarget = rp.SalesToCapitalTarget
	in.CapExIntensityStart = rp.CapExIntensityStart
	in.CapExIntensityMature = rp.CapExIntensityMature
	in.ReinvestmentFadeYears = rp.ReinvestmentFadeYears
	in.MaintenanceCapexFloor = rp.MaintenanceCapexFloor
	in.BaseOperatingMargin = baseMargin
	in.TargetOperatingMargin = targetMargin
	in.MarginConvergenceYears = rp.MarginConvergenceYears

	// Layer-B Phase-2 guidance anchors (spec Decision 6). Strict NO-OP when
	// anchors is empty — every branch below is gated on a non-nil anchor
	// pointer, so the reinvestment inputs stay byte-identical to Layer A (NF1).
	// The anchors touch ONLY year 1 (and optionally year 2); the §9.3
	// near-term-prefix invariant is asserted by applyNearTermAnchors.
	applyNearTermAnchors(in, anchors)

	s.log(ctx).Info("Using Layer-A reinvestment / operating-leverage DCF model",
		zap.String("ticker", hist.Ticker),
		zap.String("profile_id", rp.ProfileID),
		zap.String("method", string(rp.ReinvestmentMethod)),
		zap.String("revenue_source", revSource),
		zap.Float64("base_revenue", ttmRevenue),
		zap.Float64("base_margin", baseMargin),
		zap.Float64("target_margin", targetMargin),
		zap.Float64("sales_to_capital_start", rp.SalesToCapitalStart),
		zap.Float64("sales_to_capital_target", rp.SalesToCapitalTarget),
		zap.Int("reinvestment_fade_years", rp.ReinvestmentFadeYears))

	if s.calcEmitter != nil {
		s.calcEmitter.Emit(ctx, "reinvestment_model",
			zap.String("ticker", hist.Ticker),
			zap.String("method", string(rp.ReinvestmentMethod)),
			zap.Float64("base_revenue", ttmRevenue),
			zap.Float64("base_margin", baseMargin),
			zap.Float64("target_margin", targetMargin),
			zap.Float64("sales_to_capital_start", rp.SalesToCapitalStart),
			zap.Float64("sales_to_capital_target", rp.SalesToCapitalTarget),
			zap.Float64("maintenance_capex_floor", rp.MaintenanceCapexFloor),
			zap.Int("fade_years", rp.ReinvestmentFadeYears),
			zap.Int("margin_convergence_years", rp.MarginConvergenceYears),
		)
	}

	// Audit source line (mirrors RM-1 / BUG-015 source-tag convention).
	return []string{fmt.Sprintf(
		"reinvestment_model: method=%s base_margin=%.1f%% target_margin=%.1f%% sales_to_capital=%.2f→%.2f fade=%dy",
		rp.ReinvestmentMethod, baseMargin*100, targetMargin*100,
		rp.SalesToCapitalStart, rp.SalesToCapitalTarget, rp.ReinvestmentFadeYears)}
}

// applyNearTermAnchors injects the Layer-B Phase-2 guidance midpoint anchors
// into the already-populated reinvestment dcf.Inputs (spec Decision 6). It is a
// STRICT NO-OP when anchors is empty: every mutation is gated on a non-nil
// anchor pointer, so an absent-guidance valuation leaves `in` byte-identical to
// the Layer-A path (NF1 — the master invariant).
//
// Mechanic (each knob, year-1 and optionally year-2):
//   - RevenueGrowthYearN ⇒ overrides in.RevenueGrowthRates[N-1] (the engine reads
//     this slice per year). The slice is CLONED before the first write so the
//     caller's growth-rate slice (also stamped on result.GrowthRates) is never
//     mutated in place.
//   - OperatingMarginYear1 ⇒ seeds the year-1 margin start by setting
//     in.BaseOperatingMargin to the anchor; the convergence schedule then
//     proceeds from the anchored start toward TargetOperatingMargin (later years
//     still converge). OperatingMarginYear2 is not directly seedable on the
//     existing convergence curve without an engine change, so Phase 2 anchors
//     only the year-1 margin start (the year-2 margin pointer is reserved).
//   - CapExYearN (absolute USD) ⇒ in.NearTermReinvestmentOverride[N] replaces the
//     model-computed reinvestment for that year ONLY (the additive engine seam).
//
// §9.3 DOMINANCE GUARDRAIL (structural): anchors touch AT MOST year 1 and year 2.
// The function asserts the anchored year set is a strict near-term prefix —
// authority.RefuseAnchorIndex(idx) must be false for every anchored index. The
// NearTermAnchors type structurally cannot carry a year-3+ anchor (it has only
// Year1/Year2 fields), so this is a belt-and-suspenders panic that can only fire
// if the type grows an out-of-prefix field without updating the guardrail.
func applyNearTermAnchors(in *dcf.Inputs, anchors authority.NearTermAnchors) {
	if anchors.IsEmpty() {
		return // strict no-op — NF1 byte-identity
	}

	// Revenue-growth anchors mutate a CLONE of the growth slice (never the
	// caller's aliased slice). Clone lazily on the first growth anchor.
	growthCloned := false
	setGrowth := func(yearIndex int, v float64) {
		assertNearTermPrefix(yearIndex)
		if !growthCloned {
			cloned := make([]float64, len(in.RevenueGrowthRates))
			copy(cloned, in.RevenueGrowthRates)
			in.RevenueGrowthRates = cloned
			growthCloned = true
		}
		if yearIndex < len(in.RevenueGrowthRates) {
			in.RevenueGrowthRates[yearIndex] = v
		}
	}
	if anchors.RevenueGrowthYear1 != nil {
		setGrowth(0, *anchors.RevenueGrowthYear1)
	}
	if anchors.RevenueGrowthYear2 != nil {
		setGrowth(1, *anchors.RevenueGrowthYear2)
	}

	// Operating-margin year-1 anchor seeds the convergence start.
	if anchors.OperatingMarginYear1 != nil {
		assertNearTermPrefix(0)
		in.BaseOperatingMargin = *anchors.OperatingMarginYear1
		// Keep the only-expand margin guard coherent: never let the anchored
		// start exceed the target (which would invert the convergence). Raise
		// the target to the anchor so the year-1 margin is honored and later
		// years hold (rather than contract) — consistent with the §"margin
		// guard" only-expand contract.
		if in.TargetOperatingMargin < in.BaseOperatingMargin {
			in.TargetOperatingMargin = in.BaseOperatingMargin
		}
	}

	// CapEx absolute anchors become per-year reinvestment overrides (year 1–2).
	if anchors.CapExYear1 != nil {
		assertNearTermPrefix(0)
		ensureReinvestmentOverride(in)[1] = *anchors.CapExYear1
	}
	if anchors.CapExYear2 != nil {
		assertNearTermPrefix(1)
		ensureReinvestmentOverride(in)[2] = *anchors.CapExYear2
	}
}

// assertNearTermPrefix panics if yearIndex is outside the §9.3 near-term prefix
// (year 3+). The authority resolver already refuses to PRODUCE such an anchor;
// this is the post-anchor structural assertion at the consumption seam (spec
// §"§9.3 dominance guardrail enforced, not just asserted").
func assertNearTermPrefix(yearIndex int) {
	if authority.RefuseAnchorIndex(yearIndex) {
		panic(fmt.Sprintf(
			"guidance anchor at year index %d violates the §9.3 near-term-prefix guardrail (max index %d)",
			yearIndex, authority.MaxAnchorYearIndex))
	}
}

// ensureReinvestmentOverride lazily allocates the per-year reinvestment override
// map on the inputs and returns it.
func ensureReinvestmentOverride(in *dcf.Inputs) map[int]float64 {
	if in.NearTermReinvestmentOverride == nil {
		in.NearTermReinvestmentOverride = map[int]float64{}
	}
	return in.NearTermReinvestmentOverride
}
