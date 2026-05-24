package cleaneddata

import "github.com/midas/dcf-valuation-api/internal/core/entities"

// AsReported returns the balance-sheet view that preserves parser-stamped
// values verbatim. Use for any analysis that must faithfully reflect what
// the filer disclosed, even if the parser missed a tag (e.g. AMD/KO
// TotalLiabilities=0 stays zero per T2-BS-3 Option B carve-out).
//
// First-call cost: O(field count). Subsequent calls: O(1) cached.
func (c *CleanedFinancialData) AsReported() *FinancialDataView {
	if c == nil {
		return zeroView(AsReportedView)
	}
	if c.asReported != nil {
		return c.asReported
	}
	v := identityCopy(c.raw)
	v.ViewKind = AsReportedView
	c.asReported = &v
	return c.asReported
}

// identityCopy projects the consumed subset of *entities.FinancialData
// into a fresh FinancialDataView. Used as the seed for all three view
// accessors; Restated/InvestedCapital then mutate the local copy in place.
//
// A nil raw produces a zero view (every field defaulted) so accessors
// stay safe in the absence of a backing entity.
func identityCopy(raw *entities.FinancialData) FinancialDataView {
	if raw == nil {
		return FinancialDataView{}
	}
	return FinancialDataView{
		Ticker:            raw.Ticker,
		CIK:               raw.CIK,
		AsOf:              raw.AsOf,
		ReportingCurrency: raw.ReportingCurrency,

		TotalAssets:         raw.TotalAssets,
		CurrentAssets:       raw.CurrentAssets,
		TangibleAssets:      raw.TangibleAssets,
		Goodwill:            raw.Goodwill,
		OtherIntangibles:    raw.OtherIntangibles,
		Inventory:           raw.Inventory,
		DeferredTaxAssets:   raw.DeferredTaxAssets,
		TotalLiabilities:    raw.TotalLiabilities,
		CurrentLiabilities:  raw.CurrentLiabilities,
		TotalDebt:           raw.TotalDebt,
		InterestBearingDebt: raw.InterestBearingDebt,
		StockholdersEquity:  raw.StockholdersEquity,

		OperatingIncome:           raw.OperatingIncome,
		NormalizedOperatingIncome: raw.NormalizedOperatingIncome,
		Revenue:                   raw.Revenue,
		NetIncome:                 raw.NetIncome,
		InterestExpense:           raw.InterestExpense,

		OperatingCashFlow:           raw.OperatingCashFlow,
		CapitalExpenditures:         raw.CapitalExpenditures,
		DepreciationAndAmortization: raw.DepreciationAndAmortization,

		SharesOutstanding:        raw.SharesOutstanding,
		DilutedSharesOutstanding: raw.DilutedSharesOutstanding,
		DividendsPerShare:        raw.DividendsPerShare,
	}
}

func zeroView(kind ViewKind) *FinancialDataView {
	v := FinancialDataView{ViewKind: kind}
	return &v
}
