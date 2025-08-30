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
		if err := applySQL(db, f); err != nil {
			fmt.Fprintf(os.Stderr, "apply migration %s: %v\n", f, err)
			os.Exit(1)
		}
		fmt.Printf("✅ Applied migration: %s\n", filepath.Base(f))
	}

	fmt.Printf("✅ Migrations complete for %s\n", dbPath)
}
