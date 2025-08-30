package watchlist

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// Service provides business logic for managing the scheduler's watchlist
type Service struct {
	repo   ports.WatchlistRepository
	logger *zap.Logger
}

// NewService creates a new watchlist service
func NewService(repo ports.WatchlistRepository, logger *zap.Logger) *Service {
	return &Service{
		repo:   repo,
		logger: logger.Named("watchlist-service"),
	}
}

// GetActiveWatchlist retrieves all active tickers for scheduling, prioritized by priority level
func (s *Service) GetActiveWatchlist(ctx context.Context) ([]string, error) {
	filter := &entities.WatchlistFilter{
		IsActive: &[]bool{true}[0], // Only active entries
	}

	entries, err := s.repo.GetActiveWatchlist(ctx, filter)
	if err != nil {
		s.logger.Error("failed to get active watchlist", zap.Error(err))
		return nil, fmt.Errorf("failed to get active watchlist: %w", err)
	}

	// Extract tickers in priority order (already sorted by repository)
	tickers := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.CanRetry() {
			tickers = append(tickers, entry.Ticker)
		}
	}

	s.logger.Info("retrieved active watchlist",
		zap.Int("total_entries", len(entries)),
		zap.Int("retryable_entries", len(tickers)),
	)

	return tickers, nil
}

// GetWatchlistWithDetails retrieves watchlist entries with full details
func (s *Service) GetWatchlistWithDetails(ctx context.Context, filter *entities.WatchlistFilter) ([]*entities.WatchlistEntry, error) {
	entries, err := s.repo.GetAll(ctx, filter)
	if err != nil {
		s.logger.Error("failed to get watchlist with details", zap.Error(err))
		return nil, fmt.Errorf("failed to get watchlist details: %w", err)
	}

	return entries, nil
}

// AddTicker adds a new ticker to the watchlist with validation
func (s *Service) AddTicker(ctx context.Context, request *entities.CreateWatchlistEntryRequest) error {
	// Validate ticker format
	if err := s.validateTicker(request.Ticker); err != nil {
		return fmt.Errorf("invalid ticker: %w", err)
	}

	// Check if ticker already exists
	existing, err := s.repo.GetByTicker(ctx, request.Ticker)
	if err != nil {
		s.logger.Error("failed to check existing ticker", zap.String("ticker", request.Ticker), zap.Error(err))
		return fmt.Errorf("failed to check existing ticker: %w", err)
	}

	if existing != nil {
		return fmt.Errorf("ticker %s already exists in watchlist", request.Ticker)
	}

	// Create new entry with defaults
	entry := &entities.WatchlistEntry{
		Ticker:      strings.ToUpper(request.Ticker),
		IsActive:    true,
		Priority:    int(request.Priority),
		AddedReason: request.AddedReason,
		MaxFailures: request.MaxFailures,
	}

	// Apply defaults
	if entry.Priority == 0 {
		entry.Priority = int(entities.MediumPriority)
	}
	if entry.MaxFailures == 0 {
		entry.MaxFailures = 5
	}
	if entry.AddedReason == "" {
		entry.AddedReason = "manual"
	}

	err = s.repo.Add(ctx, entry)
	if err != nil {
		s.logger.Error("failed to add ticker to watchlist",
			zap.String("ticker", request.Ticker),
			zap.Error(err))
		return fmt.Errorf("failed to add ticker to watchlist: %w", err)
	}

	s.logger.Info("added ticker to watchlist",
		zap.String("ticker", entry.Ticker),
		zap.Int("priority", entry.Priority),
		zap.String("reason", entry.AddedReason),
	)

	return nil
}

// UpdateTicker updates an existing watchlist entry
func (s *Service) UpdateTicker(ctx context.Context, ticker string, updates *entities.UpdateWatchlistEntryRequest) error {
	// Validate ticker exists
	existing, err := s.repo.GetByTicker(ctx, ticker)
	if err != nil {
		return fmt.Errorf("failed to check existing ticker: %w", err)
	}
	if existing == nil {
		return fmt.Errorf("ticker %s not found in watchlist", ticker)
	}

	err = s.repo.Update(ctx, ticker, updates)
	if err != nil {
		s.logger.Error("failed to update ticker in watchlist",
			zap.String("ticker", ticker),
			zap.Error(err))
		return fmt.Errorf("failed to update ticker: %w", err)
	}

	s.logger.Info("updated ticker in watchlist",
		zap.String("ticker", ticker),
		zap.Any("updates", updates),
	)

	return nil
}

// RemoveTicker removes a ticker from the watchlist
func (s *Service) RemoveTicker(ctx context.Context, ticker string) error {
	err := s.repo.Remove(ctx, ticker)
	if err != nil {
		s.logger.Error("failed to remove ticker from watchlist",
			zap.String("ticker", ticker),
			zap.Error(err))
		return fmt.Errorf("failed to remove ticker: %w", err)
	}

	s.logger.Info("removed ticker from watchlist", zap.String("ticker", ticker))
	return nil
}

// RecordFetchResults records the results of a batch fetch operation
func (s *Service) RecordFetchResults(ctx context.Context, results map[string]error) error {
	if len(results) == 0 {
		return nil
	}

	failures := make(map[string]bool, len(results))
	fetchTime := time.Now()

	successCount := 0
	failureCount := 0

	for ticker, err := range results {
		if err == nil {
			failures[ticker] = true // success
			successCount++

			// Record individual success for detailed tracking
			if recordErr := s.repo.RecordSuccess(ctx, ticker, fetchTime); recordErr != nil {
				s.logger.Warn("failed to record individual success",
					zap.String("ticker", ticker),
					zap.Error(recordErr))
			}
		} else {
			failures[ticker] = false // failure
			failureCount++

			// Record individual failure
			if recordErr := s.repo.RecordFailure(ctx, ticker); recordErr != nil {
				s.logger.Warn("failed to record individual failure",
					zap.String("ticker", ticker),
					zap.Error(recordErr))
			}
		}
	}

	// Use bulk update as backup/verification
	if err := s.repo.BulkUpdateFailures(ctx, failures); err != nil {
		s.logger.Error("failed to bulk update fetch results", zap.Error(err))
		return fmt.Errorf("failed to update fetch results: %w", err)
	}

	s.logger.Info("recorded fetch results",
		zap.Int("total_tickers", len(results)),
		zap.Int("successful", successCount),
		zap.Int("failed", failureCount),
	)

	return nil
}

// GetStats retrieves statistics about the watchlist
func (s *Service) GetStats(ctx context.Context) (*entities.WatchlistStats, error) {
	stats, err := s.repo.GetStats(ctx)
	if err != nil {
		s.logger.Error("failed to get watchlist stats", zap.Error(err))
		return nil, fmt.Errorf("failed to get watchlist stats: %w", err)
	}

	return stats, nil
}

// EnableTicker enables a disabled ticker in the watchlist
func (s *Service) EnableTicker(ctx context.Context, ticker string) error {
	isActive := true
	updates := &entities.UpdateWatchlistEntryRequest{
		IsActive: &isActive,
	}

	return s.UpdateTicker(ctx, ticker, updates)
}

// DisableTicker disables a ticker in the watchlist without removing it
func (s *Service) DisableTicker(ctx context.Context, ticker string) error {
	isActive := false
	updates := &entities.UpdateWatchlistEntryRequest{
		IsActive: &isActive,
	}

	return s.UpdateTicker(ctx, ticker, updates)
}

// ResetFailures resets the failure count for a ticker (useful for manual intervention)
func (s *Service) ResetFailures(ctx context.Context, ticker string) error {
	// Get current entry to preserve other fields
	entry, err := s.repo.GetByTicker(ctx, ticker)
	if err != nil {
		return fmt.Errorf("failed to get ticker: %w", err)
	}
	if entry == nil {
		return fmt.Errorf("ticker %s not found", ticker)
	}

	// Record a success with current time to reset failures
	return s.repo.RecordSuccess(ctx, ticker, time.Now())
}

// validateTicker validates ticker symbol format
func (s *Service) validateTicker(ticker string) error {
	if ticker == "" {
		return fmt.Errorf("ticker cannot be empty")
	}

	if len(ticker) > 10 {
		return fmt.Errorf("ticker too long (max 10 characters)")
	}

	// Basic validation - only alphanumeric characters and dots
	for _, char := range ticker {
		if (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '.' {
			return fmt.Errorf("ticker contains invalid characters (only alphanumeric and dots allowed)")
		}
	}

	return nil
}
