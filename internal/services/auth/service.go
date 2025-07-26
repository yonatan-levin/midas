package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

var (
	// ErrKeyNotFound indicates the API key was not found
	ErrKeyNotFound = errors.New("API key not found")

	// ErrKeyExpired indicates the API key has expired
	ErrKeyExpired = errors.New("API key has expired")

	// ErrKeyInactive indicates the API key is inactive
	ErrKeyInactive = errors.New("API key is inactive")

	// ErrInvalidInput indicates invalid input parameters
	ErrInvalidInput = errors.New("invalid input parameters")
)

// Repository defines the interface for API key persistence
type Repository interface {
	GetKeyByHash(ctx context.Context, keyHash string) (*entities.APIKey, error)
	CreateKey(ctx context.Context, key *entities.APIKey) error
	UpdateKeyStatus(ctx context.Context, keyID string, isActive bool) error
	UpdateKeyExpiration(ctx context.Context, keyID string, expiresAt time.Time) error
	RecordUsage(ctx context.Context, usage *entities.APIKeyUsage) error
	GetUsageStats(ctx context.Context, keyID string, since time.Time) (*entities.UsageStats, error)
}

// Service provides authentication functionality
type Service struct {
	repository Repository
	logger     *zap.Logger
}

// NewService creates a new authentication service
func NewService(repository Repository, logger *zap.Logger) *Service {
	return &Service{
		repository: repository,
		logger:     logger,
	}
}

// ValidateKey validates an API key and returns key information
func (s *Service) ValidateKey(ctx context.Context, key string) (*entities.APIKeyInfo, error) {
	if key == "" {
		return nil, ErrInvalidInput
	}

	// Hash the provided key to look it up
	keyHash := s.hashKey(key)

	// Get the key from repository
	apiKey, err := s.repository.GetKeyByHash(ctx, keyHash)
	if err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			s.logger.Warn("Invalid API key attempt", zap.String("key_prefix", key[:min(8, len(key))]))
			return nil, ErrKeyNotFound
		}
		s.logger.Error("Failed to retrieve API key", zap.Error(err))
		return nil, fmt.Errorf("failed to retrieve API key: %w", err)
	}

	// Check if key is active
	if !apiKey.IsActive {
		s.logger.Warn("Inactive API key used", zap.String("key_id", apiKey.ID), zap.String("user_id", apiKey.UserID))
		return nil, ErrKeyInactive
	}

	// Check if key is expired
	if apiKey.IsExpired() {
		s.logger.Warn("Expired API key used", zap.String("key_id", apiKey.ID), zap.String("user_id", apiKey.UserID))
		return nil, ErrKeyExpired
	}

	s.logger.Debug("API key validated successfully",
		zap.String("key_id", apiKey.ID),
		zap.String("user_id", apiKey.UserID),
		zap.Int("permissions_count", len(apiKey.Permissions)),
	)

	return apiKey.ToInfo(), nil
}

// CreateKey creates a new API key
func (s *Service) CreateKey(ctx context.Context, userID string, permissions []entities.Permission) (*entities.APIKey, error) {
	if userID == "" {
		return nil, fmt.Errorf("%w: user ID cannot be empty", ErrInvalidInput)
	}

	if len(permissions) == 0 {
		return nil, fmt.Errorf("%w: permissions cannot be empty", ErrInvalidInput)
	}

	// Generate a new API key
	key, err := s.generateKey()
	if err != nil {
		s.logger.Error("Failed to generate API key", zap.Error(err))
		return nil, fmt.Errorf("failed to generate API key: %w", err)
	}

	// Hash the key for storage
	keyHash := s.hashKey(key)

	// Create the API key entity
	apiKey := &entities.APIKey{
		ID:          s.generateID(),
		Key:         key, // Include raw key in response only
		KeyHash:     keyHash,
		UserID:      userID,
		Permissions: permissions,
		RateLimit:   1000, // Default rate limit
		IsActive:    true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		UsageCount:  0,
	}

	// Store in repository
	err = s.repository.CreateKey(ctx, apiKey)
	if err != nil {
		s.logger.Error("Failed to create API key", zap.Error(err), zap.String("user_id", userID))
		return nil, fmt.Errorf("failed to create API key: %w", err)
	}

	s.logger.Info("API key created successfully",
		zap.String("key_id", apiKey.ID),
		zap.String("user_id", userID),
		zap.Int("permissions_count", len(permissions)),
	)

	return apiKey, nil
}

// RevokeKey deactivates an API key
func (s *Service) RevokeKey(ctx context.Context, keyID string) error {
	if keyID == "" {
		return fmt.Errorf("%w: key ID cannot be empty", ErrInvalidInput)
	}

	err := s.repository.UpdateKeyStatus(ctx, keyID, false)
	if err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			return ErrKeyNotFound
		}
		s.logger.Error("Failed to revoke API key", zap.Error(err), zap.String("key_id", keyID))
		return fmt.Errorf("failed to revoke API key: %w", err)
	}

	s.logger.Info("API key revoked successfully", zap.String("key_id", keyID))
	return nil
}

// GetKeyPermissions returns the permissions for a given API key
func (s *Service) GetKeyPermissions(ctx context.Context, key string) ([]entities.Permission, error) {
	keyInfo, err := s.ValidateKey(ctx, key)
	if err != nil {
		return nil, err
	}

	return keyInfo.Permissions, nil
}

// RecordUsage records API key usage for monitoring and rate limiting
func (s *Service) RecordUsage(ctx context.Context, keyID string, record entities.UsageRecord) error {
	if keyID == "" {
		return fmt.Errorf("%w: key ID cannot be empty", ErrInvalidInput)
	}

	usage := &entities.APIKeyUsage{
		ID:             s.generateID(),
		APIKeyID:       keyID,
		Endpoint:       record.Endpoint,
		Timestamp:      time.Now(),
		ResponseStatus: record.ResponseStatus,
		ResponseTimeMs: record.ResponseTimeMs,
		UserAgent:      record.UserAgent,
		IPAddress:      record.IPAddress,
	}

	err := s.repository.RecordUsage(ctx, usage)
	if err != nil {
		s.logger.Error("Failed to record API key usage",
			zap.Error(err),
			zap.String("key_id", keyID),
			zap.String("endpoint", record.Endpoint),
		)
		return fmt.Errorf("failed to record usage: %w", err)
	}

	return nil
}

// GetUsageStats returns usage statistics for an API key
func (s *Service) GetUsageStats(ctx context.Context, keyID string, since time.Time) (*entities.UsageStats, error) {
	if keyID == "" {
		return nil, fmt.Errorf("%w: key ID cannot be empty", ErrInvalidInput)
	}

	stats, err := s.repository.GetUsageStats(ctx, keyID, since)
	if err != nil {
		s.logger.Error("Failed to get usage stats", zap.Error(err), zap.String("key_id", keyID))
		return nil, fmt.Errorf("failed to get usage stats: %w", err)
	}

	return stats, nil
}

// generateKey creates a cryptographically secure API key
func (s *Service) generateKey() (string, error) {
	// Generate 32 random bytes
	bytes := make([]byte, 32)
	_, err := rand.Read(bytes)
	if err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	// Encode as hex and add prefix
	key := "dcf_" + hex.EncodeToString(bytes)
	return key, nil
}

// hashKey creates a SHA-256 hash of the API key for storage
func (s *Service) hashKey(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

// generateID creates a unique identifier
func (s *Service) generateID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ValidatePermission checks if a permission string is valid
func ValidatePermission(permission string) bool {
	validPermissions := []string{
		string(entities.PermissionReadFairValue),
		string(entities.PermissionReadHealth),
		string(entities.PermissionReadMetrics),
		string(entities.PermissionWriteConfig),
		string(entities.PermissionManageKeys),
		string(entities.PermissionAdmin),
	}

	for _, valid := range validPermissions {
		if permission == valid {
			return true
		}
	}
	return false
}

// ParsePermissions converts string slice to Permission slice
func ParsePermissions(permissions []string) ([]entities.Permission, error) {
	result := make([]entities.Permission, 0, len(permissions))

	for _, p := range permissions {
		if !ValidatePermission(p) {
			return nil, fmt.Errorf("%w: invalid permission '%s'", ErrInvalidInput, p)
		}
		result = append(result, entities.Permission(p))
	}

	return result, nil
}

// FormatKey adds proper formatting to an API key for display
func FormatKey(key string) string {
	if len(key) < 12 {
		return key
	}

	// Show first 4 and last 4 characters with asterisks in between
	visible := key[:4] + strings.Repeat("*", len(key)-8) + key[len(key)-4:]
	return visible
}
