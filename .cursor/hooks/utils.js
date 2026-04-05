/**
 * Shared utilities for Cursor hooks — Midas DCF Valuation API (Go)
 * Provides context-aware detection of packages and testability
 */

const fs = require('fs');
const path = require('path');

// Session tracking file
const SESSION_FILE = path.join(__dirname, '.session-edits.json');

/**
 * Package configuration — Midas Go monolith
 */
const SERVICES = {
  api: {
    path: 'internal/api',
    testCommand: 'go test ./internal/api/...',
    lintCommand: 'go vet ./internal/api/...',
    typeCheckCommand: 'go build ./cmd/server',
    hasTests: true,
    testableExtensions: ['.go']
  },
  valuation: {
    path: 'internal/services/valuation',
    testCommand: 'go test ./internal/services/valuation/...',
    lintCommand: 'go vet ./internal/services/valuation/...',
    typeCheckCommand: 'go build ./cmd/server',
    hasTests: true,
    testableExtensions: ['.go']
  },
  datacleaner: {
    path: 'internal/services/datacleaner',
    testCommand: 'go test ./internal/services/datacleaner/...',
    lintCommand: 'go vet ./internal/services/datacleaner/...',
    typeCheckCommand: 'go build ./cmd/server',
    hasTests: true,
    testableExtensions: ['.go']
  },
  datafetcher: {
    path: 'internal/services/datafetcher',
    testCommand: 'go test ./internal/services/datafetcher/...',
    lintCommand: 'go vet ./internal/services/datafetcher/...',
    typeCheckCommand: 'go build ./cmd/server',
    hasTests: true,
    testableExtensions: ['.go']
  },
  secGateway: {
    path: 'internal/infra/gateways/sec',
    testCommand: 'go test ./internal/infra/gateways/sec/...',
    lintCommand: 'go vet ./internal/infra/gateways/sec/...',
    typeCheckCommand: 'go build ./cmd/server',
    hasTests: true,
    testableExtensions: ['.go']
  },
  marketGateway: {
    path: 'internal/infra/gateways/market',
    testCommand: 'go test ./internal/infra/gateways/market/...',
    lintCommand: 'go vet ./internal/infra/gateways/market/...',
    typeCheckCommand: 'go build ./cmd/server',
    hasTests: true,
    testableExtensions: ['.go']
  },
  financeLib: {
    path: 'pkg/finance',
    testCommand: 'go test ./pkg/finance/...',
    lintCommand: 'go vet ./pkg/finance/...',
    typeCheckCommand: 'go build ./pkg/finance/...',
    hasTests: true,
    testableExtensions: ['.go']
  },
  integration: {
    path: 'internal/integration',
    testCommand: 'go test ./internal/integration/...',
    lintCommand: 'go vet ./internal/integration/...',
    typeCheckCommand: 'go build ./cmd/server',
    hasTests: true,
    testableExtensions: ['.go']
  },
  config: {
    path: 'internal/config',
    testCommand: 'go test ./internal/config/...',
    lintCommand: 'go vet ./internal/config/...',
    typeCheckCommand: 'go build ./cmd/server',
    hasTests: true,
    testableExtensions: ['.go']
  }
};

/**
 * Folders that don't have tests and should be skipped
 */
const NON_TESTABLE_PATHS = [
  'docs/',
  '.cursor/',
  '.claude/',
  '.github/',
  '.vscode/',
  'scripts/',
  'monitoring/',
  'infrastructure/',
  'terraform/',
  '.git/',
  'migrations/',
  'data/',
  'config/datacleaner/',
  'config/alerting/',
  'performance/'
];

/**
 * File extensions that don't need testing
 */
const NON_TESTABLE_EXTENSIONS = [
  '.md',
  '.json',
  '.yml',
  '.yaml',
  '.txt',
  '.env',
  '.gitignore',
  '.dockerignore',
  '.mdc',
  '.sql',
  '.sh',
  '.ps1',
  '.lock'
];

/**
 * Sensitive files that should be blocked from reading
 * NOTE: Be careful with patterns - they should match actual secret files,
 * not code files that handle sensitive features (like password reset logic)
 */
const SENSITIVE_PATTERNS = [
  /\.env$/,
  /\.env\./,
  /credentials\.json$/i,
  /secrets\.json$/i,
  /secrets\.ya?ml$/i,
  /\.pem$/,
  /\.key$/,
  // Only block files that are likely to contain actual passwords, not code that handles passwords
  /password\.txt$/i,
  /passwords\.txt$/i,
  /password\.json$/i,
];

/**
 * Security-sensitive file patterns (for OWASP checks)
 */
const SECURITY_FILE_PATTERNS = [
  /auth/i,
  /security/i,
  /jwt/i,
  /token/i,
  /session/i,
  /password/i,
  /crypto/i,
  /encrypt/i,
  /decrypt/i,
  /guard/i,
  /middleware/i,
  /interceptor/i
];

/**
 * Documentation-related file patterns — Midas Go patterns
 */
const DOC_TRIGGER_PATTERNS = {
  // Files that should trigger CONTRACTS.md update
  contracts: [
    /handlers\/.*\.go$/,
    /internal\/core\/ports\/.*\.go$/,
    /internal\/core\/entities\/.*\.go$/,
    /openapi\.yaml$/,
  ],
  // Files that should trigger ARCHITECTURE.md update
  architecture: [
    /internal\/di\/container\.go$/,
    /internal\/api\/server\.go$/,
    /cmd\/.*\/main\.go$/,
    /Dockerfile/,
    /docker-compose.*\.yml$/,
  ]
};

/**
 * Detect which service a file belongs to
 * @param {string} filePath - Absolute or relative file path
 * @returns {string|null} - Service name or null
 */
function detectService(filePath) {
  // Normalize path separators
  const normalizedPath = filePath.replace(/\\/g, '/');
  
  for (const [name, config] of Object.entries(SERVICES)) {
    if (normalizedPath.includes(config.path)) {
      return name;
    }
  }
  
  return null;
}

/**
 * Check if a file is testable (has tests associated)
 * @param {string} filePath - File path to check
 * @returns {boolean}
 */
function isTestable(filePath) {
  const normalizedPath = filePath.replace(/\\/g, '/');
  
  // Check if in non-testable path
  for (const nonTestable of NON_TESTABLE_PATHS) {
    if (normalizedPath.includes(nonTestable)) {
      return false;
    }
  }
  
  // Check extension
  const ext = path.extname(filePath).toLowerCase();
  if (NON_TESTABLE_EXTENSIONS.includes(ext)) {
    return false;
  }
  
  // Check if it belongs to a service with tests
  const service = detectService(filePath);
  if (service && SERVICES[service].hasTests) {
    const serviceConfig = SERVICES[service];
    return serviceConfig.testableExtensions.includes(ext);
  }
  
  return false;
}

/**
 * Check if a file is sensitive and should be blocked
 * @param {string} filePath - File path to check
 * @returns {boolean}
 */
function isSensitive(filePath) {
  const normalizedPath = filePath.replace(/\\/g, '/');
  const fileName = path.basename(filePath);
  
  for (const pattern of SENSITIVE_PATTERNS) {
    if (pattern.test(fileName) || pattern.test(normalizedPath)) {
      return true;
    }
  }
  
  return false;
}

/**
 * Check if a file is security-related (for OWASP checks)
 * @param {string} filePath - File path to check
 * @returns {boolean}
 */
function isSecurityFile(filePath) {
  const normalizedPath = filePath.replace(/\\/g, '/');
  const fileName = path.basename(filePath);
  
  for (const pattern of SECURITY_FILE_PATTERNS) {
    if (pattern.test(fileName) || pattern.test(normalizedPath)) {
      return true;
    }
  }
  
  return false;
}

/**
 * Check what documentation should be updated based on file
 * @param {string} filePath - File path to check
 * @returns {string[]} - Array of doc types to update: ['contracts', 'architecture']
 */
function getDocUpdateNeeded(filePath) {
  const fileName = path.basename(filePath);
  const updates = [];
  
  for (const pattern of DOC_TRIGGER_PATTERNS.contracts) {
    if (pattern.test(fileName)) {
      updates.push('contracts');
      break;
    }
  }
  
  for (const pattern of DOC_TRIGGER_PATTERNS.architecture) {
    if (pattern.test(fileName)) {
      updates.push('architecture');
      break;
    }
  }
  
  return updates;
}

/**
 * Load current session edits
 * @returns {Object} - Session data with edited files
 */
function loadSession() {
  try {
    if (fs.existsSync(SESSION_FILE)) {
      return JSON.parse(fs.readFileSync(SESSION_FILE, 'utf8'));
    }
  } catch (e) {
    // Ignore errors, return fresh session
  }
  
  return {
    startTime: new Date().toISOString(),
    editedFiles: [],
    affectedServices: new Set(),
    hasTestableChanges: false,
    hasSecurityChanges: false,
    docsToUpdate: new Set(),  // 'contracts', 'architecture'
    securityFilesEdited: []
  };
}

/**
 * Save session data
 * @param {Object} session - Session data to save
 */
function saveSession(session) {
  // Convert Sets to Arrays for JSON serialization
  const toSave = {
    ...session,
    affectedServices: Array.from(session.affectedServices || []),
    docsToUpdate: Array.from(session.docsToUpdate || [])
  };
  
  fs.writeFileSync(SESSION_FILE, JSON.stringify(toSave, null, 2));
}

/**
 * Add an edited file to the session
 * @param {string} filePath - File that was edited
 */
function trackEdit(filePath) {
  const session = loadSession();
  
  // Ensure Sets are initialized
  session.affectedServices = new Set(session.affectedServices || []);
  session.docsToUpdate = new Set(session.docsToUpdate || []);
  session.securityFilesEdited = session.securityFilesEdited || [];
  
  if (!session.editedFiles.includes(filePath)) {
    session.editedFiles.push(filePath);
  }
  
  const service = detectService(filePath);
  if (service) {
    session.affectedServices.add(service);
  }
  
  if (isTestable(filePath)) {
    session.hasTestableChanges = true;
  }
  
  // Track security file changes
  if (isSecurityFile(filePath)) {
    session.hasSecurityChanges = true;
    if (!session.securityFilesEdited.includes(filePath)) {
      session.securityFilesEdited.push(filePath);
    }
  }
  
  // Track documentation updates needed
  const docUpdates = getDocUpdateNeeded(filePath);
  for (const docType of docUpdates) {
    session.docsToUpdate.add(docType);
  }
  
  saveSession(session);
  
  return session;
}

/**
 * Clear the session (after stop hook completes)
 */
function clearSession() {
  try {
    if (fs.existsSync(SESSION_FILE)) {
      fs.unlinkSync(SESSION_FILE);
    }
  } catch (e) {
    // Ignore errors
  }
}

/**
 * Get service configuration
 * @param {string} serviceName - Name of the service
 * @returns {Object|null}
 */
function getServiceConfig(serviceName) {
  return SERVICES[serviceName] || null;
}

/**
 * Read JSON from stdin
 * @returns {Promise<Object>}
 */
function readStdin() {
  return new Promise((resolve, reject) => {
    let data = '';
    
    process.stdin.setEncoding('utf8');
    process.stdin.on('data', chunk => {
      data += chunk;
    });
    
    process.stdin.on('end', () => {
      try {
        resolve(JSON.parse(data));
      } catch (e) {
        resolve({});
      }
    });
    
    process.stdin.on('error', reject);
  });
}

/**
 * Output JSON response
 * @param {Object} response - Response object
 */
function respond(response) {
  console.log(JSON.stringify(response));
}

module.exports = {
  SERVICES,
  NON_TESTABLE_PATHS,
  NON_TESTABLE_EXTENSIONS,
  SECURITY_FILE_PATTERNS,
  DOC_TRIGGER_PATTERNS,
  detectService,
  isTestable,
  isSensitive,
  isSecurityFile,
  getDocUpdateNeeded,
  loadSession,
  saveSession,
  trackEdit,
  clearSession,
  getServiceConfig,
  readStdin,
  respond
};
