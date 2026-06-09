package attachments

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/storage"
)

// Store is the persistence contract Manager consumes. Two
// implementations ship in this package: an in-memory store
// (NewMemoryStore) and a SQL-backed store (NewSQLStore) that targets
// the unified storage.DB.
//
// All methods are safe for concurrent use.
type Store interface {
	Add(ctx context.Context, att Attachment) error
	Get(ctx context.Context, id string) (Attachment, error)
	List(ctx context.Context, filter ScopeFilter) ([]Attachment, error)
	Remove(ctx context.Context, id string) error
	Reorder(ctx context.Context, scopeKind, scopeID string, idsInOrder []string) error
	UpdateContent(ctx context.Context, id, content string) error
}

// memStore is the in-memory Store implementation.
type memStore struct {
	mu  sync.RWMutex
	all map[string]Attachment
}

// NewMemoryStore returns an in-memory Store. Useful for tests.
func NewMemoryStore() Store {
	return &memStore{all: map[string]Attachment{}}
}

func (s *memStore) Add(_ context.Context, att Attachment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.all[att.ID]; ok {
		return fmt.Errorf("attachments: duplicate id %q", att.ID)
	}
	s.all[att.ID] = att
	return nil
}

func (s *memStore) Get(_ context.Context, id string) (Attachment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.all[id]
	if !ok {
		return Attachment{}, ErrNotFound
	}
	return a, nil
}

func (s *memStore) List(_ context.Context, filter ScopeFilter) ([]Attachment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Attachment, 0, len(s.all))
	for _, a := range s.all {
		if filter.ScopeKind != "" && a.ScopeKind != filter.ScopeKind {
			continue
		}
		if filter.ScopeID != "" && a.ScopeID != filter.ScopeID {
			continue
		}
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Position != out[j].Position {
			return out[i].Position < out[j].Position
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (s *memStore) Remove(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.all[id]; !ok {
		return ErrNotFound
	}
	delete(s.all, id)
	return nil
}

func (s *memStore) Reorder(_ context.Context, scopeKind, scopeID string, idsInOrder []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	have := map[string]Attachment{}
	for _, a := range s.all {
		if a.ScopeKind == scopeKind && a.ScopeID == scopeID {
			have[a.ID] = a
		}
	}
	if len(have) != len(idsInOrder) {
		return fmt.Errorf("attachments: reorder expected %d ids, got %d", len(have), len(idsInOrder))
	}
	for _, id := range idsInOrder {
		if _, ok := have[id]; !ok {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
	}
	for i, id := range idsInOrder {
		a := have[id]
		a.Position = i
		s.all[id] = a
	}
	return nil
}

func (s *memStore) UpdateContent(_ context.Context, id, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.all[id]
	if !ok {
		return ErrNotFound
	}
	a.Content = content
	s.all[id] = a
	return nil
}

// ── SQL-backed implementation ──────────────────────────────────────────

type sqlStore struct {
	db storage.DB
}

// NewSQLStore returns a SQL-backed Store rooted in the unified
// storage.DB. Migration 0301 creates the context_attachments table this
// store reads from.
func NewSQLStore(db storage.DB) Store {
	return &sqlStore{db: db}
}

func (s *sqlStore) Add(ctx context.Context, att Attachment) error {
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		var mediaID any
		if att.MediaID != nil {
			mediaID = *att.MediaID
		}
		_, err := tx.Exec(ctx, `
            INSERT INTO context_attachments
                (id, scope_kind, scope_id, content_source, content,
                 kind, position, created_at, media_id)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
        `,
			att.ID,
			att.ScopeKind,
			att.ScopeID,
			att.ContentSource,
			att.Content,
			att.Kind,
			att.Position,
			att.CreatedAt.UnixNano(),
			mediaID,
		)
		return err
	})
}

const sqlSelectAttachment = `
    SELECT id, scope_kind, scope_id, content_source, content,
           kind, position, created_at, media_id
    FROM context_attachments
`

func (s *sqlStore) Get(ctx context.Context, id string) (Attachment, error) {
	row := s.db.Reader().QueryRow(ctx, sqlSelectAttachment+" WHERE id = ?", id)
	a, err := scanAttachment(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Attachment{}, ErrNotFound
		}
		return Attachment{}, err
	}
	return a, nil
}

func (s *sqlStore) List(ctx context.Context, filter ScopeFilter) ([]Attachment, error) {
	q := sqlSelectAttachment
	args := make([]any, 0, 2)
	conditions := make([]string, 0, 2)
	if filter.ScopeKind != "" {
		conditions = append(conditions, "scope_kind = ?")
		args = append(args, filter.ScopeKind)
	}
	if filter.ScopeID != "" {
		conditions = append(conditions, "scope_id = ?")
		args = append(args, filter.ScopeID)
	}
	if len(conditions) > 0 {
		q += " WHERE "
		for i, c := range conditions {
			if i > 0 {
				q += " AND "
			}
			q += c
		}
	}
	q += " ORDER BY position ASC, created_at ASC"
	rows, err := s.db.Reader().Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *sqlStore) Remove(ctx context.Context, id string) error {
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		res, err := tx.Exec(ctx, "DELETE FROM context_attachments WHERE id = ?", id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func (s *sqlStore) Reorder(ctx context.Context, scopeKind, scopeID string, idsInOrder []string) error {
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		// Validate every id belongs to the scope and the slice is a
		// complete permutation.
		rows, err := tx.Query(ctx,
			"SELECT id FROM context_attachments WHERE scope_kind = ? AND scope_id = ?",
			scopeKind, scopeID)
		if err != nil {
			return err
		}
		have := map[string]bool{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			have[id] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(have) != len(idsInOrder) {
			return fmt.Errorf("attachments: reorder expected %d ids, got %d", len(have), len(idsInOrder))
		}
		for _, id := range idsInOrder {
			if !have[id] {
				return fmt.Errorf("%w: %s", ErrNotFound, id)
			}
		}
		for i, id := range idsInOrder {
			if _, err := tx.Exec(ctx,
				"UPDATE context_attachments SET position = ? WHERE id = ?",
				i, id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *sqlStore) UpdateContent(ctx context.Context, id, content string) error {
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		res, err := tx.Exec(ctx,
			"UPDATE context_attachments SET content = ? WHERE id = ?",
			content, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func scanAttachment(sc interface{ Scan(dest ...any) error }) (Attachment, error) {
	var (
		a         Attachment
		createdAt int64
		mediaID   sql.NullString
	)
	if err := sc.Scan(
		&a.ID, &a.ScopeKind, &a.ScopeID, &a.ContentSource, &a.Content,
		&a.Kind, &a.Position, &createdAt, &mediaID,
	); err != nil {
		return Attachment{}, err
	}
	a.CreatedAt = time.Unix(0, createdAt).UTC()
	if mediaID.Valid && mediaID.String != "" {
		v := mediaID.String
		a.MediaID = &v
	}
	return a, nil
}
