// Package log — the SQLite-backed Backend.
//
// SQLBackend is the missing 90% spec.md §1.1 identifies: MemoryBackend
// was, until this file, the only Backend implementation that has ever
// existed (confirmed by grep: `func .*AppendRow` / `func .*SearchFTS`
// each return exactly one hit, both in store.go). SQLBackend routes
// every read through storage.DB.Reader() and every write through
// storage.DB.WriteTx — the same pattern core/units.sqlStore uses
// (core/units/store_sql.go) — so it never opens a second connection or
// a second file (check-single-persistence-file.sh binds here).
//
// UNIT-3 of audit-that-tells-the-truth-01PMZA10. Lands INERT: nothing
// in core/rpc constructs a SQLBackend yet (that is UNIT-4).
package log

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/storage"
)

// SQLBackend implements Backend and SweepableBackend against the
// harness's unified database.
//
// It deliberately does NOT import core/fleet. check-no-fleet-imports.sh
// permits only core/rpc/views/settings/ to do that, and this package is
// storage, not fleet transport. SelectBefore therefore returns this
// package's own RetentionRow; when the retention sweeper is finally
// wired (it is not — see below), the boundary package converts. Safe for concurrent use — storage.DB already serialises
// writes (core/storage/sqlite/sqlite.go:89, SetMaxOpenConns(1)).
type SQLBackend struct {
	db storage.DB
}

// NewSQLBackend constructs a Backend against the unified storage.DB.
// The caller must have already registered and applied event-log's
// migrations (core/storage/sqlite.Open does both automatically once
// RegisterMigrations is wired into it — see sqlite.go).
func NewSQLBackend(db storage.DB) *SQLBackend {
	return &SQLBackend{db: db}
}

var (
	_ Backend          = (*SQLBackend)(nil)
	_ SweepableBackend = (*SQLBackend)(nil)
)

// RetentionRow carries the fields a retention sweep needs to decide
// whether to delete a row. It mirrors core/fleet.AuditRetentionRow field
// for field, on purpose: the duplication is the layering boundary. A
// two-field struct copied at the seam is cheaper than a storage package
// that imports fleet transport.
type RetentionRow struct {
	EventID   string
	EmittedAt time.Time
}

const sqlSelectEventColumns = `event_id, session_id, emitter_id, kind, emitted_at,
	payload, payload_hash, prev_hash, redaction_summary, schema_version`

// scanner is the minimal Scan-only surface storage.Row and storage.Rows
// both satisfy — lets scanEventRow work against either.
type scanner interface {
	Scan(dest ...any) error
}

// scanEventRow decodes one `events` row. session_id is stored as the
// empty string for headless events (never SQL NULL — this backend
// controls both the write and read path, so there is no ambiguity to
// resolve; see AppendRow).
func scanEventRow(sc scanner) (Row, error) {
	var (
		r             Row
		emittedAtMs   int64
		payloadHash   []byte
		prevHash      []byte
		schemaVersion int
	)
	if err := sc.Scan(
		&r.EventID, &r.SessionID, &r.EmitterID, &r.Kind, &emittedAtMs,
		&r.Payload, &payloadHash, &prevHash, &r.RedactionSummary, &schemaVersion,
	); err != nil {
		return Row{}, err
	}
	r.EmittedAt = time.UnixMilli(emittedAtMs).UTC()
	copy(r.PayloadHash[:], payloadHash)
	copy(r.PrevHash[:], prevHash)
	r.SchemaVersion = schemaVersion
	return r, nil
}

// AppendRow implements Backend. Contract (store.go:39-43): insert the
// row AND update the session's chain head in one transaction, returning
// ErrChainHeadMismatch when expectedHead does not match the backend's
// cached head for the session — including the "no head yet" case,
// where any non-zero expectedHead is itself a mismatch.
func (b *SQLBackend) AppendRow(ctx context.Context, row Row, expectedHead [32]byte) error {
	return b.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		var curID string
		var curHashBytes []byte
		err := tx.QueryRow(ctx,
			"SELECT head_event_id, head_payload_hash FROM event_chain_heads WHERE session_id = ?",
			row.SessionID,
		).Scan(&curID, &curHashBytes)
		switch {
		case err == nil:
			var cur [32]byte
			copy(cur[:], curHashBytes)
			if cur != expectedHead {
				return fmt.Errorf("%w: session %s", ErrChainHeadMismatch, row.SessionID)
			}
		case errors.Is(err, sql.ErrNoRows):
			if expectedHead != ([32]byte{}) {
				return fmt.Errorf("%w: session %s expected zero head", ErrChainHeadMismatch, row.SessionID)
			}
		default:
			return fmt.Errorf("log: AppendRow: read head: %w", err)
		}

		// SchemaVersion is stored VERBATIM, including 0 — store.go's
		// "0 means legacy, treated as 1" is a READER-side interpretation
		// rule (schema.go's MigratePayload), not a write-time default.
		// Defaulting it here would silently diverge SQLBackend's
		// round-trip from MemoryBackend's (which stores 0 as-is), which
		// is exactly the kind of behavioural drift AC-003's differential
		// test exists to catch.
		if _, err := tx.Exec(ctx, `
			INSERT INTO events
				(event_id, session_id, emitter_id, kind, emitted_at,
				 payload, payload_hash, prev_hash, redaction_summary, schema_version)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			row.EventID, row.SessionID, row.EmitterID, row.Kind, row.EmittedAt.UnixMilli(),
			row.Payload, row.PayloadHash[:], row.PrevHash[:], row.RedactionSummary, row.SchemaVersion,
		); err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				return fmt.Errorf("log: event_id collision: %s: %w", row.EventID, err)
			}
			return fmt.Errorf("log: AppendRow: insert: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO event_chain_heads (session_id, head_event_id, head_payload_hash)
			VALUES (?, ?, ?)
			ON CONFLICT(session_id) DO UPDATE SET
				head_event_id = excluded.head_event_id,
				head_payload_hash = excluded.head_payload_hash
		`, row.SessionID, row.EventID, row.PayloadHash[:]); err != nil {
			return fmt.Errorf("log: AppendRow: update head: %w", err)
		}
		return nil
	})
}

// GetRow implements Backend.
func (b *SQLBackend) GetRow(ctx context.Context, eventID string) (Row, error) {
	row := b.db.Reader().QueryRow(ctx, "SELECT "+sqlSelectEventColumns+" FROM events WHERE event_id = ?", eventID)
	r, err := scanEventRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Row{}, ErrNotFound
		}
		return Row{}, fmt.Errorf("log: GetRow: %w", err)
	}
	return r, nil
}

// HeadFor implements Backend. Distinguishes "no events yet for this
// session" (false, nil) from an empty session id that DOES have a head
// (true) — the "" session (headless events) gets exactly the same
// treatment as any other session id (store.go:20, :47-48).
func (b *SQLBackend) HeadFor(ctx context.Context, sessionID string) ([32]byte, string, bool, error) {
	var headID string
	var hashBytes []byte
	err := b.db.Reader().QueryRow(ctx,
		"SELECT head_event_id, head_payload_hash FROM event_chain_heads WHERE session_id = ?", sessionID,
	).Scan(&headID, &hashBytes)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return [32]byte{}, "", false, nil
		}
		return [32]byte{}, "", false, fmt.Errorf("log: HeadFor: %w", err)
	}
	var hash [32]byte
	copy(hash[:], hashBytes)
	return hash, headID, true, nil
}

// selectRows is the shared query builder for the four SelectBy*
// methods and SearchFTS's non-empty-query companion. conds/args are
// ANDed together; the after-cursor and limit are applied identically
// to MemoryBackend.selectFiltered's semantics (store.go:235-265):
// event_id > after when ascending, event_id < after when descending
// (reverse), ordered accordingly, truncated to limit when limit > 0.
func (b *SQLBackend) selectRows(ctx context.Context, conds []string, args []any, after string, limit int, reverse bool) ([]Row, error) {
	allConds := append([]string{}, conds...)
	qargs := append([]any{}, args...)
	if after != "" {
		if reverse {
			allConds = append(allConds, "event_id < ?")
		} else {
			allConds = append(allConds, "event_id > ?")
		}
		qargs = append(qargs, after)
	}
	q := "SELECT " + sqlSelectEventColumns + " FROM events"
	if len(allConds) > 0 {
		q += " WHERE " + strings.Join(allConds, " AND ")
	}
	if reverse {
		q += " ORDER BY event_id DESC"
	} else {
		q += " ORDER BY event_id ASC"
	}
	if limit > 0 {
		q += " LIMIT ?"
		qargs = append(qargs, limit)
	}
	rows, err := b.db.Reader().Query(ctx, q, qargs...)
	if err != nil {
		return nil, fmt.Errorf("log: select: %w", err)
	}
	defer rows.Close()
	out := make([]Row, 0, 16)
	for rows.Next() {
		r, err := scanEventRow(rows)
		if err != nil {
			return nil, fmt.Errorf("log: select: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SelectBySession implements Backend.
func (b *SQLBackend) SelectBySession(ctx context.Context, sid string, after string, limit int, reverse bool) ([]Row, error) {
	return b.selectRows(ctx, []string{"session_id = ?"}, []any{sid}, after, limit, reverse)
}

// SelectByKind implements Backend.
func (b *SQLBackend) SelectByKind(ctx context.Context, kind string, after string, limit int, reverse bool) ([]Row, error) {
	return b.selectRows(ctx, []string{"kind = ?"}, []any{kind}, after, limit, reverse)
}

// SelectByEmitter implements Backend.
func (b *SQLBackend) SelectByEmitter(ctx context.Context, emitter string, after string, limit int, reverse bool) ([]Row, error) {
	return b.selectRows(ctx, []string{"emitter_id = ?"}, []any{emitter}, after, limit, reverse)
}

// SelectByTimeRange implements Backend. A zero from/to bound is
// unrestricted on that side, matching MemoryBackend.SelectByTimeRange.
func (b *SQLBackend) SelectByTimeRange(ctx context.Context, from, to time.Time, after string, limit int, reverse bool) ([]Row, error) {
	var conds []string
	var args []any
	if !from.IsZero() {
		conds = append(conds, "emitted_at >= ?")
		args = append(args, from.UnixMilli())
	}
	if !to.IsZero() {
		conds = append(conds, "emitted_at <= ?")
		args = append(args, to.UnixMilli())
	}
	return b.selectRows(ctx, conds, args, after, limit, reverse)
}

// SearchFTS implements Backend against events_fts, the external-content
// FTS5 table 0100_events creates (content='events', content_rowid='rowid').
// External-content tables return rowids that must be joined back to
// events — done here via `events_fts.rowid = e.rowid` — and that join
// ERRORS for any rowid whose backing events row was deleted (spec §1.6,
// [RAN] proven: "fts5: missing row 1 from content table"). Until
// UNIT-8's delete/update triggers land (migration 106), nothing in this
// package calls DeleteRows and then SearchFTS in the same path, so this
// backend does not become the first thing to meet that — but any FUTURE
// caller that deletes rows and then searches WILL hit it. That is
// UNIT-8's fix, not this unit's; noted here so it is not rediscovered
// the hard way.
//
// query is quoted as a single FTS5 phrase (internal `"` doubled per
// FTS5 string-literal escaping) so arbitrary free text — including FTS5
// operator characters a caller did not intend as operators — is matched
// literally, mirroring MemoryBackend's plain substring semantics rather
// than exposing FTS5's query-operator grammar to callers.
func (b *SQLBackend) SearchFTS(ctx context.Context, query string, sessionFilter string, kindFilter []string, limit int) ([]Row, error) {
	var (
		q    string
		args []any
	)
	if query == "" {
		// MemoryBackend: strings.Contains(payload, "") is always true —
		// an empty query matches every row (subject to the other
		// filters). FTS5 has no well-defined "match everything" MATCH
		// argument, so this is a plain select over events instead of a
		// MATCH query.
		q = "SELECT " + prefixed("e", sqlSelectEventColumns) + " FROM events e WHERE 1=1"
	} else {
		ftsQuery := `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
		q = "SELECT " + prefixed("e", sqlSelectEventColumns) +
			" FROM events_fts JOIN events e ON e.rowid = events_fts.rowid WHERE events_fts MATCH ?"
		args = append(args, ftsQuery)
	}
	if sessionFilter != "" {
		q += " AND e.session_id = ?"
		args = append(args, sessionFilter)
	}
	if len(kindFilter) > 0 {
		placeholders := make([]string, len(kindFilter))
		for i, k := range kindFilter {
			placeholders[i] = "?"
			args = append(args, k)
		}
		q += " AND e.kind IN (" + strings.Join(placeholders, ",") + ")"
	}
	q += " ORDER BY e.event_id ASC"
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := b.db.Reader().Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("log: SearchFTS: %w", err)
	}
	defer rows.Close()
	out := make([]Row, 0, 16)
	for rows.Next() {
		r, err := scanEventRow(rows)
		if err != nil {
			return nil, fmt.Errorf("log: SearchFTS: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// prefixed rewrites a "col1, col2, ..." column list to "alias.col1,
// alias.col2, ...".
func prefixed(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// AllSessionIDs implements Backend. Includes "" (headless events) when
// present, matching MemoryBackend.
func (b *SQLBackend) AllSessionIDs(ctx context.Context) ([]string, error) {
	rows, err := b.db.Reader().Query(ctx, "SELECT DISTINCT session_id FROM events ORDER BY session_id ASC")
	if err != nil {
		return nil, fmt.Errorf("log: AllSessionIDs: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			return nil, fmt.Errorf("log: AllSessionIDs: scan: %w", err)
		}
		out = append(out, sid)
	}
	return out, rows.Err()
}

// SizeBytes implements Backend — an approximation of total payload
// bytes (SUM(LENGTH(payload))), matching MemoryBackend's running total.
func (b *SQLBackend) SizeBytes(ctx context.Context) (int64, error) {
	var total sql.NullInt64
	if err := b.db.Reader().QueryRow(ctx, "SELECT SUM(LENGTH(payload)) FROM events").Scan(&total); err != nil {
		return 0, fmt.Errorf("log: SizeBytes: %w", err)
	}
	return total.Int64, nil
}

// DeleteRows implements SweepableBackend. Callers MUST archive before
// calling this (retention.go's archive-before-delete invariant) — this
// method itself does not archive. It also does NOT touch events_fts:
// until UNIT-8's migration 106 lands, a deleted row's term stays
// matchable and a subsequent SearchFTS on it errors (spec §1.6, [RAN]).
// UNIT-3 wires the deletion primitive; UNIT-8 wires the sweep that
// calls it and the trigger fix that makes calling it safe. No caller in
// this unit invokes DeleteRows — it lands inert like the rest of the
// backend.
func (b *SQLBackend) DeleteRows(ctx context.Context, eventIDs []string) error {
	if len(eventIDs) == 0 {
		return nil
	}
	return b.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		placeholders := make([]string, len(eventIDs))
		args := make([]any, len(eventIDs))
		for i, id := range eventIDs {
			placeholders[i] = "?"
			args[i] = id
		}
		_, err := tx.Exec(ctx, "DELETE FROM events WHERE event_id IN ("+strings.Join(placeholders, ",")+")", args...)
		if err != nil {
			return fmt.Errorf("log: DeleteRows: %w", err)
		}
		return nil
	})
}

// SelectBefore returns rows older than cutoff — the query a retention
// sweep needs. Its shape differs from SelectByTimeRange's, so it is not
// on Backend (spec R-6).
//
// NOT WIRED, and saying so rather than implying otherwise: core/rpc/api.go
// constructs NewAuditRetentionSweeper with Backend unset, so SweepOnce
// returns 0 rows and this method has no production caller. Connecting it
// is UNIT-6/UNIT-7's job, and that work must add an adapter in
// core/rpc/views/settings/ (the only package permitted to import fleet)
// converting []RetentionRow -> []fleet.AuditRetentionRow.
func (b *SQLBackend) SelectBefore(ctx context.Context, cutoff time.Time, limit int) ([]RetentionRow, error) {
	q := "SELECT event_id, emitted_at FROM events WHERE emitted_at < ? ORDER BY event_id ASC"
	args := []any{cutoff.UnixMilli()}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := b.db.Reader().Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("log: SelectBefore: %w", err)
	}
	defer rows.Close()
	var out []RetentionRow
	for rows.Next() {
		var id string
		var ms int64
		if err := rows.Scan(&id, &ms); err != nil {
			return nil, fmt.Errorf("log: SelectBefore: scan: %w", err)
		}
		out = append(out, RetentionRow{EventID: id, EmittedAt: time.UnixMilli(ms).UTC()})
	}
	return out, rows.Err()
}
