# RPL-1 — Replay walk-package and output-stream cleanups (R3 follow-ups)

**Status:** OPEN — filed 2026-05-03 from REVIEWER cycle 2 + QA cycle 2 on R0+R1 of the replay-tooling refactor.
**Severity:** Low (advisory — none blocked the R0+R1 merge).
**Origin:** REVIEWER + QA cycle-2 verdicts on commits `60c8572`..`6ea65b9` of `worktree-agent-a33b08d36c0f0ef58` (R0+R1 fix-cycle), filed against R3's planned scope (parallel walk + full §7 flag set).

## Context

R0 (Clock injection) + R1 (replay skeleton) of the Phase 2.D replay tooling shipped on master via fast-forward of an 8-commit worktree branch. Three items surfaced during the second-cycle review gates were explicitly framed as advisory and non-blocking — they are properly future-phase work because R3 is where the affected surfaces (the walk function and the output stream) get touched anyway. Filed here as a single grouped item so R3's BACKEND dispatch can fold them in.

The replay tooling spec lives at `docs/refactoring/observability-replay-tooling-spec.md`. The cycle-2 review reports are summarized in the orchestration session that produced the merge.

---

## RPL-1a — `walkOnce`'s `dirInfo` parameter is unused

**Source:** REVIEWER cycle 2.
**Location:** `internal/observability/replay/walk.go:77, 167` (the `walkOnce` function signature and root call).

The `dirInfo` parameter is reserved for "future extensions" and currently used only to satisfy the type signature (`_ = dirInfo`). Standard Go idiom prefers removing unused parameters and re-adding them at the point of need. Two acceptable resolutions:

- **(a)** Drop the parameter and the corresponding root-call argument. The type signature is internal to the package; reintroducing it later is cheap.
- **(b)** Keep the parameter and replace `_ = dirInfo` with an explicit comment naming the intended future use (e.g., a stat cache or per-directory reporter).

Recommendation: **(a)**. YAGNI on the future-extension scaffolding; R3's parallel walking will introduce its own per-goroutine state which is the actual future requirement.

---

## RPL-1b — `visited` slice thread-safety not documented

**Source:** REVIEWER cycle 2.
**Location:** `internal/observability/replay/walk.go:25-30` (function-level doc comment) and `:111-117, :129, :160` (the `visited` slice mutation sites).

The current `walkOnce` recursion is purely depth-first and single-threaded by construction; `visited` is appended to without any locking. This is correct for R1, where there is no parallelism. R3 introduces the `--workers` flag and the `walkOnce`/visited-set pair becomes a data race the moment two goroutines share it.

The current doc comment at lines 25-30 says the cycle-detection approach is "portable across Linux/macOS/Windows" but does not flag the single-threaded invariant. Add a one-line note so a future R3 implementer doesn't trip over it:

```go
// visited is single-threaded by construction; a future parallel walker must
// guard it with sync.Mutex or use per-goroutine snapshots. Sharing the slice
// across goroutines without synchronisation is a data race.
```

Acceptance: comment landed, AND the R3 parallel-walk dispatch sees this note before designing its concurrency model.

---

## RPL-1c — Text-mode schema-drift detail duplicated on stdout and stderr

**Source:** QA cycle 2 (informational nuance, NOT a defect against the current spec).
**Location:** `cmd/replay/main.go:204-206` (stderr path via `writeSchemaDriftDiagnostic` at `:221-238`) and `internal/observability/replay/output.go:250-269` (stdout path via the per-row `writeResultRow` drift detail).

When `--format=text` is in play and schema drift is detected without `--allow-schema-drift`, the `  - schema:<entity> bundle=N current=M` lines appear on both:

- **stdout**, via `writeResultRow`'s per-row drift detail (intentional — preserves actionable detail when stdout is the human-facing stream)
- **stderr**, via `writeSchemaDriftDiagnostic` (required by spec D5: "print the mismatched table to stderr")

The spec (D5 + §7 sample output at L505-510) is silent on whether the inline stdout representation must be suppressed. BACKEND's choice was deliberately operator-friendly: a user who runs `replay --out=foo.json` redirects the structured output to a file but still wants the actionable drift detail visible on the terminal via stderr; conversely, a user inspecting raw text output sees it inline. Both audiences win.

This is currently fine. It becomes an action item only if a future spec revision calls for single-stream emission. The two seams to touch in that case are documented above.

Recommendation: leave behavior as-is. If R3 (or a later revision) revisits the §7 contract for `--quiet` / piping ergonomics, this is a natural place to either add a `--strict-stderr-drift` flag or amend the spec to formalise the current dual-stream behavior.

---

## Why deferred to R3

R3 is scoped to introduce parallel batch execution (`--workers`), the full `--filter-ticker` / `--filter-since` / `--diff-stages` / `--verbose` flags, and the JSON-shape lock-in golden tests. All three sub-items above sit in surfaces R3 will be touching:

- **RPL-1a / RPL-1b**: R3's parallel walker is the consumer of the walk-function future-extensions surface and the data-race-relevant `visited` set.
- **RPL-1c**: R3's `--verbose` / `--quiet` / output-format polish is the natural place to revisit dual-stream emission semantics.

Bundling them into the R3 dispatch keeps the patch surface unified rather than producing a third tiny commit on the R0+R1 merge that would touch the same files R3 will touch anyway.

## Acceptance criteria

- [ ] RPL-1a: `dirInfo` parameter resolved (drop or document) before R3 introduces parallel walking, OR explicitly carry forward as a non-blocker if R3's design supersedes the question.
- [ ] RPL-1b: thread-safety comment added to `walk.go` either as part of the current single-threaded code OR as an explicit invariant statement that R3's parallel walker has to break/replace.
- [ ] RPL-1c: re-evaluated when R3 finalises `--verbose`/`--quiet` semantics; either codified as the spec's intended dual-stream behavior or refactored to single-stream with the `--strict-stderr-drift` opt-out.

## Traceability

- Filed by: REVIEWER + QA cycle 2 on R0+R1 worktree branch (2026-05-03)
- Specs it relates to: `docs/refactoring/observability-replay-tooling-spec.md` §5 D5/D9, §7, §9 R3
- Code it relates to: `internal/observability/replay/walk.go`, `internal/observability/replay/output.go`, `cmd/replay/main.go`
- R0+R1 commits the items were observed against: `430f829`, `4f4bb82`, `60c8572`, `15e5876`, `6efc49f`, `fb13087`, `dc6afbf`, `6ea65b9`
