# DC-1 Phase 3 — Implementation Plan (View Reconstruction)

**Phase:** Phase 3 of the DC-1 refactor sequence — `CleanedFinancialData` view accessors + Q2 + Q4 resolutions + `ctx` threading
**Status:** READY FOR BACKEND DISPATCH (authored 2026-05-23)
**Estimated effort:** 1–2 agent shifts (single PR recommended; Option B 2-PR split if PR-1 review surfaces design feedback)
**Branch base:** `dc1-phase-2-pr-4` final tip (post Task 4.7) OR master after HUMAN merge of the Phase 2 4-PR stack. Implementer's choice.

**Spec:** [datacleaner-component-primitive-and-parallel-views-phase-3-spec.md](../spec/datacleaner-component-primitive-and-parallel-views-phase-3-spec.md)
**Parent spec:** [datacleaner-component-primitive-and-parallel-views-spec.md](../spec/datacleaner-component-primitive-and-parallel-views-spec.md)
**Phase 2 closeout:** [datacleaner-component-primitive-and-parallel-views-phase-2-closeout.md](datacleaner-component-primitive-and-parallel-views-phase-2-closeout.md)

---

## Worktree workflow (REQUIRED)

Per `feedback_worktree_first_workflow` MEMORY rule: the main `midas/` directory MUST stay on `master`. Phase 3 work happens in a sibling worktree:

```
# From the main midas/ directory (on master)
git worktree add ../midas-dc1-phase-3 -b dc1-phase-3 <base-ref>
cd ../midas-dc1-phase-3
```

Where `<base-ref>` is either `dc1-phase-2-pr-4` (stack on PR-4 tip) or `master` (start clean after HUMAN merge). Confirm before EVERY `git commit`:
- `pwd` should end with `midas-dc1-phase-3`
- `git rev-parse --abbrev-ref HEAD` should print `dc1-phase-3`
- `git worktree list` should show your worktree alongside the main one.

If anything looks wrong, STOP and re-check. The bash-branch-switch friction that hit PR-1 is solved by worktrees as long as you stay in your branch's worktree.

---

## Required reading (in order)

### Tier 1 — Identity & continuity
1. **`CLAUDE.md`** — DC-1 gotcha (Phase 2 SHIPPED consolidation).
2. **`AGENTS.md`** — Tier 4 row 17b (DC-1 entry; Phase 3 spec reference appended).
3. **`docs/THESIS.md`** — DC-1 row (Phase 2 closed, Phase 3 spec authored).

### Tier 2 — Phase 3 spec + Phase 2 ground truth
4. **`docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-3-spec.md`** — the authoritative spec. Focus on §4 (Architecture), §5 (Q-resolutions), §8 (Testing strategy), §10 (Phase 3 → Phase 4 gate).
5. **`docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-2-closeout.md`** — what landed in Phase 2; especially "Q-resolutions" + "What's deferred to Phase 3" sections.
6. **`docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`** — the parent spec. Re-read §"Phasing & implementation sequence" row "Phase 3" + §"Consumer migration map" (Phase 4's input, NOT in scope for Phase 3 but useful context).

### Tier 3 — Phase 2 deliverables (the code Phase 3 builds on)
7. **`internal/core/entities/adjustment_ledger.go`** — `LedgerEntry`, `OverlaySpec`, `AdjustmentLedger`, `AmountSemantics`, `AIProvenance`. Phase 3 reads these by field name; do NOT change them.
8. **`internal/core/entities/financial_data.go`** — Lines ~105-160 are the Phase 0+1+2 additions: `Other*Assets`/`Other*Liabilities` plug fields, `AdjustmentLedger`, `Overlays`. Phase 3's `Restated()` accessor recomputes umbrellas from these components.
9. **`internal/services/datacleaner/service.go`** — focus on `Clean(ctx, ...)` (Phase 3 adds `CleanWithViews(ctx, ...)` sibling) and `applyActiveAdjustments(ctx, ...)` (Phase 3 updates the call signatures it dispatches to).
10. **`internal/services/datacleaner/adjustments/liabilities.go`** — `ProcessLiabilityAdjustments`. Phase 3 adds `ctx context.Context` as the first parameter.
11. **`internal/services/datacleaner/adjustments/assets.go`** — `ProcessAssetAdjustments` + A2 + A5 implementations. Phase 3 adds ctx parameter; A2 starts populating `TaxShieldDTA`.
12. **`internal/services/datacleaner/adjustments/earnings.go`** — `ProcessEarningsAdjustments`. Phase 3 adds ctx parameter (symmetric with liabilities/assets).
13. **`internal/services/datacleaner/recompute.go`** — `recomputeUmbrellas` shadow shim. **DO NOT MODIFY in Phase 3.** Read it to understand the recompute algorithm the `Restated()` accessor mirrors.
14. **`internal/observability/replay/schema.go`** — `CurrentSchemaVersions` map. Phase 3 bumps `"FinancialData": 8` to `9` atomic with Q4's first populating commit.

### Tier 4 — Phase 2 handoff templates (shape Phase 3 follows)
15. **`docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-2-pr-4-handoff.md`** — most recent Phase 2 handoff. Template for any Phase 3 PR-N handoff doc.

---

## Tasks

### Option A (recommended): single-PR breakdown

| # | Task | Files touched | Acceptance signal |
|---|---|---|---|
| 3.1 | Create `cleaneddata` package + `CleanedFinancialData` struct + `New` constructor + `FinancialDataView` DTO + `ViewKind` enum | NEW: `internal/services/datacleaner/cleaneddata/cleaned.go`, `view.go` | `go build ./...` exit 0; package compiles |
| 3.2 | Implement `AsReported()` accessor (identity copy from `raw`) | NEW: `internal/services/datacleaner/cleaneddata/asreported.go`; test in `asreported_test.go` | `TestAsReported_IdentityCopy` GREEN |
| 3.3 | Implement `Restated()` accessor (sum-of-components + plug + LedgerEntry.EquityOffset + LedgerEntry.TaxShieldDTA, with C6 EquityOffset=0 LOAD-BEARING) | NEW: `internal/services/datacleaner/cleaneddata/restate.go`; test in `restate_test.go` | `TestCleanedFinancialData_Restated_BitForBitOnNoFiredAdjusters` + `TestCleanedFinancialData_Restated_C6EquityOffsetZero` GREEN |
| 3.4 | Implement `InvestedCapital()` accessor (Restated + OverlaySpec entries: B1+B2+B3 → DebtLikeClaims; A1 → TotalAssets subtract + Goodwill=0) | NEW: `internal/services/datacleaner/cleaneddata/invested_capital.go`; test in `invested_capital_test.go` | `TestCleanedFinancialData_InvestedCapital_AppliesOverlays` GREEN |
| 3.5 | Add `Service.CleanWithViews(ctx, ...)` sibling to `service.go` | `internal/services/datacleaner/service.go` (add ~10 LOC method; no existing method changes) | `TestCleanWithViews_ReturnsWrapper` GREEN |
| 3.6 | T2-BS-3 carve-out reconstruction in `Restated()` for AMD/KO `AsReported.TotalLiabilities=0` (component-sum recompute IS the source of truth in Restated; no special branch needed — the recompute algorithm naturally fixes it) | `internal/services/datacleaner/cleaneddata/restate.go` (already covered by 3.3); test in `t2bs3_test.go` | `TestCleanedFinancialData_AsReported_PreservesParserZeros_AMD_KO` GREEN — `AsReported().TotalLiabilities==0` AND `Restated().TotalLiabilities>0` |
| 3.7 | Q2: A2 populates `TaxShieldDTA = writedownAmount * working.EffectiveTaxRate` when rate > 0; **DELETE** the Phase 2 regression pin `fired_path_TaxShieldDTA_stays_zero_per_Q2_deferral`; **ADD** `TestA2IntangibleAdjuster_TaxShieldDTA_PopulatedWhenEffectiveTaxRateNonZero` and `TestQ2_A2TaxShieldDTA_Populated` | `internal/services/datacleaner/adjustments/a2_intangible_adjuster.go`; `a2_intangible_adjuster_test.go` | A2 fired path returns `TaxShieldDTA > 0` when EffectiveTaxRate > 0; both new tests GREEN; old subtest deleted in same commit |
| 3.8 | Q4: B3 `captureB3AIProvenance` (or call site) computes `PromptHash = sha256Hex(renderedPrompt)`, `SourceDocHash = sha256Hex(footnoteText)`; ADD `TestQ4_AIProvenance_SHA256_Deterministic` | `internal/services/datacleaner/adjustments/liabilities.go` (B3 AI path); NEW helper `internal/services/datacleaner/adjustments/hash.go` (or inline `sha256Hex` if 2-line); test in `liabilities_test.go` or `b3_ai_provenance_test.go` | B3 AI path emits non-empty `PromptHash` + `SourceDocHash`; `TestQ4_AIProvenance_SHA256_Deterministic` GREEN |
| 3.9 | ctx threading: rename `ProcessLiabilityAdjustments(data, rules, cleaningCtx)` → `ProcessLiabilityAdjustments(ctx, data, rules, cleaningCtx)`. Same for `ProcessAssetAdjustments` + `ProcessEarningsAdjustments`. Update call sites in `service.go::applyActiveAdjustments` (already has `ctx` in scope). Update ALL test callers (`liabilities_test.go`, `assets_test.go`, `earnings_test.go`, `datacleaner_simple_test.go`, `b3_contingent_liability_adjuster_test.go`, etc.) to pass `context.Background()` or `context.TODO()`. | `internal/services/datacleaner/adjustments/{liabilities,assets,earnings}.go`; `internal/services/datacleaner/service.go`; all test files calling `Process*Adjustments`. | `go build ./...` exit 0; full test suite GREEN; `TestCtxThreading_LiabilityAdjusterReceivesCtx` GREEN |
| 3.10 | `SchemaVersion["FinancialData"]` 8 → 9 — atomic with the first commit that populates non-zero `TaxShieldDTA` OR non-empty `PromptHash`/`SourceDocHash` (typically Task 3.7 or 3.8, whichever lands first). Refresh `artifacts/tier2-baseline/<date>/*` bundle baselines for the structural drift (Q2 `tax_shield_dta`, Q4 hash fields). | `internal/observability/replay/schema.go`; `artifacts/tier2-baseline/<date>/*/13-cleaner-audit.json` (and similar) refreshed via replay capture. | Replay diff on basket shows numeric drift = 0, structural drift = documented Q2/Q4 fields only |
| 3.11 | Import-boundary test for `cleaneddata` package (asserts no imports from `internal/services/` outside `entities`). Mirrors Phase 0/2 pattern. | NEW: `internal/services/datacleaner/cleaneddata/import_boundary_test.go` | `TestCleanedFinancialData_ImportBoundary` GREEN |
| 3.12 | Extend `TestLedger_BasketSnapshot_ClusterPrediction` (PR-4 Task 4.6) to ALSO assert `Restated()` view's truthful `TotalLiabilities` reconstruction for AMD + KO (T2-BS-3 acceptance row). Optional in Phase 3; required for Phase 3 → Phase 4 gate. | `internal/integration/datacleaner_ledger_basket_test.go` | The extended assertions GREEN for AMD + KO |
| 3.13 | Docs sweep: CLAUDE.md DC-1 gotcha (Phase 3 SHIPPED bullet); spec/plan changelog rows; DC-1 reviewer tracker progress paragraph; THESIS DC-1 row update. **NO** AGENTS.md update needed (the Phase 3 spec reference is already added in the spec-authoring commit). | `CLAUDE.md`; `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` (change log); this file (change log); `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md`; `docs/THESIS.md` | Docs reflect Phase 3 ship; cross-references resolve |
| 3.14 | Phase 3 closeout doc filing (mirrors Phase 2 closeout template) | NEW: `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-3-closeout.md` | Closeout doc exists with what-landed / Q-resolutions / load-bearing-invariants / next-steps sections |

### Option B (fallback): 2-PR split

If PR-1 review surfaces design feedback that blocks merging in one shot, fall back to:

- **PR-1 (`dc1-phase-3-pr-1`)**: Tasks 3.1-3.6 + 3.11 (cleaneddata package + view accessors + import-boundary test). Q2/Q4 stay deferred; ctx threading deferred to PR-2.
- **PR-2 (`dc1-phase-3-pr-2`)**: Tasks 3.7-3.10 + 3.12 (Q2 + Q4 + ctx threading + SchemaVersion bump + extended basket test). Closeout doc filed at end of PR-2.

### Sub-step detail — Task 3.3 (`Restated()` accessor)

```go
// internal/services/datacleaner/cleaneddata/restate.go
package cleaneddata

func (c *CleanedFinancialData) Restated() *FinancialDataView {
    if c.restated != nil {
        return c.restated
    }
    v := identityCopy(c.raw)  // start with field-for-field copy
    v.ViewKind = RestatedView

    // Apply ledger entries (Fired only).
    for _, e := range c.raw.AdjustmentLedger {
        if !e.Fired {
            continue
        }
        applyLedgerEntryToView(&v, e)  // switch over e.Component; mutate v.X
        v.StockholdersEquity += e.EquityOffset      // C6 has EquityOffset=0 by design
        v.DeferredTaxAssets  += e.TaxShieldDTA      // A2 (Phase 3 Q2) + A5 (Phase 2)
    }

    // Recompute umbrellas from components (mirrors recompute.go::recomputeUmbrellas).
    v.CurrentAssets      = c.raw.CashAndCashEquivalents + v.Inventory + c.raw.OtherCurrentAssets
    v.TotalAssets        = v.CurrentAssets + v.Goodwill + v.OtherIntangibles +
                           v.DeferredTaxAssets + c.raw.OtherNonCurrentAssets
    v.CurrentLiabilities = c.raw.OperatingLeaseLiabilityCurrent + c.raw.OtherCurrentLiabilities
    v.TotalLiabilities   = v.CurrentLiabilities + v.TotalDebt +
                           c.raw.OperatingLeaseLiabilityNoncurrent +
                           c.raw.OtherNonCurrentLiabilities
    v.TangibleAssets     = v.TotalAssets - v.Goodwill - v.OtherIntangibles

    c.restated = &v
    return c.restated
}

func applyLedgerEntryToView(v *FinancialDataView, e entities.LedgerEntry) {
    switch e.Component {
    case "Inventory":
        v.Inventory += e.DeltaAmount
    case "OtherIntangibles":
        v.OtherIntangibles += e.DeltaAmount
    case "DeferredTaxAssets":
        v.DeferredTaxAssets += e.DeltaAmount
    case "OperatingIncome":
        v.OperatingIncome += e.DeltaAmount
    case "NormalizedOperatingIncome":
        v.NormalizedOperatingIncome += e.DeltaAmount
    case "InterestExpense":
        v.InterestExpense += e.DeltaAmount
    default:
        // Unknown Component → emit WARN (fail-soft); test pin asserts logging.
        // Don't panic; future adjusters may add components before Restated()
        // is updated.
    }
}
```

### Sub-step detail — Task 3.4 (`InvestedCapital()` accessor)

```go
// internal/services/datacleaner/cleaneddata/invested_capital.go
package cleaneddata

func (c *CleanedFinancialData) InvestedCapital() *FinancialDataView {
    if c.investedCap != nil {
        return c.investedCap
    }
    base := c.Restated()       // get (or compute) restated
    v := *base                  // shallow copy; FinancialDataView has no pointer fields
    v.ViewKind = InvestedCapitalView

    for _, o := range c.raw.Overlays {
        applyOverlayToView(&v, o)
    }

    c.investedCap = &v
    return c.investedCap
}

func applyOverlayToView(v *FinancialDataView, o entities.OverlaySpec) {
    // Phase 3 only sees Incremental in practice; future Replacement/Delta
    // need an additional branch on AmountSemantics.
    switch o.Field {
    case "TotalDebt":
        // B1 + B2: semantically these are DebtLikeClaims contributors per spec.
        v.DebtLikeClaims += o.Amount
    case "DebtLikeClaims":
        // B3 (Phase 2 routing intent realized here in Phase 3).
        v.DebtLikeClaims += o.Amount
    case "TotalAssets":
        if o.Operation == "subtract" {
            // A1 goodwill exclusion (Damodaran convention).
            v.TotalAssets -= o.Amount
            v.Goodwill = 0
            v.TangibleAssets = v.TotalAssets - v.OtherIntangibles
        }
    default:
        // Unknown Field → fail-soft WARN.
    }
}
```

---

## Acceptance gates (BEFORE each commit)

Every commit in the Phase 3 PR must pass:

1. `go build ./...` exit 0
2. `go test ./internal/services/datacleaner/... -count=1` GREEN
3. `go test ./internal/services/valuation/models/... -run TestDDM_LegacyPath_BitForBit -count=1` GREEN (JPM/BAC/WFC bit-for-bit)
4. `go test ./internal/integration/... -run TestDataCleanerRecompute_ShadowMode_TickerBasket -count=1` GREEN
5. `git diff --quiet internal/integration/testdata/recompute-shadow/` exit 0 (shadow snapshots byte-identical)
6. `go test ./internal/services/datacleaner/ -run TestOrchestrator_LedgerOrdering -count=1` GREEN
7. `go test ./internal/integration/ -run TestLedger_BasketSnapshot_ClusterPrediction -count=1` GREEN (10/10 tickers)
8. Full `go test ./... -count=1` GREEN modulo pre-existing scheduler test race
9. Replay diff after each commit on AAPL + MSFT: `go run ./cmd/replay artifacts/tier2-baseline/<date>/AAPL/` shows zero NUMERIC drift in `17-response.json`. Structural drift in `13-cleaner-audit.json` expected ONLY on Tasks 3.7 + 3.8 (Q2 tax_shield_dta; Q4 hash fields).

The SchemaVersion-bump commit (Task 3.10) is the ONLY commit allowed to introduce structural-schema drift; use `--allow-schema-drift` on that commit's replay verification.

---

## Critical invariants

| Invariant | Why it matters | Pin |
|---|---|---|
| `TestDDM_LegacyPath_BitForBit` GREEN at every commit | JPM/BAC/WFC DDM bit-for-bit is the cross-Tier-2 contract. Phase 3 does not change adjuster execution, so this is trivially preserved — but any accidental mutation of `data.*` by the view accessors would surface here. | `internal/services/valuation/models/ddm_bitforbit_test.go` |
| `TestRecomputeUmbrellas_NoMutation` GREEN | Recompute shim must stay pure; Phase 3 does NOT modify `recompute.go`. | `internal/services/datacleaner/recompute_test.go` |
| `TestOrchestrator_LedgerOrdering` GREEN | Asset → liability → earnings partition. Phase 3 changes only Process*Adjustments signatures, not execution order. | `internal/services/datacleaner/ledger_invariants_test.go` |
| Shadow snapshots byte-identical | Phase 3 does not change adjuster outputs; snapshots stay byte-stable across all commits. | `internal/integration/testdata/recompute-shadow/<TICKER>.json` |
| `CleanedFinancialData` is ADDITIVE | Existing `data.*` reads continue to work until Phase 4 migration. **Phase 3 adds `CleanWithViews(ctx, ...)` sibling, does NOT change `Clean(ctx, ...)` signature.** | `service.go` audit |
| C6 EquityOffset=0 preserved through `Restated()` | Capitalized-interest reclassification MUST NOT flow to retained equity. Pinned by both `c6_capitalized_interest_adjuster_test.go` (Phase 2) and new `TestCleanedFinancialData_Restated_C6EquityOffsetZero` (Phase 3). | Both tests |
| B3 `OverlaySpec.Field:"DebtLikeClaims"` routing intent honored | `InvestedCapital()` reads `Field:"DebtLikeClaims"` overlays and adds to `DebtLikeClaims`, NOT `TotalDebt`. Phase 4 flips consumers. | `TestCleanedFinancialData_InvestedCapital_AppliesOverlays` |
| `Restated() == AsReported()` when no adjusters fired | Identity property; ensures the recompute-from-components path produces the parser-stamped umbrella when no Restater contributed. | `TestCleanedFinancialData_Restated_BitForBitOnNoFiredAdjusters` (gopter property test) |

---

## Gotchas inherited from Phase 2

1. **Worktree discipline.** Stay in `midas-dc1-phase-3/`. Verify `pwd` + `git rev-parse --abbrev-ref HEAD` before EVERY commit. Bash branch-switch friction is real; the worktree is the firewall.

2. **CRLF noise on Windows.** `git status` and `git diff` print CRLF warnings on shadow snapshots that are NOT real content drift. Use `git diff --quiet internal/integration/testdata/recompute-shadow/` (with `--quiet`) and check exit code; exit 0 = byte-identical.

3. **C6 EquityOffset=0 LOAD-BEARING precedent.** When implementing `Restated()`, the equity-offset accumulator MUST read `e.EquityOffset` field directly. NEVER derive `EquityOffset` from `DeltaAmount` (e.g., `equityOffset := -e.DeltaAmount`) — that would break C6's contract. The Phase 2 `NativeC6Emission` subtest's failure message explicitly says "Phase 3 Restated() must NOT add C6 DeltaAmount to retained earnings"; honor it.

4. **B3 `Field:"DebtLikeClaims"` routing intent.** Phase 2's B3 OverlaySpec records the intent; Phase 3's `InvestedCapital()` accessor reads it. Phase 4 (NOT Phase 3) flips the WACC consumer to read from `InvestedCapital.DebtLikeClaims`. Phase 3 must NOT delete the `data.TotalDebt` dual-write — that's a Phase 4 deliverable.

5. **Tier2-baseline date directory.** As of 2026-05-23, the live baseline is `artifacts/tier2-baseline/2026-05-19/` (10 tickers: AAPL, AMD, BABA, EQIX, F, JNJ, KO, MSFT, MXL, TSM — JPM is NOT in this directory). Use AAPL + MSFT for spot checks; the JPM bit-for-bit invariant is exercised by `TestDDM_LegacyPath_BitForBit` (fixtures live at `internal/services/valuation/models/testdata/golden/`).

6. **SchemaVersion atomic-bump rule.** `feedback_schema_version_atomic_bump` MEMORY rule: bump in the FIRST commit that populates the field non-zero. For Phase 3, that's Task 3.7 (A2 TaxShieldDTA non-zero on first qualifying ticker) OR Task 3.8 (B3 AIProvenance.PromptHash non-empty). Whichever lands first carries the bump. Do NOT bump in the structural-only commit (Task 3.1 creating the package).

7. **`omitempty` JSON tags.** `LedgerEntry.TaxShieldDTA` and `AIProvenance.PromptHash` / `SourceDocHash` are tagged `omitempty` in Phase 2. Phase 3 populating them changes JSON output shape (these fields appear in JSON for the first time). Use `--allow-schema-drift` on the bundle baseline refresh.

8. **Test fixture density for Q4.** The B3 AI path requires `cleaningCtx.FootnoteText` to be non-empty AND the AI client to be enabled. Existing test fixtures may not exercise this path. The Q4 test (`TestQ4_AIProvenance_SHA256_Deterministic`) should use a stub AI client (`testai.NewStubClient(...)` or inline interface implementation) to avoid network. See Phase 2 PR-4's B3 test patterns for the stub shape.

9. **DO NOT migrate consumers in Phase 3.** Every `data.TotalAssets` / `data.TotalDebt` / `data.StockholdersEquity` / `data.CurrentAssets` / `data.OperatingIncome` read site in `internal/services/valuation/` stays unchanged. Phase 4 is the consumer-migration gate; Phase 3 only adds the accessor surface. If a reviewer asks "why don't we also migrate consumer X in this PR?" — the answer is "scope discipline; Phase 4 owns that."

10. **DO NOT delete the dual-write in Phase 3.** Every `data.X ±= Y` mutation in dispatcher switch arms (e.g., `ProcessLiabilityAdjustments`' B1/B2/B3 mutations of `data.TotalDebt`) stays in place. Phase 4 deletes them atomically with consumer migration. Phase 3 testing the dual-write IS the legacy contract.

11. **Translator extraction question is CLOSED.** Per spec §5.4, KEEP per-rule translators. Do not propose extraction in this PR. Phase 4 deletes them.

12. **ctx parameter ordering.** `ctx context.Context` MUST be the FIRST parameter (Go convention). Do not add it as a trailing parameter or inside a struct.

---

## Spec / doc updates required (Task 3.13)

| File | Update |
|---|---|
| `CLAUDE.md` | DC-1 gotcha: append "Phase 3 SHIPPED <date> (merge `<sha>`)" sub-bullet with the four-flavor accessor surface + Q2/Q4 closure. |
| `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` | Change-log row for 2026-05-DD: Phase 3 SHIPPED — `CleanedFinancialData` accessors + Q2 + Q4 + ctx threading. |
| `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-3-spec.md` (this spec) | Change-log row for ship date. |
| `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-3-implementation-plan.md` (this file) | Change-log row for ship date + final commit SHAs. |
| NEW: `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-3-closeout.md` | Phase 3 closeout report — mirrors Phase 2 closeout template. Include load-bearing-invariants table, Q-resolutions table, basket-test extension result for AMD/KO. |
| `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md` | Progress paragraph for Phase 3 SHIPPED. |
| `docs/THESIS.md` | DC-1 row update: Phase 3 SHIPPED; Phase 4 next. |
| `AGENTS.md` row 17b | Append Phase 3 closeout reference. |

---

## Phase 3 → Phase 4 gate

Phase 4 dispatch happens only after **all of**:

1. All Phase 3 acceptance criteria checked (spec §12).
2. Phase 3 closeout doc filed with all Q-resolutions documented.
3. `TestCleanedFinancialData_AsReported_PreservesParserZeros_AMD_KO` GREEN.
4. `TestLedger_BasketSnapshot_ClusterPrediction` extended to assert `Restated().TotalLiabilities > 0` for AMD + KO (T2-BS-3 acceptance row).
5. Full replay basket diff: zero numeric drift; documented structural drift only.
6. HUMAN signoff on the Phase 3 PR(s) and merge to master.

Phase 4's scope is enumerated in the parent spec §"Consumer migration map" — 13 read sites + B3 routing flip + dual-write deletion. Phase 4 may ship as a single mega-PR or N-PR split per consumer cluster; that decision is Phase 4 ARCH's call.

---

## Change log

| Date | Change |
|---|---|
| 2026-05-23 | Initial Phase 3 implementation plan filed by Phase 2 closeout ARCH. Covers 14 tasks across the single-PR Option A path; Option B fallback documented. Anchored at Phase 2 4-PR stack final tip on branch `dc1-phase-2-pr-4`. PR strategy: Option A recommended (single PR; ~1-2 agent shifts). Phase 3 → Phase 4 gate documented. Spec at `datacleaner-component-primitive-and-parallel-views-phase-3-spec.md`. |
