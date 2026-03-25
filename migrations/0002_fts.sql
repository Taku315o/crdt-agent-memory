CREATE TABLE IF NOT EXISTS memory_fts (
    memory_space TEXT NOT NULL,
    memory_id TEXT NOT NULL,
    namespace TEXT NOT NULL,
    subject TEXT NOT NULL,
    body TEXT NOT NULL,
    PRIMARY KEY (memory_space, memory_id)
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
