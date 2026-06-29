package datacleaner

// SR-1 B6 regression pin. On a cache hit the cleaner previously wrote
// ProcessingTime directly onto the SHARED cached *CleaningResult before
// returning it — a write visible to any concurrent reader holding the same
// pointer (a data race). The fix returns a shallow copy with the fresh
// ProcessingTime, leaving the cached object untouched.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestCleanFinancialData_CacheHit_NoSharedMutation runs concurrent
// CleanFinancialData calls on the same cache key and asserts:
//
//	(a) no data race under `go test -race` (the cache-hit path no longer
//	    mutates the shared cached pointer); and
//	(b) the cached object's ProcessingTime is not mutated by readers — it
//	    retains the value stamped at cache-miss time, while each returned
//	    copy may carry its own fresh ProcessingTime.
func TestCleanFinancialData_CacheHit_NoSharedMutation(t *testing.T) {
	cfg := createTestConfig()
	require.True(t, cfg.DataCleaner.EnableCaching,
		"test prerequisite: DataCleaner.EnableCaching must be true to exercise the cache-hit path")

	svcIface, err := NewDataCleanerService(cfg, &mockAIServiceDataCleaner{}, nil)
	require.NoError(t, err)
	svc, ok := svcIface.(*service)
	require.True(t, ok, "expected concrete *service for white-box cache inspection")

	data := &entities.FinancialData{
		Ticker:                   "RACE1",
		Revenue:                  500_000_000,
		TotalAssets:              1_000_000_000,
		Goodwill:                 400_000_000,
		SharesOutstanding:        100_000_000,
		DilutedSharesOutstanding: 100_000_000,
		FilingPeriod:             "2024Q3",
		FilingDate:               time.Now().AddDate(0, -3, 0),
		HasNormalizedData:        true,
	}

	// Prime the cache (cache-miss path stores the shared result).
	_, err = svc.CleanFinancialData(context.Background(), data)
	require.NoError(t, err)

	cacheKey := generateCacheKey(data)
	cached := svc.getCachedResult(cacheKey)
	require.NotNil(t, cached, "result must be cached after the first call")
	cachedProcessingTimeBefore := cached.ProcessingTime

	// Hammer the cache-hit path concurrently. Under -race, a write to the
	// shared cached pointer here would be flagged.
	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			r, err := svc.CleanFinancialData(context.Background(), data)
			if err != nil {
				t.Errorf("concurrent CleanFinancialData failed: %v", err)
				return
			}
			require.NotNil(t, r)
		}()
	}
	wg.Wait()

	// (b) The cached object's ProcessingTime must be unchanged by the readers.
	require.Equal(t, cachedProcessingTimeBefore, svc.getCachedResult(cacheKey).ProcessingTime,
		"cache-hit path must not mutate the shared cached result's ProcessingTime")
}
