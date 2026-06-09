# TODO Burn-Down — Part 2 Closeout

**Date:** 2026-06-07/08 · **Merged to master** as `18f4ec6` (TDB-7) → `21fb60f` (TDB-1) → `b82035c` (TDB-2), on top of `11a6b76`.

Three prioritized backlog items shipped end-to-end (design → implement → VERIFIER/REVIEWER/QA → live API), merged to `master`, with the full Go suite green and every load-bearing invariant preserved.

## What shipped

| Issue | Title | Merge commit | Net change | Key result |
|------|-------|--------------|-----------|------------|
| **#7 / TDB-7** | Delete dead code (applyRule chain, getCompanySize, IntegrationService) | `18f4ec6` | +21 / −1,462 | Zero behavior change; 47/47 suite green |
| **#1 / TDB-1** | SEC parser populates restructuring/litigation/capitalized-interest → C1/C3/C6 fire | `21fb60f` | +0 / parser+test (~240 lines) | Live: JNJ parses `restructuring_charges`=32M (was 0); SEC confirms 745M / −379M |
| **#2 / TDB-2** | Implement A6 (ROU) + A7 (excess-cash) adjusters (were `enabled:true` but silently skipped) | `b82035c` | +~600 src / +spec+plan | Live: WMT A6 fired $15.22B ROU exclusion; A7 correctly skips; per-share bit-for-bit |

**Validation (merged master):** `go build` + `go vet` + `go test ./... -count=1` = **48/48 packages ok, 0 FAIL**. Load-bearing invariants byte-identical: `TestDDM_LegacyPath_BitForBit`, recompute-shadow snapshots (`git diff --quiet` exit 0), `TestLedger_BasketSnapshot_*`. `dcf_value_per_share` bit-for-bit unchanged across all three (TDB-7/TDB-2 are behavior-preserving; TDB-1 only moves valuations for filers that report the new fields — the intended correction).

**Versions:** `CalculationVersion` stays **4.6** (no per-share change). `SchemaVersion["FinancialData"]` **9 → 10** (TDB-2's new `OperatingLeaseRightOfUseAsset` field).

## Design + tracker docs (on master)
- TDB-1: `docs/refactoring/spec/tdb-1-parser-nonrecurring-extraction-spec.md` + `…/implementations/…-implementation-plan.md`; tracker `docs/reviewer/archive/TDB-1-parser-restructuring-litigation-capex-not-populated.md`.
- TDB-2: `docs/refactoring/spec/tdb-2-a6-a7-asset-adjusters-spec.md` + `…/implementations/…-implementation-plan.md`; tracker `docs/reviewer/archive/TDB-2-missing-a6-rou-a7-excess-cash-adjusters.md`.
- TDB-7: tracker `docs/reviewer/archive/TDB-7-dead-code-cleanup-applyrule-getcompanysize.md`.
- Catalog rows R1 (TDB-1) + R3 (TDB-7) stamped DONE in `docs/integration/TODO_TASKS_CATALOG.md`.

## Open follow-ups (logged)

| Follow-up | Where tracked | Priority | Notes |
|-----------|---------------|----------|-------|
| **Expose `cleaning_adjustments` on the fair-value API** | **GitHub #11** (new) | P3 | Pre-existing: the cleaner audit trail (`ValuationResult.CleaningAdjustments`) populated by A1/A6/A7/B-rules is NOT mapped into `FairValueResponse`. Affects all adjusters, not just A6/A7. Discovered in TDB-2 review. |
| **TDB-1 operator live-replay verification** | TDB-1 tracker | — | Confirm the fair-value shift on a high-restructuring filer. The `artifacts/tier2-baseline/` is CalcVersion 4.1, drift-confounded — needs a fresh capture. Non-blocking. |
| **TDB-2 REVIEWER NITs** | TDB-2 tracker / spec | — | (a) optional `TotalAssets<=0` guard in A6 (mirrors A1's existing gap; cosmetic +Inf only); (b) broaden the A6/A7 DDM-invariance test to BAC/WFC. Non-blocking. |
| **Stale CLAUDE.md note** | this doc | — | CLAUDE.md cites AMD `Restated.TotalLiabilities`=$9,679M; the live fixture reconstructs $9,286M. Pre-existing across master; flagged by TDB-2 VERIFIER. Doc-only fix. |

## Next backlog (per the original handoff order)
- **#3 / TDB-3** (P1) — replace contingent-liability probability heuristic with AI footnote analysis (reuse B3 AI infra).
- **#8 / TDB-8** (P3) — inventory turnover for obsolescence detection (small).
- Plus the API-exposure follow-up **#11** (natural complement to TDB-1/TDB-2 — makes the audit trail consumer-visible).
