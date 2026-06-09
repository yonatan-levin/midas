# TDB-1 — SEC parser extraction of non-recurring earnings items (restructuring / litigation / capitalized interest)

**Status:** DESIGN COMPLETE — ready for `/execute`. Implementation is a separate pass.
**Issue:** GitHub `#1` (`[TDB-1]`). Tracker: `docs/reviewer/archive/TDB-1-parser-restructuring-litigation-capex-not-populated.md`.
**Type:** Correctness gap (silent — no warning, no error today).
**Priority:** P1 — Tier 1 valuation correctness. Single highest-value gap from the 2026-06-06 TODO burn-down.
**Worktree:** `worktree-tdb-1-parser-extraction` (own `go.mod`; validate with `GOWORK=off`).
**Plan:** `docs/refactoring/implementations/tdb-1-parser-nonrecurring-extraction-implementation-plan.md`.

---

## 1. Summary

The SEC XBRL parser (`internal/infra/gateways/sec/parser.go`, `parsePeriodData` at line 439)
never populates three `entities.FinancialData` income-statement fields:

| Field | Entity line | Consumer | Failure today |
|---|---|---|---|
| `RestructuringCharges` | `financial_data.go:47` | C1 `ApplyC1Restructuring` (`adjustments/earnings.go:118-190`) | falls back to a **1.5%-of-revenue guess** when `<= 0` |
| `LitigationSettlements` | `financial_data.go:49` | C3 `ApplyC3Litigation` (`adjustments/earnings.go:331-397`) | **skips** when `<= 0` (never fires) |
| `CapitalizedInterest` | `financial_data.go:52` | C6 `ApplyC6CapitalizedInterest` (`adjustments/earnings.go:552-595`) | **skips** when `<= 0` (never fires) |

All three flow into `NormalizedOperatingIncome` (C1/C3) or `InterestExpense` (C6), which drive the
DCF. Because they are never populated, normalization runs on a guess (C1) or silently no-ops (C3/C6)
on every real filing — distorting fair value with no warning. All three adjusters expect a **positive
add-back magnitude** in the field.

### Goals
- Populate `RestructuringCharges`, `LitigationSettlements`, `CapitalizedInterest` in `parsePeriodData`
  from real us-gaap and (where applicable) IFRS-full XBRL concepts, in **reporting currency** (pre-USD),
  using the established `findValue` candidate-list idiom.
- Normalize each populated value to the **positive add-back magnitude** the C1/C3/C6 adjusters require,
  with explicit, documented sign handling (the dangerous case is litigation — see §3).
- Pin population with a `sec` parser unit test using a crafted `SECCompanyFacts` fixture.
- Preserve every load-bearing invariant: DDM bit-for-bit goldens, recompute-shadow snapshots,
  plug invariants. (See §5 — verdict: all stay green.)

### Non-goals
- **No change to the C1/C3/C6 adjuster code.** They already read the fields correctly; this is a
  pure parser-population gap. (The tracker's TDB-1.3 is a *verification* step, not a code change.)
- **No population of the other unpopulated Category-C fields** (`AssetSaleGains` C2,
  `StockBasedCompensation` C4, `DerivativeGainsLosses` C5, `WorkingCapitalAdjustment` C7). Out of
  scope; TDB-1 is strictly the three named fields. Note they share the same gap and the same idiom —
  a natural follow-up, filed separately if desired.
- **No currency-code change.** All three fields are already in the FX-conversion calculation-safety
  list (`internal/services/valuation/currency.go:210,212,215`). Populating them in reporting currency
  is sufficient; FX conversion handles them with zero code change. Stated explicitly so the implementer
  does not touch `currency.go`.
- **No residual-plug treatment.** Unlike the four `Other*` balance-sheet plugs in `plugs.go`
  (`computePlugs`), these three are **direct income-statement line items**, extracted by `findValue`
  exactly like `Revenue`/`OperatingIncome`/`InterestExpense`. Confirmed against `plugs.go` — they are
  not residuals and must not be computed as such.
- **No fetch/allowlist gating change** beyond documentation. See §4 (architecture note).

---

## 2. Architecture context (verified)

1. **The SEC client fetches the entire company-facts document.** `Client.GetCompanyFacts`
   (`internal/infra/gateways/sec/client.go:112`) GETs `/api/xbrl/companyfacts/CIK*.json` — the full
   blob for the filer. There is **no per-concept request allowlist**.
2. **`extractFiscalPeriods` ingests every concept in every taxonomy.** It iterates
   `for conceptName, factGroup := range concepts` across `dei`/`us-gaap`/`ifrs-full`
   (`parser.go:282-303`) and stores each fact keyed by **bare concept name** (no taxonomy prefix) in
   `periodPayload.values`. So `data map[string]float64` inside `parsePeriodData` **already contains**
   every concept the filer reported — including the three we want — whenever the filer reported them.
3. **`findValue` is a first-hit lookup over a candidate-name slice** (`parser.go:860-867`); `sumValues`
   sums disjoint components (`parser.go:893-903`). Populating a field is therefore just adding a
   `findValue` block in `parsePeriodData` — the data is present in `data`; nothing else fetches or
   gates it.
4. **`GetSupportedConcepts()` (`parser.go:951`) is documentation/reflection only** — no production
   caller (grep: only a unit test references it). It is NOT a fetch allowlist. Precedent (M-1d,
   Phase B6) still adds new tags here for an accurate documentation surface; we follow that precedent.
5. **Duration semantics are handled identically to existing income-statement fields.** `processFacts`
   (`parser.go:375-427`) does not filter by period type; it stores `fact.Val` per concept, last-write-
   wins within a currency bucket, exactly as it does for `Revenue`/`OperatingIncome`. These three are
   all duration (period-flow) concepts, so they behave like the already-working income-statement fields.
   No new period-type handling is required.

**Consequence:** the change is mechanically small (three `findValue` blocks + sign normalization +
doc-list entries). The risk is **entirely in tag selection and sign handling**, not in plumbing.

---

## 3. XBRL concept mapping (the load-bearing decision)

### 3.1 Sign convention — the central hazard

The C1/C3/C6 adjusters all expect a **positive add-back magnitude**:
- C1: if `RestructuringCharges <= 0` → uses the 1.5%-of-revenue estimate (so a wrong-signed value
  silently falls back to the guess).
- C3: if `LitigationSettlements <= 0` → **skips**.
- C6: if `CapitalizedInterest <= 0` → **skips**.

XBRL facts for these concepts are *usually* positive (debit-balance expense / capitalized-cost
elements; confirmed via FASB balance-type design rules), **but real filings violate this**. Evidence
from the captured JNJ 10-K fixture (`artifacts/tier2-baseline/2026-05-19/JNJ/.../05-fetch-sec.raw.json`):

| Concept | JNJ value | Sign issue |
|---|---|---|
| `us-gaap:RestructuringCharges` | **+745,000,000** | clean positive expense ✓ |
| `us-gaap:InterestCostsCapitalized` | **+147,000,000** | clean positive ✓ |
| `us-gaap:LitigationSettlementExpense` | **−379,000,000** | **NEGATIVE** — filer signed a real expense as a credit-to-income presentation |
| `us-gaap:GainLossRelatedToLitigationSettlement` | **+100,000,000** | **net GAIN** — opposite economic meaning (income-increasing) |

**Design rule (sign normalization):** for the **expense/charge/cost** concepts (restructuring,
litigation-settlement-*expense*, loss-contingency-loss, capitalized-interest), store the value as
`absolute magnitude of a genuine charge`. Concretely:

- Extract via `findValue`.
- If the extracted value is **negative**, the filer is presenting the charge as a credit
  (JNJ-style). The economically-correct add-back is the magnitude → take `math.Abs(value)`.
  - Rationale: these concepts are all debit-balance *charge* elements. A negative tagged value
    represents the same dollar charge with an inverted presentation sign, **not** a reversal of a
    different line. Taking `Abs` yields the add-back the adjuster intends. The alternative — leaving
    it negative — makes C3/C6 silently skip a real, material charge (the exact bug TDB-1 fixes).
  - A truly-negative *reversal* (rare; net credit reducing prior-period expense) would be over-added
    by `Abs`. We accept this as the conservative choice because: (a) it is rare, (b) the materiality
    threshold in C1/C3 (2% / 1% of revenue) gates tiny reversals out, and (c) the failure mode of the
    alternative (skip a real charge) is strictly worse for valuation correctness. **OPEN QUESTION Q1**
    flags this for your sign-back decision; the spec's default is `Abs`.

**`GainLossRelatedToLitigationSettlement` is EXCLUDED from the litigation mapping.** It is a
credit-balance *net gain/loss* element with inverted semantics: a positive value is a gain (the
opposite of a settlement charge). Mapping it into `LitigationSettlements` — which C3 adds *back* as if
removing a charge — would inflate normalized operating income on every filer reporting a litigation
gain (JNJ +100M would be spuriously added back). Litigation *gains* are conceptually a C2-style
*subtraction* item, not a C3 add-back. Including this tag would corrupt valuations; it is deliberately
omitted. (See OPEN QUESTION Q2.)

### 3.2 Mapping table

`findValue` priority is first-hit; list the most specific / most common income-statement period-flow
concept first. All concepts below are **duration / debit-balance** (capitalized-interest is debit via
the asset). Confidence is HIGH unless noted.

| Field | us-gaap candidates (priority order) | IFRS-full candidates | Sign handling | Confidence | Verification |
|---|---|---|---|---|---|
| `RestructuringCharges` | `RestructuringCharges`, `RestructuringCosts`, `RestructuringAndRelatedCostIncurredCost` | *(none mapped — see Q3)* | `Abs(value)` | HIGH | `RestructuringCharges` present in MSFT/JNJ/KO/F/AMD/MXL/EQIX fixtures; +745M for JNJ |
| `LitigationSettlements` | `LitigationSettlementExpense`, `LossContingencyLossInPeriod` | *(none mapped — see Q3)* | `Abs(value)` | MEDIUM-HIGH | `LitigationSettlementExpense` present in JNJ/KO/BABA (JNJ −379M → Abs 379M); `LossContingencyLossInPeriod` present in AMD. `GainLossRelatedToLitigationSettlement` **deliberately excluded** (Q2) |
| `CapitalizedInterest` | `InterestCostsCapitalized`, `InterestCostsIncurredCapitalized`, `InterestPaidCapitalized` | `BorrowingCostsCapitalised` (British spelling — IAS 23) | `Abs(value)` | HIGH (us-gaap), MEDIUM (IFRS spelling unverified against a live filer) | `InterestCostsCapitalized` in JNJ/EQIX; all three variants in EQIX. IFRS `BorrowingCostsCapitalised` from IAS-23 taxonomy convention — see Q4 |

Notes on candidate ordering:
- **Restructuring:** `RestructuringCharges` is the canonical single-line income-statement total and is
  the most widely reported (7/10 basket tickers). `RestructuringCosts` and
  `RestructuringAndRelatedCostIncurredCost` are first-hit fallbacks for filers using those labels.
  Because `findValue` is first-hit, a filer reporting BOTH only contributes the first match (no
  double-count). This is correct: these are alternative presentations of the same period total, not
  disjoint components — do **not** use `sumValues` here.
- **Litigation:** `LitigationSettlementExpense` first (the direct expense line),
  `LossContingencyLossInPeriod` as the broader ASC 450 fallback (AMD's tag). First-hit avoids
  double-counting a filer that reports both.
- **Capitalized interest:** the three us-gaap variants are alternative presentations (period-amount
  vs incurred vs cash-paid). First-hit ordering puts the most common (`InterestCostsCapitalized`)
  first. Do **not** sum — they overlap economically.

### 3.3 Why not `sumValues`?

`sumValues` is reserved for **disjoint balance-sheet components** that no umbrella aggregates (the TSM
debt case, `parser.go:742-753`). The restructuring / litigation / capitalized-interest candidate lists
are **alternative presentations of the same period total**, not disjoint slices — summing them would
double-count. `findValue` (first-hit) is the correct idiom, matching how every other income-statement
field is extracted.

---

## 4. Insertion point & idiom

Insert three `findValue` blocks in `parsePeriodData`, in the **income-statement section** (after the
existing `InterestExpense` / `NetIncome` blocks, ~`parser.go:501-531`, before the cash-flow section at
~line 533). This keeps related income-statement extraction grouped and runs before `computePlugs`
(line 854) — though plug computation is unaffected by these fields (they are not balance-sheet
components). The sign normalization is applied inline.

Idiom (illustrative — final code authored in `/execute`):

```go
// C1 add-back source. Restructuring is a debit-balance period charge; filers
// occasionally sign it as a credit (JNJ-style). Store the positive add-back
// magnitude the C1 adjuster expects. See TDB-1 spec §3.1.
if val, exists := p.findValue(data, []string{
    "RestructuringCharges",
    "RestructuringCosts",
    "RestructuringAndRelatedCostIncurredCost",
}); exists {
    financialData.RestructuringCharges = math.Abs(val)
}
```

Same shape for `LitigationSettlements` and `CapitalizedInterest` (IFRS tag appended to the latter).
`math` must be added to the `sec` package imports (the implementer confirms it is not already imported;
if a helper like `absNonNegative` is preferred for testability, define it once in `parser.go`).

Also add the new tags to `GetSupportedConcepts()` (`parser.go:951`) under the appropriate
US-GAAP / IFRS-full sub-headers, for documentation-surface accuracy (precedent: M-1d, Phase B6).

---

## 5. Behavior-change & invariant-risk analysis

### 5.1 Which outputs move, and why that is correct

On any filer reporting these concepts, after the change:
- **C1** stops using the 1.5%-of-revenue guess and uses the real restructuring charge → a different
  (correct) `NormalizedOperatingIncome` when the real charge clears the 2% materiality threshold.
- **C3** fires for the first time on filers with material litigation settlements (≥1% of revenue) →
  litigation charge added back to `NormalizedOperatingIncome`.
- **C6** fires for the first time on filers with capitalized interest → reclassifies it into
  `InterestExpense`.

All three are **intended** corrections — the entire point of TDB-1. Fair values will move on affected
tickers; the movement is toward correctness (charges are normalized out instead of polluting the DCF).
This is a deliberate, documented behavior change, not a regression.

### 5.2 Load-bearing invariant verdict

| Invariant | Source / mechanism | Affected? | Evidence |
|---|---|---|---|
| **DDM bit-for-bit goldens** (`TestDDM_LegacyPath_BitForBit`, JPM/BAC/WFC) | Goldens are **pre-parsed `FinancialData` JSON** loaded via `os.ReadFile`+`json.Unmarshal` (`ddm_bitforbit_test.go:79-92`), NEVER through the parser. JPM input has `restructuring_charges: 0` baked in. | **INERT** | Confirmed: golden input is `{"HistoricalData":{...,"data":{"2026FY":{...,"restructuring_charges":0,...}}}}`. The parser is not on this code path. Banks don't report restructuring/litigation/cap-interest as material lines anyway. |
| **Recompute-shadow snapshots** (`TestDataCleanerRecompute_ShadowMode_TickerBasket` + `internal/integration/testdata/recompute-shadow/*.json`) | Test **re-parses raw company-facts** (`05-fetch-sec.raw.json`) via `parser.ParseFinancialData` (`datacleaner_recompute_shadow_test.go:124`). So the parser change **DOES run** on the 10 basket tickers, several of which report these concepts (MSFT/JNJ/KO/F/AMD/MXL/EQIX/BABA). | **EXPECTED INERT — MUST VERIFY** | The snapshots record ONLY the **four balance-sheet umbrella divergences** (CurrentAssets, TotalAssets, CurrentLiabilities, TotalLiabilities — `recompute.go:32`). The three new fields are **income-statement** fields feeding `NormalizedOperatingIncome` / `InterestExpense`, **none of which is a recompute umbrella and none of which is a balance-sheet component sum**. So the recorded WARN set is mathematically unperturbed → `git diff --quiet recompute-shadow/` should stay clean. **This is a prediction; the implementer MUST run the suite + the git-diff check to confirm before claiming done.** |
| **Plug invariants** (`datacleaner_plug_invariants_test.go`) | Asserts `umbrella == knownComponents + plug` on balance-sheet triples. | **INERT** | The three fields are income-statement, not in any plug triple (`plugs.go:55-108`). |
| **Tier-2 replay baseline** (`artifacts/tier2-baseline/*`) | Replay bundles re-run captured raw input through the engine and diff vs saved output. | **Out of scope for CI gate** | The committed baselines are `calculation_version 4.1`/older — already drift-confounded across DC-1 Phases 2-5 (per CLAUDE.md). Not a CI gate. Replay verification is an **operator follow-up**, not a blocker (mirrors the DC-1 Phase-5 replay-verification follow-up disposition). Flag in the tracker; do not gate on it. |

**Net verdict:** DDM goldens and plug invariants are provably inert. Shadow snapshots are predicted
inert (income-statement fields cannot move balance-sheet-umbrella WARN lines) but the prediction MUST
be empirically confirmed because the parser physically runs on those fixtures. If the snapshot diff is
NOT clean, that is a signal the prediction was wrong (e.g., an unexpected coupling) and the change must
be re-examined before proceeding — do not blindly regenerate the snapshots.

---

## 6. Test strategy

1. **Primary: `sec` parser unit test** (RED→GREEN). Crafted `ports.SECCompanyFacts` literal (Apple-
   style, per the existing `TestParser_ParseFinancialData_Success` idiom at `parser_test.go:25`)
   carrying:
   - a positive `RestructuringCharges` fact,
   - a **negative** `LitigationSettlementExpense` fact (the JNJ-style trap — asserts `Abs` normalization),
   - a positive `InterestCostsCapitalized` fact,
   - plus the minimum Revenue/OperatingIncome to pass the parser's `insufficient data` guard.
   Assert all three populate on `historical.Data["<period>"]` with the **positive** expected magnitudes.
   This is the load-bearing regression pin (tracker acceptance row "Regression test pins population").

2. **Sign-trap sub-case:** an explicit sub-test (or table row) feeding a negative litigation value and
   asserting the stored value is positive — pins the §3.1 sign decision so a future refactor can't
   silently drop `Abs`.

3. **Fallback-ordering sub-case:** a table row where only a fallback tag is present
   (`RestructuringCosts` but not `RestructuringCharges`) to pin first-hit fallback behavior.

4. **Exclusion sub-case (regression guard for Q2):** a fixture carrying ONLY
   `GainLossRelatedToLitigationSettlement` (no `LitigationSettlementExpense`) and asserting
   `LitigationSettlements == 0` — pins that the litigation-gain tag is NOT mapped.

5. **Adjuster-firing confirmation (low-risk, optional but recommended):** a focused test (in
   `adjustments` or `integration`) that builds a `FinancialData` with a populated
   `RestructuringCharges` clearing the 2% threshold and asserts C1 fires WITHOUT the 1.5% fallback
   (i.e., the fired `DeltaAmount` equals the real charge, not `Revenue*0.015`); similarly that C3/C6
   fire when their fields are positive. This satisfies tracker TDB-1.3 as a test, not a manual step.
   If the existing adjuster tests already cover "field populated → fires with real value," reuse them
   and skip net-new tests.

6. **Full-suite + invariant gates** (see plan §validation): `GOWORK=off go build ./... && go vet ./...
   && go test ./... -count=1`, the named invariant tests, and
   `git diff --quiet internal/integration/testdata/recompute-shadow/`.

Coverage: this package is finance-adjacent; aim to keep the `sec` package at/above its current
coverage (project policy: ≥90% critical finance, ≥80% overall per `CLAUDE.md`). No new threshold invented.

---

## 7. Risks

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Wrong/extra XBRL tag silently corrupts valuations | Low (tags verified against real fixtures) | High | Tags cross-checked against MSFT/JNJ/KO/F/AMD/MXL/EQIX/BABA captured filings (§3); `GainLossRelatedToLitigationSettlement` deliberately excluded; sign normalized to positive add-back |
| `Abs` over-adds a genuine reversal (rare net-credit) | Low | Low-Medium | Materiality thresholds (2%/1%) gate tiny reversals; Q1 surfaces the decision; conservative choice documented |
| Shadow snapshot drift (prediction wrong) | Low | Medium (blocks merge) | Mandatory empirical `git diff --quiet recompute-shadow/` gate; if dirty, STOP and re-examine, do not regenerate |
| IFRS `BorrowingCostsCapitalised` spelling/availability unverified | Medium | Low (us-gaap path unaffected; IFRS filers rarely capitalize material interest) | Q4 flags for verification; us-gaap coverage is the load-bearing path |

---

## 8. Acceptance criteria

- [ ] `parsePeriodData` populates `RestructuringCharges`, `LitigationSettlements`, `CapitalizedInterest`
      from the §3.2 candidate lists, in reporting currency, normalized to a positive add-back magnitude.
- [ ] A negative `LitigationSettlementExpense` fact yields a **positive** `LitigationSettlements`.
- [ ] A fixture carrying only `GainLossRelatedToLitigationSettlement` yields `LitigationSettlements == 0`.
- [ ] New tags added to `GetSupportedConcepts()` for documentation accuracy.
- [ ] `sec` parser unit test pins all of the above (primary + sign-trap + fallback + exclusion sub-cases).
- [ ] C1 stops using the 1.5%-revenue fallback when a real value is present; C3/C6 fire when their
      fields are positive (test- or replay-confirmed).
- [ ] `GOWORK=off go build ./... && go vet ./... && go test ./... -count=1` all green.
- [ ] `TestDDM_LegacyPath_BitForBit` green (inert).
- [ ] `git diff --quiet internal/integration/testdata/recompute-shadow/` exits 0 (snapshots byte-identical).
- [ ] `internal/services/valuation/currency.go` UNCHANGED (FX already covers these fields).
- [ ] Tracker updated to link this spec + the plan; Status stays OPEN until `/execute` lands.

---

## 9. Open questions (need a decision before / during execute)

- **Q1 (sign policy):** Confirm `math.Abs` for the expense/charge concepts (restructuring, litigation
  expense, loss-contingency, capitalized interest). Default = `Abs`. Alternative = "clamp negatives to
  0 (skip)", which is *less* aggressive but skips JNJ-style negatively-signed real charges (re-creating
  part of the bug). **Recommendation: `Abs`.**
- **Q2 (litigation gain):** Confirm `GainLossRelatedToLitigationSettlement` stays EXCLUDED from
  `LitigationSettlements`. **Recommendation: exclude** (inverted semantics; mapping it would corrupt C3).
  If you want litigation *gains* normalized too, that is a separate change to C2/`AssetSaleGains`-style
  handling — out of TDB-1 scope.
- **Q3 (IFRS restructuring/litigation):** No IFRS-full restructuring or litigation concept is mapped
  (the basket's IFRS filers — TSM/BABA — report none except BABA's `LitigationSettlementExpense`, which
  is a us-gaap-namespaced tag in its 20-F). Acceptable to ship us-gaap-only for restructuring/litigation?
  **Recommendation: yes** — add IFRS later if a real 20-F filer surfaces the need; don't guess at
  unverified IFRS tag names.
- **Q4 (IFRS capitalized interest):** Include `ifrs-full:BorrowingCostsCapitalised` (British spelling)?
  It is the IAS-23 concept but is not verified against a live captured filing in the basket.
  **Recommendation: include it** (low risk — it only fires for IFRS filers that report it; us-gaap path
  is unaffected), but mark confidence MEDIUM in the code comment and note it needs live verification.
