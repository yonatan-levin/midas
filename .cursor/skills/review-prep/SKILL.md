---
name: review-prep
description: Prepare code changes for review. Analyzes changes, runs linter, checks coverage, and generates a review summary with checklist.
---

# Review Prep Skill

When invoked with `@review-prep`, prepare the current code changes for code review.

## Purpose

Ensures code is ready for review by running quality checks, generating summaries, and creating a structured handoff to the REVIEWER role.

## Automatic Actions

### Step 1: Analyze Changed Files

Identify what was modified:
- New files created
- Existing files modified
- Deleted files
- Test files added/updated

### Step 2: Run Quality Checks

Execute these checks:

1. **Linter Check**
   - Use `ReadLints` on modified files
   - List any errors or warnings

2. **TypeScript Compilation**
   - Verify no type errors
   - Check for any `any` types that should be typed

3. **Test Execution**
   - Run tests for affected modules
   - Check if new tests were added

4. **Coverage Analysis**
   - Verify coverage >= 90% for new code
   - Identify uncovered lines

### Step 3: Generate Review Summary

Use `user-zen-codereview` for systematic analysis:
- Code quality
- Security concerns
- Performance implications
- Architectural compliance

### Step 4: Create Handoff Document

Generate structured summary for REVIEWER.

## Pre-Review Checklist

Before submitting for review, verify:

```
## Pre-Review Checklist

### Code Quality
- [ ] All linter errors resolved
- [ ] No TypeScript errors
- [ ] No `any` types without justification
- [ ] Comments added for complex logic
- [ ] No console.log or debugging code

### Testing
- [ ] Tests added for new functionality
- [ ] All tests pass
- [ ] Coverage >= 90% for new code
- [ ] E2E tests for new endpoints

### Documentation
- [ ] ARCHITECTURE.md updated (if structure changed)
- [ ] CONTRACTS.md updated (if API changed)
- [ ] JSDoc comments for public APIs
- [ ] TODO comments for follow-up work

### Security
- [ ] No secrets in code
- [ ] Input validation implemented
- [ ] Proper error handling (no stack traces exposed)
- [ ] Authorization checks in place

### Performance
- [ ] No N+1 queries
- [ ] Proper indexing for new queries
- [ ] Caching considered where appropriate
```

## Required Output Format

```
## Review Prep Summary

### Changes Overview
| Type | Count | Files |
|------|-------|-------|
| Added | X | {list} |
| Modified | X | {list} |
| Deleted | X | {list} |

### Quality Check Results

#### Linter
- Status: ✓ PASS / ✗ FAIL
- Errors: {count}
- Warnings: {count}

#### Tests
- Status: ✓ PASS / ✗ FAIL
- Tests run: {count}
- Tests passed: {count}
- Coverage: {percentage}%

#### TypeScript
- Status: ✓ PASS / ✗ FAIL
- Errors: {count}

### Pre-Review Checklist
{filled checklist from above}

### Key Changes to Review
1. {Main change 1 with file reference}
2. {Main change 2 with file reference}
3. {Main change 3 with file reference}

### Potential Concerns
- {Any areas that need extra attention}
- {Complex logic that should be carefully reviewed}
- {Security-sensitive code}

### Zen Code Review Analysis
{Summary from zen-codereview tool}

---

**Ready for Review**: ✓ YES / ✗ NO (fix issues first)

**HANDOFF_TO**: REVIEWER
```

## REVIEWER Expectations

The REVIEWER should focus on:
1. Correctness of business logic
2. Security vulnerabilities
3. Performance bottlenecks
4. Code maintainability
5. Test adequacy
6. Documentation completeness

## Composability

This skill works with:
- After `@tdd-setup` and implementation
- Before `@debug-session` if issues found
- Leads to REVIEWER → QA → HUMAN workflow

## Example Usage

```
User: @review-prep

AI: [Analyzes git diff or recent changes]
    [Runs linter with ReadLints]
    [Checks test status]
    [Uses zen-codereview for analysis]
    [Generates review summary]
    [Outputs handoff document]
```

## Handling Issues

If quality checks fail:

```
## Review Prep: Issues Found

### Blocking Issues (must fix before review)
1. ✗ Linter errors in {file}
2. ✗ Tests failing: {test names}
3. ✗ Coverage below 90%: {percentage}%

### Recommended Actions
1. Run `npm run lint:fix` to auto-fix linter issues
2. Fix failing tests
3. Add tests for uncovered code

**Review cannot proceed until issues are resolved.**
```
