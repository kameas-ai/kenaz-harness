-- Local ML activity store: <dataDir>/ml/activity.db (ml-producer-01MLPRD01 WP11).
-- GENERATED from core/mlproducer/activity.DDL(); do not edit by hand.
-- Regenerate: UPDATE_ACTIVITY_DDL=1 go test ./core/mlproducer/activity -run TestDDLDoc
--
-- Source shape: kenaz-fleet service/mltenant/mltenant.go @ d53c8c9 (:160-237),
-- SQLite-mapped (BIGSERIAL -> INTEGER PRIMARY KEY AUTOINCREMENT, UUID -> TEXT,
-- JSONB -> TEXT, received_at -> INTEGER unix ms). The harness creates every
-- table below; the engine creates and owns ml_cursor. Local rows carry
-- user_id = '' and node_id = '', producer = 'harness', source = 'harness.agent',
-- device_event_id = id; ts / started_at / last_active / completed_at are unix ms.
-- Connections: journal_mode=WAL, busy_timeout=5000. The engine is always handed the
-- absolute path in KENAZ_ML_ACTIVITY_DB (a non-standard data dir uses
-- <dataDir>/ml-activity/activity.db); it never derives one. KENAZ_ML_ACTIVITY_RETENTION_DAYS
-- is read once at spawn: a decimal integer 1..3650 (whitespace trimmed) prunes
-- ml_predictions (created_at) and ml_events (ts) older than that many days; "hold"
-- (case-insensitive) means do not prune this run; missing, empty or any other value
-- means do not prune, with one warning. Never clamped; no 90-day default engine-side.
-- The engine never prunes events / tasks. The harness sends the effective local
-- retention (the last value it read successfully when a read fails) and "hold" when it
-- has none; a change applies at the engine's next start.

CREATE TABLE IF NOT EXISTS harness_activity_schema (
  version    INTEGER PRIMARY KEY,
  applied_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS events (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  kind            TEXT    NOT NULL,
  source          TEXT    NOT NULL,
  payload         TEXT    NOT NULL,
  ts              INTEGER NOT NULL,
  user_id         TEXT    NOT NULL,
  node_id         TEXT    NOT NULL,
  device_event_id INTEGER NOT NULL,
  received_at     INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000 AS INTEGER)),
  producer        TEXT    NOT NULL CHECK (producer IN ('sigild','harness')),
  CONSTRAINT events_dedup_key UNIQUE (user_id, node_id, producer, device_event_id, ts)
);

CREATE INDEX IF NOT EXISTS idx_events_user_id ON events (user_id, id);

CREATE INDEX IF NOT EXISTS idx_events_user_ts ON events (user_id, ts);

CREATE TABLE IF NOT EXISTS tasks (
  user_id      TEXT    NOT NULL,
  node_id      TEXT    NOT NULL,
  id           TEXT    NOT NULL,
  repo_root    TEXT    NOT NULL,
  branch       TEXT    NOT NULL DEFAULT '',
  phase        TEXT    NOT NULL DEFAULT 'idle',
  files        TEXT    NOT NULL DEFAULT '{}',
  started_at   INTEGER NOT NULL,
  last_active  INTEGER NOT NULL,
  completed_at INTEGER,
  commit_count INTEGER NOT NULL DEFAULT 0,
  test_runs    INTEGER NOT NULL DEFAULT 0,
  test_fails   INTEGER NOT NULL DEFAULT 0,
  received_at  INTEGER NOT NULL DEFAULT (CAST(unixepoch('subsec')*1000 AS INTEGER)),
  producer     TEXT    NOT NULL CHECK (producer IN ('sigild','harness')),
  PRIMARY KEY (user_id, node_id, producer, id)
);

CREATE INDEX IF NOT EXISTS idx_tasks_user_completed ON tasks (user_id, completed_at);

CREATE INDEX IF NOT EXISTS idx_tasks_user_last_active ON tasks (user_id, last_active);

CREATE TABLE IF NOT EXISTS ml_predictions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    TEXT    NOT NULL DEFAULT '',
  model      TEXT    NOT NULL,
  result     TEXT    NOT NULL,
  confidence REAL    NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_ml_predictions_user_model_created ON ml_predictions (user_id, model, created_at DESC);

CREATE TABLE IF NOT EXISTS ml_events (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    TEXT    NOT NULL DEFAULT '',
  kind       TEXT    NOT NULL,
  endpoint   TEXT    NOT NULL,
  routing    TEXT    NOT NULL,
  latency_ms INTEGER NOT NULL,
  ts         INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ml_events_ts ON ml_events (ts);
