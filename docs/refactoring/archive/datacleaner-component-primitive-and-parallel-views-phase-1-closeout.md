# DC-1 Phase 1 — Closeout Report

**Phase:** Phase 1 — `recomputeUmbrellas` shadow-mode observer (cleaner-pipeline instrumentation layer)
**Status:** ✅ SHIPPED 2026-05-19
**Master HEAD at close:** `2d916a7` (`merge: dc1-phase-1 — DC-1 Phase 1 (6 commits)`)
**Branch landed:** `dc1-phase-1` (now deleted post-merge); follow-up polish lives on `dc1-phase-1-followup`
**Commits on master:** 6 implementation commits + 1 merge commit (Phase 1 main PR); +N follow-up polish commits (this PR)
**Discovery path:**
- Tracker: [docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md](../../reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md)
- Spec: [datacleaner-component-primitive-and-parallel-views-spec.md](../spec/datacleaner-component-primitive-and-parallel-views-spec.md)
- Phase 0 closeout: [datacleaner-component-primitive-and-parallel-views-phase-0-closeout.md](datacleaner-component-primitive-and-parallel-views-phase-0-closeout.md)
- Phase 1 implementer handoff: [datacleaner-component-primitive-and-parallel-views-phase-1-handoff.md](datacleaner-component-primitive-and-parallel-views-phase-1-handoff.md)
- Phase 1 implementation plan: [datacleaner-component-primitive-and-parallel-views-phase-1-implementation-plan.md](datacleaner-component-primitive-and-parallel-views-phase-1-implementation-plan.md)
- Phase 1 shadow-analysis report: [datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md](datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md)

---

## What landed

Single worktree (`dc1-phase-1`), sequential B-V-R-Q execution per the project's ABVRQ pattern. The plan was authored before BACKEND dispatch, the V-R-Q chain ran on the BACKEND output, HUMAN approved the merge, and a single follow-up PR (this report's PR) addresses post-merge REVIEWER findings + the deferred shadow-analysis deliverable.

### Worktree A — Phase 1 main PR (Tasks 1.1 → 1.6)

Merge `2d916a7`. 6 implementation commits on master.

| Commit | Scope |
|---|---|
| `ce4794f` | `internal/services/datacleaner/recompute.go` (139 LOC) + `recompute_test.go` (392 LOC). Pure read-and-log shim with 5 unit tests covering: no-mutation invariant (`reflect.DeepEqual` snapshot), nil-fd safety, nil-ctx safety (via `logctx.From(nil)` nop fallback), divergence-emission shape, clamp-suspected flag fingerprint, plus the load-bearing gopter property test (4 properties × 200 iterations, pinned seed `20260517`). |
| `8a81e73` | Wired `recomputeUmbrellas(ctx, result.CleanedData)` into `internal/services/datacleaner/service.go::CleanFinancialData` at the canonical insertion point (between `createRiskWarningFlags` and `calculateQualityScore`). +13 LOC including a multi-line comment block citing the spec + plan. |
| `2dae93c` | `internal/integration/datacleaner_recompute_shadow_test.go` (313 LOC) + initial per-ticker snapshots committed for AAPL, AMD, EQIX, F, KO, MSFT, MXL (7 / 10 basket members; JNJ / TSM / BABA skip per `t.Skipf` for lack of captured bundles). 1690 lines added across 9 files. |
| `d869d1d` | Determinism fix to the snapshot writer + `divergenceFromEntry` (whole-dollar quantization via `roundDollar`; deterministic period sort; period-key filter for `""` and `"0"`; ticker-stamp safety). Regenerated 4 of 7 snapshots — AMD / EQIX / KO / MXL changed; AAPL / F / MSFT byte-stable. 61 lines net delta on the test file. |
| `98315a5` | Documentation push: CLAUDE.md DC-1 Common Gotchas entry extended; TESTING.md plug-invariant subsection extended with the recompute-side companion pattern; spec changelog row appended; DC-1 reviewer tracker bumped to "Phase 1 SHIPPED 2026-05-19" + Phase 1 progress paragraph filed; THESIS.md DC-1 row status updated. 24 LOC across 5 files. |
| `2d916a7` | Merge commit. |

### Follow-up PR (Tasks A-E in this closeout's PR)

This PR. Five logical changes:

| Change | Scope |
|---|---|
| Shadow-analysis report | New file `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md` (~700 LOC). Satisfies AC #10 from the Phase 1 handoff. Gate verdict: SATISFIED. |
| Closeout report | New file (this document). Satisfies AC #11. |
| REVIEWER MINOR polish | 5 items across `recompute.go`, `datacleaner_recompute_shadow_test.go`, and the spec doc. Doc-truth drift correction (MXL 2017FY / EQIX 2013Q1 historical clamp citations replaced with AMD 2023FY / KO 2023FY actual carriers); PP&E absorption comment; "Branchless" → "Inline absolute-value" comment rephrase; naming-symmetry rename (`nonCurrentAssets` → `recomputedNCA`, `nonCurrentLiab` → `recomputedNCL`); atomic snapshot write via `path.tmp` + `os.Rename`. |
| REVIEWER NIT polish | 4 items: `emitIfDiverged` godoc; CLAUDE.md DC-1 bullet split into Phase 0 + Phase 1; THESIS.md DC-1 row date anchoring; gopter observer allocation hoisted out of property lambda. |
| Cross-reference updates | DC-1 tracker + THESIS.md cross-references to the new shadow-analysis report and this closeout. |

---

## Empirical verification

| Surface | Result |
|---|---|
| `go build ./...` | Clean. Zero warnings across all packages. |
| `go test ./internal/services/datacleaner/... -count=1` | Green. All recompute unit tests pass (no-mutation, nil-safety × 2, emits-warn, clamp-suspected-flag, property × 4). |
| `go test ./internal/integration/... -run TestDataCleanerRecompute -count=1` | Green. 7 / 10 basket tickers pass (AAPL, AMD, EQIX, F, KO, MSFT, MXL); 3 SKIP (JNJ, TSM, BABA — no captured bundles). `passedCount = 7 >= 5` floor satisfied. |
| Snapshot byte-stability | `git status --short internal/integration/testdata/recompute-shadow/` produces zero changes after the polish commits. Determinism preserved. |
| Snapshot diff-review-readiness | All 7 snapshots have deterministic period sort (alphabetical-by-ISO-format `2023FY` < `2023Q2` < ...) and whole-dollar quantization (`roundDollar`). |
| Property test | 4 properties × 200 iterations × 4 umbrellas = effectively 800 randomized invariant assertions per CI run. Pinned seed `20260517`. |
| Shadow data validation against tracker | MXL 2026Q1 recomputed `CurrentAssets = $215,114,400` matches the DC-1 tracker's "Expected after $34.34M inventory writedown" value exactly. Independent cross-check; shim is correct. |
| Replay regression | Phase 1 BACKEND ran `cmd/replay --from=parsed` against AAPL + MSFT pre-merge; financial-stage diffs were timestamp-only (`as_of`, `calculated_at`, `market_data_date`). No regression in the cleaned-output stage. |
| Coverage | New file `recompute.go` covered at >90% by `recompute_test.go` (property test exercises both divergence-firing and non-firing branches across 800 iterations). Integration test exercises the wired-in call site through the full production cleaner pipeline. |

---

## V-R-Q chain summary

| Stage | Verdict | Notes |
|---|---|---|
| **BACKEND** | Output landed cleanly. | Single worktree, sequential execution per the Phase 0 closeout lesson #3. ~3 days of focused work matched the plan's 3-5 day estimate. |
| **VERIFIER** | VERIFIED | Independent functional re-run. Caught controller's incorrect expectation that MXL 2026Q1 would carry `clamp_suspected: true` — the real clamp carriers in the baseline date range are AMD and KO (`reported_TL == 0`). Verifier was right; the snapshots reflect the truth. |
| **REVIEWER** | APPROVE_WITH_NITS | 5 MINOR findings + 4 NIT findings — all addressed in this follow-up PR. Approve decision was conditional on the polish landing within one week post-merge; this PR satisfies that condition. |
| **QA** | PASS | 9 / 9 in-scope acceptance criteria PASSED at merge. 2 deferred (AC #10 shadow-analysis report, AC #11 closeout report) — both now satisfied by this follow-up PR. |
| **HUMAN** | APPROVED + MERGED | Merge commit `2d916a7` on 2026-05-19. |

---

## Lessons learned

### 1. `gopls` cross-worktree workspace-config noise

The follow-up worktree at `midas-dc1-p1-followup/` is a sibling of `midas/` (the master worktree). `gopls` (running against the master `go.work`) reports false "undefined" errors when reading files under the sibling worktree because it doesn't index them. **Workaround:** `go build ./...` and `go test` from inside the worktree work correctly — they read the worktree's own `go.mod` / module path. Treat the editor squiggles as cosmetic; trust the CLI build/test output. Document this in the next worktree handoff brief so future workers don't waste time chasing phantom compile errors.

### 2. Controller spot-check expectations on `clamp_suspected` were wrong; VERIFIER caught it

The plan-authoring controller assumed MXL 2026Q1 would be a clamp-fired period (carryover assumption from the Phase 0 closeout's MXL 2017FY / EQIX 2013Q1 clamp citations). When the basket integration test landed, MXL 2026Q1 actually emitted `clamp_suspected: false` because the parser-side plug for that period is non-zero (plug = $102.5M). The real clamp carriers in the 2026-05-15 baseline date range are AMD and KO — every period reports `TotalLiabilities = 0` with `clamp_suspected: true`. VERIFIER flagged this in pre-merge review; we corrected the assumption rather than the data.

**Generalizable lesson:** when carrying assumptions from a closeout report's "lessons learned" forward into the next phase's plan, **re-validate against current empirics**, not the cited historical examples. The historical clamp cases (MXL 2017FY, EQIX 2013Q1) are documented in the Phase 0 closeout but those periods are OUTSIDE the 2026-05-15 baseline date range — they cannot be re-validated against the current shadow snapshots. The actual clamp-suspected fingerprint in this baseline lives elsewhere. Plans should validate cited examples against the *current* fixture set before quoting them.

### 3. Snapshot whole-dollar quantization is necessary, but compresses sub-dollar signal by 6 OOM

The initial snapshot writer in `2dae93c` emitted raw float values from `observer.LoggedEntry.ContextMap()`. Several periods produced non-deterministic last-bit drift (`387402066.6666667` vs `387402066.6666666` between consecutive runs) because the cleaner's adjuster orderings non-deterministically permute float accumulation. Without quantization, the committed snapshots flapped between runs and defeated the diff-review purpose of committing them. The fix in `d869d1d` introduced `roundDollar` (`math.Round(v)`) to quantize every monetary field to the nearest whole dollar. **Trade-off:** sub-dollar resolution is gone from the committed snapshots. **Sufficient for Phase 2:** the punch list operates on millions of dollars; whole-dollar precision is 6 orders of magnitude tighter than the smallest actionable signal. **Watch-out for Phase 2:** if a future change introduces a sub-dollar-magnitude divergence signal (very unlikely), the quantizer would mask it.

### 4. Pre-existing Tier 2 P0b `assumption_profile` drift on master

Tier 2 P0b (`2e48fde`) introduced `08-assumption-profile.json` to every bundle's artifact set. Replay-against-baseline runs from inside DC-1 Phase 1 saw the assumption-profile stage diff as drift, even though it's unrelated to DC-1's scope. Phase 2 will hit this if it uses `cmd/replay --from=parsed --diff-stages` against bundles captured pre-P0b. **Recommendation for Phase 2 BACKEND brief:** either refresh the baseline (`artifacts/tier2-baseline/<new-date>/`) to one captured post-P0b, or add a carve-out comment to the regression script that ignores `08-assumption-profile.json` in the diff. Document either choice in the Phase 2 plan.

### 5. Doc-truth vs. code-truth drift on clamp examples

The Phase 0 closeout cites MXL 2017FY and EQIX 2013Q1 as clamp-fired periods. The original cleaner-side reference in `recompute.go` (and the spec changelog) inherited those citations from the Phase 0 closeout. After the basket integration test ran, the real clamp carriers in the baseline date range turned out to be AMD 2023FY → 2026Q1 and KO 2023FY → 2026Q1 (every period of both, `reported_TL == 0` parser dropout). The historical MXL / EQIX cases remain documented in Phase 0's closeout — they're real but date-locked to a no-longer-captured baseline. **Fixed in this follow-up's MINOR #1:** doc-truth references updated to cite AMD 2023FY / KO 2023FY as the live examples, with a parenthetical noting the historical MXL / EQIX cases as Phase 0 closeout artifacts.

**Generalizable lesson:** when documenting expected behavior with cited fixtures, anchor the citation against a date-stamped fixture corpus. If the corpus rolls forward, the citations need refresh — or they become a slow-burn confusion source for the next phase's implementer.

### 6. Parallel agent activity stayed clean

Tier 2 P1-P4 worktrees were active concurrently with DC-1 Phase 1 (`internal/services/valuation/` vs. `internal/services/datacleaner/` — zero file-scope overlap). The handoff and plan both called out the no-overlap rule, and the BACKEND brief was explicit about staying out of `internal/services/valuation/`. **No conflicts.** Continue this pattern for Phase 2 (and beyond): when dispatching subagents, name the off-limits package directories explicitly.

### 7. Single-worktree-sequential was right for Phase 1's scope

Phase 0 used 3 parallel worktrees; this added ~30% setup overhead for ~3.5h of substantive work. Phase 1 used 1 worktree, sequential, and matched the plan's 3-5 day estimate cleanly. Phase 1's scope (one new file + one wiring point + one integration test + doc updates) fit comfortably in single-worktree-sequential. **Recommendation for Phase 2:** scope estimate first; if Phase 2's Adjuster interface refactor is > 1 week of work and decomposes into independent sub-modules (asset/liability/earnings adjusters as separate worktrees), reconsider parallel fan-out. Otherwise, stay sequential.

### 8. The plan's "recording-not-asserting" integration policy is paying off

The integration test commits per-ticker snapshots and asserts only `passedCount >= 5` for the floor. It does NOT assert on a specific divergence count for any ticker. The benefit landed immediately:
- The snapshots are diff-reviewable in every Phase 2+ PR. If Phase 2's Adjuster reroute changes the cluster shape (which it must), the diff in the PR review surfaces it without any test edit.
- Adding JNJ / TSM / BABA bundles later will simply add new `<TICKER>.json` files; no test change needed.
- The pre-existing AMD / KO parser-side `TotalLiabilities == 0` bug surfaces transparently in the snapshots (24 records with `clamp_suspected: true`) without a brittle "assert AMD has N divergences" pin.

The downside (someone could merge a snapshot change without reading it) is mitigated by adding a Phase 2 reviewer-checklist item: "Did `internal/integration/testdata/recompute-shadow/<TICKER>.json` change? If so, does the diff match the expected Adjuster-pattern reroute?"

---

## What's deferred to Phase 2+

Per the spec's "Phasing & implementation sequence" — items NOT in Phase 1's scope, blocking which feature:

### Phase 2 — Adjuster interface + AdjustmentLedger (the actual fix)

The `recomputeUmbrellas` shim diagnoses but does not fix the cleaner-side mutation pattern. Phase 2 introduces:
- `internal/services/datacleaner/adjustments/contracts.go` — `Adjuster` interface returning `AdjusterOutput` with `LedgerEntry` slice + `OverlaySpec` slice + `Flags` slice.
- Existing A1/A2/A4/A5/B1/B2/B3 adjusters migrated to the new interface. A1 reclassified as Overlay (Damodaran goodwill exclusion). B1/B2/B3 reclassified as Overlays through `InvestedCapital.DebtLikeClaims` per spec §"AmountSemantics".
- `recomputeUmbrellas` lifted from observation to authority (used to compute umbrellas after Adjuster runs); WARN severity dropped to DEBUG or removed.
- Phase 1 → Phase 2 input data: see [shadow-analysis report](datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md). 7 clusters identified; 4 disposition categories.

### Phase 3 — CleanedFinancialData view system

Three-view output (`AsReported`, `Restated`, `InvestedCapital`) with lazy reconstruction. `internal/core/entities/financial_data.go` gets a new `CleanedFinancialData` type. Adjuster outputs feed `Restated`; OverlaySpecs feed `InvestedCapital`.

### Phase 4 — Consumer migration (13 sites)

Every consumer of `*FinancialData` post-clean reads from the semantically-correct view. NCAV reads `AsReported`; DCF reads `InvestedCapital`; ROIC reads `InvestedCapital`; ROE reads `Restated`. Per the spec's "consumer migration" section, this is a 13-site mechanical refactor with a compile-time `WACCInputs` boundary as the safety net.

### SQLite persistence migration

The Phase 0 flip-gate test `TestFinancialDataRepository_PlugFields_PersistenceGap` still PASSES asserting the current "drop on write, read back zero" behavior. Phase 2+ will:
1. Add the 4 plug columns to `internal/infra/database/schema.sql`
2. Update the SQLite repository's `storeWith` INSERT (lines `:56-87`)
3. Update `GetLatest` / `GetHistorical` / `GetByPeriod` SELECTs
4. Flip the test's assertion to the desired-behavior gate per its docstring's "flip to wantOtherCA after Phase 1+ migration" instruction

The integration test that records shadow snapshots does NOT use the SQLite repository (it runs the cleaner against an in-flight `*FinancialData`), so the persistence gap doesn't affect Phase 1's deliverable. Phase 2+ closes it when the views need to round-trip through SQLite.

### Parser-side prerequisites for Phase 2 (recommended but not strictly blocking)

Per the shadow-analysis report §5:
- **AMD + KO `TotalLiabilities == 0` parser dropout** (Cluster B1-PARSER-TL-ZERO). 24 records out of 142 carry false positives that would muddy Phase 2's regression-validation diffs. Suggested follow-up ticket: `T2-BS-3-parser-totalliabilities-zero-amd-ko.md`. Either fix or document a Phase 2 carve-out.
- **JNJ / TSM / BABA bundle capture.** Three basket tickers lack bundles under `artifacts/tier2-baseline/2026-05-15/`. Capture before Phase 2 to close the basket coverage gap (pension cluster on JNJ; FX-conversion cluster on TSM; ADR + contingent-liability cluster on BABA).

---

## Estimated vs. actual effort

| Phase 1 task | Plan estimate | Actual |
|---|---|---|
| Task 1.1 — recompute.go + tests | 4-6h | ~3h (BACKEND) — landed in `ce4794f` |
| Task 1.2 — wire call site | ~1h | ~30 min — landed in `8a81e73` |
| Task 1.3 — integration test + snapshots | 4-6h | ~5h (BACKEND) — landed in `2dae93c`, refined in `d869d1d` (~2h) |
| Task 1.4 — replay regression | ~1h | ~1h — captured timestamp-only drift on AAPL + MSFT |
| Task 1.5/1.6 — docs + tracker | ~2h | ~1h — landed in `98315a5` |
| V-R-Q chain | ~6h total | ~5h — VERIFIER caught the clamp-suspected expectation error; REVIEWER 9 findings; QA 9/9 PASS |
| **Phase 1 main PR total** | **3-5 days (per handoff)** | **~3 days focused** — squarely within the estimate |
| Post-merge follow-up (this PR) | ~1 day for shadow analysis + closeout | ~4h for shadow analysis report + closeout + 9 polish items |

**Calibration take:** the handoff's 3-5 day estimate was accurate. The single sub-estimate that overran was "Task 1.3 — integration test" at 4-6h plan vs. ~7h actual including the deterministic-snapshot refinement landed in `d869d1d`. The refinement was prompted by VERIFIER catching non-deterministic snapshot output between runs; without the determinism fix, the basket-snapshot commit policy would have produced flapping diffs in every subsequent PR. Building deterministic JSON writers from the start (the lesson generalized from Phase 0's experience with replay golden fixtures) would have avoided the second pass.

**Plan-estimate calibration:** continue using "single-worktree-sequential" estimates of 3-5 days for any DC-1 phase with the same scope shape (one new file + one wiring point + one integration test + docs). Phase 2's Adjuster interface refactor is structurally larger (~1-2 weeks) and may benefit from parallel worktrees if it decomposes cleanly into asset-adjuster / liability-adjuster / earnings-adjuster sub-modules.

---

## Acceptance criteria — Phase 1 contributions to DC-1 close

Per the Phase 1 handoff's 10-item checklist (the master AC list referenced by the spec):

- [x] `recomputeUmbrellas` function lands in `internal/services/datacleaner/recompute.go` with godoc explaining shadow-mode purpose. **Landed `ce4794f`.**
- [x] Function is called at the end of the cleaner pipeline (single insertion point). **Landed `8a81e73` at `service.go:230`.**
- [x] Property test (gopter, pinned seed `20260517`) pins "well-formed input → no divergence" baseline. **Landed `ce4794f`; 4 properties × 200 iterations.**
- [x] Integration test records divergences across the 10-ticker basket as structured output. **Landed `2dae93c`; deterministic per-ticker JSON snapshots at `internal/integration/testdata/recompute-shadow/<TICKER>.json`. 7 / 10 basket members covered.**
- [x] Zero downstream behavior change — replay-verified on AAPL + MSFT. **Verified pre-merge; timestamp-only drift in `17-response.json` for both.**
- [x] Full test suite green (modulo SCHED-1 flake). **Confirmed green; SCHED-1 did not fire during BACKEND validation runs.**
- [x] CLAUDE.md Common Gotchas DC-1 entry updated to note Phase 1 SHIPPED + shadow-mode pattern. **Landed `98315a5`; refined to dual-bullet form in this follow-up PR (NIT #7).**
- [x] TESTING.md plug-invariant subsection extended with the recompute-side companion pattern. **Landed `98315a5`.**
- [x] DC-1 reviewer tracker status updated: "Phase 1 SHIPPED 2026-05-19" + Phase 1 progress paragraph. **Landed `98315a5`; cross-references to shadow-analysis + closeout added in this follow-up PR.**
- [x] Shadow analysis report filed. **Filed in this follow-up PR at [datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md](datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md). Phase 2 gate verdict: SATISFIED.**
- [x] Phase 0 closeout-style report filed for Phase 1. **This document.**

All 11 items SATISFIED. The two that were deferred at merge time (AC #10 shadow-analysis report, AC #11 closeout report) are satisfied by this follow-up PR.

**Phase 2 (`Adjuster` interface refactor) is unblocked**, subject to the two non-blocking parser-side prerequisites listed in §"What's deferred to Phase 2+" above.

---

## Next actions

1. **Phase 2 plan authorship** (ARCH cycle). Use the shadow-analysis report's 7-cluster enumeration as input. The plan should:
   - Migrate A1 / A2 / A4 / A5 to the Adjuster interface; A1 as Overlay, A2 / A4 / A5 as Restaters.
   - Migrate B1 / B2 / B3 to the Adjuster interface; all three as Overlays through `InvestedCapital.WACCInputs.DebtLikeClaims`.
   - Lift `recomputeUmbrellas` from observation to authority; drop divergence WARN severity to DEBUG (or remove).
   - Add a Phase 2 reviewer-checklist item: "Did `internal/integration/testdata/recompute-shadow/<TICKER>.json` change? Does the diff match the expected reroute?"
   - Decide on AMD / KO parser-dropout disposition (fix vs. carve-out).
   - Decide on JNJ / TSM / BABA bundle-capture timing.
2. **Track follow-up items in `docs/reviewer/`.** Recommended new tickets:
   - `T2-BS-3-parser-totalliabilities-zero-amd-ko.md` (parser-side fix scope; cited by the shadow-analysis report).
   - (Optional) A "basket bundle-capture" ticket to track JNJ / TSM / BABA bundle acquisition.
3. **Watch the shadow snapshots in every Phase 2+ PR.** Recording-not-asserting policy means the diff review is the regression signal; reviewers must read the snapshot changes intentionally.
