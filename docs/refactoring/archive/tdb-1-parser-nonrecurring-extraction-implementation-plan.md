# TDB-1 — Implementation plan: SEC parser extraction of non-recurring earnings items

**Spec:** `docs/refactoring/spec/tdb-1-parser-nonrecurring-extraction-spec.md` (read it first).
**Issue:** GitHub `#1` (`[TDB-1]`). Tracker: `docs/reviewer/archive/TDB-1-parser-restructuring-litigation-capex-not-populated.md`.
**Worktree:** `worktree-tdb-1-parser-extraction`. **Validate everything with `GOWORK=off`.**
**Mode:** TDD (RED → GREEN). Single-package change (`internal/infra/gateways/sec`) + tests.

Resolve OPEN QUESTIONS Q1-Q4 (spec §9) before/at the start of execution. Defaults below assume:
Q1 = `Abs`, Q2 = exclude litigation gain, Q3 = us-gaap-only for restructuring/litigation,
Q4 = include `ifrs-full:BorrowingCostsCapitalised` at MEDIUM confidence. If the human overrides any,
adjust the corresponding task before writing code.

---

## Pre-flight

1. Confirm worktree + isolation:
   ```bash
   cd "<repo>/.claude/worktrees/tdb-1-parser-extraction"
   git status                      # clean
   GOWORK=off go build ./...       # baseline green (verified at design time)
   ```
2. Re-read the three insertion-adjacent regions: `parser.go:466-531` (income-statement blocks),
   `parser.go:951-1070` (`GetSupportedConcepts`), `parser_test.go:25-110`
   (`TestParser_ParseFinancialData_Success` idiom).

---

## Task 1 — RED: parser unit test pinning population + sign + fallback + exclusion

**File:** `internal/infra/gateways/sec/parser_test.go` (new test function).

Add `TestParser_ParseFinancialData_NonRecurringEarningsItems` (table-driven where it reads cleanly).
Follow the existing `ports.SECCompanyFacts` literal idiom. Cases:

| Sub-case | Fixture facts (us-gaap, USD, FY) | Assertion |
|---|---|---|
| `restructuring_positive` | `Revenues`+`OperatingIncomeLoss` (guard) + `RestructuringCharges: 745000000` | `data["2023FY"].RestructuringCharges == 745000000` |
| `litigation_negative_abs` | guard + `LitigationSettlementExpense: -379000000` | `LitigationSettlements == 379000000` (positive — pins `Abs`) |
| `capint_positive` | guard + `InterestCostsCapitalized: 147000000` | `CapitalizedInterest == 147000000` |
| `restructuring_fallback` | guard + `RestructuringCosts: 100000000` (no `RestructuringCharges`) | `RestructuringCharges == 100000000` (first-hit fallback) |
| `litigation_gain_excluded` | guard + `GainLossRelatedToLitigationSettlement: 100000000` (no expense tag) | `LitigationSettlements == 0` (Q2 exclusion guard) |
| `capint_ifrs` (only if Q4=include) | IFRS-full filer shape + `BorrowingCostsCapitalised: 50000000` | `CapitalizedInterest == 50000000` |
| `all_absent` | guard only | all three `== 0` (no false population) |

Notes:
- The "guard" facts (`Revenues` > 0 or `OperatingIncomeLoss` > 0) are required or `parsePeriodData`
  returns the `insufficient data` error (`parser.go:844`).
- Use the same `Fy: 2023, Fp: "FY", Form: "10-K"` shape as the existing success test so the period key
  is `"2023FY"`.
- For the IFRS sub-case, mirror the IFRS fixture shape in `parser_test.go` (search for an existing
  `ifrs-full` test fixture, e.g. around the TSM/`extractFiscalPeriods` tests ~line 289-363, to copy the
  taxonomy/unit shape).

Run (expect FAIL — fields are zero today):
```bash
GOWORK=off go test ./internal/infra/gateways/sec/ -run TestParser_ParseFinancialData_NonRecurringEarningsItems -count=1
```

---

## Task 2 — GREEN: populate the three fields in `parsePeriodData`

**File:** `internal/infra/gateways/sec/parser.go`.

1. Add `"math"` to the import block (verify it is not already imported). Optionally define a small
   unexported helper for testability and to centralize the sign comment:
   ```go
   // absAddBack normalizes an XBRL charge fact to the positive add-back magnitude
   // the C1/C3/C6 earnings adjusters expect. Filers occasionally sign debit-balance
   // charges as credits (e.g. JNJ tags LitigationSettlementExpense as -379M). See
   // docs/refactoring/spec/tdb-1-parser-nonrecurring-extraction-spec.md §3.1.
   func absAddBack(v float64) float64 { return math.Abs(v) }
   ```

2. Insert three `findValue` blocks in the income-statement section of `parsePeriodData`, placed AFTER
   the `NetIncome` block (~line 514) and BEFORE the `DividendsPerShare` block (~line 516) — i.e.,
   grouped with the other income-statement extractions, well before `computePlugs` (line 854):

   ```go
   // C1 (restructuring) add-back source. Debit-balance period charge; filers
   // occasionally sign it as a credit, so normalize to the positive add-back
   // magnitude the C1 adjuster expects. Alternative presentations of the same
   // period total → findValue first-hit (NOT sumValues — they overlap).
   // TDB-1 spec §3.2.
   if val, exists := p.findValue(data, []string{
       "RestructuringCharges",
       "RestructuringCosts",
       "RestructuringAndRelatedCostIncurredCost",
   }); exists {
       financialData.RestructuringCharges = absAddBack(val)
   }

   // C3 (litigation) add-back source. LitigationSettlementExpense is the direct
   // expense line; LossContingencyLossInPeriod is the broader ASC-450 fallback.
   // GainLossRelatedToLitigationSettlement is DELIBERATELY EXCLUDED — it is a
   // credit-balance net gain/loss with inverted semantics (a positive value is
   // a GAIN, the opposite of a settlement charge); mapping it would corrupt C3.
   // TDB-1 spec §3.1 / §3.2 / Q2.
   if val, exists := p.findValue(data, []string{
       "LitigationSettlementExpense",
       "LossContingencyLossInPeriod",
   }); exists {
       financialData.LitigationSettlements = absAddBack(val)
   }

   // C6 (capitalized interest) reclassification source. us-gaap variants are
   // alternative presentations (period / incurred / cash-paid) → first-hit.
   // ifrs-full:BorrowingCostsCapitalised (British spelling, IAS 23) appended for
   // IFRS filers — MEDIUM confidence, unverified against a live basket filer
   // (TDB-1 Q4). TDB-1 spec §3.2.
   if val, exists := p.findValue(data, []string{
       "InterestCostsCapitalized",
       "InterestCostsIncurredCapitalized",
       "InterestPaidCapitalized",
       // IFRS-full (IAS 23) — note British spelling.
       "BorrowingCostsCapitalised",
   }); exists {
       financialData.CapitalizedInterest = absAddBack(val)
   }
   ```
   (If Q4 = exclude, drop the `BorrowingCostsCapitalised` line and the `capint_ifrs` test sub-case.)

3. Add the new tags to `GetSupportedConcepts()` (`parser.go:951`) under the matching sub-headers:
   - US-GAAP income-statement group: `"us-gaap:RestructuringCharges"`, `"us-gaap:RestructuringCosts"`,
     `"us-gaap:RestructuringAndRelatedCostIncurredCost"`, `"us-gaap:LitigationSettlementExpense"`,
     `"us-gaap:LossContingencyLossInPeriod"`, `"us-gaap:InterestCostsCapitalized"`,
     `"us-gaap:InterestCostsIncurredCapitalized"`, `"us-gaap:InterestPaidCapitalized"`.
   - IFRS-full group (only if Q4=include): `"ifrs-full:BorrowingCostsCapitalised"`.
   - If `TestParser_GetSupportedConcepts` (`parser_test.go:548`) asserts an exact list/count, update
     that test in lockstep.

Run (expect PASS):
```bash
GOWORK=off go test ./internal/infra/gateways/sec/ -run TestParser_ParseFinancialData_NonRecurringEarningsItems -count=1
```

---

## Task 3 — Confirm C1 drops the 1.5% fallback; C3/C6 fire (TDB-1.3 as a test)

**File:** `internal/services/datacleaner/adjustments/earnings_test.go` (or wherever C1/C3/C6 unit tests
live — grep `ApplyC1Restructuring`/`ApplyC3Litigation`/`ApplyC6CapitalizedInterest` in `*_test.go`).

1. First check whether existing adjuster tests already cover "field populated → fires with the real
   value (not the estimate)". If they do, no net-new test is needed — cite them in the closeout.
2. If not, add focused unit tests:
   - C1: `FinancialData{Revenue: 1e9, RestructuringCharges: 5e7}` (5% > 2% threshold) → fired
     `LedgerEntry.DeltaAmount == 5e7` (NOT `1e9*0.015 == 1.5e7`). Pins the fallback is bypassed.
   - C3: `FinancialData{Revenue: 1e9, LitigationSettlements: 2e7}` (2% > 1%) → fired, `DeltaAmount == 2e7`.
   - C6: `FinancialData{CapitalizedInterest: 1e7}` → fired, `Component == "InterestExpense"`,
     `DeltaAmount == 1e7`, `EquityOffset == 0` (load-bearing C6 invariant).

This is the most valuable test surface — it proves the end-to-end intent (parser populates → adjuster
fires correctly). Keep it pure-unit (construct `FinancialData` directly; no parser needed here).

Run:
```bash
GOWORK=off go test ./internal/services/datacleaner/adjustments/ -count=1
```

---

## Task 4 — Full validation + invariant gates

Run the complete set; ALL must pass before the change is considered done:

```bash
# Build, vet, full suite (isolation mode)
GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1

# Named load-bearing invariants (explicit, even though covered by ./...):
GOWORK=off go test ./internal/services/valuation/models/ -run TestDDM_LegacyPath_BitForBit -count=1
GOWORK=off go test ./internal/integration/ -run TestDataCleanerRecompute_ShadowMode_TickerBasket -count=1
GOWORK=off go test ./internal/integration/ -run TestRecomputeUmbrellas_NoMutation -count=1   # if present in this package
GOWORK=off go test ./internal/integration/ -run TestDataCleaner_PlugInvariants -count=1       # confirm exact name via grep

# THE critical gate — shadow snapshots must be byte-identical (income-statement
# fields must not perturb balance-sheet-umbrella WARN lines). MUST exit 0:
git diff --quiet internal/integration/testdata/recompute-shadow/ ; echo "shadow-diff exit: $?"
```

**If `git diff --quiet recompute-shadow/` exits non-zero: STOP.** The spec §5.2 prediction was wrong.
Do NOT regenerate the snapshots. Investigate the coupling (a populated income-statement field somehow
changed a balance-sheet umbrella via an adjuster), report it, and re-examine the design before
continuing. Regenerating would silently bury a real, unexpected behavior change.

Also confirm `internal/services/valuation/currency.go` is UNCHANGED (`git diff --stat` should not list it).

---

## Task 5 — Docs + tracker + commit

1. **Tracker** (`docs/reviewer/archive/TDB-1-parser-restructuring-litigation-capex-not-populated.md`): tick the
   Acceptance boxes that now hold; flip Status to RESOLVED/CLOSED only after Task 4 is fully green and
   the change is committed. (ARCH already added the "Design" link line — see below.) Note the operator
   replay-verification follow-up (spec §5.2) as the one remaining non-blocking item.
2. **`docs/integration/TODO_TASKS_CATALOG.md`:** mark the "Financial Data Extraction (R1)" residue
   addressed by TDB-1, if that catalog tracks it.
3. **No source docs beyond the above** unless `GetSupportedConcepts` or public behavior surfaced in
   `docs/openapi.yaml` changed (it does not — response shape is unchanged; only internal normalization
   improves). Skip `docs-update` for this internal correctness fix.

Commit template:

```
fix(sec): populate restructuring/litigation/capitalized-interest from XBRL (#1)

parsePeriodData now extracts RestructuringCharges, LitigationSettlements, and
CapitalizedInterest via findValue candidate lists (us-gaap + ifrs-full), in
reporting currency, normalized to the positive add-back magnitude the C1/C3/C6
earnings adjusters expect (math.Abs — filers occasionally sign debit-balance
charges as credits, e.g. JNJ tags LitigationSettlementExpense as -379M).
GainLossRelatedToLitigationSettlement is deliberately excluded (inverted
net-gain/loss semantics would corrupt C3).

Before: C1 fell back to a 1.5%-of-revenue guess; C3/C6 never fired on real
filings → distorted NormalizedOperatingIncome / InterestExpense with no warning.

Invariants verified green: DDM bit-for-bit goldens (inert — pre-parsed JSON),
recompute-shadow snapshots byte-identical (income-statement fields don't touch
balance-sheet umbrellas), plug invariants (inert). currency.go unchanged
(these fields are already in the FX calculation-safety list).

Spec:  docs/refactoring/spec/tdb-1-parser-nonrecurring-extraction-spec.md
Plan:  docs/refactoring/implementations/tdb-1-parser-nonrecurring-extraction-implementation-plan.md
Closes #1

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
```

(Commit/push only when the human asks; this plan does not auto-commit.)

---

## Sequencing summary

1. Task 1 (RED parser test) — fails.
2. Task 2 (populate + sign + doc list) — Task 1 goes GREEN.
3. Task 3 (adjuster-firing tests) — GREEN.
4. Task 4 (full suite + invariant gates + shadow-diff) — all GREEN; shadow-diff exits 0.
5. Task 5 (tracker/catalog/commit).

## What NOT to do

- Do NOT use `sumValues` for these three (alternative presentations, not disjoint components → would
  double-count).
- Do NOT map `GainLossRelatedToLitigationSettlement` into `LitigationSettlements` (Q2).
- Do NOT touch `currency.go`, the C1/C3/C6 adjuster logic, or `computePlugs`.
- Do NOT regenerate `recompute-shadow/*.json` (they must stay byte-identical; a dirty diff is a STOP signal).
- Do NOT populate the other unpopulated C-fields (C2/C4/C5/C7) — out of scope.
- Do NOT invent IFRS restructuring/litigation tags you cannot verify against a real filing (Q3).
