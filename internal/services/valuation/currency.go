// Package valuation — currency.go
//
// FX-converts FinancialData fields from FinancialData.ReportingCurrency to
// USD, in place on a *HistoricalFinancialData. Called by CalculateValuation
// after data fetch / before WACC, growth, and DCF math run, so every
// downstream calculation sees USD-denominated values.
//
// Calculation-safety contract — split between MONETARY and DIMENSIONLESS:
//
//	MONETARY (must be FX-multiplied):
//	  - Revenue, OperatingIncome, NormalizedOperatingIncome, NetIncome
//	  - InterestExpense
//	  - TotalAssets, TangibleAssets, CurrentAssets, CurrentLiabilities
//	  - Goodwill, OtherIntangibles, IntangibleAssets, IndefiniteLivedIntangibles
//	  - TotalDebt, InterestBearingDebt
//	  - CashAndCashEquivalents
//	  - StockholdersEquity, MinorityInterest, PreferredEquity
//	  - DepreciationAndAmortization, CapitalExpenditures, OperatingCashFlow
//	  - Inventory, DeadInventoryWritedown
//	  - DeferredTaxAssets, ValuationAllowance
//	  - OperatingLeaseLiability, OperatingLeaseLiabilityCurrent,
//	    OperatingLeaseLiabilityNoncurrent
//	  - PensionLiabilities, OPEBLiability, PensionPlanAssets,
//	    ProjectedBenefitObligation
//	  - ContingentLiabilities, EnvironmentalLiabilities, LitigationLiabilities
//	  - GainOnPropertySales, RestructuringCharges, AssetSaleGains,
//	    LitigationSettlements, StockBasedCompensation, DerivativeGainsLosses,
//	    CapitalizedInterest, WorkingCapitalAdjustment
//	  - CostOfGoodsSold, ResearchAndDevelopment
//	  - DividendsPerShare (per-share but currency-denominated)
//
//	DIMENSIONLESS (must NOT be FX-multiplied):
//	  - SharesOutstanding, DilutedSharesOutstanding (units = shares)
//	  - TaxRate, EffectiveTaxRate (ratios)
//	  - InventoryTurnover (ratio)
//	  - IncrementalBorrowingRate, RiskFreeRate (ratios)
//	  - HasNormalizedData, MissingFields, FilingPeriod, FilingDate, AsOf,
//	    Period, Ticker, IndustryCode, CIK (metadata)
//	  - OperatingLeaseCommitments map[string]float64 — the keys are years
//	    but the values ARE monetary and DO need conversion.
//
// If the project adds a new field on FinancialData, the contributor MUST
// classify it here. Failing to do so means the new field stays in the
// reporting currency while everything around it is USD — a silent bug.

package valuation

import (
	"context"
	"fmt"
	"sort"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/observability/narrate"
)

// SetMacroGateway injects the macro data gateway used for FX rate lookups.
// Optional — when nil, convertFinancialsToUSD treats every period as already
// USD (no-op) so non-IFRS pipelines (and tests that don't exercise FX) keep
// working unchanged.
//
// Mirrors the SetYFinanceGateway pattern: post-construction injection so the
// existing dozens of NewService(...) call sites in tests don't have to plumb
// a new required parameter.
func (s *Service) SetMacroGateway(gw ports.MacroDataGateway) {
	s.macroGateway = gw
}

// convertFinancialsToUSD walks every period on hist and rewrites monetary
// fields from each period's ReportingCurrency to USD. Idempotent for USD-
// denominated periods (rate=1 short-circuits to a no-op). Stamps each
// converted period with ReportingCurrency="USD" so downstream re-conversion
// is also a no-op.
//
// Behavior under FX failure:
//   - If GetFXRate fails for a given period's currency, that period is left
//     unchanged (NOT zeroed) and an error wrapping ports.ErrFXRateUnavailable
//     is returned. The caller (CalculateValuation) decides whether to abort
//     the whole valuation or proceed with the surviving USD periods.
//   - If hist contains a mix of USD and non-USD periods AND only the non-USD
//     ones fail to convert, the USD periods remain untouched and we still
//     return an error — the caller cannot safely mix converted and
//     unconverted data.
//
// FX rates are looked up once per distinct currency, not once per period.
// For TSM with 10 periods of TWD data, this means a single GetFXRate call.
func (s *Service) convertFinancialsToUSD(ctx context.Context, hist *entities.HistoricalFinancialData) error {
	em := narrate.From(ctx)

	// Defensive guards: nothing to do for nil/empty inputs. Also no-op when no
	// macro gateway is wired (legacy / non-IFRS pipelines) — treats every
	// period as already-USD, the historical default before Phase B5.
	if hist == nil || len(hist.Data) == 0 {
		return nil
	}
	if s.macroGateway == nil {
		return nil
	}

	// Pass 1 — collect distinct non-USD currencies. Empty string is treated
	// as "USD" (legacy rows persisted before reporting_currency shipped in
	// Phase B5.0). This loop never mutates anything.
	currencySet := make(map[string]struct{})
	for _, fd := range hist.Data {
		if fd == nil {
			continue
		}
		ccy := fd.ReportingCurrency
		if ccy == "" || ccy == "USD" {
			continue
		}
		currencySet[ccy] = struct{}{}
	}

	// Fast path: nothing to convert.
	if len(currencySet) == 0 {
		return nil
	}

	// Pass 2 — fetch rates ONCE per distinct currency. Accumulate failures
	// instead of short-circuiting so the caller learns about every missing
	// pair in one trip; this avoids a slow trickle of one-error-at-a-time
	// retries when a config rebuild is needed.
	rates := make(map[string]float64, len(currencySet))
	failed := make(map[string]struct{})
	for ccy := range currencySet {
		rate, err := s.macroGateway.GetFXRate(ctx, ccy, "USD")
		if err != nil {
			failed[ccy] = struct{}{}
			continue
		}
		rates[ccy] = rate
	}

	// Pass 3 — apply rates in place. Periods whose currency failed lookup
	// are skipped (left unchanged) so the caller can still inspect the
	// original values.
	periodsConverted := 0
	for _, fd := range hist.Data {
		if fd == nil {
			continue
		}
		ccy := fd.ReportingCurrency
		if ccy == "" || ccy == "USD" {
			continue
		}
		rate, ok := rates[ccy]
		if !ok {
			// Lookup failed earlier; leave this period as-is.
			continue
		}
		applyFXRate(fd, rate)
		fd.ReportingCurrency = "USD"
		periodsConverted++
	}

	// Stable, sorted lists for log fields so the narrate stream is
	// deterministic across runs (helpful for test pinning + log dedup).
	convertedCcys := sortedKeysFloat(rates)
	failedCcys := sortedKeys(failed)

	if len(failed) > 0 {
		em.Emit(ctx, narrate.PhaseFXConvert, narrate.OutcomeError, "FX rate unavailable",
			zap.Strings("currencies_failed", failedCcys),
			zap.Strings("currencies_converted", convertedCcys),
			zap.Int("periods_converted", periodsConverted),
		)
		return fmt.Errorf("FX conversion failed for currencies %v: %w", failedCcys, ports.ErrFXRateUnavailable)
	}

	em.Emit(ctx, narrate.PhaseFXConvert, narrate.OutcomeOK, "",
		zap.Strings("currencies_converted", convertedCcys),
		zap.Int("periods_converted", periodsConverted),
	)
	return nil
}

// applyFXRate multiplies every monetary field on fd by rate. The list of
// fields below is the calculation-safety contract — every monetary field
// MUST appear here, every dimensionless field MUST be omitted. See the
// package-level doc-comment for the canonical classification table.
func applyFXRate(fd *entities.FinancialData, rate float64) {
	// Income statement
	fd.Revenue *= rate
	fd.OperatingIncome *= rate
	fd.NormalizedOperatingIncome *= rate
	fd.NetIncome *= rate
	fd.InterestExpense *= rate
	fd.CostOfGoodsSold *= rate
	fd.ResearchAndDevelopment *= rate

	// Earnings normalization (Category C)
	fd.RestructuringCharges *= rate
	fd.AssetSaleGains *= rate
	fd.LitigationSettlements *= rate
	fd.StockBasedCompensation *= rate
	fd.DerivativeGainsLosses *= rate
	fd.CapitalizedInterest *= rate
	fd.WorkingCapitalAdjustment *= rate
	fd.GainOnPropertySales *= rate

	// Balance sheet — assets
	fd.TotalAssets *= rate
	fd.TangibleAssets *= rate
	fd.CurrentAssets *= rate
	fd.Goodwill *= rate
	fd.OtherIntangibles *= rate
	fd.IntangibleAssets *= rate
	fd.IndefiniteLivedIntangibles *= rate
	fd.DeferredTaxAssets *= rate
	fd.ValuationAllowance *= rate
	fd.Inventory *= rate
	fd.DeadInventoryWritedown *= rate
	fd.CashAndCashEquivalents *= rate

	// Balance sheet — liabilities & equity
	fd.CurrentLiabilities *= rate
	fd.TotalDebt *= rate
	fd.InterestBearingDebt *= rate
	fd.StockholdersEquity *= rate
	fd.MinorityInterest *= rate
	fd.PreferredEquity *= rate

	// Liability completeness (Category B)
	fd.OperatingLeaseLiability *= rate
	fd.OperatingLeaseLiabilityCurrent *= rate
	fd.OperatingLeaseLiabilityNoncurrent *= rate
	fd.PensionLiabilities *= rate
	fd.OPEBLiability *= rate
	fd.PensionPlanAssets *= rate
	fd.ProjectedBenefitObligation *= rate
	fd.ContingentLiabilities *= rate
	fd.EnvironmentalLiabilities *= rate
	fd.LitigationLiabilities *= rate

	// Cash flow statement
	fd.DepreciationAndAmortization *= rate
	fd.CapitalExpenditures *= rate
	fd.OperatingCashFlow *= rate

	// Per-share monetary fields. DividendsPerShare is per-share but the unit
	// is currency, so it gets FX-multiplied. SharesOutstanding /
	// DilutedSharesOutstanding are dimensionless (units=shares) and are
	// deliberately NOT in this list.
	fd.DividendsPerShare *= rate

	// OperatingLeaseCommitments: keys are year strings (metadata), values
	// are monetary. Multiply values, leave keys.
	if len(fd.OperatingLeaseCommitments) > 0 {
		for k, v := range fd.OperatingLeaseCommitments {
			fd.OperatingLeaseCommitments[k] = v * rate
		}
	}
}

// hasNonUSDPeriod returns true when at least one period on hist carries a
// ReportingCurrency that is neither "" (legacy/USD) nor "USD". Used by
// CalculateValuation to decide whether an FX-conversion failure should be
// classified as ErrForeignPrivateIssuer (the only failures were on non-USD
// data, so this is an FPI without FX coverage) or whether to proceed with
// the surviving USD-denominated periods.
func hasNonUSDPeriod(hist *entities.HistoricalFinancialData) bool {
	if hist == nil {
		return false
	}
	for _, fd := range hist.Data {
		if fd == nil {
			continue
		}
		if fd.ReportingCurrency != "" && fd.ReportingCurrency != "USD" {
			return true
		}
	}
	return false
}

// sortedKeys returns the keys of a string-keyed set in stable lexicographic
// order. Local helper to keep narrate logs deterministic without adding a
// dependency on golang.org/x/exp/maps.
func sortedKeys(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedKeysFloat returns the keys of a string→float map in stable lexicographic
// order. Used for the converted-currencies field on the narrate emit so
// callers can read the rate map directly without a separate set conversion.
func sortedKeysFloat(m map[string]float64) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
