package profile_test

// Pin-input builders for the Tier 2 P4 (VAL-3 P3) cross-model regression
// suite. Shared between the build-tag-gated TestCaptureFFOPins helper and
// the always-on TestTier2_EQIX_Pin / TestTier2_PLD_Pin tests so both code
// paths exercise byte-identical synthetic inputs.
//
// EQIX (data-center REIT) uses the existing
// testhelpers.BuildSyntheticDataCenterREITInput. PLD (industrial REIT) is
// built inline because the testhelpers package does not yet ship an
// industrial-REIT builder and Tier 2 P4 owns only ffo.go, ffo_test.go,
// pins.go, tier2_regression_test.go, and config/assumption_profiles.json
// per the worktree brief.
//
// Both inputs are stamped with FilingDate so FFOModel.GetLatestPeriod
// resolves the newest period deterministically. Profile fields mirror the
// rows P4 adds to config/assumption_profiles.json — EQIX takes the
// reit_datacenter:high_growth shape (horizon=5, terminal=28.0); PLD takes
// reit_industrial:standard_growth (horizon=3, terminal=22.5).

import (
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile/testhelpers"
)

// stampHistoricalFilingDates back-fills FilingDate on every fixture
// period from AsOf so HistoricalFinancialData.GetLatestPeriod picks the
// newest row by date. The shared synthetic builders deliberately set
// only AsOf (mirroring real entity construction); the FFO model keys
// "latest" off FilingDate, so the pin tests stamp this themselves.
func stampHistoricalFilingDates(input *models.ModelInput) {
	if input == nil || input.HistoricalData == nil {
		return
	}
	for _, period := range input.HistoricalData.Data {
		if period == nil {
			continue
		}
		if period.FilingDate.IsZero() {
			period.FilingDate = period.AsOf
		}
	}
}

// buildEQIXPinInput returns the synthetic data-center REIT input + the
// reit_datacenter:high_growth profile pinned via direct construction
// (not via the resolver — pin tests deliberately bypass the resolver to
// isolate FFO model drift from resolver drift).
func buildEQIXPinInput(t *testing.T) *models.ModelInput {
	t.Helper()
	input := testhelpers.BuildSyntheticDataCenterREITInput(t)
	stampHistoricalFilingDates(input)
	input.Profile = &profile.ResolvedProfile{
		AssumptionProfile: profile.AssumptionProfile{
			ProfileID:         "reit_datacenter:high_growth",
			Archetype:         profile.ArchetypeREITDataCenter,
			Maturity:          profile.MaturityHighGrowth,
			HorizonYears:      5,
			CompoundGrowthCap: 1.8,
			RevenueBaseMethod: profile.RevenueBaseRawTTM,
			DiscountMethod:    profile.DiscountCostOfEquity,
			TerminalMethod:    profile.TerminalExitMultiple,
			FadeYears:         1,
			TerminalMultiple:  28.0,
		},
	}
	return input
}

// buildPLDPinInput returns a synthetic industrial-REIT input (PLD-ish)
// + the reit_industrial:standard_growth profile. Built inline because
// the shared testhelpers fixtures do not yet expose an industrial REIT.
// Numbers are representative (not actual PLD 10-K values): TTM revenue
// ~$8B, FFO ~$3.2B (NI + D&A), 740M diluted shares — typical large
// industrial REIT shape.
func buildPLDPinInput(t *testing.T) *models.ModelInput {
	t.Helper()
	const (
		shares     = 740_000_000.0
		debt       = 32_000_000_000.0
		cash       = 1_000_000_000.0
		revenueTTM = 8_000_000_000.0
		netIncome  = 1_400_000_000.0
		da         = 1_800_000_000.0
		propGains  = 100_000_000.0
		operIncome = 3_000_000_000.0
	)

	latest := &entities.FinancialData{
		Ticker:                      "PLD",
		Revenue:                     revenueTTM,
		OperatingIncome:             operIncome,
		NormalizedOperatingIncome:   operIncome,
		NetIncome:                   netIncome,
		DepreciationAndAmortization: da,
		GainOnPropertySales:         propGains,
		InterestBearingDebt:         debt,
		CashAndCashEquivalents:      cash,
		StockholdersEquity:          25_000_000_000,
		TaxRate:                     0.21,
		AsOf:                        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		FilingDate:                  time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		FilingPeriod:                "2026FY",
	}

	// 5-year revenue history (oldest → newest) — gentle steady growth
	// mirroring a mature industrial REIT.
	revenueHistory := []float64{6.2e9, 6.7e9, 7.1e9, 7.5e9, 8.0e9}
	data := make(map[string]*entities.FinancialData, len(revenueHistory))
	latestYear := latest.AsOf.Year()
	for i, rev := range revenueHistory {
		periodYear := latestYear - (len(revenueHistory) - 1 - i)
		key := formatFYKey(periodYear)
		clone := *latest
		clone.Revenue = rev
		periodTime := time.Date(periodYear, 12, 31, 0, 0, 0, 0, time.UTC)
		clone.AsOf = periodTime
		clone.FilingDate = periodTime
		clone.FilingPeriod = key
		data[key] = &clone
	}

	historical := &entities.HistoricalFinancialData{
		Ticker: latest.Ticker,
		Data:   data,
	}

	return &models.ModelInput{
		HistoricalData: historical,
		MarketData: &entities.MarketData{
			Ticker:     "PLD",
			SharePrice: 130.0,
			Beta:       0.95,
		},
		MacroData: &entities.MacroData{
			RiskFreeRate:      0.04,
			MarketRiskPremium: 0.06,
		},
		GrowthEstimate: &entities.GrowthEstimate{
			ProjectedGrowthRates: []float64{0.06, 0.05, 0.04, 0.04, 0.03, 0.03, 0.03},
			TerminalGrowthRate:   0.025,
			Confidence:           "medium",
		},
		Industry:               "REIT_INDUSTRIAL",
		WACC:                   0.075,
		CostOfEquity:           0.085,
		TaxRate:                0.21,
		SharesOutstanding:      shares,
		InterestBearingDebt:    debt,
		CashAndCashEquivalents: cash,
		Now:                    func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) },
		Profile: &profile.ResolvedProfile{
			AssumptionProfile: profile.AssumptionProfile{
				ProfileID:         "reit_industrial:standard_growth",
				Archetype:         profile.ArchetypeREITIndustrial,
				Maturity:          profile.MaturityStandardGrowth,
				HorizonYears:      3,
				CompoundGrowthCap: 1.4,
				RevenueBaseMethod: profile.RevenueBaseRawTTM,
				DiscountMethod:    profile.DiscountCostOfEquity,
				TerminalMethod:    profile.TerminalExitMultiple,
				Stabilized:        true,
				FadeYears:         0,
				TerminalMultiple:  22.5,
			},
		},
	}
}

// formatFYKey returns the "YYYYFY" period key used by HistoricalFinancialData.
func formatFYKey(year int) string {
	const digits = "0123456789"
	buf := make([]byte, 0, 6)
	if year < 0 {
		year = -year
	}
	if year == 0 {
		buf = append(buf, '0')
	} else {
		var rev []byte
		for year > 0 {
			rev = append(rev, digits[year%10])
			year /= 10
		}
		for i := len(rev) - 1; i >= 0; i-- {
			buf = append(buf, rev[i])
		}
	}
	buf = append(buf, 'F', 'Y')
	return string(buf)
}
