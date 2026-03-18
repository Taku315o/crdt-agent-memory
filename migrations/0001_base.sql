CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS app_metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sync_cursors (
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
    schema_fenced INTEGER NOT NULL DEFAULT 0,
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

CREATE TABLE IF NOT EXISTS memory_embeddings (
    memory_space TEXT NOT NULL,
    memory_id TEXT NOT NULL,
    embedding_json TEXT NOT NULL,
    embedding_dim INTEGER NOT NULL,
    indexed_at_ms INTEGER NOT NULL,
    PRIMARY KEY (memory_space, memory_id)
);

CREATE TABLE IF NOT EXISTS sync_change_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    db_version INTEGER NOT NULL,
    table_name TEXT NOT NULL,
    pk_hint TEXT NOT NULL,
    namespace TEXT NOT NULL,
    memory_id TEXT NOT NULL DEFAULT '',
    changed_at_ms INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sync_change_log_namespace_version
    ON sync_change_log(namespace, db_version, id);

CREATE TABLE IF NOT EXISTS memory_nodes (
    memory_id TEXT PRIMARY KEY NOT NULL,
    memory_type TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    scope TEXT NOT NULL DEFAULT '',
    subject TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    source_uri TEXT NOT NULL DEFAULT '',
    source_hash TEXT NOT NULL DEFAULT '',
    author_agent_id TEXT NOT NULL DEFAULT '',
    origin_peer_id TEXT NOT NULL DEFAULT '',
    authored_at_ms INTEGER NOT NULL DEFAULT 0,
    valid_from_ms INTEGER NOT NULL DEFAULT 0,
    valid_to_ms INTEGER NOT NULL DEFAULT 0,
    lifecycle_state TEXT NOT NULL DEFAULT 'active',
    schema_version INTEGER NOT NULL DEFAULT 1,
    author_signature BLOB NOT NULL DEFAULT X''
);

CREATE TABLE IF NOT EXISTS memory_edges (
    edge_id TEXT PRIMARY KEY NOT NULL,
    from_memory_id TEXT NOT NULL DEFAULT '',
    to_memory_id TEXT NOT NULL DEFAULT '',
    relation_type TEXT NOT NULL DEFAULT '',
    weight REAL NOT NULL DEFAULT 1.0,
    origin_peer_id TEXT NOT NULL DEFAULT '',
    authored_at_ms INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS memory_signals (
    signal_id TEXT PRIMARY KEY NOT NULL,
    memory_id TEXT NOT NULL DEFAULT '',
    peer_id TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    signal_type TEXT NOT NULL DEFAULT '',
    value REAL NOT NULL DEFAULT 0,
    reason TEXT NOT NULL DEFAULT '',
    authored_at_ms INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS artifact_refs (
    artifact_id TEXT PRIMARY KEY NOT NULL,
    namespace TEXT NOT NULL DEFAULT '',
    uri TEXT NOT NULL DEFAULT '',
    content_hash TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    mime_type TEXT NOT NULL DEFAULT '',
    origin_peer_id TEXT NOT NULL DEFAULT '',
    authored_at_ms INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS private_memory_nodes (
    memory_id TEXT PRIMARY KEY NOT NULL,
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
    edge_id TEXT PRIMARY KEY NOT NULL,
    from_memory_id TEXT NOT NULL,
    to_memory_id TEXT NOT NULL,
    relation_type TEXT NOT NULL,
    weight REAL NOT NULL DEFAULT 1.0,
    authored_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS private_memory_signals (
    signal_id TEXT PRIMARY KEY NOT NULL,
    memory_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    signal_type TEXT NOT NULL,
    value REAL NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    authored_at_ms INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS private_artifact_refs (
    artifact_id TEXT PRIMARY KEY NOT NULL,
    local_namespace TEXT NOT NULL,
    uri TEXT NOT NULL,
    content_hash TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    mime_type TEXT NOT NULL DEFAULT '',
    authored_at_ms INTEGER NOT NULL
);

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

CREATE TRIGGER IF NOT EXISTS trg_memory_nodes_sync_insert
AFTER INSERT ON memory_nodes
BEGIN
    INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
    VALUES(crsql_db_version() + 1, 'memory_nodes', NEW.memory_id, NEW.namespace, NEW.memory_id, NEW.authored_at_ms);
END;

CREATE TRIGGER IF NOT EXISTS trg_memory_nodes_sync_update
AFTER UPDATE ON memory_nodes
BEGIN
    INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
    VALUES(crsql_db_version() + 1, 'memory_nodes', NEW.memory_id, NEW.namespace, NEW.memory_id, strftime('%s','now') * 1000);
END;

CREATE TRIGGER IF NOT EXISTS trg_memory_nodes_sync_delete
AFTER DELETE ON memory_nodes
BEGIN
    INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
    VALUES(crsql_db_version() + 1, 'memory_nodes', OLD.memory_id, OLD.namespace, OLD.memory_id, strftime('%s','now') * 1000);
END;

CREATE TRIGGER IF NOT EXISTS trg_memory_edges_sync_insert
AFTER INSERT ON memory_edges
BEGIN
    INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
    VALUES(
        crsql_db_version() + 1,
        'memory_edges',
        NEW.edge_id,
        COALESCE((SELECT namespace FROM memory_nodes WHERE memory_id = NEW.from_memory_id), (SELECT namespace FROM memory_nodes WHERE memory_id = NEW.to_memory_id), ''),
        COALESCE(NEW.from_memory_id, NEW.to_memory_id, ''),
        NEW.authored_at_ms
    );
END;

CREATE TRIGGER IF NOT EXISTS trg_memory_edges_sync_update
AFTER UPDATE ON memory_edges
BEGIN
    INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
    VALUES(
        crsql_db_version() + 1,
        'memory_edges',
        NEW.edge_id,
        COALESCE((SELECT namespace FROM memory_nodes WHERE memory_id = NEW.from_memory_id), (SELECT namespace FROM memory_nodes WHERE memory_id = NEW.to_memory_id), ''),
        COALESCE(NEW.from_memory_id, NEW.to_memory_id, ''),
        strftime('%s','now') * 1000
    );
END;

CREATE TRIGGER IF NOT EXISTS trg_memory_edges_sync_delete
AFTER DELETE ON memory_edges
BEGIN
    INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
    VALUES(
        crsql_db_version() + 1,
        'memory_edges',
        OLD.edge_id,
        COALESCE((SELECT namespace FROM memory_nodes WHERE memory_id = OLD.from_memory_id), (SELECT namespace FROM memory_nodes WHERE memory_id = OLD.to_memory_id), ''),
        COALESCE(OLD.from_memory_id, OLD.to_memory_id, ''),
        strftime('%s','now') * 1000
    );
END;

CREATE TRIGGER IF NOT EXISTS trg_memory_signals_sync_insert
AFTER INSERT ON memory_signals
BEGIN
    INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
    VALUES(
        crsql_db_version() + 1,
        'memory_signals',
        NEW.signal_id,
        COALESCE((SELECT namespace FROM memory_nodes WHERE memory_id = NEW.memory_id), ''),
        NEW.memory_id,
        NEW.authored_at_ms
    );
END;

CREATE TRIGGER IF NOT EXISTS trg_memory_signals_sync_update
AFTER UPDATE ON memory_signals
BEGIN
    INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
    VALUES(
        crsql_db_version() + 1,
        'memory_signals',
        NEW.signal_id,
        COALESCE((SELECT namespace FROM memory_nodes WHERE memory_id = NEW.memory_id), ''),
        NEW.memory_id,
        strftime('%s','now') * 1000
    );
END;

CREATE TRIGGER IF NOT EXISTS trg_memory_signals_sync_delete
AFTER DELETE ON memory_signals
BEGIN
    INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
    VALUES(
        crsql_db_version() + 1,
        'memory_signals',
        OLD.signal_id,
        COALESCE((SELECT namespace FROM memory_nodes WHERE memory_id = OLD.memory_id), ''),
        OLD.memory_id,
        strftime('%s','now') * 1000
    );
END;

CREATE TRIGGER IF NOT EXISTS trg_artifact_refs_sync_insert
AFTER INSERT ON artifact_refs
BEGIN
    INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
    VALUES(crsql_db_version() + 1, 'artifact_refs', NEW.artifact_id, NEW.namespace, '', NEW.authored_at_ms);
END;

CREATE TRIGGER IF NOT EXISTS trg_artifact_refs_sync_update
AFTER UPDATE ON artifact_refs
BEGIN
    INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
    VALUES(crsql_db_version() + 1, 'artifact_refs', NEW.artifact_id, NEW.namespace, '', strftime('%s','now') * 1000);
END;

CREATE TRIGGER IF NOT EXISTS trg_artifact_refs_sync_delete
AFTER DELETE ON artifact_refs
BEGIN
    INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
    VALUES(crsql_db_version() + 1, 'artifact_refs', OLD.artifact_id, OLD.namespace, '', strftime('%s','now') * 1000);
END;
