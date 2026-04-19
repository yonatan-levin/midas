package models

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
)

// DefaultPFFOMultiple is the default P/FFO multiple for REITs when no sector-specific
// multiple is available from config.
const DefaultPFFOMultiple = 15.0

// DefaultREITCapRate is the default capitalization rate for NAV cross-check (6%).
const DefaultREITCapRate = 0.06

// DefaultIndustryMultiplesPath is the default path to the industry multiples config file.
const DefaultIndustryMultiplesPath = "./config/industry_multiples.json"

// NAV divergence thresholds — kept in sync with valuation.DeviationThreshold{High,Low}.
// Declared locally because the models package cannot import its parent valuation package.
const (
	navDeviationThresholdHigh = 2.0
	navDeviationThresholdLow  = 0.5
)

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
type FFOModel struct {
	pffoMultiple float64 // P/FFO multiple to apply
	navCapRate   float64 // Cap rate for NAV cross-check (0 = skip NAV)
	logger       *zap.Logger
}

// NewFFOModel creates a new FFO model with P/FFO multiple and NAV cap rate
// loaded from the given config path. If configPath is empty, uses DefaultIndustryMultiplesPath.
// The config file is read ONCE — both fields are parsed from a single read.
func NewFFOModel(configPath string, logger *zap.Logger) *FFOModel {
	if configPath == "" {
		configPath = DefaultIndustryMultiplesPath
	}
	multiple, capRate := loadFFOConfig(configPath)

	return &FFOModel{
		pffoMultiple: multiple,
		navCapRate:   capRate,
		logger:       logger.Named("ffo-model"),
	}
}

// NewFFOModelWithConfig creates an FFO model with explicit P/FFO multiple and
// NAV cap rate. Used for testing and when config is provided externally.
func NewFFOModelWithConfig(pffoMultiple, navCapRate float64, logger *zap.Logger) *FFOModel {
	return &FFOModel{
		pffoMultiple: pffoMultiple,
		navCapRate:   navCapRate,
		logger:       logger.Named("ffo-model"),
	}
}

// NewFFOModelWithMultiple creates an FFO model with an explicit P/FFO multiple,
// defaulting the NAV cap rate. Kept for backward compatibility with existing tests.
func NewFFOModelWithMultiple(pffoMultiple float64, logger *zap.Logger) *FFOModel {
	return NewFFOModelWithConfig(pffoMultiple, DefaultREITCapRate, logger)
}

// loadFFOConfig reads the industry multiples file ONCE and returns both the
// P/FFO multiple and the NAV cap rate. Falls back to defaults on any error.
// This replaces two separate file reads in the constructor.
func loadFFOConfig(path string) (pffoMultiple, navCapRate float64) {
	pffoMultiple = DefaultPFFOMultiple
	navCapRate = DefaultREITCapRate

	data, err := os.ReadFile(path)
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

// loadPFFOMultiple returns only the P/FFO multiple from the config file.
// Thin wrapper preserved for backward compatibility with existing tests.
func loadPFFOMultiple(path string) (float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("failed to read industry multiples config: %w", err)
	}
	var cfg struct {
		REITPFFOMultiples map[string]float64 `json:"reit_pffo_multiples"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return 0, fmt.Errorf("failed to parse industry multiples config: %w", err)
	}
	if v, ok := cfg.REITPFFOMultiples["default"]; ok {
		return v, nil
	}
	return 0, fmt.Errorf("no default P/FFO multiple found in config")
}

// loadREITCapRate returns only the REIT cap rate from the config file.
// Thin wrapper preserved for backward compatibility with existing tests.
func loadREITCapRate(path string) (float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("failed to read industry multiples config: %w", err)
	}
	var cfg struct {
		REITCapRates map[string]float64 `json:"reit_cap_rates"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return 0, fmt.Errorf("failed to parse industry multiples config: %w", err)
	}
	if v, ok := cfg.REITCapRates["default"]; ok {
		return v, nil
	}
	return 0, fmt.Errorf("no default REIT cap rate found in config")
}

// ModelType returns the model identifier.
func (m *FFOModel) ModelType() string {
	return "ffo"
}

// SupportsIndustry returns true for REIT/real estate industry codes.
func (m *FFOModel) SupportsIndustry(industry string) bool {
	upper := strings.ToUpper(industry)
	return upper == "REIT" || upper == "RESTATE"
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

	// Apply P/FFO multiple
	valuePerShare := ffoPerShare * m.pffoMultiple

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
		m.logger.Debug("No property gains/losses data; FFO equals Net Income + D&A")
	}

	confidence := "medium"
	if ffo > 0 && da > 0 {
		confidence = "high"
	}
	if ffo <= 0 || len(warnings) > 1 {
		confidence = "low"
	}

	// NAV cross-check: compare P/FFO value against NAV per share.
	// NAV = NOI / Cap Rate, using OperatingIncome as a proxy for Net Operating Income.
	// Informational only — does not change the primary P/FFO valuation.
	if m.navCapRate > 0 && latest.OperatingIncome > 0 && valuePerShare > 0 {
		nav := latest.OperatingIncome / m.navCapRate
		navPerShare := nav / shares

		m.logger.Debug("NAV cross-check",
			zap.Float64("noi_proxy", latest.OperatingIncome),
			zap.Float64("cap_rate", m.navCapRate),
			zap.Float64("nav_per_share", navPerShare),
			zap.Float64("pffo_value_per_share", valuePerShare))

		// Flag if P/FFO value diverges significantly from NAV per share
		if navPerShare > 0 {
			ratio := valuePerShare / navPerShare
			if ratio > navDeviationThresholdHigh || ratio < navDeviationThresholdLow {
				warnings = append(warnings,
					fmt.Sprintf("P/FFO value ($%.2f) diverges from NAV cross-check ($%.2f/share, cap rate %.1f%%); ratio=%.2fx",
						valuePerShare, navPerShare, m.navCapRate*100, ratio))
			}
		}
	}

	m.logger.Info("FFO valuation completed",
		zap.Float64("net_income", netIncome),
		zap.Float64("da", da),
		zap.Float64("property_gains", propertyGains),
		zap.Float64("ffo", ffo),
		zap.Float64("ffo_per_share", ffoPerShare),
		zap.Float64("pffo_multiple", m.pffoMultiple),
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
