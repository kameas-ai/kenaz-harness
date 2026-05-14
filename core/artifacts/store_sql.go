package artifacts

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/storage"
)

// sqlStore persists artifacts through a storage.DB-shaped connection.
// Concurrency: storage.DB serializes writes through its single-writer
// queue; Inserts that race on id generation each get a fresh ULID
// from the monotonic generator below.
type sqlStore struct {
	db         storage.DB
	now        func() time.Time
	idGen      func() (string, error)
	sessReader SessionProjectReader
}

// SQLStoreOption configures a sqlStore at construction time.
type SQLStoreOption func(*sqlStore)

// WithSQLClock overrides the wall-clock source. Tests pin this for
// deterministic CreatedAt timestamps.
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

// WithSessionProjectReader injects the session→project lookup used by
// UpdateScope to validate promotions. Optional: a nil reader is
// equivalent to "no session has a project", which means every
// promotion attempt fails ErrUnsupportedScope.
func WithSessionProjectReader(r SessionProjectReader) SQLStoreOption {
	return func(s *sqlStore) {
		s.sessReader = r
	}
}

// NewSQLStore constructs a SQL-backed Store against the unified
// storage.DB.
func NewSQLStore(db storage.DB, opts ...SQLStoreOption) Store {
	s := &sqlStore{
		db:    db,
		now:   func() time.Time { return time.Now().UTC() },
		idGen: defaultArtifactID,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *sqlStore) Insert(ctx context.Context, a Artifact) (Artifact, error) {
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
	srJSON, err := json.Marshal(a.SourceRef)
	if err != nil {
		return Artifact{}, fmt.Errorf("artifacts: marshal source_ref: %w", err)
	}
	var projectID any
	if a.ProjectID != nil {
		projectID = *a.ProjectID
	}
	// session_id is nullable post-0304 — empty string maps to NULL so
	// the artifact-orphan path is representable without a public-API
	// pointer-type change.
	var sessionID any
	if a.SessionID != "" {
		sessionID = a.SessionID
	}
	if err := s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, `
            INSERT INTO artifacts
                (id, session_id, project_id, title, mime_type, content_hash,
                 byte_size, source, source_ref_json, scope_kind, created_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        `,
			a.ID, sessionID, projectID, a.Title, a.MimeType, a.ContentHash,
			a.ByteSize, a.Source, string(srJSON), a.ScopeKind,
			a.CreatedAt.UnixNano(),
		)
		return err
	}); err != nil {
		return Artifact{}, err
	}
	return a, nil
}

const sqlSelectArtifact = `
    SELECT id, session_id, project_id, title, mime_type, content_hash,
           byte_size, source, source_ref_json, scope_kind, created_at
    FROM artifacts
`

func (s *sqlStore) Get(ctx context.Context, id string) (Artifact, error) {
	row := s.db.Reader().QueryRow(ctx, sqlSelectArtifact+" WHERE id = ?", id)
	a, err := scanArtifact(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Artifact{}, ErrArtifactNotFound
		}
		return Artifact{}, err
	}
	return a, nil
}

func (s *sqlStore) List(ctx context.Context, filter ArtifactFilter) ([]Artifact, error) {
	q := sqlSelectArtifact
	args := make([]any, 0, 5)
	conds := make([]string, 0, 5)
	if filter.SessionID != "" {
		conds = append(conds, "session_id = ?")
		args = append(args, filter.SessionID)
	}
	if filter.ProjectID != "" {
		conds = append(conds, "project_id = ?")
		args = append(args, filter.ProjectID)
	}
	if filter.MimeTypePrefix != "" {
		conds = append(conds, "mime_type LIKE ?")
		args = append(args, filter.MimeTypePrefix+"%")
	}
	if filter.Source != "" {
		conds = append(conds, "source = ?")
		args = append(args, filter.Source)
	}
	if filter.ScopeKind != "" {
		conds = append(conds, "scope_kind = ?")
		args = append(args, filter.ScopeKind)
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
	var out []Artifact
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *sqlStore) UpdateScope(ctx context.Context, id, scopeKind, scopeID string) (Artifact, error) {
	if !validScope(scopeKind) {
		return Artifact{}, fmt.Errorf("%w: %q", ErrUnsupportedScope, scopeKind)
	}
	current, err := s.Get(ctx, id)
	if err != nil {
		return Artifact{}, err
	}
	var projectID any
	switch scopeKind {
	case ScopeKindProject:
		// Validate the artifact's session has a project AND the
		// requested scopeID matches it. Without a reader we cannot
		// safely promote — refuse.
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
		projectID = sessProj
	case ScopeKindSession:
		// Demote: clear project_id.
		projectID = nil
	}

	if err := s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		res, err := tx.Exec(ctx,
			"UPDATE artifacts SET scope_kind = ?, project_id = ? WHERE id = ?",
			scopeKind, projectID, id)
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

func (s *sqlStore) Delete(ctx context.Context, id string) (string, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if err := s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		res, err := tx.Exec(ctx, "DELETE FROM artifacts WHERE id = ?", id)
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
		return "", err
	}
	return current.ContentHash, nil
}

func (s *sqlStore) RefcountFor(ctx context.Context, contentHash string) (int, error) {
	row := s.db.Reader().QueryRow(ctx,
		"SELECT COUNT(*) FROM artifacts WHERE content_hash = ?", contentHash)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// Refcount satisfies the attachments.RefcountSource shape (without
// importing the attachments package — the contract is structural).
// WP02 of the artifacts mission registers an instance via
// MediaStore.RegisterRefcountSource at chassis-construction time.
func (s *sqlStore) Refcount(ctx context.Context, contentHash string) (int, error) {
	return s.RefcountFor(ctx, contentHash)
}

func (s *sqlStore) WriteVersion(ctx context.Context, v ArtifactVersion) (ArtifactVersion, error) {
	// Verify the parent artifact exists.
	if _, err := s.Get(ctx, v.ArtifactID); err != nil {
		return ArtifactVersion{}, err
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = s.now()
	}
	// Determine the next version number inside the write transaction to
	// avoid races: SELECT MAX(version)+1 and INSERT atomically.
	if err := s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		row := tx.QueryRow(ctx,
			"SELECT COALESCE(MAX(version), 0) FROM artifact_versions WHERE artifact_id = ?",
			v.ArtifactID)
		var maxVer int
		if err := row.Scan(&maxVer); err != nil {
			return fmt.Errorf("artifacts: WriteVersion: scan max version: %w", err)
		}
		v.Version = maxVer + 1
		res, err := tx.Exec(ctx, `
            INSERT INTO artifact_versions
                (artifact_id, version, content_hash, byte_size, mime_type, summary, path, created_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?)
        `,
			v.ArtifactID, v.Version, v.ContentHash, v.ByteSize,
			v.MimeType, v.Summary, v.Path,
			v.CreatedAt.UnixNano(),
		)
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
		return nil
	}); err != nil {
		return ArtifactVersion{}, err
	}
	return v, nil
}

func (s *sqlStore) ListVersions(ctx context.Context, artifactID string) ([]ArtifactVersion, error) {
	rows, err := s.db.Reader().Query(ctx, `
        SELECT id, artifact_id, version, content_hash, byte_size, mime_type,
               summary, path, created_at
        FROM artifact_versions
        WHERE artifact_id = ?
        ORDER BY version ASC
    `, artifactID)
	if err != nil {
		return nil, fmt.Errorf("artifacts: ListVersions: query: %w", err)
	}
	defer rows.Close()
	var out []ArtifactVersion
	for rows.Next() {
		v, err := scanArtifactVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func scanArtifactVersion(sc interface{ Scan(dest ...any) error }) (ArtifactVersion, error) {
	var (
		v           ArtifactVersion
		summary     sql.NullString
		path        sql.NullString
		createdAtNs int64
	)
	if err := sc.Scan(
		&v.ID, &v.ArtifactID, &v.Version, &v.ContentHash, &v.ByteSize,
		&v.MimeType, &summary, &path, &createdAtNs,
	); err != nil {
		return ArtifactVersion{}, fmt.Errorf("artifacts: scan version: %w", err)
	}
	if summary.Valid {
		s := summary.String
		v.Summary = &s
	}
	if path.Valid {
		p := path.String
		v.Path = &p
	}
	v.CreatedAt = time.Unix(0, createdAtNs).UTC()
	return v, nil
}

// ArtifactsRefcountSource exposes a Store as a RefcountSource for the
// attachments.MediaStore composite refcount. WP02 wires this via
// `media.RegisterRefcountSource(artifacts.ArtifactsRefcountSource{Store: store})`
// at chassis construction; WP01 ships only the type.
type ArtifactsRefcountSource struct {
	Store Store
}

// Refcount implements attachments.RefcountSource.
func (a ArtifactsRefcountSource) Refcount(ctx context.Context, contentHash string) (int, error) {
	if a.Store == nil {
		return 0, nil
	}
	return a.Store.RefcountFor(ctx, contentHash)
}

func scanArtifact(sc interface{ Scan(dest ...any) error }) (Artifact, error) {
	var (
		a           Artifact
		sessionID   sql.NullString
		projectID   sql.NullString
		sourceRef   string
		createdAtNs int64
	)
	if err := sc.Scan(
		&a.ID, &sessionID, &projectID, &a.Title, &a.MimeType,
		&a.ContentHash, &a.ByteSize, &a.Source, &sourceRef,
		&a.ScopeKind, &createdAtNs,
	); err != nil {
		return Artifact{}, err
	}
	// session_id is nullable post-migration 0304 (artifacts can outlive
	// their source session via the session-delete-then-promote path).
	// The public Artifact.SessionID stays a plain string; an orphaned
	// row simply renders as "" so callers that key off SessionID treat
	// it as project-scope-only.
	if sessionID.Valid {
		a.SessionID = sessionID.String
	}
	if projectID.Valid {
		v := projectID.String
		a.ProjectID = &v
	}
	if sourceRef != "" {
		if err := json.Unmarshal([]byte(sourceRef), &a.SourceRef); err != nil {
			return Artifact{}, fmt.Errorf("artifacts: unmarshal source_ref: %w", err)
		}
	}
	a.CreatedAt = time.Unix(0, createdAtNs).UTC()
	return a, nil
}

func validSource(s string) bool {
	switch s {
	case SourceCodeBlock, SourceToolOutput, SourceUserPin, SourceModelOutput:
		return true
	}
	return false
}

func validScope(s string) bool {
	switch s {
	case ScopeKindSession, ScopeKindProject:
		return true
	}
	return false
}

// ── ID generator ───────────────────────────────────────────────────────

// defaultArtifactID returns a 26-char Crockford-base32 ULID. We use a
// process-monotonic generator so two Inserts in the same millisecond
// produce strictly increasing ids — useful for List ordering.
func defaultArtifactID() (string, error) {
	return artifactULIDGen.Next()
}

var artifactULIDGen = newArtifactULIDGenerator()

type artifactULIDGenerator struct {
	mu     sync.Mutex
	lastMs uint64
	tail   [10]byte
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func newArtifactULIDGenerator() *artifactULIDGenerator { return &artifactULIDGenerator{} }

func (g *artifactULIDGenerator) Next() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ms := uint64(time.Now().UnixMilli())
	if ms < g.lastMs {
		ms = g.lastMs
	}
	if ms == g.lastMs {
		for i := len(g.tail) - 1; i >= 0; i-- {
			g.tail[i]++
			if g.tail[i] != 0 {
				break
			}
			if i == 0 {
				return "", errors.New("artifacts: ULID tail overflow")
			}
		}
	} else {
		var t [10]byte
		if _, err := rand.Read(t[:]); err != nil {
			return "", fmt.Errorf("artifacts: rand: %w", err)
		}
		g.tail = t
		g.lastMs = ms
	}
	return encodeArtifactULID(g.lastMs, g.tail), nil
}

func encodeArtifactULID(ms uint64, tail [10]byte) string {
	var raw [16]byte
	raw[0] = byte(ms >> 40)
	raw[1] = byte(ms >> 32)
	raw[2] = byte(ms >> 24)
	raw[3] = byte(ms >> 16)
	raw[4] = byte(ms >> 8)
	raw[5] = byte(ms)
	copy(raw[6:], tail[:])

	out := make([]byte, 26)
	out[0] = crockford[(raw[0]>>5)&0x07]
	out[1] = crockford[raw[0]&0x1f]
	out[2] = crockford[(raw[1]>>3)&0x1f]
	out[3] = crockford[((raw[1]&0x07)<<2)|((raw[2]>>6)&0x03)]
	out[4] = crockford[(raw[2]>>1)&0x1f]
	out[5] = crockford[((raw[2]&0x01)<<4)|((raw[3]>>4)&0x0f)]
	out[6] = crockford[((raw[3]&0x0f)<<1)|((raw[4]>>7)&0x01)]
	out[7] = crockford[(raw[4]>>2)&0x1f]
	out[8] = crockford[((raw[4]&0x03)<<3)|((raw[5]>>5)&0x07)]
	out[9] = crockford[raw[5]&0x1f]
	out[10] = crockford[(raw[6]>>3)&0x1f]
	out[11] = crockford[((raw[6]&0x07)<<2)|((raw[7]>>6)&0x03)]
	out[12] = crockford[(raw[7]>>1)&0x1f]
	out[13] = crockford[((raw[7]&0x01)<<4)|((raw[8]>>4)&0x0f)]
	out[14] = crockford[((raw[8]&0x0f)<<1)|((raw[9]>>7)&0x01)]
	out[15] = crockford[(raw[9]>>2)&0x1f]
	out[16] = crockford[((raw[9]&0x03)<<3)|((raw[10]>>5)&0x07)]
	out[17] = crockford[raw[10]&0x1f]
	out[18] = crockford[(raw[11]>>3)&0x1f]
	out[19] = crockford[((raw[11]&0x07)<<2)|((raw[12]>>6)&0x03)]
	out[20] = crockford[(raw[12]>>1)&0x1f]
	out[21] = crockford[((raw[12]&0x01)<<4)|((raw[13]>>4)&0x0f)]
	out[22] = crockford[((raw[13]&0x0f)<<1)|((raw[14]>>7)&0x01)]
	out[23] = crockford[(raw[14]>>2)&0x1f]
	out[24] = crockford[((raw[14]&0x03)<<3)|((raw[15]>>5)&0x07)]
	out[25] = crockford[raw[15]&0x1f]
	return string(out)
}
