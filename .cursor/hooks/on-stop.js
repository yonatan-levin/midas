#!/usr/bin/env node
/**
 * stop Hook
 * 
 * Context-aware quality gates when AI agent completes:
 * - Only runs tests for affected services
 * - Skips tests if only docs/config were changed
 * - Runs TypeScript type checking for affected services
 * - Runs full build to catch bundling/asset issues
 * - Reports coverage for tested code
 * - Can trigger Ralph Loop (continue if quality gates fail)
 * 
 * NOTE: Uses absolute paths to work when Cursor runs as admin
 */

const { execSync } = require('child_process');
const path = require('path');
const {
  readStdin,
  respond,
  loadSession,
  clearSession,
  getServiceConfig
} = require('./utils');

// Absolute paths for Windows admin compatibility
const NODE_PATH = process.env.NODE_PATH || 'C:\\Program Files\\nodejs';
const NPM_CMD = path.join(NODE_PATH, 'npm.cmd');
const NPX_CMD = path.join(NODE_PATH, 'npx.cmd');

// Get project root from this script's location (.cursor/hooks/on-stop.js -> project root)
const PROJECT_ROOT = path.resolve(__dirname, '..', '..');

// Configuration - adjust these settings as needed
// Can also be overridden via environment variables (e.g., CURSOR_HOOK_DRY_RUN=true)
const CONFIG = {
  maxIterations: parseInt(process.env.CURSOR_HOOK_MAX_ITERATIONS) || 10,
  enableRalphLoop: process.env.CURSOR_HOOK_RALPH_LOOP === 'true' || false,
  coverageThreshold: parseInt(process.env.CURSOR_HOOK_COVERAGE_THRESHOLD) || 90,
  runTypeCheck: process.env.CURSOR_HOOK_TYPE_CHECK !== 'false',
  runBuild: process.env.CURSOR_HOOK_BUILD !== 'false',  // Run full build check
  runTests: process.env.CURSOR_HOOK_RUN_TESTS !== 'false',
  runCoverageCheck: process.env.CURSOR_HOOK_COVERAGE_CHECK !== 'false',
  runDependencyAudit: process.env.CURSOR_HOOK_DEPENDENCY_AUDIT !== 'false',
  testTimeout: parseInt(process.env.CURSOR_HOOK_TEST_TIMEOUT) || 300000,     // 5 minutes per service
  typeCheckTimeout: parseInt(process.env.CURSOR_HOOK_TYPECHECK_TIMEOUT) || 120000, // 2 minutes
  buildTimeout: parseInt(process.env.CURSOR_HOOK_BUILD_TIMEOUT) || 180000,   // 3 minutes per service
  auditTimeout: parseInt(process.env.CURSOR_HOOK_AUDIT_TIMEOUT) || 60000,    // 1 minute
  dryRun: process.env.CURSOR_HOOK_DRY_RUN === 'true' || false  // Skip actual test execution
};

/**
 * Run tests for a specific service
 */
function runServiceTests(serviceName) {
  const serviceConfig = getServiceConfig(serviceName);
  if (!serviceConfig || !serviceConfig.hasTests) {
    return { skipped: true, reason: 'No tests configured' };
  }
  
  // Dry run mode - skip actual execution
  if (CONFIG.dryRun) {
    return { skipped: true, reason: 'Dry run mode', dryRun: true };
  }
  
  try {
    // Use absolute path to npm and project root for admin compatibility
    const testCmd = serviceConfig.testCommand.replace(/^npm /, `"${NPM_CMD}" `);
    const output = execSync(testCmd, {
      cwd: PROJECT_ROOT,
      stdio: 'pipe',
      timeout: CONFIG.testTimeout,
      encoding: 'utf8'
    });
    
    return {
      success: true,
      output: output.substring(0, 500)  // Truncate for response
    };
  } catch (e) {
    return {
      success: false,
      error: e.message,
      output: (e.stdout || '').substring(0, 500)
    };
  }
}

/**
 * Run TypeScript type check for a service
 */
function runTypeCheck(serviceName) {
  const serviceConfig = getServiceConfig(serviceName);
  if (!serviceConfig) {
    return { skipped: true };
  }
  
  // Dry run mode - skip actual execution
  if (CONFIG.dryRun) {
    return { skipped: true, reason: 'Dry run mode', dryRun: true };
  }
  
  try {
    // Try tsc --noEmit for type checking without building
    // Use absolute path to npx for admin compatibility
    const tsconfigPath = path.join(PROJECT_ROOT, serviceConfig.path, 'tsconfig.json');
    const tscCommand = `"${NPX_CMD}" tsc --noEmit -p "${tsconfigPath}"`;
    execSync(tscCommand, {
      cwd: PROJECT_ROOT,
      stdio: 'pipe',
      timeout: CONFIG.typeCheckTimeout,
      encoding: 'utf8'
    });
    
    return { success: true };
  } catch (e) {
    return {
      success: false,
      error: e.message.substring(0, 300)
    };
  }
}

/**
 * Run ESLint check for a service
 */
function runLintCheck(serviceName) {
  const serviceConfig = getServiceConfig(serviceName);
  if (!serviceConfig) {
    return { skipped: true };
  }
  
  // Dry run mode - skip actual execution
  if (CONFIG.dryRun) {
    return { skipped: true, reason: 'Dry run mode', dryRun: true };
  }
  
  try {
    // Use absolute path to npx for admin compatibility
    const srcPath = path.join(PROJECT_ROOT, serviceConfig.path, 'src');
    const lintCommand = `"${NPX_CMD}" eslint "${srcPath}" --ext .ts,.tsx --max-warnings 0`;
    execSync(lintCommand, {
      cwd: PROJECT_ROOT,
      stdio: 'pipe',
      timeout: CONFIG.typeCheckTimeout,
      encoding: 'utf8'
    });
    
    return { success: true };
  } catch (e) {
    return {
      success: false,
      error: e.message.substring(0, 300)
    };
  }
}

/**
 * Run full build for a service
 * This catches issues that tsc --noEmit might miss (bundling, assets, etc.)
 */
function runBuildCheck(serviceName) {
  const serviceConfig = getServiceConfig(serviceName);
  if (!serviceConfig) {
    return { skipped: true };
  }
  
  // Dry run mode - skip actual execution
  if (CONFIG.dryRun) {
    return { skipped: true, reason: 'Dry run mode', dryRun: true };
  }
  
  try {
    // Use the service's build command from utils.js (typeCheckCommand typically runs build)
    // Replace npm with absolute path for admin compatibility
    let buildCommand = serviceConfig.typeCheckCommand || `npm run -w ${serviceConfig.path} build`;
    buildCommand = buildCommand.replace(/^npm /, `"${NPM_CMD}" `);
    
    const output = execSync(buildCommand, {
      cwd: PROJECT_ROOT,
      stdio: 'pipe',
      timeout: CONFIG.buildTimeout,
      encoding: 'utf8'
    });
    
    return { 
      success: true,
      output: output.substring(0, 200)  // Truncate for response
    };
  } catch (e) {
    // Extract useful error info
    const stderr = e.stderr || '';
    const stdout = e.stdout || '';
    const errorOutput = (stderr + stdout).substring(0, 500);
    
    return {
      success: false,
      error: e.message.substring(0, 200),
      details: errorOutput
    };
  }
}

/**
 * Run tests with coverage for a service
 */
function runCoverageCheck(serviceName) {
  const serviceConfig = getServiceConfig(serviceName);
  if (!serviceConfig || !serviceConfig.hasTests) {
    return { skipped: true, reason: 'No tests configured' };
  }
  
  // Dry run mode - skip actual execution
  if (CONFIG.dryRun) {
    return { skipped: true, reason: 'Dry run mode', dryRun: true };
  }
  
  try {
    // Run tests with coverage - use absolute path to npm for admin compatibility
    const coverageCommand = `"${NPM_CMD}" run -w ${serviceConfig.path} test -- --coverage --coverageReporters=text-summary`;
    const output = execSync(coverageCommand, {
      cwd: PROJECT_ROOT,
      stdio: 'pipe',
      timeout: CONFIG.testTimeout,
      encoding: 'utf8'
    });
    
    // Parse coverage from output
    // Looking for: All files | XX.XX | XX.XX | XX.XX | XX.XX
    const coverageMatch = output.match(/All files[^\d]*(\d+\.?\d*)/);
    const coverage = coverageMatch ? parseFloat(coverageMatch[1]) : null;
    
    return {
      success: coverage !== null && coverage >= CONFIG.coverageThreshold,
      coverage: coverage,
      threshold: CONFIG.coverageThreshold,
      meetsThreshold: coverage !== null && coverage >= CONFIG.coverageThreshold
    };
  } catch (e) {
    // Even if tests fail, try to extract coverage
    const output = e.stdout || '';
    const coverageMatch = output.match(/All files[^\d]*(\d+\.?\d*)/);
    const coverage = coverageMatch ? parseFloat(coverageMatch[1]) : null;
    
    return {
      success: false,
      coverage: coverage,
      threshold: CONFIG.coverageThreshold,
      meetsThreshold: coverage !== null && coverage >= CONFIG.coverageThreshold,
      error: 'Tests failed during coverage check'
    };
  }
}

/**
 * Run npm audit for dependency vulnerabilities
 */
function runDependencyAudit() {
  // Dry run mode - skip actual execution
  if (CONFIG.dryRun) {
    return { skipped: true, reason: 'Dry run mode', dryRun: true };
  }
  
  try {
    // Use absolute path to npm for admin compatibility
    const output = execSync(`"${NPM_CMD}" audit --json`, {
      cwd: PROJECT_ROOT,
      stdio: 'pipe',
      timeout: CONFIG.auditTimeout,
      encoding: 'utf8'
    });
    
    const auditResult = JSON.parse(output);
    const vulnerabilities = auditResult.metadata?.vulnerabilities || {};
    
    const critical = vulnerabilities.critical || 0;
    const high = vulnerabilities.high || 0;
    const moderate = vulnerabilities.moderate || 0;
    const low = vulnerabilities.low || 0;
    const total = vulnerabilities.total || 0;
    
    return {
      success: critical === 0 && high === 0,
      vulnerabilities: { critical, high, moderate, low, total },
      hasCritical: critical > 0,
      hasHigh: high > 0
    };
  } catch (e) {
    // npm audit exits with non-zero if vulnerabilities found
    try {
      const output = e.stdout || '{}';
      const auditResult = JSON.parse(output);
      const vulnerabilities = auditResult.metadata?.vulnerabilities || {};
      
      return {
        success: false,
        vulnerabilities: vulnerabilities,
        hasCritical: (vulnerabilities.critical || 0) > 0,
        hasHigh: (vulnerabilities.high || 0) > 0
      };
    } catch (parseError) {
      return {
        success: false,
        error: 'Failed to parse audit results'
      };
    }
  }
}

async function main() {
  try {
    // Read input from Cursor via stdin - contains status, loop_count, conversation_id, etc.
    const input = await readStdin();
    
    // Early exit if agent was aborted or errored - no point running quality gates
    if (input.status === 'aborted' || input.status === 'error') {
      clearSession();
      respond({
        continue: false,
        message: `Agent ${input.status}, skipping quality gates.`
      });
      return;
    }
    
    const session = loadSession();
    
    // Check if there were any testable changes
    const affectedServices = Array.from(session.affectedServices || []);
    const hasTestableChanges = session.hasTestableChanges;
    const editedFiles = session.editedFiles || [];
    
    // If no edits tracked, nothing to check
    if (editedFiles.length === 0) {
      clearSession();
      respond({
        continue: false,
        message: 'No file edits tracked in this session.'
      });
      return;
    }
    
    // If no testable changes, skip quality gates
    if (!hasTestableChanges || affectedServices.length === 0) {
      clearSession();
      respond({
        continue: false,
        message: `Session complete. ${editedFiles.length} files edited (no testable code changes).`,
        editedFiles: editedFiles.slice(0, 10)  // Show first 10
      });
      return;
    }
    
    // Run quality gates for affected services only
    const results = {
      services: [],
      allPassed: true,
      issues: [],
      warnings: [],
      dependencyAudit: null,
      docsReminder: []
    };
    
    // Per-service quality gates
    for (const serviceName of affectedServices) {
      const serviceResults = {
        name: serviceName,
        typeCheck: null,
        lint: null,
        build: null,
        tests: null,
        coverage: null
      };
      
      // 1. TypeScript check (fast, catches type errors without full build)
      if (CONFIG.runTypeCheck) {
        serviceResults.typeCheck = runTypeCheck(serviceName);
        if (serviceResults.typeCheck.success === false) {
          results.allPassed = false;
          results.issues.push(`${serviceName}: TypeScript errors`);
        }
      }
      
      // 2. Lint check (not fix, just check)
      serviceResults.lint = runLintCheck(serviceName);
      if (serviceResults.lint.success === false) {
        results.allPassed = false;
        results.issues.push(`${serviceName}: Linter errors`);
      }
      
      // 3. Build check (full build to catch bundling/asset issues)
      if (CONFIG.runBuild) {
        serviceResults.build = runBuildCheck(serviceName);
        if (serviceResults.build.success === false) {
          results.allPassed = false;
          results.issues.push(`${serviceName}: Build failed`);
        }
      }
      
      // 4. Tests
      if (CONFIG.runTests) {
        serviceResults.tests = runServiceTests(serviceName);
        if (serviceResults.tests.success === false) {
          results.allPassed = false;
          results.issues.push(`${serviceName}: Tests failed`);
        }
      }
      
      // 5. Coverage check
      if (CONFIG.runCoverageCheck) {
        serviceResults.coverage = runCoverageCheck(serviceName);
        if (serviceResults.coverage.success === false && !serviceResults.coverage.skipped) {
          if (!serviceResults.coverage.meetsThreshold) {
            results.warnings.push(`${serviceName}: Coverage ${serviceResults.coverage.coverage}% < ${CONFIG.coverageThreshold}%`);
          }
        }
      }
      
      results.services.push(serviceResults);
    }
    
    // 5. Dependency audit (once, not per-service)
    if (CONFIG.runDependencyAudit) {
      results.dependencyAudit = runDependencyAudit();
      if (results.dependencyAudit.hasCritical) {
        results.allPassed = false;
        results.issues.push(`Dependencies: ${results.dependencyAudit.vulnerabilities.critical} CRITICAL vulnerabilities`);
      }
      if (results.dependencyAudit.hasHigh) {
        results.warnings.push(`Dependencies: ${results.dependencyAudit.vulnerabilities.high} HIGH vulnerabilities`);
      }
    }
    
    // 6. Documentation sync reminder
    const docsToUpdate = Array.from(session.docsToUpdate || []);
    if (docsToUpdate.length > 0) {
      results.docsReminder = docsToUpdate.map(d => {
        if (d === 'contracts') return 'CONTRACTS.md (controller/DTO changes detected)';
        if (d === 'architecture') return 'ARCHITECTURE.md (module changes detected)';
        return d;
      });
    }
    
    // Build response
    const summary = {
      editedFiles: editedFiles.length,
      affectedServices: affectedServices,
      allPassed: results.allPassed,
      issues: results.issues,
      warnings: results.warnings,
      docsReminder: results.docsReminder,
      hasSecurityChanges: session.hasSecurityChanges || false
    };
    
    // Ralph Loop logic (if enabled)
    // Per Cursor docs: use "followup_message" to auto-submit next user message
    // loop_count is provided by Cursor via stdin (0-indexed), add 1 for human-readable display
    if (CONFIG.enableRalphLoop && !results.allPassed) {
      const iteration = (input.loop_count || 0) + 1;
      
      if (iteration >= CONFIG.maxIterations) {
        // Max iterations reached - escalate to human
        clearSession();
        
        let escalationMsg = `Quality gates failed after ${iteration} iterations. Escalating to human review.`;
        if (results.warnings.length > 0) {
          escalationMsg += `\n\nWarnings:\n${results.warnings.join('\n')}`;
        }
        
        // No followup_message = stop the loop
        respond({
          message: escalationMsg,
          summary,
          results: results.services,
          dependencyAudit: results.dependencyAudit
        });
        return;
      }
      
      // Build followup message for Ralph Loop auto-continuation
      let followupMessage = `Quality gates failed (iteration ${iteration}/${CONFIG.maxIterations}). Please fix:\n${results.issues.join('\n')}`;
      if (results.warnings.length > 0) {
        followupMessage += `\n\nWarnings (non-blocking):\n${results.warnings.join('\n')}`;
      }
      
      // followup_message triggers auto-submission as next user message
      respond({
        followup_message: followupMessage,
        summary,
        results: results.services
      });
      return;
    }
    
    // Normal completion - no followup_message means stop
    clearSession();
    
    // Build comprehensive message (for logging/info only)
    let message = '';
    
    if (results.allPassed) {
      message = `All quality gates passed for: ${affectedServices.join(', ')}`;
    } else {
      message = `Quality gates completed with issues:\n${results.issues.join('\n')}`;
    }
    
    if (results.warnings.length > 0) {
      message += `\n\nWarnings:\n${results.warnings.join('\n')}`;
    }
    
    if (results.docsReminder.length > 0) {
      message += `\n\nDocumentation reminder:\nConsider updating: ${results.docsReminder.join(', ')}`;
    }
    
    // No followup_message = agent stops normally
    respond({
      message,
      summary,
      results: results.services,
      dependencyAudit: results.dependencyAudit
    });
    
  } catch (error) {
    console.error('stop hook error:', error.message);
    clearSession();
    respond({
      continue: false,
      error: error.message
    });
  }
}

main();
