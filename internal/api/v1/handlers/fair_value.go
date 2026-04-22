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
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
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

// FairValueResponse represents the response structure for fair value requests
// @Description Fair value calculation response with intrinsic valuation metrics
type FairValueResponse struct {
	Ticker                string                `json:"ticker" example:"AAPL"`                                  // Stock ticker symbol
	WACC                  float64               `json:"wacc" example:"0.092"`                                   // Weighted Average Cost of Capital
	GrowthRate            float64               `json:"growth_rate" example:"0.045"`                            // Summary growth rate (CAGR of projected rates)
	GrowthRates           []float64             `json:"growth_rates,omitempty"`                                 // Per-year projected growth rates
	GrowthSource          string                `json:"growth_source,omitempty" example:"analyst_blend"`        // Growth estimation source
	GrowthConfidence      string                `json:"growth_confidence,omitempty" example:"high"`             // Growth estimation confidence
	TangibleValuePerShare float64               `json:"tangible_value_per_share" example:"24.73"`               // Net tangible book value per share
	DCFValuePerShare      float64               `json:"dcf_value_per_share" example:"156.42"`                   // Discounted cash flow fair value per share
	AsOf                  string                `json:"as_of" example:"2025-08-13T22:15:34.402652598Z"`         // Timestamp of calculation
	DataQualityScore      float64               `json:"data_quality_score,omitempty" example:"85.5"`            // Data quality score (0-100)
	DataQualityGrade      string                `json:"data_quality_grade,omitempty" example:"B"`               // Data quality grade (A-F)
	CalculationMethod     string                `json:"calculation_method,omitempty" example:"multi_stage_dcf"` // Model used: multi_stage_dcf, ddm, ffo, revenue_multiple
	CalculationVersion    string                `json:"calculation_version,omitempty" example:"4.0"`            // Engine version that produced this result
	Warnings              []string              `json:"warnings,omitempty"`                                     // Data quality or assumption warnings
	SanityCheck           *entities.SanityCheck `json:"sanity_check,omitempty"`                                 // Multiples cross-check against sector medians
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

	// Calculate valuation
	result, err := h.valuationService.CalculateValuation(c.Request.Context(), ticker, opts)
	if err != nil {
		logctx.From(c.Request.Context()).Error("Valuation calculation failed",
			zap.String("ticker", ticker),
			zap.Error(err))

		// Classify error using sentinel types for reliable matching
		if errors.Is(err, valuation.ErrTickerNotFound) {
			h.sendError(c, http.StatusNotFound, "TICKER_NOT_FOUND",
				"Ticker not found",
				"The specified ticker could not be found in our database",
				map[string]interface{}{"ticker": ticker})
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
		AsOf:                  result.CalculatedAt.Format("2006-01-02T15:04:05Z"),
		DataQualityScore:      result.DataQualityScore,
		DataQualityGrade:      string(result.DataQualityGrade),
		CalculationMethod:     result.CalculationMethod,
		CalculationVersion:    result.CalculationVersion,
		Warnings:              result.Warnings,
		SanityCheck:           result.SanityCheck,
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
			AsOf:                  result.CalculatedAt.Format("2006-01-02T15:04:05Z"),
			DataQualityScore:      result.DataQualityScore,
			DataQualityGrade:      string(result.DataQualityGrade),
			CalculationMethod:     result.CalculationMethod,
			CalculationVersion:    result.CalculationVersion,
			Warnings:              result.Warnings,
			SanityCheck:           result.SanityCheck,
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
func (h *FairValueHandler) sendError(c *gin.Context, status int, errorType, title, detail string, ctx map[string]interface{}) {
	c.Header("Content-Type", "application/problem+json")
	c.JSON(status, gin.H{
		"type":      "https://problems.midas.dev/" + errorType,
		"title":     title,
		"status":    status,
		"detail":    detail,
		"instance":  c.Request.URL.Path,
		"context":   ctx,
		"code":      errorType,
		"timestamp": time.Now().UTC(),
		"method":    c.Request.Method,
	})
	c.Abort()
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
