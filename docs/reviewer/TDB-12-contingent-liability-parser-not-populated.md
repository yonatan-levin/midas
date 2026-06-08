# TDB-12 — SEC parser does not populate contingent / environmental / litigation liabilities → B3 never fires

**Status:** IMPLEMENTED 2026-06-08 (branch `worktree-tdb-12-contingent-parser`) — parser populates all 3 fields; B3 now fires. Full cycle green (VERIFIER VERIFIED · REVIEWER APPROVE_WITH_NITS · QA PASS); `go test ./... -count=1` = 50/50; shadow byte-identical; DDM bit-for-bit; `currency.go` untouched; no CalcVersion bump.
**Priority:** P1 — Tier 1 (valuation correctness). The exact TDB-1 pattern, one rule over (B3 instead of C1/C3/C6).
**Type:** Correctness gap (silent — B3 short-circuits with no warning, no error).
**Mirrored as GitHub issue:** `[TDB-12]` / `#12` (yonatan-levin/midas).
**Worktree:** `worktree-tdb-12-contingent-parser` (own `go.mod`; validate with `GOWORK=off`).
**Related:** TDB-1 (the parser-extraction precedent this mirrors), TDB-3 (the AI-failed→heuristic B3
fallback that also goes live once these fields populate), DC-1 Phase 4/5 (B3 routing flip + dual-write
deletion — B3 now emits a `DebtLikeClaims` overlay, NOT a `TotalDebt` mutation).

**Design spec (ARCH, 2026-06-08):** `docs/refactoring/spec/tdb-12-contingent-liability-parser-extraction-spec.md`
**Implementer plan (ARCH, 2026-06-08):** `docs/refactoring/implementations/tdb-12-contingent-liability-parser-extraction-implementation-plan.md`

**ARCH note (2026-06-08):** Verified the exact gap — `parsePeriodData` (`sec/parser.go`) never populates
`ContingentLiabilities` / `EnvironmentalLiabilities` / `LitigationLiabilities`, so B3's
`totalContingent <= 0` gate (`liabilities.go:1099-1103`) short-circuits and B3 never fires in production
(confirmed zero on JNJ/XOM/CVX/PFE/MRK/BA). Design mirrors TDB-1's `findValue` candidate-list idiom.

Key verified findings:
- **Shadow-inert verdict: PREDICTED BYTE-IDENTICAL** (two independent reasons). (1) The three fields are
  **not terms in any `recomputeUmbrellas` formula** (`recompute.go:74-106` reads only
  Cash/Inventory/Goodwill/OtherIntangibles/DeferredTaxAssets/Other*/OperatingLeaseLiability*/TotalDebt +
  the four umbrellas) and **not in any `computePlugs` triple** (`plugs.go:55-108`). (2) B3 firing emits
  ONLY an `Overlays[Field:"DebtLikeClaims"]` entry; in this CalcVersion-4.7 worktree the B3 dispatcher arm
  (`liabilities.go:349-378`) is mutation-free and the Phase-5-deleted dual-write means B3 **never mutates
  an umbrella** (`liabilities.go:287-289, 384-388`). MUST be empirically confirmed via
  `git diff --quiet internal/integration/testdata/recompute-shadow/` at execute (the parser + B3 physically
  run on the basket fixtures).
- **DDM bit-for-bit: INERT** — goldens are pre-parsed `FinancialData` JSON, never through the parser.
- **FX: no change** — `currency.go:253-255` already FX-converts all three. Do NOT touch `currency.go`.
- **Double-count (the central hazard):** B3 SUMS the three fields. Guards: (1) within each field,
  `findValue` first-hit **aggregate-first** (never `sumValues`) so MSFT/MXL reporting both
  `LossContingencyAccrualAtCarryingValue` AND `…Current` are not double-counted; (2) across fields,
  disjoint candidate lists; (3) income-statement litigation-expense (C3's, mapped by TDB-1) and
  possible-loss DISCLOSURE tags deliberately EXCLUDED.
- **Behavior change:** B3 fires on filers reporting these undimensioned — confirmed in the basket: **AMD**
  (`AccrualForEnvironmentalLossContingencies` $4.8M), **MSFT/EQIX/MXL** (`LossContingencyAccrualAtCarryingValue`).
  Fair value DECREASES (DebtLikeClaims subtracted in EV→Equity bridge). **JNJ/XOM/CVX/PFE/MRK/BA stay
  zero** — they tag contingencies dimensionally / via custom elements, which the dimension-unaware
  companyfacts ingestion (`ports.SECFact` has no dimensional member) does not expose. Dimensional
  extraction is a deliberate non-goal (separate follow-up).

Open questions (recommendations in spec §10): Q1 sign = clamp negatives to 0 (NOT `math.Abs`); Q2 exclude
possible-loss disclosure; Q3 ship disjoint lists, accept bounded cross-field residual; Q4 us-gaap-only (no
IAS 37 IFRS mapping yet); Q5 litigation tags MEDIUM confidence (no basket filer reports them).

---

## Context

`internal/services/datacleaner/adjustments/liabilities.go:1099-1103` (B3 `processContingentLiabilityAdjustment`):

```go
totalContingentLiability := data.ContingentLiabilities + data.EnvironmentalLiabilities + data.LitigationLiabilities
if totalContingentLiability <= 0 { return /* Applied:false — no overlay, B3 skips */ }
```

The SEC parser never populates any of the three fields (grep: zero non-test write sites in `internal/`).
So in production B3 always short-circuits, never emits its `DebtLikeClaims` overlay, and the TDB-3
AI-failed→heuristic fallback (downstream of this gate) is also dead.

## Why it matters

The B3 overlay feeds `InvestedCapital().DebtLikeClaims`, which the EV→Equity bridge subtracts. A company
carrying a large recognized contingent/environmental/litigation accrual has that debt-like claim silently
ignored → overstated equity / fair value, with no warning. This is the "extract actual contingent
liabilities from financial data" intent — mislocated as an unpopulated parser field, exactly like TDB-1.

## Scope / Tasks

| ID | Task | File | Effort |
|---|---|---|---|
| TDB-12.1 | Identify the us-gaap (+ IFRS) XBRL concepts for contingent / environmental / litigation accruals; design disjoint, aggregate-first candidate lists | `sec/parser.go`, spec §3 | M |
| TDB-12.2 | Populate the three fields in `parsePeriodData` (balance-sheet section; `findValue` first-hit; clamp ≥0) | `sec/parser.go` | M |
| TDB-12.3 | Parser unit test incl. the aggregate-vs-split double-count guard + the two exclusion cases | `sec/parser_contingent_test.go` (new) | M |
| TDB-12.4 | Confirm B3 fires when fields populate (existing adjuster test already pins the populated path; parser test proves population; optional AMD integration assertion) | `adjustments` / `integration` | S |
| TDB-12.5 | Add tags to `GetSupportedConcepts()` (doc surface) | `sec/parser.go` | S |

## Acceptance

- [x] Parser populates all three fields from recognized ASC 450 / ASC 410 balance-sheet accruals
      (aggregate-first, clamp ≥0).
- [x] Aggregate + current/noncurrent split present → field == aggregate, NOT summed (double-count guard
      test).
- [x] Income-statement `LitigationSettlementExpense` → `LitigationLiabilities` == 0 (cross-rule
      exclusion); `LossContingencyEstimateOfPossibleLoss` → `ContingentLiabilities` == 0 (disclosure
      exclusion).
- [x] B3 emits a `DebtLikeClaims` overlay on a filing that reports these — test-confirmed.
- [x] DDM bit-for-bit + recompute-shadow snapshots stay green (byte-identical); plug invariants green.
- [x] Adjustments-projection basket golden: unchanged OR reviewed-and-regenerated as expected B3 overlays.
- [x] `currency.go` UNCHANGED; no `CalculationVersion` bump.

## Out of scope

- Dimensional / custom-element contingency extraction (the JNJ/XOM/CVX/PFE/MRK/BA zero case) — separate follow-up.
- IFRS-full IAS 37 `Provisions`-family mapping (Q4).
- Live-replay confirmation of the fair-value shift on a B3-firing ticker — operator follow-up (committed
  baselines are `calculation_version 4.1`-era, drift-confounded; not a CI gate — mirrors TDB-1).
