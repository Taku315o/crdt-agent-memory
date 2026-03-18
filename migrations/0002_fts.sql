PRAGMA trusted_schema = ON;

CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
    memory_space UNINDEXED,
    memory_id UNINDEXED,
    namespace UNINDEXED,
    subject,
    body
);

CREATE TRIGGER IF NOT EXISTS trg_memory_nodes_fts_insert
AFTER INSERT ON memory_nodes
BEGIN
    INSERT INTO memory_fts(memory_space, memory_id, namespace, subject, body)
    VALUES ('shared', NEW.memory_id, NEW.namespace, NEW.subject, NEW.body);
END;

CREATE TRIGGER IF NOT EXISTS trg_memory_nodes_fts_update
AFTER UPDATE ON memory_nodes
BEGIN
    DELETE FROM memory_fts WHERE memory_space = 'shared' AND memory_id = NEW.memory_id;
    INSERT INTO memory_fts(memory_space, memory_id, namespace, subject, body)
    VALUES ('shared', NEW.memory_id, NEW.namespace, NEW.subject, NEW.body);
END;

CREATE TRIGGER IF NOT EXISTS trg_memory_nodes_fts_delete
AFTER DELETE ON memory_nodes
BEGIN
    DELETE FROM memory_fts WHERE memory_space = 'shared' AND memory_id = OLD.memory_id;
END;

CREATE TRIGGER IF NOT EXISTS trg_private_memory_nodes_fts_insert
AFTER INSERT ON private_memory_nodes
BEGIN
    INSERT INTO memory_fts(memory_space, memory_id, namespace, subject, body)
    VALUES ('private', NEW.memory_id, NEW.local_namespace, NEW.subject, NEW.body);
END;

CREATE TRIGGER IF NOT EXISTS trg_private_memory_nodes_fts_update
AFTER UPDATE ON private_memory_nodes
BEGIN
    DELETE FROM memory_fts WHERE memory_space = 'private' AND memory_id = NEW.memory_id;
    INSERT INTO memory_fts(memory_space, memory_id, namespace, subject, body)
    VALUES ('private', NEW.memory_id, NEW.local_namespace, NEW.subject, NEW.body);
END;

CREATE TRIGGER IF NOT EXISTS trg_private_memory_nodes_fts_delete
AFTER DELETE ON private_memory_nodes
BEGIN
    DELETE FROM memory_fts WHERE memory_space = 'private' AND memory_id = OLD.memory_id;
END;
