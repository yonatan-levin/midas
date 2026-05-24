package cleaneddata

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestAsReported_IdentityCopy pins that AsReported() projects every field
// in the consumed subset verbatim from the underlying *entities.FinancialData.
// T2-BS-3 carve-out implication: the parser-stamped TotalLiabilities=0 case
// stays 0 here even when components would reconstruct to a positive value;
// the AMD/KO truthful reconstruction lives in Restated(), not AsReported().
func TestAsReported_IdentityCopy(t *testing.T) {
	asOf := time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC)
	raw := &entities.FinancialData{
		Ticker:                      "ACME",
		CIK:                         "0001234567",
		AsOf:                        asOf,
		ReportingCurrency:           "USD",
		TotalAssets:                 1_000_000,
		CurrentAssets:               400_000,
		TangibleAssets:              700_000,
		Goodwill:                    200_000,
		OtherIntangibles:            100_000,
		Inventory:                   150_000,
		DeferredTaxAssets:           50_000,
		TotalLiabilities:            0, // T2-BS-3 parser dropout simulation
		CurrentLiabilities:          250_000,
		TotalDebt:                   300_000,
		InterestBearingDebt:         280_000,
		StockholdersEquity:          400_000,
		OperatingIncome:             120_000,
		NormalizedOperatingIncome:   125_000,
		Revenue:                     800_000,
		NetIncome:                   90_000,
		InterestExpense:             15_000,
		OperatingCashFlow:           110_000,
		CapitalExpenditures:         40_000,
		DepreciationAndAmortization: 35_000,
		SharesOutstanding:           1_000,
		DilutedSharesOutstanding:    1_050,
		DividendsPerShare:           1.25,
	}

	view := New(raw).AsReported()
	require.NotNil(t, view)

	assert.Equal(t, AsReportedView, view.ViewKind)
	assert.Equal(t, "ACME", view.Ticker)
	assert.Equal(t, "0001234567", view.CIK)
	assert.Equal(t, asOf, view.AsOf)
	assert.Equal(t, "USD", view.ReportingCurrency)
	assert.Equal(t, 1_000_000.0, view.TotalAssets)
	assert.Equal(t, 400_000.0, view.CurrentAssets)
	assert.Equal(t, 700_000.0, view.TangibleAssets)
	assert.Equal(t, 200_000.0, view.Goodwill)
	assert.Equal(t, 100_000.0, view.OtherIntangibles)
	assert.Equal(t, 150_000.0, view.Inventory)
	assert.Equal(t, 50_000.0, view.DeferredTaxAssets)
	assert.Equal(t, 0.0, view.TotalLiabilities, "AsReported must preserve parser-stamped zero (T2-BS-3)")
	assert.Equal(t, 250_000.0, view.CurrentLiabilities)
	assert.Equal(t, 300_000.0, view.TotalDebt)
	assert.Equal(t, 280_000.0, view.InterestBearingDebt)
	assert.Equal(t, 400_000.0, view.StockholdersEquity)
	assert.Equal(t, 120_000.0, view.OperatingIncome)
	assert.Equal(t, 125_000.0, view.NormalizedOperatingIncome)
	assert.Equal(t, 800_000.0, view.Revenue)
	assert.Equal(t, 90_000.0, view.NetIncome)
	assert.Equal(t, 15_000.0, view.InterestExpense)
	assert.Equal(t, 110_000.0, view.OperatingCashFlow)
	assert.Equal(t, 40_000.0, view.CapitalExpenditures)
	assert.Equal(t, 35_000.0, view.DepreciationAndAmortization)
	assert.Equal(t, 1_000.0, view.SharesOutstanding)
	assert.Equal(t, 1_050.0, view.DilutedSharesOutstanding)
	assert.Equal(t, 1.25, view.DividendsPerShare)
	assert.Equal(t, 0.0, view.DebtLikeClaims, "AsReported never populates DebtLikeClaims")
}

// TestAsReported_NilRaw exercises the nil-safe path. New(nil).AsReported()
// returns a zero view with the correct ViewKind tag rather than crashing,
// so consumer code can safely call accessors before checking for data
// availability.
func TestAsReported_NilRaw(t *testing.T) {
	view := New(nil).AsReported()
	require.NotNil(t, view)
	assert.Equal(t, AsReportedView, view.ViewKind)
	assert.Equal(t, "", view.Ticker)
	assert.Equal(t, 0.0, view.TotalAssets)
}

// TestAsReported_MemoizedPointer pins that repeated AsReported() calls
// return the same *FinancialDataView pointer, so consumers can rely on
// pointer identity for change detection / equality checks.
func TestAsReported_MemoizedPointer(t *testing.T) {
	c := New(&entities.FinancialData{Ticker: "MXL"})
	v1 := c.AsReported()
	v2 := c.AsReported()
	assert.Same(t, v1, v2, "AsReported must memoize its returned pointer")
}

// TestAsReported_NilReceiverReturnsZeroView pins defensive behavior on a
// nil *CleanedFinancialData receiver — accessors must not panic.
func TestAsReported_NilReceiverReturnsZeroView(t *testing.T) {
	var c *CleanedFinancialData
	view := c.AsReported()
	require.NotNil(t, view)
	assert.Equal(t, AsReportedView, view.ViewKind)
}
