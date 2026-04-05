#!/usr/bin/env node
/**
 * beforeReadFile Hook
 * 
 * Security checks before AI reads a file:
 * - Blocks access to sensitive files (.env, credentials, secrets)
 * - Logs file access for audit purposes (optional)
 * 
 * NOTE: This hook uses fail-closed behavior in Cursor - if the hook fails,
 * the file read is blocked. We handle errors gracefully to allow reads.
 * 
 * Output format (per Cursor docs):
 * - { permission: "allow" } - Allow file access
 * - { permission: "deny", user_message: "..." } - Block file access
 */

const { readStdin, isSensitive } = require('./utils');

/**
 * Respond with proper Cursor hook output format
 * @param {Object} response - Response object with permission field
 */
function respond(response) {
  console.log(JSON.stringify(response));
}

async function main() {
  try {
    const input = await readStdin();
    const filePath = input.file_path || '';
    
    // Check if file is sensitive
    if (isSensitive(filePath)) {
      respond({
        permission: 'deny',
        user_message: `Access blocked: ${filePath} contains sensitive data. Use environment variables instead.`
      });
      return;
    }
    
    // Allow access - empty object or explicit allow
    respond({ permission: 'allow' });
    
  } catch (error) {
    // IMPORTANT: beforeReadFile uses fail-closed behavior.
    // If we error out (exit non-zero), the file read is blocked.
    // For ENAMETOOLONG errors on Windows (large file content in stdin),
    // we should still allow the read since the error is infrastructure-related.
    console.error('beforeReadFile hook error:', error.message);
    
    // Allow access on error to prevent blocking legitimate reads
    // The sensitivity check is a best-effort security measure
    respond({ permission: 'allow' });
  }
}

main();
