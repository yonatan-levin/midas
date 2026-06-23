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
//	docs/refactoring/archive/assumption-profile-spec.md             (canonical spec)
//	docs/refactoring/archive/assumption-profile-implementation-plan.md
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

// ReinvestmentMethod selects the Layer-A DCF reinvestment / operating-leverage
// trajectory. The empty value (a profile that predates Layer A) is treated as
// ReinvestmentLegacyProportional — the bit-for-bit opt-out.
type ReinvestmentMethod string

const (
	// ReinvestmentLegacyProportional is the pre-Layer-A path: CapEx/ΔWC/D&A scale
	// proportionally with cumulative OI growth. Bit-for-bit with the prior engine.
	ReinvestmentLegacyProportional ReinvestmentMethod = "legacy_proportional"
	// ReinvestmentSalesToCapital is the recommended unified term:
	// Reinvestment_t = ΔRevenue_t / SalesToCapital_t (efficiency rising over the fade).
	ReinvestmentSalesToCapital ReinvestmentMethod = "sales_to_capital"
	// ReinvestmentDecliningCapexIntensity is the documented fallback:
	// Reinvestment_t = NetIntensity_t × Revenue_t (intensity declining over the fade).
	ReinvestmentDecliningCapexIntensity ReinvestmentMethod = "declining_capex_intensity"
)

// BaseMarginMethod selects how the Layer-A margin-convergence path seeds its base
// operating margin. The empty value defaults to BaseMarginTTM.
type BaseMarginMethod string

const (
	BaseMarginTTM        BaseMarginMethod = "ttm"
	BaseMarginTwoYearAvg BaseMarginMethod = "two_year_average"
	BaseMarginMidCycle   BaseMarginMethod = "mid_cycle"
)

// AssumptionProfile is the full per-(archetype, maturity) calibration record.
// Fields are populated at JSON load time; values are validated by validation.go.
// Downstream phases consume the subset relevant to each model — DCF reads
// horizon/terminal + Layer-A reinvestment fields, DDM reads DPS fields, etc. The
// Layer-A reinvestment/margin block (added 2026-06) is additive and optional:
// profiles that omit it keep the legacy proportional DCF path bit-for-bit.
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

	// --- Layer A: reinvestment / operating-leverage trajectory (DCF path only) ---
	//
	// All fields are ADDITIVE and OPTIONAL. A profile that omits them (every
	// pre-Layer-A profile) resolves ReinvestmentMethod to "" which the service
	// layer + engine treat as legacy_proportional — bit-for-bit unchanged. Only
	// profiles that explicitly set ReinvestmentMethod to a non-legacy value engage
	// the new projection. Spec §6.1.
	ReinvestmentMethod    ReinvestmentMethod `json:"reinvestment_method,omitempty"`
	SalesToCapitalStart   float64            `json:"sales_to_capital_start,omitempty"`  // starting (low) sales-to-capital for scaling firms
	SalesToCapitalTarget  float64            `json:"sales_to_capital_target,omitempty"` // mature-industry norm the ratio improves toward
	CapExIntensityStart   float64            `json:"capex_intensity_start,omitempty"`   // fallback path: starting net-reinvestment / revenue
	CapExIntensityMature  float64            `json:"capex_intensity_mature,omitempty"`  // fallback path: mature net-reinvestment / revenue
	ReinvestmentFadeYears int                `json:"reinvestment_fade_years,omitempty"` // years over which efficiency reaches target
	MaintenanceCapexFloor float64            `json:"maintenance_capex_floor,omitempty"` // §7.1 floor as a fraction of revenue

	// Margin-convergence path.
	BaseMarginMethod       BaseMarginMethod `json:"base_margin_method,omitempty"`       // how to seed the base operating margin
	TargetOperatingMargin  float64          `json:"target_operating_margin,omitempty"`  // archetype/industry-capped ceiling
	MarginConvergenceYears int              `json:"margin_convergence_years,omitempty"` // years over which margin expands base → target
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

// IsCyclicalArchetype reports whether the resolved profile is a cyclical
// archetype (mid-cycle or trough). VAL-1 Phase 3 uses this to gate
// cyclical-base normalization: for cyclical firms the DCF base operating
// income is floored at the 3-year FY mean so a trough year doesn't make the
// projected rebound look aggressive. Keeping the cyclical taxonomy inside the
// profile package (mirrors IsLegacyMatureLargeBankDDM) means a future
// cyclical_* archetype only updates this one predicate. Nil-safe: a nil
// receiver returns false.
func (r *ResolvedProfile) IsCyclicalArchetype() bool {
	return r != nil &&
		(r.Archetype == ArchetypeCyclicalMidCycle ||
			r.Archetype == ArchetypeCyclicalTrough)
}
