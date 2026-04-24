# B2: Prefix Match Should Require Underscore Boundary

**Status:** OPEN  
**Severity:** SUGGESTION  
**Found by:** Superpowers Code-Reviewer (2026-04-13)  
**Location:** `internal/services/valuation/crosscheck.go:129`, `internal/services/valuation/models/revenue_multiple.go:159`

## Description

The longest-prefix-match uses `strings.HasPrefix(upper, code)` without requiring the match to end at a delimiter boundary. This means:

- `"TECHNOLOGY"` would match key `"TECH"` — semantically incorrect
- `"FINESSE"` would match key `"FIN"` — semantically incorrect

Current industry codes use underscore-delimited hierarchies (`TECH_SAAS`, `FIN_BANKS`), so this is not currently triggered. But adding a code like `TECHNOLOGY` to the multiples config would silently match `TECH`.

## Recommended Fix

Require an underscore boundary for non-exact matches:

```go
if code != "default" && (upper == code || strings.HasPrefix(upper, code+"_")) && len(code) > len(bestKey) {
    bestKey = code
    bestVal = val
}
```

This preserves `TECH_SAAS_CLOUD` → `TECH_SAAS` but prevents `TECHNOLOGY` → `TECH`.

Apply to both `LookupMultiple` in `crosscheck.go` and `getMultiple` in `revenue_multiple.go`.

## Risk if Not Fixed

Low today. Becomes a latent bug if industry codes that share prefixes without underscores are added to `config/industry_multiples.json`.
