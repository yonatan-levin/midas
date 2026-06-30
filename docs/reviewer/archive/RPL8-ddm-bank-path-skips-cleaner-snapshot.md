# RPL-8 — Cleaner snapshot skipped on early-return paths → bundle missing FinancialData schema-stamp

**Status:** RESOLVED — fixed 2026-06-29 on `fix/rpl8-ddm-cleaner-snapshot`. GitHub issue: #25.
**Severity:** MEDIUM — replay/observability fidelity only; zero valuation-math impact. A working
`--allow-schema-drift` workaround existed.
**Origin:** Filed 2026-05-14 (QA cycle 1 of the replay-fidelity debug). Re-triaged + fixed 2026-06-29.

## Root cause — CORRECTED (the originally-filed cause was WRONG)

**Original (incorrect) hypothesis:** "The DDM code path bypasses the cleaner entirely."
This is FALSE under current code. `CleanFinancialDataWithViews`/`CleanFinancialData` runs
UNCONDITIONALLY in `internal/services/valuation/service.go` (~:590) BEFORE model routing — DDM vs
DCF vs FFO selection happens INSIDE `performValuation` (called afterward at ~:610). There is no
model-specific bypass of the cleaner.

**Actual root cause (QA-confirmed 2026-06-29):** `CleanFinancialData`
(`internal/services/datacleaner/service.go`) writes the INPUT snapshot `10-clean-input.json` early
(~:170), then `ValidateData` rejects `Revenue <= 0` (~:473-475) and EARLY-RETURNS (~:175) — AFTER
the input snapshot but BEFORE the OUTPUT snapshot block (~:338-351) that writes
`10-clean-output.json` + `10-clean-trace.json` + `AddSchemaVersion("FinancialData", 10)`.

**Banks/insurers legitimately carry `Revenue = 0`** in this pipeline (confirmed: the JPM DDM golden
input carries `"revenue": 0.0` across all periods), so every revenue-zero filer hit this early-return
and produced a bundle missing the output snapshot + the schema stamp → replay needed
`--allow-schema-drift`. The DDM correlation was a RED HERRING: banks route to DDM *and* have Revenue=0;
the trigger is the revenue-zero validation gate, not the model.

Two MORE early-returns had the same asymmetry (also fixed): the `applyActiveAdjustments`
partial-result return (~:252-256), and — found in code review — the **cache-HIT** early return
(`enable_caching` defaults true, so a warm-cache traced request reproduced the exact drift).

## Fix (3 commits on `fix/rpl8-ddm-cleaner-snapshot`)

- `5b3e0a1` — extract `snapshotCleanOutput(ctx, result)` helper (verbatim lift of the old inline
  happy-path block; nil-bundle + nil-result guarded) and call it on the ValidateData-fail and
  partial-result early-returns. Happy path stays byte-identical (single call, same payload,
  `FinancialData` still version 10). Regression test
  `TestCleanFinancialData_SnapshotSymmetry_RevenueZeroTicker_RPL8` (bank Revenue=0 + control).
- `5276272` — route the cache-HIT early return through the same helper; remove the now-redundant
  standalone `recordQualityFlagCount` (RecordQualityFlagCount ADDS — not idempotent — so keeping both
  would double-count and could spuriously trip the auto-on-quality-flag trigger).
- `378dc9c` — regression test `TestCleanFinancialData_SnapshotSymmetry_CacheHit_RPL8` (teeth-proven:
  fails when the cache-hit snapshot call is neutralized, with the exact missing-`10-clean-output.json`
  error).

**Deliberately NOT snapshotted:** the nil-data and ctx-cancel returns (no meaningful payload / bundle
being torn down). **Deferred (non-blocking):** narrate-phase emission on the validation-fail path —
replay's stage-diff consumes `10-clean-output.json` + the schema stamp, NOT a narrate artifact, so
`99-narrate.jsonl` coherence on that path is cosmetic. **NOT touched:** `ValidateData` itself, any
cleaner/adjuster math (preserves `TestDDM_LegacyPath_BitForBit` + recompute-shadow byte-identity).

## Verification evidence

- Reviewed flow: QA triage (REPRODUCED, root cause corrected) → BACKEND fix → VERIFIER (VERIFIED, teeth
  proven by neutralize-and-revert) → REVIEWER (APPROVE_WITH_NITS — found+fixed the cache-HIT gap) →
  cache-HIT regression test (teeth proven) → QA live re-verify → final combined+live verify.
- Regression tests: 3 tests / 4 subtests PASS; BOTH new tests teeth-proven (fail pre-fix, pass post-fix).
- Load-bearing invariants GREEN: `TestDDM_LegacyPath_BitForBit` (jpm/bac/wfc), `TestRecomputeUmbrellas_NoMutation`,
  recompute-shadow fixtures byte-identical. `go build ./...` exit 0; all 7 datacleaner + all 5
  observability packages `ok`. Happy-path bundle confirmed byte-identical (verbatim helper lift, single emit).
- **LIVE TEST (2026-06-29, fresh capture):** `GET /api/v1/fair-value/JPM?trace=1` → HTTP 200 (DDM bank,
  `FIN_BANK`, profile `mature_large_bank:mature`). The JPM bundle contained `10-clean-output.json` +
  `10-clean-trace.json`, and `00-manifest.json` schema_versions carried `FinancialData:10`. Pre-fix this
  bundle was MISSING all three. Control AAPL bundle unchanged (always had them).
- **Decisive replay check:** `cmd/replay --from=parsed <fresh JPM>` no longer REFUSES — pre-fix it exited 2
  with `schema drift detected (use --allow-schema-drift to proceed)`; post-fix it runs with only the benign
  `as_of` clock-binding field differing (`fields=1/70`), exactly the "succeeds with fields_changed: 1
  (as_of only)" outcome this tracker's acceptance criteria anticipated.
  - NOTE: the no-flag replay ALSO required **RPL-11 (#28)** — the live test surfaced that
    `replay/schema.go::CurrentSchemaVersions` had never registered the `AssumptionProfileManifest` +
    `GuidanceResolution` entities the manifest now stamps, which independently forced
    `--allow-schema-drift` on EVERY fresh bundle (bank or not). Fixed in the same branch (commit
    `445bd3a`). Without RPL-11 the RPL-8 `FinancialData` fix alone would still have been masked by that
    unrelated drift.

## Acceptance criteria — ALL MET

- [x] Capture a bank (JPM) bundle → contains `10-clean-output.json` and `10-clean-trace.json`. (live-confirmed)
- [x] Same bundle's manifest `schema_versions` contains `FinancialData` (value 10). (live-confirmed)
- [x] Replay JPM `--from=parsed` WITHOUT `--allow-schema-drift` → succeeds with `fields_changed: 1`
      (as_of only). (live-confirmed; required RPL-11 #28 for the two unrelated schema entities)
- [x] Regression tests added (ValidateData-fail path + cache-HIT path; both teeth-proven).

## Traceability

- GitHub issue: #25
- Branch: `fix/rpl8-ddm-cleaner-snapshot` (commits `5b3e0a1`, `5276272`, `378dc9c`)
- Original filing: 2026-05-14 (QA cycle 1, replay-fidelity debug)
- Spun off during this fix: **RPL-11 (#28)** — `docs/reviewer/archive/RPL11-replay-schema-registry-missing-entities.md`
  (the replay-registry staleness the live test exposed; same branch, commit `445bd3a`).
- Related: #22 / BUG-016 (tier2-baseline fixtures never committed — why the OLD JPM baseline can't be
  replayed locally; the live-capture acceptance check used a FRESH bundle instead).
- File:line references (current code):
  - `internal/services/datacleaner/service.go` — `snapshotCleanOutput` helper + 4 call sites
  - `internal/services/datacleaner/service_snapshot_symmetry_test.go` — both regression tests
