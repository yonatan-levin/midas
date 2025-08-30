package ai

import (
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
)

// BuildAIService constructs an AIService based on configuration. When AI integration
// is disabled or no endpoint is configured, it returns a mock implementation that
// is deterministic and low-latency for tests.
func BuildAIService(cfg *config.DataCleanerConfig) AIService {
	if cfg == nil {
		return NewMockAIService(&AIServiceConfig{})
	}
	if !cfg.EnableAIIntegration || cfg.AIServiceURL == "" {
		return NewMockAIService(&AIServiceConfig{})
	}

	return NewHTTPService(&AIServiceConfig{
		ServiceType:    "http",
		APIEndpoint:    cfg.AIServiceURL,
		APIKey:         "", // TODO: allow via env
		Model:          "",
		MaxTokens:      0,
		Temperature:    0,
		TimeoutSeconds: int(cfg.AIServiceTimeout.Seconds()),
		RetryAttempts:  2,
	})
}

// BuildAIServiceWithLogger constructs an AIService with logger injection based on configuration.
func BuildAIServiceWithLogger(cfg *config.DataCleanerConfig, logger *zap.Logger) AIService {
	if cfg == nil {
		return NewMockAIServiceWithLogger(&AIServiceConfig{}, logger)
	}
	if !cfg.EnableAIIntegration || cfg.AIServiceURL == "" {
		return NewMockAIServiceWithLogger(&AIServiceConfig{}, logger)
	}

	return NewHTTPServiceWithLogger(&AIServiceConfig{
		ServiceType:    "http",
		APIEndpoint:    cfg.AIServiceURL,
		APIKey:         "", // TODO: allow via env
		Model:          "",
		MaxTokens:      0,
		Temperature:    0,
		TimeoutSeconds: int(cfg.AIServiceTimeout.Seconds()),
		RetryAttempts:  2,
	}, logger)
}
