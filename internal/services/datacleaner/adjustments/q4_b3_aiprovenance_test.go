package adjustments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/ai"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/industry"
)

// TestQ4_AIProvenance_SHA256_Deterministic is the named per-spec-§8.1
// pin for the Q4 resolution: B3's AIProvenance.PromptHash and
// SourceDocHash are now non-empty SHA-256 hex strings computed
// pre-API-call. Replaces the Phase 2 empty-string TODO.
//
// Determinism: two B3 invocations with identical footnote text produce
// identical hashes (regardless of wall-clock); different footnote text
// produces different hashes. Replay determinism depends on this — a
// future model upgrade that changes the AI RESPONSE leaves the
// PromptHash + SourceDocHash unchanged, so replay attributes drift
// cleanly between "model changed" and "input changed".
func TestQ4_AIProvenance_SHA256_Deterministic(t *testing.T) {
	const (
		ticker       = "ACME"
		footnoteText = "ACME has a contingent loss exposure of $50M tied to ongoing IP litigation."
	)

	classifier := industry.NewIndustryClassifier()
	stub := &q4StubAIService{}
	la := NewLiabilityAdjuster(stub, classifier).WithAI(true)
	rule := &entities.CleaningRule{ID: "contingent_liabilities"}
	cleaningCtx := &entities.CleaningContext{
		IndustryCode: "TECH",
		FootnoteText: footnoteText,
	}
	data := &entities.FinancialData{
		Ticker:                ticker,
		FilingPeriod:          "2024Q4",
		ContingentLiabilities: 50_000_000,
		Revenue:               1_000_000_000,
	}

	out1, err := la.ApplyB3Contingent(context.Background(), data, rule, cleaningCtx)
	require.NoError(t, err)
	require.Len(t, out1.Overlays, 1, "fired B3 must emit one OverlaySpec")
	prov1 := out1.Overlays[0].AIProvenance
	require.NotNil(t, prov1, "AI path with stub service must produce AIProvenance")
	assert.NotEmpty(t, prov1.PromptHash, "PromptHash must be populated")
	assert.NotEmpty(t, prov1.SourceDocHash, "SourceDocHash must be populated")
	assert.Len(t, prov1.PromptHash, 64, "SHA-256 hex digest is exactly 64 chars")
	assert.Len(t, prov1.SourceDocHash, 64)

	// Determinism: second call with identical inputs produces identical hashes.
	out2, err := la.ApplyB3Contingent(context.Background(), data, rule, cleaningCtx)
	require.NoError(t, err)
	require.Len(t, out2.Overlays, 1)
	prov2 := out2.Overlays[0].AIProvenance
	require.NotNil(t, prov2)
	assert.Equal(t, prov1.PromptHash, prov2.PromptHash,
		"PromptHash MUST be deterministic across identical-input invocations")
	assert.Equal(t, prov1.SourceDocHash, prov2.SourceDocHash,
		"SourceDocHash MUST be deterministic across identical-input invocations")

	// SourceDocHash equals SHA-256 hex of the footnote text byte-for-byte.
	expectedSourceHash := sha256HexLocal(footnoteText)
	assert.Equal(t, expectedSourceHash, prov1.SourceDocHash,
		"SourceDocHash MUST equal SHA-256 hex of the footnote text exactly")

	// Sensitivity: a different footnote text produces a different SourceDocHash.
	cleaningCtx2 := &entities.CleaningContext{
		IndustryCode: "TECH",
		FootnoteText: "ACME has a contingent loss exposure of $75M tied to ongoing IP litigation.",
	}
	out3, err := la.ApplyB3Contingent(context.Background(), data, rule, cleaningCtx2)
	require.NoError(t, err)
	require.Len(t, out3.Overlays, 1)
	prov3 := out3.Overlays[0].AIProvenance
	require.NotNil(t, prov3)
	assert.NotEqual(t, prov1.SourceDocHash, prov3.SourceDocHash,
		"different footnote text MUST produce different SourceDocHash")
	assert.NotEqual(t, prov1.PromptHash, prov3.PromptHash,
		"different footnote text MUST produce different PromptHash (footnote is part of prompt)")
}

// sha256HexLocal is the test-local mirror of the production sha256Hex
// helper, kept here so the test does not depend on the unexported
// helper's source location.
func sha256HexLocal(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// q4StubAIService is a minimal AIService stub that returns a fixed
// response. Used to exercise the B3 AI path without network. Pinned
// to high-enough probability that ProcessContingentLiabilityAdjustment
// fires (Applied=true), so the OverlaySpec is emitted and we can
// inspect AIProvenance.
type q4StubAIService struct{}

func (s *q4StubAIService) AnalyzeFootnote(ctx context.Context, request *ai.FootnoteAnalysisRequest) (*ai.FootnoteAnalysisResponse, error) {
	return &ai.FootnoteAnalysisResponse{
		RequestID:    "q4-stub-" + request.Ticker,
		Ticker:       request.Ticker,
		AnalysisType: request.AnalysisType,
		Confidence:   0.85,
		ExtractedData: map[string]interface{}{
			"contingent_liability_estimate": ai.ContingentLiabilityEstimate{
				ProbabilityPercent: 60.0,
				ConfidenceLevel:    0.85,
				SupportingEvidence: []string{"Litigation footnote disclosed material exposure"},
			},
		},
	}, nil
}

func (s *q4StubAIService) BatchAnalyzeFootnotes(ctx context.Context, requests []*ai.FootnoteAnalysisRequest) ([]*ai.FootnoteAnalysisResponse, error) {
	out := make([]*ai.FootnoteAnalysisResponse, 0, len(requests))
	for _, req := range requests {
		resp, err := s.AnalyzeFootnote(ctx, req)
		if err != nil {
			return nil, err
		}
		out = append(out, resp)
	}
	return out, nil
}

func (s *q4StubAIService) GetAnalysisCapabilities() []ai.FootnoteAnalysisType {
	return []ai.FootnoteAnalysisType{ai.ContingentLiabilityAnalysis}
}

func (s *q4StubAIService) HealthCheck(ctx context.Context) error { return nil }
