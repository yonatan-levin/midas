# BUG-016 — `TestDataCleanerRecompute_ShadowMode_TickerBasket` hard-fails on a missing gitignored baseline fixture

**Status:** OPEN — filed 2026-06-26 during the T2-P4-W2 items 6+7 closeout (branch `chore/t2-p4-w2-close-items-6-7`, merged to master `610fd31`). **GitHub issue: #22.**
**Severity:** Medium — test/CI hygiene. `go test ./...` is RED on any checkout that lacks the local capture (i.e. a fresh clone, CI, or a teammate's machine). It masks nothing real today, but a permanently-red suite erodes signal and lets a genuine regression hide behind "oh, that one always fails" (same rationale as CI-1).
**Area:** TESTING — `internal/integration` (datacleaner shadow-mode observer test). NOT a product-code defect.
**Family:** Same "pre-existing master red" family as **CI-1 (#20)**, but a DISTINCT failure. CI-1 tracks the four **GitHub-CI checks** (golangci-lint / e2e-live / performance-test / schemathesis). THIS is a **local `go test ./...` failure** caused by a missing gitignored fixture — a different root cause, hence its own tracker.

---

## 1. Symptom

`go test -count=1 ./...` reports exactly one failing package:

```
--- FAIL: TestDataCleanerRecompute_ShadowMode_TickerBasket (0.00s)
        Error:      Received unexpected error:
                    CreateFile C:\...\midas\artifacts\tier2-baseline\2026-05-19: The system cannot find the file specified.
        Messages:   pinned shadow baseline C:\...\artifacts\tier2-baseline\2026-05-19 missing — restore it or repin shadowBaselineDate
FAIL    github.com/midas/dcf-valuation-api/internal/integration
```

All other 47 packages pass.

## 2. Root cause (confirmed)

`internal/integration/datacleaner_recompute_shadow_test.go` hard-pins a baseline date and `require.NoError`s an `os.Stat` of the directory:

- `:87` `const shadowBaselineDate = "2026-05-19"`; `:88` builds `bundleRoot = artifacts/tier2-baseline/2026-05-19`; `:90` `require.NoError(t, statErr, "pinned shadow baseline %s missing — restore it or repin shadowBaselineDate", bundleRoot)`.
- **The deeper cause is a broken `.gitignore` negation.** `.gitignore:90-98` documents the *intent* — "Ignore everything under artifacts/ EXCEPT the tier2-baseline subtree … the tier2-baseline directory holds pre-Tier-2 regression baselines that are checked in … Using `artifacts/*` (rather than `artifacts/`) so the negation can reach into the directory." **But the actual negation line `!artifacts/tier2-baseline/` is MISSING** — after `artifacts/*` the next line is `*.gz`. So the whole subtree is ignored and **was never committed**: `git ls-tree master:artifacts/tier2-baseline/` → `Not a valid object name` (0 tracked entries); `git ls-files 'artifacts/tier2-baseline/2026-05-19*'` → 0.
- Net effect: the baseline the test (and, per the `.gitignore` comment, `valuation/profile/tier2_regression_test.go`) depends on is **absent from every fresh checkout / CI / teammate machine**. On this machine only a locally-captured `2026-06-20/` exists; `2026-05-19/` was never present.
- `require.NoError` on the missing-dir `Stat` turns an absent fixture into a hard test FAILURE rather than a skip.

So the test can only pass on a machine that happens to have the exact pinned baseline captured locally — it is **environment-dependent by construction**, and the intended "checked-in baseline" safety net never existed because the gitignore negation is absent.

## 3. Why this is pre-existing and NOT introduced by the T2-P4-W2 work

- The failure reproduces on `master` independent of the items-6+7 diff. The diff (`reit_commercial`→`reit_office` rename + DDM test + docs) touches **no** `internal/integration/`, `datacleaner`, or `artifacts/` files (`git diff master...HEAD --stat` confirms).
- The renamed-archetype work was independently validated GREEN (VERIFIER/REVIEWER/QA + live server regression); the bit-for-bit DDM invariant and all valuation packages pass.
- Re-proven across four separate full-suite runs during the closeout — always the same single failure, always the missing `2026-05-19` directory.

## 4. Proposed fix (options — pick in review)

1. **(Recommended) Skip-when-absent.** Replace the `require.NoError(statErr, ...)` hard gate with a `t.Skipf("shadow baseline %s absent locally (artifacts/ is gitignored); skipping", shadowBaselineDate)`. The shadow test is an opt-in observer over locally-captured baselines; a missing local capture should SKIP, not FAIL. Keeps `go test ./...` green on a fresh clone while preserving the check where the fixture exists.
2. **Repin to a present/tracked baseline.** Point `shadowBaselineDate` at a baseline that is actually available (e.g. `2026-06-20`) — but this only helps machines that have *that* capture, so it does not fix the fresh-clone case on its own.
3. **Restore the intended checked-in baseline (fixes the documented intent).** Add the missing negation line `!artifacts/tier2-baseline/` (and, if Git still skips nested files, `!artifacts/tier2-baseline/**`) immediately after `artifacts/*` in `.gitignore`, then `git add artifacts/tier2-baseline/<date>/` and commit the baseline the test pins. This is what the `.gitignore` comment always intended; without it the "checked-in baselines" never existed. Caveat: verify the baseline's on-disk size before committing.

Option 1 is the smallest change that makes a fresh checkout green and matches the test's intent (an observer, not a required gate). Option 3 additionally restores the lost "committed golden baseline" guarantee that `tier2_regression_test.go` also relies on — the two can be combined (skip-when-absent **and** commit a real baseline so it normally runs).

## 5. Acceptance for closing this tracker

- [ ] `go test -count=1 ./...` is green on a **fresh clone with no local `artifacts/`** (test SKIPs or passes against a committed fixture — not a hard FAIL).
- [ ] The chosen behavior (skip / repin / track) is documented inline at the test so a future reader understands why a missing baseline is not a failure.
- [ ] GitHub #22 closed.

## 6. Out of scope

- The four GitHub-CI checks tracked separately in **CI-1 (#20)** (`docs/reviewer/CI-1-master-ci-red.md`).
- Re-capturing a fresh CalcVersion-4.10 replay baseline (a separate operator task already noted under the DC-1 Phase 5 replay follow-up).
