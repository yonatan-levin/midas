package models

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	configfs "github.com/midas/dcf-valuation-api/config"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/thresholds"
)

// DefaultPFFOMultiple is the default P/FFO multiple for REITs when no sector-specific
// multiple is available from config.
const DefaultPFFOMultiple = 15.0

// DefaultREITCapRate is the default capitalization rate for NAV cross-check (6%).
const DefaultREITCapRate = 0.06

// FFOModel implements the Funds From Operations model for REITs.
//
// FFO = Net Income + D&A - Gains on Property Sales
// Value = (FFO / Shares) * P/FFO Multiple
//
// Applied to REITs because accounting depreciation distorts net income —
// buildings depreciate on paper while typically appreciating in value.
//
// NAV cross-check: compares P/FFO value against NAV (= NOI / Cap Rate, using
// OperatingIncome as NOI proxy). Informational only — does not override P/FFO.
//
// Subsector multiples (VAL-3 P1+P4, T2-P4-W1 prefix reconciliation):
// pffoMultiples and capRates carry the full per-subsector tables
// (REIT_RESIDENTIAL, REIT_OFFICE, REIT_INDUSTRIAL, REIT_RETAIL,
// REIT_HEALTHCARE, REIT_DATACENTER, REIT_CELLTOWER, REIT_SPECIALTY). The
// lookup happens at Calculate() time using ModelInput.Industry, with
// longest-prefix-match against the keys. Falls back to pffoMultiple /
// navCapRate (the "default" entries snapshotted at construction) when no
// subsector match is found.
type FFOModel struct {
	pffoMultiple  float64            // Default P/FFO multiple (used when industry-specific lookup misses)
	navCapRate    float64            // Default cap rate (used when industry-specific lookup misses; 0 = skip NAV)
	pffoMultiples map[string]float64 // Subsector P/FFO table loaded from industry_multiples.json
	capRates      map[string]float64 // Subsector cap-rate table loaded from industry_multiples.json
	logger        *zap.Logger
}

// NewFFOModel creates a new FFO model reading both the default P/FFO multiple
// + NAV cap rate AND the full per-subsector tables (reit_pffo_multiples and
// reit_cap_rates) from the embedded industry_multiples.json (see
// config/configfs). No filesystem I/O — safe in any working directory and in
// any deployment target (Docker, standalone binary, tests).
//
// NAV cross-check is enabled by default with the embedded cap rate. To
// disable NAV cross-check pass `NewFFOModelWithConfig(multiple, 0, logger)`.
func NewFFOModel(logger *zap.Logger) *FFOModel {
	multiple, capRate := loadFFOConfig()
	pffoTable, capRateTable := loadFFOSubsectorTables()
	return &FFOModel{
		pffoMultiple:  multiple,
		navCapRate:    capRate,
		pffoMultiples: pffoTable,
		capRates:      capRateTable,
		logger:        logger.Named("ffo-model"),
	}
}

// NewFFOModelWithConfig creates an FFO model with explicit P/FFO multiple and
// NAV cap rate. Used for testing and when config is provided externally.
// Pass 0 for navCapRate to disable the NAV cross-check.
//
// Subsector tables are still loaded from the embedded config so subsector tests
// can exercise the lookup path. Tests that need to suppress subsector lookup
// should use NewFFOModelWithTables and pass nil maps.
func NewFFOModelWithConfig(pffoMultiple, navCapRate float64, logger *zap.Logger) *FFOModel {
	pffoTable, capRateTable := loadFFOSubsectorTables()
	return &FFOModel{
		pffoMultiple:  pffoMultiple,
		navCapRate:    navCapRate,
		pffoMultiples: pffoTable,
		capRates:      capRateTable,
		logger:        logger.Named("ffo-model"),
	}
}

// NewFFOModelWithTables creates an FFO model with explicit subsector tables
// alongside the default multiple + cap rate. Used for testing the subsector
// lookup path with hand-built tables. Passing nil maps disables subsector
// lookup entirely (model falls back to the default values for every input).
func NewFFOModelWithTables(pffoMultiple, navCapRate float64, pffoTable, capRateTable map[string]float64, logger *zap.Logger) *FFOModel {
	return &FFOModel{
		pffoMultiple:  pffoMultiple,
		navCapRate:    navCapRate,
		pffoMultiples: pffoTable,
		capRates:      capRateTable,
		logger:        logger.Named("ffo-model"),
	}
}

// NewFFOModelWithMultiple creates an FFO model with an explicit P/FFO multiple
// and the default NAV cap rate (DefaultREITCapRate = 6%). NAV cross-check is
// enabled by default; use NewFFOModelWithConfig(multiple, 0, logger) to
// disable it. Kept for backward compatibility with existing tests.
func NewFFOModelWithMultiple(pffoMultiple float64, logger *zap.Logger) *FFOModel {
	return NewFFOModelWithConfig(pffoMultiple, DefaultREITCapRate, logger)
}

// loadFFOConfig reads the embedded industry_multiples.json ONCE and returns
// both the P/FFO multiple and the NAV cap rate. Falls back to defaults on any
// error. Consolidates the three separate loaders that existed pre-V4.1-N2.
func loadFFOConfig() (pffoMultiple, navCapRate float64) {
	pffoMultiple = DefaultPFFOMultiple
	navCapRate = DefaultREITCapRate

	data, err := configfs.Read("industry_multiples.json")
	if err != nil {
		return pffoMultiple, navCapRate
	}

	var cfg struct {
		REITPFFOMultiples map[string]float64 `json:"reit_pffo_multiples"`
		REITCapRates      map[string]float64 `json:"reit_cap_rates"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return pffoMultiple, navCapRate
	}

	if v, ok := cfg.REITPFFOMultiples["default"]; ok && v > 0 {
		pffoMultiple = v
	}
	if v, ok := cfg.REITCapRates["default"]; ok && v > 0 {
		navCapRate = v
	}
	return pffoMultiple, navCapRate
}

// loadFFOSubsectorTables returns the full reit_pffo_multiples and
// reit_cap_rates maps from the embedded industry_multiples.json. Used by the
// subsector-aware lookup at Calculate() time. Returns nil maps on any read or
// parse error — callers must treat nil as "subsector lookup disabled" and fall
// back to the default values. Keys are REIT_* prefixed subsector codes
// (REIT_RESIDENTIAL, REIT_DATACENTER, REIT_RETAIL, …) per VAL-3 P1+P4 and
// the T2-P4-W1 prefix reconciliation.
func loadFFOSubsectorTables() (pffoTable, capRateTable map[string]float64) {
	data, err := configfs.Read("industry_multiples.json")
	if err != nil {
		return nil, nil
	}

	var cfg struct {
		REITPFFOMultiples map[string]float64 `json:"reit_pffo_multiples"`
		REITCapRates      map[string]float64 `json:"reit_cap_rates"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, nil
	}

	return cfg.REITPFFOMultiples, cfg.REITCapRates
}

// ModelType returns the model identifier.
func (m *FFOModel) ModelType() string {
	return "ffo"
}

// getMultiple returns the P/FFO multiple for the given subsector code. Looks
// up an exact (uppercased) match first, then longest-prefix-match at an
// underscore boundary, then falls back to the model's default pffoMultiple.
// The "default" key is excluded from prefix matching to mirror
// RevenueMultipleModel.getMultiple and crosscheck.LookupMultiple semantics.
func (m *FFOModel) getMultiple(industry string) float64 {
	if v, ok := lookupSubsectorValue(m.pffoMultiples, industry); ok {
		return v
	}
	return m.pffoMultiple
}

// getCapRate returns the NAV cap rate for the given subsector code. Same
// lookup semantics as getMultiple — exact match, longest-prefix-match, then
// the model's default navCapRate.
func (m *FFOModel) getCapRate(industry string) float64 {
	if v, ok := lookupSubsectorValue(m.capRates, industry); ok {
		return v
	}
	return m.navCapRate
}

// lookupSubsectorValue performs the shared subsector lookup used by both
// getMultiple and getCapRate. Returns (value, true) on a hit and (0, false)
// on miss / nil-or-empty table / empty industry, so callers can apply their
// own default. Mirrors the longest-prefix-match algorithm used by
// RevenueMultipleModel.getMultiple and crosscheck.LookupMultiple — keeps
// behaviour consistent across the three tables.
func lookupSubsectorValue(table map[string]float64, industry string) (float64, bool) {
	if len(table) == 0 || industry == "" {
		return 0, false
	}
	upper := strings.ToUpper(industry)

	if v, ok := table[upper]; ok {
		return v, true
	}

	bestKey := ""
	bestVal := 0.0
	for code, v := range table {
		if code == "default" {
			continue
		}
		// Match must end at the string end or an underscore so "TECHNOLOGY"
		// can never silently match "TECH"; longest match wins deterministically
		// regardless of Go's map iteration order (W-4 invariant).
		if upper == code || strings.HasPrefix(upper, code+"_") {
			if len(code) > len(bestKey) {
				bestKey = code
				bestVal = v
			}
		}
	}
	if bestKey != "" {
		return bestVal, true
	}
	return 0, false
}

// Calculate performs an FFO-based valuation for a REIT.
//
// FFO = Net Income + D&A - Gains on Property Sales
// Equity Value = (FFO per share) * P/FFO Multiple * Shares Outstanding
func (m *FFOModel) Calculate(ctx context.Context, input *ModelInput) (*ModelResult, error) {
	if input == nil {
		return nil, fmt.Errorf("ffo: model input is required")
	}

	latest, _ := input.HistoricalData.GetLatestPeriod()
	if latest == nil {
		return nil, fmt.Errorf("ffo: no financial data available")
	}

	warnings := []string{}

	// Calculate FFO: Net Income + D&A - Gains on Property Sales
	netIncome := latest.NetIncome
	da := latest.DepreciationAndAmortization
	propertyGains := latest.GainOnPropertySales

	// Validate we have minimum data for FFO calculation
	if netIncome == 0 && da == 0 {
		return nil, fmt.Errorf("ffo: insufficient data for FFO calculation (no net income or D&A)")
	}

	ffo := netIncome + da - propertyGains

	if ffo <= 0 {
		warnings = append(warnings, fmt.Sprintf("Negative FFO (%.2f) indicates REIT may be distressed", ffo))
	}

	// Calculate per-share values
	shares := input.SharesOutstanding
	if shares <= 0 {
		return nil, fmt.Errorf("ffo: shares outstanding must be positive")
	}

	ffoPerShare := ffo / shares

	// Apply P/FFO multiple — looks up the subsector-specific value (e.g. 31×
	// for REIT_DATACENTER, 25× for REIT_CELLTOWER) before falling back to the
	// default. VAL-3 P1: REIT subsectors differ 3× in 2025-26 multiples; using
	// the subsector key keeps the model from systematically under/over-valuing
	// data center / cell tower / mall REITs.
	pffoMultiple := m.getMultiple(input.Industry)
	valuePerShare := ffoPerShare * pffoMultiple

	// If FFO is negative, value should be zero (don't assign negative intrinsic value)
	if valuePerShare < 0 {
		valuePerShare = 0
		warnings = append(warnings, "FFO-based value is zero due to negative FFO")
	}

	equityValue := valuePerShare * shares
	enterpriseValue := equityValue + input.InterestBearingDebt - input.CashAndCashEquivalents

	// Data quality warnings
	if da == 0 {
		warnings = append(warnings, "D&A not available; FFO may understate true funds from operations")
	}

	if propertyGains == 0 {
		// This is common and OK — just note it
		logctx.Or(ctx, m.logger).Debug("No property gains/losses data; FFO equals Net Income + D&A")
	}

	confidence := "medium"
	if ffo > 0 && da > 0 {
		confidence = "high"
	}
	if ffo <= 0 || len(warnings) > 1 {
		confidence = "low"
	}

	// NAV cross-check: compare P/FFO value against NAV per share.
	// NAV = NOI / Cap Rate, using OperatingIncome as a proxy for Net Operating
	// Income. Informational only — does not change the primary P/FFO valuation.
	// Cap rate is looked up by subsector (VAL-3 P4) so e.g. data center REITs
	// use 4.0% and retail REITs use 8.5% rather than the blended 6% default.
	capRate := m.getCapRate(input.Industry)
	if capRate > 0 && latest.OperatingIncome > 0 && valuePerShare > 0 {
		nav := latest.OperatingIncome / capRate
		navPerShare := nav / shares

		logctx.Or(ctx, m.logger).Debug("NAV cross-check",
			zap.Float64("noi_proxy", latest.OperatingIncome),
			zap.Float64("cap_rate", capRate),
			zap.Float64("nav_per_share", navPerShare),
			zap.Float64("pffo_value_per_share", valuePerShare))

		// Flag if P/FFO value diverges significantly from NAV per share
		if navPerShare > 0 {
			ratio := valuePerShare / navPerShare
			if ratio > thresholds.DeviationHigh || ratio < thresholds.DeviationLow {
				warnings = append(warnings,
					fmt.Sprintf("P/FFO value ($%.4g) diverges from NAV cross-check ($%.4g/share, cap rate %.1f%%); ratio=%.2fx",
						valuePerShare, navPerShare, capRate*100, ratio))
			}
		}
	}

	logctx.Or(ctx, m.logger).Info("FFO valuation completed",
		zap.Float64("net_income", netIncome),
		zap.Float64("da", da),
		zap.Float64("property_gains", propertyGains),
		zap.Float64("ffo", ffo),
		zap.Float64("ffo_per_share", ffoPerShare),
		zap.String("industry", input.Industry),
		zap.Float64("pffo_multiple", pffoMultiple),
		zap.Float64("value_per_share", valuePerShare))

	return &ModelResult{
		IntrinsicValuePerShare: valuePerShare,
		EnterpriseValue:        enterpriseValue,
		EquityValue:            equityValue,
		ModelType:              "ffo",
		Warnings:               warnings,
		Confidence:             confidence,
	}, nil
}
