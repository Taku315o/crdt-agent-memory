CREATE TABLE IF NOT EXISTS artifact_spans (
    span_id TEXT PRIMARY KEY NOT NULL,
    artifact_id TEXT NOT NULL DEFAULT '',
    memory_id TEXT NOT NULL DEFAULT '',
    start_offset INTEGER NOT NULL DEFAULT 0,
    end_offset INTEGER NOT NULL DEFAULT 0,
    start_line INTEGER NOT NULL DEFAULT 0,
    end_line INTEGER NOT NULL DEFAULT 0,
    quote_hash TEXT NOT NULL DEFAULT '',
    authored_at_ms INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS private_artifact_spans (
    span_id TEXT PRIMARY KEY NOT NULL,
    artifact_id TEXT NOT NULL DEFAULT '',
    memory_id TEXT NOT NULL DEFAULT '',
    start_offset INTEGER NOT NULL DEFAULT 0,
    end_offset INTEGER NOT NULL DEFAULT 0,
    start_line INTEGER NOT NULL DEFAULT 0,
    end_line INTEGER NOT NULL DEFAULT 0,
    quote_hash TEXT NOT NULL DEFAULT '',
    authored_at_ms INTEGER NOT NULL DEFAULT 0
);

CREATE TRIGGER IF NOT EXISTS trg_artifact_spans_sync_insert
AFTER INSERT ON artifact_spans
BEGIN
    INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
    VALUES(
        crsql_db_version() + 1,
        'artifact_spans',
        NEW.span_id,
        COALESCE((SELECT namespace FROM memory_nodes WHERE memory_id = NEW.memory_id), (SELECT namespace FROM artifact_refs WHERE artifact_id = NEW.artifact_id), ''),
        NEW.memory_id,
        NEW.authored_at_ms
    );
END;

CREATE TRIGGER IF NOT EXISTS trg_artifact_spans_sync_update
AFTER UPDATE ON artifact_spans
BEGIN
    INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
    VALUES(
        crsql_db_version() + 1,
        'artifact_spans',
        NEW.span_id,
        COALESCE((SELECT namespace FROM memory_nodes WHERE memory_id = NEW.memory_id), (SELECT namespace FROM artifact_refs WHERE artifact_id = NEW.artifact_id), ''),
        NEW.memory_id,
        strftime('%s','now') * 1000
    );
END;

CREATE TRIGGER IF NOT EXISTS trg_artifact_spans_sync_delete
AFTER DELETE ON artifact_spans
BEGIN
    INSERT INTO sync_change_log(db_version, table_name, pk_hint, namespace, memory_id, changed_at_ms)
    VALUES(
        crsql_db_version() + 1,
        'artifact_spans',
        OLD.span_id,
        COALESCE((SELECT namespace FROM memory_nodes WHERE memory_id = OLD.memory_id), (SELECT namespace FROM artifact_refs WHERE artifact_id = OLD.artifact_id), ''),
        OLD.memory_id,
        strftime('%s','now') * 1000
    );
END;
