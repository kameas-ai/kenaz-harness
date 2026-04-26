package attachments

import (
	"context"
	"time"

	coreatt "github.com/sigil-tech/kaneaz-harness/core/attachments"
)

// SessionProjectReader resolves the project membership of a session.
// ListResolved uses it to splice in the session's project attachments.
//
// The view layer defines this interface (rather than re-using
// core/attachments.SessionProjectReader directly) so the binding wire
// stays self-describing — the rpc package supplies an adapter against
// the session manager.
type SessionProjectReader interface {
	ProjectID(ctx context.Context, sessionID string) (*string, error)
}

// API is the concrete AttachmentsAPI implementation. It wraps
// *core/attachments.Manager and a SessionProjectReader for project
// resolution.
type API struct {
	mgr    *coreatt.Manager
	reader SessionProjectReader
}

// New constructs the API. mgr is required; reader may be nil — when
// nil, ListResolved omits the project layer (loose-session behaviour).
func New(mgr *coreatt.Manager, reader SessionProjectReader) *API {
	if mgr == nil {
		panic("rpc/attachments: nil manager")
	}
	return &API{mgr: mgr, reader: reader}
}

func attToView(a coreatt.Attachment) Attachment {
	return Attachment{
		ID:            a.ID,
		ScopeKind:     a.ScopeKind,
		ScopeID:       a.ScopeID,
		ContentSource: a.ContentSource,
		Content:       a.Content,
		Kind:          a.Kind,
		Position:      a.Position,
		CreatedAt:     a.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func attsToView(in []coreatt.Attachment) []Attachment {
	out := make([]Attachment, 0, len(in))
	for _, a := range in {
		out = append(out, attToView(a))
	}
	return out
}

// List implements AttachmentsAPI.
func (a *API) List(ctx context.Context, scopeKind, scopeID string) ([]Attachment, error) {
	rows, err := a.mgr.List(ctx, coreatt.ScopeFilter{ScopeKind: scopeKind, ScopeID: scopeID})
	if err != nil {
		return nil, err
	}
	return attsToView(rows), nil
}

// ListResolved implements AttachmentsAPI.
func (a *API) ListResolved(ctx context.Context, sessionID string) ([]Attachment, error) {
	rows, err := a.mgr.ListResolved(ctx, a.bridge(), sessionID)
	if err != nil {
		return nil, err
	}
	return attsToView(rows), nil
}

// Add implements AttachmentsAPI.
func (a *API) Add(ctx context.Context, in AddInput) (Attachment, error) {
	stored, err := a.mgr.Add(ctx, coreatt.Attachment{
		ScopeKind:     in.ScopeKind,
		ScopeID:       in.ScopeID,
		ContentSource: in.ContentSource,
		Content:       in.Content,
		Kind:          in.Kind,
		Position:      in.Position,
	})
	if err != nil {
		return Attachment{}, err
	}
	return attToView(stored), nil
}

// Remove implements AttachmentsAPI.
func (a *API) Remove(ctx context.Context, id string) error {
	return a.mgr.Remove(ctx, id)
}

// Reorder implements AttachmentsAPI.
func (a *API) Reorder(ctx context.Context, scopeKind, scopeID string, idsInOrder []string) error {
	return a.mgr.Reorder(ctx, scopeKind, scopeID, idsInOrder)
}

// Refresh implements AttachmentsAPI.
func (a *API) Refresh(ctx context.Context, id string) (Attachment, error) {
	stored, err := a.mgr.Refresh(ctx, id)
	if err != nil {
		return Attachment{}, err
	}
	return attToView(stored), nil
}

// bridge returns a coreatt.SessionProjectReader-compatible value built
// from a.reader. nil receiver / nil reader yield nil so ListResolved
// skips the project layer.
func (a *API) bridge() coreatt.SessionProjectReader {
	if a == nil || a.reader == nil {
		return nil
	}
	return readerBridge{r: a.reader}
}

type readerBridge struct {
	r SessionProjectReader
}

func (b readerBridge) ProjectID(ctx context.Context, sessionID string) (*string, error) {
	return b.r.ProjectID(ctx, sessionID)
}

// Compile-time witness.
var _ AttachmentsAPI = (*API)(nil)
