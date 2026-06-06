# TDB-4 — Monitoring metrics + audit logging for datacleaner adjustments

**Status:** OPEN — filed 2026-06-06 (TODO-catalog burn-down pass).
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
- [ ] Audit log line emitted per adjustment (request-scoped, no secrets)
- [ ] Metrics added on the service registry, or deferral recorded with rationale
- [ ] Observability lint guard (`lint-logs`) stays green
