-- Rollback 0005: remove schema_version column.
-- SQLite does not support DROP COLUMN before 3.35; we recreate the table.

CREATE TABLE IF NOT EXISTS events_bak AS SELECT
    event_id, session_id, emitter_id, kind, emitted_at,
    payload, payload_hash, prev_hash, redaction_summary
FROM events;

DROP TABLE events;

ALTER TABLE events_bak RENAME TO events;
