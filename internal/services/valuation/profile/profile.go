// Package profile is the Tier 2 AssumptionProfile backbone keyed by
// (Archetype × Maturity). The package is the deliberate boundary that
// prevents the Go import cycle `models → profile → models`: it contains
// NO imports of internal/services/valuation/models or internal/core/entities.
// Translation from entities.FinancialData / MarketData / HistoricalFinancialData
// to the neutral Facts DTO lives at the consumer site (service.go).
//
// Phase P0a ships the full type system, JSON-backed Registry, resolver, and
// validation. Downstream phases (P0b-P4) wire the Registry into service.go
// and consume ResolvedProfile from ModelInput. See:
//
//	docs/refactoring/spec/assumption-profile-spec.md             (canonical spec)
//	docs/refactoring/implementations/assumption-profile-implementation-plan.md
package profile

// Archetype identifies the company shape for valuation calibration purposes.
// See spec §3.1 for the canonical 21-archetype enum.
type Archetype string

const (
	ArchetypeMatureLargeScale       Archetype = "mature_large_scale"
	ArchetypeMatureLargeBank        Archetype = "mature_large_bank"
	ArchetypeGrowthBank             Archetype = "growth_bank"
	ArchetypeInsuranceCompany       Archetype = "insurance_company"
	ArchetypeSoftwareLikeLargeScale Archetype = "software_like_large_scale"
	ArchetypeSoftwareLikeScaling    Archetype = "software_like_scaling"
	ArchetypeCyclicalMidCycle       Archetype = "cyclical_mid_cycle"
	ArchetypeCyclicalTrough         Archetype = "cyclical_trough"
	ArchetypeHypergrowthEarly       Archetype = "hypergrowth_early"
	ArchetypeHypergrowthProfitable  Archetype = "hypergrowth_profitable"
	ArchetypePreRevenueBiotech      Archetype = "pre_revenue_biotech"
	ArchetypeMaturingTechDividend   Archetype = "maturing_tech_first_dividend"
	ArchetypeMatureDividendTech     Archetype = "mature_dividend_tech"
	ArchetypeREITResidential        Archetype = "reit_residential"
	ArchetypeREITCommercial         Archetype = "reit_commercial"
	ArchetypeREITIndustrial         Archetype = "reit_industrial"
	ArchetypeREITHealthcare         Archetype = "reit_healthcare"
	ArchetypeREITDataCenter         Archetype = "reit_datacenter"
	ArchetypeREITCellTower          Archetype = "reit_celltower"
	ArchetypeREITRetail             Archetype = "reit_retail"
	ArchetypeREITSpecialty          Archetype = "reit_specialty"
)

// Maturity identifies the company's life-cycle position.
type Maturity string

const (
	MaturityMature         Maturity = "mature"
	MaturityStandardGrowth Maturity = "standard_growth"
	MaturityHighGrowth     Maturity = "high_growth"
)

// RevenueBaseMethod identifies how the model should normalize the revenue base.
type RevenueBaseMethod string

const (
	RevenueBaseRawTTM             RevenueBaseMethod = "raw_ttm"
	RevenueBaseTwoYearAverage     RevenueBaseMethod = "two_year_average"
	RevenueBaseMaxTTMOrFloor      RevenueBaseMethod = "max_ttm_or_floor"
	RevenueBaseMidCycleNormalized RevenueBaseMethod = "mid_cycle_normalized"
)

// TerminalMethod identifies how the model should compute terminal value.
type TerminalMethod string

const (
	TerminalGordonGrowth TerminalMethod = "gordon_growth"
	TerminalExitMultiple TerminalMethod = "exit_multiple"
)

// DiscountMethod identifies the discount rate to apply.
type DiscountMethod string

const (
	DiscountWACC         DiscountMethod = "wacc"
	DiscountCostOfEquity DiscountMethod = "cost_of_equity"
)

// AssumptionProfile is the full per-(archetype, maturity) calibration record.
// All 14 fields are populated at JSON load time; values are validated by
// validation.go. Downstream phases (P1-P4) consume the subset relevant to
// each model — DCF reads horizon/terminal fields, DDM reads DPS fields, etc.
type AssumptionProfile struct {
	// Identity & key
	ProfileID string    `json:"profile_id"`
	Archetype Archetype `json:"archetype"`
	Maturity  Maturity  `json:"maturity"`

	// Used by all 4 models
	HorizonYears      int               `json:"horizon_years"`
	CompoundGrowthCap float64           `json:"compound_growth_cap"`
	RevenueBaseMethod RevenueBaseMethod `json:"revenue_base_method"`
	DiscountMethod    DiscountMethod    `json:"discount_method"`

	// DCF + RM-3 terminal handling
	TerminalMethod   TerminalMethod `json:"terminal_method"`
	Stabilized       bool           `json:"stabilized"`
	FadeYears        int            `json:"fade_years"`
	TerminalMultiple float64        `json:"terminal_multiple"`

	// VAL-2 DDM specifics. DividendForecastHorizon == 0 is the bit-for-bit
	// preservation signal for the legacy mature-large-bank single-stage
	// Gordon path; see IsLegacyMatureLargeBankDDM below.
	DPSGrowthCap            float64   `json:"dps_growth_cap"`
	PayoutPath              []float64 `json:"payout_path"`
	DividendForecastHorizon int       `json:"dividend_forecast_horizon"`
	StableDividendGrowth    float64   `json:"stable_dividend_growth"`

	// Archetype-specific size thresholds override the global fallback in
	// maturity bucketing. Why: large-cap means different things for a bank
	// ($50B) versus a pre-revenue biotech ($2B); one global threshold
	// misclassifies one or the other.
	SizeThresholds *SizeThresholds `json:"size_thresholds,omitempty"`
}

// SizeThresholds carries archetype-specific revenue cutoffs used by the
// resolver's Stage-2 maturity bucketing.
type SizeThresholds struct {
	LargeCapMinUSD float64 `json:"large_cap_min_usd"`
	MidCapMinUSD   float64 `json:"mid_cap_min_usd"`
}

// ResolvedProfile is what gets stamped onto ModelInput.Profile after
// successful resolution. Embeds the full AssumptionProfile so consumers
// read fields directly; carries the ResolutionTrace for audit/replay.
type ResolvedProfile struct {
	AssumptionProfile                 // embedded; consumers read fields directly
	Trace             ResolutionTrace `json:"trace"`
}

// IsLegacyMatureLargeBankDDM reports whether the resolved profile is the
// legacy single-stage Gordon path. Models MUST consult this to take the
// bit-for-bit preservation branch (spec §3.1, §8.2).
//
// Two signals must coincide: archetype=mature_large_bank AND
// dividend_forecast_horizon=0. Either alone is insufficient — a
// mature_large_bank with horizon>0 is the new multi-stage path; a
// horizon-0 profile on any other archetype is undefined behavior.
func (r *ResolvedProfile) IsLegacyMatureLargeBankDDM() bool {
	return r != nil && r.DividendForecastHorizon == 0 &&
		r.Archetype == ArchetypeMatureLargeBank
}
