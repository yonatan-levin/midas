# Industry Classification Unification — Refactor Spec

**Version:** 1.0
**Date:** 2026-04-23
**Status:** NOT STARTED
**Scope:** Make the SEC SIC code the single source of truth for industry classification across the entire Midas pipeline. Demote the balance-sheet heuristic classifier to a narrow fallback for tickers where SIC is unavailable, and remove the heuristic's current role as parallel classification authority.

---

## Implementation Status

| Phase | Status | Notes |
|---|---|---|
| Phase 0 — Propagate SIC to FinancialData | NOT STARTED | Plumb `HistoricalFinancialData.SICCode` into per-period `FinancialData`. |
| Phase 1 — SIC → GICS mapping | NOT STARTED | Package-level lookup. First used by response handler; reused by datacleaner in Phase 2. |
| Phase 2 — Datacleaner prefers SIC | NOT STARTED | `getIndustryCode` calls `Classify(sic, …)` first, heuristic as fallback only. |
| Phase 3 — Narrow or delete heuristic predicates | NOT STARTED | Decide whether to delete `isRetailCompany` / `isTechnologyCompany` / siblings entirely or keep as SIC-less fallback. |
| Phase 4 — Response cleanup (BREAKING) | NOT STARTED | Remove `heuristic_*` fields from `FairValueResponse.Industry` once SIC is authoritative. Deprecation window required. |
| Phase 5 — Test cleanup | NOT STARTED | Remove tests that encode heuristic outputs as spec. |

---

## Table of Contents

1. [Context](#context)
2. [Goal](#goal)
3. [Phases](#phases)
4. [Blast Radius](#blast-radius)
5. [Testing Strategy](#testing-strategy)
6. [Rollout & Backwards Compatibility](#rollout--backwards-compatibility)
7. [What Stays the Same](#what-stays-the-same)
8. [Non-Goals](#non-goals)

---

## Context

Today the codebase runs two parallel classifiers. They can and do produce different labels for the same ticker:

| Classifier | Method | Input | Output shape | Consumer |
|---|---|---|---|---|
| SIC-based | `IndustryClassifier.Classify` | SEC SIC code + company name | String (`"TECH"`, `"MFG"`, `"RETAIL"`, …) | Valuation model router — picks DCF vs DDM vs FFO vs Revenue Multiple |
| Heuristic | `IndustryClassifier.ClassifyIndustry` | Balance-sheet ratios | GICS sector code + name | Datacleaner's industry-specific rule loader |

**This dual-source design is the root cause of the AMD retail misclassification incident** (fixed 2026-04-23 at `classifier.go:430-437` and `:686-700`). The SIC-based path correctly classified AMD as `"MFG"` (manufacturing). The heuristic path classified AMD as `"25"` (Consumer Discretionary / retail) based on balance-sheet ratios after the Xilinx acquisition. Because the datacleaner uses the heuristic, retail-specific cleaning rules (gift card breakage, store closure reserves, loyalty program liability) were being applied to semiconductor financials.

The hotfix tightened the heuristic's retail predicate with R&D and SBC guards, but did not address the architectural issue. This refactor does.

Related artifacts:
- `docs/FEEDBACK-LOG.md` entry dated 2026-04-23.
- `docs/superpowers/specs/2026-04-23-industry-in-response-design.md` — the sibling "ship now" design that adds both classifications to the response. This refactor removes the heuristic half once SIC is authoritative everywhere.

---

## Goal

**One classifier, one label, one source of truth.** SIC drives classification wherever it is available. The heuristic survives only as a last-resort fallback for issuers without a usable SIC code (foreign private issuers, some pre-revenue shells), and even then its output is mapped through the SIC-label vocabulary — never the parallel GICS scheme.

Success criteria:
1. A single function call site owns classification for the entire request lifecycle.
2. The response, the model router, and the datacleaner's rule loader all read the same label.
3. No ticker is classified differently in two parts of the pipeline.
4. The `FairValueResponse.Industry.heuristic_*` fields are removed in Phase 4 after a deprecation window.

---

## Phases

### Phase 0 — Propagate SIC to FinancialData

**Problem:** `FinancialData.IndustryCode` (entities/financial_data.go:18) has a `// TODO: Populate this field` comment and is empty. `HistoricalFinancialData.SICCode` (line 114) exists and is populated from SEC, but the datacleaner sees only the per-period `FinancialData`. The SIC doesn't flow down to the classifier at the datacleaner layer.

**Change:**
- Add `SICCode string` field to `FinancialData` (entities/financial_data.go).
- In the datafetcher / coordinator, when materializing per-period `FinancialData` records from `HistoricalFinancialData`, copy `SICCode` onto each period record.
- Update callers that construct `FinancialData` in tests to include `SICCode` where applicable.

**Files touched:** `internal/core/entities/financial_data.go`, `internal/services/datafetcher/coordinator.go`, test fixtures.

**Acceptance:** `FinancialData.SICCode` is non-empty for all US issuers after this phase, verified by an integration test that fetches AMD and asserts `SICCode == "3674"`.

---

### Phase 1 — SIC → GICS Mapping

**Problem:** The datacleaner loads industry-specific rule files keyed by GICS sector code (`"25"`, `"45"`, `"20"`). The SIC classifier returns labels in a different vocabulary (`"TECH"`, `"RETAIL"`, `"MFG"`). A translation table is needed so SIC output can drive the rule loader.

**Change:**
- Create a single authoritative mapping (likely at `internal/services/datacleaner/industry/sic_to_gics.go` or similar) that converts `Classify` output to GICS codes.
- This mapping also powers the `Match` check in the response handler (from the ship-now spec), so it must be referenceable from both places.

**Mapping:**

| SIC label | Primary GICS | Acceptable alternates (when SIC alone is ambiguous) |
|---|---|---|
| `"TECH"` | `"45"` Information Technology | — |
| `"MFG"` | `"20"` Industrials | `"45"` (semiconductors, hardware manufacturers) |
| `"RETAIL"` | `"25"` Consumer Discretionary | `"30"` Consumer Staples (grocery) |
| `"UTIL"` | `"55"` Utilities | — |
| `"FIN"` | `"40"` Financials | — |
| `"HEALTH"` | `"35"` Health Care | — |

**Files touched:** New file in `internal/services/datacleaner/industry/`, tests.

**Acceptance:** Unit tests cover every documented mapping entry and confirm unknown SIC labels return `""` (not a wrong-but-plausible default).

---

### Phase 2 — Datacleaner Prefers SIC

**Problem:** `internal/services/datacleaner/service.go:945` calls `ClassifyIndustry(ticker, data)` unconditionally, ignoring the SIC code even when it's present.

**Change:**
- Rewrite `getIndustryCode` to:
  1. If `data.SICCode != ""` — call `Classify(data.SICCode, "", companyName)`. Translate to GICS via the Phase 1 mapping. Use that.
  2. Else — fall back to `ClassifyIndustry(ticker, data)` (the old heuristic path).
- Log at `Info` level when the fallback triggers, so gaps are visible.

**Files touched:** `internal/services/datacleaner/service.go`.

**Acceptance:** For tickers with SIC codes, the heuristic is never invoked. For tickers without SIC, the heuristic still works. Confirm via a table-driven test with the full semi basket (AMD, NVDA, INTC, AVGO, MRVL, QRVO, NXPI).

---

### Phase 3 — Narrow or Delete the Heuristic Predicates

**Problem:** `isRetailCompany`, `isTechnologyCompany`, `isManufacturingCompany`, `isUtilitiesCompany`, `isFinancialCompany`, `isHealthcareCompany` are the heuristic guts. Once Phase 2 ships and SIC is the primary path, these functions are dead code for US issuers and fallback-only for international / edge cases.

**Decision needed:**

**Option A (aggressive): delete them.** International issuers without SIC either get a default Industrials bucket or fail with `ErrInsufficientData`. Simpler codebase, fewer traps.

**Option B (defensive): keep them as narrow fallback.** Add a top-of-file comment flagging them as "fallback only, invoked via getIndustryCode when SIC is unavailable." Keep the R&D / SBC guards from the 2026-04-23 hotfix.

**Recommendation:** Option B for one release cycle, then Option A once we confirm no production tickers hit the fallback.

**Files touched:** `internal/services/datacleaner/industry/classifier.go`.

**Acceptance:** If Option A — `grep isRetailCompany` returns zero hits. If Option B — every heuristic predicate has a doc comment pointing to the refactor spec and a log statement when it fires.

---

### Phase 4 — Response Cleanup (BREAKING)

**Problem:** The ship-now design exposes both `sic_*` and `heuristic_*` fields on `FairValueResponse.Industry`. Once SIC is authoritative, the heuristic fields are redundant and their continued presence encourages clients to compare two sources that should always agree.

**Change:**
- Deprecate the `heuristic_code` and `heuristic_name` JSON fields (add `deprecated: true` in Swagger, note in CHANGELOG, document removal date).
- After a 1-release deprecation window, remove:
  - `HeuristicCode` and `HeuristicName` fields from `Industry` struct.
  - `Match` field (tautologically true once there's only one classifier).
  - The SIC→GICS `Match` comparison logic (still needed inside the datacleaner for rule loading, but not for the response).
- Rename `SIC` field to `Label` (or `Code` / `Name` pair) since it's no longer disambiguating against a heuristic.

**Files touched:** `internal/api/v1/handlers/fair_value.go`, `docs/openapi.yaml`, client-facing CHANGELOG.

**Acceptance:** `Industry` struct has 2-3 fields max. `Match` is gone. No JSON field references "heuristic." OpenAPI deprecation notices present during the deprecation window, removed in the cleanup release.

---

### Phase 5 — Test Cleanup

**Problem:** The classifier test file (`internal/services/datacleaner/industry/classifier_test.go`) contains tests like `TestIndustryClassifier_ClassifyIndustry_Retail` (rewritten on 2026-04-23 to use real retailer data) that exercise the heuristic predicates directly. After Phase 3 decision:
- If Option A (deleted heuristic) — these tests fail to compile and must be deleted.
- If Option B (heuristic as fallback) — tests stay but their scope documentation needs to reflect "fallback path only."

**Change:** Apply the matching test disposition.

**Files touched:** `internal/services/datacleaner/industry/classifier_test.go`, `classifier_regressions_test.go`.

**Acceptance:** Tests align with the Phase 3 decision. Full `go test ./...` clean.

---

## Blast Radius

Tickers to verify after each phase (expected classification in parentheses):

**Semiconductors with acquired IP (AMD bug class):**
- AMD → `"MFG"` / `"45"`
- NVDA → `"MFG"` / `"45"`
- INTC → `"MFG"` / `"45"`
- AVGO → `"MFG"` / `"45"`
- MRVL → `"MFG"` / `"45"`
- QRVO → `"MFG"` / `"45"`
- NXPI → `"MFG"` / `"45"`

**Medical devices with acquisitions:**
- MDT → `"HEALTH"` / `"35"`
- BSX → `"HEALTH"` / `"35"`
- SYK → `"HEALTH"` / `"35"`

**Industrial conglomerates with M&A:**
- HON → `"MFG"` / `"20"`
- ETN → `"MFG"` / `"20"`
- EMR → `"MFG"` / `"20"`
- ROP → `"MFG"` / `"20"`

**Software companies with hardware bundling:**
- ANSS → `"TECH"` / `"45"`
- PTC → `"TECH"` / `"45"`

**Sanity controls (real retailers, must still classify as retail):**
- TGT → `"RETAIL"` / `"25"`
- M → `"RETAIL"` / `"25"`
- WMT → `"RETAIL"` / `"25"` (or `"30"` — Consumer Staples alternate)

**International / no-SIC fallback:**
- TSM (Taiwan ADR) — SIC may be unavailable; falls back to heuristic if Option B, defaults if Option A.
- SHOP (Canadian) — similar.

Verification is end-to-end: call the fair-value endpoint, assert the response's `industry.sic` (and post-Phase-4, just `industry.label`) matches the expected value in the table.

---

## Testing Strategy

Each phase independently testable, each with an integration test covering at least one ticker from each blast-radius category above.

**Per-phase unit tests:**
- Phase 0: `TestFinancialData_SICCodePopulated_FromHistorical`.
- Phase 1: `TestSICToGICS_AllMappings` (table-driven, all documented entries).
- Phase 2: `TestGetIndustryCode_PrefersSIC`, `TestGetIndustryCode_FallsBackWhenSICMissing`.
- Phase 3: depends on Option A vs B — either "deleted, compilation passes" or "predicate fires only when SIC missing."
- Phase 4: response shape snapshot tests updated.
- Phase 5: full suite green.

**Coverage gate:** CLAUDE.md mandates ≥90% on critical finance modules, ≥80% overall. Industry classification is not explicitly flagged as critical-finance, but it directly affects model selection, so treat as critical.

**Red-green discipline:** Every new test must fail against the pre-phase code before the phase's implementation lands. Required by `docs/FEEDBACK-LOG.md` 2026-04-23 entry on verification-before-completion.

---

## Rollout & Backwards Compatibility

- **Phases 0-3** — internal refactor, no API shape change, safe to ship incrementally.
- **Phase 4 only** — breaking response change. Deprecation window:
  - Release N: mark `heuristic_*` as deprecated in OpenAPI, add CHANGELOG note.
  - Release N+1: Remove fields.
- Client downgrade path: clients reading `sic`/`sic_code` continue to work. Clients depending on `heuristic_*` must migrate by release N+1.

---

## What Stays the Same

- The SEC ingestion pipeline (`infra/gateways/sec/`) already populates SIC — unchanged.
- The valuation model router (`services/valuation/models/router.go`) is already SIC-based — unchanged.
- The datacleaner's industry-specific rule files (`config/datacleaner/industry/*.json`) stay GICS-keyed — the Phase 1 mapping handles translation.
- `IndustryClassifier.Classify` itself is unchanged. We are re-scoping its consumers, not its implementation.

---

## Non-Goals

- Replacing the valuation model router's SIC → model mapping. That logic is out of scope.
- Auto-populating SIC for international filers without a SEC-assigned code. If SEC doesn't provide it, we don't invent it.
- Removing the valuation response's `calculation_method` field. That describes the valuation model, not the industry.
- A third classifier (e.g., NAICS-based fallback). Deferred indefinitely — if SIC isn't available, the heuristic fallback path is sufficient for the few percent of affected tickers.

---

## Related Documents

- `docs/superpowers/specs/2026-04-23-industry-in-response-design.md` — the "ship now" additive response design that this refactor later trims in Phase 4.
- `docs/FEEDBACK-LOG.md` 2026-04-23 entry — the original decision to track this as a follow-up.
- `docs/refactoring/spec/valuation-engine-upgrade-spec.md` — parent valuation-engine upgrade; this refactor is a sibling, not a child.
- `CLAUDE.md` — testing and coverage conventions.
