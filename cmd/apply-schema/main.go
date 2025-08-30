package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// apply-schema loads internal/infra/database/schema.sql and applies it to the provided DB.
func main() {
	var dbPath string
	var schemaPath string
	flag.StringVar(&dbPath, "db", "./data/midas.db", "Path to SQLite database file")
	flag.StringVar(&schemaPath, "schema", "internal/infra/database/schema.sql", "Path to schema SQL file")
	flag.Parse()

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "failed to ensure data dir: %v\n", err)
		os.Exit(1)
	}

	// Open SQLite database
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open db: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	bytes, err := os.ReadFile(schemaPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read schema: %v\n", err)
		os.Exit(1)
	}

	if _, err := db.Exec(string(bytes)); err != nil {
		fmt.Fprintf(os.Stderr, "failed to apply schema: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Applied schema to %s using %s\n", dbPath, schemaPath)
}
