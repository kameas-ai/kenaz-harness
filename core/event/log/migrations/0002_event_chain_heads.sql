-- Migration 0002: per-session chain head cache.
-- Per plan §5.2.

CREATE TABLE IF NOT EXISTS event_chain_heads (
    session_id        TEXT PRIMARY KEY,
    head_event_id     TEXT NOT NULL,
    head_payload_hash BLOB NOT NULL
);
