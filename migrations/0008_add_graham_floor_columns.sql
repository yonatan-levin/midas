-- Migration 0008: Graham-school asset-floor diagnostic columns.
-- See docs/refactoring/archive/graham-floor-metrics-spec.md.
--
-- Adds four diagnostic columns to valuation_results plus the umbrella
-- total_liabilities column on financial_data. All columns are NULLable;
-- warm rows from before this migration stay valid and the read paths in
-- internal/infra/repositories/sqlite/financial_data_repository.go use
-- COALESCE(total_liabilities, 0) on SELECT so legacy rows behave as if
-- the field had been zero at write time.
--
-- Forward-only ALTER TABLE statements. cmd/migrate's applyMigration
-- tolerates "duplicate column name" errors so the same SQL is safe on
-- both fresh installs (where schema.sql already declares the columns)
-- and upgrades (where the columns must be added).
--
-- NOTE keep comment text free of SQL terminators. cmd/migrate splits the
-- file on the terminator before stripping comment lines, so a stray
-- terminator inside a comment would fragment the ALTER TABLE.

ALTER TABLE financial_data ADD COLUMN total_liabilities DECIMAL(15,2);

ALTER TABLE valuation_results ADD COLUMN current_assets_per_share DECIMAL(12,4);
ALTER TABLE valuation_results ADD COLUMN ncav_per_share DECIMAL(12,4);
ALTER TABLE valuation_results ADD COLUMN graham_floor_per_share DECIMAL(12,4);
ALTER TABLE valuation_results ADD COLUMN graham_discount_pct DECIMAL(8,6);
