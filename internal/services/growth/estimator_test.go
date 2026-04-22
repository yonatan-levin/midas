package growth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	pkggrowth "github.com/midas/dcf-valuation-api/pkg/finance/growth"
)

func newTestEstimator() *Estimator {
	// nil calcEmitter — no calc traces in unit tests; they only fire in integration tests.
	return NewEstimator(DefaultEstimatorConfig(), zap.NewNop(), nil)
}

func TestEstimator_NoAnalystData_UsesHistorical(t *testing.T) {
	e := newTestEstimator()

	historical := &pkggrowth.CalculationResult{
		GrowthRate:  0.12,
		Method:      "CAGR",
		DataQuality: "high",
		IsReliable:  true,
	}

	result := e.EstimateGrowthRates(context.Background(), nil, historical, 0)

	assert.Equal(t, "historical_only", result.Source)
	assert.Equal(t, 0.12, result.HistoricalCAGR)
	assert.Equal(t, 7, len(result.ProjectedGrowthRates)) // 3 + 4 = 7 years

	// Stage 1 (years 1-3): should be historical CAGR (12%)
	for i := 0; i < 3; i++ {
		assert.InDelta(t, 0.12, result.ProjectedGrowthRates[i], 0.001,
			"Stage 1 year %d should use historical CAGR", i+1)
	}

	// Stage 2 (years 4-7): should fade from 12% toward 8%
	for i := 3; i < 7; i++ {
		assert.Less(t, result.ProjectedGrowthRates[i], 0.12+0.001,
			"Stage 2 year %d should be <= stage 1 rate", i+1)
		assert.Greater(t, result.ProjectedGrowthRates[i], 0.07,
			"Stage 2 year %d should not drop below fade target too fast", i+1)
	}

	// Terminal growth
	assert.InDelta(t, 0.03, result.TerminalGrowthRate, 0.001) // min(12%/2, 3%) = 3%
}

func TestEstimator_HighAnalystCoverage(t *testing.T) {
	e := newTestEstimator()

	analyst := &ports.YFinanceAnalystEstimates{
		EarningsGrowth5Year:        0.25, // 25% analyst consensus
		NumberOfAnalysts:           15,
		RevenueEstimateCurrentYear: 100000,
		RevenueEstimateNextYear:    130000,
	}
	historical := &pkggrowth.CalculationResult{
		GrowthRate:  0.10,
		Method:      "CAGR",
		DataQuality: "high",
		IsReliable:  true,
	}

	result := e.EstimateGrowthRates(context.Background(), analyst, historical, 0)

	assert.Equal(t, "analyst_blend", result.Source)
	assert.Equal(t, "high", result.Confidence)
	assert.Equal(t, 15, result.NumberOfAnalysts)

	// With 15 analysts: 80% analyst (25%) + 20% historical (10%) = 22%
	expectedBlend := 0.80*0.25 + 0.20*0.10
	assert.InDelta(t, expectedBlend, result.ProjectedGrowthRates[0], 0.001)
}

func TestEstimator_LowAnalystCoverage(t *testing.T) {
	e := newTestEstimator()

	analyst := &ports.YFinanceAnalystEstimates{
		EarningsGrowth5Year: 0.30,
		NumberOfAnalysts:    2,
	}
	historical := &pkggrowth.CalculationResult{
		GrowthRate: 0.10,
	}

	result := e.EstimateGrowthRates(context.Background(), analyst, historical, 0)

	assert.Equal(t, "low", result.Confidence)
	// With 2 analysts: 40% analyst (30%) + 60% historical (10%) = 18%
	expectedBlend := 0.40*0.30 + 0.60*0.10
	assert.InDelta(t, expectedBlend, result.ProjectedGrowthRates[0], 0.001)
}

func TestEstimator_MediumAnalystCoverage(t *testing.T) {
	e := newTestEstimator()

	analyst := &ports.YFinanceAnalystEstimates{
		EarningsGrowth5Year: 0.20,
		NumberOfAnalysts:    5,
	}
	historical := &pkggrowth.CalculationResult{
		GrowthRate: 0.10,
	}

	result := e.EstimateGrowthRates(context.Background(), analyst, historical, 0)

	assert.Equal(t, "medium", result.Confidence)
	// With 5 analysts: 60% analyst (20%) + 40% historical (10%) = 16%
	expectedBlend := 0.60*0.20 + 0.40*0.10
	assert.InDelta(t, expectedBlend, result.ProjectedGrowthRates[0], 0.001)
}

func TestEstimator_ROICSustainabilityCeiling(t *testing.T) {
	e := newTestEstimator()

	historical := &pkggrowth.CalculationResult{
		GrowthRate: 0.30, // 30% historical
	}

	// ROIC-sustainable growth is only 10%
	result := e.EstimateGrowthRates(context.Background(), nil, historical, 0.10)

	// Stage 1 rate should be blended down: (30% + 10%) / 2 = 20%
	assert.InDelta(t, 0.20, result.ProjectedGrowthRates[0], 0.001)
	assert.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "exceeds ROIC-sustainable growth")
}

func TestEstimator_GrowthRateCapping(t *testing.T) {
	e := newTestEstimator()

	historical := &pkggrowth.CalculationResult{
		GrowthRate: 0.80, // 80% — exceeds max of 50%
	}

	result := e.EstimateGrowthRates(context.Background(), nil, historical, 0)

	// Should be capped to max 50%
	assert.InDelta(t, 0.50, result.ProjectedGrowthRates[0], 0.001)
}

func TestEstimator_NegativeGrowth(t *testing.T) {
	e := newTestEstimator()

	historical := &pkggrowth.CalculationResult{
		GrowthRate: -0.10, // declining company
	}

	result := e.EstimateGrowthRates(context.Background(), nil, historical, 0)

	// Stage 1 uses -10%
	assert.InDelta(t, -0.10, result.ProjectedGrowthRates[0], 0.001)

	// Terminal growth should use floor (2%)
	assert.InDelta(t, 0.02, result.TerminalGrowthRate, 0.001)
}

func TestEstimator_DivergenceWarning(t *testing.T) {
	e := newTestEstimator()

	analyst := &ports.YFinanceAnalystEstimates{
		EarningsGrowth5Year: 0.40, // 40% analyst
		NumberOfAnalysts:    10,
	}
	historical := &pkggrowth.CalculationResult{
		GrowthRate: 0.05, // 5% historical — 8x divergence
	}

	result := e.EstimateGrowthRates(context.Background(), analyst, historical, 0)

	// Should have a divergence warning
	hasWarning := false
	for _, w := range result.Warnings {
		if len(w) > 0 {
			hasWarning = true
		}
	}
	assert.True(t, hasWarning, "Should warn about large analyst/historical divergence")
}

func TestEstimator_ThreeStageDecay(t *testing.T) {
	e := newTestEstimator()

	historical := &pkggrowth.CalculationResult{
		GrowthRate: 0.20, // 20% growth
	}

	result := e.EstimateGrowthRates(context.Background(), nil, historical, 0)

	// Verify monotonic decay through stages
	for i := 1; i < len(result.ProjectedGrowthRates); i++ {
		assert.LessOrEqual(t, result.ProjectedGrowthRates[i], result.ProjectedGrowthRates[i-1]+0.001,
			"Growth should not increase from year %d to year %d", i, i+1)
	}
}

func TestEstimator_AnalystGrowthFromRevenueEstimates(t *testing.T) {
	e := newTestEstimator()

	// No 5-year growth, but has revenue estimates
	analyst := &ports.YFinanceAnalystEstimates{
		EarningsGrowth5Year:        0, // not available
		NumberOfAnalysts:           8,
		RevenueEstimateCurrentYear: 100000,
		RevenueEstimateNextYear:    120000, // implies 20% growth
	}
	historical := &pkggrowth.CalculationResult{
		GrowthRate: 0.10,
	}

	result := e.EstimateGrowthRates(context.Background(), analyst, historical, 0)

	// Analyst growth derived from revenue: (120000-100000)/100000 = 0.20
	// With 8 analysts: 60% × 20% + 40% × 10% = 16%
	expectedBlend := 0.60*0.20 + 0.40*0.10
	assert.InDelta(t, expectedBlend, result.ProjectedGrowthRates[0], 0.001)
}

func TestEstimator_NilHistorical(t *testing.T) {
	e := newTestEstimator()

	result := e.EstimateGrowthRates(context.Background(), nil, nil, 0)

	assert.Equal(t, "historical_only", result.Source)
	assert.Equal(t, 0.0, result.HistoricalCAGR)
	// All Stage 1 rates should be 0
	for i := 0; i < 3; i++ {
		assert.Equal(t, 0.0, result.ProjectedGrowthRates[i])
	}
}

func TestGrowthEstimate_SummaryGrowthRate(t *testing.T) {
	tests := []struct {
		name     string
		rates    []float64
		expected float64
	}{
		{
			name:     "uniform 10% over 5 years",
			rates:    []float64{0.10, 0.10, 0.10, 0.10, 0.10},
			expected: 0.10,
		},
		{
			name:     "declining growth",
			rates:    []float64{0.20, 0.15, 0.10, 0.08, 0.05},
			expected: 0.1154, // CAGR of the compound
		},
		{
			name:     "empty rates",
			rates:    []float64{},
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &entities.GrowthEstimate{ProjectedGrowthRates: tt.rates}
			assert.InDelta(t, tt.expected, g.SummaryGrowthRate(), 0.005)
		})
	}
}
