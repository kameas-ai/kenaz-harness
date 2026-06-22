// ContextsAPI implementation. Wraps core/contexts.Library so the rpc
// layer doesn't import the on-disk format directly. Empty-library and
// no-library-wired paths are first-class — the chassis must boot even
// when <DataDir>/contexts/ doesn't exist yet.
package contexts

import (
	"context"
	"errors"
	"fmt"
	"strings"

	contextpack "github.com/kameas-ai/kenaz-harness/core/context/pack"
	corecontexts "github.com/kameas-ai/kenaz-harness/core/contexts"
	fleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/logging"
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
	// Module helpers (unified-context-artifacts-01NCTXU01).
	IsModule(dir string) bool
	ParseModule(dir string) (corecontexts.Module, error)
}

// AttachmentAdder is the narrow surface AttachModule uses to create a
// context_attachments row. core/attachments.Manager satisfies it;
// tests can pass a fake.
type AttachmentAdder interface {
	Add(ctx context.Context, att AttachmentInput) (ModuleAttachment, error)
}

// AttachmentInput is the input to AttachmentAdder.Add. Mirrors the
// core/attachments.Attachment fields the caller controls.
type AttachmentInput struct {
	ScopeKind     string
	ScopeID       string
	ContentSource string
	Content       string
	Kind          string
}

// API is the concrete ContextsAPI.
type API struct {
	lib        Library
	syncer     *fleet.ContextGraphSyncer
	attAdder   AttachmentAdder
}

// ErrInvalidModule is returned by AttachModule when the directory exists
// but has no root file (context.md / agents.md).
var ErrInvalidModule = errors.New("contexts: directory has no root file (context.md or agents.md) — not a valid context module")

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

// WithAttachmentAdder wires an AttachmentAdder so AttachModule can
// persist a context_attachments row. When nil, AttachModule returns an
// error rather than panicking — the attachment store is optional at
// view construction time (chassis boots before all subsystems are ready).
func (a *API) WithAttachmentAdder(adder AttachmentAdder) *API {
	a.attAdder = adder
	return a
}

// AttachModule implements ContextsAPI. It reads the module at dirPath
// (root file + always: files), concatenates their content, and creates
// a context_attachments row whose ContentSource is "module:<dirPath>".
//
// The resolved content order is: root file first, then always: files in
// declared order. On-demand files (Others) are NOT included — they are
// reachable via kaneaz__read_context_file at conversation time.
func (a *API) AttachModule(ctx context.Context, scopeKind, scopeID, dirPath string) (ModuleAttachment, error) {
	if a == nil || a.lib == nil {
		return ModuleAttachment{}, ErrLibraryUnavailable
	}
	if !a.lib.IsModule(dirPath) {
		return ModuleAttachment{}, fmt.Errorf("%w: %q", ErrInvalidModule, dirPath)
	}

	mod, err := a.lib.ParseModule(dirPath)
	if err != nil {
		return ModuleAttachment{}, fmt.Errorf("contexts: attach_module: parse module %q: %w", dirPath, err)
	}

	// Collect content: root file then always: files in order.
	type namedContent struct {
		path    string
		content string
	}
	var parts []namedContent

	rootContent, err := a.lib.Get(mod.Root)
	if err != nil {
		return ModuleAttachment{}, fmt.Errorf("contexts: attach_module: read root %q: %w", mod.Root, err)
	}
	parts = append(parts, namedContent{path: mod.Root, content: rootContent})

	for _, ap := range mod.Always {
		content, err := a.lib.Get(ap)
		if err != nil {
			// A declared always: file that can't be read (missing, or a
			// disallowed extension) is a soft error — it must not block
			// attachment — but it is NOT silent: warn so a mistyped or
			// deleted always: entry is diagnosable instead of vanishing.
			logging.L().Warn("contexts.attach_module.always_skipped",
				"module", mod.Root, "file", ap, "err", err.Error())
			continue
		}
		parts = append(parts, namedContent{path: ap, content: content})
	}

	// Concatenate into a single snapshot separated by file-path headers
	// so the model can correlate content back to filenames.
	var sb strings.Builder
	for i, p := range parts {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		fmt.Fprintf(&sb, "<!-- file: %s -->\n", p.path)
		sb.WriteString(p.content)
	}
	combined := sb.String()

	// Persist the attachment row.
	if a.attAdder == nil {
		// No attachment store wired — return a synthetic in-memory record
		// so callers get the resolved content even without persistence.
		// This path is used in tests and CLI-only mode.
		return ModuleAttachment{
			ScopeKind:     scopeKind,
			ScopeID:       scopeID,
			ContentSource: "module:" + dirPath,
			Content:       combined,
			Kind:          "system",
		}, nil
	}

	stored, err := a.attAdder.Add(ctx, AttachmentInput{
		ScopeKind:     scopeKind,
		ScopeID:       scopeID,
		ContentSource: "module:" + dirPath,
		Content:       combined,
		Kind:          "system",
	})
	if err != nil {
		return ModuleAttachment{}, fmt.Errorf("contexts: attach_module: persist attachment: %w", err)
	}
	return stored, nil
}

// Compile-time witness.
var _ ContextsAPI = (*API)(nil)
