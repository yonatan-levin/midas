# DC-1 Phase 2 PR-3 — Implementer Handoff

**Phase:** Phase 2, PR-3 of 4 — Earnings adjusters migrated to the `Adjuster` interface (Tasks 3.1–3.8)
**Status:** READY TO START
**Estimated effort:** ~1 agent shift (eight C-rule task commits + one shim-deletion commit; same rhythm as PR-2)
**Branch base:** `dc1-phase-2-pr-2` (PR-2 final tip after Task 2.7 `df25866`). **NOT master.** PR-3 stacks on top of PR-2 per the 4-PR strategy in the implementation plan §8. PR-4 will stack on PR-3.
**Master state (FYI only — do NOT integrate yet):** Master continues to evolve (Tier 2 closeout merged; sibling work may land). PR-3 is independent of master until the final 4-PR stack merge.

**Worktree workflow (REQUIRED — per user's 2026-05-22 directive `feedback_worktree_first_workflow`):**
The main `midas/` directory MUST stay on `master`. PR-3 work happens in a sibling worktree at `../midas-dc1-phase-2-pr-3/`. Set it up before any code work:

```
# From the main midas/ directory (which is on master)
git worktree add ../midas-dc1-phase-2-pr-3 -b dc1-phase-2-pr-3 dc1-phase-2-pr-2
cd ../midas-dc1-phase-2-pr-3
```

All PR-3 commands run inside that sibling directory. The PR-1 and PR-2 branches stay checked out in their own worktrees at `../midas-dc1-phase-2-pr-1/` and `../midas-dc1-phase-2-pr-2/`. Confirm before EVERY `git commit`:
- `pwd` should end with `midas-dc1-phase-2-pr-3`
- `git rev-parse --abbrev-ref HEAD` should print `dc1-phase-2-pr-3`
- `git worktree list` should show four entries: main midas at master, midas-dc1-phase-2-pr-1 at `dc1-phase-2-pr-1-clean`, midas-dc1-phase-2-pr-2 at `dc1-phase-2-pr-2`, midas-dc1-phase-2-pr-3 at `dc1-phase-2-pr-3`. If anything else, STOP — you're in the wrong worktree.

Worktrees are git's real isolation primitive. Per-PR worktrees keep parallel sessions and Bash branch-switch friction from contaminating HEAD — the two failure modes that hit PR-1.

---

## TL;DR

PR-2 migrated all 6 Category A adjusters (A1 OverlayEmitter, A2/A4 Restaters, A5 Restater + TaxShieldDTA, 2 FlagEmitter reviews) to the `Adjuster` interface and deleted the asset-side shim branch. **PR-3 extends the same pattern to Category C (earnings normalizations C1–C7) at `internal/services/datacleaner/adjustments/earnings.go`.**

Each C-rule becomes a **Restater** emitting:

```go
LedgerEntry{
    Component:    "NormalizedOperatingIncome" | "InterestExpense",
    DeltaAmount:  ±X,                  // matches today's mutation
    EquityOffset: DeltaAmount,         // OI changes flow to retained earnings
    TaxShieldDTA: 0,                   // Phase 3 work; not populated in PR-3
}
```

with two flavors of exception:

- **C6 (capitalized interest)** is an interest-expense reclassification (`data.InterestExpense += data.CapitalizedInterest`). It does NOT flow to retained earnings — it shifts dollars between line items on the income statement. So C6 emits `Component:"InterestExpense", DeltaAmount: +data.CapitalizedInterest, EquityOffset: 0`. The legacy `entities.Adjustment.Type = Reclassify` is the signal.
- **C7 (working capital)** is flag-only today — `ProcessWorkingCapitalAdjustment` at `earnings.go:389-440` does NOT mutate `data.NormalizedOperatingIncome` or any other field; it only emits a `Flag`. So C7 emits `Fired:false` LedgerEntries with non-empty `AdjusterOutput.Flags` — the same FlagEmitter convention that PR-2's Task 2.5 locked in for the 2 asset-side reviews.
- **C4 (stock-based compensation) is the same flag-only pattern.** `ProcessStockCompensationAdjustment` at `earnings.go:244-295` emits an `entities.Adjustment` with `Type:Reclassify` (`FromAccount:"StockBasedCompensation"`, `ToAccount:"OperatingExpenses"`) but does NOT mutate any field on `data`. So C4 follows the FlagEmitter convention from PR-2 Task 2.5: `Fired:false` LedgerEntry with the dilution Flag in `AdjusterOutput.Flags`. **The plan §7 Task 3.1-3.7 says "C4 same pattern as C1" — that's wrong in light of today's code; flag C4 as FlagEmitter in the PR-3 commit message.** Re-confirm by reading `earnings.go:244-295` yourself before deciding.

**PR-3 ALSO deletes the earnings-side shim branch** in `service.go::applyActiveAdjustments` (Task 3.8) — mirror the Task 2.6 deletion. After PR-3 lands, only the liability-side shim branch remains; PR-4 deletes that one alongside the helpers `shimLedgerEntriesFromLegacy` and `shimLedgerEntriesFromLegacyExcluding`.

**Predicted snapshot drift = ZERO** (per implementation plan §4 row C / §4.row "Earnings adjusters orthogonal to balance-sheet" — the earnings adjusters touch income-statement fields not visible in `recomputeUmbrellas`, so the Phase 1 shadow snapshot is unaffected by C-rule migration). This is the lowest-risk PR in the Phase 2 stack — use it as a "ship the interface against C-rules" rehearsal before PR-4's high-risk B-rule work. Tier 2 DDM bit-for-bit invariant stays GREEN trivially (DDM doesn't read earnings-normalization fields).

---

## Required reading (in order)

### Tier 1 — Identity and conventions

1. **`CLAUDE.md`** — project conventions. **MANDATORY: read the new "DC-1 Phase 2 PR-2 SHIPPED 2026-05-22" sub-bullet** that PR-2's wrap-up commit added under the existing DC-1 Phase 1 SHIPPED gotcha. It describes the canonical pattern (mutation-FREE `Apply`; dispatcher owns dual-write) and the 4 role flavors — all of which apply to PR-3.
2. **`AGENTS.md`** — Tier 4 row for DC-1.
3. **`docs/THESIS.md`** — DC-1 row (Phase 2 in-flight, PR-2 SHIPPED, PR-3 next).

### Tier 2 — Phase 2 design + PR-2 ground truth (the canonical pattern to inherit)

4. **`docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md`** — the authoritative Phase 2 plan. Focus on:
   - §3 (Adjuster interface design) — PR-1 implemented this; PR-3 consumes it.
   - **§4 row "Cluster — Earnings"** — the per-C-rule field map (`NormalizedOperatingIncome` vs `InterestExpense`).
   - §4.4 "Why earnings before liabilities (PR-3 before PR-4)" — establishes PR-3's zero-snapshot-drift prediction.
   - **§7 PR-3 Tasks 3.1–3.8** — your task list, in execution order.
   - §10 Q2 / Q4 — both remain DEFERRED to Phase 3. PR-3 inherits the deferral: emit `TaxShieldDTA: 0` on every C-rule LedgerEntry; do NOT populate AIProvenance.
   - **PR-3 acceptance criteria** at end of §7.
5. **`docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`** — focus on "Adjuster output" and "Adjuster reclassification" sections.
6. **PR-2's SHIPPED source code as the canonical pattern to imitate** — read these files end-to-end:
   - `internal/services/datacleaner/adjustments/assets.go` — six A-rule `Apply*` methods, six adapter types, six exported constructors, six translators (`a1AdjusterOutputToLegacyResult`, `a2AdjusterOutputToLegacyResult`, …), six AdjusterID constants, the `ProcessAssetAdjustments` dispatcher switch with the capture → Apply → translate → mutate → drain-natives sequence. **This is the file to clone the structure from.**
   - `internal/services/datacleaner/adjustments/a1_goodwill_adjuster_test.go` through `a_flag_only_reviews_adjuster_test.go` — six test files showing the per-adjuster `Interface_Contract` subtest pattern and the dispatcher-level `NativeXEmission` / `NativeXSkipPath` tests.
   - `internal/services/datacleaner/service.go::applyActiveAdjustments` — the orchestrator. PR-2 already deleted the asset-side shim branch; PR-3 deletes the earnings-side branch (Task 3.8). The TODO comment that PR-2 left ("PR-3 deletes earnings branch; PR-4 deletes liability branch + the helper itself.") is your roadmap.

### Tier 3 — PR-3's refactor target

7. **`internal/services/datacleaner/adjustments/earnings.go`** — your PR-3 refactor target (442 lines, 7 C-rule entry points). Read each adjuster end-to-end:
   - **C1 `ProcessRestructuringChargesAdjustment`** (line 74): mutates `data.NormalizedOperatingIncome += data.RestructuringCharges` at line 112. Fires when `restructuringAmount > 0` AND `revenue > 0` AND ratio threshold met. Restater. Emit `LedgerEntry{Component:"NormalizedOperatingIncome", DeltaAmount: +restructuringAmount, EquityOffset: +restructuringAmount}`.
   - **C2 `ProcessAssetSaleGainsAdjustment`** (line 141): mutates `data.NormalizedOperatingIncome -= data.AssetSaleGains` at line 154. Restater. Emit `LedgerEntry{Component:"NormalizedOperatingIncome", DeltaAmount: -data.AssetSaleGains, EquityOffset: -data.AssetSaleGains}`.
   - **C3 `ProcessLitigationSettlementsAdjustment`** (line 183): mutates `data.NormalizedOperatingIncome += data.LitigationSettlements` at line 214. Restater (same shape as C1).
   - **C4 `ProcessStockCompensationAdjustment`** (line 244): emits an `entities.Adjustment{Type: Reclassify, FromAccount:"StockBasedCompensation", ToAccount:"OperatingExpenses"}` BUT does NOT mutate any field on `data`. FlagEmitter — Fired:false LedgerEntry with the dilution Flag in `AdjusterOutput.Flags`. **The plan §7 Task 3.1-3.7 calls C4 "same pattern as C1" but the code disagrees; FlagEmitter is the truth-respecting role per today's behavior.**
   - **C5 `ProcessDerivativeGainsLossesAdjustment`** (line 297): mutates `data.NormalizedOperatingIncome -= adjustmentAmount` at line 313 (gain path) OR line 316 (loss path, comment notes "this adds back since amount is negative"). Net effect: one mutation per fire path. Restater. Emit one LedgerEntry with the net `DeltaAmount` (preserve sign carefully — the variable `adjustmentAmount` already carries the correct sign through both branches; the duplicated mutation site at :313/:316 is dead-code-style — read it twice to convince yourself).
   - **C6 `ProcessCapitalizedInterestAdjustment`** (line 347): mutates `data.InterestExpense += data.CapitalizedInterest` at line 360. Restater **but with EquityOffset = 0** (interest reclassification doesn't flow to retained earnings — it's a shift between income-statement line items, not a real economic loss). Emit `LedgerEntry{Component:"InterestExpense", DeltaAmount: +data.CapitalizedInterest, EquityOffset: 0}`. The legacy `entities.Adjustment.Type = Reclassify` is the signal.
   - **C7 `ProcessWorkingCapitalAdjustment`** (line 389): NO mutation on `data`. Emits one flag. FlagEmitter — Fired:false LedgerEntry with the working-capital Flag in `AdjusterOutput.Flags`.

### Tier 4 — Tests + replay tooling

8. **`internal/services/datacleaner/adjustments/earnings_test.go`** — existing C-rule tests; preserve all of them. The PR-2 pattern: add NEW `c1_restructuring_adjuster_test.go` (etc.) for the `Adjuster_Interface_Contract` and dispatcher-level `NativeC1Emission` / `NativeC1SkipPath` subtests. Do NOT bulk-rewrite `earnings_test.go`.
9. **`internal/observability/replay/schema.go`** — `CurrentSchemaVersions["FinancialData"]` is at **8** already (PR-2 Task 2.1 bumped it). **DO NOT bump again in PR-3.** No structural schema change — PR-3 only populates additional `LedgerEntry`/`Flag` slices that already exist on `FinancialData`.
10. **`cmd/replay/main.go`** — replay CLI. Spot-check: `go run ./cmd/replay --from=parsed artifacts/tier2-baseline/2026-05-19/AAPL/req_<uuid>/`. Expected: zero numeric drift in `17-response.json` and zero new `10-clean-output.json` entries that didn't exist post-PR-2 (because PR-2 already populates Native* slices for A-rules; PR-3 adds C-rule LedgerEntries to `data.AdjustmentLedger`, which IS visible in `10-clean-output.json`'s `adjustment_ledger` field). Replay drift on the `adjustment_ledger` field IS expected and is the success signal for "C-rules now emit natively." Numeric drift on valuation outputs is the failure signal — REJECT if seen.

---

## PR-3 scope

### Tasks (all 8 must land in this PR — see plan §7 for full sub-steps)

| # | Task | File(s) | Role | Notes |
|---|------|---------|------|-------|
| 3.1 | Refactor C1 restructuring charges to `Adjuster` | `adjustments/earnings.go`, `adjustments/c1_restructuring_adjuster_test.go` (NEW) | Restater | Emit `LedgerEntry{Component:"NormalizedOperatingIncome", DeltaAmount: +restructuringAmount, EquityOffset: +restructuringAmount}`. Dual-write: keep `data.NormalizedOperatingIncome += restructuringAmount` at `earnings.go:112`. |
| 3.2 | Refactor C2 asset sale gains to `Adjuster` | `adjustments/earnings.go`, `c2_asset_sale_gains_adjuster_test.go` (NEW) | Restater | Emit `LedgerEntry{Component:"NormalizedOperatingIncome", DeltaAmount: -data.AssetSaleGains, EquityOffset: -data.AssetSaleGains}`. |
| 3.3 | Refactor C3 litigation settlements to `Adjuster` | `adjustments/earnings.go`, `c3_litigation_settlements_adjuster_test.go` (NEW) | Restater | Same shape as C1 with `+data.LitigationSettlements`. |
| 3.4 | Refactor C4 stock-based compensation to `Adjuster` | `adjustments/earnings.go`, `c4_stock_compensation_adjuster_test.go` (NEW) | **FlagEmitter** (plan says "same as C1" but code shows no mutation; trust the code) | Emit `Fired:false` LedgerEntry with dilution Flag in `AdjusterOutput.Flags`. **Document the plan disagreement in the commit message.** |
| 3.5 | Refactor C5 derivative gains/losses to `Adjuster` | `adjustments/earnings.go`, `c5_derivative_gains_losses_adjuster_test.go` (NEW) | Restater | Emit ONE LedgerEntry with net `DeltaAmount`. The legacy code has two mutation sites at `:313,316` — both are the same `-= adjustmentAmount`, one in gain-path and one in loss-path; `adjustmentAmount` already carries the correct sign. Emit one LedgerEntry. |
| 3.6 | Refactor C6 capitalized interest to `Adjuster` | `adjustments/earnings.go`, `c6_capitalized_interest_adjuster_test.go` (NEW) | Restater (special: EquityOffset=0) | Emit `LedgerEntry{Component:"InterestExpense", DeltaAmount: +data.CapitalizedInterest, EquityOffset: 0}`. Interest reclassification is income-statement-only; doesn't flow to retained earnings. |
| 3.7 | Refactor C7 working capital to `Adjuster` | `adjustments/earnings.go`, `c7_working_capital_adjuster_test.go` (NEW) | **FlagEmitter** | Emit `Fired:false` LedgerEntry with WC Flag in `AdjusterOutput.Flags`. No mutation. |
| 3.8 | Delete the PR-1 shim's earnings-side branch | `service.go::applyActiveAdjustments` | — | Mirror PR-2 Task 2.6. After 3.8 the PR-2 + PR-3 shim deletions leave only the liability-side branch + the helpers `shimLedgerEntriesFromLegacy` and `shimLedgerEntriesFromLegacyExcluding`. PR-4 deletes those. |

### Suggested commit cadence

One commit per task (8 commits total). Each commit:
- includes its new test file,
- includes the per-rule additions to `assets.go`-style structure (AdjusterID constant + adapter type + exported constructor + `ApplyC*` method + translator + dispatcher switch wiring),
- runs the full acceptance gate (below) before commit,
- preserves the canonical legacy `entities.Adjustment.Reasoning` string byte-identically (PR-2 Task 2.1's reasoning-drift learning: prefer byte-identical Reasoning to avoid REVIEWER NITs; if you must drift, document it explicitly in the commit message — the A1 drift was small and intentional but the commit message overpromised).

### SchemaVersion

**STAYS at 8.** PR-2 Task 2.1 already bumped 7→8 atomic with the first populating PR per `feedback_schema_version_atomic_bump`. PR-3 does NOT bump again because PR-3 ships the SAME structural envelope (LedgerEntries + Overlays + Flags) PR-2 introduced — it just populates more of it. Do NOT touch `internal/observability/replay/schema.go`.

### What NOT to build

- Do NOT touch `adjustments/assets.go` (= PR-2 work, settled). The 6 A-rule adapters/translators stay.
- Do NOT touch `adjustments/liabilities.go` (= PR-4).
- Do NOT delete `shimLedgerEntriesFromLegacy` or `shimLedgerEntriesFromLegacyExcluding` (= PR-4 — the liability-side branch still calls `shimLedgerEntriesFromLegacy`).
- Do NOT introduce `CleanedFinancialData {AsReported, Restated, InvestedCapital}` views (= Phase 3).
- Do NOT migrate any consumer of `data.NormalizedOperatingIncome` or `data.InterestExpense` to read from a view (= Phase 4).
- Do NOT compute SHA-256 `PromptHash` / `SourceDocHash` on `AIProvenance` (= Phase 3 per Q4).
- Do NOT modify `internal/services/datacleaner/recompute.go` — it's the regression signal (and earnings-side mutations are invisible to it by design).
- Do NOT bump `CurrentSchemaVersions["FinancialData"]` (stays at 8 from PR-2).
- Do NOT touch `internal/services/valuation/*` — Tier 2 territory.
- Do NOT regenerate the JPM/BAC/WFC DDM bit-for-bit golden fixtures — they pin a load-bearing invariant.
- Do NOT modify the `Adjuster` interface or the entity field shapes — PR-1 settled them; Phase 3 reads them by name.

---

## Critical invariants (PR-3 must preserve)

1. **Bit-for-bit DDM legacy path:** `TestDDM_LegacyPath_BitForBit` (jpm/bac/wfc) GREEN at every commit. Trivial in PR-3 — DDM doesn't read earnings-normalization fields. Run after every commit anyway.
2. **Shadow snapshot byte-identity:** `internal/integration/testdata/recompute-shadow/<TICKER>.json` UNCHANGED for all 10 tickers. C-rule migration is balance-sheet-orthogonal so `recomputeUmbrellas` divergence pattern is invariant by construction. Run `TestDataCleanerRecompute_ShadowMode_TickerBasket` after each commit and `git diff --quiet internal/integration/testdata/recompute-shadow/` must exit 0.
3. **Phase 1 NoMutation invariant:** `TestRecomputeUmbrellas_NoMutation` GREEN at every commit.
4. **Dual-write discipline:** every migrated C-rule MUST keep the existing `data.NormalizedOperatingIncome ±= X` (or `data.InterestExpense += Y`) mutation alongside the new `LedgerEntry` emission. C4 and C7 have no mutation today; preserve "no mutation" for them too (FlagEmitter convention). Phase 3 deletes the dual-writes; Phase 2 leaves them in place.
5. **Ledger ordering invariant:** `TestOrchestrator_LedgerOrdering` GREEN. **Asset → Liability → Earnings partition.** PR-3's C-rules must emit AFTER the (A-rule native LedgerEntries) AND AFTER the (B-rule legacy shim LedgerEntries — still present until PR-4). The dispatcher in `ProcessEarningsAdjustments` (parallel to PR-2's `ProcessAssetAdjustments`) appends to `result.NativeLedgerEntries` / `result.NativeOverlays` / `result.NativelyEmittedRuleIDs`; `service.go::applyActiveAdjustments` drains them into `data.AdjustmentLedger` / `data.Overlays` in the existing assets → liabilities → earnings call order. Task 3.8 deletes the post-call earnings shim invocation.
6. **PR-1 entity field shapes are frozen.** Do NOT add/remove/rename fields on `LedgerEntry`, `OverlaySpec`, `AdjustmentLedger`, `AmountSemantics`, or `AIProvenance`. Phase 3 ARCH consumes these by name.

---

## Gotchas inherited from PR-2 (the agent will trip over these without warning)

1. **Worktree discipline.** `pwd` + `git rev-parse --abbrev-ref HEAD` + `git worktree list` before EVERY commit. Per-PR worktrees eliminate the parallel-session HEAD-contamination failure mode that hit PR-1; verify anyway.

2. **CRLF / LF noise on shadow snapshots is OK.** `git status` / `git diff` may render CRLF↔LF warnings on `internal/integration/testdata/recompute-shadow/*.json`; that's `core.autocrlf` cosmetic noise, not content drift. Use `git diff --quiet` (note `--quiet`) as the authoritative gate — exit 0 means byte-identical.

3. **Per-rule translator pattern stays.** PR-2 explicitly preserved 6 separate translators (`a1AdjusterOutputToLegacyResult`, `a2…`, `a4…`, `a5…`, plus 2 for the flag-only reviews) instead of extracting a generic `dispatchNativeAdjuster()` helper. The reasoning: per-rule structure is justified by role differences (Restater reads `LedgerEntry.DeltaAmount`, OverlayEmitter reads `OverlaySpec.Amount`, FlagEmitter always returns `Applied:false`). PR-3 has 7 C-rules (5 Restaters + 2 FlagEmitters); add 7 per-rule translators following the same pattern. Do NOT prematurely extract a helper. If a reviewer pushes back on duplication, point at PR-2's Task 2.1 code-quality review (deferred extraction; deemed YAGNI).

4. **`applyCtx` nil-context propagation is acceptable.** PR-2's `Apply*` methods ignore the `ctx` parameter (today's `ProcessAssetAdjustments` predates request-scoped logging in this layer). The interface signature carries `ctx context.Context` for forward-compatibility — Phase 3+ implementations may use it; PR-3 may ignore it. If go-vet complains about an unused parameter, suppress with `_ = ctx` or leave the parameter named and let lint pass on convention.

5. **Reasoning-string discipline (the PR-2 Task 2.1 NIT).** PR-2 Task 2.1's commit message overpromised "byte-identical legacy strings" for the A1 fired path; the actual code added an amount value to the string. The drift is intentional improvement but the commit message was inaccurate. **For PR-3: either preserve legacy `Reasoning` strings byte-identically across all 7 C-rules OR document the drift explicitly in the commit message.** Default to byte-identical unless the legacy string is genuinely broken; this minimizes REVIEWER NITs.

6. **C4 is FlagEmitter, not Restater (plan-vs-code disagreement).** The implementation plan §7 Task 3.1-3.7 says "C4 (stock-based comp): same pattern" implying Restater. The actual code at `earnings.go:244-295` doesn't mutate any `data` field — it just emits an `entities.Adjustment{Type: Reclassify}` and a Flag. Trust the code. Emit C4 as FlagEmitter (`Fired:false` LedgerEntry + dilution Flag in `AdjusterOutput.Flags`). Document the deviation in the commit message so REVIEWER knows the call was deliberate.

7. **C7 is FlagEmitter — confirm by reading `earnings.go:389+`.** No mutation; just `Flag` emission. Same convention as PR-2 Task 2.5's two asset-side reviews.

8. **C6 EquityOffset = 0 (special case).** Capitalized interest is a reclassification BETWEEN income-statement line items, not a real economic gain/loss. So while C6 IS a Restater (it mutates `data.InterestExpense`), its `EquityOffset` is 0 — the dollars don't flow to retained earnings. PR-3 must emit `EquityOffset: 0` and add a per-test assertion. Phase 3's `Restated()` accessor must NOT add C6's DeltaAmount to retained earnings; the EquityOffset field is the load-bearing carrier of "does this flow through equity?"

9. **C5's two mutation sites are NOT duplicated mutations — they're branch-divergent and both fire `data.NormalizedOperatingIncome -= adjustmentAmount` with the sign carried by `adjustmentAmount`.** Read the lines at :310-316 carefully. Net effect per fire: one mutation. PR-3 emits one LedgerEntry per fire. Do NOT emit two.

10. **PR-3 is the lowest-risk PR in the stack.** Use the agent-time budget on careful per-task validation rather than scope-creep. If you finish in under 1 shift, do not start PR-4. Hand back to HUMAN for PR-3 V-R-Q-merge approval before opening PR-4's worktree.

---

## Acceptance gates (run before every commit; full-suite before final PR-3 closeout)

```bash
# 1. Build
go build ./...

# 2. Adjuster-package unit tests (fast; new + legacy C-rule cases)
go test ./internal/services/datacleaner/adjustments/... -count=1

# 3. LOAD-BEARING bit-for-bit DDM invariant
go test ./internal/services/valuation/models/ -run TestDDM_LegacyPath_BitForBit -count=1

# 4. Phase 1 recompute invariants
go test ./internal/services/datacleaner/ -run 'TestOrchestrator_LedgerOrdering|TestRecomputeUmbrellas_NoMutation' -count=1

# 5. Phase 1 basket shadow test + byte-identity gate
go test ./internal/integration/... -run TestDataCleanerRecompute_ShadowMode_TickerBasket -count=1
git diff --quiet internal/integration/testdata/recompute-shadow/   # MUST exit 0

# 6. Full suite (skip if flaky datafetcher race re-fires; isolate-rerun until green)
go test ./... -count=1

# 7. Replay spot-check (final closeout only; not per-commit)
go run ./cmd/replay --from=parsed artifacts/tier2-baseline/2026-05-19/AAPL/req_<uuid>/
go run ./cmd/replay --from=parsed artifacts/tier2-baseline/2026-05-19/MSFT/req_<uuid>/
# Expected: zero numeric drift in 17-response.json's valuation_summary block.
# Expected: 10-clean-output.json's adjustment_ledger now contains C-rule entries (the success signal).
```

---

## PR-3 acceptance criteria

All gates GREEN before VERIFIER handoff:

- All Task 3.1–3.8 acceptance signals green (one per task — see §7 of the plan for per-task acceptance).
- `TestDDM_LegacyPath_BitForBit` GREEN at every commit.
- `TestRecomputeUmbrellas_NoMutation` GREEN.
- `TestOrchestrator_LedgerOrdering` GREEN (asset → liability → earnings partition preserved; C-rule entries appear in the earnings partition).
- Full `go test ./... -count=1` GREEN modulo documented pre-existing scheduler-race flake.
- `internal/integration/testdata/recompute-shadow/<TICKER>.json` byte-identical for all 10 tickers (`git diff --quiet` exit 0).
- AAPL + MSFT replay shows zero NUMERIC drift in `17-response.json`. `10-clean-output.json`'s `adjustment_ledger` now contains C-rule LedgerEntries (the per-PR success signal).
- Coverage ≥80% on migrated `earnings.go` methods (CLAUDE.md target; new C-rule tests should drive this naturally).
- Documentation updates land in PR-3 closeout: CLAUDE.md DC-1 Phase 2 PR-3 sub-bullet (under the existing PR-2 sub-bullet); spec changelog row; plan changelog row; DC-1 reviewer tracker progress paragraph.
- PR-4 handoff doc authored at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-pr-4-handoff.md` (copy-adapt from this file).

---

## Handoff to next phase

When PR-3 ships:
- Update CLAUDE.md DC-1 Phase 2 PR-3 SHIPPED sub-bullet.
- Append PR-3 spec changelog row.
- Append PR-3 plan changelog row.
- Append PR-3 progress paragraph to the DC-1 tracker.
- Author the PR-4 handoff doc — covers B1 (operating leases OverlayEmitter), B2 (pension/OPEB OverlayEmitter), B3 (contingent liabilities OverlayEmitter with `Field:"DebtLikeClaims"` + AIProvenance best-effort with empty hashes per Q4), orchestrator-level `data.TotalDebt += result.Amount` absorption at `liabilities.go:87-88` (Option α — keep the wrapper, move the mutation into the wrapper), final shim + helper deletion, the basket snapshot integration test (Task 4.6), the T2-BS-3 disposition documentation (Task 4.7), and the closing Phase 2 CLAUDE.md gotcha. PR-4 is the highest-risk PR — allocate extra verification budget; run all 3 replay tickers (AAPL/MSFT/JPM).
- Notify user. Wait for explicit go-ahead before starting PR-4.

## Change log

| Date | Change |
|---|---|
| 2026-05-22 | PR-3 handoff doc filed by orchestrator after PR-2 SHIPPED (7 commits on `dc1-phase-2-pr-2`, all load-bearing invariants GREEN; A-rule native migration + asset-side shim deletion + A-FY-NULL tracker). Anchored at branch `dc1-phase-2-pr-2` tip after Task 2.7. Q-resolutions carried through (Q1 SHIPPED in PR-1; Q2/Q4 DEFERRED to Phase 3; Q3 SHIPPED in PR-2 Task 2.7). Canonical pattern from PR-2 (mutation-FREE `Apply*` + dispatcher-owns-dual-write) is the inheritance contract for PR-3. |
