package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/event"
)

// Event kinds emitted by the manager. Naming follows the harness
// `session.<verb>` taxonomy in plan §5.1.
const (
	EventKindSessionCreated         = "session.created"
	EventKindSessionRenamed         = "session.renamed"
	EventKindSessionDeleted         = "session.deleted"
	EventKindSessionReordered       = "session.reordered"
	EventKindSessionMessageAppended = "session.message_appended"
	EventKindSessionDraftSaved      = "session.draft_saved"
	EventKindSessionScrollSaved     = "session.scroll_saved"
	EventKindSessionLastActiveBumped = "session.last_active_bumped"
)

// Audit is the narrowed event surface the Manager uses. Unlike
// event.Emitter — which serves as the authoritative log — Audit is a
// best-effort sink: failures are surfaced to a callback but never
// abort the underlying mutation. This avoids coupling chat-rail UX
// latency to the redaction pipeline cost while still recording
// lifecycle events.
//
// In production the Manager wraps an event.Emitter (see EmitterAudit).
// Tests typically pass a recordingAudit that captures every emit for
// inspection.
type Audit interface {
	Emit(ctx context.Context, kind string, payload map[string]any)
}

// EmitterAudit adapts an event.Emitter into the Audit surface.
// Errors from Append surface through onErr (typically the harness
// log) but never propagate to the caller.
type EmitterAudit struct {
	Emitter   event.Emitter
	EmitterID event.EmitterID
	OnErr     func(error)
}

// Emit implements Audit.
func (a *EmitterAudit) Emit(ctx context.Context, kind string, payload map[string]any) {
	if a == nil || a.Emitter == nil {
		return
	}
	eid := a.EmitterID
	if eid == "" {
		eid = "session/manager"
	}
	if _, err := a.Emitter.Append(ctx, event.AppendInput{
		EmitterID: eid,
		Kind:      event.Kind(kind),
		Payload:   payload,
	}); err != nil && a.OnErr != nil {
		a.OnErr(err)
	}
}

// noopAudit is the default audit if none is wired. It silently drops
// every event so callers don't have to nil-check.
type noopAudit struct{}

func (noopAudit) Emit(_ context.Context, _ string, _ map[string]any) {}

// IDGen is the session-id generator. Tests override; production uses
// the default crypto-random hex.
type IDGen func() (string, error)

// Manager is the public facade for session persistence and lifecycle.
// It is safe for concurrent use; per-session FR-002 UI state is
// preserved in the Record (Draft, ScrollPosition).
type Manager struct {
	store Store
	audit Audit
	now   func() time.Time
	idGen IDGen

	posMu sync.Mutex // serialize position assignment on Create
}

// ManagerOption configures a Manager at construction time.
type ManagerOption func(*Manager)

// WithAudit wires an Audit sink. Defaults to a no-op.
func WithAudit(a Audit) ManagerOption {
	return func(m *Manager) {
		if a != nil {
			m.audit = a
		}
	}
}

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

// NewManager constructs a Manager. The Store is required; everything
// else has a sensible default.
func NewManager(store Store, opts ...ManagerOption) *Manager {
	if store == nil {
		panic("session.NewManager: nil store")
	}
	m := &Manager{
		store: store,
		audit: noopAudit{},
		now:   func() time.Time { return time.Now().UTC() },
		idGen: defaultIDGen,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Create allocates a new session with the given display name. The
// session is appended to the end of the list (highest position + 1).
func (m *Manager) Create(ctx context.Context, name string) (Record, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Record{}, ErrInvalidName
	}
	id, err := m.idGen()
	if err != nil {
		return Record{}, fmt.Errorf("session: id gen: %w", err)
	}

	m.posMu.Lock()
	defer m.posMu.Unlock()

	existing, err := m.store.List(ctx)
	if err != nil {
		return Record{}, err
	}
	var nextPos int64
	for _, r := range existing {
		if r.Position+1 > nextPos {
			nextPos = r.Position + 1
		}
	}

	now := m.now()
	rec := Record{
		ID:           id,
		Name:         name,
		CreatedAt:    now,
		UpdatedAt:    now,
		LastActiveAt: now,
		Position:     nextPos,
	}
	if err := m.store.Create(ctx, rec); err != nil {
		return Record{}, err
	}
	m.audit.Emit(ctx, EventKindSessionCreated, map[string]any{
		"session_id": rec.ID,
		"name":       rec.Name,
		"position":   rec.Position,
	})
	return rec, nil
}

// Get returns one session by id.
func (m *Manager) Get(ctx context.Context, id string) (Record, error) {
	return m.store.Get(ctx, id)
}

// List returns every non-archived session in display order.
func (m *Manager) List(ctx context.Context) ([]Record, error) {
	return m.store.List(ctx)
}

// Rename changes a session's display name.
func (m *Manager) Rename(ctx context.Context, id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrInvalidName
	}
	if err := m.store.Rename(ctx, id, name, m.now()); err != nil {
		return err
	}
	m.audit.Emit(ctx, EventKindSessionRenamed, map[string]any{
		"session_id": id,
		"name":       name,
	})
	return nil
}

// Delete removes a session and cascades through to its messages.
func (m *Manager) Delete(ctx context.Context, id string) error {
	if err := m.store.Delete(ctx, id); err != nil {
		return err
	}
	m.audit.Emit(ctx, EventKindSessionDeleted, map[string]any{
		"session_id": id,
	})
	return nil
}

// Reorder rewrites the position field for every session in ids,
// matching their slice index. ids must be a complete permutation of
// the active session set.
func (m *Manager) Reorder(ctx context.Context, ids []string) error {
	if err := m.store.Reorder(ctx, ids, m.now()); err != nil {
		return err
	}
	m.audit.Emit(ctx, EventKindSessionReordered, map[string]any{
		"order": append([]string(nil), ids...),
	})
	return nil
}

// MarkActive bumps last_active_at to the manager's current clock.
// Called when the user focuses or sends to a session (FR-002).
func (m *Manager) MarkActive(ctx context.Context, id string) error {
	now := m.now()
	if err := m.store.UpdateLastActive(ctx, id, now); err != nil {
		return err
	}
	m.audit.Emit(ctx, EventKindSessionLastActiveBumped, map[string]any{
		"session_id": id,
	})
	return nil
}

// SaveDraft persists the unsent input for a session (FR-002). Empty
// strings clear the draft.
func (m *Manager) SaveDraft(ctx context.Context, id, draft string) error {
	if err := m.store.UpdateDraft(ctx, id, draft, m.now()); err != nil {
		return err
	}
	m.audit.Emit(ctx, EventKindSessionDraftSaved, map[string]any{
		"session_id":   id,
		"draft_length": len(draft),
	})
	return nil
}

// SaveScrollPosition pins the rail's scroll offset for a session
// (FR-002).
func (m *Manager) SaveScrollPosition(ctx context.Context, id string, pos int64) error {
	if err := m.store.UpdateScrollPosition(ctx, id, pos, m.now()); err != nil {
		return err
	}
	m.audit.Emit(ctx, EventKindSessionScrollSaved, map[string]any{
		"session_id": id,
		"position":   pos,
	})
	return nil
}

// AppendMessage stores a chat turn against the session and bumps
// last_active_at. Returns the persisted Message with its assigned
// sequence + id.
func (m *Manager) AppendMessage(ctx context.Context, sessionID string, msg Message) (Message, error) {
	if msg.ID == "" {
		id, err := m.idGen()
		if err != nil {
			return Message{}, fmt.Errorf("session: id gen: %w", err)
		}
		msg.ID = id
	}
	msg.SessionID = sessionID
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = m.now()
	}
	stored, err := m.store.AppendMessage(ctx, msg)
	if err != nil {
		return Message{}, err
	}
	// Bump last-active so the rail surfaces fresh activity.
	_ = m.store.UpdateLastActive(ctx, sessionID, stored.CreatedAt)

	m.audit.Emit(ctx, EventKindSessionMessageAppended, map[string]any{
		"session_id": sessionID,
		"message_id": stored.ID,
		"sequence":   stored.Sequence,
		"role":       string(stored.Role),
	})
	return stored, nil
}

// ListMessages returns every message stored against a session in
// sequence order.
func (m *Manager) ListMessages(ctx context.Context, sessionID string) ([]Message, error) {
	return m.store.ListMessages(ctx, sessionID)
}

// defaultIDGen returns a 16-byte hex id (32 chars). Sufficient
// uniqueness for one user's session set; collision check still happens
// at the store layer.
func defaultIDGen() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// ErrEmpty is exported so callers can errors.Is against an empty input
// without taking a hard dep on the internal sentinel.
var ErrEmpty = errors.New("session: input cannot be empty")
