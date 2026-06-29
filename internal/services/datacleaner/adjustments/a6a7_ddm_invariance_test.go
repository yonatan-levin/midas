package adjustments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestA6A7_DDMBanks_DoNotFire is the TDB-2 §6 invariant proof that the DDM
// mature-large-bank path (JPM/BAC/WFC) is unaffected by A6/A7.
//
// Banks carry no material operating-lease right-of-use assets (ROU ≈ 0 on a
// bank balance sheet), so A6 takes the no-ROU skip path. Their cash position is
// operating reserve, not non-operating excess relative to revenue. Even if A7
// DID fire, its overlay targets a view-only ExcessCash field that NO DDM input
// reads — DDM derives value from dividends. This test pins the structural
// guarantee: bank-shaped balance sheets produce Fired:false A6 (and an A7
// overlay, if any, cannot reach DDM math). Combined with
// TestDDM_LegacyPath_BitForBit, this makes the §6 "DDM byte-identical"
// guarantee testable at the adjuster boundary.
func TestA6A7_DDMBanks_DoNotFire(t *testing.T) {
	// SR-1 A3: the adapter struct was deleted; call ApplyA6RightOfUseAssets
	// directly on the AssetAdjuster (the production dispatch path).
	aa := NewAssetAdjuster()
	cleaningCtx := &entities.CleaningContext{}

	// TDB-2 REVIEWER NIT (b): cover all three mature-large-bank tickers that
	// TestDDM_LegacyPath_BitForBit pins bit-for-bit (JPM/BAC/WFC), not JPM
	// alone. Synthetic bank-shaped balance sheets: huge total assets, NO
	// operating-lease ROU (banks do not capitalize operating leases at any
	// material scale), large operating cash reserves.
	banks := []struct {
		ticker      string
		totalAssets float64
		cash        float64
		revenue     float64
	}{
		{"JPM", 3_900_000_000_000.0, 500_000_000_000.0, 170_000_000_000.0},
		{"BAC", 3_200_000_000_000.0, 400_000_000_000.0, 100_000_000_000.0},
		{"WFC", 1_900_000_000_000.0, 200_000_000_000.0, 80_000_000_000.0},
	}

	for _, b := range banks {
		t.Run(b.ticker, func(t *testing.T) {
			bank := &entities.FinancialData{
				Ticker:                        b.ticker,
				TotalAssets:                   b.totalAssets,
				OperatingLeaseRightOfUseAsset: 0.0, // banks: no material ROU
				CashAndCashEquivalents:        b.cash,
				Revenue:                       b.revenue,
			}

			// A6 must take the no-ROU skip path — no overlay reaches invested
			// capital, so InvestedCapital().TotalAssets is unchanged.
			a6Out, err := aa.ApplyA6RightOfUseAssets(context.Background(), bank, productionRightOfUseRule(), cleaningCtx)
			require.NoError(t, err)
			require.Len(t, a6Out.LedgerEntries, 1)
			assert.False(t, a6Out.LedgerEntries[0].Fired,
				"A6 must take the no-ROU skip path on a bank balance sheet")
			assert.Empty(t, a6Out.Overlays, "A6 emits no overlay when ROU is absent")

			// A7 DOES fire on a bank (cash >> 10% of revenue), but its overlay
			// can ONLY target the view-only ExcessCash field — NO DDM input (or
			// EV→Equity bridge term) reads it, so the DDM mature-large-bank path
			// stays byte-identical regardless. Pin that structural guarantee
			// rather than asserting A7 skips (it does not).
			a7Out, err := aa.ApplyA7ExcessCash(context.Background(), bank, productionExcessCashRule(), cleaningCtx)
			require.NoError(t, err)
			// A7 MUST actually fire here (bank cash >> 10%-of-revenue floor), or
			// the Field assertion below would pass vacuously and the "A7 does
			// fire" guarantee would be untested.
			require.NotEmpty(t, a7Out.Overlays,
				"A7 should fire on a bank: cash far exceeds the operating-cash floor")
			for _, ov := range a7Out.Overlays {
				assert.Equalf(t, "ExcessCash", ov.Field,
					"A7 overlay must target only the view-only ExcessCash field, never a DDM/bridge input (got %q)", ov.Field)
			}
		})
	}
}
