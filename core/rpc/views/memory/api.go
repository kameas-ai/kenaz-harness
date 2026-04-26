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
	ID         string    `json:"id"`
	SessionID  string    `json:"sessionId,omitempty"`
	SourceTurn string    `json:"sourceTurn,omitempty"`
	Content    string    `json:"content"`
	CreatedAt  time.Time `json:"createdAt"`
}

// MemoryAPI is the view-scoped accessor exposed via HarnessAPI.
//
// The hooks-driven architecture handles automatic persistence (via the
// memory.persist post_send builtin), but the chat surface still ships
// an explicit "📌 remember this" button so users can capture short
// turns the auto-persist length thresholds skip. RememberMessage is
// the binding behind that button.
type MemoryAPI interface {
	// ListChunks returns every persisted memory, newest first.
	ListChunks(ctx context.Context) ([]Chunk, error)
	// RememberMessage embeds the message at messageID inside sessionID
	// and persists it as a new memory chunk. Returns the new chunk's ID.
	RememberMessage(ctx context.Context, sessionID, messageID string) (string, error)
	// Forget deletes the chunk with the given id.
	Forget(ctx context.Context, id string) error
}
