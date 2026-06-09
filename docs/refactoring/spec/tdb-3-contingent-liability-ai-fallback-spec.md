# TDB-3 — Contingent-Liability AI Probability with Industry-Heuristic Fallback (Design Spec)

**Role:** ARCH
**Status:** SPEC — ready for `/execute`
**Tracker:** `docs/reviewer/archive/TDB-3-contingent-liability-ai-footnote-analysis.md`
**GitHub issue:** `#3` (`[TDB-3]`, OPEN)
**Worktree:** `.claude/worktrees/tdb-3-contingent-ai` (own `go.mod`; build/test with `GOWORK=off`)
**Branch:** `worktree-tdb-3-contingent-ai` (off `origin/master` `10b2378`)
**Target file:** `internal/services/datacleaner/adjustments/liabilities.go`
**CalcVersion / SchemaVersion impact:** NONE (no engine-output shape change; no schema field added).

---

## 1. Summary

The B3 contingent-liability path already routes through a real AI footnote analyzer
(`analyzeContingentLiabilityWithAI`) with deterministic SHA-256 prompt/source provenance.
What remains is an **asymmetry in the fallback**: when the AI is *disabled* the code uses
the industry heuristic (`getContingentLiabilityProbability`), but when the AI is *enabled
and fails* the code falls back to a hard-coded flat `0.40`. This spec closes that asymmetry
so that **both** AI-disabled and AI-failed paths use the same industry-calibrated heuristic,
and resolves the stale `// TODO: Replace with AI-powered footnote analysis` comment (the AI
integration exists; the heuristic is now the *documented deterministic fallback*, not a
placeholder).

This is **not** new AI integration. It reuses the existing B3 AI infra unchanged.

## 2. Goals

- AI-failed (enabled + error) → industry heuristic `getContingentLiabilityProbability(IndustryCode, totalContingent)`, not flat `0.40`.
- Make the two fallback modes (AI-disabled, AI-failed) **consistent** — both use the industry heuristic.
- Reasoning string for the AI-failed path names the failure and the fallback: `"AI analysis failed (<err>), using industry heuristic fallback"`.
- Resolve the stale TODO on `getContingentLiabilityProbability`; re-document it as the deterministic fallback used by AI-disabled **and** AI-failed paths.
- Preserve the AI-success behavior bit-for-bit (probability + SHA-256 provenance).

## 3. Non-goals

- No new AI client, no change to `analyzeContingentLiabilityWithAI`, `hash.go`, or `preComputedAIResult`.
- No change to the AI-success path (probability source, metadata copy, `AIProvenance` capture).
- No change to the heuristic rate table values (45→0.40, 20→0.70, 25→0.65, 21→0.60, 62→0.50, default→0.30) — only how/where the heuristic is *invoked as a fallback*.
- No flag-taxonomy, threshold, severity, or recommendation changes.
- No `CalculationVersion` bump, no `SchemaVersion` bump, no API-response shape change.
- No change to the load-bearing `contingent_liabilities:` reasoning *prefix* (only the mid-string fallback descriptor changes).
- No change to the DDM bit-for-bit path, recompute-shadow shim, or ledger-basket fixtures (see §6 — they are inert to this change).

## 4. Verified current state (line refs against the worktree)

All references in `internal/services/datacleaner/adjustments/liabilities.go`:

### 4.1 `getContingentLiabilityProbability(industryCode, amount)` — line 1288
Industry-classifier rate (`industryClassifier.GetSectorConfig(...).Thresholds.ContingentLiabilityRate`) when available, else a GICS-sector switch:

| GICS code | Rate | Sector |
|---|---|---|
| `45` | 0.40 | Information Technology |
| `20` | 0.70 | Industrials |
| `25` | 0.65 | Consumer Discretionary |
| `21` | 0.60 | Energy |
| `62` | 0.50 | Healthcare |
| default | 0.30 | — |

Carries a stale `// TODO: Replace with AI-powered footnote analysis for more precise estimates` at **line 1290**. Today it is called from exactly one site: the `default` branch at **line 1155**.

### 4.2 `processContingentLiabilityAdjustment(ctx, data, rule, cleaningCtx, aiResult)` — line 1092
Shared probability-weighting math. The probability `switch` (lines 1118–1157) has four arms:

| Arm | Condition | Today's probability | Reasoning prefix |
|---|---|---|---|
| 1 | `aiResult != nil && aiResult.err != nil` (AI **FAILED**, pre-computed) | **flat `0.40`** (line 1122) | `"AI analysis failed (%v), using conservative"` (1123) — **GAP** |
| 2 | `aiResult != nil` (AI success, pre-computed) | `aiResult.probability` | `"AI analysis of footnotes"` |
| 3 | `aiGateOpen` (legacy direct-call, AI enabled, no pre-compute) | AI call; on `err` → **flat `0.40`** (line 1142) | `"AI analysis failed (%v), using conservative"` (1143) — **GAP** |
| 4 | `default` (AI disabled / no service / no footnote+contingent) | `getContingentLiabilityProbability(...)` (line 1155) | `"Conservative"` (1156) — **already correct** |

`aiGateOpen` is computed at line 1116: `la.aiEnabled && la.aiService != nil && (cleaningCtx.FootnoteText != "" || totalContingentLiability > 0)`.

### 4.3 `ApplyB3Contingent(ctx, working, rule, cleaningCtx)` — line 870 (the LIVE Adjuster path)
Computes the AI result **once** when `la.aiEnabled && la.aiService != nil && (FootnoteText != "" || totalContingent > 0)` (line 888); records `AIProvenance` **only** on AI success (line 899–901 — provenance nil on failure); delegates to `processContingentLiabilityAdjustment` with the pre-computed `aiResult` (line 909). Because `ApplyB3Contingent` always pre-computes when the gate is open, **the live path reaches arm 1 on failure, not arm 3.** Arm 3 is reachable only via the legacy direct-call entry point `ProcessContingentLiabilityAdjustment` (line 1083, passes `aiResult = nil`).

### 4.4 Feature flag
`datacleaner.enable_ai_integration` (`internal/config/config.go:305`) defaults **FALSE** (`viper.SetDefault(..., false)` at `config.go:576`). `NewLiabilityAdjuster` constructs with `aiEnabled: false` (`liabilities.go:35`); AI is opted in only via `.WithAI(true)`.

## 5. The change

Two edits in the probability `switch`, plus one comment cleanup:

### 5.1 Arm 1 (line 1119–1123) — AI-FAILED, pre-computed
Replace the flat `0.40` with the industry heuristic:

```text
case aiResult != nil && aiResult.err != nil:
    probabilityWeight = la.getContingentLiabilityProbability(cleaningCtx.IndustryCode, totalContingentLiability)
    reasoningPrefix   = fmt.Sprintf("AI analysis failed (%v), using industry heuristic fallback", aiResult.err)
```

### 5.2 Arm 3 (line 1141–1143) — AI-FAILED, legacy direct-call
Replace the flat `0.40` with the same industry heuristic:

```text
if err != nil {
    probabilityWeight = la.getContingentLiabilityProbability(cleaningCtx.IndustryCode, totalContingentLiability)
    reasoningPrefix   = fmt.Sprintf("AI analysis failed (%v), using industry heuristic fallback", err)
}
```

### 5.3 Comment cleanup — `getContingentLiabilityProbability` (line 1290)
Replace the stale TODO with a doc line stating the helper is the **deterministic industry-heuristic
fallback** consulted by the AI-disabled path **and** the AI-failed path; the AI footnote analyzer
(`analyzeContingentLiabilityWithAI`) is the primary estimator when enabled and successful.

> The pseudocode above is illustrative of the contract; the implementer plan
> (`docs/refactoring/implementations/tdb-3-contingent-liability-ai-fallback-implementation-plan.md`)
> carries the exact RED→GREEN edit sequence.

## 6. Determinism & provenance preservation (explicit)

- **Single-AI-call invariant (HIGH-2/3) preserved.** No new AI call is added. `ApplyB3Contingent`
  still invokes `analyzeContingentLiabilityWithAI` exactly once; arm 1 consumes the pre-computed
  failure. The heuristic is a pure, deterministic, network-free function — it cannot add a call.
- **`AIProvenance = nil` on failure preserved.** The heuristic-derived amount has no AI provenance
  (it is deterministic, not AI-derived). `ApplyB3Contingent` already gates provenance capture on
  `aiErr == nil` (line 899), so leaving the failure-amount as a heuristic value keeps provenance nil
  — exactly the existing contract.
- **SHA-256 prompt/source provenance on AI success preserved.** Untouched — only the failure arms change.
- **Reasoning prefix preserved.** The load-bearing `contingent_liabilities:` prefix is assembled at
  line 1175 (`"contingent_liabilities: %s applied ..."`) and is unchanged. Only the mid-string
  `reasoningPrefix` descriptor moves from `"...using conservative"` to `"...using industry heuristic fallback"`.

## 7. Invariant & risk analysis

### 7.1 Default-config INERT — verdict and evidence
**Verdict: under default config this change is inert; the AI-failed arms are unreachable.**

- `enable_ai_integration` defaults FALSE (`config.go:576`); `NewLiabilityAdjuster` → `aiEnabled:false`
  (`liabilities.go:35`). With AI disabled, `aiResult` is always nil (the `ApplyB3Contingent` gate at
  line 888 is false) and `aiGateOpen` is false → the `switch` always lands in arm 4 (`default`), which
  this change does **not** modify.
- The **only** tests that enable AI are in `internal/integration/datacleaner_ai_test.go`
  (`EnableAIIntegration = true` at lines 28 and 276) plus unit tests that call `.WithAI(true)`
  explicitly. Verified: no `WithAI(true)` / `EnableAIIntegration = true` anywhere under
  `internal/services/valuation/**` or in the shadow/basket integration harnesses.
- Therefore the AI-disabled invariant suites are **inert**:
  - **DDM bit-for-bit** (`TestDDM_LegacyPath_BitForBit`) — valuation path, AI never enabled.
  - **recompute-shadow snapshots** — `git diff --quiet internal/integration/testdata/recompute-shadow/` stays **exit 0** (the cleaner runs AI-disabled; the heuristic `default` arm is unchanged).
  - **ledger-basket** (`TestLedger_BasketSnapshot_ClusterPrediction`, `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction`) — AI-disabled.

### 7.2 Behavioral delta (only when AI is enabled AND failing)
When AI is enabled and the call errors, the probability multiplier on recognized contingent
liabilities changes from a flat `0.40` to the industry rate:

| Classified GICS code | Old (flat) | New (heuristic) | Direction |
|---|---|---|---|
| `45` Tech | 0.40 | 0.40 | unchanged |
| `20` Industrials | 0.40 | 0.70 | ↑ more conservative |
| `25` Consumer Disc. | 0.40 | 0.65 | ↑ |
| `21` Energy | 0.40 | 0.60 | ↑ |
| `62` Healthcare | 0.40 | 0.50 | ↑ |
| unmapped / default | 0.40 | 0.30 | ↓ less conservative |

This is a **real change to a probability multiplier on recognized liabilities** for any
AI-enabled deployment that hits an AI outage on a non-Tech ticker. It is the intended TDB-3 outcome.

### 7.3 Tests that assert the flat-`0.40` AI-failed behavior — MUST be updated
Grepped `0.40` / `0.4` / `conservative` / `AI analysis failed` across the named test files. The
following assert the **AI-failed** behavior and must change (numbers, comments, and/or reasoning
substrings):

| File | Anchor | What asserts flat-0.40 / "conservative" | Required update |
|---|---|---|---|
| `adjustments/b3_contingent_liabilities_adjuster_test.go` | `failingAIService` godoc, line 38–41 | comment "conservative fallback (40%)" | reword to "industry heuristic fallback" |
| same | subtest "fired path with failing AI service produces nil AIProvenance", lines 360–391 | `IndustryCode:"45"`; expects `72_000.0` (180k×0.40); message says "40%% conservative fallback" | **amount stays 72k** (code 45 heuristic == 0.40) but update comment + message to "industry heuristic fallback (Tech 40%)" |
| `adjustments/b3_single_call_test.go` | `errorAIService` godoc, lines 215–217 | "conservative-40% reasoning branch" | reword to "industry heuristic fallback" |
| same | `TestB3AIFailure_FallsBackToConservativeWithoutProvenance`, lines 237–276 | `IndustryCode:"45"`; `total*0.40`; message "conservative 40% fallback" | **amount stays** (`total*0.40` == Tech heuristic); update docstring + message; consider renaming → `..._FallsBackToHeuristicWithoutProvenance` |
| `internal/integration/datacleaner_ai_test.go` | `AI_Fails_Fallback_To_Conservative`, lines 107–165 | expects `10000.0` (25k×0.40); asserts reasoning contains `"AI analysis failed"` **or** `"conservative"` | **set an explicit deterministic industry code** so the heuristic yields a known rate, OR adjust the expected amount to the classified code's heuristic rate; replace the `"conservative"` substring assertion with `"industry heuristic fallback"` (keep the `"AI analysis failed"` check) |
| same | `TestDataCleaner_B3_AIFailureScenarios` table, lines ~280–370 | `30000 * 0.4 = 12,000`; asserts reasoning contains `"conservative"` (line 364) | same treatment: deterministic code + reasoning substring → `"industry heuristic fallback"` |

Notes:
- The two **unit** AI-failure tests use `IndustryCode:"45"` whose heuristic rate is coincidentally
  `0.40`, so their **numeric** assertions stay valid; only comments/messages (and optionally a
  rename) change. This is the safest possible numeric surface.
- The two **integration** AI-failure cases run through the full `CleanFinancialData` pipeline where
  `IndustryCode` is derived by `getIndustryCode(data)` from the synthetic ticker. Their `0.40`
  expectation is **not** guaranteed under the heuristic and both assert the `"conservative"`
  reasoning substring (which is being removed). They MUST be made deterministic — the implementer
  plan prescribes pinning the industry code so the expected rate is known. (Both assertion blocks
  are guarded by `if len(...Adjustments) > 0`, so a vacuous pass is possible today; the plan makes
  the firing path explicit so the assertion is non-vacuous post-change.)

### 7.4 Tests that are INERT (must NOT change) — sanity list
- `b3_contingent_liabilities_adjuster_test.go` "fired path emits OverlaySpec with Field:DebtLikeClaims"
  (line 174) — AI **disabled**, code `45`, heuristic already returns `0.40` → `72_000.0` unchanged (arm 4).
- `b3_contingent_liabilities_adjuster_test.go` "default 30%" subtest (line 322, code `99`) — AI disabled, arm 4, unchanged.
- All `q4_b3_aiprovenance_test.go` assertions — AI **success** path, untouched.
- DDM goldens, recompute-shadow snapshots, ledger-basket fixtures — AI disabled (§7.1).

## 8. Open question (with recommendation)

**Q: AI-failed → industry heuristic (the tracker's ask) vs. keep the flat `0.40` conservative?**

**Recommendation: industry heuristic.** Rationale:
1. **Matches the tracker** (TDB-3.2: "Preserve heuristic as the AI-disabled / AI-failed fallback").
2. **Industry-calibrated > arbitrary.** A flat 40% is a magic number; the heuristic encodes
   sector-specific contingent-realization rates (Industrials 70%, Energy 60%, …) and can be tuned
   per-sector via `industryClassifier` config without code change.
3. **Consistency.** The heuristic is *already* the AI-disabled fallback (arm 4). Making AI-failed use
   the same function means there is exactly **one** deterministic fallback policy, not two.

**Counter-argument (and why it loses):** a fixed conservative 40% is *predictable* and never
under-states relative to a low-rate sector default (0.30). But the default-30% case is precisely the
"unknown sector" case where a calibrated, classifier-aware estimate is preferable to an arbitrary
inflation to 40%; and for the mapped sectors the heuristic is *more* conservative than 40%. The
predictability argument does not outweigh calibration + single-policy consistency.

**Decision needed from REVIEWER/HUMAN before merge:** confirm the heuristic direction (this spec
assumes YES). If HUMAN prefers the flat-40% safety floor, the alternative is a `max(0.40, heuristic)`
clamp — noted but **not** recommended (it re-introduces the magic number and breaks the
single-policy goal).

## 9. Acceptance criteria

- [ ] AI-failed (both pre-computed arm 1 and legacy arm 3) uses `getContingentLiabilityProbability(IndustryCode, totalContingent)` — verified by a unit test on a non-Tech code (e.g. `20`) asserting the rate is the heuristic value, **not** `0.40`.
- [ ] AI-failed reasoning contains `"AI analysis failed"` and `"industry heuristic fallback"`; still contains the `contingent_liabilities:` prefix.
- [ ] AI-success unchanged: probability == AI probability; `AIProvenance` non-nil with SHA-256 prompt/source hashes.
- [ ] AI-disabled unchanged: arm 4 heuristic; `AIProvenance` nil.
- [ ] `AIProvenance == nil` on AI failure (heuristic fallback carries no provenance).
- [ ] Stale TODO at `getContingentLiabilityProbability` resolved.
- [ ] `GOWORK=off go test ./...` green in the worktree.
- [ ] `git diff --quiet internal/integration/testdata/recompute-shadow/` exits 0 (shadow inert).
- [ ] `TestDDM_LegacyPath_BitForBit`, `TestRecomputeUmbrellas_NoMutation`, `TestOrchestrator_LedgerOrdering`, `TestLedger_BasketSnapshot_ClusterPrediction`, `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` all GREEN.

## 10. References

- Tracker: `docs/reviewer/archive/TDB-3-contingent-liability-ai-footnote-analysis.md`
- Implementer plan: `docs/refactoring/implementations/tdb-3-contingent-liability-ai-fallback-implementation-plan.md`
- B3 AI infra & provenance: DC-1 Phase 3 spec `docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-3-spec.md` §5.2; `internal/services/datacleaner/adjustments/hash.go`
