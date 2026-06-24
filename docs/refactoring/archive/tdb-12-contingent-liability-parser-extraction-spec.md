# TDB-12 — SEC parser extraction of contingent-liability accruals (contingent / environmental / litigation)

**Status:** DESIGN COMPLETE — ready for `/execute`. Implementation is a separate pass.
**Issue:** GitHub `#12` (`[TDB-12]`). Tracker: `docs/reviewer/archive/TDB-12-contingent-liability-parser-not-populated.md`.
**Type:** Correctness gap (silent — B3 short-circuits with no warning, no error today).
**Priority:** P1 — Tier 1 valuation correctness. The exact TDB-1 pattern, one rule over (B3 instead of C1/C3/C6).
**Worktree:** `worktree-tdb-12-contingent-parser` (own `go.mod`; validate with `GOWORK=off`).
**Plan:** `docs/refactoring/implementations/tdb-12-contingent-liability-parser-extraction-implementation-plan.md`.
**Engine baseline:** local master `CalculationVersion 4.7` (DC-1 all phases closed; B3 dual-write deleted).

---

## 1. Summary

The SEC XBRL parser (`internal/infra/gateways/sec/parser.go`, `parsePeriodData` at line 440)
never populates three `entities.FinancialData` balance-sheet liability fields:

| Field | Entity line | Consumer | Failure today |
|---|---|---|---|
| `ContingentLiabilities` | `financial_data.go:92` | B3 `ApplyB3Contingent` (`adjustments/liabilities.go:870`) | short-circuits: `totalContingent <= 0` → no overlay |
| `EnvironmentalLiabilities` | `financial_data.go:93` | B3 (summed into `totalContingent`) | same |
| `LitigationLiabilities` | `financial_data.go:94` | B3 (summed into `totalContingent`) | same |

B3 reads the **sum of all three** as a gross exposure, multiplies by a probability weight
(AI footnote analysis when enabled; deterministic industry heuristic otherwise — TDB-3), and emits a
declarative `OverlaySpec{Field:"DebtLikeClaims"}`. That overlay flows into
`InvestedCapital().DebtLikeClaims`, which the EV→Equity bridge **subtracts**, lowering equity value /
fair value for filers carrying material contingent accruals. Because the three fields are never
populated, B3 **never fires in production** — verified live on JNJ/XOM/CVX/PFE/MRK/BA (all zero), and
confirmed by grep (no non-test write site for any of the three fields in `internal/`).

`processContingentLiabilityAdjustment` (`liabilities.go:1099-1103`):

```go
totalContingentLiability := data.ContingentLiabilities + data.EnvironmentalLiabilities + data.LitigationLiabilities
if totalContingentLiability <= 0 { return /* Applied:false, no overlay */ }
```

The TDB-3 AI-failed→heuristic fallback (recently shipped) is downstream of this gate, so it too is dead
in production until these fields populate. TDB-12 makes B3 — and TDB-3 — **live**.

### Goals
- Populate `ContingentLiabilities`, `EnvironmentalLiabilities`, `LitigationLiabilities` in
  `parsePeriodData` from real us-gaap (and, where applicable, IFRS-full) XBRL **balance-sheet accrual**
  concepts, in **reporting currency** (pre-USD), using the established `findValue` candidate-list idiom.
- Design the three candidate lists to be **mutually disjoint** so the consumer's sum does not
  double-count (the central hazard — see §3).
- Pin population with a `sec` parser unit test using a crafted `SECCompanyFacts` fixture, including a
  double-count guard sub-case.
- Confirm B3 fires when the fields are populated (adjuster/integration test).
- Preserve every load-bearing invariant: DDM bit-for-bit goldens, recompute-shadow snapshots, plug
  invariants. (See §6 — verdict: all stay green; shadow predicted byte-identical, MUST be confirmed.)

### Non-goals
- **No change to the B3 adjuster code** (`ApplyB3Contingent` / `processContingentLiabilityAdjustment`).
  It already reads the fields and sums them correctly; this is a pure parser-population gap. The
  tracker's "confirm B3 fires" item is a *verification test*, not a code change.
- **No currency-code change.** All three fields are **already** FX-converted in `currency.go:253-255`
  (`fd.ContingentLiabilities *= rate`, etc.) and listed in the calculation-safety comment at
  `currency.go:26`. Populating them in reporting currency is sufficient; FX conversion handles them with
  **zero code change**. Stated explicitly so the implementer does **not** touch `currency.go`.
- **No residual-plug / umbrella treatment.** These three fields are **NOT** components of any
  `computePlugs` triple and **NOT** terms in any `recomputeUmbrellas` formula (see §6.2). They are
  parallel B3-input fields, extracted by `findValue` exactly like the B2 pension fields
  (`ProjectedBenefitObligation` / `PensionPlanAssets`, `parser.go:861-877`) and the A6 RightOfUse asset
  (`parser.go:850-858`, "deliberately NOT folded into computePlugs"). They must **not** be summed into
  `CurrentLiabilities` / `TotalLiabilities` or fed to `computePlugs`.
- **No dimensional / custom-element extraction.** The companyfacts API and the parser's fact store are
  **dimension-unaware** (see §2.3). TDB-12 maps only the standard, undimensioned us-gaap/IFRS concepts a
  filer reports on the face / in the default member. Dimensioned-only or custom-element contingencies
  (the likely reason JNJ/XOM/CVX/PFE/MRK/BA report zero — §3.4) are **out of scope**; a follow-up could
  add dimensional ingestion if a real filer justifies it.
- **No fetch/allowlist gating change** beyond documentation (`GetSupportedConcepts`, §4).

---

## 2. Architecture context (verified against this worktree)

1. **The SEC client fetches the entire company-facts blob.** `Client.GetCompanyFacts`
   (`sec/client.go:112`) GETs `/api/xbrl/companyfacts/CIK*.json` — the full document for the filer.
   There is **no per-concept request allowlist**; `GetSupportedConcepts()` is **not referenced by the
   client** (grep: only a unit test reads it). So adding a tag to a `findValue` list is sufficient to
   extract it whenever the filer reported it undimensioned.
2. **`extractFiscalPeriods` ingests every concept in every taxonomy** (`parser.go:283-303`), iterating
   `for conceptName, factGroup := range concepts` across `dei`/`us-gaap`/`ifrs-full` and storing each
   fact keyed by **bare concept name** (no taxonomy prefix) in `payload.values`. So the `data
   map[string]float64` inside `parsePeriodData` **already contains** every undimensioned concept the
   filer reported — including the contingent-liability concepts — whenever present. Adding a `findValue`
   block is the only change needed; nothing else fetches or gates.
3. **The fact store is DIMENSION-UNAWARE (decisive for scope and the double-count bound).** `ports.SECFact`
   (`ports/gateways.go:158-167`) has fields `End/Val/Accn/Fy/Fp/Form/Filed/Frame` — **no dimensional
   member**. The `/companyfacts/` endpoint only serves the **default (undimensioned) member** of each
   concept; dimensional breakdowns (`LossContingenciesByNatureOfContingencyAxis`) are not in this payload.
   `processFacts` (`parser.go:376-414`) stores `bucket[conceptName] = fact.Val` (**last-write-wins** within
   a period+currency bucket). **Consequence:** the parser can only ever see one scalar per concept per
   period — it physically cannot sum a dimensioned member with an undimensioned total. This *bounds* the
   double-count risk to the across-concept case (§3.3), and explains why dimensioned-only filers report
   zero (§3.4).
4. **Period / balance semantics match existing balance-sheet fields.** These three concepts are
   **instant / credit-balance** balance-sheet accruals (verified — §3.1). `processFacts` does not filter
   by period type; it stores `fact.Val` per concept exactly as for `Goodwill` / `Inventory` /
   `ProjectedBenefitObligation`. No new period-type handling is required. They are extracted in the
   **balance-sheet section**, alongside the existing liability fields.

**Consequence:** the change is mechanically small (three `findValue` blocks + doc-list entries). The
risk is **entirely in tag selection and cross-field double-count avoidance**, not in plumbing.

---

## 3. XBRL concept mapping (the load-bearing decision)

### 3.1 Balance type & sign

All target concepts are **instant, credit-balance recognized liabilities** (ASC 450 loss-contingency
accruals, ASC 410 environmental accruals, litigation reserves). Verified against the FASB balance-type
design rules and the real basket fixtures (§3.5). The companyfacts values for these are **positive**
(a credit-balance liability is reported as a positive magnitude in `Val`). B3 requires **positive
amounts** (`totalContingent > 0` gate; the gross is multiplied by a probability weight `[0,1]`).

**Sign handling decision:** unlike TDB-1's income-statement *charge* fields (which can be
credit-presented and need `math.Abs`), these are balance-sheet liabilities that are reported positive by
construction. A **negative** value would be a data anomaly (a contra-liability or filer error), not a
sign-presentation flip. The spec's default is to **clamp negatives to 0** (skip) rather than `Abs` them:
a negative contingent *liability* has no defensible "add this much debt-like claim" meaning, and `Abs`
could spuriously inflate `DebtLikeClaims` (lowering fair value) on a malformed fact. This is the opposite
of TDB-1's `Abs` choice and is intentional — see **OPEN QUESTION Q1**. (Empirically, every basket value
observed in §3.5 is ≥ 0, so this rule is defensive, not load-bearing for the basket.)

### 3.2 Concept → field mapping (verified)

`findValue` is first-hit; list the **aggregate** concept first, then the current+noncurrent split only
as a *fallback for filers that report ONLY the split* (never summed with the aggregate — see §3.3).

| Field | us-gaap candidates (priority order) | IFRS-full | Sign | Confidence | Verification |
|---|---|---|---|---|---|
| `ContingentLiabilities` | `LossContingencyAccrualAtCarryingValue`, `LossContingencyAccrualCarryingValueCurrent`, `LossContingencyAccrualCarryingValueNoncurrent` | *(none mapped — Q4)* | clamp ≥0 | HIGH (us-gaap) | aggregate present in EQIX/MSFT/MXL fixtures; `…Current` in MSFT/MXL. MSFT reports BOTH aggregate ($541M FY25) AND `…Current` ($364M) → **double-count trap**: first-hit on aggregate avoids it (§3.3) |
| `EnvironmentalLiabilities` | `AccrualForEnvironmentalLossContingencies`, `AccruedEnvironmentalLossContingenciesNoncurrent`, `AccruedEnvironmentalLossContingenciesCurrent` | *(none mapped — Q4)* | clamp ≥0 | HIGH (us-gaap) | `AccrualForEnvironmentalLossContingencies` present in AMD ($4.8M FY23, aggregate). `Accrued…Noncurrent`/`…Current` are the split fallbacks |
| `LitigationLiabilities` | `EstimatedLitigationLiability`, `LitigationReserve` | *(none mapped — Q4)* | clamp ≥0 | MEDIUM | Both are standard recognized-litigation-liability balance-sheet concepts (credit/instant), but **neither appears in the 10-ticker basket** — large filers tag litigation either dimensionally or via custom elements (§3.4). Mapped for coverage of filers that DO report them; unverified against a live basket filer → MEDIUM. **Do NOT add `LitigationSettlementExpense` / `LossContingencyLossInPeriod` here — those are income-statement charges already mapped to C3 by TDB-1 (would double-count across rules).** |

**Concepts deliberately EXCLUDED** (with rationale — these are the traps):

| Excluded concept | Why excluded |
|---|---|
| `LossContingencyEstimateOfPossibleLoss` | **Disclosure-only** "reasonably possible" range (ASC 450), NOT a recognized accrual. B3 multiplies the gross by a probability; feeding a *possible-loss* disclosure (already an unaccrued upper bound) would over-weight a number the filer explicitly chose not to recognize. Use the **accrued carrying value**, not the possible-loss estimate. (See Q2.) |
| `LossContingencyDamagesAwardedValue`, `LossContingencyEstimateOfPossibleLossMinimum/…Maximum` | Disclosure facts (awards / ranges), not balance-sheet accruals. Present in JNJ but meaningless as a debt-like claim. |
| `LitigationSettlementExpense`, `LossContingencyLossInPeriod` | **Income-statement period charges** — already mapped to `LitigationSettlements` (C3) by TDB-1. Reusing them here would (a) double-count the same dollars in two different rules and (b) mix a duration flow into an instant liability field. |
| `LossContingencyAccrualPaymentsValue` | Rollforward **flow** (payments during the period), not a balance-sheet amount. |
| `EnvironmentalRemediationExpense*` | Income-statement expense, not the accrued liability. |

### 3.3 Double-count analysis (the central hazard) — THREE layers

The consumer **sums** all three fields, so overlap *across* the three lists, or *within* a list, inflates
the gross before probability-weighting. Three distinct double-count vectors, each guarded:

1. **Within `ContingentLiabilities`: aggregate vs current/noncurrent split.** The aggregate
   `LossContingencyAccrualAtCarryingValue` is the **total** of `…Current` + `…Noncurrent`. MSFT reports
   BOTH the aggregate AND `…Current` in companyfacts. **Guard:** `findValue` (first-hit) with the
   **aggregate first** → when the aggregate is present it wins and the split variants are never read; the
   split variants only fire for filers that report ONLY the split (no aggregate). **Never use
   `sumValues` here** — it would add aggregate + current and double-count MSFT. *(Same idiom TDB-1 used
   for restructuring's alternative-presentation tags.)*
2. **Within `EnvironmentalLiabilities`: aggregate vs split.** Identical structure —
   `AccrualForEnvironmentalLossContingencies` is the total of `Accrued…Current` + `Accrued…Noncurrent`.
   Same first-hit-aggregate-first guard; **no `sumValues`**.
3. **Across the three fields: does the general loss-contingency accrual already INCLUDE
   environmental + litigation?** ASC 450's `LossContingencyAccrualAtCarryingValue` is a *generic*
   loss-contingency total that **can** conceptually include environmental and litigation accruals. The
   taxonomy does **not** enforce a calculation link making the three disjoint, so a filer that tags the
   generic total AND a specific environmental accrual *might* overlap. **However, the parser's
   dimension-unaware, undimensioned-only ingestion bounds this risk** (§2.3): a filer that decomposes
   contingencies by nature does so *dimensionally* (members the parser never sees), and a filer that
   reports a separate `AccrualForEnvironmentalLossContingencies` line *typically* does so *because* it is
   broken out from the general accrual. In the **observed basket** there is **no overlap** — AMD reports
   environmental only; EQIX/MSFT/MXL report the general accrual only; no ticker reports both an
   environmental/litigation specific tag AND the general accrual. **Guard + residual risk:** we accept
   the small residual cross-field overlap risk as bounded and documented, because (a) it is unobserved in
   the basket, (b) B3's probability weight (`< 1`) and 1%-of-revenue flag threshold damp small
   over-counts, and (c) the failure mode of the *alternative* (mapping only the general accrual and
   dropping the specific environmental/litigation breakouts) silently under-counts AMD-style filers — the
   exact bug TDB-12 fixes. **OPEN QUESTION Q3** surfaces a stricter "general accrual XOR specific tags"
   precedence if a real overlapping filer is found.

### 3.4 Why JNJ/XOM/CVX/PFE/MRK/BA report zero (and why that is acceptable)

Empirically (JNJ companyfacts, full 8.6 MB blob): JNJ reports **none** of the nine accrual concepts —
only disclosure facts (`LossContingencyDamagesAwardedValue`, `LossContingencyNumberOfPlaintiffs`,
`LossContingencyPendingClaimsNumber`) and income-statement litigation expenses (already C3's via TDB-1).
Large filers tag litigation/environmental contingencies **dimensionally** (by-nature axis) or via
**custom elements**, neither of which the dimension-unaware companyfacts ingestion exposes (§2.3). So
TDB-12 will **not** make B3 fire for JNJ/XOM/CVX/PFE/MRK/BA — and that is the correct, conservative
outcome: B3 should fire on the *recognized accrual a filer chose to report undimensioned*, not on a
guessed reconstruction of dimensional disclosures. TDB-12's win is on filers that DO report these
undimensioned — confirmed: **EQIX, MSFT, MXL** (general accrual) and **AMD** (environmental). Closing the
dimensional-extraction gap is a separate, larger follow-up (flag in tracker; do not attempt here).

### 3.5 Basket evidence (captured `artifacts/tier2-baseline/2026-05-19/*/05-fetch-sec.raw.json`)

| Ticker | Concept | Value (recent FY) | Maps to | Note |
|---|---|---|---|---|
| AMD | `AccrualForEnvironmentalLossContingencies` | $4.8M (FY23) | `EnvironmentalLiabilities` | aggregate; ~0.02% of revenue → populates but below B3 flag threshold |
| EQIX | `LossContingencyAccrualAtCarryingValue` | $0 (2013Q2 only) | `ContingentLiabilities` | sole stale fact; populates 0 → B3 skip (correct) |
| MSFT | `LossContingencyAccrualAtCarryingValue` | $541M (FY25) | `ContingentLiabilities` | **aggregate; first-hit wins** |
| MSFT | `LossContingencyAccrualCarryingValueCurrent` | $364M (FY22) | *(not read — aggregate present)* | **double-count trap source** |
| MXL | `LossContingencyAccrualAtCarryingValue` | $0 (FY22) | `ContingentLiabilities` | aggregate present → split ignored |
| MXL | `LossContingencyAccrualCarryingValueCurrent` | $3.6M (2015Q2) | *(not read for FY when aggregate present)* | trap source |
| JNJ/BABA/TSM/F/KO/AAPL | *(none of the nine accrual concepts)* | — | — | zero — dimensional/custom or none (§3.4) |

This confirms: the first-hit-aggregate-first guard is **load-bearing** (MSFT/MXL exercise it), and at
least one field populates a non-zero real value (AMD environmental) for an end-to-end B3-fires test.

---

## 4. Insertion point & idiom

Insert three `findValue` blocks in `parsePeriodData`, in the **balance-sheet liability section**, after
the pension/benefit block (`parser.go:861-877`) and before share extraction (`parser.go:879`). This
groups them with the other B-rule liability inputs (`OperatingLeaseLiability`, pension) and runs before
`computePlugs` (line 928) — which is unaffected (these are not plug components). Illustrative (final code
authored in `/execute`):

```go
// B3 contingent-liability inputs (TDB-12). Recognized ASC 450 / ASC 410
// balance-sheet accruals (instant, credit-balance). B3 sums all three as the
// gross exposure, then probability-weights it into a DebtLikeClaims overlay.
// The three candidate lists are mutually disjoint (general vs environmental vs
// litigation) and aggregate-first within each (NOT sumValues) so a filer
// reporting both an aggregate and its current/noncurrent split is not
// double-counted. Negatives clamp to 0 (a negative recognized liability is a
// data anomaly, not a sign-presentation flip — TDB-12 spec §3.1, Q1).
if val, exists := p.findValue(data, []string{
    "LossContingencyAccrualAtCarryingValue",        // aggregate (first-hit wins over the split)
    "LossContingencyAccrualCarryingValueCurrent",   // split fallback (filers reporting ONLY the split)
    "LossContingencyAccrualCarryingValueNoncurrent",
}); exists && val > 0 {
    financialData.ContingentLiabilities = val
}
// Environmental: aggregate first, then current/noncurrent split fallback.
if val, exists := p.findValue(data, []string{
    "AccrualForEnvironmentalLossContingencies",
    "AccruedEnvironmentalLossContingenciesNoncurrent",
    "AccruedEnvironmentalLossContingenciesCurrent",
}); exists && val > 0 {
    financialData.EnvironmentalLiabilities = val
}
// Litigation: recognized litigation-reserve balance-sheet concepts. NOT the
// income-statement LitigationSettlementExpense (that is C3's, mapped by TDB-1).
if val, exists := p.findValue(data, []string{
    "EstimatedLitigationLiability",
    "LitigationReserve",
}); exists && val > 0 {
    financialData.LitigationLiabilities = val
}
```

**Note on `val > 0` vs `absAddBack`:** the inline `&& val > 0` implements the §3.1 clamp-negatives-to-0
decision (Q1). Do **not** reuse `absAddBack` here — that helper is for TDB-1's credit-presented charges;
its `math.Abs` semantics are wrong for a balance-sheet liability (§3.1). No new `math` usage is required.

Also add the new tags to `GetSupportedConcepts()` (`parser.go:1034`) under a new
`Balance Sheet - Contingent Liabilities (B3)` sub-header (us-gaap) plus a comment that no IFRS-full
mapping ships (Q4), for documentation-surface accuracy (precedent: TDB-1, M-1d, Phase B6).

---

## 5. FX handling (verified — no code change)

All three fields are **already** FX-converted for foreign private issuers:
`internal/services/valuation/currency.go:253-255` multiplies each by the FX `rate`, and the
calculation-safety comment at `currency.go:26` lists them. Populating them in **reporting currency**
(which `parsePeriodData` does by construction — `extractFiscalPeriods` collapses to the dominant currency
bucket before `parsePeriodData` runs) is sufficient. `currency.go` MUST remain **unchanged**. (No basket
IFRS filer reports these concepts anyway — §3.4 — so the FX path is currently exercised only by the
unit-test fixture if it includes a non-USD case; the production FX wiring is already correct.)

---

## 6. Behavior-change & invariant-risk analysis

### 6.1 Which outputs move, and why that is correct

On any filer reporting these accruals undimensioned (confirmed: AMD, MSFT, EQIX/MXL when non-zero):
- B3 **fires for the first time** → emits `OverlaySpec{Field:"DebtLikeClaims", Amount: gross × probability}`.
- `InvestedCapital().DebtLikeClaims` increases by that amount.
- The **EV→Equity bridge subtracts** `DebtLikeClaims` → **equity value / fair value DECREASES** by
  `gross × probability × (per-share factor)` for that filer. Direction: **down** (a debt-like claim
  reduces equity holders' residual). For AMD ($4.8M × probability, vs ~$22B revenue) the move is
  immaterial; for a filer with a large recognized accrual it is material — the intended correction.
- B3 also emits a `contingent_liability_exposure` **flag** when `gross/revenue ≥ threshold` (1% default,
  2-3% for tech/energy/healthcare).

This is the entire point of TDB-12 and TDB-3. Fair values move **toward correctness** on affected
tickers. Deliberate, documented behavior change — not a regression.

### 6.2 Load-bearing invariant verdict

| Invariant | Source / mechanism | Affected? | Evidence |
|---|---|---|---|
| **DDM bit-for-bit goldens** (`TestDDM_LegacyPath_BitForBit`, JPM/BAC/WFC) | Goldens are **pre-parsed `FinancialData` JSON** loaded via `os.ReadFile`+`json.Unmarshal`, NEVER through the parser. Banks' golden inputs carry `contingent_liabilities:0` (and the two siblings) baked in. | **INERT** | Same mechanism TDB-1 confirmed. The parser is not on this code path; the three fields are absent/zero in the golden inputs. |
| **Recompute-shadow snapshots** (`TestDataCleanerRecompute_ShadowMode_TickerBasket` + `internal/integration/testdata/recompute-shadow/*.json`) | Test **re-parses raw company-facts** via `parser.ParseFinancialData` (`datacleaner_recompute_shadow_test.go:124`) then runs the FULL cleaner (incl. B3) — so the parser change **DOES run** on the basket, and B3 now **fires** on AMD/MSFT. The snapshot records ONLY `recomputeUmbrellas` divergence WARNs. | **PREDICTED INERT — MUST VERIFY** | **Two independent reasons it stays byte-identical:** (1) The three new fields are **not terms in any `recomputeUmbrellas` formula** — that function reads only `Cash/Inventory/OtherCurrentAssets`, `Goodwill/OtherIntangibles/DeferredTaxAssets/OtherNonCurrentAssets`, `OperatingLeaseLiability{Current,Noncurrent}/OtherCurrent/OtherNonCurrentLiabilities`, `TotalDebt`, and the four umbrellas (`recompute.go:74-106`). None of the contingent fields appears. (2) B3 firing emits **only** an `Overlays[Field:"DebtLikeClaims"]` entry; in this CalcVersion-4.7 worktree the B3 dispatcher arm (`liabilities.go:349-378`) calls the mutation-free `ApplyB3Contingent` and drains overlays/ledger/flags — the Phase-5-deleted dual-write means **B3 NEVER mutates `TotalDebt`/`TotalLiabilities`/`CurrentLiabilities` or any plug-component field** (`liabilities.go:287-289, 384-388`: "the no-op dualWrite closure … are DELETED. … B-rules never mutate an umbrella"). So the recomputed umbrella values, the reported umbrella values, and the WARN set are all mathematically unperturbed → `git diff --quiet recompute-shadow/` stays clean. **This is a prediction; the implementer MUST run the suite + git-diff to confirm before claiming done.** |
| **Plug invariants** (`computePlugs`, `datacleaner_plug_invariants_test.go`) | Asserts `umbrella == sum(known_components) + plug` on the four balance-sheet triples (`plugs.go:55-108`). | **INERT** | The three new fields are **not in any plug triple** — `computePlugs` reads only Cash/Inventory/Goodwill/OtherIntangibles/DeferredTaxAssets/OperatingLeaseLiability*/TotalDebt and the four umbrellas. They are parallel B3-input fields (same disposition as the A6 RightOfUse asset, `parser.go:848-849`). |
| **Adjustments-projection basket golden** (`adjustments_projection_basket_test.go` / `…_golden.json`) | Pins `result.Adjustments` produced from `data.AdjustmentLedger` for the basket. | **MAY MOVE — VERIFY** | B3 firing on AMD/MSFT adds a new ledger entry + overlay → the projection golden for those tickers **legitimately changes**. If the golden basket includes AMD/MSFT and runs AI-disabled with these fields populated, the implementer must inspect the diff and **regenerate only if the new entries are the expected B3 contingent overlays** (not blindly). If the golden uses pre-parsed fixtures with the fields zeroed, it is inert. **The implementer MUST check which tickers/fixtures the golden covers** (it currently runs B3 with these fields = 0, so today's golden has no B3 contingent entry). See plan §validation. |
| **Tier-2 replay baseline** (`artifacts/tier2-baseline/*`) | Replay re-runs captured raw input through the engine and diffs vs saved output. | **Out of scope for CI gate** | Committed baselines are `calculation_version 4.1`-era — drift-confounded across DC-1 Phases 2-5 + TDB-1. Replay verification is an **operator follow-up**, not a blocker (mirrors TDB-1 / DC-1 Phase-5 disposition). Flag in tracker; do not gate on it. |

**Net verdict:** DDM goldens and plug invariants are **provably inert**. Shadow snapshots are
**predicted inert** (contingent fields are not umbrella terms; B3 emits only a `DebtLikeClaims` overlay
that never mutates an umbrella in 4.7) — but the prediction MUST be empirically confirmed because the
parser + B3 physically run on the basket fixtures. The **adjustments-projection basket golden is the one
artifact that may legitimately move** (B3 now fires on AMD/MSFT) — handle per the regenerate-and-review
protocol in the plan, NOT blindly. If the shadow diff is NOT clean, STOP and re-examine — do not
regenerate shadow snapshots.

---

## 7. Test strategy

1. **Primary: `sec` parser unit test** (RED→GREEN), mirroring
   `TestParser_ParseFinancialData_NonRecurringEarningsItems` (`parser_test.go:129`).
   New `TestParser_ParseFinancialData_ContingentLiabilities`, table-driven over crafted
   `ports.SECCompanyFacts` literals (Apple-style FY-2023 USD facts + the minimum Revenue/OperatingIncome
   to clear the insufficient-data guard). Cases (each asserts all three fields on
   `historical.Data["2023FY"]`):
   - `contingent_aggregate` — only `LossContingencyAccrualAtCarryingValue` → `ContingentLiabilities` = it,
     other two = 0.
   - `contingent_split_fallback` — only `LossContingencyAccrualCarryingValueCurrent` (no aggregate) →
     `ContingentLiabilities` = it (first-hit fallback fires).
   - **`contingent_aggregate_wins_over_split` (THE DOUBLE-COUNT GUARD)** — BOTH
     `LossContingencyAccrualAtCarryingValue` (e.g. 541) AND `LossContingencyAccrualCarryingValueCurrent`
     (e.g. 364) present → `ContingentLiabilities` == 541 (aggregate), **NOT 905** (not summed). Pins the
     §3.3-vector-1 guard so a future refactor to `sumValues` is caught.
   - `environmental_aggregate` — `AccrualForEnvironmentalLossContingencies` → `EnvironmentalLiabilities`.
   - `litigation_reserve` — `EstimatedLitigationLiability` (or `LitigationReserve` fallback) →
     `LitigationLiabilities`.
   - **`excludes_income_statement_litigation`** — only `LitigationSettlementExpense` present →
     `LitigationLiabilities` == 0 (it is C3's field, not B3's). Pins the cross-rule exclusion.
   - **`excludes_possible_loss_disclosure`** — only `LossContingencyEstimateOfPossibleLoss` present →
     `ContingentLiabilities` == 0 (disclosure, not accrual). Pins the §3.2 exclusion.
   - `negative_clamps_to_zero` — a negative `LossContingencyAccrualAtCarryingValue` →
     `ContingentLiabilities` == 0 (Q1 clamp). Pins the sign decision.
   - `all_absent` — none present → all three = 0 (no false population).
2. **B3-fires confirmation** (satisfies the tracker's "confirm B3 fires" as a test, not a manual step).
   Prefer reusing an existing B3 adjuster test (`b3_contingent_liabilities_adjuster_test.go`) that builds
   a `FinancialData` with a populated `ContingentLiabilities` and asserts the overlay is emitted —
   confirm such a test exists and exercises the populated path; if it only tests the field-already-set
   path (it does), the parser test above is what proves population, and no net-new B3 test is needed.
   Optionally add a focused integration assertion: parse a crafted/real (AMD) fixture → run the cleaner
   AI-disabled → assert a `DebtLikeClaims` overlay with the expected `gross × heuristic-probability` amount.
3. **Full-suite + invariant gates** (plan §validation): `GOWORK=off go build ./... && go vet ./... &&
   go test ./... -count=1`, the named invariant tests, and
   `git diff --quiet internal/integration/testdata/recompute-shadow/`.
4. **Adjustments-projection golden** — run `adjustments_projection_basket_test.go`; if it covers AMD/MSFT
   with these fields populated, review the diff and regenerate ONLY if it is the expected B3 overlay
   entries (plan §validation).

Coverage: keep the `sec` package at/above its current coverage (project policy: ≥90% critical finance,
≥80% overall — `CLAUDE.md`). No new threshold invented.

---

## 8. Risks

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Aggregate + current/noncurrent double-count (MSFT/MXL) | Medium (real filers report both) | High (inflates DebtLikeClaims → wrong fair value) | **first-hit `findValue` aggregate-first, never `sumValues`**; explicit double-count guard test (§7 case 3) |
| Cross-field overlap (general accrual includes environmental/litigation) | Low (unobserved in basket; dimension-unaware ingestion bounds it) | Medium | §3.3 vector 3; Q3 offers stricter XOR precedence if a real overlapping filer surfaces; probability weight + flag threshold damp small over-counts |
| Wrong concept mapped (disclosure vs accrual) | Low (concepts verified credit/instant; disclosure tags explicitly excluded) | High | §3.2 exclusion table; `…EstimateOfPossibleLoss` / `…DamagesAwardedValue` excluded; exclusion test (§7 case 7) |
| Reusing C3's income-statement litigation tag | Low (explicitly excluded) | High (cross-rule double-count) | §3.2 exclusion; `excludes_income_statement_litigation` test (§7 case 6) |
| Shadow snapshot drift (prediction wrong) | Low | Medium (blocks merge) | Mandatory `git diff --quiet recompute-shadow/` gate; if dirty, STOP + re-examine, do not regenerate |
| Adjustments-projection golden moves unexpectedly | Medium (B3 now fires on AMD/MSFT) | Low (legitimate) | regenerate-and-review protocol (plan §validation); inspect diff = only B3 contingent overlays |
| Litigation tags unverified against a live basket filer | Medium | Low (only fire for filers that report them; us-gaap path unaffected) | MEDIUM confidence flagged in code comment + Q5; us-gaap general/environmental paths are the load-bearing wins |

---

## 9. Acceptance criteria

- [ ] `parsePeriodData` populates `ContingentLiabilities`, `EnvironmentalLiabilities`,
      `LitigationLiabilities` from the §3.2 candidate lists, in reporting currency, in the balance-sheet
      section, with negatives clamped to 0.
- [ ] A fixture with BOTH `LossContingencyAccrualAtCarryingValue` and
      `LossContingencyAccrualCarryingValueCurrent` yields `ContingentLiabilities` == the aggregate, NOT
      the sum (double-count guard).
- [ ] A fixture with only `LitigationSettlementExpense` yields `LitigationLiabilities` == 0 (cross-rule
      exclusion); a fixture with only `LossContingencyEstimateOfPossibleLoss` yields
      `ContingentLiabilities` == 0 (disclosure exclusion).
- [ ] New tags added to `GetSupportedConcepts()` for documentation accuracy.
- [ ] New `sec` parser unit test pins all of the above.
- [ ] B3 fires (emits a `DebtLikeClaims` overlay) when the fields are populated — test-confirmed.
- [ ] `GOWORK=off go build ./... && go vet ./... && go test ./... -count=1` all green.
- [ ] `TestDDM_LegacyPath_BitForBit` green (inert).
- [ ] `git diff --quiet internal/integration/testdata/recompute-shadow/` exits 0 (snapshots byte-identical).
- [ ] Plug-invariant tests green (inert).
- [ ] Adjustments-projection basket golden: either unchanged, or changed only by the expected B3
      contingent overlay entries (reviewed, not blindly regenerated).
- [ ] `internal/services/valuation/currency.go` UNCHANGED (FX already covers these fields).
- [ ] Tracker updated to link this spec + the plan; Status stays OPEN until `/execute` lands.

---

## 10. Open questions (decide before / during execute)

- **Q1 (sign policy):** Confirm **clamp-negatives-to-0** (`&& val > 0`) for these balance-sheet
  liabilities, NOT TDB-1's `math.Abs`. A negative recognized liability is a data anomaly, not a
  credit-presentation flip; `Abs` could spuriously inflate `DebtLikeClaims` and lower fair value on a
  malformed fact. **Recommendation: clamp (val > 0).** (Empirically all basket values ≥ 0, so defensive.)
- **Q2 (possible-loss disclosure):** Confirm `LossContingencyEstimateOfPossibleLoss` stays EXCLUDED
  (disclosure-only, not a recognized accrual; B3 already probability-weights, so feeding an unaccrued
  upper-bound double-discounts the wrong way). **Recommendation: exclude.** If a future requirement wants
  *possible-loss* exposure modeled, that is a separate B3-input design, not TDB-12.
- **Q3 (cross-field precedence):** Today's design maps general / environmental / litigation to disjoint
  candidate lists and accepts the bounded residual risk that a filer's general
  `LossContingencyAccrualAtCarryingValue` overlaps a separately-reported environmental/litigation accrual
  (unobserved in the basket). **Recommendation: ship as-is** (disjoint lists, first-hit-aggregate-first
  within each). If a real overlapping filer is found, add a precedence rule (e.g., if specific
  environmental/litigation tags are present, treat the general accrual as the *non-specific residual* —
  but the parser can't compute that residual without the dimensional members, so the practical fallback
  is "prefer specific, skip general for that filer"). Flag, don't pre-build.
- **Q4 (IFRS):** No IFRS-full contingent-liability concept is mapped (e.g. `ifrs-full:Provisions`,
  `ProvisionsForLegalProceedings`, `OtherProvisions` — IAS 37). The basket's IFRS filers (TSM/BABA)
  report none of the us-gaap accrual tags and IFRS provisions are typically dimensioned by provision
  class. **Recommendation: ship us-gaap-only**; add IAS 37 `Provisions`-family mapping later if a real
  20-F filer surfaces the need — do not guess at unverified IFRS tag names / dimensional structure.
- **Q5 (litigation tag confidence):** `EstimatedLitigationLiability` / `LitigationReserve` are standard
  recognized-litigation-liability concepts but appear in **no basket fixture** (large filers tag
  litigation dimensionally/custom — §3.4). **Recommendation: include both at MEDIUM confidence** (they
  only fire for filers that report them; the us-gaap general/environmental paths are the verified wins),
  with a code comment flagging the live-verification gap.
