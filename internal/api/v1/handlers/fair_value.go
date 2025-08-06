package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/services/valuation"
)

// FairValueHandler handles fair value related HTTP requests
type FairValueHandler struct {
	valuationService *valuation.Service
	logger           *zap.Logger
}

// NewFairValueHandler creates a new FairValueHandler instance
func NewFairValueHandler(valuationService *valuation.Service, logger *zap.Logger) *FairValueHandler {
	return &FairValueHandler{
		valuationService: valuationService,
		logger:           logger,
	}
}

// FairValueResponse represents the response structure for fair value requests
type FairValueResponse struct {
	Ticker                string  `json:"ticker"`
	WACC                  float64 `json:"wacc"`
	GrowthRate            float64 `json:"growth_rate"`
	TangibleValuePerShare float64 `json:"tangible_value_per_share"`
	DCFValuePerShare      float64 `json:"dcf_value_per_share"`
	AsOf                  string  `json:"as_of"`
	DataQualityScore      float64 `json:"data_quality_score,omitempty"`
	DataQualityGrade      string  `json:"data_quality_grade,omitempty"`
}

// BulkFairValueRequest represents the request structure for bulk fair value requests
type BulkFairValueRequest struct {
	Tickers          []string `json:"tickers" binding:"required,min=1,max=10"`
	OverrideBeta     *float64 `json:"override_beta,omitempty"`
	OverrideRiskFree *float64 `json:"override_rf,omitempty"`
}

// BulkFairValueResponse represents the response for bulk requests
type BulkFairValueResponse struct {
	Results []FairValueResponse `json:"results"`
	Summary BulkSummary         `json:"summary"`
}

// BulkSummary provides summary statistics for bulk requests
type BulkSummary struct {
	TotalRequested int `json:"total_requested"`
	Successful     int `json:"successful"`
	Failed         int `json:"failed"`
}

// ErrorResponse represents an RFC 7807 compliant error response
type ErrorResponse struct {
	Type     string                 `json:"type"`
	Title    string                 `json:"title"`
	Status   int                    `json:"status"`
	Detail   string                 `json:"detail"`
	Instance string                 `json:"instance"`
	Context  map[string]interface{} `json:"context,omitempty"`
}

// GetFairValue handles GET /api/v1/fair-value/:ticker
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

	// Parse query parameters
	overrideBeta := parseFloatParam(c, "override_beta")
	overrideRF := parseFloatParam(c, "override_rf")

	h.logger.Info("Processing fair value request",
		zap.String("ticker", ticker),
		zap.Float64p("override_beta", overrideBeta),
		zap.Float64p("override_rf", overrideRF))

	// Calculate valuation
	result, err := h.valuationService.CalculateValuation(c.Request.Context(), ticker)
	if err != nil {
		h.logger.Error("Valuation calculation failed",
			zap.String("ticker", ticker),
			zap.Error(err))

		// Determine appropriate error response based on error type
		if strings.Contains(err.Error(), "not found") {
			h.sendError(c, http.StatusNotFound, "TICKER_NOT_FOUND",
				"Ticker not found",
				"The specified ticker could not be found in our database",
				map[string]interface{}{"ticker": ticker})
		} else if strings.Contains(err.Error(), "insufficient data") {
			h.sendError(c, http.StatusUnprocessableEntity, "INSUFFICIENT_DATA",
				"Insufficient data for valuation",
				"Not enough financial data available to perform reliable valuation",
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
		TangibleValuePerShare: result.TangibleValuePerShare,
		DCFValuePerShare:      result.DCFValuePerShare,
		AsOf:                  result.CalculatedAt.Format("2006-01-02T15:04:05Z"),
		DataQualityScore:      result.DataQualityScore,
		DataQualityGrade:      string(result.DataQualityGrade),
	}

	h.logger.Info("Fair value calculation completed",
		zap.String("ticker", ticker),
		zap.Float64("dcf_value", result.DCFValuePerShare),
		zap.Float64("tangible_value", result.TangibleValuePerShare))

	c.JSON(http.StatusOK, response)
}

// GetBulkFairValue handles POST /api/v1/fair-value/bulk
func (h *FairValueHandler) GetBulkFairValue(c *gin.Context) {
	var request BulkFairValueRequest

	if err := c.ShouldBindJSON(&request); err != nil {
		h.sendError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"Invalid request format",
			"Request body does not match expected format",
			map[string]interface{}{"validation_error": err.Error()})
		return
	}

	h.logger.Info("Processing bulk fair value request",
		zap.Int("ticker_count", len(request.Tickers)),
		zap.Strings("tickers", request.Tickers))

	results := make([]FairValueResponse, 0, len(request.Tickers))
	successful := 0
	failed := 0

	// Process each ticker
	for _, ticker := range request.Tickers {
		ticker = strings.ToUpper(ticker)

		if !isValidTicker(ticker) {
			h.logger.Warn("Skipping invalid ticker in bulk request", zap.String("ticker", ticker))
			failed++
			continue
		}

		// Calculate valuation for this ticker
		result, err := h.valuationService.CalculateValuation(c.Request.Context(), ticker)
		if err != nil {
			h.logger.Warn("Valuation failed for ticker in bulk request",
				zap.String("ticker", ticker),
				zap.Error(err))
			failed++
			continue
		}

		// Add to results
		response := FairValueResponse{
			Ticker:                ticker,
			WACC:                  result.WACC,
			GrowthRate:            result.GrowthRate,
			TangibleValuePerShare: result.TangibleValuePerShare,
			DCFValuePerShare:      result.DCFValuePerShare,
			AsOf:                  result.CalculatedAt.Format("2006-01-02T15:04:05Z"),
			DataQualityScore:      result.DataQualityScore,
			DataQualityGrade:      string(result.DataQualityGrade),
		}

		results = append(results, response)
		successful++
	}

	// Create bulk response
	bulkResponse := BulkFairValueResponse{
		Results: results,
		Summary: BulkSummary{
			TotalRequested: len(request.Tickers),
			Successful:     successful,
			Failed:         failed,
		},
	}

	h.logger.Info("Bulk fair value calculation completed",
		zap.Int("successful", successful),
		zap.Int("failed", failed))

	c.JSON(http.StatusOK, bulkResponse)
}

// Helper functions

// sendError sends an RFC 7807 compliant error response
func (h *FairValueHandler) sendError(c *gin.Context, status int, errorType, title, detail string, context map[string]interface{}) {
	errorResponse := ErrorResponse{
		Type:     "https://api.dcf-valuation.com/errors/" + errorType,
		Title:    title,
		Status:   status,
		Detail:   detail,
		Instance: c.Request.URL.Path,
		Context:  context,
	}

	c.JSON(status, errorResponse)
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
