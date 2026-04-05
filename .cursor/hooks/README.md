# Cursor Hooks

Context-aware automation hooks for the Muppet AI project.

## Overview

These hooks automatically run at specific points in the AI agent's workflow:

| Hook | Trigger | Purpose | Behavior |
|------|---------|---------|----------|
| `beforeReadFile` | Before AI reads a file | Security - blocks sensitive files | **Fail-closed** (errors block reads) |
| `afterFileEdit` | After AI edits a file | Quality - lint, format, secret scan, OWASP checks | Observational |
| `stop` | When AI completes task | Testing - run tests, coverage, audit for affected services | Can auto-continue |

## Output Formats (Per Cursor Docs)

Each hook has a specific output format:

### beforeReadFile Output
```json
{
  "permission": "allow" | "deny",
  "user_message": "Message shown when denied (optional)"
}
```

### afterFileEdit Output
```json
{
  // Observational only - any JSON for logging
  "message": "Status message",
  "results": [...]
}
```

### stop Output
```json
{
  // Optional: triggers auto-continuation (Ralph Loop)
  "followup_message": "Message submitted as next user input",
  // Additional fields for logging
  "message": "Status message",
  "summary": {...}
}
```

## Quality Gates Summary

### afterFileEdit (per file)

| Check | Description | Status |
|-------|-------------|--------|
| Secret Detection | Scans for hardcoded secrets, API keys | WARNING if found |
| OWASP Security | Checks security files for vulnerabilities | WARNING if found |
| Prettier | Auto-formats code | PASS/FAIL |
| ESLint Fix | Auto-fixes linter issues | PASS/ATTEMPTED |
| Doc Update Reminder | Tracks if CONTRACTS.md or ARCHITECTURE.md needs update | INFO |

### stop (end of session)

| Check | Description | Status |
|-------|-------------|--------|
| TypeScript Check | Type validation per service | Blocking |
| Lint Check | Final lint verification | Blocking |
| Build Check | Full build to catch bundling/asset issues | Blocking |
| Tests | Run tests for affected services | Blocking |
| Coverage Check | Verify >= 90% coverage | Warning |
| Dependency Audit | npm audit for vulnerabilities | Critical = Blocking, High = Warning |
| Doc Reminder | Remind to update docs if needed | Info |

## Context-Aware Logic

The hooks are **smart** about what they run:

- **Service Detection**: Automatically detects which service (api, auth, chat, worker, frontend) was affected
- **Testability Check**: Skips tests if only docs, config, or non-code files were edited
- **Targeted Testing**: Only runs tests for affected services, not the entire codebase
- **Security Awareness**: Extra OWASP checks for auth/security-related files
- **Documentation Tracking**: Detects changes to controllers/modules that need doc updates

## Configuration

### Environment Variables

All config can be overridden via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `CURSOR_HOOK_DRY_RUN` | `false` | Skip actual test/lint execution |
| `CURSOR_HOOK_RALPH_LOOP` | `false` | Enable iterative fixing |
| `CURSOR_HOOK_MAX_ITERATIONS` | `10` | Max Ralph Loop iterations |
| `CURSOR_HOOK_RUN_TESTS` | `true` | Run tests on stop |
| `CURSOR_HOOK_TYPE_CHECK` | `true` | Run TypeScript check |
| `CURSOR_HOOK_BUILD` | `true` | Run full build check |
| `CURSOR_HOOK_COVERAGE_CHECK` | `true` | Run coverage check |
| `CURSOR_HOOK_COVERAGE_THRESHOLD` | `90` | Coverage percentage required |
| `CURSOR_HOOK_DEPENDENCY_AUDIT` | `true` | Run npm audit |
| `CURSOR_HOOK_TEST_TIMEOUT` | `300000` | Test timeout (5 min) |
| `CURSOR_HOOK_TYPECHECK_TIMEOUT` | `120000` | Type check timeout (2 min) |
| `CURSOR_HOOK_BUILD_TIMEOUT` | `180000` | Build timeout (3 min) |
| `CURSOR_HOOK_AUDIT_TIMEOUT` | `60000` | Audit timeout (1 min) |

### CONFIG in on-stop.js

```javascript
const CONFIG = {
  maxIterations: 10,           // Max Ralph Loop iterations
  enableRalphLoop: false,      // Set true for iterative fixing
  coverageThreshold: 90,       // Coverage requirement (%)
  runTypeCheck: true,          // TypeScript validation
  runBuild: true,              // Full build check
  runTests: true,              // Run unit/integration tests
  runCoverageCheck: true,      // Check coverage threshold
  runDependencyAudit: true,    // Run npm audit
  dryRun: false                // Skip actual execution
};
```

## Files

| File | Purpose |
|------|---------|
| `hooks.json` | Hook configuration |
| `utils.js` | Shared utilities (service detection, session tracking, security patterns) |
| `before-read.js` | Security checks - blocks sensitive files |
| `after-edit.js` | Lint, format, secret detection, OWASP checks |
| `on-stop.js` | Tests, type checking, coverage, audit, quality gates |

## Session Tracking

The hooks track edited files during a session to:
- Know which services were affected
- Determine if testable code was changed
- Track security-related file changes
- Track documentation updates needed
- Run only relevant tests

Session files (auto-generated, gitignored):
- `.session-edits.json` - Current session state

Note: Ralph Loop iteration count is provided by Cursor via `input.loop_count` (stdin).

## Security Features

### beforeReadFile

Blocks access to sensitive files:
- `.env` and `.env.*` files
- Files with `credentials`, `secrets`, `password` in name
- `.pem` and `.key` files

### afterFileEdit - Secret Detection

Scans for patterns like:
- Hardcoded API keys
- Private keys
- OpenAI API keys (`sk-...`)
- Password assignments

### afterFileEdit - OWASP Security Checks

For security-related files (auth, guard, middleware, etc.), checks for:
- SQL injection patterns
- `eval()` usage
- Sensitive data in console logs
- CORS misconfigurations
- XSS vulnerabilities
- Security TODO/FIXME comments

### stop - Dependency Audit

Runs `npm audit` and reports:
- **Critical** vulnerabilities → Blocking
- **High** vulnerabilities → Warning
- **Moderate/Low** → Reported

## Documentation Sync

The hooks track when documentation should be updated:

| File Pattern | Triggers Update To |
|--------------|-------------------|
| `*.controller.ts` | CONTRACTS.md |
| `*.dto.ts` | CONTRACTS.md |
| `*.schema.ts` | CONTRACTS.md |
| `*.module.ts` | ARCHITECTURE.md |
| `app.module.ts` | ARCHITECTURE.md |
| `main.ts` | ARCHITECTURE.md |

When these files are edited, the stop hook reminds you to update documentation.

## Enabling Ralph Loop

To enable iterative fixing (AI keeps working until tests pass):

1. Set environment variable: `CURSOR_HOOK_RALPH_LOOP=true`
2. Or edit `on-stop.js` and set `CONFIG.enableRalphLoop = true`
3. AI will continue working until all quality gates pass or max iterations reached

## Known Issues

### ENAMETOOLONG on Windows

**Symptom**: `beforeReadFile` hooks fail with `execution error: spawn ENAMETOOLONG` in the Hooks output panel.

**Cause**: Windows has a ~2047 character command line limit. The `beforeReadFile` hook receives the full file content in its input JSON. For large files, this can exceed the limit when Cursor spawns the hook process.

**Impact**: Since `beforeReadFile` uses **fail-closed behavior**, a failed hook will block the file read.

**Current Mitigations**:
1. The hook handles errors gracefully and outputs `{ permission: "allow" }` on error
2. The hook script is kept minimal to reduce spawn overhead

**Workarounds**:
1. Ensure paths contain no spaces where possible
2. If you encounter frequent issues, consider disabling the `beforeReadFile` hook temporarily
3. Report persistent issues to Cursor support

### Spaces in Workspace Path

If your workspace path contains spaces (e.g., `C:\Users\John Doe\...`), you may experience more frequent `ENAMETOOLONG` errors. Consider using a path without spaces for the workspace.

## Debugging

### View Hook Output

In Cursor:
1. Open Output panel (View → Output)
2. Select "Hooks" from dropdown
3. See INPUT/OUTPUT for each hook call
4. Check STDERR for error messages

### Verify Hooks Are Active

1. Open Cursor Settings (Cmd/Ctrl + ,)
2. Search for "Hooks"
3. Check the Hooks tab shows your configured hooks
4. Verify `hooks.json` is at `.cursor/hooks.json` (project level) or `~/.cursor/hooks.json` (user level)

### Test Hooks Manually

```bash
# Test before-read
echo '{"file_path": "services/api/src/main.ts"}' | node .cursor/hooks/before-read.js

# Test after-edit
echo '{"file_path": "services/api/src/main.ts"}' | node .cursor/hooks/after-edit.js

# Test stop (dry run)
$env:CURSOR_HOOK_DRY_RUN = "true"
echo '{}' | node .cursor/hooks/on-stop.js
```

### Common Issues

| Issue | Solution |
|-------|----------|
| Hooks not running | Restart Cursor after adding/modifying `hooks.json` |
| `ENAMETOOLONG` errors | See Known Issues section above |
| Hooks blocking reads unexpectedly | Check Hooks output panel for errors |
| Tests not running | Verify session tracking file exists (`.session-edits.json`) |

## Quality Gates Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                         HOOK FLOW                               │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  AI edits file                                                  │
│       │                                                         │
│       ▼                                                         │
│  afterFileEdit                                                  │
│  ├── Track edit in session                                      │
│  ├── Detect service (api, auth, chat, worker, frontend)         │
│  ├── Check for secrets                                          │
│  ├── OWASP security check (if security file)                    │
│  ├── Run Prettier (if .ts/.tsx)                                 │
│  ├── Run ESLint --fix (if service detected)                     │
│  └── Track documentation updates needed                         │
│       │                                                         │
│       ▼                                                         │
│  (repeat for each file edit)                                    │
│       │                                                         │
│       ▼                                                         │
│  AI finishes task → stop hook fires                             │
│       │                                                         │
│       ▼                                                         │
│  on-stop                                                        │
│  ├── Load session (affected services, testable changes)         │
│  ├── If no testable changes → skip tests                        │
│  ├── For each affected service:                                 │
│  │   ├── TypeScript check                                       │
│  │   ├── Lint check                                             │
│  │   ├── Build check                                            │
│  │   ├── Run tests                                              │
│  │   └── Coverage check                                         │
│  ├── Dependency audit (npm audit)                               │
│  ├── Documentation sync reminder                                │
│  ├── If Ralph Loop enabled && gates fail:                       │
│  │   └── Return followup_message (AI auto-continues)            │
│  └── Clear session                                              │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## Customization

### Adding a New Service

Edit `utils.js` and add to the `SERVICES` object:

```javascript
'new-service': {
  path: 'services/new-service',
  testCommand: 'npm run -w services/new-service test',
  lintCommand: 'npm run -w services/new-service lint',
  typeCheckCommand: 'npm run -w services/new-service build',
  hasTests: true,
  testableExtensions: ['.ts']
}
```

### Adding Sensitive File Patterns

Edit `utils.js` and add to `SENSITIVE_PATTERNS`:

```javascript
const SENSITIVE_PATTERNS = [
  /\.env$/,
  /your-new-pattern/i,
  // ...
];
```

### Adding Security File Patterns

Edit `utils.js` and add to `SECURITY_FILE_PATTERNS`:

```javascript
const SECURITY_FILE_PATTERNS = [
  /auth/i,
  /your-security-pattern/i,
  // ...
];
```

### Adding Non-Testable Paths

Edit `utils.js` and add to `NON_TESTABLE_PATHS`:

```javascript
const NON_TESTABLE_PATHS = [
  'docs/',
  'your-new-path/',
  // ...
];
```
