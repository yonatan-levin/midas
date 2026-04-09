-- Database schema for DCF Valuation API
-- Compatible with both SQLite and PostgreSQL

-- Enable foreign key constraints for SQLite
PRAGMA foreign_keys = ON;

-- Companies table for basic company information
CREATE TABLE IF NOT EXISTS companies (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ticker VARCHAR(10) NOT NULL UNIQUE,
    cik VARCHAR(10) NOT NULL UNIQUE,
    company_name VARCHAR(255) NOT NULL,
    exchange VARCHAR(10),
    sector VARCHAR(100),
    industry VARCHAR(100),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Index for fast ticker and CIK lookups
CREATE INDEX IF NOT EXISTS idx_companies_ticker ON companies(ticker);
CREATE INDEX IF NOT EXISTS idx_companies_cik ON companies(cik);

-- Financial data table for SEC filing data
CREATE TABLE IF NOT EXISTS financial_data (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ticker VARCHAR(10) NOT NULL,
    cik VARCHAR(10) NOT NULL,
    filing_period VARCHAR(10) NOT NULL, -- e.g., "2023Q4", "2023FY"
    filing_date DATE NOT NULL,
    as_of_date TIMESTAMP NOT NULL,
    
    -- Income Statement (normalized values)
    operating_income DECIMAL(15,2),
    normalized_operating_income DECIMAL(15,2),
    revenue DECIMAL(15,2),
    interest_expense DECIMAL(15,2),
    tax_rate DECIMAL(5,4), -- Effective tax rate as decimal (0.21 = 21%)
    
    -- Balance Sheet (adjusted values)
    total_assets DECIMAL(15,2),
    tangible_assets DECIMAL(15,2),
    goodwill DECIMAL(15,2),
    other_intangibles DECIMAL(15,2),
    total_debt DECIMAL(15,2),
    interest_bearing_debt DECIMAL(15,2),
    
    -- Inventory analysis
    inventory DECIMAL(15,2),
    inventory_turnover DECIMAL(8,4),
    dead_inventory_writedown DECIMAL(15,2),
    
    -- Cash flow statement (for true FCF calculation)
    depreciation_and_amortization DECIMAL(15,2),
    capital_expenditures DECIMAL(15,2),
    operating_cash_flow DECIMAL(15,2),

    -- Working capital components
    current_assets DECIMAL(15,2),
    current_liabilities DECIMAL(15,2),

    -- Cash position (for equity bridge)
    cash_and_cash_equivalents DECIMAL(15,2),

    -- Equity (for ROIC calculation)
    stockholders_equity DECIMAL(15,2),

    -- Share information
    shares_outstanding DECIMAL(15,0),
    diluted_shares_outstanding DECIMAL(15,0),
    
    -- Data quality flags
    has_normalized_data BOOLEAN DEFAULT FALSE,
    missing_fields TEXT, -- JSON array of missing field names
    
    -- Metadata
    raw_data_hash VARCHAR(64), -- Hash of raw SEC data for change detection
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    -- Ensure unique combination of ticker and period
    UNIQUE(ticker, filing_period),
    FOREIGN KEY (ticker) REFERENCES companies(ticker) ON DELETE CASCADE
);

-- Indexes for financial data queries
CREATE INDEX IF NOT EXISTS idx_financial_data_ticker ON financial_data(ticker);
CREATE INDEX IF NOT EXISTS idx_financial_data_ticker_period ON financial_data(ticker, filing_period);
CREATE INDEX IF NOT EXISTS idx_financial_data_filing_date ON financial_data(filing_date DESC);
CREATE INDEX IF NOT EXISTS idx_financial_data_as_of ON financial_data(as_of_date DESC);

-- Market data table for pricing and risk metrics
CREATE TABLE IF NOT EXISTS market_data (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ticker VARCHAR(10) NOT NULL,
    as_of_date TIMESTAMP NOT NULL,
    
    -- Current market metrics
    share_price DECIMAL(10,4) NOT NULL,
    market_cap DECIMAL(15,2),
    shares_outstanding DECIMAL(15,0),
    
    -- Risk metrics
    beta DECIMAL(6,4), -- 1-year beta
    beta_3_year DECIMAL(6,4), -- 3-year beta for stability
    
    -- Volume and liquidity
    average_volume DECIMAL(15,0),
    
    -- Data source metadata
    source VARCHAR(20) NOT NULL, -- 'yfinance', 'finzive', etc.
    data_quality VARCHAR(10) DEFAULT 'medium', -- 'high', 'medium', 'low'
    
    -- Metadata
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    FOREIGN KEY (ticker) REFERENCES companies(ticker) ON DELETE CASCADE
);

-- Indexes for market data queries
CREATE INDEX IF NOT EXISTS idx_market_data_ticker ON market_data(ticker);
CREATE INDEX IF NOT EXISTS idx_market_data_ticker_date ON market_data(ticker, as_of_date DESC);
CREATE INDEX IF NOT EXISTS idx_market_data_source ON market_data(source);

-- Macro data table for economic indicators
CREATE TABLE IF NOT EXISTS macro_data (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    as_of TIMESTAMP NOT NULL,
    
    -- Risk-free rate (typically 10-year Treasury)
    risk_free_rate DECIMAL(6,4),
    risk_free_rate_3m DECIMAL(6,4), -- 3-month Treasury
    
    -- Market risk premium (configurable)
    market_risk_premium DECIMAL(6,4),
    
    -- Economic indicators
    inflation_rate DECIMAL(6,4),
    
    -- Data source
    source VARCHAR(20) NOT NULL, -- 'fred', 'manual', etc.
    
    -- Metadata
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Index for macro data queries
CREATE INDEX IF NOT EXISTS idx_macro_data_date ON macro_data(as_of DESC);

-- Ticker to CIK mapping table
CREATE TABLE IF NOT EXISTS ticker_mapping (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ticker VARCHAR(10) NOT NULL UNIQUE,
    cik VARCHAR(10) NOT NULL,
    
    -- Metadata
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Indexes for ticker mapping queries
CREATE INDEX IF NOT EXISTS idx_ticker_mapping_ticker ON ticker_mapping(ticker);
CREATE INDEX IF NOT EXISTS idx_ticker_mapping_cik ON ticker_mapping(cik);

-- Valuation results table for caching DCF calculations
CREATE TABLE IF NOT EXISTS valuation_results (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ticker VARCHAR(10) NOT NULL,
    calculated_at TIMESTAMP NOT NULL,
    
    -- Input parameters used for calculation
    wacc DECIMAL(6,4) NOT NULL,
    growth_rate DECIMAL(6,4) NOT NULL,
    terminal_growth_rate DECIMAL(6,4) NOT NULL,
    market_risk_premium DECIMAL(6,4) NOT NULL,
    
    -- Valuation outputs
    tangible_value_per_share DECIMAL(10,4),
    dcf_value_per_share DECIMAL(10,4),
    enterprise_value DECIMAL(15,2),
    equity_value DECIMAL(15,2),
    
    -- Data sources and quality
    financial_data_period VARCHAR(10), -- Which period was used
    market_data_date TIMESTAMP, -- When was market data from
    data_freshness_score INTEGER DEFAULT 100, -- 0-100 score
    
    -- Metadata
    calculation_version VARCHAR(10) DEFAULT '1.0', -- Track calculation methodology
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    FOREIGN KEY (ticker) REFERENCES companies(ticker) ON DELETE CASCADE
);

-- Indexes for valuation results
CREATE INDEX IF NOT EXISTS idx_valuation_results_ticker ON valuation_results(ticker);
CREATE INDEX IF NOT EXISTS idx_valuation_results_calculated ON valuation_results(calculated_at DESC);
CREATE INDEX IF NOT EXISTS idx_valuation_results_freshness ON valuation_results(data_freshness_score DESC);

-- Cache metadata table for tracking cache status
CREATE TABLE IF NOT EXISTS cache_metadata (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    cache_key VARCHAR(255) NOT NULL UNIQUE,
    cache_type VARCHAR(50) NOT NULL, -- 'sec_filing', 'market_data', 'valuation', etc.
    ticker VARCHAR(10), -- NULL for global cache entries
    expires_at TIMESTAMP NOT NULL,
    data_size INTEGER DEFAULT 0, -- Size in bytes for monitoring
    hit_count INTEGER DEFAULT 0, -- Usage tracking
    
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Indexes for cache metadata
CREATE INDEX IF NOT EXISTS idx_cache_metadata_key ON cache_metadata(cache_key);
CREATE INDEX IF NOT EXISTS idx_cache_metadata_type ON cache_metadata(cache_type);
CREATE INDEX IF NOT EXISTS idx_cache_metadata_ticker ON cache_metadata(ticker);
CREATE INDEX IF NOT EXISTS idx_cache_metadata_expires ON cache_metadata(expires_at);

-- Raw data storage table for SEC filings (optional blob storage)
CREATE TABLE IF NOT EXISTS raw_sec_data (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    cik VARCHAR(10) NOT NULL,
    accession_number VARCHAR(25) NOT NULL,
    filing_date DATE NOT NULL,
    form_type VARCHAR(10) NOT NULL, -- '10-K', '10-Q', etc.
    
    -- Raw JSON data
    raw_json TEXT NOT NULL, -- Full SEC Company Facts JSON
    data_hash VARCHAR(64) NOT NULL, -- SHA-256 hash for deduplication
    
    -- Metadata
    file_size INTEGER DEFAULT 0,
    processing_status VARCHAR(20) DEFAULT 'pending', -- 'pending', 'processed', 'error'
    processing_error TEXT,
    
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    processed_at TIMESTAMP,
    
    UNIQUE(cik, accession_number)
);

-- Indexes for raw data storage
CREATE INDEX IF NOT EXISTS idx_raw_sec_data_cik ON raw_sec_data(cik);
CREATE INDEX IF NOT EXISTS idx_raw_sec_data_hash ON raw_sec_data(data_hash);
CREATE INDEX IF NOT EXISTS idx_raw_sec_data_status ON raw_sec_data(processing_status);
CREATE INDEX IF NOT EXISTS idx_raw_sec_data_filing_date ON raw_sec_data(filing_date DESC);

-- Audit log table for tracking data updates and API usage
CREATE TABLE IF NOT EXISTS audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type VARCHAR(50) NOT NULL, -- 'data_update', 'api_call', 'calculation', etc.
    entity_type VARCHAR(50), -- 'company', 'financial_data', 'market_data', etc.
    entity_id VARCHAR(50), -- ticker, cik, or other identifier
    
    -- Event details
    event_data TEXT, -- JSON with event-specific data
    user_agent VARCHAR(255),
    ip_address VARCHAR(45),
    request_id VARCHAR(36), -- UUID for request tracking
    
    -- Performance metrics
    processing_time_ms INTEGER,
    status VARCHAR(20) DEFAULT 'success', -- 'success', 'error', 'warning'
    error_message TEXT,
    
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Indexes for audit log
CREATE INDEX IF NOT EXISTS idx_audit_log_event_type ON audit_log(event_type);
CREATE INDEX IF NOT EXISTS idx_audit_log_entity ON audit_log(entity_type, entity_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_created ON audit_log(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_status ON audit_log(status);
CREATE INDEX IF NOT EXISTS idx_audit_log_request_id ON audit_log(request_id);

-- Views for common queries

-- Latest financial data per company
CREATE VIEW IF NOT EXISTS latest_financial_data AS
SELECT DISTINCT
    fd.ticker,
    fd.cik,
    fd.filing_period,
    fd.filing_date,
    fd.as_of_date,
    fd.operating_income,
    fd.normalized_operating_income,
    fd.revenue,
    fd.interest_expense,
    fd.tax_rate,
    fd.total_assets,
    fd.tangible_assets,
    fd.total_debt,
    fd.interest_bearing_debt,
    fd.shares_outstanding,
    fd.diluted_shares_outstanding,
    fd.has_normalized_data,
    c.company_name,
    c.sector,
    c.industry
FROM financial_data fd
INNER JOIN companies c ON fd.ticker = c.ticker
INNER JOIN (
    SELECT ticker, MAX(filing_date) as max_filing_date
    FROM financial_data
    GROUP BY ticker
) latest ON fd.ticker = latest.ticker AND fd.filing_date = latest.max_filing_date;

-- Latest market data per company
CREATE VIEW IF NOT EXISTS latest_market_data AS
SELECT DISTINCT
    md.ticker,
    md.as_of_date,
    md.share_price,
    md.market_cap,
    md.shares_outstanding,
    md.beta,
    md.beta_3_year,
    md.average_volume,
    md.source,
    md.data_quality,
    c.company_name
FROM market_data md
INNER JOIN companies c ON md.ticker = c.ticker
INNER JOIN (
    SELECT ticker, MAX(as_of_date) as max_as_of_date
    FROM market_data
    GROUP BY ticker
) latest ON md.ticker = latest.ticker AND md.as_of_date = latest.max_as_of_date;

-- Complete valuation data combining financial and market data
CREATE VIEW IF NOT EXISTS complete_valuation_data AS
SELECT 
    fd.ticker,
    fd.cik,
    c.company_name,
    c.sector,
    c.industry,
    
    -- Financial data
    fd.filing_period,
    fd.filing_date,
    fd.normalized_operating_income,
    fd.revenue,
    fd.interest_expense,
    fd.tax_rate,
    fd.tangible_assets,
    fd.total_debt,
    fd.interest_bearing_debt,
    fd.diluted_shares_outstanding,
    
    -- Market data
    md.share_price,
    md.market_cap,
    md.beta,
    md.beta_3_year,
    md.as_of_date as market_data_date,
    md.source as market_data_source,
    
    -- Macro data (latest)
    macro.risk_free_rate,
    macro.market_risk_premium,
    macro.as_of_date as macro_data_date
    
FROM latest_financial_data fd
LEFT JOIN latest_market_data md ON fd.ticker = md.ticker
LEFT JOIN companies c ON fd.ticker = c.ticker
LEFT JOIN (
    SELECT * FROM macro_data 
    ORDER BY as_of_date DESC 
    LIMIT 1
) macro ON 1=1;

-- Trigger to update updated_at timestamps (works in SQLite and PostgreSQL)
CREATE TRIGGER IF NOT EXISTS update_companies_updated_at 
    AFTER UPDATE ON companies
    FOR EACH ROW
BEGIN
    UPDATE companies SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS update_financial_data_updated_at 
    AFTER UPDATE ON financial_data
    FOR EACH ROW
BEGIN
    UPDATE financial_data SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS update_market_data_updated_at 
    AFTER UPDATE ON market_data
    FOR EACH ROW
BEGIN
    UPDATE market_data SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS update_cache_metadata_updated_at 
    AFTER UPDATE ON cache_metadata
    FOR EACH ROW
BEGIN
    UPDATE cache_metadata SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END; 

-- API Keys table for authentication (SQLite compatible)
CREATE TABLE IF NOT EXISTS api_keys (
    id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    key_hash VARCHAR(255) NOT NULL UNIQUE,
    user_id VARCHAR(255) NOT NULL,
    permissions TEXT NOT NULL DEFAULT '[]', -- JSON array as TEXT for SQLite
    rate_limit INTEGER NOT NULL DEFAULT 1000,
    expires_at TIMESTAMP NULL,
    is_active BOOLEAN NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at TIMESTAMP NULL,
    usage_count INTEGER NOT NULL DEFAULT 0
);

-- Indexes for API keys table
CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys(key_hash);
CREATE INDEX IF NOT EXISTS idx_api_keys_user_id ON api_keys(user_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_active ON api_keys(is_active);
CREATE INDEX IF NOT EXISTS idx_api_keys_expires ON api_keys(expires_at);

-- API key usage tracking for rate limiting and monitoring
CREATE TABLE IF NOT EXISTS api_key_usage (
    id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    api_key_id TEXT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    endpoint VARCHAR(255) NOT NULL,
    timestamp TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    response_status INTEGER NOT NULL,
    response_time_ms INTEGER NOT NULL,
    user_agent TEXT,
    ip_address TEXT
);

-- Indexes for usage tracking
CREATE INDEX IF NOT EXISTS idx_usage_api_key_timestamp ON api_key_usage(api_key_id, timestamp);
CREATE INDEX IF NOT EXISTS idx_usage_timestamp ON api_key_usage(timestamp);
CREATE INDEX IF NOT EXISTS idx_usage_endpoint ON api_key_usage(endpoint);

-- Trigger to update api_keys last_used_at and usage_count
CREATE TRIGGER IF NOT EXISTS update_api_key_usage
    AFTER INSERT ON api_key_usage
    FOR EACH ROW
BEGIN
    UPDATE api_keys 
    SET last_used_at = NEW.timestamp,
        usage_count = usage_count + 1,
        updated_at = CURRENT_TIMESTAMP
    WHERE id = NEW.api_key_id;
END;

-- Trigger to update api_keys updated_at timestamp
CREATE TRIGGER IF NOT EXISTS update_api_keys_updated_at 
    AFTER UPDATE ON api_keys
    FOR EACH ROW
BEGIN
    UPDATE api_keys SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

-- Scheduler watchlist table for managing which tickers to fetch during nightly ingestion
CREATE TABLE IF NOT EXISTS scheduler_watchlist (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ticker VARCHAR(10) NOT NULL UNIQUE,
    is_active BOOLEAN NOT NULL DEFAULT 1,
    priority INTEGER NOT NULL DEFAULT 1, -- 1=high, 2=medium, 3=low
    added_reason VARCHAR(255), -- Why this ticker was added (e.g., "user_request", "auto_discovery")
    last_fetched_at TIMESTAMP NULL, -- When was this ticker last successfully fetched
    fetch_failures INTEGER NOT NULL DEFAULT 0, -- Count of consecutive failures
    max_failures INTEGER NOT NULL DEFAULT 5, -- Auto-disable after this many failures
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    
    FOREIGN KEY (ticker) REFERENCES companies(ticker) ON DELETE CASCADE
);

-- Indexes for scheduler watchlist
CREATE INDEX IF NOT EXISTS idx_scheduler_watchlist_active ON scheduler_watchlist(is_active);
CREATE INDEX IF NOT EXISTS idx_scheduler_watchlist_priority ON scheduler_watchlist(priority);
CREATE INDEX IF NOT EXISTS idx_scheduler_watchlist_last_fetched ON scheduler_watchlist(last_fetched_at);
CREATE INDEX IF NOT EXISTS idx_scheduler_watchlist_failures ON scheduler_watchlist(fetch_failures);

-- Trigger to update scheduler_watchlist updated_at timestamp
CREATE TRIGGER IF NOT EXISTS update_scheduler_watchlist_updated_at 
    AFTER UPDATE ON scheduler_watchlist
    FOR EACH ROW
BEGIN
    UPDATE scheduler_watchlist SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;