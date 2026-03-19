ALTER TABLE peer_policies ADD COLUMN signing_public_key TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS memory_verification_state (
    memory_space TEXT NOT NULL,
    memory_id TEXT NOT NULL,
    signature_status TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    checked_at_ms INTEGER NOT NULL,
    PRIMARY KEY (memory_space, memory_id)
);

CREATE INDEX IF NOT EXISTS idx_memory_verification_state_status
    ON memory_verification_state(signature_status, checked_at_ms);
