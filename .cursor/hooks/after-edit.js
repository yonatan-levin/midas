#!/usr/bin/env node
/**
 * afterFileEdit Hook
 * 
 * Context-aware quality checks after file edits:
 * - Tracks edited files and affected services
 * - Runs lint/format only on edited files
 * - Scans for accidentally committed secrets
 * - Skips checks for non-code files (docs, config)
 */

const { execSync } = require('child_process');
const path = require('path');
const fs = require('fs');
const {
  readStdin,
  respond,
  trackEdit,
  detectService,
  isSecurityFile,
  getDocUpdateNeeded,
  getServiceConfig,
  NON_TESTABLE_EXTENSIONS
} = require('./utils');

/**
 * Check file content for potential secrets
 */
function checkForSecrets(filePath) {
  try {
    const content = fs.readFileSync(filePath, 'utf8');
    
    // Patterns that might indicate secrets
    const secretPatterns = [
      /api[_-]?key\s*[:=]\s*['"][^'"]+['"]/gi,
      /secret[_-]?key\s*[:=]\s*['"][^'"]+['"]/gi,
      /password\s*[:=]\s*['"][^'"]+['"]/gi,
      /private[_-]?key\s*[:=]\s*['"][^'"]+['"]/gi,
      /token\s*[:=]\s*['"][^'"]+['"]/gi,
      /-----BEGIN (RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----/,
      /sk-[a-zA-Z0-9]{20,}/  // OpenAI API keys
    ];
    
    const issues = [];
    for (const pattern of secretPatterns) {
      if (pattern.test(content)) {
        issues.push(`Potential secret detected matching pattern: ${pattern.source.substring(0, 30)}...`);
      }
    }
    
    return issues;
  } catch (e) {
    return [];
  }
}

/**
 * OWASP security checks for security-related files
 * Checks for common vulnerabilities in auth/security code
 */
function runSecurityChecks(filePath) {
  try {
    const content = fs.readFileSync(filePath, 'utf8');
    const issues = [];
    
    // OWASP Top 10 related patterns
    const securityPatterns = [
      // A1: Injection
      { pattern: /\$\{.*\}.*query|query.*\$\{/gi, issue: 'Possible SQL injection: template literal in query' },
      { pattern: /eval\s*\(/gi, issue: 'Dangerous eval() usage - potential code injection' },
      { pattern: /new\s+Function\s*\(/gi, issue: 'Dynamic function creation - potential code injection' },
      
      // A2: Broken Authentication
      { pattern: /password.*=.*['"][^'"]{1,8}['"]/gi, issue: 'Hardcoded short password detected' },
      { pattern: /expiresIn.*['"]?\d+s['"]?/gi, issue: 'Very short token expiration' },
      
      // A3: Sensitive Data Exposure
      { pattern: /console\.(log|info|debug).*password/gi, issue: 'Password logged to console' },
      { pattern: /console\.(log|info|debug).*token/gi, issue: 'Token logged to console' },
      { pattern: /console\.(log|info|debug).*secret/gi, issue: 'Secret logged to console' },
      
      // A5: Broken Access Control
      { pattern: /@Public\(\)/gi, issue: 'Public decorator found - verify intended' },
      { pattern: /skipAuth/gi, issue: 'Auth skip pattern found - verify intended' },
      
      // A6: Security Misconfiguration
      { pattern: /cors.*origin.*\*/gi, issue: 'CORS allows all origins (*)' },
      { pattern: /helmet\s*\(\s*\)/gi, issue: 'Helmet with no options - verify defaults are OK' },
      
      // A7: XSS
      { pattern: /innerHTML\s*=/gi, issue: 'innerHTML assignment - potential XSS' },
      { pattern: /dangerouslySetInnerHTML/gi, issue: 'dangerouslySetInnerHTML - verify input is sanitized' },
      
      // A9: Using Components with Known Vulnerabilities
      { pattern: /require\s*\(\s*['"]crypto['"]\s*\)/gi, issue: 'Using crypto module - ensure secure algorithms' },
      
      // General security
      { pattern: /TODO.*security|FIXME.*security/gi, issue: 'Security TODO/FIXME found' },
      { pattern: /\/\/\s*@ts-ignore/gi, issue: 'TypeScript ignore - may hide security issues' }
    ];
    
    for (const { pattern, issue } of securityPatterns) {
      if (pattern.test(content)) {
        issues.push(issue);
      }
    }
    
    return issues;
  } catch (e) {
    return [];
  }
}

/**
 * Run ESLint fix on a single file
 */
function runLintFix(filePath, service) {
  if (!service) return null;
  
  const config = getServiceConfig(service);
  if (!config) return null;
  
  try {
    // Try to run eslint --fix on the specific file
    const relativePath = path.relative(process.cwd(), filePath);
    execSync(`npx eslint --fix "${relativePath}"`, {
      cwd: process.cwd(),
      stdio: 'pipe',
      timeout: 30000
    });
    return { success: true };
  } catch (e) {
    // ESLint may exit with error if there are unfixable issues
    return { success: false, error: e.message };
  }
}


async function main() {
  try {
    const input = await readStdin();
    const filePath = input.file_path || '';
    
    if (!filePath) {
      respond({});
      return;
    }
    
    // Track this edit in session
    const session = trackEdit(filePath);
    
    // Get file extension
    const ext = path.extname(filePath).toLowerCase();
    
    // Skip non-code files entirely
    if (NON_TESTABLE_EXTENSIONS.includes(ext)) {
      respond({
        message: `Skipped: ${path.basename(filePath)} is not a code file`
      });
      return;
    }
    
    // Detect service
    const service = detectService(filePath);
    
    const results = {
      file: path.basename(filePath),
      service: service || 'unknown',
      checks: []
    };
    
    // 1. Check for secrets (always run on code files)
    const secretIssues = checkForSecrets(filePath);
    if (secretIssues.length > 0) {
      results.checks.push({
        name: 'secrets',
        status: 'WARNING',
        issues: secretIssues
      });
    }
    
    // 2. OWASP security checks (for security-related files)
    if (isSecurityFile(filePath)) {
      const securityIssues = runSecurityChecks(filePath);
      if (securityIssues.length > 0) {
        results.checks.push({
          name: 'owasp-security',
          status: 'WARNING',
          issues: securityIssues
        });
      } else {
        results.checks.push({
          name: 'owasp-security',
          status: 'PASS',
          message: 'No obvious security issues detected'
        });
      }
    }
    
    // 4. Run ESLint fix (if service detected)
    if (service && ['.ts', '.tsx', '.js', '.jsx'].includes(ext)) {
      const lintResult = runLintFix(filePath, service);
      if (lintResult) {
        results.checks.push({
          name: 'eslint-fix',
          status: lintResult.success ? 'PASS' : 'ATTEMPTED',
          error: lintResult.error
        });
      }
    }
    
    // 5. Check if documentation update is needed
    const docUpdates = getDocUpdateNeeded(filePath);
    if (docUpdates.length > 0) {
      results.checks.push({
        name: 'docs-update-needed',
        status: 'INFO',
        docsToUpdate: docUpdates,
        message: `Consider updating: ${docUpdates.map(d => d === 'contracts' ? 'CONTRACTS.md' : 'ARCHITECTURE.md').join(', ')}`
      });
    }
    
    // Build response message
    const hasWarnings = results.checks.some(c => c.status === 'WARNING');
    const hasFails = results.checks.some(c => c.status === 'FAIL');
    
    let message = `afterFileEdit: ${results.file}`;
    if (service) message += ` (${service})`;

    if (hasWarnings) {
      message += ' - WARNINGS detected';
    }

    if (hasFails) {
      message += ' - FAILURES detected';
    }
    
    respond({
      message,
      results: results.checks,
      sessionInfo: {
        affectedServices: Array.from(session.affectedServices || []),
        hasTestableChanges: session.hasTestableChanges,
        hasSecurityChanges: session.hasSecurityChanges || false,
        docsToUpdate: Array.from(session.docsToUpdate || []),
        editCount: session.editedFiles.length
      }
    });
    
  } catch (error) {
    console.error('afterFileEdit hook error:', error.message);
    respond({ error: error.message });
  }
}

main();
