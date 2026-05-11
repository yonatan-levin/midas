# RPL-5 — Replay exit-code asymmetry between text and JSON modes

**Status:** OPEN — filed 2026-05-11 by QA gate of the R3b UX-polish dispatch (merge `6efef62`).
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

- [ ] Reproduce: write a test that runs the binary in both modes against a known-FAIL bundle and asserts the exit codes match.
- [ ] Fix: centralize the exit-code computation so format choice doesn't change it.
- [ ] Document the contract in §10.7 of `docs/API_DOCUMENTATION.md` (exit codes are format-independent).
- [ ] Add an integration-level test to `cmd/replay/main_test.go` that pins the format-independence.

## Estimated effort

~30-60 minutes including the test. Single focused commit.

## Traceability

- **Filed by:** QA gate of merge `6efef62` (R3b UX-polish dispatch)
- **Discovered while validating:** Fix 3 (Windows path normalization / RPL-4b)
- **Phase:** 2.E candidate — not blocking any current work
- **Related to:** Pre-existing R3a behavior (not introduced by R3b or the 2026-05-11 UX dispatch)
