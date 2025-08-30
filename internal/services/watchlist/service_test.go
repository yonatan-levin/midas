package watchlist

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// MockWatchlistRepository is a mock implementation of WatchlistRepository for testing
type MockWatchlistRepository struct {
	mock.Mock
}

func (m *MockWatchlistRepository) GetActiveWatchlist(ctx context.Context, filter *entities.WatchlistFilter) ([]*entities.WatchlistEntry, error) {
	args := m.Called(ctx, filter)
	return args.Get(0).([]*entities.WatchlistEntry), args.Error(1)
}

func (m *MockWatchlistRepository) GetAll(ctx context.Context, filter *entities.WatchlistFilter) ([]*entities.WatchlistEntry, error) {
	args := m.Called(ctx, filter)
	return args.Get(0).([]*entities.WatchlistEntry), args.Error(1)
}

func (m *MockWatchlistRepository) GetByTicker(ctx context.Context, ticker string) (*entities.WatchlistEntry, error) {
	args := m.Called(ctx, ticker)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.WatchlistEntry), args.Error(1)
}

func (m *MockWatchlistRepository) Add(ctx context.Context, entry *entities.WatchlistEntry) error {
	args := m.Called(ctx, entry)
	return args.Error(0)
}

func (m *MockWatchlistRepository) Update(ctx context.Context, ticker string, updates *entities.UpdateWatchlistEntryRequest) error {
	args := m.Called(ctx, ticker, updates)
	return args.Error(0)
}

func (m *MockWatchlistRepository) Remove(ctx context.Context, ticker string) error {
	args := m.Called(ctx, ticker)
	return args.Error(0)
}

func (m *MockWatchlistRepository) RecordSuccess(ctx context.Context, ticker string, fetchedAt time.Time) error {
	args := m.Called(ctx, ticker, fetchedAt)
	return args.Error(0)
}

func (m *MockWatchlistRepository) RecordFailure(ctx context.Context, ticker string) error {
	args := m.Called(ctx, ticker)
	return args.Error(0)
}

func (m *MockWatchlistRepository) GetStats(ctx context.Context) (*entities.WatchlistStats, error) {
	args := m.Called(ctx)
	return args.Get(0).(*entities.WatchlistStats), args.Error(1)
}

func (m *MockWatchlistRepository) BulkUpdateFailures(ctx context.Context, failures map[string]bool) error {
	args := m.Called(ctx, failures)
	return args.Error(0)
}

func TestWatchlistService_GetActiveWatchlist(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)
	mockRepo := new(MockWatchlistRepository)
	service := NewService(mockRepo, logger)

	t.Run("returns active tickers in priority order", func(t *testing.T) {
		expectedEntries := []*entities.WatchlistEntry{
			{Ticker: "AAPL", IsActive: true, Priority: 1, FetchFailures: 0, MaxFailures: 5},
			{Ticker: "GOOGL", IsActive: true, Priority: 2, FetchFailures: 0, MaxFailures: 5},
			{Ticker: "MSFT", IsActive: true, Priority: 3, FetchFailures: 0, MaxFailures: 5},
		}

		mockRepo.On("GetActiveWatchlist", ctx, mock.MatchedBy(func(filter *entities.WatchlistFilter) bool {
			return filter != nil && filter.IsActive != nil && *filter.IsActive == true
		})).Return(expectedEntries, nil)

		tickers, err := service.GetActiveWatchlist(ctx)

		require.NoError(t, err)
		assert.Equal(t, []string{"AAPL", "GOOGL", "MSFT"}, tickers)
		mockRepo.AssertExpectations(t)
	})

	t.Run("excludes entries that cannot retry", func(t *testing.T) {
		expectedEntries := []*entities.WatchlistEntry{
			{Ticker: "AAPL", IsActive: true, Priority: 1, FetchFailures: 0, MaxFailures: 5},
			{Ticker: "FAIL", IsActive: true, Priority: 1, FetchFailures: 5, MaxFailures: 5}, // Should be excluded (failures >= max)
			{Ticker: "GOOGL", IsActive: true, Priority: 2, FetchFailures: 2, MaxFailures: 5},
		}

		// Reset mock for this test
		mockRepo.ExpectedCalls = nil
		mockRepo.On("GetActiveWatchlist", ctx, mock.AnythingOfType("*entities.WatchlistFilter")).Return(expectedEntries, nil)

		tickers, err := service.GetActiveWatchlist(ctx)

		require.NoError(t, err)
		assert.Equal(t, []string{"AAPL", "GOOGL"}, tickers) // FAIL excluded
		mockRepo.AssertExpectations(t)
	})

	t.Run("returns empty slice when no active entries", func(t *testing.T) {
		// Reset mock for this test
		mockRepo.ExpectedCalls = nil
		mockRepo.On("GetActiveWatchlist", ctx, mock.AnythingOfType("*entities.WatchlistFilter")).Return([]*entities.WatchlistEntry{}, nil)

		tickers, err := service.GetActiveWatchlist(ctx)

		require.NoError(t, err)
		assert.Empty(t, tickers)
		mockRepo.AssertExpectations(t)
	})
}

func TestWatchlistService_AddTicker(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)

	t.Run("successfully adds new ticker", func(t *testing.T) {
		mockRepo := new(MockWatchlistRepository)
		service := NewService(mockRepo, logger)

		request := &entities.CreateWatchlistEntryRequest{
			Ticker:      "AAPL",
			Priority:    entities.HighPriority,
			AddedReason: "test",
			MaxFailures: 3,
		}

		// Check ticker doesn't exist
		mockRepo.On("GetByTicker", ctx, "AAPL").Return(nil, nil)

		// Add the ticker
		mockRepo.On("Add", ctx, mock.MatchedBy(func(entry *entities.WatchlistEntry) bool {
			return entry.Ticker == "AAPL" &&
				entry.IsActive == true &&
				entry.Priority == int(entities.HighPriority) &&
				entry.AddedReason == "test" &&
				entry.MaxFailures == 3
		})).Return(nil)

		err := service.AddTicker(ctx, request)

		require.NoError(t, err)
		mockRepo.AssertExpectations(t)
	})

	t.Run("rejects duplicate ticker", func(t *testing.T) {
		mockRepo := new(MockWatchlistRepository)
		service := NewService(mockRepo, logger)

		request := &entities.CreateWatchlistEntryRequest{Ticker: "AAPL"}

		existingEntry := &entities.WatchlistEntry{Ticker: "AAPL"}
		mockRepo.On("GetByTicker", ctx, "AAPL").Return(existingEntry, nil)

		err := service.AddTicker(ctx, request)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
		mockRepo.AssertExpectations(t)
	})

	t.Run("rejects invalid ticker format", func(t *testing.T) {
		testCases := []struct {
			name   string
			ticker string
		}{
			{"empty ticker", ""},
			{"too long", "VERYLONGTICKER"},
			{"invalid characters", "AA@PL"},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				mockRepo := new(MockWatchlistRepository)
				service := NewService(mockRepo, logger)

				request := &entities.CreateWatchlistEntryRequest{Ticker: tc.ticker}
				err := service.AddTicker(ctx, request)
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "invalid ticker")
			})
		}
	})

	t.Run("applies defaults for optional fields", func(t *testing.T) {
		mockRepo := new(MockWatchlistRepository)
		service := NewService(mockRepo, logger)

		request := &entities.CreateWatchlistEntryRequest{Ticker: "AAPL"}

		mockRepo.On("GetByTicker", ctx, "AAPL").Return(nil, nil)
		mockRepo.On("Add", ctx, mock.MatchedBy(func(entry *entities.WatchlistEntry) bool {
			return entry.Priority == int(entities.MediumPriority) &&
				entry.MaxFailures == 5 &&
				entry.AddedReason == "manual"
		})).Return(nil)

		err := service.AddTicker(ctx, request)

		require.NoError(t, err)
		mockRepo.AssertExpectations(t)
	})
}

func TestWatchlistService_RecordFetchResults(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)

	t.Run("records mixed success and failure results", func(t *testing.T) {
		mockRepo := new(MockWatchlistRepository)
		service := NewService(mockRepo, logger)
		results := map[string]error{
			"AAPL":  nil,            // success
			"GOOGL": assert.AnError, // failure
			"MSFT":  nil,            // success
		}

		// Expect individual success recordings
		mockRepo.On("RecordSuccess", ctx, "AAPL", mock.AnythingOfType("time.Time")).Return(nil)
		mockRepo.On("RecordSuccess", ctx, "MSFT", mock.AnythingOfType("time.Time")).Return(nil)

		// Expect individual failure recording
		mockRepo.On("RecordFailure", ctx, "GOOGL").Return(nil)

		// Expect bulk update
		mockRepo.On("BulkUpdateFailures", ctx, mock.MatchedBy(func(failures map[string]bool) bool {
			return failures["AAPL"] == true && failures["GOOGL"] == false && failures["MSFT"] == true
		})).Return(nil)

		err := service.RecordFetchResults(ctx, results)

		require.NoError(t, err)
		mockRepo.AssertExpectations(t)
	})

	t.Run("handles empty results", func(t *testing.T) {
		mockRepo := new(MockWatchlistRepository)
		service := NewService(mockRepo, logger)

		err := service.RecordFetchResults(ctx, map[string]error{})
		require.NoError(t, err)
		// No mock expectations needed - should return early
	})
}

func TestWatchlistService_GetStats(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)
	mockRepo := new(MockWatchlistRepository)
	service := NewService(mockRepo, logger)

	expectedStats := &entities.WatchlistStats{
		TotalEntries:    10,
		ActiveEntries:   8,
		InactiveEntries: 2,
		HighPriority:    3,
		MediumPriority:  5,
		LowPriority:     2,
		RecentFailures:  1,
	}

	mockRepo.On("GetStats", ctx).Return(expectedStats, nil)

	stats, err := service.GetStats(ctx)

	require.NoError(t, err)
	assert.Equal(t, expectedStats, stats)
	mockRepo.AssertExpectations(t)
}

func TestWatchlistService_EnableDisableTicker(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)

	t.Run("enable ticker", func(t *testing.T) {
		mockRepo := new(MockWatchlistRepository)
		service := NewService(mockRepo, logger)
		existingEntry := &entities.WatchlistEntry{Ticker: "AAPL", IsActive: false}
		mockRepo.On("GetByTicker", ctx, "AAPL").Return(existingEntry, nil)
		mockRepo.On("Update", ctx, "AAPL", mock.MatchedBy(func(updates *entities.UpdateWatchlistEntryRequest) bool {
			return updates.IsActive != nil && *updates.IsActive == true
		})).Return(nil)

		err := service.EnableTicker(ctx, "AAPL")

		require.NoError(t, err)
		mockRepo.AssertExpectations(t)
	})

	t.Run("disable ticker", func(t *testing.T) {
		mockRepo := new(MockWatchlistRepository)
		service := NewService(mockRepo, logger)

		existingEntry := &entities.WatchlistEntry{Ticker: "AAPL", IsActive: true}
		mockRepo.On("GetByTicker", ctx, "AAPL").Return(existingEntry, nil)
		mockRepo.On("Update", ctx, "AAPL", mock.MatchedBy(func(updates *entities.UpdateWatchlistEntryRequest) bool {
			return updates.IsActive != nil && *updates.IsActive == false
		})).Return(nil)

		err := service.DisableTicker(ctx, "AAPL")

		require.NoError(t, err)
		mockRepo.AssertExpectations(t)
	})
}
