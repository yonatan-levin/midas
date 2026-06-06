# TDB-1 — SEC parser does not populate restructuring / litigation / capitalized-interest → C1/C3/C6 never fire

**Status:** OPEN — filed 2026-06-06 (TODO-catalog burn-down pass).
**Priority:** P1 — Tier 1 (valuation correctness). **The single highest-value gap surfaced by the burn-down.**
**Type:** Correctness gap (silent — no warning, no error).
**Mirrored as GitHub issue:** `[TDB-1]` (yonatan-levin/midas).
**Origin:** 2026-06-06 investigation of the catalog "Financial Data Extraction" item (re-filed residue **R1**). The 9 `service.go` extraction TODOs turned out to be dead code; this is the *real* gap they were pointing at, one layer up in the parser.
**Related:** `docs/integration/TODO_TASKS_CATALOG.md` (2026-06-06 pass), TDB-7 (deletes the dead `service.go` chain).

---

## Context

The DC-1 earnings-normalization adjusters read real entity fields:
- **C1** `ApplyC1Restructuring` reads `working.RestructuringCharges` (`adjustments/earnings.go:144`)
- **C3** `ApplyC3Litigation` reads `working.LitigationSettlements` (`adjustments/earnings.go:338`)
- **C6** `ApplyC6CapitalizedInterest` reads `working.CapitalizedInterest` (`adjustments/earnings.go:560`)

But the SEC parser (`internal/infra/gateways/sec/parser.go`) **never populates** any of these three fields — verified by grep: zero non-test write sites in `internal/`, and the corresponding XBRL tags are absent from the parser. So in production:
- C1 falls back to a **1.5%-of-revenue estimate** (`earnings.go:146`)
- C3 and C6 **skip entirely** (the field is 0)

## Why it matters

These adjustments feed `NormalizedOperatingIncome`, which drives the DCF. A company with a large one-time restructuring or litigation charge has its operating income silently un-normalized → distorted fair value, with **no warning** to the consumer. This is exactly the "extract actual X from financial data" intent of the original 2025 TODOs — it was just mislocated in dead cleaner code instead of the parser.

## Scope / Tasks

| ID | Task | File | Effort |
|---|---|---|---|
| TDB-1.1 | Identify the us-gaap / IFRS XBRL concepts for restructuring charges, litigation settlements/accruals, capitalized interest | `sec/parser.go`, XBRL tag config | M |
| TDB-1.2 | Populate `RestructuringCharges`, `LitigationSettlements`, `CapitalizedInterest` in `parsePeriodData` | `sec/parser.go` | M |
| TDB-1.3 | Confirm C1 stops using the 1.5%-revenue fallback when the real value is present; C3/C6 now fire | `adjustments/earnings.go` | S |
| TDB-1.4 | Regression test on a known filer that reports these (replay bundle or fixture) | `internal/integration` / `sec` tests | M |

## Acceptance
- [ ] Parser populates all three fields from real filings
- [ ] C1/C3/C6 fire on a filing that reports them (replay or live verification)
- [ ] Regression test pins population for a known filer
- [ ] DDM bit-for-bit + shadow-snapshot invariants stay green

## Out of scope
- The dead `service.go` `applyRule` chain that originally carried these TODOs — deleted under **TDB-7**.
