# BUG-003: DataFetcher does not persist fetched data to repositories

| Field | Value |
|-------|-------|
| **ID** | BUG-003 |
| **Title** | DataFetcher returns fetched data in-memory but never writes to financial/market/macro repositories |
| **Severity** | BLOCKER |
| **Status** | Resolved (2026-04-05) |
| **Component** | DataFetcher Service / Valuation Service |
| **Reported** | 2026-03-30 |
| **Environment** | Development (Windows 11, Go 1.24.4) |
| **Affects** | Every non-seeded ticker — fetched data is thrown away |

## Summary

The `DataFetcher.Fetch()` method coordinates data retrieval from SEC, Yahoo Finance, and FRED, and returns the results in a `FetchResult` struct. However, **it never writes the fetched data to any repository** (financial, market, or macro). The `ValuationService` calls `Fetch()`, **discards the return value** (assigns to `_`), then immediately re-reads from the financial repository — which is empty because nothing was persisted. This architectural gap means that even when external APIs return valid data, the valuation pipeline cannot use it.

## Impact

This is the third piece of the "real-world valuations don't work" puzzle. Even if BUG-001 (SEC struct) and BUG-002 (Yahoo Finance auth) are fixed, valuations will **still fail** because the fetched data is never stored and the valuation service can't find it.

The flow today:
```
DataFetcher.Fetch() → returns FetchResult (in-memory) → result discarded
ValuationService → reads from financialRepo → empty → "no historical data found" → 500
```

## Steps to Reproduce

1. Start the server with debug logging: `LOG_LEVEL=debug go run cmd/server/main.go`
2. Request a non-seeded ticker:
   ```bash
   curl -H "X-API-Key: dcf_demo_3a4a5b6c7d8e9f00112233445566778899aabbccddeeff001122334455667788" \
     http://localhost:8080/api/v1/fair-value/MSFT
   ```
3. Observe HTTP 500 response
4. Check server logs in sequence:
   ```
   "msg":"No historical data in repository, fetching via DataFetcher","ticker":"MSFT"
   ... (DataFetcher runs, fetches from SEC/Market/Macro) ...
   "msg":"failed to fetch financial data: no historical data found for ticker MSFT"
   ```
   The DataFetcher runs but the repository is still empty afterward.

5. Verify no data was written to the database:
   ```bash
   sqlite3 data/midas.db "SELECT COUNT(*) FROM financial_data WHERE ticker='MSFT';"
   # Returns: 0
   ```

## Root Cause Analysis

### The discard pattern (`internal/services/valuation/service.go:86-94`)

```go
// Line 86: Fetch is called but the result is DISCARDED (assigned to _)
_, fetchErr := s.dataFetcher.Fetch(ctx, fetchRequest)
if fetchErr != nil {
    return nil, fmt.Errorf("failed to fetch data via DataFetcher: %w", fetchErr)
}

// Line 92: Immediately re-reads from repository (which is still empty)
historicalData, err = s.financialRepo.GetHistorical(ctx, ticker, 10)
if err != nil || len(historicalData.Data) == 0 {
    return nil, fmt.Errorf("failed to fetch financial data: no historical data found for ticker %s", ticker)
}
```

The comment at line 91 says: _"DataFetcher should have populated the database, try repository again"_. This reveals the design intent — the developer expected the DataFetcher to write to the repository. But it doesn't.

### The DataFetcher has no repository references

Looking at `internal/services/datafetcher/service.go:15-27`:

```go
type DataFetcher struct {
    secGateway    ports.SECGateway
    marketGateway ports.MarketDataGateway
    macroGateway  ports.MacroDataGateway
    cacheRepo     ports.CacheRepository      // Only cache, not data repos
    validator     *DataValidator
    coordinator   *DataCoordinator
    config        *DataFetcherConfig
    metrics       *entities.DataFetcherMetrics
    metricsMutex  sync.RWMutex
}
```

The DataFetcher holds references to gateways (for fetching) and `cacheRepo` (for caching raw results), but **does NOT hold** `FinancialDataRepository`, `MarketDataRepository`, or `MacroDataRepository`. It literally cannot persist data because it has no access to the persistence layer.

### Confirmation: zero repository writes in the entire datafetcher package

A search for `.Store(`, `.StoreHistorical(`, `financialRepo`, `marketRepo`, or `macroRepo` across all files in `internal/services/datafetcher/` returns **zero matches**. The DataFetcher package never writes to any repository.

### The DI wiring confirms the gap

In `internal/di/container.go:554-566`:

```go
func NewDataFetcher(
    secGateway ports.SECGateway,
    marketGateway ports.MarketDataGateway,
    macroGateway ports.MacroDataGateway,
    cache ports.CacheRepository,
) *datafetcher.DataFetcher {
    return datafetcher.NewDataFetcher(
        secGateway,
        marketGateway,
        macroGateway,
        cache,
    )
}
```

The DI constructor injects only gateways and cache — no data repositories.

## Affected Files

| File | Lines | Role |
|------|-------|------|
| `internal/services/valuation/service.go` | 86-94 | Discards `FetchResult`, re-reads from empty repo |
| `internal/services/datafetcher/service.go` | 15-27 | No repository fields in struct |
| `internal/services/datafetcher/service.go` | 77+ | `Fetch()` — returns data but doesn't store it |
| `internal/services/datafetcher/coordinator.go` | 192+ | `coordinateConcurrent()` — assembles data in-memory |
| `internal/di/container.go` | 554-566 | DI wiring — no repos injected into DataFetcher |

## Proposed Fix Options

### Option A: DataFetcher persists to repositories (Recommended)

Add repository dependencies to the DataFetcher and write fetched data to them:

1. **Add repository fields to DataFetcher struct**:
   ```go
   type DataFetcher struct {
       // ... existing fields ...
       financialRepo ports.FinancialDataRepository
       marketRepo    ports.MarketDataRepository
       macroRepo     ports.MacroDataRepository
   }
   ```

2. **Update constructor and DI wiring** to inject repositories

3. **Add persistence at the end of `Fetch()`**:
   ```go
   func (df *DataFetcher) Fetch(ctx context.Context, request *entities.FetchRequest) (*entities.FetchResult, error) {
       // ... existing fetch logic ...

       // Persist fetched data to repositories
       if result.FinancialData != nil {
           if err := df.financialRepo.StoreHistorical(ctx, result.FinancialData); err != nil {
               df.logger.Warn("Failed to persist financial data", zap.Error(err))
               // Non-fatal: data is still in the result
           }
       }
       if result.MarketData != nil {
           if err := df.marketRepo.Store(ctx, result.MarketData); err != nil {
               df.logger.Warn("Failed to persist market data", zap.Error(err))
           }
       }
       if result.MacroData != nil {
           if err := df.macroRepo.Store(ctx, result.MacroData); err != nil {
               df.logger.Warn("Failed to persist macro data", zap.Error(err))
           }
       }

       return result, nil
   }
   ```

4. **Keep the valuation service's re-read pattern** — it serves as a consistency check and works with the cache

### Option B: Valuation service uses FetchResult directly

Instead of discarding the FetchResult, use the data it contains:

```go
fetchResult, fetchErr := s.dataFetcher.Fetch(ctx, fetchRequest)
if fetchErr != nil {
    return nil, fmt.Errorf("failed to fetch data via DataFetcher: %w", fetchErr)
}

// Use the fetched data directly instead of re-reading from repo
if fetchResult.FinancialData != nil && len(fetchResult.FinancialData.Data) > 0 {
    historicalData = fetchResult.FinancialData
}
```

### Recommendation

**Option A** is the correct architectural fix — it maintains the clean architecture pattern where the DataFetcher is responsible for both fetching AND persisting, and the valuation service reads from the repository as the single source of truth. This also means data is cached in the DB for future requests.

**Option B** is a quick fix but bypasses the repository pattern and means data isn't cached in the DB.

**Best approach**: Implement Option A, then also fix the valuation service to **not discard** the FetchResult (remove the `_`) as a safety net, using the in-memory data if the repository re-read still fails.

## Regression Risks

- Existing DataFetcher tests use mock gateways and don't test repository writes — they will continue to pass
- Need to update DataFetcher tests to verify repository writes
- Need to ensure the `FetchResult.FinancialData` structure matches what `FinancialDataRepository.StoreHistorical()` expects
- The `companies` table has a foreign key constraint — ticker must exist in `companies` before `financial_data` can be inserted. The persist logic must handle company upsert first.

## Acceptance Criteria

- [ ] After calling `DataFetcher.Fetch("MSFT")`, the `financial_data` table contains rows for MSFT
- [ ] After calling `DataFetcher.Fetch("MSFT")`, the `market_data` table contains a row for MSFT
- [ ] After calling `DataFetcher.Fetch("MSFT")`, the `macro_data` table contains current rates
- [ ] `ValuationService.CalculateValuation("MSFT")` succeeds after the fetch
- [ ] Second call to `CalculateValuation("MSFT")` uses cached DB data (no external API calls)
- [ ] Unit test: DataFetcher.Fetch writes to mock repositories
- [ ] Integration test: Full pipeline fetch → persist → read → calculate for a real ticker

## Dependency

This bug depends on BUG-001 and BUG-002 being fixed first — if SEC data can't be parsed and market data can't be fetched, there's nothing to persist. However, the persistence fix should be implemented concurrently so all three fixes can be tested together.

## References

- Comment in `service.go:91`: "DataFetcher should have populated the database" — confirms the original design intent
- Clean Architecture principle: the service layer orchestrates, infrastructure adapters persist
- Hexagonal Architecture: ports (interfaces) define the contract, adapters (repositories) implement storage

## Resolution (verified 2026-04-23)

- **Classification**: RESOLVED (architecture pivoted to Option B — valuation service consumes `FetchResult` directly)
- **Fix commit**: `9841939` ("Fix 9 bugs: real-world valuations working end-to-end")
- **Evidence inspected**:
  - `internal/services/valuation/service.go:153-213` — `s.dataFetcher.Fetch` result is now consumed directly (`fetchResult.HistoricalData`, `fetchResult.FinancialData`, `fetchResult.MarketData`, `fetchResult.MacroData`); the value is no longer discarded with `_`
  - Grep in `internal/services/datafetcher/` for `financialRepo|marketRepo|macroRepo|StoreHistorical` returns zero matches, confirming the DataFetcher never owned persistence — the valuation pipeline was refactored to use the in-memory result as the source of truth instead
  - Result: end-to-end valuations now complete without relying on the financial repository being pre-populated
