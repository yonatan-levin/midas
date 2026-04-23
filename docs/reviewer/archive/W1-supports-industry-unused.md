# W-1: SupportsIndustry() Declared But Never Called

| Field | Value |
|-------|-------|
| **ID** | W-1 |
| **Severity** | MEDIUM |
| **Status** | Resolved (2026-04-19) |
| **Found In** | Phase 3 Code Review |
| **File** | `internal/services/valuation/models/router.go:23` |

## Description

The `ValuationModel` interface declares `SupportsIndustry(industry string) bool`, and each model implements it, but `SelectModel()` never calls it. The routing is entirely hardcoded (FIN -> DDM, REIT -> FFO, negative OI -> Revenue Multiple, default -> DCF).

This means if you register a new model that claims `SupportsIndustry("ENERGY")`, the router will still ignore it.

## Impact

- Dead code in the interface — adds maintenance burden without providing value
- New models cannot self-register their supported industries
- Misleading interface contract (models implement a method that's never called)

## Recommended Fix

**Option A (simpler):** Remove `SupportsIndustry` from the interface until actually needed. Keep the routing logic explicit and hardcoded.

**Option B (extensible):** Use `SupportsIndustry` in `SelectModel` as the primary routing mechanism instead of hardcoded industry checks. This enables a registry pattern where models declare what they support.

Option A is recommended for now — the hardcoded routing is clear and maintainable with 4 models.

## Acceptance Criteria

- [x] Either remove `SupportsIndustry` from interface, or wire it into routing logic
- [x] All existing routing tests still pass

## Resolution (verified 2026-04-23)

- **Classification:** RESOLVED
- **Commits:** `4d46142` "Resolve reviewer Tier 1+2 follow-ups: W-1, W-2, W-3, W-4, S-5"
- **Evidence:** `internal/services/valuation/models/router.go:12-22` — the `ValuationModel` interface is now pared down to `Calculate()` and `ModelType()`; `SupportsIndustry()` has been removed. The routing comment explicitly states "Routing is performed by ModelRouter.SelectModel based on industry + financials, not by self-declaration from the models themselves." Grep confirms no call sites for `SupportsIndustry` remain in the codebase (only historical references in docs/spec).
- **Verification:** Read `router.go` at HEAD and grepped the repo for `SupportsIndustry` — confirmed Option A (remove from interface) was chosen.
