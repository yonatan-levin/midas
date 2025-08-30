package entities

import "time"

// WatchlistEntry represents a ticker in the scheduler's watchlist for nightly data ingestion
type WatchlistEntry struct {
	ID            int        `json:"id" db:"id"`
	Ticker        string     `json:"ticker" db:"ticker"`
	IsActive      bool       `json:"is_active" db:"is_active"`
	Priority      int        `json:"priority" db:"priority"` // 1=high, 2=medium, 3=low
	AddedReason   string     `json:"added_reason" db:"added_reason"`
	LastFetchedAt *time.Time `json:"last_fetched_at,omitempty" db:"last_fetched_at"`
	FetchFailures int        `json:"fetch_failures" db:"fetch_failures"`
	MaxFailures   int        `json:"max_failures" db:"max_failures"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
}

// WatchlistPriority represents the priority levels for watchlist entries
type WatchlistPriority int

const (
	HighPriority   WatchlistPriority = 1
	MediumPriority WatchlistPriority = 2
	LowPriority    WatchlistPriority = 3
)

// String returns the string representation of the priority
func (p WatchlistPriority) String() string {
	switch p {
	case HighPriority:
		return "high"
	case MediumPriority:
		return "medium"
	case LowPriority:
		return "low"
	default:
		return "unknown"
	}
}

// WatchlistFilter represents filtering criteria for watchlist queries
type WatchlistFilter struct {
	IsActive    *bool              `json:"is_active,omitempty"`
	Priority    *WatchlistPriority `json:"priority,omitempty"`
	MaxFailures *int               `json:"max_failures,omitempty"`
	Limit       int                `json:"limit,omitempty"`
	Offset      int                `json:"offset,omitempty"`
}

// WatchlistStats provides statistics about the watchlist
type WatchlistStats struct {
	TotalEntries    int `json:"total_entries"`
	ActiveEntries   int `json:"active_entries"`
	InactiveEntries int `json:"inactive_entries"`
	HighPriority    int `json:"high_priority"`
	MediumPriority  int `json:"medium_priority"`
	LowPriority     int `json:"low_priority"`
	RecentFailures  int `json:"recent_failures"` // Entries with failures in last 24h
}

// CreateWatchlistEntryRequest represents a request to add a ticker to the watchlist
type CreateWatchlistEntryRequest struct {
	Ticker      string            `json:"ticker" validate:"required,max=10"`
	Priority    WatchlistPriority `json:"priority,omitempty"`
	AddedReason string            `json:"added_reason,omitempty"`
	MaxFailures int               `json:"max_failures,omitempty"`
}

// UpdateWatchlistEntryRequest represents a request to update a watchlist entry
type UpdateWatchlistEntryRequest struct {
	IsActive    *bool              `json:"is_active,omitempty"`
	Priority    *WatchlistPriority `json:"priority,omitempty"`
	MaxFailures *int               `json:"max_failures,omitempty"`
}

// IsHighPriority returns true if the entry has high priority
func (w *WatchlistEntry) IsHighPriority() bool {
	return w.Priority == int(HighPriority)
}

// IsMediumPriority returns true if the entry has medium priority
func (w *WatchlistEntry) IsMediumPriority() bool {
	return w.Priority == int(MediumPriority)
}

// IsLowPriority returns true if the entry has low priority
func (w *WatchlistEntry) IsLowPriority() bool {
	return w.Priority == int(LowPriority)
}

// ShouldDisableAfterFailure returns true if the entry should be disabled due to consecutive failures
func (w *WatchlistEntry) ShouldDisableAfterFailure() bool {
	return w.FetchFailures >= w.MaxFailures
}

// CanRetry returns true if the entry is active and hasn't exceeded max failures
func (w *WatchlistEntry) CanRetry() bool {
	return w.IsActive && !w.ShouldDisableAfterFailure()
}

// GetPriority returns the priority as a WatchlistPriority enum
func (w *WatchlistEntry) GetPriority() WatchlistPriority {
	return WatchlistPriority(w.Priority)
}
