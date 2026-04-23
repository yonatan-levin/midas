# S-5: Financial Company With Zero DPS Fails Entirely (No Fallback)

| Field | Value |
|-------|-------|
| **ID** | S-5 |
| **Severity** | MEDIUM |
| **Status** | Resolved (2026-04-19) |
| **Found In** | Phase 3 Code Review |
| **Files** | `router.go:89-95`, `service.go:400-406` |

## Description

When a company is classified as `FIN` (financial), the ModelRouter always selects DDM. If the company doesn't pay dividends (DPS = 0), DDM's `Calculate()` returns an error ("does not pay dividends"). This error propagates through `performAlternativeValuation` as `ErrModelNotApplicable`, and the API returns HTTP 422.

This means **a non-dividend-paying financial company** (e.g., a growth-stage payment processor, a fintech like SQ or AFRM, or a bank that temporarily suspended dividends) gets no valuation at all — not DDM, not DCF, not revenue multiples. It just errors out.

## Real-World Impact

| Company | Industry | DPS | What Happens | What Should Happen |
|---------|----------|-----|--------------|-------------------|
| JPM | FIN (bank) | $4.60 | DDM works correctly | Correct |
| SQ (Block) | FIN (fintech) | $0 | DDM fails, error 422 | Should fall back to DCF or revenue multiple |
| GS during 2020 | FIN (bank) | $0 (suspended) | DDM fails, error 422 | Should fall back to DCF |
| AFRM (Affirm) | FIN (fintech) | $0, negative OI | DDM fails, then error | Should use revenue multiple |

## Root Cause

The routing in `SelectModel()` is one-shot: it picks the "best" model and commits to it. There's no fallback chain. The service code at `service.go:400-406` wraps the alternative model's error as `ErrModelNotApplicable` and returns immediately.

## Recommended Fix: Two-Phase Routing

Implement a fallback chain in `performAlternativeValuation`:

```go
// Try the selected model first
result, err := selectedModel.Calculate(ctx, modelInput)
if err != nil {
    // Selected model failed — try fallback models in priority order
    fallbacks := s.modelRouter.GetFallbacks(industryCode, latestFinancialData)
    for _, fallbackModel := range fallbacks {
        result, err = fallbackModel.Calculate(ctx, modelInput)
        if err == nil {
            result.Warnings = append(result.Warnings,
                fmt.Sprintf("Primary model (%s) failed, used %s as fallback", 
                    selectedModel.ModelType(), fallbackModel.ModelType()))
            break
        }
    }
    if err != nil {
        return nil, fmt.Errorf("%w: all models failed for %s", ErrModelNotApplicable, ticker)
    }
}
```

### Fallback Chain Logic

```
FIN company:
  1. Try DDM (dividend-based)
  2. If DPS=0 and OI>0: fall back to multi_stage_dcf
  3. If DPS=0 and OI<=0: fall back to revenue_multiple

REIT company:
  1. Try FFO
  2. If missing data: fall back to multi_stage_dcf (if OI>0)
  3. Last resort: revenue_multiple

Default:
  1. Try multi_stage_dcf
  2. If OI<=0: fall back to revenue_multiple
```

### Implementation Phases

This fix naturally fits into the existing upgrade spec phases:

**Phase 3 follow-up (immediate):**
- Add `GetFallbacks(industry, financials) []ValuationModel` to `ModelRouter`
- Implement the retry loop in `performAlternativeValuation`
- Test with SQ (FIN, zero DPS, positive OI) → should get DCF
- Test with AFRM (FIN, zero DPS, negative OI) → should get revenue_multiple

**Phase 4 connection (country risk + cross-checks):**
- When Phase 4 adds the multiples sanity cross-check, it can compare the fallback model's result against sector medians
- The fallback warning in the API response tells the user "this used a secondary model" — the cross-check can validate whether that secondary result is reasonable
- Country risk premium (Phase 4.1) would also apply to the fallback model's WACC, ensuring international financials like Banco Santander get correct cost of equity even through a fallback path

**Longer-term (post Phase 4):**
- The `SupportsIndustry()` interface method (W-1) becomes useful here — models can declare which industries they support as fallbacks
- This enables a fully declarative routing table where models register themselves and their fallback priorities
- Eventually: `industry_routing.json` config file defining primary + fallback chains per industry

## Why This Matters Financially

The financial sector is diverse. "FIN" includes:
- **Traditional banks** (JPM, BAC) — dividend-heavy, DDM is perfect
- **Investment banks** (GS, MS) — variable dividends, DDM works but needs care
- **Fintechs** (SQ, PYPL, AFRM) — zero dividends, growth-stage, DCF or revenue multiples needed
- **Insurance** (BRK, MET) — some pay dividends, some don't
- **Crypto/DeFi** (COIN) — no dividends, volatile revenue

A single-model-per-industry approach misses this diversity. The fallback chain handles it gracefully: try the industry-specific model, but don't give up if it can't produce a result.

## Acceptance Criteria

- [x] FIN company with zero DPS gets a valuation (not an error)
- [x] Fallback model is logged as a warning in the API response
- [x] `calculation_method` reflects the actual model used (not the one that failed)
- [x] Test: SQ (FIN, zero DPS, positive OI) → multi_stage_dcf fallback
- [x] Test: AFRM (FIN, zero DPS, negative OI) → revenue_multiple fallback
- [x] Test: JPM (FIN, positive DPS) → DDM (no fallback needed)
- [x] Test: standard company → DCF directly (no routing change)

## Resolution (verified 2026-04-23)

- **Classification:** RESOLVED
- **Commits:** `01f4db0` "Fix Phase 3 follow-ups: repo persistence, model fallback chain, reviewer nits" (commit message: "S-5: Model fallback chain when primary model fails: DDM fails + positive OI → falls back to standard DCF path; DDM fails + negative OI → falls back to revenue_multiple; errFallbackToDCF sentinel replaces (nil, nil) convention"). Status header in the doc itself was already updated to "Resolved (2026-04-19)".
- **Evidence:**
  - `internal/services/valuation/errors.go:25-28` — new unexported sentinel `errFallbackToDCF` wraps the fallback signal.
  - `internal/services/valuation/service.go:467-489` — `performAlternativeValuation` is invoked for non-DCF primary models; if it returns `errFallbackToDCF`, the code continues into the standard DCF path and attaches a warning via `dcfFallbackWarning`.
  - `service.go:687-708` (`performAlternativeValuation`) — on primary-model error, branches on `effectiveOI(latestFinancialData)`: positive OI returns `errFallbackToDCF` (→ DCF fallback); non-positive OI constructs a `RevenueMultipleModelWithMultiples(nil, ...)` as a last-resort fallback and appends a warning identifying which primary failed.
  - `service.go:605-607` — fallback warning propagated as `result.Warnings` in the API response so clients see which model was used.
- **Verification:** Read the fallback chain end-to-end in `service.go` and confirmed the sentinel plumbing in `errors.go`. The three fallback paths (DDM→DCF, DDM→RevenueMultiple, FFO→any) all route through the same mechanism.
