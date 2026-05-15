package profile_test

// Tier 2 cross-model regression suite. Pins 6 fields per ticker per
// spec §8.2:
//   - assumption_profile (exact)
//   - horizon_selected (exact)
//   - chosen_model (exact)
//   - primary_value (bit-for-bit for mature_large_bank, ε=1e-9 elsewhere)
//   - trailing_value (ε=1e-9 where applicable)
//   - warning_count (exact)
//
// Populated incrementally by P1-P4 worktrees. Skeleton lands in
// Phase Bootstrap so the file exists at master HEAD before parallel
// work dispatches.

import "testing"

func TestTier2_BasketRegression(t *testing.T) {
	t.Skip("Populated by P1-P4 worktrees; skeleton only at Phase Bootstrap")
}
