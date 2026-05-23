# T2-P0b-1 — Replay walker `compareFairValueResponses` extension required before P2 populates `DCFPerYearPV`

**Status:** RESOLVED 2026-05-21 — walker extension landed in P2 (`a19506d`, merged via `877fa76`). `compareFairValueResponses` now handles `DCFPerYearPV` (length + per-element float-tolerance) alongside the simple scalar fields P0b already covered. `ResolutionTrace` walker coverage remains a residual gap (would only surface via test-only `cmp.Diff` paths); tracker closes because the P2 acceptance criterion (drift in `DCFPerYearPV` surfaces in production `Replay()` diff output) is met.
**Severity:** Medium (latent — fields not yet populated; becomes real when P2 ships)
**Filed:** 2026-05-16 by P0b REVIEWER + QA gates
**Phase context:** Tier 2 — opened during P0b code review (commit `2e48fde`), confirmed by P0b QA. Resolved during Tier 2 P2 merge (`877fa76`).
**Owner:** P2 implementer (VAL-1 DCF archetype-aware horizon + diagnostics) — landed

---

## Context

P0b added 7 new Tier-2 fields to `FairValueResponse` (and the matching `ValuationResult`):

1. `AssumptionProfile` (string)
2. `ResolutionTrace` (`*profile.ResolutionTrace` — pointer to struct)
3. `DCFHorizonYears` (int)
4. `DCFTerminalMethod` (string)
5. `DCFTerminalPctOfEV` (float64)
6. `DCFPerYearPV` (`[]float64` — slice)
7. `DCFTerminalGrowthUsed` (float64)

The production replay tool's hand-rolled walker, `compareFairValueResponses` in `internal/observability/replay/compare.go`, enumerates fields explicitly. BACKEND extended it for fields 1, 3, 4, 5, 7 (the simple scalar types). Fields 2 (`ResolutionTrace`) and 6 (`DCFPerYearPV`) were NOT extended because they require special handling:

- `ResolutionTrace` is a pointer to a nested struct — needs nil-check + recursive field walk
- `DCFPerYearPV` is a `[]float64` — needs nil-check + length comparison + per-element float-tolerance comparison

## Why this matters

P0b populates `ResolutionTrace` on every request, and `AssumptionProfile` (already in walker). Walker gap on `ResolutionTrace` means a drift in resolver behavior (e.g., a future commit accidentally changes `Source = SourceFallback` to `SourceExplicit` for the same Facts) would NOT surface in production `Replay()`. The drift would only surface in test-only `CompareResponse` calls that use `cmp.Diff` directly.

**P2 will populate `DCFPerYearPV`** as the per-year PV slice for chart-friendly visualization (per implementer plan §6.2). Once P2 ships, drift in the DCF per-year PV math would silently bypass production replay regression — exactly the kind of cross-phase regression that pinned bundles are supposed to catch.

## The fix

Extend `compareFairValueResponses` in `internal/observability/replay/compare.go` to handle both fields:

```go
// ResolutionTrace — pointer-to-struct, nil-aware
if got.ResolutionTrace == nil && want.ResolutionTrace == nil {
    // both nil; skip
} else if got.ResolutionTrace == nil || want.ResolutionTrace == nil {
    diffs = append(diffs, ...) // one-side-nil diff
} else {
    // walk struct fields: ProfileID, Source, MatchedRuleID, FallbackReason, etc.
    if got.ResolutionTrace.ProfileID != want.ResolutionTrace.ProfileID {
        diffs = append(diffs, ...)
    }
    // ... etc for each ResolutionTrace field
}

// DCFPerYearPV — slice, nil-aware + per-element float-tolerance
if len(got.DCFPerYearPV) != len(want.DCFPerYearPV) {
    diffs = append(diffs, ...) // length-mismatch diff
} else {
    for i := range got.DCFPerYearPV {
        if !floatEqualWithTolerance(got.DCFPerYearPV[i], want.DCFPerYearPV[i], relTol, absTol) {
            diffs = append(diffs, ...) // per-element drift
        }
    }
}
```

Also: bump `countFairValueFields` constant in `diff.go` to match the new walker coverage (the reflect-vs-constant guard at startup currently passes because the count constant was bumped to 43; that guard only ensures the constant matches `reflect.NumField`, NOT that every field is walked).

## Acceptance criteria

- [ ] `compareFairValueResponses` enumerates `ResolutionTrace` (recursive struct walk)
- [ ] `compareFairValueResponses` enumerates `DCFPerYearPV` (length + per-element float-tolerance comparison)
- [ ] Comment in `compare.go` updated to remove the deferred-to-CompareResponse note for these two fields
- [ ] New test cases in `compare_test.go` exercise: nil-vs-nil, nil-vs-non-nil, populated-vs-populated (ResolutionTrace); empty-slice, length-mismatch, value-drift (DCFPerYearPV)
- [ ] Production `Replay()` against a bundle with a drifted `ResolutionTrace.ProfileID` or `DCFPerYearPV[i]` correctly flags the drift in its diff output

## Out of scope

- Backfilling pre-Tier-2 bundles with `ResolutionTrace` / `DCFPerYearPV` values — they're additive fields, expected to be empty in older bundles
- `goFieldToJSON` map updates — both fields use standard `json:"..."` tags so no special mapping needed
- Tightening tolerance for `DCFPerYearPV` — use the same `--float-rel-tol` / `--float-abs-tol` flags as other float fields

## Closing this tracker

Move to `docs/reviewer/archive/` once:
- P2 ships AND production replay against a known-drifted DCFPerYearPV produces a non-empty diff in `Replay()` output (not just `CompareResponse`)
- A regression test in `compare_test.go` exercises the new walker branches

Alternatively, close at Tier 2 close if both fields remain unused after P2-P4 ship (extremely unlikely given P2 spec explicitly populates DCFPerYearPV).
