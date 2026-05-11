# RM-1.A — Stale-data check for TTM revenue helper (deferred from RM-1 / T7)

**Status:** OPEN — filed 2026-05-09 alongside the merge of Stream B's bridge-ordering fix (commit `9da6c68`).
**Severity:** Low. The TTM helper's audit-trail string accurately reports the path that fired; the stale-data check is a data-quality enhancement, not a correctness fix.
**Origin:** Spec `docs/reviewer/RM-1-revenue-multiple-quarterly-vs-ttm.md` T7 ("Stale data: latest quarter > 18 months old"). Deferred during Stream B implementation because the helper lives in `internal/core/entities` (a leaf package); adding a stale-data check would couple `entities` to wall-clock time and break replay determinism.
**Blocks:** Nothing.
**Related:** RM-1 (parent tracker), RM-1.B (spec doc-drift companion), RM-3 (forward-revenue model — would also benefit from a clock dependency), `internal/observability/replay/clock.go` (the manifest-pinned clock pattern that already exists for D10 cross-year invariants).

---

## Context

Spec T7 specifies: when the latest reporting period is more than 18 months old, the TTM helper should emit a warning like `"revenue_base: data is N months old"`.

The current implementation in `internal/core/entities/financial_data.go::TrailingTwelveMonthsRevenue` and its sub-helpers (`ttmPriorBridgeRevenue`, `ttmFourQuartersRevenue`, `latestAnnualRevenue`, `annualizedQuarterRevenue`) does NOT consult a clock. All five fallback paths operate purely on period-name comparison and `FilingDate` ordering. The T7 deferral comment is inline at `financial_data.go:339-341`.

### Why the stale-data check was deferred

1. **Layering invariant.** Adding `time.Now()` to a leaf entity package would violate the convention that core/entities has no infrastructure dependencies (no clock, no logger, no FS, no network).
2. **Replay determinism.** Replay tooling consumes the helper through captured artifact bundles. A stale-data check consulting wall-clock time would emit different warnings depending on WHEN the replay runs, breaking the "frozen-in-time" invariant the replay pipeline established as D10 (see `internal/observability/replay/clock.go`'s `manifestClock`).
3. **Plumbing cost.** A `Clock` interface injected through every `HistoricalFinancialData` construction site is significant churn for a single warning string.

## Proposed fix (one of)

### Option A — Inject a Clock dependency at the entity layer

Add a `Clock` interface to `entities` (or a new wrapper type around `HistoricalFinancialData`). Replay uses the existing `manifestClock` that's already pinned for D10. Production wires `time.Now`-backed default.

**Pros:** Stale-data check lives at the data-source-of-truth layer.
**Cons:** Layering violation; touches every constructor site; replay must re-thread the manifestClock through entity construction.

### Option B — Compute staleness in the consumer

Move the stale-data check to `internal/services/valuation/models/revenue_multiple.go` where the valuation engine already has wall-clock context (used for FX rates and `data_freshness_score`). The TTM helper itself stays clock-free.

**Pros:** Layering preserved; replay determinism preserved (revenue_multiple already gets a clock from the valuation service); minimal plumbing.
**Cons:** Stale-data warning now lives at a different layer than the TTM source-string warning, which is semantically a small split.

### Option C — Drop the requirement

Decide that stale-data warnings are out of scope for the TTM helper specifically. The existing `data_freshness_score` (15-point penalty when historical data is outdated) already covers the broader staleness signal at the valuation-result level.

**Pros:** Zero work; arguably the most conservative interpretation of "do not over-engineer".
**Cons:** Spec T7 promises a specific warning string that this option drops.

## Recommendation

**Option B**. Keeps the TTM helper deterministic and replay-safe. The valuation engine already has the right plumbing (clock, freshness scoring, warning aggregation). The semantic split between source-path warnings (in the helper) and staleness warnings (in the consumer) is acceptable because both ride on `result.Warnings` for the eventual API response.

## Acceptance for closing this tracker

- [ ] Decision made (A, B, or C).
- [ ] If A or B: stale-data warning fires when the latest period is > 18 months old; warning string matches the spec format.
- [ ] T7 inline test added at the implementation layer.
- [ ] T7 deferral comment at `financial_data.go:339-341` removed (or updated to reflect the new home of the check).

## Out of scope

- Per-period freshness scoring (the existing `data_freshness_score` is a separate concern).
- Replay-time staleness assertion (artifact bundles intentionally don't track wall-clock-relative staleness).
- The DCF path's own use of `latest.Revenue` for projection seeding (the original RM-1 spec out-of-scope item) — track separately if needed.
