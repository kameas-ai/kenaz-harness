-- Migration 0003: operator-managed redaction rules table.
-- Per plan §5.3. Rules are append-only; "disable" is recorded by
-- setting disabled_at, never by deletion.

CREATE TABLE IF NOT EXISTS redaction_rules (
    rule_id      TEXT PRIMARY KEY,
    kind         TEXT NOT NULL CHECK (kind IN ('pattern','field_path','kind_specific')),
    definition   TEXT NOT NULL,
    enabled_at   INTEGER NOT NULL,
    disabled_at  INTEGER
);
