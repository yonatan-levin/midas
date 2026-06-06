# TDB-9 — Expand industry mapping coverage

**Status:** OPEN (UNDER-SPECIFIED) — filed 2026-06-06 (TODO-catalog burn-down pass).
**Priority:** P4 — Tier 4 (backlog; needs concrete scope before it is actionable).
**Type:** Enhancement.
**Mirrored as GitHub issue:** `[TDB-9]` (yonatan-levin/midas).
**Origin:** 2026-06-06 burn-down — catalog "Industry Mapping Expansion" (`datacleaner/service.go:459`).
**Related:** RM-2 (sector multiple coverage gaps) — overlapping classifier-coverage theme.

---

## Context

`datacleaner/service.go:459` carries an open-ended TODO: "Add more industry mappings as needed." There is no concrete list of missing industries, so this is not yet an actionable task — it needs a driver (a ticker that misclassifies, or a sector the screener cares about) to define scope.

## Scope / Tasks

| ID | Task | Effort |
|---|---|---|
| TDB-9.1 | Identify the concrete industries currently missing or misclassified (driven by real tickers) | S |
| TDB-9.2 | Add mappings to `config/datacleaner/industry_codes.json` + classifier coverage tests | S-M |

## Acceptance
- [ ] Concrete list of missing industries identified (this is the gating step)
- [ ] Mappings added + classifier tests cover them

## Note
Likely best handled together with RM-2 (sector multiples) since both stem from coarse classifier coverage.
