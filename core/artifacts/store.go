package artifacts

import (
	"context"
)

// Store is the persistence contract Manager consumes. Two
// implementations ship in this package: an in-memory store (memStore,
// returned by NewMemoryStore) used by unit tests, and a SQL-backed
// store (sqlStore, returned by NewSQLStore) for runtime.
//
// All methods are safe for concurrent use.
type Store interface {
	// Insert writes a new row. If a.ID is empty a fresh ULID is
	// minted; if a.CreatedAt is the zero time the current wall clock
	// is used. Returns the inserted Artifact (with id and timestamp
	// populated).
	Insert(ctx context.Context, a Artifact) (Artifact, error)

	// Get returns one row by id. ErrArtifactNotFound when the id has
	// no match.
	Get(ctx context.Context, id string) (Artifact, error)

	// List returns rows matching filter, ordered by created_at DESC.
	List(ctx context.Context, filter ArtifactFilter) ([]Artifact, error)

	// UpdateScope mutates one row's (scope_kind, project_id) pair. The
	// caller passes the artifact id, the new ScopeKind ("session" or
	// "project"), and a scopeID — the project id when promoting to
	// project, or empty when demoting to session. Returns the updated
	// Artifact. ErrUnsupportedScope if the scopeKind is unknown or if
	// promoting a session→project but the artifact's session has no
	// project assigned.
	UpdateScope(ctx context.Context, id, scopeKind, scopeID string) (Artifact, error)

	// Delete removes one row by id and returns the deleted row's
	// content_hash so callers can call MediaStore.RefcountFor against
	// it and decide whether to remove the on-disk CAS file.
	// ErrArtifactNotFound when the id has no match.
	Delete(ctx context.Context, id string) (contentHash string, err error)

	// RefcountFor satisfies media.RefcountSource. Returns the count of
	// artifacts rows whose content_hash matches. The MediaStore sums
	// this with the attachments source's count to decide on-disk file
	// liveness.
	RefcountFor(ctx context.Context, contentHash string) (int, error)
}

// SessionProjectReader is the narrow read surface artifacts uses to
// validate session→project promotions and to default new artifacts'
// project_id from their session's project. The session manager's
// public API (Manager.Get) implements this contract; passing it
// through an interface keeps core/artifacts independent of
// core/session and avoids a cycle should session ever want to read
// artifacts.
type SessionProjectReader interface {
	// SessionProject reports the project id (or "") for a session.
	// Returns ErrSessionNotFound-shaped errors when the session is
	// missing — artifacts treats any non-nil error from this method
	// as "promotion not allowed".
	SessionProject(ctx context.Context, sessionID string) (projectID string, err error)
}

// staticSessionProjectReader satisfies SessionProjectReader from a
// pre-resolved map. Useful for tests and for the Manager's
// constructor when the caller wants to skip live lookups.
type staticSessionProjectReader struct {
	projects map[string]string
}

// NewStaticSessionProjectReader returns a SessionProjectReader backed
// by a session_id → project_id map. Lookups for missing sessions
// return ("", nil) — the empty project id signals "session has no
// project" to UpdateScope.
func NewStaticSessionProjectReader(m map[string]string) SessionProjectReader {
	return &staticSessionProjectReader{projects: m}
}

func (s *staticSessionProjectReader) SessionProject(_ context.Context, sessionID string) (string, error) {
	if s.projects == nil {
		return "", nil
	}
	return s.projects[sessionID], nil
}
