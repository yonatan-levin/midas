package models

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	configfs "github.com/midas/dcf-valuation-api/config"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
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
	multiples map[string]float64 // industry code -> EV/Revenue multiple
	logger    *zap.Logger
}

// NewRevenueMultipleModel creates a new Revenue Multiple model. Loads sector
// multiples from the embedded industry_multiples.json (see config/configfs).
// No filesystem I/O — safe in any working directory and any deployment.
func NewRevenueMultipleModel(logger *zap.Logger) *RevenueMultipleModel {
	multiples := map[string]float64{"default": DefaultEVRevenueMultiple}
	if configMultiples, err := loadEVRevenueMultiples(); err == nil && len(configMultiples) > 0 {
		multiples = configMultiples
	}
	return &RevenueMultipleModel{
		multiples: multiples,
		logger:    logger.Named("revenue-multiple-model"),
	}
}

// NewRevenueMultipleModelWithMultiples creates a Revenue Multiple model with explicit multiples.
// Used for testing.
func NewRevenueMultipleModelWithMultiples(multiples map[string]float64, logger *zap.Logger) *RevenueMultipleModel {
	return &RevenueMultipleModel{
		multiples: multiples,
		logger:    logger.Named("revenue-multiple-model"),
	}
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

	// Select the appropriate EV/Revenue multiple for this industry
	multiple := m.getMultiple(input.Industry)

	// Calculate enterprise value
	enterpriseValue := revenue * multiple

	// Equity bridge: EV - Debt + Cash
	equityValue := enterpriseValue - input.InterestBearingDebt + input.CashAndCashEquivalents

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

	logctx.Or(ctx, m.logger).Info("Revenue multiple valuation completed",
		zap.Float64("revenue", revenue),
		zap.Float64("multiple", multiple),
		zap.String("industry", input.Industry),
		zap.Float64("enterprise_value", enterpriseValue),
		zap.Float64("value_per_share", valuePerShare))

	return &ModelResult{
		IntrinsicValuePerShare: valuePerShare,
		EnterpriseValue:        enterpriseValue,
		EquityValue:            equityValue,
		ModelType:              "revenue_multiple",
		Warnings:               warnings,
		Confidence:             "low", // Always low confidence for revenue multiples
	}, nil
}

// getMultiple returns the EV/Revenue multiple for the given industry code.
// Falls back to the default multiple if no industry-specific multiple is
// configured. Uses longest-prefix-match at an underscore boundary to avoid
// "TECHNOLOGY" silently matching "TECH" (same fix as crosscheck.LookupMultiple).
func (m *RevenueMultipleModel) getMultiple(industry string) float64 {
	upper := strings.ToUpper(industry)

	// Try exact match first
	if multiple, ok := m.multiples[upper]; ok {
		return multiple
	}

	// Longest prefix match at an underscore boundary
	// (e.g., "TECH_SAAS_CLOUD" matches "TECH_SAAS" over "TECH").
	bestKey := ""
	bestVal := 0.0
	for code, multiple := range m.multiples {
		if code == "default" {
			continue
		}
		if upper == code || strings.HasPrefix(upper, code+"_") {
			if len(code) > len(bestKey) {
				bestKey = code
				bestVal = multiple
			}
		}
	}
	if bestKey != "" {
		return bestVal
	}

	// Default fallback
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
