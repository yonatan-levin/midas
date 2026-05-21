# T2-P4-W1 — Classifier output vs assumption_profiles.json archetype-rule prefix mismatch

**Status:** **MOSTLY RESOLVED — REIT side + P3 + P4 defect-fixups complete; 2 acceptance rows deferred to a true Tier 2 Closeout validation pass** (live API regression on EQIX + PLD + replay regression against `artifacts/tier2-baseline/`). REIT-side fix MERGED to master 2026-05-19 (`be92a79`). P3 + P4 defect-fixup sweep closed the prefix-mismatch defects through merge `362b63b` 2026-05-21 (deletions of dead `fin_small_bank` + `fin_large_bank` rules in P3; rename of `REIT_COMMERCIAL` → `REIT_OFFICE` + add of `reit_specialty` rule in P4). Tracker stays OPEN at `docs/reviewer/` (NOT yet archived) pending the deferred Closeout validation pass.
**Severity:** HIGH (now mitigated for both REIT and FIN paths at the engine + config layer; residual is validation-only — engine and config are consistent end-to-end)
**Filed:** 2026-05-16 by P4 REVIEWER during the parallel B-V-R-Q cycle
**Phase context:** Tier 2 — surfaced during P4 review; same gap applied to P3 (see FIN-side P3 coordination note in the 2026-05-19 audit appendix; closed by P3 defect-fixup sweep)
**Owner:** Tier 2 integration / Closeout phase (deferred validation rows only)
**Chosen reconciliation:** **Option 1 — Update the classifier to emit `REIT_*` / `FIN_*` prefixed forms** (HUMAN decision 2026-05-16; REIT-side + FIN-side defect-fixups complete; only Closeout validation pass remains)

---

## Context

P0b wired profile resolution into `service.go::performValuation`:

```go
facts := profile.Facts{
    Industry:           industryCode,
    IndustryNormalized: strings.ToUpper(strings.TrimSpace(industryCode)),
    ...
}
resolvedProfile, resolutionTrace := s.profileRegistry.Resolve(facts)
```

The resolver uses prefix-match against `industry_prefix` strings in `config/assumption_profiles.json`. Tier 2 spec §4.1 (line 415) uses the convention `REIT_DATACENTER`, `REIT_CELLTOWER`, etc., for REIT subsectors and `FIN_LARGE_BANK`, `FIN_SMALL_BANK`, `FIN_INSURANCE` for financials.

But the live `industry.Classifier` emits **bare subsector codes**: `DATA_CENTER`, `CELLTOWER`, `RESIDENTIAL`, `INDUSTRIAL`, `RETAIL_REIT`, `HEALTHCARE_REIT`, `SPECIALTY`. Confirmed via:

- `internal/services/datacleaner/industry/classifier_val3p1_reit_test.go:37` — asserts `Classify(...)` returns `"DATA_CENTER"`, not `"REIT_DATACENTER"`
- `config/industry_multiples.json` — keys are bare (`"DATA_CENTER": 31.0`, `"CELLTOWER": 25.0`, etc.)
- `internal/services/valuation/models/router.go:194-205` — REIT routing set lists bare codes (RESIDENTIAL, OFFICE, INDUSTRIAL, …) alongside parent labels (REIT, RESTATE)

Effect: a real DLR / EQIX / AMT / PLD / DRE / O / SPG request produces `Facts.IndustryNormalized = "DATA_CENTER"` (or analog), which **does NOT prefix-match** any rule whose `industry_prefix` is `"REIT_DATACENTER"`. The resolver falls through to the wildcard fallback `software_like_scaling:standard_growth` (horizon=5, terminal=4.0), and the P4 forward FFO path runs against a meaningless software-shaped profile — or stays dormant if the model is FFO-routed (the router uses bare codes too, so routing still works; only the profile resolution flips).

Same gap affects P3: rule `fin_small_bank` (`industry_prefix: "FIN_SMALL_BANK"`) and `insurance` (`industry_prefix: "FIN_INSURANCE"`) don't match if the classifier emits bare `SMALL_BANK` / `INSURANCE` codes. (P3 was not independently checked for this during review; verify in fix work.)

## Why Option 1 (update classifier) was chosen

Three reconciliation strategies were considered:

| Option | Mechanism | Blast radius | Pros | Cons |
|---|---|---|---|---|
| **1 (CHOSEN)** | Update classifier to emit `REIT_*` / `FIN_*` prefixed forms | LARGEST | Aligns with spec convention; one canonical form everywhere | Affects every existing classifier consumer (industry_multiples.json keys, FFO subsector lookup, router REIT-set, datacleaner industry-code config) — coordinated multi-file migration |
| 2 | Update JSON archetype rules to use bare codes | SMALLEST | JSON-only edit; spec text unchanged | Spec text diverges from reality; future docs drift |
| 3 | Add normalization layer in Facts construction | MEDIUM | Spec + classifier both unchanged | Two parallel naming forms persist; cognitive overhead |

Option 1 was chosen because the spec's `REIT_*` / `FIN_*` convention is more discoverable (the prefix anchors the category) and because a single canonical form everywhere reduces long-term cognitive overhead at the cost of one focused migration commit.

## Affected files (migration scope)

### Classifier output

- `internal/services/datacleaner/industry/classifier.go` — `Classify()` and any sub-sector pass-2 logic that returns bare codes; change to emit prefixed forms
- `internal/services/datacleaner/industry/classifier_val3p1_reit_test.go` — update asserted return values
- `internal/services/datacleaner/industry/classifier_test.go` and other classifier tests — update asserted return values
- `config/datacleaner/industry_codes.json` (or wherever bare codes are mapped from SIC/NAICS) — update emission targets

### Downstream consumers that read classifier output

- `config/industry_multiples.json` — `reit_pffo_multiples` keys + `reit_cap_rates` keys (rename `DATA_CENTER` → `REIT_DATACENTER`, etc.; `SMALL_BANK` → `FIN_SMALL_BANK` if it exists)
- `internal/services/valuation/models/ffo.go::getMultiple` / `getCapRate` — subsector lookup keys (already uses `lookupSubsectorValue` which matches prefix; needs the JSON keys updated, NOT the lookup logic — verify)
- `internal/services/valuation/models/router.go:194-205` — `reitIndustrySet` map; bare codes (RESIDENTIAL, OFFICE, etc.) need to become prefixed (REIT_RESIDENTIAL, REIT_OFFICE, etc.) OR keep bare-code routing if the router stays "below" the prefix-convention layer
- `internal/api/v1/handlers/fair_value.go` — `sicToGICS` map keys + `BuildIndustryFromResult` may need awareness if it surfaces industry codes in the response
- Any other test that asserts bare-code classifier output

### Already-correct files (no change needed)

- `config/assumption_profiles.json` — P3 and P4 rules use `REIT_*` / `FIN_*` prefix; they were spec-faithful from the start
- `docs/refactoring/spec/assumption-profile-spec.md` §4.1 — the convention

## Acceptance criteria

- [ ] Classifier emits `REIT_*` / `FIN_*` prefixed forms; classifier tests assert the new outputs
- [ ] `config/industry_multiples.json` keys updated (subsector multiple + cap-rate maps)
- [ ] FFO subsector lookup continues to work end-to-end against the new classifier output
- [ ] Router REIT-set updated to match new classifier output (keep both prefixed and parent-label forms if needed)
- [ ] P3 fin_small_bank + insurance rules verified to fire against new classifier output for synthetic FIN_SMALL_BANK / FIN_INSURANCE fixtures
- [ ] P4 REIT subsector rules verified to fire against new classifier output for synthetic REIT_DATACENTER / REIT_CELLTOWER / REIT_INDUSTRIAL / etc. fixtures
- [ ] Tier 2 forward FFO path produces real ForwardValue values (not the fallback `software_like_scaling:standard_growth`) for live REIT requests
- [ ] Live API regression on EQIX + PLD shows the correct profile (`reit_datacenter:high_growth`, `reit_industrial:standard_growth`) in the response's `assumption_profile` field — NOT `software_like_scaling:standard_growth`
- [ ] Replay regression against `artifacts/tier2-baseline/` for EQIX + PLD shows the new prefixed industry code AND the correct resolved profile

## Open questions for human review

- **Is renaming the classifier's emitted codes a breaking change for external consumers?** If any persisted SQLite rows (`FinancialData.IndustryCode` field per `entities/financial_data.go:18`) carry bare codes today, those would need either a migration or a read-time normalization shim. Investigate before merging.
- **Should the classifier emit BOTH forms (e.g., `REIT_DATACENTER` as primary + `DATA_CENTER` as alias)?** Could ease the migration but doubles surface area; probably unnecessary if all consumers are updated atomically.
- **Should this fix go into a dedicated commit on master BEFORE Tier 2 phases merge?** That way each P3/P4 worktree rebases onto a master where the classifier already emits prefixed forms, and the rules light up immediately on merge. Alternative: bundle the classifier fix into the Closeout phase, knowing P3+P4 ship inert and only activate after Closeout lands.

## Closing this tracker

Move to `docs/reviewer/archive/` once:
- Classifier emits prefixed forms across all tests
- Downstream consumers (industry_multiples.json, FFO lookup, router REIT-set, etc.) are updated
- Live API regression for EQIX + PLD confirms the correct profile flows end-to-end
- Replay regression against tier2-baseline bundles shows the new prefixed codes

This tracker MUST close before Tier 2 ships to master.

---

## Update — 2026-05-19 BACKEND/audit findings (status: REIT-side DONE; FIN-side P3 coordination note)

The REIT-side reconciliation is complete in this commit. The FIN-side audit produced findings that differ from the tracker's original premise; documented here so P3 (in-flight on `tier2-p3`) can coordinate.

### REIT side — DONE

- `config/datacleaner/industry_codes.json`: 8 subsector codes renamed to `REIT_*` prefixed form (DATA_CENTER → REIT_DATACENTER, CELLTOWER → REIT_CELLTOWER, HEALTHCARE_REIT → REIT_HEALTHCARE, RETAIL_REIT → REIT_RETAIL, INDUSTRIAL → REIT_INDUSTRIAL, RESIDENTIAL → REIT_RESIDENTIAL, OFFICE → REIT_OFFICE, SPECIALTY → REIT_SPECIALTY). Because the classifier is config-driven (reads `code` field directly from the JSON), this is the equivalent of editing the classifier's emission.
- `config/industry_multiples.json` v1.3.0: `reit_pffo_multiples` and `reit_cap_rates` map keys renamed to match.
- `internal/services/valuation/models/router.go::reitIndustrySet`: 8 entries renamed to prefixed form + defensive `strings.HasPrefix(upperIndustry, "REIT_")` fallback so any future REIT subsector that ships in `industry_codes.json` alone still routes to FFO (forward-compatibility with P4-style additions).
- `internal/services/valuation/models/ffo.go`: comment + subsector-table doc text updated to the new key convention. `lookupSubsectorValue` longest-prefix-match logic unchanged (was already prefix-tolerant by design).
- `internal/api/v1/handlers/fair_value.go::sicToGICS`: 8 REIT subsector entries renamed to `REIT_*`. The parent-strip fallback in `matchSICToGICS` is intentionally NOT used for REIT_* codes — `"REIT"` is not a key in `sicToGICS`, so the full-code exact-match path is the only resolution path for REIT subsectors (documented in the lookup-order comment).
- All tests updated and green: `classifier_val3p1_reit_test.go`, `ffo_subsector_test.go`, `fair_value_test.go`, plus the entire `go test ./...` suite (47 packages, clean cache).

### FIN side — different from tracker premise; no work needed on master

The tracker originally speculated: "Same gap affects P3: ... if the classifier emits bare `SMALL_BANK` / `INSURANCE` codes." Audit found the actual state is:

- `FIN_INSURANCE` — classifier already emits the prefixed form (no rename needed). Future P3 `insurance` rule (`industry_prefix: "FIN_INSURANCE"`) will fire correctly.
- `FIN_BANK` — classifier emits a unified bank code with no large/small split. On master this matches the existing `fin_generic` rule (`industry_prefix: "FIN"`, archetype `mature_large_bank`) which keeps the JPM bit-for-bit DDM legacy path intact.

**For P3 to coordinate**: if `tier2-p3` introduces `fin_small_bank` / `fin_large_bank` rules with explicit `FIN_SMALL_BANK` / `FIN_LARGE_BANK` prefixes, those rules will NOT fire against today's `FIN_BANK` emission. P3 must choose one of:

1. Add size-based bank bucketing to the classifier (split `FIN_BANK` into `FIN_LARGE_BANK` vs `FIN_SMALL_BANK` based on total assets); or
2. Implement size bucketing inside the `Resolve()` algorithm's maturity-bucketing stage (uses `Facts.TotalAssets` / `Facts.Revenue`); or
3. Keep a single `fin_bank` rule (`industry_prefix: "FIN_BANK"`, archetype `mature_large_bank`) and defer large/small differentiation to a later phase.

Option 3 has zero risk to the JPM bit-for-bit invariant and is the most conservative choice for the initial P3 merge.

### Remaining acceptance rows (deferred to Tier 2 Closeout)

- Live API regression on EQIX + PLD — deferred; requires P4 (`tier2-p4`) to merge so REIT-specific archetype rules exist in `config/assumption_profiles.json` to exercise the new classifier emission against
- Replay regression against `artifacts/tier2-baseline/` — same deferral

These two rows will be re-validated during the Tier 2 Closeout phase, not this tracker's BACKEND fix.


---

## Update — 2026-05-21 Tier 2 Closeout — all engine + config defects closed; 2 validation rows remain deferred

All Tier 2 phase merges + cross-phase defect-fixup commits have landed on master through merge `362b63b` (tier2-p4). The four prefix-mismatch defects originally enumerated against this tracker (REIT + FIN) are now closed at the engine + config layer; the remaining acceptance work is purely the deferred validation pass (live API + replay regression) that requires the merged engine to run against external systems.

### Defects closed by P3 + P4 defect-fixup commits

1. **REIT-side prefix reconciliation** — classifier emits `REIT_*` prefixed subsector codes; downstream consumers (industry_multiples.json, router REIT-set, FFO subsector lookup, sicToGICS) atomically updated. Original merge `be92a79` 2026-05-19 (REIT-side fix on then-current master); held through all three Tier 2 phase merges that followed. **CLOSED.**
2. **Dead `fin_small_bank` + `fin_large_bank` archetype rules** — P3 originally introduced these rules with `FIN_SMALL_BANK` / `FIN_LARGE_BANK` prefixes that the classifier never emits (today's classifier emits unified `FIN_BANK`). Defect-fixup commit `5a72208` on `tier2-p3` deleted both rules. JPM continues to route via `fin_generic` (industry_prefix `FIN`, archetype `mature_large_bank`) which preserves the bit-for-bit DDM invariant. Bank size-bucketing deferred to a future phase (3 options enumerated above). **CLOSED.**
3. **`REIT_COMMERCIAL` prefix mismatch** — P4 originally shipped `reit_commercial` archetype rule with `industry_prefix: "REIT_COMMERCIAL"`, but the T2-P4-W1 REIT-side reconciliation already renamed the office-REIT classifier emission to `REIT_OFFICE` (not `REIT_COMMERCIAL`). Defect-fixup commit `b8853c7` on `tier2-p4` renamed the rule's prefix to `REIT_OFFICE` while keeping the archetype id `reit_commercial` for type-system continuity. **CLOSED.**
4. **Missing `reit_specialty` archetype rule** — P4 originally shipped REIT subsector rules but missed the SPECIALTY subsector (self-storage / billboard / prison / timber REITs — Public Storage, Lamar, CoreCivic, Weyerhaeuser). Same defect-fixup commit `b8853c7` added the `reit_specialty` profile + archetype rule (priority 100, `REIT_SPECIALTY` prefix). **CLOSED.**

Final state on master after Tier 2 close: **31 profiles + 19 rules** in `config/assumption_profiles.json` (P0b 2 + P1 6 + P2 6 + P3 6 + P4 11 = 31; `fin_generic` + `insurance` + 5 P1 + 3 P2 + 8 P4 + `fallback_default` = 19). All 8 REIT subsectors have working archetype rules: `REIT_RESIDENTIAL`, `REIT_OFFICE`, `REIT_INDUSTRIAL`, `REIT_HEALTHCARE`, `REIT_DATACENTER`, `REIT_CELLTOWER`, `REIT_RETAIL`, `REIT_SPECIALTY`. Load-bearing invariants intact: JPM/BAC/WFC bit-for-bit DDM 3/3 PASS, `pkg/finance/*` D7 empty diff.

### Acceptance rows still deferred to a future validation pass

These two rows from the original acceptance checklist are NOT yet validated end-to-end against the live engine. They require a separate Closeout validation step (a Tier 2 Closeout validation sweep, distinct from this docs sweep) to flip from deferred → satisfied:

- **Live API regression on EQIX + PLD** — start the server with the post-Tier-2 master, POST against `/v1/fair-value` for EQIX and PLD, confirm the response's `assumption_profile` field reads `reit_datacenter:high_growth` (EQIX) and `reit_industrial:standard_growth` (PLD), NOT the wildcard `software_like_scaling:standard_growth`. Test pins in `tier2_regression_test.go::TestTier2_EQIX_Pin` and `TestTier2_PLD_Pin` provide the synthetic-fixture equivalent; the deferred row is the live-API end-to-end variant.
- **Replay regression against `artifacts/tier2-baseline/`** — run `go run ./cmd/replay --diff-stages --from=parsed artifacts/tier2-baseline/` across the 10-ticker basket and confirm REIT-bundle profiles resolve to the correct REIT_* archetype rather than the wildcard fallback. Authorized additive drift on `assumption_profile`, `dcf_per_year_pv`, `resolution_trace`, and forward-FFO/forward-revenue fields is expected and acceptable; what should NOT appear is profile resolution falling through to `software_like_scaling:standard_growth` for any of the REIT bundles.

Both rows will be validated in a follow-on Closeout validation sweep (separate session). Until then, this tracker stays OPEN at `docs/reviewer/` (NOT moved to `archive/`).

### Closing this tracker (revised)

Move to `docs/reviewer/archive/` once:
- Live API regression on EQIX + PLD confirms the correct profile flows end-to-end on the live engine
- Replay regression against tier2-baseline bundles confirms REIT bundles resolve to REIT_* archetypes (not wildcard)

The 4 engine + config defects above are already closed and do not need to re-validate to archive this tracker — only the 2 deferred rows above.
