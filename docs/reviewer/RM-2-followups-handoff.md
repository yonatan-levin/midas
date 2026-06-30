# RM-2 follow-ups — next-session handoff (RM-2.5 + RM-2.4)

**Created:** 2026-06-30 · **Author:** prior session (RM-2 Phase 2 close-out)
**Master at handoff:** `b1efbaa` · **GitHub issue:** [#14](https://github.com/yonatan-levin/midas/issues/14) (OPEN)
**Parent tracker:** `docs/reviewer/RM-2-sector-multiple-coverage-gaps.md` → "Remaining open follow-ups" section.

---

## TL;DR for whoever picks this up

RM-2 **Phase 1 + Phase 2 are MERGED to master** (Damodaran SIC-driven EV/Revenue multiples, provenance via `industry.multiple_source`, the `cmd/refresh-damodaran` live-fetch tool, CalcVersion `4.11`). Live-validated: MXL → `revenue_multiple`, `multiple_source: "Damodaran 2026-01-01"`, applied **15.7×** (real Damodaran "Semiconductor" value); AAPL control → DCF, field omitted.

**Two items remain, both deliberately DEFERRED — neither blocks anything, both are gated on a concrete driver:**
- **RM-2.5** — datacleaner industry **rule-override files** (absorbed from TDB-9). *Different axis from Phase 2* (cleaning rules, not multiples).
- **RM-2.4** — **per-region** multiples (US/Europe/Japan/China). Phase 2 ships US-only.

**Do NOT start either without a driver** (see each section). If there is no driver, the correct action is to leave them deferred — they are working-as-intended defaults, not bugs.

---

## Workflow rules for the next session (read first)

- **Worktree-per-task is MANDATORY.** `git worktree add ../midas-<slug> -b <type>/<slug> master`; do all edits/tests/commits there; fast-forward `master` from the branch when done; `git worktree remove`. Never commit directly to `master`. (CLAUDE.md "Working model".)
- **Dual-track every finding:** GitHub issue (#14 or a child) + a `docs/reviewer/` tracker. Cross-link both ways.
- **Replay field-count guard (CRITICAL if you touch `FairValueResponse`):** any new response field must be added to `internal/observability/replay/diff.go` `goFieldToJSON` AND `countFairValueFields()` bumped, in the SAME commit, or the `init()` reflection guard panics every replay test. Current count after Phase 2 = **54** (FairValueResponse 40 + Industry 6 + SanityCheck 8).
- **Bit-for-bit invariants stay green:** `TestDDM_LegacyPath_BitForBit`, `TestRecomputeUmbrellas_NoMutation`, `TestOrchestrator_LedgerOrdering`, and the recompute-shadow byte-identity (`git diff --quiet internal/integration/testdata/recompute-shadow/`).
- **Known pre-existing red tests (NOT your regression):** 3 `internal/integration` tests fail on a fresh checkout with `no baseline date directories under artifacts/tier2-baseline` (BUG-016 — the gitignored baseline isn't present). Confirm any failures you see are exactly these before worrying.

---

## RM-2.5 — datacleaner industry rule-override files (absorbed from TDB-9)

**Axis:** datacleaner **cleaning-rule overrides**, NOT valuation multiples. `internal/services/datacleaner/service.go::loadIndustryRules` maps a GICS sector code → `config/datacleaner/industry/<sector>.json`. Only `45`→`technology.json` and `25`→`retail.json` exist.

**Current reality (why it's deferred, not broken):**
- The live `IndustryClassifier.ClassifyIndustry` only ever emits **`45`, `20`, or `25`** (`industry/classifier.go::loadDefaultConfigurations` defines exactly those 3 sectorConfigs; default `20`).
- So the ONLY reachable-and-uncovered sector is **`20` (Industrials)** — it falls through to base `rules.json` with a **non-fatal warning** (`service.go:~242`). That is a working default, not a bug.
- Every other GICS code (`10/15/30/35/40/50/55/60`) is an override-file namespace the classifier **cannot currently emit** — adding a file for one is a no-op until the classifier is taught to emit that code first.

**Driver / gate to START:** a GICS-`20` (Industrials) ticker whose base-rule cleaning is **demonstrably wrong** (produces a bad valuation traceable to a missing Industrials override), OR a deliberate decision to broaden classifier emission. Without that, leave deferred.

**If a driver appears — the procedure (from the in-code note at `loadIndustryRules`, do NOT add a bare mapping line):**
1. If the target sector isn't emitted yet, extend `ClassifyIndustry` to emit the GICS code FIRST.
2. Author a **domain-validated** `config/datacleaner/industry/<sector>.json` override (what asset/earnings rules does this sector actually need? — not a guessed no-op).
3. Add the code→file entry in `loadIndustryRules`.
4. Add the new `<sector>.json` to the `datacleaner/ledger_invariants_test.go` sync list.
5. **Regenerate + REVIEW** the recompute-shadow snapshots for affected tickers (this WILL drift `internal/integration/testdata/recompute-shadow/` — review every diff).
6. **For Financials (`40`):** `TestDDM_LegacyPath_BitForBit` is golden-fixture-pinned and will NOT catch cleaner drift — re-validate JPM/BAC/WFC END-TO-END through the live DDM path, not via that unit test.

**Files:** `internal/services/datacleaner/service.go` (`loadIndustryRules`, `getIndustryCode`), `internal/services/datacleaner/industry/classifier.go`, `config/datacleaner/industry/*.json`, `internal/services/datacleaner/ledger_invariants_test.go`, `internal/integration/testdata/recompute-shadow/`.

**Effort:** M per sector (mostly the domain-validated rule content + shadow review). **Risk:** medium — touches the cleaner output, so shadow/bit-for-bit review is load-bearing.

---

## RM-2.4 — per-region multiples (US / Europe / Japan / China)

**Axis:** valuation multiple **regional split**. Phase 2 ships **US-only** (`psdata.xls`). International ADRs (TSM, ASML, BABA, SAP, …) currently get the **US-equivalent industry multiple as a stopgap**.

**What exists to build on (Phase 2 plumbing):**
- `cmd/refresh-damodaran/main.go` — fetches + parses a Damodaran `.xls` (legacy OLE2/BIFF8 via `github.com/extrame/xls`; `excelize` CANNOT read these) and regenerates the committed JSON. Damodaran publishes a **global** file: `https://pages.stern.nyu.edu/~adamodar/pc/datasets/psGlobal.xls` (US/Europe/Japan/emerging/global columns).
- `config/damodaran_sector_multiples.json` (94 US industries, `dataset_date 2026-01-01`) + `config/sic_to_damodaran.json` crosswalk, both `go:embed`'d.
- `internal/services/valuation/models/revenue_multiple.go::resolveMultiple(sic, industry)` — Damodaran-by-SIC → Phase 1 bucket → default. The lookup primitive is `sector_lookup.go::lookupDamodaranMultiple`.

**Driver / gate to START:** demand for region-accurate multiples on non-US tickers (e.g. a TSM/ASML valuation visibly wrong because it uses the US semi multiple). Without that, the US-equivalent stopgap is acceptable.

**If a driver appears — likely shape (confirm with an ARCH pass):**
1. Determine the ticker's region. **Open question to resolve first:** where does region come from? (SEC country-of-incorporation? the ADR-ratio config? a new mapping?) This is the load-bearing design decision — do NOT hardcode.
2. Extend the refresh tool to also pull `psGlobal.xls` and emit a **region-keyed** table (e.g. `{ "US": {...}, "Europe": {...}, "Japan": {...}, "Emerging": {...} }`).
3. Thread region into `resolveMultiple` (additive param, like `SICCode` was) → pick the regional EV/Sales, falling back to US when the region/industry is missing (preserve the zero-regression fallback discipline).
4. Surface region in provenance — extend `multiple_source` (e.g. `"Damodaran 2026-01-01 (Europe)"`) — **and if you add any new `FairValueResponse` field, honor the replay diff.go guard (count → 55).**
5. Consider a `CalculationVersion` bump (a real output change for international tickers).

**Files:** `cmd/refresh-damodaran/main.go`, `config/damodaran_sector_multiples.json` (or a new region-keyed file), `internal/services/valuation/models/{revenue_multiple,sector_lookup}.go`, `internal/services/valuation/service.go` (region plumbing), possibly `internal/api/v1/handlers/fair_value.go` + `internal/observability/replay/diff.go`, `docs/operations/damodaran-refresh.md`.

**Effort:** M–L (the region-source decision + global-file parse + plumbing). **Risk:** low–medium (additive; US path stays the default fallback).

---

## Reference: how Phase 2 was built (pattern to reuse)

`/plan-and-create` → ARCH spec → BACKEND (TDD) → independent VERIFIER → REVIEWER → QA (live API) → `/code-review` → `/verification-before-completion`. Key artifacts:
- Spec: `docs/reviewer/RM-2-phase-2-damodaran-implementation-plan.md`.
- Runbook: `docs/operations/damodaran-refresh.md` (annual Damodaran refresh; runs early Feb after Damodaran's Jan publish).
- Tracker: `docs/reviewer/RM-2-sector-multiple-coverage-gaps.md`.
- The `.xls` is legacy OLE2 — **`extrame/xls`, never `excelize`**. The dataset-date cell is a `"YYYY.MM"` string, not an Excel serial.

---

## Starting prompt for the next session (paste this)

> Pick up the RM-2 follow-ups. Read `docs/reviewer/RM-2-followups-handoff.md` first, then `docs/reviewer/RM-2-sector-multiple-coverage-gaps.md` ("Remaining open follow-ups") and GitHub issue #14. Two deferred items remain: **RM-2.5** (datacleaner Industrials `industrials.json` rule-override) and **RM-2.4** (per-region Damodaran multiples).
>
> First, **confirm whether a driver actually exists** before building anything:
> - RM-2.5: is there a GICS-`20` (Industrials) ticker whose cleaning/valuation is demonstrably wrong because it falls through to base `rules.json`? If not, it stays deferred.
> - RM-2.4: is there a non-US ADR (e.g. TSM, ASML) whose `revenue_multiple` valuation is visibly wrong because it uses the US-equivalent industry multiple? If not, it stays deferred.
>
> If a driver exists, work the relevant item via `/plan-and-create` (ARCH → BACKEND → VERIFIER → REVIEWER → QA), in a **dedicated git worktree off master**, tracked on #14 + the RM-2 tracker. Honor the replay `diff.go` field-count guard (currently 54) on any new `FairValueResponse` field, keep the zero-regression fallback discipline, and keep the DDM/recompute-shadow bit-for-bit invariants green (note the 3 pre-existing BUG-016 `artifacts/tier2-baseline` integration failures are NOT your regression). If NO driver exists, report that and leave both deferred — do not build speculative coverage.

---

*When BOTH RM-2.5 and RM-2.4 are resolved (or formally won't-fix'd), archive `RM-2-sector-multiple-coverage-gaps.md` + `RM-2-phase-2-damodaran-implementation-plan.md` + this handoff into `docs/reviewer/archive/` and close #14.*
