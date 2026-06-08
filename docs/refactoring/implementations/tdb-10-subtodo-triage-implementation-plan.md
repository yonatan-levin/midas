# TDB-10 — Implementer plan: XBRL-matcher / flag-evaluator sub-TODO triage

**Ticket:** TDB-10 / #10 · **ROLE:** BACKEND (Go) · **Spec:**
`docs/refactoring/spec/tdb-10-subtodo-triage-spec.md`
**Worktree:** `.claude/worktrees/tdb-10-subtodo-triage` · branch `worktree-tdb-10-subtodo-triage`
**ALWAYS `GOWORK=off`** (own go.mod). `cd` into the worktree before any command.
**No engine-version bump. No shadow regeneration. Lenient-by-default validators.**

Disposition recap: **IMPLEMENT** items 1–4; **DE-SCOPE** items 5–7.

| # | item | file:line | disposition |
|---|------|-----------|-------------|
| 1 | date/duration type validation | `xbrl_matcher.go:276` | IMPLEMENT |
| 2 | regex format | `xbrl_matcher.go:378` | IMPLEMENT |
| 3 | consistency equation | `xbrl_matcher.go:388` | IMPLEMENT |
| 4 | date condition | `flag_evaluator.go:384` | IMPLEMENT |
| 5 | log levels | `flag_evaluator.go:487` | DE-SCOPE (note) |
| 6 | alert mechanism | `flag_evaluator.go:495` | DE-SCOPE (note) |
| 7 | transform action | `flag_evaluator.go:502` | DE-SCOPE (note) |

---

## Pre-flight

```bash
cd ".claude/worktrees/tdb-10-subtodo-triage"
GOWORK=off go build ./... 2>&1 | tail -5        # baseline green
GOWORK=off go test ./internal/services/datacleaner/... ./internal/config/... ./internal/integration/... -count=1
git diff --quiet internal/integration/testdata/recompute-shadow/ && echo "SHADOW CLEAN"
```

Record the baseline as PASS before editing. Do all work TDD: write the failing test (RED), run it,
then implement (GREEN).

---

## Task 0 — shared date helper (supports tasks 1 & 4)

**File:** new `internal/services/datacleaner/datecoerce.go` (package `datacleaner`).

Add an unexported flexible date parser shared by both items (Q2 recommendation = share):

```go
package datacleaner

import (
	"regexp"
	"time"
)

var dateLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02",
	"01/02/2006",
	"--01-02", // XBRL dei:CurrentFiscalYearEndDate, e.g. "--09-28"
}

// parseFlexibleDate attempts a small fixed set of layouts. Returns ok=false if none parse.
func parseFlexibleDate(s string) (time.Time, bool) {
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// coerceTime accepts time.Time or a parseable date string.
func coerceTime(v interface{}) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		return parseFlexibleDate(x)
	default:
		return time.Time{}, false
	}
}

var iso8601DurationRE = regexp.MustCompile(`^P(?:\d+Y)?(?:\d+M)?(?:\d+W)?(?:\d+D)?(?:T(?:\d+H)?(?:\d+M)?(?:\d+S)?)?$`)

// isISO8601Duration reports whether s looks like an ISO-8601 duration (e.g. "P1Y", "P3M").
func isISO8601Duration(s string) bool {
	return s != "P" && iso8601DurationRE.MatchString(s)
}
```

No new dependency (`time`, `regexp` only). Build: `GOWORK=off go build ./internal/services/datacleaner/...`.

---

## Task 1 — IMPLEMENT `validateDataType` date/duration (`xbrl_matcher.go:276`)

### 1a. RED — extend `TestXBRLTagMatcherDataTypes`

In `internal/integration/xbrl_tag_matcher_test.go`, add subtests that drive `validateDataType`
indirectly via `MatchSingleTag` on the `fiscal_year_end` mapping (`data_type:"date"`), plus a
direct-ish path if the test already calls a helper. Cases:
- `fiscal_year_end` value `"--09-28"` (string date) → match succeeds, no error.
- `fiscal_year_end` value `"2024-01-31"` → succeeds.
- `fiscal_year_end` value `"not-a-date"` → `MatchSingleTag` returns a data-type validation error.
- (if a `duration` mapping is added to the test config) value `"P1Y"` → succeeds; `"banana"` → error.

Run: `GOWORK=off go test ./internal/integration/ -run TestXBRLTagMatcherDataTypes -count=1` → MUST FAIL.

### 1b. GREEN — replace the no-op arm

In `xbrl_matcher.go`, replace lines 275–277 (`case "date", "duration":` + the bare TODO) with:

```go
	case "date":
		switch v := value.(type) {
		case time.Time:
			return nil
		case string:
			if _, ok := parseFlexibleDate(v); ok {
				return nil
			}
			return fmt.Errorf("expected date, got unparseable string %q", v)
		default:
			return fmt.Errorf("expected date, got %T", value)
		}
	case "duration":
		switch v := value.(type) {
		case string:
			if isISO8601Duration(v) {
				return nil
			}
			if _, ok := parseFlexibleDate(v); ok {
				return nil // period contexts sometimes carry plain date strings
			}
			return fmt.Errorf("expected duration, got %q", v)
		default:
			return fmt.Errorf("expected duration, got %T", value)
		}
```

Add `"time"` to the `xbrl_matcher.go` import block.

Run the RED test → GREEN. Then `GOWORK=off go test ./internal/integration/ -run TestXBRLTagMatcher -count=1`.

---

## Task 2 — IMPLEMENT `validateFormat` regex (`xbrl_matcher.go:378`)

### 2a. RED

Add a subtest under `TestXBRLTagMatcher` driving `ValidateMatches` against a config that includes a
`format` rule with a `pattern` (use a local test config, or extend `setupTestConfig`):
- value matches pattern → no error.
- value does not match → error containing the rule's `error_message`.
- absent pattern → no error.

Run → MUST FAIL (today `validateFormat` ignores the pattern, so the mismatch case wrongly passes).

### 2b. GREEN

Add a lazy memoized compile helper to the struct, then honor the pattern. Replace the
`if pattern, ok := params["pattern"].(string); ok {` block (lines 377–381) with:

```go
	if pattern, ok := params["pattern"].(string); ok && pattern != "" {
		re, err := s.compiledRegexForPattern(pattern)
		if err != nil {
			return fmt.Errorf("invalid format pattern %q: %w", pattern, err)
		}
		if !re.MatchString(strValue) {
			return fmt.Errorf(errorMsg) // nolint:staticcheck — matches validateRange posture
		}
	}
	return nil
```

Add the helper (reuses the existing `s.compiledRegexs` field):

```go
// compiledRegexForPattern compiles a format pattern on first use and caches it.
func (s *XBRLTagMatcherService) compiledRegexForPattern(pattern string) (*regexp.Regexp, error) {
	if re, ok := s.compiledRegexs[pattern]; ok {
		return re, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	s.compiledRegexs[pattern] = re
	return re, nil
}
```

(`regexp` is already imported.) Run RED → GREEN; rerun the package.

---

## Task 3 — IMPLEMENT `validateConsistency` (`xbrl_matcher.go:388`)

### 3a. RED — add the missing failure test

In `internal/integration/xbrl_tag_matcher_test.go`, add:

```go
	t.Run("ValidateMatches_BalanceSheetEquation_Imbalanced_Error", func(t *testing.T) {
		matches := []entities.MatchResult{
			{InternalField: "total_assets", Value: float64(1_000_000)},
			{InternalField: "total_liabilities", Value: float64(600_000)},
			{InternalField: "stockholders_equity", Value: float64(100_000)}, // 700k != 1.0M
		}
		err := matcher.ValidateMatches(context.Background(), matches)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Balance sheet does not balance")
	})
```

Run → MUST FAIL (today `validateConsistency` returns nil).

### 3b. GREEN — implement the equation check

Replace the body of `validateConsistency` (lines 387–389) with:

```go
func (s *XBRLTagMatcherService) validateConsistency(data map[string]interface{}, params map[string]interface{}, errorMsg string) error {
	equation, _ := params["equation"].(string)
	if equation == "" {
		return nil
	}

	lhsField, rhsFields, ok := parseLinearEquation(equation)
	if !ok {
		s.logger.Printf("validateConsistency: unsupported equation %q, skipping", equation)
		return nil
	}

	lhs, ok := s.lookupNumeric(data, lhsField)
	if !ok {
		return nil // missing operand → cannot check → lenient skip
	}
	var rhs float64
	for _, f := range rhsFields {
		v, ok := s.lookupNumeric(data, f)
		if !ok {
			return nil
		}
		rhs += v
	}

	tolerance := 0.0
	if t, ok := params["tolerance"].(float64); ok {
		tolerance = t
	}
	denom := math.Max(math.Abs(lhs), 1.0)
	if math.Abs(lhs-rhs)/denom > tolerance {
		return fmt.Errorf(errorMsg) // nolint:staticcheck
	}
	return nil
}

// parseLinearEquation parses "<lhs> = <rhs1> + <rhs2> [+ ...]". Returns ok=false otherwise.
func parseLinearEquation(eq string) (lhs string, rhs []string, ok bool) {
	sides := strings.SplitN(eq, "=", 2)
	if len(sides) != 2 {
		return "", nil, false
	}
	lhs = strings.TrimSpace(sides[0])
	for _, part := range strings.Split(sides[1], "+") {
		p := strings.TrimSpace(part)
		if p == "" {
			return "", nil, false
		}
		rhs = append(rhs, p)
	}
	if lhs == "" || len(rhs) == 0 {
		return "", nil, false
	}
	return lhs, rhs, true
}

// lookupNumeric reads data[field] as float64.
func (s *XBRLTagMatcherService) lookupNumeric(data map[string]interface{}, field string) (float64, bool) {
	v, exists := data[field]
	if !exists {
		return 0, false
	}
	f, err := s.toFloat64(v)
	if err != nil {
		return 0, false
	}
	return f, true
}
```

Add `"math"` to the import block (`strings` already imported). Run RED → GREEN. Verify the existing
`ValidateMatches_BalanceSheetEquation_Success` still passes (now non-vacuously).

---

## Task 4 — IMPLEMENT `evaluateDateCondition` (`flag_evaluator.go:384`)

### 4a. RED

Add `internal/integration/flag_evaluator_date_test.go` (or a subtest in the existing file) that
constructs `config.Condition{Type: "date", ...}` directly and calls a small exported-for-test seam
OR drives it through `EvaluateFlag` with a synthetic in-test flag config. Cases:
- field `time.Time` vs literal date string, operator `before`/`after`/`eq`/`ne` → expected bool.
- `between` with a 2-element `[]interface{}` of dates.
- non-date field value → `(false, "...not a date...")`.
- unsupported operator → `(false, "unsupported date operator...")`.

> If the integration test can't reach `evaluateDateCondition` without a date flag in shipped config,
> drive it via a locally-built `config.FlagConditionsConfig` passed to
> `NewFlagConditionEvaluatorService` (the test file already constructs the evaluator that way).

Run → MUST FAIL (today returns `(false, "date conditions not yet implemented")`).

### 4b. GREEN

Replace the body of `evaluateDateCondition` (lines 383–387) with the absolute-operator
implementation (Q1 recommendation = absolute only; defer relative-window operators):

```go
func (s *FlagConditionEvaluatorService) evaluateDateCondition(condition config.Condition, fieldValue interface{}) (bool, string) {
	fieldTime, ok := coerceTime(fieldValue)
	if !ok {
		return false, fmt.Sprintf("%s is not a date: %v", condition.Field, fieldValue)
	}

	switch condition.Operator {
	case "before", "lt":
		ref, ok := coerceTime(condition.Value)
		if !ok {
			return false, "invalid date literal in condition"
		}
		return fieldTime.Before(ref), fmt.Sprintf("%s %v before %v", condition.Field, fieldTime, ref)
	case "after", "gt":
		ref, ok := coerceTime(condition.Value)
		if !ok {
			return false, "invalid date literal in condition"
		}
		return fieldTime.After(ref), fmt.Sprintf("%s %v after %v", condition.Field, fieldTime, ref)
	case "eq":
		ref, ok := coerceTime(condition.Value)
		if !ok {
			return false, "invalid date literal in condition"
		}
		return fieldTime.Equal(ref), fmt.Sprintf("%s %v eq %v", condition.Field, fieldTime, ref)
	case "ne":
		ref, ok := coerceTime(condition.Value)
		if !ok {
			return false, "invalid date literal in condition"
		}
		return !fieldTime.Equal(ref), fmt.Sprintf("%s %v ne %v", condition.Field, fieldTime, ref)
	case "between":
		if list, ok := condition.Value.([]interface{}); ok && len(list) == 2 {
			lo, ok1 := coerceTime(list[0])
			hi, ok2 := coerceTime(list[1])
			if ok1 && ok2 {
				inRange := !fieldTime.Before(lo) && !fieldTime.After(hi)
				return inRange, fmt.Sprintf("%s %v between %v and %v", condition.Field, fieldTime, lo, hi)
			}
		}
		return false, "invalid date range in condition"
	default:
		return false, fmt.Sprintf("unsupported date operator: %s", condition.Operator)
	}
}
```

`coerceTime` comes from Task 0 (`datecoerce.go`, same package). `time` is already imported in
`flag_evaluator.go`. Run RED → GREEN; rerun the package.

---

## Task 5 — DE-SCOPE inline notes (items 5, 6, 7)

Comment-only edits in `flag_evaluator.go`. No behavior change.

- **`executeLogAction` (`:487`)** — replace the `// TODO: Implement different log levels` line with
  the §5.1 note from the spec (DE-SCOPED TDB-10/#10; ExecuteActions has no prod caller).
- **`executeAlertAction` (`:495`)** — replace the `// TODO: Implement alert mechanism...` line with
  the §5.2 note (redundant with `internal/services/alerting/`).
- **`executeTransformAction` (`:502`)** — replace the `// TODO: Implement data transformation logic`
  line with the §5.3 note (aspirational stub, no caller).

Use the EXACT wording in spec §5.1–§5.3.

After this task: `GOWORK=off rg -n 'TODO' internal/services/datacleaner/xbrl_matcher.go internal/services/datacleaner/flag_evaluator.go`
MUST return **zero bare TODOs** (the de-scope notes say `DE-SCOPED (TDB-10 / #10)`, not `TODO`; the
implemented ones are gone).

> Out of scope, leave as-is: the `// TODO: Add proper logging` lines in `service.go` (lines 96, 920)
> belong to a different concern (the cleaner's flag-load fallback) and are not in the two TDB-10
> files. Do not touch them under this ticket.

---

## Final validation (run all; every line must pass)

```bash
cd ".claude/worktrees/tdb-10-subtodo-triage"

# 1. Build
GOWORK=off go build ./...

# 2. Touched packages
GOWORK=off go test ./internal/services/datacleaner/... ./internal/config/... ./internal/integration/... -count=1

# 3. Full suite + named invariants
GOWORK=off go test ./... -count=1
GOWORK=off go test ./internal/services/valuation/models/ -run TestDDM_LegacyPath_BitForBit -count=1
GOWORK=off go test ./internal/services/datacleaner/ -run TestRecomputeUmbrellas_NoMutation -count=1
GOWORK=off go test ./internal/integration/ -run 'TestOrchestrator_LedgerOrdering|TestLedger_BasketSnapshot_ClusterPrediction|TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction' -count=1

# 4. Shadow byte-identity (MUST exit 0 — no regeneration)
git diff --quiet internal/integration/testdata/recompute-shadow/ && echo "SHADOW CLEAN"

# 5. No bare TODO left in the two files
GOWORK=off rg -n 'TODO' internal/services/datacleaner/xbrl_matcher.go internal/services/datacleaner/flag_evaluator.go || echo "NO BARE TODO"

# 6. gofmt / vet on touched files
GOWORK=off gofmt -l internal/services/datacleaner/xbrl_matcher.go internal/services/datacleaner/flag_evaluator.go internal/services/datacleaner/datecoerce.go
GOWORK=off go vet ./internal/services/datacleaner/...
```

Acceptance = §9 of the spec, all boxes checked.

---

## Commit template

Squash or stack per preference; reference #10 on every commit. Suggested ladder:

```
test(datacleaner): RED pins for TDB-10 XBRL date/format/consistency + flag date-condition (#10)

feat(datacleaner): implement XBRL date/duration + regex-format + consistency validators (#10)

feat(datacleaner): implement flag-evaluator date-condition (absolute operators) (#10)

chore(datacleaner): de-scope dead flag-action stubs (log-level/alert/transform) with #10 notes (#10)

Triage of the 7 residual sub-TODOs in xbrl_matcher.go + flag_evaluator.go.
IMPLEMENT (live/peripheral but cheap+tested): date/duration type validation,
regex format, balance-sheet consistency, flag date-condition. DE-SCOPE (dead
ExecuteActions path, no prod caller; alert redundant with services/alerting):
log-levels, alert mechanism, transform action.

No CalculationVersion/SchemaVersion bump. XBRL matcher has no production caller
(invisible to valuation output); the date-condition arm is inert under shipped
config (no flag uses type:"date"). Shadow byte-identical; DDM bit-for-bit,
ledger-ordering, basket invariants green.

Spec: docs/refactoring/spec/tdb-10-subtodo-triage-spec.md
Plan: docs/refactoring/implementations/tdb-10-subtodo-triage-implementation-plan.md

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
```

---

## Rollback / safety notes for the implementer

- If `git diff --quiet internal/integration/testdata/recompute-shadow/` is **non-zero** after any
  edit, STOP — something unexpected touched cleaner output. Revert and re-investigate. This ticket
  must not regenerate shadows.
- Keep every validator **lenient** (skip on missing/unparseable operands; only reject clearly-wrong
  shapes). Do not make any validator strict enough to fail a previously-passing real dataset.
- Do **not** convert the stdlib `s.logger.Printf` calls to `logctx` (separate concern). Do not add
  any new log line on the request path.
- Do **not** touch `executeAction`'s dispatcher, `executeSetFieldAction`, or the `service.go`
  `// TODO: Add proper logging` lines.
