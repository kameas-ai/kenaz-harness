-- Migration 0001: events table + indexes + FTS5.
-- Owner: core/event (event-log-01KQ1A3M).
-- Per plan §5.1.

CREATE TABLE IF NOT EXISTS events (
    event_id           TEXT PRIMARY KEY,
    session_id         TEXT,
    emitter_id         TEXT NOT NULL,
    kind               TEXT NOT NULL,
    emitted_at         INTEGER NOT NULL,
    payload            BLOB NOT NULL,
    payload_hash       BLOB NOT NULL,
    prev_hash          BLOB NOT NULL,
    redaction_summary  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_events_session_event   ON events(session_id, event_id);
CREATE INDEX IF NOT EXISTS idx_events_kind_event      ON events(kind, event_id);
CREATE INDEX IF NOT EXISTS idx_events_emitter_event   ON events(emitter_id, event_id);
CREATE INDEX IF NOT EXISTS idx_events_emitted_at      ON events(emitted_at);

CREATE VIRTUAL TABLE IF NOT EXISTS events_fts USING fts5(
    payload,
    content='events',
    content_rowid='rowid'
);

-- Keep FTS in sync.
CREATE TRIGGER IF NOT EXISTS events_ai AFTER INSERT ON events BEGIN
    INSERT INTO events_fts(rowid, payload) VALUES (new.rowid, new.payload);
END;

-- No update / delete triggers: append-only at storage layer (C-002).
