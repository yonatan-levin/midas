package profile

import "strings"

// Facts is the neutral interchange struct populated by service.go from
// entities.FinancialData / HistoricalFinancialData / MarketData. It carries
// only the signals the resolver needs — keeping the profile package free of
// any entities or models imports (spec §3.2).
//
// Pointer fields distinguish "no signal" (nil) from "zero is meaningful"
// (non-nil pointer to zero). The distinction is load-bearing: a pre-revenue
// biotech has Revenue == &0.0 (zero is correct) while a malformed bundle has
// Revenue == nil (missing). The resolver treats these differently.
//
// IMPORTANT: this package contains NO imports of entities or models. The
// deliberate boundary prevents the Go import cycle:
//
//	models → profile → models    (FORBIDDEN)
//
// Construction logic that needs entities lives at the consumer site
// (service.go in P0b); NewFactsForTest below is exported only for unit
// tests that need a Facts value without dragging entities in.
type Facts struct {
	// Industry is the raw classifier output (e.g. "fin_large_bank",
	// "FIN_LARGE_BANK", with or without trailing whitespace).
	Industry string
	// IndustryNormalized is the upper-cased + trimmed form the resolver's
	// prefix-match consumes. Set at construction time so the resolver does
	// not re-normalize on every call.
	IndustryNormalized string

	// Revenue is TTM revenue (already-resolved via RM-1 helper at consumer
	// site). nil means missing; pointer-to-zero means zero is meaningful.
	Revenue *float64
	// OperatingIncome is signed; negative triggers the cyclical_trough
	// archetype override in resolver Stage 1b.
	OperatingIncome *float64
	NetIncome       *float64

	// RevenueGrowthYoY is computed at facts-construction time from the
	// most recent two periods. nil if fewer than two periods are available.
	RevenueGrowthYoY *float64
	// ConsecutivePositiveOIYears is a stability signal; 0 means none or
	// not computed.
	ConsecutivePositiveOIYears int

	MarketCap *float64
	// DividendsPerShare drives archetype refinement on dividend-paying
	// tickers (e.g. mature_dividend_tech vs. maturing_tech_first_dividend).
	DividendsPerShare *float64
}

// NewFactsForTest is exported for unit-test use only. Production Facts
// construction lives in service.go (P0b), where entities imports are
// available. Tests in the profile package use this constructor to build
// minimal Facts without taking on entities as a test dependency.
//
// Normalizes industry to upper-case-trimmed so the IndustryNormalized
// field is suitable for the resolver's prefix match.
func NewFactsForTest(industry string, revenue, oi *float64) Facts {
	return Facts{
		Industry:           industry,
		IndustryNormalized: strings.ToUpper(strings.TrimSpace(industry)),
		Revenue:            revenue,
		OperatingIncome:    oi,
	}
}
