package auth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

func TestAuthService_ValidateKey(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)

	tests := []struct {
		name        string
		setupKey    func(service *Service) string
		key         string
		wantValid   bool
		wantError   bool
		wantUserID  string
		wantExpired bool
	}{
		{
			name: "valid_active_key",
			setupKey: func(service *Service) string {
				key, err := service.CreateKey(ctx, "test-user", []entities.Permission{
					entities.PermissionReadFairValue,
				})
				require.NoError(t, err)
				return key.Key
			},
			wantValid:  true,
			wantUserID: "test-user",
		},
		{
			name:      "invalid_key",
			key:       "invalid-key-12345",
			wantValid: false,
			wantError: true,
		},
		{
			name: "expired_key",
			setupKey: func(service *Service) string {
				key, err := service.CreateKey(ctx, "test-user", []entities.Permission{
					entities.PermissionReadFairValue,
				})
				require.NoError(t, err)

				// Expire the key
				err = service.repository.UpdateKeyExpiration(ctx, key.ID, time.Now().Add(-1*time.Hour))
				require.NoError(t, err)

				return key.Key
			},
			wantValid:   false,
			wantError:   true,
			wantExpired: true,
		},
		{
			name: "deactivated_key",
			setupKey: func(service *Service) string {
				key, err := service.CreateKey(ctx, "test-user", []entities.Permission{
					entities.PermissionReadFairValue,
				})
				require.NoError(t, err)

				// Deactivate the key
				err = service.RevokeKey(ctx, key.ID)
				require.NoError(t, err)

				return key.Key
			},
			wantValid: false,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test database
			repo := setupTestRepository(t)
			service := NewService(repo, logger)

			var testKey string
			if tt.setupKey != nil {
				testKey = tt.setupKey(service)
			} else {
				testKey = tt.key
			}

			// Test validation
			info, err := service.ValidateKey(ctx, testKey)

			if tt.wantError {
				assert.Error(t, err)
				assert.Nil(t, info)

				if tt.wantExpired {
					assert.Contains(t, err.Error(), "expired")
				}
			} else {
				assert.NoError(t, err)
				require.NotNil(t, info)
				assert.Equal(t, tt.wantUserID, info.UserID)
				assert.True(t, info.IsActive)
				assert.NotEmpty(t, info.Permissions)
			}
		})
	}
}

func TestAuthService_CreateKey(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)

	tests := []struct {
		name        string
		userID      string
		permissions []entities.Permission
		wantError   bool
	}{
		{
			name:   "valid_key_creation",
			userID: "test-user-123",
			permissions: []entities.Permission{
				entities.PermissionReadFairValue,
				entities.PermissionReadHealth,
			},
			wantError: false,
		},
		{
			name:        "empty_user_id",
			userID:      "",
			permissions: []entities.Permission{entities.PermissionReadFairValue},
			wantError:   true,
		},
		{
			name:        "empty_permissions",
			userID:      "test-user",
			permissions: []entities.Permission{},
			wantError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := setupTestRepository(t)
			service := NewService(repo, logger)

			key, err := service.CreateKey(ctx, tt.userID, tt.permissions)

			if tt.wantError {
				assert.Error(t, err)
				assert.Nil(t, key)
			} else {
				assert.NoError(t, err)
				require.NotNil(t, key)
				assert.NotEmpty(t, key.ID)
				assert.NotEmpty(t, key.Key)
				assert.Equal(t, tt.userID, key.UserID)
				assert.Equal(t, tt.permissions, key.Permissions)
				assert.True(t, key.IsActive)
				assert.Equal(t, 1000, key.RateLimit) // Default rate limit
			}
		})
	}
}

func TestAuthService_RevokeKey(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)
	repo := setupTestRepository(t)
	service := NewService(repo, logger)

	// Create a key to revoke
	key, err := service.CreateKey(ctx, "test-user", []entities.Permission{
		entities.PermissionReadFairValue,
	})
	require.NoError(t, err)

	// Verify key is valid before revocation
	info, err := service.ValidateKey(ctx, key.Key)
	require.NoError(t, err)
	assert.True(t, info.IsActive)

	// Revoke the key
	err = service.RevokeKey(ctx, key.ID)
	assert.NoError(t, err)

	// Verify key is no longer valid
	_, err = service.ValidateKey(ctx, key.Key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inactive")
}

func TestAuthService_GetKeyPermissions(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)
	repo := setupTestRepository(t)
	service := NewService(repo, logger)

	permissions := []entities.Permission{
		entities.PermissionReadFairValue,
		entities.PermissionReadHealth,
		entities.PermissionReadMetrics,
	}

	// Create a key with specific permissions
	key, err := service.CreateKey(ctx, "test-user", permissions)
	require.NoError(t, err)

	// Get permissions
	gotPermissions, err := service.GetKeyPermissions(ctx, key.Key)
	assert.NoError(t, err)
	assert.ElementsMatch(t, permissions, gotPermissions)
}

func TestAuthService_UpdateUsage(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)
	repo := setupTestRepository(t)
	service := NewService(repo, logger)

	// Create a key
	key, err := service.CreateKey(ctx, "test-user", []entities.Permission{
		entities.PermissionReadFairValue,
	})
	require.NoError(t, err)

	// Record usage
	err = service.RecordUsage(ctx, key.ID, entities.UsageRecord{
		Endpoint:       "/api/v1/fair-value/AAPL",
		ResponseStatus: 200,
		ResponseTimeMs: 150,
		UserAgent:      "test-client/1.0",
		IPAddress:      "192.168.1.1",
	})
	assert.NoError(t, err)

	// Verify usage was recorded
	info, err := service.ValidateKey(ctx, key.Key)
	require.NoError(t, err)
	assert.Equal(t, int64(1), info.UsageCount)
	assert.NotNil(t, info.LastUsedAt)
}

// Helper function to set up test repository
func setupTestRepository(t *testing.T) Repository {
	// TODO: Implement in-memory test repository
	// For now, return a mock that satisfies the interface
	return &mockRepository{
		keys:  make(map[string]*entities.APIKey),
		usage: make(map[string][]entities.UsageRecord),
	}
}

// Mock repository for testing
type mockRepository struct {
	keys  map[string]*entities.APIKey
	usage map[string][]entities.UsageRecord
}

func (m *mockRepository) GetKeyByHash(ctx context.Context, keyHash string) (*entities.APIKey, error) {
	if key, exists := m.keys[keyHash]; exists {
		return key, nil
	}
	return nil, ErrKeyNotFound
}

func (m *mockRepository) CreateKey(ctx context.Context, key *entities.APIKey) error {
	m.keys[key.KeyHash] = key
	return nil
}

func (m *mockRepository) UpdateKeyStatus(ctx context.Context, keyID string, isActive bool) error {
	for _, key := range m.keys {
		if key.ID == keyID {
			key.IsActive = isActive
			return nil
		}
	}
	return ErrKeyNotFound
}

func (m *mockRepository) UpdateKeyExpiration(ctx context.Context, keyID string, expiresAt time.Time) error {
	for _, key := range m.keys {
		if key.ID == keyID {
			key.ExpiresAt = &expiresAt
			return nil
		}
	}
	return ErrKeyNotFound
}

func (m *mockRepository) RecordUsage(ctx context.Context, usage *entities.APIKeyUsage) error {
	if records, exists := m.usage[usage.APIKeyID]; exists {
		m.usage[usage.APIKeyID] = append(records, entities.UsageRecord{
			Endpoint:       usage.Endpoint,
			ResponseStatus: usage.ResponseStatus,
			ResponseTimeMs: usage.ResponseTimeMs,
			UserAgent:      usage.UserAgent,
			IPAddress:      usage.IPAddress,
		})
	} else {
		m.usage[usage.APIKeyID] = []entities.UsageRecord{{
			Endpoint:       usage.Endpoint,
			ResponseStatus: usage.ResponseStatus,
			ResponseTimeMs: usage.ResponseTimeMs,
			UserAgent:      usage.UserAgent,
			IPAddress:      usage.IPAddress,
		}}
	}

	// Update usage count and last used
	for _, key := range m.keys {
		if key.ID == usage.APIKeyID {
			key.UsageCount++
			now := time.Now()
			key.LastUsedAt = &now
			break
		}
	}

	return nil
}

func (m *mockRepository) GetUsageStats(ctx context.Context, keyID string, since time.Time) (*entities.UsageStats, error) {
	// TODO: Implement if needed for tests
	return &entities.UsageStats{}, nil
}
