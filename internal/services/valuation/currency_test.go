package valuation

// Tests for the FX-conversion service-layer step (Phase B9 of the IFRS-FPI
// plan, docs/refactoring/ifrs-foreign-private-issuer-support-spec.md).
//
// The contract under test:
//   - convertFinancialsToUSD multiplies every monetary field on every period
//     by the FX rate looked up from MacroDataGateway.GetFXRate(ccy, "USD").
//   - SharesOutstanding / DilutedSharesOutstanding / TaxRate /
//     InventoryTurnover / IncrementalBorrowingRate / RiskFreeRate are
//     dimensionless and MUST NOT be touched.
//   - OperatingLeaseCommitments map values ARE monetary (year keys are
//     metadata, only the float values get multiplied).
//   - USD-denominated and empty-currency periods are no-ops; the gateway is
//     not called for them.
//   - Exactly one GetFXRate call per distinct currency, even across many
//     periods of the same currency.
//   - On FX failure, the period is left unchanged (NOT zeroed) and the
//     function returns an error wrapping ports.ErrFXRateUnavailable.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// fxMockMacroGateway is a testify-mock implementation of MacroDataGateway
// whose GetFXRate is wired through m.Called so tests can program per-currency
// rates and assert call counts. The other interface methods are no-op stubs
// because Phase B9 only exercises GetFXRate.
//
// Distinct from MockMacroDataGateway in service_test.go which deliberately
// short-circuits GetFXRate to 1.0 to keep dozens of pre-existing tests
// compiling without per-test FX expectations.
type fxMockMacroGateway struct {
	mock.Mock
}

func (m *fxMockMacroGateway) GetFXRate(ctx context.Context, fromCcy, toCcy string) (float64, error) {
	args := m.Called(ctx, fromCcy, toCcy)
	return args.Get(0).(float64), args.Error(1)
}

func (m *fxMockMacroGateway) GetTreasuryRates(_ context.Context) (*entities.TreasuryRates, error) {
	return nil, nil
}

func (m *fxMockMacroGateway) GetMarketRiskPremium(_ context.Context) (float64, error) {
	return 0, nil
}

func (m *fxMockMacroGateway) HealthCheck(_ context.Context) error {
	return nil
}

// newFXTestService builds a minimal Service wired only with the bits the FX
// conversion path actually touches (logger, metrics stub, macro gateway,
// calcEmitter). Everything else is nil; convertFinancialsToUSD must not
// reach into them.
func newFXTestService(t *testing.T, gw ports.MacroDataGateway) *Service {
	t.Helper()
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:                 1 * time.Hour,
			DefaultTerminalGrowthCap: 0.025,
			DCFMaxGrowthRate:         0.5,
			DCFMinGrowthRate:         -0.3,
		},
	}
	svc := NewService(nil, nil, nil, nil, nil, nil, &MockMetricsService{}, cfg, zap.NewNop(), newTestCalcEmitter())
	svc.SetMacroGateway(gw)
	return svc
}

// TestService_ConvertFinancialsToUSD_TWD pins the calculation-safety contract.
// TSM-shaped data in TWD must come out in USD with monetary fields multiplied
// and dimensionless fields untouched.
func TestService_ConvertFinancialsToUSD_TWD(t *testing.T) {
	ctx := context.Background()

	gw := &fxMockMacroGateway{}
	gw.On("GetFXRate", mock.Anything, "TWD", "USD").Return(0.0312, nil).Once()

	svc := newFXTestService(t, gw)

	hist := &entities.HistoricalFinancialData{
		Ticker: "TSM",
		Data: map[string]*entities.FinancialData{
			"2023FY": {
				Ticker:                   "TSM",
				FilingPeriod:             "2023FY",
				ReportingCurrency:        "TWD",
				Revenue:                  2_894_308_000_000,
				OperatingIncome:          1_321_714_000_000,
				TotalAssets:              6_654_855_000_000,
				SharesOutstanding:        25_932_733_242,
				DilutedSharesOutstanding: 25_932_733_242,
				TaxRate:                  0.13,
			},
		},
	}

	err := svc.convertFinancialsToUSD(ctx, hist)
	require.NoError(t, err)

	got := hist.Data["2023FY"]
	// Currency stamped to USD so a second pass is a no-op (idempotency).
	assert.Equal(t, "USD", got.ReportingCurrency)

	// Monetary fields multiplied by 0.0312.
	assert.InDelta(t, 90_302_409_600.0, got.Revenue, 1.0, "Revenue must be FX-converted")
	assert.InDelta(t, 41_237_476_800.0, got.OperatingIncome, 1.0, "OperatingIncome must be FX-converted")
	assert.InDelta(t, 207_631_476_000.0, got.TotalAssets, 1.0, "TotalAssets must be FX-converted")

	// Dimensionless fields MUST NOT be touched. This is the calculation-safety
	// invariant — multiplying shares by 0.0312 silently corrupts every
	// downstream per-share number.
	assert.Equal(t, 25_932_733_242.0, got.SharesOutstanding, "shares are dimensionless; must not be FX-converted")
	assert.Equal(t, 25_932_733_242.0, got.DilutedSharesOutstanding, "diluted shares are dimensionless; must not be FX-converted")
	assert.Equal(t, 0.13, got.TaxRate, "tax rate is a ratio; must not be FX-converted")

	gw.AssertExpectations(t)
}

// TestService_ConvertFinancialsToUSD_USD_NoOp asserts USD-denominated data is
// untouched and the gateway is not consulted (cheap path).
func TestService_ConvertFinancialsToUSD_USD_NoOp(t *testing.T) {
	ctx := context.Background()

	gw := &fxMockMacroGateway{}
	// No expectations set: gw.AssertExpectations(t) requires zero calls.

	svc := newFXTestService(t, gw)

	hist := &entities.HistoricalFinancialData{
		Ticker: "AAPL",
		Data: map[string]*entities.FinancialData{
			"2023FY": {
				Ticker:            "AAPL",
				ReportingCurrency: "USD",
				Revenue:           383_285_000_000,
				OperatingIncome:   114_301_000_000,
				SharesOutstanding: 15_550_061_000,
			},
		},
	}

	err := svc.convertFinancialsToUSD(ctx, hist)
	require.NoError(t, err)

	got := hist.Data["2023FY"]
	assert.Equal(t, "USD", got.ReportingCurrency)
	assert.Equal(t, 383_285_000_000.0, got.Revenue)
	assert.Equal(t, 114_301_000_000.0, got.OperatingIncome)
	assert.Equal(t, 15_550_061_000.0, got.SharesOutstanding)

	// No FX call expected.
	gw.AssertNotCalled(t, "GetFXRate", mock.Anything, mock.Anything, mock.Anything)
}

// TestService_ConvertFinancialsToUSD_EmptyCurrency_TreatedAsUSD verifies legacy
// rows persisted before Phase B5 (which lack the reporting_currency column)
// are treated as USD: no FX call, no mutation, no error.
func TestService_ConvertFinancialsToUSD_EmptyCurrency_TreatedAsUSD(t *testing.T) {
	ctx := context.Background()

	gw := &fxMockMacroGateway{}
	svc := newFXTestService(t, gw)

	hist := &entities.HistoricalFinancialData{
		Ticker: "MSFT",
		Data: map[string]*entities.FinancialData{
			"2023FY": {
				Ticker:            "MSFT",
				ReportingCurrency: "", // legacy / pre-B5
				Revenue:           211_915_000_000,
			},
		},
	}

	err := svc.convertFinancialsToUSD(ctx, hist)
	require.NoError(t, err)

	got := hist.Data["2023FY"]
	assert.Equal(t, "", got.ReportingCurrency, "legacy empty currency must remain empty (do not silently relabel)")
	assert.Equal(t, 211_915_000_000.0, got.Revenue)

	gw.AssertNotCalled(t, "GetFXRate", mock.Anything, mock.Anything, mock.Anything)
}

// TestService_ConvertFinancialsToUSD_FXFailure_PreservesData asserts the
// failure-mode contract: when the gateway can't resolve the rate we leave the
// period untouched (NOT zeroed) and return an error wrapping
// ports.ErrFXRateUnavailable so the caller can classify the failure.
func TestService_ConvertFinancialsToUSD_FXFailure_PreservesData(t *testing.T) {
	ctx := context.Background()

	gw := &fxMockMacroGateway{}
	gw.On("GetFXRate", mock.Anything, "TWD", "USD").
		Return(0.0, ports.ErrFXRateUnavailable).Once()

	svc := newFXTestService(t, gw)

	hist := &entities.HistoricalFinancialData{
		Ticker: "TSM",
		Data: map[string]*entities.FinancialData{
			"2023FY": {
				Ticker:            "TSM",
				ReportingCurrency: "TWD",
				Revenue:           2_894_308_000_000,
				OperatingIncome:   1_321_714_000_000,
				SharesOutstanding: 25_932_733_242,
			},
		},
	}

	err := svc.convertFinancialsToUSD(ctx, hist)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrFXRateUnavailable),
		"error must wrap ports.ErrFXRateUnavailable so callers can classify it")

	got := hist.Data["2023FY"]
	// Period must be left UNCHANGED — not zeroed, not relabeled. Preserving
	// the original values lets the caller decide whether to abort or proceed
	// with stale data.
	assert.Equal(t, "TWD", got.ReportingCurrency, "currency must remain TWD on FX failure (not relabeled USD)")
	assert.Equal(t, 2_894_308_000_000.0, got.Revenue, "Revenue must be preserved on FX failure")
	assert.Equal(t, 1_321_714_000_000.0, got.OperatingIncome, "OperatingIncome must be preserved on FX failure")
	assert.Equal(t, 25_932_733_242.0, got.SharesOutstanding)

	gw.AssertExpectations(t)
}

// TestService_ConvertFinancialsToUSD_MultiPeriod_OneFXLookupPerCurrency pins
// the optimization that we cache rates locally — N periods of the same
// currency must produce a single gateway call, not N.
func TestService_ConvertFinancialsToUSD_MultiPeriod_OneFXLookupPerCurrency(t *testing.T) {
	ctx := context.Background()

	gw := &fxMockMacroGateway{}
	// .Once() asserts the single-call invariant.
	gw.On("GetFXRate", mock.Anything, "TWD", "USD").Return(0.0312, nil).Once()

	svc := newFXTestService(t, gw)

	hist := &entities.HistoricalFinancialData{
		Ticker: "TSM",
		Data:   make(map[string]*entities.FinancialData, 5),
	}
	for i, period := range []string{"2019FY", "2020FY", "2021FY", "2022FY", "2023FY"} {
		hist.Data[period] = &entities.FinancialData{
			Ticker:            "TSM",
			FilingPeriod:      period,
			ReportingCurrency: "TWD",
			Revenue:           1_000_000_000_000 + float64(i)*100_000_000_000,
			SharesOutstanding: 25_932_733_242,
		}
	}

	err := svc.convertFinancialsToUSD(ctx, hist)
	require.NoError(t, err)

	for _, period := range []string{"2019FY", "2020FY", "2021FY", "2022FY", "2023FY"} {
		got := hist.Data[period]
		assert.Equal(t, "USD", got.ReportingCurrency, "period %s must be relabeled USD", period)
		assert.Greater(t, got.Revenue, 0.0, "period %s revenue must be set", period)
		assert.Less(t, got.Revenue, 1e13, "period %s revenue must be FX-converted (smaller magnitude)", period)
	}

	// Critical invariant: exactly one gateway call regardless of period count.
	gw.AssertExpectations(t)
	gw.AssertNumberOfCalls(t, "GetFXRate", 1)
}

// TestService_ConvertFinancialsToUSD_MixedCurrencies covers the realistic
// case where periods carry different currencies (e.g., a ticker that
// re-domiciled). Each currency must be looked up exactly once and applied
// only to its own periods.
func TestService_ConvertFinancialsToUSD_MixedCurrencies(t *testing.T) {
	ctx := context.Background()

	gw := &fxMockMacroGateway{}
	gw.On("GetFXRate", mock.Anything, "TWD", "USD").Return(0.0312, nil).Once()
	gw.On("GetFXRate", mock.Anything, "EUR", "USD").Return(1.085, nil).Once()

	svc := newFXTestService(t, gw)

	hist := &entities.HistoricalFinancialData{
		Ticker: "MIXED",
		Data: map[string]*entities.FinancialData{
			"2022FY": {Ticker: "MIXED", FilingPeriod: "2022FY", ReportingCurrency: "TWD", Revenue: 1_000_000_000_000},
			"2023FY": {Ticker: "MIXED", FilingPeriod: "2023FY", ReportingCurrency: "TWD", Revenue: 1_100_000_000_000},
			"2024FY": {Ticker: "MIXED", FilingPeriod: "2024FY", ReportingCurrency: "EUR", Revenue: 50_000_000_000},
		},
	}

	err := svc.convertFinancialsToUSD(ctx, hist)
	require.NoError(t, err)

	assert.InDelta(t, 31_200_000_000.0, hist.Data["2022FY"].Revenue, 1.0)
	assert.InDelta(t, 34_320_000_000.0, hist.Data["2023FY"].Revenue, 1.0)
	assert.InDelta(t, 54_250_000_000.0, hist.Data["2024FY"].Revenue, 1.0)

	for _, p := range []string{"2022FY", "2023FY", "2024FY"} {
		assert.Equal(t, "USD", hist.Data[p].ReportingCurrency)
	}

	gw.AssertExpectations(t)
	gw.AssertNumberOfCalls(t, "GetFXRate", 2)
}

// TestService_ConvertFinancialsToUSD_OperatingLeaseCommitments_Converted
// exercises the special-case map: keys (year strings) are metadata, but the
// values are monetary and DO get FX-multiplied.
func TestService_ConvertFinancialsToUSD_OperatingLeaseCommitments_Converted(t *testing.T) {
	ctx := context.Background()

	gw := &fxMockMacroGateway{}
	gw.On("GetFXRate", mock.Anything, "TWD", "USD").Return(0.0312, nil).Once()

	svc := newFXTestService(t, gw)

	hist := &entities.HistoricalFinancialData{
		Ticker: "TSM",
		Data: map[string]*entities.FinancialData{
			"2023FY": {
				Ticker:            "TSM",
				ReportingCurrency: "TWD",
				OperatingLeaseCommitments: map[string]float64{
					"2025": 100_000,
					"2026": 80_000,
				},
			},
		},
	}

	err := svc.convertFinancialsToUSD(ctx, hist)
	require.NoError(t, err)

	got := hist.Data["2023FY"].OperatingLeaseCommitments
	require.NotNil(t, got)

	// Year-string keys are unchanged — they're metadata, not currency.
	_, has2025 := got["2025"]
	_, has2026 := got["2026"]
	assert.True(t, has2025, "year key 2025 must survive conversion")
	assert.True(t, has2026, "year key 2026 must survive conversion")

	// Values are FX-multiplied.
	assert.InDelta(t, 3120.0, got["2025"], 0.001, "lease commitment value must be FX-converted")
	assert.InDelta(t, 2496.0, got["2026"], 0.001, "lease commitment value must be FX-converted")

	gw.AssertExpectations(t)
}
