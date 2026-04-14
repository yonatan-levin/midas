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

// DefaultIndustryMultiplesPath is the default path to the industry multiples config file.
const DefaultIndustryMultiplesPath = "./config/industry_multiples.json"

// FFOModel implements the Funds From Operations model for REITs.
//
// FFO = Net Income + D&A - Gains on Property Sales
// Value = (FFO / Shares) * P/FFO Multiple
//
// This model is the standard valuation approach for Real Estate Investment Trusts (REITs)
// since traditional earnings are distorted by large depreciation charges on real estate assets.
type FFOModel struct {
	pffoMultiple float64 // P/FFO multiple to apply
	logger       *zap.Logger
}

// NewFFOModel creates a new FFO model with P/FFO multiple loaded from the given config path.
// If configPath is empty, uses DefaultIndustryMultiplesPath.
func NewFFOModel(configPath string, logger *zap.Logger) *FFOModel {
	if configPath == "" {
		configPath = DefaultIndustryMultiplesPath
	}
	multiple := DefaultPFFOMultiple

	// Attempt to load the P/FFO multiple from config
	configMultiple, err := loadPFFOMultiple(configPath)
	if err == nil && configMultiple > 0 {
		multiple = configMultiple
	}

	return &FFOModel{
		pffoMultiple: multiple,
		logger:       logger.Named("ffo-model"),
	}
}

// NewFFOModelWithMultiple creates an FFO model with an explicit P/FFO multiple.
// Used for testing and when the multiple is provided externally.
func NewFFOModelWithMultiple(pffoMultiple float64, logger *zap.Logger) *FFOModel {
	return &FFOModel{
		pffoMultiple: pffoMultiple,
		logger:       logger.Named("ffo-model"),
	}
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

// loadPFFOMultiple loads the default P/FFO multiple from the industry multiples config file.
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

	if defaultMultiple, ok := cfg.REITPFFOMultiples["default"]; ok {
		return defaultMultiple, nil
	}

	return 0, fmt.Errorf("no default P/FFO multiple found in config")
}
