# Local CI Skill

When invoked with `@local-ci`, run local CI checks and attempt to fix any errors found.

## Purpose

Provides a comprehensive local CI workflow that:
1. Runs lint, type check, and tests on affected services
2. Automatically attempts to fix lint errors
3. Identifies and helps fix test failures
4. Prepares code for commit

## Usage

```
@local-ci                    # Run full local CI on all changed files
@local-ci quick              # Run quick mode (lint only)
@local-ci fix                # Run and auto-fix all issues
@local-ci <service>          # Run CI for specific service (api, auth, frontend, etc.)
```

## Workflow

### Standard Mode (`@local-ci`)

```
┌─────────────────────────────────────────────────────────────────────┐
│                         LOCAL CI WORKFLOW                            │
├─────────────────────────────────────────────────────────────────────┤
│  1. DETECT                                                          │
│     ├── Get list of changed files (staged + unstaged)               │
│     └── Identify affected services                                  │
│                                                                     │
│  2. LINT                                                            │
│     ├── Run linter on affected services                             │
│     ├── If errors: attempt auto-fix with --fix                      │
│     └── Report remaining issues                                     │
│                                                                     │
│  3. TYPE CHECK                                                      │
│     ├── Run TypeScript compiler                                     │
│     └── Report type errors with locations                           │
│                                                                     │
│  4. TEST                                                            │
│     ├── Run tests for affected services                             │
│     ├── Report failures with details                                │
│     └── Suggest fixes for common patterns                           │
│                                                                     │
│  5. SUMMARY                                                         │
│     ├── Overall pass/fail status                                    │
│     ├── List of issues to fix                                       │
│     └── Suggest next steps                                          │
└─────────────────────────────────────────────────────────────────────┘
```

### Quick Mode (`@local-ci quick`)

Only runs linting - fast validation for quick iterations.

### Fix Mode (`@local-ci fix`)

Attempts to automatically fix all issues:
1. Runs `npm run lint:fix` for lint errors
2. Applies suggested TypeScript fixes where possible
3. Updates test expectations if appropriate

## Automatic Actions

### Step 1: Detect Changes

Run these commands to detect what needs to be checked:

```bash
# Get staged files
git diff --cached --name-only

# Get unstaged changes
git diff --name-only

# Combined
git status --porcelain
```

### Step 2: Identify Affected Services

Map changed files to services:

| Path Pattern | Service |
|-------------|---------|
| `services/api/**` | api |
| `services/auth/**` | auth |
| `services/chat/**` | chat |
| `services/worker/**` | worker |
| `services/frontend/**` | frontend |
| `packages/shared-domain/**` | shared-domain |
| `packages/shared-infrastructure/**` | shared-infrastructure |
| `packages/jwt-validation/**` | jwt-validation |

### Step 3: Run Checks

For each affected service, run:

```powershell
# Lint
npm run -w <workspace> lint

# Lint with auto-fix
npm run -w <workspace> lint -- --fix

# Type check (backend)
npm run -w <workspace> build

# Type check (frontend)
npx -w services/frontend tsc --noEmit

# Tests
npm run -w <workspace> test
```

### Step 4: Fix Issues

When issues are found:

1. **Lint Errors**: 
   - Run `npm run -w <workspace> lint -- --fix`
   - For remaining errors, use the StrReplace tool to fix

2. **Type Errors**:
   - Read the error message and file location
   - Use the StrReplace tool to fix type issues

3. **Test Failures**:
   - Read the test output to understand the failure
   - Check if it's an assertion error or runtime error
   - Fix the code or update the test as appropriate

## Output Format

```
## Local CI Results

### Changes Detected
- **Staged files**: X
- **Unstaged files**: Y
- **Affected services**: service1, service2

### Stage 1: Linting
| Service | Status | Issues |
|---------|--------|--------|
| api | ✓ Passed | 0 |
| frontend | ✗ Failed | 3 errors |

### Stage 2: Type Check
| Service | Status | Issues |
|---------|--------|--------|
| api | ✓ Passed | 0 |
| frontend | ✓ Passed | 0 |

### Stage 3: Tests
| Service | Status | Passed | Failed |
|---------|--------|--------|--------|
| api | ✓ | 354 | 0 |
| frontend | ✗ | 638 | 2 |

### Issues to Fix
1. **frontend**: Lint error in `src/components/Button.tsx:15` - 'unused variable'
2. **frontend**: Test failure in `Button.test.tsx` - expected 'Click' but got 'Submit'

### Auto-Fix Applied
- [x] Fixed 2 lint errors in frontend
- [ ] 1 issue requires manual fix

### Next Steps
- Fix remaining issues above
- Run `git add .` to stage changes
- Run `git commit -m "your message"` to commit
```

## Integration with Git Hooks

This skill works alongside the pre-commit hook:

1. **Before committing**: Run `@local-ci` to check and fix issues
2. **On commit**: Pre-commit hook verifies everything passes
3. **On push**: Pre-push hook runs quick validation

## Commands Reference

### Run specific service CI
```
@local-ci api       # API Gateway only
@local-ci frontend  # Frontend only
@local-ci packages  # All packages
```

### Skip stages
```
@local-ci --skip-tests    # Skip tests, run lint + type only
@local-ci --skip-lint     # Skip lint, run type + tests only
```

### Full validation (like CI)
```
@local-ci full    # Run everything including gitleaks
```

## Error Recovery

If local CI fails repeatedly:

1. **Check for infrastructure issues**:
   - Is Docker running? (for services needing DB)
   - Are dependencies installed? (`npm install`)

2. **Reset and retry**:
   ```bash
   npm ci                    # Clean install
   npm run build             # Rebuild all
   @local-ci                 # Retry
   ```

3. **Bypass for emergency** (NOT recommended):
   ```bash
   git commit --no-verify
   ```

## Related Skills

- `@preflight` - Full pre-flight checklist before implementation
- `@review-prep` - Prepare changes for code review
- `@debug-session` - Debug failing tests or code issues
