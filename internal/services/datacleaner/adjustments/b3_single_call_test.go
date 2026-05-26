package adjustments

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/ai"
)

// countingAIService wraps a Probability-fixed stub and counts AnalyzeFootnote
// invocations atomically so tests can assert "exactly one call per B3 fire".
//
// Phase 3 followup (HIGH-2 fix) regression aid: the previous B3 path
// invoked AnalyzeFootnote TWICE per fire — once in
// analyzeContingentLiabilityWithAI for the amount, once in
// captureB3AIProvenance for the AIProvenance record. The unified
// helper invokes AnalyzeFootnote exactly once and returns both. This
// stub's call counter is the regression signal.
type countingAIService struct {
	callCount atomic.Int32
	// probabilityPercent is returned to the caller. Fixed across invocations
	// so divergent values in the recorded amount vs. provenance would be
	// invisible to assertions that look only at probabilities — the
	// "exactly one call" assertion catches the regression instead.
	probabilityPercent float64
	confidence         float64
	supportingEvidence string
}

func (c *countingAIService) AnalyzeFootnote(ctx context.Context, request *ai.FootnoteAnalysisRequest) (*ai.FootnoteAnalysisResponse, error) {
	c.callCount.Add(1)
	return &ai.FootnoteAnalysisResponse{
		RequestID:    "counting-" + request.Ticker,
		Ticker:       request.Ticker,
		AnalysisType: request.AnalysisType,
		Confidence:   c.confidence,
		ExtractedData: map[string]interface{}{
			"contingent_liability_estimate": ai.ContingentLiabilityEstimate{
				ProbabilityPercent: c.probabilityPercent,
				ConfidenceLevel:    c.confidence,
				SupportingEvidence: []string{c.supportingEvidence},
			},
		},
	}, nil
}

func (c *countingAIService) BatchAnalyzeFootnotes(ctx context.Context, requests []*ai.FootnoteAnalysisRequest) ([]*ai.FootnoteAnalysisResponse, error) {
	out := make([]*ai.FootnoteAnalysisResponse, 0, len(requests))
	for _, req := range requests {
		resp, err := c.AnalyzeFootnote(ctx, req)
		if err != nil {
			return nil, err
		}
		out = append(out, resp)
	}
	return out, nil
}

func (c *countingAIService) GetAnalysisCapabilities() []ai.FootnoteAnalysisType {
	return []ai.FootnoteAnalysisType{ai.ContingentLiabilityAnalysis}
}

func (c *countingAIService) HealthCheck(ctx context.Context) error { return nil }

// TestB3AISinglePath_AmountAndProvenance_AreConsistent is the load-bearing
// HIGH-2 regression pin. It exercises the full ApplyB3Contingent path
// against a counting AI stub and asserts two invariants:
//
//  1. AnalyzeFootnote is invoked EXACTLY ONCE per fired B3 — not twice.
//  2. overlay.Amount = totalContingent * overlay.AIProvenance.Probability,
//     i.e. the recorded amount derives from the same AI response that
//     produced the recorded provenance. Audit integrity is preserved.
//
// Before the fix this test would observe callCount == 2 (and could observe
// drift between the recorded probability and the probability that produced
// the amount, since LLM responses are non-deterministic; the counting stub
// makes the drift theoretical but the call-count assertion is the binding
// signal).
func TestB3AISinglePath_AmountAndProvenance_AreConsistent(t *testing.T) {
	stub := &countingAIService{
		probabilityPercent: 55.0,
		confidence:         0.80,
		supportingEvidence: "single-call counting stub evidence",
	}
	la := NewLiabilityAdjuster(stub, nil).WithAI(true)
	rule := &entities.CleaningRule{ID: "contingent_liabilities"}
	cleaningCtx := &entities.CleaningContext{
		IndustryCode: "45",
		FootnoteText: "Material patent and product-liability disputes ongoing.",
	}
	data := &entities.FinancialData{
		Ticker:                   "SCALL",
		FilingPeriod:             "2024Q4",
		ContingentLiabilities:    50_000_000,
		EnvironmentalLiabilities: 20_000_000,
		LitigationLiabilities:    10_000_000,
		TotalAssets:              500_000_000,
		Revenue:                  200_000_000,
	}

	out, err := la.ApplyB3Contingent(context.Background(), data, rule, cleaningCtx)
	require.NoError(t, err)
	require.Len(t, out.Overlays, 1, "fired B3 must emit one OverlaySpec")

	assert.Equal(t, int32(1), stub.callCount.Load(),
		"B3 must invoke AnalyzeFootnote EXACTLY ONCE per fire — pre-fix code invoked it twice")

	overlay := out.Overlays[0]
	require.NotNil(t, overlay.AIProvenance,
		"AI-enabled path with a healthy stub must populate AIProvenance")

	total := data.ContingentLiabilities + data.EnvironmentalLiabilities + data.LitigationLiabilities
	expectedAmount := total * overlay.AIProvenance.Probability
	assert.InDelta(t, expectedAmount, overlay.Amount, 0.01,
		"overlay.Amount MUST equal totalContingent * overlay.AIProvenance.Probability — the recorded amount and the recorded provenance must derive from the same AI response")

	// Sanity: the recorded probability matches the stub's fixed value.
	assert.InDelta(t, stub.probabilityPercent/100.0, overlay.AIProvenance.Probability, 1e-9)
}

// cancelObservingAIService records whether AnalyzeFootnote saw a cancelled
// ctx via ctx.Err() and returns context.Canceled on cancellation.
type cancelObservingAIService struct {
	callCount   atomic.Int32
	sawCanceled atomic.Bool
}

func (c *cancelObservingAIService) AnalyzeFootnote(ctx context.Context, request *ai.FootnoteAnalysisRequest) (*ai.FootnoteAnalysisResponse, error) {
	c.callCount.Add(1)
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			c.sawCanceled.Store(true)
			return nil, err
		}
	}
	return &ai.FootnoteAnalysisResponse{
		RequestID:    "cancel-observer",
		Ticker:       request.Ticker,
		AnalysisType: request.AnalysisType,
		Confidence:   0.5,
		ExtractedData: map[string]interface{}{
			"contingent_liability_estimate": ai.ContingentLiabilityEstimate{
				ProbabilityPercent: 30.0,
				ConfidenceLevel:    0.5,
			},
		},
	}, nil
}

func (c *cancelObservingAIService) BatchAnalyzeFootnotes(ctx context.Context, requests []*ai.FootnoteAnalysisRequest) ([]*ai.FootnoteAnalysisResponse, error) {
	out := make([]*ai.FootnoteAnalysisResponse, 0, len(requests))
	for _, req := range requests {
		resp, err := c.AnalyzeFootnote(ctx, req)
		if err != nil {
			return nil, err
		}
		out = append(out, resp)
	}
	return out, nil
}

func (c *cancelObservingAIService) GetAnalysisCapabilities() []ai.FootnoteAnalysisType {
	return []ai.FootnoteAnalysisType{ai.ContingentLiabilityAnalysis}
}

func (c *cancelObservingAIService) HealthCheck(ctx context.Context) error { return nil }

// TestB3AmountAICall_HonorsContextCancellation is the HIGH-3 regression pin.
// It cancels the parent ctx BEFORE invoking ApplyB3Contingent and asserts
// that the AI call observes the cancellation (either ctx.Err() returns
// context.Canceled inside AnalyzeFootnote, or the AI was never invoked).
//
// Before the fix the amount-path AI call used context.Background()
// internally; even with a cancelled parent ctx, the AI invocation would
// proceed and the cancellation would be silently lost.
func TestB3AmountAICall_HonorsContextCancellation(t *testing.T) {
	stub := &cancelObservingAIService{}
	la := NewLiabilityAdjuster(stub, nil).WithAI(true)
	rule := &entities.CleaningRule{ID: "contingent_liabilities"}
	cleaningCtx := &entities.CleaningContext{
		IndustryCode: "45",
		FootnoteText: "Material litigation footnote",
	}
	data := &entities.FinancialData{
		Ticker:                   "CXL",
		FilingPeriod:             "2024Q4",
		ContingentLiabilities:    1_000_000,
		EnvironmentalLiabilities: 500_000,
		LitigationLiabilities:    250_000,
		TotalAssets:              50_000_000,
		Revenue:                  20_000_000,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := la.ApplyB3Contingent(ctx, data, rule, cleaningCtx)
	// Acceptable signals: either the AI call ran and observed
	// context.Canceled (the stub's ctx.Err() returned the cancellation
	// and was propagated up), OR the AI call observed the cancellation
	// and the legacy fallback absorbed the error. The binding signal is
	// that the cancellation REACHED the AI call's ctx — pre-fix code
	// used context.Background() so this would always be false.
	_ = err // ApplyB3Contingent never propagates AI errors today; it absorbs them
	assert.True(t, stub.sawCanceled.Load() || stub.callCount.Load() == 0,
		"the cancelled parent ctx MUST reach the AI call's ctx — pre-fix code used context.Background() so cancellation was silently dropped")
}

// errorAIService always returns an error, exercising the AI-failure
// fallback path and verifying the legacy conservative-40% reasoning
// branch still works end-to-end after the F.2 refactor.
type errorAIService struct {
	callCount atomic.Int32
}

func (e *errorAIService) AnalyzeFootnote(ctx context.Context, request *ai.FootnoteAnalysisRequest) (*ai.FootnoteAnalysisResponse, error) {
	e.callCount.Add(1)
	return nil, errors.New("AI upstream unavailable")
}

func (e *errorAIService) BatchAnalyzeFootnotes(ctx context.Context, requests []*ai.FootnoteAnalysisRequest) ([]*ai.FootnoteAnalysisResponse, error) {
	return nil, errors.New("AI upstream unavailable")
}

func (e *errorAIService) GetAnalysisCapabilities() []ai.FootnoteAnalysisType {
	return []ai.FootnoteAnalysisType{ai.ContingentLiabilityAnalysis}
}

func (e *errorAIService) HealthCheck(ctx context.Context) error { return nil }

// TestB3AIFailure_FallsBackToConservativeWithoutProvenance pins that the
// F.2 refactor preserves the legacy AI-failure fallback semantics:
// when the unified analyzeContingentLiabilityWithAI errors, ApplyB3Contingent
// computes the amount via the conservative 40% rule-based path and leaves
// AIProvenance nil (no AI response → no provenance). The single-call
// invariant is also preserved: AnalyzeFootnote is invoked EXACTLY ONCE.
func TestB3AIFailure_FallsBackToConservativeWithoutProvenance(t *testing.T) {
	stub := &errorAIService{}
	la := NewLiabilityAdjuster(stub, nil).WithAI(true)
	rule := &entities.CleaningRule{ID: "contingent_liabilities"}
	cleaningCtx := &entities.CleaningContext{
		IndustryCode: "45",
		FootnoteText: "Material litigation footnote",
	}
	data := &entities.FinancialData{
		Ticker:                   "FAIL",
		FilingPeriod:             "2024Q4",
		ContingentLiabilities:    1_000_000,
		EnvironmentalLiabilities: 500_000,
		LitigationLiabilities:    250_000,
		TotalAssets:              50_000_000,
		Revenue:                  20_000_000,
	}

	out, err := la.ApplyB3Contingent(context.Background(), data, rule, cleaningCtx)
	require.NoError(t, err)
	require.Len(t, out.Overlays, 1)

	assert.Equal(t, int32(1), stub.callCount.Load(),
		"AI failure must NOT cause a retry — exactly one AnalyzeFootnote call per fire")

	overlay := out.Overlays[0]
	assert.Nil(t, overlay.AIProvenance,
		"AI failure must leave AIProvenance nil — the recorded amount is rule-based, not AI-derived")

	// Conservative 40% applied to total contingent ($1.75M).
	total := data.ContingentLiabilities + data.EnvironmentalLiabilities + data.LitigationLiabilities
	assert.InDelta(t, total*0.40, overlay.Amount, 0.01,
		"AI failure must engage the conservative 40% fallback (matching pre-followup behavior)")
}
