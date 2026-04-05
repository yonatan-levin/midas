package datafetcher

import (
	"context"
	"errors"
	"testing"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// --- Mock implementations for scheduler job tests ---

// mockBulkFetcher implements BulkFetcher for testing the ingestion job.
type mockBulkFetcher struct {
	results []*entities.FetchResult
	err     error
}

func (m *mockBulkFetcher) BulkFetch(_ context.Context, requests []*entities.FetchRequest) ([]*entities.FetchResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

// mockWatchlistProvider implements WatchlistProvider for testing.
type mockWatchlistProvider struct {
	tickers []string
	err     error
}

func (m *mockWatchlistProvider) GetActiveWatchlist(_ context.Context) ([]string, error) {
	return m.tickers, m.err
}

// mockFetchResultRecorder implements FetchResultRecorder for testing.
type mockFetchResultRecorder struct {
	recorded map[string]error
	err      error
}

func (m *mockFetchResultRecorder) RecordFetchResults(_ context.Context, results map[string]error) error {
	m.recorded = results
	return m.err
}

// TestNewIngestionJob verifies that the constructor wires all dependencies correctly.
func TestNewIngestionJob(t *testing.T) {
	fetcher := &mockBulkFetcher{}
	watchlist := &mockWatchlistProvider{}
	recorder := &mockFetchResultRecorder{}
	logger := zap.NewNop()

	job := NewIngestionJob(fetcher, watchlist, recorder, logger)

	require.NotNil(t, job)
	assert.Equal(t, fetcher, job.fetcher)
	assert.Equal(t, watchlist, job.watchlist)
	assert.Equal(t, recorder, job.resultRecorder)
	assert.NotNil(t, job.logger)
}

// TestIngestionJob_Name verifies the job returns the correct name identifier.
func TestIngestionJob_Name(t *testing.T) {
	job := NewIngestionJob(
		&mockBulkFetcher{},
		&mockWatchlistProvider{},
		&mockFetchResultRecorder{},
		zap.NewNop(),
	)

	assert.Equal(t, "nightly_ingestion", job.Name())
}

// TestIngestionJob_Run exercises all execution paths of the Run method.
func TestIngestionJob_Run(t *testing.T) {
	tests := []struct {
		name            string
		watchlistTicker []string
		watchlistErr    error
		fetchResults    []*entities.FetchResult
		fetchErr        error
		recorderErr     error
		expectError     bool
		expectErrorMsg  string
		expectRecorded  map[string]bool // ticker -> success(true)/failure(false)
	}{
		{
			name:            "successful_run_all_tickers_pass",
			watchlistTicker: []string{"AAPL", "MSFT"},
			fetchResults: []*entities.FetchResult{
				{Ticker: "AAPL", Success: true},
				{Ticker: "MSFT", Success: true},
			},
			expectError:    false,
			expectRecorded: map[string]bool{"AAPL": true, "MSFT": true},
		},
		{
			name:            "empty_watchlist_skips_ingestion",
			watchlistTicker: []string{},
			expectError:     false,
			expectRecorded:  nil, // recorder should not be called
		},
		{
			name:           "watchlist_error_propagates",
			watchlistErr:   errors.New("database connection failed"),
			expectError:    true,
			expectErrorMsg: "failed to get active watchlist",
		},
		{
			name:            "bulk_fetch_error_propagates",
			watchlistTicker: []string{"AAPL"},
			fetchErr:        errors.New("bulk fetch timeout"),
			expectError:     true,
			expectErrorMsg:  "bulk fetch failed",
		},
		{
			name:            "partial_failures_recorded",
			watchlistTicker: []string{"AAPL", "BAD_TICKER"},
			fetchResults: []*entities.FetchResult{
				{Ticker: "AAPL", Success: true},
				{Ticker: "BAD_TICKER", Success: false, Errors: []entities.FetchError{
					{Source: entities.SECSource, Type: "fetch_error", Message: "ticker not found"},
				}},
			},
			expectError:    false,
			expectRecorded: map[string]bool{"AAPL": true, "BAD_TICKER": false},
		},
		{
			name:            "failure_without_error_details",
			watchlistTicker: []string{"UNKNOWN"},
			fetchResults: []*entities.FetchResult{
				{Ticker: "UNKNOWN", Success: false, Errors: []entities.FetchError{}},
			},
			expectError:    false,
			expectRecorded: map[string]bool{"UNKNOWN": false},
		},
		{
			name:            "recorder_error_does_not_fail_job",
			watchlistTicker: []string{"AAPL"},
			fetchResults: []*entities.FetchResult{
				{Ticker: "AAPL", Success: true},
			},
			recorderErr: errors.New("failed to write results"),
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetcher := &mockBulkFetcher{results: tt.fetchResults, err: tt.fetchErr}
			watchlist := &mockWatchlistProvider{tickers: tt.watchlistTicker, err: tt.watchlistErr}
			recorder := &mockFetchResultRecorder{err: tt.recorderErr}
			logger := zap.NewNop()

			job := NewIngestionJob(fetcher, watchlist, recorder, logger)

			err := job.Run(context.Background())

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectErrorMsg)
				return
			}

			require.NoError(t, err)

			// Verify recorded results match expectations
			if tt.expectRecorded != nil {
				require.NotNil(t, recorder.recorded, "recorder should have been called")
				for ticker, expectSuccess := range tt.expectRecorded {
					recordedErr, exists := recorder.recorded[ticker]
					require.True(t, exists, "ticker %s should be in recorded results", ticker)
					if expectSuccess {
						assert.Nil(t, recordedErr, "ticker %s should have nil error (success)", ticker)
					} else {
						assert.NotNil(t, recordedErr, "ticker %s should have non-nil error (failure)", ticker)
					}
				}
			}
		})
	}
}
