# TDB-10 — Residual sub-TODO triage: XBRL matcher & flag evaluator

**MODE:** PLAN_AND_CREATE · **ROLE:** ARCH
**Ticket:** TDB-10 / GitHub issue #10 · **Status:** OPEN
**Tracker:** `docs/reviewer/TDB-10-residual-subtodos-xbrl-flag-evaluator.md`
**Implementer plan:** `docs/refactoring/implementations/tdb-10-subtodo-triage-implementation-plan.md`
**Worktree:** `.claude/worktrees/tdb-10-subtodo-triage` · branch `worktree-tdb-10-subtodo-triage` · **always `GOWORK=off`**
**Engine version impact:** NONE. No `CalculationVersion` / `SchemaVersion` bump. DCF/DDM/FFO/revenue_multiple outputs unchanged.

---

## 1. Summary

This is a **triage-then-act** ticket over 7 residual sub-TODOs in two datacleaner files
(`xbrl_matcher.go`, `flag_evaluator.go`). Both files were filed under HIGH-priority systems
the TODO catalog already marks COMPLETE, so these are sub-feature polish, not formal features.

The decisive question was wiring: **are these two services on the live production fair-value
path, or peripheral/aspirational subsystems?** The investigation (§2) shows a clean split, which
drives the dispositions:

- **`XBRLTagMatcherService` is fully PERIPHERAL** — zero non-test callers, not in DI, not on the
  ingestion path (production XBRL is `sec/parser.go`'s `findValue`). Its 3 TODOs are small,
  self-contained pure functions whose integration tests **already exist** and assert the very
  behavior the TODOs gate. Because nothing production reads this service, implementing them
  **cannot move any valuation output** — they are pure, zero-blast-radius completions of an
  already-tested-but-stubbed validator. → **IMPLEMENT all 3.**
- **`FlagConditionEvaluatorService` is SPLIT** — its evaluation path (`EvaluateFlags`) IS on the
  cleaner path, but its **action-execution** path (`ExecuteActions` → log/alert/transform stubs)
  has **zero non-test callers** and is dead in production. A separate real `alerting` service
  exists. → **IMPLEMENT** the one reachable, valuable item (date-condition `:384`); **DE-SCOPE**
  the three dead action stubs (`:487`, `:495`, `:502`) with inline notes referencing #10.

**Net: 4 IMPLEMENT, 3 DE-SCOPE.** A DE-SCOPE is an explicitly encouraged outcome here — the
acceptance bar is "each sub-TODO is implemented OR explicitly de-scoped with an inline note; no
bare `TODO` without a tracking reference."

---

## 2. Wiring findings (with grep evidence)

### 2.1 `XBRLTagMatcherService` — PERIPHERAL / ORPHANED (no production caller)

Constructor + config loader callers across the whole repo:

```
$ rg -n 'NewXBRLTagMatcherService|LoadXBRLConfig'
internal/services/datacleaner/xbrl_matcher.go:26   func NewXBRLTagMatcherService(...)   # definition
internal/config/xbrl_config.go:68                  func LoadXBRLConfig(...)             # definition
internal/integration/xbrl_tag_matcher_test.go:28   datacleaner.NewXBRLTagMatcherService(cfg, logger)  # TEST
internal/integration/xbrl_tag_matcher_test.go:190  datacleaner.NewXBRLTagMatcherService(cfg, logger)  # TEST
```

- **No DI wiring:** `rg 'XBRLTagMatcher|FlagConditionEvaluator' internal/di/` → **no matches**.
- **Not constructed in `service.go`** or any non-test production file.
- Production XBRL/financial-fact ingestion is the **SEC parser** (`internal/infra/gateways/sec/parser.go`,
  the `findValue` first-hit path documented throughout CLAUDE.md), NOT this matcher.
- The service uses **stdlib `log.Logger`** (`*log.Logger` field), not zap/logctx — consistent with
  a non-request-path utility.

**Verdict:** `XBRLTagMatcherService` is a tested-but-unwired parallel validator. It is reachable
ONLY from its own integration tests. Therefore any change to its validation helpers is invisible
to production valuation output (NF: cannot affect DDM bit-for-bit, recompute-shadow, ledger, or
basket — none of those exercise this service).

### 2.2 `FlagConditionEvaluatorService` — SPLIT (evaluation live, actions dead)

```
$ rg -n 'NewFlagConditionEvaluatorService|EvaluateFlags|ExecuteActions|\.flagEvaluator'
internal/services/datacleaner/service.go:103   flagEvaluator, err := NewFlagConditionEvaluatorService(flagConfig, nil)  # PROD wiring
internal/services/datacleaner/service.go:917   flagResults, err := s.flagEvaluator.EvaluateFlags(ctx, dataMap)          # PROD call
internal/services/datacleaner/flag_evaluator.go:403  func (s *...) ExecuteActions(...)                                   # definition
internal/integration/flag_condition_evaluator_test.go:57,156  evaluator.ExecuteActions(...)                            # TEST only
```

- **Evaluation IS live:** `createRiskWarningFlags` (`service.go:903`) builds a `dataMap` and calls
  `EvaluateFlags`. It reads **only** `result.Triggered` and `result.Details` to emit
  `entities.Flag`s. The flag-condition dispatch (`evaluateCondition`, `service.go:917` → `:186`)
  routes by `condition.Type`, including `case "date": return s.evaluateDateCondition(...)`.
- **Actions are DEAD in production:** `ExecuteActions` (the dispatcher to `executeLogAction` /
  `executeAlertAction` / `executeTransformAction`) has **zero non-test callers**. The cleaner
  never calls `ExecuteActions`. So `:487`, `:495`, `:502` are unreachable from any production code.
- **Logger is `nil` + stdlib:** the constructor is called with `NewFlagConditionEvaluatorService(flagConfig, nil)`
  — a `nil *log.Logger`. The service is NOT on the request-scoped logging path; it uses
  `context.Background()` at `service.go:904`, not the request ctx. Confirms peripheral logging posture
  and is why a request-path `s.logger.Printf` does NOT trip the `lint-logs` guard (which targets
  singleton-zap misuse, not this stdlib utility). Converting this service to `logctx` is a
  **separate concern, OUT OF SCOPE** for TDB-10.
- **A real alerting service already exists:** `internal/services/alerting/{configuration,regression_detection}.go`.
  The flag-evaluator `executeAlertAction` (email/webhook) TODO is **redundant** with that subsystem
  and would duplicate it inside a dead code path.
- **Config evidence:** `config/datacleaner/flag_conditions.json` contains `alert`/`log`/`transform`
  action declarations (e.g. `:123 "type":"alert" → :125 "type":"email"`, `:392 "type":"alert" → :394 "type":"dashboard"`),
  but because `ExecuteActions` is never invoked, **none of these action blocks execute today**.
  There is **no `"type":"date"` condition** anywhere in `flag_conditions.json` (verified). So the
  date-condition path is reachable-by-design but **currently unexercised by shipped config** — a
  good "complete the capability + unit-test it; no production behavior change today" candidate.

---

## 3. Triage table

| # | TODO | file:line | What it does today | Live path? | Disposition | Rationale |
|---|------|-----------|--------------------|-----------|-------------|-----------|
| 1 | date/duration type validation | `xbrl_matcher.go:276` | `case "date","duration":` arm of `validateDataType` is a no-op (always passes) | Peripheral (no prod caller) | **IMPLEMENT** | Self-contained; the config has a `data_type:"date"` mapping (`fiscal_year_end`); existing test file already drives `validateDataType` for other types. Tiny, pure, zero blast radius. |
| 2 | regex pattern matching | `xbrl_matcher.go:378` | `validateFormat` reads `params["pattern"]` then discards it (`_ = pattern`) | Peripheral | **IMPLEMENT** | The `format`/`pattern` machinery is config-shaped and trivial to honor with `regexp`. Self-contained, pure. |
| 3 | consistency checks | `xbrl_matcher.go:388` | `validateConsistency` always returns nil | Peripheral | **IMPLEMENT** | `xbrl_tag_mappings.json` has a real `balance_sheet_equation` consistency rule (`assets = liabilities + equity`, tolerance 0.01) AND an existing test `ValidateMatches_BalanceSheetEquation_Success` that only proves the happy path because the validator is a no-op. Implement the equation + tolerance check; add the missing failure test. |
| 4 | date-condition evaluation | `flag_evaluator.go:384` | `evaluateDateCondition` returns `(false, "not yet implemented")` | **Live eval path** (reachable via `EvaluateFlags`→`evaluateCondition`) | **IMPLEMENT** | On a reachable dispatch arm. Self-contained (parse + compare dates). No shipped flag uses `"type":"date"` today, so it changes **no current production output** — but it removes a dead arm and is unit-testable. Gated by config opt-in. |
| 5 | different log levels | `flag_evaluator.go:487` | `executeLogAction` `Printf`s regardless of level | **Dead** (`ExecuteActions` has no prod caller) | **DE-SCOPE** | Aspirational config-action stub. Not invoked in production. Stdlib logger; the service is not on the request/logctx path. Implementing log-level routing on a never-called action adds untested dead code. Document inline. |
| 6 | alert mechanism (email/webhook) | `flag_evaluator.go:495` | `executeAlertAction` just `Printf`s | **Dead** | **DE-SCOPE** | Redundant with the real `internal/services/alerting/` subsystem. Building email/webhook inside a dead, untested code path duplicates an existing service and invites drift. Document inline, point at `services/alerting`. |
| 7 | data-transformation logic | `flag_evaluator.go:502` | `executeTransformAction` returns nil | **Dead** | **DE-SCOPE** | Aspirational config-action stub with no live config consumer and no caller. Document inline. |

---

## 4. Designs for IMPLEMENT items

All four are **small, pure, additive**. None touches the cleaner's mutated `*FinancialData`, the
adjustment ledger, umbrellas, or any valuation model. Items 1–3 are invisible to production (no
caller); item 4 is on a reachable arm but inert under shipped config.

### 4.1 Item 1 — `validateDataType` date/duration (`xbrl_matcher.go:276`)

**Goal:** the `case "date", "duration":` arm should accept values that are plausibly a date/duration
and reject obviously-wrong types, mirroring the existing string/number/boolean arms (which return a
typed error on mismatch).

**Design (pseudocode):**

```
case "date":
    switch v := value.(type) {
    case time.Time:
        return nil
    case string:
        if isParseableDate(v) { return nil }
        return fmt.Errorf("expected date, got unparseable string %q", v)
    default:
        return fmt.Errorf("expected date, got %T", value)
    }
case "duration":
    switch v := value.(type) {
    case string:
        // XBRL durations are ISO-8601 (e.g. "P1Y", "P3M") OR plain date strings
        // for period contexts. Accept ISO-8601 duration prefix or a parseable date.
        if isISO8601Duration(v) || isParseableDate(v) { return nil }
        return fmt.Errorf("expected duration, got %q", v)
    default:
        return fmt.Errorf("expected duration, got %T", value)
    }
```

- `isParseableDate` tries a small fixed list of layouts: `time.RFC3339`, `"2006-01-02"`,
  `"2006-01-02T15:04:05"`, `"01/02/2006"`, `"--01-02"` (XBRL `dei:CurrentFiscalYearEndDate` form,
  e.g. `--09-28`). Keep the list short and documented; do NOT pull a new dependency.
- `isISO8601Duration` is a single compiled regex `^P(?:\d+Y)?(?:\d+M)?(?:\d+D)?...` (or a minimal
  `^P\d` heuristic) — no new dependency.
- **Lenient-by-default:** unknown/empty values flow through as today (the arm only rejects clearly
  wrong shapes). This keeps it from rejecting real filings.

**Blast radius:** none in production (no caller). The arm is exercised only by the matcher's own
integration tests.

### 4.2 Item 2 — `validateFormat` regex (`xbrl_matcher.go:378`)

**Goal:** when a `format` validation rule supplies a `pattern`, compile it once and fail the rule if
the string value does not match.

**Design (pseudocode):**

```
if pattern, ok := params["pattern"].(string); ok && pattern != "" {
    re, err := s.compiledRegexForPattern(pattern)   // memoize via s.compiledRegexs
    if err != nil {
        return fmt.Errorf("invalid format pattern %q: %w", pattern, err)
    }
    if !re.MatchString(strValue) {
        return fmt.Errorf(errorMsg)   // nolint:staticcheck — consistent with validateRange
    }
}
return nil
```

- Reuse the existing `compiledRegexs map[string]*regexp.Regexp` field on the struct (already present,
  currently only initialized, never populated for format rules). Add a tiny lazy
  `compiledRegexForPattern(pattern) (*regexp.Regexp, error)` helper that compiles-on-first-use and
  caches. (No need to pre-compile at construction — format rules are rare and this keeps the change
  local.)
- Error semantics match `validateRange` exactly (return `errorMsg`, same `nolint` posture) for
  consistency.

**Blast radius:** none in production (no caller).

### 4.3 Item 3 — `validateConsistency` (`xbrl_matcher.go:388`)

**Goal:** honor the `balance_sheet_equation` consistency rule
(`total_assets = total_liabilities + stockholders_equity`, `tolerance: 0.01`).

**Design (pseudocode):**

```
// params: { "equation": "total_assets = total_liabilities + stockholders_equity",
//           "tolerance": 0.01 }
equation, _ := params["equation"].(string)
if equation == "" { return nil }   // nothing to check

lhsField, rhsFields, ok := parseLinearEquation(equation)   // "A = B + C" → "A", ["B","C"]
if !ok {
    // Unsupported equation shape: log + skip (do not fail valid data on a config we can't parse)
    s.logger.Printf("validateConsistency: unsupported equation %q, skipping", equation)
    return nil
}

lhs, ok1 := s.lookupNumeric(data, lhsField)
if !ok1 { return nil }   // missing operand → cannot check → skip (lenient)
var rhs float64
for _, f := range rhsFields {
    v, ok := s.lookupNumeric(data, f)
    if !ok { return nil }   // missing operand → skip
    rhs += v
}

tolerance := floatParam(params, "tolerance", 0.0)   // relative tolerance
denom := math.Max(math.Abs(lhs), 1.0)
if math.Abs(lhs-rhs)/denom > tolerance {
    return fmt.Errorf(errorMsg)   // nolint:staticcheck
}
return nil
```

- `parseLinearEquation` supports the single shape the config uses today: `"<lhs> = <rhs1> + <rhs2> [+ ...]"`.
  Anything else → log-and-skip (lenient: never fail real data on an unparseable equation we don't
  understand). This keeps the surface minimal and avoids building an expression engine.
- `lookupNumeric` reuses `toFloat64` over `data[field]`.
- **Tolerance is relative** (fraction of the larger side, floored at 1.0 to avoid divide-by-zero) —
  matches the `0.01` (1%) intent in the config better than an absolute dollar tolerance.
- **Lenient on missing operands** (skip, don't fail) so partial XBRL sets never spuriously error.

**Blast radius:** none in production (no caller). It does make the **existing**
`ValidateMatches_BalanceSheetEquation_Success` test meaningful (it currently passes vacuously) and
enables a new failure test.

### 4.4 Item 4 — `evaluateDateCondition` (`flag_evaluator.go:384`)

**Goal:** implement date parsing + comparison so a flag condition of `"type":"date"` can compare a
field date against a literal date or relative window, using the same operator vocabulary as the
numeric path where sensible (`eq`, `ne`, `gt`/`after`, `lt`/`before`, `between`).

**Design (pseudocode):**

```
func evaluateDateCondition(condition, fieldValue) (bool, string) {
    fieldTime, ok := coerceTime(fieldValue)
    if !ok {
        return false, fmt.Sprintf("%s is not a date: %v", condition.Field, fieldValue)
    }

    switch condition.Operator {
    case "before", "lt":
        ref, ok := coerceTime(condition.Value)
        if !ok { return false, "invalid date literal in condition" }
        return fieldTime.Before(ref), describe(...)
    case "after", "gt":
        ref, ok := coerceTime(condition.Value)
        ...
        return fieldTime.After(ref), describe(...)
    case "eq":
        ref, ok := coerceTime(condition.Value)
        ...
        return fieldTime.Equal(ref), describe(...)   // or same calendar day, documented
    case "ne":
        ...
    case "between":
        if list, ok := condition.Value.([]interface{}); ok && len(list) == 2 {
            lo, ok1 := coerceTime(list[0]); hi, ok2 := coerceTime(list[1])
            ...
            return !fieldTime.Before(lo) && !fieldTime.After(hi), describe(...)
        }
    case "older_than_days", "within_days":
        // relative-to-now window using s.clock/time.Now — OPTIONAL, see Open Questions
    }
    return false, fmt.Sprintf("unsupported date operator: %s", condition.Operator)
}
```

- `coerceTime` accepts `time.Time` and a small fixed list of string layouts (shared spirit with item
  1 — extract a shared `parseFlexibleDate(string) (time.Time, bool)` helper, or duplicate the tiny
  layout list if cross-package sharing is awkward; both files are in package `datacleaner`, so a
  single unexported helper can be shared).
- **No new dependency.** Uses `time` only.
- **Inert under shipped config:** no flag in `flag_conditions.json` uses `"type":"date"`, so this
  changes **no current production output**. It removes the dead "not yet implemented" arm and is
  fully unit-tested. If/when an operator adds a date flag, it now works.
- **Relative-to-now operators** (`older_than_days`, `within_days`) are the only piece that needs a
  clock. The evaluator has no injected clock today and uses `time.Now()` directly in
  `evaluateSingleFlag`. **Recommendation (Open Question Q1): ship only the absolute-comparison
  operators** (`before`/`after`/`eq`/`ne`/`between`) in this ticket and defer relative-window
  operators (they need a clock seam for deterministic tests). This keeps the change minimal and
  test-deterministic.

---

## 5. DE-SCOPE inline-note wording (exact text to apply)

Replace each bare `TODO` with a tracking-referenced de-scope note. Exact strings:

### 5.1 `flag_evaluator.go:487` (`executeLogAction`)

```go
	// DE-SCOPED (TDB-10 / #10): per-level log routing is not implemented because
	// ExecuteActions has no production caller — the cleaner path uses EvaluateFlags
	// only (service.go: createRiskWarningFlags) and reads Triggered/Details. This
	// action dispatcher is exercised solely by integration tests. Revisit if flag
	// actions are ever wired into the request path (would also require a logctx seam).
	s.logger.Printf("[%s] %s", level, message)
```

### 5.2 `flag_evaluator.go:495` (`executeAlertAction`)

```go
	// DE-SCOPED (TDB-10 / #10): redundant with the real alerting subsystem at
	// internal/services/alerting/ (configuration.go + regression_detection.go).
	// ExecuteActions has no production caller, so building email/webhook here would
	// add untested dead code that duplicates an existing service. If flag-driven
	// alerts become a real requirement, route them through internal/services/alerting.
	s.logger.Printf("ALERT: Flag %s triggered - %s", result.FlagName, result.Details)
```

### 5.3 `flag_evaluator.go:502` (`executeTransformAction`)

```go
	// DE-SCOPED (TDB-10 / #10): aspirational config-action stub with no live config
	// consumer and no caller (ExecuteActions is test-only). No transformation grammar
	// is defined and none is needed by the shipped flag_conditions.json. Left as a
	// no-op intentionally; revisit only if/when ExecuteActions is wired into production.
	return nil
```

> Note: these three live inside `ExecuteActions`'s dispatch. The dispatcher itself
> (`executeAction`) and the working `executeSetFieldAction` are left untouched.

---

## 6. Regression-safety statement

**No load-bearing invariant can be affected by this ticket.**

- **Items 1–3 (XBRL matcher):** the service has **zero production callers** (§2.1). It is reachable
  only from `xbrl_tag_matcher_test.go`. Therefore implementing its validation helpers **cannot
  alter** any cleaner output, adjustment ledger entry, umbrella, or valuation result. The
  recompute-shadow snapshots, DDM bit-for-bit goldens, ledger-ordering, and basket tests do not
  exercise this service at all — they remain byte-identical/bit-identical by construction.
- **Item 4 (date condition):** lives on the `EvaluateFlags` dispatch, which IS on the cleaner path.
  But **no shipped flag uses `"type":"date"`** (§2.2), so the `case "date":` arm is never reached by
  production config. The change replaces a `(false, "not yet implemented")` return with a real
  evaluation for an unused arm → **zero change to current flag output** → `createRiskWarningFlags`
  emits the same flags → cleaner output unchanged → shadow/DDM/ledger/basket unchanged.
- **DE-SCOPE items 5–7:** comment-only edits. Code paths (`executeLogAction`/`executeAlertAction`/
  `executeTransformAction`) keep their exact current behavior. Zero functional change.
- **Logging posture:** none of the implemented code adds a log line on the request path. Items 1–3
  do not log (they return typed errors). Item 4 does not log. We do **not** add any new
  singleton-logger request-path violation, so the `lint-logs` guard is unaffected. Converting the
  pre-existing stdlib `s.logger.Printf` calls to `logctx` is explicitly **out of scope** (separate
  concern; these services are not on the request-scoped logging path).
- **Mandatory shadow gate:** `git diff --quiet internal/integration/testdata/recompute-shadow/`
  MUST exit 0 after the change (no regeneration permitted by this ticket). If it does not, the change
  has an unexpected side effect and must be reverted.

**Defensive gating:** items 1–4 are all **lenient-by-default** — they only reject clearly-wrong
shapes and skip on missing/unparseable operands. None can newly fail a previously-passing real
dataset, even hypothetically if a future caller wired the matcher in.

---

## 7. Test strategy (IMPLEMENT items only)

TDD: RED first (failing test), then GREEN. All tests run with `GOWORK=off`.

| Item | Test file | Cases (RED→GREEN) |
|------|-----------|-------------------|
| 1 date/duration | `internal/integration/xbrl_tag_matcher_test.go` (extend `TestXBRLTagMatcherDataTypes`) | valid `time.Time` → pass; valid date string (`--09-28`, `2024-01-31`) → pass; ISO-8601 duration (`P1Y`) → pass; garbage string for `date` → error; non-string non-time for `date` → error. |
| 2 regex format | `internal/integration/xbrl_tag_matcher_test.go` (new subtest under `TestXBRLTagMatcher`) | pattern matches → no error; pattern does not match → error with rule's `errorMsg`; invalid pattern → compile error surfaced; absent pattern → no error (skip). |
| 3 consistency | `internal/integration/xbrl_tag_matcher_test.go` | **new failure test** `ValidateMatches_BalanceSheetEquation_Imbalanced_Error` (assets ≠ liabilities + equity beyond tolerance → error); within-tolerance imbalance → pass; missing operand → pass (skip); the existing `..._Success` test still passes (now non-vacuously). |
| 4 date condition | `internal/integration/flag_condition_evaluator_test.go` OR a new `flag_evaluator_date_test.go` | `before`/`after`/`eq`/`ne`/`between` over `time.Time` and string-date field values; non-date field value → `(false, "...not a date...")`; unsupported operator → `(false, "unsupported date operator...")`. Build a synthetic `config.Condition{Type:"date", ...}` directly (no need to add a date flag to shipped config). |

**Regression pins to keep green (run after each GREEN):**
- `go test ./internal/services/datacleaner/... ./internal/config/... ./internal/integration/...` (the touched packages),
- the named invariants via the full suite (`go test ./... -count=1`): `TestDDM_LegacyPath_BitForBit`,
  `TestRecomputeUmbrellas_NoMutation`, `TestOrchestrator_LedgerOrdering`,
  `TestLedger_BasketSnapshot_ClusterPrediction`, `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction`,
- shadow byte-identity: `git diff --quiet internal/integration/testdata/recompute-shadow/`.

---

## 8. Open questions & recommendations

- **Q1 (date relative-window operators).** Ship only absolute date operators
  (`before`/`after`/`eq`/`ne`/`between`) and **defer** relative-to-now operators
  (`older_than_days`/`within_days`)? — **Recommendation: YES, defer.** They need a clock seam for
  deterministic tests; the evaluator has none today. Absolute operators deliver the capability with
  no clock dependency. (Non-blocking.)
- **Q2 (shared date helper).** Both `xbrl_matcher.go` (item 1) and `flag_evaluator.go` (item 4) need
  flexible date parsing. Extract one unexported `parseFlexibleDate` in the `datacleaner` package and
  share it? — **Recommendation: YES** — single helper (e.g. in a new tiny `datecoerce.go` or
  appended to an existing file), keeps the layout list DRY. (Non-blocking; cosmetic if duplicated.)
- **Q3 (consistency equation generality).** Support only the single `A = B + C` shape used by the
  shipped config, or a small expression grammar? — **Recommendation: single shape, log-and-skip
  otherwise.** Avoids building an expression engine for an unwired validator. (Non-blocking.)
- **Q4 (could the de-scoped action stubs ever be revived?).** If a future ticket wires
  `ExecuteActions` into the cleaner path, the alert stub should route through
  `internal/services/alerting/`, not grow its own email/webhook. The inline notes record this
  intent. (Informational; no action now.)

---

## 9. Acceptance criteria

- [ ] Each of the 7 sub-TODOs is either IMPLEMENTED (1–4) or DE-SCOPED with an inline note
      referencing TDB-10 / #10 (5–7).
- [ ] **No bare `TODO`** remains in `xbrl_matcher.go` or `flag_evaluator.go` without a tracking
      reference (the implemented ones are removed; the de-scoped ones carry the #10 reference).
- [ ] New unit/integration tests cover every implemented behavior (date/duration type validation,
      regex format, consistency equation incl. a failure case, date-condition operators).
- [ ] The pre-existing `ValidateMatches_BalanceSheetEquation_Success` test passes **non-vacuously**
      (the validator now actually checks the equation).
- [ ] `GOWORK=off go test ./... -count=1` is fully green in the worktree.
- [ ] Named invariants green: `TestDDM_LegacyPath_BitForBit`, `TestRecomputeUmbrellas_NoMutation`,
      `TestOrchestrator_LedgerOrdering`, `TestLedger_BasketSnapshot_ClusterPrediction`,
      `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction`.
- [ ] `git diff --quiet internal/integration/testdata/recompute-shadow/` exits 0 (no shadow drift).
- [ ] No `CalculationVersion` / `SchemaVersion` bump; no new request-path singleton-logger usage.
