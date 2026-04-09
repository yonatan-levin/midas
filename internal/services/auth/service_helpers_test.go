package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// ---------------------------------------------------------------------------
// ValidatePermission — package-level function (line 259)
// ---------------------------------------------------------------------------

func TestValidatePermission_ValidPermissions(t *testing.T) {
	// Every permission constant defined in the entities package must be accepted.
	tests := []struct {
		name       string
		permission string
	}{
		{name: "read:fair_value", permission: "read:fair_value"},
		{name: "read:health", permission: "read:health"},
		{name: "read:metrics", permission: "read:metrics"},
		{name: "write:config", permission: "write:config"},
		{name: "manage:keys", permission: "manage:keys"},
		{name: "admin:all", permission: "admin:all"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, ValidatePermission(tt.permission),
				"expected %q to be a valid permission", tt.permission)
		})
	}
}

func TestValidatePermission_InvalidPermissions(t *testing.T) {
	tests := []struct {
		name       string
		permission string
	}{
		{name: "empty_string", permission: ""},
		{name: "arbitrary_string", permission: "invalid"},
		{name: "unknown_scope", permission: "read:nonexistent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, ValidatePermission(tt.permission),
				"expected %q to be an invalid permission", tt.permission)
		})
	}
}

// ---------------------------------------------------------------------------
// ParsePermissions — package-level function (line 278)
// ---------------------------------------------------------------------------

func TestParsePermissions_ValidSlice(t *testing.T) {
	input := []string{"read:fair_value", "read:health"}

	result, err := ParsePermissions(input)

	require.NoError(t, err)
	assert.Equal(t, []entities.Permission{
		entities.PermissionReadFairValue,
		entities.PermissionReadHealth,
	}, result)
}

func TestParsePermissions_InvalidEntry(t *testing.T) {
	// One valid + one invalid entry — entire call must fail.
	input := []string{"read:fair_value", "bogus"}

	result, err := ParsePermissions(input)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, errors.Is(err, ErrInvalidInput),
		"expected error to wrap ErrInvalidInput, got: %v", err)
	assert.Contains(t, err.Error(), "bogus")
}

func TestParsePermissions_EmptySlice(t *testing.T) {
	result, err := ParsePermissions([]string{})

	require.NoError(t, err)
	// An empty input must return an empty (non-nil) slice.
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

// ---------------------------------------------------------------------------
// FormatKey — package-level function (line 292)
// ---------------------------------------------------------------------------

func TestFormatKey_Scenarios(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{
			name: "short_key_returned_as_is",
			key:  "abc",
			want: "abc",
		},
		{
			name: "exactly_11_chars_returned_as_is",
			key:  "12345678901",
			want: "12345678901",
		},
		{
			name: "exactly_12_chars_is_masked",
			key:  "123456789012",
			// first 4 + 4 asterisks (12-8=4) + last 4
			want: "1234" + strings.Repeat("*", 4) + "9012",
		},
		{
			name: "normal_dcf_prefixed_key",
			key:  "dcf_abcdefghijklmnop",
			// first 4 + asterisks for middle + last 4
			want: "dcf_" + strings.Repeat("*", len("dcf_abcdefghijklmnop")-8) + "mnop",
		},
		{
			name: "empty_key",
			key:  "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatKey(tt.key)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// GetUsageStats — Service method (line 207)
// ---------------------------------------------------------------------------

func TestGetUsageStats_EmptyKeyID(t *testing.T) {
	logger := zaptest.NewLogger(t)
	repo := setupTestRepository(t)
	service := NewService(repo, logger)

	stats, err := service.GetUsageStats(context.Background(), "", time.Now().Add(-24*time.Hour))

	assert.Nil(t, stats)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidInput),
		"expected ErrInvalidInput, got: %v", err)
}

func TestGetUsageStats_HappyPath(t *testing.T) {
	logger := zaptest.NewLogger(t)
	repo := setupTestRepository(t)
	service := NewService(repo, logger)
	ctx := context.Background()

	// Create a key so we have a valid keyID.
	key, err := service.CreateKey(ctx, "stats-user", []entities.Permission{
		entities.PermissionReadFairValue,
	})
	require.NoError(t, err)

	// Record some usage so the call exercises the full path.
	err = service.RecordUsage(ctx, key.ID, entities.UsageRecord{
		Endpoint:       "/api/v1/fair-value/AAPL",
		ResponseStatus: 200,
		ResponseTimeMs: 100,
	})
	require.NoError(t, err)

	// Retrieve stats — the mock returns an empty UsageStats struct.
	stats, err := service.GetUsageStats(ctx, key.ID, time.Now().Add(-1*time.Hour))

	require.NoError(t, err)
	assert.NotNil(t, stats)
}

// ---------------------------------------------------------------------------
// RevokeKey — error paths (line 148)
// ---------------------------------------------------------------------------

func TestRevokeKey_EmptyKeyID(t *testing.T) {
	logger := zaptest.NewLogger(t)
	repo := setupTestRepository(t)
	service := NewService(repo, logger)

	err := service.RevokeKey(context.Background(), "")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidInput),
		"expected ErrInvalidInput, got: %v", err)
}

func TestRevokeKey_NonExistentKeyID(t *testing.T) {
	logger := zaptest.NewLogger(t)
	repo := setupTestRepository(t)
	service := NewService(repo, logger)

	err := service.RevokeKey(context.Background(), "does-not-exist")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrKeyNotFound),
		"expected ErrKeyNotFound, got: %v", err)
}

// ---------------------------------------------------------------------------
// RecordUsage — error path (line 177)
// ---------------------------------------------------------------------------

func TestRecordUsage_EmptyKeyID(t *testing.T) {
	logger := zaptest.NewLogger(t)
	repo := setupTestRepository(t)
	service := NewService(repo, logger)

	err := service.RecordUsage(context.Background(), "", entities.UsageRecord{
		Endpoint:       "/api/v1/health",
		ResponseStatus: 200,
		ResponseTimeMs: 10,
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidInput),
		"expected ErrInvalidInput, got: %v", err)
}

// ---------------------------------------------------------------------------
// ValidateKey — edge case (line 57)
// ---------------------------------------------------------------------------

func TestValidateKey_EmptyKey(t *testing.T) {
	logger := zaptest.NewLogger(t)
	repo := setupTestRepository(t)
	service := NewService(repo, logger)

	info, err := service.ValidateKey(context.Background(), "")

	assert.Nil(t, info)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidInput),
		"expected ErrInvalidInput, got: %v", err)
}

// ---------------------------------------------------------------------------
// Error-injection tests — cover repository error propagation paths
// ---------------------------------------------------------------------------

// errMockRepository wraps mockRepository but injects errors for specific methods.
type errMockRepository struct {
	mockRepository
	createKeyErr    error
	recordUsageErr  error
	getStatsErr     error
	getKeyErr       error
	updateStatusErr error
}

func (m *errMockRepository) CreateKey(ctx context.Context, key *entities.APIKey) error {
	if m.createKeyErr != nil {
		return m.createKeyErr
	}
	return m.mockRepository.CreateKey(ctx, key)
}

func (m *errMockRepository) RecordUsage(ctx context.Context, usage *entities.APIKeyUsage) error {
	if m.recordUsageErr != nil {
		return m.recordUsageErr
	}
	return m.mockRepository.RecordUsage(ctx, usage)
}

func (m *errMockRepository) GetUsageStats(ctx context.Context, keyID string, since time.Time) (*entities.UsageStats, error) {
	if m.getStatsErr != nil {
		return nil, m.getStatsErr
	}
	return m.mockRepository.GetUsageStats(ctx, keyID, since)
}

func (m *errMockRepository) GetKeyByHash(ctx context.Context, keyHash string) (*entities.APIKey, error) {
	if m.getKeyErr != nil {
		return nil, m.getKeyErr
	}
	return m.mockRepository.GetKeyByHash(ctx, keyHash)
}

func (m *errMockRepository) UpdateKeyStatus(ctx context.Context, keyID string, isActive bool) error {
	if m.updateStatusErr != nil {
		return m.updateStatusErr
	}
	return m.mockRepository.UpdateKeyStatus(ctx, keyID, isActive)
}

func TestCreateKey_RepositoryError(t *testing.T) {
	logger := zaptest.NewLogger(t)
	repo := &errMockRepository{
		mockRepository: mockRepository{
			keys:  make(map[string]*entities.APIKey),
			usage: make(map[string][]entities.UsageRecord),
		},
		createKeyErr: errors.New("db connection lost"),
	}
	service := NewService(repo, logger)

	key, err := service.CreateKey(context.Background(), "user-1", []entities.Permission{
		entities.PermissionReadFairValue,
	})

	assert.Nil(t, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create API key")
}

func TestRecordUsage_RepositoryError(t *testing.T) {
	logger := zaptest.NewLogger(t)
	repo := &errMockRepository{
		mockRepository: mockRepository{
			keys:  make(map[string]*entities.APIKey),
			usage: make(map[string][]entities.UsageRecord),
		},
		recordUsageErr: errors.New("disk full"),
	}
	service := NewService(repo, logger)

	err := service.RecordUsage(context.Background(), "some-key-id", entities.UsageRecord{
		Endpoint:       "/api/v1/health",
		ResponseStatus: 200,
		ResponseTimeMs: 10,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to record usage")
}

func TestGetUsageStats_RepositoryError(t *testing.T) {
	logger := zaptest.NewLogger(t)
	repo := &errMockRepository{
		mockRepository: mockRepository{
			keys:  make(map[string]*entities.APIKey),
			usage: make(map[string][]entities.UsageRecord),
		},
		getStatsErr: errors.New("query timeout"),
	}
	service := NewService(repo, logger)

	stats, err := service.GetUsageStats(context.Background(), "some-key-id", time.Now().Add(-1*time.Hour))

	assert.Nil(t, stats)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get usage stats")
}

func TestValidateKey_RepositoryGenericError(t *testing.T) {
	logger := zaptest.NewLogger(t)
	repo := &errMockRepository{
		mockRepository: mockRepository{
			keys:  make(map[string]*entities.APIKey),
			usage: make(map[string][]entities.UsageRecord),
		},
		getKeyErr: errors.New("db timeout"),
	}
	service := NewService(repo, logger)

	info, err := service.ValidateKey(context.Background(), "dcf_some_valid_looking_key")

	assert.Nil(t, info)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to retrieve API key")
}

func TestGetKeyPermissions_InvalidKey(t *testing.T) {
	logger := zaptest.NewLogger(t)
	repo := setupTestRepository(t)
	service := NewService(repo, logger)

	perms, err := service.GetKeyPermissions(context.Background(), "nonexistent-key")

	assert.Nil(t, perms)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrKeyNotFound))
}

func TestRevokeKey_RepositoryGenericError(t *testing.T) {
	logger := zaptest.NewLogger(t)
	repo := &errMockRepository{
		mockRepository: mockRepository{
			keys:  make(map[string]*entities.APIKey),
			usage: make(map[string][]entities.UsageRecord),
		},
	}
	service := NewService(repo, logger)

	// Create a key first so revoke doesn't hit "not found"
	key, err := service.CreateKey(context.Background(), "user", []entities.Permission{entities.PermissionReadFairValue})
	require.NoError(t, err)

	// Inject a DB error for UpdateKeyStatus
	repo.updateStatusErr = errors.New("connection reset")

	err = service.RevokeKey(context.Background(), key.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to revoke API key")
}
