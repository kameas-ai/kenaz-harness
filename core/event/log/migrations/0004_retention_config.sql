-- Migration 0004: versioned retention configuration.
-- Per plan §5.4.

CREATE TABLE IF NOT EXISTS retention_config (
    version       INTEGER PRIMARY KEY,
    policy        TEXT NOT NULL,
    effective_at  INTEGER NOT NULL
);

INSERT OR IGNORE INTO retention_config(version, policy, effective_at)
VALUES (1, '{"kind":"keep_all"}', 0);
