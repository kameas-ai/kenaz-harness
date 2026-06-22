// Package attachments owns the harness's context_attachments persistence —
// the post-Mission-A replacement for the session.system_prompt column. An
// Attachment is a snapshot of starting context at one of three scopes:
//
//   - "global"  — applies to every session in the harness.
//   - "project" — applies to every session under a given project.
//   - "session" — applies to one specific session.
//
// Attachments are content-addressed by their source: an "inline:<sha256>"
// source identifies a snapshot pasted/uploaded by the user; a
// "library:<path>" source points at a file in the Context Library
// (core/contexts) and may be Refresh'd to re-read the source. The
// snapshot lives in the Content column either way, so deletion of the
// underlying library file does not affect already-attached sessions.
//
// Mission A's per-session SystemPrompt path collapses into a single
// session-scoped Attachment at Position 0 with Kind == "system" once
// migration 0301 lands. WP04 will introduce the project + global scopes
// to the UI.
package attachments

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ScopeKind values for Attachment.ScopeKind.
const (
	ScopeKindGlobal  = "global"
	ScopeKindProject = "project"
	ScopeKindSession = "session"
)

// Kind values for Attachment.Kind. Mirrors session.ContextKind* so the
// shim path can pass values through without translation.
const (
	KindSystem = "system"
	KindUser   = "user"
)

// Attachment is the durable representation of one context attachment.
type Attachment struct {
	// ID is a 32-char hex identifier (16 random bytes); generated at
	// Add time when empty.
	ID string
	// ScopeKind is one of ScopeKindGlobal, ScopeKindProject,
	// ScopeKindSession.
	ScopeKind string
	// ScopeID identifies the scoped owner. Empty for global; project_id
	// for project; session_id for session.
	ScopeID string
	// ContentSource records where Content came from. Two prefixes are
	// recognised:
	//
	//   - "inline:<sha256>"     — snapshot uploaded/pasted by the user.
	//   - "library:<root-rel>"  — snapshot pulled from the Context
	//                             Library at <root-rel>; Refresh re-reads
	//                             the file from disk.
	ContentSource string
	// Content is the inlined snapshot — survives deletion of the source.
	Content string
	// Kind is "system" (invisible, prepended) or "user" (visible — the
	// caller is responsible for surfacing it as a user turn).
	Kind string
	// Position is the 0-based ordering within (ScopeKind, ScopeID).
	Position int
	// CreatedAt is the wall clock at insert time (UTC).
	CreatedAt time.Time
	// MediaID, when non-nil, identifies the MediaArtifact backing this
	// attachment. The presence of MediaID flags the attachment as
	// binary-backed; Content is permitted to be empty. Existing
	// text-only attachments leave MediaID nil and the resolver path
	// unchanged. Migration 0302 adds the underlying column with
	// ON DELETE SET NULL semantics so a media row deletion never
	// orphans an attachment.
	MediaID *string
}

// Sentinel errors. Stable typed errors so callers can errors.Is.
var (
	// ErrNotFound is returned when an id has no matching row.
	ErrNotFound = errors.New("attachments: not found")

	// ErrInvalidScope is returned when ScopeKind is outside the canonical
	// set, or ScopeID is required but empty (project / session scopes).
	ErrInvalidScope = errors.New("attachments: invalid scope")

	// ErrInvalidKind is returned when Kind is outside {system, user}.
	ErrInvalidKind = errors.New("attachments: invalid kind")

	// ErrInvalidContentSource is returned when ContentSource is empty
	// or has an unrecognised scheme.
	ErrInvalidContentSource = errors.New("attachments: invalid content_source")

	// ErrSourceUnavailable is returned by Refresh when the underlying
	// source cannot be re-read (the file was deleted, the library is
	// not configured, etc.).
	ErrSourceUnavailable = errors.New("attachments: source unavailable")

	// ErrMediaStoreUnavailable is returned by AddMedia when the manager
	// was constructed without WithMediaStore.
	ErrMediaStoreUnavailable = errors.New("attachments: media store not configured")
)

// SessionProjectReader resolves the project membership of a session.
// ListResolved consumes it to splice in the session's project-scope
// attachments. The attachments package does not depend on core/session
// directly — the rpc layer wires the adapter.
type SessionProjectReader interface {
	ProjectID(ctx context.Context, sessionID string) (*string, error)
}

// LibraryReader is the narrow surface ListResolved / Refresh use to
// re-read "library:<path>" attachments from the Context Library. nil
// disables library refresh (Refresh on a library-source returns
// ErrSourceUnavailable; ListResolved returns the stored snapshot
// regardless).
type LibraryReader interface {
	Get(ctx context.Context, path string) (string, error)
}

// IDGen produces attachment ids. Tests override; production uses the
// default crypto-random hex.
type IDGen func() (string, error)

// Manager is the public façade. Safe for concurrent use.
type Manager struct {
	store   Store
	now     func() time.Time
	idGen   IDGen
	library LibraryReader
	media   MediaStore
}

// ManagerOption configures a Manager at construction time.
type ManagerOption func(*Manager)

// WithClock overrides the wall-clock source. Tests pin this for
// deterministic timestamps.
func WithClock(now func() time.Time) ManagerOption {
	return func(m *Manager) {
		if now != nil {
			m.now = now
		}
	}
}

// WithIDGen overrides the id generator.
func WithIDGen(gen IDGen) ManagerOption {
	return func(m *Manager) {
		if gen != nil {
			m.idGen = gen
		}
	}
}

// WithLibrary wires the Context Library reader so Refresh on
// library-source attachments re-reads from disk.
func WithLibrary(lib LibraryReader) ManagerOption {
	return func(m *Manager) {
		m.library = lib
	}
}

// WithMediaStore wires the MediaStore used by AddMedia. Without it the
// AddMedia path returns ErrMediaStoreUnavailable.
func WithMediaStore(ms MediaStore) ManagerOption {
	return func(m *Manager) {
		m.media = ms
	}
}

// NewManager constructs a Manager. The Store is required.
func NewManager(store Store, opts ...ManagerOption) *Manager {
	if store == nil {
		panic("attachments.NewManager: nil store")
	}
	m := &Manager{
		store: store,
		now:   func() time.Time { return time.Now().UTC() },
		idGen: defaultIDGen,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// ScopeFilter narrows the List query.
type ScopeFilter struct {
	// ScopeKind matches against Attachment.ScopeKind. "" matches every
	// scope.
	ScopeKind string
	// ScopeID matches against Attachment.ScopeID. "" matches every id
	// inside the chosen ScopeKind.
	ScopeID string
}

// List returns attachments matching the filter, ordered by Position
// ASC (then by CreatedAt ASC as the tiebreaker).
func (m *Manager) List(ctx context.Context, filter ScopeFilter) ([]Attachment, error) {
	if filter.ScopeKind != "" && !validScopeKind(filter.ScopeKind) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidScope, filter.ScopeKind)
	}
	return m.store.List(ctx, filter)
}

// ListResolved returns the resolved attachment list for sessionID in
// declared injection order:
//
//	[global..., project..., session..., ...]
//
// Attachments inside each scope are ordered by Position ASC. Project
// attachments are spliced when the session is bound to a project (via
// the supplied SessionProjectReader); a loose session sees only global
// + session-scope attachments.
//
// reader may be nil; in that case the project layer is omitted (the
// caller is signalling "no session-to-project lookup available").
func (m *Manager) ListResolved(ctx context.Context, reader SessionProjectReader, sessionID string) ([]Attachment, error) {
	out, err := m.store.List(ctx, ScopeFilter{ScopeKind: ScopeKindGlobal})
	if err != nil {
		return nil, err
	}
	if reader != nil && sessionID != "" {
		projectID, err := reader.ProjectID(ctx, sessionID)
		if err == nil && projectID != nil && *projectID != "" {
			projAtt, err := m.store.List(ctx, ScopeFilter{
				ScopeKind: ScopeKindProject,
				ScopeID:   *projectID,
			})
			if err != nil {
				return nil, err
			}
			out = append(out, projAtt...)
		}
	}
	if sessionID != "" {
		sessAtt, err := m.store.List(ctx, ScopeFilter{
			ScopeKind: ScopeKindSession,
			ScopeID:   sessionID,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, sessAtt...)
	}
	return out, nil
}

// Add inserts a new attachment. ID, CreatedAt, and Position default if
// zero / negative.
//
// TODO(audit-wired): emit a `context.attached` audit event after a
// successful store.Add. Payload: {attachment_id, scope_kind, scope_id,
// content_source, kind, position}. Same wiring blocker as
// projects.Manager.Create — an emitter has to thread through rpc/api.go
// before this Manager can construct one safely.
func (m *Manager) Add(ctx context.Context, att Attachment) (Attachment, error) {
	if !validScopeKind(att.ScopeKind) {
		return Attachment{}, fmt.Errorf("%w: %q", ErrInvalidScope, att.ScopeKind)
	}
	if att.ScopeKind != ScopeKindGlobal && att.ScopeID == "" {
		return Attachment{}, fmt.Errorf("%w: scope_id required for %s", ErrInvalidScope, att.ScopeKind)
	}
	if att.ScopeKind == ScopeKindGlobal && att.ScopeID != "" {
		return Attachment{}, fmt.Errorf("%w: scope_id must be empty for global", ErrInvalidScope)
	}
	if att.Kind == "" {
		att.Kind = KindSystem
	}
	if !validKind(att.Kind) {
		return Attachment{}, fmt.Errorf("%w: %q", ErrInvalidKind, att.Kind)
	}
	if strings.TrimSpace(att.ContentSource) == "" {
		return Attachment{}, ErrInvalidContentSource
	}
	if !validContentSource(att.ContentSource) {
		return Attachment{}, fmt.Errorf("%w: %q", ErrInvalidContentSource, att.ContentSource)
	}
	if att.ID == "" {
		id, err := m.idGen()
		if err != nil {
			return Attachment{}, fmt.Errorf("attachments: id gen: %w", err)
		}
		att.ID = id
	}
	if att.CreatedAt.IsZero() {
		att.CreatedAt = m.now()
	}
	if err := m.store.Add(ctx, att); err != nil {
		return Attachment{}, err
	}
	return att, nil
}

// AddMedia uploads bytes through the MediaStore and inserts a
// matching media-backed Attachment row. Content is left empty (binary
// payload lives in the CAS file); ContentSource is set to
// "media:<artifact-id>" so future readers can resolve back to the
// MediaArtifact. AddMedia is the canonical entry point for image /
// document attachments.
func (m *Manager) AddMedia(ctx context.Context, scopeKind, scopeID string, bytes []byte, mediaType, originalName string) (Attachment, error) {
	if m.media == nil {
		return Attachment{}, ErrMediaStoreUnavailable
	}
	if !validScopeKind(scopeKind) {
		return Attachment{}, fmt.Errorf("%w: %q", ErrInvalidScope, scopeKind)
	}
	if scopeKind != ScopeKindGlobal && scopeID == "" {
		return Attachment{}, fmt.Errorf("%w: scope_id required for %s", ErrInvalidScope, scopeKind)
	}
	if scopeKind == ScopeKindGlobal && scopeID != "" {
		return Attachment{}, fmt.Errorf("%w: scope_id must be empty for global", ErrInvalidScope)
	}
	art, err := m.media.Put(ctx, bytes, mediaType, originalName)
	if err != nil {
		return Attachment{}, err
	}
	mediaID := art.ID
	att := Attachment{
		ScopeKind:     scopeKind,
		ScopeID:       scopeID,
		ContentSource: "media:" + art.ID,
		Content:       "",
		Kind:          KindUser,
		MediaID:       &mediaID,
	}
	return m.Add(ctx, att)
}

// Remove deletes one attachment by id. When the deleted attachment was
// media-backed, Remove also reaps the underlying MediaArtifact: it
// deletes the metadata row, then — if no other reference source still
// points at the content hash — removes the on-disk CAS file. Errors
// from the media-side cleanup are surfaced to the caller, but the
// attachment row deletion is committed first so an attachment is never
// left dangling because the media sweep failed.
func (m *Manager) Remove(ctx context.Context, id string) error {
	att, getErr := m.store.Get(ctx, id)
	if err := m.store.Remove(ctx, id); err != nil {
		return err
	}
	if getErr != nil || m.media == nil || att.MediaID == nil || *att.MediaID == "" {
		return nil
	}
	// Resolve the content hash through the media store, then try to
	// drop the metadata row. After the row is gone, a refcount of zero
	// (no other attachments reference the hash, no other media row
	// references the hash) means we can also remove the file.
	art, _, err := m.media.Get(ctx, *att.MediaID)
	if err != nil && !errors.Is(err, ErrMediaNotFound) {
		return err
	}
	hash := art.ContentHash
	if delErr := m.media.Delete(ctx, *att.MediaID); delErr != nil && !errors.Is(delErr, ErrMediaNotFound) {
		return delErr
	}
	if hash == "" {
		return nil
	}
	count, err := m.media.RefcountFor(ctx, hash)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	// PruneOrphans removes the file when no source references it.
	// Idempotent on second call (nothing left to remove).
	if _, err := m.media.PruneOrphans(ctx); err != nil {
		return err
	}
	return nil
}

// RemoveScope deletes every attachment matching (scopeKind, scopeID)
// via Manager.Remove, so each removal participates in the media
// refcount sweep. Used by the rpc layer's session-delete handler to
// honour A8: deleting a session frees its session-scope attachments
// AND any media artifacts no longer referenced (refcount-driven).
//
// Returns the number of removed rows. A non-nil error stops the walk;
// rows already removed before the error are not rolled back.
func (m *Manager) RemoveScope(ctx context.Context, scopeKind, scopeID string) (int, error) {
	rows, err := m.store.List(ctx, ScopeFilter{
		ScopeKind: scopeKind,
		ScopeID:   scopeID,
	})
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, r := range rows {
		if err := m.Remove(ctx, r.ID); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// Reorder rewrites Position fields so each id in idsInOrder lands at
// its slice index. ids MUST be a permutation of every attachment in
// (scopeKind, scopeID); the store rejects partial permutations.
func (m *Manager) Reorder(ctx context.Context, scopeKind, scopeID string, idsInOrder []string) error {
	if !validScopeKind(scopeKind) {
		return fmt.Errorf("%w: %q", ErrInvalidScope, scopeKind)
	}
	if scopeKind != ScopeKindGlobal && scopeID == "" {
		return fmt.Errorf("%w: scope_id required for %s", ErrInvalidScope, scopeKind)
	}
	return m.store.Reorder(ctx, scopeKind, scopeID, idsInOrder)
}

// Refresh re-reads the source for a "library:<path>" attachment and
// updates Content with the latest snapshot. Inline attachments are a
// no-op (the snapshot is already authoritative).
func (m *Manager) Refresh(ctx context.Context, id string) (Attachment, error) {
	att, err := m.store.Get(ctx, id)
	if err != nil {
		return Attachment{}, err
	}
	libPath, ok := libraryPath(att.ContentSource)
	if !ok {
		return att, nil
	}
	if m.library == nil {
		return Attachment{}, fmt.Errorf("%w: no library reader wired", ErrSourceUnavailable)
	}
	content, err := m.library.Get(ctx, libPath)
	if err != nil {
		return Attachment{}, fmt.Errorf("%w: %v", ErrSourceUnavailable, err)
	}
	att.Content = content
	if err := m.store.UpdateContent(ctx, att.ID, content); err != nil {
		return Attachment{}, err
	}
	return att, nil
}

// validScopeKind reports whether kind is a known ScopeKind*.
func validScopeKind(kind string) bool {
	switch kind {
	case ScopeKindGlobal, ScopeKindProject, ScopeKindSession:
		return true
	}
	return false
}

// validKind reports whether kind is a known Kind*.
func validKind(kind string) bool {
	switch kind {
	case KindSystem, KindUser:
		return true
	}
	return false
}

// validContentSource reports whether src has a recognised scheme.
func validContentSource(src string) bool {
	if strings.HasPrefix(src, "inline:") {
		return len(src) > len("inline:")
	}
	if strings.HasPrefix(src, "library:") {
		return len(src) > len("library:")
	}
	if strings.HasPrefix(src, "media:") {
		// "media:<artifact-id>" — the artifact id is a 26-char ULID
		// minted by MediaStore.Put. Manager.AddMedia constructs this
		// string after a successful Put.
		return len(src) > len("media:")
	}
	if strings.HasPrefix(src, "module:") {
		// "module:<dir-rel-path>" — a context module directory. The
		// directory contains a root file (context.md / agents.md) whose
		// front-matter declares which sibling files to load eagerly.
		// Introduced by unified-context-artifacts-01NCTXU01.
		return len(src) > len("module:")
	}
	return false
}

// libraryPath returns the path portion of a "library:<path>" source,
// or ("", false) if src is not a library source.
func libraryPath(src string) (string, bool) {
	const prefix = "library:"
	if !strings.HasPrefix(src, prefix) {
		return "", false
	}
	return src[len(prefix):], true
}

// defaultIDGen returns a 16-byte hex id (32 chars). Mirrors session and
// projects manager id shapes.
func defaultIDGen() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// staticReader is a small SessionProjectReader convenience for tests
// and the in-rpc adapter; lookup returns the stored mapping or
// (nil, nil) for an unknown session.
type staticReader struct {
	mu sync.RWMutex
	m  map[string]string
}

// NewStaticReader returns a SessionProjectReader backed by the supplied
// session_id -> project_id map. Useful for tests; production wiring
// adapts core/session.Manager.
func NewStaticReader(m map[string]string) SessionProjectReader {
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return &staticReader{m: cp}
}

func (s *staticReader) ProjectID(_ context.Context, sessionID string) (*string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[sessionID]
	if !ok || v == "" {
		return nil, nil
	}
	return &v, nil
}
