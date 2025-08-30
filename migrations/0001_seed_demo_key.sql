-- Seed a demo API key using SQL only. Choose a deterministic raw key and store only its SHA-256 hash.
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
    '["read:fair_value"]',
    1000,
    1,
    CURRENT_TIMESTAMP,
    CURRENT_TIMESTAMP
);


