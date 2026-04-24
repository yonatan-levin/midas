package growth

import (
	"context"
	"fmt"
	"math"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/observability/calclog"
	pkggrowth "github.com/midas/dcf-valuation-api/pkg/finance/growth"
)

// EstimatorConfig holds configurable parameters for growth estimation.
type EstimatorConfig struct {
	MaxGrowthRate       float64 // Upper bound for any single-year growth rate
	MinGrowthRate       float64 // Lower bound for any single-year growth rate
	FadeTargetRate      float64 // Rate to fade toward in Stage 2 (default 0.08 = 8%)
	Stage1Years         int     // Number of years in high-growth stage (default 3)
	Stage2Years         int     // Number of years in fade stage (default 4)
	TerminalGrowthMax   float64 // Maximum terminal growth rate (default 0.03)
	TerminalGrowthFloor float64 // Minimum terminal growth rate (default 0.02)
	DefaultPayoutRatio  float64 // Assumed payout ratio for ROIC sustainability (default 0.3 = 30%)
}

// DefaultEstimatorConfig returns sensible defaults.
func DefaultEstimatorConfig() EstimatorConfig {
	return EstimatorConfig{
		MaxGrowthRate:       0.5,
		MinGrowthRate:       -0.3,
		FadeTargetRate:      0.08,
		Stage1Years:         3,
		Stage2Years:         4,
		TerminalGrowthMax:   0.03,
		TerminalGrowthFloor: 0.02,
		DefaultPayoutRatio:  0.3,
	}
}

// Estimator blends analyst consensus with historical growth to produce
// per-year projected growth rates for the DCF model.
type Estimator struct {
	config      EstimatorConfig
	logger      *zap.Logger
	calcEmitter *calclog.Emitter // emits stage-5 "growth" trace per valuation
}

// NewEstimator creates a GrowthEstimator with the given config.
// calcEmitter may be nil (nop path) — no panic occurs.
func NewEstimator(cfg EstimatorConfig, logger *zap.Logger, calcEmitter *calclog.Emitter) *Estimator {
	return &Estimator{config: cfg, logger: logger, calcEmitter: calcEmitter}
}

// Config returns the estimator's configuration.
func (e *Estimator) Config() EstimatorConfig {
	return e.config
}

// EstimateGrowthRates produces a GrowthEstimate with per-year growth rates.
//
// ctx is used to emit stage-5 "growth" calc trace so it can be correlated with
// the originating HTTP request via logctx.
//
// ticker is emitted on the calc trace so the "growth" entry is self-describing
// (addresses M-1a); callers pass the valuation target's ticker.
//
// analystData may be nil (no analyst coverage).
// historicalGrowth is from HistoricalFinancialData.CalculateAverageGrowthRate().
// sustainableGrowth is from CalculateSustainableGrowth (ROIC × reinvestment).
func (e *Estimator) EstimateGrowthRates(
	ctx context.Context,
	ticker string,
	analystData *ports.YFinanceAnalystEstimates,
	historicalGrowth *pkggrowth.CalculationResult,
	sustainableGrowth float64,
) *entities.GrowthEstimate {
	estimate := &entities.GrowthEstimate{}

	// Extract historical CAGR
	historicalCAGR := 0.0
	if historicalGrowth != nil {
		historicalCAGR = historicalGrowth.GrowthRate
	}
	estimate.HistoricalCAGR = historicalCAGR
	estimate.SustainableGrowthRate = sustainableGrowth

	// Determine blended Stage 1 growth rate
	stage1Rate := e.blendGrowthRate(analystData, historicalCAGR, estimate)

	// Apply ROIC sustainability ceiling
	if sustainableGrowth > 0 && stage1Rate > sustainableGrowth {
		estimate.Warnings = append(estimate.Warnings,
			fmt.Sprintf("Stage 1 growth (%.1f%%) exceeds ROIC-sustainable growth (%.1f%%); blending downward",
				stage1Rate*100, sustainableGrowth*100))
		// Blend toward sustainable rather than hard cap
		stage1Rate = (stage1Rate + sustainableGrowth) / 2
	}

	// Cap to config bounds
	stage1Rate = pkggrowth.CapGrowthRateWithBounds(stage1Rate, e.config.MinGrowthRate, e.config.MaxGrowthRate)

	// Build per-year growth rates using three-stage model
	totalYears := e.config.Stage1Years + e.config.Stage2Years
	rates := make([]float64, totalYears)

	// Stage 1: constant high-growth rate
	for i := 0; i < e.config.Stage1Years && i < totalYears; i++ {
		rates[i] = stage1Rate
	}

	// Stage 2: linear fade from Stage 1 exit rate toward fade target
	fadeStart := stage1Rate
	fadeEnd := math.Min(e.config.FadeTargetRate, stage1Rate) // don't fade UP if already below target
	for i := 0; i < e.config.Stage2Years; i++ {
		yearIdx := e.config.Stage1Years + i
		if yearIdx >= totalYears {
			break
		}
		// Linear interpolation: fade from fadeStart to fadeEnd over Stage2Years
		t := float64(i+1) / float64(e.config.Stage2Years+1)
		rates[yearIdx] = fadeStart + t*(fadeEnd-fadeStart)
		rates[yearIdx] = pkggrowth.CapGrowthRateWithBounds(rates[yearIdx], e.config.MinGrowthRate, e.config.MaxGrowthRate)
	}

	estimate.ProjectedGrowthRates = rates

	// Terminal growth rate
	terminalGrowth := math.Min(historicalCAGR*0.5, e.config.TerminalGrowthMax)
	if terminalGrowth <= 0 {
		terminalGrowth = e.config.TerminalGrowthFloor
	}
	estimate.TerminalGrowthRate = terminalGrowth

	e.logger.Info("Growth estimation complete",
		zap.String("source", estimate.Source),
		zap.String("confidence", estimate.Confidence),
		zap.Float64("stage1_rate", stage1Rate),
		zap.Float64("terminal_rate", estimate.TerminalGrowthRate),
		zap.Int("projection_years", totalYears))

	// Stage 5 — "growth" calc trace: emit blended growth rates and their provenance so
	// operators can audit how analyst vs. historical data influenced the projection.
	if e.calcEmitter != nil {
		e.calcEmitter.Emit(ctx, "growth",
			zap.String("ticker", ticker),
			zap.String("source", estimate.Source),
			zap.Float64s("growth_rates", estimate.ProjectedGrowthRates),
			zap.Float64("roic_ceiling", sustainableGrowth),
			zap.String("sustainability", estimate.Confidence),
		)
	}

	return estimate
}

// blendGrowthRate combines analyst and historical growth using confidence-weighted blending.
func (e *Estimator) blendGrowthRate(
	analystData *ports.YFinanceAnalystEstimates,
	historicalCAGR float64,
	estimate *entities.GrowthEstimate,
) float64 {
	if analystData == nil || analystData.NumberOfAnalysts == 0 {
		// No analyst coverage — use historical only
		estimate.Source = "historical_only"
		estimate.Confidence = e.assessHistoricalConfidence(historicalCAGR)
		estimate.Method = "100% historical CAGR (no analyst coverage)"
		return historicalCAGR
	}

	// Populate analyst data on the estimate
	estimate.AnalystRevenueGrowthY1 = analystData.RevenueEstimateCurrentYear
	estimate.AnalystRevenueGrowthY2 = analystData.RevenueEstimateNextYear
	estimate.AnalystEarningsGrowth5Y = analystData.EarningsGrowth5Year
	estimate.NumberOfAnalysts = analystData.NumberOfAnalysts

	// Derive analyst growth rate from available data
	// Prefer 5-year earnings growth if available, else derive from revenue estimates
	analystGrowthRate := analystData.EarningsGrowth5Year
	if analystGrowthRate == 0 && analystData.RevenueEstimateCurrentYear > 0 && analystData.RevenueEstimateNextYear > 0 {
		// Approximate growth from Y1 to Y2 revenue estimates
		analystGrowthRate = (analystData.RevenueEstimateNextYear - analystData.RevenueEstimateCurrentYear) / analystData.RevenueEstimateCurrentYear
	}

	// Determine blending weights based on analyst count
	var analystWeight, historicalWeight float64
	n := analystData.NumberOfAnalysts

	switch {
	case n >= 10:
		analystWeight, historicalWeight = 0.80, 0.20
		estimate.Confidence = "high"
	case n >= 3:
		analystWeight, historicalWeight = 0.60, 0.40
		estimate.Confidence = "medium"
	default: // 1-2 analysts
		analystWeight, historicalWeight = 0.40, 0.60
		estimate.Confidence = "low"
	}

	blended := analystWeight*analystGrowthRate + historicalWeight*historicalCAGR

	estimate.Source = "analyst_blend"
	estimate.Method = fmt.Sprintf("%.0f%% analyst (%d analysts) + %.0f%% historical CAGR",
		analystWeight*100, n, historicalWeight*100)

	// Divergence check
	if historicalCAGR != 0 {
		divergence := math.Abs(analystGrowthRate-historicalCAGR) / math.Abs(historicalCAGR)
		if divergence > 1.0 { // >2x divergence (100% difference)
			estimate.Warnings = append(estimate.Warnings,
				fmt.Sprintf("Analyst consensus (%.1f%%) diverges significantly from historical CAGR (%.1f%%)",
					analystGrowthRate*100, historicalCAGR*100))
		}
	}

	return blended
}

// assessHistoricalConfidence returns confidence level based on historical growth quality.
func (e *Estimator) assessHistoricalConfidence(historicalCAGR float64) string {
	absGrowth := math.Abs(historicalCAGR)
	if absGrowth > 0.5 {
		return "low" // Extreme historical growth is unreliable as predictor
	}
	if absGrowth > 0.2 {
		return "medium"
	}
	return "medium" // Even stable historical isn't "high" without analyst validation
}
