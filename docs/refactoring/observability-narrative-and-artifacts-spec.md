# Midas Observability — Narrative & Artifacts Upgrade Specification

**Version:** 0.1 DRAFT
**Date:** 2026-04-25
**Status:** PHASE 1 + 2.A + 2.B + 2.C IMPLEMENTED — Phase 1 merged 2026-04-25 as `83cbfc7` (manual `?trace=1` / `X-Midas-Trace` triggers, plus the bundle-log-streams follow-up that closes QA-2026-04-25 MINOR-1 — `99-narrate.jsonl` + `99-debug-trace.jsonl` written via a `BundleSink` `zapcore.Core` wrapper). Phase 2.A merged 2026-04-27 as `48a9578` (auto-on-error trigger via `logging.artifact_store.triggers.on_error`; deferred-mode Bundle infrastructure). Phase 2.B merged 2026-04-29 as `fa89aa2` (auto-on-quality-flag trigger via `logging.artifact_store.triggers.quality_flag_threshold`; cleaner cache-hit path included; threshold normalisation + startup Warn; `trace.bundle.promoted` Info line). Phase 2.C merged 2026-05-01 as `6e3ad8f` (always-on knob via `logging.artifact_store.triggers.always`; precedence ladder updated to manual > on_quality_flag > on_error > always; boot Warn `config.artifact_store.always_on_active`; `trace.bundle.promoted` suppressed for trigger=always). Phase 2.D (replay tooling) explicitly deferred — see [Deferred Work](#13-deferred-work-phase-2).
**Builds on:** [`observability-upgrade-spec.md`](./observability-upgrade-spec.md) (v1.1, ALL PHASES COMPLETE 2026-04-23). This spec is additive; it does not modify Phases O/R/S/M/U/D.

---

## 1. Context

The completed observability work (v1.1) gave us:

- Per-request correlation via `request_id` propagated through `internal/observability/logctx`
- Console + rotating-file sinks via `lumberjack`
- 12 calculation-trace points (`stage=wacc`, `stage=fcf_projection`, …) gated by `logging.trace_calculations`

**Remaining pain (raised 2026-04-25):**

> *"In the logs you see what visited, but not what was actually given to it and what came out. I want to see — at each meaningful step — the raw JSON the API returned, the parsed object after we mapped it, the financial data before vs after the cleaner ran, the WACC inputs and the WACC output. Today none of that exists."*

The existing logs are reconstruction-grade for **control flow**. They are not reconstruction-grade for **data flow**. Three things are missing:

1. A **narrative layer** — one Info line per pipeline phase that reads top-to-bottom as the story of one request, with phase + outcome + key fields. Today, request logs are a mix of access lines and per-stage calc traces with no overall through-line.
2. A **Debug-level tracer convention** — every meaningful operation (gateway call, parse, rule application) emits inputs + outputs + elapsed at Debug level when needed.
3. A **per-request artifact bundle** on disk — raw API payloads, parsed domain structs, before/after pipeline snapshots — captured to files so they can be `diff`ed and replayed weeks later.

This spec adds those three things as a single coordinated upgrade.

---

## 2. Goals and Non-Goals

### Goals

- **G1** A reader can scan one request's Info-level logs top-to-bottom and follow what happened: which sources were tried, which fell back, which model was chosen, what the final number was — without needing to enable Debug.
- **G2** When Debug is needed, every meaningful operation produces a structured trace line carrying its inputs, its outputs, and its elapsed time — not just "we visited here."
- **G3** A developer can opt a single request into full artifact capture (raw + parsed payloads at every pipeline stage) by adding a header or query param, and recover the bundle from disk afterwards.
- **G4** Bundles are self-describing — manifest, schema versions, git SHA, redaction list — so they remain useful months later.
- **G5** Zero impact on requests that do not opt in. No payload buffering, no extra disk writes, no extra log lines.
- **G6** Zero new dependencies added to the production-path code outside the new `narrate` and `artifact` packages.

### Non-Goals (Phase 1)

- **Auto-triggering** of bundle capture (on error, on data-quality flag, sampled) — see [Deferred Work](#deferred-work-phase-2).
- **A web UI** for browsing bundles. Bundles are filesystem artifacts; consume with `cat`/`jq`/your editor.
- **Replay tooling** (re-running a request against the saved bundle). Tracked separately.
- **Distributed tracing** (OpenTelemetry / Jaeger). Out of scope; same as v1.1.
- **Centralized aggregation** of artifacts (S3, etc.). Local disk only — this is a developer-debugging tool.

---

## 3. Architecture

Three observability tiers, layered. Each tier serves a different question:

```
┌──────────────────────────────────────────────────────────────┐
│ Tier 1: Narrative log (Info, gated by logging.narrate)       │
│  - One line per pipeline phase                                │
│  - Reads top-to-bottom as the story of one request            │
│  - 17 phases, closed enum (see §5)                            │
│  - Implementation: NEW internal/observability/narrate/        │
│  - Question answered: "What happened in this request?"        │
└──────────────────────────────────────────────────────────────┘
┌──────────────────────────────────────────────────────────────┐
│ Tier 2: Debug-tracer convention (Debug, on when level=debug)  │
│  - Every meaningful op: inputs + outcome + elapsed            │
│  - Convention, not abstraction                                │
│  - Message prefix: "trace.<area>.<op>"                        │
│  - Implementation: documented call-shape, applied across      │
│    existing files — extends scripts/lint-logs.* with prefix   │
│    check                                                      │
│  - Question answered: "What was given to step X, what did     │
│    it produce, how long did it take?"                         │
└──────────────────────────────────────────────────────────────┘
┌──────────────────────────────────────────────────────────────┐
│ Tier 3: Artifact bundle (per-request, gated by manual flag)   │
│  - Raw + parsed payloads + before/after snapshots on disk     │
│  - Self-describing manifest, schema versions, redaction list  │
│  - Implementation: NEW internal/observability/artifact/       │
│  - Question answered: "Show me exactly what came back from    │
│    SEC for AMD on Tuesday and exactly what we did to it."     │
└──────────────────────────────────────────────────────────────┘
```

### Relationship to existing observability (v1.1)

| Concern | v1.1 (existing) | v0.1 of this spec (new) |
|---|---|---|
| Per-request correlation | `logctx.From(ctx)` + `request_id` | unchanged; reused |
| Calculation-stage detail | `calclog.Emit` (12 stages) | unchanged; calclog is the layer *below* narrate. Narrate emits ONE `valuation.computed` line; calclog continues to emit per-stage detail underneath. |
| Console / file sinks | `lumberjack`-rotated file | unchanged; reused |
| Request-path log routing | `logctx.From(ctx)` | unchanged; narrate uses the same logger |
| Payload capture | none | NEW — artifact bundle |
| Pipeline narrative | scattered Info lines | NEW — `narrate.Emitter` |
| Per-op input/output trace | sparse | NEW — Debug-tracer convention |

There is **no overlap and no rewrite** of v1.1 functionality.

---

## 4. Standard Fields (Tier 1 narrate lines)

Every narrate line carries these fields automatically (set on the emitter at construction, not per-call):

| Field | Type | Source | Why |
|---|---|---|---|
| `event` | string, always `"narrate"` | constant | filterable: `event=narrate` is the entire story stream |
| `request_id` | string | `ctx` (set by request-id middleware, v1.1) | correlation — joins narrate to calclog and to the artifact bundle |
| `ticker` | string | handler, set on emitter at handler entry | grep `ticker=AMD event=narrate` reads one request's story |
| `phase` | string | per-call | the state-machine name (closed enum, §5) |
| `outcome` | enum | per-call | `ok` / `fallback` / `partial` / `skipped` / `error` |
| `notes` | string | per-call, optional | free-text detail for things the enum can't carry. Examples: `notes="yahoo cookie expired, switched to finzive"`, `notes="FY2019 missing, extrapolated linearly"` |
| `elapsed_ms` | int | optional, per-call | wall-clock for any phase that did real work |
| `payload_ref` | string | optional, per-call | path to the artifact-bundle file for this phase, when the request is being bundled |

**No `seq` field** — ordering is by `ts` only. Concurrent phases (the fan-out) will interleave by timestamp; this is acceptable.

### Outcome semantics (closed enum)

```
ok       — phase did its job using primary path
fallback — primary path failed, secondary path succeeded; result is usable
partial  — phase produced a result but had to fill gaps
skipped  — phase was a no-op by design (cache miss, ratelimit bypass)
error    — phase failed; downstream may emit fallback/partial to recover
```

`error` does **not** mean the request failed. The request only fails if `response.sent` carries `status >= 500`.

---

## 5. Phase Taxonomy

17 phases, closed set, version-controlled. First segment is the layer (`request`, `auth`, `fetch`, `clean`, `classify`, `growth`, `wacc`, `model`, `valuation`, `crosscheck`, `response`); rest is the operation. Order is the natural order of a successful fair-value request.

| # | phase | When emitted | Phase-specific fields | Typical outcomes |
|---|---|---|---|---|
| 1 | `request.received` | trace middleware after `request_id` assigned | `method`, `path`, `client_ip_hash`, `trace_enabled` (bool) | `ok` |
| 2 | `auth.resolved` | after API-key middleware | `key_id`, `permissions` (count), `auth_source` | `ok` / `error` |
| 3 | `ratelimit.checked` | after rate-limit middleware | `bucket`, `remaining`, `limit` | `ok` / `error` / `skipped` |
| 4 | `handler.entry` | top of `fair_value` handler | `options` (overrides applied: beta, rfr) | `ok` |
| 5 | `cache.lookup` | before fetch | `cache_key`, `hit` (bool), `age_seconds` (if hit) | `ok` (hit) / `skipped` (miss) |
| 6 | `fetch.fanout` | once after coordinator returns | `sources_attempted`, `sources_ok`, `sources_fallback`, `sources_error`, `total_elapsed_ms` | `ok` / `partial` / `error` |
| 7 | `fetch.sec` | per coordinator | `cik`, `filing_count`, `bytes`, `from_cache` (bool) | `ok` / `error` |
| 8 | `fetch.market` | per coordinator | `provider` (yahoo/finzive), `fields_returned`, `auth_refresh` (bool) | `ok` / `fallback` / `error` |
| 9 | `fetch.macro` | per coordinator | `series_count`, `provider` (fred/manual) | `ok` / `fallback` |
| 10 | `clean.normalized` | after datacleaner | `rules_applied`, `adjustments_made`, `flags_raised` | `ok` / `partial` |
| 11 | `classify.industry` | after both classifiers | `sic_label`, `heuristic_label`, `match` (bool), `chosen` | `ok` |
| 12 | `growth.estimated` | after growth service | `stage_count`, `analyst_weight`, `historical_weight`, `g_year_1`, `g_terminal` | `ok` / `partial` |
| 13 | `wacc.computed` | after WACC | `cost_of_equity`, `cost_of_debt`, `weight_equity`, `wacc`, `country_premium_applied` (bool) | `ok` |
| 14 | `model.selected` | router decision | `model`, `reason` | `ok` |
| 15 | `valuation.computed` | after valuation engine | `model`, `fair_value_per_share`, `current_price`, `upside_pct` | `ok` / `error` |
| 16 | `crosscheck.evaluated` | after sanity check | `implied_pe`, `sector_pe`, `deviation_sigma`, `flagged` (bool) | `ok` / `partial` |
| 17 | `response.sent` | trace middleware on response | `status`, `body_bytes`, `total_elapsed_ms`, `artifact_path` (if bundled) | `ok` / `error` |

Cap is **17 lines per request**. A request with three concurrent fetches still emits one `fetch.fanout` summary plus three per-source lines — not 3-per-source-times-N.

---

## 6. Tier 2: Debug-Tracer Convention

Not a package. A convention applied across existing code:

```go
logctx.From(ctx).Debug("trace.<area>.<op>",
    // inputs
    zap.String("input_field_a", ...),
    zap.Int("input_field_b", ...),
    // outputs
    zap.String("outcome", "ok"),
    zap.Int("output_size", ...),
    // timing
    zap.Duration("elapsed", time.Since(start)),
)
```

The message prefix `trace.<area>.<op>` is the convention. Examples:

- `trace.gateway.sec.fetch` — fields: `cik`, `endpoint`, `status`, `bytes`, `from_cache`, `elapsed`
- `trace.gateway.sec.parse` — fields: `bytes_in`, `entries_extracted`, `unknown_tags`, `elapsed`
- `trace.gateway.market.yahoo.auth_refresh` — fields: `prev_crumb_age`, `refresh_reason`, `success`, `elapsed`
- `trace.cleaner.rule.apply` — fields: `rule_name`, `field`, `before`, `after`, `adjustment_made`, `flag_raised`
- `trace.classifier.heuristic.decide` — fields: `sic`, `naics`, `branch`, `confidence`, `chosen_label`
- `trace.growth.blend` — fields: `analyst_g`, `historical_g`, `weight_a`, `weight_h`, `result_g`
- `trace.dcf.year` — fields: `year`, `fcf`, `discount_factor`, `pv`
- `trace.dcf.terminal` — fields: `method`, `terminal_g`, `terminal_value`

### Lint guard

Extend `scripts/lint-logs.{sh,ps1}` (added in v1.1 / Phase S.6) with a check: any `Debug(` call inside the request-path packages (`internal/services/`, `internal/infra/gateways/`) whose first argument is a string literal must match `^trace\.[a-z_]+(\.[a-z_]+)+$`. Free-form Debug messages remain allowed outside the request path.

This is a **convention** check, not a behavior change — Go's compiler can't enforce it, but the lint script can.

---

## 7. Tier 3: Artifact Bundle

### Bundle layout on disk

```
artifacts/
  2026-04-25/                    # date partition (UTC), simplifies retention
    AMD/                         # ticker partition, makes per-ticker forensics easy
      req_01HW8ZQXKR.../         # request-id directory; one bundle per request
        00-manifest.json         # bundle manifest (see §7.2)
        01-request.json          # original HTTP request (headers redacted)
        02-handler-options.json  # parsed ValuationOptions
        05-fetch-sec.raw.json    # raw SEC companyfacts response bytes
        05-fetch-sec.parsed.json # parsed SECCompanyFacts struct
        06-fetch-market.raw.json # raw Yahoo response (after redaction)
        06-fetch-market.parsed.json
        07-fetch-macro.raw.json
        07-fetch-macro.parsed.json
        10-clean-input.json      # FinancialData going into cleaner
        10-clean-output.json     # FinancialData after cleaner
        10-clean-trace.json      # per-rule trace
        11-classify.json         # both classifier outputs + match decision
        12-growth-curve.json     # final multi-stage growth curve
        13-wacc.json             # all WACC inputs + final value
        14-model-selection.json  # router decision + reason
        15-valuation.json        # full DCF working: per-year cashflows, PVs, TV
        16-crosscheck.json       # implied multiples + sector medians
        17-response.json         # final response body sent to client
        99-narrate.jsonl         # full narrate stream for this request
        99-debug-trace.jsonl     # full Debug stream (if level=debug at request time)
```

Numeric prefix matches the phase number in §5. `ls` of any bundle directory reads in pipeline order.

### `00-manifest.json` schema

```json
{
  "bundle_version": "1.0",
  "request_id": "req_01HW8ZQXKR...",
  "ticker": "AMD",
  "trigger": "header",
  "started_at": "2026-04-25T10:23:14.470Z",
  "finished_at": "2026-04-25T10:23:18.221Z",
  "outcome": "ok",
  "phases_recorded": [
    {"phase": "fetch.sec", "files": ["05-fetch-sec.raw.json", "05-fetch-sec.parsed.json"], "bytes": 6212048},
    {"phase": "fetch.market", "files": ["06-fetch-market.raw.json", "06-fetch-market.parsed.json"], "bytes": 51204}
  ],
  "redactions_applied": ["headers.authorization", "headers.cookie", "headers.x-api-key", "yahoo.crumb"],
  "schema_versions": {
    "SECCompanyFacts": "v3",
    "FinancialData": "v7",
    "ValuationResult": "v2"
  },
  "git_sha": "a3f8c1e",
  "build_version": "v0.9.0-rc1"
}
```

### Raw vs Parsed

| Suffix | Content | Source | Mutability |
|---|---|---|---|
| `.raw.json` | Exact bytes from upstream, after header/auth redaction only | `http.Response.Body` captured by gateway adapter via `io.TeeReader` | byte-for-byte preserved |
| `.parsed.json` | `json.Marshal(parsedStruct)` after gateway maps to domain type | Domain struct just before returning to coordinator | Go-encoded, not original |

For the cleaner: `.input` and `.output` are the equivalent suffixes — snapshot before the rule pipeline ran, snapshot after.

### Capture mechanics

Three patterns, one per layer:

1. **Gateways (raw + parsed):** Wrap `http.Response.Body` in `io.TeeReader` so reads dual-stream into the parser AND into the bundle's raw file. After parse, marshal the parsed struct into the parsed file.
2. **Pipeline stages (input + output):**
   ```go
   bundle.Snapshot(ctx, "10-clean-input", financialData)
   cleaner.Apply(...)
   bundle.Snapshot(ctx, "10-clean-output", financialData)
   ```
   No-op when the bundle isn't on `ctx`. Marshal happens in a background goroutine with a bounded queue (snapshot doesn't block the request thread).
3. **Streams (`99-*.jsonl`):** Trace middleware installs a `zapcore.Core` that *also* writes to per-request bundle JSONL files when the trace flag is on. JSONL because line-appendable, grep-friendly.

### Redaction (hard-coded, not config)

| Field path | Action |
|---|---|
| `headers.Authorization` | replaced with `"<redacted>"` |
| `headers.Cookie`, `headers.Set-Cookie` | replaced with `"<redacted>"` |
| `headers.X-API-Key` | replaced with `"<redacted>"` |
| Yahoo `crumb` query param | replaced with `"<redacted>"` |
| FRED `api_key` query param | replaced with `"<redacted>"` |
| Any key matching `(?i)(password|secret|token|bearer)` | replaced with `"<redacted>"` |

A unit test in `internal/observability/artifact/redact_test.go` pins the redaction list against fixtures. Adding a new external API requires adding its auth field to this list and a fixture.

---

## 8. Trigger Gating (Phase 1 = Manual Only)

A bundle is written if and only if:

```
logging.artifact_store.enabled == true
  AND
  one of:
    request has header  X-Midas-Trace: 1
    request has query   ?trace=1
```

That's it. No auto-triggers in Phase 1.

The `request.received` narrate line carries `trace_enabled` (bool) so you always know from the log stream whether a bundle was opened for this request.

If the server-level switch is off (`logging.artifact_store.enabled = false`), the trace flag is honored but produces no bundle (logged as `trace_enabled=false reason=disabled`). This gives a clean kill-switch.

---

## 9. Configuration Surface

New section in `config/config.yaml`:

```yaml
logging:
  # existing v1.1 keys unchanged: level, format, file, trace_calculations,
  # access_log_skip_paths

  # NEW (this spec)
  narrate:
    enabled: true                    # master switch for tier-1 narrate stream
    sample_rate: 1.0                 # 0.0–1.0; sampled per request_id (not per line)
    redact_fields: ["client_ip_hash"] # drop these fields entirely if present

  artifact_store:
    enabled: false                   # master switch for tier-3 bundles (default OFF)
    root_path: "./artifacts"
    retention_days: 7
    max_total_bytes: 5368709120      # 5 GiB cap; oldest evicted first
    triggers:
      manual: true                   # ?trace=1 / X-Midas-Trace header
      # on_error: deferred to Phase 2 (see §13)
      # on_quality_flag: deferred to Phase 2 (see §13)
      # always: deferred to Phase 2 (see §13)
```

Env-var mapping per the existing convention (`LOGGING_NARRATE_ENABLED`, `LOGGING_ARTIFACT_STORE_ENABLED`, etc.).

Default-by-environment (matches v1.1 D3 pattern):

| env | narrate.enabled | artifact_store.enabled |
|-----|---|---|
| development | true | true |
| staging | true | false |
| production | true | false |

Sampling decision is made once, at `request.received`, and stuck on the emitter for the request's lifetime. If sampled out, **zero** narrate lines emit for that request — never a half-told story.

---

## 10. Files Touched

### New files

```
internal/observability/narrate/narrate.go
internal/observability/narrate/narrate_test.go
internal/observability/narrate/phases.go        # phase-name constants
internal/observability/artifact/bundle.go
internal/observability/artifact/bundle_test.go
internal/observability/artifact/manifest.go
internal/observability/artifact/redact.go
internal/observability/artifact/redact_test.go
internal/observability/artifact/reaper.go       # 1-hour-tick retention sweep
internal/observability/artifact/reaper_test.go
internal/api/middleware/trace.go                # parses ?trace=1, opens bundle
internal/api/middleware/trace_test.go
```

### Modified files

| File | Change |
|---|---|
| `internal/api/server.go` | wire trace middleware before requestID |
| `internal/config/config.go` | add `LoggingConfig.Narrate`, `LoggingConfig.ArtifactStore` |
| `internal/api/v1/handlers/fair_value.go` | construct `narrate.Emitter`, emit `handler.entry` + `valuation.computed`; add `bundle.Snapshot` for response |
| `internal/services/datafetcher/coordinator.go` | emit `fetch.fanout`; per-source narrate calls; Debug-tracer convention |
| `internal/infra/gateways/sec/client.go` | TeeReader for raw capture; emit `fetch.sec`; Debug-tracer for parse |
| `internal/infra/gateways/market/*.go` | same shape as SEC |
| `internal/infra/gateways/macro/gateway.go` | same shape as SEC |
| `internal/services/datacleaner/service.go` | emit `clean.normalized`; bundle Snapshot in/out |
| `internal/services/datacleaner/industry/classifier.go` | emit `classify.industry` |
| `internal/services/growth/estimator.go` | emit `growth.estimated` |
| `internal/services/valuation/service.go` | emit `wacc.computed`, `valuation.computed`; bundle snapshot the working result |
| `internal/services/valuation/models/router.go` | emit `model.selected` |
| `internal/services/valuation/crosscheck.go` | emit `crosscheck.evaluated` |
| `scripts/lint-logs.sh` / `.ps1` | add Debug-tracer prefix-shape check |
| `config/config.yaml` | add `logging.narrate`, `logging.artifact_store` sections |
| `docs/API_DOCUMENTATION.md` | document `?trace=1` / `X-Midas-Trace`, narrate format, bundle layout |
| `docs/THESIS.md` | move "Narrative & Artifact capture" from Next Candidate Work into completed Phases when this lands |

`pkg/finance/*` — **NOT modified** (same invariant as v1.1 D7). All emission happens in the service layer.

---

## 11. Rollout Sequence (Phase 1)

Three commits on a feature branch (`feat/observability-narrative`), each independently revertable. Branch follows the same single-merge integration pattern as v1.1 (no PR review flow).

### Commit 1 — Foundation (no behavior change)

- New packages: `internal/observability/narrate/`, `internal/observability/artifact/`
- Trace middleware (parses flag, no-op if `enabled=false`)
- Config struct + defaults
- Reaper goroutine (idle if `artifact_store.enabled=false`)
- Lint-script extension

**Done when:** packages have ≥ 95% coverage; `go test ./...` green; trace middleware is wired but no narrate calls exist yet → no observable behavior change.

### Commit 2 — Gateway-layer capture

- TeeReader in SEC, Market, Macro gateways
- Narrate emissions: `fetch.sec`, `fetch.market`, `fetch.macro`, `fetch.fanout`
- Debug-tracer lines: `trace.gateway.sec.*`, `trace.gateway.market.*`, `trace.gateway.macro.*`
- Bundle file writes for raw + parsed payloads at gateway boundaries

**Done when:** an opt-in request (`?trace=1`) produces `05-fetch-sec.raw.json` + `.parsed.json` (and equivalents for market, macro) on disk; narrate stream contains the four fetch lines.

### Commit 3 — Pipeline-layer capture

- Narrate emissions: `request.received`, `auth.resolved`, `ratelimit.checked`, `handler.entry`, `cache.lookup`, `clean.normalized`, `classify.industry`, `growth.estimated`, `wacc.computed`, `model.selected`, `valuation.computed`, `crosscheck.evaluated`, `response.sent`
- Debug-tracer lines: `trace.cleaner.*`, `trace.classifier.*`, `trace.growth.*`, `trace.dcf.*`
- Bundle snapshots: clean input/output, classify, growth curve, WACC, model selection, valuation, crosscheck, response
- Manifest written + finalized at request end

**Done when:** an opt-in request produces a complete 17-phase narrate stream + a bundle directory containing all phase files + manifest + 99-streams; full integration test passes.

### Independently revertable

- Revert commit 3 → narrate disappears, gateway-level bundles still write (just no pipeline narrative)
- Revert commit 2 → no payloads captured anywhere; narrate also gone (since commit 3 depends on commit 2's gateway emissions for `fetch.*` phases)
- Revert commit 1 → all changes undone, including config keys (which are inert if not consumed)

---

## 12. Testing Strategy

| Layer | What | How |
|---|---|---|
| Unit | `narrate.Emitter` adds standard fields, respects sample_rate | `internal/observability/narrate/narrate_test.go` with `zaptest.NewObserver` |
| Unit | `narrate.Phase*` enum is closed (no string drift) | Compile-time const check + lint test |
| Unit | `artifact.Bundle` no-op when not on ctx | `internal/observability/artifact/bundle_test.go` |
| Unit | `artifact.Bundle.Snapshot` async + bounded queue (no request-thread blocking) | Bench + race detector |
| Unit | Manifest written with all required fields | Golden-file test |
| Unit | Redactor strips every entry in §7.5 fixture | Table test, fail-on-leak |
| Unit | Reaper prunes by age and by total-bytes; skips locked files | `t.TempDir()` + synthetic bundles |
| Unit | Trace middleware: no-op if `artifact_store.enabled=false`, no-op if no flag, opens bundle if flag set | `httptest` table test |
| Middleware | `trace_enabled` field present on `request.received` line | observer assertion |
| Integration | One `GET /api/v1/fair-value/AAPL?trace=1` produces (a) full 17-phase narrate stream with shared `request_id`, (b) complete bundle dir with all expected files, (c) manifest pointing to all of them | `internal/integration/narrate_artifact_test.go` |
| Integration | Same request *without* `?trace=1` produces narrate stream but **no bundle directory** | same file, second test |
| Performance | p99 latency delta < 5% for non-traced requests; < 25% for traced (acceptable since opt-in) | `scripts/load_tester.go` |
| Lint | `scripts/lint-logs.{sh,ps1}` rejects `Debug(` calls in request-path packages without `trace.<area>.<op>` prefix | CI |

Coverage gates:

- `internal/observability/narrate/` ≥ 95%
- `internal/observability/artifact/` ≥ 90%
- No regression on existing package coverage.

---

## 13. Deferred Work (Phase 2)

The following were designed in detail (see conversation thread 2026-04-25) but explicitly deferred. They should be filed as `docs/reviewer/` follow-up items at the time Phase 1 merges:

### Phase 2.A — Auto-trigger: on-error — **SHIPPED 2026-04-27 (merge `48a9578`)**

When a request returns HTTP status >= 500, write a bundle for it even without the `?trace=1` flag.

**Status:** SHIPPED on `feat/observability-narrative-phase2` (5 commits: `f937286`, `f892448`, `b27b317`, `2bca707`, `8dab2e6`). Coverage: artifact 90.5%, middleware 95.1%. Race-detector clean under `-count=3`.

**As built:** the trace middleware now opens a `*Bundle` in **deferred mode** for every request when `logging.artifact_store.triggers.on_error=true` (config knob added). In deferred mode, `OpenDeferredBundle` allocates the bundle but does NOT create a directory; `Snapshot`/`AppendStream`/`SnapshotRaw` calls (and BundleSink-wrapped log entries) buffer into in-memory slices protected by `pendingMu`. At request end, the middleware calls `Promote(TriggerOnError)` on 5xx to flush buffers to disk, OR `Close()` on 2xx/3xx/4xx to dissolve the in-memory state. Manual `?trace=1` / `X-Midas-Trace` triggers still take precedence for the manifest's `trigger` field. The post-`c.Next()` block runs inside a `defer` with `recover()` so panics still trigger Promote.

**New surface:**
- Config: `logging.artifact_store.triggers.on_error bool` (default `false`); `logging.artifact_store.pending_bytes_cap int64` (default 10 MiB).
- Constants: `artifact.MaxStreamLineBytes = 256 * 1024` (per-line cap on the BundleSink stream — defends against one giant Debug payload blowing the buffer).
- Manifest: `notes` field now formats `"write_failures=N queue_drops=M oversize_lines=O"` when any of those counters is non-zero; `outcome` degrades to `"partial"`.
- Trigger constant: `artifact.TriggerOnError Trigger = "on_error"`.

**Spec deviation (intentional):** the spec text said "buffer would-be snapshot calls for every request"; the implementation only opens the deferred bundle when `on_error=true` is configured (avoids per-request allocation when the feature is off). Same observable behavior; lower idle cost.

**Unblocks:** Phase 2.B and 2.C are now each ~one `case` arm in the trace-middleware switch + a config key; the `*Bundle` deferred-mode infrastructure carries them.

### Phase 2.B — Auto-trigger: on-quality-flag — **SHIPPED 2026-04-29 (merge `fa89aa2`)**

When the data cleaner raises one or more flags at-or-above a configurable severity threshold, write a bundle for the request.

**Status:** SHIPPED on `feat/observability-narrative-phase2b` (10 commits: `c4d0194`, `1e38103`, `d90b378`, `02beb0d`, `f8d38af`, `afe75a1`, `6d2e669`, `7180422`, `75aeb17`, `e01fd6d`). Coverage: artifact 90.8%, middleware 95.5%, datacleaner 40.8%. Race-detector clean under `-count=3`.

**As built:** the trace middleware opens a single deferred `*Bundle` when EITHER auto-trigger is configured (no double-allocation when both `on_error=true` and `quality_flag_threshold!=""` are set). The cleaner's `CleanFinancialData` calls `recordQualityFlagCount(ctx, flags)` on BOTH the cache-miss return path AND the cache-hit return path (the latter was REVIEWER's HIGH — would have silently bypassed the trigger for repeat-ticker requests, the dominant production case). At request end the precedence ladder evaluates manual > on_quality_flag > on_error and calls `Promote()` exactly once with the winning trigger.

**New surface:**
- Config: `logging.artifact_store.triggers.quality_flag_threshold string` (default `""` = disabled). Accepts `info`/`low`/`warning`/`medium`/`high`/`critical`. Lowercased + trimmed at config-load via new `internal/config/artifact_triggers.go::normalizeArtifactTriggers`. Unknown values fail closed AND emit a Warn at server boot via `ValidateArtifactTriggers` so typos like `Warning` (uppercase env-var convention) or `warnng` don't silently disable the feature.
- Constants: `artifact.TriggerOnQualityFlag Trigger = "on_quality_flag"`. New `entities.KnownFlagSeverities` canonical slice in `internal/core/entities/data_cleaning.go` — single source of truth for both runtime validation and the `severityRank` exhaustiveness test (adding a new severity without updating both sites fails CI).
- Bundle methods: `(*Bundle).RecordQualityFlagCount(int)` (atomic accumulator, nil-safe, clamps negative to no-op), `(*Bundle).QualityFlagCount() int64`, `(*Bundle).QualityFlagThreshold() string` (read-only, set at construction).
- Operator visibility: new `trace.bundle.promoted` Info log line emitted via `logctx.From(ctx)` on any auto-trigger Promote success, carrying `request_id`, `trigger`, `artifact_path`. Symmetrical with the existing `trace.bundle.promote_failed` Warn — operators can `grep "trace.bundle.promoted" host.log | jq '.artifact_path'` to enumerate auto-bundles.
- Severity rank (`internal/services/datacleaner/quality_flag_severity.go`): collapses two parallel vocabularies — `info`/`low`=1, `warning`/`medium`=2, `high`=3, `critical`=4. At-or-above semantics: a `warning` threshold catches `warning`/`medium`/`high`/`critical` flags.
- Manifest: trigger field enum extended with `on_quality_flag`. `outcome` semantics unchanged.

**Spec deviation (intentional):** spec text said "above a configurable severity threshold"; implementation interprets as "at-or-above" so a `warning` threshold catches `warning` flags themselves (not just `high`/`critical`). This matches operator intent — they want the Warning-level signal too.

**Inherited follow-up:** BUG-012 (artifact bundle drops/oversize-lines surface only postmortem in manifest notes; no runtime Warn) — filed in `docs/bugs/`. Was REVIEWER's B-4 from Phase 2.A; re-surfaced by Phase 2.B QA. Out of scope for this phase because it requires a `Bundle` constructor signature change crossing the artifact package's deliberate logger-free boundary.

### Phase 2.C — Always-on knob — **SHIPPED 2026-05-01 (merge `6e3ad8f`)**

A boolean `logging.artifact_store.triggers.always = true` that bundles every request regardless of flag. Intended for sustained debugging sessions ("flip on for an hour, flip off when done").

**Status:** SHIPPED on `feat/observability-narrative-phase2c` (4 commits: `4d9d5f9`, `b1ab077`, `f4423e6`, `c9e6201`). Coverage: artifact 90.8%, middleware 95.6%, config 50.3%. Race-detector clean under `-count=3`.

**As built:** the trace middleware's `autoTriggerActive` ORs in `cfgA.Triggers.Always`, opening a deferred bundle for every request when set. The post-`c.Next()` defer block's precedence switch gains a third case at the BOTTOM (`case cfgA.Triggers.Always:`), making always the lowest-precedence catch-all. Final ladder: **manual > on_quality_flag > on_error > always** — a 5xx response under `always=true` records `trigger=on_error` in the manifest because postmortem readers want to know WHY a bundle is interesting, not just "the always-knob was on." Promote called exactly once per request.

**New surface:**
- Constants: `artifact.TriggerAlways Trigger = "always"`.
- Config: `ArtifactTriggers.Always bool` (default `false`, env `LOGGING_ARTIFACT_STORE_TRIGGERS_ALWAYS=true`).
- Operator visibility: new boot Warn `config.artifact_store.always_on_active` carrying `effect`, `on_error_also_active`, `quality_flag_threshold` so the operator who flipped the knob and forgot gets a loud reminder at next deploy on stdout AND the rotating file. `ValidateArtifactTriggers` refactored to drop early returns so both the Phase 2.B threshold-typo Warn and the new always-on Warn can fire on the same boot.
- Operator UX: `trace.bundle.promoted` Info line is SUPPRESSED for `trigger=always` (would have flooded host log at 6,000 lines/min at 100 req/s production rate; on_error and on_quality_flag still emit it). On-disk manifest still records `trigger=always` for postmortem grep.

**Spec deviation (intentional):** spec said "Just an OR clause in the trigger evaluator" — implementation matched, and the trigger evaluator structure naturally accommodated the fourth case. No retrofit needed.

**Inherited follow-up:** BUG-013 (deferred-bundle `SetTicker` fails silently because `os.Rename` runs on a directory that doesn't exist yet at handler-time; bundle promotes to `_no-ticker/` instead of `<TICKER>/`). Pre-existing — affects all three auto-triggers (on_error, on_quality_flag, always). Phase 2.C made it the dominant case because every request hits the path. Filed in `docs/bugs/`. Manifest `ticker` field is correct (postmortem grep works), but per-ticker filesystem navigation is broken until BUG-013 lands.

### Phase 2.D — Replay tooling

A CLI command `cmd/replay/main.go` that takes a bundle directory and re-runs the request through the current code, diffing the output against the saved response.

**Reason for deferral:** Whole separate feature; bundles must exist first before there's anything to replay.

---

## 14. Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Raw-payload capture leaks an unredacted secret | Low | High | Hard-coded redaction list with fail-on-leak unit test; redactor runs at gateway boundary before any disk I/O |
| Snapshot goroutine queue overflows under burst | Low | Medium | Bounded queue with drop-and-log behavior; queue size tunable; bundle marks itself `partial` if drops occurred |
| Disk fills despite reaper | Low | Medium | 5 GB total cap evicts oldest first; 7-day age cap; reaper logs every action; `df` check on startup logs warning |
| Narrate stream noisy in dev when not needed | Low | Low | `logging.narrate.enabled = false` opts out entirely; sample_rate provides per-request gating |
| Debug-tracer prefix lint blocks unrelated commits | Low | Low | Lint runs only on changed files; clear error message points to convention doc |
| Schema-version drift makes old bundles unreadable | Medium | Low (acceptable for debug data) | Manifest pins schema versions + git SHA; old bundles can be replayed against the matching code revision |
| Two ways to log the same thing (narrate vs calclog overlap) | Medium | Low (confusion, not bugs) | This spec §3 explicitly defines the layering: narrate is one Info line per phase, calclog is detail underneath. Documented in CLAUDE.md as part of D.2 of v1.1. |

---

## 15. What Stays the Same

- HTTP API contract — no endpoint, header, or response-shape changes beyond `?trace=1` being honored as opt-in
- DCF / DDM / FFO / Revenue Multiple math — unchanged
- Prometheus metrics — unchanged
- Rate limiting and authentication — unchanged
- Scheduler and background jobs — unchanged; still use the fx singleton (narrate is request-path only)
- `pkg/finance/*` — unchanged (v1.1 D7 invariant preserved)
- Existing v1.1 observability — unchanged

---

## 16. Glossary

- **Narrate / narrative log** — the Tier-1 stream of one Info-level line per pipeline phase, emitted via `narrate.Emitter`.
- **Phase** — one of the 17 closed-enum names in §5 (e.g. `fetch.sec`, `wacc.computed`).
- **Outcome** — the closed-enum status of a phase: `ok` / `fallback` / `partial` / `skipped` / `error`.
- **Notes** — free-text field on a narrate line for context the enum can't carry.
- **Debug-tracer** — the convention of emitting Debug log lines with prefix `trace.<area>.<op>` carrying inputs + outputs + elapsed.
- **Artifact bundle** — a per-request directory on disk containing raw payloads, parsed structs, before/after snapshots, manifest, and full narrate/Debug streams.
- **Manifest** — the `00-manifest.json` file at the root of a bundle, describing everything inside it (schema versions, redactions, git SHA, file index).
- **Trace flag** — the `?trace=1` query param or `X-Midas-Trace: 1` header that opts a request into bundle capture.
- **Raw / parsed** — file-name suffixes inside a bundle. Raw = upstream bytes after redaction. Parsed = `json.Marshal` of the domain struct after our gateway parsed it.

---

## Change Log

| Date | Change |
|---|---|
| 2026-04-25 | v0.1 — Initial design draft. Three-tier architecture (narrate / Debug-tracer convention / artifact bundle). 17-phase taxonomy. Manual-trigger only (Phase 1). Phase 2 auto-triggers explicitly deferred — see §13. |
| 2026-04-25 | v0.2 — §7.1 + §7.3 closed: `99-narrate.jsonl` and `99-debug-trace.jsonl` are now written into bundles via a `BundleSink` `zapcore.Core` wrapper installed by trace middleware after a successful bundle open. The wrapper forwards every entry to the wrapped core unchanged AND tees `event=narrate` entries to `99-narrate.jsonl` plus Debug-level entries to `99-debug-trace.jsonl`. Bundle stream files are flushed + closed on `Bundle.Close`. Closes QA-2026-04-25 MINOR-1. |
| 2026-04-27 | v0.3 — §13.A SHIPPED. Phase 2.A (auto-on-error) merged as `48a9578`. Adds: deferred-mode `*Bundle` infrastructure (`OpenDeferredBundle` / `Promote()` / `promoted atomic.Bool`); new `TriggerOnError` constant; new config knobs `logging.artifact_store.triggers.on_error` (default `false`) and `logging.artifact_store.pending_bytes_cap` (default 10 MiB); new exported constant `artifact.MaxStreamLineBytes = 256 KiB` for BundleSink per-line cap; `oversize_lines` counter joins `dropped`/`writeErrors` in manifest `notes`; trace middleware wraps post-`c.Next()` block in `defer`+`recover()` so panics still trigger Promote. REVIEWER round 1 caught 4 HIGH (Promote stream-flush truncation, Close/Promote race goroutine leak, no per-line size cap, panic skips Promote) — all fixed. QA APPROVED-FOR-MERGE with one MINOR (`artifact_path` populated on `response.sent` even when Promote failed) — fixed in `8dab2e6`. Phase 2.B / 2.C / 2.D remain deferred. |
| 2026-04-29 | v0.4 — §13.B SHIPPED. Phase 2.B (auto-on-quality-flag) merged as `fa89aa2`. Adds: new `TriggerOnQualityFlag` constant; new config knob `logging.artifact_store.triggers.quality_flag_threshold` with case+whitespace normalisation at config-load (`internal/config/artifact_triggers.go::normalizeArtifactTriggers`) and startup Warn on unknown values (`ValidateArtifactTriggers`); new `entities.KnownFlagSeverities` canonical slice in core (single source of truth for runtime validation + severity-rank exhaustiveness test); new `(*Bundle).RecordQualityFlagCount`/`QualityFlagCount`/`QualityFlagThreshold` methods; new `internal/services/datacleaner/quality_flag_severity.go` with severity rank that collapses two parallel vocabularies (`info`/`low`=1, `warning`/`medium`=2, `high`=3, `critical`=4); cleaner hook in `CleanFinancialData` runs on BOTH cache-miss AND cache-hit return paths via shared helper; precedence ladder manual > on_quality_flag > on_error with single Promote per request; new `trace.bundle.promoted` Info log line emitted on any auto-trigger Promote success (operator-greppable). REVIEWER round 1 returned APPROVE-WITH-MINOR but flagged 1 HIGH (cleaner cache-hit short-circuit) + 2 MEDIUM (threshold normalisation, operator log line) + 2 LOW — all addressed. QA APPROVED-FOR-MERGE with 2 MINOR (panic test for on_quality_flag — fixed in `e01fd6d`; runtime-Warn-on-drops gap — filed as BUG-012). Phase 2.C / 2.D remain deferred. |
| 2026-05-01 | v0.5 — §13.C SHIPPED. Phase 2.C (always-on knob) merged as `6e3ad8f`. Adds: new `TriggerAlways = "always"` constant; new config knob `logging.artifact_store.triggers.always` (default `false`); precedence ladder updated to **manual > on_quality_flag > on_error > always** with always at the BOTTOM (catch-all); new boot Warn `config.artifact_store.always_on_active` so operators who flipped the knob and forgot get a loud reminder at next deploy; `trace.bundle.promoted` Info line SUPPRESSED for trigger=always to avoid host-log flood (still emitted for on_error/on_quality_flag); `ValidateArtifactTriggers` refactored to drop early returns so both Phase 2.B threshold-typo Warn and Phase 2.C always-on Warn can fire on the same boot. REVIEWER round 1 returned APPROVE-WITH-MINOR with 2 HIGH (log flood, no boot Warn) + 2 MEDIUM (docs gap, missing panic test) + 3 LOW — all 4 in-scope fixes applied (`b1ab077` + `f4423e6` + `c9e6201`). QA APPROVED-FOR-MERGE with 1 MEDIUM (pre-existing deferred-bundle SetTicker bug exposed by always=true; affects all three auto-triggers; filed as BUG-013) + 3 LOW. Phase 2.D (replay tooling) remains the only deferred item. |
