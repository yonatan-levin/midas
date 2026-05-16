package sec

import (
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// computePlugs fills the four "Other*" plug fields on fd as residuals between
// each balance-sheet umbrella and the sum of its known typed components.
//
// Invariant after this call (for every (umbrella, components, plug) triple):
//
//	umbrella == sum(known_components) + plug,  with plug >= 0
//
// When sum(known_components) > umbrella the raw residual is negative; we clamp
// to zero (preserving the >= 0 invariant) and emit a Debug log line so the
// data-quality anomaly is observable without polluting Warnings.
//
// computePlugs assumes fd's monetary fields are already currency-coherent —
// i.e., extractFiscalPeriods has already collapsed the per-currency value
// buckets via the dominant-currency resolution at parser.go:309-363. The
// caller in parsePeriodData satisfies this by construction.
//
// DC-1 Phase 0 — see docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md.
func computePlugs(fd *entities.FinancialData, logger *zap.Logger) {
	if fd == nil {
		return
	}

	// Plug 1: OtherCurrentAssets = CurrentAssets - (Cash + Inventory)
	currentAssetsComponents := fd.CashAndCashEquivalents + fd.Inventory
	fd.OtherCurrentAssets = clampPlug(
		"OtherCurrentAssets",
		fd.CurrentAssets,
		currentAssetsComponents,
		fd,
		logger,
	)

	// Plug 2: OtherNonCurrentAssets = (TotalAssets - CurrentAssets) - (Goodwill + Intangibles + DTA)
	// Note: DTA subtracts from the non-current plug only because the entity carries
	// only the gross/aggregate DeferredTaxAssets field — there is no current-portion
	// DTA today. Phase 1+ may split DTA if cleaner-side adjusters need it.
	nonCurrentAssetsUmbrella := fd.TotalAssets - fd.CurrentAssets
	if nonCurrentAssetsUmbrella < 0 {
		nonCurrentAssetsUmbrella = 0
	}
	nonCurrentAssetsComponents := fd.Goodwill + fd.OtherIntangibles + fd.DeferredTaxAssets
	fd.OtherNonCurrentAssets = clampPlug(
		"OtherNonCurrentAssets",
		nonCurrentAssetsUmbrella,
		nonCurrentAssetsComponents,
		fd,
		logger,
	)

	// Plug 3: OtherCurrentLiabilities = CurrentLiabilities - OperatingLeaseLiabilityCurrent
	currentLiabComponents := fd.OperatingLeaseLiabilityCurrent
	fd.OtherCurrentLiabilities = clampPlug(
		"OtherCurrentLiabilities",
		fd.CurrentLiabilities,
		currentLiabComponents,
		fd,
		logger,
	)

	// Plug 4: OtherNonCurrentLiabilities = (TotalLiabilities - CurrentLiabilities) - (TotalDebt + OpLeaseNoncurrent)
	// Note: TotalDebt aggregates current + noncurrent debt today (parser.go:728-753),
	// so subtracting all of it from non-current-liabilities slightly over-subtracts
	// for filers with material short-term debt. The max(0,…) clamp absorbs this;
	// Phase 1's recomputeUmbrellas shadow-mode will quantify the residual.
	nonCurrentLiabUmbrella := fd.TotalLiabilities - fd.CurrentLiabilities
	if nonCurrentLiabUmbrella < 0 {
		nonCurrentLiabUmbrella = 0
	}
	nonCurrentLiabComponents := fd.TotalDebt + fd.OperatingLeaseLiabilityNoncurrent
	fd.OtherNonCurrentLiabilities = clampPlug(
		"OtherNonCurrentLiabilities",
		nonCurrentLiabUmbrella,
		nonCurrentLiabComponents,
		fd,
		logger,
	)
}

// clampPlug returns max(0, umbrella - components), emitting a Debug log line
// (tagged with cik/period/plug_field/raw_residual) when the raw residual is
// negative. Logger may be nil — in that case the clamp still happens silently.
func clampPlug(plugField string, umbrella, components float64, fd *entities.FinancialData, logger *zap.Logger) float64 {
	raw := umbrella - components
	if raw >= 0 {
		return raw
	}
	if logger != nil {
		logger.Debug("plug residual clamped to zero",
			zap.String("cik", fd.CIK),
			zap.String("period", fd.FilingPeriod),
			zap.String("plug_field", plugField),
			zap.Float64("umbrella", umbrella),
			zap.Float64("components", components),
			zap.Float64("raw_residual", raw),
		)
	}
	return 0
}
