package log

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/storage"
)

// SavedQueryStore persists SavedQuery rows in the saved_audit_queries
// table (migration event-log/0105-saved-audit-queries). Separate from
// Store/Backend deliberately: saved queries are an unrelated table to
// events, and the audit view's ListSavedQueries/SaveQuery/DeleteQuery
// (audit-that-tells-the-truth-01PMZA10 UNIT-6) never touched it —
// `a.savedQueries map[string]eventlog.SavedQuery` (views/audit/impl.go)
// was in-memory only, so a two-kind saved query that survived a
// load→save round trip inside one process vanished on the next
// restart. This is the real persistence layer that closes that gap.
type SavedQueryStore struct {
	db storage.DB
}

// NewSavedQueryStore wraps db. The caller must have already applied
// event-log's migrations (true by construction whenever db came from
// core/storage/sqlite.Open, same precondition as NewSQLBackend).
func NewSavedQueryStore(db storage.DB) *SavedQueryStore {
	return &SavedQueryStore{db: db}
}

// List returns every persisted saved query, oldest first.
func (s *SavedQueryStore) List(ctx context.Context) ([]SavedQuery, error) {
	rows, err := s.db.Reader().Query(ctx,
		"SELECT id, name, query_json, created_at, user_id FROM saved_audit_queries ORDER BY created_at ASC")
	if err != nil {
		return nil, fmt.Errorf("log: SavedQueryStore.List: %w", err)
	}
	defer rows.Close()
	var out []SavedQuery
	for rows.Next() {
		var id, name, queryJSON, userID string
		var createdMs int64
		if err := rows.Scan(&id, &name, &queryJSON, &createdMs, &userID); err != nil {
			return nil, fmt.Errorf("log: SavedQueryStore.List: scan: %w", err)
		}
		var q FilterQuery
		if jsonErr := json.Unmarshal([]byte(queryJSON), &q); jsonErr != nil {
			// A corrupt single row must not take down the whole list —
			// skip it rather than erroring every caller. There is no
			// production write path that could produce this (Save
			// below always marshals FilterQuery itself), so this is a
			// defensive fallback, not an expected case.
			continue
		}
		out = append(out, SavedQuery{
			ID:        id,
			Name:      name,
			Query:     q,
			CreatedAt: time.UnixMilli(createdMs).UTC(),
			UserID:    userID,
		})
	}
	return out, rows.Err()
}

// Save upserts q by ID. If q.CreatedAt is the zero value, now is used
// (mirrors the SaveQuery RPC's contract — callers do not set it).
func (s *SavedQueryStore) Save(ctx context.Context, q SavedQuery) error {
	if q.ID == "" {
		return fmt.Errorf("log: SavedQueryStore.Save: empty ID")
	}
	if q.Name == "" {
		return fmt.Errorf("log: SavedQueryStore.Save: empty Name")
	}
	raw, err := json.Marshal(q.Query)
	if err != nil {
		return fmt.Errorf("log: SavedQueryStore.Save: marshal query: %w", err)
	}
	createdAt := q.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO saved_audit_queries (id, name, query_json, created_at, user_id)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO UPDATE SET
				name = excluded.name,
				query_json = excluded.query_json,
				user_id = excluded.user_id`,
			q.ID, q.Name, string(raw), createdAt.UnixMilli(), q.UserID)
		if err != nil {
			return fmt.Errorf("log: SavedQueryStore.Save: %w", err)
		}
		return nil
	})
}

// Delete removes a saved query by ID. No-op if the ID is unknown.
func (s *SavedQueryStore) Delete(ctx context.Context, id string) error {
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, "DELETE FROM saved_audit_queries WHERE id = ?", id)
		if err != nil {
			return fmt.Errorf("log: SavedQueryStore.Delete: %w", err)
		}
		return nil
	})
}
