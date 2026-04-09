-- Seed a demo API key for local development with full permissions (admin:all).
-- SECURITY NOTE: This key is for local/staging use only. It has admin permissions
-- intentionally to allow full API exploration during development. Do NOT deploy
-- this migration to production without restricting permissions.
-- The raw key to use locally is:
-- DEMO RAW KEY: dcf_demo_3a4a5b6c7d8e9f00112233445566778899aabbccddeeff001122334455667788
-- Its SHA-256 (computed via cmd/hash-key) is:
-- 07b7dc84a8e720803fe20679742b813baecde27256f57d9bb062069193503802

INSERT OR IGNORE INTO api_keys (
    id, key_hash, user_id, permissions, rate_limit, is_active, created_at, updated_at
) VALUES (
    'demo_key_0001',
    '07b7dc84a8e720803fe20679742b813baecde27256f57d9bb062069193503802',
    'demo-user',
    '["read:fair_value","read:health","read:metrics","manage:keys","admin:all"]',
    1000,
    1,
    CURRENT_TIMESTAMP,
    CURRENT_TIMESTAMP
);

-- Ensure existing demo key gets full permissions on upgrade (BUG-011 fix)
UPDATE api_keys
SET permissions = '["read:fair_value","read:health","read:metrics","manage:keys","admin:all"]',
    updated_at = CURRENT_TIMESTAMP
WHERE id = 'demo_key_0001';
