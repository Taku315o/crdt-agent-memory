CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS app_metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS crsql_clock (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    version INTEGER NOT NULL
);

INSERT OR IGNORE INTO crsql_clock(singleton, version) VALUES (1, 0);

CREATE TABLE IF NOT EXISTS capture_control (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    suppress INTEGER NOT NULL DEFAULT 0
);

INSERT OR IGNORE INTO capture_control(singleton, suppress) VALUES (1, 0);

CREATE TABLE IF NOT EXISTS crsql_tracked_peers (
    peer_id TEXT NOT NULL,
    namespace TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 0,
    updated_at_ms INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (peer_id, namespace)
);

CREATE TABLE IF NOT EXISTS peer_policies (
    peer_id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL DEFAULT '',
    trust_state TEXT NOT NULL DEFAULT 'allow',
    trust_weight REAL NOT NULL DEFAULT 1.0,
    discovery_profile TEXT NOT NULL DEFAULT '',
    relay_profile TEXT NOT NULL DEFAULT '',
    notes TEXT NOT NULL DEFAULT '',
    updated_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS peer_sync_state (
    peer_id TEXT NOT NULL,
    namespace TEXT NOT NULL,
    last_seen_at_ms INTEGER NOT NULL DEFAULT 0,
    last_transport TEXT NOT NULL DEFAULT '',
    last_path_type TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    last_success_at_ms INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (peer_id, namespace)
);

CREATE TABLE IF NOT EXISTS sync_quarantine (
    batch_id TEXT PRIMARY KEY,
    peer_id TEXT NOT NULL,
    namespace TEXT NOT NULL,
    reason TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    created_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS index_queue (
    queue_id TEXT PRIMARY KEY,
    memory_space TEXT NOT NULL,
    memory_id TEXT NOT NULL,
    enqueued_at_ms INTEGER NOT NULL,
    processed_at_ms INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS memory_nodes (
    memory_id TEXT PRIMARY KEY,
    memory_type TEXT NOT NULL,
    namespace TEXT NOT NULL,
    scope TEXT NOT NULL,
    subject TEXT NOT NULL,
    body TEXT NOT NULL,
    source_uri TEXT NOT NULL DEFAULT '',
    source_hash TEXT NOT NULL DEFAULT '',
    author_agent_id TEXT NOT NULL,
    origin_peer_id TEXT NOT NULL,
    authored_at_ms INTEGER NOT NULL,
    valid_from_ms INTEGER NOT NULL DEFAULT 0,
    valid_to_ms INTEGER NOT NULL DEFAULT 0,
    lifecycle_state TEXT NOT NULL,
    schema_version INTEGER NOT NULL,
    author_signature BLOB NOT NULL DEFAULT X''
);

CREATE TABLE IF NOT EXISTS memory_edges (
    edge_id TEXT PRIMARY KEY,
    from_memory_id TEXT NOT NULL,
    to_memory_id TEXT NOT NULL,
    relation_type TEXT NOT NULL,
    weight REAL NOT NULL DEFAULT 1.0,
    origin_peer_id TEXT NOT NULL,
    authored_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS memory_signals (
    signal_id TEXT PRIMARY KEY,
    memory_id TEXT NOT NULL,
    peer_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    signal_type TEXT NOT NULL,
    value REAL NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    authored_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS artifact_refs (
    artifact_id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    uri TEXT NOT NULL,
    content_hash TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    mime_type TEXT NOT NULL DEFAULT '',
    origin_peer_id TEXT NOT NULL,
    authored_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS private_memory_nodes (
    memory_id TEXT PRIMARY KEY,
    local_namespace TEXT NOT NULL,
    memory_type TEXT NOT NULL,
    subject TEXT NOT NULL,
    body TEXT NOT NULL,
    source_uri TEXT NOT NULL DEFAULT '',
    source_hash TEXT NOT NULL DEFAULT '',
    author_agent_id TEXT NOT NULL,
    origin_peer_id TEXT NOT NULL,
    authored_at_ms INTEGER NOT NULL,
    valid_from_ms INTEGER NOT NULL DEFAULT 0,
    valid_to_ms INTEGER NOT NULL DEFAULT 0,
    lifecycle_state TEXT NOT NULL,
    schema_version INTEGER NOT NULL,
    author_signature BLOB NOT NULL DEFAULT X''
);

CREATE TABLE IF NOT EXISTS private_memory_edges (
    edge_id TEXT PRIMARY KEY,
    from_memory_id TEXT NOT NULL,
    to_memory_id TEXT NOT NULL,
    relation_type TEXT NOT NULL,
    weight REAL NOT NULL DEFAULT 1.0,
    authored_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS private_memory_signals (
    signal_id TEXT PRIMARY KEY,
    memory_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    signal_type TEXT NOT NULL,
    value REAL NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    authored_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS private_artifact_refs (
    artifact_id TEXT PRIMARY KEY,
    local_namespace TEXT NOT NULL,
    uri TEXT NOT NULL,
    content_hash TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    mime_type TEXT NOT NULL DEFAULT '',
    authored_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS crsql_changes (
    site_id TEXT NOT NULL,
    db_version INTEGER NOT NULL,
    seq INTEGER NOT NULL,
    table_name TEXT NOT NULL,
    pk TEXT NOT NULL,
    op TEXT NOT NULL,
    row_json TEXT NOT NULL,
    memory_id TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    changed_at_ms INTEGER NOT NULL,
    PRIMARY KEY (site_id, db_version, seq)
);

CREATE INDEX IF NOT EXISTS idx_crsql_changes_namespace_version
    ON crsql_changes(namespace, db_version);

CREATE VIEW IF NOT EXISTS recall_memory_view AS
SELECT
    'shared' AS memory_space,
    memory_id,
    namespace AS namespace,
    memory_type,
    subject,
    body,
    lifecycle_state,
    authored_at_ms,
    source_uri,
    source_hash,
    origin_peer_id
FROM memory_nodes
UNION ALL
SELECT
    'private' AS memory_space,
    memory_id,
    local_namespace AS namespace,
    memory_type,
    subject,
    body,
    lifecycle_state,
    authored_at_ms,
    source_uri,
    source_hash,
    origin_peer_id
FROM private_memory_nodes;

CREATE TRIGGER IF NOT EXISTS trg_memory_nodes_capture_insert
AFTER INSERT ON memory_nodes
WHEN (SELECT suppress FROM capture_control WHERE singleton = 1) = 0
BEGIN
    UPDATE crsql_clock SET version = version + 1 WHERE singleton = 1;
    INSERT INTO crsql_changes(site_id, db_version, seq, table_name, pk, op, row_json, memory_id, namespace, changed_at_ms)
    VALUES(
        NEW.origin_peer_id,
        (SELECT version FROM crsql_clock WHERE singleton = 1),
        0,
        'memory_nodes',
        NEW.memory_id,
        'upsert',
        json_object(
            'memory_id', NEW.memory_id,
            'memory_type', NEW.memory_type,
            'namespace', NEW.namespace,
            'scope', NEW.scope,
            'subject', NEW.subject,
            'body', NEW.body,
            'source_uri', NEW.source_uri,
            'source_hash', NEW.source_hash,
            'author_agent_id', NEW.author_agent_id,
            'origin_peer_id', NEW.origin_peer_id,
            'authored_at_ms', NEW.authored_at_ms,
            'valid_from_ms', NEW.valid_from_ms,
            'valid_to_ms', NEW.valid_to_ms,
            'lifecycle_state', NEW.lifecycle_state,
            'schema_version', NEW.schema_version
        ),
        NEW.memory_id,
        NEW.namespace,
        NEW.authored_at_ms
    );
END;

CREATE TRIGGER IF NOT EXISTS trg_memory_nodes_capture_update
AFTER UPDATE ON memory_nodes
WHEN (SELECT suppress FROM capture_control WHERE singleton = 1) = 0
BEGIN
    UPDATE crsql_clock SET version = version + 1 WHERE singleton = 1;
    INSERT INTO crsql_changes(site_id, db_version, seq, table_name, pk, op, row_json, memory_id, namespace, changed_at_ms)
    VALUES(
        NEW.origin_peer_id,
        (SELECT version FROM crsql_clock WHERE singleton = 1),
        0,
        'memory_nodes',
        NEW.memory_id,
        'upsert',
        json_object(
            'memory_id', NEW.memory_id,
            'memory_type', NEW.memory_type,
            'namespace', NEW.namespace,
            'scope', NEW.scope,
            'subject', NEW.subject,
            'body', NEW.body,
            'source_uri', NEW.source_uri,
            'source_hash', NEW.source_hash,
            'author_agent_id', NEW.author_agent_id,
            'origin_peer_id', NEW.origin_peer_id,
            'authored_at_ms', NEW.authored_at_ms,
            'valid_from_ms', NEW.valid_from_ms,
            'valid_to_ms', NEW.valid_to_ms,
            'lifecycle_state', NEW.lifecycle_state,
            'schema_version', NEW.schema_version
        ),
        NEW.memory_id,
        NEW.namespace,
        CAST(strftime('%s', 'now') AS INTEGER) * 1000
    );
END;

CREATE TRIGGER IF NOT EXISTS trg_memory_edges_capture_insert
AFTER INSERT ON memory_edges
WHEN (SELECT suppress FROM capture_control WHERE singleton = 1) = 0
BEGIN
    UPDATE crsql_clock SET version = version + 1 WHERE singleton = 1;
    INSERT INTO crsql_changes(site_id, db_version, seq, table_name, pk, op, row_json, memory_id, namespace, changed_at_ms)
    VALUES(
        NEW.origin_peer_id,
        (SELECT version FROM crsql_clock WHERE singleton = 1),
        0,
        'memory_edges',
        NEW.edge_id,
        'upsert',
        json_object(
            'edge_id', NEW.edge_id,
            'from_memory_id', NEW.from_memory_id,
            'to_memory_id', NEW.to_memory_id,
            'relation_type', NEW.relation_type,
            'weight', NEW.weight,
            'origin_peer_id', NEW.origin_peer_id,
            'authored_at_ms', NEW.authored_at_ms
        ),
        NEW.from_memory_id,
        COALESCE((SELECT namespace FROM memory_nodes WHERE memory_id = NEW.from_memory_id), ''),
        NEW.authored_at_ms
    );
END;

CREATE TRIGGER IF NOT EXISTS trg_memory_signals_capture_insert
AFTER INSERT ON memory_signals
WHEN (SELECT suppress FROM capture_control WHERE singleton = 1) = 0
BEGIN
    UPDATE crsql_clock SET version = version + 1 WHERE singleton = 1;
    INSERT INTO crsql_changes(site_id, db_version, seq, table_name, pk, op, row_json, memory_id, namespace, changed_at_ms)
    VALUES(
        NEW.peer_id,
        (SELECT version FROM crsql_clock WHERE singleton = 1),
        0,
        'memory_signals',
        NEW.signal_id,
        'upsert',
        json_object(
            'signal_id', NEW.signal_id,
            'memory_id', NEW.memory_id,
            'peer_id', NEW.peer_id,
            'agent_id', NEW.agent_id,
            'signal_type', NEW.signal_type,
            'value', NEW.value,
            'reason', NEW.reason,
            'authored_at_ms', NEW.authored_at_ms
        ),
        NEW.memory_id,
        COALESCE((SELECT namespace FROM memory_nodes WHERE memory_id = NEW.memory_id), ''),
        NEW.authored_at_ms
    );
END;

CREATE TRIGGER IF NOT EXISTS trg_artifact_refs_capture_insert
AFTER INSERT ON artifact_refs
WHEN (SELECT suppress FROM capture_control WHERE singleton = 1) = 0
BEGIN
    UPDATE crsql_clock SET version = version + 1 WHERE singleton = 1;
    INSERT INTO crsql_changes(site_id, db_version, seq, table_name, pk, op, row_json, memory_id, namespace, changed_at_ms)
    VALUES(
        NEW.origin_peer_id,
        (SELECT version FROM crsql_clock WHERE singleton = 1),
        0,
        'artifact_refs',
        NEW.artifact_id,
        'upsert',
        json_object(
            'artifact_id', NEW.artifact_id,
            'namespace', NEW.namespace,
            'uri', NEW.uri,
            'content_hash', NEW.content_hash,
            'title', NEW.title,
            'mime_type', NEW.mime_type,
            'origin_peer_id', NEW.origin_peer_id,
            'authored_at_ms', NEW.authored_at_ms
        ),
        '',
        NEW.namespace,
        NEW.authored_at_ms
    );
END;
