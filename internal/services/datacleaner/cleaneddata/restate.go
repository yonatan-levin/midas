package cleaneddata

// Restated returns the balance-sheet view reconstructed from the POST-CLEAN
// entity's components, applying LedgerEntry.EquityOffset and
// LedgerEntry.TaxShieldDTA for every fired Restater-role adjuster.
//
// Phase 3 followup (HIGH-1 fix): the reducer NO LONGER re-applies
// LedgerEntry.DeltaAmount to component fields. The cleaner's dispatcher
// performs a dual-write to data.X immediately after each Restater fires
// (A2 OtherIntangibles, A4 Inventory, A5 Inventory + TaxShieldDTA,
// C1/C2/C3/C5 NormalizedOperatingIncome, C6 InterestExpense). The
// restated entity passed to New() already holds those post-dispatcher
// values. Re-applying DeltaAmount in this reducer would double-count
// every Restater fire.
//
// The ledger drives two remaining flows:
//
//   - EquityOffset → StockholdersEquity. Read directly from the LedgerEntry,
//     NEVER derived from DeltaAmount. C6 (capitalized_interest) is the
//     load-bearing counter-example: it carries DeltaAmount != 0 on
//     InterestExpense AND EquityOffset == 0 because the reclassification
//     is between income-statement lines, not a real economic loss flowing
//     to retained earnings.
//
//   - TaxShieldDTA → DeferredTaxAssets. Positive when an asset writedown
//     generates a deferred-tax asset (A5; A2 starts populating it in
//     Phase 3 per Q2 resolution).
//
// T2-BS-3 acceptance: for AMD/KO-style parser dropouts where
// AsReported.TotalLiabilities==0, the recompute below reconstructs
// TotalLiabilities from sum(components) using the post-clean
// (already-recomputed by the dispatcher and recompute shim where
// applicable) field values. This reconstruction is the ANALYTICAL
// sum-of-components view; it is NOT bit-for-bit equal to the parser-stamped
// umbrella tag (which may differ or, as in T2-BS-3, be missing entirely).
// That is exactly why drift-neutral consumers that need the as-filed umbrella
// read AsReported() instead of Restated().
//
// First-call cost: O(adjusters + fields). Subsequent calls: O(1) cached.
func (c *CleanedFinancialData) Restated() *FinancialDataView {
	if c == nil {
		return zeroView(RestatedView)
	}
	if c.restatedView != nil {
		return c.restatedView
	}
	// SEED: identity copy of the POST-CLEAN entity. The dispatcher dual-
	// write has already restated every Restater-touched component field
	// (OtherIntangibles, Inventory, NormalizedOperatingIncome, InterestExpense).
	v := identityCopy(c.restated)
	v.ViewKind = RestatedView

	if c.restated != nil {
		for _, e := range c.restated.AdjustmentLedger {
			if !e.Fired {
				continue
			}
			// EquityOffset and TaxShieldDTA are the ONLY ledger-driven
			// adjustments. The Restater-component DeltaAmount is already
			// in v.X via the dispatcher dual-write — re-applying here
			// would double-count.
			v.StockholdersEquity += e.EquityOffset
			v.DeferredTaxAssets += e.TaxShieldDTA
		}

		// Recompute umbrellas from components + plug, mirroring
		// recompute.go::recomputeUmbrellas (which only logs, never
		// mutates). The math is duplicated rather than shared because
		// the recompute shim's purpose is observability over the
		// pre-Phase-3 mutated *FinancialData — Restated() is the view
		// reconstruction over the same component set.
		v.CurrentAssets = c.restated.CashAndCashEquivalents + v.Inventory + c.restated.OtherCurrentAssets
		v.TotalAssets = v.CurrentAssets +
			v.Goodwill +
			v.OtherIntangibles +
			v.DeferredTaxAssets +
			c.restated.OtherNonCurrentAssets
		v.CurrentLiabilities = c.restated.OperatingLeaseLiabilityCurrent + c.restated.OtherCurrentLiabilities
		v.TotalLiabilities = v.CurrentLiabilities +
			v.TotalDebt +
			c.restated.OperatingLeaseLiabilityNoncurrent +
			c.restated.OtherNonCurrentLiabilities
		v.TangibleAssets = v.TotalAssets - v.Goodwill - v.OtherIntangibles
	}

	c.restatedView = &v
	return c.restatedView
}
