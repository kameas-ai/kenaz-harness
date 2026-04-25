package migrations

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// LedgerDDL is the SQL that creates the harness_meta and
// harness_migrations tables. Bootstrap migrations 1 and 2 invoke this.
//
// SQL note: this script must be valid under both libSQL and stdlib
// SQLite. INTEGER for booleans and timestamps stored as ISO-8601 text
// keeps it portable.
const LedgerDDL = `
CREATE TABLE IF NOT EXISTS harness_meta (
    install_id          TEXT NOT NULL PRIMARY KEY,
    created_at          TEXT NOT NULL,
    libsql_version      TEXT NOT NULL DEFAULT '',
    sqlite_version      TEXT NOT NULL DEFAULT '',
    schema_version      INTEGER NOT NULL DEFAULT 0,
    encryption_status   TEXT NOT NULL DEFAULT 'disabled',
    wal_mode            INTEGER NOT NULL DEFAULT 1,
    foreign_keys_on     INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS harness_migrations (
    rowid                       INTEGER PRIMARY KEY AUTOINCREMENT,
    version                     INTEGER NOT NULL,
    id                          TEXT NOT NULL,
    applied_at                  TEXT NOT NULL,
    content_hash                TEXT NOT NULL,
    owning_mission              TEXT NOT NULL,
    action                      TEXT NOT NULL,
    rolled_back_from_version    INTEGER NULL
);

CREATE INDEX IF NOT EXISTS idx_harness_migrations_version
    ON harness_migrations (version);
CREATE INDEX IF NOT EXISTS idx_harness_migrations_action
    ON harness_migrations (action);
`

// EnsureLedger creates the ledger tables if they don't exist. Idempotent.
// Called by storage.Open before any migration apply.
func EnsureLedger(ctx context.Context, exec Executor) error {
	return exec.WriteTx(ctx, func(tx WriteTx) error {
		// Split DDL on semicolons. Each CREATE statement is independent
		// and safe to issue separately.
		stmts := splitStatements(LedgerDDL)
		for _, stmt := range stmts {
			if stmt == "" {
				continue
			}
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("ensure ledger: %w", err)
			}
		}
		return nil
	})
}

// Applied returns every ledger row ordered by rowid (insertion order).
// Includes both applied and rolled_back rows.
func (r *Registry) Applied() ([]LedgerEntry, error) {
	if r.exec == nil {
		return nil, errors.New("migrations: no executor configured")
	}
	ctx := context.Background()
	rows, err := r.exec.Query(ctx, `
        SELECT version, id, applied_at, content_hash, owning_mission, action, rolled_back_from_version
        FROM harness_migrations
        ORDER BY rowid ASC
    `)
	if err != nil {
		return nil, fmt.Errorf("applied: %w", err)
	}
	defer rows.Close()
	var out []LedgerEntry
	for rows.Next() {
		var (
			version  int
			id       string
			at       string
			hash     string
			owning   string
			action   string
			rbFromOK *int
		)
		// Driver-portable null handling: scan into *int via sql.NullInt64
		// shape would require importing database/sql. Migrations exec
		// abstraction pushes this into the storage adapter; for the
		// pure-Go fake we treat the value as -1-sentinel via Scan.
		var rbFromRaw any
		if err := rows.Scan(&version, &id, &at, &hash, &owning, &action, &rbFromRaw); err != nil {
			return nil, fmt.Errorf("applied scan: %w", err)
		}
		if rbFromRaw != nil {
			switch v := rbFromRaw.(type) {
			case int64:
				vv := int(v)
				rbFromOK = &vv
			case int:
				vv := v
				rbFromOK = &vv
			}
		}
		out = append(out, LedgerEntry{
			Version:               version,
			ID:                    id,
			AppliedAt:             at,
			ContentHash:           hash,
			OwningMission:         owning,
			Action:                LedgerAction(action),
			RolledBackFromVersion: rbFromOK,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("applied iter: %w", err)
	}
	return out, nil
}

// appendLedger writes a ledger row. Caller is inside a write tx.
func appendLedger(ctx context.Context, tx WriteTx, e LedgerEntry) error {
	var rbFrom any
	if e.RolledBackFromVersion != nil {
		rbFrom = *e.RolledBackFromVersion
	}
	_, err := tx.Exec(ctx, `
        INSERT INTO harness_migrations
            (version, id, applied_at, content_hash, owning_mission, action, rolled_back_from_version)
        VALUES (?, ?, ?, ?, ?, ?, ?)
    `,
		e.Version, e.ID, e.AppliedAt, e.ContentHash, e.OwningMission, string(e.Action), rbFrom,
	)
	return err
}

// upsertHarnessMetaInstallID writes the install_id row on first boot.
// Idempotent: subsequent calls leave the existing row untouched.
func upsertHarnessMetaInstallID(ctx context.Context, tx WriteTx, installID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := tx.Exec(ctx, `
        INSERT INTO harness_meta (install_id, created_at)
        VALUES (?, ?)
        ON CONFLICT(install_id) DO NOTHING
    `, installID, now)
	return err
}

// splitStatements splits a SQL script on top-level semicolons. It does
// not understand strings, so the DDL above must avoid embedded ';' in
// quoted text — verified by inspection.
func splitStatements(src string) []string {
	var out []string
	var b []byte
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == ';' {
			s := trimSpaceASCII(string(b))
			if s != "" {
				out = append(out, s)
			}
			b = b[:0]
			continue
		}
		b = append(b, c)
	}
	if len(b) > 0 {
		s := trimSpaceASCII(string(b))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func trimSpaceASCII(s string) string {
	start := 0
	for start < len(s) {
		c := s[start]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		start++
	}
	end := len(s)
	for end > start {
		c := s[end-1]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		end--
	}
	return s[start:end]
}
