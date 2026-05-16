package sec

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestComputePlugs_TypicalFiler pins the happy-path: a US-GAAP filer with
// all components populated yields strictly-non-negative plug residuals that
// satisfy the components-sum-to-umbrellas invariant.
func TestComputePlugs_TypicalFiler(t *testing.T) {
	fd := &entities.FinancialData{
		CIK:                               "0000320193",
		FilingPeriod:                      "2023FY",
		TotalAssets:                       352_755.0,
		CurrentAssets:                     143_566.0,
		CurrentLiabilities:                145_308.0,
		TotalLiabilities:                  290_437.0,
		CashAndCashEquivalents:            29_965.0,
		Inventory:                         6_331.0,
		Goodwill:                          0.0,
		OtherIntangibles:                  0.0,
		DeferredTaxAssets:                 0.0,
		TotalDebt:                         111_088.0,
		OperatingLeaseLiabilityCurrent:    1_410.0,
		OperatingLeaseLiabilityNoncurrent: 10_550.0,
	}

	computePlugs(fd, zap.NewNop())

	// OtherCurrentAssets = 143566 - (29965 + 6331) = 107270
	assert.InDelta(t, 107_270.0, fd.OtherCurrentAssets, 0.01)
	// non_current_assets = 352755 - 143566 = 209189; minus (0+0+0) = 209189
	assert.InDelta(t, 209_189.0, fd.OtherNonCurrentAssets, 0.01)
	// OtherCurrentLiabilities = 145308 - 1410 = 143898
	assert.InDelta(t, 143_898.0, fd.OtherCurrentLiabilities, 0.01)
	// OtherNonCurrentLiabilities = 290437 - 145308 - 111088 - 10550 = 23491
	assert.InDelta(t, 23_491.0, fd.OtherNonCurrentLiabilities, 0.01)
}

// TestComputePlugs_ZeroUmbrellas verifies that missing umbrellas leave all
// plugs at zero (no negative residuals leaking from arithmetic on zero).
func TestComputePlugs_ZeroUmbrellas(t *testing.T) {
	fd := &entities.FinancialData{
		CIK:          "0000000000",
		FilingPeriod: "2024Q1",
		// All fields default to zero.
	}

	computePlugs(fd, zap.NewNop())

	assert.Equal(t, 0.0, fd.OtherCurrentAssets)
	assert.Equal(t, 0.0, fd.OtherNonCurrentAssets)
	assert.Equal(t, 0.0, fd.OtherCurrentLiabilities)
	assert.Equal(t, 0.0, fd.OtherNonCurrentLiabilities)
}

// TestComputePlugs_NegativeResidualClampedAndLogged verifies the safety net:
// when sum(components) > umbrella (data-quality edge case), the plug clamps
// to zero and a Debug log line is emitted.
func TestComputePlugs_NegativeResidualClampedAndLogged(t *testing.T) {
	core, recorded := observer.New(zap.DebugLevel)
	logger := zap.New(core)

	fd := &entities.FinancialData{
		CIK:                    "0001234567",
		FilingPeriod:           "2025Q2",
		CurrentAssets:          100.0,
		CashAndCashEquivalents: 80.0,
		Inventory:              50.0, // 80 + 50 = 130 > 100 → negative residual
	}

	computePlugs(fd, logger)

	assert.Equal(t, 0.0, fd.OtherCurrentAssets, "negative residual must clamp to zero")

	// Exactly one debug log line for the clamped field.
	entries := recorded.FilterMessage("plug residual clamped to zero").All()
	assert.Len(t, entries, 1)
	ctxMap := entries[0].ContextMap()
	assert.Equal(t, "0001234567", ctxMap["cik"])
	assert.Equal(t, "2025Q2", ctxMap["period"])
	assert.Equal(t, "OtherCurrentAssets", ctxMap["plug_field"])
	// raw residual = 100 - 130 = -30
	assert.InDelta(t, -30.0, ctxMap["raw_residual"].(float64), 0.01)
}

// TestComputePlugs_IFRSFullFiler_TSM mimics the TSM-style decomposition
// (large goodwill, intangibles, multi-currency had already collapsed before
// the call). Just confirms IFRS-shaped data flows through identically.
func TestComputePlugs_IFRSFullFiler_TSM(t *testing.T) {
	fd := &entities.FinancialData{
		CIK:                    "0001046179",
		FilingPeriod:           "2024FY",
		ReportingCurrency:      "TWD",
		TotalAssets:            6_000_000.0,
		CurrentAssets:          2_000_000.0,
		CurrentLiabilities:     1_000_000.0,
		TotalLiabilities:       3_000_000.0,
		CashAndCashEquivalents: 1_500_000.0,
		Inventory:              250_000.0,
		Goodwill:               50_000.0,
		OtherIntangibles:       30_000.0,
		DeferredTaxAssets:      20_000.0,
		TotalDebt:              900_000.0,
	}

	computePlugs(fd, zap.NewNop())

	// Plug invariant: umbrella == sum(known components) + plug.
	got := fd.CashAndCashEquivalents + fd.Inventory + fd.OtherCurrentAssets
	assert.InDelta(t, fd.CurrentAssets, got, 0.01)

	gotNCA := fd.Goodwill + fd.OtherIntangibles + fd.DeferredTaxAssets + fd.OtherNonCurrentAssets
	assert.InDelta(t, fd.TotalAssets-fd.CurrentAssets, gotNCA, 0.01)

	assert.GreaterOrEqual(t, fd.OtherCurrentAssets, 0.0)
	assert.GreaterOrEqual(t, fd.OtherNonCurrentAssets, 0.0)
	assert.GreaterOrEqual(t, fd.OtherCurrentLiabilities, 0.0)
	assert.GreaterOrEqual(t, fd.OtherNonCurrentLiabilities, 0.0)
}
