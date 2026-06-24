# TDB-9 — Industry-mapping coverage analysis + disposition

**Ticket:** TDB-9 / issue #9 (P4, under-specified). **Date:** 2026-06-09. **Role:** ARCH-equivalent (inline coverage analysis).
**Disposition: DOCUMENTED DEFER** — resolve the bare open-ended TODO into a tracked, criteria-based reference; record the concrete coverage finding; gate further expansion on a real driver. Comment/doc-only ⇒ zero behavior change (shadow byte-identical).

---

## 1. What the TODO actually is (not what the tracker headline implies)

The bare TODO `// TODO: Add more industry mappings as needed` lives at
`internal/services/datacleaner/service.go:504`, inside `loadIndustryRules`:

```go
func (s *service) loadIndustryRules(industryCode string) error {
    industryFileMap := map[string]string{
        "45": "technology.json",   // GICS Information Technology
        "25": "retail.json",       // GICS Consumer Discretionary
        // TODO: Add more industry mappings as needed
    }
    filename, exists := industryFileMap[industryCode]
    if !exists {
        return fmt.Errorf("no industry rules file found for industry code: %s", industryCode)
    }
    industryRulesPath := fmt.Sprintf("%s/%s", s.config.IndustryRulesPath, filename)
    return s.rulesEngine.LoadIndustryRules(industryRulesPath)
}
```

This is **NOT** about `config/datacleaner/industry_codes.json** (the SIC/keyword classifier),
which is already broad (TECH, FIN, HEALTH, RETAIL, MFG, ENERGY, RESTATE, TELECOM, TRANS, CONS
+ sub-industries incl. all 8 REIT subsectors). It is about **industry-specific datacleaner
rule-override files**: only two exist —
`config/datacleaner/industry/technology.json` and `.../retail.json`.

### Mechanism (evidence)
- `getIndustryCode(data)` (`service.go:865`) returns a **GICS-style sector code** — via
  `industryClassifier.ClassifyIndustry(...).SectorCode` (the balance-sheet heuristic classifier),
  plus a hardcoded test-ticker map (`45`=Tech, `25`=Consumer Disc, `20`=Industrials, `21`=Energy/Chem,
  `62`=Healthcare). The integration-test comment at `datacleaner_ai_test.go:151` confirms:
  "getIndustryCode maps ticker 'FAIL_TEST' → GICS '45'".
- The live cleaner calls `loadIndustryRules(cleaningCtx.IndustryCode)` at `service.go:243`, but
  **only when `EnableIndustry && IndustryCode != ""`**, and the result is **gracefully degraded**
  (`service.go:242-248`):
  ```go
  if err := s.loadIndustryRules(cleaningCtx.IndustryCode); err != nil {
      result.Warnings = append(result.Warnings, fmt.Sprintf("Failed to load industry rules: %v", err))
  } else {
      result.IndustrySpecific = true
  }
  ```
  An uncovered sector ⇒ a warning string + the cleaner **continues on the base rule set**
  (`rules.json`). `result.IndustrySpecific` stays false. **This is a deliberate, working fallback,
  not a bug.**

## 2. Coverage finding (the gating deliverable — concrete)

**Crucial correction (REVIEWER NIT-1, verified):** the *override-file namespace* is the full GICS
sector set, but the **live `IndustryClassifier.ClassifyIndustry` only ever emits 3 sector codes** —
`45`, `20`, `25` — because `loadDefaultConfigurations` (`industry/classifier.go:582/626/670`) defines
exactly those `sectorConfigs` (and defaults to `20`). So the *reachable* production picture is small:

| GICS code | Sector | Classifier emits it? | Override file? | Behavior today |
|---|---|---|---|---|
| `45` | Information Technology | ✅ | ✅ `technology.json` | industry-specific overrides applied |
| `25` | Consumer Discretionary | ✅ | ✅ `retail.json` | industry-specific overrides applied |
| `20` | Industrials | ✅ (also the default) | ❌ | **base rules + non-fatal warning (graceful)** |
| `10/15/30/35/40/50/55/60` | Energy / Materials / Consumer-Staples / Health-Care / Financials / Comm-Services / Utilities / Real-Estate | ❌ (NOT emitted today) | ❌ | unreachable — see below |

(The hardcoded test-ticker map in `getIndustryCode` injects non-canonical `21`/`62` for `CHEM`/`MULTI`
fixtures, but those are test-only and never come from `ClassifyIndustry`.)

**Concrete gap (the gating deliverable):**
- **The ONLY reachable-and-uncovered sector today is `20` (Industrials)** — it degrades gracefully
  (base `rules.json` + a `result.Warnings` note). This is the single real, live coverage gap.
- The other 8 GICS sectors are an **override-file namespace that the current classifier cannot
  produce**. Covering them requires a **classifier change first** (to make `ClassifyIndustry` emit the
  code) AND a domain-authored override file — i.e. strictly more than "add a mapping line."

## 3. Why this is a DEFER, not a quick add

1. **Adding a sector is domain authorship, not a config line.** Each `<sector>.json` is a curated
   set of industry-specific cleaning-rule overrides (what asset/earnings adjustments does *Energy* or
   *Financials* need beyond base rules?). Inventing these without a domain driver produces either a
   no-op file (pointless) or unreviewed adjustments (risky).
2. **It CHANGES cleaner output ⇒ breaks the regression gates.** Loading new override rules alters the
   adjustments fired for every ticker in that sector → `recompute-shadow` snapshots drift →
   `git diff --quiet internal/integration/testdata/recompute-shadow/` would FAIL, requiring a
   reviewed regeneration. (Covering `20`/Industrials — the one reachable-uncovered sector — would
   touch the default bucket and trip shadow immediately.)
   **On Financials specifically (REVIEWER NIT-2, corrected framing):** `TestDDM_LegacyPath_BitForBit`
   reads frozen golden JSON fixtures (`models/testdata/golden/{jpm,bac,wfc}_*`), so it is NOT directly
   driven by the cleaner — a `financials.json` would not perturb that unit test directly. The real
   Financials risk is bigger, not smaller: the classifier cannot even emit `40` today, so covering
   Financials requires (a) a CLASSIFIER change to emit `40`, (b) a domain-authored `financials.json`,
   (c) a recompute-shadow regen, AND (d) end-to-end JPM/BAC/WFC valuation re-validation (the live DDM
   path, not the golden-pinned unit test). Strictly more work than a config edit.
3. **No driver.** The ticket is explicitly UNDER-SPECIFIED / "needs a driver." There is no
   misclassifying ticker and no sector whose base-rule cleaning is demonstrably wrong in hand. P4.

## 4. Disposition — DOCUMENTED DEFER (the action this ticket takes)

1. **Resolve the bare TODO** at `service.go:504` into a tracked, criteria-based note referencing
   TDB-9 / #9 (so no bare `TODO` remains — matches the TDB-10 hygiene standard). The note documents:
   the map is GICS-sector → rule-override file; current coverage (`45`, `25`); the graceful
   fall-through for uncovered sectors; and the **4-step procedure** + driver gate for adding a sector.
2. **Record this coverage analysis** (this spec + the tracker).
3. **No code-behavior change** ⇒ shadow byte-identical, DDM bit-for-bit, all invariants untouched;
   no `CalculationVersion`/`SchemaVersion` bump.

### The replacement note (shipped at `service.go` `loadIndustryRules`)
The bare TODO is replaced by a tracked note documenting: the GICS-code→override-file mapping; that the
live classifier emits only `45`/`20`/`25` (so `20` is the one reachable-uncovered sector and the other
GICS codes are an unreachable namespace); the graceful base-rule fall-through; and a 5-step add
procedure — (1) extend `ClassifyIndustry` if it can't emit the code yet; (2) author a domain-validated
`<sector>.json`; (3) add the code→file entry; (4) add the file to the `ledger_invariants_test.go` sync
list; (5) regen + REVIEW recompute-shadow. For Financials (40) it explicitly notes that
`TestDDM_LegacyPath_BitForBit` is golden-fixture-pinned (won't catch cleaner drift) so JPM/BAC/WFC must
be re-validated end-to-end through the live DDM path. Driver gate: a sector whose base-rule cleaning is
demonstrably wrong, or RM-2. (See the actual comment in `loadIndustryRules` for verbatim wording.)

## 5. Acceptance mapping
- [x] **Concrete list of missing industries identified (the gating step):** §2 — ~9 uncovered GICS
  sectors, with the mechanism and the graceful-fallback behavior. This is the deliverable the ticket
  blocked on.
- [x] **Mappings added + classifier tests, OR documented defer:** DEFER — bare TODO resolved into a
  tracked criteria-based reference; expansion procedure + driver gate documented. (No mappings added
  by design — see §3.)

## 6. Regression safety
Comment/doc-only. No `.go` logic, no `.json` config, no rule file changed. `getIndustryCode`,
`loadIndustryRules`, the classifier, and every rule file are byte-identical in behavior ⇒
`recompute-shadow` byte-identical, `TestDDM_LegacyPath_BitForBit` / `TestRecomputeUmbrellas_NoMutation`
/ `TestOrchestrator_LedgerOrdering` / `TestLedger_BasketSnapshot_*` all unaffected. No version bump.

## 7. Test strategy
No new behavior ⇒ no new unit test required. The validation is: full suite green, named invariants
green, `recompute-shadow` byte-identical (exit 0), and `grep TODO service.go` shows the bare TODO
replaced by a TDB-9-referencing note.
