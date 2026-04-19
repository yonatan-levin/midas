package models

import (
	"context"
	"strings"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// ValuationModel defines the interface for industry-specific valuation models.
// Each model implements a different valuation methodology (DCF, DDM, FFO, Revenue Multiple).
// Routing is performed by ModelRouter.SelectModel based on industry + financials,
// not by self-declaration from the models themselves.
type ValuationModel interface {
	// Calculate performs the valuation using this model's methodology.
	Calculate(ctx context.Context, input *ModelInput) (*ModelResult, error)

	// ModelType returns a string identifier for this model (e.g., "multi_stage_dcf", "ddm").
	ModelType() string
}

// ModelInput contains all data needed by any valuation model.
// Pre-computed values (WACC, cost of equity) are included so models
// don't need to recalculate them.
type ModelInput struct {
	HistoricalData *entities.HistoricalFinancialData
	MarketData     *entities.MarketData
	MacroData      *entities.MacroData
	GrowthEstimate *entities.GrowthEstimate
	Industry       string

	// Pre-computed financial metrics
	WACC         float64
	CostOfEquity float64
	TaxRate      float64

	// Share count for per-share calculations (diluted preferred)
	SharesOutstanding float64

	// Balance sheet items for equity bridge
	InterestBearingDebt    float64
	CashAndCashEquivalents float64
}

// ModelResult contains the standardized output from any valuation model.
type ModelResult struct {
	IntrinsicValuePerShare float64   `json:"intrinsic_value_per_share"`
	EnterpriseValue        float64   `json:"enterprise_value"`
	EquityValue            float64   `json:"equity_value"`
	ModelType              string    `json:"model_type"`
	Warnings               []string  `json:"warnings,omitempty"`
	Confidence             string    `json:"confidence"` // "high", "medium", "low"
	Projections            []float64 `json:"projections,omitempty"`
}

// ModelRouter selects the appropriate valuation model based on industry classification
// and financial characteristics of the company.
type ModelRouter struct {
	models []ValuationModel
	logger *zap.Logger
}

// NewModelRouter creates a ModelRouter with the given set of models.
// Models are checked in order, so register them from most specific to most general.
func NewModelRouter(models []ValuationModel, logger *zap.Logger) *ModelRouter {
	return &ModelRouter{
		models: models,
		logger: logger,
	}
}

// SelectModel determines the best valuation model for the given company.
//
// Selection logic (in priority order):
//  1. Financial industry (FIN prefix) -> DDM model
//  2. REIT industry -> FFO model
//  3. Non-positive operating income -> Revenue Multiple model
//  4. Default -> Multi-stage DCF model
//
// If the selected model does not support the industry, falls back to DCF.
func (r *ModelRouter) SelectModel(industry string, financials *entities.FinancialData) ValuationModel {
	upperIndustry := strings.ToUpper(industry)

	// Rule 1: Financial companies use Dividend Discount Model
	if strings.HasPrefix(upperIndustry, "FIN") {
		if model := r.findModel("ddm"); model != nil {
			r.logger.Info("Selected DDM model for financial company",
				zap.String("industry", industry))
			return model
		}
	}

	// Rule 2: REITs use FFO model
	// Match both "REIT" and the config's "RESTATE" (Real Estate) code
	if upperIndustry == "REIT" || upperIndustry == "RESTATE" {
		if model := r.findModel("ffo"); model != nil {
			r.logger.Info("Selected FFO model for REIT",
				zap.String("industry", industry))
			return model
		}
	}

	// Rule 3: Pre-revenue or negative OI companies use Revenue Multiple
	if financials != nil {
		baseOI := financials.NormalizedOperatingIncome
		if baseOI <= 0 {
			baseOI = financials.OperatingIncome
		}
		if baseOI <= 0 {
			if model := r.findModel("revenue_multiple"); model != nil {
				r.logger.Info("Selected Revenue Multiple model for negative OI company",
					zap.String("industry", industry),
					zap.Float64("operating_income", financials.OperatingIncome))
				return model
			}
		}
	}

	// Rule 4: Default to multi-stage DCF
	if model := r.findModel("multi_stage_dcf"); model != nil {
		return model
	}

	// Absolute fallback: return the first available model
	if len(r.models) > 0 {
		r.logger.Warn("No DCF model found, using first available model",
			zap.String("model_type", r.models[0].ModelType()))
		return r.models[0]
	}

	return nil
}

// findModel returns the first model with the given type identifier.
func (r *ModelRouter) findModel(modelType string) ValuationModel {
	for _, m := range r.models {
		if m.ModelType() == modelType {
			return m
		}
	}
	return nil
}
