CREATE TABLE IF NOT EXISTS transcript_sessions (
    session_id TEXT PRIMARY KEY,
    source_kind TEXT NOT NULL,
    namespace TEXT NOT NULL,
    repo_path TEXT NOT NULL DEFAULT '',
    repo_root_hash TEXT NOT NULL DEFAULT '',
    branch_name TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    agent_name TEXT NOT NULL DEFAULT '',
    user_identity TEXT NOT NULL DEFAULT '',
    started_at_ms INTEGER NOT NULL,
    ended_at_ms INTEGER NOT NULL,
    ingest_version INTEGER NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    sensitivity TEXT NOT NULL DEFAULT 'private',
    retention_class TEXT NOT NULL DEFAULT 'default',
    created_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS transcript_messages (
    message_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    seq INTEGER NOT NULL,
    role TEXT NOT NULL,
    tool_name TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    authored_at_ms INTEGER NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    UNIQUE(session_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_transcript_messages_session_seq
    ON transcript_messages(session_id, seq);

CREATE INDEX IF NOT EXISTS idx_transcript_messages_session_hash
    ON transcript_messages(session_id, content_hash);

CREATE TABLE IF NOT EXISTS transcript_chunks (
    chunk_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    chunk_strategy_version INTEGER NOT NULL,
    chunk_seq INTEGER NOT NULL,
    chunk_kind TEXT NOT NULL,
    start_seq INTEGER NOT NULL,
    end_seq INTEGER NOT NULL,
    text TEXT NOT NULL,
    normalized_text TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    authored_at_ms INTEGER NOT NULL,
    source_uri TEXT NOT NULL DEFAULT '',
    sensitivity TEXT NOT NULL DEFAULT 'private',
    retention_class TEXT NOT NULL DEFAULT 'default',
    is_indexable INTEGER NOT NULL DEFAULT 1,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    UNIQUE(session_id, chunk_strategy_version, chunk_seq)
);

CREATE INDEX IF NOT EXISTS idx_transcript_chunks_session_authored
    ON transcript_chunks(session_id, authored_at_ms);

CREATE INDEX IF NOT EXISTS idx_transcript_chunks_kind_authored
    ON transcript_chunks(chunk_kind, authored_at_ms);

CREATE TABLE IF NOT EXISTS transcript_artifact_spans (
    span_id TEXT PRIMARY KEY,
    chunk_id TEXT NOT NULL,
    artifact_id TEXT NOT NULL,
    start_offset INTEGER NOT NULL DEFAULT 0,
    end_offset INTEGER NOT NULL DEFAULT 0,
    start_line INTEGER NOT NULL DEFAULT 0,
    end_line INTEGER NOT NULL DEFAULT 0,
    quote_hash TEXT NOT NULL DEFAULT '',
    authored_at_ms INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_transcript_artifact_spans_chunk
    ON transcript_artifact_spans(chunk_id);

CREATE TABLE IF NOT EXISTS transcript_promotions (
    promotion_id TEXT PRIMARY KEY,
    chunk_id TEXT NOT NULL,
    memory_id TEXT NOT NULL,
    created_at_ms INTEGER NOT NULL,
    UNIQUE(chunk_id, memory_id)
);

CREATE TABLE IF NOT EXISTS memory_publications (
    publication_id TEXT PRIMARY KEY,
    private_memory_id TEXT NOT NULL,
    shared_memory_id TEXT NOT NULL,
    published_at_ms INTEGER NOT NULL,
    UNIQUE(private_memory_id, shared_memory_id)
);

CREATE TABLE IF NOT EXISTS retrieval_units (
    unit_id TEXT PRIMARY KEY,
    source_type TEXT NOT NULL,
    source_id TEXT NOT NULL,
    memory_space TEXT NOT NULL,
    namespace TEXT NOT NULL,
    unit_kind TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL,
    body_hash TEXT NOT NULL,
    authored_at_ms INTEGER NOT NULL,
    sensitivity TEXT NOT NULL DEFAULT 'private',
    retention_class TEXT NOT NULL DEFAULT 'default',
    state TEXT NOT NULL DEFAULT 'active',
    source_uri TEXT NOT NULL DEFAULT '',
    project_key TEXT NOT NULL DEFAULT '',
    branch_name TEXT NOT NULL DEFAULT '',
    schema_version INTEGER NOT NULL DEFAULT 1,
    UNIQUE(source_type, source_id)
);

CREATE INDEX IF NOT EXISTS idx_retrieval_units_space_namespace
    ON retrieval_units(memory_space, namespace, authored_at_ms DESC);

CREATE INDEX IF NOT EXISTS idx_retrieval_units_project
    ON retrieval_units(project_key, branch_name, authored_at_ms DESC);

CREATE TABLE IF NOT EXISTS retrieval_index_queue (
    queue_id TEXT PRIMARY KEY,
    unit_id TEXT NOT NULL,
    enqueued_at_ms INTEGER NOT NULL,
    processed_at_ms INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS retrieval_embeddings (
    unit_id TEXT PRIMARY KEY,
    memory_space TEXT NOT NULL,
    embedding_json TEXT NOT NULL,
    embedding_dim INTEGER NOT NULL,
    indexed_at_ms INTEGER NOT NULL
);

CREATE VIEW IF NOT EXISTS retrieval_units_view AS
SELECT
    ru.unit_id,
    ru.source_type,
    ru.source_id,
    ru.memory_space,
    ru.namespace,
    ru.unit_kind,
    ru.title,
    ru.body,
    ru.authored_at_ms,
    ru.state,
    ru.source_uri,
    CASE
        WHEN ru.source_type IN ('shared_memory', 'private_memory') THEN ru.source_id
        ELSE ''
    END AS source_hash,
    CASE
        WHEN ru.memory_space = 'shared' THEN COALESCE(mn.origin_peer_id, '')
        WHEN ru.memory_space = 'private' THEN COALESCE(pmn.origin_peer_id, '')
        ELSE ''
    END AS origin_peer_id
FROM retrieval_units ru
LEFT JOIN memory_nodes mn
    ON ru.source_type = 'shared_memory'
   AND mn.memory_id = ru.source_id
LEFT JOIN private_memory_nodes pmn
    ON ru.source_type = 'private_memory'
   AND pmn.memory_id = ru.source_id;

CREATE TABLE IF NOT EXISTS retrieval_fts (
    unit_id TEXT PRIMARY KEY,
    memory_space TEXT NOT NULL,
    namespace TEXT NOT NULL,
    source_type TEXT NOT NULL,
    unit_kind TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL
);

CREATE TRIGGER IF NOT EXISTS trg_retrieval_units_fts_insert
AFTER INSERT ON retrieval_units
BEGIN
    INSERT INTO retrieval_fts(unit_id, memory_space, namespace, source_type, unit_kind, title, body)
    VALUES(
        NEW.unit_id,
        NEW.memory_space,
        NEW.namespace,
        NEW.source_type,
        NEW.unit_kind,
        NEW.title,
        CASE
            WHEN NEW.state != 'active' OR NEW.sensitivity = 'secret' THEN ''
            ELSE NEW.body
        END
    );
END;

CREATE TRIGGER IF NOT EXISTS trg_retrieval_units_fts_update
AFTER UPDATE ON retrieval_units
BEGIN
    DELETE FROM retrieval_fts WHERE unit_id = NEW.unit_id;
    INSERT INTO retrieval_fts(unit_id, memory_space, namespace, source_type, unit_kind, title, body)
    VALUES(
        NEW.unit_id,
        NEW.memory_space,
        NEW.namespace,
        NEW.source_type,
        NEW.unit_kind,
        NEW.title,
        CASE
            WHEN NEW.state != 'active' OR NEW.sensitivity = 'secret' THEN ''
            ELSE NEW.body
        END
    );
END;

CREATE TRIGGER IF NOT EXISTS trg_retrieval_units_fts_delete
AFTER DELETE ON retrieval_units
BEGIN
    DELETE FROM retrieval_fts WHERE unit_id = OLD.unit_id;
END;
