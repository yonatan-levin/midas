# DC-1 Phase 2 — Closeout Report

**Phase:** Phase 2 — `Adjuster` interface + `AdjustmentLedger` + `OverlaySpec` (cleaner-pipeline structural refactor)
**Status:** SHIPPED 2026-05-23
**Branch at close:** `dc1-phase-2-pr-4` (full 4-PR stack on stacked branches; awaiting final HUMAN merge to master)
**4-PR stack tip:** PR-1 `39cf0fa` → PR-2 `2e8f83b` → PR-3 `207f41a` → PR-4 final tip (after Task 4.7)
**Commits across the stack:** ~28 implementation commits + per-PR wrap-up doc commits
**Discovery path:**
- Tracker: [docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md](../../reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md)
- Spec: [datacleaner-component-primitive-and-parallel-views-spec.md](../spec/datacleaner-component-primitive-and-parallel-views-spec.md)
- Phase 0 closeout: [datacleaner-component-primitive-and-parallel-views-phase-0-closeout.md](datacleaner-component-primitive-and-parallel-views-phase-0-closeout.md)
- Phase 1 closeout: [datacleaner-component-primitive-and-parallel-views-phase-1-closeout.md](datacleaner-component-primitive-and-parallel-views-phase-1-closeout.md)
- Phase 1 shadow-analysis (the Phase 2 punch-list input): [datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md](datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md)
- Phase 2 implementation plan: [datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md](datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md)
- Phase 2 handoff: [datacleaner-component-primitive-and-parallel-views-phase-2-handoff.md](datacleaner-component-primitive-and-parallel-views-phase-2-handoff.md)
- Per-PR handoffs: [pr-2-handoff](datacleaner-component-primitive-and-parallel-views-phase-2-pr-2-handoff.md), [pr-3-handoff](datacleaner-component-primitive-and-parallel-views-phase-2-pr-3-handoff.md), [pr-4-handoff](datacleaner-component-primitive-and-parallel-views-phase-2-pr-4-handoff.md)

---

## What landed

A 4-PR stack on independent branches under the worktree-first workflow. PR-N branches stack on PR-(N-1) tips; final HUMAN merge collapses the stack onto master.

### PR-1 — Adjuster interface + entities + scaffolding shim

**Branch:** `dc1-phase-2-pr-1-clean` — tip `39cf0fa`

Introduces the unified contract:
- `Adjuster` interface at `internal/services/datacleaner/adjustments/adjuster.go` — single-method contract: `Apply(ctx, working, rule, cleaningCtx) (AdjusterOutput, error)` + `Name() string`. `AdjusterOutput` carries three slices: `LedgerEntries`, `Overlays`, `Flags`. Role flavors (Restater / OverlayEmitter / Hybrid / FlagEmitter) emerge from the **shape** of the returned output, not from a self-declared enum — orchestrator dispatch logic stays single-pass.
- New entities at `internal/core/entities/adjustment_ledger.go`: `LedgerEntry`, `OverlaySpec`, `AdjustmentLedger` (slice newtype), `AmountSemantics` enum (`Incremental` / `Replacement` / `Delta`), `AIProvenance` (best-effort hash + model metadata).
- `FinancialData` gains two fields: `AdjustmentLedger []LedgerEntry` and `Overlays []OverlaySpec` (positioned after the Phase 0 plug fields).
- Orchestrator scaffolding shim at `service.go::applyActiveAdjustments` — three contiguous branches (assets / liabilities / earnings) that mechanically translate the legacy `entities.Adjustment` shape into `LedgerEntry` records after each `Process*Adjustments` call. Shim branches are deletion order: PR-2 deletes assets, PR-3 deletes earnings, PR-4 deletes liabilities + the two shim helpers.
- `TestOrchestrator_LedgerOrdering` property test pins the asset → liability → earnings ledger partition.
- `recomputeUmbrellas` WARN line additively renders `recent_adjusters []string` field (last 5 AdjusterIDs from `fd.AdjustmentLedger`) per Q1 resolution. `TestRecomputeUmbrellas_NoMutation` invariant preserved.

**PR-1 invariant — opt-in observability only.** No production consumer reads `data.AdjustmentLedger` or `data.Overlays` yet (matches Phase 0 plug-field discipline). Dual-write mutations (`data.TotalAssets -= X`, `data.TotalDebt += Y`, etc.) remain unchanged; downstream DCF / WACC / Graham / EV-bridge outputs are bit-for-bit unchanged.

### PR-2 — 6 Category A asset adjusters + asset-shim deletion + SchemaVersion bump

**Branch:** `dc1-phase-2-pr-2` (stacked on PR-1) — tip `2e8f83b`

7 implementation commits + 1 wrap-up commit:
| Task | Commit | Scope |
|---|---|---|
| 2.1 | `4ca4b3c` | A1 goodwill_exclusion OverlayEmitter + SchemaVersion 7→8 (atomic with the first populating PR per `feedback_schema_version_atomic_bump` MEMORY rule) |
| 2.2 | `15a5798` | A2 intangible_writedown Restater (TaxShieldDTA=0 per Q2 deferral) |
| 2.3 | `79d3015` | A4 deferred_tax_assets Restater (TaxShieldDTA=0; A4 IS the DTA reduction itself) |
| 2.4 | `631bf72` | A5 obsolete_inventory Restater + TaxShieldDTA (`writedown × working.EffectiveTaxRate` when rate > 0) |
| 2.5 | `039b680` | 2 flag-only reviews (R&D capitalization, capitalized software) via FlagEmitter convention |
| 2.6 | `2c132aa` | Delete asset-side shim branch (A-rules fully native) |
| 2.7 | `df25866` | A-FY-NULL read-only investigation tracker at `docs/reviewer/DC-1-FY-enable-predicate-investigation.md` (Q3) |
| wrap-up | `039b680` → `2e8f83b` | Docs sweep: CLAUDE.md + spec/plan changelogs + DC-1 tracker progress + PR-3 handoff |

Four role flavors emerged from output shape:
- **OverlayEmitter** (A1): empty `Component`/`DeltaAmount`/`EquityOffset` on LedgerEntry; monetary delta lives on `OverlaySpec.Amount`.
- **Restater** (A2, A4): `LedgerEntry{Component:"X", DeltaAmount<0, EquityOffset=DeltaAmount}`.
- **Restater + TaxShieldDTA** (A5 only): populates `TaxShieldDTA` when EffectiveTaxRate > 0.
- **FlagEmitter** (2 reviews): `Fired:false` LedgerEntry with non-empty `AdjusterOutput.Flags` as the firing signal.

### PR-3 — 7 Category C earnings adjusters + earnings-shim deletion

**Branch:** `dc1-phase-2-pr-3` (stacked on PR-2) — tip `207f41a`

8 implementation commits + 1 wrap-up commit:
| Task | Commit | Scope |
|---|---|---|
| 3.1 | `b1af6b1` | C1 restructuring_charges Restater |
| 3.2 | `e621320` | C2 asset_sale_gains Restater |
| 3.3 | `988a371` | C3 litigation_settlements Restater |
| 3.5 | `5654464` | C5 derivative_gains_losses Restater (branch-divergent signed `DeltaAmount`) |
| 3.6 | `5610d51` | C6 capitalized_interest Restater — **LOAD-BEARING `EquityOffset=0`** for interest reclassification |
| 3.4 | `79b78bd` | C4 stock_compensation FlagEmitter (plan-vs-code disagreement resolved by trust-the-code: legacy code is Reclassify-only, not Restater) |
| 3.7 | `75afa8b` | C7 working_capital_window_dressing FlagEmitter |
| 3.8 | `4af3c33` | Delete earnings-side shim branch (mirrors PR-2 Task 2.6) |
| wrap-up | → `207f41a` | Docs sweep + PR-4 handoff |

Two role flavors used (already locked by PR-2): Restater + FlagEmitter. No new flavors.

C6's `EquityOffset=0` is the LOAD-BEARING special case: capitalized interest reclassifies between income-statement line items (operating expense → interest expense), does NOT flow to retained earnings, so Phase 3's `Restated()` accessor must NOT add C6's DeltaAmount to equity. Pinned by a dedicated subtest in `c6_capitalized_interest_adjuster_test.go` AND by the dispatcher test's `NativeC6Emission` subtest with an explicit failure-message naming the Phase 3 contract.

### PR-4 — 3 Category B liability adjusters + orchestrator absorption + shim+helper deletion + basket integration test + closeout docs

**Branch:** `dc1-phase-2-pr-4` (stacked on PR-3) — final tip after Task 4.7

7 implementation commits:
| Task | Commit | Scope |
|---|---|---|
| 4.1 | `613773a` | B1 operating_lease_capitalization OverlayEmitter (`Field:"TotalDebt"`) |
| 4.2 | `e328226` | B2 pension_underfunding OverlayEmitter (`Field:"TotalDebt"`) |
| 4.3 | `5c4e88d` | B3 contingent_liability OverlayEmitter (`Field:"DebtLikeClaims"` — Phase 4 routing intent) + best-effort `AIProvenance` (Q4 empty hashes) |
| 4.4 | `37f7400` | Orchestrator `data.TotalDebt += result.Amount` mutation absorbed into `ProcessLiabilityAdjustments`' per-rule switch arms (Option α) |
| 4.5 | `5eaadd3` | Delete liability-side shim branch + remove `shimLedgerEntriesFromLegacy` + `shimLedgerEntriesFromLegacyExcluding` helpers — **PR-1 shim FULLY gone** |
| 4.6 | `f2c5d0e` | `TestLedger_BasketSnapshot_ClusterPrediction` integration test (10/10 basket tickers PASS) |
| 4.7 | (this commit) | Phase 2 closeout docs sweep — CLAUDE.md consolidation + spec/plan changelogs + T2-BS-3 + THESIS/AGENTS/ARCHITECTURE/TESTING + NEW Phase 2 closeout doc (this file) |

PR-4 is the highest-risk PR because B-rules touch `TotalDebt`, which feeds the JPM/BAC/WFC DDM bit-for-bit invariant. The per-commit replay validation across all 7 commits confirms no numeric drift.

---

## Aggregate scope

| Surface | Count |
|---|---:|
| Cleaner-side adjusters now implementing `Adjuster` natively | 17 (6 A-rules + 7 C-rules + 3 B-rules + 1 PR-1 shim-deletion conceptual rule) |
| Role flavors observed | 4 (OverlayEmitter / Restater / Restater+TaxShieldDTA / FlagEmitter) |
| Stacked branches across the 4-PR stack | 4 |
| Implementation commits across the stack | ~28 |
| Lines of test code added (per-rule adapter tests + integration tests + closeout-doc updates) | ~5000+ across the stack |
| `SchemaVersion["FinancialData"]` bumps | 1 (7→8 atomic with PR-2 Task 2.1) |

---

## Architectural achievements

1. **`Adjuster` interface unification.** One contract; role flavors emerge from output shape rather than interface multiplication. The orchestrator dispatch stays single-pass; the per-rule adapter pattern (a small struct holding a pointer to the legacy `*Adjuster` plus an `Apply` method that delegates to the mutation-free `Apply*` on the legacy type) keeps the legacy `Process*Adjustments` switch tables intact during the migration window.
2. **Mutation-FREE Apply + dispatcher-owns-dual-write canonical pattern.** Every `Apply*` reads `working` and returns `AdjusterOutput` — never mutates. The dispatcher in `ProcessAssetAdjustments` / `ProcessEarningsAdjustments` / `ProcessLiabilityAdjustments` owns the dual-write: `capture original* → call Apply* → translate to legacy *AdjustmentResult via per-rule translator → mutate data.X → drain AdjusterOutput.LedgerEntries/Overlays/Flags into result's Native* slices + add the RuleID to NativelyEmittedRuleIDs`. Concentrating mutation at one call site per category means **Phase 3's "delete the dual-write" reduces to a single deletion per category** instead of N adjuster `Apply` methods.
3. **Native-drain via `NativelyEmittedRuleIDs` exclusion.** The cleaner orchestrator drains per-category `NativeLedgerEntries` and `NativeOverlays` directly onto `data.AdjustmentLedger` / `data.Overlays`. Rules whose `AdjusterID` appears in `result.NativelyEmittedRuleIDs` are excluded from the legacy-translation shim path (which was deleted entirely in PR-4 Task 4.5). The exclusion set is the bridge that let the asset/earnings/liability shim branches be deleted independently as their adjusters migrated.
4. **PR-1 shim FULL deletion across all 3 categories.** Asset shim deleted PR-2 Task 2.6 (`2c132aa`); earnings shim deleted PR-3 Task 3.8 (`4af3c33`); liability shim + both helper functions (`shimLedgerEntriesFromLegacy` + `shimLedgerEntriesFromLegacyExcluding`) deleted PR-4 Task 4.5 (`5eaadd3`). The PR-1 scaffolding has zero remaining callers as of Phase 2 close.
5. **Orchestrator absorption (Option α).** PR-4 Task 4.4 moved the orchestrator-level `data.TotalDebt += result.Amount` mutation from a wrapper-level loop into per-rule switch arms inside `ProcessLiabilityAdjustments`. The wrapper kept; the mutation moved. This canonizes the dispatcher-owns-dual-write pattern for the liability category and is the prerequisite for PR-4 Task 4.5's shim deletion.
6. **B3 substantive accuracy correction visible in output.** B3's `OverlaySpec.Field:"DebtLikeClaims"` records Phase 4's routing intent — contingent liabilities feed `InvestedCapital.WACCInputs.DebtLikeClaims`, NOT `Restated.TotalDebt` (Damodaran convention). Today's Phase 2 dual-write still mutates `data.TotalDebt` for legacy preservation; Phase 4's consumer migration flips the read site to consume `Overlays[Field:"DebtLikeClaims"]` and deletes the `data.TotalDebt` mutation for B3.

---

## Load-bearing invariants — green throughout

| Invariant | Pin | Status |
|---|---|---|
| `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC) | `internal/services/valuation/models/ddm_bitforbit_test.go` | GREEN through all 28 commits. Non-trivial in PR-4 because B-rules touch `TotalDebt`. Per-commit replay validation across PR-4's 7 commits confirms zero numeric drift. |
| `TestRecomputeUmbrellas_NoMutation` | `internal/services/datacleaner/recompute_test.go` | GREEN. `reflect.DeepEqual` snapshot pins the no-mutation contract. |
| `TestOrchestrator_LedgerOrdering` | `internal/services/datacleaner/ledger_invariants_test.go` | GREEN. Asset → liability → earnings partition preserved through all category migrations. |
| `TestDataCleanerRecompute_ShadowMode_TickerBasket` shadow snapshots | `internal/integration/testdata/recompute-shadow/<TICKER>.json` | **PR-4 empirically preserves shadow-snapshot byte-identity** — verified via `git diff --quiet dc1-phase-2-pr-3..dc1-phase-2-pr-4 -- internal/integration/testdata/recompute-shadow/` (exit 0) and direct inspection (no PR-4 commit touched the recompute-shadow directory). The original Phase 1 cluster classification predicted slight mutation because B-rules touch `data.TotalDebt` / `data.InterestBearingDebt`, but `recomputeUmbrellas` only recomputes balance-sheet asset/liability umbrellas (`TotalCurrentAssets`, `TotalNonCurrentAssets`, `TotalLiabilities`, etc.) — none of which the B-rule dual-write affects. The new `TestLedger_BasketSnapshot_ClusterPrediction` (PR-4 Task 4.6) complements the shadow-snapshot regime by asserting the dispatcher emits an `AdjusterID` per considered rule, which the snapshots cannot encode. |
| `TestLedger_BasketSnapshot_ClusterPrediction` (new in PR-4 Task 4.6) | `internal/integration/datacleaner_ledger_basket_test.go` | GREEN (10/10 basket tickers PASS). First integration test to read `data.AdjustmentLedger` directly. |
| Full test suite (`go test ./...`) | All packages | GREEN. |

---

## Q-resolutions

| Q | Resolution | Status | Cite |
|---|---|---|---|
| Q1 | `recompute` WARN `recent_adjusters` enrichment | SHIPPED in PR-1 | Plan §10 Q1 |
| Q2 | A2 `TaxShieldDTA` actual population | DEFERRED to Phase 3 | A2 ships with `TaxShieldDTA=0` to preserve dual-write bit-for-bit contract; pinned by `TestA2IntangibleAdjuster_Adjuster_Interface_Contract/fired_path_TaxShieldDTA_stays_zero_per_Q2_deferral`. Plan §10 Q2. |
| Q3 | A-FY-NULL tracker filing | SHIPPED in PR-2 Task 2.7 | `df25866` filed `docs/reviewer/DC-1-FY-enable-predicate-investigation.md` (HIGH-confidence NOT-A-BUG for Phase 2; heuristic fix punted to Phase 4+). |
| Q4 | `AIProvenance` SHA-256 hash field computation | DEFERRED to Phase 3 | B3 ships with `PromptHash`/`SourceDocHash`/`ExtractedSpan` empty; today's `ai.AnalyzeFootnote` call site does not compute them. TODO is in the entity godoc. Plan §10 Q4. |

---

## T2-BS-3 disposition

**Option B (carve-out) chosen by Phase 2 ARCH on 2026-05-19.** Parser fix DEFERRED.

The AMD/KO `TotalLiabilities==0` parser dropout (24 shadow-snapshot divergence records with `clamp_suspected: true`) is a parser-side bug surfaced by DC-1 Phase 1's shadow shim, NOT a cleaner-side issue. Phase 2 does not address it directly. Disposition:

- **`AsReported.TotalLiabilities` stays at 0 for AMD/KO** (Phase 2 honors source data faithfully).
- **Phase 3's `Restated` view reconstruction (from `sum(components) + plug`)** will produce a non-zero value, correctly surfacing the components-derived liability total for downstream consumption.
- **Tracker `docs/reviewer/T2-BS-3-parser-totalliabilities-zero-amd-ko.md` stays OPEN.** Reconsidered if (a) a future phase needs `AsReported.TotalLiabilities` to be truthful for AMD/KO, or (b) a separate parser-side initiative requests it.

---

## What's deferred to Phase 3

The architectural gate after Phase 2:

1. **CleanedFinancialData view reconstruction.** Three-view output (`AsReported`/`Restated`/`InvestedCapital`) with lazy accessors that consume `data.AdjustmentLedger` + `data.Overlays`:
   - `AsReported`: identity view; matches the SEC-parser output before any cleaner mutation.
   - `Restated`: applies `LedgerEntry` records (Component + DeltaAmount + EquityOffset + TaxShieldDTA) — equity offsets flow through retained earnings. The Restated view is the regression sentinel for the C6 `EquityOffset=0` special case (the accessor MUST NOT add C6's DeltaAmount to equity).
   - `InvestedCapital`: applies `OverlaySpec` records — `Field:"DebtLikeClaims"` overlays feed `WACCInputs.DebtLikeClaims`, NOT `Restated.TotalDebt`.
2. **A2 `TaxShieldDTA` actual population (Q2).** When the Restated accessor lands, A2's tax shield can be computed and consumed without breaking the A2 dual-write bit-for-bit contract.
3. **`AIProvenance` SHA-256 hash computation (Q4).** The hash trail needs the `ai.AnalyzeFootnote` call site to compute deterministic SHA-256 over the prompt + source-doc + extracted span; Phase 3 wires it through.
4. **`ctx` threading through `ProcessLiabilityAdjustments` signature.** The B3 AI path today reads `cleaningCtx.FootnoteText`; Phase 3 should consider whether `ProcessLiabilityAdjustments` and siblings should accept `context.Context` explicitly for cancellation + tracing propagation. Out of scope for Phase 2; documented for Phase 3 design.
5. **Per-rule translator extraction.** Each role flavor today has its own translator function (`a1AdjusterOutputToLegacyResult`, `c1AdjusterOutputToLegacyResult`, etc.). Extraction to a generic helper was DEFERRED throughout PR-2, PR-3, and PR-4 per YAGNI — per-rule structure is justified by role differences (Restater reads `LedgerEntry.DeltaAmount` magnitude, OverlayEmitter reads `OverlaySpec.Amount`, flag-only translators always return `Applied:false`). Phase 3 may revisit if a fifth role flavor lands.
6. **C3/C5/C6 percentage `Revenue > 0` guard convention.** C3/C5/C6 add a `Revenue > 0` percentage guard that diverges from legacy's NaN/+Inf on Revenue=0 tickers — an intentional defensive guard documented per-commit. It is NOT unified into a helper; Phase 3 reviewers should decide whether to extract or leave per-rule.

## What's deferred to Phase 4

The consumer-migration gate after Phase 3:

1. **Consumer migration of 13 valuation read sites.** Every consumer of `*FinancialData` post-clean reads from the semantically-correct view: NCAV → `AsReported.CurrentAssets` + `AsReported.TotalLiabilities`; tangible book equity per share → `Restated.TangibleAssets` + `Restated.StockholdersEquity`; DCF / WACC / revenue-multiple → `InvestedCapital.*`. The 13 sites are enumerated in the spec's "consumer migration" section.
2. **B3 routing flip.** Consumer reads `Overlays[Field:"DebtLikeClaims"]` instead of `data.TotalDebt` for the contingent-liability component. Today's Phase 2 dual-write at the dispatcher level continues to populate `data.TotalDebt` for legacy preservation; Phase 4 deletes that mutation and updates the WACC consumer.
3. **Dual-write deletion.** The actual `data.X += Y` mutations in each dispatcher's per-rule switch arm get deleted. After Phase 4, the cleaner is purely additive (writes to `AdjustmentLedger` + `Overlays`); the views are the source of truth for downstream consumption.

---

## Next steps

1. **Phase 3 ARCH spec authoring** — convert the deferred items above into an implementation plan. Anchor on the spec's "view reconstruction" section. **DONE 2026-05-23** — Phase 3 spec + implementer plan filed; see `datacleaner-component-primitive-and-parallel-views-phase-3-spec.md` + `datacleaner-component-primitive-and-parallel-views-phase-3-implementation-plan.md`. Phase 3 BACKEND dispatch is the immediate next action.
2. **Watch the shadow snapshots in every Phase 3+ PR.** Recording-not-asserting policy continues: snapshot diff is the diff-review signal; reviewers must read the snapshot changes intentionally. The `TestLedger_BasketSnapshot_ClusterPrediction` integration test (PR-4 Task 4.6) is the structural-regression sentinel.
3. **Phase 3 worktree workflow** — start a new worktree at `midas-dc1-phase-3/` per the worktree-first MEMORY rule; do NOT touch master.

---

## Change log

| Date | Change |
|------|--------|
| 2026-05-23 | Initial filing. Anchored at branch `dc1-phase-2-pr-4` final tip (after Task 4.7 commits). Source: PR-1 `39cf0fa`, PR-2 `2e8f83b`, PR-3 `207f41a`, PR-4 commits 4.1-4.7. Phase 3 gate verdict: SATISFIED (architecturally — view reconstruction is now scoped, no blockers remain). |
