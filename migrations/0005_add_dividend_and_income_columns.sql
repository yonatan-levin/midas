-- Migration 0005: Add columns for DDM and FFO valuation models (Phase 3)
-- Adds dividends_per_share, net_income, and gain_on_property_sales to financial_data table.

ALTER TABLE financial_data ADD COLUMN dividends_per_share DECIMAL(15,4);
ALTER TABLE financial_data ADD COLUMN net_income DECIMAL(15,2);
ALTER TABLE financial_data ADD COLUMN gain_on_property_sales DECIMAL(15,2);
