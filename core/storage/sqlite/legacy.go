package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/sigil-tech/kaneaz-harness/core/logging"
)

// MigrateLegacySessionsDB folds a pre-consolidation
// <DataDir>/sessions.db shortcut into the unified <DataDir>/data.db.
// It is intended to run from core.Start once per boot. Idempotent:
// once the legacy file has been renamed, subsequent calls are no-ops.
//
// Behaviour:
//   - sessions.db missing: no-op.
//   - sessions.db exists, data.db missing: open a fresh data.db,
//     bring its schema up via the migration runner (by calling
//     storage.Open), ATTACH the legacy file, copy rows, DETACH, close,
//     rename legacy to sessions.legacy.db.
//   - sessions.db exists, data.db present: open data.db, ATTACH legacy,
//     copy rows, DETACH, close, rename legacy. Any rows that already
//     exist in main are kept (INSERT OR IGNORE).
//
// The function deliberately runs Open itself rather than asking the
// caller to thread the live storage.DB through, so it can advance the
// schema before touching legacy data — a fresh install ships data.db
// with the full migrated shape, then we ATTACH and copy.
func MigrateLegacySessionsDB(dataDir string) error {
	if dataDir == "" {
		return errors.New("storage/sqlite: empty data dir")
	}
	legacyPath := filepath.Join(dataDir, "sessions.db")
	dataPath := filepath.Join(dataDir, "data.db")
	renamedPath := filepath.Join(dataDir, "sessions.legacy.db")

	if _, err := os.Stat(legacyPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat legacy: %w", err)
	}

	logging.L().Info("storage.legacy_sessions.copy_start",
		"from", legacyPath, "to", dataPath)

	// Open the unified DB so migrations 1..304 run before we copy. The
	// caller will Open() again later to take the long-lived handle —
	// modernc.org/sqlite is happy to be re-opened.
	cfg := defaultConfig(dataDir)
	db, err := Open(cfg)
	if err != nil {
		return fmt.Errorf("open unified db: %w", err)
	}
	if err := db.Close(context.Background()); err != nil {
		return fmt.Errorf("close unified db: %w", err)
	}

	dsn := "file:" + url.PathEscape(dataPath) +
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open data.db: %w", err)
	}
	defer raw.Close()
	raw.SetMaxOpenConns(1)

	ctx := context.Background()

	if _, err := raw.ExecContext(ctx, "ATTACH DATABASE ? AS legacy", legacyPath); err != nil {
		return fmt.Errorf("attach legacy: %w", err)
	}
	defer func() {
		if _, derr := raw.ExecContext(ctx, "DETACH DATABASE legacy"); derr != nil {
			logging.L().Warn("storage.legacy_sessions.detach_failed", "err", derr.Error())
		}
	}()

	cols, err := legacySessionColumns(ctx, raw)
	if err != nil {
		return err
	}
	if err := copyLegacySessions(ctx, raw, cols); err != nil {
		return err
	}
	if _, err := raw.ExecContext(ctx, `INSERT OR IGNORE INTO main.session_messages
        SELECT * FROM legacy.session_messages`); err != nil {
		return fmt.Errorf("copy session_messages: %w", err)
	}

	if err := raw.Close(); err != nil {
		return fmt.Errorf("close raw db: %w", err)
	}

	if err := os.Rename(legacyPath, renamedPath); err != nil {
		return fmt.Errorf("rename legacy: %w", err)
	}
	logging.L().Info("storage.legacy_sessions.copy_done",
		"renamed_to", renamedPath)
	return nil
}

// copyLegacySessions copies every row from legacy.sessions into
// main.sessions, defaulting columns the legacy schema is missing.
// SELECT defaults '' / 'system' for system_prompt / context_kind so a
// pre-303/304 sessions.db lands with sensible values in the columns
// the migration runner just added.
func copyLegacySessions(ctx context.Context, db *sql.DB, legacyCols map[string]bool) error {
	selectExpr := []string{
		"id", "name", "created_at", "updated_at", "last_active_at",
		"position", "draft", "scroll_position", "archived_at",
	}
	if legacyCols["system_prompt"] {
		selectExpr = append(selectExpr, "system_prompt")
	} else {
		selectExpr = append(selectExpr, "'' AS system_prompt")
	}
	if legacyCols["context_kind"] {
		selectExpr = append(selectExpr, "context_kind")
	} else {
		selectExpr = append(selectExpr, "'system' AS context_kind")
	}
	insertCols := `id, name, created_at, updated_at, last_active_at,
        position, draft, scroll_position, archived_at,
        system_prompt, context_kind`
	stmt := `INSERT OR IGNORE INTO main.sessions (` + insertCols + `)
        SELECT ` + joinCols(selectExpr) + ` FROM legacy.sessions`
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("copy sessions: %w", err)
	}
	return nil
}

func joinCols(cols []string) string {
	out := ""
	for i, c := range cols {
		if i > 0 {
			out += ", "
		}
		out += c
	}
	return out
}

func legacySessionColumns(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	// pragma_table_info doesn't accept a database-name prefix in
	// SQLite, but the PRAGMA <schema>.table_info form does.
	rows, err := db.QueryContext(ctx, `PRAGMA legacy.table_info('sessions')`)
	if err != nil {
		return nil, fmt.Errorf("legacy schema probe: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}
