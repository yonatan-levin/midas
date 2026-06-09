# TDB-9 — Implementer plan (DOCUMENTED DEFER)

**Disposition:** DOCUMENTED DEFER (see `docs/refactoring/spec/tdb-9-industry-mapping-coverage-spec.md`).
The actionable work is comment/doc-only: resolve the bare TODO into a tracked, criteria-based note
+ record the coverage analysis. **Zero behavior change** ⇒ shadow byte-identical.

## Tasks
1. **DONE — Coverage analysis** → `docs/refactoring/spec/tdb-9-industry-mapping-coverage-spec.md`
   (the gating deliverable: the ~9 uncovered GICS sectors + the graceful-fallback mechanism + why
   expansion is driver-gated domain work).
2. **DONE — Resolve the bare TODO** at `internal/services/datacleaner/service.go:504`
   (`// TODO: Add more industry mappings as needed`) → a tracked note referencing TDB-9 / #9 that
   documents the GICS-sector→override-file mapping, the graceful fall-through, the 4-step add
   procedure, and the driver gate (incl. the DDM-bit-for-bit caution for Financials/40). Comment-only.
3. **DONE — Tracker update** → `docs/reviewer/archive/TDB-9-industry-mapping-expansion.md` (disposition +
   acceptance + link to spec).

## Validation (GOWORK=off)
- `go build ./...` + `go vet ./...` exit 0 (comment-only — must still compile clean).
- Full `go test ./... -count=1` exit 0 (no behavior change).
- Named invariants: `TestDDM_LegacyPath_BitForBit`, `TestRecomputeUmbrellas_NoMutation`,
  `TestOrchestrator_LedgerOrdering`, `TestLedger_BasketSnapshot_*` — all green (untouched).
- **Shadow gate (HARD):** `git diff --quiet internal/integration/testdata/recompute-shadow/` exit 0.
- **No bare TODO:** `grep -n "TODO" internal/services/datacleaner/service.go` — the
  `Add more industry mappings` line is gone; any remaining TODO-ish text references TDB-9/#9.

## Commit
`docs(datacleaner): resolve open-ended industry-mapping TODO; document coverage + defer (#9)`

## Non-goals (explicitly deferred — require a concrete driver)
- Authoring new `<sector>.json` rule-override files (domain work that changes cleaner output).
- Any change to `industry_codes.json`, the classifier, or `getIndustryCode`.
- Touching the graceful-fallback warning string at `service.go:244` (could affect `result.Warnings`).
