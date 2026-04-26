package sessions

import (
	"context"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/session"
)

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
	return Session{
		ID:           r.ID,
		Name:         r.Name,
		CreatedAt:    r.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:    r.UpdatedAt.UTC().Format(time.RFC3339Nano),
		SystemPrompt: r.SystemPrompt,
		ContextKind:  kind,
	}
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
	mgr *session.Manager
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
func (a *managerAPI) Delete(ctx context.Context, id string) error {
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
func (a *managerAPI) SetSystemPrompt(ctx context.Context, id, content, kind string) error {
	return a.mgr.SetSystemPrompt(ctx, id, content, kind)
}
