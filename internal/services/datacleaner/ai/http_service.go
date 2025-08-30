package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// HTTPService is a concrete AIService implementation that calls an external HTTP API
// to analyze footnotes and return structured results.
type HTTPService struct {
	client *http.Client
	cfg    *AIServiceConfig
	logger *zap.Logger
}

// NewHTTPService creates a new HTTP-based AI service client
func NewHTTPService(cfg *AIServiceConfig) *HTTPService {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &HTTPService{
		client: &http.Client{Timeout: timeout},
		cfg:    cfg,
		logger: zap.L().Named("ai-http-service"), // Use global logger by default
	}
}

// NewHTTPServiceWithLogger creates a new HTTP-based AI service client with custom logger
func NewHTTPServiceWithLogger(cfg *AIServiceConfig, logger *zap.Logger) *HTTPService {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &HTTPService{
		client: &http.Client{Timeout: timeout},
		cfg:    cfg,
		logger: logger.Named("ai-http-service"),
	}
}

// AnalyzeFootnote sends a single analysis request to the external AI service.
func (s *HTTPService) AnalyzeFootnote(ctx context.Context, request *FootnoteAnalysisRequest) (*FootnoteAnalysisResponse, error) {
	startTime := time.Now()

	if s == nil || s.cfg == nil || s.cfg.APIEndpoint == "" {
		return nil, fmt.Errorf("invalid HTTP service configuration")
	}

	// Log request (without sensitive footnote text for privacy)
	s.logger.Info("AI footnote analysis request",
		zap.String("ticker", request.Ticker),
		zap.String("analysis_type", string(request.AnalysisType)),
		zap.String("filing_type", request.FilingType),
		zap.String("priority", string(request.PriorityLevel)),
	)

	if s.cfg.APIEndpoint == "" {
		s.logger.Error("AI service not configured",
			zap.String("ticker", request.Ticker),
		)
		return nil, fmt.Errorf("ai http service not configured")
	}

	payload := map[string]interface{}{
		"ticker":        request.Ticker,
		"filing_type":   request.FilingType,
		"footnote_text": request.FootnoteText,
		"analysis_type": request.AnalysisType,
		"context":       request.Context,
		"priority":      request.PriorityLevel,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ai request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.APIEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create ai http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if s.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	}

	resp, err := s.client.Do(httpReq)
	duration := time.Since(startTime)

	if err != nil {
		s.logger.Warn("AI footnote analysis failed",
			zap.String("ticker", request.Ticker),
			zap.String("analysis_type", string(request.AnalysisType)),
			zap.Error(err),
			zap.Float64("duration_ms", float64(duration.Nanoseconds())/1e6),
		)
		return nil, fmt.Errorf("ai http request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.logger.Warn("AI footnote analysis failed",
			zap.String("ticker", request.Ticker),
			zap.String("analysis_type", string(request.AnalysisType)),
			zap.Int("status_code", resp.StatusCode),
			zap.Float64("duration_ms", float64(duration.Nanoseconds())/1e6),
		)
		return nil, fmt.Errorf("ai http request returned status %d", resp.StatusCode)
	}

	var aiResp FootnoteAnalysisResponse
	if err := json.NewDecoder(resp.Body).Decode(&aiResp); err != nil {
		s.logger.Warn("AI footnote analysis failed",
			zap.String("ticker", request.Ticker),
			zap.String("analysis_type", string(request.AnalysisType)),
			zap.Error(err),
			zap.Float64("duration_ms", float64(duration.Nanoseconds())/1e6),
			zap.String("error_type", "decode_failure"),
		)
		return nil, fmt.Errorf("failed to decode ai response: %w", err)
	}

	// Log successful response
	s.logger.Info("AI footnote analysis completed",
		zap.String("ticker", request.Ticker),
		zap.String("analysis_type", string(request.AnalysisType)),
		zap.Float64("confidence", aiResp.Confidence),
		zap.String("request_id", aiResp.RequestID),
		zap.Float64("duration_ms", float64(duration.Nanoseconds())/1e6),
	)

	// TODO: Add Prometheus metrics for AI service performance
	// - Counter: ai_requests_total{status="success|failure", analysis_type="..."}
	// - Histogram: ai_request_duration_seconds{analysis_type="..."}
	// - Gauge: ai_confidence_score{analysis_type="..."}

	return &aiResp, nil
}

// BatchAnalyzeFootnotes sends a batch analysis request; by default this implementation
// iterates requests sequentially to keep behavior simple and reliable. If EnableBatchMode
// is configured, callers can adapt this to a true batch endpoint.
func (s *HTTPService) BatchAnalyzeFootnotes(ctx context.Context, requests []*FootnoteAnalysisRequest) ([]*FootnoteAnalysisResponse, error) {
	results := make([]*FootnoteAnalysisResponse, 0, len(requests))
	for _, req := range requests {
		r, err := s.AnalyzeFootnote(ctx, req)
		if err != nil {
			return results, err
		}
		results = append(results, r)
	}
	return results, nil
}

// GetAnalysisCapabilities returns supported types. Since capabilities are service-specific,
// this returns a broad set and allows the server to reject unsupported ones.
func (s *HTTPService) GetAnalysisCapabilities() []FootnoteAnalysisType {
	return []FootnoteAnalysisType{
		ContingentLiabilityAnalysis,
		PensionObligationAnalysis,
		OperatingLeaseAnalysis,
		RestructuringAnalysis,
		LitigationAnalysis,
		StockCompensationAnalysis,
		DerivativeAnalysis,
		IntangibleValuationAnalysis,
		InventoryObsolescenceAnalysis,
		DeferredTaxAnalysis,
		ComprehensiveAnalysis,
	}
}

// HealthCheck verifies the AI endpoint is reachable.
func (s *HTTPService) HealthCheck(ctx context.Context) error {
	if s == nil || s.cfg == nil || s.cfg.APIEndpoint == "" {
		return fmt.Errorf("ai http service not configured")
	}
	// Lightweight HEAD probe; many services will accept POST only, so we tolerate 405
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, s.cfg.APIEndpoint, nil)
	if err != nil {
		return err
	}
	if s.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	// Accept any response as "reachable"
	return nil
}
