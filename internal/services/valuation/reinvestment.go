package valuation

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
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
