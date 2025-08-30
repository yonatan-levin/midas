package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/fx"
	"go.uber.org/zap"

	// Import SQLite driver
	_ "github.com/mattn/go-sqlite3"
	"github.com/midas/dcf-valuation-api/internal/api"
	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/di"
	// Import generated docs (will be created after swag init)
	// TODO: Enable after fixing module dependencies in container
	// _ "github.com/midas/dcf-valuation-api/docs"
)

// @title           DCF Valuation API
// @version         1.0
// @description     DCF (Discounted Cash Flow) Valuation API provides intrinsic value calculations for publicly traded companies.
// @description     The API computes Net Tangible Asset Value and DCF Fair Value per share using SEC filings and market data.
// @termsOfService  https://midas.dev/terms

// @contact.name   API Support
// @contact.url    https://midas.dev/support
// @contact.email  support@midas.dev

// @license.name  Apache 2.0
// @license.url   http://www.apache.org/licenses/LICENSE-2.0.html

// @host      localhost:8080
// @BasePath  /api/v1

// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-API-Key
// @description API key required for authentication. Include this in the X-API-Key header.

// @externalDocs.description  OpenAPI Specification
// @externalDocs.url          /docs/openapi.yaml

func main() {
	// Initialize configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	log.Printf("Starting DCF Valuation API Server (version: %s, port: %s, environment: %s)",
		cfg.Version, cfg.Port, cfg.Environment)

	// Create Fx application with dependency injection
	app := fx.New(
		// Provide configuration
		fx.Provide(func() *config.Config { return cfg }),

		// Include DI container module (includes logger creation)
		di.Module,

		// Provide HTTP server
		fx.Provide(api.NewServer),

		// Register lifecycle hooks
		fx.Invoke(registerHooks),
	)

	// Handle graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start the application
	startCtx, startCancel := context.WithTimeout(ctx, 30*time.Second)
	defer startCancel()

	if err := app.Start(startCtx); err != nil {
		log.Fatalf("Failed to start application: %v", err)
	}

	// Wait for shutdown signal
	<-sigChan
	log.Println("Shutdown signal received, gracefully shutting down...")

	// Stop the application with timeout
	stopCtx, stopCancel := context.WithTimeout(ctx, 30*time.Second)
	defer stopCancel()

	if err := app.Stop(stopCtx); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}

	log.Println("Server shutdown completed")
}

// registerHooks registers application lifecycle hooks
func registerHooks(lc fx.Lifecycle, server *api.Server, logger *zap.Logger) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			logger.Info("Starting HTTP server...")

			// Start server in goroutine to avoid blocking
			go func() {
				if err := server.Start(); err != nil && err != http.ErrServerClosed {
					logger.Fatal("HTTP server failed to start", zap.Error(err))
				}
			}()

			// Give server time to start
			time.Sleep(100 * time.Millisecond)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			logger.Info("Stopping HTTP server...")
			return server.Shutdown(ctx)
		},
	})
}
