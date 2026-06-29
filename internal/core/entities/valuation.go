package entities

import (
	"time"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// ValuationResult represents the output of a DCF analysis
type ValuationResult struct {
	Ticker string    `json:"ticker"`
	AsOf   time.Time `json:"as_of"`

	// Core valuation metrics
	TangibleValuePerShare float64 `json:"tangible_value_per_share"` // Net tangible assets / shares
	DCFValuePerShare      float64 `json:"dcf_value_per_share"`      // Intrinsic value from DCF model

	// Graham-school asset-floor diagnostics (see internal/services/valuation/graham.go
	// and docs/refactoring/archive/graham-floor-metrics-spec.md). All four use *float64 +
	// omitempty: nil = TotalLiabilities unresolved (a warning is appended to
	// Warnings instead). Non-nil = resolved; the value may be negative
	// (NCAVPerShare on distressed companies) or 0 (GrahamFloorPerShare clamped
	// when NCAV is negative). Plain float64 + omitempty would silently drop
	// legitimate &0.0 values, collapsing the "unresolved" and "deep distress"
	// states into the same wire shape.
	CurrentAssetsPerShare *float64 `json:"current_assets_per_share,omitempty"`
	NCAVPerShare          *float64 `json:"ncav_per_share,omitempty"`
	GrahamFloorPerShare   *float64 `json:"graham_floor_per_share,omitempty"`
	GrahamDiscountPct     *float64 `json:"graham_discount_pct,omitempty"`

	// WACC components
	WACC           float64 `json:"wacc"`             // Weighted Average Cost of Capital
	CostOfEquity   float64 `json:"cost_of_equity"`   // CAPM-derived cost of equity
	CostOfDebt     float64 `json:"cost_of_debt"`     // After-tax cost of debt
	WeightOfEquity float64 `json:"weight_of_equity"` // E/(E+D)
	WeightOfDebt   float64 `json:"weight_of_debt"`   // D/(E+D)

	// Growth assumptions
	GrowthRate         float64   `json:"growth_rate"`                 // Summary growth rate (CAGR of projected rates, backward-compatible)
	GrowthRates        []float64 `json:"growth_rates,omitempty"`      // Per-year projected growth rates
	TerminalGrowthRate float64   `json:"terminal_growth_rate"`        // Long-term growth rate
	GrowthSource       string    `json:"growth_source,omitempty"`     // "analyst_blend", "historical_only", "default"
	GrowthConfidence   string    `json:"growth_confidence,omitempty"` // "high", "medium", "low"

	// DCF model details
	ProjectionYears int     `json:"projection_years"` // Number of explicit forecast years
	TerminalValue   float64 `json:"terminal_value"`   // Present value of terminal value

	// Input data timestamps
	FinancialDataAsOf time.Time `json:"financial_data_as_of"` // When the fundamental data was filed
	MarketDataAsOf    time.Time `json:"market_data_as_of"`    // When the market data was captured
	FilingPeriod      string    `json:"filing_period"`        // e.g., "2023Q4"

	// Calculation metadata
	CalculationMethod string   `json:"calculation_method"` // "standard_dcf", "simplified", etc.
	DataQualityScore  float64  `json:"data_quality_score"` // 0-100 score based on data completeness
	Warnings          []string `json:"warnings,omitempty"` // Any data quality or assumption warnings

	// Extended fields for comprehensive analysis
	CalculatedAt        time.Time       `json:"calculated_at"`
	DataQualityGrade    QualityGrade    `json:"data_quality_grade"`   // A, B, C, D, F
	CleaningReport      *CleaningReport `json:"cleaning_report"`      // Full cleaning report
	CleaningFlags       []Flag          `json:"cleaning_flags"`       // Key risk flags
	CleaningAdjustments []Adjustment    `json:"cleaning_adjustments"` // Applied adjustments
	MarketRiskPremium   float64         `json:"market_risk_premium"`
	EnterpriseValue     float64         `json:"enterprise_value"`
	EquityValue         float64         `json:"equity_value"`
	FinancialDataPeriod string          `json:"financial_data_period"`
	MarketDataDate      time.Time       `json:"market_data_date"`
	DataFreshnessScore  int             `json:"data_freshness_score"`
	CalculationVersion  string          `json:"calculation_version"`

	// Phase 4: Multiples sanity cross-check comparing DCF-implied multiples
	// against sector medians. Nil when cross-check data is unavailable.
	SanityCheck *SanityCheck `json:"sanity_check,omitempty"`

	// Industry classification metadata — both the SIC-derived label (used
	// internally to select the valuation model) and the balance-sheet
	// heuristic label (used by the datacleaner's rule loader). Surfacing
	// both lets API consumers detect classifier drift; see
	// docs/superpowers/specs/2026-04-23-industry-in-response-design.md.
	SICCodeRaw            string `json:"sic_code_raw,omitempty"`            // Raw SIC code from SEC (may be empty)
	IndustrySIC           string `json:"industry_sic,omitempty"`            // Canonical SIC-derived label from IndustryClassifier.Classify: "TECH", "MFG", "RETAIL", "UTIL", "FIN", "HEALTH", "ENERGY", "RESTATE", "TELECOM", "TRANS", "CONS", "NA" (or sub-industries like "TECH_SAAS")
	IndustryHeuristicCode string `json:"industry_heuristic_code,omitempty"` // GICS sector code from the balance-sheet heuristic (e.g. "45")
	IndustryHeuristicName string `json:"industry_heuristic_name,omitempty"` // Human-readable GICS sector name (e.g. "Information Technology")

	// RM-2 Phase 2: provenance of the EV/Revenue multiple. Populated only by the
	// revenue_multiple model — "Damodaran <date>" (SIC resolved a Damodaran
	// sector EV/Sales) or "sector-bucket" (Phase 1 classifier-bucket fallback).
	// DCF / DDM / FFO leave it "" so omitempty drops it from their responses.
	MultipleSource string `json:"multiple_source,omitempty"`

	// IFRS / FPI transparency fields (Phase B12 of the IFRS-FPI plan,
	// docs/refactoring/archive/ifrs-foreign-private-issuer-support-spec.md).
	//
	// ReportingCurrency is the ISO-4217 code that DCFValuePerShare and
	// every other monetary field on this result are denominated in.
	// Always "USD" — convertFinancialsToUSD (Phase B9) FX-converts every
	// non-USD period before WACC / growth / DCF math runs. Surfaced so
	// API consumers know not to re-convert.
	//
	// ADRRatioApplied is the ordinary-shares-per-ADR multiplier that
	// applyADRRatio (Phase B10) divided SharesOutstanding /
	// DilutedSharesOutstanding by before per-share values were computed.
	// Always 1 for domestic 10-K filers and unknown tickers; non-1 for
	// configured ADRs (TSM=5, BABA=8, …). Surfaced so API consumers can
	// reconcile the per-share value against the listed ADR price without
	// guessing the ratio.
	ReportingCurrency string `json:"reporting_currency,omitempty"`
	ADRRatioApplied   int    `json:"adr_ratio_applied,omitempty"`

	// CurrentPrice is the live per-share market price captured from the
	// market-data gateway (Yahoo Finance / Finzive) at the moment the
	// valuation was computed. Denominated in the same units as
	// DCFValuePerShare and TangibleValuePerShare (USD), and on the same
	// per-share basis — for ADRs this is the per-ADR price the exchange
	// publishes (e.g., TSM ≈ $402), which is directly comparable to the
	// per-ADR DCFValuePerShare produced after applyADRRatio. Surfaced so
	// API consumers can compute the upside/downside discount in one step
	// without a separate quote lookup. Zero when no market data was
	// available; omitempty keeps the JSON clean in that case.
	CurrentPrice float64 `json:"current_price,omitempty"`

	// Tier 2 P0b additive fields. All omitempty — when zero-valued
	// (legacy path / pre-P0b cached results / profile registry not wired)
	// they are omitted from JSON, preserving byte equality with pre-Tier-2
	// responses on the legacy DDM bit-for-bit path.
	//
	// AssumptionProfile is the resolved profile_id (e.g.
	// "mature_large_bank:mature"), surfaced so API consumers can correlate
	// the result with the calibration record that produced it. Empty when
	// the service's profileRegistry was nil (test paths only — production
	// always wires it).
	AssumptionProfile string `json:"assumption_profile,omitempty"`

	// ResolutionTrace is the full structured audit trail from
	// profile.Registry.Resolve. Pointer + omitempty so unresolved (test)
	// paths drop the field entirely; the spec §3.3 documented fields land
	// directly on the wire because ResolutionTrace already carries JSON
	// tags. Importing profile here introduces no cycle — profile imports
	// nothing from entities/models (see profile/import_boundary_test.go).
	ResolutionTrace *profile.ResolutionTrace `json:"resolution_trace,omitempty"`

	// DCF diagnostics — populated by P2 (DCF archetype-aware horizon work).
	// Declared in P0b for schema ownership so the JSON shape is stable
	// from this commit forward; P2 wires DCFModel.Calculate to fill them.
	DCFHorizonYears       int       `json:"dcf_horizon_years,omitempty"`
	DCFTerminalMethod     string    `json:"dcf_terminal_method,omitempty"`
	DCFTerminalPctOfEV    float64   `json:"dcf_terminal_pct_of_ev,omitempty"`
	DCFPerYearPV          []float64 `json:"dcf_per_year_pv,omitempty"`
	DCFTerminalGrowthUsed float64   `json:"dcf_terminal_growth_used,omitempty"`

	// DCFBaseNormalization records VAL-1 Phase 3 cyclical-base normalization:
	// "3y_mean" when the 3-year FY mean operating income floored the DCF base
	// (trough normalization fired), "latest" when the latest/TTM base was
	// already >= the mean. Omitempty + populated ONLY on the cyclical DCF path,
	// so non-cyclical and DDM/FFO/revenue_multiple responses are byte-identical.
	DCFBaseNormalization string `json:"dcf_base_normalization,omitempty"`

	// DCFGordonTerminalValue and DCFExitMultipleTerminalValue surface BOTH raw
	// terminal-value estimates (VAL-1 Phase 4), nominal (before discounting and
	// before the 50/50 blend). They mirror dcf.Result.GordonTV /
	// dcf.Result.ExitMultipleTV. The blended primary remains EnterpriseValue /
	// DCFValuePerShare; dcf_terminal_method tells the consumer which method drove
	// the terminal. DCFExitMultipleTerminalValue is 0 (omitempty drops it) on the
	// pure-Gordon path, so non-exit-multiple responses are byte-identical.
	DCFGordonTerminalValue       float64 `json:"dcf_gordon_terminal_value,omitempty"`
	DCFExitMultipleTerminalValue float64 `json:"dcf_exit_multiple_terminal_value,omitempty"`

	// DCFForwardDilutedShares / DCFAppliedDilutionRate record the VAL-1 Phase 5
	// diluted-share-forward adjustment (DCF path only). When a high-SBC profile
	// opts in (DilutedShareForwardEnabled), the DCF per-share denominator is the
	// diluted share count projected to the DCF horizon at DCFAppliedDilutionRate
	// (the clamped historical share-count CAGR). Both are omitempty and stay zero
	// on the default/no-op path (flag off, ineligible history, or non-DCF model),
	// so default-path and DDM/FFO/revenue_multiple responses are byte-identical.
	DCFForwardDilutedShares float64 `json:"dcf_forward_diluted_shares,omitempty"`
	DCFAppliedDilutionRate  float64 `json:"dcf_applied_dilution_rate,omitempty"`

	// AppliedOverrides carries the set of knobs that were explicitly set by the
	// request (source=="request"). Populated only when the request supplied at
	// least one override; nil/omitted on the default path so default-path
	// responses are byte-identical (omitempty).
	//
	// The carrier type is defined in this package to keep the import boundary
	// clean: entities must NOT import internal/services/valuation/params (that
	// package is a leaf). The service maps params.EffectiveValuationParams →
	// AppliedOverrideValue using plain interface{} + string fields, which is
	// all the handler needs to build the applied_overrides JSON object.
	AppliedOverrides map[string]AppliedOverrideValue `json:"applied_overrides,omitempty"`

	// AssumptionSources carries, per near-term assumption, which authority level
	// (Layer B Phase 2 §9) supplied the final value — keyed by assumption (e.g.
	// "capex_year1", "operating_margin_year1"). Populated ONLY when guidance OR a
	// non-default source actually fired; nil/omitted on the default (absent-
	// guidance) path so default-path responses stay byte-identical (omitempty —
	// the NF1 invariant). Mirrors AppliedOverrides' transport-clean shape.
	//
	// The carrier type lives in this package (not authority) to keep the import
	// boundary clean: entities must NOT import the authority/guidance leaf
	// packages, exactly as it must not import params. The service maps
	// authority.Resolution.Sources → AssumptionSourceValue.
	AssumptionSources map[string]AssumptionSourceValue `json:"assumption_sources,omitempty"`

	// VAL-3 Phase 2 (REIT FFO/AFFO). Both omitempty: only the FFO-model path
	// populates them, so DCF/DDM/revenue_multiple results omit them (byte-
	// identical wire shape). PFFOValuePerShare is the FFO-based number (present
	// on every REIT result); PAFFOValuePerShare is the AFFO-based number, present
	// only when maintenance capex is disclosed OR estimable (0.7× capex). When
	// PAFFO is present it equals the headline DCFValuePerShare.
	PFFOValuePerShare  float64 `json:"pffo_value_per_share,omitempty"`
	PAFFOValuePerShare float64 `json:"paffo_value_per_share,omitempty"`
}

// AssumptionSourceValue is the per-assumption payload on ValuationResult
// recording which §9 authority level supplied a near-term assumption and a
// human-readable detail (Layer B Phase 2, Decision 4). Source is one of
// "user_override" | "guidance" | "profile" | "historical" | "default".
//
// Defined in entities rather than authority to avoid an import cycle: entities
// is imported by both the service and handler layers, while authority is a
// service-layer leaf (mirrors AppliedOverrideValue / params).
type AssumptionSourceValue struct {
	// Source is the precedence layer that supplied this assumption's value.
	Source string `json:"source"`
	// Detail is an optional provenance string, e.g.
	// "accession=0000002488-26-000012 period=FY2026 conf=0.82 midpoint=$1.50B".
	Detail string `json:"detail,omitempty"`
}

// AppliedOverrideValue is the per-knob payload carried on ValuationResult when
// a request explicitly supplied that knob. Value holds the resolved scalar (the
// type mirrors the knob — float64, int, or string); Source is always "request"
// in v1 (the R5 design decision: echo only request-sourced knobs).
//
// Defined in entities rather than in the params or handlers packages to avoid
// import cycles: entities is imported by both the service layer and the handler
// layer, while params is a service-layer leaf and handlers is a transport leaf.
type AppliedOverrideValue struct {
	// Value is the resolved knob value as it was used by the engine (after any
	// layer-precedence merge). The concrete Go type matches the knob (float64
	// for rate/multiplier fields, int for year fields, string for method fields).
	Value interface{} `json:"value"`
	// Source is the precedence layer that supplied this value. Always "request"
	// for v1 (only request-set knobs are echoed per design §8 R5).
	Source string `json:"source"`
}

// SanityCheck contains cross-check multiples that compare the DCF-implied valuation
// against sector median multiples. Flags divergences > 2x or < 0.5x sector median.
type SanityCheck struct {
	ImpliedPE            float64  `json:"implied_pe"`                   // DCF value / EPS
	SectorMedianPE       float64  `json:"sector_median_pe"`             // Sector median P/E ratio
	ImpliedEVEBITDA      float64  `json:"implied_ev_ebitda"`            // DCF enterprise value / EBITDA
	SectorMedianEVEBITDA float64  `json:"sector_median_ev_ebitda"`      // Sector median EV/EBITDA
	ImpliedPFCF          float64  `json:"implied_pfcf,omitempty"`       // DCF value per share / FCF per share (omitted when zero FCF)
	SectorMedianPFCF     float64  `json:"sector_median_pfcf,omitempty"` // Sector median P/FCF ratio (omitted when unknown)
	IsReasonable         bool     `json:"is_reasonable"`                // True if implied multiples are within 0.5x-2x of sector medians
	Flags                []string `json:"flags,omitempty"`              // Specific warnings about divergences
}

// DCFProjection represents the detailed cash flow projections
type DCFProjection struct {
	Ticker string    `json:"ticker"`
	AsOf   time.Time `json:"as_of"`

	// Annual projections (typically 5 years)
	YearlyProjections []YearlyProjection `json:"yearly_projections"`

	// Terminal value calculation
	TerminalValue      float64 `json:"terminal_value"`
	TerminalGrowthRate float64 `json:"terminal_growth_rate"`

	// Discounting
	WACC                 float64 `json:"wacc"`
	PresentValue         float64 `json:"present_value"` // Sum of discounted cash flows
	PresentValuePerShare float64 `json:"present_value_per_share"`
}

// YearlyProjection represents cash flow projection for a single year
type YearlyProjection struct {
	Year            int     `json:"year"`             // Projection year (1, 2, 3, 4, 5)
	OperatingIncome float64 `json:"operating_income"` // Projected operating income
	NOPAT           float64 `json:"nopat"`            // Net Operating Profit After Tax
	FreeCashFlow    float64 `json:"free_cash_flow"`   // Free cash flow to firm
	DiscountFactor  float64 `json:"discount_factor"`  // (1 + WACC)^year
	PresentValue    float64 `json:"present_value"`    // FCF / discount factor

	// Growth calculations
	GrowthRate float64 `json:"growth_rate"` // Applied growth rate for this year
}

// CalculateTotalPresentValue returns the sum of all discounted cash flows plus terminal value
func (d *DCFProjection) CalculateTotalPresentValue() float64 {
	total := d.TerminalValue // Terminal value is already present value

	for _, projection := range d.YearlyProjections {
		total += projection.PresentValue
	}

	return total
}

// GetProjectionByYear returns the projection for a specific year (1-5)
func (d *DCFProjection) GetProjectionByYear(year int) *YearlyProjection {
	for i := range d.YearlyProjections {
		if d.YearlyProjections[i].Year == year {
			return &d.YearlyProjections[i]
		}
	}
	return nil
}

// CalculationInputs represents all the inputs used in valuation calculation
// Useful for debugging and transparency
type CalculationInputs struct {
	Ticker string `json:"ticker"`

	// Financial data inputs
	NormalizedOperatingIncome float64 `json:"normalized_operating_income"`
	TangibleAssets            float64 `json:"tangible_assets"`
	TotalDebt                 float64 `json:"total_debt"`
	SharesOutstanding         float64 `json:"shares_outstanding"`
	TaxRate                   float64 `json:"tax_rate"`

	// Market data inputs
	SharePrice          float64 `json:"share_price"`
	Beta                float64 `json:"beta"`
	MarketValueOfEquity float64 `json:"market_value_of_equity"`

	// Macro inputs
	RiskFreeRate      float64 `json:"risk_free_rate"`
	MarketRiskPremium float64 `json:"market_risk_premium"`

	// Calculated intermediates
	CostOfEquity       float64 `json:"cost_of_equity"`
	CostOfDebt         float64 `json:"cost_of_debt"`
	WeightOfEquity     float64 `json:"weight_of_equity"`
	WeightOfDebt       float64 `json:"weight_of_debt"`
	FiveYearGrowthRate float64 `json:"five_year_growth_rate"`

	// Configuration overrides
	Overrides map[string]interface{} `json:"overrides,omitempty"`
}
