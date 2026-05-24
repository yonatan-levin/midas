# Tier 2 — Unified `AssumptionProfile` Architecture Spec

**Status:** SHIPPED — Tier 2 complete 2026-05-21 across Bootstrap + P0a + P0b + P1 + P2 + P3 + P4 + T2-P4-W1 reconciliation + Closeout docs sweep
**Version:** v0.2 — Tier 2 close 2026-05-21 (v0.1 was initial spec landing 2026-05-14; v0.2 reflects the final post-Tier-2 state — 31 profiles + 19 rules in `config/assumption_profiles.json`, REIT_* / FIN_* classifier emission canonicalized via T2-P4-W1, JPM/BAC/WFC bit-for-bit DDM invariant preserved across all phase merges)
**Spec author session:** Tier 2 brainstorming + gpt-5.5-pro architectural critique
**Companion docs:**
- `docs/refactoring/archive/tier-2-assumption-profile-kickoff.md` (kickoff brief, scope, estimates)
- `docs/refactoring/spec/assumption-profile-db-backed-future.md` (future DB-backed `Registry` follow-up)
- `docs/reviewer/RM-3-forward-revenue-multiple-model.md`
- `docs/reviewer/VAL-1-dcf-model-archetype-aware-horizon-and-normalization.md`
- `docs/reviewer/VAL-2-ddm-multistage-and-cost-of-equity-discipline.md`
- `docs/reviewer/VAL-3-ffo-affo-subsector-multiples-and-forward-projection.md`

---

## 0. TL;DR

Tier 2 introduces a shared `AssumptionProfile` abstraction keyed by `(Archetype × Maturity)` that drives calibration for all four valuation models simultaneously: DCF, DDM, FFO, RevenueMultiple. Four open trackers (RM-3, VAL-1, VAL-2, VAL-3 Phase 3) close as one coordinated sprint instead of shipping piecemeal.

**Load-bearing invariants:**
1. Mature-large-bank DDM output is **bit-for-bit identical** (via `math.Float64bits` equality) to pre-Tier-2 for JPM/BAC/WFC
2. Profile resolution is **pure and deterministic** — same `(industry, FinancialData, config)` always produces the same profile
3. Replay tooling produces identical numerical output for any Tier-2-affected bundle (`F11` hermeticity from prior phases)
4. `pkg/finance/*` unchanged (D7 invariant from prior phases)

**Architectural shape:** new sibling subpackage `internal/services/valuation/profile/` mirroring the `datacleaner/industry/` pattern. JSON values (`config/assumption_profiles.json`) drive a typed Go schema with priority-ordered rule arrays. Resolver is a pure function `Resolve(facts, config) → (ResolvedProfile, ResolutionTrace)` invoked from `service.go::performValuation` upstream of `router.SelectModel`.

**Rollout:** P0a (package skeleton + Facts DTO + resolver) → P0b (JSON content + bundle manifest extensions + golden fixture harness) → P1-P4 (parallel worktrees for RM-3, VAL-1, VAL-2, VAL-3 P3, with explicit JSON-row + struct-field ownership).

---

## 1. Goals & non-goals

### Goals
- Close RM-3 (forward revenue multiple model)
- Close VAL-1 (DCF archetype-aware horizon + normalization)
- Close VAL-2 (DDM multi-stage for non-mature dividend payers)
- Close VAL-3 Phase 3 (forward FFO projection at cost-of-equity)
- Introduce the shared `AssumptionProfile` backbone so future model work consumes it consistently
- Preserve replay determinism across Tier 2 changes
- Preserve mature-large-bank DDM byte-for-byte (modulo additive `omitempty` response fields)

### Non-goals (explicit out of scope)
- **VAL-6** (HEALTHCARE_REIT keyword precedence collision) — blocked on unification refactor
- **DC-1** (datacleaner component primitive + parallel views) — Tier 4 foundational refactor
- **G-1** (growth blend weights coarse) — separate tracker, can ship anytime
- **RM-2 Phase 2** (Damodaran adoption) — Tier 3 independent improvement
- **VAL-3 Phase 2** (AFFO support) — independent of profile work; can ship before or after
- **Replay tooling follow-ups** (RPL-1/2/3/4) — separate Phase 2.D umbrella
- **Per-ticker override map** — explicitly rejected (fix the bucket, not the ticker)
- **Sum-of-the-parts multi-segment valuation** — separate future feature
- **Feature flag gating** — the bit-for-bit invariant IS the safety net
- **DB-backed Registry implementation** — interface ships in Tier 2; concrete DB impl tracked in `assumption-profile-db-backed-future.md`
- **CalculationVersion bump** — deferred to a single atomic post-Tier-2 commit (4.1 → 4.2, cache-busts on rollout)

---

## 2. Architecture overview

### 2.1 Package layout

```
internal/services/valuation/
├── models/                              ← EXISTING (math/algorithms)
│   ├── dcf_model.go                     (marker class)
│   ├── ddm.go                           (single-stage Gordon — UNTOUCHED in legacy branch)
│   ├── ffo.go                           (FFO model + subsector multiples)
│   ├── revenue_multiple.go              (trailing revenue model)
│   ├── router.go                        (defines ModelInput; gains Profile field)
│   └── *_test.go
│
├── profile/                             ← NEW (policy/data — sibling, not nested)
│   ├── profile.go                       (AssumptionProfile struct, enum types, ResolvedProfile)
│   ├── facts.go                         (Facts DTO — prevents import cycle)
│   ├── registry.go                      (Registry interface + jsonRegistry impl + LoadFromJSON)
│   ├── resolver.go                      (Resolve(facts, cfg) → ResolvedProfile, ResolutionTrace)
│   ├── validation.go                    (load-time invariant checks)
│   ├── trace.go                         (ResolutionTrace struct + manifest types)
│   ├── version.go                       (resolver_version constant + config_hash helpers)
│   └── *_test.go
│
├── service.go                           ← EXISTING — fires profile.Resolve, builds Facts DTO
├── currency.go, graham.go, …            ← EXISTING

config/
└── assumption_profiles.json             ← NEW (3 sections: version, profiles, rules, thresholds)

internal/services/valuation/models/testdata/golden/
├── jpm_ddm_pre_tier2.json               ← NEW (bit-for-bit baseline)
├── bac_ddm_pre_tier2.json
├── wfc_ddm_pre_tier2.json
└── (regression fixtures captured pre-P0a from master 0324057)

artifacts/tier2-baseline/                ← NEW
└── <date>/<ticker>/...                  (10 replay bundles for full E2E regression)
```

### 2.2 Import graph contract (one-way)

```
valuation/service.go      ───imports──→  profile  (calls Resolve)
valuation/service.go      ───imports──→  models   (calls SelectModel)
valuation/service.go      ───imports──→  entities (existing)

valuation/models/*.go     ───imports──→  profile  (reads ResolvedProfile via input.Profile)
valuation/models/*.go     ───imports──→  entities (existing)

valuation/profile/*.go    ───imports──→  (only stdlib + zap)
                          ─DOES NOT───→  models
                          ─DOES NOT───→  entities  (Facts DTO is neutral; no entities dep)
```

**Why:** Avoids the Go import-cycle trap `models → profile → models`. The `Facts` DTO is the neutral interchange struct populated by `service.go` from `entities.FinancialData` and consumed by `profile.Resolve`. Models import `profile` only to read the `*ResolvedProfile` type from `input.Profile` — they never call `profile.Resolve` directly.

### 2.3 Resolution lifecycle

```
HTTP request → handler → valuation.Service.CalculateValuation
  → performValuation(ctx, historicalData, marketData, macroData, opts)
     1. classifier.Classify(sic, ...) → industry string
     2. growthEstimator.EstimateGrowthRates(...) → growthEstimate
     3. wacc.Calculate(...) → waccResult
     4. ← NEW: facts := profile.NewFacts(industry, latestFD, historicalFD, marketData)
     5. ← NEW: resolved, trace := profileRegistry.Resolve(facts)
     6. modelInput := &models.ModelInput{..., Profile: resolved}
     7. model := router.SelectModel(ctx, ticker, industry, latestFD)   (UNCHANGED routing)
     8. result := model.Calculate(ctx, modelInput)
     9. ← NEW: response.AssumptionProfile = resolved.ProfileID
     10. ← NEW: response.ResolutionTrace = trace
     11. ← NEW: bundle.Manifest.AssumptionProfileManifest = (snapshot of resolved, trace, config_hash, etc.)
```

Routing and calibration are orthogonal. The router answers "which model class?" The resolved profile answers "with what calibration values?" Two different questions; two different code paths.

---

## 3. Type definitions

### 3.1 `profile/profile.go` — core types

```go
package profile

// Archetype identifies the company shape for valuation calibration purposes.
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
    TerminalGordonGrowth   TerminalMethod = "gordon_growth"
    TerminalExitMultiple   TerminalMethod = "exit_multiple"
)

// DiscountMethod identifies the discount rate to apply.
type DiscountMethod string

const (
    DiscountWACC         DiscountMethod = "wacc"
    DiscountCostOfEquity DiscountMethod = "cost_of_equity"
)

// AssumptionProfile is the full per-archetype calibration record.
// All 14 fields are populated by P0; downstream streams (P1-P4) consume the
// subset relevant to their model. Loaded from JSON via LoadFromJSON;
// values are validated at load time.
type AssumptionProfile struct {
    // Identity & key
    ProfileID   string    `json:"profile_id"`
    Archetype   Archetype `json:"archetype"`
    Maturity    Maturity  `json:"maturity"`

    // Used by all 4 models
    HorizonYears        int               `json:"horizon_years"`
    CompoundGrowthCap   float64           `json:"compound_growth_cap"`
    RevenueBaseMethod   RevenueBaseMethod `json:"revenue_base_method"`
    DiscountMethod      DiscountMethod    `json:"discount_method"`

    // DCF + RM-3 terminal handling
    TerminalMethod    TerminalMethod `json:"terminal_method"`
    Stabilized        bool           `json:"stabilized"`
    FadeYears         int            `json:"fade_years"`
    TerminalMultiple  float64        `json:"terminal_multiple"`

    // VAL-2 DDM specifics
    DPSGrowthCap            float64   `json:"dps_growth_cap"`
    PayoutPath              []float64 `json:"payout_path"`
    DividendForecastHorizon int       `json:"dividend_forecast_horizon"`
    StableDividendGrowth    float64   `json:"stable_dividend_growth"`

    // Archetype-specific size thresholds (override the global fallback)
    SizeThresholds *SizeThresholds `json:"size_thresholds,omitempty"`
}

// SizeThresholds carry archetype-specific revenue cutoffs that override the
// global fallback in maturity bucketing. Why: large-cap means different
// things for a bank ($50B) than a pre-revenue biotech ($2B); a single global
// threshold misclassifies one or the other.
type SizeThresholds struct {
    LargeCapMinUSD float64 `json:"large_cap_min_usd"`
    MidCapMinUSD   float64 `json:"mid_cap_min_usd"`
}

// ResolvedProfile is what gets stamped onto ModelInput.Profile after
// successful resolution. Carries the full AssumptionProfile plus the
// resolution trace for audit purposes.
type ResolvedProfile struct {
    AssumptionProfile           // embedded; consumers read fields directly
    Trace ResolutionTrace `json:"trace"`
}

// IsLegacyMatureLargeBankDDM reports whether the resolved profile is the
// legacy single-stage Gordon path. Models MUST consult this to take the
// bit-for-bit preservation branch.
func (r *ResolvedProfile) IsLegacyMatureLargeBankDDM() bool {
    return r != nil && r.DividendForecastHorizon == 0 &&
        r.Archetype == ArchetypeMatureLargeBank
}
```

### 3.2 `profile/facts.go` — neutral DTO

```go
package profile

// Facts is the neutral interchange struct populated by service.go from
// entities.FinancialData/HistoricalFinancialData/MarketData. It carries
// only the signals the resolver needs — keeping the profile package free
// of any entities or models imports (prevents Go import cycle).
//
// Pointer fields distinguish "no signal" (nil) from "zero is meaningful"
// (e.g., zero OI is meaningful; missing OI is not).
type Facts struct {
    Industry             string    // classifier output (e.g. "FIN_LARGE_BANK")
    IndustryNormalized   string    // upper-cased + trimmed at facts-construction time
    Revenue              *float64  // TTM revenue (already-resolved via RM-1 helper)
    OperatingIncome      *float64  // signed; negative triggers cyclical_trough archetype override
    NetIncome            *float64
    RevenueGrowthYoY     *float64  // computed at facts-construction (most recent two periods)
    ConsecutivePositiveOIYears int  // 0 if none; used for stability signal
    MarketCap            *float64
    DividendsPerShare    *float64  // for archetype refinement on dividend-paying tickers
}

// NewFacts constructs a Facts DTO from entities. Owned by service.go;
// the profile package itself contains no entities imports. Translation
// logic is at the consumer site so the boundary is one-way.
func NewFacts(industry string, latest, prior *entities.FinancialData, market *entities.MarketData) Facts {
    // (implementation lives in service.go since it imports entities;
    //  alternatively a thin adapter in a separate sub-package)
    panic("constructor lives at the consumer site")
}
```

**Why pointer fields:** distinguishing missing from zero is load-bearing. A pre-revenue biotech has `Revenue == 0` (zero is correct, not missing); a malformed bundle has `Revenue == nil` (missing). The resolver must treat these differently.

### 3.3 `profile/trace.go` — structured audit

```go
package profile

// ResolutionTrace is the structured audit trail for a single resolution
// outcome. Replaces the originally-proposed single route_reason string.
// Lands on every FairValueResponse and every artifact bundle manifest.
type ResolutionTrace struct {
    ProfileID       string   `json:"profile_id"`
    Source          Source   `json:"source"`                  // explicit | inferred | fallback
    ResolverVersion string   `json:"resolver_version"`        // semver of profile package
    ConfigVersion   string   `json:"config_version"`          // semver of assumption_profiles.json
    ConfigHash      string   `json:"config_hash,omitempty"`   // SHA-256 of canonicalized JSON
    MatchedRuleID   string   `json:"matched_rule_id,omitempty"`
    FallbackReason  string   `json:"fallback_reason,omitempty"`
    MissingFacts    []string `json:"missing_facts,omitempty"`
    HumanReason     string   `json:"human_reason,omitempty"`  // derived debug text
}

type Source string

const (
    SourceExplicit Source = "explicit"  // industry rule matched, archetype found
    SourceInferred Source = "inferred"  // partial match; maturity inferred from defaults
    SourceFallback Source = "fallback"  // no industry rule matched; conservative default applied
)

// AssumptionProfileManifest is the bundle-manifest extension carrying the
// full resolved profile + audit trail. Lands in the replay bundle (alongside
// existing manifest fields like clock_seed, git_sha). Two consumption modes:
//   - ResolvedSnapshot present → replay uses it directly (perfect determinism)
//   - ResolvedSnapshot nil → replay re-resolves; if ConfigHash mismatches
//     the current config_hash, replay emits non_reproducible_replay warning
type AssumptionProfileManifest struct {
    ProfileID        string          `json:"profile_id"`
    Source           Source          `json:"source"`
    ResolverVersion  string          `json:"resolver_version"`
    ConfigVersion    string          `json:"config_version"`
    ConfigHash       string          `json:"config_hash"`
    ResolvedSnapshot *AssumptionProfile `json:"resolved_snapshot,omitempty"`
    Trace            ResolutionTrace `json:"trace"`
}
```

### 3.4 `profile/registry.go` — interface + JSON impl

```go
package profile

// Registry is the lookup surface for profiles. Designed so a future
// DB-backed implementation can swap in without touching consumers
// (see docs/refactoring/spec/assumption-profile-db-backed-future.md).
type Registry interface {
    Resolve(facts Facts) (*ResolvedProfile, ResolutionTrace)
    Lookup(arch Archetype, mat Maturity) (*AssumptionProfile, bool)
    ConfigVersion() string
    ConfigHash() string
}

// LoadFromJSON loads the registry from assumption_profiles.json at the
// configured path. Returns error on:
//   - file not readable
//   - JSON malformed
//   - any validation failure (unknown archetype, missing fallback rule,
//     duplicate rule IDs, etc.)
// Engine MUST fail startup on any of these errors — invalid shipped config
// is an operator error, not user-data degradation.
func LoadFromJSON(path string) (Registry, error)

// jsonRegistry is the concrete JSON-backed implementation. Frozen at
// startup; safe for concurrent reads. Rule matching uses the priority-
// ordered slice in archetypeRules — no map iteration anywhere.
type jsonRegistry struct {
    configVersion string
    configHash    string
    profiles      map[archetypeMaturityKey]*AssumptionProfile  // lookup by (Arch, Mat)
    archetypeRules []ArchetypeRule                              // ordered; first match wins
    fallbackProfile *AssumptionProfile
    sizeThresholds globalSizeThresholds
    maturityThresholds MaturityThresholds
}

type ArchetypeRule struct {
    ID             string    `json:"id"`
    Priority       int       `json:"priority"`        // higher fires first
    IndustryPrefix string    `json:"industry_prefix"` // matched against Facts.IndustryNormalized
    Archetype      Archetype `json:"archetype"`
    Notes          string    `json:"notes,omitempty"`
}
```

---

## 4. JSON schema: `config/assumption_profiles.json`

### 4.1 Top-level structure

```json
{
  "$schema": "assumption-profile-v1",
  "config_version": "1.0.0",
  "resolver_version": "1.0.0",

  "profiles": {
    "mature_large_bank:mature": { ... },
    "growth_bank:standard_growth": { ... },
    "...": "(one entry per (archetype, maturity) combination that exists in practice; ~15-25 entries total, NOT a full 21-archetype × 3-maturity cartesian product since many archetypes pin to a single maturity)"
  },

  "archetype_rules": [
    {"id": "fin_large_bank",       "priority": 100, "industry_prefix": "FIN_LARGE_BANK",       "archetype": "mature_large_bank"},
    {"id": "fin_small_bank",       "priority": 95,  "industry_prefix": "FIN_SMALL_BANK",       "archetype": "growth_bank"},
    {"id": "insurance",            "priority": 95,  "industry_prefix": "INSURANCE",            "archetype": "insurance_company"},
    {"id": "fin_generic",          "priority": 50,  "industry_prefix": "FIN",                  "archetype": "mature_large_bank"},
    {"id": "reit_datacenter",      "priority": 100, "industry_prefix": "REIT_DATACENTER",      "archetype": "reit_datacenter"},
    {"id": "reit_celltower",       "priority": 100, "industry_prefix": "REIT_CELLTOWER",       "archetype": "reit_celltower"},
    {"id": "reit_industrial",      "priority": 100, "industry_prefix": "REIT_INDUSTRIAL",      "archetype": "reit_industrial"},
    {"id": "reit_residential",     "priority": 100, "industry_prefix": "REIT_RESIDENTIAL",     "archetype": "reit_residential"},
    {"id": "reit_retail",          "priority": 100, "industry_prefix": "REIT_RETAIL",          "archetype": "reit_retail"},
    {"id": "reit_healthcare",      "priority": 100, "industry_prefix": "REIT_HEALTHCARE",      "archetype": "reit_healthcare"},
    {"id": "reit_commercial",      "priority": 100, "industry_prefix": "REIT_COMMERCIAL",      "archetype": "reit_commercial"},
    {"id": "reit_generic",         "priority": 50,  "industry_prefix": "REIT",                 "archetype": "reit_residential"},
    {"id": "tech_saas",            "priority": 90,  "industry_prefix": "TECH_SAAS",            "archetype": "software_like_scaling"},
    {"id": "tech_generic",         "priority": 50,  "industry_prefix": "TECH",                 "archetype": "software_like_large_scale"},
    {"id": "mfg_semi",             "priority": 90,  "industry_prefix": "MFG_SEMI",             "archetype": "cyclical_mid_cycle"},
    {"id": "mfg_generic",          "priority": 50,  "industry_prefix": "MFG",                  "archetype": "cyclical_mid_cycle"},
    {"id": "health_biotech",       "priority": 90,  "industry_prefix": "HEALTH_BIOTECH",       "archetype": "pre_revenue_biotech"},
    {"id": "automotive",           "priority": 80,  "industry_prefix": "AUTOMOTIVE",           "archetype": "cyclical_mid_cycle"},
    {"id": "energy",               "priority": 80,  "industry_prefix": "ENERGY",               "archetype": "cyclical_mid_cycle"},
    {"id": "retail_consumer",      "priority": 60,  "industry_prefix": "RETAIL",               "archetype": "mature_large_scale"},
    {"id": "fallback_default",     "priority": 0,   "industry_prefix": "*",                    "archetype": "software_like_scaling"}
  ],

  "maturity_thresholds_fallback": {
    "large_cap_revenue_min_usd": 50000000000,
    "mid_cap_revenue_min_usd":   10000000000,
    "high_growth_revenue_yoy_min": 0.30,
    "mature_revenue_yoy_max":      0.10,
    "trough_oi_threshold":         0.0
  }
}
```

### 4.2 Sample profile entry

```json
{
  "mature_large_bank:mature": {
    "profile_id": "mature_large_bank:mature",
    "archetype": "mature_large_bank",
    "maturity": "mature",
    "horizon_years": 3,
    "compound_growth_cap": 1.5,
    "revenue_base_method": "raw_ttm",
    "discount_method": "cost_of_equity",
    "terminal_method": "gordon_growth",
    "stabilized": true,
    "fade_years": 0,
    "terminal_multiple": 0.8,
    "dps_growth_cap": 0.08,
    "payout_path": [],
    "dividend_forecast_horizon": 0,
    "stable_dividend_growth": 0.03,
    "size_thresholds": {
      "large_cap_min_usd": 50000000000,
      "mid_cap_min_usd":   10000000000
    }
  }
}
```

Note: `dividend_forecast_horizon: 0` is the bit-for-bit preservation signal for VAL-2's mature-large-bank path. Models MUST consult this field (via `ResolvedProfile.IsLegacyMatureLargeBankDDM()`) to take the legacy single-stage Gordon branch.

### 4.3 Load-time validation invariants

`profile.LoadFromJSON` MUST verify all of these at startup; any failure refuses to boot:

1. `config_version` is present and parseable as semver
2. `resolver_version` matches the compiled-in `profile.ResolverVersion` constant
3. Every entry in `profiles` has all 14 required fields with valid enum values
4. Every `archetype_rules` entry has unique `id`
5. Priorities have no ties unless explicitly documented (currently: subsector REITs all share priority 100, which is fine since each `industry_prefix` is unique)
6. Every `archetype` referenced by a rule has at least one profile entry across all maturity values
7. A fallback rule (`industry_prefix: "*"` or equivalent) exists with the lowest priority
8. `maturity_thresholds_fallback` is present and all values are non-negative
9. Computed config_hash (SHA-256 of canonicalized JSON) is stored on the registry

If ANY of these fails: `LoadFromJSON` returns error → DI container fails service construction → service refuses to boot. The engine never silently runs with malformed calibration config.

### 4.4 Two-failure-mode discipline

| Failure type | Engine response |
|---|---|
| **Shipped config malformed** (any of the 9 validation invariants fails) | Fail startup loudly. Operator error; do NOT silently degrade. |
| **User-data gap** (ticker's industry doesn't match any rule, or facts have nil-valued signals) | Apply fallback profile + emit `ResolutionTrace.Source: "fallback"` + log warning. Engine continues; valuation produced; audit trail visible. |

The two are NOT the same and must not collapse into a single "graceful degradation" path. User-data graceful degradation is a calibration-inference choice the user explicitly preferred. Shipped-config invalidity is an operator error and must surface loudly.

---

## 5. Resolver algorithm

### 5.1 Three-stage derivation

```go
// profile/resolver.go (sketch)

func (r *jsonRegistry) Resolve(facts Facts) (*ResolvedProfile, ResolutionTrace) {
    trace := ResolutionTrace{
        ResolverVersion: ResolverVersion,
        ConfigVersion:   r.configVersion,
        ConfigHash:      r.configHash,
    }

    // Stage 1: industry → archetype via priority-ordered rule match
    arch, ruleID, matched := r.matchArchetypeRule(facts.IndustryNormalized)
    if !matched {
        trace.Source = SourceFallback
        trace.FallbackReason = "no_industry_rule_matched"
        return r.applyFallback(&trace), trace
    }
    trace.MatchedRuleID = ruleID

    // Stage 1b: cyclical-trough override
    // When archetype is cyclical AND OperatingIncome < 0, override to cyclical_trough.
    // Why: trough state needs revenue normalization (max_ttm_or_floor) that the
    // mid-cycle profile doesn't have.
    if isCyclicalArchetype(arch) && facts.OperatingIncome != nil && *facts.OperatingIncome < r.maturityThresholds.TroughOIThreshold {
        arch = ArchetypeCyclicalTrough
        trace.HumanReason = "cyclical_trough_override:operating_income_negative"
    }

    // Stage 2: balance-sheet signals → maturity bucket
    mat, maturityReason := r.deriveMaturity(facts, arch)
    if maturityReason != "" {
        trace.HumanReason = joinReasons(trace.HumanReason, maturityReason)
    }

    // Stage 3: archetype-specific maturity overrides
    // Some archetypes pin maturity regardless of stage-2 output.
    // Why: mature_large_bank always means "mature" regardless of threshold drift.
    if pinnedMat, ok := archetypeMaturityPin(arch); ok {
        mat = pinnedMat
    }

    // Look up the resolved profile
    profile, ok := r.Lookup(arch, mat)
    if !ok {
        trace.Source = SourceFallback
        trace.FallbackReason = "no_profile_for_resolved_key:" + string(arch) + ":" + string(mat)
        return r.applyFallback(&trace), trace
    }

    trace.ProfileID = profile.ProfileID
    if trace.Source == "" {
        trace.Source = SourceExplicit
    }
    return &ResolvedProfile{AssumptionProfile: *profile, Trace: trace}, trace
}

func (r *jsonRegistry) matchArchetypeRule(industryNormalized string) (Archetype, string, bool) {
    // archetypeRules is sorted by Priority descending at LoadFromJSON time.
    // Iterate the slice; first matching rule wins. Deterministic by construction.
    for _, rule := range r.archetypeRules {
        if matchPrefix(industryNormalized, rule.IndustryPrefix) {
            return rule.Archetype, rule.ID, true
        }
    }
    return "", "", false
}

func (r *jsonRegistry) deriveMaturity(facts Facts, arch Archetype) (Maturity, string) {
    // Pull archetype-specific size thresholds; fall back to global thresholds.
    thresholds := r.thresholdsForArchetype(arch)

    if facts.Revenue == nil {
        return MaturityStandardGrowth, "ambiguous_no_revenue_signal"
    }

    revenue := *facts.Revenue
    yoy := 0.0
    if facts.RevenueGrowthYoY != nil {
        yoy = *facts.RevenueGrowthYoY
    }

    if revenue >= thresholds.LargeCapMinUSD && yoy < r.maturityThresholds.MatureYoYMax {
        return MaturityMature, "large_cap_low_growth"
    }
    if yoy >= r.maturityThresholds.HighGrowthYoYMin {
        return MaturityHighGrowth, "yoy_above_high_growth_threshold"
    }
    return MaturityStandardGrowth, "default_standard_growth"
}
```

### 5.2 Determinism guarantees

The resolver is pure: same `(Facts, configuredRegistry)` → always the same `(ResolvedProfile, ResolutionTrace)`. Specifically:

- **No I/O:** registry is frozen at construction; no DB reads, no file reads during resolution
- **No time:** resolver never calls `time.Now()`; freshness signals come pre-computed in `Facts` (built upstream where Clock is bound)
- **No random:** no `math/rand` calls
- **No map iteration:** archetype matching uses the sorted `archetypeRules` slice; profile lookup is a single `map[archetypeMaturityKey]` access (single-key access is deterministic in Go)
- **Stable industry normalization:** `Facts.IndustryNormalized` is computed at construction time using `strings.ToUpper(strings.TrimSpace(industry))` — deterministic
- **Tie-breaking:** rule priorities are unique within their `industry_prefix` namespace (validated at load); first-match on equal priority would still be deterministic since slice order is preserved

### 5.3 Failure-mode contract

| Resolver outcome | `Trace.Source` | `Trace.FallbackReason` | Engine action |
|---|---|---|---|
| Rule matched + profile found | `explicit` | (empty) | Use resolved profile |
| Rule matched + profile not found (key gap) | `fallback` | `no_profile_for_resolved_key:<arch>:<mat>` | Use fallback profile; emit WARN log |
| No rule matched (industry unmapped) | `fallback` | `no_industry_rule_matched` | Use fallback profile; emit WARN log |
| Facts.Revenue == nil (data gap) | `inferred` | (empty); `MissingFacts: ["revenue"]` | Use resolved profile (maturity defaulted to standard_growth) |
| Facts == nil (programmer error) | n/a | n/a | Panic; this is a bug, not a data gap |

---

## 6. Per-model integration (P1-P4)

### 6.1 P1 — RM-3 (`models/revenue_multiple.go`)

**Strategy:** ADDITIVE — extend existing `Calculate()` with a forward-projection branch gated on `Profile.HorizonYears > 0`. Trailing path (today's behavior) preserved as the fallback when profile says `HorizonYears == 0` OR `Profile == nil` (defensive).

**Diagnostic fields added to `ModelResult`:**
- `TrailingValue float64` — always computed (today's trailing-revenue math)
- `ForwardValue float64` — computed only when `Profile.HorizonYears > 0`
- `HorizonSelected int` — copied from `Profile.HorizonYears`
- `TerminalMultiple float64` — copied from `Profile.TerminalMultiple`

**JSON rows owned by P1:** `cyclical_*` (mid_cycle + trough), `hypergrowth_early`, `hypergrowth_profitable`, `pre_revenue_biotech` profile entries. Rule entries: `mfg_semi`, `mfg_generic`, `health_biotech`, `automotive`, `energy`.

### 6.2 P2 — VAL-1 (`service.go::performValuation` DCF body)

**Strategy:** Per VAL-1 spec, the DCF horizon is currently hard-coded to ~7 years (the growth estimator's slice length). Tier 2 makes horizon profile-driven: 3y (mature) / 5y (standard_growth) / 7y (high_growth) / 10y (hypergrowth_profitable). Terminal method gated on `Profile.TerminalMethod`.

**Diagnostic fields added to `entities.ValuationResult`:**
- `DCFHorizonYears int`
- `DCFTerminalMethod string` (`"gordon_growth"` or `"exit_multiple"`)
- `DCFTerminalPctOfEV float64` — flag if >0.80
- `DCFPerYearPV []float64` — for chart-friendly visualization
- `DCFTerminalGrowthUsed float64` — the clamped terminal growth rate

**JSON rows owned by P2:** `mature_large_scale`, `software_like_*`, `hypergrowth_profitable` profile entries. Rule entries: `tech_saas`, `tech_generic`, `retail_consumer`, plus archetype/maturity coverage.

### 6.3 P3 — VAL-2 (`models/ddm.go`)

**Strategy: PATH DUPLICATION, NOT FUNCTION EXTRACTION.** The existing `Calculate()` body (lines 53-192) stays literally untouched. A new `calculateMultiStage` function is added as a sibling. The dispatch:

```go
func (m *DDMModel) Calculate(ctx context.Context, input *ModelInput) (*ModelResult, error) {
    // Defensive: nil profile falls through to legacy path.
    if input.Profile == nil || input.Profile.IsLegacyMatureLargeBankDDM() {
        return m.calculateLegacy(ctx, input)  // EXISTING lines 53-192 lifted verbatim or kept inline
    }
    return m.calculateMultiStage(ctx, input)
}
```

The lifting from `Calculate` to `calculateLegacy` is the ONLY transform on the legacy path — no statement reordering, no variable renaming, no extraction of helper closures (the `pbvCheck` closure stays inline in the legacy path). Bit-for-bit math invariant holds because the source code is structurally identical.

**Why duplication beats extraction:** function extraction risks compiler-level optimization differences (e.g., escape analysis on the closure, inlining decisions). Duplication keeps both paths fully separate; the legacy path's compiled code is byte-identical to pre-Tier-2 since the source is byte-identical.

**JSON rows owned by P3:** `mature_large_bank`, `growth_bank`, `insurance_company`, `maturing_tech_first_dividend`, `mature_dividend_tech` profile entries. Rule entries: `fin_large_bank`, `fin_small_bank`, `insurance`, `fin_generic`.

### 6.4 P4 — VAL-3 Phase 3 (`models/ffo.go`)

**Strategy:** ADDITIVE — extend existing `Calculate()` with a forward-projection branch gated on `Profile.HorizonYears > 0`. REIT subsector multiples (already shipped via VAL-3 P1+P4) continue to apply on both paths.

**Diagnostic fields added to `ModelResult`:**
- `TrailingValue float64` (existing FFO-based snapshot — today's behavior)
- `ForwardValue float64` (forward FFO projection at cost-of-equity)
- (other diagnostics shared with P1 — `HorizonSelected`, `TerminalMultiple`)

**JSON rows owned by P4:** all 7 `reit_*` profile entries (residential, commercial, industrial, healthcare, datacenter, celltower, retail). Rule entries: all `reit_*` rules.

---

## 7. Backward compatibility strategy

### 7.1 Bit-for-bit DDM invariant — two-layer

**Layer 1 — Model numeric invariant** (the load-bearing assertion):

```go
// models/ddm_test.go
func TestDDM_JPM_LegacyPath_BitForBit(t *testing.T) {
    input := loadFixtureModelInput("testdata/jpm_pre_tier2_input.json")
    expectedResult := loadFixtureModelResult("testdata/jpm_pre_tier2_output.json")

    actual, err := ddmModel.Calculate(ctx, input)
    require.NoError(t, err)

    // Float64bits comparison — exact bit equality on the float fields
    assert.Equal(t,
        math.Float64bits(expectedResult.IntrinsicValuePerShare),
        math.Float64bits(actual.IntrinsicValuePerShare),
        "JPM legacy DDM IntrinsicValuePerShare drifted from pre-Tier-2 bits")
    assert.Equal(t,
        math.Float64bits(expectedResult.EquityValue),
        math.Float64bits(actual.EquityValue))
    assert.Equal(t,
        math.Float64bits(expectedResult.EnterpriseValue),
        math.Float64bits(actual.EnterpriseValue))

    // Non-float fields: exact equality
    assert.Equal(t, expectedResult.ModelType, actual.ModelType)
    assert.Equal(t, expectedResult.Confidence, actual.Confidence)
    assert.Equal(t, expectedResult.Warnings, actual.Warnings)
}
```

**Layer 2 — Response shape invariant** (new fields must not break consumers):

All Tier-2-added fields on `ModelResult`, `ValuationResult`, and `FairValueResponse` are tagged `omitempty`. When the legacy path runs (`horizon == 0`), those fields are zero-valued and thus omitted from JSON. A pre-Tier-2 consumer parsing the new response sees exactly the same field set as before. New consumers see the additional fields.

This means the JSON byte sequence DOES change between pre-Tier-2 and post-Tier-2 (new fields appear when the multi-stage path runs), but the *legacy fields are byte-identical for the legacy path*. That is the achievable, useful invariant.

**Cross-platform tolerance fallback:** If bit-for-bit proves brittle in practice (e.g., one CI environment uses a different Go toolchain version), the spec permits relaxation to `assert.InDelta(t, expected, actual, 1e-15 * abs(expected))` — but ONLY after documenting which platform drifted and why. The default invariant is `Float64bits`.

### 7.2 Golden fixture capture

Pre-P0a bootstrap commit:

1. Run live engine at master HEAD `0324057` against JPM/BAC/WFC/AAPL/MSFT/KO/F/MXL/NVDA/AMD/EQIX/PLD with `X-Midas-Trace: 1`
2. For DDM-routed tickers (JPM/BAC/WFC), capture the full `ModelInput` (serialized to JSON) and full `ModelResult` (serialized to JSON) into `internal/services/valuation/models/testdata/golden/<ticker>_pre_tier2_{input,output}.json`
3. For the full 10-ticker basket, capture artifact bundles into `artifacts/tier2-baseline/<date>/<ticker>/`
4. Commit both directories as one regression-baseline commit

The bootstrap commit is the LAST commit on master that doesn't depend on Tier 2 work. Every Tier 2 commit must pass the regression tests built on these fixtures.

### 7.3 Replay determinism via `ResolvedSnapshot`

When the artifact bundle is captured live, the request lifecycle writes the full resolved profile into `AssumptionProfileManifest.ResolvedSnapshot` (alongside `ProfileID`, `ConfigVersion`, `ConfigHash`). When replay tooling re-runs that bundle:

1. **Best case:** `ResolvedSnapshot` is present → replay bypasses `profile.Resolve` entirely and stamps the snapshot directly onto `ModelInput.Profile`. Perfect determinism regardless of config changes between capture and replay.
2. **Middle case:** `ResolvedSnapshot` is nil BUT `ConfigHash` matches the current config's hash → replay re-resolves using current config; produces identical results because the config didn't change.
3. **Degraded case:** `ResolvedSnapshot` is nil AND `ConfigHash` differs from current → replay re-resolves using current config; emits `non_reproducible_replay_warning` flag in the diff output. The replay still produces a number, but the diff explicitly attributes any drift to config evolution rather than code drift.

The first commit of P0b wires the snapshot WRITE into the bundle. Replay-tooling READ consumption can ship in a follow-up R-tracker (RPL-5) without blocking Tier 2.

---

## 8. Testing strategy

### 8.1 Replay tooling — used as-is

No refactor to `cmd/replay/` or `internal/observability/replay/`. The 14-flag CLI surface is sufficient. The bootstrap commit creates `artifacts/tier2-baseline/` with 10 bundles captured at master `0324057`. CI runs:

```bash
go run ./cmd/replay --diff-stages --from=parsed artifacts/tier2-baseline/
```

…per-PR for any PR touching `internal/services/valuation/**` or `config/assumption_profiles.json`. Existing `--allow-schema-drift` flag suppresses additive field appearance until baselines are recaptured post-Tier-2.

### 8.2 Go-test surgical pinning

```
internal/services/valuation/profile/
└── tier2_regression_test.go     ← NEW
```

Per-ticker assertions (6 fields, split-tolerance regime):

| Field | Tolerance | Reason |
|---|---|---|
| `AssumptionProfile` (resolved ID) | exact string match | Catches resolver flip |
| `HorizonSelected` | exact int match | Companion to profile assertion |
| `ChosenModel` | exact string match | Catches router regression |
| `PrimaryValuePerShare` | bit-for-bit (Float64bits) for `mature_large_bank` archetype; ε=1e-9 relative elsewhere | Bit-for-bit invariant |
| `TrailingValuePerShare` (where applicable) | ε=1e-9 relative | New field; tolerance from start |
| `WarningCount` | exact int match | Catches "we lost a warning" |

10 fixtures, one per ticker in the basket. Each fixture pins both the input (`ModelInput` snapshot) and the expected output (`ModelResult` snapshot + chosen model + resolved profile).

### 8.3 Edge-case unit tests

Targeted, not full-bundle:

- IFRS-FPI ticker (TSM): assert profile resolver receives the post-FX-conversion `Facts.Revenue` (per ADR), NOT the raw TWD ordinary-share revenue. Pin `AssumptionProfile == software_like_large_scale` (Damodaran-style for TSM).
- Pre-revenue biotech (synthetic fixture with us-gaap present but Revenue == 0): assert `AssumptionProfile == pre_revenue_biotech:high_growth`, archetype rule `health_biotech` matched.
- Mall REIT (SPG synthetic fixture): assert `AssumptionProfile == reit_retail:standard_growth`, rule `reit_retail` matched, profile carries low P/FFO multiple from VAL-3 P4.
- mREIT (mortgage REIT — out of model scope per VAL-3): assert resolver still produces SOMETHING (fallback), trace logs `non_property_reit_inferred`. Don't try to route through FFO; surface the inference via trace.

Coverage target ≥90% on `internal/services/valuation/profile/` per CLAUDE.md finance-module standard.

### 8.4 Resolver-specific tests

Independent of the model integration:

- `TestDerive_JPM_StaysMatureLargeBank` — resolver-level pin
- `TestDerive_NVDA_ResolvesToHypergrowth` — high-growth signal
- `TestDerive_MXL_NegativeOI_TriggersCyclicalTroughOverride` — Stage 1b override
- `TestDerive_UnknownIndustry_FallsBackWithExplicitTrace` — fallback path
- `TestRegistry_ValidationRejectsMalformedConfig` — load-time validation
- `TestRegistry_RuleOrderingDeterministic` — verifies sorted slice match (no map iteration)

---

## 9. Rollout plan

### 9.1 Bootstrap commit (pre-Tier-2)

```
chore(tier2): capture pre-Tier-2 regression baselines
  - artifacts/tier2-baseline/<date>/{JPM,BAC,WFC,AAPL,MSFT,KO,F,MXL,NVDA,AMD,EQIX,PLD}/
  - internal/services/valuation/models/testdata/golden/{jpm,bac,wfc}_ddm_pre_tier2_{input,output}.json
  - internal/services/valuation/profile/tier2_regression_test.go (skeleton; passing against current master output)
```

This commit lands on master at HEAD `0324057` (or wherever master is when Tier 2 kicks off). Every Tier 2 commit must keep these fixtures green.

### 9.2 P0a — Profile package skeleton + Facts DTO + resolver

```
feat(profile): add AssumptionProfile types, Facts DTO, and Registry interface
```

Scope:
- All Go type definitions (`profile.go`, `facts.go`, `trace.go`, `registry.go`, `version.go`)
- `LoadFromJSON` reads a minimal `config/assumption_profiles.json` containing ONLY the fallback profile + a single mature_large_bank profile (for the JPM bit-for-bit test)
- `Resolve` implements the 3-stage algorithm
- `Validation.go` covers all 9 load-time invariants
- Unit tests on the resolver: per-stage tests, fallback paths, validation rejections
- Coverage ≥90% on the profile package
- Verifies Go import boundary holds (`profile` imports nothing from `models` or `entities`)

Acceptance: profile package builds, all unit tests pass, golden DDM fixtures still green (because no model code changed yet; this commit only adds the package).

### 9.3 P0b — JSON content + bundle manifest + golden fixture harness

```
feat(profile): populate assumption_profiles.json + wire bundle manifest
```

Scope:
- Full `config/assumption_profiles.json` with 21 profile entries + 21 archetype rules + maturity_thresholds_fallback
- `AssumptionProfileManifest` struct added to bundle manifest (read by replay tooling lazily — write only in this commit)
- `service.go::performValuation` constructs `Facts` and calls `profileRegistry.Resolve` (but does NOT pass result to models yet — Profile field added but consumers are no-op)
- `FairValueResponse` gets `AssumptionProfile` + `ResolutionTrace` fields (omitempty)
- `ModelInput.Profile` field added (consumers are no-op until P1-P4)
- All Tier-2-added diagnostic fields on `ModelResult` and `ValuationResult` declared (zero-valued; populated by P1-P4)

Acceptance: build green, all existing tests pass, profile resolves on every request, response carries trace, bundle manifest carries resolved snapshot. **Critically: golden DDM fixtures still bit-for-bit identical because no model code path changed.**

### 9.4 P1-P4 — Parallel worktree dispatch

After P0b lands on master, dispatch all 4 streams simultaneously into separate worktrees. Each stream's diff is restricted to:

- ONE model file (`revenue_multiple.go` / `service.go::DCF body` / `ddm.go` / `ffo.go`)
- That model's tests
- That stream's owned JSON profile rows (per ownership table §10)
- That stream's owned archetype rule entries (per ownership table §10)

No stream touches struct definitions (those are owned by P0b). No stream touches another stream's JSON rows. Rebase-before-merge: each stream rebases against current master at V/R/Q gate before merging.

V/R/Q gate per stream:
- VERIFIER: independently validates functional invariants
- REVIEWER: cross-cutting code review
- QA: operator-empathy smoke + 10-ticker live API regression on the stream's affected tickers

### 9.5 Post-merge integration gate

After all 4 streams merge:

```
chore(tier2): integration gate
  - go test ./... -count=1 (47/47 packages green)
  - go run ./cmd/replay --diff-stages artifacts/tier2-baseline/  (all 10 bundles pass)
  - Live API regression: curl loop on basket + verify expected (profile, primary_value) per ticker
```

### 9.6 Archival commit

```
docs(reviewer): archive 4 Tier-2 trackers — RM-3 + VAL-1 + VAL-2 + VAL-3 P3 closed
```

Move all 4 tracker files to `docs/reviewer/archive/`. File any small follow-up trackers surfaced during V/R/Q.

### 9.7 CalculationVersion bump

```
feat(valuation): bump CalculationVersion 4.1 → 4.2 (Tier 2 close)
```

Single atomic commit; cache-busts on rollout. Last commit of the sprint.

---

## 10. JSON-row + struct-field ownership table

To make parallel-after-P0 actually safe, ownership is explicit:

### 10.1 Profile JSON keys

| Owner | Profile keys |
|---|---|
| P0a/P0b | `software_like_scaling:standard_growth` (fallback default) + all archetype rule entries with priority < 100 (`fin_generic`, `tech_generic`, `mfg_generic`, `reit_generic`, `retail_consumer`, `fallback_default`) |
| P1 (RM-3) | `cyclical_mid_cycle:*`, `cyclical_trough:*`, `hypergrowth_early:*`, `pre_revenue_biotech:*` profiles + `mfg_semi`, `health_biotech`, `automotive`, `energy` rules |
| P2 (VAL-1) | `mature_large_scale:*`, `software_like_large_scale:*`, `software_like_scaling:high_growth`, `hypergrowth_profitable:*` profiles + `tech_saas` rule |
| P3 (VAL-2) | `mature_large_bank:mature`, `growth_bank:*`, `insurance_company:*`, `maturing_tech_first_dividend:*`, `mature_dividend_tech:*` profiles + `fin_large_bank`, `fin_small_bank`, `insurance` rules |
| P4 (VAL-3 P3) | All `reit_*:*` profiles (residential, commercial, industrial, healthcare, datacenter, celltower, retail) + all subsector rules (`reit_residential`, `reit_commercial`, `reit_industrial`, `reit_healthcare`, `reit_datacenter`, `reit_celltower`, `reit_retail`) |

### 10.2 Struct fields

| Struct | Owner | Fields |
|---|---|---|
| `ModelInput` | P0b | `Profile *ResolvedProfile` |
| `ModelResult` | P0b declares all; P1/P4 populate | `AssumptionProfile`, `HorizonSelected`, `TerminalMultiple`, `TrailingValue`, `ForwardValue` |
| `ValuationResult` | P0b declares all; P2 populates | `DCFHorizonYears`, `DCFTerminalMethod`, `DCFTerminalPctOfEV`, `DCFPerYearPV`, `DCFTerminalGrowthUsed` |
| `FairValueResponse` | P0b | `AssumptionProfile`, `ResolutionTrace` (both omitempty) |
| `AssumptionProfileManifest` | P0b | all fields |

**Discipline:** P1-P4 never modify a struct definition. They only populate fields P0b already declared as zero-valued. Eliminates the schema-merge surface entirely.

### 10.3 Conflict resolution rule

If two streams accidentally touch a non-owned JSON row or non-owned struct field, the rebase-before-merge step catches it. Conflict resolution rule: **revert the unauthorized change in the violating stream; route the actual content into its rightful owning stream.** Never resolve by "take both" — that masks ownership violations.

---

## 11. P0 acceptance criteria

Before P1-P4 dispatch, P0a + P0b must satisfy ALL of:

1. `internal/services/valuation/profile/` package exists with typed config, `ResolvedProfile`, `Facts` DTO, `ResolutionTrace`
2. Config validation runs at service startup AND in CI (`go test ./internal/services/valuation/profile/`)
3. Resolver is deterministic; rule matching uses ordered slice (no map iteration anywhere)
4. Legacy mature-large-bank DDM path produces unchanged numeric output via `math.Float64bits` equality on JPM/BAC/WFC fixtures
5. Service-response JSON compatibility explicitly defined: all new fields `omitempty`, legacy-path response carries exactly the pre-Tier-2 field set
6. Bundle manifest includes ProfileID, Source, ResolverVersion, ConfigVersion, ConfigHash; `ResolvedSnapshot` is populated on write
7. Profile package contains NO imports of `models` or `entities` (enforced via `go vet` or import-boundary test similar to `cmd/server/import_boundary_test.go`)
8. New response fields are `omitempty` if golden byte equality matters (verified via legacy-path response shape test)
9. Archetype rule matching has explicit priority ordering and tie-breaking documented
10. `Facts` DTO uses pointer types for missing-vs-zero distinction (`Revenue *float64`, etc.)

---

## 12. Quality gates

- Coverage ≥90% on `internal/services/valuation/profile/` package
- Coverage ≥90% on modified per-model files (`ddm.go`, `revenue_multiple.go`, `ffo.go`); ≥92% package-level on `internal/services/valuation/models/`
- No `time.Now()` outside the consumer layer (Clock pattern from RM-1.A)
- Profile resolver is a pure function — verified by absence of imports beyond stdlib + zap
- `pkg/finance/*` unchanged (D7 invariant from prior phases)
- Replay determinism: any Tier-2-affected bundle replays to identical numerical output (verified via `cmd/replay --diff-stages` per PR)
- Bit-for-bit JPM/BAC/WFC DDM output: pinned via `math.Float64bits` equality
- Import boundary: `profile` package does NOT import `models` or `entities` (enforced)
- JSON validation: malformed `assumption_profiles.json` fails startup loudly (not silently)

---

## 13. Open questions for human review

None significant. All 6 design axes plus 7 critique-driven revisions are settled. The spec is implementable as written.

**Optional refinement (RPL-5 follow-up tracker):** Replay tooling can grow a `09-profile-resolution.json` bundle stage capturing the resolver's input/output explicitly for `--diff-stages` drill-down. Not required for Tier 2; tracked separately.

---

## 14. Companion: future DB-backed Registry

See `docs/refactoring/spec/assumption-profile-db-backed-future.md` for the deferred work. The `Registry` interface ships in Tier 2 specifically to allow a future DB-backed implementation to swap in without consumer changes. The JSON-backed implementation is sufficient for current scope (single-tenant, personal investment use, fintech-platform-grade quality target).

---

## 15. Estimated commits at Tier 2 close

Following Tier 1's commit-shape pattern (~8 commits for 4 streams + closeout):

| # | Commit | Owner |
|---|---|---|
| 1 | `chore(tier2): capture pre-Tier-2 regression baselines` | bootstrap |
| 2 | `feat(profile): add AssumptionProfile types, Facts DTO, and Registry interface` | P0a |
| 3 | `feat(profile): populate assumption_profiles.json + wire bundle manifest` | P0b |
| 4 | `feat(valuation): RM-3 forward revenue multiple path` + merge | P1 |
| 5 | `feat(valuation): VAL-1 DCF archetype-aware horizon + diagnostics` + merge | P2 |
| 6 | `feat(valuation): VAL-2 DDM multi-stage path (legacy preserved bit-for-bit)` + merge | P3 |
| 7 | `feat(valuation): VAL-3 P3 forward FFO projection` + merge | P4 |
| 8 | `chore(tier2): integration gate (full test suite + 10-ticker live API regression)` | gate |
| 9 | `docs(reviewer): archive 4 Tier-2 trackers` | closeout |
| 10 | `feat(valuation): bump CalculationVersion 4.1 → 4.2 (Tier 2 close)` | closeout |

Plus 4 merge commits (one per worktree). Total: ~11-14 atomic commits over ~1 week of focused engineering.
