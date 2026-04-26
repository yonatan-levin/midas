package sec

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

func TestNewParser(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	assert.NotNil(t, parser)
	assert.NotNil(t, parser.logger)
}

func TestParser_ParseFinancialData_Success(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Create mock SEC company facts with nested taxonomy -> concept structure
	facts := &ports.SECCompanyFacts{
		CIK:        "0000320193",
		EntityName: "Apple Inc.",
		Facts: map[string]map[string]ports.SECFactGroup{
			"us-gaap": {
				"Revenues": {
					Label:       "Revenues",
					Description: "Revenue from operations",
					Units: map[string][]ports.SECFact{
						"USD": {
							{
								End:   "2023-09-30",
								Val:   383285000000,
								Accn:  "0000320193-23-000106",
								Fy:    2023,
								Fp:    "FY",
								Form:  "10-K",
								Filed: "2023-11-03",
								Frame: "CY2023Q3I",
							},
						},
					},
				},
				"OperatingIncomeLoss": {
					Label:       "Operating Income Loss",
					Description: "Operating income or loss",
					Units: map[string][]ports.SECFact{
						"USD": {
							{
								End:   "2023-09-30",
								Val:   114301000000,
								Accn:  "0000320193-23-000106",
								Fy:    2023,
								Fp:    "FY",
								Form:  "10-K",
								Filed: "2023-11-03",
								Frame: "CY2023Q3I",
							},
						},
					},
				},
				"Assets": {
					Label:       "Assets",
					Description: "Total assets",
					Units: map[string][]ports.SECFact{
						"USD": {
							{
								End:   "2023-09-30",
								Val:   352755000000,
								Accn:  "0000320193-23-000106",
								Fy:    2023,
								Fp:    "FY",
								Form:  "10-K",
								Filed: "2023-11-03",
								Frame: "CY2023Q3I",
							},
						},
					},
				},
			},
		},
	}

	ctx := context.Background()
	historical, err := parser.ParseFinancialData(ctx, facts)

	require.NoError(t, err)
	assert.NotNil(t, historical)
	assert.Equal(t, "", historical.Ticker) // Ticker is set by caller
	assert.True(t, len(historical.Data) > 0)

	// Check if we parsed the 2023FY period
	data, exists := historical.Data["2023FY"]
	assert.True(t, exists)
	assert.NotNil(t, data)
	assert.Equal(t, "0000320193", data.CIK)
	assert.Equal(t, "2023FY", data.FilingPeriod)
	assert.Equal(t, 383285000000.0, data.Revenue)
	assert.Equal(t, 114301000000.0, data.OperatingIncome)
	assert.Equal(t, 352755000000.0, data.TotalAssets)
}

func TestParser_ParseFinancialData_NilFacts(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	ctx := context.Background()
	historical, err := parser.ParseFinancialData(ctx, nil)

	assert.Error(t, err)
	assert.Nil(t, historical)
	assert.Contains(t, err.Error(), "facts cannot be nil")
}

func TestParser_ParseFinancialData_NoValidData(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Create facts with no valid financial data (no recognized taxonomy/concept)
	facts := &ports.SECCompanyFacts{
		CIK:        "0000320193",
		EntityName: "Apple Inc.",
		Facts: map[string]map[string]ports.SECFactGroup{
			"other": {
				"invalid-concept": {
					Label:       "Invalid",
					Description: "Invalid concept",
					Units: map[string][]ports.SECFact{
						"USD": {
							{
								End:   "2023-09-30",
								Val:   100,
								Accn:  "0000320193-23-000106",
								Fy:    2023,
								Fp:    "FY",
								Form:  "10-K",
								Filed: "2023-11-03",
								Frame: "CY2023Q3I",
							},
						},
					},
				},
			},
		},
	}

	ctx := context.Background()
	historical, err := parser.ParseFinancialData(ctx, facts)

	assert.Error(t, err)
	assert.Nil(t, historical)
	// ParseFinancialData wraps ports.ErrCompanyFactsNotFound when no period
	// has usable US-GAAP data, so the valuation layer can classify this as
	// ErrInsufficientData (→ HTTP 422) rather than ErrTickerNotFound (→ 404).
	assert.True(t, errors.Is(err, ports.ErrCompanyFactsNotFound),
		"parser must wrap ports.ErrCompanyFactsNotFound when all periods lack usable financials; got: %v", err)
	// And it must NOT additionally wrap ErrForeignPrivateIssuer (no IFRS taxonomy
	// in this fixture — this is a generic-no-data case, not an FPI case).
	assert.False(t, errors.Is(err, ports.ErrForeignPrivateIssuer),
		"generic-no-data must not be misclassified as foreign-private-issuer; got: %v", err)
}

// TestParser_ParseFinancialData_ForeignPrivateIssuer_UnmappedConcepts
// pins the FPI classification for 20-F filers whose ifrs-full response
// carries ONLY concepts the parser does not yet recognize (e.g., obscure
// IFRS-full tags outside the Phase B6 mapping table). Such filings reach
// extractFiscalPeriods → parsePeriodData but every period fails the
// "insufficient data: no revenue or operating income" check, so
// historical.Data ends up empty and classifyEmptyParseError fires.
//
// Pre Phase B5/B6 this test exercised a "TWD silently dropped by USD-only
// loop" code path that no longer exists. The fixture has been migrated
// to a Phase-B6-resistant shape (unmapped IFRS concept) so the FPI
// sentinel still ships even after IFRS-full mapping landed.
func TestParser_ParseFinancialData_ForeignPrivateIssuer_UnmappedConcepts(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// IFRS-full taxonomy with an unmapped concept (`OtherComprehensiveIncome`
	// — not in the Phase B6 lookup tables, and unlikely to ever drive
	// valuation directly). The currency loop will extract the period,
	// but parsePeriodData has no Revenue or OperatingIncome to find, so
	// historical.Data ends up empty and the FPI classifier fires.
	facts := &ports.SECCompanyFacts{
		CIK:        "1046179",
		EntityName: "Hypothetical IFRS Filer",
		Facts: map[string]map[string]ports.SECFactGroup{
			"ifrs-full": {
				"OtherComprehensiveIncome": {
					Label: "Other Comprehensive Income",
					Units: map[string][]ports.SECFact{
						"TWD": {
							{End: "2024-12-31", Val: 12345000000, Fy: 2024, Fp: "FY", Form: "20-F", Filed: "2025-04-17"},
						},
					},
				},
			},
		},
	}

	ctx := context.Background()
	historical, err := parser.ParseFinancialData(ctx, facts)

	assert.Error(t, err)
	assert.Nil(t, historical)
	assert.True(t, errors.Is(err, ports.ErrForeignPrivateIssuer),
		"IFRS-only filer with no mappable revenue/op-income concepts must wrap ports.ErrForeignPrivateIssuer; got: %v", err)
	assert.False(t, errors.Is(err, ports.ErrCompanyFactsNotFound),
		"FPI must not be misclassified as missing-companyfacts; got: %v", err)
}

// TestParser_ParseFinancialData_ForeignPrivateIssuer pins the classification
// behavior for 20-F filers whose ifrs-full response carries dei cover-page
// data plus IFRS concepts that the Phase B6 mapping table does not (yet)
// resolve to Revenue or OperatingIncome.
//
// Phase A originally exercised this path with a TSM-shape fixture against a
// USD-only parser loop — but Phase B5/B6 made TSM-shape data parse
// successfully (see TestParser_ParseFinancialData_TSM_IFRS_HappyPath). The
// FPI sentinel must still ship for 20-F filers whose body uses concepts
// outside our coverage (e.g., comprehensive-income-only filings, or
// jurisdiction-specific IFRS extensions like K-IFRS / J-GAAP overlays we
// have not yet mapped). This fixture pins that residual behavior.
func TestParser_ParseFinancialData_ForeignPrivateIssuer(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	facts := &ports.SECCompanyFacts{
		CIK:        "1046179",
		EntityName: "Hypothetical IFRS Filer With Unmapped Concepts",
		Facts: map[string]map[string]ports.SECFactGroup{
			"dei": {
				"EntityCommonStockSharesOutstanding": {
					Label: "Entity Common Stock, Shares Outstanding",
					Units: map[string][]ports.SECFact{
						"shares": {
							{End: "2024-12-31", Val: 25932733242, Fy: 2024, Fp: "FY", Form: "20-F", Filed: "2025-04-17"},
						},
					},
				},
			},
			"ifrs-full": {
				// Concepts intentionally outside the Phase B6 lookup tables.
				// These are real IFRS concepts that exist in some 20-F
				// filings but do not map to Revenue or OperatingIncome —
				// so parsePeriodData hits "insufficient data" and
				// classifyEmptyParseError fires the FPI sentinel.
				"OtherComprehensiveIncome": {
					Label: "Other Comprehensive Income",
					Units: map[string][]ports.SECFact{
						"TWD": {
							{End: "2024-12-31", Val: 50000000000, Fy: 2024, Fp: "FY", Form: "20-F", Filed: "2025-04-17"},
						},
					},
				},
				"GainsLossesOnFinancialAssetsAtFairValueThroughProfitOrLossNet": {
					Label: "Gains Losses On Financial Assets",
					Units: map[string][]ports.SECFact{
						"TWD": {
							{End: "2024-12-31", Val: 12000000000, Fy: 2024, Fp: "FY", Form: "20-F", Filed: "2025-04-17"},
						},
					},
				},
			},
		},
	}

	ctx := context.Background()
	historical, err := parser.ParseFinancialData(ctx, facts)

	assert.Error(t, err)
	assert.Nil(t, historical)
	assert.True(t, errors.Is(err, ports.ErrForeignPrivateIssuer),
		"20-F filer with only unmapped ifrs-full concepts must wrap ports.ErrForeignPrivateIssuer; got: %v", err)
	assert.False(t, errors.Is(err, ports.ErrCompanyFactsNotFound),
		"FPI must not be misclassified as missing-companyfacts; got: %v", err)
}

// TestParser_ExtractFiscalPeriods_TWD_Currency pins Phase B5 currency-capture:
// extractFiscalPeriods must read non-USD currency unit keys (TWD, EUR, CNY,
// JPY, …) and stamp the per-period currency on the *periodPayload struct.
// Pre Phase B5 this fixture would have been silently dropped because the
// loop only iterated `Units["USD"]` and `Units["shares"]`.
func TestParser_ExtractFiscalPeriods_TWD_Currency(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	facts := &ports.SECCompanyFacts{
		CIK:        "1046179",
		EntityName: "TWD-Reporting Filer",
		Facts: map[string]map[string]ports.SECFactGroup{
			"ifrs-full": {
				"Revenue": {
					Label: "Revenue",
					Units: map[string][]ports.SECFact{
						"TWD": {
							{End: "2024-12-31", Val: 2894308000000, Fy: 2024, Fp: "FY", Form: "20-F", Filed: "2025-04-17"},
						},
					},
				},
				"Assets": {
					Label: "Assets",
					Units: map[string][]ports.SECFact{
						"TWD": {
							{End: "2024-12-31", Val: 6654855000000, Fy: 2024, Fp: "FY", Form: "20-F", Filed: "2025-04-17"},
						},
					},
				},
			},
			"dei": {
				"EntityCommonStockSharesOutstanding": {
					Label: "Entity Common Stock, Shares Outstanding",
					Units: map[string][]ports.SECFact{
						"shares": {
							{End: "2024-12-31", Val: 25932733242, Fy: 2024, Fp: "FY", Form: "20-F", Filed: "2025-04-17"},
						},
					},
				},
			},
		},
	}

	periods, err := parser.extractFiscalPeriods(facts)
	require.NoError(t, err)
	require.Len(t, periods, 1, "TWD-only fixture must extract exactly one period (no longer silently dropped)")

	payload, ok := periods["2024FY"]
	require.True(t, ok, "expected 2024FY period")
	require.NotNil(t, payload)

	// Currency stamp must reflect the IFRS-full fact unit, NOT influenced
	// by the dei `shares` fact (calculation-safety contract).
	assert.Equal(t, "TWD", payload.currency,
		"currency stamp must be TWD when monetary facts came from Units[\"TWD\"]")

	// Both monetary facts must be in the values map under their concept names.
	assert.Equal(t, 2894308000000.0, payload.values["Revenue"])
	assert.Equal(t, 6654855000000.0, payload.values["Assets"])
	// Shares fact is dimensionless — same code path as before, no FX.
	assert.Equal(t, 25932733242.0, payload.values["EntityCommonStockSharesOutstanding"])
}

// TestParser_ParseFinancialData_TSM_IFRS_HappyPath pins Phase B6 IFRS-full
// tag mapping end-to-end: a TSM-shape fixture (real 2024FY values from the
// captured artifact) must produce a populated FinancialData struct with
// ReportingCurrency="TWD" and no FPI/INSUFFICIENT_DATA error.
//
// This test is the regression guard for "TSM 422 → 200" in the IFRS / FPI
// support spec (docs/refactoring/ifrs-foreign-private-issuer-support-spec.md).
func TestParser_ParseFinancialData_TSM_IFRS_HappyPath(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	facts := &ports.SECCompanyFacts{
		CIK:        "1046179",
		EntityName: "Taiwan Semiconductor Manufacturing Company Limited",
		Facts: map[string]map[string]ports.SECFactGroup{
			"dei": {
				"EntityCommonStockSharesOutstanding": {
					Label: "Entity Common Stock, Shares Outstanding",
					Units: map[string][]ports.SECFact{
						"shares": {
							{End: "2024-12-31", Val: 25932733242, Fy: 2024, Fp: "FY", Form: "20-F", Filed: "2025-04-17"},
						},
					},
				},
			},
			"ifrs-full": {
				"Revenue": {
					Label: "Revenue",
					Units: map[string][]ports.SECFact{
						"TWD": {
							{End: "2024-12-31", Val: 2894308000000, Fy: 2024, Fp: "FY", Form: "20-F", Filed: "2025-04-17"},
						},
					},
				},
				"ProfitLossFromOperatingActivities": {
					Label: "Profit Loss From Operating Activities",
					Units: map[string][]ports.SECFact{
						"TWD": {
							{End: "2024-12-31", Val: 1321714000000, Fy: 2024, Fp: "FY", Form: "20-F", Filed: "2025-04-17"},
						},
					},
				},
				"Assets": {
					Label: "Assets",
					Units: map[string][]ports.SECFact{
						"TWD": {
							{End: "2024-12-31", Val: 6654855000000, Fy: 2024, Fp: "FY", Form: "20-F", Filed: "2025-04-17"},
						},
					},
				},
				"ProfitLoss": {
					Label: "Profit Loss",
					Units: map[string][]ports.SECFact{
						"TWD": {
							{End: "2024-12-31", Val: 1173267000000, Fy: 2024, Fp: "FY", Form: "20-F", Filed: "2025-04-17"},
						},
					},
				},
			},
		},
	}

	ctx := context.Background()
	historical, err := parser.ParseFinancialData(ctx, facts)

	require.NoError(t, err, "TSM-shape IFRS-full fixture must parse successfully after Phase B5+B6")
	require.NotNil(t, historical)
	require.GreaterOrEqual(t, len(historical.Data), 1, "expected at least one fiscal period")

	data, ok := historical.Data["2024FY"]
	require.True(t, ok, "expected 2024FY period in historical.Data")
	require.NotNil(t, data)

	// Income statement: IFRS Revenue / ProfitLossFromOperatingActivities / ProfitLoss
	// must resolve through the Phase B6 lookup tables.
	assert.Greater(t, data.Revenue, 0.0, "Revenue must be populated from ifrs-full:Revenue")
	assert.Greater(t, data.OperatingIncome, 0.0, "OperatingIncome must be populated from ifrs-full:ProfitLossFromOperatingActivities")
	assert.Greater(t, data.NetIncome, 0.0, "NetIncome must be populated from ifrs-full:ProfitLoss")

	// Shares: dei cover-page concept must populate SharesOutstanding (non-FX).
	assert.Greater(t, data.SharesOutstanding, 0.0, "SharesOutstanding must be populated from dei:EntityCommonStockSharesOutstanding")

	// Phase B5 currency stamp: TWD, NOT defaulted to USD.
	assert.Equal(t, "TWD", data.ReportingCurrency,
		"ReportingCurrency must be stamped from the ifrs-full fact units")

	// And critically: this case must NOT be classified as FPI anymore.
	assert.False(t, errors.Is(err, ports.ErrForeignPrivateIssuer),
		"TSM-shape fixture must no longer wrap ErrForeignPrivateIssuer (Phase B5+B6 lifted that limitation)")
}

func TestParser_NormalizeFinancialData_Success(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Create sample financial data
	data := &entities.FinancialData{
		Ticker:                   "AAPL",
		CIK:                      "0000320193",
		OperatingIncome:          100000000,
		Revenue:                  400000000,
		TotalAssets:              300000000,
		Goodwill:                 5000000,
		OtherIntangibles:         10000000,
		Inventory:                5000000,
		InterestExpense:          2000000,
		TotalDebt:                50000000,
		SharesOutstanding:        15000000,
		DilutedSharesOutstanding: 15500000,
		TaxRate:                  0.21,
		FilingPeriod:             "2023FY",
		FilingDate:               time.Now(),
		AsOf:                     time.Now(),
	}

	ctx := context.Background()
	normalized, err := parser.NormalizeFinancialData(ctx, data)

	require.NoError(t, err)
	assert.NotNil(t, normalized)
	assert.True(t, normalized.HasNormalizedData)
	assert.Equal(t, "AAPL", normalized.Ticker)

	// Check tangible assets calculation (total assets - goodwill - intangibles)
	expectedTangibleAssets := 300000000.0 - 5000000.0 - 10000000.0
	assert.Equal(t, expectedTangibleAssets, normalized.TangibleAssets)

	// Check normalized operating income
	assert.Equal(t, 100000000.0, normalized.NormalizedOperatingIncome)

	// Check that tax rate is preserved
	assert.Equal(t, 0.21, normalized.TaxRate)
}

func TestParser_NormalizeFinancialData_NilData(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	ctx := context.Background()
	normalized, err := parser.NormalizeFinancialData(ctx, nil)

	assert.Error(t, err)
	assert.Nil(t, normalized)
	assert.Contains(t, err.Error(), "data cannot be nil")
}

func TestParser_NormalizeFinancialData_InvalidTaxRate(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Create data with invalid tax rate
	data := &entities.FinancialData{
		Ticker:          "AAPL",
		CIK:             "0000320193",
		OperatingIncome: 100000000,
		TaxRate:         -0.1, // Invalid tax rate
		FilingPeriod:    "2023FY",
		FilingDate:      time.Now(),
		AsOf:            time.Now(),
	}

	ctx := context.Background()
	normalized, err := parser.NormalizeFinancialData(ctx, data)

	require.NoError(t, err)
	assert.NotNil(t, normalized)
	assert.Equal(t, 0.21, normalized.TaxRate) // Should use default tax rate
	assert.Contains(t, normalized.MissingFields, "tax_rate")
}

func TestParser_NormalizeFinancialData_NegativeTangibleAssets(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Create data where goodwill + intangibles > total assets
	data := &entities.FinancialData{
		Ticker:           "AAPL",
		CIK:              "0000320193",
		OperatingIncome:  100000000,
		TotalAssets:      100000000,
		Goodwill:         60000000,
		OtherIntangibles: 60000000, // Combined > total assets
		TaxRate:          0.21,
		FilingPeriod:     "2023FY",
		FilingDate:       time.Now(),
		AsOf:             time.Now(),
	}

	ctx := context.Background()
	normalized, err := parser.NormalizeFinancialData(ctx, data)

	require.NoError(t, err)
	assert.NotNil(t, normalized)
	assert.Equal(t, 0.0, normalized.TangibleAssets) // Should be clamped to 0
}

func TestParser_GetSupportedConcepts(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	concepts := parser.GetSupportedConcepts()

	assert.NotEmpty(t, concepts)
	// Check for some expected concepts
	conceptMap := make(map[string]bool)
	for _, concept := range concepts {
		conceptMap[concept] = true
	}

	assert.True(t, conceptMap["us-gaap:Revenues"])
	assert.True(t, conceptMap["us-gaap:OperatingIncomeLoss"])
	assert.True(t, conceptMap["us-gaap:Assets"])
}

func TestParser_ExtractFiscalPeriods(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Create facts with multiple periods using nested taxonomy -> concept structure
	facts := &ports.SECCompanyFacts{
		CIK:        "0000320193",
		EntityName: "Apple Inc.",
		Facts: map[string]map[string]ports.SECFactGroup{
			"us-gaap": {
				"Revenues": {
					Label:       "Revenues",
					Description: "Revenue from operations",
					Units: map[string][]ports.SECFact{
						"USD": {
							{
								End:   "2023-09-30",
								Val:   383285000000,
								Fy:    2023,
								Fp:    "FY",
								Filed: "2023-11-03",
							},
							{
								End:   "2023-06-30",
								Val:   81797000000,
								Fy:    2023,
								Fp:    "Q3",
								Filed: "2023-08-03",
							},
						},
					},
				},
			},
		},
	}

	periods, err := parser.extractFiscalPeriods(facts)

	require.NoError(t, err)
	assert.NotEmpty(t, periods)
	assert.Contains(t, periods, "2023FY")
	assert.Contains(t, periods, "2023Q3")

	// Phase B5: extractFiscalPeriods now returns *periodPayload per period
	// (not map[string]float64). Access values via the .values map and the
	// per-period currency stamp via .currency.
	assert.Equal(t, 383285000000.0, periods["2023FY"].values["Revenues"])
	assert.Equal(t, 81797000000.0, periods["2023Q3"].values["Revenues"])
	// Both periods carried USD facts, so the currency stamp must be USD
	// (preserves the pre-Phase-B5 default for domestic 10-K filers).
	assert.Equal(t, "USD", periods["2023FY"].currency)
	assert.Equal(t, "USD", periods["2023Q3"].currency)
}

func TestParser_FindValue(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	data := map[string]float64{
		"Revenues":                        400000000,
		"RevenueFromContractWithCustomer": 350000000,
		"SalesRevenueNet":                 300000000,
	}

	// Test finding the first available value
	val, found := parser.findValue(data, []string{
		"Revenues",
		"RevenueFromContractWithCustomer",
		"SalesRevenueNet",
	})

	assert.True(t, found)
	assert.Equal(t, 400000000.0, val)

	// Test finding fallback value
	val, found = parser.findValue(data, []string{
		"NonExistent",
		"RevenueFromContractWithCustomer",
		"SalesRevenueNet",
	})

	assert.True(t, found)
	assert.Equal(t, 350000000.0, val)

	// Test not finding any value
	val, found = parser.findValue(data, []string{
		"NonExistent1",
		"NonExistent2",
	})

	assert.False(t, found)
	assert.Equal(t, 0.0, val)
}

func TestParser_NormalizeOperatingIncome(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Test with positive income
	normalized := parser.normalizeOperatingIncome(100000000)
	assert.Equal(t, 100000000.0, normalized)

	// Test with negative income (should be preserved)
	normalized = parser.normalizeOperatingIncome(-50000000)
	assert.Equal(t, -50000000.0, normalized)

	// Test with zero
	normalized = parser.normalizeOperatingIncome(0)
	assert.Equal(t, 0.0, normalized)
}

func TestParser_CalculateDeadInventoryWritedown(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Test with normal inventory
	data := &entities.FinancialData{
		Inventory:         10000000,
		InventoryTurnover: 5.0, // Normal turnover
	}

	writedown := parser.calculateDeadInventoryWritedown(data)
	assert.Equal(t, 0.0, writedown) // No writedown for normal inventory

	// Test with zero inventory
	data = &entities.FinancialData{
		Inventory:         0,
		InventoryTurnover: 5.0,
	}

	writedown = parser.calculateDeadInventoryWritedown(data)
	assert.Equal(t, 0.0, writedown) // No writedown for zero inventory

	// Test with zero turnover
	data = &entities.FinancialData{
		Inventory:         10000000,
		InventoryTurnover: 0,
	}

	writedown = parser.calculateDeadInventoryWritedown(data)
	assert.Equal(t, 0.0, writedown) // No writedown calculation without turnover data
}

// TestParser_CalculateDeadInventoryWritedown_LowTurnover verifies the writedown
// calculation when inventory turnover drops below 1.0, which indicates dead inventory.
func TestParser_CalculateDeadInventoryWritedown_LowTurnover(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Turnover < 1.0 triggers dead inventory writedown: 50% excess * 40% writedown
	data := &entities.FinancialData{
		Inventory:         10000000,
		InventoryTurnover: 0.5, // Very low turnover signals dead inventory
	}

	writedown := parser.calculateDeadInventoryWritedown(data)
	// Expected: 10M * 0.5 (excess) * 0.4 (writedown rate) = 2M
	assert.Equal(t, 2000000.0, writedown)
}

// TestParser_CalculateDeadInventoryWritedown_NegativeInventory verifies no writedown
// for negative inventory values (defensive guard).
func TestParser_CalculateDeadInventoryWritedown_NegativeInventory(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	data := &entities.FinancialData{
		Inventory:         -100,
		InventoryTurnover: 0.5,
	}

	writedown := parser.calculateDeadInventoryWritedown(data)
	assert.Equal(t, 0.0, writedown)
}

// TestParser_ParsePeriodData_AllXBRLTags exercises extraction of every XBRL concept
// that parsePeriodData handles, including the recently-added cash flow and balance
// sheet tags (D&A, CapEx, operating cash flow, current assets/liabilities, cash).
func TestParser_ParsePeriodData_AllXBRLTags(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Build period data with every recognized XBRL tag
	data := map[string]float64{
		"_filing_date": float64(time.Date(2023, 11, 3, 0, 0, 0, 0, time.UTC).Unix()),
		"_end_date":    float64(time.Date(2023, 9, 30, 0, 0, 0, 0, time.UTC).Unix()),

		// Income statement
		"OperatingIncomeLoss": 114301000000,
		"Revenues":            383285000000,
		"InterestExpense":     3933000000,

		// Cash flow statement
		"DepreciationDepletionAndAmortization":       11519000000,
		"PaymentsToAcquirePropertyPlantAndEquipment": 10959000000,
		"NetCashProvidedByOperatingActivities":       110543000000,

		// Balance sheet
		"Assets":                                352755000000,
		"AssetsCurrent":                         143566000000,
		"LiabilitiesCurrent":                    145308000000,
		"CashAndCashEquivalentsAtCarryingValue": 29965000000,
		"Goodwill":                              0, // zero value - legitimate, found as 0
		"IntangibleAssetsNetExcludingGoodwill":  5000000000,
		"LongTermDebt":                          109280000000,
		"InventoryNet":                          6331000000,
		"DeferredTaxAssetsNet":                  17852000000,
		"OperatingLeaseLiability":               11087000000,
		// M-1d: equity bridge correction terms (primary XBRL tags).
		"MinorityInterest":    250000000,
		"PreferredStockValue": 100000000,

		// Pension
		"DefinedBenefitPlanPensionPlansProjectedBenefitObligationIncrease": 500000000,
		"DefinedBenefitPlanAssets": 400000000,

		// Shares (share unit data normally, but stored same way in period map)
		"CommonStockSharesOutstanding":                    15550061000,
		"WeightedAverageNumberOfDilutedSharesOutstanding": 15812547000,
	}

	// Phase B5: parsePeriodData now consumes a *periodPayload (struct
	// refactor that captures the period's reporting currency). Empty
	// currency defaults to USD — preserves backward compat for tests
	// that build period data literals without an explicit stamp.
	result, err := parser.parsePeriodData("0000320193", "2023FY", &periodPayload{values: data})
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify income statement
	assert.Equal(t, 114301000000.0, result.OperatingIncome)
	assert.Equal(t, 383285000000.0, result.Revenue)
	assert.Equal(t, 3933000000.0, result.InterestExpense)

	// Verify cash flow items
	assert.Equal(t, 11519000000.0, result.DepreciationAndAmortization)
	assert.Equal(t, 10959000000.0, result.CapitalExpenditures)
	assert.Equal(t, 110543000000.0, result.OperatingCashFlow)

	// Verify balance sheet
	assert.Equal(t, 352755000000.0, result.TotalAssets)
	assert.Equal(t, 143566000000.0, result.CurrentAssets)
	assert.Equal(t, 145308000000.0, result.CurrentLiabilities)
	assert.Equal(t, 29965000000.0, result.CashAndCashEquivalents)
	assert.Equal(t, 5000000000.0, result.OtherIntangibles)
	assert.Equal(t, 109280000000.0, result.TotalDebt)
	assert.Equal(t, 109280000000.0, result.InterestBearingDebt) // same as total debt
	assert.Equal(t, 6331000000.0, result.Inventory)
	assert.Equal(t, 17852000000.0, result.DeferredTaxAssets)
	assert.Equal(t, 11087000000.0, result.OperatingLeaseLiability)

	// Verify M-1d equity bridge correction terms (primary XBRL tags).
	assert.Equal(t, 250000000.0, result.MinorityInterest)
	assert.Equal(t, 100000000.0, result.PreferredEquity)

	// Verify pension fields
	assert.Equal(t, 500000000.0, result.ProjectedBenefitObligation)
	assert.Equal(t, 400000000.0, result.PensionPlanAssets)

	// Verify shares
	assert.Equal(t, 15550061000.0, result.SharesOutstanding)
	assert.Equal(t, 15812547000.0, result.DilutedSharesOutstanding)

	// Verify computed fields
	expectedTurnover := 383285000000.0 / 6331000000.0
	assert.InDelta(t, expectedTurnover, result.InventoryTurnover, 0.01)

	// Verify CIK and period
	assert.Equal(t, "0000320193", result.CIK)
	assert.Equal(t, "2023FY", result.FilingPeriod)

	// Phase B5 regression-pin: empty currency stamp defaults to USD so
	// pre-Phase-B5 callers (and this test) keep observing a USD ledger.
	assert.Equal(t, "USD", result.ReportingCurrency)
}

// TestParser_ParsePeriodData_FallbackTags verifies that when the primary XBRL tag
// is absent, the parser correctly falls back to alternate tag names.
func TestParser_ParsePeriodData_FallbackTags(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	data := map[string]float64{
		"_filing_date": float64(time.Date(2023, 11, 3, 0, 0, 0, 0, time.UTC).Unix()),
		"_end_date":    float64(time.Date(2023, 9, 30, 0, 0, 0, 0, time.UTC).Unix()),

		// Use fallback tag names instead of primary ones
		"IncomeLossFromContinuingOperationsBeforeIncomeTaxesExtraordinaryItemsNoncontrollingInterest": 50000000,
		"RevenueFromContractWithCustomerExcludingAssessedTax":                                         200000000,
		"InterestExpenseDebt":                                     1000000,
		"DepreciationAndAmortization":                             5000000,
		"PaymentsToAcquireProductiveAssets":                       3000000,
		"CashProvidedByOperatingActivities":                       40000000,
		"CashCashEquivalentsAndShortTermInvestments":              15000000,
		"IntangibleAssetsNet":                                     2000000,
		"LongTermDebtNoncurrent":                                  30000000,
		"Inventory":                                               1000000,
		"DeferredIncomeTaxAssetsNet":                              500000,
		"CommonStockSharesIssued":                                 10000000,
		"WeightedAverageNumberOfSharesOutstandingBasicAndDiluted": 10500000,
		"ProjectedBenefitObligation":                              200000,
		"PensionPlanAssets":                                       150000,
		// M-1d: equity bridge correction terms (fallback XBRL tags).
		"MinorityInterestInLimitedPartnerships": 75000,
		"PreferredStockValueOutstanding":        25000,
	}

	// Phase B5: parsePeriodData now consumes a *periodPayload struct.
	result, err := parser.parsePeriodData("0000789019", "2023FY", &periodPayload{values: data})
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify fallback resolution
	assert.Equal(t, 50000000.0, result.OperatingIncome)
	assert.Equal(t, 200000000.0, result.Revenue)
	assert.Equal(t, 1000000.0, result.InterestExpense)
	assert.Equal(t, 5000000.0, result.DepreciationAndAmortization)
	assert.Equal(t, 3000000.0, result.CapitalExpenditures)
	assert.Equal(t, 40000000.0, result.OperatingCashFlow)
	assert.Equal(t, 15000000.0, result.CashAndCashEquivalents)
	assert.Equal(t, 2000000.0, result.OtherIntangibles)
	assert.Equal(t, 30000000.0, result.TotalDebt)
	assert.Equal(t, 1000000.0, result.Inventory)
	assert.Equal(t, 500000.0, result.DeferredTaxAssets)
	assert.Equal(t, 10000000.0, result.SharesOutstanding)
	assert.Equal(t, 10500000.0, result.DilutedSharesOutstanding)
	assert.Equal(t, 200000.0, result.ProjectedBenefitObligation)
	assert.Equal(t, 150000.0, result.PensionPlanAssets)
	// M-1d: fallback tag resolution.
	assert.Equal(t, 75000.0, result.MinorityInterest)
	assert.Equal(t, 25000.0, result.PreferredEquity)
}

// TestParser_ParsePeriodData_InsufficientData verifies parsePeriodData returns an
// error when the period has neither revenue nor operating income.
func TestParser_ParsePeriodData_InsufficientData(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Only has total assets -- no revenue or operating income
	data := map[string]float64{
		"_filing_date": float64(time.Date(2023, 11, 3, 0, 0, 0, 0, time.UTC).Unix()),
		"_end_date":    float64(time.Date(2023, 9, 30, 0, 0, 0, 0, time.UTC).Unix()),
		"Assets":       100000000,
	}

	// Phase B5: parsePeriodData now consumes a *periodPayload struct.
	result, err := parser.parsePeriodData("0000320193", "2023FY", &periodPayload{values: data})
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "insufficient data")
}

// TestParser_ParsePeriodData_DilutedSharesFallback verifies that when diluted
// shares data is absent, the parser falls back to regular shares outstanding.
func TestParser_ParsePeriodData_DilutedSharesFallback(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	data := map[string]float64{
		"_filing_date":                 float64(time.Date(2023, 11, 3, 0, 0, 0, 0, time.UTC).Unix()),
		"_end_date":                    float64(time.Date(2023, 9, 30, 0, 0, 0, 0, time.UTC).Unix()),
		"Revenues":                     200000000,
		"OperatingIncomeLoss":          50000000,
		"CommonStockSharesOutstanding": 10000000,
		// No diluted shares -- should fall back to CommonStockSharesOutstanding
	}

	// Phase B5: parsePeriodData now consumes a *periodPayload struct.
	result, err := parser.parsePeriodData("0000320193", "2023FY", &periodPayload{values: data})
	require.NoError(t, err)
	assert.Equal(t, 10000000.0, result.DilutedSharesOutstanding)
}

// TestParser_ParsePeriodData_MissingFields verifies that the MissingFields slice
// is correctly populated when key fields (revenue, total_assets, shares) are absent.
func TestParser_ParsePeriodData_MissingFields(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Only operating income -- revenue, total_assets, shares_outstanding are missing
	data := map[string]float64{
		"_filing_date":        float64(time.Date(2023, 11, 3, 0, 0, 0, 0, time.UTC).Unix()),
		"_end_date":           float64(time.Date(2023, 9, 30, 0, 0, 0, 0, time.UTC).Unix()),
		"OperatingIncomeLoss": 50000000,
	}

	// Phase B5: parsePeriodData now consumes a *periodPayload struct.
	result, err := parser.parsePeriodData("0000320193", "2023FY", &periodPayload{values: data})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Contains(t, result.MissingFields, "revenue")
	assert.Contains(t, result.MissingFields, "total_assets")
	assert.Contains(t, result.MissingFields, "shares_outstanding")
}

// TestParser_ExtractFiscalPeriods_WithSharesUnits verifies that the parser also
// processes facts from the "shares" unit type (e.g., CommonStockSharesOutstanding).
func TestParser_ExtractFiscalPeriods_WithSharesUnits(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	facts := &ports.SECCompanyFacts{
		CIK:        "0000320193",
		EntityName: "Apple Inc.",
		Facts: map[string]map[string]ports.SECFactGroup{
			"us-gaap": {
				"CommonStockSharesOutstanding": {
					Label:       "Common Stock Shares Outstanding",
					Description: "Shares outstanding",
					Units: map[string][]ports.SECFact{
						"shares": {
							{
								End:   "2023-09-30",
								Val:   15550061000,
								Fy:    2023,
								Fp:    "FY",
								Filed: "2023-11-03",
							},
						},
					},
				},
				// Also include revenue in USD so parsePeriodData can succeed
				"Revenues": {
					Label:       "Revenues",
					Description: "Revenue",
					Units: map[string][]ports.SECFact{
						"USD": {
							{
								End:   "2023-09-30",
								Val:   383285000000,
								Fy:    2023,
								Fp:    "FY",
								Filed: "2023-11-03",
							},
						},
					},
				},
			},
		},
	}

	periods, err := parser.extractFiscalPeriods(facts)
	require.NoError(t, err)
	require.Contains(t, periods, "2023FY")

	// Verify shares data was extracted alongside USD data
	// (Phase B5: access via .values map on the new *periodPayload struct).
	assert.Equal(t, 15550061000.0, periods["2023FY"].values["CommonStockSharesOutstanding"])
	assert.Equal(t, 383285000000.0, periods["2023FY"].values["Revenues"])
	// USD currency stamped from the Revenues fact; shares are dimensionless
	// and MUST NOT influence the stamp (calculation-safety contract).
	assert.Equal(t, "USD", periods["2023FY"].currency)
}

// TestParser_ExtractFiscalPeriods_MultiCurrency_PicksDominant pins the
// Phase B5 multi-currency edge case: when a single period carries facts in
// MORE than one currency (rare — typically a corporate-action artifact such
// as a mid-year reporting-currency change), the parser must pick the
// currency with the most fact entries and continue without failing.
func TestParser_ExtractFiscalPeriods_MultiCurrency_PicksDominant(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Two TWD-denominated facts and one USD-denominated fact in the same
	// period; TWD must win because it has more entries (2 > 1).
	facts := &ports.SECCompanyFacts{
		CIK:        "1046179",
		EntityName: "Edge Case Filer",
		Facts: map[string]map[string]ports.SECFactGroup{
			"ifrs-full": {
				"Revenue": {
					Label: "Revenue",
					Units: map[string][]ports.SECFact{
						"TWD": {
							{End: "2024-12-31", Val: 2894308000000, Fy: 2024, Fp: "FY", Form: "20-F", Filed: "2025-04-17"},
						},
					},
				},
				"Assets": {
					Label: "Assets",
					Units: map[string][]ports.SECFact{
						"TWD": {
							{End: "2024-12-31", Val: 6654855000000, Fy: 2024, Fp: "FY", Form: "20-F", Filed: "2025-04-17"},
						},
					},
				},
				// Single USD-denominated fact (e.g., legacy corporate-action
				// disclosure) — minority count, must NOT win.
				"FinanceCosts": {
					Label: "Finance Costs",
					Units: map[string][]ports.SECFact{
						"USD": {
							{End: "2024-12-31", Val: 100000000, Fy: 2024, Fp: "FY", Form: "20-F", Filed: "2025-04-17"},
						},
					},
				},
			},
		},
	}

	periods, err := parser.extractFiscalPeriods(facts)
	require.NoError(t, err)
	require.Contains(t, periods, "2024FY")

	// TWD is dominant (2 facts vs 1 USD fact).
	assert.Equal(t, "TWD", periods["2024FY"].currency,
		"dominant currency must be TWD (2 facts) not USD (1 fact)")
}

// TestParser_ExtractFiscalPeriods_EmptyFacts verifies that empty fact maps
// return an appropriate error.
func TestParser_ExtractFiscalPeriods_EmptyFacts(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	facts := &ports.SECCompanyFacts{
		CIK:        "0000320193",
		EntityName: "Apple Inc.",
		Facts:      map[string]map[string]ports.SECFactGroup{},
	}

	periods, err := parser.extractFiscalPeriods(facts)
	assert.Error(t, err)
	assert.Nil(t, periods)
	assert.Contains(t, err.Error(), "no financial periods extracted")
}

// TestParser_NormalizeFinancialData_DeadInventoryWritedown verifies that normalization
// correctly adjusts tangible assets when dead inventory is detected.
func TestParser_NormalizeFinancialData_DeadInventoryWritedown(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	data := &entities.FinancialData{
		Ticker:            "TEST",
		OperatingIncome:   50000000,
		TotalAssets:       100000000,
		Goodwill:          0,
		OtherIntangibles:  0,
		Inventory:         20000000,
		InventoryTurnover: 0.5, // Low turnover triggers dead inventory writedown
		Revenue:           10000000,
		TaxRate:           0.21,
		FilingPeriod:      "2023FY",
		FilingDate:        time.Now(),
		AsOf:              time.Now(),
	}

	ctx := context.Background()
	normalized, err := parser.NormalizeFinancialData(ctx, data)

	require.NoError(t, err)
	// Dead inventory writedown: 20M * 0.5 * 0.4 = 4M
	assert.Equal(t, 4000000.0, normalized.DeadInventoryWritedown)
	// Tangible assets: 100M - 0 - 0 = 100M, then - 4M writedown = 96M
	assert.Equal(t, 96000000.0, normalized.TangibleAssets)
}

// TestParser_NormalizeFinancialData_TaxRateAboveOne verifies normalization with
// a tax rate > 1.0 (invalid, should default to 21%).
func TestParser_NormalizeFinancialData_TaxRateAboveOne(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	data := &entities.FinancialData{
		Ticker:          "TEST",
		OperatingIncome: 50000000,
		TaxRate:         1.5, // Invalid: > 1.0
		FilingPeriod:    "2023FY",
		FilingDate:      time.Now(),
		AsOf:            time.Now(),
	}

	ctx := context.Background()
	normalized, err := parser.NormalizeFinancialData(ctx, data)

	require.NoError(t, err)
	assert.Equal(t, 0.21, normalized.TaxRate) // Should use default
	assert.Contains(t, normalized.MissingFields, "tax_rate")
}

// TestParser_NormalizeFinancialData_NegativeOperatingIncome verifies normalization
// preserves negative operating income.
func TestParser_NormalizeFinancialData_NegativeOperatingIncome(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	data := &entities.FinancialData{
		Ticker:          "LOSS",
		OperatingIncome: -30000000,
		TaxRate:         0.21,
		FilingPeriod:    "2023FY",
		FilingDate:      time.Now(),
		AsOf:            time.Now(),
	}

	ctx := context.Background()
	normalized, err := parser.NormalizeFinancialData(ctx, data)

	require.NoError(t, err)
	assert.Equal(t, -30000000.0, normalized.NormalizedOperatingIncome)
}

// TestParser_NormalizeFinancialData_DeadInventoryExceedsTangible verifies that
// tangible assets are clamped to 0 when dead inventory writedown exceeds them.
func TestParser_NormalizeFinancialData_DeadInventoryExceedsTangible(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	data := &entities.FinancialData{
		Ticker:            "TEST",
		OperatingIncome:   10000000,
		TotalAssets:       5000000, // Low total assets
		Goodwill:          1000000,
		OtherIntangibles:  1000000,
		Inventory:         50000000, // Very high inventory
		InventoryTurnover: 0.3,      // Very low turnover
		Revenue:           15000000,
		TaxRate:           0.21,
		FilingPeriod:      "2023FY",
		FilingDate:        time.Now(),
		AsOf:              time.Now(),
	}

	ctx := context.Background()
	normalized, err := parser.NormalizeFinancialData(ctx, data)

	require.NoError(t, err)
	// Tangible assets: 5M - 1M - 1M = 3M
	// Dead inventory: 50M * 0.5 * 0.4 = 10M
	// 3M - 10M = -7M => clamped to 0
	assert.Equal(t, 0.0, normalized.TangibleAssets)
	assert.Equal(t, 10000000.0, normalized.DeadInventoryWritedown)
}
