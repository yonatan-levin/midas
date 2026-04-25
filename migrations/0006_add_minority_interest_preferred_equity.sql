-- Migration 0006: Add columns for the equity-bridge correction terms (M-1d follow-up).
-- Closes the persistence-layer gap flagged by the validation cycle: the SQLite
-- repository was silently dropping MinorityInterest and PreferredEquity on
-- store/load, so warm-cache reads zeroed both fields and per-share values
-- regressed to pre-M-1d behavior on the dominant code path.

ALTER TABLE financial_data ADD COLUMN minority_interest DECIMAL(15,2);
ALTER TABLE financial_data ADD COLUMN preferred_equity DECIMAL(15,2);
