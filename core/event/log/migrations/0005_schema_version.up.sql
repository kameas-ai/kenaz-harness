-- Migration 0005: schema_version column on events.
-- Owner: core/event (audit-log-enhancement-01KX5R8F WP01).
-- Per plan §5.5.

-- Add schema_version to events. Existing rows are backfilled to version 1.
ALTER TABLE events ADD COLUMN schema_version INTEGER NOT NULL DEFAULT 1;
