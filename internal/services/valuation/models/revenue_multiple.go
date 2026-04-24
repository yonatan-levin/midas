package models

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
)

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

// NewRevenueMultipleModel creates a new Revenue Multiple model.
// Loads sector multiples from the given config path. If configPath is empty, uses DefaultIndustryMultiplesPath.
func NewRevenueMultipleModel(configPath string, logger *zap.Logger) *RevenueMultipleModel {
	if configPath == "" {
		configPath = DefaultIndustryMultiplesPath
	}
	multiples := map[string]float64{
		"default": DefaultEVRevenueMultiple,
	}

	// Attempt to load multiples from config
	configMultiples, err := loadEVRevenueMultiples(configPath)
	if err == nil && len(configMultiples) > 0 {
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

	revenue := latest.Revenue
	if revenue <= 0 {
		return nil, fmt.Errorf("revenue_multiple: company has no revenue (%.2f); cannot apply revenue multiple", revenue)
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

// loadEVRevenueMultiples loads EV/Revenue multiples from the industry multiples config file.
func loadEVRevenueMultiples(path string) (map[string]float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read industry multiples config: %w", err)
	}

	var cfg struct {
		EVRevenueMultiples map[string]float64 `json:"ev_revenue_multiples"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse industry multiples config: %w", err)
	}

	return cfg.EVRevenueMultiples, nil
}
