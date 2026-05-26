# DC-1 Phase 3 Followup — Implementation Plan (Cross-Model Review Fixes)

**Phase:** Followup PR on top of DC-1 Phase 3 (merge `46e84b1`); precedes Phase 4 dispatch
**Status:** READY FOR BACKEND DISPATCH (authored 2026-05-25)
**Estimated effort:** 1 agent shift (~6–9 agent-hours)
**Branch base:** master at `46e84b1` (Phase 3 merge commit) — worktree already created at `midas-dc1-phase-3-followup/`, branch `dc1-phase-3-followup`

**Spec:** [dc1-phase-3-followup-spec.md](../spec/dc1-phase-3-followup-spec.md)
**Parent spec:** [datacleaner-component-primitive-and-parallel-views-phase-3-spec.md](../spec/datacleaner-component-primitive-and-parallel-views-phase-3-spec.md)
**Phase 3 closeout:** [datacleaner-component-primitive-and-parallel-views-phase-3-closeout.md](datacleaner-component-primitive-and-parallel-views-phase-3-closeout.md)

---

## Worktree workflow (REQUIRED)

Per `feedback_worktree_first_workflow` MEMORY rule: the main `midas/` directory MUST stay on `master`. Followup work happens in the sibling worktree already provisioned:

```
C:\Users\Yonatan Levin\Documents\Programming\Projects\FinTech\Strade\midas-dc1-phase-3-followup
```

Branch: `dc1-phase-3-followup` (created from master `46e84b1`).

Before EVERY `git commit` (Bash CWD resets between calls):

```bash
cd "/c/Users/Yonatan Levin/Documents/Programming/Projects/FinTech/Strade/midas-dc1-phase-3-followup"
pwd                                  # must end with midas-dc1-phase-3-followup
git rev-parse --abbrev-ref HEAD      # must print dc1-phase-3-followup
git worktree list                    # main midas at master + this worktree visible
```

If anything looks wrong, STOP and re-check before committing.

---

## Required reading (in order)

### Tier 1 — Identity & continuity
1. `CLAUDE.md` — DC-1 Phase 3 SHIPPED bullet.
2. `AGENTS.md` — row 17b.
3. `docs/THESIS.md` — DC-1 row.

### Tier 2 — Followup spec + Phase 3 ground truth
4. **`docs/refactoring/spec/dc1-phase-3-followup-spec.md`** — this followup's authoritative spec.
5. **`docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-3-spec.md`** — §4 (Architecture), §5 (Q-resolutions), §10 (gate).
6. **`docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-3-closeout.md`** — what Phase 3 actually shipped.

### Tier 3 — Code surfaces touched by the followup
7. `internal/services/datacleaner/cleaneddata/cleaned.go` — `CleanedFinancialData` struct + `New` (HIGH-1, LOW-2, LOW-4).
8. `internal/services/datacleaner/cleaneddata/restate.go` — `Restated()` reducer + `applyLedgerEntryToView` (HIGH-1).
9. `internal/services/datacleaner/cleaneddata/asreported.go` — `identityCopy` (LOW-3).
10. `internal/services/datacleaner/cleaneddata/view.go` — `FinancialDataView` struct (LOW-3 enumeration target).
11. `internal/services/datacleaner/service.go` lines 110–351 — `CleanFinancialData` + `CleanFinancialDataWithViews` (HIGH-1 pre-clean snapshot capture).
12. `internal/services/datacleaner/adjustments/liabilities.go` — B1 `ProcessOperatingLeaseAdjustment` (line 652, MEDIUM-1), B3 `ApplyB3Contingent` + `captureB3AIProvenance` (~line 1090, HIGH-2/3), `analyzeContingentLiabilityWithAI` (~line 1657, HIGH-2/3).
13. `internal/services/datacleaner/adjustments/hash.go` — `sha256HexPromptCanonical` (LOW-1).
14. `internal/services/datacleaner/cleaneddata/asreported_test.go`, `restate_test.go`, `invested_capital_test.go`, `cleaned_test.go` — existing Phase 3 unit tests (callers of `cleaneddata.New(raw)` that need signature updates).
15. `internal/integration/datacleaner_ledger_basket_test.go` — `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` callsite (one call to `cleanerSvc.CleanFinancialDataWithViews`).

### Tier 4 — Templates
16. `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-3-implementation-plan.md` — Phase 3 plan template; this followup mirrors its task-table shape.

---

## Tasks

Tasks are ordered for atomicity: each task is **one commit** that leaves the repo at a GREEN-test state. The recommended order:

| # | Task | Files touched | Acceptance signal |
|---|---|---|---|
| F.1 | **HIGH-1 fix.** Capture pre-clean snapshot in `CleanFinancialDataWithViews`; change `cleaneddata.New(raw)` → `cleaneddata.New(asReported, restated)`; simplify `Restated()` reducer (delete `applyLedgerEntryToView`; ledger drives equity + DTA only); update all unit-test callers of `cleaneddata.New`; add `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire` + `OnEarningsFire`. | `internal/services/datacleaner/service.go`; `internal/services/datacleaner/cleaneddata/cleaned.go`; `internal/services/datacleaner/cleaneddata/restate.go`; `internal/services/datacleaner/cleaneddata/cleaned_test.go`; `internal/services/datacleaner/cleaneddata/restate_test.go`; `internal/services/datacleaner/cleaneddata/asreported_test.go`; `internal/services/datacleaner/cleaneddata/invested_capital_test.go`; `internal/services/datacleaner/cleaneddata/t2bs3_test.go` (if it calls `New`); plus any other call sites surfaced via `Grep cleaneddata.New\(`. | Both new tests GREEN; existing tests GREEN after signature updates; `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` GREEN; full `go test ./...` exit 0; `TestDDM_LegacyPath_BitForBit` GREEN; shadow snapshots byte-identical. |
| F.2 | **HIGH-2 + HIGH-3 fix.** Refactor `analyzeContingentLiabilityWithAI` to take `(ctx, data, cleaningCtx, timestamp)` and return `(probability, *AIProvenance, metadata, error)`. Extract `parseContingentLiabilityEstimate(extractedData, fallbackConfidence)`. Update `ApplyB3Contingent` to call the unified function and read provenance from the return value. Update `ProcessContingentLiabilityAdjustment` (legacy path) to pass `ctx` + ignore the provenance return. **DELETE** `captureB3AIProvenance`. Add `TestB3AISinglePath_AmountAndProvenance_AreConsistent` + `TestB3AmountAICall_HonorsContextCancellation`. | `internal/services/datacleaner/adjustments/liabilities.go`; `internal/services/datacleaner/adjustments/liabilities_test.go` (and/or `b3_*_test.go`). | New tests GREEN; `TestQ4_AIProvenance_SHA256_Deterministic` GREEN (hashes unchanged); `captureB3AIProvenance` no longer findable via Grep; mock `AnalyzeFootnote` call-count == 1 on the B3 happy path. |
| F.3 | **MEDIUM-1 fix.** Add `ctx context.Context` as first parameter of `ProcessOperatingLeaseAdjustment` AND `ApplyB1OperatingLeases` (if separate). Remove `ctx := context.Background() // TODO` at line 654. Forward `ctx` to `leaseCalculator.CalculatePresentValue`. Update test callers. Add `TestB1LeasePV_HonorsContextCancellation`. | `internal/services/datacleaner/adjustments/liabilities.go`; `internal/services/datacleaner/adjustments/liabilities_test.go` (B1 tests); any other callers surfaced via `Grep ProcessOperatingLeaseAdjustment` + `Grep ApplyB1OperatingLeases`. | New test GREEN; production code path has no `context.Background()` reachable from B1 (`Grep -n 'context\.Background\(\)' internal/services/datacleaner/adjustments/*.go | grep -v _test.go` shows no matches inside B1). |
| F.4 | **MEDIUM-2 fix.** Amend `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-3-spec.md` §5.2: replace the "rendered prompt" description with the canonical-request fingerprint description (per followup spec §4.4). Add a changelog row to the Phase 3 spec dated 2026-05-25 noting "PromptHash semantics clarified — canonical-request fingerprint, not literal LLM prompt string." | `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-3-spec.md` only. | Spec text accurately describes `sha256HexPromptCanonical`'s behavior; no code touched in this commit. |
| F.5 | **LOW-1 fix.** Update `hash.go::sha256HexPromptCanonical`: replace inner `_` json.Marshal error with `<unsupported:%T>` tag; replace outer Marshal `_` with a panic-on-error guard. Add `TestSha256HexPromptCanonical_HandlesUnsupportedContextValues`. | `internal/services/datacleaner/adjustments/hash.go`; `internal/services/datacleaner/adjustments/hash_test.go` (or `liabilities_test.go` if the existing test file is preferred). | New test GREEN; `TestQ4_AIProvenance_SHA256_Deterministic` still GREEN (the existing supported-types path produces identical hashes to before). |
| F.6 | **LOW-2 fix.** Add the goroutine-safety godoc warning on `CleanedFinancialData` per followup spec §4.6. Doc change only. | `internal/services/datacleaner/cleaneddata/cleaned.go`. | Comment present; no code change. |
| F.7 | **LOW-3 fix.** Add `TestIdentityCopy_CoversEveryViewField` reflection-based field-coverage test. | `internal/services/datacleaner/cleaneddata/asreported_test.go` (or new `asreported_coverage_test.go`). | New test GREEN. Confirm by intentionally removing one assignment in `identityCopy` locally to verify the test fails (then restore). |
| F.8 | **LOW-4 fix.** Add `// TODO(phase-5): delete after consumer migration completes — see ...` on `Raw()`. Add a Phase 5 sub-section entry in `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` under "Phasing & implementation sequence": "Phase 5 — Delete `Raw()` migration-window escape hatch." | `internal/services/datacleaner/cleaneddata/cleaned.go`; `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`. | TODO present; parent spec Phase 5 row mentions Raw() deletion. |
| F.9 | **Docs sweep.** Update `CLAUDE.md` (append followup bullet under the DC-1 Phase 3 row); `AGENTS.md` row 17b (append followup summary); `docs/THESIS.md` (DC-1 row); the Phase 3 closeout doc (append "Followup PR landed 2026-05-25" section + changelog row + cross-link); this followup's spec changelog; this followup's plan changelog. | `CLAUDE.md`; `AGENTS.md`; `docs/THESIS.md`; `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-3-closeout.md`; `docs/refactoring/spec/dc1-phase-3-followup-spec.md`; this file. | Docs reflect the followup ship; cross-references resolve. |
| F.10 | **Followup closeout.** File `docs/refactoring/implementations/dc1-phase-3-followup-closeout.md` mirroring the Phase 3 closeout template (what-landed, finding disposition table, load-bearing invariants, NON-goals honored, deferred items). | NEW: `docs/refactoring/implementations/dc1-phase-3-followup-closeout.md`. | Closeout doc exists; all 9 findings have a SHIPPED row. |

### Atomicity rules

- Each task is ONE git commit. The commit message names the task ID + the finding ID (e.g., `followup F.1: HIGH-1 view-seed double-count fix`).
- Every commit MUST leave the repo at GREEN test state. Run `go test ./...` after each task; do not stack failing commits.
- `cleaneddata.New` signature change in F.1 will break compilation across multiple test files. The test-file updates MUST land in the same commit as the signature change.
- F.2 deletes `captureB3AIProvenance`. The deletion MUST be in the same commit as the consolidated `analyzeContingentLiabilityWithAI` — never leave a half-migrated state on `dc1-phase-3-followup`.

### Recommended commit ladder

```
F.1 → F.2 → F.3 → F.5 → F.7 → F.6 → F.8 → F.4 → F.9 → F.10
```

Rationale: code fixes first (in severity order: HIGH-1 → HIGH-2/3 → MEDIUM-1 → LOW-1 → LOW-3 → LOW-2 → LOW-4), then doc fixes (MEDIUM-2 spec amend → docs sweep → closeout). The doc-only tasks (F.4, F.6, F.8, F.9, F.10) can be reordered freely.

---

## Detailed task notes

### F.1 — HIGH-1 view-seed double-count

**The load-bearing change.** Before touching code, write the regression test FIRST so it fails RED:

```go
// internal/services/datacleaner/cleaneddata/cleaned_test.go (new test)

func TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire(t *testing.T) {
    // Build a synthetic FinancialData that fires A2 intangible_writedown.
    // The test asserts Restated().OtherIntangibles == original - writedown
    // (one application of the delta, NOT two).
    //
    // Today, before the fix, Restated().OtherIntangibles == original - 2*writedown.
    // This test MUST fail before F.1 and pass after.
    data := buildA2FiringFixture(t)
    original := data.OtherIntangibles
    require.Greater(t, original, 0.0, "fixture must have non-zero OtherIntangibles")

    s := newServiceForTest(t)
    ctx := context.Background()
    result, views, err := s.CleanFinancialDataWithViews(ctx, data)
    require.NoError(t, err)
    require.NotNil(t, result)
    require.NotNil(t, views)

    // The A2 dispatcher dual-write has already restated data.OtherIntangibles.
    // The cleaned (post-clean) value:
    restated := result.CleanedData.OtherIntangibles
    require.Less(t, restated, original, "A2 must have fired and reduced OtherIntangibles")
    writedown := original - restated // computed indirectly to avoid hardcoding rule internals

    // KEY ASSERTION — no double-count.
    assert.InDelta(t, restated, views.Restated().OtherIntangibles, 0.01,
        "Restated().OtherIntangibles must equal the post-dispatcher value (one delta application), NOT (original - 2*writedown)")

    // AsReported preserves the pre-clean value.
    assert.InDelta(t, original, views.AsReported().OtherIntangibles, 0.01,
        "AsReported().OtherIntangibles must preserve the pre-clean value")

    // Equity offset is applied (from the LedgerEntry, post-fix).
    expectedEquityDelta := -writedown // A2's EquityOffset = DeltaAmount; both negative
    assert.InDelta(t,
        result.CleanedData.StockholdersEquity+expectedEquityDelta,
        views.Restated().StockholdersEquity, 0.01,
        "Restated().StockholdersEquity must equal post-clean equity + LedgerEntry.EquityOffset")
}
```

(Pseudocode — actual fixture builder uses the existing `BuildA2FiringFixture` helper if present in `internal/services/datacleaner/testhelpers/`, or constructs `entities.FinancialData` inline with A2's required trigger fields: `OtherIntangibles > 0`, `IndefiniteLivedIntangibles > 0`, an industry code that maps to A2-enabled rules, `EffectiveTaxRate > 0`.)

Then apply the code change:

1. `service.go::CleanFinancialDataWithViews`: capture `var snapshot entities.FinancialData; if data != nil { snapshot = *data }` before calling `s.CleanFinancialData(ctx, data)`; pass `&snapshot` and `result.CleanedData` to the new `cleaneddata.New` signature.
2. `cleaneddata/cleaned.go`: change `New(raw *entities.FinancialData)` → `New(asReported, restated *entities.FinancialData)`; rename fields (`raw` → `restated`); add `asReportedSnapshot` field.
3. `cleaneddata/asreported.go::AsReported`: read from `c.asReportedSnapshot` (not `c.raw`).
4. `cleaneddata/restate.go::Restated`: seed from `c.restated`; remove the ledger-driven DeltaAmount loop's `applyLedgerEntryToView(&v, e)` call; keep ONLY `v.StockholdersEquity += e.EquityOffset` and `v.DeferredTaxAssets += e.TaxShieldDTA`. Delete the helper `applyLedgerEntryToView` entirely (with its switch).
5. `cleaneddata/invested_capital.go::InvestedCapital`: seed continues to come from `Restated()` — no signature impact.
6. Update test callers of `cleaneddata.New`: search via `Grep "cleaneddata\.New\(" internal/`. Convert single-arg calls to two-arg: `cleaneddata.New(raw)` → `cleaneddata.New(raw, raw)` (same pointer twice — preserves the "synthesized test" semantics).
7. Add `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnEarningsFire` (C1 restructuring shape).

#### Important — preserving `TestCleanedFinancialData_Restated_C6EquityOffsetZero`

The existing test synthesizes a `FinancialData` directly. After F.1, that test calls `cleaneddata.New(synthesized, synthesized)`. The C6 invariant (EquityOffset=0 ⇒ no equity flow) still holds: the reducer only adds `e.EquityOffset` to `v.StockholdersEquity`, and C6 entries carry `EquityOffset=0`, so the invariant is preserved naturally.

#### Important — `TestCleanedFinancialData_AsReported_PreservesParserZeros_AMD_KO`

This test should keep passing because:
- `AsReported()` now reads from the pre-clean snapshot.
- For AMD/KO, the parser stamps `TotalLiabilities=0` PRE-clean.
- The snapshot captures the pre-clean state, so `AsReported().TotalLiabilities == 0` still holds.

Verify by running the test before and after F.1. If a synthesized-fixture variant of this test is updated to use the dispatcher path, the assertion changes shape — but the live-fixture sibling (`TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction`) is the canonical T2-BS-3 acceptance pin and should not need updates.

### F.2 — HIGH-2 + HIGH-3 single-call B3

`captureB3AIProvenance` (lines 1160–1249 of `liabilities.go`) and `analyzeContingentLiabilityWithAI` (lines 1657–1734) collapse into one. The new function lives where `analyzeContingentLiabilityWithAI` is today.

Key consolidation: the `extractedData` → `(probability, extractedSpan)` parser branch exists in BOTH helpers today (slightly different shapes). Extract `parseContingentLiabilityEstimate(extractedData, fallbackConfidence) (prob float64, span string, err error)` to consolidate.

Update call sites:
- `ApplyB3Contingent` line 1090: replace `aiProv, aiErr := la.captureB3AIProvenance(ctx, working, cleaningCtx, now)` with `_, aiProv, _, aiErr := la.analyzeContingentLiabilityWithAI(ctx, working, cleaningCtx, now)`. Provenance is non-nil on success.
- `ProcessContingentLiabilityAdjustment` line 1417: replace `aiProbability, aiMetadata, err := la.analyzeContingentLiabilityWithAI(data, cleaningCtx)` with `aiProbability, _, aiMetadata, err := la.analyzeContingentLiabilityWithAI(ctx, data, cleaningCtx, time.Now())`. **This requires `ProcessContingentLiabilityAdjustment` to also accept `ctx` as a parameter** — verify the dispatcher already passes ctx (Phase 3 added it to `ProcessLiabilityAdjustments`); if it's not yet forwarded to the per-rule `ProcessContingentLiabilityAdjustment`, add a ctx parameter and forward.

Mock test:

```go
// internal/services/datacleaner/adjustments/liabilities_test.go

func TestB3AISinglePath_AmountAndProvenance_AreConsistent(t *testing.T) {
    mock := &mockAIServiceWithCounter{}
    la := buildLiabilityAdjusterWithAI(t, mock)
    data := buildB3FiringFixture(t)
    cleaningCtx := &entities.CleaningContext{
        IndustryCode:  "TECH_SOFTWARE",
        FootnoteText:  "Disclosed contingent litigation exposure of $50M.",
    }
    rule := &entities.CleaningRule{ID: "contingent_liabilities", Enabled: true /* ... */}

    out, err := la.ApplyB3Contingent(context.Background(), data, rule, cleaningCtx, time.Now())
    require.NoError(t, err)
    require.NotEmpty(t, out.Overlays)
    require.NotNil(t, out.Overlays[0].AIProvenance)

    // ONE call.
    assert.Equal(t, int32(1), mock.callCount.Load(),
        "B3 must invoke AnalyzeFootnote exactly once — not twice")

    // Amount derives from the same probability that the provenance records.
    overlay := out.Overlays[0]
    total := data.ContingentLiabilities + data.EnvironmentalLiabilities + data.LitigationLiabilities
    expectedAmount := total * overlay.AIProvenance.Probability
    assert.InDelta(t, expectedAmount, overlay.Amount, 0.01,
        "overlay.Amount must equal totalContingent * overlay.AIProvenance.Probability")
}

func TestB3AmountAICall_HonorsContextCancellation(t *testing.T) {
    la := buildLiabilityAdjusterWithAI(t, &mockAIServiceCancellable{})
    data := buildB3FiringFixture(t)
    cleaningCtx := &entities.CleaningContext{ /* ... */ }
    rule := &entities.CleaningRule{ID: "contingent_liabilities", Enabled: true}

    ctx, cancel := context.WithCancel(context.Background())
    cancel()
    _, err := la.ApplyB3Contingent(ctx, data, rule, cleaningCtx, time.Now())
    // Acceptable signals: context.Canceled error, OR an error wrapping it,
    // OR the AI call was never made (mock should record zero calls).
    if err != nil {
        assert.ErrorIs(t, err, context.Canceled,
            "ctx cancellation must propagate through B3 AI path")
    }
}
```

### F.3 — MEDIUM-1 B1 ctx threading

Mechanical. Add `ctx context.Context` as first parameter; remove the inline `ctx := context.Background()`. Update `ApplyB1OperatingLeases` if it also calls `ProcessOperatingLeaseAdjustment` (check call sites). Forward `ctx` to `leaseCalculator.CalculatePresentValue(ctx, ...)`.

Test:

```go
func TestB1LeasePV_HonorsContextCancellation(t *testing.T) {
    la := buildLiabilityAdjusterForB1(t)
    data := buildB1FiringFixture(t)
    cleaningCtx := &entities.CleaningContext{ /* ... */ }
    rule := &entities.CleaningRule{ID: "operating_lease_capitalization", Enabled: true}

    ctx, cancel := context.WithCancel(context.Background())
    cancel()
    result := la.ProcessOperatingLeaseAdjustment(ctx, data, rule, cleaningCtx)
    // ProcessOperatingLeaseAdjustment returns *AdjustmentResult — assert
    // either Applied=false (fallback path engaged) or an error-tagged
    // result.Reasoning mentioning cancellation.
    if result.Applied {
        assert.Contains(t, result.Reasoning, "context",
            "cancelled ctx must surface via Reasoning when fallback path engages")
    }
}
```

### F.4 — MEDIUM-2 spec amendment

Edit `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-3-spec.md` §5.2. Replace the existing "rendered prompt" prose with the followup spec §4.4 text. Add changelog row.

No code in this commit. Smallest commit in the ladder.

### F.5 — LOW-1 json.Marshal error handling

```go
// internal/services/datacleaner/adjustments/hash.go

// (modified) — inner loop:
for _, k := range keys {
    b, err := json.Marshal(request.Context[k])
    if err != nil {
        ctx[k] = fmt.Sprintf("<unsupported:%T>", request.Context[k])
        continue
    }
    ctx[k] = string(b)
}

// (modified) — outer Marshal:
buf, err := json.Marshal(c)
if err != nil {
    panic(fmt.Sprintf("hash.go: encoding/json.Marshal failed on canonical hash input: %v", err))
}
return sha256Hex(string(buf))
```

Test:

```go
func TestSha256HexPromptCanonical_HandlesUnsupportedContextValues(t *testing.T) {
    // chan is not JSON-encodable; encoding/json returns *json.UnsupportedTypeError.
    req := &ai.FootnoteAnalysisRequest{
        Ticker:       "TEST",
        FootnoteText: "footnote",
        AnalysisType: ai.ContingentLiabilityAnalysis,
        Context: map[string]interface{}{
            "good_value":  "string",
            "bad_value":   make(chan int),  // unsupported by encoding/json
        },
    }
    hash := sha256HexPromptCanonical(req)
    assert.Len(t, hash, 64, "must still produce a 64-char hex digest")

    // Determinism: same unsupported type → same tag → same hash bytes.
    hash2 := sha256HexPromptCanonical(req)
    assert.Equal(t, hash, hash2)

    // Collision prevention: different unsupported types produce different hashes.
    req.Context["bad_value"] = make(chan string)
    hash3 := sha256HexPromptCanonical(req)
    assert.NotEqual(t, hash, hash3, "different unsupported types must produce different hashes")
}
```

### F.6 — LOW-2 godoc warning

Doc-only. Apply the comment block from followup spec §4.6 to `cleaneddata.CleanedFinancialData`.

### F.7 — LOW-3 identityCopy reflection test

Test-only. Implementation in followup spec §4.7.

### F.8 — LOW-4 Raw() deletion TODO

```go
// internal/services/datacleaner/cleaneddata/cleaned.go

// Raw returns the underlying *entities.FinancialData. Intended for the
// migration window only — Phase 4 consumers will read views directly.
// Returning the entity rather than a copy keeps the migration cheap;
// callers MUST treat it as read-only.
//
// TODO(phase-5): delete after Phase 4 consumer migration completes.
// Tracking: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md
// (Phase 5 row in "Phasing & implementation sequence").
func (c *CleanedFinancialData) Raw() *entities.FinancialData { /* ... */ }
```

Parent spec update: add a "Phase 5 — Delete `Raw()` migration-window escape hatch" entry under the "Phasing & implementation sequence" table.

### F.9 — Docs sweep

For each doc, append (do NOT replace) a Phase-3-followup summary. Templates:

**CLAUDE.md** — find the DC-1 Phase 3 SHIPPED bullet, append at the end:

> **DC-1 Phase 3 followup SHIPPED 2026-05-25** (branch `dc1-phase-3-followup`, single PR, ~10 commits). Closes 9 cross-model review findings: HIGH-1 (`Restated()` view-seed double-count fixed via pre-clean snapshot in `CleanFinancialDataWithViews`; `cleaneddata.New` signature now `(asReported, restated)`; ledger drives equity + DTA only — `DeltaAmount` is no longer re-applied because the dispatcher dual-write has already restated component fields), HIGH-2 + HIGH-3 (B3 collapsed to single `analyzeContingentLiabilityWithAI(ctx, ...)` call returning both probability AND provenance; `captureB3AIProvenance` deleted; amount-path ctx now honored), MEDIUM-1 (B1 lease PV ctx threading), MEDIUM-2 (Phase 3 spec §5.2 amended — `PromptHash` documented as canonical-request fingerprint, not literal LLM prompt-as-sent), LOW-1 through LOW-4. NON-goals preserved: no consumer migration, no B3 routing flip, no dual-write deletion, no `CalculationVersion` bump, no SchemaVersion bump. New load-bearing pin: `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire` — exercises the full pipeline on an A2-firing fixture; without it, HIGH-1 can silently re-regress.

**AGENTS.md** — row 17b: append in the same column:

> Phase 3 followup SHIPPED 2026-05-25 — 9 findings closed (HIGH-1 view-seed double-count, HIGH-2/3 B3 single AI call + ctx, MEDIUM-1 B1 ctx, MEDIUM-2 PromptHash spec amend, LOW-1..4); spec at `docs/refactoring/spec/dc1-phase-3-followup-spec.md`; closeout at `docs/refactoring/implementations/dc1-phase-3-followup-closeout.md`.

**THESIS.md** — DC-1 row description: append "Phase 3 followup (cross-model review fixes, 9 findings) SHIPPED 2026-05-25."

**Phase 3 closeout** — append section:

```
## Followup PR landed 2026-05-25

The Phase 3 single-PR merge surfaced 9 follow-on findings via independent
cross-model review (zen-mcp gpt-5.5). All 9 closed on branch
`dc1-phase-3-followup` (merge SHA: <fill at merge time>).

See [dc1-phase-3-followup-spec.md](../spec/dc1-phase-3-followup-spec.md)
and [dc1-phase-3-followup-closeout.md](./dc1-phase-3-followup-closeout.md).

| Finding | Severity | Disposition |
|---|---|---|
| HIGH-1 view-seed double-count | HIGH | SHIPPED (F.1) — pre-clean snapshot; ledger drives equity + DTA only |
| HIGH-2 B3 two-call divergence | HIGH | SHIPPED (F.2) — single AI call |
| HIGH-3 B3 amount-path ctx | HIGH | SHIPPED (F.2) — same commit as HIGH-2 |
| MEDIUM-1 B1 lease PV ctx | MED | SHIPPED (F.3) |
| MEDIUM-2 PromptHash naming drift | MED | SHIPPED (F.4) — spec amended |
| LOW-1 json.Marshal swallowing | LOW | SHIPPED (F.5) |
| LOW-2 cleaneddata mutex/Once | LOW | SHIPPED (F.6) — godoc warning |
| LOW-3 identityCopy coverage | LOW | SHIPPED (F.7) — reflection test |
| LOW-4 Raw() mutable escape hatch | LOW | SHIPPED (F.8) — TODO + Phase 5 plan |

Add a changelog row to this closeout dated 2026-05-25 referencing this section.
```

### F.10 — Followup closeout

Mirror Phase 3 closeout shape:

1. Header (status, branch, commits).
2. "What landed" — narrative summary.
3. "Commit ladder" table (oldest → newest, with task ID + finding ID).
4. "Finding disposition" table (all 9 SHIPPED).
5. "Load-bearing invariants" — same table as Phase 3 closeout, status GREEN.
6. "NON-goals honored" table.
7. "Phase 4 readiness" — confirm gate items still satisfied; add explicit "HIGH-1 fix unblocks Phase 4 consumer migration" note.
8. Change log.

---

## Test plan summary

After every task commit, run:

```bash
cd "/c/Users/Yonatan Levin/Documents/Programming/Projects/FinTech/Strade/midas-dc1-phase-3-followup"
go test ./...
```

Must exit 0. If any test fails, fix in the SAME commit before pushing.

After F.1 specifically, also verify shadow snapshots stay byte-identical:

```bash
go test -count=1 ./internal/integration/... -run TestDataCleanerRecompute_ShadowMode
git diff --quiet internal/integration/testdata/recompute-shadow/  # exit 0 required
```

After F.2 specifically, verify the B3 happy path produces exactly one AI call:

```bash
go test -count=1 ./internal/services/datacleaner/adjustments/... -run TestB3AISinglePath
```

After full ladder lands, run the load-bearing pin set explicitly:

```bash
go test -count=1 ./internal/services/valuation/models/... -run TestDDM_LegacyPath_BitForBit
go test -count=1 ./internal/services/datacleaner/... -run TestRecomputeUmbrellas_NoMutation
go test -count=1 ./internal/services/datacleaner/... -run TestOrchestrator_LedgerOrdering
go test -count=1 ./internal/integration/... -run TestLedger_BasketSnapshot
```

All must exit 0.

---

## Commit message templates

```
followup F.1: HIGH-1 — fix Restated() view-seed double-count

Problem: CleanFinancialDataWithViews wrapped the post-dual-write entity in
cleaneddata.New(raw). Restated() seeded from the post-dispatcher values
AND re-applied LedgerEntry.DeltaAmount, double-counting every Restater-role
adjuster (A2, A4, A5, C1/C2/C3/C5, C6).

Fix: capture a pre-clean snapshot in CleanFinancialDataWithViews; pass it
alongside the post-clean entity to cleaneddata.New(asReported, restated).
AsReported() reads the snapshot; Restated() seeds from the post-clean
entity and applies ONLY LedgerEntry.EquityOffset + TaxShieldDTA from the
ledger. DeltaAmount is no longer re-applied (it's already in the
post-clean component values via the dispatcher dual-write).

Regression pin: TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire
exercises the full pipeline on an A2-firing fixture. Without this test,
HIGH-1 can silently re-regress.

Spec: docs/refactoring/spec/dc1-phase-3-followup-spec.md §4.1
```

```
followup F.2: HIGH-2 + HIGH-3 — collapse B3 to single AI call with ctx

Problem: B3 invoked ai.AnalyzeFootnote TWICE per fire — once in
analyzeContingentLiabilityWithAI (amount) and once in captureB3AIProvenance
(provenance). LLM responses are non-deterministic; the recorded amount
and the recorded provenance could describe different responses, breaking
audit integrity. Additionally, the amount-path call used context.Background()
so request cancellation was not honored.

Fix: refactor analyzeContingentLiabilityWithAI to take ctx and return
(probability, *AIProvenance, metadata, error) from a single AI call.
Delete captureB3AIProvenance. ApplyB3Contingent reads provenance from
the unified return value.

Spec: docs/refactoring/spec/dc1-phase-3-followup-spec.md §4.2
```

(Repeat the pattern for F.3 .. F.10.)

---

## Change log

| Date | Change |
|---|---|
| 2026-05-25 | Initial plan authored by ARCH for Phase 3 followup BACKEND dispatch. 10-task ladder (F.1 through F.10) closing 9 cross-model review findings. Estimated 6–9 agent-hours single-shift. |
| 2026-05-25 | **All 10 tasks SHIPPED.** Commit ladder (oldest → newest): F.1 `ee9b2e9` (HIGH-1 view-seed double-count) → F.2 `d6312b0` (HIGH-2 + HIGH-3 B3 single AI call + ctx) → F.3 `e1fbe3f` (MEDIUM-1 B1 lease PV ctx threading) → F.5 `48aeee6` (LOW-1 hash.go json.Marshal error handling) → F.7 `6763e60` (LOW-3 identityCopy reflection test) → F.6 `49faba7` (LOW-2 godoc warning) → F.8 `7092654` (LOW-4 Raw() phase-5 TODO) → F.4 `31ed394` (MEDIUM-2 PromptHash spec amend) → F.9 (docs sweep) → F.10 (followup closeout). All 9 acceptance gates GREEN. Load-bearing invariants stayed GREEN at every commit. Closeout: `docs/refactoring/implementations/dc1-phase-3-followup-closeout.md`. |
