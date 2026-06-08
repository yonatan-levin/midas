# TDB-10 — Residual sub-TODOs in XBRL matcher & flag evaluator

**Status:** IMPLEMENTED 2026-06-08 (branch `worktree-tdb-10-subtodo-triage`) — 4 IMPLEMENT + 3 DE-SCOPE landed. VERIFIER VERIFIED (suite 0 FAIL, shadow exit 0, no bare TODO, named invariants green, consistency non-vacuous); REVIEWER APPROVE_WITH_NITS; QA PASS. Filed 2026-06-06 (TODO-catalog burn-down pass).
**Priority:** P4 — Tier 4 (polish within already-"complete" systems).
**Type:** Enhancement.
**Mirrored as GitHub issue:** `[TDB-10]` (yonatan-levin/midas).
**Origin:** 2026-06-06 burn-down — in-code residue found while grepping TODOs. These live inside HIGH-priority systems the catalog already marks COMPLETE (XBRL tag matching, flag conditions), so they are sub-feature polish, not formal catalog items.

---

## Context — residual sub-TODOs

**`internal/services/datacleaner/xbrl_matcher.go`:**
- `:276` add date/duration validation
- `:378` implement regex pattern matching
- `:388` implement consistency checks (e.g. assets = liabilities + equity)

**`internal/services/datacleaner/flag_evaluator.go`:**
- `:384` implement date-condition evaluation
- `:487` implement different log levels
- `:495` implement alert mechanism (email, webhook, etc.)
- `:502` implement data-transformation logic

## Scope

Each sub-TODO is independently small. The right disposition for several may be **explicit de-scope** (e.g. the flag-evaluator alert mechanism may be redundant with the existing alerting service) rather than implementation. Treat this as a triage-then-act ticket.

## Triage outcome (ARCH, 2026-06-08)

- **Spec:** `docs/refactoring/spec/tdb-10-subtodo-triage-spec.md`
- **Implementer plan:** `docs/refactoring/implementations/tdb-10-subtodo-triage-implementation-plan.md`

**Wiring verdict (decisive):**
- `XBRLTagMatcherService` (`xbrl_matcher.go`) is **PERIPHERAL / ORPHANED** — `NewXBRLTagMatcherService` + `LoadXBRLConfig` have **zero non-test callers** (not in `internal/di/`, not in `service.go`, not on the SEC ingestion path; production XBRL is `sec/parser.go`'s `findValue`). Reachable only from its own integration tests ⇒ implementing its validators is invisible to valuation output.
- `FlagConditionEvaluatorService` (`flag_evaluator.go`) is **SPLIT** — `EvaluateFlags` **is** on the cleaner path (`service.go:917` via `createRiskWarningFlags`, reading only `Triggered`/`Details`), but `ExecuteActions` (→ log/alert/transform stubs) has **zero non-test callers** and is dead in production. A real alerting service already exists at `internal/services/alerting/`.

**Dispositions: 4 IMPLEMENT, 3 DE-SCOPE.**

| # | TODO | file:line | Disposition | Rationale |
|---|------|-----------|-------------|-----------|
| 1 | date/duration validation | `xbrl_matcher.go:276` | **IMPLEMENT** | Small, pure; config has `data_type:"date"` (`fiscal_year_end`); zero blast radius (no prod caller). |
| 2 | regex pattern matching | `xbrl_matcher.go:378` | **IMPLEMENT** | `format`/`pattern` machinery is config-shaped; trivial with `regexp`. |
| 3 | consistency checks | `xbrl_matcher.go:388` | **IMPLEMENT** | Config has a real `balance_sheet_equation` rule; existing happy-path test passes vacuously — implement + add a failure test. |
| 4 | date-condition evaluation | `flag_evaluator.go:384` | **IMPLEMENT** | On a reachable dispatch arm; inert under shipped config (no flag uses `type:"date"`) ⇒ no current output change; unit-testable. |
| 5 | different log levels | `flag_evaluator.go:487` | **DE-SCOPE** | Dead path (`ExecuteActions` has no prod caller). Inline note → #10. |
| 6 | alert mechanism | `flag_evaluator.go:495` | **DE-SCOPE** | Redundant with `internal/services/alerting/`; dead path. Inline note → #10. |
| 7 | data-transformation | `flag_evaluator.go:502` | **DE-SCOPE** | Aspirational stub, no caller/config consumer. Inline note → #10. |

**Regression safety:** no load-bearing invariant can be affected. Items 1–3 have no production caller; item 4's `date` arm is unreached by shipped config; DE-SCOPE items are comment-only. No `CalculationVersion`/`SchemaVersion` bump; shadow byte-identity preserved; no new request-path singleton-logger usage (logctx conversion is explicitly out of scope).

## Acceptance
- [x] Each sub-TODO is either implemented or explicitly de-scoped with an inline note referencing TDB-10 / #10 (4 IMPLEMENT + 3 DE-SCOPE + 1 DEFERRED note for relative-to-now date operators)
- [x] Tests cover any newly-implemented behavior (incl. the balance-sheet `..._Imbalanced_Error` failure test — the prior happy-path test now passes non-vacuously)
- [x] No remaining bare `TODO` comments without a tracking reference in these two files (`grep TODO` → zero; the 4 notes carry `(TDB-10 / #10)`)
- [x] `GOWORK=off go test ./... -count=1` green; named invariants green; shadow diff exits 0

## Implementation status (2026-06-08 — VERIFIED / APPROVE_WITH_NITS / PASS)

- **New** `internal/services/datacleaner/datecoerce.go` — shared unexported `parseFlexibleDate`/`coerceTime`/`isISO8601Duration` (stdlib `time`/`regexp` only; layouts incl. XBRL `--09-28`), consumed by items 1 + 4.
- **Item 1** `xbrl_matcher.go::validateDataType` — `date`/`duration` arms now type-check (accept `time.Time`/parseable/ISO-8601 duration; typed error otherwise).
- **Item 2** `xbrl_matcher.go::validateFormat` — `params["pattern"]` compiled once via lazy `compiledRegexForPattern` (reuses the existing `compiledRegexs` cache); mismatch → rule `errorMsg`.
- **Item 3** `xbrl_matcher.go::validateConsistency` — `A = B + C` via `parseLinearEquation`/`lookupNumeric` with RELATIVE tolerance `|lhs−rhs|/max(|lhs|,1) > tol`; **lenient** (missing operand → skip; unparseable → log+skip). Failure pinned by `..._Imbalanced_Error`.
- **Item 4** `flag_evaluator.go::evaluateDateCondition` — absolute operators `before/after/eq/ne/between` via `coerceTime`; relative-to-now DEFERRED (no clock seam) with an inline `DEFERRED (TDB-10 / #10)` note. Returns `(bool,string)`, does NOT log. Inert under shipped config (no `type:"date"` flag).
- **Items 5–7** `flag_evaluator.go` `executeLogAction`/`executeAlertAction`/`executeTransformAction` — `DE-SCOPED (TDB-10 / #10)` inline notes (dead `ExecuteActions` path / redundant with `services/alerting` / aspirational); stub behavior unchanged.

**Validation:** `GOWORK=off go build/vet ./...` exit 0; full `go test ./... -count=1` exit 0 (0 FAIL); shadow gate exit 0 (byte-identical); DDM bit-for-bit / recompute-no-mutation / ledger-ordering / basket green; `grep TODO` → zero bare TODOs.

## Deferred NITs (REVIEWER 2026-06-08, advisory, non-blocking)
- `xbrl_matcher.go::compiledRegexForPattern` lazily writes `s.compiledRegexs` without a mutex — safe today (the service is orphaned/single-threaded); add a `sync.RWMutex`/`sync.Map` IF it's ever wired into a concurrent production path.
- `fmt.Errorf(errorMsg)` at the format/consistency sites passes a config string as the format verb (a literal `%` would garble) — matches the pre-existing `validateRange` posture; harden to `fmt.Errorf("%s", errorMsg)` in a future sweep.

**Status:** IMPLEMENTED — see header.
