# RPL-3 — Phase 2.D R3a deferred items + cleanup sweep (R3b backlog)

**Status:** **RESOLVED 2026-05-09** — all 16 items closed by R3b dispatch (merge `0741958`). R3a (parallel batch + filter flags + tolerance flags + Stage O sweep) shipped on master via merge `011d78c`; R3b (Stage K + L.1 + M.3 + M.1 + N + O.6 + R3b-Final cleanup + 3 polish commits) shipped on master via merge `0741958` 3 days later. See `## Resolution` section at end of this file for per-item commit-SHA mapping. Phase 2.D is COMPLETE.
**Severity:** Mixed — 5 deferred Stages (capability work, ~700-800 LoC) + 8 LOW NITs (Go-style modernization, ~50 LoC) + 1 missing test + 1 R2 modernization sweep.
**Origin:** R3a's 4 review-gate cycles surfaced the deferrals: BACKEND-1/2/3/4 ran out of quota before completing the full plan; VERIFIER cycles 1/2/3 + REVIEWER + QA confirmed the partial as mergeable; HUMAN approved the partial-merge with this file as the explicit backlog.

## Context

R3a shipped 9 of 14 plan stages cleanly across 11 commits and 4 BACKEND continuation cycles. The 5 deferred stages and 8 LOW NITs are non-blocking — the partial is correct as-shipped, all 4 review gates green (VERIFIER × 3, REVIEWER, QA × 1) — but the work was scoped out due to per-dispatch quota walls. R3b becomes one well-scoped future BACKEND run that completes Phase 2.D.

Spec v0.4 records R3a SHIPPED + R3b deferred. The R3 implementation plan v2 is now historical (status SHIPPED with implementation outcome filled in).

---

## Section A — 5 deferred Stages (capability work, ~700-800 LoC total)

### RPL-3a — Stage K (`--diff-stages` engine wiring)

**Severity:** Capability (R3b dispatch).
**Status:** Plan §3 Stage K not implemented; flag intentionally NOT registered (see commit `f9c99b5` which dropped the contract leak).

R3a's `cmd/replay/main.go` has a doc-comment block at lines 128-132 explaining why `--diff-stages` is currently absent: the CLI surface was advertised in R3a-cycle-2 but the engine never read `Options.DiffStages`, so VERIFIER cycle 1 caught it as a contract leak and BACKEND-3 dropped the flag entirely. R3b re-adds the flag *after* implementing the engine-side per-stage diff machinery. Plan §3 Stage K specifies the `stage_diff.go` shape: read `13-wacc.json`, `12-growth-curve.json`, `15-valuation.json`, `10-clean-output.json` from the bundle; render per-stage diffs in text/JSON output.

**Estimated:** ~300 LoC + tests.

### RPL-3b — Stage L.1 (verbose stage-diff text render)

**Severity:** Capability (R3b dispatch).

Blocked on RPL-3a. The `--verbose` flag currently retains its R0+R1+R2 meaning (drives `DriftedWithinTolerance` annotation only). R3b's L.1 extends `--verbose` to render per-field stage diffs in text mode — natural fit alongside Stage K.

**Estimated:** ~80 LoC + tests.

### RPL-3c — Stage M.1 (JSON contract golden tests)

**Severity:** Capability (R3b dispatch).

Plan §3 Stage M.1 specifies golden-file tests pinning the JSON output shape against checked-in fixtures, with an `UPDATE_GOLDEN=1` env var harness for intentional updates. R3a's JSON shape is currently locked only by inline assertions in `r3_run_test.go` (which delete timing fields before structural checks). Golden tests would catch any future field rename / removal at CI rather than at runtime.

**Estimated:** ~150 LoC + 6 fixture files (~50 KB).

### RPL-3d — Stage M.3 (parsed-mode round-trip integration test)

**Severity:** Capability (R3b dispatch).

R3a-BACKEND-2 attempted Stage M.3 but reverted because `seedFullBundle` (the existing test fixture) only emits raw-mode payloads. Stage M.3 requires a parallel `seedFullBundle_ParsedMode` that emits `*.parsed.json` shapes for the gateway parsers to consume directly. Documented in-line at the integration_test.go reverted-attempt site so a future BACKEND sees the gap.

QA cycle 1 explicitly flagged the absence of `--from=parsed` round-trip integration coverage as Issue Q-MINOR-3 in its R2 review (carried forward to RPL-2b). R3b's M.3 closes the loop.

**Estimated:** ~150 LoC for the parsed-mode fixture + ~30 LoC for the new round-trip test.

### RPL-3e — Stage N (perf benches NF2 ≤ 200ms / NF3 ≤ 30s)

**Severity:** Capability (R3b dispatch).

Plan §3 Stage N + spec NF2/NF3 require a synthetic 100-bundle corpus + benches asserting per-bundle replay completes in ≤ 600ms (NF2 with 3× CI slack) and 100-bundle batch completes in ≤ 90s (NF3 with 3× CI slack). R3a deferred entirely. The `--workers > 1` parallel dispatch (Stage I.2) is already wired and would be the load-bearing capability under bench.

**Estimated:** ~150 LoC bench code + a generated fixture corpus (~5 MB checked-in or generated at TestMain).

### RPL-3f — Stage O.6 (`init()` reflection guard for `countFairValueFields`)

**Severity:** Capability (R3b dispatch).

R3a-BACKEND deferred this Stage O.6 item (RPL-2h carry-forward). The reflection guard is documented in plan §3 Stage O.6: at package init, walk `reflect.TypeOf(handlers.FairValueResponse{})` and assert the field count matches `countFairValueFields`'s hand-counted constant. On mismatch, the replay binary refuses to start (panic at init). Failure scope is replay-binary-only because Stage O.13's import-boundary CI guard ensures `cmd/server` doesn't import the replay package transitively.

**Estimated:** ~30 LoC + 1 test.

---

## Section B — 8 LOW NITs (Go-style modernization sweep, ~50 LoC total)

All non-blocking per REVIEWER cycle 1 + VERIFIER cycle 3. R3b folds them into a single coordinated cleanup commit because they touch the same surfaces R3b will be modifying for Stages K/L.1/M.1.

### RPL-3g — `forvar` shadow at `cmd/replay/main.go:430`

**Source:** REVIEWER cycle 1 + VERIFIER cycle 3 line-shift surfacing.
**Origin:** Stage I.2 commit `2136444`.

`i, b := i, b` shadow inside `for i, b := range bundles`. Module declares `go 1.23.0` so per-iteration semantics are in effect; the shadow is dead code that future readers will mis-read as load-bearing.

**Fix:** drop the shadow line.

### RPL-3h — `rangeint` at `internal/observability/replay/module.go:262`

**Source:** REVIEWER cycle 1.
**Origin:** Stage O sweep commit `5d5d5dc`.

`for i := 0; i < 16; i++` could be `for range 16` (Go 1.22+ integer-range form). The variable `i` is unused inside the loop body.

**Fix:** convert to `for range 16`.

### RPL-3i — `rangeint` + `forvar` at `spike_parallel_fxapp_test.go:69-70`

**Source:** REVIEWER cycle 1.
**Origin:** Pre-Flight spike commit `e793d77`.

Same `for i := 0; i < numWorkers; i++` + `i := i` shadow combo behind `replay_spike` build tag.

**Fix:** convert to `for i := range numWorkers` + drop the shadow.

### RPL-3j — `stringscutprefix` at `internal/observability/replay/duration.go:58`

**Source:** VERIFIER cycle 3 line-shift surfacing.
**Origin:** Pre-existing R1 (predates R3 entirely).

`strings.HasSuffix + strings.TrimSuffix` could be `strings.CutSuffix` (Go 1.21+). One-line refactor.

**Fix:** replace with `strings.CutSuffix`.

### RPL-3k — Dropped-flag rationale comment archaeology at `cmd/replay/main.go:128-132`

**Source:** REVIEWER cycle 1.
**Origin:** Cycle-3 commit `f9c99b5`.

When R3b ships Stage K and re-adds `--diff-stages`, the rationale comment block at lines 128-132 ("this previously had X, now deferred to Stage K") becomes confusing archaeology. Acceptable for R3a (preserves continuity for the next BACKEND) but should be removed when Stage K lands.

**Fix:** drop the comment block as part of Stage K's commit.

### RPL-3l — `_ = marketGateway` parameter discard clarity at `internal/observability/replay/module.go:367-374`

**Source:** REVIEWER cycle 1.

`_ = marketGateway` reads as "unused" but is actually consumed transitively by `valuation.NewService`. Future maintainer might be tempted to delete the parameter.

**Fix:** add a one-line comment explaining the transitive consumption, OR remove the underscore (Go allows named-but-unused parameters in this context).

### RPL-3m — `Summary.DurationMs` doc-comment clarity at `output.go:122-130`

**Source:** REVIEWER cycle 1.

`Summary.ReplayDurationMs` correctly notes wall-clock-vs-CPU-time semantics under `--workers > 1`. `Summary.DurationMs` (the cumulative per-bundle sum) does NOT — under `--workers=4` `DurationMs` ~= `replay_duration_ms` × 4 for evenly-loaded batches, which can confuse operators.

**Fix:** add to `DurationMs` doc-comment: "Sum of per-bundle wall-clock; under `--workers > 1` this exceeds `ReplayDurationMs` because workers run concurrently."

### RPL-3n — `--float-rel-tol=0` silent-default footgun at `replay.go:131-141`

**Source:** REVIEWER cycle 1.

Zero-as-default sentinel for `FloatRelTol` / `FloatAbsTol` works, but `--float-rel-tol=0` from the CLI silently means "use default 1e-9" rather than "no tolerance / exact match." The CLI parser at `main.go:204` rejects negative+NaN+Inf but allows `0`.

**Fix:** add a one-line note in the CLI usage block at `main.go:82` saying `--float-rel-tol=0 means "use default 1e-9"`.

---

## Section C — Missing panic-coverage test (LOW)

### RPL-3o — `evaluateBundleWithRecover` panic-coverage test missing

**Source:** REVIEWER cycle 1.
**Location:** `cmd/replay/main.go:444` (the `defer recover()` at the worker boundary).

REVIEWER noted there's no test that asserts `evaluateBundleWithRecover`'s defer-recover actually catches a panic. The recover is defense-in-depth for the Auth/Watchlist stub layer (which sits OUTSIDE the F11 goroutine boundary); the test that would prove it works (inject a panicking stub, confirm the worker returns `StatusErrored` without crashing the binary) is missing. Today the panic path is unreachable, so the gap is non-blocking.

**Fix:** add a test that constructs a synthetic Replay() that panics inside its worker goroutine; assert the parent process doesn't crash and the Result has `Status=StatusErrored` with the panic value in the error string.

---

## Section D — R2 modernization sweep (NIT, separate from R3 work)

### RPL-3p — `mapsloop` + `interface{}→any` in `internal/observability/replay/integration_test.go:47-49 + :242`

**Source:** REVIEWER cycle 1 (mis-attributed to R3 by VERIFIER cycle 1; corrected by `git blame`).
**Origin:** Pre-existing R2 code from commit `8434989`.

`for k, v := range src { dst[k] = v }` could use `maps.Copy` (Go 1.21+); `var recovered interface{}` could be `var recovered any` (Go 1.18+).

**Fix:** included in R3b's coordinated Go-style modernization sweep across the whole replay package (covers RPL-3g, 3h, 3i, 3j, 3p in one commit).

---

## Why deferred to R3b

R3a hit successive quota walls across 4 BACKEND cycles. Continuing to push for full completion would have required a 5th BACKEND continuation, which the self-imposed cap (and the structural-rethink threshold) flagged as escalation territory. The 5 deferred Stages alone are ~700-800 LoC — well-scoped for one R3b dispatch with a clean ARCH planning pass if needed. The 8 LOW NITs are cosmetic; folding them into R3b's natural touches of the same files keeps the patch surface unified rather than producing a third tiny commit on the R3a merge.

R3b dispatch should:
1. Read this file as the explicit backlog
2. Run an ARCH planning pass for Stage K specifically (the largest item, ~300 LoC, deserves design attention alongside `stage_diff.go`'s contract)
3. Execute Stages K → L.1 → M.1 → M.3 → N → O.6 in plan order
4. Sweep RPL-3g through RPL-3p as a final cleanup commit
5. After R3b ships: spec bumps to v0.5, marks Phase 2.D COMPLETE, and the AGENTS.md Tier 4 entry transitions to "ALL R0–R3 SHIPPED"

## Acceptance criteria

- [ ] All 5 deferred Stages (RPL-3a through RPL-3f) completed in R3b's BACKEND dispatch
- [ ] All 8 LOW NITs (RPL-3g through RPL-3p) addressed in a single coordinated cleanup commit
- [ ] `evaluateBundleWithRecover` panic-coverage test (RPL-3o) added
- [ ] R3b's plan/dispatch explicitly references this file
- [ ] Coverage gap (replay 84.4% → 90% target) addressed if R3b's natural test additions lift it; otherwise documented as final acceptable

## Traceability

- Filed by: R3a HUMAN merge step (2026-05-06) consolidating findings from VERIFIER × 3 + REVIEWER cycle 1 + QA cycle 1
- Specs it relates to: `docs/refactoring/observability-replay-tooling-spec.md` (v0.4 post-R3a, v0.5 post-R3b), `docs/refactoring/observability-replay-tooling-r3-implementation-plan.md` (v2 — R3a stages SHIPPED), `docs/refactoring/observability-replay-tooling-r3b-implementation-plan.md` (v1 — R3b stages SHIPPED per §10)
- Code it relates to: `cmd/replay/main.go`, `internal/observability/replay/*.go`, `internal/observability/replay/integration_test.go`, `cmd/server/import_boundary_test.go`
- R3a commits the items were observed against: 11 commits across `e793d77..959997f`, merged as `011d78c`
- R3a merge: `011d78c` (2026-05-06)
- R3b merge that closed all 16 items: `0741958` (2026-05-09)
- Prior follow-up files: `RPL1-replay-walk-and-output-r3-followups.md` (R0+R1, all items folded into R3 plan v2), `RPL2-r2-followups.md` (R2, all items folded into R3 plan v2 except O.6/O.7 which deferred again to RPL-3f)
- Subsequent follow-up file: `RPL4-r3b-followups.md` (R3b, 4 deferred items — 1 spec/sample documentation call + 2 cross-platform polish + 1 documented coverage residual; Phase 2.E candidates)

---

## Resolution (2026-05-09)

All 16 items closed by R3b. Per-item commit-SHA mapping (commits live in worktree `worktree-agent-a927bf55184a27f2a`, merged into master as `0741958`):

### Section A — 5 deferred Stages

| Item | Commit | Notes |
|---|---|---|
| RPL-3a — Stage K (`--diff-stages` engine wiring + `stage_diff.go`) | `905b295` | Decision K.1 fallback used (ephemeral temp-dir bundle via `os.MkdirTemp` rather than tee-writer default — REVIEWER independently judged "better than the plan's documented fallback"). 9 tests in `stage_diff_test.go` cover inventory pin + asymmetric absences + within/outside tolerance + nested path + string change + new-field-on-current + malformed JSON. |
| RPL-3b — Stage L.1 (verbose stage-diff text render) | `b87b3b7` | `writeStageDiffSection` in `output.go` emits sorted-by-stage-filename per-stage subheaders + per-field rows. 4 new render tests in `output_test.go`. |
| RPL-3c — Stage M.1 (JSON contract golden tests) | `339a273` | 6 fixtures under `internal/observability/replay/testdata/golden/json_*.json` + `output_golden_test.go` + `UPDATE_GOLDEN=1` regeneration harness. |
| RPL-3d — Stage M.3 (parsed-mode round-trip) | `145b23d` | New `seedFullBundle_ParsedMode` helper in `integration_test.go`; `TestRoundTrip_ReplaySelfConsistency_ParsedMode_ZeroDiffs` passes under `-race -count=10`. |
| RPL-3e — Stage N (perf benches NF2/NF3) | `ab4b02b` | 3 benches in `replay_bench_test.go`: NF2 single-bundle (≤200ms gate, 3.5ms measured), NF3 sequential 100-bundle (≤30s gate, 329ms measured), NF3 parallel 100-bundle (≤30s gate, 87ms measured). Synthetic corpus generator inlined into the same file, bench-gated via `TestMain`'s `flag.Lookup("test.bench")` check. |
| RPL-3f — Stage O.6 (`init()` reflection guard) | `a990173` | `init()` at `diff.go:36` panics if `countFairValueFields() = 36` disagrees with reflected count. Panic scope confined to replay binary per R3a Stage O.13's `cmd/server/import_boundary_test.go`. VERIFIER's panic-injection smoke confirmed the guard works (added junk field → panic with exact field-count breakdown → reverted cleanly). |

### Section B — 8 LOW NITs (Go-style modernization sweep)

All 8 landed across the planned R3b-Final commit + 2 iterative-cleanup sweeps that closed gopls's iterative-diagnostic surfacing pattern (the cleanup commit's narrowly-scoped sweep missed instances in R3b's own new code; subsequent sweeps closed those).

| Item | Commit | Location after fix |
|---|---|---|
| RPL-3g — `i, b := i, b` shadow at `cmd/replay/main.go:430` | `257ff5c` | Removed (Go 1.22+ per-iteration semantics make it dead code) |
| RPL-3h — `for i := 0; i < 16; i++` at `module.go:262` | `257ff5c` | Now `for range 16` |
| RPL-3i — `rangeint` + `forvar` at `spike_parallel_fxapp_test.go` | `257ff5c` | Now `for i := range numWorkers` |
| RPL-3j — `strings.HasSuffix + strings.TrimSuffix` at `duration.go:58` | `257ff5c` | Now `strings.CutSuffix` |
| RPL-3k — Drop `--diff-stages` deferred-rationale comment at `main.go:128-132` | `905b295` (Stage K commit) | Removed when `--diff-stages` re-registered |
| RPL-3l — `_ = marketGateway` clarity comment at `module.go:367-374` | `257ff5c` | Comment added explaining transitive consumption |
| RPL-3m — `Summary.DurationMs` doc-comment clarity | `257ff5c` | Clarified that under `--workers > 1` it exceeds `ReplayDurationMs` |
| RPL-3n — `--float-rel-tol=0` silent-default footgun note | `257ff5c` | Usage block at `main.go:82` notes "0 means use default, NOT exact-match" |
| RPL-3p — `mapsloop` + `interface{}→any` at `integration_test.go` | `257ff5c` (initial sweep) + `a5f08f3` + `b7a9bdc` (gopls iterative tail in `replay_test.go`) | All instances across the package use `maps.Copy` and `any` |

### Section C — 1 missing test

| Item | Commit | Notes |
|---|---|---|
| RPL-3o — `evaluateBundleWithRecover` panic-coverage test | `257ff5c` | Test in `cmd/replay/main_test.go` exercises recover via 1-LoC `evaluateBundleFn` package-var seam (restored via `t.Cleanup`). Plan §3 default (build-tag-gated seam) deviated to package-var for simpler discoverability. Acceptable per Decision R3b-Final.9. |

### Section D — 1 R2 modernization sweep

Folded into the same `257ff5c` cleanup commit per RPL-3p mapping above.

---

## Resolution: post-impl + V/R/Q polish coverage

Three more commits landed in the worktree before merge for items NOT in this RPL-3 backlog but discovered during the R3b cycle:

| Commit | Origin | What it addressed |
|---|---|---|
| `7c4676f` | Plan §10 outcome | Populated R3b implementation plan §10 Implementation Outcome table |
| `a5f08f3` | gopls iterative diagnostics post-stage commits | NIT sweep on R3b's new code (Stage N's bench file + Stage O.6's init guard introduced fresh instances of the same patterns RPL-3p targeted) |
| `b7a9bdc` | gopls iterative tail | Final NIT sweep with explicit cutoff line ("after this commit, further gopls findings become a Phase 2.E followup") |
| `573e517` | V/R/Q polish | REVIEWER #2-#4 (thread-safety doc on `evaluateBundleFn`; empty-marker doc on `Result.StageDiffs`; `StageDiff.Empty()` helper) + QA M3/D1/B2 (`.gitattributes` LF-pin; `--from=raw` error hint; empty-dir warn+exit-2) |

**File-level status: RESOLVED.** Subsequent follow-ups tracked at `docs/reviewer/RPL4-r3b-followups.md`.
