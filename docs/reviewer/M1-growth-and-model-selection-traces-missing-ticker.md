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

---

## M-1d — `equity_bridge` trace omits `minority_interest` and `preferred`

The spec field table for `equity_bridge` lists `ticker, cash, debt, minority_interest, preferred, equity_value, diluted_shares, per_share`. The `FinancialData` entity does not currently carry `minority_interest` or `preferred_equity` fields, so the emit omits both rather than emitting a hardcoded 0 that would mislead downstream log consumers.

### Proposed fix

- Extend `internal/core/entities/financial_data.go` with `MinorityInterest float64` and `PreferredEquity float64`.
- Update the SEC gateway to populate them from the right XBRL tags (`us-gaap:MinorityInterest`, `us-gaap:PreferredStockValue`, or the fact-tag equivalents).
- Update the datacleaner normalisation to carry the values through.
- Update `dcf.CalculateEquityValue` to subtract them alongside debt, if the current equation doesn't already.
- Add the two fields to the `equity_bridge` emit.

### Why deferred

This requires a coordinated change across entity, gateway, cleaner, and math layers — not a trace-only change. It belongs in an "equity bridge completeness" ticket, not in Phase M's observability scope.

---

---

## M-1e — `NewLogger` file sink fails silently on unwritable path

When `logging.file.enabled=true` and `logging.file.path` points at a non-existent directory or an unwritable location, `lumberjack.Logger` lazily fails on first write — it silently drops the line. The stdout core keeps working, so the server remains operational, but operators enabling file logging on a misconfigured path get zero signal that file logs are being lost.

### Proposed fix

In `internal/di/container.go NewLogger`, before registering the file core, proactively verify the directory exists (or can be created) and the file is writable. On probe failure, log a warning to the stdout core and skip the file core — fall back cleanly to stdout-only. Sketch:

```go
if cfg.Logging.File.Enabled {
    if err := os.MkdirAll(filepath.Dir(cfg.Logging.File.Path), 0o755); err != nil {
        // Build a temporary stdout-only logger to emit the warning
        stdoutOnly := zap.New(stdoutCore, zap.AddCaller())
        stdoutOnly.Warn("logging.file.enabled=true but directory is unwritable; falling back to stdout-only",
            zap.String("path", cfg.Logging.File.Path), zap.Error(err))
    } else {
        fileWriter := &lumberjack.Logger{...}
        ...
        cores = append(cores, fileCore)
    }
}
```

### Required test

`TestNewLogger_FileSinkProbeFailure`: set `File.Path` to a guaranteed-unwritable path (e.g. `/proc/1/nope/x.log` on Linux or a path inside a nonexistent drive on Windows), call `NewLogger`, capture stdout, assert:
- no error returned
- one warning line emitted containing the phrase "falling back to stdout-only"
- the returned logger's core is stdout-only (the tee does not include a file core)

### Why deferred

Deferred from the QA validation cycle because it's a resilience improvement, not a correctness bug. Existing behavior is acceptable (server stays up, stdout logs keep flowing); the fix adds operator-visibility for a misconfiguration case. Scope-correct for a small cleanup PR after the observability branch lands.

---

## M-1f — `requestIDMiddleware` control-character injection test gap

`TestServer_requestIDMiddleware` covers newline (`\n`) and overlength (129 chars) injection cases. It does NOT explicitly exercise `\x00` (NUL), `\x7f` (DEL), `\t` (tab), or space characters. The regex `^[A-Za-z0-9_.:-]{1,128}$` structurally blocks all of them, so this is a test-coverage gap, not a functional bug.

### Proposed fix

Add three sub-cases to the existing injection table test:

```go
{name: "rejects NUL byte", header: "foo\x00bar", wantGenerated: true},
{name: "rejects tab char",  header: "foo\tbar",  wantGenerated: true},
{name: "rejects space",     header: "foo bar",   wantGenerated: true},
```

### Why deferred

30-second fix but not blocking. The regex proves the rejection is total; the missing cases just make the test exhaustive.

---

## Tracked when

- Review: Phase M code-quality review + final integration-gate review + QA validation cycle, 2026-04-23.
- Raised by: REVIEWER + QA subagents during subagent-driven-development flow.
- Related spec decisions: D7 / R1 in `docs/refactoring/observability-upgrade-spec.md`.
