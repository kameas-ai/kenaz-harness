-- Down for migration 0007 (event-log version 106). Drops the two sync
-- triggers. The retention_config seed correction is a data fix, not a
-- schema change, and is deliberately NOT reverted here: reintroducing
-- the invalid '{"kind":"keep_all"}' literal on Down would just recreate
-- the bug the Up half exists to fix, for no benefit (Down is only ever
-- exercised in tests, never against a real install's data).
DROP TRIGGER IF EXISTS events_fts_ad;
DROP TRIGGER IF EXISTS events_fts_au;
