PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE harness_activity_schema (
			version    INTEGER PRIMARY KEY,
			applied_at INTEGER NOT NULL
		);
INSERT INTO harness_activity_schema VALUES(1,1792283910955);
CREATE TABLE events (
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
INSERT INTO events VALUES(1,'phase_change','harness.agent','{"phase":"coding","task":"agent-a1"}',1792108800000,'','',1,1792283910955,'harness');
INSERT INTO events VALUES(2,'tool_call','harness.agent','{"task":"agent-a1","tool":"kenaz__bash"}',1792108801000,'','',2,1792283910955,'harness');
INSERT INTO events VALUES(3,'phase_change','harness.agent','{"phase":"coding","task":"agent-b2"}',1789603200000,'','',3,1792283910956,'harness');
INSERT INTO events VALUES(4,'tool_call','harness.agent','{"task":"agent-b2","tool":"kenaz__bash"}',1789603201000,'','',4,1792283910956,'harness');
INSERT INTO events VALUES(5,'phase_change','harness.agent','{"phase":"coding","task":"agent-c3"}',1783555200000,'','',5,1792283910956,'harness');
INSERT INTO events VALUES(6,'tool_call','harness.agent','{"task":"agent-c3","tool":"kenaz__bash"}',1783555201000,'','',6,1792283910956,'harness');
INSERT INTO events VALUES(7,'phase_change','harness.agent','{"phase":"coding","task":"agent-d4"}',1757635200000,'','',7,1792283910957,'harness');
INSERT INTO events VALUES(8,'tool_call','harness.agent','{"task":"agent-d4","tool":"kenaz__bash"}',1757635201000,'','',8,1792283910957,'harness');
CREATE TABLE tasks (
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
INSERT INTO tasks VALUES('','','agent-a1','h:repo','xxxx','coding','{"h:f1":2}',1792108800000,1792108801000,NULL,0,0,0,1792283910955,'harness');
INSERT INTO tasks VALUES('','','agent-b2','h:repo','xxxx','coding','{"h:f1":2}',1789603200000,1789603201000,NULL,1,1,0,1792283910956,'harness');
INSERT INTO tasks VALUES('','','agent-c3','h:repo','xxxx','coding','{"h:f1":2}',1783555200000,1783555201000,NULL,2,2,0,1792283910956,'harness');
INSERT INTO tasks VALUES('','','agent-d4','h:repo','xxxx','coding','{"h:f1":2}',1757635200000,1757635201000,NULL,3,3,0,1792283910957,'harness');
CREATE TABLE ml_predictions (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id    TEXT    NOT NULL DEFAULT '',
			model      TEXT    NOT NULL,
			result     TEXT    NOT NULL,
			confidence REAL    NOT NULL,
			created_at INTEGER NOT NULL,
			expires_at INTEGER
		);
INSERT INTO ml_predictions VALUES(1,'','phase','{"phase":"testing"}',0.75,1792108800000,1792195200000);
INSERT INTO ml_predictions VALUES(2,'','phase','{"phase":"testing"}',0.75,1789603200000,1789689600000);
INSERT INTO ml_predictions VALUES(3,'','phase','{"phase":"testing"}',0.75,1783555200000,1783641600000);
INSERT INTO ml_predictions VALUES(4,'','phase','{"phase":"testing"}',0.75,1757635200000,1757721600000);
CREATE TABLE ml_events (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id    TEXT    NOT NULL DEFAULT '',
			kind       TEXT    NOT NULL,
			endpoint   TEXT    NOT NULL,
			routing    TEXT    NOT NULL,
			latency_ms INTEGER NOT NULL,
			ts         INTEGER NOT NULL
		);
INSERT INTO ml_events VALUES(1,'','predict','/v1/phase','local',12,1792108800000);
INSERT INTO ml_events VALUES(2,'','predict','/v1/phase','local',13,1789603200000);
INSERT INTO ml_events VALUES(3,'','predict','/v1/phase','local',14,1783555200000);
INSERT INTO ml_events VALUES(4,'','predict','/v1/phase','local',15,1757635200000);
INSERT INTO sqlite_sequence VALUES('events',8);
INSERT INTO sqlite_sequence VALUES('ml_predictions',4);
INSERT INTO sqlite_sequence VALUES('ml_events',4);
CREATE INDEX idx_events_user_id ON events (user_id, id);
CREATE INDEX idx_events_user_ts ON events (user_id, ts);
CREATE INDEX idx_tasks_user_completed ON tasks (user_id, completed_at);
CREATE INDEX idx_tasks_user_last_active ON tasks (user_id, last_active);
CREATE INDEX idx_ml_predictions_user_model_created ON ml_predictions (user_id, model, created_at DESC);
CREATE INDEX idx_ml_events_ts ON ml_events (ts);
COMMIT;
