package valuation

import (
	"context"
	"fmt"
	"math"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// defaultMaxAnnualDilutionRate is the clamp ceiling applied to the derived
// historical dilution rate when a profile leaves MaxAnnualDilutionRate at 0.
// 8%/yr is already an aggressive sustained dilution pace for an SBC-heavy
// grower; capping protects the forward projection from a transient share-count
// spike (e.g. a single dilutive acquisition year) compounding implausibly.
const defaultMaxAnnualDilutionRate = 0.08

// deriveAnnualDilutionRate derives the historical annual diluted-share dilution
// rate as the share-count CAGR across the available annual (FY) periods.
//
// It is PURE and CLOCK-FREE (no Service receiver, no time.Now): it reads only
// the FY diluted-share counts and the StockBasedCompensation eligibility gate
// off HistoricalFinancialData, so it is replay-deterministic.
//
// rate = (sharesₙ / shares₀)^(1/years) − 1 over the oldest→newest FY periods.
//
// eligible is false (a NO-OP for the caller) when any of:
//   - fewer than 2 FY periods with positive diluted-share counts, OR
//   - no StockBasedCompensation anywhere in the FY series (the firm is not a
//     non-trivial SBC issuer — SBC is the eligibility gate, NOT the rate source), OR
//   - the derived rate is ≤ 0 (flat or buyback-driven share-count decline — the
//     adjustment must never INFLATE per-share value, only dilute it).
func deriveAnnualDilutionRate(hist *entities.HistoricalFinancialData) (rate float64, eligible bool) {
	if hist == nil {
		return 0, false
	}

	// Walk FY periods oldest→newest. GetSortedPeriods is ascending; filter to the
	// annual set so quarterly share counts don't distort the CAGR span.
	annual := hist.GetAnnualPeriods()
	var firstShares, lastShares float64
	var fyCount, yearsSpan int
	hasSBC := false
	for _, period := range hist.GetSortedPeriods() {
		data, ok := annual[period]
		if !ok || data == nil {
			continue
		}
		if data.StockBasedCompensation > 0 {
			hasSBC = true
		}
		if data.DilutedSharesOutstanding <= 0 {
			continue
		}
		if fyCount == 0 {
			firstShares = data.DilutedSharesOutstanding
		}
		lastShares = data.DilutedSharesOutstanding
		fyCount++
	}
	// yearsSpan is the number of compounding intervals between first and last
	// usable FY share counts (count − 1).
	yearsSpan = fyCount - 1

	if fyCount < 2 || yearsSpan < 1 || !hasSBC || firstShares <= 0 || lastShares <= 0 {
		return 0, false
	}

	rate = math.Pow(lastShares/firstShares, 1.0/float64(yearsSpan)) - 1
	if rate <= 0 {
		return 0, false
	}
	return rate, true
}

// applyDilutedShareForward projects the diluted share count forward over the
// resolved DCF horizon at the derived historical dilution rate, for high-SBC
// growth profiles that opt in. It mirrors the Layer-A reinvestment.go wiring
// pattern: profile-gated, DEFAULT-OFF, returns audit []string for the caller to
// append to result.Warnings, and emits a calc-trace stage.
//
// It is a strict NO-OP (returns currentShares, 0, nil) — leaving the DCF
// per-share denominator byte-identical to today — when ANY of three independent
// layers holds:
//  1. rp == nil (no profile resolved — test paths / registry not wired), OR
//  2. !rp.DilutedShareForwardEnabled (the default for every shipping profile), OR
//  3. deriveAnnualDilutionRate returns eligible=false (insufficient FY history,
//     no SBC, or a non-positive rate).
//
// When it fires, the derived rate is clamped to [0, cap] (cap =
// MaxAnnualDilutionRate, or defaultMaxAnnualDilutionRate when 0) and the forward
// count is currentShares × (1+rate)^horizon.
func (s *Service) applyDilutedShareForward(
	ctx context.Context,
	currentShares float64,
	rp *profile.ResolvedProfile,
	hist *entities.HistoricalFinancialData,
	horizon int,
) (forwardShares, appliedRate float64, audit []string) {
	if rp == nil || !rp.DilutedShareForwardEnabled {
		return currentShares, 0, nil // default-off: byte-identical denominator
	}

	rate, eligible := deriveAnnualDilutionRate(hist)
	if !eligible {
		return currentShares, 0, nil // ineligible history: no-op
	}

	rateCap := rp.MaxAnnualDilutionRate
	if rateCap <= 0 {
		rateCap = defaultMaxAnnualDilutionRate
	}
	if rate > rateCap {
		rate = rateCap
	}

	forwardShares = currentShares * math.Pow(1+rate, float64(horizon))

	s.log(ctx).Info("Using VAL-1 Phase 5 diluted-share-forward adjustment",
		zap.String("ticker", hist.Ticker),
		zap.String("profile_id", rp.ProfileID),
		zap.Float64("applied_dilution_rate", rate),
		zap.Float64("current_diluted_shares", currentShares),
		zap.Float64("forward_diluted_shares", forwardShares),
		zap.Int("horizon_years", horizon))

	if s.calcEmitter != nil {
		s.calcEmitter.Emit(ctx, "diluted_share_forward",
			zap.String("ticker", hist.Ticker),
			zap.Float64("applied_dilution_rate", rate),
			zap.Float64("current_diluted_shares", currentShares),
			zap.Float64("forward_diluted_shares", forwardShares),
			zap.Int("horizon_years", horizon),
		)
	}

	// Audit source line (mirrors the reinvestment_model: source-tag convention).
	audit = []string{fmt.Sprintf(
		"diluted_share_forward: rate=%.2f%%/yr horizon=%dy current_shares=%.0f forward_shares=%.0f",
		rate*100, horizon, currentShares, forwardShares)}
	return forwardShares, rate, audit
}
