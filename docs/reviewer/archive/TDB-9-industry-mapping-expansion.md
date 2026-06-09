# TDB-9 — Expand industry mapping coverage

**Status:** RESOLVED — DOCUMENTED DEFER, 2026-06-09 (branch `worktree-tdb-9-industry-mapping`). The gating coverage analysis is done; the bare open-ended TODO is resolved into a tracked, criteria-based reference; expansion is gated on a concrete driver. Comment/doc-only ⇒ shadow byte-identical, no behavior change. Filed 2026-06-06 (TODO-catalog burn-down pass).
**Priority:** P4 — Tier 4 (backlog; needs concrete scope before it is actionable).
**Type:** Enhancement.
**Mirrored as GitHub issue:** `[TDB-9]` (yonatan-levin/midas).
**Origin:** 2026-06-06 burn-down — catalog "Industry Mapping Expansion" (`datacleaner/service.go` `loadIndustryRules`).
**Related:** RM-2 (sector multiple coverage gaps) — overlapping classifier-coverage theme.

**Disposition artifacts (2026-06-09):**
- Coverage analysis + disposition: `docs/refactoring/spec/tdb-9-industry-mapping-coverage-spec.md`
- Implementer plan: `docs/refactoring/implementations/tdb-9-industry-mapping-implementation-plan.md`

---

## Context (corrected by the coverage analysis)

The bare TODO `// TODO: Add more industry mappings as needed` is in
`internal/services/datacleaner/service.go::loadIndustryRules` (the original `:459` line drifted; it
is at `:504` on `master` `5b432ac`). It is **NOT** about `config/datacleaner/industry_codes.json` (the
SIC/keyword classifier, already broad). It maps a **GICS sector code** (from `getIndustryCode`) to an
**industry-specific datacleaner rule-override file**. Only two exist: `45` (Information Technology) →
`config/datacleaner/industry/technology.json`, and `25` (Consumer Discretionary) → `retail.json`.
Uncovered sectors fall through to the base rule set (`rules.json`) with a **non-fatal warning**
(`service.go:242-248`) — a deliberate, working default.

## Coverage finding (the gating step — DONE)

**The live `ClassifyIndustry` emits only `45`, `20`, `25`** (`industry/classifier.go::loadDefaultConfigurations`
defines exactly those `sectorConfigs`, default `20`). So:
- Covered (override file exists): `45` (Info Tech) → `technology.json`, `25` (Consumer Disc.) → `retail.json`.
- **Reachable-and-uncovered: `20` (Industrials) only** — degrades gracefully (base `rules.json` + a
  non-fatal `result.Warnings` note). This is the single live coverage gap.
- The other 8 GICS codes (Energy 10, Materials 15, Consumer Staples 30, Health Care 35, Financials 40,
  Comm Services 50, Utilities 55, Real Estate 60) are an **override-file namespace the classifier
  cannot currently produce** — covering them needs a CLASSIFIER change first. Full table + mechanism in
  the coverage spec §2.

## Why DEFER (not a quick add)

1. Each `<sector>.json` is **curated domain content** (industry-specific cleaning-rule overrides), not
   a mapping line — inventing them without a driver yields a no-op or unreviewed adjustments.
2. Adding overrides **changes cleaner output** → `recompute-shadow` drifts (gate fails, needs reviewed
   regen). Covering `20`/Industrials touches the default bucket and trips shadow immediately.
3. Covering a not-yet-emitted sector (e.g. Financials `40` → JPM/BAC/WFC) needs a **classifier change
   first**, then a domain override, then a shadow regen, **then end-to-end JPM/BAC/WFC re-validation
   through the live DDM path** (`TestDDM_LegacyPath_BitForBit` is golden-fixture-pinned and won't catch
   cleaner drift). Strictly more than a config edit.
4. **No driver** — under-specified P4; no misclassifying ticker / demonstrably-wrong sector in hand.

## Action taken (DOCUMENTED DEFER)

Resolved the bare TODO at `service.go:504` into a tracked, criteria-based note referencing TDB-9 / #9
(the GICS-sector→override-file mapping, the graceful fall-through, the 4-step add procedure incl. the
DDM caution, and the driver gate). Comment/doc-only — zero behavior change.

## Acceptance
- [x] **Concrete list of missing industries identified (the gating step)** — the ~9 uncovered GICS
  sectors + the graceful-fallback mechanism (coverage spec §2).
- [x] **Mappings added + classifier tests, OR documented defer** — DEFER: bare TODO resolved into a
  tracked reference; expansion procedure + driver gate documented. No mappings added by design
  (would change cleaner output and threaten DDM bit-for-bit; no driver). Future expansion gated on a
  concrete driver / RM-2.

## Note
Best handled together with RM-2 (sector multiples) — both stem from coarse classifier/override
coverage and both want a concrete driver before action.
