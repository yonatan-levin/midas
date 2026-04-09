-- Add cash flow, working capital, and equity columns for true FCF calculation.
-- These are nullable (SQLite ALTER TABLE ADD COLUMN always nullable).
-- Existing rows will have NULL/0 for these fields, triggering the
-- completeness check to re-fetch from SEC on next valuation request.

ALTER TABLE financial_data ADD COLUMN depreciation_and_amortization DECIMAL(15,2);
ALTER TABLE financial_data ADD COLUMN capital_expenditures DECIMAL(15,2);
ALTER TABLE financial_data ADD COLUMN operating_cash_flow DECIMAL(15,2);
ALTER TABLE financial_data ADD COLUMN current_assets DECIMAL(15,2);
ALTER TABLE financial_data ADD COLUMN current_liabilities DECIMAL(15,2);
ALTER TABLE financial_data ADD COLUMN cash_and_cash_equivalents DECIMAL(15,2);
ALTER TABLE financial_data ADD COLUMN stockholders_equity DECIMAL(15,2);
