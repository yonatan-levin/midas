package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"regexp"
	"runtime/debug"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/api/middleware"
	"github.com/midas/dcf-valuation-api/internal/api/v1/handlers"
	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
	"github.com/midas/dcf-valuation-api/internal/observability/narrate"
	"github.com/midas/dcf-valuation-api/internal/services/auth"
	"github.com/midas/dcf-valuation-api/internal/services/metrics"
	"github.com/midas/dcf-valuation-api/internal/services/ratelimit"
	"github.com/midas/dcf-valuation-api/internal/services/valuation"
)

// (No embedded spec; serving from filesystem path when available)

// requestIDValidator is a precompiled regex that validates an incoming
// X-Request-ID header value. The allowed character set is intentionally
// conservative to prevent log-injection and header-injection attacks.
// Consumed by requestIDMiddleware to decide whether to accept or
// replace a client-supplied request ID.
//
// This is an immutable sentinel (compiled once at package init), not mutable
// state. The project's "no globals" rule targets mutable state managed
// through DI, not precompiled constants.
//
//nolint:gochecknoglobals // immutable precompiled regex; not mutable state
var requestIDValidator = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

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

	// Phase 2.B post-launch (REVIEWER MEDIUM-1): emit a Warn during boot when
	// an operator set logging.artifact_store.triggers.quality_flag_threshold
	// to an unknown value (typo, unsupported severity). config.Load already
	// normalised case + whitespace, so anything still unrecognised here is a
	// genuine misconfiguration that would silently disable the trigger.
	// Surfacing it on the boot log turns a multi-day post-incident mystery
	// into a five-second log search.
	config.ValidateArtifactTriggers(cfg.Logging.ArtifactStore.Triggers, logger)

	// Setup all middleware in setupMiddleware (single source of truth for the chain)
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

// setupMiddleware configures the global middleware chain.
// Order is intentional:
//  1. requestIDMiddleware  — must be first: injects request_id + child logger into context
//  2. traceMiddleware       — opens narrate.Emitter (always) + artifact.Bundle (when ?trace=1)
//     and attaches both to the request context. Runs AFTER requestID
//     because the bundle directory uses request_id as its name.
//  3. securityHeadersMiddleware — sets security headers early so all responses carry them
//  4. metrics.HTTPMetricsMiddleware — records latency/status counts
//  5. accessLogMiddleware — OUTSIDE CustomRecovery so it always emits its line, even on panics.
//     Because it wraps CustomRecovery, its c.Next() returns normally after recovery; by the
//     time the deferred access log runs, the status is already 500 (set by panicHandler).
//  6. gin.CustomRecovery — catches panics from handlers; logs with request_id; returns 500.
//     Registered AFTER accessLog so it is on the INSIDE of the access log wrapper.
//  7. CORS
//  8. rateLimitMiddleware
func (s *Server) setupMiddleware() {
	// 1. Request correlation — injects request_id into context logger; runs first
	s.engine.Use(s.requestIDMiddleware())

	// 2. Trace middleware — narrate.Emitter (always) + artifact.Bundle (when opt-in flag).
	//    No-op for bundle when logging.artifact_store.enabled=false; narrate emitter still
	//    attaches to ctx so downstream code can call narrate.From(ctx).Emit unconditionally.
	s.engine.Use(middleware.TraceMiddleware(
		narrate.Config{
			Enabled:      s.config.Logging.Narrate.Enabled,
			SampleRate:   s.config.Logging.Narrate.SampleRate,
			RedactFields: s.config.Logging.Narrate.RedactFields,
		},
		artifact.Config{
			Enabled:         s.config.Logging.ArtifactStore.Enabled,
			RootPath:        s.config.Logging.ArtifactStore.RootPath,
			RetentionDays:   s.config.Logging.ArtifactStore.RetentionDays,
			MaxTotalBytes:   s.config.Logging.ArtifactStore.MaxTotalBytes,
			QueueSize:       s.config.Logging.ArtifactStore.QueueSize,
			PendingBytesCap: s.config.Logging.ArtifactStore.PendingBytesCap,
			Triggers: artifact.TriggerConfig{
				OnError:              s.config.Logging.ArtifactStore.Triggers.OnError,
				QualityFlagThreshold: s.config.Logging.ArtifactStore.Triggers.QualityFlagThreshold,
				// Phase 2.C — always-on knob. Wired here so operators can flip
				// LOGGING_ARTIFACT_STORE_TRIGGERS_ALWAYS=true at runtime for a
				// debugging session without touching the trace middleware.
				Always: s.config.Logging.ArtifactStore.Triggers.Always,
			},
			GitSHA:       s.config.GitCommit,
			BuildVersion: s.config.Version,
			// RPL-9 (capture side): stamp the resolved valuation + macro
			// config subset into every bundle as `00-config.json`. Closes
			// the bug class where replay-side hardcoded defaults silently
			// diverged from production viper defaults (cycles 1+2+3 of the
			// replay-fidelity debug). Captured here — at the boot-time
			// trace middleware construction site — because viper has
			// already resolved env vars + YAML overlays at this point, so
			// what we mirror into the bundle is the EXACT effective config
			// the request-path code will read. Subset chosen per the
			// tracker (RPL-9): algorithmically load-bearing fields only;
			// runtime knobs (cache TTLs, server timeouts) are excluded
			// because they don't affect valuation math.
			ConfigSnapshot: artifact.ConfigSnapshot{
				Valuation: artifact.ValuationConfigSnapshot{
					DefaultMarketRiskPremium: s.config.Valuation.DefaultMarketRiskPremium,
					DefaultTerminalGrowthCap: s.config.Valuation.DefaultTerminalGrowthCap,
					DefaultTaxRate:           s.config.Valuation.DefaultTaxRate,
					MinDataPointsForGrowth:   s.config.Valuation.MinDataPointsForGrowth,
					DCFProjectionYears:       s.config.Valuation.DCFProjectionYears,
					DCFMaxGrowthRate:         s.config.Valuation.DCFMaxGrowthRate,
					DCFMinGrowthRate:         s.config.Valuation.DCFMinGrowthRate,
					DCFIterationTolerance:    s.config.Valuation.DCFIterationTolerance,
					DCFMaxIterations:         s.config.Valuation.DCFMaxIterations,
				},
				Macro: artifact.MacroConfigSnapshot{
					ManualRiskFreeRate:      s.config.Macro.ManualRiskFreeRate,
					ManualMarketRiskPremium: s.config.Macro.ManualMarketRiskPremium,
				},
			},
		},
		// BUG-012: pass the PLAIN singleton logger so the bundle can emit an
		// at-most-once runtime Warn on buffer drops / oversize lines / write
		// errors. NOT the BundleSink-wrapped request logger (re-entry risk).
		s.logger,
	))

	// 3. Security headers on every response
	s.engine.Use(s.securityHeadersMiddleware())

	// 4. Prometheus metrics (records HTTP request latency/status counts)
	s.engine.Use(metrics.HTTPMetricsMiddleware(s.metricsService, s.logger))

	// 5. Structured access log — registered BEFORE CustomRecovery so that when a panic
	// occurs, CustomRecovery catches it (sets status 500), then returns normally to
	// accessLogMiddleware's c.Next() call, which then emits the access line with
	// the correct 500 status.
	s.engine.Use(s.accessLogMiddleware())

	// 6. Panic recovery — CustomRecovery logs with request_id from logctx; runs INSIDE
	// the access log middleware wrapper so panics are caught before accessLog reads the status.
	s.engine.Use(gin.CustomRecovery(s.panicHandler))

	// 7. CORS
	s.engine.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"}, // TODO: Configure appropriately for production
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Content-Length", "Accept-Encoding", "X-CSRF-Token", "Authorization", "X-API-Key", "X-Request-ID", "X-Midas-Trace"},
		ExposeHeaders:    []string{"Content-Length", "X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// 8. Rate limiting (applied globally; uses api_key_info from context if present)
	s.engine.Use(s.rateLimitMiddleware())
}

// setupRoutes configures all routes for the application
func (s *Server) setupRoutes() {
	// Temporary startup config echo for debugging (non-request-path, uses s.logger directly)
	s.logger.Info("Startup config",
		zap.Bool("enable_swagger", s.config.EnableSwagger),
		zap.String("db_driver", s.config.Database.Driver),
		zap.String("db_sqlite_path", s.config.Database.SQLitePath),
	)
	// Health check endpoints (no authentication required)
	s.engine.GET("/health", s.healthCheck)
	s.engine.GET("/ready", s.readinessCheck)
	s.engine.GET("/version", s.versionInfo)

	// Prometheus metrics endpoint (no authentication required for monitoring).
	// Serves the service-owned registry (PREX-1) so Midas metrics are surfaced
	// without colliding with Prometheus's default Go-runtime collectors.
	s.engine.GET("/metrics", gin.WrapH(promhttp.HandlerFor(
		s.metricsService.GetRegistry(),
		promhttp.HandlerOpts{},
	)))

	// Optional pprof endpoints for performance profiling (dev/staging only)
	if s.config.EnablePprof {
		// Use stdlib pprof handler mounting via http.DefaultServeMux
		// Expose under /debug/pprof/* for standard tooling
		s.engine.GET("/debug/pprof/*any", gin.WrapH(http.DefaultServeMux))
	}

	// API v1 routes
	v1 := s.engine.Group("/api/v1")

	// Create handlers
	fairValueHandler := handlers.NewFairValueHandler(s.valuationService, s.logger)

	// Fair value endpoints (protected)
	fairValueGroup := v1.Group("/fair-value")
	fairValueGroup.Use(s.authMiddleware())                                    // Apply authentication to this group
	fairValueGroup.Use(s.requirePermission(entities.PermissionReadFairValue)) // Require fair value permission
	{
		// Handle empty ticker case — use respondWithError for RFC 7807 consistency
		fairValueGroup.GET("/", func(c *gin.Context) {
			s.respondWithError(c, http.StatusBadRequest, "INVALID_TICKER", "Ticker parameter is required")
		})
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

	// Documentation endpoints (if Swagger/OpenAPI is enabled)
	// Always serve Swagger/OpenAPI in development environment; otherwise honor toggle
	serveSwagger := s.config.EnableSwagger
	if s.config.Environment == "development" {
		serveSwagger = true
	}
	if serveSwagger {
		// Serve the static OpenAPI spec for external tools (e.g., Schemathesis)
		// In containers, the file exists at /app/docs; in local dev at ./docs
		if _, err := os.Stat("docs/openapi.yaml"); err == nil {
			s.engine.StaticFile("/docs/openapi.yaml", "docs/openapi.yaml")
		} else if _, err := os.Stat("/app/docs/openapi.yaml"); err == nil {
			s.engine.StaticFile("/docs/openapi.yaml", "/app/docs/openapi.yaml")
		} else {
			s.logger.Warn("OpenAPI spec not available; /docs/openapi.yaml will 404")
		}

		// Serve generated Swagger JSON for Swagger UI
		if _, err := os.Stat("docs/swagger.json"); err == nil {
			s.engine.StaticFile("/docs/swagger.json", "docs/swagger.json")
		} else if _, err := os.Stat("/app/docs/swagger.json"); err == nil {
			s.engine.StaticFile("/docs/swagger.json", "/app/docs/swagger.json")
		} else {
			s.logger.Warn("Swagger JSON not available; /docs/swagger.json will 404")
		}

		// Swagger UI using gin-swagger middleware with custom URL
		url := ginSwagger.URL("/docs/swagger.json") // Point to our custom endpoint
		s.engine.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler, url))
		s.engine.GET("/swagger", func(c *gin.Context) { c.Redirect(http.StatusFound, "/swagger/index.html") })

		s.logger.Info("Swagger UI available at /swagger/index.html")
	}

	// ----- NEW AUTH ROUTES -----
	authHandler := handlers.NewAuthHandler(s.authService, s.logger)
	authGroup := v1.Group("/auth")
	authGroup.Use(s.authMiddleware())
	authGroup.Use(s.requirePermission(entities.PermissionManageKeys))
	{
		authGroup.POST("/keys", authHandler.CreateAPIKey)
	}
}

// Engine returns the underlying Gin engine (useful for tests)
func (s *Server) Engine() *gin.Engine {
	return s.engine
}

// Middleware implementations

// requestIDMiddleware is the single, canonical implementation for request correlation.
// It runs first in the middleware chain and:
//  1. Reads X-Request-ID from the incoming request.
//  2. Trusts it if non-empty and isValidRequestID passes; otherwise generates a UUID v4.
//  3. Echoes the ID back to the client via X-Request-ID response header.
//  4. Stores the ID in the gin context (backward-compat key "request_id").
//  5. Creates a child logger enriched with request_id and injects it into
//     c.Request.Context() via logctx.Inject, so all downstream log sites
//     (handlers, services, access log) share the same correlation ID.
func (s *Server) requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader("X-Request-ID")
		// Validate client-supplied ID — reject empty or unsafe values
		if rid == "" || !isValidRequestID(rid) {
			rid = generateRequestID()
		}

		// Echo the final ID back to the client
		c.Header("X-Request-ID", rid)

		// Backward-compat: store as gin context value for any code reading c.Get("request_id")
		c.Set("request_id", rid)

		// Build a child logger carrying request_id and inject into the request context.
		// All downstream middleware and handlers should use logctx.From(c.Request.Context())
		// so they automatically inherit this field.
		child := s.logger.With(zap.String("request_id", rid))
		ctx := logctx.Inject(c.Request.Context(), child)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

// accessLogMiddleware emits one structured access-log line per request, after
// the handler has returned (so it captures the final status code, latency, and
// any error_code set by respondWithError or panicHandler).
//
// Paths listed in cfg.Logging.AccessLogSkipPaths (default: /metrics, /health,
// /ready) are suppressed at Info level to reduce monitoring noise. They are
// still emitted at Debug level so power users can see them via log-level config.
//
// The logger is read from the context AFTER c.Next() so it picks up any
// enrichments added by downstream middleware (e.g. user_id / key_id from auth).
func (s *Server) accessLogMiddleware() gin.HandlerFunc {
	// Build skip-set once at middleware init time, not per request
	skipPaths := make(map[string]struct{}, len(s.config.Logging.AccessLogSkipPaths))
	for _, p := range s.config.Logging.AccessLogSkipPaths {
		skipPaths[p] = struct{}{}
	}

	return func(c *gin.Context) {
		start := time.Now()

		// Run the full handler chain.
		// CustomRecovery is registered AFTER (inner to) accessLogMiddleware, so any panic
		// from a handler is caught by CustomRecovery before it propagates back here.
		// This means c.Next() always returns normally, with the correct final status code.
		c.Next()

		// Read the enriched logger AFTER c.Next() — this captures any user_id/key_id
		// that auth middleware may have injected into c.Request.Context() during the request.
		logger := logctx.From(c.Request.Context())
		path := c.Request.URL.Path

		// Normalize bytes_out: c.Writer.Size() returns -1 when nothing was written
		bytesOut := max(c.Writer.Size(), 0)

		// Normalize route: c.FullPath() returns "" for unmatched requests (404);
		// use a sentinel so log consumers can distinguish "matched empty route"
		// from "no route matched".
		route := c.FullPath()
		if route == "" {
			route = "(unmatched)"
		}

		// Core access log fields
		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("route", route),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
			zap.String("client_ip", c.ClientIP()),
			zap.String("user_agent", c.Request.UserAgent()),
			zap.Int("bytes_out", bytesOut),
		}

		// Include error_code if respondWithError set it (used by downstream error analysis)
		if errCode := c.GetString("error_code"); errCode != "" {
			fields = append(fields, zap.String("error_code", errCode))
		}

		// Suppress info-level for skip-listed paths (probes, scrape endpoints)
		if _, skip := skipPaths[path]; skip {
			logger.Debug("access", fields...)
			return
		}

		logger.Info("access", fields...)
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
			// Log with request-scoped logger so the line carries request_id
			logctx.From(c.Request.Context()).Error("Rate limit check failed", zap.Error(err))
			// Allow request on error to prevent outage, but log for investigation
			c.Next()
			return
		}

		// Add rate limit headers
		headers := s.rateLimiter.GetRateLimitHeaders(result)
		for key, value := range headers {
			c.Header(key, value)
		}

		// Tier-1 narrate: ratelimit.checked. Emitted on every request that
		// reaches the limiter so the per-request story carries the bucket and
		// remaining/limit counters regardless of allow/deny.
		rlOutcome := narrate.OutcomeOK
		rlNotes := ""
		if !result.Allowed {
			rlOutcome = narrate.OutcomeError
			rlNotes = "limit exceeded"
		}
		narrate.From(c.Request.Context()).Emit(c.Request.Context(),
			narrate.PhaseRateLimitChecked, rlOutcome, rlNotes,
			zap.String("bucket", string(limitType)),
			zap.Int("remaining", result.Remaining),
		)

		if !result.Allowed {
			// Log with request-scoped logger so the line carries request_id
			logctx.From(c.Request.Context()).Warn("Rate limit exceeded",
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

		// Allow Swagger UI resources from CDN
		if c.Request.URL.Path == "/swagger/index.html" || c.Request.URL.Path == "/swagger/*any" {
			c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' https://unpkg.com; style-src 'self' 'unsafe-inline' https://unpkg.com; img-src 'self' data:; font-src 'self' https://unpkg.com")
		} else {
			c.Header("Content-Security-Policy", "default-src 'self'")
		}
		c.Next()
	}
}

// panicHandler is the gin.CustomRecovery callback. It:
//  1. Logs the panic value and full stack trace at Error level via the
//     request-scoped logger (carries request_id automatically).
//  2. Responds with a generic RFC 7807 500 error — the panic message is
//     intentionally not exposed to the client so internals never leak.
func (s *Server) panicHandler(c *gin.Context, err any) {
	// Emit structured error with stack trace, correlated to the request
	logctx.From(c.Request.Context()).Error("panic recovered",
		zap.Any("panic", err),
		zap.ByteString("stack", debug.Stack()),
	)

	// Respond with a generic 500 — do NOT include panic details in the response
	s.respondWithError(c, http.StatusInternalServerError, "INTERNAL", "internal server error")
}

// authMiddleware provides API key authentication.
// After successful validation it enriches the context logger with user_id and
// key_id so all downstream log lines (handlers, services, access log) inherit
// those fields automatically.
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
			// Log with request-scoped logger so the line carries request_id
			logctx.From(c.Request.Context()).Warn("API key validation failed",
				zap.Error(err),
				zap.String("key_prefix", s.safeKeyPrefix(apiKey)),
				zap.String("ip", c.ClientIP()),
			)

			// Tier-1 narrate: auth.resolved with outcome=error so the per-
			// request story shows the auth attempt and its failure.
			narrate.From(c.Request.Context()).Emit(c.Request.Context(),
				narrate.PhaseAuthResolved, narrate.OutcomeError, err.Error(),
				zap.String("auth_source", "header:X-API-Key"),
			)

			// Determine specific error response
			switch {
			case errors.Is(err, auth.ErrKeyNotFound):
				s.respondWithError(c, http.StatusUnauthorized, "AUTH_002", "Invalid API key")
			case errors.Is(err, auth.ErrKeyExpired):
				s.respondWithError(c, http.StatusUnauthorized, "AUTH_003", "API key has expired")
			case errors.Is(err, auth.ErrKeyInactive):
				s.respondWithError(c, http.StatusUnauthorized, "AUTH_004", "API key is inactive")
			default:
				s.respondWithError(c, http.StatusInternalServerError, "AUTH_005", "Authentication service error")
			}
			return
		}

		// Store key information in context for later use by permission checks and rate limiter
		c.Set("api_key_info", keyInfo)
		c.Set("user_id", keyInfo.UserID)

		// R.4.1: Enrich the context logger with user_id and key_id so that all
		// downstream log lines — in handlers, services, and the access log —
		// automatically carry these fields without extra plumbing.
		ctx := c.Request.Context()
		enriched := logctx.From(ctx).With(
			zap.String("user_id", keyInfo.UserID),
			zap.String("key_id", keyInfo.ID),
		)
		c.Request = c.Request.WithContext(logctx.Inject(ctx, enriched))

		// Capture a correlated child logger before spawning the goroutine.
		// Reads the just-enriched context so request_id, user_id, and key_id
		// are all inherited from the parent — do NOT re-add them here.
		reqLogger := logctx.From(c.Request.Context()).With(
			zap.String("async_task", "record_usage"),
		)

		// Record usage asynchronously (don't block request).
		// ResponseStatus / ResponseTimeMs are recorded as zero — the async path
		// fires before the handler completes, and there is no post-response
		// hook wired to back-fill these. Accepted tradeoff: RecordUsage captures
		// the key+endpoint+IP for billing/audit; response attributes come from
		// Prometheus metrics instead.
		go func() {
			asyncCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			err := s.authService.RecordUsage(asyncCtx, keyInfo.ID, entities.UsageRecord{
				Endpoint:       c.Request.URL.Path,
				ResponseStatus: 0,
				ResponseTimeMs: 0,
				UserAgent:      c.Request.UserAgent(),
				IPAddress:      c.ClientIP(),
			})

			if err != nil {
				reqLogger.Error("Failed to record API usage", zap.Error(err))
			}
		}()

		// Log successful auth with the now-enriched request-scoped logger
		logctx.From(c.Request.Context()).Debug("API key authenticated successfully",
			zap.Int("permissions", len(keyInfo.Permissions)),
		)

		// Tier-1 narrate: auth.resolved with outcome=ok. Carries key_id and
		// permission count so the per-request story identifies which API key
		// authenticated the call.
		narrate.From(c.Request.Context()).Emit(c.Request.Context(),
			narrate.PhaseAuthResolved, narrate.OutcomeOK, "",
			zap.String("key_id", keyInfo.ID),
			zap.Int("permissions", len(keyInfo.Permissions)),
			zap.String("auth_source", "header:X-API-Key"),
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
			// Log with request-scoped logger so the line carries request_id, user_id, key_id
			logctx.From(c.Request.Context()).Warn("Insufficient permissions",
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

// respondWithError sends a standardized RFC 7807 Problem Details error response.
// It also stores the error code in the gin context so accessLogMiddleware can
// include it in the access log line without re-parsing the response body.
//
// Uses the typed handlers.ErrorResponse struct (the same one fair_value.go
// sendError uses) so the two error paths emit byte-identical JSON shapes.
// Pre-D2-follow-through this function passed a raw time.Time which json
// marshalled as RFC3339Nano (variable nanosecond precision); now both paths
// emit RFC3339 second precision matching the CONTRACTS.md example.
func (s *Server) respondWithError(c *gin.Context, statusCode int, errorCode, message string) {
	// Make error_code available to accessLogMiddleware for structured access logging
	c.Set("error_code", errorCode)

	c.Header("Content-Type", "application/problem+json")
	c.JSON(statusCode, handlers.ErrorResponse{
		Type:      "https://problems.midas.dev/" + errorCode,
		Title:     http.StatusText(statusCode),
		Status:    statusCode,
		Detail:    message,
		Instance:  c.Request.URL.Path,
		Code:      errorCode,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Method:    c.Request.Method,
	})
	c.Abort()
}

// Helper functions

// generateRequestID generates a cryptographically random, globally unique
// request identifier using UUID v4 (RFC 4122).
// The returned string is a standard UUID in hyphenated form, e.g.:
//
//	"550e8400-e29b-41d4-a716-446655440000"
//
// requestIDMiddleware uses this when no valid X-Request-ID header is provided.
func generateRequestID() string {
	return uuid.NewString()
}

// isValidRequestID reports whether s is a safe, non-empty request ID that can
// be accepted from an X-Request-ID header and propagated through the system.
//
// The validator enforces:
//   - Non-empty string
//   - Maximum length of 128 characters (prevents header-size abuse)
//   - Only alphanumeric characters plus ".", "_", ":", "-"
//     (excludes whitespace, control characters, and other injection vectors)
func isValidRequestID(s string) bool {
	return requestIDValidator.MatchString(s)
}
