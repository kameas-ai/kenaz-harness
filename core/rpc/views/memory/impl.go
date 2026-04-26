// Concrete MemoryAPI implementation. Wraps core/memory.Store +
// core/memory.Embedder behind the view-scoped surface; the rpc layer
// constructs exactly one instance per process and shares it with the
// hooks subsystem (memory.retrieve / memory.persist builtins) so the
// auto-persist path and the explicit "📌 remember this" path see the
// same on-disk gob file.
//
// The hooks-driven architecture handles automatic persistence based
// on length thresholds; this surface backs the user-driven pin button
// that captures short turns the auto-path would skip.
package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	corememory "github.com/sigil-tech/kaneaz-harness/core/memory"
)

// MessageReader is the slice of session.Manager the impl needs to
// resolve a (sessionID, messageID) pair to its content. The rpc layer
// adapts session.Manager to it; tests pass fakes.
type MessageReader interface {
	ListMessages(ctx context.Context, sessionID string) ([]Message, error)
}

// Message mirrors the role+content+id triple buildMessages cares about;
// kept here so the impl doesn't import core/session directly.
type Message struct {
	ID      string
	Role    string
	Content string
}

// API is the concrete MemoryAPI.
type API struct {
	store    corememory.Store
	embedder corememory.Embedder
	reader   MessageReader
}

// Config bundles dependencies for New. Embedder + Reader are required
// for the explicit RememberMessage path; ListChunks / Forget continue
// working when only Store is wired (the rpc layer's degraded mode).
type Config struct {
	Store    corememory.Store
	Embedder corememory.Embedder
	Reader   MessageReader
}

// New constructs a MemoryAPI.
func New(cfg Config) *API {
	return &API{
		store:    cfg.Store,
		embedder: cfg.Embedder,
		reader:   cfg.Reader,
	}
}

// ErrStoreUnavailable surfaces when the harness booted without a
// working vector store. The chassis still runs (chat works) but the
// memory surface returns this so the UI can render an actionable
// empty state.
var ErrStoreUnavailable = errors.New("memory: store unavailable")

// ErrEmbedderUnavailable mirrors the lower-level error so the rpc
// surface can match without importing core/memory.
var ErrEmbedderUnavailable = corememory.ErrEmbedderUnavailable

// ListChunks returns every chunk newest-first. A nil store yields an
// empty slice so the UI's empty state is the observable behaviour.
func (a *API) ListChunks(ctx context.Context) ([]Chunk, error) {
	if a == nil || a.store == nil {
		return []Chunk{}, nil
	}
	stored, err := a.store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Chunk, 0, len(stored))
	for _, c := range stored {
		out = append(out, Chunk{
			ID:         c.ID,
			SessionID:  c.SessionID,
			SourceTurn: c.SourceTurn,
			Content:    c.Content,
			CreatedAt:  c.CreatedAt,
		})
	}
	return out, nil
}

// RememberMessage persists the message at (sessionID, messageID) as a
// new memory chunk. Privacy: content stays on disk under the harness's
// data dir; the only network call is the embeddings request to the
// configured OpenAI provider.
func (a *API) RememberMessage(ctx context.Context, sessionID, messageID string) (string, error) {
	if a == nil || a.store == nil {
		return "", ErrStoreUnavailable
	}
	if a.embedder == nil {
		return "", ErrEmbedderUnavailable
	}
	if _, ok := a.embedder.(corememory.NoopEmbedder); ok {
		return "", ErrEmbedderUnavailable
	}
	if a.reader == nil {
		return "", errors.New("memory: session reader unwired")
	}
	msgs, err := a.reader.ListMessages(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("memory: load session %s: %w", sessionID, err)
	}
	var target *Message
	for i := range msgs {
		if msgs[i].ID == messageID {
			target = &msgs[i]
			break
		}
	}
	if target == nil {
		return "", fmt.Errorf("memory: message %s not in session %s", messageID, sessionID)
	}
	if target.Content == "" {
		return "", errors.New("memory: cannot remember empty content")
	}
	vecs, err := a.embedder.Embed(ctx, []string{target.Content})
	if err != nil {
		return "", fmt.Errorf("memory: embed: %w", err)
	}
	if len(vecs) == 0 {
		return "", errors.New("memory: embedder returned no vectors")
	}
	id, err := newChunkID()
	if err != nil {
		return "", err
	}
	chunk := corememory.Chunk{
		ID:         id,
		SessionID:  sessionID,
		SourceTurn: target.Role,
		Content:    target.Content,
		Embedding:  vecs[0],
		CreatedAt:  time.Now().UTC(),
	}
	if err := a.store.Add(ctx, chunk); err != nil {
		return "", err
	}
	return id, nil
}

// Forget removes the chunk with id from the store. Bare wrapper around
// Store.Delete so the bindings layer doesn't import core/memory.
func (a *API) Forget(ctx context.Context, id string) error {
	if a == nil || a.store == nil {
		return ErrStoreUnavailable
	}
	return a.store.Delete(ctx, id)
}

// newChunkID returns a 16-byte hex-encoded random id. crypto/rand so
// concurrent Remembers cannot collide and the value stays opaque to the
// frontend.
func newChunkID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("memory: random id: %w", err)
	}
	return "mem-" + hex.EncodeToString(b), nil
}

// Compile-time witness.
var _ MemoryAPI = (*API)(nil)
