package replay

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

func TestNotFoundCacheRepo_Get_AlwaysReturnsCacheMiss(t *testing.T) {
	repo := NewNotFoundCacheRepo()
	cases := []string{"valuation:v4:AAPL", "macro:treasury", "anything"}
	for _, key := range cases {
		var dest interface{}
		err := repo.Get(context.Background(), key, &dest)
		if err == nil {
			t.Fatalf("Get(%q): expected non-nil error; got nil", key)
		}
		if !strings.Contains(err.Error(), "cache key not found") {
			t.Fatalf("Get(%q): expected miss error, got %v", key, err)
		}
	}
}

func TestNotFoundCacheRepo_Set_NoOpStillMisses(t *testing.T) {
	repo := NewNotFoundCacheRepo()
	if err := repo.Set(context.Background(), "k", 42, time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	var dest int
	if err := repo.Get(context.Background(), "k", &dest); err == nil {
		t.Fatalf("Get after Set: expected miss; got nil")
	}
}

func TestNotFoundCacheRepo_Exists_AlwaysFalse(t *testing.T) {
	repo := NewNotFoundCacheRepo()
	exists, err := repo.Exists(context.Background(), "k")
	if err != nil || exists {
		t.Fatalf("Exists: want (false, nil); got (%v, %v)", exists, err)
	}
}

func TestNotFoundCacheRepo_DeleteAndPattern_NoOps(t *testing.T) {
	repo := NewNotFoundCacheRepo()
	if err := repo.Delete(context.Background(), "k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.DeletePattern(context.Background(), "*"); err != nil {
		t.Fatalf("DeletePattern: %v", err)
	}
	keys, err := repo.GetKeys(context.Background(), "*")
	if err != nil || len(keys) != 0 {
		t.Fatalf("GetKeys: want ([], nil); got (%v, %v)", keys, err)
	}
}

func TestNotFoundFinancialDataRepo_GetHistorical_ReturnsEmpty(t *testing.T) {
	repo := NewNotFoundFinancialDataRepo()
	got, err := repo.GetHistorical(context.Background(), "AAPL", 10)
	if err != nil {
		t.Fatalf("GetHistorical: %v", err)
	}
	if got == nil {
		t.Fatalf("GetHistorical: want non-nil empty struct; got nil")
	}
	if len(got.Data) != 0 {
		t.Fatalf("GetHistorical: want empty Data; got %d entries", len(got.Data))
	}
}

func TestNotFoundFinancialDataRepo_GetLatest_ReturnsError(t *testing.T) {
	repo := NewNotFoundFinancialDataRepo()
	_, err := repo.GetLatest(context.Background(), "AAPL")
	if err == nil {
		t.Fatalf("GetLatest: expected error; got nil")
	}
}

func TestNotFoundMarketDataRepo_GetLatest_ReturnsError(t *testing.T) {
	repo := NewNotFoundMarketDataRepo()
	_, err := repo.GetLatest(context.Background(), "AAPL")
	if err == nil {
		t.Fatalf("GetLatest: expected error; got nil")
	}
}

func TestNotFoundMarketDataRepo_GetBatch_EmptyMap(t *testing.T) {
	repo := NewNotFoundMarketDataRepo()
	got, err := repo.GetBatch(context.Background(), []string{"AAPL", "MSFT"})
	if err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("GetBatch: want empty map; got %d", len(got))
	}
}

func TestNotFoundMacroDataRepo_GetLatest_ReturnsError(t *testing.T) {
	repo := NewNotFoundMacroDataRepo()
	_, err := repo.GetLatest(context.Background())
	if err == nil {
		t.Fatalf("GetLatest: expected error; got nil")
	}
}

func TestNotFoundTickerMappingRepo_GetCIK_ReturnsError(t *testing.T) {
	repo := NewNotFoundTickerMappingRepo()
	_, err := repo.GetCIK(context.Background(), "AAPL")
	if err == nil {
		t.Fatalf("GetCIK: expected error; got nil")
	}
}

func TestNotFoundTickerMappingRepo_StoreAndBulk_NoOps(t *testing.T) {
	repo := NewNotFoundTickerMappingRepo()
	if err := repo.Store(context.Background(), "AAPL", "320193"); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if err := repo.BulkStore(context.Background(), map[string]string{"AAPL": "320193"}); err != nil {
		t.Fatalf("BulkStore: %v", err)
	}
}

// recoveredPanic returns the recover() value from invoking fn. nil indicates
// no panic happened.
func recoveredPanic(fn func()) (recovered interface{}) {
	defer func() { recovered = recover() }()
	fn()
	return nil
}

func TestPanicWatchlistRepo_Methods_PanicOnCall(t *testing.T) {
	repo := NewPanicWatchlistRepo()
	cases := []struct {
		name string
		fn   func()
	}{
		{"GetActiveWatchlist", func() { _, _ = repo.GetActiveWatchlist(context.Background(), nil) }},
		{"GetAll", func() { _, _ = repo.GetAll(context.Background(), nil) }},
		{"GetByTicker", func() { _, _ = repo.GetByTicker(context.Background(), "AAPL") }},
		{"Add", func() { _ = repo.Add(context.Background(), &entities.WatchlistEntry{}) }},
		{"Update", func() { _ = repo.Update(context.Background(), "AAPL", nil) }},
		{"Remove", func() { _ = repo.Remove(context.Background(), "AAPL") }},
		{"RecordSuccess", func() { _ = repo.RecordSuccess(context.Background(), "AAPL", time.Now()) }},
		{"RecordFailure", func() { _ = repo.RecordFailure(context.Background(), "AAPL") }},
		{"GetStats", func() { _, _ = repo.GetStats(context.Background()) }},
		{"BulkUpdateFailures", func() { _ = repo.BulkUpdateFailures(context.Background(), nil) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := recoveredPanic(tc.fn)
			if r == nil {
				t.Fatalf("%s: expected panic; got none", tc.name)
			}
		})
	}
}

func TestNoOpMetricsService_AllMethodsAreNoOpsAndIncrementCallsCount(t *testing.T) {
	svc := NewNoOpMetricsService()
	op := AsNoOpMetricsService(svc)
	if op == nil {
		t.Fatalf("AsNoOpMetricsService: nil — type-cast failed")
	}
	before := op.CallsCount()
	svc.IncDCFCalculations()
	svc.IncWACCCalculations()
	svc.RecordHTTPRequest("GET", "/x", 200, time.Millisecond, 100)
	svc.RecordValuationRequest("AAPL", "single", "ok", time.Second)
	svc.RecordValuationError("AAPL", "x")
	svc.RecordSECAPIRequest("/x", "ok")
	svc.RecordMarketAPIRequest("yfinance", "ok")
	svc.RecordMacroAPIRequest("fred", "ok")
	svc.RecordDataFetch("yfinance", "AAPL", time.Millisecond)
	svc.RecordCacheRequest("memory", "get", "miss")
	svc.SetCacheHitRatio("memory", 0.5)
	svc.SetAverageWACC(0.09)
	svc.SetAverageGrowthRate(0.04)
	svc.IncHTTPRequestsInFlight()
	svc.DecHTTPRequestsInFlight()
	if op.CallsCount() <= before {
		t.Fatalf("CallsCount did not advance; before=%d after=%d", before, op.CallsCount())
	}
	if err := svc.HealthCheck(); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	// All getters return sentinels (zero values).
	if svc.GetTotalRequests() != 0 || svc.GetCacheHitRate() != 0 || svc.GetErrorRate() != 0 {
		t.Fatalf("expected zero getters")
	}
}

func TestAsNoOpMetricsService_WrongType_ReturnsNil(t *testing.T) {
	if got := AsNoOpMetricsService(nil); got != nil {
		t.Fatalf("nil arg: want nil; got %v", got)
	}
}

// TestNotFoundCompositeStubs_TrivialMethodsAreNoOps sweeps the
// no-op accessors / setters across all NotFound repos so the trivial
// pass-through methods are exercised. This bumps file-level coverage
// above the 90% gate without writing per-method targeted tests for
// stubs that have no behavior to assert beyond "doesn't panic".
func TestNotFoundCompositeStubs_TrivialMethodsAreNoOps(t *testing.T) {
	ctx := context.Background()

	// Financial data: GetByPeriod, StoreHistorical, GetLastUpdated.
	frepo := NewNotFoundFinancialDataRepo()
	if _, err := frepo.GetByPeriod(ctx, "AAPL", "2023FY"); err == nil {
		t.Fatalf("GetByPeriod: want error; got nil")
	}
	if err := frepo.StoreHistorical(ctx, &entities.HistoricalFinancialData{Ticker: "AAPL"}); err != nil {
		t.Fatalf("StoreHistorical: %v", err)
	}
	if _, err := frepo.GetLastUpdated(ctx, "AAPL"); err == nil {
		t.Fatalf("GetLastUpdated: want error; got nil")
	}
	if err := frepo.Store(ctx, &entities.FinancialData{Ticker: "AAPL"}); err != nil {
		t.Fatalf("Store: %v", err)
	}

	// Market data: Store, IsStale, GetLastUpdated.
	mrepo := NewNotFoundMarketDataRepo()
	if err := mrepo.Store(ctx, &entities.MarketData{Ticker: "AAPL"}); err != nil {
		t.Fatalf("Market.Store: %v", err)
	}
	stale, err := mrepo.IsStale(ctx, "AAPL", time.Hour)
	if err != nil || !stale {
		t.Fatalf("Market.IsStale: want (true, nil); got (%v, %v)", stale, err)
	}
	if _, err := mrepo.GetLastUpdated(ctx, "AAPL"); err == nil {
		t.Fatalf("Market.GetLastUpdated: want error; got nil")
	}

	// Macro data: Store, IsStale.
	macrorepo := NewNotFoundMacroDataRepo()
	if err := macrorepo.Store(ctx, &entities.MacroData{}); err != nil {
		t.Fatalf("Macro.Store: %v", err)
	}
	stale2, err := macrorepo.IsStale(ctx, time.Hour)
	if err != nil || !stale2 {
		t.Fatalf("Macro.IsStale: want (true, nil); got (%v, %v)", stale2, err)
	}

	// Ticker mapping: GetTicker, GetAllMappings, LoadFromSEC.
	trepo := NewNotFoundTickerMappingRepo()
	if _, err := trepo.GetTicker(ctx, "320193"); err == nil {
		t.Fatalf("GetTicker: want error; got nil")
	}
	all, err := trepo.GetAllMappings(ctx)
	if err != nil || len(all) != 0 {
		t.Fatalf("GetAllMappings: want ({}, nil); got (%v, %v)", all, err)
	}
	if err := trepo.LoadFromSEC(ctx); err != nil {
		t.Fatalf("LoadFromSEC: %v", err)
	}

	// SetNX: cache stub is a no-op-ok path.
	crepo := NewNotFoundCacheRepo()
	ok, err := crepo.SetNX(ctx, "k", 1, time.Minute)
	if err != nil || !ok {
		t.Fatalf("SetNX: want (true, nil); got (%v, %v)", ok, err)
	}

	// NoOpMetricsService: every getter returns 0 / no error.
	svc := NewNoOpMetricsService()
	if svc.GetActiveConnections() != 0 {
		t.Fatalf("GetActiveConnections: want 0")
	}
	if svc.GetAverageResponseTime() != 0 {
		t.Fatalf("GetAverageResponseTime: want 0")
	}
	if svc.GetTotalValuations() != 0 {
		t.Fatalf("GetTotalValuations: want 0")
	}
	if svc.GetSuccessfulValuations() != 0 {
		t.Fatalf("GetSuccessfulValuations: want 0")
	}
	if svc.GetFailedValuations() != 0 {
		t.Fatalf("GetFailedValuations: want 0")
	}
	if svc.GetAverageWACC() != 0 {
		t.Fatalf("GetAverageWACC: want 0")
	}
	if svc.GetAverageGrowthRate() != 0 {
		t.Fatalf("GetAverageGrowthRate: want 0")
	}
	if svc.GetUniqueTickersServed() != 0 {
		t.Fatalf("GetUniqueTickersServed: want 0")
	}
}

// TestBundleYFinanceGateway_CallsCount bumps coverage on the test-only
// accessor.
func TestBundleYFinanceGateway_CallsCount_AdvancesOnEachCall(t *testing.T) {
	tmpDir := t.TempDir()
	seedBundleFile(t, tmpDir, marketRawFile, makeMarketRaw(t, "AAPL"))
	gw := NewBundleYFinanceGateway(tmpDir, ModeRaw)

	if gw.CallsCount() != 0 {
		t.Fatalf("initial CallsCount: want 0")
	}
	_, _ = gw.GetQuote(context.Background(), "AAPL")
	if gw.CallsCount() != 1 {
		t.Fatalf("after 1 GetQuote: want 1, got %d", gw.CallsCount())
	}
}
