---
name: debug-session
description: Start a structured debugging session with proper tooling. Uses zen-thinkdeep for root cause analysis and follows QA triage patterns.
---

# Debug Session Skill

When invoked with `@debug-session {problem description}`, start a structured debugging session with proper analysis and tooling.

## Purpose

Provides a systematic approach to debugging, ensuring root causes are identified rather than symptoms being patched. Follows the DEBUG mode workflow from the project standards.

## Debug Workflow

```
┌─────────────────────────────────────────────────────────────┐
│                      DEBUG MODE FLOW                         │
├─────────────────────────────────────────────────────────────┤
│  1. TRIAGE (QA Role)                                         │
│     ├── Assign severity                                      │
│     ├── Reproduce issue                                      │
│     └── Identify responsible area                            │
│                                                              │
│  2. INVESTIGATE (BACKEND/FRONTEND Role)                      │
│     ├── Root cause analysis                                  │
│     ├── Identify fix                                         │
│     └── Implement fix                                        │
│                                                              │
│  3. VERIFY (QA Role)                                         │
│     ├── Confirm fix                                          │
│     ├── Add regression test                                  │
│     └── Check for side effects                               │
└─────────────────────────────────────────────────────────────┘
```

## Automatic Actions

### Step 1: Problem Analysis

Use `user-zen-thinkdeep` with high thinking mode to:
- Understand the problem statement
- Form initial hypotheses
- Identify areas to investigate

### Step 2: Severity Assessment

Assign severity based on impact:

| Severity | Criteria | Response |
|----------|----------|----------|
| **CRITICAL** | Prod down, data loss, security breach | Immediate, skip optional steps |
| **HIGH** | Major feature broken, blocking users | Priority fix |
| **MEDIUM** | Degraded experience, workaround exists | Fix in current cycle |
| **LOW** | Minor/cosmetic, edge case | Fix if quick |

### Step 3: Reproduction

Attempt to reproduce the issue:
- Identify reproduction steps
- Capture error logs/stack traces
- Note environment (dev/staging/prod)

### Step 4: Root Cause Analysis

Use `user-zen-thinkdeep` to:
- Trace the code path
- Identify where behavior diverges from expected
- Form and test hypotheses

### Step 5: Store Context

Use `user-memory-create_entities` to store:
- Problem description
- Reproduction steps
- Hypotheses tested
- Root cause identified
- Fix approach

## Required Output Format

```
## Debug Session Started

### Problem Statement
{Clear description of the issue}

### Severity Assessment
**Severity**: {CRITICAL | HIGH | MEDIUM | LOW}
**Reason**: {Why this severity level}

### Reproduction
**Status**: ✓ Reproduced / ✗ Cannot reproduce

**Steps**:
1. {step 1}
2. {step 2}
3. {step 3}

**Error Output**:
```
{error message, stack trace, or unexpected behavior}
```

### Initial Hypotheses
1. {Hypothesis 1} - {likelihood: high/medium/low}
2. {Hypothesis 2} - {likelihood}
3. {Hypothesis 3} - {likelihood}

### Files to Investigate
1. {file 1} - {reason}
2. {file 2} - {reason}

### Debug Strategy
1. {First investigation step}
2. {Second step}
3. {Third step}

### MCP Tools Active
- zen-thinkdeep: Root cause analysis
- memory: Storing findings
- perplexity: Research if needed

---

**ROLE**: QA (Triage) → BACKEND/FRONTEND (Fix)
**MODE**: DEBUG
```

## Investigation Output Format

After investigation:

```
## Root Cause Identified

### Summary
{1-2 sentence description of root cause}

### Evidence
- {Evidence 1}
- {Evidence 2}
- {Evidence 3}

### Root Cause Location
**File**: {file path}
**Line(s)**: {line numbers}
**Code**:
```{language}
{problematic code snippet}
```

### Why This Causes the Issue
{Explanation of how this code leads to the observed behavior}

### Proposed Fix

**Approach**: {Description of fix approach}

**Changes Required**:
1. {Change 1}
2. {Change 2}

**Potential Side Effects**:
- {Side effect 1 - how to mitigate}
- {Side effect 2 - how to mitigate}

### Regression Test Required
- [ ] Test that reproduces the bug (fails before fix)
- [ ] Test passes after fix
- [ ] No existing tests broken

---

**HANDOFF_TO**: BACKEND/FRONTEND (to implement fix)
```

## Post-Fix Verification

```
## Debug Session Complete

### Fix Applied
- **Commit**: {commit description}
- **Files Changed**: {list}

### Verification
- [ ] Original issue no longer reproduces
- [ ] Regression test added and passes
- [ ] No new issues introduced
- [ ] Related functionality tested

### Regression Test Added
**File**: {test file}
**Test Name**: {test name}
**Coverage**: Reproduces exact scenario that caused bug

---

**HANDOFF_TO**: REVIEWER → QA → HUMAN
```

## Composability

This skill can be used with:
- `@load-context {path}` - get service, package, or infrastructure context first
- `@preflight` - ensure you have full project context
- `@review-prep` - after fix is implemented

## Example Usage

```
User: @debug-session Users are getting 500 errors when uploading large files

AI: [Uses zen-thinkdeep for analysis]
    [Assesses severity: HIGH]
    [Forms hypotheses about file size limits, memory, timeouts]
    [Identifies files to investigate]
    [Stores context in memory]
    [Outputs debug session plan]
```

## Research Support

If the issue involves unfamiliar territory:
- Use `user-perplexity-ask-perplexity_ask` for research
- Use `user-context7-query-docs` for library documentation
- Store findings in memory for reference
