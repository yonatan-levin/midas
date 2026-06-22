# TDB-4 — Implementer plan: datacleaner adjustment observability

**Spec:** `docs/refactoring/spec/tdb-4-adjustment-observability-spec.md`
**Issue:** #4 (TDB-4)
**Branch / worktree:** `worktree-tdb-4-adjustment-observability`
**Runtime:** Go. **ALWAYS run with `GOWORK=off`** (worktree has its own `go.mod`).

> Side-effect-only change. No `CalculationVersion` / `SchemaVersion` bump. No
> response-shape change. Audit log is the MUST-HAVE; the counter ships only if its
> wiring stays clean (it does — see §3). If a reviewer objects to the DI churn,
> the counter can be dropped without affecting the log half.

---

## 0. Preconditions

```powershell
cd "C:\Users\Yonatan Levin\Documents\Programming\Projects\FinTech\Strade\midas\.claude\worktrees\tdb-4-adjustment-observability"
$env:GOWORK = "off"
git status              # clean, on worktree-tdb-4-adjustment-observability
go build ./...          # baseline green
```

Capture a baseline shadow-clean check (must stay byte-identical at the end):

```powershell
git diff --quiet internal/integration/testdata/recompute-shadow/   # exit 0 expected
```

---

## 1. Task A — Audit log (RED → GREEN). MUST-HAVE.

### A.1 RED — write the observed-log test first

**File:** `internal/services/datacleaner/applyactive_audit_log_test.go` (new)

- Build a `service` via the existing test constructor pattern
  (`NewDataCleanerService(cfg, &mockAIServiceDataCleaner{}, nil)` — see
  `service_test.go` / `applyactive_firingsignal_parity_test.go` for the fixture
  helpers).
- Create an observed zap core:
  ```go
  core, observed := observer.New(zap.DebugLevel)   // go.uber.org/zap/zaptest/observer
  ctx := logctx.Inject(context.Background(), zap.New(core))
  ```
- Use a `FinancialData` fixture (with `Ticker` set) + `CleaningContext` that fires
  a known adjuster set (reuse the A1/A2/etc. fixtures already used by the native-
  emission tests). Call `s.applyActiveAdjustments(ctx, data, cleaningCtx)`.
- Assert:
  - `observed.FilterMessage("trace.datacleaner.adjustment").Len()` == number of
    fired adjusters (== `len(returnedAdjustments)`).
  - For one known entry: fields `ticker`, `rule_id`, `category`, `type`, `amount`
    present and correct (`entry.ContextMap()`).
- Second test `..._NoAuditLogWhenNoneFire`: fixture that fires nothing →
  `FilterMessage("trace.datacleaner.adjustment").Len()` == 0 (and, if the summary
  line ships, exactly one `trace.datacleaner.adjustments_summary` with
  `fired_count=0`).

Run RED — confirm it fails (no emit yet):
```powershell
$env:GOWORK="off"; go test ./internal/services/datacleaner/ -run TestApplyActiveAdjustments_EmitsAuditLog -count=1
```

### A.2 GREEN — emit the log

**File:** `internal/services/datacleaner/service.go`, in `applyActiveAdjustments`,
**after** `allAdjustments := adjustmentsFromLedger(...)` (line ~598) and **before**
`return allAdjustments, allFlags, totalRulesApplied, nil`:

```go
// TDB-4: per-fired-adjustment audit trail. Request-scoped via logctx so each
// line inherits request_id/user_id/key_id. Read-only over the already-built
// projection (one entry == one fired adjuster) — no *FinancialData mutation,
// so every load-bearing invariant holds by construction. Debug level: up to
// ~20 lines per request. trace.<area>.<op> message satisfies lint-logs.
log := logctx.From(ctx)
for i := range allAdjustments {
    adj := allAdjustments[i]
    log.Debug("trace.datacleaner.adjustment",
        zap.String("ticker", data.Ticker),
        zap.String("rule_id", adj.RuleID),
        zap.String("category", string(adj.Category)),
        zap.String("type", string(adj.Type)),
        zap.Float64("amount", adj.Amount),
        zap.Float64("percentage", adj.Percentage),
        zap.String("from_account", adj.FromAccount),
        zap.String("to_account", adj.ToAccount),
    )
}
log.Debug("trace.datacleaner.adjustments_summary",
    zap.String("ticker", data.Ticker),
    zap.Int("fired_count", len(allAdjustments)),
)
```

- Add imports to `service.go`: `"go.uber.org/zap"` (already imported) and
  `"github.com/midas/dcf-valuation-api/internal/observability/logctx"`.
- Do NOT add a `logger` field to the `service` struct. Do NOT use any
  `s.logger.*` form (would trip `lint-logs`).

Run GREEN:
```powershell
$env:GOWORK="off"; go test ./internal/services/datacleaner/ -run TestApplyActiveAdjustments_EmitsAuditLog -count=1
```

**Commit 1** (log half — shippable on its own):
```
feat(datacleaner): per-fired-adjustment audit log via logctx (#4)

Emit one request-scoped trace.datacleaner.adjustment Debug line per fired
adjuster from applyActiveAdjustments, iterating the adjustmentsFromLedger
projection. Read-only observer; no FinancialData mutation, no behavior change.

Refs: docs/refactoring/spec/tdb-4-adjustment-observability-spec.md
Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
```

---

## 2. Task B — Counter metric (RED → GREEN). SHIP IF CLEAN.

### B.1 Add the metric to `metrics.Service` (allowlisted file)

**File:** `internal/services/metrics/service.go`

- Add a field next to the other counters:
  ```go
  // Datacleaner adjustment counter (TDB-4). Bounded labels only — NEVER
  // ticker (high-cardinality; lives in the audit log instead).
  datacleanerAdjustmentsTotal *prometheus.CounterVec
  ```
- Register it in `initMetrics` via the existing `factory := promauto.With(registry)`:
  ```go
  s.datacleanerAdjustmentsTotal = factory.NewCounterVec(
      prometheus.CounterOpts{
          Name: "datacleaner_adjustments_total",
          Help: "Total datacleaner adjustments applied, by rule/category/type",
      },
      []string{"rule_id", "category", "type"},
  )
  ```
- Add the recorder method:
  ```go
  // RecordAdjustment counts one fired datacleaner adjustment (TDB-4).
  func (s *Service) RecordAdjustment(ruleID, category, adjType string) {
      s.datacleanerAdjustmentsTotal.WithLabelValues(ruleID, category, adjType).Inc()
  }
  ```
- Bump the approximate `getMetricsCount()` return (28 → 29) for consistency.

> All registration stays inside `metrics/service.go` (allowlisted by
> `lint-prometheus-registers`). No `promauto.NewXxx` / `DefaultRegisterer`
> anywhere else.

**Metrics unit test** (`internal/services/metrics/service_test.go`): register on a
fresh `prometheus.NewRegistry()`, call `RecordAdjustment("A1","asset_quality","exclude")`
twice, assert `testutil.ToFloat64(...)` == 2 for that label set.

### B.2 Define the narrow port + nil-safe wiring (datacleaner side)

**File:** `internal/services/datacleaner/service.go`

- Add the interface (DIP — keeps datacleaner off a hard `metrics` import):
  ```go
  // AdjustmentMetrics records per-fired-adjustment counters (TDB-4). The
  // production impl is *metrics.Service; nil is valid (records nothing).
  type AdjustmentMetrics interface {
      RecordAdjustment(ruleID, category, adjType string)
  }
  ```
- Add a nil-able field on `service`: `adjMetrics AdjustmentMetrics`.
- Add a functional option (keeps the 3-arg constructor signature intact):
  ```go
  type Option func(*service)
  func WithAdjustmentMetrics(m AdjustmentMetrics) Option { return func(s *service) { s.adjMetrics = m } }
  ```
- Change the inner constructor signature to variadic-optional so existing callers
  compile unchanged:
  ```go
  func NewDataCleanerService(cfg *config.Config, aiSvc ai.AIService, calcEmitter *calclog.Emitter, opts ...Option) (DataCleanerService, error)
  ```
  …and apply `for _, opt := range opts { opt(svc) }` before `return svc, nil`.
  (All ~20 existing `n(cfg, ai, nil)` test callers stay valid — variadic absorbs
  zero options.)

### B.3 Increment in the orchestrator loop

In the same `applyActiveAdjustments` loop added in A.2, add inside the `for`:
```go
if s.adjMetrics != nil {
    s.adjMetrics.RecordAdjustment(adj.RuleID, string(adj.Category), string(adj.Type))
}
```

### B.4 DI + replay wiring

**File:** `internal/di/container.go` — `NewDataCleanerService` wrapper (line ~644):
- Add `metricsService *metrics.Service` to its fx params (already provided in the
  graph at `container.go:150`).
- Pass the option through:
  ```go
  return datacleaner.NewDataCleanerService(cfg, aiSvc, calcEmitter,
      datacleaner.WithAdjustmentMetrics(metricsService))
  ```
- `*metrics.Service` already has `RecordAdjustment`, so it satisfies
  `datacleaner.AdjustmentMetrics`.

**File:** `internal/observability/replay/module.go` —
`replayDataCleanerService` (line ~432): it already constructs a
`*metrics.Service` via `replayMetricsService`. Either pass
`datacleaner.WithAdjustmentMetrics(<the replay metrics.Service>)` (hermetic — its
registry is a fresh per-instance one) OR leave it nil (replay does not scrape
metrics). RECOMMEND leave nil for replay to keep the replay graph minimal and
make the hermeticity argument trivially obvious. Document the choice in the
function comment.

### B.5 Counter test in the datacleaner package

**File:** `internal/services/datacleaner/applyactive_audit_log_test.go` (extend)
- `TestApplyActiveAdjustments_IncrementsAdjustmentCounter`: construct
  `metrics.NewServiceWithRegistry(zap.NewNop(), prometheus.NewRegistry())`, build
  the cleaner with `WithAdjustmentMetrics(ms)`, run the firing fixture, gather the
  registry, assert `datacleaner_adjustments_total` == fired count.
- `TestService_AdjustmentMetrics_NilSafe`: default 3-arg constructor (no option) →
  run firing fixture → no panic, returns normally.

**Commit 2** (counter half):
```
feat(metrics): datacleaner_adjustments_total counter on service registry (#4)

Bounded labels {rule_id,category,type} — never ticker (cardinality). Wired
into the datacleaner via a nil-safe AdjustmentMetrics option; *metrics.Service
satisfies it. DI + replay updated. Registration stays in metrics/service.go
(lint-prometheus-registers allowlist).

Refs: docs/refactoring/spec/tdb-4-adjustment-observability-spec.md
Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
```

---

## 3. Task C — Remove the resolved TODOs

**File:** `internal/services/datacleaner/adjustments/liabilities.go:646-647`
Delete the two TODO comments now that audit logging (A) and metrics (B) ship.
(Keep this in Commit 1 or 2; do NOT leave the TODOs dangling once the work lands.)

---

## 4. Validation (full gate — run all)

```powershell
cd "C:\Users\Yonatan Levin\Documents\Programming\Projects\FinTech\Strade\midas\.claude\worktrees\tdb-4-adjustment-observability"
$env:GOWORK = "off"

# Build + full suite
go build ./...
go test ./... -count=1

# Named invariants (explicit)
go test ./internal/services/valuation/models/ -run TestDDM_LegacyPath_BitForBit -count=1
go test ./internal/services/datacleaner/ -run "TestRecomputeUmbrellas_NoMutation|TestOrchestrator_LedgerOrdering|TestApplyActiveAdjustments_FiringSignalParity" -count=1
go test ./internal/integration/ -run "TestLedger_BasketSnapshot_ClusterPrediction|TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction" -count=1

# Shadow byte-identity gate (MUST exit 0)
git diff --quiet internal/integration/testdata/recompute-shadow/

# BOTH lint guards (MUST exit 0)
.\scripts\lint-logs.ps1
.\scripts\lint-prometheus-registers.ps1
```

All must be green. If `lint-logs` flags a Debug message, confirm it begins with
`trace.`. If `lint-prometheus-registers` flags a line, confirm the registration is
only in `metrics/service.go`.

---

## 5. Rollback / scope-control

- **Counter wiring proves messier than expected** (e.g. an unforeseen import edge
  or DI test breakage): ship Commit 1 (log) only and convert Task B into a
  documented deferral in the spec + tracker, with the same cardinality/registry
  rationale already written. The log half is the MUST-HAVE and stands alone.
- **Latency histogram is OUT OF SCOPE** for this issue (deferred per spec §6.3 —
  the stage-level `data_cleaning_duration` already exists).

---

## 6. Commit template

```
<type>(<area>): <summary> (#4)

<body — what + why; note "side-effect-only observer, no behavior change,
no CalculationVersion/SchemaVersion bump">

Refs: docs/refactoring/spec/tdb-4-adjustment-observability-spec.md
Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
```

Do NOT commit or push unless the user asks. Branch is already isolated.
