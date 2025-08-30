-- Seed minimal demo data for AAPL to enable fair-value smoke locally

-- Company
INSERT OR REPLACE INTO companies (ticker, cik, company_name, exchange, sector, industry, created_at, updated_at)
VALUES ('AAPL', '0000320193', 'Apple Inc.', 'NASDAQ', 'Technology', 'Consumer Electronics', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);

-- Macro data (latest)
INSERT INTO macro_data (as_of, risk_free_rate, risk_free_rate_3m, market_risk_premium, inflation_rate, source, created_at, updated_at)
VALUES (CURRENT_TIMESTAMP, 0.04, 0.04, 0.05, 0.02, 'seed', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);

-- Market data (latest)
INSERT INTO market_data (ticker, as_of_date, share_price, market_cap, shares_outstanding, beta, beta_3_year, average_volume, source, data_quality, created_at, updated_at)
VALUES ('AAPL', CURRENT_TIMESTAMP, 150.0, 2000000000000.0, 15000000000.0, 1.2, 1.1, 10000000.0, 'seed', 'high', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);

-- Financial data (three periods)
INSERT INTO financial_data (
  ticker, cik, filing_period, filing_date, as_of_date,
  operating_income, normalized_operating_income, revenue,
  interest_expense, tax_rate,
  total_assets, tangible_assets, goodwill, other_intangibles,
  total_debt, interest_bearing_debt,
  inventory, inventory_turnover, dead_inventory_writedown,
  shares_outstanding, diluted_shares_outstanding,
  has_normalized_data, missing_fields, created_at, updated_at
) VALUES
('AAPL','0000320193','2021FY', date('now','-3 years'), CURRENT_TIMESTAMP,
  7500000000.0, 7500000000.0, 250000000000.0,
  1000000000.0, 0.21,
  500000000000.0, 450000000000.0, 0.0, 0.0,
  20000000000.0, 20000000000.0,
  50000000000.0, 5.0, 0.0,
  15000000000.0, 15000000000.0,
  1, '[]', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),

('AAPL','0000320193','2022FY', date('now','-2 years'), CURRENT_TIMESTAMP,
  8500000000.0, 8500000000.0, 280000000000.0,
  1000000000.0, 0.21,
  520000000000.0, 470000000000.0, 0.0, 0.0,
  20000000000.0, 20000000000.0,
  52000000000.0, 5.0, 0.0,
  15000000000.0, 15000000000.0,
  1, '[]', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),

('AAPL','0000320193','2023FY', date('now','-1 years'), CURRENT_TIMESTAMP,
  10000000000.0, 10000000000.0, 300000000000.0,
  1000000000.0, 0.21,
  540000000000.0, 490000000000.0, 0.0, 0.0,
  20000000000.0, 20000000000.0,
  54000000000.0, 5.0, 0.0,
  15000000000.0, 15000000000.0,
  1, '[]', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);


