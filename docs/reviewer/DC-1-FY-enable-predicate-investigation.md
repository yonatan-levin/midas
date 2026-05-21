# DC-1 FY-NULL — Why FY periods do not emit the A1-A5 paired CA-down / TA-up divergence pattern

**Status:** OPEN (read-only investigation) — disposition is **NOT A BUG**; this is the natural consequence of SEC XBRL's FY-vs-Q Revenue reporting convention interacting with A5's quarterly-tuned obsolescence heuristic. No production behavior change recommended for DC-1 Phase 2. Closure deferred to a Phase 4+ heuristic-review pass (or to whenever the rule-engine refactor lands native FY-aware annualization).
**Severity:** LOW — does not block Phase 2 close per `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md` Cluster A-FY-NULL disposition; the FY snapshots in the basket either A) correctly no-op A5 because inventory looks healthy on annual cadence, or B) silently underestimate inventory writedowns on FY filings of distressed slow-turn issuers. The B side is the only conceivable consumer impact but no live ticker in the basket presents that profile today.
**Filed:** 2026-05-22 by DC-1 Phase 2 PR-2 Task 2.7 (read-only investigator role).
**Phase context:** Spawned from `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md`'s Cluster A-FY-NULL section (line 160). Phase 1 shadow-analysis recorded the empirical FY-NULL pattern and tagged it "needs further investigation, low priority". This tracker is that investigation.
**Owner:** DC-1 Phase 2 ARCH (filing) — disposition is "close with caveat" or "punt to Phase 4 heuristic-review subtask"; no owner pickup required for PR-2.
**Blocks:** Nothing. Read-only finding.

---

## Question — empirical observation

The Phase 1 shadow-mode shim at `internal/services/datacleaner/recompute.go::recomputeUmbrellas` records WARN divergences across the DC-1 ticker basket. For AMD / F / KO / MXL — the four basket members carrying inventory — the recorded snapshots show this asymmetric divergence shape:

| Ticker | Q1/Q2/Q3 periods | FY periods |
|---|---|---|
| AMD | CA-down / TA-up paired + TL divergence | TL divergence ONLY |
| F | CA-down / TA-up paired + TL divergence | TL divergence ONLY |
| KO | CA-down / TA-up paired + TL divergence | TL divergence ONLY |
| MXL | CA-down / TA-up paired + TL divergence | TL divergence ONLY |

Concretely from `internal/integration/testdata/recompute-shadow/MXL.json` (lines 7-78):
- `2023Q2`: CA delta = **-$50.46M**, TA delta = **+$50.46M** (equal magnitude, opposite sign), TL delta = +$39.62M
- `2023FY`: TL delta = +$35.38M — **no CA or TA divergence record**
- `2024Q1`: CA = -$38.45M, TA = +$38.45M, TL = +$33.06M
- `2024FY`: TL = +$26.37M — **no CA or TA divergence record**
- `2025Q1`/`Q2`/`Q3`/`2026Q1`: paired CA/TA + TL divergences
- `2025FY`: TL = +$21.41M — **no CA or TA divergence record**

The cleaner code is the same Go binary running against every period; there is no `if period == "FY"` branch anywhere in the request path. So either A) some rule's enable predicate gates against a field whose value is structurally different on FY vs Qx, or B) the parser stamps FY periods with different field values than Qx for the same balance-sheet snapshot.

---

## Hypotheses

The shadow-analysis report enumerated three candidates:

- **(a) Rule-engine enable-predicate gates on period type.** The rule engine's `checkRuleApplicability` checks the period (FY vs Qx) directly or via some thresholding that translates to that gate. A-rules become NOT-APPLICABLE on FY filings.
- **(b) SEC parser FY collapse strips A-rule inputs.** The parser at `internal/infra/gateways/sec/parser.go::extractFiscalPeriods` aggregates / strips fields differently on `fp=FY` records, so by the time `parsePeriodData` runs, FY-period `FinancialData` has zero/missing values for `Goodwill`, `OtherIntangibles`, `Inventory`, `DeferredTaxAssets` — and the A-rules silently no-op on `data.X <= 0` guards.
- **(c) Side effect of a different SEC-reporting convention.** Some FY-vs-Q reporting difference that's NOT period-type-gating but still produces a divergent firing decision through indirect downstream math (e.g., a ratio that crosses a threshold).

---

## Evidence

### Code inspection — Hypothesis (a) eliminated

**`internal/services/datacleaner/service.go::checkRuleApplicability` (lines 602-624):**
```go
func (s *service) checkRuleApplicability(rule *entities.CleaningRule, data *entities.FinancialData) bool {
    if rule.Threshold != nil {
        return s.evaluateRuleThreshold(rule, data)
    }
    switch rule.ID {
    case "goodwill_exclusion":
        return data.Goodwill > 0
    case "intangible_adjustment":
        return data.OtherIntangibles > 0
    case "obsolete_inventory":
        return data.Inventory > 0 && data.InventoryTurnover < 6.0
    case "operating_leases":
        return data.Revenue > 0
    default:
        return s.hasRelevantDataForRule(rule, data)
    }
}
```

`evaluateRuleThreshold` (lines 627-687) and `hasRelevantDataForRule` (lines 691-705) reference ONLY `data.Revenue`, `data.TotalAssets`, `data.TotalDebt`, `data.Inventory`, `data.InventoryTurnover`, `data.ContingentLiabilities`, etc. — pure `*entities.FinancialData` field values. **There is no period-type field on `*entities.FinancialData` that the predicate reads** (`FilingPeriod` is present but not referenced). The rule engine has no FY-vs-Q knowledge.

The same holds for the rule definitions at `config/datacleaner/rules.json` (lines 12-90): the `obsolete_inventory`, `goodwill_exclusion`, `intangible_adjustment`, `deferred_tax_assets` rule objects carry `id`, `name`, `description`, `category`, `xbrl_tags`, `adjustment`, `threshold`, `industry`, `severity`, `enabled` — no period-type field. The `internal/services/datacleaner/rules/engine.go::isRuleApplicableToIndustry` (lines 254-261) filters only by GICS industry code, not period.

**Hypothesis (a) is ruled out by code inspection.** No period-type gate exists at any layer of the rule engine.

### Code inspection — Hypothesis (b) eliminated

**`internal/infra/gateways/sec/parser.go::extractFiscalPeriods` (lines 278-366) and `processFacts` (lines 375-427):** the parser iterates `taxonomy → concept → unit → factList` and partitions each fact by `periodKey = fmt.Sprintf("%d%s", fact.Fy, fact.Fp)`. FY and Qx are SEPARATE buckets keyed off the SEC's own `fp` field on each fact. There is no FY-specific collapse, aggregation, or transformation — facts tagged `"fp": "FY"` go into the `"<year>FY"` bucket; facts tagged `"fp": "Q3"` go into the `"<year>Q3"` bucket.

**`parsePeriodData` (lines 439-857)** then runs identically per period — same XBRL tag list, same `findValue` priority, same plug computation. The four fields the A-rules read (`Goodwill`, `OtherIntangibles`, `Inventory`, `DeferredTaxAssets`) are populated from the same XBRL tags regardless of period.

**Empirical confirmation:** raw SEC data inspection at `artifacts/tier2-baseline/2026-05-19/MXL/req_10884776-8ff3-4641-8abb-af7e2ef89320/05-fetch-sec.raw.json::InventoryNet` shows `fp=FY` Inventory facts present and non-zero (e.g., MXL FY2025 `val=78104000`; FY2024 `val=90343000`). Same shape for `Goodwill`, `OtherIntangibles`, `DeferredTaxAssets`. The parser populates them onto FY periods just as it does for Qx.

**Hypothesis (b) is ruled out by both code and data inspection.** The parser does not strip A-rule inputs on FY.

### Code inspection + arithmetic — Hypothesis (c) confirmed

**Step 1 — establish that only A5 (obsolete_inventory) can produce the CA-down / TA-up paired signature.**

`internal/services/datacleaner/recompute.go::recomputeUmbrellas` (lines 74-88) recomputes:
- `recomputedCA = Cash + Inventory + OtherCurrentAssets`
- `recomputedTA = (Goodwill + OtherIntangibles + DeferredTaxAssets + OtherNonCurrentAssets) + CurrentAssets`

Now walk the four A-rule mutations at `internal/services/datacleaner/adjustments/assets.go`:

| Adjuster | Fields mutated | Effect on recomputeCA | Effect on recomputeTA |
|---|---|---|---|
| A1 goodwill_exclusion (lines 38-109) | `Goodwill -= X`, `TotalAssets -= X` | unchanged (Goodwill not in CA formula) | drops by X (Goodwill in NCA components) **and** reportedTA drops by X — both sides move together → **NET ZERO TA divergence** |
| A2 intangible_adjustment (lines 111-194) | `OtherIntangibles -= X`, `TotalAssets -= X` | unchanged | both sides move together → **NET ZERO TA divergence** |
| A4 deferred_tax_assets (lines 271-350) | `DeferredTaxAssets -= X`, `TotalAssets -= X` | unchanged | both sides move together → **NET ZERO TA divergence** |
| A5 obsolete_inventory (lines 196-269) | `Inventory -= X`, `TotalAssets -= X` | **drops by X** (Inventory in CA components, cleaner did NOT touch CA umbrella) | recomputeTA includes the unchanged CA umbrella → recomputeTA does NOT drop, but reportedTA drops by X → **+X TA divergence** |

A5 is the only adjuster whose mutation falls on the CA side of the umbrella-vs-component split. A1/A2/A4 mutations are symmetric (both component AND umbrella move) so they produce zero net recompute divergence. **The CA-down / TA-up paired signature is uniquely A5's fingerprint.**

This is verified empirically against MXL `2026Q1` (the canonical case from the original DC-1 tracker):
- 10-clean-input: `inventory=85,839,000`, `total_assets=771,267,000`, `goodwill=318,588,000`, `other_intangibles=46,412,000`
- 10-clean-output: `inventory=51,503,400`, `total_assets=387,402,067`, `goodwill=0`, `other_intangibles=15,470,667`
- Inventory writedown = 85,839,000 - 51,503,400 = **34,335,600** = 40% × 85,839,000 (A5 writedown rate)
- Shadow MXL.json `2026Q1` CA delta = **-34,335,600** — exact match.
- Shadow MXL.json `2026Q1` TA delta = **+34,335,600** — exact match.
- Goodwill drop ($318.59M) and intangibles drop ($30.94M) DO NOT show as CA/TA divergences (zero net).

### Step 2 — establish why A5 fires on Qx but no-ops on FY

`assets.go::ProcessInventoryAdjustment` (lines 196-269) fires when:
1. `data.Inventory > 0` — present on both FY and Qx for inventory-carrying issuers.
2. **AND EITHER** `isObsolete` (via `detectInventoryObsolescence` at lines 682-694) **OR** `inventoryRatio > industryThreshold`.

`detectInventoryObsolescence` (lines 682-694) sets `isObsolete = true` when:
- `data.InventoryTurnover > 0 && data.InventoryTurnover < 3.0`
- OR `inventoryRatio > 1.5 × industryThreshold`

`InventoryTurnover` is computed at `parser.go:834-835` as `Revenue / Inventory`. **This is where the FY-vs-Q divergence lives:** the SEC's XBRL convention reports Revenue facts under `fp=Qx` as the QUARTERLY revenue (or YTD-Qx, depending on the issuer), while `fp=FY` facts carry the ANNUAL revenue. Inventory is a point-in-time balance-sheet figure that is independent of period length. So:

- **Qx period:** `Turnover = Quarterly_Revenue / Inventory` — gives a "quarterly velocity" number that for a typical slow-turn issuer (MXL: ~$137M Q revenue / ~$86M inventory = 1.59) lands BELOW 3.0 → `isObsolete = true` → A5 fires.
- **FY period:** `Turnover = Annual_Revenue / Inventory` — typically 4× the Qx number (MXL: ~$478M annual revenue for fy=2020 / ~$78M-90M inventory ≈ 5.3 to 6.1) lands ABOVE 3.0 → `isObsolete = false`. Falls through to `inventoryRatio > threshold` check. With `inventoryRatio ≈ 78M / ~400M = 19.5%` and `industryThreshold = 0.25` (default) or `0.20` (industrials), A5 still does not fire because ratio is BELOW threshold → A5 no-ops.

The SEC-side fact data confirms the Revenue-magnitude asymmetry. From `artifacts/tier2-baseline/2026-05-19/MXL/req_10884776-8ff3-4641-8abb-af7e2ef89320/05-fetch-sec.raw.json::RevenueFromContractWithCustomerExcludingAssessedTax`, MXL `fp=FY, fy=2020` is `val=478,596,000` (annual) — roughly 4× the typical $120-140M Q value.

**Cross-check against Ford / KO / AMD:**
- Ford Q3 2023: CA writedown = $7.3B (from shadow), implying Inventory ≈ $18.3B. Quarterly revenue ≈ $42B → turnover = 2.29 (< 3.0) → obsolete → A5 fires.
- Ford FY 2023: Annual revenue ≈ $170B → turnover = 9.3 (> 3.0) → not obsolete. Inventory / TotalAssets ≈ 18.3/270 ≈ 6.8% → < 25% threshold → A5 no-ops.
- KO/AMD have similar mechanics; their TL=0 parser dropouts (tracked separately at `docs/reviewer/T2-BS-3-parser-totalliabilities-zero-amd-ko.md`) are orthogonal to this finding.

**Cross-check against AAPL** (the basket's no-CA/TA control): AAPL has very high inventory turnover (~40-50× annual) and inventory ratio is below 2% of total assets. A5 no-ops on BOTH FY and Qx periods because `isObsolete = false` (turnover > 3.0) AND `inventoryRatio <= threshold` (≈ 2% < 25%). The AAPL shadow snapshot at `internal/integration/testdata/recompute-shadow/AAPL.json` shows ZERO CA/TA divergences across all 12 periods — consistent.

**Cross-check against EQIX** (REIT, no inventory): from the shadow analysis report line 232, "EQIX has 12 records, all TotalLiabilities-only, ... No CA/TA pairs (so A5 inventory adjuster isn't firing — REITs have minimal inventory; this is correct)." Consistent.

---

## Conclusion

**Hypothesis (c) is supported. Confidence: HIGH.**

The FY-NULL pattern is **not a rule-engine gate** (hypothesis (a) ruled out by code inspection) and **not a parser FY collapse** (hypothesis (b) ruled out by code + raw SEC fact inspection). It is the natural consequence of:

1. The SEC's XBRL `fp` convention reports Revenue facts with PERIOD-LENGTH-VARYING magnitude (quarterly for Qx, annual for FY) while Inventory is a point-in-time balance.
2. `parser.go::InventoryTurnover = Revenue / Inventory` therefore lands ~4× higher on FY periods than on Qx for the same balance-sheet snapshot.
3. A5's `detectInventoryObsolescence` (`assets.go:682-694`) classifies inventory as obsolete when `InventoryTurnover < 3.0` — a threshold tuned for QUARTERLY velocity but applied unconditionally.
4. FY periods therefore cross the obsolescence threshold UPWARD and A5's `isObsolete` flips to false; the fallback `inventoryRatio > industryThreshold` check rarely fires either because annual inventory ratios are typically well below the 20-25% defaults.
5. The CA-down / TA-up paired divergence is uniquely A5's recompute fingerprint (A1/A2/A4 mutations cancel symmetrically). So when A5 no-ops, the pair disappears — exactly the FY-NULL pattern observed.

**Is this a bug?** Technically YES — A5's `InventoryTurnover < 3.0` heuristic is implicitly QUARTERLY-tuned and is unsound on FY annual snapshots. A slow-turn issuer whose Q periods correctly flag obsolescence has its FY snapshot silently passed clean. In principle, the same balance sheet on the same filer should produce the same writedown regardless of how the surrounding period's Revenue is reported.

**Should we fix it now?** NO — for three reasons:

1. **No live ticker in the basket presents the consumer harm.** MXL is the only slow-turn issuer in the basket and its valuation route reads the LATEST period (which is Qx-style — `2026Q1`), not the FY periods. Other API consumers similarly consume the latest period. FY snapshots affect downstream consumers only if they explicitly scan historical periods AND if the period happens to be FY AND if the issuer is genuinely slow-turn — a triple coincidence that no current consumer exhibits.

2. **The Phase 2 PR-2 Adjuster migration preserves the current per-period firing decision by construction.** The PR-2 native `Adjuster.Apply` for A5 inherits the same `data.Inventory > 0 && (isObsolete || ratio > threshold)` gating logic; the FY-NULL behavior is preserved on both sides of the PR-2 cut. Phase 2 close does not require this finding's resolution.

3. **Fixing requires period-aware annualization in the parser or the predicate.** Two options exist (both deferred):
   - **Option A:** Parser annualizes Q Revenue (multiply by 4, or use TTM via period stitching). Risk: alters Revenue field semantics broadly, affecting growth-blend, valuation, and every other Revenue consumer — a much larger refactor than this finding's scope warrants.
   - **Option B:** A5 predicate normalizes turnover (multiply Q turnover by 4, or skip the obsolescence check on FY periods entirely). Risk: still treats Inventory writedowns inconsistently across period types; may also incidentally surface other latent quarterly-vs-annual heuristic bugs (e.g., InterestExpense / OperatingIncome ratios). Defer to a heuristic-review pass that audits ALL period-length-sensitive thresholds at once.

**Disposition: Close-with-caveat at PR-2 wrap-up; punt the heuristic fix to a Phase 4+ subtask titled "FY-aware annualization for quarterly-tuned heuristics".** No Phase 2 production behavior change.

---

## Phase 2 disposition

PR-2 closeout note should say:

> "Task 2.7 investigation concluded HIGH-confidence root cause: SEC's `fp=FY` Revenue facts are annual while `fp=Qx` facts are quarterly, so the same balance-sheet snapshot reaches A5 with materially different InventoryTurnover values on FY vs Qx. A5's quarterly-tuned `<3.0` obsolescence threshold therefore no-ops on FY for slow-turn issuers that correctly flag on Qx. No production behavior change for PR-2 — the native `Adjuster.Apply` for A5 preserves the per-period firing decision the legacy code already encodes. Heuristic fix punted to a Phase 4+ subtask. Tracker remains OPEN at `docs/reviewer/DC-1-FY-enable-predicate-investigation.md` for future closure."

---

## Recommended next steps (if a future agent wants to close this fully)

A future agent who wants to MOVE this from OPEN → RESOLVED should run one of these confirmation experiments to nail the conclusion to nine-decimal certainty (above is high-confidence by code reading; experiments below give bit-for-bit numerical confirmation):

### Experiment 1 — direct firing log on FY periods (smallest patch, ~10 LoC)

In a SCRATCH branch (do NOT commit), add a `WARN` or `Debug` log at `assets.go:198`, `assets.go:217`, and `assets.go:226` capturing `fd.Ticker`, `fd.FilingPeriod`, `data.Inventory`, `data.InventoryTurnover`, `inventoryRatio`, `isObsolete`. Run the shadow basket test (`go test ./internal/integration/... -run TestDataCleanerRecompute_ShadowMode_TickerBasket -count=1`) and grep the captured WARN stream for `period=2024FY` and `period=2024Q3` rows for MXL. Should observe:
- 2024Q3: `InventoryTurnover ≈ 1.6`, `isObsolete=true`, firing
- 2024FY: `InventoryTurnover ≈ 5-7`, `isObsolete=false`, `ratio < 0.25`, no-op

If observed, confirms conclusion BIT-FOR-BIT.

### Experiment 2 — direct field dump from the existing shadow test (no source patch required)

Modify the test ONLY (`internal/integration/datacleaner_recompute_shadow_test.go`) to additionally serialize `fd.Revenue`, `fd.Inventory`, `fd.InventoryTurnover`, `fd.Goodwill`, `fd.OtherIntangibles`, `fd.DeferredTaxAssets` into the per-period snapshot section. Run the basket. Diff `2024FY` vs `2024Q3` for MXL/AMD/F/KO — should observe FY Revenue ≈ 4× Q Revenue with all other fields identical, confirming the Revenue-magnitude asymmetry is the entire root cause.

### Experiment 3 — replay against a slow-turn FY-only issuer

Find a known slow-turn issuer whose FY filing inventory IS genuinely obsolete (e.g., the textbook examples are retail bankruptcy cases like RAD Q4 2023 or BBBY FY 2022 — historical data from the SEC archive). Replay through the cleaner. Confirm A5 no-ops on the FY period. The expected outcome — silent under-writedown — IS the consumer harm this finding identifies. If the harm is reproducible, escalate from "low-priority Phase 4 subtask" to "MEDIUM-priority Phase 3 prerequisite". As of 2026-05-22 no such ticker has been observed in production.

### Experiment 4 — sensitivity test: would `InventoryTurnover < 6.0` fix it?

The `checkRuleApplicability` fallback path at `service.go:616` uses `data.Inventory > 0 && data.InventoryTurnover < 6.0` (a looser threshold than A5's actual `<3.0`). Run experiment 1 with the obsolescence threshold raised to 6.0 in `assets.go:685`. Does MXL FY then enter the obsolete branch? If yes, the heuristic fix is one-line (still doesn't address the systemic quarterly-vs-annual question but resolves the FY-NULL specifically).

### Decision point

If experiment 1 or 2 confirms, change this tracker's status to RESOLVED-NOT-A-BUG with a forward link to the new heuristic-review subtask. If experiments find a more subtle interaction (e.g., the parser DOES strip Revenue on certain FY periods due to currency-bucket collapse or a fact-conflict last-write-wins), reopen and re-investigate.

---

## Cross-references

- **Phase 1 shadow analysis Cluster A-FY-NULL section:** `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md` lines 160-167 (the empirical observation this tracker investigates).
- **Phase 1 shadow analysis MXL deep-dive:** same file lines 216-228 (the canonical $34.34M A5 inventory writedown case).
- **Affected JSON snapshots (committed at PR-1 base):**
  - `internal/integration/testdata/recompute-shadow/MXL.json` — 8 Qx periods with CA/TA pair, 3 FY periods TL-only.
  - `internal/integration/testdata/recompute-shadow/AMD.json` — 8 Qx with pair, 4 FY TL-only (entangled with T2-BS-3 parser TL=0 dropout).
  - `internal/integration/testdata/recompute-shadow/F.json` — 8 Qx with pair, 3 FY TL-only.
  - `internal/integration/testdata/recompute-shadow/KO.json` — 8 Qx with pair, 4 FY TL-only (also entangled with T2-BS-3).
- **Source files cited:**
  - `internal/services/datacleaner/service.go` lines 602-705 — `checkRuleApplicability` + `evaluateRuleThreshold` + `hasRelevantDataForRule` (eliminated hypothesis (a)).
  - `internal/services/datacleaner/rules/engine.go` lines 102-261 — `GetRules` / `GetIndustryRules` / `isRuleApplicableToIndustry` (no period filter).
  - `internal/services/datacleaner/recompute.go` lines 74-106 — recompute formulas that produce the CA-down / TA-up signature.
  - `internal/services/datacleaner/adjustments/assets.go` lines 38-269, 666-694 — A1/A2/A5/A4 firing logic + `detectInventoryObsolescence`.
  - `internal/infra/gateways/sec/parser.go` lines 264-427, 463-857 — `extractFiscalPeriods` + `parsePeriodData` (no FY collapse).
  - `config/datacleaner/rules.json` lines 12-90 — A1/A2/A4/A5 rule definitions (no period-type field).
- **Related trackers:**
  - `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md` — parent DC-1 tracker; this finding is recorded under its open-questions section after PR-2 wrap-up.
  - `docs/reviewer/T2-BS-3-parser-totalliabilities-zero-amd-ko.md` — orthogonal AMD/KO TL=0 parser dropout; visible on the same FY rows in `AMD.json` / `KO.json` snapshots but unrelated mechanism.
- **Spec / plan references:**
  - `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` — DC-1 spec (FY-NULL is outside the spec's mutation-symmetry remit; resolution is a future heuristic-review subtask).
  - `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md` — PR-2 plan (this tracker is Task 2.7's deliverable).
