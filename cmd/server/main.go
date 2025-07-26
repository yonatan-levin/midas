package main

import (
	"context"
	"fmt"
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

	// Initialize logger
	logger, err := initLogger(cfg.LogLevel)
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Sync()

	logger.Info("Starting DCF Valuation API Server",
		zap.String("version", cfg.Version),
		zap.String("port", cfg.Port),
		zap.String("environment", cfg.Environment))

	// Create Fx application with dependency injection
	app := fx.New(
		// Provide configuration and logger
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *zap.Logger { return logger }),

		// Include DI container module
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
		logger.Fatal("Failed to start application", zap.Error(err))
	}

	logger.Info("DCF Valuation API Server started successfully",
		zap.String("address", fmt.Sprintf(":%s", cfg.Port)))

	// Wait for shutdown signal
	<-sigChan
	logger.Info("Shutdown signal received, gracefully shutting down...")

	// Stop the application with timeout
	stopCtx, stopCancel := context.WithTimeout(ctx, 30*time.Second)
	defer stopCancel()

	if err := app.Stop(stopCtx); err != nil {
		logger.Error("Error during shutdown", zap.Error(err))
	}

	logger.Info("Server shutdown completed")
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

// initLogger initializes the structured logger based on log level
func initLogger(logLevel string) (*zap.Logger, error) {
	var config zap.Config

	switch logLevel {
	case "debug":
		config = zap.NewDevelopmentConfig()
		config.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "info":
		config = zap.NewProductionConfig()
		config.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	case "warn":
		config = zap.NewProductionConfig()
		config.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		config = zap.NewProductionConfig()
		config.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		config = zap.NewProductionConfig()
		config.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	// Customize output format for better readability
	config.Encoding = "json"
	config.OutputPaths = []string{"stdout"}
	config.ErrorOutputPaths = []string{"stderr"}

	// Add caller information in development
	if logLevel == "debug" {
		config.Development = true
		config.DisableCaller = false
		config.DisableStacktrace = false
	} else {
		config.Development = false
		config.DisableCaller = true
		config.DisableStacktrace = true
	}

	return config.Build()
}
