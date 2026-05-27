package models

import (
	"context"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/observability/calclog"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/cleaneddata"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
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

	// LatestRestatedView is the DC-1 Phase 4 (C-3) Restated() view of the
	// latest period. The FFO model reads its OperatingIncome for the NAV NOI
	// proxy so the cross-check reflects restated earnings. May be nil on
	// test/no-cleaner paths; consumers MUST nil-check and fall back to the
	// HistoricalData.GetLatestPeriod() entity read. Not Restater-touched for
	// REIT tickers today (zero numeric drift); migrated for coherence.
	LatestRestatedView *cleaneddata.FinancialDataView

	// Now is the wall-clock seam used by consumers that need a current
	// timestamp (e.g., RevenueMultipleModel's RM-1.A staleness check).
	// Service populates this from its Clock binding so replay
	// determinism is preserved (manifest-pinned clock flows through to
	// the staleness check). When nil, consumers fall back to time.Now.
	Now func() time.Time

	// Profile is the resolved AssumptionProfile from upstream resolution
	// (service.go::performValuation, Tier 2 P0b). Carries calibration
	// values (horizon, caps, terminal method, payout path) for downstream
	// model consumption. May be nil only in defensive/test paths (no
	// registry wired); models MUST handle nil by falling through to legacy
	// behavior — P0b ships every model with nil-safe access since the
	// per-model wiring lands in P1/P2/P3/P4. Spec §2.3, §3.1.
	Profile *profile.ResolvedProfile
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

	// Tier 2 P0b additive fields. All omitempty — when zero-valued (legacy
	// path) they are omitted from JSON, preserving byte equality with pre-
	// Tier-2 responses on the legacy DDM bit-for-bit path. Populated by
	// P1/P3/P4 (trailing/forward DDM blending, horizon selection, terminal
	// multiple selection). Declared here so the schema is stable from P0b.
	TrailingValue    float64 `json:"trailing_value,omitempty"`
	ForwardValue     float64 `json:"forward_value,omitempty"`
	HorizonSelected  int     `json:"horizon_selected,omitempty"`
	TerminalMultiple float64 `json:"terminal_multiple,omitempty"`
}

// ModelRouter selects the appropriate valuation model based on industry classification
// and financial characteristics of the company.
type ModelRouter struct {
	models      []ValuationModel
	logger      *zap.Logger
	calcEmitter *calclog.Emitter // emits stage-4 "model_selection" trace per valuation
}

// NewModelRouter creates a ModelRouter with the given set of models.
// Models are checked in order, so register them from most specific to most general.
// calcEmitter may be nil (nop path) — no panic occurs.
func NewModelRouter(models []ValuationModel, logger *zap.Logger, calcEmitter *calclog.Emitter) *ModelRouter {
	return &ModelRouter{
		models:      models,
		logger:      logger,
		calcEmitter: calcEmitter,
	}
}

// SelectModel determines the best valuation model for the given company.
//
// ctx is used to emit the stage-4 "model_selection" calc trace so it can be
// correlated with the originating HTTP request via logctx.
//
// ticker is emitted on the calc trace so the "model_selection" entry is
// self-describing (addresses M-1a); callers pass the valuation target's ticker.
//
// Selection logic (in priority order):
//  1. Financial industry (FIN prefix) -> DDM model
//  2. REIT industry -> FFO model
//  3. Non-positive operating income -> Revenue Multiple model
//  4. Default -> Multi-stage DCF model
//
// If the selected model does not support the industry, falls back to DCF.
//
// DC-1 Phase 4 (C-3): financials is a *cleaneddata.FinancialDataView (Restated)
// so the negative-OI routing decision (Rule 3) reflects the restated earnings
// the engine values, not the as-reported OI. The view is also nil-safe.
func (r *ModelRouter) SelectModel(ctx context.Context, ticker, industry string, financials *cleaneddata.FinancialDataView) ValuationModel {
	upperIndustry := strings.ToUpper(industry)

	// Rule 1: Financial companies use Dividend Discount Model
	if strings.HasPrefix(upperIndustry, "FIN") {
		if model := r.findModel("ddm"); model != nil {
			logctx.Or(ctx, r.logger).Info("Selected DDM model for financial company",
				zap.String("industry", industry))
			r.emitModelSelection(ctx, ticker, "ddm", "FIN prefix matched — financial company uses DDM")
			return model
		}
	}

	// Rule 2: REITs use FFO model.
	// Matches both "REIT" / "RESTATE" (parent code) and the REIT_* prefixed
	// subsector codes that the classifier emits via the RESTATE Pass-2
	// sub-industry refinement (VAL-3 P1+P4, T2-P4-W1 prefix reconciliation):
	// REIT_RESIDENTIAL, REIT_OFFICE, REIT_INDUSTRIAL, REIT_RETAIL,
	// REIT_HEALTHCARE, REIT_DATACENTER, REIT_CELLTOWER, REIT_SPECIALTY.
	// Listing them explicitly here (rather than reverse-mapping to RESTATE)
	// keeps the routing key the same shape the FFO model uses for its
	// multiple/cap-rate lookup, so model_selection traces remain
	// self-describing. The REIT_* prefix also aligns with parallel Tier 2
	// archetype rules in config/assumption_profiles.json so unknown REIT
	// subsector codes longest-prefix-match the same archetype family.
	if isREITIndustry(upperIndustry) {
		if model := r.findModel("ffo"); model != nil {
			logctx.Or(ctx, r.logger).Info("Selected FFO model for REIT",
				zap.String("industry", industry))
			r.emitModelSelection(ctx, ticker, "ffo", "REIT or RESTATE matched — REIT uses FFO model")
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
				logctx.Or(ctx, r.logger).Info("Selected Revenue Multiple model for negative OI company",
					zap.String("industry", industry),
					zap.Float64("operating_income", financials.OperatingIncome))
				r.emitModelSelection(ctx, ticker, "revenue_multiple", "negative or zero operating income — revenue multiple model")
				return model
			}
		}
	}

	// Rule 4: Default to multi-stage DCF
	if model := r.findModel("multi_stage_dcf"); model != nil {
		r.emitModelSelection(ctx, ticker, "dcf", "default — multi-stage DCF for profitable company")
		return model
	}

	// Absolute fallback: return the first available model
	if len(r.models) > 0 {
		logctx.Or(ctx, r.logger).Warn("No DCF model found, using first available model",
			zap.String("model_type", r.models[0].ModelType()))
		r.emitModelSelection(ctx, ticker, r.models[0].ModelType(), "absolute fallback — no DCF model registered")
		return r.models[0]
	}

	return nil
}

// emitModelSelection emits stage-4 "model_selection" trace. Centralised helper
// so each early-return branch above only calls one line.
func (r *ModelRouter) emitModelSelection(ctx context.Context, ticker, modelChosen, reason string) {
	if r.calcEmitter == nil {
		return
	}
	r.calcEmitter.Emit(ctx, "model_selection",
		zap.String("ticker", ticker),
		zap.String("model_chosen", modelChosen),
		zap.String("reason", reason),
	)
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

// reitIndustrySet holds the REIT_* prefixed subsector codes emitted by the
// RESTATE Pass-2 sub-industry classifier in addition to the parent-level
// "REIT" / "RESTATE" labels. Kept in the models package so isREITIndustry
// stays a pure-string check — no dep on the classifier or config packages.
//
// Codes are kept consistent with config/datacleaner/industry_codes.json
// `code` fields and config/industry_multiples.json reit_pffo_multiples keys
// (T2-P4-W1 prefix reconciliation). Any new REIT subsector must be added in
// all three places simultaneously.
var reitIndustrySet = map[string]struct{}{
	"REIT":             {},
	"RESTATE":          {},
	"REIT_RESIDENTIAL": {},
	"REIT_OFFICE":      {},
	"REIT_INDUSTRIAL":  {},
	"REIT_RETAIL":      {},
	"REIT_HEALTHCARE":  {},
	"REIT_DATACENTER":  {},
	"REIT_CELLTOWER":   {},
	"REIT_SPECIALTY":   {},
}

// isREITIndustry reports whether the upper-cased industry code denotes a REIT
// — either the parent label or one of the REIT_* prefixed subsector codes the
// classifier emits via Pass-2 sub-industry refinement. Also accepts any
// future REIT_* prefixed code via the explicit-prefix check so the FFO model
// stays the routing target for unknown REIT subsectors (longest-prefix-match
// inside FFOModel.getMultiple still resolves the value).
func isREITIndustry(upperIndustry string) bool {
	if _, ok := reitIndustrySet[upperIndustry]; ok {
		return true
	}
	// Defensive: accept any future REIT_* code that the classifier might
	// emit before reitIndustrySet is updated. Keeps routing stable when a
	// new subsector ships in industry_codes.json alone.
	return strings.HasPrefix(upperIndustry, "REIT_")
}
