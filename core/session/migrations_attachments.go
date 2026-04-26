package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

// migrationIDAttachments is the identifier for migration 0301.
//
// NOTE: the original spec for context-library WP03 named this migration
// 308. The collapse in 697074f flattened every prior session migration
// into 0300, so the next free version in the "sessions" block is 0301.
// Renumbering here keeps the version stream contiguous; the table name
// and DDL are unchanged from the spec.
const migrationIDAttachments = "sessions/0301-context-attachments"

// sqlAttachmentsSchema is the canonical DDL for context_attachments.
// Indexed for the (scope_kind, scope_id, position) triple that powers
// ListResolved.
const sqlAttachmentsSchema = `
        CREATE TABLE IF NOT EXISTS context_attachments (
            id              TEXT PRIMARY KEY,
            scope_kind      TEXT NOT NULL CHECK (scope_kind IN ('global','project','session')),
            scope_id        TEXT NOT NULL,
            content_source  TEXT NOT NULL,
            content         TEXT NOT NULL,
            kind            TEXT NOT NULL DEFAULT 'system',
            position        INTEGER NOT NULL DEFAULT 0,
            created_at      INTEGER NOT NULL
        );

        CREATE INDEX IF NOT EXISTS idx_context_attachments_scope
            ON context_attachments (scope_kind, scope_id, position);
    `

// migration0301 returns the migration that adds context_attachments and
// seeds it from existing sessions.system_prompt rows. The seeding step
// is implemented in Go (rather than as inline SQL) so each session's
// prompt is content-addressed via sha256 — SQLite has no native sha256.
func migration0301() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDAttachments,
		Version:       301,
		OwningMission: OwningMission,
		UpSource:      sqlAttachmentsSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlAttachmentsSchema) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return seedAttachmentsFromSystemPrompts(ctx, tx)
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				"DROP INDEX IF EXISTS idx_context_attachments_scope",
				"DROP TABLE IF EXISTS context_attachments",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// seedAttachmentsFromSystemPrompts copies every session row carrying a
// non-empty system_prompt into context_attachments as a session-scope
// inline attachment at position 0. Idempotent: re-runs detect a
// matching row by (scope_kind='session', scope_id=<id>, content_source).
//
// SQLite has no native sha256, so we compute the hash in-process and
// emit per-row INSERTs with the address baked into content_source. The
// spec calls this Option (a); the rejected alternative would have used
// "inline:legacy:<id>" but that loses content addressing.
func seedAttachmentsFromSystemPrompts(ctx context.Context, tx migrations.WriteTx) error {
	rows, err := tx.Query(ctx, `
        SELECT id,
               COALESCE(system_prompt, '') AS system_prompt,
               COALESCE(NULLIF(context_kind, ''), 'system') AS context_kind,
               created_at
        FROM sessions
        WHERE system_prompt IS NOT NULL AND system_prompt != ''
    `)
	if err != nil {
		return fmt.Errorf("seed attachments: query sessions: %w", err)
	}
	type seed struct {
		sessionID    string
		systemPrompt string
		kind         string
		createdAt    int64
	}
	var work []seed
	for rows.Next() {
		var s seed
		if err := rows.Scan(&s.sessionID, &s.systemPrompt, &s.kind, &s.createdAt); err != nil {
			rows.Close()
			return fmt.Errorf("seed attachments: scan: %w", err)
		}
		work = append(work, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("seed attachments: rows err: %w", err)
	}

	for _, s := range work {
		hash := sha256.Sum256([]byte(s.systemPrompt))
		contentSource := "inline:" + hex.EncodeToString(hash[:])

		// Skip if a matching row already exists (idempotent rerun).
		exRow := tx.QueryRow(ctx, `
            SELECT 1 FROM context_attachments
            WHERE scope_kind = 'session'
              AND scope_id = ?
              AND content_source = ?
            LIMIT 1
        `, s.sessionID, contentSource)
		var one int
		if err := exRow.Scan(&one); err == nil {
			// Row already present; skip.
			continue
		}

		// Map session.context_kind onto the attachment kind enum:
		// "system" stays "system"; "user_seed" becomes "user".
		kind := "system"
		if s.kind == "user_seed" {
			kind = "user"
		}

		id, err := newAttachmentID()
		if err != nil {
			return fmt.Errorf("seed attachments: id gen: %w", err)
		}
		if _, err := tx.Exec(ctx, `
            INSERT INTO context_attachments
                (id, scope_kind, scope_id, content_source, content,
                 kind, position, created_at)
            VALUES (?, 'session', ?, ?, ?, ?, 0, ?)
        `,
			id, s.sessionID, contentSource, s.systemPrompt, kind, s.createdAt,
		); err != nil {
			return fmt.Errorf("seed attachments: insert %s: %w", s.sessionID, err)
		}
	}
	return nil
}

// newAttachmentID returns a 32-char hex id (16 random bytes). Mirrors
// session and projects manager id shapes so the migrated rows carry
// the same identifier silhouette as future Manager.Add inserts.
func newAttachmentID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// SeedAttachmentsForTest exposes the seeding step to package tests so
// they can drive it against a real SQLite db without going through the
// full migration apply path. Production callers must NOT use this — the
// migration runner owns the seed lifecycle.
//
// tx must be a session.WriteTx (or anything matching its shape); the
// adapter in dbadapter.go projects a storage.WriteTx into the session
// shape, so callers wiring through storage.DB.WriteTx pass the wrapped
// value.
func SeedAttachmentsForTest(ctx context.Context, tx WriteTx) error {
	return seedAttachmentsFromSystemPrompts(ctx, &writeTxToMigrationsAdapter{tx: tx})
}

// writeTxToMigrationsAdapter projects a session.WriteTx into a
// migrations.WriteTx. Same method shapes; we wrap so the seeding helper
// keeps its production signature.
type writeTxToMigrationsAdapter struct {
	tx WriteTx
}

func (a *writeTxToMigrationsAdapter) Exec(ctx context.Context, query string, args ...any) (migrations.Result, error) {
	res, err := a.tx.Exec(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return migrationsResultAdapter{r: res}, nil
}

func (a *writeTxToMigrationsAdapter) QueryRow(ctx context.Context, query string, args ...any) migrations.Row {
	return migrationsRowAdapter{r: a.tx.QueryRow(ctx, query, args...)}
}

func (a *writeTxToMigrationsAdapter) Query(ctx context.Context, query string, args ...any) (migrations.Rows, error) {
	rows, err := a.tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return migrationsRowsAdapter{r: rows}, nil
}

type migrationsResultAdapter struct{ r Result }

func (a migrationsResultAdapter) RowsAffected() (int64, error) { return a.r.RowsAffected() }
func (a migrationsResultAdapter) LastInsertID() (int64, error) { return 0, nil }

type migrationsRowAdapter struct{ r Row }

func (a migrationsRowAdapter) Scan(dest ...any) error { return a.r.Scan(dest...) }

type migrationsRowsAdapter struct{ r Rows }

func (a migrationsRowsAdapter) Next() bool             { return a.r.Next() }
func (a migrationsRowsAdapter) Scan(dest ...any) error { return a.r.Scan(dest...) }
func (a migrationsRowsAdapter) Close() error           { return a.r.Close() }
func (a migrationsRowsAdapter) Err() error             { return a.r.Err() }
