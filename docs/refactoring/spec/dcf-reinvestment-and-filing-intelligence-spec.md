# DCF Reinvestment / Operating-Leverage Model + Filing-Intelligence Guidance Spec

**Version:** 0.2
**Date:** 2026-06-06
**Status:** Phase 1 (Layer A) SHIPPED 2026-06-06 (`feat/dcf-reinvestment-layer-a`, CalcVersion 4.7). Phases 2–4 PLANNING — NOT STARTED. See `docs/refactoring/archive/dcf-reinvestment-layer-a-closeout.md`.
**Scope:** Two layers + a cross-cutting policy that together close the *calibration* gap left after BUG-014 + BUG-015: (A) a reinvestment / operating-leverage model inside midas's DCF so FCF can turn positive within the explicit horizon for hypergrowth, reinvestment-heavy firms; (B) an offline, accession-keyed "Filing Intelligence" tool that extracts forward CapEx / margin / revenue guidance from 10-K/10-Q MD&A prose as a provenanced, immutable, replay-captured input; and (X) an explicit assumption-authority hierarchy that governs which source supplies each valuation assumption. Layer A is mandatory and ships first; Layers B/X are sequenced behind it.

> **Provenance.** This plan synthesizes an internal analysis of the `cmd/accuracy` 4.6 baseline (`docs/accuracy/report-2026-06-05.md`) with a cross-model architecture review (gpt-5.5, high reasoning) conducted 2026-06-06. The cross-model review's headline contribution is the assumption-authority hierarchy (§9) and the determinism-boundary requirements for Layer B (§8.3); both are load-bearing and are not optional polish.

---

## Implementation Status

| Phase | Layer | What | Status |
|-------|-------|------|--------|
| Phase 1 | A | Reinvestment / operating-leverage model in midas DCF: sales-to-capital (or declining capex-intensity) trajectory, per-archetype `AssumptionProfile` reinvestment + margin-convergence parameters, accounting-consistency guardrails, golden tests, `CalculationVersion` bump. **MANDATORY, no new data.** | **SHIPPED 2026-06-06** (CalcVersion 4.7; commits `c21e1e6` + review-fix `94b529f`). AMD per-year FCF crosses positive at yr 2 (NEG_FCF_YEARS 5→1), terminal 247%→89%, basket mean gap 59.1%→53.2%; DDM/FFO/revenue_multiple bit-for-bit. Closeout: `docs/refactoring/archive/dcf-reinvestment-layer-a-closeout.md`. |
| Phase 2 | B/X | Define the guidance-artifact contract + the assumption-authority hierarchy; make midas consume a **hand-authored fixture** guidance artifact deterministically (incl. replay capture) — BEFORE any LLM exists. | **SHIPPED 2026-06-08** (`feat/layer-b-phase2-guidance-fixture`, CalcVersion UNCHANGED at 4.7 — empty `GuidanceRoot` is byte-identical to 4.7). §12.4 = midpoint-replace near-term-only; §12.5 = no bump. Validated via B-V-R-Q + live API + **gpt-5.5 cross-model review** (caught a §9.3 terminal-leak HIGH-1 the same-model gates missed). Design: `docs/refactoring/spec/layer-b-phase2-guidance-fixture-spec.md`; closeout: `docs/refactoring/archive/layer-b-phase2-guidance-fixture-closeout.md`. |
| Phase 3 | B | Build the offline filing-intelligence extraction tool (narrow scope: CapEx + explicit margin/revenue guidance only), accession-keyed, validator-computed confidence, human-in-the-loop, deterministic section extraction. | **PLANNING — NOT STARTED** |
| Phase 4 | B | *(optional, earn it)* Broaden filing-intelligence scope and/or graduate to a REST service + orchestrator integration — only if reuse demands it. | **PLANNING — NOT STARTED (gated)** |

**Dependency order:** Phase 1 → Phase 2 → Phase 3 → Phase 4. Phase 1 stands alone and delivers the biggest, most reliable accuracy improvement with no new data. Phase 2 establishes the contract + policy that Phase 3's tool produces against. Phase 4 is conditional.

---

## Table of Contents

1. [Context & Problem](#1-context--problem)
2. [Relationship to Existing Work](#2-relationship-to-existing-work)
3. [Goals & Non-Goals](#3-goals--non-goals)
4. [Root Cause — Why FCF Is Sign-Locked to the Base Year](#4-root-cause--why-fcf-is-sign-locked-to-the-base-year)
5. [Layer A — Reinvestment / Operating-Leverage Model](#5-layer-a--reinvestment--operating-leverage-model)
6. [Layer A — Per-Archetype Parameters in `AssumptionProfile`](#6-layer-a--per-archetype-parameters-in-assumptionprofile)
7. [Layer A — Accounting-Consistency Guardrails](#7-layer-a--accounting-consistency-guardrails)
8. [Layer B — Filing Intelligence (Offline Guidance Extraction)](#8-layer-b--filing-intelligence-offline-guidance-extraction)
9. [Cross-Cutting — Assumption-Authority Hierarchy](#9-cross-cutting--assumption-authority-hierarchy)
10. [Phase Plan & Sequencing](#10-phase-plan--sequencing)
11. [Testing Strategy](#11-testing-strategy)
12. [Open Questions](#12-open-questions)
13. [What Stays the Same](#13-what-stays-the-same)

---

## 1. Context & Problem

Midas's multi-stage DCF **systematically undervalues hypergrowth, reinvestment-heavy firms.** The canonical case is **AMD**; the pattern also depresses **KO** and most names. From the `cmd/accuracy` 4.6 baseline (`docs/accuracy/report-2026-06-05.md`, basket of 10):

- **8 of 10 tickers value below market** (mean gap −61.5%); mean absolute gap 59.1%.
- **6 of 10 flagged `TERMINAL_DOMINANCE`** (`dcf_terminal_pct_of_ev > 0.80`).
- AMD: intrinsic **+6.97** vs price 491.72 (−98.6%), `TERMINAL_DOMINANCE` (terminal = **247% of EV**), `NEG_FCF_YEARS` (all 5 explicit years negative).
- AAPL −52.4%, NVDA −33.1%, MSFT −21.9% — all `TERMINAL_DOMINANCE`.

The AMD signature after the BUG-014/015 fixes is diagnostic: intrinsic finally crossed positive (+6.97), but **every projected-year FCF is still negative** (`[−2.05 … −3.14] $B`) and the terminal assumption is doing 247% of the work. The explicit window contributes ~nothing; value appears *only* through the terminal multiple.

**The deeper cause is a missing concept, not a defect.** BUG-014 (cash excluded from working capital) and BUG-015 (TTM operating-income base for 10-Q-latest filers) each removed a genuine *defect*. What remains is a **calibration gap**: the projection has no notion of **operating leverage**. In reality, a scaling firm's capex-intensity *declines* as it matures and its margins *expand*, so its FCF crosses positive *within* the explicit horizon. Midas's projection cannot express that — see §4.

---

## 2. Relationship to Existing Work

This spec is the **successor to the BUG-014 + BUG-015 fixes and the VAL-1 DCF strand.** It does not re-open any of them; it builds on the foundation they established.

| Prior work | Status | Relationship to this spec |
|---|---|---|
| **BUG-014** — DCF working capital excludes cash (`docs/bugs/BUG-014-...md`) | DONE (CalcVersion 4.4 → 4.5) | Removed the cash-pollution *defect* in `calculateNetWorkingCapitalChange`. **Prior art, not in scope.** BUG-014 §8 deliberately left the `dcf.go:122` base-ΔNWC scaling untouched and named "reconsider base-ΔNWC scaling semantics" as a follow-up — **this spec is that follow-up** (subsumed into Layer A's reinvestment-term replacement). |
| **BUG-015** — TTM operating-income base for 10-Q-latest filers (`docs/bugs/BUG-015-...md`) | DONE (CalcVersion 4.5 → 4.6) | Fixed the quarterly-base sign *defect* (KO −15.52 → +15.74, AMD −21.68 → +6.85). **Prior art, not in scope.** Layer A consumes the BUG-015 TTM base as the starting NOPAT; FY-latest invariance is preserved. |
| **VAL-1** — archetype-aware horizon, cyclical normalization, terminal handling (`docs/reviewer/VAL-1-...md`) | Phase 1 RESOLVED; Phases 2-5 OPEN, gated on unified `AssumptionProfile` | VAL-1 Phase 1 shipped the 5 DCF diagnostic fields (`dcf_horizon_years`, `dcf_terminal_method`, `dcf_terminal_pct_of_ev`, `dcf_per_year_pv`, `dcf_terminal_growth_used`) — **this spec's `cmd/accuracy` oracle reads them.** Layer A advances VAL-1 Phases 2-4 (horizon + terminal already lit; reinvestment trajectory is the new strand). VAL-1's "least-broken model, calibration not correction" framing is exactly the gap this spec closes. |
| **`valuation-engine-upgrade-spec.md`** | Phases 0-4 DONE | Established the multi-stage DCF, growth estimator, `ModelRouter`, and the true-FCF (`NOPAT + D&A − CapEx − ΔWC`) formula that Layer A *replaces* for the reinvestment terms. The growth-fade and ROIC-ceiling machinery (`internal/services/growth/estimator.go`, `pkg/finance/growth/sustainability.go`) is reused: Layer A's `Reinvestment = (Growth / ROIC) × NOPAT` form is the dual of the existing sustainable-growth ceiling. |
| **AssumptionProfile system (Tier 2)** (`internal/services/valuation/profile/`, `config/assumption_profiles.json`; `docs/refactoring/archive/assumption-profile-spec.md`, `docs/refactoring/spec/assumption-profile-db-backed-future.md`) | DONE (31 profiles + 19 rules) | **The natural home for per-archetype reinvestment-trajectory + margin-convergence parameters.** Layer A extends `AssumptionProfile` with new fields and `config/assumption_profiles.json` with values (§6). The `(archetype × maturity)` resolution, `ConfigHash` discipline, and `ResolvedSnapshot` replay capture all apply unchanged. |
| **AIProvenance pattern (DC-1)** (`internal/core/entities/adjustment_ledger.go:115`, `internal/services/datacleaner/adjustments/hash.go`) | DONE | midas's one existing LLM call already records `{ModelName, PromptHash, SourceDocHash, ExtractedSpan, Probability, Confidence, Timestamp}` as SHA-256 digests. **Layer B's guidance-artifact `ai_provenance` block (§8.2) mirrors this exact pattern** — pre-call canonical-prompt hashing, source-doc hashing, no wall-clock in the hash. |
| **`cmd/accuracy` harness + 2026-06-05 4.6 baseline** (`cmd/accuracy/`, `docs/accuracy/report-2026-06-05.md`, `docs/accuracy/baseline-capture-runbook.md`) | DONE | **The regression oracle for Layer A.** Hermetic, read-only; reads `17-response.json` + `14-model-selection.json` per bundle. Success criteria for Layer A are stated against its flag taxonomy (`TERMINAL_DOMINANCE`, `NEG_FCF_YEARS`, `EXTREME_GAP`) and mean-gap metric (§5.7, §11). |
| **`cmd/replay` tooling** (`cmd/replay/`, `internal/observability/replay/`) | DONE (R0-R3 SHIPPED) | Hermetic bit-for-bit replay over captured bundles. **Layer A golden/replay tests run through it (`--from=parsed --diff-stages`).** Layer B guidance artifacts are captured *into* the replay bundle so a valuation that consumed guidance replays deterministically (§8.4). |
| **Strade `docs/TOOL_REGISTRY.md`** (`../../docs/TOOL_REGISTRY.md` at the Strade parent) | — | **Where Layer B would register** *if* Phase 4 graduates it to a service. As an offline artifact-producing tool (Phases 2-3) it is *not* a registered orchestrator tool; it produces files midas consumes. Registration is a Phase-4 decision (§8.6, §10). |

---

## 3. Goals & Non-Goals

### Goals

1. **(Layer A)** Make midas's DCF express **operating leverage** so projected FCF can cross from negative to positive *within* the explicit horizon for genuinely reinvestment-heavy firms — decoupling the *sign* of projected FCF from the base year.
2. **(Layer A)** Parameterize the reinvestment trajectory + margin-convergence path **per archetype** in `AssumptionProfile`, kept coarse — calibrate by company *shape*, never per ticker.
3. **(Layer A)** Add **accounting-consistency guardrails** so the new freedom cannot produce implausible early FCF, runaway margins, or terminal inconsistency.
4. **(Layer B)** Stand up an **offline, accession-keyed, human-in-the-loop** tool that extracts forward CapEx / explicit margin / revenue guidance from MD&A prose into **immutable, content-addressed, provenanced** artifacts.
5. **(Layer B)** Have midas consume guidance as a **captured, immutable, replay-pinned input** — **never** a live LLM call in the valuation hot path.
6. **(Cross-cutting)** Define an explicit **assumption-authority hierarchy** so the system is not merely deterministic but financially *sound*: every valuation records which source supplied each assumption, and AI guidance *anchors* near-term assumptions without dominating intrinsic value.

### Non-Goals

- **Does NOT re-open BUG-014 or BUG-015.** The cash-exclusion NWC code and the TTM-OI base are inputs to Layer A, not subjects of it.
- **Layer A is DCF-path only.** No changes to the DDM, FFO, or revenue_multiple *base* logic. The bit-for-bit DDM invariant (`TestDDM_LegacyPath_BitForBit`) and the FFO / revenue_multiple primary values must be unaffected.
- **Layer B is NOT a live dependency of valuation** and does **not** replace Layer A. With no guidance artifact (the common case), valuation falls back to the Layer-A modeled trajectory and produces a complete, defensible result.
- **No Monte Carlo / probabilistic DCF.** Deterministic projection only (sensitivity is a separate, additive concern).
- **No broad semantic RAG over filings** (§8.5 explains why deterministic section extraction is preferred).
- **Phase 4 is not committed.** Graduating Layer B to a service or registering it in `TOOL_REGISTRY.md` is conditional on demonstrated reuse.

---

## 4. Root Cause — Why FCF Is Sign-Locked to the Base Year

Confirmed in `pkg/finance/dcf/dcf.go` (`CalculateDCF`, projection loop lines ~100-165). In the `UseTrueFCF` branch, **every** cash-flow component is scaled by the **same cumulative operating-income growth factor**:

```go
// pkg/finance/dcf/dcf.go ~117-124 (current)
growthFactor := currentOperatingIncome / inputs.BaseOperatingIncome  // cumulative OI growth
scaledDA       := inputs.DepreciationAndAmortization * growthFactor
scaledCapEx    := inputs.CapitalExpenditures        * growthFactor
scaledNWCChange:= inputs.NetWorkingCapitalChange    * growthFactor
freeCashFlow = nopat + scaledDA - scaledCapEx - scaledNWCChange
```

Because NOPAT, D&A, CapEx, and ΔWC all ride the *same* `growthFactor`, the per-year FCF is algebraically:

```
FCF_t = growthFactor_t × (NOPAT_base + D&A_base − CapEx_base − ΔWC_base)
      = growthFactor_t × FCF_base
```

The bracketed term is a **constant** (the base-year FCF). The entire projection is `FCF_t = growth_factor_t × FCF_base`.

**Consequence — the sign is structurally locked to the base year.** If `FCF_base < 0` (a firm investing ahead of earnings, the *definition* of a scaling reinvestment-heavy firm), then every projected `FCF_t < 0`, and because `growthFactor_t` increases monotonically, each year is **more** negative than the last. The explicit window can only contribute negative value; positive value can appear **only** through the terminal assumption. That is precisely the AMD signature: intrinsic +6.97, per-year FCF `[−2.05 … −3.14] $B`, terminal = 247% of EV.

**The missing concept is operating leverage.** A scaling firm does not reinvest a fixed fraction of a growing base forever — its **capex-intensity declines** toward a mature-industry norm and its **margins expand** as fixed costs are absorbed. Reinvestment should be tied to *incremental* growth and a *changing* efficiency, not to the cumulative growth of the base year's snapshot. Layer A replaces the proportional scaling with a reinvestment model that can turn FCF positive in-window.

---

## 5. Layer A — Reinvestment / Operating-Leverage Model

> **Mandatory, ships first, needs no new data.** This is the largest and most reliable accuracy improvement available, and it depends only on data already in `entities.FinancialData` / the cleaned views.

### 5.1 The change in one sentence

Replace the proportional `× growthFactor` scaling of CapEx/ΔWC (and, optionally, D&A) with a **unified reinvestment term** whose efficiency *improves over the horizon*, so that `FCF_t = NOPAT_t − Reinvestment_t` can cross positive within the explicit window.

### 5.2 Recommended formulation — Damodaran sales-to-capital

Project a single **reinvestment** term rather than four separately-scaled components:

```
Reinvestment_t = (Revenue_t − Revenue_{t−1}) / SalesToCapital_t
FCF_t          = NOPAT_t − Reinvestment_t
```

where `SalesToCapital_t` **improves** (rises) over the horizon from a low starting value toward a mature-industry norm. Equivalently, by the fundamental growth identity (`g = ROIC × ReinvestmentRate`), this is the dual of the sustainable-growth machinery already in `pkg/finance/growth/sustainability.go`:

```
Reinvestment_t = (Growth_t / ROIC_t) × NOPAT_t
```

Either parameterization expresses the same economics: the firm spends to grow, the *efficiency* of that spend improves, and as growth fades and efficiency rises, `Reinvestment_t` falls below `NOPAT_t` and FCF turns positive.

### 5.3 Why a unified reinvestment term (the trade-off)

| Approach | Interpretability | Risk |
|---|---|---|
| **Separately project NOPAT / D&A / CapEx / ΔWC** (today's shape) | Low — four independently-scaled series can drift into accounting-incoherent combinations | **High — the "hybrid trap":** naive tapering of CapEx while D&A and ΔWC still scale mechanically can manufacture implausibly *high* early FCF (D&A add-back outruns shrinking CapEx). This is the failure mode the cross-model review flagged most strongly. |
| **Unified reinvestment term** (recommended) | High — one number, one curve, one economic story (`Reinvestment = capital deployed to fund this year's growth`) | Lower — the relationship between growth, ROIC/sales-to-capital, and reinvestment is enforced by construction; D&A is implicitly inside the net reinvestment figure rather than a free add-back |

**Recommendation: the unified reinvestment term.** It is more interpretable, avoids the hybrid trap, and maps cleanly onto the existing ROIC/sustainable-growth code. The four-component path stays available as the **fallback** when revenue history or ROIC inputs are insufficient (see §5.6).

### 5.4 Alternative / simpler formulation — declining capex-intensity curve

If sales-to-capital / ROIC inputs are unreliable for a ticker, a simpler operating-leverage proxy is a **declining capex-intensity curve**:

```
CapExIntensity_t = CapEx_t / Revenue_t,  tapering linearly from intensity_base → mature_industry_norm over fade_years
CapEx_t          = CapExIntensity_t × Revenue_t
```

This is less theoretically tidy (it does not couple reinvestment to growth/ROIC) but is robust when only revenue and historical CapEx are available. **The spec recommends sales-to-capital as primary and declining-capex-intensity as the documented fallback** — see Open Question §12.1 for the final selection criteria.

### 5.5 Margin-convergence path

Operating leverage also expands *margins*. Project NOPAT not by scaling the base operating income by growth alone, but by a **margin-convergence path**:

```
OperatingMargin_t = base_margin + (target_margin − base_margin) × convergence_fraction(t)
NOPAT_t           = Revenue_t × OperatingMargin_t × (1 − TaxRate)
```

where `target_margin` and the convergence schedule are **per-archetype** (§6). For hypergrowth, margins expand over years 5-10; for mature, the path is flat (`base_margin == target_margin`). Margin expansion is **capped** by archetype/industry (§7) — no firm converges to an implausible margin.

### 5.6 Where the change lands (touch-points)

| Concern | File / function | Change |
|---|---|---|
| Reinvestment & margin projection math | `pkg/finance/dcf/dcf.go` projection loop (~lines 100-165) | Replace the `UseTrueFCF` `× growthFactor` block. New `Inputs` fields carry the per-year reinvestment curve + margin path (or the parameters to derive them) so `pkg/` stays config-free. Keep the legacy proportional path behind a flag/empty-curve fallback for bit-for-bit safety on tickers that opt out. |
| Reinvestment / margin parameters → DCF inputs | `internal/services/valuation/service.go`, `dcf.Inputs` construction (~lines 1088-1167, extending to the `dcfInputs := dcf.Inputs{...}` block ~1181-1208) | Derive the per-year reinvestment curve and margin path from the resolved `AssumptionProfile` + the (BUG-015) TTM base + revenue history + ROIC; pass them into `dcf.Inputs`. Surface the chosen curve / fallback on a calc-trace stage and `result.Warnings`. |
| Revenue series for the reinvestment term | `internal/core/entities/financial_data.go` (`HistoricalFinancialData`) + the cleaned views | Reuse `TrailingTwelveMonthsRevenue()` / the RM-1 source-tag contract for the revenue base; project revenue forward via the existing growth-rate slice. No new XBRL fields required. |
| Per-archetype params | `internal/services/valuation/profile/profile.go` (new `AssumptionProfile` fields) + `config/assumption_profiles.json` (values) | §6. |

### 5.7 Success criteria (Layer A)

Validated against `cmd/accuracy` on a **fresh CalcVersion-bumped baseline** (§11):

1. **Negative-base-FCF crosses positive in-window.** For AMD (the canonical case), the explicit-year FCF series transitions from negative to positive *before* the terminal year under the hypergrowth profile — `NEG_FCF_YEARS` clears or is materially reduced, and no year is *more* negative than the prior.
2. **Capex-intensity declines** over the horizon under the hypergrowth profile (assert the projected `CapEx_t / Revenue_t` series is monotonically non-increasing toward the mature norm).
3. **Terminal share moves to a more plausible range.** `dcf_terminal_pct_of_ev` for AMD drops from 247% toward a defensible band; the `TERMINAL_DOMINANCE` rollup across the basket shrinks. **Terminal-dominance is a diagnostic, not a hard pass/fail** (§7.4) — for genuine hypergrowth a high terminal share is legitimate; the target is that the explicit window *captures the transition to positive economics* and terminal value is no longer *compensating for broken interim FCF mechanics*.
4. **Mean absolute gap** across the basket shrinks vs the 4.6 baseline (59.1%), with the improvement concentrated on the reinvestment-heavy names (AMD, KO).
5. **Bit-for-bit preservation where intended** (§13, §11): DDM/FFO/revenue_multiple primary values unchanged; FY-latest tickers that opt out of the new path replay identically.

---

## 6. Layer A — Per-Archetype Parameters in `AssumptionProfile`

The reinvestment trajectory and margin-convergence path are calibrated **per archetype**, in the existing Tier 2 profile system. Keep archetypes **coarse**; do not overfit per company.

### 6.1 New `AssumptionProfile` fields (proposed)

Add to `internal/services/valuation/profile/profile.go::AssumptionProfile`:

```go
// Layer A — reinvestment / operating-leverage trajectory (DCF path only).
ReinvestmentMethod      ReinvestmentMethod `json:"reinvestment_method"`       // "sales_to_capital" | "declining_capex_intensity" | "legacy_proportional"
SalesToCapitalStart     float64            `json:"sales_to_capital_start"`    // starting (low) sales-to-capital for scaling firms
SalesToCapitalTarget    float64            `json:"sales_to_capital_target"`   // mature-industry norm the ratio improves toward
CapExIntensityStart     float64            `json:"capex_intensity_start"`     // fallback path: starting CapEx/Revenue
CapExIntensityMature    float64            `json:"capex_intensity_mature"`    // fallback path: mature-industry CapEx/Revenue norm
ReinvestmentFadeYears   int                `json:"reinvestment_fade_years"`   // years over which efficiency improves toward target
MaintenanceCapexFloor   float64            `json:"maintenance_capex_floor"`   // §7.1 — reinvestment may not fall below this × Revenue (or × D&A)

// Margin-convergence path.
BaseMarginMethod        BaseMarginMethod   `json:"base_margin_method"`        // "ttm" | "two_year_average" | "mid_cycle"
TargetOperatingMargin   float64            `json:"target_operating_margin"`   // archetype/industry-capped ceiling
MarginConvergenceYears  int                `json:"margin_convergence_years"`  // years over which margin expands base → target
```

`ReinvestmentMethod` and `BaseMarginMethod` are new small enums in `profile.go` alongside the existing `RevenueBaseMethod` / `TerminalMethod` / `DiscountMethod`. The `legacy_proportional` value selects the pre-Layer-A `× growthFactor` path (the bit-for-bit opt-out — see §13).

### 6.2 Per-archetype calibration (illustrative — values TBD in implementation)

| Archetype shape | Reinvestment trajectory | Margin path | Rationale |
|---|---|---|---|
| **Hypergrowth** (`hypergrowth_early`, `hypergrowth_profitable`, `cyclical_mid_cycle:high_growth` — AMD) | Low-but-**improving** sales-to-capital / steep capex-intensity taper over a long `reinvestment_fade_years` | Margin **expansion** years 5-10 toward an archetype-capped target | Captures the transition to positive economics inside the explicit window |
| **Software-like** (`software_like_scaling`, `software_like_large_scale` — MSFT, AAPL) | Moderate, asset-light; reinvestment a small fraction of NOPAT | Mild expansion toward a high software margin ceiling | Asset-light firms reach positive FCF quickly |
| **Mature** (`mature_large_scale`, `mature_dividend_tech` — KO at FY) | **Flat** sales-to-capital / flat capex-intensity (≈ legacy behavior) | `base_margin == target_margin` (no expansion) | No operating-leverage story; reinvestment ≈ maintenance |
| **Cyclical** (`cyclical_mid_cycle`, `cyclical_trough` — F, MXL) | **Mean-revert** capex-intensity to mid-cycle norm; base from a normalized (two-year / mid-cycle) revenue & margin | Mean-revert margin to mid-cycle | Avoids extrapolating a trough or a peak |

These map onto the 21 existing archetypes × 3 maturities already in `config/assumption_profiles.json`. **Calibration sourcing** (Damodaran sector sales-to-capital / margin tables vs internal back-fit) is Open Question §12.3.

### 6.3 Discipline preserved

- The `(archetype × maturity)` resolution algorithm, the 9 load-time validation invariants, the `ConfigHash` SHA-256 discipline, and the `ResolvedSnapshot` bundle-manifest field all apply unchanged. New fields get new validation invariants (e.g., `SalesToCapitalTarget ≥ SalesToCapitalStart > 0`; `0 ≤ base_margin ≤ TargetOperatingMargin ≤ industry_cap`; `ReinvestmentFadeYears ≥ 0`).
- New fields are **additive**; existing profiles default `ReinvestmentMethod` to `legacy_proportional` unless explicitly set, so any profile not yet recalibrated replays bit-for-bit.

---

## 7. Layer A — Accounting-Consistency Guardrails

The cross-model review's strongest caution: **giving the projection freedom to taper reinvestment without guardrails creates implausibly-high early FCF.** The following constraints are mandatory.

### 7.1 Maintenance-capex floor

Reinvestment may **not** fall below a maintenance-capex proxy (`MaintenanceCapexFloor`, expressed as a fraction of Revenue, or pinned to a multiple of D&A). A firm cannot grow — or even sustain — on sub-maintenance capex. Enforced in the projection loop; a clamp emits a `Warnings` entry.

### 7.2 D&A / CapEx plausibility

In the unified-reinvestment formulation D&A is implicitly netted; if the four-component fallback path is taken, the **D&A/CapEx relationship must stay plausible** — D&A must not exceed CapEx indefinitely (that implies a shrinking asset base for a *growing* firm). Assert and clamp; warn on violation. This is the specific defense against the "hybrid trap" (§5.3).

### 7.3 Margin & terminal consistency

- **Margin expansion is capped** by archetype/industry (`TargetOperatingMargin ≤ industry ceiling`).
- **Terminal reinvestment must be consistent with terminal growth AND ROIC:** at the terminal year, `terminal_reinvestment_rate = terminal_growth / terminal_ROIC`. A terminal year that grows at `g` while reinvesting *nothing* is incoherent. Derive terminal-year reinvestment from the terminal growth + terminal ROIC rather than letting the fade curve free-run past the horizon.

### 7.4 Terminal-dominance is a diagnostic, not a gate

`dcf_terminal_pct_of_ev` and the `TERMINAL_DOMINANCE` flag are **interpretation aids**, not pass/fail metrics. For a genuine hypergrowth firm the terminal share is *legitimately* high (most value is years out). The real target is: **the explicit window captures the transition to positive interim economics, and terminal value is not compensating for broken interim FCF mechanics.** Layer A's golden tests assert the *shape* (FCF crossing positive, capex-intensity declining), not a fixed terminal-share threshold.

---

## 8. Layer B — Filing Intelligence (Offline Guidance Extraction)

> **Second, separate project.** Layer B is sequenced **after** Layer A and is **not** a live dependency of valuation.

### 8.1 Shape — offline artifact-producing tool, not a service

Build Layer B as a **separate, offline, artifact-producing tool with its own repo/harness (Python)** — **not** initially an always-on REST service. The cross-model review was explicit: *the contract and boundary matter, not the service shape.* A full service is operational overkill for a single-developer system. **Graduate to a service only if reuse demands it** (Phase 4, §8.6).

**What it does:** extract **forward** CapEx / margin / revenue guidance from 10-K/10-Q **MD&A prose**. Structured XBRL carries only *historical* CapEx; forward guidance lives in MD&A free text, is typically ~1 year ahead, and is inconsistently phrased. The tool produces immutable, provenanced artifacts; midas consumes them.

### 8.2 The guidance-artifact contract

The artifact must carry **semantic meaning, not just a number.** Proposed shape (final schema in Phase 2):

```jsonc
{
  "schema_version": "1.0.0",
  "issuer":   { "ticker": "AMD", "cik": "0000002488" },
  "filing":   {
    "accession": "0000002488-26-000012", "form_type": "10-K",
    "filing_date": "2026-02-04", "period_end": "2025-12-28",
    "sec_url": "https://www.sec.gov/...", "source_doc_sha256": "…"
  },
  "source_selection": { "sections": ["Item 7 MD&A", "Liquidity and Capital Resources"],
                        "selected_text_sha256": "…" },
  "extraction": {
    "capex_guidance": {
      "value_low": 1.4e9, "value_high": 1.6e9, "currency": "USD",
      "period": "FY2026",
      "basis": { "gross_or_net": "gross", "cash_or_accrual": "cash",
                 "gaap_or_non_gaap": "gaap", "consolidated_or_segment": "consolidated" },
      "confidence": 0.82,
      "evidence": [ { "quote": "we expect capital expenditures of approximately $1.5 billion in fiscal 2026",
                      "location": "Item 7, ¶ Liquidity" } ]
    },
    "margin_guidance":  [ /* same envelope: value/period/basis/confidence/evidence */ ],
    "revenue_guidance": [ /* … */ ]
  },
  "ai_provenance": {
    "provider": "…", "model_name": "…", "model_version": "…", "temperature": 0.0,
    "prompt_sha256": "…", "schema_sha256": "…", "raw_response_sha256": "…",
    "extraction_code_git_sha": "…"
  },
  "validation": { "status": "validated", "warnings": [], "normalization_rules_version": "1.0.0" },
  "artifact_sha256": "…"
}
```

- The `ai_provenance` block **mirrors midas's existing `AIProvenance` pattern** (`internal/core/entities/adjustment_ledger.go:115`; SHA-256, pre-call canonical-prompt hash, no wall-clock in the hash).
- A `"no_explicit_guidance_found"` status form is **mandatory** — absence is information and must be cached (§8.3).

### 8.3 Determinism-boundary requirements (load-bearing)

These come directly from the cross-model review and are **non-negotiable** for replay/bit-for-bit determinism:

1. **Filing identity is ACCESSION-based** — `(CIK, accession number)`, **never** ticker/date. Tickers change; 10-K/A amendments supersede; "latest filing" is non-deterministic. The valuation receives an explicit **as-of / filing-cutoff**, and replay pins the *exact* artifact.
2. **Artifacts are IMMUTABLE + content-addressed/versioned.** A new prompt or model produces a **new** artifact (new `artifact_sha256`), never an overwrite.
3. **Cache "NO GUIDANCE FOUND" too.** Absence is a first-class, cacheable result.
4. **Deterministic CONFLICT resolution** between sources (e.g., a 10-K vs a newer 10-Q): a fixed, documented precedence (newest filing for the overlapping period wins; ties broken by form specificity).
5. **STALENESS rules** — guidance expires after the period it references; a FY2026 capex guidance is stale once FY2026 actuals exist.
6. **CONFIDENCE is computed by a deterministic VALIDATOR, never trusted from the LLM.** The validator asks: was the value/period/unit/currency explicit? does it reconcile to historical scale (capex/revenue/PP&E/D&A)? It assigns `confidence` and `validation.status`.

### 8.4 How midas consumes it

- midas reads guidance as a **provenanced, captured, immutable** input — **never** a live LLM call in the valuation hot path.
- The selected artifact (or the `"no_explicit_guidance_found"` record) is **captured into the replay bundle** (a new numbered stage file, e.g. alongside the existing `10-clean-output.json` / `15-valuation.json` / `17-response.json`), so a valuation that consumed guidance **replays bit-for-bit** through `cmd/replay`.
- Guidance feeds the assumption-authority hierarchy (§9) at level (2). It **anchors/clamps** near-term (year 1-2) assumptions and feeds sensitivity; **low-confidence or absent → fall back to the Layer-A modeled trajectory** (§9).

### 8.5 Extraction approach — deterministic sections + narrow question

- **Use deterministic section extraction** (Item 7 MD&A / "Liquidity and Capital Resources" located *by document structure*) + a **narrow question** — **NOT** broad semantic RAG. Most failures come from retrieval/chunking, not the model.
- **Human-in-the-loop** fits a personal-investor system: LLM extracts a *candidate + evidence quote* → deterministic validator normalizes + scores → **human accepts/rejects** → the accepted result is frozen into an immutable artifact.

### 8.6 LLM-extraction pitfalls the tool must defend against

- **Units / scale errors are catastrophic** ($1.5B parsed as $1.5M). The validator **must numeric-sanity-check** every extracted value against historical capex/revenue/PP&E/D&A scale.
- **Period ambiguity** — next quarter vs FY vs NTM. Require an explicit period; reject ambiguous ones.
- **CapEx hidden under many labels** — "capital investments", "additions to property & equipment", "fab construction", "capitalized software", … Section extraction + a broad-but-anchored prompt, not a single keyword.
- **Non-GAAP margins** — record the `gaap_or_non_gaap` basis; never silently mix.
- **Historical-stated-as-forward confusion** — require future-tense / forward language in the evidence quote; reject backward-looking statements.
- **Guidance is often OUTSIDE MD&A** (earnings calls, 8-K). Coverage is **incomplete** — be honest about hit rate; `"no_explicit_guidance_found"` is the expected common case, not a failure.

### 8.7 Phase-4 graduation (conditional)

Graduating Layer B to a REST service and registering it in Strade's `docs/TOOL_REGISTRY.md` (so the orchestrator can drive it) is a **Phase-4** decision, taken **only if reuse demands it** (e.g., the screener or orchestrator wants guidance on demand). Until then it is an offline file-producing tool. Registration would follow the published-contract / delegation model the Strade workspace already uses for `midas` and `algo_beta`.

---

## 9. Cross-Cutting — Assumption-Authority Hierarchy

> The cross-model review's headline point: **without an explicit override policy the system is deterministic but financially unsound.** This section is mandatory and applies regardless of whether Layer B exists yet.

### 9.1 Precedence (highest authority first)

| Level | Source | Notes |
|---|---|---|
| **1** | **User-specified scenario override** | Explicit per-request assumption (e.g., `ValuationOptions`) — analyst's deliberate input. |
| **2** | **High-confidence company-guidance artifact** (Layer B) | Only when `validation.status == "validated"` AND `confidence ≥ threshold`. Anchors near-term (year 1-2); never dominates intrinsic value (§9.3). |
| **3** | **Deterministic `AssumptionProfile` model** (Layer A) | The per-archetype reinvestment + margin trajectory. The default for the vast majority of valuations. |
| **4** | **Historical normalized fallback** | TTM / two-year / mid-cycle normalized base when profile params are absent. |
| **5** | **Conservative default** | Last-resort safe constants. |

### 9.2 Record which level supplied each assumption

**Every valuation MUST record, per assumption, which level supplied it** — surfaced in the response/trace (a new diagnostic block or per-assumption source tag, consistent with the RM-1 source-tag and VAL-1 diagnostic conventions). This makes the result auditable: an operator can see that, say, year-1 capex came from a 10-K guidance artifact while years 2-10 came from the hypergrowth profile.

### 9.3 Guardrail against "assumption laundering"

- **Numeric overrides REQUIRE direct extractive evidence.** A guidance artifact may set a number only when it carries an explicit `value` + `evidence` quote that the validator accepted.
- **Vague bullish prose is stored as qualitative context ONLY** — never silently converted to a number. ("Management is optimistic about long-term margins" is context, not a margin assumption.)
- **AI guidance anchors/clamps near-term and feeds sensitivity; it does not dominate intrinsic value.** Apply guidance to year 1-2 assumptions (and the sensitivity range), then let the Layer-A trajectory carry the rest of the horizon. Low-confidence or absent guidance → fall through to level 3 (the Layer-A modeled trajectory).

---

## 10. Phase Plan & Sequencing

### Phase 1 — Layer A reinvestment / operating-leverage model (MANDATORY)

`AssumptionProfile` fields + config values (§6); reinvestment + margin-convergence math in `pkg/finance/dcf/dcf.go` (§5); guardrails (§7); golden + replay tests (§11); deliberate `CalculationVersion` bump. No new data. Biggest, most reliable improvement.

**Tasks:**
- BACKEND: add `AssumptionProfile` fields + enums (`profile.go`) + validation invariants; add per-archetype values to `config/assumption_profiles.json` (bump `config_version`).
- BACKEND: replace the `UseTrueFCF` `× growthFactor` block in `dcf.go` with the unified reinvestment + margin path; keep `legacy_proportional` opt-out.
- BACKEND: derive the per-year reinvestment curve + margin path in `service.go` from resolved profile + TTM base + revenue history + ROIC; pass into `dcf.Inputs`; emit a calc-trace stage + `Warnings`.
- BACKEND: implement the §7 guardrails (maintenance floor, D&A/CapEx plausibility, terminal consistency).
- BACKEND: bump `CalculationVersion` at both stamp sites; update `service_test.go` version pins.
- QA: golden tests (§11.1); fresh-baseline `cmd/accuracy` run; bit-for-bit invariants green.

### Phase 2 — Guidance-artifact contract + assumption-authority hierarchy + fixture consumption (BEFORE any LLM)

Define the §8.2 contract and the §9 hierarchy; make midas consume a **hand-authored fixture artifact** deterministically, including replay capture. **No LLM exists yet** — this proves the consumption path, the precedence policy, the source-recording, and the replay bundle capture against a known-good fixture.

**Tasks:**
- ARCH: finalize the artifact JSON schema + the precedence table + the source-recording diagnostic shape.
- BACKEND (midas): a deterministic guidance-artifact loader (accession-keyed, immutable, caches "no guidance"), the assumption-authority resolver (§9), per-assumption source tagging, and replay-bundle capture of the consumed artifact.
- QA: fixture-driven tests — high-confidence fixture anchors year-1; low-confidence/absent falls through to Layer A; replay bit-for-bit with the fixture captured.

### Phase 3 — Layer B offline extraction tool

Build the narrow extraction tool (CapEx + explicit margin/revenue guidance only), accession-keyed, validator-computed confidence, human-in-the-loop, deterministic section extraction (§8). Its output validates against the Phase-2 contract.

**Tasks:**
- BACKEND (new tool, Python, own repo/harness): deterministic Item-7/Liquidity section extractor; narrow extraction prompt; deterministic validator (units/period/scale reconciliation → confidence + status); human accept/reject step; immutable content-addressed artifact writer; AIProvenance-style hashing.
- QA: layered tests (§11.3); quarantined model-quality eval set (NOT deterministic CI).

### Phase 4 — Broaden / graduate (optional, earn it)

Broaden filing-intelligence scope and/or graduate to a REST service + orchestrator integration **only if reuse demands it** (§8.7). Would register in `../../docs/TOOL_REGISTRY.md`.

---

## 11. Testing Strategy

### 11.1 Layer A golden / replay tests (must prove)

1. **Negative-base-FCF crosses positive within the explicit horizon** — an AMD-shaped fixture under the hypergrowth profile produces an FCF series that transitions negative → positive before the terminal year (and no year more negative than the prior). Must **fail on pre-Layer-A code**.
2. **Capex-intensity declines** under the hypergrowth profile — assert the projected `CapEx_t / Revenue_t` series is monotonically non-increasing toward the mature norm.
3. **Terminal share moves to a more plausible range** — AMD `dcf_terminal_pct_of_ev` drops materially from 247%; basket `TERMINAL_DOMINANCE` rollup shrinks (diagnostic, not a hard gate — §7.4).
4. **Replay stays bit-for-bit where intended** — `cmd/replay --from=parsed --diff-stages` over a re-captured baseline; FY-latest / `legacy_proportional`-opted-out tickers unchanged.
5. **Guardrails fire** — maintenance-capex floor clamp, D&A/CapEx plausibility clamp, terminal-consistency derivation each have a dedicated table-driven test + a `Warnings` assertion.
6. **Mathematical-invariant test** — if the reinvestment curve is configured flat and margin path flat (`legacy_proportional`), Layer A reproduces the pre-Layer-A result exactly (the bit-for-bit opt-out).

### 11.2 Load-bearing invariants (must stay green; do NOT regenerate goldens)

- `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC — DDM is dividend-derived; Layer A is DCF-path only).
- FFO (PLD/EQIX) and revenue_multiple (MXL) primary values bit-for-bit unchanged.
- DC-1 invariants (`TestRecomputeUmbrellas_NoMutation`, shadow snapshots byte-identical, ledger basket).
- Full `go test ./... -count=1` green after each phase.

### 11.3 Layer B tests (layer them)

Layer the deterministic parts and quarantine the model-quality part:
- Section selection (deterministic — fixture filings).
- Prompt-hash stability (canonical-request hash determinism, AIProvenance-style).
- Parsing of **stored** raw responses (no live calls in CI).
- Validator (units/period/scale reconciliation → confidence + status).
- Contract compatibility (artifact validates against the Phase-2 schema; `"no_explicit_guidance_found"` round-trips).
- Replay capture (a valuation that consumed a fixture artifact replays bit-for-bit).
- **Model quality** — a **quarantined eval set**, NOT deterministic CI (model output is non-deterministic and version-sensitive).

### 11.4 Regression oracle

`cmd/accuracy` over a fresh, CalcVersion-bumped baseline captured per `docs/accuracy/baseline-capture-runbook.md`. Compare the mean-gap metric and flag rollup against `docs/accuracy/report-2026-06-05.md` (4.6). Coverage ≥90% on new finance-module code per the CLAUDE.md standard.

---

## 12. Open Questions

1. **Sales-to-capital vs declining-capex-intensity as the primary trajectory.** §5.2 vs §5.4. Recommendation: sales-to-capital primary, declining-capex-intensity fallback when ROIC inputs are unreliable. *Decision needed:* the exact selection criterion (which inputs gate which path) and whether both ship in Phase 1 or the fallback is deferred.
2. **Surgical patch to `dcf.go` vs deeper FCFF refactor.** Is Layer A a contained replacement of the `UseTrueFCF` block (smaller blast radius, keeps the legacy path) or a deeper refactor toward a first-class FCFF projection (cleaner long-term, larger risk)? Recommendation: surgical patch in Phase 1 with the `legacy_proportional` opt-out; revisit a deeper refactor only if the patch proves unwieldy.
3. **Archetype-parameter sourcing / calibration.** Damodaran sector sales-to-capital / margin tables vs an internal back-fit against the `cmd/accuracy` basket. How to keep archetypes coarse and avoid overfitting to 10 tickers? *Decision needed* before Phase 1 config values are finalized.
4. **Year-1 anchor application from guidance.** When a high-confidence guidance artifact exists, apply it as a **midpoint** of the (`value_low`, `value_high`) range, or as a **clamp** on the modeled value, or feed the full range into sensitivity? §9.3 says anchor/clamp near-term; the exact mechanic is a Phase-2 decision.
5. **`CalculationVersion` value.** Layer A is a deliberate bump from 4.6. Confirm the target (4.7) and whether Phase 2's fixture-consumption path warrants its own bump.
6. **Terminal-share target band.** §7.4 keeps terminal dominance a diagnostic; should the golden tests assert any soft band for hypergrowth, or only the shape invariants (FCF crossing, capex-intensity declining)?

---

## 13. What Stays the Same

These systems are unchanged by this spec:

- **BUG-014 NWC code** (`calculateNetWorkingCapitalChange`) — operating NWC excludes cash; Layer A consumes its output unchanged.
- **BUG-015 TTM operating-income base** — the annualized base feeds Layer A's starting NOPAT; FY-latest invariance preserved.
- **DDM / FFO / revenue_multiple base logic** — Layer A is DCF-path only.
- **`AssumptionProfile` resolution machinery** — `(archetype × maturity)` algorithm, validation framework, `ConfigHash`, `ResolvedSnapshot`. Only new additive fields + values.
- **`cmd/accuracy` + `cmd/replay` hermeticity contracts** — both stay offline/read-only; Layer A is validated *through* them.
- **Growth estimator + ROIC sustainability** (`internal/services/growth/estimator.go`, `pkg/finance/growth/sustainability.go`) — reused; Layer A's reinvestment form is their dual.
- **The valuation hot path stays LLM-free** — Layer B is offline; midas only ever reads captured immutable artifacts.
