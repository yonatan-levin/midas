package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// Errors
var (
	ErrCacheKeyNotFound = errors.New("cache key not found")
	ErrLimitExceeded    = errors.New("rate limit exceeded")
	ErrInvalidConfig    = errors.New("invalid rate limit configuration")
)

// LimitType defines the type of rate limiting
type LimitType string

const (
	LimitTypeAPIKey   LimitType = "api_key"
	LimitTypeIP       LimitType = "ip"
	LimitTypeEndpoint LimitType = "endpoint"
	LimitTypeGlobal   LimitType = "global"
)

// LimitConfig defines a rate limiting configuration
type LimitConfig struct {
	Type         LimitType     `json:"type"`
	Identifier   string        `json:"identifier"`
	MaxRequests  int           `json:"max_requests"`
	Window       time.Duration `json:"window"`
	BurstAllowed bool          `json:"burst_allowed"`
	BurstSize    int           `json:"burst_size,omitempty"`
	Description  string        `json:"description,omitempty"`
}

// RateLimitRequest represents a request to check rate limits
type RateLimitRequest struct {
	Identifier string    `json:"identifier"`
	Type       LimitType `json:"type"`
	IPAddress  string    `json:"ip_address,omitempty"`
	Endpoint   string    `json:"endpoint,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
}

// RateLimitResult represents the result of a rate limit check
type RateLimitResult struct {
	Allowed    bool          `json:"allowed"`
	Remaining  int           `json:"remaining"`
	ResetTime  time.Time     `json:"reset_time"`
	RetryAfter time.Duration `json:"retry_after,omitempty"`
	LimitType  LimitType     `json:"limit_type"`
	Identifier string        `json:"identifier"`
}

// UsageInfo represents current usage statistics
type UsageInfo struct {
	RequestsUsed int           `json:"requests_used"`
	MaxRequests  int           `json:"max_requests"`
	Remaining    int           `json:"remaining"`
	ResetTime    time.Time     `json:"reset_time"`
	Window       time.Duration `json:"window"`
}

// CacheStore defines the interface for storing rate limit data
type CacheStore interface {
	Increment(ctx context.Context, key string, window time.Duration) (int, time.Time, error)
	Get(ctx context.Context, key string) (int, time.Time, error)
	Set(ctx context.Context, key string, value int, window time.Duration) error
	Delete(ctx context.Context, key string) error
}

// RateLimiter provides rate limiting functionality
type RateLimiter struct {
	cache  CacheStore
	logger *zap.Logger
	limits map[string]LimitConfig // Cache of configured limits
}

// NewRateLimiter creates a new rate limiter instance
func NewRateLimiter(cache CacheStore, logger *zap.Logger) *RateLimiter {
	return &RateLimiter{
		cache:  cache,
		logger: logger,
		limits: make(map[string]LimitConfig),
	}
}

// SetLimit configures a rate limit for a given identifier and type
func (rl *RateLimiter) SetLimit(ctx context.Context, config LimitConfig) error {
	// Validate configuration
	if err := rl.validateConfig(config); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}

	// Store the limit configuration
	key := rl.getLimitKey(config.Type, config.Identifier)
	rl.limits[key] = config

	rl.logger.Info("Rate limit configured",
		zap.String("type", string(config.Type)),
		zap.String("identifier", config.Identifier),
		zap.Int("max_requests", config.MaxRequests),
		zap.Duration("window", config.Window),
	)

	return nil
}

// AllowRequest checks if a request should be allowed based on rate limits
func (rl *RateLimiter) AllowRequest(ctx context.Context, req RateLimitRequest) (*RateLimitResult, error) {
	// Check all applicable limits for this request
	limitsToCheck := rl.getApplicableLimits(req)

	for _, limit := range limitsToCheck {
		result, err := rl.checkLimit(ctx, limit, req)
		if err != nil {
			return nil, fmt.Errorf("failed to check rate limit: %w", err)
		}

		// If any limit is exceeded, deny the request
		if !result.Allowed {
			rl.logger.Warn("Rate limit exceeded",
				zap.String("type", string(result.LimitType)),
				zap.String("identifier", result.Identifier),
				zap.Int("remaining", result.Remaining),
			)
			return result, nil
		}
	}

	// If we get here, all limits passed - increment counters
	var finalResult *RateLimitResult
	for _, limit := range limitsToCheck {
		result, err := rl.incrementLimit(ctx, limit, req)
		if err != nil {
			return nil, fmt.Errorf("failed to increment rate limit: %w", err)
		}

		// Use the most restrictive result
		if finalResult == nil || result.Remaining < finalResult.Remaining {
			finalResult = result
		}
	}

	if finalResult == nil {
		// No limits configured - allow by default
		finalResult = &RateLimitResult{
			Allowed:    true,
			Remaining:  999,
			ResetTime:  time.Now().Add(time.Hour),
			LimitType:  req.Type,
			Identifier: req.Identifier,
		}
	}

	return finalResult, nil
}

// GetUsage returns current usage statistics for an identifier
func (rl *RateLimiter) GetUsage(ctx context.Context, identifier string, limitType LimitType) (*UsageInfo, error) {
	limitKey := rl.getLimitKey(limitType, identifier)
	limit, exists := rl.limits[limitKey]
	if !exists {
		return nil, fmt.Errorf("no rate limit configured for %s:%s", limitType, identifier)
	}

	cacheKey := rl.getCacheKey(limitType, identifier)
	used, resetTime, err := rl.cache.Get(ctx, cacheKey)
	if err != nil {
		if errors.Is(err, ErrCacheKeyNotFound) {
			used = 0
			resetTime = time.Now().Add(limit.Window)
		} else {
			return nil, fmt.Errorf("failed to get usage from cache: %w", err)
		}
	}

	remaining := limit.MaxRequests - used
	if remaining < 0 {
		remaining = 0
	}

	return &UsageInfo{
		RequestsUsed: used,
		MaxRequests:  limit.MaxRequests,
		Remaining:    remaining,
		ResetTime:    resetTime,
		Window:       limit.Window,
	}, nil
}

// RemoveLimit removes a rate limit configuration
func (rl *RateLimiter) RemoveLimit(ctx context.Context, limitType LimitType, identifier string) error {
	limitKey := rl.getLimitKey(limitType, identifier)
	delete(rl.limits, limitKey)

	// Also clean up the cache entry
	cacheKey := rl.getCacheKey(limitType, identifier)
	err := rl.cache.Delete(ctx, cacheKey)
	if err != nil && !errors.Is(err, ErrCacheKeyNotFound) {
		rl.logger.Warn("Failed to clean up cache entry", zap.Error(err))
	}

	rl.logger.Info("Rate limit removed",
		zap.String("type", string(limitType)),
		zap.String("identifier", identifier),
	)

	return nil
}

// GetLimits returns all configured rate limits
func (rl *RateLimiter) GetLimits() map[string]LimitConfig {
	// Return a copy to prevent external modification
	result := make(map[string]LimitConfig)
	for k, v := range rl.limits {
		result[k] = v
	}
	return result
}

// Helper methods

// validateConfig validates a rate limit configuration
func (rl *RateLimiter) validateConfig(config LimitConfig) error {
	if config.Type == "" {
		return errors.New("limit type cannot be empty")
	}

	if config.Identifier == "" {
		return errors.New("identifier cannot be empty")
	}

	if config.MaxRequests <= 0 {
		return errors.New("max requests must be positive")
	}

	if config.Window <= 0 {
		return errors.New("window must be positive")
	}

	if config.BurstAllowed && config.BurstSize <= config.MaxRequests {
		return errors.New("burst size must be greater than max requests")
	}

	return nil
}

// getApplicableLimits returns all limits that apply to a request
func (rl *RateLimiter) getApplicableLimits(req RateLimitRequest) []LimitConfig {
	var limits []LimitConfig

	// Check for specific identifier limit
	if req.Identifier != "" {
		limitKey := rl.getLimitKey(req.Type, req.Identifier)
		if limit, exists := rl.limits[limitKey]; exists {
			limits = append(limits, limit)
		}
	}

	// Check for IP-based limits
	if req.IPAddress != "" {
		limitKey := rl.getLimitKey(LimitTypeIP, req.IPAddress)
		if limit, exists := rl.limits[limitKey]; exists {
			limits = append(limits, limit)
		}
	}

	// Check for endpoint-based limits
	if req.Endpoint != "" {
		limitKey := rl.getLimitKey(LimitTypeEndpoint, req.Endpoint)
		if limit, exists := rl.limits[limitKey]; exists {
			limits = append(limits, limit)
		}
	}

	// Check for global limits
	limitKey := rl.getLimitKey(LimitTypeGlobal, "global")
	if limit, exists := rl.limits[limitKey]; exists {
		limits = append(limits, limit)
	}

	return limits
}

// checkLimit checks if a request would exceed the limit without incrementing
func (rl *RateLimiter) checkLimit(ctx context.Context, limit LimitConfig, req RateLimitRequest) (*RateLimitResult, error) {
	cacheKey := rl.getCacheKey(limit.Type, limit.Identifier)

	current, resetTime, err := rl.cache.Get(ctx, cacheKey)
	if err != nil {
		if errors.Is(err, ErrCacheKeyNotFound) {
			// No usage yet - allow
			return &RateLimitResult{
				Allowed:    true,
				Remaining:  limit.MaxRequests - 1,
				ResetTime:  time.Now().Add(limit.Window),
				LimitType:  limit.Type,
				Identifier: limit.Identifier,
			}, nil
		}
		return nil, err
	}

	maxAllowed := limit.MaxRequests
	if limit.BurstAllowed && limit.BurstSize > 0 {
		maxAllowed = limit.BurstSize
	}

	remaining := maxAllowed - current - 1 // -1 for the current request
	if remaining < 0 {
		remaining = 0
	}

	allowed := current < maxAllowed
	retryAfter := time.Duration(0)
	if !allowed {
		retryAfter = time.Until(resetTime)
	}

	return &RateLimitResult{
		Allowed:    allowed,
		Remaining:  remaining,
		ResetTime:  resetTime,
		RetryAfter: retryAfter,
		LimitType:  limit.Type,
		Identifier: limit.Identifier,
	}, nil
}

// incrementLimit increments the usage counter for a limit
func (rl *RateLimiter) incrementLimit(ctx context.Context, limit LimitConfig, req RateLimitRequest) (*RateLimitResult, error) {
	cacheKey := rl.getCacheKey(limit.Type, limit.Identifier)

	current, resetTime, err := rl.cache.Increment(ctx, cacheKey, limit.Window)
	if err != nil {
		return nil, err
	}

	maxAllowed := limit.MaxRequests
	if limit.BurstAllowed && limit.BurstSize > 0 {
		maxAllowed = limit.BurstSize
	}

	remaining := maxAllowed - current
	if remaining < 0 {
		remaining = 0
	}

	return &RateLimitResult{
		Allowed:    current <= maxAllowed,
		Remaining:  remaining,
		ResetTime:  resetTime,
		LimitType:  limit.Type,
		Identifier: limit.Identifier,
	}, nil
}

// getLimitKey creates a key for storing limit configuration
func (rl *RateLimiter) getLimitKey(limitType LimitType, identifier string) string {
	return fmt.Sprintf("limit:%s:%s", limitType, identifier)
}

// getCacheKey creates a cache key for storing usage counts
func (rl *RateLimiter) getCacheKey(limitType LimitType, identifier string) string {
	// Use time window alignment for consistent reset times
	now := time.Now()

	// Determine window alignment based on limit configuration
	var windowStart time.Time
	limitKey := rl.getLimitKey(limitType, identifier)
	if limit, exists := rl.limits[limitKey]; exists {
		windowStart = rl.alignTimeWindow(now, limit.Window)
	} else {
		// Default to minute alignment
		windowStart = rl.alignTimeWindow(now, time.Minute)
	}

	return fmt.Sprintf("usage:%s:%s:%d", limitType, identifier, windowStart.Unix())
}

// alignTimeWindow aligns time to window boundaries for consistent resets
func (rl *RateLimiter) alignTimeWindow(t time.Time, window time.Duration) time.Time {
	switch {
	case window >= 24*time.Hour:
		// Daily windows - align to start of day
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	case window >= time.Hour:
		// Hourly windows - align to start of hour
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
	case window >= time.Minute:
		// Minute windows - align to start of minute
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, t.Location())
	default:
		// Sub-minute windows - align to window boundaries
		windowSeconds := int64(window.Seconds())
		if windowSeconds <= 0 {
			windowSeconds = 1 // Prevent divide by zero
		}
		aligned := (t.Unix() / windowSeconds) * windowSeconds
		return time.Unix(aligned, 0)
	}
}

// CleanExpiredEntries removes expired cache entries (for maintenance)
func (rl *RateLimiter) CleanExpiredEntries(ctx context.Context) error {
	// This would typically be implemented by the cache store
	// For now, we'll just log that cleanup should happen
	rl.logger.Info("Rate limiter cache cleanup requested")
	return nil
}

// GetRateLimitHeaders returns standard HTTP rate limit headers
func (rl *RateLimiter) GetRateLimitHeaders(result *RateLimitResult) map[string]string {
	headers := make(map[string]string)

	if result == nil {
		return headers
	}

	headers["X-RateLimit-Limit"] = fmt.Sprintf("%d", result.Remaining+1) // Approximation
	headers["X-RateLimit-Remaining"] = fmt.Sprintf("%d", result.Remaining)
	headers["X-RateLimit-Reset"] = fmt.Sprintf("%d", result.ResetTime.Unix())

	if result.RetryAfter > 0 {
		headers["Retry-After"] = fmt.Sprintf("%d", int(result.RetryAfter.Seconds()))
	}

	return headers
}

// SetDefaultLimits configures default rate limits for the application
func (rl *RateLimiter) SetDefaultLimits(ctx context.Context) error {
	defaultLimits := []LimitConfig{
		{
			Type:        LimitTypeGlobal,
			Identifier:  "global",
			MaxRequests: 1000,
			Window:      time.Minute,
			Description: "Global rate limit for all requests",
		},
		{
			Type:        LimitTypeEndpoint,
			Identifier:  "/api/v1/fair-value",
			MaxRequests: 60,
			Window:      time.Minute,
			Description: "Rate limit for fair value endpoint",
		},
		{
			Type:        LimitTypeEndpoint,
			Identifier:  "/api/v1/health",
			MaxRequests: 30,
			Window:      time.Minute,
			Description: "Rate limit for health endpoints",
		},
	}

	for _, limit := range defaultLimits {
		if err := rl.SetLimit(ctx, limit); err != nil {
			return fmt.Errorf("failed to set default limit: %w", err)
		}
	}

	rl.logger.Info("Default rate limits configured", zap.Int("count", len(defaultLimits)))
	return nil
}
