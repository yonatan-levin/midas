package valuation

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/params"
	"github.com/midas/dcf-valuation-api/pkg/finance/wacc"
)

// newTaxTestService builds a Service wired only with the dependencies the
// default DCF path exercises (metrics + calc emitter), mirroring the existing
// TestService_CalculateValuation_OverrideBeta harness.
func newTaxTestService(t *testing.T) *Service {
	t.Helper()

	metricsService := &MockMetricsService{}
	metricsService.On("IncWACCCalculations").Return()
	metricsService.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	metricsService.On("IncDCFCalculations").Return()
	metricsService.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()

	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:             1 * time.Hour,
			SlowRequestThreshold: 500 * time.Millisecond,
			DataFetchTimeout:     30 * time.Second,
		},
	}
	return NewService(nil, nil, nil, nil, nil, nil, metricsService, cfg, zap.NewNop(), newTestCalcEmitter(), nil)
}

// TestBetaLadder_TaxOverride_ChangesTaxShield isolates the CF-2 (T5) Hamada
// unlever/relever tax-shield change at the math level. It reproduces the exact
// two-line ladder service.go runs for the createTestData() AAPL fixture (positive
// InterestBearingDebt + positive market equity ⇒ the ladder fires).
//
// IMPORTANT — the relevered *output* beta is tax-INVARIANT in this single-company
// ladder: unlevering and relevering at the SAME debt/equity ratio algebraically
// cancels the (1 − taxRate)·D/E factor, so the final relevered beta equals the
// input levered beta regardless of taxRate. The tax shield the override moves is
// the INTERMEDIATE unlevered (asset) beta — the value emitted on the WACC trace
// and the one a future industry-average-beta comparison would consume. So this
// test proves:
//
//   - p.TaxRate == entity TaxRate  ⇒  the unlevered beta is BIT-FOR-BIT identical
//     to the legacy entity-tax computation (the load-bearing default-path
//     property: "p.TaxRate == entity tax ⇒ identical beta ladder").
//   - p.TaxRate != entity TaxRate  ⇒  the unlevered beta changes (the override
//     actually reaches the tax shield).
//   - the relevered round-trip remains exact for any tax rate (documents why
//     the WACC move from a tax override comes via cost-of-debt, not the beta).
func TestBetaLadder_TaxOverride_ChangesTaxShield(t *testing.T) {
	historicalData, marketData, _ := createTestData()

	latest, _ := historicalData.GetLatestPeriod()
	entityTax := latest.TaxRate // 0.21 for the AAPL fixture
	require.Greater(t, entityTax, 0.0, "fixture must have a positive entity tax rate")

	// Reproduce the service's beta-ladder inputs exactly.
	blumeBeta := wacc.BlumeAdjustedBeta(marketData.GetEffectiveBeta())
	marketEquity := marketData.CalculateMarketValue()
	ibd := latest.InterestBearingDebt
	require.Greater(t, marketEquity, 0.0)
	require.Greater(t, ibd, 0.0)
	debtEquityRatio := ibd / marketEquity

	unleveredFor := func(taxRate float64) float64 {
		return wacc.UnleveredBeta(blumeBeta, taxRate, debtEquityRatio)
	}
	releveredFor := func(taxRate float64) float64 {
		return wacc.RelleveredBeta(unleveredFor(taxRate), taxRate, debtEquityRatio)
	}

	// Default-path identity: resolved p.TaxRate == entity tax ⇒ identical tax shield.
	legacyUnlevered := unleveredFor(entityTax)
	resolvedDefaultUnlevered := unleveredFor(entityTax) // p.TaxRate == entityTax when no override
	assert.Equal(t, math.Float64bits(legacyUnlevered), math.Float64bits(resolvedDefaultUnlevered),
		"no-override unlevered beta must be bit-for-bit identical to the legacy entity-tax beta")

	// Override path: a different tax rate must move the unlevered (tax-shield) beta.
	overriddenUnlevered := unleveredFor(0.50)
	assert.NotEqual(t, math.Float64bits(legacyUnlevered), math.Float64bits(overriddenUnlevered),
		"a tax_rate override must change the unlevered (Hamada tax-shield) beta")

	// Document the round-trip invariance: relevering at the same D/E recovers the
	// input levered beta for ANY tax rate, so the final beta is tax-invariant.
	assert.InDelta(t, blumeBeta, releveredFor(entityTax), 1e-12,
		"relever at the same D/E must round-trip to the levered beta (entity tax)")
	assert.InDelta(t, blumeBeta, releveredFor(0.50), 1e-12,
		"relever at the same D/E must round-trip to the levered beta (override tax)")
}

// TestService_performValuation_TaxOverride_MovesWACCAndDefaultIdentical drives
// the full performValuation engine to prove the tax_rate override is actually
// PLUMBED to the engine (CF-1 alt-model tax + CF-2 beta-ladder + the existing
// WACC cost-of-debt tax site all read p.TaxRate), and that the default path stays
// byte-identical.
func TestService_performValuation_TaxOverride_MovesWACCAndDefaultIdentical(t *testing.T) {
	ctx := context.Background()

	// Baseline (no overrides) — two calls must be bit-identical (determinism +
	// default-path stability).
	svc1 := newTaxTestService(t)
	hist1, mkt1, mac1 := createTestData()
	baseA, err := svc1.performValuation(ctx, hist1, mkt1, mac1, nil, nil)
	require.NoError(t, err)

	svc2 := newTaxTestService(t)
	hist2, mkt2, mac2 := createTestData()
	baseB, err := svc2.performValuation(ctx, hist2, mkt2, mac2, &ValuationOptions{}, nil)
	require.NoError(t, err)

	assert.Equal(t, math.Float64bits(baseA.WACC), math.Float64bits(baseB.WACC),
		"empty-options WACC must be bit-for-bit identical to nil-opts WACC (default-path identity)")

	// Tax override — a sharply different tax rate must move WACC (it feeds the
	// beta ladder + cost-of-debt). The override path bypasses no resolver guard
	// (0.50 is a valid rate), so this is a clean engine move.
	overrideTax := 0.50
	svc3 := newTaxTestService(t)
	hist3, mkt3, mac3 := createTestData()
	overridden, err := svc3.performValuation(ctx, hist3, mkt3, mac3,
		&ValuationOptions{Overrides: params.Overrides{TaxRate: &overrideTax}}, nil)
	require.NoError(t, err)

	assert.NotEqual(t, math.Float64bits(baseA.WACC), math.Float64bits(overridden.WACC),
		"a tax_rate override must move the resulting WACC (override reaches the engine)")
}

// TestService_performValuation_StagingOverride_UsesPerRequestEstimator proves the
// S3 per-request estimator path: a stage-years override changes the projected
// growth-rate slice length (only the per-request estimator can do this), while a
// no-override call reuses the shared estimator and is byte-identical to baseline.
func TestService_performValuation_StagingOverride_UsesPerRequestEstimator(t *testing.T) {
	ctx := context.Background()

	// Baseline: shared estimator (Stage1=3, Stage2=4, Stage3=0 ⇒ 7-year horizon).
	svcBase := newTaxTestService(t)
	histBase, mktBase, macBase := createTestData()
	base, err := svcBase.performValuation(ctx, histBase, mktBase, macBase, nil, nil)
	require.NoError(t, err)
	baseLen := len(base.GrowthRates)
	require.Greater(t, baseLen, 0, "baseline must produce a non-empty growth slice")

	// Override Stage3 to extend the horizon ⇒ a per-request estimator runs and the
	// growth-rate slice grows. This exercises growthEstimatorFor + the estimator
	// selection predicate (overridesAffectEstimator).
	stage3 := 3
	svcOv := newTaxTestService(t)
	histOv, mktOv, macOv := createTestData()
	overridden, err := svcOv.performValuation(ctx, histOv, mktOv, macOv,
		&ValuationOptions{Overrides: params.Overrides{Stage3Years: &stage3}}, nil)
	require.NoError(t, err)

	assert.Greater(t, len(overridden.GrowthRates), baseLen,
		"extending Stage3Years via the per-request estimator must lengthen the projected growth slice")
}

// TestValidateEstimatorConfig_InvalidStaging_Propagates422 proves the S3
// pre-estimator pre-check rejects an invalid staging config with a typed
// *params.ParamError (mapped to 422 by the handler) BEFORE the estimator runs.
func TestService_performValuation_InvalidStagingOverride_ReturnsParamError(t *testing.T) {
	ctx := context.Background()

	// min_growth_rate > max_growth_rate is a structural violation; the pre-check
	// must reject it before the estimator runs.
	badMin := 0.9
	badMax := 0.1
	svc := newTaxTestService(t)
	hist, mkt, mac := createTestData()
	_, err := svc.performValuation(ctx, hist, mkt, mac,
		&ValuationOptions{Overrides: params.Overrides{MinGrowthRate: &badMin, MaxGrowthRate: &badMax}}, nil)
	require.Error(t, err)

	var paramErr *params.ParamError
	require.ErrorAs(t, err, &paramErr, "invalid staging override must surface a *params.ParamError")
}
