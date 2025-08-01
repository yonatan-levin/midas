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
)

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
