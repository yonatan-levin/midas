# FEEDBACK-LOG.md — Agent Corrections & Preferences

Append-only log of **corrections and validated preferences** the user has given to AI agents. Items here should survive any single session — they represent how to work with this user on this project.

This file is distinct from `memory/MEMORY.md`:
- **MEMORY.md** = durable facts about the project and user (who, what, why)
- **FEEDBACK-LOG.md** = how-we-work rules learned through interaction (corrections and validated choices)

Items that recur often should be **promoted to `MEMORY.md`** during weekly curation.

---

## Format

Each entry should include:

```markdown
### YYYY-MM-DD — <short rule>

**Rule:** <the instruction itself, imperative form>

**Why:** <the reason the user gave, usually a past incident or strong preference>

**How to apply:** <when/where this guidance kicks in>

**Source:** <conversation where this was established, optional>
```

Lead with the rule. The **Why** lets future sessions judge edge cases. The **How to apply** specifies scope so the rule doesn't over-generalize.

---

## Active Rules

### 2026-04-23 — Datacleaner should use SIC-based classifier, not heuristic

**Rule:** Prefer SIC-code-driven industry classification in the datacleaner over the balance-sheet heuristic. `getIndustryCode` in `internal/services/datacleaner/service.go:945` should call `Classify(sic, naics, name)` first and only fall back to `ClassifyIndustry(ticker, data)` when SIC is unavailable.

**Why:** A hotfix on 2026-04-23 patched an AMD regression where the heuristic `isRetailCompany` predicate matched on 12% inventory + 40% intangibles and classified AMD (SIC 3674, semiconductor) as Consumer Discretionary ("25"). The hotfix added R&D/SBC guards and reordered the tech/retail check in `classifier.go`, but the deeper issue — `getIndustryCode` ignores SIC even though `Classify` is available on the same struct and returns `"MFG"` for AMD — remains. The heuristic is a proxy; SIC is ground truth where the SEC provides it.

**How to apply:**
- `HistoricalFinancialData.SICCode` already exists (`internal/core/entities/financial_data.go:114`) and is populated from SEC submissions. The gap is that the per-period `FinancialData` struct the classifier sees has `IndustryCode` (TODO at `internal/core/entities/financial_data.go:18`) but no SIC field — so SIC is not plumbed down to the classifier.
- Fix path A (minimal): change `getIndustryCode` in `internal/services/datacleaner/service.go:945` to accept `*HistoricalFinancialData` or take the SIC code directly, then prefer `Classify(sic, naics, name)` and fall back to the ratio heuristic only when SIC is missing.
- Fix path B (entity change): add a `SICCode` field to `FinancialData` itself and populate it in the datafetcher when materializing periods from `HistoricalFinancialData`. Delete or satisfy the `IndustryCode` TODO at line 18.
- Reconcile the string code mapping (`"TECH"`, `"MFG"`, `"RETAIL"`) with the GICS sector codes (`"45"`, `"20"`, `"25"`) expected downstream by industry-specific rule loading.
- Blast radius to re-verify once SIC is wired: semiconductors with acquired IP (AMD, AVGO, MRVL, NXPI, QRVO), medical devices (MDT, BSX, SYK), industrial conglomerates (HON, ETN, EMR, ROP), tech companies with inventory (ANSS, PTC).

**Source:** AMD retail misclassification hotfix, 2026-04-23 session.

### 2026-04-24 — Heuristic retail predicate rejects owned-store retailers

**Rule:** `isRetailCompany` at `internal/services/datacleaner/industry/classifier.go` has two independent success branches — `intangibles/assets > 10%` OR `tangibles/assets < 70%`. Both branches fail for classic owned-store retailers (Target, Home Depot, Lowe's, Costco), because they own their stores (tangibles > 70%) and carry modest brand intangibles (< 10%). Those tickers fall through to `isManufacturingCompany` and get misclassified as Industrials.

**Why:** Surfaced by live QA on 2026-04-24 — `/api/v1/fair-value/TGT` returned `industry.heuristic_code = "20"` (Industrials) and `match: false` despite Target being a textbook retailer with `sic = "RETAIL"`. The AMD retail-fix on 2026-04-23 tightened `isRetailCompany` with R&D/SBC guards but kept the original OR-branch structure. The `TestIsRetailCompany_AcceptsActualRetailer` sentinel used inventory-22% + tangibles-65% + intangibles-5% which hits the tangibles<70% branch; a real Target with tangibles>75% misses both branches.

**How to apply:**
- Add a third branch to `isRetailCompany`: a strong `inventoryRatio` (e.g., > 0.15) combined with near-zero R&D should qualify even when tangibles are high and intangibles are low. Owned-store retailers have meaningful inventory turnover by definition.
- Strengthen `TestIsRetailCompany_AcceptsActualRetailer` with a third subcase: inventory-20%+, intangibles-5%, tangibles-75%+, R&D-0, matching Target's actual balance-sheet shape.
- Consider this a sibling finding to the SIC-only unification (`docs/refactoring/spec/industry-classification-unification-spec.md`) — fixing it right may not be worth the churn if the heuristic is going away; but the refactor doesn't close for weeks, so interim tightening is defensible.

**Source:** Live QA run 2026-04-24, part of the Industry-in-response feature verification.

### 2026-04-24 — Cleaned `FinancialData` missing `ResearchAndDevelopment` for some tickers

**Rule:** `FinancialData.ResearchAndDevelopment` is not being populated for at least some tickers in the live SEC → datacleaner pipeline. AMD specifically — its SEC XBRL includes R&D as ~25% of revenue, but the heuristic `isTechnologyCompany` returns false for AMD live (would trigger on R&D/Revenue > 10%), so AMD falls through to `isManufacturingCompany` and gets labeled Industrials by the heuristic.

**Why:** Surfaced by live QA on 2026-04-24 — `/api/v1/fair-value/AMD` returned `industry.heuristic_code = "20"` despite the unit test `TestClassifyIndustry_AMD_NotRetail` asserting AMD classifies as `"45"` when R&D is populated. The discrepancy is the unit test hardcodes R&D; the live pipeline isn't filling it in. NVDA with the same SIC code returns `heuristic_code = "45"` correctly, so the gap is per-ticker data extraction, not a systemic pipeline failure.

**How to apply:**
- Investigate XBRL tag extraction for AMD specifically. Start at `internal/services/datacleaner/xbrl_matcher.go` and check the US-GAAP R&D tag list.
- Add an integration test that fetches AMD's live SEC data and asserts `FinancialData.ResearchAndDevelopment > 0`.
- The Industry-in-response feature's `Match` field partially masks this gap (AMD: `sic=MFG` + `heuristic=20` returns `match=true` via the `MFG → {20, 45}` multi-map). When the SIC-only refactor lands, the heuristic output disappears and this gap becomes less visible — good reason to fix it before, not after.

**Source:** Live QA run 2026-04-24, part of the Industry-in-response feature verification.

### 2026-05-30 — `/execute` B-V-R-Q must dispatch VERIFIER/REVIEWER/QA subagents, not roll into self-validation

**Rule:** When running the `/execute` skill's Phase-2 validation cycle, dispatch the VERIFIER, REVIEWER, and QA subagents via the `Agent` tool — do NOT roll all four lenses into inline self-validation (running tests + self-reviewing the diff + self-summarizing spec conformance). For the Q (cross-model query) step, use `mcp__zen-mcp__codereview` with `gpt-5.5` (or the named model) as a separate independent pass after the subagent cycle.

**Why:** Surfaced 2026-05-30 — user explicitly asked "per /execute plan did you ran B V R Q on the fix? with the rlevant sub agents?" after I had completed 3 atomic fix commits + inline tests + self-review. I had rolled all four cycle lenses into self-validation, skipping the subagent dispatch entirely. This violates the `/execute` skill's Critical Rule 2 ("Never skip validation gates"). When I subsequently dispatched the proper subagent cycle on the same commits, the REVIEWER subagent caught a stale impl-plan test-name drift the inline self-review missed, and the gpt-5.5 Q-pass caught a parent-spec sign-drift ("DDM subtracts" vs the shipped "DDM adds") that BOTH inline review AND the REVIEWER+QA subagents had missed because the subagents were scoped to the immediate fix commits, not the cross-cutting parent spec. Subagents bring genuinely independent perspective; gpt-5.5 widens the lens beyond the immediate diff.

**How to apply:**
- ALWAYS in `/execute` Phase 2: dispatch VERIFIER + REVIEWER + QA subagents in parallel via 3 `Agent` tool calls in a single message (they're independent — don't sequence them). Each prompt must be self-contained (branch + commits + spec context + what to check + output format).
- After subagent cycle returns: run `mcp__zen-mcp__codereview` with `gpt-5.5` (or named model) as the Q step. Use external validation (`review_validation_type: "external"`, two-step workflow).
- If subagents/Q surface NITs, address them in follow-up commits BEFORE HUMAN handoff — the prior Phase 5 partial cycle showed Q catches what inline misses.
- Inline self-validation (running `go test ./...` + reading the diff) is necessary but NOT sufficient. It satisfies the B (Build) and V (Verify) steps' MECHANICS but does not satisfy the R (Review) and Q (Query) steps' INDEPENDENCE requirement.
- Hotfix path (CRITICAL urgency only, explicit HUMAN approval) defers QA — but VERIFIER + REVIEWER subagent dispatch stays mandatory.

**Source:** User callout during DC-1 Phase 5 post-review-fix execution, 2026-05-30 session. Validated by the immediate subsequent cycle catching real bugs (REVIEWER LOW + Q MEDIUM) that inline self-validation had missed.

---

## Archive (Promoted to MEMORY.md or Obsolete)

*(Empty. Move items here when they are promoted to `memory/MEMORY.md` or are no longer relevant.)*

---

## Curation Rhythm

- **Per correction:** append immediately while context is fresh
- **Weekly:** review active rules; promote recurring ones to `MEMORY.md`; move promoted entries to Archive
- **Quarterly:** prune Archive entries older than 6 months that no longer apply

---

## Example Entries (Format Reference — Not Active Rules)

> The entries below are illustrative examples, not actual rules. Delete or ignore when the first real entry is added.

```markdown
### 2026-04-20 — Don't introduce backwards-compat shims

**Rule:** When removing code or renaming APIs, delete cleanly. Do not add `// removed` comments, rename-only `_var` stubs, or compatibility re-exports.

**Why:** User prefers tight diffs. Backwards-compat cruft hides the real change in code review.

**How to apply:** Applies to any refactor or cleanup inside `internal/`. Not applicable to public API changes where an announced deprecation window is needed.
```

```markdown
### 2026-04-21 — Single bundled PR preferred for refactors in internal/

**Rule:** For refactors touching multiple files in `internal/`, ship one bundled PR rather than a chain of small commits.

**Why:** User confirmed on 2026-04-21 that a 12-file bundled PR was the right call; splitting it would have been churn. Validated judgment, not a correction.

**How to apply:** Applies only to `internal/` refactors. Cross-package changes spanning `internal/` + `pkg/` + `cmd/` should still be split by package.
```

---

## Change Log

| Date | Change |
|------|--------|
| 2026-04-18 | Initial empty template. No active rules yet. |
