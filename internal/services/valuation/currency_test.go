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
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

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
	svc := NewService(nil, nil, nil, nil, nil, nil, &MockMetricsService{}, cfg, zap.NewNop(), newTestCalcEmitter(), nil)
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

// ---------------------------------------------------------------------------
// Phase B10 — applyADRRatio tests
// ---------------------------------------------------------------------------
//
// The contract under test:
//   - applyADRRatio divides SharesOutstanding and DilutedSharesOutstanding on
//     EVERY period by the configured ADR ratio. No other field is touched.
//   - Tickers absent from config get ratio 1 → no-op (must produce identical
//     results to pre-B10 era for domestic 10-K filers).
//   - When marketData carries Yahoo's reported sharesOutstanding (already
//     ADR-equivalent), a deviation of (post-divide-shares vs Yahoo-shares) of
//     more than 10% emits a WARN log but is non-blocking — the configured
//     ratio still wins.
//   - Function does NOT mark the data; it is single-call-only by contract.
//   - nil *ADRRatios receiver and nil *MarketData are both safe (no panic).

// newADRTestService builds a Service with a configured *ADRRatios map. Mirrors
// newFXTestService but adds the adrRatios field which Phase B10 reads. The
// returned service uses the supplied logger so tests can capture WARN lines
// via zaptest/observer.
func newADRTestService(t *testing.T, ratios map[string]int, logger *zap.Logger) *Service {
	t.Helper()
	cfg := &config.Config{
		Valuation: config.ValuationConfig{
			CacheTTL:                 1 * time.Hour,
			DefaultTerminalGrowthCap: 0.025,
			DCFMaxGrowthRate:         0.5,
			DCFMinGrowthRate:         -0.3,
		},
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	svc := NewService(nil, nil, nil, nil, nil, nil, &MockMetricsService{}, cfg, logger, newTestCalcEmitter(), nil)
	// Override the on-disk-loaded *ADRRatios with the test fixture so tests are
	// hermetic and don't depend on config/adr_ratios.json contents.
	if ratios == nil {
		svc.adrRatios = nil
	} else {
		svc.adrRatios = &ADRRatios{Ratios: ratios}
	}
	return svc
}

// TestService_ApplyADRRatio_TSM_Divides5x pins the calculation-safety contract:
// 25_932_733_242 ordinary shares / ratio 5 = 5_186_546_648.4 ADR-equivalent
// shares. This is the concrete TSM 2024-12-31 value from the captured artifact;
// any drift in the divide arithmetic surfaces as a 5x per-share-value bug.
func TestService_ApplyADRRatio_TSM_Divides5x(t *testing.T) {
	ctx := context.Background()
	svc := newADRTestService(t, map[string]int{"TSM": 5}, nil)

	hist := &entities.HistoricalFinancialData{
		Ticker: "TSM",
		Data: map[string]*entities.FinancialData{
			"2024FY": {
				Ticker:                   "TSM",
				FilingPeriod:             "2024FY",
				SharesOutstanding:        25_932_733_242,
				DilutedSharesOutstanding: 25_932_733_242,
				Revenue:                  90_000_000_000, // touched only as a non-shares sentinel
			},
		},
	}

	// marketData = nil → cross-check is skipped; pure divide path under test.
	svc.applyADRRatio(ctx, "TSM", hist, nil)

	got := hist.Data["2024FY"]
	assert.InDelta(t, 5_186_546_648.4, got.SharesOutstanding, 1.0,
		"SharesOutstanding must be ordinary / 5 = ADR-equivalent")
	assert.InDelta(t, 5_186_546_648.4, got.DilutedSharesOutstanding, 1.0,
		"DilutedSharesOutstanding must be ordinary / 5 = ADR-equivalent")
	// Sentinel: Revenue must NOT be touched. Phase B9 owns FX; B10 owns shares.
	assert.Equal(t, 90_000_000_000.0, got.Revenue,
		"Revenue must NOT be touched by applyADRRatio")
}

// TestService_ApplyADRRatio_NoEntry_NoOp asserts the domestic-filer invariant:
// AAPL is absent from the map → ratio 1 → shares unchanged. This guarantees
// pre-B10 era results for 10-K filers stay byte-identical.
func TestService_ApplyADRRatio_NoEntry_NoOp(t *testing.T) {
	ctx := context.Background()
	svc := newADRTestService(t, map[string]int{"TSM": 5}, nil)

	hist := &entities.HistoricalFinancialData{
		Ticker: "AAPL",
		Data: map[string]*entities.FinancialData{
			"2024FY": {
				Ticker:                   "AAPL",
				SharesOutstanding:        15_550_061_000,
				DilutedSharesOutstanding: 15_550_061_000,
			},
		},
	}

	svc.applyADRRatio(ctx, "AAPL", hist, nil)

	got := hist.Data["2024FY"]
	assert.Equal(t, 15_550_061_000.0, got.SharesOutstanding,
		"AAPL absent from ADR map → ratio 1 → shares unchanged")
	assert.Equal(t, 15_550_061_000.0, got.DilutedSharesOutstanding,
		"AAPL diluted shares must be unchanged")
}

// TestService_ApplyADRRatio_NilADRRatios_NoOp asserts the receiver-nil safety
// contract: callers should never panic if the *ADRRatios field is nil (e.g.,
// service constructed in a test that bypasses NewService).
func TestService_ApplyADRRatio_NilADRRatios_NoOp(t *testing.T) {
	ctx := context.Background()
	svc := newADRTestService(t, nil, nil)
	require.Nil(t, svc.adrRatios, "test fixture: adrRatios must be nil")

	hist := &entities.HistoricalFinancialData{
		Ticker: "TSM",
		Data: map[string]*entities.FinancialData{
			"2024FY": {
				Ticker:                   "TSM",
				SharesOutstanding:        25_932_733_242,
				DilutedSharesOutstanding: 25_932_733_242,
			},
		},
	}

	// Must not panic.
	svc.applyADRRatio(ctx, "TSM", hist, nil)

	got := hist.Data["2024FY"]
	assert.Equal(t, 25_932_733_242.0, got.SharesOutstanding,
		"nil adrRatios must be a no-op (Get returns 1)")
	assert.Equal(t, 25_932_733_242.0, got.DilutedSharesOutstanding)
}

// TestService_ApplyADRRatio_DivergentYFShares_WarnsButProceeds asserts the
// drift-detection contract: when post-divide shares deviate by > 10% from
// Yahoo's reported (already-ADR-equivalent) shares count, we emit a WARN with
// all required fields BUT still apply the divide. Operator gets visibility to
// update config/adr_ratios.json without the request failing.
func TestService_ApplyADRRatio_DivergentYFShares_WarnsButProceeds(t *testing.T) {
	ctx := context.Background()

	// Capture WARN-and-above so we can assert on the deviation log.
	core, observed := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)
	svc := newADRTestService(t, map[string]int{"TSM": 5}, logger)

	hist := &entities.HistoricalFinancialData{
		Ticker: "TSM",
		Data: map[string]*entities.FinancialData{
			"2024FY": {
				Ticker:                   "TSM",
				FilingPeriod:             "2024FY",
				FilingDate:               time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC),
				SharesOutstanding:        25_932_733_242,
				DilutedSharesOutstanding: 25_932_733_242,
			},
		},
	}
	// Yahoo says ~1B shares; expected post-divide is ~5.19B → ~81% deviation.
	marketData := &entities.MarketData{
		Ticker:            "TSM",
		SharesOutstanding: 1_000_000_000,
	}

	svc.applyADRRatio(ctx, "TSM", hist, marketData)

	// Shares ARE divided despite the warning — non-blocking diagnostic.
	got := hist.Data["2024FY"]
	assert.InDelta(t, 5_186_546_648.4, got.SharesOutstanding, 1.0,
		"divide must still apply even when cross-check warns")
	assert.InDelta(t, 5_186_546_648.4, got.DilutedSharesOutstanding, 1.0)

	// Locate the deviation WARN. There may be other warns in flight; pick by
	// substring rather than indexing entries[0].
	entries := observed.All()
	var found *observer.LoggedEntry
	for i := range entries {
		if entries[i].Level == zapcore.WarnLevel && // narrow to WARN
			contains(entries[i].Message, "ADR ratio deviation > 10%") {
			found = &entries[i]
			break
		}
	}
	require.NotNil(t, found, "expected WARN log: 'ADR ratio deviation > 10%%' (got %d entries)", len(entries))

	fields := found.ContextMap()
	assert.Equal(t, "TSM", fields["ticker"])
	assert.EqualValues(t, 5, fields["configured_ratio"])
	assert.InDelta(t, 5_186_546_648.4, fields["expected_post_divide_shares"], 1.0)
	assert.InDelta(t, 1_000_000_000.0, fields["yahoo_reported_shares"], 0.001)
	// Deviation ≈ |5.19B - 1B| / 1B ≈ 4.187 → 418.7%. Just sanity-check it's > 10.
	dev, ok := fields["deviation_pct"].(float64)
	require.True(t, ok, "deviation_pct must be a float64 field")
	assert.Greater(t, dev, 10.0, "deviation must exceed the 10%% warn threshold")
}

// TestService_ApplyADRRatio_YFSharesWithinTolerance_NoWarn pins the inverse:
// Yahoo's count agrees with post-divide expectation within 10% → no WARN.
func TestService_ApplyADRRatio_YFSharesWithinTolerance_NoWarn(t *testing.T) {
	ctx := context.Background()

	core, observed := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)
	svc := newADRTestService(t, map[string]int{"TSM": 5}, logger)

	hist := &entities.HistoricalFinancialData{
		Ticker: "TSM",
		Data: map[string]*entities.FinancialData{
			"2024FY": {
				Ticker:                   "TSM",
				FilingPeriod:             "2024FY",
				FilingDate:               time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC),
				SharesOutstanding:        25_932_733_242,
				DilutedSharesOutstanding: 25_932_733_242,
			},
		},
	}
	// Expected post-divide = 5_186_546_648.4. Yahoo says 5.2B → ~0.26% deviation.
	marketData := &entities.MarketData{
		Ticker:            "TSM",
		SharesOutstanding: 5_200_000_000,
	}

	svc.applyADRRatio(ctx, "TSM", hist, marketData)

	for _, e := range observed.All() {
		assert.NotContains(t, e.Message, "ADR ratio deviation > 10%",
			"no deviation WARN when Yahoo shares are within 10%% of expected")
	}
}

// TestService_ApplyADRRatio_NilMarketData_NoCrossCheck pins the nil-marketData
// safety contract. Cross-check requires Yahoo's shares; nil short-circuits.
func TestService_ApplyADRRatio_NilMarketData_NoCrossCheck(t *testing.T) {
	ctx := context.Background()

	core, observed := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)
	svc := newADRTestService(t, map[string]int{"TSM": 5}, logger)

	hist := &entities.HistoricalFinancialData{
		Ticker: "TSM",
		Data: map[string]*entities.FinancialData{
			"2024FY": {
				Ticker:                   "TSM",
				SharesOutstanding:        25_932_733_242,
				DilutedSharesOutstanding: 25_932_733_242,
			},
		},
	}

	// Must not panic.
	svc.applyADRRatio(ctx, "TSM", hist, nil)

	got := hist.Data["2024FY"]
	assert.InDelta(t, 5_186_546_648.4, got.SharesOutstanding, 1.0,
		"divide must still apply with nil marketData")

	// No deviation WARN — cross-check was skipped.
	for _, e := range observed.All() {
		assert.NotContains(t, e.Message, "ADR ratio deviation > 10%",
			"no cross-check warn when marketData is nil")
	}
}

// TestService_ApplyADRRatio_MultiplePeriods_AllDivided pins the all-periods
// invariant: every period in the history is divided, not just the latest.
// Critical because growth + WACC consume historical periods directly.
func TestService_ApplyADRRatio_MultiplePeriods_AllDivided(t *testing.T) {
	ctx := context.Background()
	svc := newADRTestService(t, map[string]int{"TSM": 5}, nil)

	hist := &entities.HistoricalFinancialData{
		Ticker: "TSM",
		Data:   make(map[string]*entities.FinancialData, 5),
	}
	periods := []string{"2020FY", "2021FY", "2022FY", "2023FY", "2024FY"}
	for _, p := range periods {
		hist.Data[p] = &entities.FinancialData{
			Ticker:                   "TSM",
			FilingPeriod:             p,
			SharesOutstanding:        25_932_733_242,
			DilutedSharesOutstanding: 25_932_733_242,
		}
	}

	svc.applyADRRatio(ctx, "TSM", hist, nil)

	for _, p := range periods {
		got := hist.Data[p]
		assert.InDelta(t, 5_186_546_648.4, got.SharesOutstanding, 1.0,
			"period %s shares must be divided", p)
		assert.InDelta(t, 5_186_546_648.4, got.DilutedSharesOutstanding, 1.0,
			"period %s diluted shares must be divided", p)
	}
}

// contains is a tiny local helper for substring search used by the deviation
// log assertion. Avoids pulling in strings just to assert a fragment. Kept
// here, file-local, so it doesn't pollute package scope.
func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
