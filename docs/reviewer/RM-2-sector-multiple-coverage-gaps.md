# RM-2 — Sector EV/Revenue multiples are coarsely categorised; semis/biotech/SaaS hit a 1.5× MFG default

**Status:** PHASE 1 RESOLVED (2026-05-23); PHASE 2 (Damodaran sprint) IMPLEMENTED 2026-06-29 on branch `feat/rm-2-p2-damodaran-multiples` (Phase 1 retained as fallback; RM-2.5 DEFERRED). RM-2.4 (regional split) remains the only OPEN follow-up. Filed 2026-05-06 during live-API verification of the Graham-floor PR.
**GitHub issue:** [#14](https://github.com/yonatan-levin/midas/issues/14) (mirrors this tracker; covers Phase 2 + the absorbed TDB-9 override-file work in RM-2.5).
**Severity:** Major. Compounds with RM-1 to produce silent ~25× understatements of fair value for negative-OI tickers in tech/biotech/finance sectors.
**Origin:** Live MXL response showed `revenue_multiple` applying `1.5×` (the MFG default) to a fabless semiconductor whose peer-group EV/Revenue is in the 6-12× range. Investigation revealed `config/industry_multiples.json` has only a handful of broad sector entries and no semiconductor-specific bucket.
**Blocks:** Nothing — long-standing gap, not a regression.
**Related specs:** `docs/reviewer/RM-1-revenue-multiple-quarterly-vs-ttm.md` (revenue base; the multiplier and the base are independent fixes for the same headline number), `docs/refactoring/spec/industry-classification-unification-spec.md` (the underlying classifier-emits-coarse-codes problem), `docs/reviewer/archive/TDB-9-industry-mapping-expansion.md` (datacleaner override-file coverage — deferred work **absorbed here**; see "Absorbed from TDB-9" below).

---

## Context

`internal/services/valuation/models/revenue_multiple.go:142-167` does longest-prefix matching on the industry code:

```go
func (m *RevenueMultipleModel) getMultiple(industry string) float64 {
    upper := strings.ToUpper(industry)
    if multiple, ok := m.multiples[upper]; ok { return multiple }
    // longest-prefix-match at underscore boundary
    bestKey := ""
    bestVal := 0.0
    for code, multiple := range m.multiples {
        if code == "default" { continue }
        if upper == code || strings.HasPrefix(upper, code+"_") {
            if len(code) > len(bestKey) { bestKey = code; bestVal = multiple }
        }
    }
    if bestKey != "" { return bestVal }
    if defaultMultiple, ok := m.multiples["default"]; ok { return defaultMultiple }
    return DefaultEVRevenueMultiple // 2.0
}
```

The lookup logic is sound. The problem is the **table is sparse**. `config/industry_multiples.json` has the EV/Revenue map populated for a handful of broad sectors (TECH, MFG, RETAIL, HEALTH, FIN, etc., all with single-digit multiples). The classifier (`internal/services/datacleaner/industry/classifier.go`) emits richer codes — `TECH_SEMI`, `TECH_SAAS`, `HEALTH_BIOTECH`, `FIN_BANK` — but those codes have no entries in the multiples table, so they fall through to the broader sector code's value (or to default 2.0).

For MXL specifically:
- SIC 3674 (Semiconductors) → classifier emits `MFG` (currently)
- `MFG` → 1.5× per `industry_multiples.json`
- Real-world fabless semiconductor sector: 6-12× EV/Revenue (NVDA ~30×, MRVL ~12×, AMD ~8×, MCHP ~7×, peer median ~6-8×)
- 4-7× error in the multiplier alone

Combined with RM-1's quarterly-revenue bug, MXL's headline number is off by ~25×. The user noticed because $1.32 < $2.85 — but the *correct* fair value for MXL using TTM revenue + a sector-appropriate multiple is in the $35-45 range, only ~50% below the $80 market price. That's a "this is a growth-priced stock and you're paying a premium" signal, not a "this stock is wildly overvalued" signal. The current model gets the qualitative direction right but the magnitude catastrophically wrong.

## Why it matters

1. **Silent for the consumer.** No warning fires. The headline number looks credible. A workflow that filters tickers by "fair value < market price by 50%" would flag every healthy semi as overvalued.
2. **Worst on the most interesting tickers.** Negative-OI semis (MXL, INTC at points, every clinical-stage biotech, every pre-profit SaaS) are exactly where investors most want a forward-looking valuation. They're also exactly where the coarse-bucket multiplier is most wrong.
3. **No graceful degradation.** Today's longest-prefix match means any unmapped code falls back to the broadest available sector — so `TECH_AI_INFRASTRUCTURE` gets the `TECH` default, not a sector-appropriate multiple. There's no "I don't know your sector, here's a warning + a wide range" mechanism.
4. **Annual maintenance debt.** Sector multiples drift. NVDA's run-up has dragged the semi sector multiple from ~5× to ~12× since 2023. A static config file frozen at one point in time is wrong by construction within 12-18 months.

## Proposed fix (one of)

### Option A — Add the missing high-volume buckets manually

Pure data-config change to `config/industry_multiples.json`. Add ~10 entries for the buckets the classifier already emits:

```json
{
  "ev_revenue_multiples": {
    "default": 2.0,
    "MFG": 1.5,
    "MFG_SEMI": 6.5,            // NEW — fabless semis
    "MFG_SEMI_FABLESS": 7.5,    // NEW — even more specific
    "TECH": 4.0,
    "TECH_SAAS": 8.0,           // NEW
    "TECH_SAAS_VERTICAL": 9.5,  // NEW
    "TECH_AI": 12.0,            // NEW
    "HEALTH": 3.0,
    "HEALTH_BIOTECH": 5.0,      // NEW — clinical-stage often higher
    "HEALTH_PHARMA": 4.0,       // NEW
    "FIN": 2.5,
    "FIN_BANK": 2.0,            // NEW
    "FIN_INSURANCE": 1.0,       // NEW
    "RETAIL": 1.0,
    "ENERGY": 1.5,
    "UTIL": 2.0,
    "TELECOM": 1.5
  }
}
```

The classifier and the longest-prefix-match code don't need changing — they already do the right thing once the data is present.

**Pros:** trivial; ~15 minutes of work; immediate impact; reversible.
**Cons:** manual maintenance; numbers go stale; no source-of-truth provenance; values are someone's best guess at point in time.
**Risk:** low. Worst case: numbers are slightly off in the same direction as today, just less so.

### Option B — Adopt Damodaran's annual sector tables as the source of truth

[Aswath Damodaran](https://pages.stern.nyu.edu/~adamodar/) publishes free annual datasets at NYU Stern's site, covering ~95 industry sectors with EV/Revenue, EV/EBITDA, P/E, P/B, and other multiples. The data is widely cited as the gold standard for sector benchmarks. Confirmed via web research (2026-05): **still canonical, no modern free alternatives match scope/quality.** Bloomberg, FactSet, S&P Capital IQ are paid; Yahoo screener lacks sector aggregates; FRED and SEC publish no multiples data.

**Concrete dataset URLs and formats** (verified January 2026 refresh):

| Dataset | URL | Format | Notes |
|---|---|---|---|
| US EV/EBITDA by sector | `https://pages.stern.nyu.edu/~adamodar/pc/datasets/vebitda.xls` | Excel `.xls` | Most-cited file |
| Global EV/Sales (EV/Revenue) by sector | `https://pages.stern.nyu.edu/~adamodar/pc/datasets/psGlobal.xls` | Excel `.xls` | Includes US, Europe, Japan, China |
| Industry name canonical list | `https://pages.stern.nyu.edu/~adamodar/pc/datasets/indname.xls` | Excel `.xls` | Lists all ~95 Damodaran industries |
| Landing page (HTML tables) | `https://pages.stern.nyu.edu/~adamodar/New_Home_Page/datacurrent.html` | HTML | Browseable mirror |
| Archives | `https://pages.stern.nyu.edu/adamodar/pc/archives/data.html` | HTML | Historical snapshots back to 1998 |

**Refresh cadence:** annual, **early January** (most recent: 2026-01-09). One refresh per year is sufficient — sector multiples don't move enough mid-year to justify quarterly pulls.

**SIC→Damodaran mapping is implementer-built.** Web research confirmed: there is **no published, maintained crosswalk** between SEC SIC codes (~600) and Damodaran's ~95 industries. Practitioners build their own. Typical effort estimate: **4-8 hours of focused work** for a junior-to-mid analyst, with the bulk being keyword-based clustering of SICs against Damodaran's industry names. Edge cases (~1% of SICs that don't fit any Damodaran bucket, e.g. SIC 7389 "Business Services NEC") default to "Other" or to the parent NAICS sector.

Implementation:
1. Pull `vebitda.xls` and `psGlobal.xls` quarterly-ish (annually plus a sanity-check pull mid-year). Store under `data/damodaran/2026-01-09/` (one snapshot per refresh date).
2. Add `config/damodaran_sector_multiples.json` — the canonical multiples table, transformed from the Excel files. Format mirrors the existing `industry_multiples.json` keyed by Damodaran's "Industry Name" (e.g., `"Semiconductor"`, `"Software (System & Application)"`).
3. Add `config/sic_to_damodaran.json` — implementer-built SIC code → Damodaran-industry-name lookup table. ~600 SIC codes covered; one-to-many is fine (multiple SICs map to the same industry). Edge cases: `"7389"` → `"Business & Consumer Services"` (parent NAICS bucket).
4. Modify the classifier (or the `revenue_multiple.go` lookup) to first try the Damodaran lookup via SIC, falling back to the existing broad-sector match if the SIC is unmapped.
5. Add a `damodaran_dataset_date: "2026-01-09"` metadata field on `FairValueResponse` so consumers can see how stale the data is.
6. Annual refresh: a small script (`scripts/refresh_damodaran.go`) that pulls the `.xls` files via HTTP, transforms to the JSON format using a small XLS reader (e.g. `github.com/xuri/excelize/v2`), and bumps the dataset date. Track in CI as a calendar reminder for early-February (gives Damodaran a few weeks past Jan 9 to publish the year's update).

**Pros:** authoritative source (Damodaran is THE canonical free reference); ~95 sectors covered; updated annually; auditable provenance (dataset_date + URL); aligns with practice at every major equity-research desk.
**Cons:** ~200 lines of plumbing for the lookup + ~4-8 hours to build the SIC mapping + annual refresh operational burden; Damodaran's industry boundaries don't always align cleanly with our existing sector codes (one-time translation work to the existing `MFG/TECH/HEALTH` codes).
**Risk:** low-medium. The biggest risk is that the SIC→Damodaran mapping has gaps that surface as silent fall-throughs to the broad default. Mitigate with a CI check that asserts every SIC code observed in our 60-day request log has a Damodaran mapping.

### Option C — Live multi-source sector multiples with weekly refresh

Combine multiple sources:
- Damodaran (annual baseline)
- Yahoo Finance / Finzive screener (weekly current peer-group medians)
- SEC EDGAR XBRL aggregations (quarterly, where available)

Compute a weighted blend and refresh weekly via a scheduler job.

**Pros:** most accurate; tracks sector-multiple drift in near-real-time; provides a confidence interval (not just a point estimate).
**Cons:** significant infrastructure (scheduler integration, source-blending logic, peer-group definition algorithms, anti-gaming guards); scraping fragility; Yahoo screener API instability.
**Risk:** medium-high. The complexity is real and the per-ticker accuracy gain over Option B is small for most sectors.

## Recommendation

**Two-phase rollout.**

**Phase 1: Option A immediately** — ship the missing buckets in `industry_multiples.json` as a hotfix. ~15 minutes; closes the worst MXL-style errors. The fix is config-only, requires no code changes, and is reversible. The classifier already routes SIC 3674 to `MFG` today; in Phase 1 we'd update the classifier to emit `MFG_SEMI` for SIC 3674 (one-line change in `internal/services/datacleaner/industry/classifier.go`).

**Phase 2: Option B as the proper fix** — adopt Damodaran data over a 2-week sprint. Phase 2 supersedes Phase 1; once it ships, the manually-curated buckets in Phase 1 are deleted and replaced with the Damodaran-sourced table.

**Skip Option C for now.** The marginal accuracy gain over Damodaran's annual data is not worth the operational complexity for a single-user investing tool. Re-evaluate if Midas grows into a multi-user platform where weekly accuracy matters.

## Phase 1 — concrete tasks

| ID | Task | File | Effort |
|---|---|---|---|
| RM-2.1.1 | Add `MFG_SEMI` (6.5×), `TECH_SAAS` (8.0×), `TECH_AI` (12.0×), `HEALTH_BIOTECH` (5.0×), `HEALTH_PHARMA` (4.0×), `FIN_BANK` (2.0×), `FIN_INSURANCE` (1.0×) entries | `config/industry_multiples.json` | XS |
| RM-2.1.2 | Update SIC→industry mapping so SIC 3674/3672/3677 emits `MFG_SEMI`; SIC 7372/7371 emits `TECH_SAAS` (or whichever match the existing classifier convention is); biotech SICs emit `HEALTH_BIOTECH` | `internal/services/datacleaner/industry/classifier.go` and/or `config/datacleaner/industry_codes.json` | S |
| RM-2.1.3 | Regression test: assert `getMultiple("MFG_SEMI")` returns 6.5 and not 1.5 | `revenue_multiple_test.go` | XS |
| RM-2.1.4 | Live regression: hit `/api/v1/fair-value/MXL` and assert the warning message includes "6.5x" not "1.5x" | manual or contract-fuzz | XS |

## Phase 2 — concrete tasks

| ID | Task | File | Effort |
|---|---|---|---|
| RM-2.2.1 | Pull Damodaran's `vebitda.html` and `evrev.html` datasets into `data/damodaran/2026-01-15/` (one snapshot per refresh date) | new `scripts/refresh_damodaran.go` | S |
| RM-2.2.2 | Define `config/damodaran_sector_multiples.json` schema with `dataset_date` + `industries[]` | new config | S |
| RM-2.2.3 | Define `config/sic_to_damodaran.json` mapping ~600 SIC codes to ~95 Damodaran industries | new config (mostly data entry) | M |
| RM-2.2.4 | Add `lookupDamodaranMultiple(sic string) (multiple float64, source string, ok bool)` helper | `internal/services/valuation/models/sector_lookup.go` (new) | M |
| RM-2.2.5 | Modify `RevenueMultipleModel.getMultiple` to try Damodaran first, fall back to existing broad-sector match | `revenue_multiple.go` | S |
| RM-2.2.6 | Surface the dataset_date in valuation response under `industry.multiple_source: "Damodaran 2026-01-15"` | `entities.ValuationResult`, `FairValueResponse`, `openapi.yaml` | S |
| RM-2.2.7 | CI check: every SIC code observed in the past 60 days of `valuation_results` has a Damodaran mapping; fail if any are unmapped | `.github/workflows/coverage-check.yml` (or local script) | M |
| RM-2.2.8 | Annual-refresh checklist documentation under `docs/operations/damodaran-refresh.md` | docs | S |

## Tests required

For Phase 1:
- 5+ new rows in `revenue_multiple_test.go` covering the new buckets
- Update `industry_classification_test.go` assertions where SIC mappings change
- Live regression on MXL, NVDA, AMD, AAPL (the AAPL test should be unchanged because it has positive OI and never hits revenue_multiple)

For Phase 2:
- Unit test on `lookupDamodaranMultiple` with mapped + unmapped SIC codes
- Integration test that exercises the fallback chain (Damodaran → broad sector → default)
- Snapshot test pinning the response shape including `industry.multiple_source`
- CI test: assert every observed SIC has a mapping (data integrity gate)

## Out of scope

- Live multiple scraping (Option C). Track separately as `RM-2.3` if/when needed.
- Sector multiples for OTHER metrics (P/E, EV/EBITDA, P/B). The crosscheck module (`internal/services/valuation/crosscheck.go`) already uses sector medians for these; bringing the same Damodaran-based source-of-truth into crosscheck is a related-but-separate cleanup.
- Per-region multiples (Damodaran publishes US, Europe, Japan, China). Phase 2 ships US-only; international tickers (TSM, ASML) get the US-equivalent industry multiple as a stopgap. Track regional split as `RM-2.4` after Phase 2.

## Absorbed from TDB-9 — datacleaner override-file coverage (RM-2.5)

TDB-9 (`docs/reviewer/archive/TDB-9-industry-mapping-expansion.md`, closed 2026-06-09 as a
**documented defer**) covered an *adjacent but distinct* axis: the datacleaner's industry-specific
**rule-override files** — `internal/services/datacleaner/service.go::loadIndustryRules` maps a GICS
sector code to `config/datacleaner/industry/<sector>.json`. Only `45`→`technology.json` and
`25`→`retail.json` exist. The live `ClassifyIndustry` emits only `45`/`20`/`25`, so **`20`
(Industrials) is the single reachable-and-uncovered sector** — it degrades gracefully to base
`rules.json` + a non-fatal warning (a working default, not a bug). The other GICS codes
(10/15/30/35/40/50/55/60) are an override-file namespace the classifier cannot yet emit.

That deferred work has no other home, so it is tracked here (it shares RM-2's classifier-coverage theme):

| ID | Task | File | Effort |
|---|---|---|---|
| RM-2.5.1 | Author a domain-validated `industrials.json` override (or confirm base-rule coverage is sufficient and close the `20` gap) | `config/datacleaner/industry/industrials.json` (new) | M |
| RM-2.5.2 | To cover a not-yet-emitted GICS sector, extend `ClassifyIndustry` to emit the code FIRST — **overlaps RM-2.1.2** (classifier emission) | `internal/services/datacleaner/industry/classifier.go` | M |
| RM-2.5.3 | Add each new `<sector>.json` to the `datacleaner/ledger_invariants_test.go` sync list, then regenerate + **REVIEW** the recompute-shadow snapshots for affected tickers | tests + fixtures | S |

**Distinction from RM-2.1.2:** RM-2.1.2 fixes the *multiplier emission* (classifier → code → `industry_multiples.json`); RM-2.5 fixes the *cleaning-rule overrides* (code → `<sector>.json`). They share the classifier-emission dependency but are independent deliverables. **Driver gate (unchanged from TDB-9):** a sector whose base-rule cleaning is demonstrably wrong, or this RM-2 sprint. The verbatim 5-step add procedure + the Financials/DDM bit-for-bit caution live in the in-code note at `loadIndustryRules` — do NOT add a bare mapping line.

**RM-2.5 disposition (Phase 2 close, 2026-06-29 — B12): DEFER, no code.** RM-2.5 is a *different axis* from the Phase 2 valuation-multiple work — datacleaner cleaning-rule overrides, not EV/Revenue multiples. Only GICS `20` (Industrials) is reachable-and-uncovered, and it already degrades gracefully to base `rules.json` with a non-fatal warning (a working default, not a bug). Authoring `industrials.json` requires domain-validated override content + a reviewed recompute-shadow regeneration (would drift the shadow snapshots and expand the blast radius for no multiple-related benefit), and there is no concrete driver (no demonstrably-wrong Industrials cleaning). Revisit when an Industrials cleaning defect surfaces.

## Acceptance for closing this tracker

### Phase 1 acceptance
- [x] Five+ new sector entries in `industry_multiples.json` covering semi, SaaS, AI, biotech, pharma, banks, insurance. Shipped in `2dadd83` (MFG_SEMI, FIN_BANK, FIN_INSURANCE) and realigned to spec values in this commit (TECH_AI 10.0 → 12.0, HEALTH_BIOTECH 6.0 → 5.0). Full seven-bucket contract now matches the tracker spec exactly: MFG_SEMI=6.5, TECH_SAAS=8.0, TECH_AI=12.0, HEALTH_BIOTECH=5.0, HEALTH_PHARMA=4.0, FIN_BANK=2.0, FIN_INSURANCE=1.0.
- [x] SIC mapping updated so SIC 3674 emits `MFG_SEMI`. Config-only fix in `config/datacleaner/industry_codes.json`; SIC 3672 and 3677 also map to MFG_SEMI for parity. Pinned by `TestClassify_RM2P1_NewSubIndustries`.
- [ ] Live MXL response shows `Applied 6.5x EV/Revenue multiple for MFG_SEMI sector` warning string instead of 1.5×. *Deferred to a live-API verification pass; the unit test `TestRevenueMultipleModel_GetMultiple_RM2P1ContractFullSeven` asserts the engine returns 6.5x for MFG_SEMI under the embedded production config.*
- [x] No regression on AAPL, MSFT, GOOGL (positive-OI; don't route to revenue_multiple). Verified via `go test ./...` (all 40+ packages PASS).
- [x] All tests pass. `go test ./...` PASS on `feat/rm-2-p1-sector-multiple-buckets`.

### Phase 2 acceptance (additive over Phase 1 — implemented 2026-06-29, branch `feat/rm-2-p2-damodaran-multiples`)
- [x] Damodaran data ingested and committed (snapshot date documented). `config/damodaran_sector_multiples.json` (94 industries, `dataset_date: 2026-01-01`) + `config/sic_to_damodaran.json` crosswalk; both `go:embed`'d. Regenerated bit-for-bit by `cmd/refresh-damodaran` from a live psdata.xls fetch.
- [x] SIC→Damodaran mapping covers the initial high-volume set. **DEVIATION:** the original "all SICs in the past 60 days of `valuation_results`" check needs a request-log corpus we do not have in CI; substituted by the static referential-integrity gate (`models/sector_lookup_integrity_test.go`). Coverage is partial **by design** — unmapped SICs degrade gracefully to the Phase 1 bucket, then `default` 2.0×.
- [x] `industry.multiple_source` surfaces the dataset date in API responses (`"Damodaran 2026-01-01"` vs `"sector-bucket"`; omitempty so DCF/DDM/FFO omit it).
- [x] Annual-refresh runbook documented — `docs/operations/damodaran-refresh.md`.
- [x] CI gate prevents dangling/unmapped references. **DEVIATION (as above):** static referential-integrity (every crosswalk industry name exists as a Damodaran table key; `dataset_date` parses YYYY-MM-DD), not a request-log coverage check.
- [x] Phase 1's manually-curated entries **RETAINED as a fallback tier** (NOT removed). **DEVIATION from the original "removed" wording — explicit human decision:** additive fallback gives zero-regression (any non-Damodaran-resolved path is bit-for-bit identical to pre-Phase-2). Phase 1 buckets now serve unmapped SICs.
- [x] CLAUDE.md updated (build command + gotcha entry); `CalculationVersion` bumped 4.10 → 4.11 (mapped-SIC `revenue_multiple` multiplier is a real output change).
- [x] **Live-API verification (closes the Phase-1 deferred MXL box too).** QA hit the running engine 2026-06-29: `GET /api/v1/fair-value/MXL` → `calculation_method: revenue_multiple`, `industry.multiple_source: "Damodaran 2026-01-01"`, warning `Applied 15.7x EV/Revenue multiple for MFG_SEMI sector` + `multiple_source: Damodaran 2026-01-01 (industry=Semiconductor)`, `calculation_version: 4.11`. The applied **15.7×** is the live Damodaran "Semiconductor" EV/Sales (`15.7006` in the committed table), traceably read from config — **NOT** the 1.5× MFG default and not a hardcoded constant. (This supersedes the Phase-1 estimate of 6.5×: the actual 2026 Damodaran semiconductor multiple is higher — the NVDA-era multiple expansion the "Why it matters #4" section predicted. The Phase-1 unit pin still asserts 6.5× for the *Phase-1 bucket* `MFG_SEMI`, which is the fallback when no SIC maps.) Control: `GET /api/v1/fair-value/AAPL` → `multi_stage_dcf`, `multiple_source` **absent** (omitempty byte-identity confirmed).

**Other Phase-2 deviation:** the source `Date updated:` cell is a `"YYYY.MM"` string (`"2026.01"`), NOT an Excel serial as the plan assumed — canonicalized to first-of-month `2026-01-01`.
