# Request-Driven Valuation Parameter Overrides — Implementation Plan

- **Date:** 2026-06-05
- **Status:** SHIPPED — merged to master `a9afd97` (2026-06-06), live-verified. Archived.
- **Role producing this:** ARCH (PLAN_AND_CREATE).
- **Authoritative design (source of truth):** `docs/refactoring/archive/2026-06-05-request-valuation-overrides-design.md`
- **Engine baseline:** CalculationVersion 4.4 (no bump on the default path — prime directive).
- **Owner area:** `internal/services/valuation`, `internal/services/valuation/params` (new), `internal/api/v1/handlers`, `internal/api/server.go`.

> This is an implementer-grade plan. It conforms to the approved design; it does **not** re-open scope. Where the design left a decision implicit, this plan records the decision in §8 (Risks & open questions) with a recommendation and proceeds on the recommended choice. If a BACKEND implementer finds a recommended choice unworkable, stop and escalate to ARCH rather than silently diverging.

---

## 1. Summary

### Goal
Let a request body set the **absolute value of every valuation math knob** (DCF / WACC / growth), wired through the engine and validated, with explicit auditable resolution (`config < profile < request`). Introduce a single `Resolve()` that produces an `EffectiveValuationParams` struct read by every consumption site, eliminating the per-knob archaeology of "is this configurable, and is it honored?"

### Non-goals (YAGNI — from design §11)
Multipliers/weights; infra/auth/data-source/cache config; per-knob multiplier syntax; cache-keying on overrides (overrides bypass the cache wholesale); any UI; persisting override presets. Out of scope for *this* feature: changing any default math.

### Prime directive (bit-for-bit guarantee)
**`Resolve(...)` called with empty overrides MUST return values byte-identical to today's `config + profile + literal` resolution.** The default (no-override) path stays byte-for-byte identical. The following gating tests MUST stay green at *every commit*:
- `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC `math.Float64bits` equality).
- `cmd/replay --diff-stages` vs `artifacts/tier2-baseline/` on default params.
- `TestRecomputeUmbrellas_NoMutation`, `TestOrchestrator_LedgerOrdering`.
- Full `go test ./... -count=1` EXIT=0.

No `CalculationVersion` bump. Overridden responses are non-canonical by construction (and cache-bypassed).

---

## 2. Requirements

### Functional
- **F1.** Single-ticker `POST /api/v1/fair-value/{ticker}` accepts `{"options": ValuationOverrides}` and returns `FairValueResponse`. `GET` is unchanged.
- **F2.** `BulkFairValueRequest` gains `options ValuationOverrides` applied to all tickers.
- **F3.** Every knob in design §5 is overridable as an **absolute value**, resolved via precedence `config defaults < AssumptionProfile < request override`, and **actually wired** to the consumption site.
- **F4.** Legacy `override_beta` / `override_rf` (GET query + bulk top-level fields) keep working and map into the same override input. **If both a legacy field and its `options` twin are set → 422 conflict** (explicit, never silently resolved).
- **F5.** `FairValueResponse` gains an `applied_overrides` object echoing, per knob the request touched, the final value and its source layer.
- **F6.** Any request carrying **any** override bypasses the cache (read + write), preserving today's behavior.

### Non-functional / validation philosophy
- **NF1.** Two validation layers, both → RFC 7807 `422` with field-named detail:
  - **Layer 1 (handler, static):** per-knob range + enum checks — *fat-finger rails*. Negatives allowed where economically real (terminal growth, revenue growth, beta, tax, risk-free). Ranges are sanity rails (catch `50` vs `0.05`), not economic gates.
  - **Layer 2 (resolver, cross-knob):** hard invariants needing computed WACC / growth-rate length. Reject with typed `ParamError` mapped to 422. **No silent clamping.**
- **NF2.** Replay determinism: a captured bundle's pinned override snapshot widens to the full resolved param set so replay re-runs deterministically.
- **NF3.** No behavior shift on the default path (the gating suite in §1).
- **NF4.** Per-request estimator construction (when overrides present) must have **no hidden shared state** — confirmed in §8 (R3). Default path reuses the shared `s.growthEstimator`.
- **NF5.** `params` package is import-clean: zero imports of `internal/services/valuation/models` or `internal/core/entities`, mirroring the `profile` package boundary. Enforced by an import-boundary test.

---

## 3. Architecture / file-level design

### 3.1 New package `internal/services/valuation/params/`

```
internal/services/valuation/params/
  params.go        // EffectiveValuationParams + Source enum + named default constants
  resolve.go       // Resolve* funcs: precedence merge + Layer-2 invariant validation
  errors.go        // ParamError (typed; carries Knob + Reason); IsParamError helper
  params_test.go   // struct/default-constant tests + byte-identity table
  resolve_test.go  // precedence table tests + per-invariant 422 tests
  boundary_test.go // import-boundary test (no models/entities)
```

Rationale for the package boundary: `profile` already proves the pattern — a leaf package depending only on the standard library (and, if needed, `internal/config` types passed *by value*, not the package). Keep `params` dependency-light: it accepts plain scalars and a small `Overrides` input struct, **not** `*config.Config`, `*entities.*`, or `*profile.ResolvedProfile` directly, to keep the import graph clean and the resolver pure/testable. The service layer is responsible for projecting config/profile values into the resolver's input (mirroring how `service.go` projects entities into `profile.Facts`).

> **Boundary note on config:** `internal/config` does **not** import `models`/`entities`, so importing it from `params` would not create the forbidden cycle. However, to keep `Resolve` a pure function of scalars (trivially table-testable and replay-deterministic) the recommendation (§8 R4) is to pass a small `Defaults` value-struct rather than `*config.Config`. The boundary test asserts only the hard rule (no `models`/`entities`); importing `config` is allowed but discouraged.

### 3.2 `EffectiveValuationParams` (params.go)

A flat struct of **resolved, non-pointer** values — the single source of truth the engine reads. One field per knob in design §5, plus a parallel provenance map.

```go
package params

// Source records which precedence layer supplied a resolved knob value.
type Source string

const (
    SourceDefault Source = "default" // named constant or config default
    SourceProfile Source = "profile" // AssumptionProfile
    SourceRequest Source = "request" // request override (options / legacy field)
)

// EffectiveValuationParams is the fully-resolved knob set the engine reads.
// Every field is a concrete value; Provenance carries the per-knob Source for
// the applied_overrides response echo (design §8).
type EffectiveValuationParams struct {
    // Terminal value
    TerminalGrowthRate    float64  // explicit terminal g; see TerminalGrowthExplicit
    TerminalGrowthExplicit bool    // true when caller set terminal_growth_rate (skips auto-derive)
    TerminalGrowthCap     float64  // auto-derive cap (the relocated 3% literal)
    TerminalMethod        string   // "gordon_growth" | "exit_multiple"
    TerminalMultiple      float64  // 0 = use industry default / Gordon-only

    // Horizon + growth staging
    HorizonYears          int      // 0 = "fall through to growth-rate length" (legacy signal)
    Stage1Years           int
    Stage2Years           int
    Stage3Years           int
    MaxGrowthRate         float64
    MinGrowthRate         float64

    // WACC inputs
    Beta                  float64
    RiskFreeRate          float64
    MarketRiskPremium     float64

    // Tax
    TaxRate               float64

    // Provenance: knob-name -> Source. Only knobs the caller TOUCHED or that
    // the resolver wants to echo are populated. See design §8 / §8(R5).
    Provenance map[string]Source
}
```

### 3.3 Named default constants (params.go) — the core fix

Relocate the scattered literals into named constants so each knob has exactly one resolution path and "the 3% literal" no longer hides in a function body:

```go
const (
    // DefaultTerminalGrowthCap is the auto-derive cap formerly hardcoded as
    // maxTerminalGrowth := 0.03 in service.go::calculateTerminalGrowthRate
    // (and duplicated, unused, in config.ValuationConfig.DefaultTerminalGrowthCap).
    DefaultTerminalGrowthCap = 0.03
    // DefaultTerminalGrowthFloor mirrors estimator TerminalGrowthFloor / the
    // service's 0.02 inflation floor.
    DefaultTerminalGrowthFloor = 0.02
    // DefaultTerminalWACCSpread is the "≥ 2% below WACC" guard band.
    DefaultTerminalWACCSpread = 0.02

    // Growth-stage defaults — mirror growthsvc.DefaultEstimatorConfig().
    DefaultStage1Years = 3
    DefaultStage2Years = 4
    DefaultStage3Years = 0 // legacy 7-year horizon byte-identity signal

    // Growth-rate bounds — mirror config.dcf_{max,min}_growth_rate defaults.
    DefaultMaxGrowthRate = 0.5
    DefaultMinGrowthRate = -0.3

    DefaultTerminalMethod = "gordon_growth"
)
```

> **These constants are reflections of today's values, not new policy.** Each MUST equal the literal/default it replaces, verified by a same-package test asserting `DefaultTerminalGrowthCap == 0.03`, etc. Do NOT "tidy" any value while relocating.

### 3.4 `Resolve` signature — TWO-PHASE (resolve-WACC-inputs, then resolve-DCF-params)

The design's §12 risk note and §4.4 require the `terminal_growth_rate < WACC` invariant to be checked against the **computed** WACC, which itself can be overridden (beta/rf/MRP). So `Resolve` is split into two phases, each pure:

```go
// Defaults is the resolver's lower-precedence input (config-derived scalars).
type Defaults struct {
    TerminalGrowthCap float64 // config.DefaultTerminalGrowthCap
    MaxGrowthRate     float64 // config.DCFMaxGrowthRate
    MinGrowthRate     float64 // config.DCFMinGrowthRate
    Stage1Years       int     // estimator default (3)
    Stage2Years       int     // estimator default (4)
    Stage3Years       int     // estimator default (0)
    // Data-source baselines for WACC inputs / tax, already resolved by the
    // service from market/macro/entity data BEFORE calling the resolver.
    Beta              float64 // marketData.GetEffectiveBeta()
    RiskFreeRate      float64 // macroData.GetEffectiveRiskFreeRate()
    MarketRiskPremium float64 // macroData.MarketRiskPremium
    TaxRate           float64 // latestFinancialData.TaxRate
    // Profile-derived knob baselines (0 / "" mean "profile carries no value").
    ProfileHorizonYears  int    // resolvedProfile.HorizonYears (0 = legacy)
    ProfileTerminalMethod string // resolvedProfile.TerminalMethod ("" = none)
    ProfileTerminalMultiple float64
    // Industry default for exit-multiple (LookupMultiple result; 0 = none).
    IndustryExitMultiple float64
}

// Overrides is the request-layer input, projected from the transport DTO.
// All pointer fields; nil = "not set by request".
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

// Phase 1: resolve the inputs that feed WACC + tax + growth staging. These do
// NOT depend on a computed WACC. Validates only the cross-knob invariants that
// are WACC-independent (min ≤ max growth; stage-sum ≥ 1; horizon ≤ stage sum).
// growthRateLen is the length of the growth-rate slice the estimator produced
// for THIS request (needed for the horizon ≤ len invariant); pass it after the
// estimator runs (see §3.7 ordering).
func ResolveInputs(d Defaults, o Overrides, growthRateLen int) (EffectiveValuationParams, error)

// Phase 2: validate the knobs that need the computed WACC, mutating/annotating
// the params in place (currently just terminal growth: explicit value < WACC,
// or auto-derive against the resolved cap). Returns the final terminal growth +
// updated provenance.
func ResolveTerminal(p *EffectiveValuationParams, computedWACC, historicalCAGR float64) error
```

Both phases return a typed `*ParamError` on invariant violation. `EffectiveValuationParams.Provenance` is populated in `ResolveInputs` for every knob the caller touched (and for `TerminalMethod`/`HorizonYears`/`TerminalMultiple` when sourced from profile, per design §8 "plus any knob the resolver pulled from profile/default that the caller asked about").

> **Why two functions, not one:** the ordering constraint (§3.7) makes a single `Resolve` impossible without passing both the estimator output AND the computed WACC — but WACC is computed *between* those two points. Splitting keeps each function pure and the call sites obvious. See §8 (R1).

### 3.5 Precedence merge per knob (resolve.go)

Each knob resolves independently `default ← profile ← override`. Profile source applies only where a profile field exists:

| Knob | Default source | Profile source? | Override field |
|------|----------------|-----------------|----------------|
| `terminal_growth_rate` | const `DefaultTerminalGrowthFloor`/auto-derive | **No** (profile has no terminal-g field) | `o.TerminalGrowthRate` |
| `terminal_growth_cap` | `d.TerminalGrowthCap` (config) | No | `o.TerminalGrowthCap` |
| `horizon_years` | growth-rate length (legacy) | `d.ProfileHorizonYears` (when >0) | `o.HorizonYears` |
| `stage{1,2,3}_years` | `d.Stage{1,2,3}Years` (estimator) | No | `o.Stage{1,2,3}Years` |
| `max_growth_rate` | `d.MaxGrowthRate` (config) | No | `o.MaxGrowthRate` |
| `min_growth_rate` | `d.MinGrowthRate` (config) | No | `o.MinGrowthRate` |
| `terminal_method` | const `DefaultTerminalMethod` | `d.ProfileTerminalMethod` (when !="") | `o.TerminalMethod` |
| `terminal_multiple` | `d.IndustryExitMultiple` (industry lookup) | `d.ProfileTerminalMultiple` (when >0) | `o.TerminalMultiple` |
| `tax_rate` | `d.TaxRate` (entity/config) | No | `o.TaxRate` |
| `beta` | `d.Beta` (market data) | No | `o.Beta` |
| `risk_free_rate` | `d.RiskFreeRate` (macro) | No | `o.RiskFreeRate` |
| `market_risk_premium` | `d.MarketRiskPremium` (macro/config) | No | `o.MarketRiskPremium` |

The service is responsible for collapsing "profile vs config vs default" into the `Defaults` value-struct fields **in the order the legacy code already uses**, so an empty-override `ResolveInputs` reproduces today's read. Specifically: `d.ProfileHorizonYears`/`d.ProfileTerminalMethod`/`d.ProfileTerminalMultiple` are populated from the resolved profile when present; the resolver applies the profile value only when override is nil AND the profile value is "set" (>0 / !=""), exactly matching the existing `if resolvedProfile != nil && resolvedProfile.HorizonYears > 0` gate at `service.go:1110`.

### 3.6 Consumption-site rewire (table: site → today → after)

| # | Site (file:line) | Today | After (reads `params`) |
|---|------------------|-------|------------------------|
| S1 | `service.go:806` + `:1721` `calculateTerminalGrowthRate` | hardcoded `maxTerminalGrowth := 0.03`; WACC−2% clamp; floor 0.02 | `ResolveTerminal` owns the logic. When `params.TerminalGrowthExplicit`, use `params.TerminalGrowthRate` subject to `< WACC` invariant (→422). Else auto-derive using `params.TerminalGrowthCap` (relocated 3%), `DefaultTerminalGrowthFloor`, `DefaultTerminalWACCSpread` — byte-identical to today when cap=0.03. The method `calculateTerminalGrowthRate` is **kept** for the default/auto path but its `0.03` becomes `params.TerminalGrowthCap`. |
| S2 | `service.go:1105–1129` horizon block | `profile.HorizonYears` + **silent clamp** to growth length, with WARN | `params.HorizonYears` (already resolved). The silent clamp is **removed** — the `horizon ≤ stage-sum` and `horizon ≤ growthRateLen` checks become resolver invariants (→422 in `ResolveInputs`). On the default path `params.HorizonYears` equals the old clamped value (resolver never rejects when override is nil and profile horizon ≤ available rates — see §8 R2 for the legacy-clamp-vs-422 nuance). |
| S3 | `service.go:107` estimator construction (in `NewService`) | shared `s.growthEstimator` from `DefaultEstimatorConfig()` | Default path: reuse `s.growthEstimator` (fast path, unchanged). Override path: when any of `stage*`/`max`/`min` overridden, build a **per-request** `growthsvc.NewEstimator(cfg, s.logger, s.calcEmitter)` with `cfg` derived from `params.Stage{1,2,3}Years` + `params.{Max,Min}GrowthRate`. See §3.7 for the ordering wrinkle (estimator config needs stage/min/max BEFORE growth runs, but horizon validation needs growth length AFTER). |
| S4 | `service.go:746–754` WACC inputs (`wacc.Inputs`) | `macroData.MarketRiskPremium`, `beta`, `riskFreeRate` (beta/rf already override-applied at L688-695) | `params.MarketRiskPremium`, `params.Beta`, `params.RiskFreeRate`. The existing L688-695 `opts.OverrideBeta/OverrideRiskFree` application is **replaced** by reading the resolved `params.Beta/RiskFreeRate` (legacy fields are mapped into `Overrides` upstream — see S7). |
| S5 | `service.go:1169–1180` exit-multiple block | industry-config `LookupMultiple` only | `params.TerminalMethod` decides Gordon vs exit-multiple; `params.TerminalMultiple` is the multiple (industry lookup feeds the *default* via `d.IndustryExitMultiple`). When `params.TerminalMethod == "exit_multiple"` set `dcfInputs.ExitMultiple = params.TerminalMultiple`. |
| S6 | `service.go:1146` tax (`dcfInputs.TaxRate`) | `latestFinancialData.TaxRate` | `params.TaxRate`. (Note: WACC `TaxRate` at L754 and the alt-model `ModelInput.TaxRate` at L1569 should read `params.TaxRate` too for consistency — see §8 R6.) |
| S7 | handler opts → service | `ValuationOptions{OverrideBeta, OverrideRiskFree}` | `ValuationOptions` widened to carry the full `params.Overrides` (or an embedded copy). Legacy `override_beta/override_rf` map into `Overrides.Beta/RiskFreeRate`; conflict with `options.beta/risk_free_rate` → 422 (handler Layer-1). |
| S8 | alt-model path `performAlternativeValuation` (`service.go:1498`) | reads `profile` via `ModelInput.Profile`; tax/IBD from entity/Restated | Stamp resolved `params` onto `ModelInput` (new `ModelInput.Params *params.EffectiveValuationParams`, additive). Alt models that consume horizon/terminal/tax read it; **DDM stays on its legacy path** (gated by `IsLegacyMatureLargeBankDDM`) — do NOT route DDM through new params (bit-for-bit). |

### 3.7 Ordering constraint (the load-bearing sequencing)

The single hard ordering rule (design §4.4, §12): **resolve WACC inputs (beta/rf/MRP) → compute WACC → THEN resolve+validate terminal growth against the computed WACC.** But the growth estimator's config (stage/min/max) must be resolved *before* the estimator runs, and the horizon invariant needs the growth-rate length *after* it runs. Concretely, the new ordering inside `performValuation`:

```
1. Build Overrides from opts (projected from DTO + legacy fields).      [handler/service]
2. Resolve estimator config knobs (stage1/2/3, max, min) into a local
   EstimatorConfig — these don't need WACC. Pick shared vs per-request
   estimator (S3).                                                       [before growth]
3. Run growth estimate with that estimator → growthRateLen known.
4. Call params.ResolveInputs(Defaults, Overrides, growthRateLen):
   - validates min≤max, stage-sum≥1, horizon≤stage-sum, horizon≤growthRateLen
   - resolves beta/rf/MRP/tax/horizon/terminal-method/terminal-multiple
   - returns EffectiveValuationParams (terminal growth NOT yet finalized)  → 422 on violation
5. Apply params.Beta/RiskFreeRate/MarketRiskPremium to the WACC inputs
   (replaces the L688-695 opts application + L748 macro read).
6. Compute WACC (wacc.Calculate) — unchanged math.
7. Call params.ResolveTerminal(&params, waccResult.WACC, historicalCAGR):
   - explicit terminal_growth_rate: assert < WACC (warn if within 1%)      → 422 on violation
   - else auto-derive using params.TerminalGrowthCap                        (byte-identical default)
8. Feed params.HorizonYears / TerminalMethod / TerminalMultiple / TaxRate
   / TerminalGrowthRate into dcfInputs (S1, S2, S5, S6).
```

> **Subtlety for step 2/3:** the estimator config knobs (`max`/`min`/`stage*`) are validated for the `min ≤ max` and `stage-sum ≥ 1` invariants in `ResolveInputs` at step 4 — i.e. AFTER the estimator already ran at step 3. To avoid running the estimator with an invalid config, do a **cheap Layer-1 static pre-check in the handler** (ranges) plus a **pre-estimator structural check** of `min ≤ max` / `stage-sum ≥ 1` when overrides are present (a tiny `params.ValidateEstimatorConfig(Overrides, Defaults)` helper called before building the per-request estimator). `ResolveInputs` then re-asserts (idempotent). The horizon-vs-length invariant genuinely needs the post-estimator length and stays in `ResolveInputs`. Implementer: keep the pre-check and `ResolveInputs` invariant logic in ONE place (a shared unexported `validateStaging` func) to avoid drift.

### 3.8 Import-boundary test (boundary_test.go)

Mirror `internal/services/valuation/profile`'s boundary test: parse the `params` package's imports (via `go/packages` or a hardcoded `golang.org/x/tools/go/packages` walk as `profile` does) and assert none match `internal/services/valuation/models` or `internal/core/entities`. Fail with a message naming the offending import.

---

## 4. API Contracts

### 4.1 Transport DTO `ValuationOverrides` (handler layer)

Lives in `internal/api/v1/handlers` (transport-only; translated into `params.Overrides` so the wire format never reaches the domain). All fields `*pointer` + `omitempty`. JSON names per design §5 catalog:

```go
// ValuationOverrides is the request-body transport for per-request valuation
// knob overrides. All fields optional; nil = "use resolved default".
type ValuationOverrides struct {
    TerminalGrowthRate *float64      `json:"terminal_growth_rate,omitempty" example:"-0.01"`
    TerminalGrowthCap  *float64      `json:"terminal_growth_cap,omitempty" example:"0.03"`
    HorizonYears       *int          `json:"horizon_years,omitempty" example:"5"`
    GrowthStages       *GrowthStages `json:"growth_stages,omitempty"`
    MaxGrowthRate      *float64      `json:"max_growth_rate,omitempty" example:"0.5"`
    MinGrowthRate      *float64      `json:"min_growth_rate,omitempty" example:"-0.3"`
    TerminalMethod     *string       `json:"terminal_method,omitempty" example:"exit_multiple"` // gordon_growth|exit_multiple
    TerminalMultiple   *float64      `json:"terminal_multiple,omitempty" example:"14.0"`
    TaxRate            *float64      `json:"tax_rate,omitempty" example:"0.21"`
    Beta               *float64      `json:"beta,omitempty" example:"1.2"`
    RiskFreeRate       *float64      `json:"risk_free_rate,omitempty" example:"0.045"`
    MarketRiskPremium  *float64      `json:"market_risk_premium,omitempty" example:"0.05"`
}

type GrowthStages struct {
    Stage1Years *int `json:"stage1_years,omitempty" example:"3"`
    Stage2Years *int `json:"stage2_years,omitempty" example:"4"`
    Stage3Years *int `json:"stage3_years,omitempty" example:"0"`
}

// SingleFairValueRequest is the POST body.
type SingleFairValueRequest struct {
    Options *ValuationOverrides `json:"options,omitempty"`
}
```

`BulkFairValueRequest` gains `Options *ValuationOverrides json:"options,omitempty"` (applies to all tickers). Legacy `OverrideBeta`/`OverrideRiskFree` top-level fields stay.

### 4.2 New route `POST /api/v1/fair-value/{ticker}`

- **Registration:** `server.go` ~L299, add `fairValueGroup.POST("/:ticker", fairValueHandler.PostFairValue)` alongside the existing `GET("/:ticker", ...)` and `POST("/bulk", ...)`. Gin routes POST and GET on the same path to different handlers — no conflict. **Watch the existing `POST("/bulk")`:** Gin treats `/:ticker` and `/bulk` as a wildcard-vs-static conflict on the same method. Verify route registration order/priority; if Gin panics on conflicting wildcard, register `/bulk` before `/:ticker` (Gin's tree prefers static segments, so this is usually fine, but the test in §5 must boot the router to confirm).
- **Request:** body `{"options": {...}}` (may be empty `{}` or omitted → behaves like GET).
- **Response 200:** `FairValueResponse` (+ `applied_overrides`).
- **Status codes:** `200` success; `400` invalid ticker / malformed JSON; `422` Layer-1 range/enum violation, Layer-2 invariant violation, legacy-vs-options conflict, or genuine engine 422 (FPI / insufficient data / model-not-applicable); `401/403/429/500` per existing middleware.

### 4.3 RFC 7807 422 shape (field-named detail)

Reuse `h.sendError` / `ErrorResponse`. For a Layer-1 or Layer-2 override rejection:

```json
{
  "type": "https://problems.midas.dev/INVALID_OVERRIDE",
  "title": "Invalid valuation override",
  "status": 422,
  "detail": "terminal_growth_rate (0.12) must be strictly less than WACC (0.094)",
  "instance": "/api/v1/fair-value/MU",
  "context": { "knob": "terminal_growth_rate", "value": 0.12, "wacc": 0.094 },
  "code": "INVALID_OVERRIDE",
  "timestamp": "2026-06-05T...Z",
  "method": "POST"
}
```

Static range failures use `code: "INVALID_OVERRIDE"` with `detail` naming the field + allowed range. The conflict case uses `detail: "beta set in both legacy override_beta and options.beta; supply only one"` with `context.knob: "beta"`. Map `params.ParamError` → 422 via `errors.As(err, *params.ParamError)` in both `PostFairValue` and the bulk loop (per-ticker failure for bulk).

> **Status-code note:** legacy GET range checks currently return **400** (`fair_value.go:331/338`). The design specifies **422** for the new override validation. Keep GET's existing 400 behavior unchanged (back-compat); the NEW `options`-path validation returns 422 per design §7.3. Document this asymmetry in `API_DOCUMENTATION.md`.

### 4.4 `applied_overrides` response object

`FairValueResponse` gains:

```go
AppliedOverrides map[string]AppliedOverride `json:"applied_overrides,omitempty"`
```

```go
type AppliedOverride struct {
    Value  any    `json:"value"`  // resolved final value (float64 or int or string)
    Source string `json:"source"` // "request" | "profile" | "default"
}
```

Populated from `EffectiveValuationParams.Provenance` + the resolved values. **Omitted entirely (`omitempty`) on the default GET path** so existing GET responses stay byte-identical (the parity test in §6 depends on this). Only present when the request supplied `options` (or legacy overrides). See §8 (R5) for the representation decision (request-set knobs always echoed; profile/default knobs echoed only when the caller asked about that knob).

### 4.5 OpenAPI / Swagger regeneration

Per the 2026-06-04 FEEDBACK-LOG rule: regenerate generated files with the **go.mod-pinned** swag version (`go.mod` pins `github.com/swaggo/swag v1.8.12`):

```
go run github.com/swaggo/swag/cmd/swag@v1.8.12 init -g cmd/server/main.go --parseDependency --parseInternal -o docs
go build ./docs/
```

Add `@Router /fair-value/{ticker} [post]` annotations to `PostFairValue` and update `docs/openapi.yaml` + `docs/API_DOCUMENTATION.md` + `CONTRACTS.md` + `README.md` by hand for the new route, the `options` schema, the `applied_overrides` field, and the 422 conflict rule. Confirm `swagger.json`/`swagger.yaml`/`docs.go` compile (`go build ./docs/`).

### 4.6 Critical abstractions (module boundaries)
- `params.EffectiveValuationParams` — the ONLY value the engine reads for knobs (single source of truth).
- `params.Overrides` / `params.Defaults` — the resolver's pure inputs (no domain types).
- `params.ParamError` — the typed cross-knob-violation carrier (→422).
- `handlers.ValuationOverrides` — the wire DTO; never crosses into `valuation`/`params` as-is (projected first).
- `valuation.ValuationOptions` — the existing service-boundary seam, widened to carry the override input.

---

## 5. Tasks by Agent — ordered, commit-sized (BACKEND)

Phasing maps onto design §13. **Every commit must keep the full gating suite (§1) green.** Each task lists files, change, and exit check.

### BACKEND

**Phase 1 — `params` package + byte-identity proof FIRST (design §13.1)**

- **T1. Scaffold `params` package (struct + constants + boundary test).**
  - Files: `internal/services/valuation/params/params.go`, `errors.go`, `params_test.go`, `boundary_test.go`.
  - Change: define `EffectiveValuationParams`, `Source`, all named default constants (§3.3), `ParamError` + `Error()` + `IsParamError`. Add `params_test.go` asserting each constant equals the literal it replaces (`DefaultTerminalGrowthCap == 0.03`, `DefaultStage3Years == 0`, etc.). Add `boundary_test.go` (no `models`/`entities` imports).
  - Exit: `go test ./internal/services/valuation/params/...` green; boundary test passes.

- **T2. Implement `ResolveInputs` + `ResolveTerminal` + `validateStaging` (default-path-faithful).**
  - Files: `internal/services/valuation/params/resolve.go`, `resolve_test.go`.
  - Change: precedence merge (§3.5), provenance population, the WACC-independent invariants in `ResolveInputs`, the WACC-dependent terminal logic in `ResolveTerminal` (a faithful port of `calculateTerminalGrowthRate`'s auto-derive math when no explicit override). Add the precedence table tests + per-invariant 422 tests + a **byte-identity test**: for a representative `Defaults` (cap=0.03, stages 3/4/0, etc.) and EMPTY `Overrides`, assert resolved values equal the legacy reads, and `ResolveTerminal` reproduces `calculateTerminalGrowthRate` output across a table of `(historicalCAGR, wacc)` pairs via `math.Float64bits` equality.
  - Exit: package tests green, including the bit-identity table.

**Phase 2 — Rewire consumption sites to read `params` on the default path (design §13.2)**

- **T3. Widen `ValuationOptions` to carry the override input; thread through (no behavior change).**
  - Files: `internal/services/valuation/options.go`, `service.go` (`CalculateValuation` hasOverrides check ~L223/546, `performValuation` signature/body).
  - Change: add `Overrides params.Overrides` (keep `OverrideBeta`/`OverrideRiskFree` for back-compat mapping). Update `hasOverrides` to also be true when any `Overrides.*` field is set. Map legacy `OverrideBeta/RiskFree` into `Overrides.Beta/RiskFreeRate` at the service boundary (single place). **No consumption-site reads yet** — purely plumbing.
  - Exit: full suite green; cache-bypass still triggers for legacy overrides.

- **T4. Resolve `params` in `performValuation` and rewire the DCF consumption sites (S1, S2, S5, S6) + WACC inputs (S4).**
  - Files: `internal/services/valuation/service.go`.
  - Change: implement the §3.7 ordering. Build `Defaults` from config/profile/market/macro/entity/industry. Call `ResolveInputs` after growth, `ResolveTerminal` after WACC. Replace L688-695 opts application, L748 MRP read, L806 terminal-growth call, L1105-1129 horizon block, L1146 tax, L1169-1180 exit-multiple with `params` reads. Keep `calculateTerminalGrowthRate` as the auto-derive helper but parameterize its cap. On the default path (empty overrides) every resolved value MUST equal today's.
  - Exit: **`TestDDM_LegacyPath_BitForBit` green; `cmd/replay --diff-stages` vs `artifacts/tier2-baseline/` shows zero default-path drift; full suite green.** This is the riskiest commit — run the replay diff explicitly before committing.

- **T5. Per-request estimator (S3) + alt-model `ModelInput.Params` (S8).**
  - Files: `internal/services/valuation/service.go`, `internal/services/valuation/models/router.go` (or wherever `ModelInput` is defined — additive field), `performAlternativeValuation`.
  - Change: when staging/min/max overridden, build a per-request estimator; else reuse `s.growthEstimator`. Stamp `params` onto `ModelInput` (additive, nil-safe; DDM ignores it — legacy path). Confirm per-request estimator has no shared mutable state (it's `config + logger + emitter`, all read-only).
  - Exit: full suite green; default path still uses shared estimator (assert via a test that a no-override call does not allocate a new estimator — or simpler, assert output parity). DDM bit-for-bit green.

**Phase 3 — DTO + handler Layer-1 validation (design §13.3)**

- **T6. Add `ValuationOverrides`/`GrowthStages`/`SingleFairValueRequest` DTOs + `options` on bulk; projection to `params.Overrides`.**
  - Files: `internal/api/v1/handlers/fair_value.go`.
  - Change: DTO structs (§4.1); a `projectOverrides(*ValuationOverrides) params.Overrides` helper; legacy-field mapping with **conflict detection** (legacy `override_beta` + `options.beta` both set → collect a conflict). No new route yet — wire projection into the EXISTING bulk path behind `request.Options`.
  - Exit: suite green; bulk with `options` resolves; conflict returns 422.

- **T7. Layer-1 static validation (ranges + enum) in a shared validator.**
  - Files: `internal/api/v1/handlers/fair_value.go` (+ `fair_value_validation.go` if cleaner).
  - Change: `validateOverrides(*ValuationOverrides) *ErrorResponse`-style helper implementing design §5 static ranges + the `terminal_method` enum + the `exit_multiple ⇒ multiple resolvable` precondition where statically knowable. Field-named 422 (§4.3). Negatives allowed where design §5 says so.
  - Exit: table test per knob range; enum test; suite green.

**Phase 4 — Resolver Layer-2 invariants + 422 mapping (design §13.4)**

- **T8. Map `params.ParamError` → 422 at all call sites.**
  - Files: `internal/api/v1/handlers/fair_value.go` (error classification in GET/POST/bulk), `service.go` (ensure `ParamError` propagates un-wrapped-enough for `errors.As`).
  - Change: add `errors.As(err, &paramErr)` branch returning `sendError(... 422 "INVALID_OVERRIDE" ...)` with `context.knob`. Bulk: per-ticker `BulkFailure{ErrorCode:"INVALID_OVERRIDE"}`.
  - Exit: invariant-violation requests return 422 with the right `knob`; suite green.

**Phase 5 — `POST /fair-value/{ticker}` + bulk `options` + OpenAPI + `applied_overrides` (design §13.5)**

- **T9. Add `PostFairValue` handler + route.**
  - Files: `internal/api/v1/handlers/fair_value.go`, `internal/api/server.go` (~L299).
  - Change: `PostFairValue` binds `SingleFairValueRequest`, projects+validates overrides (Layer-1), builds `ValuationOptions`, calls the service, renders `FairValueResponse` (shared builder with GET to avoid drift — extract a `buildFairValueResponse(result)` helper). Register the route; verify no Gin wildcard/static conflict with `/bulk`.
  - Exit: POST with empty `{}` ≡ GET; POST with overrides moves the number; router boots without panic; suite green.

- **T10. `applied_overrides` response field + provenance wiring.**
  - Files: `internal/api/v1/handlers/fair_value.go`, `internal/core/entities/...` (if the resolved params/provenance must ride on `ValuationResult` to reach the handler — see §8 R5), `service.go`.
  - Change: surface the resolved `EffectiveValuationParams` (or just its `Provenance` + touched values) from the service to the handler so `AppliedOverrides` can be built. Populate only on override requests (`omitempty`).
  - Exit: `applied_overrides` echoes request knobs with `source:"request"`; absent on default GET (parity test green).

- **T11. OpenAPI/Swagger regen + API-doc-sync.**
  - Files: regen `docs/swagger.json|yaml`, `docs/docs.go`; hand-edit `docs/openapi.yaml`, `docs/API_DOCUMENTATION.md`, `CONTRACTS.md`, `README.md`.
  - Change: per §4.5; use the pinned swag version; `go build ./docs/`.
  - Exit: `go build ./docs/` passes; all doc surfaces describe the route, `options` schema, `applied_overrides`, 422 conflict rule, and agree with the serialized struct.

**Phase 6 — e2e override tests + cache-bypass + artifact-pin widening (design §13.6)**

- **T12. End-to-end override tests + cache-bypass assertions + replay bundle pin.**
  - Files: `internal/integration/api_routes_test.go` (new POST cases), `internal/api/server_test.go`, the artifact-snapshot site in `PostFairValue` (widen the pinned override map to the full `ValuationOverrides`/resolved params).
  - Change: the "number actually moved" test (§6); cache-bypass test (override request neither reads nor writes `valuation:v4:TICKER`); widen the bundle `Snapshot("handler.entry", "02-handler-options.json", ...)` for POST to carry the full options. Confirm `cmd/replay` re-reads it deterministically.
  - Exit: §6 tests green; replay determinism preserved; full suite + race green.

---

## 6. Test plan

### New tests (locations + names)
- **Precedence (`internal/services/valuation/params/resolve_test.go`):**
  - `TestResolveInputs_Precedence_PerKnob` — table: for each knob, `config < profile < override` (assert resolved value + `Provenance` source). Knobs with no profile source assert `config < override` only.
- **Per-invariant 422 (`resolve_test.go`):**
  - `TestResolveInputs_Invariant_MinGreaterThanMax_Returns422` (`min_growth_rate ≤ max_growth_rate`).
  - `TestResolveInputs_Invariant_StageSumBelowOne_Returns422` (`sum ≥ 1`).
  - `TestResolveInputs_Invariant_HorizonExceedsStageSum_Returns422`.
  - `TestResolveInputs_Invariant_HorizonExceedsGrowthLen_Returns422`.
  - `TestResolveTerminal_Invariant_TerminalGrowthGEWACC_Returns422` (+ a near-WACC warn sub-case).
  - `TestResolveTerminal_Invariant_ExitMultipleUnresolvable_Returns422` (method=exit_multiple, no multiple, no industry default).
  - Each asserts `errors.As(err, *params.ParamError)` and `ParamError.Knob`.
- **Byte-identity (`params_test.go` / `resolve_test.go`):**
  - `TestResolveInputs_EmptyOverrides_MatchesLegacyDefaults`.
  - `TestResolveTerminal_EmptyOverride_MatchesCalculateTerminalGrowthRate` — `math.Float64bits` equality across a `(historicalCAGR, wacc)` table vs the legacy function.
- **Handler Layer-1 (`internal/api/v1/handlers/...` or integration):**
  - `TestValidateOverrides_RangePerKnob` (table, incl. allowed-negatives cases).
  - `TestValidateOverrides_TerminalMethodEnum`.
  - `TestPostFairValue_LegacyVsOptionsConflict_Returns422` (beta in both).
- **Parity (`internal/integration/api_routes_test.go`):**
  - `TestPostFairValue_EmptyOptions_EqualsGet` — response identical to GET **modulo `applied_overrides`** (assert `applied_overrides` absent).
- **The number actually moved (`internal/integration/api_routes_test.go`):**
  - `TestPostFairValue_ExitMultipleHorizon_MovesTerminalShare` — `POST /fair-value/MU` with `terminal_method=exit_multiple` + `horizon_years=5` → assert `dcf_terminal_pct_of_ev` drops vs the default `GET`, and `applied_overrides` echoes the request knobs with `source:"request"`. (Use a recorded/stubbed MU fixture if MU isn't in the test corpus; otherwise pick a ticker present in the integration harness and document the substitution.)
- **Cache-bypass (`internal/integration/...`):**
  - `TestPostFairValue_WithOverrides_BypassesCache` — override request does not populate or read `valuation:v4:TICKER`.

### Gating regression suite (must stay GREEN at EVERY commit)
- `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC `math.Float64bits`).
- `cmd/replay --diff-stages` vs `artifacts/tier2-baseline/` on default params (run after T4 and T5 especially).
- `TestRecomputeUmbrellas_NoMutation`, `TestOrchestrator_LedgerOrdering`.
- `go test ./... -count=1` EXIT=0; `go test -race ./...` for the touched packages.

### Bit-for-bit acceptance method (explicit)
The byte-identity requirement is satisfied iff: (1) `TestResolveInputs_EmptyOverrides_MatchesLegacyDefaults` + `TestResolveTerminal_EmptyOverride_MatchesCalculateTerminalGrowthRate` pass with `math.Float64bits` equality; AND (2) `cmd/replay --diff-stages` reports zero numeric drift on `artifacts/tier2-baseline/` with default params; AND (3) `TestDDM_LegacyPath_BitForBit` passes. All three are required — the resolver test proves the resolution math, the replay proves the wired engine, the DDM test proves the alt-model path.

---

## 7. Spec Updates (checklist for the implementer — do NOT edit now)

Per the 2026-06-04 API-doc-sync rule, update these surfaces as part of the feature (they must agree with the serialized structs):

- [ ] `docs/openapi.yaml` — new `POST /fair-value/{ticker}`; `options`/`ValuationOverrides`/`GrowthStages` schema; `applied_overrides` on `FairValueResponse`; `options` on `BulkFairValueRequest`; 422 responses.
- [ ] `docs/API_DOCUMENTATION.md` — new endpoint, knob catalog, validation layers (note GET-stays-400 vs options-path-422 asymmetry), `applied_overrides`, legacy-vs-options conflict rule.
- [ ] `CONTRACTS.md` — invoked-surface entry for the new POST route + override contract.
- [ ] `README.md` — endpoint surface line (mirrors the CLAUDE.md "midas surface to know" bullet).
- [ ] `docs/swagger.json` / `docs/swagger.yaml` / `docs/docs.go` — REGENERATED via pinned swag (`v1.8.12`); `go build ./docs/` passes.
- [ ] `ARCHITECTURE.md` — note the `params` resolver as the single knob-resolution path + the `config < profile < override` precedence.
- [ ] `TESTING.md` — add the byte-identity acceptance method + new test locations to the patterns list.
- [ ] `CLAUDE.md` — add `internal/services/valuation/params/` to "Important Files"; add a gotcha: "Every valuation knob resolves through `params.Resolve*`; empty-override resolution is byte-identical (gated by replay + DDM bit-for-bit); GET range checks stay 400, options-path validation is 422."
- [ ] Optionally append a closeout note under `docs/refactoring/spec/` referencing this plan once shipped (no separate archive move needed until superseded).

---

## 8. Risks & open questions (decisions + recommendations)

- **R1. One `Resolve` vs two-phase.** *Decision: TWO-PHASE* (`ResolveInputs` + `ResolveTerminal`). The `terminal_growth_rate < WACC` invariant must run against the computed WACC (design §12), which is produced *after* WACC inputs are resolved and *between* the estimator run and the terminal calc. A single `Resolve` would force passing both the estimator output and the WACC, but those aren't available at the same point. Two pure functions keep call sites obvious and each independently testable. *If the implementer prefers a single `Resolve` taking `(Defaults, Overrides, growthRateLen, computedWACC)` called once after WACC, that also works — but then the per-request estimator config must be resolved by a separate tiny helper before the estimator, duplicating the staging merge. The two-phase split avoids that duplication; recommend it.*

- **R2. Legacy silent horizon-clamp vs new 422.** Today `service.go:1117` **silently clamps** `profile.HorizonYears` to the available growth-rate count and WARNs. The design makes "horizon ≤ growth length" a 422 invariant. *Risk:* if `ResolveInputs` rejects whenever profile horizon > growth length even on the DEFAULT (no-override) path, it changes default-path behavior (a profile-driven response that previously succeeded now 422s) — violating the prime directive. *Decision/recommendation:* the 422 invariant fires **only for request-supplied `horizon_years`**. When horizon comes from the profile (override nil), preserve the legacy clamp+WARN (no 422). Implement this by passing a flag/Source into the invariant check: `ResolveInputs` rejects only when `Provenance["horizon_years"] == SourceRequest`. This keeps default-path byte-identity while making explicit caller overrides hard-fail. **Flagging because the design's §5 table states the invariant unconditionally; this plan narrows it to request-sourced horizons to protect the prime directive. ARCH confirm.**

- **R3. Per-request estimator hidden shared state.** `growthsvc.Estimator` is `{config, logger, calcEmitter}` — config is a value, logger/emitter are shared read-only sinks. Building one per request is cheap (no I/O) and has no mutable shared state. *Confirmed safe.* The per-request estimator writes traces through the same `calcEmitter` (correct — traces are request-scoped via ctx). No action beyond a comment.

- **R4. `params` importing `internal/config`.** Allowed (config has no models/entities cycle) but discouraged. *Decision:* pass a `Defaults` value-struct, not `*config.Config`, to keep `Resolve` pure and trivially testable. Boundary test asserts only the hard rule (no models/entities).

- **R5. How `applied_overrides` reaches the handler + represents profile/default fall-through.** The resolved `EffectiveValuationParams` lives in `service.performValuation`; the handler needs its `Provenance` + values. *Decision/recommendation:* carry the resolved params (or a compact `AppliedOverrides` projection) on `entities.ValuationResult` as an additive, `omitempty` field (e.g. `result.AppliedOverrides`), populated only when `hasOverrides`. The handler copies it onto the response. **Representation:** echo every knob the request explicitly set with `source:"request"`; additionally echo a knob with `source:"profile"`/`"default"` ONLY when the caller set a *different* knob in the same family and asking is useful — to keep it simple, the recommended v1 behavior is **echo exactly the knobs the request touched** (all `source:"request"`), which is the direct cure for "was my value honored?" and avoids guessing what the caller "asked about". Profile/default echoes can be added later if needed. **Flagging because design §8 mentions "plus any knob the resolver pulled from profile/default that the caller asked about" — recommend deferring that to v2 to avoid an ambiguous "asked about" definition. ARCH confirm scope.**

- **R6. Tax knob applied to WACC + alt-model, not just DCF.** Design §6 only lists the DCF `service.go:1146` tax site. But `tax_rate` also feeds WACC (`wacc.Inputs.TaxRate`, L754) and `ModelInput.TaxRate` (L1569). *Recommendation:* apply `params.TaxRate` to ALL THREE for a coherent override (a caller overriding tax expects it everywhere). This is still byte-identical on the default path (`params.TaxRate == latestFinancialData.TaxRate` when not overridden). **Flagging because design §6's table only names the DCF site; this plan extends tax to WACC + alt-model for coherence. Low risk, default-path-safe. ARCH confirm.**

- **R7. Gin route conflict `/:ticker` (POST) vs `/bulk` (POST).** Both are POST under `/fair-value`. Gin's radix tree usually prefers the static `/bulk` over the `:ticker` wildcard, but registration can panic on perceived conflict in some versions. *Mitigation:* the §5 T9 exit check boots the real router and asserts no panic + correct routing of both `/bulk` and `/MU`. If it conflicts, register `/bulk` first or special-case `ticker == "bulk"`.

- **R8. `terminal_growth_cap` vs explicit `terminal_growth_rate` interaction.** Design §5 says cap is "ignored when explicit `terminal_growth_rate` set". *Decision:* `ResolveTerminal` checks `TerminalGrowthExplicit` first; if true, cap is unused (only the `< WACC` invariant applies). Covered by the resolver tests.

- **R9. MU may not be in the integration test corpus.** The "number actually moved" e2e test names MU. *Mitigation:* if MU lacks a recorded fixture, substitute a ticker present in the harness with positive OI that routes through DCF (so `dcf_terminal_pct_of_ev` is populated) and document the substitution in the test. The assertion (terminal share drops under exit-multiple + shorter horizon) is ticker-agnostic.

### Non-blocking open questions
- Should `applied_overrides` also surface on the **bulk** response per-ticker? (Recommend: yes, same `omitempty` field on each `FairValueResponse` in `results[]`.)
- Should the near-WACC terminal-growth **warning** (design §5: "warn if within 1% of WACC") be a response `warnings[]` entry or only a log line? (Recommend: append to `result.Warnings` so the caller sees it — consistent with existing warning surfacing.)

---

## 9. Acceptance criteria

- **AC1.** `POST /api/v1/fair-value/{ticker}` with empty/absent `options` returns a response byte-identical to `GET` for the same ticker, except `applied_overrides` is absent on both. (Parity test green.)
- **AC2.** `POST` with `terminal_method=exit_multiple` + `horizon_years=5` produces a lower `dcf_terminal_pct_of_ev` than the default GET, and `applied_overrides` echoes those knobs with `source:"request"`. (Number-moved test green.)
- **AC3.** Each hard invariant (terminal_growth < WACC; horizon ≤ stage-sum; horizon ≤ growth-len for request-set horizon; min ≤ max; stage-sum ≥ 1; exit_multiple resolvable) returns 422 RFC-7807 with `context.knob` naming the violated knob. (Per-invariant tests green.)
- **AC4.** Setting both legacy `override_beta` and `options.beta` returns 422 conflict. (Conflict test green.)
- **AC5.** Any override request bypasses the `valuation:v4:TICKER` cache (read + write). (Cache-bypass test green.)
- **AC6.** Default-path bit-for-bit: `TestDDM_LegacyPath_BitForBit` green; `cmd/replay --diff-stages` vs `artifacts/tier2-baseline/` reports zero drift; `TestRecomputeUmbrellas_NoMutation` + `TestOrchestrator_LedgerOrdering` green; no `CalculationVersion` bump.
- **AC7.** `params` package imports neither `models` nor `entities`. (Boundary test green.)
- **AC8.** All doc surfaces (openapi, API_DOCUMENTATION, CONTRACTS, README, regenerated swagger) describe the new route/`options`/`applied_overrides` and agree; `go build ./docs/` passes.
- **AC9.** Full `go test ./... -count=1` EXIT=0.

---

## 10. Implementation roadmap (suggested order)

T1 → T2 (params package, prove byte-identity) → T3 (plumb options) → **T4 (rewire DCF sites; run replay diff)** → T5 (per-request estimator + alt-model) → T6 → T7 (DTO + Layer-1) → T8 (Layer-2 → 422) → T9 (POST route) → T10 (`applied_overrides`) → T11 (OpenAPI/doc sync) → T12 (e2e + cache-bypass + artifact pin). Commit after each task; run the gating suite each time; run the replay diff explicitly after T4 and T5.

---

## 11. GitHub Issue Update
- Issue: N/A (no issue provided).
- Status: not updated.
- Actions taken: none (planning artifact only).

---

## 12. Next steps
1. **BACKEND** executes T1→T12 per §5, committing per task in the worktree.
2. ARCH confirms the three flagged narrowings before/within T4 + T10: **R2** (horizon 422 only for request-set horizon), **R5** (`applied_overrides` v1 = request-touched knobs only), **R6** (tax applied to WACC + alt-model too).
3. **REVIEWER** focuses on: default-path byte-identity (the prime directive), the two-phase ordering correctness (§3.7), the legacy-vs-options conflict surface, and the `params` import boundary.
4. **QA** validates the §9 acceptance criteria + the gating suite.

HANDOFF_TO: BACKEND
