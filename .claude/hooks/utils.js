#!/usr/bin/env node
/**
 * Shared utilities for Claude Code hooks — Midas DCF Valuation API (Go)
 *
 * Provides context-aware detection of packages, testability checks,
 * session tracking, and proper Claude Code response formatting.
 *
 * Claude Code hooks receive JSON on stdin and communicate via:
 * - exit 0 + stdout JSON → success (action proceeds)
 * - exit 2 + stderr      → blocking error (action blocked)
 * - other exit codes      → non-blocking error (action proceeds)
 */

const fs = require('fs');
const path = require('path');

// ──────────────────────────────────────────────
// Project root detection
// ──────────────────────────────────────────────

/**
 * Normalize a path that may be in Git Bash format (/c/Users/...)
 * to a native Windows path (C:/Users/...) so Node.js APIs work correctly.
 */
function normalizeGitBashPath(p) {
  if (!p) return p;
  const converted = p.replace(/^\/([a-zA-Z])\//, '$1:/');
  return path.resolve(converted);
}

/**
 * Resolve project root directory.
 * Prefers $CLAUDE_PROJECT_DIR (set by Claude Code), falls back to
 * walking up from this script's location.
 */
function getProjectRoot() {
  if (process.env.CLAUDE_PROJECT_DIR) {
    return normalizeGitBashPath(process.env.CLAUDE_PROJECT_DIR);
  }
  return path.resolve(__dirname, '..', '..');
}

const PROJECT_ROOT = getProjectRoot();

// Session tracking file (gitignored)
const SESSION_FILE = path.join(__dirname, '.session-edits.json');

// ──────────────────────────────────────────────
// Package configuration — Midas Go monolith
// ──────────────────────────────────────────────

const SERVICES = {
  api: {
    path: 'internal/api',
    testCommand: 'go test ./internal/api/...',
    lintCommand: 'go vet ./internal/api/...',
    buildCommand: 'go build ./cmd/server',
    hasTests: true,
    testableExtensions: ['.go'],
    runtime: 'go'
  },
  valuation: {
    path: 'internal/services/valuation',
    testCommand: 'go test ./internal/services/valuation/...',
    lintCommand: 'go vet ./internal/services/valuation/...',
    buildCommand: 'go build ./cmd/server',
    hasTests: true,
    testableExtensions: ['.go'],
    runtime: 'go'
  },
  datacleaner: {
    path: 'internal/services/datacleaner',
    testCommand: 'go test ./internal/services/datacleaner/...',
    lintCommand: 'go vet ./internal/services/datacleaner/...',
    buildCommand: 'go build ./cmd/server',
    hasTests: true,
    testableExtensions: ['.go'],
    runtime: 'go'
  },
  datafetcher: {
    path: 'internal/services/datafetcher',
    testCommand: 'go test ./internal/services/datafetcher/...',
    lintCommand: 'go vet ./internal/services/datafetcher/...',
    buildCommand: 'go build ./cmd/server',
    hasTests: true,
    testableExtensions: ['.go'],
    runtime: 'go'
  },
  secGateway: {
    path: 'internal/infra/gateways/sec',
    testCommand: 'go test ./internal/infra/gateways/sec/...',
    lintCommand: 'go vet ./internal/infra/gateways/sec/...',
    buildCommand: 'go build ./cmd/server',
    hasTests: true,
    testableExtensions: ['.go'],
    runtime: 'go'
  },
  marketGateway: {
    path: 'internal/infra/gateways/market',
    testCommand: 'go test ./internal/infra/gateways/market/...',
    lintCommand: 'go vet ./internal/infra/gateways/market/...',
    buildCommand: 'go build ./cmd/server',
    hasTests: true,
    testableExtensions: ['.go'],
    runtime: 'go'
  },
  financeLib: {
    path: 'pkg/finance',
    testCommand: 'go test ./pkg/finance/...',
    lintCommand: 'go vet ./pkg/finance/...',
    buildCommand: 'go build ./pkg/finance/...',
    hasTests: true,
    testableExtensions: ['.go'],
    runtime: 'go'
  },
  integration: {
    path: 'internal/integration',
    testCommand: 'go test ./internal/integration/...',
    lintCommand: 'go vet ./internal/integration/...',
    buildCommand: 'go build ./cmd/server',
    hasTests: true,
    testableExtensions: ['.go'],
    runtime: 'go'
  },
  domain: {
    path: 'internal/core',
    testCommand: 'go test ./internal/core/...',
    lintCommand: 'go vet ./internal/core/...',
    buildCommand: 'go build ./cmd/server',
    hasTests: false,
    testableExtensions: ['.go'],
    runtime: 'go'
  },
  config: {
    path: 'internal/config',
    testCommand: 'go test ./internal/config/...',
    lintCommand: 'go vet ./internal/config/...',
    buildCommand: 'go build ./cmd/server',
    hasTests: true,
    testableExtensions: ['.go'],
    runtime: 'go'
  }
};

/**
 * Cross-package dependency map for Midas.
 * When a package is affected, its dependents should also be tested.
 */
const SERVICE_DEPENDENCIES = {
  domain: ['valuation', 'datacleaner', 'datafetcher', 'api', 'secGateway', 'marketGateway'],
  financeLib: ['valuation'],
  config: ['api', 'valuation', 'datacleaner', 'datafetcher'],
  datafetcher: ['valuation'],
  datacleaner: ['valuation'],
};

/**
 * Expand a list of affected services to include their dependents.
 * Prevents transitive build failures from going undetected.
 *
 * @param {string[]} services - Directly affected services
 * @returns {string[]} Expanded list including dependent services
 */
function expandWithDependents(services) {
  const expanded = new Set(services);
  for (const svc of services) {
    const deps = SERVICE_DEPENDENCIES[svc];
    if (deps) {
      for (const dep of deps) {
        if (SERVICES[dep]) expanded.add(dep);
      }
    }
  }
  return [...expanded];
}

// ──────────────────────────────────────────────
// Path classification
// ──────────────────────────────────────────────

const NON_TESTABLE_PATHS = [
  'docs/', '.cursor/', '.claude/', '.github/',
  '.vscode/', 'scripts/', 'monitoring/',
  'infrastructure/', 'terraform/', '.git/',
  'migrations/', 'data/', 'config/datacleaner/',
  'config/alerting/', 'performance/'
];

const NON_TESTABLE_EXTENSIONS = [
  '.md', '.json', '.yml', '.yaml', '.txt',
  '.env', '.gitignore', '.dockerignore',
  '.mdc', '.sql', '.sh', '.ps1', '.lock'
];

// ──────────────────────────────────────────────
// Sensitive file detection
// ──────────────────────────────────────────────

const SENSITIVE_PATTERNS = [
  /\.env$/,
  /\.env\..+$/,
  /credentials\.json$/i,
  /secrets\.json$/i,
  /secrets\.ya?ml$/i,
  /\.pem$/,
  /\.key$/,
  /password\.txt$/i,
  /passwords\.txt$/i,
  /password\.json$/i,
  /service-?account.*\.json$/i,
  /\.pfx$/,
  /\.p12$/,
  /id_rsa$/,
  /id_ed25519$/,
];

// ──────────────────────────────────────────────
// Security file patterns (for OWASP checks)
// ──────────────────────────────────────────────

const SECURITY_FILE_PATTERNS = [
  /auth/i, /security/i, /jwt/i, /token/i,
  /session/i, /password/i, /crypto/i,
  /encrypt/i, /decrypt/i, /guard/i,
  /middleware/i, /interceptor/i
];

// ──────────────────────────────────────────────
// Documentation triggers — Midas Go patterns
// ──────────────────────────────────────────────

const DOC_TRIGGER_PATTERNS = {
  contracts: [
    /handlers\/.*\.go$/,           // API handlers define request/response contracts
    /internal\/core\/ports\/.*\.go$/, // Port interfaces are service contracts
    /internal\/core\/entities\/.*\.go$/, // Domain entities are data contracts
    /openapi\.yaml$/,              // OpenAPI spec is the API contract
  ],
  architecture: [
    /internal\/di\/container\.go$/, // DI wiring defines architecture
    /internal\/api\/server\.go$/,  // Server setup defines routes/middleware
    /cmd\/.*\/main\.go$/,          // Entry points define architecture
    /Dockerfile/,                  // Container config is architecture
    /docker-compose.*\.yml$/,      // Deployment topology is architecture
  ]
};

// ──────────────────────────────────────────────
// Detection helpers
// ──────────────────────────────────────────────

function normalizePath(filePath) {
  return filePath.replace(/\\/g, '/');
}

function detectService(filePath) {
  const normalized = normalizePath(filePath);
  for (const [name, config] of Object.entries(SERVICES)) {
    if (normalized.includes(config.path)) {
      return name;
    }
  }
  return null;
}

function isTestable(filePath) {
  const normalized = normalizePath(filePath);
  for (const p of NON_TESTABLE_PATHS) {
    if (normalized.includes(p)) return false;
  }
  const ext = path.extname(filePath).toLowerCase();
  if (NON_TESTABLE_EXTENSIONS.includes(ext)) return false;

  const service = detectService(filePath);
  if (service && SERVICES[service].hasTests) {
    return SERVICES[service].testableExtensions.includes(ext);
  }
  return false;
}

function isSensitive(filePath) {
  const normalized = normalizePath(filePath);
  const fileName = path.basename(filePath);
  return SENSITIVE_PATTERNS.some(p => p.test(fileName) || p.test(normalized));
}

function isSecurityFile(filePath) {
  const normalized = normalizePath(filePath);
  const fileName = path.basename(filePath);
  return SECURITY_FILE_PATTERNS.some(p => p.test(fileName) || p.test(normalized));
}

function getDocUpdateNeeded(filePath) {
  const fileName = path.basename(filePath);
  const updates = [];
  if (DOC_TRIGGER_PATTERNS.contracts.some(p => p.test(fileName))) updates.push('contracts');
  if (DOC_TRIGGER_PATTERNS.architecture.some(p => p.test(fileName))) updates.push('architecture');
  return updates;
}

function getServiceConfig(serviceName) {
  return SERVICES[serviceName] || null;
}

// ──────────────────────────────────────────────
// Session tracking
// ──────────────────────────────────────────────

function loadSession() {
  try {
    if (fs.existsSync(SESSION_FILE)) {
      const raw = JSON.parse(fs.readFileSync(SESSION_FILE, 'utf8'));
      // Restore arrays that represent sets
      raw.affectedServices = raw.affectedServices || [];
      raw.docsToUpdate = raw.docsToUpdate || [];
      raw.editedFiles = raw.editedFiles || [];
      raw.securityFilesEdited = raw.securityFilesEdited || [];
      return raw;
    }
  } catch { /* return fresh session */ }

  return {
    startTime: new Date().toISOString(),
    editedFiles: [],
    affectedServices: [],
    hasTestableChanges: false,
    hasSecurityChanges: false,
    docsToUpdate: [],
    securityFilesEdited: []
  };
}

function saveSession(session) {
  // Deduplicate arrays before saving
  const toSave = {
    ...session,
    affectedServices: [...new Set(session.affectedServices)],
    docsToUpdate: [...new Set(session.docsToUpdate)]
  };
  fs.writeFileSync(SESSION_FILE, JSON.stringify(toSave, null, 2));
}

function trackEdit(filePath) {
  const session = loadSession();

  if (!session.editedFiles.includes(filePath)) {
    session.editedFiles.push(filePath);
  }

  const service = detectService(filePath);
  if (service && !session.affectedServices.includes(service)) {
    session.affectedServices.push(service);
  }

  if (isTestable(filePath)) {
    session.hasTestableChanges = true;
  }

  if (isSecurityFile(filePath)) {
    session.hasSecurityChanges = true;
    if (!session.securityFilesEdited.includes(filePath)) {
      session.securityFilesEdited.push(filePath);
    }
  }

  const docUpdates = getDocUpdateNeeded(filePath);
  for (const docType of docUpdates) {
    if (!session.docsToUpdate.includes(docType)) {
      session.docsToUpdate.push(docType);
    }
  }

  saveSession(session);
  return session;
}

function clearSession() {
  try {
    if (fs.existsSync(SESSION_FILE)) fs.unlinkSync(SESSION_FILE);
  } catch { /* ignore */ }
}

// ──────────────────────────────────────────────
// I/O helpers for Claude Code hooks
// ──────────────────────────────────────────────

/**
 * Read JSON from stdin (Claude Code sends hook context here).
 */
function readStdin() {
  return new Promise((resolve) => {
    let data = '';
    process.stdin.setEncoding('utf8');
    process.stdin.on('data', (chunk) => { data += chunk; });
    process.stdin.on('end', () => {
      try { resolve(JSON.parse(data)); }
      catch { resolve({}); }
    });
    process.stdin.on('error', () => resolve({}));
  });
}

/**
 * Exit 0 with JSON on stdout — success, action proceeds.
 * Claude Code parses: continue, stopReason, suppressOutput, systemMessage,
 * and hookSpecificOutput (for PreToolUse/PermissionRequest).
 */
function respondOk(json) {
  if (json && Object.keys(json).length > 0) {
    process.stdout.write(JSON.stringify(json));
  }
  process.exit(0);
}

/**
 * Exit 2 with message on stderr — blocking error.
 * Only effective for blocking-capable events (PreToolUse, UserPromptSubmit, Stop).
 * The stderr message is fed back to Claude as context.
 */
function respondBlock(message) {
  process.stderr.write(message);
  process.exit(2);
}

module.exports = {
  PROJECT_ROOT,
  SERVICES,
  SERVICE_DEPENDENCIES,
  NON_TESTABLE_PATHS,
  NON_TESTABLE_EXTENSIONS,
  SECURITY_FILE_PATTERNS,
  DOC_TRIGGER_PATTERNS,
  normalizeGitBashPath,
  normalizePath,
  detectService,
  isTestable,
  isSensitive,
  isSecurityFile,
  getDocUpdateNeeded,
  getServiceConfig,
  expandWithDependents,
  loadSession,
  saveSession,
  trackEdit,
  clearSession,
  readStdin,
  respondOk,
  respondBlock
};
