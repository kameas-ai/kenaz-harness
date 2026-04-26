// Long-term memory types. Chunk is the unit the user pins via the chat
// surface's "remember this" button; the embedding stays out of the
// JSON wire shape because the frontend never reads it (it is consumed
// only by the local k-NN search inside core/memory.Store).
package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Scope kinds. Mirrors claude-mem's scope dimension: a chunk is either
// global (visible across every session), project-scoped (visible to
// every session inside a Project), or session-scoped (the default —
// visible only to the originating session).
const (
	ScopeKindGlobal  = "global"
	ScopeKindProject = "project"
	ScopeKindSession = "session"
)

// Chunk is one stored memory: an opt-in snippet the user explicitly
// asked the harness to keep across sessions.
type Chunk struct {
	ID            string    `json:"id"`
	SessionID     string    `json:"session_id,omitempty"`
	ProjectID     string    `json:"project_id,omitempty"`
	ScopeKind     string    `json:"scope_kind"`
	ScopeID       string    `json:"scope_id"`
	SourceTurn    string    `json:"source_turn,omitempty"`
	Content       string    `json:"content"`
	ContentHash   string    `json:"content_hash"`
	ToolName      string    `json:"tool_name,omitempty"`
	FilesRead     []string  `json:"files_read,omitempty"`
	FilesModified []string  `json:"files_modified,omitempty"`
	Title         string    `json:"title,omitempty"`
	Embedding     []float32 `json:"-"`
	CreatedAt     time.Time `json:"created_at"`
}

// Result pairs a Chunk with its similarity score against a query
// embedding. Similarity is cosine; values close to 1 mean "highly
// related", values near 0 mean "unrelated".
type Result struct {
	Chunk      Chunk
	Similarity float32
}

// HashContent returns the hex-encoded sha256 of content. Used by the
// store to compute ContentHash on Add when the caller leaves it empty
// and to backfill old gobs that predate the column.
func HashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// ScopeFilter targets chunks by (kind, id) for List / Query / Delete.
// An empty ID matches every chunk of the given kind (used for global,
// where ScopeID is always empty).
type ScopeFilter struct {
	Kind string
	ID   string
}

// matchesScope returns true when c falls under any filter in filters.
// An empty filter slice matches every chunk (no filtering).
func matchesScope(c Chunk, filters []ScopeFilter) bool {
	if len(filters) == 0 {
		return true
	}
	for _, f := range filters {
		if f.Kind != c.ScopeKind {
			continue
		}
		if f.ID == "" || f.ID == c.ScopeID {
			return true
		}
	}
	return false
}
