# TDB-3 — Replace contingent-liability probability heuristic with AI footnote analysis

**Status:** OPEN — filed 2026-06-06 (TODO-catalog burn-down pass).
**Priority:** P1 — Tier 1 (precision uplift; the AI infrastructure already exists).
**Type:** Enhancement.
**Mirrored as GitHub issue:** `[TDB-3]` (yonatan-levin/midas).
**Origin:** 2026-06-06 burn-down — catalog "AI Integration Structure" was PARTIAL: the B3 contingent-liability AI path shipped in DC-1 Phase 3, but one residual heuristic helper remains.
**Related:** DC-1 Phase 3 B3 (`analyzeContingentLiabilityWithAI`), `adjustments/hash.go` (SHA-256 provenance).

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
- [ ] Helper consults the AI footnote path with heuristic fallback
- [ ] Deterministic SHA-256 prompt/source provenance preserved
- [ ] Three-path test coverage
