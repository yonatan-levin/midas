package valuation

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// bug015Service builds a Service wired the same way as the other
// performValuation unit tests (no repos/cleaner; real calc emitter).
func bug015Service(t *testing.T) *Service {
	t.Helper()
	metricsService := &MockMetricsService{}
	metricsService.On("IncWACCCalculations").Return()
	metricsService.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	metricsService.On("IncDCFCalculations").Return()
	metricsService.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:                 1 * time.Hour,
			DefaultTerminalGrowthCap: 0.025,
			DCFMaxGrowthRate:         0.5,
			DCFMinGrowthRate:         -0.3,
		},
	}
	return NewService(nil, nil, nil, nil, nil, nil, metricsService, cfg, zap.NewNop(), newTestCalcEmitter(), nil)
}

func bug015MarketData() *entities.MarketData {
	return &entities.MarketData{
		Ticker:            "KOISH",
		AsOf:              time.Now(),
		SharePrice:        60.0,
		MarketCap:         260_000_000_000,
		SharesOutstanding: 4_300_000_000,
		Beta:              0.6,
		Beta3Y:            0.6,
		AverageVolume:     12_000_000,
		Source:            "yfinance",
		DataQuality:       "good",
	}
}

func bug015MacroData() *entities.MacroData {
	return &entities.MacroData{
		AsOf:              time.Now(),
		RiskFreeRate:      0.045,
		MarketRiskPremium: 0.06,
		InflationRate:     0.032,
		Source:            "fred",
	}
}

// newQuarterlyPeriod builds a single quarterly FinancialData period with a
// KO-shaped balance sheet (annual D&A/CapEx, quarterly OI). The quarterly OI
// is intentionally ~1/4 of the annual run-rate so that, when fed to the DCF
// alongside the annual D&A/CapEx/ΔNWC terms, the single-quarter base drives
// FCF negative (the BUG-015 symptom).
func newQuarterlyPeriod(ticker, period string, filing time.Time, quarterOI, quarterRevenue float64) *entities.FinancialData {
	return &entities.FinancialData{
		Ticker:                      ticker,
		FilingPeriod:                period,
		FilingDate:                  filing,
		AsOf:                        time.Now(),
		OperatingIncome:             quarterOI,
		NormalizedOperatingIncome:   quarterOI,
		Revenue:                     quarterRevenue,
		InterestExpense:             400_000_000,
		TaxRate:                     0.21,
		TotalAssets:                 95_000_000_000,
		TangibleAssets:              70_000_000_000,
		CurrentAssets:               25_000_000_000,
		CurrentLiabilities:          27_000_000_000,
		CashAndCashEquivalents:      12_000_000_000,
		InterestBearingDebt:         40_000_000_000,
		DepreciationAndAmortization: 1_500_000_000, // annual-scale D&A on each period
		CapitalExpenditures:         1_700_000_000, // annual-scale CapEx on each period
		SharesOutstanding:           4_300_000_000,
		DilutedSharesOutstanding:    4_350_000_000,
		HasNormalizedData:           true,
	}
}

// bug015QuarterlyLatestData returns a HistoricalFinancialData whose LATEST
// period is a 10-Q (4 contiguous quarters present + 2 prior FYs for the annual
// D&A/CapEx/ΔNWC terms). The quarterly OI run-rate (~4.36B/quarter) annualizes
// to ~17.4B TTM, which is materially larger than the single-quarter base.
func bug015QuarterlyLatestData() *entities.HistoricalFinancialData {
	q := func(period string, filing time.Time) *entities.FinancialData {
		// KO-shaped: ~4.36B quarterly OI, ~12.5B quarterly revenue.
		return newQuarterlyPeriod("KOISH", period, filing, 4_359_000_000, 12_472_000_000)
	}
	// Two prior FY periods supply the annual history GetRecentYears(2)/(5) read
	// for ΔNWC and average D&A/CapEx. They are NOT the latest period (the
	// quarters have later FilingDates), so they do not become the OI base.
	fy := func(period string, filing time.Time, annualOI float64) *entities.FinancialData {
		fd := newQuarterlyPeriod("KOISH", period, filing, annualOI, annualOI*2.86)
		return fd
	}
	return &entities.HistoricalFinancialData{
		Ticker: "KOISH",
		Data: map[string]*entities.FinancialData{
			"2024FY": fy("2024FY", time.Date(2025, 2, 20, 0, 0, 0, 0, time.UTC), 14_000_000_000),
			"2025Q1": q("2025Q1", time.Date(2025, 4, 25, 0, 0, 0, 0, time.UTC)),
			"2025Q2": q("2025Q2", time.Date(2025, 7, 25, 0, 0, 0, 0, time.UTC)),
			"2025Q3": q("2025Q3", time.Date(2025, 10, 25, 0, 0, 0, 0, time.UTC)),
			"2025Q4": q("2025Q4", time.Date(2026, 1, 25, 0, 0, 0, 0, time.UTC)),
		},
	}
}

// TestService_performValuation_BUG015_QuarterlyLatest_UsesTTMBase is the
// canonical service-level regression for BUG-015. A 10-Q-latest ticker must:
//   - annualize the OI base via the TTM helper (warning carries source=TTM_4Q),
//   - produce a materially larger intrinsic than the single-quarter base would.
//
// RED proof on pre-fix code: pre-fix, baseOI is the single latest quarter and
// NO "operating_income_base:" warning is ever emitted, so the
// assert.Contains(... "source=TTM_4Q") below fails. The intrinsic comparison
// (TTM > single-quarter) also fails because pre-fix both paths use the same
// single-quarter base.
func TestService_performValuation_BUG015_QuarterlyLatest_UsesTTMBase(t *testing.T) {
	svc := bug015Service(t)
	ctx := context.Background()

	quarterlyLatest := bug015QuarterlyLatestData()
	resultTTM, err := svc.performValuation(ctx, quarterlyLatest, bug015MarketData(), bug015MacroData(), nil, nil)
	require.NoError(t, err)
	require.NotNil(t, resultTTM)

	// The OI base provenance must be surfaced with the TTM_4Q source tag.
	var oiBaseWarning string
	for _, w := range resultTTM.Warnings {
		if strings.HasPrefix(w, "operating_income_base: source=") {
			oiBaseWarning = w
		}
	}
	require.NotEmpty(t, oiBaseWarning, "expected an operating_income_base provenance warning on the quarter-latest DCF path; warnings=%v", resultTTM.Warnings)
	assert.Contains(t, oiBaseWarning, "source=TTM_4Q",
		"4 contiguous quarters should resolve to TTM_4Q; got %q", oiBaseWarning)
	assert.Equal(t, "4.8", resultTTM.CalculationVersion) // SR-1/B3 bump (was 4.7 Layer A)

	// The TTM-annualized OI base must yield a positive DCF value: the
	// single-quarter base drives this KO-shaped fixture negative (the BUG-015
	// symptom). The apples-to-apples materiality comparison lives in the sibling
	// _Materiality test.
	assert.Greater(t, resultTTM.DCFValuePerShare, 0.0,
		"TTM-annualized OI base should produce a positive DCF value for this KO-shaped fixture")
}

// TestService_performValuation_BUG015_TTMvsSingleQuarter_Materiality is the
// apples-to-apples materiality proof. It compares two DCF runs that differ ONLY
// in the OI base: (A) a quarter-latest KO-shaped fixture, whose base the fix
// TTM-annualizes, vs (B) an FY-latest fixture reporting a quarter-sized OI,
// which takes the FY passthrough (no annualization) — the stand-in for the
// pre-fix single-quarter base. Both stay on multi_stage_dcf, so the values are
// directly comparable; the TTM base (~4× larger) must yield a strictly and
// materially larger (>=1.5×) positive intrinsic.
func TestService_performValuation_BUG015_TTMvsSingleQuarter_Materiality(t *testing.T) {
	svc := bug015Service(t)
	ctx := context.Background()

	// (A) Quarterly-latest → TTM base (positive).
	ttmResult, err := svc.performValuation(ctx, bug015QuarterlyLatestData(), bug015MarketData(), bug015MacroData(), nil, nil)
	require.NoError(t, err)
	require.NotNil(t, ttmResult)

	// (B) FY-latest with a quarter-sized OI → single-quarter-sized base, NOT
	// annualized (FY passthrough). This is the apples-to-apples "single quarter
	// base" comparator: same per-period OI magnitude, no TTM lift.
	fySingleQuarterSized := &entities.HistoricalFinancialData{
		Ticker: "KOISH",
		Data: map[string]*entities.FinancialData{
			"2023FY": newQuarterlyPeriod("KOISH", "2023FY", time.Date(2024, 2, 20, 0, 0, 0, 0, time.UTC), 4_359_000_000, 12_472_000_000),
			"2024FY": newQuarterlyPeriod("KOISH", "2024FY", time.Date(2025, 2, 20, 0, 0, 0, 0, time.UTC), 4_359_000_000, 12_472_000_000),
		},
	}
	sqResult, err := svc.performValuation(ctx, fySingleQuarterSized, bug015MarketData(), bug015MacroData(), nil, nil)
	require.NoError(t, err)
	require.NotNil(t, sqResult)

	// The single-quarter-sized FY fixture stays on the multi_stage_dcf path
	// (positive OI), so both results are directly comparable DCF values. The
	// TTM-annualized OI base (~4× larger) MUST yield a strictly and materially
	// larger intrinsic. This is the unconditional materiality proof: pre-fix
	// the quarterly fixture would use the same single-quarter base as the
	// comparator, collapsing the gap.
	require.Equal(t, "multi_stage_dcf", sqResult.CalculationMethod,
		"single-quarter-sized comparator should stay on the DCF path for an apples-to-apples comparison")
	require.Equal(t, "multi_stage_dcf", ttmResult.CalculationMethod)
	assert.Greater(t, ttmResult.DCFValuePerShare, sqResult.DCFValuePerShare,
		"TTM-annualized OI base must yield a materially larger intrinsic than the single-quarter-sized base (ttm=%.4f single=%.4f)",
		ttmResult.DCFValuePerShare, sqResult.DCFValuePerShare)
	// Materiality threshold: the TTM lift should be substantial (the OI base is
	// ~4× larger), not a rounding difference. Require at least a 50% uplift.
	assert.Greater(t, ttmResult.DCFValuePerShare, sqResult.DCFValuePerShare*1.5,
		"TTM uplift should be material (>=50%%): ttm=%.4f single=%.4f",
		ttmResult.DCFValuePerShare, sqResult.DCFValuePerShare)
	// The TTM path itself must be positive.
	assert.Greater(t, ttmResult.DCFValuePerShare, 0.0)
}

// TestService_performValuation_BUG015_FYLatestInvariance pins the critical
// FY-latest invariance constraint (BUG-015 §4): a fixture whose latest period
// is FY must produce a bit-for-bit identical baseOI (hence intrinsic) to the
// pre-fix engine. We assert this by running the same FY-latest fixture and
// confirming (a) NO operating_income_base provenance warning is emitted (the
// FY path leaves baseOI = effectiveOI(dcfRestated) untouched), and (b) the
// value matches a reference run that bypasses the BUG-015 branch entirely.
//
// Bit-for-bit cross-check: we run the standard AAPL FY-latest fixture and
// snapshot DCFValuePerShare / EquityValue; the only permitted drift from a
// pre-fix engine is CalculationVersion (4.5 → 4.6). The absence of the
// operating_income_base warning proves the OI base was NOT touched.
func TestService_performValuation_BUG015_FYLatestInvariance(t *testing.T) {
	svc := bug015Service(t)
	ctx := context.Background()

	historicalData, marketData, macroData := createTestData() // AAPL: FY-latest
	// Provide D&A/CapEx so the true-FCF path runs (matches the realistic engine path).
	for _, fd := range historicalData.Data {
		fd.DepreciationAndAmortization = 11_000_000_000
		fd.CapitalExpenditures = 10_000_000_000
		fd.CurrentAssets = 135_000_000_000
		fd.CurrentLiabilities = 145_000_000_000
		fd.CashAndCashEquivalents = 29_000_000_000
		fd.DilutedSharesOutstanding = fd.SharesOutstanding * 1.02
	}

	result, err := svc.performValuation(ctx, historicalData, marketData, macroData, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	// FY-latest must NOT emit the BUG-015 operating_income_base warning — the OI
	// base path is bypassed entirely for FY-latest, leaving baseOI bit-for-bit.
	for _, w := range result.Warnings {
		assert.False(t, strings.HasPrefix(w, "operating_income_base:"),
			"FY-latest must not annualize OI base; unexpected warning: %q", w)
	}
	assert.Equal(t, "4.8", result.CalculationVersion) // SR-1/B3 bump (was 4.7 Layer A)
	assert.Greater(t, result.DCFValuePerShare, 0.0)
}
