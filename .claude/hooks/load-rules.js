#!/usr/bin/env node
/**
 * UserPromptSubmit hook — load workflow rules into Claude Code context.
 *
 * Reads agents/rules/*.mdc files and writes them as plain text to stdout.
 * For UserPromptSubmit hooks, Claude Code adds stdout content directly
 * into Claude's context (not JSON — raw text).
 *
 * Canonical rule location per AGENTS.md Tier 3 loading contract.
 * Previously lived under .cursor/rules/ (Cursor-specific).
 *
 * Deduplication: skips loading when the same session already has
 * the same rule content loaded. Detects new sessions and file changes
 * via session_id + content hash.
 *
 * Exit codes:
 *   0 → success (stdout text added to context)
 *   other → non-blocking error
 */

const crypto = require('crypto');
const fs = require('fs');
const path = require('path');
const { PROJECT_ROOT, readStdin } = require('./utils');

// Rule files to load (relative to project root).
const RULE_FILES = [
  'agents/rules/_shared-workflow.md',   // Foundation: roles, validation cycle, response format
  'agents/rules/preflight.md',          // Pre-implementation checklist
  'agents/rules/orchestrator.md',       // Routing logic and specialist dispatch
];

// State file tracking what was loaded and for which session
const STATE_FILE = path.join(__dirname, '.rules-loaded');

/**
 * Strip YAML frontmatter (--- ... ---) from .mdc file content
 */
function stripFrontmatter(content) {
  const match = content.match(/^---\s*\n[\s\S]*?\n---\s*\n?/);
  return match ? content.slice(match[0].length).trim() : content.trim();
}

/**
 * Compute a fast hash of the combined rule file contents.
 * Changes when any rule file is edited, added, or removed.
 */
function computeContentHash(sections) {
  return crypto
    .createHash('md5')
    .update(sections.map(s => s.content).join('\n'))
    .digest('hex');
}

/**
 * Load previous state to check if rules are already in context.
 */
function loadState() {
  try {
    if (fs.existsSync(STATE_FILE)) {
      return JSON.parse(fs.readFileSync(STATE_FILE, 'utf8'));
    }
  } catch { /* corrupted — treat as missing */ }
  return null;
}

/**
 * Save state after loading rules.
 */
function saveState(sessionId, contentHash) {
  try {
    fs.writeFileSync(STATE_FILE, JSON.stringify({
      sessionId,
      contentHash,
      loadedAt: new Date().toISOString()
    }));
  } catch { /* best effort */ }
}

async function main() {
  try {
    const input = await readStdin();

    // Extract session ID from hook input (Claude Code provides this)
    const sessionId = input.session_id || null;

    // Read and parse all rule files
    const sections = [];
    for (const relPath of RULE_FILES) {
      const fullPath = path.join(PROJECT_ROOT, relPath);
      try {
        if (fs.existsSync(fullPath)) {
          const raw = fs.readFileSync(fullPath, 'utf8');
          const content = stripFrontmatter(raw);
          if (content) {
            const name = path.basename(relPath, '.mdc');
            sections.push({ name, content });
          }
        }
      } catch {
        // Skip unreadable files
      }
    }

    if (sections.length === 0) {
      process.exit(0);
    }

    //const contentHash = computeContentHash(sections);
    // const prevState = loadState();

    // // Skip if same session + same content (already in context).
    // // Also enforce a TTL: if the state is older than 1 hour, always reload.
    // // This handles cases where the Stop hook didn't run (crash, Ctrl+C)
    // // and the stale state would otherwise suppress the first-prompt load.
    // const MAX_STATE_AGE_MS = 60 * 60 * 1000; // 1 hour
    // const stateAge = prevState && prevState.loadedAt
    //   ? Date.now() - new Date(prevState.loadedAt).getTime()
    //   : Infinity;

    // if (
    //   prevState &&
    //   prevState.contentHash === contentHash &&
    //   sessionId &&
    //   prevState.sessionId === sessionId &&
    //   stateAge < MAX_STATE_AGE_MS
    // ) {
    //   process.exit(0);
    // }

    // New session, different content, or no previous state → load rules
    // saveState(sessionId, contentHash);

    const body = sections
      .map(s => `## Rule: ${s.name}\n\n${s.content}`)
      .join('\n\n---\n\n');

    const message =
      '# Loaded Workflow Rules (agents/rules/)\n\n' +
      'The following project workflow rules have been loaded into context. ' +
      'Follow these rules alongside CLAUDE.md and AGENTS.md instructions.\n\n' +
      body;

    // Write plain text to stdout — Claude Code injects stdout content
    // directly into Claude's context for UserPromptSubmit hooks.
    // Use write callback to ensure stdout flushes before exit.
    process.stdout.write(message, () => process.exit(0));

  } catch (error) {
    process.stderr.write(`load-rules hook error (non-blocking): ${error.message}\n`);
    process.exit(1);
  }
}

main();
