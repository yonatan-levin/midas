# RM-2 Phase 2 — Damodaran annual sector tables as source-of-truth for EV/Revenue multiples

MODE: PLAN_AND_CREATE
ROLE: ARCH
HANDOFF_TO: BACKEND

**Tracker:** `docs/reviewer/RM-2-sector-multiple-coverage-gaps.md` (GitHub #14) — Phase 2 tasks RM-2.2.1–RM-2.2.8 + RM-2.5.
**Worktree:** `midas-rm2-p2-damodaran` / branch `feat/rm-2-p2-damodaran-multiples`.
**Engine version before this change:** `CalculationVersion 4.10`.

---

## Summary

Adopt Aswath Damodaran's annual NYU-Stern sector tables (`psdata.xls`, US "Price and Value to Sales") as the **primary** source for the EV/Revenue multiple applied by the `revenue_multiple` valuation model, with the existing Phase 1 `industry_multiples.json` buckets retained as an **additive fallback tier**. The lookup is SIC-driven: SEC raw SIC → Damodaran industry name → EV/Sales. When the SIC is unmapped, resolution degrades gracefully to today's classifier-code longest-prefix match over the Phase 1 buckets, then to the `default` 2.0×. Provenance (`Damodaran 2026-01-05` vs `sector-bucket`) is surfaced on the response so consumers can audit staleness. A `scripts/refresh_damodaran` tool fetches the live `.xls` and regenerates the committed JSON.

### Goals
- `revenue_multiple` tickers with a mapped SIC get a Damodaran sector EV/Sales multiple instead of a coarse classifier bucket (the MXL 1.5× → ~6× class of error).
- Zero regression: any ticker path that does not resolve a Damodaran multiple produces a **bit-for-bit identical** multiplier to today (Phase 1 fallback preserved exactly).
- Auditable provenance + a documented annual-refresh procedure.
- A CI integrity gate so the crosswalk can never reference a non-existent Damodaran industry.

### Non-goals (out of scope for this PR)
- EV/EBITDA, P/E, P/B sector tables for `crosscheck.go` (related but separate; RM-2 "Out of scope").
- Per-region multiples (US-only; international tickers get the US-equivalent industry — RM-2.4).
- Live multi-source weekly blend (Option C / RM-2.3).
- Full ~600-SIC crosswalk coverage on day one (partial coverage degrades gracefully by design — see RM-2.2.3).
- DDM / FFO / DCF paths — untouched. Only the `revenue_multiple` multiplier changes.

---

## Requirements

The `revenue_multiple` model is the negative-OI / pre-profit fallback. Its `getMultiple()` does longest-prefix matching over `m.multiples` (loaded from embedded `industry_multiples.json`). The table is sparse, so semis/biotech/SaaS fall through to a broad sector multiple (or 2.0×), understating fair value by 4–7× on the multiplier alone.

Phase 1 (shipped) added seven manual buckets (`MFG_SEMI=6.5`, `TECH_SAAS=8.0`, …) keyed on classifier codes. Phase 2 replaces the *primary* source with Damodaran's ~96-industry table keyed by SIC, keeping Phase 1 as fallback.

### Ground-truth constraints (established; do not re-derive)
- `psdata.xls` is **legacy OLE2/BIFF8 `.xls`** (magic `d0cf11e0`). `excelize` reads only `.xlsx` → **MUST use `github.com/extrame/xls`** (add to `go.mod`).
- `psdata.xls` structure: sheet `Industry Averages`; header at row index 7 (`Industry Name`=col0, `Number of firms`=1, `Price/Sales`=2, `Net Margin`=3, `EV/Sales`=4, `Pre-tax Operating Margin`=5); data rows from index 8; **96 US industries**; dataset date in cell B1 (row0,col1) as an Excel serial decoding to `2026-01-05`.
- `data/` is gitignored → the raw `.xls` snapshot is local-only; the **committed canonical artifact is the transformed JSON config** (`config/damodaran_sector_multiples.json`).
- Configs reach the service via **`go:embed`** (`config/embed.go`, package `configfs`; `configfs.Read("industry_multiples.json")`). New configs MUST be added to the `//go:embed` directive or they will not be readable in production/replay.

---

## Architecture

### Decision 1 — SIC-driven lookup; plumb the raw SIC into the model (RM-2.2.5)

`getMultiple` today takes the classifier **code** string (`ModelInput.Industry`), not a SIC. The raw SIC (`historicalData.SICCode`) IS available at the ModelInput-construction site (`service.go:2291`) and is already stamped onto `ValuationResult.SICCodeRaw`. We thread it into the model via a **new additive field on `ModelInput`**:

```go
// router.go ModelInput struct — additive field
SICCode string // raw SEC SIC code (e.g. "3674"); "" when SEC data lacked it
```

Populated in `service.go` ModelInput construction (the single alt-model site at ~line 2296):
```go
Industry: industryCode,
SICCode:  historicalData.SICCode,   // NEW
```

Rationale: minimal, additive, no constructor churn. Other models ignore the field. The model keeps its existing `getMultiple(industry string)` signature for the fallback path and gains a new SIC-first entry point.

### Decision 2 — Damodaran table is package-loaded, mirroring `loadEVRevenueMultiples` (RM-2.2.4)

The Phase 1 multiples are loaded once per model construction from `configfs`. The Damodaran table + crosswalk follow the identical pattern: loaded in `NewRevenueMultipleModel` from embedded JSON, stored on the model struct. Tests inject via the existing `NewRevenueMultipleModelWithMultiples` plus a new sibling that also accepts the Damodaran table (see Tasks). **No DI-container change** — the model self-loads from `configfs`, same as today.

New struct fields on `RevenueMultipleModel`:
```go
damodaran      map[string]float64 // Damodaran industry name -> EV/Sales
sicToDamodaran map[string]string  // SIC code -> Damodaran industry name
datasetDate    string             // e.g. "2026-01-05"; "" when table absent
```

When either Damodaran config is absent/unparseable, the loader logs a warn-and-fallback (same stance as the existing `loadEVRevenueMultiples` error path) and leaves the maps nil → the model behaves exactly like Phase 1 (zero-regression).

### Decision 3 — `lookupDamodaranMultiple` helper (RM-2.2.4)

New file `internal/services/valuation/models/sector_lookup.go`:
```go
// lookupDamodaranMultiple resolves a raw SIC to a Damodaran EV/Sales multiple.
// ok=false when sic is empty, unmapped in sicToDamodaran, or the mapped
// industry name is missing from the damodaran table (dangling crosswalk entry —
// the CI gate prevents this, but the runtime guard keeps it safe).
func lookupDamodaranMultiple(
    sic string,
    sicToDamodaran map[string]string,
    damodaran map[string]float64,
) (multiple float64, industry string, ok bool)
```
Pure, table-driven-testable, no clock/IO. Returns the resolved Damodaran industry name so callers can build the provenance string and warning.

### Decision 4 — `getMultiple` resolution order (RM-2.2.5)

The model gains the SIC at the call site (`Calculate` reads `input.SICCode`). New private method:
```go
// resolveMultiple returns the multiple, a provenance source string, and the
// matched key. Order: (1) Damodaran-by-SIC; (2) Phase 1 longest-prefix over
// m.multiples; (3) "default"; (4) DefaultEVRevenueMultiple.
func (m *RevenueMultipleModel) resolveMultiple(sic, industry string) (multiple float64, source string)
```
- (1) `lookupDamodaranMultiple(sic, m.sicToDamodaran, m.damodaran)` → `source = "Damodaran " + m.datasetDate`.
- (2)/(3)/(4) reuse the existing `getMultiple(industry)` → `source = "sector-bucket"`.

`Calculate` replaces the line `multiple := m.getMultiple(input.Industry)` with `multiple, multipleSource := m.resolveMultiple(input.SICCode, input.Industry)`. The existing warning string keeps its `%.1fx EV/Revenue multiple for %s sector` shape; add a companion `multiple_source:` warning line (mirrors the existing `revenue_base:` line convention) so dashboards can pivot without parsing the response struct.

### Decision 5 — provenance on the response: `Industry.multiple_source` (RM-2.2.6)

The tracker mandates `industry.multiple_source`. Placement on the **`Industry` struct** (not top-level) — it is industry-classification provenance and groups naturally. Flow:

1. `ModelResult` gains `MultipleSource string` (omitempty) — populated only by `revenue_multiple` (DDM/FFO/DCF leave it `""`).
2. `ValuationResult` gains `MultipleSource string \`json:"multiple_source,omitempty"\`` — stamped from `modelResult.MultipleSource` in the alt-model assembly (`service.go:~2399`). The DCF path leaves it `""`.
3. `handlers.Industry` gains `MultipleSource string \`json:"multiple_source,omitempty" example:"Damodaran 2026-01-05"\``.
4. `BuildIndustryFromResult` sets `MultipleSource: result.MultipleSource`. **Adjust the early-return nil guard**: `MultipleSource` does NOT count toward "no classification signal" — keep the guard checking the four existing classification fields only (an SIC-less ticker that somehow had a source would be an anomaly; defensible to drop). This keeps the Industry-absent case byte-identical.

Because `multiple_source` is on `Industry` (nested) and `omitempty`, **DCF / DDM / FFO responses are byte-identical** (they never set it). Only `revenue_multiple` responses gain the field.

### Decision 6 — replay guard (MANDATORY, RM-2.2.6)

`internal/observability/replay/diff.go` has an `init()` reflection guard that panics on every replay test if `FairValueResponse + Industry + SanityCheck` field count drifts from `countFairValueFields()` without a matching `goFieldToJSON` entry. We add **one field to `Industry`**, so:

- **`goFieldToJSON`**: add `"MultipleSource": "multiple_source",` in the `// Industry` block (after `"Match"`).
- **`countFairValueFields()`**: bump the Industry term `5 → 6`. Change the `return 40 + 5 + 8` to `return 40 + 6 + 8`, and update the two godoc comments (`// Industry: 5 fields.` → `6`; the "Current: ... + 5 (Industry) ..." line → `6 (Industry)`, total `53 → 54`).

This MUST land in the **same commit** as the `Industry` struct field (Task B7) or replay tests panic. The reflection guard will confirm correctness (it counts the real struct).

### Decision 7 — refresh tool: `cmd/refresh-damodaran` (RM-2.2.1, RM-2.2.6)

Make it a **`cmd/` entry point** (`cmd/refresh-damodaran/main.go`), not a `scripts/*.go` loose file — consistent with the repo's existing `cmd/migrate`, `cmd/seed-demo-key`, `cmd/hash-key`, `cmd/replay` convention (all are `cmd/<tool>/main.go`). It:
1. Fetches `https://pages.stern.nyu.edu/~adamodar/pc/datasets/psdata.xls` via `net/http` with a descriptive User-Agent (SEC-style: `midas-dcf/refresh-damodaran (contact: <email>)`).
2. Writes the raw snapshot to `data/damodaran/<dataset-date>/psdata.xls` (gitignored; local provenance).
3. Parses via `github.com/extrame/xls`: open workbook → sheet `Industry Averages` → decode B1 serial to `YYYY-MM-DD` → iterate data rows (index 8+), reading col0 (industry) + col4 (EV/Sales).
4. Regenerates `config/damodaran_sector_multiples.json` with `dataset_date`, `source_url`, and the `industries` map.
5. **Does NOT auto-edit `sic_to_damodaran.json`** — the crosswalk is hand-maintained; the tool only refreshes the multiples table + date. After a refresh, run the CI integrity test (Task B9) to catch industry-name renames that orphan crosswalk entries.

The Excel-serial→date conversion: Excel serial `n` → `time.Date(1899,12,30,...).AddDate(0,0,int(n))` (the 1900 leap-year-bug epoch offset). Verify the decoded value equals `2026-01-05` for the current file.

Invocation documented in CLAUDE.md build section:
```bash
go run ./cmd/refresh-damodaran -ua "midas-dcf/refresh-damodaran (you@example.com)"
```

### Files touched

| File | Change |
|---|---|
| `go.mod` / `go.sum` | add `github.com/extrame/xls` (refresh tool dep) |
| `config/embed.go` | extend `//go:embed` to include the two new JSON files |
| `config/damodaran_sector_multiples.json` | **new** — Damodaran industry → EV/Sales + metadata |
| `config/sic_to_damodaran.json` | **new** — SIC → Damodaran industry name crosswalk |
| `internal/services/valuation/models/sector_lookup.go` | **new** — `lookupDamodaranMultiple` + Damodaran loaders |
| `internal/services/valuation/models/revenue_multiple.go` | `resolveMultiple`; load Damodaran tables in ctor; set `MultipleSource` |
| `internal/services/valuation/models/router.go` | `ModelInput.SICCode` field; `ModelResult.MultipleSource` field |
| `internal/services/valuation/service.go` | populate `ModelInput.SICCode`; stamp `ValuationResult.MultipleSource` |
| `internal/core/entities/valuation.go` | `ValuationResult.MultipleSource` field |
| `internal/api/v1/handlers/fair_value.go` | `Industry.MultipleSource`; set it in `BuildIndustryFromResult` |
| `internal/observability/replay/diff.go` | `goFieldToJSON` entry + `countFairValueFields` bump (atomic with handler) |
| `cmd/refresh-damodaran/main.go` | **new** — fetch + parse + regenerate |
| `docs/operations/damodaran-refresh.md` | **new** — annual-refresh runbook |
| `docs/openapi.yaml` | document `industry.multiple_source` |
| `CLAUDE.md` | build-command + gotcha entry |
| `*_test.go` | unit + integration + CI-integrity + replay-count tests |

### Data flow

```
SEC SIC (historicalData.SICCode)
        │
        ▼  service.go: ModelInput.SICCode = historicalData.SICCode
ModelInput ──► RevenueMultipleModel.Calculate
                     │
                     ▼ resolveMultiple(sic, industry)
        ┌────────────┴───────────────────────────────┐
        │ 1. lookupDamodaranMultiple(sic, x-walk, tbl)│  ok ─► "Damodaran 2026-01-05"
        │ 2. getMultiple(industry)  [Phase 1 prefix]  │  ─────► "sector-bucket"
        │ 3. m.multiples["default"] / const 2.0       │
        └────────────┬───────────────────────────────┘
                     ▼ multiple, source
       ModelResult{ MultipleSource: source, ... }
                     │ service.go alt-model assembly
                     ▼
       ValuationResult.MultipleSource
                     │ BuildIndustryFromResult
                     ▼
       FairValueResponse.Industry.multiple_source
```

---

## API Contracts

### Response: `industry.multiple_source` (new, omitempty)

`GET /api/v1/fair-value/{ticker}` for a `revenue_multiple` ticker:
```json
{
  "ticker": "MXL",
  "dcf_value_per_share": 38.42,
  "calculation_method": "revenue_multiple",
  "calculation_version": "4.11",
  "industry": {
    "sic_code": "3674",
    "sic": "MFG_SEMI",
    "heuristic_code": "45",
    "heuristic_name": "Information Technology",
    "match": true,
    "multiple_source": "Damodaran 2026-01-01"
  },
  "warnings": [
    "Applied 15.7x EV/Revenue multiple for MFG_SEMI sector",
    "multiple_source: Damodaran 2026-01-01 (industry=Semiconductor)",
    "revenue_base: source=TTM_4Q revenue=$..."
  ]
}
```
Fallback (unmapped SIC) → `"multiple_source": "sector-bucket"`. DCF/DDM/FFO responses **omit** `multiple_source` entirely (field absent). The exact value strings (`"Damodaran <date>"`, `"sector-bucket"`) are part of the contract — replay tooling and dashboards key off them; do not rename without coordinating consumers.

### `diff.go` change (exact)
```go
// goFieldToJSON, in the // Industry block:
"MultipleSource": "multiple_source",

// countFairValueFields():
return 40 + 6 + 8   // Industry 5 -> 6
```
Plus the two godoc comment updates (Industry 5→6; total 53→54).

### Config schemas (minimal / YAGNI)

`config/damodaran_sector_multiples.json`:
```json
{
  "dataset_date": "2026-01-05",
  "source_url": "https://pages.stern.nyu.edu/~adamodar/pc/datasets/psdata.xls",
  "industries": {
    "Semiconductor": 15.7006,
    "Software (System & Application)": 8.42,
    "Drugs (Biotechnology)": 7.918,
    "...": 0.0
  }
}
```
- `industries` is a flat `name → EV/Sales` map (mirrors `ev_revenue_multiples`' shape). EV/EBITDA is **not** included this PR (YAGNI; the must-have is EV/Revenue).
- All 96 US industries from `psdata.xls` are written by the refresh tool.

`config/sic_to_damodaran.json`:
```json
{
  "version": "1.0.0",
  "description": "SEC SIC code -> Damodaran industry name. Partial coverage degrades to industry_multiples.json (Phase 1). Industry names MUST exist in damodaran_sector_multiples.json (CI-gated).",
  "map": {
    "3674": "Semiconductor",
    "3672": "Semiconductor",
    "3677": "Semiconductor",
    "7372": "Software (System & Application)",
    "7371": "Computer Services",
    "2836": "Drugs (Biotechnology)",
    "8731": "Drugs (Biotechnology)",
    "2834": "Drugs (Pharmaceutical)",
    "6022": "Banks (Regional)",
    "6021": "Bank (Money Center)",
    "6311": "Insurance (Life)",
    "...": "..."
  }
}
```

---

## Module Descriptions

- **`sector_lookup.go`** — owns the Damodaran lookup primitive + the two config loaders (`loadDamodaranMultiples`, `loadSICToDamodaran`, both returning `(map, datasetDate, error)` / `(map, error)`). Pure lookup, table-driven testable. No state.
- **`revenue_multiple.go`** — gains `resolveMultiple` (the 4-tier resolution) and Damodaran-table fields, loaded in the ctor. The forward (RM-3) path is unchanged.
- **`router.go`** — additive DTO fields only (`ModelInput.SICCode`, `ModelResult.MultipleSource`).
- **`cmd/refresh-damodaran`** — the only consumer of `extrame/xls`. Network + filesystem + transform. Not imported by `cmd/server` (no production XLS dependency on the request path).

---

## Crosswalk coverage scope (RM-2.2.3)

Ship a **pragmatic initial set**, not all ~600 SICs. Cover, at minimum:
1. The classifier's existing special-cases: semiconductors (3674/3672/3677), banks (60xx), insurance (63xx/64xx), biotech/pharma (2834/2836/8731), software/SaaS (7372/7371/7389), computer hardware (3571/3572/3576/3577).
2. Common large-cap manufacturing/consumer/energy/retail SICs that appear in the existing test fixtures and replay baselines (inspect `internal/services/valuation/models/testdata/` and `artifacts/tier2-baseline/` for SICs already exercised).
3. The semiconductor + tech + biotech sectors are the highest-value targets (where the Phase 1 error was worst).

Use the 96-industry vocabulary in `damodaran_evsales.csv` (scratchpad) to pick exact industry-name strings — they MUST match the keys the refresh tool writes. Target ~40–80 SIC entries for the initial PR. Unmapped SICs degrade to Phase 1 (that is the whole point of additive fallback). Document the partial coverage in the config `description` and the runbook.

---

## CalculationVersion: bump to 4.11 (recommendation)

The multiplier value **changes for `revenue_multiple` tickers with a mapped SIC** — a real output change on that path. Per the repo's versioning convention (every prior output-changing engine change bumped the stamp), **bump `4.10 → 4.11`** at both stamp sites (`service.go:830` DCF path and `service.go:2399` alt-model path — engine-wide single stamp, consistent with VAL-1 reconciliation). DDM/FFO/DCF primary numerics stay bit-for-bit; only the version stamp and the `revenue_multiple` multiplier drift. Update the six `CalculationVersion == "4.10"` test pins and the OpenAPI/swagger `example` values to `4.11`.

> If BACKEND prefers to defer the bump, the alternative is to scope the change as "fallback-only is bit-for-bit, mapped-SIC is a deliberate new output" — but given the explicit value change, the bump is the honest choice. **Recommendation: bump.**

---

## Tasks by Agent

### BACKEND (numbered, small reviewable chunks — TDD: test first each step)

**B1 — `extrame/xls` dependency.** Add `github.com/extrame/xls` to `go.mod` via `go get`. Commit `go.mod`+`go.sum` only. (No code yet — isolates the dep churn.)

**B2 — Config schemas + initial data.** Create `config/damodaran_sector_multiples.json` (all 96 industries; transcribe EV/Sales from the scratchpad CSV / a refresh run) and `config/sic_to_damodaran.json` (initial ~40–80 SIC set per "Crosswalk coverage scope"). Extend `config/embed.go`'s `//go:embed` directive to include both. Test: `config/embed_test.go` reads both files non-empty and they parse.

**B3 — `sector_lookup.go` (RM-2.2.4).** New file with `loadDamodaranMultiples()`, `loadSICToDamodaran()`, and the pure `lookupDamodaranMultiple(sic, xwalk, tbl)`. Table-driven tests: mapped SIC → correct multiple + industry + ok=true; unmapped SIC → ok=false; empty SIC → ok=false; **dangling crosswalk entry** (SIC mapped to an industry absent from the table) → ok=false.

**B4 — `ModelInput.SICCode` + `ModelResult.MultipleSource` (router.go).** Additive fields. No behavior change yet. Confirm `go build ./...` green.

**B5 — `resolveMultiple` + ctor wiring (revenue_multiple.go, RM-2.2.5).** Load Damodaran tables in `NewRevenueMultipleModel` (warn-and-fallback on load error). Add `resolveMultiple(sic, industry) (float64, string)`. Add a sibling test ctor `NewRevenueMultipleModelWithDamodaran(multiples, damodaran, sicToDamodaran, datasetDate, logger)`. In `Calculate`, replace `m.getMultiple(input.Industry)` with `resolveMultiple` and set `MultipleSource` on the result + emit the `multiple_source:` warning. Tests: (a) mapped SIC returns Damodaran multiple + `"Damodaran <date>"`; (b) unmapped SIC returns Phase 1 prefix multiple + `"sector-bucket"`; (c) absent Damodaran tables → byte-identical to today's `getMultiple` (zero-regression pin); (d) `getMultiple` itself unchanged.

**B6 — Plumb SIC through the service.** Set `ModelInput.SICCode = historicalData.SICCode` at `service.go:~2296`. Stamp `ValuationResult.MultipleSource = modelResult.MultipleSource` in the alt-model assembly (`service.go:~2399`). Add `ValuationResult.MultipleSource` field in `entities/valuation.go`.

**B7 — Response surface + replay guard (RM-2.2.6) — ATOMIC COMMIT.** Add `Industry.MultipleSource` in `fair_value.go`; set it in `BuildIndustryFromResult` (keep the nil-guard on the four existing classification fields). In the **same commit**: add `"MultipleSource": "multiple_source"` to `goFieldToJSON` and bump `countFairValueFields` Industry term 5→6 (+godoc updates). Run `go test ./internal/observability/replay/...` — the `init()` guard must pass (panics if the count/field disagree).

**B8 — CalculationVersion 4.10 → 4.11.** Both `service.go` stamp sites; the six test pins; OpenAPI/swagger examples. (Decision above.)

**B9 — CI integrity gate (RM-2.2.7).** A Go test (e.g. `config/crosswalk_integrity_test.go` or `models/sector_lookup_integrity_test.go`) asserting: every industry-name value in `sic_to_damodaran.json` exists as a key in `damodaran_sector_multiples.json` (no dangling references); `dataset_date` is non-empty and parses as `YYYY-MM-DD`. **Deviation note:** the tracker's "every SIC in the 60-day request log" check needs a request log we do not have in CI; substitute this static referential-integrity check (documented in the test godoc + runbook).

**B10 — Refresh tool (RM-2.2.1).** `cmd/refresh-damodaran/main.go`: fetch psdata.xls (User-Agent flag, default to a placeholder contact), snapshot to `data/damodaran/<date>/`, parse via `extrame/xls` (sheet `Industry Averages`, header row 7, data row 8+, col0/col4, B1-serial date), regenerate `config/damodaran_sector_multiples.json`. Guard: log+exit non-zero if the workbook shape (sheet name, header labels) does not match the expected layout, so a Damodaran format change fails loudly instead of writing garbage. A small unit test on the Excel-serial→date helper (assert serial for 2026-01-05 round-trips).

**B11 — Docs.** Runbook `docs/operations/damodaran-refresh.md` (RM-2.2.8); `docs/openapi.yaml` `industry.multiple_source`; CLAUDE.md build command + a gotcha entry; check the RM-2 tracker Phase-2 boxes.

**B12 — RM-2.5 assessment (defer recommended).** See "RM-2.5" below. No code unless trivial; record the defer in the tracker.

### QA
- Verify `revenue_multiple` ticker (e.g. MXL) response carries `industry.multiple_source = "Damodaran <date>"` and a sector-appropriate multiple (not 1.5×).
- Verify a `revenue_multiple` ticker with an **unmapped** SIC falls back to `"sector-bucket"` and the Phase 1 multiple (bit-for-bit vs pre-change).
- Verify AAPL/MSFT/GOOGL (positive-OI, DCF path) responses **omit** `multiple_source` and are byte-identical (no DCF drift; only the version stamp changes if B8 lands).
- Confirm `go test ./...` green incl. replay package (the `init()` guard).

### REVIEWER
- The replay `diff.go` change is atomic with the `Industry` field (B7) — confirm the count bump matches reflection.
- Zero-regression pin (B5c): absent-Damodaran path must equal today's `getMultiple` exactly.
- Crosswalk industry-name strings exactly match the table keys (B9 gate enforces; spot-check).
- `cmd/refresh-damodaran` is NOT imported by `cmd/server` (no XLS on the request path).
- CalculationVersion bump rationale (B8) — confirm DDM/FFO bit-for-bit invariants hold (only the stamp drifts on those paths).

---

## Spec Updates

**`docs/reviewer/RM-2-sector-multiple-coverage-gaps.md`** — check Phase 2 acceptance boxes as B-tasks land:
- [ ] Damodaran data ingested + committed (snapshot date documented) → B2/B10.
- [ ] SIC→Damodaran covers the initial high-volume set (deviation: partial coverage + graceful fallback, not the 60-day log) → B2/B9.
- [ ] `industry.multiple_source` surfaces the dataset date → B7.
- [ ] Annual-refresh runbook documented → B11.
- [ ] CI gate prevents dangling/unmapped references (static integrity, not request-log) → B9.
- [ ] Phase 1 entries **retained as fallback** (deviation from the tracker's "removed" — human decision: additive fallback for zero-regression). Note this explicitly in the tracker.
- [ ] CLAUDE.md updated → B11.

**`CLAUDE.md`** — build section: `go run ./cmd/refresh-damodaran ...`; gotcha: "revenue_multiple multiplier is Damodaran-by-SIC first, Phase 1 bucket fallback; `industry.multiple_source` surfaces provenance; `.xls` requires `extrame/xls` not excelize; new Industry field → diff.go count must stay in sync."

**`docs/openapi.yaml`** — add `multiple_source` to the `Industry` schema with the two example values.

---

## Test Plan

- **Unit (`sector_lookup_test.go`)**: `lookupDamodaranMultiple` — mapped / unmapped / empty / dangling-entry.
- **Unit (`revenue_multiple_test.go`)**: `resolveMultiple` 4-tier order; mapped-SIC source string; unmapped-SIC fallback source; **zero-regression pin** (nil Damodaran tables == legacy `getMultiple`, `math.Float64bits`-equal multiplier); existing `getMultiple` tests unchanged.
- **Integration**: end-to-end `revenue_multiple` valuation with a mapped SIC produces the Damodaran multiple and stamps `Industry.multiple_source`; with an unmapped SIC produces the Phase 1 multiple + `"sector-bucket"`.
- **Replay (`diff.go`)**: package compiles + `init()` guard passes at the new count (54). Add/adjust a `CompareResponse` test if one pins the field set.
- **CI integrity (B9)**: crosswalk referential integrity + dataset_date parseable.
- **Refresh tool**: Excel-serial→date helper unit test.
- **Regression**: AAPL/MSFT/GOOGL DCF responses omit `multiple_source` + byte-identical except version stamp; DDM bit-for-bit (`TestDDM_LegacyPath_BitForBit`) green.
- Coverage ≥90% for `sector_lookup.go` + the new `revenue_multiple.go` logic (finance module threshold).

### Critical edge cases (must have tests)
- SIC mapped to an industry name that is absent from the table (dangling) → fallback, not panic.
- Empty SIC (`historicalData.SICCode == ""`) → fallback.
- Damodaran config absent/unparseable → warn + full Phase 1 behavior (zero-regression).
- A ticker whose classifier code resolves a Phase 1 bucket but whose SIC is also Damodaran-mapped → Damodaran wins (order assertion).

---

## RM-2.5 assessment (industrials.json override)

**Recommendation: DEFER (no code this PR).** RM-2.5 is a *different axis* — datacleaner cleaning-rule **overrides** (`loadIndustryRules` → `config/datacleaner/industry/<sector>.json`), not valuation multiples. Only GICS `20` (Industrials) is reachable-and-uncovered, and it already degrades gracefully to base `rules.json` with a non-fatal warning (a working default, not a bug). Authoring `industrials.json` requires domain-validated override content + a reviewed recompute-shadow regeneration (per the in-code 5-step procedure at `loadIndustryRules`), which would drift the shadow snapshots and expand this PR's blast radius for no multiple-related benefit. There is no concrete driver (no demonstrably-wrong Industrials cleaning). Record the defer in the tracker; revisit when an Industrials cleaning defect surfaces. (B12 = the tracker note only.)

---

## Risks

| Risk | Mitigation |
|---|---|
| New `Industry` field panics every replay test if diff.go not updated atomically | B7 is a single atomic commit; the `init()` guard is the safety net (fails fast in CI). |
| Crosswalk industry-name typo silently falls back (no value) | B9 CI integrity gate rejects dangling references; runtime guard returns ok=false (safe). |
| Damodaran changes the `.xls` layout next January | B10 shape-validation guard fails loudly; runbook documents the manual verification step. |
| CalculationVersion churn vs DDM/FFO bit-for-bit | Only the stamp + revenue_multiple multiplier change; DDM/FFO numerics unchanged (pinned by existing bit-for-bit tests). |
| `extrame/xls` is a less-maintained dep | Confined to `cmd/refresh-damodaran` (offline tool); not on the request path; not imported by `cmd/server`. |
| Partial crosswalk → many tickers still on Phase 1 | By design (additive fallback = zero-regression); coverage grows incrementally; runbook documents how to extend. |

---

## Acceptance Criteria
- A `revenue_multiple` ticker with a mapped SIC returns a Damodaran EV/Sales multiple and `industry.multiple_source = "Damodaran <date>"`.
- A `revenue_multiple` ticker with an unmapped SIC returns the Phase 1 multiple and `"sector-bucket"`, **bit-for-bit identical** to pre-change on the multiplier.
- DCF/DDM/FFO responses omit `multiple_source` and are byte-identical (except the version stamp).
- `go test ./...` green, including `internal/observability/replay` (`init()` guard at count 54).
- CI integrity test passes (no dangling crosswalk entries; dataset_date parseable).
- `cmd/refresh-damodaran` regenerates `damodaran_sector_multiples.json` from a live fetch with the correct decoded date.
- Runbook + OpenAPI + CLAUDE.md updated; RM-2 tracker Phase-2 boxes checked.

## Assumptions and Open Questions
- **Assumption:** the human-confirmed live fetch (HTTP 200) and the row/col/serial layout are stable for the current file (verified). The refresh tool guards against future drift.
- **Assumption:** placing `multiple_source` on `Industry` (per the tracker's explicit `industry.multiple_source`) is preferred over a top-level field. (Decided.)
- **Open (non-blocking):** exact initial SIC coverage list — BACKEND picks ~40–80 per the scope guidance + fixture/replay SIC inspection. Reviewable in B2.
- **Open (non-blocking):** whether to also emit EV/EBITDA from `vebitda.xls` now — **No** (YAGNI; EV/Revenue only this PR).

HANDOFF_TO: BACKEND
