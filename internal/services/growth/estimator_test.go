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

	result := e.EstimateGrowthRates(context.Background(), "TEST", nil, historical, 0)

	assert.Equal(t, "historical_only", result.Source)
	assert.Equal(t, 0.12, result.HistoricalCAGR)
	assert.Equal(t, 7, len(result.ProjectedGrowthRates)) // 3 + 4 = 7 years

	// G-1: no analyst coverage → 0/1 weights.
	assert.Equal(t, 0.0, result.AnalystWeight, "no analyst coverage → AnalystWeight=0")
	assert.Equal(t, 1.0, result.HistoricalWeight, "no analyst coverage → HistoricalWeight=1")

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

	result := e.EstimateGrowthRates(context.Background(), "TEST", analyst, historical, 0)

	assert.Equal(t, "analyst_blend", result.Source)
	assert.Equal(t, "high", result.Confidence)
	assert.Equal(t, 15, result.NumberOfAnalysts)

	// With 15 analysts: 80% analyst (25%) + 20% historical (10%) = 22%
	expectedBlend := 0.80*0.25 + 0.20*0.10
	assert.InDelta(t, expectedBlend, result.ProjectedGrowthRates[0], 0.001)

	// G-1: high coverage bucket (n>=10) uses 0.80/0.20.
	assert.InDelta(t, 0.80, result.AnalystWeight, 1e-9)
	assert.InDelta(t, 0.20, result.HistoricalWeight, 1e-9)
	assert.InDelta(t, 1.0, result.AnalystWeight+result.HistoricalWeight, 1e-9, "weights must sum to 1")
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

	result := e.EstimateGrowthRates(context.Background(), "TEST", analyst, historical, 0)

	assert.Equal(t, "low", result.Confidence)
	// With 2 analysts: 40% analyst (30%) + 60% historical (10%) = 18%
	expectedBlend := 0.40*0.30 + 0.60*0.10
	assert.InDelta(t, expectedBlend, result.ProjectedGrowthRates[0], 0.001)

	// G-1: low coverage bucket (1-2 analysts) uses 0.40/0.60.
	assert.InDelta(t, 0.40, result.AnalystWeight, 1e-9)
	assert.InDelta(t, 0.60, result.HistoricalWeight, 1e-9)
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

	result := e.EstimateGrowthRates(context.Background(), "TEST", analyst, historical, 0)

	assert.Equal(t, "medium", result.Confidence)
	// With 5 analysts: 60% analyst (20%) + 40% historical (10%) = 16%
	expectedBlend := 0.60*0.20 + 0.40*0.10
	assert.InDelta(t, expectedBlend, result.ProjectedGrowthRates[0], 0.001)

	// G-1: medium coverage bucket (3<=n<10) uses 0.60/0.40.
	assert.InDelta(t, 0.60, result.AnalystWeight, 1e-9)
	assert.InDelta(t, 0.40, result.HistoricalWeight, 1e-9)
}

func TestEstimator_ROICSustainabilityCeiling(t *testing.T) {
	e := newTestEstimator()

	historical := &pkggrowth.CalculationResult{
		GrowthRate: 0.30, // 30% historical
	}

	// ROIC-sustainable growth is only 10%
	result := e.EstimateGrowthRates(context.Background(), "TEST", nil, historical, 0.10)

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

	result := e.EstimateGrowthRates(context.Background(), "TEST", nil, historical, 0)

	// Should be capped to max 50%
	assert.InDelta(t, 0.50, result.ProjectedGrowthRates[0], 0.001)
}

func TestEstimator_NegativeGrowth(t *testing.T) {
	e := newTestEstimator()

	historical := &pkggrowth.CalculationResult{
		GrowthRate: -0.10, // declining company
	}

	result := e.EstimateGrowthRates(context.Background(), "TEST", nil, historical, 0)

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

	result := e.EstimateGrowthRates(context.Background(), "TEST", analyst, historical, 0)

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

	result := e.EstimateGrowthRates(context.Background(), "TEST", nil, historical, 0)

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

	result := e.EstimateGrowthRates(context.Background(), "TEST", analyst, historical, 0)

	// Analyst growth derived from revenue: (120000-100000)/100000 = 0.20
	// With 8 analysts: 60% × 20% + 40% × 10% = 16%
	expectedBlend := 0.60*0.20 + 0.40*0.10
	assert.InDelta(t, expectedBlend, result.ProjectedGrowthRates[0], 0.001)
}

func TestEstimator_NilHistorical(t *testing.T) {
	e := newTestEstimator()

	result := e.EstimateGrowthRates(context.Background(), "TEST", nil, nil, 0)

	assert.Equal(t, "historical_only", result.Source)
	assert.Equal(t, 0.0, result.HistoricalCAGR)
	// All Stage 1 rates should be 0
	for i := 0; i < 3; i++ {
		assert.Equal(t, 0.0, result.ProjectedGrowthRates[i])
	}
}

// TestEstimator_ProducesAtLeast10Stages_WhenConfigured verifies that the
// estimator can produce a 10+-element ProjectedGrowthRates slice when the
// config requests a longer horizon. Required by Tier 2 P2's
// hypergrowth_profitable archetype which sets HorizonYears=10. Spec §6.2.
//
// Implementation note: the estimator stages are Stage1 (constant) + Stage2
// (linear fade) + optional Stage3 (continued fade toward terminal). With
// Stage1=3, Stage2=4, Stage3=3 the slice length is 10. Default config
// keeps Stage3=0 so existing behavior is byte-identical.
func TestEstimator_ProducesAtLeast10Stages_WhenConfigured(t *testing.T) {
	cfg := DefaultEstimatorConfig()
	cfg.Stage3Years = 3 // extends the existing 3+4=7 to 3+4+3=10

	est := NewEstimator(cfg, zap.NewNop(), nil)
	historical := &pkggrowth.CalculationResult{
		GrowthRate:  0.30,
		Method:      "CAGR",
		DataQuality: "high",
		IsReliable:  true,
	}
	result := est.EstimateGrowthRates(
		context.Background(), "TEST",
		nil, // no analyst data
		historical,
		0.10, // sustainable growth
	)
	assert.GreaterOrEqual(t, len(result.ProjectedGrowthRates), 10,
		"hypergrowth_profitable archetype needs at least 10 growth stages")

	// Stage 3 years should continue the fade (monotonic non-increasing).
	for i := 1; i < len(result.ProjectedGrowthRates); i++ {
		assert.LessOrEqual(t, result.ProjectedGrowthRates[i], result.ProjectedGrowthRates[i-1]+1e-9,
			"growth should not increase from year %d to year %d", i, i+1)
	}
}

// TestEstimator_DefaultConfig_StillProduces7Stages pins the existing
// behavior contract: when Stage3Years is unset (=0), the slice length
// stays at 7. Guards against accidental default changes that would
// drift production output.
func TestEstimator_DefaultConfig_StillProduces7Stages(t *testing.T) {
	cfg := DefaultEstimatorConfig()
	assert.Equal(t, 0, cfg.Stage3Years, "default Stage3Years must be 0 (no extension)")

	est := NewEstimator(cfg, zap.NewNop(), nil)
	historical := &pkggrowth.CalculationResult{GrowthRate: 0.12}
	result := est.EstimateGrowthRates(context.Background(), "TEST", nil, historical, 0)

	assert.Equal(t, 7, len(result.ProjectedGrowthRates),
		"default config must still produce 3+4=7 stages (backward compatibility)")
}

// TestEstimator_BlendWeights_AllBuckets pins the G-1 contract: every
// analyst-count bucket populates AnalystWeight + HistoricalWeight on
// the returned GrowthEstimate, and the two always sum to 1.0. The
// narrate `growth.estimated` phase reads these fields directly (see
// internal/services/valuation/service.go around the call site), so a
// regression here silently demotes the operator signal back to the
// coarse 0.5/0.5 era this tracker closed. Filed as
// docs/reviewer/G1-growth-blend-weights-coarse.md.
func TestEstimator_BlendWeights_AllBuckets(t *testing.T) {
	cases := []struct {
		name             string
		analystCount     int
		wantAnalyst      float64
		wantHistorical   float64
		wantConfidence   string
		analystGrowth    float64
		historicalGrowth float64
	}{
		{
			name:             "no analyst data → 0/1",
			analystCount:     0, // sentinel; below switches to nil path
			wantAnalyst:      0.0,
			wantHistorical:   1.0,
			wantConfidence:   "medium", // historical-only assessHistoricalConfidence
			analystGrowth:    0.0,
			historicalGrowth: 0.10,
		},
		{
			name:             "low coverage (1-2) → 0.40/0.60",
			analystCount:     2,
			wantAnalyst:      0.40,
			wantHistorical:   0.60,
			wantConfidence:   "low",
			analystGrowth:    0.20,
			historicalGrowth: 0.10,
		},
		{
			name:             "medium coverage (3-9) → 0.60/0.40",
			analystCount:     5,
			wantAnalyst:      0.60,
			wantHistorical:   0.40,
			wantConfidence:   "medium",
			analystGrowth:    0.20,
			historicalGrowth: 0.10,
		},
		{
			name:             "high coverage (>=10) → 0.80/0.20",
			analystCount:     15,
			wantAnalyst:      0.80,
			wantHistorical:   0.20,
			wantConfidence:   "high",
			analystGrowth:    0.20,
			historicalGrowth: 0.10,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEstimator()
			historical := &pkggrowth.CalculationResult{
				GrowthRate: tc.historicalGrowth,
				IsReliable: true,
			}
			var analyst *ports.YFinanceAnalystEstimates
			if tc.analystCount > 0 {
				analyst = &ports.YFinanceAnalystEstimates{
					EarningsGrowth5Year: tc.analystGrowth,
					NumberOfAnalysts:    tc.analystCount,
				}
			}

			result := e.EstimateGrowthRates(context.Background(), "TEST", analyst, historical, 0)

			assert.InDelta(t, tc.wantAnalyst, result.AnalystWeight, 1e-9,
				"AnalystWeight for %s", tc.name)
			assert.InDelta(t, tc.wantHistorical, result.HistoricalWeight, 1e-9,
				"HistoricalWeight for %s", tc.name)
			assert.InDelta(t, 1.0, result.AnalystWeight+result.HistoricalWeight, 1e-9,
				"weights must sum to 1.0 (G-1 invariant)")
			if tc.analystCount > 0 {
				assert.Equal(t, tc.wantConfidence, result.Confidence)
			}
		})
	}
}

// TestEstimator_BlendWeights_AnalystPresentButZeroAnalysts pins the
// edge case where YFinanceAnalystEstimates is non-nil but its
// NumberOfAnalysts == 0 (Yahoo returned an empty earningsTrend
// payload). The estimator must treat this identically to "no analyst
// coverage" — Source=historical_only and weights 0/1.
func TestEstimator_BlendWeights_AnalystPresentButZeroAnalysts(t *testing.T) {
	e := newTestEstimator()
	historical := &pkggrowth.CalculationResult{GrowthRate: 0.10, IsReliable: true}
	analyst := &ports.YFinanceAnalystEstimates{
		EarningsGrowth5Year: 0.25, // present but no analysts behind it
		NumberOfAnalysts:    0,
	}

	result := e.EstimateGrowthRates(context.Background(), "TEST", analyst, historical, 0)

	assert.Equal(t, "historical_only", result.Source)
	assert.Equal(t, 0.0, result.AnalystWeight)
	assert.Equal(t, 1.0, result.HistoricalWeight)
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
