package resilience

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// State represents the circuit breaker state
type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "CLOSED"
	case StateOpen:
		return "OPEN"
	case StateHalfOpen:
		return "HALF_OPEN"
	default:
		return "UNKNOWN"
	}
}

// CircuitBreakerConfig holds circuit breaker configuration
type CircuitBreakerConfig struct {
	Name             string
	MaxFailures      int           // Number of failures before opening
	FailureTimeout   time.Duration // How long to wait before moving to half-open
	SuccessThreshold int           // Number of successes needed to close from half-open
	RequestTimeout   time.Duration // Maximum time for individual requests
	ResetTimeout     time.Duration // Time to reset failure count
}

// DefaultCircuitBreakerConfig returns sensible defaults for external API calls
func DefaultCircuitBreakerConfig(name string) CircuitBreakerConfig {
	return CircuitBreakerConfig{
		Name:             name,
		MaxFailures:      5,
		FailureTimeout:   30 * time.Second,
		SuccessThreshold: 3,
		RequestTimeout:   10 * time.Second,
		ResetTimeout:     60 * time.Second,
	}
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	config          CircuitBreakerConfig
	state           State
	failures        int
	successes       int
	lastFailureTime time.Time
	lastSuccessTime time.Time
	mutex           sync.RWMutex
	logger          *zap.Logger
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(config CircuitBreakerConfig, logger *zap.Logger) ports.CircuitBreaker {
	return &CircuitBreaker{
		config: config,
		state:  StateClosed,
		logger: logger.Named("circuit_breaker").With(zap.String("name", config.Name)),
	}
}

// Execute runs the function with circuit breaker protection
func (cb *CircuitBreaker) Execute(ctx context.Context, fn func() error) error {
	if !cb.allowRequest() {
		cb.logger.Warn("Circuit breaker is open, rejecting request",
			zap.String("state", cb.State()),
			zap.Int("failures", cb.failures))
		return errors.New("circuit breaker is open")
	}

	// Create timeout context for the request
	timeoutCtx, cancel := context.WithTimeout(ctx, cb.config.RequestTimeout)
	defer cancel()

	// Execute function with timeout
	err := cb.executeWithTimeout(timeoutCtx, fn)

	if err != nil {
		cb.recordFailure()
		return err
	}

	cb.recordSuccess()
	return nil
}

// State returns the current circuit breaker state
func (cb *CircuitBreaker) State() string {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()
	return cb.state.String()
}

// Reset manually resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.state = StateClosed
	cb.failures = 0
	cb.successes = 0
	cb.lastFailureTime = time.Time{}
	cb.lastSuccessTime = time.Now()

	cb.logger.Info("Circuit breaker manually reset")
}

// allowRequest determines if a request should be allowed
func (cb *CircuitBreaker) allowRequest() bool {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	switch cb.state {
	case StateClosed:
		return true
	case StateOpen:
		// Check if we should move to half-open
		if time.Since(cb.lastFailureTime) > cb.config.FailureTimeout {
			cb.state = StateHalfOpen
			cb.successes = 0
			cb.logger.Info("Circuit breaker moving to half-open state")
			return true
		}
		return false
	case StateHalfOpen:
		return true
	default:
		return false
	}
}

// executeWithTimeout runs the function with a timeout
func (cb *CircuitBreaker) executeWithTimeout(ctx context.Context, fn func() error) error {
	// Channel to receive the result
	resultChan := make(chan error, 1)

	// Run the function in a goroutine
	go func() {
		defer func() {
			if r := recover(); r != nil {
				cb.logger.Error("Function panicked in circuit breaker",
					zap.Any("panic", r))
				resultChan <- errors.New("function panicked")
			}
		}()
		resultChan <- fn()
	}()

	// Wait for either completion or timeout
	select {
	case err := <-resultChan:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// recordFailure records a failure and potentially opens the circuit
func (cb *CircuitBreaker) recordFailure() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.failures++
	cb.lastFailureTime = time.Now()

	cb.logger.Debug("Circuit breaker recorded failure",
		zap.Int("failures", cb.failures),
		zap.Int("max_failures", cb.config.MaxFailures))

	switch cb.state {
	case StateClosed:
		if cb.failures >= cb.config.MaxFailures {
			cb.state = StateOpen
			cb.logger.Warn("Circuit breaker opened due to failures",
				zap.Int("failures", cb.failures))
		}
	case StateHalfOpen:
		cb.state = StateOpen
		cb.logger.Warn("Circuit breaker re-opened from half-open state")
	}
}

// recordSuccess records a success and potentially closes the circuit
func (cb *CircuitBreaker) recordSuccess() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.successes++
	cb.lastSuccessTime = time.Now()

	// Reset failure count on success after reset timeout
	if time.Since(cb.lastFailureTime) > cb.config.ResetTimeout {
		cb.failures = 0
	}

	cb.logger.Debug("Circuit breaker recorded success",
		zap.Int("successes", cb.successes),
		zap.String("state", cb.state.String()))

	switch cb.state {
	case StateHalfOpen:
		if cb.successes >= cb.config.SuccessThreshold {
			cb.state = StateClosed
			cb.failures = 0
			cb.logger.Info("Circuit breaker closed after successful requests")
		}
	}
}
