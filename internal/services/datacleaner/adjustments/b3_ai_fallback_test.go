package adjustments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestB3ContingentLiability_FallbackPolicy is the TDB-3 acceptance pin: the
// B3 contingent-liability probability follows exactly THREE policies —
//
//  1. AI enabled + success  → AI probability, AIProvenance non-nil (SHA-256 hashes)
//  2. AI disabled           → industry heuristic, AIProvenance nil
//  3. AI enabled + FAILED    → industry heuristic (NOT a flat 0.40), AIProvenance nil
//
// Before TDB-3 the AI-failed arm used a hard-coded flat 0.40 instead of the
// industry heuristic. The decisive case below uses a NON-Tech industry code
// (Industrials "20" → 0.70) so it FAILS against the old flat-0.40 code and
// passes only once the AI-failed arm routes through
// getContingentLiabilityProbability — proving the two fallback modes
// (AI-disabled, AI-failed) now share one sector-calibrated policy.
//
// All three cases use the same disclosed totals (100k + 50k + 30k = 180k) so
// the expected amount is purely a function of the policy's probability weight.
func TestB3ContingentLiability_FallbackPolicy(t *testing.T) {
	const (
		contingent     = 100_000.0
		environmental  = 50_000.0
		litigation     = 30_000.0
		totalDisclosed = contingent + environmental + litigation // 180k
	)

	newData := func(ticker string) *entities.FinancialData {
		return &entities.FinancialData{
			Ticker:                   ticker,
			ContingentLiabilities:    contingent,
			EnvironmentalLiabilities: environmental,
			LitigationLiabilities:    litigation,
			TotalAssets:              1_000_000.0,
			Revenue:                  500_000.0,
		}
	}

	t.Run("AI success → AI probability and non-nil provenance", func(t *testing.T) {
		// mockAIService returns ProbabilityPercent=30.0 → 0.30 weight.
		la := NewLiabilityAdjuster(&mockAIService{}, nil).WithAI(true)
		adj := la // SR-1 A3: adapter deleted; call ApplyB3Contingent directly
		rule := productionContingentLiabilitiesRule()
		// IndustryCode "45" (Tech) on purpose: if the AI path were somehow
		// bypassed, the heuristic (0.40) would differ from the AI weight
		// (0.30), so the amount assertion still detects a regression.
		cleaningCtx := &entities.CleaningContext{
			IndustryCode: "45",
			FootnoteText: "Material patent and product-liability disputes ongoing.",
		}

		out, err := adj.ApplyB3Contingent(context.Background(), newData("AI_SUCCESS"), rule, cleaningCtx)
		require.NoError(t, err)
		require.Len(t, out.Overlays, 1)

		overlay := out.Overlays[0]
		assert.InDelta(t, totalDisclosed*b3AIMockResponseProbability, overlay.Amount, 1e-9,
			"AI-success amount = totalContingent * AI probability (180k * 0.30 = 54k)")
		require.NotNil(t, overlay.AIProvenance,
			"AI-success path must capture AIProvenance")
		assert.Len(t, overlay.AIProvenance.PromptHash, 64,
			"AI-success PromptHash must be a 64-char SHA-256 hex digest")
		assert.Len(t, overlay.AIProvenance.SourceDocHash, 64,
			"AI-success SourceDocHash must be a 64-char SHA-256 hex digest")
	})

	t.Run("AI disabled → industry heuristic and nil provenance", func(t *testing.T) {
		// AI service supplied but WithAI NOT called → arm 4 (default).
		// Industrials "20" heuristic = 0.70 → 180k * 0.70 = 126k.
		la := NewLiabilityAdjuster(&mockAIService{}, nil)
		adj := la // SR-1 A3: adapter deleted; call ApplyB3Contingent directly
		rule := productionContingentLiabilitiesRule()
		cleaningCtx := &entities.CleaningContext{IndustryCode: "20"} // Industrials

		out, err := adj.ApplyB3Contingent(context.Background(), newData("AI_DISABLED"), rule, cleaningCtx)
		require.NoError(t, err)
		require.Len(t, out.Overlays, 1)

		overlay := out.Overlays[0]
		assert.InDelta(t, totalDisclosed*0.70, overlay.Amount, 1e-9,
			"AI-disabled amount = totalContingent * industry heuristic (180k * 0.70 = 126k for Industrials)")
		assert.Nil(t, overlay.AIProvenance,
			"AI-disabled heuristic amount is not AI-derived — AIProvenance must be nil")
		assert.Contains(t, overlay.Reasoning, "contingent_liabilities",
			"AI-disabled reasoning must carry the load-bearing 'contingent_liabilities:' prefix")
		assert.Contains(t, overlay.Reasoning, "Conservative",
			"AI-disabled arm 4 reasoning prefix is unchanged ('Conservative')")
	})

	t.Run("AI failed → industry heuristic NOT flat 0.40, nil provenance", func(t *testing.T) {
		// THE DECISIVE TDB-3 CASE. AI enabled + service errors → arm 1.
		// Industrials "20" heuristic = 0.70 → 180k * 0.70 = 126k.
		// Old flat-0.40 code produced 180k * 0.40 = 72k → this FAILS RED.
		la := NewLiabilityAdjuster(&failingAIService{}, nil).WithAI(true)
		adj := la // SR-1 A3: adapter deleted; call ApplyB3Contingent directly
		rule := productionContingentLiabilitiesRule()
		cleaningCtx := &entities.CleaningContext{
			IndustryCode: "20", // Industrials → heuristic 0.70 (NOT 0.40)
			FootnoteText: "Material disputes ongoing.",
		}

		out, err := adj.ApplyB3Contingent(context.Background(), newData("AI_FAILED"), rule, cleaningCtx)
		require.NoError(t, err, "Apply must absorb AI errors, never surface them")
		require.Len(t, out.Overlays, 1)

		overlay := out.Overlays[0]
		assert.InDelta(t, totalDisclosed*0.70, overlay.Amount, 1e-9,
			"AI-failed amount MUST use the industry heuristic (180k * 0.70 = 126k), NOT flat 0.40 (72k)")
		assert.Nil(t, overlay.AIProvenance,
			"AI-failed heuristic amount is not AI-derived — AIProvenance must be nil")
		// Reasoning must name the failure AND the fallback, and keep the prefix.
		assert.Contains(t, overlay.Reasoning, "AI analysis failed",
			"AI-failed reasoning must name the failure mode")
		assert.Contains(t, overlay.Reasoning, "industry heuristic fallback",
			"AI-failed reasoning must name the industry-heuristic fallback (TDB-3)")
		assert.Contains(t, overlay.Reasoning, "contingent_liabilities",
			"AI-failed reasoning must still carry the 'contingent_liabilities:' prefix")
	})

	t.Run("AI failed via legacy direct-call (arm 3) → industry heuristic", func(t *testing.T) {
		// Legacy direct-call entry point passes aiResult=nil, so on AI
		// failure the switch lands in arm 3 (aiGateOpen → err). Energy "21"
		// heuristic = 0.60 → 180k * 0.60 = 108k. Old flat-0.40 → 72k (RED).
		la := NewLiabilityAdjuster(&failingAIService{}, nil).WithAI(true)
		rule := productionContingentLiabilitiesRule()
		cleaningCtx := &entities.CleaningContext{
			IndustryCode: "21", // Energy → heuristic 0.60 (NOT 0.40)
			FootnoteText: "Environmental remediation disputes ongoing.",
		}

		result := la.ProcessContingentLiabilityAdjustment(context.Background(), newData("AI_FAILED_LEGACY"), rule, cleaningCtx)
		require.NotNil(t, result)
		require.True(t, result.Applied)

		assert.InDelta(t, totalDisclosed*0.60, result.Amount, 1e-9,
			"arm-3 AI-failed amount MUST use the industry heuristic (180k * 0.60 = 108k for Energy), NOT flat 0.40")
		require.Len(t, result.Adjustments, 1)
		reasoning := result.Adjustments[0].Reasoning
		assert.Contains(t, reasoning, "AI analysis failed",
			"arm-3 reasoning must name the failure mode")
		assert.Contains(t, reasoning, "industry heuristic fallback",
			"arm-3 reasoning must name the industry-heuristic fallback (TDB-3)")
		assert.Contains(t, reasoning, "contingent_liabilities",
			"arm-3 reasoning must still carry the 'contingent_liabilities:' prefix")
	})
}
