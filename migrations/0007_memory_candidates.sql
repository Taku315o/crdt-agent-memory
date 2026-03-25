CREATE TABLE IF NOT EXISTS memory_candidates (
    candidate_id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    candidate_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    subject TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL,
    source_uri TEXT NOT NULL DEFAULT '',
    authored_at_ms INTEGER NOT NULL,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    author_agent_id TEXT NOT NULL DEFAULT '',
    origin_peer_id TEXT NOT NULL DEFAULT '',
    sensitivity TEXT NOT NULL DEFAULT 'private',
    retention_class TEXT NOT NULL DEFAULT 'default',
    project_key TEXT NOT NULL DEFAULT '',
    branch_name TEXT NOT NULL DEFAULT '',
    approved_memory_id TEXT NOT NULL DEFAULT '',
    reviewed_at_ms INTEGER NOT NULL DEFAULT 0,
    review_note TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_memory_candidates_namespace_status
    ON memory_candidates(namespace, status, authored_at_ms DESC);

CREATE INDEX IF NOT EXISTS idx_memory_candidates_project_status
    ON memory_candidates(project_key, branch_name, status, authored_at_ms DESC);

CREATE TABLE IF NOT EXISTS memory_candidate_chunks (
    link_id TEXT PRIMARY KEY,
    candidate_id TEXT NOT NULL,
    chunk_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL DEFAULT 0,
    UNIQUE(candidate_id, chunk_id)
);

CREATE INDEX IF NOT EXISTS idx_memory_candidate_chunks_candidate
    ON memory_candidate_chunks(candidate_id, ordinal, chunk_id);
