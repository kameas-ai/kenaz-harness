package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/autonomy"
	"github.com/sigil-tech/kaneaz-harness/core/storage"
)

// sqlStore persists projects through a storage.DB-shaped connection.
type sqlStore struct {
	db storage.DB
}

// NewSQLStore constructs a SQL-backed Store against the unified
// storage.DB.
func NewSQLStore(db storage.DB) Store {
	return &sqlStore{db: db}
}

func (s *sqlStore) Create(ctx context.Context, p Project) error {
	if p.Name == "" {
		return ErrNameRequired
	}
	level, overrides, err := encodeAutonomySQL(autonomyLayerFromProject(p))
	if err != nil {
		return err
	}
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, `
            INSERT INTO projects
                (id, name, description, created_at, updated_at,
                 autonomy_level, autonomy_overrides)
            VALUES (?, ?, ?, ?, ?, ?, ?)
        `,
			p.ID,
			p.Name,
			p.Description,
			p.CreatedAt.UnixNano(),
			p.UpdatedAt.UnixNano(),
			level,
			overrides,
		)
		return err
	})
}

const sqlSelectProject = `
    SELECT id, name, description, created_at, updated_at,
           autonomy_level, autonomy_overrides
    FROM projects
`

func (s *sqlStore) Get(ctx context.Context, id string) (Project, error) {
	row := s.db.Reader().QueryRow(ctx, sqlSelectProject+" WHERE id = ?", id)
	p, err := scanProject(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Project{}, ErrNotFound
		}
		return Project{}, err
	}
	return p, nil
}

func (s *sqlStore) List(ctx context.Context) ([]Project, error) {
	rows, err := s.db.Reader().Query(ctx,
		sqlSelectProject+" ORDER BY created_at ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *sqlStore) Rename(ctx context.Context, id, name string, now time.Time) error {
	if name == "" {
		return ErrNameRequired
	}
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		res, err := tx.Exec(ctx,
			"UPDATE projects SET name = ?, updated_at = ? WHERE id = ?",
			name, now.UnixNano(), id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

func (s *sqlStore) UpdateDescription(ctx context.Context, id, description string, now time.Time) error {
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		res, err := tx.Exec(ctx,
			"UPDATE projects SET description = ?, updated_at = ? WHERE id = ?",
			description, now.UnixNano(), id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

func (s *sqlStore) SetAutonomyProfile(ctx context.Context, id string, layer autonomy.Layer) error {
	level, overrides, err := encodeAutonomySQL(layer)
	if err != nil {
		return err
	}
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		res, err := tx.Exec(ctx,
			"UPDATE projects SET autonomy_level = ?, autonomy_overrides = ? WHERE id = ?",
			level, overrides, id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

func (s *sqlStore) GetAutonomyProfile(ctx context.Context, id string) (autonomy.Layer, error) {
	row := s.db.Reader().QueryRow(ctx,
		"SELECT autonomy_level, autonomy_overrides FROM projects WHERE id = ?", id)
	var (
		level     sql.NullInt64
		overrides sql.NullString
	)
	if err := row.Scan(&level, &overrides); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return autonomy.Layer{}, ErrNotFound
		}
		return autonomy.Layer{}, err
	}
	return decodeAutonomySQL(level, overrides)
}

func (s *sqlStore) Delete(ctx context.Context, id string) error {
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		res, err := tx.Exec(ctx, "DELETE FROM projects WHERE id = ?", id)
		if err != nil {
			return err
		}
		return rowsAffectedOrNotFound(res)
	})
}

func scanProject(sc interface{ Scan(dest ...any) error }) (Project, error) {
	var (
		p                 Project
		createdAt         int64
		updatedAt         int64
		autonomyLevel     sql.NullInt64
		autonomyOverrides sql.NullString
	)
	if err := sc.Scan(&p.ID, &p.Name, &p.Description, &createdAt, &updatedAt,
		&autonomyLevel, &autonomyOverrides); err != nil {
		return Project{}, err
	}
	p.CreatedAt = time.Unix(0, createdAt).UTC()
	p.UpdatedAt = time.Unix(0, updatedAt).UTC()
	if autonomyLevel.Valid {
		t := autonomy.Tier(int(autonomyLevel.Int64))
		p.AutonomyLevel = &t
	}
	if autonomyOverrides.Valid && autonomyOverrides.String != "" {
		ov, err := decodeAutonomyOverrides(autonomyOverrides.String)
		if err != nil {
			return Project{}, fmt.Errorf("projects: decode autonomy_overrides: %w", err)
		}
		p.AutonomyOverrides = ov
	}
	return p, nil
}

func rowsAffectedOrNotFound(res storage.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
