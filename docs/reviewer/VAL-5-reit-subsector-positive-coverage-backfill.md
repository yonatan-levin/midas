# VAL-5 — REIT-subsector sicToGICS positive-coverage backfill + small NITs

**Status:** OPEN — filed 2026-05-09 alongside the merge of Stream A's V/R/Q follow-up commit `449326a` (test-only addition for REIT-subsector match invariants).
**Severity:** Low. Functional correctness is pinned by the negative test (`TestFairValueResponse_Industry_REITSubsector_NegativeGICS`); this is paranoid-coverage padding for the still-untested individual entries.
**Origin:** Stream A small V/R/Q (2026-05-09) — QA's PASS_WITH_CONCERNS verdict flagged the 5 untested entries; REVIEWER's APPROVE flagged 2 NITs alongside.
**Blocks:** Nothing.
**Related:** RM-2 P1, VAL-3 P1+P4 (which introduced the entries), VAL-3.1 / VAL-6 (HEALTHCARE_REIT keyword collision), VAL-7 (SPECIALTY incomplete).

---

## Three deferred items

### Item 1 — Backfill 5 missing REIT subsector positives (QA gap)

The negative test pins the `matchSICToGICS` exact-match-first lookup ordering against any silent-demotion regression. But individual entries `HEALTHCARE_REIT`, `OFFICE`, `RESIDENTIAL`, `INDUSTRIAL`, `SPECIALTY` could in principle be deleted from `sicToGICS` without the negative test firing — the test asserts only `RETAIL_REIT → 60`.

**Suggested fix.** Extend `TestFairValueResponse_Industry_RealClassifier` with one positive subtest per entry. Real-classifier-driven, with company names that route to each subsector via existing keyword paths:

| Subsector | Test ticker | Company name | Keyword path |
|---|---|---|---|
| `HEALTHCARE_REIT` | WELL | "Welltower Inc." | `welltower` |
| `OFFICE` | BXP | "Boston Properties" | `boston properties` |
| `RESIDENTIAL` | EQR | "Equity Residential" | `equity residential` |
| `INDUSTRIAL` | PLD | "Prologis Inc." | `prologis` |
| `SPECIALTY` | (none — see VAL-7) | — | — |

Note `SPECIALTY` has no classifier matchers today (tracked separately as VAL-7); skip until VAL-7 lands.

### Item 2 — Crown Castle isolation NIT (REVIEWER NIT-1)

The AMT positive subtest in `449326a` uses "American Tower Corporation" which matches BOTH the RESTATE parent's `exact_names` list AND the CELLTOWER sub-industry's `american tower` keyword. Both routes converge on `CELLTOWER` so the test passes, but it doesn't isolate which path actually drove the result.

**Suggested fix.** Replace AMT's company name with "Crown Castle" or "SBA Communications" — names that match only the sub-industry keyword (no parent exact-name overlap). Cleaner regression sentinel.

### Item 3 — Symmetric negative for RETAIL_REIT vs GICS 30 (REVIEWER NIT-2)

The `RETAIL` parent maps to GICS `{25, 30}`. The current negative subtest pins only `RETAIL_REIT vs 25`. A symmetric subtest pinning `RETAIL_REIT vs 30` would close the analogous regression hole at zero cost.

---

## Acceptance for closing this tracker

- [ ] 4 new positive subtests (WELL/BXP/EQR/PLD) added.
- [ ] AMT subtest replaced with Crown Castle / SBA for clean isolation.
- [ ] Symmetric negative subtest `RETAIL_REIT vs 30` added.
- [ ] Coverage on `internal/api/v1/handlers` ≥ 93.4% (no regression).

## Out of scope

- SPECIALTY positive coverage — gated on VAL-7 (classifier matchers + cap rate first).
- Test-table refactor to systematically exercise every `sicToGICS` entry × every non-matching GICS sector. The QA agent's "single table-driven test" suggestion would generalise the pattern but is broader than this tracker.
