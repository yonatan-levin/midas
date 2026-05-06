# Observability Replay Tooling — Phase R3 Implementation Plan

**Status:** PLAN v2 — awaiting human approval before BACKEND dispatch.

**Builds on:**
- [`observability-replay-tooling-spec.md`](./observability-replay-tooling-spec.md) v0.3 (R0+R1+R2 SHIPPED). All design decisions, ADRs, CLI contract, and testing strategy are owned by that spec.
- [`observability-replay-tooling-r2-implementation-plan.md`](./observability-replay-tooling-r2-implementation-plan.md) v2 (historical — R2 SHIPPED at merge `e4d2fb2`). This R3 plan mirrors its structure (Pre-Flight + ordered Stages + per-task contracts + coverage gates + done-when checklist).
- `docs/reviewer/RPL1-replay-walk-and-output-r3-followups.md` (3 advisory items folded in).
- `docs/reviewer/RPL2-r2-followups.md` (15 advisory items folded in).

This document does **not** redesign anything. It sequences BACKEND's work for the R3 phase only — the final phase of the Phase 2.D replay tooling refactor.

**Scope:** Phase **R3 only** — parallel batch (`--workers`), filter flags (`--filter-ticker`/`--filter-since`), `--diff-stages`, `--verbose`, `--float-rel-tol`/`--float-abs-tol` flags, JSON contract golden tests, NF2/NF3 perf benches, plus the 18 advisory follow-ups from RPL-1 + RPL-2. After R3 ships, Phase 2.D is complete.

**LoC + commit estimate (R3 only, derived from spec §1 estimate of ~600 LoC):**
- Pre-Flight parallel-fx.App spike (v2 Addition #3): ~80 LoC build-tag-gated, no commit (lands inside Stage I.0's commit OR a dedicated Pre-Flight commit at BACKEND's discretion).
- Stage I.0 (Prometheus registry audit + `lint-prometheus-registers` script — v2 Addition #1): ~15 LoC (lint script) + audit doc note, 1 commit (or folded into Stage I.1's commit).
- Stage I (parallel walker + `--workers`, with RPL-1a/1b folded in): ~180 LoC, 1 commit.
- Stage J (`--filter-ticker` + `--filter-since`): ~80 LoC, 1 commit.
- Stage K (`--diff-stages`): ~150 LoC, 1 commit.
- Stage L (`--verbose` + `--float-rel-tol`/`--float-abs-tol` + walk/replay timing instrumentation — v2 Addition #4): ~65 LoC (was 50; +15 for split-duration fields), 1 commit.
- Stage M (JSON contract golden tests + RPL-2a rename + RPL-2b parsed-mode round-trip): ~120 LoC, 1 commit.
- Stage N (perf benches NF2/NF3): ~100 LoC, 1 commit.
- Stage O (RPL-2 cleanup sweep — 7 LOW + 5 NIT): ~80 LoC across, 1–2 commits.
- Stage O.13 (`cmd/server` ↔ `replay` import-boundary CI guard — v2 Addition #2): ~10 LoC, folded into Stage O's final commit.
- **Estimated total:** ~870 LoC including tests + spike + lint script, 7–8 commits (the spike is build-tag-gated and ships alongside Stage I.0).

The spec §1 estimate of ~600 LoC excludes the cleanup sweep; with the RPL fold-ins and v2's 4 additions this plan budgets ~870 LoC (was ~760 in v1). The ratio still tracks the implementation-to-test 1:1 norm of the package.

**Commit cadence:** Each stage is a separate commit so any individual step can be reverted in isolation, mirroring R0+R1+R2.

---

## Revision History

- **v1 (initial)**: Stage breakdown for R3, RPL-1 + RPL-2 fold-in mapping, parallelism design decisions, golden-test fixture approach, perf-bench harness location.
- **v2 (2026-05-05 — revision pass after `zen.thinkdeep` validation)**: Folded in 4 additions surfaced by deep-think analysis. (1) Stage I.0 / Pre-Flight repo-wide `prometheus.MustRegister` audit — codebase evidence (PREX-1 fix at `metrics/service.go:80-85`) confirmed RPL-2g framing, but a `lint-prometheus-registers` script prevents future stray DefaultRegisterer reintroduction. (2) Stage O.13 CI guard preventing `cmd/server` from depending on `replay` package — keeps RPL-2h init() panic scope replay-binary-only under future refactors, not just current convention. (3) **Pre-Flight parallel-fx.App spike** mirroring R2's `spike_test.go` pattern — runs 4 concurrent fx.App lifecycles under `-race -count=10`, asserts no panic / no deadlock / per-app registry isolation. ~80 LoC build-tag-gated under `replay_spike`. Surfaces fx-concurrency assumptions before Stage I implementation begins. (4) Walk vs replay timing instrumentation in JSON output — makes Surface #2's scale ceiling observable rather than debated. ~15 LoC. The 5 Critical Surfaces' core decisions are unchanged from v1; only the surrounding task list grew by 4 items.

---

## Implementation Outcome (placeholder for BACKEND post-shipment)

| Stage | Result | Commit(s) |
|-------|--------|-----------|
| Pre-Flight parallel-fx.App spike (v2 Addition #3) | TBD | TBD |
| Stage I.0 (Prometheus registry audit + lint script — v2 Addition #1) | TBD | TBD |
| Stage I (parallel walker + `--workers`) | TBD | TBD |
| Stage J (filter flags) | TBD | TBD |
| Stage K (`--diff-stages`) | TBD | TBD |
| Stage L (`--verbose` + tolerance flags + walk/replay timing — v2 Addition #4) | TBD | TBD |
| Stage M (JSON golden tests + RPL-2a/RPL-2b) | TBD | TBD |
| Stage N (perf benches NF2/NF3) | TBD | TBD |
| Stage O (RPL-2 cleanup sweep) | TBD | TBD |
| Stage O.13 (`cmd/server` ↔ `replay` import-boundary guard — v2 Addition #2) | TBD | TBD |

BACKEND fills in commit SHAs and any deviations from this plan during the post-shipment record pass.

---

## 1. Preamble

**R0 + R1 + R2 already shipped on master.** Confirmed live in the repo:

- `internal/observability/replay/{errors,manifest,schema,diff,walk,output,duration,clock,types,compare}.go` plus tests (84.5% coverage).
- `internal/observability/replay/{gateway_sec,gateway_market,gateway_macro,gateway_yfinance,stubs,module,replay}.go` plus tests.
- `internal/services/valuation/clock.go` — `Clock` interface + `wallClock{}` (D10).
- `cmd/replay/main.go` — flag-parsing for the R0+R1+R2 subset: `--format`, `--out`, `--allow-schema-drift`, `--allow-git-drift`, `--quiet`, `--verbose`, `--from`. (`--verbose` exists as a flag but is wired only to the existing per-row `DriftedWithinTolerance` path; full per-field diff verbosity is R3.)

**Coverage baseline at start of R3** (per spec v0.3 change-log + RPL-2 carry-forward note):
- `internal/observability/replay/`: 84.5% (gap concentrated in defensive `if err != nil` branches; gates `≥ 90%`).
- `cmd/replay/`: 81.4% (gates `≥ 80%`).

R3's natural test additions (parallel-walk tests, filter-flag tests, golden tests, stage-diff tests) are expected to lift the replay package toward 90%. If they don't, the residual gap is acceptable per VERIFIER cycle 1 verdict — defensive paths with no logic — but BACKEND should attempt the lift before accepting carry-forward.

**Key R3 code surfaces (already shipped, will be modified):**
- `internal/observability/replay/walk.go` — single-threaded recursive walker; R3 adds parallelism wrapper.
- `internal/observability/replay/replay.go` — `Replay()` orchestrator; R3 batch-calls it under `--workers`.
- `internal/observability/replay/output.go` — JSON shape (`Report`, `Result`, `Summary`, `FloatDiff`, `StringDiff`); R3 locks via golden tests.
- `internal/observability/replay/compare.go` — manual `compareFairValueResponses` walker; RPL-2h/2i targets.
- `internal/observability/replay/diff.go` — float-tolerance helpers `CompareFloat` / `FloatDiffOf`; R3 wires `--float-rel-tol`/`--float-abs-tol` from CLI through `Options`.
- `internal/observability/replay/duration.go` — `ParseDurationExtended` (already shipped in R0+R1, ready for R3's `--filter-since` to consume).
- `internal/observability/replay/integration_test.go` — round-trip + cross-year tests; R3 adds golden + parsed-mode round-trip + perf benches.
- `cmd/replay/main.go` — flag set; R3 registers 6 new flag behaviors (workers, filter-ticker, filter-since, diff-stages, float-rel-tol, float-abs-tol).

---

## 2. Pre-Flight

**v2 update — a spike IS required for R3.** v1 framed R3 as "no spike required" on the reasoning that R3 introduces no new fx composition concerns. v2's `zen.thinkdeep` pass disagreed: R3 introduces N parallel `fx.New(...).Start(ctx)` lifecycles in one process, which is a less-trodden combination of fx primitives + concurrency. Failure modes that could surface include shared validator caches racing, internal logger registry races, fx app dispatch ordering under contention, OnStart/OnStop ordering races, and shared globals in tracing/metrics/viper/expvar leaking between apps. The spike below catches these BEFORE Stage I implementation begins, mirroring R2's `spike_test.go` pattern (which itself caught a real `fx.Decorate` composition concern).

### Pre-Flight Spike — Parallel `fx.App` lifecycle (v2 Addition #3)

**File new:** `internal/observability/replay/spike_parallel_fxapp_test.go`
**Build tag:** `replay_spike` (same tag R2's `spike_test.go` uses; the spike test is gated out of normal `go test ./...` runs and ships only when explicitly invoked).
**Approximate size:** ~80 LoC.

**Spike protocol:**
1. Spawns 4 goroutines, each constructing a `replay.Module(bundleDir, opts)` fx.App (use the same `testdata/happy/` bundle for all 4), calls `App.Start(ctx)` + `App.Stop(ctx)`, then exits its goroutine.
2. Driver runs all 4 under `sync.WaitGroup` + `t.Parallel()`.
3. Test invocation: `go test -tags=replay_spike -race -count=10 -run TestSpike_ParallelFxAppLifecycle ./internal/observability/replay/`.
4. **Pass criteria** — ALL must hold:
   - No panic in any goroutine.
   - No deadlock (each goroutine completes within a 30s timeout — use `context.WithTimeout` and check `ctx.Err()` post-return).
   - No `-race` data-race report across the entire test run.
   - Per-app `*metrics.Service` registries are independent: verify by capturing the `*metrics.Service.GetRegistry()` pointer in each goroutine (push to a goroutine-local slice; merge under mutex post-`Wait`); assert all 4 pointers are pairwise distinct (`registries[i] != registries[j]` for `i != j`).
5. **Disposition:** spike test retained behind `replay_spike` build tag as a permanent regression guard — same disposition R2's spike used. Future replay refactors that touch `replay.Module` composition or fx app lifecycle MUST re-run the spike before merge.

**Sequencing:** the spike lands BEFORE Stage I.0 (the Prometheus audit) and BEFORE Stage I.1 (the actual parallel dispatcher / replay-orchestration parallelism). If the spike fails, BACKEND surfaces the fx-concurrency issue immediately rather than discovering races during Stage I implementation. If it passes, BACKEND proceeds to Stage I.0 with confidence that the parallel-fx-App composition is sound.

**Spike commit cadence:** the spike test can ship as its own dedicated Pre-Flight commit, OR fold into Stage I.0's commit. BACKEND's choice. Either way, the spike file lands BEFORE the parallel-dispatcher code in Stage I.2.

### Two execution-level uncertainties that BACKEND should resolve early (during Stage I) but do NOT warrant a discrete spike commit:

1. **Stage Pre-I.A — Validate `errgroup.SetLimit` semantics at the project's `golang.org/x/sync` pin.** The spec §6 says parallelism is bounded by `runtime.NumCPU()` and the existing R0+R1 walker is sequential, so `golang.org/x/sync/errgroup` may not yet be a project dependency. Run `go list -m golang.org/x/sync` and confirm whether it's already pinned. If pinned: use `errgroup.SetLimit` (decision in §3 below). If NOT pinned: BACKEND has two options (decide before Stage I):
   - **Add `golang.org/x/sync` as a direct dependency** (very lightweight, stdlib-adjacent, used widely across the Go ecosystem; `go.mod` diff would be the single addition under direct block). Spec NF1 says "no new external Go module is added to the build graph" — `golang.org/x/sync` would technically be a new direct dep, BUT it is already pulled transitively by many existing deps in this project (verify with `go mod why golang.org/x/sync`). If transitive: the promotion to direct mirrors `go-cmp`'s R2 transitive→direct promotion and is acceptable per spec NF1's "the dependency itself is not new; only its scope changes" precedent.
   - **Roll a hand-coded bounded goroutine pool** (~30 LoC) using stdlib `sync.WaitGroup` + a buffered channel as the semaphore. Avoids the dep question entirely.
   - **Default recommendation:** if `golang.org/x/sync` is already transitive, promote and use `errgroup.SetLimit`. If not, hand-code the bounded pool. Either is fine; the API surface from Stage I's perspective is identical.

2. **Stage Pre-I.B — Inventory which intermediate stage files are diffable for `--diff-stages`.** Per spec §1 bundle layout (cited at the top of the spec), the candidates are: `10-clean-output.json`, `11-classify.json`, `12-growth-curve.json`, `13-wacc.json`, `14-model-selection.json`, `15-valuation.json`, `16-crosscheck.json`. R3 plan §3 Stage K below pins the initial set as `{12-growth-curve.json, 13-wacc.json, 15-valuation.json}` (the user's named G1 follow-up surface) plus `10-clean-output.json` (cleaner-stage diagnostic). BACKEND must verify these files actually appear in production-shipped bundles (run `ls artifacts/<recent-date>/AAPL/req_*/ | grep -E '^(10|12|13|15)'`) before wiring the dispatch. If a file is sometimes absent (e.g. cleaner skipped for FPI tickers), the diff path treats absent-on-both-sides as "no diff" and absent-on-one-side as a `StringDiff` at path `stages.<filename>.<missing-side>` so the operator sees the asymmetry.

Both Pre-I checks land inside Stage I's first commit; they are NOT a separate phase.

---

## 3. Ordered Task List (TDD)

Each task is `Test first → Implementation → Acceptance`. Stages run sequentially; within a Stage, tasks can be combined into a single commit if they share test file scope. BACKEND respects dependency order: the Pre-Flight spike (§2) and Stage I.0 (Prometheus audit) land first; Stage I unblocks Stages J/K/L/M/N (because they all touch the per-bundle pipeline that Stage I parallelizes); Stage O can interleave with any of them but lands cleanly at the end as a sweep, with O.13 (the `cmd/server` ↔ `replay` import-boundary CI guard) as the final commit.

### Stage I.0 — Repo-wide `prometheus.MustRegister` audit + CI lint (v2 Addition #1)

**Goal:** Verify the codebase has no stray `prometheus.DefaultRegisterer` registrations outside the per-instance-registry pattern that PREX-1 established at `internal/services/metrics/service.go:80-85,105-123`. Document the result and add a `lint-prometheus-registers` CI script (mirroring the existing `scripts/lint-logs.sh` pattern) that prevents future regressions. This stage lands BEFORE Stage I.1 because it confirms the safety property RPL-2g's half-fix relies on.

**Background:** `zen.thinkdeep` confirmed that `internal/services/metrics/service.go:80-85,105-123` (PREX-1 fix) gave Midas a per-instance `prometheus.NewRegistry()`, NOT the process-global `DefaultRegisterer`. So v1's Stage I.2 framing of "RPL-2g (Prometheus registry leak) becomes load-bearing under parallelism" remains correct, but the production hazard form is bounded — IF and ONLY IF no other package registers metrics on `prometheus.DefaultRegisterer`. PREX-1 fixed `metrics/service.go` but did not audit the rest of the tree. Stage I.0 closes that gap and adds permanent CI enforcement.

#### Task I.0.a — One-time audit

- **Action:** BACKEND runs the following checks and records results in this plan's Implementation Outcome row:
  - `grep -rn 'prometheus\.MustRegister' --include='*.go' .` — expected to be confined to `internal/services/metrics/service.go` (or empty if PREX-1 used `Registry.MustRegister` exclusively).
  - `grep -rn 'prometheus\.Register(' --include='*.go' .` — expected empty (the un-prefixed `Register` mutates `DefaultRegisterer`).
  - `grep -rn 'prometheus\.DefaultRegisterer' --include='*.go' .` — expected empty.
  - `grep -rn 'promauto\.New' --include='*.go' .` — expected empty (promauto registers against `DefaultRegisterer` by default).
- **Expected result:** all hits, if any, are inside `internal/services/metrics/service.go` and are using a per-instance `*prometheus.Registry`, NOT `DefaultRegisterer`.
- **If a stray DefaultRegisterer hit is found:** STOP — this is a pre-existing production hazard outside R3's scope. File a separate REVIEWER ticket; do not silently absorb into R3.

#### Task I.0.b — CI lint script

- **File new:** `scripts/lint-prometheus-registers.sh` (Linux/macOS) + `scripts/lint-prometheus-registers.ps1` (Windows). Mirror the structure of the existing `scripts/lint-logs.sh` / `scripts/lint-logs.ps1` from `CLAUDE.md`'s Build & Run section. Requires ripgrep (already a project prerequisite).
- **Lint logic:** the script greps for `prometheus.MustRegister`, `prometheus.Register(`, `prometheus.DefaultRegisterer`, and `promauto.New` across `**/*.go` files and FAILS (exit non-zero) if any hit lands outside an explicit allowlist. Allowlist initially contains exactly `internal/services/metrics/service.go`.
- **Allowlist mechanism:** keep the allowlist inline in the script as a small array; if a future legitimate metrics provider needs to land at a new path, the PR adds the path to the array AND documents the rationale.
- **Exit codes:** 0 = clean; 1 = stray registration found; 2 = ripgrep not installed (with a helpful install hint mirroring the lint-logs scripts).
- **Test first:**
  - Manual smoke: run the script on master HEAD; expect exit 0 (assuming Task I.0.a's audit found no strays).
  - Inject a synthetic stray (e.g., temporarily add `_ = prometheus.MustRegister(...)` in a scratch file under `internal/observability/replay/`); expect exit 1; revert.
- **Acceptance:**
  - Both scripts exist and are executable on their respective platforms.
  - Running the script on master HEAD exits 0.
  - The Done-When checklist (§7) gains a new line: `lint-prometheus-registers passes`.

#### Task I.0.c — Document the audit + script

- **File modified:** `CLAUDE.md`'s Build & Run section (post-shipment via the docs-update dispatch — DO NOT modify CLAUDE.md during R3 implementation; the spec-update enumeration in §9 captures it).
- **Acceptance (post-shipment, NOT R3):** CLAUDE.md gains a `lint-prometheus-registers.sh` invocation row right next to the `lint-logs.sh` row.

**Stage I.0 commit cadence:** Stage I.0 lands as its own commit OR folds into the start of Stage I.1's commit. BACKEND's choice. The audit is ~5 minutes of grep work; the script is ~50 lines per platform.

---

### Stage I — `--workers` parallel walker + parallelism model (RPL-1a/1b folded in)

**Goal:** Replace the single-threaded `walkOnce` recursion with a goroutine-pool-based parallel walker. The walker emits bundle directories on a channel; a separate worker pool consumes them and calls `Replay()`. Both `WalkBundles` (returning a slice) and the new pipeline-channel API exist; the pipeline-channel API is the production path for `--workers > 1`.

**Decisions resolved at the implementation-level (NOT new spec ADRs):**

- **Decision I.1 — Parallelism model:** Use `golang.org/x/sync/errgroup.Group` with `SetLimit(workers)` if it is already a project dep (or already transitive — promote per spec NF1 precedent set by `go-cmp` in R2). Otherwise, hand-code a bounded pool with `sync.WaitGroup` + a buffered semaphore channel of capacity `workers`. The choice is determined by Stage Pre-I.A above. Both approaches expose the same internal abstraction `func runParallel(ctx, workers int, work <-chan string, fn func(ctx, string) Result) []Result`.
- **Decision I.2 — `visited` set thread-safety (RPL-1b fold-in):** Replace the single-threaded `[]os.FileInfo` slice with a `sync.Mutex`-guarded `map[string]struct{}` keyed on `os.FileInfo`'s identity. Justification: `sync.Map` is optimized for read-heavy maps with rare writes; the walker is write-heavy and the read-side is `os.SameFile` against every key (not a key lookup), so `sync.Map` provides no benefit. A plain `map` plus a `sync.Mutex` is simpler. The key is `<dev>:<inode>` derived from `os.FileInfo.Sys()` on POSIX or `os.SameFile` per-entry comparison on Windows — but we keep using `os.SameFile` semantics by storing the FileInfo and locking the slice/map for both reads and writes. Concretely: `type visitSet struct { mu sync.Mutex; entries []os.FileInfo }`. The `add` and `contains` methods take the lock, scan, and append. Linear scan stays acceptable because cycle-detection only fires on symlinks, which are rare.
  - **Alternative considered & rejected:** per-goroutine snapshot with a merge step at the end. Rejected because the walker needs cross-goroutine visibility while traversing (a symlink in worker A's subtree pointing to worker B's subtree must be detected immediately). A shared mutex-guarded set is the simplest correct approach.
- **Decision I.3 — Stdout ordering under parallelism:** **Buffer per-bundle Results in memory; sort by bundle path before rendering.** This is the same approach `RenderJSON` and `RenderText` already take (both call `sort.Slice(r.Results, ...)` on the slice). With this approach, `--workers=N > 1` produces deterministic stdout order identical to `--workers=1` because rendering is post-walk and post-sort. **No streaming output mode for R3.** Rationale: the spec §7 sample output at L515-554 does not require streaming; streaming would force per-line locking and re-introduce the determinism question. R3 keeps the report-then-render model. This means Decision I.3 satisfies the spec §7 row that says `--workers=1` MUST produce deterministic stdout — **all `--workers` values produce deterministic stdout** in R3 because the renderer always sorts. Document this in the `Run()` doc-comment.
- **Decision I.4 — `walkOnce`'s `dirInfo` parameter (RPL-1a fold-in):** **Drop it.** R3's parallel walker rewrites the walk surface anyway; the YAGNI scaffolding goes with the rewrite. The new function signature emerges from the rewrite naturally — it has no use for a pre-stat'd root because the channel-emission model statics each entry as it's read.
- **Decision I.5 — RPL-1c (text-mode dual-stream drift detail):** **Leave behavior as-is.** RPL-1c is "informational, not a defect" per the file. R3's `--verbose` semantics are about per-field response diffs, not schema drift output stream policy. Adding a `--strict-stderr-drift` flag would be CLI surface for no clear win. Document the decision in this plan and in a code comment near `writeSchemaDriftDiagnostic` so a future reader sees the explicit decision.

#### Task I.1 — Replace `walkOnce` with channel-based parallel walker

- **File modified:** `internal/observability/replay/walk.go`
- **Test first:** Update `walk_test.go`:
  - Existing `TestWalkBundles_*` tests must continue to pass unchanged (the public API is `WalkBundles(rootDir string) ([]string, error)`; its behavior is unchanged for callers).
  - Add `TestWalkBundles_ParallelEmission_RaceFree` — set up a fixture tree of 50 synthetic bundles, run `WalkBundles` under `-race`, assert the returned slice contains all 50 sorted ascending. This is a smoke test for Decision I.2 (the visited-set mutex actually fires under concurrent symlink resolution).
  - Add `TestVisitSet_ConcurrentAddContains_ThreadSafe` — direct unit test against the new `visitSet` type with 100 goroutines calling `add` + `contains`; assert no race under `-race`.
  - Add `TestWalkBundles_SymlinkCycle_StillProtected_UnderParallelism` — fixture tree with a self-referential symlink; assert the walk terminates (does NOT infinite-loop) and produces the expected bundle list.
- **Implementation:**
  - Drop the `dirInfo` parameter from `walkOnce`'s signature (RPL-1a).
  - Replace the `visited []os.FileInfo` parameter with a `*visitSet` type defined in the same file:
    ```go
    type visitSet struct {
        mu      sync.Mutex
        entries []os.FileInfo
    }
    func (v *visitSet) addIfAbsent(info os.FileInfo) bool { /* returns true on first add, false if already present */ }
    ```
  - The internal recursion can stay sequential per-call-tree; parallelism is layered on top in Task I.2 by running multiple `walkOnce` calls (one per worker) against disjoint subtrees emitted by a top-level dispatcher. Alternative simpler design (RECOMMENDED): keep `WalkBundles` itself sequential (it returns a slice anyway), and put the parallelism at the per-bundle replay level (Task I.2). This is simpler and matches the spec §1 architecture diagram which shows parallelism at the per-bundle replay invocation, not at the walk.
  - **BACKEND chooses between (a) parallel walk OR (b) parallel per-bundle replay over a sequential walk.** Recommendation: **(b)** — sequential walk is fast (a directory tree traversal is I/O-bound at <1 s for 100 bundles), and parallelism at the replay level is where the 30 s budget is spent. This makes Decision I.2 (`visitSet` mutex) unnecessary if BACKEND picks (b); the existing `[]os.FileInfo` approach stays. **Plan default: (b).** If BACKEND profiles and finds the walk is the bottleneck, escalate to (a) and apply Decision I.2.
- **Acceptance:**
  - `go test ./internal/observability/replay/ -run TestWalkBundles -race -count=10` passes.
  - File-level coverage of `walk.go` ≥ 90%.
  - If (b) chosen: `walk.go` diff is small — drop `dirInfo`, document thread-safety stance ("walker is sequential; parallelism happens at the replay-orchestration layer in `cmd/replay/main.go`").
  - If (a) chosen: full `visitSet` type plus tests.

#### Task I.2 — Add `--workers` flag + parallel per-bundle replay in `cmd/replay/main.go`

- **File modified:** `cmd/replay/main.go`
- **Test first:** Update `cmd/replay/main_test.go`:
  - `TestParseFlags_Workers_DefaultIsRuntimeNumCPU` — argv without `--workers`; assert `flags.workers == runtime.NumCPU()`. Use a stub or read the runtime value; the assertion is the default lookup, not a specific number.
  - `TestParseFlags_Workers_ExplicitInteger` — argv `--workers=4`; assert `flags.workers == 4`.
  - `TestParseFlags_Workers_OneIsValid_DeterministicMode` — argv `--workers=1`; assert no error, `flags.workers == 1`.
  - `TestParseFlags_Workers_ZeroOrNegative_Errors` — argv `--workers=0` and `--workers=-1`; assert error containing `"--workers must be >= 1"`.
  - `TestRun_ParallelDispatch_AllBundlesReplayed` — fixture tree with 5 synthetic bundles; run with `--workers=3`; assert all 5 results in the output and sorted by bundle path.
  - `TestRun_WorkersOne_DeterministicStdout` — same fixture, run twice with `--workers=1`, assert byte-identical stdout across runs.
  - `TestRun_WorkersFour_DeterministicStdoutAfterSort` — same fixture, run with `--workers=4`, assert byte-identical stdout to `--workers=1` run (because rendering sorts).
  - `TestEnvVar_REPLAY_WORKERS_LowerPrecedenceThanFlag` — set `REPLAY_WORKERS=8`, pass `--workers=2`, assert flag wins (per spec §8 env-var table).
- **Implementation:**
  - Add `--workers int` flag to `parseFlags()` with default `runtime.NumCPU()`.
  - Add `int workers` field to `flags` struct.
  - Add validation: `if f.workers < 1 { return error }`.
  - Add `REPLAY_WORKERS` env var fallback (after `os.Getenv` lookup, before `flag.Parse` defaults — mirror `REPLAY_OUT` if it exists or implement both fresh).
  - In `Run()`, replace the sequential `for _, b := range bundles { res := evaluateBundle(b, f); report.Results = append(...) }` loop with a parallel dispatcher:
    - If `f.workers == 1`: keep the sequential loop unchanged (preserves R0+R1+R2 behavior bit-for-bit when explicitly requested).
    - If `f.workers > 1`: dispatch via the chosen parallelism primitive (Decision I.1). Each goroutine calls `evaluateBundle(b, f)` and pushes the `Result` to a buffered channel; main goroutine collects into `report.Results`. `errgroup.SetLimit(f.workers)` if available; hand-coded semaphore otherwise.
  - **Critical: post-collect, sort `report.Results` by `Bundle` BEFORE calling `ComputeSummary`** (it's currently order-independent, but explicit ordering keeps the renderer's `sort.Slice` from being a load-bearing stabilization step).
  - Update `usageMessage` to document `--workers`.
- **F11 hermeticity preservation under parallelism (load-bearing):**
  - Each goroutine constructs its own `replay.Module(bundleDir, opts)` fx app via `Replay()`. Per `replay.go:148-182` the fx app's lifetime is bounded by `runEngine`, so per-goroutine fx apps are independent — no shared state.
  - **RPL-2g (Prometheus registry leak) becomes load-bearing here.** With sequential R2 invocation, each `Replay()` call constructs a `*metrics.Service` whose Prometheus collectors register against a shared `prometheus.DefaultRegisterer`. Sequential calls accumulate registries (already a slow leak); parallel calls would race on registration AND accumulate faster. **Stage O (cleanup sweep) MUST land RPL-2g before Stage I exposes parallel replay to users.** Two options:
    - **(I.2.a)** Stage O runs before Stage I (re-order the plan). Drawback: cleanup sweep is logically a polish phase.
    - **(I.2.b)** RPL-2g is split: the metrics-registry concern is fixed in Stage I as a precondition; the rest of RPL-2g (documentation, broader cleanup) lands in Stage O. **Plan default: (I.2.b).** The Stage I commit pulls in a no-op metrics decorator so the parallel path is hermetic from day 1; the "document how each parallel run gets its own metrics scope" task in Stage O becomes the doc-only completion.
  - Specifically: in Stage I, modify `replay.Module` (or add a decorator) so the metrics service binding for replay is a no-op recording stub (already exists per spec §5 D8 NoSideEffectsModule), NOT the production `*metrics.Service`. RPL-2g notes the production `*metrics.Service` is currently returned from `module.go:291-293`; replace it with a per-fx-app no-op stub. This is a 5-line change; document the change here so REVIEWER can audit it.
- **Acceptance:**
  - `go test ./cmd/replay/... -run "TestRun_(Parallel|Workers)" -race -count=10` passes.
  - `cmd/replay/` coverage ≥ 80% (no regression).
  - Manual smoke: `go run ./cmd/replay --workers=4 artifacts/2026-04-25/` produces deterministic output across 3 invocations (assuming bundle data is unchanged).
  - `replay.Module` no longer registers production `*metrics.Service` for replay (RPL-2g half-fix); per-replay registration is a no-op.

### Stage J — `--filter-ticker` + `--filter-since` filter flags

**Goal:** Filter the bundle list before dispatch. Filters apply post-walk, pre-replay.

#### Task J.1 — `--filter-ticker` flag

- **File modified:** `cmd/replay/main.go`
- **Test first:** `cmd/replay/main_test.go`:
  - `TestParseFlags_FilterTicker_Empty_NoFilter` — argv without `--filter-ticker`; assert `flags.filterTicker == ""`.
  - `TestParseFlags_FilterTicker_Match_PassesThrough` — argv `--filter-ticker=AAPL`; assert string captured.
  - `TestRun_FilterTicker_OnlyMatchingBundlesReplayed` — fixture tree with 3 bundles (AAPL/MSFT/AMD); run with `--filter-ticker=AAPL`; assert exactly 1 result with `Ticker == "AAPL"`.
  - `TestRun_FilterTicker_NoMatch_EmptyResultsExitZero` — same fixture, `--filter-ticker=NONE`; assert 0 results, exit code 0 (per spec §9 R1 acceptance: 0/0 passed = exit 0).
  - `TestRun_FilterTicker_CaseSensitive` — verify the match is exact-case (no toupper); document this is intentional in the doc-comment.
- **Implementation:**
  - Add `--filter-ticker string` flag with default `""`.
  - Add filtering step in `Run()` between `WalkBundles()` and the dispatch loop: for each bundle, peek the manifest's ticker via `replay.ReadManifest(bundleDir)` and skip if `f.filterTicker != "" && mf.Ticker != f.filterTicker`. Use exact-equality comparison; case-sensitivity is intentional (tickers are conventionally uppercase in the system).
  - Reading the manifest twice (once for the filter, once inside `Replay()`) is acceptable for R3 — manifests are tiny (<1 KiB). If a future profiling pass shows it as a hotspot, fold into a single read.
  - Update `usageMessage`.
- **Acceptance:**
  - All `TestParseFlags_FilterTicker_*` and `TestRun_FilterTicker_*` tests pass.

#### Task J.2 — `--filter-since` flag

- **File modified:** `cmd/replay/main.go`
- **Test first:** `cmd/replay/main_test.go`:
  - `TestParseFlags_FilterSince_Empty_NoFilter` — no flag; `flags.filterSince == time.Duration(0)`.
  - `TestParseFlags_FilterSince_DaysSyntax_AcceptedViaParseDurationExtended` — argv `--filter-since=7d`; assert `flags.filterSince == 7 * 24 * time.Hour`. Confirms `replay.ParseDurationExtended` is wired (it already exists per R0+R1 — `internal/observability/replay/duration.go`).
  - `TestParseFlags_FilterSince_HoursSyntax_AcceptedViaGoStdParse` — argv `--filter-since=48h`; assert `flags.filterSince == 48 * time.Hour`.
  - `TestParseFlags_FilterSince_InvalidUnit_Errors` — argv `--filter-since=1w`; assert error from `ParseDurationExtended` propagates.
  - `TestRun_FilterSince_OnlyRecentBundles` — fixture tree with 3 bundles whose manifest `started_at` values are `now - 1h`, `now - 5d`, `now - 30d`; run with `--filter-since=7d`; assert 2 results (the 1h and 5d bundles).
  - `TestRun_FilterSince_BoundaryInclusive` — bundle exactly at `now - 7d`; assert it IS included (boundary is inclusive — document the choice).
- **Implementation:**
  - Add `--filter-since string` flag (string-typed because Go's `flag.DurationVar` doesn't support `7d`; we delegate to `replay.ParseDurationExtended`).
  - Parse via `replay.ParseDurationExtended(f.filterSinceRaw)` after the flagset's `Parse` returns; store result in `f.filterSince time.Duration`.
  - Add filtering step alongside ticker filter: skip bundle if `f.filterSince > 0 && time.Since(mf.StartedAt) > f.filterSince`.
  - **Boundary: inclusive (`>=`).** A bundle at exactly `now - 7d` is included by `time.Since(...) <= 7d`. Document this in the flag's usage text.
  - Update `usageMessage`.
- **Acceptance:**
  - All `TestParseFlags_FilterSince_*` and `TestRun_FilterSince_*` tests pass.
  - `replay.ParseDurationExtended` is consumed (it has been dead code since R0+R1 shipped it).

### Stage K — `--diff-stages` flag + per-stage diff logic

**Goal:** When `--diff-stages` is set, replay diffs intermediate-stage JSON files (`10-clean-output.json`, `12-growth-curve.json`, `13-wacc.json`, `15-valuation.json`) against the bundle's recorded versions, in addition to the response-level diff. Output enriches each `Result` with a per-stage diff section.

#### Task K.1 — Define the stage-diff inventory

- **File new:** `internal/observability/replay/stage_diff.go`
- **Test first:** `stage_diff_test.go`:
  - `TestStageDiffInventory_HasExpectedStages` — assert the constant slice contains exactly `{10-clean-output.json, 12-growth-curve.json, 13-wacc.json, 15-valuation.json}` (the v1 set; can grow later).
  - `TestStageDiff_BothFilesAbsent_NoDiff` — neither bundle nor current snapshot has the file; assert empty diff.
  - `TestStageDiff_FileAbsentInBundle_RecordedAsAsymmetric` — bundle missing the file but the engine produced one; assert one `StringDiff` at path `stages.<filename>.bundle_missing`.
  - `TestStageDiff_FileAbsentInCurrent_RecordedAsAsymmetric` — bundle has the file but engine didn't produce one; assert one `StringDiff` at path `stages.<filename>.current_missing`. **NOTE:** in R3, "current" comes from the engine's narrate stream OR a re-snapshot — see Implementation below for the source-of-truth decision.
  - `TestStageDiff_FloatFieldDriftWithinTolerance` — both files exist; the bundle's `13-wacc.json` has `cost_of_equity: 0.118` and the engine's has `0.118 + 1e-10`; assert it lands in `DriftedWithinTolerance` not `Diffs`.
  - `TestStageDiff_FloatFieldDriftOutsideTolerance` — `cost_of_equity` differs by 5%; assert one `FloatDiff` at path `stages.13-wacc.json.cost_of_equity`.
- **Implementation:**
  - Define `var StageDiffInventory = []string{"10-clean-output.json", "12-growth-curve.json", "13-wacc.json", "15-valuation.json"}`. Pre-I.B inventory check confirms these exist in production bundles.
  - Define a per-stage diff function `func diffStage(bundleDir, stageFile string, current any, relTol, absTol float64) (FloatDiffs []FloatDiff, StringDiffs []StringDiff)`:
    - Read `<bundleDir>/<stageFile>` → `bundleStage map[string]any` via `json.Unmarshal`.
    - Read or accept the engine-produced `current` value.
    - Walk both maps using a recursive function (the spec §6 `compareFairValueResponses` pattern is the model — see RPL-2i note about unifying once go-cmp coverage stabilizes; for R3 K.1 we keep the manual walker for stages because the stage shapes are heterogeneous and a generic `cmp.Diff` would over-report nil vs zero etc.).
  - **Source of "current" stage values:** R3 has two options:
    - **(K.1.a)** Snapshot intermediate stages during replay by tee-ing through a synthetic `artifact.Bundle` writer pointed at `t.TempDir()`. The replay's engine path naturally produces the same `12-growth-curve.json`, etc. via the production narrate emissions (per spec §1 bundle layout). Capture the snapshot directory and diff against the bundle's saved versions. Cleanest design.
    - **(K.1.b)** Re-derive each stage from the `*entities.ValuationResult` (e.g., `13-wacc.json` content is `result.WACCComponents`). Tighter coupling to entity shapes; brittle.
    - **Plan default: (K.1.a).** The replay path already ignores narrate emissions per spec §5 D8 (no-op metrics); we extend the no-op metrics decoration to include a tee-bundle writer that captures stage snapshots in-memory (NOT to disk), making them available for `--diff-stages`. ~80 LoC. Document the design choice in `stage_diff.go`'s package comment.
  - Add `Result.StageDiffs map[string]StageDiff` field where `StageDiff struct { Floats []FloatDiff; Strings []StringDiff }`. JSON-serialized as `"stage_diffs": {...}`. Field is `omitempty` so default replays (no `--diff-stages`) don't bloat the JSON output.
- **Acceptance:**
  - All tests pass.
  - File-level coverage of `stage_diff.go` ≥ 90%.

#### Task K.2 — Wire `--diff-stages` flag

- **File modified:** `cmd/replay/main.go`
- **Test first:** `cmd/replay/main_test.go`:
  - `TestParseFlags_DiffStages_DefaultFalse`.
  - `TestParseFlags_DiffStages_ExplicitTrue`.
  - `TestRun_DiffStages_PopulatesStageDiffsField` — fixture bundle with a deliberately mutated `13-wacc.json` on disk; run with `--diff-stages`; assert `result.StageDiffs["13-wacc.json"].Floats` is non-empty.
  - `TestRun_DiffStages_DisabledByDefault_ZeroStageDiffs` — same fixture without the flag; assert `result.StageDiffs == nil`.
- **Implementation:**
  - Add `--diff-stages bool` flag.
  - Add `DiffStages bool` field to `replay.Options`.
  - In `Replay()` (modify `internal/observability/replay/replay.go`): when `opts.DiffStages` is true, after `runEngine` succeeds, call `diffStage()` for each entry in `StageDiffInventory` and populate `Result.StageDiffs`.
  - Update text renderer (`output.go::writeResultRow`) to emit a "Stage diffs:" section when `Result.StageDiffs` is non-empty AND `--verbose` is set (the JSON output emits unconditionally because spec §7 sample at L497-510 shows them inline).
  - Update `usageMessage`.
- **Acceptance:**
  - All tests pass.
  - Manual smoke: `go run ./cmd/replay --diff-stages --verbose artifacts/<bundle>` produces output matching spec §7 sample shape.

### Stage L — `--verbose` + `--float-rel-tol` + `--float-abs-tol` flag wiring

**Goal:** Promote per-field diff verbosity in text mode (today's `--verbose` only triggers `DriftedWithinTolerance` listing); make the float tolerances tunable (today they're hardcoded `DefaultFloatRelTol = 1e-9`, `DefaultFloatAbsTol = 1e-12` per `diff.go:26-27`).

#### Task L.1 — `--verbose` per-field diff in text mode

- **File modified:** `internal/observability/replay/output.go` and possibly `cmd/replay/main.go`
- **Test first:** `output_test.go`:
  - `TestRenderText_VerboseFalse_OmitsDriftedWithinTolerance` — already exists; verify still passes (regression).
  - `TestRenderText_VerboseTrue_EmitsDriftedWithinTolerance` — already exists; verify still passes.
  - `TestRenderText_VerboseTrue_EmitsStageDiffsSectionWhenPresent` — populate `Result.StageDiffs` with one entry; assert text output includes the "Stage diffs:" section.
  - `TestRenderText_VerboseFalse_OmitsStageDiffsSection` — same Result, verbose=false; assert no "Stage diffs:" header.
  - `TestRenderJSON_VerboseFlag_HasNoEffectOnJSONOutput` — JSON always emits everything. Property test: render the same Result with verbose=true and verbose=false; assert byte-identical JSON output.
- **Implementation:**
  - In `output.go::writeResultRow`, add a section after the existing diff loops for `Result.StageDiffs`:
    ```go
    if verbose && len(res.StageDiffs) > 0 {
        // emit "  Stage diffs:" header
        // for each stage, emit per-field diffs
    }
    ```
  - In `output.go::RenderText`, the existing `r.Verbose` check already gates `DriftedWithinTolerance`; the new stage-diffs section uses the same gate.
- **Acceptance:**
  - All tests pass.
  - No JSON output change.

#### Task L.2 — `--float-rel-tol` and `--float-abs-tol` flags

- **File modified:** `cmd/replay/main.go`, `internal/observability/replay/replay.go`, `internal/observability/replay/types.go` (or wherever `Options` is defined)
- **Test first:** `cmd/replay/main_test.go`:
  - `TestParseFlags_FloatRelTol_DefaultIs1e9` — default flag value matches `replay.DefaultFloatRelTol`.
  - `TestParseFlags_FloatRelTol_ExplicitOverride` — argv `--float-rel-tol=1e-6`; assert captured.
  - `TestParseFlags_FloatRelTol_NegativeOrNaN_Errors` — argv `--float-rel-tol=-1` and `--float-rel-tol=NaN`; assert error (negative tolerances are nonsensical).
  - `TestParseFlags_FloatAbsTol_*` — same set for absolute.
  - `TestRun_RelaxedTolerance_FormerlyFailingBundlePasses` — fixture bundle with a 1% drift; run with default tolerance (FAIL); run with `--float-rel-tol=0.05` (5%, PASS).
- **Implementation:**
  - Add `--float-rel-tol float64` and `--float-abs-tol float64` flags. Defaults pulled from `replay.DefaultFloatRelTol` / `replay.DefaultFloatAbsTol` (already exported per `diff.go:26-27`).
  - Add `FloatRelTol float64` and `FloatAbsTol float64` fields to `replay.Options`.
  - In `Replay()`, replace the hardcoded `DefaultFloatRelTol`/`DefaultFloatAbsTol` at `replay.go:131` with `opts.FloatRelTol`/`opts.FloatAbsTol`. Add a fallback at the top of `Replay()`: `if opts.FloatRelTol == 0 { opts.FloatRelTol = DefaultFloatRelTol }` (zero is sentinel for "use default"). Same for absolute.
  - Update `usageMessage`.
- **Acceptance:**
  - All tests pass.
  - Manual smoke: `go run ./cmd/replay --float-rel-tol=1e-6 <bundle>` produces a strictly-tighter diff result than the default (or strictly-looser, depending on sign).

#### Task L.3 — Walk vs replay timing instrumentation in JSON output (v2 Addition #4)

**Background:** `zen.thinkdeep` noted that v1 Surface #2 (sequential walk + parallel replay) is sound for the user's named scale (100–1000 bundles) but the "fast walk" assumption breaks at 10K+ bundles. v1's plan had no observability into whether walk or replay dominates a given run. Adding split timing fields makes Surface #2's scale ceiling **observable rather than debated**: if a future user reports "replay is slow on 10K bundles," they have data to pinpoint walk vs replay as the bottleneck without spelunking source.

**Decision L.3.a — Field placement:** **Top-level `Report.Summary`**, NOT per-bundle. Rationale: walk duration is a single batch-level measurement (one `WalkBundles` call covers the whole run); replay duration is naturally per-bundle but its aggregate is what answers the "where's the time going" question. Per-bundle `replay_duration_ms` already exists implicitly via the `Result` struct's existing fields (or is trivially computable from start/end timestamps); the new fields below add the BATCH-level split.

**Decision L.3.b — Spec impact:** None. The JSON shape is "additive only" per spec §5 D6, so adding two new fields to `Summary` is a non-breaking change. The new fields land in the Stage M golden fixtures naturally as part of the M.1 fixture build.

- **File modified:** `internal/observability/replay/output.go` (add `WalkDurationMs int64` and `ReplayDurationMs int64` to the `Summary` struct; ~5 LoC), `cmd/replay/main.go` (capture `walkStart := time.Now()` before `WalkBundles`; `walkDuration := time.Since(walkStart)` after; capture `replayStart := time.Now()` before the dispatcher; `replayDuration := time.Since(replayStart)` after; populate the two `Summary` fields; ~10 LoC).
- **Test first:** `cmd/replay/main_test.go`:
  - `TestRun_Summary_HasWalkAndReplayDurations_BothPositive` — fixture run with 3 synthetic bundles; assert `report.Summary.WalkDurationMs > 0` AND `report.Summary.ReplayDurationMs > 0` (both should be non-zero on any real run; on tiny fixtures they might be 0 if rounded — assert `>= 0` to absorb sub-millisecond runs and document why).
  - `TestRun_Summary_ReplayDurationGreaterThanWalk_OnRealisticBatch` — fixture with 5 bundles; assert `report.Summary.ReplayDurationMs > report.Summary.WalkDurationMs` (the expected ratio for the 100–1000 bundle target). Document this as a "shape sanity check, not a perf gate."
  - `TestRenderJSON_Summary_IncludesBothDurationFields` — golden-fixture-adjacent: assert the rendered JSON contains both `"walk_duration_ms"` and `"replay_duration_ms"` keys.
- **Implementation:**
  - Add the two `int64` fields to `Summary` with `json:"walk_duration_ms"` and `json:"replay_duration_ms"` tags.
  - In `cmd/replay/main.go::Run`, capture timestamps around `WalkBundles` and around the dispatcher loop.
  - **Note on parallelism timing under `--workers > 1`:** `replay_duration_ms` measures wall-clock time spent in the dispatcher (start of dispatch to last worker complete), NOT cumulative CPU time. Document this in `Summary`'s doc-comment: a 30 s wall-clock with `--workers=4` and 100 bundles means each bundle averaged ~1.2 s of work but they ran 4-at-a-time, so the wall clock is the user's actual waiting time. This is the right number for the "scale ceiling observable" use case.
- **Acceptance:**
  - All tests pass.
  - Stage M golden fixtures (Task M.1) include both fields.
  - Manual smoke: `go run ./cmd/replay --format=json artifacts/<UTC-date>/ | jq .summary` shows both fields.

### Stage M — JSON contract golden tests + RPL-2a (round-trip rename) + RPL-2b (parsed-mode round-trip)

**Goal:** Lock in the v0.1 stable JSON shape from R0+R1 by checking in a golden fixture and asserting `RenderJSON` output matches it byte-for-byte under representative inputs. Also fold the R2 round-trip-test follow-ups: rename the self-referential test (RPL-2a) and add the `--from=parsed` round-trip (RPL-2b).

**Decision M.1 — Golden test approach:** **Checked-in `testdata/golden/*.json` fixtures + `cmp.Diff` comparison.** Rationale: the spec's JSON shape is "additive only" per §5 D6; field rename or removal MUST trip a CI failure. A golden fixture is the simplest mechanism (`go-cmp` with `cmpopts.EquateEmpty()` and `cmpopts.SortSlices` is sufficient). `jsonschema` would require a schema-maintenance pass that's out of proportion for the value.

#### Task M.1 — JSON contract golden fixture + tests

- **File new:** `internal/observability/replay/testdata/golden/json_pass_one_bundle.json`, `json_fail_one_bundle.json`, `json_errored_one_bundle.json`, `json_mixed_three_bundles.json`, `json_with_stage_diffs.json`, `json_with_drifted_within_tolerance.json`
- **File new:** `internal/observability/replay/output_golden_test.go`
- **Test first:**
  - `TestRenderJSON_GoldenFixture_PassOneBundle` — construct a `Report` representing one passing bundle; render to bytes; compare against the golden file. If divergence, the test produces a `cmp.Diff`-style report so the operator sees exactly which field changed.
  - `TestRenderJSON_GoldenFixture_FailOneBundle` — same with one failing bundle (one float diff).
  - `TestRenderJSON_GoldenFixture_ErroredOneBundle` — one errored bundle (e.g., `ErrBundleMissingPayload`).
  - `TestRenderJSON_GoldenFixture_MixedThreeBundles` — pass+fail+errored together; verify deterministic sort order and aggregate Summary.
  - `TestRenderJSON_GoldenFixture_WithStageDiffs` — Result populated with `StageDiffs`; assert the golden includes the new field.
  - `TestRenderJSON_GoldenFixture_WithDriftedWithinTolerance` — verify the existing `drifted_within_tolerance` field stays serialized.
  - `TestRenderJSON_GoldenFixture_UpdateInstructions` — meta-test: when the golden fixture diverges from the rendered output, `t.Errorf` includes a clear "to update, run UPDATE_GOLDEN=1 go test ..." hint. Implement the `UPDATE_GOLDEN` env var conventionally; document in the test file's doc-comment.
- **Implementation:**
  - Build each fixture by hand (tiny; ~30 lines each). Pin field order via Go's deterministic JSON marshaling for structs (which is field-declaration order). Use `json.MarshalIndent` with 2-space indent to match `RenderJSON`.
  - In each test, build the `Report` programmatically (NOT loaded from another fixture); render via `(*Report).RenderJSON`; compare bytes via `cmp.Diff` under `cmpopts.EquateEmpty()` (or use `bytes.Equal` for strict byte comparison — simpler and matches the "additive-only contract" tighter).
  - Bytes-equal is the recommended comparison: any change in field order, indentation, key naming, or addition/removal trips the test. The "to update" workflow uses the env var.
- **Acceptance:**
  - All tests pass.
  - `internal/observability/replay/output.go` coverage approaches 100% (all branches exercised).

#### Task M.2 — RPL-2a fold-in: rename `TestRoundTrip_ProduceBundleThenReplay_ZeroDiffs`

- **File modified:** `internal/observability/replay/integration_test.go`
- **Test first:** N/A — this is a rename; the test still runs.
- **Implementation:**
  - Rename `TestRoundTrip_ProduceBundleThenReplay_ZeroDiffs` to `TestRoundTrip_ReplaySelfConsistency_ZeroDiffs`. The new name is honest about what the test asserts (replay is deterministic against itself, NOT that replay reproduces production exactly).
  - Update the test's doc-comment to explain the limitation: "Both halves of the round-trip use the same `buildFairValueResponse` helper. A bug in that helper would pass this test silently because both sides invoke the same buggy projection. Functional 'replay reproduces production' coverage comes from `TestRenderJSON_GoldenFixture_*` and the cross-year regression test."
- **Acceptance:**
  - Test still passes (rename only).
  - Doc-comment is explicit about the limitation per RPL-2a.

#### Task M.3 — RPL-2b fold-in: add `TestRoundTrip_ReplaySelfConsistency_ParsedMode_ZeroDiffs`

- **File modified:** `internal/observability/replay/integration_test.go`
- **Test first / Implementation:**
  - Add `TestRoundTrip_ReplaySelfConsistency_ParsedMode_ZeroDiffs` (~30 LoC). Identical setup to the renamed test in M.2 but uses `Options{Mode: ModeParsed}` end-to-end. Asserts zero diffs.
  - Per RPL-2b: this closes the gap that `--from=parsed` is verified at the unit level (gateway dispatch) and CLI level (flag parse) but not at the integration level.
- **Acceptance:**
  - Test passes with `-count=10 -race`.

### Stage N — Performance benches NF2 (≤ 200 ms / single bundle) + NF3 (≤ 30 s / 100 bundles)

**Goal:** Establish performance regression guards. Per spec §12 and §11 RPL-1's "deferred to R3" framing.

**Decision N.1 — Bench harness location:** **Pre-computed synthetic corpus under `testdata/perf/` + `_bench_test.go` files in the replay package.** Rationale: generating 100 bundles in `TestMain` would slow every `go test ./...` run; pre-computed corpus is checked in once. The corpus is small (each bundle ~50 KiB; 100 × 50 KiB = 5 MiB checked into the repo, acceptable). Alternative considered: generate in `TestMain` only when running benches (gated by `testing.Short()`). Rejected for repo-cleanness — synthetic bundles are static, not dynamic.

#### Task N.1 — Synthetic perf corpus

- **File new:** `internal/observability/replay/testdata/perf/<bundle-1>/...` × 100 directories (or a generator script in `internal/observability/replay/testdata/perf/gen/main.go` that produces them deterministically — see Implementation).
- **Implementation:**
  - **Option N.1.a (RECOMMENDED):** add a generator at `internal/observability/replay/testdata/perf/gen/main.go` (kept under `gen/` so `go build ./...` doesn't compile it as part of the package) that, when run via `go run ./internal/observability/replay/testdata/perf/gen/`, produces 100 deterministic synthetic bundles. The bundles are checked into the repo (5 MiB total); the generator exists for regenerating them when the schema_versions map changes.
  - **Option N.1.b:** generate at bench-time in a `BenchmarkMain` setup. Rejected: makes benches slow.
  - **Plan default: N.1.a.**
- **Acceptance:**
  - 100 bundles checked into `testdata/perf/`.
  - Each bundle has a valid manifest, all four `*.raw.json` files (SEC, market, macro × N series), and a `17-response.json`. Schema versions match `CurrentSchemaVersions`.

#### Task N.2 — `BenchmarkReplay_SingleBundle_NF2` (≤ 200 ms target)

- **File new:** `internal/observability/replay/replay_bench_test.go`
- **Implementation:**
  - `BenchmarkReplay_SingleBundle_NF2(b *testing.B)`:
    - Pick one bundle from the synthetic corpus (e.g., `testdata/perf/AAPL-001/`).
    - In `b.ResetTimer()` loop: call `replay.Replay(ctx, bundleDir, opts)`. `b.N` controls iterations.
    - After the loop, compute `b.Elapsed() / time.Duration(b.N)`; assert it is `<= 200 * time.Millisecond` via `b.Errorf` if exceeded.
  - **NF threshold enforcement:** the spec says "≤ 200 ms on the user's local machine"; CI machines may be slower. Mitigation: emit the per-iter timing as a benchmark output (Go's bench framework already does this via `-benchmem`); the assertion fails ONLY when the per-iter exceeds `200ms * 3 = 600ms` to absorb 3x CI variance. Document the 3x slack factor in the bench comment.
- **Acceptance:**
  - `go test -bench=BenchmarkReplay_SingleBundle_NF2 ./internal/observability/replay/ -benchtime=10x` exits 0.
  - Output shows per-iter wall time.

#### Task N.3 — `BenchmarkReplay_BatchOf100_NF3` (≤ 30 s target)

- **File same:** `internal/observability/replay/replay_bench_test.go`
- **Implementation:**
  - `BenchmarkReplay_BatchOf100_NF3(b *testing.B)`:
    - Walk `testdata/perf/` to get all 100 bundle paths.
    - In `b.ResetTimer()` loop: call `replay.Replay` 100x sequentially OR run via `runParallel(workers=runtime.NumCPU())`. Run BOTH (two sub-benches: `_Sequential` and `_Parallel`).
    - Assert total wall time `<= 30 * time.Second` with the same 3x slack as N.2 (so 90 s ceiling for CI variance).
- **Acceptance:**
  - Both sub-benches pass.
  - Output shows total wall time and effective bundles/second throughput.

### Stage O — RPL-2 cleanup sweep (7 LOW + 5 NIT items)

**Goal:** Land the 12 LOW + NIT items from `docs/reviewer/RPL2-r2-followups.md` that haven't been folded into Stages I-N. Each is a focused commit-grain change.

**Note:** RPL-2a (MEDIUM) is folded into Stage M.2. RPL-2b (MINOR) is folded into Stage M.3. RPL-2g (Prometheus registry) is half-folded into Stage I.2 (the metrics no-op) and half-completed here (documentation). The remaining 11 items are tackled in this stage.

#### Task O.1 — RPL-2c: drop dead `authsvc` import sentinel

- **File:** `internal/observability/replay/module.go:354-361`
- **Change:** Remove the `_ = authsvc.NewService` line and the `authsvc` import. The comment claiming the sentinel is needed is misleading (verified per RPL-2c).
- **Acceptance:** `go build ./...` passes; module test for "no production auth wired" still passes.

#### Task O.2 — RPL-2d: resolve `var _ = artifact.ManifestVersion`

- **File:** `internal/observability/replay/replay.go:259`
- **Change:** Stage K's stage-diff implementation reads bundle stage files (e.g. `12-growth-curve.json`); if it ends up consuming `artifact.*` APIs (likely via `artifact.OpenBundle` to read snapshots), the speculative import becomes a real one. If NOT, drop the sentinel and the import.
- **Acceptance:** Either the import is genuinely consumed (and the sentinel is removed) OR both the sentinel and the import are removed.

#### Task O.3 — RPL-2e: `gitSHAResolver` package-var test seam

- **File:** `internal/observability/replay/replay.go:236-237`
- **Decision required upfront:** This is a CLAUDE.md "no globals" violation; sequential test usage is fine, but `t.Parallel()` would race it. Two options:
  - **(O.3.a)** Document explicitly that tests touching `gitSHAResolver` MUST NOT call `t.Parallel()`. Add a comment near the package-var declaration: `// gitSHAResolver is a package-level test seam. Tests overriding it MUST NOT call t.Parallel().` Lowest-cost option.
  - **(O.3.b)** Refactor `Replay()` to accept an optional `Options.GitSHAResolver func() string` field; tests inject without a global. Higher cost (~20 LoC of plumbing) but properly removes the global.
  - **Plan default: (O.3.a).** Documenting the constraint matches the project's "pragmatic, not dogmatic" stance and the cost of (O.3.b) is disproportionate. Document in this plan AND in code.
- **Acceptance:** Comment lands at `replay.go:231-237`; no behavior change.

#### Task O.4 — RPL-2f: `DataQuality: "good"` hardcode → `"replay-stub"`

- **File:** `internal/observability/replay/gateway_market.go:190-203` (`quoteToMarketData`)
- **Change:** Replace `DataQuality: "good"` with `DataQuality: "replay-stub"`. Document in the function comment that this is a sentinel for replay-context callers; if a future engine path branches on `DataQuality`, this surfaces in tests.
- **Acceptance:** The change is mechanical; existing tests must continue to pass (verify nothing in the engine path branches on the literal string `"good"`; if anything does, escalate before changing).

#### Task O.5 — RPL-2g doc completion (the half not in Stage I.2)

- **File:** `internal/observability/replay/module.go:291-293` (the `replayMetricsService` provider area)
- **Change:** After Stage I.2 ships the no-op metrics decorator, document in the function's doc-comment why the no-op is necessary under `--workers > 1`. Reference Stage I.2's commit. Confirm via test that the production `*metrics.Service` is NOT registered for replay (test exists; verify still passing).
- **Acceptance:** Doc-comment is explicit; module tests confirm hermeticity under parallelism.

#### Task O.6 — RPL-2h: `countFairValueFields` compile-time linkage

- **File:** `internal/observability/replay/diff.go:460-465`
- **Change:** Add a `func init()` that uses `reflect.TypeOf(handlers.FairValueResponse{}).NumField() + reflect.TypeOf(handlers.Industry{}).NumField() + reflect.TypeOf(entities.SanityCheck{}).NumField()` and panics on mismatch with the hand-counted constant. Catches any future field addition that didn't update both `goFieldToJSON` AND `countFairValueFields`.
  - **Caveat:** field-counting via reflection is brittle for embedded fields and `omitempty` semantics. BACKEND verifies the count math is right at implementation time; if it drifts from the existing `19 + 8 + 5 = 32`, fix the reflection rather than the constant (the 32 is the assertion, not the source of truth).
- **Acceptance:** `init()` runs at package load; if a future field is added, the panic surfaces in `go test ./...` immediately.

#### Task O.7 — RPL-2i: unify `compareFairValueResponses` with `CompareResponse`

- **File:** `internal/observability/replay/compare.go` and `internal/observability/replay/diff.go`
- **Decision required:** RPL-2i flagged the manual diff walker as a maintenance hot spot. Stage M's golden tests cover the response-diff JSON contract by output assertion, but the underlying walker is still hand-rolled.
- **Recommendation:** **Defer the unification.** The R2 plan v2 explicitly accepts the manual walker per spec D3 invariant ("a new field is invisible until added to the walker, surfaced by REVIEWER"). Stage O.6 (compile-time linkage via reflection) is a stronger guard than the unification; together with Stage O.6, the manual walker's failure mode (silent invisibility of new fields) becomes a panic-at-init, which fully addresses the original RPL-2i concern.
- **Plan default:** Drop RPL-2i from R3's scope; mark as "subsumed by O.6's compile-time guard." Document in this plan and in the RPL-2 follow-up file (post-shipment doc-update task).
- **Acceptance:** RPL-2 file is updated post-shipment to mark RPL-2i as resolved-via-O.6.

#### Task O.8 — RPL-2j: `resolveDataCleanerConfigPath` walks 4 parents

- **File:** `internal/observability/replay/module.go:212-260`
- **Change:** Replace the four-parent walk with `filepath.Walk` going UP from `runtime.Caller(0)` until the first ancestor containing `go.mod`. ~10 LoC. The semantics are identical for the current repo layout but stay correct if `module.go` moves.
- **Acceptance:** Module tests pass; the new traversal is robust against directory-depth drift.

#### Task O.9 — RPL-2k: drop dead nil check on `macroGateway`

- **File:** `internal/observability/replay/module.go:343-345`
- **Change:** Remove `if macroGateway != nil { svc.SetMacroGateway(macroGateway) }`. The parameter comes through `fx.Provide` which never produces nil. Replace with the unconditional call.
- **Acceptance:** Module tests pass.

#### Task O.10 — RPL-2l: reword `scrubTimestamps` comment

- **File:** `internal/observability/replay/integration_test.go:296-304`
- **Change:** Update doc to "scrubbed fields are the WALL-CLOCK echoes — not derived math from the wall clock" per RPL-2l verbatim.
- **Acceptance:** Diff is one comment block.

#### Task O.11 — RPL-2m: `gateway_yfinance.go::copy` shadows builtin

- **File:** `internal/observability/replay/gateway_yfinance.go:74-77`
- **Change:** Rename `copy` to `q` or `dup`. Mechanical.
- **Acceptance:** `go vet ./...` clean; existing tests pass.

#### Task O.12 — RPL-2n: `clock.go` silent fallback on `time.Parse` failure

- **File:** `internal/observability/replay/clock.go:33-42`
- **Change:** When `time.Parse` fails, log a `WARN` line via the package-level zap logger. The fallback to `valuation.NewWallClock()` stays (defense in depth), but the operator is no longer silenced about a manifest-corruption case.
- **Acceptance:** Add a unit test (`clock_test.go`) that injects a malformed timestamp; assert WARN line is captured (use `zaptest.NewLogger(t)` + `observer` to capture).

#### Task O.13 — `cmd/server` ↔ `replay` package import-boundary CI guard (v2 Addition #2)

**Background:** `zen.thinkdeep` confirmed RPL-2h's `init()` reflection guard panic scope is replay-binary-only because `cmd/server` does not import the replay package. Verified via grep — only `cmd/replay/main.go:23` and a doc-comment in `internal/api/v1/handlers/fair_value.go:110` reference it. The catch: this is currently true by convention, NOT by enforcement. A future refactor (or someone extracting a helper "shared library" between server and replay) could silently reintroduce the dependency, at which point the `init()` panic scope collapses and replay-package field-walker drift could brick production startup.

**Solution:** a Go test that uses `go/packages` (or `go list`) to load `cmd/server`'s transitive dep set and asserts no path matches `/observability/replay`. ~10 LoC. Lands as the very last R3 commit (Stage O.13) so it gates everything that came before.

- **File new:** `cmd/server/import_boundary_test.go`
- **Build tag (optional but recommended):** none. The test runs as part of normal `go test ./cmd/server/...`. (An earlier draft considered `// +build !nofence`; rejected because the boundary should be unconditionally enforced — there is no legitimate reason to bypass it.)
- **Test first / Implementation:**
  - Define `TestImportBoundary_CmdServer_DoesNotDependOnReplayPackage(t *testing.T)`.
  - Use `golang.org/x/tools/go/packages` to load `./cmd/server` with `LoadDeps | LoadImports` config (or run `go list -deps ./cmd/server` via `os/exec` and parse output — simpler, no new dep).
  - Assert no entry in the dep set has a path matching `github.com/midas/dcf-valuation-api/internal/observability/replay` or `github.com/midas/dcf-valuation-api/cmd/replay`.
  - On violation: `t.Errorf` with a clear message: `"cmd/server transitively imports replay package via path: %v. The replay package contains init()-time reflection guards that will panic-on-startup in cmd/server if a field-walker drift is introduced. Either revert the new import, or extract the shared symbol into a third package neither replay nor cmd/server depends on."`.
- **Decision required upfront — `go list` vs `go/packages`:**
  - **(O.13.a) `go list -deps ./cmd/server` via `os/exec`:** zero new deps, simplest implementation, ~5 LoC plus parsing. Output is one import path per line; trivial to filter.
  - **(O.13.b) `golang.org/x/tools/go/packages`:** richer API, but `golang.org/x/tools` is not currently a project dep — adding it for one test would violate spec NF1. Rejected.
  - **Plan default: (O.13.a).** No new deps; clean implementation.
- **Acceptance:**
  - Test runs as part of `go test ./cmd/server/...` and passes on master HEAD.
  - Manual injection check: temporarily add `import _ "github.com/midas/dcf-valuation-api/internal/observability/replay"` to `cmd/server/main.go`; verify the test FAILS with the descriptive error message; revert.
  - The Done-When checklist (§7) gains a new line: `cmd/server import boundary holds`.

---

## 4. Per-task contract details (parallel walker + F11 invariant)

### 4.1 Goroutine boundary in `cmd/replay/main.go::Run` (Stage I.2)

The load-bearing concern under parallelism is preserving the F11 hermeticity invariant — `internal/services/datafetcher/coordinator.go:181-196` runs gateway calls in goroutines under `sync.WaitGroup`, and a panic in a child goroutine is NOT recovered by the replay binary's main goroutine. R2 designed bundle gateways to return `ErrBundleMissingPayload` precisely for this reason.

R3's parallelism layer wraps **another** goroutine boundary: each `Replay()` invocation runs under a worker goroutine. The full call stack under `--workers=N` becomes:

```
main goroutine
  └─ Run() (cmd/replay/main.go)
       └─ errgroup worker N (Stage I.2)
            └─ Replay() (internal/observability/replay/replay.go)
                 └─ runEngine() — fx.App.Start()
                      └─ valuation.Service.CalculateValuation
                           └─ datafetcher.Coordinator.FetchAll (R2-era)
                                └─ goroutine per gateway (the F11 boundary)
```

Two recover boundaries are needed:

- **Outer recover** (Stage I.2): each errgroup worker MUST `defer recover()` to convert any panic that escapes `Replay()` into a `Result{Status: StatusErrored, Error: "panic in replay worker: %v"}`. This converts an Auth/Watchlist stub panic (which is allowed per F11; sits OUTSIDE the F11 goroutine path) into a graceful errored Result instead of crashing the binary. Without this, a parallel replay run could die mid-batch on a wiring drift detection.

- **F11 invariant unchanged**: bundle gateways still return `ErrBundleMissingPayload` (NOT panic). The R2 implementation is preserved; R3 doesn't loosen this.

#### Goroutine-safety re-audit checklist for Stage I.2

BACKEND verifies before commit:

- [ ] Each errgroup worker constructs its own `replay.Module(...)` fx app via `Replay()`. NO shared `*fx.App` across workers.
- [ ] Each errgroup worker has a `defer recover()` that converts panics to errored Results.
- [ ] Bundle gateway structs (post-R2) are immutable post-construction; struct types are stateless. `BundleSECGateway`, `BundleMarketGateway`, `BundleMacroGateway`, `BundleYFinanceGateway` already meet this per R2 plan §4.1.
- [ ] `*metrics.Service` is replaced with the no-op stub (Stage I.2 incorporates RPL-2g half-fix).
- [ ] `gitSHAResolver` package-var (Stage O.3) is documented as `t.Parallel()`-incompatible.
- [ ] No production-engine path mutates package-level state during a single `Replay()` invocation. Spot-checked at R2; verified again here for any post-R2 additions.

### 4.2 Stage K stage-diff source-of-truth

Per Decision K.1.a above: stage diffs draw "current" values from a tee'd in-memory `artifact.Bundle` writer that captures snapshots during the engine run. Concrete contract:

- `replay.Module` receives an option `opts.CaptureStageSnapshots bool` (default false; set to true when `--diff-stages` is passed).
- When true, the module wires a writer that stores snapshots in a thread-local map keyed by stage filename (`12-growth-curve.json` → `[]byte`).
- After `runEngine` returns, `Replay()` reads the in-memory map and diffs each entry against the bundle's saved file at the same name.

Goroutine safety: under `--workers > 1`, each fx app has its own writer (per-app state), so per-worker maps are isolated. **No shared map across workers.** Document explicitly in the writer's doc-comment.

### 4.3 Stage M golden test maintenance flow

When a future BACKEND adds a field to `FairValueResponse` (or any other JSON-serialized struct in the replay output):

1. The Stage O.6 `init()` reflection check fires at test load — fail-fast feedback.
2. The new field is added to `goFieldToJSON` AND `countFairValueFields` (the reflection check now passes).
3. The Stage M golden tests fail with a `cmp.Diff`-style message showing the new field.
4. Operator runs `UPDATE_GOLDEN=1 go test ./internal/observability/replay/ -run TestRenderJSON_GoldenFixture` to regenerate fixtures.
5. The diff is reviewed in PR; an additive change is acceptable per spec §5 D6 ("additive only").
6. Renames or removals MUST bump replay's version (per spec §5 D6 final paragraph). The PR description documents the bump.

This flow is documented in `output_golden_test.go`'s package comment.

---

## 5. Test Plan (R3-specific)

Authoritative file-by-file test inventory for R3, derived from spec §12 R3-applicable rows and the per-stage tests above.

### 5.1 New test files

| File | Test name | Stage | Assertion |
|---|---|---|---|
| `spike_parallel_fxapp_test.go` (NEW, build-tag-gated under `replay_spike`) | `TestSpike_ParallelFxAppLifecycle` | Pre-Flight (v2 Addition #3) | 4 concurrent fx.App lifecycles complete with no panic / no deadlock / no race / per-app metrics registries pairwise distinct |
| `scripts/lint-prometheus-registers.sh` + `.ps1` (NEW; v2 Addition #1) | (smoke run, not a Go test) | Stage I.0 | Exit 0 on master HEAD; exit 1 on synthetic stray `prometheus.MustRegister` outside allowlist |
| `walk_test.go` (extended) | `TestWalkBundles_ParallelEmission_RaceFree` | I.1 | 50 bundles found under `-race` |
| `walk_test.go` (extended) | `TestVisitSet_ConcurrentAddContains_ThreadSafe` (only if Decision I.1.a chosen) | I.1 | `visitSet` mutex correctness |
| `cmd/replay/main_test.go` (extended) | `TestParseFlags_Workers_*` (×4) | I.2 | flag parse |
| `cmd/replay/main_test.go` (extended) | `TestRun_ParallelDispatch_AllBundlesReplayed` | I.2 | 5 bundles, workers=3, sorted output |
| `cmd/replay/main_test.go` (extended) | `TestRun_WorkersOne_DeterministicStdout` | I.2 | byte-identical across runs |
| `cmd/replay/main_test.go` (extended) | `TestRun_WorkersFour_DeterministicStdoutAfterSort` | I.2 | byte-identical workers=1 vs workers=4 |
| `cmd/replay/main_test.go` (extended) | `TestEnvVar_REPLAY_WORKERS_LowerPrecedenceThanFlag` | I.2 | env var + flag interaction |
| `cmd/replay/main_test.go` (extended) | `TestParseFlags_FilterTicker_*` (×2) + `TestRun_FilterTicker_*` (×3) | J.1 | filter ticker flag |
| `cmd/replay/main_test.go` (extended) | `TestParseFlags_FilterSince_*` (×4) + `TestRun_FilterSince_*` (×2) | J.2 | filter since flag |
| `stage_diff_test.go` (NEW) | `TestStageDiffInventory_*`, `TestStageDiff_*` (×6) | K.1 | per-stage diff logic |
| `cmd/replay/main_test.go` (extended) | `TestParseFlags_DiffStages_*`, `TestRun_DiffStages_*` (×4) | K.2 | flag wiring |
| `output_test.go` (extended) | `TestRenderText_VerboseTrue_EmitsStageDiffsSectionWhenPresent`, `TestRenderText_VerboseFalse_OmitsStageDiffsSection` | L.1 | verbose stage diff |
| `output_test.go` (extended) | `TestRenderJSON_VerboseFlag_HasNoEffectOnJSONOutput` | L.1 | JSON unaffected |
| `cmd/replay/main_test.go` (extended) | `TestParseFlags_FloatRelTol_*`, `TestParseFlags_FloatAbsTol_*`, `TestRun_RelaxedTolerance_*` (×7) | L.2 | tolerance flags |
| `cmd/replay/main_test.go` (extended) | `TestRun_Summary_HasWalkAndReplayDurations_BothPositive`, `TestRun_Summary_ReplayDurationGreaterThanWalk_OnRealisticBatch`, `TestRenderJSON_Summary_IncludesBothDurationFields` (×3) | L.3 (v2 Addition #4) | walk vs replay split timing in JSON `Summary` |
| `output_golden_test.go` (NEW) | `TestRenderJSON_GoldenFixture_*` (×6) | M.1 | JSON contract lock-in |
| `integration_test.go` (extended) | `TestRoundTrip_ReplaySelfConsistency_ZeroDiffs` (RENAMED from `TestRoundTrip_ProduceBundleThenReplay_ZeroDiffs`) | M.2 | RPL-2a fold-in |
| `integration_test.go` (extended) | `TestRoundTrip_ReplaySelfConsistency_ParsedMode_ZeroDiffs` | M.3 | RPL-2b fold-in |
| `replay_bench_test.go` (NEW) | `BenchmarkReplay_SingleBundle_NF2` | N.2 | NF2 perf gate |
| `replay_bench_test.go` (NEW) | `BenchmarkReplay_BatchOf100_NF3_Sequential` + `_Parallel` | N.3 | NF3 perf gate |
| `cmd/server/import_boundary_test.go` (NEW; v2 Addition #2) | `TestImportBoundary_CmdServer_DoesNotDependOnReplayPackage` | O.13 | `cmd/server` transitive dep set excludes `/observability/replay` and `/cmd/replay` |

### 5.2 Stage O test changes

| Task | Test impact |
|---|---|
| O.1 (drop authsvc sentinel) | Remove imports; existing module tests still pass |
| O.2 (resolve manifest-version sentinel) | None or test-cleanup depending on resolution |
| O.3 (gitSHAResolver doc) | None; documentation only |
| O.4 (`DataQuality: "replay-stub"`) | Existing gateway_market_test.go assertions update if any check string literal `"good"` |
| O.5 (RPL-2g doc) | Existing module test confirms metrics no-op (added in I.2) |
| O.6 (countFairValueFields init) | Add `TestCountFairValueFields_MatchesReflection` to verify init-time reflection-vs-constant pinning |
| O.8 (resolveDataCleanerConfigPath) | Module tests still pass; new test verifies traversal works at depth 5 (not just 4) |
| O.9 (drop nil check) | Module tests still pass |
| O.10 (scrubTimestamps comment) | None; comment only |
| O.11 (rename `copy` var) | Existing tests still pass; `go vet ./...` clean |
| O.12 (`clock.go` WARN log) | New `TestClockManifest_MalformedTimestamp_LogsWarn` |
| O.13 (`cmd/server` import-boundary CI guard — v2 Addition #2) | New `TestImportBoundary_CmdServer_DoesNotDependOnReplayPackage` under `cmd/server/` |

---

## 6. Coverage Gates

| Path | Threshold | Source |
|---|---|---|
| `internal/observability/replay/` | ≥ 90% | spec NF6; R2 baseline 84.5% — R3 SHOULD lift this |
| `cmd/replay/` | ≥ 80% | spec NF6; R2 baseline 81.4% — must not regress |
| `internal/services/valuation/` | no regression vs R2's 89.1% baseline | R3 makes no production source changes here |
| **New per-file expectations (R3 surfaces)** | | |
| `walk.go` (after Stage I.1) | ≥ 90% | per-file gate |
| `cmd/replay/main.go` (after Stages I-N) | ≥ 80% | per-file gate |
| `stage_diff.go` (NEW, Stage K.1) | ≥ 90% | per-file gate |
| `output.go` (after Stage L.1) | ≥ 95% | per-file gate (golden tests exercise nearly every branch) |

**Verification:**
```
go test ./internal/observability/replay/... ./cmd/replay/... -coverprofile=cov.out
go tool cover -func=cov.out
```

If R3's natural test additions don't lift the replay package to 90%, the residual gap is acceptable per VERIFIER cycle 1 verdict from R2 — defensive `if err != nil` branches with no logic. BACKEND attempts the lift; if 88-89% lands, that's a documented carry-forward with a one-line note in the post-shipment record.

---

## 7. Done-When Checklist

BACKEND uses this to determine R3 is ready for VERIFIER hand-off. Two items below (marked HUMAN) come from the R2-era dispatch's "what HUMAN should verify" checklist; R3 inherits the polarity flip per the user-supplied dispatch instruction.

- [ ] **Pre-Flight spike (v2 Addition #3) PASSED**: `go test -tags=replay_spike -race -count=10 -run TestSpike_ParallelFxAppLifecycle ./internal/observability/replay/` exits 0; spike file lives at `internal/observability/replay/spike_parallel_fxapp_test.go` and is retained behind the `replay_spike` build tag as a permanent regression guard
- [ ] **Stage I.0 (v2 Addition #1)**: Prometheus registry audit completed; `lint-prometheus-registers.sh` / `.ps1` ship under `scripts/` and pass on master HEAD; no stray `prometheus.MustRegister` / `prometheus.Register(` / `prometheus.DefaultRegisterer` / `promauto.New` outside `internal/services/metrics/service.go`
- [ ] §3 Stage I tasks complete; parallel walker / `--workers` flag wired; `TestRun_ParallelDispatch_AllBundlesReplayed` passes with `-race -count=10`
- [ ] §3 Stage I.2 RPL-2g half-fix landed (replay metrics no longer registers production `*metrics.Service`)
- [ ] §3 Stage J tasks complete; `--filter-ticker` and `--filter-since` flags wired; tests pass
- [ ] `replay.ParseDurationExtended` is consumed by `--filter-since` (was dead code from R0+R1)
- [ ] §3 Stage K tasks complete; `--diff-stages` flag wired; `stage_diff.go` lands with ≥ 90% coverage
- [ ] §3 Stage L tasks complete; `--verbose` extended for stage diffs; `--float-rel-tol` and `--float-abs-tol` tunable
- [ ] **Stage L.3 (v2 Addition #4)**: JSON output `Summary` exposes `walk_duration_ms` and `replay_duration_ms` separately; `TestRun_Summary_HasWalkAndReplayDurations_BothPositive` and `TestRenderJSON_Summary_IncludesBothDurationFields` pass
- [ ] §3 Stage M tasks complete; six golden fixtures checked into `testdata/golden/`; `TestRenderJSON_GoldenFixture_*` pass
- [ ] §3 Stage M.2 RPL-2a fold-in: `TestRoundTrip_ReplaySelfConsistency_ZeroDiffs` is the new name; doc-comment is honest
- [ ] §3 Stage M.3 RPL-2b fold-in: `TestRoundTrip_ReplaySelfConsistency_ParsedMode_ZeroDiffs` exists and passes
- [ ] §3 Stage N tasks complete; synthetic perf corpus checked into `testdata/perf/`; NF2 + NF3 benches pass at 3x slack
- [ ] §3 Stage O tasks complete; 11 RPL-2 follow-ups landed (12 minus RPL-2i which is subsumed by O.6)
- [ ] **Stage O.13 (v2 Addition #2)**: `cmd/server` import boundary holds — `cmd/server/import_boundary_test.go` runs as part of `go test ./cmd/server/...` and asserts `cmd/server`'s transitive dep set contains no `internal/observability/replay` or `cmd/replay` path
- [ ] RPL-1a (`dirInfo` parameter) folded into Stage I.1 — dropped per Decision I.4
- [ ] RPL-1b (visited-set thread-safety) folded into Stage I.1 — Decision I.2 applies if Decision I.1.a chosen; otherwise documented in walk.go's package comment
- [ ] RPL-1c (text-mode dual-stream drift) folded into Stage I.5 decision — left as-is, decision documented in code comment near `writeSchemaDriftDiagnostic`
- [ ] §6 coverage gates met for every file
- [ ] `go test ./... -race` full repo green
- [ ] `go vet ./...` clean
- [ ] **HUMAN**: `git diff master..HEAD -- pkg/finance/` is empty (D7 v1.1 / NF4 invariant)
- [ ] `git diff master..HEAD -- go.mod` shows AT MOST one new direct dep (`golang.org/x/sync` if Decision I.1 chose errgroup AND it wasn't already direct; otherwise empty per spec NF1)
- [ ] **HUMAN**: `lint-logs.sh` clean (project's observability lint guard from CLAUDE.md Build & Run section; verifies request-path code uses `logctx.From(ctx)`. R3 doesn't add new logger calls in service-path code, but verify the new clock.go WARN logging in O.12 lands in a non-request-path file)
- [ ] **HUMAN**: R3 flags now ARE registered with real behavior (the R2 deferral test that asserted `--workers`/`--filter-ticker`/etc. were absent is removed or its polarity flipped: now asserts the flags ARE present)
- [ ] Manual smoke: `go run ./cmd/replay --workers=4 artifacts/<UTC-date>/` produces deterministic output
- [ ] Manual smoke: `go run ./cmd/replay --filter-ticker=AAPL --filter-since=7d artifacts/` filters correctly
- [ ] Manual smoke: `go run ./cmd/replay --diff-stages --verbose artifacts/<bundle>` shows per-stage drift detail

---

## 8. Risks & How to Handle (R3-Specific)

Spec §14 covers all design risks; the table below is R3-execution-specific.

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Parallelism introduces a race condition that escapes `-race` because it depends on real I/O timing | Medium | High | Run all parallel tests with `-count=20 -race` (vs the package norm of 10x). Stage I tests explicitly exercise 50-100 bundles to maximize race-detection surface. The metrics-registry fix (RPL-2g half) is mandatory before parallelism opens to users. |
| Perf benches (Stage N) flake under CI load | Medium | Low | 3x slack factor on the wall-time assertion (so 600 ms ceiling for NF2, 90 s for NF3). Run benches in a dedicated CI step that doesn't compete with other tests. If flakes persist, downgrade to advisory-only (emit timing, don't fail). |
| Golden tests (Stage M) drift on minor JSON-shape changes | Low | Low | This is by design — see §4.3 maintenance flow. Document the `UPDATE_GOLDEN=1` regeneration workflow in the test file's package comment so a future contributor isn't surprised. |
| Stage K's tee'd in-memory snapshot writer (Decision K.1.a) doesn't capture all stages because the engine path doesn't call `b.Snapshot` for every stage | Medium | Medium | Stage Pre-I.B inventory check verifies which stages are actually written in production. If a stage in `StageDiffInventory` isn't captured by the engine, drop it from the inventory and document the omission. Stage K's tests assert against the verified inventory, not the spec's aspirational list. |
| `--workers > 1` panic in an Auth/Watchlist stub (legitimate per F11) crashes the entire batch | Low | High | Stage I.2's outer `defer recover()` catches it and converts to `StatusErrored`. Test added: `TestRun_AuthStubPanic_DoesNotCrashBatch` — produces a synthetic bundle that triggers the auth path (somehow — probably forces a code path change in module.go for the test build); asserts batch continues. If unrealistic to test, document the recover as defensive and rely on integration coverage. |
| Stage O.6's reflection-based field count panics at init in production builds (not just tests) | Low | High | The `init()` is deliberately at package load. If the count diverges, the binary FAILS TO START. This is the desired behavior — a silent invisibility regression is worse than a startup crash — but BACKEND must verify the math at implementation time. If reflection over embedded fields is brittle, fall back to a build-tag-gated sanity check that runs in tests only. |
| RPL-2g's metrics-registry fix breaks an existing test that depends on the production `*metrics.Service` | Low | Low | The Stage I.2 commit must run `go test ./internal/observability/replay/` to verify; if any test depends on the production binding, it gets updated to expect the no-op stub. |
| `--filter-since` parses correctly but the manifest's `started_at` field is missing for older bundles | Low | Low | `replay.ReadManifest` already returns an error for malformed manifests; the filter step propagates that. Bundles produced post-Phase-1 have `started_at` populated; older bundles aren't replayable anyway per spec NG7. |
| `golang.org/x/sync` promotion conflicts with NF1 (no new external Go modules) | Low | Low | Per Stage Pre-I.A: if it's already transitive (likely — pulled by many ecosystem deps), the promotion is precedent (R2 did this with `go-cmp`). If not transitive, hand-code the bounded pool. Decision is reviewable. |

---

## 9. Spec Updates Needed Post-R3

Forward-looking; **do not apply during R3 implementation.** Enumerated for the post-R3 docs-update dispatch.

### `docs/refactoring/observability-replay-tooling-spec.md`

- Append a Change Log row:
  ```
  | 2026-MM-DD | v0.4 — R3 SHIPPED as <merge-sha>. Phase 2.D COMPLETE. Parallel batch (`--workers`) with errgroup-bounded pool, filter flags (`--filter-ticker`/`--filter-since` consuming `ParseDurationExtended`), `--diff-stages` for per-stage diff detail, `--verbose` extended for stage diffs, tunable float tolerances via `--float-rel-tol`/`--float-abs-tol`. JSON contract locked via 6 golden fixtures under `testdata/golden/`. Performance benches NF2 (single-bundle ≤ 200 ms) and NF3 (100-bundle batch ≤ 30 s) pass at 3x CI slack. 18 advisory follow-ups from RPL-1 + RPL-2 closed (RPL-2i subsumed by Stage O.6's compile-time field-count guard). Coverage: replay <X>%, cmd/replay <Y>%. `pkg/finance/*` byte-for-byte unchanged; `go.mod` adds at most `golang.org/x/sync` direct-promotion if errgroup chosen.
  ```
- Update §1 Status: `R0 + R1 + R2 SHIPPED on master. R3 deferred...` → `R0 + R1 + R2 + R3 SHIPPED on master. Phase 2.D COMPLETE.`
- Update §9 Phase R3 entry: Status SHIPPED + commit SHA.

### `AGENTS.md` Tier 4 table

- Update the tracking row to `Phase 2.D R3 SHIPPED [date] as <merge-sha>; Phase 2.D COMPLETE`.

### `CLAUDE.md`

- In Build & Run section, add:
  ```bash
  # Parallel batch replay across a watchlist of bundles
  go run ./cmd/replay --workers=4 --format=json artifacts/2026-04-25/

  # Filter to a specific ticker or recent bundles only
  go run ./cmd/replay --filter-ticker=AAPL --filter-since=7d artifacts/

  # Per-stage diff detail (intermediate-stage drift inspection)
  go run ./cmd/replay --diff-stages --verbose artifacts/<UTC-date>/<TICKER>/req_<id>/
  ```
- Reorganize the "Replay tooling is hermetic by construction" Common Gotcha to mention parallelism preservation: "F11 hermeticity is preserved under `--workers > 1` because each worker constructs its own `replay.Module` fx app; bundle gateway structs are immutable post-construction and `*metrics.Service` is replaced with a no-op stub to avoid Prometheus registry accumulation."

### `docs/reviewer/RPL1-replay-walk-and-output-r3-followups.md`

- Append a section documenting which decision in Stage I addressed each item (RPL-1a dropped per Decision I.4, RPL-1b documented per Decision I.2, RPL-1c left as-is per Decision I.5).
- Mark file-level status as RESOLVED.

### `docs/reviewer/RPL2-r2-followups.md`

- Append a section documenting which Stage in R3 addressed each item.
- Mark RPL-2i as "subsumed by Stage O.6's compile-time field-count guard."
- Mark file-level status as RESOLVED (or partially-resolved with the carry-forwards explicitly listed).

### `docs/THESIS.md`

- Move "Replay tooling (observability Phase 2.D)" from Planned/In-Progress to Completed Phases when R3 lands.

---

## 10. Implementation Outcome (placeholder for BACKEND)

BACKEND populates this section post-shipment, mirroring the R2 plan's outcome table format.

- Pre-Flight spike result (v2 Addition #3): TBD
- Stage I.0 audit + lint script result (v2 Addition #1): TBD
- Stage I result: TBD
- Stage J result: TBD
- Stage K result: TBD
- Stage L result (including L.3 walk/replay timing — v2 Addition #4): TBD
- Stage M result: TBD
- Stage N result: TBD
- Stage O result: TBD
- Stage O.13 import-boundary guard result (v2 Addition #2): TBD

Document any deviations from this plan with a rationale and a code commit reference. The R2 plan landed 5 deviations across its stages; R3's deviation count is expected to track R2's (the v2 Pre-Flight spike was added precisely to surface fx-composition concerns early, the only category of unknown that could drive late deviations).

---

## Appendix A — RPL fold-in mapping

| Item | Severity | Location | R3 Stage that absorbs |
|---|---|---|---|
| RPL-1a — `walkOnce`'s `dirInfo` unused | Low (advisory) | walk.go:77,167 | Stage I.1 (Decision I.4 — drop) |
| RPL-1b — `visited` slice thread-safety | Low (advisory) | walk.go:25-30, 111-160 | Stage I.1 (Decision I.2 — `visitSet` mutex if I.1.a; else doc) |
| RPL-1c — text-mode dual-stream drift | Low (informational) | output.go:250-269 + main.go:204-206 | Stage I.5 (decision: leave as-is) |
| RPL-2a — round-trip self-referential test (MEDIUM) | MEDIUM | integration_test.go:125-150 | Stage M.2 (rename + doc) |
| RPL-2b — parsed-mode round-trip gap (MINOR) | MINOR | integration_test.go | Stage M.3 (new test) |
| RPL-2c — dead `authsvc` import | LOW | module.go:354-361 | Stage O.1 |
| RPL-2d — `var _ = artifact.ManifestVersion` | LOW | replay.go:259 | Stage O.2 (resolved by Stage K consumption OR drop) |
| RPL-2e — `gitSHAResolver` package-var | LOW | replay.go:236-237 | Stage O.3 (Decision: O.3.a doc-only) |
| RPL-2f — `DataQuality: "good"` hardcode | LOW | gateway_market.go:190-203 | Stage O.4 (sentinel rename) |
| RPL-2g — Prometheus registry leak | LOW | module.go:291-293 | Stages I.2 (no-op stub) + O.5 (doc completion) |
| RPL-2h — `countFairValueFields` no compile-time linkage | LOW | diff.go:460-465 | Stage O.6 (init reflection guard) |
| RPL-2i — manual diff walker hot spot | LOW | compare.go:25-150 | Stage O.7 (subsumed by O.6) |
| RPL-2j — `resolveDataCleanerConfigPath` walks 4 parents | NIT | module.go:212-260 | Stage O.8 (go.mod-anchored walk) |
| RPL-2k — dead nil check on `macroGateway` | NIT | module.go:343-345 | Stage O.9 (drop) |
| RPL-2l — `scrubTimestamps` comment | NIT | integration_test.go:296-304 | Stage O.10 (reword) |
| RPL-2m — `gateway_yfinance.go::copy` shadows builtin | NIT | gateway_yfinance.go:74-77 | Stage O.11 (rename) |
| RPL-2n — `clock.go` silent fallback | NIT | clock.go:33-42 | Stage O.12 (WARN log) |

**Total:** 18 follow-up items, all addressed in R3.

---

## Appendix B — Decisions Resolved by This Plan (Implementation-Level, NOT New Spec ADRs)

| # | Decision | Choice | Rationale |
|---|---|---|---|
| I.1 | Parallelism primitive | `errgroup.SetLimit` if dep available; hand-coded bounded pool otherwise | Identical API surface from caller's perspective; choose based on dep graph |
| I.2 | Visited-set thread-safety | `sync.Mutex`-guarded slice (only if I.1.a parallel walk chosen) | Linear scan acceptable for cycle-rare symlinks; mutex simpler than `sync.Map` |
| I.3 | Stdout ordering under parallelism | Buffer + sort (deterministic for all `--workers` values) | Spec §7 row satisfied; no streaming complexity |
| I.4 | RPL-1a `dirInfo` resolution | Drop the parameter | YAGNI; R3's parallel-aware walker rewrites it anyway |
| I.5 | RPL-1c dual-stream drift | Leave as-is; document in code | RPL-1c file says "fine; becomes action item only if spec changes" |
| K.1 | Stage-diff source-of-truth | Tee'd in-memory snapshot via fx-injected writer | Cleanest; avoids re-deriving from `*ValuationResult` |
| M.1 | JSON golden test approach | Checked-in fixtures + `bytes.Equal` (with `UPDATE_GOLDEN=1` regen) | Matches "additive-only" contract enforcement tighter than `cmp.Diff` |
| N.1 | Bench harness location | Pre-computed corpus under `testdata/perf/` | 5 MiB acceptable; bench-time generation slows default test runs |
| O.3 | `gitSHAResolver` global | Document `t.Parallel()`-incompatibility (O.3.a) | Cost of refactor (O.3.b) disproportionate; pragmatic over dogmatic |
| O.7 | RPL-2i diff-walker unification | Subsumed by O.6's compile-time field-count guard | Strongest possible regression guard against the "new field invisible" failure mode |
| Pre-Flight (v2 Addition #3) | Parallel-fx.App spike disposition | Build-tag-gated under `replay_spike`; retained as permanent regression guard | Mirrors R2's `spike_test.go` precedent; future `replay.Module` refactors can re-run cheaply |
| I.0 (v2 Addition #1) | Prometheus stray-registration enforcement | One-time audit + permanent CI lint script (`scripts/lint-prometheus-registers.sh` / `.ps1`) | Codifies PREX-1's per-instance-registry pattern; prevents silent reintroduction of the global `DefaultRegisterer` hazard |
| L.3 (v2 Addition #4) | Walk vs replay timing field placement | Top-level `Summary.WalkDurationMs` + `Summary.ReplayDurationMs` (NOT per-bundle) | Walk is one batch-level call; per-bundle `Result` already carries timing implicitly. Aggregate fields answer the "where's the time going" question for the 10K+ bundle scale ceiling |
| O.13 (v2 Addition #2) | `cmd/server` ↔ `replay` boundary enforcement primitive | `go list -deps ./cmd/server` via `os/exec` (O.13.a) | Zero new deps; rejects the `golang.org/x/tools/go/packages` alternative because adding `golang.org/x/tools` for one test would violate spec NF1 |
