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

	"github.com/sigil-tech/kaneaz-harness/core/autonomy"
	"github.com/sigil-tech/kaneaz-harness/core/event"
)

// Event kinds emitted by the manager. Naming follows the harness
// `session.<verb>` taxonomy in plan §5.1.
const (
	EventKindSessionCreated          = "session.created"
	EventKindSessionRenamed          = "session.renamed"
	EventKindSessionDeleted          = "session.deleted"
	EventKindSessionReordered        = "session.reordered"
	EventKindSessionMessageAppended  = "session.message_appended"
	EventKindSessionDraftSaved       = "session.draft_saved"
	EventKindSessionScrollSaved      = "session.scroll_saved"
	EventKindSessionLastActiveBumped = "session.last_active_bumped"
	EventKindSessionSystemPromptSet  = "session.system_prompt_set"
	EventKindSessionMovedToProject   = "session.moved_to_project"
	// EventKindSessionAutoTitled is emitted by AutoTitle (success path),
	// MarkAutoTitleAttempted (failure path), and RequestRetitle.
	EventKindSessionAutoTitled = "session.auto_titled"
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
	hooks SessionHookRunner // optional; nil disables v2 lifecycle hooks

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

// WithSessionHookRunner wires a v2 lifecycle hook runner. When set the
// manager fires session_start and setup events after session creation.
func WithSessionHookRunner(h SessionHookRunner) ManagerOption {
	return func(m *Manager) {
		if h != nil {
			m.hooks = h
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

// Store exposes the underlying persistence handle so cross-mission
// wiring (e.g. core/compaction/wiring) can compose its own adapters
// against the same store the manager already opened. The chat path
// keeps consuming the manager's higher-level methods; this getter is
// only for adapters that need the narrower Store interface (and that
// must NOT bypass the manager's audit emission for mutations the
// manager itself owns).
func (m *Manager) Store() Store {
	if m == nil {
		return nil
	}
	return m.store
}

// Create allocates a new session with the given display name. The
// session is appended to the end of the list (highest position + 1).
func (m *Manager) Create(ctx context.Context, name string) (Record, error) {
	return m.CreateInProject(ctx, name, nil)
}

// CreateInProject is Create with an optional project membership. A nil
// projectID creates a loose session.
func (m *Manager) CreateInProject(ctx context.Context, name string, projectID *string) (Record, error) {
	return m.createInternal(ctx, name, projectID, SessionKindChat)
}

// CreateWithKind is CreateInProject + an explicit session Kind tag. An
// empty kind is normalised to SessionKindChat. Used by the onboarding
// flow (harness-self-mcp-onboarding-01KQ8TDU WP03) to mark the session
// for capability gating in the Cedar policy layer.
func (m *Manager) CreateWithKind(ctx context.Context, name string, projectID *string, kind string) (Record, error) {
	if kind == "" {
		kind = SessionKindChat
	}
	return m.createInternal(ctx, name, projectID, kind)
}

func (m *Manager) createInternal(ctx context.Context, name string, projectID *string, kind string) (Record, error) {
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
		Kind:         kind,
	}
	if projectID != nil {
		v := *projectID
		rec.ProjectID = &v
	}
	if err := m.store.Create(ctx, rec); err != nil {
		return Record{}, err
	}
	m.audit.Emit(ctx, EventKindSessionCreated, map[string]any{
		"session_id": rec.ID,
		"name":       rec.Name,
		"position":   rec.Position,
		"kind":       rec.Kind,
	})

	// v2 lifecycle hook: session_start. Fire after the audit event so the
	// session is visible in the store before hooks run. Errors are non-fatal:
	// the session was created successfully; a broken hook runner must not
	// block session creation.
	if m.hooks != nil {
		projectID := ""
		if rec.ProjectID != nil {
			projectID = *rec.ProjectID
		}
		_, _ = m.hooks.FireSessionStart(ctx, SessionStartRequest{
			SessionID: rec.ID,
			ProjectID: projectID,
		})
	}

	return rec, nil
}

// Get returns one session by id.
func (m *Manager) Get(ctx context.Context, id string) (Record, error) {
	return m.store.Get(ctx, id)
}

// FireSetup fires the setup lifecycle hook for the given session.
// This is a no-op when no hook runner is configured. Callers invoke
// this once, just before the first LLM turn of the session.
func (m *Manager) FireSetup(ctx context.Context, sessionID, projectID, cwd string) (SessionHookResult, error) {
	if m == nil || m.hooks == nil {
		return SessionHookResult{}, nil
	}
	return m.hooks.FireSetup(ctx, sessionID, projectID, cwd)
}

// FireCwdChanged fires the cwd_changed lifecycle hook. Callers invoke
// this when the working directory of a session changes.
func (m *Manager) FireCwdChanged(ctx context.Context, sessionID, oldCWD, newCWD string) (SessionHookResult, error) {
	if m == nil || m.hooks == nil {
		return SessionHookResult{}, nil
	}
	return m.hooks.FireCwdChanged(ctx, sessionID, oldCWD, newCWD)
}

// List returns every non-archived session in display order.
func (m *Manager) List(ctx context.Context) ([]Record, error) {
	return m.store.List(ctx)
}

// ListByProject returns the non-archived sessions belonging to the
// given project, in display order.
func (m *Manager) ListByProject(ctx context.Context, projectID string) ([]Record, error) {
	return m.store.ListByProject(ctx, projectID)
}

// MoveToProject sets the session's project membership. A nil projectID
// detaches the session and makes it loose. Errors when the session
// does not exist.
func (m *Manager) MoveToProject(ctx context.Context, sessionID string, projectID *string) error {
	if err := m.store.SetProject(ctx, sessionID, projectID, m.now()); err != nil {
		return err
	}
	payload := map[string]any{
		"session_id": sessionID,
	}
	if projectID != nil {
		payload["project_id"] = *projectID
	} else {
		payload["project_id"] = nil
	}
	m.audit.Emit(ctx, EventKindSessionMovedToProject, payload)
	return nil
}

// SetBranchAdvisorDismissed persists the per-session "don't suggest
// again" flag for the branch advisor. When dismissed is true the
// backend skips detection for this session (FR-010 resolution step 4).
func (m *Manager) SetBranchAdvisorDismissed(ctx context.Context, id string, dismissed bool) error {
	return m.store.SetBranchAdvisorDismissed(ctx, id, dismissed, m.now())
}

// EventKindSessionKindTransitioned is emitted when a session's Kind column
// changes. Used by the onboarding FSM when it reaches its terminal state
// and transitions the session from "onboarding" → "chat" (WP09).
const EventKindSessionKindTransitioned = "session.kind_transitioned"

// SetKind updates a session's Kind and emits an audit event. Used by the
// onboarding FSM to transition the session from "onboarding" → "chat" on
// terminal state (harness-self-mcp-onboarding-01KQ8TDU WP09).
func (m *Manager) SetKind(ctx context.Context, id, kind string) error {
	if err := m.store.SetKind(ctx, id, kind); err != nil {
		return err
	}
	m.audit.Emit(ctx, EventKindSessionKindTransitioned, map[string]any{
		"session_id": id,
		"kind":       kind,
	})
	return nil
}

// SetLastUsage persists the per-turn usage snapshot for the session.
// Called by the chat runner's UsageHook after every completed LLM turn
// (backend-context-window-length-01KQ8TD3 WP02).
func (m *Manager) SetLastUsage(ctx context.Context, id string, u LastUsage) error {
	return m.store.SetLastUsage(ctx, id, u)
}

// GetLastUsage loads the most-recently-persisted usage snapshot.
// Returns a zero LastUsage (not an error) when no turn has completed yet.
func (m *Manager) GetLastUsage(ctx context.Context, id string) (LastUsage, error) {
	return m.store.GetLastUsage(ctx, id)
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

// SetAutonomyProfile persists the per-session autonomy.Layer
// (autonomy-dial-01KR3M2A WP03 RPC plumbing). An empty Layer (nil
// Level + empty Overrides) clears the columns so the resolver folds
// through to the project / global / tier-default chain.
func (m *Manager) SetAutonomyProfile(ctx context.Context, id string, layer autonomy.Layer) error {
	return m.store.SetAutonomyProfile(ctx, id, layer)
}

// GetAutonomyProfile loads the per-session autonomy.Layer.
func (m *Manager) GetAutonomyProfile(ctx context.Context, id string) (autonomy.Layer, error) {
	return m.store.GetAutonomyProfile(ctx, id)
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

// GetMessage returns one message by id within a session. Used by the
// resume RPC to load the partial assistant row before reconstructing
// the continuation prompt (long-turn-resilience-01KR3PRS WP03).
func (m *Manager) GetMessage(ctx context.Context, sessionID, messageID string) (Message, error) {
	return m.store.GetMessage(ctx, sessionID, messageID)
}

// MarkStreamingFailure stamps the resume metadata on an assistant row
// after a stream drop. Best-effort audit emission so a failed write to
// the audit pipeline does not abort the underlying mutation.
//
// long-turn-resilience-01KR3PRS WP03.
func (m *Manager) MarkStreamingFailure(ctx context.Context, sessionID, messageID string,
	kind string, recoverable bool) error {
	if err := m.store.MarkStreamingFailure(ctx, sessionID, messageID, m.now(), kind, recoverable); err != nil {
		return err
	}
	m.audit.Emit(ctx, EventKindSessionMessageAppended, map[string]any{
		"session_id":             sessionID,
		"message_id":             messageID,
		"streaming_failure_kind": kind,
		"streaming_recoverable":  recoverable,
	})
	return nil
}

// AppendContinuation persists a continuation assistant row produced by
// the Sessions_ResumeMessage RPC. Mirrors AppendMessage semantics
// (assigns id + sequence + created_at when zero) and stamps the
// continuation_of pointer onto the new row.
//
// long-turn-resilience-01KR3PRS WP03.
func (m *Manager) AppendContinuation(ctx context.Context, sessionID, originalID string, msg Message) (Message, error) {
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
	stored, err := m.store.AppendContinuation(ctx, originalID, msg)
	if err != nil {
		return Message{}, err
	}
	_ = m.store.UpdateLastActive(ctx, sessionID, stored.CreatedAt)
	m.audit.Emit(ctx, EventKindSessionMessageAppended, map[string]any{
		"session_id":      sessionID,
		"message_id":      stored.ID,
		"sequence":        stored.Sequence,
		"role":            string(stored.Role),
		"continuation_of": originalID,
	})
	return stored, nil
}

// ListMessagesActive returns only the messages whose ArchivedAt is nil
// — i.e. the live scrollback view, with soft-archived originals folded
// into a summary hidden. Used by the compaction-strategy-ui WP07 RPC
// when the user has not toggled "Show full history".
func (m *Manager) ListMessagesActive(ctx context.Context, sessionID string) ([]Message, error) {
	return m.store.ListMessagesActive(ctx, sessionID)
}

// SetSystemPrompt persists the per-session starting context. kind
// MUST be ContextKindSystem (invisible, prepended on every send) or
// ContextKindUserSeed (visible — the caller is expected to AppendMessage
// the seed as a user turn at create time; SetSystemPrompt only records
// the chosen kind for downstream UI).
func (m *Manager) SetSystemPrompt(ctx context.Context, sessionID, content, kind string) error {
	if !validContextKind(kind) {
		return fmt.Errorf("%w: %q", ErrInvalidContextKind, kind)
	}
	if err := m.store.SetSystemPrompt(ctx, sessionID, content, kind, m.now()); err != nil {
		return err
	}
	m.audit.Emit(ctx, EventKindSessionSystemPromptSet, map[string]any{
		"session_id":     sessionID,
		"kind":           kind,
		"content_length": len(content),
	})
	return nil
}

// AutoTitle writes a generated title and sets auto_titled=1 on the
// session, but only when auto_titled is currently 0. If the row's
// auto_titled flag is already 1 — either because the engine already ran
// or a user manually renamed — ErrAutoTitleSuperseded is returned and no
// write occurs. The predicate check and the write happen inside the same
// transaction in the store layer (race-safe).
func (m *Manager) AutoTitle(ctx context.Context, id, title string) error {
	if err := m.store.AutoTitle(ctx, id, title, m.now()); err != nil {
		return err
	}
	m.audit.Emit(ctx, EventKindSessionAutoTitled, map[string]any{
		"session_id": id,
		"title":      title,
		"trigger":    "auto",
	})
	return nil
}

// MarkAutoTitleAttempted sets auto_titled=1 without changing the name.
// Used on the failure path so a crashed generator run does not retry
// indefinitely.
func (m *Manager) MarkAutoTitleAttempted(ctx context.Context, id string) error {
	if err := m.store.MarkAutoTitleAttempted(ctx, id, m.now()); err != nil {
		return err
	}
	m.audit.Emit(ctx, EventKindSessionAutoTitled, map[string]any{
		"session_id": id,
		"trigger":    "auto",
		"error_kind": "attempted_no_title",
	})
	return nil
}

// ClearTitle writes name="" and auto_titled=0, re-enabling future
// auto-title attempts. Emits EventKindSessionRenamed so the rail
// refreshes the session row.
func (m *Manager) ClearTitle(ctx context.Context, id string) error {
	if err := m.store.ClearTitle(ctx, id, m.now()); err != nil {
		return err
	}
	m.audit.Emit(ctx, EventKindSessionRenamed, map[string]any{
		"session_id": id,
		"name":       "",
	})
	return nil
}

// RequestRetitle forces a new auto-title run regardless of the current
// auto_titled state. It clears the flag first so AutoTitle's predicate
// check passes, then sets the new title atomically. This is the "Suggest
// new title" manual path — it bypasses the auto_titled guard intentionally.
func (m *Manager) RequestRetitle(ctx context.Context, id, title string) error {
	// Clear first so AutoTitle's predicate (auto_titled == 0) passes.
	if err := m.store.ClearTitle(ctx, id, m.now()); err != nil {
		return err
	}
	if err := m.store.AutoTitle(ctx, id, title, m.now()); err != nil {
		return err
	}
	m.audit.Emit(ctx, EventKindSessionAutoTitled, map[string]any{
		"session_id": id,
		"title":      title,
		"trigger":    "manual",
	})
	return nil
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
