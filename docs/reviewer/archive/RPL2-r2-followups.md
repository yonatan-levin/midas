# RPL-2 — Phase 2.D R2 follow-ups (deferred to R3)

**Status:** OPEN — filed 2026-05-05 from REVIEWER cycle 1 + QA cycle 1 on R2 (merge `e4d2fb2`).
**Severity:** Mixed (1 MEDIUM + 1 MINOR + 8 LOW + 5 NIT — none blocked the R2 merge).
**Origin:** REVIEWER + QA cycle-1 verdicts on the 17-commit R2 branch (`2c4b60c..6d485c3`), reviewed against spec v0.2 and plan v2.

## Context

Phase 2.D R2 (gateway substitution + engine wiring producing a real `*entities.ValuationResult` for a single bundle, diffed against `17-response.json`) shipped on master via merge `e4d2fb2`. The 4-gate validation cycle (VERIFIER × 2, REVIEWER, QA) closed all spec-conformance and correctness questions. Fifteen advisory findings of varying severity remain — all explicitly non-blocking, all naturally fitted to R3's scope (parallel batch, filter flags, `--diff-stages`, `--verbose`, JSON contract golden tests, perf benches). Filing as a single grouped item so R3's BACKEND dispatch folds them in without per-item context-rebuilding.

---

## RPL-2a — Round-trip integration test is self-referential (MEDIUM)

**Source:** REVIEWER cycle 1.
**Location:** `internal/observability/replay/integration_test.go:125-150` (`TestRoundTrip_ProduceBundleThenReplay_ZeroDiffs`).

Both halves of the round-trip (bundle production + replay) use the same `buildFairValueResponse` helper. A bug in that helper would pass the test silently because both sides invoke the same buggy projection. The test proves "replay is deterministic against itself" — NOT "replay reproduces what production produced." Functional coverage comes from the mutated-response test and the cross-year regression test, but the round-trip test's name oversells what it asserts.

**Fix direction:** add a golden-file test (hand-crafted `FairValueResponse` not engine-derived) and assert the engine reproduces it within tolerance, OR rename to `TestRoundTrip_ReplaySelfConsistency_ZeroDiffs` and document the limitation prominently in the test's doc-comment.

**Fits R3:** R3 introduces JSON contract golden tests; the golden-file pattern fits naturally there.

## RPL-2b — `--from=parsed` round-trip integration coverage gap (MINOR)

**Source:** QA cycle 1.
**Location:** Same file as RPL-2a.

CLI dispatches `--from=parsed` correctly to gateways (verified). Gateway-unit tests cover `ModeParsed` dispatch (verified). Orchestrator passes `Options.Mode` correctly (verified). What's missing is an end-to-end "produce bundle → replay with `Mode=ModeParsed` → zero diffs" assertion at the integration level.

**Fix direction:** add `TestRoundTrip_ProduceBundleThenReplay_ParsedMode_ZeroDiffs` (~30 LoC, identical setup to the existing round-trip test but with `Options{Mode: ModeParsed}`).

**Fits R3:** R3 revisits the integration-test surface for golden tests + perf benches.

## RPL-2c — Dead `authsvc` import sentinel (LOW)

**Source:** REVIEWER.
**Location:** `internal/observability/replay/module.go:354-361`.

`_ = authsvc.NewService` keeps the `authsvc` import alive, but nothing in the `fx.Provide` lines references it. Comment claiming otherwise is misleading.

**Fix:** drop both sentinel lines and the `authsvc` import.

## RPL-2d — `var _ = artifact.ManifestVersion` speculative import (LOW)

**Source:** REVIEWER.
**Location:** `internal/observability/replay/replay.go:259`.

Speculative import sentinel for "future code likely will" use it. If genuinely consumed, write a real call. If not, drop the import.

## RPL-2e — `gitSHAResolver` package-var test seam (LOW)

**Source:** REVIEWER.
**Location:** `internal/observability/replay/replay.go:236-237`.

Test seam works correctly for sequential tests (uses `t.Cleanup`), but two callers running with `t.Parallel()` would race the package-level mutable. CLAUDE.md's "no globals" rule cautions against this pattern.

**Fix direction:** either (a) document explicitly that tests touching `gitSHAResolver` MUST NOT call `t.Parallel()`, or (b) refactor `Replay()` to accept an optional `Options.GitSHAResolver` field so tests inject without a global.

## RPL-2f — `DataQuality: "good"` hardcode in BundleMarketGateway (LOW)

**Source:** REVIEWER.
**Location:** `internal/observability/replay/gateway_market.go:190-203` (`quoteToMarketData`).

Hardcodes `DataQuality: "good"`. If a future engine path gates math on `DataQuality`, replay silently lies.

**Fix direction:** emit a sentinel value like `"replay-stub"` instead, so a future engine consumer that branches on quality sees a recognizable string in tests.

## RPL-2g — `replayMetricsService` accumulates Prometheus registries (LOW)

**Source:** REVIEWER.
**Location:** `internal/observability/replay/module.go:291-293`.

Returns the production `*metrics.Service`, which constructs Prometheus collectors. Today this is fine because each replay is one `fx.App` per process, but R3's parallel batch + per-bundle parallelism would accumulate registries.

**Fix direction:** when R3 lands per-bundle parallelism, document how each parallel run gets its own metrics scope (or route through a no-op metrics impl entirely).

## RPL-2h — `countFairValueFields` lacks compile-time linkage (LOW)

**Source:** REVIEWER.
**Location:** `internal/observability/replay/diff.go:460-465`.

Hand-counted constant `19 + 8 + 5 = 32`. Adding a new struct field requires updating `goFieldToJSON` AND this constant — three places, no compile-time guard.

**Fix direction:** add a `func init()` that compares `countFairValueFields()` against `reflect.TypeOf(handlers.FairValueResponse{}).NumField() + reflect.TypeOf(handlers.Industry{}).NumField() + reflect.TypeOf(entities.SanityCheck{}).NumField()` and panics on mismatch. Or convert to a function that walks reflection at call time.

## RPL-2i — Manual diff walker maintenance hot spot (LOW)

**Source:** REVIEWER.
**Location:** `internal/observability/replay/compare.go:25-150` (`compareFairValueResponses`).

Diff walker enumerates fields manually. Adding a new `FairValueResponse` field requires editing this list AND the `goFieldToJSON` map AND `countFairValueFields`. Three places, no compile-time linkage. Acceptable for R2 as documented per spec D3 invariant ("a new field is invisible until added to the walker, surfaced by REVIEWER"), but worth a unification with `CompareResponse` (the go-cmp-based path).

**Fix direction:** unify with `CompareResponse` when R3 lands JSON golden tests.

## RPL-2j — `resolveDataCleanerConfigPath` walks 4 parents (NIT)

**Source:** REVIEWER.
**Location:** `internal/observability/replay/module.go:212-260`.

Walks up four parents from `runtime.Caller(0)` to reach repo root. If `module.go` moves to a different directory depth, the path silently breaks.

**Fix direction:** walk up until the first ancestor containing `go.mod`. Or use `filepath.Walk` to find `config/datacleaner` going up.

## RPL-2k — Dead nil check on `macroGateway` (NIT)

**Source:** REVIEWER.
**Location:** `internal/observability/replay/module.go:343-345`.

`if macroGateway != nil { svc.SetMacroGateway(macroGateway) }` — the parameter comes through `fx.Provide`, which never produces nil.

**Fix:** drop the `if`. If defense-in-depth is wanted, add an fx-level constraint instead.

## RPL-2l — `scrubTimestamps` comment slightly inaccurate (NIT)

**Source:** REVIEWER.
**Location:** `internal/observability/replay/integration_test.go:296-304`.

Doc says scrubbed fields "literally are the Clock's read echoed back." More accurate: they're the wall-clock echoes — not derived math from the wall clock. Subtle but important distinction for the next maintainer.

**Fix:** reword to "scrubbed fields are the WALL-CLOCK echoes — not derived math from the wall clock."

## RPL-2m — `gateway_yfinance.go::copy` shadows builtin (NIT)

**Source:** REVIEWER.
**Location:** `internal/observability/replay/gateway_yfinance.go:74-77`.

Variable `copy` shadows Go's builtin. Compiles fine; flagged by some linters.

**Fix:** rename to `q := *quote` or `dup := *quote`.

## RPL-2n — `clock.go` silent fallback on `time.Parse` failure (NIT)

**Source:** REVIEWER.
**Location:** `internal/observability/replay/clock.go:33-42`.

When `time.Parse` fails, falls back silently to `valuation.NewWallClock()`. Manifest-corruption case — production replay would silently mis-clock without surfacing.

**Fix:** log a `WARN` line via the package-level zap logger when fallback fires.

---

## Why deferred to R3

R3 is scoped for: parallel batch execution (`--workers`), filter flags (`--filter-ticker`, `--filter-since`), `--diff-stages`, `--verbose`, JSON contract golden tests, perf benches (NF2 ≤ 200 ms / NF3 ≤ 30 s).

All 15 sub-items above sit in surfaces R3 will be touching:

- **Test surface** (RPL-2a, RPL-2b, RPL-2l): R3 introduces golden tests + per-mode integration coverage
- **Module/gateway code paths** (RPL-2c, RPL-2d, RPL-2e, RPL-2f, RPL-2g, RPL-2j, RPL-2k, RPL-2m, RPL-2n): R3 touches module wiring + gateway behavior
- **Diff walker** (RPL-2h, RPL-2i): R3's JSON golden tests will exercise these paths

Bundling into the R3 dispatch keeps the patch surface unified rather than producing tiny cleanup commits on R2.

## Open coverage gap (informational, not a finding)

R2 final coverage: replay package 84.5%, `cmd/replay` 81.4%. Spec NF6 targets are 90% / 80%. VERIFIER accepted at the lower number because gaps are concentrated in defensive `if err != nil` branches with no logic of their own. R3's natural test additions (golden tests, parallel-walk tests, filter-flag tests) will likely lift the number organically; if not, this is a known carry-forward.

## Acceptance criteria

- [ ] Each sub-item resolved (or explicitly carry-forward closed) by R3's BACKEND dispatch
- [ ] R3's plan/dispatch reads this file and addresses the items as part of its scope
- [ ] Coverage gap addressed if R3's natural test additions lift it; otherwise documented as acceptable

## Traceability

- Filed by: REVIEWER cycle 1 + QA cycle 1 of R2 (2026-05-05)
- Specs it relates to: `docs/refactoring/observability-replay-tooling-spec.md` (v0.3 post-merge), `docs/refactoring/observability-replay-tooling-r2-implementation-plan.md` (v2 — R2 stages complete)
- Code it relates to: `cmd/replay/main.go`, `internal/observability/replay/*`, `internal/api/v1/handlers/fair_value.go`, `internal/infra/gateways/macro/parser.go`
- R2 commits the items were observed against: 17 commits across `2c4b60c..6d485c3`, merged as `e4d2fb2`
