# Graham Floor Metrics — Architecture Spec

| | |
|---|---|
| **Status** | DESIGN (ready for BACKEND) |
| **Author** | ARCH (code-architect) |
| **Date** | 2026-05-05 |
| **Related** | `internal/services/valuation/service.go` (`calculateTangibleValuePerShare`), `internal/api/v1/handlers/fair_value.go` (`FairValueResponse`), `internal/core/entities/valuation.go` (`ValuationResult`), `internal/core/entities/financial_data.go` (`FinancialData`), `internal/infra/gateways/sec/parser.go`, `docs/openapi.yaml`, `internal/infra/database/schema.sql` |
| **Out-of-scope** | Model router changes, growth/WACC math, screening endpoints |

---

## 1. Summary

Add a Graham-school **asset-floor / Net Current Asset Value (NCAV) diagnostic block** to the `/api/v1/fair-value/{ticker}` response. Four new per-share fields surface a balance-sheet-only downside floor (independent of operating earnings, growth, or WACC) so consumers can answer *"how cheap is this stock relative to the value of what it actually owns today?"*. The block is a transparency add-on, not a new valuation model: the model router (`internal/services/valuation/models/router.go`), DCF math, growth estimator, and equity-bridge are all untouched. As part of the same change we flip the existing `tangible_value_per_share` denominator from market-basic to diluted shares for cross-field consistency — a small but breaking numeric change called out in §10.

**Non-goal explicitly:** This spec does NOT add an NCAV-based valuation model to `internal/services/valuation/models/router.go`. The four new fields are diagnostics carried alongside the engine's output, never substituted for `dcf_value_per_share`.

---

## 2. Motivation

The user (a fintech-platform-grade investor) currently computes these metrics by hand against Yahoo Finance balance-sheet data:

| User cell | Formula | Meaning |
|---|---|---|
| M11 | `Total Assets ÷ Shares` | Asset-side floor |
| (informal) | `Current Assets ÷ Shares` | Liquid-asset floor |
| L8 | `Price ÷ Floor × 1.2` | Margin-of-safety check |

The Midas API already has all the inputs (`CurrentAssets`, `StockholdersEquity`, `TotalAssets`, `DilutedSharesOutstanding`, `marketData.SharePrice`). Surfacing the metrics in-response removes the manual Excel step and standardises on the **proper Graham conventions**:

- Net Current Asset Value = `CurrentAssets − TotalLiabilities` (not just total assets).
- "Buy below" trigger = NCAV × 2/3 (Graham's classic margin), not the user's ×1.2 inversion.
- Shares basis = diluted (consistent with the rest of Midas's per-share output).

Reference: Benjamin Graham, *Security Analysis* (1934), ch. on "net-net" stocks.

---

## 3. Requirements

### 3.1 Functional

| # | Requirement |
|---|---|
| F-1 | Response carries four new per-share fields: `current_assets_per_share`, `ncav_per_share`, `graham_floor_per_share`, `graham_discount_pct`. |
| F-2 | `ncav_per_share` may be **negative** and is returned as the raw value (no clamping). |
| F-3 | `graham_floor_per_share = max(ncav_per_share × 2/3, 0)` — clamps at 0 when NCAV is negative. |
| F-4 | `graham_discount_pct` returns **`null` (omitted via `omitempty`)** when `graham_floor_per_share == 0`, to avoid a divide-by-zero false signal. |
| F-5 | All four fields are **omitted entirely** when `TotalLiabilities` cannot be resolved (see §4.4). A warning string is appended to `result.Warnings` in that branch. |
| F-6 | `tangible_value_per_share` denominator switches from `market.SharesOutstanding` (basic) to diluted shares using the same priority chain already used for DCF (`diluted → market.basic → financial.basic`). |
| F-7 | All four metrics are computed on both the standard DCF path and the alternative-model path (DDM, FFO, revenue_multiple). |

### 3.2 Non-functional

| # | Requirement |
|---|---|
| NF-1 | Test coverage on the new computation function meets the project floor of ≥90% for finance modules (per CLAUDE.md "Coverage target"). |
| NF-2 | OpenAPI schema (`docs/openapi.yaml`) and `docs/API_DOCUMENTATION.md` are updated in the same change set. |
| NF-3 | No new external API calls; metrics are O(1) over already-fetched data — performance impact ≈ zero. |
| NF-4 | No change to the model router, growth estimator, WACC, terminal value, sanity-check, or any forward-looking math. |
| NF-5 | Persistence: 4 new columns added to `valuation_results` table via a forward-only migration (`migrations/0008_add_graham_floor_columns.sql`). The columns are `NULL`able to avoid breaking the read path on warm rows from before the migration. |

---

## 4. Design

### 4.1 Field definitions

> **All four fields are `*float64` (pointer to float64) with `omitempty`.** This is a deliberate choice: a `nil` pointer (field absent from JSON) means "TotalLiabilities couldn't be resolved — no answer." A non-nil pointer to `0.0` (field present in JSON as `0`) means "we computed the answer and the answer is zero" (e.g. floor clamped because NCAV is negative). Plain `float64 + omitempty` would silently collapse those two semantically different states into the same wire shape — see §10 R5 below for the full reasoning.

| Field | Type | Formula | Units | Null behaviour | Example (Sheet2 healthy stock) | Example (MXL distressed, resolved) |
|---|---|---|---|---|---|---|
| `current_assets_per_share` | `*float64` | `CurrentAssets / DilutedShares` | USD/share | `nil` ⇒ omitted only when `TotalLiabilities` can't be resolved (§4.4) or shares ≤ 0 | `2,180,000,000 / 39,540,000 = 55.13` | `249,450,000 / 87,595,000 = 2.85` |
| `ncav_per_share` | `*float64` | `(CurrentAssets − TotalLiabilities) / DilutedShares` | USD/share | same as above; **may be negative** when populated | `(2,180,000,000 − 2,000,000,000) / 39,540,000 = 4.55` | `(249,450,000 − 316,450,000) / 87,595,000 = −0.765` |
| `graham_floor_per_share` | `*float64` | `max(ncav_per_share × 2/3, 0)` | USD/share | same as above; **`&0.0` stays in JSON** when present (clamped from negative NCAV) | `4.55 × 2/3 = 3.03` | `max(−0.51, 0) = 0` (present as `0`) |
| `graham_discount_pct` | `*float64` | `(current_price − graham_floor_per_share) / graham_floor_per_share` | ratio (`0.10` = 10% above floor) | **`nil` / omitted** when `graham_floor_per_share == 0` OR `current_price == 0` OR liabilities unresolved | `(73.64 − 3.03) / 3.03 = 23.30` | omitted (floor is `0`) |

> Sign convention for `graham_discount_pct`: **positive = price above floor (normal/overvalued)**, **negative = price below floor (deep value / Graham net-net territory)**. This matches the `(dcf_value − price) / price` convention used elsewhere only in shape; readers should consult the description text in OpenAPI before reasoning about it.

> **Three distinguishable wire shapes** — the contract this design produces:
> - **Healthy** (resolved + positive NCAV): all four fields present with positive values.
> - **Deep distress** (resolved + negative NCAV → floor clamped): first three present (`ncav_per_share` negative, `graham_floor_per_share: 0`), fourth absent.
> - **Unresolved** (TotalLiabilities can't be sourced): all four absent + a `graham_floor:` warning string in `warnings`.

### 4.2 Computation site

All four metrics are computed by a single new private helper:

```go
// internal/services/valuation/graham.go (new file)
package valuation

import "github.com/midas/dcf-valuation-api/internal/core/entities"

// grahamFloor holds the four Graham-school per-share diagnostics. All values
// are in USD on a per-diluted-share basis.
type grahamFloor struct {
    CurrentAssetsPerShare float64
    NCAVPerShare          float64
    GrahamFloorPerShare   float64
    GrahamDiscountPct     *float64 // nil when GrahamFloorPerShare == 0
    Warnings              []string
}

// calculateGrahamFloorMetrics is a pure function: no I/O, no logging side
// effects. Caller (service.CalculateValuation) is responsible for stamping
// the resulting values onto ValuationResult and appending warnings.
func calculateGrahamFloorMetrics(
    fd *entities.FinancialData,
    sharesOutstanding float64,
    currentPrice float64,
) grahamFloor {
    // implementation per §4.3
}
```

**Wiring sites** in `internal/services/valuation/service.go`:

1. **DCF path** — call after `dcfValuePerShare` is computed (current line ~1067), before the `result := &entities.ValuationResult{...}` literal at line 1095. Stamp the four fields onto the literal.
2. **Alternative-model path** — call inside `performAlternativeValuation` after `modelResult` succeeds, before the `result := &entities.ValuationResult{...}` literal at line 1295. Stamp the four fields onto the literal.

Both call sites already have `latestFinancialData`, `sharesOutstanding`, and `marketData.SharePrice` in scope, so no new arguments thread through.

### 4.3 Algorithm (pseudocode)

```
function calculateGrahamFloorMetrics(fd, shares, price):
    out = grahamFloor{}

    // Hard guard: divisor.
    if shares <= 0:
        return out  // all zeros; caller treats as "skip emission"

    // Resolve TotalLiabilities (see §4.4 fallback chain).
    tl, ok = resolveTotalLiabilities(fd)
    if not ok:
        out.Warnings = ["graham_floor: insufficient balance-sheet data (total_liabilities unresolved)"]
        return out

    out.CurrentAssetsPerShare = fd.CurrentAssets / shares

    netCurrent = fd.CurrentAssets - tl
    out.NCAVPerShare = netCurrent / shares  // may be negative

    floor = (netCurrent / shares) * (2.0 / 3.0)
    if floor < 0:
        out.GrahamFloorPerShare = 0
    else:
        out.GrahamFloorPerShare = floor

    if out.GrahamFloorPerShare > 0 and price > 0:
        d = (price - out.GrahamFloorPerShare) / out.GrahamFloorPerShare
        out.GrahamDiscountPct = &d
    // else: leave nil → JSON omitempty drops the field

    return out
```

### 4.4 Resolving `TotalLiabilities` — the sourcing decision

This was the highest-risk question in the brief. The current state:

- **Parser side:** `internal/infra/gateways/sec/parser.go` line 966 already lists `us-gaap:Liabilities` (and `LiabilitiesCurrent` / `LiabilitiesNoncurrent`) in the requested-tags array. The SEC fetch *does pull the value*.
- **Entity side:** `entities.FinancialData` (per `internal/core/entities/financial_data.go`) has `TotalAssets`, `CurrentAssets`, `CurrentLiabilities`, `StockholdersEquity`, `OperatingLeaseLiability`, `PensionLiabilities`, etc. — but **no `TotalLiabilities` field**. The parsed value is therefore discarded today.
- **Datacleaner side:** `config/datacleaner/xbrl_tag_mappings.json` references `us-gaap:Liabilities`, but no downstream FinancialData field is populated.

The MXL inconsistency reported in the brief (`stockholders_equity: 454,191,000`, `total_assets: 387,402,066` ⇒ implied liabilities ≈ −$67M) is not a transient bug — it is a real data-quality issue caused by the datacleaner's adjusters mutating one balance-sheet term but not the other. Surfacing NCAV will make this visible. That is a **feature**, not a bug, of this spec (see §10 risk discussion).

#### Decision: **Option B — add a first-class `TotalLiabilities` field, populate from XBRL, fall back to derivation.**

Fallback chain (executed in `resolveTotalLiabilities`):

1. **Direct** — if `fd.TotalLiabilities > 0`, use it (set by the SEC parser via `us-gaap:Liabilities` / `ifrs-full:Liabilities`).
2. **Sum of components** — if the umbrella tag was missing but `fd.LiabilitiesCurrent > 0` AND a non-current component is available, sum them. (Implementation detail: this requires either adding a `LiabilitiesNoncurrent` field or summing `CurrentLiabilities + OperatingLeaseLiability + PensionLiabilities`. **Recommend skipping this branch in v1** — it adds complexity for marginal coverage gain. The umbrella tag is filed by virtually every US issuer.)
3. **Derived** — if `fd.TotalAssets > 0 && fd.StockholdersEquity > 0`, compute `derived = fd.TotalAssets − fd.StockholdersEquity`. Use `derived` only when it is positive; emit a `WARN`-level log via `logctx.From(ctx)` with `zap.Float64("derived_total_liabilities", derived)` when the derivation path fires (so operators can correlate against data-cleaner adjustments). When `derived ≤ 0`, treat as unresolved.
4. **Unresolved** — return `(0, false)`. Caller emits warning F-5 and the four new fields drop from the response.

#### Rejected alternatives

| Option | Why rejected |
|---|---|
| **A. Derive only (`TotalAssets − StockholdersEquity`)** | Cheap, but every cleaner adjustment that touches assets-only or equity-only contaminates the derived value. MXL is the canonical case. Direct XBRL is preferred when available. |
| **C. Sum component liabilities (current + non-current + leases + pensions)** | Accurate when umbrella missing, but the components overlap (e.g. operating leases are inside `LiabilitiesNoncurrent` for some filers, separate for others). Risk of double-count outweighs the rare miss. |
| **D. Add the field but populate via the datacleaner adjusters** | Adjusters mutate the value (e.g. capitalised-lease additions) — for NCAV we want the *as-reported* number. Bypassing the cleaner keeps the floor honest. |

### 4.5 `tangible_value_per_share` denominator flip

Current code (`service.go` lines 1322–1338):

```go
func (s *Service) calculateTangibleValuePerShare(financial *entities.FinancialData, market *entities.MarketData) float64 {
    tangibleEquity := financial.TangibleAssets
    shares := market.SharesOutstanding
    if shares <= 0 {
        shares = financial.SharesOutstanding
    }
    if shares <= 0 { return 0 }
    return tangibleEquity / shares
}
```

Replace with the same priority chain used for the DCF path at line 862:

```
diluted (financial.DilutedSharesOutstanding) → market.basic → financial.basic → 0
```

This is a **breaking change** to the field's numeric output. Empirical delta is typically <5% (diluted exceeds basic by issued options/RSUs/convertibles). The change is intentional for cross-field consistency: every other per-share number in the response (DCF, NCAV, current_assets_per_share, graham_floor) uses diluted.

#### Migration approach for cached values

Decision: **No backfill. Stale values self-heal on next request.**

- The `valuation_results` table is read-only-cache today (no live code path consumes it; verified by grep — only `schema.sql` and docs reference it).
- The in-memory / Redis cache on `valuation:` keys has a TTL; expired entries recompute on next call.
- Any caller comparing the previous cached number against the new one will see at most a 5% jump — documented in the changelog and §10.

#### Rejected alternative

- **Add a new `tangible_value_per_share_diluted` field, leave the old one alone.** Rejected because the v0.9.0-rc1 contract is small and we do not want two near-identical per-share fields with subtly different semantics. The existing field's name (`tangible_value_per_share`) does not commit to "basic" — and the brief explicitly approved the in-place flip.

### 4.6 Persistence

`internal/infra/database/schema.sql` — extend the `valuation_results` table:

```sql
-- columns appended after dcf_value_per_share
current_assets_per_share DECIMAL(12,4),
ncav_per_share DECIMAL(12,4),         -- may be negative; SQLite DECIMAL is nullable
graham_floor_per_share DECIMAL(12,4),
graham_discount_pct DECIMAL(8,6) NULL, -- explicit NULL semantics for the omitted case
```

Migration `migrations/0008_add_graham_floor_columns.sql`:

```sql
-- Migration 0008: Add Graham-school asset-floor diagnostic columns.
-- Forward-only ALTER TABLE statements; cmd/migrate's applyMigration tolerates
-- "duplicate column name" errors when schema.sql already defines them on a
-- fresh database, so the same SQL works for both upgrade and clean-install paths.

ALTER TABLE valuation_results ADD COLUMN current_assets_per_share DECIMAL(12,4);
ALTER TABLE valuation_results ADD COLUMN ncav_per_share DECIMAL(12,4);
ALTER TABLE valuation_results ADD COLUMN graham_floor_per_share DECIMAL(12,4);
ALTER TABLE valuation_results ADD COLUMN graham_discount_pct DECIMAL(8,6);
```

Pattern matches `migrations/0006_add_minority_interest_preferred_equity.sql` and `migrations/0007_add_reporting_currency.sql`.

`financial_data` table also needs a new `total_liabilities` column for replay/persistence parity. Append to migration `0008`:

```sql
ALTER TABLE financial_data ADD COLUMN total_liabilities DECIMAL(15,2);
```

### 4.7 Folder structure (delta only)

```
internal/
  core/entities/
    valuation.go          # +4 fields on ValuationResult
    financial_data.go     # +1 field on FinancialData (TotalLiabilities)
  services/valuation/
    service.go            # 2 wiring sites (DCF + alt-model paths) + denominator flip
    graham.go             # NEW — calculateGrahamFloorMetrics + resolveTotalLiabilities
    graham_test.go        # NEW — table-driven unit tests
  infra/gateways/sec/
    parser.go             # 1 new findValue block writing fd.TotalLiabilities
  api/v1/handlers/
    fair_value.go         # +4 fields on FairValueResponse + handler shaping (single + bulk)
  infra/database/
    schema.sql            # +4 cols on valuation_results, +1 col on financial_data
migrations/
  0008_add_graham_floor_columns.sql   # NEW
docs/
  openapi.yaml            # +4 properties on FairValueResponse schema
  API_DOCUMENTATION.md    # Response section update
```

---

## 5. API Contracts

### 5.1 OpenAPI snippet (insert into `FairValueResponse` schema in `docs/openapi.yaml` after `dcf_value_per_share`)

> **All four fields use `nullable: true`.** This matches the Go-side `*float64 + omitempty` decision (§4.1) — a `null` (omitted) value is a load-bearing signal meaning "TotalLiabilities couldn't be resolved", distinct from a present-but-zero value meaning "we computed it and the answer is zero".

```yaml
        current_assets_per_share:
          type: number
          format: float
          nullable: true
          example: 55.13
          description: |
            Current assets ÷ diluted shares. Pure asset-side floor with no
            liability subtraction. Useful as an upper bound on the "what would
            shareholders get in liquidation if non-current assets vanished"
            scenario. Returned as null (omitted from JSON) when
            total_liabilities cannot be resolved (a warning is added to
            `warnings`).
        ncav_per_share:
          type: number
          format: float
          nullable: true
          example: 4.55
          description: |
            Net Current Asset Value per share — Graham's classic deep-value
            metric: (current_assets − total_liabilities) ÷ diluted_shares. May
            be **negative** when liabilities exceed current assets; the raw
            value is returned (no clamping). Returned as null (omitted) when
            total_liabilities cannot be resolved.
        graham_floor_per_share:
          type: number
          format: float
          nullable: true
          example: 3.03
          description: |
            "Buy below" trigger per Graham — max(ncav_per_share × 2/3, 0).
            **The value 0 stays in JSON when present** — clamped from a
            negative NCAV — and represents "no asset floor exists, the
            company has more obligations than liquid assets". Returned as
            null (omitted) only when total_liabilities cannot be resolved
            (distinct signal: data unavailable vs. data says zero).
        graham_discount_pct:
          type: number
          format: float
          nullable: true
          example: 23.30
          description: |
            Ratio (current_price − graham_floor_per_share) ÷
            graham_floor_per_share. **Positive = price above the floor**
            (normal market pricing or overvalued vs. floor); **negative =
            price below the floor** (Graham net-net territory). Returned as
            null (omitted from JSON) when graham_floor_per_share is 0
            (divide-by-zero would otherwise produce a meaningless ±Inf
            signal) or when current_price is unavailable.
```

### 5.2 Sample response — Sheet2 healthy stock

```json
{
  "ticker": "EXAMPLE",
  "wacc": 0.092,
  "growth_rate": 0.045,
  "tangible_value_per_share": 38.20,
  "dcf_value_per_share": 84.50,
  "current_assets_per_share": 55.13,
  "ncav_per_share": 4.55,
  "graham_floor_per_share": 3.03,
  "graham_discount_pct": 23.30,
  "current_price": 73.64,
  "currency": "USD",
  "as_of": "2026-05-05T12:00:00Z"
}
```

### 5.3 Sample response — MXL-style distressed (negative NCAV, **resolved**)

This is the "deep distress" wire shape. TotalLiabilities was resolved successfully (no `graham_floor:` warning), but the calculated NCAV is negative, so the floor clamps to `0`. The first three fields are present (with `ncav_per_share` carrying the negative signal); `graham_discount_pct` is absent because dividing by a zero floor would produce a meaningless infinity.

```json
{
  "ticker": "MXL",
  "wacc": 0.105,
  "growth_rate": 0.030,
  "tangible_value_per_share": 1.20,
  "dcf_value_per_share": 12.40,
  "current_assets_per_share": 2.85,
  "ncav_per_share": -0.765,
  "graham_floor_per_share": 0,
  "current_price": 11.20,
  "currency": "USD",
  "as_of": "2026-05-05T12:00:00Z"
}
```

> Note: `graham_discount_pct` is **absent** from the JSON because `graham_floor_per_share == 0`. `graham_floor_per_share: 0` is **present** because the value is a non-nil `*float64` pointing to `0.0` — the "data says zero" signal, distinct from the "data unavailable" signal in §5.4 below.

### 5.3.1 Sample response — MXL-style **unresolved** (cleaner asymmetry hits the derivation fallback)

This is the alternative MXL wire shape. The SEC parser couldn't find `us-gaap:Liabilities` AND the derivation fallback `TotalAssets − StockholdersEquity` produced a negative number (the cleaner-asymmetry signature documented in DC-1). All four Graham fields are absent, replaced by exactly one warning. Distinguishable from the deep-distress shape above: zero Graham keys present + warning, vs. three Graham keys present + no warning.

```json
{
  "ticker": "MXL",
  "wacc": 0.105,
  "growth_rate": 0.030,
  "tangible_value_per_share": 1.20,
  "dcf_value_per_share": 12.40,
  "current_price": 11.20,
  "currency": "USD",
  "as_of": "2026-05-05T12:00:00Z",
  "warnings": [
    "graham_floor: insufficient balance-sheet data (total_liabilities unresolved)"
  ]
}
```

> The choice between the §5.3 shape (deep-distress, resolved) and the §5.3.1 shape (unresolved) depends on whether the SEC parser found the umbrella `us-gaap:Liabilities` / `ifrs-full:Liabilities` tag for that ticker. Most US 10-K filers populate it; a minority don't, and for those the derivation fallback is the only option — and it's only used when the result is positive.

### 5.4 Backward compatibility

All four fields are additive. `tangible_value_per_share` keeps its name but its numeric value will shift slightly (typically −2% to −5%) — see §10 risk R1. No existing field is removed or renamed.

---

## 6. Module descriptions

### 6.1 `internal/services/valuation/graham.go` (new)

Pure-function module: no fx wiring, no I/O, no logger dependency. Exposes `calculateGrahamFloorMetrics` (package-private). Contains `resolveTotalLiabilities` as a private helper. Single-purpose, easy to unit-test, no DI surface area.

### 6.2 `internal/core/entities/financial_data.go` (modify)

Add one field, immediately after the existing `CurrentLiabilities` field at line 103:

```go
// TotalLiabilities is the as-reported balance-sheet line "total liabilities"
// (us-gaap:Liabilities / ifrs-full:Liabilities). Populated by the SEC parser
// when the umbrella XBRL tag is present; left at zero otherwise. Distinct
// from CurrentLiabilities (the short-term subset). Used by the Graham-floor
// diagnostic computation in internal/services/valuation/graham.go; not
// consumed by the DCF or alt-model engines.
TotalLiabilities float64 `json:"total_liabilities"`
```

### 6.3 `internal/infra/gateways/sec/parser.go` (modify)

Add one `findValue` block immediately after the existing `CurrentLiabilities` block at line 605–611:

```go
if val, exists := p.findValue(data, []string{
    "Liabilities",
    // IFRS-full equivalent — most filers use "Liabilities" identically; some
    // use "TotalLiabilitiesAndEquity" minus equity, but we do not derive here.
}); exists {
    financialData.TotalLiabilities = val
}
```

The umbrella tag was already in the requested-tags list (line 966), so the on-the-wire response already carries it; this change just plumbs the parsed value onto FinancialData.

### 6.4 `internal/services/valuation/service.go` (modify)

Two wiring blocks (DCF path ~line 1095, alt-model path ~line 1295) and one denominator flip (`calculateTangibleValuePerShare` at ~line 1322).

### 6.5 `internal/api/v1/handlers/fair_value.go` (modify)

Two struct field additions (`FairValueResponse`) and two response-shape constructions (single-handler at line 376, bulk-handler at line 516).

---

## 7. Tasks by Agent

### BACKEND

| ID | Task | Estimated effort | Files |
|---|---|---|---|
| **B1** | Add 4 fields to `entities.ValuationResult` (`CurrentAssetsPerShare`, `NCAVPerShare`, `GrahamFloorPerShare`, `GrahamDiscountPct *float64`). Mirror existing field-tag conventions (omitempty where appropriate; `GrahamDiscountPct` MUST use pointer + `omitempty` for the null behaviour). | XS | `internal/core/entities/valuation.go` |
| **B2** | Add `TotalLiabilities` to `entities.FinancialData` and populate from `us-gaap:Liabilities` in the SEC parser. The XBRL tag is already in the requested-tags list — only the field plumbing is missing. | S | `internal/core/entities/financial_data.go`, `internal/infra/gateways/sec/parser.go` |
| **B3** | Create `internal/services/valuation/graham.go` with `calculateGrahamFloorMetrics` (signature in §4.2) and `resolveTotalLiabilities`. Wire the call into `service.go` at both the DCF path (~line 1095) and the alt-model path (~line 1295). Stamp the four returned values onto the `ValuationResult` literal. Append warnings via `result.Warnings = append(result.Warnings, gf.Warnings...)`. | M | `internal/services/valuation/graham.go` (new), `internal/services/valuation/service.go` |
| **B4** | Flip `calculateTangibleValuePerShare` denominator from `market.SharesOutstanding`-first to `financial.DilutedSharesOutstanding`-first, matching the share-resolution chain at `service.go` lines 862–873. Single-line semantic change inside the helper. | XS | `internal/services/valuation/service.go` |
| **B5** | Add 4 fields to `FairValueResponse` (mirror tags from `ValuationResult`). Update both the single-ticker handler (`GetFairValue` ~line 376) and the bulk handler (`GetBulkFairValue` ~line 516) to copy the values from `result` into the response struct. | S | `internal/api/v1/handlers/fair_value.go` |
| **B6** | Add 4 columns to `valuation_results` and 1 column (`total_liabilities`) to `financial_data` in `schema.sql`. Author `migrations/0008_add_graham_floor_columns.sql` containing the `ALTER TABLE` statements (forward-only; relies on `applyMigration` duplicate-column tolerance). | S | `internal/infra/database/schema.sql`, `migrations/0008_add_graham_floor_columns.sql` (new) |
| **B7** | Update `docs/openapi.yaml` `FairValueResponse` schema with the 4 new properties (snippet in §5.1). | XS | `docs/openapi.yaml` |
| **B8** | Update `docs/API_DOCUMENTATION.md` Response section with the new field descriptions, the diluted-shares note for `tangible_value_per_share`, and a worked example covering the Sheet2 healthy and MXL distressed cases. | S | `docs/API_DOCUMENTATION.md` |
| **B9** | Author tests (see §9). At minimum: 6-row table-driven unit test for `calculateGrahamFloorMetrics`, 1 unit test for `resolveTotalLiabilities` covering all 4 fallback branches, 1 integration test asserting JSON-shape with omitempty behaviour for the divide-by-zero case, 1 regression test pinning the new `tangible_value_per_share` denominator. | M | `internal/services/valuation/graham_test.go` (new), `internal/api/v1/handlers/fair_value_test.go`, `internal/integration/...` |

### QA
- Verify `current_assets_per_share` matches `CurrentAssets ÷ DilutedShares` to 4 decimal places on a sample of 10 tickers (AAPL, MSFT, JNJ, KO, T, F, AMD, MXL, TSM, BABA — covers domestic + ADR + distressed).
- Verify the response **omits** `graham_discount_pct` for any ticker where `graham_floor_per_share == 0`. JSON Schema validation should surface this.
- Verify the warning message fires exactly once when `total_liabilities` is unresolved.
- Verify `tangible_value_per_share` matches the new denominator (manual cross-check against the SEC filing's diluted share count).
- Run a contract-fuzz pass (`./scripts/contract_fuzz.ps1`) to confirm no regressions.

### REVIEWER
- Confirm `graham.go` is purely a function file — no DI, no fx provider, no global state.
- Confirm both wiring sites (DCF + alt-model) populate the four fields — a missed alt-model path means DDM/FFO/Revenue-Multiple tickers silently lose the diagnostic block.
- Confirm `GrahamDiscountPct` field uses `*float64` + `omitempty` (not `float64` + `omitempty`, which would also drop `0.0` and corrupt healthy-stock responses where the floor is exactly equal to the price).
- Confirm migration ordering: `0008_add_graham_floor_columns.sql` after `0007_add_reporting_currency.sql`.
- Spot-check the `TotalLiabilities` derivation fallback — ensure the WARN log carries `ticker` so operators can audit which tickers fall into the derived branch.
- Confirm the breaking-change call-out for `tangible_value_per_share` lands in `CHANGELOG.md` and `CLAUDE.md`'s "Common Gotchas" section.

### UX_UI / FRONTEND
- N/A. No UI surface in this repo.

---

## 8. Spec updates

| File | Change |
|---|---|
| `CLAUDE.md` § "Common Gotchas" | Add a bullet: *"`tangible_value_per_share` denominator changed from market-basic to diluted shares in v0.10.0 (Graham floor metrics). Cached pre-v0.10.0 values may be ~2-5% higher; expect drift on first recompute."* |
| `CHANGELOG.md` (or whatever release-notes file the project uses for v0.10.x) | Note the four new fields under "Added" and the denominator flip under "Changed (breaking)". |
| `docs/openapi.yaml` | §5.1 snippet. |
| `docs/API_DOCUMENTATION.md` | Add a "Graham Floor Metrics" subsection under Response. Quote the four field descriptions and one Sheet2 + one MXL example. |
| `docs/THESIS.md` | No update required (this is a transparency add-on, not a phase milestone). |

---

## 9. Tests

### 9.1 Required unit cases for `calculateGrahamFloorMetrics`

| # | Scenario | Inputs | Expected |
|---|---|---|---|
| U1 | Positive NCAV, healthy stock | `CurrentAssets=2.18B`, `TotalLiab=2.0B`, `Diluted=39.54M`, `Price=73.64` | `current_assets_per_share≈55.13`, `ncav_per_share≈4.55`, `floor≈3.03`, `discount≈23.30` (non-nil) |
| U2 | Negative NCAV, distressed (MXL-style) | `CurrentAssets=249.45M`, `TotalLiab=316.45M`, `Diluted=87.595M`, `Price=11.20` | `ncav_per_share≈−0.765`, `floor==0`, `discount==nil` |
| U3 | Zero diluted shares | `Diluted=0` | All fields zero, `Warnings == nil`, no panic. (Caller will still set the omitempty-tagged fields to zero — but the brief is to NOT emit a warning here, since the same guard already fires earlier in the engine for `ErrInsufficientData`.) |
| U4 | TotalLiabilities directly available | `fd.TotalLiabilities = 1B` | Uses direct path, no derivation log. |
| U5 | TotalLiabilities derivation path | `fd.TotalLiabilities=0`, `fd.TotalAssets=2B`, `fd.StockholdersEquity=1.5B` | Uses `2B − 1.5B = 0.5B`; warning string includes "derived". |
| U6 | TotalLiabilities unresolved | `fd.TotalLiabilities=0`, `fd.TotalAssets=0`, `fd.StockholdersEquity=0` | Returns zeros, single warning `"graham_floor: insufficient balance-sheet data (total_liabilities unresolved)"`. |
| U7 | Negative derived liabilities (MXL signature) | `fd.TotalLiabilities=0`, `fd.TotalAssets=387M`, `fd.StockholdersEquity=454M` | Derived = `−67M` ⇒ treated as unresolved (same as U6). |
| U8 | Floor == price (boundary) | `floor=10`, `price=10` | `discount == 0` (non-nil pointer). Confirms pointer-vs-value distinction. |
| U9 | Floor > 0, price == 0 (delisted) | `floor=3`, `price=0` | `discount == nil` (per the algorithm: `price > 0` guard). |

Use `testify/assert` with the table-driven `[]struct{name string; ...}` pattern per CLAUDE.md "Testing".

### 9.2 Integration test

`internal/integration/graham_floor_response_test.go`:
- Stand up the in-process HTTP server with a mocked `ValuationCalculator` returning a fixed `*entities.ValuationResult` with `GrahamFloorPerShare = 0`.
- Hit `GET /api/v1/fair-value/EXAMPLE` and assert the JSON body **does not contain the key `graham_discount_pct`**.
- Mirror the test for `GrahamFloorPerShare > 0` and assert the key IS present and equals the expected ratio.

### 9.3 Snapshot / golden test

Add a JSON golden under `internal/api/v1/handlers/testdata/fair_value_response_with_graham.golden.json` covering the Sheet2 healthy case in §5.2. The test compares the marshalled response byte-for-byte (sorting JSON keys for stability).

### 9.4 Regression for the denominator flip

Add a single test row in the existing `service_test.go` that builds a `*entities.FinancialData` with `DilutedSharesOutstanding != SharesOutstanding` and asserts `TangibleValuePerShare == TangibleAssets / DilutedSharesOutstanding`.

### 9.5 Coverage target

Per CLAUDE.md, finance-critical modules must hit ≥90%. Run `go test -cover ./internal/services/valuation/...` and confirm the new file's coverage is ≥90% before merge. Test all 9 unit cases above (Graham floor) plus the 4 fallback branches (`resolveTotalLiabilities`).

---

## 10. Risks and trade-offs

### R1 — `tangible_value_per_share` is a breaking numeric change

- **Impact:** any downstream consumer comparing pre-v0.10.0 vs post-v0.10.0 values will see ~2–5% drift (diluted shares ≥ basic shares). Strictly speaking, no client should pin exact values from a valuation API, but this needs flagging.
- **Mitigation:** call out in `CHANGELOG.md` and `CLAUDE.md` "Common Gotchas". The change is **opt-in to consistency** with every other per-share field — keeping the old behaviour would be the harder thing to defend.
- **Alternative considered + rejected:** add `tangible_value_per_share_diluted` as a parallel field. Rejected: doubles the API surface for marginal benefit.

### R2 — NCAV will surface latent data-quality issues

- **Impact:** the MXL case (assets=$387M, equity=$454M ⇒ implied liabilities=−$67M) will *visibly* produce a sentinel response (warning + omitted fields) rather than silently producing a wrong number elsewhere. Users who notice will either (a) appreciate the diagnostic, or (b) file a bug demanding the cleaner be fixed.
- **Mitigation:** the warning message is precise — it names `total_liabilities unresolved` and (in the derived branch) `derived from total_assets − stockholders_equity`, so the user knows where to look. The deeper fix (datacleaner-adjuster atomicity) is out of scope here; track it as a follow-up.
- **Alternative considered + rejected:** clamp negative derived liabilities to 0. Rejected: produces a fake floor and hides the data error. The four-field-omission sentinel is honest.

### R3 — Diluted vs basic philosophy on NCAV

- **Impact:** classical Graham (1934) used basic shares; we use diluted. Ratios will be marginally more conservative (smaller per-share NCAV) than the textbook benchmark.
- **Mitigation:** documented in the OpenAPI description (`per diluted share`). For consistency with the rest of the response (DCF, revenue-multiple model, equity-bridge per-share), diluted is the correct choice for Midas. This is a deliberate departure noted in CLAUDE.md gotchas.

### R4 — Cache invalidation for the denominator flip

- **Impact:** existing `valuation:*` Redis / in-memory cache entries will continue to serve old values until their TTL expires. Stale values will be inconsistent with NCAV and current_assets_per_share. Worst case: a caller sees a per-share field returned at cached basic-shares precision while the new fields use diluted.
- **Mitigation:** TTL is short on this cache (per `internal/services/valuation/service.go` cache settings) — drift window is bounded. Operators wanting an immediate flush can `FLUSHDB` Redis on rollout.
- **Alternative considered + rejected:** bump `CalculationVersion` to "5.0" and key the cache on it. Rejected: out of scope; the version bump should be batched with a larger semantic change.

---

## 11. Implementation roadmap

Suggested sequence (each step is independently mergeable):

1. **B2** — Add `TotalLiabilities` field + parser plumbing. Write parser test. Merge.
2. **B6** — Schema + migration. Merge.
3. **B1** — Add the 4 fields to `ValuationResult`. Merge.
4. **B3** — Create `graham.go` + tests. Wire into both code paths in `service.go`. Merge.
5. **B4** — Denominator flip. Merge with one regression test.
6. **B5** — Handler shaping (single + bulk). Merge with integration test.
7. **B7** + **B8** — OpenAPI + API docs. Merge.
8. **B9** — Final test sweep, hit ≥90% coverage, contract-fuzz pass.

The split keeps each PR small and reviewable. Steps 1–3 are pure additions and risk-free; step 4 is the breaking change and benefits from being its own commit for `git revert` ergonomics.

---

## 12. Potential challenges

| Challenge | Mitigation |
|---|---|
| **Foreign issuers (IFRS-full filers like TSM, ASML)** report under `ifrs-full:Liabilities`. Initial implementation only checks `us-gaap:Liabilities` — IFRS filers will fall through to the derivation branch. | Add `ifrs-full:Liabilities` to the parser's `findValue` slice in B2 (the `findValue` helper supports namespaced lookup; see how `Equity` / `EquityAttributableToOwnersOfParent` are already paired in the StockholdersEquity block at line 626). Cost: one line. |
| **Datacleaner adjusters mutate `TotalAssets` and `StockholdersEquity` non-atomically.** The derivation path will produce wrong numbers when the cleaner has applied capitalised-lease additions to assets but not the offsetting liability adjustment. | Keep the derivation as best-effort fallback only and emit the WARN log so operators can audit. The proper fix lives in the datacleaner refactor (out of scope). |
| **Bulk endpoint** has its own response-shaping block at line 516 — easy to forget. | Reviewer item: confirm both single + bulk handler paths populate the four fields. Snapshot test in B9 covers the bulk case. |
| **`graham_discount_pct` pointer null behaviour** is subtle: `*float64` with `omitempty` — `nil` drops, `&0.0` keeps. Any backend dev tempted to use `float64` with a sentinel `-1` will break the contract. | Spec explicit on field type. Reviewer item flagged. Test U8 (floor == price) pins the `&0.0` case. |

---

## 13. Acceptance criteria

| # | Criterion | Verifiable by |
|---|---|---|
| AC-1 | `GET /api/v1/fair-value/AAPL` returns the 4 new fields with non-zero values. | QA (manual call) |
| AC-2 | `GET /api/v1/fair-value/MXL` returns a response where `graham_floor_per_share == 0` and `graham_discount_pct` is **absent** from the JSON. Response body validates against the updated OpenAPI schema. | Automated integration test |
| AC-3 | OpenAPI schema in `docs/openapi.yaml` validates and contains all 4 new properties with correct types and the `nullable: true` flag on `graham_discount_pct`. | `go run ./cmd/server` + Swagger UI render |
| AC-4 | `tangible_value_per_share` for AAPL (or any large-cap with options outstanding) differs from the pre-merge value by 1–5% in the diluted direction. | Regression test pinning the formula |
| AC-5 | New unit test file achieves ≥90% line coverage. | `go test -cover ./internal/services/valuation/...` |
| AC-6 | Contract-fuzz script (`./scripts/contract_fuzz.ps1`) passes on a clean build. | CI |
| AC-7 | `go run ./cmd/migrate -db <fresh.db>` applies cleanly; re-running on a populated DB does not error (duplicate-column tolerance). | Manual + integration test |
| AC-8 | Single-handler and bulk-handler responses both contain the four new fields when applicable. | Integration test for both endpoints |
| AC-9 | When `TotalLiabilities` cannot be resolved, the four fields are omitted and exactly one warning string of the documented format is appended to `warnings`. | Unit + integration test |

---

## 14. Out of scope

- NCAV-based screening endpoint (e.g. `GET /api/v1/screen/net-net?country=US`).
- Watchlist filters that auto-flag tickers when `graham_discount_pct < threshold`.
- Historical ratio time-series in `valuation_results` (we store the latest only).
- Fixing the datacleaner's non-atomic adjuster mutations (the root cause behind the MXL `assets < equity` anomaly).
- Adding a Graham model to `internal/services/valuation/models/router.go`. Diagnostics only — never substituted for the engine output.
- Changing how `tangible_value_per_share` is computed beyond the denominator flip (e.g. switching from `TangibleAssets` to `TangibleAssets − TotalLiabilities` for "tangible book value").

---

## 15. Open questions for HUMAN

| # | Question | Default if unanswered |
|---|---|---|
| Q1 | Should `graham_discount_pct` use **`*float64` + `omitempty` + `nullable: true`** (proposed) or **`float64` + a separate `graham_discount_available bool` flag**? Pointer is more idiomatic Go but breaks `0.0` round-tripping in some downstream JSON libs. | **Proceed with `*float64`.** |
| Q2 | Add `ifrs-full:Liabilities` lookup in B2 alongside `us-gaap:Liabilities` to give IFRS filers the floor metrics for free? Spec assumes **yes** (one extra string in the slice, zero risk). | **Yes — include both tags from day 1.** |
| Q3 | Bump `CalculationVersion` from `"4.0"` to `"4.1"` in the same PR, or keep at `"4.0"` because the math is unchanged? Spec is silent; recommend bump-on-merge so cache/replay tooling can detect the shape change. | **Bump to `"4.1"`.** |

If HUMAN does not respond, BACKEND should proceed with the defaults shown above.

---

## 16. Next steps

1. **HUMAN** approves spec (or answers the three open questions in §15).
2. **BACKEND** executes B1 → B9 in the order in §11.
3. **QA** runs the verification matrix in §7.
4. **REVIEWER** hits the checklist in §7.
5. **ARCH** updates `docs/THESIS.md` only if this work changes phase status (it does not).

**HANDOFF_TO: BACKEND**
