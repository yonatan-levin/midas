package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
	"github.com/midas/dcf-valuation-api/internal/observability/narrate"
	"github.com/midas/dcf-valuation-api/internal/services/valuation"
)

// ValuationCalculator abstracts the valuation service so handlers depend on
// an interface rather than a concrete type, following clean architecture.
// *valuation.Service satisfies this interface implicitly.
type ValuationCalculator interface {
	CalculateValuation(ctx context.Context, ticker string, opts *valuation.ValuationOptions) (*entities.ValuationResult, error)
}

// FairValueHandler handles fair value related HTTP requests
type FairValueHandler struct {
	valuationService ValuationCalculator
	// logger is retained for non-request contexts; request-path log sites use logctx.From(ctx)
	logger *zap.Logger
}

// NewFairValueHandler creates a new FairValueHandler instance
func NewFairValueHandler(valuationService ValuationCalculator, logger *zap.Logger) *FairValueHandler {
	return &FairValueHandler{
		valuationService: valuationService,
		logger:           logger,
	}
}

// Industry exposes both industry classifications the engine computes on every
// fair-value request: the SIC-derived label (canonical, used for valuation
// model selection) and the balance-sheet heuristic label (used by the
// datacleaner's industry-specific rule loader). Consumers can compare the two
// via the Match flag to surface classification drift.
// @Description Dual industry classification (SIC + heuristic) with a Match flag
type Industry struct {
	SICCode       string `json:"sic_code,omitempty" example:"3674"`                         // Raw SIC code from SEC (may be empty if SEC data lacked it)
	SIC           string `json:"sic,omitempty" example:"MFG"`                               // SIC-derived industry label from IndustryClassifier.Classify
	HeuristicCode string `json:"heuristic_code,omitempty" example:"45"`                     // GICS sector code from IndustryClassifier.ClassifyIndustry
	HeuristicName string `json:"heuristic_name,omitempty" example:"Information Technology"` // GICS sector name
	Match         bool   `json:"match" example:"true"`                                      // true when SIC and heuristic agree per the canonical mapping
}

// sicToGICS is the canonical mapping from SIC-classifier labels (as emitted by
// IndustryClassifier.Classify per config/datacleaner/industry_codes.json) to
// the set of GICS sector codes considered a "match". Keys here MUST correspond
// to the `code` fields in industry_codes.json — any divergence silently
// demotes every ticker in that sector to Match=false. The MFG -> {"20", "45"}
// multi-map is deliberate: semiconductors/hardware return SIC "MFG"
// (manufacturing) but GICS "45" (Information Technology), and that pairing is
// a legitimate match rather than classifier drift. Same rationale for
// RETAIL -> {"25", "30"} (grocery retailers) and CONS -> {"30", "25"}.
// Any combination outside this table — or a missing value on either side —
// yields Match=false (conservative, preferring false negatives over false
// positives as drift signals).
var sicToGICS = map[string]map[string]bool{
	"TECH":    {"45": true},             // Information Technology
	"MFG":     {"20": true, "45": true}, // Industrials OR Info Tech (semi/hardware mfrs)
	"RETAIL":  {"25": true, "30": true}, // Consumer Discretionary OR Consumer Staples (grocery)
	"UTIL":    {"55": true},             // Utilities
	"FIN":     {"40": true},             // Financials (B-1 fix: was incorrectly "FINL")
	"HEALTH":  {"35": true},             // Health Care
	"ENERGY":  {"10": true},             // Energy
	"RESTATE": {"60": true},             // Real Estate
	"TELECOM": {"50": true},             // Communication Services
	"TRANS":   {"20": true},             // Industrials (transportation)
	"CONS":    {"30": true, "25": true}, // Consumer Staples primary, Discretionary secondary

	// REIT subsector codes emitted by the RESTATE Pass-2 sub-industry
	// classifier (VAL-3 P1+P4). All map to GICS "60" (Real Estate). Listed
	// explicitly so matchSICToGICS can resolve the full code without falling
	// through to the parent-prefix strip — RETAIL_REIT, DATA_CENTER, etc.
	// would otherwise normalize to "RETAIL" / "DATA" and silently drift to
	// match=false.
	"RESIDENTIAL":     {"60": true},
	"OFFICE":          {"60": true},
	"INDUSTRIAL":      {"60": true},
	"RETAIL_REIT":     {"60": true},
	"HEALTHCARE_REIT": {"60": true},
	"DATA_CENTER":     {"60": true},
	"CELLTOWER":       {"60": true},
	"SPECIALTY":       {"60": true},
}

// matchSICToGICS returns true when the SIC-derived label and the heuristic
// GICS code agree per the sicToGICS table. Empty inputs are never a match.
//
// Lookup order:
//  1. Full-code exact match (catches REIT subsector codes like RETAIL_REIT
//     and DATA_CENTER that legitimately contain underscores yet map to a
//     different GICS sector than their first-token prefix would suggest).
//  2. Strip-at-first-underscore parent prefix, then look up again. Lets the
//     classifier's Pass-2 sub-industry codes (TECH_SAAS, HEALTH_BIOTECH,
//     FIN_IB, MFG_SEMI, FIN_BANK, …) inherit their parent's GICS mapping
//     without an explicit entry per sub-industry.
func matchSICToGICS(sicLabel, gicsCode string) bool {
	if sicLabel == "" || gicsCode == "" {
		return false
	}
	// 1. Exact full-code match wins so REIT subsector codes resolve correctly:
	//    RETAIL_REIT → 60 must NOT collapse to RETAIL → {25, 30}.
	if allowed, ok := sicToGICS[sicLabel]; ok {
		return allowed[gicsCode]
	}
	// 2. Fall back to parent prefix for codes whose subsector isn't listed
	//    explicitly in sicToGICS (TECH_SAAS, HEALTH_BIOTECH, FIN_BANK, …).
	if i := strings.IndexByte(sicLabel, '_'); i >= 0 {
		sicLabel = sicLabel[:i]
		if allowed, ok := sicToGICS[sicLabel]; ok {
			return allowed[gicsCode]
		}
	}
	return false
}

// BuildIndustryFromResult constructs the Industry response object from the
// classification fields plumbed onto ValuationResult. Returns nil when the
// engine produced no classification signal at all, so the response's
// omitempty-tagged Industry field disappears entirely.
//
// Exported in Phase R2 D1.1 (observability replay tooling) so the replay
// orchestration layer in internal/observability/replay/replay.go can rebuild
// a response-equivalent shape from *entities.ValuationResult and diff it
// against the bundle's recorded 17-response.json. The rename is logic-free —
// callers of the lowercase symbol moved to the capitalized name with no
// behavioral change.
func BuildIndustryFromResult(result *entities.ValuationResult) *Industry {
	if result == nil {
		return nil
	}
	if result.SICCodeRaw == "" && result.IndustrySIC == "" &&
		result.IndustryHeuristicCode == "" && result.IndustryHeuristicName == "" {
		return nil
	}
	return &Industry{
		SICCode:       result.SICCodeRaw,
		SIC:           result.IndustrySIC,
		HeuristicCode: result.IndustryHeuristicCode,
		HeuristicName: result.IndustryHeuristicName,
		Match:         matchSICToGICS(result.IndustrySIC, result.IndustryHeuristicCode),
	}
}

// FairValueResponse represents the response structure for fair value requests
// @Description Fair value calculation response with intrinsic valuation metrics
type FairValueResponse struct {
	Ticker                string    `json:"ticker" example:"AAPL"`                           // Stock ticker symbol
	WACC                  float64   `json:"wacc" example:"0.092"`                            // Weighted Average Cost of Capital
	GrowthRate            float64   `json:"growth_rate" example:"0.045"`                     // Summary growth rate (CAGR of projected rates)
	GrowthRates           []float64 `json:"growth_rates,omitempty"`                          // Per-year projected growth rates
	GrowthSource          string    `json:"growth_source,omitempty" example:"analyst_blend"` // Growth estimation source
	GrowthConfidence      string    `json:"growth_confidence,omitempty" example:"high"`      // Growth estimation confidence
	TangibleValuePerShare float64   `json:"tangible_value_per_share" example:"24.73"`        // Net tangible book value per share
	DCFValuePerShare      float64   `json:"dcf_value_per_share" example:"156.42"`            // Discounted cash flow fair value per share

	// Graham-school asset-floor diagnostics — see
	// docs/refactoring/graham-floor-metrics-spec.md. All four use *float64 +
	// omitempty: nil = TotalLiabilities unresolved (a warning is appended to
	// `warnings`). Non-nil = resolved; values may be negative (NCAV on
	// distressed companies) or 0 (floor clamped when NCAV is negative). Pointer
	// types preserve the deep-distress signal (resolved + negative + clamped)
	// distinct from the unresolved-fallback signal (all four absent + warning).
	CurrentAssetsPerShare *float64 `json:"current_assets_per_share,omitempty" example:"55.13"`
	NCAVPerShare          *float64 `json:"ncav_per_share,omitempty" example:"4.55"`
	GrahamFloorPerShare   *float64 `json:"graham_floor_per_share,omitempty" example:"3.03"`
	GrahamDiscountPct     *float64 `json:"graham_discount_pct,omitempty" example:"23.30"`

	AsOf               string                `json:"as_of" example:"2025-08-13T22:15:34.402652598Z"`         // Timestamp of calculation
	DataQualityScore   float64               `json:"data_quality_score,omitempty" example:"85.5"`            // Data quality score (0-100)
	DataQualityGrade   string                `json:"data_quality_grade,omitempty" example:"B"`               // Data quality grade (A-F)
	CalculationMethod  string                `json:"calculation_method,omitempty" example:"multi_stage_dcf"` // Model used: multi_stage_dcf, ddm, ffo, revenue_multiple
	CalculationVersion string                `json:"calculation_version,omitempty" example:"4.1"`            // Engine version that produced this result
	Warnings           []string              `json:"warnings,omitempty"`                                     // Data quality or assumption warnings
	SanityCheck        *entities.SanityCheck `json:"sanity_check,omitempty"`                                 // Multiples cross-check against sector medians
	Industry           *Industry             `json:"industry,omitempty"`                                     // Dual industry classification (SIC + heuristic) for drift detection

	// Currency is the ISO-4217 code that dcf_value_per_share and
	// tangible_value_per_share are denominated in. Always "USD" — the
	// valuation service FX-converts each period's reporting-currency
	// monetary fields to USD via Phase B9 of the IFRS-FPI plan
	// (docs/refactoring/ifrs-foreign-private-issuer-support-spec.md), so
	// API consumers MUST NOT re-convert. Surfaced so a downstream client
	// can display "USD" alongside the per-share value rather than guessing.
	Currency string `json:"currency" example:"USD"`

	// ADRRatioApplied is the ordinary-shares-per-ADR multiplier that the
	// valuation engine divided SEC-reported share counts by before
	// computing per-share values, so the resulting fair value compares
	// like-for-like with the listed ADR price. 1 for domestic 10-K filers
	// (and any ticker absent from config/adr_ratios.json); non-1 for
	// configured ADRs (TSM=5, BABA=8, …). Phase B10 of the IFRS-FPI plan.
	// Omitted from the JSON when zero (defensive — the service always
	// stamps a positive int via ADRRatios.Get, but omitempty keeps the
	// response clean if a future bug produces 0).
	ADRRatioApplied int `json:"adr_ratio_applied,omitempty" example:"5"`

	// CurrentPrice is the live per-share market price captured from the
	// market-data gateway (Yahoo Finance / Finzive) at the moment the
	// valuation was computed. Same denomination and per-share basis as
	// DCFValuePerShare and TangibleValuePerShare — for ADRs this is the
	// per-ADR exchange price, directly comparable to the per-ADR DCF
	// value the engine produces after applyADRRatio. Surfaced so a
	// consumer can compute the upside/downside discount ((dcf - price)
	// / price) without a second quote lookup. Omitted when zero.
	CurrentPrice float64 `json:"current_price,omitempty" example:"190.25"`
}

// BulkFairValueRequest represents the request structure for bulk fair value requests
// @Description Bulk fair value calculation request for multiple tickers
type BulkFairValueRequest struct {
	Tickers          []string `json:"tickers" binding:"required,min=1,max=10" example:"[\"AAPL\",\"MSFT\",\"GOOGL\"]"` // Stock ticker symbols (max 10)
	OverrideBeta     *float64 `json:"override_beta,omitempty" example:"1.2"`                                           // Optional beta override
	OverrideRiskFree *float64 `json:"override_rf,omitempty" example:"0.045"`                                           // Optional risk-free rate override
}

// BulkFailure describes why a single ticker failed during bulk valuation.
type BulkFailure struct {
	Ticker    string `json:"ticker"`
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

// BulkFairValueResponse represents the response for bulk requests.
// When both successes and failures exist, the HTTP status is 207 Multi-Status.
// When all tickers fail, the HTTP status is 422 Unprocessable Entity.
type BulkFairValueResponse struct {
	Results  []FairValueResponse `json:"results"`
	Failures []BulkFailure       `json:"failures,omitempty"`
	Summary  BulkSummary         `json:"summary"`
}

// BulkSummary provides summary statistics for bulk requests
type BulkSummary struct {
	TotalRequested int `json:"total_requested"`
	Successful     int `json:"successful"`
	Failed         int `json:"failed"`
}

// ErrorResponse represents an error response structure
// @Description Standard error response following RFC 7807 Problem Details
type ErrorResponse struct {
	Type      string                 `json:"type" example:"https://problems.midas.dev/INVALID_TICKER"` // Problem type URI
	Title     string                 `json:"title" example:"Bad Request"`                              // Human-readable title
	Status    int                    `json:"status" example:"400"`                                     // HTTP status code
	Detail    string                 `json:"detail" example:"Invalid ticker format"`                   // Human-readable explanation
	Instance  string                 `json:"instance" example:"/api/v1/fair-value/INVALID"`            // URI reference to specific occurrence
	Context   map[string]interface{} `json:"context,omitempty"`                                        // Additional context information
	Code      string                 `json:"code,omitempty" example:"INVALID_TICKER"`                  // Error code (RFC 7807 extension)
	Timestamp string                 `json:"timestamp,omitempty"`                                      // ISO 8601 timestamp (RFC 7807 extension)
	Method    string                 `json:"method,omitempty" example:"GET"`                           // HTTP method (RFC 7807 extension)
}

// GetFairValue handles GET /api/v1/fair-value/:ticker requests
// @Summary      Get fair value for a stock
// @Description  Calculate intrinsic fair value for a stock using DCF and net tangible assets
// @Tags         fair-value
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Param        ticker         path     string   true  "Stock ticker symbol (e.g., AAPL)"
// @Param        override_beta  query    number   false "Override beta for WACC calculation" minimum(0) maximum(3)
// @Param        override_rf    query    number   false "Override risk-free rate" minimum(0) maximum(0.2)
// @Success      200  {object}  FairValueResponse
// @Failure      400  {object}  ErrorResponse "Invalid ticker or parameters"
// @Failure      401  {object}  ErrorResponse "Missing or invalid API key"
// @Failure      403  {object}  ErrorResponse "Insufficient permissions"
// @Failure      404  {object}  ErrorResponse "Ticker not found"
// @Failure      429  {object}  ErrorResponse "Rate limit exceeded"
// @Failure      500  {object}  ErrorResponse "Internal server error"
// @Router       /fair-value/{ticker} [get]
func (h *FairValueHandler) GetFairValue(c *gin.Context) {
	ticker := strings.ToUpper(c.Param("ticker"))

	// Validate ticker format
	if !isValidTicker(ticker) {
		h.sendError(c, http.StatusBadRequest, "INVALID_TICKER",
			"Invalid ticker format",
			"Ticker must be 1-5 alphanumeric characters",
			map[string]interface{}{"ticker": ticker})
		return
	}

	// Stamp the ticker on the request-scoped narrate emitter so every
	// downstream Emit call (auth.resolved already fired with no ticker;
	// everything from here on does) carries it as a standard field. Also
	// stamp the bundle so its manifest reflects the parsed ticker.
	em := narrate.From(c.Request.Context())
	em.WithTicker(ticker)
	if b := artifact.From(c.Request.Context()); b != nil {
		b.SetTicker(ticker)
	}

	// Parse and validate query parameters
	overrideBeta := parseFloatParam(c, "override_beta")
	overrideRF := parseFloatParam(c, "override_rf")

	// Validate override ranges (defense in depth — matches Swagger spec bounds)
	if overrideBeta != nil && (*overrideBeta < 0 || *overrideBeta > 3.0) {
		h.sendError(c, http.StatusBadRequest, "INVALID_PARAMETER",
			"Invalid override_beta",
			"Beta override must be between 0 and 3.0",
			map[string]interface{}{"override_beta": *overrideBeta})
		return
	}
	if overrideRF != nil && (*overrideRF < 0 || *overrideRF > 0.20) {
		h.sendError(c, http.StatusBadRequest, "INVALID_PARAMETER",
			"Invalid override_rf",
			"Risk-free rate override must be between 0 and 0.20",
			map[string]interface{}{"override_rf": *overrideRF})
		return
	}

	logctx.From(c.Request.Context()).Info("Processing fair value request",
		zap.String("ticker", ticker),
		zap.Float64p("override_beta", overrideBeta),
		zap.Float64p("override_rf", overrideRF))

	// Build valuation options from query parameter overrides
	var opts *valuation.ValuationOptions
	if overrideBeta != nil || overrideRF != nil {
		opts = &valuation.ValuationOptions{
			OverrideBeta:     overrideBeta,
			OverrideRiskFree: overrideRF,
		}
	}

	// Tier-1 narrate: handler.entry. The "options" field reports which
	// overrides the user supplied so the per-request story shows whether
	// this was a default-parameter call or an ad-hoc tweak.
	overridesApplied := []string{}
	if overrideBeta != nil {
		overridesApplied = append(overridesApplied, "beta")
	}
	if overrideRF != nil {
		overridesApplied = append(overridesApplied, "rf")
	}
	em.Emit(c.Request.Context(), narrate.PhaseHandlerEntry, narrate.OutcomeOK, "",
		zap.Strings("options", overridesApplied),
	)

	// Tier-3 artifact bundle: snapshot the parsed handler input so the
	// bundle pins exactly what overrides this request used.
	if b := artifact.From(c.Request.Context()); b != nil {
		b.Snapshot(c.Request.Context(), "handler.entry", "02-handler-options.json", map[string]any{
			"ticker":        ticker,
			"override_beta": overrideBeta,
			"override_rf":   overrideRF,
		})
	}

	// Calculate valuation
	result, err := h.valuationService.CalculateValuation(c.Request.Context(), ticker, opts)
	if err != nil {
		// Tier-1 narrate: valuation.computed with outcome=error so the per-
		// request story shows the failure even when the engine returns
		// before any of the lower-level emissions could fire.
		em.Emit(c.Request.Context(), narrate.PhaseValuationComputed, narrate.OutcomeError, err.Error())

		logctx.From(c.Request.Context()).Error("Valuation calculation failed",
			zap.String("ticker", ticker),
			zap.Error(err))

		// Classify error using sentinel types for reliable matching.
		// FPI MUST be checked before ErrInsufficientData — both produce 422
		// but FPI carries a more specific code/message that helps users
		// understand we have data, just in a taxonomy we don't yet parse.
		if errors.Is(err, valuation.ErrTickerNotFound) {
			h.sendError(c, http.StatusNotFound, "TICKER_NOT_FOUND",
				"Ticker not found",
				"The specified ticker could not be found in our database",
				map[string]interface{}{"ticker": ticker})
		} else if errors.Is(err, valuation.ErrForeignPrivateIssuer) {
			h.sendError(c, http.StatusUnprocessableEntity, "FOREIGN_PRIVATE_ISSUER_UNSUPPORTED",
				"Foreign private issuer not covered",
				"This ticker files using a taxonomy or currency pair Midas does not yet cover. Supported: ifrs-full taxonomy with FRED-tracked currencies (TWD, EUR, JPY, GBP, HKD, CNY, KRW, CHF, CAD, AUD, INR, BRL, DKK). Out-of-coverage taxonomies (JGAAP, K-IFRS, ifrs-smes) and currencies are tracked in docs/refactoring/ifrs-foreign-private-issuer-support-spec.md.",
				map[string]interface{}{
					"ticker":      ticker,
					"filing_type": "20-F",
					"taxonomy":    "ifrs-full",
				})
		} else if errors.Is(err, valuation.ErrInsufficientData) {
			h.sendError(c, http.StatusUnprocessableEntity, "INSUFFICIENT_DATA",
				"Insufficient data for valuation",
				"Not enough financial data available to perform reliable valuation",
				map[string]interface{}{"ticker": ticker})
		} else if errors.Is(err, valuation.ErrModelNotApplicable) {
			h.sendError(c, http.StatusUnprocessableEntity, "MODEL_NOT_APPLICABLE",
				"Standard DCF model not applicable",
				"Standard DCF requires positive operating income and alternative models (DDM, FFO, revenue multiples) could not produce a result for this company.",
				map[string]interface{}{"ticker": ticker})
		} else {
			h.sendError(c, http.StatusInternalServerError, "CALCULATION_ERROR",
				"Valuation calculation failed",
				"An internal error occurred during valuation calculation",
				map[string]interface{}{"ticker": ticker})
		}
		return
	}

	// Convert to response format
	response := FairValueResponse{
		Ticker:                ticker,
		WACC:                  result.WACC,
		GrowthRate:            result.GrowthRate,
		GrowthRates:           result.GrowthRates,
		GrowthSource:          result.GrowthSource,
		GrowthConfidence:      result.GrowthConfidence,
		TangibleValuePerShare: result.TangibleValuePerShare,
		DCFValuePerShare:      result.DCFValuePerShare,
		CurrentAssetsPerShare: result.CurrentAssetsPerShare,
		NCAVPerShare:          result.NCAVPerShare,
		GrahamFloorPerShare:   result.GrahamFloorPerShare,
		GrahamDiscountPct:     result.GrahamDiscountPct,
		AsOf:                  result.CalculatedAt.Format("2006-01-02T15:04:05Z"),
		DataQualityScore:      result.DataQualityScore,
		DataQualityGrade:      string(result.DataQualityGrade),
		CalculationMethod:     result.CalculationMethod,
		CalculationVersion:    result.CalculationVersion,
		Warnings:              result.Warnings,
		SanityCheck:           result.SanityCheck,
		Industry:              BuildIndustryFromResult(result),
		// Phase B12 (IFRS-FPI): always-present transparency fields. Currency
		// falls back to "USD" if an upstream code path forgot to stamp it
		// (defense in depth — the valuation service guarantees "USD" today).
		Currency:        currencyOrUSD(result.ReportingCurrency),
		ADRRatioApplied: result.ADRRatioApplied,
		CurrentPrice:    result.CurrentPrice,
	}

	// Tier-1 narrate: valuation.computed success line. Carries the headline
	// numbers so the per-request story ends with the actual fair-value output.
	em.Emit(c.Request.Context(), narrate.PhaseValuationComputed, narrate.OutcomeOK, "",
		zap.String("model", result.CalculationMethod),
		zap.Float64("fair_value_per_share", result.DCFValuePerShare),
		zap.Float64("tangible_value_per_share", result.TangibleValuePerShare),
	)

	// Tier-3 artifact bundle: snapshot the final response body. This is the
	// canonical "what we sent back to the client" record — invaluable when
	// a downstream consumer reports an unexpected number weeks later.
	if b := artifact.From(c.Request.Context()); b != nil {
		b.Snapshot(c.Request.Context(), "response.sent", "17-response.json", &response)
		b.AddSchemaVersion("FairValueResponse", 1)
	}

	logctx.From(c.Request.Context()).Info("Fair value calculation completed",
		zap.String("ticker", ticker),
		zap.Float64("dcf_value", result.DCFValuePerShare),
		zap.Float64("tangible_value", result.TangibleValuePerShare))

	c.JSON(http.StatusOK, response)
}

// GetBulkFairValue handles POST /api/v1/fair-value/bulk requests
// @Summary      Get fair values for multiple stocks
// @Description  Calculate intrinsic fair values for multiple stocks in a single request
// @Tags         fair-value
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Param        request  body     BulkFairValueRequest  true  "Bulk fair value request"
// @Success      200  {object}  BulkFairValueResponse
// @Failure      400  {object}  ErrorResponse "Invalid request format"
// @Failure      401  {object}  ErrorResponse "Missing or invalid API key"
// @Failure      403  {object}  ErrorResponse "Insufficient permissions"
// @Failure      429  {object}  ErrorResponse "Rate limit exceeded"
// @Failure      500  {object}  ErrorResponse "Internal server error"
// @Router       /fair-value/bulk [post]
func (h *FairValueHandler) GetBulkFairValue(c *gin.Context) {
	var request BulkFairValueRequest

	if err := c.ShouldBindJSON(&request); err != nil {
		h.sendError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"Invalid request format",
			"Request body does not match expected format",
			map[string]interface{}{"validation_error": err.Error()})
		return
	}

	// Bulk requests do not have a URL :ticker param, but they still carry
	// useful ticker context in the body. Stamp a stable pseudo-ticker so
	// always/on-error/on-quality-flag artifact bundles do not promote under
	// the generic _no-ticker partition.
	if subject := bulkArtifactSubject(request.Tickers); subject != "" {
		narrate.From(c.Request.Context()).WithTicker(subject)
		if b := artifact.From(c.Request.Context()); b != nil {
			b.SetTicker(subject)
		}
	}

	// Validate override ranges (same bounds as single endpoint)
	if request.OverrideBeta != nil && (*request.OverrideBeta < 0 || *request.OverrideBeta > 3.0) {
		h.sendError(c, http.StatusBadRequest, "INVALID_PARAMETER",
			"Invalid override_beta",
			"Beta override must be between 0 and 3.0",
			map[string]interface{}{"override_beta": *request.OverrideBeta})
		return
	}
	if request.OverrideRiskFree != nil && (*request.OverrideRiskFree < 0 || *request.OverrideRiskFree > 0.20) {
		h.sendError(c, http.StatusBadRequest, "INVALID_PARAMETER",
			"Invalid override_rf",
			"Risk-free rate override must be between 0 and 0.20",
			map[string]interface{}{"override_rf": *request.OverrideRiskFree})
		return
	}

	logctx.From(c.Request.Context()).Info("Processing bulk fair value request",
		zap.Int("ticker_count", len(request.Tickers)),
		zap.Strings("tickers", request.Tickers))

	results := make([]FairValueResponse, 0, len(request.Tickers))
	failures := make([]BulkFailure, 0)
	successful := 0
	failed := 0

	// Process each ticker
	for _, ticker := range request.Tickers {
		ticker = strings.ToUpper(ticker)

		// Validate ticker format
		if !isValidTicker(ticker) {
			logctx.From(c.Request.Context()).Warn("Skipping invalid ticker in bulk request", zap.String("ticker", ticker))
			failures = append(failures, BulkFailure{
				Ticker:    ticker,
				ErrorCode: "INVALID_TICKER",
				Message:   "Invalid ticker format: must be 1-5 alphanumeric characters",
			})
			failed++
			continue
		}

		// Build valuation options from bulk request overrides
		var opts *valuation.ValuationOptions
		if request.OverrideBeta != nil || request.OverrideRiskFree != nil {
			opts = &valuation.ValuationOptions{
				OverrideBeta:     request.OverrideBeta,
				OverrideRiskFree: request.OverrideRiskFree,
			}
		}

		// Calculate valuation for this ticker
		result, err := h.valuationService.CalculateValuation(c.Request.Context(), ticker, opts)
		if err != nil {
			logctx.From(c.Request.Context()).Warn("Valuation failed for ticker in bulk request",
				zap.String("ticker", ticker),
				zap.Error(err))

			// Classify the error using sentinel types for per-ticker failure detail
			failure := classifyBulkError(ticker, err)
			failures = append(failures, failure)
			failed++
			continue
		}

		// Add to results
		response := FairValueResponse{
			Ticker:                ticker,
			WACC:                  result.WACC,
			GrowthRate:            result.GrowthRate,
			GrowthRates:           result.GrowthRates,
			GrowthSource:          result.GrowthSource,
			GrowthConfidence:      result.GrowthConfidence,
			TangibleValuePerShare: result.TangibleValuePerShare,
			DCFValuePerShare:      result.DCFValuePerShare,
			CurrentAssetsPerShare: result.CurrentAssetsPerShare,
			NCAVPerShare:          result.NCAVPerShare,
			GrahamFloorPerShare:   result.GrahamFloorPerShare,
			GrahamDiscountPct:     result.GrahamDiscountPct,
			AsOf:                  result.CalculatedAt.Format("2006-01-02T15:04:05Z"),
			DataQualityScore:      result.DataQualityScore,
			DataQualityGrade:      string(result.DataQualityGrade),
			CalculationMethod:     result.CalculationMethod,
			CalculationVersion:    result.CalculationVersion,
			Warnings:              result.Warnings,
			SanityCheck:           result.SanityCheck,
			Industry:              BuildIndustryFromResult(result),
			// Phase B12 (IFRS-FPI): mirror single-ticker handler for parity.
			Currency:        currencyOrUSD(result.ReportingCurrency),
			ADRRatioApplied: result.ADRRatioApplied,
			CurrentPrice:    result.CurrentPrice,
		}

		results = append(results, response)
		successful++
	}

	// Create bulk response with failure details
	bulkResponse := BulkFairValueResponse{
		Results:  results,
		Failures: failures,
		Summary: BulkSummary{
			TotalRequested: len(request.Tickers),
			Successful:     successful,
			Failed:         failed,
		},
	}

	logctx.From(c.Request.Context()).Info("Bulk fair value calculation completed",
		zap.Int("successful", successful),
		zap.Int("failed", failed))

	// Choose HTTP status based on outcome:
	// - 200 OK: all tickers succeeded
	// - 207 Multi-Status: partial success (some succeeded, some failed)
	// - 422 Unprocessable Entity: all tickers failed
	switch {
	case failed == 0:
		c.JSON(http.StatusOK, bulkResponse)
	case successful == 0:
		c.JSON(http.StatusUnprocessableEntity, bulkResponse)
	default:
		c.JSON(http.StatusMultiStatus, bulkResponse)
	}
}

// classifyBulkError maps a valuation service error to a BulkFailure with
// an appropriate error code and human-readable message.
func classifyBulkError(ticker string, err error) BulkFailure {
	switch {
	case errors.Is(err, valuation.ErrTickerNotFound):
		return BulkFailure{
			Ticker:    ticker,
			ErrorCode: "TICKER_NOT_FOUND",
			Message:   "Ticker not found in any data source",
		}
	case errors.Is(err, valuation.ErrForeignPrivateIssuer):
		// Must be checked before ErrInsufficientData (more specific case).
		return BulkFailure{
			Ticker:    ticker,
			ErrorCode: "FOREIGN_PRIVATE_ISSUER_UNSUPPORTED",
			Message:   "Foreign private issuer with taxonomy or currency outside Midas coverage",
		}
	case errors.Is(err, valuation.ErrInsufficientData):
		return BulkFailure{
			Ticker:    ticker,
			ErrorCode: "INSUFFICIENT_DATA",
			Message:   "Not enough financial data for reliable valuation",
		}
	case errors.Is(err, valuation.ErrModelNotApplicable):
		return BulkFailure{
			Ticker:    ticker,
			ErrorCode: "MODEL_NOT_APPLICABLE",
			Message:   "Standard DCF not applicable; company has non-positive operating income",
		}
	default:
		return BulkFailure{
			Ticker:    ticker,
			ErrorCode: "CALCULATION_ERROR",
			Message:   "Valuation calculation failed",
		}
	}
}

// Helper functions

// sendError sends an RFC 7807 compliant error response, consistent with
// the server.go respondWithError format (code, timestamp, method fields).
// Uses the ErrorResponse struct (not gin.H) so field additions stay
// compile-checked and the timestamp is explicitly RFC 3339.
func (h *FairValueHandler) sendError(c *gin.Context, status int, errorType, title, detail string, ctx map[string]interface{}) {
	c.Header("Content-Type", "application/problem+json")
	c.JSON(status, ErrorResponse{
		Type:      "https://problems.midas.dev/" + errorType,
		Title:     title,
		Status:    status,
		Detail:    detail,
		Instance:  c.Request.URL.Path,
		Context:   ctx,
		Code:      errorType,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Method:    c.Request.Method,
	})
	c.Abort()
}

// currencyOrUSD returns its argument when non-empty, "USD" otherwise.
// Defense-in-depth helper for the Phase B12 transparency field — the
// valuation service always stamps result.ReportingCurrency = "USD" today,
// but this guarantees the response always carries an ISO-4217 code so
// downstream clients never see the empty string.
func currencyOrUSD(c string) string {
	if c == "" {
		return "USD"
	}
	return c
}

// isValidTicker validates ticker format (1-5 alphanumeric characters)
func isValidTicker(ticker string) bool {
	if len(ticker) == 0 || len(ticker) > 5 {
		return false
	}

	for _, char := range ticker {
		// nolint:staticcheck // readability preferred over De Morgan simplification
		if !((char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')) {
			return false
		}
	}

	return true
}

func bulkArtifactSubject(tickers []string) string {
	parts := make([]string, 0, len(tickers))
	for _, ticker := range tickers {
		t := strings.ToUpper(strings.TrimSpace(ticker))
		if isValidTicker(t) {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return "BULK_INVALID"
	}
	return "BULK_" + strings.Join(parts, "_")
}

// parseFloatParam safely parses a float query parameter
func parseFloatParam(c *gin.Context, param string) *float64 {
	value := c.Query(param)
	if value == "" {
		return nil
	}

	if parsed, err := strconv.ParseFloat(value, 64); err == nil {
		return &parsed
	}

	return nil
}
