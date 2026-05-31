package valuation

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/cleaneddata"
	"github.com/midas/dcf-valuation-api/pkg/finance/dcf"
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

// TestPerformValuation_NWCChangeUsesAsReported verifies
// calculateNetWorkingCapitalChange reads the latest AND prior periods'
// working-capital umbrellas from the AsReported() view (the parser-stamped
// CurrentAssets/CurrentLiabilities), NOT from Restated() (DC-1 Phase 4 C-2
// REVIEWER-HIGH followup).
//
// REGRESSION GUARD: this fixture models the AMD-class case where the Phase-0
// plug UNDER-reconstructs the umbrella — i.e. stamped CurrentAssets !=
// Cash+Inventory+OtherCurrentAssets (and likewise for liabilities) — with ZERO
// Restaters firing. Restated() recomputes CA as sum(components)+plug
// (restate.go:68), so a Restated() read would silently drift NWC change → FCF →
// dcf_value_per_share. AsReported() is the identity copy of the stamped
// umbrella, so it stays bit-for-bit equal to pre-Phase-4. The latest/prior
// numbers below are chosen so that the AsReported (stamped) delta and the
// Restated (recomputed) delta are DIFFERENT — the assertion fails if the read
// ever flips back to Restated().
func TestPerformValuation_NWCChangeUsesAsReported(t *testing.T) {
	svc := &Service{}

	// Latest: stamped umbrellas are the as-filed truth (CA=16,505 / CL=9,000).
	// The plug components DELIBERATELY under-reconstruct: Cash+Inv+OtherCA =
	// 14,678 (AMD's real −1,827 plug shortfall) and OtherCL = 8,000. With zero
	// Restaters firing, Restated() would read 14,678 / 8,000 here.
	postCleanLatest := &entities.FinancialData{
		CurrentAssets:           16_505, // as-filed umbrella (AsReported truth)
		CurrentLiabilities:      9_000,  // as-filed umbrella (AsReported truth)
		CashAndCashEquivalents:  10_000,
		Inventory:               4_000,
		OtherCurrentAssets:      678,   // sum-of-components = 14,678 != 16,505 stamped
		OtherCurrentLiabilities: 8_000, // recomputed CL = 8,000 != 9,000 stamped
		FilingDate:              time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
	}
	cleaned := cleaneddata.New(postCleanLatest, postCleanLatest)

	// Prior: same shape — stamped CA=14,000 / CL=8,000, but components
	// under-reconstruct to 12,500 / 7,300. The cross-period plug shortfall
	// grows, so AsReported and Restated deltas do NOT coincide.
	prior := &entities.FinancialData{
		CurrentAssets:           14_000, // as-filed umbrella
		CurrentLiabilities:      8_000,  // as-filed umbrella
		CashAndCashEquivalents:  9_000,
		Inventory:               3_000,
		OtherCurrentAssets:      500,   // sum-of-components = 12,500 != 14,000 stamped
		OtherCurrentLiabilities: 7_300, // recomputed CL = 7,300 != 8,000 stamped
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

	// AsReported (CORRECT, pinned) basis:
	//   latest NWC = 16,505 - 9,000 = 7,505
	//   prior  NWC = 14,000 - 8,000 = 6,000
	//   delta      = 1,505
	const wantAsReportedDelta = 1_505.0

	// Restated (WRONG, recomputed-umbrella) basis — what the bug produced:
	//   latest NWC = (10,000+4,000+678) - 8,000 = 14,678 - 8,000 = 6,678
	//   prior  NWC = (9,000+3,000+500) - 7,300  = 12,500 - 7,300 = 5,200
	//   delta      = 1,478
	const restatedDelta = 1_478.0

	result := svc.calculateNetWorkingCapitalChange(historical, postCleanLatest, cleaned)
	assert.InDelta(t, wantAsReportedDelta, result, 1e-6,
		"NWC change must read the stamped (AsReported) CurrentAssets/CurrentLiabilities umbrellas, NOT the recomputed Restated umbrellas — otherwise the Phase-0 plug shortfall drifts FCF on AMD-class tickers")
	assert.Greater(t, math.Abs(result-restatedDelta), 1.0,
		"a Restated()-basis NWC delta (1,478) would mean the read regressed back to the recomputed umbrella")
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

// b3OverlayEntity returns a post-clean entity carrying a fired B3 contingent-
// liability OverlaySpec (Field:"DebtLikeClaims") of the given amount, plus the
// given interest-bearing debt. Mirrors the Phase 4 B3 routing: the contingent
// amount lives ONLY on the overlay (the dispatcher dual-write to TotalDebt is
// deleted), so Restated().InterestBearingDebt == ibd and
// InvestedCapital().DebtLikeClaims == amount.
func b3OverlayEntity(ibd, contingent float64) *entities.FinancialData {
	return &entities.FinancialData{
		InterestBearingDebt: ibd,
		Overlays: []entities.OverlaySpec{{
			OverlayID:       "B3_contingent_liability",
			RuleID:          "contingent_liabilities",
			Field:           "DebtLikeClaims",
			Operation:       "add",
			Amount:          contingent,
			AmountSemantics: entities.AmountIncremental,
		}},
	}
}

// TestPerformValuation_WACCUnaffectedByB3 is the defining B3 routing-flip pin
// (DC-1 Phase 4 C-4, spec §8.3 #3). A B3 contingent liability fires; the WACC
// capital-structure input (Restated().InterestBearingDebt) must be UNCHANGED
// versus the no-B3 case — contingent liabilities no longer inflate the
// interest-bearing capital denominator. The contingent amount surfaces ONLY in
// InvestedCapital().DebtLikeClaims.
func TestPerformValuation_WACCUnaffectedByB3(t *testing.T) {
	const ibd = 200_000_000.0
	const contingent = 1_000_000_000.0

	withB3 := cleaneddata.New(b3OverlayEntity(ibd, contingent), b3OverlayEntity(ibd, contingent))
	noB3Entity := &entities.FinancialData{InterestBearingDebt: ibd}
	noB3 := cleaneddata.New(noB3Entity, noB3Entity)

	// WACC reads Restated().InterestBearingDebt — identical with/without B3.
	assert.InDelta(t,
		restatedViewOr(noB3, nil).InterestBearingDebt,
		restatedViewOr(withB3, nil).InterestBearingDebt,
		1e-6,
		"WACC capital-structure debt (Restated().InterestBearingDebt) must be unaffected by a B3 contingent liability")
	assert.InDelta(t, ibd, restatedViewOr(withB3, nil).InterestBearingDebt, 1e-6,
		"Restated().InterestBearingDebt must equal the parser-stamped value (B-rule-free)")

	// The contingent amount lives in InvestedCapital().DebtLikeClaims.
	assert.InDelta(t, contingent, investedCapitalOr(withB3, nil).DebtLikeClaims, 1e-6,
		"B3 contingent amount must surface in InvestedCapital().DebtLikeClaims")
	assert.InDelta(t, 0.0, investedCapitalOr(noB3, nil).DebtLikeClaims, 1e-6,
		"no-B3 case must have zero DebtLikeClaims")
}

// TestPerformValuation_EquityBridgeSubtractsDebtLikeClaims verifies the EV→Equity
// bridge subtracts InvestedCapital().DebtLikeClaims (DC-1 Phase 4 C-4, spec
// §8.3 #4). result.EquityValue == EV - debt + cash - minority - preferred -
// debtLikeClaims.
func TestPerformValuation_EquityBridgeSubtractsDebtLikeClaims(t *testing.T) {
	const (
		ev         = 1_500_000_000.0
		ibd        = 200_000_000.0
		cash       = 50_000_000.0
		minority   = 0.0
		preferred  = 0.0
		contingent = 1_000_000_000.0
	)
	withB3 := cleaneddata.New(b3OverlayEntity(ibd, contingent), b3OverlayEntity(ibd, contingent))

	debtLikeClaims := investedCapitalOr(withB3, nil).DebtLikeClaims
	restatedDebt := restatedViewOr(withB3, nil).InterestBearingDebt

	// Reproduce the exact bridge the migrated consumer computes.
	got := dcf.CalculateEquityValueWithDebtLikeClaims(ev, restatedDebt, cash, minority, preferred, debtLikeClaims)
	want := ev - ibd + cash - minority - preferred - contingent
	assert.InDelta(t, want, got, 1e-6,
		"equity bridge must subtract DebtLikeClaims (B3 contingent) in addition to interest-bearing debt")

	// Without the DebtLikeClaims term the equity would be $1B higher — the
	// substantive correction the routing flip delivers.
	legacy := dcf.CalculateEquityValue(ev, restatedDebt, cash, minority, preferred)
	assert.InDelta(t, legacy-contingent, got, 1e-6,
		"the new bridge differs from the legacy 5-arg bridge by exactly the contingent amount")
}

// DC-1 Phase 5 P5-C1 CalculationVersion 4.3 → 4.4 LIVE-stamp coverage:
//   - DCF path: service_test.go::TestService_performValuation,
//     ::TestService_performValuation_TrueFCF, and
//     ::TestService_performValuation_FINZeroDPS_FallbackToDCF
//     (FIN → DDM-fail → DCF fallback) all assert
//     result.CalculationVersion == "4.4".
//   - Alt-model path: service_test.go::TestService_performValuation_NegativeOperatingIncome
//     routes to revenue_multiple (performAlternativeValuation) and
//     asserts result.CalculationVersion == "4.4".
//
// The previous incarnation of this gate was a self-referential
// TestCalculationVersion_IsV43/_IsV44 in this file; gpt-5.5 review
// (P5 post-merge MEDIUM-3) flagged it as non-behavioral (it asserted
// `require.Equal(t, "4.4", "4.4")` against a local constant — no
// production code path was exercised). DELETED in favor of the four
// live pins above, which DO exercise the stamp through performValuation /
// performAlternativeValuation. Updating those four pins (and the two
// inline service.go stamp comments at lines 1323 + 1635) is the canonical
// way to track future CalculationVersion bumps.

// TestCalculateTangibleValuePerShare_UsesView pins the DC-1 Phase 4 C-5
// migration of calculateTangibleValuePerShare to a *cleaneddata.FinancialDataView
// (spec §8.3 #11). It reads the view's TangibleAssets (AsReported — see the
// helper godoc for the spec deviation rationale) and the view's diluted-first
// share chain. Bit-for-bit equal to the pre-Phase-4 entity read because
// AsReported is an identity projection.
func TestCalculateTangibleValuePerShare_UsesView(t *testing.T) {
	svc := &Service{}
	fd := &entities.FinancialData{
		TangibleAssets:           11_000_000_000,
		DilutedSharesOutstanding: 110_000_000,
		SharesOutstanding:        100_000_000, // decoy — diluted must win
	}
	market := &entities.MarketData{SharesOutstanding: 100_000_000}

	view := cleaneddata.New(fd, fd).AsReported()
	got := svc.calculateTangibleValuePerShare(view, market)
	assert.InDelta(t, 100.0, got, 1e-6,
		"tangible value reads the view's TangibleAssets / diluted shares (11B / 110M = 100)")
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
