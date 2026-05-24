# Tier 2 Kickoff — Unified `AssumptionProfile` Architectural Sprint

**Status:** READY TO START (Tier 1 verified complete; see `tier-1-close-verification` evidence in master commit `0324057` + verification report in chat log 2026-05-11)
**Estimated effort:** ~1 week (parallelizable across the 4 model surfaces once the profile machinery lands)
**Scope:** RM-3 + VAL-1 + VAL-2 + VAL-3 Phase 3
**Architectural backbone:** shared `AssumptionProfile` keyed by `(archetype × maturity)` driving horizon, growth caps, base normalization, terminal handling, and discount-rate selection

---

## TL;DR

The four open trackers (RM-3, VAL-1, VAL-2, VAL-3 P3) all explicitly call out a shared design — an `AssumptionProfile` table keyed by `(archetype, maturity)` 2-tuple. They cannot ship piecemeal cleanly because they share this backbone. Tier 2 = one coordinated sprint that builds the profile table first, then applies it to all four valuation models simultaneously with a cross-model regression suite.

---

## Where we are (state of master at session start)

- **HEAD:** `0324057` `docs(reviewer): archive 5 Graham-umbrella Tier-1 trackers — all closed`
- **Graham umbrella status:** spec archived, Tier 1 polish closed (VAL-4, VAL-5, VAL-7, RM-1.A, RM-1.B archived to `docs/reviewer/archive/`)
- **VAL-6 (HEALTHCARE_REIT keyword collision):** open, blocked on unification refactor — leave alone in Tier 2
- **Sibling RPL trackers (replay tooling):** out of scope for this sprint
- **Test suite:** 47/47 packages green on master as of verification gate on 2026-05-11

---

## The architectural backbone — `AssumptionProfile`

Each of the four model files needs to consume a profile resolved from `(archetype, maturity)`. The profile drives:

| Profile field | Type | Purpose | Consumed by |
|---|---|---|---|
| `Archetype` | enum | `mature_large_scale`, `software_like_scaling`, `cyclical_mid_cycle`, `cyclical_trough`, `hypergrowth_early`, `pre_revenue_biotech`, … | all 4 models |
| `Maturity` | enum | `mature`, `standard_growth`, `high_growth` (3 buckets per Damodaran) | DCF horizon, DDM horizon |
| `HorizonYears` | int | 3 (mature) / 5 (standard) / 7-10 (high-growth) | DCF, DDM, FFO, RevMult |
| `CompoundGrowthCap` | float | per-archetype cap (e.g., 1.5× for mature large-cap, 4× for software-like scaling, 8× for pre-revenue biotech) | RM-3, VAL-1, VAL-3 P3 |
| `RevenueBaseMethod` | enum | `raw_ttm`, `two_year_average`, `max_ttm_or_floor`, `mid_cycle_normalized` | RM-3, VAL-1 (cyclicals) |
| `TerminalMethod` | enum | `gordon_growth`, `exit_multiple` | DCF, DDM |
| `Stabilized` | bool | Is year-N a steady state, or does fade need to extend? | DCF, RM-3 |
| `FadeYears` | int | 0, 1, or 2 additional years of growth deceleration before terminal | DCF, RM-3 |
| `TerminalMultiple` | float | year-N multiple (lower than current peer multiple to avoid double-counting growth) | RM-3, DDM |
| `DiscountMethod` | enum | `wacc` (DCF), `cost_of_equity` (DDM, RM-3, FFO forward) | all 4 models |
| `DPSGrowthCap` | float | per-archetype DPS-CAGR cap (8% mature bank, 15% growth bank, 25% maturing-tech-first-dividend) | VAL-2 |
| `PayoutPath` | []float | per-year payout ratio progression (for DDM multi-stage explicit forecast) | VAL-2 |
| `DividendForecastHorizon` | int | 0 (single-stage Gordon) or 5-10y (multi-stage) | VAL-2 |
| `StableDividendGrowth` | float | terminal g for DDM stable phase | VAL-2 |
| `ProfileName` | string | audit-trail identifier (e.g., `"cyclical_mid_cycle"`) | response field |

Profile resolution happens **upstream** in the router (or a dedicated `AssumptionProfileResolver` service). The model receives the resolved profile and produces output + audit fields.

Each model emits the chosen profile + horizon + cap_applied + route_reason on the response so consumers and replay tooling have full visibility.

---

## In-scope trackers

| Tracker | Scope | Key new behavior |
|---|---|---|
| **RM-3** (forward revenue-multiple model) | 1-3y forward anchor; cyclical-base normalization; cost-of-equity discount (NOT WACC); profile-keyed compound-growth cap; trailing+forward parallel emission | Adds `forward_value` field alongside trailing; profile-driven preference for primary `intrinsic_value_per_share` |
| **VAL-1** (DCF archetype-aware) | Horizon resolved from profile (3y mature / 5y standard / 7-10y hyper-growth); cyclical-base normalization; exit-multiple terminal optional; forward-diluted shares for high-SBC tickers | Adds `dcf_horizon_years`, `dcf_terminal_method`, `dcf_terminal_pct_of_ev`, `dcf_per_year_pv`, `dcf_terminal_growth_used` diagnostic fields |
| **VAL-2** (DDM multi-stage) | Single-stage Gordon → multi-stage for non-mature dividend payers (AAPL/MSFT-style, growth banks); profile-keyed DPS-CAGR caps; PayoutPath-driven explicit dividend forecast | `horizon=0` preserves today's behavior for mature large banks (no regression); `horizon=5-10y` for maturing payers |
| **VAL-3 Phase 3** (forward FFO/AFFO) | Profile-driven REIT horizon (NTM-style anchor for stable; 5y for growth subsectors like data center); cost-of-equity discount; revenue growth as proxy for FFO growth (or FFO-specific growth signal in later iteration) | Builds on Phase 1 (subsector multiples, shipped 2026-05-09) and Phase 2 (AFFO, separate; can ship before or after P3) |

VAL-3 Phase 2 (AFFO support) is independent of `AssumptionProfile`; can ship before or alongside without coupling.

---

## Read-first (in this order before writing any code)

1. `CLAUDE.md` (project conventions — coverage ≥90% for finance modules, structured logging via `logctx.From(ctx)`, no globals, TDD mandatory, table-driven tests)
2. `AGENTS.md` (project context contract)
3. `docs/reviewer/RM-3-forward-revenue-multiple-model.md` (canonical for the profile design — has the most detailed "Refined design (post-research)" section with 5 corrections including the maturity-driven horizon, cyclical-base normalization, year-by-year vs single-shot discount discussion)
4. `docs/reviewer/VAL-1-dcf-model-archetype-aware-horizon-and-normalization.md`
5. `docs/reviewer/VAL-2-ddm-multistage-and-cost-of-equity-discipline.md`
6. `docs/reviewer/VAL-3-ffo-affo-subsector-multiples-and-forward-projection.md` (Phase 3 sections — the forward FFO/AFFO design)
7. `docs/refactoring/valuation-engine-upgrade-spec.md` (broader engine roadmap — situates the AssumptionProfile work)
8. Existing implementation: `internal/services/valuation/service.go` (DCF body), `internal/services/valuation/models/ddm.go`, `internal/services/valuation/models/ffo.go`, `internal/services/valuation/models/revenue_multiple.go`, `internal/services/valuation/models/router.go`
9. Memory: `project_upgrade_status.md` (where this work sits in the project's overall arc)

---

## Recommended approach

### Phase A — Brainstorming (~1-2 hours, single session, no code)

Invoke `superpowers:brainstorming` to explore design questions that the existing trackers leave open:

- Where does `AssumptionProfile` live as a Go type? (`internal/services/valuation/profile.go` as a new package-private file, or `internal/core/entities/assumption_profile.go` for domain placement?)
- How is profile resolution wired into the router? (extend `ModelInput` with a `Profile *AssumptionProfile` field, or accept the profile as a separate `ModelRouter.Route(...)` parameter?)
- Is profile resolution deterministic from (sic, industry, classifier outputs, balance-sheet signals), or does it require its own service with config?
- Should the profile table be code (Go map literal) or config (JSON in `config/`)? Trade-off: code = compile-time safety; config = runtime updatability for analysts to tune.
- For VAL-2 specifically: how does the single-stage-Gordon → multi-stage upgrade preserve `horizon=0` bit-for-bit for mature large banks (JPM, BAC)?
- Coverage strategy: do we want a single golden-file regression suite that exercises all 4 models against a basket of 10 representative tickers (AAPL, MSFT, JPM, KO, F, MXL, NVDA, AMD, EQIX, PLD)?
- What's the rollout sequence? Profile table → wire into 1 model → validate → wire into next → … or build all 4 simultaneously behind a feature flag?

Land the answers in a brainstorming notes file under `docs/refactoring/` before writing the spec.

### Phase B — ARCH cycle / `/plan-and-create` (~1 day)

Produce `docs/refactoring/assumption-profile-spec.md` with:

- Full type definitions (`AssumptionProfile`, `Archetype`, `Maturity` enums)
- Profile table (per-archetype values with citations to spec sources)
- Profile resolution algorithm (deterministic; pure function of inputs)
- Per-model integration sketch (4 sub-sections)
- Rollout sequencing (single-stage migration vs feature-flag)
- Acceptance criteria + V/R/Q gate strategy
- Out-of-scope (DC-1 datacleaner refactor; G-1 growth blend; replay tooling)
- Open questions for HUMAN (similar to the Q1-Q3 block in the Graham-floor spec)

### Phase C — Parallel implementation streams (~3-5 days)

Once the spec lands:

**Stream P0 — `AssumptionProfile` machinery**
- Type definitions + profile table + resolver
- No model wiring yet
- Comprehensive unit tests on resolver: per-archetype expected fields, edge cases (unknown SIC, missing balance-sheet signals)
- Coverage ≥90%

**Streams P1-P4 — model integration (parallel-safe IF P0 has shipped)**
- P1: RM-3 wire profile into `revenue_multiple.go` + add `forward_value` field
- P2: VAL-1 wire profile into DCF in `service.go::performValuation` + 5 diagnostic fields
- P3: VAL-2 wire profile into `ddm.go`; preserve `horizon=0` for mature large banks
- P4: VAL-3 P3 wire profile into `ffo.go` for forward FFO projection

Each stream gets its own worktree, BACKEND agent invokes `/execute` against the spec, RED→GREEN proofs captured, single atomic commit per stream.

**Cross-model regression suite — ships in the same window as the 4 streams**
- Golden-file fixtures for 10 representative tickers; each ticker run through all 4 models and the chosen profile
- Pin per-ticker (chosen_profile, horizon, primary_value, trailing_value, warning_count)
- Run on every PR to prevent silent profile-resolution regressions

### Phase D — V/R/Q gate per stream + integration gate

Each P1-P4 stream gets VERIFIER + REVIEWER + QA on its worktree. After all 4 merge to master:
- Full integration test on combined state (`go test ./... -count=1`)
- Live API regression on the 10-ticker basket (`cmd/server` + curl loop)
- Verification skill before claiming "Tier 2 closed"

### Phase E — Archival + tracker closure

- Move RM-3 + VAL-1 + VAL-2 + VAL-3 (parent if Phase 3 completes the spec; otherwise keep VAL-3 open for any remaining phases) to `docs/reviewer/archive/`
- File any small follow-up trackers surfaced during V/R/Q (similar to how Tier 1 produced VAL-4/5/6/7 + RM-1.A/B)
- Update `project_upgrade_status.md` memory entry with the Tier 2 close + key commits

---

## Quality gates (Tier 2 specific)

- **Coverage:** ≥90% on `internal/services/valuation/profile.go` (or wherever AssumptionProfile lives). ≥90% on the modified per-model files. Package-level coverage ≥92% (current baselines: `valuation/models` 93.6%, `services/valuation` 89.7%).
- **Hermeticity:** no `time.Now()` calls outside the consumer layer (Clock dependency pattern established by RM-1.A).
- **Replay determinism:** profile resolution must be a pure function of inputs (sic, industry, classifier outputs, balance-sheet signals + the captured clock). No wall-clock dependencies in resolution.
- **Backward compat for VAL-2:** mature large banks (JPM/BAC) with `horizon=0` produce **bit-for-bit identical** DDM outputs vs pre-Tier-2. Pin via golden-file regression test.
- **Cross-cutting invariants from prior phases:**
  - `pkg/finance/*` unchanged (v1.1 D7)
  - `go.mod`/`go.sum` net additions documented + justified (NF1 from replay tooling spec)
  - Zero non-comment `time.Now()` in `internal/services/valuation/service.go` (D10 from replay tooling)
  - F11 hermeticity: replay can re-run any Tier-2-affected bundle and get identical numerical output

---

## Out of scope (explicitly)

- **VAL-6** (HEALTHCARE_REIT keyword precedence collision) — blocked on the unification refactor; not part of Tier 2
- **DC-1** (datacleaner component primitive + parallel views) — Tier 4 foundational refactor; do not bundle in
- **G-1** (growth blend weights coarse) — small follow-up, can ship anytime; not blocking Tier 2
- **RM-2 Phase 2** (Damodaran adoption) — Tier 3 independent improvement; can run in parallel with Tier 2 in a separate sprint but is NOT part of the AssumptionProfile backbone
- **VAL-3 Phase 2** (AFFO support) — independent of AssumptionProfile; can ship before or after Tier 2
- **Replay tooling follow-ups** (RPL-1/2/3/4) — separate Phase 2.D umbrella
- **CalculationVersion bump** — defer until Tier 2 ships; then bump 4.1 → 4.2 in a single atomic commit, cache-busts on rollout

---

## Ready-to-paste session-start prompt

Copy-paste this into a fresh Claude Code session when ready to start Tier 2:

```
Start Tier 2 of the Graham-style valuation umbrella — the unified
AssumptionProfile architectural sprint covering RM-3 + VAL-1 + VAL-2 +
VAL-3 Phase 3.

Before any code, run superpowers:brainstorming to settle 6-8 open design
questions on profile placement, resolution wiring, table-as-code-vs-config,
VAL-2 backward-compat, golden-file regression strategy, and rollout
sequencing. The full kickoff brief is at
docs/refactoring/tier-2-assumption-profile-kickoff.md — read it first.

After brainstorming, run /plan-and-create to produce
docs/refactoring/assumption-profile-spec.md. Then dispatch parallel
worktree-isolated BACKEND agents for the 4 model-integration streams
(P1-P4) once the P0 profile-machinery stream has shipped. V/R/Q each
stream before merge; final integration gate runs the full 47-package
test suite + a 10-ticker live API regression on master.

Master is at 0324057 (Tier 1 archived + verified clean as of 2026-05-11).
Tier 1 closed VAL-4, VAL-5, VAL-7, RM-1.A, RM-1.B — all in
docs/reviewer/archive/. VAL-6 stays open (blocked on unification refactor).

Quality gates: coverage ≥90% on touched modules, no time.Now() outside
consumer layer, profile resolution deterministic, VAL-2 mature-large-bank
output bit-for-bit identical to pre-Tier-2.

Out of scope: VAL-6, DC-1, G-1, RM-2 P2, VAL-3 P2 (AFFO), RPL tooling.
Bump CalculationVersion 4.1 → 4.2 only at the end of the sprint.
```

---

## Estimated commits at Tier 2 close

Following the Tier 1 commit-shape pattern (~8 commits for 4 streams):

- 1 commit: `feat(valuation): add AssumptionProfile machinery (Tier 2 P0)` — profile types + table + resolver + tests
- 4 commits: one per stream P1-P4 (`feat(valuation): RM-3 forward revenue model...`, etc.)
- 4 merge commits: `merge: worktree-agent-XXX — <tracker> (1 commit)`
- 1 commit: `docs(reviewer): archive 4 Tier-2 trackers — RM-3 + VAL-1 + VAL-2 + VAL-3 P3 closed`
- 1 commit: `feat(valuation): bump CalculationVersion 4.1 → 4.2 (Tier 2 close)` — final commit, cache-busts on rollout

Total: ~11 atomic commits over ~1 week of focused engineering.

After Tier 2 closes, the Graham umbrella will have ONE open tracker (VAL-6, blocked), and the next natural sprint is either Tier 3 (RM-2 P2 Damodaran + VAL-3 P2 AFFO, in parallel) or Tier 4 (DC-1 datacleaner refactor as a longer-running standalone effort).
