# DC-1 Phase 2 PR-4 — Implementer Handoff

**Phase:** Phase 2, PR-4 of 4 — Liability adjusters migrated to the `Adjuster` interface + B3 OverlaySpec routing to `DebtLikeClaims` + final shim + helper deletion + basket snapshot integration test + T2-BS-3 disposition documentation (Tasks 4.1–4.7)
**Status:** READY TO START
**Estimated effort:** ~1 agent shift (seven B-rule task commits per the plan §7; **highest-risk PR in the Phase 2 stack — allocate extra verification budget**)
**Branch base:** `dc1-phase-2-pr-3` (PR-3 final tip after Task 3.8 `4af3c33`). **NOT master.** PR-4 stacks on top of PR-3 per the 4-PR strategy in the implementation plan §8. When PR-4 lands the full 4-PR stack is ready for final master merge approval.
**Master state (FYI only — do NOT integrate yet):** Master continues to evolve. PR-4 is independent of master until the final 4-PR stack merge.

**Worktree workflow (REQUIRED — per user's 2026-05-22 directive `feedback_worktree_first_workflow`):**
The main `midas/` directory MUST stay on `master`. PR-4 work happens in a sibling worktree at `../midas-dc1-phase-2-pr-4/`. Set it up before any code work:

```
# From the main midas/ directory (which is on master)
git worktree add ../midas-dc1-phase-2-pr-4 -b dc1-phase-2-pr-4 dc1-phase-2-pr-3
cd ../midas-dc1-phase-2-pr-4
```

All PR-4 commands run inside that sibling directory. The PR-1, PR-2, and PR-3 branches stay checked out in their own worktrees at `../midas-dc1-phase-2-pr-1/`, `../midas-dc1-phase-2-pr-2/`, and `../midas-dc1-phase-2-pr-3/`. Confirm before EVERY `git commit`:
- `pwd` should end with `midas-dc1-phase-2-pr-4`
- `git rev-parse --abbrev-ref HEAD` should print `dc1-phase-2-pr-4`
- `git worktree list` should show five entries: main midas at master, midas-dc1-phase-2-pr-1 at `dc1-phase-2-pr-1-clean`, midas-dc1-phase-2-pr-2 at `dc1-phase-2-pr-2`, midas-dc1-phase-2-pr-3 at `dc1-phase-2-pr-3`, midas-dc1-phase-2-pr-4 at `dc1-phase-2-pr-4`. If anything else, STOP — you're in the wrong worktree.

Worktrees are git's real isolation primitive. Per-PR worktrees keep parallel sessions and Bash branch-switch friction from contaminating HEAD — the two failure modes that hit PR-1.

---

## TL;DR

PR-3 introduced 7 C-rule native adapters (5 Restaters + 2 FlagEmitters; C6 `EquityOffset=0` special case; C4 plan-vs-code disagreement resolved by trust-the-code precedent) and deleted the earnings-side shim branch. **PR-4 extends the same pattern to Category B (liability adjusters B1/B2/B3) at `internal/services/datacleaner/adjustments/liabilities.go`.**

All three B-rules are **OverlayEmitters**:

```go
OverlaySpec{
    OverlayID:        "B<n>_<name>",
    Field:            "TotalDebt" | "DebtLikeClaims",  // B3 is DebtLikeClaims
    Operation:        "add",
    Amount:           presentValue,
    AmountSemantics:  AmountIncremental,
    Reasoning:        ...,
    AIProvenance:     nil except B3 best-effort,
}
```

with three load-bearing nuances:

- **B3 routes to `Field:"DebtLikeClaims"` (NOT `"TotalDebt"`)** per the substantive accuracy correction in spec §"B3 routing correction" lines 181-189. The OverlaySpec records the Phase 4 routing intent. The dual-write mutation STILL points at `TotalDebt` via the orchestrator at `liabilities.go:87-88` (Phase 4 flips the consumer to read `Overlays[Field:"DebtLikeClaims"]` and the dual-write mutation gets deleted then).

- **B3 AIProvenance capture (Q4 deferral carry-through).** When AI fires (`la.aiEnabled && la.aiService != nil`), populate `OverlaySpec.AIProvenance` with best-effort fields (`ModelName`, `Confidence`, `Probability` from the existing `ai.AnalyzeFootnote` response). **Hash fields (`PromptHash`, `SourceDocHash`) stay zero-string in Phase 2 with a documented TODO for Phase 3** per Q4 resolution. Today's AI helper does not return prompt/source hashes.

- **Orchestrator-level `data.TotalDebt += result.Amount` absorption (Task 4.4 — Option α).** The mutation at `liabilities.go:87-88` is absorbed into the `ProcessLiabilityAdjustments` wrapper. **Option α (chosen):** keep `ProcessLiabilityAdjustments` as a thin wrapper that for each rule calls the new `Apply*` method and then performs the dual-write mutation `data.TotalDebt += output.Overlays[0].Amount` (when output is OverlayEmitter-shaped and Field is `"TotalDebt"` or `"DebtLikeClaims"`). The mutation logic moves into the wrapper but the bytewise behavior is preserved. Option β (each B-rule mutating `data.TotalDebt` inside its own `Apply`) was rejected for spreading the dual-write site across three files.

**PR-4 ALSO:**
- **Deletes the liability-side shim branch** in `service.go::applyActiveAdjustments` (Task 4.5; mirrors PR-2 Task 2.6 + PR-3 Task 3.8).
- **Deletes the shim helpers** `shimLedgerEntriesFromLegacy` AND `shimLedgerEntriesFromLegacyExcluding` — after Task 4.5 lands, neither has any caller.
- **Adds the basket snapshot integration test** at `internal/integration/datacleaner_ledger_basket_test.go` (Task 4.6 — `TestLedger_BasketSnapshot_ClusterPrediction`). Uses the existing 10 `recompute-shadow/<TICKER>.json` snapshots as truth source for which AdjusterIDs should appear in each ticker's ledger.
- **Lands T2-BS-3 disposition documentation** (Task 4.7): updates `docs/reviewer/T2-BS-3-parser-totalliabilities-zero-amd-ko.md` with "Disposition: Option B chosen by Phase 2 ARCH 2026-05-19. Parser fix deferred." Tracker stays OPEN.
- **Adds the Phase 2 closing CLAUDE.md gotcha** (Task 4.7) — a new "DC-1 datacleaner refactor — Phase 2 SHIPPED" bullet replacing the per-PR sub-bullets (PR-1/PR-2/PR-3) with a consolidated Phase 2 summary. The per-PR sub-bullets in CLAUDE.md should be removed in favor of one consolidated bullet describing the four-PR landing as a single shipped phase.
- **Creates the Phase 2 closeout doc** at `docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-2-closeout.md` (template from Phase 1 closeout).

**PR-4 is the HIGHEST-RISK PR in the Phase 2 stack** because:
1. B-rules touch the `TotalDebt` surface that DDM depends on (any drift breaks `TestDDM_LegacyPath_BitForBit`).
2. B3 AI provenance is a new orchestration path on a code site that already has the most-fragile current behavior (the AI helper at `liabilities.go:622-701`).
3. The orchestrator-level `liabilities.go:87-88` absorption is the LOAD-BEARING dual-write mutation site that, if mishandled, silently changes `data.TotalDebt` magnitudes for every ticker.
4. Final shim + helper deletion changes the orchestrator's call shape (mirror PR-2 Task 2.6 + PR-3 Task 3.8 patterns carefully).

**Run all 3 replay tickers (AAPL + MSFT + JPM) per highest-risk discipline.** Reject merge if any numeric drift appears in `17-response.json`.

---

## Required reading (in order)

### Tier 1 — Identity and conventions

1. **`CLAUDE.md`** — project conventions. **MANDATORY: read BOTH the "DC-1 Phase 2 PR-2 SHIPPED 2026-05-22" AND the "DC-1 Phase 2 PR-3 SHIPPED 2026-05-22" sub-bullets** that PR-2's + PR-3's wrap-up commits added under the DC-1 Phase 1 SHIPPED gotcha. They describe the canonical pattern (mutation-FREE `Apply`; dispatcher owns dual-write), all 4 role flavors, and the C6 `EquityOffset=0` precedent that PR-4 inherits.
2. **`AGENTS.md`** — Tier 4 row 17b for DC-1 (updated to reflect PR-3 SHIPPED).
3. **`docs/THESIS.md`** — DC-1 row (Phase 2 in-flight, PR-3 SHIPPED, PR-4 next).

### Tier 2 — Phase 2 design + PR-2/PR-3 ground truth (the canonical pattern to inherit)

4. **`docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md`** — the authoritative Phase 2 plan. Focus on:
   - §3 (Adjuster interface design) — PR-1 implemented this; PR-4 consumes it.
   - **§4 row "Cluster — Liabilities"** (table at lines 364-366) — the per-B-rule field map (B1 operating leases → `Field:"TotalDebt"`; B2 pension/OPEB → `Field:"TotalDebt"`; B3 contingent liabilities → `Field:"DebtLikeClaims"`).
   - §4.4 "Why earnings before liabilities" — confirms B-rules carry the highest risk; uses the C-rule rehearsal in PR-3 as the validated interface foundation.
   - **§7 PR-4 Tasks 4.1-4.7** (lines 627-670) — your task list, in execution order. Read each task's sub-steps and acceptance signal carefully.
   - §10 Q2 / Q4 — both remain DEFERRED to Phase 3 by design. PR-4 inherits the deferral: emit `TaxShieldDTA: 0` where applicable (B-rules don't typically populate it; the field is irrelevant for OverlayEmitters anyway); B3 `AIProvenance` zero-hash with documented TODO.
   - **PR-4 acceptance criteria** at end of §7.
5. **`docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`** — focus on "B3 routing correction" (lines 181-189), the OverlaySpec design, and the `AmountSemantics` design.
6. **PR-3's SHIPPED source code as the canonical pattern to imitate** — read these files end-to-end:
   - `internal/services/datacleaner/adjustments/earnings.go` — seven C-rule `Apply*` methods, seven adapter types, seven exported constructors, seven translators, seven AdjusterID constants, the `ProcessEarningsAdjustments` dispatcher switch with the capture → Apply → translate → mutate → drain-natives sequence (this is the file to clone the structure from for PR-4).
   - PR-3's seven test files (`c1_restructuring_adjuster_test.go` through `c7_working_capital_adjuster_test.go`) — `Adjuster_Interface_Contract` subtest pattern + dispatcher-level `NativeC*Emission` / `NativeC*SkipPath` tests. **Pay special attention to C6's `EquityOffset=0` subtest** — PR-4 B-rules don't have an analogous special case, but the dedicated-failure-message convention is what to mimic for any future load-bearing pin.
   - `internal/services/datacleaner/adjustments/assets.go` — PR-2's OverlayEmitter is A1 goodwill_exclusion. PR-4 B-rules are also OverlayEmitters; A1's adapter + translator shape is the closest existing template.
   - `internal/services/datacleaner/service.go::applyActiveAdjustments` — the orchestrator. PR-2 deleted the asset-side branch; PR-3 deleted the earnings-side branch. PR-4 deletes the liability-side branch AND the two helper functions. The TODO comment that PR-3 left ("PR-4 deletes liability branch + the helpers themselves.") is your roadmap.
   - **PR-2 Task 2.5 a_flag_only_reviews_adjuster_test.go** — the AIProvenance considerations carry through to B3's `AIProvenance` best-effort path.

### Tier 3 — PR-4's refactor target

7. **`internal/services/datacleaner/adjustments/liabilities.go`** — your PR-4 refactor target. Read each adjuster end-to-end:
   - **`ProcessLiabilityAdjustments`** (line 56-104): the orchestrator. Lines 87-88 contain the LOAD-BEARING `data.TotalDebt += result.Amount` and `data.InterestBearingDebt += result.Amount` mutations. **Task 4.4 absorbs these into the wrapper per Option α.** Read this function carefully — the dual-write mutation order across B1 → B2 → B3 must be bit-identical to today.
   - **B1 `ProcessOperatingLeaseAdjustment`** (line 107-284, including `fallbackToSimpleCapitalization` at line 226-284): OverlayEmitter. Emit `OverlaySpec{OverlayID:"B1_operating_lease_capitalization", Field:"TotalDebt", Operation:"add", Amount:presentValue, AmountSemantics:AmountIncremental}`. **The B1 fallback at line 226-284 (`fallbackToSimpleCapitalization`) becomes either a separate Adjuster (B1-fallback) or stays inline as a private method** — implementer chooses; document the choice in the commit message. Recommendation: keep inline as a private method to minimize PR-4 surface area. The error-return on calculator failure (per plan §3 line 143) is a known consideration; lift the error to the interface per the design.
   - **B2 `ProcessPensionAdjustment`** (line 287-359): OverlayEmitter. Emit `OverlaySpec{OverlayID:"B2_pension_underfunding", Field:"TotalDebt", Operation:"add", AmountSemantics:AmountIncremental}`. Dual-write unchanged.
   - **B3 `ProcessContingentLiabilityAdjustment`** (line 362-456) + the AI helper at line 622-701: OverlayEmitter with **`Field:"DebtLikeClaims"`** (NOT `"TotalDebt"`). The OverlaySpec's `Field` records the Phase 4 routing intent; the dual-write mutation STILL points at `TotalDebt` via the orchestrator at `:87-88`. When AI fires (`la.aiEnabled && la.aiService != nil`): populate `OverlaySpec.AIProvenance` with best-effort fields (`ModelName`, `Confidence`, `Probability`). Hash fields stay zero-string with documented TODO.

### Tier 4 — Tests + replay tooling

8. **`internal/services/datacleaner/adjustments/liabilities_test.go`** — existing B-rule tests; preserve all of them. The PR-2/PR-3 pattern: add NEW `b1_operating_leases_adjuster_test.go`, `b2_pension_adjuster_test.go`, `b3_contingent_liabilities_adjuster_test.go` for the `Adjuster_Interface_Contract` and dispatcher-level `NativeB*Emission` / `NativeB*SkipPath` subtests. Do NOT bulk-rewrite `liabilities_test.go`. The B3 test must pin the `Field:"DebtLikeClaims"` invariant with an explicit failure message naming "Phase 4 routing intent — Phase 2 dual-write still mutates TotalDebt; Phase 4 flips consumer to read OverlaySpec[Field:'DebtLikeClaims']".
9. **`internal/observability/replay/schema.go`** — `CurrentSchemaVersions["FinancialData"]` is at **8** already (PR-2 Task 2.1 bumped it). **DO NOT bump again in PR-4.** No structural schema change — PR-4 only populates additional `OverlaySpec` slices on `data.Overlays` that already exist on `FinancialData`.
10. **`cmd/replay/main.go`** — replay CLI. Spot-check against all 3 replay tickers per highest-risk discipline:
    ```
    go run ./cmd/replay --from=parsed artifacts/tier2-baseline/2026-05-19/AAPL/req_<uuid>/
    go run ./cmd/replay --from=parsed artifacts/tier2-baseline/2026-05-19/MSFT/req_<uuid>/
    go run ./cmd/replay --from=parsed artifacts/tier2-baseline/2026-05-19/JPM/req_<uuid>/
    ```
    Expected: **zero numeric drift in `17-response.json`** on all 3 tickers (highest-risk discipline — JPM is critical because the bit-for-bit DDM goldens key off it). The `data.Overlays` slice will gain B-rule entries (the success signal). REJECT merge if any numeric drift on the valuation outputs.

---

## PR-4 scope

### Tasks (all 7 must land in this PR — see plan §7 Tasks 4.1-4.7 for full sub-steps)

| # | Task | File(s) | Role | Notes |
|---|------|---------|------|-------|
| 4.1 | Refactor B1 (operating leases) to `Adjuster` | `adjustments/liabilities.go`, `b1_operating_leases_adjuster_test.go` (NEW) | OverlayEmitter | Emit `OverlaySpec{OverlayID:"B1_operating_lease_capitalization", Field:"TotalDebt", Operation:"add", Amount:presentValue, AmountSemantics:AmountIncremental}`. Dual-write at `:87-88` STAYS unchanged (Task 4.4 absorbs it). Decide whether `fallbackToSimpleCapitalization` becomes a separate Adjuster or stays inline; document the choice. |
| 4.2 | Refactor B2 (pension/OPEB) to `Adjuster` | `adjustments/liabilities.go`, `b2_pension_adjuster_test.go` (NEW) | OverlayEmitter | Emit `OverlaySpec{OverlayID:"B2_pension_underfunding", Field:"TotalDebt", Operation:"add", AmountSemantics:AmountIncremental}`. Dual-write unchanged. |
| 4.3 | Refactor B3 (contingent liabilities) to `Adjuster` — including AI provenance capture | `adjustments/liabilities.go` + AI helper at lines 622-701, `b3_contingent_liabilities_adjuster_test.go` (NEW) | OverlayEmitter | Emit `OverlaySpec{OverlayID:"B3_contingent_liability", Field:"DebtLikeClaims", Operation:"add", Amount:probableAmount, AmountSemantics:AmountIncremental}`. Field is **`DebtLikeClaims`** NOT `TotalDebt` (substantive accuracy correction). When AI fires: populate `AIProvenance` best-effort (`ModelName`, `Confidence`, `Probability` from `ai.AnalyzeFootnote`); zero-string `PromptHash`/`SourceDocHash` with documented Phase 3 TODO. |
| 4.4 | Absorb orchestrator-level `data.TotalDebt += result.Amount` at `liabilities.go:87-88` into wrapper | `adjustments/liabilities.go` lines 56-104 (`ProcessLiabilityAdjustments`) | — | **Option α (chosen)**: keep `ProcessLiabilityAdjustments` as a thin wrapper; for each rule call the new `Apply*` method then perform the dual-write `data.TotalDebt += output.Overlays[0].Amount` (when output is OverlayEmitter-shaped and Field is `"TotalDebt"` or `"DebtLikeClaims"`). Bytewise behavior preserved; mutation logic moves into the wrapper. |
| 4.5 | Delete the PR-1 shim's liability-side branch AND the helpers `shimLedgerEntriesFromLegacy` + `shimLedgerEntriesFromLegacyExcluding` | `service.go::applyActiveAdjustments` | — | Mirror PR-2 Task 2.6 + PR-3 Task 3.8. After 4.5 the PR-1 shim is FULLY deleted including both helpers — no remaining callers. Run `go build ./...` immediately to catch any miss. |
| 4.6 | Add `TestLedger_BasketSnapshot_ClusterPrediction` integration test | `internal/integration/datacleaner_ledger_basket_test.go` (NEW) | — | Per plan §5.2. Use the existing 10 `recompute-shadow/<TICKER>.json` snapshots as truth source for which AdjusterIDs should appear in each ticker's ledger. |
| 4.7 | T2-BS-3 documentation + Phase 2 closing CLAUDE.md gotcha + spec/plan/tracker/THESIS/AGENTS wrap-up + Phase 2 closeout doc | `CLAUDE.md`, `docs/reviewer/T2-BS-3-parser-totalliabilities-zero-amd-ko.md`, `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md`, `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`, `docs/THESIS.md`, `AGENTS.md`, `docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-2-closeout.md` (NEW), `ARCHITECTURE.md`, `TESTING.md` | — | **Replace the per-PR sub-bullets (PR-1/PR-2/PR-3) in CLAUDE.md with a single consolidated "DC-1 datacleaner refactor — Phase 2 SHIPPED" bullet.** Update T2-BS-3 tracker. Update DC-1 tracker. Update spec + plan changelogs. Update THESIS row + AGENTS row 17b. Update ARCHITECTURE/TESTING. Create Phase 2 closeout doc. |

### Suggested commit cadence

One commit per task (7 commits total). Each commit:
- includes its new test file (where applicable),
- includes the per-rule additions to `liabilities.go` (AdjusterID constant + adapter type + exported constructor + `ApplyB*` method + translator + dispatcher switch wiring),
- runs the full acceptance gate (below) before commit,
- preserves the canonical legacy `entities.Adjustment.Reasoning` string byte-identically (PR-2 Task 2.1's NIT carry-through: prefer byte-identical Reasoning to avoid REVIEWER NITs; if you must drift, document it explicitly in the commit message; PR-3 C3/C5/C6 added a `Revenue > 0` percentage guard for defensive reasons — analogous guards in B-rules should be documented similarly).

### SchemaVersion

**STAYS at 8.** PR-2 Task 2.1 already bumped 7→8 atomic with the first populating PR per `feedback_schema_version_atomic_bump`. PR-3 didn't bump. PR-4 does NOT bump because PR-4 ships the SAME structural envelope (LedgerEntries + Overlays + Flags) PR-2 introduced — it just populates more of it (the Overlays slice). Do NOT touch `internal/observability/replay/schema.go`.

### What NOT to build

- Do NOT touch `adjustments/assets.go` (= PR-2 work, settled). The 6 A-rule adapters/translators stay.
- Do NOT touch `adjustments/earnings.go` (= PR-3 work, settled). The 7 C-rule adapters/translators stay.
- Do NOT introduce `CleanedFinancialData {AsReported, Restated, InvestedCapital}` views (= Phase 3).
- Do NOT migrate any consumer of `data.TotalDebt` to read from `Overlays[Field:"DebtLikeClaims"]` (= Phase 4).
- Do NOT delete the dual-write mutation in `ProcessLiabilityAdjustments` (= Phase 3). Task 4.4 only ABSORBS the mutation into the wrapper; the mutation itself stays.
- Do NOT compute SHA-256 `PromptHash` / `SourceDocHash` on B3's `AIProvenance` (= Phase 3 per Q4). Zero-string with documented TODO.
- Do NOT modify `internal/services/datacleaner/recompute.go` — it's the regression signal.
- Do NOT bump `CurrentSchemaVersions["FinancialData"]` (stays at 8 from PR-2).
- Do NOT touch `internal/services/valuation/*` — Tier 2 territory.
- Do NOT regenerate the JPM/BAC/WFC DDM bit-for-bit golden fixtures — they pin a load-bearing invariant.
- Do NOT modify the `Adjuster` interface or the entity field shapes — PR-1 settled them; Phase 3 reads them by name.
- Do NOT fix T2-BS-3 itself in PR-4 — the disposition is "Option B carve-out; parser fix deferred". Task 4.7 only updates the tracker docs.

---

## Critical invariants (PR-4 must preserve — HIGHEST-RISK DISCIPLINE)

1. **Bit-for-bit DDM legacy path:** `TestDDM_LegacyPath_BitForBit` (jpm/bac/wfc) GREEN at every commit. **NON-TRIVIAL in PR-4** — DDM reads `data.TotalDebt` for the equity-bridge step. Any drift in `TotalDebt` magnitude on JPM breaks the invariant. Run after every commit; if any task's commit fails this, REVERT (do NOT update goldens to make it pass).
2. **Shadow snapshot byte-identity:** `internal/integration/testdata/recompute-shadow/<TICKER>.json` UNCHANGED for all 10 tickers. B-rule migration must preserve the dual-write behavior bit-identically. Run `TestDataCleanerRecompute_ShadowMode_TickerBasket` after each commit and `git diff --quiet internal/integration/testdata/recompute-shadow/` must exit 0.
3. **Phase 1 NoMutation invariant:** `TestRecomputeUmbrellas_NoMutation` GREEN at every commit.
4. **Dual-write discipline:** every migrated B-rule MUST keep the existing `data.TotalDebt += result.Amount` mutation alongside the new `OverlaySpec` emission — Task 4.4 absorbs the mutation into the orchestrator wrapper but the mutation itself stays. Phase 3 deletes the dual-write; Phase 2 leaves it in place.
5. **Ledger ordering invariant:** `TestOrchestrator_LedgerOrdering` GREEN. **Asset → Liability → Earnings partition.** PR-4's B-rules must emit BETWEEN the asset partition (A-rules from PR-2) and the earnings partition (C-rules from PR-3). The dispatcher in `ProcessLiabilityAdjustments` (parallel to PR-2's `ProcessAssetAdjustments` and PR-3's `ProcessEarningsAdjustments`) appends to `result.NativeLedgerEntries` / `result.NativeOverlays` / `result.NativelyEmittedRuleIDs`; `service.go::applyActiveAdjustments` drains them into `data.AdjustmentLedger` / `data.Overlays` in the existing assets → liabilities → earnings call order. Task 4.5 deletes the post-call liability shim invocation.
6. **PR-1 entity field shapes are frozen.** Do NOT add/remove/rename fields on `LedgerEntry`, `OverlaySpec`, `AdjustmentLedger`, `AmountSemantics`, or `AIProvenance`. Phase 3 ARCH consumes these by name.
7. **B3 `Field:"DebtLikeClaims"` invariant.** Pin via the B3 test with explicit failure message naming the Phase 4 routing intent.
8. **`data.TotalDebt` mutation order across B1→B2→B3 must be bit-identical to today.** Task 4.4's wrapper absorption preserves byte-for-byte mutation order; do NOT reorder B-rule invocation.

---

## Gotchas inherited from PR-2/PR-3 (the agent will trip over these without warning)

1. **Worktree discipline.** `pwd` + `git rev-parse --abbrev-ref HEAD` + `git worktree list` before EVERY commit. Per-PR worktrees eliminate the parallel-session HEAD-contamination failure mode that hit PR-1; verify anyway. **Five worktrees expected** before each PR-4 commit.

2. **CRLF / LF noise on shadow snapshots is OK.** `git status` / `git diff` may render CRLF↔LF warnings on `internal/integration/testdata/recompute-shadow/*.json`; that's `core.autocrlf` cosmetic noise, not content drift. Use `git diff --quiet` (note `--quiet`) as the authoritative gate — exit 0 means byte-identical.

3. **Per-rule translator pattern stays — by PR-4 end the codebase has ~13 translator instances.** PR-2 explicitly preserved 6 per-rule translators; PR-3 added 7 more (one per C-rule). PR-4 adds 3 more (B1/B2/B3). Extraction to a generic `dispatchNativeAdjuster()` helper is STILL DEFERRED (per-rule structure justified by role differences — Restater reads `LedgerEntry.DeltaAmount`, OverlayEmitter reads `OverlaySpec.Amount`, FlagEmitter always returns `Applied:false`). Do NOT prematurely extract. If a reviewer pushes back on duplication, point at PR-2's Task 2.1 code-quality review (deferred extraction; deemed YAGNI) and PR-3's C-rule-confirmation of the same call.

4. **`applyCtx` nil-context propagation is acceptable.** PR-2/PR-3 `Apply*` methods ignore the `ctx` parameter. The interface signature carries `ctx context.Context` for forward-compatibility — Phase 3+ implementations may use it; PR-4 may ignore it. B3's AI path DOES need `ctx` for `ai.AnalyzeFootnote` invocation — propagate through to the AI call site.

5. **Reasoning-string discipline (the PR-2/PR-3 NIT carry-through).** Default to byte-identical legacy `Reasoning` strings across all 3 B-rules unless the legacy string is genuinely broken (e.g., NaN/+Inf on Revenue=0); minimize REVIEWER NITs.

6. **B3 `Field:"DebtLikeClaims"` vs `TotalDebt` mismatch is INTENTIONAL.** The OverlaySpec records the Phase 4 routing intent; the dual-write mutation still points at `TotalDebt` via the orchestrator at `:87-88`. The mismatch is the substantive accuracy correction in the spec. Document the intent in the B3 commit message AND in the test failure message.

7. **B3 AIProvenance zero-hash is INTENTIONAL.** Today's AI helper does not return prompt/source hashes. Best-effort populate available fields (`ModelName`, `Confidence`, `Probability`); leave `PromptHash`/`SourceDocHash` zero-string with a TODO comment naming Phase 3 as the implementation owner. Document in commit message.

8. **Task 4.4 (orchestrator absorption) is the riskiest task.** The `data.TotalDebt += result.Amount` and `data.InterestBearingDebt += result.Amount` mutations at `liabilities.go:87-88` are LOAD-BEARING — they're what DDM and the equity-bridge read. Option α moves the mutation into the wrapper; the test discipline is "bytewise behavior preserved". Run `TestDDM_LegacyPath_BitForBit` immediately after Task 4.4 lands.

9. **Task 4.5 deletes TWO helpers (`shimLedgerEntriesFromLegacy` + `shimLedgerEntriesFromLegacyExcluding`) — not just the shim branch.** After PR-4 lands, the PR-1 shim is FULLY deleted. Run `go build ./...` immediately to catch any stale caller (especially in tests).

10. **Task 4.7 is the Phase 2 closeout.** Replace the per-PR sub-bullets in CLAUDE.md with a single consolidated "Phase 2 SHIPPED" bullet describing the four-PR landing. Update T2-BS-3 tracker, DC-1 tracker, spec/plan changelogs, THESIS row, AGENTS row 17b, ARCHITECTURE/TESTING, AND create the Phase 2 closeout doc. This is a large docs sweep — budget time for it.

11. **PR-4 is the HIGHEST-RISK PR in the stack.** Allocate extra verification budget. Run all 3 replay tickers (AAPL/MSFT/JPM) after each task commit, not just at PR-4 closeout. Reject merge on any numeric drift in `17-response.json`.

---

## Acceptance gates (run before every commit; full-suite + all 3 replay tickers before final PR-4 closeout)

```bash
# 1. Build
go build ./...

# 2. Adjuster-package unit tests (fast; new B-rule + all prior cases)
go test ./internal/services/datacleaner/adjustments/... -count=1

# 3. LOAD-BEARING bit-for-bit DDM invariant (run after EVERY commit in PR-4)
go test ./internal/services/valuation/models/ -run TestDDM_LegacyPath_BitForBit -count=1

# 4. Phase 1 recompute invariants
go test ./internal/services/datacleaner/ -run 'TestOrchestrator_LedgerOrdering|TestRecomputeUmbrellas_NoMutation' -count=1

# 5. Phase 1 basket shadow test + byte-identity gate
go test ./internal/integration/... -run TestDataCleanerRecompute_ShadowMode_TickerBasket -count=1
git diff --quiet internal/integration/testdata/recompute-shadow/   # MUST exit 0

# 6. Basket snapshot test (added by Task 4.6)
go test ./internal/integration/... -run TestLedger_BasketSnapshot_ClusterPrediction -count=1

# 7. Full suite (skip if flaky datafetcher race re-fires; isolate-rerun until green)
go test ./... -count=1

# 8. Replay spot-check on ALL 3 tickers (highest-risk discipline)
go run ./cmd/replay --from=parsed artifacts/tier2-baseline/2026-05-19/AAPL/req_<uuid>/
go run ./cmd/replay --from=parsed artifacts/tier2-baseline/2026-05-19/MSFT/req_<uuid>/
go run ./cmd/replay --from=parsed artifacts/tier2-baseline/2026-05-19/JPM/req_<uuid>/
# Expected: zero NUMERIC drift in 17-response.json's valuation_summary on ALL 3 tickers.
# Expected: 10-clean-output.json's overlays slice now contains B-rule OverlaySpec entries (the success signal).
# REJECT MERGE on any numeric drift.
```

---

## PR-4 acceptance criteria

All gates GREEN before VERIFIER handoff:

- All Task 4.1–4.7 acceptance signals green (one per task — see §7 of the plan for per-task acceptance).
- `TestDDM_LegacyPath_BitForBit` GREEN at every commit (LOAD-BEARING — JPM/BAC/WFC must stay bit-for-bit).
- `TestRecomputeUmbrellas_NoMutation` GREEN.
- `TestOrchestrator_LedgerOrdering` GREEN (asset → liability → earnings partition preserved; B-rule entries appear in the liability partition).
- `TestLedger_BasketSnapshot_ClusterPrediction` GREEN (new integration test from Task 4.6).
- Full `go test ./... -count=1` GREEN modulo documented pre-existing scheduler-race flake.
- `internal/integration/testdata/recompute-shadow/<TICKER>.json` byte-identical for all 10 tickers (`git diff --quiet` exit 0).
- **AAPL + MSFT + JPM replay all show zero NUMERIC drift in `17-response.json`** — REJECT MERGE on any drift. `10-clean-output.json`'s `overlays` now contains B-rule OverlaySpec entries (the per-PR success signal).
- Coverage ≥80% on migrated `liabilities.go` methods (CLAUDE.md target ≥90% for critical finance modules; B-rule code lives there).
- All documentation updates land in Task 4.7 (Phase 2 closing CLAUDE.md gotcha replacing per-PR sub-bullets; T2-BS-3 tracker status update; DC-1 tracker Phase 2 SHIPPED paragraph; spec/plan changelog rows; THESIS row; AGENTS row 17b; ARCHITECTURE/TESTING; Phase 2 closeout doc).

---

## Handoff to next phase

When PR-4 ships:
- The full 4-PR stack (`dc1-phase-2-pr-1-clean` → `dc1-phase-2-pr-2` → `dc1-phase-2-pr-3` → `dc1-phase-2-pr-4`) is ready for final master merge approval.
- Notify user. Wait for explicit go-ahead before the final merge sequence.
- After merge to master: archive the four PR worktrees; close out the Phase 2 closeout doc with the final merge SHA; bump the engine `CalculationVersion` if/when Phase 3 ships any consumer change (Phase 2 alone does not bump version — dual-write means behavior is bytewise-identical to pre-Phase-2).
- **Phase 3 ARCH spec authoring begins** — view reconstruction (`CleanedFinancialData {AsReported, Restated, InvestedCapital}` accessors), Q2/Q4 resolution (A2 TaxShieldDTA actual population; AIProvenance SHA-256 hash computation), and the 13-site consumer migration plan. Phase 3 ARCH consumes the `LedgerEntry.EquityOffset` semantics from Phase 2 (C6's `EquityOffset=0` precedent is load-bearing — Phase 3 `Restated()` must NOT add C6 DeltaAmount to retained earnings).

## Change log

| Date | Change |
|---|---|
| 2026-05-22 | PR-4 handoff doc filed by orchestrator after PR-3 SHIPPED (8 commits on `dc1-phase-2-pr-3`, all load-bearing invariants GREEN; 7 C-rule native migration — 5 Restaters including C6 EquityOffset=0 + 2 FlagEmitters including C4 plan-vs-code disagreement; earnings-side shim deletion). Anchored at branch `dc1-phase-2-pr-3` tip `4af3c33`. Canonical pattern from PR-2 + PR-3 (mutation-FREE `Apply*` + dispatcher-owns-dual-write + per-rule translator) is the inheritance contract for PR-4. PR-4 is the highest-risk PR in the Phase 2 stack — orchestrator-level `data.TotalDebt += result.Amount` absorption, B3 `Field:"DebtLikeClaims"` routing intent, B3 AIProvenance best-effort with zero-hash TODO, final shim + helper deletion, basket snapshot integration test, T2-BS-3 disposition documentation, Phase 2 closing CLAUDE.md gotcha. Run all 3 replay tickers (AAPL/MSFT/JPM) after each commit. |
