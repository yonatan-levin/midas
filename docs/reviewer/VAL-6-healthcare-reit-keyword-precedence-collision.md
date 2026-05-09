# VAL-6 — HEALTHCARE_REIT keyword precedence collision (Healthpeak / Ventas Healthcare class)

**Status:** OPEN — filed 2026-05-09 alongside the merge of Stream A.
**Severity:** Medium. Same defect class as the documented DLR/TECH known-fail; affects healthcare REITs whose company names contain HEALTH-parent keywords.
**Origin:** Stream A V/R/Q first-pass REVIEWER finding #2 (2026-05-09).
**Blocks:** Nothing functionally — affected tickers still get a sensible value via the HEALTH path. But the `HEALTHCARE_REIT` subsector multiple (17.5× P/FFO) and cap rate (6.0%) are bypassed entirely.
**Related:** RM-2 P1, VAL-3 P1+P4 (which introduced HEALTHCARE_REIT), `docs/refactoring/industry-classification-unification-spec.md` (the canonical solution path), inline DLR known-fail comment at `classifier_val3p1_reit_test.go:28-36`.

---

## Context

`config/datacleaner/industry_codes.json` HEALTH parent has the pattern `\b(health|medical|pharma|bio)\b` at priority 85. The RESTATE parent (where REIT subsectors live) is at priority 65. So any company name containing "health" or "medical" matches HEALTH first, regardless of the SIC code.

### Affected ticker class

| Ticker | Company name | SIC | Today's route | Should-be route |
|---|---|---|---|---|
| **DOC** | Healthpeak Properties | 6798 | HEALTH parent → default HEALTH multiple | RESTATE → HEALTHCARE_REIT (17.5× P/FFO) |
| **VTR** | Ventas, Inc. | 6798 | RESTATE → HEALTHCARE_REIT (works — name has no HEALTH keyword) | OK |
| **OHI** | Omega Healthcare Investors | 6798 | HEALTH parent → default | RESTATE → HEALTHCARE_REIT |
| **MPW** | Medical Properties Trust | 6798 | HEALTH parent (matches "medical") → default | RESTATE → HEALTHCARE_REIT |

So the bug fires on Healthpeak / Omega / Medical Properties Trust, but NOT Welltower or Ventas (those have no HEALTH keyword in name). Same shape as DLR (matches `\b(tech\|software\|digital\|cyber)\b` priority 100) being misrouted out of RESTATE.

## Why it matters

Healthcare REITs are a 6-7% slice of the public REIT universe. Mis-routing them to HEALTH multiples (typically 22× P/E or 14× EV/EBITDA) instead of HEALTHCARE_REIT P/FFO multiple (17.5×) produces meaningfully different valuations because the underlying methodology is different — REITs distribute earnings as dividends; the FFO model captures this; the DCF/multiple-on-earnings path applied to REITs systematically understates value because earnings are depressed by depreciation.

## Proposed fix

Same root-cause solution as the unification spec: invert global classifier precedence so SIC outranks company-name keywords. SIC 6798 unambiguously identifies a REIT; the keyword "health" should not override that. Tracked in `docs/refactoring/industry-classification-unification-spec.md`.

### Hotfix alternative — rejected

Removing `health`/`medical` from the HEALTH parent pattern would whack-a-mole — biotech and pharma names contain those tokens too, and stripping them would mis-route the actual healthcare companies. The unification refactor is the correct scope.

## Acceptance for closing this tracker

- [ ] Unification spec updated to enumerate the HEALTHCARE_REIT case in the documented affected set (companion to the DLR/TECH case).
- [ ] When unification ships: regression test pinning Healthpeak (SIC 6798, name "Healthpeak Properties") → `HEALTHCARE_REIT`.
- [ ] Inline DLR known-fail comment in `classifier_val3p1_reit_test.go` updated to mention the parallel HEALTH-class collision.

## Out of scope

- Hotfix the HEALTH keyword pattern.
- Per-subsector classifier rules outside the priority-inversion framework.
- Backporting the fix to existing cached `valuation_results` rows — they'll self-heal on next request.
