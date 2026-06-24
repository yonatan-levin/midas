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
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// fixedProfileRegistry is a minimal profile.Registry test double that resolves
// EVERY ticker to a single supplied profile. It lets the service-level tests
// exercise the Phase-5 DCF seam with the diluted-share-forward flag on/off
// without loading the shipped assumption_profiles.json.
type fixedProfileRegistry struct {
	resolved *profile.ResolvedProfile
}

func (r *fixedProfileRegistry) Resolve(_ profile.Facts) (*profile.ResolvedProfile, profile.ResolutionTrace) {
	return r.resolved, r.resolved.Trace
}

func (r *fixedProfileRegistry) Lookup(_ profile.Archetype, _ profile.Maturity) (*profile.AssumptionProfile, bool) {
	return &r.resolved.AssumptionProfile, true
}

func (r *fixedProfileRegistry) ConfigVersion() string { return "0.0.0-test" }
func (r *fixedProfileRegistry) ConfigHash() string    { return "test-hash" }
func (r *fixedProfileRegistry) MaxHorizonYears() int  { return r.resolved.HorizonYears }

// highSBCData builds a DCF-eligible fixture whose FY diluted share counts grow
// ~5%/yr with non-trivial SBC — the eligibility profile of an NVDA/TSLA-class
// high-SBC grower. Mirrors createTestData()'s DCF inputs so the model router
// selects the DCF path (no dividends, non-financial, non-REIT).
func highSBCData() (*entities.HistoricalFinancialData, *entities.MarketData, *entities.MacroData) {
	mk := func(period string, filing time.Time, oi, normOI, rev, diluted, sbc float64) *entities.FinancialData {
		return &entities.FinancialData{
			Ticker:                    "GROW",
			FilingPeriod:              period,
			Period:                    period,
			FilingDate:                filing,
			AsOf:                      filing,
			OperatingIncome:           oi,
			NormalizedOperatingIncome: normOI,
			Revenue:                   rev,
			InterestExpense:           100000000,
			TaxRate:                   0.21,
			TotalAssets:               200000000000,
			TangibleAssets:            150000000000,
			InterestBearingDebt:       10000000000,
			DilutedSharesOutstanding:  diluted,
			SharesOutstanding:         diluted,
			StockBasedCompensation:    sbc,
			HasNormalizedData:         true,
		}
	}
	hist := &entities.HistoricalFinancialData{
		Ticker: "GROW",
		Data: map[string]*entities.FinancialData{
			// Diluted shares 1000 → 1050 → 1102.5 (≈5%/yr CAGR), SBC present.
			"2021FY": mk("2021FY", time.Date(2022, 1, 15, 0, 0, 0, 0, time.UTC), 40000000000, 38000000000, 100000000000, 1000000000, 5000000000),
			"2022FY": mk("2022FY", time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC), 44000000000, 42000000000, 120000000000, 1050000000, 5500000000),
			"2023FY": mk("2023FY", time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), 48000000000, 46000000000, 140000000000, 1102500000, 6000000000),
		},
	}
	mkt := &entities.MarketData{
		Ticker:            "GROW",
		AsOf:              time.Now(),
		SharePrice:        100.0,
		MarketCap:         110000000000,
		SharesOutstanding: 1102500000,
		Beta:              1.30,
		Beta3Y:            1.25,
		AverageVolume:     50000000,
		Source:            "yfinance",
		DataQuality:       "good",
	}
	mac := &entities.MacroData{
		AsOf:              time.Now(),
		RiskFreeRate:      0.045,
		MarketRiskPremium: 0.06,
		InflationRate:     0.032,
		Source:            "fred",
	}
	return hist, mkt, mac
}

func newDilutedForwardService(t *testing.T, reg profile.Registry) *Service {
	t.Helper()
	metricsService := &MockMetricsService{}
	metricsService.On("IncWACCCalculations").Return()
	metricsService.On("SetAverageWACC", mock.AnythingOfType("float64")).Return()
	metricsService.On("IncDCFCalculations").Return()
	metricsService.On("SetAverageGrowthRate", mock.AnythingOfType("float64")).Return()
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:                 1 * time.Hour,
			SlowRequestThreshold:     500 * time.Millisecond,
			DataFetchTimeout:         30 * time.Second,
			DefaultTerminalGrowthCap: 0.025,
			DCFMaxGrowthRate:         0.5,
			DCFMinGrowthRate:         -0.3,
		},
	}
	return NewService(&MockFinancialDataRepository{}, &MockMarketDataRepository{}, &MockMacroDataRepository{},
		&MockCacheRepository{}, &MockDataCleanerService{}, nil, metricsService, cfg, zap.NewNop(), newTestCalcEmitter(), reg)
}

// dcfProfile returns a non-financial DCF profile with the Phase-5 flag set as
// requested. HorizonYears>0 so the forward projection has a non-trivial horizon.
func dcfProfile(flagOn bool) *profile.ResolvedProfile {
	return &profile.ResolvedProfile{
		AssumptionProfile: profile.AssumptionProfile{
			ProfileID:                  "test:high_sbc",
			Archetype:                  profile.ArchetypeHypergrowthProfitable,
			Maturity:                   profile.MaturityHighGrowth,
			HorizonYears:               5,
			CompoundGrowthCap:          2.0,
			DilutedShareForwardEnabled: flagOn,
			MaxAnnualDilutionRate:      0.10,
		},
	}
}

// TestService_performValuation_DilutedForward_FlagOff_ByteIdentical pins R5: the
// Phase-5 adjustment is a strict no-op when the flag is off. Holding the resolved
// profile FIXED (so Phase-2/3/4 horizon/archetype effects are identical across
// both runs) and toggling ONLY DilutedShareForwardEnabled, the flag-off
// dcf_value_per_share must be bit-for-bit identical to the flag-on run over an
// INELIGIBLE history (flat shares) — i.e. every path where the adjustment does
// not fire produces the same denominator, and both diagnostic fields stay
// omitted. This isolates the Phase-5 code as the only variable.
func TestService_performValuation_DilutedForward_FlagOff_ByteIdentical(t *testing.T) {
	// Ineligible history: flat diluted share count → deriveAnnualDilutionRate
	// returns eligible=false, so even the flag-on run is a no-op. Any residual
	// per-share difference would therefore be a Phase-5 code defect.
	flatData := func() (*entities.HistoricalFinancialData, *entities.MarketData, *entities.MacroData) {
		hist, mkt, mac := highSBCData()
		for _, p := range hist.Data {
			p.DilutedSharesOutstanding = 1102500000 // flat across all FY periods
			p.SharesOutstanding = 1102500000
		}
		return hist, mkt, mac
	}

	histOff, mktOff, macOff := flatData()
	offSvc := newDilutedForwardService(t, &fixedProfileRegistry{resolved: dcfProfile(false)})
	offResult, err := offSvc.performValuation(context.Background(), histOff, mktOff, macOff, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, offResult)
	require.Greater(t, offResult.DCFValuePerShare, 0.0)

	histOn, mktOn, macOn := flatData()
	onSvc := newDilutedForwardService(t, &fixedProfileRegistry{resolved: dcfProfile(true)})
	onResult, err := onSvc.performValuation(context.Background(), histOn, mktOn, macOn, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, onResult)

	assert.Equal(t,
		math.Float64bits(offResult.DCFValuePerShare),
		math.Float64bits(onResult.DCFValuePerShare),
		"no-op path dcf_value_per_share must be byte-identical regardless of the flag")
	assert.Zero(t, offResult.DCFForwardDilutedShares, "flag-off: dcf_forward_diluted_shares omitted")
	assert.Zero(t, offResult.DCFAppliedDilutionRate, "flag-off: dcf_applied_dilution_rate omitted")
	assert.Zero(t, onResult.DCFForwardDilutedShares, "flag-on-ineligible: dcf_forward_diluted_shares omitted")
	assert.Zero(t, onResult.DCFAppliedDilutionRate, "flag-on-ineligible: dcf_applied_dilution_rate omitted")
}

// TestService_performValuation_DilutedForward_FiredRelationship pins the
// fired-path relationship (not an absolute value): forward diluted shares exceed
// the current diluted count and the resulting per-share value is strictly lower
// than the same fixture with the flag off.
func TestService_performValuation_DilutedForward_FiredRelationship(t *testing.T) {
	const currentDiluted = 1102500000.0

	histOff, mktOff, macOff := highSBCData()
	offSvc := newDilutedForwardService(t, &fixedProfileRegistry{resolved: dcfProfile(false)})
	offResult, err := offSvc.performValuation(context.Background(), histOff, mktOff, macOff, nil, nil)
	require.NoError(t, err)

	histOn, mktOn, macOn := highSBCData()
	onSvc := newDilutedForwardService(t, &fixedProfileRegistry{resolved: dcfProfile(true)})
	onResult, err := onSvc.performValuation(context.Background(), histOn, mktOn, macOn, nil, nil)
	require.NoError(t, err)

	assert.Greater(t, onResult.DCFAppliedDilutionRate, 0.0, "adjustment must fire")
	assert.Greater(t, onResult.DCFForwardDilutedShares, currentDiluted,
		"forward diluted shares must exceed the current diluted count")
	assert.Less(t, onResult.DCFValuePerShare, offResult.DCFValuePerShare,
		"a larger per-share denominator must lower dcf_value_per_share")
}

// negativeOIData builds a high-SBC fixture with positive revenue but
// zero/negative operating income, so the model router selects the
// revenue_multiple alt-model (router Rule 3) instead of multi_stage_dcf. The
// Phase-5 seam lives only in the DCF path, so a revenue_multiple result must
// leave both diagnostic fields zero even with the flag enabled. The FY diluted
// share counts still grow (so deriveAnnualDilutionRate WOULD be eligible) —
// proving the omission is because the DCF seam is never reached, not because
// the history is ineligible.
func negativeOIData() (*entities.HistoricalFinancialData, *entities.MarketData, *entities.MacroData) {
	hist, mkt, mac := highSBCData()
	for _, p := range hist.Data {
		p.OperatingIncome = -1000000000           // negative OI → no DCF route
		p.NormalizedOperatingIncome = -1000000000 // both ≤ 0 triggers router Rule 3
	}
	return hist, mkt, mac
}

// TestService_performValuation_DilutedForward_AltModelOmitted is the negative
// pin: an alt-model (revenue_multiple) fixture with the flag on at the profile
// leaves both Phase-5 fields omitted (the adjustment is DCF-path only).
func TestService_performValuation_DilutedForward_AltModelOmitted(t *testing.T) {
	hist, mkt, mac := negativeOIData()
	svc := newDilutedForwardService(t, &fixedProfileRegistry{resolved: dcfProfile(true)})

	result, err := svc.performValuation(context.Background(), hist, mkt, mac, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEqual(t, "multi_stage_dcf", result.CalculationMethod,
		"fixture must route to an alt-model, not the DCF path")
	assert.Zero(t, result.DCFForwardDilutedShares, "alt-model path leaves dcf_forward_diluted_shares omitted")
	assert.Zero(t, result.DCFAppliedDilutionRate, "alt-model path leaves dcf_applied_dilution_rate omitted")
}
