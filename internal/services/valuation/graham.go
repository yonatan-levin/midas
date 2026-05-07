package valuation

import (
	"context"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
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
// See docs/refactoring/graham-floor-metrics-spec.md and
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
// does NOT mutate fd.
func calculateGrahamFloorMetrics(
	ctx context.Context,
	logger *zap.Logger,
	ticker string,
	fd *entities.FinancialData,
	dilutedShares float64,
	currentPrice float64,
) grahamFloor {
	out := grahamFloor{}

	if fd == nil || dilutedShares <= 0 {
		return out
	}

	totalLiabilities, ok := resolveTotalLiabilities(ctx, logger, ticker, fd)
	if !ok {
		out.Warnings = []string{
			"graham_floor: insufficient balance-sheet data (total_liabilities unresolved)",
		}
		return out
	}

	caps := fd.CurrentAssets / dilutedShares
	out.CurrentAssetsPerShare = &caps

	ncav := (fd.CurrentAssets - totalLiabilities) / dilutedShares
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
	fd *entities.FinancialData,
) (float64, bool) {
	if fd.TotalLiabilities > 0 {
		return fd.TotalLiabilities, true
	}

	if fd.TotalAssets > 0 && fd.StockholdersEquity > 0 {
		derived := fd.TotalAssets - fd.StockholdersEquity
		if derived > 0 {
			logctx.Or(ctx, logger).Warn("graham_floor: derived total_liabilities from balance-sheet identity",
				zap.String("ticker", ticker),
				zap.Float64("total_assets", fd.TotalAssets),
				zap.Float64("stockholders_equity", fd.StockholdersEquity),
				zap.Float64("derived_total_liabilities", derived),
			)
			return derived, true
		}
	}

	return 0, false
}
