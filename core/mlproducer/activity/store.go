// Package activity is the harness ML producer's LOCAL activity store
// (ml-producer-01MLPRD01 WP11, spec §12 A-16: always collect locally;
// Fleet is opt-in).
//
// It is a SEPARATE sqlite file, <dataDir>/ml/activity.db, not a table in
// the harness's data.db (kenaz-ml ruling 2026-10-10, option (a): least
// privilege — the local engine is handed this one file through
// KENAZ_ML_ACTIVITY_DB and never opens data.db). It has its own small
// schema-version ledger (harness_activity_schema) and is NOT part of the
// data.db migration framework.
//
// Its tables mirror Fleet's per-tenant ML schema
// (kenaz-fleet service/mltenant/mltenant.go @ d53c8c9, :160-237) under
// the agreed SQLite mapping, so the engine's existing DataStore /
// SqliteStore reads it unchanged:
//
//   - BIGSERIAL id   → INTEGER PRIMARY KEY AUTOINCREMENT (ids are never
//     reused, even after a prune or a delete, so the engine's ml_cursor
//     stays valid);
//   - UUID           → lowercase TEXT; JSONB → TEXT;
//   - received_at    → INTEGER unix ms, defaulted by sqlite;
//   - the producer CHECK, the dedup UNIQUE, the tasks primary key and the
//     index names are Fleet's.
//
// Ownership: the harness writes events and tasks; it CREATES
// ml_predictions and ml_events, which the engine inserts into; the engine
// creates and owns ml_cursor (never created here). ml_signals is optional
// on Fleet and not created.
//
// Values are the minimised wire form (core/mlproducer): hashed paths, two
// token commands, agent-<h> task ids. Nothing raw is stored.
//
// Every connection runs WAL with busy_timeout=5000 and IMMEDIATE write
// transactions, so the engine process writing ml_predictions while the
// harness writes events waits instead of failing with SQLITE_BUSY.
package activity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // the "sqlite" driver (same as core/storage/sqlite)
)

// DirName / FileName place the store at <dataDir>/ml/activity.db.
const (
	DirName  = "ml"
	FileName = "activity.db"
)

// EnvVar is the variable the local ML engine is spawned with: the absolute
// path of this store (kenaz-ml: `serve --activity-db` wins over it; unset
// means no store, and there is no default).
const EnvVar = "KENAZ_ML_ACTIVITY_DB"

// Producer is every harness row's producer value.
const Producer = "harness"

// SchemaVersion is the newest schema this build knows (see migrations).
const SchemaVersion = 1

// Path returns <dataDir>/ml/activity.db, absolute when dataDir resolves.
func Path(dataDir string) string {
	p := filepath.Join(dataDir, DirName, FileName)
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// migrations is the store's own ordered schema ledger: index i holds the
// statements that bring version i to i+1. Additive only; a shipped entry
// is never edited.
var migrations = [][]string{
	// v1 (WP11): Fleet's tenant tables, SQLite-mapped.
	{
		`CREATE TABLE IF NOT EXISTS events (
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
		)`,
		`CREATE INDEX IF NOT EXISTS idx_events_user_id ON events (user_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_events_user_ts ON events (user_id, ts)`,
		`CREATE TABLE IF NOT EXISTS tasks (
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
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_user_completed ON tasks (user_id, completed_at)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_user_last_active ON tasks (user_id, last_active)`,
		`CREATE TABLE IF NOT EXISTS ml_predictions (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id    TEXT    NOT NULL DEFAULT '',
			model      TEXT    NOT NULL,
			result     TEXT    NOT NULL,
			confidence REAL    NOT NULL,
			created_at INTEGER NOT NULL,
			expires_at INTEGER
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ml_predictions_user_model_created ON ml_predictions (user_id, model, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS ml_events (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id    TEXT    NOT NULL DEFAULT '',
			kind       TEXT    NOT NULL,
			endpoint   TEXT    NOT NULL,
			routing    TEXT    NOT NULL,
			latency_ms INTEGER NOT NULL,
			ts         INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ml_events_ts ON ml_events (ts)`,
	},
}

// DDL returns every schema statement in order (the ledger table first),
// for the kenaz-ml fixture handoff and the drift test.
func DDL() []string {
	out := []string{ledgerDDL}
	for _, m := range migrations {
		out = append(out, m...)
	}
	return out
}

const ledgerDDL = `CREATE TABLE IF NOT EXISTS harness_activity_schema (
			version    INTEGER PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)`

// Store is the open activity database. Safe for concurrent use.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens (creating when absent) the store at path and brings its
// schema to SchemaVersion. The file and its directory are owner-only. A
// file written by a NEWER harness (ledger above SchemaVersion) still
// opens: its migrations are additive, so this build's statements work.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("activity: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("activity: mkdir: %w", err)
	}
	// Pre-create owner-only so sqlite (and its -wal/-shm, which copy the
	// main file's mode) never makes it world-readable.
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600); err == nil {
		_ = f.Close()
	} else {
		return nil, fmt.Errorf("activity: create: %w", err)
	}
	dsn := "file:" + url.PathEscape(path) +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("activity: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: path}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, ledgerDDL); err != nil {
		return fmt.Errorf("activity: ledger: %w", err)
	}
	cur, err := s.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	for v := cur; v < len(migrations); v++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("activity: migrate begin: %w", err)
		}
		for _, stmt := range migrations[v] {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("activity: migrate v%d: %w", v+1, err)
			}
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO harness_activity_schema (version, applied_at) VALUES (?, CAST(unixepoch('subsec')*1000 AS INTEGER))`, v+1); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("activity: migrate ledger v%d: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("activity: migrate commit v%d: %w", v+1, err)
		}
	}
	return nil
}

// SchemaVersion is the highest version the ledger records (0 = none).
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(version) FROM harness_activity_schema`).Scan(&v); err != nil {
		return 0, fmt.Errorf("activity: schema version: %w", err)
	}
	return int(v.Int64), nil
}

// Path is the database file.
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Close closes the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Event is one events row: the event body's kind / ts / payload. source is
// always the producer's collector name, given to Commit.
type Event struct {
	Kind    string
	TS      int64 // unix ms
	Payload map[string]any
}

// Task is one tasks row in Fleet's column shape. CompletedAt 0 = NULL
// (open).
type Task struct {
	ID          string
	RepoRoot    string
	Branch      string
	Phase       string
	Files       map[string]int
	StartedAt   int64
	LastActive  int64
	CompletedAt int64
	CommitCount int
	TestRuns    int
	TestFails   int
}

// Write is one atomic recorder step.
type Write struct {
	Source string // events.source (the collector name)
	Events []Event
	Task   *Task // upserted when non-nil
}

// Local rows carry empty user / node ids: the store is one device's, it
// predates (and outlives) any Fleet sign-in, and Fleet's ml_* tables
// default user_id to the empty string the same way.
const (
	localUserID = ""
	localNodeID = ""
)

// Commit writes w in one transaction and returns the ids of w.Events.
// device_event_id = id (the local row id is this device's event id).
func (s *Store) Commit(ctx context.Context, w Write) ([]int64, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("activity: nil store")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("activity: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	ids := make([]int64, 0, len(w.Events))
	for _, ev := range w.Events {
		payload := ev.Payload
		if payload == nil {
			payload = map[string]any{}
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("activity: payload: %w", err)
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO events (kind, source, payload, ts, user_id, node_id, device_event_id, producer)
			VALUES (?, ?, ?, ?, ?, ?, 0, ?)`,
			ev.Kind, w.Source, string(body), ev.TS, localUserID, localNodeID, Producer)
		if err != nil {
			return nil, fmt.Errorf("activity: insert event: %w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("activity: event id: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE events SET device_event_id = id WHERE id = ?`, id); err != nil {
			return nil, fmt.Errorf("activity: event device id: %w", err)
		}
		ids = append(ids, id)
	}
	if w.Task != nil {
		if err := upsertTask(ctx, tx, *w.Task); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("activity: commit: %w", err)
	}
	return ids, nil
}

func upsertTask(ctx context.Context, tx *sql.Tx, t Task) error {
	files := t.Files
	if files == nil {
		files = map[string]int{}
	}
	fj, err := json.Marshal(files)
	if err != nil {
		return fmt.Errorf("activity: task files: %w", err)
	}
	var completed any
	if t.CompletedAt != 0 {
		completed = t.CompletedAt
	}
	phase := t.Phase
	if phase == "" {
		phase = "idle"
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO tasks (user_id, node_id, id, repo_root, branch, phase, files, started_at,
		  last_active, completed_at, commit_count, test_runs, test_fails, producer)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, node_id, producer, id) DO UPDATE SET
		  repo_root = excluded.repo_root,
		  branch = excluded.branch,
		  phase = excluded.phase,
		  files = excluded.files,
		  started_at = excluded.started_at,
		  last_active = excluded.last_active,
		  completed_at = excluded.completed_at,
		  commit_count = excluded.commit_count,
		  test_runs = excluded.test_runs,
		  test_fails = excluded.test_fails,
		  received_at = CAST(unixepoch('subsec')*1000 AS INTEGER)`,
		localUserID, localNodeID, t.ID, t.RepoRoot, t.Branch, phase, string(fj), t.StartedAt,
		t.LastActive, completed, t.CommitCount, t.TestRuns, t.TestFails, Producer)
	if err != nil {
		return fmt.Errorf("activity: upsert task: %w", err)
	}
	return nil
}

const taskCols = `id, repo_root, branch, phase, files, started_at, last_active, completed_at, commit_count, test_runs, test_fails`

type scanner interface{ Scan(dest ...any) error }

func scanTask(r scanner) (Task, error) {
	var t Task
	var fj string
	var completed sql.NullInt64
	if err := r.Scan(&t.ID, &t.RepoRoot, &t.Branch, &t.Phase, &fj, &t.StartedAt, &t.LastActive,
		&completed, &t.CommitCount, &t.TestRuns, &t.TestFails); err != nil {
		return Task{}, err
	}
	t.CompletedAt = completed.Int64
	t.Files = map[string]int{}
	if fj != "" {
		if err := json.Unmarshal([]byte(fj), &t.Files); err != nil {
			return Task{}, fmt.Errorf("activity: decode task files: %w", err)
		}
	}
	return t, nil
}

// LoadTask returns the harness task id, or ok=false.
func (s *Store) LoadTask(ctx context.Context, id string) (Task, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+taskCols+` FROM tasks
		WHERE user_id = ? AND node_id = ? AND producer = ? AND id = ?`, localUserID, localNodeID, Producer, id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, false, nil
	}
	if err != nil {
		return Task{}, false, fmt.Errorf("activity: load task: %w", err)
	}
	return t, true, nil
}

// OpenTasksIdleSince returns open harness tasks whose last_active is
// before cutoff (unix ms): the recorder's idle sweep.
func (s *Store) OpenTasksIdleSince(ctx context.Context, cutoff int64) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+taskCols+` FROM tasks
		WHERE producer = ? AND completed_at IS NULL AND last_active < ? ORDER BY last_active`, Producer, cutoff)
	if err != nil {
		return nil, fmt.Errorf("activity: idle tasks: %w", err)
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// PruneBefore deletes harness events with ts before cutoff and harness
// tasks whose last activity is before cutoff (unix ms), in one
// transaction, and returns how many of each went. The engine's own tables
// (ml_predictions, ml_events, ml_cursor) are the engine's to prune.
func (s *Store) PruneBefore(ctx context.Context, cutoff int64) (events, tasks int64, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("activity: prune begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM events WHERE producer = ? AND ts < ?`, Producer, cutoff)
	if err != nil {
		return 0, 0, fmt.Errorf("activity: prune events: %w", err)
	}
	events, _ = res.RowsAffected()
	res, err = tx.ExecContext(ctx, `DELETE FROM tasks WHERE producer = ? AND last_active < ?`, Producer, cutoff)
	if err != nil {
		return 0, 0, fmt.Errorf("activity: prune tasks: %w", err)
	}
	tasks, _ = res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("activity: prune commit: %w", err)
	}
	return events, tasks, nil
}

// DeleteAll is "Delete local activity data": every row of events, tasks,
// ml_predictions and ml_events (the engine's outputs are derived from the
// activity, so they go too) in one transaction, then a WAL truncate and a
// VACUUM so the deleted pages leave the file. ml_cursor is the engine's
// and is left alone; AUTOINCREMENT keeps new ids above it, so the cursor
// stays valid. The VACUUM is best effort (a concurrent engine reader can
// hold it off); the rows are gone either way.
func (s *Store) DeleteAll(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("activity: delete begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range []string{
		`DELETE FROM events`, `DELETE FROM tasks`, `DELETE FROM ml_predictions`, `DELETE FROM ml_events`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("activity: delete: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("activity: delete commit: %w", err)
	}
	_, _ = s.db.ExecContext(ctx, `VACUUM`)
	_, _ = s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return nil
}

// Stats is the Settings readout.
type Stats struct {
	Events    int64
	Tasks     int64
	SizeBytes int64 // the database file plus its WAL
}

// Stats counts harness rows and sizes the files.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE producer = ?`, Producer).Scan(&st.Events); err != nil {
		return Stats{}, fmt.Errorf("activity: count events: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE producer = ?`, Producer).Scan(&st.Tasks); err != nil {
		return Stats{}, fmt.Errorf("activity: count tasks: %w", err)
	}
	for _, p := range []string{s.path, s.path + "-wal"} {
		if fi, err := os.Stat(p); err == nil {
			st.SizeBytes += fi.Size()
		}
	}
	return st, nil
}
