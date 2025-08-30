package ai

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestHTTPService_LoggingBehavior(t *testing.T) {
	// Setup test observer to capture log entries
	core, logs := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)

	// Create HTTP service with logger
	cfg := &AIServiceConfig{
		APIEndpoint:    "http://mock-ai-service.com/analyze",
		APIKey:         "test-key",
		TimeoutSeconds: 5,
	}

	// Test service with logger injection (will be implemented)
	service := NewHTTPServiceWithLogger(cfg, logger)

	ctx := context.Background()
	request := &FootnoteAnalysisRequest{
		Ticker:       "TEST",
		AnalysisType: ContingentLiabilityAnalysis,
		FootnoteText: "Test footnote for logging",
		FilingType:   "10-K",
	}

	// This test will fail initially as we haven't implemented logging yet
	// Call the service (will fail due to mock endpoint, but should log the attempt)
	_, err := service.AnalyzeFootnote(ctx, request)

	// We expect an error since the endpoint is fake, but logs should be present
	assert.Error(t, err, "Expected error for fake endpoint")

	// Verify logging behavior
	logEntries := logs.All()
	require.Greater(t, len(logEntries), 0, "Expected at least one log entry")

	// Find the AI request log entry
	var requestLog *observer.LoggedEntry
	for _, entry := range logEntries {
		if entry.Message == "AI footnote analysis request" {
			requestLog = &entry
			break
		}
	}

	require.NotNil(t, requestLog, "Expected AI request log entry")
	assert.Equal(t, zapcore.InfoLevel, requestLog.Level)

	// Verify logged fields (no sensitive data)
	fields := requestLog.ContextMap()
	assert.Equal(t, "TEST", fields["ticker"])
	assert.Equal(t, string(ContingentLiabilityAnalysis), fields["analysis_type"])
	assert.Equal(t, "10-K", fields["filing_type"])
	// Should NOT log footnote text for privacy
	assert.NotContains(t, fields, "footnote_text")
}

func TestHTTPService_LoggingFailureScenarios(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)

	cfg := &AIServiceConfig{
		APIEndpoint:    "http://invalid-endpoint.local",
		TimeoutSeconds: 1, // Short timeout to trigger timeout scenario
	}

	service := NewHTTPServiceWithLogger(cfg, logger)

	ctx := context.Background()
	request := &FootnoteAnalysisRequest{
		Ticker:       "FAIL_TEST",
		AnalysisType: ContingentLiabilityAnalysis,
		FootnoteText: "Test footnote",
	}

	// Call service - should fail and log error
	_, err := service.AnalyzeFootnote(ctx, request)
	assert.Error(t, err)

	// Verify error logging
	logEntries := logs.All()
	require.Greater(t, len(logEntries), 0, "Expected error log entries")

	// Find the error log entry
	var errorLog *observer.LoggedEntry
	for _, entry := range logEntries {
		if entry.Message == "AI footnote analysis failed" {
			errorLog = &entry
			break
		}
	}

	require.NotNil(t, errorLog, "Expected AI error log entry")
	assert.Equal(t, zapcore.WarnLevel, errorLog.Level)

	fields := errorLog.ContextMap()
	assert.Equal(t, "FAIL_TEST", fields["ticker"])
	assert.Contains(t, fields, "error")
	assert.Contains(t, fields, "duration_ms")
}

func TestMockService_LoggingBehavior(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)

	cfg := &AIServiceConfig{
		APIEndpoint: "mock://test",
	}

	// Create mock service with logger (will be implemented)
	service := NewMockAIServiceWithLogger(cfg, logger)

	ctx := context.Background()
	request := &FootnoteAnalysisRequest{
		Ticker:       "MOCK_TEST",
		AnalysisType: ContingentLiabilityAnalysis,
		FootnoteText: "Mock footnote",
	}

	// Call service - should succeed and log
	response, err := service.AnalyzeFootnote(ctx, request)
	assert.NoError(t, err)
	assert.NotNil(t, response)

	// Verify logging behavior
	logEntries := logs.All()
	require.Greater(t, len(logEntries), 0, "Expected log entries")

	// Check for both request and response logs
	var requestLog, responseLog *observer.LoggedEntry
	for _, entry := range logEntries {
		switch entry.Message {
		case "AI footnote analysis request":
			requestLog = &entry
		case "AI footnote analysis completed":
			responseLog = &entry
		}
	}

	require.NotNil(t, requestLog, "Expected request log")
	require.NotNil(t, responseLog, "Expected response log")

	// Verify request log
	requestFields := requestLog.ContextMap()
	assert.Equal(t, "MOCK_TEST", requestFields["ticker"])
	assert.Equal(t, string(ContingentLiabilityAnalysis), requestFields["analysis_type"])

	// Verify response log
	responseFields := responseLog.ContextMap()
	assert.Equal(t, "MOCK_TEST", responseFields["ticker"])
	assert.Contains(t, responseFields, "confidence")
	assert.Contains(t, responseFields, "duration_ms")
	assert.Contains(t, responseFields, "request_id")
}

// TestAIServiceLoggingIntegration tests logging in the context of the liability adjuster
func TestAIServiceLoggingIntegration(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)

	cfg := &AIServiceConfig{
		APIEndpoint: "mock://test",
	}

	service := NewMockAIServiceWithLogger(cfg, logger)

	ctx := context.Background()
	request := &FootnoteAnalysisRequest{
		Ticker:       "INTEGRATION_TEST",
		AnalysisType: ContingentLiabilityAnalysis,
		FootnoteText: "The company is subject to litigation with potential losses ranging from $10M to $50M.",
		Context: map[string]interface{}{
			"contingent_liabilities": 25000000,
			"revenue":                1000000000,
		},
	}

	response, err := service.AnalyzeFootnote(ctx, request)
	assert.NoError(t, err)
	assert.NotNil(t, response)

	// Verify comprehensive logging for integration scenario
	logEntries := logs.All()

	// Should have request and response logs
	hasRequestLog := false
	hasResponseLog := false

	for _, entry := range logEntries {
		switch entry.Message {
		case "AI footnote analysis request":
			hasRequestLog = true
			fields := entry.ContextMap()
			assert.Equal(t, "INTEGRATION_TEST", fields["ticker"])
		case "AI footnote analysis completed":
			hasResponseLog = true
			fields := entry.ContextMap()
			assert.Equal(t, "INTEGRATION_TEST", fields["ticker"])
			// Verify AI response metadata is logged
			assert.Contains(t, fields, "confidence")
		}
	}

	assert.True(t, hasRequestLog, "Expected request log")
	assert.True(t, hasResponseLog, "Expected response log")
}
