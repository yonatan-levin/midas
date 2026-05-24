# DC-1 Phase 2 — Implementer Handoff

**Phase:** Phase 2 — Unified `Adjuster` interface + `AdjustmentLedger` (single interface; Restater / Overlay / Hybrid roles distinguished by output)
**Status:** READY TO START
**Estimated effort:** 2-3 weeks per the DC-1 spec (Layer 2 — materially larger than Phase 1's shadow-mode shim)
**Master HEAD when Phase 1 closed:** `7a08506` (2026-05-19) — full Phase 1 chronology: `2d916a7` (main merge) → `b8e9c77` (followup merge) → `5544e74` (T2-BS-3 filed) → `7a08506` (tier2-baseline refresh to 10/10)
**Prerequisites:**
- Phase 0 SHIPPED — see [Phase 0 closeout](../archive/datacleaner-component-primitive-and-parallel-views-phase-0-closeout.md)
- Phase 1 SHIPPED — see [Phase 1 closeout](../archive/datacleaner-component-primitive-and-parallel-views-phase-1-closeout.md) + [Phase 1 shadow-analysis report](datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md)
- **T2-BS-3 disposition decision required** before BACKEND dispatch — see `docs/reviewer/T2-BS-3-parser-totalliabilities-zero-amd-ko.md`. ARCH must choose between fixing the AMD/KO parser dropout (Option A) or carving it out via the `Restated` view (Option B). Option B is the path of least resistance for Phase 2 scope; Option A is the more "true to source data" path that adds parser-side scope. Recommend Option B unless the user has a strong opinion otherwise.

---

## TL;DR

Phase 2 introduces the `Adjuster` interface (a single Go interface; the Restater / Overlay / Hybrid distinction emerges from the output shape, not from interface multiplication) and refactors every existing adjuster in `internal/services/datacleaner/adjustments/{assets,liabilities,earnings}.go` to implement it. The interface produces explicit `LedgerEntry` records appended to a new `AdjustmentLedger` field on `FinancialData`, which downstream Phase 3 view-reconstruction work consumes. Phase 2 does NOT yet introduce `CleanedFinancialData {AsReported, Restated, InvestedCapital}` (Phase 3) and does NOT migrate any consumer of `data.TotalAssets` (Phase 4).

**The success signal**: the committed snapshot files at `internal/integration/testdata/recompute-shadow/<TICKER>.json` change in well-understood ways. Each PR's snapshot diff is reviewed against the shadow-analysis report's 7-cluster punch list — a snapshot change that matches a predicted cluster reroute is correct; an unexpected change is a regression.

---

## Required reading (in order)

Stop at the first tier that gives you enough context:

### Tier 1 — Identity & direction (always read)

1. **`CLAUDE.md`** — project conventions, especially:
   - Code style: structured logging via `logctx.From(ctx)`, no globals, table-driven tests
   - "Common Gotchas" → DC-1 entries (Phase 0 + Phase 1 SHIPPED)
   - "Common Gotchas" → Tier 2 bit-for-bit DDM invariant (LOAD-BEARING)
2. **`AGENTS.md`** — Tier 4 row for DC-1
3. **`docs/THESIS.md`** — DC-1 row in the Phases table (in-flight status with Phase 0 + Phase 1 SHIPPED)

### Tier 2 — DC-1 design + Phase 1 ground truth

4. **`docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`** — the design spec. Focus on:
   - "Solution shape" — three views, one ledger, lazy reconstruction
   - "Phasing & implementation sequence" → Phase 2 row + the Phase 1 → Phase 2 gate
   - "Adjuster interface" — the proposed shape (refine during ARCH cycle if needed)
   - "AdjustmentLedger" — entry schema + invariants
5. **`docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md`** — **the load-bearing input for Phase 2's punch list.** Read in full. The 7 clusters with adjuster-line correlations ARE Phase 2's target list.
6. **`docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-1-closeout.md`** — what landed in Phase 1, V-R-Q outcomes, lessons learned (especially: master-drift during multi-week phases, gopls workspace artifacts, snapshot quantization).
7. **`docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md`** — the live tracker
8. **`docs/reviewer/T2-BS-3-parser-totalliabilities-zero-amd-ko.md`** — the parser-side prerequisite. Read the "Phase 2 dependency analysis" section. ARCH chooses Option A or B.

### Tier 3 — Code locations to inspect before designing

9. **`internal/services/datacleaner/adjustments/assets.go`** — the 4 `data.TotalAssets -= ...` mutation sites at lines ~69, ~157, ~232, ~308. **These are Phase 2's primary refactor targets.** Read each adjuster end-to-end to understand its current contract before redesigning.
10. **`internal/services/datacleaner/adjustments/liabilities.go`** — the `data.TotalDebt += result.Amount` orchestrator at lines ~87-88 plus the B1/B2/B3 lease-capitalization rules. **The AAPL FY2023-2025 ~$12B TotalLiabilities divergence cluster traces back here.**
11. **`internal/services/datacleaner/adjustments/earnings.go`** — earnings adjusters (Phase 2 includes these in the interface refactor; check if they're already roughly Adjuster-shaped).
12. **`internal/services/datacleaner/service.go`** — `CleanFinancialData` orchestrator. The current pipeline calls `applyActiveAdjustments` somewhere around lines 215-217; Phase 2 may refactor this to drive the new Adjuster interface.
13. **`internal/services/datacleaner/recompute.go`** — Phase 1's shadow shim. **DO NOT MODIFY in Phase 2.** Its WARN logs and snapshot artifacts are Phase 2's regression signal — if you change it, you lose the signal. Confirm by reading the godoc.
14. **`internal/core/entities/financial_data.go`** — Phase 2 adds a new `AdjustmentLedger []LedgerEntry` field. Confirm where to slot it relative to the existing Phase 0 plug fields (around lines 105-136).
15. **`internal/integration/testdata/recompute-shadow/<TICKER>.json` (all 10)** — read at least AAPL, AMD, MXL, and JNJ. These are the regression signal. Internalize what the records look like, what `clamp_suspected: true` vs `false` means, and how Phase 2's reroutes will move records out of the divergence set.

---

## Phase 2 scope

### What to BUILD

1. **`Adjuster` interface** at `internal/services/datacleaner/adjustments/adjuster.go` (NEW file). The interface produces `AdjusterOutput` containing zero or more `LedgerEntry` records. Refine the exact signature during ARCH cycle; the spec proposes:
   ```go
   type Adjuster interface {
       Name() string
       Apply(ctx context.Context, fd *entities.FinancialData) (AdjusterOutput, error)
   }
   type AdjusterOutput struct {
       Entries []LedgerEntry
       // ...other fields TBD by ARCH
   }
   ```
2. **`LedgerEntry` entity** at `internal/core/entities/adjustment_ledger.go` (NEW file). Schema TBD by ARCH; at minimum: `Source` (adjuster name), `Type` (Restater | Overlay | Hybrid), `FromAccount`, `ToAccount`, `Amount`, `EquityOffset` (for Restaters), `Reason` (free text). Spec describes the invariants.
3. **`AdjustmentLedger` field** on `entities.FinancialData` — slice of `LedgerEntry`. Populated by the orchestrator after each adjuster runs.
4. **Refactor every existing adjuster** in `adjustments/assets.go`, `adjustments/liabilities.go`, and `adjustments/earnings.go` to implement `Adjuster`. The existing direct-mutation pattern (`data.TotalAssets -= X`) becomes either:
   - A `Restater`-shaped LedgerEntry that mutates components AND records the equity offset, OR
   - An `Overlay`-shaped LedgerEntry that mutates nothing on `FinancialData` directly (the overlay applies later in Phase 3's view reconstruction)
5. **A1 (goodwill exclusion) becomes an Overlay**, per the spec's explicit Damodaran-convention preservation. Goodwill stays in `AsReported.TotalAssets` and `Restated.TotalAssets`; only `InvestedCapital.TotalAssets` (Phase 3) excludes it via the overlay record.
6. **A2 / A5 become Restaters** with explicit equity offsets. A2 intangible writedown reduces `Goodwill` (or `OtherIntangibles`) AND adds a matching offset to `StockholdersEquity`. A5 inventory writedown reduces `Inventory` AND adds a matching offset.
7. **B1/B2/B3 lease capitalization** — these currently mutate `TotalDebt` via the orchestrator at `liabilities.go:87-88`. Reroute via the Adjuster interface so they ALSO populate the corresponding lease-component fields (`OperatingLeaseLiabilityCurrent`, `OperatingLeaseLiabilityNoncurrent`) such that `recomputeUmbrellas` no longer flags the AAPL TL divergence cluster.
8. **Orchestrator wiring** in `internal/services/datacleaner/service.go` — `applyActiveAdjustments` runs each registered Adjuster, appends LedgerEntries to `data.AdjustmentLedger`, and applies any direct mutations specified by the Adjuster output.
9. **Tests:**
   - Unit test per refactored adjuster confirming `LedgerEntry` shape + correct mutation/overlay behavior.
   - Property test: for any well-formed `FinancialData`, after `applyActiveAdjustments`, `sum(restater equity offsets) == 0` for any view that should balance.
   - Update `internal/integration/datacleaner_recompute_shadow_test.go` — committed snapshots will CHANGE for tickers whose Phase 2 reroutes move divergences out of the recorded set. Document the snapshot diff in the PR.
10. **Documentation updates**: CLAUDE.md DC-1 entry adds Phase 2 SHIPPED note; TESTING.md plug-invariant subsection extends with Adjuster contract; spec changelog row; DC-1 tracker progress paragraph; THESIS.md row.

### What to NOT build

- Do NOT introduce `CleanedFinancialData {AsReported, Restated, InvestedCapital}` — Phase 3.
- Do NOT migrate any consumer of `data.TotalAssets` to read from a view — Phase 4.
- Do NOT close the SQLite persistence gap for the plug fields (`TestFinancialDataRepository_PlugFields_PersistenceGap` stays as a flip-gate). Defer until a consumer actually needs to read plugs from a cached row. Phase 2 only needs in-flight `FinancialData`.
- Do NOT modify `internal/services/datacleaner/recompute.go` — its WARN logs + the committed snapshot artifacts are Phase 2's regression signal.
- Do NOT change `*entities.FinancialData`'s existing field types or names — only ADD `AdjustmentLedger`. Existing consumers must continue compiling against the same shape.
- Do NOT touch `internal/services/valuation/*` — Tier 2 territory (P1-P4 worktrees may still be live).
- Do NOT introduce a new HTTP response shape or OpenAPI surface — the ledger is internal-only until Phase 4 surfaces it via consumer migration (if at all).

### Critical invariants (Phase 2 must preserve)

1. **Snapshot files in `internal/integration/testdata/recompute-shadow/` are the Phase 2 regression signal.** Every PR's snapshot diff must be explainable against the shadow-analysis cluster predictions:
   - Cluster B1 (AAPL TL drift) → after Phase 2's lease reroute, AAPL.json should show ZERO TotalLiabilities divergences (or substantially fewer).
   - Cluster A1-A5 patterns (MXL multi-umbrella) → similar reductions.
   - Cluster B1-PARSER-TL-ZERO (AMD/KO TL=0) → unchanged if Option B is chosen; resolved if Option A.
   - **Any snapshot change that is NOT predicted by a cluster is a regression — reject the PR.**
2. **`TestRecomputeUmbrellas_NoMutation` stays green.** The Phase 1 no-mutation invariant on the shadow shim itself is independent of Phase 2's work.
3. **Tier 2 mature-large-bank DDM bit-for-bit invariant.** Phase 2 must not alter the DDM math for JPM/BAC/WFC. `TestDDM_LegacyPath_BitForBit` (in `internal/services/valuation/models/`) is LOAD-BEARING. Phase 2 is in `datacleaner/` territory but adjuster reroutes can cascade — verify after the refactor.
4. **Phase 0 plug invariants hold.** `computePlugs` math at `internal/infra/gateways/sec/plugs.go` is unchanged.
5. **`recomputeUmbrellas` WARN field set is a contract.** 10 fields (ticker, period, cik, umbrella, reported, recomputed, delta, plug, clamp_suspected, phase). Do not drift without coordinating with downstream log consumers (currently none per Phase 1 QA grep, but check again).
6. **Full test suite stays green** (modulo SCHED-1 scheduler flake).

---

## Known gotchas

1. **T2-BS-3 parser prerequisite is a decision gate, not a blocker.** ARCH must explicitly choose Option A (parser-side fix, larger scope) or Option B (carve-out via Restated view, cleaner Phase 2 scope). Document the choice in the Phase 2 plan and reference T2-BS-3 from the plan.

2. **Two `tier2-baseline/<date>/` dirs coexist** (2026-05-15 from Bootstrap, 2026-05-19 from Phase 1 followup). The integration test at `datacleaner_recompute_shadow_test.go` resolves to the newest dir. If Phase 2 captures fresh bundles into a `2026-XX-YY/` dir with all 10 tickers, that becomes the new baseline; older dirs can be retired in a follow-up commit once Tier 2 worktrees rebase past Phase 2's master tip.

3. **Tier 2 worktrees may still be live.** As of master `7a08506`, `tier2-p1` through `tier2-p4` are sibling worktrees on `internal/services/valuation/`. Phase 2's scope (`internal/services/datacleaner/adjustments/`) does NOT overlap, but the orchestrator wiring at `internal/services/datacleaner/service.go` is a watch-out — if Tier 2's P1-P4 introduce new dependencies on the cleaner pipeline shape, coordinate.

4. **Snapshot drift will be intentional in Phase 2** (unlike Phase 1, where any drift was a regression). REVIEWER must shift mental model: snapshot diffs are now welcome IF predicted by a cluster, hostile IF unpredicted. Document this in the Phase 2 implementation plan so REVIEWER doesn't reject good drift.

5. **CRLF/LF warnings on Windows** are noisy but cosmetic — git's autocrlf normalizes on commit. Pre-commit the snapshot regen by running the integration test and committing the changes as a SEPARATE commit ahead of the adjuster refactor, so the adjuster commits show ONLY the reroute-driven snapshot changes (cleaner reviewer signal).

6. **The pre-existing `assumption_profile` replay drift is RESOLVED** for `tier2-baseline/2026-05-19/` (bundles include `08-assumption-profile.json`). If Phase 2 captures fresh bundles, this carve-out is no longer needed.

7. **SCHED-1 scheduler flake is intermittent.** Pre-existing race; not introduced by DC-1. Document if it fires; don't chase.

8. **gopls cross-worktree noise** — same as Phase 1. Diagnostics like "undefined: DataCleanerService" against sibling worktrees are workspace-config artifacts. Run `go build` and `go test` from inside the active worktree; ignore the editor diagnostics.

9. **Subagent dispatch discipline.** Phase 1 hit two BACKEND limits; the recovery pattern is: controller inspects uncommitted work, completes inline if mechanical, then resumes V-R-Q. Don't dispatch fresh subagents for sub-3-edit follow-ups.

10. **Master-drift during multi-week phases.** Phase 1's followup hit this: Tier 2 P4 work merged to master during the followup BACKEND cycle. Phase 2 is 2-3 weeks, so the probability is higher. Recommended cadence: BACKEND `git fetch && git merge master` weekly during the phase to keep drift small. The recovery pattern (merge master into feature branch, resolve THESIS.md / CLAUDE.md conflicts, then merge feature back to master) is documented in Phase 1's followup merge `b8e9c77`.

---

## Suggested implementation approach

1. **Start with an ARCH cycle** (`/plan-and-create` skill). The spec describes Phase 2 at a high level; ARCH produces a focused implementation plan at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md`. The plan MUST include:
   - T2-BS-3 disposition (Option A or B) with explicit rationale
   - Final `Adjuster` / `AdjusterOutput` / `LedgerEntry` signatures
   - File-by-file deltas for every adjuster (8+ adjusters to refactor)
   - Per-adjuster predicted snapshot diff (which cluster does this adjuster resolve?)
   - Test plan including the property-based "equity offsets sum to zero" invariant
   - Phase 2 → Phase 3 gate criteria (what does Phase 3 ARCH need from Phase 2's output?)
   - Risks: the cascading risk to Tier 2 DDM bit-for-bit invariant; mitigation via early test runs after first 2-3 adjusters

2. **Single worktree, sequential execution.** Phase 2's scope is much larger than Phase 1 (8+ adjusters vs. 1 new file), but the work is sequentially dependent (each adjuster's reroute depends on the interface design landing first). Parallel fan-out adds setup cost without saving wall-clock time at this scale.

3. **Phase 2 is a strong candidate for incremental merges** — consider landing the Adjuster interface + LedgerEntry skeleton in PR1 (~3-5 days), then refactor adjusters 2-3 at a time in PR2/PR3/PR4 (~1 week each). Each PR's V-R-Q cycle keeps reviewer load manageable. The snapshot diff per PR is the proof.

4. **Full ABVRQ chain per PR.** BACKEND in the worktree → VERIFIER independent re-run → REVIEWER code quality + snapshot-diff cluster-predicted-vs-not classification → QA spec conformance + Phase 1 → Phase 2 gate input fidelity → HUMAN merge approval.

5. **Empirical replay validation per PR.** Before merge, run `cmd/replay --from=parsed --diff-stages` against AAPL + MSFT + JPM (the Tier 2 bit-for-bit ticker) using the 2026-05-19 baseline. Any unexpected drift in `17-response.json`'s financial fields = REJECT.

6. **After full Phase 2 merge, file the closeout report and shadow-analysis-v2.** The shadow-analysis-v2 enumerates the post-Phase-2 divergence set and confirms which clusters Phase 3 still needs to address (any residual divergences are the Phase 3 view-reconstruction targets).

---

## Acceptance criteria for closing Phase 2

- [ ] T2-BS-3 disposition decided and documented in Phase 2 plan
- [ ] `Adjuster` interface lands at `internal/services/datacleaner/adjustments/adjuster.go` with godoc explaining Restater / Overlay / Hybrid output semantics
- [ ] `LedgerEntry` entity lands at `internal/core/entities/adjustment_ledger.go` with field-level godoc
- [ ] `AdjustmentLedger []LedgerEntry` field added to `entities.FinancialData`
- [ ] All adjusters in `adjustments/{assets,liabilities,earnings}.go` implement `Adjuster`; direct `data.TotalAssets -=` style mutations replaced
- [ ] A1 goodwill exclusion is an Overlay (preserves Damodaran convention)
- [ ] A2 / A5 are Restaters with explicit equity offsets (post-clean `Assets ≈ Liabilities + Equity` invariant holds at the component level)
- [ ] B1/B2/B3 lease capitalization populates lease-component fields, NOT just `TotalDebt`
- [ ] Property test pins "sum of Restater equity offsets == 0" for any well-formed input
- [ ] Per-adjuster unit tests cover LedgerEntry shape + mutation behavior
- [ ] Committed snapshot diffs in `recompute-shadow/<TICKER>.json` are documented in the PR and match the shadow-analysis cluster predictions:
  - [ ] AAPL.json: TotalLiabilities cluster resolved (FY2023-2025 ~$12B divergences gone)
  - [ ] MXL.json: TotalAssets multi-cluster divergences substantially reduced
  - [ ] AMD.json + KO.json: TotalLiabilities=0 pattern resolved (Option A) OR unchanged (Option B + carve-out note)
  - [ ] All other tickers: any change is explained
- [ ] Full test suite green (modulo SCHED-1 flake)
- [ ] `TestRecomputeUmbrellas_NoMutation` still green
- [ ] `TestDDM_LegacyPath_BitForBit` still green (Tier 2 invariant)
- [ ] Replay validation on AAPL + MSFT + JPM shows only timestamp drift in `17-response.json`'s financial fields
- [ ] CLAUDE.md DC-1 Common Gotchas entry updated to note Phase 2 SHIPPED
- [ ] TESTING.md extended with Adjuster contract + equity-offset invariant subsection
- [ ] Spec changelog row for Phase 2 SHIPPED
- [ ] DC-1 tracker progress paragraph for Phase 2
- [ ] THESIS.md DC-1 row status bumped
- [ ] Shadow-analysis-v2 report filed post-merge enumerating residual divergences as Phase 3 input
- [ ] Phase 2 closeout report filed post-merge (use this handoff's structure as input + Phase 1 closeout as template)

---

## Files you will likely create

1. `internal/services/datacleaner/adjustments/adjuster.go` (NEW) — Adjuster interface + AdjusterOutput
2. `internal/services/datacleaner/adjustments/adjuster_test.go` (NEW) — interface contract test
3. `internal/core/entities/adjustment_ledger.go` (NEW) — LedgerEntry entity + invariants
4. `internal/core/entities/adjustment_ledger_test.go` (NEW)
5. `internal/services/datacleaner/adjustments/ledger_invariants_test.go` (NEW) — property test for equity-offset zero-sum
6. `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md` (NEW — ARCH output)
7. `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-shadow-analysis.md` (NEW — post-merge)
8. `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-closeout.md` (NEW — at close)

## Files you will likely modify

1. `internal/services/datacleaner/adjustments/assets.go` — all 4 mutation sites become Adjuster implementations
2. `internal/services/datacleaner/adjustments/liabilities.go` — orchestrator + B1/B2/B3 rules become Adjuster implementations
3. `internal/services/datacleaner/adjustments/earnings.go` — earnings adjusters
4. `internal/services/datacleaner/service.go` — `applyActiveAdjustments` orchestrator drives the Adjuster interface; populates `AdjustmentLedger`
5. `internal/core/entities/financial_data.go` — add `AdjustmentLedger []LedgerEntry` field
6. `internal/integration/testdata/recompute-shadow/<TICKER>.json` (multiple) — snapshot diffs from adjuster reroutes
7. `CLAUDE.md` — DC-1 Common Gotchas (Phase 2 SHIPPED note)
8. `TESTING.md` — Adjuster contract + equity-offset invariant subsection
9. `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` — changelog row
10. `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md` — Phase 2 progress paragraph
11. `docs/THESIS.md` — DC-1 phase row status update
12. (If Option A for T2-BS-3) `internal/infra/gateways/sec/parser.go` — Liabilities umbrella fallback rule

## Files you will NOT modify

1. `internal/services/datacleaner/recompute.go` — Phase 1 shim is the regression signal
2. `internal/services/datacleaner/recompute_test.go` — same
3. `internal/services/valuation/*` — Tier 2 territory
4. `internal/infra/gateways/sec/plugs.go` — Phase 0's `computePlugs` invariant is sealed
5. `internal/infra/database/schema.sql` — Phase 3+ closes the SQLite gap
6. `internal/infra/repositories/sqlite/financial_data_repository.go` — same

---

## Starting prompt (copy-paste to begin Phase 2)

> I'm starting Phase 2 of DC-1 (datacleaner Adjuster-interface refactor). Phase 1 shipped on master at merge `b8e9c77` (post-merge polish) with the punch-list input data at `internal/integration/testdata/recompute-shadow/<TICKER>.json` (10 tickers, fresh `tier2-baseline/2026-05-19/` bundles). Current master HEAD: `7a08506`.
>
> Phase 2's scope is to introduce the `Adjuster` interface (single Go interface with Restater / Overlay / Hybrid roles emerging from output shape) and refactor every existing adjuster in `internal/services/datacleaner/adjustments/{assets,liabilities,earnings}.go` to implement it, producing explicit `LedgerEntry` records on a new `AdjustmentLedger` field of `FinancialData`. Phase 2 does NOT introduce `CleanedFinancialData` views (Phase 3) or migrate any consumer (Phase 4).
>
> Authoritative documents to read in order:
> 1. **`docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-handoff.md`** — read this FIRST. It has scope, required reading list, gotchas, acceptance criteria, and the T2-BS-3 disposition decision gate.
> 2. The Phase 1 shadow-analysis report at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md` — the 7-cluster punch list IS Phase 2's target list.
> 3. The design spec at `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` — focus on the "Adjuster interface" + "AdjustmentLedger" sections and the Phase 2 row of the phasing table.
> 4. The T2-BS-3 tracker at `docs/reviewer/T2-BS-3-parser-totalliabilities-zero-amd-ko.md` — ARCH must choose Option A (parser fix) or Option B (carve-out via Restated view) before BACKEND dispatch.
>
> Recommended starting move: invoke `/plan-and-create` to dispatch an ARCH agent that produces a focused Phase 2 implementation plan at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md`. Then consider whether to ship as one PR (~2-3 weeks) or split into PR1 (interface + ledger skeleton, ~3-5 days) + PR2/3/4 (per-adjuster reroute, ~1 week each). The split approach keeps reviewer load manageable and lets each adjuster's snapshot diff be reviewed in isolation against the cluster predictions.
>
> Critical invariants: (a) snapshot files in `internal/integration/testdata/recompute-shadow/` are the Phase 2 regression signal — diffs MUST be explainable as cluster-predicted reroutes; (b) `TestRecomputeUmbrellas_NoMutation` stays green; (c) Tier 2 mature-large-bank DDM bit-for-bit invariant (`TestDDM_LegacyPath_BitForBit`) stays green; (d) full test suite green modulo pre-existing SCHED-1 flake.
>
> Awareness: Tier 2 worktrees (`tier2-p1`..`tier2-p4`) may still be active on `internal/services/valuation/`. Phase 2's scope (`internal/services/datacleaner/adjustments/`) does NOT overlap, but coordinate weekly via `git fetch && git merge master` in the Phase 2 worktree to keep drift small (Phase 1's followup hit master drift; recovery pattern documented in merge `b8e9c77`).
>
> Begin by reading the handoff doc.

---

## Change log

| Date | Change |
|------|--------|
| 2026-05-19 | Initial Phase 2 handoff doc filed post-Phase-1-shipping at master `7a08506`. Cross-referenced from `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md` (pending). |
