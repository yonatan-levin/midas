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

- [ ] INSERT query includes all 10 new columns
- [ ] SELECT query includes all 10 new columns
- [ ] Round-trip test: Store FinancialData with non-zero DividendsPerShare, retrieve it, verify DividendsPerShare is preserved
- [ ] Round-trip test: Same for CapitalExpenditures and CashAndCashEquivalents
