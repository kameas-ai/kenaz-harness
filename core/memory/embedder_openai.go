// OpenAI-compatible embedder. We mirror core/llm/anthropic's net/http
// shape (no SDK dependency) so dependency surface stays unchanged.
package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const (
	defaultOpenAIEmbeddingsURL = "https://api.openai.com/v1/embeddings"
	defaultOpenAIModel         = "text-embedding-3-small"
	// text-embedding-3-small returns 1536-dim vectors. Hard-coded so
	// callers building empty stores can know the expected width without
	// a network round-trip.
	defaultOpenAIDims = 1536
)

// KeyResolver fetches the plaintext API key for a given OpenAI-kind
// provider. The rpc layer wires this against the credref resolver +
// secrets backend; tests pass a fake.
type KeyResolver func(ctx context.Context) ([]byte, error)

// OpenAIEmbedder is an Embedder backed by an OpenAI-compatible
// /v1/embeddings endpoint. Several provider Kinds reuse this type
// (openai, openrouter, custom_openai_compatible, azure) by overriding
// the endpoint via WithOpenAIEndpoint. Kind() reports the SOURCE
// provider Kind set via WithOpenAISourceKind so the health UI shows
// the actual upstream the user configured rather than the literal
// implementation type.
type OpenAIEmbedder struct {
	httpc       *http.Client
	endpoint    string
	model       string
	dims        int
	resolveKey  KeyResolver
	sourceKind  string
}

// OpenAIOption configures an OpenAIEmbedder.
type OpenAIOption func(*OpenAIEmbedder)

// WithOpenAIHTTPClient overrides the http client (tests use httptest).
func WithOpenAIHTTPClient(c *http.Client) OpenAIOption {
	return func(e *OpenAIEmbedder) {
		if c != nil {
			e.httpc = c
		}
	}
}

// WithOpenAIEndpoint overrides the embeddings URL.
func WithOpenAIEndpoint(u string) OpenAIOption {
	return func(e *OpenAIEmbedder) {
		if u != "" {
			e.endpoint = u
		}
	}
}

// WithOpenAIModel overrides the embedding model id.
func WithOpenAIModel(m string) OpenAIOption {
	return func(e *OpenAIEmbedder) {
		if m != "" {
			e.model = m
		}
	}
}

// WithOpenAISourceKind sets the provider Kind that originated this
// embedder (e.g. "openrouter", "azure", "custom_openai_compatible").
// Reported via Kind() so the health dashboard surfaces the actual
// upstream the user picked, not the literal "openai" implementation.
// Empty string falls back to "openai" for backwards compatibility
// with callers that don't set it.
func WithOpenAISourceKind(k string) OpenAIOption {
	return func(e *OpenAIEmbedder) {
		if k != "" {
			e.sourceKind = k
		}
	}
}

// NewOpenAIEmbedder constructs an OpenAI embedder. resolveKey is
// required; pass NoopEmbedder when no provider is configured.
func NewOpenAIEmbedder(resolveKey KeyResolver, opts ...OpenAIOption) *OpenAIEmbedder {
	e := &OpenAIEmbedder{
		httpc:      &http.Client{}, // no Timeout; ctx drives lifetime
		endpoint:   defaultOpenAIEmbeddingsURL,
		model:      defaultOpenAIModel,
		dims:       defaultOpenAIDims,
		resolveKey: resolveKey,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Kind returns the SOURCE provider Kind (the upstream the user
// configured: "openai", "openrouter", "azure", "custom_openai_compatible").
// Falls back to "openai" when no source was set, matching pre-v0.5.5
// behaviour for callers that build embedders directly without going
// through newEmbedderFromProfiles.
func (e *OpenAIEmbedder) Kind() string {
	if e.sourceKind != "" {
		return e.sourceKind
	}
	return "openai"
}
func (e *OpenAIEmbedder) Dimensions() int { return e.dims }

type embeddingsRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingsResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// Embed posts texts to /v1/embeddings and returns one vector per input
// in the same order. Errors mirror the anthropic adapter's classify
// pattern: HTTP non-2xx becomes a wrapped error including the upstream
// body for support visibility.
func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if e.resolveKey == nil {
		return nil, ErrEmbedderUnavailable
	}
	if len(texts) == 0 {
		return nil, errors.New("memory: empty input")
	}
	cred, err := e.resolveKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("memory: resolve openai key: %w", err)
	}
	defer zeroBytes(cred)
	body, err := json.Marshal(embeddingsRequest{Model: e.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("memory: marshal embeddings request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("memory: build embeddings request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+string(cred))
	resp, err := e.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("memory: embeddings call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("memory: embeddings: HTTP %d: %s", resp.StatusCode, string(buf))
	}
	var doc embeddingsResponse
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("memory: parse embeddings response: %w", err)
	}
	if doc.Error != nil {
		return nil, fmt.Errorf("memory: embeddings: %s", doc.Error.Message)
	}
	out := make([][]float32, len(texts))
	for _, d := range doc.Data {
		if d.Index < 0 || d.Index >= len(texts) {
			continue
		}
		out[d.Index] = d.Embedding
	}
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("memory: embeddings response missing index %d", i)
		}
	}
	if len(out) > 0 && len(out[0]) > 0 {
		// Cache the actual dim so callers reading Dimensions() after a
		// non-default model see the real width.
		e.dims = len(out[0])
	}
	return out, nil
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
