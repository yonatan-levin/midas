// Package params is the single source of truth for every valuation math knob.
// It defines the resolved EffectiveValuationParams struct that the engine reads,
// the precedence-layer Source enum, and the named default constants that replace
// scattered literals in service.go and the growth estimator.
//
// Precedence: config defaults < AssumptionProfile < request override.
// The Resolve* functions (resolve.go, T2) apply this precedence per knob.
//
// Import boundary (enforced by boundary_test.go): this package MUST NOT import
// internal/services/valuation/models or internal/core/entities — either would
// create a forbidden import cycle.
package params

// Source records which precedence layer supplied a resolved knob value.
// Used in EffectiveValuationParams.Provenance and echoed in the applied_overrides
// response field (design §8).
type Source string

const (
	// SourceDefault indicates the knob was resolved from a named default constant
	// or a config-layer default (the lowest-precedence tier).
	SourceDefault Source = "default"
	// SourceProfile indicates the knob was supplied by the AssumptionProfile for
	// this ticker (middle-precedence tier).
	SourceProfile Source = "profile"
	// SourceRequest indicates the knob was explicitly set by the request override
	// (highest-precedence tier; triggers cache bypass).
	SourceRequest Source = "request"
)

// EffectiveValuationParams is the fully-resolved knob set the engine reads.
// Every field is a concrete (non-pointer) value; the engine never reads scattered
// literals or Viper defaults directly after the params package is wired in.
//
// Provenance maps each touched knob name to its Source for the applied_overrides
// response echo. Only knobs that the resolver explicitly resolved (or that the
// request touched) are populated; un-touched knobs are absent from the map.
//
// Not goroutine-safe: this struct is created per-request and must not be shared
// across goroutines without external synchronisation.
type EffectiveValuationParams struct {
	// --- Terminal value ---

	// TerminalGrowthRate is the resolved terminal growth rate (g) used in the
	// Gordon Growth Model or as the perpetuity rate for exit-multiple blending.
	// When TerminalGrowthExplicit is true this is the caller-supplied absolute
	// value (subject to the < WACC invariant); otherwise it is auto-derived by
	// ResolveTerminal from TerminalGrowthCap, DefaultTerminalGrowthFloor, and
	// DefaultTerminalWACCSpread.
	TerminalGrowthRate float64

	// TerminalGrowthExplicit is true when the request explicitly supplied
	// terminal_growth_rate, skipping the auto-derive path in ResolveTerminal.
	// The < WACC hard invariant still applies.
	TerminalGrowthExplicit bool

	// TerminalGrowthCap is the ceiling used by the auto-derive path (replaces
	// the hardcoded 0.03 literal in service.go::calculateTerminalGrowthRate).
	// Resolves from DefaultTerminalGrowthCap ← request override.
	TerminalGrowthCap float64

	// TerminalMethod selects the terminal-value model:
	//   "gordon_growth"  — perpetuity of FCF (default)
	//   "exit_multiple"  — industry EV/EBITDA multiple
	// Resolves from DefaultTerminalMethod ← profile ← request override.
	TerminalMethod string

	// TerminalMultiple is the EV/EBITDA (or similar) multiple used when
	// TerminalMethod == "exit_multiple". Zero means "use the industry default
	// looked up at resolution time". Resolves from the industry EV/EBITDA lookup
	// ← profile ← request override.
	TerminalMultiple float64

	// --- Horizon + growth staging ---

	// HorizonYears is the explicit DCF forecast horizon in years.
	// Zero is the legacy signal "fall through to growth-rate-slice length".
	// Resolves from growth-rate length (legacy) ← profile ← request override.
	HorizonYears int

	// Stage1Years, Stage2Years, Stage3Years are the high-growth, fade, and
	// long-tail growth-stage durations supplied to the growth estimator.
	// Resolve from DefaultStage{1,2,3}Years ← request override.
	Stage1Years int
	Stage2Years int
	Stage3Years int

	// MaxGrowthRate and MinGrowthRate are the clamps applied inside the growth
	// estimator. Resolve from DefaultMaxGrowthRate/DefaultMinGrowthRate (config)
	// ← request override.
	MaxGrowthRate float64
	MinGrowthRate float64

	// --- WACC inputs ---

	// Beta is the equity beta used in the CAPM cost-of-equity calculation.
	// Negative-beta tickers are real (inverse-correlated assets).
	// Resolves from market data ← request override.
	Beta float64

	// RiskFreeRate is the risk-free nominal rate (e.g. 10-year Treasury yield).
	// Negative values are real (EUR/JPY/CHF regimes).
	// Resolves from macro data ← request override.
	RiskFreeRate float64

	// MarketRiskPremium is the equity risk premium (ERP) added to the risk-free
	// rate in CAPM. Must be ≥ 0; a negative ERP is economically nonsensical.
	// Resolves from macro/config ← request override.
	MarketRiskPremium float64

	// --- Tax ---

	// TaxRate is the effective corporate tax rate applied to after-tax cost of
	// debt (WACC), DCF FCF, and the alt-model ModelInput. Negative effective
	// rates are real (NOLs / tax credits). Resolves from entity TaxRate ←
	// request override.
	TaxRate float64

	// --- Diagnostics ---

	// Warnings collects non-fatal advisories raised during resolution (e.g. an
	// explicit terminal_growth_rate within 1% of WACC, or a profile-sourced
	// horizon that had to be clamped to the available growth-rate length). The
	// service drains these into result.Warnings so the caller sees them; they are
	// never returned as errors. Nil/empty on the clean default path, preserving
	// byte-identity (an empty slice adds nothing to result.Warnings).
	Warnings []string

	// --- Provenance ---

	// Provenance maps knob names (matching the options JSON field names) to the
	// Source that supplied each resolved value. Only knobs the resolver touched
	// or the request set are present. Populated by ResolveInputs; augmented by
	// ResolveTerminal for TerminalGrowthRate. Consumed by the handler to build
	// the applied_overrides response field (design §8).
	Provenance map[string]Source
}

// ---------------------------------------------------------------------------
// Named default constants — the core fix (design §4.3, plan §3.3)
//
// Each constant equals the literal or Viper default it replaces in existing
// code. They are REFLECTIONS of today's values, not new policy.
// The file:line citations are verified against the live code.
// ---------------------------------------------------------------------------

const (
	// DefaultTerminalGrowthCap is the cap applied during auto-derivation of the
	// terminal growth rate. Replaces the hardcoded literal
	//   maxTerminalGrowth := 0.03   (service.go:1726)
	// and matches the (unused-for-that-purpose) Viper default
	//   viper.SetDefault("valuation.default_terminal_growth_cap", 0.03)  (config.go:556)
	DefaultTerminalGrowthCap = 0.03

	// DefaultTerminalGrowthFloor is the minimum terminal growth rate applied when
	// the auto-derived rate is ≤ 0 ("viable businesses grow at least with prices").
	// Replaces the inline literal
	//   terminalGrowth = 0.02   (service.go:1733)
	// Also mirrors growth/estimator.go:44  TerminalGrowthFloor: 0.02
	DefaultTerminalGrowthFloor = 0.02

	// DefaultTerminalWACCSpread is the minimum required spread between WACC and
	// the terminal growth rate ("≥ 2% below WACC guard to prevent TV explosion").
	// Replaces the inline literal
	//   terminalGrowth = wacc - 0.02   (service.go:1737-1738)
	DefaultTerminalWACCSpread = 0.02

	// DefaultTerminalGrowthDegenWACCFloor is the absolute floor applied on the
	// auto-derive path AFTER the WACC-spread clamp, for the degenerate case where
	// WACC is so low that (wacc − DefaultTerminalWACCSpread) would drop the terminal
	// growth below 1%. Replaces the inner inline literal
	//   if terminalGrowth < 0.01 { terminalGrowth = 0.01 }   (service.go:1739-1741)
	//
	// This is DISTINCT from DefaultTerminalGrowthFloor (0.02): that floor guards the
	// ≤0 auto-derived rate ("viable businesses grow with prices"); THIS floor guards
	// the post-WACC-spread degenerate case (low-WACC regimes). They must NOT be
	// merged — using 0.02 here would change byte-identity in low-WACC rows.
	DefaultTerminalGrowthDegenWACCFloor = 0.01

	// DefaultStage1Years is the high-growth stage duration in the multi-stage
	// growth estimator. Mirrors DefaultEstimatorConfig() Stage1Years: 3
	// (growth/estimator.go:40)
	DefaultStage1Years = 3

	// DefaultStage2Years is the fade-stage duration in the multi-stage growth
	// estimator. Mirrors DefaultEstimatorConfig() Stage2Years: 4
	// (growth/estimator.go:41)
	DefaultStage2Years = 4

	// DefaultStage3Years is the long-tail extension stage. Zero keeps the legacy
	// 7-year (Stage1+Stage2) horizon; callers opt in by setting > 0. Mirrors
	// DefaultEstimatorConfig() Stage3Years: 0  (growth/estimator.go:42)
	DefaultStage3Years = 0

	// DefaultMaxGrowthRate is the upper clamp applied inside the growth estimator.
	// Mirrors viper.SetDefault("valuation.dcf_max_growth_rate", 0.5)  (config.go:566)
	// and DefaultEstimatorConfig() MaxGrowthRate: 0.5  (growth/estimator.go:37)
	DefaultMaxGrowthRate = 0.5

	// DefaultMinGrowthRate is the lower clamp applied inside the growth estimator.
	// Mirrors viper.SetDefault("valuation.dcf_min_growth_rate", -0.3)  (config.go:567)
	// and DefaultEstimatorConfig() MinGrowthRate: -0.3  (growth/estimator.go:38)
	DefaultMinGrowthRate = -0.3

	// DefaultTerminalMethod is the terminal-value model used when neither the
	// AssumptionProfile nor a request override specifies one.
	// Mirrors the inline label
	//   terminalMethodLabel := "gordon_growth"   (service.go:1109)
	// and profile.TerminalGordonGrowth = "gordon_growth"  (profile/profile.go:67)
	DefaultTerminalMethod = "gordon_growth"
)

// ---------------------------------------------------------------------------
// Defaults — the resolver's lower-precedence input (plan §3.4)
//
// The service projects config / profile / market / macro / entity values into
// this struct before calling Resolve*. Passing a value-struct keeps Resolve*
// pure (trivially table-testable, replay-deterministic) and avoids importing
// *config.Config.
// ---------------------------------------------------------------------------

// Defaults carries all the lower-precedence (config + profile + market/macro/entity)
// knob baselines that the resolver needs. The service is responsible for
// populating every field before calling ResolveInputs.
//
// Fields are ordinary scalars; zero values signal "no data available / use the
// named default constant". See each field comment for the exact "zero means"
// semantics.
type Defaults struct {
	// TerminalGrowthCap is the config-layer cap (ValuationConfig.DefaultTerminalGrowthCap).
	// Zero → resolver falls back to DefaultTerminalGrowthCap.
	TerminalGrowthCap float64

	// MaxGrowthRate / MinGrowthRate are the config-layer growth-rate bounds
	// (ValuationConfig.DCFMaxGrowthRate / DCFMinGrowthRate).
	// Zero → resolver falls back to DefaultMaxGrowthRate / DefaultMinGrowthRate.
	MaxGrowthRate float64 // "zero" means "no config value"; resolver uses DefaultMaxGrowthRate
	MinGrowthRate float64 // negative is meaningful; "zero" means "no config override"

	// Stage{1,2,3}Years are the estimator-config defaults (from DefaultEstimatorConfig).
	// Zero for Stage1/Stage2 → resolver falls back to DefaultStage{1,2}Years.
	// Zero for Stage3 is the legitimate default (legacy 7-year horizon), so the
	// resolver uses it as-is.
	Stage1Years int
	Stage2Years int
	Stage3Years int

	// Data-source baselines for WACC inputs / tax — already resolved by the
	// service from market / macro / entity data BEFORE calling the resolver.
	Beta              float64 // from marketData.GetEffectiveBeta()
	RiskFreeRate      float64 // from macroData.GetEffectiveRiskFreeRate()
	MarketRiskPremium float64 // from macroData.MarketRiskPremium
	TaxRate           float64 // from latestFinancialData.TaxRate

	// Profile-derived knob baselines. Zero / empty means "profile carries no
	// value for this knob"; the resolver uses the const default instead.
	ProfileHorizonYears     int    // resolvedProfile.HorizonYears (0 = legacy signal)
	ProfileTerminalMethod   string // resolvedProfile.TerminalMethod ("" = none)
	ProfileTerminalMultiple float64

	// IndustryExitMultiple is the industry EV/EBITDA lookup result (used as the
	// default source for TerminalMultiple when TerminalMethod == "exit_multiple").
	// Zero means "no industry default found".
	IndustryExitMultiple float64
}

// ---------------------------------------------------------------------------
// Overrides — the request-layer input (plan §3.4)
//
// All fields are pointers; nil means "not set by the request". The handler
// projects the transport DTO (ValuationOverrides) into this struct before
// passing it to the resolver. This keeps wire-format details out of the domain.
// ---------------------------------------------------------------------------

// Overrides carries the request-layer knob values projected from the transport
// DTO. Nil pointer = "the request did not set this knob".
type Overrides struct {
	TerminalGrowthRate *float64
	TerminalGrowthCap  *float64
	HorizonYears       *int
	Stage1Years        *int
	Stage2Years        *int
	Stage3Years        *int
	MaxGrowthRate      *float64
	MinGrowthRate      *float64
	TerminalMethod     *string
	TerminalMultiple   *float64
	TaxRate            *float64
	Beta               *float64
	RiskFreeRate       *float64
	MarketRiskPremium  *float64
}
