# CI-1.2 — Performance benchmark harness is incomplete

**Status:** OPEN — filed 2026-07-02, spun out of CI-1 (#20) when the fixed server-boot
let the benchmark step run for the first time.
**Severity:** Low. CI hygiene / tooling — the `Performance Testing` workflow is gated off the
default push/PR path (CI-1), so this blocks nothing on normal merges.
**GitHub issue:** _to be filed (`/github-tracking`)._

---

## What's wrong

The `scripts/` benchmark tool has **no `main` entrypoint**. `benchmark_runner.go`,
`benchmark_executor.go`, and `benchmark_analyzer.go` define only types + methods — there is no
`func main()` anywhere for them, so the workflow's invocation

```
go run scripts/benchmark_runner.go scripts/benchmark_executor.go scripts/benchmark_analyzer.go ...
```

fails at compile with `runtime.main_main·f: function main is undeclared in the main package`.
The `Generate comprehensive report` step is broken the same way — `go run -tags cli
scripts/performance_analyzer.go` lists one file whose symbols (`CLIConfig`,
`NewCLIPerformanceAnalyzer`, `BenchmarkResult`, …) live in sibling files that aren't listed.

This was never observed before because the `Test and Coverage` lint step always failed first (and
the perf server-start step failed on a DB-env bug — both fixed in CI-1), so the benchmark step
never actually ran.

Additionally, the benchmark scenarios (`single_ticker_baseline`, etc.) drive the **live valuation
path** (`GET /api/v1/fair-value/{ticker}`), which calls real SEC/Yahoo/FRED APIs — the same
rate-limited, CI-unfriendly dependency that gates `e2e-live`. So even with an entrypoint, this
suite is a nightly/gated concern, not a push-path check.

## Current state (from CI-1)

- The deprecated actions are bumped (v4/v5) and the server-boot env is fixed (migrate +
  `DATABASE_DRIVER`/`DATABASE_SQLITE_PATH`), so the server boots and answers `/health`.
- The `performance-test` job is **gated** (`workflow_dispatch` or PR label `perf`) off the default
  push/PR path; nightly coverage stays on the separate `scheduled-performance-test` job.

## To close

- [ ] Add a `main` entrypoint for the benchmark tool (a `//go:build benchmark`-tagged
      `scripts/benchmark_main.go` that flag-parses `-config/-duration/-concurrency/-output/-scenarios/-all-scenarios`,
      runs the selected `TestScenario`s via `NewBenchmarkExecutor`, and writes a JSON **array** of
      `BenchmarkResult` — what the analyzer's `LoadResults` expects). Update the workflow's `go run`
      lines to `-tags benchmark` + the full file list (benchmark step + scheduled job), and fix the
      analyzer step's file list.
- [ ] Point the benchmark scenarios at a mock/stub endpoint (or keep them nightly-only) so they
      don't depend on live external APIs.
- [ ] Once reliably green, drop the `perf` gate and return it to the push/PR path (or decide it
      stays nightly-only and document that).

## Out of scope
- The CI-green work (CI-1 / #20). This tracker is purely the perf-harness completion.
