# Midas Observability — Upgrade Specification

**Version:** 1.1
**Date:** 2026-04-23 (v1.0 drafted 2026-04-22; v1.1 refinements R1/R2/R3 applied during execution kickoff)
**Status:** Phase M COMPLETE (2026-04-23) — commits `2f95d12` (impl) + `2da7d7b` (spec-review fixes) + `52ad5ef` (quality-review fixes). Phases O + R + S landed earlier the same day. Phase U next (console polish + docker-compose cleanup). Three noted field-completeness follow-ups tracked in `docs/reviewer/M1`. Work continues on branch `feat/observability` in worktree `.worktrees/feat-observability`.
**Scope:** Make every HTTP request's full execution flow reconstructible from logs. Persist logs locally for development, rely on container log drivers in staging / production. Add calculation-level tracing so each DCF valuation's math can be inspected end-to-end.

---

## Table of Contents

1. [Context](#context)
2. [Goals and Non-Goals](#goals-and-non-goals)
3. [Verified Problems in Current State](#verified-problems-in-current-state)
4. [Requirements](#requirements)
5. [Architecture Decisions](#architecture-decisions)
6. [Log Format Samples](#log-format-samples)
7. [Configuration Surface](#configuration-surface)
8. [Phase Plan](#phase-plan)
9. [Files Touched](#files-touched)
10. [Testing Strategy](#testing-strategy)
11. [Rollout & Rollback](#rollout--rollback)
12. [Risks and Mitigations](#risks-and-mitigations)
13. [What Stays the Same](#what-stays-the-same)
14. [Glossary](#glossary)

---

## Context

Midas currently logs via `zap` to `stdout` only. The `dcf_logs:/app/logs` Docker volume is declared but never written to. Middleware sets an `X-Request-ID` header but never attaches it to a child logger, so individual handler and service log lines cannot be correlated to a request. Valuation math (WACC, growth, FCF projection, terminal value, cross-check) emits sparse logs with no structured fields tying them to the triggering request.

**Primary user pain:** *"I can't see in logs what happened in the flow of a request — we log stuff but it's not kept anywhere."*

Verification confirms the claim is correct and worse than described — see [Verified Problems](#verified-problems-in-current-state).

---

## Goals and Non-Goals

### Goals

- **G1** Every log line on the request path carries `request_id` plus relevant identity (`user_id`, `key_id`) and domain (`ticker`) fields.
- **G2** Local developers get readable, colored, console-formatted logs persisted to a rotating file on disk.
- **G3** Staging and production containers emit single-line JSON to `stdout` only — the container runtime's log driver is authoritative.
- **G4** The valuation engine emits structured, request-correlated trace entries for every math stage (growth, WACC, FCF projection, terminal value, sanity check) so a single request's calculation can be reconstructed from logs alone.
- **G5** No change to Midas's public HTTP API; `X-Request-ID` behavior remains additive and backward-compatible.
- **G6** Test ergonomics unchanged — `zap.NewNop()` and `zaptest.NewLogger(t)` continue to work without per-test wiring.

### Non-Goals

- Distributed tracing with OpenTelemetry / Jaeger (candidate for a follow-up; not in this iteration).
- Shipping logs to SaaS or self-hosted aggregators (Loki, ELK, Datadog). The JSON stream must be *ready* for shipment; choosing a destination and operator is separate work.
- Changing Prometheus metrics cardinality or emitting additional business metrics.
- A web UI, dashboard, or TUI for browsing logs. "Human-readable" here means *console-formatted output fit for `tail -f` and terminal reading*.
- Adding a new "audit log" persistence concept distinct from structured application logs.

---

## Verified Problems in Current State

Findings from a repository audit on 2026-04-22. Cited with file:line.

| # | Finding | Evidence |
|---|---------|----------|
| P1 | Zap writes to stdout only. No file sink, no rotation, no async writer. | `internal/di/container.go:234` — `config.OutputPaths = []string{"stdout"}` |
| P2 | Docker Compose declares `dcf_logs:/app/logs` volume and `Dockerfile` creates `/app/logs/`; **no code writes there**. Volume is dead weight. | `docker-compose.yml:60`, `docker-compose.prod.yml:71`, `Dockerfile:68` |
| P3 | `requestIDMiddleware` is registered twice — method form at `server.go:75` and anonymous form at `server.go:122–130`. Second overwrites the first. | `internal/api/server.go:75,122-130` |
| P4 | `generateRequestID` is `fmt.Sprintf("req-%d", time.Now().UnixNano())` with a `TODO` to use UUIDs. Collisions possible under bursty load. | `internal/api/server.go:561-564` |
| P5 | Gin `loggingMiddleware` does not include `request_id` in its access line. | `internal/api/server.go:265-277` |
| P6 | `request_id` is set on `c.Set("request_id", ...)` but never attached to a child zap logger. Handler and service logs (`h.logger`, `s.logger`) run independently of request state. | `internal/api/v1/handlers/fair_value.go:154,171,219,269,306,351` |
| P7 | No `LoggerFromContext` helper exists. Services hold singleton `*zap.Logger` from fx; they cannot access request-scoped fields. | whole `internal/services/**` tree |
| P8 | `loggingMiddleware` runs after `gin.Recovery()`. Recovered panics never reach the access line. | `internal/api/server.go:136-139` |
| P9 | No distributed tracing. `Span` / `Tracer` grep returned only false positives. | grep result |
| P10 | Calculation stages (WACC, growth, FCF, terminal value, cross-check) emit sparse logs; those that exist include no request identifier. | `internal/services/valuation/`, `pkg/finance/` |

---

## Requirements

### Functional

- **F1** A middleware generates (or accepts from client) a UUID request ID, attaches a child logger carrying `request_id` to `context.Context`, and echoes the ID on the `X-Request-ID` response header.
- **F2** Every handler and every request-path service retrieves its logger via `logctx.From(ctx)` rather than using the fx-provided singleton.
- **F3** One access-log line per request, emitted after response, containing: method, path, route, status, latency_ms, request_id, user_id (if authed), key_id (if authed), client_ip, bytes_out, and (if set) an error_code.
- **F4** The access-log middleware captures panics recovered by `gin.Recovery` — they produce a log line tagged `level=error` with the panic value and the request_id.
- **F5** A `logging` config section controls:
  - `level` (debug|info|warn|error)
  - `format` (console|json)
  - `file.enabled` (bool)
  - `file.path`, `file.max_size_mb`, `file.max_backups`, `file.max_age_days`, `file.compress`
  - `trace_calculations` (bool) — gates Phase M calculation logs at info level; otherwise they emit at debug.
  - `access_log_skip_paths` (string list) — paths like `/metrics`, `/health`, `/ready` never emit access lines at info, still counted in metrics.
- **F6** Default configuration per environment:
  - `environment=development` → `format=console`, `file.enabled=true`, `level=debug`, `trace_calculations=true`.
  - `environment=staging` or `environment=production` → `format=json`, `file.enabled=false`, `level=info`, `trace_calculations=false`.
  - These defaults are overridable by env vars or `config.yaml`.
- **F7** Every math stage in the valuation pipeline emits a structured log entry with inputs and outputs (see Phase M for the full list of trace points).
- **F8** Calculation trace entries always include `request_id`, `ticker`, and a `stage` field (e.g. `stage=wacc`, `stage=growth`, `stage=fcf_projection`).
- **F9** The existing `datacleaner.LogAdjustments` and `datacleaner.LogFlags` feature flags continue to work but route through the same request-scoped logger when invoked on the request path.
- **F10** `X-Request-ID` request header: accepted if it matches a permissive ID regex (`^[A-Za-z0-9_.:-]{1,128}$`) to prevent log injection; rejected silently and replaced with a generated UUID if malformed.

### Non-Functional

- **NF1** Hot-path overhead < 5% p99 latency vs baseline (measure with the existing `scripts/load_tester.go`).
- **NF2** `go test ./...` continues to pass with no test requiring real file I/O. The `NewLogger` factory is unit-tested in isolation into a `t.TempDir()`.
- **NF3** No secrets logged. Continue using `safeKeyPrefix` for any API-key values. Calculation logs include numerical inputs/outputs — no PII.
- **NF4** Coverage targets: `internal/observability/logctx` ≥ 95%; `internal/api` access-log middleware ≥ 90%; valuation trace points covered by existing valuation tests (no regression).
- **NF5** Zero breaking changes to `config.yaml`. Missing `logging.*` keys inherit the defaults in F6 based on `environment`.
- **NF6** Zero breaking changes to any call signature that currently takes a `*zap.Logger`. The refactor introduces `logctx` *in addition to*, not *instead of*, existing logger wiring.
- **NF7** File-rotation writes are synchronous. Disk-full conditions drop log lines silently — valuation correctness is never gated on logging success.

---

## Architecture Decisions

### D1 — Custom `zapcore.Core` tee, not a single `zap.Config`

**Decision:** Replace `zap.Config.Build()` with an explicit `zapcore.NewTee(...)` that composes 1–2 cores:

- `stdoutCore` — always on. Uses JSON encoder in staging/prod, console encoder in dev.
- `fileCore` — present only when `logging.file.enabled=true`. Wraps `gopkg.in/natefinch/lumberjack.v2` for size-based rotation. Always uses JSON encoder (machine-parseable for later shipment).

**Why:** `zap.Config` cannot express "console to stdout + JSON to file" or "rotate this sink." Tee + custom encoders unlocks both.

**Trade-off:** More code (~40 LoC vs ~10). Accepted — it's isolated to `NewLogger` and well-covered by tests.

### D2 — Request-scoped logger in `context.Context`

**Decision:** New package `internal/observability/logctx` with:

```go
package logctx

func Inject(ctx context.Context, l *zap.Logger) context.Context
func From(ctx context.Context) *zap.Logger  // returns zap.NewNop() on miss
```

The singleton `*zap.Logger` from fx remains the base. Middleware does `base.With(zap.String("request_id", rid), ...)` and injects the child. Handlers and services call `logctx.From(c.Request.Context())` or `logctx.From(ctx)`.

**Why:** The codebase already threads `context.Context` everywhere (ports/adapters pattern). This is the lowest-friction way to propagate request-scoped fields without changing any function signature.

**Trade-off:** `logctx.From` on a background context returns a no-op logger. Startup, scheduler, and background jobs must continue to use the fx-provided singleton directly — they are *not* request-scoped. We draw the line clearly: request path → `logctx.From(ctx)`; non-request path → fx singleton.

### D3 — Environment-driven defaults, user-overridable

**Decision:** Config defaults for `logging.*` are a function of `environment`:

| env | format | file.enabled | level | trace_calculations |
|-----|--------|--------------|-------|---------------------|
| development | console | true  | debug | true  |
| staging     | json    | false | info  | false |
| production  | json    | false | info  | false |

Explicit `config.yaml` or env-var values always win.

**Why:** Matches the user's explicit constraint — file sinks are for local dev only; containers rely on the Docker (or systemd) log driver for staging/prod.

**Trade-off:** Developers running locally with `environment=production` get no file logs unless they override. Acceptable — production behavior is the safer default.

### D4 — Docker Compose cleanup

**Decision:** Remove the `dcf_logs` named volume and the `- dcf_logs:/app/logs` bind from `docker-compose.yml` and `docker-compose.prod.yml`. Keep `mkdir /app/logs` in the Dockerfile (cheap, harmless) in case a future local-like override wants it.

**Why:** The volume is misleading — it implies logs are persisted when they are not, and the new design explicitly uses stdout-only in containers.

**Trade-off:** One-time breaking change for anyone who had scripts reading from `dcf_logs`. Mitigation: call out in release notes; `docker logs <container>` is the replacement.

### D5 — Middleware order and consolidation

**Decision:** Final order:

```
1. requestIDMiddleware        // validates/generates ID, injects child logger, adds header
2. httpMetricsMiddleware      // existing, unchanged
3. gin.Recovery()             // unchanged
4. accessLogMiddleware        // renamed from loggingMiddleware; logs once per request; captures panic line
5. CORS                       // unchanged
6. rateLimitMiddleware        // unchanged
7. authMiddleware             // route-level, unchanged
```

Drop the duplicate anonymous requestID middleware at `server.go:122–130`.

**Why:** requestID must run first so Recovery and every subsequent middleware logs with the correlation ID. accessLog must run after Recovery so recovered panics are captured in a log line.

**Trade-off:** Existing test fixtures that assume the current order may need updating. Covered in QA checklist.

### D6 — UUIDv4 for request IDs

**Decision:** Use `github.com/google/uuid` — `uuid.NewString()`.

**Why:** Standard, zero-CGO, 0.5 MB of code, no new transitive dependencies. UUIDv4 is collision-safe for this workload. Alternative (`crypto/rand` + hex) is fine but costs slightly more code.

**Trade-off:** One extra dependency. Accepted.

### D7 — Calculation tracing lives in the service layer; `pkg/finance` stays logger-free

**Decision (refined in v1.1, R1):** Add `stage` and input/output zap fields at each math boundary — **but only inside the service layer** (`internal/services/valuation/*`, `internal/services/growth/*`, `internal/services/datacleaner/*`). The pure math libraries under `pkg/finance/*` (e.g. `wacc.Calculate(inputs Inputs) (*Result, error)` at `pkg/finance/wacc/wacc.go:50`) **do not** take a `context.Context` and must not. They remain pure, side-effect-free, trivially testable functions.

The trace happens at the caller: service code emits a `calclog.Emit(ctx, "wacc", ...)` immediately after invoking `wacc.Calculate(...)` with the same inputs and outputs. Same observability payoff, cleaner layering.

Level gating stays as originally specified:
- `trace_calculations=true` → info (visible by default in dev).
- `trace_calculations=false` → debug (filtered out in prod unless level is lowered).

A thin helper `calclog.Emit(ctx, stage, fields...)` lives in `internal/observability/calclog` to make level gating uniform and to enforce the `stage` field.

**Why:** Keeps `pkg/finance` logger-free — the domain's clean-architecture invariant ("no external deps in core math") is preserved. The service layer is already the "controller" of the math; adding trace emission there is a natural fit. A dedicated tracer or threading a logger into `wacc`/`dcf`/`growth` functions would be premature coupling.

**Trade-off:** Trace fields must be materialized in the service layer after each math call (slight duplication of inputs). Accepted: services already hold those inputs to pass into the math function, so it's a no-cost re-use.

### D8 — No metrics changes

**Decision:** Leave `metrics.HTTPMetricsMiddleware` and Prometheus counters/histograms unchanged. Add only the access-log-skip-path behavior at the log layer, not the metric layer.

**Why:** Prometheus cardinality is fine and the user did not ask for metric changes. Logs ≠ metrics.

---

## Log Format Samples

### Console format (development)

```
2026-04-22T09:14:02.138+0300  INFO  api/server.go:312  access
  request_id=7f9b3e0a-5c18-4b81-9c55-4a2e6b7d0a11
  method=GET path=/api/v1/fair-value/AAPL route=/api/v1/fair-value/:ticker
  status=200 latency_ms=487 user_id=u_47 key_id=k_3 bytes_out=2184

2026-04-22T09:14:02.212+0300  INFO  valuation/service.go:188  calc
  request_id=7f9b3e0a-5c18-4b81-9c55-4a2e6b7d0a11
  stage=wacc ticker=AAPL rf=0.0421 beta_raw=1.18 beta_blume=1.12 erp=0.055 crp=0.000 wacc=0.1039

2026-04-22T09:14:02.233+0300  INFO  valuation/service.go:231  calc
  request_id=7f9b3e0a-5c18-4b81-9c55-4a2e6b7d0a11
  stage=fcf_projection ticker=AAPL years=7 growth_rates=[0.12,0.11,0.10,0.08,0.06,0.05,0.04]
  fcf_series=[110.2B,122.3B,134.5B,145.3B,154.0B,161.7B,168.2B]
```

### JSON format (staging / production)

```json
{"ts":"2026-04-22T06:14:02.138Z","level":"info","msg":"access","request_id":"7f9b3e0a-5c18-4b81-9c55-4a2e6b7d0a11","method":"GET","path":"/api/v1/fair-value/AAPL","route":"/api/v1/fair-value/:ticker","status":200,"latency_ms":487,"user_id":"u_47","key_id":"k_3","bytes_out":2184}
{"ts":"2026-04-22T06:14:02.212Z","level":"info","msg":"calc","request_id":"7f9b3e0a-5c18-4b81-9c55-4a2e6b7d0a11","stage":"wacc","ticker":"AAPL","rf":0.0421,"beta_raw":1.18,"beta_blume":1.12,"erp":0.055,"crp":0.000,"wacc":0.1039}
```

Parse JSON locally with `docker logs midas-api | jq .` or ship it as-is.

---

## Configuration Surface

New `logging` section in `config/config.yaml`. All keys optional; defaults in D3.

```yaml
logging:
  level: info                   # debug | info | warn | error
  format: json                  # json | console
  trace_calculations: false     # emit math-stage logs at info (true) vs debug (false)
  access_log_skip_paths:
    - /metrics
    - /health
    - /ready
  file:
    enabled: false
    path: ./logs/midas.log
    max_size_mb: 100
    max_backups: 10
    max_age_days: 14
    compress: true
```

Env var mapping (Viper nested keys → underscore, as elsewhere in `internal/config/config.go`):

| Key | Env var |
|-----|---------|
| `logging.level` | `LOGGING_LEVEL` |
| `logging.format` | `LOGGING_FORMAT` |
| `logging.trace_calculations` | `LOGGING_TRACE_CALCULATIONS` |
| `logging.file.enabled` | `LOGGING_FILE_ENABLED` |
| `logging.file.path` | `LOGGING_FILE_PATH` |
| `logging.file.max_size_mb` | `LOGGING_FILE_MAX_SIZE_MB` |
| `logging.file.max_backups` | `LOGGING_FILE_MAX_BACKUPS` |
| `logging.file.max_age_days` | `LOGGING_FILE_MAX_AGE_DAYS` |
| `logging.file.compress` | `LOGGING_FILE_COMPRESS` |

Existing top-level `log_level` is retained as an alias for `logging.level` during the deprecation window.

---

## Phase Plan

Four sequential phases, each independently mergeable. Within a phase, TDD: red test → green implementation → refactor.

### Phase O — Foundation (plumbing)

Outcome: file sink working, request IDs real, child logger reachable.

| # | Work | Owner | Done when |
|---|------|-------|-----------|
| O.1 | Add deps `gopkg.in/natefinch/lumberjack.v2`, `github.com/google/uuid` | BACKEND | `go.mod`, `go.sum` updated; `go build ./...` passes |
| O.2 | Create `internal/observability/logctx` (Inject, From) | BACKEND | ≥ 95% coverage; no-op logger on miss |
| O.3 | Create `internal/observability/calclog` (Emit helper) | BACKEND | Unit tested; enforces `stage` field |
| O.4 | Extend `internal/config/config.go` with `LoggingConfig`; add defaults keyed on `environment` | BACKEND | Table-driven test covers dev/staging/prod defaults and explicit override |
| O.5 | Rewrite `NewLogger` → tee of stdout core + optional file core; console/json encoders | BACKEND | Unit test with `t.TempDir()` asserts file sink + rotation; existing tests pass |
| O.6 | Replace `generateRequestID` with `uuid.NewString`; add injection-safe header validation | BACKEND | Table test: valid/invalid/missing headers |

### Phase R — Request correlation

Outcome: every request-path log line carries `request_id`.

| # | Work | Owner | Done when |
|---|------|-------|-----------|
| R.1 | Consolidate `requestIDMiddleware` (single implementation); build child logger; inject into `c.Request.Context()` via `logctx.Inject` | BACKEND | Only one place in code sets `request_id` zap field |
| R.2 | Rename `loggingMiddleware` → `accessLogMiddleware`; one line per request; includes error_code and panic capture | BACKEND | Tests for 2xx / 4xx / 5xx / panic paths |
| R.3 | Reorder middleware chain per D5 | BACKEND | Chain order asserted in a table test |
| R.4 | Migrate handlers (`fair_value.go`, `auth.go`, `health.go`, `performance.go`) to `logctx.From(c.Request.Context())` | BACKEND | Grep `h\.logger\.` in handlers → 0 hits on request path |

### Phase S — Service-layer migration

Outcome: valuation, datacleaner, growth, datafetcher, gateways all log with `request_id`.

| # | Work | Owner | Done when |
|---|------|-------|-----------|
| S.1 | Valuation service (`internal/services/valuation/service.go`, `router.go`, `crosscheck.go`) | BACKEND | All request-path logs routed through `logctx.From(ctx)` |
| S.2 | Data cleaner (`internal/services/datacleaner/service.go`, `adjustments/*`, `flagging/*`) | BACKEND | Existing `LogAdjustments`/`LogFlags` gated entries gain `request_id` |
| S.3 | Growth estimator (`internal/services/growth/estimator.go`) | BACKEND | Same |
| S.4 | Data fetcher coordinator (`internal/services/datafetcher/coordinator.go`) | BACKEND | Same |
| S.5 | Gateways (`internal/infra/gateways/sec`, `market`, `macro`) — on request path only | BACKEND | Background fetchers retain singleton logger; request-triggered fetches use context logger |
| S.6 | CI grep guard: `rg 's\.logger\.(Info|Warn|Error)' internal/services/` must return only pre-approved non-request-path sites listed in a whitelist file | BACKEND | Grep guard wired as a `make lint-logs` target |

### Phase M — Math tracing (the new piece you asked for)

Outcome: a single DCF valuation emits a structured trace of every calculation stage, correlated to its request.

Trace points (minimum). All emit sites are in the **service layer**, not in `pkg/finance/*` (per D7 / R1). The "Emit from" column names the Go file that *calls* the math primitive and emits the trace; `pkg/finance/*` functions themselves remain untouched.

| Stage | Emit from (service layer) | Fields |
|-------|---------------------------|--------|
| `data_fetch` | `internal/services/datafetcher/coordinator.go` | ticker, sources_tried, sources_ok, duration_ms |
| `data_clean_summary` | `internal/services/datacleaner/service.go` | ticker, adjustments_count, flags_count, quality_score, quality_grade |
| `industry_classification` | `internal/services/datacleaner/industry/classifier.go` | ticker, sic, naics, sector, industry, model_hint |
| `model_selection` | `internal/services/valuation/models/router.go` | ticker, model_chosen (`dcf`/`ddm`/`ffo`/`revenue_multiple`), reason |
| `growth` | `internal/services/growth/estimator.go` | ticker, source (`analyst`/`historical`/`blended`), growth_rates[], roic_ceiling, sustainability |
| `wacc` | `internal/services/valuation/service.go` (post `wacc.Calculate`) | ticker, rf, beta_raw, beta_blume, beta_unlevered, beta_relevered, erp, crp, tax_rate, cost_of_debt, wacc |
| `fcf_projection` | `internal/services/valuation/service.go` (post `dcf.Project`) | ticker, years, growth_rates[], fcf_series[] |
| `terminal_value` | `internal/services/valuation/service.go` (post `dcf.TerminalValue`) | ticker, gordon_tv, exit_multiple_tv, averaged_tv, terminal_growth |
| `discount` | `internal/services/valuation/service.go` (post `dcf.Discount`) | ticker, pv_explicit, pv_terminal, enterprise_value |
| `equity_bridge` | `internal/services/valuation/service.go` (equity-bridge block) | ticker, cash, debt, minority_interest, preferred, equity_value, diluted_shares, per_share |
| `cross_check` | `internal/services/valuation/crosscheck.go` | ticker, implied_pe, implied_ev_ebitda, sector_median_pe, sector_median_ev_ebitda, flags |
| `final` | `internal/services/valuation/service.go` (`CalculateValuation` exit) | ticker, dcf_per_share, tangible_per_share, method, version, quality_score, warnings_count |

All entries emit via `calclog.Emit(ctx, "<stage>", ...)`. Gated by `logging.trace_calculations` per D7. No file under `pkg/finance/` is modified by this phase.

| # | Work | Owner | Done when |
|---|------|-------|-----------|
| M.1 | Implement `calclog.Emit` with stage-field enforcement | BACKEND | Covered by unit test |
| M.2 | Wire trace points in the table above | BACKEND | Each trace point has a unit-test assertion that it fires on a happy-path valuation |
| M.3 | Integration test: one `GET /api/v1/fair-value/AAPL` produces all 12 stage entries, all carrying the same `request_id` | QA | Test parses log output into JSON and asserts the set |

### Phase U — UX polish (readability) and Phase D — Docs

Outcome: the stream is legible and the docs reflect reality.

| # | Work | Owner | Done when |
|---|------|-------|-----------|
| U.1 | Console encoder tuned for readability (color in TTY, aligned keys, trimmed caller path) | BACKEND | Sample screen matches [Log Format Samples](#log-format-samples) |
| U.2 | `scripts/launch_staging.sh` / `.ps1`: assert the container starts cleanly with `environment=staging` and file sink disabled | BACKEND | Scripts exit non-zero if file sink appears |
| U.3 | Remove `dcf_logs` volume from `docker-compose.yml` and `docker-compose.prod.yml` (D4) | BACKEND | Grep for `dcf_logs` in repo returns only docs references |
| D.1 | `docs/API_DOCUMENTATION.md` → new "Observability" section | BACKEND | Section describes request ID, log format, rotation knobs, troubleshooting |
| D.2 | `CLAUDE.md` → conventions: request-path logs use `logctx.From(ctx)` | BACKEND | Section added under Code Style |
| D.3 | `AGENTS.md` → Tier 4: add `internal/observability/` row | BACKEND | Row present |
| D.4 | `docs/THESIS.md` → move "Observability" from Next Candidate Work into completed Phases table when Phase M merges | BACKEND | Updated entry |

---

## Files Touched

New files:

- `internal/observability/logctx/logctx.go`
- `internal/observability/logctx/logctx_test.go`
- `internal/observability/calclog/calclog.go`
- `internal/observability/calclog/calclog_test.go`

Modified files (estimated):

- `internal/di/container.go` — rewrite `NewLogger`
- `internal/config/config.go` — add `LoggingConfig`, env-driven defaults
- `internal/api/server.go` — middleware consolidation, access log, reorder
- `internal/api/v1/handlers/fair_value.go` — `h.logger` → `logctx.From(ctx)`
- `internal/api/v1/handlers/auth.go` — same
- `internal/api/v1/handlers/health.go` — same
- `internal/api/v1/handlers/performance.go` — same
- `internal/services/valuation/service.go` — migrate logs; add trace points
- `internal/services/valuation/models/router.go` — add trace
- `internal/services/valuation/crosscheck.go` — add trace
- `internal/services/growth/estimator.go` — add trace
- `internal/services/datacleaner/service.go` — migrate logs; add trace
- `internal/services/datafetcher/coordinator.go` — migrate logs; add trace
- `internal/infra/gateways/sec/client.go` — request-path logs via ctx
- `internal/infra/gateways/market/*.go` — same where on request path
- `internal/infra/gateways/macro/gateway.go` — same
- **`pkg/finance/*` — NOT modified** (per D7 / R1). All calc-trace emission happens in the service-layer callers above.
- `config/config.yaml` — add `logging` section
- `config.env.example` — add `LOGGING_*` entries
- `docker-compose.yml`, `docker-compose.prod.yml` — remove `dcf_logs`
- `scripts/lint-logs.ps1` + `scripts/lint-logs.sh` (R2) — static check that request-path services use `logctx.From(ctx).*` rather than the singleton `s.logger.*`; invoked in CI and via a documented pre-commit command (no Makefile in this repo)
- `go.mod`, `go.sum` — `lumberjack.v2`, `google/uuid`
- `.gitignore` — add `.worktrees/` (one-time; keeps the main worktree's `git status` clean when feature branches use project-local worktrees)

Docs: `CLAUDE.md`, `AGENTS.md`, `docs/THESIS.md`, `docs/API_DOCUMENTATION.md`.

---

## Testing Strategy

| Layer | What | How |
|-------|------|-----|
| Unit | `logctx.Inject` / `From` round-trip; no-op on miss | `internal/observability/logctx/logctx_test.go` |
| Unit | `calclog.Emit` level gating; stage field present | `internal/observability/calclog/calclog_test.go` |
| Unit | `NewLogger` writes to file in temp dir; rotation triggers at threshold | Uses `t.TempDir()` + `lumberjack` default |
| Unit | `LoggingConfig` defaults per environment | Table test in `internal/config/config_test.go` |
| Middleware | RequestID injection, child logger reachable via `c.Request.Context()` | `httptest` + `observer` zap sink |
| Middleware | Access log line on 2xx / 4xx / 5xx / panic paths | `httptest` + panic injection |
| Middleware | Chain order (requestID → metrics → recovery → accessLog → CORS → rateLimit → auth) | Table test listing applied middleware |
| Integration | Valuation run emits all 12 stage entries with identical `request_id` | `internal/integration/observability_test.go` |
| Property | UUID collision resistance (1e6 IDs, no duplicates) | `gopter` |
| Performance | p99 latency delta vs baseline < 5% on `scripts/load_tester.go` | Recorded in the PR description |
| Regression | Full `go test ./...` still green | CI |

Coverage gates (CI):

- `internal/observability/` ≥ 95%
- `internal/api` (middleware) ≥ 90%
- Existing package coverage must not regress.

---

## Rollout & Rollback

### Rollout (local-only repo — no GitHub remote, per R3)

Each phase is one logical commit on `feat/observability`. When all phases are complete and QA-verified, the branch is merged or fast-forwarded into `master` as a single integration step via the `superpowers:finishing-a-development-branch` skill. There is no PR review flow; the REVIEWER subagent dispatched mid-phase provides the review gate.

1. Commit Phase O. Observable effect: none in staging/prod (file sink off by default); local dev gets file logs at `./logs/midas.log`.
2. Commit Phase R. Observable effect: every access log line now carries `request_id`. Response header `X-Request-ID` now a real UUID.
3. Commit Phase S. Observable effect: handler + service logs correlate.
4. Commit Phase M. Observable effect: in dev, every valuation emits 12 calc-trace lines; in prod, same lines at debug level — invisible unless level is lowered.
5. Commit Phase U + Phase D. Observable effect: cleaner console output locally; `dcf_logs` volume removed from compose.

Each commit is independently revertable via `git revert <sha>` on the feature branch before integration.

### Rollback

- Phase O revert restores stdout-only behavior.
- Phase R revert restores non-correlated logs (but keeps real UUIDs from O.6 — benign).
- Phase S revert leaves handlers correlated, services not. Benign.
- Phase M revert removes trace points. No runtime impact.
- Phase U/D revert restores Docker volume declarations. No runtime impact.

---

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Widespread log-site migration misses a site | Medium | Low — log line just lacks `request_id` | Phase S.6 grep guard prevents regressions |
| Console encoder breaks CI log parsers | Low | Low | Staging/prod default stays JSON; CI runs in `environment=test` which also defaults to json |
| File sink enabled accidentally in production | Low | Medium — disk fill | Default is `false` when `environment != development`; require explicit override to enable |
| `lumberjack` ungraceful behavior on permission-denied | Low | Medium | Fallback: WriteSyncer wraps `lumberjack` inside a `zapcore.Lock`; on error we log once to stdout and continue stdout-only |
| UUID dependency churn | Very low | Very low | `google/uuid` is Google-maintained, minimal surface |
| Readers of current `req-<nanos>` IDs parse them elsewhere | Unknown | Unknown | No internal callers detected; add a note to the release changelog |

---

## What Stays the Same

- HTTP API contract — no endpoint, header, or response-shape changes beyond `X-Request-ID` now being a real UUID.
- DCF / DDM / FFO / Revenue Multiple math — unchanged.
- Prometheus metrics — unchanged.
- Rate limiting and authentication — unchanged.
- Scheduler and background jobs — unchanged; still use the fx-provided singleton logger.
- Test harness — `zap.NewNop()` and `zaptest.NewLogger(t)` continue to work with no fixture changes.

---

## Glossary

- **Request-scoped logger** — a `*zap.Logger` created per HTTP request by `.With(zap.String("request_id", rid), ...)`, injected into `context.Context`.
- **Access log** — one line per request, emitted after response, containing the HTTP-transaction summary.
- **Calculation trace** — the set of structured log entries emitted from valuation math stages, tagged with a `stage` field.
- **Stage** — a named checkpoint in the valuation pipeline (e.g. `wacc`, `fcf_projection`, `terminal_value`). See the [Phase M table](#phase-m--math-tracing-the-new-piece-you-asked-for).
- **Console encoder / JSON encoder** — Zap's two output encodings. Console is human-readable and colorable; JSON is single-line machine-parseable.
- **File sink** — the rotating file output wired via `lumberjack`. Off by default; on in local dev.

---

## Change Log

| Date | Change |
|------|--------|
| 2026-04-22 | v1.0 — Initial draft. Scope locked with user: structured request correlation + file sink (local only) + calc tracing + console/json format. Phases O / R / S / M / U / D defined. |
| 2026-04-23 | v1.1 — Execution-kickoff refinements. R1: keep `pkg/finance/*` logger-free; emit all calc traces from the service-layer caller (verified `wacc.Calculate(inputs Inputs)` takes no ctx and shouldn't). R2: replace "Makefile `lint-logs` target" with `scripts/lint-logs.{ps1,sh}` — repo has no Makefile. R3: reword rollout in terms of commits, not merges — repo has no GitHub remote; integration is a single branch merge via `finishing-a-development-branch`. Worktree added at `.worktrees/feat-observability`; `.gitignore` entry added alongside. |
