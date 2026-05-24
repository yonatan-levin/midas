# DC-1 Phase 0 — Closeout Report

**Phase:** Phase 0 — Plug + entity extension (foundation layer)
**Status:** ✅ SHIPPED 2026-05-16
**Master HEAD at close:** `112c505` (merge `merge: worktree-agent-a86e0e8ceeca31cc9 — DC-1 Phase 0 closeout (4 commits)`)
**Discovery path:**
- Tracker: [docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md](../../reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md)
- Spec: [datacleaner-component-primitive-and-parallel-views-spec.md](../spec/datacleaner-component-primitive-and-parallel-views-spec.md)
- Plan: [datacleaner-component-primitive-and-parallel-views-phase-0-implementation-plan.md](datacleaner-component-primitive-and-parallel-views-phase-0-implementation-plan.md)

---

## What landed

Three worktrees, full B-V-R-Q validation per worktree, 11 commits on master across 3 merge commits.

### Worktree A — foundation (Tasks 0.1 + 0.2 + 0.3)

Merge `1640394`. 5 commits on master.

| Commit | Scope |
|---|---|
| `bf9a671` | 4 plug fields on `FinancialData`: `OtherCurrentAssets`, `OtherNonCurrentAssets`, `OtherCurrentLiabilities`, `OtherNonCurrentLiabilities` |
| `46a5aac` | `computePlugs` + `clampPlug` in `internal/infra/gateways/sec/plugs.go` |
| `16339eb` | Wired `computePlugs` into end of `parsePeriodData` |
| `4b11876` | Committed spec + Phase 0 implementation plan |
| `48f8fe5` | Comment clarifications addressing REVIEWER A-1/A-2 (lease-decomposition note + test fixture rationale) |

### Worktree B — tests + persistence (Tasks 0.4 + 0.5 + 0.6)

Merge `4612f77`. 3 commits on master.

| Commit | Scope |
|---|---|
| `2dfe8b2` | `gopter` property test (4 invariants × 200 iterations, pinned seed `20260516`, well-formed branch by construction) |
| `bac089c` | Ticker-basket integration test (`internal/integration/datacleaner_plug_invariants_test.go`) with two-branch `assertPlugTriple` helper |
| `d0fd5f1` | Memory-cache round-trip (PASS) + SQLite flip-gate (REVIEWER called the pattern "exemplary") |

### Worktree C — closeout (Tasks 0.7 + 0.8 + reviewer carryover + tracker)

Merge `112c505`. 4 commits on master.

| Commit | Scope |
|---|---|
| `b547867` | ARCHITECTURE.md plug-fields block + spec changelog row |
| `354c17a` | L1 (dynamic baseline date resolution) + L2 (passedCount ≥5 floor) + N6 (SQLite docstring line refs corrected) |
| `fd3a678` | SCHED-1 scheduler-test-cleanup-race tracker filed |
| `a4a74d1` | TESTING.md plug-invariant subsection + DC-1 tracker status bumped to IN PROGRESS |

---

## Empirical verification

| Surface | Result |
|---|---|
| Build clean | `go build ./...` silent across all 40+ packages |
| Full suite | `go test -count=1 ./...` green; scheduler test flake (SCHED-1) intermittent, pre-existing |
| Ticker basket | 7 of 10 PASS (AAPL, MSFT, KO, F, AMD, MXL, EQIX), 3 SKIP (JNJ, TSM, BABA — no captured bundles); `passedCount=7` above the `≥5` floor |
| Property test | 4 properties × 200 iterations PASS; pinned seed `20260516` |
| Replay regression | AAPL + MSFT bundles: only `as_of` / `calculated_at` / `market_data_date` timestamps drift; **zero financial drift** in `17-response.json` |
| Coverage | sec 90.1% (flat), integration 96.0% (new), sqlite +1.2%, cache flat — no regressions |

---

## What's deferred to Phase 1+

### Schema migration (load-bearing for Phase 1)

The SQLite repository (`internal/infra/repositories/sqlite/financial_data_repository.go`) uses explicit per-column INSERT/SELECT. Plug fields are dropped on write and read back as zero. Pinned by `TestFinancialDataRepository_PlugFields_PersistenceGap` with a flip-gate failure-message ("flip to wantOtherCA after Phase 1+ migration") — when Phase 1 adds the migration, the inverted assertions will fail and force the test to flip to a desired-behavior gate.

**Required changes (per the SQLite test's docstring):**
1. `internal/infra/database/schema.sql:25-107` — add 4 columns to `financial_data` table
2. `financial_data_repository.go:56-87` (`storeWith` INSERT) — enumerate the 4 plug columns
3. `financial_data_repository.go:141` (`GetLatest`), `:202` (`GetHistorical`), `:278` (`GetByPeriod`) — add 4 SELECT columns

### Lease-split decomposition (not blocking; quality)

Today's SEC parser populates only the umbrella `OperatingLeaseLiability`; the `*.Current` / `*.Noncurrent` split fields exist on the entity but are NEVER populated (they're `findValue` fallbacks for the umbrella). As a consequence, `OtherCurrentLiabilities` absorbs the entire current-liabilities umbrella and `OtherNonCurrentLiabilities` absorbs everything except `TotalDebt`. The math invariants hold by construction; only the lease-split granularity is deferred.

Phase 1's `recomputeUmbrellas` shadow shim will surface tickers where the lease-split would materially shift the plug values, informing whether/when Phase 1+ should teach the parser to split.

### Worktree B NITs not addressed

REVIEWER on Worktree B flagged 8 items; Worktree C closed 3 (L1 / L2 / N6). The remaining 5 are pure polish:

- **N1** — test naming: `TestDatacleaner_PlugInvariants_TickerBasket` more strictly is `TestSECParser_*` since it exercises the parser
- **N2** — test naming: `TestMemoryCacheRepository_FinancialDataPlugFields_RoundTrip` → `TestMemoryCacheRepository_SetGet_PreservesFinancialDataPlugFields`
- **N3** — `approxEqual` helper could use `assert.InDelta` directly
- **N4** — redundant `>= 0` check inside property test (belt-and-suspenders given clamp + generator construction)
- **N5** — `_ = wantOtherCA` linter-silencer block in SQLite test may be unnecessary
- **N7** — minor comment redundancy
- **N8** — no-op `var _ = (*entities.FinancialData)(nil)` compile-time guard

Can roll into Phase 1's first cleanup commit or leave as standing polish.

### Open auxiliary tracker

[`docs/reviewer/scheduler-test-cleanup-race.md`](../../reviewer/scheduler-test-cleanup-race.md) — pre-existing race in `internal/services/scheduler/scheduler.go:50-56` where the `Start()` goroutine outlives `*testing.T` scope and hits its deferred logger after teardown. Discovered during Worktree B QA. **NOT caused by DC-1.** Three fix options documented; out of scope for DC-1 and Tier 2.

---

## Lessons for Phase 1 ARCH cycle

1. **Plug residuals exhibit clamp-fired behavior on legacy data** — MXL 2017FY and EQIX 2013Q1 trigger the clamp path because of legitimate cross-period accounting quirks (goodwill+intangibles+DTA briefly exceeding non-current asset umbrella; TotalDebt aggregating current+noncurrent over-subtracting). Phase 1's `recomputeUmbrellas` shadow shim should expect these as known cases, not flag them as bugs.

2. **The SQLite gap-pin test pattern is reusable** — REVIEWER called it "exemplary" because the failure-mode message names the post-flip target constant. When deferring any future production change, copy this pattern: the test asserts current broken behavior with explicit "flip to X" instructions, and the future implementer is forced to update the test when they apply the migration.

3. **Parallel worktree fan-out works for small phases but adds setup overhead** — for Phase 0's ~3.5h scope, three parallel worktrees produced excellent validation rigor but cost ~30% in dispatch overhead. Phase 1's shadow-mode work is small enough that single-worktree-sequential may be more efficient. Reassess at Phase 1 ARCH time.

4. **Two BACKEND subagents hit usage limits mid-task during this phase** (Worktree A fix-loop, Worktree C BACKEND). The recovery pattern (controller inspects uncommitted work, completes inline via Edit/Bash, then resumes the V-R-Q dispatch chain) is the right move when the remaining work is mechanical. Don't dispatch fresh subagents for sub-3-edit follow-ups.

5. **Concurrent agent activity is real and needs explicit context** — Tier 2 P0a, P0b, and P1-P4 worktrees all ran in parallel with DC-1 Phase 0 on the same repo. Zero file-scope overlap meant zero conflicts, but Worktree C's BACKEND prompt explicitly listed which Tier 2 files NOT to touch. Future parallel-track work should include this kind of guard in dispatch briefs.

---

## Acceptance criteria — Phase 0 contributions to DC-1 close

Per the spec's "Acceptance criteria for closing DC-1":

- [x] 4 plug fields on `FinancialData`, populated by SEC parser
- [x] `computePlugs` helper at end of `parsePeriodData`
- [x] Property test pinning components-sum-to-umbrellas invariant
- [x] Integration test across ticker basket (current 7-of-10 PASS, ≥5 floor asserted)
- [x] Persistence round-trip (memory PASS, SQLite gap pinned)
- [x] Documentation updates: ARCHITECTURE.md, TESTING.md, spec changelog, CLAUDE.md+AGENTS.md+THESIS.md (in `3a29504`)
- [x] DC-1 reviewer tracker status bumped to IN PROGRESS with Phase 0 progress paragraph
- [x] Zero downstream behavior change — empirically replay-verified

**Phase 1 (`recomputeUmbrellas` shadow shim) is unblocked.**
