# TDB-3 — Implementer Plan: Contingent-Liability AI Probability with Industry-Heuristic Fallback

**Role:** ARCH → handoff to BACKEND (`/execute`)
**Spec:** `docs/refactoring/spec/tdb-3-contingent-liability-ai-fallback-spec.md`
**Tracker:** `docs/reviewer/archive/TDB-3-contingent-liability-ai-footnote-analysis.md`
**GitHub issue:** `#3`
**Worktree:** `.claude/worktrees/tdb-3-contingent-ai` — **all `go` commands MUST set `GOWORK=off`**.
**Branch:** `worktree-tdb-3-contingent-ai`
**Single production file touched:** `internal/services/datacleaner/adjustments/liabilities.go`

> TDD: RED before GREEN. Write/extend the failing tests in Task 1, watch them fail with the
> *current* flat-`0.40` code, then make the production edits in Task 2, then reconcile the
> existing tests in Task 3.

---

## Pre-flight (read-only, ~2 min)

```bash
GOWORK=off go build ./internal/services/datacleaner/...        # expect exit 0
GOWORK=off go test ./internal/services/datacleaner/adjustments/... -run B3 -count=1   # baseline GREEN
git diff --quiet internal/integration/testdata/recompute-shadow/ && echo SHADOW_CLEAN
```

---

## Task 1 — RED: add the 3-path probability tests

Add to `internal/services/datacleaner/adjustments/b3_contingent_liabilities_adjuster_test.go`
(or a new sibling `b3_ai_fallback_test.go`) a focused table/subtest set proving the **new**
contract. The decisive new assertion is the **non-Tech AI-failure** case, which fails under the
current flat-`0.40` code.

1. **AI-success → AI probability + provenance** (regression guard, should already pass):
   - `la := NewLiabilityAdjuster(&mockAIService{}, nil).WithAI(true)`; `IndustryCode:"45"`;
     total contingent 180k → expect `180k * 0.30 = 54k` (`mockAIService` returns 30%);
     `out.Overlays[0].AIProvenance != nil`; provenance `PromptHash`/`SourceDocHash` are 64-char hex.

2. **AI-disabled → heuristic** (regression guard, should already pass):
   - `NewLiabilityAdjuster(&mockAIService{}, nil)` (no `.WithAI`); `IndustryCode:"20"` (Industrials);
     total 180k → expect `180k * 0.70 = 126k`; `AIProvenance == nil`; reasoning prefix `Conservative`
     (arm 4 unchanged) and carries `contingent_liabilities:` prefix.

3. **AI-failed → heuristic fallback, NOT 0.40, provenance nil** (NEW — fails RED):
   - `NewLiabilityAdjuster(&failingAIService{}, nil).WithAI(true)`; **`IndustryCode:"20"`** (Industrials);
     total 180k.
   - Expect `out.Overlays[0].Amount == 180k * 0.70 = 126_000` (NOT `72_000` = 180k×0.40).
   - `out.Overlays[0].AIProvenance == nil`.
   - The fired-path LedgerEntry / overlay reasoning contains `"AI analysis failed"` **and**
     `"industry heuristic fallback"`, and still contains `"contingent_liabilities"`.
   - **RED check:** under current code this asserts `126_000` but gets `72_000` → fails. Good.

4. **AI-failed via legacy direct-call (arm 3) → heuristic fallback** (NEW — fails RED):
   - `la := NewLiabilityAdjuster(&failingAIService{}, nil).WithAI(true)`; `IndustryCode:"21"` (Energy);
     call `la.ProcessContingentLiabilityAdjustment(ctx, data, rule, cleaningCtx)` (the `aiResult=nil`
     entry point) with `FootnoteText` set so `aiGateOpen` is true.
   - Expect weighted amount `total * 0.60` (Energy), reasoning contains `"AI analysis failed"` +
     `"industry heuristic fallback"`.
   - **RED check:** current arm 3 returns `total * 0.40` → fails.

Run RED:
```bash
GOWORK=off go test ./internal/services/datacleaner/adjustments/... -run 'B3|Contingent' -count=1
# EXPECT: the two NEW non-Tech failure subtests FAIL (got 0.40-derived amounts).
```

## Task 2 — GREEN: production edits in `liabilities.go`

Exactly three edits. Keep the `contingent_liabilities:` prefix assembly at line 1175 untouched.

### Edit 2.1 — arm 1 (AI-failed, pre-computed), lines 1119–1123
Replace:
```go
case aiResult != nil && aiResult.err != nil:
    // Caller already attempted the AI call; record the failure mode
    // in Reasoning and use the conservative 40% fallback.
    probabilityWeight = 0.40
    reasoningPrefix = fmt.Sprintf("AI analysis failed (%v), using conservative", aiResult.err)
```
with:
```go
case aiResult != nil && aiResult.err != nil:
    // Caller already attempted the AI call and it failed. Fall back to the
    // deterministic industry heuristic (TDB-3) — the SAME fallback the
    // AI-disabled `default` arm uses — so the two fallback modes are
    // consistent and sector-calibrated rather than a flat 40%. The
    // heuristic is network-free, so the single-AI-call invariant holds and
    // AIProvenance stays nil (no AI input produced this amount).
    probabilityWeight = la.getContingentLiabilityProbability(cleaningCtx.IndustryCode, totalContingentLiability)
    reasoningPrefix = fmt.Sprintf("AI analysis failed (%v), using industry heuristic fallback", aiResult.err)
```

### Edit 2.2 — arm 3 (AI-failed, legacy direct-call), lines 1141–1143
Replace:
```go
if err != nil {
    probabilityWeight = 0.40
    reasoningPrefix = fmt.Sprintf("AI analysis failed (%v), using conservative", err)
} else {
```
with:
```go
if err != nil {
    // Legacy direct-call AI failure → same deterministic industry-heuristic
    // fallback as arm 1 and the AI-disabled default arm (TDB-3).
    probabilityWeight = la.getContingentLiabilityProbability(cleaningCtx.IndustryCode, totalContingentLiability)
    reasoningPrefix = fmt.Sprintf("AI analysis failed (%v), using industry heuristic fallback", err)
} else {
```

### Edit 2.3 — resolve stale TODO, line 1290
In `getContingentLiabilityProbability` replace:
```go
    // Use industry-specific probability from classifier if available
    // TODO: Replace with AI-powered footnote analysis for more precise estimates
```
with:
```go
    // Deterministic industry-heuristic probability. This is the documented
    // FALLBACK used when the AI footnote analyzer is disabled OR enabled-but-
    // failed (TDB-3); the primary estimator is analyzeContingentLiabilityWithAI
    // when AI is enabled and succeeds. Sourced from the industry classifier's
    // per-sector ContingentLiabilityRate when available, else the GICS switch.
```

Run GREEN:
```bash
GOWORK=off go build ./internal/services/datacleaner/...
GOWORK=off go test ./internal/services/datacleaner/adjustments/... -run 'B3|Contingent' -count=1
```

## Task 3 — reconcile existing tests (the §7.3 list)

> Order matters: do this AFTER Task 2 so failures point only at stale expectations.

### 3.1 `b3_contingent_liabilities_adjuster_test.go`
- `failingAIService` godoc (lines ~38–41): reword "conservative fallback (40%)" → "industry heuristic fallback".
- Subtest "fired path with failing AI service produces nil AIProvenance" (lines 360–391):
  `IndustryCode:"45"` → Tech heuristic is `0.40`, so the `72_000.0` amount **stays correct**; update
  the comment (385–386) and message (388) from "40% conservative fallback" → "industry heuristic
  fallback (Tech 40%)". `AIProvenance` nil assertion stays.

### 3.2 `b3_single_call_test.go`
- `errorAIService` godoc (lines ~215–217) and `TestB3AIFailure_FallsBackToConservativeWithoutProvenance`
  (lines 237–276): `IndustryCode:"45"` → `total*0.40` **stays correct**. Update docstring + the
  message at 274–275 to "industry heuristic fallback". Optionally rename the test
  `→ TestB3AIFailure_FallsBackToHeuristicWithoutProvenance` (update the `func` name only; keep coverage).

### 3.3 `internal/integration/datacleaner_ai_test.go` — the two pipeline AI-failure cases
These run through `CleanFinancialData`, so `IndustryCode` is derived by `getIndustryCode(data)` and
is **not** guaranteed to be a known rate. Make them deterministic:

- **`AI_Fails_Fallback_To_Conservative` (lines 107–165):** Set the input so the classified industry
  code is deterministic and known (preferred: add the field that forces classification to GICS `45`
  — Tech — so the existing `10000.0` = 25k×0.40 expectation still holds; verify by logging
  `result.IndustryCode`). If the classifier cannot be pinned to `45`, instead read the heuristic
  rate for the classified code and compute the expected amount as `25000 * rate`. Replace the
  reasoning assertion: keep `assert.Contains(... "AI analysis failed")`, and change the
  `"conservative"` alternative to `"industry heuristic fallback"`. Make the firing non-vacuous:
  ensure `len(fallbackAdjustments) > 0` is actually true (require it, don't `if`-guard) so the
  assertion can't pass silently.
- **`TestDataCleaner_B3_AIFailureScenarios` table (lines ~280–370):** same treatment — pin the
  industry code (or compute `30000 * rate`), replace the `"conservative"` substring assertion
  (line 364) with `"industry heuristic fallback"`, keep the `expectedReason: "AI analysis failed"`
  checks (those still hold).

> Implementer judgment: the cleanest deterministic fix is to give these synthetic tickers an input
> shape that classifies to GICS `45` (rate 0.40) so the legacy numeric expectations survive and only
> the reasoning substring changes. If that proves brittle, switch to computing the expected amount
> from `getContingentLiabilityProbability` for the observed code. Either is acceptable; **do not**
> leave a `"conservative"` substring assertion, and **do not** leave the firing path vacuous.

## Task 4 — full validation (`GOWORK=off`)

```bash
# Package + integration
GOWORK=off go test ./internal/services/datacleaner/... -count=1
GOWORK=off go test ./internal/integration/... -count=1

# Named load-bearing invariants (must be GREEN)
GOWORK=off go test ./internal/services/valuation/models/... -run TestDDM_LegacyPath_BitForBit -count=1
GOWORK=off go test ./internal/services/datacleaner/... -run TestRecomputeUmbrellas_NoMutation -count=1
GOWORK=off go test ./internal/services/datacleaner/adjustments/... -run TestOrchestrator_LedgerOrdering -count=1
GOWORK=off go test ./internal/integration/... -run 'TestLedger_BasketSnapshot_ClusterPrediction|TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction' -count=1

# Shadow gate — MUST stay clean (AI-disabled path is inert)
git diff --quiet internal/integration/testdata/recompute-shadow/ && echo SHADOW_CLEAN || (echo SHADOW_DIRTY_FAIL && git --no-pager diff --stat internal/integration/testdata/recompute-shadow/)

# Whole tree
GOWORK=off go test ./... -count=1
```

**Pass bar:** all GREEN; `SHADOW_CLEAN` printed; no DDM golden regeneration; no `*.json` fixture diffs.

## Task 5 — observability lint guards (if service/gateway log lines were touched)

Not expected (no new log lines), but run for safety:
```bash
.\scripts\lint-logs.ps1
```

## Invariants to restate in the PR/commit (named)

- `TestDDM_LegacyPath_BitForBit` — GREEN (valuation path, AI never enabled).
- `TestRecomputeUmbrellas_NoMutation` — GREEN.
- `TestOrchestrator_LedgerOrdering` — GREEN.
- `TestLedger_BasketSnapshot_ClusterPrediction` / `..._T2BS3_RestatedReconstruction` — GREEN.
- recompute-shadow snapshots byte-identical (`git diff --quiet ...` exit 0).
- Single-AI-call invariant (HIGH-2/3) preserved; `AIProvenance == nil` on AI failure.

## Commit template (reference #3)

```text
fix(datacleaner): contingent-liability AI failure falls back to industry heuristic (#3)

B3's contingent-liability probability used the industry heuristic only when AI was
disabled; when AI was enabled-and-failed it used a flat 0.40. Route both AI-failed
arms (pre-computed + legacy direct-call) through getContingentLiabilityProbability so
the two fallback modes are consistent and sector-calibrated. Resolve the stale
"replace with AI" TODO — the heuristic is now the documented deterministic fallback.

Inert under default config (enable_ai_integration=false → arm 4 unchanged): DDM
bit-for-bit, recompute-shadow snapshots, and ledger-basket fixtures untouched.
Preserves the single-AI-call invariant, AIProvenance=nil on failure, the SHA-256
provenance on AI success, and the load-bearing `contingent_liabilities:` reasoning
prefix.

Closes #3.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
```

## Out of scope (do NOT do here)
- No `CalculationVersion` / `SchemaVersion` bump.
- No change to `analyzeContingentLiabilityWithAI`, `hash.go`, `preComputedAIResult`, or the AI-success arm.
- No heuristic rate-table value changes.
- No DDM golden regeneration; no shadow-snapshot regeneration.
