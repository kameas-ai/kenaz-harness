package workflows

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/storage"
)

// WorkflowSummary is the lightweight projection returned by Store.List.
// Use Store.Load to fetch the full Workflow including yaml_source.
type WorkflowSummary struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Version     int       `json:"version"`
	Hash        string    `json:"hash"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// WorkflowVersion is one row from the workflow_versions append-only
// history.
type WorkflowVersion struct {
	WorkflowID string    `json:"workflowId"`
	Version    int       `json:"version"`
	YAMLSource string    `json:"yamlSource"`
	Hash       string    `json:"hash"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Store is the persistence contract for user-defined workflows. The
// SQLite-backed implementation lives in this package; tests substitute
// in-memory fakes. The Save method increments Workflow.Version and
// writes a workflow_versions row when the canonical yaml_source hash
// changes; identical-hash saves are no-ops.
type Store interface {
	// Save persists w. The caller's w.YAMLSource is canonicalised via
	// MarshalYAML when empty (e.g. a freshly parsed Workflow with no
	// YAML round-trip yet); otherwise the supplied bytes are stored
	// as-is. Returns the persisted Workflow with Version, Hash, and
	// timestamps populated.
	Save(ctx context.Context, w Workflow) (Workflow, error)
	// Load returns the current canonical workflow record for id.
	// Returns ErrWorkflowNotFound when the id is unknown.
	Load(ctx context.Context, id string) (Workflow, error)
	// Delete removes the workflow row and cascades the
	// workflow_versions history.
	Delete(ctx context.Context, id string) error
	// List returns every workflow's summary, ordered by updated_at DESC.
	List(ctx context.Context) ([]WorkflowSummary, error)
	// History returns every recorded version for id, newest first.
	// Returns ErrWorkflowNotFound when the id is unknown.
	History(ctx context.Context, id string) ([]WorkflowVersion, error)
}

// NewSQLiteStore returns a Store backed by the workflows +
// workflow_versions tables landed by migration 0319. The supplied
// storage.DB must already be migrated to schema version 319 or later;
// callers typically pass the same DB they handed to session.NewStorageDB.
func NewSQLiteStore(db storage.DB) Store {
	return &sqliteStore{db: db, now: func() time.Time { return time.Now().UTC() }}
}

// sqliteStore is the production Store. Concurrent Save calls for the
// same id serialise behind the underlying storage.WriteTx; the per-id
// no-op-on-equal-hash check + version bump happen inside the same
// transaction so two writers never see a stale version.
type sqliteStore struct {
	db  storage.DB
	now func() time.Time
}

// Save implements Store.Save.
//
// Behaviour:
//   - If the workflow id has no row yet, inserts at version=1 and
//     records a workflow_versions(version=1) row.
//   - If a row exists and the supplied yaml_source hash matches the
//     current row's hash, the call is a no-op (returns the unchanged
//     stored Workflow).
//   - Otherwise, increments version by 1, rewrites the workflows row
//     with the new yaml_source/hash/updated_at, and appends a new
//     workflow_versions row at the new version.
//
// Validation: the supplied workflow must pass Validate. If
// w.YAMLSource is empty, Save canonicalises via MarshalYAML.
func (s *sqliteStore) Save(ctx context.Context, w Workflow) (Workflow, error) {
	if err := Validate(w); err != nil {
		return Workflow{}, err
	}
	if w.ID == "" {
		return Workflow{}, fmt.Errorf("%w: empty", ErrInvalidID)
	}

	// Resolve canonical yaml_source. If the caller didn't pre-marshal
	// (common path for callers that just parsed and bumped a field),
	// run MarshalYAML so the stored payload is the round-trip canonical
	// form.
	var yamlBytes []byte
	if w.yamlSource != "" {
		yamlBytes = []byte(w.yamlSource)
	} else {
		b, err := MarshalYAML(w)
		if err != nil {
			return Workflow{}, fmt.Errorf("workflows: marshal yaml: %w", err)
		}
		yamlBytes = b
	}
	hash := hashYAML(yamlBytes)

	var out Workflow
	err := s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		// Look up current row, if any.
		var (
			curHash    string
			curVersion int
			curCreated int64
		)
		row := tx.QueryRow(ctx,
			`SELECT hash, version, created_at FROM workflows WHERE id = ?`, w.ID)
		err := row.Scan(&curHash, &curVersion, &curCreated)
		switch {
		case err == nil:
			// Existing row.
			if curHash == hash {
				// No-op: load and return the stored record.
				out = w
				out.Version = curVersion
				out.yamlSource = string(yamlBytes)
				out.hash = curHash
				out.createdAt = time.Unix(0, curCreated).UTC()
				// Re-read updated_at so callers see the canonical value.
				var updated int64
				if err := tx.QueryRow(ctx,
					`SELECT updated_at FROM workflows WHERE id = ?`, w.ID).
					Scan(&updated); err != nil {
					return err
				}
				out.updatedAt = time.Unix(0, updated).UTC()
				return nil
			}
			// Hash changed: bump version + rewrite + append history.
			newVersion := curVersion + 1
			now := s.now()
			if _, err := tx.Exec(ctx,
				`UPDATE workflows
				   SET name = ?, description = ?, yaml_source = ?, version = ?, hash = ?, updated_at = ?
				 WHERE id = ?`,
				w.Name, w.Description, string(yamlBytes), newVersion, hash, now.UnixNano(), w.ID,
			); err != nil {
				return fmt.Errorf("workflows: update: %w", err)
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO workflow_versions (workflow_id, version, yaml_source, hash, created_at)
				 VALUES (?, ?, ?, ?, ?)`,
				w.ID, newVersion, string(yamlBytes), hash, now.UnixNano(),
			); err != nil {
				return fmt.Errorf("workflows: insert version: %w", err)
			}
			out = w
			out.Version = newVersion
			out.yamlSource = string(yamlBytes)
			out.hash = hash
			out.createdAt = time.Unix(0, curCreated).UTC()
			out.updatedAt = now
			return nil

		case isNoRows(err):
			// New row at version=1.
			now := s.now()
			version := 1
			if _, err := tx.Exec(ctx,
				`INSERT INTO workflows
				   (id, name, description, yaml_source, version, hash, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				w.ID, w.Name, w.Description, string(yamlBytes), version, hash,
				now.UnixNano(), now.UnixNano(),
			); err != nil {
				return fmt.Errorf("workflows: insert: %w", err)
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO workflow_versions (workflow_id, version, yaml_source, hash, created_at)
				 VALUES (?, ?, ?, ?, ?)`,
				w.ID, version, string(yamlBytes), hash, now.UnixNano(),
			); err != nil {
				return fmt.Errorf("workflows: insert version: %w", err)
			}
			out = w
			out.Version = version
			out.yamlSource = string(yamlBytes)
			out.hash = hash
			out.createdAt = now
			out.updatedAt = now
			return nil

		default:
			return fmt.Errorf("workflows: lookup: %w", err)
		}
	})
	if err != nil {
		return Workflow{}, err
	}
	return out, nil
}

// Load implements Store.Load.
func (s *sqliteStore) Load(ctx context.Context, id string) (Workflow, error) {
	if id == "" {
		return Workflow{}, fmt.Errorf("%w: empty", ErrInvalidID)
	}
	r := s.db.Reader()
	var (
		yaml       string
		version    int
		hash       string
		created    int64
		updated    int64
	)
	row := r.QueryRow(ctx,
		`SELECT yaml_source, version, hash, created_at, updated_at
		   FROM workflows WHERE id = ?`, id)
	if err := row.Scan(&yaml, &version, &hash, &created, &updated); err != nil {
		if isNoRows(err) {
			return Workflow{}, fmt.Errorf("%w: %s", ErrWorkflowNotFound, id)
		}
		return Workflow{}, fmt.Errorf("workflows: load: %w", err)
	}
	w, err := LoadYAML([]byte(yaml))
	if err != nil {
		return Workflow{}, fmt.Errorf("workflows: parse stored yaml: %w", err)
	}
	w.Version = version
	w.yamlSource = yaml
	w.hash = hash
	w.createdAt = time.Unix(0, created).UTC()
	w.updatedAt = time.Unix(0, updated).UTC()
	return w, nil
}

// Delete implements Store.Delete. The workflow_versions rows cascade
// via the FK declared in migration 0319.
func (s *sqliteStore) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("%w: empty", ErrInvalidID)
	}
	return s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, `DELETE FROM workflows WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("workflows: delete: %w", err)
		}
		return nil
	})
}

// List implements Store.List.
func (s *sqliteStore) List(ctx context.Context) ([]WorkflowSummary, error) {
	rows, err := s.db.Reader().Query(ctx,
		`SELECT id, name, description, version, hash, created_at, updated_at
		   FROM workflows ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("workflows: list: %w", err)
	}
	defer rows.Close()
	var out []WorkflowSummary
	for rows.Next() {
		var (
			s WorkflowSummary
			c int64
			u int64
		)
		if err := rows.Scan(&s.ID, &s.Name, &s.Description, &s.Version, &s.Hash, &c, &u); err != nil {
			return nil, fmt.Errorf("workflows: scan summary: %w", err)
		}
		s.CreatedAt = time.Unix(0, c).UTC()
		s.UpdatedAt = time.Unix(0, u).UTC()
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// History implements Store.History.
func (s *sqliteStore) History(ctx context.Context, id string) ([]WorkflowVersion, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: empty", ErrInvalidID)
	}
	// First confirm the workflow exists so we can disambiguate
	// "no history" from "unknown id".
	var probe int
	if err := s.db.Reader().QueryRow(ctx,
		`SELECT 1 FROM workflows WHERE id = ?`, id).Scan(&probe); err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: %s", ErrWorkflowNotFound, id)
		}
		return nil, fmt.Errorf("workflows: history probe: %w", err)
	}
	rows, err := s.db.Reader().Query(ctx,
		`SELECT version, yaml_source, hash, created_at
		   FROM workflow_versions
		  WHERE workflow_id = ?
		  ORDER BY version DESC`, id)
	if err != nil {
		return nil, fmt.Errorf("workflows: history: %w", err)
	}
	defer rows.Close()
	var out []WorkflowVersion
	for rows.Next() {
		v := WorkflowVersion{WorkflowID: id}
		var c int64
		if err := rows.Scan(&v.Version, &v.YAMLSource, &v.Hash, &c); err != nil {
			return nil, fmt.Errorf("workflows: scan version: %w", err)
		}
		v.CreatedAt = time.Unix(0, c).UTC()
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// hashYAML returns the canonical sha256 hex digest used for change
// detection. The store compares this against the stored hash to decide
// no-op vs version bump.
func hashYAML(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// isNoRows reports whether err is the database/sql sentinel for
// "row not found". The storage adapter wraps sql.ErrNoRows, so we
// match by message text here to stay decoupled from the concrete
// driver. The session package follows the same pattern.
func isNoRows(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "sql: no rows") || strings.Contains(msg, "no rows in result set")
}

// ExportYAML returns the canonical YAML bytes for w. When w was loaded
// via Store.Load the cached yaml_source is returned verbatim; otherwise
// the workflow is re-marshalled.
func ExportYAML(w Workflow) ([]byte, error) {
	if w.yamlSource != "" {
		return []byte(w.yamlSource), nil
	}
	return MarshalYAML(w)
}

// ImportYAML parses b through the existing loader, validates, and
// assigns a fresh id (replacing whatever id was in the source). The
// returned Workflow is ready to hand to Store.Save without further
// massaging.
//
// The fresh-id behaviour is the safety net for the share/import flow:
// importing the same YAML twice produces two distinct workflow rows
// rather than overwriting the operator's existing copy.
func ImportYAML(b []byte) (Workflow, error) {
	w, err := LoadYAML(b)
	if err != nil {
		return Workflow{}, err
	}
	w.ID = freshWorkflowID()
	// Re-validate to confirm the rewritten id still satisfies isKebab.
	// freshWorkflowID is deterministic kebab-case so this is a defensive
	// check rather than a runtime risk.
	if err := Validate(w); err != nil {
		return Workflow{}, err
	}
	// Cache the original source so a follow-on ExportYAML returns
	// byte-for-byte the imported payload (minus any id rewrite the
	// re-marshal would impose). We deliberately re-marshal here so the
	// cached source matches the new id.
	out, err := MarshalYAML(w)
	if err != nil {
		return Workflow{}, fmt.Errorf("workflows: re-marshal import: %w", err)
	}
	w.yamlSource = string(out)
	return w, nil
}

// freshWorkflowID returns a kebab-case id with a 12-hex-character random
// suffix. The "wf-" prefix keeps the id visually distinct from
// session/project ids.
func freshWorkflowID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Vanishingly unlikely; fall back to nanos so we never panic.
		return fmt.Sprintf("wf-%d", time.Now().UnixNano())
	}
	return "wf-" + hex.EncodeToString(b[:])
}
