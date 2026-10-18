package labels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PushRow is one advice_labels row as the push lane reads it: the stored
// Row plus the two fields the frozen ingest contract keys on (design
// Amendment A3.3): TS (created_at, epoch ms) and Revision (the
// table-global monotonic change counter migration 1601 adds — see
// sqlAdviceLabelsRevision for why it is global rather than per-row).
type PushRow struct {
	Row
	TS       int64
	Revision int64
}

// PushCursor is the durable ack position for one (sink, kind): every row
// of that kind with Revision <= Revision has been acknowledged by the
// sink. TS is the created_at of the acked row, carried because the wire
// ack is over (ts, revision); ordering itself is by Revision alone
// (total, since Revision is table-global).
type PushCursor struct {
	TS       int64
	Revision int64
}

// PushSource is the read side of the label push lane — implemented by
// *SQLStore, consumed by core/mlsidecar's LabelPusher. It lives here
// (next to the schema it reads) so mlsidecar never issues SQL against
// advice_labels itself.
type PushSource interface {
	// PushKinds lists every kind that has at least one row.
	PushKinds(ctx context.Context) ([]string, error)
	// PendingSince returns up to limit rows of kind with
	// Revision > afterRevision, ascending by Revision — new rows AND rows
	// whose user_action changed since they were last pushed.
	PendingSince(ctx context.Context, kind string, afterRevision int64, limit int) ([]PushRow, error)
	// LoadCursor returns the durable cursor for (sink, kind); the zero
	// cursor when none exists (a full push from zero).
	LoadCursor(ctx context.Context, sink, kind string) (PushCursor, error)
	// SaveCursor durably records c for (sink, kind).
	SaveCursor(ctx context.Context, sink, kind string, c PushCursor) error
	// ResetCursors forgets every cursor of sink, so the next push
	// re-sends everything from zero (mirror loss recovery: the harness DB
	// is the source of truth, the sink's copy is rebuildable).
	ResetCursors(ctx context.Context, sink string) error
}

var _ PushSource = (*SQLStore)(nil)

// PushKinds implements PushSource.
func (s *SQLStore) PushKinds(ctx context.Context) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("labels: nil store")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT kind FROM advice_labels ORDER BY kind`)
	if err != nil {
		return nil, fmt.Errorf("labels: push kinds: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("labels: push kinds scan: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// PendingSince implements PushSource.
func (s *SQLStore) PendingSince(ctx context.Context, kind string, afterRevision int64, limit int) ([]PushRow, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("labels: nil store")
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind, prompt_version, features_hash, features_json, features_complete, model_id, rung,
		       decision, confidence, shown, user_action, latency_ms, session_id, created_at, revision
		FROM advice_labels
		WHERE kind = ? AND revision > ?
		ORDER BY revision ASC
		LIMIT ?`, kind, afterRevision, limit)
	if err != nil {
		return nil, fmt.Errorf("labels: pending since: %w", err)
	}
	defer rows.Close()
	var out []PushRow
	for rows.Next() {
		var (
			r                         PushRow
			complete, decision, shown int
			action                    string
			createdMS                 int64
		)
		if err := rows.Scan(&r.KindID, &r.PromptVersion, &r.FeaturesHash, &r.FeaturesJSON, &complete, &r.ModelID, &r.Rung,
			&decision, &r.Confidence, &shown, &action, &r.LatencyMS, &r.SessionID, &createdMS, &r.Revision); err != nil {
			return nil, fmt.Errorf("labels: pending since scan: %w", err)
		}
		r.FeaturesComplete = complete != 0
		r.Decision = decision != 0
		r.Shown = shown != 0
		r.UserAction = UserAction(action)
		r.TS = createdMS
		r.CreatedAt = time.UnixMilli(createdMS).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// LoadCursor implements PushSource.
func (s *SQLStore) LoadCursor(ctx context.Context, sink, kind string) (PushCursor, error) {
	if s == nil || s.db == nil {
		return PushCursor{}, fmt.Errorf("labels: nil store")
	}
	var c PushCursor
	err := s.db.QueryRowContext(ctx,
		`SELECT cursor_ts, cursor_revision FROM advice_label_push_cursor WHERE sink = ? AND kind = ?`,
		sink, kind).Scan(&c.TS, &c.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return PushCursor{}, nil
	}
	if err != nil {
		return PushCursor{}, fmt.Errorf("labels: load cursor: %w", err)
	}
	return c, nil
}

// SaveCursor implements PushSource.
func (s *SQLStore) SaveCursor(ctx context.Context, sink, kind string, c PushCursor) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("labels: nil store")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO advice_label_push_cursor (sink, kind, cursor_ts, cursor_revision, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(sink, kind) DO UPDATE SET
		    cursor_ts = excluded.cursor_ts,
		    cursor_revision = excluded.cursor_revision,
		    updated_at = excluded.updated_at`,
		sink, kind, c.TS, c.Revision, time.Now().UTC().UnixMilli())
	if err != nil {
		return fmt.Errorf("labels: save cursor: %w", err)
	}
	return nil
}

// ResetCursors implements PushSource.
func (s *SQLStore) ResetCursors(ctx context.Context, sink string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("labels: nil store")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM advice_label_push_cursor WHERE sink = ?`, sink); err != nil {
		return fmt.Errorf("labels: reset cursors: %w", err)
	}
	return nil
}
