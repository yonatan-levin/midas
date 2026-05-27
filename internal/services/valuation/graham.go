package valuation

import (
	"context"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/cleaneddata"
)

// grahamFloor holds the Graham-school per-share diagnostics returned by
// calculateGrahamFloorMetrics. All four pointer fields are nil when
// TotalLiabilities cannot be resolved (Warnings is populated instead);
// non-nil when resolved. The pointer types preserve two distinct signals:
//
//   - unresolved: all four nil (caller surfaces the warning, JSON omits all four)
//   - resolved:   the first three populated (possibly to negative or &0.0
//     values) and GrahamDiscountPct populated only when the floor and price
//     are both > 0
//
// See docs/refactoring/archive/graham-floor-metrics-spec.md and
// docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md
// for the routing decision that led to direct-XBRL preference over derived
// total liabilities.
type grahamFloor struct {
	CurrentAssetsPerShare *float64
	NCAVPerShare          *float64
	GrahamFloorPerShare   *float64
	GrahamDiscountPct     *float64
	Warnings              []string
}

// calculateGrahamFloorMetrics computes the four Graham-school diagnostic
// per-share values from balance-sheet inputs. The only side effect is a
// WARN log on the derivation fallback path so operators can correlate
// warnings against the cleaner asymmetry tracked in DC-1. The function
// does NOT mutate the view.
//
// DC-1 Phase 4 (C-2/C-5): reads the AsReported() view. NCAV is intentionally
// a conservative as-filed metric (Graham's "two-thirds of NCAV" defensive
// buy floor) — using Restated() would shift the floor upward for companies
// with goodwill/inventory writedowns, defeating Graham's intent (§4.2.9).
// AsReported is also IMMUNE to the Phase 4 §8.2.1 Option A transitional state
// where the post-clean entity's umbrella fields (TotalAssets/TotalLiabilities)
// become incoherent after dispatcher umbrella dual-writes are deleted — it
// reads the pre-clean snapshot, so the derivation fallback below sees
// parser-stamped values regardless of which dual-write cluster has landed.
// This is why Graham migrates in C-2 (the first cluster that makes umbrellas
// incoherent) rather than waiting for C-5.
func calculateGrahamFloorMetrics(
	ctx context.Context,
	logger *zap.Logger,
	ticker string,
	view *cleaneddata.FinancialDataView,
	dilutedShares float64,
	currentPrice float64,
) grahamFloor {
	out := grahamFloor{}

	if view == nil || dilutedShares <= 0 {
		return out
	}

	totalLiabilities, ok := resolveTotalLiabilities(ctx, logger, ticker, view)
	if !ok {
		out.Warnings = []string{
			"graham_floor: insufficient balance-sheet data (total_liabilities unresolved)",
		}
		return out
	}

	caps := view.CurrentAssets / dilutedShares
	out.CurrentAssetsPerShare = &caps

	ncav := (view.CurrentAssets - totalLiabilities) / dilutedShares
	out.NCAVPerShare = &ncav

	// Graham's "buy below" trigger: 2/3 of NCAV. Citation: Benjamin Graham,
	// Security Analysis (1934), "net-net" stocks chapter. Clamp to 0 when
	// NCAV is negative — that case represents "no asset floor exists".
	floor := ncav * (2.0 / 3.0)
	if floor < 0 {
		floor = 0
	}
	out.GrahamFloorPerShare = &floor

	if floor > 0 && currentPrice > 0 {
		d := (currentPrice - floor) / floor
		out.GrahamDiscountPct = &d
	}

	return out
}

// resolveTotalLiabilities returns the as-reported total-liabilities figure
// for fd. Routing per spec §4.4:
//  1. Direct — fd.TotalLiabilities populated by the SEC parser from
//     us-gaap:Liabilities or ifrs-full:Liabilities.
//  2. Derived — TotalAssets - StockholdersEquity, with a WARN log so
//     operators know the cleaner asymmetry (DC-1) may have distorted the
//     value. Only used when both inputs are positive AND the result is
//     positive.
//  3. Unresolved — return (0, false) so the caller emits the documented
//     warning string and the four diagnostic fields drop from the response.
//
// The derivation path is a fallback because cleaner adjusters mutate
// TotalAssets without applying the offsetting equity hit; for tickers
// where the umbrella XBRL tag is absent (rare in practice), the derived
// value can be wrong. The warning makes that case auditable instead of
// silent.
func resolveTotalLiabilities(
	ctx context.Context,
	logger *zap.Logger,
	ticker string,
	view *cleaneddata.FinancialDataView,
) (float64, bool) {
	if view.TotalLiabilities > 0 {
		return view.TotalLiabilities, true
	}

	if view.TotalAssets > 0 && view.StockholdersEquity > 0 {
		derived := view.TotalAssets - view.StockholdersEquity
		if derived > 0 {
			logctx.Or(ctx, logger).Warn("graham_floor: derived total_liabilities from balance-sheet identity",
				zap.String("ticker", ticker),
				zap.Float64("total_assets", view.TotalAssets),
				zap.Float64("stockholders_equity", view.StockholdersEquity),
				zap.Float64("derived_total_liabilities", derived),
			)
			return derived, true
		}
	}

	return 0, false
}
