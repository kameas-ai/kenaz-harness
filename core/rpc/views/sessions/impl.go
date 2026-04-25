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
	return Session{
		ID:        r.ID,
		Name:      r.Name,
		CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: r.UpdatedAt.UTC().Format(time.RFC3339Nano),
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
