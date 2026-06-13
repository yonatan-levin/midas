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
	a6 := NewAssetAdjuster()
	cleaningCtx := &entities.CleaningContext{}

	// Synthetic JPM-shaped balance sheet: huge total assets, NO ROU asset
	// (banks do not capitalize operating leases at any material scale).
	bank := &entities.FinancialData{
		Ticker:                        "JPM",
		TotalAssets:                   3_900_000_000_000.0, // ~$3.9T
		OperatingLeaseRightOfUseAsset: 0.0,                 // banks: no material ROU
		CashAndCashEquivalents:        500_000_000_000.0,
		Revenue:                       170_000_000_000.0,
	}

	a6Out, err := a6.ApplyA6RightOfUseAssets(context.Background(), bank, productionRightOfUseRule(), cleaningCtx)
	require.NoError(t, err)
	require.Len(t, a6Out.LedgerEntries, 1)
	assert.False(t, a6Out.LedgerEntries[0].Fired,
		"A6 must take the no-ROU skip path on a bank balance sheet")
	assert.Empty(t, a6Out.Overlays, "A6 emits no overlay when ROU is absent")
}
