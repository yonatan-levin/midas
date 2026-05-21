# DC-1 Phase 2 PR-2 — Implementer Handoff

**Phase:** Phase 2, PR-2 of 4 — Asset adjusters migrated to the `Adjuster` interface (Tasks 2.1–2.7)
**Status:** READY TO START
**Estimated effort:** ~1 agent shift (see [feedback-ai-agent-time-estimates](../../../.claude/projects/.../memory/feedback_ai_agent_time_estimates.md) — translate human-day estimates to agent-hour pacing)
**Branch base:** `dc1-phase-2-pr-1-clean` at `9d8321c` (PR-1 follow-up commit). **DO NOT branch from master.** PR-2/3/4 are stacked on `dc1-phase-2-pr-1-clean` per the user's PR-strategy choice (no master merge until all 4 PRs land).
**Master state (FYI only — do NOT integrate yet):** `67a2bae` (Tier 2 closeout). Master has advanced 7 commits beyond PR-1's base `987ec31`; this is independent work and is irrelevant until the final 4-PR stack merge.

---

## TL;DR

PR-1 introduced the `Adjuster` interface, the `LedgerEntry` / `OverlaySpec` / `AdjustmentLedger` entities, and an intermediate **shim** in `service.go::applyActiveAdjustments` that maps the legacy `entities.Adjustment` records emitted by the existing `ProcessXAdjustments` methods into `LedgerEntry`s on `data.AdjustmentLedger`. PR-2 deletes the asset-side branch of that shim by **migrating every asset adjuster** in `adjustments/assets.go` to implement the new `Adjuster` interface natively — emitting `AdjusterOutput` instead of `entities.Adjustment`.

PR-2 also ships **the Phase 2 first-populating SchemaVersion bump** (`CurrentSchemaVersions["FinancialData"]: 7 → 8`) atomic with the first migrated adjuster, and refreshes `artifacts/tier2-baseline/*/` baselines so the replay tool stays diagnostic. Per Q3 resolution, PR-2 also produces a **read-only investigation tracker** for the Phase 1 shadow-analysis Cluster A-FY-NULL finding (no code changes from that task — Task 2.7 deliverable is a markdown tracker only).

**The success signal:** `internal/integration/testdata/recompute-shadow/<TICKER>.json` snapshots **remain UNCHANGED** for PR-2 (dual-write discipline preserves downstream behavior). The Tier 2 DDM bit-for-bit test stays GREEN at every commit. Replay drift on AAPL/MSFT bundles stays zero on numeric fields (structural fields can change ONLY in the SchemaVersion-bump commit and ONLY in the documented way).

---

## Required reading (in order)

### Tier 1 — Identity & continuity

1. **`CLAUDE.md`** — project conventions. Especially the **DC-1 Phase 2 PR-1 SHIPPED** sub-bullet under the existing DC-1 gotcha (PR-1's CLAUDE.md commit added it; read what's there now, NOT what's in your training data).
2. **`AGENTS.md`** — Tier 4 row for DC-1.
3. **`docs/THESIS.md`** — DC-1 row (Phase 2 in-flight, PR-1 SHIPPED).

### Tier 2 — Phase 2 design + PR-1 ground truth

4. **`docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md`** — the **authoritative Phase 2 plan.** Focus on:
   - §3 (Adjuster interface design) — PR-1 implemented this; PR-2 consumes it. Field shapes are LOAD-BEARING; do NOT change them.
   - §5 (test strategy + invariants)
   - **§7 PR-2 Tasks 2.1–2.7** — your task list, in execution order
   - **§10 RESOLVED** — Q1/Q3/Q4 dispositions you must honor:
     - Q1: Task 1.6 already shipped in PR-1 (recompute WARN `recent_adjusters` field). Do NOT modify.
     - Q3: Task 2.7 is in scope for THIS PR — produces `docs/reviewer/DC-1-FY-enable-predicate-investigation.md` tracker, NOT code.
     - Q4: AIProvenance hash fields stay empty in Phase 2; do NOT compute SHA-256 hashes in PR-2 (defers to Phase 3).
   - **PR-2 acceptance criteria** at end of §7 — note the SchemaVersion=8 bump requirement.
5. **`docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`** — focus on "Adjuster output" and "AdjustmentLedger" sections (PR-1 implemented these; you must read what's there to use the contract correctly).
6. **`docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md`** — the 7-cluster punch list. PR-2 migrates the **asset adjusters** that drive Clusters A1 (goodwill), A2 (intangibles), A4 (DTA), A5 (inventory). Re-read these cluster sections specifically.
7. **`docs/reviewer/T2-BS-3-parser-totalliabilities-zero-amd-ko.md`** — disposition is **Option B** (carve-out via Restated view, no parser fix). Tracker stays OPEN through Phase 3. Do NOT attempt to fix the parser.

### Tier 3 — PR-1's deliverables (read the actual code that's now on the branch)

8. **`internal/core/entities/adjustment_ledger.go`** — the entity definitions. `LedgerEntry`, `OverlaySpec`, `AdjustmentLedger`, `AmountSemantics`, `AIProvenance`. PR-2's adjusters emit these.
9. **`internal/services/datacleaner/adjustments/adjuster.go`** — the `Adjuster` interface and `AdjusterOutput` struct. PR-2's migrated adjusters implement this interface.
10. **`internal/services/datacleaner/service.go::applyActiveAdjustments`** — the orchestrator. PR-1's intermediate shim (`shimLedgerEntriesFromLegacy`) lives here. PR-2 deletes the asset-side branch of the shim as adjusters migrate to native emission. The TODO inside the helper flags PR-4's liability-overlay sign asymmetry; you can ignore it for PR-2 (it's PR-4's concern).
11. **`internal/services/datacleaner/recompute.go`** — Phase 1's shim with PR-1's `recent_adjusters` enrichment. **DO NOT MODIFY in PR-2.** The WARN+snapshot machinery is PR-2's regression signal.
12. **`internal/services/datacleaner/adjustments/assets.go`** — your PR-2 refactor target. Read each adjuster end-to-end:
    - A1 goodwill exclusion (lines ~38–109): expected role **Overlay** per spec, but plan §7 Task 2.1 says emit `OverlaySpec{Field:"TotalAssets", Operation:"subtract", ...}` with continued dual-write mutation
    - A2 intangible writedown (lines ~112–194): expected role **Restater** (emits `LedgerEntry` with `EquityOffset`)
    - A4 DTA valuation allowance (lines ~271–349): **Restater**
    - A5 inventory writedown (lines ~196–269): **Restater + TaxShieldDTA** (plan §7 Task 2.4 — populate `LedgerEntry.TaxShieldDTA = writedownAmount * data.EffectiveTaxRate` when tax rate > 0)
    - Flag-only reviews: `ProcessRDCapitalizationReview` (lines ~412+) and `ProcessCapitalizedSoftwareReview` — emit `Fired:false` ledger entries for observability per plan §7 Task 2.5.

### Tier 4 — Replay tooling + schema versioning

13. **`internal/observability/replay/schema.go`** — the `CurrentSchemaVersions` map. **You will bump `"FinancialData": 8` in PR-2 atomically with the first asset-adjuster migration commit.** This is per QA's risk-surface finding from PR-1 (see plan §7 PR-2 acceptance criteria).
14. **`cmd/replay/main.go`** — the replay CLI. Use `go run ./cmd/replay artifacts/tier2-baseline/2026-05-19/AAPL/` to verify zero numeric drift after each commit. **Note:** the tier2-baseline directory contains 10 tickers but does NOT contain JPM (which the original plan referenced); use AAPL + MSFT + AMD for spot-checks.

---

## PR-2 scope

### Tasks (all 7 must land in this PR — see plan §7 for full sub-steps)

| # | Task | File(s) | Role | Notes |
|---|------|---------|------|-------|
| 2.1 | Refactor A1 goodwill exclusion to `Adjuster` | `adjustments/assets.go`, `assets_test.go` | Overlay | Emit `OverlaySpec{Field:"TotalAssets", Operation:"subtract", Amount:originalGoodwill}`. Dual-write: keep `data.Goodwill=0` + `data.TotalAssets -= originalGoodwill`. |
| 2.2 | Refactor A2 intangible writedown to `Adjuster` | `adjustments/assets.go`, `assets_test.go` | Restater | Emit `LedgerEntry{Component:"OtherIntangibles", DeltaAmount:-writedownAmount, EquityOffset:-writedownAmount}`. Dual-write preserved. |
| 2.3 | Refactor A4 DTA valuation allowance to `Adjuster` | `adjustments/assets.go`, `assets_test.go` | Restater | Emit `LedgerEntry{Component:"DeferredTaxAssets", DeltaAmount:-valuationAllowance, EquityOffset:-valuationAllowance}`. |
| 2.4 | Refactor A5 inventory writedown to `Adjuster` + **TaxShieldDTA** | `adjustments/assets.go`, `assets_test.go` | Restater + TaxShieldDTA | Per plan §7 Task 2.4: `TaxShieldDTA: writedownAmount * data.EffectiveTaxRate` when tax rate > 0, else 0. Add regression test `TestProcessInventoryAdjustment_TaxShieldDTA_PopulatedWhenEffectiveTaxRateNonZero`. |
| 2.5 | Refactor flag-only review functions to emit ledger entries | `adjustments/assets.go`, `assets_test.go` | Flag-only (Fired:false) | Even though they don't mutate balance sheet, record their consideration in ledger per spec line 120. |
| 2.6 | Delete the PR-1 shim's asset-side branch | `service.go::shimLedgerEntriesFromLegacy` (or wherever Task 1.3 placed it) | — | Only delete the asset-category branch. Keep liability + earnings shim active for PR-3/PR-4. Add a comment noting "PR-3 deletes earnings branch; PR-4 deletes liability branch + the helper itself." |
| 2.7 | Cluster A-FY-NULL investigation (per Q3 resolution) | `docs/reviewer/DC-1-FY-enable-predicate-investigation.md` (NEW) | — | Read-only investigation. Two hypotheses to confirm/refute: (a) rule-engine enable-predicate gates A-rules on quarterly periods only; (b) SEC parser's FY collapse strips inputs. Produce a markdown tracker with conclusion + evidence. **NO CODE CHANGES from this task.** |

### SchemaVersion bump (must land atomically with Task 2.1's first migration commit)

- File: `internal/observability/replay/schema.go`
- Change: `CurrentSchemaVersions["FinancialData"]` from `7` to `8`.
- Also: refresh all bundles under `artifacts/tier2-baseline/2026-05-19/*/15-valuation.json` etc. via the appropriate capture command (check `cmd/replay --help` or existing scripts for the refresh path).
- Why: PR-1's `omitempty` JSON tags meant empty-ledger bundles stayed byte-identical to pre-PR-1. The moment PR-2 lands and adjusters populate the ledger fields, captured bundles diverge structurally. The SchemaVersion bump signals "structural delta intentional, separate from valuation math" so replay drift output stays diagnostic.
- See: `feedback_schema_version_atomic_bump.md` memory file.

### What to NOT build

- Do NOT touch `adjustments/earnings.go` (= PR-3).
- Do NOT touch `adjustments/liabilities.go` (= PR-4).
- Do NOT introduce `CleanedFinancialData {AsReported, Restated, InvestedCapital}` views (= Phase 3).
- Do NOT migrate any consumer of `data.TotalAssets` to read from a view (= Phase 4).
- Do NOT compute SHA-256 `PromptHash` / `SourceDocHash` on `AIProvenance` (= Phase 3 per Q4).
- Do NOT modify `internal/services/datacleaner/recompute.go` — it's the regression signal.
- Do NOT modify the PR-1 commits or rebase the branch (history is settled).
- Do NOT touch `internal/services/valuation/*` — Tier 2 territory (PR-2 lands BEFORE final master-integration, so Tier 2 drift is irrelevant to PR-2's local gates).
- Do NOT modify the `Adjuster` interface or the entity field shapes — PR-1 settled them; Phase 3 reads them by name.

---

## Critical invariants (PR-2 must preserve)

1. **Bit-for-bit DDM legacy path:** `TestDDM_LegacyPath_BitForBit` (jpm/bac/wfc) GREEN at every commit. Never update goldens to make it pass; revert if it fails.
2. **Shadow snapshot byte-identity:** `internal/integration/testdata/recompute-shadow/<TICKER>.json` UNCHANGED for all 10 tickers. Run `TestDataCleanerRecompute_ShadowMode_TickerBasket` after each commit and `git diff --quiet internal/integration/testdata/recompute-shadow/` must exit 0.
3. **Phase 1 NoMutation invariant:** `TestRecomputeUmbrellas_NoMutation` GREEN at every commit. Do not mutate state inside the recompute shim.
4. **Dual-write discipline:** every migrated adjuster MUST keep the existing `data.TotalAssets -= X` (or `data.Inventory -= Y`, etc.) mutation alongside the new `LedgerEntry`/`OverlaySpec` emission. PR-2 ships zero downstream behavior change — Phase 3 deletes the dual-write when consumers read views.
5. **Ledger ordering invariant:** `TestOrchestrator_LedgerOrdering` GREEN. Asset adjusters MUST appear first in `data.AdjustmentLedger`, in `rulesEngine.GetIndustryRules` order.
6. **PR-1 entity field shapes are frozen.** Do NOT add/remove/rename fields on `LedgerEntry`, `OverlaySpec`, `AdjustmentLedger`, `AmountSemantics`, or `AIProvenance`. Phase 3 ARCH consumes these by name.

---

## Gotchas from PR-1 (lessons the next agent will trip over otherwise)

1. **Bash branch-switch friction.** The sandboxed Bash tool sometimes resets HEAD to `master` after a command body completes (visible in the previous BACKEND's handoff under "Issues / Blockers" section 3, and observed during PR-1 orchestration). **Mitigation:** before EVERY `git commit`, run `git rev-parse --abbrev-ref HEAD` and confirm it prints the expected branch. If it prints `master` or anything else, run `git checkout dc1-phase-2-pr-1-clean` (or the active PR-2 branch) BEFORE committing.

2. **Parallel-session contamination is real.** During PR-1, a parallel agent session ran `git merge tier2-p2` and `git merge tier2-p3` on the in-flight `dc1-phase-2-pr-1` branch, requiring a cherry-pick recovery onto a clean branch. **Mitigation:** if you observe unexpected commits on your branch (commits you didn't author appearing in `git log`), STOP IMMEDIATELY and hand back to HUMAN. Do not "work around" the contamination by continuing on the polluted branch.

3. **Lint-diagnostic noise is mostly false positives.** Pre-existing `unused parameter` warnings on `applyActiveAdjustments(ctx, ...)` are intentional — the `ctx` is threaded through for PR-2+ adjuster migrations (they take `ctx` in `Adjuster.Apply`). Do NOT "fix" by removing the parameter. Other noise: `interface{}→any` modernization, `if N>len{}` → `max()`, "unused write" on table-driven test fixture fields. Ignore unless they hide a real bug.

4. **The `tier2-baseline/2026-05-19/` directory contains 10 tickers, NOT 11.** AAPL/AMD/BABA/EQIX/F/JNJ/KO/MSFT/MXL/TSM are present; JPM is NOT. PR-1's prompts referenced a JPM bundle that doesn't exist; substitute AAPL+MSFT for spot-checks or use `--allow-schema-drift` if the bundle has a missing-file edge case.

5. **CRLF-LF warnings on shadow snapshots are NOT actual content drift.** `git diff --quiet` (with the `--quiet` flag) gives the authoritative answer — exit 0 means byte-identical. The CRLF warnings printed by `git status` / `git diff` come from `core.autocrlf` config noise, not real changes. Use `--quiet` + exit code as the gate, not text output.

6. **Master has advanced 7 commits during PR-1.** Master is at `67a2bae` (Tier 2 closeout). The PR-2 branch base is `dc1-phase-2-pr-1-clean`, NOT current master. Do NOT rebase onto master until the final 4-PR stack merge. Per the user's PR-strategy choice, PR-2/3/4 all stack onto `dc1-phase-2-pr-1-clean`.

---

## PR-2 acceptance criteria

All gates GREEN before VERIFIER handoff:

- All Task 2.1–2.7 acceptance signals green (Task 2.7 produces a tracker, not code — VERIFIER inspects the markdown file's existence + minimum content)
- `TestDDM_LegacyPath_BitForBit` GREEN
- `TestRecomputeUmbrellas_NoMutation` GREEN
- `TestOrchestrator_LedgerOrdering` GREEN (asset entries still first; new entries from migrated adjusters are sourced from `AdjusterOutput`, not the shim)
- Full `go test ./... -count=1` GREEN modulo pre-existing flakes (scheduler test race noted in PR-1's QA report)
- `internal/integration/testdata/recompute-shadow/<TICKER>.json` byte-identical for all 10 tickers (`git diff --quiet` exit 0)
- AAPL + MSFT replay shows zero NUMERIC drift in `17-response.json`; `10-clean-output.json` carries A-rule entries in `adjustment_ledger`
- Structural schema drift IS expected in this PR (SchemaVersion 7 → 8); the replay tool should report it cleanly as a schema bump, not a math regression
- `artifacts/tier2-baseline/*/` baselines refreshed atomically with the SchemaVersion bump commit
- Coverage ≥80% on migrated adjuster.go methods (CLAUDE.md target)
- Documentation updates: CLAUDE.md DC-1 Phase 2 PR-2 sub-bullet; spec changelog row; plan changelog row; DC-1 reviewer tracker progress paragraph

---

## Handoff to next phase

When PR-2 ships:
- Update task tracker (PR-2 complete; PR-3 ready).
- Author PR-3 handoff doc (this template at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-pr-3-handoff.md` — copy-adapt from this file).
- Notify user. Wait for explicit go-ahead before starting PR-3.

## Change log

| Date | Change |
|---|---|
| 2026-05-22 | PR-2 handoff doc filed by orchestrator after PR-1 SHIPPED + signed off (HUMAN: stack-onto-PR-1 strategy + Q1/Q3/Q4 resolutions + SchemaVersion=8-in-PR-2 decision). Anchored at branch `dc1-phase-2-pr-1-clean` @ `9d8321c`. |
