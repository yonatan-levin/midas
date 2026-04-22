# M-1 — `growth` and `model_selection` calc traces miss the `ticker` field

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
