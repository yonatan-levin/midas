package cleaneddata

import "github.com/midas/dcf-valuation-api/internal/core/entities"

// Restated returns the balance-sheet view reconstructed from
// sum(components) + plug, applying LedgerEntry.EquityOffset and
// LedgerEntry.TaxShieldDTA for every fired Restater-role adjuster.
//
// T2-BS-3 acceptance: for AMD/KO-style parser dropouts where
// AsReported.TotalLiabilities==0, Restated.TotalLiabilities is the
// component-sum reconstruction (truthful) — the recompute below is the
// source of truth for Restated regardless of the parser-stamped value.
//
// LOAD-BEARING: C6 (capitalized_interest) has EquityOffset=0 by design.
// The reducer below reads e.EquityOffset directly and MUST NOT derive
// it from DeltaAmount; doing so would silently flow C6's reclassification
// into retained equity.
//
// First-call cost: O(adjusters + fields). Subsequent calls: O(1) cached.
func (c *CleanedFinancialData) Restated() *FinancialDataView {
	if c == nil {
		return zeroView(RestatedView)
	}
	if c.restated != nil {
		return c.restated
	}
	v := identityCopy(c.raw)
	v.ViewKind = RestatedView

	if c.raw != nil {
		for _, e := range c.raw.AdjustmentLedger {
			if !e.Fired {
				continue
			}
			applyLedgerEntryToView(&v, e)
			// EquityOffset is read directly from the LedgerEntry — NEVER
			// derived from DeltaAmount. C6 (capitalized_interest) is the
			// load-bearing counter-example: it carries DeltaAmount != 0
			// on InterestExpense AND EquityOffset == 0 because the
			// reclassification is between income-statement lines, not a
			// real economic loss flowing to retained earnings.
			v.StockholdersEquity += e.EquityOffset
			// TaxShieldDTA is positive when an asset writedown generates
			// a deferred-tax asset (A5 already; A2 starts populating
			// here in Phase 3).
			v.DeferredTaxAssets += e.TaxShieldDTA
		}

		// Recompute umbrellas from components + plug, mirroring
		// recompute.go::recomputeUmbrellas (which only logs, never
		// mutates). The math is duplicated rather than shared because
		// the recompute shim's purpose is observability over the
		// pre-Phase-3 mutated *FinancialData — Restated() is the view
		// reconstruction over the same component set.
		v.CurrentAssets = c.raw.CashAndCashEquivalents + v.Inventory + c.raw.OtherCurrentAssets
		v.TotalAssets = v.CurrentAssets +
			v.Goodwill +
			v.OtherIntangibles +
			v.DeferredTaxAssets +
			c.raw.OtherNonCurrentAssets
		v.CurrentLiabilities = c.raw.OperatingLeaseLiabilityCurrent + c.raw.OtherCurrentLiabilities
		v.TotalLiabilities = v.CurrentLiabilities +
			v.TotalDebt +
			c.raw.OperatingLeaseLiabilityNoncurrent +
			c.raw.OtherNonCurrentLiabilities
		v.TangibleAssets = v.TotalAssets - v.Goodwill - v.OtherIntangibles
	}

	c.restated = &v
	return c.restated
}

// applyLedgerEntryToView routes a fired Restater-role LedgerEntry to the
// component it adjusts. Unknown Component values are silently skipped
// (fail-soft) so future adjusters can add components incrementally
// without breaking Restated() during the migration window.
//
// EquityOffset and TaxShieldDTA are applied by the caller, NOT here —
// keeping the per-component switch focused on component-amount routing
// makes the C6 EquityOffset=0 invariant easier to audit.
func applyLedgerEntryToView(v *FinancialDataView, e entities.LedgerEntry) {
	switch e.Component {
	case "Inventory":
		v.Inventory += e.DeltaAmount
	case "OtherIntangibles":
		v.OtherIntangibles += e.DeltaAmount
	case "DeferredTaxAssets":
		v.DeferredTaxAssets += e.DeltaAmount
	case "OperatingIncome":
		v.OperatingIncome += e.DeltaAmount
	case "NormalizedOperatingIncome":
		v.NormalizedOperatingIncome += e.DeltaAmount
	case "InterestExpense":
		v.InterestExpense += e.DeltaAmount
		// default: silently skip — empty Component (OverlayEmitter rows) or
		// future-added components fall through here. Fail-soft preserves
		// view reconstruction across schema additions.
	}
}
