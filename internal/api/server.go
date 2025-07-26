package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/api/v1/handlers"
	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/auth"
	"github.com/midas/dcf-valuation-api/internal/services/metrics"
	"github.com/midas/dcf-valuation-api/internal/services/ratelimit"
	"github.com/midas/dcf-valuation-api/internal/services/valuation"
)

// Server represents the HTTP server
type Server struct {
	httpServer       *http.Server
	engine           *gin.Engine
	config           *config.Config
	logger           *zap.Logger
	valuationService *valuation.Service
	authService      *auth.Service
	rateLimiter      *ratelimit.RateLimiter
	healthHandler    *handlers.HealthHandler
	metricsService   *metrics.Service
}

// NewServer creates a new HTTP server instance
func NewServer(
	cfg *config.Config,
	logger *zap.Logger,
	valuationService *valuation.Service,
	authService *auth.Service,
	rateLimiter *ratelimit.RateLimiter,
	healthHandler *handlers.HealthHandler,
	metricsService *metrics.Service,
) *Server {
	// Set Gin mode based on environment
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}

	// Create Gin engine
	engine := gin.New()

	// Create server instance
	server := &Server{
		engine:           engine,
		config:           cfg,
		logger:           logger,
		valuationService: valuationService,
		authService:      authService,
		rateLimiter:      rateLimiter,
		healthHandler:    healthHandler,
		metricsService:   metricsService,
	}

	// Setup middleware
	server.setupMiddleware()

	// Setup routes
	server.setupRoutes()

	// Create HTTP server
	server.httpServer = &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      engine,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return server
}

// Start starts the HTTP server
func (s *Server) Start() error {
	s.logger.Info("Starting HTTP server", zap.String("address", s.httpServer.Addr))

	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}

	return nil
}

// Shutdown gracefully shuts down the HTTP server
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down HTTP server...")

	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown HTTP server: %w", err)
	}

	s.logger.Info("HTTP server shut down successfully")
	return nil
}

// setupMiddleware configures middleware for the Gin engine
func (s *Server) setupMiddleware() {
	// Request ID middleware
	s.engine.Use(func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = generateRequestID()
		}
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	})

	// Metrics middleware - should be early in the chain
	s.engine.Use(metrics.HTTPMetricsMiddleware(s.metricsService, s.logger))

	// Recovery middleware
	s.engine.Use(gin.Recovery())

	// Logging middleware
	s.engine.Use(s.loggingMiddleware())

	// CORS middleware
	s.engine.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"}, // TODO: Configure appropriately for production
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Content-Length", "Accept-Encoding", "X-CSRF-Token", "Authorization", "X-API-Key", "X-Request-ID"},
		ExposeHeaders:    []string{"Content-Length", "X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// Rate limiting middleware (applied globally)
	s.engine.Use(s.rateLimitMiddleware())
}

// setupRoutes configures all routes for the application
func (s *Server) setupRoutes() {
	// Health check endpoints (no authentication required)
	s.engine.GET("/health", s.healthCheck)
	s.engine.GET("/ready", s.readinessCheck)
	s.engine.GET("/version", s.versionInfo)

	// Prometheus metrics endpoint (no authentication required for monitoring)
	s.engine.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// API v1 routes
	v1 := s.engine.Group("/api/v1")

	// Create handlers
	fairValueHandler := handlers.NewFairValueHandler(s.valuationService, s.logger)

	// Fair value endpoints (protected)
	fairValueGroup := v1.Group("/fair-value")
	fairValueGroup.Use(s.authMiddleware())                                    // Apply authentication to this group
	fairValueGroup.Use(s.requirePermission(entities.PermissionReadFairValue)) // Require fair value permission
	{
		fairValueGroup.GET("/:ticker", fairValueHandler.GetFairValue)
		fairValueGroup.POST("/bulk", fairValueHandler.GetBulkFairValue)
	}

	// Health endpoints (protected)
	healthGroup := v1.Group("/health")
	healthGroup.Use(s.authMiddleware())
	healthGroup.Use(s.requirePermission(entities.PermissionReadHealth))
	{
		healthGroup.GET("/detailed", s.healthHandler.DetailedHealthCheck) // Changed to use s.healthHandler
	}

	// Metrics endpoints (protected)
	metricsGroup := v1.Group("/metrics")
	metricsGroup.Use(s.authMiddleware())
	metricsGroup.Use(s.requirePermission(entities.PermissionReadMetrics))
	{
		metricsGroup.GET("", s.healthHandler.GetMetrics) // Changed to use s.healthHandler
	}

	// Documentation endpoints (if Swagger is enabled)
	if s.config.EnableSwagger {
		// TODO: Add Swagger/OpenAPI documentation endpoints
		s.logger.Info("Swagger documentation will be available at /docs")
	}
}

// Middleware implementations

// loggingMiddleware provides structured request logging
func (s *Server) loggingMiddleware() gin.HandlerFunc {
	return gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		s.logger.Info("HTTP Request",
			zap.String("method", param.Method),
			zap.String("path", param.Path),
			zap.Int("status", param.StatusCode),
			zap.Duration("latency", param.Latency),
			zap.String("client_ip", param.ClientIP),
			zap.String("user_agent", param.Request.UserAgent()),
		)
		return ""
	})
}

// requestIDMiddleware adds a unique request ID to each request
func (s *Server) requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = generateRequestID()
		}
		c.Header("X-Request-ID", requestID)
		c.Set("request_id", requestID)
		c.Next()
	}
}

// rateLimitMiddleware implements rate limiting
func (s *Server) rateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get API key info from context (set by auth middleware)
		var identifier string
		var limitType ratelimit.LimitType

		if apiKeyInfo, exists := c.Get("api_key_info"); exists {
			if keyInfo, ok := apiKeyInfo.(*entities.APIKeyInfo); ok {
				identifier = keyInfo.ID
				limitType = ratelimit.LimitTypeAPIKey
			}
		}

		// If no API key, use IP-based rate limiting
		if identifier == "" {
			identifier = c.ClientIP()
			limitType = ratelimit.LimitTypeIP
		}

		// Check rate limit
		result, err := s.rateLimiter.AllowRequest(c.Request.Context(), ratelimit.RateLimitRequest{
			Identifier: identifier,
			Type:       limitType,
			IPAddress:  c.ClientIP(),
			Endpoint:   c.Request.URL.Path,
			UserAgent:  c.Request.UserAgent(),
		})

		if err != nil {
			s.logger.Error("Rate limit check failed", zap.Error(err))
			// Allow request on error to prevent outage, but log for investigation
			c.Next()
			return
		}

		// Add rate limit headers
		headers := s.rateLimiter.GetRateLimitHeaders(result)
		for key, value := range headers {
			c.Header(key, value)
		}

		if !result.Allowed {
			s.logger.Warn("Rate limit exceeded",
				zap.String("identifier", identifier),
				zap.String("type", string(limitType)),
				zap.String("ip", c.ClientIP()),
				zap.String("endpoint", c.Request.URL.Path),
			)

			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{
					"code":    "RATE_LIMIT_EXCEEDED",
					"message": "Rate limit exceeded",
					"type":    "rate_limit_error",
				},
				"rate_limit": gin.H{
					"remaining":   result.Remaining,
					"reset_time":  result.ResetTime.Unix(),
					"retry_after": int(result.RetryAfter.Seconds()),
				},
				"timestamp": time.Now().UTC(),
				"path":      c.Request.URL.Path,
				"method":    c.Request.Method,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// securityHeadersMiddleware adds security headers
func (s *Server) securityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Header("Content-Security-Policy", "default-src 'self'")
		c.Next()
	}
}

// authMiddleware provides API key authentication
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get API key from header
		apiKey := c.GetHeader("X-API-Key")
		if apiKey == "" {
			s.respondWithError(c, http.StatusUnauthorized, "AUTH_001", "Missing API key")
			return
		}

		// Validate API key using auth service
		keyInfo, err := s.authService.ValidateKey(c.Request.Context(), apiKey)
		if err != nil {
			s.logger.Warn("API key validation failed",
				zap.Error(err),
				zap.String("key_prefix", s.safeKeyPrefix(apiKey)),
				zap.String("ip", c.ClientIP()),
			)

			// Determine specific error response
			switch err {
			case auth.ErrKeyNotFound:
				s.respondWithError(c, http.StatusUnauthorized, "AUTH_002", "Invalid API key")
			case auth.ErrKeyExpired:
				s.respondWithError(c, http.StatusUnauthorized, "AUTH_003", "API key has expired")
			case auth.ErrKeyInactive:
				s.respondWithError(c, http.StatusUnauthorized, "AUTH_004", "API key is inactive")
			default:
				s.respondWithError(c, http.StatusInternalServerError, "AUTH_005", "Authentication service error")
			}
			return
		}

		// Store key information in context for later use
		c.Set("api_key_info", keyInfo)
		c.Set("user_id", keyInfo.UserID)

		// Record usage asynchronously (don't block request)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			err := s.authService.RecordUsage(ctx, keyInfo.ID, entities.UsageRecord{
				Endpoint:       c.Request.URL.Path,
				ResponseStatus: 0, // Will be updated in response middleware
				ResponseTimeMs: 0, // Will be calculated
				UserAgent:      c.Request.UserAgent(),
				IPAddress:      c.ClientIP(),
			})

			if err != nil {
				s.logger.Error("Failed to record API usage", zap.Error(err))
			}
		}()

		s.logger.Debug("API key authenticated successfully",
			zap.String("user_id", keyInfo.UserID),
			zap.String("key_id", keyInfo.ID),
			zap.Int("permissions", len(keyInfo.Permissions)),
		)

		c.Next()
	}
}

// requirePermission middleware checks if the authenticated key has required permission
func (s *Server) requirePermission(permission entities.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		keyInfo, exists := c.Get("api_key_info")
		if !exists {
			s.respondWithError(c, http.StatusUnauthorized, "AUTH_006", "No authentication information")
			return
		}

		apiKeyInfo, ok := keyInfo.(*entities.APIKeyInfo)
		if !ok {
			s.respondWithError(c, http.StatusInternalServerError, "AUTH_007", "Invalid authentication information")
			return
		}

		// Check if key has required permission
		hasPermission := false
		for _, p := range apiKeyInfo.Permissions {
			if p == permission || p == entities.PermissionAdmin {
				hasPermission = true
				break
			}
		}

		if !hasPermission {
			s.logger.Warn("Insufficient permissions",
				zap.String("user_id", apiKeyInfo.UserID),
				zap.String("required_permission", string(permission)),
				zap.Strings("user_permissions", s.permissionsToStrings(apiKeyInfo.Permissions)),
			)
			s.respondWithError(c, http.StatusForbidden, "AUTH_008", "Insufficient permissions")
			return
		}

		c.Next()
	}
}

// Health check handlers

// healthCheck provides basic health status
func (s *Server) healthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"timestamp": time.Now().UTC(),
		"service":   "dcf-valuation-api",
	})
}

// readinessCheck checks if the service is ready to handle requests
func (s *Server) readinessCheck(c *gin.Context) {
	// TODO: Check database connectivity, external service health, etc.
	c.JSON(http.StatusOK, gin.H{
		"status":    "ready",
		"timestamp": time.Now().UTC(),
		"checks": gin.H{
			"database":      "ok",
			"external_apis": "ok",
			"cache":         "ok",
		},
	})
}

// versionInfo provides version information
func (s *Server) versionInfo(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"version":     s.config.Version,
		"environment": s.config.Environment,
		"build_time":  s.config.BuildTime,
		"git_commit":  s.config.GitCommit,
	})
}

// Helper methods

// safeKeyPrefix returns a safe prefix of the API key for logging
func (s *Server) safeKeyPrefix(key string) string {
	if len(key) < 8 {
		return "***"
	}
	return key[:4] + "***"
}

// permissionsToStrings converts Permission slice to string slice
func (s *Server) permissionsToStrings(permissions []entities.Permission) []string {
	result := make([]string, len(permissions))
	for i, p := range permissions {
		result[i] = string(p)
	}
	return result
}

// respondWithError sends a standardized error response
func (s *Server) respondWithError(c *gin.Context, statusCode int, errorCode, message string) {
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"code":    errorCode,
			"message": message,
			"type":    "authentication_error",
		},
		"timestamp": time.Now().UTC(),
		"path":      c.Request.URL.Path,
		"method":    c.Request.Method,
	})
	c.Abort()
}

// Helper functions

// generateRequestID generates a unique request ID
func generateRequestID() string {
	// TODO: Implement proper UUID generation
	return fmt.Sprintf("req-%d", time.Now().UnixNano())
}
