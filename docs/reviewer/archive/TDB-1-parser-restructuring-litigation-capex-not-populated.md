# TDB-1 — SEC parser does not populate restructuring / litigation / capitalized-interest → C1/C3/C6 never fire

**Status:** IMPLEMENTED 2026-06-06 (branch `worktree-tdb-1-parser-extraction`) — parser populates all three fields; full validation cycle green (VERIFIER VERIFIED · REVIEWER APPROVE_WITH_NITS, MAJOR + NITs addressed · QA PASS); `go test ./... -count=1` = 47/47 ok; shadow snapshots byte-identical; DDM bit-for-bit green; `currency.go` untouched. **One non-blocking operator follow-up remains:** live-replay verification of the fair-value shift on an affected ticker (e.g. JNJ) — the committed `artifacts/tier2-baseline/` is CalcVersion 4.1 and drift-confounded, so it is not a CI gate (mirrors the DC-1 Phase-5 disposition).
**Priority:** P1 — Tier 1 (valuation correctness). **The single highest-value gap surfaced by the burn-down.**
**Type:** Correctness gap (silent — no warning, no error).
**Mirrored as GitHub issue:** `[TDB-1]` (yonatan-levin/midas).
**Origin:** 2026-06-06 investigation of the catalog "Financial Data Extraction" item (re-filed residue **R1**). The 9 `service.go` extraction TODOs turned out to be dead code; this is the *real* gap they were pointing at, one layer up in the parser.
**Related:** `docs/integration/TODO_TASKS_CATALOG.md` (2026-06-06 pass), TDB-7 (deletes the dead `service.go` chain).
**Design:** ARCH spec → `docs/refactoring/spec/tdb-1-parser-nonrecurring-extraction-spec.md`; implementer plan → `docs/refactoring/implementations/tdb-1-parser-nonrecurring-extraction-implementation-plan.md` (authored 2026-06-06). Verified XBRL mapping against real basket fixtures (MSFT/JNJ/KO/F/AMD/MXL/EQIX/BABA); load-bearing-invariant verdict: DDM goldens + plug invariants INERT, recompute-shadow snapshots predicted byte-identical (income-statement fields don't touch balance-sheet umbrellas — MUST be empirically confirmed at execute). Open decisions Q1-Q4 documented in spec §9. **Sign trap identified:** JNJ tags `LitigationSettlementExpense` as −379M → `math.Abs` normalization required; `GainLossRelatedToLitigationSettlement` deliberately excluded.

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
- [x] Parser populates all three fields from real filings (us-gaap + ifrs-full candidate lists; `math.Abs` positive add-backs)
- [x] C1/C3/C6 fire on a filing that reports them — pinned by the C1/C3/C6 adjuster unit tests (real value, not the 1.5% fallback; C3/C6 above thresholds). *Live-replay confirmation is the deferred operator follow-up above.*
- [x] Regression test pins population for a known filer (`TestParser_ParseFinancialData_NonRecurringEarningsItems`, 9 cases incl. sign-trap, first-hit priority, gain-exclusion, IFRS, all-absent)
- [x] DDM bit-for-bit + shadow-snapshot invariants stay green (byte-identical)

## Out of scope
- The dead `service.go` `applyRule` chain that originally carried these TODOs — deleted under **TDB-7**.
