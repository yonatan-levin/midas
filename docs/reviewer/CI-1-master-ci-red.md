# CI-1 — Master CI is red (pre-existing): golangci-lint + e2e-live + performance-test + schemathesis

**Status:** OPEN — filed 2026-06-24 during the VAL-1 Phases 2-5 merge (#17 / PR #18). **GitHub issue: #20.**
**Severity:** Medium. CI hygiene — merges only proceed because these checks are non-required; that masks real regressions and erodes signal.
**Origin:** Surfaced by the holistic `/code-review` + merge gate on VAL-1. **Confirmed PRE-EXISTING**, not introduced by VAL-1: the same four checks fail identically at master `5b26eef` and `4c4f6b4` (before VAL-1), and none of the lint-flagged symbols are VAL-1 code.
**Blocks:** Nothing hard (checks are non-required → `mergeStateStatus = UNSTABLE`, still mergeable), but it blocks *confident* green-CI merges.

---

## The four failing checks

### 1. `Test and Coverage` → golangci-lint step (concrete, the most fixable)
golangci-lint `latest` (resolved v1.64.8) reports:
- `Error return value of \`tx.Rollback\` is not checked` (errcheck)
- `func \`allPhases\` is unused` (unused)
- `func \`allOutcomes\` is unused` (unused)
- `var \`iso8601DurationRE\` is unused` / `func \`isISO8601Duration\` is unused` (TDB-10 helpers kept "for future tests")
- Warning: `Found unknown linters in //nolint directives: unused` — the existing `//nolint:unused` directive is **not honored** by v1.64.8. **Root cause = lint-version drift** (`version: latest` in the workflow).

### 2. `e2e-live`
`Process completed with exit code 1` after Go dep download. Likely needs live external-API secrets/network (SEC EDGAR / Yahoo / FRED) the runner lacks. **Needs triage.**

### 3. `performance-test`
Fails in ~3s — likely setup/infra. **Needs triage.**

### 4. `schemathesis` (Contract Fuzzing)
Installs Python deps then `exit code 1`; needs a running server + the OpenAPI spec. **Needs triage.**

## Proposed fix
1. **Lint (do first — concrete):** check `tx.Rollback`'s error (or `//nolint:errcheck` with reason); delete or correctly-annotate the unused helpers (`allPhases`, `allOutcomes`, `iso8601DurationRE`, `isISO8601Duration`); and **pin golangci-lint to a specific version** in `.github/workflows/*` (stop using `latest`) so the supported linter/directive set is stable.
2. **e2e-live / performance-test / schemathesis:** root-cause each — either provide the CI secrets/services they need or gate them behind an explicit condition (e.g. only on a `live` label / nightly schedule) and document the gate, so a clean PR run is green-or-documented rather than silently red.

## Acceptance for closing this tracker
- [ ] golangci-lint green on master (errcheck + unused fixed; golangci-lint version pinned).
- [ ] e2e-live / performance-test / schemathesis root-caused and either green or gated-with-documented-reason.
- [ ] A clean master run is green (or red-with-documented-reason), and GitHub #20 closed.

## Out of scope
- Making these checks *required* branch-protection gates — decide that after they're reliably green.
