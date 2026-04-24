# Reviewer Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the 10 open reviewer follow-ups in `docs/reviewer/` (B-2, D-1, D-2, Q-1, Q-2, Q-3, S-1, S-4, V4-1 11-item polish pass, Y-2) so the post-MVP cleanup queue is empty.

**Architecture:** Three-phase approach. Phase 0 commits 4 items already implemented but uncommitted (Q1/Q2/Q3/Y2 + V4.1-N10 doc). Phase 1 handles HTTP/persistence code-quality (D-1, D-2). Phase 2 handles the valuation engine (B-2, S-1/S-4, V4-1 polish bundle). Phase 3 updates tracking docs.

**Tech Stack:** Go 1.23, Gin, sqlx, uber/zap. `embed.FS` for config bundling (S-1/S-4). No new dependencies.

---

## Task 0: Land the in-flight Q1/Q2/Q3/Y2 + V4.1-N10 work

**Files (already edited, not yet committed):**
- Modify: `internal/api/server.go` (Q-2 errors.Is, Q-3 removed duplicate middleware)
- Modify: `internal/api/server_test.go` (Q-1 429 assertions, Y-2 init→TestMain)
- Modify: `docs/reviewer/V4-1-post-simplify-nits.md` (N-10 WON'T FIX note)

- [ ] **Step 0.1: Verify the in-flight diff compiles**

Run: `go build ./internal/api/...`
Expected: clean build, no output.

- [ ] **Step 0.2: Verify the in-flight diff passes tests**

Run: `go test ./internal/api/... -run 'TestServer_rateLimitMiddleware|authMiddleware' -count=1 -v`
Expected: PASS for the rate-limit denied case and auth sentinel cases.

- [ ] **Step 0.3: Commit the bundle**

```bash
git add internal/api/server.go internal/api/server_test.go docs/reviewer/V4-1-post-simplify-nits.md
git commit -m "$(cat <<'EOF'
chore(reviewer): close Q1/Q2/Q3/Y2 and mark V4.1-N10 WON'T FIX

- Q1: assert all 8 fields on 429 response (error.message/type, rate_limit.*, timestamp, path, method)
- Q2: switch to errors.Is() in authMiddleware for sentinel comparison
- Q3: remove duplicate requestIDMiddleware registration in setupMiddleware
- Y2: replace package-level init() with TestMain() in server_test.go
- V4.1-N10: mark SIC cache empty-string sentinel WON'T FIX (reviewer noted skip-worthy)
EOF
)"
```

- [ ] **Step 0.4: Archive the four resolved reviewer docs**

```bash
git mv docs/reviewer/Q1-429-response-field-assertions.md docs/reviewer/archive/Q1-429-response-field-assertions.md
git mv docs/reviewer/Q2-auth-switch-vs-errors-is.md docs/reviewer/archive/Q2-auth-switch-vs-errors-is.md
git mv docs/reviewer/Q3-duplicate-requestid-middleware.md docs/reviewer/archive/Q3-duplicate-requestid-middleware.md
git mv docs/reviewer/Y2-init-vs-testmain.md docs/reviewer/archive/Y2-init-vs-testmain.md
git commit -m "docs(reviewer): archive Q1/Q2/Q3/Y2 — resolved in previous commit"
```

---

## Task 1: D-1 — Extract `storeWith` helper (dedupe Store / storeInTx)

**Files:**
- Modify: `internal/infra/repositories/sqlite/financial_data_repository.go` (lines 31-113 Store, 325-407 storeInTx)
- Verify-only: `internal/infra/repositories/sqlite/financial_data_repository_test.go`

- [ ] **Step 1.1: Locate both callers and confirm their queries are identical**

Run: `diff <(sed -n '42,69p' internal/infra/repositories/sqlite/financial_data_repository.go) <(sed -n '336,363p' internal/infra/repositories/sqlite/financial_data_repository.go)`
Expected: empty diff (queries identical).

- [ ] **Step 1.2: Add the executor interface + `storeWith` helper**

Insert immediately above `Store` (line 30):

```go
// namedExecer abstracts *sqlx.DB and *sqlx.Tx so Store and storeInTx can share
// the query/args build path. Both satisfy this interface via NamedExecContext.
type namedExecer interface {
	NamedExecContext(ctx context.Context, query string, arg interface{}) (sql.Result, error)
}

// storeWith builds and executes the canonical INSERT for a FinancialData row
// against any executor (DB or Tx). This is the single source of truth — adding
// a column requires one change, not two.
func (r *FinancialDataRepository) storeWith(ctx context.Context, exec namedExecer, data *entities.FinancialData) error {
	if data == nil {
		return fmt.Errorf("financial data cannot be nil")
	}

	missingFieldsJSON, err := json.Marshal(data.MissingFields)
	if err != nil {
		return fmt.Errorf("failed to marshal missing fields: %w", err)
	}

	query := `
		INSERT INTO financial_data (
			ticker, cik, filing_period, filing_date, as_of_date,
			operating_income, normalized_operating_income, revenue,
			interest_expense, tax_rate,
			total_assets, tangible_assets, goodwill, other_intangibles,
			total_debt, interest_bearing_debt,
			inventory, inventory_turnover, dead_inventory_writedown,
			dividends_per_share, net_income, gain_on_property_sales,
			depreciation_and_amortization, capital_expenditures, operating_cash_flow,
			current_assets, current_liabilities,
			cash_and_cash_equivalents, stockholders_equity,
			shares_outstanding, diluted_shares_outstanding,
			has_normalized_data, missing_fields, created_at, updated_at
		) VALUES (
			:ticker, :cik, :filing_period, :filing_date, :as_of_date,
			:operating_income, :normalized_operating_income, :revenue,
			:interest_expense, :tax_rate,
			:total_assets, :tangible_assets, :goodwill, :other_intangibles,
			:total_debt, :interest_bearing_debt,
			:inventory, :inventory_turnover, :dead_inventory_writedown,
			:dividends_per_share, :net_income, :gain_on_property_sales,
			:depreciation_and_amortization, :capital_expenditures, :operating_cash_flow,
			:current_assets, :current_liabilities,
			:cash_and_cash_equivalents, :stockholders_equity,
			:shares_outstanding, :diluted_shares_outstanding,
			:has_normalized_data, :missing_fields, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		)`

	args := map[string]interface{}{
		"ticker":                        data.Ticker,
		"cik":                           data.CIK,
		"filing_period":                 data.FilingPeriod,
		"filing_date":                   data.FilingDate,
		"as_of_date":                    data.AsOf,
		"operating_income":              data.OperatingIncome,
		"normalized_operating_income":   data.NormalizedOperatingIncome,
		"revenue":                       data.Revenue,
		"interest_expense":              data.InterestExpense,
		"tax_rate":                      data.TaxRate,
		"total_assets":                  data.TotalAssets,
		"tangible_assets":               data.TangibleAssets,
		"goodwill":                      data.Goodwill,
		"other_intangibles":             data.OtherIntangibles,
		"total_debt":                    data.TotalDebt,
		"interest_bearing_debt":         data.InterestBearingDebt,
		"inventory":                     data.Inventory,
		"inventory_turnover":            data.InventoryTurnover,
		"dead_inventory_writedown":      data.DeadInventoryWritedown,
		"dividends_per_share":           data.DividendsPerShare,
		"net_income":                    data.NetIncome,
		"gain_on_property_sales":        data.GainOnPropertySales,
		"depreciation_and_amortization": data.DepreciationAndAmortization,
		"capital_expenditures":          data.CapitalExpenditures,
		"operating_cash_flow":           data.OperatingCashFlow,
		"current_assets":                data.CurrentAssets,
		"current_liabilities":           data.CurrentLiabilities,
		"cash_and_cash_equivalents":     data.CashAndCashEquivalents,
		"stockholders_equity":           data.StockholdersEquity,
		"shares_outstanding":            data.SharesOutstanding,
		"diluted_shares_outstanding":    data.DilutedSharesOutstanding,
		"has_normalized_data":           data.HasNormalizedData,
		"missing_fields":                string(missingFieldsJSON),
	}

	if _, err := exec.NamedExecContext(ctx, query, args); err != nil {
		return fmt.Errorf("failed to store financial data: %w", err)
	}
	return nil
}
```

- [ ] **Step 1.3: Collapse Store to a one-liner**

Replace the old `Store` body (lines 31-113) with:

```go
// Store stores financial data for a company.
func (r *FinancialDataRepository) Store(ctx context.Context, data *entities.FinancialData) error {
	return r.storeWith(ctx, r.db, data)
}
```

- [ ] **Step 1.4: Collapse storeInTx to a one-liner**

Replace the old `storeInTx` body (lines 326-407) with:

```go
// storeInTx inserts a single period's data using the given transaction handle.
func (r *FinancialDataRepository) storeInTx(ctx context.Context, tx *sqlx.Tx, data *entities.FinancialData) error {
	return r.storeWith(ctx, tx, data)
}
```

- [ ] **Step 1.5: Build + test**

Run: `go build ./internal/infra/... && go test ./internal/infra/repositories/sqlite/... -count=1`
Expected: build succeeds; all existing repo tests PASS (behavior is unchanged).

- [ ] **Step 1.6: Commit**

```bash
git add internal/infra/repositories/sqlite/financial_data_repository.go
git commit -m "$(cat <<'EOF'
refactor(sqlite): extract storeWith helper to dedupe Store/storeInTx (D-1)

Both methods carried identical 80-line INSERT blocks differing only in
the executor. Introduce a `namedExecer` interface satisfied by *sqlx.DB
and *sqlx.Tx, collapse Store/storeInTx to one-line delegates over
`storeWith`. Column additions now touch a single place.
EOF
)"
```

- [ ] **Step 1.7: Archive D-1 reviewer doc**

```bash
git mv docs/reviewer/D1-storeInTx-duplicates-store.md docs/reviewer/archive/D1-storeInTx-duplicates-store.md
git commit -m "docs(reviewer): archive D-1 — resolved in previous commit"
```

---

## Task 2: D-2 — `sendError` uses `ErrorResponse` struct, not `gin.H`

**Files:**
- Modify: `internal/api/v1/handlers/fair_value.go:487-503`

- [ ] **Step 2.1: Locate the current `sendError`**

Run: `sed -n '487,503p' internal/api/v1/handlers/fair_value.go`
Expected: the gin.H-returning block with `"timestamp": time.Now().UTC()` (a `time.Time`, not a string).

- [ ] **Step 2.2: Replace the body with the `ErrorResponse` struct form**

Replace lines 487-503 of `internal/api/v1/handlers/fair_value.go` with:

```go
// sendError sends an RFC 7807 compliant error response, consistent with
// the server.go respondWithError format (code, timestamp, method fields).
// Uses the ErrorResponse struct (not gin.H) so field additions stay
// compile-checked.
func (h *FairValueHandler) sendError(c *gin.Context, status int, errorType, title, detail string, ctx map[string]interface{}) {
	c.Header("Content-Type", "application/problem+json")
	c.JSON(status, ErrorResponse{
		Type:      "https://problems.midas.dev/" + errorType,
		Title:     title,
		Status:    status,
		Detail:    detail,
		Instance:  c.Request.URL.Path,
		Context:   ctx,
		Code:      errorType,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Method:    c.Request.Method,
	})
	c.Abort()
}
```

- [ ] **Step 2.3: Verify build**

Run: `go build ./internal/api/...`
Expected: clean build. (`ErrorResponse` is defined in the same file at line 175; no new imports needed.)

- [ ] **Step 2.4: Run handler tests**

Run: `go test ./internal/api/v1/handlers/... -count=1`
Expected: PASS. The timestamp type change (`time.Time` → RFC3339 `string`) happens to serialize identically to today's behavior because `json.Marshal(time.Time)` already uses RFC3339, so any existing assertions on the timestamp field stay green.

- [ ] **Step 2.5: Commit and archive**

```bash
git add internal/api/v1/handlers/fair_value.go
git commit -m "$(cat <<'EOF'
refactor(handlers): sendError uses ErrorResponse struct (D-2)

Replace gin.H with the typed ErrorResponse so struct drift is
compile-checked and the timestamp field is explicitly RFC3339 rather
than relying on implicit json marshaling of time.Time.
EOF
)"
git mv docs/reviewer/D2-sendError-gin-H-vs-struct.md docs/reviewer/archive/D2-sendError-gin-H-vs-struct.md
git commit -m "docs(reviewer): archive D-2 — resolved in previous commit"
```

---

## Task 3: B-2 + V4.1-N1 — Underscore-boundary match + extract `thresholds` package

These two items both touch `crosscheck.go`. Bundling keeps the diff coherent.

**Files:**
- Create: `internal/services/valuation/thresholds/thresholds.go`
- Modify: `internal/services/valuation/crosscheck.go`
- Modify: `internal/services/valuation/models/ffo.go` (lines 22-27)
- Modify: `internal/services/valuation/models/ddm.go` (lines 19-24)
- Modify: `internal/services/valuation/models/revenue_multiple.go` (lines 150-158)
- Create: `internal/services/valuation/crosscheck_boundary_test.go` (new test file for B-2)

- [ ] **Step 3.1: Write the failing boundary test (TDD for B-2)**

Create `internal/services/valuation/crosscheck_boundary_test.go`:

```go
package valuation

import "testing"

// TestLookupMultiple_UnderscoreBoundary pins B-2: prefix match must require a
// code boundary so "TECHNOLOGY" does not hit key "TECH". Matches at the end of
// the string or before an underscore both qualify.
func TestLookupMultiple_UnderscoreBoundary(t *testing.T) {
	multiples := map[string]float64{
		"default":   10.0,
		"TECH":      18.0,
		"TECH_SAAS": 22.0,
		"FIN":       12.0,
	}

	tests := []struct {
		name     string
		industry string
		want     float64
	}{
		{"exact TECH", "TECH", 18.0},
		{"exact TECH_SAAS", "TECH_SAAS", 22.0},
		{"TECH_SAAS_CLOUD longest-prefix wins", "TECH_SAAS_CLOUD", 22.0},
		{"TECHNOLOGY must not hit TECH — falls to default", "TECHNOLOGY", 10.0},
		{"FINESSE must not hit FIN — falls to default", "FINESSE", 10.0},
		{"unknown falls to default", "ZZZ", 10.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := LookupMultiple(multiples, tc.industry)
			if got != tc.want {
				t.Fatalf("LookupMultiple(%q) = %v, want %v", tc.industry, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 3.2: Run the test — expect the TECHNOLOGY/FINESSE cases to fail**

Run: `go test ./internal/services/valuation/ -run TestLookupMultiple_UnderscoreBoundary -v`
Expected: FAIL on the `TECHNOLOGY` case (returns 18.0) and the `FINESSE` case (returns 12.0) because `strings.HasPrefix` matches without a boundary.

- [ ] **Step 3.3: Fix `LookupMultiple` (crosscheck.go:128)**

Replace the `LookupMultiple` body in `internal/services/valuation/crosscheck.go`:

```go
// LookupMultiple finds the appropriate multiple for an industry code.
// Tries exact match first, then longest prefix match at an underscore
// boundary, then default. Requiring `code+"_"` prevents "TECHNOLOGY" from
// silently matching key "TECH" when new codes are added to the config.
func LookupMultiple(multiples map[string]float64, industry string) float64 {
	upper := strings.ToUpper(industry)

	if val, ok := multiples[upper]; ok {
		return val
	}

	bestKey := ""
	bestVal := 0.0
	for code, val := range multiples {
		if code == "default" {
			continue
		}
		// Match must end at the string end or an underscore.
		if upper == code || strings.HasPrefix(upper, code+"_") {
			if len(code) > len(bestKey) {
				bestKey = code
				bestVal = val
			}
		}
	}
	if bestKey != "" {
		return bestVal
	}

	if val, ok := multiples["default"]; ok {
		return val
	}
	return 0
}
```

- [ ] **Step 3.4: Fix `getMultiple` in revenue_multiple.go:142**

Replace the `getMultiple` method body:

```go
// getMultiple returns the EV/Revenue multiple for the given industry code.
// Falls back to the default multiple if no industry-specific multiple is
// configured. Uses longest-prefix-match at an underscore boundary to avoid
// "TECHNOLOGY" → "TECH" silent matches.
func (m *RevenueMultipleModel) getMultiple(industry string) float64 {
	upper := strings.ToUpper(industry)

	if multiple, ok := m.multiples[upper]; ok {
		return multiple
	}

	bestKey := ""
	bestVal := 0.0
	for code, multiple := range m.multiples {
		if code == "default" {
			continue
		}
		if upper == code || strings.HasPrefix(upper, code+"_") {
			if len(code) > len(bestKey) {
				bestKey = code
				bestVal = multiple
			}
		}
	}
	if bestKey != "" {
		return bestVal
	}

	if defaultMultiple, ok := m.multiples["default"]; ok {
		return defaultMultiple
	}
	return DefaultEVRevenueMultiple
}
```

- [ ] **Step 3.5: Re-run boundary test — expect PASS**

Run: `go test ./internal/services/valuation/ -run TestLookupMultiple_UnderscoreBoundary -v -count=1`
Expected: PASS for all 6 subcases.

- [ ] **Step 3.6: Create the `thresholds` leaf package (V4.1-N1)**

Create `internal/services/valuation/thresholds/thresholds.go`:

```go
// Package thresholds exposes the single source of truth for divergence
// thresholds used across cross-check sites (P/E, EV/EBITDA, P/FCF, NAV, P/BV).
// Lives in a leaf package so both `valuation` and `valuation/models` can import
// it without creating a cycle.
package thresholds

// DeviationHigh is the upper-bound multiplier. Ratios above this are flagged.
const DeviationHigh = 2.0

// DeviationLow is the lower-bound multiplier. Ratios below this are flagged.
const DeviationLow = 0.5
```

- [ ] **Step 3.7: Replace the local constants in `crosscheck.go` with imports**

In `internal/services/valuation/crosscheck.go`:

1. Add to imports:

```go
"github.com/midas/dcf-valuation-api/internal/services/valuation/thresholds"
```

2. Replace lines 12-26 (all the threshold constants + alias block) with:

```go
// Re-exported for backward compatibility with external callers that may have
// read `valuation.DeviationThresholdHigh`/`Low` before the thresholds package
// was introduced. New code should import `thresholds` directly.
const (
	DeviationThresholdHigh = thresholds.DeviationHigh
	DeviationThresholdLow  = thresholds.DeviationLow
)
```

3. In `FlagDivergence` (lines 39, 43), replace `DeviationThresholdHigh`/`DeviationThresholdLow` with `thresholds.DeviationHigh`/`thresholds.DeviationLow`.
4. In `isDeviationReasonable` (line 160), replace `deviationThresholdLow`/`deviationThresholdHigh` with `thresholds.DeviationLow`/`thresholds.DeviationHigh`.

- [ ] **Step 3.8: Replace local constants in `ffo.go`**

In `internal/services/valuation/models/ffo.go`:

1. Add to imports:

```go
"github.com/midas/dcf-valuation-api/internal/services/valuation/thresholds"
```

2. Delete lines 22-27 (the local `navDeviationThresholdHigh`/`Low` block).
3. In the `Calculate` method (line 235), replace `navDeviationThresholdHigh`/`navDeviationThresholdLow` with `thresholds.DeviationHigh`/`thresholds.DeviationLow`.

- [ ] **Step 3.9: Replace local constants in `ddm.go`**

In `internal/services/valuation/models/ddm.go`:

1. Add to imports:

```go
"github.com/midas/dcf-valuation-api/internal/services/valuation/thresholds"
```

2. Delete lines 19-24 (the local `ddmPBVDeviationHigh`/`Low` block).
3. In `Calculate` (line 144), replace `ddmPBVDeviationHigh`/`ddmPBVDeviationLow` with `thresholds.DeviationHigh`/`thresholds.DeviationLow`.

- [ ] **Step 3.10: Run the full valuation test suite**

Run: `go test ./internal/services/valuation/... -count=1`
Expected: all PASS. The threshold values are unchanged (2.0 / 0.5); only their home moved.

- [ ] **Step 3.11: Commit**

```bash
git add internal/services/valuation/thresholds/ internal/services/valuation/crosscheck.go internal/services/valuation/crosscheck_boundary_test.go internal/services/valuation/models/ffo.go internal/services/valuation/models/ddm.go internal/services/valuation/models/revenue_multiple.go
git commit -m "$(cat <<'EOF'
refactor(valuation): underscore-boundary prefix match + thresholds pkg (B-2, V4.1-N1)

- B-2: LookupMultiple and RevenueMultipleModel.getMultiple now require
  either an exact match or a `code + "_"` boundary, so "TECHNOLOGY" no
  longer silently matches key "TECH". Added regression test.
- V4.1-N1: extract DeviationHigh/DeviationLow to a leaf
  `valuation/thresholds` package so valuation and valuation/models share
  one source. Back-compat aliases kept on the valuation package.
EOF
)"
git mv docs/reviewer/B2-prefix-match-underscore-boundary.md docs/reviewer/archive/B2-prefix-match-underscore-boundary.md
git commit -m "docs(reviewer): archive B-2 — resolved in previous commit"
```

---

## Task 4: S-1 + S-4 — embed.FS for config files

Closes both items in one change: S-1 (relative paths) and S-4 (constructors perform I/O). After this task, no production constructor touches the filesystem.

**Files:**
- Create: `config/configfs/configfs.go` (single `embed.FS` point)
- Modify: `internal/services/valuation/models/ffo.go` (NewFFOModel signature)
- Modify: `internal/services/valuation/models/revenue_multiple.go` (NewRevenueMultipleModel signature)
- Modify: `internal/services/valuation/crosscheck.go` (LoadIndustryMultiples reads from embed)
- Modify: `internal/services/valuation/service.go:71-72` (call sites)
- Modify: `internal/services/datacleaner/industry/classifier.go:139-172` (NewIndustryClassifier reads from embed)
- Verify: existing `internal/integration/industry_code_detector_test.go` path overrides still work

- [ ] **Step 4.1: Create the embedded config filesystem**

Create `config/configfs/configfs.go`:

```go
// Package configfs exposes the repo's `config/` directory as an embed.FS so
// production code never depends on the process working directory. Tests that
// need a custom override construct models directly via the With-style
// constructors — they do not go through this FS.
package configfs

import "embed"

//go:embed all:*.json all:datacleaner/*.json
var FS embed.FS

// Read returns the contents of a file packaged into the binary. Path is
// relative to the repo's `config/` directory, e.g. "industry_multiples.json"
// or "datacleaner/industry_codes.json".
func Read(path string) ([]byte, error) {
	return FS.ReadFile(path)
}
```

Note: this `configfs.go` must sit **inside** `config/` for the embed directives to see `industry_multiples.json` and `datacleaner/industry_codes.json` as siblings/children.

- [ ] **Step 4.2: Verify embed compiles**

Run: `go build ./config/configfs/`
Expected: clean build.

- [ ] **Step 4.3: Add a unit test for the embed FS**

Create `config/configfs/configfs_test.go`:

```go
package configfs

import (
	"strings"
	"testing"
)

func TestRead_IndustryMultiples(t *testing.T) {
	b, err := Read("industry_multiples.json")
	if err != nil {
		t.Fatalf("Read(industry_multiples.json) error: %v", err)
	}
	if !strings.Contains(string(b), "reit_pffo_multiples") {
		t.Fatalf("industry_multiples.json missing expected key")
	}
}

func TestRead_IndustryCodes(t *testing.T) {
	b, err := Read("datacleaner/industry_codes.json")
	if err != nil {
		t.Fatalf("Read(industry_codes.json) error: %v", err)
	}
	if !strings.Contains(string(b), "mappings") {
		t.Fatalf("industry_codes.json missing expected key")
	}
}
```

Run: `go test ./config/configfs/ -v`
Expected: PASS.

- [ ] **Step 4.4: Rewrite `LoadIndustryMultiples` in crosscheck.go to read from embed**

In `internal/services/valuation/crosscheck.go`:

1. Remove the `"os"` import (no longer needed).
2. Add: `"github.com/midas/dcf-valuation-api/config/configfs"`
3. Replace `LoadIndustryMultiples` (lines 110-123):

```go
// LoadIndustryMultiples parses the embedded industry_multiples.json file.
// The `path` parameter is deprecated and ignored — kept for call-site
// backward compatibility; pass "" for new call sites.
func LoadIndustryMultiples(_ string) (*industryMultiplesConfig, error) {
	data, err := configfs.Read("industry_multiples.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded industry multiples config: %w", err)
	}
	var cfg industryMultiplesConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse industry multiples config: %w", err)
	}
	return &cfg, nil
}
```

- [ ] **Step 4.5: Collapse the FFO model constructor signatures (S-4)**

In `internal/services/valuation/models/ffo.go`:

1. Remove `"os"` and `"encoding/json"` from imports.
2. Add: `"github.com/midas/dcf-valuation-api/config/configfs"`
3. Replace lines 19-20 (`DefaultIndustryMultiplesPath` const) — delete, no longer needed in production; keep only if any test imports it (grep shows no external imports, safe to delete).
4. Replace `NewFFOModel` (lines 45-59) with a zero-arg form that reads from embed:

```go
// NewFFOModel creates a new FFO model, reading the P/FFO multiple and NAV
// cap rate from the embedded industry_multiples.json (see config/configfs).
// No I/O to the host filesystem — safe in any working directory.
//
// NAV cross-check is enabled by default with the embedded cap rate. Pass
// an explicit 0 to NewFFOModelWithConfig to disable it.
func NewFFOModel(logger *zap.Logger) *FFOModel {
	multiple, capRate := loadFFOConfig()
	return &FFOModel{
		pffoMultiple: multiple,
		navCapRate:   capRate,
		logger:       logger.Named("ffo-model"),
	}
}
```

5. Replace `loadFFOConfig` (lines 80-104) with the embed-backed form; delete `loadPFFOMultiple` and `loadREITCapRate` entirely (this also closes V4.1-N2):

```go
// loadFFOConfig reads the embedded industry multiples config ONCE and returns
// both the P/FFO multiple and NAV cap rate. Falls back to defaults on any
// error. Replaces the three separate loaders that existed pre-V4.1-N2.
func loadFFOConfig() (pffoMultiple, navCapRate float64) {
	pffoMultiple = DefaultPFFOMultiple
	navCapRate = DefaultREITCapRate

	data, err := configfs.Read("industry_multiples.json")
	if err != nil {
		return pffoMultiple, navCapRate
	}
	var cfg struct {
		REITPFFOMultiples map[string]float64 `json:"reit_pffo_multiples"`
		REITCapRates      map[string]float64 `json:"reit_cap_rates"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return pffoMultiple, navCapRate
	}
	if v, ok := cfg.REITPFFOMultiples["default"]; ok && v > 0 {
		pffoMultiple = v
	}
	if v, ok := cfg.REITCapRates["default"]; ok && v > 0 {
		navCapRate = v
	}
	return pffoMultiple, navCapRate
}
```

6. Re-add the `encoding/json` import (still used by loadFFOConfig).

- [ ] **Step 4.6: Update `NewFFOModelWithMultiple` godoc (V4.1-N3)**

Above `NewFFOModelWithMultiple` (now around line 65), replace the existing comment with:

```go
// NewFFOModelWithMultiple creates an FFO model with an explicit P/FFO multiple
// and the default NAV cap rate (DefaultREITCapRate = 6%). NAV cross-check is
// enabled by default; use NewFFOModelWithConfig(multiple, 0, logger) to
// disable it. Kept for backward compatibility with tests that predate the
// consolidated two-field constructor.
func NewFFOModelWithMultiple(pffoMultiple float64, logger *zap.Logger) *FFOModel {
```

- [ ] **Step 4.7: Collapse the RevenueMultiple constructor (S-4)**

In `internal/services/valuation/models/revenue_multiple.go`:

1. Remove `"os"` and `"encoding/json"` from imports.
2. Add: `"github.com/midas/dcf-valuation-api/config/configfs"`
3. Replace `NewRevenueMultipleModel` (lines 32-52):

```go
// NewRevenueMultipleModel creates a new Revenue Multiple model, reading
// sector multiples from the embedded industry_multiples.json.
func NewRevenueMultipleModel(logger *zap.Logger) *RevenueMultipleModel {
	multiples := map[string]float64{"default": DefaultEVRevenueMultiple}
	if configMultiples, err := loadEVRevenueMultiples(); err == nil && len(configMultiples) > 0 {
		multiples = configMultiples
	}
	return &RevenueMultipleModel{
		multiples: multiples,
		logger:    logger.Named("revenue-multiple-model"),
	}
}
```

4. Replace `loadEVRevenueMultiples` (lines 172-186):

```go
// loadEVRevenueMultiples loads EV/Revenue multiples from the embedded config.
func loadEVRevenueMultiples() (map[string]float64, error) {
	data, err := configfs.Read("industry_multiples.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded industry multiples config: %w", err)
	}
	var cfg struct {
		EVRevenueMultiples map[string]float64 `json:"ev_revenue_multiples"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse industry multiples config: %w", err)
	}
	return cfg.EVRevenueMultiples, nil
}
```

5. Re-add the `encoding/json` import (still used).

- [ ] **Step 4.8: Update the two call sites in `service.go:71-72`**

In `internal/services/valuation/service.go`:

Replace:
```go
models.NewFFOModel(models.DefaultIndustryMultiplesPath, logger),
models.NewRevenueMultipleModel(models.DefaultIndustryMultiplesPath, logger),
```

With:
```go
models.NewFFOModel(logger),
models.NewRevenueMultipleModel(logger),
```

- [ ] **Step 4.9: Update the `IndustryClassifier` to read from embed (S-1)**

In `internal/services/datacleaner/industry/classifier.go`:

1. Add: `"github.com/midas/dcf-valuation-api/config/configfs"`
2. Change `NewIndustryClassifier` (lines 159-173) to call a new embed-backed loader:

```go
// NewIndustryClassifier creates a new industry classifier with configurations
// loaded from the embedded config (config/configfs). No working-directory
// dependency — safe in any deployment target.
func NewIndustryClassifier() *IndustryClassifier {
	classifier := &IndustryClassifier{
		sectorConfigs: make(map[string]*SectorConfig),
	}
	classifier.loadDefaultConfigurations()
	_ = classifier.loadEmbeddedCodes()
	return classifier
}

// loadEmbeddedCodes reads the compiled-in industry_codes.json. Kept private;
// tests that need a custom config use LoadIndustryCodesConfig with a path.
func (ic *IndustryClassifier) loadEmbeddedCodes() error {
	data, err := configfs.Read("datacleaner/industry_codes.json")
	if err != nil {
		return fmt.Errorf("failed to read embedded industry codes config: %w", err)
	}
	var cfg industryCodesConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to parse industry codes config: %w", err)
	}
	compileCodesConfig(&cfg)
	ic.codesConfig = &cfg
	return nil
}
```

3. Keep the existing `LoadIndustryCodesConfig(path)` method — tests use it.
4. Remove the `DefaultIndustryCodesPath` constant (no longer referenced in production).

- [ ] **Step 4.10: Run the full test suite**

Run: `go test ./... -count=1`
Expected: all PASS. Watch for:
- Tests that passed a config path to `NewFFOModel` / `NewRevenueMultipleModel` — none in the repo after the change (DI uses the new zero-arg form; tests use the `WithMultiple`/`WithMultiples` constructors that never touched I/O).
- `internal/integration/industry_code_detector_test.go` uses `LoadIndustryCodesConfig(path)` explicitly — still works.
- `fair_value_test.go:872` (`NewIndustryClassifier()` + path hack) — the path-hack fallback at lines 874-884 is now dead but harmless; can keep for one more cycle.

- [ ] **Step 4.11: Commit**

```bash
git add config/configfs/ internal/services/valuation/ internal/services/datacleaner/industry/classifier.go
git commit -m "$(cat <<'EOF'
refactor(config): embed industry configs; remove I/O from constructors (S-1, S-4, V4.1-N2, V4.1-N3)

- S-1: package config/configfs bundles industry_multiples.json and
  datacleaner/industry_codes.json via embed.FS. Production code no
  longer depends on the process working directory — Docker + standalone
  binary deployments now load industry rules correctly.
- S-4: NewFFOModel / NewRevenueMultipleModel / NewIndustryClassifier
  all read from embed instead of os.ReadFile. Constructors are fast and
  side-effect-free.
- V4.1-N2: loadPFFOMultiple and loadREITCapRate wrappers deleted;
  loadFFOConfig is the single entry point.
- V4.1-N3: documented that NewFFOModelWithMultiple enables NAV
  cross-check by default with DefaultREITCapRate.
EOF
)"
git mv docs/reviewer/S1-config-paths-relative.md docs/reviewer/archive/S1-config-paths-relative.md
git mv docs/reviewer/S4-constructors-perform-io.md docs/reviewer/archive/S4-constructors-perform-io.md
git commit -m "docs(reviewer): archive S-1 and S-4 — resolved in previous commit"
```

---

## Task 5: V4.1 polish bundle — N4, N5, N6, N7, N8, N9, N11

Five tiny, independent edits. Single commit.

**Files:**
- Modify: `internal/services/valuation/crosscheck.go` (N5: remove dead function)
- Modify: `internal/services/valuation/crosscheck_test.go` (N5: drop orphaned test)
- Modify: `internal/services/valuation/service.go` (N6: FCF comment)
- Modify: `internal/services/valuation/models/ddm.go` (N4: flatten Debug log + N7: hoist roe)
- Modify: `internal/core/entities/valuation.go` (N8: omitempty tags)
- Modify: `internal/services/valuation/models/ffo.go` (N11: warning format precision)
- Modify: `config/industry_multiples.json` (N9: uppercase reit keys)

- [ ] **Step 5.1: N5 — Delete `isDeviationReasonable` and its test**

Remove lines 154-161 of `internal/services/valuation/crosscheck.go` (the `isDeviationReasonable` function). Remove any test in `crosscheck_test.go` named `TestIsDeviationReasonable*`. Search it out:

Run: `grep -n "isDeviationReasonable\|IsDeviationReasonable" internal/services/valuation/crosscheck_test.go`
If it appears, delete the subtests / test function that call it.

- [ ] **Step 5.2: N6 — Document FCF simplification in service.go**

In `internal/services/valuation/service.go`, replace the block at line 671-677:

```go
		// Calculate FCF per share for P/FCF cross-check.
		// Simplified FCF = NetIncome + D&A - CapEx. This omits NWC change and
		// uses NetIncome rather than NOPAT (the DCF engine's "true FCF"
		// definition), so ImpliedPFCF is a sanity-check proxy, not the same
		// FCF number driving the DCF itself.
		fcfPerShare := 0.0
		fcf := latestFinancialData.NetIncome + latestFinancialData.DepreciationAndAmortization - latestFinancialData.CapitalExpenditures
		if fcf > 0 && sharesOutstanding > 0 {
			fcfPerShare = fcf / sharesOutstanding
		}
```

- [ ] **Step 5.3: N4 + N7 — Flatten DDM P/BV cross-check block and hoist `roe`**

In `internal/services/valuation/models/ddm.go`, replace the entire P/BV cross-check block (currently lines 129-157). The rewrite hoists `roe` to a single site, then uses an early-continue guard style:

```go
	// P/BV cross-check: implied P/BV (DDM value / book value per share) vs
	// ROE-justified P/BV (= (ROE - g) / (CoE - g)). Flags >2x or <0.5x divergence.
	pbvCheck := func() {
		if latest.StockholdersEquity <= 0 || input.SharesOutstanding <= 0 || latest.NetIncome <= 0 {
			return
		}
		bookValuePerShare := latest.StockholdersEquity / input.SharesOutstanding
		if bookValuePerShare <= 0 {
			return
		}
		impliedPBV := valuePerShare / bookValuePerShare
		roe := latest.NetIncome / latest.StockholdersEquity

		coeMinusG := costOfEquity - dividendGrowth
		if coeMinusG <= ddmDenominatorEpsilon {
			return
		}
		roeMinusG := roe - dividendGrowth
		if roeMinusG <= 0 {
			return
		}
		roeJustifiedPBV := roeMinusG / coeMinusG
		if roeJustifiedPBV <= 0 || impliedPBV <= 0 {
			return
		}
		ratio := impliedPBV / roeJustifiedPBV
		if ratio > thresholds.DeviationHigh || ratio < thresholds.DeviationLow {
			warnings = append(warnings,
				fmt.Sprintf("Implied P/BV (%.2fx) diverges from ROE-justified P/BV (%.2fx); ratio=%.2fx",
					impliedPBV, roeJustifiedPBV, ratio))
		}
		m.logger.Debug("P/BV cross-check",
			zap.Float64("implied_pbv", impliedPBV),
			zap.Float64("book_value_per_share", bookValuePerShare),
			zap.Float64("roe", roe),
			zap.Float64("dividend_growth", dividendGrowth))
	}
	pbvCheck()
```

Then in the ROE-reasonableness block above (lines 108-116) — already computes `roe := latest.NetIncome / latest.StockholdersEquity`. Leave that one in place; the closure above has its own `roe` scoped to the cross-check. (The review note flagged re-computation within the same block; the closure cleanly separates the two.)

- [ ] **Step 5.4: N8 — Add `omitempty` to `ImpliedPFCF` / `SectorMedianPFCF`**

In `internal/core/entities/valuation.go` lines 80-81, change:

```go
	ImpliedPFCF          float64  `json:"implied_pfcf"`            // DCF value per share / FCF per share
	SectorMedianPFCF     float64  `json:"sector_median_pfcf"`      // Sector median P/FCF ratio
```

to:

```go
	ImpliedPFCF          float64  `json:"implied_pfcf,omitempty"`    // DCF value per share / FCF per share
	SectorMedianPFCF     float64  `json:"sector_median_pfcf,omitempty"` // Sector median P/FCF ratio
```

- [ ] **Step 5.5: N9 — Uppercase `reit_*` keys in industry_multiples.json**

In `config/industry_multiples.json`, uppercase every key in `reit_pffo_multiples` and `reit_cap_rates` **except** `default`:

```json
  "reit_pffo_multiples": {
    "default": 15.0,
    "RESIDENTIAL": 18.0,
    "INDUSTRIAL": 22.0,
    "OFFICE": 12.0,
    "RETAIL_REIT": 13.0,
    "HEALTHCARE_REIT": 14.0,
    "DATA_CENTER": 25.0
  },
```

and

```json
  "reit_cap_rates": {
    "default": 0.06,
    "RESIDENTIAL": 0.05,
    "INDUSTRIAL": 0.055,
    "OFFICE": 0.07,
    "RETAIL_REIT": 0.065,
    "HEALTHCARE_REIT": 0.06,
    "DATA_CENTER": 0.045
  },
```

- [ ] **Step 5.6: N11 — Use `%.4g` for NAV warning format**

In `internal/services/valuation/models/ffo.go` line 236-238 (the `fmt.Sprintf` inside the NAV cross-check block), change:

```go
				warnings = append(warnings,
					fmt.Sprintf("P/FFO value ($%.2f) diverges from NAV cross-check ($%.2f/share, cap rate %.1f%%); ratio=%.2fx",
						valuePerShare, navPerShare, m.navCapRate*100, ratio))
```

to:

```go
				warnings = append(warnings,
					fmt.Sprintf("P/FFO value ($%.4g) diverges from NAV cross-check ($%.4g/share, cap rate %.1f%%); ratio=%.2fx",
						valuePerShare, navPerShare, m.navCapRate*100, ratio))
```

- [ ] **Step 5.7: Run the full test suite**

Run: `go test ./... -count=1`
Expected: all PASS. The N4/N7 refactor is behavior-preserving; N8 omitempty only affects JSON output when the fields are zero; N9 only matters when non-default REIT cap rates are read (currently none); N11 changes warning formatting only.

- [ ] **Step 5.8: Commit**

```bash
git add internal/services/valuation/crosscheck.go internal/services/valuation/crosscheck_test.go internal/services/valuation/service.go internal/services/valuation/models/ddm.go internal/services/valuation/models/ffo.go internal/core/entities/valuation.go config/industry_multiples.json
git commit -m "$(cat <<'EOF'
polish(valuation): V4.1 post-simplify bundle (N4, N5, N6, N7, N8, N9, N11)

- N4: flatten DDM P/BV cross-check into an early-return closure
- N5: delete unused isDeviationReasonable (FlagDivergence supersedes it)
- N6: comment the simplified FCF used for the P/FCF sanity check
- N7: ROE computed once per cross-check site (no double calc)
- N8: omitempty on ImpliedPFCF / SectorMedianPFCF — hides zero values
- N9: uppercase reit_pffo_multiples and reit_cap_rates keys — aligns
  with the uppercase convention used by every other map and keeps
  future industry-specific cap rates lookupable via LookupMultiple
- N11: %.4g format in NAV warning — preserves precision for $1K+ prices
EOF
)"
```

---

## Task 6: Update tracking docs

**Files:**
- Modify: `docs/THESIS.md` (known follow-ups table — S-1 / S-4 closed)
- Modify: `.claude/projects/<hash>/memory/MEMORY.md` (upgrade status item)
- Modify: `.claude/projects/<hash>/memory/project_upgrade_status.md` (open items list)

- [ ] **Step 6.1: Update THESIS.md known-follow-ups table**

In `docs/THESIS.md` section "Known Follow-Ups (Tracked, Not Blocking)", remove the S-1 and S-4 rows. Leave IC-1/IC-2/IC-3 (those are separate tracked items, not part of this cleanup).

- [ ] **Step 6.2: Update MEMORY upgrade-status note**

In `C:\Users\Yonatan Levin\.claude\projects\C--Users-Yonatan-Levin-Documents-Programming-Projects-FinTech-Strade-midas\memory\project_upgrade_status.md`, replace the open-items snapshot with:

```markdown
Open items (snapshot 2026-04-24): 0 in docs/reviewer/. All 10 items from
the 2026-04-23 snapshot (B-2, D-1, D-2, Q-1, Q-2, Q-3, S-1, S-4, V4-1, Y-2)
have been closed. docs/reviewer/ now contains only archive/.
```

- [ ] **Step 6.3: Archive the V4-1-post-simplify-nits doc**

```bash
git mv docs/reviewer/V4-1-post-simplify-nits.md docs/reviewer/archive/V4-1-post-simplify-nits.md
```

- [ ] **Step 6.4: Append a daily log entry**

Create / append to `C:\Users\Yonatan Levin\.claude\projects\C--Users-Yonatan-Levin-Documents-Programming-Projects-FinTech-Strade-midas\memory\daily\2026-04-24.md`:

```markdown
## 2026-04-24 — Reviewer cleanup sweep

Closed all 10 open reviewer items in a single session:
- Already-in-flight: Q1, Q2, Q3, Y2, V4.1-N10 (committed Task 0)
- D-1: storeWith helper extracted in sqlite repo
- D-2: sendError uses ErrorResponse struct
- B-2 + V4.1-N1: underscore-boundary prefix match + thresholds leaf package
- S-1 + S-4 + V4.1-N2 + V4.1-N3: config/configfs embed.FS; constructors no longer do I/O
- V4.1 polish bundle: N4, N5, N6, N7, N8, N9, N11

Net result: docs/reviewer/ now contains only archive/. THESIS.md known-follow-ups table shrinks to IC-1/IC-2/IC-3 (classification unification, separate track).
```

- [ ] **Step 6.5: Commit**

```bash
git add docs/THESIS.md docs/reviewer/archive/V4-1-post-simplify-nits.md "C:/Users/Yonatan Levin/.claude/projects/C--Users-Yonatan-Levin-Documents-Programming-Projects-FinTech-Strade-midas/memory/project_upgrade_status.md" "C:/Users/Yonatan Levin/.claude/projects/C--Users-Yonatan-Levin-Documents-Programming-Projects-FinTech-Strade-midas/memory/daily/2026-04-24.md"
git commit -m "docs: close reviewer cleanup tracking (THESIS, MEMORY, daily log)"
```

- [ ] **Step 6.6: Final sanity check**

Run: `go build ./... && go test ./... -count=1`
Expected: clean build, all tests PASS.

Run: `ls docs/reviewer/`
Expected: only `archive/` directory remaining.

---

## Summary

| Task | Reviewer Items Closed | New Code |
|------|----------------------|----------|
| 0    | Q1, Q2, Q3, Y2, V4.1-N10 | (in-flight only) |
| 1    | D-1                  | `storeWith` helper |
| 2    | D-2                  | `sendError` uses `ErrorResponse` |
| 3    | B-2, V4.1-N1         | `thresholds` package + boundary test |
| 4    | S-1, S-4, V4.1-N2, V4.1-N3 | `config/configfs` embed.FS |
| 5    | V4.1-N4/N5/N6/N7/N8/N9/N11 | polish bundle |
| 6    | (docs)               | tracking updates |

**Total commits:** 12 (including doc-archive commits).

**Risk level:** Low. Every task is behavior-preserving except S-1/S-4 (swaps config source from disk to embed, but JSON content is identical) and B-2 (fixes a latent prefix-boundary bug — no current code triggers the old behavior).
