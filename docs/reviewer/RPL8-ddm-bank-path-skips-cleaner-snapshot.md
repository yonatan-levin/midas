# RPL-8 — DDM/bank code path skips cleaner snapshot → bundle missing FinancialData schema-stamp

**Status:** OPEN — filed 2026-05-14 by QA cycle 1 of the replay-fidelity debug.
**Severity:** MAJOR — replaying any DDM-routed ticker (banks, insurance) requires `--allow-schema-drift` flag because the manifest is incomplete.
**Origin:** Discovered during debug Phase 3c QA re-verify (cycle 1) while running JPM as a sector-diversity smoke.

## Symptom

```powershell
./replay --from=parsed artifacts/2026-05-14/JPM/req_*/
# ERROR: schema drift detected (use --allow-schema-drift to proceed)
# - schema:FinancialData  bundle=0 current=7 (not stamped in bundle)
```

JPM (and any FIN_BANK ticker routed to DDM) replays cleanly with `--allow-schema-drift`, but the schema-drift flag should NOT be required for a same-SHA replay of a freshly-captured bundle. The flag exists for genuine schema changes, not missing-from-bundle metadata.

## Root cause

The DDM code path in `internal/services/valuation/service.go` doesn't run through the datacleaner snapshot phase. Specifically:
- `internal/services/datacleaner/service.go:285` calls `AddSchemaVersion("FinancialData", 7)` after the cleaning phase
- The DDM path bypasses the cleaner entirely
- Consequence: JPM bundles are missing `10-clean-output.json`, `10-clean-trace.json`, AND the `FinancialData: 7` entry in `00-manifest.json: schema_versions`

This is the **same architectural pattern** as Gap-3 (alt-model path missing `15-valuation.json` snapshot) that was closed in commit `4290266` for the revenue_multiple model. The DDM path has the same shape of asymmetry for a different stage.

## Sample evidence

```bash
$ ls artifacts/2026-05-14/JPM/req_51de2078-fae5-4875-af72-167375e1220d/
# Missing: 10-clean-output.json, 10-clean-trace.json
# Present: 17-response.json, 13-wacc.json, etc.

$ cat artifacts/2026-05-14/JPM/req_*/00-manifest.json | jq '.schema_versions | keys'
# Missing: FinancialData
```

JPM's `99-narrate.jsonl` has no `phase: "clean.normalized"` entry while AAPL/MXL bundles both do.

## Fix

Two approaches, both architectural:

1. **Route DDM through the cleaner snapshot phase** — ensure ALL valuation paths emit `10-clean-output.json` + `AddSchemaVersion("FinancialData", 7)` post-cleaner, regardless of model. This is the same pattern as the Gap-3 fix at `service.go:1352-1361` (alt-model `Snapshot` + `AddSchemaVersion`).

2. **Make snapshot stamping idempotent across paths** — wire the manifest's `schema_versions` map to be populated by a single source-of-truth phase (e.g., manifest finalization) rather than per-path stamping. Lower-risk for future bugs of this class.

Recommended: (1) for the immediate fix because it preserves the existing pattern; (2) for future as part of RPL-9's manifest-config-snapshot work.

## Acceptance criteria

- [ ] Capture a JPM (or BAC, WFC) bundle → bundle contains `10-clean-output.json` and `10-clean-trace.json`.
- [ ] Same bundle's manifest `schema_versions` contains `FinancialData` with value 7.
- [ ] Replay JPM `--from=parsed` WITHOUT `--allow-schema-drift` flag → succeeds with `fields_changed: 1` (as_of only).
- [ ] Regression test: capture a known-DDM ticker fixture in test fixtures, assert `10-clean-output.json` is present.

## Traceability

- Discovered by: QA cycle 1 of the replay-fidelity debug (2026-05-14)
- Related fix (Gap-3, same pattern): commit `4290266`, `internal/services/valuation/service.go:1352-1361`
- File:line references:
  - `internal/services/valuation/service.go` (DDM path — search for `performDDMValuation` or model router branches)
  - `internal/services/datacleaner/service.go:285` (snapshot site that DDM path bypasses)
