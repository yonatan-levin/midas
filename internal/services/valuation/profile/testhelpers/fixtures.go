// Synthetic ModelInput builders for Phase Bootstrap. See testhelpers.go for
// the package-level overview and discipline notes.
package testhelpers

import (
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
)

// BuildMXLModelInput returns a ModelInput approximating MXL (negative-OI
// cyclical-trough semi) for P1 RM-3 testing. Synthetic but representative
// of the trough shape: revenue ~$560M TTM, OI ~-$50M, negative growth.
func BuildMXLModelInput(t *testing.T) *models.ModelInput {
	t.Helper()
	latest := &entities.FinancialData{
		Ticker:                    "MXL",
		Revenue:                   560_000_000,
		OperatingIncome:           -50_000_000,
		NormalizedOperatingIncome: -50_000_000,
		NetIncome:                 -75_000_000,
		InterestBearingDebt:       151_000_000,
		CashAndCashEquivalents:    61_000_000,
		StockholdersEquity:        300_000_000,
		TaxRate:                   0.21,
		AsOf:                      time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	}
	historical := buildHistoricalFromLatest(latest, []float64{560e6, 800e6, 1200e6, 950e6, 600e6})
	return &models.ModelInput{
		HistoricalData:         historical,
		MarketData:             buildMarketData("MXL", 80.0, 1.5),
		MacroData:              buildMacroData(0.04, 0.06),
		GrowthEstimate:         buildGrowthEstimate([]float64{0.50, 0.50, 0.41, 0.33, 0.25, 0.16, 0.08}, 0.03, "high"),
		Industry:               "MFG_SEMI",
		WACC:                   0.19,
		CostOfEquity:           0.21,
		TaxRate:                0.21,
		SharesOutstanding:      82_000_000,
		InterestBearingDebt:    151_000_000,
		CashAndCashEquivalents: 61_000_000,
		Now:                    func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) },
	}
}

// BuildSyntheticAAPLishModelInput returns a ModelInput shaped like a
// maturing-tech-first-dividend ticker (AAPL-ish). Used by P3 to test the
// multi-stage DDM path with rising payout.
func BuildSyntheticAAPLishModelInput(t *testing.T) *models.ModelInput {
	t.Helper()
	latest := &entities.FinancialData{
		Ticker:                    "SYNTH_AAPLISH",
		Revenue:                   390_000_000_000,
		OperatingIncome:           115_000_000_000,
		NormalizedOperatingIncome: 115_000_000_000,
		NetIncome:                 95_000_000_000,
		DividendsPerShare:         0.95,
		InterestBearingDebt:       110_000_000_000,
		CashAndCashEquivalents:    65_000_000_000,
		StockholdersEquity:        62_000_000_000,
		TaxRate:                   0.16,
		AsOf:                      time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	}
	historical := buildHistoricalFromLatest(latest, []float64{390e9, 380e9, 365e9, 350e9, 320e9})
	// Patch DPS history so estimateDividendGrowth produces a meaningful CAGR
	// for P3's multi-stage DDM scenario. Periods sorted newest-first by
	// period key (2026FY > 2025FY > 2024FY > 2023FY > 2022FY).
	historical.Data["2026FY"].DividendsPerShare = 0.95
	historical.Data["2025FY"].DividendsPerShare = 0.88
	historical.Data["2024FY"].DividendsPerShare = 0.80
	historical.Data["2023FY"].DividendsPerShare = 0.74
	historical.Data["2022FY"].DividendsPerShare = 0.66
	return &models.ModelInput{
		HistoricalData:         historical,
		MarketData:             buildMarketData("SYNTH_AAPLISH", 190.0, 1.25),
		MacroData:              buildMacroData(0.04, 0.06),
		GrowthEstimate:         buildGrowthEstimate([]float64{0.08, 0.07, 0.06, 0.05, 0.05, 0.04, 0.04, 0.03, 0.03, 0.03}, 0.03, "high"),
		Industry:               "TECH",
		WACC:                   0.10,
		CostOfEquity:           0.11,
		TaxRate:                0.16,
		SharesOutstanding:      15_500_000_000,
		InterestBearingDebt:    110_000_000_000,
		CashAndCashEquivalents: 65_000_000_000,
		Now:                    func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) },
	}
}

// BuildSyntheticDataCenterREITInput returns a ModelInput shaped like a
// data-center REIT (EQIX-ish) for P4 forward-FFO testing.
func BuildSyntheticDataCenterREITInput(t *testing.T) *models.ModelInput {
	t.Helper()
	latest := &entities.FinancialData{
		Ticker:                      "SYNTH_DCREIT",
		Revenue:                     8_000_000_000,
		OperatingIncome:             1_400_000_000,
		NetIncome:                   600_000_000,
		DepreciationAndAmortization: 1_900_000_000,
		GainOnPropertySales:         50_000_000,
		InterestBearingDebt:         16_000_000_000,
		CashAndCashEquivalents:      2_000_000_000,
		StockholdersEquity:          9_000_000_000,
		TaxRate:                     0.21,
		AsOf:                        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	}
	historical := buildHistoricalFromLatest(latest, []float64{8e9, 7.2e9, 6.5e9, 5.9e9, 5.3e9})
	return &models.ModelInput{
		HistoricalData:         historical,
		MarketData:             buildMarketData("SYNTH_DCREIT", 800.0, 0.85),
		MacroData:              buildMacroData(0.04, 0.06),
		GrowthEstimate:         buildGrowthEstimate([]float64{0.12, 0.11, 0.10, 0.08, 0.07, 0.05, 0.04}, 0.03, "high"),
		Industry:               "REIT_DATACENTER",
		WACC:                   0.08,
		CostOfEquity:           0.09,
		TaxRate:                0.21,
		SharesOutstanding:      95_000_000,
		InterestBearingDebt:    16_000_000_000,
		CashAndCashEquivalents: 2_000_000_000,
		Now:                    func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) },
	}
}

// buildHistoricalFromLatest assembles a HistoricalFinancialData with annual
// periods keyed "<year>FY" (matching the production period-key convention
// in entities.HistoricalFinancialData.GetSortedPeriods). The newest period
// (index 0 of revenueHistory) is anchored at the year of latest.AsOf;
// earlier entries step back one calendar year each.
func buildHistoricalFromLatest(latest *entities.FinancialData, revenueHistory []float64) *entities.HistoricalFinancialData {
	data := make(map[string]*entities.FinancialData, len(revenueHistory))
	latestYear := latest.AsOf.Year()
	if latestYear == 0 {
		latestYear = 2026
	}
	for i, rev := range revenueHistory {
		periodYear := latestYear - i
		key := periodKey(periodYear)
		// Shallow clone of latest; mutate only revenue and AsOf so all other
		// fields remain consistent across the synthetic history.
		clone := *latest
		clone.Revenue = rev
		clone.AsOf = time.Date(periodYear, 12, 31, 0, 0, 0, 0, time.UTC)
		data[key] = &clone
	}
	return &entities.HistoricalFinancialData{
		Ticker:  latest.Ticker,
		SICCode: latest.IndustryCode,
		Data:    data,
	}
}

// periodKey formats the FY period key used by entities.HistoricalFinancialData.Data
// (e.g., 2026 -> "2026FY"). See entities.parsePeriodKey for the parsing side.
func periodKey(year int) string {
	// Tiny inline formatter — avoids strconv import for a single use site.
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

func buildMarketData(ticker string, price, beta float64) *entities.MarketData {
	return &entities.MarketData{
		Ticker:            ticker,
		SharePrice:        price,
		Beta:              beta,
		SharesOutstanding: 0, // populated by caller via ModelInput.SharesOutstanding
	}
}

func buildMacroData(rf, erp float64) *entities.MacroData {
	return &entities.MacroData{
		RiskFreeRate:      rf,
		MarketRiskPremium: erp,
	}
}

func buildGrowthEstimate(rates []float64, terminal float64, confidence string) *entities.GrowthEstimate {
	return &entities.GrowthEstimate{
		ProjectedGrowthRates: rates,
		TerminalGrowthRate:   terminal,
		Confidence:           confidence,
	}
}
