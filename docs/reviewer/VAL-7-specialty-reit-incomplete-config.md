# VAL-7 — SPECIALTY REIT entries are partial (multiple defined, no cap rate, no classifier matchers)

**Status:** OPEN — filed 2026-05-09 alongside the merge of Stream A.
**Severity:** Low. Dead config today — the entries exist but nothing in the classifier emits the `SPECIALTY` code.
**Origin:** Stream A V/R/Q first-pass REVIEWER finding #4 (2026-05-09).
**Blocks:** Nothing.
**Related:** VAL-3 P1+P4 (where SPECIALTY entries were added).

---

## Context

`config/industry_multiples.json` declares `reit_pffo_multiples.SPECIALTY: 17.5` and `internal/services/valuation/models/router.go::reitIndustrySet` includes `SPECIALTY`. `internal/api/v1/handlers/fair_value.go::sicToGICS` maps `SPECIALTY → {60: true}`.

But:

- `config/industry_multiples.json::reit_cap_rates` has **no** `SPECIALTY` entry — falls back to default 6%.
- `config/datacleaner/industry_codes.json` has **no** `SPECIALTY` sub-industry definition.
- The classifier therefore **never emits** `SPECIALTY` (the longest-prefix-match has nothing to find).

**Net effect.** Specialty REITs (self-storage like PSA, EXR; billboard like LAMR, OUT; prison/corrections like CXW; timber like WY, RYN) currently fall through to `default` 15× P/FFO + 6% cap rate with no subsector identification. The `SPECIALTY` config entries are reserved-but-unused.

## Why it matters

Specialty REITs in 2025-2026 trade at meaningfully different multiples and cap rates than the 15× / 6% default suggests:

| Subsegment | NTM P/FFO | Cap rate range |
|---|---|---|
| Self-storage | 18-22× | 5.0-6.0% |
| Billboard / outdoor | 14-18× | 6.5-7.5% |
| Prison / corrections | 8-11× | 9-11% |
| Timber | 16-20× | 5.5-6.5% |

Bucketing all four into one "SPECIALTY" line misses real heterogeneity, but ANY subsector-level signal would be better than the current uniform default.

## Proposed fix (one of)

### Option A — Complete the entry (recommended for serious self-storage / timber coverage)

- Add `SPECIALTY` sub-industry to `config/datacleaner/industry_codes.json` with keyword matchers:
  - `self storage`, `public storage`, `extra space`, `cubesmart`, `national storage`
  - `lamar`, `outfront`, `clear channel outdoor`
  - `corecivic`, `geo group`
  - `weyerhaeuser`, `rayonier`, `potlatch`
- Add `config/industry_multiples.json::reit_cap_rates.SPECIALTY: 0.055` (rough median; or finer subsector sub-codes if the analytical depth is wanted).

### Option B — Drop the dead entries

Remove `SPECIALTY` from `reit_pffo_multiples`, `reitIndustrySet`, `sicToGICS`. Leave the affected tickers on the default 15× / 6% path. Cleaner config, fewer reserved-but-unused codes.

### Option C — Sub-divide SPECIALTY

Replace the single SPECIALTY entry with `STORAGE_REIT`, `BILLBOARD_REIT`, `PRISON_REIT`, `TIMBER_REIT` and finer-grained multiples. Best fidelity, most config surface.

## Recommendation

- **Option A** if specialty REITs are part of the active investment universe. Cheap to implement (~15 minutes for keywords + cap rate).
- **Option B** if the unification refactor (`docs/refactoring/industry-classification-unification-spec.md`) is shipping soon — the broader refactor would supersede this anyway.
- **Option C** post-unification, when the classifier framework has cleaner subsector machinery.

Default if not decided in the next sweep: **Option A** with a single SPECIALTY bucket and the keyword set above. Option C is filed as VAL-7.A.

## Acceptance for closing this tracker

- [ ] Decision made (A, B, or C).
- [ ] Either: complete the entry (cap rate + classifier matchers) OR drop the partial entries.
- [ ] No dead config in `reit_pffo_multiples` / `reitIndustrySet` / `sicToGICS`.
- [ ] If Option A: regression test pinning at least one specialty REIT (e.g., PSA, "Public Storage", SIC 6798) → `SPECIALTY`.

## Out of scope

- Specialty-REIT-specific NAV cross-check logic (beyond the cap-rate config change).
- Per-subsegment cap rates (Option C scope).
