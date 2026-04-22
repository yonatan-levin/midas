# M-1 — Calc-trace field completeness follow-ups

This file aggregates three small field-completeness items from Phase M code review that were deferred because the cleanest fix touches out-of-scope code (`pkg/finance/*`) or requires a richer classifier return type.

---

## M-1a — `growth` and `model_selection` calc traces miss the `ticker` field

**Status:** Noted during Phase M spec review (2026-04-23); not blocking Phase M completion.
**Severity:** Low (field completeness; correlation already available via `request_id`).

## Context

Phase M of the observability upgrade (`docs/refactoring/observability-upgrade-spec.md`) specifies that every calc-trace entry carry a minimal field set per stage. For `growth` and `model_selection` the spec table lists `ticker` as a required field.

The current implementation omits `ticker` because the emitting functions — `growth.Estimator.EstimateGrowthRates(...)` and `valuation/models.ModelRouter.SelectModel(...)` — do not receive the ticker as a parameter.

- `internal/services/growth/estimator.go:70` — `EstimateGrowthRates(ctx, analystData, historicalGrowth, sustainableGrowth)` — no ticker argument.
- `internal/services/valuation/models/router.go:87` — `SelectModel(ctx, industry, financials)` — no ticker argument.

The omission is operationally mitigated because the request-scoped logger already carries `request_id` (injected by `requestIDMiddleware`) plus `user_id`/`key_id` (after auth). Combined with the `ticker` field on the access log line for the same request, operators can reconstruct which ticker a given `growth` or `model_selection` entry refers to.

## Why it matters

- Self-describing log entries are easier to grep/filter in isolation (`stage=growth AND ticker=AAPL`).
- Downstream pipelines that fan calc traces out by ticker (e.g., "what's our growth estimate distribution across the S&P 500?") need the ticker on the entry itself, not via a join with the access log.

## Proposed fix (options)

1. **Pass ticker through.** Add a `ticker string` parameter to `EstimateGrowthRates` and to `SelectModel`. Update the one caller each in `valuation/service.go`. Both changes are internal (private-ish) — no public API impact. ~6 lines of code.
2. **Emit from the caller.** Move the emit sites up to `valuation/service.go performValuation`, consistent with how `industry_classification` is already handled. Downside: the callee loses self-contained tracing, making it slightly harder to reason about.
3. **Document the omission in the spec.** Update the field table for these two stages to note that `ticker` is intentionally excluded because it's available via request correlation.

Recommendation: **Option 1** — most principled, preserves emit-from-callee pattern, minimal diff.

## Tracked when

- Review: Phase M spec-review, 2026-04-23.
- Raised by: REVIEWER subagent during subagent-driven-development flow.

## Link

`docs/refactoring/observability-upgrade-spec.md` §Phase M (trace points table).

---

## M-1b — `industry_classification` trace emits a single code as `industry_code`; no parent-sector split

The spec field table listed `sic, naics, sector, industry, model_hint`. The current `industry.Classifier.Classify(...)` returns a single `industry_code` string (e.g. `"TECH_SAAS"` or `"FIN"`), not a `(sector, subIndustry)` tuple. A naïve split-on-`_` would be arbitrary — the code set is not guaranteed to follow that pattern.

The emit therefore surfaces only the fields the classifier genuinely produces: `sic`, `industry_code`, `model_hint`. `naics` and `sector` are dropped rather than populated with misleading duplicates.

### Proposed fix

- Extend `Classify` to return a richer struct (e.g. `ClassificationResult{ Sector, Industry, SubIndustry, ModelHint string; NAICS string }`) instead of a single string.
- Update its one caller (`valuation/service.go performValuation`).
- Update the emit to populate the full field set.

### Why deferred

Touches the classifier's public return type (a meaningful internal refactor). Phase M kept its scope to the observability wiring; the classifier enhancement belongs to a "classifier v2" or similar task.

---

## M-1c — `terminal_value` trace omits the raw exit-multiple TV component

The spec field table listed `gordon_tv, exit_multiple_tv, averaged_tv, terminal_growth`. `pkg/finance/dcf/dcf.Result` only exposes the averaged `TerminalValueNominal` — not the raw exit-multiple component. Back-calculating it via `2 * averaged - gordon` produces the mathematically correct value when exit multiples WERE used, but `gordon_tv` when they weren't, which is misleading.

The emit therefore surfaces `gordon_tv` (re-derived), `averaged_tv` (authoritative), and a boolean `exit_multiple_used` flag derived from the difference. The raw `exit_multiple_tv` is omitted.

### Proposed fix

Add `ExitMultipleTV float64` to `dcf.Result` in `pkg/finance/dcf/dcf.go` — a one-field addition set at the point where the average is computed. Update the `terminal_value` emit to include it.

### Why deferred

`pkg/finance/*` is explicitly out-of-scope per spec Decision D7 / Refinement R1 ("keep `pkg/finance/` logger-free; emit all calc traces from the service layer"). A one-field data addition to `dcf.Result` is not a "logger concern" and would be allowed in principle — but the deliberate policy is "zero `pkg/finance` diff in Phase M." Move with a companion Phase M.1 cleanup commit or bundle with a future dcf-enhancement task.

---

## Tracked when

- Review: Phase M code-quality review, 2026-04-23.
- Raised by: REVIEWER subagent during subagent-driven-development flow.
- Related spec decisions: D7 / R1 in `docs/refactoring/observability-upgrade-spec.md`.
