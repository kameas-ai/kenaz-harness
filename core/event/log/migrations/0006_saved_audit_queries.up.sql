-- Migration 0006: saved_audit_queries table.
-- Owner: core/event (audit-log-enhancement-01KX5R8F WP01).
-- Per plan §5.6.

CREATE TABLE IF NOT EXISTS saved_audit_queries (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    query_json  TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    user_id     TEXT NOT NULL DEFAULT '',
    UNIQUE(name, user_id)
);
