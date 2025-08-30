package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	sqliteRepo "github.com/midas/dcf-valuation-api/internal/infra/repositories/sqlite"
	"github.com/midas/dcf-valuation-api/internal/services/auth"
	"go.uber.org/zap"
)

// This CLI creates a demo API key directly in the database and prints it.
func main() {
	var dbPath string
	flag.StringVar(&dbPath, "db", "./data/midas.db", "Path to SQLite database file")
	flag.Parse()

	if err := os.MkdirAll("./data", 0o755); err != nil {
		log.Fatalf("failed to ensure data dir: %v", err)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("failed to open sqlite db %s: %v", dbPath, err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS api_keys (
    id TEXT PRIMARY KEY,
    key_hash TEXT NOT NULL UNIQUE,
    user_id TEXT NOT NULL,
    permissions TEXT NOT NULL,
    rate_limit INTEGER DEFAULT 1000,
    expires_at TIMESTAMP,
    is_active BOOLEAN DEFAULT 1,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_used_at TIMESTAMP,
    usage_count INTEGER DEFAULT 0
);`); err != nil {
		log.Fatalf("failed to ensure api_keys table: %v", err)
	}

	repo := sqliteRepo.NewAuthRepository(db)
	logger, _ := zap.NewDevelopment()
	svc := auth.NewService(repo, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key, err := svc.CreateKey(ctx, "demo-user", []entities.Permission{entities.PermissionReadFairValue})
	if err != nil {
		log.Fatalf("failed to create demo key: %v", err)
	}

	fmt.Printf("DEMO_API_KEY=%s\n", key.Key)
}
