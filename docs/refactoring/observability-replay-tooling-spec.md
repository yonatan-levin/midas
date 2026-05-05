# Midas Observability — Replay Tooling Specification (Phase 2.D)

**Version:** 0.3
**Date:** 2026-05-05
**Status:** R0 + R1 + R2 SHIPPED on master (R0+R1 merge `8a9878f` 2026-05-03; R2 merge `e4d2fb2` 2026-05-05). R3 (parallel batch + filter flags + JSON golden tests + perf benches) deferred to a future dispatch — tracked at `docs/reviewer/RPL2-r2-followups.md` for the 15 advisory follow-ups surfaced by R2's REVIEWER + QA gates.
**Builds on:** [`observability-narrative-and-artifacts-spec.md`](./observability-narrative-and-artifacts-spec.md) (v0.6, Phases 1 + 2.A + 2.B + 2.C SHIPPED). This spec is additive; it consumes artifact bundles produced by Phases 1–2.C and does not modify their on-disk format.

---

## Table of Contents

1. [Context](#1-context)
2. [Goals and Non-Goals](#2-goals-and-non-goals)
3. [Verified Problems / Motivation](#3-verified-problems--motivation)
4. [Requirements](#4-requirements)
5. [Architecture Decisions](#5-architecture-decisions)
6. [Architecture](#6-architecture)
7. [CLI Contract](#7-cli-contract)
8. [Configuration Surface](#8-configuration-surface)
9. [Phase Plan](#9-phase-plan)
10. [Files Touched](#10-files-touched)
11. [Tasks by Agent](#11-tasks-by-agent)
12. [Testing Strategy](#12-testing-strategy)
13. [Rollout & Rollback](#13-rollout--rollback)
14. [Risks and Mitigations](#14-risks-and-mitigations)
15. [What Stays the Same](#15-what-stays-the-same)
16. [Spec Updates](#16-spec-updates)
17. [Glossary](#17-glossary)
18. [Change Log](#18-change-log)

---

## 1. Context

Phases 1 + 2.A + 2.B + 2.C of the observability narrative & artifacts upgrade have shipped. Every qualifying request now leaves on disk a self-describing bundle directory:

```
artifacts/<UTC-date>/<TICKER>/req_<id>/
  00-manifest.json          # bundle_version, request_id, ticker, trigger,
                            #   started_at, finished_at, outcome, phases_recorded[],
                            #   redactions_applied[], schema_versions{}, git_sha
  01-request.json           # original HTTP request (headers redacted)
  02-handler-options.json   # parsed ValuationOptions (override_beta, override_rf)
  05-fetch-sec.raw.json     # SEC EDGAR companyfacts response bytes (redacted)
  05-fetch-sec.parsed.json  # ports.SECCompanyFacts struct after parser
  06-fetch-market.raw.json  # Yahoo / Finzive raw payload
  06-fetch-market.parsed.json
  07-fetch-macro.raw.json   # FRED raw payload
  07-fetch-macro.parsed.json
  10-clean-input.json       # entities.FinancialData before cleaner
  10-clean-output.json      # entities.FinancialData after cleaner
  10-clean-trace.json       # per-rule trace
  11-classify.json          # both classifiers + match decision
  12-growth-curve.json      # multi-stage growth curve
  13-wacc.json              # WACC inputs + final value
  14-model-selection.json   # router decision + reason
  15-valuation.json         # full DCF working
  16-crosscheck.json        # implied multiples vs sector medians
  17-response.json          # final response body sent to client
  99-narrate.jsonl          # full narrate stream
  99-debug-trace.jsonl      # full Debug stream (if level=debug at request time)
```

The bundle is the most complete artifact the system produces. Today nothing reads it back.

**The user pain that motivates this phase:**

> *"I just changed the growth-blend weights. I don't want to re-fetch SEC for AAPL/MSFT/AMD/TSM/NVO/BABA again — I have the raw bytes from yesterday. Run the pipeline against those bytes and tell me what changed."*

Replay closes that loop: take a bundle, swap the live SEC/Yahoo/FRED gateways for "read-from-bundle" implementations, run the same code path, diff the produced response against `17-response.json`. Output: a per-bundle pass/fail plus a structured diff showing which fields moved.

The personal-investment use case is regression-style: regress a code change against a watchlist of bundles. So replay must be batch-capable from day one — single-bundle replay is the unit; the user's natural invocation is "replay everything in `artifacts/2026-05-01/`".

---

## 2. Goals and Non-Goals

### Goals

- **G1** Given a bundle directory, the replay binary re-runs the request through the current code and produces a structured diff against the saved `17-response.json`.
- **G2** Replay does not perform any external network call. SEC / Yahoo / FRED responses come from the bundle's `*.raw.json` files. Database, cache, and metrics are no-ops.
- **G3** Replay supports two entry depths: `--from=raw` (re-run gateway parsers from `*.raw.json`) and `--from=parsed` (skip parsers, inject `*.parsed.json` directly into the pipeline). Default `raw` because it exercises more code per replay.
- **G4** Replay supports batch mode: pointed at a directory of bundles, it walks them, replays each, and emits a summary row per bundle plus an aggregate pass/fail count. Exit code is 0 when all bundles match within tolerance, 1 when any mismatch, 2 on infrastructure failure (missing files, schema-version mismatch, etc.).
- **G5** Diffs are float-tolerant: per-field tolerances configurable via flag (default `--float-tol=1e-9` for relative, `1e-12` for absolute). Mismatches outside tolerance fail the replay; mismatches inside tolerance pass with a "drifted-within-tolerance" annotation in the verbose output.
- **G6** Replay refuses to run against bundles whose manifest schema versions don't match the current code's schema versions, unless `--allow-schema-drift` is passed (in which case mismatches degrade to warnings on stderr).
- **G7** Output is machine-parseable JSON when `--format=json`, human text otherwise. Both formats are stable contracts so CI / shell-pipelines can build on either.

### Non-Goals

- **NG1** Replaying writes to the database, cache, or any external system. Replay is read-only against bundles and has no side effects on Midas state.
- **NG2** Re-creating bundles. Replay reads existing bundles; producing them is Phases 1–2.C's job.
- **NG3** A web UI for browsing replay results. CLI + JSON output only.
- **NG4** Time-series aggregation across replays. Each invocation produces a one-shot summary; trending is the consumer's job.
- **NG5** Mutating the bundle on disk. Replay is strictly read-only on the input directory; output goes to stdout / stderr / a `--out` file.
- **NG6** Replaying non-fair-value endpoints. The bundle taxonomy in Phases 1–2.C is fair-value-specific (17 phases). Health, auth, performance handlers do not produce bundles and are out of scope.
- **NG7** Replaying pre-`bundle_version=1.0` bundles. Replay is forward-compatible from `1.0`; older formats don't exist.
- **NG8** Cross-version replay across breaking schema changes. When `schema_versions` in the manifest don't match the current code, the default behavior is to refuse with a clear error. `--allow-schema-drift` is an escape hatch, not a blessed workflow.

---

## 3. Verified Problems / Motivation

Findings from the project's actual workflow as of 2026-05-02.

| # | Scenario | Today | With replay |
|---|----------|-------|-------------|
| M1 | The open `docs/reviewer/G1-growth-blend-weights-coarse.md` follow-up proposes extending `growth.Result` with `AnalystWeight` / `HistoricalWeight` and routing them into the `growth.estimated` narrate phase. The user wants to see what mix the estimator chose for AAPL/MSFT/AMD before and after the change. | Manually re-run live valuations against the live API. Costs SEC / Yahoo / FRED quota; results vary because upstream data drifts. | Point replay at the saved bundles for those tickers from yesterday. Same SEC bytes in, see exactly which rate moved. Zero network. |
| M2 | A bug fix in the cleaner (e.g. a future fix in the `industry-classification-unification-spec`) needs regression checking against ~20 tickers. | Each ticker requires a full live request, ~3–4 sec each, with risk of upstream rate limits. | Replay walks `artifacts/2026-04-25/*/req_*/`, produces a 20-row pass/fail table in seconds. |
| M3 | A flaky 5xx in production left a deferred bundle on disk via Phase 2.A's `on_error` trigger. The user wants to know what input drove the failure. | The request_id and the `99-narrate.jsonl` are visible — but reproducing the failure requires running the same code with the same upstream bytes. | `replay <bundle-dir>` re-runs the exact upstream payloads through the current code; the failure either reproduces (still a bug) or doesn't (fixed). |
| M4 | A schema-version refactor (e.g. bumping `FinancialData` from v7 to v8) needs a "did this break anything" sweep against all bundles from the prior week. | No tooling exists. The user manually cherry-picks tickers. | `replay --allow-schema-drift artifacts/` runs all bundles; the diff stream surfaces every field that moved. |

The G1 follow-up is the immediate forcing function — the user has identified a specific small change they want to verify against historical bundles, and replay is the cheapest way.

---

## 4. Requirements

### Functional

- **F1** A new binary `cmd/replay/main.go` accepts a bundle directory path or a parent directory containing many bundles, and replays each by re-running the request through the current code with bundle-substituted gateways.
- **F2** Replay re-uses the same fx providers and the same `*valuation.Service` constructor as the production server, with three substitutions: gateways read from the bundle, repositories return `not-found` so the cache miss path is exercised, metrics service is a no-op recorder.
- **F3** Replay supports `--from=raw` (default) and `--from=parsed` flags. In `raw` mode, gateway adapters wrap a `BundleHTTPRoundTripper` that returns canned `*.raw.json` bytes for the bundle's recorded SEC / Yahoo / FRED requests. In `parsed` mode, gateways are replaced with stub implementations that `json.Unmarshal` from `*.parsed.json` files directly into the gateway's domain return type.
- **F4** Replay diffs the produced response against `17-response.json` field-by-field with float tolerance. Mismatches outside tolerance produce a non-zero exit code.
- **F5** Replay refuses to run when the manifest's `schema_versions` map disagrees with the current code's schema versions, unless `--allow-schema-drift` is set.
- **F6** Replay refuses to run when the manifest's `git_sha` is empty AND the current build's git SHA disagrees with the bundle's git SHA, unless `--allow-git-drift` is set. (Empty `git_sha` in the manifest is treated as "unknown" and skipped, not an error.)
- **F7** Determinism gaps must be pinned to manifest values: `request_id` is read from the manifest and re-injected into ctx; `time.Now()` for any "as of" stamp is replaced by `manifest.started_at`; UUID generation is short-circuited via a fx-provided clock and ID generator that the production code consults.
- **F8** Replay produces structured output: per-bundle row (path, status, fields_changed, fields_drifted_within_tolerance, replay_duration_ms) plus an aggregate summary (total, pass, fail, errored). JSON shape is stable across versions.
- **F9** Replay's exit code: `0` = all replays passed within tolerance, `1` = at least one mismatch outside tolerance, `2` = at least one infrastructure failure (missing files, malformed manifest, schema drift without flag).
- **F10** Replay never modifies the input bundle directory. All output is on stdout / stderr / the file passed via `--out`.
- **F11** Replay enforces a hermeticity invariant: the gateway substitution layer returns a structured `replay.ErrBundleMissingPayload` (carrying the missing file path) if asked for a payload that is not present in the bundle. The substituted gateway never makes an HTTP call regardless of bundle contents — substitution happens at fx-construction time, not runtime. Errors propagate via the existing `datafetcher.coordinator` error channel and surface as a per-bundle `errored` Result. **Bundle gateways MUST return errors, never panic** — `internal/services/datafetcher/coordinator.go:181-196` runs gateway calls inside `go func()` goroutines under `sync.WaitGroup`, so a panic in a child goroutine would not be recovered by the replay binary's main goroutine and would crash the binary. Auth- and Watchlist-stubs (D8), which sit outside the goroutine path, MAY still panic — they catch wiring drift, not bundle-content gaps.

### Non-Functional

- **NF1** No new external Go module is added to the build graph. Replay uses only the existing `pkg/finance/*`, `internal/*`, `go.uber.org/fx` (v1.24.0, supports `fx.Decorate`), `go.uber.org/zap`, `github.com/google/uuid`, and the standard library — plus `github.com/google/go-cmp`, currently a transitive dependency via `testify`, which the first non-test import in `internal/observability/replay/diff.go` promotes to a direct dependency in `go.mod`. The dependency itself is not new; only its scope changes.
- **NF2** A single bundle replay completes in ≤ 200 ms on the user's local machine for a typical AAPL bundle (no network, fully cached).
- **NF3** Batch replay across 100 bundles completes in ≤ 30 seconds. Parallelism is bounded by `runtime.NumCPU()` and is bypassable via `--workers=1` for deterministic stdout output.
- **NF4** `pkg/finance/*` is not modified. Replay's gateway substitution lives entirely in `internal/observability/replay/`.
- **NF5** Replay code lives primarily under `cmd/replay/` (entry point) and `internal/observability/replay/` (core logic). Two narrowly-scoped, additive production-source changes are required and are called out as separate Architecture Decisions:
  - **D1**: export pre-existing response-construction helpers in `internal/api/v1/handlers/fair_value.go` (renames only, no logic change).
  - **D10**: add a `valuation.Clock` interface in `internal/services/valuation/clock.go` and route the three existing `time.Now()` reads in `service.go` through it. Production behavior is byte-identical to today (the production clock impl wraps `time.Now()`); replay supplies a manifest-bound clock for determinism.

  Both changes are surgical and reviewable. `pkg/finance/*` is unchanged (D7 v1.1 invariant preserved). No code is added or modified under `internal/infra/`.
- **NF6** Coverage gates: `internal/observability/replay/` ≥ 90%; the new `cmd/replay/` flag-parsing layer ≥ 80%.

---

## 5. Architecture Decisions

Each decision lists the chosen option, the rejected alternative, and the rationale.

### D1 — Entry point: invoke `valuation.Service.CalculateValuation` directly (NOT the Gin handler)

**Chosen:** Replay constructs a `*valuation.Service` via fx with substituted gateways, then calls `CalculateValuation(ctx, ticker, options)` and compares the returned `*entities.ValuationResult` against `17-response.json`'s decoded shape (mapping the response struct's fields back from `FairValueResponse`).

**Rejected:** Instantiating Gin, replaying the HTTP request from `01-request.json` against an `httptest.Server`. Adds the middleware chain (auth, rate-limit, logging, trace, requestID, response writer) without value — replay's question is "did the engine's math change", not "did the HTTP plumbing change". Auth/rate-limit/trace middleware would also need their own substitution. The Gin path was the user's decision-point question; the engine path wins because it tests the part that actually moves.

**Consequence:** The HTTP-level fields in `17-response.json` (status, headers) are not re-validated. The body's domain-relevant fields (`dcf_value_per_share`, `wacc`, `growth_rate`, `industry`, etc.) are diffed by re-running the engine and rebuilding the response struct via the same response-construction helpers as production. **As of v0.2 of this spec, those helpers are NOT yet exported** — `grep '^func [A-Z]\w+' internal/api/v1/handlers/fair_value.go` shows `NewFairValueHandler` is the only top-level export. BACKEND must export the helpers in handlers (e.g. `BuildIndustryFromResult`) as a precondition of Phase R2. The change is rename-only, additive in API surface, and is the first of two narrowly-scoped production-source changes this spec introduces (the second is D10's `valuation.Clock`). Both are listed in §10 Modified Files. The exact list of helpers to export is fixed during the R2 spike; REVIEWER (§11) verifies no logic change accompanies the renames.

### D2 — Gateway substitution: replace via fx-supplied bundle gateways, controlled by a `replay.Module` fx option

**Chosen:** A new `internal/observability/replay/module.go` exports an `fx.Option` (`replay.Module(bundleDir, opts)`) that:

1. Reads the bundle's manifest.
2. Constructs `BundleSECGateway`, `BundleMarketGateway`, `BundleMacroGateway`, `BundleYFinanceGateway`, `BundleFinziveGateway` (one per gateway interface in `internal/core/ports/`), each backed by the bundle's `*.raw.json` (`raw` mode) or `*.parsed.json` (`parsed` mode) files.
3. Replaces the production gateway providers via `fx.Decorate` (which exists in fx ≥ 1.18).
4. Provides a no-op `ports.MetricsService`, a `not-found`-returning `ports.CacheRepository`, and a stub `ports.FinancialDataRepository` / `MarketDataRepository` / `MacroDataRepository` so the engine's cache-miss path is exercised exactly once (no double-fetch from a hot cache).
5. Provides a deterministic `Clock` and `IDGenerator` (see D7) seeded from the manifest.

**Rejected — A:** A new "swappable gateway" port abstraction that production code consults. Adds permanent surface to production for a debug-only feature; high cost, no production benefit.

**Rejected — B:** Build-tag (`//go:build replay`). Compile-time switch fragments the test matrix and forces replay-specific changes in production files. fx already supplies the seam.

**Rejected — C:** A standalone replay-binary that reimplements the pipeline. Defeats the entire point — replay must run *the same code* the server runs.

**Consequence:** The CoreModule provider list (`internal/di/container.go`) does not change. `replay.Module` overrides via `fx.Decorate` and `fx.Replace`. The production `cmd/server/main.go` does not import `replay`. Replay's `fx.App` composes `CoreModule` + `ServiceModule` (NOT `HandlerModule`, NOT the lifecycle hooks that touch disk).

### D3 — Raw vs parsed entry: both, behind `--from=raw|parsed`, default `raw`

**Chosen:** `--from=raw` is the default. Gateway adapters in raw mode wrap an HTTP `RoundTripper` that returns the bundle's recorded `*.raw.json` bytes for SEC / FRED, and a custom Yahoo cookie+crumb shim that returns the bundle's market raw bytes when the gateway makes its scoped HTTP call. The gateway's parser (`sec/parser.go`, `market/yfinance.go`, `macro/gateway.go`) runs against those bytes — exactly as in production.

`--from=parsed` skips the gateway and `json.Unmarshal`s the bundle's `*.parsed.json` directly into the gateway's return type, then a stub gateway returns it. Used to isolate "the parser is not the change" from "the math is the change".

**Rejected:** Only one mode. The user's two natural questions are different — "did my math change" wants `parsed`, "did my parser+math change" wants `raw`. Supporting both is a single flag and a small extra implementation cost.

**Consequence:** `BundleSECGateway` / `BundleMarketGateway` / `BundleMacroGateway` each have two backends behind a `Mode` enum (`ModeRaw` / `ModeParsed`). Cleaner-stage and downstream substitution is `parsed`-only — the cleaner's input is always `10-clean-input.json`, exercising the cleaner code; substituting the cleaner output (`10-clean-output.json`) is **not** a mode replay supports because that would skip the very layer the user most often wants to verify.

A third mode `--from=cleaned` (start replay after the cleaner, using `10-clean-output.json` as the engine input) was considered and rejected for v0.1: skipping the cleaner skips the layer the user most often wants to verify, and `--from=parsed --diff-stages` already lets the user inspect cleaner-output drift as a diagnostic. If a future use case demands it (e.g., regressing a downstream-only change), a `cleaned` mode is a one-flag addition.

### D4 — Diff strategy: structural deep-diff with per-field float tolerance, default tolerances tunable via flag

**Chosen:** Replay uses `github.com/google/go-cmp/cmp` with a custom `cmpopts.EquateApprox(relTol, absTol)` for `float64` and `[]float64` fields. Default `--float-rel-tol=1e-9` and `--float-abs-tol=1e-12`. Diffs cover the entire `FairValueResponse` shape AND the underlying `entities.ValuationResult` (so internal fields like `IndustrySIC`, `IndustryHeuristicCode`, `SanityCheck.ImpliedPE` are checked too — the response's `omitempty` would otherwise hide drift in fields a production response chose not to emit).

**Why two tolerances, and why these defaults.** The relative and absolute tolerances cover orthogonal cases. `go-cmp`'s `EquateApprox` treats values as equal when `|a-b| <= max(absTol, relTol * max(|a|, |b|))`. So:

- **Relative `1e-9`** is the binding constraint for non-zero values. On a per-share fair value of $156.42 it permits drift of ~$0.00000016 — six orders of magnitude tighter than a cent. It is intentionally loose enough to absorb the ULP-level (~`1e-15` relative) drift that compiler-version changes can produce in `math.Pow`-heavy DCF math, while staying tight enough to surface any genuine math change.
- **Absolute `1e-12`** is the binding constraint for near-zero values, where relative tolerance is meaningless. Several legitimately-zero fields exist in production (e.g., `crp = 0.0` for non-ADR tickers, `MinorityInterest` for companies that don't have any). Without a small absolute floor, a value that should remain `0` could silently drift to `1e-10` and pass the relative check — exactly the kind of regression replay must catch.

Defaults are deliberately tight because the project's quality target is fintech-grade and a false-positive drift signal is preferable to a false-negative. Both are tunable via flag if a build-tooling change introduces drift the user wants to ignore.

Replay does NOT diff intermediate-stage outputs (clean-output, growth-curve, WACC, etc.) by default — the response-level diff is the source of truth, intermediate stages are derived. `--diff-stages` adds per-stage diffs (`13-wacc.json`, `12-growth-curve.json`, `15-valuation.json`) for users who want to see *where* the drift entered the pipeline.

**Rejected — A:** Exact equality. False-fails on legitimate float non-determinism (any `math.Pow`-heavy DCF can drift in the 16th significant digit due to compiler optimization changes). Useless as a regression signal.

**Rejected — B:** Final per-share only. Loses the `industry`, `wacc`, `growth_source`, `sanity_check` signals — exactly the fields the user inspects when something looks off.

**Consequence:** `github.com/google/go-cmp` is currently a transitive dependency of the project (pulled by `testify`). `internal/observability/replay/diff.go` is the first non-test file to import it, which promotes it to a direct dependency in `go.mod` after `go mod tidy`. No new external module enters the build graph; only the dependency scope changes. `cmp` is widely deployed as a runtime helper outside test code (it is not test-tagged) and brings no transitive runtime weight.

### D5 — Schema-drift policy: refuse by default, `--allow-schema-drift` to warn-and-proceed

**Chosen:** At replay start, `replay` reads the manifest's `schema_versions` map and compares each entry to the current code's stamped schema versions (a new compile-time map exposed at `internal/observability/replay/schema.go::CurrentSchemaVersions()`, hand-maintained and asserted in CI to match what production stamps into manifests).

If any entry mismatches:

- Without `--allow-schema-drift`: print the mismatched table to stderr and exit 2.
- With `--allow-schema-drift`: print the mismatched table to stderr as a Warn line, continue. The replay output annotates the bundle row with `schema_drift=true`.

`git_sha` mismatch is handled the same way under `--allow-git-drift` (see F6). The two flags are independent.

**Rejected:** Migration. We do not write schema migrators between bundle versions. Schema drift means "you're replaying against code the bundle wasn't built against; treat the result with suspicion." That's information, not a transformation.

**Consequence:** The `CurrentSchemaVersions` map must be hand-updated whenever a domain entity's on-disk shape changes. A test in `internal/observability/replay/schema_test.go` round-trips a synthetic bundle through `OpenBundle` and asserts the manifest's schema_versions is a subset of `CurrentSchemaVersions`. This forces the map to stay in sync.

### D6 — Output: dual JSON + text, controlled by `--format=json|text`, default `text`

**Chosen:** Two stable output formats. `text` is the default for human use:

```
artifacts/2026-04-25/AAPL/req_01HW8...    PASS   fields=0/47   stages=ok    duration=87ms
artifacts/2026-04-25/AMD/req_01HW9...     FAIL   fields=2/47   stages=wacc  duration=92ms
  - dcf_value_per_share: 156.42 -> 156.81 (drift +0.25%, outside relTol=1e-9)
  - wacc:                0.092  -> 0.094  (drift +2.17%, outside relTol=1e-9)
SUMMARY: 1/2 passed, 1 failed, 0 errored
```

`json` is one stable JSON object on stdout for CI / scripts:

```json
{
  "summary": {"total": 2, "passed": 1, "failed": 1, "errored": 0, "duration_ms": 184},
  "results": [
    {"bundle": "artifacts/...", "status": "pass", "fields_total": 47, "fields_changed": 0, "duration_ms": 87, "schema_drift": false},
    {"bundle": "artifacts/...", "status": "fail", "fields_total": 47, "fields_changed": 2, "duration_ms": 92,
     "diffs": [
       {"path": "dcf_value_per_share", "old": 156.42, "new": 156.81, "rel_drift": 0.0025},
       {"path": "wacc", "old": 0.092, "new": 0.094, "rel_drift": 0.0217}
     ]}
  ]
}
```

Exit codes match F9: `0` clean pass, `1` mismatch outside tolerance, `2` infrastructure failure.

**Rejected:** YAML, table-with-borders, TUI. None add information; all add code.

**Consequence:** The JSON shape becomes a stable contract from v0.1 of replay. Any addition is additive (new top-level field or per-result field). Removals or renames bump replay to v0.2 explicitly.

### D7 — Determinism: pin everything the engine reads from the world to manifest values

**Chosen:** Replay overrides three sources of non-determinism:

1. **`time.Now()`** — handled by D10 (Clock injection). Verification of `internal/services/valuation/service.go` shows three production reads at lines ~161, 245, 1057, 1254. The read at line 245 (`periodKey = fmt.Sprintf("%dFY", time.Now().Year())`) is *math-affecting*: a bundle captured in 2026 and replayed in 2027 would select a different financial-data period and produce a different valuation, which would falsely look like a code regression. The reads at 1057 and 1254 only stamp `CalculatedAt` on the response (timestamp-only drift, harmless to math). The read at 161 is for log timing (drift OK). All four are routed through the new `valuation.Clock` interface in D10 — production keeps wall-clock semantics; replay binds the clock to `manifest.started_at`.
2. **`uuid.NewString()` / request_id** — read from the manifest's `request_id` and injected into ctx via `logctx.Inject`. Replay's middleware stub never generates a new ID.
3. **Random sources** — there are none in `pkg/finance/*` or the engine today. If one is added, replay panics on first call (a synthetic `crypto/rand`-replacement that aborts) so the determinism gap is loud. (This is a panic at the source-of-randomness layer, NOT in a gateway, so the F11 goroutine-safety rule does not apply.)

Replay's narrate emitter and bundle writer are turned off entirely (replay does not produce bundles of bundles).

**Rejected:** Refactor production to take a `Clock` interface *everywhere* (including `pkg/finance/*`). Disproportionate cost for a debug-only feature, and the v1.1 D7 invariant ("`pkg/finance/*` stays logger-free and `context.Context`-free") would be violated if a Clock were plumbed through pure-math libraries. The actual fix in D10 is bounded to `internal/services/valuation/` only.

**Consequence:** Replay produces a deterministic response per (bundle, code-revision) pair as long as the engine itself is deterministic. The wall-clock surface is now fully pinned via D10's Clock interface. The earlier draft of this spec (v0.1) treated the year-rollover leak as accepted drift — that was wrong; cross-year replay would silently fail the user's primary regression-test use case.

### D8 — Side-effect isolation: a `replay.NoSideEffectsModule` overrides every fx provider that touches state

**Chosen:** `replay.Module` includes a sub-module `NoSideEffectsModule` that overrides:

- `ports.FinancialDataRepository` → stub returning `cache miss / not found`
- `ports.MarketDataRepository` → same
- `ports.MacroDataRepository` → same
- `ports.CacheRepository` → no-op (Set/Get always miss)
- `ports.MetricsService` → recording stub (counts, doesn't ship)
- `ports.AuthRepository` → never called by the engine path; provided as a sentinel that panics on any call (catches accidental wiring drift)
- `ports.WatchlistRepository` → same panic-stub
- `*sql.DB` / Redis client → not constructed; their providers are removed from replay's `fx.App`

Replay never opens the SQLite database file. The `cmd/replay/main.go` invocation does not require `--db` or any state argument — it operates on the bundle directory only.

**Rejected:** Reusing the production fx app and just "trusting nothing writes." Cache and metrics writes happen on the request path today; trusting is fragile. Hard substitution makes the no-write invariant a property of the binary, not a code-review convention.

**Consequence:** Replay's `fx.App` is meaningfully smaller than production's. The smaller graph also makes replay's startup time near-instant (~10 ms for the fx wire-up).

### D9 — Batch mode is the default; single-bundle is a degenerate batch

**Chosen:** `replay <path>` walks the path. If `<path>` is a directory containing `00-manifest.json`, that's a single bundle (a degenerate one-element batch). If `<path>` is a directory containing date-prefixed subdirectories or ticker-prefixed subdirectories, replay walks recursively for any sub-tree containing `00-manifest.json`. Batch results are aggregated; the output format is identical for one bundle vs many (a one-row text table or a one-element JSON results array).

**Rejected:** A separate batch subcommand. Adds CLI surface for no benefit; the directory walk subsumes single-bundle.

**Consequence:** The walk function is hermetic — it never recurses into unrelated directories (only paths containing `00-manifest.json` are replayed). Symlinks are followed once (no cycles).

### D10 — `valuation.Clock` injection for replay determinism (load-bearing production change)

**Chosen:** Add a small interface in `internal/services/valuation/clock.go`:

```go
package valuation

import "time"

// Clock is a narrow seam used by *Service to read the wall clock.
// Production binds it to wallClock (time.Now). Replay binds it to
// a manifest-bound clock that returns the bundle's started_at.
type Clock interface {
    Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

// NewWallClock returns the production Clock binding. Wired by fx.
func NewWallClock() Clock { return wallClock{} }
```

`*valuation.Service` gains a `clock Clock` field, populated by fx, and the three existing reads in `service.go` become `s.clock.Now()` calls. Production behavior is byte-identical: `wallClock.Now() == time.Now()`. Replay binds `Clock` to a struct whose `Now()` returns the parsed `manifest.started_at`.

**Why this is its own ADR (and not buried inside D7):** D10 is the only production-source change in the engine path that this spec introduces. It's small (~30 LoC + tests) but architecturally load-bearing — without it, replay's primary use case (cross-year regression) silently fails. The change is reviewable in isolation and could be merged as Phase R0 ahead of replay itself if BACKEND prefers.

**Rejected — A:** Pass valuation date/time as an explicit argument through `CalculateValuation(ctx, ticker, opts, asOf)`. Cleaner in theory (explicit dependencies > implicit), but ripples through every caller including the Gin handler, contract tests, and benchmarks. The Clock seam is local to the service.

**Rejected — B:** Accept the year-rollover drift and add `--allow-time-drift` analogous to `--allow-schema-drift`. Defensible for a one-off debug, but for a fintech-grade tool the user uses to gate code changes against a watchlist, "your replay was correct in November and silently lied in January" is an unacceptable failure mode.

**Rejected — C:** A `time.Now`-replacing build tag (`//go:build replay`). Fragments the test matrix and forces production code awareness of replay.

**Consequence:** `internal/services/valuation/service.go`, `internal/services/valuation/clock.go` (new), and `internal/di/container.go` (register `NewWallClock` provider) are modified. `pkg/finance/*` is untouched (v1.1 D7 invariant preserved). The Clock provider is co-located with the existing valuation-service registrations in fx; it does not require a new fx module.

---

## 6. Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│ cmd/replay/main.go                                                       │
│  - Parses flags                                                          │
│  - Walks input path for bundles                                          │
│  - Per bundle: invokes internal/observability/replay.Replay()            │
│  - Aggregates results, writes summary to stdout, sets exit code          │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│ internal/observability/replay/                                           │
│  ├── replay.go        Replay(ctx, bundleDir, opts) (Result, error)       │
│  │                    1. Read manifest                                   │
│  │                    2. Validate schema_versions / git_sha              │
│  │                    3. fx.New(replay.Module(bundleDir, opts))          │
│  │                    4. Resolve *valuation.Service                      │
│  │                    5. CalculateValuation(ctx, manifest.ticker, opts)  │
│  │                    6. Diff result vs 17-response.json                 │
│  │                                                                      │
│  ├── module.go        replay.Module(bundleDir, opts) fx.Option           │
│  │                    fx.Decorate over CoreModule + ServiceModule        │
│  │                                                                      │
│  ├── gateway_sec.go   BundleSECGateway implements ports.SECGateway       │
│  ├── gateway_market.go (MarketDataGateway, YFinanceGateway, Finzive)     │
│  ├── gateway_macro.go (MacroDataGateway)                                 │
│  ├── stubs.go         No-op repos / metrics / panic-stubs                │
│  ├── manifest.go      Read & validate 00-manifest.json                   │
│  ├── schema.go        CurrentSchemaVersions map; drift detection         │
│  ├── diff.go          Field-level diff with float tolerance              │
│  ├── walk.go          Directory walk for batch mode                      │
│  ├── output.go        Text + JSON renderers                              │
│  └── *_test.go        Unit tests for each file                           │
│                                                                          │
│  + integration tests in internal/observability/replay/integration_test.go│
│    that round-trip: produce bundle → replay it → assert no diff          │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│ Existing pipeline (UNCHANGED)                                            │
│  internal/services/valuation/service.go                                  │
│  internal/services/datafetcher/coordinator.go                            │
│  internal/services/datacleaner/service.go                                │
│  internal/services/valuation/models/router.go                            │
│  pkg/finance/*                                                           │
│                                                                          │
│  Reads gateways via ports.*; replay's BundleGateway implementations       │
│  satisfy those interfaces. No production code change.                    │
└─────────────────────────────────────────────────────────────────────────┘
```

### Folder structure

```
cmd/
  replay/
    main.go                       # NEW — flag parsing, walk, aggregate, exit code
    main_test.go                  # NEW — flag parsing only (logic tested in package)
internal/
  observability/
    replay/                       # NEW package
      replay.go                   # Replay() entry point
      replay_test.go
      module.go                   # fx.Module composition
      module_test.go
      gateway_sec.go              # BundleSECGateway
      gateway_sec_test.go
      gateway_market.go           # BundleMarketGateway + BundleYFinanceGateway + BundleFinzive
      gateway_market_test.go
      gateway_macro.go            # BundleMacroGateway
      gateway_macro_test.go
      stubs.go                    # No-op repos, metrics, panic-stubs
      stubs_test.go
      manifest.go                 # Manifest read + validate
      manifest_test.go
      schema.go                   # CurrentSchemaVersions, drift detection
      schema_test.go
      diff.go                     # Field-level diff with float tolerance
      diff_test.go
      walk.go                     # Bundle directory walk
      walk_test.go
      duration.go                 # ParseDurationExtended (Go std + d for days)
      duration_test.go
      output.go                   # Text + JSON renderers
      output_test.go
      errors.go                   # ErrBundleMissingPayload sentinel
      errors_test.go
      integration_test.go         # Round-trip: produce bundle -> replay
      testdata/                   # Synthetic bundles for tests
        happy/...
        schema-drift/...
        missing-files/...
internal/services/valuation/
  clock.go                        # NEW (D10) — Clock interface + wallClock impl
  clock_test.go                   # NEW
```

### Critical abstractions

- **`replay.Mode`** — `ModeRaw` (default) or `ModeParsed`. Drives gateway-stub backends.
- **`replay.Options`** — struct passed to `Replay()`. Mirrors the CLI flags (`Mode`, `FloatRelTol`, `FloatAbsTol`, `AllowSchemaDrift`, `AllowGitDrift`, `DiffStages`).
- **`replay.Result`** — per-bundle outcome (Status, FieldsTotal, FieldsChanged, Diffs, Duration, SchemaDrift, GitDrift).
- **`replay.BundleGateway`** — internal interface every bundle gateway implements: `Load(bundleDir, mode) error`. Lets the test framework swap one for a mock when testing the dispatch layer.
- **`replay.ErrBundleMissingPayload`** — sentinel error type carrying `BundlePath string` and `RelativePath string` fields. Returned by every bundle-gateway implementation when asked for a payload not present on disk. Required by F11 (gateways must NOT panic — `datafetcher.coordinator` runs gateways in goroutines, and a child-goroutine panic would crash the binary).
- **`replay.ParseDurationExtended(s string) (time.Duration, error)`** — extends Go's `time.ParseDuration` (which only supports `ns`/`us`/`µs`/`ms`/`s`/`m`/`h`) with a `d` (days, treated as 24h) suffix. Examples: `7d` = 168h, `30d` = 720h. Used by `--filter-since`. Weeks/months/years are intentionally unsupported (ambiguous semantics, not needed for the watchlist-regression workflow). Lives at `internal/observability/replay/duration.go`.
- **`valuation.Clock`** (per D10) — production binding `valuation.NewWallClock()` is registered in `internal/di/container.go` as part of the existing valuation provider group. Replay's `NoSideEffectsModule` overrides the binding via `fx.Decorate` to a manifest-bound clock returning `manifest.started_at`.

---

## 7. CLI Contract

### Synopsis

```
replay [flags] <path>

  <path>   A bundle directory (containing 00-manifest.json) OR a parent
           directory containing one or more bundles (recursively walked).
```

### Flags

| Flag | Type | Default | Meaning |
|---|---|---|---|
| `--from` | `raw` \| `parsed` | `raw` | Gateway substitution mode (D3) |
| `--format` | `text` \| `json` | `text` | Output format (D6) |
| `--out` | path | `-` (stdout) | Where to write the report (text or JSON) |
| `--float-rel-tol` | float | `1e-9` | Relative tolerance for float field diffs (D4) |
| `--float-abs-tol` | float | `1e-12` | Absolute tolerance for float field diffs |
| `--diff-stages` | bool | `false` | Also diff intermediate-stage JSON files (D4) |
| `--allow-schema-drift` | bool | `false` | Warn instead of refusing on `schema_versions` mismatch (D5) |
| `--allow-git-drift` | bool | `false` | Warn instead of refusing on `git_sha` mismatch (F6) |
| `--workers` | int | `runtime.NumCPU()` | Parallel replay workers; `1` for deterministic stdout order |
| `--filter-ticker` | string | `""` | Only replay bundles whose ticker matches (e.g. `AAPL`) |
| `--filter-since` | duration | `""` | Only replay bundles whose `started_at` is newer than `now - filter_since` (e.g. `7d`, `48h`). Parsed by `replay.ParseDurationExtended` (Go's standard units plus `d` for days; see §6). `time.ParseDuration` alone does not accept `d` — that's why an extended parser exists. |
| `--verbose` | bool | `false` | Per-field diff output in text mode (no-op for JSON, which is always full) |
| `--quiet` | bool | `false` | Suppress per-bundle rows; only print the aggregate summary |

`--quiet` and `--verbose` are mutually exclusive; passing both is a usage error (exit 2).

### Exit codes

| Code | Meaning |
|---|---|
| `0` | All replays passed within tolerance |
| `1` | At least one replay failed: a field moved outside tolerance |
| `2` | Infrastructure failure: missing files (`ErrBundleMissingPayload`), malformed manifest, schema drift without `--allow-schema-drift`, git drift without `--allow-git-drift`, or a panic in an Auth/Watchlist stub (which would indicate a wiring bug — those stubs sit outside the goroutine path so panics there ARE recovered by `cmd/replay/main.go`) |

### Sample output (text)

```
$ replay artifacts/2026-04-25/AAPL/req_01HW8ZQXKR/

artifacts/2026-04-25/AAPL/req_01HW8ZQXKR/   PASS   fields=0/47   duration=87ms

SUMMARY: 1/1 passed, 0 failed, 0 errored, total duration=87ms
```

```
$ replay --diff-stages --verbose artifacts/2026-04-25/AMD/req_01HW9ABCDE/

artifacts/2026-04-25/AMD/req_01HW9ABCDE/   FAIL   fields=2/47   duration=92ms
  Stage diffs:
    13-wacc.json:
      - cost_of_equity: 0.118 -> 0.121 (rel_drift +2.54%)
    15-valuation.json:
      - dcf_value_per_share: 156.42 -> 156.81 (rel_drift +0.25%)
  Response diffs:
    - dcf_value_per_share: 156.42 -> 156.81 (rel_drift +0.25%)
    - wacc: 0.092 -> 0.094 (rel_drift +2.17%)

SUMMARY: 0/1 passed, 1 failed, 0 errored, total duration=92ms
```

### Sample output (JSON)

```json
{
  "replay_version": "0.1",
  "git_sha_current": "a3f8c1e",
  "summary": {
    "total": 2,
    "passed": 1,
    "failed": 1,
    "errored": 0,
    "duration_ms": 184
  },
  "results": [
    {
      "bundle": "artifacts/2026-04-25/AAPL/req_01HW8ZQXKR",
      "status": "pass",
      "ticker": "AAPL",
      "fields_total": 47,
      "fields_changed": 0,
      "schema_drift": false,
      "git_drift": false,
      "duration_ms": 87,
      "diffs": []
    },
    {
      "bundle": "artifacts/2026-04-25/AMD/req_01HW9ABCDE",
      "status": "fail",
      "ticker": "AMD",
      "fields_total": 47,
      "fields_changed": 2,
      "schema_drift": false,
      "git_drift": false,
      "duration_ms": 92,
      "diffs": [
        {"path": "dcf_value_per_share", "old": 156.42, "new": 156.81, "rel_drift": 0.0025},
        {"path": "wacc", "old": 0.092, "new": 0.094, "rel_drift": 0.0217}
      ]
    }
  ]
}
```

### Sample output (infrastructure failure → exit 2)

```
$ replay artifacts/2026-04-25/TSM/req_01HWABCDEFG/

ERROR: schema drift detected (use --allow-schema-drift to proceed)
  bundle: artifacts/2026-04-25/TSM/req_01HWABCDEFG/
  manifest schema_versions:
    FinancialData: 7      -> current code: 8       MISMATCH
    SECCompanyFacts: 3    -> current code: 3
    ValuationResult: 2    -> current code: 2

SUMMARY: 0/1 passed, 0 failed, 1 errored, total duration=4ms
```

---

## 8. Configuration Surface

Replay does **not** add any new keys to `config/config.yaml`. It operates entirely from CLI flags and bundle contents.

The replay binary reads the bundle's manifest to recover the production-config-relevant fields (the ones that *would* have affected the response): `git_sha`, `build_version`, `schema_versions`. Replay does NOT replay the production server's `config.yaml` against the bundle — it uses the *current* code's defaults. This is intentional: replay's purpose is "did the *code* change behavior", not "did the *config* change behavior". The latter is a separate question and out of scope.

The single `cmd/replay/main.go` flag-parsing layer is the entire user-facing surface (§7).

Optional environment variables (mirror existing project pattern):

| Env var | Effect |
|---|---|
| `REPLAY_OUT` | Equivalent to `--out`, lower precedence than CLI flag |
| `REPLAY_WORKERS` | Equivalent to `--workers` |

No `MIDAS_*` env vars are read — replay does not consult production config.

---

## 9. Phase Plan

Three independently-mergeable phases on a `feat/replay-tooling` branch. Each phase is TDD-disciplined: tests first, then implementation, then integration.

### Phase R1 — Skeleton + manifest validation + walk (no replay yet)

**Goal:** Produce a binary that walks bundles, validates manifests, prints a summary — but does not run the engine.

**Includes:**

- `cmd/replay/main.go` flag parsing
- `internal/observability/replay/manifest.go` — read & validate `00-manifest.json`
- `internal/observability/replay/schema.go` — `CurrentSchemaVersions` map seeded with current entity versions; drift detection
- `internal/observability/replay/walk.go` — directory walk that yields bundle directories
- `internal/observability/replay/output.go` — text + JSON renderers (with stub `Result` data)
- `internal/observability/replay/diff.go` — float-tolerant cmp helpers (no integration with engine yet)
- Tests for all of the above + a `testdata/` tree of synthetic bundles

**Done when:** `replay artifacts/` walks the tree, prints a per-bundle row "skeleton OK", exits 0. `replay --allow-schema-drift=false` against a synthetic schema-drift bundle exits 2 with the expected stderr message.

**Estimated LoC:** ~800 (incl. tests).

**Independently revertable:** Yes — adds a binary nobody depends on yet.

### Phase R2 — Gateway substitution + engine wiring (single-bundle replay works)

**Goal:** A single-bundle replay produces a real `*entities.ValuationResult` from the current code path, diffed against `17-response.json`.

**Includes (prerequisites — do these first):**

- **D1 helper exports** (Finding 1): rename pre-existing response-construction helpers in `internal/api/v1/handlers/fair_value.go` from lowercase to capitalized so replay can import them. Pure rename diff. Lands as the first commit on the R2 branch.
- **D10 `valuation.Clock` injection** (Finding 3): add `internal/services/valuation/clock.go` (Clock interface + `wallClock`), thread `clock Clock` into `*Service`, replace the three `time.Now()` reads in `service.go`, register `valuation.NewWallClock` provider in `internal/di/container.go`. Production wall-clock semantics unchanged. Could optionally land as a separate "R0" commit ahead of replay; the spec leaves the partitioning to BACKEND.
- **`fx.Decorate` spike**: smoke-test that `replay.Module` can override `CoreModule`'s gateway providers via `fx.Decorate` at the pinned `go.uber.org/fx v1.24.0`. If it doesn't compose, fall back to the contingent `container.go` gateway-provider sub-module split (§10).

**Includes (replay implementation):**

- `internal/observability/replay/gateway_sec.go`, `gateway_market.go`, `gateway_macro.go` — BundleGateway implementations for both `Mode=Raw` (HTTP RoundTripper) and `Mode=Parsed` (direct unmarshal). Each returns `ErrBundleMissingPayload` (NOT panic) on missing files.
- `internal/observability/replay/errors.go` — `ErrBundleMissingPayload` sentinel definition.
- `internal/observability/replay/stubs.go` — no-op repos / metrics; Auth/Watchlist panic-stubs (they sit outside the datafetcher goroutine path).
- `internal/observability/replay/module.go` — `replay.Module(bundleDir, opts)` fx.Option using `fx.Decorate` over the existing `CoreModule` + `ServiceModule`. Binds `valuation.Clock` to a manifest-clock.
- `internal/observability/replay/replay.go` — `Replay(ctx, bundleDir, opts) (Result, error)` orchestration.
- Tests including the round-trip integration test (produce a real bundle in-memory, replay it, assert no diff).

**Done when:**
- `replay artifacts/2026-04-25/AAPL/req_<id>/` against a freshly-produced bundle returns PASS (zero field diffs).
- `replay --from=parsed` against the same bundle also PASSes.
- The hermeticity invariant (F11) is exercised by a test that produces a bundle missing a required file and verifies the binary exits with code 2 and an `errored` Result via the `ErrBundleMissingPayload` path — explicitly running through the real `datafetcher.coordinator` goroutines, not synchronous mocks (asserts process exit code, not just a function return).
- Cross-year regression test (D10): replay a bundle with `manifest.started_at` in 2026 against a binary running in calendar year 2027; output is byte-identical to in-year replay. Confirms `service.go:245` periodKey is fully pinned via `valuation.Clock`.

**Estimated LoC:** ~1200 (incl. tests).

**Independently revertable:** Yes — Phase R1 still ships a usable walk-and-validate binary.

### Phase R3 — Batch mode polish + stage diffs + filters

**Goal:** Production-ready CLI: parallelism, filters, `--diff-stages`, `--verbose`, JSON contract finalised.

**Includes:**

- Parallel batch execution (`--workers`)
- `--filter-ticker` and `--filter-since` flags
- `--diff-stages` mode (diffs `13-wacc.json` / `12-growth-curve.json` / `15-valuation.json` / `10-clean-output.json` / etc.)
- `--verbose` per-field diffs in text output
- Final JSON shape lock-in + golden test
- Bench target ensuring NF2 (≤ 200 ms / bundle) and NF3 (≤ 30 s / 100 bundles)

**Done when:**
- `replay artifacts/` against a 100-bundle tree produces a deterministic JSON output (`--workers=1`) and finishes within NF3.
- `replay --filter-ticker AAPL artifacts/2026-04-25/` only replays AAPL bundles.
- A "drifted-within-tolerance" test produces a bundle, mutates a float field by `5e-10` (under default `1e-9` rel-tol), replays, asserts PASS with a "drifted-within-tolerance" annotation visible only with `--verbose`.

**Estimated LoC:** ~600 (incl. tests).

**Independently revertable:** Yes — Phase R2 still ships a single-bundle replayer.

---

## 10. Files Touched

### New files

```
cmd/replay/main.go
cmd/replay/main_test.go

internal/observability/replay/replay.go
internal/observability/replay/replay_test.go
internal/observability/replay/module.go
internal/observability/replay/module_test.go
internal/observability/replay/gateway_sec.go
internal/observability/replay/gateway_sec_test.go
internal/observability/replay/gateway_market.go
internal/observability/replay/gateway_market_test.go
internal/observability/replay/gateway_macro.go
internal/observability/replay/gateway_macro_test.go
internal/observability/replay/stubs.go
internal/observability/replay/stubs_test.go
internal/observability/replay/manifest.go
internal/observability/replay/manifest_test.go
internal/observability/replay/schema.go
internal/observability/replay/schema_test.go
internal/observability/replay/diff.go
internal/observability/replay/diff_test.go
internal/observability/replay/walk.go
internal/observability/replay/walk_test.go
internal/observability/replay/duration.go               # NEW (Finding 4b)
internal/observability/replay/duration_test.go
internal/observability/replay/errors.go                 # NEW (ErrBundleMissingPayload)
internal/observability/replay/errors_test.go
internal/observability/replay/output.go
internal/observability/replay/output_test.go
internal/observability/replay/integration_test.go
internal/observability/replay/testdata/<synthetic bundles>

internal/services/valuation/clock.go                    # NEW (D10) — Clock interface + wallClock
internal/services/valuation/clock_test.go
```

### Modified files

| File | Change |
|---|---|
| `go.mod` | `github.com/google/go-cmp` promoted from transitive (via `testify`) to a direct dependency after `go mod tidy`. No new external module enters the build graph. |
| `internal/observability/artifact/manifest.go` | None at code level; the `Manifest` struct is consumed read-only by replay. |
| `internal/api/v1/handlers/fair_value.go` | **NEW (D1, Finding 1)** — Export pre-existing response-construction helpers (e.g. `BuildIndustryFromResult`). Renames only; no logic change, no behavior change, no signature changes beyond capitalization. Exact list of functions to export is fixed during the R2 spike. REVIEWER (§11) verifies no logic change accompanies the renames. |
| `internal/services/valuation/service.go` | **NEW (D10, Finding 3)** — Add `clock Clock` field to the `*Service` struct (additive). Replace the three production `time.Now()` reads (lines ~161, 245, 1057, 1254) with `s.clock.Now()`. Production behavior is byte-identical because the wall-clock binding's `Now()` wraps `time.Now()`. |
| `internal/di/container.go` | **NEW (D10)** — Register `valuation.NewWallClock()` as a provider in the existing valuation provider group. Strictly additive. (This also resolves the §10-v0.1 fallback re: gateway-provider sub-module split: it is no longer a fallback — D10 already requires touching `container.go`. The gateway-provider sub-module split remains an R2-spike contingency only if `fx.Decorate` proves insufficient.) |

The replay path consumes existing fx providers and existing port interfaces. Two narrowly-scoped, additive production-source changes (the handlers exports in D1 and the Clock injection in D10) are the only modifications required outside the new replay package and the new binary. `pkg/finance/*` is byte-for-byte unchanged (v1.1 D7 invariant preserved); CI lint check enforces this (§12).

### Contingent — only if `fx.Decorate` is insufficient at the version pinned in `go.mod`

| File | Change |
|---|---|
| `internal/di/container.go` | Split the gateway providers into a tiny sub-module so replay can `fx.Replace` them as a unit. Strictly mechanical — no behavior change. |

This is a fallback. The investigation in Phase R2 must verify `fx.Decorate` works for our setup at the pinned `go.uber.org/fx v1.24.0`; only if it does not is the container.go gateway-split done. The Clock-provider addition above is unconditional.

---

## 11. Tasks by Agent

### BACKEND

- Implement Phase R1 per the file list and acceptance in §9.
- Implement Phase R2 per the file list and acceptance in §9, including the round-trip integration test that produces a real bundle in-memory (using the existing `internal/observability/artifact` package) and replays it.
- Implement Phase R3 per the file list and acceptance in §9.
- Confirm no new entries in `go.mod` (only the test → main promotion of `go-cmp`).
- Confirm `pkg/finance/*` is not touched (lint check).
- Add `replay` to the Build & Run section of `CLAUDE.md` (proposed text in §16).

### FRONTEND

- N/A — replay is a CLI tool with no UI surface.

### UX_UI

- N/A.

### QA

- Validate the round-trip property: a bundle produced via `?trace=1` against the live server, when replayed against the same code revision, yields zero diffs. (Integration test in Phase R2 covers this; QA validates by running it on at least 5 production-like tickers from the user's watchlist.)
- Validate schema-drift behavior: synthesise a bundle with a stale `schema_versions` map; assert exit 2 without `--allow-schema-drift` and exit 0 / 1 with it.
- Validate hermeticity: produce a bundle, delete one of its `*.raw.json` files, replay; assert the result is `errored` (not `panicked`).
- Validate JSON output stability: round-trip the JSON through `jq` and a second `replay` invocation that just renders it; field set is identical.
- Validate `--workers=1` produces deterministic stdout ordering across runs.
- Validate exit codes match §7 across the three failure modes.

### REVIEWER

- **D1 / D2** — verify `cmd/replay/main.go` does not import `internal/api/server.go` or any HTTP plumbing. The replay path is `valuation.Service.CalculateValuation` only.
- **D1 (Finding 1)** — verify the helpers exported in `internal/api/v1/handlers/fair_value.go` are touched by **export-rename only**, with no logic change, no signature change beyond capitalization, and no new parameters added. `git log -p` on the rename commit must be a clean rename diff.
- **D8** — verify the `NoSideEffectsModule` substitutions cover every port the engine path can possibly consult; specifically, audit `internal/services/valuation/service.go` for any new dependency added since Phase 2.C and confirm its replay stub exists. Note: gateway-stubs return `ErrBundleMissingPayload` on missing payloads (NOT panic — see F11). Auth/Watchlist panic-stubs do still panic (different layer; not in goroutine path; an actual hit would be a wiring bug).
- **D7 / D10** — verify replay does not generate a new `request_id` (manifest's value must be authoritative) and does not emit narrate/calclog lines (the request-path observability emitters must be no-op'd in replay). Verify `valuation.Clock` is bound to a manifest-clock in replay's fx graph and that production code reads `s.clock.Now()` consistently in `service.go` (no surviving direct `time.Now()` calls on the engine path; `grep 'time\.Now\(\)' internal/services/valuation/service.go` returns zero hits in non-test files after R2).
- **F11** — verify bundle gateways return `ErrBundleMissingPayload` (NOT panic) when asked for an absent payload — confirmed by a unit test that calls each gateway with a known-empty bundle and asserts `errors.Is(err, replay.ErrBundleMissingPayload)`. **Critically, the integration test must run replay against a bundle missing one `*.raw.json` file and assert the binary exits with code 2 and an `errored` Result, NOT a Go panic.** Cause: `internal/services/datafetcher/coordinator.go:181-196` runs gateways in goroutines under `sync.WaitGroup`; a child-goroutine panic would not be recovered.
- **NF1** — verify `go.mod` has no new external module declarations beyond the `go-cmp` transitive→direct promotion.
- **Bundle reaper interaction:** confirm replay does NOT trigger the artifact-reaper (replay must not create or modify bundles, including not bumping their mtime).
- **D10 production parity:** verify `valuation.NewWallClock().Now()` returns `time.Now()` byte-identically in production wiring (table test under `clock_test.go`).

---

## 12. Testing Strategy

| Layer | What | How |
|---|---|---|
| Unit | `manifest.Read` parses a v1.0 manifest correctly, including the optional `notes` field | golden-file test against fixtures in `testdata/manifests/` |
| Unit | `manifest.Read` rejects malformed JSON, missing `bundle_version`, unknown bundle versions | table test |
| Unit | `schema.DriftReport` returns the correct mismatch tuple for each combination | table test against synthetic `CurrentSchemaVersions` |
| Unit | `walk.WalkBundles` yields exactly the bundle directories under the input path; ignores symlink cycles; handles empty subtrees | `t.TempDir()` + filesystem fixtures |
| Unit | `diff.Compare` with `EquateApprox` returns zero diffs for floats within tolerance, mismatches outside | property test using `quick.Check` against random float pairs |
| Unit | `BundleSECGateway.GetCompanyFacts` (mode=raw) returns bytes from `05-fetch-sec.raw.json` and parses them via the production parser | integration with a real synthetic bundle |
| Unit | `BundleSECGateway.GetCompanyFacts` (mode=parsed) skips the parser and unmarshals directly | same |
| Unit | `BundleSECGateway` returns `ErrBundleMissingPayload` (with `RelativePath="05-fetch-sec.raw.json"`) when the bundle is missing the file — does NOT panic (see F11 / Finding 2) | error-path test asserting `errors.Is(err, replay.ErrBundleMissingPayload)` |
| Unit | `valuation.Clock` production binding (`wallClock.Now()`) returns a `time.Time` within 100 ns of an immediately-following `time.Now()` call — byte-identical semantics | `clock_test.go` |
| Unit | `replay.ParseDurationExtended` accepts `7d` (= 168h), `30d`, `48h`, `5m`; rejects `1w`, `1mo`, `1y`, `7days`, `7 d`, empty | table test |
| Unit | `stubs.NotFoundCacheRepo.Get` always returns `cache miss`; never produces side effects | trivial test |
| Unit | `stubs.PanicAuthRepo.GetByKey` panics, catching accidental wiring of the auth path into replay | recover()-asserting test |
| Unit | `output.RenderText` and `output.RenderJSON` produce stable byte output for known `Result` slices | golden-file tests |
| Unit | `output.RenderJSON` escape characters in field paths correctly | property test on random bytes |
| Integration | Round-trip: produce a bundle in-memory via `artifact.OpenBundle` + a synthetic engine run + close; then `Replay` it; assert zero diffs | `internal/observability/replay/integration_test.go` |
| Integration | A bundle with a deliberately mutated `17-response.json` (one float field shifted by 1%) replays as FAIL with exactly one diff | same file, second test |
| Integration | A bundle with `schema_versions: {FinancialData: 999}` exits 2 without `--allow-schema-drift`, exits 0 with it | same file, third test |
| Integration | A bundle with `git_sha: "deadbeef"` exits 2 without `--allow-git-drift`, exits 0 with it | same file, fourth test |
| Integration | A bundle with `05-fetch-sec.raw.json` deleted produces an `errored` Result and exit code 2 (NOT a Go panic-crash). Test must run replay end-to-end through the production `datafetcher.coordinator` goroutines to exercise the goroutine-boundary safety — a panic in a child goroutine would NOT be recovered (see F11). | same file, fifth test; asserts process exit code, NOT just function return value |
| Integration | A bundle replayed in calendar year `manifest.started_at + 1 year` produces a byte-identical result to the same bundle replayed at the time of capture (regression for D10 / Finding 3 — `service.go:245` periodKey via `valuation.Clock`) | new test in integration_test.go |
| Integration | Batch over 5 synthetic bundles in a tree, with `--workers=1`, produces deterministic stdout | same file, sixth test |
| Property | Replay-replay idempotence: replaying the SAME bundle twice in a row produces byte-identical JSON output | added to integration_test.go |
| Performance | Single-bundle replay completes in ≤ 200 ms on the user's local machine | bench in `replay_bench_test.go` |
| Performance | Batch over 100 synthetic bundles completes in ≤ 30 s | same bench file |
| Lint | No new external module declarations in `go.mod` | CI check via `go list -m all` diff |
| Lint | `pkg/finance/*` byte-for-byte unchanged | grep/diff in CI script |

Coverage gates:

- `internal/observability/replay/` ≥ 90%
- `cmd/replay/` ≥ 80%
- No regression on existing package coverage.

---

## 13. Rollout & Rollback

### Rollout

Replay is a **new binary** with no impact on the running server. It can be merged in three independently-revertable phases (R1 → R2 → R3 — see §9). Production deploy is unaffected; the binary ships in the repo and is invoked manually by the user.

The branch model mirrors the v1.1 single-merge integration pattern: `feat/replay-tooling` lands on `master` as a single merge commit after the three phases.

There is one cross-package surface change worth highlighting:

- `github.com/google/go-cmp` moves from a test-scope-only import to a regular import (because `internal/observability/replay/diff.go` is a non-test file). The dependency itself is already declared in `go.mod`; only its usage scope expands. No new module is added. Operators upgrading do NOT need any migration step.

### Rollback

Reverting any of the three phases is a single revert commit:

- Revert R3 → batch parallelism / filters / stage diffs disappear; single-bundle replay still works.
- Revert R2 → engine wiring disappears; `replay` becomes a manifest validator + walker.
- Revert R1 → all replay code is gone; no production change to undo.

The artifact-bundle on-disk format is **not** modified by this spec. Bundles produced before, during, or after replay's existence are mutually compatible.

---

## 14. Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| `fx.Decorate` doesn't compose cleanly with `CoreModule`'s gateway providers | Low | Medium | Phase R2 includes an early spike (≤ 1 day) before committing to the design. Fallback (§10 "Optional") is to split the gateway providers into a minimal sub-module. No behavior change in production. |
| Float non-determinism makes legit replays look like FAILs | Medium | Medium | Default `--float-rel-tol=1e-9` is generous enough for any DCF math we run; tunable via flag. The `drifted-within-tolerance` annotation surfaces near-misses for inspection without failing the run. |
| Bundle file format silently changes under replay's feet | Low | High | The `bundle_version` field in the manifest pins the format. Replay refuses unknown versions outright (no `--allow-bundle-version-drift` flag — that's a hard incompatibility). |
| Replay is asked for a payload not in the bundle (missing file, malformed manifest) | Medium | Low | F11 hermeticity invariant: gateway returns `ErrBundleMissingPayload`; error propagates via `datafetcher.coordinator`'s existing per-source error channel; replay binary surfaces as `errored` Result with exit code 2. **Bundle gateways must NOT panic** — `internal/services/datafetcher/coordinator.go:181-196` runs gateways in goroutines, so a child-goroutine panic would crash the binary (`recover()` in the main goroutine doesn't cross goroutine boundaries). REVIEWER (§11) explicitly audits this invariant. |
| Cross-year replay produces silently-wrong results because `service.go:245` reads `time.Now().Year()` for periodKey | Was Medium / High before D10 — now Mitigated | High | D10 introduces `valuation.Clock` injection. Replay binds the Clock to `manifest.started_at`. Production binding is byte-identical to `time.Now()`. A regression test in §12 replays a bundle with simulated year-rollover and asserts byte-identical output to in-year replay. v0.1 of this spec missed this leak; v0.2 fixed it. |
| The schema_versions map drifts from production over time | Medium | Medium | A test in `internal/observability/replay/schema_test.go` round-trips a synthetic bundle through `OpenBundle` and asserts the manifest's schema_versions is a subset of `CurrentSchemaVersions`. CI fails if production stamps a version replay doesn't know about. |
| Replay accidentally writes to disk (cache, db, metrics) | Low | High | D8 NoSideEffectsModule overrides every stateful provider; panic-stubs catch wiring drift. Reviewer task in §11 explicitly audits this. |
| Replay's JSON output shape becomes a CI contract that's hard to evolve | Low | Low | Output shape is documented as additive-only; field removals/renames bump replay's version explicitly. Golden tests pin the current shape. |
| The G1 follow-up's `growth.Result` signature change breaks bundle-vs-current-code diffs | Low | Low | This is the *intended* signal: when the user implements G1 and replays old bundles, the `growth_source` / blend-weight fields will diff — exactly what they want to see. The spec covers this with `--diff-stages` so the diff is informative. |

---

## 15. What Stays the Same

- HTTP API contract — replay is a CLI; no endpoint, header, or response-shape change. The handler-package export rename in D1 is internal-only and produces no externally-visible HTTP behavior change.
- Bundle on-disk format (`00-manifest.json` schema, file naming, redaction list) — read-only by replay.
- DCF / DDM / FFO / Revenue Multiple math — replay invokes the production code unchanged. The D10 Clock injection preserves observable production behavior bit-for-bit: `wallClock.Now()` and `time.Now()` are byte-identical at every call site.
- Prometheus metrics — replay does not emit metrics; production code's metrics emissions are no-op'd in replay.
- Rate limiting and authentication — replay does not exercise the HTTP middleware chain (D1).
- Scheduler and background jobs — replay does not start them (D8).
- `pkg/finance/*` — byte-for-byte unchanged. v1.1 D7 invariant preserved. CI lint check (§12) enforces this.
- Existing v1.1 + Phases 1–2.C observability — unchanged.
- The artifact-bundle reaper — unchanged. Replay is read-only on bundles and does not bump their mtime.

**Production-source surface this spec DOES touch (additively):**
- `internal/api/v1/handlers/fair_value.go` — exports a few helpers (D1, rename-only).
- `internal/services/valuation/service.go`, `clock.go` (new), `internal/di/container.go` — `valuation.Clock` interface (D10, additive only).

---

## 16. Spec Updates

This section enumerates the cross-doc updates required when this spec is **approved and merged**. They are not part of this PR's filesystem diff.

### `AGENTS.md` (Tier 4, table of "Read Only When Relevant")

Add a new row after the existing observability spec entry:

```
| 17b | `docs/refactoring/observability-replay-tooling-spec.md` | Phase 2.D — replay CLI for offline regression of code changes against captured bundles. SHIPPED [date] as `<merge-sha>` |
```

When this spec is approved (still DESIGN), the row should read `DESIGN — awaiting approval` and be filed at the same position.

### `CLAUDE.md`

In the Build & Run section, add:

````markdown
# Replay a captured artifact bundle (Phase 2.D)
go run ./cmd/replay artifacts/<UTC-date>/<TICKER>/req_<id>/

# Batch-replay a watchlist's worth of bundles
go run ./cmd/replay --format=json --workers=4 artifacts/2026-04-25/
````

In the Architecture section, add to the `cmd/` line:

```
cmd/                    # Entry points (server, migrate, seed-demo-key, hash-key, apply-schema, replay)
```

In the "Important Files" table, add:

```
| `cmd/replay/main.go` | Replay CLI: re-runs captured bundles through the current code and diffs against saved responses |
| `internal/observability/replay/` | Replay core: bundle gateways, fx module, diff, walk |
```

### `docs/refactoring/observability-narrative-and-artifacts-spec.md`

§13 — replace the current Phase 2.D entry with a SHIPPED reference once this spec is implemented and merged. Until then, update the entry to:

```
### Phase 2.D — Replay tooling — DESIGN

A CLI command `cmd/replay/main.go` that takes a bundle directory and re-runs
the request through the current code, diffing the output against the saved
response. Full design now lives in
[observability-replay-tooling-spec.md](./observability-replay-tooling-spec.md).
```

Add a single row to that file's Change Log:

```
| 2026-05-02 | v0.7 — §13.D moved to its own spec at observability-replay-tooling-spec.md (DESIGN). The two-sentence placeholder is replaced with a forward link. |
```

### `docs/THESIS.md`

Move "Replay tooling (observability Phase 2.D)" from the Deferred Work / Next Candidate Work section to the Planned / In-Progress section (DESIGN status pending human approval).

When implementation lands, move it from Planned to Completed Phases.

---

## 17. Glossary

- **Replay** — Running a saved bundle's recorded inputs through the current code and comparing the produced outputs against the bundle's recorded outputs.
- **Bundle** — A per-request directory on disk produced by Phases 1–2.C of the parent observability spec. Self-describing via `00-manifest.json`.
- **Mode (raw / parsed)** — Controls the depth at which gateway substitution intercepts. Raw replays the parser; parsed skips it.
- **Schema drift** — The bundle's `schema_versions` map disagrees with the current code's `CurrentSchemaVersions`.
- **Git drift** — The bundle's `git_sha` disagrees with the binary's `git_sha`. Often expected (replay is for cross-revision regression); flagged so the user is conscious of it.
- **Float tolerance** — Per-field allowance for legitimate float drift between code revisions. Two knobs: relative (`abs(a-b) / max(abs(a), abs(b))`) and absolute (`abs(a-b)`).
- **Hermeticity** — Replay's invariant that no external network call, no database, no cache, no metric ship happens during replay. Bundle-gateway violations (missing payloads) surface as `ErrBundleMissingPayload` errors so they propagate safely across the `datafetcher.coordinator` goroutine boundary. Auth/Watchlist violations (which would indicate a code wiring bug, not a bundle-content gap) panic — they sit outside the goroutine path, so the panic is reachable by `recover()` in `cmd/replay/main.go`.
- **NoSideEffectsModule** — The fx sub-module that overrides every stateful production provider with one of: a no-op (cache, metrics), a not-found stub (repos), a manifest-bound substitute (Clock), or a panic-stub (Auth/Watchlist). Implementation of hermeticity.
- **Drifted-within-tolerance** — A field that moved between bundle and replay, but within configured float tolerance. Not a failure; surfaced to `--verbose` for inspection.

---

## 18. Change Log

| Date | Change |
|---|---|
| 2026-05-02 | v0.1 — Initial design. Three-phase plan (R1 walk + manifest, R2 engine wiring, R3 batch polish). 9 architecture decisions documented (D1 entry-point, D2 fx substitution, D3 raw-vs-parsed, D4 diff strategy, D5 schema drift, D6 output formats, D7 determinism, D8 side-effect isolation, D9 batch-by-default). No new external dependencies. `pkg/finance/*` invariant preserved. Status: DESIGN — awaiting human approval before implementation. |
| 2026-05-02 | v0.2 — Review-driven revision after `zen.thinkdeep` analysis with file:line evidence from the codebase. Three substantive defects fixed: (1) **Finding 1 / D1**: `internal/api/v1/handlers/fair_value.go` does NOT export the response-construction helpers v0.1 claimed it did (`grep '^func [A-Z]\w+'` returned only `NewFairValueHandler`). BACKEND now exports them as a precondition of R2; rename-only change called out as the first of two production-source modifications. (2) **Finding 2 / F11**: bundle gateways MUST return `ErrBundleMissingPayload`, NOT panic — `internal/services/datafetcher/coordinator.go:181-196` runs gateways in goroutines under `sync.WaitGroup`, and a child-goroutine panic would not be recovered by the replay binary, crashing it. F11, §6 Critical abstractions, §11 REVIEWER audit list, §12 testing rows, §14 risks row 4 all rewritten. Auth/Watchlist panic-stubs (which sit outside the goroutine path) retained as panics. (3) **Finding 3 / D10**: `service.go:245` reads `time.Now().Year()` to construct the financial-data periodKey — v0.1 dismissed this as "doesn't affect float math", but cross-year replay would silently select different financial data and falsely report a code regression. New ADR D10 introduces a `valuation.Clock` interface (production binding wraps `time.Now`; replay binds to `manifest.started_at`). `pkg/finance/*` v1.1 invariant preserved (Clock lives in `internal/services/valuation/`). Minor fixes: Finding 4a (go-cmp wording: transitive→direct), Finding 4b (`replay.ParseDurationExtended` for `7d` syntax), Finding 5 (D4 tolerance rationale: relative tol covers non-zero drift, absolute tol covers near-zero like `crp=0.0`), Finding 7 (rejected-mode-3 documented in D3). NF5 rewritten to call out the two additive production changes; §10 Files Touched expanded; §15 What Stays the Same scoped accordingly. Status: still DESIGN — awaiting human approval before implementation. |
| 2026-05-05 | v0.3 — R0 + R1 + R2 SHIPPED on master. **R0+R1 merge `8a9878f`** (2026-05-03, 8 commits): `valuation.Clock` injection (D10), replay skeleton with manifest validation, schema-drift detection, bundle walk with symlink follow-once, float-tolerant diff helpers, `ErrBundleMissingPayload` sentinel, text + JSON renderers. **R2 merge `e4d2fb2`** (2026-05-05, 17 commits across 3 BACKEND review cycles + 4 review gates): bundle gateways for SEC/Market/Macro/YFinance, side-effect stubs, `replay.Module` fx composition, `Replay()` orchestrator, `--from=raw\|parsed` CLI flag, headline round-trip + cross-year integration tests, `go-cmp` direct-import. **Stage C deviation (documented post-shipment)**: spec §5 D2 said `fx.Decorate` over `CoreModule + ServiceModule`; implementation hand-picks individual `fx.Provide` lines because CoreModule transitively pulls `*sqlx.DB` and `*redis.Client` constructors that would side-effect even when downstream consumers are decorated away. The hand-pick strengthens F11 hermeticity at the cost of needing module updates when production engine constructor signatures change. Documented in 60-line package comment at `internal/observability/replay/module.go:23-60`. **Coverage**: replay 84.5%, cmd/replay 81.4%, valuation 89.1% (improved from 88.7% baseline). `pkg/finance/*` byte-for-byte unchanged; `go.mod` adds only `github.com/google/go-cmp v0.7.0` direct-promotion (transitive→direct via testify). **R3 deferred** — the 15 advisory items surfaced by R2's REVIEWER + QA gates are tracked at `docs/reviewer/RPL2-r2-followups.md`; R3 covers parallel batch (`--workers`), filter flags (`--filter-ticker`/`--filter-since`), `--diff-stages`, `--verbose`, JSON contract golden tests, and perf benches (NF2/NF3). Implementation plan for R2 (now historical): `docs/refactoring/observability-replay-tooling-r2-implementation-plan.md` v2. |
