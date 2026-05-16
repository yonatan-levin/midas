# DC-1 Phase 1 — Implementer Handoff

**Phase:** Phase 1 — Component primitive + `recomputeUmbrellas` shim (shadow mode)
**Status:** READY TO START
**Estimated effort:** 3-5 days per spec
**Master HEAD when Phase 0 closed:** `282288e` (2026-05-16)
**Prerequisite:** Phase 0 SHIPPED — see [Phase 0 closeout report](datacleaner-component-primitive-and-parallel-views-phase-0-closeout.md)

---

## TL;DR

Phase 1 adds a single new function — `recomputeUmbrellas(*entities.FinancialData)` — invoked at the end of the cleaner pipeline. The function recomputes each balance-sheet umbrella (`TotalAssets`, `CurrentAssets`, `TotalLiabilities`, `CurrentLiabilities`) from its known components plus the Phase 0 plug fields, compares the result to the existing mutated umbrella value, and **logs a warning if they diverge**. The recomputed value is NOT used by anyone — the existing mutated umbrella is preserved. Pure shadow-mode observability.

**The whole point** is to surface, in production logs, every place where the current cleaner mutates `TotalAssets` without also mutating components in a way that satisfies `umbrella = sum(known components)`. Those divergences become the targeted fixes that land in Phase 2's Adjuster-interface refactor.

---

## Required reading (in order)

Stop at the first tier that gives you enough context:

### Tier 1 — Identity & direction (always read)

1. **`CLAUDE.md`** — project conventions, especially:
   - Code style: structured logging via `logctx.From(ctx)`, no globals, table-driven tests
   - "Common Gotchas" → the DC-1 entry (Phase 0 SHIPPED, lease-split-deferred corollary)
   - "Important Files" → `internal/infra/gateways/sec/plugs.go` row
2. **`AGENTS.md`** — Tier 4 row #17b for DC-1
3. **`docs/THESIS.md`** — DC-1 row in the Phases table (in-flight status)

### Tier 2 — DC-1 design + Phase 0 ground truth

4. **`docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`** — the design spec. Focus on:
   - Section "Solution shape — Three views, one ledger, lazy reconstruction"
   - Section "Pipeline flow" — Phase 1's `recomputeUmbrellas` slots in HERE
   - Section "Phasing & implementation sequence" → Phase 1 row + the shadow-mode warning gate (Phase 1 → Phase 2)
   - Section "Testing strategy" → T1 property test (Phase 0 implemented `computePlugs`-shaped; Phase 1 ALSO benefits from the same gopter pattern around `recomputeUmbrellas`)
5. **`docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-0-closeout.md`** — what landed, what's deferred, **lessons learned**. Key items:
   - MXL 2017FY and EQIX 2013Q1 trigger the clamp path — expect these as known cases, not bugs
   - The SQLite gap-pin test pattern is reusable for any deferred production change
6. **`docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md`** — the live tracker
7. **`docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-0-implementation-plan.md`** — Phase 0 plan (for tone/structure reference when ARCH writes Phase 1's plan)

### Tier 3 — Code locations to inspect before designing

8. **`internal/services/datacleaner/service.go`** — top-level cleaner orchestration. `recomputeUmbrellas` will be called from here at the end of the pipeline. Find `(*Service).Clean` (or equivalent) to see where adjusters run and where Phase 1's shim slots in.
9. **`internal/services/datacleaner/adjustments/assets.go`** — the 4 `data.TotalAssets -=` mutations (lines `:69, :157, :232, :308`) are the primary divergence sources Phase 1 will observe. **DO NOT modify them in Phase 1** — that's Phase 2's job. Just instrument.
10. **`internal/services/datacleaner/adjustments/liabilities.go`** — the orchestrator at `:87-88` does `data.TotalDebt += result.Amount` for ALL B-rules. Same rule — don't touch in Phase 1, just instrument.
11. **`internal/core/entities/financial_data.go`** — confirm the 4 plug fields land at lines ~105-136 (per Phase 0). Read the doc-comment block — it explains the lease-decomposition state and PP&E absorption.
12. **`internal/infra/gateways/sec/plugs.go`** — the Phase 0 `computePlugs` helper. Phase 1 will reuse the same mathematical decomposition for the recompute side.

---

## Phase 1 scope

### What to BUILD

1. **A new function** `recomputeUmbrellas(fd *entities.FinancialData, logger *zap.Logger)`. Pure (no I/O, no allocs in the hot path). Lives in `internal/services/datacleaner/recompute.go` (NEW file).

   ```go
   // Sketch — finalize during ARCH cycle:
   func recomputeUmbrellas(fd *entities.FinancialData, logger *zap.Logger) {
       recomputedCA := fd.CashAndCashEquivalents + fd.Inventory + fd.OtherCurrentAssets
       if math.Abs(recomputedCA - fd.CurrentAssets) > tolerance {
           logger.Warn("recomputeUmbrellas: CurrentAssets divergence",
               zap.Float64("reported", fd.CurrentAssets),
               zap.Float64("recomputed", recomputedCA),
               zap.Float64("delta", recomputedCA - fd.CurrentAssets),
               ...
           )
       }
       // Same shape for TotalAssets, CurrentLiabilities, TotalLiabilities.
       // DO NOT mutate fd.*.
   }
   ```

2. **A call site** at the END of the cleaner pipeline in `internal/services/datacleaner/service.go` (or `pipeline.go`), AFTER all adjusters have run. The exact insertion point: right before `Clean()` returns its result.

3. **A property test** (gopter, mirror Phase 0's pattern at `internal/infra/gateways/sec/plugs_test.go`) asserting that, on any well-formed input, `recomputeUmbrellas` does NOT emit a warning. This pins the "happy path: no divergence" baseline.

4. **An integration test** that runs the FULL cleaner pipeline against the 10-ticker basket fixtures and records the divergences as a structured output (e.g., a JSON snapshot per ticker × period). Phase 2's gate criterion is "divergences analyzed" — Phase 1's deliverable is the data those analyses run on.

5. **Documentation update** to CLAUDE.md / TESTING.md describing the shadow-mode pattern + how to read the divergence warnings.

### What to NOT build

- Do NOT remove the existing `data.TotalAssets -= X` mutations from `assets.go` / `liabilities.go`. Those are Phase 2's job.
- Do NOT introduce the `Adjuster` interface. Phase 2.
- Do NOT introduce `CleanedFinancialData` or view reconstruction. Phase 3.
- Do NOT migrate any consumer of `data.TotalAssets`. Phase 4.
- Do NOT close the SQLite persistence gap. Defer per the Phase 0 flip-gate test until Phase 2+ explicitly needs to read plug fields from cached rows.

### Critical invariants (Phase 1 must preserve)

1. **Zero downstream behavior change.** `data.*` mutated values are still the canonical output. `recomputeUmbrellas` ONLY reads, never writes. Empirical proof: replay against AAPL + MSFT bundles must show timestamp-only drift in `17-response.json`, identical to Phase 0.
2. **Full test suite stays green.** Pre-existing SCHED-1 flake (`docs/reviewer/scheduler-test-cleanup-race.md`) may or may not fire — that's not a Phase 1 regression.
3. **Phase 0 invariants hold.** The 4 plug fields are still populated; the SQLite gap-pin test still PASSES asserting current gap behavior.

---

## Known gotchas

1. **Concurrent Tier 2 worktrees may still be active.** As of 2026-05-16, tier2-p1 through tier2-p4 are touching `internal/services/valuation/` (different files than DC-1 Phase 1). Zero file-scope overlap, but check `git worktree list` before dispatching and tell BACKEND subagents to stay out of valuation/.

2. **Clamp-fired periods will produce expected divergences.** MXL 2017FY and EQIX 2013Q1 trigger the Phase 0 clamp path (`plug == 0` because `sum(components) > umbrella`). Under Phase 1's `recomputeUmbrellas`, these periods WILL emit divergence warnings — that's correct behavior, not a bug. The integration test should record but not fail on them. Phase 2 will resolve via the `Adjuster` interface refactor.

3. **The SCHED-1 scheduler flake is intermittent.** Pre-existing race in `scheduler.go:50-56` — not introduced by DC-1. Don't chase it; it's tracked separately. If it fires during your test runs, document but don't block.

4. **`OperatingLeaseLiability` umbrella vs split fields.** Today's SEC parser populates ONLY the umbrella; `*.Current` / `*.Noncurrent` split fields are always zero. `OtherCurrentLiabilities` absorbs the entire lease portion. Phase 1's recompute should treat this consistently with Phase 0's plug computation — no surprises here, but worth knowing.

5. **Subagent usage limits.** During Phase 0, two BACKEND subagents hit limits mid-task. The recovery pattern: controller inspects uncommitted work, completes inline if mechanical, then resumes the V-R-Q dispatch chain. Don't dispatch fresh subagents for sub-3-edit follow-ups.

---

## Suggested implementation approach

1. **Start with an ARCH cycle** (`/plan-and-create` skill). The spec describes Phase 1 at a high level; the ARCH should produce a focused implementation plan at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-implementation-plan.md` with:
   - File-by-file deltas
   - Property + integration test specifications
   - Acceptance criteria for the shadow-mode warning gate (Phase 1 → Phase 2)
   - A regression-test strategy that pins Phase 0's ticker-basket invariants

2. **Single worktree, sequential execution.** Phase 1's scope is small (~3-5 days, one new file, instrumentation only). Parallel fan-out adds setup cost without saving wall-clock time. Per the Phase 0 closeout lessons learned (#3), single-worktree-sequential is more efficient for this scope.

3. **Full B-V-R-Q chain per the project's ABVRQ pattern.** BACKEND in the worktree → VERIFIER independent re-run → REVIEWER code quality → QA spec conformance → HUMAN merge approval. Don't skip stages.

4. **Empirical replay validation.** Before merge, run `cmd/replay --from=parsed --diff-stages` against AAPL + MSFT bundles. `17-response.json` must show timestamp-only drift (matches Phase 0's empirical baseline).

5. **After merge, an explicit "shadow analysis" follow-up.** Phase 1's gate criterion is "shadow warnings analyzed across basket" — that analysis is itself a deliverable. File a `docs/refactoring/implementations/...-phase-1-shadow-analysis.md` enumerating which tickers + periods + adjusters produced divergences. That report is Phase 2's input.

---

## Acceptance criteria for closing Phase 1

- [ ] `recomputeUmbrellas` function lands in `internal/services/datacleaner/recompute.go` with godoc explaining shadow-mode purpose
- [ ] Function is called at the end of the cleaner pipeline (single insertion point)
- [ ] Property test (gopter) pins "well-formed input → no divergence" baseline
- [ ] Integration test records divergences across the 10-ticker basket as structured output
- [ ] Zero downstream behavior change — replay-verified on AAPL + MSFT
- [ ] Full test suite green (modulo SCHED-1 flake)
- [ ] CLAUDE.md Common Gotchas entry updated to note Phase 1 SHIPPED + shadow-mode pattern
- [ ] TESTING.md plug-invariant subsection extended with the recompute-side companion pattern
- [ ] DC-1 reviewer tracker status updated: "Phase 1 SHIPPED YYYY-MM-DD; Phases 2-4 pending"
- [ ] Shadow analysis report filed in `implementations/` and cross-referenced from the DC-1 tracker
- [ ] Phase 0 closeout-style report filed for Phase 1 (use this handoff's structure as the input)

---

## Files you will likely create

1. `internal/services/datacleaner/recompute.go` (NEW)
2. `internal/services/datacleaner/recompute_test.go` (NEW — property test)
3. `internal/integration/datacleaner_recompute_shadow_test.go` (NEW — basket integration test)
4. `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-implementation-plan.md` (NEW — ARCH output)
5. `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md` (NEW — after-merge analysis)
6. `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-closeout.md` (NEW — at close)

## Files you will likely modify

1. `internal/services/datacleaner/service.go` (or `pipeline.go`) — add the `recomputeUmbrellas` call site
2. `CLAUDE.md` — Common Gotchas DC-1 entry (Phase 1 SHIPPED + pattern note)
3. `TESTING.md` — plug-invariant subsection (recompute companion pattern)
4. `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` — changelog row
5. `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md` — Phase 1 progress paragraph
6. `docs/THESIS.md` — DC-1 phase row status update

## Files you will NOT modify

1. `internal/services/datacleaner/adjustments/*.go` — Phase 2's job
2. `internal/services/valuation/*` — Tier 2 territory; touch only if a DC-1 reason absolutely requires it (consult the user first)
3. `internal/infra/database/schema.sql` — Phase 2+ closes the SQLite gap
4. `internal/infra/repositories/sqlite/financial_data_repository.go` — same

---

## Starting prompt (copy-paste to begin Phase 1)

> I'm starting Phase 1 of DC-1 (datacleaner component primitive refactor). Phase 0 shipped on master at commit `282288e` (2026-05-16). Phase 1's scope is to add a `recomputeUmbrellas` shadow-mode shim that surfaces divergences between the cleaner's mutated umbrellas and the components-plus-plug sum, without changing any downstream behavior.
>
> Authoritative documents to read in order:
> 1. **`docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-handoff.md`** — read this FIRST, it has everything you need including the scope, required reading list, gotchas, and acceptance criteria.
> 2. The design spec at `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` — focus on the "Phasing & implementation sequence" Phase 1 row.
> 3. The Phase 0 closeout report at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-0-closeout.md` — read the "Lessons learned" and "What's deferred to Phase 1+" sections.
>
> Recommended starting move: invoke `/plan-and-create` to dispatch an ARCH agent that produces a focused Phase 1 implementation plan at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-implementation-plan.md`. Then proceed BACKEND → VERIFIER → REVIEWER → QA → HUMAN per the project's ABVRQ pattern. Single worktree, sequential — Phase 1 is small enough that parallel fan-out adds overhead without saving wall-clock time.
>
> Critical invariant: **zero downstream behavior change**. The recompute function ONLY reads and logs; it does NOT mutate `data.*`. Empirically verify via `cmd/replay --from=parsed --diff-stages` against AAPL + MSFT bundles before merge — `17-response.json` must show timestamp-only drift, matching Phase 0's empirical baseline.
>
> Awareness: Tier 2 worktrees may still be active touching `internal/services/valuation/`. Phase 1's scope (`internal/services/datacleaner/`) does not overlap. Check `git worktree list` before dispatching and instruct BACKEND subagents to stay out of valuation/. Pre-existing SCHED-1 scheduler-test-cleanup race may fire intermittently — track but do not chase.
>
> Begin by reading the handoff doc.

---

## Change log

| Date | Change |
|------|--------|
| 2026-05-16 | Initial handoff doc filed at Phase 0 close. Cross-referenced from `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md`. |
