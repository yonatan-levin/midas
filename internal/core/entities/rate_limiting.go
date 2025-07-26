package entities

import (
	"time"
)

// LimitType represents different types of rate limits
type LimitType string

const (
	LimitTypeGlobal    LimitType = "global"
	LimitTypeAPIKey    LimitType = "api_key"
	LimitTypeIP        LimitType = "ip"
	LimitTypeEndpoint  LimitType = "endpoint"
	LimitTypeUser      LimitType = "user"
	LimitTypeBurst     LimitType = "burst"
	LimitTypeDaily     LimitType = "daily"
	LimitTypeHourly    LimitType = "hourly"
	LimitTypePerMinute LimitType = "per_minute"
	LimitTypePerSecond LimitType = "per_second"
)

// LimitConfig represents configuration for a specific rate limit
type LimitConfig struct {
	Type        LimitType     `json:"type"`
	Limit       int64         `json:"limit"`       // Maximum requests allowed
	Window      time.Duration `json:"window"`      // Time window for the limit
	BurstSize   int64         `json:"burst_size"`  // Maximum burst allowed
	ResetTime   time.Time     `json:"reset_time"`  // When the limit resets
	Enabled     bool          `json:"enabled"`     // Whether this limit is active
	Description string        `json:"description"` // Human-readable description
	Priority    int           `json:"priority"`    // Priority for applying multiple limits
}

// RateLimitRequest represents a request to check rate limits
type RateLimitRequest struct {
	Identifier string            `json:"identifier"`  // API key, IP, user ID, etc.
	LimitTypes []LimitType       `json:"limit_types"` // Types of limits to check
	Endpoint   string            `json:"endpoint"`    // API endpoint being accessed
	Method     string            `json:"method"`      // HTTP method
	Metadata   map[string]string `json:"metadata"`    // Additional context
	Timestamp  time.Time         `json:"timestamp"`   // Request timestamp
}

// RateLimitResult represents the result of a rate limit check
type RateLimitResult struct {
	Allowed           bool              `json:"allowed"`            // Whether request is allowed
	LimitType         LimitType         `json:"limit_type"`         // Which limit was hit (if any)
	RemainingRequests int64             `json:"remaining_requests"` // Requests remaining in window
	ResetTime         time.Time         `json:"reset_time"`         // When limit resets
	RetryAfter        time.Duration     `json:"retry_after"`        // How long to wait before retry
	UsageInfo         UsageInfo         `json:"usage_info"`         // Current usage information
	AppliedLimits     []LimitConfig     `json:"applied_limits"`     // All limits that were checked
	RejectionReason   string            `json:"rejection_reason"`   // Why request was rejected
	Headers           map[string]string `json:"headers"`            // HTTP headers to return
}

// UsageInfo represents current usage statistics for rate limiting
type UsageInfo struct {
	RequestCount   int64         `json:"request_count"`   // Total requests in current window
	WindowStart    time.Time     `json:"window_start"`    // Start of current window
	WindowEnd      time.Time     `json:"window_end"`      // End of current window
	WindowDuration time.Duration `json:"window_duration"` // Duration of the window
	RecentRequests []time.Time   `json:"recent_requests"` // Recent request timestamps
	AverageRate    float64       `json:"average_rate"`    // Average requests per second
	PeakRate       float64       `json:"peak_rate"`       // Peak requests per second
	BurstUsed      int64         `json:"burst_used"`      // Burst capacity used
	BurstRemaining int64         `json:"burst_remaining"` // Burst capacity remaining
	LastRequest    time.Time     `json:"last_request"`    // Timestamp of last request
}
