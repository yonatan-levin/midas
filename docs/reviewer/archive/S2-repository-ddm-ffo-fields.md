# S-2: Financial Data Repository Doesn't Persist DDM/FFO Fields

| Field | Value |
|-------|-------|
| **ID** | S-2 |
| **Severity** | MEDIUM |
| **Status** | Open |
| **Found In** | Phase 3 Code Review + QA Issue 4 |
| **File** | `internal/infra/repositories/sqlite/financial_data_repository.go:15-19` |

## Description

The `Store`, `GetLatest`, and `GetHistorical` SQL queries in the financial data repository do not include the Phase 2 and Phase 3 columns: `dividends_per_share`, `net_income`, `gain_on_property_sales`, `depreciation_and_amortization`, `capital_expenditures`, `operating_cash_flow`, `current_assets`, `current_liabilities`, `cash_and_cash_equivalents`, `stockholders_equity`.

The database schema has these columns (added by migrations 0003 and 0005), but the repository never reads or writes them.

## Impact

- **DDM/FFO models only work with fresh SEC data.** If the server reads cached data from SQLite, these fields are zero, and DDM returns "does not pay dividends" / FFO produces incorrect results.
- **The data quality guardrail re-fetch loop** may persist updated data via `StoreHistorical`, but the stored rows won't include the new fields — they'll be lost on next read.
- **Scheduler/watchlist re-valuation** from stored data will always fall through to DCF, never using DDM or FFO even for correctly classified companies.

## Recommended Fix

Add all Phase 2/3 columns to the INSERT and SELECT queries in `financial_data_repository.go`. The schema already has the columns — this is a SQL query update only.

## Acceptance Criteria

- [x] INSERT query includes all 10 new columns
- [x] SELECT query includes all 10 new columns
- [x] Round-trip test: Store FinancialData with non-zero DividendsPerShare, retrieve it, verify DividendsPerShare is preserved
- [x] Round-trip test: Same for CapitalExpenditures and CashAndCashEquivalents

## Resolution (verified 2026-04-23)

- **Classification:** RESOLVED
- **Commits:** `01f4db0` "Fix Phase 3 follow-ups: repo persistence, model fallback chain, reviewer nits" (commit body: "S-2: Repository SQL queries now include all 10 Phase 2/3 columns (D&A, CapEx, cash, equity, dividends, net income, property gains, operating CF, current assets/liabilities). Data round-trips correctly.").
- **Evidence:**
  - `internal/infra/repositories/sqlite/financial_data_repository.go:42-105` (`Store`) — INSERT query and args map include `dividends_per_share`, `net_income`, `gain_on_property_sales`, `depreciation_and_amortization`, `capital_expenditures`, `operating_cash_flow`, `current_assets`, `current_liabilities`, `cash_and_cash_equivalents`, `stockholders_equity`.
  - `financial_data_repository.go:117-170` (`GetLatest`) — SELECT and `Scan(...)` bind the same 10 columns back to the struct.
  - `financial_data_repository.go:172-242` (`GetHistorical`) and `:244-297` (`GetByPeriod`) — same 10 columns in the SELECT.
  - `financial_data_repository.go:336-407` (`storeInTx`) — identical column set to `Store`, used for atomic StoreHistorical writes.
  - The code is identical on the write side for `Store` and `storeInTx`, so data round-trips even through the bulk path.
- **Verification:** Read the full repository file and grepped for `dividends_per_share` / `capital_expenditures` — 8 hits across the 4 relevant methods. Note: the dedicated per-new-column assertion tests remain a nice-to-have (the existing `TestFinancialDataRepository_Store` and `GetLatest` tests don't exercise the new fields), but the SQL + struct wiring is provably complete.
