package models

import (
	"context"
	"fmt"
	"math"
	"strings"

	"go.uber.org/zap"
)

// Minimum spread between cost of equity and dividend growth rate.
// Below this the Gordon Growth denominator blows up and the model is unreliable.
const ddmDenominatorEpsilon = 0.005

// Fraction of cost of equity to cap dividend growth at when g >= CoE.
// Keeps the model numerically stable for companies with temporarily high growth.
const ddmGrowthCapFraction = 0.7

// DDM P/BV divergence thresholds — kept in sync with valuation.DeviationThreshold{High,Low}.
// Declared locally because the models package cannot import its parent valuation package.
const (
	ddmPBVDeviationHigh = 2.0
	ddmPBVDeviationLow  = 0.5
)

// DDMModel implements the Gordon Growth / Dividend Discount Model for financial companies.
//
// Value = DPS * (1 + g) / (CoE - g)
//
// Where:
//   - DPS = Dividends per share
//   - g   = Dividend growth rate (from historical DPS growth or analyst estimates)
//   - CoE = Cost of equity (from CAPM)
//
// This model is appropriate for mature financial companies (banks, insurance)
// that pay regular dividends. It should NOT be used for growth companies
// with zero or irregular dividends.
//
// P/BV cross-check: compares implied P/BV against the ROE-justified P/BV
// = (ROE - g) / (CoE - g). Informational only — does not override DDM.
type DDMModel struct {
	logger *zap.Logger
}

// NewDDMModel creates a new Dividend Discount Model.
func NewDDMModel(logger *zap.Logger) *DDMModel {
	return &DDMModel{
		logger: logger.Named("ddm-model"),
	}
}

// ModelType returns the model identifier.
func (m *DDMModel) ModelType() string {
	return "ddm"
}

// SupportsIndustry returns true for financial industry codes.
func (m *DDMModel) SupportsIndustry(industry string) bool {
	return strings.HasPrefix(strings.ToUpper(industry), "FIN")
}

// Calculate performs a DDM valuation.
//
// Uses the Gordon Growth Model for a single-stage DDM.
// Falls back to P/E based approach if DPS is not available but the company is a dividend payer.
func (m *DDMModel) Calculate(ctx context.Context, input *ModelInput) (*ModelResult, error) {
	if input == nil {
		return nil, fmt.Errorf("ddm: model input is required")
	}

	latest, _ := input.HistoricalData.GetLatestPeriod()
	if latest == nil {
		return nil, fmt.Errorf("ddm: no financial data available")
	}

	dps := latest.DividendsPerShare
	if dps <= 0 {
		return nil, fmt.Errorf("ddm: company does not pay dividends (DPS=%.2f); DDM not applicable", dps)
	}

	costOfEquity := input.CostOfEquity
	if costOfEquity <= 0 {
		return nil, fmt.Errorf("ddm: cost of equity must be positive (got %.4f)", costOfEquity)
	}

	// Estimate dividend growth rate from historical data or growth estimate
	dividendGrowth := m.estimateDividendGrowth(input)

	// Guard: dividend growth must be below cost of equity for Gordon model to work
	if dividendGrowth >= costOfEquity {
		originalGrowth := dividendGrowth
		dividendGrowth = costOfEquity * ddmGrowthCapFraction
		m.logger.Warn("Dividend growth exceeds cost of equity, capping",
			zap.Float64("original_growth", originalGrowth),
			zap.Float64("capped_growth", dividendGrowth))
	}

	// Gordon Growth Model: Value = DPS * (1 + g) / (CoE - g)
	denominator := costOfEquity - dividendGrowth
	if denominator <= ddmDenominatorEpsilon {
		denominator = ddmDenominatorEpsilon
	}

	valuePerShare := dps * (1 + dividendGrowth) / denominator

	// Calculate implied equity and enterprise values
	equityValue := valuePerShare * input.SharesOutstanding
	enterpriseValue := equityValue + input.InterestBearingDebt - input.CashAndCashEquivalents

	warnings := []string{}

	// Validate ROE reasonableness for financials
	if latest.StockholdersEquity > 0 && latest.NetIncome > 0 {
		roe := latest.NetIncome / latest.StockholdersEquity
		if roe < 0.05 {
			warnings = append(warnings, fmt.Sprintf("Low ROE (%.1f%%) may indicate stressed financials", roe*100))
		}
		if roe > 0.25 {
			warnings = append(warnings, fmt.Sprintf("High ROE (%.1f%%) may be unsustainable", roe*100))
		}
	}

	// Payout ratio check
	if latest.NetIncome > 0 && input.SharesOutstanding > 0 {
		eps := latest.NetIncome / input.SharesOutstanding
		if eps > 0 {
			payoutRatio := dps / eps
			if payoutRatio > 0.9 {
				warnings = append(warnings, fmt.Sprintf("High payout ratio (%.0f%%) leaves little room for growth", payoutRatio*100))
			}
		}
	}

	// P/BV cross-check: implied P/BV (DDM value / book value per share) vs
	// ROE-justified P/BV (= (ROE - g) / (CoE - g)). Flags >2x or <0.5x divergence
	// as a signal that the DDM value may be inconsistent with fundamentals.
	if latest.StockholdersEquity > 0 && input.SharesOutstanding > 0 && latest.NetIncome > 0 {
		bookValuePerShare := latest.StockholdersEquity / input.SharesOutstanding
		if bookValuePerShare > 0 {
			impliedPBV := valuePerShare / bookValuePerShare
			roe := latest.NetIncome / latest.StockholdersEquity

			coeMinusG := costOfEquity - dividendGrowth
			roeMinusG := roe - dividendGrowth
			if coeMinusG > ddmDenominatorEpsilon && roeMinusG > 0 {
				roeJustifiedPBV := roeMinusG / coeMinusG
				if roeJustifiedPBV > 0 && impliedPBV > 0 {
					ratio := impliedPBV / roeJustifiedPBV
					if ratio > ddmPBVDeviationHigh || ratio < ddmPBVDeviationLow {
						warnings = append(warnings,
							fmt.Sprintf("Implied P/BV (%.2fx) diverges from ROE-justified P/BV (%.2fx); ratio=%.2fx",
								impliedPBV, roeJustifiedPBV, ratio))
					}
				}
				m.logger.Debug("P/BV cross-check",
					zap.Float64("implied_pbv", impliedPBV),
					zap.Float64("book_value_per_share", bookValuePerShare),
					zap.Float64("roe", roe),
					zap.Float64("dividend_growth", dividendGrowth))
			}
		}
	}

	confidence := "medium"
	if len(warnings) == 0 && dividendGrowth > 0 {
		confidence = "high"
	}
	if len(warnings) > 1 {
		confidence = "low"
	}

	m.logger.Info("DDM valuation completed",
		zap.Float64("dps", dps),
		zap.Float64("dividend_growth", dividendGrowth),
		zap.Float64("cost_of_equity", costOfEquity),
		zap.Float64("value_per_share", valuePerShare))

	return &ModelResult{
		IntrinsicValuePerShare: valuePerShare,
		EnterpriseValue:        enterpriseValue,
		EquityValue:            equityValue,
		ModelType:              "ddm",
		Warnings:               warnings,
		Confidence:             confidence,
	}, nil
}

// estimateDividendGrowth calculates the expected dividend growth rate.
// Priority: historical DPS CAGR > sustainable growth (ROE * retention) > growth estimate.
func (m *DDMModel) estimateDividendGrowth(input *ModelInput) float64 {
	// Try to calculate historical DPS growth from multi-year data
	recentYears := input.HistoricalData.GetRecentYears(5)
	if len(recentYears) >= 2 {
		// Find oldest and newest DPS values
		var oldestDPS, newestDPS float64
		for i := len(recentYears) - 1; i >= 0; i-- {
			if recentYears[i].DividendsPerShare > 0 {
				oldestDPS = recentYears[i].DividendsPerShare
				break
			}
		}
		if recentYears[0].DividendsPerShare > 0 {
			newestDPS = recentYears[0].DividendsPerShare
		}

		if oldestDPS > 0 && newestDPS > 0 && newestDPS != oldestDPS {
			years := float64(len(recentYears) - 1)
			if years > 0 {
				cagr := math.Pow(newestDPS/oldestDPS, 1.0/years) - 1
				// Cap to reasonable range for dividend growth
				if cagr > 0.15 {
					cagr = 0.15 // Max 15% dividend growth
				}
				if cagr < -0.05 {
					cagr = -0.05 // Min -5% (declining dividends)
				}
				return cagr
			}
		}
	}

	// Fallback: sustainable growth = ROE * retention ratio
	latest, _ := input.HistoricalData.GetLatestPeriod()
	if latest != nil && latest.StockholdersEquity > 0 && latest.NetIncome > 0 {
		roe := latest.NetIncome / latest.StockholdersEquity
		// Estimate retention ratio from payout ratio
		retentionRatio := 0.5 // default 50% retention
		if latest.DividendsPerShare > 0 && input.SharesOutstanding > 0 {
			eps := latest.NetIncome / input.SharesOutstanding
			if eps > 0 {
				payoutRatio := latest.DividendsPerShare / eps
				retentionRatio = 1 - payoutRatio
				if retentionRatio < 0 {
					retentionRatio = 0
				}
			}
		}
		sustainableGrowth := roe * retentionRatio
		if sustainableGrowth > 0 && sustainableGrowth < 0.15 {
			return sustainableGrowth
		}
	}

	// Final fallback: use the growth estimate's terminal rate
	if input.GrowthEstimate != nil {
		return input.GrowthEstimate.TerminalGrowthRate
	}

	return 0.03 // 3% default
}
