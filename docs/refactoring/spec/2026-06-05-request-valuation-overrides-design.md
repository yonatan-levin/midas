# Request-Driven Valuation Parameter Overrides — Design Spec

- **Date:** 2026-06-05
- **Status:** Design approved; implementation plan pending (`writing-plans`).
- **Owner area:** `internal/services/valuation`, `internal/api/v1/handlers`
- **Engine baseline:** CalculationVersion 4.4

> **Spec-location convention (read before adding new specs).** Midas specs live in
> the project docs tree — `docs/refactoring/spec/` for engine/architecture specs
> (this folder), `docs/reviewer/` for review trackers, `docs/refactoring/archive/`
> for closed specs/plans. **Do NOT put project specs in `docs/superpowers/`** — that
> path is the generic brainstorming-skill default and is considered **obsolete** for
> this repo. The brainstorming/`writing-plans` skills' default output location is
> overridden here. See `docs/FEEDBACK-LOG.md` (2026-06-05 entry).

---

## 1. Context & problem

A live valuation of **MU (Micron)** surfaced a `terminal_dominance` warning
(`terminal_pv is 107.0% of enterprise_value`). Investigating it exposed a deeper
structural issue the user wants fixed: **valuation knobs are configured in three
competing places, and only some are actually wired:**

1. **Hardcoded literals** — e.g. the 3% terminal-growth cap in
   `service.go::calculateTerminalGrowthRate` (`maxTerminalGrowth := 0.03`), and the
   growth estimator's `Stage3Years = 0` in `DefaultEstimatorConfig`.
2. **Viper config** — `ValuationConfig.DefaultTerminalGrowthCap`
   (`config.go:266`, default 0.03) — which the terminal-growth calc **does not read**
   (it's effectively dead for that purpose), plus `DCFProjectionYears`,
   `DCFMaxGrowthRate`, `DCFMinGrowthRate`.
3. **`AssumptionProfile`** — `HorizonYears`, `TerminalMethod`, `TerminalMultiple`,
   resolved per ticker from `config/assumption_profiles.json`.

Because the same knob (e.g. terminal growth) is owned by a literal while another
(e.g. horizon) is owned by the profile and a third (max growth) by config, the
answer to "can I configure X, and is it honored?" is per-knob archaeology. A user
who lowered `default_terminal_growth_cap` saw **no effect** — the value was not
wired. That is the bug this design eliminates.

The current per-request override seam (`valuation.ValuationOptions`) carries only
`OverrideBeta` and `OverrideRiskFree`. It is threaded handler → service → DCF, is
range-validated, is pinned into the replay artifact bundle, and bypasses the cache.
That seam is the right extension point; it is simply too narrow.

## 2. Goal

Let a **request body** set the **absolute value of every valuation math knob**, with
the value **actually wired** through the engine and **validated**. Resolution must be
explicit and auditable, and the **default (no-override) path must stay byte-for-byte
identical** to today so the load-bearing invariants (DDM bit-for-bit, replay
baselines) hold.

## 3. Decisions (locked with the user)

| # | Decision | Choice |
|---|----------|--------|
| D1 | **Scope** of overridable knobs | **Valuation math knobs only** (DCF/WACC/growth). No infra/auth/cache/data-source config. |
| D2 | **Semantics** of an override | **Absolute values**, each validated. (Not multipliers.) |
| D3 | **Invariant violations** | **Reject with HTTP 422 + clear reason.** No silent clamping. |
| D4 | **Single-ticker transport** | **New `POST /api/v1/fair-value/{ticker}`** with an `options` body. `GET` stays unchanged. |
| D5 | **Architecture** | **Full effective-config resolver** — one `Resolve()` merges `config < profile < override` for every knob. |
| D6 | **Negative values** | **Allowed where economically real** (terminal growth, revenue growth, beta, tax, risk-free). Static ranges are fat-finger rails, not economic gates. |

## 4. Architecture — the resolver

### 4.1 New package `internal/services/valuation/params/`

```
params/
  params.go        // EffectiveValuationParams struct + named default constants
  resolve.go       // Resolve(...) precedence merge + invariant validation
  errors.go        // ParamError (typed; carries knob + reason) → mapped to 422
  resolve_test.go  // precedence table tests + per-invariant 422 tests
```

Import-clean (no `models`/`entities` cycles), mirroring the `profile` package
boundary; enforced by an import-boundary test.

### 4.2 `EffectiveValuationParams`

A flat struct of **resolved, non-pointer** values — the single source of truth the
engine reads. Every field is accompanied by a `Source` tag (`default` | `profile` |
`request`) captured in a parallel `Provenance` map for the `applied_overrides`
response echo (§8).

### 4.3 Precedence

```
config defaults   <   AssumptionProfile   <   request overrides
```

Each knob resolves independently. A nil override falls to the profile value; a
profile that doesn't carry the knob falls to config; config that doesn't carry it
falls to a **named default constant relocated into `params`** (e.g.
`DefaultTerminalGrowthCap = 0.03`, `DefaultStage3Years = 0`). This relocation is the
core fix: after it, **every knob has exactly one resolution path**, and "the 3%
literal" no longer hides in a function body.

### 4.4 Where `Resolve()` is called

At the top of `performValuation` (and the alt-model path `performAlternativeValuation`),
**after** WACC and the growth-rate length are known (because two invariants depend on
them). The resolved `EffectiveValuationParams` is then read by every downstream site.

## 5. Knob catalog (scope = valuation math only)

Static range = **outer sanity rail** (catch unit errors like `50` vs `0.05`); the
**hard invariant** column is the real safety. Negatives are permitted where real.

| Knob | `options` JSON field | Source (low→high) | Static range | Hard invariant (→422) |
|------|----------------------|-------------------|--------------|------------------------|
| Terminal growth rate | `terminal_growth_rate` | const default ← override | `[-0.20, 0.50]` | **< WACC** (strict; warn if within 1% of WACC) |
| Terminal growth cap (auto-derive path) | `terminal_growth_cap` | config `DefaultTerminalGrowthCap` ← override | `[-0.20, 0.50]` | ignored when explicit `terminal_growth_rate` set |
| Horizon years | `horizon_years` | `profile.HorizonYears` ← override | `[1, 50]` | **≤ stage1+stage2+stage3** — fires as 422 **only when `horizon_years` is request-sourced**; a profile-sourced horizon keeps today's clamp+WARN so the default path stays byte-identical |
| Growth stage years | `growth_stages.{stage1,stage2,stage3}_years` | estimator default `3/4/0` ← override | each `[0, 50]` | `sum ≥ 1` AND `sum ≥ horizon_years` |
| Max growth rate | `max_growth_rate` | config `DCFMaxGrowthRate` ← override | `[-1.0, 10.0]` | **≥ min_growth_rate** |
| Min growth rate | `min_growth_rate` | config `DCFMinGrowthRate` ← override | `[-1.0, 10.0]` | **≥ -1.0** (revenue can't shrink past 0) AND `≤ max` |
| Terminal method | `terminal_method` | `profile.TerminalMethod` ← override | enum `{gordon_growth, exit_multiple}` | `exit_multiple` ⇒ a terminal multiple is resolvable |
| Terminal multiple | `terminal_multiple` | industry EV/EBITDA lookup ← override | `[0, 100]` | required if method=`exit_multiple` and no industry default |
| Tax rate | `tax_rate` | `latest.TaxRate` / config ← override | `[-0.5, 1.0]` | — (negative effective rates real: NOLs/credits) |
| Beta | `beta` | market data / `OverrideBeta` ← override | `[-5, 5]` | — (negative-beta names real) |
| Risk-free rate | `risk_free_rate` | macro / `OverrideRiskFree` ← override | `[-0.05, 0.25]` | — (negative nominal RF real: EUR/JPY/CHF regimes) |
| Market risk premium | `market_risk_premium` | macro/config ← override | `[0, 0.30]` | — (ERP `≥ 0`; a negative equity premium is nonsensical) |

`beta` and `risk_free_rate` move under the same `options` object for a uniform
surface; the legacy `GET` query params (`override_beta`, `override_rf`) and the legacy
top-level bulk fields remain accepted and map into the same override input. **If both a
legacy field and its `options` twin are set, that is a 422 conflict** (explicit, not
silently resolved).

## 6. Consumption sites rewired

Each scattered read becomes a read of the resolved params:

| Site | Today | After |
|------|-------|-------|
| `service.go:806/1721` `calculateTerminalGrowthRate` | hardcoded 3% cap + WACC−2% clamp | reads `params.TerminalGrowthRate`; auto-derive helper still exists for the default path but its cap comes from `params`, and an explicit override is honored as-is subject to the `< WACC` invariant |
| `service.go:1105–1129` horizon block | `profile.HorizonYears` + **silent clamp** to growth length | `params.HorizonYears`; clamp→422 invariant **only for request-sourced horizon**; profile-sourced retains the clamp+WARN (default-path preservation) |
| `service.go:107` estimator construction | shared `s.growthEstimator` from `DefaultEstimatorConfig()` | when override present, build a **per-request** estimator from `params.Stage{1,2,3}Years` + `params.{Max,Min}GrowthRate`; otherwise reuse the shared one (fast path) |
| `service.go:747–748` WACC inputs | `macroData.MarketRiskPremium`, beta/rf | `params.MarketRiskPremium`, `params.Beta`, `params.RiskFreeRate` |
| `service.go:1169–1180` exit-multiple block | industry-config lookup only | `params.TerminalMethod` / `params.TerminalMultiple` (industry lookup is the default source) |
| `service.go:1146` tax | `latest.TaxRate` | `params.TaxRate` — the same override also applies to the WACC after-tax cost of debt and the alt-model `ModelInput.TaxRate`; all three move together for coherence |

## 7. API surface

### 7.1 Routes
- **New** `POST /api/v1/fair-value/{ticker}` — body `{"options": ValuationOverrides}` → `FairValueResponse`.
- `BulkFairValueRequest` gains `options ValuationOverrides` (applies to all tickers).
- `GET /api/v1/fair-value/{ticker}` — unchanged; `override_beta`/`override_rf` query params still work.

### 7.2 Transport DTO `ValuationOverrides`
All fields `*pointer` with `omitempty`; lives in the handler/transport layer and is
translated into the resolver's override input (keeps the wire format out of the
domain). Example:

```json
{
  "options": {
    "terminal_method": "exit_multiple",
    "terminal_multiple": 14.0,
    "horizon_years": 5,
    "terminal_growth_rate": -0.01,
    "growth_stages": { "stage1_years": 3, "stage2_years": 2, "stage3_years": 0 }
  }
}
```

### 7.3 Validation — two layers, both → RFC 7807 `422`
- **Layer 1 (handler, static):** per-knob range + enum checks before any work; field-named Problem Details (extends the existing beta/rf checks at `fair_value.go:330`).
- **Layer 2 (resolver, cross-knob):** invariants needing computed WACC / growth length; returns a typed `ParamError` (never a panic), mapped to 422 by the handler.

## 8. Response transparency

`FairValueResponse` gains an `applied_overrides` object: for each knob the request
touched, the **final value** and its **source layer** (`request`), plus any knob the
resolver applied. **v1 scope:** echo only the knobs the request explicitly set (each tagged `source: "request"`); echoing profile-/default-sourced knobs the caller merely asked about is deferred. Provenance rides on `entities.ValuationResult` as an additive `omitempty` field. This is the
direct cure for "was my value honored?" — the caller never has to guess.

## 9. Caching & replay

- A request carrying **any** override **bypasses the cache** (read + write) — keeps the
  `valuation:v4:TICKER` cache uncontaminated; matches today's override-bypass behavior.
  (Cache-keying on a param hash was considered and rejected as scope creep.)
- The replay artifact bundle's pinned override map widens to the full resolved param
  set, so a captured bundle replays deterministically.

## 10. Bit-for-bit invariant protection

The chosen full-resolver approach has exactly one danger: a behavior shift on the
default path. The design forecloses it.

- **`Resolve()` with empty options MUST return values byte-identical to today's
  config+profile+literal resolution.** This is a hard requirement and the central
  test target.
- **Gating tests (must stay green at every commit):**
  - `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC `math.Float64bits` equality).
  - `cmd/replay --diff-stages` vs `artifacts/tier2-baseline/` (default params).
  - `TestRecomputeUmbrellas_NoMutation`, `TestOrchestrator_LedgerOrdering`.
- **New tests:**
  - Resolver precedence table: for each knob, assert `config < profile < override`.
  - One test per hard invariant returning 422 with the right reason.
  - End-to-end "the number actually moved": `POST /fair-value/MU` with
    `terminal_method=exit_multiple` + `horizon_years=5` → assert
    `dcf_terminal_pct_of_ev` drops vs the default `GET`, and `applied_overrides`
    echoes the request.
  - Parity: `POST` with empty `options` ≡ `GET` (same response modulo `applied_overrides`).
- **No `CalculationVersion` bump** on the default path (behavior identical). Overridden
  responses are non-canonical by construction (and cache-bypassed).

## 11. Out of scope (YAGNI)

Multipliers/weights, infra/auth/data-source config, per-knob multiplier syntax,
cache-keying on overrides, any UI, persisting override presets.

## 12. Open risks

- **Per-request estimator cost:** building an estimator per overridden request is
  cheap (config + logger + emitter, no I/O), but the plan must confirm no hidden
  shared state. Default path keeps the shared estimator.
- **Conflict surface:** legacy-field-vs-`options` conflict handling (422) must be
  covered explicitly so back-compat callers don't get surprised.
- **`terminal_growth_rate < WACC` is evaluated against the *computed* WACC**, which
  itself can be overridden (beta/rf/MRP). Order matters: resolve WACC inputs →
  compute WACC → then validate terminal growth against it. The plan must sequence this.

## 13. Phasing (for the implementation plan)

1. `params` package: struct + named-default constants + `Resolve()` (default-path only) + precedence tests — prove byte-identity first.
2. Rewire consumption sites to read `params` on the default path; replay/DDM green.
3. Widen `ValuationOptions`/add `ValuationOverrides` DTO + handler Layer-1 validation.
4. Resolver Layer-2 invariants + 422 mapping.
5. `POST /fair-value/{ticker}` + bulk `options` + OpenAPI/Swagger + `applied_overrides`.
6. End-to-end override tests + cache-bypass + artifact-pin widening.
