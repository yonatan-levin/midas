# TDB-3 — Replace contingent-liability probability heuristic with AI footnote analysis

**Status:** IMPLEMENTED 2026-06-08 (branch `worktree-tdb-3-contingent-ai`) — both AI-failed arms route to the industry heuristic (was flat 0.40); full validation cycle green (VERIFIER VERIFIED · REVIEWER APPROVE_WITH_NITS · QA PASS); `go test ./... -count=1` = 48/48 ok; shadow byte-identical; DDM bit-for-bit green; **inert under default config** (AI disabled). Single production file (`liabilities.go`, 3 edits) + a 4-path test + reconciled b3/integration AI tests.
**Priority:** P1 — Tier 1 (precision uplift; the AI infrastructure already exists).
**Type:** Enhancement.
**Mirrored as GitHub issue:** `[TDB-3]` (yonatan-levin/midas).
**Origin:** 2026-06-06 burn-down — catalog "AI Integration Structure" was PARTIAL: the B3 contingent-liability AI path shipped in DC-1 Phase 3, but one residual heuristic helper remains.
**Related:** DC-1 Phase 3 B3 (`analyzeContingentLiabilityWithAI`), `adjustments/hash.go` (SHA-256 provenance).

**Design spec (ARCH, 2026-06-08):** `docs/refactoring/spec/tdb-3-contingent-liability-ai-fallback-spec.md`
**Implementer plan (ARCH, 2026-06-08):** `docs/refactoring/implementations/tdb-3-contingent-liability-ai-fallback-implementation-plan.md`
**ARCH note:** Verified the precise gap — the two flat-`0.40` AI-FAILED fallbacks at `adjustments/liabilities.go:1122` (pre-computed arm) and `:1142` (legacy direct-call arm). The AI-disabled `default` arm (`:1155`) already uses the heuristic, so this closes the asymmetry. Change is **inert under default config** (`enable_ai_integration=false`): DDM bit-for-bit, recompute-shadow, and ledger-basket suites are AI-disabled and untouched. Open question (AI-failed → heuristic vs flat-40%) recommended **heuristic** (matches this tracker's TDB-3.2; sector-calibrated; single fallback policy). Status stays **OPEN** — implementation is a separate `/execute` pass.

---

## Context

`adjustments/liabilities.go:1290 getContingentLiabilityProbability(industryCode, amount)` still returns an **industry-classifier heuristic rate** with a standing `// TODO: Replace with AI-powered footnote analysis` comment. The heavier B3 path already calls a real AI footnote analyzer with deterministic SHA-256 prompt/source provenance — so this is a matter of routing this helper through that existing infrastructure, not building AI integration from scratch.

## Why it matters

Contingent-liability probability is a direct multiplier on the recognized liability. A flat industry rate is coarse; the actual probability is described in the 10-K footnotes the B3 path already reads. This is a precision improvement on an already-working flow, hence P1-but-not-blocking.

## Scope / Tasks

| ID | Task | File | Effort |
|---|---|---|---|
| TDB-3.1 | Route `getContingentLiabilityProbability` through the B3 AI analyzer (reuse, don't duplicate) | `adjustments/liabilities.go` | M |
| TDB-3.2 | Preserve heuristic as the AI-disabled / AI-failed fallback | same | S |
| TDB-3.3 | Tests: AI-success, AI-disabled, AI-failure fallback paths | `liabilities` tests | S |

## Acceptance
- [x] Helper consults the AI footnote path with heuristic fallback (AI preferred when enabled; heuristic on AI-disabled AND AI-failed)
- [x] Deterministic SHA-256 prompt/source provenance preserved (on AI success; nil on failure — single-AI-call invariant intact)
- [x] Three-path test coverage (`b3_ai_fallback_test.go::TestB3ContingentLiability_FallbackPolicy`, 4 paths incl. a non-Tech AI-failed case proving the heuristic; reconciled b3 + integration AI tests)
