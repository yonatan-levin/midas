# DC-1 Phase 1 — `recomputeUmbrellas` Shadow-Mode Shim Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a pure read-and-log function `recomputeUmbrellas` invoked at the END of the cleaner pipeline that recomputes each balance-sheet umbrella (`TotalAssets`, `CurrentAssets`, `TotalLiabilities`, `CurrentLiabilities`) from its known components plus the Phase 0 plug, compares the result to the cleaner's mutated umbrella, and emits a structured WARN log on divergence — without mutating `*entities.FinancialData`.

**Architecture:** Pure-Go observer that runs once per `CleanFinancialData` call after `applyActiveAdjustments` returns (and after `createRiskWarningFlags` appends to the flag list, BEFORE `calculateQualityScore`). The function reads `result.CleanedData` and the request-scoped logger from `ctx` via `logctx.From(ctx)`. Zero downstream behavior change is enforced by construction (no field writes anywhere in `recomputeUmbrellas`) and by an integration-test snapshot that records every (ticker, period, umbrella, delta) divergence as JSON.

**Tech Stack:** Go 1.23 (toolchain 1.24.4); `go.uber.org/zap` for structured logs via `internal/observability/logctx`; `github.com/leanovate/gopter` for property-based testing; `github.com/stretchr/testify` for asserts. Foreground packages: `internal/services/datacleaner` (new file), `internal/integration` (new test).

**Master HEAD at plan authorship:** `219ad9e` (2026-05-17).
**Phase 0 SHIPPED:** merge `1640394` 2026-05-16 (plug fields + `computePlugs`).
**Estimated effort:** 3-5 days for the BACKEND worktree (one new file + one test + one integration test + 3 small doc edits); +1 day post-merge for the shadow-analysis report.

---

## Mode

MODE: PLAN_AND_CREATE
ROLE: ARCH

## Summary

Phase 1 of the DC-1 datacleaner refactor (spec at `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`). We add the **shadow-mode observability layer** that Phase 2's targeted-fix work depends on. The deliverable is a single new function — `recomputeUmbrellas(ctx, fd)` — wired in at the end of the cleaner pipeline. It recomputes each of the four balance-sheet umbrellas from `sum(known_components) + plug` and emits a WARN log every time the recomputed value disagrees with the cleaner's mutated value. Phase 1 ships **zero** mutations to `*entities.FinancialData` and zero changes to downstream behavior; the recomputed value is computed, logged, and discarded.

The point of Phase 1 is to surface — in production logs and in a per-ticker JSON snapshot — every place where the current cleaner mutates `TotalAssets` / `CurrentAssets` / `TotalLiabilities` / `CurrentLiabilities` without keeping them in sync with their typed components. That enumeration is the input to Phase 2's targeted-fix punch list (`Adjuster` interface refactor that mutates components only and recomputes umbrellas as the final step).

**Phase 1 → Phase 2 gate.** Per the spec's "Phasing & implementation sequence" table:
> *Shadow warnings analyzed across basket; expected divergences only.*

Phase 2 cannot start until the shadow-analysis report (filed post-merge) enumerates which (ticker, period, umbrella, adjuster-cluster) tuples produced divergences and concludes that none are unexpected. AAPL + MSFT bundles must show **only** timestamp drift in `17-response.json` after the merge — matching Phase 0's empirical baseline. The basket integration test records (does not assert on) divergence counts so the unknown divergence shape from Phase 0's clamp-affected periods (MXL 2017FY, EQIX 2013Q1) doesn't fail CI.

---

## Requirements

### Functional

1. New function `recomputeUmbrellas(ctx context.Context, fd *entities.FinancialData)` lives in `internal/services/datacleaner/recompute.go` (NEW file).
2. The function reads `fd` and, for each of the four balance-sheet umbrellas, computes `recomputed := sum(known_components) + plug` and compares it to the cleaner's `fd.<Umbrella>` field. On divergence beyond a fixed tolerance, the function emits a single WARN log line per divergent umbrella with structured fields enabling Phase 2 to filter by `(ticker, period, umbrella)`.
3. **The function MUST NOT mutate `fd`.** Enforced by review and by a unit test that asserts pre-call and post-call snapshots of every numeric field on `fd` are bit-for-bit identical.
4. The function uses `logctx.From(ctx)` to obtain the request-scoped logger (so each WARN inherits `request_id` / `user_id` / `key_id`). A nil context (testing-only path) MUST not panic — `logctx.From(nil)` returns a nop logger.
5. The function is called exactly once per `CleanFinancialData` invocation. The call site is in `internal/services/datacleaner/service.go`, AFTER `additionalFlags := s.createRiskWarningFlags(result.CleanedData, startTime)` (currently line 228) and BEFORE `qualityScore, qualityIssues, err := s.calculateQualityScore(...)` (currently line 232). Insertion point is the line immediately before `// Calculate quality score` (~line 231).
6. A property test (`gopter`) asserts: for any well-formed input (`umbrella >= sum(known_components)`, plugs in plug-invariant state), `recomputeUmbrellas` emits ZERO WARN log lines.
7. An integration test runs the FULL cleaner pipeline against captured replay bundles for the 10-ticker basket and records (does NOT assert on) every divergence as structured JSON for Phase 2's analysis report.

### Non-functional

- **Zero downstream behavior change.** No `fd.*` field write anywhere in the new file. Empirically verified via `cmd/replay --from=parsed --diff-stages` against AAPL + MSFT bundles — `17-response.json` shows ONLY timestamp drift (`as_of` / `calculated_at` / `market_data_date`).
- **Zero performance regression.** The function is four `(sum, compare, optional-log)` triples per `CleanFinancialData` call. The compare path is six float subtractions and four comparisons; the log path fires only on divergence (rare in well-formed data). Phase 0's measured parser-side overhead was sub-microsecond; this is the same shape.
- **Determinism.** No clock, RNG, or external I/O beyond the optional WARN log emission.
- **Log-volume safety.** Each WARN line is fired at most once per umbrella per request (4 max per `CleanFinancialData` call). On a clamp-affected ticker like MXL the worst case is 2 WARN lines per request (TotalAssets and CurrentAssets diverge under A1/A2/A5 mutation pattern). The default zap encoder is JSON; volume is well below the existing `99-narrate.jsonl` Tier-1 stream.

### Constraints

- **MUST NOT modify** `internal/services/datacleaner/adjustments/*.go`. The 4 `data.TotalAssets -=` mutations in `assets.go` (lines 69, 157, 232, 308) and the `data.TotalDebt += result.Amount` orchestrator in `liabilities.go:87-88` are Phase 2's job. Phase 1 only observes their effects.
- **MUST NOT introduce** `Adjuster` interface, `LedgerEntry`, `OverlaySpec`, `AdjusterOutput`, `CleanedFinancialData`, `Restated()`, or `InvestedCapital()`. Those are Phases 2-3.
- **MUST NOT migrate** any consumer of `data.TotalAssets`. Phase 4.
- **MUST NOT close** the SQLite persistence gap (`TestFinancialDataRepository_PlugFields_PersistenceGap` flip-gate stays as-is). Phase 1's recompute uses fields that are present on the in-flight `*FinancialData` from the parser through the cleaner — no SQLite involvement.
- **MUST NOT change** the public OpenAPI surface. The function is internal-only; nothing surfaces on `FairValueResponse`.
- **MUST use** `logctx.From(ctx)` for the warning logger, per CLAUDE.md "Code Style" convention. Reserves the singleton `*zap.Logger` for startup/scheduler/non-request paths.
- **MUST stay clear of** valuation/ files (Tier 2 P1-P4 worktrees may still be live; check `git worktree list` before dispatching).

---

## Architecture decisions for Phase 1

### A. Where the call site lives

The call site goes in `internal/services/datacleaner/service.go::CleanFinancialData`, AFTER the asset/liability/earnings adjusters run and AFTER `createRiskWarningFlags` appends additional flags, but BEFORE `calculateQualityScore` reads the cleaned data to compute the data-quality grade.

**Exact insertion point (line numbers as of master `219ad9e`):**

```
Line 228:    additionalFlags := s.createRiskWarningFlags(result.CleanedData, startTime)
Line 229:    result.Flags = append(result.Flags, additionalFlags...)
Line 230:
             // ← INSERT HERE: recomputeUmbrellas(ctx, result.CleanedData)
Line 231:    // Calculate quality score
Line 232:    qualityScore, qualityIssues, err := s.calculateQualityScore(result.CleanedData, flags)
```

**Why here (not at the very end and not before `applyActiveAdjustments`):**

| Candidate site | Pros | Cons | Decision |
|---|---|---|---|
| Right after `applyActiveAdjustments` (between line 217 and 228) | Captures pure asset/liability/earnings adjuster mutations | Misses the rare case where `createRiskWarningFlags` could mutate (today it doesn't, but Phase 2 might add) | Reject |
| **After `createRiskWarningFlags`, before `calculateQualityScore` (after line 229)** | Captures EVERY pre-quality-score mutation; `calculateQualityScore` is read-only on `data` so the observation point is the final state | None — quality score doesn't influence balance-sheet fields | **CHOSEN** |
| At the very end (after line 301's `recordQualityFlagCount`) | Captures every mutation no matter what | `calculateQualityScore` is observed today to be read-only, but a future refactor might add a mutator; placing recompute AFTER quality scoring would silently miss those. More importantly, the artifact-bundle snapshot at line 283 (`10-clean-output.json`) fires BEFORE the very end, so any divergence we surface post-snapshot wouldn't be reconstructible from a captured bundle | Reject — placing **before** the snapshot keeps the bundle the authoritative replay surface |

The chosen site sits between the last mutator and the snapshot — every captured bundle in `10-clean-output.json` can be re-derived through `recomputeUmbrellas` and produce the same WARN set in replay.

### B. Function signature — `context.Context` not `*zap.Logger`

```go
// recomputeUmbrellas (shadow-mode, DC-1 Phase 1).
// Reads fd, recomputes each balance-sheet umbrella from sum(known components) + plug,
// and emits a WARN log per umbrella where the recomputed value diverges from
// the cleaner's mutated value. DOES NOT MUTATE fd.
func recomputeUmbrellas(ctx context.Context, fd *entities.FinancialData)
```

**Why ctx (not `*zap.Logger`):**

The handoff sketch passes `*zap.Logger` explicitly. The project convention in CLAUDE.md is:

> **Request-path logs via `logctx.From(ctx)`** — Any log line emitted during an HTTP request must go through `internal/observability/logctx.From(ctx)` so it inherits `request_id` (and `user_id`/`key_id` post-auth). Reserve the fx-provided singleton `*zap.Logger` for startup, shutdown, scheduler, and other non-request contexts.

`CleanFinancialData` is unambiguously on the request path (it's called by the datafetcher coordinator during fair-value handling). Every WARN emitted from `recomputeUmbrellas` SHOULD inherit the request_id so Phase 2's shadow-analysis tooling can correlate divergences against a specific bundle. Passing `*zap.Logger` directly would either (a) require the caller to do `logctx.From(ctx)` themselves at the call site (extra ceremony), or (b) bypass the request-scoped logger (loses request_id).

The function takes `ctx` as the first parameter (matching the project convention that all service/repository methods accept `context.Context` as first parameter, per CLAUDE.md "Code Style") and resolves the logger internally via `logctx.From(ctx)`. The function is safe to call with `nil` context — `logctx.From(nil)` returns `zap.NewNop()` so the unit test path doesn't need to thread a real context.

The function does NOT take an `*Service` receiver. It's a package-private free function in the `datacleaner` package, with no dependency on the rule engine, the AI service, or any other cleaner state. This keeps it independently unit-testable without spinning up the whole service.

### C. Tolerance constant — `divergenceTolerance = 1.0`

```go
// divergenceTolerance is the absolute USD threshold above which the recomputed
// umbrella is considered to diverge from the cleaner's value. A WARN log fires
// only when |recomputed - reported| > divergenceTolerance.
//
// Why 1.0 USD: every adjuster mutation today subtracts at least dollars (the
// smallest A2 intangible writedown still moves TotalAssets by thousands), so a
// $1 absolute tolerance never false-triggers on float accumulation noise while
// staying tight enough to catch every real adjuster mutation. Relative tolerance
// is intentionally NOT used here: the divergence we want to detect IS a fraction
// of the umbrella (A1 goodwill exclusion drives 45% of MXL's TotalAssets delta),
// so any relative tolerance would mask exactly the divergences Phase 2 needs to fix.
const divergenceTolerance = 1.0
```

**Interaction with Phase 0's clamp:** when the parser-side clamp fires (MXL 2017FY, EQIX 2013Q1), `plug == 0` even though `sum(components) > umbrella`. In that case, `recomputed = sum(components) + 0 = sum(components) > umbrella`. The divergence is real (the parser couldn't make the math balance) and SHOULD be logged — but it's a Phase 0 known case, not a Phase 2 punch-list item. To distinguish, the WARN includes a `clamp_suspected: bool` field set true when `recomputed > reported` AND the corresponding plug is exactly zero AND `sum(known_components) > 0`. Phase 2's analysis can filter on this field.

**Why NOT recompute by re-running `computePlugs` and using the resulting plug:** the spec is explicit ("the recomputed value is NEVER used"). Re-running `computePlugs` would mutate the plug fields (`computePlugs` writes `fd.OtherCurrentAssets = ...`), violating the "no mutation" invariant. Phase 1 reads the existing plug values (which the parser already wrote at the end of `parsePeriodData`) and uses them in the recompute formula. If a Phase 2 adjuster later mutates a component without re-running `computePlugs`, the resulting divergence is exactly what Phase 1 is built to surface.

### D. Warning log shape

```go
logctx.From(ctx).Warn("recomputeUmbrellas: umbrella divergence",
    zap.String("ticker", fd.Ticker),
    zap.String("period", fd.FilingPeriod),
    zap.String("cik", fd.CIK),
    zap.String("umbrella", "CurrentAssets"),            // or "TotalAssets" / "CurrentLiabilities" / "TotalLiabilities"
    zap.Float64("reported", fd.CurrentAssets),          // cleaner's mutated value
    zap.Float64("recomputed", recomputed),              // sum(components) + plug
    zap.Float64("delta", recomputed - fd.CurrentAssets),
    zap.Float64("plug", fd.OtherCurrentAssets),
    zap.Bool("clamp_suspected", recomputed > fd.CurrentAssets && fd.OtherCurrentAssets == 0),
    zap.String("phase", "DC-1-P1-shadow"),              // grep key for analysis tooling
)
```

**Field rationale:**

| Field | Purpose |
|---|---|
| `ticker` / `cik` / `period` | Phase 2 needs to correlate divergences against specific bundles |
| `umbrella` | Which of the four diverged (filter target) |
| `reported` / `recomputed` / `delta` | Magnitude and direction of the divergence |
| `plug` | Sanity check — when `plug` is large and divergence is also large, the parser was already absorbing a residual that the cleaner then made worse |
| `clamp_suspected` | Filters known-OK Phase 0 clamp-fired periods (MXL 2017FY, EQIX 2013Q1) out of Phase 2's punch list |
| `phase` | Grep key — `rg '"phase":"DC-1-P1-shadow"' artifacts/` for analysis |

**Adjuster identity is INTENTIONALLY NOT in the log.** The recompute call site fires once at the END of the cleaner pipeline; by that point multiple adjusters have run and we can't attribute a divergence to a single adjuster from the post-state. Two options were considered:

| Option | Pros | Cons | Decision |
|---|---|---|---|
| **Single end-of-pipeline recompute (chosen)** | Tiny scope; one WARN per umbrella per request; matches the spec's "pure observation" framing | Phase 2 must correlate divergences with the adjuster-order ledger by reading `result.Adjustments` (already populated by the cleaner) | **CHOSEN — minimum Phase 1 scope** |
| Per-adjuster before/after snapshot | Attributes each divergence to a specific adjuster directly | Requires plumbing recompute into the asset/liability/earnings adjusters' code paths (modifies `adjustments/*.go`, violates handoff non-goal "DO NOT modify them in Phase 1"). Adds 14× log volume per request. Bloats Phase 1 to 7-10 days. | Reject — out of Phase 1 scope |

Correlation hint for Phase 2 analysts: `result.Adjustments` (already serialized to `10-clean-trace.json` by the existing artifact-bundle snapshot at service.go:284) contains `Adjustment.FromAccount` ("Goodwill", "Inventory", "IntangibleAssets", etc.). Cross-referencing the WARN's `umbrella` field against the `FromAccount` values in the same bundle's trace gives 90% adjuster attribution without instrumenting per-adjuster.

### E. Recompute formulas (mirror `computePlugs`)

The four recompute formulas are byte-for-byte the inverse of `computePlugs`. They use the SAME component decomposition so that:
- In well-formed Phase 0 state (no cleaner mutation), `recomputed == reported` exactly.
- Any cleaner-side mutation that breaks `umbrella == sum(components) + plug` produces a divergence.

```go
// CurrentAssets:
recomputedCA := fd.CashAndCashEquivalents + fd.Inventory + fd.OtherCurrentAssets

// TotalAssets (umbrella = NCA_umbrella + CurrentAssets; NCA umbrella was clamped to 0 by computePlugs if negative):
nonCurrentAssetsRecomputed := fd.Goodwill + fd.OtherIntangibles + fd.DeferredTaxAssets + fd.OtherNonCurrentAssets
recomputedTA := nonCurrentAssetsRecomputed + fd.CurrentAssets

// CurrentLiabilities:
recomputedCL := fd.OperatingLeaseLiabilityCurrent + fd.OtherCurrentLiabilities

// TotalLiabilities:
nonCurrentLiabRecomputed := fd.TotalDebt + fd.OperatingLeaseLiabilityNoncurrent + fd.OtherNonCurrentLiabilities
recomputedTL := nonCurrentLiabRecomputed + fd.CurrentLiabilities
```

**Note on TotalLiabilities:** in production today the cleaner mutates `fd.TotalDebt` via `liabilities.go:87-88` (B1/B2/B3 add to `TotalDebt`) but does NOT touch `fd.TotalLiabilities`. So a B1 lease capitalization of $254M will produce `recomputedTL = reportedTL + 254M`, surfacing as a divergence that Phase 2 will resolve via the `Adjuster`/`OverlaySpec` split (the spec routes B1 to an Overlay, not to `TotalDebt` directly).

**Note on TotalAssets:** today `assets.go` mutates BOTH the component (`fd.Goodwill = 0.0`, `fd.OtherIntangibles -= X`, `fd.Inventory -= X`, `fd.DeferredTaxAssets -= X`) AND the umbrella (`fd.TotalAssets -= X`). In an ideal world the umbrella mutation would keep the math balanced, but the plug stays at its parser-set value, so the recompute uses the OLD plug + the NEW components and surfaces a divergence of magnitude `delta = -X` (negative meaning the cleaner mutated more than the components account for via the plug). Phase 2's fix is to stop mutating the umbrella and let `recomputeUmbrellas` propagate the component delta.

### F. What we explicitly DON'T pre-commit to

1. **WARN vs DEBUG severity.** Phase 1 emits WARN because the divergences are the explicit Phase 2 punch list — they SHOULD be visible. After Phase 2 lands and the punch list is closed, severity can drop to DEBUG (or the call site can be removed entirely). Leave that decision open.
2. **In-bundle artifact emission.** A future enhancement could write divergences to a `13-recompute-shadow.jsonl` artifact in the request's bundle. Out of scope for Phase 1 — the existing `99-narrate.jsonl` stream is sufficient for analysis. Add only if the post-merge shadow-analysis report shows the WARN volume swamps the narrate stream.
3. **Per-adjuster attribution.** As discussed in §D, attribution is deferred to Phase 2's per-adjuster ledger. Don't add it to Phase 1.
4. **Replay artifact diff target.** Today the replay tool's `--diff-stages` walks `10-clean-output.json` etc. but does NOT walk the warn lines in `99-narrate.jsonl`. Adding a `--diff-warns` mode is OUT OF SCOPE; the shadow analysis runs by direct `rg` over the captured `99-narrate.jsonl` files in the basket bundles.

---

## File Structure

Files created or modified by this plan:

| Path | Change | Responsibility |
|---|---|---|
| `internal/services/datacleaner/recompute.go` | Create | `recomputeUmbrellas(ctx, fd)` shadow-mode observer; the `divergenceTolerance` constant; `umbrellaTriple` helper |
| `internal/services/datacleaner/recompute_test.go` | Create | Property test (gopter, 4 properties × 200 iterations, pinned seed `20260517`) + table tests for divergence emission, no-mutation, clamp_suspected flag, nil-fd safety |
| `internal/services/datacleaner/service.go` | Modify (1 line + comment block) | Insert `recomputeUmbrellas(ctx, result.CleanedData)` after `createRiskWarningFlags` (line 229), before `calculateQualityScore` (line 231) |
| `internal/integration/datacleaner_recompute_shadow_test.go` | Create | Runs full cleaner pipeline against the 10-ticker basket bundles; records every divergence as JSON; uses recording-not-asserting strategy (see §Tests) |
| `internal/integration/testdata/recompute-shadow/.gitkeep` | Create | Empty placeholder; the test writes per-ticker `<TICKER>.json` snapshots here that BACKEND commits as the Phase 1 → Phase 2 input data |
| `CLAUDE.md` | Modify (Common Gotchas DC-1 entry) | Note Phase 1 SHIPPED + shadow-mode pattern note + cross-ref to shadow-analysis report |
| `TESTING.md` | Modify (plug-invariant subsection at line 174-184) | Extend with the recompute-side companion pattern; cross-ref the integration test |
| `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` | Modify (Change log) | Append Phase 1 completion row at the bottom of the table |
| `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md` | Modify (status + progress paragraph) | Bump status note for Phase 1, cross-ref the closeout report |
| `docs/THESIS.md` | Modify (DC-1 phase row) | Status bump to "Phase 1 SHIPPED YYYY-MM-DD" |

**Post-merge follow-ups (NEW files, separate PR after analysis):**

| Path | Change | Responsibility |
|---|---|---|
| `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md` | Create | The shadow-analysis report — Phase 2's input data, structured per §"Test plan for shadow-analysis follow-up" below |
| `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-closeout.md` | Create | Mirror Phase 0's closeout format; what landed, what's deferred, lessons learned |

---

## API Contracts

Phase 1 has **NO API contract changes**.

- `recomputeUmbrellas` is a package-private function in `internal/services/datacleaner`. Not exported.
- The function signature `(ctx context.Context, fd *entities.FinancialData)` returns nothing — pure side-effect (log emission).
- No new entity fields, no new methods on `*FinancialData`, no new types.
- No changes to `FairValueResponse` or `docs/openapi.yaml`.
- The replay bundle's `10-clean-output.json` shape is unchanged (no new keys serialized).
- The `99-narrate.jsonl` stream gains zero new phase names; the new WARN lines use zap's default WARN severity which is already part of the stream's surface.

---

## Module Descriptions

### `internal/services/datacleaner/recompute.go` (new)

A small package-private observer file. One package-private function, one helper, one constant. Pure-Go arithmetic — no I/O beyond optional WARN log emission via `logctx.From(ctx)`. No imports from `internal/services/datacleaner/adjustments` (the recompute reads `fd` directly, not through any adjuster interface). No package-level state.

The file structure:

```
package datacleaner

import (
    "context"

    "go.uber.org/zap"

    "github.com/midas/dcf-valuation-api/internal/core/entities"
    "github.com/midas/dcf-valuation-api/internal/observability/logctx"
)

// divergenceTolerance is the absolute USD threshold above which the recomputed
// umbrella is considered to diverge from the cleaner's reported value. See
// implementation-plan §C for the rationale (no relative tolerance; $1 absolute).
const divergenceTolerance = 1.0

// recomputeUmbrellas (shadow-mode, DC-1 Phase 1) reads fd and, for each of the
// four balance-sheet umbrellas (CurrentAssets, TotalAssets, CurrentLiabilities,
// TotalLiabilities), recomputes umbrella = sum(known_components) + plug and
// emits a structured WARN log line when the recomputed value diverges from the
// cleaner's mutated value beyond divergenceTolerance.
//
// MUST NOT mutate fd. Phase 1 of DC-1 ships this as observability only.
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md.
func recomputeUmbrellas(ctx context.Context, fd *entities.FinancialData) {
    if fd == nil {
        return
    }
    logger := logctx.From(ctx)

    // --- CurrentAssets ---
    recomputedCA := fd.CashAndCashEquivalents + fd.Inventory + fd.OtherCurrentAssets
    emitIfDiverged(logger, fd, "CurrentAssets", fd.CurrentAssets, recomputedCA, fd.OtherCurrentAssets)

    // --- TotalAssets ---
    nonCurrentAssets := fd.Goodwill + fd.OtherIntangibles + fd.DeferredTaxAssets + fd.OtherNonCurrentAssets
    recomputedTA := nonCurrentAssets + fd.CurrentAssets
    emitIfDiverged(logger, fd, "TotalAssets", fd.TotalAssets, recomputedTA, fd.OtherNonCurrentAssets)

    // --- CurrentLiabilities ---
    recomputedCL := fd.OperatingLeaseLiabilityCurrent + fd.OtherCurrentLiabilities
    emitIfDiverged(logger, fd, "CurrentLiabilities", fd.CurrentLiabilities, recomputedCL, fd.OtherCurrentLiabilities)

    // --- TotalLiabilities ---
    nonCurrentLiab := fd.TotalDebt + fd.OperatingLeaseLiabilityNoncurrent + fd.OtherNonCurrentLiabilities
    recomputedTL := nonCurrentLiab + fd.CurrentLiabilities
    emitIfDiverged(logger, fd, "TotalLiabilities", fd.TotalLiabilities, recomputedTL, fd.OtherNonCurrentLiabilities)
}

// emitIfDiverged fires a single WARN log line when |recomputed - reported| > divergenceTolerance.
// All field names match the spec (see implementation-plan §D).
func emitIfDiverged(logger *zap.Logger, fd *entities.FinancialData, umbrella string, reported, recomputed, plug float64) {
    delta := recomputed - reported
    if delta < 0 {
        if -delta <= divergenceTolerance {
            return
        }
    } else if delta <= divergenceTolerance {
        return
    }
    clampSuspected := recomputed > reported && plug == 0
    logger.Warn("recomputeUmbrellas: umbrella divergence",
        zap.String("ticker", fd.Ticker),
        zap.String("period", fd.FilingPeriod),
        zap.String("cik", fd.CIK),
        zap.String("umbrella", umbrella),
        zap.Float64("reported", reported),
        zap.Float64("recomputed", recomputed),
        zap.Float64("delta", delta),
        zap.Float64("plug", plug),
        zap.Bool("clamp_suspected", clampSuspected),
        zap.String("phase", "DC-1-P1-shadow"),
    )
}
```

### `internal/services/datacleaner/service.go` (modify)

Insert exactly one call between line 229 (`result.Flags = append(result.Flags, additionalFlags...)`) and line 231 (`// Calculate quality score`). The diff is:

```go
     // Add additional warning flags for risky patterns
     additionalFlags := s.createRiskWarningFlags(result.CleanedData, startTime)
     result.Flags = append(result.Flags, additionalFlags...)

+    // DC-1 Phase 1 shadow-mode observability: recompute each balance-sheet
+    // umbrella from sum(known_components) + plug and emit a WARN log on
+    // divergence. Pure read; does NOT mutate result.CleanedData. The WARN
+    // stream is the input to Phase 2's targeted-fix punch list (Adjuster
+    // interface refactor). See:
+    //   docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md
+    //   docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-implementation-plan.md
+    recomputeUmbrellas(ctx, result.CleanedData)
+
     // Calculate quality score
     qualityScore, qualityIssues, err := s.calculateQualityScore(result.CleanedData, flags)
```

### `internal/services/datacleaner/recompute_test.go` (new)

Five test groups:

1. **`TestRecomputeUmbrellas_NoMutation`** — table test asserting that a cleaner-mutated `FinancialData` snapshot, after `recomputeUmbrellas` runs, is bit-for-bit identical (every float field compared via `reflect.DeepEqual` of the struct).
2. **`TestRecomputeUmbrellas_NilFD_Safe`** — `recomputeUmbrellas(ctx, nil)` and `recomputeUmbrellas(nil, fd)` both return without panic.
3. **`TestRecomputeUmbrellas_EmitsWarnOnDivergence`** — synthetic `fd` where `TotalAssets - X` was applied to the umbrella but components stay intact; asserts EXACTLY one WARN line per diverged umbrella with the correct structured fields (uses `zaptest/observer`).
4. **`TestRecomputeUmbrellas_ClampSuspectedFlag`** — `fd` where plug is exactly zero and `sum(components) > umbrella`; asserts `clamp_suspected: true` in the WARN.
5. **`TestRecomputeUmbrellas_Property_WellFormedNoDivergence`** — `gopter` property test (4 properties × 200 iterations, pinned seed `20260517`) asserting: for any well-formed `FinancialData` where the parser's plug invariant holds (components + plug == umbrella), `recomputeUmbrellas` emits ZERO WARN lines.

Property test generator shape (mirrors `plugs_test.go::TestComputePlugs_Property_ComponentsSumToUmbrellas`):

```go
// "well-formed CurrentAssets" property
prop.ForAll(
    func(cash, inventory, otherCA float64) bool {
        fd := &entities.FinancialData{
            Ticker:                 "FUZZ",
            FilingPeriod:           "2024FY",
            CIK:                    "0000000000",
            CashAndCashEquivalents: cash,
            Inventory:              inventory,
            OtherCurrentAssets:     otherCA,
            CurrentAssets:          cash + inventory + otherCA,  // invariant holds
        }
        recomputeUmbrellas(loggerCtx(observer), fd)
        return len(observer.FilterMessage("recomputeUmbrellas: umbrella divergence").All()) == 0
    },
    gen.Float64Range(0, 1e12),
    gen.Float64Range(0, 1e12),
    gen.Float64Range(0, 1e12),
)
```

Repeat for the other three umbrellas. Shrinker behavior: gopter's default shrinker for `float64Range` halves the value on failure; counterexample expectation is a triple where the WARN does fire, which would indicate a logic bug in `recomputeUmbrellas` (not a data shape we should accept). Pinned seed `20260517` for deterministic reproduction; matches Phase 0's pattern (`20260516`).

### `internal/integration/datacleaner_recompute_shadow_test.go` (new)

Walks the same 10-ticker basket as Phase 0's `TestDatacleaner_PlugInvariants_TickerBasket` (`AAPL, MSFT, JNJ, KO, F, AMD, MXL, TSM, BABA, EQIX`), loads each captured SEC raw fixture from `artifacts/tier2-baseline/<newest-date>/<TICKER>/req_<uuid>/05-fetch-sec.raw.json`, runs the SEC parser, then runs the FULL cleaner pipeline (calling `service.CleanFinancialData` against a wired-up `datacleaner.NewDataCleanerService` with a `zaptest/observer`-attached context), and captures every WARN emitted by `recomputeUmbrellas`.

Per-ticker output written to `internal/integration/testdata/recompute-shadow/<TICKER>.json`:

```json
{
  "ticker": "MXL",
  "bundle_root": "artifacts/tier2-baseline/2026-05-12/MXL/req_<uuid>/",
  "periods_processed": 47,
  "periods_with_divergence": 12,
  "divergences": [
    {
      "period": "2026Q1",
      "umbrella": "TotalAssets",
      "reported": 387402067,
      "recomputed": 771267000,
      "delta": 383864933,
      "plug": 0,
      "clamp_suspected": false
    },
    {
      "period": "2017FY",
      "umbrella": "CurrentAssets",
      "reported": 250000000,
      "recomputed": 305000000,
      "delta": 55000000,
      "plug": 0,
      "clamp_suspected": true
    }
  ]
}
```

**Recording-not-asserting policy** (see §"Integration test specification" below): the test passes as long as every basket member's per-ticker JSON snapshot exists with valid structure. It does NOT assert on a specific divergence count — that would require pre-knowledge of every adjuster's divergence pattern, which is exactly what Phase 1 is built to discover. The post-merge analysis (separate PR) reads these snapshots and produces the structured Phase 2 punch list.

The test asserts ONE summary invariant: `passedCount >= 5` (matching Phase 0's `assertPlugTriple` floor) to catch the all-skip silent regression.

---

## Tasks by Agent

Phase 1 is **BACKEND-only** for the code work; **QA** validates spec conformance and the recording-not-asserting strategy; **REVIEWER** verifies the no-mutation invariant and the log-shape spec.

### BACKEND: 8 tasks

#### Task 1.1: Author `recomputeUmbrellas` + property test (RED → GREEN)

**Files:** Create `internal/services/datacleaner/recompute.go`, `internal/services/datacleaner/recompute_test.go`.

**Steps:**
- [ ] Write the failing property test first (`TestRecomputeUmbrellas_Property_WellFormedNoDivergence`); confirms `recomputeUmbrellas` is undefined → compile error.
- [ ] Write the four table tests (`NoMutation`, `NilFD_Safe`, `EmitsWarnOnDivergence`, `ClampSuspectedFlag`).
- [ ] Implement `recomputeUmbrellas`, `emitIfDiverged`, `divergenceTolerance` per §"Module Descriptions → recompute.go".
- [ ] Run `go test ./internal/services/datacleaner/... -run TestRecomputeUmbrellas` — all five test cases PASS.
- [ ] Run `go vet ./internal/services/datacleaner/...` — no issues.
- [ ] Commit: `feat(datacleaner): add recomputeUmbrellas shadow-mode shim (DC-1 Phase 1)`.

#### Task 1.2: Wire call site into `CleanFinancialData`

**Files:** Modify `internal/services/datacleaner/service.go` (insert at line 230).

**Steps:**
- [ ] Add the comment block + `recomputeUmbrellas(ctx, result.CleanedData)` call per §"Module Descriptions → service.go".
- [ ] Run `go test ./internal/services/datacleaner/...` — full datacleaner suite PASSES (no behavior change expected).
- [ ] Run `go test ./...` — full suite GREEN (modulo SCHED-1 flake; see §Risks).
- [ ] Commit: `feat(datacleaner): invoke recomputeUmbrellas at end of cleaner pipeline (DC-1 Phase 1)`.

#### Task 1.3: Integration test — basket shadow-recording

**Files:** Create `internal/integration/datacleaner_recompute_shadow_test.go`, `internal/integration/testdata/recompute-shadow/.gitkeep`.

**Steps:**
- [ ] Mirror the bundle-discovery logic from `datacleaner_plug_invariants_test.go` (newest ISO-date subdir under `artifacts/tier2-baseline/`).
- [ ] For each basket ticker, load the captured SEC raw payload, run `sec.NewParser(zap.NewNop()).ParseFinancialData(ctx, &facts)`, then walk every period and call `service.CleanFinancialData(ctx, fd)` with a `zaptest/observer`-attached context.
- [ ] Capture every observer entry matching `"recomputeUmbrellas: umbrella divergence"` into a per-ticker `<TICKER>.json` snapshot under `testdata/recompute-shadow/`.
- [ ] Assert `passedCount >= 5` after the loop (matching Phase 0's floor).
- [ ] Add `testdata/recompute-shadow/*.json` to the commit (they're the Phase 2 punch-list input).
- [ ] Run `go test ./internal/integration/... -run TestDatacleaner_RecomputeShadow_TickerBasket -count=1` — PASS.
- [ ] Commit: `test(integration): record recomputeUmbrellas divergences across 10-ticker basket (DC-1 Phase 1)`.

#### Task 1.4: Replay regression validation

**Files:** No code changes; runs `cmd/replay` against AAPL + MSFT bundles.

**Steps:**
- [ ] Run `go run ./cmd/replay --from=parsed --diff-stages artifacts/tier2-baseline/<newest>/AAPL/req_<uuid>/`.
- [ ] Run `go run ./cmd/replay --from=parsed --diff-stages artifacts/tier2-baseline/<newest>/MSFT/req_<uuid>/`.
- [ ] Confirm `17-response.json` diffs show ONLY timestamp drift (`as_of`, `calculated_at`, `market_data_date`). Any non-timestamp diff is a Phase 1 regression — STOP and investigate.
- [ ] If JPM bundle requires `--allow-schema-drift` (per the existing T2-BS-2 note in CLAUDE.md), include the flag; document in commit message.
- [ ] No commit at this stage — empirical verification only. Capture the diff output for the closeout report.

#### Task 1.5: Documentation updates

**Files:** Modify `CLAUDE.md` (Common Gotchas DC-1 entry), `TESTING.md` (plug-invariant subsection extension), `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` (Change log).

**Steps:**
- [ ] **CLAUDE.md:** in the existing "DC-1 datacleaner refactor is IN FLIGHT (Phase 0 SHIPPED 2026-05-16, merge `1640394`)" gotcha entry, append a sentence: *"Phase 1 SHIPPED YYYY-MM-DD (merge `<hash>`): `internal/services/datacleaner/recompute.go::recomputeUmbrellas` runs at the end of the cleaner pipeline as a shadow-mode observer — emits WARN lines tagged `"phase":"DC-1-P1-shadow"` for every umbrella divergence, but does NOT mutate `*FinancialData`. Phase 2's `Adjuster` interface refactor consumes the divergence enumeration captured in `internal/integration/testdata/recompute-shadow/`."*
- [ ] **TESTING.md:** in the "Plug-invariant property tests (DC-1 Phase 0+)" subsection at line 174-184, append: *"Phase 1 extends the same pattern to the recompute side: `internal/services/datacleaner/recompute_test.go::TestRecomputeUmbrellas_Property_WellFormedNoDivergence` (4 properties × 200 iterations, pinned seed `20260517`) asserts that for any well-formed FinancialData where the plug invariant holds, `recomputeUmbrellas` emits zero WARN lines. The clamp-fired and adjuster-mutated branches are recorded (not asserted) by the integration test at `internal/integration/datacleaner_recompute_shadow_test.go` for Phase 2's punch list."*
- [ ] **spec changelog:** append row to the Change log table: *"| YYYY-MM-DD | Phase 1 SHIPPED: `recomputeUmbrellas` shadow-mode observer added to end of cleaner pipeline; emits structured WARN logs on umbrella/component divergence without mutating FinancialData; ticker-basket integration test records divergences as `testdata/recompute-shadow/<TICKER>.json` snapshots for Phase 2 input. Zero downstream behavior change — replay-verified on AAPL + MSFT. |"*
- [ ] Commit: `docs(dc1): Phase 1 SHIPPED — recomputeUmbrellas shadow-mode + cross-refs`.

#### Task 1.6: Tracker + thesis updates

**Files:** Modify `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md`, `docs/THESIS.md`.

**Steps:**
- [ ] **DC-1 tracker:** in the "Status" header, change to *"IN PROGRESS — Phase 0 SHIPPED 2026-05-16 (merge `1640394`); Phase 1 SHIPPED YYYY-MM-DD (merge `<hash>`); Phases 2-4 pending."* Append a "Phase 1 progress" paragraph mirroring the Phase 0 progress paragraph's structure (cross-reference the implementation plan, closeout report, and shadow-analysis report).
- [ ] **THESIS.md:** find the DC-1 row in the Phases table; update status from "in flight" to "Phase 1 SHIPPED".
- [ ] Commit: `docs(dc1): bump tracker + thesis for Phase 1 SHIPPED`.

#### Task 1.7: REVIEWER cleanup — Worktree B Phase 0 NITs (optional)

**Files:** Various test files per Phase 0 closeout's list (N1-N8).

**Steps:**
- [ ] **(Optional, BACKEND-judgment call)** Pick up Phase 0's remaining 5 NIT items (N1, N2, N3, N4, N5, N7, N8) per the Phase 0 closeout "Worktree B NITs not addressed" section, OR leave for a standing polish PR. Default: leave for polish PR (keeps the Phase 1 worktree scope tight).
- [ ] If addressed, commit separately: `refactor(tests): Phase 0 NIT cleanup (DC-1 reviewer carryover)`.

#### Task 1.8: Closeout-report draft

**Files:** Create `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-closeout.md`.

**Steps:**
- [ ] Mirror Phase 0's closeout structure: What landed (single worktree, X commits), Empirical verification (replay diff captured in Task 1.4), What's deferred to Phase 2+ (per-adjuster ledger, `Adjuster` interface, `CleanedFinancialData` view system, SQLite schema migration), Lessons learned, Acceptance criteria contributions.
- [ ] Cross-reference the (about-to-be-filed) shadow-analysis report.
- [ ] Commit: `docs(dc1): Phase 1 closeout report`.

### QA: 4 tasks

- [ ] Verify the recording-not-asserting strategy is correctly implemented: no integration-test failure on MXL 2017FY / EQIX 2013Q1 known clamp-fired periods.
- [ ] Verify `cmd/replay --diff-stages` against AAPL + MSFT yields ONLY timestamp drift (no financial drift in `17-response.json`).
- [ ] Verify the `internal/integration/testdata/recompute-shadow/` snapshots are deterministic across runs (same fixture → same JSON).
- [ ] Spot-check three random divergent WARN lines in the JSON snapshots against the corresponding bundle's `10-clean-trace.json` to confirm the `FromAccount` field of an Adjustment in the trace correlates with the WARN's `umbrella` field.

### REVIEWER: 4 tasks

- [ ] Confirm `recomputeUmbrellas` does not mutate `*entities.FinancialData` anywhere — read every line and grep for `fd.* =` assignments (there should be zero, only reads).
- [ ] Confirm the call site uses `logctx.From(ctx)` (via the new function's signature) and inherits request_id from the HTTP path. Read the call chain from `api/v1/handlers/fair_value.go` through `datafetcher/coordinator.go` to `datacleaner.CleanFinancialData` to confirm `ctx` is the request-scoped context.
- [ ] Confirm the WARN field set matches the spec (§D) exactly — Phase 2 tooling depends on the field names being stable.
- [ ] Confirm the `divergenceTolerance = 1.0` constant has the documented rationale in a comment block and is NOT a relative tolerance.

### HUMAN: 2 tasks

- [ ] Approve the plan (before BACKEND dispatch).
- [ ] Approve the merge (after V-R-Q sign-off).

### Post-merge follow-up (separate PR, BACKEND): 1 task

- [ ] File `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md` per §"Test plan for shadow-analysis follow-up" below.

---

## Spec Updates

Proposed concrete changes (small and bounded):

### `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`

Append to the Change log table:

```
| YYYY-MM-DD | Phase 1 SHIPPED: `recomputeUmbrellas` shadow-mode observer added to end of cleaner pipeline; emits structured WARN logs (`phase: "DC-1-P1-shadow"`) on umbrella/component divergence WITHOUT mutating FinancialData; ticker-basket integration test records divergences as `internal/integration/testdata/recompute-shadow/<TICKER>.json` snapshots for Phase 2's input. Zero downstream behavior change — replay-verified on AAPL + MSFT. |
```

### `CLAUDE.md` (Common Gotchas, DC-1 entry)

Append after the existing Phase 0 paragraph:

> Phase 1 SHIPPED YYYY-MM-DD (merge `<hash>`): `internal/services/datacleaner/recompute.go::recomputeUmbrellas` runs at the end of the cleaner pipeline as a shadow-mode observer — emits WARN lines tagged `"phase":"DC-1-P1-shadow"` for every umbrella divergence, but does NOT mutate `*FinancialData`. Phase 2's `Adjuster` interface refactor consumes the divergence enumeration captured in `internal/integration/testdata/recompute-shadow/`. To grep production logs: `rg '"phase":"DC-1-P1-shadow"'`. To re-derive the shadow analysis locally: `go test ./internal/integration/... -run TestDatacleaner_RecomputeShadow_TickerBasket -count=1`.

### `TESTING.md` (plug-invariant subsection)

Append after line 184:

> Phase 1 extends the same pattern to the recompute side: `internal/services/datacleaner/recompute_test.go::TestRecomputeUmbrellas_Property_WellFormedNoDivergence` (4 properties × 200 iterations, pinned seed `20260517`) asserts that for any well-formed FinancialData where the plug invariant holds, `recomputeUmbrellas` emits zero WARN lines. The clamp-fired and adjuster-mutated branches are recorded (NOT asserted) by the integration test at `internal/integration/datacleaner_recompute_shadow_test.go` for Phase 2's punch list — committed snapshots under `internal/integration/testdata/recompute-shadow/<TICKER>.json` are the Phase 1 → Phase 2 hand-off artifact.

### `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md`

Status line update + new progress paragraph (mirror Phase 0's structure).

### `docs/THESIS.md`

DC-1 phase row status update from "Phase 0 SHIPPED, Phase 1 in flight" to "Phase 1 SHIPPED".

---

## Tests

### Property test specification (`recompute_test.go::TestRecomputeUmbrellas_Property_WellFormedNoDivergence`)

**Invariant pinned:** for any well-formed `FinancialData` (where the parser's plug invariant `umbrella == sum(known_components) + plug` holds with `plug >= 0`), `recomputeUmbrellas` emits ZERO WARN lines.

**Generator shape:** mirrors `plugs_test.go::TestComputePlugs_Property_ComponentsSumToUmbrellas` byte-for-byte, except instead of calling `computePlugs` to fill the plugs, the test pre-computes the well-formed plug values and pre-stamps them on `fd`:

```go
// "well-formed CurrentAssets" property:
prop.ForAll(func(cash, inventory, otherCA float64) bool {
    fd := &entities.FinancialData{
        Ticker: "FUZZ", CIK: "0", FilingPeriod: "2024FY",
        CashAndCashEquivalents: cash,
        Inventory:              inventory,
        OtherCurrentAssets:     otherCA,
        CurrentAssets:          cash + inventory + otherCA,  // well-formed by construction
    }
    core, recorded := observer.New(zap.WarnLevel)
    ctx := logctx.Inject(context.Background(), zap.New(core))
    recomputeUmbrellas(ctx, fd)
    return len(recorded.FilterMessage("recomputeUmbrellas: umbrella divergence").All()) == 0
},
    gen.Float64Range(0, 1e12),
    gen.Float64Range(0, 1e12),
    gen.Float64Range(0, 1e12),
)
```

Repeat for `TotalAssets`, `CurrentLiabilities`, `TotalLiabilities` properties — same shape, different field set.

**Shrinker behavior:** gopter's default `Float64Range` shrinker halves the value on each shrink step. The expected counterexample (if a logic bug existed) would be a triple where the WARN fires despite the well-formed precondition — that would indicate either (a) `recomputeUmbrellas` uses a different decomposition than `computePlugs` (a real bug), or (b) the divergence tolerance is too tight for accumulated float64 error.

**Pinned seed:** `20260517` (matches Phase 0's `20260516` cadence; deterministic reproduction across CI).

**Iterations:** 200 per property (same as Phase 0).

### Integration test specification (`datacleaner_recompute_shadow_test.go`)

**Format:** per-ticker JSON snapshot at `internal/integration/testdata/recompute-shadow/<TICKER>.json`. Schema as documented in §"Module Descriptions → recompute_test.go".

**Location:** `internal/integration/` (matches Phase 0's `datacleaner_plug_invariants_test.go` co-location convention).

**Pass criterion: RECORDING, not assertion-on-divergence-count.**

Justification: Phase 0 demonstrated that known clamp-fired periods (MXL 2017FY, EQIX 2013Q1) violate the equality branch by design — those will produce WARN lines that recording-not-asserting absorbs cleanly. A maximum-divergence-count assertion would require pre-knowing every adjuster's divergence shape across the basket, which is exactly the data Phase 1 is built to discover. Asserting a count would either (a) be brittle to legitimate new divergences from future adjuster additions, or (b) require updating the constant on every adjuster change, conflating observation with policy.

The single ASSERTION the test makes is **`passedCount >= 5`** (matching Phase 0's `assertPlugTriple` floor) — this catches the "all bundles failed to load" silent regression but tolerates JNJ/TSM/BABA being skipped for lack of captured fixtures.

**Recommended supplement (NOT a Phase 1 requirement):** the test could also assert that the per-ticker JSON has **at least one** divergence for the tickers known to mutate balance-sheet umbrellas (MXL is the canonical example per the spec — A1 goodwill exclusion drives $318.6M of TotalAssets reduction). That assertion would be a tripwire for "recomputeUmbrellas isn't actually wired in." A conservative form: assert that across the full basket, the sum of `periods_with_divergence` is > 0. BACKEND can add this if the post-merge analysis suggests it would catch regressions.

**File commit policy:** the per-ticker JSON snapshots ARE committed. They are the Phase 1 → Phase 2 hand-off artifact and need to be diff-reviewable. If the snapshot output changes after a future adjuster tweak, the diff in PR review surfaces the change — which is exactly the signal we want.

### Critical edge cases that MUST have unit tests

- Nil `fd` → no panic.
- Nil `ctx` → uses `logctx.From(nil)` nop logger, no panic.
- All-zero `fd` → no WARN (everything sums to zero; divergence is zero).
- `TotalAssets` mutated to `TotalAssets - 100` with components intact → exactly one WARN with `umbrella: "TotalAssets"`, `delta: 100`.
- `OperatingLeaseLiabilityCurrent` populated AND `OtherCurrentLiabilities` populated (today never happens, but Phase 2 may split) → invariant holds, no WARN.
- Clamp-fired `fd` (plug zero, components > umbrella) → WARN with `clamp_suspected: true`.

---

## Implementation Roadmap

Single worktree, sequential execution. Per Phase 0 closeout lesson #3, single-worktree-sequential is more efficient than fan-out for this scope.

1. **Create worktree:** `git worktree add ../midas-dc1-p1 -b dc1-phase-1 master`.
2. **Task 1.1** — author `recomputeUmbrellas` + RED/GREEN property + table tests (~4-6h).
3. **Task 1.2** — wire call site, full-suite regression (~1h).
4. **Task 1.3** — integration test + commit per-ticker snapshots (~4-6h; includes baseline-resolution boilerplate).
5. **Task 1.4** — replay regression validation (~1h, but blocks merge).
6. **Task 1.5 + 1.6** — documentation + tracker updates (~2h).
7. **Dispatch VERIFIER** (independent functional re-run, ~2h).
8. **Dispatch REVIEWER** (code-quality + log-shape verification, ~2h).
9. **Dispatch QA** (recording-not-asserting + replay-diff confirmation, ~2h).
10. **HUMAN merge approval.**
11. **Post-merge: Task 1.8 closeout report** (~2h).
12. **Post-merge separate PR: shadow-analysis report** (~4-6h, see §"Test plan for shadow-analysis follow-up").

Total wall clock: ~3 days of focused BACKEND work + ~1 day V-R-Q + 1 day for the post-merge analysis report = 5 days (matches the handoff's 3-5 day estimate).

---

## Potential Challenges

### Risk 1 — Accidental mutation of `*fd`

**Failure mode:** a developer mistypes `=` for `==` (e.g., `fd.TotalAssets = recomputedTA` instead of using `recomputedTA` only as an argument), silently breaking the "zero behavior change" invariant.

**Mitigations:**
1. `TestRecomputeUmbrellas_NoMutation` table test takes a pre-call deep copy of `fd`, runs `recomputeUmbrellas`, and asserts `reflect.DeepEqual(precall, fd)`. Catches any field write.
2. REVIEWER explicitly greps for `fd.* =` assignments in `recompute.go` per Task list above.
3. Empirical: AAPL + MSFT replay diff in Task 1.4 catches any cleaner-output drift end-to-end.

### Risk 2 — Log-volume blowup

**Failure mode:** if every adjuster cycle produces 4 WARN lines and the cleaner runs over a 47-period historical series during a fair-value request, that's potentially 188 WARN lines per request. Multiplied across the live basket on a hot tag like `?force_recalculate=true`, this could overwhelm log shippers.

**Mitigations:**
1. **The cleaner runs ONCE per request, not per-period.** `CleanFinancialData` is called with the latest `*FinancialData` only (verified by reading `service.go::CleanFinancialData` signature and call sites). So the WARN budget is 4 lines/request max, not 188.
2. The integration test captures the divergence count across the basket; if any single ticker emits more than 4 WARN lines per `CleanFinancialData` call, the test surfaces it (each subtest logs the count).
3. Default zap encoder is JSON; WARN volume is well below the existing `99-narrate.jsonl` Tier-1 stream rate.

### Risk 3 — SCHED-1 scheduler flake firing during test runs

**Failure mode:** the pre-existing race in `scheduler.go:50-56` (tracked at `docs/reviewer/scheduler-test-cleanup-race.md`) may fire during `go test ./...`, producing a misleading "Phase 1 broke the scheduler" attribution.

**Mitigations:**
1. Phase 1's scope is `internal/services/datacleaner/` only — touches NO scheduler code. Any scheduler test failure is the pre-existing SCHED-1, not Phase 1.
2. If it fires during BACKEND validation, document in the closeout report under "Known pre-existing flakes" and re-run `go test ./internal/services/scheduler/...` to confirm intermittency. Do not chase.

### Risk 4 — Concurrent Tier 2 worktrees still active

**Failure mode:** Tier 2 P1-P4 worktrees are touching `internal/services/valuation/` (different files than DC-1 Phase 1's `internal/services/datacleaner/`). Zero file-scope overlap, but a hasty `git worktree add` on top of stale master could create rebase conflicts.

**Mitigations:**
1. Run `git worktree list` before dispatching BACKEND. Confirm Tier 2 worktrees are still on their branches, not master.
2. Master HEAD `219ad9e` includes the Phase 1 handoff doc — fork the worktree from this commit.
3. Instruct BACKEND subagent: "Stay out of `internal/services/valuation/` unless a DC-1 reason absolutely requires it. Tier 2 P1-P4 worktrees are active on those files."

### Risk 5 — Misattribution in the integration test snapshot

**Failure mode:** a future change to `assets.go` or `liabilities.go` (Phase 2) shifts the divergence pattern; the committed `testdata/recompute-shadow/*.json` snapshots become stale, but the test still passes (recording-not-asserting).

**Mitigations:**
1. Phase 2's PR will re-run the integration test and the snapshot diff will surface every adjuster pattern change. The diff IS the regression signal; the snapshots are intentionally diff-reviewable.
2. The shadow-analysis report (filed post-merge) names every divergent (ticker, umbrella) pair so Phase 2's PR can be matched against the expected pattern shift.

---

## GitHub Issue Update

- Issue: N/A (no GitHub issue tracker integration for DC-1 phase work; tracked in `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md`).
- Status: not updated.
- Actions taken: tracker update is part of Task 1.6 (see §"Tasks by Agent → BACKEND → 1.6").
- Proposed update: N/A.

---

## Acceptance Criteria

Copied from the handoff's 10-item checklist with line-anchored sharpening:

- [ ] `recomputeUmbrellas` function lands in `internal/services/datacleaner/recompute.go` with godoc comment block referencing the spec, the Phase 1 implementation plan, AND the "MUST NOT mutate" invariant.
- [ ] Function is called exactly ONCE per `CleanFinancialData` invocation, at the line between current line 229 (`result.Flags = append(result.Flags, additionalFlags...)`) and line 231 (`// Calculate quality score`).
- [ ] Property test (`gopter`, pinned seed `20260517`, 4 properties × 200 iterations) at `internal/services/datacleaner/recompute_test.go::TestRecomputeUmbrellas_Property_WellFormedNoDivergence` pins "well-formed input → no divergence" baseline.
- [ ] Integration test at `internal/integration/datacleaner_recompute_shadow_test.go` records every divergence across the 10-ticker basket as `internal/integration/testdata/recompute-shadow/<TICKER>.json` snapshots; `passedCount >= 5` asserted.
- [ ] Zero downstream behavior change — replay-verified on AAPL + MSFT (`17-response.json` shows only timestamp drift in `as_of` / `calculated_at` / `market_data_date`).
- [ ] Full test suite green (modulo SCHED-1 flake if it fires; documented but not chased).
- [ ] CLAUDE.md Common Gotchas DC-1 entry updated to note Phase 1 SHIPPED + shadow-mode WARN grep pattern (`rg '"phase":"DC-1-P1-shadow"'`).
- [ ] TESTING.md plug-invariant subsection (line 174-184) extended with the recompute-side companion pattern + cross-ref to the integration test.
- [ ] DC-1 reviewer tracker status updated: "Phase 1 SHIPPED YYYY-MM-DD; Phases 2-4 pending" + Phase 1 progress paragraph filed.
- [ ] Shadow analysis report filed in `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md` (post-merge, separate PR) and cross-referenced from the DC-1 tracker.
- [ ] Phase 0 closeout-style report filed for Phase 1 at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-closeout.md`.

### Reviewer / QA verification commands

```bash
# No mutation invariant (REVIEWER)
go test ./internal/services/datacleaner/... -run TestRecomputeUmbrellas_NoMutation -count=1 -v

# Property test invariant (REVIEWER + QA)
go test ./internal/services/datacleaner/... -run TestRecomputeUmbrellas_Property -count=1 -v

# Integration recording (QA)
go test ./internal/integration/... -run TestDatacleaner_RecomputeShadow_TickerBasket -count=1 -v

# Replay no-drift (QA)
go run ./cmd/replay --from=parsed --diff-stages artifacts/tier2-baseline/<newest>/AAPL/req_<uuid>/
go run ./cmd/replay --from=parsed --diff-stages artifacts/tier2-baseline/<newest>/MSFT/req_<uuid>/

# Production WARN grep pattern (post-merge, operators)
rg '"phase":"DC-1-P1-shadow"' artifacts/
```

---

## Assumptions and Open Questions

### Assumptions used to produce this plan

1. **`CleanFinancialData` is called exactly once per fair-value request.** Verified by reading the datafetcher coordinator's call pattern (the cleaner is invoked once on the latest `*FinancialData`, not per-period). Risk 2's log-volume math depends on this.
2. **`logctx.From(ctx)` returns a logger that inherits `request_id` on the HTTP path.** Verified by reading `internal/observability/logctx/logctx.go`'s godoc and the existing usage pattern in the cleaner-adjacent code.
3. **The integration test's bundle-discovery pattern (newest ISO-date subdir under `artifacts/tier2-baseline/`) is stable.** Mirrored from Phase 0's `datacleaner_plug_invariants_test.go` which is green on master.
4. **Tier 2 P1-P4 worktrees stay clear of `internal/services/datacleaner/`.** Verified by reading the Phase 0 closeout and the current handoff doc; Tier 2's scope is `internal/services/valuation/`.
5. **The `result.CleanedData` pointer at line 230 is the same `*entities.FinancialData` instance that adjusters mutated** (verified by reading `service.go:195-196` — `cleanedData := *data; result.CleanedData = &cleanedData`; the adjusters at `applyActiveAdjustments` operate on `result.CleanedData` and the recompute observation is on that same pointer).
6. **`assets.go::ProcessGoodwillAdjustment`, `ProcessIntangibleAdjustment`, `ProcessInventoryAdjustment`, `ProcessDeferredTaxAdjustment`** mutate both component and umbrella but DO NOT touch the plug field (verified by reading lines 65-69, 156-157, 230-232, 307-308). Phase 1 surfaces this asymmetry; Phase 2 fixes it.

### Blocking questions

None. The plan is ready for BACKEND dispatch on HUMAN approval.

### Non-blocking questions (resolvable during implementation or post-merge)

1. **Should the integration test ALSO assert "sum of divergences across basket > 0"** as a tripwire for "recomputeUmbrellas isn't wired in"? Recommendation: ADD this assertion after the first basket run confirms a known-positive sum (so the assertion is data-anchored, not speculative). Add via a follow-up commit in the same worktree if the post-merge analysis suggests it's needed.
2. **Should we add the WARN lines to a new artifact-bundle artifact** (`13-recompute-shadow.jsonl`) alongside the existing `10-clean-trace.json`? Recommendation: DEFER. The narrate stream + the per-ticker JSON snapshot are sufficient for Phase 2's analysis. Revisit if the post-merge shadow-analysis report suggests narrate volume swamps the relevant signal.
3. **Once Phase 2 fixes the divergences, do we KEEP `recomputeUmbrellas` as an ongoing regression catcher or REMOVE it?** Recommendation: KEEP, but drop severity to DEBUG. The function is cheap and catches future regressions where a new adjuster forgets to keep components and umbrellas in sync. Codify this decision in Phase 2's closeout.

### Deferred to Phase 2+

- Per-adjuster ledger / before-after snapshots (§D — out of Phase 1 scope; bloats to 7-10 days).
- `Adjuster` interface, `LedgerEntry`, `OverlaySpec`, `AdjusterOutput` (Phase 2).
- `CleanedFinancialData`, `Restated()`, `InvestedCapital()` (Phase 3).
- SQLite schema migration for the plug columns (Phase 2+; pinned by Phase 0's flip-gate test).
- Consumer migration to view-specific reads (Phase 4).
- B3 routing correction (Phase 4).

---

## Test plan for shadow-analysis follow-up

Phase 1's gate criterion to Phase 2 is "shadow warnings analyzed across basket". The post-merge analysis report MUST capture:

### Required sections of `phase-1-shadow-analysis.md`

1. **Methodology**: how the snapshots were generated (`go test ./internal/integration/... -run TestDatacleaner_RecomputeShadow_TickerBasket`, baseline date, master HEAD at run).
2. **Per-ticker divergence summary**: for each of the 10 basket tickers, a table:
   - `periods_processed`, `periods_with_divergence`, `total_warn_lines`
   - Breakdown by umbrella (`CurrentAssets`, `TotalAssets`, `CurrentLiabilities`, `TotalLiabilities`)
   - Breakdown by `clamp_suspected` (true/false)
3. **Cross-ticker pattern table**: for each (umbrella, clamp_suspected) cell, count of distinct tickers exhibiting the pattern. Identifies the most-mutated umbrella (likely `TotalAssets` per the spec's MXL analysis showing $383M reduction).
4. **Adjuster attribution (best-effort)**: cross-reference WARN lines against the `Adjustment.FromAccount` values in each bundle's `10-clean-trace.json`. Attribute each divergence to one of {A1 goodwill, A2 intangible, A4 DTA, A5 inventory, B1 lease, B2 pension, B3 contingent, multi-adjuster}.
5. **Clamp-fired enumeration**: list every (ticker, period) where `clamp_suspected: true` fired. Confirm these match Phase 0's known cases (MXL 2017FY, EQIX 2013Q1) or document any new ones.
6. **Phase 2 punch-list**: ordered list of "Phase 2 must fix this" items, prioritized by:
   - Frequency (number of tickers × periods affected)
   - Magnitude (median absolute delta in USD)
   - Risk (whether the divergence affects a known DCF-input field — `TotalDebt` divergence is high-priority because B1/B2/B3 land there today)
7. **"No surprises" confirmation**: explicit statement that every observed divergence maps to a known adjuster pattern OR an expected Phase 0 clamp case. ANY unexplained divergence is a red flag that Phase 1 surfaced a hidden assumption we hadn't enumerated — STOP and investigate before starting Phase 2.

### Required data attached

- The committed `testdata/recompute-shadow/*.json` snapshots (analysis is reproducible from them).
- The raw `99-narrate.jsonl` excerpts showing the WARN lines (extracted via `rg '"phase":"DC-1-P1-shadow"'` from the captured replay run).
- Master HEAD SHA at analysis time.
- Date of analysis.

### Approval gate

The shadow-analysis report's conclusion section MUST end with one of:
- **"Phase 1 → Phase 2 gate OPEN. Phase 2 can start."** — every observed divergence is expected.
- **"Phase 1 → Phase 2 gate BLOCKED. Investigate <list>."** — at least one unexplained divergence; Phase 2 cannot start until resolved.

Filed at: `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md`. Cross-referenced from the DC-1 tracker.

---

## Next Steps

1. **HUMAN reviews and approves this plan.**
2. **Create Phase 1 worktree** (`git worktree add ../midas-dc1-p1 -b dc1-phase-1 master`).
3. **Dispatch BACKEND** with this plan + the Phase 1 handoff doc as required reading. BACKEND executes Tasks 1.1-1.8 in order.
4. **Dispatch VERIFIER** for independent functional re-run.
5. **Dispatch REVIEWER** with explicit focus on the no-mutation invariant and the WARN-shape spec.
6. **Dispatch QA** with explicit focus on the recording-not-asserting strategy and replay-diff confirmation.
7. **HUMAN final acceptance + merge.**
8. **Post-merge:** dispatch BACKEND for the shadow-analysis report (separate PR).

HANDOFF_TO: HUMAN (for plan approval); then BACKEND.
