package fleet_hosted

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/openaiwire"
)

// BearerProvider returns the current fleet Bearer token to be injected into
// outgoing requests. The provider is called per-request so token refreshes
// are transparent.
//
// Returning an empty string or an error results in the Stream call failing
// with an ErrAuth error.
type BearerProvider func() (string, error)

// EnabledFunc is a predicate the registry queries to decide whether the
// adapter is currently active. Called at adapter-resolve time so tier
// changes propagate without restart.
type EnabledFunc func() bool

// Option configures an Adapter at construction time.
type Option func(*Adapter)

// WithHTTPClient overrides the HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(a *Adapter) {
		if c != nil {
			a.base.HTTPClient = c
		}
	}
}

// Adapter is the fleet-hosted inference ProviderAdapter.
// It speaks the OpenAI Chat Completions SSE protocol against
// <FleetBaseURL>/llm/v1/chat/completions with fleet Bearer auth.
//
// The adapter DOES NOT import core/fleet directly. Auth is provided via
// BearerProvider so the adapter remains importable in OSS forks without
// pulling in fleet dependencies. The bearer token is obtained from the fleet
// client's credential store, injected at wiring time by core/rpc/views/settings.
type Adapter struct {
	base      openaiwire.Base
	bearer    BearerProvider
	enabledFn EnabledFunc
	chatURL   string
}

// Compile-time assertion.
var _ llm.ProviderAdapter = (*Adapter)(nil)

// New constructs an Adapter.
//
//   - fleetBaseURL is the fleet server base URL (e.g. "https://fleet.kameas.dev").
//     The chat completions endpoint is derived as <fleetBaseURL>/llm/v1/chat/completions.
//   - bearer is called per-request to obtain the current access token.
//   - enabled is called at resolve time; when it returns false the adapter
//     returns ErrAuth so the registry does not serve it.
func New(fleetBaseURL string, bearer BearerProvider, enabled EnabledFunc, opts ...Option) *Adapter {
	chatURL := strings.TrimRight(fleetBaseURL, "/") + "/llm/v1/chat/completions"
	a := &Adapter{
		base:      openaiwire.NewBase(chatURL),
		bearer:    bearer,
		enabledFn: enabled,
		chatURL:   chatURL,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Kind returns the canonical provider kind "fleet-hosted".
func (a *Adapter) Kind() string { return Kind }

// Enabled reports whether the capability gate currently allows this adapter.
// Returns false when the EnabledFunc is nil (defensive default).
func (a *Adapter) Enabled() bool {
	if a.enabledFn == nil {
		return false
	}
	return a.enabledFn()
}

// Capabilities returns a conservative descriptor for fleet-hosted models.
// The fleet inference proxy forwards to underlying models; we report
// streaming + tool_calling + usage_reporting as always-on, and vision as
// model-dependent.
func (a *Adapter) Capabilities(model string) llm.CapabilityDescriptor {
	return llm.CapabilityDescriptor{
		Provider: Kind,
		Model:    model,
		Supported: map[llm.Capability]bool{
			llm.CapStreaming:      true,
			llm.CapToolCalling:    true,
			llm.CapUsageReporting: true,
		},
		Notes: map[llm.Capability]string{
			llm.CapVision: "depends on model behind fleet proxy",
		},
	}
}

// Stream opens an SSE connection to the fleet inference endpoint and returns
// a llm.Stream. The bearer token is fetched from the BearerProvider; the
// cred parameter from the registry is ignored (fleet manages its own creds).
func (a *Adapter) Stream(ctx context.Context, req llm.GenerationRequest, prof llm.ProviderProfile, _ []byte) (llm.Stream, error) {
	if a.enabledFn != nil && !a.enabledFn() {
		return nil, &llm.ErrAuth{Status: 0, Message: "fleet-hosted: hosted_inference capability not enabled for current tier"}
	}

	if a.bearer == nil {
		return nil, &llm.ErrAuth{Status: 0, Message: "fleet-hosted: no bearer provider configured"}
	}
	token, err := a.bearer()
	if err != nil || token == "" {
		return nil, &llm.ErrAuth{Status: 0, Message: fmt.Sprintf("fleet-hosted: bearer token unavailable: %v", err)}
	}

	chatURL := a.chatURL
	if prof.Endpoint != "" {
		chatURL = prof.Endpoint
	}

	model := prof.Model
	if model == "" && len(req.Messages) > 0 {
		model = "default"
	}

	bodyMap, err := openaiwire.BuildRequestBody(req, model, nil)
	if err != nil {
		return nil, &llm.ErrInvalidRequest{Status: 0, Message: fmt.Sprintf("fleet-hosted: build request: %v", err)}
	}
	body, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, &llm.ErrInvalidRequest{Status: 0, Message: fmt.Sprintf("fleet-hosted: marshal request: %v", err)}
	}

	streamCtx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-streamCtx.Done():
		}
	}()

	httpReq, err := http.NewRequestWithContext(streamCtx, http.MethodPost, chatURL, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, &llm.ErrInvalidRequest{Status: 0, Message: err.Error()}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.base.Client().Do(httpReq)
	if err != nil {
		cancel()
		return nil, &llm.ErrTransient{Status: 0, Message: err.Error(), Cause: err}
	}

	if resp.StatusCode != http.StatusOK {
		bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		cancel()
		return nil, llm.ClassifyStatus(resp.StatusCode, bodySnippet)
	}

	s := &chatStream{
		resp:   resp,
		cancel: cancel,
		events: make(chan llm.StreamEvent, 16),
		done:   make(chan struct{}),
	}
	go s.pump(streamCtx)
	return s, nil
}

// chatStream implements llm.Stream over an SSE HTTP response.
type chatStream struct {
	resp   *http.Response
	cancel context.CancelFunc
	events chan llm.StreamEvent
	done   chan struct{}

	mu        sync.Mutex
	final     llm.Response
	finalErr  error
	cancelled bool
	textBuf   strings.Builder
	usage     llm.Usage
}

// Events returns the channel of streaming chunks.
func (s *chatStream) Events() <-chan llm.StreamEvent { return s.events }

// Cancel terminates the upstream connection. Safe to call repeatedly.
func (s *chatStream) Cancel() error {
	s.mu.Lock()
	s.cancelled = true
	s.mu.Unlock()
	s.cancel()
	if s.resp != nil && s.resp.Body != nil {
		_ = s.resp.Body.Close()
	}
	return nil
}

// Final blocks until the pump finishes and returns the accumulated Response.
func (s *chatStream) Final() (llm.Response, error) {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelled && s.finalErr == nil {
		return llm.Response{}, &llm.ErrCancelled{Reason: "stop"}
	}
	return s.final, s.finalErr
}

// pump reads SSE frames and sends StreamEvents.
func (s *chatStream) pump(ctx context.Context) {
	defer func() {
		close(s.events)
		s.cancel()
		// Build final Response.
		s.mu.Lock()
		if s.finalErr == nil && !s.cancelled {
			s.final = llm.Response{
				Content:      []llm.ContentBlock{{Type: "text", Text: s.textBuf.String()}},
				FinishReason: "stop",
				Usage:        s.usage,
			}
		}
		s.mu.Unlock()
		close(s.done)
		_, _ = io.Copy(io.Discard, s.resp.Body)
		_ = s.resp.Body.Close()
	}()

	_ = openaiwire.ScanSSE(ctx, s.resp.Body, func(raw []byte) bool {
		frame, err := openaiwire.ParseFrame(raw)
		if err != nil || frame == nil {
			return true
		}
		// Handle usage frame.
		if frame.Usage != nil {
			s.mu.Lock()
			s.usage = llm.Usage{
				InputTokens:  frame.Usage.PromptTokens,
				OutputTokens: frame.Usage.CompletionTokens,
			}
			s.mu.Unlock()
		}
		for _, ch := range frame.Choices {
			if ch.Delta.Content != "" {
				s.mu.Lock()
				s.textBuf.WriteString(ch.Delta.Content)
				s.mu.Unlock()
				ev := llm.StreamEvent{
					Kind: llm.StreamText,
					Text: ch.Delta.Content,
				}
				select {
				case s.events <- ev:
				case <-ctx.Done():
					return false
				}
			}
		}
		return true
	})
}
