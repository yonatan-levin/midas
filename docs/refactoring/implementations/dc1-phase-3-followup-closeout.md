# DC-1 Phase 3 Followup — Closeout Report

**Phase:** Phase 3 followup (cross-model review fixes — 9 findings)
**Status:** SHIPPED 2026-05-25
**Branch at close:** `dc1-phase-3-followup` (single PR, 10 commits; awaiting HUMAN merge to master)
**Anchored on:** master `46e84b1` (DC-1 Phase 3 merge commit)
**Spec:** [dc1-phase-3-followup-spec.md](../spec/dc1-phase-3-followup-spec.md)
**Plan:** [dc1-phase-3-followup-implementation-plan.md](./dc1-phase-3-followup-implementation-plan.md)
**Phase 3 closeout (parent):** [datacleaner-component-primitive-and-parallel-views-phase-3-closeout.md](./datacleaner-component-primitive-and-parallel-views-phase-3-closeout.md)

---

## What landed

A single PR on branch `dc1-phase-3-followup` closing 9 findings surfaced by independent cross-model review (gpt-5.5 via `zen-mcp`) against the Phase 3 merge.

Headline architectural changes:

- **HIGH-1 (F.1)** — `Restated()` view-seed double-count fixed. `CleanFinancialDataWithViews` now captures a pre-clean snapshot of the input `*FinancialData` before invoking `CleanFinancialData`; the snapshot flows into a new `cleaneddata.New(asReported, restated)` two-arg signature. `AsReported()` reads the snapshot; `Restated()` seeds from the post-clean entity and applies ONLY `LedgerEntry.EquityOffset + TaxShieldDTA` from the ledger. The `applyLedgerEntryToView` helper is DELETED — the per-component `DeltaAmount` switch no longer exists because the dispatcher dual-write has already applied that delta to the post-clean component fields. The pre-fix code double-counted every Restater fire (A2 / A4 / A5 / C1 / C2 / C3 / C5 / C6).

- **HIGH-2 + HIGH-3 (F.2)** — B3 collapsed to a single AI call. `analyzeContingentLiabilityWithAI(ctx, data, cleaningCtx, timestamp)` now returns `(probability, *AIProvenance, metadata, error)` from ONE `AnalyzeFootnote` invocation. `captureB3AIProvenance` is DELETED. `ApplyB3Contingent` pre-computes the AI result and injects it via a new `preComputedAIResult` parameter into the shared `processContingentLiabilityAdjustment` helper, so the legacy method does NOT re-invoke the AI service. The recorded `overlay.Amount` and `overlay.AIProvenance.Probability` now derive from the SAME response; the ctx threading closes HIGH-3 by removing the previously-hardcoded `context.Background()` on the amount path.

- **MEDIUM-1 (F.3)** — B1 lease PV ctx threading. `ProcessOperatingLeaseAdjustment(ctx, ...)` + `ApplyB1OperatingLeases(ctx, ...)` forward ctx to `leaseCalculator.CalculatePresentValue`. The hardcoded `ctx := context.Background() // TODO` line is deleted.

- **MEDIUM-2 (F.4)** — Phase 3 spec §5.2 amended. `PromptHash` is now documented as a **canonical-request fingerprint** (timestamp-stripped serialization of `ai.FootnoteAnalysisRequest` fields, sorted Context keys) — NOT the literal LLM prompt string. The AI client at this layer does not expose a rendered-prompt string; the canonical-request hash satisfies the underlying intent. Prompt-template drift is captured separately by `AIProvenance.ModelName`. No code change.

- **LOW-1 (F.5)** — `hash.go::sha256HexPromptCanonical` hardened. Inner `json.Marshal` errors emit `<unsupported:%T>` tag (was: silent empty-string collision); outer Marshal gets panic-on-error guard.

- **LOW-2 (F.6)** — godoc warning on `cleaneddata.CleanedFinancialData` documenting NOT-goroutine-safe contract. Code change: none. Future parallel-read consumers would need a `sync.Once` retrofit (deferred to Phase 5).

- **LOW-3 (F.7)** — `TestIdentityCopy_CoversEveryViewField` reflection-based field-coverage test. Enumerates every `FinancialDataView` field, builds a synthetic entity with distinct sentinel values, asserts every non-exempt view field is non-zero post-`identityCopy` and equals the entity value.

- **LOW-4 (F.8)** — `Raw()` carries `TODO(phase-5)`; the parent spec's "Phasing & implementation sequence" table gains a new Phase 5 row tracking the planned removal.

---

## Commit ladder

| # | SHA | Task | Finding | One-line |
|---|---|---|---|---|
| 1 | `ee9b2e9` | F.1 | HIGH-1 | fix Restated() view-seed double-count via pre-clean snapshot |
| 2 | `d6312b0` | F.2 | HIGH-2 + HIGH-3 | collapse B3 to single AI call with ctx |
| 3 | `e1fbe3f` | F.3 | MEDIUM-1 | thread ctx through B1 lease PV path |
| 4 | `48aeee6` | F.5 | LOW-1 | harden hash.go against unsupported Context values |
| 5 | `6763e60` | F.7 | LOW-3 | reflection-based field-coverage test for identityCopy |
| 6 | `49faba7` | F.6 | LOW-2 | godoc warning on CleanedFinancialData goroutine-safety |
| 7 | `7092654` | F.8 | LOW-4 | Phase-5 deletion TODO on Raw() escape hatch |
| 8 | `31ed394` | F.4 | MEDIUM-2 | amend Phase 3 spec §5.2 PromptHash semantics |
| 9 | `992b4f7` | F.9 | docs sweep | CLAUDE.md / AGENTS.md / THESIS / Phase 3 closeout |
| 10 | (this commit) | F.10 | followup closeout | this document |

---

## Finding disposition

All 9 findings closed:

| Finding | Severity | Disposition | Task |
|---|---|---|---|
| HIGH-1 — `Restated()` view-seed double-count | HIGH | SHIPPED | F.1 |
| HIGH-2 — B3 two-call audit divergence | HIGH | SHIPPED | F.2 |
| HIGH-3 — B3 amount-path ctx | HIGH | SHIPPED | F.2 (same commit as HIGH-2) |
| MEDIUM-1 — B1 lease PV ctx threading | MED | SHIPPED | F.3 |
| MEDIUM-2 — `PromptHash` naming/spec drift | MED | SHIPPED — spec amended (Option (a)) | F.4 |
| LOW-1 — `json.Marshal` swallowing in `hash.go` | LOW | SHIPPED | F.5 |
| LOW-2 — cleaneddata mutex/Once | LOW | SHIPPED — godoc warning; sync.Once deferred to Phase 5 | F.6 |
| LOW-3 — `identityCopy` coverage | LOW | SHIPPED | F.7 |
| LOW-4 — `Raw()` mutable escape hatch | LOW | SHIPPED — TODO + Phase 5 row | F.8 |

---

## New load-bearing pin

`TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire` (file `internal/services/datacleaner/service_cleanwithviews_no_double_count_test.go`):

Exercises the FULL `CleanFinancialDataWithViews` pipeline on an A2-firing fixture (`OtherIntangibles=$150M`, `TotalAssets=$1B`, `EffectiveTaxRate=0.25`). Asserts:

1. `views.AsReported().OtherIntangibles == original_intangibles` (pre-clean snapshot preserved).
2. `views.Restated().OtherIntangibles == post_dispatcher_intangibles` (one delta application, NOT two).
3. `views.Restated().StockholdersEquity == post_clean_equity + LedgerEntry.EquityOffset` (single application).
4. A2 `LedgerEntry.TaxShieldDTA == writedown × EffectiveTaxRate` (Q2 invariant preserved).
5. `views.Restated().DeferredTaxAssets == post_clean_DTA + TaxShieldDTA`.

Pre-fix code produced `views.Restated().OtherIntangibles == post_dispatcher_intangibles - writedown` (double-counted). The pin fails RED on the pre-fix code with the diagnostic "If it equals originalIntangibles - 2\*writedown the ledger reducer is double-counting."

Sibling pin: `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnEarningsFire` — same shape against C1 restructuring on `NormalizedOperatingIncome`. Skips gracefully if a future classifier change prevents C1 from firing.

---

## Load-bearing invariants — GREEN at every commit

| Invariant | Status |
|---|---|
| `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC `math.Float64bits` equality) | GREEN |
| `TestRecomputeUmbrellas_NoMutation` | GREEN |
| `TestOrchestrator_LedgerOrdering` | GREEN |
| `TestLedger_BasketSnapshot_ClusterPrediction` (10/10) | GREEN |
| `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` (AMD $9.679B / KO $60.912B) | GREEN |
| `TestDataCleanerRecompute_ShadowMode_TickerBasket` | GREEN |
| Shadow snapshots byte-identical (`git diff --quiet internal/integration/testdata/recompute-shadow/`) | GREEN at every commit |
| `TestQ2_A2TaxShieldDTA_Populated` | GREEN |
| `TestQ4_AIProvenance_SHA256_Deterministic` | GREEN |
| `TestCleanedFinancialData_Restated_C6EquityOffsetZero` | GREEN |
| Full `go test ./...` exit 0 | GREEN at every commit |

---

## NON-goals honored

Phase 3 NON-goals continued to hold across all 10 commits. None of these were touched:

| NON-goal | Status |
|---|---|
| No consumer migration (the 13 `data.*` read sites in `internal/services/valuation/` stay unchanged) | HONORED |
| No B3 routing flip (`data.TotalDebt` dual-write for B3 stays in dispatcher) | HONORED |
| No dispatcher dual-write deletion | HONORED |
| No `CalculationVersion` bump (stays at `"4.2"`) | HONORED |
| No `SchemaVersion["FinancialData"]` bump (stays at 9) | HONORED |
| No changes to `recompute.go` | HONORED |
| No DDM golden regeneration | HONORED |
| No translator extraction | HONORED |

---

## Phase 4 readiness

Phase 3 followup unblocks Phase 4 in two material ways:

1. **HIGH-1 fix** removes the latent double-count bug from `Restated()`. Without this fix, Phase 4's first consumer that flips to read `Restated().OtherIntangibles` / `.Inventory` / `.NormalizedOperatingIncome` / `.InterestExpense` / `.DeferredTaxAssets` would produce wrong numbers silently. The new regression pin
(`TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire`) guards
against the bug returning.

2. **HIGH-2 + HIGH-3 fix** restores audit integrity for the B3 AI path. Phase 4's WACC consumer will eventually consume `InvestedCapital.DebtLikeClaims` (the overlay's amount) AND the AIProvenance metadata; the pre-fix code could ship divergent values between them.

All Phase 3 → Phase 4 gate items remain satisfied:

- [x] Phase 3 acceptance criteria still checked (no regressions on the original 12 gate items).
- [x] Followup closeout filed (this document).
- [x] `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` GREEN — AMD/KO Restated reconstructions still produce $9.679B / $60.912B against the 2026-05-19 baseline.
- [x] Full `go test ./...` exit 0.
- [ ] HUMAN signoff on the Phase 3 followup PR and merge to master — IN FLIGHT.

Phase 4 dispatch follows HUMAN merge of this followup.

---

## Known deferred items

| Item | Status | Owner |
|---|---|---|
| `sync.Once` retrofit on `CleanedFinancialData` accessors | DEFERRED to Phase 5 | Triggered IF a parallel-read consumer lands (e.g., batch valuation endpoint) |
| `Raw()` deletion | DEFERRED to Phase 5 | After Phase 4 closes the consumer migration window |
| Replace canonical-request hash with actual rendered-prompt hash | NOT PLANNED (Option (a) was the chosen MEDIUM-2 fix) | Watch item — only revisit if regulatory traceability rules require it |
| Operator baseline refresh (`artifacts/tier2-baseline/`) post-followup | NOT REQUIRED (no numeric drift in `17-response.json` expected; this followup is bug-fix only) | Operator (optional refresh) |

---

## Change log

| Date | Change |
|---|---|
| 2026-05-25 | Initial closeout filed by Phase 3 followup implementer. Covers the 10-commit ship on branch `dc1-phase-3-followup` (single PR). All 9 findings closed. New load-bearing pin (`TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire`) added. All load-bearing invariants GREEN at every commit. NON-goals preserved. Phase 4 readiness signalled. |
