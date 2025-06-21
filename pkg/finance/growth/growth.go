package growth

import (
	"errors"
	"math"
)

// CalculationResult contains the growth rate and metadata about the calculation
type CalculationResult struct {
	GrowthRate  float64 `json:"growth_rate"`  // Calculated growth rate (CAGR)
	Method      string  `json:"method"`       // Calculation method used
	PeriodsUsed int     `json:"periods_used"` // Number of periods in calculation
	DataQuality string  `json:"data_quality"` // high, medium, low
	IsReliable  bool    `json:"is_reliable"`  // Whether result is statistically reliable

	// Intermediate calculations for transparency
	StartingValue     float64   `json:"starting_value"`                 // First period value
	EndingValue       float64   `json:"ending_value"`                   // Last period value
	YearOverYearRates []float64 `json:"year_over_year_rates,omitempty"` // Individual YoY growth rates
}

// CalculateCAGR computes Compound Annual Growth Rate from historical values
// CAGR = (Ending Value / Beginning Value)^(1/n) - 1
// where n is the number of periods
func CalculateCAGR(values []float64) (*CalculationResult, error) {
	if len(values) < 2 {
		return nil, errors.New("need at least 2 data points for CAGR calculation")
	}

	// Remove zero and negative values that would distort calculation
	cleanedValues := removeInvalidValues(values)
	if len(cleanedValues) < 2 {
		return nil, errors.New("insufficient positive values for growth calculation")
	}

	result := &CalculationResult{
		Method:        "CAGR",
		PeriodsUsed:   len(cleanedValues),
		StartingValue: cleanedValues[0],
		EndingValue:   cleanedValues[len(cleanedValues)-1],
	}

	// Calculate CAGR
	if result.StartingValue <= 0 {
		return nil, errors.New("starting value must be positive for CAGR calculation")
	}

	n := float64(len(cleanedValues) - 1) // Number of growth periods
	result.GrowthRate = math.Pow(result.EndingValue/result.StartingValue, 1/n) - 1

	// Calculate year-over-year rates for quality assessment
	result.YearOverYearRates = calculateYoYRates(cleanedValues)

	// Assess data quality and reliability
	result.DataQuality = assessDataQuality(result.PeriodsUsed, result.YearOverYearRates)
	result.IsReliable = isGrowthRateReliable(result.GrowthRate, result.YearOverYearRates)

	return result, nil
}

// CalculateAverageGrowth computes average of year-over-year growth rates
// More robust when there are large variations or outliers
func CalculateAverageGrowth(values []float64) (*CalculationResult, error) {
	if len(values) < 2 {
		return nil, errors.New("need at least 2 data points for average growth calculation")
	}

	cleanedValues := removeInvalidValues(values)
	if len(cleanedValues) < 2 {
		return nil, errors.New("insufficient positive values for growth calculation")
	}

	result := &CalculationResult{
		Method:        "Average YoY",
		PeriodsUsed:   len(cleanedValues),
		StartingValue: cleanedValues[0],
		EndingValue:   cleanedValues[len(cleanedValues)-1],
	}

	// Calculate year-over-year growth rates
	yoyRates := calculateYoYRates(cleanedValues)
	result.YearOverYearRates = yoyRates

	// Calculate average (arithmetic mean)
	sum := 0.0
	for _, rate := range yoyRates {
		sum += rate
	}
	result.GrowthRate = sum / float64(len(yoyRates))

	// Assess quality
	result.DataQuality = assessDataQuality(result.PeriodsUsed, result.YearOverYearRates)
	result.IsReliable = isGrowthRateReliable(result.GrowthRate, result.YearOverYearRates)

	return result, nil
}

// CalculateWeightedGrowth calculates growth rate giving more weight to recent years
func CalculateWeightedGrowth(values []float64) (*CalculationResult, error) {
	if len(values) < 2 {
		return nil, errors.New("need at least 2 data points for weighted growth calculation")
	}

	cleanedValues := removeInvalidValues(values)
	if len(cleanedValues) < 2 {
		return nil, errors.New("insufficient positive values for growth calculation")
	}

	result := &CalculationResult{
		Method:        "Weighted Average",
		PeriodsUsed:   len(cleanedValues),
		StartingValue: cleanedValues[0],
		EndingValue:   cleanedValues[len(cleanedValues)-1],
	}

	// Calculate year-over-year growth rates
	yoyRates := calculateYoYRates(cleanedValues)
	result.YearOverYearRates = yoyRates

	// Apply weights (more recent years get higher weights)
	weightedSum := 0.0
	totalWeight := 0.0

	for i, rate := range yoyRates {
		weight := float64(i + 1) // Linear weighting: 1, 2, 3, 4...
		weightedSum += rate * weight
		totalWeight += weight
	}

	result.GrowthRate = weightedSum / totalWeight

	// Assess quality
	result.DataQuality = assessDataQuality(result.PeriodsUsed, result.YearOverYearRates)
	result.IsReliable = isGrowthRateReliable(result.GrowthRate, result.YearOverYearRates)

	return result, nil
}

// CalculateBestGrowthRate automatically selects the most appropriate method
func CalculateBestGrowthRate(values []float64) (*CalculationResult, error) {
	if len(values) < 2 {
		return nil, errors.New("need at least 2 data points for growth calculation")
	}

	cleanedValues := removeInvalidValues(values)
	if len(cleanedValues) < 2 {
		return nil, errors.New("insufficient positive values for growth calculation")
	}

	// Try different methods
	cagrResult, err1 := CalculateCAGR(cleanedValues)
	avgResult, err2 := CalculateAverageGrowth(cleanedValues)

	// If either failed, return the successful one
	if err1 != nil && err2 == nil {
		return avgResult, nil
	}
	if err2 != nil && err1 == nil {
		return cagrResult, nil
	}
	if err1 != nil && err2 != nil {
		return nil, errors.New("unable to calculate growth rate with any method")
	}

	// Both succeeded - choose based on data characteristics
	yoyRates := calculateYoYRates(cleanedValues)
	volatility := calculateVolatility(yoyRates)

	// If high volatility, prefer average method
	if volatility > 0.5 { // More than 50% standard deviation
		avgResult.Method = "Average YoY (High Volatility)"
		return avgResult, nil
	}

	// If low volatility and sufficient data points, prefer CAGR
	if len(cleanedValues) >= 4 {
		cagrResult.Method = "CAGR (Stable Growth)"
		return cagrResult, nil
	}

	// Default to average for short time series
	avgResult.Method = "Average YoY (Short Series)"
	return avgResult, nil
}

// Helper functions

func removeInvalidValues(values []float64) []float64 {
	cleaned := make([]float64, 0, len(values))
	for _, v := range values {
		if v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) {
			cleaned = append(cleaned, v)
		}
	}
	return cleaned
}

func calculateYoYRates(values []float64) []float64 {
	if len(values) < 2 {
		return []float64{}
	}

	rates := make([]float64, len(values)-1)
	for i := 1; i < len(values); i++ {
		if values[i-1] > 0 {
			rates[i-1] = (values[i] - values[i-1]) / values[i-1]
		}
	}
	return rates
}

func calculateVolatility(rates []float64) float64 {
	if len(rates) < 2 {
		return 0
	}

	// Calculate mean
	sum := 0.0
	for _, rate := range rates {
		sum += rate
	}
	mean := sum / float64(len(rates))

	// Calculate variance
	sumSquares := 0.0
	for _, rate := range rates {
		diff := rate - mean
		sumSquares += diff * diff
	}
	variance := sumSquares / float64(len(rates)-1)

	return math.Sqrt(variance)
}

func assessDataQuality(periods int, yoyRates []float64) string {
	if periods >= 5 {
		volatility := calculateVolatility(yoyRates)
		if volatility < 0.2 {
			return "high"
		}
		if volatility < 0.5 {
			return "medium"
		}
		return "low"
	}

	if periods >= 3 {
		return "medium"
	}

	return "low"
}

func isGrowthRateReliable(growthRate float64, yoyRates []float64) bool {
	// Check for reasonable growth rate bounds
	if growthRate < -0.5 || growthRate > 1.0 { // Less than -50% or more than 100%
		return false
	}

	// Check for consistency in year-over-year rates
	if len(yoyRates) >= 3 {
		volatility := calculateVolatility(yoyRates)
		return volatility < 0.8 // Standard deviation less than 80%
	}

	return len(yoyRates) >= 2
}

// CapGrowthRate applies reasonable bounds to growth rate
func CapGrowthRate(growthRate float64) float64 {
	const maxGrowthRate = 0.5  // 50% max growth
	const minGrowthRate = -0.3 // -30% max decline

	if growthRate > maxGrowthRate {
		return maxGrowthRate
	}
	if growthRate < minGrowthRate {
		return minGrowthRate
	}
	return growthRate
}

// CalculateTerminalGrowthRate derives appropriate terminal growth rate
// Terminal growth should be conservative (typically 2-3%)
func CalculateTerminalGrowthRate(historicalGrowth float64) float64 {
	const maxTerminalGrowth = 0.03      // 3% maximum
	const defaultTerminalGrowth = 0.025 // 2.5% default

	if historicalGrowth <= 0 {
		return defaultTerminalGrowth
	}

	// Terminal growth should be at most half of historical growth or 3%, whichever is lower
	terminalGrowth := math.Min(historicalGrowth*0.5, maxTerminalGrowth)

	// But at least 1% for viable businesses
	if terminalGrowth < 0.01 {
		terminalGrowth = 0.01
	}

	return terminalGrowth
}
