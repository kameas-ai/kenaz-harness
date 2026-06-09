// ContextsAPI implementation. Wraps core/contexts.Library so the rpc
// layer doesn't import the on-disk format directly. Empty-library and
// no-library-wired paths are first-class — the chassis must boot even
// when <DataDir>/contexts/ doesn't exist yet.
package contexts

import (
	"context"
	"errors"
	"fmt"

	contextpack "github.com/sigil-tech/kaneaz-harness/core/context/pack"
	corecontexts "github.com/sigil-tech/kaneaz-harness/core/contexts"
	fleet "github.com/sigil-tech/kaneaz-harness/core/fleet"
)

// Library is the slim interface this view needs. core/contexts.Library
// satisfies it by construction; tests pass fakes.
type Library interface {
	Tree() (corecontexts.Node, error)
	TreeWithOptions(includeHidden bool) (corecontexts.Node, error)
	Get(path string) (string, error)
	Save(path, content string) error
	CreateFolder(path string) error
	Rename(oldPath, newPath string) error
	Delete(path string) error
	RecentlyApplied(limit int) []string
	Root() string
}

// API is the concrete ContextsAPI.
type API struct {
	lib    Library
	syncer *fleet.ContextGraphSyncer
}

// New constructs the view. A nil library is allowed; methods return
// ErrLibraryUnavailable so the frontend renders an empty state instead
// of a confusing "not wired" error from the chassis stub.
func New(lib Library) *API {
	return &API{lib: lib}
}

// WithSyncer wires the fleet context-graph syncer. Safe to call once
// at construction; passing nil clears any previously wired syncer.
// When the syncer is nil the Context_Publish / Context_Promote methods
// return ErrFleetDisabled; Context_SyncStatus returns a zeroed view.
func (a *API) WithSyncer(s *fleet.ContextGraphSyncer) *API {
	a.syncer = s
	return a
}

// ErrLibraryUnavailable indicates the chassis booted without the
// library wired (e.g. an empty DataDir during tests). The frontend's
// empty-state card is the user-visible behaviour.
var ErrLibraryUnavailable = errors.New("contexts: library unavailable")

// List returns the recursive tree converted into the wire shape.
func (a *API) List(_ context.Context) (Node, error) {
	if a == nil || a.lib == nil {
		return Node{Kind: KindFolder}, ErrLibraryUnavailable
	}
	root, err := a.lib.Tree()
	if err != nil {
		return Node{}, err
	}
	return toWire(root), nil
}

// ListAll returns the tree with dotfiles included. Used by the
// "Show hidden" toggle in /contexts.
func (a *API) ListAll(_ context.Context) (Node, error) {
	if a == nil || a.lib == nil {
		return Node{Kind: KindFolder}, ErrLibraryUnavailable
	}
	root, err := a.lib.TreeWithOptions(true)
	if err != nil {
		return Node{}, err
	}
	return toWire(root), nil
}

func (a *API) Get(_ context.Context, path string) (string, error) {
	if a == nil || a.lib == nil {
		return "", ErrLibraryUnavailable
	}
	return a.lib.Get(path)
}

func (a *API) Save(_ context.Context, path, content string) error {
	if a == nil || a.lib == nil {
		return ErrLibraryUnavailable
	}
	return a.lib.Save(path, content)
}

func (a *API) CreateFolder(_ context.Context, path string) error {
	if a == nil || a.lib == nil {
		return ErrLibraryUnavailable
	}
	return a.lib.CreateFolder(path)
}

func (a *API) Rename(_ context.Context, oldPath, newPath string) error {
	if a == nil || a.lib == nil {
		return ErrLibraryUnavailable
	}
	return a.lib.Rename(oldPath, newPath)
}

func (a *API) Delete(_ context.Context, path string) error {
	if a == nil || a.lib == nil {
		return ErrLibraryUnavailable
	}
	return a.lib.Delete(path)
}

func (a *API) RecentlyApplied(_ context.Context, limit int) ([]string, error) {
	if a == nil || a.lib == nil {
		return []string{}, nil
	}
	out := a.lib.RecentlyApplied(limit)
	if out == nil {
		out = []string{}
	}
	return out, nil
}

func (a *API) RootPath(_ context.Context) (string, error) {
	if a == nil || a.lib == nil {
		return "", ErrLibraryUnavailable
	}
	return a.lib.Root(), nil
}

// toWire converts the core node into the rpc-surface node. The two
// shapes happen to be field-identical today; keeping the conversion
// explicit means a future Node-shape divergence (e.g. richer file
// metadata) doesn't silently leak into the wire.
func toWire(in corecontexts.Node) Node {
	out := Node{
		Name:     in.Name,
		Path:     in.Path,
		Kind:     NodeKind(in.Kind),
		Size:     in.Size,
		Modified: in.Modified,
	}
	if len(in.Children) > 0 {
		out.Children = make([]Node, 0, len(in.Children))
		for _, c := range in.Children {
			out.Children = append(out.Children, toWire(c))
		}
	}
	return out
}

// ── Fleet context-graph sync methods ─────────────────────────────────────────

// Context_Publish publishes a local entry to the fleet context graph.
// The request must specify layer = "team" or "org"; personal entries are
// rejected with ErrPersonalLayerNotSyncable.
// Requires a wired ContextGraphSyncer; returns ErrFleetDisabled otherwise.
func (a *API) Context_Publish(ctx context.Context, req ContextPublishRequest) (ContextPublishResult, error) {
	if a == nil || a.syncer == nil {
		return ContextPublishResult{}, fleet.ErrFleetDisabled
	}

	layer := contextpack.Layer(req.Layer)

	entry := fleet.ContextNodeEntry{
		ID:      req.NodeID,
		Layer:   layer,
		Kind:    req.Kind,
		Title:   req.Title,
		Body:    req.Body,
		Version: req.Version,
	}
	if req.TeamID != "" {
		entry.TeamID = &req.TeamID
	}

	result, err := a.syncer.PushEntry(ctx, entry, nil)
	if err != nil {
		return ContextPublishResult{}, fmt.Errorf("contexts: publish: %w", err)
	}
	return ContextPublishResult{
		AcceptedNodes: result.AcceptedNodes,
		AcceptedEdges: result.AcceptedEdges,
		Conflicts:     result.Conflicts,
	}, nil
}

// Context_Promote elevates a team_shared entry to org_shared.
// Requires a wired ContextGraphSyncer; returns ErrFleetDisabled otherwise.
func (a *API) Context_Promote(ctx context.Context, nodeID string) (ContextPromoteResult, error) {
	if a == nil || a.syncer == nil {
		return ContextPromoteResult{}, fleet.ErrFleetDisabled
	}

	result, err := a.syncer.Promote(ctx, nodeID)
	if err != nil {
		return ContextPromoteResult{}, fmt.Errorf("contexts: promote: %w", err)
	}
	return ContextPromoteResult{
		UpdatedNodeID:     result.Node.ID,
		NewClassification: string(result.Node.Classification),
	}, nil
}

// Context_SyncStatus returns a read-only snapshot of the context-graph
// syncer state. Always returns a non-error result; sync problems are
// surfaced in the struct fields.
func (a *API) Context_SyncStatus(_ context.Context) (ContextSyncStatusView, error) {
	if a == nil || a.syncer == nil {
		// Syncer not wired — return zeroed view, no error.
		return ContextSyncStatusView{}, nil
	}
	snap := a.syncer.Status()
	return ContextSyncStatusView{
		Cursor:         snap.Cursor,
		LastPullAt:     snap.LastPullAt,
		LastPullErr:    snap.LastPullErr,
		LastPushErr:    snap.LastPushErr,
		PullCount:      snap.PullCount,
		TeamCapEnabled: snap.TeamCapEnabled,
	}, nil
}

// Compile-time witness.
var _ ContextsAPI = (*API)(nil)
