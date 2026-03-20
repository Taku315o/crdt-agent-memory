CREATE TABLE IF NOT EXISTS local_graph_suspensions (
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    memory_space TEXT NOT NULL DEFAULT 'shared',
    memory_id TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    detail TEXT NOT NULL DEFAULT '',
    first_seen_at_ms INTEGER NOT NULL,
    last_seen_at_ms INTEGER NOT NULL,
    resolved_at_ms INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (entity_type, entity_id)
);

CREATE INDEX IF NOT EXISTS idx_local_graph_suspensions_active
    ON local_graph_suspensions(entity_type, resolved_at_ms, last_seen_at_ms);
