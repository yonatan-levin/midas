//go:build tools

package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Schema for test database
const testSchema = `
-- Create tables for performance testing
CREATE TABLE IF NOT EXISTS financial_data (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ticker TEXT NOT NULL,
    filing_date DATE NOT NULL,
    period_type TEXT NOT NULL,
    fiscal_year INTEGER NOT NULL,
    fiscal_quarter INTEGER,
    net_income REAL,
    revenue REAL,
    assets REAL,
    liabilities REAL,
    shareholders_equity REAL,
    diluted_shares_outstanding REAL,
    interest_expense REAL,
    income_tax_expense REAL,
    operating_income REAL,
    cost_of_revenue REAL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS market_data (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ticker TEXT NOT NULL,
    price REAL NOT NULL,
    market_cap REAL,
    beta REAL,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS macro_data (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    data_type TEXT NOT NULL,
    value REAL NOT NULL,
    date DATE NOT NULL,
    source TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS ticker_mappings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ticker TEXT UNIQUE NOT NULL,
    company_name TEXT,
    cik TEXT,
    exchange TEXT,
    sector TEXT,
    industry TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Create indexes for performance
CREATE INDEX IF NOT EXISTS idx_financial_data_ticker ON financial_data(ticker);
CREATE INDEX IF NOT EXISTS idx_financial_data_date ON financial_data(filing_date);
CREATE INDEX IF NOT EXISTS idx_market_data_ticker ON market_data(ticker);
CREATE INDEX IF NOT EXISTS idx_market_data_timestamp ON market_data(timestamp);
CREATE INDEX IF NOT EXISTS idx_macro_data_type_date ON macro_data(data_type, date);
CREATE INDEX IF NOT EXISTS idx_ticker_mappings_ticker ON ticker_mappings(ticker);
`

// Test data for Apple (AAPL) to support performance testing
const testData = `
-- Insert test ticker mapping
INSERT OR REPLACE INTO ticker_mappings (ticker, company_name, cik, exchange, sector, industry)
VALUES ('AAPL', 'Apple Inc.', '0000320193', 'NASDAQ', 'Technology', 'Consumer Electronics');

-- Insert test financial data for AAPL (recent quarters)
INSERT OR REPLACE INTO financial_data 
(ticker, filing_date, period_type, fiscal_year, fiscal_quarter, net_income, revenue, assets, liabilities, shareholders_equity, diluted_shares_outstanding, interest_expense, income_tax_expense, operating_income, cost_of_revenue)
VALUES 
('AAPL', '2024-09-30', 'Q4', 2024, 4, 14.7e9, 94.9e9, 364.9e9, 290.4e9, 74.5e9, 15.1e9, 2.9e9, 2.8e9, 26.3e9, 52.2e9),
('AAPL', '2024-06-30', 'Q3', 2024, 3, 21.4e9, 85.8e9, 352.8e9, 279.4e9, 73.4e9, 15.2e9, 3.2e9, 3.1e9, 24.5e9, 47.8e9),
('AAPL', '2024-03-31', 'Q2', 2024, 2, 23.6e9, 90.8e9, 347.1e9, 274.5e9, 72.6e9, 15.3e9, 3.4e9, 3.5e9, 27.4e9, 49.1e9),
('AAPL', '2023-12-31', 'Q1', 2024, 1, 33.9e9, 119.6e9, 352.6e9, 279.4e9, 73.2e9, 15.3e9, 3.8e9, 4.3e9, 40.3e9, 65.8e9);

-- Insert test market data for AAPL
INSERT OR REPLACE INTO market_data (ticker, price, market_cap, beta, timestamp)
VALUES 
('AAPL', 225.50, 3.4e12, 1.25, '2024-07-26 16:00:00'),
('AAPL', 224.18, 3.38e12, 1.25, '2024-07-25 16:00:00'),
('AAPL', 220.35, 3.32e12, 1.25, '2024-07-24 16:00:00');

-- Insert test macro data (10-year treasury rate)
INSERT OR REPLACE INTO macro_data (data_type, value, date, source)
VALUES 
('10Y_TREASURY_RATE', 4.25, '2024-07-26', 'FRED'),
('10Y_TREASURY_RATE', 4.28, '2024-07-25', 'FRED'),
('10Y_TREASURY_RATE', 4.22, '2024-07-24', 'FRED');
`

func main() {
	// Get database path from environment or use default
	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		dbPath = "testdata/db/test.db"
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		log.Fatalf("Failed to create database directory: %v", err)
	}

	// Connect to SQLite database
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Enable foreign keys and WAL mode for better performance
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		log.Fatalf("Failed to enable foreign keys: %v", err)
	}

	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		log.Fatalf("Failed to set WAL mode: %v", err)
	}

	// Create schema
	if _, err := db.Exec(testSchema); err != nil {
		log.Fatalf("Failed to create schema: %v", err)
	}

	// Insert test data
	if _, err := db.Exec(testData); err != nil {
		log.Fatalf("Failed to insert test data: %v", err)
	}

	// Verify setup by checking data
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM financial_data WHERE ticker = 'AAPL'").Scan(&count); err != nil {
		log.Fatalf("Failed to verify test data: %v", err)
	}

	fmt.Printf("✅ Test database setup complete at: %s\n", dbPath)
	fmt.Printf("📊 Inserted %d financial records for AAPL\n", count)
	fmt.Println("🚀 Ready for performance testing!")
}
