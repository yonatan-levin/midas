// Package cleaneddata exposes the three-view accessor surface
// (AsReported / Restated / InvestedCapital) over a post-clean
// *entities.FinancialData.
//
// Phase 3 invariant: the accessors are ADDITIVE. No production consumer
// reads from CleanedFinancialData yet — every downstream valuation site
// continues to read data.* directly until Phase 4 migrates them one at
// a time. The package exists so Phase 4 can flip consumers without
// further entity-shape changes.
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-3-spec.md
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-3-implementation-plan.md
package cleaneddata

import "time"

// ViewKind tags a FinancialDataView so consumers and debug logs can
// attribute reads to the correct accessor. The string values are part
// of the package's public contract — replay tooling and dashboards key
// off them.
type ViewKind string

const (
	// AsReportedView preserves parser-stamped values verbatim. T2-BS-3
	// carve-out: AMD/KO TotalLiabilities=0 stays 0 here.
	AsReportedView ViewKind = "as_reported"

	// RestatedView reconstructs balance-sheet umbrellas from
	// sum(components) + plug, applying LedgerEntry.EquityOffset and
	// LedgerEntry.TaxShieldDTA for every fired Restater-role adjuster.
	// AMD/KO get truthful TotalLiabilities here.
	RestatedView ViewKind = "restated"

	// InvestedCapitalView starts from Restated and applies OverlaySpec
	// entries: B1+B2+B3 contribute to DebtLikeClaims, A1 excludes
	// goodwill from TotalAssets per Damodaran convention.
	InvestedCapitalView ViewKind = "invested_capital"
)

// FinancialDataView is the read-only DTO returned by CleanedFinancialData's
// accessor methods. Fields mirror the consumed subset of
// entities.FinancialData (NOT all 100+ fields). Adding a field requires
// both a struct update here AND an update to whichever accessor populates
// it; the intentional friction is the point.
//
// All fields are value-typed. The accessors return a *FinancialDataView,
// but the pointer is to a value the caller is free to read without
// aliasing into the underlying CleanedFinancialData state.
type FinancialDataView struct {
	// Identification (identity across all views).
	Ticker            string
	CIK               string
	AsOf              time.Time
	ReportingCurrency string

	// Balance sheet (Restated/InvestedCapital recompute; AsReported is identity).
	TotalAssets         float64
	CurrentAssets       float64
	TangibleAssets      float64
	Goodwill            float64
	OtherIntangibles    float64
	Inventory           float64
	DeferredTaxAssets   float64
	TotalLiabilities    float64
	CurrentLiabilities  float64
	TotalDebt           float64
	InterestBearingDebt float64
	StockholdersEquity  float64

	// DebtLikeClaims is the InvestedCapital-only field: sum of B1 + B2 + B3
	// overlay amounts. Zero on AsReported and Restated views.
	DebtLikeClaims float64

	// Earnings (Restater-touched fields are recomputed for Restated).
	OperatingIncome           float64
	NormalizedOperatingIncome float64
	Revenue                   float64
	NetIncome                 float64
	InterestExpense           float64

	// Cash flow (identity across all three views in Phase 3 — no Restater
	// touches them today).
	OperatingCashFlow           float64
	CapitalExpenditures         float64
	DepreciationAndAmortization float64

	// Per-share (identity across all three views).
	SharesOutstanding        float64
	DilutedSharesOutstanding float64
	DividendsPerShare        float64

	// ViewKind records which accessor produced this view; debug-logging hook.
	ViewKind ViewKind
}
