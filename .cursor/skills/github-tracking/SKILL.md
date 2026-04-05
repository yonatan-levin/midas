---
name: github-tracking
description: Track work progress through GitHub issues. Creates issues, logs progress, updates labels, and maintains traceability from planning to completion.
---

# GitHub Issue Tracking Skill

When invoked with `@github-tracking {action}`, perform GitHub issue operations to track work progress through the development lifecycle.

## Purpose

Ensures all development work is tracked in GitHub issues with proper labels, task lists, and progress logs. Maintains traceability from initial request through validation cycle to completion.

## Repository

All operations target: `Muppet-AI/Muppet`

## Available Actions

| Action | Description | Used By |
|--------|-------------|---------|
| `create-feature` | Create new feature issue | ORCHESTRATOR |
| `create-bug` | Create bug issue | Any agent |
| `update-plan` | Add architecture plan and task list | ARCH |
| `log-progress` | Add progress comment | BACKEND, FRONTEND |
| `log-verification` | Add verification report | VERIFIER |
| `log-review` | Add review findings | REVIEWER |
| `log-qa` | Add QA report | QA |
| `update-labels` | Update issue labels | Any agent |
| `check-task` | Check off task in task list | Any agent |
| `get-context` | Load issue context for session continuity | Any agent |
| `close-issue` | Close issue (HUMAN approval required) | HUMAN |

---

## Label Mapping (Existing Labels)

### Status Labels
| Label | Meaning | Next State |
|-------|---------|------------|
| `planning` | Initial planning phase | `in-progress` |
| `in-progress` | Active implementation | `verification` |
| `verification` | Awaiting VERIFIER check | `review` or back to `in-progress` |
| `review` | Awaiting code review | `qa` or back to `in-progress` |
| `qa` | In QA validation | `Completed` or back to `in-progress` |
| `Completed` | Done, ready for HUMAN close | (closed) |

### Type Labels
| Label | Use For |
|-------|---------|
| `enhancement` | New features |
| `bug` | Bug reports |
| `documentation` | Doc updates |

### Domain Labels
| Label | Use For |
|-------|---------|
| `Backend` | Backend work |
| `Frontend` | Frontend work |
| `Architecture` | Architecture design |

### Phase Labels
| Label | Use For |
|-------|---------|
| `Phase-1` to `Phase-4` | Project phases |
| `MVP` | MVP scope |

### Track Labels
| Label | Use For |
|-------|---------|
| `track:technical` | Technical tasks |
| `track:ux` | UX tasks |
| `track:ui` | UI tasks |
| `track:functionality` | Feature functionality |
| `track:acceptance` | Acceptance criteria |
| `track:release` | Release tasks |

### Area Labels
| Label | Use For |
|-------|---------|
| `area:widget` | Chat widget work |
| `area:dashboard` | Dashboard work |
| `area:rag` | RAG system work |

### Priority
| Label | Use For |
|-------|---------|
| `priority-high` | High priority items |

---

## Action Details

### 1. create-feature

Creates a new feature issue when entering PLAN_AND_CREATE mode.

**Command:**
```bash
gh issue create \
  --repo Muppet-AI/Muppet \
  --title "[FEATURE] {title}" \
  --label "enhancement,planning" \
  --body "$(cat <<'EOF'
## Summary
{brief description}

## Requirements
- {requirement 1}
- {requirement 2}

## Acceptance Criteria
- [ ] {criterion 1}
- [ ] {criterion 2}
- [ ] {criterion 3}

## Tasks
<!-- Will be populated by ARCH agent -->

### Architecture
- [ ] Design API contracts
- [ ] Define data models
- [ ] Update documentation

### Implementation
- [ ] Backend implementation
- [ ] Frontend implementation (if applicable)
- [ ] Write tests (90%+ coverage)

### Validation
- [ ] VERIFIER approval
- [ ] REVIEWER approval
- [ ] QA approval

## Context
- Mode: PLAN_AND_CREATE
- Created by: ORCHESTRATOR
- Related docs: {links}

## Definition of Done
- [ ] All acceptance criteria met
- [ ] Tests passing with 90%+ coverage
- [ ] Code reviewed and approved
- [ ] QA validated
- [ ] Documentation updated
- [ ] HUMAN approval received
EOF
)"
```

**After Creation:**
1. Store issue number in memory: `memory:create_entities("active-issue", {number, title, url})`
2. Add issue number to response for other agents
3. Return issue URL

---

### 2. create-bug

Creates a bug issue when a defect is discovered during development.

**Command:**
```bash
gh issue create \
  --repo Muppet-AI/Muppet \
  --title "[BUG] {title}" \
  --label "bug,planning" \
  --body "$(cat <<'EOF'
## Description
{what is broken}

## Steps to Reproduce
1. {step 1}
2. {step 2}
3. {step 3}

## Expected Behavior
{what should happen}

## Actual Behavior
{what actually happens}

## Severity
- [ ] Critical - System unusable
- [ ] High - Major feature broken
- [ ] Medium - Feature impaired
- [ ] Low - Minor inconvenience

## Context
- Found during: {phase/task}
- Related issue: #{parent_issue_number}
- Environment: {dev/staging/prod}
- Files involved: {file paths}

## Acceptance Criteria
- [ ] Bug no longer reproducible
- [ ] Regression test added
- [ ] No new issues introduced

## Technical Notes
{any relevant technical details}
EOF
)"
```

---

### 3. update-plan

ARCH agent updates issue with architecture plan and detailed task list.

**Command:**
```bash
gh issue edit {issue_number} \
  --repo Muppet-AI/Muppet \
  --add-label "Architecture" \
  --body "$(cat <<'EOF'
## Summary
{updated summary with architecture context}

## Architecture Plan
### Design Decisions
- {decision 1}: {rationale}
- {decision 2}: {rationale}

### Components
- {component 1}: {responsibility}
- {component 2}: {responsibility}

### Data Flow
{brief description or diagram reference}

## Acceptance Criteria
- [ ] {criterion 1}
- [ ] {criterion 2}
- [ ] {criterion 3}

## Tasks

### Architecture (ARCH)
- [x] Design system architecture
- [x] Define API contracts
- [ ] Update ARCHITECTURE.md
- [ ] Update CONTRACTS.md

### Backend (BACKEND)
- [ ] Implement service layer
- [ ] Add database migrations
- [ ] Create API endpoints
- [ ] Write E2E tests

### Frontend (FRONTEND)
- [ ] Create components
- [ ] Implement state management
- [ ] Add styling
- [ ] Write component tests

### Validation
- [ ] VERIFIER: Run tests, check coverage
- [ ] REVIEWER: Code quality review
- [ ] QA: Behavior validation

## API Contracts
{endpoint definitions or link to CONTRACTS.md}

## Test Strategy
- Unit tests: {scope}
- Integration tests: {scope}
- E2E tests: {scope}
- Coverage target: 90%+

## Definition of Done
- [ ] All acceptance criteria met
- [ ] Tests passing (90%+ coverage)
- [ ] Code reviewed and approved
- [ ] QA validated
- [ ] Docs updated
- [ ] HUMAN approval
EOF
)"
```

**After Update:**
```bash
gh issue comment {issue_number} \
  --repo Muppet-AI/Muppet \
  --body "## Architecture Plan Complete

**ARCH** has completed the architecture design.

### Summary
{brief summary of design decisions}

### Next Steps
HANDOFF_TO: BACKEND/FRONTEND

Ready for implementation phase."
```

---

### 4. log-progress

Implementation agents log progress as comments.

**Command:**
```bash
gh issue edit {issue_number} \
  --repo Muppet-AI/Muppet \
  --remove-label "planning" \
  --add-label "in-progress,{Backend|Frontend}"

gh issue comment {issue_number} \
  --repo Muppet-AI/Muppet \
  --body "## Implementation Progress

**{BACKEND|FRONTEND}** - {timestamp}

### Completed
- {task 1}
- {task 2}

### In Progress
- {current task}

### Blockers
- {any blockers or none}

### Files Changed
- \`{file1}\`
- \`{file2}\`

### Notes
{any relevant notes or decisions made}

---
Status: **IN PROGRESS** | Coverage: {X%}"
```

**Check off tasks:**
```bash
# Update issue body to check completed tasks
# [x] Task that was completed
```

---

### 5. log-verification

VERIFIER adds verification report. This is a two-step process:
1. When VERIFIER starts: transition `in-progress` → `verification`
2. After verification: transition `verification` → `review` (if verified) or `verification` → `in-progress` (if not verified)

**Step 1: Start Verification (set verification label):**
```bash
gh issue edit {issue_number} \
  --repo Muppet-AI/Muppet \
  --remove-label "in-progress" \
  --add-label "verification"
```

**Step 2a: Command (VERIFIED):**
```bash
gh issue edit {issue_number} \
  --repo Muppet-AI/Muppet \
  --remove-label "verification" \
  --add-label "review"

gh issue comment {issue_number} \
  --repo Muppet-AI/Muppet \
  --body "## Verification Report

**VERIFIER** - {timestamp}

### Result: VERIFIED ✅

### Test Results
| Suite | Status | Details |
|-------|--------|---------|
| Unit Tests | ✅ | {X} passed |
| E2E Tests | ✅ | {X} passed |
| Coverage | ✅ | {X}% (target: 90%) |

### Checks Performed
- [x] Implementation exists and matches scope
- [x] All tests pass
- [x] Coverage meets requirements
- [x] No linter errors
- [x] TypeScript compiles clean

### Notes
{any observations}

---
**HANDOFF_TO: REVIEWER**"
```

**Step 2b: Command (NOT VERIFIED):**
```bash
gh issue edit {issue_number} \
  --repo Muppet-AI/Muppet \
  --remove-label "verification" \
  --add-label "in-progress"

gh issue comment {issue_number} \
  --repo Muppet-AI/Muppet \
  --body "## Verification Report

**VERIFIER** - {timestamp}

### Result: NOT VERIFIED ❌

### Issues Found
| Severity | Description | Location |
|----------|-------------|----------|
| {BLOCKER|MAJOR|MINOR} | {issue} | {file:line} |

### Required Actions
1. {action 1}
2. {action 2}

---
**HANDOFF_TO: {BACKEND|FRONTEND}** - Fix issues and re-submit"
```

---

### 6. log-review

REVIEWER adds code review findings.

**Command (APPROVE):**
```bash
gh issue edit {issue_number} \
  --repo Muppet-AI/Muppet \
  --remove-label "review" \
  --add-label "qa"

gh issue comment {issue_number} \
  --repo Muppet-AI/Muppet \
  --body "## Code Review Report

**REVIEWER** - {timestamp}

### Result: APPROVED ✅

### Strengths
- {what was done well}

### Minor Suggestions (Optional)
- {nit 1}
- {nit 2}

### Security Check
- [x] No hardcoded secrets
- [x] Input validation present
- [x] Error handling appropriate

### Code Quality
- [x] Clean Architecture followed
- [x] KISS principle applied
- [x] Tests meaningful

---
**HANDOFF_TO: QA**"
```

**Command (REJECT):**
```bash
gh issue edit {issue_number} \
  --repo Muppet-AI/Muppet \
  --remove-label "review" \
  --add-label "in-progress"

gh issue comment {issue_number} \
  --repo Muppet-AI/Muppet \
  --body "## Code Review Report

**REVIEWER** - {timestamp}

### Result: REJECTED ❌

### Critical Issues
| Severity | Location | Type | Description |
|----------|----------|------|-------------|
| {HIGH|MEDIUM} | {file:line} | {type} | {description} |

### Required Changes
1. {specific fix 1}
2. {specific fix 2}

---
**HANDOFF_TO: {BACKEND|FRONTEND}** - Address issues and re-submit"
```

---

### 7. log-qa

QA adds validation report.

**Command (PASS):**
```bash
gh issue edit {issue_number} \
  --repo Muppet-AI/Muppet \
  --remove-label "qa" \
  --add-label "Completed"

gh issue comment {issue_number} \
  --repo Muppet-AI/Muppet \
  --body "## QA Validation Report

**QA** - {timestamp}

### Result: PASS ✅

### Checks Performed
- [x] Implementation matches requirements
- [x] Acceptance criteria verified
- [x] API contracts followed
- [x] Edge cases handled
- [x] Error states appropriate

### Test Evidence
{summary of manual/automated testing}

### Notes
{any observations}

---
**Status: READY FOR HUMAN APPROVAL**

All validation stages complete. Awaiting HUMAN to close issue."
```

**Command (FAIL):**
```bash
gh issue edit {issue_number} \
  --repo Muppet-AI/Muppet \
  --remove-label "qa" \
  --add-label "in-progress"

gh issue comment {issue_number} \
  --repo Muppet-AI/Muppet \
  --body "## QA Validation Report

**QA** - {timestamp}

### Result: FAIL ❌

### Issues Found
| Severity | Agent | Description |
|----------|-------|-------------|
| {HIGH|MEDIUM|LOW} | {BACKEND|FRONTEND} | {description} |

### Required Fixes
1. {fix 1}
2. {fix 2}

---
**HANDOFF_TO: {BACKEND|FRONTEND}** - Address issues and restart validation cycle"
```

---

### 8. get-context

Load issue context for session continuity.

**Command:**
```bash
# Check memory for active issue
memory:search_nodes("active-issue")

# If found, load issue details
gh issue view {issue_number} \
  --repo Muppet-AI/Muppet \
  --json number,title,body,labels,comments

# Parse and provide context to current agent
```

**Output:**
```
## Active Issue Context

**Issue #123**: [FEATURE] {title}
**Status**: {current label}
**Last Activity**: {latest comment summary}

### Current Tasks
- [x] Completed task
- [ ] Pending task

### Recent Comments
{last 2-3 comments summarized}

### Next Steps
{based on current status}
```

---

### 9. update-labels

Update issue labels for status transitions.

**Command:**
```bash
gh issue edit {issue_number} \
  --repo Muppet-AI/Muppet \
  --remove-label "{old_label}" \
  --add-label "{new_label}"
```

**Valid Transitions:**
- `planning` → `in-progress` (implementation started)
- `in-progress` → `verification` (implementation complete)
- `verification` → `review` (verified) OR `in-progress` (not verified)
- `review` → `qa` (approved) OR `in-progress` (rejected)
- `qa` → `Completed` (pass) OR `in-progress` (fail)

---

### 10. close-issue

Close issue after HUMAN approval.

**Command:**
```bash
gh issue close {issue_number} \
  --repo Muppet-AI/Muppet \
  --comment "## Issue Closed

**HUMAN** has approved and closed this issue.

### Summary
- All acceptance criteria met
- All validation stages passed
- Ready for deployment

### Final Status
✅ COMPLETE"
```

**After Close:**
```bash
# Clear from memory
memory:delete_entities("active-issue")
```

---

## Workflow Integration

### Session Start
```
1. Check memory for active issue
2. If exists: Load context with get-context
3. If not: Wait for ORCHESTRATOR to create or user to specify
```

### Session End
```
1. Log final progress to issue
2. Update memory with current state
3. Include issue # in any commits
```

---

## Best Practices

### Do's
- Always include issue # in commit messages: `git commit -m "feat: add X (#123)"`
- Log significant decisions as comments
- Update task checkboxes as work progresses
- Use collapsible sections for long content: `<details><summary>...</summary>...</details>`
- Reference related issues with `#number`
- Tag relevant team members when needed

### Don'ts
- Don't close issues without HUMAN approval
- Don't skip validation stages
- Don't create issues for trivial changes
- Don't forget to update labels on status change
- Don't leave issues in limbo (always handoff or update)

---

## Example Usage

```
User: @github-tracking create-feature "Add rate limiting to API"

AI: [Creates issue with template]
    [Stores issue # in memory]
    [Returns issue URL]
    
    Created issue #125: [FEATURE] Add rate limiting to API
    URL: https://github.com/Muppet-AI/Muppet/issues/125
    Labels: enhancement, planning
    
    Next: ARCH should update plan with @github-tracking update-plan
```

---

## Composability

This skill chains with:
- `@preflight` - Creates issue context before implementation
- `@load-context` - Loads service context for issue creation
- `@tdd-setup` - Links tests to issue acceptance criteria
- `@review-prep` - Prepares review summary for issue

---

## Memory Integration

Store active issue in memory for session continuity:

```
# Create
memory:create_entities([{
  name: "active-issue",
  entityType: "github-issue",
  observations: [
    "issue_number: 123",
    "title: Add rate limiting",
    "status: in-progress",
    "current_agent: BACKEND"
  ]
}])

# Update
memory:add_observations("active-issue", [
  "status: review",
  "last_update: 2026-01-27"
])

# Search
memory:search_nodes("active-issue")
```
