# VAL-3 Phase 2 — AFFO support — Implementation Plan (EXECUTION-READY)

MODE: PLAN_AND_CREATE
ROLE: ARCH
Status: DRAFT — ready for BACKEND execution under midas's own harness (delegated; run gates inside `midas/`).
Spec authority: [VAL-3 spec](VAL-3-ffo-affo-subsector-multiples-and-forward-projection.md) — "Phase 2 — AFFO support" (§ lines 110-120) + Phase 2 acceptance checklist (§ lines 200-204).
Engine version at time of writing: **4.8** (the task brief's "4.7" is stale — see [service.go:2044](../../../internal/services/valuation/service.go) and the `service_test.go` pins asserting `"4.8"`).

---

## 1. Goal & scope

Add **AFFO (Adjusted Funds From Operations)** valuation to the REIT FFO model. AFFO = FFO − MaintenanceCapEx; it is the methodologically-superior REIT cash-flow base (Damodaran) because FFO ignores the recurring capex needed to maintain the property portfolio. Phase 2 plumbs a new `MaintenanceCapEx` entity field (SEC-parsed where disclosed, else estimated at `0.7 × CapitalExpenditures` for REITs), computes AFFO in [`ffo.go`](../../../internal/services/valuation/models/ffo.go), applies the **existing subsector P/FFO multiple** to the AFFO base, and surfaces two response fields — `pffo_value_per_share` (existing FFO-based number, backward-compat) and `paffo_value_per_share` (new AFFO-based number). The headline `intrinsic_value_per_share` (→ `dcf_value_per_share` on the alt-model wire) becomes the AFFO-based value **when AFFO data is available**, otherwise it stays the FFO-based value. The whole AFFO path is gated to be a **no-op (bit-for-bit identical to today)** whenever maintenance capex is neither disclosed nor estimable.

### Non-goals (explicitly out of scope per spec § lines 183-189)
- **Distinct P/AFFO multiples.** Spec § line 112 mandates reusing the same subsector P/FFO multiple on the AFFO base. We document the methodological caveat (real P/AFFO ≠ P/FFO) but do NOT add a `reit_paffo_multiples` table. (See decision D1.)
- **Forward FFO/AFFO projection** — that is Phase 3 (the existing `Trailing/Forward/HorizonSelected/TerminalMultiple` scaffold in `ffo.go` is untouched here; the forward path keeps projecting off `ffoPerShare`, see decision D3).
- **mREITs** (mortgage REITs — different model; tracked as VAL-3.5).
- **International REIT analogs** (J-REITs, A-REITs).
- **StraightLineRentAdjustment / other non-cash AFFO adjustments** (spec § line 30 lists them but Phase 2 scopes AFFO to `FFO − MaintenanceCapEx` only).
- **Triple-net vs gross-lease subclassification.**

---

## 2. File-by-file change list (safe commit ladder order)

| # | File | Change | Why |
|---|------|--------|-----|
| 1 | [`internal/core/entities/financial_data.go`](../../../internal/core/entities/financial_data.go) | Add `MaintenanceCapEx float64` field next to `CapitalExpenditures` (~line 105), stored POSITIVE. | Domain field the parser populates and the model consumes. Pure additive; zero-valued by default → no behavior change. |
| 2 | [`internal/services/valuation/currency.go`](../../../internal/services/valuation/currency.go) | Add `fd.MaintenanceCapEx *= rate` in the cash-flow block (after line 259, beside `CapitalExpenditures`). | FPI/ADR tickers report in non-USD; MaintenanceCapEx is monetary and MUST be FX-converted exactly like CapitalExpenditures, or AFFO would mix currencies. |
| 3 | [`internal/infra/gateways/sec/parser.go`](../../../internal/infra/gateways/sec/parser.go) | (a) In `parsePeriodData`, after the CapitalExpenditures block (line 723-730), add a `findValue` first-hit extraction for maintenance-capex tags → `financialData.MaintenanceCapEx` (clamp `val > 0`, no `absAddBack`). (b) Add the new tags to `GetSupportedConcepts()` (~line 1267 area). | Populate from XBRL where a maintenance-capex tag is filed. The 0.7× ESTIMATE is NOT done here — it's a model-layer fallback (keeps the parser a pure data-extraction layer and the estimate REIT-gated). |
| 4 | [`internal/services/valuation/models/router.go`](../../../internal/services/valuation/models/router.go) | Add two `omitempty float64` fields to `ModelResult` (~line 130): `PFFOValuePerShare`, `PAFFOValuePerShare`. | Carry both numbers from the FFO model up to service.go. omitempty → DDM/DCF/revenue_multiple paths emit them as 0 → no JSON change there. |
| 5 | [`internal/services/valuation/models/ffo.go`](../../../internal/services/valuation/models/ffo.go) | Core: compute MaintenanceCapEx (disclosed-or-estimate), AFFO, AFFO-per-share, AFFO-based value; select headline; populate `PFFOValuePerShare`/`PAFFOValuePerShare`; gate so absent-AFFO ⇒ bit-for-bit. | The algorithm (§3). |
| 6 | [`internal/core/entities/valuation.go`](../../../internal/core/entities/valuation.go) | Add `PFFOValuePerShare`/`PAFFOValuePerShare float64 json:"...,omitempty"` to `ValuationResult` (~after line 16). | Transport from service → handler. omitempty preserves wire shape for non-REIT results. |
| 7 | [`internal/services/valuation/service.go`](../../../internal/services/valuation/service.go) | In `performAlternativeValuation`'s `ValuationResult{...}` literal (~line 2017-2046), map `modelResult.PFFOValuePerShare`/`.PAFFOValuePerShare` onto the new result fields. Headline `DCFValuePerShare` stays `modelResult.IntrinsicValuePerShare` (the model already chose AFFO-vs-FFO). | Wire the two new numbers through. No headline-selection logic here — the model owns it. |
| 8 | [`internal/api/v1/handlers/fair_value.go`](../../../internal/api/v1/handlers/fair_value.go) | Add `PFFOValuePerShare`/`PAFFOValuePerShare float64 json:"pffo_value_per_share,omitempty"` / `"paffo_value_per_share,omitempty"` to `FairValueResponse` (~after line 174); map them in `buildFairValueResponse` (~line 757). | Surface to API consumers. omitempty preserves POST{}==GET byte-identity and legacy shape. |
| 9 | [`internal/observability/replay/diff.go`](../../../internal/observability/replay/diff.go) | **LOAD-BEARING.** Add `"PFFOValuePerShare":"pffo_value_per_share"` + `"PAFFOValuePerShare":"paffo_value_per_share"` to `goFieldToJSON` (after line 424) AND bump `countFairValueFields()` from `33` → `35` (line 547). | The `init()` reflection guard PANICS every replay test if struct field count drifts from the constant. Two new fields ⇒ +2. |
| 10 | `docs/` (swagger) | Regenerate: `go run github.com/swaggo/swag/cmd/swag init -g cmd/server/main.go -o docs/ --parseInternal --parseDependency`. Then `git diff docs/swagger.json docs/swagger.yaml docs/openapi.yaml`. | Module-pinned swag (NEVER a global binary). The new `example` tags render into the OpenAPI spec. |
| 11 | `internal/services/valuation/models/ffo_test.go` (+ parser/entity tests) | Add the test functions in §6. | TDD: write RED tests in the same commit-ladder step that ships each behavior. |
| 12 | `CLAUDE.md` (midas) + spec Phase 2 checklist | Tick the Phase 2 acceptance boxes; add a CLAUDE.md gotcha bullet for the 0.7× estimate + headline rule. | Spec § line 203 requires CHANGELOG/CLAUDE.md update. |

---

## 3. The AFFO algorithm (precise)

Insert into `FFOModel.Calculate` **after** `ffoPerShare` is computed (after [ffo.go:236](../../../internal/services/valuation/models/ffo.go)) and **before** the existing `pffoMultiple := m.getMultiple(...)` / `valuePerShare` block. The existing FFO value computation stays exactly as-is and becomes the `pffoValuePerShare`.

```
// ---- existing (unchanged) ----
ffo := netIncome + da - propertyGains
ffoPerShare := ffo / shares
pffoMultiple := m.getMultiple(input.Industry)
pffoValuePerShare := ffoPerShare * pffoMultiple        // == today's valuePerShare
if pffoValuePerShare < 0 { pffoValuePerShare = 0; warn(...) }

// ---- NEW: maintenance capex resolution (REIT-gated estimate) ----
maintCapEx := latest.MaintenanceCapEx                  // disclosed (positive) or 0
affoAvailable := false
if maintCapEx <= 0 && latest.CapitalExpenditures > 0 {
    // Estimate fallback. ONLY for REITs (this model only runs for REITs,
    // so reaching here already implies REIT). Spec § line 112: 0.7×.
    maintCapEx = MaintenanceCapExEstimateRatio * latest.CapitalExpenditures   // 0.70
    warn("AFFO maintenance capex ESTIMATED at 0.7× total capex (%.0f); SEC breakout not filed", maintCapEx)
    affoAvailable = true   // we CAN compute AFFO from the estimate
} else if maintCapEx > 0 {
    affoAvailable = true   // disclosed
}

// ---- NEW: AFFO value ----
valuePerShare := pffoValuePerShare      // default headline = FFO-based (bit-for-bit)
paffoValuePerShare := 0.0
if affoAvailable {
    affo := ffo - maintCapEx
    affoPerShare := affo / shares
    paffoValuePerShare = affoPerShare * pffoMultiple     // SAME multiple (decision D1)
    if paffoValuePerShare < 0 { paffoValuePerShare = 0; warn("AFFO-based value floored at 0") }
    valuePerShare = paffoValuePerShare                   // headline switches to AFFO
    warn(fmt.Sprintf("AFFO base used: FFO %.0f − maint capex %.0f = AFFO %.0f", ffo, maintCapEx, affo))
}
```

The rest of `Calculate` (equity bridge, forward scaffold, NAV cross-check, logging) keeps reading `valuePerShare` — so when `affoAvailable` is false, `valuePerShare == pffoValuePerShare`, identical to today.

Populate the result:
```
return &ModelResult{
    IntrinsicValuePerShare: valuePerShare,        // AFFO-based iff available, else FFO-based
    PFFOValuePerShare:      pffoValuePerShare,     // always the FFO-based number
    PAFFOValuePerShare:     paffoValuePerShare,    // 0 (omitted) when AFFO unavailable
    ... (existing fields unchanged) ...
}
```

### Constants (in `ffo.go`)
- `const MaintenanceCapExEstimateRatio = 0.70` — spec § line 112 ("0.7 × CapitalExpenditures"). Documented as an industry rule-of-thumb (spec § line 37: practitioners estimate 60-80%); 0.70 is the spec-mandated point estimate.

### The bit-for-bit gate (LOAD-BEARING)
`affoAvailable` is false **iff** `MaintenanceCapEx <= 0 AND CapitalExpenditures <= 0`. In that case:
- `valuePerShare = pffoValuePerShare` (the exact pre-Phase-2 computation, same float ops, same order).
- `PAFFOValuePerShare = 0` → `omitempty` drops it from JSON.
- No new warnings appended.
- `IntrinsicValuePerShare` is byte-identical to today's `valuePerShare`.

`PFFOValuePerShare` is non-zero for every REIT (it equals the old `valuePerShare`), so it will appear in the REIT JSON even on the bit-for-bit path — **this is an intended, additive new field on REIT responses** and does NOT affect non-REIT responses (FFO model never runs for them; `omitempty` keeps it absent). Replay bundles captured for REITs will show the added `pffo_value_per_share` field; this is a deliberate schema addition (see §7 replay note), NOT a value regression of any existing field.

---

## 4. Data plumbing detail

### 4.1 Entity field ([financial_data.go:105](../../../internal/core/entities/financial_data.go))
```go
CapitalExpenditures float64 `json:"capital_expenditures"`     // Cash outflow for PP&E (stored as positive)
MaintenanceCapEx    float64 `json:"maintenance_capex"`        // Recurring/maintenance capex (REIT AFFO). Stored POSITIVE. 0 = not disclosed; FFO model estimates 0.7× CapitalExpenditures.
```
NOT a `computePlugs` term and NOT a `recomputeUmbrellas` term — it is a standalone cash-flow line, exactly like `CapitalExpenditures` and `DepreciationAndAmortization` (which are also not plug/umbrella terms). **State this explicitly in the field godoc** so a future maintainer does not wire it into a residual. Verified: the plug invariants (financial_data.go:116-121) reference only `CashAndCashEquivalents/Inventory/Other*/Goodwill/OtherIntangibles/DeferredTaxAssets/TotalDebt/OperatingLease*` — MaintenanceCapEx is absent from all of them.

### 4.2 Parser tags (mirror TDB-1 / TDB-12 first-hit `findValue` pattern)
Insert after the CapitalExpenditures block ([parser.go:723-730](../../../internal/infra/gateways/sec/parser.go)). Maintenance-capex is **rarely broken out** in XBRL — the candidate list is a best-effort first-hit; absence is the common case and is fine (the 0.7× estimate covers it at the model layer).

```go
// Maintenance / recurring capital expenditures (VAL-3 Phase 2 — REIT AFFO).
// Rarely filed as a discrete tag; when absent the FFO model estimates
// 0.7× CapitalExpenditures. findValue first-hit (NOT sumValues — these are
// alternative presentations of the same recurring-capex line). Clamp val > 0
// (a negative recurring-capex magnitude is a data anomaly — do NOT absAddBack;
// these are cash-outflow magnitudes stored positive, mirroring CapitalExpenditures).
// NOT a computePlugs/recomputeUmbrellas term.
if val, exists := p.findValue(data, []string{
    "PaymentsForCapitalImprovements",                 // primary — recurring property improvements
    "PaymentsForCapitalImprovementsRealEstate",       // some REIT filers
}); exists && val > 0 {
    financialData.MaintenanceCapEx = val
}
```

**Tags DELIBERATELY EXCLUDED — do NOT "helpfully" add them** (mirrors the TDB-1/TDB-12 exclusion idiom):
- `us-gaap:PaymentsToDevelopRealEstateAssets` — DEVELOPMENT capex (growth), NOT maintenance. Spec § line 39 calls this out specifically. Including it would over-state maintenance capex and under-state AFFO.
- `us-gaap:PaymentsToAcquireRealEstate` / `PaymentsToAcquirePropertyPlantAndEquipment` — acquisition/total capex (already mapped to `CapitalExpenditures` at line 724). Reusing it makes the estimate-vs-disclosed distinction meaningless.

> **Open research item (R1, non-blocking):** confirm `PaymentsForCapitalImprovements` is the most-filed recurring-capex concept across the test-basket REITs (DLR/SPG/PLD/AMT/EQR). If live capture shows none of them tag it, the disclosed path is effectively untested on real bundles and the 0.7× estimate path carries all REITs — that is acceptable for Phase 2 (the estimate is the documented common case) but should be noted in the closeout. Use `mcp__perplexity-ask` or an SEC companyfacts probe to verify before finalizing the candidate list.

### 4.3 `GetSupportedConcepts()` ([parser.go:1267 area](../../../internal/infra/gateways/sec/parser.go))
Add the two extracted tags (qualified form) to the returned slice:
```go
"us-gaap:PaymentsForCapitalImprovements",
"us-gaap:PaymentsForCapitalImprovementsRealEstate",
```

### 4.4 FX conversion ([currency.go:259](../../../internal/services/valuation/currency.go))
```go
fd.CapitalExpenditures *= rate
fd.MaintenanceCapEx *= rate    // VAL-3 Phase 2 — monetary, FX-convert like CapitalExpenditures (FPI/ADR REITs)
fd.OperatingCashFlow *= rate
```

---

## 5. Response shape

### New fields ([fair_value.go](../../../internal/api/v1/handlers/fair_value.go))
```go
// VAL-3 Phase 2 — REIT FFO/AFFO. Both omitempty: present only on REIT
// (FFO-model) responses; absent for DCF/DDM/revenue_multiple. PFFO is the
// FFO-based number (always present on REIT responses); PAFFO is the AFFO-based
// number, present only when maintenance capex is disclosed OR estimable
// (0.7× capex). When PAFFO is present it equals the headline
// intrinsic value (dcf_value_per_share); when absent the headline is PFFO.
PFFOValuePerShare  float64 `json:"pffo_value_per_share,omitempty" example:"42.10"`
PAFFOValuePerShare float64 `json:"paffo_value_per_share,omitempty" example:"31.50"`
```

### Headline-selection rule (single source of truth = the model)
`intrinsic_value_per_share` (→ `dcf_value_per_share`) = `PAFFOValuePerShare` when AFFO available, else `PFFOValuePerShare`. **The model decides** (sets `IntrinsicValuePerShare`); service.go and the handler do NOT re-derive it. This keeps one decision point and guarantees the headline always equals one of the two surfaced numbers.

### omitempty / byte-identity
- Non-REIT responses: FFO model never runs → both fields are 0 → omitted → byte-identical to today.
- REIT, no maintenance capex (no disclosure + zero capex): `PAFFO=0` omitted; `PFFO` is added (new field on REIT JSON, additive). Headline = FFO-based, unchanged value.
- POST{}==GET: both paths flow through the single `buildFairValueResponse`; no override touches FFO inputs → POST{} and GET produce identical bytes. Add a regression assertion to the existing `TestPostFairValue_EmptyBody_EqualsGET`-style coverage if a REIT fixture is wired there (non-blocking).

### diff.go registration (LOAD-BEARING — §2 row 9)
Two entries in `goFieldToJSON` + bump `countFairValueFields()` 33→35, **in the same commit as the `FairValueResponse` field addition**. Run any replay test (e.g. `go test ./internal/observability/replay/...`) to confirm the `init()` guard does not panic.

### Swagger
Module-pinned regen (§2 row 10). Verify the diff only adds the two new properties.

---

## 6. Test plan (TDD) — table-driven, mapped to spec acceptance

All in `internal/services/valuation/models/ffo_test.go` unless noted. Use `NewFFOModelWithConfig`/`NewFFOModelWithTables` so the multiple is pinned and deterministic. Coverage target **≥90% on `ffo.go`** (CLAUDE.md finance-module standard).

| Test function | Maps to spec acceptance | Asserts |
|---|---|---|
| `TestFFOModel_Calculate_AFFO_DisclosedMaintenanceCapEx` | § line 172 (maint capex disclosed: AFFO<FFO, headline uses AFFO) | `latest.MaintenanceCapEx>0`; `PAFFOValuePerShare < PFFOValuePerShare`; `IntrinsicValuePerShare == PAFFOValuePerShare`; AFFO warning present; no estimate warning. |
| `TestFFOModel_Calculate_AFFO_EstimatedFromCapEx` | § line 173 (no breakout: estimate 0.7× total capex, emit warning) | `MaintenanceCapEx==0`, `CapitalExpenditures>0`; resolved maint capex `== 0.7×capex`; `PAFFO == (ffo−0.7·capex)/shares × multiple`; estimate warning string present; headline = AFFO. |
| `TestFFOModel_Calculate_AFFO_AbsentBitForBit` | § line 174 + Phase 2 master invariant | `MaintenanceCapEx==0 && CapitalExpenditures==0`; `PAFFOValuePerShare==0`; `IntrinsicValuePerShare` `math.Float64bits`-equal to a sibling call through the pre-Phase-2 code path (capture golden, or assert equals `PFFOValuePerShare` computed the legacy way); no AFFO/estimate warnings. |
| `TestFFOModel_Calculate_AFFO_NegativeAFFOFlooredToZero` | edge case | FFO>0 but maint capex>FFO ⇒ AFFO<0 ⇒ `PAFFO==0` with floor warning; headline = 0 (or PFFO? — see decision D2). |
| `TestFFOModel_Calculate_AFFO_PFFOAlwaysSurfaced` | response-shape contract | For any REIT input, `PFFOValuePerShare > 0` equals the FFO-based number regardless of AFFO availability. |
| `TestParser_MaintenanceCapEx_Disclosed` (parser pkg) | § line 201 (field populated by parser) | A companyfacts fixture tagging `PaymentsForCapitalImprovements` → `financialData.MaintenanceCapEx == tagged value` (positive); development/acquisition tags do NOT populate it. |
| `TestParser_MaintenanceCapEx_Absent` (parser pkg) | bit-for-bit data path | No maintenance tag → `MaintenanceCapEx == 0` (estimate happens at model layer, not parser). |
| `TestCurrency_MaintenanceCapEx_FXConverted` (currency test) | FPI/ADR correctness | After `convertFinancialsToUSD` with rate≠1, `MaintenanceCapEx` is multiplied by `rate`. |
| `TestBuildFairValueResponse_AFFOFields` (handlers pkg) | response mapping + omitempty | `PFFO/PAFFO` map through; absent (0) values are omitted from marshalled JSON; present values render snake_case. |

Existing FFO tests (`TestFFOModel_Calculate_StandardREIT`, the subsector NAV tests) MUST stay green — they use fixtures with no `MaintenanceCapEx`. **Verify whether any of them set `CapitalExpenditures>0`**: if so, those fixtures now take the 0.7× ESTIMATE path and their headline value changes. Resolution: either (a) those fixtures have `CapitalExpenditures==0` (likely — FFO fixtures rarely set it) → no change, or (b) update the expected values AND add an explanatory comment that AFFO is now the headline. Determine this empirically in the RED phase; do NOT assume.

---

## 7. Invariant checklist

| Invariant | How this plan keeps it green |
|---|---|
| **Phase 2 bit-for-bit (AFFO absent ⇒ today's FFO output)** | `affoAvailable` gate (§3). When false, `valuePerShare = pffoValuePerShare` via the unchanged legacy computation; `IntrinsicValuePerShare` is the same float ops in the same order. Pinned by `TestFFOModel_Calculate_AFFO_AbsentBitForBit`. |
| **`TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC)** | Phase 2 touches only `ffo.go`, the parser (additive field), currency (additive multiply), and additive response/entity fields. DDM model code path is untouched; DDM fixtures set no `MaintenanceCapEx`; the new entity field is zero-valued on the DDM path. No DDM golden regeneration. |
| **recompute-shadow byte-identity** | `MaintenanceCapEx` is NOT a `computePlugs` or `recomputeUmbrellas` term (verified §4.1). `recompute.go` is untouched. The cleaner does not read the new field. `git diff --quiet internal/integration/testdata/recompute-shadow/` must exit 0 — confirm in CI. |
| **ledger-basket invariants** | The datacleaner adjuster pipeline does not read or write `MaintenanceCapEx`; no ledger entry/overlay references it. `TestLedger_BasketSnapshot_*` untouched. |
| **Replay diff.go field-count guard** | §2 row 9 + §5: two `goFieldToJSON` entries + `countFairValueFields` 33→35 in the same commit. Run a replay test to confirm no `init()` panic. |
| **POST{}==GET byte-identity** | Single `buildFairValueResponse`; no override touches FFO inputs; both new fields omitempty. |
| **Swagger pinned regen** | Module-pinned swag command (§2 row 10); never global binary. |
| **Coverage ≥90% on ffo.go** | Test matrix §6 exercises both AFFO branches, the floor, the bit-for-bit gate, and PFFO-always-surfaced. |

---

## 8. Commit ladder (each independently green: `go build ./...` + `go test ./...`)

1. **C1 — entity field.** Add `MaintenanceCapEx` to `entities.FinancialData` (+ godoc: positive, not-a-plug-term). `go build ./...`. (No behavior change; trivially green.)
2. **C2 — FX conversion.** `currency.go` multiply + `TestCurrency_MaintenanceCapEx_FXConverted`. Green.
3. **C3 — parser extraction.** `findValue` block + `GetSupportedConcepts` + `TestParser_MaintenanceCapEx_{Disclosed,Absent}`. Green. (Field now populated from XBRL where filed; all downstream still ignores it → no value change.)
4. **C4 — ModelResult fields.** Add `PFFOValuePerShare`/`PAFFOValuePerShare` to `ModelResult` (router.go). `go build`. Green (omitempty, unset everywhere).
5. **C5 — FFO model AFFO algorithm.** Implement §3 in `ffo.go` + the full `ffo_test.go` matrix. This is the behavior commit. Confirm `TestDDM_LegacyPath_BitForBit`, recompute-shadow, ledger-basket all still green. Confirm existing FFO tests (adjust expectations only if a fixture sets `CapitalExpenditures>0` — see §6 note).
6. **C6 — ValuationResult + service.go wiring.** Add entity result fields + map in `performAlternativeValuation`. Green.
7. **C7 — handler response + diff.go + swagger.** `FairValueResponse` fields + `buildFairValueResponse` mapping + `goFieldToJSON` + `countFairValueFields` 33→35 + `TestBuildFairValueResponse_AFFOFields`. Regenerate swagger. Run a replay test to confirm no `init()` panic. Green.
8. **C8 — CalculationVersion bump (see decision D4) + docs.** Bump version stamp, update the `service_test.go` version pins, tick spec Phase 2 checklist, add CLAUDE.md gotcha bullet, write closeout. Green.

> Rationale for the order: data plumbing (C1-C4) lands inert before the behavior commit (C5); the response/replay/version surface (C6-C8) lands last so the field-count guard and version pins flip in one reviewable step. Each commit compiles and tests green so a `git bisect` stays meaningful.

---

## 9. Open questions / decisions (with recommendations)

**D1 — Reuse P/FFO multiple on the AFFO base, or add a distinct P/AFFO table?**
Recommendation: **REUSE** (per spec § line 112). Real-world P/AFFO multiples are structurally higher than P/FFO (AFFO is a smaller base), so applying the P/FFO multiple to AFFO will systematically under-value vs a "correct" P/AFFO multiple. **This is a known, spec-sanctioned simplification for Phase 2.** Document it as a warning-string caveat and a CLAUDE.md note; a distinct `reit_paffo_multiples` table is an explicit follow-up (call it VAL-3 Phase 2.1). Surfacing both `pffo_value_per_share` and `paffo_value_per_share` lets a consumer see the FFO-based number unmolested, mitigating the under-valuation.

**D2 — When AFFO is negative (maint capex > FFO), what is the headline?**
Recommendation: floor `PAFFO` to 0, append a warning, and **keep the headline = `PAFFO` (0)**, mirroring the existing negative-FFO handling (ffo.go:247 floors the FFO value to 0 too). Rationale: if AFFO data is available, AFFO is the chosen base; a distressed REIT with maintenance capex exceeding FFO genuinely has ~0 distributable cash. Alternative (fall back to PFFO) would hide the distress signal. (Test `..._NegativeAFFOFlooredToZero` pins whichever is chosen.) Confirm with the spec owner if uncertain — this is a judgment call, not spec-mandated.

**D3 — Does the forward scaffold (Phase 3, ffo.go:265-297) project off FFO or AFFO?**
Recommendation: **leave it projecting off `ffoPerShare`** (unchanged). Phase 2 is trailing-only; rewiring the forward path to AFFO is Phase 3 scope. The forward fields stay omitempty/zero unless a profile horizon is set, so this is invisible in Phase 2. Note it in the closeout so Phase 3 picks it up.

**D4 — CalculationVersion bump?**
Recommendation: **YES — bump 4.8 → 4.9.** Phase 2 changes the headline `intrinsic_value_per_share` for any REIT with maintenance capex disclosed OR estimable (i.e. any REIT with `CapitalExpenditures>0`) — a real production value change on the REIT path. Per the established convention (e.g. the 4.6→4.7→4.8 ladder), the first production value-change warrants a version bump. The bump is engine-wide (single stamp at [service.go:2044](../../../internal/services/valuation/service.go) and the DCF-path stamp); update the four `service_test.go` `"4.8"` pins (lines 977, 2617, 2658, 3115) and the `bug015_quarterly_oi_base_test.go` pins (150, 249) to `"4.9"` in C8. **Caveat:** the version stamp is shared across all models, so bumping it drifts `calculation_version` on DDM/DCF/revenue_multiple responses too (their numeric values stay bit-for-bit — only the version string moves). This matches how 4.8 was handled (the comment at service.go:2044 documents exactly this engine-wide-stamp behavior). If the spec owner prefers to defer the bump until live REIT verification, gate D4 to C8 and make it the only thing C8 changes — but the recommendation is to bump now.

**D5 — Estimate ratio source (0.70).** Spec mandates 0.7 (§ line 112); practitioner range is 60-80% (§ line 37). Recommendation: ship `MaintenanceCapExEstimateRatio = 0.70` as a code constant (a documented domain default, not runtime-tunable). If a future need for per-subsector ratios arises, externalize to `industry_multiples.json` then — out of scope now.

---

## 10. Next steps
1. BACKEND (in `midas/` under its own harness): execute the commit ladder §8, RED-first per §6.
2. Resolve R1 (parser tag verification, §4.2) and D2/D4 with the spec owner before C5/C8 if convenient; both have safe defaults if no answer.
3. REVIEWER: pay attention to the bit-for-bit gate (§3), the diff.go field-count bump (§5), and whether any existing FFO fixture sets `CapitalExpenditures>0` (§6 note).
4. QA: validate the three spec acceptance scenarios (disclosed AFFO<FFO, 0.7× estimate + warning, bit-for-bit absent) live against a real REIT (DLR/PLD/SPG) and a non-REIT control.

HANDOFF_TO: BACKEND
