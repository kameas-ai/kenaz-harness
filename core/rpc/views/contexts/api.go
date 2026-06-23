// Package contexts is the view-scoped accessor for the Context Library.
//
// The package replaces the original rpc/views/contextview stub. Naming:
// the stdlib's `context` is imported under its plain name so this
// package keeps the descriptive name `contexts` (mirrors core/contexts);
// callers alias it on import — see api.go in core/rpc.
//
// Wire shapes are deliberately small — Node, Recent — so the JSON
// payload that crosses the Wails boundary stays compact even for
// libraries with hundreds of files.
package contexts

import (
	"context"
	"time"

	fleet "github.com/kameas-ai/kenaz-harness/core/fleet"
)

// NodeKind discriminates files from folders in the tree response.
type NodeKind string

const (
	KindFolder NodeKind = "folder"
	KindFile   NodeKind = "file"
)

// Node is a single tree entry. Path is slash-separated and relative to
// the library root. The frontend never sees an absolute path.
type Node struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Kind     NodeKind  `json:"kind"`
	Size     int64     `json:"size,omitempty"`
	Modified time.Time `json:"modified,omitempty"`
	Children []Node    `json:"children,omitempty"`
}

// ContextsAPI is the view-scoped surface backing /contexts.
//
// All path arguments are validated against the library root by the
// implementation; bindings reject ".." / absolute paths / symlink
// escape before any I/O.
type ContextsAPI interface {
	// List returns the recursive tree rooted at the library. An
	// empty library returns a Node with Kind=folder and no children.
	// Dotfiles are filtered.
	List(ctx context.Context) (Node, error)
	// ListAll is like List but surfaces dotfile entries (excluding
	// the chassis-internal .trash dir and .recent.json metadata).
	// The /contexts "Show hidden" toggle calls this when on.
	ListAll(ctx context.Context) (Node, error)
	// Get reads a file's contents as a string.
	Get(ctx context.Context, path string) (string, error)
	// Save writes content to path; creates parent directories.
	Save(ctx context.Context, path, content string) error
	// CreateFolder creates a directory (recursively).
	CreateFolder(ctx context.Context, path string) error
	// Rename moves oldPath to newPath. Both validated.
	Rename(ctx context.Context, oldPath, newPath string) error
	// Delete moves path into <root>/.trash/<ts>-<basename>.
	Delete(ctx context.Context, path string) error
	// RecentlyApplied returns up to limit most-recently-applied
	// paths (LRU-ordered, newest first). WP01 ships the surface;
	// later WPs flip the call sites that mark a path applied.
	RecentlyApplied(ctx context.Context, limit int) ([]string, error)
	// RootPath returns the absolute path of the library root. The
	// shell uses this as a hint ("library at <path>") and the
	// "open in finder" affordance.
	RootPath(ctx context.Context) (string, error)

	// ── Fleet context-graph sync (fleet-context-graph-sync-01NDFSEX17) ──

	// Context_Publish publishes an entry (nodeID) to the fleet context graph.
	// The entry must have layer = team or org. Returns the push result with
	// accepted counts and any version conflicts.
	// Requires CapSharedTeamGraph. Returns ErrFleetDisabled / ErrCapabilityNotInTier
	// when the capability is absent — the frontend hides the affordance in that case.
	Context_Publish(ctx context.Context, req ContextPublishRequest) (ContextPublishResult, error)

	// Context_Promote elevates a team_shared entry to org_shared.
	// Requires CapSharedTeamGraph. Returns ErrCapabilityNotInTier when absent.
	Context_Promote(ctx context.Context, nodeID string) (ContextPromoteResult, error)

	// Context_SyncStatus returns a snapshot of the context graph syncer state:
	// cursor, last pull time, error strings, pull count, and team cap enabled.
	// Always returns a non-error result; sync problems are surfaced in the struct.
	Context_SyncStatus(ctx context.Context) (ContextSyncStatusView, error)

	// Context_Search runs a server-side search over the caller's visible
	// context graph (title+body match in v0). teamID is optional; limit <= 0
	// lets the server pick a default. Returns an empty result (no error) when
	// fleet is disabled / unentitled / signed-out.
	// (harness-fleet-sync-activation-01NSYNC01 gap #5)
	Context_Search(ctx context.Context, query, teamID string, limit int) ([]ContextSearchHitView, error)

	// Context_Export streams the caller's visible context graph as NDJSON
	// (format "jsonl", default) or a gzipped tarball (format "tarball").
	// teamID optionally narrows to a single team. Returns an empty result (no
	// error) when fleet is disabled / unentitled.
	// (harness-fleet-sync-activation-01NSYNC01 gap #5)
	Context_Export(ctx context.Context, teamID, format string) (ContextExportView, error)

	// ── Context module attachment (unified-context-artifacts-01NCTXU01) ──

	// AttachModule creates an attachment for a context module directory.
	//
	// dirPath is the library-relative path of a module directory (a
	// directory containing a context.md or agents.md root file).
	// scopeKind is one of "global", "project", "session"; scopeID is
	// the project or session id (empty for global).
	//
	// The returned ModuleAttachment has ContentSource = "module:<dirPath>"
	// and Content = the concatenated root file + all always:-listed files
	// (the on-demand files are NOT included; they are reached only via the
	// kenaz__read_context_file tool).
	//
	// Returns ErrLibraryUnavailable when the library is not wired,
	// ErrNotFound (wrapped) when dirPath does not exist or has no root
	// file, and ErrInvalidModule when the directory is not a valid module.
	AttachModule(ctx context.Context, scopeKind, scopeID, dirPath string) (ModuleAttachment, error)
}

// ── Wire shapes for context sync RPC ──────────────────────────────────────────

// ContextPublishRequest is the body for Context_Publish.
type ContextPublishRequest struct {
	// NodeID is the stable UUID for this entry (generated by the caller on first publish).
	NodeID string `json:"node_id"`
	// Layer must be "team" or "org" (personal entries cannot be published).
	Layer string `json:"layer"`
	// Kind is the entry kind (guidance, fact, snippet, …).
	Kind string `json:"kind"`
	// Title is the display name (used as merge key in the merge layer).
	Title string `json:"title"`
	// Body is the entry text.
	Body string `json:"body"`
	// TeamID is the fleet team UUID, required for team-layer entries.
	TeamID string `json:"team_id,omitempty"`
	// Version is the client's monotonic version counter.
	Version int `json:"version"`
}

// ContextPublishResult is the response from Context_Publish.
type ContextPublishResult struct {
	AcceptedNodes int `json:"accepted_nodes"`
	AcceptedEdges int `json:"accepted_edges"`
	// Conflicts lists per-node version conflicts (LWW: server version wins).
	Conflicts []fleet.ContextPushConflict `json:"conflicts"`
}

// ContextPromoteResult is the response from Context_Promote.
type ContextPromoteResult struct {
	// UpdatedNodeID is the ID of the promoted node.
	UpdatedNodeID string `json:"updated_node_id"`
	// NewClassification is always "org_shared" in v0.
	NewClassification string `json:"new_classification"`
}

// ContextSyncStatusView is the response from Context_SyncStatus.
type ContextSyncStatusView struct {
	// Cursor is the RFC3339Nano pull cursor (empty before first sync).
	Cursor string `json:"cursor"`
	// LastPullAt is the wall-clock time of the last successful pull, or zero.
	LastPullAt time.Time `json:"last_pull_at"`
	// LastPullErr is the last pull error string (empty on success).
	LastPullErr string `json:"last_pull_err"`
	// LastPushErr is the last push error string (empty on success).
	LastPushErr string `json:"last_push_err"`
	// PullCount is the total number of entries ever received from fleet.
	PullCount int `json:"pull_count"`
	// TeamCapEnabled is true when the team-graph sharing capability is active.
	TeamCapEnabled bool `json:"team_cap_enabled"`
	// Conflicts holds per-node server/client version conflicts from the most
	// recent push (empty when the last push had none). The frontend prompts
	// the user to reconcile each conflicted entry.
	Conflicts []ContextConflictView `json:"conflicts,omitempty"`
}

// ContextConflictView is one per-node version conflict surfaced by
// Context_SyncStatus (server_version vs client_version).
type ContextConflictView struct {
	NodeID        string `json:"node_id"`
	ServerVersion int    `json:"server_version"`
	ClientVersion int    `json:"client_version"`
}

// ContextSearchHitView is one search result from Context_Search.
type ContextSearchHitView struct {
	// NodeID is the matched node's stable UUID.
	NodeID string `json:"node_id"`
	// Title is the matched node's title.
	Title string `json:"title"`
	// Classification is "team_shared" or "org_shared".
	Classification string `json:"classification"`
	// Snippet is an excerpt with the match wrapped in **bold markers**.
	Snippet string `json:"snippet"`
	// Rank is the v0 match-count stand-in for the vector similarity score.
	Rank float64 `json:"rank"`
}

// ContextExportView is the result of Context_Export: the base64-encoded export
// stream plus the server's content type (application/x-ndjson | application/gzip).
// Base64 keeps the (possibly gzipped binary) payload wire-safe over the JSON
// RPC boundary.
type ContextExportView struct {
	// ContentType is the export MIME type.
	ContentType string `json:"content_type"`
	// DataBase64 is the base64-encoded export stream. Empty when fleet is
	// disabled / unentitled.
	DataBase64 string `json:"data_base64"`
	// ByteLen is the decoded length, for the frontend to show a size hint
	// without decoding.
	ByteLen int `json:"byte_len"`
}

// ── Context module / AttachModule ────────────────────────────────────────────

// ModuleAttachment is the wire shape returned by AttachModule. It mirrors
// the context_attachments table row so the frontend can render and manage
// the attachment without a separate List fetch.
//
// JSON field names are identical to those of the attachments view's
// Attachment type so the frontend's existing attachment-list logic
// can handle both shapes without modification.
type ModuleAttachment struct {
	ID            string `json:"id"`
	ScopeKind     string `json:"scopeKind"`
	ScopeID       string `json:"scopeId,omitempty"`
	ContentSource string `json:"contentSource"`
	Content       string `json:"content"`
	Kind          string `json:"kind"`
	Position      int    `json:"position"`
	CreatedAt     string `json:"createdAt"`
}
