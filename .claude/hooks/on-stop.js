#!/usr/bin/env node
/**
 * Stop hook — context-aware quality gates when Claude finishes responding.
 *
 * Only runs checks for services that were actually edited in this session:
 *  1. TypeScript type check (tsc --noEmit)
 *  2. ESLint (lint only, no fix)
 *  3. Full build (catches bundling/asset issues)
 *  4. Tests for affected services
 *  5. Coverage check (threshold: 90%)
 *  6. npm audit for dependency vulnerabilities
 *  7. Documentation sync reminder
 *
 * If any quality gate fails and the Ralph Loop is enabled,
 * the hook blocks the stop with { decision: "block", reason: "..." }
 * so Claude continues fixing the issues.
 *
 * IMPORTANT: Must check `stop_hook_active` to prevent infinite loops.
 * When Claude is already responding to a Stop hook block, this field is true.
 *
 * Exit codes:
 *   0 → Claude stops normally (stdout may contain JSON with decision)
 *   2 → Blocks the stop, stderr fed back to Claude
 */

const { execSync } = require('child_process');
const path = require('path');
const {
  PROJECT_ROOT,
  readStdin,
  respondOk,
  loadSession,
  clearSession,
  getServiceConfig,
  detectService,
  expandWithDependents
} = require('./utils');

// ──────────────────────────────────────────────
// Configuration (override via environment variables)
// ──────────────────────────────────────────────

const CONFIG = {
  // Ralph Loop is handled by the ralph-loop plugin (invoke via /ralph-loop).
  // This hook only runs quality gates and reports results.
  coverageThreshold: parseInt(process.env.CLAUDE_HOOK_COVERAGE_THRESHOLD) || 90,
  runTypeCheck: process.env.CLAUDE_HOOK_TYPE_CHECK !== 'false',
  runBuild: process.env.CLAUDE_HOOK_BUILD !== 'false',
  runTests: process.env.CLAUDE_HOOK_RUN_TESTS !== 'false',
  runCoverageCheck: process.env.CLAUDE_HOOK_COVERAGE_CHECK !== 'false',
  runDependencyAudit: process.env.CLAUDE_HOOK_DEPENDENCY_AUDIT !== 'false',
  testTimeout: parseInt(process.env.CLAUDE_HOOK_TEST_TIMEOUT) || 300000,
  buildTimeout: parseInt(process.env.CLAUDE_HOOK_BUILD_TIMEOUT) || 180000,
  typeCheckTimeout: parseInt(process.env.CLAUDE_HOOK_TYPECHECK_TIMEOUT) || 120000,
  auditTimeout: parseInt(process.env.CLAUDE_HOOK_AUDIT_TIMEOUT) || 60000,
};

// ──────────────────────────────────────────────
// Quality gate runners
// ──────────────────────────────────────────────

/**
 * Run a shell command safely, handling paths with spaces on Windows/Git Bash.
 *
 * @param {string} command  Shell command to execute
 * @param {number} timeout  Timeout in milliseconds
 * @param {string} [cwd]    Working directory override (defaults to PROJECT_ROOT)
 */
function runCmd(command, timeout, cwd) {
  const fs = require('fs');
  const effectiveCwd = cwd || PROJECT_ROOT;

  if (!fs.existsSync(effectiveCwd)) {
    return {
      success: false,
      error: `Working directory does not exist: ${effectiveCwd}`,
      output: ''
    };
  }

  try {
    const output = execSync(command, {
      cwd: effectiveCwd,
      stdio: 'pipe',
      timeout,
      encoding: 'utf8',
      windowsHide: true,
      // Explicitly use cmd.exe on Windows to avoid Git Bash path splitting
      // issues when the cwd contains spaces (e.g., "Yonatan Levin")
      shell: process.platform === 'win32' ? process.env.COMSPEC || true : true
    });
    return { success: true, output: (output || '').substring(0, 500) };
  } catch (e) {
    return {
      success: false,
      error: (e.message || '').substring(0, 300),
      output: ((e.stdout || '') + (e.stderr || '')).substring(0, 500)
    };
  }
}

function runTypeCheck(serviceName) {
  const config = getServiceConfig(serviceName);
  if (!config) return { skipped: true };

  // Go has no separate type check step — go vet covers it
  if (config.runtime === 'go') return { skipped: true, reason: 'Go — type check via go vet (lint)' };

  const tsconfig = path.join(PROJECT_ROOT, config.path, 'tsconfig.json').replace(/\\/g, '/');
  return runCmd(`npx tsc --noEmit -p "${tsconfig}"`, CONFIG.typeCheckTimeout);
}

function runLintCheck(serviceName) {
  const config = getServiceConfig(serviceName);
  if (!config) return { skipped: true };

  // Go services use go vet
  if (config.runtime === 'go') {
    return runCmd(config.lintCommand, CONFIG.typeCheckTimeout);
  }

  const src = path.join(PROJECT_ROOT, config.path, 'src').replace(/\\/g, '/');
  return runCmd(`npx eslint "${src}" --ext .ts,.tsx --max-warnings 0`, CONFIG.typeCheckTimeout);
}

function runBuildCheck(serviceName) {
  const config = getServiceConfig(serviceName);
  if (!config) return { skipped: true };

  // Go build — all packages share the same build command
  return runCmd(config.buildCommand, CONFIG.buildTimeout);
}

function runServiceTests(serviceName) {
  const config = getServiceConfig(serviceName);
  if (!config || !config.hasTests) return { skipped: true, reason: 'No tests configured' };

  // Go tests
  return runCmd(config.testCommand, CONFIG.testTimeout);
}

function runCoverageCheck(serviceName) {
  const config = getServiceConfig(serviceName);
  if (!config || !config.hasTests) return { skipped: true };

  // Go coverage: go test -cover outputs "coverage: XX.X% of statements"
  if (config.runtime === 'go') {
    const result = runCmd(
      `go test -cover ./${config.path}/...`,
      CONFIG.testTimeout
    );
    const output = result.output || '';
    // Go outputs: "coverage: 85.3% of statements"
    const match = output.match(/coverage:\s+(\d+\.?\d*)%/);
    const coverage = match ? parseFloat(match[1]) : null;
    return {
      ...result,
      coverage,
      threshold: CONFIG.coverageThreshold,
      meetsThreshold: coverage !== null && coverage >= CONFIG.coverageThreshold
    };
  }

  const result = runCmd(
    `${config.testCommand} -- --coverage`,
    CONFIG.testTimeout
  );

  const output = result.output || '';
  const match = output.match(/All files[^\d]*(\d+\.?\d*)/);
  const coverage = match ? parseFloat(match[1]) : null;

  return {
    ...result,
    coverage,
    threshold: CONFIG.coverageThreshold,
    meetsThreshold: coverage !== null && coverage >= CONFIG.coverageThreshold
  };
}

function runDependencyAudit() {
  // Go: use govulncheck if available, otherwise skip
  const result = runCmd('govulncheck ./...', CONFIG.auditTimeout);
  if (result.error && result.error.includes('not found')) {
    // govulncheck not installed — skip gracefully
    return { success: true, note: 'govulncheck not installed, skipping vulnerability audit' };
  }
  // govulncheck exits 0 if no vulns, non-zero if vulns found
  return {
    success: result.success,
    note: result.success ? 'No known vulnerabilities' : 'Vulnerabilities found',
    output: (result.output || '').substring(0, 300)
  };
}

// ──────────────────────────────────────────────
// Git diff fallback detection
// ──────────────────────────────────────────────

/**
 * Use `git diff` to detect services with uncommitted changes.
 * Catches services missed by session tracking (e.g., changes made before
 * the session started, or files edited outside the hook-tracked flow).
 *
 * @returns {string[]} Service names with uncommitted changes
 */
function getGitDiffAffectedServices() {
  try {
    const result = runCmd(
      'git diff --name-only HEAD 2>/dev/null || git diff --name-only',
      30000
    );
    if (!result.success || !result.output) return [];

    const files = result.output.split('\n').filter(Boolean);
    const services = new Set();
    for (const file of files) {
      const svc = detectService(file);
      if (svc) services.add(svc);
    }
    return [...services];
  } catch {
    return [];
  }
}

// ──────────────────────────────────────────────
// Skill reminders (docs-update, github-tracking)
// ──────────────────────────────────────────────

/**
 * Determine which skills should run before completing.
 *
 * Returns { mustRun: [...], optional: [...] }
 *  - mustRun: block the stop until these run (when there are doc/code changes)
 *  - optional: suggest but don't block
 */
function buildSkillReminders(session, editedFiles, affectedServices) {
  const mustRun = [];
  const optional = [];

  // Already reminded once — don't block again (prevents infinite loop)
  if (session.skillsReminded) {
    return { mustRun: [], optional: [] };
  }

  const hasCodeChanges = session.hasTestableChanges;
  const docsToUpdate = session.docsToUpdate || [];

  // /docs-update — when code changes touched contracts or architecture
  if (hasCodeChanges && docsToUpdate.length > 0) {
    const docNames = docsToUpdate.map(d =>
      d === 'contracts' ? 'CONTRACTS.md' : 'ARCHITECTURE.md'
    );
    mustRun.push(`/docs-update — update ${docNames.join(', ')} to reflect code changes`);
  }

  // /github-tracking — when there were meaningful code changes in services
  if (hasCodeChanges && affectedServices.length > 0) {
    mustRun.push(
      `/github-tracking — log progress for services: ${affectedServices.join(', ')}`
    );
  }

  return { mustRun, optional };
}

// ──────────────────────────────────────────────
// Main
// ──────────────────────────────────────────────

async function main() {
  try {
    const input = await readStdin();

    // CRITICAL: Prevent infinite loops.
    // When Claude is already responding to a previous Stop hook block,
    // stop_hook_active is true. We must exit 0 immediately.
    if (input.stop_hook_active) {
      respondOk({});
      return;
    }

    const session = loadSession();
    const sessionServices = session.affectedServices || [];
    const editedFiles = session.editedFiles || [];

    // Merge session-tracked services with git diff detection (catches missed services)
    const gitDiffServices = getGitDiffAffectedServices();
    const mergedServices = [...new Set([...sessionServices, ...gitDiffServices])];

    // Expand with dependent services (e.g., editing api → also build chat)
    const affectedServices = expandWithDependents(mergedServices);

    // Clean up the rules-loaded state so the next conversation starts fresh
    try {
      const rulesStateFile = path.join(__dirname, '.rules-loaded');
      if (require('fs').existsSync(rulesStateFile)) {
        require('fs').unlinkSync(rulesStateFile);
      }
    } catch { /* best effort */ }

    // Nothing edited — nothing to check
    if (editedFiles.length === 0 && gitDiffServices.length === 0) {
      clearSession();
      respondOk({});
      return;
    }

    // Only non-code edits — skip quality gates
    if (!session.hasTestableChanges && gitDiffServices.length === 0) {
      clearSession();
      respondOk({
        systemMessage: `Session: ${editedFiles.length} files edited (no testable code changes).`
      });
      return;
    }

    // If we only have git diff services (no session tracking), still run quality gates
    if (affectedServices.length === 0) {
      clearSession();
      respondOk({});
      return;
    }

    // ── Run quality gates ──
    const issues = [];
    const warnings = [];
    const serviceResults = [];

    for (const svc of affectedServices) {
      const sr = { name: svc };

      try {
        if (CONFIG.runTypeCheck) {
          sr.typeCheck = runTypeCheck(svc);
          if (sr.typeCheck.success === false) issues.push(`${svc}: TypeScript errors`);
        }

        sr.lint = runLintCheck(svc);
        if (sr.lint.success === false) issues.push(`${svc}: Lint errors`);

        if (CONFIG.runBuild) {
          sr.build = runBuildCheck(svc);
          if (sr.build.success === false) issues.push(`${svc}: Build failed`);
        }

        if (CONFIG.runTests) {
          sr.tests = runServiceTests(svc);
          if (sr.tests.success === false) issues.push(`${svc}: Tests failed`);
        }

        if (CONFIG.runCoverageCheck) {
          sr.coverage = runCoverageCheck(svc);
          if (sr.coverage && !sr.coverage.skipped && !sr.coverage.meetsThreshold) {
            warnings.push(`${svc}: Coverage ${sr.coverage.coverage}% < ${CONFIG.coverageThreshold}%`);
          }
        }
      } catch (svcError) {
        issues.push(`${svc}: Quality gate error — ${(svcError.message || '').substring(0, 100)}`);
      }

      serviceResults.push(sr);
    }

    // Dependency audit (once, not per-service)
    let auditResult = null;
    if (CONFIG.runDependencyAudit) {
      auditResult = runDependencyAudit();
      if (auditResult.vulnerabilities) {
        if (auditResult.vulnerabilities.critical > 0)
          issues.push(`Dependencies: ${auditResult.vulnerabilities.critical} CRITICAL vulnerabilities`);
        if (auditResult.vulnerabilities.high > 0)
          warnings.push(`Dependencies: ${auditResult.vulnerabilities.high} HIGH vulnerabilities`);
      }
    }

    // Documentation reminder
    const docsToUpdate = session.docsToUpdate || [];
    const docsReminder = docsToUpdate.map(d =>
      d === 'contracts' ? 'CONTRACTS.md' : 'ARCHITECTURE.md'
    );

    const allPassed = issues.length === 0;

    // ── Skill reminders (docs-update, github-tracking) ──
    const skillReminders = buildSkillReminders(session, editedFiles, affectedServices);

    // ── Normal completion ──
    clearSession();

    let message = '';
    if (allPassed) {
      message = `All quality gates passed for: ${affectedServices.join(', ')}`;
    } else {
      message = `Quality gates completed with issues:\n${issues.join('\n')}`;
    }
    if (warnings.length > 0) message += `\nWarnings: ${warnings.join(', ')}`;
    if (docsReminder.length > 0) message += `\nDocs reminder: update ${docsReminder.join(', ')}`;
    if (skillReminders.optional.length > 0) {
      message += `\nSkill reminders: ${skillReminders.optional.join(', ')}`;
    }

    respondOk({ systemMessage: message });

  } catch (error) {
    process.stderr.write(`on-stop hook error: ${error.message}\n`);
    clearSession();
    process.exit(1);
  }
}


// Safety net: if anything escapes the try/catch in main(), exit cleanly
// rather than crashing with a confusing bash error. The stop hook is a
// quality gate, not a security gate — better to skip checks than block.
main().catch(() => {
  try { clearSession(); } catch { /* ignore */ }
  respondOk({});
});
