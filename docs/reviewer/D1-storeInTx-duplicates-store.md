# D1: storeInTx Duplicates Store (~80 Lines)

**Status:** OPEN  
**Severity:** WARNING  
**Found by:** REVIEWER + Superpowers Code-Reviewer (2026-04-13)  
**Location:** `internal/infra/repositories/sqlite/financial_data_repository.go`

## Description

`storeInTx` (lines 326-407) is a near-verbatim copy of `Store` (lines 31-113), differing only in the executor (`tx.NamedExecContext` vs `r.db.NamedExecContext`). Both have identical query strings, field lists, and arg maps. Future column additions must be updated in both places or data will silently not persist.

## Recommended Fix

Extract the query and args builder into a shared helper. Both `*sqlx.DB` and `*sqlx.Tx` satisfy `sqlx.ExecerContext` via `NamedExecContext`, so a single implementation can accept an executor interface:

```go
type namedExecer interface {
    NamedExecContext(ctx context.Context, query string, arg interface{}) (sql.Result, error)
}

func (r *FinancialDataRepository) storeWith(ctx context.Context, exec namedExecer, data *entities.FinancialData) error {
    // shared query + args logic
}
```

Then `Store` calls `r.storeWith(ctx, r.db, data)` and `storeInTx` calls `r.storeWith(ctx, tx, data)`.

## Risk if Not Fixed

Column-drift bug: adding a new column to `Store` but not `storeInTx` (or vice versa) would silently lose data during bulk historical writes.
