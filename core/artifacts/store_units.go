package artifacts

// store_units.go — the units-backed artifacts.Store
// (artifacts-as-units-01DOGF0C WP02, spec FR-1).
//
// Artifacts are core/units rows with kind='artifact'. The mapping is the
// one recorded in docs/missions/artifacts-as-units.md (D1):
//
//   units.id             = Artifact.ID (preserved verbatim, D2)
//   units.scope          = Artifact.ScopeKind
//   units.scope_id       = session id | project id | '' by scope
//   units.classification = 'personal'  (never synced, D3)
//   units.load_policy    = 'on_demand' (never auto-injected)
//   units.body           = ''          (bytes stay in the media CAS)
//   units.metadata       = {content_hash, byte_size, mime_type, source,
//                           source_ref, session_id, project_id,
//                           legacy_artifact_id}
//   unit_versions        = one row per ArtifactVersion; metadata carries
//                           {content_hash, byte_size, mime_type, summary,
//                           path}
//
// The head row is the CAPTURE, not the newest revision: WriteVersion
// appends history and bumps units.version/updated_at only, exactly as the
// legacy artifacts row was never mutated by a revision.
//
// Ownership: core/units owns the units tables' schema; this file is the
// repository for the kind='artifact' rows within them. Every statement
// below is scoped `kind = 'artifact'` (units.KindArtifact; pinned by
// TestUnitsStore_RowShape) so it can never read or mutate a
// document, snippet or any other unit kind.

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

// unitsStore implements Store over the units / unit_versions tables.
type unitsStore struct {
	db         storage.DB
	now        func() time.Time
	idGen      func() (string, error)
	sessReader SessionProjectReader
}

// NewUnitsStore constructs the units-backed Store. It accepts the same
// options as NewSQLStore (clock, id generator, session→project reader).
func NewUnitsStore(db storage.DB, opts ...SQLStoreOption) Store {
	s := &unitsStore{
		db:    db,
		now:   func() time.Time { return time.Now().UTC() },
		idGen: defaultArtifactID,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// artifactUnitMeta is the JSON shape of units.metadata for an artifact
// unit. Pointer fields encode as JSON null, matching what migration
// units/1104 writes with json_object() for NULL session/project ids.
type artifactUnitMeta struct {
	ContentHash      string             `json:"content_hash"`
	ByteSize         int64              `json:"byte_size"`
	MimeType         string             `json:"mime_type"`
	Source           string             `json:"source"`
	SourceRef        *ArtifactSourceRef `json:"source_ref,omitempty"`
	SourceRefRaw     *string            `json:"source_ref_raw,omitempty"`
	SessionID        *string            `json:"session_id"`
	ProjectID        *string            `json:"project_id"`
	LegacyArtifactID string             `json:"legacy_artifact_id,omitempty"`
}

// artifactVersionMeta is the JSON shape of unit_versions.metadata for an
// artifact revision.
type artifactVersionMeta struct {
	ContentHash     string  `json:"content_hash"`
	ByteSize        int64   `json:"byte_size"`
	MimeType        string  `json:"mime_type"`
	Summary         *string `json:"summary"`
	Path            *string `json:"path"`
	LegacyVersionID *int64  `json:"legacy_version_id,omitempty"`
	Synthesized     bool    `json:"synthesized,omitempty"`
}

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// scopeIDFor derives units.scope_id from the artifact's scope.
func scopeIDFor(scopeKind, sessionID string, projectID *string) string {
	switch scopeKind {
	case ScopeKindSession:
		return sessionID
	case ScopeKindProject:
		if projectID != nil {
			return *projectID
		}
	}
	return ""
}

func (s *unitsStore) Insert(ctx context.Context, a Artifact) (Artifact, error) {
	if !validSource(a.Source) {
		return Artifact{}, fmt.Errorf("%w: %q", ErrUnsupportedSource, a.Source)
	}
	if a.ScopeKind == "" {
		a.ScopeKind = ScopeKindSession
	}
	if !validScope(a.ScopeKind) {
		return Artifact{}, fmt.Errorf("%w: %q", ErrUnsupportedScope, a.ScopeKind)
	}
	if a.ID == "" {
		id, err := s.idGen()
		if err != nil {
			return Artifact{}, fmt.Errorf("artifacts: id gen: %w", err)
		}
		a.ID = id
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = s.now()
	}
	ref := a.SourceRef
	meta := artifactUnitMeta{
		ContentHash: a.ContentHash,
		ByteSize:    a.ByteSize,
		MimeType:    a.MimeType,
		Source:      a.Source,
		SourceRef:   &ref,
		SessionID:   strPtrOrNil(a.SessionID),
	}
	if a.ProjectID != nil {
		p := *a.ProjectID
		meta.ProjectID = &p
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return Artifact{}, fmt.Errorf("artifacts: marshal metadata: %w", err)
	}
	ts := a.CreatedAt.UnixNano()
	if err := s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, `
            INSERT INTO units
                (id, kind, scope, scope_id, classification, version,
                 load_policy, title, body, metadata, created_at, updated_at)
            VALUES (?, 'artifact', ?, ?, 'personal', 0, 'on_demand', ?, '', ?, ?, ?)
        `,
			a.ID, a.ScopeKind, scopeIDFor(a.ScopeKind, a.SessionID, a.ProjectID),
			a.Title, string(metaJSON), ts, ts,
		)
		return err
	}); err != nil {
		return Artifact{}, err
	}
	return a, nil
}

const unitsSelectArtifact = `
    SELECT id, scope, title, metadata, created_at
    FROM units
`

func (s *unitsStore) Get(ctx context.Context, id string) (Artifact, error) {
	row := s.db.Reader().QueryRow(ctx,
		unitsSelectArtifact+" WHERE id = ? AND kind = 'artifact'", id)
	a, err := scanArtifactUnit(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Artifact{}, ErrArtifactNotFound
		}
		return Artifact{}, err
	}
	return a, nil
}

func (s *unitsStore) List(ctx context.Context, filter ArtifactFilter) ([]Artifact, error) {
	conds := []string{"kind = 'artifact'"}
	args := make([]any, 0, 5)
	if filter.SessionID != "" {
		conds = append(conds, "json_extract(metadata, '$.session_id') = ?")
		args = append(args, filter.SessionID)
	}
	if filter.ProjectID != "" {
		conds = append(conds, "json_extract(metadata, '$.project_id') = ?")
		args = append(args, filter.ProjectID)
	}
	if filter.MimeTypePrefix != "" {
		conds = append(conds, "json_extract(metadata, '$.mime_type') LIKE ?")
		args = append(args, filter.MimeTypePrefix+"%")
	}
	if filter.Source != "" {
		conds = append(conds, "json_extract(metadata, '$.source') = ?")
		args = append(args, filter.Source)
	}
	if filter.ScopeKind != "" {
		conds = append(conds, "scope = ?")
		args = append(args, filter.ScopeKind)
	}
	q := unitsSelectArtifact + " WHERE " + strings.Join(conds, " AND ") + " ORDER BY created_at DESC"
	rows, err := s.db.Reader().Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		a, err := scanArtifactUnit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *unitsStore) UpdateScope(ctx context.Context, id, scopeKind, scopeID string) (Artifact, error) {
	if !validScope(scopeKind) {
		return Artifact{}, fmt.Errorf("%w: %q", ErrUnsupportedScope, scopeKind)
	}
	current, err := s.Get(ctx, id)
	if err != nil {
		return Artifact{}, err
	}
	var projectID *string
	switch scopeKind {
	case ScopeKindProject:
		// Same contract as the legacy store: the artifact's session must
		// have a project and scopeID (when given) must match it.
		if s.sessReader == nil {
			return Artifact{}, fmt.Errorf("%w: no session→project reader configured", ErrUnsupportedScope)
		}
		sessProj, err := s.sessReader.SessionProject(ctx, current.SessionID)
		if err != nil {
			return Artifact{}, fmt.Errorf("%w: %v", ErrUnsupportedScope, err)
		}
		if sessProj == "" {
			return Artifact{}, fmt.Errorf("%w: session %s has no project", ErrUnsupportedScope, current.SessionID)
		}
		if scopeID != "" && scopeID != sessProj {
			return Artifact{}, fmt.Errorf("%w: scope id %q does not match session's project %q",
				ErrUnsupportedScope, scopeID, sessProj)
		}
		projectID = &sessProj
	case ScopeKindGlobal, ScopeKindSession:
		// Promote to global / demote to session both clear the project
		// link (legacy: project_id = NULL).
		projectID = nil
	}
	var projectArg any
	if projectID != nil {
		projectArg = *projectID
	}
	newScopeID := scopeIDFor(scopeKind, current.SessionID, projectID)
	if err := s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		res, err := tx.Exec(ctx, `
            UPDATE units
               SET scope = ?, scope_id = ?,
                   metadata = json_set(metadata, '$.project_id', ?)
             WHERE id = ? AND kind = 'artifact'`,
			scopeKind, newScopeID, projectArg, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrArtifactNotFound
		}
		return nil
	}); err != nil {
		return Artifact{}, err
	}
	return s.Get(ctx, id)
}

func (s *unitsStore) Delete(ctx context.Context, id string) (string, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if err := s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		n, err := deleteArtifactUnitsTx(ctx, tx, "id = ?", id)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrArtifactNotFound
		}
		return nil
	}); err != nil {
		return "", err
	}
	return current.ContentHash, nil
}

// deleteArtifactUnitsTx removes the artifact units matching where (a
// predicate over the units table, scoped to kind='artifact' here) and,
// explicitly, their version rows, edges and sync sidecar rows. The child
// deletes do not rely on the foreign_keys pragma: units.scope_id has no
// FK at all, and a delete path that depends on a pragma being set is a
// delete path that silently stops deleting when it is not.
func deleteArtifactUnitsTx(ctx context.Context, tx storage.WriteTx, where string, args ...any) (int64, error) {
	sel := "SELECT id FROM units WHERE kind = 'artifact' AND " + where
	children := []string{
		"DELETE FROM unit_versions WHERE unit_id IN (" + sel + ")",
		"DELETE FROM unit_edges WHERE from_id IN (" + sel + ") OR to_id IN (" + sel + ")",
		"DELETE FROM unit_sync_state WHERE unit_id IN (" + sel + ")",
	}
	for i, stmt := range children {
		stmtArgs := args
		if i == 1 { // edges predicate references sel twice
			stmtArgs = append(append([]any{}, args...), args...)
		}
		if _, err := tx.Exec(ctx, stmt, stmtArgs...); err != nil {
			return 0, err
		}
	}
	res, err := tx.Exec(ctx, "DELETE FROM units WHERE kind = 'artifact' AND "+where, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RefcountFor counts every unit and unit version — of any kind — whose
// metadata names contentHash. Counting beyond kind='artifact' and beyond
// the head row is deliberately conservative: the media store deletes a
// blob when this (summed with the other sources) is zero, so an
// over-count keeps bytes, an under-count destroys them.
func (s *unitsStore) RefcountFor(ctx context.Context, contentHash string) (int, error) {
	row := s.db.Reader().QueryRow(ctx, `
        SELECT
          (SELECT COUNT(*) FROM units
            WHERE json_extract(metadata, '$.content_hash') = ?)
        + (SELECT COUNT(*) FROM unit_versions
            WHERE json_extract(metadata, '$.content_hash') = ?)`,
		contentHash, contentHash)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// Refcount satisfies the attachments.RefcountSource shape structurally.
func (s *unitsStore) Refcount(ctx context.Context, contentHash string) (int, error) {
	return s.RefcountFor(ctx, contentHash)
}

func (s *unitsStore) WriteVersion(ctx context.Context, v ArtifactVersion) (ArtifactVersion, error) {
	if _, err := s.Get(ctx, v.ArtifactID); err != nil {
		return ArtifactVersion{}, err
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = s.now()
	}
	meta, err := json.Marshal(artifactVersionMeta{
		ContentHash: v.ContentHash,
		ByteSize:    v.ByteSize,
		MimeType:    v.MimeType,
		Summary:     v.Summary,
		Path:        v.Path,
	})
	if err != nil {
		return ArtifactVersion{}, fmt.Errorf("artifacts: WriteVersion: marshal metadata: %w", err)
	}
	if err := s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		row := tx.QueryRow(ctx,
			"SELECT COALESCE(MAX(version), 0) FROM unit_versions WHERE unit_id = ?", v.ArtifactID)
		var maxVer int
		if err := row.Scan(&maxVer); err != nil {
			return fmt.Errorf("artifacts: WriteVersion: scan max version: %w", err)
		}
		v.Version = maxVer + 1
		res, err := tx.Exec(ctx, `
            INSERT INTO unit_versions (unit_id, version, body, metadata, created_at)
            VALUES (?, ?, '', ?, ?)`,
			v.ArtifactID, v.Version, string(meta), v.CreatedAt.UnixNano())
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				return ErrVersionConflict
			}
			return fmt.Errorf("artifacts: WriteVersion: insert: %w", err)
		}
		id, err := res.LastInsertID()
		if err != nil {
			return fmt.Errorf("artifacts: WriteVersion: last insert id: %w", err)
		}
		v.ID = id
		// Head row bookkeeping only — the capture metadata is NOT
		// rewritten (see file header).
		_, err = tx.Exec(ctx,
			"UPDATE units SET version = ?, updated_at = ? WHERE id = ? AND kind = 'artifact'",
			v.Version, v.CreatedAt.UnixNano(), v.ArtifactID)
		return err
	}); err != nil {
		return ArtifactVersion{}, err
	}
	return v, nil
}

func (s *unitsStore) ListVersions(ctx context.Context, artifactID string) ([]ArtifactVersion, error) {
	rows, err := s.db.Reader().Query(ctx, `
        SELECT uv.id, uv.unit_id, uv.version, uv.metadata, uv.created_at
          FROM unit_versions uv
          JOIN units u ON u.id = uv.unit_id AND u.kind = 'artifact'
         WHERE uv.unit_id = ?
         ORDER BY uv.version ASC`, artifactID)
	if err != nil {
		return nil, fmt.Errorf("artifacts: ListVersions: query: %w", err)
	}
	defer rows.Close()
	var out []ArtifactVersion
	for rows.Next() {
		var (
			v         ArtifactVersion
			metaText  string
			createdNs int64
		)
		if err := rows.Scan(&v.ID, &v.ArtifactID, &v.Version, &metaText, &createdNs); err != nil {
			return nil, fmt.Errorf("artifacts: scan version: %w", err)
		}
		var m artifactVersionMeta
		if err := json.Unmarshal([]byte(metaText), &m); err != nil {
			return nil, fmt.Errorf("artifacts: decode version metadata: %w", err)
		}
		v.ContentHash = m.ContentHash
		v.ByteSize = m.ByteSize
		v.MimeType = m.MimeType
		v.Summary = m.Summary
		v.Path = m.Path
		v.CreatedAt = time.Unix(0, createdNs).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}

func scanArtifactUnit(sc interface{ Scan(dest ...any) error }) (Artifact, error) {
	var (
		a         Artifact
		metaText  string
		createdNs int64
	)
	if err := sc.Scan(&a.ID, &a.ScopeKind, &a.Title, &metaText, &createdNs); err != nil {
		return Artifact{}, err
	}
	var m artifactUnitMeta
	if err := json.Unmarshal([]byte(metaText), &m); err != nil {
		return Artifact{}, fmt.Errorf("artifacts: decode unit metadata for %s: %w", a.ID, err)
	}
	a.ContentHash = m.ContentHash
	a.ByteSize = m.ByteSize
	a.MimeType = m.MimeType
	a.Source = m.Source
	if m.SourceRef != nil {
		a.SourceRef = *m.SourceRef
	}
	if m.SessionID != nil {
		a.SessionID = *m.SessionID
	}
	if m.ProjectID != nil {
		p := *m.ProjectID
		a.ProjectID = &p
	}
	a.CreatedAt = time.Unix(0, createdNs).UTC()
	return a, nil
}

// ScopePurger is implemented by stores whose rows reference sessions and
// projects WITHOUT a foreign key (the units-backed store: units.scope_id
// is plain TEXT). The legacy artifacts table had schema FKs —
// sessions(id)/projects(id) ON DELETE SET NULL — so it needed none of
// this; after the move onto units the cascade is explicit code, registered
// as session.Manager / projects.Manager delete observers
// (artifacts-as-units-01DOGF0C WP03, spec FR-6).
type ScopePurger interface {
	// PurgeSession deletes every artifact unit scoped to the session
	// (with its versions, edges and sync rows) and nulls the origin
	// session_id on artifact units that merely originated there but were
	// promoted to project/global scope — the legacy SET NULL parity.
	// Returns the distinct content hashes the deleted units and versions
	// referenced, so the caller can release the media they pinned.
	PurgeSession(ctx context.Context, sessionID string) ([]string, error)
	// PurgeProject deletes every artifact unit scoped to the project and
	// nulls project_id on the rest that referenced it. Returns hashes as
	// PurgeSession does.
	PurgeProject(ctx context.Context, projectID string) ([]string, error)
}

var _ ScopePurger = (*unitsStore)(nil)

func (s *unitsStore) PurgeSession(ctx context.Context, sessionID string) ([]string, error) {
	return s.purgeScope(ctx, ScopeKindSession, "$.session_id", sessionID)
}

func (s *unitsStore) PurgeProject(ctx context.Context, projectID string) ([]string, error) {
	return s.purgeScope(ctx, ScopeKindProject, "$.project_id", projectID)
}

func (s *unitsStore) purgeScope(ctx context.Context, scope, metaPath, id string) ([]string, error) {
	if id == "" {
		return nil, nil // never widen to "every unscoped row"
	}
	var hashes []string
	err := s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		rows, err := tx.Query(ctx, `
            SELECT json_extract(metadata, '$.content_hash') FROM units
             WHERE kind = 'artifact' AND scope = ? AND scope_id = ?
            UNION
            SELECT json_extract(uv.metadata, '$.content_hash') FROM unit_versions uv
              JOIN units u ON u.id = uv.unit_id
             WHERE u.kind = 'artifact' AND u.scope = ? AND u.scope_id = ?`,
			scope, id, scope, id)
		if err != nil {
			return fmt.Errorf("artifacts: purge %s %s: collect hashes: %w", scope, id, err)
		}
		for rows.Next() {
			var h sql.NullString
			if err := rows.Scan(&h); err != nil {
				_ = rows.Close()
				return err
			}
			if h.Valid && h.String != "" {
				hashes = append(hashes, h.String)
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if _, err := deleteArtifactUnitsTx(ctx, tx, "scope = ? AND scope_id = ?", scope, id); err != nil {
			return fmt.Errorf("artifacts: purge %s %s: %w", scope, id, err)
		}
		if _, err := tx.Exec(ctx, `
            UPDATE units SET metadata = json_set(metadata, '`+metaPath+`', NULL)
             WHERE kind = 'artifact' AND json_extract(metadata, '`+metaPath+`') = ?`, id); err != nil {
			return fmt.Errorf("artifacts: purge %s %s: unlink: %w", scope, id, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return hashes, nil
}
