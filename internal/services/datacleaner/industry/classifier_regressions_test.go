package industry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestClassifyIndustry_AMD_NotRetail pins the AMD regression.
//
// Before the fix, AMD (Advanced Micro Devices, SIC 3674 semiconductor) was
// misclassified as retail ("25" Consumer Discretionary) by the heuristic
// classifier because its balance sheet (post-Xilinx acquisition) shows
// moderate inventory (~12% of assets) and high intangibles (~40% of assets) —
// inputs that the old isRetailCompany predicate matched on.
//
// Real retailers don't run 25% R&D-to-revenue or ~8% SBC-to-revenue.
// AMD does. The fix adds R&D and SBC early-return guards to isRetailCompany
// and re-orders ClassifyIndustry so the technology predicate runs first.
func TestClassifyIndustry_AMD_NotRetail(t *testing.T) {
	classifier := NewIndustryClassifier()

	// AMD-realistic FY2023-ish numbers (in $M equivalents; scale is irrelevant to ratios):
	//   TotalAssets=70B, Inventory=8.5B (12%), IntangibleAssets=28B (40%),
	//   TangibleAssets=42B (60%), Revenue=23B, R&D=5.8B (25% of revenue),
	//   SBC=1.9B (~8% of revenue).
	data := &entities.FinancialData{
		Ticker:                 "AMD",
		TotalAssets:            70_000,
		Inventory:              8_500,
		IntangibleAssets:       28_000,
		TangibleAssets:         42_000,
		Revenue:                23_000,
		ResearchAndDevelopment: 5_800,
		StockBasedCompensation: 1_900,
	}

	sectorConfig, err := classifier.ClassifyIndustry("AMD", data)
	require.NoError(t, err)
	require.NotNil(t, sectorConfig)

	// AMD must NOT land in Consumer Discretionary / retail.
	assert.NotEqual(t, "25", sectorConfig.SectorCode,
		"AMD should not be classified as Consumer Discretionary (retail)")
	assert.NotEqual(t, "Consumer Discretionary", sectorConfig.SectorName,
		"AMD should not be classified as Consumer Discretionary")

	// With R&D > 10% of revenue and SBC > 5% of revenue, AMD is unambiguously tech.
	assert.Equal(t, "45", sectorConfig.SectorCode,
		"AMD should be classified as Information Technology (sector 45)")
}

// TestClassifyIndustry_SemiconductorBasket_NotRetail extends the AMD regression to
// other semiconductor / fabless tech companies whose post-acquisition balance sheets
// (high acquired-IP intangibles + working inventory) risk tripping the old retail
// heuristic. None of these should classify as retail after the fix.
func TestClassifyIndustry_SemiconductorBasket_NotRetail(t *testing.T) {
	classifier := NewIndustryClassifier()

	// Shared tech-profile: moderate inventory, substantial intangibles,
	// high R&D intensity, tech-level SBC. Ratios matter, not absolute scale.
	baseData := func() *entities.FinancialData {
		return &entities.FinancialData{
			TotalAssets:            50_000,
			Inventory:              6_000,  // 12% of assets
			IntangibleAssets:       20_000, // 40% of assets
			TangibleAssets:         30_000, // 60% of assets
			Revenue:                18_000,
			ResearchAndDevelopment: 4_000, // ~22% of revenue
			StockBasedCompensation: 1_200, // ~6.7% of revenue
		}
	}

	tests := []struct {
		name   string
		ticker string
	}{
		{name: "NVDA_not_retail", ticker: "NVDA"},
		{name: "INTC_not_retail", ticker: "INTC"},
		{name: "AVGO_not_retail", ticker: "AVGO"},
		{name: "MRVL_not_retail", ticker: "MRVL"},
		{name: "QRVO_not_retail", ticker: "QRVO"},
		{name: "NXPI_not_retail", ticker: "NXPI"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := baseData()
			data.Ticker = tc.ticker

			sectorConfig, err := classifier.ClassifyIndustry(tc.ticker, data)
			require.NoError(t, err)
			require.NotNil(t, sectorConfig)

			assert.NotEqualf(t, "25", sectorConfig.SectorCode,
				"%s should not be classified as retail", tc.ticker)
			assert.NotEqualf(t, "Consumer Discretionary", sectorConfig.SectorName,
				"%s should not be classified as Consumer Discretionary", tc.ticker)
		})
	}
}

// TestIsRetailCompany_RejectsHighRnD exercises the new R&D guard directly.
// A company with 12% inventory + 40% intangibles but 25% R&D intensity is
// NOT a retailer — real retailers have essentially zero R&D.
func TestIsRetailCompany_RejectsHighRnD(t *testing.T) {
	classifier := NewIndustryClassifier()

	data := &entities.FinancialData{
		TotalAssets:            70_000,
		Inventory:              8_400,  // 12% of assets (inside old retail band)
		IntangibleAssets:       28_000, // 40% of assets (old heuristic triggered here)
		TangibleAssets:         42_000, // 60% (inside old <70% branch)
		Revenue:                23_000,
		ResearchAndDevelopment: 5_750, // 25% of revenue — tech, not retail
	}

	assert.False(t, classifier.isRetailCompany(data),
		"company with R&D > 5%% of revenue must not be classified as retail")
}

// TestIsRetailCompany_AcceptsActualRetailer guards against over-tightening.
// The retail predicate succeeds via EITHER of two independent balance-sheet
// branches — intangibles-heavy (brand value) OR tangible-light (asset-light).
// This test pins BOTH branches separately so that deleting one cannot go
// undetected by the GREEN state of the other.
func TestIsRetailCompany_AcceptsActualRetailer(t *testing.T) {
	classifier := NewIndustryClassifier()

	tests := []struct {
		name string
		data *entities.FinancialData
		// branchDescription documents which predicate branch this case targets.
		branchDescription string
	}{
		{
			// Asset-light specialty retailer — stores leased, low owned PP&E,
			// moderate brand intangibles. Hits the tangibles <70% branch.
			// Intangibles ratio is 5% (below the 10% intangibles branch threshold),
			// so the intangibles branch MUST NOT fire — only tangibles branch can.
			name: "tangibles_branch_assetLight",
			data: &entities.FinancialData{
				TotalAssets:            50_000,
				Inventory:              11_000, // 22% of assets — classic retailer
				IntangibleAssets:       2_500,  // 5% of assets — below intangibles branch
				TangibleAssets:         32_500, // 65% of assets — triggers tangibles branch
				Revenue:                80_000,
				ResearchAndDevelopment: 0,
				StockBasedCompensation: 400, // 0.5% of revenue
			},
			branchDescription: "tangibleRatio < 0.70 branch",
		},
		{
			// Owned-store retailer with meaningful acquired brand value —
			// think a department store that owns its real estate and has
			// bought smaller brands. Hits the intangibles >10% branch.
			// Tangibles ratio is 75% (above the <70% tangibles threshold),
			// so the tangibles branch MUST NOT fire — only intangibles branch can.
			name: "intangibles_branch_brandHeavy",
			data: &entities.FinancialData{
				TotalAssets:            50_000,
				Inventory:              10_000, // 20% of assets
				IntangibleAssets:       6_000,  // 12% of assets — triggers intangibles branch
				TangibleAssets:         37_500, // 75% of assets — above tangibles branch threshold
				Revenue:                70_000,
				ResearchAndDevelopment: 0,
				StockBasedCompensation: 350, // 0.5% of revenue
			},
			branchDescription: "intangibleRatio > 0.10 branch",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.True(t, classifier.isRetailCompany(tc.data),
				"genuine retailer profile must match via %s", tc.branchDescription)
		})
	}
}
