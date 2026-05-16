# DC-1 — Datacleaner adjusters are single-sided; collapse "valuation overlay" with "as-of restatement"

**Status:** IN PROGRESS — **Phase 0 SHIPPED 2026-05-16** (merge `1640394`); Phases 1-4 pending. Originally filed 2026-05-05 during the Graham-floor metrics design pass.
**Severity:** Major (silently produces inconsistent balance-sheet output; surfaces only when a downstream consumer needs `Assets = Liabilities + Equity` to balance).
**Origin:** Investigation triggered by `docs/refactoring/graham-floor-metrics-spec.md` (R2 risk discussion). Discovered while validating whether to derive `TotalLiabilities` from `TotalAssets − StockholdersEquity` for NCAV.
**Blocks:** No production work. Graham-floor metrics ship around it via direct `us-gaap:Liabilities` XBRL preference (see `graham-floor-metrics-spec.md` §4.4).
**Related specs:** `docs/refactoring/graham-floor-metrics-spec.md`, `docs/refactoring/industry-classification-unification-spec.md` (similar "two parallel paths" theme).
**Phase 0 progress (2026-05-16):** Added 4 plug fields to `FinancialData` (`OtherCurrentAssets`, `OtherNonCurrentAssets`, `OtherCurrentLiabilities`, `OtherNonCurrentLiabilities`); SEC parser populates them at end of `parsePeriodData` via `computePlugs`. Empirically zero behavior change (replay-verified on AAPL + MSFT, timestamp-only drift). Property test + ticker-basket integration test + persistence round-trip pin the components-sum-to-umbrellas invariant. SQLite-side schema migration deferred to Phase 1+ via a flip-gate test. Phase 1 (`recomputeUmbrellas` shadow shim) is now unblocked. See `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` for design + `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-0-implementation-plan.md` for the Phase 0 plan.

---

## Context

The financial datacleaner (`internal/services/datacleaner/`) applies a battery of adjuster rules (A1 goodwill exclusion, A2 intangible writedown, A5 inventory writedown, valuation-allowance, etc.) that mutate `entities.FinancialData` in place before the valuation engine consumes it. Three structural issues coexist in the current implementation, exposed by the MXL Q1 2026 case bundled in `artifacts/2026-05-03/MXL/req_9f02dfbc-0993-4508-b2eb-ffff52fd71f6/`:

### B1 — Component / umbrella desync

`assets.go:228-232` reduces `Inventory` and `TotalAssets` together, but **does not propagate** to `CurrentAssets` even though inventory is a current-asset component.

| Field | `10-clean-input.json` | `10-clean-output.json` | Expected after a $34.34M inventory writedown |
|---|---:|---:|---:|
| `inventory` | $85,839,000 | $51,503,400 (−$34.336M) | ✓ |
| `total_assets` | $771,267,000 | $387,402,067 (−$383.86M, sum of all 3 adjusters) | ✓ |
| `current_assets` | $249,450,000 | $249,450,000 (unchanged) | ❌ should be $215,114,400 |

Any consumer that reads `CurrentAssets` post-cleaning sees a stale value. The full list of consumers should be audited; at minimum the new NCAV computation in `internal/services/valuation/graham.go` and the working-capital checks in the existing equity-bridge.

### B2 — `StockholdersEquity` is never mutated by any adjuster

Grep confirms zero `StockholdersEquity` mutations across `internal/services/datacleaner/`:

```
$ grep -rn "StockholdersEquity\s*[-+*/]=\|\.StockholdersEquity\s*=" internal/services/datacleaner/
(no matches)
```

After cleaning, MXL has `total_assets = $387.4M`, `stockholders_equity = $454.2M`, implied `total_liabilities = −$66.8M` — a violation of the accounting identity. This was harmless until now because no downstream math used `stockholders_equity` directly, but every new feature that needs a balanced balance sheet (NCAV, tangible book equity per share, ROE-based screens, distress signals) hits this immediately.

### B3 — Conflation of "valuation overlay" with "as-of restatement"

The deeper issue. Two semantically distinct rule families share the same mutation pipeline:

| Rule type | Examples (current code) | Conceptual meaning | Should restate balance sheet? |
|---|---|---|---|
| **As-of restatement** | A5 inventory obsolescence; A2 intangible impairment; valuation-allowance on DTAs | "This asset is worth less than reported. The balance sheet was misstated." | **Yes** — flows through equity (real economic loss). |
| **Valuation overlay** | A1 goodwill exclusion (Damodaran-style ROIC normalization); capitalised-software exclusion; lease capitalisation | "For invested-capital / ROIC analysis, exclude this. The reported balance sheet itself is correct as filed." | **No** — overlay only, leave underlying data unchanged. |

A1's *wording* in `assets.go:107` is correct: `"goodwill_exclusion: Excluded %.0f goodwill from asset base (%.1f%% of assets)"`. But the *implementation* mutates `data.TotalAssets -= originalGoodwill` and `data.Goodwill = 0.0`, restating the balance sheet. The wording and the code disagree.

For MXL the scale matters: $318.6M of goodwill exclusion (45.1% of pre-clean assets) drives most of the $383.86M total-assets reduction. Treating it as an overlay rather than a restatement closes the bulk of the asymmetry.

---

## Why it matters

- **NCAV ships with derived-fallback warnings until this is fixed.** The Graham-floor spec's TotalLiabilities resolution chain (direct XBRL → derive → unresolved) routes around the cleaner asymmetry by preferring as-reported XBRL. But for any ticker where the SEC parser misses the umbrella tag (uncommon but real for some ADRs / 20-F filers), the derived path will fire and emit a warning. Fixing DC-1 lets NCAV consume cleaned values directly without the workaround.
- **Future features will compound.** Distress screens (Altman-Z, Piotroski-F), ROE / ROA decompositions, P/B ratios, sector-relative book-value comparisons all need `Assets = Liabilities + Equity` to hold. Each one will either (a) work around the cleaner like NCAV does, accreting workarounds; or (b) silently produce wrong numbers.
- **The current data-quality score (90, "A" grade in MXL's case) doesn't catch the asymmetry.** The score evaluates whether rules applied without errors, not whether the resulting balance sheet is internally consistent. Fixing DC-1 also surfaces the need for a balance-sheet-identity check in the cleaner's quality assertions.

---

## Proposed direction (sketch — formal ARCH cycle required before implementation)

Three layers, in dependency order. Each layer alone is insufficient; the combination is the proper fix.

### Layer 1 — Component primitive

Forbid direct mutation of "umbrella" values (`TotalAssets`, `CurrentAssets`, `TotalLiabilities`). Adjusters mutate components only; umbrellas are recomputed:

```go
// internal/services/datacleaner/recompute.go (new)
func recomputeUmbrellas(d *entities.FinancialData) {
    d.CurrentAssets = d.CashAndCashEquivalents +
        d.AccountsReceivable + d.Inventory + d.PrepaidExpenses + d.OtherCurrentAssets
    d.TotalAssets = d.CurrentAssets + d.PPE + d.Goodwill + d.OtherIntangibles +
        d.DeferredTaxAssets + d.OtherNonCurrentAssets
    // (similar for liabilities side)
}
```

Called once at the end of the adjustment pipeline. Adjusters become single-purpose: A5 mutates `Inventory` only; A2 mutates `OtherIntangibles` only; the umbrella propagation is automatic.

**Cost:** moderate. `entities.FinancialData` may need new component fields it doesn't have today (e.g. `OtherCurrentAssets`, `OtherNonCurrentAssets`); SEC parser needs to populate them; every adjuster's existing `data.TotalAssets -=` line is removed.

### Layer 2 — Separate adjuster types: `Restater` vs `Overlay`

Introduce two distinct adjuster interfaces. Restaters apply mutations and stamp `data.AdjustmentLedger` for the equity offset. Overlays produce a parallel view without touching the underlying data.

```go
type Restater interface {
    Apply(d *entities.FinancialData) (delta float64, err error) // mutates components, returns equity-offset amount
}

type Overlay interface {
    ApplyOverlay(d *entities.FinancialData, view *InvestedCapitalView) error // mutates view only
}
```

Existing rules get reclassified:
- **Restaters:** A2 (intangible impairment), A5 (inventory writedown), valuation-allowance on DTAs.
- **Overlays:** A1 (goodwill exclusion for invested capital), capitalised-software exclusion, lease capitalisation for ROIC.

### Layer 3 — Three named views, not one

Replace the current single `*entities.FinancialData` output with a triple:

```go
type CleanedFinancialData struct {
    AsReported        *entities.FinancialData  // direct from SEC parser; never mutated post-fetch
    Restated          *entities.FinancialData  // AsReported + Type-1 adjusters; equity offsets applied
    InvestedCapital   *entities.FinancialData  // Restated + Type-2 overlays; for DCF/ROIC
    Adjustments       []Adjustment             // audit trail (existing)
    Flags             []Flag                   // existing
}
```

Each downstream consumer chooses the view that matches its semantic intent:
- NCAV → `AsReported.CurrentAssets`, `AsReported.TotalLiabilities`
- Tangible book equity per share → `Restated.TangibleAssets`, `Restated.StockholdersEquity`
- DCF / WACC / revenue-multiple → `InvestedCapital.*`
- Per-share metrics that mix views (e.g. `dcf_value_per_share`) read each value from its semantically-correct view

This is the largest change. Affects:
- `entities.CleaningResult` (replaces flat `*FinancialData` output)
- Every downstream consumer in `internal/services/valuation/` (audit needed)
- Persistence layer (`valuation_results` may need view-prefixed columns or a single canonical view per consumer)
- Replay tooling (the bundle artifacts currently snapshot one cleaned view; would snapshot three)

---

## Open questions for the formal ARCH cycle

1. **Ledger-based equity offsets vs. derive-from-deltas?** When a Restater mutates components, do we explicitly track the equity offset in an `AdjustmentLedger` field on `FinancialData`, or recompute equity at the end as `AsReported.StockholdersEquity − sum(restater.deltas)`? Ledger is more transparent; derivation is less code.
2. **Component completeness.** Does the SEC XBRL parser already populate every component the cleaner needs to recompute umbrellas (cash, AR, inventory, prepaid, other-current; PPE, goodwill, intangibles, DTAs, other-non-current)? An audit pass on `internal/infra/gateways/sec/parser.go` is required before the refactor scope is real.
3. **Persistence shape.** The `valuation_results` and `financial_data` tables currently store a single denormalised view. Three views imply either three rows per fetch (with a `view` discriminator), three sets of columns, or canonical-view-per-consumer with the others computed on read. Trade-offs around storage cost vs. replay parity.
4. **Migration / backfill.** Existing cached cleaned data uses the old single-view shape. Cut over with a `cache_version` bump? Or run dual-shape writes for a transition period?
5. **Replay parity.** `internal/observability/replay/` snapshots the cleaning output as `10-clean-output.json`. The new shape needs a backwards-compat read path, or replay's golden bundles will all fail to deserialize.

---

## Suggested next step

Open an ARCH cycle to produce `docs/refactoring/datacleaner-component-primitive-and-parallel-views-spec.md` covering the three layers above with concrete file-by-file deltas, a phased migration plan (Layer 1 → Layer 2 → Layer 3, each independently mergeable), and a regression-test strategy that pins the existing DCF outputs against pre-refactor values for a representative ticker basket (AAPL, MSFT, JNJ, KO, T, F, AMD, MXL, TSM, BABA).

Estimate before formal scoping: 2–3 weeks of focused work, comparable in size to the Phase 2.D replay-tooling refactor.

---

## Acceptance for closing this tracker

- [ ] ARCH spec filed at `docs/refactoring/datacleaner-component-primitive-and-parallel-views-spec.md`.
- [ ] BACKEND implements Layer 1 (component primitive); existing tests stay green; new property-based test asserts `Assets = Liabilities + Equity` post-clean for a randomized fixture set.
- [ ] BACKEND implements Layer 2 (Restater / Overlay split); A1 reclassified as Overlay; A2 + A5 as Restaters with explicit equity offsets.
- [ ] BACKEND implements Layer 3 (three views); downstream consumers migrated one by one; replay golden bundles regenerated.
- [ ] Graham-floor metrics derive `TotalLiabilities` from `AsReported` view, dropping the warn-on-derivation fallback path.
- [ ] CLAUDE.md "Common Gotchas" section updates to reflect the new view shape; the existing single-view note is retired.
