package adjustments

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/ai"
)

// productionContingentLiabilitiesRule returns a CleaningRule whose ID
// matches the production rules.json entry ("contingent_liabilities") so the
// rule reaches the contingent_liabilities branch in
// ProcessLiabilityAdjustments. Mirrors productionOperatingLeasesRule /
// productionPensionRule.
func productionContingentLiabilitiesRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:          "contingent_liabilities",
		Name:        "Contingent Liability Estimation",
		Category:    entities.LiabilityCompleteness,
		Adjustment:  entities.ProbabilityWeighted,
		Description: "Apply probability weighting to disclosed contingent liabilities per B3 rule",
		Enabled:     true,
	}
}

// b3AIMockResponseProbability is the probability returned by the package
// `mockAIService` for ContingentLiabilityAnalysis. Held in a constant so
// the AI-fired-path test can compute the expected weighted amount without
// duplicating the literal.
const b3AIMockResponseProbability = 0.30 // mockAIService returns ProbabilityPercent=30.0

// failingAIService implements the ai.AIService interface but returns an
// error from AnalyzeFootnote. Used to exercise the B3 AI-failure branch:
// AIProvenance must be nil because the recorded amount is the rule-based
// conservative fallback (40%), not an AI-derived value.
type failingAIService struct{}

func (f *failingAIService) AnalyzeFootnote(ctx context.Context, request *ai.FootnoteAnalysisRequest) (*ai.FootnoteAnalysisResponse, error) {
	return nil, errors.New("simulated AI service outage")
}

func (f *failingAIService) BatchAnalyzeFootnotes(ctx context.Context, requests []*ai.FootnoteAnalysisRequest) ([]*ai.FootnoteAnalysisResponse, error) {
	return nil, errors.New("simulated AI service outage")
}

func (f *failingAIService) GetAnalysisCapabilities() []ai.FootnoteAnalysisType {
	return []ai.FootnoteAnalysisType{ai.ContingentLiabilityAnalysis}
}

func (f *failingAIService) HealthCheck(ctx context.Context) error {
	return errors.New("simulated AI service outage")
}

// TestB3ContingentLiabilityAdjuster_Adjuster_Interface_Contract pins the
// DC-1 Phase 2 PR-4 Task 4.3 acceptance gate: b3ContingentLiabilityAdjuster
// satisfies the Adjuster interface AND its AdjusterOutput matches the
// spec / plan §3.5 contracts for the fired (rule-based + AI) and skipped
// paths. Critically, this test pins the **Phase 4 routing intent**:
// OverlaySpec.Field MUST be "DebtLikeClaims" (NOT "TotalDebt") per spec
// §"B3 routing correction" lines 181-189.
//
// The compile-time assertion
// `var _ Adjuster = (*b3ContingentLiabilityAdjuster)(nil)` in liabilities.go
// is the primary signature pin; this test exercises the runtime contract.
func TestB3ContingentLiabilityAdjuster_Adjuster_Interface_Contract(t *testing.T) {
	t.Run("Name returns AdjusterID constant", func(t *testing.T) {
		la := NewLiabilityAdjuster(&mockAIService{}, nil)
		adj := NewB3ContingentLiabilityAdjuster(la)
		require.NotNil(t, adj)
		// Name() contract: stable identifier consumers can join on. Locked
		// to the AdjusterID constant so a rename forces both the test and
		// the constant to move together.
		assert.Equal(t, adjusterIDB3ContingentLiability, adj.Name(),
			"b3ContingentLiabilityAdjuster.Name() must equal the AdjusterID constant")
	})

	t.Run("fired path emits OverlaySpec with Field:DebtLikeClaims", func(t *testing.T) {
		// Rule-based path (AI disabled by default — NewLiabilityAdjuster
		// returns aiEnabled=false). Tech industry probability for
		// contingent liabilities = 0.40 per
		// getContingentLiabilityProbability. Total disclosed = 100k +
		// 50k + 30k = 180k. Expected weighted = 180k * 0.40 = 72k.
		la := NewLiabilityAdjuster(&mockAIService{}, nil)
		adj := NewB3ContingentLiabilityAdjuster(la)
		rule := productionContingentLiabilitiesRule()

		data := &entities.FinancialData{
			Ticker:                   "TECH",
			ContingentLiabilities:    100_000.0,
			EnvironmentalLiabilities: 50_000.0,
			LitigationLiabilities:    30_000.0,
			TotalAssets:              1_000_000.0,
			Revenue:                  500_000.0,
		}
		cleaningCtx := &entities.CleaningContext{IndustryCode: "45"} // Technology

		// Snapshot data fields touched by the dual-write so we can assert
		// Apply is mutation-FREE.
		origTotalDebt := data.TotalDebt
		origInterestBearingDebt := data.InterestBearingDebt

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err, "Apply must not error on a well-formed fired-path input")

		require.Len(t, out.LedgerEntries, 1, "fired path emits exactly one LedgerEntry")
		require.Len(t, out.Overlays, 1, "fired path emits exactly one OverlaySpec on DebtLikeClaims")

		overlay := out.Overlays[0]
		assert.Equal(t, adjusterIDB3ContingentLiability, overlay.OverlayID)
		assert.Equal(t, rule.ID, overlay.RuleID)

		// CRITICAL invariant — Phase 4 routing intent. Phase 2 dual-
		// write still mutates TotalDebt; Phase 4 flips consumers to
		// read OverlaySpec[Field:'DebtLikeClaims']. Spec §"B3 routing
		// correction" lines 181-189.
		assert.Equal(t, "DebtLikeClaims", overlay.Field,
			"Phase 4 routing intent — Phase 2 dual-write still mutates TotalDebt; Phase 4 flips consumer to read OverlaySpec[Field:'DebtLikeClaims']")

		assert.Equal(t, "add", overlay.Operation)
		assert.InDelta(t, 72_000.0, overlay.Amount, 1e-9,
			"overlay amount must equal totalContingent * probabilityWeight (180k * 0.40 for Tech sector)")
		assert.Equal(t, entities.AmountIncremental, overlay.AmountSemantics)
		assert.Contains(t, overlay.Reasoning, "contingent_liabilities",
			"overlay reasoning must carry the 'contingent_liabilities:' prefix (greppable across logs)")

		// LedgerEntry contract (plan §3.5 OverlayEmitter role): Fired=true,
		// AdjusterID matches Name(), Component / DeltaAmount /
		// EquityOffset LEFT UNSET because the declarative amount lives on
		// OverlaySpec.
		entry := out.LedgerEntries[0]
		assert.True(t, entry.Fired, "fired-path LedgerEntry must have Fired=true")
		assert.Equal(t, adjusterIDB3ContingentLiability, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.NotEmpty(t, entry.Reasoning, "Reasoning must be populated for fired entries")
		assert.False(t, entry.Timestamp.IsZero(), "Timestamp must be set on fired entries")
		assert.Empty(t, entry.Component, "B3 is an OverlayEmitter — Component must NOT be set")
		assert.Zero(t, entry.DeltaAmount, "B3 is an OverlayEmitter — DeltaAmount must be zero on the LedgerEntry")
		assert.Zero(t, entry.EquityOffset, "B3 is an OverlayEmitter — EquityOffset must be zero on the LedgerEntry")
		assert.Zero(t, entry.TaxShieldDTA, "B3 is an OverlayEmitter — TaxShieldDTA must be zero on the LedgerEntry")
		assert.Empty(t, entry.SkipReason, "SkipReason must be empty for fired entries")

		// CRITICAL invariant: Apply must NOT mutate `working`. The
		// dispatcher in ProcessLiabilityAdjustments performs the dual-
		// write — Apply is read-only.
		assert.Equal(t, origTotalDebt, data.TotalDebt, "Apply must NOT mutate data.TotalDebt")
		assert.Equal(t, origInterestBearingDebt, data.InterestBearingDebt, "Apply must NOT mutate data.InterestBearingDebt")
	})

	t.Run("fired path with AI enabled populates AIProvenance", func(t *testing.T) {
		// AI enabled — mockAIService returns ContingentLiabilityEstimate
		// with ProbabilityPercent=30.0 / ConfidenceLevel=0.8 and the
		// outer Response.Confidence=0.8 too. Expected weighted amount =
		// 180k * 0.30 = 54k. AIProvenance must capture ModelName,
		// Confidence, Probability, Timestamp; PromptHash + SourceDocHash
		// stay empty per Q4 (Phase 3 TODO).
		la := NewLiabilityAdjuster(&mockAIService{}, nil).WithAI(true)
		adj := NewB3ContingentLiabilityAdjuster(la)
		rule := productionContingentLiabilitiesRule()

		data := &entities.FinancialData{
			Ticker:                   "PHARMA",
			ContingentLiabilities:    100_000.0,
			EnvironmentalLiabilities: 50_000.0,
			LitigationLiabilities:    30_000.0,
			TotalAssets:              1_000_000.0,
			Revenue:                  500_000.0,
		}
		cleaningCtx := &entities.CleaningContext{
			IndustryCode: "62", // Healthcare
			FootnoteText: "Material patent and product-liability disputes ongoing.",
		}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.Overlays, 1)
		overlay := out.Overlays[0]
		assert.Equal(t, "DebtLikeClaims", overlay.Field)
		assert.Equal(t, "add", overlay.Operation)
		assert.InDelta(t, 180_000.0*b3AIMockResponseProbability, overlay.Amount, 1e-9,
			"AI path weighted amount = totalContingent * AI probability (180k * 0.30)")

		// AIProvenance contract — best-effort capture per Q4. Probability
		// + Confidence + ModelName + Timestamp populated; hashes empty
		// (Phase 3 TODO).
		require.NotNil(t, overlay.AIProvenance,
			"AI-enabled fired path must populate AIProvenance")
		assert.Equal(t, b3AIModelName, overlay.AIProvenance.ModelName,
			"AIProvenance.ModelName must equal the canonical b3AIModelName")
		assert.InDelta(t, 0.8, overlay.AIProvenance.Confidence, 1e-9,
			"AIProvenance.Confidence must equal the AI response Confidence")
		assert.InDelta(t, b3AIMockResponseProbability, overlay.AIProvenance.Probability, 1e-9,
			"AIProvenance.Probability must equal the AI response probability (0.30)")
		assert.False(t, overlay.AIProvenance.Timestamp.IsZero(),
			"AIProvenance.Timestamp must be populated")

		// Q4 resolution per plan §10: empty hashes are accepted in Phase 2
		// with a Phase 3 TODO. Phase 3 introduces SHA-256 hashing of the
		// prompt template + footnote source text for replay determinism.
		assert.Empty(t, overlay.AIProvenance.PromptHash,
			"PromptHash must stay empty in Phase 2 — Phase 3 TODO per Q4 resolution (plan §10)")
		assert.Empty(t, overlay.AIProvenance.SourceDocHash,
			"SourceDocHash must stay empty in Phase 2 — Phase 3 TODO per Q4 resolution (plan §10)")
	})

	t.Run("fired path with AI disabled produces nil AIProvenance", func(t *testing.T) {
		// AI service supplied but aiEnabled defaults to false. The
		// rule-based conservative path runs (40% for FIN_INSURANCE).
		// AIProvenance MUST be nil because the recorded amount is not
		// AI-derived.
		la := NewLiabilityAdjuster(&mockAIService{}, nil) // WithAI(true) NOT called
		adj := NewB3ContingentLiabilityAdjuster(la)
		rule := productionContingentLiabilitiesRule()

		data := &entities.FinancialData{
			Ticker:                   "DEFAULT",
			ContingentLiabilities:    100_000.0,
			EnvironmentalLiabilities: 50_000.0,
			LitigationLiabilities:    30_000.0,
			TotalAssets:              1_000_000.0,
			Revenue:                  500_000.0,
		}
		cleaningCtx := &entities.CleaningContext{IndustryCode: "99"} // unmapped → default 30%

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.Overlays, 1)
		overlay := out.Overlays[0]
		assert.Equal(t, "DebtLikeClaims", overlay.Field)
		assert.Nil(t, overlay.AIProvenance,
			"rule-based path (AI disabled) — AIProvenance must be nil because amount is not AI-derived")
	})

	t.Run("fired path with nil AI service produces nil AIProvenance", func(t *testing.T) {
		// la.aiService == nil — even if aiEnabled is somehow set, the
		// guard in ApplyB3Contingent prevents the AI call. Recorded
		// amount is rule-based; AIProvenance must be nil.
		la := NewLiabilityAdjuster(nil, nil).WithAI(true)
		adj := NewB3ContingentLiabilityAdjuster(la)
		rule := productionContingentLiabilitiesRule()

		data := &entities.FinancialData{
			Ticker:                   "DEFAULT",
			ContingentLiabilities:    100_000.0,
			EnvironmentalLiabilities: 50_000.0,
			LitigationLiabilities:    30_000.0,
			TotalAssets:              1_000_000.0,
			Revenue:                  500_000.0,
		}
		cleaningCtx := &entities.CleaningContext{IndustryCode: "99"}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.Overlays, 1)
		assert.Nil(t, out.Overlays[0].AIProvenance,
			"nil AI service — AIProvenance must be nil even when aiEnabled is true")
	})

	t.Run("fired path with failing AI service produces nil AIProvenance", func(t *testing.T) {
		// AI enabled + service returns an error. The legacy path
		// absorbs the failure into a conservative 40% fallback amount
		// (NOT AI-derived). AIProvenance MUST be nil — only AI-derived
		// amounts carry provenance.
		la := NewLiabilityAdjuster(&failingAIService{}, nil).WithAI(true)
		adj := NewB3ContingentLiabilityAdjuster(la)
		rule := productionContingentLiabilitiesRule()

		data := &entities.FinancialData{
			Ticker:                   "AI_OUTAGE",
			ContingentLiabilities:    100_000.0,
			EnvironmentalLiabilities: 50_000.0,
			LitigationLiabilities:    30_000.0,
			TotalAssets:              1_000_000.0,
			Revenue:                  500_000.0,
		}
		cleaningCtx := &entities.CleaningContext{IndustryCode: "45"}

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err, "Apply must NOT surface AI errors — legacy path absorbs them")

		require.Len(t, out.Overlays, 1)
		overlay := out.Overlays[0]
		assert.Equal(t, "DebtLikeClaims", overlay.Field)
		// AI failure → legacy uses 40% conservative fallback. Weighted
		// amount = 180k * 0.40 = 72k.
		assert.InDelta(t, 72_000.0, overlay.Amount, 1e-9,
			"AI-failure path uses 40%% conservative fallback (180k * 0.40)")
		assert.Nil(t, overlay.AIProvenance,
			"AI-failure path — AIProvenance must be nil because recorded amount is the conservative fallback, not AI-derived")
	})

	t.Run("skip path (no contingent-liability data) emits Fired:false LedgerEntry", func(t *testing.T) {
		la := NewLiabilityAdjuster(&mockAIService{}, nil)
		adj := NewB3ContingentLiabilityAdjuster(la)
		rule := productionContingentLiabilitiesRule()

		data := &entities.FinancialData{
			Ticker:                   "TEST",
			ContingentLiabilities:    0.0,
			EnvironmentalLiabilities: 0.0,
			LitigationLiabilities:    0.0,
			TotalAssets:              1_000_000.0,
			Revenue:                  500_000.0,
		}
		cleaningCtx := &entities.CleaningContext{IndustryCode: "45"}

		// Snapshot the dual-write fields to confirm Apply is mutation-FREE
		// on the skip path too.
		origTotalDebt := data.TotalDebt
		origInterestBearingDebt := data.InterestBearingDebt

		out, err := adj.Apply(context.Background(), data, rule, cleaningCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1, "skip path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays, "skip path emits no OverlaySpec")
		assert.Empty(t, out.Flags, "skip path emits no Flags")

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired, "skip-path LedgerEntry must have Fired=false")
		assert.Equal(t, adjusterIDB3ContingentLiability, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.Contains(t, entry.SkipReason, "No contingent liabilities disclosed",
			"SkipReason must use the canonical legacy phrasing")
		// Plan §3.6.6: skipped entries carry zero monetary deltas.
		assert.Zero(t, entry.DeltaAmount)
		assert.Zero(t, entry.EquityOffset)
		assert.Zero(t, entry.TaxShieldDTA)

		// Mutation-free even on the skip path.
		assert.Equal(t, origTotalDebt, data.TotalDebt)
		assert.Equal(t, origInterestBearingDebt, data.InterestBearingDebt)
	})
}

// TestLiabilityAdjuster_ProcessLiabilityAdjustments_NativeB3Emission pins
// the dispatcher's contract: when contingent_liabilities is among the input
// rules AND contingent-liability data is present, ProcessLiabilityAdjustments
// populates
// LiabilityAdjustmentResult.{NativeLedgerEntries,NativeOverlays,NativelyEmittedRuleIDs}
// AND mutates data.TotalDebt / data.InterestBearingDebt exactly as before
// (dual-write preserved — load-bearing for DDM bit-for-bit invariant and
// for the WACC weight that consumers depend on until Phase 4).
//
// Critical: the OverlaySpec records Field:"DebtLikeClaims" (Phase 4
// routing intent) but the dual-write STILL points at TotalDebt — the
// mismatch is intentional per spec §"B3 routing correction" lines 181-189.
func TestLiabilityAdjuster_ProcessLiabilityAdjustments_NativeB3Emission(t *testing.T) {
	// AI enabled so we also exercise the AIProvenance capture path
	// end-to-end through the dispatcher.
	la := NewLiabilityAdjuster(&mockAIService{}, nil).WithAI(true)
	data := &entities.FinancialData{
		Ticker:                   "PHARMA",
		ContingentLiabilities:    100_000.0,
		EnvironmentalLiabilities: 50_000.0,
		LitigationLiabilities:    30_000.0,
		TotalAssets:              1_000_000.0,
		Revenue:                  500_000.0,
		TotalDebt:                400_000.0,
		InterestBearingDebt:      400_000.0,
	}
	rules := []*entities.CleaningRule{productionContingentLiabilitiesRule()}
	cleaningCtx := &entities.CleaningContext{
		IndustryCode: "62", // Healthcare
		FootnoteText: "Material patent and product-liability disputes ongoing.",
	}

	origTotalDebt := data.TotalDebt
	origInterestBearingDebt := data.InterestBearingDebt

	result := la.ProcessLiabilityAdjustments(data, rules, cleaningCtx)
	require.NotNil(t, result)

	// Legacy contract: Applied=true, one Adjustment with the weighted
	// amount.
	assert.True(t, result.Applied)
	require.Len(t, result.Adjustments, 1)
	expectedWeighted := 180_000.0 * b3AIMockResponseProbability
	assert.InDelta(t, expectedWeighted, result.Adjustments[0].Amount, 1e-9,
		"legacy Adjustment.Amount must equal totalContingent * AI probability (180k * 0.30)")

	// Phase 2 PR-4 Task 4.3 native emission contract:
	require.GreaterOrEqual(t, len(result.NativeLedgerEntries), 1,
		"ProcessLiabilityAdjustments must surface the B3 native LedgerEntry")
	require.Len(t, result.NativeOverlays, 1,
		"ProcessLiabilityAdjustments must surface the B3 native OverlaySpec")
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["contingent_liabilities"],
		"contingent_liabilities must appear in NativelyEmittedRuleIDs so the shim skips it")

	// OverlaySpec landed in NativeOverlays — verify Phase 4 routing
	// intent (Field:"DebtLikeClaims") and AIProvenance capture.
	overlay := result.NativeOverlays[0]
	assert.Equal(t, adjusterIDB3ContingentLiability, overlay.OverlayID)
	assert.Equal(t, "DebtLikeClaims", overlay.Field,
		"Native B3 overlay must carry Phase 4 routing intent Field:'DebtLikeClaims'")
	assert.Equal(t, "add", overlay.Operation)
	assert.InDelta(t, expectedWeighted, overlay.Amount, 1e-9)
	require.NotNil(t, overlay.AIProvenance,
		"AI-enabled dispatcher path must propagate AIProvenance on the native overlay")
	assert.Equal(t, b3AIModelName, overlay.AIProvenance.ModelName)

	// Dual-write preserved — data.TotalDebt and data.InterestBearingDebt
	// were mutated as before. This is the FIELD-vs-MUTATION mismatch
	// documented in spec §"B3 routing correction" lines 181-189:
	// OverlaySpec.Field is "DebtLikeClaims" but dispatcher still touches
	// TotalDebt. Phase 4 deletes the dual-write.
	assert.InDelta(t, origTotalDebt+expectedWeighted, data.TotalDebt, 1e-9,
		"dispatcher must add weighted contingent amount to TotalDebt (Phase 2 dual-write, deleted in Phase 4)")
	assert.InDelta(t, origInterestBearingDebt+expectedWeighted, data.InterestBearingDebt, 1e-9,
		"dispatcher must add weighted contingent amount to InterestBearingDebt (Phase 2 dual-write)")
}

// TestLiabilityAdjuster_ProcessLiabilityAdjustments_NativeB3SkipPath
// confirms that on the skip path (no contingent-liability data),
// ProcessLiabilityAdjustments surfaces the Fired:false LedgerEntry through
// NativeLedgerEntries — and the shim path (run later in service.go) skips
// emitting its own generic skip entry for the same rule. The dual-write
// MUST NOT fire on the skip path.
func TestLiabilityAdjuster_ProcessLiabilityAdjustments_NativeB3SkipPath(t *testing.T) {
	la := NewLiabilityAdjuster(&mockAIService{}, nil)
	data := &entities.FinancialData{
		Ticker:                   "TEST",
		ContingentLiabilities:    0.0,
		EnvironmentalLiabilities: 0.0,
		LitigationLiabilities:    0.0,
		TotalAssets:              1_000_000.0,
		Revenue:                  500_000.0,
		TotalDebt:                100_000.0,
		InterestBearingDebt:      100_000.0,
	}
	rules := []*entities.CleaningRule{productionContingentLiabilitiesRule()}
	cleaningCtx := &entities.CleaningContext{IndustryCode: "45"}

	result := la.ProcessLiabilityAdjustments(data, rules, cleaningCtx)
	require.NotNil(t, result)

	// Legacy contract: Applied=false, no Adjustments.
	assert.False(t, result.Applied)
	assert.Empty(t, result.Adjustments)

	// Native emission contract — skip path still emits a Fired:false entry.
	require.Len(t, result.NativeLedgerEntries, 1,
		"skip path must still surface a Fired:false native LedgerEntry")
	assert.False(t, result.NativeLedgerEntries[0].Fired)
	assert.Empty(t, result.NativeOverlays, "skip path emits no Overlays")
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["contingent_liabilities"],
		"contingent_liabilities must appear in NativelyEmittedRuleIDs even on skip path")

	// Dual-write contract — skip path must NOT mutate balance-sheet fields.
	assert.Equal(t, 100_000.0, data.TotalDebt)
	assert.Equal(t, 100_000.0, data.InterestBearingDebt)
}

// TestB3ContingentLiabilityAdjuster_LegacyDirectInvocation pins backward
// compatibility: existing TestLiabilityAdjuster_ProcessContingentLiabilityAdjustment
// callers that invoke ProcessContingentLiabilityAdjustment directly (not
// through the dispatcher's switch arm) still get the legacy
// *AdjustmentResult shape with the same Applied / Amount / Flags behavior.
// This guards against an accidental migration of the legacy method that
// would break the existing test suite.
func TestB3ContingentLiabilityAdjuster_LegacyDirectInvocation(t *testing.T) {
	la := NewLiabilityAdjuster(&mockAIService{}, nil) // AI disabled — rule-based 40% for Tech
	data := &entities.FinancialData{
		Ticker:                   "TECH",
		ContingentLiabilities:    100_000.0,
		EnvironmentalLiabilities: 50_000.0,
		LitigationLiabilities:    30_000.0,
		TotalAssets:              1_000_000.0,
		Revenue:                  500_000.0,
	}
	rule := productionContingentLiabilitiesRule()
	cleaningCtx := &entities.CleaningContext{IndustryCode: "45"} // Tech: 40% probability

	// Direct invocation bypasses the dispatcher's switch arm — must still
	// return a populated legacy result.
	result := la.ProcessContingentLiabilityAdjustment(data, rule, cleaningCtx)
	require.NotNil(t, result)
	assert.True(t, result.Applied)
	assert.InDelta(t, 72_000.0, result.Amount, 1e-9,
		"direct invocation must produce same weighted amount (180k * 0.40)")
	// Apply was NOT called on this path — data must remain unmutated by
	// the legacy method itself (the legacy method only emits the
	// Adjustment; dual-write is the dispatcher's responsibility).
	assert.Equal(t, 0.0, data.TotalDebt,
		"ProcessContingentLiabilityAdjustment does not mutate data — only the dispatcher does")
}
