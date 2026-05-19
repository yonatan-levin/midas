# T2-BS-3 — SEC parser emits `TotalLiabilities = 0` for AMD and KO across all periods

**Status:** OPEN — surfaced by DC-1 Phase 1 shadow-mode shim on first basket run (2026-05-19)
**Severity:** MEDIUM (data-quality bug; existing consumers tolerate it via the Graham-floor `resolveTotalLiabilities` fallback at `internal/services/valuation/service.go`, but Phase 2's `Adjuster`-based view reconstruction will inherit the parser zero unless this is addressed)
**Filed:** 2026-05-19 by DC-1 Phase 1 post-merge follow-up
**Phase context:** Discovered via `internal/integration/testdata/recompute-shadow/{AMD,KO}.json` after DC-1 Phase 1 merged to master (`2d916a7`). The shadow-mode `recomputeUmbrellas` shim recorded 12 `clamp_suspected: true` divergences each for AMD and KO TotalLiabilities — 12 of 12 periods covered in `artifacts/tier2-baseline/2026-05-15/`.
**Owner:** SEC-parser maintainer (whoever lands the fix) coordinated with DC-1 Phase 2 ARCH (who decides prerequisite-vs-carveout disposition)
**Blocks:** Not strictly blocking, but a Phase 2 prerequisite candidate (see "Phase 2 dependency analysis" below)

---

## Empirical observation

DC-1 Phase 1 shipped a shadow-mode observer that runs at the end of `CleanFinancialData` and recomputes each balance-sheet umbrella from `sum(components) + Phase 0 plug`, then logs a WARN per divergence. The committed snapshots at `internal/integration/testdata/recompute-shadow/{AMD,KO}.json` carry 12 entries each shaped like:

```json
{
    "period": "2023-12-31",
    "umbrella": "TotalLiabilities",
    "reported": 0,
    "recomputed": 49126000000,
    "delta": 49126000000,
    "plug": 0,
    "clamp_suspected": true
}
```

Pattern across both tickers, every period:
- `reported: 0` — the cleaner's `fd.TotalLiabilities` after `CleanFinancialData` is exactly zero
- `recomputed: <positive, billions>` — `sum(known_components) + plug` is genuinely non-zero (the underlying liability data IS present in the components)
- `plug: 0` — Phase 0's `computePlugs` clamped the residual to 0 (because `sum(components) > umbrella == 0`, the residual would have been negative)
- `clamp_suspected: true` — Phase 1's recompute predicate correctly identifies this as a Phase 0 clamp event, not a cleaner-side adjuster mutation

Distinct from the AAPL TotalLiabilities pattern (`reported: ~290B`, `clamp_suspected: false`, ~$11B delta from B1 lease-capitalization), which IS a Phase 2 punch-list target. AMD/KO's `reported: 0` is a more upstream problem: the cleaner never had `TotalLiabilities` to mutate in the first place.

## Root-cause hypothesis

The SEC EDGAR parser at `internal/infra/gateways/sec/parser.go` is producing `TotalLiabilities = 0` while populating individual liability components (`TotalDebt`, `AccountsPayable`, `OperatingLeaseLiability`, …) non-zero. Three candidate mechanisms (not yet investigated):

1. **XBRL tag dropout** — AMD's and KO's 10-K / 10-Q filings tag the umbrella `us-gaap:Liabilities` concept differently than the parser's `findValue` chain expects. Possibly using a non-standard taxonomy concept or the IFRS-full equivalent that the parser isn't looking for under `us-gaap`. (See `parser.go::findValue` and the XBRL tag-mapping config under `config/datacleaner/`.)
2. **`findValue` returning 0 on tag-present-but-no-value** — if the XBRL serialization has `<us-gaap:Liabilities contextRef="..."/>` (empty element) or a numeric value of "0" in a context that doesn't apply to the filer's fiscal year, `findValue` may parse it as 0 rather than skipping and falling back.
3. **Filer-side reporting choice** — AMD and KO may be using a non-standard line-item structure (e.g., reporting `LiabilitiesCurrent` + `LiabilitiesNoncurrent` separately without emitting the rolled-up `Liabilities` umbrella). The parser's `findValue` then has no fallback chain from the split fields to the umbrella.

Mechanism 3 is the most likely — KO's 10-K is known to break out the umbrella into current/non-current splits without the rollup; AMD may follow a similar pattern. Investigation should start by pulling AMD's and KO's 10-K from `data.sec.gov/api/xbrl/companyfacts/CIK<padded>.json` and grepping for `Liabilities` concept variants. The parser may need a fallback rule along the lines of `if Liabilities missing { Liabilities = LiabilitiesCurrent + LiabilitiesNoncurrent }` (analogous to existing fallbacks in `parser.go`).

## Phase 2 dependency analysis

DC-1 Phase 2's `Adjuster` interface refactor will produce three views (`AsReported`, `Restated`, `InvestedCapital`) reconstructed from the typed component fields. For AMD and KO:

- **`AsReported.TotalLiabilities`** will inherit the parser zero unchanged (Phase 2 honors source data faithfully)
- **`Restated.TotalLiabilities`** will reconstruct from `sum(components) + plug` and produce a non-zero value (the recompute math is exactly what `Restated` uses), correctly surfacing the components-derived liability total
- **`InvestedCapital.TotalLiabilities`** will follow the same component-sum reconstruction

So Phase 2 *partially* hides this bug from downstream consumers via `Restated` and `InvestedCapital` views — but `AsReported` still surfaces the zero, and any consumer that reads `AsReported` for AMD/KO will get the bad value.

Two dispositions for Phase 2 ARCH to choose between:

| Option | Mechanism | Pros | Cons |
|---|---|---|---|
| **A — Fix in parser before Phase 2 starts** | Add `Liabilities = LiabilitiesCurrent + LiabilitiesNoncurrent` fallback (or similar mechanism-3 fix) in `parser.go`. Re-capture AMD/KO bundles. Phase 1's recompute snapshots should then show `reported > 0` with `clamp_suspected: false` (potentially a smaller real cleaner-side divergence remains, becoming a normal Phase 2 punch-list item). | `AsReported` view becomes truthful for these tickers. Existing Graham-floor fallback at `service.go::resolveTotalLiabilities` becomes unnecessary for AMD/KO. All three Phase 2 views agree on the same liability total. | Parser-side scope creep into the DC-1 phase boundary. Requires re-capturing the tier2-baseline bundles, which Tier 2 P1-P4 worktrees may also depend on. |
| **B — Carve-out: document AMD/KO as `AsReported`-untrustworthy for TotalLiabilities** | Keep the parser as-is. Document in Phase 2 spec + closeout that `AsReported.TotalLiabilities == 0` for AMD/KO is a known parser quirk; consumers must use `Restated.TotalLiabilities` (or the Graham-floor fallback) for these tickers. | Phase 2 scope stays cleaner. The `Restated` view's component-sum reconstruction was always going to be the more trustworthy surface anyway. | The `AsReported` view loses some "faithful to source data" credibility; future tickers with the same XBRL quirk will silently inherit the bug. |

Recommendation: defer to Phase 2 ARCH after they design the view-reconstruction layer. If they're already going to make `Restated` the canonical surface for cleaner-side consumers, Option B has low marginal cost. If they want `AsReported` to be the truthful-to-XBRL view that downstream consumers can trust, Option A is right.

## Reproduction

From master HEAD (post `b8e9c77`):

```bash
# Re-run the shadow-mode integration test to regenerate snapshots:
go test ./internal/integration/... -run TestDataCleanerRecompute -count=1

# Inspect the AMD / KO snapshots:
cat internal/integration/testdata/recompute-shadow/AMD.json | jq '.divergences[] | select(.umbrella == "TotalLiabilities")' | head
cat internal/integration/testdata/recompute-shadow/KO.json  | jq '.divergences[] | select(.umbrella == "TotalLiabilities")' | head

# Production WARN grep (when running the live server):
rg '"phase":"DC-1-P1-shadow"' /var/log/midas/*.log | rg '"ticker":"(AMD|KO)"' | rg '"umbrella":"TotalLiabilities"'

# Pull AMD's raw SEC companyfacts for investigation:
curl -A "midas-dev contact@example.com" 'https://data.sec.gov/api/xbrl/companyfacts/CIK0000002488.json' | jq '.facts."us-gaap" | keys | .[] | select(. | test("Liabilities"; "i"))'
# (CIK 2488 = AMD; pad to 10 digits for the URL)
# Same pattern for KO: CIK 0000021344
```

## Affected consumers

| Consumer | Behavior today | Behavior post-Phase-2 (Option A) | Behavior post-Phase-2 (Option B) |
|---|---|---|---|
| Graham-floor diagnostic (`service.go::resolveTotalLiabilities`) | Falls back to `TotalAssets − StockholdersEquity` derivation, emits WARN | Direct read succeeds | Falls back via `Restated` view |
| DC-1 Phase 1 recompute shim | Emits WARN, records in snapshot | No WARN (recomputed ≈ reported) | WARN persists (recompute differs from `AsReported`) |
| Future Altman-Z / leverage ratios (Phase 2+) | N/A (not yet built) | Direct read | Must use `Restated` view |
| Future P/B (Phase 2+) | N/A | Direct read | Must use `Restated` view |

## Cross-references

- DC-1 Phase 1 shadow-analysis report: `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md` (cluster `B1-PARSER-TL-ZERO` describes this same finding)
- DC-1 spec (Phase 2 view design): `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`
- DC-1 tracker: `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md`
- Phase 0 closeout (`computePlugs` clamp behavior): `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-0-closeout.md`
- SEC parser entry point: `internal/infra/gateways/sec/parser.go::parsePeriodData`
- Plug computation: `internal/infra/gateways/sec/plugs.go::computePlugs`
- Graham-floor fallback: `internal/services/valuation/service.go::resolveTotalLiabilities`
- Sibling parser-side trackers (per CLAUDE.md): T2-BS-1 (`DividendsPerShare=0` for FIN-prefix tickers), T2-BS-2 (cleaner-side bug — JPM bundle missing `10-clean-output.json`). T2-BS-3 (this file) extends the T2-BS-* series for parser-side balance-sheet dropouts.

## Change log

| Date | Change |
|------|--------|
| 2026-05-19 | Filed by DC-1 Phase 1 post-merge follow-up. Empirical evidence from committed `recompute-shadow/{AMD,KO}.json` snapshots at master `b8e9c77`. Cross-referenced from DC-1 tracker. |
