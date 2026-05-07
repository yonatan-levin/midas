# VAL-3 — FFO model is snapshot only, uses uniform 15× P/FFO across all REIT subsectors, and prefers FFO over the methodologically-superior AFFO

**Status:** OPEN — filed 2026-05-06 as part of the cross-model review.
**Severity:** High. The FFO model has THREE compounding issues — same shape as MXL had on the revenue-multiple path: (1) wrong base metric (FFO instead of AFFO), (2) uniform multiple across wildly-different subsectors (residential/commercial/industrial/data center/cell tower differ 3×), (3) no forward projection. Combined understatement/overstatement varies by subsector but can be 2-4× off the right number.
**Origin:** Cross-model review prompted by RM-3's findings. `thinkdeep` second-round review noted REITs need (a) per-share AFFO/FFO growth, (b) cost of equity or forward yield (NOT enterprise WACC), (c) 1-2y forward P/FFO anchor (not 5y) for multiple-based REIT valuation, OR full forward FFO/AFFO projection + terminal multiple if doing forward-cash-flow valuation. Damodaran (perplexity-cited from his 2024 REIT paper and Stern tools): *"AFFO (FFO − maintenance capex/recurring capex) is superior for valuation as FFO overstates cash flow; AFFO multiples or FCFF DCF are mandated for REITs, adjusting for non-cash NOI growth."* Subsector multiples (perplexity-cited from Nareit/BAML 2025-2026): Residential 18-22×, Commercial (office) 12-16×, Industrial 20-25×, Healthcare 15-20×, Data Center 28-35×, Cell Tower 22-28×. Today's model uses **15× for all REITs**.
**Blocks:** Nothing. FFO works for typical REITs but is mis-calibrated for the high-multiple subsectors (data center, cell tower, industrial).
**Related specs:** RM-2 (sector multiple coverage), RM-3 (revenue multiple unified design), VAL-1 (DCF), VAL-2 (DDM), `internal/services/valuation/models/ffo.go`.

---

## Context

Reading `ffo.go` (218 lines):

- **Algorithm.** `FFO = NetIncome + D&A − GainsOnPropertySales`. `Value = (FFO / shares) × P/FFO`. Static, snapshot-based.
- **Multiple.** Default `15.0` (`DefaultPFFOMultiple`). Loaded from `industry_multiples.json` under `reit_pffo_multiples` map; only the `default` key is consulted today.
- **NAV cross-check.** Reasonable: `NAV = OperatingIncome / cap_rate` (default 6%). Compared against P/FFO value, flags if divergence >2× or <0.5×. **This is the only thing the existing model gets right that the other multiple-based models don't have.**
- **Equity bridge.** `EquityValue = ValuePerShare × Shares; EnterpriseValue = EquityValue + Debt − Cash`. Standard.
- **Confidence.** "high" if FFO > 0 AND D&A > 0; "low" otherwise.
- **Data flow.** `input.GrowthEstimate` and `input.WACC` are **never used.** No forward projection. Same gap as today's revenue-multiple.

## Why it matters

### Issue 1 — FFO instead of AFFO

Damodaran is explicit: AFFO is superior. The difference:

```
FFO  = NetIncome + D&A − PropertyGains
AFFO = FFO − MaintenanceCapEx − StraightLineRentAdjustment − [other non-cash adjustments]
```

For a typical REIT, AFFO ≈ 70-85% of FFO. The "missing" amount is recurring capital expenditure to maintain the property portfolio — real cash flow, not optional. Using FFO overstates true distributable cash flow.

**Why this matters numerically.** A REIT trading at $100 with FFO of $8/share (P/FFO of 12.5×) and AFFO of $6/share (P/AFFO of 16.7×) looks "cheap" on P/FFO but "fair" on P/AFFO. Today's model judges using the cheaper-looking metric. Investors get a misleading "buy" signal on REITs with high maintenance capex (industrial, data center, healthcare).

**Implementation.** AFFO requires the maintenance-capex line item. SEC filings break out "capital expenditures for maintenance" separately from "capital expenditures for development/acquisition" only sometimes. When unavailable, practitioners estimate maintenance capex as 60-80% of total capex (industry rule of thumb). The SEC parser would need to either:
- Map a new XBRL tag (`us-gaap:PaymentsForMaintenanceOfProperty` or similar), or
- Estimate from total capex × archetype-specific ratio.

### Issue 2 — Uniform 15× across subsectors

REIT subsector multiples vary 3× in 2025-2026 (perplexity-cited from Nareit/BAML data):

| Subsector | NTM P/FFO range | Today's Midas multiple | Error magnitude |
|---|---|---|---|
| Residential (apartments) | 18-22× | 15× | 25-50% understatement |
| Commercial / office | 12-16× | 15× | 0-20% overstatement |
| Industrial / logistics | 20-25× | 15× | 33-67% understatement |
| Healthcare facilities | 15-20× | 15× | 0-33% understatement |
| **Data center** | 28-35× | 15× | **87-133% understatement** |
| **Cell tower** | 22-28× | 15× | **47-87% understatement** |
| Retail / mall | 8-12× | 15× | 25-87% overstatement |
| Specialty | 15-20× | 15× | 0-33% understatement |

For data center REITs (DLR, EQIX, etc.) the 15× default produces a fair value roughly half of what subsector-correct math would say. For mall REITs it's the opposite.

**This is the same shape as RM-2's MFG-default-1.5× problem.** The fix is the same: subsector-keyed multiples table.

### Issue 3 — Snapshot, not forward

Damodaran's REIT-specific recommendation: *"Forward-looking preferred: 5y FFO forecast + terminal P/FFO (15-20×) + DCF (WACC 7-9%)."*

Today's model: snapshot. Doesn't use the engine's growth curve at all.

For high-growth REIT subsectors (industrial benefiting from e-commerce, data center benefiting from AI infrastructure, cell tower benefiting from 5G), forward FFO projections matter. A data center REIT growing 12%/year with a static 15× multiple looks fairly valued; a forward DCF would show meaningful upside.

For low-growth REITs (mall, office in 2025-26), snapshot is closer to right because there's little growth to project.

### Issue 4 — Discount rate

`thinkdeep`: *"FFO equity valuation generally wants cost of equity or a forward equity multiple/yield framework, not enterprise WACC."* If we add forward FFO projection, the discount should be **cost of equity**, not WACC. (Same correction as for DDM, opposite of what RM-3's original sketch had.)

## Proposed fix

### Phase 1 — Subsector-keyed P/FFO multiples (cheap, immediate impact)

Mirror RM-2 Phase 1. Update `industry_multiples.json` to add subsector entries:

```json
{
  "reit_pffo_multiples": {
    "default": 15.0,
    "REIT_RESIDENTIAL": 20.0,
    "REIT_COMMERCIAL": 14.0,
    "REIT_INDUSTRIAL": 22.5,
    "REIT_HEALTHCARE": 17.5,
    "REIT_DATACENTER": 31.0,
    "REIT_CELLTOWER": 25.0,
    "REIT_RETAIL": 10.0,
    "REIT_SPECIALTY": 17.5
  },
  "reit_cap_rates": {
    "default": 0.06,
    "REIT_RESIDENTIAL": 0.05,
    "REIT_COMMERCIAL": 0.075,
    "REIT_INDUSTRIAL": 0.045,
    "REIT_HEALTHCARE": 0.06,
    "REIT_DATACENTER": 0.04,
    "REIT_CELLTOWER": 0.045,
    "REIT_RETAIL": 0.085
  }
}
```

The classifier needs to emit subsector codes for known REIT subsectors. SIC codes: 6798 (REITs general), 6500-6799 (Real Estate), but the SIC granularity isn't fine enough for subsector — Midas would need a separate REIT-subsector classifier (probably keyword-based on company name: "Industrial Realty Trust", "Digital Realty", "American Tower").

Effort: ~1 day. Adds `reit_subsector` field on `entities.FinancialData` (or as a classifier output), populates from a heuristic/keyword classifier, and the FFO model picks the correct multiple via the existing longest-prefix-match logic.

### Phase 2 — AFFO support (medium impact, requires data plumbing)

Add `MaintenanceCapEx` to `entities.FinancialData`. Populate from XBRL where available; estimate as `0.7 × CapitalExpenditures` for REITs where the breakout isn't filed. Compute AFFO = FFO − MaintenanceCapEx. Apply the multiple to AFFO instead of FFO when AFFO is available.

Two response fields:
- `pffo_value_per_share` — the existing FFO-based number (for backward compat).
- `paffo_value_per_share` — the new AFFO-based number (preferred when available).

The headline `intrinsic_value_per_share` becomes the AFFO-based number when AFFO data is available; falls back to FFO-based otherwise.

Effort: ~2 days. Includes parser change, entity field, model algorithm, tests.

### Phase 3 — Forward FFO/AFFO projection (largest change)

Mirror RM-3's profile-driven design. Profile says `(REIT, residential)` → horizon 2y (NTM-style anchor); `(REIT, datacenter)` → horizon 5y (high-growth subsector warrants longer projection). Apply per-year FFO/AFFO growth from a REIT-specific growth curve, project forward, apply terminal multiple at year N, discount at cost of equity.

REIT growth curve: today's `GrowthEstimate.ProjectedGrowthRates` is computed from analyst-blend on revenue. For REITs, FFO growth ≠ revenue growth (NOI growth + acquisition activity matters). Two options:

A. Use revenue growth as a proxy. Cheap; will be wrong on heavy-acquisition years.
B. Add an FFO-specific growth signal. Expensive; needs Yahoo/SEC FFO history.

Recommended: A initially, B later if calibration matters.

Effort: ~3 days. Largest of the three phases.

### Phase 4 — NAV cross-check upgraded with subsector cap rates

Already present (lines 178-199) but uses default 6% cap rate everywhere. With subsector cap rates from Phase 1, NAV cross-check becomes more accurate:

| Subsector | Cap rate range (2025-26) |
|---|---|
| Residential | 4.5-5.5% |
| Commercial / office | 7-8% |
| Industrial | 4-5% |
| Healthcare | 5.5-6.5% |
| Data center | 3.5-4.5% |
| Cell tower | 4-5% |
| Retail | 7.5-9.5% |

Existing NAV-divergence warning behaviour is preserved; just uses the right cap rate.

Effort: included in Phase 1.

## Recommendation

**Phase 1 + Phase 4 first, Phase 2 second, Phase 3 last.**

Phase 1 produces the largest immediate accuracy improvement (3× error magnitudes on data center / cell tower fixed in a single config change). Phase 4 makes the existing NAV cross-check more accurate. Together, ~1 day of work.

Phase 2 (AFFO) is the methodological upgrade and matters for serious REIT analysis. Adds a meaningful data dependency (maintenance capex parsing) but is well-scoped.

Phase 3 (forward FFO) is the biggest change and is the analog of RM-3 for REITs. Ships with the unified `AssumptionProfile` work cross-cutting with VAL-1, VAL-2, RM-3.

## Tests required

Phase 1 (subsector multiples):
- DLR fixture (Digital Realty, data center): assert classifier emits `REIT_DATACENTER`, model uses 31× P/FFO. Per-share value ≈ 2× the current 15×-based number.
- AMT fixture (American Tower, cell tower): assert classifier emits `REIT_CELLTOWER`, model uses 25×. Per-share value ≈ 1.7× current.
- Mall REIT fixture (SPG): assert classifier emits `REIT_RETAIL`, model uses 10×. Per-share value ≈ 0.67× current.
- Unmapped REIT: falls through to default 15×, no regression.

Phase 2 (AFFO):
- REIT fixture with maintenance capex disclosed: assert AFFO < FFO, headline value uses AFFO.
- REIT fixture without maintenance capex: estimate at 0.7× total capex, emit warning.
- Bit-for-bit regression: when AFFO data is unavailable, output matches today's FFO-based model.

Phase 3 (forward):
- Industrial REIT (PLD): forward 5y FFO with growth curve; per-share value 15-30% higher than snapshot.
- Mall REIT (SPG): horizon 2y (low-growth subsector); forward ≈ snapshot ±10%.
- All REITs in test suite: trailing and forward both emitted, divergence visible.

Coverage: ≥90% on `ffo.go` per CLAUDE.md finance-module standard.

## Out of scope

- Triple net lease vs gross lease subclassification (one tier deeper than this tracker addresses).
- International REIT analogs (Australian REITs, Japanese REITs/J-REITs).
- mREIT (mortgage REIT) classification — fundamentally different model (interest-rate-spread driven, not property-cash-flow driven). Track as VAL-3.5; mREITs probably should NOT route to FFO model at all.
- Per-property NAV detail (would require a property-level data feed).
- Implied Cap Rate cross-check (vs current NOI-based NAV cross-check).

## Acceptance for closing this tracker

### Phase 1
- [ ] 8+ subsector entries in `industry_multiples.json` for `reit_pffo_multiples` and `reit_cap_rates`.
- [ ] REIT-subsector classifier emits codes (keyword-based or SIC-based).
- [ ] FFO model picks the correct multiple via longest-prefix-match.
- [ ] NAV cross-check uses subsector cap rates.
- [ ] Tests pass for DLR, AMT, SPG, PLD, EQR fixtures.

### Phase 2
- [ ] `MaintenanceCapEx` field on `FinancialData`, populated by parser.
- [ ] AFFO-based valuation alongside FFO-based; AFFO preferred when data is available.
- [ ] CHANGELOG/CLAUDE.md updated.

### Phase 3 (with unified profile work)
- [ ] Forward FFO/AFFO projection lands; profile-driven horizon.
- [ ] Cost-of-equity (not WACC) discount.
- [ ] Both trailing and forward values emitted.
- [ ] All RM-3 / VAL-1 / VAL-2 acceptance criteria holding (no cross-model regressions).
