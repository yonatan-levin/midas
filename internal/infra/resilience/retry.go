package resilience

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// BackoffStrategy defines different backoff strategies
type BackoffStrategy string

const (
	BackoffLinear      BackoffStrategy = "linear"
	BackoffExponential BackoffStrategy = "exponential"
	BackoffFixed       BackoffStrategy = "fixed"
)

// RetryConfig holds retry policy configuration
type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	Strategy    BackoffStrategy
	Jitter      bool // Add random jitter to prevent thundering herd
}

// DefaultRetryConfig returns sensible defaults for external API calls
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    5 * time.Second,
		Strategy:    BackoffExponential,
		Jitter:      true,
	}
}

// RetryPolicy implements retry logic with configurable backoff
type RetryPolicy struct {
	config RetryConfig
	logger *zap.Logger
}

// NewRetryPolicy creates a new retry policy
func NewRetryPolicy(config RetryConfig, logger *zap.Logger) ports.RetryPolicy {
	return &RetryPolicy{
		config: config,
		logger: logger.Named("retry_policy"),
	}
}

// Execute runs the function with retry logic
func (rp *RetryPolicy) Execute(ctx context.Context, fn func() error) error {
	var lastErr error

	for attempt := 1; attempt <= rp.config.MaxAttempts; attempt++ {
		// Check if context is cancelled before attempting
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Execute the function
		err := fn()
		if err == nil {
			if attempt > 1 {
				rp.logger.Info("Function succeeded after retry",
					zap.Int("attempt", attempt),
					zap.Int("total_attempts", rp.config.MaxAttempts))
			}
			return nil
		}

		lastErr = err
		rp.logger.Warn("Function failed, will retry",
			zap.Error(err),
			zap.Int("attempt", attempt),
			zap.Int("max_attempts", rp.config.MaxAttempts))

		// Don't wait after the last attempt
		if attempt == rp.config.MaxAttempts {
			break
		}

		// Calculate delay based on strategy
		delay := rp.calculateDelay(attempt)

		// Wait with context cancellation support
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
			// Continue to next attempt
		}
	}

	rp.logger.Error("Function failed after all retry attempts",
		zap.Error(lastErr),
		zap.Int("attempts", rp.config.MaxAttempts))

	return lastErr
}

// WithMaxAttempts returns a new retry policy with updated max attempts
func (rp *RetryPolicy) WithMaxAttempts(attempts int) ports.RetryPolicy {
	newConfig := rp.config
	newConfig.MaxAttempts = attempts
	return NewRetryPolicy(newConfig, rp.logger)
}

// WithBackoff returns a new retry policy with updated backoff strategy
func (rp *RetryPolicy) WithBackoff(strategy string) ports.RetryPolicy {
	newConfig := rp.config
	newConfig.Strategy = BackoffStrategy(strategy)
	return NewRetryPolicy(newConfig, rp.logger)
}

// calculateDelay calculates the delay for the given attempt
func (rp *RetryPolicy) calculateDelay(attempt int) time.Duration {
	var delay time.Duration

	switch rp.config.Strategy {
	case BackoffFixed:
		delay = rp.config.BaseDelay

	case BackoffLinear:
		delay = time.Duration(attempt) * rp.config.BaseDelay

	case BackoffExponential:
		// Exponential backoff: baseDelay * 2^(attempt-1)
		multiplier := math.Pow(2, float64(attempt-1))
		delay = time.Duration(float64(rp.config.BaseDelay) * multiplier)

	default:
		// Default to exponential
		multiplier := math.Pow(2, float64(attempt-1))
		delay = time.Duration(float64(rp.config.BaseDelay) * multiplier)
	}

	// Cap at max delay
	if delay > rp.config.MaxDelay {
		delay = rp.config.MaxDelay
	}

	// Add jitter to prevent thundering herd
	if rp.config.Jitter {
		// Add random jitter of ±25%
		jitterRange := float64(delay) * 0.25
		jitter := time.Duration(rand.Float64()*jitterRange*2 - jitterRange)
		delay += jitter

		// Ensure delay is not negative
		if delay < 0 {
			delay = rp.config.BaseDelay
		}
	}

	rp.logger.Debug("Calculated retry delay",
		zap.Duration("delay", delay),
		zap.Int("attempt", attempt),
		zap.String("strategy", string(rp.config.Strategy)))

	return delay
}

// IsRetryableError determines if an error should trigger a retry
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for specific error types that should be retried
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return true
	case errors.Is(err, context.Canceled):
		return false // Don't retry cancelled contexts
	default:
		// Add more specific retry logic here based on error types
		// For now, retry most errors except cancellation
		return true
	}
}

// RetryableFunc wraps a function to make it retryable only for certain errors
func RetryableFunc(fn func() error) func() error {
	return func() error {
		err := fn()
		if !IsRetryableError(err) {
			// Return a wrapped error to prevent retrying
			return &NonRetryableError{Err: err}
		}
		return err
	}
}

// NonRetryableError wraps an error to indicate it should not be retried
type NonRetryableError struct {
	Err error
}

func (e *NonRetryableError) Error() string {
	return e.Err.Error()
}

func (e *NonRetryableError) Unwrap() error {
	return e.Err
}
