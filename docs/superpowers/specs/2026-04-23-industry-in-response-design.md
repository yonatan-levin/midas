# Industry Classification in Fair Value Response — Design Spec

**Version:** 1.0
**Date:** 2026-04-23
**Status:** APPROVED, IN IMPLEMENTATION
**Scope:** Additive response-surface change to `FairValueResponse` exposing both the SIC-based and the balance-sheet-heuristic industry labels the engine already computes internally.

---

## Context

The Midas valuation engine runs two parallel industry classifiers on every fair-value request:

| Classifier | Method | Input | Output | Consumer |
|---|---|---|---|---|
| SIC-based | `IndustryClassifier.Classify(sic, naics, name)` | SEC SIC code + company name | String: top-level codes `"TECH"`, `"MFG"`, `"RETAIL"`, `"UTIL"`, `"FIN"`, `"HEALTH"`, `"ENERGY"`, `"RESTATE"`, `"TELECOM"`, `"TRANS"`, `"CONS"`, `"NA"`, plus sub-industry codes `"TECH_SAAS"`, `"TECH_AI"`, `"FIN_IB"`, `"FIN_AM"`, `"HEALTH_PHARMA"`, `"HEALTH_BIOTECH"`, `"RETAIL_ECOM"`, `"ENERGY_RENEW"` | Valuation model router (picks DCF / DDM / FFO / Revenue Multiple) |
| Heuristic | `IndustryClassifier.ClassifyIndustry(ticker, data)` | Balance-sheet ratios only | `*SectorConfig` with GICS sector code (`"45"`, `"25"`, `"20"`, …) and name | Datacleaner's industry-specific rule loader (retail.json, technology.json, …) |

Today neither label is exposed to API consumers. The label is observable only via logs or by inferring from `calculation_method`. The AMD retail-misclassification incident of 2026-04-23 was diagnosed only because the user noticed abnormal output through external channels.

This spec adds a single `industry` field to the response containing **both** labels so misclassification is visible in every API call. A `match` flag surfaces disagreement between the two classifiers, making the API a passive watchdog for the classifier-drift class of bugs.

This is intentionally not the architectural unification — that is tracked separately in `docs/refactoring/spec/industry-classification-unification-spec.md`.

---

## Response Shape

### New struct

File: `internal/api/v1/handlers/fair_value.go`

```go
// Industry exposes both industry classifications the engine computes on every
// fair-value request: the SIC-derived label (canonical, used for valuation
// model selection) and the balance-sheet heuristic label (used by the
// datacleaner's industry-specific rule loader). Consumers can compare the two
// via the Match flag to surface classification drift.
type Industry struct {
    SICCode       string `json:"sic_code,omitempty" example:"3674"`                        // Raw SIC code from SEC (may be empty if SEC data lacked it)
    SIC           string `json:"sic,omitempty" example:"MFG"`                              // SIC-derived industry label from IndustryClassifier.Classify
    HeuristicCode string `json:"heuristic_code,omitempty" example:"45"`                    // GICS sector code from IndustryClassifier.ClassifyIndustry
    HeuristicName string `json:"heuristic_name,omitempty" example:"Information Technology"` // GICS sector name
    Match         bool   `json:"match" example:"true"`                                     // true when SIC and heuristic agree per known mapping
}
```

### Addition to `FairValueResponse`

```go
type FairValueResponse struct {
    // ...existing fields...
    Industry *Industry `json:"industry,omitempty"` // Industry classification (SIC + heuristic)
}
```

### Worked examples

**AMD (after 2026-04-23 fix — classifiers agree):**
```json
"industry": {
  "sic_code": "3674",
  "sic": "MFG",
  "heuristic_code": "45",
  "heuristic_name": "Information Technology",
  "match": true
}
```

**Hypothetical disagreement (classifier drift):**
```json
"industry": {
  "sic_code": "3674",
  "sic": "MFG",
  "heuristic_code": "25",
  "heuristic_name": "Consumer Discretionary",
  "match": false
}
```

**SIC absent (international filer, shell company):**
```json
"industry": {
  "heuristic_code": "45",
  "heuristic_name": "Information Technology",
  "match": false
}
```

---

## SIC → GICS Mapping (for `Match` computation)

The SIC classifier returns high-level category strings. The heuristic classifier returns GICS sector codes. `Match` requires comparing these dissimilar shapes, using the canonical mapping below.

| SIC-classifier label | GICS sector code | GICS sector name |
|---|---|---|
| `"TECH"` | `"45"` | Information Technology |
| `"MFG"` | `"20"` | Industrials |
| `"MFG"` | `"45"` | Information Technology *(accepted for manufacturers that are also tech — semiconductors, hardware)* |
| `"RETAIL"` | `"25"` | Consumer Discretionary |
| `"UTIL"` | `"55"` | Utilities |
| `"FIN"` | `"40"` | Financials |
| `"HEALTH"` | `"35"` | Health Care |

Any other combination, or a missing value on either side, → `Match: false` (conservative — preferring false negatives over false positives for drift signals).

The `"MFG"` → `{"20", "45"}` multi-map is deliberate: semiconductors return SIC "MFG" (manufacturing) but GICS "45" (tech), and that pairing is correct, not a drift. Both codes count as a match.

**Sub-industry normalization.** `IndustryClassifier.Classify` can refine to sub-industry codes like `"TECH_SAAS"`, `"HEALTH_BIOTECH"`, `"FIN_IB"`, `"RETAIL_ECOM"`, `"ENERGY_RENEW"` (Pass 2 refinement in classifier.go). For match-computation purposes a sub-industry is equivalent to its parent: `"TECH_SAAS"` vs `"45"` is a match, not a drift. Normalize in `matchSICToGICS` by stripping anything after the first underscore before the `sicToGICS` lookup:

```go
// Sub-industries (TECH_SAAS, HEALTH_BIOTECH, …) are equivalent to their parent
// (TECH, HEALTH) for match purposes. Take the parent prefix before lookup.
if i := strings.IndexByte(sicLabel, '_'); i >= 0 {
    sicLabel = sicLabel[:i]
}
```

The mapping lives as a private package-level var in the handler: `var sicToGICS = map[string]map[string]bool{...}`.

---

## Data Flow

### Source of truth, per classifier

1. **SIC-side** — already computed in `internal/services/valuation/service.go:436-451`. The existing call `s.industryClassifier.Classify(historicalData.SICCode, "", companyName)` produces the label. The raw SIC code is `historicalData.SICCode`. Both need to flow into the `ValuationResult` returned by the service.

2. **Heuristic-side** — computed in `internal/services/datacleaner/service.go:945` via `getIndustryCode` → `ClassifyIndustry`. Written to `CleaningResult.IndustryCode`. The datacleaner also knows the sector name via its `sectorConfigs` map, but currently only the code is plumbed out. Both need to flow through to the valuation service, then to `ValuationResult`.

### Plumbing steps

1. Add 4 new fields to `ValuationResult` (entity): `SICCodeRaw`, `IndustrySIC`, `IndustryHeuristicCode`, `IndustryHeuristicName`.
2. Populate them in `valuation/service.go` around line 436-451 where the classifier is already called.
3. Thread the heuristic sector *name* out of the datacleaner. Today only the code comes out — `CleaningResult` needs a `SectorName` field (or use an existing one; check).
4. In the handler, build the `Industry` struct from `ValuationResult` fields, compute `Match`, attach to `FairValueResponse.Industry`.
5. Bulk endpoint reuses automatically because `BulkFairValueResponse.Results` is `[]FairValueResponse`.

### Failure modes

- **Missing SIC**: `historicalData.SICCode == ""` — `Industry.SICCode` and `Industry.SIC` both omitted, `Match: false`.
- **SIC classification returns empty or `"NA"`**: same as above.
- **Heuristic returns nil `SectorConfig`** (shouldn't happen with current code but defensive): `HeuristicCode` and `HeuristicName` omitted, `Match: false`.
- **Datacleaner returned no CleaningResult** (upstream error): Industry field itself is `nil` / omitted entirely — use pointer so `omitempty` works.

Never fail the valuation for classification metadata errors. Classification is descriptive, not prescriptive.

---

## Testing Strategy

### New tests

- `internal/api/v1/handlers/fair_value_test.go`:
  - `TestFairValueResponse_Industry_BothPresent` — both classifications, match expected.
  - `TestFairValueResponse_Industry_ClassifierMismatch` — SIC says "MFG", heuristic says "25" → `Match: false`.
  - `TestFairValueResponse_Industry_MissingSIC` — only heuristic populated.
  - `TestFairValueResponse_Industry_SemiHybrid` — SIC "MFG" + heuristic "45" should be `Match: true` per the hybrid mapping.
  - `TestFairValueResponse_Industry_RealClassifier` — drives `IndustryClassifier.Classify` with real SIC codes (3674 semiconductor, 6020 financial, 7372 software) and asserts `Match: true` against the heuristic labels their profiles would produce. This is the integration test that would have caught a label-name typo or a missing-mapping bug. **Required** — unit tests that stub `ValuationResult` with hardcoded strings cannot catch spec-vs-reality gaps.
  - Buildless paths: `TestBuildIndustryFromResult_NilResult` and `TestBuildIndustryFromResult_AllFieldsEmpty` → both return `nil` so `omitempty` drops the field.

- Red-green verification required: stash the plumbing, confirm new tests FAIL, restore, confirm PASS.

### Coverage gate

Per CLAUDE.md: ≥90% on critical finance modules, ≥80% overall. The handler file is already near 90%; new tests should keep it there.

### Regression

All existing `fair_value_test.go` tests must pass unchanged. Existing JSON field ordering is preserved (additive change only).

---

## OpenAPI

`docs/openapi.yaml` — add the `Industry` schema and reference it from `FairValueResponse`. Follow existing Swagger tag conventions.

---

## Non-goals (this spec)

- Removing the heuristic classifier — in the refactor spec.
- Unifying `getIndustryCode` in the datacleaner to prefer SIC — in the refactor spec.
- Breaking the response contract — this is purely additive.
- Populating `FinancialData.IndustryCode` upstream in the fetcher — not strictly needed for this spec, since the valuation service already computes the SIC label.

---

## Rollout

Single PR. No feature flag needed — additive JSON field with `omitempty`. Existing clients ignore unknown fields per JSON convention; new clients get the new field.

OpenAPI docs must land in the same PR.

---

## Acceptance Criteria

1. AMD fair-value response includes `industry: {sic_code: "3674", sic: "MFG", heuristic_code: "45", heuristic_name: "Information Technology", match: true}`.
2. All existing tests pass unchanged.
3. New tests cover the four cases above (both present, mismatch, missing SIC, hybrid match).
4. `go vet ./...` clean, `gofmt -l` clean, full `go test ./...` clean.
5. OpenAPI spec documents the new field.
6. Reviewer approves (no BLOCKING findings).
