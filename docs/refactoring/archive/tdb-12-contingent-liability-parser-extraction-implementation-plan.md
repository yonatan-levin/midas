# TDB-12 — Implementer Plan: SEC parser extraction of contingent-liability accruals

**Spec:** `docs/refactoring/spec/tdb-12-contingent-liability-parser-extraction-spec.md` (read it first).
**Issue:** GitHub `#12`. **Tracker:** `docs/reviewer/archive/TDB-12-contingent-liability-parser-not-populated.md`.
**Worktree:** `worktree-tdb-12-contingent-parser` — own `go.mod`; **validate with `GOWORK=off`**.
**Method:** strict TDD (RED → GREEN), single production file (`parser.go`) + one new test file + doc/`GetSupportedConcepts` entries. **Do NOT touch `currency.go`, `liabilities.go`, `recompute.go`, `plugs.go`, or any DDM golden.**

---

## 0. Pre-flight (read-only, ~5 min)

```bash
# from the worktree root
GOWORK=off go build ./... && GOWORK=off go test ./internal/infra/gateways/sec/... -count=1
git status   # MUST be clean before starting
```

Confirm the three entity fields exist and their JSON tags (no change needed, just orient):
- `internal/core/entities/financial_data.go:92-94` — `ContingentLiabilities` / `EnvironmentalLiabilities` / `LitigationLiabilities` (all `float64`, all `(B3)`).

Confirm the consumer (no change — read for context):
- `internal/services/datacleaner/adjustments/liabilities.go:1099-1103` — B3 sums the three, gates `<= 0`.
- `internal/services/datacleaner/adjustments/b3_contingent_liabilities_adjuster_test.go:186-188` — already
  pins the **field-populated** path emits a `DebtLikeClaims` overlay. **This is why TDB-12 needs no new
  B3 adjuster test — the population gap is the only gap.**

Confirm FX is already wired (no change):
- `internal/services/valuation/currency.go:253-255` — all three already `*= rate`.

---

## 1. RED — author the failing parser unit test

**New file:** `internal/infra/gateways/sec/parser_contingent_test.go` (kept separate from
`parser_test.go` for review clarity; package `sec`, same imports as the TDB-1 test).

Mirror `TestParser_ParseFinancialData_NonRecurringEarningsItems` (`parser_test.go:129-288`) exactly —
reuse the `usGAAPFact`/`ifrsFact` single-fact FY-2023 helper idiom (USD, 10-K, end `2023-09-30` → period
key `2023FY`) and the `factsByTaxonomy` → `ports.SECCompanyFacts` → `parser.ParseFinancialData` → assert
on `historical.Data["2023FY"]` skeleton.

```go
func TestParser_ParseFinancialData_ContingentLiabilities(t *testing.T) {
    logger := zap.NewNop()
    parser := NewParser(logger)
    usGAAPFact := func(val float64) ports.SECFactGroup { /* identical to TDB-1 test */ }

    tests := []struct {
        name              string
        usGAAP            map[string]ports.SECFactGroup
        wantContingent    float64
        wantEnvironmental float64
        wantLitigation    float64
    }{
        {
            name: "contingent_aggregate",
            usGAAP: map[string]ports.SECFactGroup{
                "Revenues":                              usGAAPFact(383285000000),
                "OperatingIncomeLoss":                   usGAAPFact(114301000000),
                "LossContingencyAccrualAtCarryingValue": usGAAPFact(541000000),
            },
            wantContingent: 541000000,
        },
        {
            name: "contingent_split_fallback", // only the Current split, no aggregate
            usGAAP: map[string]ports.SECFactGroup{
                "Revenues":                                    usGAAPFact(383285000000),
                "OperatingIncomeLoss":                         usGAAPFact(114301000000),
                "LossContingencyAccrualCarryingValueCurrent":  usGAAPFact(364000000),
            },
            wantContingent: 364000000,
        },
        {
            // THE DOUBLE-COUNT GUARD (MSFT-shaped): aggregate + current both present.
            name: "contingent_aggregate_wins_over_split",
            usGAAP: map[string]ports.SECFactGroup{
                "Revenues":                                    usGAAPFact(383285000000),
                "OperatingIncomeLoss":                         usGAAPFact(114301000000),
                "LossContingencyAccrualAtCarryingValue":       usGAAPFact(541000000),
                "LossContingencyAccrualCarryingValueCurrent":  usGAAPFact(364000000),
            },
            wantContingent: 541000000, // aggregate wins; NOT 541M+364M summed.
        },
        {
            name: "environmental_aggregate",
            usGAAP: map[string]ports.SECFactGroup{
                "Revenues":                                 usGAAPFact(383285000000),
                "OperatingIncomeLoss":                      usGAAPFact(114301000000),
                "AccrualForEnvironmentalLossContingencies": usGAAPFact(4800000),
            },
            wantEnvironmental: 4800000,
        },
        {
            name: "litigation_reserve",
            usGAAP: map[string]ports.SECFactGroup{
                "Revenues":                     usGAAPFact(383285000000),
                "OperatingIncomeLoss":          usGAAPFact(114301000000),
                "EstimatedLitigationLiability": usGAAPFact(250000000),
            },
            wantLitigation: 250000000,
        },
        {
            // Cross-rule exclusion: income-statement litigation EXPENSE is C3's
            // (TDB-1), NOT B3's balance-sheet litigation liability.
            name: "excludes_income_statement_litigation",
            usGAAP: map[string]ports.SECFactGroup{
                "Revenues":                    usGAAPFact(383285000000),
                "OperatingIncomeLoss":         usGAAPFact(114301000000),
                "LitigationSettlementExpense": usGAAPFact(379000000),
            },
            wantLitigation: 0,
        },
        {
            // Disclosure exclusion: possible-loss estimate is NOT a recognized accrual.
            name: "excludes_possible_loss_disclosure",
            usGAAP: map[string]ports.SECFactGroup{
                "Revenues":                            usGAAPFact(383285000000),
                "OperatingIncomeLoss":                 usGAAPFact(114301000000),
                "LossContingencyEstimateOfPossibleLoss": usGAAPFact(900000000),
            },
            wantContingent: 0,
        },
        {
            name: "negative_clamps_to_zero",
            usGAAP: map[string]ports.SECFactGroup{
                "Revenues":                              usGAAPFact(383285000000),
                "OperatingIncomeLoss":                   usGAAPFact(114301000000),
                "LossContingencyAccrualAtCarryingValue": usGAAPFact(-100000000),
            },
            wantContingent: 0, // clamp (val > 0), NOT math.Abs.
        },
        {
            name: "all_absent",
            usGAAP: map[string]ports.SECFactGroup{
                "Revenues":            usGAAPFact(383285000000),
                "OperatingIncomeLoss": usGAAPFact(114301000000),
            },
        },
    }
    // for each: build factsByTaxonomy{"us-gaap": tt.usGAAP}, ParseFinancialData,
    // data := historical.Data["2023FY"]; assert.Equal on the three fields.
}
```

Run it RED (the production code does not populate yet):

```bash
GOWORK=off go test ./internal/infra/gateways/sec/ -run TestParser_ParseFinancialData_ContingentLiabilities -count=1
# EXPECT: FAIL — all want-non-zero cases get 0.
```

---

## 2. GREEN — populate the three fields in `parsePeriodData`

**File:** `internal/infra/gateways/sec/parser.go`. **Insertion point:** immediately after the pension
block (after `parser.go:877`, the `PensionPlanAssets` `findValue` close-brace) and before the
share-extraction comment at `parser.go:879`. Exact edit:

```go
	// B3 contingent-liability inputs (TDB-12). Recognized ASC 450 / ASC 410
	// balance-sheet accruals (instant, credit-balance). B3 (ApplyB3Contingent)
	// SUMS all three as the gross exposure, then probability-weights it into a
	// DebtLikeClaims overlay (EV→Equity bridge subtracts it → lower fair value
	// for filers carrying material accruals). The three candidate lists are
	// mutually DISJOINT (general vs environmental vs litigation) and
	// AGGREGATE-FIRST within each — findValue (first-hit), NOT sumValues — so a
	// filer reporting both an aggregate AND its current/noncurrent split is not
	// double-counted (MSFT/MXL report both). Negatives clamp to 0: a negative
	// recognized liability is a data anomaly, not a credit-presentation flip, so
	// math.Abs (TDB-1's idiom for income-statement charges) is deliberately NOT
	// used here. These three fields are parallel B3 inputs — NOT components of
	// any computePlugs triple or recomputeUmbrellas formula (same disposition as
	// the A6 RightOfUse asset above). See
	// docs/refactoring/spec/tdb-12-contingent-liability-parser-extraction-spec.md.

	// General ASC 450 loss-contingency accrual. Aggregate first; current/
	// noncurrent split are fallbacks for filers reporting ONLY the split.
	if val, exists := p.findValue(data, []string{
		"LossContingencyAccrualAtCarryingValue",
		"LossContingencyAccrualCarryingValueCurrent",
		"LossContingencyAccrualCarryingValueNoncurrent",
	}); exists && val > 0 {
		financialData.ContingentLiabilities = val
	}

	// Environmental remediation accrual (ASC 410). Aggregate first, then split.
	if val, exists := p.findValue(data, []string{
		"AccrualForEnvironmentalLossContingencies",
		"AccruedEnvironmentalLossContingenciesNoncurrent",
		"AccruedEnvironmentalLossContingenciesCurrent",
	}); exists && val > 0 {
		financialData.EnvironmentalLiabilities = val
	}

	// Recognized litigation reserve. NOT LitigationSettlementExpense /
	// LossContingencyLossInPeriod — those are income-statement charges already
	// mapped to LitigationSettlements (C3) by TDB-1; reusing them would
	// double-count the same dollars across two rules. MEDIUM confidence — these
	// concepts appear in no basket fixture (large filers tag litigation
	// dimensionally / via custom elements, which the dimension-unaware
	// companyfacts ingestion does not expose). TDB-12 spec §3.4 / Q5.
	if val, exists := p.findValue(data, []string{
		"EstimatedLitigationLiability",
		"LitigationReserve",
	}); exists && val > 0 {
		financialData.LitigationLiabilities = val
	}
```

**Constraints:**
- No new imports (`findValue` is a method; `val > 0` needs no `math`).
- Do NOT call `sumValues` for any of these lists (would double-count aggregate + split).
- Do NOT add any of these to `computePlugs` or `recomputeUmbrellas`.

Run GREEN:

```bash
GOWORK=off go test ./internal/infra/gateways/sec/ -run TestParser_ParseFinancialData_ContingentLiabilities -count=1
# EXPECT: ok.
```

---

## 3. GREEN — extend `GetSupportedConcepts()` (documentation surface)

**File:** `internal/infra/gateways/sec/parser.go`, in `GetSupportedConcepts()` (~`parser.go:1034`).
Add, under the US-GAAP section after the "Pension & Benefits" block (`parser.go:1098-1100`):

```go
		// Balance Sheet - Contingent Liabilities (B3 — TDB-12).
		// Recognized ASC 450 / ASC 410 accruals B3 probability-weights into a
		// DebtLikeClaims overlay. Aggregate-first per field; income-statement
		// litigation-expense and possible-loss DISCLOSURE tags are deliberately
		// NOT listed (they are C3's / disclosure-only — TDB-12 spec §3.2).
		"us-gaap:LossContingencyAccrualAtCarryingValue",
		"us-gaap:LossContingencyAccrualCarryingValueCurrent",
		"us-gaap:LossContingencyAccrualCarryingValueNoncurrent",
		"us-gaap:AccrualForEnvironmentalLossContingencies",
		"us-gaap:AccruedEnvironmentalLossContingenciesNoncurrent",
		"us-gaap:AccruedEnvironmentalLossContingenciesCurrent",
		"us-gaap:EstimatedLitigationLiability",
		"us-gaap:LitigationReserve",
```

(No IFRS-full entry — Q4: no IAS 37 `Provisions` mapping ships. Add a one-line comment noting that if you
want the documentation surface to record the deferral.)

The existing `TestParser_GetSupportedConcepts` (`parser_test.go:726`) asserts the list is non-empty /
well-formed; if it pins a specific count or specific entries, update that assertion to include the new
tags. Run it:

```bash
GOWORK=off go test ./internal/infra/gateways/sec/ -run TestParser_GetSupportedConcepts -count=1
```

---

## 4. Verify invariants (the load-bearing gates)

Run in order; **each must pass before proceeding**.

```bash
# 4a. Full build + vet + suite
GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -count=1

# 4b. DDM bit-for-bit (predicted INERT — pre-parsed goldens, parser not on path)
GOWORK=off go test ./internal/services/valuation/models/ -run TestDDM_LegacyPath_BitForBit -count=1

# 4c. Recompute-shadow basket (parser + B3 RUN here; predicted byte-identical)
GOWORK=off go test ./internal/integration/ -run TestDataCleanerRecompute_ShadowMode_TickerBasket -count=1
git diff --quiet internal/integration/testdata/recompute-shadow/ ; echo "shadow git-diff exit: $?"
#   MUST print 'shadow git-diff exit: 0'. If NON-zero: STOP. Do NOT regenerate.
#   A dirty shadow means the inert prediction was wrong (an unexpected coupling) —
#   re-read spec §6.2 and find why B3's DebtLikeClaims overlay perturbed an umbrella.

# 4d. Plug invariants (predicted INERT — fields not in any plug triple)
GOWORK=off go test ./internal/integration/ -run TestDataCleaner.*[Pp]lug -count=1   # adjust -run to the actual test name

# 4e. Adjustments-projection basket golden — MAY legitimately move (B3 now fires on AMD/MSFT)
GOWORK=off go test ./internal/services/datacleaner/ -run TestAdjustmentsProjection.*Basket -count=1
git diff --stat internal/services/datacleaner/testdata/adjustments_projection_basket_golden.json
#   If unchanged: good (golden likely runs with these fields zeroed). If changed:
#   INSPECT the diff. It is ACCEPTABLE only if the added entries are B3 contingent
#   overlays (Field:"DebtLikeClaims", rule contingent_liabilities) on the tickers
#   that report accruals (AMD environmental; MSFT general). Regenerate per the
#   golden's UPDATE protocol ONLY after confirming the diff is exactly that.
#   Any other change = investigate, do not regenerate.
```

**Decision tree for 4e:** if the basket golden test builds `FinancialData` from **pre-parsed fixtures
with the three fields absent/zero**, it is inert (B3 still skips). If it builds from **re-parsed raw
companyfacts** (like the shadow test), B3 now fires on AMD/MSFT → the golden gains the expected contingent
overlay entries → regenerate-and-review. Determine which by reading the test's fixture-loading path
before touching the golden.

---

## 5. Optional — end-to-end B3-fires integration assertion

Only if the existing `b3_contingent_liabilities_adjuster_test.go` (which already pins the populated path)
plus the §1 parser test feel insufficient to a reviewer. A focused integration test:
parse the AMD `2026-05-19` fixture → run the cleaner **AI-disabled** → assert exactly one
`OverlaySpec{Field:"DebtLikeClaims"}` with `Amount == EnvironmentalLiabilities ×
heuristicProbability(industryCode)`. Keep it deterministic (AI disabled). Skip if the two existing tests
already give the reviewer confidence — do not add redundant coverage.

---

## 6. Final gate + commit

```bash
GOWORK=off go test ./... -count=1   # full green
git diff --quiet internal/integration/testdata/recompute-shadow/ && echo "shadow clean"
git status   # only: parser.go, parser_contingent_test.go, (maybe) parser_test.go, the two docs, tracker
```

**Files expected in the diff:**
- `internal/infra/gateways/sec/parser.go` (3 `findValue` blocks + `GetSupportedConcepts` entries)
- `internal/infra/gateways/sec/parser_contingent_test.go` (new)
- `internal/infra/gateways/sec/parser_test.go` (only if `GetSupportedConcepts` test pins entries/count)
- `docs/refactoring/spec/tdb-12-...md`, `docs/refactoring/implementations/tdb-12-...md`
- `docs/reviewer/archive/TDB-12-...md` (tracker)
- (possibly) `internal/services/datacleaner/testdata/adjustments_projection_basket_golden.json` — only if
  §4e confirmed it is the expected B3-overlay change.
- **`internal/services/valuation/currency.go` MUST NOT appear.**

**Commit template:**

```
feat(sec): populate contingent/environmental/litigation liabilities from XBRL (#12)

parsePeriodData now extracts the three B3 contingent-liability inputs from
recognized ASC 450 / ASC 410 balance-sheet accruals (LossContingencyAccrual*,
AccrualForEnvironmentalLossContingencies, EstimatedLitigationLiability/
LitigationReserve), in reporting currency, via findValue first-hit
(aggregate-first per field, never summed — avoids the MSFT/MXL aggregate+split
double-count). Income-statement litigation expense (C3's) and possible-loss
DISCLOSURE tags are deliberately excluded. B3 (ApplyB3Contingent) now fires on
filers reporting these undimensioned (AMD env, MSFT/EQIX/MXL general),
emitting a DebtLikeClaims overlay that the EV→Equity bridge subtracts — and
the TDB-3 AI-failed→heuristic fallback becomes live with it.

Inert for: DDM bit-for-bit goldens (pre-parsed), recompute-shadow snapshots
(fields are not umbrella terms; B3 emits only a DebtLikeClaims overlay that
never mutates an umbrella in 4.7), plug invariants. currency.go unchanged
(already FX-converts all three). No CalculationVersion bump.

Spec:  docs/refactoring/spec/tdb-12-contingent-liability-parser-extraction-spec.md
Plan:  docs/refactoring/implementations/tdb-12-contingent-liability-parser-extraction-implementation-plan.md
Closes #12 (pending operator replay confirmation on a B3-firing ticker)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
```

---

## 7. Named invariants checklist (paste into the PR/handoff)

- [ ] `GOWORK=off go build ./... && go vet ./... && go test ./... -count=1` — green
- [ ] `TestParser_ParseFinancialData_ContingentLiabilities` — green (incl. double-count guard, two
      exclusion cases, negative-clamp)
- [ ] `TestDDM_LegacyPath_BitForBit` — green (INERT)
- [ ] `git diff --quiet internal/integration/testdata/recompute-shadow/` → exit 0 (byte-identical)
- [ ] plug-invariant test — green (INERT)
- [ ] adjustments-projection basket golden — unchanged, OR reviewed-and-regenerated as expected B3 overlays
- [ ] `internal/services/valuation/currency.go` — UNCHANGED
- [ ] `CalculationVersion` — UNCHANGED (no behavior-version bump; this is a population fix)
- [ ] tracker `docs/reviewer/archive/TDB-12-...md` — links spec + plan, Status OPEN

## 8. Out of scope (do NOT do)

- Dimensional / custom-element contingency extraction (the JNJ/XOM/CVX/PFE/MRK/BA zero case — §3.4).
- IFRS-full IAS 37 `Provisions`-family mapping (Q4).
- Any change to B3 adjuster code, `currency.go`, `recompute.go`, `plugs.go`.
- Regenerating DDM goldens or shadow snapshots to "make tests pass".
- `CalculationVersion` bump.
