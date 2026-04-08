package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// ---- Mock for AuthKeyManager interface ----

// mockAuthService implements AuthKeyManager for unit testing.
type mockAuthService struct {
	mock.Mock
}

func (m *mockAuthService) CreateKey(
	ctx context.Context,
	userID string,
	permissions []entities.Permission,
) (*entities.APIKey, error) {
	args := m.Called(ctx, userID, permissions)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.APIKey), args.Error(1)
}

// ---- Tests for CreateAPIKey ----

func TestAuthHandler_CreateAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Fixed timestamp for deterministic assertions
	fixedTime := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		body       string // raw JSON request body
		setupMock  func(m *mockAuthService)
		wantStatus int
		wantBody   func(t *testing.T, body []byte)
	}{
		{
			name: "success_creates_key",
			body: `{"user_id":"user-123","permissions":["read:fair_value","read:health"]}`,
			setupMock: func(m *mockAuthService) {
				m.On("CreateKey", mock.Anything, "user-123", []entities.Permission{
					entities.PermissionReadFairValue,
					entities.PermissionReadHealth,
				}).Return(&entities.APIKey{
					ID:          "key-abc",
					Key:         "dcf_rawkey123",
					UserID:      "user-123",
					Permissions: []entities.Permission{entities.PermissionReadFairValue, entities.PermissionReadHealth},
					RateLimit:   1000,
					IsActive:    true,
					CreatedAt:   fixedTime,
				}, nil)
			},
			wantStatus: http.StatusCreated,
			wantBody: func(t *testing.T, body []byte) {
				var resp map[string]interface{}
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "key-abc", resp["id"])
				assert.Equal(t, "dcf_rawkey123", resp["key"])
				assert.Equal(t, "user-123", resp["user_id"])
				assert.Equal(t, float64(1000), resp["rate_limit"])

				// Verify permissions array
				perms, ok := resp["permissions"].([]interface{})
				require.True(t, ok)
				assert.Len(t, perms, 2)
			},
		},
		{
			name:       "invalid_request_missing_user_id",
			body:       `{"permissions":["read:fair_value"]}`,
			setupMock:  func(m *mockAuthService) {},
			wantStatus: http.StatusBadRequest,
			wantBody: func(t *testing.T, body []byte) {
				var resp map[string]interface{}
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "invalid_request", resp["error"])
			},
		},
		{
			name:       "invalid_request_missing_permissions",
			body:       `{"user_id":"user-123"}`,
			setupMock:  func(m *mockAuthService) {},
			wantStatus: http.StatusBadRequest,
			wantBody: func(t *testing.T, body []byte) {
				var resp map[string]interface{}
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "invalid_request", resp["error"])
			},
		},
		{
			name:       "invalid_request_empty_body",
			body:       `{}`,
			setupMock:  func(m *mockAuthService) {},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid_request_malformed_json",
			body:       `{not json}`,
			setupMock:  func(m *mockAuthService) {},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "service_error_returns_500",
			body: `{"user_id":"user-456","permissions":["read:fair_value"]}`,
			setupMock: func(m *mockAuthService) {
				m.On("CreateKey", mock.Anything, "user-456", []entities.Permission{
					entities.Permission("read:fair_value"),
				}).Return(nil, errors.New("database write failed"))
			},
			wantStatus: http.StatusInternalServerError,
			wantBody: func(t *testing.T, body []byte) {
				var resp map[string]interface{}
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "internal_error", resp["error"])
				assert.Contains(t, resp["message"], "database write failed")
			},
		},
		{
			name: "single_permission",
			body: `{"user_id":"admin-1","permissions":["admin:all"]}`,
			setupMock: func(m *mockAuthService) {
				m.On("CreateKey", mock.Anything, "admin-1", []entities.Permission{
					entities.PermissionAdmin,
				}).Return(&entities.APIKey{
					ID:          "key-admin",
					Key:         "dcf_adminkey999",
					UserID:      "admin-1",
					Permissions: []entities.Permission{entities.PermissionAdmin},
					RateLimit:   1000,
					IsActive:    true,
					CreatedAt:   fixedTime,
				}, nil)
			},
			wantStatus: http.StatusCreated,
			wantBody: func(t *testing.T, body []byte) {
				var resp map[string]interface{}
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "admin-1", resp["user_id"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSvc := new(mockAuthService)
			tt.setupMock(mockSvc)
			handler := NewAuthHandler(mockSvc, zap.NewNop())

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/api/v1/auth/keys", strings.NewReader(tt.body))
			c.Request.Header.Set("Content-Type", "application/json")

			handler.CreateAPIKey(c)

			assert.Equal(t, tt.wantStatus, w.Code)

			if tt.wantBody != nil {
				tt.wantBody(t, w.Body.Bytes())
			}

			mockSvc.AssertExpectations(t)
		})
	}
}
