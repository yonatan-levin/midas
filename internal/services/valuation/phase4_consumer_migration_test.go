package valuation

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/cleaneddata"
)

// firedRestaterEntry builds a fired Restater-role LedgerEntry that the
// cleaneddata.Restated() reducer applies (EquityOffset → StockholdersEquity).
// Mirrors A2's emission shape (Component:"OtherIntangibles", EquityOffset
// negative for a writedown).
func firedRestaterEntry(component string, delta, equityOffset float64) entities.LedgerEntry {
	return entities.LedgerEntry{
		Timestamp:    time.Now(),
		AdjusterID:   "TEST_RESTATER",
		RuleID:       "test_restater",
		Fired:        true,
		Component:    component,
		DeltaAmount:  delta,
		EquityOffset: equityOffset,
	}
}

// TestPerformValuation_RestatedReadsAtROIC verifies the ROIC consumer reads the
// Restated() view (DC-1 Phase 4 C-2 §4.2.1). The migrated read goes through
// restatedViewOr(cleaned, latest); this test pins that the Restated equity
// (post EquityOffset) — NOT the as-reported equity — is what the ROIC
// denominator sees.
func TestPerformValuation_RestatedReadsAtROIC(t *testing.T) {
	// Post-clean entity: the dispatcher has already applied the component delta
	// (OtherIntangibles reduced) and the ledger carries the EquityOffset that
	// Restated() folds into StockholdersEquity.
	const originalEquity = 1_000_000.0
	const equityOffset = -200_000.0 // A2-style writedown hit to equity
	postClean := &entities.FinancialData{
		StockholdersEquity:        originalEquity,
		InterestBearingDebt:       500_000.0,
		NormalizedOperatingIncome: 300_000.0,
		AdjustmentLedger: entities.AdjustmentLedger{
			firedRestaterEntry("OtherIntangibles", -200_000.0, equityOffset),
		},
	}
	asReported := &entities.FinancialData{
		StockholdersEquity:        originalEquity,
		InterestBearingDebt:       500_000.0,
		NormalizedOperatingIncome: 300_000.0,
	}
	cleaned := cleaneddata.New(asReported, postClean)

	// The migrated ROIC read uses restatedViewOr; assert it returns the
	// Restated (offset-applied) equity, not the as-reported value.
	view := restatedViewOr(cleaned, asReported)
	assert.InDelta(t, originalEquity+equityOffset, view.StockholdersEquity, 1e-6,
		"ROIC denominator must read Restated().StockholdersEquity (equity offset applied)")
	assert.NotEqual(t, asReported.StockholdersEquity, view.StockholdersEquity,
		"Restated equity must differ from as-reported when a Restater fires")

	// Nil-cleaned fallback path returns the entity's own equity (identity).
	fallbackView := restatedViewOr(nil, asReported)
	assert.InDelta(t, originalEquity, fallbackView.StockholdersEquity, 1e-6,
		"nil-cleaned ROIC read must fall back to the entity's equity")
}

// TestPerformValuation_NWCChangeUsesRestated verifies calculateNetWorkingCapitalChange
// reads the latest period's working-capital fields from the Restated() view
// rather than the raw entity (DC-1 Phase 4 C-2 §4.2.7). The fixture gives the
// raw `latest` arg a deliberately-wrong stamped CurrentAssets while the
// post-clean entity's plug components reconstruct a different (correct) value;
// the test proves the function used the reconstructed view value.
func TestPerformValuation_NWCChangeUsesRestated(t *testing.T) {
	svc := &Service{}

	// Post-clean latest: plug components reconstruct CurrentAssets=500k,
	// CurrentLiabilities=300k. The stamped umbrella fields are deliberately
	// LEFT at a stale/incoherent value to prove the view (not the umbrella)
	// drives the math.
	postCleanLatest := &entities.FinancialData{
		CurrentAssets:           999_999, // stale umbrella — must be ignored
		CurrentLiabilities:      999_999, // stale umbrella — must be ignored
		OtherCurrentAssets:      500_000,
		OtherCurrentLiabilities: 300_000,
		FilingDate:              time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
	}
	cleaned := cleaneddata.New(postCleanLatest, postCleanLatest)

	prior := &entities.FinancialData{
		OtherCurrentAssets:      400_000,
		OtherCurrentLiabilities: 250_000,
		FilingDate:              time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC),
		Revenue:                 1_000_000,
		OperatingIncome:         100_000,
		SharesOutstanding:       1000,
	}
	historical := &entities.HistoricalFinancialData{
		Ticker: "TEST",
		Data: map[string]*entities.FinancialData{
			"2023FY": prior,
			"2024FY": postCleanLatest,
		},
	}

	// Latest NWC (from Restated view) = 500_000 - 300_000 = 200_000
	// Prior NWC (one-shot Restated)   = 400_000 - 250_000 = 150_000
	// Delta = 50_000
	result := svc.calculateNetWorkingCapitalChange(historical, postCleanLatest, cleaned)
	assert.InDelta(t, 50_000.0, result, 1e-6,
		"NWC change must read CurrentAssets/CurrentLiabilities from the Restated view (reconstructed from plug components), not the stale stamped umbrella")
}

// TestEffectiveOI_ReadsView pins the Phase 4 effectiveOI signature flip: the
// helper now reads a *cleaneddata.FinancialDataView (Restated OI) rather than
// the raw entity.
func TestEffectiveOI_ReadsView(t *testing.T) {
	view := &cleaneddata.FinancialDataView{
		NormalizedOperatingIncome: 250_000,
		OperatingIncome:           100_000,
	}
	assert.InDelta(t, 250_000.0, effectiveOI(view), 1e-6,
		"effectiveOI prefers NormalizedOperatingIncome when positive")

	fallback := &cleaneddata.FinancialDataView{
		NormalizedOperatingIncome: 0,
		OperatingIncome:           80_000,
	}
	assert.InDelta(t, 80_000.0, effectiveOI(fallback), 1e-6,
		"effectiveOI falls back to OperatingIncome when normalized is non-positive")
}

// TestPerformValuation_CrossCheckReadsRestated pins that the DCF cross-check +
// the alt-model OI routing read the Restated view (DC-1 Phase 4 C-3 §4.2.6 +
// §4.2.2). A C1-style restructuring add-back raises Restated
// NormalizedOperatingIncome above the as-reported value; the migrated reads
// (cross-check EBITDA via OperatingIncome, NOPAT-fallback guard,
// effectiveOI, router OI) must all see the restated figure.
func TestPerformValuation_CrossCheckReadsRestated(t *testing.T) {
	const reportedOI = 100_000_000.0
	const reportedNOI = 100_000_000.0
	const addBack = 30_000_000.0 // C1 restructuring add-back

	// Post-clean entity: the dispatcher (via the generic component-delta
	// helper) has already raised NormalizedOperatingIncome by the add-back and
	// the ledger carries the fired entry. OperatingIncome is identity-copied.
	postClean := &entities.FinancialData{
		OperatingIncome:           reportedOI,
		NormalizedOperatingIncome: reportedNOI + addBack,
		NetIncome:                 80_000_000,
		AdjustmentLedger: entities.AdjustmentLedger{
			firedRestaterEntry("NormalizedOperatingIncome", addBack, addBack),
		},
	}
	asReported := &entities.FinancialData{
		OperatingIncome:           reportedOI,
		NormalizedOperatingIncome: reportedNOI,
		NetIncome:                 80_000_000,
	}
	cleaned := cleaneddata.New(asReported, postClean)

	view := restatedViewOr(cleaned, asReported)
	assert.InDelta(t, reportedNOI+addBack, view.NormalizedOperatingIncome, 1e-6,
		"cross-check / NOPAT-guard / router OI must read Restated().NormalizedOperatingIncome (add-back applied)")
	// effectiveOI prefers the (now restated) normalized OI.
	assert.InDelta(t, reportedNOI+addBack, effectiveOI(view), 1e-6,
		"effectiveOI must reflect the restated normalized OI")
	// NetIncome (used by the cross-check EPS/FCF) is not Restater-touched today
	// but is read via the view for coherence.
	assert.InDelta(t, 80_000_000.0, view.NetIncome, 1e-6)
}

// TestPerformValuation_GrahamUsesAsReported pins that the Graham consumer reads
// the AsReported() view, NOT Restated() (DC-1 Phase 4 §4.2.9). The fixture
// makes the post-clean (restated) entity's CurrentAssets components diverge
// from the pre-clean (as-reported) snapshot; the Graham metrics must reflect
// the as-filed (snapshot) CurrentAssets so NCAV stays a conservative as-filed
// floor and is immune to the §8.2.1 Option A umbrella-incoherence transitional
// state.
func TestPerformValuation_GrahamUsesAsReported(t *testing.T) {
	asReported := &entities.FinancialData{
		CurrentAssets:      200_000_000,
		TotalLiabilities:   50_000_000,
		OtherCurrentAssets: 200_000_000, // plug reproduces the umbrella
	}
	// Post-clean: a Restater has reduced the components AND the umbrella is now
	// incoherent (Option A). If Graham mistakenly read Restated/the post-clean
	// entity, CurrentAssets would differ.
	postClean := &entities.FinancialData{
		CurrentAssets:      999_999_999, // incoherent umbrella (Option A)
		TotalLiabilities:   50_000_000,
		OtherCurrentAssets: 120_000_000, // reduced components → Restated CA = 120M
	}
	cleaned := cleaneddata.New(asReported, postClean)

	const dilutedShares = 1_000_000.0
	gf := calculateGrahamFloorMetrics(context.Background(), zap.NewNop(), "TEST",
		asReportedViewOr(cleaned, postClean), dilutedShares, 100.0)

	require.NotNil(t, gf.CurrentAssetsPerShare)
	// AsReported CurrentAssets = 200M / 1M shares = 200.0 (NOT the restated 120M).
	assert.InDelta(t, 200.0, *gf.CurrentAssetsPerShare, 1e-6,
		"Graham must read as-filed CurrentAssets (AsReported), not Restated")
	require.NotNil(t, gf.NCAVPerShare)
	// NCAV = (200M - 50M) / 1M = 150.0 from as-filed values.
	assert.InDelta(t, 150.0, *gf.NCAVPerShare, 1e-6,
		"Graham NCAV must be computed from as-filed CurrentAssets and TotalLiabilities")
}

func TestRestatedViewOr_NilFallbackIdentity(t *testing.T) {
	fd := &entities.FinancialData{
		NormalizedOperatingIncome: 123_456,
		StockholdersEquity:        789_000,
		InterestBearingDebt:       42_000,
	}
	v := restatedViewOr(nil, fd)
	require.NotNil(t, v)
	assert.InDelta(t, 123_456.0, v.NormalizedOperatingIncome, 1e-6)
	assert.InDelta(t, 789_000.0, v.StockholdersEquity, 1e-6)
	assert.InDelta(t, 42_000.0, v.InterestBearingDebt, 1e-6)
}
