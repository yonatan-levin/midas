# Handoff — D-0: classification re-verify at 4.8 + scope the SIC-unification refactor

**Date filed:** 2026-06-20 · **Status:** READY · **Owner:** next session
**Engine at handoff:** `CalculationVersion 4.8` · **master tip:** `91e0b05`
**Bucket:** D (classification cleanup — niche, non-blocking)

---

## 1. Why this is a *re-verify first*, not a build

midas has **two industry classifiers that can disagree**:
- `Classify(sic, naics, name)` — SIC-driven, the canonical label for the model router.
- `ClassifyIndustry(ticker, data)` — a balance-sheet **heuristic** that drives
  datacleaner rules.

`FairValueResponse.industry.match` surfaces whether they agree; `cmd/accuracy` flags
`CLASSIFIER_MISMATCH` when `match == false` (in the 4.8 report: **EQIX, PLD, JPM**).

**SR-1 (4.8) changed this landscape and the change is unmeasured:**
- `a7626de` — the SEC parser now **extracts R&D** (among others). The headline open
  item **IC-3** (AMD missed R&D → misclassified as Industrials) may now be **fixed**.
- SR-1 also **deleted** the orphaned `IndustryCodeDetectorService` + XBRL tag-matcher,
  so the unification refactor's scope likely **shrank**.

So before committing to the 5-phase unification refactor, **re-verify what's actually
still broken at 4.8.** This session is primarily an **investigation + scoping** task
that ends with an updated decision, not necessarily a big code change.

## 2. Open items to re-verify (live, at 4.8)

| ID | Claim (pre-4.8) | Re-verify |
|---|---|---|
| **IC-3** | AMD misses R&D extraction → misclassified | Does AMD now classify as semiconductor/MFG? (SR-1 `a7626de` should have fixed it.) |
| **IC-2** | Owned-store retailers (TGT, HD, COST, LOW) → Industrials by heuristic | Still misclassified? `isRetailCompany` predicate. |
| **VAL-6** | Healthcare REITs (Healthpeak/Omega/MPW) bypass HEALTHCARE_REIT multiple because name keyword `health/medical` matches HEALTH parent first | Still mis-routed? Tracker: [docs/reviewer/VAL-6-healthcare-reit-keyword-precedence-collision.md](../../reviewer/VAL-6-healthcare-reit-keyword-precedence-collision.md) |
| **IC-1** | `getIndustryCode` ignores SIC even though `Classify` is available | Still the dual-authority root cause? |
| basket | `CLASSIFIER_MISMATCH` on EQIX/PLD/JPM | Are these genuine mismatches or benign? |

FEEDBACK-LOG context: the 2026-04-23/24 rules (SIC-over-heuristic; retail predicate;
R&D extraction) in [docs/FEEDBACK-LOG.md](../../FEEDBACK-LOG.md).
Refactor spec: [docs/refactoring/spec/industry-classification-unification-spec.md](../../refactoring/spec/industry-classification-unification-spec.md) (5 phases — make SIC the single source of truth; final phase is a BREAKING response change).

## 3. Goal & scope

**Goal (this session):**
1. Live-classify AMD + TGT/HD/COST/LOW + Healthpeak(PEAK)/Omega(OHI)/MPW and confirm
   which of IC-2/IC-3/VAL-6 still reproduce at 4.8.
2. **Update the trackers** with the 4.8 truth (close what SR-1 fixed; keep what remains).
3. **Re-scope** `industry-classification-unification-spec.md` to the now-smaller surface
   and recommend: do the unification refactor, or just targeted fixes for what's left.

**Optional (only if cheap + clearly correct):** the minimal IC-1 fix — make
`getIndustryCode` prefer `Classify(sic,…)` and fall back to the heuristic only when SIC
is missing (FEEDBACK-LOG "Fix path A"). **If you change cleaning classification, the
recompute-shadow snapshots WILL drift — that needs a reviewed regen, so prefer to
spec it and defer the code change unless the session has room.**

**Non-goals:** no valuation-math change; don't whack-a-mole keyword priorities (VAL-6's
real fix is the global SIC-over-keyword precedence in the unification spec); no
CalcVersion bump for a re-verify.

## 4. Mandatory workflow — WORKTREE (per FEEDBACK-LOG 2026-06-04)

**Never edit master's working tree. Work in an isolated git worktree** — even for a
mostly-investigation session, because you'll edit trackers/spec and may run the server.

```bash
REPO="c:/Users/Yonatan Levin/Documents/Programming/Projects/FinTech/Strade/midas"
WT="$TEMP/midas-wt-classify"      # any path OUTSIDE the Strade tree
git -C "$REPO" worktree add -b docs/classification-reverify "$WT" master
cd "$WT"
```

- **Live classify** via a dev server (same recipe as the baseline runbook): build,
  seed a key (`go run ./cmd/seed-demo-key -db ./data/midas.db` prints it), start a
  cold instance on port 8090 (`SERVER_PORT=8090 PORT=8090 SCHEDULER_ENABLED=false`,
  `ENVIRONMENT` unset), `GET /api/v1/fair-value/<T>?trace=1`, then read `industry`
  (sic/heuristic/match) + `11-classify.json` in the bundle. **SEC works; Yahoo may 429
  (server has cookie/crumb + Finzive fallback).** Prune untracked `artifacts/<date>/`
  when done (only `tier2-baseline/**` is tracked).
- **gopls lies in worktrees**; **CRLF warnings benign**; **rebase before merge**;
  **guarded FF merge** (main checkout on master + clean); **cleanup** worktree + branch.
- **NEVER touch** `midas-dc1-phase-5-followup/` or `claude/sharp-morse-*` worktrees.
- **Stop any server you start** (`Stop-Process` the port-8090 PID) and prune temp.

## 5. Code map

| Where | What |
|---|---|
| [internal/services/datacleaner/industry/classifier.go](../../../internal/services/datacleaner/industry/classifier.go) | Both classifiers: `Classify` (SIC) + `ClassifyIndustry` (heuristic) + `isRetailCompany` + keyword priorities. |
| [internal/services/datacleaner/service.go](../../../internal/services/datacleaner/service.go) | `getIndustryCode` — the IC-1 dual-authority site (line moved post-SR-1; find by name). |
| [internal/api/v1/handlers/fair_value.go](../../../internal/api/v1/handlers/fair_value.go) | `sicToGICS` map + `BuildIndustryFromResult` + the `match` flag. |
| [config/datacleaner/industry_codes.json](../../../config/datacleaner/industry_codes.json) | SIC/NAICS/keyword → label mappings (source of truth for `Classify`). |
| [internal/services/datacleaner/industry/classifier_regressions_test.go](../../../internal/services/datacleaner/industry/classifier_regressions_test.go) | AMD retail-misclassification regression pins (extend here). |

## 6. Acceptance criteria

- A written 4.8 verdict per item (IC-1/IC-2/IC-3/VAL-6 + basket mismatches):
  reproduced / fixed-by-SR-1 / benign — with live evidence (the `industry` block +
  `11-classify.json`).
- Trackers updated (close IC-3 if SR-1 fixed it; keep the rest with current status).
- `industry-classification-unification-spec.md` re-scoped with a go/no-go recommendation.
- If any code changed: full suite green (`CGO_ENABLED=1 go test ./...`), classifier
  regression pins extended, and **recompute-shadow drift reviewed + regenerated** if
  cleaning classification changed (`git diff internal/integration/testdata/recompute-shadow/`).
- **DDM bit-for-bit preserved** — if you touch FIN classification, re-validate JPM/BAC/WFC
  **end-to-end through the live DDM path** (the golden-fixture `TestDDM_LegacyPath_BitForBit`
  is input-pinned and will NOT catch live cleaner drift).

## 7. Validation (project gate — FEEDBACK-LOG 2026-05-30)

If code changes: `/execute` B-V-R-Q (VERIFIER + REVIEWER + QA subagents) + `gpt-5.5`
codereview. If docs/scoping only: a REVIEWER pass on the verdict + spec re-scope is enough.

---

## STARTING PROMPT (copy-paste)

> Resume midas bucket-D work item **D-0 (classification re-verify at 4.8)**. Read
> `docs/refactoring/implementations/D0-classification-reverify-handoff.md` in full and
> follow it. This is investigation + scoping FIRST, not a big build. Engine is at
> `CalculationVersion 4.8` (master `91e0b05`); SR-1 `a7626de` added R&D extraction
> (likely fixes IC-3/AMD) and deleted the orphaned classifier cruft, so re-verify what's
> actually still broken. Live-classify AMD, TGT/HD/COST/LOW, and PEAK/OHI/MPW via a
> cold dev server (`?trace=1`, read the `industry` block + `11-classify.json`), and
> confirm which of IC-1/IC-2/IC-3/VAL-6 still reproduce. Then update the trackers with
> the 4.8 truth and re-scope `docs/refactoring/spec/industry-classification-unification-spec.md`
> with a go/no-go on the SIC-unification refactor. Work in an **isolated git worktree off
> master**; prune any untracked `artifacts/<date>/` and stop any server you start; rebase
> before a guarded FF merge. Only make code changes if cheap and clearly correct — and if
> you change cleaning classification, regenerate recompute-shadow snapshots (reviewed) and
> re-validate JPM/BAC/WFC end-to-end through the live DDM path.
