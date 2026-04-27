// Package memory defines the MemoryAPI view-scoped accessor that
// surfaces the long-term-memory store to the frontend. The concrete
// implementation lives in impl.go and wraps core/memory.Store +
// core/memory.Embedder so the rpc layer doesn't import the storage
// internals directly (DIRECTIVE_001).
package memory

import (
	"context"
	"time"
)

// Chunk is the wire shape returned to the management UI. Mirrors
// core/memory.Chunk minus the embedding column — the frontend never
// needs the vectors and they should never leave the harness.
type Chunk struct {
	ID            string    `json:"id"`
	SessionID     string    `json:"sessionId,omitempty"`
	ProjectID     string    `json:"projectId,omitempty"`
	ScopeKind     string    `json:"scopeKind"`
	ScopeID       string    `json:"scopeId"`
	SourceTurn    string    `json:"sourceTurn,omitempty"`
	Content       string    `json:"content"`
	ContentHash   string    `json:"contentHash"`
	ToolName      string    `json:"toolName,omitempty"`
	FilesRead     []string  `json:"filesRead,omitempty"`
	FilesModified []string  `json:"filesModified,omitempty"`
	Title         string    `json:"title,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	// Bundle E WP15 — greedy memory metadata.
	Pinned       bool      `json:"pinned,omitempty"`
	RecallCount  int       `json:"recallCount,omitempty"`
	LastAccessed time.Time `json:"lastAccessed,omitempty"`
	Source       string    `json:"source,omitempty"`
}

// ListFilter narrows the chunks returned by ListChunks. Each non-empty
// field acts as a conjunction: ScopeKind="project" AND ScopeID=p1
// returns only chunks pinned to that project. Empty filter returns
// every chunk.
type ListFilter struct {
	ScopeKind string `json:"scopeKind,omitempty"`
	ScopeID   string `json:"scopeId,omitempty"`
}

// JournalEntry is the wire shape for one memory hook journal row
// (Bundle E WP16). Surfaces what the greedy-memory hooks captured so
// the user can audit write-time activity.
type JournalEntry struct {
	Seq         int64     `json:"seq"`
	Boundary    string    `json:"boundary"`
	Scope       string    `json:"scope"`
	Title       string    `json:"title,omitempty"`
	Source      string    `json:"source,omitempty"`
	Written     bool      `json:"written"`
	Deduped     bool      `json:"deduped"`
	Skipped     bool      `json:"skipped"`
	SkipReason  string    `json:"skipReason,omitempty"`
	ChunkID     string    `json:"chunkId,omitempty"`
	ContentHash string    `json:"contentHash,omitempty"`
	At          time.Time `json:"at"`
}

// PruneStats is the wire shape for one prune-sweep summary
// (Bundle E WP15).
type PruneStats struct {
	StartedAt time.Time `json:"startedAt"`
	DurationMs int64    `json:"durationMs"`
	Kept      int       `json:"kept"`
	Dropped   int       `json:"dropped"`
	Collapsed int       `json:"collapsed"`
	Pinned    int       `json:"pinned"`
}

// PruneVerdict is the wire shape for one chunk's prune-sweep verdict.
type PruneVerdict struct {
	ID            string  `json:"id"`
	Action        string  `json:"action"`
	Reason        string  `json:"reason,omitempty"`
	KeepScore     float64 `json:"keepScore"`
	CollapsedInto string  `json:"collapsedInto,omitempty"`
}

// PrunePreview is the dry-run output. The frontend renders the
// would-prune set so the user can review before clicking "Apply".
type PrunePreview struct {
	Verdicts []PruneVerdict `json:"verdicts"`
	Stats    PruneStats     `json:"stats"`
}

// MemoryAPI is the view-scoped accessor exposed via HarnessAPI.
//
// The hooks-driven architecture handles automatic persistence (via the
// memory.persist post_send builtin), but the chat surface still ships
// an explicit "📌 remember this" button so users can capture short
// turns the auto-persist length thresholds skip. RememberMessage is
// the binding behind that button.
type MemoryAPI interface {
	// ListChunks returns persisted memories that match the filter,
	// newest first. Empty filter returns every chunk.
	ListChunks(ctx context.Context, filter ListFilter) ([]Chunk, error)
	// RememberMessage embeds the message at messageID inside sessionID
	// and persists it as a new memory chunk under scope. scope must be
	// one of "global", "project", "session"; defaults to "session" when
	// empty. Returns the new chunk's ID.
	RememberMessage(ctx context.Context, sessionID, messageID, scope string) (string, error)
	// PromoteScope moves the chunk with chunkID to (newScopeKind,
	// newScopeID). Move semantics: the original row is deleted, a new
	// row is inserted with a new ID. Returns the new chunk's ID.
	PromoteScope(ctx context.Context, chunkID, newScopeKind, newScopeID string) (string, error)
	// Forget deletes the chunk with the given id.
	Forget(ctx context.Context, id string) error
	// Pin marks a chunk as immune to the prune sweep (Bundle E WP16).
	// pinned=false unpins. Returns ErrStoreUnavailable when the wired
	// store doesn't support pinning.
	Pin(ctx context.Context, id string, pinned bool) error
	// JournalTail returns the most recent N memory hook journal
	// entries. Used by HookJournalView to surface what greedy memory
	// is capturing.
	JournalTail(ctx context.Context, scope string, sinceSeq int64, limit int) ([]JournalEntry, error)
	// PrunePreview computes the prune verdict without mutating the
	// store. The user reviews the result before triggering Apply.
	PrunePreview(ctx context.Context, scope string) (PrunePreview, error)
	// RunPruneNow applies the prune sweep immediately and returns the
	// resulting stats. Bypasses the scheduler's cadence.
	RunPruneNow(ctx context.Context, scope string) (PruneStats, error)
}
