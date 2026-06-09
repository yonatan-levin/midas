# TDB-4 — Monitoring metrics + audit logging for datacleaner adjustments

**Status:** IMPLEMENTED 2026-06-08 (branch `worktree-tdb-4-adjustment-observability`) — VERIFIER VERIFIED (suite 0 FAIL, shadow exit 0, both lint patterns green, named invariants green); REVIEWER APPROVE_WITH_NITS (no-mutation confirmed, cardinality bounded, registry/nil-safe clean); QA PASS. Filed 2026-06-06 (TODO-catalog burn-down pass).
**Priority:** P2 — Tier 2 (operational value).
**Type:** Enhancement (observability).
**Mirrored as GitHub issue:** `[TDB-4]` (yonatan-levin/midas).
**Origin:** 2026-06-06 burn-down — catalog "Monitoring & Observability" (`adjustments/liabilities.go:641-642`).

---

## Context

Two standing TODOs at `adjustments/liabilities.go:641-642`:
- "Add monitoring metrics for calculation performance"
- "Log calculation details for audit trail"

The **logging** half is a quick, low-risk win (a structured `logctx`-scoped line per adjustment). The **metrics** half needs a small design decision: which Prometheus metrics, and registered on the service-owned registry (per the PREX-1 convention) rather than the default registerer.

## Scope / Tasks

| ID | Task | File | Effort |
|---|---|---|---|
| TDB-4.1 | Structured audit log line per fired adjustment (ticker, rule id, amount, semantics) via `logctx.From(ctx)` | `adjustments/*.go` | S |
| TDB-4.2 | Prometheus metric(s) for adjustment calc latency/counts on the per-instance registry, OR record an explicit deferral | `services/metrics`, adjusters | M |

## Acceptance
- [x] Audit log line emitted per adjustment (request-scoped, no secrets) — `trace.datacleaner.adjustment` per fired adjuster via `logctx.From(ctx)`; pinned by `TestApplyActiveAdjustments_EmitsAuditLogPerFiredAdjustment`
- [x] Metrics added on the service registry, or deferral recorded with rationale — counter `datacleaner_adjustments_total{rule_id,category,type}` on the per-instance registry SHIPPED; per-adjuster latency histogram DEFERRED (rationale below)
- [x] Observability lint guard (`lint-logs`) stays green — `trace.` prefix + `logctx.From(ctx)` only; `lint-prometheus-registers` also green (counter registers only in the allowlisted `metrics/service.go`)

---

## Design (ARCH, 2026-06-08) — Status: OPEN

- **Spec:** `docs/refactoring/spec/tdb-4-adjustment-observability-spec.md`
- **Implementer plan:** `docs/refactoring/implementations/tdb-4-adjustment-observability-implementation-plan.md`

### Decisions
- **Emit point:** `internal/services/datacleaner/service.go::applyActiveAdjustments`
  — the single orchestrator where all three category dispatchers converge and the
  native ledger is drained. It iterates the already-built `adjustmentsFromLedger`
  projection (one entry == one fired adjuster), with `ctx` and `data.Ticker` in
  scope. Read-only; no per-role fire-signal branching; no divergence from the
  TDB-11 API audit field (same projection source).
- **Log shape:** one `logctx.From(ctx).Debug("trace.datacleaner.adjustment", …)`
  per fired adjuster — fields `ticker, rule_id, category, type, amount,
  percentage, from_account, to_account`; **Debug** level (≤ ~20 lines/request);
  no secrets. Message uses the mandatory `trace.<area>.<op>` prefix (datacleaner
  is NOT on the `lint-logs` Debug whitelist). Plus an optional per-clean
  `trace.datacleaner.adjustments_summary` (`fired_count`) line.
- **Metrics ship-vs-defer:** **SHIP** the counter
  `datacleaner_adjustments_total{rule_id, category, type}` on the per-instance
  `metrics.Service` registry (PREX-1), wired into the datacleaner via a nil-safe
  `AdjustmentMetrics` option (no churn to the ~20 existing 3-arg test callers).
  **DEFER** the per-adjustment latency histogram — the stage-level
  `data_cleaning_duration` already exists (and isn't even wired into a production
  path yet), and per-adjuster timing would require invasive plumbing into the
  bit-for-bit-pinned dispatch paths for low value. If the counter wiring proves
  messy in review, the log half ships alone and the counter becomes a documented
  deferral.
- **Cardinality:** bounded labels only (`rule_id` ~16–20, `category` 3, `type`
  ~7); realized series ≈ #rules. **`ticker` is NOT a label** (thousands,
  unbounded) — it lives in the log line, where high cardinality is free.
- **No-mutation guarantee:** observers read the freshly-built `allAdjustments`
  slice + `data.Ticker` only; no `*FinancialData` write. DDM bit-for-bit,
  recompute-shadow byte-identity, ledger-ordering, firing-signal parity, and
  basket tests stay green by construction.
- **Lint guards:** both `lint-logs` (singleton + Debug-prefix) and
  `lint-prometheus-registers` stay green — `logctx.From(ctx)` only (no
  `s.logger.*`); counter registered solely in the allowlisted
  `metrics/service.go`.

---

## Implementation status (2026-06-08 — VERIFIED / APPROVE_WITH_NITS / PASS)

Both halves SHIPPED per the plan (counter wiring stayed clean → no deferral of Task B):

- **`service.go::applyActiveAdjustments` (`:646-666`):** after `adjustmentsFromLedger`
  builds the `allAdjustments` projection and before return, emits one
  `log.Debug("trace.datacleaner.adjustment", …)` per fired adjuster (fields
  `ticker, rule_id, category, type, amount, percentage, from_account, to_account`) +
  a `trace.datacleaner.adjustments_summary` (`fired_count`) line, via
  `log := logctx.From(ctx)`. Read-only over the projection — no `*FinancialData` write.
- **`metrics/service.go`:** counter `datacleaner_adjustments_total{rule_id,category,type}`
  registered via `promauto.With(registry)` (per-instance, never `DefaultRegisterer`) +
  a `RecordAdjustment(ruleID, category, adjType)` method. `getMetricsCount()` 28→29.
- **Wiring:** nil-safe `AdjustmentMetrics` port + variadic `WithAdjustmentMetrics`
  option on `NewDataCleanerService` (the ~30 existing 3-arg callers compile
  unchanged; datacleaner takes NO hard `metrics` import — DIP). DI injects
  `*metrics.Service`; replay leaves it nil (hermetic).
- **TODOs resolved:** the two at `adjustments/liabilities.go:646-647` deleted (replaced
  with a pointer comment to the orchestrator emit point).
- **Tests:** `applyactive_audit_log_test.go` (observed zap core — per-fired-adjuster
  emission + field cross-check + 0-when-none-fire + counter-increment + nil-safe) +
  `metrics/service_test.go::TestRecordAdjustment`.

**DEFERRED (documented, out of scope):** per-adjustment latency histogram — the
stage-level `data_cleaning_duration` already exists (and is not even wired into a
production path yet); per-adjuster timing would require invasive plumbing into the
bit-for-bit-pinned dispatch paths for low value.

**Validation:** `GOWORK=off go build/vet ./...` exit 0; full `go test ./... -count=1`
exit 0 (0 FAIL); shadow gate exit 0 (byte-identical); DDM bit-for-bit /
recompute-no-mutation / ledger-ordering / firing-signal-parity / basket invariants
green; both lint patterns (singleton + Debug-prefix; no `DefaultRegisterer`) green.

## Deferred NITs (REVIEWER 2026-06-08, advisory, non-blocking)
- `service.go:647-648` index-then-copy loop (`for i := range … { adj := allAdjustments[i] }`)
  could be `for _, adj := range allAdjustments` — functionally identical; not worth a re-spin.
- The counter `type` label is droppable to `{rule_id, category}` if a tighter series cap
  is ever wanted (current choice is bounded and fine).
