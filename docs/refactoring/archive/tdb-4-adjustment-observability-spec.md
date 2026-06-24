# TDB-4 — Datacleaner adjustment observability (audit log + metrics)

**Status:** DESIGN (ARCH) — 2026-06-08
**Issue:** #4 (TDB-4)
**Tracker:** `docs/reviewer/archive/TDB-4-adjustment-monitoring-and-audit-logging.md`
**Type:** Enhancement (observability) — side-effect-only, no behavior change.
**Engine impact:** NONE. No `CalculationVersion` bump, no `SchemaVersion` bump, no
response-shape change. Logging + metrics are pure observers.

---

## 1. Goal

Close the two standing TODOs at `internal/services/datacleaner/adjustments/liabilities.go:646-647`:

1. **Audit logging** — emit a structured, request-scoped (`logctx`) log line per
   FIRED datacleaner adjustment carrying ticker, rule id, category, type,
   amount, and percentage. No secrets.
2. **Metrics** — a Prometheus counter for adjustment counts on the service-owned
   per-instance registry (PREX-1), with bounded labels only. The per-adjustment
   *latency* histogram is explicitly DEFERRED (rationale in §6.3).

Both halves must keep the load-bearing invariants and both CI lint guards green.

## 2. Non-goals

- No per-adjuster timing/latency histogram (§6.3 — deferred, already-covered).
- No new response field (`cleaning_adjustments` already ships the audit trail to
  API consumers via TDB-11; this issue is about *server-side* observability).
- No change to any adjuster's `Apply*` math, the dispatcher dual-write deletion,
  the ledger projection, or the firing signal.
- No `ticker` Prometheus label (high-cardinality — §5).
- No mutation of `*FinancialData` (§4.4).
- No migration of the datacleaner package off the `lint-logs` Phase-T whitelist
  beyond what this change requires (the change introduces zero singleton-logger
  calls — it uses `logctx.From(ctx)` exclusively).

---

## 3. Grounding (verified in code)

| Fact | Location | Consequence for this design |
|---|---|---|
| `applyActiveAdjustments(ctx, data, cleaningCtx)` is the single orchestrator where all three category dispatchers run and their native `LedgerEntry`/`OverlaySpec` emissions are drained onto `data.AdjustmentLedger` / `data.Overlays`. | `internal/services/datacleaner/service.go:491` | **This is the emit point.** `ctx` is the cleaner's request-scoped context; all fired adjusters converge here exactly once. |
| `adjustmentsFromLedger(data.AdjustmentLedger, data.Overlays, perRuleAdjustmentMeta)` already projects the fired ledger into `[]entities.Adjustment` (`RuleID`, `Category`, `Type`, `Amount`, `Percentage`, `FromAccount`, `ToAccount`, `Reasoning`, `Applied:true`) right before `applyActiveAdjustments` returns. | `service.go:598`, projection in `adjustment_projection.go:339` | The audit log iterates this already-built slice — **one entry == one fired adjuster**, read-only, no re-derivation. |
| Ticker lives on `FinancialData.Ticker`; `data == result.CleanedData` is the arg passed into `applyActiveAdjustments`. `CleaningContext` has **no** ticker field. | `internal/core/entities/financial_data.go:17`; call site `service.go:218` | Log ticker = `data.Ticker`. No new threading. |
| The `service` struct has **no `*zap.Logger` field** and no metrics reference; it uses `calclog.Emitter` for traces. | `service.go:25-38` | The audit log MUST use `logctx.From(ctx)` (correct per convention — needs no struct field). |
| `logctx.From(ctx)` returns the request-scoped logger (or a nop when none is injected — safe in tests / replay). | `internal/observability/logctx/logctx.go:29` | No nil-guards needed; tests without an injected logger silently no-op. |
| `lint-logs` scans `internal/services/**` (datacleaner is NOT whitelisted; only `datacleaner/ai/**` is) for `(s\|c\|r\|e\|g\|h)\.logger\.(...)` AND for `Debug("...")` messages missing a `trace.` prefix (datacleaner is NOT on the Debug whitelist). | `scripts/lint-logs.ps1:38,104-119` | Use `logctx.From(ctx)` (never a receiver `.logger`), and the Debug message MUST start with `trace.`. |
| `lint-prometheus-registers` flags any `prometheus.MustRegister/Register/DefaultRegisterer` or `promauto.NewXxx` outside the allowlist (`metrics/service.go`, `metrics/service_test.go`, `replay/module.go`). | `scripts/lint-prometheus-registers.ps1:39-60` | The new counter MUST be registered inside `metrics/service.go` via the existing `promauto.With(registry)` factory — never from the adjuster/datacleaner layer. |
| `metrics.Service` owns a per-instance `*prometheus.Registry`; all collectors are registered via `promauto.With(registry)` in `initMetrics`. | `internal/services/metrics/service.go:105-130` | Add the counter as a field + one `initMetrics` registration + a `RecordAdjustment(...)` method, exactly like the existing `RecordDataCleaning`. |
| The DI wrapper `di.NewDataCleanerService(cfg, logger, aiSvc, calcEmitter)` already receives `*zap.Logger` (and discards it); `*metrics.Service` is provided in the same fx graph (`container.go:150`). The replay graph also builds both (`replay/module.go:416,432`). | `internal/di/container.go:644`; `replay/module.go` | Metrics can be injected into the datacleaner without a cycle (`metrics` does not import `datacleaner`). |
| The inner `datacleaner.NewDataCleanerService(cfg, aiSvc, calcEmitter)` has **~20+ direct test call sites** across `internal/services/datacleaner/*_test.go` and `internal/integration/*_test.go`, plus `replay/module.go`. | grep | A metrics param added to the inner constructor signature would churn all of them → use an **additive optional setter** (functional option / `WithAdjustmentMetrics`) so existing 3-arg callers compile unchanged. |
| `metrics.RecordDataCleaning(ticker, industry, duration)` exists but is called from **no production path** today (only a unit test). | `service.go:438`; grep | Cleaner-stage latency metering is already provisioned-but-unused → adding a *second* latency surface for adjustments is redundant plumbing (supports §6.3 deferral). |

---

## 4. Design

### 4.1 Emit point — DECISION

Emit the audit log from **`service.go::applyActiveAdjustments`**, iterating the
`allAdjustments` slice that `adjustmentsFromLedger` returns, immediately before
the function returns.

**Why this point and not the dispatcher switch arms (`ProcessAssetAdjustments` /
`ProcessLiabilityAdjustments` / `ProcessEarningsAdjustments`):**

- **Logged exactly once per fired adjuster.** The orchestrator iterates the
  already-deduplicated, already-firing-filtered projection. Emitting from the
  three dispatchers would scatter the logic across three files and three role
  shapes (Restater / OverlayEmitter / FlagEmitter), each with its own
  "did it fire?" predicate — easy to double-emit or miss the C4 FlagEmitter
  fire-signal. The projection has already resolved all of that.
- **`ctx` in scope.** `applyActiveAdjustments` takes the request-scoped `ctx`.
  (The dispatchers also take `ctx`, but see the next point.)
- **`ticker` in scope.** `data.Ticker` is available here; the dispatchers receive
  `data` too, but the projection already collapses the per-rule shape into the
  uniform `entities.Adjustment`, so the orchestrator log needs no per-role
  branching.
- **Zero new derivation.** The projection is the canonical "what fired" list
  (same one TDB-11 surfaces to the API). Logging from a *different* source than
  the API audit trail risks divergence; logging from the *same* projection keeps
  the server log and the API field provably consistent.

### 4.2 Log-line shape

One `Debug` line per element of `allAdjustments`:

```
logctx.From(ctx).Debug("trace.datacleaner.adjustment",
    zap.String("ticker", data.Ticker),
    zap.String("rule_id", adj.RuleID),
    zap.String("category", string(adj.Category)),
    zap.String("type", string(adj.Type)),
    zap.Float64("amount", adj.Amount),
    zap.Float64("percentage", adj.Percentage),
    zap.String("from_account", adj.FromAccount),
    zap.String("to_account", adj.ToAccount),
)
```

| Aspect | Decision | Rationale |
|---|---|---|
| **Message** | `"trace.datacleaner.adjustment"` | MANDATORY `trace.<area>.<op>` prefix — datacleaner is NOT on the `lint-logs` Debug whitelist, so any non-`trace.` Debug message fails the guard. |
| **Logger** | `logctx.From(ctx)` | Request-scoped: inherits `request_id`/`user_id`/`key_id`. NOT a singleton receiver-logger → `lint-logs` singleton check stays green (no `s.logger.` exists to match). Nop in tests/replay with no injected logger → zero noise. |
| **Level** | `Debug` | Volume: up to ~20 adjusters can fire per request; at `Info` this would dominate request logs. `Debug` keeps it opt-in via log level. The line is an *audit/trace* detail, not an operational signal — matches the existing calc-trace convention (`trace.*`). |
| **Fields** | ticker, rule_id, category, type, amount, percentage, from_account, to_account | Mirrors the `entities.Adjustment` projection 1:1 (the same fields TDB-11 exposes). `percentage`/`to_account` may be zero/empty for OverlayEmitters — emitted anyway (structured fields, harmless). |
| **Fired-bool** | Implicit | The projection only contains fired adjusters (`Applied:true`); a separate `fired` field would always be `true`, so it is omitted. Skip paths are NOT logged here (they have no `entities.Adjustment`). |
| **Secrets** | NONE | No API keys, no tokens, no PII. `ticker` is a public symbol and is safe in a log line (it is high-cardinality for *metrics labels* — §5 — but unrestricted in logs). `reasoning` is intentionally omitted to keep the line compact (it is available on the API field and the ledger). |

**Optional summary line (recommended, low-cost):** one additional `Debug` line
per clean call with the count, e.g.
`logctx.From(ctx).Debug("trace.datacleaner.adjustments_summary", zap.String("ticker", data.Ticker), zap.Int("fired_count", len(allAdjustments)))`.
Useful for "0 fired" visibility (the per-adjustment loop emits nothing when
empty). Include it; it is one line and aids operability.

### 4.3 Metrics — DECISION: SHIP the counter, DEFER the histogram

**Ship:** a single counter on the service-owned registry:

```
datacleaner_adjustments_total{rule_id, category, type}
```

- Incremented once per element of `allAdjustments` (same loop as the log).
- Registered in `metrics/service.go::initMetrics` via `promauto.With(registry)`.
- Exposed via a new method `(*metrics.Service) RecordAdjustment(ruleID, category, adjType string)`.

**Wiring (additive, churn-free):** inject the metrics recorder into the
datacleaner via an **optional setter**, NOT a new positional constructor arg, so
the ~20 existing `NewDataCleanerService(cfg, ai, calc)` test callers compile
unchanged and emit no metric (nop). Two acceptable shapes — pick one in the plan:

- **(A) Narrow interface + setter (RECOMMENDED).** Define a tiny port in the
  datacleaner package:
  ```go
  type AdjustmentMetrics interface {
      RecordAdjustment(ruleID, category, adjType string)
  }
  ```
  Add an unexported `adjMetrics AdjustmentMetrics` field (nil-safe) on `service`
  and a `WithAdjustmentMetrics(m AdjustmentMetrics) *service`-style option or a
  functional `Option`. The DI wrapper (`di.NewDataCleanerService`, which already
  receives `*metrics.Service`… actually receives `*zap.Logger`; metrics is in the
  graph) passes the concrete `*metrics.Service` (which satisfies the interface).
  - Keeps the datacleaner package free of a hard import on `metrics` (depends on
    its own small interface — DIP). No import cycle either way (`metrics` does
    not import `datacleaner`).
  - `nil` field → guarded `if s.adjMetrics != nil` → tests/replay record nothing.
- **(B) Functional options on the constructor.** `NewDataCleanerService(cfg, ai,
  calc, opts ...Option)` with `WithAdjustmentMetrics(m)`. Variadic keeps existing
  callers compiling. Equivalent outcome; slightly more ceremony.

**DI change:** `di.NewDataCleanerService` gains a `*metrics.Service` fx param
(already provided in the graph) and applies the option. `replay/module.go::
replayDataCleanerService` is updated the same way (it already constructs a
`*metrics.Service` via `replayMetricsService`) — staying hermetic because that
service owns a fresh per-instance registry (PREX-1), never `DefaultRegisterer`.

### 4.4 No-mutation / side-effect-only guarantee

The log loop and the counter loop both read from `allAdjustments` (a freshly
built local slice) and from `data.Ticker` (read-only). They:

- never write any `*FinancialData` field,
- never append to `data.AdjustmentLedger` / `data.Overlays` (the drain already
  happened earlier in the function; the observers run after the projection),
- never change the function's return values (`allAdjustments`, `allFlags`,
  `totalRulesApplied`, `err`).

Therefore every load-bearing invariant holds **by construction**:

| Invariant | Why it stays green |
|---|---|
| `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC) | DDM math untouched; observers read-only. |
| `TestRecomputeUmbrellas_NoMutation` + shadow byte-identity | `recompute.go` untouched; no `*FinancialData` write. |
| `TestOrchestrator_LedgerOrdering` | Drain order untouched; observers run after, read-only. |
| `TestLedger_BasketSnapshot_ClusterPrediction` / `_T2BS3_RestatedReconstruction` | Ledger contents unchanged. |
| `TestApplyActiveAdjustments_FiringSignalParity_*` | `nativeFired`/`totalRulesApplied` logic untouched. |
| `adjustmentsFromLedger` basket parity | Projection unchanged; we only iterate its output. |

### 4.5 Lint-guard compatibility

- **`lint-logs` (singleton check):** the design adds **zero** `<recv>.logger.Level(...)`
  calls. It uses `logctx.From(ctx).Debug(...)`. The datacleaner `service` struct
  gains no `logger` field. → GREEN.
- **`lint-logs` (Debug-prefix check):** the only new `Debug(...)` messages are
  `"trace.datacleaner.adjustment"` and `"trace.datacleaner.adjustments_summary"`,
  both with the `trace.` prefix. → GREEN.
- **`lint-prometheus-registers`:** the counter is registered exclusively inside
  `metrics/service.go` (allowlisted) via `promauto.With(s.registry)`. The
  datacleaner/adjuster layer calls only `RecordAdjustment(...)` (a method call,
  not a registration) — no `promauto.NewXxx`, no `prometheus.*Register*`,
  no `DefaultRegisterer`. → GREEN.

---

## 5. Cardinality analysis

A Prometheus time series is created per unique label-value combination. The
allowed labels and their bounded domains:

| Label | Domain | Cardinality |
|---|---|---|
| `rule_id` | The fixed adjuster set: A1, A2, A4, A5, A6, A7, B1, B2, B3, C1, C2, C3, C4, C5, C6, C7 (+ the flag-only reviews) | ≤ ~20, fixed by code |
| `category` | `asset_quality`, `liability_completeness`, `earnings_normalization` | 3 |
| `type` | `exclude`, `writedown`, `valuation_allowance`, `reclassify`, `treat_as_debt`, `probability_weighted`, `flag` | ≤ ~7, fixed by the `AdjustmentType` enum |

Worst-case series count ≈ `20 × 3 × 7` = 420, but in practice each `rule_id` maps
to exactly one `(category, type)` pair, so the realized series count ≈ the number
of rules (~16–20). Bounded and small. **`type` is optional** — if a reviewer
prefers a tighter cap, drop it and key on `{rule_id, category}` (~16 series). The
plan should make `type` easy to drop.

**Why `ticker` is forbidden as a label:** the universe of tickers is in the
thousands and grows unbounded as new symbols are valued. A `{ticker, rule_id}`
counter would create thousands × ~20 series, blowing up Prometheus memory and
scrape/query cost — the exact hazard the existing `data_cleaning_duration{ticker,
industry}` histogram already over-pays for. `ticker` belongs in the **log line**
(where high cardinality is free and useful for correlation), never in a metric
label. The audit log is the per-ticker drill-down; the counter is the
aggregate-by-rule operational view. They are complementary by design.

---

## 6. Decisions & rationale

### 6.1 Why Debug, not Info
Up to ~20 lines per request × every fair-value call would flood Info-level logs.
The line is audit/trace granularity (matches the `trace.*` calc-trace family,
which is also Debug-gated). Operators enable Debug for the datacleaner when they
need the per-adjustment audit; aggregate health comes from the counter at Info-
independent scrape time.

### 6.2 Why the orchestrator, not the adjusters
Single convergence point; one emit per fired adjuster; ctx + ticker in scope;
reuses the canonical projection so the server log and the TDB-11 API field cannot
diverge; no per-role fire-signal branching.

### 6.3 Why DEFER the per-adjustment latency histogram
- **Already provisioned at the right granularity.** `metrics.Service` already has
  `data_cleaning_duration_seconds` (the whole-cleaner stage timing). The cleaner
  is a single in-process synchronous pass; per-adjuster timings are sub-
  microsecond arithmetic with no I/O — the operationally meaningful latency is
  the *stage* duration, not per-rule splits.
- **Invasive timing plumbing for low value.** Capturing per-adjuster latency
  needs a `time.Now()`/`time.Since` pair threaded into each of the three
  dispatchers (or into the `Adjuster.Apply` seam across all 16 adjusters),
  widening the blast radius far beyond a read-only observer and touching the
  bit-for-bit-pinned dispatch paths. That cost is disproportionate to the value
  given the stage histogram already exists.
- **The unused `RecordDataCleaning` shows the appetite.** The stage histogram
  isn't even wired into a production call today — adding a *finer* latency
  surface before the coarser one is used is premature.
- **Reversible.** If a future need arises, the stage histogram can be wired first
  (cheap), and only then a per-adjuster split considered. Documented here so the
  deferral is a recorded decision, not an omission.

The counter (counts) is cheap, churn-free, bounded, and answers the "which rules
fire, how often, across the fleet" question the tracker actually asks. We ship it
and defer only the latency half.

---

## 7. Test strategy

| Test | Asserts | Mechanism |
|---|---|---|
| `TestApplyActiveAdjustments_EmitsAuditLogPerFiredAdjustment` (datacleaner pkg) | One `trace.datacleaner.adjustment` log entry per fired adjuster, with the right fields (ticker, rule_id, category, type, amount). | `zaptest`/`observer` core (`go.uber.org/zap/zaptest/observer`) injected via `logctx.Inject(ctx, observedLogger)`; run `applyActiveAdjustments` on a fixture that fires a known set of rules; filter `observer.FilterMessage("trace.datacleaner.adjustment")` and assert count + fields. |
| `TestApplyActiveAdjustments_NoAuditLogWhenNoneFire` | Zero per-adjustment lines when no rule fires (summary line still emitted with `fired_count=0` if the summary line ships). | Same observer, fixture that fires nothing. |
| `TestApplyActiveAdjustments_IncrementsAdjustmentCounter` (only if counter ships) | `datacleaner_adjustments_total{rule_id,category,type}` increments once per fired adjuster. | Construct `metrics.NewServiceWithRegistry(zap.NewNop(), prometheus.NewRegistry())`, inject via the option, run the orchestrator, read back via `testutil.ToFloat64(...)` / registry gather. |
| `TestService_AdjustmentMetrics_NilSafe` | With no metrics injected (nil field), the orchestrator runs and records nothing without panic. | Default 3-arg constructor (no option) → nil recorder → guarded. |
| Named invariants (regression) | All stay GREEN unchanged. | Run the existing suite: `TestDDM_LegacyPath_BitForBit`, `TestRecomputeUmbrellas_NoMutation`, shadow byte-identity (`git diff --quiet internal/integration/testdata/recompute-shadow/`), `TestOrchestrator_LedgerOrdering`, `TestLedger_BasketSnapshot_*`, `TestApplyActiveAdjustments_FiringSignalParity_*`, projection basket parity. |

TDD order: write the observed-log test RED first (no emit yet), make it GREEN with
the loop; then the counter test RED, GREEN with the metric + wiring.

---

## 8. Acceptance criteria

- [ ] One request-scoped `trace.datacleaner.adjustment` Debug line per fired
      adjuster, carrying ticker, rule_id, category, type, amount, percentage,
      from_account, to_account — emitted from `applyActiveAdjustments` via
      `logctx.From(ctx)`. No secrets.
- [ ] (Counter) `datacleaner_adjustments_total{rule_id,category,type}` registered
      on the `metrics.Service` per-instance registry and incremented once per
      fired adjuster; nil-safe when metrics not injected.
- [ ] No `*FinancialData` mutation; all named invariants + shadow byte-identity
      GREEN.
- [ ] `scripts/lint-logs.ps1` and `scripts/lint-prometheus-registers.ps1` both
      exit 0.
- [ ] Existing `NewDataCleanerService(cfg, ai, calc)` callers compile unchanged
      (additive option, no positional-arg break).
- [ ] `GOWORK=off go test ./... -count=1` (worktree) passes.

---

## 9. Open questions (with recommendations)

1. **Include the `type` label on the counter?** — RECOMMEND **yes** (still
   bounded ~7; realized series ≈ #rules since type is rule-fixed). Trivially
   droppable to `{rule_id, category}` if a reviewer wants the tightest cap.
2. **Ship the per-clean summary line (`fired_count`)?** — RECOMMEND **yes** (one
   line; gives "0 fired" visibility the per-item loop can't).
3. **Interface-setter (A) vs functional-options (B) for wiring?** — RECOMMEND
   **(A)** narrow `AdjustmentMetrics` interface + setter: keeps datacleaner off a
   hard `metrics` import (DIP), nil-safe, minimal DI churn.
4. **Counter vs counter+gauge?** — RECOMMEND counter only. Counts answer the
   operational question; a gauge adds nothing here.
