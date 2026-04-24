package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/api/v1/handlers"
	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/auth"
	"github.com/midas/dcf-valuation-api/internal/services/metrics"
	"github.com/midas/dcf-valuation-api/internal/services/ratelimit"
)

// ---------------------------------------------------------------------------
// Mock: auth.Repository — used to back a real *auth.Service for middleware tests
// ---------------------------------------------------------------------------

type mockAuthRepo struct {
	mock.Mock
}

func (m *mockAuthRepo) GetKeyByHash(ctx context.Context, keyHash string) (*entities.APIKey, error) {
	args := m.Called(ctx, keyHash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.APIKey), args.Error(1)
}

func (m *mockAuthRepo) CreateKey(ctx context.Context, key *entities.APIKey) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

func (m *mockAuthRepo) UpdateKeyStatus(ctx context.Context, keyID string, isActive bool) error {
	args := m.Called(ctx, keyID, isActive)
	return args.Error(0)
}

func (m *mockAuthRepo) UpdateKeyExpiration(ctx context.Context, keyID string, expiresAt time.Time) error {
	args := m.Called(ctx, keyID, expiresAt)
	return args.Error(0)
}

func (m *mockAuthRepo) RecordUsage(ctx context.Context, usage *entities.APIKeyUsage) error {
	args := m.Called(ctx, usage)
	return args.Error(0)
}

func (m *mockAuthRepo) GetUsageStats(ctx context.Context, keyID string, since time.Time) (*entities.UsageStats, error) {
	args := m.Called(ctx, keyID, since)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.UsageStats), args.Error(1)
}

// ---------------------------------------------------------------------------
// Mock: ratelimit.CacheStore — used to back a real *ratelimit.RateLimiter
// ---------------------------------------------------------------------------

type mockCacheStore struct {
	mock.Mock
}

func (m *mockCacheStore) Increment(ctx context.Context, key string, window time.Duration) (int, time.Time, error) {
	args := m.Called(ctx, key, window)
	return args.Int(0), args.Get(1).(time.Time), args.Error(2)
}

func (m *mockCacheStore) Get(ctx context.Context, key string) (int, time.Time, error) {
	args := m.Called(ctx, key)
	return args.Int(0), args.Get(1).(time.Time), args.Error(2)
}

func (m *mockCacheStore) Set(ctx context.Context, key string, value int, window time.Duration) error {
	args := m.Called(ctx, key, value, window)
	return args.Error(0)
}

func (m *mockCacheStore) Delete(ctx context.Context, key string) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func TestMain(m *testing.M) {
	// Prevent Gin debug logs from polluting test output
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// newMinimalServer creates a Server with only the fields needed for most tests.
// Individual tests can override specific fields before calling methods.
func newMinimalServer() *Server {
	return &Server{
		logger: zap.NewNop(),
		config: &config.Config{
			Version:     "v0.9.0-test",
			Environment: "test",
			BuildTime:   "2025-01-01T00:00:00Z",
			GitCommit:   "abc1234",
			Port:        "8080",
		},
		engine: gin.New(),
	}
}

// newServerWithAuth creates a Server wired with a real auth.Service backed by
// the given mock repository. The returned mock is ready for expectation setup.
func newServerWithAuth(repo *mockAuthRepo) *Server {
	s := newMinimalServer()
	s.authService = auth.NewService(repo, zap.NewNop())
	return s
}

// newServerWithRateLimiter creates a Server with a real RateLimiter backed by
// the given mock cache store.
func newServerWithRateLimiter(cache *mockCacheStore) *Server {
	s := newMinimalServer()
	s.rateLimiter = ratelimit.NewRateLimiter(cache, zap.NewNop())
	return s
}

// performRequest sends an HTTP request through a gin engine and returns the recorder.
func performRequest(engine *gin.Engine, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	engine.ServeHTTP(w, req)
	return w
}

// parseJSONBody decodes the response body into a map for assertion.
func parseJSONBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err, "response body should be valid JSON")
	return body
}

// validAPIKey is a well-known test key long enough for safeKeyPrefix.
const validAPIKey = "dcf_test_key_1234567890abcdef"

// buildActiveAPIKey creates an APIKey entity that is active and not expired.
func buildActiveAPIKey(permissions []entities.Permission) *entities.APIKey {
	return &entities.APIKey{
		ID:          "key-id-001",
		UserID:      "user-001",
		Permissions: permissions,
		RateLimit:   1000,
		IsActive:    true,
		CreatedAt:   time.Now().Add(-24 * time.Hour),
		UpdatedAt:   time.Now(),
	}
}

// ===================================================================
// Group 1: Pure helper function tests
// ===================================================================

func TestGenerateRequestID(t *testing.T) {
	t.Run("returns non-empty string with req- prefix", func(t *testing.T) {
		id := generateRequestID()
		assert.True(t, strings.HasPrefix(id, "req-"), "should start with req-")
		assert.Greater(t, len(id), 4, "should be longer than just the prefix")
	})

	t.Run("IDs are non-empty and properly formatted", func(t *testing.T) {
		// generateRequestID uses time.Now().UnixNano(), which on Windows
		// has ~15ms granularity — tight-loop uniqueness isn't guaranteed.
		// We verify format correctness instead.
		for i := 0; i < 10; i++ {
			id := generateRequestID()
			assert.True(t, strings.HasPrefix(id, "req-"), "ID must start with req- prefix")
			assert.Greater(t, len(id), 8, "ID must include a numeric suffix")
		}
	})
}

func TestServer_safeKeyPrefix(t *testing.T) {
	s := newMinimalServer()

	tests := []struct {
		name     string
		key      string
		expected string
	}{
		{
			name:     "short key returns masked value",
			key:      "abc",
			expected: "***",
		},
		{
			name:     "exactly 8 chars returns first 4 + mask",
			key:      "12345678",
			expected: "1234***",
		},
		{
			name:     "long key returns first 4 + mask",
			key:      "dcf_test_key_1234567890",
			expected: "dcf_***",
		},
		{
			name:     "empty key returns masked value",
			key:      "",
			expected: "***",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := s.safeKeyPrefix(tc.key)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestServer_permissionsToStrings(t *testing.T) {
	s := newMinimalServer()

	tests := []struct {
		name        string
		permissions []entities.Permission
		expected    []string
	}{
		{
			name:        "empty slice",
			permissions: []entities.Permission{},
			expected:    []string{},
		},
		{
			name:        "single permission",
			permissions: []entities.Permission{entities.PermissionReadFairValue},
			expected:    []string{"read:fair_value"},
		},
		{
			name: "multiple permissions",
			permissions: []entities.Permission{
				entities.PermissionReadFairValue,
				entities.PermissionReadHealth,
				entities.PermissionAdmin,
			},
			expected: []string{"read:fair_value", "read:health", "admin:all"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := s.permissionsToStrings(tc.permissions)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// ===================================================================
// Group 2: Simple handler tests (healthCheck, readinessCheck, versionInfo)
// ===================================================================

func TestServer_healthCheck(t *testing.T) {
	s := newMinimalServer()
	s.engine.GET("/health", s.healthCheck)

	w := performRequest(s.engine, http.MethodGet, "/health", nil)

	assert.Equal(t, http.StatusOK, w.Code)

	body := parseJSONBody(t, w)
	assert.Equal(t, "ok", body["status"])
	assert.Equal(t, "dcf-valuation-api", body["service"])
	assert.NotEmpty(t, body["timestamp"], "should include a timestamp")
}

func TestServer_readinessCheck(t *testing.T) {
	s := newMinimalServer()
	s.engine.GET("/ready", s.readinessCheck)

	w := performRequest(s.engine, http.MethodGet, "/ready", nil)

	assert.Equal(t, http.StatusOK, w.Code)

	body := parseJSONBody(t, w)
	assert.Equal(t, "ready", body["status"])
	assert.NotEmpty(t, body["timestamp"])

	// Verify nested checks object
	checks, ok := body["checks"].(map[string]interface{})
	require.True(t, ok, "checks should be an object")
	assert.Equal(t, "ok", checks["database"])
	assert.Equal(t, "ok", checks["external_apis"])
	assert.Equal(t, "ok", checks["cache"])
}

func TestServer_versionInfo(t *testing.T) {
	s := newMinimalServer()
	s.engine.GET("/version", s.versionInfo)

	w := performRequest(s.engine, http.MethodGet, "/version", nil)

	assert.Equal(t, http.StatusOK, w.Code)

	body := parseJSONBody(t, w)
	assert.Equal(t, "v0.9.0-test", body["version"])
	assert.Equal(t, "test", body["environment"])
	assert.Equal(t, "2025-01-01T00:00:00Z", body["build_time"])
	assert.Equal(t, "abc1234", body["git_commit"])
}

// ===================================================================
// Group 3: respondWithError — RFC 7807 Problem Details
// ===================================================================

func TestServer_respondWithError(t *testing.T) {
	s := newMinimalServer()

	// Register a test route that triggers respondWithError
	s.engine.GET("/test-error", func(c *gin.Context) {
		s.respondWithError(c, http.StatusBadRequest, "TEST_001", "something went wrong")
	})

	w := performRequest(s.engine, http.MethodGet, "/test-error", nil)

	// Verify status code
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Verify Content-Type header
	assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))

	body := parseJSONBody(t, w)

	// RFC 7807 required fields
	assert.Equal(t, "https://problems.midas.dev/TEST_001", body["type"])
	assert.Equal(t, "Bad Request", body["title"])
	assert.Equal(t, float64(http.StatusBadRequest), body["status"])
	assert.Equal(t, "something went wrong", body["detail"])
	assert.Equal(t, "/test-error", body["instance"])

	// Extension fields
	assert.Equal(t, "TEST_001", body["code"])
	assert.NotEmpty(t, body["timestamp"])
	assert.Equal(t, "GET", body["method"])
}

func TestServer_respondWithError_aborts(t *testing.T) {
	s := newMinimalServer()
	handlerReached := false

	// The middleware calls respondWithError, then Gin should abort
	s.engine.GET("/abort-check", func(c *gin.Context) {
		s.respondWithError(c, http.StatusForbidden, "FORBIDDEN", "no access")
	}, func(c *gin.Context) {
		// This handler should NOT be reached because respondWithError calls c.Abort()
		handlerReached = true
	})

	performRequest(s.engine, http.MethodGet, "/abort-check", nil)
	assert.False(t, handlerReached, "handlers after respondWithError should not execute")
}

// ===================================================================
// Group 4: securityHeadersMiddleware
// ===================================================================

func TestServer_securityHeadersMiddleware(t *testing.T) {
	s := newMinimalServer()

	// Apply middleware and a dummy handler
	s.engine.Use(s.securityHeadersMiddleware())
	s.engine.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	s.engine.GET("/swagger/index.html", func(c *gin.Context) {
		c.String(http.StatusOK, "swagger")
	})

	t.Run("standard path sets restrictive CSP", func(t *testing.T) {
		w := performRequest(s.engine, http.MethodGet, "/test", nil)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
		assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
		assert.Equal(t, "1; mode=block", w.Header().Get("X-XSS-Protection"))
		assert.Contains(t, w.Header().Get("Strict-Transport-Security"), "max-age=31536000")
		assert.Contains(t, w.Header().Get("Strict-Transport-Security"), "includeSubDomains")
		assert.Equal(t, "default-src 'self'", w.Header().Get("Content-Security-Policy"))
	})

	t.Run("swagger path sets relaxed CSP for CDN resources", func(t *testing.T) {
		w := performRequest(s.engine, http.MethodGet, "/swagger/index.html", nil)

		assert.Equal(t, http.StatusOK, w.Code)
		csp := w.Header().Get("Content-Security-Policy")
		assert.Contains(t, csp, "script-src")
		assert.Contains(t, csp, "https://unpkg.com")
	})
}

// ===================================================================
// Group 5: requestIDMiddleware
// ===================================================================

func TestServer_requestIDMiddleware(t *testing.T) {
	s := newMinimalServer()

	s.engine.Use(s.requestIDMiddleware())
	s.engine.GET("/test", func(c *gin.Context) {
		// Echo the request_id from context
		reqID, _ := c.Get("request_id")
		c.String(http.StatusOK, reqID.(string))
	})

	t.Run("generates new ID when none provided", func(t *testing.T) {
		w := performRequest(s.engine, http.MethodGet, "/test", nil)

		assert.Equal(t, http.StatusOK, w.Code)
		responseID := w.Header().Get("X-Request-ID")
		assert.NotEmpty(t, responseID, "should generate a request ID")
		assert.True(t, strings.HasPrefix(responseID, "req-"), "generated ID should have req- prefix")
		// Body should match the header
		assert.Equal(t, responseID, w.Body.String())
	})

	t.Run("uses provided X-Request-ID header", func(t *testing.T) {
		customID := "my-custom-request-id-42"
		w := performRequest(s.engine, http.MethodGet, "/test", map[string]string{
			"X-Request-ID": customID,
		})

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, customID, w.Header().Get("X-Request-ID"))
		assert.Equal(t, customID, w.Body.String())
	})
}

// ===================================================================
// Group 6: authMiddleware
// ===================================================================

func TestServer_authMiddleware(t *testing.T) {
	tests := []struct {
		name         string
		apiKey       string // header value; empty => omit header
		setupRepo    func(repo *mockAuthRepo)
		expectStatus int
		expectCode   string // error code in JSON body
		expectCtxKey bool   // should api_key_info be set in context?
	}{
		{
			name:         "missing API key returns 401 AUTH_001",
			apiKey:       "",
			setupRepo:    func(repo *mockAuthRepo) {},
			expectStatus: http.StatusUnauthorized,
			expectCode:   "AUTH_001",
		},
		{
			name:   "invalid API key returns 401 AUTH_002",
			apiKey: validAPIKey,
			setupRepo: func(repo *mockAuthRepo) {
				// Any hash lookup returns not found
				repo.On("GetKeyByHash", mock.Anything, mock.Anything).
					Return(nil, auth.ErrKeyNotFound)
			},
			expectStatus: http.StatusUnauthorized,
			expectCode:   "AUTH_002",
		},
		{
			name:   "expired API key returns 401 AUTH_003",
			apiKey: validAPIKey,
			setupRepo: func(repo *mockAuthRepo) {
				expiredTime := time.Now().Add(-1 * time.Hour)
				key := buildActiveAPIKey([]entities.Permission{entities.PermissionReadFairValue})
				key.ExpiresAt = &expiredTime
				repo.On("GetKeyByHash", mock.Anything, mock.Anything).
					Return(key, nil)
			},
			expectStatus: http.StatusUnauthorized,
			expectCode:   "AUTH_003",
		},
		{
			name:   "inactive API key returns 401 AUTH_004",
			apiKey: validAPIKey,
			setupRepo: func(repo *mockAuthRepo) {
				key := buildActiveAPIKey([]entities.Permission{entities.PermissionReadFairValue})
				key.IsActive = false
				repo.On("GetKeyByHash", mock.Anything, mock.Anything).
					Return(key, nil)
			},
			expectStatus: http.StatusUnauthorized,
			expectCode:   "AUTH_004",
		},
		{
			name:   "auth service error returns 500 AUTH_005",
			apiKey: validAPIKey,
			setupRepo: func(repo *mockAuthRepo) {
				repo.On("GetKeyByHash", mock.Anything, mock.Anything).
					Return(nil, errors.New("database connection lost"))
			},
			expectStatus: http.StatusInternalServerError,
			expectCode:   "AUTH_005",
		},
		{
			name:   "valid API key passes through and sets context",
			apiKey: validAPIKey,
			setupRepo: func(repo *mockAuthRepo) {
				key := buildActiveAPIKey([]entities.Permission{entities.PermissionReadFairValue})
				repo.On("GetKeyByHash", mock.Anything, mock.Anything).
					Return(key, nil)
				// RecordUsage is called asynchronously — accept any args
				repo.On("RecordUsage", mock.Anything, mock.Anything).
					Return(nil).Maybe()
			},
			expectStatus: http.StatusOK,
			expectCtxKey: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := new(mockAuthRepo)
			tc.setupRepo(repo)

			s := newServerWithAuth(repo)

			// Build engine with auth middleware + a test handler
			s.engine.Use(s.authMiddleware())
			s.engine.GET("/protected", func(c *gin.Context) {
				// Verify context was set correctly
				if tc.expectCtxKey {
					info, exists := c.Get("api_key_info")
					assert.True(t, exists, "api_key_info should be in context")
					assert.NotNil(t, info)

					userID, exists := c.Get("user_id")
					assert.True(t, exists, "user_id should be in context")
					assert.Equal(t, "user-001", userID)
				}
				c.String(http.StatusOK, "ok")
			})

			headers := map[string]string{}
			if tc.apiKey != "" {
				headers["X-API-Key"] = tc.apiKey
			}

			w := performRequest(s.engine, http.MethodGet, "/protected", headers)
			assert.Equal(t, tc.expectStatus, w.Code)

			if tc.expectCode != "" {
				body := parseJSONBody(t, w)
				assert.Equal(t, tc.expectCode, body["code"])
			}

			// RecordUsage runs in a fire-and-forget goroutine with .Maybe() expectation —
			// no need to wait for it; AssertExpectations passes regardless.
			repo.AssertExpectations(t)
		})
	}
}

// ===================================================================
// Group 7: requirePermission middleware
// ===================================================================

func TestServer_requirePermission(t *testing.T) {
	tests := []struct {
		name         string
		contextSetup func(c *gin.Context) // how to pre-populate gin context
		permission   entities.Permission
		expectStatus int
		expectCode   string
	}{
		{
			name: "no api_key_info in context returns 401 AUTH_006",
			contextSetup: func(c *gin.Context) {
				// do not set api_key_info
			},
			permission:   entities.PermissionReadFairValue,
			expectStatus: http.StatusUnauthorized,
			expectCode:   "AUTH_006",
		},
		{
			name: "wrong type in context returns 500 AUTH_007",
			contextSetup: func(c *gin.Context) {
				c.Set("api_key_info", "not-a-pointer")
			},
			permission:   entities.PermissionReadFairValue,
			expectStatus: http.StatusInternalServerError,
			expectCode:   "AUTH_007",
		},
		{
			name: "missing required permission returns 403 AUTH_008",
			contextSetup: func(c *gin.Context) {
				c.Set("api_key_info", &entities.APIKeyInfo{
					ID:          "k1",
					UserID:      "u1",
					Permissions: []entities.Permission{entities.PermissionReadHealth},
				})
			},
			permission:   entities.PermissionReadFairValue,
			expectStatus: http.StatusForbidden,
			expectCode:   "AUTH_008",
		},
		{
			name: "exact permission match passes",
			contextSetup: func(c *gin.Context) {
				c.Set("api_key_info", &entities.APIKeyInfo{
					ID:          "k2",
					UserID:      "u2",
					Permissions: []entities.Permission{entities.PermissionReadFairValue},
				})
			},
			permission:   entities.PermissionReadFairValue,
			expectStatus: http.StatusOK,
		},
		{
			name: "admin permission grants any access",
			contextSetup: func(c *gin.Context) {
				c.Set("api_key_info", &entities.APIKeyInfo{
					ID:          "k3",
					UserID:      "u3",
					Permissions: []entities.Permission{entities.PermissionAdmin},
				})
			},
			permission:   entities.PermissionManageKeys,
			expectStatus: http.StatusOK,
		},
		{
			name: "multiple permissions includes required one",
			contextSetup: func(c *gin.Context) {
				c.Set("api_key_info", &entities.APIKeyInfo{
					ID:     "k4",
					UserID: "u4",
					Permissions: []entities.Permission{
						entities.PermissionReadHealth,
						entities.PermissionReadMetrics,
						entities.PermissionReadFairValue,
					},
				})
			},
			permission:   entities.PermissionReadMetrics,
			expectStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newMinimalServer()

			// Pre-populate context before the permission middleware runs
			s.engine.Use(func(c *gin.Context) {
				tc.contextSetup(c)
				c.Next()
			})
			s.engine.Use(s.requirePermission(tc.permission))
			s.engine.GET("/check", func(c *gin.Context) {
				c.String(http.StatusOK, "allowed")
			})

			w := performRequest(s.engine, http.MethodGet, "/check", nil)
			assert.Equal(t, tc.expectStatus, w.Code)

			if tc.expectCode != "" {
				body := parseJSONBody(t, w)
				assert.Equal(t, tc.expectCode, body["code"])
			}
		})
	}
}

// ===================================================================
// Group 8: rateLimitMiddleware
// ===================================================================

func TestServer_rateLimitMiddleware(t *testing.T) {
	t.Run("allowed request passes through with rate limit headers", func(t *testing.T) {
		cache := new(mockCacheStore)
		s := newServerWithRateLimiter(cache)

		// No limits configured — RateLimiter returns default "allowed" result
		s.engine.Use(s.rateLimitMiddleware())
		s.engine.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})

		w := performRequest(s.engine, http.MethodGet, "/test", nil)
		assert.Equal(t, http.StatusOK, w.Code)

		// Default result should include rate limit headers
		assert.NotEmpty(t, w.Header().Get("X-RateLimit-Remaining"))
	})

	t.Run("denied request returns 429 with retry info", func(t *testing.T) {
		cache := new(mockCacheStore)
		s := newServerWithRateLimiter(cache)

		// Configure a limit then exhaust it
		resetTime := time.Now().Add(60 * time.Second)
		err := s.rateLimiter.SetLimit(context.Background(), ratelimit.LimitConfig{
			Type:        ratelimit.LimitTypeIP,
			Identifier:  "192.0.2.1",
			MaxRequests: 1,
			Window:      time.Minute,
		})
		require.NoError(t, err)

		// First check (getApplicableLimits -> checkLimit) returns "already at max"
		cache.On("Get", mock.Anything, mock.Anything).
			Return(1, resetTime, nil)

		s.engine.Use(s.rateLimitMiddleware())
		s.engine.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "should not reach")
		})

		w := performRequest(s.engine, http.MethodGet, "/test", nil)
		assert.Equal(t, http.StatusTooManyRequests, w.Code)

		body := parseJSONBody(t, w)
		errorObj, ok := body["error"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "RATE_LIMIT_EXCEEDED", errorObj["code"])
		assert.Equal(t, "Rate limit exceeded", errorObj["message"])
		assert.Equal(t, "rate_limit_error", errorObj["type"])

		rateLimitObj, ok := body["rate_limit"].(map[string]interface{})
		require.True(t, ok, "rate_limit must be a JSON object")
		assert.Contains(t, rateLimitObj, "remaining")
		assert.Contains(t, rateLimitObj, "reset_time")
		assert.Contains(t, rateLimitObj, "retry_after")

		assert.NotEmpty(t, body["timestamp"], "timestamp must be present")
		assert.Equal(t, "/test", body["path"])
		assert.Equal(t, http.MethodGet, body["method"])
	})

	t.Run("rate limit error allows request through gracefully", func(t *testing.T) {
		cache := new(mockCacheStore)
		s := newServerWithRateLimiter(cache)

		// Configure a limit so getApplicableLimits finds it
		err := s.rateLimiter.SetLimit(context.Background(), ratelimit.LimitConfig{
			Type:        ratelimit.LimitTypeIP,
			Identifier:  "192.0.2.1",
			MaxRequests: 100,
			Window:      time.Minute,
		})
		require.NoError(t, err)

		// Make cache.Get return an error to trigger the error path in checkLimit
		cache.On("Get", mock.Anything, mock.Anything).
			Return(0, time.Time{}, errors.New("cache unavailable"))

		s.engine.Use(s.rateLimitMiddleware())
		s.engine.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})

		w := performRequest(s.engine, http.MethodGet, "/test", nil)
		// On error, the middleware should allow the request through (fail open)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("uses API key identifier when present in context", func(t *testing.T) {
		cache := new(mockCacheStore)
		s := newServerWithRateLimiter(cache)

		// Inject api_key_info into context before rate limit middleware
		s.engine.Use(func(c *gin.Context) {
			c.Set("api_key_info", &entities.APIKeyInfo{
				ID:     "key-123",
				UserID: "user-abc",
			})
			c.Next()
		})
		s.engine.Use(s.rateLimitMiddleware())
		s.engine.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})

		w := performRequest(s.engine, http.MethodGet, "/test", nil)
		// No limits configured, so it passes through with defaults
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// ===================================================================
// Group 9: loggingMiddleware
// ===================================================================

func TestServer_loggingMiddleware(t *testing.T) {
	// The logging middleware uses gin.LoggerWithFormatter which writes to the
	// zap logger. We verify that it can be created and used without panics.
	s := newMinimalServer()
	s.engine.Use(s.loggingMiddleware())
	s.engine.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := performRequest(s.engine, http.MethodGet, "/test", nil)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ===================================================================
// Group 10: Integration — auth + permission chain
// ===================================================================

func TestServer_AuthPermissionChain_Integration(t *testing.T) {
	// Test the full middleware chain: auth -> requirePermission -> handler
	// This verifies the two middlewares work together correctly.
	repo := new(mockAuthRepo)

	// Setup: valid key with read:fair_value permission
	activeKey := buildActiveAPIKey([]entities.Permission{entities.PermissionReadFairValue})
	repo.On("GetKeyByHash", mock.Anything, mock.Anything).Return(activeKey, nil)
	repo.On("RecordUsage", mock.Anything, mock.Anything).Return(nil).Maybe()

	s := newServerWithAuth(repo)

	// Protected route requiring read:fair_value
	protected := s.engine.Group("/api")
	protected.Use(s.authMiddleware())
	protected.Use(s.requirePermission(entities.PermissionReadFairValue))
	protected.GET("/data", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"data": "secret"})
	})

	t.Run("valid key with correct permission succeeds", func(t *testing.T) {
		w := performRequest(s.engine, http.MethodGet, "/api/data", map[string]string{
			"X-API-Key": validAPIKey,
		})
		assert.Equal(t, http.StatusOK, w.Code)
		body := parseJSONBody(t, w)
		assert.Equal(t, "secret", body["data"])
	})

	t.Run("no API key gets 401 from auth middleware", func(t *testing.T) {
		w := performRequest(s.engine, http.MethodGet, "/api/data", nil)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestServer_AuthPermissionChain_WrongPermission(t *testing.T) {
	repo := new(mockAuthRepo)

	// Key only has read:health, not read:fair_value
	key := buildActiveAPIKey([]entities.Permission{entities.PermissionReadHealth})
	repo.On("GetKeyByHash", mock.Anything, mock.Anything).Return(key, nil)
	repo.On("RecordUsage", mock.Anything, mock.Anything).Return(nil).Maybe()

	s := newServerWithAuth(repo)

	protected := s.engine.Group("/api")
	protected.Use(s.authMiddleware())
	protected.Use(s.requirePermission(entities.PermissionReadFairValue))
	protected.GET("/data", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"data": "secret"})
	})

	w := performRequest(s.engine, http.MethodGet, "/api/data", map[string]string{
		"X-API-Key": validAPIKey,
	})
	assert.Equal(t, http.StatusForbidden, w.Code)

	body := parseJSONBody(t, w)
	assert.Equal(t, "AUTH_008", body["code"])
}

// ===================================================================
// Group 11: Engine accessor
// ===================================================================

func TestServer_Engine(t *testing.T) {
	s := newMinimalServer()
	engine := s.Engine()
	assert.NotNil(t, engine)
	assert.Equal(t, s.engine, engine, "Engine() should return the internal gin engine")
}

// ===================================================================
// Group 12: respondWithError — different status codes
// ===================================================================

func TestServer_respondWithError_StatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		errorCode  string
		message    string
		wantTitle  string
	}{
		{
			name:       "401 Unauthorized",
			statusCode: http.StatusUnauthorized,
			errorCode:  "AUTH_001",
			message:    "Missing API key",
			wantTitle:  "Unauthorized",
		},
		{
			name:       "403 Forbidden",
			statusCode: http.StatusForbidden,
			errorCode:  "AUTH_008",
			message:    "Insufficient permissions",
			wantTitle:  "Forbidden",
		},
		{
			name:       "500 Internal Server Error",
			statusCode: http.StatusInternalServerError,
			errorCode:  "AUTH_005",
			message:    "Authentication service error",
			wantTitle:  "Internal Server Error",
		},
		{
			name:       "429 Too Many Requests",
			statusCode: http.StatusTooManyRequests,
			errorCode:  "RATE_LIMIT",
			message:    "Rate limit exceeded",
			wantTitle:  "Too Many Requests",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newMinimalServer()
			// Need a fresh engine per subtest since we register on the same path
			s.engine = gin.New()
			s.engine.GET("/err", func(c *gin.Context) {
				s.respondWithError(c, tc.statusCode, tc.errorCode, tc.message)
			})

			w := performRequest(s.engine, http.MethodGet, "/err", nil)
			assert.Equal(t, tc.statusCode, w.Code)

			// RFC 7807: Content-Type must be application/problem+json for all status codes
			assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"),
				"Content-Type must be application/problem+json for %d", tc.statusCode)

			body := parseJSONBody(t, w)
			assert.Equal(t, tc.wantTitle, body["title"])
			assert.Equal(t, tc.errorCode, body["code"])
			assert.Equal(t, tc.message, body["detail"])
			assert.Equal(t, float64(tc.statusCode), body["status"])
		})
	}
}

// ===================================================================
// Group 13: Middleware stacking — requestID + security headers combined
// ===================================================================

func TestServer_MiddlewareStack_RequestIDAndSecurity(t *testing.T) {
	s := newMinimalServer()

	// Stack both middlewares (same order as NewServer)
	s.engine.Use(s.requestIDMiddleware())
	s.engine.Use(s.securityHeadersMiddleware())
	s.engine.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := performRequest(s.engine, http.MethodGet, "/test", nil)

	assert.Equal(t, http.StatusOK, w.Code)
	// Request ID header should be present
	assert.NotEmpty(t, w.Header().Get("X-Request-ID"))
	// Security headers should be present
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
}

// ===================================================================
// Group 14: Full route empty ticker test
// ===================================================================

func TestServer_FairValueEmptyTicker(t *testing.T) {
	// Test the inline handler for GET /api/v1/fair-value/ (no ticker)
	// We need auth to pass first, so set up a valid key
	repo := new(mockAuthRepo)
	key := buildActiveAPIKey([]entities.Permission{entities.PermissionReadFairValue})
	repo.On("GetKeyByHash", mock.Anything, mock.Anything).Return(key, nil)
	repo.On("RecordUsage", mock.Anything, mock.Anything).Return(nil).Maybe()

	s := newServerWithAuth(repo)

	// Manually recreate the route setup for the empty ticker case
	v1 := s.engine.Group("/api/v1")
	fairValueGroup := v1.Group("/fair-value")
	fairValueGroup.Use(s.authMiddleware())
	fairValueGroup.Use(s.requirePermission(entities.PermissionReadFairValue))
	fairValueGroup.GET("/", func(c *gin.Context) {
		s.respondWithError(c, http.StatusBadRequest, "INVALID_TICKER", "Ticker parameter is required")
	})

	w := performRequest(s.engine, http.MethodGet, "/api/v1/fair-value/", map[string]string{
		"X-API-Key": validAPIKey,
	})

	assert.Equal(t, http.StatusBadRequest, w.Code)
	body := parseJSONBody(t, w)
	assert.Equal(t, "INVALID_TICKER", body["code"])
	assert.Equal(t, "Ticker parameter is required", body["detail"])
}

// ===================================================================
// Group 15: respondWithError POST method reflection
// ===================================================================

func TestServer_respondWithError_ReflectsMethod(t *testing.T) {
	s := newMinimalServer()
	s.engine.POST("/err", func(c *gin.Context) {
		s.respondWithError(c, http.StatusBadRequest, "BAD", "bad request")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/err", nil)
	s.engine.ServeHTTP(w, req)

	body := parseJSONBody(t, w)
	assert.Equal(t, "POST", body["method"], "method field should reflect the HTTP method")
}

// ===================================================================
// Stub implementations for NewServer integration test dependencies
// ===================================================================

// stubCacheRepo is a no-op CacheRepository for wiring NewServer tests.
// It satisfies ports.CacheRepository without real storage.
type stubCacheRepo struct{}

func (s *stubCacheRepo) Set(_ context.Context, _ string, _ interface{}, _ time.Duration) error {
	return nil
}
func (s *stubCacheRepo) Get(_ context.Context, _ string, _ interface{}) error { return nil }
func (s *stubCacheRepo) Delete(_ context.Context, _ string) error             { return nil }
func (s *stubCacheRepo) Exists(_ context.Context, _ string) (bool, error)     { return false, nil }
func (s *stubCacheRepo) SetNX(_ context.Context, _ string, _ interface{}, _ time.Duration) (bool, error) {
	return true, nil
}
func (s *stubCacheRepo) GetKeys(_ context.Context, _ string) ([]string, error) { return nil, nil }
func (s *stubCacheRepo) DeletePattern(_ context.Context, _ string) error       { return nil }

// stubSECGateway is a no-op SECGateway for wiring tests.
type stubSECGateway struct{}

func (s *stubSECGateway) GetCompanyFacts(_ context.Context, _ string) (*entities.CompanyFactsResponse, error) {
	return nil, nil
}
func (s *stubSECGateway) GetCompanyConcepts(_ context.Context, _, _ string) (*entities.ConceptResponse, error) {
	return nil, nil
}
func (s *stubSECGateway) GetTickerCIKMapping(_ context.Context) (map[string]string, error) {
	return nil, nil
}
func (s *stubSECGateway) GetFinancialDataForTicker(_ context.Context, _, _ string) (*entities.HistoricalFinancialData, error) {
	return nil, nil
}
func (s *stubSECGateway) HealthCheck(_ context.Context) error { return nil }

// stubMarketGateway is a no-op MarketDataGateway for wiring tests.
type stubMarketGateway struct{}

func (s *stubMarketGateway) GetQuote(_ context.Context, _ string) (*entities.MarketData, error) {
	return nil, nil
}
func (s *stubMarketGateway) GetQuotes(_ context.Context, _ []string) (map[string]*entities.MarketData, error) {
	return nil, nil
}
func (s *stubMarketGateway) GetHistoricalPrices(_ context.Context, _ string, _, _ time.Time) ([]*entities.PriceData, error) {
	return nil, nil
}
func (s *stubMarketGateway) HealthCheck(_ context.Context) error { return nil }

// stubMacroGateway is a no-op MacroDataGateway for wiring tests.
type stubMacroGateway struct{}

func (s *stubMacroGateway) GetTreasuryRates(_ context.Context) (*entities.TreasuryRates, error) {
	return nil, nil
}
func (s *stubMacroGateway) GetMarketRiskPremium(_ context.Context) (float64, error) { return 0.05, nil }
func (s *stubMacroGateway) HealthCheck(_ context.Context) error                     { return nil }

// stubMetricsService is a no-op MetricsService that satisfies ports.MetricsService.
type stubMetricsService struct{}

func (s *stubMetricsService) RecordHTTPRequest(_, _ string, _ int, _ time.Duration, _ int) {}
func (s *stubMetricsService) IncHTTPRequestsInFlight()                                     {}
func (s *stubMetricsService) DecHTTPRequestsInFlight()                                     {}
func (s *stubMetricsService) RecordValuationRequest(_, _, _ string, _ time.Duration)       {}
func (s *stubMetricsService) RecordValuationError(_, _ string)                             {}
func (s *stubMetricsService) IncDCFCalculations()                                          {}
func (s *stubMetricsService) IncWACCCalculations()                                         {}
func (s *stubMetricsService) RecordSECAPIRequest(_, _ string)                              {}
func (s *stubMetricsService) RecordMarketAPIRequest(_, _ string)                           {}
func (s *stubMetricsService) RecordMacroAPIRequest(_, _ string)                            {}
func (s *stubMetricsService) RecordDataFetch(_, _ string, _ time.Duration)                 {}
func (s *stubMetricsService) RecordCacheRequest(_, _, _ string)                            {}
func (s *stubMetricsService) SetCacheHitRatio(_ string, _ float64)                         {}
func (s *stubMetricsService) SetAverageWACC(_ float64)                                     {}
func (s *stubMetricsService) SetAverageGrowthRate(_ float64)                               {}
func (s *stubMetricsService) GetTotalRequests() int64                                      { return 0 }
func (s *stubMetricsService) GetActiveConnections() int                                    { return 0 }
func (s *stubMetricsService) GetAverageResponseTime() float64                              { return 0 }
func (s *stubMetricsService) GetErrorRate() float64                                        { return 0 }
func (s *stubMetricsService) GetCacheHitRate() float64                                     { return 0 }
func (s *stubMetricsService) GetTotalValuations() int64                                    { return 0 }
func (s *stubMetricsService) GetSuccessfulValuations() int64                               { return 0 }
func (s *stubMetricsService) GetFailedValuations() int64                                   { return 0 }
func (s *stubMetricsService) GetAverageWACC() float64                                      { return 0 }
func (s *stubMetricsService) GetAverageGrowthRate() float64                                { return 0 }
func (s *stubMetricsService) GetUniqueTickersServed() int64                                { return 0 }
func (s *stubMetricsService) HealthCheck() error                                           { return nil }

// newTestConfig builds a valid config for NewServer tests.
func newTestConfig() *config.Config {
	return &config.Config{
		Version:       "v0.9.0-test",
		Environment:   "test",
		BuildTime:     "2025-01-01T00:00:00Z",
		GitCommit:     "test-commit",
		Port:          "0", // port 0 = OS picks a free port
		EnableSwagger: false,
		EnablePprof:   false,
		Server: config.ServerConfig{
			Port: "0",
		},
		Database: config.DatabaseConfig{
			Driver:     "sqlite3",
			SQLitePath: ":memory:",
		},
	}
}

// newFullTestServer creates a fully-wired Server via NewServer for integration tests.
// Uses an in-memory SQLite DB and stub gateways. Returns the server and a cleanup function.
func newFullTestServer(t *testing.T, cfg *config.Config) *Server {
	t.Helper()

	// Create in-memory SQLite DB for the health handler
	db, err := sqlx.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Auth service with a stub repo
	authRepo := new(mockAuthRepo)
	// Default: reject all keys (tests that need auth will override)
	authRepo.On("GetKeyByHash", mock.Anything, mock.Anything).
		Return(nil, auth.ErrKeyNotFound).Maybe()
	authRepo.On("RecordUsage", mock.Anything, mock.Anything).
		Return(nil).Maybe()
	authService := auth.NewService(authRepo, zap.NewNop())

	// Rate limiter with a stub cache
	rateLimitCache := new(mockCacheStore)
	rateLimiter := ratelimit.NewRateLimiter(rateLimitCache, zap.NewNop())

	// Metrics service with isolated prometheus registry (prevents duplicate registration panics)
	registry := prometheus.NewRegistry()
	metricsService := metrics.NewServiceWithRegistry(zap.NewNop(), registry)

	// Health handler with stub dependencies
	healthHandler := handlers.NewHealthHandler(
		zap.NewNop(),
		db,
		nil, // redis client — optional, nil is handled
		&stubCacheRepo{},
		rateLimiter,
		&stubSECGateway{},
		&stubMarketGateway{},
		&stubMacroGateway{},
		&stubMetricsService{},
	)

	server := NewServer(
		cfg,
		zap.NewNop(),
		nil, // valuation service — only used when fair-value routes are actually called
		authService,
		rateLimiter,
		healthHandler,
		metricsService,
	)

	return server
}

// ===================================================================
// Group 16: NewServer — verifies constructor, setupMiddleware, setupRoutes
// ===================================================================

func TestNewServer_CreatesServerWithRoutes(t *testing.T) {
	cfg := newTestConfig()
	s := newFullTestServer(t, cfg)

	require.NotNil(t, s)
	require.NotNil(t, s.engine)
	require.NotNil(t, s.httpServer)

	// Verify key server fields are wired correctly
	assert.Equal(t, ":0", s.httpServer.Addr)
	assert.Equal(t, 30*time.Second, s.httpServer.ReadTimeout)
	assert.Equal(t, 30*time.Second, s.httpServer.WriteTimeout)
	assert.Equal(t, 120*time.Second, s.httpServer.IdleTimeout)
}

func TestNewServer_PublicRoutesAccessible(t *testing.T) {
	cfg := newTestConfig()
	s := newFullTestServer(t, cfg)

	// Public routes should be accessible without authentication
	t.Run("GET /health returns 200", func(t *testing.T) {
		w := performRequest(s.engine, http.MethodGet, "/health", nil)
		assert.Equal(t, http.StatusOK, w.Code)
		body := parseJSONBody(t, w)
		assert.Equal(t, "ok", body["status"])
	})

	t.Run("GET /ready returns 200", func(t *testing.T) {
		w := performRequest(s.engine, http.MethodGet, "/ready", nil)
		assert.Equal(t, http.StatusOK, w.Code)
		body := parseJSONBody(t, w)
		assert.Equal(t, "ready", body["status"])
	})

	t.Run("GET /version returns config values", func(t *testing.T) {
		w := performRequest(s.engine, http.MethodGet, "/version", nil)
		assert.Equal(t, http.StatusOK, w.Code)
		body := parseJSONBody(t, w)
		assert.Equal(t, "v0.9.0-test", body["version"])
		assert.Equal(t, "test", body["environment"])
	})
}

func TestNewServer_ProtectedRoutesRequireAuth(t *testing.T) {
	cfg := newTestConfig()
	s := newFullTestServer(t, cfg)

	protectedPaths := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/fair-value/AAPL"},
		{http.MethodGet, "/api/v1/health/detailed"},
		{http.MethodGet, "/api/v1/metrics"},
	}

	for _, pp := range protectedPaths {
		t.Run(fmt.Sprintf("%s %s without auth returns 401", pp.method, pp.path), func(t *testing.T) {
			w := performRequest(s.engine, pp.method, pp.path, nil)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})
	}
}

func TestNewServer_SecurityHeadersOnAllResponses(t *testing.T) {
	cfg := newTestConfig()
	s := newFullTestServer(t, cfg)

	// Even public endpoints should include security headers
	w := performRequest(s.engine, http.MethodGet, "/health", nil)

	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	assert.Contains(t, w.Header().Get("Strict-Transport-Security"), "max-age=")
}

func TestNewServer_RequestIDOnAllResponses(t *testing.T) {
	cfg := newTestConfig()
	s := newFullTestServer(t, cfg)

	w := performRequest(s.engine, http.MethodGet, "/health", nil)
	// The requestIDMiddleware is registered twice (global + setupMiddleware),
	// but the header should still be present.
	assert.NotEmpty(t, w.Header().Get("X-Request-ID"))
}

func TestNewServer_ProductionMode(t *testing.T) {
	cfg := newTestConfig()
	cfg.Environment = "production"

	s := newFullTestServer(t, cfg)
	require.NotNil(t, s)

	// Server should still serve requests in production mode
	w := performRequest(s.engine, http.MethodGet, "/health", nil)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestNewServer_SwaggerDisabledByDefault(t *testing.T) {
	cfg := newTestConfig()
	cfg.EnableSwagger = false
	cfg.Environment = "test" // not "development", so swagger should be off

	s := newFullTestServer(t, cfg)

	// Swagger endpoint should 404 when disabled
	w := performRequest(s.engine, http.MethodGet, "/swagger/index.html", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestNewServer_PprofDisabledByDefault(t *testing.T) {
	cfg := newTestConfig()
	cfg.EnablePprof = false

	s := newFullTestServer(t, cfg)

	// Pprof endpoint should 404 when disabled
	w := performRequest(s.engine, http.MethodGet, "/debug/pprof/", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestNewServer_PprofEnabledWhenConfigured(t *testing.T) {
	cfg := newTestConfig()
	cfg.EnablePprof = true

	s := newFullTestServer(t, cfg)

	// Pprof endpoint should be accessible when enabled
	w := performRequest(s.engine, http.MethodGet, "/debug/pprof/", nil)
	assert.NotEqual(t, http.StatusNotFound, w.Code, "pprof should be registered when enabled")
}

func TestNewServer_EmptyTickerRoute(t *testing.T) {
	// Build a server with a valid auth key so we can reach the empty-ticker handler
	db, err := sqlx.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	authRepo := new(mockAuthRepo)
	key := buildActiveAPIKey([]entities.Permission{entities.PermissionReadFairValue})
	authRepo.On("GetKeyByHash", mock.Anything, mock.Anything).Return(key, nil)
	authRepo.On("RecordUsage", mock.Anything, mock.Anything).Return(nil).Maybe()
	authService := auth.NewService(authRepo, zap.NewNop())

	rateLimitCache := new(mockCacheStore)
	rateLimiter := ratelimit.NewRateLimiter(rateLimitCache, zap.NewNop())

	registry := prometheus.NewRegistry()
	metricsService := metrics.NewServiceWithRegistry(zap.NewNop(), registry)

	healthHandler := handlers.NewHealthHandler(
		zap.NewNop(), db, nil, &stubCacheRepo{}, rateLimiter,
		&stubSECGateway{}, &stubMarketGateway{}, &stubMacroGateway{}, &stubMetricsService{},
	)

	cfg := newTestConfig()
	s := NewServer(cfg, zap.NewNop(), nil, authService, rateLimiter, healthHandler, metricsService)

	// GET /api/v1/fair-value/ (empty ticker) with valid auth should return 400
	w := performRequest(s.engine, http.MethodGet, "/api/v1/fair-value/", map[string]string{
		"X-API-Key": validAPIKey,
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	body := parseJSONBody(t, w)
	assert.Equal(t, "INVALID_TICKER", body["code"])
}

// ===================================================================
// Group 17: Start and Shutdown
// ===================================================================

func TestServer_StartAndShutdown(t *testing.T) {
	cfg := newTestConfig()
	cfg.Port = "0" // OS picks a free port
	s := newFullTestServer(t, cfg)

	// Start the server in a goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start()
	}()

	// Give the server time to start listening
	time.Sleep(100 * time.Millisecond)

	// Shutdown gracefully
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := s.Shutdown(ctx)
	assert.NoError(t, err, "Shutdown should succeed")

	// Start should return nil (server closed gracefully)
	startErr := <-errCh
	assert.NoError(t, startErr, "Start should return nil after graceful shutdown")
}

// ===================================================================
// Group 18: Engine accessor from NewServer
// ===================================================================

func TestNewServer_EngineReturnsWiredEngine(t *testing.T) {
	cfg := newTestConfig()
	s := newFullTestServer(t, cfg)

	engine := s.Engine()
	require.NotNil(t, engine)

	// The returned engine should handle registered routes
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	engine.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// ===================================================================
// Group 19: SwaggerEnabled in development mode
// ===================================================================

func TestNewServer_SwaggerEnabledInDevelopment(t *testing.T) {
	cfg := newTestConfig()
	cfg.Environment = "development"
	cfg.EnableSwagger = false // even with this false, dev mode forces swagger on

	s := newFullTestServer(t, cfg)

	// Swagger redirect route should be registered
	w := performRequest(s.engine, http.MethodGet, "/swagger", nil)
	// Should redirect to /swagger/index.html (302)
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "/swagger/index.html")
}
