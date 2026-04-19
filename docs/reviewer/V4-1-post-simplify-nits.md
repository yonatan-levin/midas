# V4.1 Post-Simplify Nits

**Status:** OPEN  
**Found by:** REVIEWER agent (2026-04-19)  
**Context:** Review of v4.1 spec items (analyst caching, ImpliedPFCF, SIC extraction, REIT NAV, DDM P/BV) after the `/simplify` cleanup pass.  
**Reviewer verdict:** APPROVE_WITH_NITS (no blockers)

All 11 items below are non-blocking polish. Grouped by severity and effort.

---

## Yellow (maintenance concern) — 4 items

### V4.1-N1: Extract `thresholds` leaf package

**Location:** `internal/services/valuation/crosscheck.go`, `models/ffo.go`, `models/ddm.go`

Threshold constants are triple-declared across 3 files:
- `crosscheck.go`: `DeviationThresholdHigh = 2.0` / `DeviationThresholdLow = 0.5`
- `ffo.go`: `navDeviationThresholdHigh = 2.0` / `navDeviationThresholdLow = 0.5`
- `ddm.go`: `ddmPBVDeviationHigh = 2.0` / `ddmPBVDeviationLow = 0.5`

Reason: models package cannot import its parent valuation package (cyclic).

**Fix:** Create `internal/services/valuation/thresholds/thresholds.go` exporting `DeviationHigh = 2.0` and `DeviationLow = 0.5`. Import from both `valuation` and `valuation/models`.

**Effort:** ~15 min.

---

### V4.1-N2: Consolidate `loadPFFOMultiple`/`loadREITCapRate` into `loadFFOConfig`

**Location:** `internal/services/valuation/models/ffo.go:89-150`

Three functions now exist that each `os.ReadFile` + `json.Unmarshal` the same `industry_multiples.json`. The wrappers (`loadPFFOMultiple`, `loadREITCapRate`) are preserved only for backward-compat with existing tests.

**Fix:** Rewrite the wrappers on top of `loadFFOConfig` (deleting ~20 lines of duplicated unmarshal structs), OR delete the wrappers and port their 6 tests to `loadFFOConfig`.

**Effort:** ~10 min.

---

### V4.1-N3: Document `NewFFOModelWithMultiple` NAV default behavior change

**Location:** `internal/services/valuation/models/ffo.go`

`NewFFOModelWithMultiple` previously left `navCapRate = 0` (zero value), silently disabling NAV cross-check. After the simplify pass, it delegates to `NewFFOModelWithConfig(..., DefaultREITCapRate, ...)`, which enables NAV cross-check with 6% cap rate.

No existing test asserts absence of NAV warnings, so no test break. But this is a behavior change worth documenting.

**Fix:** Add a godoc comment on `NewFFOModelWithMultiple`: "NAV cross-check is enabled by default with `DefaultREITCapRate`. Use `NewFFOModelWithConfig(multiple, 0, logger)` to disable."

**Effort:** ~2 min.

---

### V4.1-N4: Flatten DDM P/BV Debug log nesting

**Location:** `internal/services/valuation/models/ddm.go:150-155`

The `m.logger.Debug("P/BV cross-check", ...)` call is placed outside the `roeJustifiedPBV > 0` block but inside the `coeMinusG > epsilon` block. The nested structure is hard to follow.

**Fix:** Flatten with early-continue guards, or extract the cross-check into a helper method `(m *DDMModel) performPBVCrossCheck(...)`.

**Effort:** ~5 min.

---

## Green (suggestion) — 7 items

### V4.1-N5: Delete dead `isDeviationReasonable`

**Location:** `internal/services/valuation/crosscheck.go:155`

Only used in `crosscheck_test.go`. `FlagDivergence` now encapsulates this logic with a richer contract.

**Fix:** Delete the function and its test, OR retarget the test at `FlagDivergence` boundary semantics.

**Effort:** ~3 min.

---

### V4.1-N6: Document FCF simplification in sanity check

**Location:** `internal/services/valuation/service.go:609`

FCF used for the P/FCF sanity check is `NetIncome + D&A - CapEx` — a simplification that omits NWC change and uses NI instead of NOPAT. The DCF engine's "true FCF" includes both.

**Fix:** Add a one-line comment documenting the simplification so users don't assume `ImpliedPFCF` reflects the DCF's actual FCF definition.

**Effort:** ~2 min.

---

### V4.1-N7: DRY `roe` calculation in DDM

**Location:** `internal/services/valuation/models/ddm.go:115 + 142`

`roe := latest.NetIncome / latest.StockholdersEquity` is computed twice.

**Fix:** Hoist once near the top after the equity>0 guard.

**Effort:** ~3 min.

---

### V4.1-N8: Add `omitempty` to `ImpliedPFCF`/`SectorMedianPFCF` JSON tags

**Location:** `internal/core/entities/valuation.go:70-71`

For valuations where the P/FCF cross-check is skipped (zero FCF or zero sector median), clients see `0.0` values. Parity with cleaner API surface would use `omitempty`.

**Fix:** Add `,omitempty` to both JSON tags.

**Effort:** ~2 min.

---

### V4.1-N9: Uppercase `reit_cap_rates` keys in `industry_multiples.json`

**Location:** `config/industry_multiples.json`

`reit_cap_rates` uses lowercase keys (`residential`, `office`) while all other maps use uppercase industry codes. Today only `"default"` is read, so latent. But `LookupMultiple` uppercases its input — if industry-specific cap rates are ever wired through it, no non-default key will match.

**Fix:** Normalize all keys to uppercase (`RESIDENTIAL`, `OFFICE`, etc.) now.

**Effort:** ~2 min.

---

### V4.1-N10: SIC cache empty-string sentinel

**Location:** `internal/infra/gateways/sec/client.go:478`

`GetCompanySIC` caches `""` results (for companies genuinely without SIC). Current behavior is correct — prevents re-hitting the endpoint — but slightly confusing when reading the code because "empty" is the same as "never fetched" (except the cache Load hits).

**Fix:** Either add a comment clarifying the intent, or use a sentinel struct `type sicEntry struct { code string; fetched bool }`.

**Effort:** ~10 min (skip-worthy — reviewer noted "current behavior is correct").

---

### V4.1-N11: NAV warning format precision

**Location:** `internal/services/valuation/models/ffo.go:245`

NAV warning formats cash flows as `$%.2f`. For REITs where `valuePerShare > $1000`, this loses granularity.

**Fix:** Change format specifier to `%.4g` or add thousands separators.

**Effort:** ~2 min.

---

## Summary

| Group | Items | Effort |
|-------|-------|--------|
| Yellow | N1, N2, N3, N4 | ~35 min |
| Green  | N5, N6, N7, N8, N9, N10, N11 | ~25 min |
| **Total** | **11 items** | **~60 min** |

None of these block the v4.1 release. They are tracked here as v4.2 candidates.
