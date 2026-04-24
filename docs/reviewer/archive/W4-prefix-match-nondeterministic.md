# W-4: Prefix Match in getMultiple() Non-Deterministic Over Map Iteration

| Field | Value |
|-------|-------|
| **ID** | W-4 |
| **Severity** | LOW |
| **Status** | Resolved (2026-04-19) |
| **Found In** | Phase 3 Code Review |
| **File** | `internal/services/valuation/models/revenue_multiple.go:152` |

## Description

The prefix match loop iterates over `m.multiples` (a `map[string]float64`). Go map iteration order is randomized. If a company has industry code `"HEALTH_BIO"` and the map contains both `"HEALTH"` (2.0x) and `"HEALTH_BIOTECH"` (4.0x), the exact match won't hit either, and the prefix match could return either depending on iteration order.

The exact match on line 147 correctly handles cases like `"TECH_SAAS"`, but partial prefixes where neither key is an exact match are non-deterministic.

## Impact

- Could produce different valuation results on different runs for the same ticker
- Unlikely to trigger in practice (current sub-industry codes are well-formed), but a latent bug

## Recommended Fix

Sort map keys by length (longest first) before prefix matching, so the most specific match wins:

```go
keys := make([]string, 0, len(m.multiples))
for k := range m.multiples { keys = append(keys, k) }
sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
for _, k := range keys {
    if strings.HasPrefix(industry, k) {
        return m.multiples[k]
    }
}
```

## Acceptance Criteria

- [x] Longest prefix match wins deterministically
- [x] Test with overlapping prefixes returns the most specific one

## Resolution (verified 2026-04-23)

- **Classification:** RESOLVED
- **Commits:** `4d46142` "Resolve reviewer Tier 1+2 follow-ups: W-1, W-2, W-3, W-4, S-5" (and earlier `ed9cf63` which first introduced longest-prefix logic).
- **Evidence:**
  - `internal/services/valuation/models/revenue_multiple.go:142-169` (`getMultiple`) — iterates all map keys, keeps only the longest one whose `strings.HasPrefix(upper, code)` is true via `len(code) > len(bestKey)`. Map-iteration order no longer leaks into results because every key is inspected and the longest wins.
  - `internal/services/valuation/crosscheck.go:128-152` (`LookupMultiple`) — same longest-prefix-match algorithm, shared by sanity-check and exit-multiple lookups.
  - Test: `internal/services/valuation/models/revenue_multiple_test.go:238-260` (`TestRevenueMultipleModel_GetMultiple_LongestPrefixWinsDeterministic`) runs the lookup 100 times with `HEALTH` + `HEALTH_BIOTECH` in the map to prove determinism against Go's randomized map iteration.
  - Test: `internal/services/valuation/crosscheck_test.go:230+` (`TestLookupMultiple`) covers the shared LookupMultiple.
- **Verification:** Read both `getMultiple` and `LookupMultiple` implementations, confirmed identical longest-prefix pattern, and inspected the 100-iteration determinism test.
