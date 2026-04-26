package memory

import (
	"context"
	"errors"
)

// Embedder turns text into vectors the Store can index. The interface
// is small on purpose — swapping providers (OpenAI, a local model,
// a mock for tests) is a one-implementation change.
type Embedder interface {
	Kind() string
	Dimensions() int
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// ErrEmbedderUnavailable is returned when the harness has no working
// embedder configured. Surfaces in the rpc layer as a typed error so
// the UI can render "configure an OpenAI provider to enable memory".
var ErrEmbedderUnavailable = errors.New("memory: embedder unavailable")

// NoopEmbedder is the safe-by-default embedder used when no real one
// is wired. Embed always errors with ErrEmbedderUnavailable so the
// rpc layer can short-circuit "remember this" without persisting an
// unembedded chunk.
type NoopEmbedder struct{}

func (NoopEmbedder) Kind() string                                          { return "noop" }
func (NoopEmbedder) Dimensions() int                                       { return 0 }
func (NoopEmbedder) Embed(_ context.Context, _ []string) ([][]float32, error) {
	return nil, ErrEmbedderUnavailable
}
