package valuation

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/cleaneddata"
)

const floatTol = 1e-6

func ptrFloat(v float64) *float64 { return &v }

// arView wraps a synthetic *entities.FinancialData in the AsReported view the
// DC-1 Phase 4 Graham consumer now reads. AsReported is an identity projection
// (no umbrella recompute), so the wrapped view's CurrentAssets / TotalAssets /
// TotalLiabilities / StockholdersEquity are byte-identical to the entity's
// stamped values — these tests' expectations are unchanged.
func arView(fd *entities.FinancialData) *cleaneddata.FinancialDataView {
	if fd == nil {
		// Preserve the "nil financial data → nil view → sentinel" contract:
		// the production caller (asReportedViewOr) never produces a nil view
		// because latestFinancialData is non-nil by the time Graham runs, but
		// the U4 test exercises the nil-view guard directly.
		return nil
	}
	return cleaneddata.New(fd, fd).AsReported()
}

// assertPtrEq asserts both want and got are nil, or both are non-nil and
// agree to floatTol. Used uniformly across all four pointer fields on
// grahamFloor so each table row's expectation reads naturally.
func assertPtrEq(t *testing.T, want *float64, got *float64, name string) {
	t.Helper()
	if want == nil {
		assert.Nil(t, got, "%s: expected nil, got %v", name, got)
		return
	}
	require.NotNil(t, got, "%s: expected non-nil, got nil", name)
	assert.InDelta(t, *want, *got, floatTol, name)
}

func TestCalculateGrahamFloorMetrics(t *testing.T) {
	tests := []struct {
		name           string
		fd             *entities.FinancialData
		dilutedShares  float64
		currentPrice   float64
		wantCAPS       *float64 // nil = expect nil pointer; non-nil = expect non-nil pointer with this value
		wantNCAV       *float64
		wantFloor      *float64
		wantDiscount   *float64
		wantWarnSubstr string // empty == no warning expected
		wantSentinel   bool   // U3/U4: zero result, no warning, all four pointers nil
	}{
		{
			name: "U1 healthy positive NCAV (Sheet2 profile)",
			fd: &entities.FinancialData{
				CurrentAssets:    2_180_000_000,
				TotalLiabilities: 2_000_000_000,
			},
			dilutedShares: 39_540_000,
			currentPrice:  73.64,
			wantCAPS:      ptrFloat(2_180_000_000.0 / 39_540_000.0),
			wantNCAV:      ptrFloat((2_180_000_000.0 - 2_000_000_000.0) / 39_540_000.0),
			wantFloor:     ptrFloat(((2_180_000_000.0 - 2_000_000_000.0) / 39_540_000.0) * (2.0 / 3.0)),
			wantDiscount: ptrFloat(
				(73.64 - ((2_180_000_000.0-2_000_000_000.0)/39_540_000.0)*(2.0/3.0)) /
					(((2_180_000_000.0 - 2_000_000_000.0) / 39_540_000.0) * (2.0 / 3.0)),
			),
		},
		{
			name: "U2 distressed negative NCAV (MXL profile) clamps floor to &0.0, drops discount",
			fd: &entities.FinancialData{
				CurrentAssets:    249_450_000,
				TotalLiabilities: 316_450_000,
			},
			dilutedShares: 87_595_000,
			currentPrice:  11.20,
			wantCAPS:      ptrFloat(249_450_000.0 / 87_595_000.0),
			wantNCAV:      ptrFloat((249_450_000.0 - 316_450_000.0) / 87_595_000.0), // negative
			wantFloor:     ptrFloat(0.0),                                            // clamped, but pointer is non-nil
			wantDiscount:  nil,                                                      // floor==0 → discount nil
		},
		{
			name: "U3 zero diluted shares returns sentinel (all pointers nil, no warning)",
			fd: &entities.FinancialData{
				CurrentAssets:    100,
				TotalLiabilities: 50,
			},
			dilutedShares: 0,
			currentPrice:  5,
			wantSentinel:  true,
		},
		{
			name:          "U4 nil financial data returns sentinel (all pointers nil, no warning)",
			fd:            nil,
			dilutedShares: 100,
			currentPrice:  10,
			wantSentinel:  true,
		},
		{
			name: "U5 derivation fallback fires when TotalLiabilities==0",
			fd: &entities.FinancialData{
				CurrentAssets:      500_000_000,
				TotalAssets:        2_000_000_000,
				StockholdersEquity: 1_500_000_000,
				// TotalLiabilities deliberately zero
			},
			dilutedShares: 50_000_000,
			currentPrice:  20.0,
			// derived: 2B - 1.5B = 500M; NCAV = (500M - 500M) / 50M = 0
			wantCAPS:     ptrFloat(500_000_000.0 / 50_000_000.0),
			wantNCAV:     ptrFloat(0.0),
			wantFloor:    ptrFloat(0.0),
			wantDiscount: nil,
		},
		{
			name: "U6 unresolved liabilities (all balance-sheet inputs zero) emits warning + nil pointers",
			fd: &entities.FinancialData{
				CurrentAssets: 100_000_000,
			},
			dilutedShares:  10_000_000,
			currentPrice:   5,
			wantWarnSubstr: "insufficient balance-sheet data",
		},
		{
			name: "U7 negative derived liabilities (MXL signature) treated as unresolved",
			fd: &entities.FinancialData{
				CurrentAssets:      249_450_000,
				TotalAssets:        387_402_066,
				StockholdersEquity: 454_191_000,
				TotalLiabilities:   0,
			},
			dilutedShares:  87_595_000,
			currentPrice:   77.18,
			wantWarnSubstr: "insufficient balance-sheet data",
		},
		{
			name: "U8 boundary: floor exactly equals price → discount is &0.0 (NOT nil)",
			fd: &entities.FinancialData{
				CurrentAssets:    100_000_000,
				TotalLiabilities: 25_000_000, // NCAV = 75M; floor = 50M
			},
			dilutedShares: 1_000_000, // floor = 50M / 1M = 50
			currentPrice:  50.0,
			wantCAPS:      ptrFloat(100.0),
			wantNCAV:      ptrFloat(75.0),
			wantFloor:     ptrFloat(50.0),
			wantDiscount:  ptrFloat(0.0),
		},
		{
			name: "U9 floor > 0 but price == 0 → discount nil (delisted/unavailable)",
			fd: &entities.FinancialData{
				CurrentAssets:    100_000_000,
				TotalLiabilities: 25_000_000,
			},
			dilutedShares: 1_000_000,
			currentPrice:  0,
			wantCAPS:      ptrFloat(100.0),
			wantNCAV:      ptrFloat(75.0),
			wantFloor:     ptrFloat(50.0),
			wantDiscount:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateGrahamFloorMetrics(context.Background(), zap.NewNop(),
				"TEST", arView(tt.fd), tt.dilutedShares, tt.currentPrice)

			if tt.wantSentinel {
				assert.Equal(t, grahamFloor{}, got)
				return
			}

			if tt.wantWarnSubstr != "" {
				require.Len(t, got.Warnings, 1, "expected exactly one warning")
				assert.Contains(t, got.Warnings[0], tt.wantWarnSubstr)
				assert.Nil(t, got.CurrentAssetsPerShare)
				assert.Nil(t, got.NCAVPerShare)
				assert.Nil(t, got.GrahamFloorPerShare)
				assert.Nil(t, got.GrahamDiscountPct)
				return
			}

			assertPtrEq(t, tt.wantCAPS, got.CurrentAssetsPerShare, "CurrentAssetsPerShare")
			assertPtrEq(t, tt.wantNCAV, got.NCAVPerShare, "NCAVPerShare")
			assertPtrEq(t, tt.wantFloor, got.GrahamFloorPerShare, "GrahamFloorPerShare")
			assertPtrEq(t, tt.wantDiscount, got.GrahamDiscountPct, "GrahamDiscountPct")
			assert.Empty(t, got.Warnings, "no warnings expected on healthy path")
		})
	}
}

// TestResolveTotalLiabilities pins each branch of the fallback chain plus the
// audit-log emission on the derivation path.
func TestResolveTotalLiabilities(t *testing.T) {
	t.Run("direct path: TotalLiabilities populated", func(t *testing.T) {
		fd := &entities.FinancialData{TotalLiabilities: 1_000_000}
		got, ok := resolveTotalLiabilities(context.Background(), zap.NewNop(), "T", arView(fd))
		assert.True(t, ok)
		assert.Equal(t, 1_000_000.0, got)
	})

	t.Run("derived path: positive derivation, emits WARN with ticker + amounts", func(t *testing.T) {
		core, sink := observer.New(zap.WarnLevel)
		logger := zap.New(core)
		fd := &entities.FinancialData{TotalAssets: 2_000_000, StockholdersEquity: 1_500_000}

		got, ok := resolveTotalLiabilities(context.Background(), logger, "T", arView(fd))
		assert.True(t, ok)
		assert.Equal(t, 500_000.0, got)

		entries := sink.All()
		require.Len(t, entries, 1, "expected one WARN entry")
		assert.Equal(t, "graham_floor: derived total_liabilities from balance-sheet identity", entries[0].Message)
		fields := entries[0].ContextMap()
		assert.Equal(t, "T", fields["ticker"])
		assert.InDelta(t, 500_000.0, fields["derived_total_liabilities"], floatTol)
	})

	t.Run("derived path: negative derivation (MXL signature) returns unresolved", func(t *testing.T) {
		fd := &entities.FinancialData{TotalAssets: 387_402_066, StockholdersEquity: 454_191_000}
		got, ok := resolveTotalLiabilities(context.Background(), zap.NewNop(), "T", arView(fd))
		assert.False(t, ok)
		assert.Equal(t, 0.0, got)
	})

	t.Run("unresolved: all inputs zero", func(t *testing.T) {
		fd := &entities.FinancialData{}
		got, ok := resolveTotalLiabilities(context.Background(), zap.NewNop(), "T", arView(fd))
		assert.False(t, ok)
		assert.Equal(t, 0.0, got)
	})

	t.Run("unresolved: only TotalAssets present", func(t *testing.T) {
		fd := &entities.FinancialData{TotalAssets: 1_000_000}
		got, ok := resolveTotalLiabilities(context.Background(), zap.NewNop(), "T", arView(fd))
		assert.False(t, ok)
		assert.Equal(t, 0.0, got)
	})
}

// Sanity-pin that the U1 healthy-stock floor matches the spec's hand-computed
// example (Sheet2 healthy stock from graham-floor-metrics-spec.md §5.2).
func TestGrahamFloor_Sheet2Example(t *testing.T) {
	fd := &entities.FinancialData{
		CurrentAssets:    2_180_000_000,
		TotalLiabilities: 2_000_000_000,
	}
	gf := calculateGrahamFloorMetrics(context.Background(), zap.NewNop(),
		"EXAMPLE", arView(fd), 39_540_000, 73.64)

	// Spec §5.2 reference values (rounded for human readability):
	//   current_assets_per_share ≈ 55.13
	//   ncav_per_share          ≈ 4.55
	//   graham_floor_per_share  ≈ 3.03
	//   graham_discount_pct     ≈ 23.30 (price=73.64 vs floor=3.03)
	require.NotNil(t, gf.CurrentAssetsPerShare)
	require.NotNil(t, gf.NCAVPerShare)
	require.NotNil(t, gf.GrahamFloorPerShare)
	require.NotNil(t, gf.GrahamDiscountPct)
	assert.True(t, math.Abs(*gf.CurrentAssetsPerShare-55.13) < 0.01)
	assert.True(t, math.Abs(*gf.NCAVPerShare-4.55) < 0.01)
	assert.True(t, math.Abs(*gf.GrahamFloorPerShare-3.03) < 0.01)
	assert.True(t, math.Abs(*gf.GrahamDiscountPct-23.30) < 0.05)
}
