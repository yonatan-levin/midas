# SR-1 Part-B — deferred follow-ups

**Status:** OPEN — **GitHub issue: #29.** Spun out of the SR-1 Part-B burndown (#27, closed 2026-06-29) so the open work has a live home after the parent SR-1 tracker was archived.

**Parent (archived, full context):** `docs/reviewer/archive/SR-1-simplify-and-code-review-candidates.md` (see its "Part B disposition" block). These three items were deliberately NOT fixed in the #27 batch because each carries behavior- or wire-change risk that is unsafe to bundle with surgical fixes — they each need their own plan + validation.

---

## B7 (MEDIUM) — `evaluateRuleThreshold` ignores most configured thresholds

- **Where:** `internal/services/datacleaner/service.go` (`evaluateRuleThreshold`).
- **Claim:** the `PercentageOfRevenue` generic branch returns `data.Revenue > 10_000_000` (ignores the configured value); the `deferred_tax_assets` branch compares a fixed `TotalAssets*0.03` estimate's own 3% ratio against the threshold instead of the actual `data.DeferredTaxAssets`; `working_capital_window_dressing` keys off `Revenue > 50M`.
- **Why deferred:** honoring the configured thresholds changes *cleaning applicability* → which adjusters fire → recompute-shadow snapshots + the DDM bit-for-bit invariant. Requires a dedicated plan and a **deliberate recompute-shadow regeneration with golden review**, not a drive-by fix.
- **Adjacent:** TDB-5 externalized the *adjuster-side* gates only; this is the *rule-applicability* gate.

## B10 (LOW-MEDIUM) — growth fields carry USD in rate-named/JSON-tagged fields

- **Where:** `internal/services/growth/estimator.go::blendGrowthRate`.
- **Claim:** absolute USD revenue estimates are assigned into `AnalystRevenueGrowthY1/Y2`, which are named and JSON-tagged as growth *rates*. Consumers reading `analyst_revenue_growth_y1` get dollars (e.g. `4.5e11`), not a rate.
- **Why deferred:** the fix (rename the field or convert to a rate) is a **wire-contract change** needing consumer/dashboard coordination, and — if the response shape moves — a `internal/observability/replay/diff.go` field-count-guard update.
- **Verify:** inspect a covered ticker's `12-growth-curve.json`.

## bulk-errgroup (LOW) — serial bulk valuation loop

- **Where:** `GetBulkFairValue` handler loop.
- **Claim:** ≤10 tickers are valued strictly serially (latency = Σ tickers). A bounded, cache-respecting `errgroup` would cut p95.
- **Note:** a latency **enhancement**, not a correctness defect.

## (minor, folded in) B11 usage-recorder shutdown drain

- **Where:** `internal/api/usage_recorder.go`.
- **Claim:** the 4 worker goroutines run for the process lifetime; the queue is never closed and there is no graceful drain, so on shutdown in-flight/queued usage rows are lost. REVIEWER rated this LOW and "acceptable as-is" (it matches — and bounds — the prior fire-and-forget lifecycle; not a regression).
- **Why deferred:** optional hardening — add a `Close()` that closes the queue and waits, wired into server shutdown.

---

## Acceptance Criteria
- [ ] B7 fixed with a deliberate recompute-shadow regen + golden review, OR explicitly accepted-as-is with rationale.
- [ ] B10 resolved (rename or rate-convert) with consumer coordination + replay field-count guard updated if the response shape moves.
- [ ] bulk-errgroup implemented (bounded, cache-respecting) or declined with rationale.
- [ ] (optional) B11 worker-pool `Close()`/drain wired into server shutdown.

When all are dispositioned, archive this tracker to `docs/reviewer/archive/` and close #29.
