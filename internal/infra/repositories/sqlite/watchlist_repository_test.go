package sqlite

import (
	"context"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// setupWatchlistRepo creates an in-memory DB + WatchlistRepository for testing.
// Reuses setupAuthTestDB (same package) which loads the full schema with FKs disabled.
func setupWatchlistRepo(t *testing.T) *WatchlistRepository {
	t.Helper()
	db := setupAuthTestDB(t) // *sql.DB, in-memory, schema applied, FKs off
	repo := NewWatchlistRepository(db)
	return repo.(*WatchlistRepository)
}

// seedEntry adds a watchlist entry and returns it for assertions.
func seedEntry(t *testing.T, repo *WatchlistRepository, ticker string, active bool, priority int) {
	t.Helper()
	err := repo.Add(context.Background(), &entities.WatchlistEntry{
		Ticker:   ticker,
		IsActive: active,
		Priority: priority,
	})
	require.NoError(t, err, "seeding %s", ticker)
}

// ---------------------------------------------------------------------------
// NewWatchlistRepository
// ---------------------------------------------------------------------------

func TestWatchlistRepository_New(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := NewWatchlistRepository(db)
	assert.NotNil(t, repo)
}

// ---------------------------------------------------------------------------
// Add + GetByTicker round-trip
// ---------------------------------------------------------------------------

func TestWatchlistRepository_Add_GetByTicker(t *testing.T) {
	repo := setupWatchlistRepo(t)
	ctx := context.Background()

	err := repo.Add(ctx, &entities.WatchlistEntry{
		Ticker:   "AAPL",
		IsActive: true,
		Priority: int(entities.HighPriority),
	})
	require.NoError(t, err)

	entry, err := repo.GetByTicker(ctx, "AAPL")
	require.NoError(t, err)
	require.NotNil(t, entry)

	assert.Equal(t, "AAPL", entry.Ticker)
	assert.True(t, entry.IsActive)
	assert.Equal(t, int(entities.HighPriority), entry.Priority)
	assert.Equal(t, 5, entry.MaxFailures, "default max_failures should be 5")
	assert.Equal(t, "manual", entry.AddedReason, "default reason should be 'manual'")
	assert.Equal(t, 0, entry.FetchFailures)
	assert.Nil(t, entry.LastFetchedAt)
}

func TestWatchlistRepository_Add_Defaults(t *testing.T) {
	repo := setupWatchlistRepo(t)
	ctx := context.Background()

	// Add with zero-value priority and empty reason — should get defaults
	err := repo.Add(ctx, &entities.WatchlistEntry{
		Ticker:   "MSFT",
		IsActive: true,
	})
	require.NoError(t, err)

	entry, err := repo.GetByTicker(ctx, "MSFT")
	require.NoError(t, err)
	assert.Equal(t, int(entities.MediumPriority), entry.Priority, "default priority")
	assert.Equal(t, "manual", entry.AddedReason, "default reason")
	assert.Equal(t, 5, entry.MaxFailures, "default max_failures")
}

func TestWatchlistRepository_Add_DuplicateTicker(t *testing.T) {
	repo := setupWatchlistRepo(t)
	ctx := context.Background()

	seedEntry(t, repo, "AAPL", true, 1)

	// Adding duplicate should fail (UNIQUE constraint on ticker)
	err := repo.Add(ctx, &entities.WatchlistEntry{
		Ticker:   "AAPL",
		IsActive: true,
		Priority: 2,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to add watchlist entry")
}

func TestWatchlistRepository_GetByTicker_NotFound(t *testing.T) {
	repo := setupWatchlistRepo(t)
	entry, err := repo.GetByTicker(context.Background(), "ZZZZZ")
	assert.NoError(t, err, "not found should not be an error")
	assert.Nil(t, entry)
}

// ---------------------------------------------------------------------------
// GetAll with filters
// ---------------------------------------------------------------------------

func TestWatchlistRepository_GetAll_NoFilter(t *testing.T) {
	repo := setupWatchlistRepo(t)
	ctx := context.Background()

	seedEntry(t, repo, "AAPL", true, int(entities.HighPriority))
	seedEntry(t, repo, "MSFT", true, int(entities.MediumPriority))
	seedEntry(t, repo, "DEAD", false, int(entities.LowPriority))

	entries, err := repo.GetAll(ctx, nil)
	require.NoError(t, err)
	assert.Len(t, entries, 3)
	// Ordered by priority ASC, ticker ASC
	assert.Equal(t, "AAPL", entries[0].Ticker) // priority 1
	assert.Equal(t, "MSFT", entries[1].Ticker) // priority 2
	assert.Equal(t, "DEAD", entries[2].Ticker) // priority 3
}

func TestWatchlistRepository_GetAll_ActiveFilter(t *testing.T) {
	repo := setupWatchlistRepo(t)
	ctx := context.Background()

	seedEntry(t, repo, "AAPL", true, 1)
	seedEntry(t, repo, "DEAD", false, 3)

	isActive := true
	entries, err := repo.GetAll(ctx, &entities.WatchlistFilter{IsActive: &isActive})
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, "AAPL", entries[0].Ticker)
}

func TestWatchlistRepository_GetAll_PriorityFilter(t *testing.T) {
	repo := setupWatchlistRepo(t)
	ctx := context.Background()

	seedEntry(t, repo, "AAPL", true, int(entities.HighPriority))
	seedEntry(t, repo, "MSFT", true, int(entities.MediumPriority))

	high := entities.HighPriority
	entries, err := repo.GetAll(ctx, &entities.WatchlistFilter{Priority: &high})
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, "AAPL", entries[0].Ticker)
}

func TestWatchlistRepository_GetAll_MaxFailuresFilter(t *testing.T) {
	repo := setupWatchlistRepo(t)
	ctx := context.Background()

	seedEntry(t, repo, "GOOD", true, 1)
	seedEntry(t, repo, "BAD", true, 1)

	// Record 3 failures for BAD
	for i := 0; i < 3; i++ {
		require.NoError(t, repo.RecordFailure(ctx, "BAD"))
	}

	maxFail := 2
	entries, err := repo.GetAll(ctx, &entities.WatchlistFilter{MaxFailures: &maxFail})
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, "GOOD", entries[0].Ticker)
}

func TestWatchlistRepository_GetAll_LimitOffset(t *testing.T) {
	repo := setupWatchlistRepo(t)
	ctx := context.Background()

	seedEntry(t, repo, "A", true, 1)
	seedEntry(t, repo, "B", true, 1)
	seedEntry(t, repo, "C", true, 1)

	entries, err := repo.GetAll(ctx, &entities.WatchlistFilter{Limit: 2})
	require.NoError(t, err)
	assert.Len(t, entries, 2)

	entries, err = repo.GetAll(ctx, &entities.WatchlistFilter{Limit: 2, Offset: 2})
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, "C", entries[0].Ticker)
}

// ---------------------------------------------------------------------------
// GetActiveWatchlist
// ---------------------------------------------------------------------------

func TestWatchlistRepository_GetActiveWatchlist(t *testing.T) {
	repo := setupWatchlistRepo(t)
	ctx := context.Background()

	seedEntry(t, repo, "AAPL", true, 1)
	seedEntry(t, repo, "MSFT", true, 2)
	seedEntry(t, repo, "DEAD", false, 3)

	entries, err := repo.GetActiveWatchlist(ctx, nil)
	require.NoError(t, err)
	assert.Len(t, entries, 2, "only active entries")

	for _, e := range entries {
		assert.True(t, e.IsActive)
	}
}

func TestWatchlistRepository_GetActiveWatchlist_EmptyFilter(t *testing.T) {
	repo := setupWatchlistRepo(t)
	ctx := context.Background()

	seedEntry(t, repo, "AAPL", true, 1)

	entries, err := repo.GetActiveWatchlist(ctx, &entities.WatchlistFilter{})
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func TestWatchlistRepository_Update_SingleField(t *testing.T) {
	repo := setupWatchlistRepo(t)
	ctx := context.Background()

	seedEntry(t, repo, "AAPL", true, int(entities.MediumPriority))

	inactive := false
	err := repo.Update(ctx, "AAPL", &entities.UpdateWatchlistEntryRequest{IsActive: &inactive})
	require.NoError(t, err)

	entry, err := repo.GetByTicker(ctx, "AAPL")
	require.NoError(t, err)
	assert.False(t, entry.IsActive)
	assert.Equal(t, int(entities.MediumPriority), entry.Priority, "untouched field unchanged")
}

func TestWatchlistRepository_Update_MultipleFields(t *testing.T) {
	repo := setupWatchlistRepo(t)
	ctx := context.Background()

	seedEntry(t, repo, "AAPL", true, int(entities.LowPriority))

	newPriority := entities.HighPriority
	newMax := 10
	err := repo.Update(ctx, "AAPL", &entities.UpdateWatchlistEntryRequest{
		Priority:    &newPriority,
		MaxFailures: &newMax,
	})
	require.NoError(t, err)

	entry, err := repo.GetByTicker(ctx, "AAPL")
	require.NoError(t, err)
	assert.Equal(t, int(entities.HighPriority), entry.Priority)
	assert.Equal(t, 10, entry.MaxFailures)
}

func TestWatchlistRepository_Update_EmptyUpdate(t *testing.T) {
	repo := setupWatchlistRepo(t)
	ctx := context.Background()

	seedEntry(t, repo, "AAPL", true, 1)

	// No fields set → no-op, no error
	err := repo.Update(ctx, "AAPL", &entities.UpdateWatchlistEntryRequest{})
	assert.NoError(t, err)
}

func TestWatchlistRepository_Update_NotFound(t *testing.T) {
	repo := setupWatchlistRepo(t)
	inactive := false
	err := repo.Update(context.Background(), "ZZZZZ", &entities.UpdateWatchlistEntryRequest{IsActive: &inactive})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ---------------------------------------------------------------------------
// Remove
// ---------------------------------------------------------------------------

func TestWatchlistRepository_Remove(t *testing.T) {
	repo := setupWatchlistRepo(t)
	ctx := context.Background()

	seedEntry(t, repo, "AAPL", true, 1)

	err := repo.Remove(ctx, "AAPL")
	require.NoError(t, err)

	entry, err := repo.GetByTicker(ctx, "AAPL")
	require.NoError(t, err)
	assert.Nil(t, entry, "should be gone after remove")
}

func TestWatchlistRepository_Remove_NotFound(t *testing.T) {
	repo := setupWatchlistRepo(t)
	err := repo.Remove(context.Background(), "NOPE")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ---------------------------------------------------------------------------
// RecordSuccess
// ---------------------------------------------------------------------------

func TestWatchlistRepository_RecordSuccess(t *testing.T) {
	repo := setupWatchlistRepo(t)
	ctx := context.Background()

	seedEntry(t, repo, "AAPL", true, 1)

	// Record a few failures first
	require.NoError(t, repo.RecordFailure(ctx, "AAPL"))
	require.NoError(t, repo.RecordFailure(ctx, "AAPL"))

	// Now record success
	fetchedAt := time.Now().UTC().Truncate(time.Second)
	err := repo.RecordSuccess(ctx, "AAPL", fetchedAt)
	require.NoError(t, err)

	entry, err := repo.GetByTicker(ctx, "AAPL")
	require.NoError(t, err)
	assert.Equal(t, 0, entry.FetchFailures, "failures reset to 0")
	assert.NotNil(t, entry.LastFetchedAt)
}

func TestWatchlistRepository_RecordSuccess_NotFound(t *testing.T) {
	repo := setupWatchlistRepo(t)
	err := repo.RecordSuccess(context.Background(), "NOPE", time.Now())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ---------------------------------------------------------------------------
// RecordFailure + auto-disable
// ---------------------------------------------------------------------------

func TestWatchlistRepository_RecordFailure(t *testing.T) {
	repo := setupWatchlistRepo(t)
	ctx := context.Background()

	seedEntry(t, repo, "AAPL", true, 1)

	err := repo.RecordFailure(ctx, "AAPL")
	require.NoError(t, err)

	entry, err := repo.GetByTicker(ctx, "AAPL")
	require.NoError(t, err)
	assert.Equal(t, 1, entry.FetchFailures)
	assert.True(t, entry.IsActive, "still active after 1 failure")
}

func TestWatchlistRepository_RecordFailure_AutoDisable(t *testing.T) {
	repo := setupWatchlistRepo(t)
	ctx := context.Background()

	// Add with max_failures=3
	err := repo.Add(ctx, &entities.WatchlistEntry{
		Ticker:      "FLAKY",
		IsActive:    true,
		Priority:    1,
		MaxFailures: 3,
	})
	require.NoError(t, err)

	// Fail 3 times — should auto-disable at max_failures
	for i := 0; i < 3; i++ {
		require.NoError(t, repo.RecordFailure(ctx, "FLAKY"))
	}

	entry, err := repo.GetByTicker(ctx, "FLAKY")
	require.NoError(t, err)
	assert.Equal(t, 3, entry.FetchFailures)
	assert.False(t, entry.IsActive, "auto-disabled after hitting max_failures")
}

func TestWatchlistRepository_RecordFailure_NotFound(t *testing.T) {
	repo := setupWatchlistRepo(t)
	err := repo.RecordFailure(context.Background(), "NOPE")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ---------------------------------------------------------------------------
// GetStats
// ---------------------------------------------------------------------------

func TestWatchlistRepository_GetStats(t *testing.T) {
	repo := setupWatchlistRepo(t)
	ctx := context.Background()

	seedEntry(t, repo, "AAPL", true, int(entities.HighPriority))
	seedEntry(t, repo, "MSFT", true, int(entities.MediumPriority))
	seedEntry(t, repo, "GOOG", true, int(entities.LowPriority))
	seedEntry(t, repo, "DEAD", false, int(entities.MediumPriority))

	// Record a failure for GOOG (recent)
	require.NoError(t, repo.RecordFailure(ctx, "GOOG"))

	stats, err := repo.GetStats(ctx)
	require.NoError(t, err)

	assert.Equal(t, 4, stats.TotalEntries)
	assert.Equal(t, 3, stats.ActiveEntries)
	assert.Equal(t, 1, stats.InactiveEntries)
	assert.Equal(t, 1, stats.HighPriority)
	assert.Equal(t, 2, stats.MediumPriority) // MSFT + DEAD
	assert.Equal(t, 1, stats.LowPriority)
	assert.Equal(t, 1, stats.RecentFailures)
}

func TestWatchlistRepository_GetStats_Empty(t *testing.T) {
	repo := setupWatchlistRepo(t)
	stats, err := repo.GetStats(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, stats.TotalEntries)
}

// ---------------------------------------------------------------------------
// BulkUpdateFailures
// ---------------------------------------------------------------------------

func TestWatchlistRepository_BulkUpdateFailures(t *testing.T) {
	repo := setupWatchlistRepo(t)
	ctx := context.Background()

	seedEntry(t, repo, "AAPL", true, 1)
	seedEntry(t, repo, "MSFT", true, 1)
	seedEntry(t, repo, "GOOG", true, 1)

	// AAPL succeeds, MSFT fails, GOOG succeeds
	err := repo.BulkUpdateFailures(ctx, map[string]bool{
		"AAPL": true,
		"MSFT": false,
		"GOOG": true,
	})
	require.NoError(t, err)

	aapl, _ := repo.GetByTicker(ctx, "AAPL")
	assert.Equal(t, 0, aapl.FetchFailures)
	assert.NotNil(t, aapl.LastFetchedAt, "success sets last_fetched_at")

	msft, _ := repo.GetByTicker(ctx, "MSFT")
	assert.Equal(t, 1, msft.FetchFailures)

	goog, _ := repo.GetByTicker(ctx, "GOOG")
	assert.Equal(t, 0, goog.FetchFailures)
}

func TestWatchlistRepository_BulkUpdateFailures_EmptyMap(t *testing.T) {
	repo := setupWatchlistRepo(t)
	err := repo.BulkUpdateFailures(context.Background(), map[string]bool{})
	assert.NoError(t, err, "empty map is a no-op")
}
