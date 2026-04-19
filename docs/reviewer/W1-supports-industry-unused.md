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

- [ ] Either remove `SupportsIndustry` from interface, or wire it into routing logic
- [ ] All existing routing tests still pass
