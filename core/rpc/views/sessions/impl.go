package sessions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/attachments"
	"github.com/sigil-tech/kaneaz-harness/core/session"
)

// ErrEmptyContentBlocks is returned by SendMessageWithBlocks when the
// caller passes a nil or empty slice. Mirrors the existing
// AppendMessage behaviour where the manager rejects empty content.
var ErrEmptyContentBlocks = errors.New("rpc/sessions: contentBlocks must be non-empty")

// recordToView projects a session.Record into the wire-shape Session
// the frontend consumes. Lives here (not in core/session) to avoid an
// import cycle: this package imports session, so session must not
// import this package.
//
// Timestamps render as RFC3339Nano UTC for byte-stable JSON.
func recordToView(r session.Record) Session {
	kind := r.ContextKind
	if kind == "" {
		// Sessions persisted before the system_prompt feature landed
		// have an empty kind from the SQL DEFAULT; surface the canonical
		// 'system' so the frontend never sees a third value.
		kind = session.ContextKindSystem
	}
	out := Session{
		ID:           r.ID,
		Name:         r.Name,
		CreatedAt:    r.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:    r.UpdatedAt.UTC().Format(time.RFC3339Nano),
		SystemPrompt: r.SystemPrompt,
		ContextKind:  kind,
	}
	if r.ProjectID != nil {
		out.ProjectID = *r.ProjectID
	}
	return out
}

// managerAPI is the real SessionsAPI implementation: a thin adapter
// from the rpc-layer wire shape to the core/session.Manager. It owns
// no state of its own; the Manager is the single source of truth.
//
// Streaming subscriptions (StartStream / StopStream) are not yet
// wired; they remain best-effort no-ops returning empty subscription
// ids until the streaming mission lands. The CRUD surface — which is
// what the rail UI needs to render — is fully functional.
type managerAPI struct {
	mgr         *session.Manager
	attachments *attachments.Manager // optional; nil falls back to legacy column path
}

// NewManagerAPI returns a SessionsAPI backed by the supplied Manager.
// Manager must be non-nil; callers (typically core/rpc.New) must
// construct one before invoking this.
func NewManagerAPI(mgr *session.Manager) SessionsAPI {
	if mgr == nil {
		panic("rpc/sessions: NewManagerAPI: nil manager")
	}
	return &managerAPI{mgr: mgr}
}

// NewManagerAPIWithAttachments returns a SessionsAPI that drives the
// attachments table for SetSystemPrompt while keeping the legacy
// session.system_prompt column populated for one-release compat.
//
// TODO: remove the legacy column write next mission post-WP04 once the
// frontend reads attachments directly.
func NewManagerAPIWithAttachments(mgr *session.Manager, att *attachments.Manager) SessionsAPI {
	if mgr == nil {
		panic("rpc/sessions: NewManagerAPI: nil manager")
	}
	return &managerAPI{mgr: mgr, attachments: att}
}

// List implements SessionsAPI.
func (a *managerAPI) List(ctx context.Context) ([]Session, error) {
	records, err := a.mgr.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(records))
	for _, r := range records {
		out = append(out, recordToView(r))
	}
	return out, nil
}

// Get implements SessionsAPI.
func (a *managerAPI) Get(ctx context.Context, id string) (Session, error) {
	r, err := a.mgr.Get(ctx, id)
	if err != nil {
		return Session{}, err
	}
	return recordToView(r), nil
}

// Create implements SessionsAPI.
func (a *managerAPI) Create(ctx context.Context, name string) (Session, error) {
	r, err := a.mgr.Create(ctx, name)
	if err != nil {
		return Session{}, err
	}
	return recordToView(r), nil
}

// Rename implements SessionsAPI.
func (a *managerAPI) Rename(ctx context.Context, id, name string) error {
	return a.mgr.Rename(ctx, id, name)
}

// Delete implements SessionsAPI.
//
// When an attachments manager is wired, Delete also drops every
// session-scope attachment via Manager.RemoveScope so refcount-driven
// media artifact cleanup runs as part of the same operation (spec A8:
// session delete prunes session-scope attachments AND any media
// artifacts no longer referenced). Errors from the attachment cleanup
// are surfaced to the caller; the session row is deleted only after
// the cleanup completes.
func (a *managerAPI) Delete(ctx context.Context, id string) error {
	if a.attachments != nil {
		if _, err := a.attachments.RemoveScope(ctx, attachments.ScopeKindSession, id); err != nil {
			return err
		}
	}
	return a.mgr.Delete(ctx, id)
}

// Reorder implements SessionsAPI.
func (a *managerAPI) Reorder(ctx context.Context, ids []string) error {
	return a.mgr.Reorder(ctx, ids)
}

// StartStream implements SessionsAPI. Streaming is not yet wired by
// the harness; returns an empty subscription id so the frontend's
// subscribe/unsubscribe protocol does not error. The streaming
// mission replaces this implementation.
func (a *managerAPI) StartStream(_ context.Context, _ string) (string, error) {
	return "", nil
}

// StopStream implements SessionsAPI. Counterpart to StartStream;
// always succeeds.
func (a *managerAPI) StopStream(_ context.Context, _ string) error {
	return nil
}

// messageToView projects the durable session.Message into the
// rpc-layer wire shape. Tool-call args are coerced to a one-line
// summary; the full structured payload stays in the durable record.
func messageToView(m session.Message) Message {
	out := Message{
		ID:        m.ID,
		SessionID: m.SessionID,
		Role:      string(m.Role),
		Content:   m.Content,
		CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if len(m.ToolCalls) > 0 {
		out.ToolCalls = make([]ToolCall, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:          tc.ID,
				Name:        tc.Name,
				ArgsSummary: "",
			})
		}
	}
	return out
}

// ListMessages implements SessionsAPI.
func (a *managerAPI) ListMessages(ctx context.Context, id string) ([]Message, error) {
	msgs, err := a.mgr.ListMessages(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageToView(m))
	}
	return out, nil
}

// AppendMessage implements SessionsAPI. Persists a single chat turn
// and returns the stored record (with an assigned id + sequence).
func (a *managerAPI) AppendMessage(ctx context.Context, id, role, content string) (Message, error) {
	stored, err := a.mgr.AppendMessage(ctx, id, session.Message{
		Role:    session.Role(role),
		Content: content,
	})
	if err != nil {
		return Message{}, err
	}
	return messageToView(stored), nil
}

// SendMessageWithBlocks implements SessionsAPI. The persisted row uses
// the polymorphic []ContentBlock shape (multimodal-io WP02 added the
// content_json column). Legacy callers stick with AppendMessage; new
// frontend code (WP04) hits this path whenever the user attaches an
// image or document.
//
// The LLM stream itself is NOT triggered here — the chat surface calls
// LLM_StartStream after the user turn lands, mirroring the pre-WP03
// AppendMessage flow.
func (a *managerAPI) SendMessageWithBlocks(ctx context.Context, id string, contentBlocks []ContentBlock) (Message, error) {
	if len(contentBlocks) == 0 {
		return Message{}, ErrEmptyContentBlocks
	}
	stored, err := a.mgr.AppendMessage(ctx, id, session.Message{
		Role:          session.RoleUser,
		ContentBlocks: contentBlocks,
	})
	if err != nil {
		return Message{}, err
	}
	return messageToView(stored), nil
}

// SaveDraft implements SessionsAPI.
func (a *managerAPI) SaveDraft(ctx context.Context, id, draft string) error {
	return a.mgr.SaveDraft(ctx, id, draft)
}

// LoadDraft implements SessionsAPI. The Manager surfaces the draft as
// part of the Record; we round-trip through Get to keep the wire
// shape single-purpose.
func (a *managerAPI) LoadDraft(ctx context.Context, id string) (string, error) {
	r, err := a.mgr.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return r.Draft, nil
}

// SetSystemPrompt implements SessionsAPI.
//
// Post-WP03 this is a thin wrapper over the attachments table — the
// canonical home for session starting context. The legacy
// session.system_prompt column write stays for the one-release compat
// buffer so Mission A's existing code paths (and any old DB rows that
// migration 0301 already seeded) keep working without surprise.
//
// Behaviour:
//   - content == "" with an existing position-0 inline session-scope
//     attachment removes the attachment row.
//   - content != "" upserts a position-0 inline attachment whose
//     content_source is "inline:<sha256(content)>". Any prior
//     position-0 inline attachment for the session is removed first so
//     a re-set always replaces.
//
// TODO: remove the legacy a.mgr.SetSystemPrompt call next mission
// post-WP04 once the frontend reads attachments directly.
func (a *managerAPI) SetSystemPrompt(ctx context.Context, id, content, kind string) error {
	if a.attachments != nil {
		if err := a.upsertSessionAttachment(ctx, id, content, kind); err != nil {
			return err
		}
	}
	return a.mgr.SetSystemPrompt(ctx, id, content, kind)
}

// upsertSessionAttachment removes any existing position-0 inline
// session-scope attachment for sessionID and inserts a new one when
// content is non-empty. Library-source attachments at position 0 are
// preserved — only inline snapshots are owned by the SetSystemPrompt
// shim.
func (a *managerAPI) upsertSessionAttachment(ctx context.Context, sessionID, content, kind string) error {
	existing, err := a.attachments.List(ctx, attachments.ScopeFilter{
		ScopeKind: attachments.ScopeKindSession,
		ScopeID:   sessionID,
	})
	if err != nil {
		return err
	}
	for _, att := range existing {
		if att.Position != 0 {
			continue
		}
		if !startsWithInline(att.ContentSource) {
			continue
		}
		if err := a.attachments.Remove(ctx, att.ID); err != nil {
			return err
		}
	}
	if content == "" {
		return nil
	}
	hash := sha256.Sum256([]byte(content))
	source := "inline:" + hex.EncodeToString(hash[:])
	attKind := attachments.KindSystem
	if kind == session.ContextKindUserSeed {
		attKind = attachments.KindUser
	}
	_, err = a.attachments.Add(ctx, attachments.Attachment{
		ScopeKind:     attachments.ScopeKindSession,
		ScopeID:       sessionID,
		ContentSource: source,
		Content:       content,
		Kind:          attKind,
		Position:      0,
	})
	return err
}

func startsWithInline(src string) bool {
	const prefix = "inline:"
	return len(src) >= len(prefix) && src[:len(prefix)] == prefix
}

// MoveToProject implements SessionsAPI. An empty projectID detaches
// the session (loose).
func (a *managerAPI) MoveToProject(ctx context.Context, id, projectID string) error {
	var p *string
	if projectID != "" {
		v := projectID
		p = &v
	}
	return a.mgr.MoveToProject(ctx, id, p)
}
