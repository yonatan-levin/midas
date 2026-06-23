package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
	"github.com/midas/dcf-valuation-api/internal/observability/narrate"
	"github.com/midas/dcf-valuation-api/internal/services/valuation"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/params"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// ValuationCalculator abstracts the valuation service so handlers depend on
// an interface rather than a concrete type, following clean architecture.
// *valuation.Service satisfies this interface implicitly.
type ValuationCalculator interface {
	CalculateValuation(ctx context.Context, ticker string, opts *valuation.ValuationOptions) (*entities.ValuationResult, error)
}

// FairValueHandler handles fair value related HTTP requests
type FairValueHandler struct {
	valuationService ValuationCalculator
	// logger is retained for non-request contexts; request-path log sites use logctx.From(ctx)
	logger *zap.Logger
}

// NewFairValueHandler creates a new FairValueHandler instance
func NewFairValueHandler(valuationService ValuationCalculator, logger *zap.Logger) *FairValueHandler {
	return &FairValueHandler{
		valuationService: valuationService,
		logger:           logger,
	}
}

// Industry exposes both industry classifications the engine computes on every
// fair-value request: the SIC-derived label (canonical, used for valuation
// model selection) and the balance-sheet heuristic label (used by the
// datacleaner's industry-specific rule loader). Consumers can compare the two
// via the Match flag to surface classification drift.
// @Description Dual industry classification (SIC + heuristic) with a Match flag
type Industry struct {
	SICCode       string `json:"sic_code,omitempty" example:"3674"`                         // Raw SIC code from SEC (may be empty if SEC data lacked it)
	SIC           string `json:"sic,omitempty" example:"MFG"`                               // SIC-derived industry label from IndustryClassifier.Classify
	HeuristicCode string `json:"heuristic_code,omitempty" example:"45"`                     // GICS sector code from IndustryClassifier.ClassifyIndustry
	HeuristicName string `json:"heuristic_name,omitempty" example:"Information Technology"` // GICS sector name
	Match         bool   `json:"match" example:"true"`                                      // true when SIC and heuristic agree per the canonical mapping
}

// sicToGICS is the canonical mapping from SIC-classifier labels (as emitted by
// IndustryClassifier.Classify per config/datacleaner/industry_codes.json) to
// the set of GICS sector codes considered a "match". Keys here MUST correspond
// to the `code` fields in industry_codes.json — any divergence silently
// demotes every ticker in that sector to Match=false. The MFG -> {"20", "45"}
// multi-map is deliberate: semiconductors/hardware return SIC "MFG"
// (manufacturing) but GICS "45" (Information Technology), and that pairing is
// a legitimate match rather than classifier drift. Same rationale for
// RETAIL -> {"25", "30"} (grocery retailers) and CONS -> {"30", "25"}.
// Any combination outside this table — or a missing value on either side —
// yields Match=false (conservative, preferring false negatives over false
// positives as drift signals).
var sicToGICS = map[string]map[string]bool{
	"TECH":    {"45": true},             // Information Technology
	"MFG":     {"20": true, "45": true}, // Industrials OR Info Tech (semi/hardware mfrs)
	"RETAIL":  {"25": true, "30": true}, // Consumer Discretionary OR Consumer Staples (grocery)
	"UTIL":    {"55": true},             // Utilities
	"FIN":     {"40": true},             // Financials (B-1 fix: was incorrectly "FINL")
	"HEALTH":  {"35": true},             // Health Care
	"ENERGY":  {"10": true},             // Energy
	"RESTATE": {"60": true},             // Real Estate
	"TELECOM": {"50": true},             // Communication Services
	"TRANS":   {"20": true},             // Industrials (transportation)
	"CONS":    {"30": true, "25": true}, // Consumer Staples primary, Discretionary secondary

	// REIT subsector codes emitted by the RESTATE Pass-2 sub-industry
	// classifier (VAL-3 P1+P4, T2-P4-W1 prefix reconciliation). All map to
	// GICS "60" (Real Estate). With the REIT_* prefix convention, the
	// parent-strip fallback in matchSICToGICS would yield "REIT" (unmapped)
	// rather than collapsing to RETAIL/DATA/etc., so these explicit entries
	// are what binds each subsector to GICS 60.
	"REIT_RESIDENTIAL": {"60": true},
	"REIT_OFFICE":      {"60": true},
	"REIT_INDUSTRIAL":  {"60": true},
	"REIT_RETAIL":      {"60": true},
	"REIT_HEALTHCARE":  {"60": true},
	"REIT_DATACENTER":  {"60": true},
	"REIT_CELLTOWER":   {"60": true},
	"REIT_SPECIALTY":   {"60": true},
}

// matchSICToGICS returns true when the SIC-derived label and the heuristic
// GICS code agree per the sicToGICS table. Empty inputs are never a match.
//
// Lookup order:
//  1. Full-code exact match (catches REIT subsector codes like REIT_RETAIL
//     and REIT_DATACENTER that share the REIT_ prefix but each map
//     individually to GICS "60").
//  2. Strip-at-first-underscore parent prefix, then look up again. Lets the
//     classifier's Pass-2 sub-industry codes (TECH_SAAS, HEALTH_BIOTECH,
//     FIN_IB, MFG_SEMI, FIN_BANK, …) inherit their parent's GICS mapping
//     without an explicit entry per sub-industry. The parent-strip path is
//     NOT used for REIT_* codes because "REIT" itself is not a key in
//     sicToGICS — REIT subsectors rely on the full-code exact match above.
func matchSICToGICS(sicLabel, gicsCode string) bool {
	if sicLabel == "" || gicsCode == "" {
		return false
	}
	// 1. Exact full-code match wins so REIT_* subsector codes resolve to GICS 60
	//    directly rather than relying on the parent-strip path.
	if allowed, ok := sicToGICS[sicLabel]; ok {
		return allowed[gicsCode]
	}
	// 2. Fall back to parent prefix for codes whose subsector isn't listed
	//    explicitly in sicToGICS (TECH_SAAS, HEALTH_BIOTECH, FIN_BANK, …).
	if i := strings.IndexByte(sicLabel, '_'); i >= 0 {
		sicLabel = sicLabel[:i]
		if allowed, ok := sicToGICS[sicLabel]; ok {
			return allowed[gicsCode]
		}
	}
	return false
}

// BuildIndustryFromResult constructs the Industry response object from the
// classification fields plumbed onto ValuationResult. Returns nil when the
// engine produced no classification signal at all, so the response's
// omitempty-tagged Industry field disappears entirely.
//
// Exported in Phase R2 D1.1 (observability replay tooling) so the replay
// orchestration layer in internal/observability/replay/replay.go can rebuild
// a response-equivalent shape from *entities.ValuationResult and diff it
// against the bundle's recorded 17-response.json. The rename is logic-free —
// callers of the lowercase symbol moved to the capitalized name with no
// behavioral change.
func BuildIndustryFromResult(result *entities.ValuationResult) *Industry {
	if result == nil {
		return nil
	}
	if result.SICCodeRaw == "" && result.IndustrySIC == "" &&
		result.IndustryHeuristicCode == "" && result.IndustryHeuristicName == "" {
		return nil
	}
	return &Industry{
		SICCode:       result.SICCodeRaw,
		SIC:           result.IndustrySIC,
		HeuristicCode: result.IndustryHeuristicCode,
		HeuristicName: result.IndustryHeuristicName,
		Match:         matchSICToGICS(result.IndustrySIC, result.IndustryHeuristicCode),
	}
}

// FairValueResponse represents the response structure for fair value requests
// @Description Fair value calculation response with intrinsic valuation metrics
type FairValueResponse struct {
	Ticker                string    `json:"ticker" example:"AAPL"`                           // Stock ticker symbol
	WACC                  float64   `json:"wacc" example:"0.092"`                            // Weighted Average Cost of Capital
	GrowthRate            float64   `json:"growth_rate" example:"0.045"`                     // Summary growth rate (CAGR of projected rates)
	GrowthRates           []float64 `json:"growth_rates,omitempty"`                          // Per-year projected growth rates
	GrowthSource          string    `json:"growth_source,omitempty" example:"analyst_blend"` // Growth estimation source
	GrowthConfidence      string    `json:"growth_confidence,omitempty" example:"high"`      // Growth estimation confidence
	TangibleValuePerShare float64   `json:"tangible_value_per_share" example:"24.73"`        // Net tangible book value per share
	DCFValuePerShare      float64   `json:"dcf_value_per_share" example:"156.42"`            // Discounted cash flow fair value per share

	// VAL-3 Phase 2 — REIT FFO/AFFO. Both omitempty: present only on REIT
	// (FFO-model) responses; absent for DCF/DDM/revenue_multiple. PFFO is the
	// FFO-based number (always present on REIT responses); PAFFO is the AFFO-based
	// number, present only when maintenance capex is disclosed OR estimable
	// (0.7× capex). When PAFFO is present it equals the headline intrinsic value
	// (dcf_value_per_share); when absent the headline is PFFO.
	PFFOValuePerShare  float64 `json:"pffo_value_per_share,omitempty" example:"42.10"`
	PAFFOValuePerShare float64 `json:"paffo_value_per_share,omitempty" example:"31.50"`

	// Graham-school asset-floor diagnostics — see
	// docs/refactoring/archive/graham-floor-metrics-spec.md. All four use *float64 +
	// omitempty: nil = TotalLiabilities unresolved (a warning is appended to
	// `warnings`). Non-nil = resolved; values may be negative (NCAV on
	// distressed companies) or 0 (floor clamped when NCAV is negative). Pointer
	// types preserve the deep-distress signal (resolved + negative + clamped)
	// distinct from the unresolved-fallback signal (all four absent + warning).
	CurrentAssetsPerShare *float64 `json:"current_assets_per_share,omitempty" example:"55.13"`
	NCAVPerShare          *float64 `json:"ncav_per_share,omitempty" example:"4.55"`
	GrahamFloorPerShare   *float64 `json:"graham_floor_per_share,omitempty" example:"3.03"`
	GrahamDiscountPct     *float64 `json:"graham_discount_pct,omitempty" example:"23.30"`

	AsOf               string                `json:"as_of" example:"2025-08-13T22:15:34.402652598Z"`         // Timestamp of calculation
	DataQualityScore   float64               `json:"data_quality_score,omitempty" example:"85.5"`            // Data quality score (0-100)
	DataQualityGrade   string                `json:"data_quality_grade,omitempty" example:"B"`               // Data quality grade (A-F)
	CalculationMethod  string                `json:"calculation_method,omitempty" example:"multi_stage_dcf"` // Model used: multi_stage_dcf, ddm, ffo, revenue_multiple
	CalculationVersion string                `json:"calculation_version,omitempty" example:"4.9"`            // Engine version that produced this result
	Warnings           []string              `json:"warnings,omitempty"`                                     // Data quality or assumption warnings
	SanityCheck        *entities.SanityCheck `json:"sanity_check,omitempty"`                                 // Multiples cross-check against sector medians
	Industry           *Industry             `json:"industry,omitempty"`                                     // Dual industry classification (SIC + heuristic) for drift detection

	// Currency is the ISO-4217 code that dcf_value_per_share and
	// tangible_value_per_share are denominated in. Always "USD" — the
	// valuation service FX-converts each period's reporting-currency
	// monetary fields to USD via Phase B9 of the IFRS-FPI plan
	// (docs/refactoring/archive/ifrs-foreign-private-issuer-support-spec.md), so
	// API consumers MUST NOT re-convert. Surfaced so a downstream client
	// can display "USD" alongside the per-share value rather than guessing.
	Currency string `json:"currency" example:"USD"`

	// ADRRatioApplied is the ordinary-shares-per-ADR multiplier that the
	// valuation engine divided SEC-reported share counts by before
	// computing per-share values, so the resulting fair value compares
	// like-for-like with the listed ADR price. 1 for domestic 10-K filers
	// (and any ticker absent from config/adr_ratios.json); non-1 for
	// configured ADRs (TSM=5, BABA=8, …). Phase B10 of the IFRS-FPI plan.
	// Omitted from the JSON when zero (defensive — the service always
	// stamps a positive int via ADRRatios.Get, but omitempty keeps the
	// response clean if a future bug produces 0).
	ADRRatioApplied int `json:"adr_ratio_applied,omitempty" example:"5"`

	// CurrentPrice is the live per-share market price captured from the
	// market-data gateway (Yahoo Finance / Finzive) at the moment the
	// valuation was computed. Same denomination and per-share basis as
	// DCFValuePerShare and TangibleValuePerShare — for ADRs this is the
	// per-ADR exchange price, directly comparable to the per-ADR DCF
	// value the engine produces after applyADRRatio. Surfaced so a
	// consumer can compute the upside/downside discount ((dcf - price)
	// / price) without a second quote lookup. Omitted when zero.
	CurrentPrice float64 `json:"current_price,omitempty" example:"190.25"`

	// Tier 2 P0b additive fields. All omitempty — legacy mature-large-bank
	// DDM responses remain byte-identical (TestDDM_LegacyPath_BitForBit
	// pins this). AssumptionProfile is the resolved profile_id so API
	// consumers can correlate the result with the calibration record.
	// ResolutionTrace carries the full audit trail (matched_rule_id,
	// fallback_reason, config_hash) for replay determinism and audit. The
	// DCF diagnostic fields are declared here for schema ownership in P0b
	// even though P2 fills them — keeps the wire shape stable from this
	// commit forward.
	AssumptionProfile     string                   `json:"assumption_profile,omitempty" example:"mature_large_bank:mature"`
	ResolutionTrace       *profile.ResolutionTrace `json:"resolution_trace,omitempty"`
	DCFHorizonYears       int                      `json:"dcf_horizon_years,omitempty" example:"5"`
	DCFTerminalMethod     string                   `json:"dcf_terminal_method,omitempty" example:"gordon_growth"`
	DCFTerminalPctOfEV    float64                  `json:"dcf_terminal_pct_of_ev,omitempty"`
	DCFPerYearPV          []float64                `json:"dcf_per_year_pv,omitempty"`
	DCFTerminalGrowthUsed float64                  `json:"dcf_terminal_growth_used,omitempty"`
	// DCFBaseNormalization (VAL-1 Phase 3): "latest" | "3y_mean". Omitempty +
	// present only on the cyclical DCF path, so non-cyclical responses are
	// byte-identical (no key emitted).
	DCFBaseNormalization string `json:"dcf_base_normalization,omitempty" example:"3y_mean"`

	// AppliedOverrides echoes the valuation knobs that were explicitly set by
	// the request, each with the resolved value and source "request". Absent
	// (omitempty) when no overrides were supplied — default GET responses and
	// POST{} requests are byte-identical (TestPostFairValue_EmptyBody_EqualsGET
	// pins this). Only request-sourced knobs are echoed in v1 (design §8 R5).
	//
	// Each entry:
	//   "knob_name": { "value": <resolved scalar>, "source": "request" }
	//
	// Example with two overrides:
	//   "applied_overrides": {
	//     "tax_rate":    { "value": 0.21,  "source": "request" },
	//     "horizon_years": { "value": 7,   "source": "request" }
	//   }
	AppliedOverrides map[string]AppliedOverride `json:"applied_overrides,omitempty"`

	// AssumptionSources records, per near-term assumption (e.g. "capex_year1"),
	// which authority level supplied the final value — one of user_override |
	// guidance | profile | historical | default — with a provenance detail
	// (Layer-B Phase-2 §9.2). Populated ONLY when a non-default source fires (e.g.
	// a high-confidence guidance fixture anchors year-1); absent (omitempty) on the
	// default path, so guidance-free responses stay byte-identical to the 4.7
	// engine. Example:
	//   "assumption_sources": {
	//     "capex_year1": { "source": "guidance", "detail": "accession=… conf=0.82 midpoint=$1.50B" }
	//   }
	AssumptionSources map[string]entities.AssumptionSourceValue `json:"assumption_sources,omitempty"`

	// CleaningAdjustments is the datacleaner audit trail: one entry per
	// normalization adjuster that FIRED on this company's financials (A1–C7,
	// the B1/B2/B3 overlays, and the TDB-2 A6/A7 + TDB-12 contingent
	// overlays), projected from result.CleaningAdjustments via
	// adjustmentsFromLedger. Lets consumers see which restatements/overlays
	// shaped the valuation inputs (e.g. lease capitalization, inventory
	// restatement, excess-cash exclusion). Omitted (omitempty) when no
	// adjuster fired, so the default no-adjustment response stays
	// byte-identical to the pre-TDB-11 wire shape. Fired-only — the
	// projection emits only Applied==true entries.
	CleaningAdjustments []CleaningAdjustment `json:"cleaning_adjustments,omitempty"`
}

// CleaningAdjustment is the per-adjuster payload in the cleaning_adjustments
// response array. It projects the audit-relevant fields of an
// entities.Adjustment (the cleaner's internal carrier) into the transport
// layer, omitting bookkeeping fields (ID, Timestamp, Applied) that carry no
// value to API consumers. buildFairValueResponse maps entities → this type.
//
// @Description One datacleaner normalization adjustment that fired on the inputs
type CleaningAdjustment struct {
	// Rule is the config rule identifier that fired (entities.Adjustment.RuleID),
	// e.g. "goodwill_exclusion", "contingent_liabilities", "right_of_use_assets".
	Rule string `json:"rule" example:"goodwill_exclusion"`
	// Category is the rule family: "asset_quality" | "liability_completeness" |
	// "earnings_normalization".
	Category string `json:"category,omitempty" example:"asset_quality"`
	// Type is the adjustment kind: "exclude" | "writedown" | "valuation_allowance" |
	// "reclassify" | "treat_as_debt" | "probability_weighted" | "flag".
	Type string `json:"type,omitempty" example:"exclude"`
	// FromAccount is the source balance-sheet / income-statement line item.
	FromAccount string `json:"from_account,omitempty" example:"Goodwill"`
	// ToAccount is the destination line item for reclassifications; empty for
	// overlays and pure exclusions.
	ToAccount string `json:"to_account,omitempty" example:"EstimatedLiabilities"`
	// Amount is the signed monetary delta the adjuster applied, in USD.
	Amount float64 `json:"amount,omitempty" example:"1234.5"`
	// Percentage is the proportional change relative to the pre-adjustment
	// value, when the adjuster reports one (Restater family). Omitted otherwise.
	Percentage float64 `json:"percentage,omitempty" example:"12.5"`
	// Reasoning is the human-readable explanation of why the adjuster fired.
	Reasoning string `json:"reasoning,omitempty" example:"Excluded goodwill of $1234.5M from invested capital"`
}

// AppliedOverride is the per-knob payload in the applied_overrides response
// object. It mirrors entities.AppliedOverrideValue but lives in the handlers
// package to keep the transport layer self-contained. buildFairValueResponse
// maps entities → this type.
//
// @Description Per-knob override echo in the applied_overrides response field
type AppliedOverride struct {
	// Value is the resolved scalar that the engine used. Type matches the knob:
	// float64 for rate/multiplier fields, int for year fields, string for method fields.
	Value interface{} `json:"value"`
	// Source is the precedence layer that supplied this value. Always "request" in v1.
	Source string `json:"source"`
}

// BulkFairValueRequest represents the request structure for bulk fair value requests
// @Description Bulk fair value calculation request for multiple tickers
type BulkFairValueRequest struct {
	Tickers          []string `json:"tickers" binding:"required,min=1,max=10" example:"[\"AAPL\",\"MSFT\",\"GOOGL\"]"` // Stock ticker symbols (max 10)
	OverrideBeta     *float64 `json:"override_beta,omitempty" example:"1.2"`                                           // Optional beta override (legacy; prefer options.beta)
	OverrideRiskFree *float64 `json:"override_rf,omitempty" example:"0.045"`                                           // Optional risk-free rate override (legacy; prefer options.risk_free_rate)

	// Options carries structured per-request valuation knob overrides applied to
	// ALL tickers in the batch. When set, it must not duplicate any knob that is
	// also set via the legacy OverrideBeta / OverrideRiskFree fields — the
	// request is rejected 422 with code "INVALID_OVERRIDE" if both are present
	// for the same knob.
	Options *ValuationOverrides `json:"options,omitempty"`
}

// BulkFailure describes why a single ticker failed during bulk valuation.
//
// Knob is populated for INVALID_OVERRIDE failures so programmatic consumers
// can identify exactly which valuation knob triggered the error without
// parsing the human-readable Message. For all other error codes Knob is
// omitted (omitempty) to keep the wire shape backward-compatible.
type BulkFailure struct {
	Ticker    string `json:"ticker"`
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
	// Knob is the name of the override parameter that caused an INVALID_OVERRIDE
	// error. Empty (and omitted) for non-override error codes. Mirrors the
	// context.knob field in the single-ticker 422 ErrorResponse.
	Knob string `json:"knob,omitempty"`
}

// BulkFairValueResponse represents the response for bulk requests.
// When both successes and failures exist, the HTTP status is 207 Multi-Status.
// When all tickers fail, the HTTP status is 422 Unprocessable Entity.
type BulkFairValueResponse struct {
	Results  []FairValueResponse `json:"results"`
	Failures []BulkFailure       `json:"failures,omitempty"`
	Summary  BulkSummary         `json:"summary"`
}

// BulkSummary provides summary statistics for bulk requests
type BulkSummary struct {
	TotalRequested int `json:"total_requested"`
	Successful     int `json:"successful"`
	Failed         int `json:"failed"`
}

// ValuationOverrides is the request-body transport for per-request valuation
// knob overrides. All fields are optional pointers; nil means "use the resolved
// default". JSON names follow the design §5 catalog exactly.
//
// This struct is a transport DTO only — it is projected into params.Overrides via
// projectOverrides before being passed to the domain layer. Wire-format details
// must never reach the valuation service or params package directly.
//
// @Description Per-request valuation knob overrides (all fields optional)
type ValuationOverrides struct {
	// Terminal growth rate — explicit absolute value for g in Gordon Growth Model.
	// Negative values are allowed (real-terms decline). If omitted the engine
	// auto-derives g from historical CAGR, capped by terminal_growth_cap.
	TerminalGrowthRate *float64 `json:"terminal_growth_rate,omitempty" example:"-0.01"`

	// Cap applied during auto-derivation of the terminal growth rate.
	// Defaults to 0.03 (3%). Has no effect when terminal_growth_rate is set explicitly.
	TerminalGrowthCap *float64 `json:"terminal_growth_cap,omitempty" example:"0.03"`

	// DCF forecast horizon in years. When omitted the engine uses the length of
	// the growth-rate slice produced by the estimator (legacy signal).
	HorizonYears *int `json:"horizon_years,omitempty" example:"5"`

	// GrowthStages overrides the three-stage growth estimator configuration.
	// When any sub-field is set a per-request estimator is built instead of
	// reusing the shared service-level estimator.
	GrowthStages *GrowthStages `json:"growth_stages,omitempty"`

	// MaxGrowthRate is the upper clamp applied inside the growth estimator.
	// Defaults to 0.5 (50%). Negative values are meaningless here but accepted.
	MaxGrowthRate *float64 `json:"max_growth_rate,omitempty" example:"0.5"`

	// MinGrowthRate is the lower clamp applied inside the growth estimator.
	// Defaults to -0.3 (-30%). Negative values represent contraction scenarios.
	MinGrowthRate *float64 `json:"min_growth_rate,omitempty" example:"-0.3"`

	// TerminalMethod selects the terminal-value calculation model.
	// Allowed values: "gordon_growth" (default) | "exit_multiple".
	//   - "gordon_growth" SUPPRESSES exit-multiple blending: the terminal value is a
	//     pure Gordon Growth perpetuity (no exit multiple is mixed in).
	//   - "exit_multiple" BLENDS an exit-multiple terminal value 50/50 with the Gordon
	//     Growth terminal value (the engine AVERAGES the two estimates to reduce
	//     single-model dependence; it is NOT a pure exit-multiple terminal value).
	//     It requires that a multiple is resolvable (via terminal_multiple or the
	//     industry default); if neither is available the request is rejected 422.
	TerminalMethod *string `json:"terminal_method,omitempty" example:"exit_multiple"`

	// TerminalMultiple is the EV/EBITDA (or analogous) exit multiple used when
	// terminal_method is "exit_multiple". When omitted the engine falls back to
	// the industry default looked up at resolution time.
	TerminalMultiple *float64 `json:"terminal_multiple,omitempty" example:"14.0"`

	// TaxRate is the effective corporate tax rate applied to FCF, WACC, and the
	// alt-model ModelInput. Negative values are allowed (NOLs / tax credits).
	TaxRate *float64 `json:"tax_rate,omitempty" example:"0.21"`

	// Beta is the equity beta used in the CAPM cost-of-equity calculation.
	// Negative values are allowed (inverse-correlated assets).
	// Conflicts with the legacy override_beta field on the bulk request.
	Beta *float64 `json:"beta,omitempty" example:"1.2"`

	// RiskFreeRate is the nominal risk-free rate (e.g. 10-year Treasury yield).
	// Negative values are allowed (EUR/JPY/CHF regimes).
	// Conflicts with the legacy override_rf field on the bulk request and the
	// override_rf query parameter on the GET single-ticker endpoint.
	RiskFreeRate *float64 `json:"risk_free_rate,omitempty" example:"0.045"`

	// MarketRiskPremium is the equity risk premium (ERP) added to the risk-free
	// rate in CAPM. Must be ≥ 0; a negative ERP is economically unsound and
	// will be caught by Layer-1 validation (T7).
	MarketRiskPremium *float64 `json:"market_risk_premium,omitempty" example:"0.05"`
}

// GrowthStages carries the three-stage growth-estimator duration configuration.
// All fields optional; nil means "keep the engine default for that stage".
//
// Stage durations are in years:
//   - Stage1Years: high-growth phase (default 3)
//   - Stage2Years: fade/transition phase (default 4)
//   - Stage3Years: long-tail extension (default 0 — legacy 7-year horizon)
//
// @Description Three-stage growth estimator duration overrides
type GrowthStages struct {
	Stage1Years *int `json:"stage1_years,omitempty" example:"3"`
	Stage2Years *int `json:"stage2_years,omitempty" example:"4"`
	Stage3Years *int `json:"stage3_years,omitempty" example:"0"`
}

// SingleFairValueRequest is the POST body for the single-ticker fair-value
// endpoint (POST /api/v1/fair-value/{ticker}). An empty or omitted `options`
// block produces a response byte-identical to the GET endpoint.
//
// @Description POST body for single-ticker fair-value calculation with optional overrides
type SingleFairValueRequest struct {
	// Options carries per-request valuation knob overrides. Nil or absent means
	// "use engine defaults" — identical to calling the GET endpoint.
	Options *ValuationOverrides `json:"options,omitempty"`
}

// ErrorResponse represents an error response structure
// @Description Standard error response following RFC 7807 Problem Details
type ErrorResponse struct {
	Type      string                 `json:"type" example:"https://problems.midas.dev/INVALID_TICKER"` // Problem type URI
	Title     string                 `json:"title" example:"Bad Request"`                              // Human-readable title
	Status    int                    `json:"status" example:"400"`                                     // HTTP status code
	Detail    string                 `json:"detail" example:"Invalid ticker format"`                   // Human-readable explanation
	Instance  string                 `json:"instance" example:"/api/v1/fair-value/INVALID"`            // URI reference to specific occurrence
	Context   map[string]interface{} `json:"context,omitempty"`                                        // Additional context information
	Code      string                 `json:"code,omitempty" example:"INVALID_TICKER"`                  // Error code (RFC 7807 extension)
	Timestamp string                 `json:"timestamp,omitempty"`                                      // ISO 8601 timestamp (RFC 7807 extension)
	Method    string                 `json:"method,omitempty" example:"GET"`                           // HTTP method (RFC 7807 extension)
}

// GetFairValue handles GET /api/v1/fair-value/:ticker requests
// @Summary      Get fair value for a stock
// @Description  Calculate intrinsic fair value for a stock using DCF and net tangible assets
// @Tags         fair-value
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Param        ticker         path     string   true  "Stock ticker symbol (e.g., AAPL)"
// @Param        override_beta  query    number   false "Override beta for WACC calculation" minimum(-5) maximum(5)
// @Param        override_rf    query    number   false "Override risk-free rate" minimum(-0.05) maximum(0.25)
// @Success      200  {object}  FairValueResponse
// @Failure      400  {object}  ErrorResponse "Invalid ticker or parameters"
// @Failure      401  {object}  ErrorResponse "Missing or invalid API key"
// @Failure      403  {object}  ErrorResponse "Insufficient permissions"
// @Failure      404  {object}  ErrorResponse "Ticker not found"
// @Failure      429  {object}  ErrorResponse "Rate limit exceeded"
// @Failure      500  {object}  ErrorResponse "Internal server error"
// @Router       /fair-value/{ticker} [get]
func (h *FairValueHandler) GetFairValue(c *gin.Context) {
	ticker := strings.ToUpper(c.Param("ticker"))

	// Validate ticker format
	if !isValidTicker(ticker) {
		h.sendError(c, http.StatusBadRequest, "INVALID_TICKER",
			"Invalid ticker format",
			"Ticker must be 1-5 alphanumeric characters",
			map[string]interface{}{"ticker": ticker})
		return
	}

	// Stamp the ticker on the request-scoped narrate emitter so every
	// downstream Emit call (auth.resolved already fired with no ticker;
	// everything from here on does) carries it as a standard field. Also
	// stamp the bundle so its manifest reflects the parsed ticker.
	em := narrate.From(c.Request.Context())
	em.WithTicker(ticker)
	if b := artifact.From(c.Request.Context()); b != nil {
		b.SetTicker(ticker)
	}

	// Parse and validate query parameters.
	// parseFloatParam rejects non-finite values (NaN/±Inf) with a descriptive
	// error; a present-but-invalid param yields 400, absent params → nil.
	overrideBeta, err := parseFloatParam(c, "override_beta")
	if err != nil {
		h.sendError(c, http.StatusBadRequest, "INVALID_PARAMETER",
			"Invalid override_beta",
			err.Error(),
			map[string]interface{}{"override_beta": c.Query("override_beta")})
		return
	}
	overrideRF, err := parseFloatParam(c, "override_rf")
	if err != nil {
		h.sendError(c, http.StatusBadRequest, "INVALID_PARAMETER",
			"Invalid override_rf",
			err.Error(),
			map[string]interface{}{"override_rf": c.Query("override_rf")})
		return
	}

	// Validate override ranges (defense in depth).
	// Bounds are widened to match the structured `options` block (betaMin/betaMax,
	// riskFreeRateMin/riskFreeRateMax from fair_value_validation.go) so both
	// entry points accept the same economic range. Legacy scalar checks and the
	// structured-options validator must always agree.
	if overrideBeta != nil && (*overrideBeta < betaMin || *overrideBeta > betaMax) {
		h.sendError(c, http.StatusBadRequest, "INVALID_PARAMETER",
			"Invalid override_beta",
			fmt.Sprintf("Beta override must be between %.4g and %.4g", betaMin, betaMax),
			map[string]interface{}{"override_beta": *overrideBeta})
		return
	}
	if overrideRF != nil && (*overrideRF < riskFreeRateMin || *overrideRF > riskFreeRateMax) {
		h.sendError(c, http.StatusBadRequest, "INVALID_PARAMETER",
			"Invalid override_rf",
			fmt.Sprintf("Risk-free rate override must be between %.4g and %.4g", riskFreeRateMin, riskFreeRateMax),
			map[string]interface{}{"override_rf": *overrideRF})
		return
	}

	logctx.From(c.Request.Context()).Info("Processing fair value request",
		zap.String("ticker", ticker),
		zap.Float64p("override_beta", overrideBeta),
		zap.Float64p("override_rf", overrideRF))

	// Build valuation options from query parameter overrides
	var opts *valuation.ValuationOptions
	if overrideBeta != nil || overrideRF != nil {
		opts = &valuation.ValuationOptions{
			OverrideBeta:     overrideBeta,
			OverrideRiskFree: overrideRF,
		}
	}

	// Tier-1 narrate: handler.entry. The "options" field reports which
	// overrides the user supplied so the per-request story shows whether
	// this was a default-parameter call or an ad-hoc tweak.
	overridesApplied := []string{}
	if overrideBeta != nil {
		overridesApplied = append(overridesApplied, "beta")
	}
	if overrideRF != nil {
		overridesApplied = append(overridesApplied, "rf")
	}
	em.Emit(c.Request.Context(), narrate.PhaseHandlerEntry, narrate.OutcomeOK, "",
		zap.Strings("options", overridesApplied),
	)

	// Tier-3 artifact bundle: snapshot the parsed handler input so the
	// bundle pins exactly what overrides this request used.
	if b := artifact.From(c.Request.Context()); b != nil {
		b.Snapshot(c.Request.Context(), "handler.entry", "02-handler-options.json", map[string]any{
			"ticker":        ticker,
			"override_beta": overrideBeta,
			"override_rf":   overrideRF,
		})
	}

	// Calculate valuation
	result, err := h.valuationService.CalculateValuation(c.Request.Context(), ticker, opts)
	if err != nil {
		h.respondValuationError(c, em, ticker, err)
		return
	}

	h.respondFairValueSuccess(c, em, ticker, result, "Fair value calculation completed")
}

// respondValuationError classifies a CalculateValuation failure and writes the
// matching error response. Shared verbatim by GetFairValue and PostFairValue
// (SR-1 A8) so the sentinel ladder, its ordering, and the wire messages cannot
// drift between the two endpoints.
//
// Layer-2 cross-knob invariants (T8): params.ParamError surfaces when the
// resolver finds terminal_growth >= WACC, min > max, stage-sum < 1, horizon
// out of range, or exit-multiple unresolvable AFTER Layer-1 static ranges
// passed (T7). These are caller errors — map to 422 INVALID_OVERRIDE. Checked
// first so a specific invariant message is never masked by the generic
// TICKER_NOT_FOUND / CALCULATION_ERROR fallthrough. (On GET the branch is
// defensive — the legacy beta/rf query overrides have no Layer-2 cross-knob
// invariants today.)
func (h *FairValueHandler) respondValuationError(c *gin.Context, em *narrate.Emitter, ticker string, err error) {
	// Tier-1 narrate: valuation.computed with outcome=error so the per-
	// request story shows the failure even when the engine returns
	// before any of the lower-level emissions could fire.
	em.Emit(c.Request.Context(), narrate.PhaseValuationComputed, narrate.OutcomeError, err.Error())

	logctx.From(c.Request.Context()).Error("Valuation calculation failed",
		zap.String("ticker", ticker),
		zap.Error(err))

	if errResp, ok := paramErrorResponse(err); ok {
		errResp.Instance = c.Request.URL.Path
		errResp.Timestamp = time.Now().UTC().Format(time.RFC3339)
		errResp.Method = c.Request.Method
		c.Header("Content-Type", "application/problem+json")
		c.JSON(http.StatusUnprocessableEntity, errResp)
		c.Abort()
		return
	}

	// FPI MUST be checked before ErrInsufficientData — both produce 422
	// but FPI carries a more specific code/message that helps users
	// understand we have data, just in a taxonomy we don't yet parse.
	if errors.Is(err, valuation.ErrTickerNotFound) {
		h.sendError(c, http.StatusNotFound, "TICKER_NOT_FOUND",
			"Ticker not found",
			"The specified ticker could not be found in our database",
			map[string]interface{}{"ticker": ticker})
	} else if errors.Is(err, valuation.ErrForeignPrivateIssuer) {
		h.sendError(c, http.StatusUnprocessableEntity, "FOREIGN_PRIVATE_ISSUER_UNSUPPORTED",
			"Foreign private issuer not covered",
			"This ticker files using a taxonomy or currency pair Midas does not yet cover. Supported: ifrs-full taxonomy with FRED-tracked currencies (TWD, EUR, JPY, GBP, HKD, CNY, KRW, CHF, CAD, AUD, INR, BRL, DKK). Out-of-coverage taxonomies (JGAAP, K-IFRS, ifrs-smes) and currencies are tracked in docs/refactoring/archive/ifrs-foreign-private-issuer-support-spec.md.",
			map[string]interface{}{
				"ticker":      ticker,
				"filing_type": "20-F",
				"taxonomy":    "ifrs-full",
			})
	} else if errors.Is(err, valuation.ErrInsufficientData) {
		h.sendError(c, http.StatusUnprocessableEntity, "INSUFFICIENT_DATA",
			"Insufficient data for valuation",
			"Not enough financial data available to perform reliable valuation",
			map[string]interface{}{"ticker": ticker})
	} else if errors.Is(err, valuation.ErrModelNotApplicable) {
		h.sendError(c, http.StatusUnprocessableEntity, "MODEL_NOT_APPLICABLE",
			"Standard DCF model not applicable",
			"Standard DCF requires positive operating income and alternative models (DDM, FFO, revenue multiples) could not produce a result for this company.",
			map[string]interface{}{"ticker": ticker})
	} else {
		h.sendError(c, http.StatusInternalServerError, "CALCULATION_ERROR",
			"Valuation calculation failed",
			"An internal error occurred during valuation calculation",
			map[string]interface{}{"ticker": ticker})
	}
}

// respondFairValueSuccess builds the response via buildFairValueResponse and
// writes the success-path tail (narrate line, artifact snapshot, completion
// log, 200 body) shared by GetFairValue and PostFairValue (SR-1 A8). logMsg
// preserves each endpoint's distinct completion message ("Fair value
// calculation completed" vs "POST fair value calculation completed") that
// operators grep for.
func (h *FairValueHandler) respondFairValueSuccess(c *gin.Context, em *narrate.Emitter, ticker string, result *entities.ValuationResult, logMsg string) {
	response := h.buildFairValueResponse(ticker, result)

	// Tier-1 narrate: valuation.computed success line. Carries the headline
	// numbers so the per-request story ends with the actual fair-value output.
	em.Emit(c.Request.Context(), narrate.PhaseValuationComputed, narrate.OutcomeOK, "",
		zap.String("model", result.CalculationMethod),
		zap.Float64("fair_value_per_share", result.DCFValuePerShare),
		zap.Float64("tangible_value_per_share", result.TangibleValuePerShare),
	)

	// Tier-3 artifact bundle: snapshot the final response body. This is the
	// canonical "what we sent back to the client" record — invaluable when
	// a downstream consumer reports an unexpected number weeks later.
	if b := artifact.From(c.Request.Context()); b != nil {
		b.Snapshot(c.Request.Context(), "response.sent", "17-response.json", &response)
		b.AddSchemaVersion("FairValueResponse", 1)
	}

	logctx.From(c.Request.Context()).Info(logMsg,
		zap.String("ticker", ticker),
		zap.Float64("dcf_value", result.DCFValuePerShare),
		zap.Float64("tangible_value", result.TangibleValuePerShare))

	c.JSON(http.StatusOK, response)
}

// buildFairValueResponse constructs a FairValueResponse from a validated result.
// This is the single place where ValuationResult fields are mapped to the API
// response shape, shared by GetFairValue (GET) and PostFairValue (POST) to
// guarantee byte-identical output for the same service result.
//
// T10: applied_overrides is populated by copying result.AppliedOverrides
// (entities.AppliedOverrideValue map) into the handler-layer AppliedOverride map.
// Both are nil/omitempty when no overrides were applied, preserving byte-identity
// for default GET responses and POST{} requests.
func (h *FairValueHandler) buildFairValueResponse(ticker string, result *entities.ValuationResult) FairValueResponse {
	// Copy the applied overrides from the service-layer entity carrier to the
	// handler-layer response type. The two structs are identical in shape but
	// defined in separate packages to respect the import boundary. Nil when the
	// result carries no overrides (default path) — omitempty drops the field.
	var appliedOverrides map[string]AppliedOverride
	if len(result.AppliedOverrides) > 0 {
		appliedOverrides = make(map[string]AppliedOverride, len(result.AppliedOverrides))
		for k, v := range result.AppliedOverrides {
			appliedOverrides[k] = AppliedOverride{
				Value:  v.Value,
				Source: v.Source,
			}
		}
	}

	// Project the cleaner audit trail (fired-only — adjustmentsFromLedger emits
	// just the adjusters that fired) onto the transport DTO. Nil when no adjuster
	// fired; omitempty then drops cleaning_adjustments, keeping default responses
	// byte-identical to the pre-TDB-11 shape.
	cleaningAdjustments := buildCleaningAdjustments(result.CleaningAdjustments)

	return FairValueResponse{
		Ticker:                ticker,
		WACC:                  result.WACC,
		GrowthRate:            result.GrowthRate,
		GrowthRates:           result.GrowthRates,
		GrowthSource:          result.GrowthSource,
		GrowthConfidence:      result.GrowthConfidence,
		TangibleValuePerShare: result.TangibleValuePerShare,
		DCFValuePerShare:      result.DCFValuePerShare,
		PFFOValuePerShare:     result.PFFOValuePerShare,  // VAL-3 Phase 2 (omitempty — REIT only)
		PAFFOValuePerShare:    result.PAFFOValuePerShare, // VAL-3 Phase 2 (omitempty — REIT only)
		CurrentAssetsPerShare: result.CurrentAssetsPerShare,
		NCAVPerShare:          result.NCAVPerShare,
		GrahamFloorPerShare:   result.GrahamFloorPerShare,
		GrahamDiscountPct:     result.GrahamDiscountPct,
		AsOf:                  result.CalculatedAt.Format("2006-01-02T15:04:05Z"),
		DataQualityScore:      result.DataQualityScore,
		DataQualityGrade:      string(result.DataQualityGrade),
		CalculationMethod:     result.CalculationMethod,
		CalculationVersion:    result.CalculationVersion,
		Warnings:              result.Warnings,
		SanityCheck:           result.SanityCheck,
		Industry:              BuildIndustryFromResult(result),
		// Phase B12 (IFRS-FPI): always-present transparency fields. Currency
		// falls back to "USD" if an upstream code path forgot to stamp it
		// (defense in depth — the valuation service guarantees "USD" today).
		Currency:        currencyOrUSD(result.ReportingCurrency),
		ADRRatioApplied: result.ADRRatioApplied,
		CurrentPrice:    result.CurrentPrice,
		// Tier 2 P0b: copy profile + DCF diagnostics. All omitempty —
		// fields default-zero when the profileRegistry isn't wired (test
		// paths) or until P2 populates DCF diagnostics. Legacy DDM responses
		// stay byte-identical because the resolved profile's
		// DividendForecastHorizon==0 keeps DDM on the legacy single-stage
		// branch, but AssumptionProfile is still surfaced for auditability.
		AssumptionProfile:     result.AssumptionProfile,
		ResolutionTrace:       result.ResolutionTrace,
		DCFHorizonYears:       result.DCFHorizonYears,
		DCFTerminalMethod:     result.DCFTerminalMethod,
		DCFTerminalPctOfEV:    result.DCFTerminalPctOfEV,
		DCFPerYearPV:          result.DCFPerYearPV,
		DCFTerminalGrowthUsed: result.DCFTerminalGrowthUsed,
		DCFBaseNormalization:  result.DCFBaseNormalization,
		// T10: copy applied_overrides from the service-layer entity carrier.
		// Nil when result carries no overrides (default path); omitempty drops it.
		AppliedOverrides: appliedOverrides,
		// Layer-B Phase-2: copy per-assumption source provenance. Nil/empty on the
		// default (no-guidance) path; omitempty drops it ⇒ byte-identical responses.
		AssumptionSources: result.AssumptionSources,
		// TDB-11: surface the datacleaner audit trail. Nil when no adjuster fired;
		// omitempty then drops cleaning_adjustments from the wire.
		CleaningAdjustments: cleaningAdjustments,
	}
}

// buildCleaningAdjustments projects the cleaner's internal audit entries
// (entities.Adjustment) onto the transport-layer CleaningAdjustment DTO. The
// input is already fired-only (adjustmentsFromLedger emits only adjusters that
// fired with Applied==true), so this is a straight field projection. Returns
// nil for an empty/absent trail so the omitempty-tagged response field
// disappears entirely, preserving byte-identity with pre-TDB-11 captures.
func buildCleaningAdjustments(adjustments []entities.Adjustment) []CleaningAdjustment {
	if len(adjustments) == 0 {
		return nil
	}
	out := make([]CleaningAdjustment, 0, len(adjustments))
	for _, a := range adjustments {
		out = append(out, CleaningAdjustment{
			Rule:        a.RuleID,
			Category:    string(a.Category),
			Type:        string(a.Type),
			FromAccount: a.FromAccount,
			ToAccount:   a.ToAccount,
			Amount:      a.Amount,
			Percentage:  a.Percentage,
			Reasoning:   a.Reasoning,
		})
	}
	return out
}

// PostFairValue handles POST /api/v1/fair-value/:ticker requests.
//
// The POST form accepts an optional JSON body with an `options` block of
// per-request valuation knob overrides. An empty body (or `{}`) produces a
// response byte-identical to GET /api/v1/fair-value/:ticker for the same
// ticker. The body is completely optional — callers that have no overrides
// MAY use GET instead; POST exists so override knobs can be passed without
// stuffing them into query parameters.
//
// Error ordering mirrors GetFairValue:
//  1. Ticker validation (400)
//  2. Layer-1 override validation via validateOverrides (422 INVALID_OVERRIDE)
//  3. Service call + Layer-2 ParamError (422 INVALID_OVERRIDE)
//  4. Sentinel errors: FPI → INSUFFICIENT_DATA → MODEL_NOT_APPLICABLE → 404 → 500
//
// @Summary      Post fair value for a stock with optional overrides
// @Description  Calculate intrinsic fair value for a stock; optional JSON body accepts per-request valuation knob overrides
// @Tags         fair-value
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Param        ticker   path     string              true  "Stock ticker symbol (e.g., AAPL)"
// @Param        request  body     SingleFairValueRequest  false "Optional override options"
// @Success      200  {object}  FairValueResponse
// @Failure      400  {object}  ErrorResponse "Invalid ticker or parameters"
// @Failure      401  {object}  ErrorResponse "Missing or invalid API key"
// @Failure      403  {object}  ErrorResponse "Insufficient permissions"
// @Failure      404  {object}  ErrorResponse "Ticker not found"
// @Failure      422  {object}  ErrorResponse "Invalid override knob value"
// @Failure      429  {object}  ErrorResponse "Rate limit exceeded"
// @Failure      500  {object}  ErrorResponse "Internal server error"
// @Router       /fair-value/{ticker} [post]
func (h *FairValueHandler) PostFairValue(c *gin.Context) {
	ticker := strings.ToUpper(c.Param("ticker"))

	// Validate ticker format — same guard as GET.
	if !isValidTicker(ticker) {
		h.sendError(c, http.StatusBadRequest, "INVALID_TICKER",
			"Invalid ticker format",
			"Ticker must be 1-5 alphanumeric characters",
			map[string]interface{}{"ticker": ticker})
		return
	}

	// Stamp ticker on the observability handles, same as GET.
	em := narrate.From(c.Request.Context())
	em.WithTicker(ticker)
	if b := artifact.From(c.Request.Context()); b != nil {
		b.SetTicker(ticker)
	}

	// Bind the optional request body. An absent or empty body is explicitly
	// valid — the zero-value req (Options == nil) is the "no overrides" case
	// and produces a response byte-identical to GET.
	//
	// ShouldBindJSON returns io.EOF when the request body is completely empty.
	// We treat that as "no body supplied" rather than an error so that callers
	// can POST with no body (or Content-Type absent) and get GET semantics.
	var req SingleFairValueRequest
	if err := c.ShouldBindJSON(&req); err != nil && !isEmptyBodyError(err) {
		h.sendError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"Invalid request format",
			"Request body does not match expected format",
			map[string]interface{}{"validation_error": err.Error()})
		return
	}

	// Layer-1 static validation: per-knob range + enum checks.
	// Returns 422 INVALID_OVERRIDE on the first out-of-range knob.
	if errResp := validateOverrides(req.Options); errResp != nil {
		errResp.Instance = c.Request.URL.Path
		errResp.Timestamp = time.Now().UTC().Format(time.RFC3339)
		errResp.Method = c.Request.Method
		c.Header("Content-Type", "application/problem+json")
		c.JSON(errResp.Status, errResp)
		c.Abort()
		return
	}

	// Project the DTO into domain-layer Overrides. Only allocate opts when at
	// least one override is present — nil *ValuationOptions signals "no overrides"
	// to the cache layer, preserving the same cache-eligibility as a plain GET.
	var opts *valuation.ValuationOptions
	if req.Options != nil && anyBulkOverride(nil, nil, req.Options) {
		projected := projectOverrides(req.Options)
		opts = &valuation.ValuationOptions{
			Overrides: projected,
		}
	}

	// Tier-1 narrate: handler.entry — mirrors GET for observability parity.
	em.Emit(c.Request.Context(), narrate.PhaseHandlerEntry, narrate.OutcomeOK, "",
		zap.Bool("has_overrides", opts != nil),
	)

	// Tier-3 artifact bundle: snapshot the parsed handler input.
	if b := artifact.From(c.Request.Context()); b != nil {
		b.Snapshot(c.Request.Context(), "handler.entry", "02-handler-options.json", map[string]any{
			"ticker":  ticker,
			"options": req.Options,
		})
	}

	logctx.From(c.Request.Context()).Info("Processing POST fair value request",
		zap.String("ticker", ticker),
		zap.Bool("has_overrides", opts != nil))

	// Calculate valuation. Error classification + the success tail are shared
	// verbatim with GET via respondValuationError / respondFairValueSuccess
	// (SR-1 A8) — POST{} == GET byte-identity rides on that sharing.
	result, err := h.valuationService.CalculateValuation(c.Request.Context(), ticker, opts)
	if err != nil {
		h.respondValuationError(c, em, ticker, err)
		return
	}

	h.respondFairValueSuccess(c, em, ticker, result, "POST fair value calculation completed")
}

// GetBulkFairValue handles POST /api/v1/fair-value/bulk requests
// @Summary      Get fair values for multiple stocks
// @Description  Calculate intrinsic fair values for multiple stocks in a single request
// @Tags         fair-value
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Param        request  body     BulkFairValueRequest  true  "Bulk fair value request"
// @Success      200  {object}  BulkFairValueResponse
// @Failure      400  {object}  ErrorResponse "Invalid request format"
// @Failure      401  {object}  ErrorResponse "Missing or invalid API key"
// @Failure      403  {object}  ErrorResponse "Insufficient permissions"
// @Failure      429  {object}  ErrorResponse "Rate limit exceeded"
// @Failure      500  {object}  ErrorResponse "Internal server error"
// @Router       /fair-value/bulk [post]
func (h *FairValueHandler) GetBulkFairValue(c *gin.Context) {
	var request BulkFairValueRequest

	if err := c.ShouldBindJSON(&request); err != nil {
		h.sendError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"Invalid request format",
			"Request body does not match expected format",
			map[string]interface{}{"validation_error": err.Error()})
		return
	}

	// Bulk requests do not have a URL :ticker param, but they still carry
	// useful ticker context in the body. Stamp a stable pseudo-ticker so
	// always/on-error/on-quality-flag artifact bundles do not promote under
	// the generic _no-ticker partition.
	if subject := bulkArtifactSubject(request.Tickers); subject != "" {
		narrate.From(c.Request.Context()).WithTicker(subject)
		if b := artifact.From(c.Request.Context()); b != nil {
			b.SetTicker(subject)
		}
	}

	// Validate override ranges (same bounds as single endpoint).
	// Widened to match betaMin/betaMax and riskFreeRateMin/riskFreeRateMax from
	// fair_value_validation.go so legacy and structured-options paths are consistent.
	if request.OverrideBeta != nil && (*request.OverrideBeta < betaMin || *request.OverrideBeta > betaMax) {
		h.sendError(c, http.StatusBadRequest, "INVALID_PARAMETER",
			"Invalid override_beta",
			fmt.Sprintf("Beta override must be between %.4g and %.4g", betaMin, betaMax),
			map[string]interface{}{"override_beta": *request.OverrideBeta})
		return
	}
	if request.OverrideRiskFree != nil && (*request.OverrideRiskFree < riskFreeRateMin || *request.OverrideRiskFree > riskFreeRateMax) {
		h.sendError(c, http.StatusBadRequest, "INVALID_PARAMETER",
			"Invalid override_rf",
			fmt.Sprintf("Risk-free rate override must be between %.4g and %.4g", riskFreeRateMin, riskFreeRateMax),
			map[string]interface{}{"override_rf": *request.OverrideRiskFree})
		return
	}

	// Detect conflicts between legacy top-level fields and the structured options
	// block. A conflict means the same knob is supplied twice with potentially
	// different values — we reject rather than silently resolve (design §4.1/F4).
	if conflicts := detectOverrideConflicts(request.OverrideBeta, request.OverrideRiskFree, request.Options); len(conflicts) > 0 {
		// Report the first conflict; the typed Knob field lets us name the
		// conflicting parameter without string parsing (I-1 carry-forward).
		h.sendError(c, http.StatusUnprocessableEntity, "INVALID_OVERRIDE",
			"Invalid valuation override",
			conflicts[0].Message,
			map[string]interface{}{"knob": conflicts[0].Knob})
		return
	}

	// Layer-1 static validation: per-knob range + enum checks before any work.
	// Returns 422 INVALID_OVERRIDE on the first out-of-range knob. Cross-knob
	// invariants (terminal < WACC, min ≤ max, horizon ≤ stage-sum) are Layer 2
	// (T8, resolver) and are NOT checked here.
	if errResp := validateOverrides(request.Options); errResp != nil {
		errResp.Instance = c.Request.URL.Path
		errResp.Timestamp = time.Now().UTC().Format(time.RFC3339)
		errResp.Method = c.Request.Method
		c.Header("Content-Type", "application/problem+json")
		c.JSON(errResp.Status, errResp)
		c.Abort()
		return
	}

	// Project the structured options DTO into the domain-layer Overrides once,
	// at the request level. The same projected overrides apply to every ticker.
	// projectOverrides is nil-safe: if request.Options is nil, projectedOverrides
	// is a zero params.Overrides (all nil pointers) — equivalent to no override.
	projectedOverrides := projectOverrides(request.Options)

	logctx.From(c.Request.Context()).Info("Processing bulk fair value request",
		zap.Int("ticker_count", len(request.Tickers)),
		zap.Strings("tickers", request.Tickers))

	results := make([]FairValueResponse, 0, len(request.Tickers))
	failures := make([]BulkFailure, 0)
	successful := 0
	failed := 0

	// Process each ticker
	for _, ticker := range request.Tickers {
		ticker = strings.ToUpper(ticker)

		// Validate ticker format
		if !isValidTicker(ticker) {
			logctx.From(c.Request.Context()).Warn("Skipping invalid ticker in bulk request", zap.String("ticker", ticker))
			failures = append(failures, BulkFailure{
				Ticker:    ticker,
				ErrorCode: "INVALID_TICKER",
				Message:   "Invalid ticker format: must be 1-5 alphanumeric characters",
			})
			failed++
			continue
		}

		// Build valuation options from legacy bulk request fields plus the
		// structured options block. Legacy fields still work for backward
		// compatibility; the structured Overrides carries the new knobs.
		//
		// Only allocate opts when at least one override is actually present —
		// a nil *ValuationOptions signals "no overrides" to the cache layer and
		// the service. anyBulkOverride checks the same conditions as
		// ValuationOptions.hasAnyOverride (package-private) but at the handler
		// layer so we don't cross the package boundary.
		var opts *valuation.ValuationOptions
		if anyBulkOverride(request.OverrideBeta, request.OverrideRiskFree, request.Options) {
			opts = &valuation.ValuationOptions{
				OverrideBeta:     request.OverrideBeta,
				OverrideRiskFree: request.OverrideRiskFree,
				Overrides:        projectedOverrides,
			}
		}

		// Calculate valuation for this ticker
		result, err := h.valuationService.CalculateValuation(c.Request.Context(), ticker, opts)
		if err != nil {
			logctx.From(c.Request.Context()).Warn("Valuation failed for ticker in bulk request",
				zap.String("ticker", ticker),
				zap.Error(err))

			// Classify the error using sentinel types for per-ticker failure detail
			failure := classifyBulkError(ticker, err)
			failures = append(failures, failure)
			failed++
			continue
		}

		// Add to results — use the shared builder to guarantee parity with the
		// single-ticker endpoints (GET + POST).
		results = append(results, h.buildFairValueResponse(ticker, result))
		successful++
	}

	// Create bulk response with failure details
	bulkResponse := BulkFairValueResponse{
		Results:  results,
		Failures: failures,
		Summary: BulkSummary{
			TotalRequested: len(request.Tickers),
			Successful:     successful,
			Failed:         failed,
		},
	}

	logctx.From(c.Request.Context()).Info("Bulk fair value calculation completed",
		zap.Int("successful", successful),
		zap.Int("failed", failed))

	// Choose HTTP status based on outcome:
	// - 200 OK: all tickers succeeded
	// - 207 Multi-Status: partial success (some succeeded, some failed)
	// - 422 Unprocessable Entity: all tickers failed
	switch {
	case failed == 0:
		c.JSON(http.StatusOK, bulkResponse)
	case successful == 0:
		c.JSON(http.StatusUnprocessableEntity, bulkResponse)
	default:
		c.JSON(http.StatusMultiStatus, bulkResponse)
	}
}

// classifyBulkError maps a valuation service error to a BulkFailure with
// an appropriate error code and human-readable message.
//
// Layer-2 cross-knob invariant violations (params.ParamError) are checked first
// so that a per-ticker bad override is surfaced as INVALID_OVERRIDE rather than
// CALCULATION_ERROR, preserving failure isolation: one ticker's invariant breach
// does not affect the rest of the batch.
func classifyBulkError(ticker string, err error) BulkFailure {
	// Layer-2 cross-knob invariants (T8): terminal_growth >= WACC, min > max,
	// stage-sum < 1, horizon out of range, exit-multiple unresolvable. These are
	// caller errors — map to INVALID_OVERRIDE with the offending knob name so
	// the consumer can fix the request without contacting the operator.
	// I-2: populate Knob from pe.Knob so bulk INVALID_OVERRIDE entries carry the
	// same machine-readable knob fidelity as the single-ticker context.knob field.
	var pe *params.ParamError
	if errors.As(err, &pe) {
		return BulkFailure{
			Ticker:    ticker,
			ErrorCode: "INVALID_OVERRIDE",
			Message:   pe.Error(),
			Knob:      pe.Knob,
		}
	}

	switch {
	case errors.Is(err, valuation.ErrTickerNotFound):
		return BulkFailure{
			Ticker:    ticker,
			ErrorCode: "TICKER_NOT_FOUND",
			Message:   "Ticker not found in any data source",
		}
	case errors.Is(err, valuation.ErrForeignPrivateIssuer):
		// Must be checked before ErrInsufficientData (more specific case).
		return BulkFailure{
			Ticker:    ticker,
			ErrorCode: "FOREIGN_PRIVATE_ISSUER_UNSUPPORTED",
			Message:   "Foreign private issuer with taxonomy or currency outside Midas coverage",
		}
	case errors.Is(err, valuation.ErrInsufficientData):
		return BulkFailure{
			Ticker:    ticker,
			ErrorCode: "INSUFFICIENT_DATA",
			Message:   "Not enough financial data for reliable valuation",
		}
	case errors.Is(err, valuation.ErrModelNotApplicable):
		return BulkFailure{
			Ticker:    ticker,
			ErrorCode: "MODEL_NOT_APPLICABLE",
			Message:   "Standard DCF not applicable; company has non-positive operating income",
		}
	default:
		return BulkFailure{
			Ticker:    ticker,
			ErrorCode: "CALCULATION_ERROR",
			Message:   "Valuation calculation failed",
		}
	}
}

// paramErrorResponse inspects err for a *params.ParamError and, when found,
// builds the RFC 7807 422 INVALID_OVERRIDE problem-detail body ready for the
// handler to send. Returns (nil, false) when err is not (or does not wrap) a
// *params.ParamError, so callers can fall through to their own error routing.
//
// T9 (POST single-ticker) should call this after its CalculateValuation call
// using the same pattern:
//
//	if errResp, ok := paramErrorResponse(err); ok {
//	    c.Header("Content-Type", "application/problem+json")
//	    c.JSON(http.StatusUnprocessableEntity, errResp)
//	    c.Abort()
//	    return
//	}
func paramErrorResponse(err error) (*ErrorResponse, bool) {
	var pe *params.ParamError
	if !errors.As(err, &pe) {
		return nil, false
	}
	// MEDIUM-3: use pe.Error() (the full "invalid override for <knob> (value=...):
	// <reason> (limit=...)" message) rather than pe.Reason alone, so the
	// single-ticker 422 carries the same value/limit detail the bulk path already
	// surfaces via pe.Error(). The offending value/limit are also exposed in the
	// context object for machine consumers.
	ctx := map[string]interface{}{
		"knob":  pe.Knob,
		"value": pe.Value,
	}
	// Limit is included only when the ParamError carries a meaningful threshold
	// (HasLimit). Gating on HasLimit rather than `Limit != 0` preserves a real
	// zero limit (e.g. min_growth > max_growth with max == 0) that would otherwise
	// be silently dropped, while still omitting it for enum / structural errors.
	if pe.HasLimit {
		ctx["limit"] = pe.Limit
	}
	return &ErrorResponse{
		Type:    "https://problems.midas.dev/INVALID_OVERRIDE",
		Title:   "Invalid valuation override",
		Status:  http.StatusUnprocessableEntity,
		Detail:  pe.Error(),
		Code:    "INVALID_OVERRIDE",
		Context: ctx,
	}, true
}

// ---------------------------------------------------------------------------
// Override helpers (T6)
// ---------------------------------------------------------------------------

// projectOverrides translates the transport DTO (*ValuationOverrides) into the
// domain-layer params.Overrides struct. This is the single point where wire
// names are mapped to domain names, so the domain layer never sees the DTO.
//
// Nil-safe: a nil dto returns a zero params.Overrides (all pointers nil).
// GrowthStages is flattened: dto.GrowthStages.StageNYears → params.StageNYears.
func projectOverrides(o *ValuationOverrides) params.Overrides {
	if o == nil {
		return params.Overrides{}
	}

	out := params.Overrides{
		TerminalGrowthRate: o.TerminalGrowthRate,
		TerminalGrowthCap:  o.TerminalGrowthCap,
		HorizonYears:       o.HorizonYears,
		MaxGrowthRate:      o.MaxGrowthRate,
		MinGrowthRate:      o.MinGrowthRate,
		TerminalMethod:     o.TerminalMethod,
		TerminalMultiple:   o.TerminalMultiple,
		TaxRate:            o.TaxRate,
		Beta:               o.Beta,
		RiskFreeRate:       o.RiskFreeRate,
		MarketRiskPremium:  o.MarketRiskPremium,
	}

	// Flatten GrowthStages sub-struct into the flat Overrides fields.
	if o.GrowthStages != nil {
		out.Stage1Years = o.GrowthStages.Stage1Years
		out.Stage2Years = o.GrowthStages.Stage2Years
		out.Stage3Years = o.GrowthStages.Stage3Years
	}

	return out
}

// anyBulkOverride reports whether a bulk request carries any per-request override
// via the legacy fields or the structured options block. This mirrors the logic
// of valuation.ValuationOptions.hasAnyOverride (which is unexported) but operates
// on the raw request fields so the handler can decide whether to allocate opts
// before crossing the package boundary.
func anyBulkOverride(legacyBeta, legacyRF *float64, o *ValuationOverrides) bool {
	if legacyBeta != nil || legacyRF != nil {
		return true
	}
	if o == nil {
		return false
	}
	return o.Beta != nil ||
		o.RiskFreeRate != nil ||
		o.MarketRiskPremium != nil ||
		o.TaxRate != nil ||
		o.TerminalGrowthRate != nil ||
		o.TerminalGrowthCap != nil ||
		o.HorizonYears != nil ||
		o.MaxGrowthRate != nil ||
		o.MinGrowthRate != nil ||
		o.TerminalMethod != nil ||
		o.TerminalMultiple != nil ||
		(o.GrowthStages != nil && (o.GrowthStages.Stage1Years != nil ||
			o.GrowthStages.Stage2Years != nil ||
			o.GrowthStages.Stage3Years != nil))
}

// overrideConflict describes a single knob that was supplied through BOTH a
// legacy top-level field and the structured options block. Knob is the
// canonical override name (e.g. "beta", "risk_free_rate"); Message is the
// human-readable explanation sent in the 422 Detail field.
type overrideConflict struct {
	// Knob is the machine-readable override name, matching the options JSON key
	// (e.g. "beta", "risk_free_rate"). Callers can use Knob directly instead of
	// parsing Message.
	Knob string
	// Message is the human-readable explanation suitable for the 422 Detail field.
	Message string
}

// detectOverrideConflicts checks whether any knob is supplied BOTH through a
// legacy field (OverrideBeta / OverrideRiskFree) AND through the structured
// options block. Returns one overrideConflict per conflicting knob.
//
// The caller is responsible for deciding what to do with conflicts; for the
// bulk path a non-empty slice means 422.
//
// Design §4.1 / plan §5 T6: "conflict detection" — only beta and risk_free_rate
// have legacy counterparts in the bulk request. MarketRiskPremium and all other
// knobs are available only via options, so they never conflict here.
func detectOverrideConflicts(legacyBeta, legacyRF *float64, o *ValuationOverrides) []overrideConflict {
	if o == nil {
		return nil
	}

	var conflicts []overrideConflict

	if legacyBeta != nil && o.Beta != nil {
		conflicts = append(conflicts, overrideConflict{
			Knob:    "beta",
			Message: "beta set in both override_beta and options.beta; supply only one",
		})
	}

	if legacyRF != nil && o.RiskFreeRate != nil {
		conflicts = append(conflicts, overrideConflict{
			Knob:    "risk_free_rate",
			Message: "risk_free_rate set in both override_rf and options.risk_free_rate; supply only one",
		})
	}

	return conflicts
}

// Helper functions

// sendError sends an RFC 7807 compliant error response, consistent with
// the server.go respondWithError format (code, timestamp, method fields).
// Uses the ErrorResponse struct (not gin.H) so field additions stay
// compile-checked and the timestamp is explicitly RFC 3339.
func (h *FairValueHandler) sendError(c *gin.Context, status int, errorType, title, detail string, ctx map[string]interface{}) {
	c.Header("Content-Type", "application/problem+json")
	c.JSON(status, ErrorResponse{
		Type:      "https://problems.midas.dev/" + errorType,
		Title:     title,
		Status:    status,
		Detail:    detail,
		Instance:  c.Request.URL.Path,
		Context:   ctx,
		Code:      errorType,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Method:    c.Request.Method,
	})
	c.Abort()
}

// currencyOrUSD returns its argument when non-empty, "USD" otherwise.
// Defense-in-depth helper for the Phase B12 transparency field — the
// valuation service always stamps result.ReportingCurrency = "USD" today,
// but this guarantees the response always carries an ISO-4217 code so
// downstream clients never see the empty string.
func currencyOrUSD(c string) string {
	if c == "" {
		return "USD"
	}
	return c
}

// isEmptyBodyError reports whether err signals an absent or zero-length request
// body rather than a malformed one. Gin's ShouldBindJSON returns io.EOF when
// the body reader is empty, which PostFairValue treats as "no overrides
// supplied" (equivalent to calling GET) rather than a request error.
func isEmptyBodyError(err error) bool {
	return errors.Is(err, io.EOF)
}

// isValidTicker validates ticker format (1-5 alphanumeric characters)
func isValidTicker(ticker string) bool {
	if len(ticker) == 0 || len(ticker) > 5 {
		return false
	}

	for _, char := range ticker {
		// nolint:staticcheck // readability preferred over De Morgan simplification
		if !((char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')) {
			return false
		}
	}

	return true
}

func bulkArtifactSubject(tickers []string) string {
	parts := make([]string, 0, len(tickers))
	for _, ticker := range tickers {
		t := strings.ToUpper(strings.TrimSpace(ticker))
		if isValidTicker(t) {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return "BULK_INVALID"
	}
	return "BULK_" + strings.Join(parts, "_")
}

// parseFloatParam safely parses a float query parameter.
//
// Returns:
//   - (nil, nil)          — parameter absent; caller should use the default.
//   - (*float64, nil)     — parameter present and valid.
//   - (nil, non-nil err)  — parameter present but unparseable or non-finite
//     (NaN / ±Inf).  strconv.ParseFloat accepts "NaN", "+Inf", "-Inf" without
//     error, so we must explicitly reject those — a non-finite value silently
//     propagates into WACC/DCF and produces a non-finite response or a 500.
func parseFloatParam(c *gin.Context, param string) (*float64, error) {
	value := c.Query(param)
	if value == "" {
		return nil, nil
	}

	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil, fmt.Errorf("%s must be a finite number", param)
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return nil, fmt.Errorf("%s must be a finite number", param)
	}
	return &parsed, nil
}
