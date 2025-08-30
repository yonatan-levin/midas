package datafetcher

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// BulkFetcher abstracts the bulk fetch capability for scheduling.
type BulkFetcher interface {
	BulkFetch(ctx context.Context, requests []*entities.FetchRequest) ([]*entities.FetchResult, error)
}

// WatchlistProvider abstracts the watchlist service for fetching active tickers
type WatchlistProvider interface {
	GetActiveWatchlist(ctx context.Context) ([]string, error)
}

// FetchResultRecorder abstracts the watchlist service for recording fetch results
type FetchResultRecorder interface {
	RecordFetchResults(ctx context.Context, results map[string]error) error
}

// IngestionJob is a scheduler Job that runs a bulk SEC ingestion for watchlist tickers.
// It fetches the current active watchlist dynamically and records the results.
type IngestionJob struct {
	fetcher        BulkFetcher
	watchlist      WatchlistProvider
	resultRecorder FetchResultRecorder
	logger         *zap.Logger
}

// NewIngestionJob creates a new ingestion job with watchlist-based ticker management
func NewIngestionJob(fetcher BulkFetcher, watchlist WatchlistProvider, resultRecorder FetchResultRecorder, logger *zap.Logger) *IngestionJob {
	return &IngestionJob{
		fetcher:        fetcher,
		watchlist:      watchlist,
		resultRecorder: resultRecorder,
		logger:         logger.Named("ingestion-job"),
	}
}

func (j *IngestionJob) Name() string { return "nightly_ingestion" }

func (j *IngestionJob) Run(ctx context.Context) error {
	j.logger.Info("starting nightly ingestion job")

	// Get current active watchlist
	tickers, err := j.watchlist.GetActiveWatchlist(ctx)
	if err != nil {
		j.logger.Error("failed to get active watchlist", zap.Error(err))
		return fmt.Errorf("failed to get active watchlist: %w", err)
	}

	if len(tickers) == 0 {
		j.logger.Info("no active tickers in watchlist, skipping ingestion")
		return nil
	}

	j.logger.Info("fetching data for watchlist tickers",
		zap.Int("ticker_count", len(tickers)),
		zap.Strings("tickers", tickers))

	// Create fetch requests
	requests := make([]*entities.FetchRequest, 0, len(tickers))
	for _, ticker := range tickers {
		requests = append(requests, &entities.FetchRequest{
			Ticker:          ticker,
			ValidationLevel: entities.ValidationNone,
		})
	}

	// Perform bulk fetch
	results, err := j.fetcher.BulkFetch(ctx, requests)
	if err != nil {
		j.logger.Error("bulk fetch failed", zap.Error(err))
		return fmt.Errorf("bulk fetch failed: %w", err)
	}

	// Process results and record success/failure per ticker
	fetchResults := make(map[string]error, len(tickers))
	for i, result := range results {
		ticker := requests[i].Ticker
		if !result.Success || len(result.Errors) > 0 {
			// Use first error if available, otherwise create generic error
			if len(result.Errors) > 0 {
				fetchResults[ticker] = fmt.Errorf("fetch failed: %s", result.Errors[0].Message)
			} else {
				fetchResults[ticker] = fmt.Errorf("fetch failed for unknown reason")
			}
		} else {
			fetchResults[ticker] = nil // success
		}
	}

	// Record results in watchlist for failure tracking
	if err := j.resultRecorder.RecordFetchResults(ctx, fetchResults); err != nil {
		j.logger.Warn("failed to record fetch results", zap.Error(err))
		// Don't fail the job for recording issues
	}

	successCount := 0
	for _, err := range fetchResults {
		if err == nil {
			successCount++
		}
	}

	j.logger.Info("nightly ingestion job completed",
		zap.Int("total_tickers", len(tickers)),
		zap.Int("successful", successCount),
		zap.Int("failed", len(tickers)-successCount),
	)

	return nil
}
