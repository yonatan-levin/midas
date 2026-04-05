#!/usr/bin/env node
/**
 * PreToolUse hook for Read tool — sensitive file guard.
 *
 * Blocks Claude from reading files that may contain secrets
 * (.env, credentials, private keys, etc.).
 *
 * Claude Code PreToolUse output format:
 *   hookSpecificOutput.permissionDecision: "allow" | "deny" | "ask"
 *   hookSpecificOutput.permissionDecisionReason: string
 *
 * Exit codes:
 *   0 → action proceeds (stdout parsed for JSON)
 *   2 → action blocked  (stderr fed back to Claude)
 */

const { readStdin, isSensitive, respondOk } = require('./utils');

async function main() {
  try {
    const input = await readStdin();
    const filePath = (input.tool_input && input.tool_input.file_path) || '';

    if (!filePath) {
      respondOk({});
      return;
    }

    if (isSensitive(filePath)) {
      respondOk({
        hookSpecificOutput: {
          hookEventName: 'PreToolUse',
          permissionDecision: 'deny',
          permissionDecisionReason:
            `Blocked: "${filePath}" matches a sensitive file pattern. ` +
            'Use environment variables or vault references instead of reading secrets directly.'
        }
      });
      return;
    }

    // Allow — exit 0 with no output is fine, but explicit allow is clearer
    respondOk({});

  } catch (error) {
    // Fail-open: allow reads on hook errors so we don't block legitimate work.
    // The sensitivity check is a best-effort guardrail.
    process.stderr.write(`pre-read hook error (non-blocking): ${error.message}\n`);
    process.exit(1); // non-zero but not 2 → non-blocking, action proceeds
  }
}

main();
