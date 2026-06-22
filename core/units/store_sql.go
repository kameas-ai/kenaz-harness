package units

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/storage"
)

// sqlStore persists units and edges through a storage.DB-shaped
// connection. Concurrency: storage.DB serialises writes through its
// single-writer queue.
type sqlStore struct {
	db    storage.DB
	now   func() time.Time
	idGen func() (string, error)
}

// SQLStoreOption configures a sqlStore at construction time.
type SQLStoreOption func(*sqlStore)

// WithSQLClock overrides the wall-clock source. Tests pin this for
// deterministic timestamps.
func WithSQLClock(now func() time.Time) SQLStoreOption {
	return func(s *sqlStore) {
		if now != nil {
			s.now = now
		}
	}
}

// WithSQLIDGen overrides the id generator.
func WithSQLIDGen(gen func() (string, error)) SQLStoreOption {
	return func(s *sqlStore) {
		if gen != nil {
			s.idGen = gen
		}
	}
}

// NewSQLStore constructs a SQL-backed Store against the unified
// storage.DB.
func NewSQLStore(db storage.DB, opts ...SQLStoreOption) Store {
	s := &sqlStore{
		db:    db,
		now:   func() time.Time { return time.Now().UTC() },
		idGen: defaultUnitID,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ── Create ─────────────────────────────────────────────────────────────

func (s *sqlStore) Create(ctx context.Context, u Unit) (Unit, error) {
	if err := validateUnit(u); err != nil {
		return Unit{}, err
	}
	if u.ID == "" {
		id, err := s.idGen()
		if err != nil {
			return Unit{}, fmt.Errorf("units: id gen: %w", err)
		}
		u.ID = id
	}
	now := s.now()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	u.UpdatedAt = u.CreatedAt
	u.Version = 0
	u.Metadata = normaliseMetadata(u.Metadata)

	if err := s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, `
            INSERT INTO units
                (id, kind, scope, scope_id, classification, version,
                 load_policy, title, body, metadata, created_at, updated_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        `,
			u.ID, string(u.Kind), string(u.Scope), u.ScopeID,
			string(u.Classification), u.Version, string(u.LoadPolicy),
			u.Title, u.Body, string(u.Metadata),
			u.CreatedAt.UnixNano(), u.UpdatedAt.UnixNano(),
		)
		return err
	}); err != nil {
		return Unit{}, err
	}
	return u, nil
}

// ── Get ────────────────────────────────────────────────────────────────

const sqlSelectUnit = `
    SELECT id, kind, scope, scope_id, classification, version,
           load_policy, title, body, metadata, created_at, updated_at
    FROM units
`

func (s *sqlStore) Get(ctx context.Context, id string) (Unit, error) {
	row := s.db.Reader().QueryRow(ctx, sqlSelectUnit+" WHERE id = ?", id)
	u, err := scanUnit(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Unit{}, ErrUnitNotFound
		}
		return Unit{}, err
	}
	return u, nil
}

// ── List ───────────────────────────────────────────────────────────────

func (s *sqlStore) List(ctx context.Context, filter UnitFilter) ([]Unit, error) {
	q := sqlSelectUnit
	args := make([]any, 0, 5)
	conds := make([]string, 0, 5)

	if filter.Scope != "" {
		conds = append(conds, "scope = ?")
		args = append(args, string(filter.Scope))
	}
	if filter.ScopeID != "" {
		conds = append(conds, "scope_id = ?")
		args = append(args, filter.ScopeID)
	}
	if filter.Kind != "" {
		conds = append(conds, "kind = ?")
		args = append(args, string(filter.Kind))
	}
	if filter.Classification != "" {
		conds = append(conds, "classification = ?")
		args = append(args, string(filter.Classification))
	}
	if filter.LoadPolicy != "" {
		conds = append(conds, "load_policy = ?")
		args = append(args, string(filter.LoadPolicy))
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY created_at DESC"

	rows, err := s.db.Reader().Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Unit
	for rows.Next() {
		u, err := scanUnit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ── Update ─────────────────────────────────────────────────────────────

func (s *sqlStore) Update(ctx context.Context, id, body string, metadata []byte) (Unit, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return Unit{}, err
	}

	meta := normaliseMetadata(metadata)
	now := s.now()
	newVersion := current.Version + 1

	if err := s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		// Append history row first.
		_, err := tx.Exec(ctx, `
            INSERT INTO unit_versions
                (unit_id, version, body, metadata, created_at)
            VALUES (?, ?, ?, ?, ?)
        `,
			id, newVersion, body, string(meta), now.UnixNano(),
		)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				return ErrVersionConflict
			}
			return fmt.Errorf("units: Update: insert version: %w", err)
		}

		// Mutate the units row.
		res, err := tx.Exec(ctx, `
            UPDATE units
            SET version = ?, body = ?, metadata = ?, updated_at = ?
            WHERE id = ?
        `, newVersion, body, string(meta), now.UnixNano(), id)
		if err != nil {
			return fmt.Errorf("units: Update: update row: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrUnitNotFound
		}
		return nil
	}); err != nil {
		return Unit{}, err
	}

	current.Version = newVersion
	current.Body = body
	current.Metadata = meta
	current.UpdatedAt = now
	return current, nil
}

// ── AddEdge ────────────────────────────────────────────────────────────

func (s *sqlStore) AddEdge(ctx context.Context, e Edge) (Edge, error) {
	if !validEdgeKind(e.Kind) {
		return Edge{}, fmt.Errorf("%w: %q", ErrUnsupportedEdgeKind, e.Kind)
	}
	if e.FromID == "" || e.ToID == "" {
		return Edge{}, fmt.Errorf("units: AddEdge: FromID and ToID are required")
	}
	// Verify both units exist.
	if _, err := s.Get(ctx, e.FromID); err != nil {
		return Edge{}, fmt.Errorf("units: AddEdge: from unit: %w", err)
	}
	if _, err := s.Get(ctx, e.ToID); err != nil {
		return Edge{}, fmt.Errorf("units: AddEdge: to unit: %w", err)
	}
	if e.ID == "" {
		id, err := s.idGen()
		if err != nil {
			return Edge{}, fmt.Errorf("units: edge id gen: %w", err)
		}
		e.ID = id
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = s.now()
	}
	e.Version = 1

	if err := s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, `
            INSERT INTO unit_edges
                (id, from_id, to_id, kind, version, created_at)
            VALUES (?, ?, ?, ?, ?, ?)
        `, e.ID, e.FromID, e.ToID, string(e.Kind), e.Version, e.CreatedAt.UnixNano())
		return err
	}); err != nil {
		return Edge{}, err
	}
	return e, nil
}

// ── ListEdges ──────────────────────────────────────────────────────────

func (s *sqlStore) ListEdges(ctx context.Context, unitID string) ([]Edge, error) {
	rows, err := s.db.Reader().Query(ctx, `
        SELECT id, from_id, to_id, kind, version, created_at
        FROM unit_edges
        WHERE from_id = ? OR to_id = ?
        ORDER BY created_at ASC
    `, unitID, unitID)
	if err != nil {
		return nil, fmt.Errorf("units: ListEdges: query: %w", err)
	}
	defer rows.Close()
	var out []Edge
	for rows.Next() {
		e, err := scanEdge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ── ListVersions ───────────────────────────────────────────────────────

func (s *sqlStore) ListVersions(ctx context.Context, unitID string) ([]UnitVersion, error) {
	rows, err := s.db.Reader().Query(ctx, `
        SELECT id, unit_id, version, body, metadata, created_at
        FROM unit_versions
        WHERE unit_id = ?
        ORDER BY version ASC
    `, unitID)
	if err != nil {
		return nil, fmt.Errorf("units: ListVersions: query: %w", err)
	}
	defer rows.Close()
	var out []UnitVersion
	for rows.Next() {
		v, err := scanUnitVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ── Scan helpers ───────────────────────────────────────────────────────

func scanUnit(sc interface{ Scan(dest ...any) error }) (Unit, error) {
	var (
		u           Unit
		kind        string
		scope       string
		class       string
		loadPolicy  string
		metaStr     string
		createdAtNs int64
		updatedAtNs int64
	)
	if err := sc.Scan(
		&u.ID, &kind, &scope, &u.ScopeID, &class, &u.Version,
		&loadPolicy, &u.Title, &u.Body, &metaStr,
		&createdAtNs, &updatedAtNs,
	); err != nil {
		return Unit{}, err
	}
	u.Kind = Kind(kind)
	u.Scope = Scope(scope)
	u.Classification = Classification(class)
	u.LoadPolicy = LoadPolicy(loadPolicy)
	if metaStr != "" {
		u.Metadata = json.RawMessage(metaStr)
	} else {
		u.Metadata = json.RawMessage("{}")
	}
	u.CreatedAt = time.Unix(0, createdAtNs).UTC()
	u.UpdatedAt = time.Unix(0, updatedAtNs).UTC()
	return u, nil
}

func scanEdge(sc interface{ Scan(dest ...any) error }) (Edge, error) {
	var (
		e           Edge
		kind        string
		createdAtNs int64
	)
	if err := sc.Scan(
		&e.ID, &e.FromID, &e.ToID, &kind, &e.Version, &createdAtNs,
	); err != nil {
		return Edge{}, fmt.Errorf("units: scan edge: %w", err)
	}
	e.Kind = EdgeKind(kind)
	e.CreatedAt = time.Unix(0, createdAtNs).UTC()
	return e, nil
}

func scanUnitVersion(sc interface{ Scan(dest ...any) error }) (UnitVersion, error) {
	var (
		v           UnitVersion
		metaStr     string
		createdAtNs int64
	)
	if err := sc.Scan(
		&v.ID, &v.UnitID, &v.Version, &v.Body, &metaStr, &createdAtNs,
	); err != nil {
		return UnitVersion{}, fmt.Errorf("units: scan version: %w", err)
	}
	if metaStr != "" {
		v.Metadata = json.RawMessage(metaStr)
	} else {
		v.Metadata = json.RawMessage("{}")
	}
	v.CreatedAt = time.Unix(0, createdAtNs).UTC()
	return v, nil
}

// ── Validation ─────────────────────────────────────────────────────────

func validateUnit(u Unit) error {
	if !validKind(u.Kind) {
		return fmt.Errorf("%w: %q", ErrUnsupportedKind, u.Kind)
	}
	if !validScope(u.Scope) {
		return fmt.Errorf("%w: %q", ErrUnsupportedScope, u.Scope)
	}
	if !validClassification(u.Classification) {
		return fmt.Errorf("%w: %q", ErrUnsupportedClassification, u.Classification)
	}
	if !validLoadPolicy(u.LoadPolicy) {
		return fmt.Errorf("%w: %q", ErrUnsupportedLoadPolicy, u.LoadPolicy)
	}
	return nil
}
