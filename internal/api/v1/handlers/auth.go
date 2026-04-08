package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// AuthKeyManager abstracts the auth service so handlers depend on an interface
// rather than a concrete type, following clean architecture.
// *auth.Service satisfies this interface implicitly.
type AuthKeyManager interface {
	CreateKey(ctx context.Context, userID string, permissions []entities.Permission) (*entities.APIKey, error)
}

// AuthHandler provides endpoints for API key management.
// It intentionally depends only on the auth service to respect clean-arch boundaries.
// All permission checks are handled by the outer middleware in server.go.
type AuthHandler struct {
	authService AuthKeyManager
	logger      *zap.Logger
}

// NewAuthHandler constructs an AuthHandler instance.
func NewAuthHandler(authService AuthKeyManager, logger *zap.Logger) *AuthHandler {
	return &AuthHandler{
		authService: authService,
		logger:      logger,
	}
}

// createKeyRequest defines the expected JSON payload for creating an API key.
type createKeyRequest struct {
	UserID      string   `json:"user_id" binding:"required"`
	Permissions []string `json:"permissions" binding:"required"`
}

// CreateAPIKey handles POST /auth/keys and returns the newly created key (raw) once.
// Requires PermissionManageKeys (enforced by router middleware).
func (h *AuthHandler) CreateAPIKey(c *gin.Context) {
	var req createKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": err.Error(),
		})
		return
	}

	// Convert permissions to domain type and validate.
	perms := make([]entities.Permission, 0, len(req.Permissions))
	for _, p := range req.Permissions {
		perm := entities.Permission(p)
		perms = append(perms, perm)
	}

	apiKey, err := h.authService.CreateKey(c.Request.Context(), req.UserID, perms)
	if err != nil {
		h.logger.Error("failed to create API key", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_error",
			"message": err.Error(),
		})
		return
	}

	// Only expose the raw key once in the response.
	c.JSON(http.StatusCreated, gin.H{
		"id":          apiKey.ID,
		"key":         apiKey.Key,
		"user_id":     apiKey.UserID,
		"permissions": apiKey.Permissions,
		"rate_limit":  apiKey.RateLimit,
		"created_at":  apiKey.CreatedAt,
		"expires_at":  apiKey.ExpiresAt,
	})
}
