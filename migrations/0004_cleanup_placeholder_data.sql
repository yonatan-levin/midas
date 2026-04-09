-- Remove placeholder financial data seeded before Phase 1.2.
-- These rows have approximate values (e.g., AAPL OI=$10B vs real $120B)
-- and lack the new FCF fields (D&A, CapEx, Cash).
-- Fresh data will be fetched from SEC EDGAR on next valuation request.

DELETE FROM financial_data WHERE depreciation_and_amortization IS NULL
   OR (depreciation_and_amortization = 0 AND capital_expenditures = 0 AND cash_and_cash_equivalents = 0);
