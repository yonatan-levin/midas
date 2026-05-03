# IFRS / Foreign Private Issuer Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `GET /api/v1/fair-value/TSM` (and the rest of the foreign-private-issuer universe filing Form 20-F with IFRS taxonomy) produce a real valuation instead of `HTTP 422 INSUFFICIENT_DATA`. Two phases:

- **Phase A (ships first, ~1 day):** Replace the misleading "INSUFFICIENT_DATA" 422 with a distinct `FOREIGN_PRIVATE_ISSUER_UNSUPPORTED` 422 + clear message. Closes the user-confusion bug.
- **Phase B (ships second, ~3-5 days):** Teach the SEC parser to read IFRS tags, convert reporting-currency financials to USD via FRED FX rates, and apply an ADR-to-ordinary share ratio so per-share values match the listed ADR price. After Phase B, TSM (and ASML, NVO, AZN, SAP, BABA, …) produce real valuations.

**Architecture:**

- *Phase A* — A new sentinel `ports.ErrForeignPrivateIssuer` is returned by `sec/parser.go` when the SEC response contains zero `us-gaap` concepts but ≥1 `ifrs-full` concept. The valuation service propagates it as `valuation.ErrForeignPrivateIssuer`. The fair-value handler maps it to a dedicated 422 with code `FOREIGN_PRIVATE_ISSUER_UNSUPPORTED` and a message that says *which* taxonomy was found.
- *Phase B* — Three new pieces, each behind a feature flag (`valuation.ifrs_enabled`, default `true` after the phase ships):
  1. **IFRS-aware parser** — `findValue` lookup tables in `parser.go` extended with IFRS-full equivalents (`Revenue`, `ProfitLossFromOperatingActivities`, `ProfitLoss`, `Borrowings`, …); the unit-iteration switches from "USD only" to "any ISO-4217 currency, with the unit code captured on `FinancialData.ReportingCurrency`".
  2. **FX conversion** — A new `MacroDataGateway.GetFXRate(ctx, fromCcy, toCcy) (float64, error)` method backed by FRED daily series (DEXTAUS for TWD, DEXUSEU for EUR, DEXJPUS for JPY, etc.) with a static-config fallback in `config/fx_rates.json` mirroring the country-risk pattern. Conversion runs at the *service* layer (after parsing, before WACC/DCF) so all downstream calculations see USD numbers.
  3. **ADR ratio** — A new `config/adr_ratios.json` keyed by ticker (`{"TSM": 5, "ASML": 1, "BABA": 8, …}`). The valuation service divides SEC ordinary-share counts by this ratio before computing per-ADR values, so `intrinsic_value_per_share` is comparable to the listed USD ADR price. A runtime cross-check warns if `SEC_shares / YF_shares` deviates >10% from the configured ratio.

**Tech Stack:** Go 1.23, `go.uber.org/zap`, existing `uber/fx` DI container, FRED API (already wired through `MacroDataGateway`), `testify/mock`. No new external dependencies.

**Worktree (recommended):**
```bash
cd "C:/Users/Yonatan Levin/Documents/Programming/Projects/FinTech/Strade/midas"
git worktree add .worktrees/feat-ifrs-fpi -b feat/ifrs-foreign-private-issuer master
cd .worktrees/feat-ifrs-fpi
```
All paths in this plan are relative to the repo root regardless of which directory holds the working copy.

**Spec sections / source bug evidence:**
- `logs/midas.log:22-43` (this conversation's debug session) — TSM returns 336 IFRS concepts, parser only reads US-GAAP, all 10 periods dropped.
- `artifacts/2026-04-26/_no-ticker/req_78653629-7b5c-47bb-b8df-a43c20e95e7e/05-fetch-sec.parsed.json` — captured SEC response used as the IFRS test fixture below.
- `internal/infra/gateways/sec/parser.go:212-444` — current US-GAAP-only `findValue` lookup tables.
- `CLAUDE.md` "Common Gotchas" — already documents that 20-F filers return 422 by design; this plan removes that gotcha.

**Branch / commit policy:**
- One commit per task. Conventional commits (`feat(sec): …`, `fix(parser): …`, `test(valuation): …`).
- Phase A is committed and merged before Phase B starts. They share a feature branch but Phase A could be cherry-picked to a release branch independently.

---

## Phase A — Distinct error code for foreign private issuers

### Task A1: Add `ports.ErrForeignPrivateIssuer` sentinel + classifier in the SEC parser

**Files:**
- Modify: `internal/core/ports/gateways.go` (add sentinel next to `ErrCompanyFactsNotFound`)
- Modify: `internal/infra/gateways/sec/parser.go:66-75` (replace the catch-all `ErrCompanyFactsNotFound` wrap with a taxonomy-aware classifier)
- Test: `internal/infra/gateways/sec/parser_test.go`

**Rationale:** Today every "no usable periods" outcome wraps `ErrCompanyFactsNotFound`. Foreign private issuers and clinical-stage biotechs both look identical from this code path even though their root causes differ. We split the sentinel space so the HTTP layer can give a useful message.

- [ ] **Step A1.1: Define the new sentinel in `ports/gateways.go`** (just below `ErrCompanyFactsNotFound`):

  ```go
  // ErrForeignPrivateIssuer indicates SEC EDGAR returned company facts using
  // a non-US-GAAP taxonomy (typically `ifrs-full` from a Form 20-F filing).
  // The data exists but is not parseable by the current US-GAAP-only mapping
  // tables. Distinguished from ErrCompanyFactsNotFound so the HTTP layer can
  // emit a tailored 422 explaining the taxonomy mismatch instead of the
  // misleading "no data available" message.
  //
  // After Phase B of the IFRS support plan ships, this sentinel will only fire
  // for taxonomies still outside our mapping coverage (e.g. JGAAP, K-IFRS).
  var ErrForeignPrivateIssuer = errors.New("foreign private issuer: non-US-GAAP taxonomy")
  ```

- [ ] **Step A1.2: Replace the `ErrCompanyFactsNotFound` wrap in `parser.go:66-75`** with this taxonomy classifier. Add a helper above `ParseFinancialData`:

  ```go
  // classifyEmptyParseError chooses between ErrForeignPrivateIssuer and
  // ErrCompanyFactsNotFound based on which taxonomies the SEC response carried.
  // Keeps the decision in one place and makes the parser-level intent obvious.
  func classifyEmptyParseError(facts *ports.SECCompanyFacts) error {
      hasUSGAAP := false
      hasIFRS := false
      for taxonomy := range facts.Facts {
          switch taxonomy {
          case "us-gaap":
              hasUSGAAP = true
          case "ifrs-full", "ifrs":
              hasIFRS = true
          }
      }
      if !hasUSGAAP && hasIFRS {
          return fmt.Errorf("%w: SEC filing uses ifrs-full taxonomy (likely Form 20-F)", ports.ErrForeignPrivateIssuer)
      }
      return fmt.Errorf("%w: no periods with usable US-GAAP financials", ports.ErrCompanyFactsNotFound)
  }
  ```

  Then change the `len(historical.Data) == 0` branch:

  ```go
  if len(historical.Data) == 0 {
      return nil, classifyEmptyParseError(facts)
  }
  ```

- [ ] **Step A1.3: Add `TestParser_ClassifyEmpty_ForeignPrivateIssuer`** in `parser_test.go`. Builds a fake `SECCompanyFacts` with only `dei` + `ifrs-full` taxonomies (use the captured TSM artifact as inspiration but inline a minimal fixture). Asserts `errors.Is(err, ports.ErrForeignPrivateIssuer)` and `!errors.Is(err, ports.ErrCompanyFactsNotFound)`.

- [ ] **Step A1.4: Add `TestParser_ClassifyEmpty_GenericNoData`** in `parser_test.go`. Builds a fake `SECCompanyFacts` with `us-gaap` taxonomy present but every period missing Revenue/OperatingIncome (e.g., a clinical-stage biotech). Asserts `errors.Is(err, ports.ErrCompanyFactsNotFound)` and `!errors.Is(err, ports.ErrForeignPrivateIssuer)`.

- [ ] **Step A1.5: Run the parser tests**:
  ```bash
  go test -run TestParser ./internal/infra/gateways/sec/...
  ```
  Expected: PASS, including the two new tests.

- [ ] **Step A1.6: Commit**
  ```bash
  git add internal/core/ports/gateways.go internal/infra/gateways/sec/parser.go internal/infra/gateways/sec/parser_test.go
  git commit -m "feat(sec): distinguish foreign-private-issuer from missing-companyfacts (Phase A1)"
  ```

---

### Task A2: Propagate FPI sentinel through the valuation service

**Files:**
- Modify: `internal/services/valuation/errors.go`
- Modify: `internal/services/valuation/service.go:188-241` (the DataFetcher branch + the `hasCompanyFactsNotFoundError` site)
- Test: `internal/services/valuation/errors_test.go`, `service_test.go`

**Rationale:** Mirror the parser-level split at the service boundary so the HTTP handler stays decoupled from gateway internals.

- [ ] **Step A2.1: Add `ErrForeignPrivateIssuer` sentinel** in `valuation/errors.go`:

  ```go
  // ErrForeignPrivateIssuer indicates SEC EDGAR returned company facts using
  // a non-US-GAAP taxonomy (typically ifrs-full from Form 20-F). The HTTP
  // layer maps this to 422 FOREIGN_PRIVATE_ISSUER_UNSUPPORTED. After Phase B
  // ships, the valuation service will attempt IFRS-aware parsing first and
  // only return this error for taxonomies still outside coverage (JGAAP, K-IFRS).
  var ErrForeignPrivateIssuer = errors.New("foreign private issuer: ifrs-full taxonomy not yet supported")
  ```

- [ ] **Step A2.2: Add a sibling helper** to `hasCompanyFactsNotFoundError` (same file):

  ```go
  // hasForeignPrivateIssuerError returns true when any per-source FetchError in
  // the list wraps ports.ErrForeignPrivateIssuer.
  func hasForeignPrivateIssuerError(errs []entities.FetchError) bool {
      for i := range errs {
          if errors.Is(errs[i].RawErr, ports.ErrForeignPrivateIssuer) {
              return true
          }
      }
      return false
  }
  ```

- [ ] **Step A2.3: Update `service.go:202-204`** (the early-fetch-error branch):

  ```go
  if errors.Is(fetchErr, ports.ErrForeignPrivateIssuer) {
      return nil, fmt.Errorf("%w: SEC filing uses ifrs-full taxonomy", ErrForeignPrivateIssuer)
  }
  if errors.Is(fetchErr, ports.ErrCompanyFactsNotFound) {
      return nil, fmt.Errorf("%w: no US-GAAP XBRL facts currently available for %s via SEC EDGAR", ErrInsufficientData, ticker)
  }
  ```

- [ ] **Step A2.4: Update `service.go:237-241`** (the no-data-from-fetcher branch):

  ```go
  if hasForeignPrivateIssuerError(fetchResult.Errors) {
      return nil, fmt.Errorf("%w: SEC filing uses ifrs-full taxonomy", ErrForeignPrivateIssuer)
  }
  if hasCompanyFactsNotFoundError(fetchResult.Errors) {
      return nil, fmt.Errorf("%w: no US-GAAP XBRL facts currently available for %s via SEC EDGAR", ErrInsufficientData, ticker)
  }
  return nil, fmt.Errorf("%w: DataFetcher returned no financial data for %s", ErrTickerNotFound, ticker)
  ```

- [ ] **Step A2.5: Add `TestService_CalculateValuation_ForeignPrivateIssuer`** in `service_test.go`. Builds a mock `DataFetcher` whose `Fetch` returns `&entities.FetchResult{Errors: []entities.FetchError{{Source: "sec", RawErr: fmt.Errorf("wrapped: %w", ports.ErrForeignPrivateIssuer)}}}`. Asserts `errors.Is(err, valuation.ErrForeignPrivateIssuer)`.

- [ ] **Step A2.6: Run valuation tests**:
  ```bash
  go test -run "TestService_CalculateValuation|TestHas" ./internal/services/valuation/...
  ```
  Expected: PASS, including the new test; existing `TestService_CalculateValuation_InsufficientData_FPI`-style tests (if any) continue to pass and now exercise the *new* error path.

- [ ] **Step A2.7: Commit**
  ```bash
  git add internal/services/valuation/errors.go internal/services/valuation/service.go internal/services/valuation/errors_test.go internal/services/valuation/service_test.go
  git commit -m "feat(valuation): propagate ErrForeignPrivateIssuer through service layer (Phase A2)"
  ```

---

### Task A3: Map FPI sentinel to a dedicated 422 in the HTTP handler

**Files:**
- Modify: `internal/api/v1/handlers/fair_value.go:301-330` (the error classification chain)
- Modify: `internal/api/v1/handlers/fair_value.go:514-540` (`classifyBulkError` for the bulk endpoint)
- Test: `internal/api/v1/handlers/fair_value_test.go`
- Modify: `docs/openapi.yaml` (add the new error code to the documented enum)

**Rationale:** The handler is the only place that touches the user-facing response. Map the new sentinel BEFORE the generic `ErrInsufficientData` branch so the more-specific message wins.

- [ ] **Step A3.1: Insert the new branch before the existing `ErrInsufficientData` branch** at `fair_value.go:307`:

  ```go
  if errors.Is(err, valuation.ErrForeignPrivateIssuer) {
      h.sendError(c, http.StatusUnprocessableEntity, "FOREIGN_PRIVATE_ISSUER_UNSUPPORTED",
          "Foreign private issuer not yet supported",
          "This ticker files with the SEC under Form 20-F using IFRS taxonomy, which Midas cannot currently parse. Support is on the roadmap.",
          map[string]interface{}{"ticker": ticker, "filing_type": "20-F", "taxonomy": "ifrs-full"})
      return
  }
  ```

- [ ] **Step A3.2: Add the same branch in `classifyBulkError`** at `fair_value.go:516`:

  ```go
  case errors.Is(err, valuation.ErrForeignPrivateIssuer):
      return BulkFailure{
          Ticker:    ticker,
          ErrorCode: "FOREIGN_PRIVATE_ISSUER_UNSUPPORTED",
          Message:   "Foreign private issuer (Form 20-F / IFRS) not yet supported",
      }
  ```

- [ ] **Step A3.3: Update OpenAPI** in `docs/openapi.yaml` — the `code` enum on the Problem-Details schema for `/api/v1/fair-value/{ticker}` 422 responses must include `FOREIGN_PRIVATE_ISSUER_UNSUPPORTED`. (Search for the existing `INSUFFICIENT_DATA` enum entry and add the new value alphabetically.)

- [ ] **Step A3.4: Add `TestFairValueHandler_ForeignPrivateIssuer_Returns422`** in `fair_value_test.go`. Mocks the valuation service to return `valuation.ErrForeignPrivateIssuer`; asserts the response body has `status=422`, `code="FOREIGN_PRIVATE_ISSUER_UNSUPPORTED"`, `context.taxonomy="ifrs-full"`, and that `INSUFFICIENT_DATA` is NOT the code (regression guard against the old error winning).

- [ ] **Step A3.5: Run handler tests**:
  ```bash
  go test -run TestFairValueHandler ./internal/api/v1/handlers/...
  ```
  Expected: PASS.

- [ ] **Step A3.6: Manual reproduction** — start the server and hit TSM:
  ```bash
  go run cmd/server/main.go &
  curl -s -H "X-API-Key: <demo-key>" http://localhost:8080/api/v1/fair-value/TSM | jq .
  ```
  Expected JSON includes `"code":"FOREIGN_PRIVATE_ISSUER_UNSUPPORTED"` and `"taxonomy":"ifrs-full"`. Compare against the original log artifact at `artifacts/2026-04-26/_no-ticker/req_78653629-…` to confirm the *new* artifact bundle records the new phase outcome.

- [ ] **Step A3.7: Commit**
  ```bash
  git add internal/api/v1/handlers/fair_value.go internal/api/v1/handlers/fair_value_test.go docs/openapi.yaml
  git commit -m "feat(api): return FOREIGN_PRIVATE_ISSUER_UNSUPPORTED 422 for 20-F filers (Phase A3)"
  ```

---

### Task A4: Update CLAUDE.md gotcha + ship Phase A

**Files:**
- Modify: `CLAUDE.md` (the "Common Gotchas" entry about 20-F filers)
- Modify: `docs/THESIS.md` if it references coverage scope (only if the file mentions ticker coverage explicitly)

**Rationale:** Docs need to reflect that 20-F filers no longer get a misleading 422 — they now get a clear, distinct one. Phase B will overwrite this gotcha entirely.

- [ ] **Step A4.1: Update the CLAUDE.md gotcha** ("Tickers whose SEC response has no usable US-GAAP XBRL …") to read:

  > Tickers whose SEC response has no usable US-GAAP XBRL split into two distinct error codes: foreign private issuers filing 20-F with `ifrs-full` taxonomy return `HTTP 422 FOREIGN_PRIVATE_ISSUER_UNSUPPORTED` (handled by `ports.ErrForeignPrivateIssuer` → `valuation.ErrForeignPrivateIssuer`); clinical-stage biotechs and pre-revenue companies whose `us-gaap` taxonomy is present but missing Revenue/OperatingIncome return `HTTP 422 INSUFFICIENT_DATA` (handled by `ports.ErrCompanyFactsNotFound` → `valuation.ErrInsufficientData`). The classification happens in `sec/parser.go:classifyEmptyParseError`.

- [ ] **Step A4.2: Run the full test suite**:
  ```bash
  go test ./...
  ```
  Expected: PASS.

- [ ] **Step A4.3: Lint check**:
  ```bash
  ./scripts/lint-logs.sh
  go vet ./...
  ```
  Expected: clean.

- [ ] **Step A4.4: Commit + tag the phase milestone**
  ```bash
  git add CLAUDE.md
  git commit -m "docs: update FPI gotcha for new error code (Phase A complete)"
  git tag phase-a-fpi-error-code
  ```

> 🚦 **Phase A is shippable here.** Open a PR for Phase A only if you want it in production before Phase B is ready. The remaining tasks (B5–B12) build on top of Phase A but do not block its release.

---

## Phase B — IFRS parsing + currency conversion + ADR ratio

### Task B5: Capture reporting currency on the financial entity + parser scaffolding

**Files:**
- Modify: `internal/core/entities/financial_data.go` (add `ReportingCurrency string` field)
- Modify: `internal/infra/gateways/sec/parser.go:139-167` (`extractFiscalPeriods` — currency-aware)
- Test: `internal/infra/gateways/sec/parser_test.go`

**Rationale:** Today `extractFiscalPeriods` only iterates `factGroup.Units["USD"]` and `factGroup.Units["shares"]`. IFRS filers report in their presentation currency (TWD, EUR, JPY, GBp, …), so we need to (a) iterate any currency unit, (b) record which currency we picked per period, and (c) carry it through to `FinancialData` so the service layer can FX-convert later.

- [ ] **Step B5.1: Add `ReportingCurrency` field** to `FinancialData` (ISO-4217 code, e.g., `"USD"`, `"TWD"`, or empty for legacy entries):

  ```go
  // ReportingCurrency is the ISO-4217 code of the currency that financial
  // statement values (Revenue, Assets, OperatingIncome, …) are denominated
  // in, as taken from the SEC XBRL `Units` key. Empty for legacy data
  // persisted before IFRS support shipped; treated as USD by callers in that
  // case. SharesOutstanding is dimensionless so it is unaffected.
  ReportingCurrency string `json:"reporting_currency,omitempty" db:"reporting_currency"`
  ```

  If the entity has a `Validate()` method, accept empty + any 3-letter ISO code without further check (FRED is the source of truth for valid codes).

- [ ] **Step B5.2: Rewrite `extractFiscalPeriods`** to iterate all currency units, recording the currency on the period payload. The new core loop:

  ```go
  // ISO-4217 codes are 3 uppercase letters. "shares" / "pure" / "decimal"
  // are dimensionless and handled separately. Anything else is treated as
  // a financial unit.
  func isCurrencyUnit(unit string) bool {
      if len(unit) != 3 {
          return false
      }
      for _, r := range unit {
          if r < 'A' || r > 'Z' {
              return false
          }
      }
      return true
  }
  ```

  Inside `extractFiscalPeriods`:
  ```go
  for unit, facts := range factGroup.Units {
      switch {
      case unit == "shares":
          p.processFacts(periods, conceptName, facts)
      case isCurrencyUnit(unit):
          p.processFactsWithCurrency(periods, conceptName, facts, unit)
      }
  }
  ```

  Add `processFactsWithCurrency` mirroring `processFacts` but stamping `periods[periodKey]["_currency"] = currencyEncode(unit)` (encoded numerically — see B5.3 — so it fits the existing `map[string]float64` shape).

- [ ] **Step B5.3: Add a small currency code↔float8 codec** in a new private helper inside `parser.go` (do NOT introduce a real string-valued sidecar map yet — keeps the existing structure): pack the 3-letter code into a float64 by treating `'A'..'Z'` as 0..25, then `c0*676 + c1*26 + c2` and storing as float. Decode in reverse in `parsePeriodData`. Comment it as a workaround pending the broader entity refactor.

  > Alternative (slightly cleaner): introduce `periods map[string]*periodData` where `*periodData` holds both `values map[string]float64` and `currency string`. Acceptable; choose this if the codec feels too clever — but mirror the change throughout `parsePeriodData`.

- [ ] **Step B5.4: In `parsePeriodData`** (around line 198) decode the currency and stamp `financialData.ReportingCurrency`. If a period has values in multiple currencies (rare — usually a corporate-action edge case), pick the unit with the most facts and log a `parser.go` warning.

- [ ] **Step B5.5: Add `TestParser_ExtractFiscalPeriods_TWD_Currency`** using a trimmed TSM fixture (drop into `internal/infra/gateways/sec/testdata/tsm_ifrs_minimal.json` — copy 2 periods of `ifrs-full.Revenue` + `ifrs-full.Assets` + `dei.EntityCommonStockSharesOutstanding` from the captured artifact). Asserts the parsed `FinancialData.ReportingCurrency == "TWD"`.

- [ ] **Step B5.6: Run parser tests**:
  ```bash
  go test -run "TestParser" ./internal/infra/gateways/sec/...
  ```

- [ ] **Step B5.7: Commit**
  ```bash
  git add internal/core/entities/financial_data.go internal/infra/gateways/sec/parser.go internal/infra/gateways/sec/parser_test.go internal/infra/gateways/sec/testdata/tsm_ifrs_minimal.json
  git commit -m "feat(parser): capture XBRL reporting currency on FinancialData (Phase B5)"
  ```

---

### Task B6: Extend `findValue` lookup tables with IFRS-full equivalents

**Files:**
- Modify: `internal/infra/gateways/sec/parser.go:212-419` (every `findValue` call)
- Modify: `internal/infra/gateways/sec/parser.go:500-565` (`GetSupportedConcepts`)
- Test: `internal/infra/gateways/sec/parser_test.go`

**Rationale:** With currency now captured, the parser still drops every IFRS period because the lookup tables don't know IFRS tag names. This task adds them. **Important:** US-GAAP names are kept FIRST in each list so domestic filers retain identical priority order.

- [ ] **Step B6.1: Extend Revenue lookup** (parser.go:222-230):
  ```go
  if val, exists := p.findValue(data, []string{
      "Revenues",
      "RevenueFromContractWithCustomerExcludingAssessedTax",
      "SalesRevenueNet",
      // IFRS-full
      "Revenue",
      "RevenueFromContractsWithCustomers",
  }); exists {
      financialData.Revenue = val
  }
  ```

- [ ] **Step B6.2: Extend OperatingIncome lookup** (parser.go:212-220):
  ```go
  if val, exists := p.findValue(data, []string{
      "OperatingIncomeLoss",
      "IncomeLossFromContinuingOperationsBeforeIncomeTaxesExtraordinaryItemsNoncontrollingInterest",
      "IncomeLossFromContinuingOperationsBeforeIncomeTaxes",
      // IFRS-full
      "ProfitLossFromOperatingActivities",
      "ProfitBeforeTax",
  }); exists {
      financialData.OperatingIncome = val
  }
  ```

- [ ] **Step B6.3: Extend NetIncome, Assets, Cash, Debt, StockholdersEquity, MinorityInterest, OperatingCashFlow, CapEx, D&A, Inventory, OperatingLeaseLiability, ProjectedBenefitObligation, SharesOutstanding, DilutedShares** with IFRS equivalents. Reference table to use (verified against the captured `05-fetch-sec.parsed.json` for TSM, `ifrs-full.json` taxonomy):

  | Field | Add IFRS tags |
  |---|---|
  | NetIncome | `ProfitLoss`, `ProfitLossAttributableToOwnersOfParent` |
  | Assets | `Assets` (already shared in IFRS) |
  | CashAndCashEquivalents | `CashAndCashEquivalents`, `Cash` |
  | TotalDebt | `Borrowings`, `NoncurrentBorrowings`, `CurrentBorrowings`, `LeaseLiabilities` |
  | StockholdersEquity | `Equity`, `EquityAttributableToOwnersOfParent` |
  | MinorityInterest | `NoncontrollingInterests` |
  | InterestExpense | `FinanceCosts`, `InterestExpense` |
  | OperatingCashFlow | `CashFlowsFromUsedInOperatingActivities` |
  | CapitalExpenditures | `PurchaseOfPropertyPlantAndEquipmentClassifiedAsInvestingActivities` |
  | DepreciationAndAmortization | `DepreciationAndAmortisationExpense`, `DepreciationAmortisationAndImpairmentLossReversalOfImpairmentLossRecognisedInProfitOrLoss` |
  | Inventory | `Inventories` |
  | OperatingLeaseLiability | `LeaseLiabilities` |
  | ProjectedBenefitObligation | `DefinedBenefitObligationAtPresentValue` |
  | DividendsPerShare | `DividendsPaidOrdinaryShares`, `DividendsRecognisedAsDistributionsToOwnersOfParent` (note: these are total-paid, not per-share — only useful as fallback) |
  | CurrentAssets | `CurrentAssets` |
  | CurrentLiabilities | `CurrentLiabilities` |
  | Goodwill | `Goodwill` |
  | OtherIntangibles | `IntangibleAssetsOtherThanGoodwill` |
  | DeferredTaxAssets | `DeferredTaxAssets` |

  *Skip* fields with no clean IFRS equivalent (e.g., `GainLossOnSaleOfProperties` — IFRS REITs are rare in our universe; defer until a 20-F REIT shows up).

  **Phase B post-launch follow-up (2026-04-29):** the Phase B6 mapping table above
  was incomplete for TSM-style filers that publish component-level concepts
  instead of the IFRS-full umbrella tags. Two additional mappings shipped after
  the live TSM verification (logs/midas.log 2026-04-29 16:47:46) revealed
  `avg_da:0` and a single-component debt extraction:

  | Field | Additional IFRS tags (Phase B follow-up) | Lookup mode |
  |---|---|---|
  | DepreciationAndAmortization | `DepreciationExpense` | findValue (first-hit fallback after the Phase B6 umbrella tags) |
  | TotalDebt | `LongtermBorrowings`, `ShorttermBorrowings`, `CurrentPortionOfLongtermBorrowings`, `NoncurrentPortionOfNoncurrentBondsIssued`, `CurrentBondsIssuedAndCurrentPortionOfNoncurrentBondsIssued` | sumValues (component sum, fires only when umbrella `Borrowings` family is absent) |

  The `sumValues` helper was added to `parser.go` for this case — TSM splits debt
  across five disjoint balance-sheet slices with no umbrella concept. The
  precedence rule is: path-1 (`findValue` over `Borrowings` family) wins when the
  umbrella is present; path-2 (`sumValues` over components) fires only when path-1
  returned nothing. This avoids double-counting for filers who publish both.

  Hard exclusion preserved: `LeaseLiabilities` and `FinanceLeaseLiability*` are
  still NOT in either TotalDebt path. They map ONLY to `OperatingLeaseLiability`
  (Phase B post-launch hotfix `2d15e9e`).

  Cross-checked against `docs/columns name.txt`: also added
  `us-gaap:DepreciationAmortizationAndAccretionNet` (D&A umbrella for
  energy/utility filers), `us-gaap:DebtInstrumentCarryingAmount`, and
  `us-gaap:OtherShortTermBorrowings` to the corresponding US-GAAP lookup lists
  for completeness.

- [ ] **Step B6.4: Update `GetSupportedConcepts()`** to include the IFRS tags as `"ifrs-full:Revenue"`, `"ifrs-full:ProfitLossFromOperatingActivities"`, etc. so the documentation surface stays accurate.

- [ ] **Step B6.5: Add `TestParser_ParseFinancialData_TSM_IFRS_HappyPath`** using `testdata/tsm_ifrs_minimal.json`. Asserts:
  - `len(historical.Data) >= 2` (two periods recovered)
  - `financialData["2024FY"].Revenue > 0`
  - `financialData["2024FY"].OperatingIncome > 0`
  - `financialData["2024FY"].ReportingCurrency == "TWD"`
  - `errors.Is(err, ports.ErrForeignPrivateIssuer)` is FALSE (we now succeed on this path).

- [ ] **Step B6.6: Run all parser tests**:
  ```bash
  go test -cover -run TestParser ./internal/infra/gateways/sec/...
  ```
  Expected: PASS, coverage ≥ existing baseline (parser.go currently >85%).

- [ ] **Step B6.7: Commit**
  ```bash
  git add internal/infra/gateways/sec/parser.go internal/infra/gateways/sec/parser_test.go
  git commit -m "feat(parser): map IFRS-full concepts to FinancialData fields (Phase B6)"
  ```

---

### Task B7: Add `MacroDataGateway.GetFXRate` with FRED + static-config fallback

**Files:**
- Modify: `internal/core/ports/gateways.go` (extend `MacroDataGateway` interface)
- Modify: `internal/infra/gateways/macro/gateway.go` (implement)
- New: `config/fx_rates.json` (static fallback)
- New: `internal/services/valuation/fx_config.go` (loader, mirrors `country_risk.go`)
- Test: `internal/infra/gateways/macro/gateway_test.go`, `internal/services/valuation/fx_config_test.go`

**Rationale:** Per `internal/services/valuation/service.go:142`, the macro gateway is already wired into the valuation service. Adding `GetFXRate` keeps FX in the macro layer (where it conceptually belongs) and reuses the existing FRED API key + circuit breaker.

- [ ] **Step B7.1: Extend the interface**:
  ```go
  type MacroDataGateway interface {
      GetTreasuryRates(ctx context.Context) (*entities.TreasuryRates, error)
      GetMarketRiskPremium(ctx context.Context) (float64, error)
      // GetFXRate returns the spot exchange rate fromCcy→toCcy (units of toCcy
      // per unit of fromCcy). Implementations consult FRED daily series first
      // (e.g., DEXTAUS for TWD→USD) and fall back to config/fx_rates.json
      // when FRED is unavailable. Both arguments are ISO-4217 codes.
      GetFXRate(ctx context.Context, fromCcy, toCcy string) (float64, error)
      HealthCheck(ctx context.Context) error
  }
  ```

- [ ] **Step B7.2: Create `config/fx_rates.json`** with manual rates (sourced from FRED H.10 release, dated in the file):
  ```json
  {
    "as_of": "2026-04-26",
    "source": "FRED H.10 manual snapshot — fallback only",
    "rates_to_usd": {
      "TWD": 0.0312,
      "EUR": 1.0850,
      "JPY": 0.00665,
      "GBP": 1.2710,
      "HKD": 0.1280,
      "CNY": 0.1390,
      "KRW": 0.000725,
      "CHF": 1.1320,
      "CAD": 0.7350,
      "AUD": 0.6580,
      "INR": 0.01195,
      "BRL": 0.1980
    }
  }
  ```

  *Add an entry for every currency that appears as a `Units` key in the SEC IFRS filer universe — TWD, EUR, JPY, GBP, HKD, CNY, KRW are the high-priority ones.*

- [ ] **Step B7.3: Implement `GetFXRate` in `macro/gateway.go`**:
  - Cache key: `fmt.Sprintf("fx:%s:%s", fromCcy, toCcy)` with 6-hour TTL (FX moves daily).
  - Try FRED series first via `c.client.GetSeries(ctx, fredSeriesFor(fromCcy, toCcy))` where `fredSeriesFor("TWD","USD")="DEXTAUS"`, etc. Build a small lookup map.
  - On any FRED error (including circuit-breaker-open), fall back to `config/fx_rates.json` `rates_to_usd[fromCcy]` (invert if `toCcy != "USD"`; cross via USD if neither side is USD).
  - Log at INFO when falling back: `gateway.macro.fx.fallback`, fields: `from`, `to`, `rate`, `source="static_config"`.

- [ ] **Step B7.4: Build `LoadFXRates(path string) (*FXRates, error)`** in `internal/services/valuation/fx_config.go`, mirroring `LoadCountryRiskPremiums` style. Inject into the macro gateway via fx DI.

- [ ] **Step B7.5: Add `TestMacroGateway_GetFXRate_FREDSuccess`** and `TestMacroGateway_GetFXRate_FallsBackToConfig` using `httptest.Server` to simulate FRED responses. Assert correct rate returned in both paths and that the fallback log line fires only on fallback.

- [ ] **Step B7.6: Add `TestLoadFXRates_RoundTrip`** that loads `config/fx_rates.json` and asserts `TWD` is present and `0 < rate < 1`.

- [ ] **Step B7.7: Run gateway + config tests**:
  ```bash
  go test -run "TestMacroGateway_GetFXRate|TestLoadFXRates" ./internal/...
  ```

- [ ] **Step B7.8: Commit**
  ```bash
  git add internal/core/ports/gateways.go internal/infra/gateways/macro/gateway.go internal/infra/gateways/macro/gateway_test.go internal/services/valuation/fx_config.go internal/services/valuation/fx_config_test.go config/fx_rates.json
  git commit -m "feat(macro): GetFXRate via FRED with static config fallback (Phase B7)"
  ```

---

### Task B8: Add ADR ratios config + loader

**Files:**
- New: `config/adr_ratios.json`
- New: `internal/services/valuation/adr_ratios.go`
- Test: `internal/services/valuation/adr_ratios_test.go`
- Modify: `internal/services/valuation/service.go` (load on `NewService`, log warning if absent)

**Rationale:** TSM ADR represents 5 ordinary shares; ASML 1:1; BABA 8 (was 1:1, then split-adjusted, double-check the current ratio); NVO 1:1. Without this, per-share intrinsic value comes out 5× too low for TSM.

- [ ] **Step B8.1: Create `config/adr_ratios.json`**:
  ```json
  {
    "as_of": "2026-04-26",
    "source": "Depositary bank prospectuses — verify before adding new entries",
    "ratios": {
      "TSM":  5,
      "ASML": 1,
      "NVO":  1,
      "AZN":  2,
      "SAP":  1,
      "BABA": 8,
      "BIDU": 1,
      "TM":   10,
      "RIO":  1,
      "BHP":  2,
      "NVS":  1,
      "SHEL": 2,
      "BP":   6
    }
  }
  ```
  *Each ratio is the number of ordinary shares per 1 ADR. Verify each entry against the depositary bank's prospectus before adding new tickers — a wrong ratio silently corrupts the valuation.*

- [ ] **Step B8.2: Implement loader** in `internal/services/valuation/adr_ratios.go` — same shape as `country_risk.go`. Expose `LoadADRRatios(path string) (map[string]int, error)` and a default path constant. Empty / missing file is non-fatal (returns empty map with a warning).

- [ ] **Step B8.3: Wire into `NewService`** at `service.go:117` — load alongside `crpMap` and `indMultiples`, store on the Service struct as `adrRatios map[string]int`. Log `Warn` when missing.

- [ ] **Step B8.4: Add `TestLoadADRRatios_HappyPath`** + `TestLoadADRRatios_MissingFile_ReturnsEmpty`.

- [ ] **Step B8.5: Commit**
  ```bash
  git add config/adr_ratios.json internal/services/valuation/adr_ratios.go internal/services/valuation/adr_ratios_test.go internal/services/valuation/service.go
  git commit -m "feat(valuation): load ADR-to-ordinary-share ratios from config (Phase B8)"
  ```

---

### Task B9: Convert reporting-currency financials to USD in the service layer

**Files:**
- New: `internal/services/valuation/currency.go`
- Modify: `internal/services/valuation/service.go` (insert `convertFinancialsToUSD` between data fetch and DCF)
- Test: `internal/services/valuation/currency_test.go`

**Rationale:** With B5/B6 we have IFRS values in TWD on `FinancialData.ReportingCurrency = "TWD"`; with B7 we can fetch USD/TWD; this task ties them together. After this step, every `FinancialData` reaching the DCF/WACC/growth pipeline is in USD.

- [ ] **Step B9.1: Create `currency.go`** with:
  ```go
  // convertFinancialsToUSD multiplies every monetary field on every period by
  // an FX rate derived from the period's ReportingCurrency. Idempotent for
  // USD-denominated data (rate=1 short-circuits). SharesOutstanding is left
  // untouched (dimensionless). On FX lookup failure, returns the original
  // data unchanged plus an error so the caller can decide whether to abort
  // or proceed with stale data.
  func (s *Service) convertFinancialsToUSD(ctx context.Context, hist *entities.HistoricalFinancialData) error
  ```
  Implementation walks `hist.Data`, calls `s.macroGateway.GetFXRate(ctx, fd.ReportingCurrency, "USD")` once per distinct currency (cached locally), multiplies every `float64` monetary field. List of monetary fields to convert (do NOT convert these: `SharesOutstanding`, `DilutedSharesOutstanding`, `TaxRate`, `InventoryTurnover`, `DividendsPerShare` already-per-share):
  - `Revenue`, `OperatingIncome`, `NormalizedOperatingIncome`, `NetIncome`, `InterestExpense`
  - `TotalAssets`, `CurrentAssets`, `TangibleAssets`, `CurrentLiabilities`
  - `CashAndCashEquivalents`, `TotalDebt`, `InterestBearingDebt`
  - `Goodwill`, `OtherIntangibles`, `Inventory`, `DeadInventoryWritedown`
  - `StockholdersEquity`, `MinorityInterest`, `PreferredEquity`
  - `DepreciationAndAmortization`, `CapitalExpenditures`, `OperatingCashFlow`
  - `OperatingLeaseLiability`, `ProjectedBenefitObligation`, `PensionPlanAssets`
  - `DeferredTaxAssets`
  - `GainOnPropertySales`

  Stamp `fd.ReportingCurrency = "USD"` after conversion so downstream re-conversion is a no-op.

- [ ] **Step B9.2: Insert the call** in `service.go` immediately after the `Successfully fetched data via DataFetcher` log line:
  ```go
  if err := s.convertFinancialsToUSD(ctx, historicalData); err != nil {
      s.log(ctx).Warn("FX conversion partially failed; valuation may be stale",
          zap.String("ticker", ticker), zap.Error(err))
      // Best-effort: continue. The narrate.fx.convert phase carries the partial-failure flag.
  }
  ```
  Add a corresponding `narrate.PhaseFXConvert` constant in `internal/observability/narrate/narrate.go` and emit on success/failure.

- [ ] **Step B9.3: Add `TestService_ConvertFinancialsToUSD_TWD`** that constructs a `HistoricalFinancialData` with `ReportingCurrency="TWD"`, mocks `MacroDataGateway.GetFXRate("TWD","USD")` → `0.0312`, asserts `Revenue` is multiplied by `0.0312` and `SharesOutstanding` is unchanged, and `ReportingCurrency` is now `"USD"`.

- [ ] **Step B9.4: Add `TestService_ConvertFinancialsToUSD_USD_NoOp`** with `ReportingCurrency="USD"` — assert no `GetFXRate` call (`assertNotCalled`), values unchanged.

- [ ] **Step B9.5: Add `TestService_ConvertFinancialsToUSD_FXFailure_PreservesData`** — mock returns error; assert original values preserved and the function returns an error.

- [ ] **Step B9.6: Run tests**:
  ```bash
  go test -run "TestService_ConvertFinancialsToUSD" ./internal/services/valuation/...
  ```

- [ ] **Step B9.7: Commit**
  ```bash
  git add internal/services/valuation/currency.go internal/services/valuation/service.go internal/services/valuation/currency_test.go internal/observability/narrate/narrate.go
  git commit -m "feat(valuation): FX-convert IFRS reporting-currency financials to USD (Phase B9)"
  ```

---

### Task B10: Apply ADR ratio to ordinary-share counts before per-share calculations

**Files:**
- Modify: `internal/services/valuation/service.go` (apply ratio after currency conversion, before WACC)
- Test: `internal/services/valuation/service_test.go` (TSM-shape integration test)

**Rationale:** The valuation service computes `intrinsic_value_per_share = equity_value / shares_outstanding`. For ADRs, `equity_value` is now in USD (post-B9) but `shares_outstanding` is in *ordinary shares* from SEC. Dividing by ordinary shares gives per-ordinary-share value; we want per-ADR value to compare against the listed price. So divide ordinary shares by the ADR ratio.

- [ ] **Step B10.1: Add a small helper** to `service.go`:
  ```go
  // applyADRRatio rewrites SharesOutstanding and DilutedSharesOutstanding on
  // every period from "ordinary shares" to "ADR-equivalent shares" by dividing
  // by the configured ratio. No-op when ratio is 1 or the ticker has no entry.
  // Logs WARN if YF-reported sharesOutstanding deviates from
  // (SECshares / ratio) by more than 10%, indicating the ratio config may be
  // stale.
  func (s *Service) applyADRRatio(ctx context.Context, ticker string, hist *entities.HistoricalFinancialData, marketData *entities.MarketData)
  ```
  Logic:
  - Look up `s.adrRatios[strings.ToUpper(ticker)]`. If absent or 1, return.
  - For each period: `fd.SharesOutstanding /= float64(ratio); fd.DilutedSharesOutstanding /= float64(ratio)`.
  - Latest-period sanity check: if `marketData.SharesOutstanding > 0`, compute `expected := latest.SharesOutstanding` and `observed := marketData.SharesOutstanding`; warn if `math.Abs(expected-observed)/observed > 0.10`.

- [ ] **Step B10.2: Insert the call** in `service.go` immediately after `convertFinancialsToUSD`:
  ```go
  s.applyADRRatio(ctx, ticker, historicalData, marketData)
  ```

- [ ] **Step B10.3: Add `TestService_ApplyADRRatio_TSM_Divides5x`** — construct `historicalData` with `SharesOutstanding=25_932_733_242` and config-mock `ratio=5`; assert post-call `SharesOutstanding == 5_186_546_648.4` (within 1e-3 tolerance).

- [ ] **Step B10.4: Add `TestService_ApplyADRRatio_NoEntry_NoOp`** — ticker absent from config, assert shares unchanged.

- [ ] **Step B10.5: Add `TestService_ApplyADRRatio_DivergentYFShares_WarnsButProceeds`** — mock YF reporting `1_000_000_000` (very different from `25.9B / 5`); assert WARN log fires and shares are still divided.

- [ ] **Step B10.6: Run tests**:
  ```bash
  go test -run "TestService_ApplyADRRatio" ./internal/services/valuation/...
  ```

- [ ] **Step B10.7: Commit**
  ```bash
  git add internal/services/valuation/service.go internal/services/valuation/service_test.go
  git commit -m "feat(valuation): apply ADR ratio to ordinary-share counts (Phase B10)"
  ```

---

### Task B11: Stop returning `ErrForeignPrivateIssuer` for IFRS filers we now support

**Files:**
- Modify: `internal/infra/gateways/sec/parser.go` (`classifyEmptyParseError`)
- Modify: `internal/services/valuation/service.go` (graceful degradation if ratio/FX missing)
- Test: `internal/infra/gateways/sec/parser_test.go`

**Rationale:** With B5–B10 in place, IFRS data parses successfully and Phase A's `ErrForeignPrivateIssuer` only fires when *no* fields could be extracted (e.g., a JGAAP filer or a 20-F that doesn't tag IFRS at all). The error stays as a safety net but its blast radius shrinks dramatically.

- [ ] **Step B11.1: Verify `classifyEmptyParseError`** still does the right thing — it only fires when `len(historical.Data) == 0`. After B6, IFRS parses succeed, so `historical.Data` is non-empty for TSM. Add an explicit comment in the function noting the post-B6 invariant: *"Only triggers when both us-gaap AND ifrs-full failed to yield ANY usable period — typically JGAAP / K-IFRS / unmapped IFRS extensions."*

- [ ] **Step B11.2: Service-layer guard** — in `service.go`, after `convertFinancialsToUSD` returns an error AND the historical data has non-USD currency, return `ErrForeignPrivateIssuer` so the user gets the clear message rather than a downstream WACC NaN:
  ```go
  if err != nil && hasNonUSDPeriod(historicalData) {
      return nil, fmt.Errorf("%w: FX conversion failed for ticker reporting in %s", ErrForeignPrivateIssuer, dominantCurrency(historicalData))
  }
  ```

- [ ] **Step B11.3: Run all valuation + parser tests**:
  ```bash
  go test ./internal/infra/gateways/sec/... ./internal/services/valuation/...
  ```

- [ ] **Step B11.4: Commit**
  ```bash
  git add internal/infra/gateways/sec/parser.go internal/services/valuation/service.go
  git commit -m "feat(valuation): tighten FPI sentinel scope to truly-unsupported taxonomies (Phase B11)"
  ```

---

### Task B12: End-to-end TSM verification + docs update

**Files:**
- Modify: `internal/api/v1/handlers/fair_value.go` (add `currency` and `adr_ratio_applied` fields to `FairValueResponse` if not already present)
- Modify: `docs/openapi.yaml` (response schema additions)
- Modify: `CLAUDE.md` (rewrite the FPI gotcha to reflect new coverage)
- New: `internal/integration/ifrs_filer_test.go` (E2E gated by `E2E_LIVE=1`)
- Test: existing `internal/integration/...`

**Rationale:** Pin TSM's behavior so a future parser refactor can't regress. The integration test is gated by the same env var the rest of the live tests use.

- [ ] **Step B12.1: Add response fields** to `FairValueResponse`:
  ```go
  // Currency is the ISO-4217 code of the currency intrinsic_value_per_share
  // is denominated in. Always "USD" for now (FX-converted at the service
  // layer before the result is built).
  Currency string `json:"currency"`
  // ADRRatioApplied is the ordinary-shares-per-ADR multiplier used when
  // computing per-share values for foreign private issuers; 1 for domestic
  // 10-K filers. Surfaces in the response for transparency.
  ADRRatioApplied int `json:"adr_ratio_applied,omitempty"`
  ```

- [ ] **Step B12.2: Update OpenAPI** schema for `/api/v1/fair-value/{ticker}` with the two new fields.

- [ ] **Step B12.3: E2E integration test** — `internal/integration/ifrs_filer_test.go`:
  ```go
  //go:build integration
  // +build integration

  func TestE2E_TSM_ProducesValuation(t *testing.T) {
      if os.Getenv("E2E_LIVE") != "1" {
          t.Skip("requires E2E_LIVE=1 (calls live SEC + Yahoo + FRED)")
      }
      // Boot the server in-process via the fx DI container.
      // GET /api/v1/fair-value/TSM with the demo key.
      // Assert: HTTP 200, body has fair_value > 0, currency=="USD",
      //         adr_ratio_applied==5, industry has both SIC + heuristic labels.
  }
  ```

- [ ] **Step B12.4: Manual smoke test**:
  ```bash
  go run cmd/server/main.go &
  curl -s -H "X-API-Key: <demo>" http://localhost:8080/api/v1/fair-value/TSM | jq '.fair_value, .currency, .adr_ratio_applied'
  ```
  Expected output (Damodaran-ish ballpark for TSM today): a number in the **$200 – $600** range, `"USD"`, `5`. If the number is implausibly small (e.g., $40), the ADR ratio wasn't applied — bisect B10. If it's wildly large ($2000+), the FX conversion is missing — bisect B9.

- [ ] **Step B12.5: Update CLAUDE.md** "Common Gotchas" — the FPI entry now reads:
  > Foreign private issuers filing 20-F with `ifrs-full` taxonomy (TSM, ASML, NVO, AZN, SAP, BABA, …) are fully supported as of Phase B of the IFRS-FPI plan. Reporting-currency values are FX-converted to USD via `MacroDataGateway.GetFXRate` (FRED daily series with `config/fx_rates.json` fallback), and ordinary-share counts are divided by the ADR ratio in `config/adr_ratios.json` so per-share values match the listed ADR price. Tickers using truly-unmapped taxonomies (JGAAP, K-IFRS, certain Form F-1 extensions) still return `HTTP 422 FOREIGN_PRIVATE_ISSUER_UNSUPPORTED` — add their concept names to `findValue` in `sec/parser.go` to extend coverage.

- [ ] **Step B12.6: Final test run + lint**:
  ```bash
  go test ./...
  go vet ./...
  ./scripts/lint-logs.sh
  ```

- [ ] **Step B12.7: Commit**
  ```bash
  git add internal/api/v1/handlers/fair_value.go docs/openapi.yaml internal/integration/ifrs_filer_test.go CLAUDE.md
  git commit -m "feat(api): expose currency + adr_ratio_applied in fair-value response (Phase B12)"
  git tag phase-b-ifrs-fpi-support
  ```

---

## Self-review (filled in after writing the plan)

**Spec coverage** — every section of the original analysis maps to a task:

| Original requirement | Tasks |
|---|---|
| Distinct error code for 20-F filers (Option 2) | A1, A2, A3, A4 |
| IFRS taxonomy parsing (Option 1, parser core) | B5, B6 |
| Reporting-currency capture | B5 |
| FX conversion infra | B7 |
| FX conversion at service layer | B9 |
| ADR ratio infra | B8 |
| ADR ratio application | B10 |
| Tighten FPI sentinel post-feature | B11 |
| E2E + docs | B12 |
| Regression test fixture from real TSM artifact | B5.5, B6.5 |

**Placeholder scan** — no `TBD` / `implement later` / `similar to Task N` strings present.

**Type consistency check:**
- `ports.ErrForeignPrivateIssuer` (A1) → `valuation.ErrForeignPrivateIssuer` (A2) → `"FOREIGN_PRIVATE_ISSUER_UNSUPPORTED"` HTTP code (A3) — three names, one chain, all consistent.
- `FinancialData.ReportingCurrency` (B5) → consumed by `convertFinancialsToUSD` (B9) → reset to `"USD"` after conversion — same field, same name throughout.
- `s.adrRatios map[string]int` (B8) → consumed by `applyADRRatio` (B10) — type matches.
- `MacroDataGateway.GetFXRate(ctx, from, to)` (B7) → called as `s.macroGateway.GetFXRate(ctx, fd.ReportingCurrency, "USD")` (B9) — signature matches.

No issues found.
