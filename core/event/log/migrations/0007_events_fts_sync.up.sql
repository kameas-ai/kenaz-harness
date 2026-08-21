-- Migration 0007 (event-log version 106): a one-time correction of the
-- migration 103 seed value, and FTS5 delete/update sync triggers.
-- Owner: event-log. Per audit-that-tells-the-truth-01PMZA10 UNIT-8.
--
-- Statement order matters here: the plain UPDATE below runs first,
-- then the two trigger-creation blocks. register.go's
-- splitTrailingTriggers parses this file with a naive, non-comment-
-- aware string search for the DDL keyword pair that opens a trigger
-- block, so that two-word pair (spelled with a space between them,
-- deliberately not written verbatim in this comment block) must not
-- appear anywhere above the real trigger DDL below. Keep the UPDATE
-- first if this file is ever edited.

-- event-log/0103-retention-config (SHIPPED at v0.65.0, UpSource
-- frozen) seeded policy='{"kind":"keep_all"}', which is not one of the
-- three RetentionStrategy values (keep_forever / delete_after_window /
-- archive_after_window — core/event/log/retention.go:25-32). Corrected
-- here as a data UPDATE scoped to the exact known-bad literal, so it is
-- a no-op on any install where the row already holds a real strategy
-- (there is no such install today — nothing writes retention_config
-- before this migration — but the WHERE clause makes the statement
-- correct regardless, and idempotent on re-application).
UPDATE retention_config SET policy = '{"kind":"keep_forever"}'
    WHERE version = 1 AND policy = '{"kind":"keep_all"}';

-- 0001_events.sql (event-log/0100, SHIPPED at v0.65.0 — its UpSource is
-- content-hash-frozen by assertLedgerHashesUnchanged and must never be
-- edited again) created events_fts as an EXTERNAL-CONTENT fts5 table
-- with only an AFTER INSERT trigger, under a comment at its line 33
-- claiming "No update / delete triggers: append-only at storage layer
-- (C-002)." That claim was false the day it shipped
-- (SweepableBackend.DeleteRows / BulkPurge already existed in the same
-- package) and is [RAN]-proven false in the mission spec: deleting a
-- row from events left its term matchable in events_fts AND made any
-- subsequent SearchFTS query that matched the deleted rowid error with
-- "fts5: missing row N from content table". Because 0001's own text
-- cannot be edited post-ship, THIS statement is the record that
-- supersedes it — 0001:33 is false and is fixed here, not there.
--
-- Trigger bodies mirror core/session/migrations_search_fts.go's
-- messages_fts_au / messages_fts_ad triggers for session_messages —
-- the same external-content fts5 delete/update pattern, already proven
-- in production for a different table.
CREATE TRIGGER IF NOT EXISTS events_fts_au AFTER UPDATE ON events BEGIN
    INSERT INTO events_fts(events_fts, rowid, payload) VALUES ('delete', old.rowid, old.payload);
    INSERT INTO events_fts(rowid, payload) VALUES (new.rowid, new.payload);
END;

CREATE TRIGGER IF NOT EXISTS events_fts_ad AFTER DELETE ON events BEGIN
    INSERT INTO events_fts(events_fts, rowid, payload) VALUES ('delete', old.rowid, old.payload);
END;
