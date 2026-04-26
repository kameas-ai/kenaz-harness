// Long-term memory types. Chunk is the unit the user pins via the chat
// surface's "remember this" button; the embedding stays out of the
// JSON wire shape because the frontend never reads it (it is consumed
// only by the local k-NN search inside core/memory.Store).
package memory

import "time"

// Chunk is one stored memory: an opt-in snippet the user explicitly
// asked the harness to keep across sessions.
type Chunk struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id,omitempty"`
	SourceTurn string    `json:"source_turn,omitempty"`
	Content    string    `json:"content"`
	Embedding  []float32 `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
}

// Result pairs a Chunk with its similarity score against a query
// embedding. Similarity is cosine; values close to 1 mean "highly
// related", values near 0 mean "unrelated".
type Result struct {
	Chunk      Chunk
	Similarity float32
}
