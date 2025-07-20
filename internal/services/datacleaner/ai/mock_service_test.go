package ai

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMockAIService(t *testing.T) {
	config := &AIServiceConfig{
		ServiceType:    "mock",
		MaxTokens:      4000,
		Temperature:    0.7,
		TimeoutSeconds: 30,
	}

	service := NewMockAIService(config)
	assert.NotNil(t, service)
	assert.Equal(t, config, service.config)
	assert.NotNil(t, service.metrics)
}

func TestMockAIService_AnalyzeFootnote_ContingentLiability(t *testing.T) {
	service := NewMockAIService(&AIServiceConfig{})
	ctx := context.Background()

	request := &FootnoteAnalysisRequest{
		Ticker:           "TEST",
		FilingType:       "10-K",
		FootnoteText:     "The Company is subject to various legal proceedings...",
		AnalysisType:     ContingentLiabilityAnalysis,
		PriorityLevel:    PriorityNormal,
		RequestTimestamp: time.Now(),
	}

	response, err := service.AnalyzeFootnote(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, response)

	assert.Equal(t, "TEST", response.Ticker)
	assert.Equal(t, ContingentLiabilityAnalysis, response.AnalysisType)
	assert.Greater(t, response.Confidence, 0.8)
	assert.NotEmpty(t, response.ExtractedData)
	assert.NotEmpty(t, response.Recommendations)
	assert.Len(t, response.Flags, 1)

	// Verify contingent liability data structure
	contingentData, exists := response.ExtractedData["contingent_liability"]
	assert.True(t, exists)
	
	liability, ok := contingentData.(ContingentLiabilityEstimate)
	assert.True(t, ok)
	assert.Equal(t, "litigation", liability.LiabilityType)
	assert.Greater(t, liability.EstimatedAmount, 0.0)
	assert.Equal(t, "reasonably possible", liability.ProbabilityRange)
	assert.Greater(t, liability.ProbabilityPercent, 0.0)
}

func TestMockAIService_AnalyzeFootnote_PensionObligation(t *testing.T) {
	service := NewMockAIService(&AIServiceConfig{})
	ctx := context.Background()

	request := &FootnoteAnalysisRequest{
		Ticker:       "TEST",
		FilingType:   "10-K",
		FootnoteText: "The Company sponsors defined benefit pension plans...",
		AnalysisType: PensionObligationAnalysis,
	}

	response, err := service.AnalyzeFootnote(ctx, request)
	require.NoError(t, err)

	pensionData, exists := response.ExtractedData["pension_obligation"]
	assert.True(t, exists)
	
	pension, ok := pensionData.(PensionObligationData)
	assert.True(t, ok)
	assert.Equal(t, "defined_benefit", pension.PlanType)
	assert.Equal(t, "underfunded", pension.FundingStatus)
	assert.Greater(t, pension.ProjectedBenefit, pension.PlanAssets)
	assert.Equal(t, pension.ProjectedBenefit-pension.PlanAssets, pension.UnfundedAmount)
}

func TestMockAIService_AnalyzeFootnote_OperatingLease(t *testing.T) {
	service := NewMockAIService(&AIServiceConfig{})
	ctx := context.Background()

	request := &FootnoteAnalysisRequest{
		Ticker:       "TEST",
		AnalysisType: OperatingLeaseAnalysis,
		FootnoteText: "Future minimum lease payments under operating leases...",
	}

	response, err := service.AnalyzeFootnote(ctx, request)
	require.NoError(t, err)

	leaseData, exists := response.ExtractedData["operating_lease"]
	assert.True(t, exists)
	
	lease, ok := leaseData.(OperatingLeaseData)
	assert.True(t, ok)
	assert.Greater(t, lease.TotalCommitments, 0.0)
	assert.Len(t, lease.YearlyCommitments, 5)
	assert.Greater(t, lease.WeightedAverageRate, 0.0)
	assert.Greater(t, lease.WeightedAverageTerm, 0.0)
	assert.Less(t, lease.PresentValue, lease.TotalCommitments) // NPV should be less than total
}

func TestMockAIService_AnalyzeFootnote_Restructuring(t *testing.T) {
	service := NewMockAIService(&AIServiceConfig{})
	ctx := context.Background()

	request := &FootnoteAnalysisRequest{
		Ticker:       "TEST",
		AnalysisType: RestructuringAnalysis,
		FootnoteText: "The Company incurred restructuring charges...",
	}

	response, err := service.AnalyzeFootnote(ctx, request)
	require.NoError(t, err)

	restructuringData, exists := response.ExtractedData["restructuring"]
	assert.True(t, exists)
	
	restructuring, ok := restructuringData.(RestructuringData)
	assert.True(t, ok)
	assert.Equal(t, "facility_closure", restructuring.ChargeType)
	assert.False(t, restructuring.RecurringNature)
	assert.Equal(t, restructuring.CashPortion+restructuring.NonCashPortion, restructuring.TotalCharge)
	assert.NotEmpty(t, restructuring.BusinessRationale)
}

func TestMockAIService_BatchAnalyzeFootnotes(t *testing.T) {
	service := NewMockAIService(&AIServiceConfig{})
	ctx := context.Background()

	requests := []*FootnoteAnalysisRequest{
		{
			Ticker:       "TEST1",
			AnalysisType: ContingentLiabilityAnalysis,
			FootnoteText: "Legal proceedings...",
		},
		{
			Ticker:       "TEST2",
			AnalysisType: PensionObligationAnalysis,
			FootnoteText: "Pension plans...",
		},
		{
			Ticker:       "TEST3",
			AnalysisType: OperatingLeaseAnalysis,
			FootnoteText: "Lease commitments...",
		},
	}

	responses, err := service.BatchAnalyzeFootnotes(ctx, requests)
	require.NoError(t, err)
	assert.Len(t, responses, 3)

	// Verify each response
	for i, response := range responses {
		assert.Equal(t, requests[i].Ticker, response.Ticker)
		assert.Equal(t, requests[i].AnalysisType, response.AnalysisType)
		assert.Greater(t, response.Confidence, 0.0)
		assert.NotEmpty(t, response.ExtractedData)
	}
}

func TestMockAIService_GetAnalysisCapabilities(t *testing.T) {
	service := NewMockAIService(&AIServiceConfig{})
	
	capabilities := service.GetAnalysisCapabilities()
	assert.NotEmpty(t, capabilities)
	
	// Verify key capabilities are present
	expectedCapabilities := []FootnoteAnalysisType{
		ContingentLiabilityAnalysis,
		PensionObligationAnalysis,
		OperatingLeaseAnalysis,
		RestructuringAnalysis,
		LitigationAnalysis,
		StockCompensationAnalysis,
	}

	for _, expected := range expectedCapabilities {
		assert.Contains(t, capabilities, expected)
	}
}

func TestMockAIService_HealthCheck(t *testing.T) {
	service := NewMockAIService(&AIServiceConfig{})
	ctx := context.Background()

	err := service.HealthCheck(ctx)
	assert.NoError(t, err)
}

func TestMockAIService_ContextCancellation(t *testing.T) {
	service := NewMockAIService(&AIServiceConfig{})
	
	// Create a context that's already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	request := &FootnoteAnalysisRequest{
		Ticker:       "TEST",
		AnalysisType: ContingentLiabilityAnalysis,
		FootnoteText: "Test footnote...",
	}

	response, err := service.AnalyzeFootnote(ctx, request)
	assert.Error(t, err)
	assert.Nil(t, response)
	assert.Equal(t, context.Canceled, err)
}

func TestMockAIService_UnsupportedAnalysisType(t *testing.T) {
	service := NewMockAIService(&AIServiceConfig{})
	ctx := context.Background()

	request := &FootnoteAnalysisRequest{
		Ticker:       "TEST",
		AnalysisType: FootnoteAnalysisType("unsupported_type"),
		FootnoteText: "Test footnote...",
	}

	response, err := service.AnalyzeFootnote(ctx, request)
	require.NoError(t, err)
	assert.Equal(t, 0.0, response.Confidence)
	
	analysisType, exists := response.ExtractedData["analysis_type"]
	assert.True(t, exists)
	assert.Equal(t, "unsupported", analysisType)
}

func TestMockAIService_Metrics(t *testing.T) {
	service := NewMockAIService(&AIServiceConfig{})
	ctx := context.Background()

	// Initial metrics should be zero
	metrics := service.GetMetrics()
	assert.Equal(t, int64(0), metrics.TotalRequests)
	assert.Equal(t, int64(0), metrics.SuccessfulRequests)

	// Make a request
	request := &FootnoteAnalysisRequest{
		Ticker:       "TEST",
		AnalysisType: ContingentLiabilityAnalysis,
		FootnoteText: "Test footnote...",
	}

	_, err := service.AnalyzeFootnote(ctx, request)
	require.NoError(t, err)

	// Metrics should be updated
	metrics = service.GetMetrics()
	assert.Equal(t, int64(1), metrics.TotalRequests)
	assert.Equal(t, int64(1), metrics.SuccessfulRequests)
	assert.Equal(t, int64(0), metrics.FailedRequests)
}
