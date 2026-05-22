# RPL-5 — Replay exit-code asymmetry between text and JSON modes

**Status:** RESOLVED 2026-05-22 — see "Closeout" section at the bottom of this file. (Originally filed 2026-05-11 by QA gate of the R3b UX-polish dispatch (merge `6efef62`).)
**Severity:** MINOR (potential CI-script blind spot — could silently mask FAIL bundles when scripts use text mode).
**Origin:** QA discovered while smoke-testing Fix 3 (Windows path normalization). The same FAIL'd bundle produces different exit codes depending on `--format`.

## Symptom

```powershell
# Same bundle. Same code. Different exit codes.

./replay.exe --from=parsed --allow-schema-drift <bundle-that-fails>
# Reports FAIL in the per-row output → EXIT=0

./replay.exe --format=json --from=parsed --allow-schema-drift <bundle-that-fails>
# Reports FAIL in JSON output → EXIT=1
```

Reproduced on bundle `req_b370d086-b3af-48e5-b892-bb9bcda09dc1` during the 2026-05-11 QA cycle.

## Why this matters

The replay binary is designed to be CI-script-friendly: per `docs/API_DOCUMENTATION.md` §10.7, exit codes are:

| Code | Meaning |
|------|---------|
| 0 | Every bundle's response matched its saved `17-response.json` (within tolerance). |
| 1 | At least one bundle differed outside tolerance. |
| 2 | Infrastructure failure (missing files, schema-version mismatch, invalid flags, etc.). |

A CI script that runs `replay --format=text` against a watchlist and checks `$?` for non-zero will currently **see EXIT=0 even when bundles failed** — silently masking regressions. That's the opposite of what an operator running replay-in-CI expects.

## Root cause hypothesis (not yet confirmed)

The exit code derives from `Report.ExitCode()` somewhere in `cmd/replay/main.go`. The asymmetry suggests either:
- Text mode's rendering function has a side effect that resets the report's outcome flags before the exit-code computation reads them, OR
- The exit-code computation runs at different points in the two output paths (text-mode early-exit on render, JSON-mode at the end), OR
- A bug in `ComputeSummary` that depends on iteration order through the results slice (only one mode actually visits the FAIL'd entry).

REVIEWER suggested this is pre-existing R3a behavior, not introduced by R3b or the 2026-05-11 UX dispatch.

## Recommended fix direction

1. Read `cmd/replay/main.go` around `report.ExitCode()` and `Report.ComputeSummary` to find where the two modes diverge.
2. Centralize exit-code computation so it runs ONCE, after both rendering paths finish, reading from the same summary.
3. Add a test pinning the contract: same bundle, same flags except `--format`, must produce the same exit code.

## Acceptance criteria

- [x] Reproduce: write a test that runs the binary in both modes against a known-FAIL bundle and asserts the exit codes match. (`TestRun_ExitCode_IsFormatIndependent` + mixed/pass-fail/quiet variants in `cmd/replay/main_test.go`.)
- [x] Fix: centralize the exit-code computation so format choice doesn't change it. (Exit code now captured into a local at line ~450 of `cmd/replay/main.go`, BEFORE any rendering — structurally precluding any render-path mutation of `Summary` from affecting exit code.)
- [x] Document the contract in §10.7 of `docs/API_DOCUMENTATION.md` (exit codes are format-independent).
- [x] Add an integration-level test to `cmd/replay/main_test.go` that pins the format-independence.

## Estimated effort

~30-60 minutes including the test. Single focused commit.

## Traceability

- **Filed by:** QA gate of merge `6efef62` (R3b UX-polish dispatch)
- **Discovered while validating:** Fix 3 (Windows path normalization / RPL-4b)
- **Phase:** 2.E candidate — not blocking any current work
- **Related to:** Pre-existing R3a behavior (not introduced by R3b or the 2026-05-11 UX dispatch)
- **Resolved by:** branch `fix/rpl-5-replay-exit-code-symmetry` (2026-05-22)

## Closeout (2026-05-22)

Investigation finding: the original 2026-05-11 symptom (`text=0` / `json=1` on the same FAIL'd bundle) does NOT reproduce against current master. A reproduction harness using the existing `evaluateBundleFn` test seam to inject deterministic Pass / Fail / Errored outcomes shows BOTH modes returning the spec-§7 exit code (0 / 1 / 2 respectively). All three of the filer's root-cause hypotheses are inconsistent with the actual code path:

1. **"Text rendering has a side effect that resets outcome flags"** — `RenderText` (in `internal/observability/replay/output.go`) reads from `r.Summary` but never mutates it; the only state it sorts is `r.Results`, which doesn't feed back into `Summary` after `ComputeSummary` runs.
2. **"Exit-code computation runs at different points in the two output paths"** — both paths converge on a single `return report.ExitCode()` at the end of `Run()` in `cmd/replay/main.go`. The format dispatch happens entirely inside `renderReport` and never branches the exit-code call.
3. **"`ComputeSummary` bug with iteration-order dependency"** — `ComputeSummary` runs ONCE before any rendering (line 446 of `cmd/replay/main.go`); it cannot vary between two subsequent renders of the same `Report`.

The original observation may have been (a) misdiagnosed during the 2026-05-11 QA cycle (e.g., the two replays were against different bundles or with different flag combinations), or (b) fixed implicitly by one of the intermediate cleanup commits (`257ff5c` RPL-3 cleanup sweep, `573e517` R3b polish, `2d623c9` UX fixes). Either way, the current code's `Run()` function is structurally symmetric between formats and the reproduction test confirms it.

Despite the bug being non-reproducible, the acceptance criteria still call for a centralized exit-code computation + pinned contract. The fix:

1. **Defensive centralization**: capture `report.ExitCode()` into a local `exitCode` variable IMMEDIATELY after `report.Summary` is computed, BEFORE rendering — so even if a future refactor were to mutate `Summary` from inside `RenderText` / `RenderJSON` (a class of regression that's hard to spot in code review), the captured value remains correct. The closing `return exitCode` is now load-bearing for the contract.
2. **Test pin**: `TestRun_ExitCode_IsFormatIndependent` (table-driven over Pass / Fail / Errored), `TestRun_ExitCode_MixedResults_FormatIndependent` (3-bundle tree with all three outcomes), `TestRun_ExitCode_PassFailOnly_FormatIndependent` (2-bundle PASS+FAIL — the CI-watchlist scenario), and `TestRun_ExitCode_IsFormatIndependent_Quiet` (same contract under `--quiet`, which takes the shallow-copy render branch). Together these enumerate the realistic CI-script surface where the asymmetry would matter most.
3. **Doc contract**: §10.7 of `docs/API_DOCUMENTATION.md` now states explicitly: *"Exit codes are format-independent."* CI script authors can rely on this.

Files touched:
- `cmd/replay/main.go` — defensive pre-render `exitCode` capture + load-bearing comments.
- `cmd/replay/main_test.go` — 4 new tests pinning the format-independence contract.
- `docs/API_DOCUMENTATION.md` §10.7 — exit-code contract clause.
- `docs/reviewer/RPL5-replay-exit-code-asymmetry.md` — this closeout note.

This tracker is RESOLVED. Future moves to `docs/reviewer/archive/` are permitted at the next archive sweep.
