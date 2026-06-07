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
//	  - OperatingLeaseRightOfUseAsset (A6 ROU asset; asset-side mirror of the lease)
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
//
// CURRENCY PRE-CLEANER INVARIANT — DO NOT MIGRATE TO CleanedFinancialData VIEWS.
//
// DC-1 Phase 4 (C-5, spec §4.2.10): this is the ONE consumer migration-map row
// Phase 4 deliberately does NOT migrate. FX conversion runs PRE-cleaner — the
// SEC parser stamps ReportingCurrency, convertFinancialsToUSD mutates every
// monetary field IN PLACE on the raw *FinancialData, and ONLY THEN does
// CleanFinancialData run. By the time a *cleaneddata.CleanedFinancialData wrapper
// exists, the underlying entity is already USD-denominated, so all three views
// (AsReported / Restated / InvestedCapital) inherit USD automatically. Reading
// or writing FX through a view here would be both impossible (no view exists at
// this pipeline stage) and wrong (it would double-convert or skip fields the
// view set doesn't carry). The mutation site stays at fd.X *= rate.

package valuation

import (
	"context"
	"fmt"
	"math"
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
	// A6 (TDB-2): right-of-use asset is a monetary balance-sheet asset; FX-convert
	// it so FPI tickers (TSM, ASML, …) get a USD ROU value for the A6 overlay.
	fd.OperatingLeaseRightOfUseAsset *= rate

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

// applyADRRatio rewrites SharesOutstanding and DilutedSharesOutstanding on
// every period from "ordinary shares" to "ADR-equivalent shares" by dividing
// by the configured ratio. No-op when ratio is 1, when ticker is absent from
// config/adr_ratios.json, or when s.adrRatios is nil. Per-share monetary
// fields (e.g., DividendsPerShare) are NOT divided again because Yahoo
// Finance reports them per-ADR for foreign filers — dividing here would
// double-correct.
//
// Calculation-safety contract — applies to ONLY two fields:
//
//	DIVIDED:
//	  - SharesOutstanding
//	  - DilutedSharesOutstanding
//	NOT touched (already per-ADR or dimensionless / monetary handled by B9):
//	  - everything else on FinancialData
//
// Single-call-only contract: this function does NOT mark the data — calling
// it twice on the same *HistoricalFinancialData yields garbage (5.19B/25,
// not 5.19B). The caller (CalculateValuation) is responsible for invoking
// exactly once. We deliberately do NOT add an "applied" sentinel because
// that would pollute the FinancialData entity with valuation-pipeline state.
//
// Sanity cross-check: when marketData carries Yahoo's reported
// sharesOutstanding (which is already ADR-equivalent for foreign filers), a
// drift of more than 10% between (SECshares / ratio) and yfShares is logged
// at WARN. Drift usually means the configured ADR ratio is stale (depositary
// banks occasionally restructure ratios — TSM has been 5:1 since IPO but
// BABA went 1:1 → 8:1 after a 2024 corporate action). The warning is
// non-blocking: B10 still applies the divide because the user explicitly
// chose the configured ratio over Yahoo's count by maintaining
// config/adr_ratios.json.
//
// Phase B10 of docs/refactoring/archive/ifrs-foreign-private-issuer-support-spec.md.
func (s *Service) applyADRRatio(ctx context.Context, ticker string, hist *entities.HistoricalFinancialData, marketData *entities.MarketData) {
	// Defensive guards: nothing to do for nil/empty inputs. Each is its own
	// branch so a future debugger can step through and see exactly which
	// guard short-circuited.
	if hist == nil || len(hist.Data) == 0 {
		return
	}

	// adrRatios.Get is nil-safe by contract (B8), so we can call it without
	// a nil check on the receiver. Unknown tickers and nil receivers both
	// resolve to ratio=1 which short-circuits the no-op path below.
	ratio := s.adrRatios.Get(ticker)
	if ratio == 1 {
		// Domestic 10-K filers must produce identical pre-B10 results. No
		// narrate emit either — keeps the stream quiet for the common case.
		return
	}

	// Apply the divide in place across all periods. Per-share monetary fields
	// like DividendsPerShare are deliberately NOT divided here: for foreign
	// filers, Yahoo reports DPS already per-ADR, so dividing would
	// double-correct. (FX-converted SEC values are dimensionally per-ordinary-
	// share-currency-unit; that's a known asymmetry tracked for Phase B11+.)
	ratioF := float64(ratio)
	periodsAdjusted := 0
	for _, fd := range hist.Data {
		if fd == nil {
			continue
		}
		fd.SharesOutstanding /= ratioF
		fd.DilutedSharesOutstanding /= ratioF
		periodsAdjusted++
	}

	// Cross-check against Yahoo's reported sharesOutstanding. Yahoo's number
	// is already ADR-equivalent for foreign filers, so it should agree with
	// our post-divide result. A > 10% gap usually means the configured ratio
	// is stale; we WARN but don't fail because the user explicitly chose the
	// configured ratio.
	if marketData != nil && marketData.SharesOutstanding > 0 {
		latest, _ := hist.GetLatestPeriod()
		if latest != nil {
			expected := latest.SharesOutstanding // already post-divide
			observed := marketData.SharesOutstanding
			deviation := math.Abs(expected-observed) / observed
			if deviation > 0.10 {
				s.log(ctx).Warn("ADR ratio deviation > 10% — config may be stale",
					zap.String("ticker", ticker),
					zap.Int("configured_ratio", ratio),
					zap.Float64("expected_post_divide_shares", expected),
					zap.Float64("yahoo_reported_shares", observed),
					zap.Float64("deviation_pct", deviation*100),
				)
			}
		}
	}

	// Tier-1 narrate emission. Skipped for the no-op path (ratio=1 returned
	// earlier) so domestic filers don't pollute the stream.
	em := narrate.From(ctx)
	em.Emit(ctx, narrate.PhaseADRRatioApplied, narrate.OutcomeOK, "",
		zap.String("ticker", ticker),
		zap.Int("ratio", ratio),
		zap.Int("periods_adjusted", periodsAdjusted),
	)
}
