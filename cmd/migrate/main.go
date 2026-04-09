package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

func applySQL(db *sql.DB, path string) error {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if _, err := db.Exec(string(bytes)); err != nil {
		return fmt.Errorf("exec %s: %w", path, err)
	}
	return nil
}

// applyMigration applies a migration SQL file, tolerating "duplicate column name"
// errors that occur when ALTER TABLE ADD COLUMN runs on a database where
// schema.sql already defined those columns (fresh DB case).
func applyMigration(db *sql.DB, path string) error {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	// Split into individual statements for granular error handling
	for _, stmt := range strings.Split(string(bytes), ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" || strings.HasPrefix(stmt, "--") {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			if strings.Contains(err.Error(), "duplicate column name") {
				continue // Column already exists — schema.sql created it
			}
			return fmt.Errorf("exec %s: %w", path, err)
		}
	}
	return nil
}

func main() {
	var dbPath string
	var schemaPath string
	var migrationsDir string
	flag.StringVar(&dbPath, "db", "./data/midas.db", "SQLite database path")
	flag.StringVar(&schemaPath, "schema", "internal/infra/database/schema.sql", "Schema SQL path")
	flag.StringVar(&migrationsDir, "migrations", "./migrations", "Migrations directory")
	flag.Parse()

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "failed to ensure data dir: %v\n", err)
		os.Exit(1)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	// Apply base schema
	if err := applySQL(db, schemaPath); err != nil {
		fmt.Fprintf(os.Stderr, "apply schema: %v\n", err)
		os.Exit(1)
	}

	entries, _ := os.ReadDir(migrationsDir)
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".sql") {
			files = append(files, filepath.Join(migrationsDir, name))
		}
	}
	sort.Strings(files)
	for _, f := range files {
		if err := applyMigration(db, f); err != nil {
			fmt.Fprintf(os.Stderr, "apply migration %s: %v\n", f, err)
			os.Exit(1)
		}
		fmt.Printf("✅ Applied migration: %s\n", filepath.Base(f))
	}

	fmt.Printf("✅ Migrations complete for %s\n", dbPath)

	// Print the demo API key so users know how to authenticate.
	// This key is seeded by 0001_seed_demo_key.sql with full permissions.
	fmt.Println("")
	fmt.Println("🔑 Demo API key (admin, full permissions):")
	fmt.Println("   dcf_demo_3a4a5b6c7d8e9f00112233445566778899aabbccddeeff001122334455667788")
	fmt.Println("   Use with: -H \"X-API-Key: dcf_demo_3a4a5b6c7d8e9f00112233445566778899aabbccddeeff001122334455667788\"")
}
