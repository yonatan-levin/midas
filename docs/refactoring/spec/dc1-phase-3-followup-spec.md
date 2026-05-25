# DC-1 Phase 3 Followup — Spec (Cross-Model Review Fixes)

**Status:** DESIGN (authored 2026-05-25, ready for BACKEND dispatch)
**Phase:** Followup PR on top of DC-1 Phase 3 (merge `46e84b1`); precedes Phase 4 dispatch
**Parent spec:** [datacleaner-component-primitive-and-parallel-views-phase-3-spec.md](datacleaner-component-primitive-and-parallel-views-phase-3-spec.md)
**Phase 3 closeout:** [datacleaner-component-primitive-and-parallel-views-phase-3-closeout.md](../implementations/datacleaner-component-primitive-and-parallel-views-phase-3-closeout.md)
**Implementer plan:** [dc1-phase-3-followup-implementation-plan.md](../implementations/dc1-phase-3-followup-implementation-plan.md)
**Worktree / branch:** `midas-dc1-phase-3-followup/` on branch `dc1-phase-3-followup` (created from master `46e84b1`)
**Estimated effort:** 1 agent shift (~6–9 agent-hours)

---

## 1. Summary

DC-1 Phase 3 merged to master as `46e84b1` after an in-session B-V-R-Q gate. An independent cross-model review (gpt-5.5 via `zen-mcp`) then surfaced **9 findings** the in-session gate did not catch:

| # | Severity | One-line |
|---|---|---|
| HIGH-1 | HIGH | `Restated()` double-counts Restater LedgerEntry deltas (post-dual-write seed + ledger reapply) |
| HIGH-2 | HIGH | B3 makes TWO AI calls (amount + provenance) that can produce divergent answers, breaking audit integrity |
| HIGH-3 | HIGH | The B3 amount-producing AI call uses `context.Background()` — upstream cancellation not honored |
| MEDIUM-1 | MED | B1 lease PV call uses `context.Background()` — ctx not threaded |
| MEDIUM-2 | MED | `PromptHash` hashes a canonical request, not the prompt-as-sent — naming/spec drift |
| LOW-1 | LOW | `json.Marshal` errors swallowed inside the prompt-canonical hash helper |
| LOW-2 | LOW | `cleaneddata.CleanedFinancialData` lazy memoization has no mutex / `sync.Once` |
| LOW-3 | LOW | `identityCopy` is a 25-field manual assignment with no guard against forgotten fields |
| LOW-4 | LOW | `Raw()` exposes the entity mutably; documented as read-only, not enforced |

This followup ships a single PR addressing all 9 findings. It preserves every Phase 3 NON-goal (no consumer migration, no B3 routing flip, no dual-write deletion, no `CalculationVersion` bump, no DDM golden regeneration, no changes to `recompute.go`).

The headline architectural decisions:

- **HIGH-1 fix — Option A (pre-clean snapshot inside `CleanFinancialDataWithViews`).** Deep-copy the input `*FinancialData` BEFORE calling `CleanFinancialData`, then construct the view wrapper from the pre-clean snapshot plus the post-clean ledger/overlays. Surgical to the new method; backwards-compatible with every existing `CleanFinancialData` caller. Memory cost: one extra `*FinancialData` value-copy per view call. `cleaneddata.New` signature extends from `(raw)` → `(asReported, restated, ledger, overlays)` — see §4.1.
- **MEDIUM-2 fix — Option (a) (amend spec).** The canonical-request hash IS the right concept for replay determinism — the AI client at this layer does not expose a single rendered-prompt string, and the canonical-request hash satisfies the underlying intent (deterministic identification of the inputs that produced the response). Rename `PromptHash` interpretation in the spec from "prompt-as-sent" to "canonical-request fingerprint"; preserve the field name on `AIProvenance` (backwards-compatible).

---

## 2. Goals

1. Eliminate the `Restated()` double-count for every Restater-role adjuster (A2, A4, A5, C1, C2, C3, C5, C6). After the fix, `Restated().X == data.X` (post-clean) for every Restater-touched component on the happy path; the ledger drives equity offsets and DTA shields only.
2. Collapse B3's two AI calls into one. The single call produces the amount AND the provenance, eliminating the audit divergence.
3. Thread real request-scoped `ctx` through every remaining `context.Background()` call site inside the cleaner's AI / lease-PV paths.
4. Reconcile the `PromptHash` naming drift (spec §5.2) so the spec describes what the implementation actually does.
5. Harden `hash.go`'s error handling so future input types do not silently produce hash collisions.
6. Add a non-trivial regression test that would catch HIGH-1 if it ever returns: full `CleanFinancialDataWithViews` pipeline on a fixture that fires A2 (a Restater), asserting `Restated().OtherIntangibles == originalValue - writedown` (not `originalValue - 2*writedown`).
7. Document `cleaneddata` package's goroutine-safety contract.
8. Pin `identityCopy` against forgotten fields via a reflection-based test.
9. Mark `Raw()` for deletion in Phase 5.

## 3. Non-goals (Phase 3 NON-goals continue to hold)

| NON-goal | Honored |
|---|---|
| No consumer migration (the 13 `data.*` read sites in `internal/services/valuation/` stay unchanged) | YES |
| No B3 routing flip (`data.TotalDebt` dual-write for B3 stays in dispatcher) | YES |
| No dispatcher dual-write deletion | YES |
| No `CalculationVersion` bump (stays at `"4.2"`) | YES |
| No translator extraction | YES |
| No changes to `recompute.go` | YES |
| No DDM golden regeneration | YES |

Out of scope items that surfaced during finding triage but stay deferred:

- Replacing `Raw()` with an actual immutable wrapper (Phase 5 — full Phase 4 consumer-migration must happen first so `Raw()`'s call-site list shrinks to zero).
- Promoting `cleaneddata` accessors to `sync.Once`-protected reads (sufficient for Phase 3 followup is the godoc warning; full parallel-safe consumers belong in a later phase if and when they arrive).
- Refactoring `ai.AnalyzeFootnote` to expose a rendered prompt string (MEDIUM-2 Option (b)) — out of scope under Option (a).

---

## 4. Architectural decisions

### 4.1 HIGH-1 — `Restated()` view-seed double-count

**Chosen fix: Option A — pre-clean snapshot inside `CleanFinancialDataWithViews`.**

#### Root cause

`CleanFinancialDataWithViews` calls `CleanFinancialData` first (which mutates `*data` in place via the dispatcher dual-writes), then wraps the resulting `result.CleanedData` in `cleaneddata.New(result.CleanedData)`. The wrapper holds the **post-dual-write** entity as `raw`.

When `Restated()` runs:
1. `v := identityCopy(c.raw)` seeds the view with the post-dual-write field values (e.g., `v.OtherIntangibles = 70` after A2 fired with a $30 writedown on an original $100).
2. The reducer adds `e.DeltaAmount = -30` from the LedgerEntry → `v.OtherIntangibles = 40`. **Double-counted.**

For every Restater-role adjuster (A2 OtherIntangibles, A4 Inventory, A5 Inventory + TaxShieldDTA, C1/C2/C3/C5 NormalizedOperatingIncome, C6 InterestExpense) the dispatcher dual-writes a delta to `data.X` AND the LedgerEntry carries the same delta as `DeltaAmount`. The double-count is universal across Restaters.

Empirically the bug does not show up in the existing test suite because:
- `TestCleanedFinancialData_Restated_BitForBitOnNoFiredAdjusters` exercises only the empty-ledger case.
- `TestCleanedFinancialData_Restated_C6EquityOffsetZero` synthesizes a `FinancialData` directly (not via the dispatcher) so there is no dual-write to double-count against.
- `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` asserts only `Restated.TotalLiabilities`, which depends on `TotalDebt + OperatingLeaseLiability + Other*Liabilities` — none are Restater-touched fields. (B-rules are OverlayEmitters: empty `Component` on the LedgerEntry, so the reducer's `applyLedgerEntryToView` skips them via the `default:` arm.)

Latent in Phase 3 (NON-goal: no consumer migration). **Breaks Phase 4 the moment any consumer reads `Restated().OtherIntangibles` / `.Inventory` / `.NormalizedOperatingIncome` / `.InterestExpense` / `.DeferredTaxAssets`.**

#### Why Option A over B and C

| Option | Pros | Cons | Verdict |
|---|---|---|---|
| **A — Pre-clean snapshot inside `CleanFinancialDataWithViews`** | Surgical; only `CleanFinancialDataWithViews` + `cleaneddata.New` change; existing `CleanFinancialData` callers untouched | One extra value-copy of `FinancialData` per call; `cleaneddata.New` signature extends | **CHOSEN** |
| **B — Stash snapshot on `CleaningResult`** | Single deep-copy per pipeline call; reusable "what changed?" snapshot for other consumers | Changes `CleaningResult` shape — replay-bundle implications (`10-clean-trace.json` serialization grows a field); broader blast radius for one consumer | Not chosen — no Phase 3-followup consumer needs the snapshot beyond view reconstruction |
| **C — Make cleaner non-mutating (return a new entity)** | Eliminates the entire dual-write problem | Phase 4 scope; massive refactor | Not chosen — explicitly out of Phase 3-followup scope |

Option A's memory cost is bounded: a `*FinancialData` value-copy is ~1KB per period (the struct is large but contains no nested slices that grow with portfolio size). On the live API path, a single per-request copy is negligible against the existing per-request allocations.

#### Sketch — new shape

```go
// internal/services/datacleaner/cleaneddata/cleaned.go

type CleanedFinancialData struct {
    // asReportedSnapshot is the PRE-CLEAN input — captured before
    // CleanFinancialData mutates anything via dispatcher dual-writes.
    // AsReported() returns an identity copy of THIS.
    asReportedSnapshot *entities.FinancialData

    // restated is the POST-CLEAN entity (output of CleanFinancialData).
    // Restater dual-writes have already been applied to its fields.
    // Restated() returns an identity copy of THIS, then applies ONLY
    //   - LedgerEntry.EquityOffset → StockholdersEquity
    //   - LedgerEntry.TaxShieldDTA → DeferredTaxAssets
    // (NOT LedgerEntry.DeltaAmount — that delta is already in restated's
    // component fields via the dispatcher's dual-write.)
    restated *entities.FinancialData

    // Memoized views.
    asReportedView *FinancialDataView
    restatedView   *FinancialDataView
    investedCap    *FinancialDataView
}

// New constructs a CleanedFinancialData with explicit AsReported (pre-
// clean) + Restated (post-clean) entities. Caller MUST NOT mutate either
// after the call returns.
//
// Phase 3 followup: this signature replaces the single-entity New(raw)
// constructor. Callers other than CleanFinancialDataWithViews are limited
// to test code; they update to pass either two snapshots (when the test
// is exercising the bit-for-bit-no-fired-adjusters property) or the same
// pointer twice (when the test is synthesizing a CleanedFinancialData
// from scratch without going through the cleaner — the legacy
// single-entity construction shape).
func New(asReported, restated *entities.FinancialData) *CleanedFinancialData {
    return &CleanedFinancialData{
        asReportedSnapshot: asReported,
        restated:           restated,
    }
}
```

```go
// internal/services/datacleaner/service.go

func (s *service) CleanFinancialDataWithViews(
    ctx context.Context,
    data *entities.FinancialData,
) (*entities.CleaningResult, *cleaneddata.CleanedFinancialData, error) {
    // Capture pre-clean snapshot BEFORE the dispatcher mutates anything.
    // entities.FinancialData has no nested mutable state that the
    // dispatcher modifies on the happy path (the AdjustmentLedger /
    // Overlays slices are appended POST-clone in CleanFinancialData,
    // so the pre-clean snapshot's slices stay nil); a value-copy
    // captures every monetary field by value.
    var snapshot entities.FinancialData
    if data != nil {
        snapshot = *data
    }

    result, err := s.CleanFinancialData(ctx, data)
    if err != nil {
        return nil, nil, err
    }
    if result == nil {
        return nil, nil, nil
    }
    return result, cleaneddata.New(&snapshot, result.CleanedData), nil
}
```

#### Sketch — updated reducer

```go
// internal/services/datacleaner/cleaneddata/restate.go

func (c *CleanedFinancialData) Restated() *FinancialDataView {
    if c == nil {
        return zeroView(RestatedView)
    }
    if c.restatedView != nil {
        return c.restatedView
    }
    // SEED: identity copy of the POST-CLEAN entity. Dispatcher dual-writes
    // have already restated every Restater-touched component.
    v := identityCopy(c.restated)
    v.ViewKind = RestatedView

    if c.restated != nil {
        // The ledger drives equity offsets + DTA shields ONLY. We MUST NOT
        // re-apply DeltaAmount to v.Component — that delta is already in
        // c.restated's component fields. Re-applying would double-count.
        for _, e := range c.restated.AdjustmentLedger {
            if !e.Fired {
                continue
            }
            v.StockholdersEquity += e.EquityOffset
            v.DeferredTaxAssets += e.TaxShieldDTA
        }
        // Recompute umbrellas from components + plug. Identical to today;
        // the components are the post-dual-write values which is correct.
        v.CurrentAssets = c.restated.CashAndCashEquivalents + v.Inventory + c.restated.OtherCurrentAssets
        v.TotalAssets = v.CurrentAssets +
            v.Goodwill +
            v.OtherIntangibles +
            v.DeferredTaxAssets +
            c.restated.OtherNonCurrentAssets
        v.CurrentLiabilities = c.restated.OperatingLeaseLiabilityCurrent + c.restated.OtherCurrentLiabilities
        v.TotalLiabilities = v.CurrentLiabilities +
            v.TotalDebt +
            c.restated.OperatingLeaseLiabilityNoncurrent +
            c.restated.OtherNonCurrentLiabilities
        v.TangibleAssets = v.TotalAssets - v.Goodwill - v.OtherIntangibles
    }

    c.restatedView = &v
    return c.restatedView
}
```

`applyLedgerEntryToView` is **deleted**. The per-component switch existed solely to route `DeltaAmount` to the correct field, which is precisely the work we no longer do. Its removal also removes the silent-skip fail-soft branch (LOW-1-shaped potential for hidden bugs) and is the cleanest expression of "ledger drives equity + DTA, not components."

#### Side effect: T2-BS-3 carve-out semantics stay correct

The carve-out invariant is `AsReported.TotalLiabilities == 0` AND `Restated.TotalLiabilities > 0` (component-sum reconstruction). After the fix:
- `AsReported.TotalLiabilities` now comes from the pre-clean snapshot (parser-stamped value). For AMD/KO this is `0` — invariant preserved.
- `Restated.TotalLiabilities` is recomputed from the post-clean entity's components (which include the umbrella-fix-up done by the cleaner's recompute / dual-write paths) → positive truthful value for AMD/KO. Invariant preserved.

`TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` continues to pass without modification. Documented in §6 (test plan) as a load-bearing invariant pin.

### 4.2 HIGH-2 + HIGH-3 — Collapse the two B3 AI calls into one with proper ctx

**Chosen fix:** Refactor `analyzeContingentLiabilityWithAI` to return `(probability, *entities.AIProvenance, error)` from a SINGLE call, with `ctx context.Context` as the first parameter. Delete `captureB3AIProvenance` as a separate function. `ProcessContingentLiabilityAdjustment` and `ApplyB3Contingent` both consume the unified result.

#### Root cause

`ProcessContingentLiabilityAdjustment` (legacy entry point) calls `analyzeContingentLiabilityWithAI(data, cleaningCtx)` to produce the **amount** (probability × total). `ApplyB3Contingent` (Phase 2 native Adjuster path) then calls `captureB3AIProvenance(ctx, working, cleaningCtx, now)` to capture **provenance**. Both calls invoke `la.aiService.AnalyzeFootnote(ctx, request)` with structurally-identical requests, but:

- **HIGH-2:** LLM responses are non-deterministic. Call #1 may return `Probability=0.40`; call #2 may return `Probability=0.42`. The `OverlaySpec.Amount` reflects call #1; the stamped `AIProvenance.Probability` / `Confidence` / `ExtractedSpan` reflect call #2. The provenance no longer describes the call that produced the recorded amount. Audit-integrity violation.
- **HIGH-3:** `analyzeContingentLiabilityWithAI` uses `ctx := context.Background()` (line 1658). Request-scoped cancellation does NOT propagate to the amount call. The Phase 3 closeout claim "B3 AI path now respects upstream cancellation" is only true for the provenance call (call #2), not the amount call (call #1). Spec §5.3 explicitly cites this gap as motivation for ctx threading.

The hashes (`PromptHash`/`SourceDocHash`) are deterministic functions of the request inputs (timestamp-free canonical serialization), so they are independent of which call computes them — but the response-derived fields are not.

#### Sketch — new signature

```go
// internal/services/datacleaner/adjustments/liabilities.go

// analyzeContingentLiabilityWithAI runs the B3 AI footnote analysis ONCE
// and returns both the probability (for amount computation) and the
// AIProvenance (for OverlaySpec.AIProvenance stamping). Single-call
// semantics ensure the recorded provenance describes the same response
// that produced the recorded amount.
//
// Hash computation is performed PRE-API-CALL so a network failure leaves
// no partial/inconsistent hash. The hashes are deterministic functions
// of the request inputs; they are independent of the model response.
func (la *LiabilityAdjuster) analyzeContingentLiabilityWithAI(
    ctx context.Context,
    data *entities.FinancialData,
    cleaningCtx *entities.CleaningContext,
    timestamp time.Time,
) (probability float64, provenance *entities.AIProvenance, metadata map[string]string, err error) {
    if ctx == nil {
        ctx = context.Background() // defensive guard for tests calling directly
    }

    footnoteText := cleaningCtx.FootnoteText
    if footnoteText == "" {
        footnoteText = fmt.Sprintf("Company disclosed contingent liabilities of $%.0f related to litigation and other potential exposures.",
            data.ContingentLiabilities+data.EnvironmentalLiabilities+data.LitigationLiabilities)
    }

    request := &ai.FootnoteAnalysisRequest{
        Ticker:           data.Ticker,
        FilingType:       data.FilingPeriod,
        FootnoteText:     footnoteText,
        AnalysisType:     ai.ContingentLiabilityAnalysis,
        PriorityLevel:    ai.PriorityNormal,
        RequestTimestamp: timestamp,
        Context: map[string]interface{}{
            "industry_code":           cleaningCtx.IndustryCode,
            "total_contingent_amount": data.ContingentLiabilities + data.EnvironmentalLiabilities + data.LitigationLiabilities,
            "revenue":                 data.Revenue,
        },
    }

    // Pre-API-call hashes — deterministic, response-independent.
    promptHash := sha256HexPromptCanonical(request)
    sourceDocHash := sha256Hex(footnoteText)

    response, callErr := la.aiService.AnalyzeFootnote(ctx, request)
    if callErr != nil {
        return 0, nil, nil, fmt.Errorf("AI service call failed: %w", callErr)
    }
    if response == nil {
        return 0, nil, nil, fmt.Errorf("AI service returned nil response")
    }
    if response.Error != "" {
        return 0, nil, nil, fmt.Errorf("AI service returned error: %s", response.Error)
    }

    // Parse probability + ExtractedSpan from the response (same logic both
    // helpers shared before — consolidate here, delete the duplicate
    // in captureB3AIProvenance).
    extractedData, ok := response.ExtractedData["contingent_liability_estimate"]
    if !ok {
        return 0, nil, nil, fmt.Errorf("AI response missing contingent liability estimate")
    }
    prob, span, parseErr := parseContingentLiabilityEstimate(extractedData, response.Confidence)
    if parseErr != nil {
        return 0, nil, nil, parseErr
    }
    if prob < 0.0 || prob > 1.0 {
        return 0, nil, nil, fmt.Errorf("AI returned invalid probability: %.2f%%", prob*100)
    }

    provenance = &entities.AIProvenance{
        ModelName:     b3AIModelName,
        PromptHash:    promptHash,
        SourceDocHash: sourceDocHash,
        ExtractedSpan: span,
        Probability:   prob,
        Confidence:    response.Confidence,
        Timestamp:     timestamp,
    }

    metadata = map[string]string{
        "ai_confidence":      fmt.Sprintf("%.2f", response.Confidence),
        "ai_model_used":      b3AIModelName,
        "ai_processing_time": response.ProcessingTime.String(),
        "ai_probability":     fmt.Sprintf("%.2f%%", prob*100),
        "analysis_type":      string(response.AnalysisType),
        "request_id":         response.RequestID,
    }

    return prob, provenance, metadata, nil
}

// parseContingentLiabilityEstimate handles both the struct form (mock
// service) and the map form (HTTP service). Extracted from the legacy
// helpers so the unified single-call path stays readable.
func parseContingentLiabilityEstimate(extractedData interface{}, fallbackConfidence float64) (prob float64, extractedSpan string, err error) {
    // ... existing branching from analyzeContingentLiabilityWithAI / captureB3AIProvenance,
    //     consolidated. Returns prob ∈ [0, 1] (caller validates), extractedSpan,
    //     and a typed error.
}
```

`captureB3AIProvenance` is **deleted**. `ApplyB3Contingent` calls `la.analyzeContingentLiabilityWithAI(ctx, working, cleaningCtx, now)` directly and reads the `provenance` return value. `ProcessContingentLiabilityAdjustment` calls the same function but discards the provenance (the legacy path doesn't surface it on `*AdjustmentResult`).

The unified call site eliminates the audit divergence (one response feeds both amount and provenance) AND fixes HIGH-3 (the ctx parameter flows through).

#### Why this is a single PR commit (HIGH-2 + HIGH-3 together)

The two findings cannot be cleanly separated: collapsing to one call automatically threads ctx through the amount path (HIGH-3's fix is a side effect of HIGH-2's). Splitting them would mean either (a) fixing HIGH-3 first by adding ctx to the broken two-call path (touching code that's about to be deleted), or (b) fixing HIGH-2 first by collapsing while leaving `context.Background()` in place (defeating the cancellation invariant the fix is supposed to establish). Single commit is cleaner.

### 4.3 MEDIUM-1 — B1 lease PV ctx threading

**Mechanical fix.** `ProcessOperatingLeaseAdjustment` (line 652 of `liabilities.go`) currently does `ctx := context.Background()` on line 654 with a `// TODO` comment. The fix:

1. Add `ctx context.Context` as the first parameter of `ProcessOperatingLeaseAdjustment`.
2. Remove the `ctx := context.Background()` line and the TODO.
3. Forward the dispatcher's `ctx` at the call site (the dispatcher already has `ctx` in scope post-Phase-3).
4. Update `ApplyB1OperatingLeases` to pass its received ctx through.
5. Update all test callers to pass `context.Background()` / `context.TODO()`.

The `leaseCalculator.CalculatePresentValue(ctx, data, cleaningContext)` already accepts ctx — the threading is end-to-end the moment the dispatcher line is updated.

### 4.4 MEDIUM-2 — PromptHash naming-vs-spec drift

**Chosen fix: Option (a) — amend the spec.**

#### Why Option (a) over Option (b)

The Phase 3 spec §5.2 says `PromptHash` = SHA-256 of "the rendered prompt (after template substitution: ticker, period, footnote text inserted) … the prompt-as-sent." The implementation hashes a canonical-request shape (selected fields of `ai.FootnoteAnalysisRequest`, timestamp-stripped, sorted Context keys) — not the literal LLM prompt string.

Option (b) (refactor the implementation to expose the rendered prompt string from the AI client) requires:
- A new method on the `ai.Service` interface (`RenderPrompt(req)` or `BuildPromptString(req)`).
- A guarantee from every concrete implementation (`MockAIService`, HTTP-backed service) that the rendered string is byte-identical across calls with the same inputs.
- A coupling between the test layer (which would need to capture/compare the rendered string) and the AI client internals.

Option (a) (amend the spec) acknowledges that the canonical-request fingerprint is the correct concept for replay determinism. The hash's job is to identify "what inputs produced this response?" The canonical-request shape:
- Excludes `RequestTimestamp` (necessary — wall-clock drift defeats determinism).
- Sorts Context map keys (necessary — Go map iteration is non-deterministic).
- Captures the substituted inputs (`Ticker`, `FootnoteText`, etc.) that DO drive the prompt content.

If the AI client's prompt template ever changes (e.g., a new system prompt), the prompt-as-sent would change but the canonical-request would NOT — and that is the correct behavior for replay: prompt-template drift is a model-side concern, not an input-side concern, and the existing `ModelName` field on `AIProvenance` already disambiguates model versions.

#### Spec amendment

Spec §5.2 will be updated in this followup's commit ladder. The new text:

```
PromptHash = SHA-256 hex of a deterministic, timestamp-free canonical
serialization of the ai.FootnoteAnalysisRequest fields (Ticker, FilingType,
FootnoteText, AnalysisType, PriorityLevel, Context with sorted keys).

This is a CANONICAL-REQUEST FINGERPRINT — not the literal LLM prompt string.
Two calls with structurally-equal request inputs produce identical PromptHash
values regardless of (a) wall-clock or (b) LLM prompt-template version.

Prompt-template drift is captured separately by AIProvenance.ModelName,
which records the model + template version that generated the response.
PromptHash answers "what inputs?"; ModelName answers "what model/template?".
```

The implementation behavior in `sha256HexPromptCanonical` is unchanged. The fix is documentation alignment; no code changes for MEDIUM-2 itself (other LOW items in `hash.go` are addressed in §4.5).

### 4.5 LOW-1 — `json.Marshal` error swallowing in `hash.go`

**Chosen fix:** Emit a typed `<unsupported:%T>` tag rather than empty string. Today's Context values (string, float64, int) cannot produce Marshal errors; the fix guards against future callers that might pass `chan`, `func`, or cyclic types.

#### Sketch

```go
// internal/services/datacleaner/adjustments/hash.go

func sha256HexPromptCanonical(request *ai.FootnoteAnalysisRequest) string {
    if request == nil {
        return sha256Hex("")
    }

    // ... canonical struct definition unchanged ...

    ctx := make(map[string]string, len(request.Context))
    keys := make([]string, 0, len(request.Context))
    for k := range request.Context {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    for _, k := range keys {
        b, err := json.Marshal(request.Context[k])
        if err != nil {
            // Future-proofing: encoding/json fails on chan/func/cyclic
            // structures. Surface the type explicitly rather than producing
            // an empty hash input (which would collide silently across
            // structurally-different values).
            ctx[k] = fmt.Sprintf("<unsupported:%T>", request.Context[k])
            continue
        }
        ctx[k] = string(b)
    }

    c := canonical{ /* ... */ Context: ctx }
    buf, err := json.Marshal(c)
    if err != nil {
        // c.Context is map[string]string + scalar string fields — Marshal
        // cannot fail on this shape. If Go's stdlib breaks that invariant
        // we want a LOUD failure, not silent hash collision.
        panic(fmt.Sprintf("hash.go: encoding/json.Marshal failed on canonical hash input: %v", err))
    }
    return sha256Hex(string(buf))
}
```

The inner-loop tag preserves hash determinism (same unsupported type → same tag → same hash bytes) while making the type visible if a future field hits this path. The outer-Marshal panic is justified because the outer shape (`canonical` struct) has no path-to-Marshal-failure — if Go's stdlib ever regresses there, we want a loud failure at hash time, not silent corruption downstream.

### 4.6 LOW-2 — Goroutine-safety contract on `CleanedFinancialData`

**Chosen fix:** godoc warning on the struct. No code change.

#### Rationale

Phase 3 / Phase 4 consumers all run on the same request goroutine — `CleanFinancialDataWithViews` returns a wrapper that lives only as long as the request's call stack. There is no current code path that shares a `*CleanedFinancialData` across goroutines.

Adding `sync.Once` or a `sync.Mutex` now (a) costs an atomic load on every accessor call on the happy path (negligible but not free), (b) introduces a contract Phase 4 must honor (every consumer must understand the locking model), and (c) is premature optimization given zero current parallel consumers.

A loud godoc warning is sufficient. If a Phase 5+ feature surfaces a parallel-read consumer (e.g., a batch valuation endpoint), the `sync.Once` retrofit is a 5-line change to the three accessor methods.

#### Sketch

```go
// internal/services/datacleaner/cleaneddata/cleaned.go

// CleanedFinancialData wraps a post-clean *entities.FinancialData together
// with its AdjustmentLedger and Overlays and exposes three semantically-
// distinct views over the underlying entity: AsReported, Restated, and
// InvestedCapital. Views are computed on first access and memoized on the
// struct.
//
// GOROUTINE-SAFETY: NOT goroutine-safe. The accessor methods (AsReported,
// Restated, InvestedCapital) lazily populate cached *FinancialDataView
// pointers without locking. Do NOT share a single *CleanedFinancialData
// across goroutines without external synchronization. Phase 3 / Phase 4
// consumers all run on a single request goroutine, which is sufficient
// for current use cases; a future parallel-read consumer would need a
// sync.Once retrofit (see TODO in cleaned.go).
//
// Phase 3 invariant: NO production consumer reads from CleanedFinancialData
// yet ...
type CleanedFinancialData struct { /* ... */ }
```

### 4.7 LOW-3 — `identityCopy` field-coverage guard

**Chosen fix:** Reflection-based test that enumerates `FinancialDataView` fields, builds a `*entities.FinancialData` with a distinct non-zero value for each field that maps to a `FinancialData` field, runs `identityCopy`, and asserts every output field is non-zero.

#### Test sketch

```go
// internal/services/datacleaner/cleaneddata/asreported_coverage_test.go

func TestIdentityCopy_CoversEveryViewField(t *testing.T) {
    // Build a FinancialData with a deliberately distinct non-zero
    // value for every field that identityCopy is supposed to copy.
    // Float fields get the field's reflect-index as a sentinel value;
    // string fields get the field name; time.Time gets a non-zero date.
    raw := &entities.FinancialData{}
    rawV := reflect.ValueOf(raw).Elem()
    for i := 0; i < rawV.NumField(); i++ {
        f := rawV.Field(i)
        if !f.CanSet() {
            continue
        }
        switch f.Kind() {
        case reflect.Float64:
            f.SetFloat(float64(i + 1) * 1e6) // 1M, 2M, 3M ... unique per field
        case reflect.String:
            f.SetString(rawV.Type().Field(i).Name)
        case reflect.Struct:
            if t, ok := f.Interface().(time.Time); ok {
                f.Set(reflect.ValueOf(t.Add(24 * time.Hour))) // non-zero
            }
        }
    }

    out := identityCopy(raw)

    // For every field on FinancialDataView that has a matching named
    // field on FinancialData, assert the output field is non-zero.
    // (ViewKind, DebtLikeClaims are view-only — exempt.)
    viewV := reflect.ValueOf(out)
    viewT := viewV.Type()
    exempt := map[string]bool{"ViewKind": true, "DebtLikeClaims": true}
    for i := 0; i < viewT.NumField(); i++ {
        name := viewT.Field(i).Name
        if exempt[name] {
            continue
        }
        rawField := rawV.FieldByName(name)
        if !rawField.IsValid() {
            // View has a field with no entity counterpart — fine
            // (e.g., future synthesized fields). Document via exempt
            // map when added.
            continue
        }
        // Output field must be non-zero (copied) when the entity field
        // is non-zero.
        outField := viewV.Field(i)
        require.False(t, outField.IsZero(),
            "identityCopy missed field %s — add it to asreported.go's "+
                "identityCopy() helper", name)
    }
}
```

Runtime: reflection-based, runs on every `go test ./internal/services/datacleaner/cleaneddata/...`. Adds <1ms to the test suite.

### 4.8 LOW-4 — `Raw()` deletion TODO

**Chosen fix:** Add a `// TODO(phase-5): delete after consumer migration completes — see docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` comment on `Raw()`. Record the deletion as a Phase 5 task in the parent spec's "Phasing & implementation sequence" section.

No code-behavior change. The comment is documentation-only.

---

## 5. Pipeline integration

Only `Service.CleanFinancialDataWithViews` and `cleaneddata.New` change shape. `Service.CleanFinancialData` is untouched. Every Phase-3-era caller of `CleanFinancialDataWithViews` is updated in the same commit as the signature change (the call sites are limited to the new T2-BS-3 integration test).

`MockDataCleanerService` in `internal/services/valuation/service_test.go` gains the same change.

---

## 6. Backwards compatibility

| Surface | Followup state |
|---|---|
| `CleanFinancialData(ctx, data)` signature | UNCHANGED |
| `CleanFinancialDataWithViews(ctx, data)` signature | UNCHANGED — the wrapper internals change but the (return) shape is identical |
| `cleaneddata.New(raw)` → `cleaneddata.New(asReported, restated)` | **CHANGED** — `cleaneddata.New` takes TWO `*FinancialData` pointers instead of one. Every existing call site updates. Phase 3-era call sites today: `CleanFinancialDataWithViews` (production) + Phase 3 unit tests + the T2-BS-3 integration test. |
| `Restated()` numeric semantics | **CHANGED on Restater-firing tickers** — previously double-counted; now correct. This is the bug fix. No production consumer is affected (NON-goal: no consumer migration). Unit tests that explicitly asserted the (buggy) double-count must be updated; see §7. |
| `analyzeContingentLiabilityWithAI` signature | **CHANGED** — internal method, takes `ctx` + returns provenance |
| `captureB3AIProvenance` | **DELETED** |
| DCF / WACC / DDM / FFO / Graham outputs | Bit-for-bit unchanged (no consumer migration) |
| `TestDDM_LegacyPath_BitForBit` GREEN | Stays GREEN |
| Shadow snapshots | Byte-identical |
| Replay golden bundles | No structural drift expected (the populated `ai_provenance.prompt_hash` / `source_doc_hash` fields don't change; the only change is whether they describe call #1 or call #2, which is invisible at the JSON level for matching inputs) |

---

## 7. Test plan

### 7.1 New tests (required)

1. **`TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire`** — **THE HIGH-1 REGRESSION PIN.** Construct a synthetic `*entities.FinancialData` with `OtherIntangibles=$100M`, `IndefiniteLivedIntangibles=$60M`, `EffectiveTaxRate=0.25`, ticker + sector wiring sufficient to trigger A2's `intangible_writedown` rule. Run the FULL pipeline: `s.CleanFinancialDataWithViews(ctx, data)`. Assert:
   - `views.AsReported().OtherIntangibles == 100_000_000` (pre-clean value preserved).
   - `views.Restated().OtherIntangibles == 100_000_000 - writedown` where `writedown` is the A2 rule's emitted delta (one application of the delta, NOT two).
   - `views.Restated().DeferredTaxAssets == data.DeferredTaxAssets + writedown * 0.25` (Q2 TaxShieldDTA application).
   - `views.Restated().StockholdersEquity == data.StockholdersEquity - writedown` (equity offset from the LedgerEntry).
   - `views.AsReported().StockholdersEquity == data.StockholdersEquity + writedown` (pre-clean equity, before A2's equity-offset hit).

   This test is **load-bearing**. Without it, HIGH-1 can silently re-regress under any future refactor that changes the seed/ledger contract.

2. **`TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnEarningsFire`** — Same shape as #1 but for an earnings-side Restater (C1 restructuring is convenient). Assert `views.Restated().NormalizedOperatingIncome` matches one application of the C1 delta.

3. **`TestB3AISinglePath_AmountAndProvenance_AreConsistent`** — HIGH-2 regression pin. Stub `MockAIService.AnalyzeFootnote` with a call-counter. Run `ApplyB3Contingent` end-to-end on a B3-firing fixture. Assert:
   - The mock was invoked **exactly once** (not twice).
   - `overlay.Amount = totalContingent * overlay.AIProvenance.Probability` (the recorded amount derives from the same probability as the recorded provenance).

4. **`TestB3AmountAICall_HonorsContextCancellation`** — HIGH-3 regression pin. Create `ctx, cancel := context.WithCancel(parent); cancel()` before calling `ApplyB3Contingent`. Assert the dispatcher returns / the AI call errors with `context.Canceled` (or the request was never actually made — both are acceptable signals of honored cancellation).

5. **`TestB1LeasePV_HonorsContextCancellation`** — MEDIUM-1 regression pin. Cancel ctx; call `ProcessOperatingLeaseAdjustment` (or `ApplyB1OperatingLeases`). Assert cancellation propagates to `leaseCalculator.CalculatePresentValue`.

6. **`TestIdentityCopy_CoversEveryViewField`** — LOW-3 reflection-based field-coverage test.

7. **`TestSha256HexPromptCanonical_HandlesUnsupportedContextValues`** — LOW-1 regression pin. Build an `ai.FootnoteAnalysisRequest` whose `Context` map contains a value of unsupported type (e.g., `chan int` or a cyclic struct). Assert `sha256HexPromptCanonical` returns a non-empty hash AND that the `<unsupported:%T>` tag changes the hash from the unsupported-omitted case (collision prevention).

### 7.2 Tests requiring updates

- **`TestCleanedFinancialData_Restated_BitForBitOnNoFiredAdjusters`** — `cleaneddata.New(raw)` callsite updates to `cleaneddata.New(raw, raw)` (no fired adjusters → no dispatcher mutation → pre/post identical).
- **`TestCleanedFinancialData_Restated_C6EquityOffsetZero`** — same `cleaneddata.New` update. The C6 invariant (EquityOffset=0 ⇒ Restated.StockholdersEquity == AsReported.StockholdersEquity) still holds and the assertion stays identical, because EquityOffset is still read from the LedgerEntry directly.
- Other unit tests that called `cleaneddata.New(raw)` with a single argument — search via `Grep` `cleaneddata.New\(` in the worktree.

### 7.3 Load-bearing invariants — must stay GREEN

| Invariant | Path |
|---|---|
| `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC math.Float64bits equality) | `internal/services/valuation/models/ddm_bitforbit_test.go` |
| `TestRecomputeUmbrellas_NoMutation` | `internal/services/datacleaner/recompute_test.go` |
| `TestOrchestrator_LedgerOrdering` | `internal/services/datacleaner/ledger_invariants_test.go` |
| Shadow snapshots byte-identical | `internal/integration/testdata/recompute-shadow/<TICKER>.json` (`git diff --quiet` exits 0) |
| `TestLedger_BasketSnapshot_ClusterPrediction` 10/10 | `internal/integration/datacleaner_ledger_basket_test.go` |
| `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` | same file — AMD/KO reconstruction stays correct |
| `TestQ2_A2TaxShieldDTA_Populated` | `internal/services/datacleaner/adjustments/a2_intangible_adjuster_test.go` |
| `TestQ4_AIProvenance_SHA256_Deterministic` | `internal/services/datacleaner/adjustments/liabilities_test.go` or `b3_*_test.go` |
| Full `go test ./...` exit 0 | (full suite) |

### 7.4 Replay drift expectation

`cmd/replay` against `artifacts/tier2-baseline/2026-05-19/`: zero numeric drift in `17-response.json` (no consumer migration). Zero structural drift in `13-cleaner-audit.json` (the hashes themselves are unchanged; the call site collapse is invisible at the JSON level).

---

## 8. Tasks by Agent

### BACKEND

1. **Task F.1** — HIGH-1 fix: pre-clean snapshot in `CleanFinancialDataWithViews`; `cleaneddata.New` signature change to `(asReported, restated *entities.FinancialData)`; `restate.go` reducer simplified (delete `applyLedgerEntryToView`). Update all unit-test callers of `cleaneddata.New`. Add `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire` + `OnEarningsFire`. Verify load-bearing invariants GREEN.
2. **Task F.2** — HIGH-2 + HIGH-3 fix: collapse `analyzeContingentLiabilityWithAI` + `captureB3AIProvenance` into one ctx-aware method; delete `captureB3AIProvenance`. Add `TestB3AISinglePath_AmountAndProvenance_AreConsistent` + `TestB3AmountAICall_HonorsContextCancellation`. Verify B3 AI flow tests stay GREEN.
3. **Task F.3** — MEDIUM-1 fix: thread `ctx` through `ProcessOperatingLeaseAdjustment` + `ApplyB1OperatingLeases`; delete `ctx := context.Background()` line; update test call sites; add `TestB1LeasePV_HonorsContextCancellation`.
4. **Task F.4** — MEDIUM-2 fix: amend Phase 3 spec §5.2 (`PromptHash` text). Doc change only. No code touched.
5. **Task F.5** — LOW-1 fix: `hash.go` json.Marshal error handling. Add `TestSha256HexPromptCanonical_HandlesUnsupportedContextValues`.
6. **Task F.6** — LOW-2 fix: godoc warning on `CleanedFinancialData`. Doc change only.
7. **Task F.7** — LOW-3 fix: add `TestIdentityCopy_CoversEveryViewField` reflection-based field-coverage test.
8. **Task F.8** — LOW-4 fix: `Raw()` deletion TODO comment + Phase 5 plan note in parent spec.
9. **Task F.9** — Docs sweep: CLAUDE.md DC-1 Phase 3 bullet (append followup summary); AGENTS.md row 17b (append followup); THESIS DC-1 row; the Phase 3 spec changelog; the Phase 3 closeout changelog; this spec's changelog; this followup's implementer plan changelog. **No new SchemaVersion bump.** **No CalculationVersion bump.**

### QA

- Run `go test ./...` and verify exit 0.
- Run `go test ./internal/services/datacleaner/...` with `-count=2` to verify memoization caches don't leak state across calls.
- Run `go run ./cmd/replay --diff-stages --workers=4 artifacts/tier2-baseline/2026-05-19/` (if a baseline is available) and verify zero numeric + zero structural drift.
- Manual smoke: hit `GET /v1/fair_value?ticker=AAPL` and verify `fair_value` matches the pre-followup value byte-for-byte.

### REVIEWER

Focus the review on:
- **Restated semantics** — the reducer change is load-bearing for Phase 4. Re-prove on paper: for any Restater-role adjuster, post-fix `Restated().X == (post-dual-write data).X`. For OverlayEmitter (B-rules), `Restated().X == (post-dual-write data).X` continues to hold because the reducer does not touch component fields and B-rules' equity offset is 0.
- **Single-call B3 audit** — `overlay.Amount` MUST equal `totalContingent * overlay.AIProvenance.Probability` after the fix. Reviewer should grep for any leftover call to `aiService.AnalyzeFootnote` inside the B3 code path beyond the single unified call.
- **ctx propagation** — verify no remaining `context.Background()` inside `internal/services/datacleaner/adjustments/` on a production code path (`Grep` `context\.Background\(\)` over `*.go` minus `_test.go`).
- **`cleaneddata.New` callsite enumeration** — every callsite updated; no surprise omissions in test files.
- **Spec amendment (MEDIUM-2)** — the new §5.2 text describes what the code does. Doc accuracy.

### ARCH (this document)

Produced by `/plan-and-create` dispatch on 2026-05-25.

---

## 9. Spec Updates

Within this followup PR:

| Doc | Update |
|---|---|
| `docs/refactoring/spec/dc1-phase-3-followup-spec.md` | NEW — this document |
| `docs/refactoring/implementations/dc1-phase-3-followup-implementation-plan.md` | NEW — implementer plan |
| `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-3-spec.md` | §5.2 rewrite for the MEDIUM-2 canonical-request-fingerprint amendment; changelog row dated 2026-05-25 |
| `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-3-closeout.md` | Append "Followup PR landed 2026-05-25" section + changelog row; cross-link this spec |
| `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` | Add a `Raw()` deletion entry to the Phase 5 sub-section of "Phasing & implementation sequence" |
| `CLAUDE.md` | Append a "Phase 3 followup SHIPPED 2026-05-25" bullet under the DC-1 Phase 3 row (3-5 line summary); preserve the existing Phase 3 bullet verbatim |
| `AGENTS.md` row 17b | Append "Phase 3 followup SHIPPED 2026-05-25" two-line summary |
| `docs/THESIS.md` | DC-1 row: append "Phase 3 followup (cross-model review fixes) SHIPPED 2026-05-25 — 9 findings closed: HIGH-1 view-seed double-count, HIGH-2/3 B3 single-AI-call, MEDIUM-1 B1 ctx, MEDIUM-2 spec amend, plus 4 LOWs." |

NO SchemaVersion bump. NO CalculationVersion bump.

---

## 10. Acceptance criteria

- [ ] `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire` GREEN
- [ ] `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnEarningsFire` GREEN
- [ ] `TestB3AISinglePath_AmountAndProvenance_AreConsistent` GREEN
- [ ] `TestB3AmountAICall_HonorsContextCancellation` GREEN
- [ ] `TestB1LeasePV_HonorsContextCancellation` GREEN
- [ ] `TestIdentityCopy_CoversEveryViewField` GREEN
- [ ] `TestSha256HexPromptCanonical_HandlesUnsupportedContextValues` GREEN
- [ ] `TestDDM_LegacyPath_BitForBit` GREEN (no regression)
- [ ] `TestRecomputeUmbrellas_NoMutation` GREEN
- [ ] `TestOrchestrator_LedgerOrdering` GREEN
- [ ] Shadow snapshots byte-identical (`git diff --quiet internal/integration/testdata/recompute-shadow/`)
- [ ] `TestLedger_BasketSnapshot_ClusterPrediction` 10/10 GREEN
- [ ] `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` GREEN
- [ ] `TestQ2_A2TaxShieldDTA_Populated` GREEN
- [ ] `TestQ4_AIProvenance_SHA256_Deterministic` GREEN
- [ ] `captureB3AIProvenance` deleted from the codebase (`Grep captureB3AIProvenance` returns no matches in production code)
- [ ] No `context.Background()` on a production code path inside `internal/services/datacleaner/adjustments/` (`Grep -n 'context\.Background\(\)'` over `*.go` minus `_test.go` returns no matches in the adjusters/ directory)
- [ ] `Raw()` carries `// TODO(phase-5): delete...` comment
- [ ] Phase 3 spec §5.2 amended
- [ ] Docs sweep complete (CLAUDE.md, AGENTS.md, THESIS, Phase 3 spec changelog, Phase 3 closeout changelog, this spec, this plan)
- [ ] Full `go test ./...` exit 0
- [ ] Replay on `artifacts/tier2-baseline/` if available: zero numeric drift in `17-response.json`, zero structural drift in `13-cleaner-audit.json`

---

## 11. Open questions

None blocking. Two non-blocking items for HUMAN awareness:

1. **Future LOW-2 promotion to `sync.Once`.** If a parallel-read consumer ever lands (e.g., a batch-valuation endpoint), the godoc warning becomes insufficient and the accessors must be hardened. Out of scope for this followup.
2. **Future MEDIUM-2 promotion to Option (b).** If at some point a strict "rendered-prompt-as-sent" audit becomes a regulatory requirement (financial-services AI traceability rules are evolving), Option (b) — exposing the rendered prompt from the AI client — may become mandatory. Tracked as a watch item; not in scope today.

---

## 12. Change log

| Date | Change |
|---|---|
| 2026-05-25 | Initial spec authored by Phase 3 followup ARCH. Covers the 9-finding fix slate from gpt-5.5 cross-model review: HIGH-1 (Restated double-count, Option A pre-clean snapshot), HIGH-2 (B3 single AI call), HIGH-3 (B3 amount-path ctx), MEDIUM-1 (B1 lease PV ctx), MEDIUM-2 (PromptHash spec amend, Option (a)), LOW-1 through LOW-4. Phase 3 NON-goals continue to hold. Implementation plan filed alongside at `dc1-phase-3-followup-implementation-plan.md`. |
