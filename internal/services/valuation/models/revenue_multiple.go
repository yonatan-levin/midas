package models

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"go.uber.org/zap"

	configfs "github.com/midas/dcf-valuation-api/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// staleDataThresholdMonths defines when the consumer-side T7 staleness check
// fires: latest filing date >= 18 months in the past relative to the
// injected clock. Matches the threshold documented in spec RM-1 T7 and
// resolved via the RM-1.A consumer-layer placement (entity layer stays
// clock-free, replay determinism preserved via the existing Clock seam).
const staleDataThresholdMonths = 18

// DefaultEVRevenueMultiple is the fallback EV/Revenue multiple when no sector-specific
// multiple is configured.
const DefaultEVRevenueMultiple = 2.0

// RevenueMultipleModel implements a revenue-based valuation for pre-revenue or
// negative operating income companies.
//
// Enterprise Value = Revenue * Sector EV/Revenue Multiple
// Equity Value = EV - Debt + Cash
// Value per Share = Equity Value / Shares Outstanding
//
// This model is always flagged as low-confidence since it does not account for
// profitability or cash flow generation. It serves as a fallback when DCF is
// inapplicable due to negative operating income.
type RevenueMultipleModel struct {
	multiples map[string]float64 // industry code -> EV/Revenue multiple (Phase 1 fallback)

	// RM-2 Phase 2: Damodaran sector tables (primary source). When either is
	// nil (config absent/unparseable at construction) resolveMultiple skips
	// tier (1) and behaves exactly like Phase 1 — the zero-regression path.
	damodaran      map[string]float64 // Damodaran industry name -> EV/Sales
	sicToDamodaran map[string]string  // SIC code -> Damodaran industry name
	datasetDate    string             // e.g. "2026-01-01"; "" when table absent

	logger *zap.Logger
}

// NewRevenueMultipleModel creates a new Revenue Multiple model. Loads sector
// multiples from the embedded industry_multiples.json (see config/configfs).
// No filesystem I/O — safe in any working directory and any deployment.
func NewRevenueMultipleModel(logger *zap.Logger) *RevenueMultipleModel {
	named := logger.Named("revenue-multiple-model")

	multiples := map[string]float64{"default": DefaultEVRevenueMultiple}
	if configMultiples, err := loadEVRevenueMultiples(); err == nil && len(configMultiples) > 0 {
		multiples = configMultiples
	}

	// RM-2 Phase 2: load the Damodaran sector table + SIC crosswalk. Warn-and-
	// fallback on any load error (same stance as loadEVRevenueMultiples above):
	// a nil table leaves resolveMultiple on the Phase 1 path — zero regression.
	damodaran, datasetDate, err := loadDamodaranMultiples()
	if err != nil || len(damodaran) == 0 {
		named.Warn("Damodaran multiples table unavailable; falling back to Phase 1 sector buckets",
			zap.Error(err))
		damodaran, datasetDate = nil, ""
	}
	sicToDamodaran, err := loadSICToDamodaran()
	if err != nil || len(sicToDamodaran) == 0 {
		named.Warn("SIC->Damodaran crosswalk unavailable; falling back to Phase 1 sector buckets",
			zap.Error(err))
		sicToDamodaran = nil
	}

	return &RevenueMultipleModel{
		multiples:      multiples,
		damodaran:      damodaran,
		sicToDamodaran: sicToDamodaran,
		datasetDate:    datasetDate,
		logger:         named,
	}
}

// NewRevenueMultipleModelWithMultiples creates a Revenue Multiple model with explicit multiples.
// Used for testing. The Damodaran tables are left nil, so resolveMultiple runs
// the Phase 1 path exclusively (the zero-regression configuration).
func NewRevenueMultipleModelWithMultiples(multiples map[string]float64, logger *zap.Logger) *RevenueMultipleModel {
	return &RevenueMultipleModel{
		multiples: multiples,
		logger:    logger.Named("revenue-multiple-model"),
	}
}

// NewRevenueMultipleModelWithDamodaran creates a Revenue Multiple model with
// explicit Phase 1 multiples AND injected Damodaran tables. Used for testing the
// SIC-first resolution path without depending on the embedded config bytes.
func NewRevenueMultipleModelWithDamodaran(
	multiples map[string]float64,
	damodaran map[string]float64,
	sicToDamodaran map[string]string,
	datasetDate string,
	logger *zap.Logger,
) *RevenueMultipleModel {
	return &RevenueMultipleModel{
		multiples:      multiples,
		damodaran:      damodaran,
		sicToDamodaran: sicToDamodaran,
		datasetDate:    datasetDate,
		logger:         logger.Named("revenue-multiple-model"),
	}
}

// resolveMultiple selects the EV/Revenue multiple and reports its provenance.
// Resolution order (RM-2 Phase 2):
//  1. Damodaran-by-SIC (lookupDamodaranMultiple) -> source "Damodaran <date>".
//  2. Phase 1 longest-prefix over m.multiples (getMultiple) -> source
//     "sector-bucket". This also covers the "default" key and the package
//     constant fallback, so resolveMultiple always returns a usable multiple.
//
// When the Damodaran tables are nil (config absent, or a test ctor that did not
// inject them) tier (1) is skipped and the result is bit-for-bit identical to
// getMultiple(industry) — the zero-regression guarantee.
// The third return value is the matched Damodaran industry name (e.g.
// "Semiconductor") on the Damodaran path, or "" on the Phase 1 / fallback path.
// It is used ONLY to enrich the audit warning line — the contract `source`
// string ("Damodaran <date>" / "sector-bucket") deliberately excludes it so the
// response field value stays stable.
func (m *RevenueMultipleModel) resolveMultiple(sic, industry string) (multiple float64, source, damodaranIndustry string) {
	if multiple, matchedIndustry, ok := lookupDamodaranMultiple(sic, m.sicToDamodaran, m.damodaran); ok {
		return multiple, "Damodaran " + m.datasetDate, matchedIndustry
	}
	return m.getMultiple(industry), "sector-bucket", ""
}

// ModelType returns the model identifier.
func (m *RevenueMultipleModel) ModelType() string {
	return "revenue_multiple"
}

// Calculate performs a revenue multiple valuation.
//
// This is the simplest valuation model and should be used only when DCF is not applicable.
// The result is always flagged as low-confidence.
func (m *RevenueMultipleModel) Calculate(ctx context.Context, input *ModelInput) (*ModelResult, error) {
	if input == nil {
		return nil, fmt.Errorf("revenue_multiple: model input is required")
	}

	latest, _ := input.HistoricalData.GetLatestPeriod()
	if latest == nil {
		return nil, fmt.Errorf("revenue_multiple: no financial data available")
	}

	// RM-1: never read latest.Revenue directly — that's single-quarter for
	// 10-Q filings and produces ~4x understatement against an annual
	// EV/Revenue multiple. The TTM helper enforces a documented fallback
	// chain (TTM_PRIOR_BRIDGE -> TTM_4Q -> ANNUAL_FY -> ANNUALIZED_QUARTER ->
	// INSUFFICIENT_HISTORY) and surfaces the path via `source` so replay
	// tooling and dashboards can audit lossy fallbacks. The bridge runs
	// first so partial-year IPO shapes are preserved as TTM_PRIOR_BRIDGE
	// instead of being silently absorbed into TTM_4Q.
	revenue, source, ttmWarning := input.HistoricalData.TrailingTwelveMonthsRevenue()
	if revenue <= 0 {
		return nil, fmt.Errorf("revenue_multiple: insufficient revenue history (%s)", source)
	}

	// Select the appropriate EV/Revenue multiple for this industry. RM-2
	// Phase 2: resolveMultiple tries the Damodaran-by-SIC table first and
	// degrades to the Phase 1 sector bucket; multipleSource records which won.
	multiple, multipleSource, damodaranIndustry := m.resolveMultiple(input.SICCode, input.Industry)

	// Calculate enterprise value
	enterpriseValue := revenue * multiple

	// Equity bridge: EV - Debt + Cash - DebtLikeClaims
	// DebtLikeClaims (B1 lease + B2 pension + B3 contingent overlays) competes
	// with shareholders for cash flows; after the DC-1 Phase 4 dual-write
	// deletion InterestBearingDebt no longer includes them, so they MUST be
	// subtracted here (mirrors the DCF equity bridge). 0 when no B-rule fires.
	equityValue := enterpriseValue - input.InterestBearingDebt + input.CashAndCashEquivalents - input.DebtLikeClaims

	// Calculate per-share value
	shares := input.SharesOutstanding
	if shares <= 0 {
		return nil, fmt.Errorf("revenue_multiple: shares outstanding must be positive")
	}

	valuePerShare := equityValue / shares
	if valuePerShare < 0 {
		valuePerShare = 0
	}

	warnings := []string{
		"Revenue multiple valuation is a rough approximation — does not account for profitability or cash flows",
		fmt.Sprintf("Applied %.1fx EV/Revenue multiple for %s sector", multiple, input.Industry),
	}

	// Surface the TTM source so consumers can distinguish a clean TTM_4Q
	// from a lossy ANNUALIZED_QUARTER. We always emit the source line
	// (even on the clean path) so downstream dashboards can pivot on it
	// without parsing free-form warning text. The helper returns a
	// non-empty `ttmWarning` ONLY for the lossy paths.
	warnings = append(warnings, fmt.Sprintf("revenue_base: source=%s revenue=$%.0f", source, revenue))
	if ttmWarning != "" {
		warnings = append(warnings, ttmWarning)
	}

	// RM-2 Phase 2: surface the multiple provenance on a dedicated warning line
	// (mirrors the revenue_base: convention) so dashboards can pivot on the
	// source without parsing the response struct. "Damodaran <date>" vs
	// "sector-bucket" — both are part of the public contract.
	if damodaranIndustry != "" {
		warnings = append(warnings, fmt.Sprintf("multiple_source: %s (industry=%s)", multipleSource, damodaranIndustry))
	} else {
		warnings = append(warnings, fmt.Sprintf("multiple_source: %s", multipleSource))
	}

	// RM-1.A: stale-data check (T7 from spec, deferred from the entity layer).
	// Lives here at the consumer rather than in HistoricalFinancialData so the
	// entity package stays clock-free and replay-deterministic. The clock seam
	// flows from *valuation.Service.clock -> input.Now (set when the Service
	// builds ModelInput); when nil (older test call sites that pre-date this
	// plumbing), we fall back to time.Now. Fires regardless of which TTM
	// source path won — staleness is a property of the filing date, not the
	// synthesis algorithm.
	if monthsOld := monthsSince(latest.FilingDate, input.Now); monthsOld >= staleDataThresholdMonths {
		warnings = append(warnings, fmt.Sprintf("revenue_base: data is %d months old", monthsOld))
	}

	// Additional warning for negative OI companies
	baseOI := latest.NormalizedOperatingIncome
	if baseOI <= 0 {
		baseOI = latest.OperatingIncome
	}
	if baseOI <= 0 {
		warnings = append(warnings,
			fmt.Sprintf("Company has negative operating income (%.2f); standard DCF not applicable", baseOI))
	}

	// RM-3 forward path. Gated on profile.HorizonYears > 0; nil profile or
	// horizon == 0 falls through to trailing-only behavior so legacy call
	// sites (pre-Tier-2 ModelInputs without Profile, fallback wildcard
	// profiles with horizon=0) keep their existing per-share output. Spec §6.1.
	trailingValue := valuePerShare
	forwardValue := 0.0
	horizonSelected := 0
	terminalMultipleUsed := 0.0

	if input.Profile != nil && input.Profile.HorizonYears > 0 {
		p := &input.Profile.AssumptionProfile
		var rates []float64
		if input.GrowthEstimate != nil {
			rates = input.GrowthEstimate.ProjectedGrowthRates
		}
		// Guard: only project when we have enough explicit growth-rate cells
		// to cover the horizon AND a positive cost-of-equity to discount at.
		// Partial coverage would silently shrink the forward base; better to
		// emit zero (omitted via omitempty) than a half-finished projection.
		if len(rates) >= p.HorizonYears && input.CostOfEquity > 0 {
			revenueBase := normalizeRevenueBase(revenue, p.RevenueBaseMethod, input.HistoricalData)

			// Compound revenue forward at the per-year projected rates.
			forwardRevenue := revenueBase
			for i := 0; i < p.HorizonYears; i++ {
				forwardRevenue *= 1 + rates[i]
			}

			// Apply terminal multiple to the projected revenue. For RM-3
			// "terminal" is really "horizon-year multiple of revenue" — a
			// relative-valuation construct, not a Gordon perpetuity.
			forwardEV := forwardRevenue * p.TerminalMultiple

			// Discount at cost-of-equity (NOT WACC — relative valuation
			// per RM-3 spec correction §6.1). Profiles that explicitly
			// pick DiscountWACC opt out of equity-discount and use WACC.
			discountRate := input.CostOfEquity
			if p.DiscountMethod == profile.DiscountWACC && input.WACC > 0 {
				discountRate = input.WACC
			}
			discount := math.Pow(1+discountRate, float64(p.HorizonYears))
			if discount > 0 {
				forwardEV /= discount
			}

			// Forward equity bridge: same EV - Debt + Cash - DebtLikeClaims
			// shape as the trailing path above.
			forwardEquity := forwardEV - input.InterestBearingDebt + input.CashAndCashEquivalents - input.DebtLikeClaims
			forwardValue = forwardEquity / shares
			if forwardValue < 0 {
				forwardValue = 0
			}

			horizonSelected = p.HorizonYears
			terminalMultipleUsed = p.TerminalMultiple

			warnings = append(warnings,
				fmt.Sprintf("RM-3 forward: %dy projection at avg %.1f%% growth, terminal %.1fx",
					p.HorizonYears, avg(rates[:p.HorizonYears])*100, p.TerminalMultiple))
		}
	}

	logctx.Or(ctx, m.logger).Info("Revenue multiple valuation completed",
		zap.Float64("revenue", revenue),
		zap.Float64("multiple", multiple),
		zap.String("industry", input.Industry),
		zap.Float64("enterprise_value", enterpriseValue),
		zap.Float64("value_per_share", valuePerShare),
		zap.Float64("forward_value", forwardValue),
		zap.Int("horizon_selected", horizonSelected))

	return &ModelResult{
		IntrinsicValuePerShare: valuePerShare,
		TrailingValue:          trailingValue,
		ForwardValue:           forwardValue,
		HorizonSelected:        horizonSelected,
		TerminalMultiple:       terminalMultipleUsed,
		EnterpriseValue:        enterpriseValue,
		EquityValue:            equityValue,
		ModelType:              "revenue_multiple",
		MultipleSource:         multipleSource,
		Warnings:               warnings,
		Confidence:             "low", // Always low confidence for revenue multiples
	}, nil
}

// normalizeRevenueBase applies the profile-specified normalization to the
// trailing-revenue input. Per spec §3.1 RevenueBaseMethod enum:
//   - raw_ttm: use the TTM helper output as-is (default for stable issuers).
//   - two_year_average: avg of the two most recent annual periods. Smooths
//     out one-time spikes for issuers with lumpy revenue recognition.
//   - max_ttm_or_floor: max(TTM, 5y mean) — the cyclical-trough rule. When
//     a semi or auto issuer is in a down-cycle the TTM understates the
//     mid-cycle base; the 5y mean floors the projection at a sustainable
//     run-rate.
//   - mid_cycle_normalized: 5y mean directly (overrides TTM entirely). Used
//     for archetypes where the TTM is structurally noisy.
//
// Nil or empty history is tolerated and returns ttm (defensive). Callers
// remain responsible for declining to project when the history is too thin
// for the method they chose; this helper picks the safest output given
// what's available.
func normalizeRevenueBase(ttm float64, method profile.RevenueBaseMethod, hist *entities.HistoricalFinancialData) float64 {
	switch method {
	case profile.RevenueBaseTwoYearAverage:
		annuals := newestFirstAnnualRevenues(hist, 2)
		if len(annuals) < 2 {
			return ttm
		}
		return (annuals[0] + annuals[1]) / 2
	case profile.RevenueBaseMaxTTMOrFloor:
		floor := meanRecentRevenue(hist, 5)
		if floor > ttm {
			return floor
		}
		return ttm
	case profile.RevenueBaseMidCycleNormalized:
		return meanRecentRevenue(hist, 5)
	default: // RevenueBaseRawTTM (and any future enum value not yet handled).
		return ttm
	}
}

// meanRecentRevenue returns the arithmetic mean of the most-recent up-to-N
// annual revenue periods. Returns 0 when no annual history is available;
// callers should treat 0 as "no signal" rather than a meaningful floor.
// When fewer than N annuals exist, the helper averages whatever it has
// rather than dividing by N (which would understate the mean and bias the
// floor downward for short-history tickers).
func meanRecentRevenue(hist *entities.HistoricalFinancialData, years int) float64 {
	annuals := newestFirstAnnualRevenues(hist, years)
	if len(annuals) == 0 {
		return 0
	}
	sum := 0.0
	for _, r := range annuals {
		sum += r
	}
	return sum / float64(len(annuals))
}

// newestFirstAnnualRevenues returns up to `n` revenue values for the most-
// recent annual (FY) periods, newest first. Encapsulates the period-sort
// idiom so the two consumers (normalizeRevenueBase, meanRecentRevenue) stay
// thin. Returns an empty slice for nil or empty history.
func newestFirstAnnualRevenues(hist *entities.HistoricalFinancialData, n int) []float64 {
	if hist == nil || len(hist.Data) == 0 || n <= 0 {
		return nil
	}
	annual := hist.GetAnnualPeriods()
	if len(annual) == 0 {
		return nil
	}
	periods := make([]string, 0, len(annual))
	for k := range annual {
		periods = append(periods, k)
	}
	// Lexicographic descending sort works for the "YYYYFY" key shape: the
	// year prefix dominates, and the constant "FY" suffix breaks no ties.
	sort.Sort(sort.Reverse(sort.StringSlice(periods)))
	out := make([]float64, 0, n)
	for i, k := range periods {
		if i >= n {
			break
		}
		if d := annual[k]; d != nil {
			out = append(out, d.Revenue)
		}
	}
	return out
}

// getMultiple returns the EV/Revenue multiple for the given industry code:
// exact match, then longest prefix match at an underscore boundary (the
// shared W-4-deterministic core, LookupByLongestPrefix — SR-1 A10), then the
// "default" key, then the package constant.
func (m *RevenueMultipleModel) getMultiple(industry string) float64 {
	if multiple, ok := LookupByLongestPrefix(m.multiples, industry); ok {
		return multiple
	}
	if defaultMultiple, ok := m.multiples["default"]; ok {
		return defaultMultiple
	}
	return DefaultEVRevenueMultiple
}

// monthsSince returns the whole-month difference between now() and filingDate.
// Returns 0 when filingDate is the zero value (no signal to measure against)
// or when filingDate is in the future relative to now (clock skew / fixtures
// dated ahead — never report negative staleness). When now is nil, falls
// back to time.Now so older call sites that did not plumb the clock are
// safe; production wiring routes input.Now through *Service.clock so replay
// stays deterministic.
func monthsSince(filingDate time.Time, now func() time.Time) int {
	if filingDate.IsZero() {
		return 0
	}
	clockNow := time.Now
	if now != nil {
		clockNow = now
	}
	current := clockNow()
	if !current.After(filingDate) {
		return 0
	}
	// Whole-month delta via (Y*12 + M) decomposition; matches Bloomberg/FactSet
	// "months stale" reporting convention. Day-of-month adjustment ensures the
	// 18-month boundary fires at exactly 18 months elapsed, not 17.x.
	months := (current.Year()-filingDate.Year())*12 + int(current.Month()-filingDate.Month())
	if current.Day() < filingDate.Day() {
		months--
	}
	if months < 0 {
		return 0
	}
	return months
}

// loadEVRevenueMultiples loads EV/Revenue multiples from the embedded
// industry_multiples.json.
func loadEVRevenueMultiples() (map[string]float64, error) {
	data, err := configfs.Read("industry_multiples.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded industry multiples config: %w", err)
	}

	var cfg struct {
		EVRevenueMultiples map[string]float64 `json:"ev_revenue_multiples"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse industry multiples config: %w", err)
	}

	return cfg.EVRevenueMultiples, nil
}
