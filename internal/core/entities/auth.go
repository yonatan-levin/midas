package entities

import "time"

// Permission represents different levels of API access
type Permission string

const (
	// Read permissions
	PermissionReadFairValue Permission = "read:fair_value"
	PermissionReadHealth    Permission = "read:health"
	PermissionReadMetrics   Permission = "read:metrics"

	// Write permissions (for future use)
	PermissionWriteConfig Permission = "write:config"
	PermissionManageKeys  Permission = "manage:keys"

	// Admin permissions
	PermissionAdmin Permission = "admin:all"
)

// APIKey represents an API key for authentication
type APIKey struct {
	ID          string       `json:"id" db:"id"`
	Key         string       `json:"key,omitempty" db:"-"` // Never store the raw key in DB
	KeyHash     string       `json:"-" db:"key_hash"`      // bcrypt hash of the key
	UserID      string       `json:"user_id" db:"user_id"`
	Permissions []Permission `json:"permissions" db:"permissions"`
	RateLimit   int          `json:"rate_limit" db:"rate_limit"`
	ExpiresAt   *time.Time   `json:"expires_at,omitempty" db:"expires_at"`
	IsActive    bool         `json:"is_active" db:"is_active"`
	CreatedAt   time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at" db:"updated_at"`
	LastUsedAt  *time.Time   `json:"last_used_at,omitempty" db:"last_used_at"`
	UsageCount  int64        `json:"usage_count" db:"usage_count"`
}

// APIKeyInfo represents validated API key information
type APIKeyInfo struct {
	ID          string       `json:"id"`
	UserID      string       `json:"user_id"`
	Permissions []Permission `json:"permissions"`
	RateLimit   int          `json:"rate_limit"`
	ExpiresAt   *time.Time   `json:"expires_at,omitempty"`
	IsActive    bool         `json:"is_active"`
	LastUsedAt  *time.Time   `json:"last_used_at,omitempty"`
	UsageCount  int64        `json:"usage_count"`
}

// APIKeyUsage represents a record of API key usage
type APIKeyUsage struct {
	ID             string    `json:"id" db:"id"`
	APIKeyID       string    `json:"api_key_id" db:"api_key_id"`
	Endpoint       string    `json:"endpoint" db:"endpoint"`
	Timestamp      time.Time `json:"timestamp" db:"timestamp"`
	ResponseStatus int       `json:"response_status" db:"response_status"`
	ResponseTimeMs int       `json:"response_time_ms" db:"response_time_ms"`
	UserAgent      string    `json:"user_agent,omitempty" db:"user_agent"`
	IPAddress      string    `json:"ip_address,omitempty" db:"ip_address"`
}

// UsageRecord represents usage information for recording
type UsageRecord struct {
	Endpoint       string `json:"endpoint"`
	ResponseStatus int    `json:"response_status"`
	ResponseTimeMs int    `json:"response_time_ms"`
	UserAgent      string `json:"user_agent,omitempty"`
	IPAddress      string `json:"ip_address,omitempty"`
}

// UsageStats represents aggregated usage statistics
type UsageStats struct {
	TotalRequests     int64      `json:"total_requests"`
	AverageResponseMs int        `json:"average_response_ms"`
	ErrorRate         float64    `json:"error_rate"`
	MostUsedEndpoint  string     `json:"most_used_endpoint"`
	RequestsPerHour   int64      `json:"requests_per_hour"`
	LastActivityAt    *time.Time `json:"last_activity_at,omitempty"`
}

// IsExpired checks if the API key has expired
func (k *APIKey) IsExpired() bool {
	return k.ExpiresAt != nil && k.ExpiresAt.Before(time.Now())
}

// HasPermission checks if the API key has a specific permission
func (k *APIKey) HasPermission(permission Permission) bool {
	for _, p := range k.Permissions {
		if p == permission || p == PermissionAdmin {
			return true
		}
	}
	return false
}

// IsValid checks if the API key is valid (active and not expired)
func (k *APIKey) IsValid() bool {
	return k.IsActive && !k.IsExpired()
}

// ToInfo converts APIKey to APIKeyInfo (safe for external use)
func (k *APIKey) ToInfo() *APIKeyInfo {
	return &APIKeyInfo{
		ID:          k.ID,
		UserID:      k.UserID,
		Permissions: k.Permissions,
		RateLimit:   k.RateLimit,
		ExpiresAt:   k.ExpiresAt,
		IsActive:    k.IsActive,
		LastUsedAt:  k.LastUsedAt,
		UsageCount:  k.UsageCount,
	}
}
