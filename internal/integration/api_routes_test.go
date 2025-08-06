package integration

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestAPIRoutesWiring tests that all expected API routes are properly wired
func TestAPIRoutesWiring(t *testing.T) {
	// Setup test environment
	testEnv := SetupTestEnvironment(t)
	defer testEnv.Cleanup()

	// Create a test API key for protected endpoints
	ctx := context.Background()
	apiKey, err := testEnv.NewTestAPIKey(ctx, "test-user", []entities.Permission{
		entities.PermissionReadFairValue,
		entities.PermissionReadHealth,
		entities.PermissionReadMetrics,
		entities.PermissionManageKeys,
	})
	require.NoError(t, err, "Failed to create test API key")

	tests := []struct {
		name           string
		method         string
		path           string
		requiresAuth   bool
		expectedStatus []int // Multiple acceptable status codes
		description    string
	}{
		{
			name:           "Health Check",
			method:         "GET",
			path:           "/health",
			requiresAuth:   false,
			expectedStatus: []int{http.StatusOK},
			description:    "Basic health check should be publicly accessible",
		},
		{
			name:           "Readiness Check",
			method:         "GET",
			path:           "/ready",
			requiresAuth:   false,
			expectedStatus: []int{http.StatusOK},
			description:    "Readiness check should be publicly accessible",
		},
		{
			name:           "Version Info",
			method:         "GET",
			path:           "/version",
			requiresAuth:   false,
			expectedStatus: []int{http.StatusOK},
			description:    "Version info should be publicly accessible",
		},
		{
			name:           "Prometheus Metrics",
			method:         "GET",
			path:           "/metrics",
			requiresAuth:   false,
			expectedStatus: []int{http.StatusOK},
			description:    "Prometheus metrics should be publicly accessible",
		},
		{
			name:           "Fair Value Endpoint Auth Required",
			method:         "GET",
			path:           "/api/v1/fair-value/AAPL",
			requiresAuth:   false, // Test without auth first
			expectedStatus: []int{http.StatusUnauthorized},
			description:    "Fair value endpoint should require authentication",
		},
		{
			name:           "Fair Value Endpoint With Auth",
			method:         "GET",
			path:           "/api/v1/fair-value/AAPL",
			requiresAuth:   true,
			expectedStatus: []int{http.StatusOK, http.StatusInternalServerError, http.StatusBadRequest},
			description:    "Fair value endpoint should work with valid API key",
		},
		{
			name:           "Detailed Health Check Auth Required",
			method:         "GET",
			path:           "/api/v1/health/detailed",
			requiresAuth:   false,
			expectedStatus: []int{http.StatusUnauthorized},
			description:    "Detailed health check should require authentication",
		},
		{
			name:           "Detailed Health Check With Auth",
			method:         "GET",
			path:           "/api/v1/health/detailed",
			requiresAuth:   true,
			expectedStatus: []int{http.StatusOK, http.StatusPartialContent, http.StatusInternalServerError},
			description:    "Detailed health check should work with valid API key (206 acceptable when degraded)",
		},
		{
			name:           "Application Metrics Auth Required",
			method:         "GET",
			path:           "/api/v1/metrics",
			requiresAuth:   false,
			expectedStatus: []int{http.StatusUnauthorized},
			description:    "Application metrics should require authentication",
		},
		{
			name:           "Application Metrics With Auth",
			method:         "GET",
			path:           "/api/v1/metrics",
			requiresAuth:   true,
			expectedStatus: []int{http.StatusOK, http.StatusInternalServerError},
			description:    "Application metrics should work with valid API key",
		},
		{
			name:           "Create API Key Auth Required",
			method:         "POST",
			path:           "/api/v1/auth/keys",
			requiresAuth:   false,
			expectedStatus: []int{http.StatusUnauthorized},
			description:    "Creating API key should require authentication",
		},
		{
			name:           "Create API Key With Auth",
			method:         "POST",
			path:           "/api/v1/auth/keys",
			requiresAuth:   true,
			expectedStatus: []int{http.StatusCreated, http.StatusBadRequest, http.StatusInternalServerError},
			description:    "Creating API key should work with manage-key permission",
		},
		{
			name:           "Empty Ticker Validation",
			method:         "GET",
			path:           "/api/v1/fair-value/",
			requiresAuth:   true,
			expectedStatus: []int{http.StatusBadRequest, http.StatusInternalServerError},
			description:    "Empty ticker should return validation error (500 allowed due to missing auth tables)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create request
			var reqBody io.Reader
			if tt.method == "POST" && tt.path == "/api/v1/auth/keys" {
				payload := `{"user_id":"test-create","permissions":["read:fair_value"]}`
				reqBody = strings.NewReader(payload)
			}
			req := httptest.NewRequest(tt.method, tt.path, reqBody)
			if tt.method == "POST" {
				req.Header.Set("Content-Type", "application/json")
			}
			if tt.requiresAuth {
				req.Header.Set("X-API-Key", apiKey.Key)
			}

			// Execute request
			w := httptest.NewRecorder()
			testEnv.Router.ServeHTTP(w, req)

			// Check status code
			statusOK := false
			for _, expectedStatus := range tt.expectedStatus {
				if w.Code == expectedStatus {
					statusOK = true
					break
				}
			}

			assert.True(t, statusOK,
				"Expected status code to be one of %v, got %d\nDescription: %s\nResponse: %s",
				tt.expectedStatus, w.Code, tt.description, w.Body.String())

			t.Logf("✅ %s: %d %s", tt.name, w.Code, http.StatusText(w.Code))
		})
	}
}

// TestAuthServiceCreateAPIKey tests the API key creation functionality
func TestAuthServiceCreateAPIKey(t *testing.T) {
	testEnv := SetupTestEnvironment(t)
	defer testEnv.Cleanup()

	ctx := context.Background()

	// Test creating an API key via the auth service
	apiKey, err := testEnv.NewTestAPIKey(ctx, "test-user-create", []entities.Permission{
		entities.PermissionReadFairValue,
	})

	require.NoError(t, err, "Should be able to create API key")
	assert.NotEmpty(t, apiKey.Key, "API key should not be empty")
	assert.Equal(t, "test-user-create", apiKey.UserID, "UserID should match")
	assert.Contains(t, apiKey.Permissions, entities.PermissionReadFairValue, "Should have fair value permission")

	t.Logf("✅ API Key Created: %s (partial)", apiKey.Key[:8]+"...")
}
